package main

import (
	"context"
	"crypto/tls"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/crisog/openseer/gen/openseer/v1/openseerv1connect"
	controlplane "github.com/crisog/openseer/internal/app/control-plane"
	"github.com/crisog/openseer/internal/app/control-plane/api/dashboard"
	"github.com/crisog/openseer/internal/app/control-plane/api/enrollment"
	"github.com/crisog/openseer/internal/app/control-plane/api/monitors"
	"github.com/crisog/openseer/internal/app/control-plane/api/user"
	"github.com/crisog/openseer/internal/app/control-plane/api/worker"
	"github.com/crisog/openseer/internal/app/control-plane/auth/session"
	metrics "github.com/crisog/openseer/internal/app/control-plane/metrics"
	"github.com/crisog/openseer/internal/app/control-plane/store/sqlc"
	"github.com/crisog/openseer/internal/pkg/auth"
	"github.com/crisog/openseer/internal/pkg/recovery"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"go.uber.org/zap"

	connectcors "connectrpc.com/cors"
	"github.com/rs/cors"
)

func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

func getEnvDuration(key string, defaultValue time.Duration) time.Duration {
	if value := os.Getenv(key); value != "" {
		dur, err := time.ParseDuration(value)
		if err != nil {
			log.Printf("Invalid duration for %s=%q: %v. Using default %v", key, value, err, defaultValue)
			return defaultValue
		}
		if dur <= 0 {
			log.Printf("Duration for %s must be positive. Using default %v", key, defaultValue)
			return defaultValue
		}
		return dur
	}
	return defaultValue
}

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	logger, err := zap.NewProduction()
	if err != nil {
		log.Fatalf("Failed to create logger: %v", err)
	}
	defer logger.Sync()

	databaseURL := getEnv("DATABASE_URL", "postgres://openseer:openseer@localhost:5432/openseer?sslmode=disable")

	config, err := pgx.ParseConfig(databaseURL)
	if err != nil {
		log.Fatalf("Failed to parse database URL: %v", err)
	}

	sqlDB := stdlib.OpenDB(*config)
	defer sqlDB.Close()

	if err := sqlDB.Ping(); err != nil {
		log.Fatalf("Failed to connect to database: %v", err)
	}

	queries := sqlc.New(sqlDB)
	log.Println("Database connected")

	clusterToken := getEnv("CLUSTER_TOKEN", "")
	if clusterToken == "" {
		log.Fatal("CLUSTER_TOKEN environment variable is required and cannot be empty")
	}

	betterAuthSecret := getEnv("BETTER_AUTH_SECRET", "")
	if betterAuthSecret == "" {
		log.Fatal("BETTER_AUTH_SECRET environment variable is required and cannot be empty")
	}
	pkiManager, err := auth.NewPKI()
	if err != nil {
		log.Fatalf("Failed to create PKI: %v", err)
	}

	authService, err := auth.NewAuthService(clusterToken, pkiManager, logger)
	if err != nil {
		log.Fatalf("Failed to create auth service: %v", err)
	}

	authService.StartCleanupWorker()

	leaseReaperInterval := getEnvDuration("LEASE_REAPER_INTERVAL", 5*time.Second)
	streamHealthInterval := getEnvDuration("STREAM_HEALTH_INTERVAL", 15*time.Second)
	workerInactivityInterval := getEnvDuration("WORKER_INACTIVITY_INTERVAL", 30*time.Second)
	schedulerInterval := getEnvDuration("SCHEDULER_POLL_INTERVAL", time.Second)

	disp := controlplane.New(queries, sqlDB, 30*time.Second, leaseReaperInterval, streamHealthInterval, workerInactivityInterval)
	ing := metrics.New(queries)
	scheduler := controlplane.NewScheduler(queries, sqlDB, schedulerInterval)

	apiEndpoint := getEnv("API_ENDPOINT", "grpc://control-plane:8080")
	enrollmentService := enrollment.NewEnrollmentService(queries, logger, pkiManager, clusterToken, apiEndpoint)
	monitorsService := monitors.NewMonitorsService(queries, logger)
	dashboardService := dashboard.NewDashboardService(queries, logger)
	userService := user.NewUserService(queries, logger)
	workerService := worker.NewWorkerService(queries, logger, disp, ing, authService)

	workerMux := http.NewServeMux()
	webMux := http.NewServeMux()

	enrollmentPath, enrollmentHandler := openseerv1connect.NewEnrollmentServiceHandler(enrollmentService)
	webMux.Handle(enrollmentPath, enrollmentHandler)

	workerPath, workerHandler := openseerv1connect.NewWorkerServiceHandler(workerService)
	workerMux.Handle(workerPath, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		if r.TLS != nil && len(r.TLS.PeerCertificates) > 0 {
			cn := r.TLS.PeerCertificates[0].Subject.CommonName
			ctx = context.WithValue(ctx, worker.WorkerIDContextKey, cn)
		}
		workerHandler.ServeHTTP(w, r.WithContext(ctx))
	}))

	monitorsPath, monitorsHandler := openseerv1connect.NewMonitorsServiceHandler(monitorsService)
	webMux.Handle(monitorsPath, monitorsHandler)

	dashboardPath, dashboardHandler := openseerv1connect.NewDashboardServiceHandler(dashboardService)
	webMux.Handle(dashboardPath, dashboardHandler)

	userPath, userHandler := openseerv1connect.NewUserServiceHandler(userService)
	webMux.Handle(userPath, userHandler)

	sessionMw := session.NewMiddleware(sqlDB)

	tlsHostsStr := getEnv("TLS_HOSTS", "localhost,control-plane,0.0.0.0")
	hosts := strings.Split(tlsHostsStr, ",")
	for i := range hosts {
		hosts[i] = strings.TrimSpace(hosts[i])
	}

	workerTLSConfig, err := authService.CreateServerTLSConfig(hosts)
	if err != nil {
		log.Fatalf("Failed to create worker TLS config: %v", err)
	}

	webTLSDisabled := getEnv("WEB_TLS_DISABLE", "") == "true"

	var webTLSConfig *tls.Config
	if !webTLSDisabled {
		webTLSConfig, err = authService.CreateWebServerTLSConfig(hosts)
		if err != nil {
			log.Fatalf("Failed to create web TLS config: %v", err)
		}
	}

	corsOrigin := getEnv("CORS_ORIGIN", "http://localhost:3000")
	corsMiddleware := cors.New(cors.Options{
		AllowedOrigins:   []string{corsOrigin},
		AllowedMethods:   connectcors.AllowedMethods(),
		AllowedHeaders:   connectcors.AllowedHeaders(),
		ExposedHeaders:   connectcors.ExposedHeaders(),
		AllowCredentials: true,
	})

	workerPort := getEnv("WORKER_PORT", "8081")
	workerServer := &http.Server{
		Addr:      ":" + workerPort,
		Handler:   workerMux,
		TLSConfig: workerTLSConfig,
	}

	webPort := getEnv("WEB_PORT", "8082")
	webServer := &http.Server{
		Addr:      ":" + webPort,
		Handler:   corsMiddleware.Handler(sessionMw.WithSession(webMux)),
		TLSConfig: webTLSConfig,
	}

	var wg sync.WaitGroup

	wg.Add(1)
	go recovery.WithRecoverCallback("dispatcher-lease-reaper", func() {
		defer wg.Done()
		disp.StartLeaseReaper(ctx)
	}, func(err error) {
		log.Printf("CRITICAL: Lease reaper crashed - jobs may not be reclaimed: %v", err)
	})()

	wg.Add(1)
	go recovery.WithRecoverCallback("scheduler", func() {
		defer wg.Done()
		scheduler.Start(ctx)
	}, func(err error) {
		log.Printf("CRITICAL: Scheduler crashed - no new monitoring jobs will be created: %v", err)
	})()

	wg.Add(1)
	go recovery.WithRecoverCallback("stream-health-monitor", func() {
		defer wg.Done()
		disp.StartStreamHealthMonitor(ctx)
	}, func(err error) {
		log.Printf("CRITICAL: Stream health monitor crashed - dead worker streams may not be detected: %v", err)
	})()

	wg.Add(1)
	go recovery.WithRecoverCallback("worker-inactivity-monitor", func() {
		defer wg.Done()
		disp.StartWorkerInactivityMonitor(ctx)
	}, func(err error) {
		log.Printf("CRITICAL: Worker inactivity monitor crashed - stale worker statuses may linger: %v", err)
	})()

	wg.Add(1)
	go recovery.WithRecoverCallback("worker-server", func() {
		defer wg.Done()
		log.Printf("Worker services starting with mTLS on :%s", workerPort)
		if err := workerServer.ListenAndServeTLS("", ""); err != http.ErrServerClosed {
			log.Printf("Worker server error: %v", err)
		}
	}, func(err error) {
		log.Printf("CRITICAL: Worker server crashed - workers cannot connect: %v", err)
	})()

	wg.Add(1)
	go recovery.WithRecoverCallback("web-server", func() {
		defer wg.Done()
		if webTLSDisabled {
			log.Printf("Web services starting without TLS on :%s", webPort)
			if err := webServer.ListenAndServe(); err != http.ErrServerClosed {
				log.Printf("Web server error: %v", err)
			}
			return
		}

		log.Printf("Web services starting with TLS on :%s", webPort)
		if err := webServer.ListenAndServeTLS("", ""); err != http.ErrServerClosed {
			log.Printf("Web server error: %v", err)
		}
	}, func(err error) {
		log.Printf("CRITICAL: Web server crashed - dashboard and API unavailable: %v", err)
	})()

	<-ctx.Done()
	log.Println("Shutting down gracefully...")

	log.Println("Stopping HTTP servers...")
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer shutdownCancel()

	go func() {
		if err := workerServer.Shutdown(shutdownCtx); err != nil {
			log.Printf("Worker server shutdown error: %v", err)
		}
	}()
	go func() {
		if err := webServer.Shutdown(shutdownCtx); err != nil {
			log.Printf("Web server shutdown error: %v", err)
		}
	}()

	log.Println("Stopping background services...")
	cancel()

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		log.Println("Shutdown complete - all services stopped cleanly")
	case <-time.After(15 * time.Second):
		log.Println("Shutdown timeout - forcing exit")
	}
}
