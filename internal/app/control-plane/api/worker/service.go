package worker

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"

	"connectrpc.com/connect"
	openseerv1 "github.com/crisog/openseer/gen/openseer/v1"
	"github.com/crisog/openseer/gen/openseer/v1/openseerv1connect"
	workermetrics "github.com/crisog/openseer/internal/app/control-plane/metrics"
	"github.com/crisog/openseer/internal/app/control-plane/middleware"
	"github.com/crisog/openseer/internal/app/control-plane/store/sqlc"
	"github.com/sqlc-dev/pqtype"
	"go.uber.org/zap"
)

type WorkerService struct {
	openseerv1connect.UnimplementedWorkerServiceHandler
	queries               *sqlc.Queries
	logger                *zap.Logger
	ingest                *workermetrics.Ingest
	leaseDuration         time.Duration
	heartbeatWriteTracker *heartbeatTracker
}

func NewWorkerService(
	queries *sqlc.Queries,
	logger *zap.Logger,
	ingest *workermetrics.Ingest,
	leaseDuration time.Duration,
	heartbeatMinInterval time.Duration,
) *WorkerService {
	if leaseDuration <= 0 {
		leaseDuration = 45 * time.Second
	}
	if heartbeatMinInterval <= 0 {
		heartbeatMinInterval = 15 * time.Second
	}
	return &WorkerService{
		queries:               queries,
		logger:                logger,
		ingest:                ingest,
		leaseDuration:         leaseDuration,
		heartbeatWriteTracker: newHeartbeatTracker(heartbeatMinInterval),
	}
}

func (s *WorkerService) GetJobs(
	ctx context.Context,
	req *connect.Request[openseerv1.GetJobsRequest],
) (*connect.Response[openseerv1.GetJobsResponse], error) {
	workerID, ok := middleware.WorkerIDFromContext(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, nil)
	}

	s.touchWorkerHeartbeat(ctx, workerID)

	worker, err := s.queries.GetWorkerByID(ctx, workerID)
	if err != nil {
		s.logger.Error("Worker not found", zap.String("worker_id", workerID), zap.Error(err))
		return nil, connect.NewError(connect.CodeNotFound, nil)
	}

	maxJobs := req.Msg.MaxJobs
	if maxJobs <= 0 {
		maxJobs = 1
	}

	leaseExpiresAt := time.Now().Add(s.leaseDuration)

	jobs, err := s.queries.LeaseJobsWithMonitorData(ctx, &sqlc.LeaseJobsWithMonitorDataParams{
		WorkerID: sql.NullString{String: workerID, Valid: true},
		Limit:    maxJobs,
		Region:   worker.Region,
		LeaseExpiresAt: sql.NullTime{
			Time:  leaseExpiresAt,
			Valid: true,
		},
	})
	if err != nil {
		s.logger.Error("Failed to lease jobs", zap.String("worker_id", workerID), zap.Error(err))
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	apiJobs := make([]*openseerv1.MonitorJob, 0, len(jobs))
	for _, job := range jobs {
		apiJobs = append(apiJobs, &openseerv1.MonitorJob{
			RunId:     job.RunID,
			MonitorId: job.MonitorID,
			Url:       job.Url,
			TimeoutMs: job.TimeoutMs,
			Method:    job.Method,
			Headers:   convertHeaders(job.Headers),
		})
	}

	if len(apiJobs) > 0 {
		s.logger.Info("Leased jobs to worker",
			zap.String("worker_id", workerID),
			zap.Int("count", len(apiJobs)))
	}

	return connect.NewResponse(&openseerv1.GetJobsResponse{
		Jobs: apiJobs,
	}), nil
}

func (s *WorkerService) SubmitResult(
	ctx context.Context,
	req *connect.Request[openseerv1.SubmitResultRequest],
) (*connect.Response[openseerv1.SubmitResultResponse], error) {
	workerID, ok := middleware.WorkerIDFromContext(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, nil)
	}

	s.touchWorkerHeartbeat(ctx, workerID)

	result := req.Msg.Result
	if result == nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, nil)
	}

	s.logger.Info("Received result from worker",
		zap.String("worker_id", workerID),
		zap.String("run_id", result.RunId),
		zap.String("status", result.Status))

	committed := false
	if err := s.ingest.ProcessResult(ctx, result); err != nil {
		s.logger.Error("Failed to store result", zap.String("run_id", result.RunId), zap.Error(err))
	} else if err := s.completeJob(ctx, workerID, result.RunId); err != nil {
		s.logger.Error("Failed to complete job", zap.String("run_id", result.RunId), zap.Error(err))
	} else {
		committed = true
	}

	if committed {
		s.logger.Info("Result committed", zap.String("run_id", result.RunId))
	} else {
		s.logger.Warn("Failed to commit result", zap.String("run_id", result.RunId))
	}

	return connect.NewResponse(&openseerv1.SubmitResultResponse{
		Committed: committed,
		RunId:     result.RunId,
	}), nil
}

func (s *WorkerService) RenewLease(
	ctx context.Context,
	req *connect.Request[openseerv1.RenewLeaseRequest],
) (*connect.Response[openseerv1.RenewLeaseResponse], error) {
	workerID, ok := middleware.WorkerIDFromContext(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, nil)
	}

	s.touchWorkerHeartbeat(ctx, workerID)

	leaseExpiresAt := time.Now().Add(s.leaseDuration)

	_, err := s.queries.RenewLease(ctx, &sqlc.RenewLeaseParams{
		RunID:    req.Msg.RunId,
		WorkerID: sql.NullString{String: workerID, Valid: true},
		LeaseExpiresAt: sql.NullTime{
			Time:  leaseExpiresAt,
			Valid: true,
		},
	})

	if err != nil {
		s.logger.Error("Failed to renew lease",
			zap.String("run_id", req.Msg.RunId),
			zap.String("worker_id", workerID),
			zap.Error(err))
		return connect.NewResponse(&openseerv1.RenewLeaseResponse{
			Renewed: false,
		}), nil
	}

	s.logger.Debug("Renewed lease",
		zap.String("run_id", req.Msg.RunId),
		zap.String("worker_id", workerID))

	return connect.NewResponse(&openseerv1.RenewLeaseResponse{
		Renewed: true,
	}), nil
}

func (s *WorkerService) completeJob(ctx context.Context, workerID, runID string) error {
	_, err := s.queries.CompleteJob(ctx, &sqlc.CompleteJobParams{
		RunID:    runID,
		WorkerID: sql.NullString{String: workerID, Valid: true},
	})
	if err != nil {
		if err == sql.ErrNoRows {
			job, getErr := s.queries.GetJobByRunID(ctx, runID)
			if getErr == nil && job.Status == "done" {
				return nil
			}
		}
		return err
	}
	return nil
}

func convertHeaders(headersJSON pqtype.NullRawMessage) map[string]string {
	if !headersJSON.Valid || len(headersJSON.RawMessage) == 0 {
		return make(map[string]string)
	}

	var headers map[string]string
	if err := json.Unmarshal(headersJSON.RawMessage, &headers); err != nil {
		return make(map[string]string)
	}

	return headers
}

func (s *WorkerService) touchWorkerHeartbeat(ctx context.Context, workerID string) {
	if !s.heartbeatWriteTracker.ShouldUpdate(workerID) {
		return
	}

	if err := s.queries.UpdateWorkerHeartbeat(ctx, workerID); err != nil {
		s.logger.Error("Failed to update worker heartbeat", zap.String("worker_id", workerID), zap.Error(err))
		// Allow immediate retry on next RPC after transient failures.
		s.heartbeatWriteTracker.Invalidate(workerID)
	}
}
