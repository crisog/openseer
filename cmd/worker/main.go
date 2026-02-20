package main

import (
	"context"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/crisog/openseer/internal/app/worker"
)

func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

func getEnvInt32(key string, defaultValue int32) int32 {
	if str := os.Getenv(key); str != "" {
		if val, err := strconv.Atoi(str); err == nil {
			return int32(val)
		}
	}
	return defaultValue
}

func getEnvInt(key string, defaultValue int) int {
	if str := os.Getenv(key); str != "" {
		if val, err := strconv.Atoi(str); err == nil {
			return val
		}
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
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	workerID := getEnv("WORKER_ID", fmt.Sprintf("worker-%d", time.Now().UnixNano()))

	dialer := &net.Dialer{
		Timeout:   10 * time.Second,
		KeepAlive: 30 * time.Second,
	}

	transport := &http.Transport{
		Dial:                  dialer.Dial,
		MaxIdleConns:          100,
		MaxIdleConnsPerHost:   10,
		MaxConnsPerHost:       20,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 15 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	}

	hc := &http.Client{
		Transport: transport,
	}

	clusterToken := getEnv("CLUSTER_TOKEN", "")
	if clusterToken == "" {
		log.Fatal("CLUSTER_TOKEN environment variable is required and cannot be empty")
	}

	enrollmentURL := getEnv("ENROLLMENT_URL", "http://localhost:8080")
	maxConcurrency := getEnvInt32("MAX_CONCURRENCY", 5)
	pollBaseInterval := getEnvDuration("POLL_BASE_INTERVAL", 1*time.Second)
	pollMaxInterval := getEnvDuration("POLL_MAX_INTERVAL", 10*time.Second)
	resultSubmitMaxAttempts := getEnvInt("RESULT_SUBMIT_MAX_ATTEMPTS", 6)
	resultSubmitRetryInterval := getEnvDuration("RESULT_SUBMIT_RETRY_INTERVAL", 5*time.Second)
	resultSubmitTimeout := getEnvDuration("RESULT_SUBMIT_TIMEOUT", 10*time.Second)

	w := worker.NewWorker(
		workerID,
		getEnv("REGION", "us-east-1"),
		"0.4.0",
		enrollmentURL,
		clusterToken,
		maxConcurrency,
		hc,
	).
		WithPollingConfig(pollBaseInterval, pollMaxInterval).
		WithResultRetryConfig(resultSubmitMaxAttempts, resultSubmitRetryInterval, resultSubmitTimeout)

	log.Printf("Worker %s starting, max concurrency: %d, enrollment URL: %s", workerID, maxConcurrency, enrollmentURL)

	err := w.Run(ctx)

	if ctx.Err() == context.Canceled {
		log.Printf("Worker received shutdown signal, exiting gracefully...")
	} else if err != nil {
		log.Printf("Worker stopped with error: %v", err)
	} else {
		log.Printf("Worker stopped normally")
	}
}
