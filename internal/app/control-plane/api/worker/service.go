package worker

import (
	"context"
	"database/sql"
	"fmt"
	"sync"

	"connectrpc.com/connect"
	openseerv1 "github.com/crisog/openseer/gen/openseer/v1"
	"github.com/crisog/openseer/gen/openseer/v1/openseerv1connect"
	controlplane "github.com/crisog/openseer/internal/app/control-plane"
	workermetrics "github.com/crisog/openseer/internal/app/control-plane/metrics"
	"github.com/crisog/openseer/internal/app/control-plane/store/sqlc"
	"github.com/crisog/openseer/internal/pkg/auth"
	"github.com/crisog/openseer/internal/pkg/regions"
	"go.uber.org/zap"
)

type WorkerService struct {
	openseerv1connect.UnimplementedWorkerServiceHandler
	queries    *sqlc.Queries
	logger     *zap.Logger
	dispatcher *controlplane.Dispatcher
	ingest     *workermetrics.Ingest
	auth       *auth.AuthService
}

type ContextKey string

const WorkerIDContextKey ContextKey = "worker_id"

func NewWorkerService(queries *sqlc.Queries, logger *zap.Logger, dispatcher *controlplane.Dispatcher, ingest *workermetrics.Ingest, auth *auth.AuthService) *WorkerService {
	return &WorkerService{
		queries:    queries,
		logger:     logger,
		dispatcher: dispatcher,
		ingest:     ingest,
		auth:       auth,
	}
}

func (s *WorkerService) WorkerStream(
	ctx context.Context,
	stream *connect.BidiStream[openseerv1.WorkerMessage, openseerv1.ServerMessage],
) error {
	var workerID string
	if v := ctx.Value(WorkerIDContextKey); v != nil {
		if s, ok := v.(string); ok {
			workerID = s
		}
	}
	if workerID == "" {
		s.logger.Error("No worker ID found in context")
		return connect.NewError(connect.CodeUnauthenticated, fmt.Errorf("client certificate required"))
	}

	safeStream := &connectStreamWrapper{stream: stream}

	defer func() {
		s.dispatcher.UnregisterWorker(workerID)
		s.logger.Info("Worker disconnected and unregistered", zap.String("worker_id", workerID))
	}()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		msg, err := stream.Receive()
		if err != nil {
			s.logger.Error("Stream receive error", zap.String("worker_id", workerID), zap.Error(err))
			return err
		}

		if err := s.handleWorkerMessage(ctx, workerID, safeStream, msg); err != nil {
			s.logger.Error("Error handling worker message", zap.String("worker_id", workerID), zap.Error(err))
			return err
		}
	}
}

func (s *WorkerService) handleWorkerMessage(ctx context.Context, workerID string, stream controlplane.WorkerStream, msg *openseerv1.WorkerMessage) error {
	switch m := msg.Message.(type) {
	case *openseerv1.WorkerMessage_Register:
		return s.handleRegister(ctx, workerID, stream, m.Register)
	case *openseerv1.WorkerMessage_JobRequest:
		return s.handleJobRequest(ctx, workerID, stream, m.JobRequest)
	case *openseerv1.WorkerMessage_Result:
		return s.handleResult(ctx, workerID, stream, m.Result)
	case *openseerv1.WorkerMessage_LeaseRenewal:
		return s.handleLeaseRenewal(ctx, workerID, m.LeaseRenewal)
	case *openseerv1.WorkerMessage_Pong:
		return s.handlePong(ctx, workerID)
	default:
		return fmt.Errorf("unhandled worker message type: %T", m)
	}
}

func (s *WorkerService) handleRegister(ctx context.Context, workerID string, stream controlplane.WorkerStream, req *openseerv1.RegisterRequest) error {
	worker, err := s.queries.GetWorkerByID(ctx, workerID)
	if err != nil {
		s.logger.Error("Worker not enrolled", zap.String("worker_id", workerID), zap.Error(err))
		return connect.NewError(connect.CodePermissionDenied, fmt.Errorf("worker not enrolled"))
	}

	if worker.Status == "revoked" {
		s.logger.Warn("Revoked worker attempted to connect", zap.String("worker_id", workerID))
		return connect.NewError(connect.CodePermissionDenied, fmt.Errorf("worker enrollment revoked"))
	}

	normalizedRegion := regions.Normalize(req.Region)
	if normalizedRegion != worker.Region {
		s.logger.Info("Worker region updated",
			zap.String("worker_id", workerID),
			zap.String("previous_region", worker.Region),
			zap.String("new_region", normalizedRegion))
	}

	if _, err := s.queries.RegisterWorker(ctx, &sqlc.RegisterWorkerParams{
		ID:      workerID,
		Region:  normalizedRegion,
		Version: req.WorkerVersion,
	}); err != nil {
		s.logger.Error("Failed to mark worker active", zap.String("worker_id", workerID), zap.Error(err))
	}

	s.dispatcher.UnregisterWorker(workerID)
	s.dispatcher.RegisterWorker(workerID, normalizedRegion, stream)

	s.logger.Info("Worker registered via Connect",
		zap.String("worker_id", workerID),
		zap.String("region", normalizedRegion),
		zap.String("version", req.WorkerVersion))

	return stream.Send(&openseerv1.ServerMessage{
		Message: &openseerv1.ServerMessage_Registered{
			Registered: &openseerv1.RegisterResponse{
				WorkerId: workerID,
				Accepted: true,
			},
		},
	})
}

func (s *WorkerService) handleJobRequest(ctx context.Context, workerID string, stream controlplane.WorkerStream, req *openseerv1.JobRequest) error {
	if err := s.queries.UpdateWorkerHeartbeat(ctx, workerID); err != nil {
		s.logger.Error("Error updating worker heartbeat", zap.String("worker_id", workerID), zap.Error(err))
	}

	s.logger.Debug("Worker requested jobs",
		zap.String("worker_id", workerID),
		zap.Int32("count", req.Count))

	if req.Count > 0 {
		jobs, err := s.dispatcher.LeaseJobs(ctx, workerID, req.Count)
		if err != nil {
			s.logger.Error("Error leasing jobs for worker", zap.String("worker_id", workerID), zap.Error(err))
			return nil
		}

		for _, job := range jobs {
			if err := stream.Send(&openseerv1.ServerMessage{
				Message: &openseerv1.ServerMessage_Job{Job: job},
			}); err != nil {
				s.logger.Error("Error sending job to worker", zap.String("worker_id", workerID), zap.Error(err))
				s.dispatcher.HandleStreamFailure(ctx, workerID)
				return err
			}
			s.logger.Info("Sent job to worker via Connect",
				zap.String("job_id", job.RunId),
				zap.String("worker_id", workerID))
		}

		if len(jobs) > 0 {
			s.logger.Info("Dispatched jobs to worker",
				zap.String("worker_id", workerID),
				zap.Int("jobs_sent", len(jobs)),
				zap.Int32("jobs_requested", req.Count))
		}
	}
	return nil
}

func (s *WorkerService) handleResult(ctx context.Context, workerID string, stream controlplane.WorkerStream, result *openseerv1.MonitorResult) error {
	s.logger.Info("Worker result received via Connect",
		zap.String("worker_id", workerID),
		zap.String("run_id", result.RunId),
		zap.String("status", result.Status))

	committed := false
	if err := s.ingest.ProcessResult(ctx, result); err != nil {
		s.logger.Error("Error storing result", zap.String("run_id", result.RunId), zap.Error(err))
	} else if err := s.dispatcher.CompleteJob(ctx, workerID, result.RunId); err != nil {
		s.logger.Error("Error completing job", zap.String("run_id", result.RunId), zap.Error(err))
	} else {
		committed = true
	}

	if err := stream.Send(&openseerv1.ServerMessage{
		Message: &openseerv1.ServerMessage_Ack{
			Ack: &openseerv1.ResultAck{
				RunId:     result.RunId,
				Committed: committed,
			},
		},
	}); err != nil {
		return err
	}

	if committed {
		s.logger.Info("Result committed and acked via Connect", zap.String("run_id", result.RunId))
	} else {
		s.logger.Warn("Failed to commit result, sent negative ACK", zap.String("run_id", result.RunId))
	}
	return nil
}

func (s *WorkerService) handleLeaseRenewal(ctx context.Context, workerID string, req *openseerv1.LeaseRenewal) error {
	_, err := s.queries.RenewLease(ctx, &sqlc.RenewLeaseParams{
		RunID:    req.RunId,
		WorkerID: sql.NullString{String: workerID, Valid: true},
	})
	if err != nil {
		s.logger.Error("Failed to renew lease",
			zap.String("run_id", req.RunId),
			zap.String("worker_id", workerID),
			zap.Error(err))
	} else {
		s.logger.Debug("Renewed lease via Connect",
			zap.String("run_id", req.RunId),
			zap.String("worker_id", workerID))
	}
	return nil
}

func (s *WorkerService) handlePong(ctx context.Context, workerID string) error {
	s.dispatcher.HandlePong(workerID)
	s.logger.Debug("Received pong from worker", zap.String("worker_id", workerID))
	if err := s.queries.UpdateWorkerHeartbeat(ctx, workerID); err != nil {
		s.logger.Error("Error updating worker heartbeat after pong",
			zap.String("worker_id", workerID), zap.Error(err))
	}
	return nil
}

type connectStreamWrapper struct {
	stream *connect.BidiStream[openseerv1.WorkerMessage, openseerv1.ServerMessage]
	mu     sync.Mutex
}

func (w *connectStreamWrapper) Send(msg *openseerv1.ServerMessage) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.stream.Send(msg)
}
