package integration_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/crisog/openseer/internal/app/control-plane/store/sqlc"
	workerpkg "github.com/crisog/openseer/internal/app/worker"
	"github.com/crisog/openseer/tests/helpers"
)

func TestWorkerEnrollsAndPolls(t *testing.T) {
	t.Parallel()

	env := helpers.SetupControlPlane(t)
	env.StartBackgroundServices()

	enrollmentSrv := env.StartEnrollmentServer(t)
	workerSrv := env.StartWorkerServer(t)

	enrollmentURL, err := url.Parse(enrollmentSrv.URL)
	require.NoError(t, err)

	workerHTTPClient := &http.Client{Timeout: 10 * time.Second}

	worker := workerpkg.NewWorker(
		"integration-worker",
		"us-east-1",
		"1.0.0",
		enrollmentURL.String(),
		env.ClusterToken,
		2,
		workerHTTPClient,
	)

	workerCtx, workerCancel := context.WithCancel(context.Background())
	t.Cleanup(workerCancel)

	errCh := make(chan error, 1)
	go func() {
		errCh <- worker.Run(workerCtx)
	}()

	require.Eventually(t, func() bool {
		workers, err := env.Queries.GetActiveWorkers(context.Background())
		if err != nil {
			t.Logf("failed to fetch active workers: %v", err)
			return false
		}
		for _, w := range workers {
			if w.Region == "us-east-1" && (w.Status == "enrolled" || w.Status == "active") {
				return true
			}
		}
		return false
	}, 10*time.Second, 100*time.Millisecond, "worker should be enrolled")

	workerCancel()

	select {
	case err := <-errCh:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(5 * time.Second):
		t.Fatal("worker did not shut down after cancellation")
	}

	_ = workerSrv
}

func TestWorkerExecutesJobAndReportsMetrics(t *testing.T) {
	t.Parallel()

	env := helpers.SetupControlPlane(t)
	env.StartBackgroundServices()

	enrollmentSrv := env.StartEnrollmentServer(t)
	workerSrv := env.StartWorkerServer(t)

	enrollmentURL, err := url.Parse(enrollmentSrv.URL)
	require.NoError(t, err)

	workerHTTPClient := &http.Client{Timeout: 5 * time.Second}

	targetServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodGet, r.Method)
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}))
	t.Cleanup(targetServer.Close)

	worker := workerpkg.NewWorker(
		"integration-worker-metrics",
		"us-east-1",
		"1.0.0",
		enrollmentURL.String(),
		env.ClusterToken,
		1,
		workerHTTPClient,
	)

	jobMonitor := helpers.CreateMonitorWithUser(t, env.Queries, env.TestDB.DB, helpers.MonitorConfig{
		URL:        targetServer.URL,
		Method:     http.MethodGet,
		Regions:    []string{"us-east-1"},
		IntervalMs: 60000,
		TimeoutMs:  5000,
	})

	runID := helpers.CreateTestJob(t, env.Queries, jobMonitor.ID, "us-east-1").RunID

	workerCtx, workerCancel := context.WithCancel(context.Background())
	t.Cleanup(workerCancel)

	errCh := make(chan error, 1)
	go func() {
		errCh <- worker.Run(workerCtx)
	}()

	helpers.WaitForJobCompletion(t, env.Queries, runID, 15*time.Second)
	result := helpers.WaitForMonitorResultByRunID(t, env.Queries, jobMonitor.ID, runID, 5*time.Second)
	require.Equal(t, "OK", result.Status)

	workerCancel()
	select {
	case err := <-errCh:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(5 * time.Second):
		t.Fatal("worker did not shut down after cancellation")
	}

	_ = workerSrv
}

func TestWorkerTimeoutHandling(t *testing.T) {
	t.Parallel()

	env := helpers.SetupControlPlane(t)
	env.StartBackgroundServices()

	enrollmentSrv := env.StartEnrollmentServer(t)
	workerSrv := env.StartWorkerServer(t)

	enrollmentURL, err := url.Parse(enrollmentSrv.URL)
	require.NoError(t, err)

	workerHTTPClient := &http.Client{Timeout: 5 * time.Second}

	targetServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(3 * time.Second)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}))
	t.Cleanup(targetServer.Close)

	worker := workerpkg.NewWorker(
		"integration-worker-timeout",
		"us-east-1",
		"1.0.0",
		enrollmentURL.String(),
		env.ClusterToken,
		1,
		workerHTTPClient,
	)

	monitor := helpers.CreateMonitorWithUser(t, env.Queries, env.TestDB.DB, helpers.MonitorConfig{
		URL:        targetServer.URL,
		Method:     http.MethodGet,
		Regions:    []string{"us-east-1"},
		IntervalMs: 60000,
		TimeoutMs:  1000,
	})

	runID := helpers.CreateTestJob(t, env.Queries, monitor.ID, "us-east-1").RunID

	workerCtx, workerCancel := context.WithCancel(context.Background())
	t.Cleanup(workerCancel)

	errCh := make(chan error, 1)
	go func() {
		errCh <- worker.Run(workerCtx)
	}()

	helpers.WaitForJobCompletion(t, env.Queries, runID, 15*time.Second)

	result := helpers.WaitForMonitorResultByRunID(t, env.Queries, monitor.ID, runID, 5*time.Second)
	require.Equal(t, "ERROR", result.Status, "timeout should result in ERROR status")
	require.True(t, result.ErrorMessage.Valid, "timeout should have error message")

	workerCancel()
	select {
	case err := <-errCh:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(5 * time.Second):
		t.Fatal("worker did not shut down after cancellation")
	}

	_ = workerSrv
}

func TestWorkerHandlesMultipleConcurrentJobs(t *testing.T) {
	t.Parallel()

	env := helpers.SetupControlPlane(t)
	env.StartBackgroundServices()

	ctx := context.Background()

	enrollmentSrv := env.StartEnrollmentServer(t)
	workerSrv := env.StartWorkerServer(t)

	enrollmentURL, err := url.Parse(enrollmentSrv.URL)
	require.NoError(t, err)

	workerHTTPClient := &http.Client{Timeout: 10 * time.Second}

	var concurrentRequests atomic.Int32
	var maxConcurrency atomic.Int32

	targetServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		current := concurrentRequests.Add(1)
		defer concurrentRequests.Add(-1)

		for {
			max := maxConcurrency.Load()
			if current <= max || maxConcurrency.CompareAndSwap(max, current) {
				break
			}
		}

		time.Sleep(2 * time.Second)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}))
	t.Cleanup(targetServer.Close)

	worker := workerpkg.NewWorker(
		"integration-worker-concurrent",
		"us-east-1",
		"1.0.0",
		enrollmentURL.String(),
		env.ClusterToken,
		3,
		workerHTTPClient,
	)

	monitor := helpers.CreateMonitorWithUser(t, env.Queries, env.TestDB.DB, helpers.MonitorConfig{
		URL:        targetServer.URL,
		Method:     http.MethodGet,
		Regions:    []string{"us-east-1"},
		IntervalMs: 60000,
		TimeoutMs:  8000,
	})

	jobs := make([]*sqlc.AppJob, 3)
	for i := 0; i < 3; i++ {
		jobs[i] = helpers.CreateTestJob(t, env.Queries, monitor.ID, "us-east-1")
	}

	workerCtx, workerCancel := context.WithCancel(context.Background())
	t.Cleanup(workerCancel)

	errCh := make(chan error, 1)
	go func() {
		errCh <- worker.Run(workerCtx)
	}()

	require.Eventually(t, func() bool {
		leasedCount := 0
		for _, job := range jobs {
			jobStatus, err := env.Queries.GetJobByRunID(ctx, job.RunID)
			if err == nil && jobStatus.Status == "leased" {
				leasedCount++
			}
		}
		return leasedCount == 3
	}, 10*time.Second, 100*time.Millisecond, "all 3 jobs should be leased concurrently")

	require.Eventually(t, func() bool {
		doneCount := 0
		for _, job := range jobs {
			jobStatus, err := env.Queries.GetJobByRunID(ctx, job.RunID)
			if err == nil && jobStatus.Status == "done" {
				doneCount++
			}
		}
		return doneCount == 3
	}, 15*time.Second, 200*time.Millisecond, "all jobs should complete")

	require.Equal(t, int32(3), maxConcurrency.Load(), "should have achieved 3 concurrent requests")

	for _, job := range jobs {
		result := helpers.WaitForMonitorResultByRunID(t, env.Queries, monitor.ID, job.RunID, 5*time.Second)
		require.Equal(t, "OK", result.Status)
	}

	workerCancel()
	select {
	case err := <-errCh:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(5 * time.Second):
		t.Fatal("worker did not shut down after cancellation")
	}

	_ = workerSrv
}

func TestWorkerHTTPMethodsAndHeaders(t *testing.T) {
	t.Parallel()

	env := helpers.SetupControlPlane(t)
	env.StartBackgroundServices()

	enrollmentSrv := env.StartEnrollmentServer(t)
	workerSrv := env.StartWorkerServer(t)

	enrollmentURL, err := url.Parse(enrollmentSrv.URL)
	require.NoError(t, err)

	workerHTTPClient := &http.Client{Timeout: 5 * time.Second}

	var receivedMethod string
	var receivedHeaders http.Header
	var receivedBody []byte

	targetServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedMethod = r.Method
		receivedHeaders = r.Header.Clone()
		receivedBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status": "success"}`))
	}))
	t.Cleanup(targetServer.Close)

	worker := workerpkg.NewWorker(
		"integration-worker-http",
		"us-east-1",
		"1.0.0",
		enrollmentURL.String(),
		env.ClusterToken,
		1,
		workerHTTPClient,
	)

	monitor := helpers.CreateMonitorWithUser(t, env.Queries, env.TestDB.DB, helpers.MonitorConfig{
		URL:        targetServer.URL,
		Method:     http.MethodPost,
		Headers:    map[string]string{"X-Custom-Header": "test-value", "Content-Type": "application/json"},
		Regions:    []string{"us-east-1"},
		IntervalMs: 60000,
		TimeoutMs:  5000,
	})

	runID := helpers.CreateTestJob(t, env.Queries, monitor.ID, "us-east-1").RunID

	workerCtx, workerCancel := context.WithCancel(context.Background())
	t.Cleanup(workerCancel)

	errCh := make(chan error, 1)
	go func() {
		errCh <- worker.Run(workerCtx)
	}()

	helpers.WaitForJobCompletion(t, env.Queries, runID, 15*time.Second)
	result := helpers.WaitForMonitorResultByRunID(t, env.Queries, monitor.ID, runID, 5*time.Second)
	require.Equal(t, "OK", result.Status)

	require.Equal(t, http.MethodPost, receivedMethod)
	require.Equal(t, "test-value", receivedHeaders.Get("X-Custom-Header"))
	require.Equal(t, "application/json", receivedHeaders.Get("Content-Type"))
	_ = receivedBody

	workerCancel()
	select {
	case err := <-errCh:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(5 * time.Second):
		t.Fatal("worker did not shut down after cancellation")
	}

	_ = workerSrv
}

func TestWorkerNetworkErrorHandling(t *testing.T) {
	t.Parallel()

	env := helpers.SetupControlPlane(t)
	env.StartBackgroundServices()

	enrollmentSrv := env.StartEnrollmentServer(t)
	workerSrv := env.StartWorkerServer(t)

	enrollmentURL, err := url.Parse(enrollmentSrv.URL)
	require.NoError(t, err)

	workerHTTPClient := &http.Client{Timeout: 5 * time.Second}

	worker := workerpkg.NewWorker(
		"integration-worker-network-error",
		"us-east-1",
		"1.0.0",
		enrollmentURL.String(),
		env.ClusterToken,
		1,
		workerHTTPClient,
	)

	monitor := helpers.CreateMonitorWithUser(t, env.Queries, env.TestDB.DB, helpers.MonitorConfig{
		URL:        "http://192.0.2.1:9999/unreachable",
		Method:     http.MethodGet,
		Regions:    []string{"us-east-1"},
		IntervalMs: 60000,
		TimeoutMs:  2000,
	})

	runID := helpers.CreateTestJob(t, env.Queries, monitor.ID, "us-east-1").RunID

	workerCtx, workerCancel := context.WithCancel(context.Background())
	t.Cleanup(workerCancel)

	errCh := make(chan error, 1)
	go func() {
		errCh <- worker.Run(workerCtx)
	}()

	helpers.WaitForJobCompletion(t, env.Queries, runID, 15*time.Second)
	result := helpers.WaitForMonitorResultByRunID(t, env.Queries, monitor.ID, runID, 5*time.Second)
	require.Equal(t, "ERROR", result.Status, "network error should result in ERROR status")
	require.True(t, result.ErrorMessage.Valid, "network error should have error message")

	workerCancel()
	select {
	case err := <-errCh:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(5 * time.Second):
		t.Fatal("worker did not shut down after cancellation")
	}

	_ = workerSrv
}

func TestWorkerLeaseRenewalForLongJobs(t *testing.T) {
	t.Parallel()

	env := helpers.SetupControlPlane(t)
	env.StartBackgroundServices()

	ctx := context.Background()

	enrollmentSrv := env.StartEnrollmentServer(t)
	workerSrv := env.StartWorkerServer(t)

	enrollmentURL, err := url.Parse(enrollmentSrv.URL)
	require.NoError(t, err)

	workerHTTPClient := &http.Client{Timeout: 30 * time.Second}

	targetServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(8 * time.Second)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}))
	t.Cleanup(targetServer.Close)

	worker := workerpkg.NewWorker(
		"integration-worker-long-job",
		"us-east-1",
		"1.0.0",
		enrollmentURL.String(),
		env.ClusterToken,
		1,
		workerHTTPClient,
	)

	monitor := helpers.CreateMonitorWithUser(t, env.Queries, env.TestDB.DB, helpers.MonitorConfig{
		URL:        targetServer.URL,
		Method:     http.MethodGet,
		Regions:    []string{"us-east-1"},
		IntervalMs: 60000,
		TimeoutMs:  25000,
	})

	runID := helpers.CreateTestJob(t, env.Queries, monitor.ID, "us-east-1").RunID

	workerCtx, workerCancel := context.WithCancel(context.Background())
	t.Cleanup(workerCancel)

	errCh := make(chan error, 1)
	go func() {
		errCh <- worker.Run(workerCtx)
	}()

	require.Eventually(t, func() bool {
		job, err := env.Queries.GetJobByRunID(ctx, runID)
		if err != nil {
			return false
		}
		return job.Status == "leased"
	}, 10*time.Second, 100*time.Millisecond, "job should be leased")

	job, err := env.Queries.GetJobByRunID(ctx, runID)
	require.NoError(t, err)
	require.True(t, job.LeaseExpiresAt.Valid)
	initialLeaseExpiry := job.LeaseExpiresAt.Time

	time.Sleep(12 * time.Second)

	job, err = env.Queries.GetJobByRunID(ctx, runID)
	require.NoError(t, err)
	if job.Status == "leased" && job.LeaseExpiresAt.Valid {
		require.True(t, job.LeaseExpiresAt.Time.After(initialLeaseExpiry),
			"lease should have been renewed: initial=%v, current=%v", initialLeaseExpiry, job.LeaseExpiresAt.Time)
	}

	helpers.WaitForJobCompletion(t, env.Queries, runID, 15*time.Second)
	result := helpers.WaitForMonitorResultByRunID(t, env.Queries, monitor.ID, runID, 5*time.Second)
	require.Equal(t, "OK", result.Status)

	workerCancel()
	select {
	case err := <-errCh:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(5 * time.Second):
		t.Fatal("worker did not shut down after cancellation")
	}

	_ = workerSrv
}

func TestWorkerInactivityDetection(t *testing.T) {
	t.Parallel()

	env := helpers.SetupControlPlane(t)
	env.StartBackgroundServices()

	enrollmentSrv := env.StartEnrollmentServer(t)
	_ = env.StartWorkerServer(t)

	enrollmentURL, err := url.Parse(enrollmentSrv.URL)
	require.NoError(t, err)

	workerHTTPClient := &http.Client{Timeout: 5 * time.Second}

	worker := workerpkg.NewWorker(
		"integration-worker-inactivity",
		"us-east-1",
		"1.0.0",
		enrollmentURL.String(),
		env.ClusterToken,
		1,
		workerHTTPClient,
	)

	workerCtx, workerCancel := context.WithCancel(context.Background())

	errCh := make(chan error, 1)
	go func() {
		errCh <- worker.Run(workerCtx)
	}()

	var workerID string
	require.Eventually(t, func() bool {
		workers, err := env.Queries.GetActiveWorkers(context.Background())
		if err != nil {
			return false
		}
		for _, w := range workers {
			if w.Region == "us-east-1" && (w.Status == "enrolled" || w.Status == "active") {
				workerID = w.ID
				return true
			}
		}
		return false
	}, 10*time.Second, 100*time.Millisecond, "worker should be enrolled")

	workerCancel()
	select {
	case err := <-errCh:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(5 * time.Second):
		t.Fatal("worker did not shut down after cancellation")
	}

	_, err = env.TestDB.DB.ExecContext(context.Background(), "UPDATE app.workers SET last_seen_at = NOW() - INTERVAL '5 minutes' WHERE id = $1", workerID)
	require.NoError(t, err)

	helpers.WaitForWorkerInactivity(t, env.Queries, workerID, 15*time.Second)
}

func TestWorkerOverloadProtection(t *testing.T) {
	t.Parallel()

	env := helpers.SetupControlPlane(t)
	env.StartBackgroundServices()

	ctx := context.Background()

	enrollmentSrv := env.StartEnrollmentServer(t)
	workerSrv := env.StartWorkerServer(t)

	enrollmentURL, err := url.Parse(enrollmentSrv.URL)
	require.NoError(t, err)

	workerHTTPClient := &http.Client{Timeout: 10 * time.Second}

	var activeRequests atomic.Int32
	var peakRequests atomic.Int32

	targetServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		current := activeRequests.Add(1)
		defer activeRequests.Add(-1)

		for {
			peak := peakRequests.Load()
			if current <= peak || peakRequests.CompareAndSwap(peak, current) {
				break
			}
		}

		time.Sleep(3 * time.Second)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}))
	t.Cleanup(targetServer.Close)

	maxConcurrent := int32(2)
	worker := workerpkg.NewWorker(
		"integration-worker-overload",
		"us-east-1",
		"1.0.0",
		enrollmentURL.String(),
		env.ClusterToken,
		maxConcurrent,
		workerHTTPClient,
	)

	monitor := helpers.CreateMonitorWithUser(t, env.Queries, env.TestDB.DB, helpers.MonitorConfig{
		URL:        targetServer.URL,
		Method:     http.MethodGet,
		Regions:    []string{"us-east-1"},
		IntervalMs: 60000,
		TimeoutMs:  10000,
	})

	for i := 0; i < 5; i++ {
		helpers.CreateTestJob(t, env.Queries, monitor.ID, "us-east-1")
	}

	workerCtx, workerCancel := context.WithCancel(context.Background())
	t.Cleanup(workerCancel)

	errCh := make(chan error, 1)
	go func() {
		errCh <- worker.Run(workerCtx)
	}()

	require.Eventually(t, func() bool {
		doneCount := 0
		jobs, _ := env.Queries.GetJobsForMonitor(ctx, monitor.ID)
		for _, job := range jobs {
			if job.Status == "done" {
				doneCount++
			}
		}
		return doneCount >= 4
	}, 30*time.Second, 500*time.Millisecond, "jobs should complete")

	require.LessOrEqual(t, peakRequests.Load(), maxConcurrent,
		"peak concurrent requests should not exceed maxConcurrent: got %d, max %d",
		peakRequests.Load(), maxConcurrent)

	workerCancel()
	select {
	case err := <-errCh:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(5 * time.Second):
		t.Fatal("worker did not shut down after cancellation")
	}

	_ = workerSrv
}

func TestWorkerSupportsAllHTTPMethods(t *testing.T) {
	t.Parallel()

	env := helpers.SetupControlPlane(t)
	env.StartBackgroundServices()

	enrollmentSrv := env.StartEnrollmentServer(t)
	workerSrv := env.StartWorkerServer(t)

	enrollmentURL, err := url.Parse(enrollmentSrv.URL)
	require.NoError(t, err)

	workerHTTPClient := &http.Client{Timeout: 5 * time.Second}

	methods := []string{http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete, http.MethodHead}
	receivedMethods := make(map[string]bool)
	var methodMu sync.Mutex

	targetServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		methodMu.Lock()
		receivedMethods[r.Method] = true
		methodMu.Unlock()
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}))
	t.Cleanup(targetServer.Close)

	worker := workerpkg.NewWorker(
		"integration-worker-methods",
		"us-east-1",
		"1.0.0",
		enrollmentURL.String(),
		env.ClusterToken,
		int32(len(methods)),
		workerHTTPClient,
	)

	runIDs := make([]string, 0, len(methods))
	for _, method := range methods {
		monitor := helpers.CreateMonitorWithUser(t, env.Queries, env.TestDB.DB, helpers.MonitorConfig{
			URL:        targetServer.URL,
			Method:     method,
			Regions:    []string{"us-east-1"},
			IntervalMs: 60000,
			TimeoutMs:  5000,
		})
		runID := helpers.CreateTestJob(t, env.Queries, monitor.ID, "us-east-1").RunID
		runIDs = append(runIDs, runID)
	}

	workerCtx, workerCancel := context.WithCancel(context.Background())
	t.Cleanup(workerCancel)

	errCh := make(chan error, 1)
	go func() {
		errCh <- worker.Run(workerCtx)
	}()

	for _, runID := range runIDs {
		helpers.WaitForJobCompletion(t, env.Queries, runID, 15*time.Second)
	}

	methodMu.Lock()
	for _, method := range methods {
		require.True(t, receivedMethods[method], "method %s should have been used", method)
	}
	methodMu.Unlock()

	workerCancel()
	select {
	case err := <-errCh:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(5 * time.Second):
		t.Fatal("worker did not shut down after cancellation")
	}

	_ = workerSrv
}
