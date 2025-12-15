package helpers

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"

	openseerv1connect "github.com/crisog/openseer/gen/openseer/v1/openseerv1connect"
	controlplane "github.com/crisog/openseer/internal/app/control-plane"
	"github.com/crisog/openseer/internal/app/control-plane/api/dashboard"
	"github.com/crisog/openseer/internal/app/control-plane/api/enrollment"
	monitorsapi "github.com/crisog/openseer/internal/app/control-plane/api/monitors"
	"github.com/crisog/openseer/internal/app/control-plane/api/user"
	workerapi "github.com/crisog/openseer/internal/app/control-plane/api/worker"
	"github.com/crisog/openseer/internal/app/control-plane/auth/session"
	metrics "github.com/crisog/openseer/internal/app/control-plane/metrics"
	"github.com/crisog/openseer/internal/app/control-plane/middleware"
	"github.com/crisog/openseer/internal/app/control-plane/store/sqlc"
)

type ControlPlaneTestEnvironment struct {
	T *testing.T

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	TestDB *TestDB

	Queries           *sqlc.Queries
	LeaseReaper       *controlplane.LeaseReaper
	InactivityMonitor *controlplane.WorkerInactivityMonitor
	Scheduler         *controlplane.Scheduler
	Ingest            *metrics.Ingest

	ClusterToken      string
	APIEndpoint       string
	EnrollmentService *enrollment.EnrollmentService
	WorkerService     *workerapi.WorkerService
	MonitorsService   *monitorsapi.MonitorsService
	DashboardService  *dashboard.DashboardService
	UserService       *user.UserService
}

func SetupControlPlane(t *testing.T) *ControlPlaneTestEnvironment {
	t.Helper()

	testDB := SetupTestDB(t)
	logger := zaptest.NewLogger(t)

	clusterToken := "test-cluster-token"

	leaseReaper := controlplane.NewLeaseReaper(testDB.Queries, testDB.DB, 1*time.Second)
	inactivityMonitor := controlplane.NewWorkerInactivityMonitor(testDB.Queries, 1*time.Second)
	ingest := metrics.New(testDB.Queries)
	scheduler := controlplane.NewScheduler(testDB.Queries, testDB.DB, 200*time.Millisecond)

	enrollmentService := enrollment.NewEnrollmentService(testDB.Queries, logger, clusterToken, "http://test-control-plane:8080")
	workerService := workerapi.NewWorkerService(testDB.Queries, logger, ingest, 10*time.Second)
	monitorsService := monitorsapi.NewMonitorsService(testDB.Queries, logger)
	dashboardService := dashboard.NewDashboardService(testDB.Queries, logger)
	userService := user.NewUserService(testDB.Queries, logger)

	ctx, cancel := context.WithCancel(context.Background())

	env := &ControlPlaneTestEnvironment{
		T:                 t,
		ctx:               ctx,
		cancel:            cancel,
		TestDB:            testDB,
		Queries:           testDB.Queries,
		LeaseReaper:       leaseReaper,
		InactivityMonitor: inactivityMonitor,
		Scheduler:         scheduler,
		Ingest:            ingest,
		ClusterToken:      clusterToken,
		APIEndpoint:       "http://test-control-plane:8080",
		EnrollmentService: enrollmentService,
		WorkerService:     workerService,
		MonitorsService:   monitorsService,
		DashboardService:  dashboardService,
		UserService:       userService,
	}

	t.Cleanup(env.Shutdown)

	return env
}

func SetupControlPlaneWithDB(t *testing.T, sharedDB *sql.DB) *ControlPlaneTestEnvironment {
	t.Helper()

	logger := zaptest.NewLogger(t)
	clusterToken := "test-cluster-token"

	queries := sqlc.New(sharedDB)
	leaseReaper := controlplane.NewLeaseReaper(queries, sharedDB, 1*time.Second)
	inactivityMonitor := controlplane.NewWorkerInactivityMonitor(queries, 1*time.Second)
	ingest := metrics.New(queries)
	scheduler := controlplane.NewScheduler(queries, sharedDB, 200*time.Millisecond)

	enrollmentService := enrollment.NewEnrollmentService(queries, logger, clusterToken, "http://test-control-plane-2:8080")
	workerService := workerapi.NewWorkerService(queries, logger, ingest, 10*time.Second)
	monitorsService := monitorsapi.NewMonitorsService(queries, logger)
	dashboardService := dashboard.NewDashboardService(queries, logger)
	userService := user.NewUserService(queries, logger)

	ctx, cancel := context.WithCancel(context.Background())

	env := &ControlPlaneTestEnvironment{
		T:                 t,
		ctx:               ctx,
		cancel:            cancel,
		TestDB:            nil,
		Queries:           queries,
		LeaseReaper:       leaseReaper,
		InactivityMonitor: inactivityMonitor,
		Scheduler:         scheduler,
		Ingest:            ingest,
		ClusterToken:      clusterToken,
		APIEndpoint:       "http://test-control-plane-2:8080",
		EnrollmentService: enrollmentService,
		WorkerService:     workerService,
		MonitorsService:   monitorsService,
		DashboardService:  dashboardService,
		UserService:       userService,
	}

	t.Cleanup(env.Shutdown)

	return env
}

func (env *ControlPlaneTestEnvironment) Context() context.Context {
	return env.ctx
}

func (env *ControlPlaneTestEnvironment) StartBackgroundServices() {
	env.startLoop("lease-reaper", env.LeaseReaper.Start)
	env.startLoop("inactivity-monitor", env.InactivityMonitor.Start)
	env.startLoop("scheduler", env.Scheduler.Start)
}

func (env *ControlPlaneTestEnvironment) startLoop(_ string, fn func(context.Context)) {
	env.wg.Add(1)
	go func() {
		defer env.wg.Done()
		fn(env.ctx)
	}()
}

func (env *ControlPlaneTestEnvironment) StartWorkerServer(t *testing.T, _ ...string) *httptest.Server {
	t.Helper()

	workerPath, workerHandler := openseerv1connect.NewWorkerServiceHandler(env.WorkerService)

	mux := http.NewServeMux()
	mux.Handle(workerPath, middleware.TokenAuthHandler(env.Queries, workerHandler))

	server := httptest.NewServer(mux)
	env.EnrollmentService.SetAPIEndpoint(server.URL)
	env.APIEndpoint = server.URL

	t.Cleanup(server.Close)
	return server
}

func (env *ControlPlaneTestEnvironment) StartEnrollmentServer(t *testing.T, _ ...string) *httptest.Server {
	t.Helper()

	enrollmentPath, handler := openseerv1connect.NewEnrollmentServiceHandler(env.EnrollmentService)

	mux := http.NewServeMux()
	mux.Handle(enrollmentPath, handler)

	server := httptest.NewServer(mux)

	t.Cleanup(server.Close)
	return server
}

func (env *ControlPlaneTestEnvironment) StartWebServer(t *testing.T) *httptest.Server {
	t.Helper()

	mux := http.NewServeMux()

	dashboardPath, dashboardHandler := openseerv1connect.NewDashboardServiceHandler(env.DashboardService)
	mux.Handle(dashboardPath, dashboardHandler)

	userPath, userHandler := openseerv1connect.NewUserServiceHandler(env.UserService)
	mux.Handle(userPath, userHandler)

	monitorsPath, monitorsHandler := openseerv1connect.NewMonitorsServiceHandler(env.MonitorsService)
	mux.Handle(monitorsPath, monitorsHandler)

	sessionMw := session.NewMiddleware(env.TestDB.DB)
	server := httptest.NewServer(sessionMw.WithSession(mux))

	t.Cleanup(server.Close)
	return server
}

func (env *ControlPlaneTestEnvironment) Shutdown() {
	env.cancel()
	env.wg.Wait()
}

func NewRequest[T any](msg *T) *connect.Request[T] {
	return connect.NewRequest(msg)
}

func WaitForJobCompletion(t *testing.T, queries *sqlc.Queries, runID string, timeout time.Duration) {
	t.Helper()
	require.Eventually(t, func() bool {
		job, err := queries.GetJobByRunID(context.Background(), runID)
		if err != nil {
			t.Logf("Failed to get job %s: %v", runID, err)
			return false
		}
		t.Logf("Job %s status: %s", runID, job.Status)
		return job.Status == "done"
	}, timeout, 200*time.Millisecond, fmt.Sprintf("Job %s should complete", runID))
}

func WaitForWorkerInactivity(t *testing.T, queries *sqlc.Queries, workerID string, timeout time.Duration) {
	t.Helper()
	require.Eventually(t, func() bool {
		worker, err := queries.GetWorkerByID(context.Background(), workerID)
		if err != nil {
			t.Logf("Failed to get worker %s: %v", workerID, err)
			return false
		}
		t.Logf("Worker %s status: %s", workerID, worker.Status)
		return worker.Status == "inactive"
	}, timeout, 1*time.Second, fmt.Sprintf("Worker %s should become inactive", workerID))
}

func WaitForWorkerDBRegistration(t *testing.T, queries *sqlc.Queries, hostname string, region string, timeout time.Duration) {
	t.Helper()
	require.Eventually(t, func() bool {
		workers, err := queries.GetActiveWorkers(context.Background())
		if err != nil {
			t.Logf("Failed to get active workers: %v", err)
			return false
		}
		for _, worker := range workers {
			if worker.Hostname.Valid && worker.Hostname.String == hostname && worker.Region == region && worker.Status == "active" {
				t.Logf("Worker %s found in DB with hostname %s, status %s", worker.ID, hostname, worker.Status)
				return true
			}
		}
		t.Logf("Worker with hostname %s and region %s not found in DB or not active yet", hostname, region)
		return false
	}, timeout, 100*time.Millisecond, fmt.Sprintf("Worker with hostname %s should be registered in database", hostname))
}

func WaitForMonitorResultByRunID(t *testing.T, queries *sqlc.Queries, monitorID string, runID string, timeout time.Duration) *sqlc.TsResultsRaw {
	t.Helper()
	var result *sqlc.TsResultsRaw
	require.Eventually(t, func() bool {
		results, err := queries.GetRecentResults(context.Background(), &sqlc.GetRecentResultsParams{MonitorID: monitorID, Limit: 20})
		if err != nil {
			t.Logf("Failed to get recent results for monitor %s: %v", monitorID, err)
			return false
		}
		for _, candidate := range results {
			if candidate.RunID == runID {
				result = candidate
				return true
			}
		}
		return false
	}, timeout, 200*time.Millisecond, fmt.Sprintf("Result for run %s should exist", runID))
	return result
}
