package dashboard

import (
	"context"
	"database/sql"

	"connectrpc.com/connect"
	openseerv1 "github.com/crisog/openseer/gen/openseer/v1"
	"github.com/crisog/openseer/gen/openseer/v1/openseerv1connect"
	"github.com/crisog/openseer/internal/app/control-plane/auth/session"
	"github.com/crisog/openseer/internal/app/control-plane/store/sqlc"
	"go.uber.org/zap"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type DashboardService struct {
	openseerv1connect.UnimplementedDashboardServiceHandler
	queries *sqlc.Queries
	logger  *zap.Logger
}

func NewDashboardService(queries *sqlc.Queries, logger *zap.Logger) *DashboardService {
	return &DashboardService{
		queries: queries,
		logger:  logger,
	}
}

func (s *DashboardService) GetDashboardOverview(
	ctx context.Context,
	req *connect.Request[openseerv1.GetDashboardOverviewRequest],
) (*connect.Response[openseerv1.GetDashboardOverviewResponse], error) {
	user, ok := session.GetUserFromContext(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, nil)
	}

	checks, err := s.queries.ListMonitorsByUser(ctx, sql.NullString{
		String: user.ID,
		Valid:  true,
	})
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	latestResults, err := s.queries.GetLatestResultPerMonitor(ctx)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	resultMap := make(map[string]*sqlc.GetLatestResultPerMonitorRow)
	for _, result := range latestResults {
		resultMap[result.MonitorID] = result
	}

	totalChecks := len(checks)
	activeChecks := 0
	failingChecks := 0
	checkStatuses := make([]*openseerv1.MonitorStatus, 0, len(checks))

	for _, check := range checks {
		if !check.UserID.Valid || check.UserID.String != user.ID {
			continue
		}

		enabled := check.Enabled.Valid && check.Enabled.Bool

		if enabled {
			activeChecks++
		}

		status := &openseerv1.MonitorStatus{
			MonitorId: check.ID,
			Url:       check.Url,
			Status:    "UNKNOWN",
		}

		if result, exists := resultMap[check.ID]; exists {
			status.Status = result.Status
			status.LastFailure = timestamppb.New(result.EventAt)

			if result.Status != "OK" && enabled {
				failingChecks++
			}
		}

		checkStatuses = append(checkStatuses, status)
	}

	return connect.NewResponse(&openseerv1.GetDashboardOverviewResponse{
		Overview: &openseerv1.DashboardOverview{
			TotalMonitors:      int64(totalChecks),
			EnabledMonitors:    int64(activeChecks),
			DisabledMonitors:   int64(totalChecks - activeChecks),
			HealthyMonitors:    int64(activeChecks - failingChecks),
			UnhealthyMonitors:  int64(failingChecks),
			TotalRuns_24H:      0,
			FailedRuns_24H:     0,
			OverallSuccessRate: 0,
			RecentFailures:     checkStatuses,
			SlowestMonitors:    []*openseerv1.MonitorPerformance{},
		},
	}), nil
}
