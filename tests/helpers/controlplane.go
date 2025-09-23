package helpers

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"os"
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
	"github.com/crisog/openseer/internal/app/control-plane/store/sqlc"
	"github.com/crisog/openseer/internal/pkg/auth"
)

type ControlPlaneTestEnvironment struct {
	T *testing.T

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	TestDB *TestDB

	Queries    *sqlc.Queries
	Dispatcher *controlplane.Dispatcher
	Scheduler  *controlplane.Scheduler
	Ingest     *metrics.Ingest

	AuthService       *auth.AuthService
	PKI               *auth.PKI
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

	dataDir := t.TempDir()
	prevDataDir, hadPrev := os.LookupEnv("OPENSEER_DATA_DIR")
	require.NoError(t, os.Setenv("OPENSEER_DATA_DIR", dataDir))
	t.Cleanup(func() {
		if hadPrev {
			require.NoError(t, os.Setenv("OPENSEER_DATA_DIR", prevDataDir))
		} else {
			require.NoError(t, os.Unsetenv("OPENSEER_DATA_DIR"))
		}
	})

	pkiManager, err := auth.NewPKI()
	require.NoError(t, err)

	authService, err := auth.NewAuthService(clusterToken, pkiManager, logger)
	require.NoError(t, err)
	authService.StartCleanupWorker()

	dispatcher := controlplane.New(testDB.Queries, testDB.DB, 45*time.Second, 2*time.Second, 2*time.Second, 2*time.Second)
	ingest := metrics.New(testDB.Queries)
	scheduler := controlplane.NewScheduler(testDB.Queries, testDB.DB, 200*time.Millisecond)

	enrollmentService := enrollment.NewEnrollmentService(testDB.Queries, logger, pkiManager, clusterToken, "grpc://test-control-plane:8080")
	workerService := workerapi.NewWorkerService(testDB.Queries, logger, dispatcher, ingest, authService)
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
		Dispatcher:        dispatcher,
		Scheduler:         scheduler,
		Ingest:            ingest,
		AuthService:       authService,
		PKI:               pkiManager,
		ClusterToken:      clusterToken,
		APIEndpoint:       "grpc://test-control-plane:8080",
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

	dataDir := t.TempDir()
	prevDataDir, hadPrev := os.LookupEnv("OPENSEER_DATA_DIR")
	require.NoError(t, os.Setenv("OPENSEER_DATA_DIR", dataDir))
	t.Cleanup(func() {
		if hadPrev {
			require.NoError(t, os.Setenv("OPENSEER_DATA_DIR", prevDataDir))
		} else {
			require.NoError(t, os.Unsetenv("OPENSEER_DATA_DIR"))
		}
	})

	pkiManager, err := auth.NewPKI()
	require.NoError(t, err)

	authService, err := auth.NewAuthService(clusterToken, pkiManager, logger)
	require.NoError(t, err)
	authService.StartCleanupWorker()

	queries := sqlc.New(sharedDB)
	dispatcher := controlplane.New(queries, sharedDB, 45*time.Second, 2*time.Second, 2*time.Second, 2*time.Second)
	ingest := metrics.New(queries)
	scheduler := controlplane.NewScheduler(queries, sharedDB, 200*time.Millisecond)

	enrollmentService := enrollment.NewEnrollmentService(queries, logger, pkiManager, clusterToken, "grpc://test-control-plane-2:8080")
	workerService := workerapi.NewWorkerService(queries, logger, dispatcher, ingest, authService)
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
		Dispatcher:        dispatcher,
		Scheduler:         scheduler,
		Ingest:            ingest,
		AuthService:       authService,
		PKI:               pkiManager,
		ClusterToken:      clusterToken,
		APIEndpoint:       "grpc://test-control-plane-2:8080",
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
	env.startLoop("dispatcher-lease-reaper", env.Dispatcher.StartLeaseReaper)
	env.startLoop("dispatcher-stream-health", env.Dispatcher.StartStreamHealthMonitor)
	env.startLoop("dispatcher-worker-inactivity", env.Dispatcher.StartWorkerInactivityMonitor)
	env.startLoop("scheduler", env.Scheduler.Start)
}

func (env *ControlPlaneTestEnvironment) startLoop(_ string, fn func(context.Context)) {
	env.wg.Add(1)
	go func() {
		defer env.wg.Done()
		fn(env.ctx)
	}()
}

func (env *ControlPlaneTestEnvironment) StartWorkerServer(t *testing.T, hosts ...string) *httptest.Server {
	t.Helper()

	if len(hosts) == 0 {
		hosts = []string{"localhost"}
	}

	workerPath, workerHandler := openseerv1connect.NewWorkerServiceHandler(env.WorkerService)

	mux := http.NewServeMux()
	mux.Handle(workerPath, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		if r.TLS != nil && len(r.TLS.PeerCertificates) > 0 {
			cn := r.TLS.PeerCertificates[0].Subject.CommonName
			ctx = context.WithValue(ctx, workerapi.WorkerIDContextKey, cn)
		}
		workerHandler.ServeHTTP(w, r.WithContext(ctx))
	}))

	tlsConfig, err := env.AuthService.CreateServerTLSConfig(hosts)
	require.NoError(t, err)

	server := httptest.NewUnstartedServer(mux)
	server.EnableHTTP2 = true
	server.TLS = tlsConfig
	server.StartTLS()

	t.Cleanup(server.Close)
	return server
}

func (env *ControlPlaneTestEnvironment) StartEnrollmentServer(t *testing.T, hosts ...string) *httptest.Server {
	t.Helper()

	if len(hosts) == 0 {
		hosts = []string{"localhost"}
	}

	enrollmentPath, handler := openseerv1connect.NewEnrollmentServiceHandler(env.EnrollmentService)

	mux := http.NewServeMux()
	mux.Handle(enrollmentPath, handler)

	tlsConfig, err := env.AuthService.CreateWebServerTLSConfig(hosts)
	require.NoError(t, err)

	server := httptest.NewUnstartedServer(mux)
	server.EnableHTTP2 = true
	server.TLS = tlsConfig
	server.StartTLS()

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
