package controlplane

import (
	"context"
	"database/sql"
	"fmt"
	"log"

	openseerv1 "github.com/crisog/openseer/gen/openseer/v1"
	"github.com/crisog/openseer/internal/app/control-plane/store/sqlc"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type Ingest struct {
	queries *sqlc.Queries
}

func New(queries *sqlc.Queries) *Ingest {
	return &Ingest{
		queries: queries,
	}
}

func (i *Ingest) ProcessResult(ctx context.Context, result *openseerv1.MonitorResult) error {
	params := &sqlc.UpsertResultParams{
		RunID:     result.RunId,
		MonitorID: result.MonitorId,
		Region:    result.Region,
		EventAt:   result.EventAt.AsTime(),
		Status:    convertStatus(result.Status),
	}

	if result.HttpCode != nil && *result.HttpCode > 0 {
		params.HttpCode = sql.NullInt32{Int32: *result.HttpCode, Valid: true}
	}
	if result.DnsMs != nil && *result.DnsMs > 0 {
		params.DnsMs = sql.NullInt32{Int32: *result.DnsMs, Valid: true}
	}
	if result.ConnectMs != nil && *result.ConnectMs > 0 {
		params.ConnectMs = sql.NullInt32{Int32: *result.ConnectMs, Valid: true}
	}
	if result.TlsMs != nil && *result.TlsMs > 0 {
		params.TlsMs = sql.NullInt32{Int32: *result.TlsMs, Valid: true}
	}
	if result.TtfbMs != nil && *result.TtfbMs > 0 {
		params.TtfbMs = sql.NullInt32{Int32: *result.TtfbMs, Valid: true}
	}
	if result.DownloadMs != nil && *result.DownloadMs > 0 {
		params.DownloadMs = sql.NullInt32{Int32: *result.DownloadMs, Valid: true}
	}
	if result.TotalMs != nil && *result.TotalMs > 0 {
		params.TotalMs = sql.NullInt32{Int32: *result.TotalMs, Valid: true}
	}
	if result.SizeBytes != nil && *result.SizeBytes > 0 {
		params.SizeBytes = sql.NullInt64{Int64: *result.SizeBytes, Valid: true}
	}
	if result.ErrorMessage != nil && *result.ErrorMessage != "" {
		params.ErrorMessage = sql.NullString{String: *result.ErrorMessage, Valid: true}
	}

	storedResult, err := i.queries.UpsertResult(ctx, params)
	if err != nil {
		return fmt.Errorf("failed to store result: %w", err)
	}

	log.Printf("Stored result: run_id=%s monitor_id=%s status=%s total_ms=%v",
		storedResult.RunID, storedResult.MonitorID, storedResult.Status,
		storedResult.TotalMs.Int32)

	return nil
}

func convertStatus(status string) string {
	switch status {
	case "OK":
		return "OK"
	case "FAIL":
		return "FAIL"
	case "ERROR":
		return "ERROR"
	default:
		return "UNKNOWN"
	}
}

func (i *Ingest) GetRecentResults(ctx context.Context, monitorID string, limit int32) ([]*openseerv1.MonitorResult, error) {
	results, err := i.queries.GetRecentResults(ctx, &sqlc.GetRecentResultsParams{
		MonitorID: monitorID,
		Limit:     limit,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to get recent results: %w", err)
	}

	out := make([]*openseerv1.MonitorResult, 0, len(results))
	for _, r := range results {
		cr := &openseerv1.MonitorResult{
			RunId:     r.RunID,
			MonitorId: r.MonitorID,
			Region:    r.Region,
			EventAt:   timestamppb.New(r.EventAt),
			Status:    convertStatusFromDB(r.Status),
		}
		if r.HttpCode.Valid {
			cr.HttpCode = &r.HttpCode.Int32
		}
		if r.DnsMs.Valid {
			cr.DnsMs = &r.DnsMs.Int32
		}
		if r.ConnectMs.Valid {
			cr.ConnectMs = &r.ConnectMs.Int32
		}
		if r.TlsMs.Valid {
			cr.TlsMs = &r.TlsMs.Int32
		}
		if r.TtfbMs.Valid {
			cr.TtfbMs = &r.TtfbMs.Int32
		}
		if r.DownloadMs.Valid {
			cr.DownloadMs = &r.DownloadMs.Int32
		}
		if r.TotalMs.Valid {
			cr.TotalMs = &r.TotalMs.Int32
		}
		if r.SizeBytes.Valid {
			cr.SizeBytes = &r.SizeBytes.Int64
		}
		if r.ErrorMessage.Valid {
			cr.ErrorMessage = &r.ErrorMessage.String
		}
		out = append(out, cr)
	}

	return out, nil
}

func convertStatusFromDB(status string) string {
	switch status {
	case "OK":
		return "OK"
	case "FAIL":
		return "FAIL"
	case "ERROR":
		return "ERROR"
	default:
		return "ERROR"
	}
}

func (i *Ingest) GetLatestResults(ctx context.Context) ([]*openseerv1.MonitorResult, error) {
	results, err := i.queries.GetLatestResultPerMonitor(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get latest results: %w", err)
	}

	out := make([]*openseerv1.MonitorResult, 0, len(results))
	for _, r := range results {
		cr := &openseerv1.MonitorResult{
			RunId:     "",
			MonitorId: r.MonitorID,
			Region:    r.Region,
			EventAt:   timestamppb.New(r.EventAt),
			Status:    convertStatusFromDB(r.Status),
		}
		if r.HttpCode.Valid {
			cr.HttpCode = &r.HttpCode.Int32
		}
		if r.TotalMs.Valid {
			cr.TotalMs = &r.TotalMs.Int32
		}
		out = append(out, cr)
	}

	return out, nil
}
