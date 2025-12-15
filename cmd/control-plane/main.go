package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
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
	"github.com/crisog/openseer/internal/app/control-plane/middleware"
	"github.com/crisog/openseer/internal/app/control-plane/store/sqlc"
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

	leaseReaperInterval := getEnvDuration("LEASE_REAPER_INTERVAL", 5*time.Second)
	workerInactivityInterval := getEnvDuration("WORKER_INACTIVITY_INTERVAL", 30*time.Second)
	schedulerInterval := getEnvDuration("SCHEDULER_POLL_INTERVAL", time.Second)
	jobLeaseDuration := getEnvDuration("JOB_LEASE_DURATION", 45*time.Second)

	leaseReaper := controlplane.NewLeaseReaper(queries, sqlDB, leaseReaperInterval)
	inactivityMonitor := controlplane.NewWorkerInactivityMonitor(queries, workerInactivityInterval)
	ing := metrics.New(queries)
	scheduler := controlplane.NewScheduler(queries, sqlDB, schedulerInterval)

	apiEndpoint := getEnv("API_ENDPOINT", "http://control-plane:8080")
	enrollmentService := enrollment.NewEnrollmentService(queries, logger, clusterToken, apiEndpoint)
	monitorsService := monitors.NewMonitorsService(queries, logger)
	dashboardService := dashboard.NewDashboardService(queries, logger)
	userService := user.NewUserService(queries, logger)
	workerService := worker.NewWorkerService(queries, logger, ing, jobLeaseDuration)

	mux := http.NewServeMux()

	enrollmentPath, enrollmentHandler := openseerv1connect.NewEnrollmentServiceHandler(enrollmentService)
	mux.Handle(enrollmentPath, enrollmentHandler)

	workerPath, workerHandler := openseerv1connect.NewWorkerServiceHandler(workerService)
	mux.Handle(workerPath, middleware.TokenAuthHandler(queries, workerHandler))

	monitorsPath, monitorsHandler := openseerv1connect.NewMonitorsServiceHandler(monitorsService)
	mux.Handle(monitorsPath, monitorsHandler)

	dashboardPath, dashboardHandler := openseerv1connect.NewDashboardServiceHandler(dashboardService)
	mux.Handle(dashboardPath, dashboardHandler)

	userPath, userHandler := openseerv1connect.NewUserServiceHandler(userService)
	mux.Handle(userPath, userHandler)

	sessionMw := session.NewMiddleware(sqlDB)

	corsOrigin := getEnv("CORS_ORIGIN", "http://localhost:3000")
	corsMiddleware := cors.New(cors.Options{
		AllowedOrigins:   []string{corsOrigin},
		AllowedMethods:   connectcors.AllowedMethods(),
		AllowedHeaders:   connectcors.AllowedHeaders(),
		ExposedHeaders:   connectcors.ExposedHeaders(),
		AllowCredentials: true,
	})

	port := getEnv("PORT", "8080")
	server := &http.Server{
		Addr:    ":" + port,
		Handler: corsMiddleware.Handler(sessionMw.WithSession(mux)),
	}

	var wg sync.WaitGroup

	wg.Add(1)
	go recovery.WithRecoverCallback("lease-reaper", func() {
		defer wg.Done()
		leaseReaper.Start(ctx)
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
	go recovery.WithRecoverCallback("worker-inactivity-monitor", func() {
		defer wg.Done()
		inactivityMonitor.Start(ctx)
	}, func(err error) {
		log.Printf("CRITICAL: Worker inactivity monitor crashed - stale worker statuses may linger: %v", err)
	})()

	wg.Add(1)
	go recovery.WithRecoverCallback("http-server", func() {
		defer wg.Done()
		log.Printf("HTTP server starting on :%s", port)
		if err := server.ListenAndServe(); err != http.ErrServerClosed {
			log.Printf("HTTP server error: %v", err)
		}
	}, func(err error) {
		log.Printf("CRITICAL: HTTP server crashed: %v", err)
	})()

	<-ctx.Done()
	log.Println("Shutting down gracefully...")

	log.Println("Stopping HTTP server...")
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer shutdownCancel()

	go func() {
		if err := server.Shutdown(shutdownCtx); err != nil {
			log.Printf("HTTP server shutdown error: %v", err)
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
