package monitors

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"connectrpc.com/connect"
	openseerv1 "github.com/crisog/openseer/gen/openseer/v1"
	"github.com/crisog/openseer/gen/openseer/v1/openseerv1connect"
	"github.com/crisog/openseer/internal/app/control-plane/auth/session"
	"github.com/crisog/openseer/internal/app/control-plane/store/sqlc"
	"github.com/crisog/openseer/internal/pkg/regions"
	"github.com/sqlc-dev/pqtype"
	"go.uber.org/zap"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type MonitorsService struct {
	openseerv1connect.UnimplementedMonitorsServiceHandler
	queries *sqlc.Queries
	logger  *zap.Logger
}

func NewMonitorsService(queries *sqlc.Queries, logger *zap.Logger) *MonitorsService {
	return &MonitorsService{
		queries: queries,
		logger:  logger,
	}
}

func (s *MonitorsService) CreateMonitor(
	ctx context.Context,
	req *connect.Request[openseerv1.CreateMonitorRequest],
) (*connect.Response[openseerv1.CreateMonitorResponse], error) {
	user, ok := session.GetUserFromContext(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, nil)
	}

	msg := req.Msg
	if msg.Id == "" || msg.Url == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, nil)
	}

	if msg.IntervalMs == 0 {
		msg.IntervalMs = 60000
	}
	if msg.TimeoutMs == 0 {
		msg.TimeoutMs = 5000
	}
	if msg.Method == "" {
		msg.Method = "GET"
	}
	if len(msg.Regions) == 0 {
		msg.Regions = []string{"global"}
	}
	msg.Regions = regions.NormalizeList(msg.Regions)

	enabled := true
	if msg.Enabled != nil {
		enabled = *msg.Enabled
	}

	params := &sqlc.CreateMonitorParams{
		ID:         msg.Id,
		Name:       msg.Name,
		Url:        msg.Url,
		IntervalMs: msg.IntervalMs,
		TimeoutMs:  msg.TimeoutMs,
		Regions:    msg.Regions,
		JitterSeed: 42,
		Method:     msg.Method,
		Enabled: sql.NullBool{
			Bool:  enabled,
			Valid: true,
		},
		UserID: sql.NullString{
			String: user.ID,
			Valid:  true,
		},
	}

	if msg.Headers != nil {
		headerBytes, err := protojson.Marshal(msg.Headers)
		if err != nil {
			return nil, connect.NewError(connect.CodeInvalidArgument, err)
		}
		params.Headers = pqtype.NullRawMessage{
			RawMessage: headerBytes,
			Valid:      true,
		}
	}

	if msg.Assertions != nil {
		assertionBytes, err := protojson.Marshal(msg.Assertions)
		if err != nil {
			return nil, connect.NewError(connect.CodeInvalidArgument, err)
		}
		params.Assertions = pqtype.NullRawMessage{
			RawMessage: assertionBytes,
			Valid:      true,
		}
	}

	check, err := s.queries.CreateMonitor(ctx, params)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	protoCheck, err := s.dbMonitorToProto(check)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	warningRegions, err := s.zeroHealthyRegions(ctx, msg.Regions)
	if err != nil {
		s.logger.Error("Failed to evaluate region health on monitor create", zap.Error(err))
	}

	resp := connect.NewResponse(&openseerv1.CreateMonitorResponse{
		Monitor: protoCheck,
	})
	if len(warningRegions) > 0 {
		resp.Header().Set("X-Openseer-Zero-Healthy-Regions", strings.Join(warningRegions, ","))
		s.logger.Warn("Monitor created with regions lacking healthy workers",
			zap.String("monitor_id", check.ID),
			zap.Strings("regions", warningRegions))
	}

	return resp, nil
}

func (s *MonitorsService) GetMonitor(
	ctx context.Context,
	req *connect.Request[openseerv1.GetMonitorRequest],
) (*connect.Response[openseerv1.GetMonitorResponse], error) {
	user, ok := session.GetUserFromContext(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, nil)
	}

	if req.Msg.Id == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, nil)
	}

	check, err := s.queries.GetMonitorByUser(ctx, &sqlc.GetMonitorByUserParams{
		ID: req.Msg.Id,
		UserID: sql.NullString{
			String: user.ID,
			Valid:  true,
		},
	})
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, connect.NewError(connect.CodeNotFound, nil)
		}
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	protoCheck, err := s.dbMonitorToProto(check)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	return connect.NewResponse(&openseerv1.GetMonitorResponse{
		Monitor: protoCheck,
	}), nil
}

func (s *MonitorsService) UpdateMonitor(
	ctx context.Context,
	req *connect.Request[openseerv1.UpdateMonitorRequest],
) (*connect.Response[openseerv1.UpdateMonitorResponse], error) {
	user, ok := session.GetUserFromContext(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, nil)
	}

	if req.Msg.Id == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, nil)
	}

	existing, err := s.queries.GetMonitorByUser(ctx, &sqlc.GetMonitorByUserParams{
		ID: req.Msg.Id,
		UserID: sql.NullString{
			String: user.ID,
			Valid:  true,
		},
	})
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, connect.NewError(connect.CodeNotFound, nil)
		}
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	params := &sqlc.UpdateMonitorByUserParams{
		ID: req.Msg.Id,
		UserID: sql.NullString{
			String: user.ID,
			Valid:  true,
		},
		Name:       existing.Name,
		Url:        existing.Url,
		IntervalMs: existing.IntervalMs,
		TimeoutMs:  existing.TimeoutMs,
		Regions:    existing.Regions,
		JitterSeed: existing.JitterSeed,
		Method:     existing.Method,
		Enabled:    existing.Enabled,
	}

	if req.Msg.Name != nil {
		params.Name = *req.Msg.Name
	}
	if req.Msg.Url != nil {
		params.Url = *req.Msg.Url
	}
	if req.Msg.IntervalMs != nil {
		params.IntervalMs = *req.Msg.IntervalMs
	}
	if req.Msg.TimeoutMs != nil {
		params.TimeoutMs = *req.Msg.TimeoutMs
	}
	if len(req.Msg.Regions) > 0 {
		params.Regions = regions.NormalizeList(req.Msg.Regions)
	}
	if req.Msg.Method != nil {
		params.Method = *req.Msg.Method
	}
	if req.Msg.Enabled != nil {
		params.Enabled = sql.NullBool{
			Bool:  *req.Msg.Enabled,
			Valid: true,
		}
	}

	if req.Msg.Headers != nil {
		headerBytes, err := protojson.Marshal(req.Msg.Headers)
		if err != nil {
			return nil, connect.NewError(connect.CodeInvalidArgument, err)
		}
		params.Headers = pqtype.NullRawMessage{
			RawMessage: headerBytes,
			Valid:      true,
		}
	} else {
		params.Headers = existing.Headers
	}

	if req.Msg.Assertions != nil {
		assertionBytes, err := protojson.Marshal(req.Msg.Assertions)
		if err != nil {
			return nil, connect.NewError(connect.CodeInvalidArgument, err)
		}
		params.Assertions = pqtype.NullRawMessage{
			RawMessage: assertionBytes,
			Valid:      true,
		}
	} else {
		params.Assertions = existing.Assertions
	}

	updated, err := s.queries.UpdateMonitorByUser(ctx, params)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	protoCheck, err := s.dbMonitorToProto(updated)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	warningRegions, err := s.zeroHealthyRegions(ctx, params.Regions)
	if err != nil {
		s.logger.Error("Failed to evaluate region health on monitor update", zap.Error(err))
	}

	resp := connect.NewResponse(&openseerv1.UpdateMonitorResponse{
		Monitor: protoCheck,
	})
	if len(warningRegions) > 0 {
		resp.Header().Set("X-Openseer-Zero-Healthy-Regions", strings.Join(warningRegions, ","))
		s.logger.Warn("Monitor updated with regions lacking healthy workers",
			zap.String("monitor_id", updated.ID),
			zap.Strings("regions", warningRegions))
	}

	return resp, nil
}

func (s *MonitorsService) ListMonitors(
	ctx context.Context,
	req *connect.Request[openseerv1.ListMonitorsRequest],
) (*connect.Response[openseerv1.ListMonitorsResponse], error) {
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

	protoChecks := make([]*openseerv1.Monitor, 0, len(checks))
	for _, check := range checks {
		protoCheck, err := s.dbMonitorToProto(check)
		if err != nil {
			return nil, connect.NewError(connect.CodeInternal, err)
		}
		protoChecks = append(protoChecks, protoCheck)
	}

	return connect.NewResponse(&openseerv1.ListMonitorsResponse{
		Monitors: protoChecks,
	}), nil
}

func (s *MonitorsService) GetMonitorResults(
	ctx context.Context,
	req *connect.Request[openseerv1.GetMonitorResultsRequest],
) (*connect.Response[openseerv1.GetMonitorResultsResponse], error) {
	user, ok := session.GetUserFromContext(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, nil)
	}

	if req.Msg.MonitorId == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, nil)
	}

	_, err := s.queries.GetMonitorByUser(ctx, &sqlc.GetMonitorByUserParams{
		ID: req.Msg.MonitorId,
		UserID: sql.NullString{
			String: user.ID,
			Valid:  true,
		},
	})
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, connect.NewError(connect.CodeNotFound, nil)
		}
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	limit := req.Msg.Limit
	if limit == 0 {
		limit = 100
	}

	results, err := s.queries.GetRecentResults(ctx, &sqlc.GetRecentResultsParams{
		MonitorID: req.Msg.MonitorId,
		Limit:     limit,
	})
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	protoResults := make([]*openseerv1.MonitorResult, 0, len(results))
	for _, result := range results {
		protoResult := &openseerv1.MonitorResult{
			RunId:     result.RunID,
			MonitorId: result.MonitorID,
			Region:    result.Region,
			EventAt:   timestamppb.New(result.EventAt),
			Status:    result.Status,
		}

		if result.HttpCode.Valid {
			protoResult.HttpCode = &result.HttpCode.Int32
		}
		if result.DnsMs.Valid {
			protoResult.DnsMs = &result.DnsMs.Int32
		}
		if result.ConnectMs.Valid {
			protoResult.ConnectMs = &result.ConnectMs.Int32
		}
		if result.TlsMs.Valid {
			protoResult.TlsMs = &result.TlsMs.Int32
		}
		if result.TtfbMs.Valid {
			protoResult.TtfbMs = &result.TtfbMs.Int32
		}
		if result.DownloadMs.Valid {
			protoResult.DownloadMs = &result.DownloadMs.Int32
		}
		if result.TotalMs.Valid {
			protoResult.TotalMs = &result.TotalMs.Int32
		}
		if result.SizeBytes.Valid {
			protoResult.SizeBytes = &result.SizeBytes.Int64
		}
		if result.ErrorMessage.Valid {
			protoResult.ErrorMessage = &result.ErrorMessage.String
		}

		protoResults = append(protoResults, protoResult)
	}

	return connect.NewResponse(&openseerv1.GetMonitorResultsResponse{
		Results: protoResults,
	}), nil
}

func (s *MonitorsService) GetMonitorMetrics(
	ctx context.Context,
	req *connect.Request[openseerv1.GetMonitorMetricsRequest],
) (*connect.Response[openseerv1.GetMonitorMetricsResponse], error) {
	user, ok := session.GetUserFromContext(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, nil)
	}

	if req.Msg.MonitorId == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, nil)
	}

	_, err := s.queries.GetMonitorByUser(ctx, &sqlc.GetMonitorByUserParams{
		ID: req.Msg.MonitorId,
		UserID: sql.NullString{
			String: user.ID,
			Valid:  true,
		},
	})
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, connect.NewError(connect.CodeNotFound, nil)
		}
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	endTime := time.Now()
	startTime := endTime.Add(-24 * time.Hour)
	if req.Msg.StartTime != nil {
		startTime = req.Msg.StartTime.AsTime()
	}
	if req.Msg.EndTime != nil {
		endTime = req.Msg.EndTime.AsTime()
	}

	metrics, err := s.queries.GetAggregatedMetrics(ctx, &sqlc.GetAggregatedMetricsParams{
		MonitorID: req.Msg.MonitorId,
		Bucket:    startTime,
		Bucket_2:  endTime,
	})
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	protoMetrics := make([]*openseerv1.MonitorMetrics, 0, len(metrics))
	for _, metric := range metrics {
		errorRate := 0.0
		if metric.Count > 0 {
			errorRate = float64(metric.ErrorCount) / float64(metric.Count)
		}

		protoMetric := &openseerv1.MonitorMetrics{
			MonitorId:  metric.MonitorID,
			Region:     metric.Region,
			Count:      metric.Count,
			ErrorCount: metric.ErrorCount,
			ErrorRate:  errorRate,
			P50Ms:      metric.P50Ms,
			P95Ms:      metric.P95Ms,
			P99Ms:      metric.P99Ms,
			AvgMs:      metric.AvgMs,
		}

		if bucket, ok := metric.Bucket.(time.Time); ok {
			protoMetric.Bucket = timestamppb.New(bucket)
		}

		if minMs, ok := metric.MinMs.(float64); ok {
			protoMetric.MinMs = minMs
		}

		if maxMs, ok := metric.MaxMs.(float64); ok {
			protoMetric.MaxMs = maxMs
		}

		protoMetrics = append(protoMetrics, protoMetric)
	}

	return connect.NewResponse(&openseerv1.GetMonitorMetricsResponse{
		Metrics: protoMetrics,
	}), nil
}

func (s *MonitorsService) zeroHealthyRegions(ctx context.Context, monitorRegions []string) ([]string, error) {
	if len(monitorRegions) == 0 {
		return nil, nil
	}

	rows, err := s.queries.ListRegionHealth(ctx)
	if err != nil {
		return nil, err
	}

	healthyByRegion := make(map[string]int64, len(rows))
	for _, row := range rows {
		healthyByRegion[row.Region] = row.HealthyWorkers
	}

	warnings := make([]string, 0)
	seen := make(map[string]struct{}, len(monitorRegions))
	for _, region := range monitorRegions {
		if region == "global" {
			continue
		}
		if _, exists := seen[region]; exists {
			continue
		}
		seen[region] = struct{}{}

		if healthyByRegion[region] <= 0 {
			warnings = append(warnings, region)
		}
	}

	return warnings, nil
}

func (s *MonitorsService) dbMonitorToProto(check *sqlc.AppMonitor) (*openseerv1.Monitor, error) {
	protoCheck := &openseerv1.Monitor{
		Id:         check.ID,
		Name:       check.Name,
		Url:        check.Url,
		IntervalMs: check.IntervalMs,
		TimeoutMs:  check.TimeoutMs,
		Regions:    check.Regions,
		Method:     check.Method,
		Enabled:    check.Enabled.Valid && check.Enabled.Bool,
		CreatedAt:  timestamppb.New(check.CreatedAt),
		UpdatedAt:  timestamppb.New(check.UpdatedAt),
	}

	if check.Headers.Valid && len(check.Headers.RawMessage) > 0 {
		var headers structpb.Struct
		if err := protojson.Unmarshal(check.Headers.RawMessage, &headers); err != nil {
			return nil, err
		}
		protoCheck.Headers = &headers
	}

	if check.Assertions.Valid && len(check.Assertions.RawMessage) > 0 {
		var assertions structpb.Struct
		if err := protojson.Unmarshal(check.Assertions.RawMessage, &assertions); err != nil {
			return nil, err
		}
		protoCheck.Assertions = &assertions
	}

	if check.LastScheduledAt.Valid {
		protoCheck.LastScheduledAt = timestamppb.New(check.LastScheduledAt.Time)
	}

	if check.NextDueAt.Valid {
		protoCheck.NextDueAt = timestamppb.New(check.NextDueAt.Time)
	}

	return protoCheck, nil
}

func (s *MonitorsService) DeleteMonitor(
	ctx context.Context,
	req *connect.Request[openseerv1.DeleteMonitorRequest],
) (*connect.Response[openseerv1.DeleteMonitorResponse], error) {
	user, ok := session.GetUserFromContext(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, nil)
	}

	if req.Msg.Id == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, nil)
	}

	err := s.queries.DeleteMonitorByUser(ctx, &sqlc.DeleteMonitorByUserParams{
		ID: req.Msg.Id,
		UserID: sql.NullString{
			String: user.ID,
			Valid:  true,
		},
	})
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	return connect.NewResponse(&openseerv1.DeleteMonitorResponse{}), nil
}

func (s *MonitorsService) GetMonitorUptime(ctx context.Context, req *connect.Request[openseerv1.GetMonitorUptimeRequest]) (*connect.Response[openseerv1.GetMonitorUptimeResponse], error) {
	user, ok := session.GetUserFromContext(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, nil)
	}

	if _, err := s.queries.GetMonitorByUser(ctx, &sqlc.GetMonitorByUserParams{
		ID: req.Msg.MonitorId,
		UserID: sql.NullString{
			String: user.ID,
			Valid:  true,
		},
	}); err != nil {
		if err == sql.ErrNoRows {
			return nil, connect.NewError(connect.CodeNotFound, nil)
		}
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	var uptimeData uptimeResult
	var err error

	switch req.Msg.TimeRange {
	case "24h":
		uptimeData, err = s.fetchUptime24h(ctx, req.Msg.MonitorId)
	case "7d":
		uptimeData, err = s.fetchUptime7d(ctx, req.Msg.MonitorId)
	case "30d":
		uptimeData, err = s.fetchUptime30d(ctx, req.Msg.MonitorId)
	default:
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("invalid time range: %s", req.Msg.TimeRange))
	}

	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	return connect.NewResponse(uptimeData.toProto()), nil
}

func (s *MonitorsService) GetMonitorUptimeTimeline(ctx context.Context, req *connect.Request[openseerv1.GetMonitorUptimeTimelineRequest]) (*connect.Response[openseerv1.GetMonitorUptimeTimelineResponse], error) {
	user, ok := session.GetUserFromContext(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, nil)
	}

	_, err := s.queries.GetMonitorByUser(ctx, &sqlc.GetMonitorByUserParams{
		ID: req.Msg.MonitorId,
		UserID: sql.NullString{
			String: user.ID,
			Valid:  true,
		},
	})
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, connect.NewError(connect.CodeNotFound, nil)
		}
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	var timelineData interface{}
	switch req.Msg.TimeRange {
	case "24h":
		timelineData, err = s.queries.GetUptimeTimeline24h(ctx, req.Msg.MonitorId)
	case "7d":
		timelineData, err = s.queries.GetUptimeTimeline7d(ctx, req.Msg.MonitorId)
		if err == nil {
			if rows, ok := timelineData.([]*sqlc.GetUptimeTimeline7dRow); ok && len(rows) == 0 {
				timelineData, err = s.queries.GetUptimeTimeline24h(ctx, req.Msg.MonitorId)
			}
		}
	case "30d":
		timelineData, err = s.queries.GetUptimeTimeline30d(ctx, req.Msg.MonitorId)
		if err == nil {
			if rows, ok := timelineData.([]*sqlc.GetUptimeTimeline30dRow); ok && len(rows) == 0 {
				if rows7, err7 := s.queries.GetUptimeTimeline7d(ctx, req.Msg.MonitorId); err7 == nil && len(rows7) > 0 {
					timelineData = rows7
				} else {
					timelineData, err = s.queries.GetUptimeTimeline24h(ctx, req.Msg.MonitorId)
				}
			}
		}
	default:
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("invalid time range: %s", req.Msg.TimeRange))
	}

	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	response := &openseerv1.GetMonitorUptimeTimelineResponse{
		Data: make([]*openseerv1.UptimeTimelinePoint, 0),
	}

	switch data := timelineData.(type) {
	case []*sqlc.GetUptimeTimeline24hRow:
		for _, row := range data {
			point := &openseerv1.UptimeTimelinePoint{
				TotalChecks:      row.TotalChecks,
				SuccessfulChecks: row.SuccessfulChecks,
				UptimePercentage: float64(row.UptimePercentage),
			}
			if bucket, ok := row.Bucket.(time.Time); ok {
				point.Bucket = timestamppb.New(bucket)
			}
			response.Data = append(response.Data, point)
		}
	case []*sqlc.GetUptimeTimeline7dRow:
		for _, row := range data {
			point := &openseerv1.UptimeTimelinePoint{
				TotalChecks:      row.TotalChecks,
				SuccessfulChecks: row.SuccessfulChecks,
				UptimePercentage: float64(row.UptimePercentage),
			}
			if bucket, ok := row.Bucket.(time.Time); ok {
				point.Bucket = timestamppb.New(bucket)
			}
			response.Data = append(response.Data, point)
		}
	case []*sqlc.GetUptimeTimeline30dRow:
		for _, row := range data {
			point := &openseerv1.UptimeTimelinePoint{
				TotalChecks:      row.TotalChecks,
				SuccessfulChecks: row.SuccessfulChecks,
				UptimePercentage: float64(row.UptimePercentage),
			}
			if bucket, ok := row.Bucket.(time.Time); ok {
				point.Bucket = timestamppb.New(bucket)
			}
			response.Data = append(response.Data, point)
		}
	}

	return connect.NewResponse(response), nil
}

type uptimeResult struct {
	TotalChecks      int64
	SuccessfulChecks int64
	FailedChecks     int64
	UptimePercentage float64
}

func (r *uptimeResult) toProto() *openseerv1.GetMonitorUptimeResponse {
	return &openseerv1.GetMonitorUptimeResponse{
		TotalChecks:      r.TotalChecks,
		SuccessfulChecks: r.SuccessfulChecks,
		FailedChecks:     r.FailedChecks,
		UptimePercentage: r.UptimePercentage,
	}
}

func (s *MonitorsService) fetchUptime24h(ctx context.Context, monitorID string) (uptimeResult, error) {
	data, err := s.queries.GetUptimeData24h(ctx, monitorID)
	if err != nil {
		return uptimeResult{}, err
	}
	if data.TotalChecks.(int64) == 0 {
		rawData, err := s.queries.GetUptimeData24hRaw(ctx, monitorID)
		if err != nil {
			return uptimeResult{}, err
		}
		return uptimeResult{
			TotalChecks:      rawData.TotalChecks.(int64),
			SuccessfulChecks: rawData.SuccessfulChecks.(int64),
			FailedChecks:     rawData.FailedChecks.(int64),
			UptimePercentage: float64(rawData.UptimePercentage),
		}, nil
	}
	return uptimeResult{
		TotalChecks:      data.TotalChecks.(int64),
		SuccessfulChecks: data.SuccessfulChecks.(int64),
		FailedChecks:     data.FailedChecks.(int64),
		UptimePercentage: float64(data.UptimePercentage),
	}, nil
}

func (s *MonitorsService) fetchUptime7d(ctx context.Context, monitorID string) (uptimeResult, error) {
	data, err := s.queries.GetUptimeData7d(ctx, monitorID)
	if err != nil {
		return uptimeResult{}, err
	}
	if data.TotalChecks.(int64) == 0 {
		return s.fetchUptime24h(ctx, monitorID)
	}
	return uptimeResult{
		TotalChecks:      data.TotalChecks.(int64),
		SuccessfulChecks: data.SuccessfulChecks.(int64),
		FailedChecks:     data.FailedChecks.(int64),
		UptimePercentage: float64(data.UptimePercentage),
	}, nil
}

func (s *MonitorsService) fetchUptime30d(ctx context.Context, monitorID string) (uptimeResult, error) {
	data, err := s.queries.GetUptimeData30d(ctx, monitorID)
	if err != nil {
		return uptimeResult{}, err
	}
	if data.TotalChecks.(int64) == 0 {
		return s.fetchUptime7d(ctx, monitorID)
	}
	return uptimeResult{
		TotalChecks:      data.TotalChecks.(int64),
		SuccessfulChecks: data.SuccessfulChecks.(int64),
		FailedChecks:     data.FailedChecks.(int64),
		UptimePercentage: float64(data.UptimePercentage),
	}, nil
}
