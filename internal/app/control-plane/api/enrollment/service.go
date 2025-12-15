package enrollment

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"fmt"
	"time"

	"connectrpc.com/connect"
	openseerv1 "github.com/crisog/openseer/gen/openseer/v1"
	"github.com/crisog/openseer/gen/openseer/v1/openseerv1connect"
	"github.com/crisog/openseer/internal/app/control-plane/store/sqlc"
	"github.com/crisog/openseer/internal/pkg/auth"
	"github.com/crisog/openseer/internal/pkg/regions"
	"go.uber.org/zap"
)

type EnrollmentService struct {
	openseerv1connect.UnimplementedEnrollmentServiceHandler
	queries      *sqlc.Queries
	logger       *zap.Logger
	clusterToken string
	apiEndpoint  string
}

func NewEnrollmentService(queries *sqlc.Queries, logger *zap.Logger, clusterToken, apiEndpoint string) *EnrollmentService {
	return &EnrollmentService{
		queries:      queries,
		logger:       logger,
		clusterToken: clusterToken,
		apiEndpoint:  apiEndpoint,
	}
}

// SetAPIEndpoint updates the advertised worker API endpoint (useful in tests).
func (s *EnrollmentService) SetAPIEndpoint(endpoint string) {
	s.apiEndpoint = endpoint
}

func (s *EnrollmentService) EnrollWorker(
	ctx context.Context,
	req *connect.Request[openseerv1.EnrollWorkerRequest],
) (*connect.Response[openseerv1.EnrollWorkerResponse], error) {
	msg := req.Msg

	if msg.EnrollmentToken == "" {
		return nil, connect.NewError(connect.CodeUnauthenticated, fmt.Errorf("enrollment token is required"))
	}

	if msg.Hostname == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("hostname is required"))
	}

	if msg.WorkerVersion == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("worker version is required"))
	}

	if subtle.ConstantTimeCompare([]byte(msg.EnrollmentToken), []byte(s.clusterToken)) != 1 {
		s.logger.Warn("Invalid enrollment token provided",
			zap.String("worker_hostname", msg.Hostname),
			zap.String("provided_token_length", fmt.Sprintf("%d", len(msg.EnrollmentToken))))
		return nil, connect.NewError(connect.CodeUnauthenticated, fmt.Errorf("invalid enrollment token"))
	}

	workerID, err := generateWorkerID()
	if err != nil {
		s.logger.Error("Failed to generate worker ID", zap.Error(err))
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("failed to generate worker ID"))
	}

	apiToken, tokenHash, err := auth.GenerateAPIToken()
	if err != nil {
		s.logger.Error("Failed to generate API token", zap.Error(err))
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("failed to generate API token"))
	}

	normalizedRegion := regions.Normalize(msg.Region)

	_, err = s.queries.EnrollWorker(ctx, &sqlc.EnrollWorkerParams{
		ID:       workerID,
		Hostname: sql.NullString{String: msg.Hostname, Valid: true},
		Region:   normalizedRegion,
		Version:  msg.WorkerVersion,
	})
	if err != nil {
		s.logger.Error("Failed to enroll worker in database", zap.String("worker_id", workerID), zap.Error(err))
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("failed to enroll worker"))
	}

	err = s.queries.SetWorkerToken(ctx, &sqlc.SetWorkerTokenParams{
		TokenHash: sql.NullString{String: tokenHash, Valid: true},
		ID:        workerID,
	})
	if err != nil {
		s.logger.Error("Failed to set worker token", zap.String("worker_id", workerID), zap.Error(err))
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("failed to set worker token"))
	}

	s.logger.Info("Worker enrolled successfully",
		zap.String("worker_id", workerID),
		zap.String("hostname", msg.Hostname),
		zap.String("region", normalizedRegion),
		zap.String("version", msg.WorkerVersion))

	return connect.NewResponse(&openseerv1.EnrollWorkerResponse{
		WorkerId:    workerID,
		Accepted:    true,
		ApiToken:    apiToken,
		ApiEndpoint: s.apiEndpoint,
	}), nil
}

func (s *EnrollmentService) RenewEnrollment(
	ctx context.Context,
	req *connect.Request[openseerv1.RenewEnrollmentRequest],
) (*connect.Response[openseerv1.RenewEnrollmentResponse], error) {
	msg := req.Msg

	if msg.WorkerId == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("worker ID is required"))
	}

	worker, err := s.queries.GetWorkerByID(ctx, msg.WorkerId)
	if err != nil {
		s.logger.Error("Worker not found", zap.String("worker_id", msg.WorkerId), zap.Error(err))
		return nil, connect.NewError(connect.CodeNotFound, fmt.Errorf("worker not found"))
	}

	if worker.Status == "revoked" {
		return connect.NewResponse(&openseerv1.RenewEnrollmentResponse{
			Renewed: false,
			Reason:  "worker enrollment is revoked",
		}), nil
	}

	apiToken, tokenHash, err := auth.GenerateAPIToken()
	if err != nil {
		s.logger.Error("Failed to generate API token", zap.Error(err))
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("failed to generate API token"))
	}

	err = s.queries.SetWorkerToken(ctx, &sqlc.SetWorkerTokenParams{
		TokenHash: sql.NullString{String: tokenHash, Valid: true},
		ID:        msg.WorkerId,
	})
	if err != nil {
		s.logger.Error("Failed to set worker token", zap.String("worker_id", msg.WorkerId), zap.Error(err))
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("failed to renew token"))
	}

	s.logger.Info("Worker enrollment renewed",
		zap.String("worker_id", msg.WorkerId),
		zap.String("region", worker.Region))

	return connect.NewResponse(&openseerv1.RenewEnrollmentResponse{
		Renewed:  true,
		ApiToken: apiToken,
	}), nil
}

func (s *EnrollmentService) RevokeEnrollment(
	ctx context.Context,
	req *connect.Request[openseerv1.RevokeEnrollmentRequest],
) (*connect.Response[openseerv1.RevokeEnrollmentResponse], error) {
	msg := req.Msg

	if msg.WorkerId == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("worker ID is required"))
	}

	_, err := s.queries.GetWorkerByID(ctx, msg.WorkerId)
	if err != nil {
		s.logger.Error("Worker not found", zap.String("worker_id", msg.WorkerId), zap.Error(err))
		return nil, connect.NewError(connect.CodeNotFound, fmt.Errorf("worker not found"))
	}

	err = s.queries.SetWorkerToken(ctx, &sqlc.SetWorkerTokenParams{
		TokenHash: sql.NullString{Valid: false},
		ID:        msg.WorkerId,
	})
	if err != nil {
		s.logger.Error("Failed to clear worker token", zap.String("worker_id", msg.WorkerId), zap.Error(err))
	}

	err = s.queries.RevokeWorker(ctx, &sqlc.RevokeWorkerParams{
		RevokedReason: sql.NullString{String: msg.Reason, Valid: msg.Reason != ""},
		ID:            msg.WorkerId,
	})
	if err != nil {
		s.logger.Error("Failed to revoke worker", zap.String("worker_id", msg.WorkerId), zap.Error(err))
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("failed to revoke worker: %w", err))
	}

	s.logger.Info("Worker enrollment revoked",
		zap.String("worker_id", msg.WorkerId),
		zap.String("reason", msg.Reason))

	return connect.NewResponse(&openseerv1.RevokeEnrollmentResponse{
		Revoked: true,
		Message: "Worker enrollment revoked successfully",
	}), nil
}

func (s *EnrollmentService) GetWorkerStatus(
	ctx context.Context,
	req *connect.Request[openseerv1.GetWorkerStatusRequest],
) (*connect.Response[openseerv1.GetWorkerStatusResponse], error) {
	msg := req.Msg

	if msg.WorkerId == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("worker ID is required"))
	}

	worker, err := s.queries.GetWorkerByID(ctx, msg.WorkerId)
	if err != nil {
		s.logger.Error("Worker not found", zap.String("worker_id", msg.WorkerId), zap.Error(err))
		return nil, connect.NewError(connect.CodeNotFound, fmt.Errorf("worker not found"))
	}

	status := worker.Status
	if status != "revoked" && time.Since(worker.LastSeenAt) > 5*time.Minute {
		status = "inactive"
	}

	hostname := worker.ID
	if worker.Hostname.Valid {
		hostname = worker.Hostname.String
	}

	capabilities := make(map[string]string)

	return connect.NewResponse(&openseerv1.GetWorkerStatusResponse{
		WorkerId:      worker.ID,
		Status:        status,
		EnrolledAt:    worker.EnrolledAt.Unix(),
		LastSeenAt:    worker.LastSeenAt.Unix(),
		Region:        worker.Region,
		Hostname:      hostname,
		WorkerVersion: worker.Version,
		Capabilities:  capabilities,
	}), nil
}

func (s *EnrollmentService) ListWorkers(
	ctx context.Context,
	req *connect.Request[openseerv1.ListWorkersRequest],
) (*connect.Response[openseerv1.ListWorkersResponse], error) {
	msg := req.Msg

	pageSize := int32(msg.PageSize)
	if pageSize == 0 || pageSize > 100 {
		pageSize = 100
	}

	offset := int32(0)

	region := ""
	status := ""
	if msg.Region != "" {
		region = msg.Region
	}
	if msg.Status != "" {
		status = msg.Status
	}

	workers, err := s.queries.ListWorkers(ctx, &sqlc.ListWorkersParams{
		Column1: region,
		Column2: status,
		Limit:   pageSize,
		Offset:  offset,
	})
	if err != nil {
		s.logger.Error("Failed to list workers", zap.Error(err))
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("failed to list workers"))
	}

	totalCount, err := s.queries.CountWorkers(ctx, &sqlc.CountWorkersParams{
		Column1: region,
		Column2: status,
	})
	if err != nil {
		s.logger.Warn("Failed to get worker count", zap.Error(err))
		totalCount = int64(len(workers))
	}

	workerInfos := make([]*openseerv1.WorkerInfo, 0, len(workers))
	for _, worker := range workers {
		hostname := worker.ID
		if worker.Hostname.Valid {
			hostname = worker.Hostname.String
		}

		workerInfo := &openseerv1.WorkerInfo{
			WorkerId:      worker.ID,
			Status:        worker.Status,
			EnrolledAt:    worker.EnrolledAt.Unix(),
			LastSeenAt:    worker.LastSeenAt.Unix(),
			Region:        worker.Region,
			Hostname:      hostname,
			WorkerVersion: worker.Version,
		}

		workerInfos = append(workerInfos, workerInfo)
	}

	return connect.NewResponse(&openseerv1.ListWorkersResponse{
		Workers:    workerInfos,
		TotalCount: int32(totalCount),
	}), nil
}

func generateWorkerID() (string, error) {
	bytes := make([]byte, 16)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return "worker-" + hex.EncodeToString(bytes), nil
}
