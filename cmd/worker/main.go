package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
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

	controlPlaneAddr := getEnv("CONTROL_PLANE_ADDR", "localhost:8081")
	enrollmentPort := getEnv("ENROLLMENT_PORT", "8082")
	controlHost := controlPlaneAddr
	if idx := strings.Index(controlPlaneAddr, ":"); idx >= 0 {
		controlHost = controlPlaneAddr[:idx]
	}
	enrollmentServerName := getEnv("ENROLLMENT_SERVER_NAME", controlHost)
	enrollmentCAPath := getEnv("ENROLLMENT_CA_FILE", "")

	var enrollmentTLSConfig *tls.Config
	if enrollmentCAPath != "" {
		caPEM, err := os.ReadFile(enrollmentCAPath)
		if err != nil {
			log.Fatalf("failed to read enrollment CA file %s: %v", enrollmentCAPath, err)
		}
		caPool := x509.NewCertPool()
		if !caPool.AppendCertsFromPEM(caPEM) {
			log.Fatalf("failed to parse enrollment CA certificate at %s", enrollmentCAPath)
		}
		enrollmentTLSConfig = &tls.Config{
			RootCAs:    caPool,
			MinVersion: tls.VersionTLS12,
			ServerName: enrollmentServerName,
		}
	}

	enrollmentScheme := getEnv("ENROLLMENT_SCHEME", "")
	if enrollmentScheme == "" {
		if enrollmentTLSConfig != nil {
			enrollmentScheme = "https"
		} else {
			enrollmentScheme = "http"
		}
	}

	maxConcurrency := getEnvInt32("MAX_CONCURRENCY", 5)

	w := worker.NewWorker(
		workerID,
		getEnv("REGION", "us-east-1"),
		"0.3.0",
		controlPlaneAddr,
		enrollmentPort,
		clusterToken,
		maxConcurrency,
		enrollmentScheme,
		enrollmentTLSConfig,
		hc,
	)

	log.Printf("Worker %s starting, max concurrency: %d, connecting to %s (pull-based job model)", workerID, maxConcurrency, controlPlaneAddr)

	err := w.Run(ctx)

	if ctx.Err() == context.Canceled {
		log.Printf("Worker received shutdown signal, exiting gracefully...")
	} else if err != nil {
		log.Printf("Worker stopped with error: %v", err)
	} else {
		log.Printf("Worker stopped normally")
	}
}
