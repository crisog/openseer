package helpers

import (
	"context"
	"database/sql"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/sqlc-dev/pqtype"
	"github.com/stretchr/testify/require"

	"github.com/crisog/openseer/internal/app/control-plane/store/sqlc"
)

type TestFixtures struct {
	Monitor *sqlc.AppMonitor
	Worker  *sqlc.AppWorker
	Jobs    []*sqlc.AppJob
}

func CreateTestMonitor(t *testing.T, queries *sqlc.Queries) *sqlc.AppMonitor {
	ctx := context.Background()

	monitorID := uuid.New().String()
	params := &sqlc.CreateMonitorParams{
		ID:         monitorID,
		Name:       "test-monitor",
		Url:        "https://example.com/health",
		IntervalMs: 60000,
		TimeoutMs:  5000,
		Regions:    []string{"us-east-1", "eu-west-1"},
		JitterSeed: 42,
		Method:     "GET",
		Enabled:    sql.NullBool{Bool: true, Valid: true},
	}

	monitor, err := queries.CreateMonitor(ctx, params)
	require.NoError(t, err)

	return monitor
}

type MonitorConfig struct {
	Name       string
	URL        string
	IntervalMs int32
	TimeoutMs  int32
	Regions    []string
	Method     string
	Headers    map[string]string
	Assertions map[string]any
	Enabled    *bool
	JitterSeed int32
	UserID     string
}

func CreateMonitorWithConfig(t *testing.T, queries *sqlc.Queries, cfg MonitorConfig) *sqlc.AppMonitor {
	t.Helper()
	ctx := context.Background()

	if cfg.Name == "" {
		cfg.Name = "monitor-" + uuid.NewString()
	}
	if cfg.URL == "" {
		cfg.URL = "https://example.com/config"
	}
	if cfg.IntervalMs == 0 {
		cfg.IntervalMs = 60000
	}
	if cfg.TimeoutMs == 0 {
		cfg.TimeoutMs = 5000
	}
	if len(cfg.Regions) == 0 {
		cfg.Regions = []string{"us-east-1"}
	}
	if cfg.Method == "" {
		cfg.Method = "GET"
	}
	if cfg.JitterSeed == 0 {
		cfg.JitterSeed = 42
	}
	if cfg.UserID == "" {
		cfg.UserID = "test-user-default"
	}

	var headersRaw pqtype.NullRawMessage
	if len(cfg.Headers) > 0 {
		headerBytes, err := json.Marshal(cfg.Headers)
		require.NoError(t, err)
		headersRaw = pqtype.NullRawMessage{RawMessage: headerBytes, Valid: true}
	}

	var assertionsRaw pqtype.NullRawMessage
	if cfg.Assertions != nil {
		assertionBytes, err := json.Marshal(cfg.Assertions)
		require.NoError(t, err)
		assertionsRaw = pqtype.NullRawMessage{RawMessage: assertionBytes, Valid: true}
	}

	enabled := sql.NullBool{Bool: true, Valid: true}
	if cfg.Enabled != nil {
		enabled = sql.NullBool{Bool: *cfg.Enabled, Valid: true}
	}

	params := &sqlc.CreateMonitorParams{
		ID:           uuid.NewString(),
		Name:         cfg.Name,
		Url:          cfg.URL,
		IntervalMs:   cfg.IntervalMs,
		TimeoutMs:    cfg.TimeoutMs,
		Regions:      cfg.Regions,
		JitterSeed:   cfg.JitterSeed,
		RateLimitKey: sql.NullString{},
		Method:       cfg.Method,
		Headers:      headersRaw,
		Assertions:   assertionsRaw,
		Enabled:      enabled,
		UserID:       sql.NullString{String: cfg.UserID, Valid: true},
	}

	monitor, err := queries.CreateMonitor(ctx, params)
	require.NoError(t, err)

	return monitor
}

func CreateTestWorker(t *testing.T, queries *sqlc.Queries, region string) *sqlc.AppWorker {
	ctx := context.Background()

	workerID := uuid.New().String()
	params := &sqlc.RegisterWorkerParams{
		ID:      workerID,
		Region:  region,
		Version: "test-1.0.0",
	}

	worker, err := queries.RegisterWorker(ctx, params)
	require.NoError(t, err)

	return worker
}

func CreateTestJob(t *testing.T, queries *sqlc.Queries, monitorID string, region string) *sqlc.AppJob {
	ctx := context.Background()

	runID := uuid.New().String()
	params := &sqlc.CreateJobParams{
		RunID:       runID,
		MonitorID:   monitorID,
		Region:      region,
		ScheduledAt: time.Now().Add(-1 * time.Second),
	}

	job, err := queries.CreateJob(ctx, params)
	require.NoError(t, err)

	return job
}

func CreateTestResult(t *testing.T, queries *sqlc.Queries, runID, monitorID, region string) *sqlc.TsResultsRaw {
	ctx := context.Background()

	params := &sqlc.UpsertResultParams{
		RunID:      runID,
		MonitorID:  monitorID,
		Region:     region,
		EventAt:    time.Now(),
		Status:     "OK",
		HttpCode:   sql.NullInt32{Int32: 200, Valid: true},
		TotalMs:    sql.NullInt32{Int32: 150, Valid: true},
		DnsMs:      sql.NullInt32{Int32: 10, Valid: true},
		ConnectMs:  sql.NullInt32{Int32: 20, Valid: true},
		TtfbMs:     sql.NullInt32{Int32: 100, Valid: true},
		DownloadMs: sql.NullInt32{Int32: 20, Valid: true},
	}

	result, err := queries.UpsertResult(ctx, params)
	require.NoError(t, err)

	return result
}

func SetupTestFixtures(t *testing.T, queries *sqlc.Queries) *TestFixtures {
	monitor := CreateTestMonitor(t, queries)
	worker := CreateTestWorker(t, queries, "us-east-1")

	jobs := make([]*sqlc.AppJob, 3)
	for i := 0; i < 3; i++ {
		jobs[i] = CreateTestJob(t, queries, monitor.ID, "us-east-1")
	}

	return &TestFixtures{
		Monitor: monitor,
		Worker:  worker,
		Jobs:    jobs,
	}
}

func CreateLeaseExpiredJob(t *testing.T, queries *sqlc.Queries, monitorID string, region string) *sqlc.AppJob {
	ctx := context.Background()

	runID := uuid.New().String()
	params := &sqlc.CreateJobParams{
		RunID:       runID,
		MonitorID:   monitorID,
		Region:      region,
		ScheduledAt: time.Now().Add(-10 * time.Minute),
	}

	job, err := queries.CreateJob(ctx, params)
	require.NoError(t, err)

	return job
}

func CreateScheduledJobs(t *testing.T, queries *sqlc.Queries, monitorID string, region string, count int) []*sqlc.AppJob {
	jobs := make([]*sqlc.AppJob, count)

	for i := 0; i < count; i++ {
		scheduledAt := time.Now().Add(time.Duration(i) * time.Minute)

		ctx := context.Background()
		runID := uuid.New().String()
		params := &sqlc.CreateJobParams{
			RunID:       runID,
			MonitorID:   monitorID,
			Region:      region,
			ScheduledAt: scheduledAt,
		}

		job, err := queries.CreateJob(ctx, params)
		require.NoError(t, err)

		jobs[i] = job
	}

	return jobs
}

func CreateTestUser(t *testing.T, db *sql.DB, userID string) {
	t.Helper()
	ctx := context.Background()

	_, err := db.ExecContext(ctx, `
		INSERT INTO "user" (id, name, email, "emailVerified")
		VALUES ($1, $2, $3, true)
		ON CONFLICT (id) DO NOTHING
	`, userID, "Test User", userID+"@test.com")
	require.NoError(t, err)
}

func CreateMonitorWithUser(t *testing.T, queries *sqlc.Queries, db *sql.DB, cfg MonitorConfig) *sqlc.AppMonitor {
	t.Helper()

	if cfg.UserID == "" {
		cfg.UserID = "test-user-default"
	}

	CreateTestUser(t, db, cfg.UserID)

	return CreateMonitorWithConfig(t, queries, cfg)
}
