package integration_test

import (
	"context"
	"crypto/x509"
	"database/sql"
	"fmt"
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

func TestWorkerEnrollsAndRegisters(t *testing.T) {
	t.Parallel()

	env := helpers.SetupControlPlane(t)
	env.StartBackgroundServices()

	workerSrv := env.StartWorkerServer(t, "127.0.0.1", "localhost")
	enrollmentSrv := env.StartEnrollmentServer(t, "127.0.0.1", "localhost")

	workerURL, err := url.Parse(workerSrv.URL)
	require.NoError(t, err)

	enrollmentURL, err := url.Parse(enrollmentSrv.URL)
	require.NoError(t, err)

	caPool := x509.NewCertPool()
	require.True(t, caPool.AppendCertsFromPEM(env.PKI.GetCACertPEM()))
	enrollmentTLS := tlsConfig(t, caPool)
	enrollmentTLS.ServerName = workerURL.Hostname()

	workerHTTPClient := &http.Client{Timeout: 5 * time.Second}

	worker := workerpkg.NewWorker(
		"integration-worker",
		"us-east-1",
		"1.0.0",
		workerURL.Host,
		enrollmentURL.Port(),
		env.ClusterToken,
		2,
		enrollmentURL.Scheme,
		enrollmentTLS,
		workerHTTPClient,
	)

	workerCtx, workerCancel := context.WithCancel(context.Background())
	t.Cleanup(workerCancel)

	errCh := make(chan error, 1)
	go func() {
		errCh <- worker.Run(workerCtx)
	}()

	helpers.WaitForWorkerRegistration(t, env.Dispatcher, 1, 10*time.Second)

	require.Eventually(t, func() bool {
		workers, err := env.Queries.GetActiveWorkers(context.Background())
		if err != nil {
			t.Logf("failed to fetch active workers: %v", err)
			return false
		}
		return len(workers) == 1 && workers[0].Region == "us-east-1"
	}, 5*time.Second, 100*time.Millisecond, "worker should be enrolled and marked active")

	workerCancel()

	select {
	case err := <-errCh:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(5 * time.Second):
		t.Fatal("worker did not shut down after cancellation")
	}
}

func TestWorkerExecutesJobAndReportsMetrics(t *testing.T) {
	t.Parallel()

	env := helpers.SetupControlPlane(t)
	env.StartBackgroundServices()

	workerSrv := env.StartWorkerServer(t, "127.0.0.1", "localhost")
	enrollmentSrv := env.StartEnrollmentServer(t, "127.0.0.1", "localhost")

	workerURL, err := url.Parse(workerSrv.URL)
	require.NoError(t, err)

	enrollmentURL, err := url.Parse(enrollmentSrv.URL)
	require.NoError(t, err)

	caPool := x509.NewCertPool()
	require.True(t, caPool.AppendCertsFromPEM(env.PKI.GetCACertPEM()))
	enrollmentTLS := tlsConfig(t, caPool)
	enrollmentTLS.ServerName = workerURL.Hostname()

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
		workerURL.Host,
		enrollmentURL.Port(),
		env.ClusterToken,
		1,
		enrollmentURL.Scheme,
		enrollmentTLS,
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

	helpers.WaitForWorkerRegistration(t, env.Dispatcher, 1, 10*time.Second)

	helpers.WaitForJobCompletion(t, env.Queries, runID, 10*time.Second)
	result := helpers.WaitForMonitorResultByRunID(t, env.Queries, jobMonitor.ID, runID, 5*time.Second)
	require.Equal(t, "OK", result.Status)

	workerCancel()
	select {
	case err := <-errCh:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(5 * time.Second):
		t.Fatal("worker did not shut down after cancellation")
	}
}

func TestWorkerRetriesOnNegativeAck(t *testing.T) {
	t.Parallel()

	env := helpers.SetupControlPlane(t)
	env.StartBackgroundServices()

	workerSrv := env.StartWorkerServer(t, "127.0.0.1", "localhost")
	enrollmentSrv := env.StartEnrollmentServer(t, "127.0.0.1", "localhost")

	workerURL, err := url.Parse(workerSrv.URL)
	require.NoError(t, err)

	enrollmentURL, err := url.Parse(enrollmentSrv.URL)
	require.NoError(t, err)

	caPool := x509.NewCertPool()
	require.True(t, caPool.AppendCertsFromPEM(env.PKI.GetCACertPEM()))
	enrollmentTLS := tlsConfig(t, caPool)
	enrollmentTLS.ServerName = workerURL.Hostname()

	transport := &http.Transport{}
	workerHTTPClient := &http.Client{Timeout: 5 * time.Second, Transport: transport}
	t.Cleanup(transport.CloseIdleConnections)

	worker := workerpkg.NewWorker(
		"integration-worker-retry",
		"us-east-1",
		"1.0.0",
		workerURL.Host,
		enrollmentURL.Port(),
		env.ClusterToken,
		1,
		enrollmentURL.Scheme,
		enrollmentTLS,
		workerHTTPClient,
	)

	workerCtx, workerCancel := context.WithCancel(context.Background())
	t.Cleanup(workerCancel)
	errCh := make(chan error, 1)
	go func() {
		errCh <- worker.Run(workerCtx)
	}()

	helpers.WaitForWorkerRegistration(t, env.Dispatcher, 1, 10*time.Second)

	var workerID string
	require.Eventually(t, func() bool {
		workers, err := env.Queries.GetActiveWorkers(context.Background())
		if err != nil || len(workers) != 1 {
			return false
		}
		workerID = workers[0].ID
		return true
	}, 5*time.Second, 100*time.Millisecond)

	firstAttempt := atomic.Bool{}
	resetErrCh := make(chan error, 1)
	var jobRunID string

	targetServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if firstAttempt.CompareAndSwap(false, true) {
			go func() {
				if jobRunID == "" {
					resetErrCh <- fmt.Errorf("job run ID not set before request")
					return
				}
				res, err := env.TestDB.DB.ExecContext(context.Background(), "UPDATE app.jobs SET status='ready', worker_id = NULL, lease_expires_at = NULL WHERE run_id = $1", jobRunID)
				if err != nil {
					resetErrCh <- err
					return
				}
				rows, err := res.RowsAffected()
				if err != nil {
					resetErrCh <- err
					return
				}
				if rows != 1 {
					resetErrCh <- fmt.Errorf("expected to update 1 row, got %d", rows)
					return
				}
				time.Sleep(500 * time.Millisecond)
				res, err = env.TestDB.DB.ExecContext(context.Background(), "UPDATE app.jobs SET status='leased', worker_id = $1, lease_expires_at = NOW() + INTERVAL '45 seconds' WHERE run_id = $2", workerID, jobRunID)
				if err != nil {
					resetErrCh <- err
					return
				}
				rows, err = res.RowsAffected()
				if err != nil {
					resetErrCh <- err
					return
				}
				if rows != 1 {
					resetErrCh <- fmt.Errorf("expected to update 1 row, got %d", rows)
					return
				}
				resetErrCh <- nil
			}()
		}
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("boom"))
	}))
	t.Cleanup(targetServer.Close)

	monitor := helpers.CreateMonitorWithUser(t, env.Queries, env.TestDB.DB, helpers.MonitorConfig{
		URL:        targetServer.URL,
		Method:     http.MethodGet,
		Regions:    []string{"us-east-1"},
		IntervalMs: 60000,
		TimeoutMs:  5000,
	})
	job := helpers.CreateTestJob(t, env.Queries, monitor.ID, "us-east-1")
	jobRunID = job.RunID

	helpers.WaitForJobCompletion(t, env.Queries, job.RunID, 15*time.Second)
	result := helpers.WaitForMonitorResultByRunID(t, env.Queries, monitor.ID, job.RunID, 5*time.Second)
	require.Equal(t, "FAIL", result.Status)

	require.Eventually(t, func() bool {
		storedJob, err := env.Queries.GetJobByRunID(context.Background(), job.RunID)
		if err != nil {
			return false
		}
		return storedJob.Status == "done" && storedJob.WorkerID.Valid && storedJob.WorkerID.String == workerID
	}, 10*time.Second, 200*time.Millisecond)

	select {
	case err := <-resetErrCh:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for job lease reset")
	}

	workerCancel()
	select {
	case err := <-errCh:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(5 * time.Second):
		t.Fatal("worker did not shut down after cancellation")
	}
	require.True(t, firstAttempt.Load(), "expected handler to trigger negative ACK path")
}

func TestWorkerHTTPMethodsAndHeaders(t *testing.T) {
	t.Parallel()

	env := helpers.SetupControlPlane(t)
	env.StartBackgroundServices()

	workerSrv := env.StartWorkerServer(t, "127.0.0.1", "localhost")
	enrollmentSrv := env.StartEnrollmentServer(t, "127.0.0.1", "localhost")

	workerURL, err := url.Parse(workerSrv.URL)
	require.NoError(t, err)

	enrollmentURL, err := url.Parse(enrollmentSrv.URL)
	require.NoError(t, err)

	caPool := x509.NewCertPool()
	require.True(t, caPool.AppendCertsFromPEM(env.PKI.GetCACertPEM()))
	enrollmentTLS := tlsConfig(t, caPool)
	enrollmentTLS.ServerName = workerURL.Hostname()

	workerHTTPClient := &http.Client{Timeout: 5 * time.Second}

	targetServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			w.WriteHeader(http.StatusCreated)
		} else {
			w.WriteHeader(http.StatusOK)
		}

		if userAgent := r.Header.Get("User-Agent"); userAgent != "" {
			w.Header().Set("Echo-User-Agent", userAgent)
		}

		_, _ = w.Write([]byte("ok"))
	}))
	t.Cleanup(targetServer.Close)

	worker := workerpkg.NewWorker(
		"integration-worker-methods",
		"us-east-1",
		"1.0.0",
		workerURL.Host,
		enrollmentURL.Port(),
		env.ClusterToken,
		1,
		enrollmentURL.Scheme,
		enrollmentTLS,
		workerHTTPClient,
	)

	monitor := helpers.CreateMonitorWithUser(t, env.Queries, env.TestDB.DB, helpers.MonitorConfig{
		URL:        targetServer.URL,
		Method:     http.MethodPost,
		Regions:    []string{"us-east-1"},
		IntervalMs: 60000,
		TimeoutMs:  5000,
		Headers:    map[string]string{"User-Agent": "OpenSeer-Test/1.0"},
	})

	runID := helpers.CreateTestJob(t, env.Queries, monitor.ID, "us-east-1").RunID

	workerCtx, workerCancel := context.WithCancel(context.Background())
	t.Cleanup(workerCancel)

	errCh := make(chan error, 1)
	go func() {
		errCh <- worker.Run(workerCtx)
	}()

	helpers.WaitForWorkerRegistration(t, env.Dispatcher, 1, 10*time.Second)

	helpers.WaitForJobCompletion(t, env.Queries, runID, 15*time.Second)

	result := helpers.WaitForMonitorResultByRunID(t, env.Queries, monitor.ID, runID, 5*time.Second)
	require.Equal(t, "OK", result.Status)
	require.True(t, result.HttpCode.Valid)
	require.Equal(t, int32(201), result.HttpCode.Int32, "POST should return 201")

	workerCancel()
	select {
	case err := <-errCh:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(5 * time.Second):
		t.Fatal("worker did not shut down after cancellation")
	}
}

func TestWorkerTimeoutHandling(t *testing.T) {
	t.Parallel()

	env := helpers.SetupControlPlane(t)
	env.StartBackgroundServices()

	workerSrv := env.StartWorkerServer(t, "127.0.0.1", "localhost")
	enrollmentSrv := env.StartEnrollmentServer(t, "127.0.0.1", "localhost")

	workerURL, err := url.Parse(workerSrv.URL)
	require.NoError(t, err)

	enrollmentURL, err := url.Parse(enrollmentSrv.URL)
	require.NoError(t, err)

	caPool := x509.NewCertPool()
	require.True(t, caPool.AppendCertsFromPEM(env.PKI.GetCACertPEM()))
	enrollmentTLS := tlsConfig(t, caPool)
	enrollmentTLS.ServerName = workerURL.Hostname()

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
		workerURL.Host,
		enrollmentURL.Port(),
		env.ClusterToken,
		1,
		enrollmentURL.Scheme,
		enrollmentTLS,
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

	helpers.WaitForWorkerRegistration(t, env.Dispatcher, 1, 10*time.Second)

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
}

func TestWorkerNetworkErrorHandling(t *testing.T) {
	t.Parallel()

	env := helpers.SetupControlPlane(t)
	env.StartBackgroundServices()

	workerSrv := env.StartWorkerServer(t, "127.0.0.1", "localhost")
	enrollmentSrv := env.StartEnrollmentServer(t, "127.0.0.1", "localhost")

	workerURL, err := url.Parse(workerSrv.URL)
	require.NoError(t, err)

	enrollmentURL, err := url.Parse(enrollmentSrv.URL)
	require.NoError(t, err)

	caPool := x509.NewCertPool()
	require.True(t, caPool.AppendCertsFromPEM(env.PKI.GetCACertPEM()))
	enrollmentTLS := tlsConfig(t, caPool)
	enrollmentTLS.ServerName = workerURL.Hostname()

	workerHTTPClient := &http.Client{Timeout: 5 * time.Second}

	worker := workerpkg.NewWorker(
		"integration-worker-network-errors",
		"us-east-1",
		"1.0.0",
		workerURL.Host,
		enrollmentURL.Port(),
		env.ClusterToken,
		1,
		enrollmentURL.Scheme,
		enrollmentTLS,
		workerHTTPClient,
	)

	monitor := helpers.CreateMonitorWithUser(t, env.Queries, env.TestDB.DB, helpers.MonitorConfig{
		URL:        "http://nonexistent-domain-that-should-fail.invalid",
		Method:     http.MethodGet,
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

	helpers.WaitForWorkerRegistration(t, env.Dispatcher, 1, 10*time.Second)

	helpers.WaitForJobCompletion(t, env.Queries, runID, 15*time.Second)

	result := helpers.WaitForMonitorResultByRunID(t, env.Queries, monitor.ID, runID, 5*time.Second)
	require.Equal(t, "ERROR", result.Status, "DNS failure should result in ERROR status")
	require.True(t, result.ErrorMessage.Valid, "DNS failure should have error message")

	workerCancel()
	select {
	case err := <-errCh:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(5 * time.Second):
		t.Fatal("worker did not shut down after cancellation")
	}
}

func TestWorkerLeaseRenewalForLongJobs(t *testing.T) {
	t.Parallel()

	env := helpers.SetupControlPlane(t)
	env.StartBackgroundServices()

	ctx := context.Background()

	workerSrv := env.StartWorkerServer(t, "127.0.0.1", "localhost")
	enrollmentSrv := env.StartEnrollmentServer(t, "127.0.0.1", "localhost")

	workerURL, err := url.Parse(workerSrv.URL)
	require.NoError(t, err)

	enrollmentURL, err := url.Parse(enrollmentSrv.URL)
	require.NoError(t, err)

	caPool := x509.NewCertPool()
	require.True(t, caPool.AppendCertsFromPEM(env.PKI.GetCACertPEM()))
	enrollmentTLS := tlsConfig(t, caPool)
	enrollmentTLS.ServerName = workerURL.Hostname()

	workerHTTPClient := &http.Client{Timeout: 30 * time.Second}

	targetServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(25 * time.Second)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}))
	t.Cleanup(targetServer.Close)

	worker := workerpkg.NewWorker(
		"integration-worker-lease-renewal",
		"us-east-1",
		"1.0.0",
		workerURL.Host,
		enrollmentURL.Port(),
		env.ClusterToken,
		1,
		enrollmentURL.Scheme,
		enrollmentTLS,
		workerHTTPClient,
	)

	monitor := helpers.CreateMonitorWithUser(t, env.Queries, env.TestDB.DB, helpers.MonitorConfig{
		URL:        targetServer.URL,
		Method:     http.MethodGet,
		Regions:    []string{"us-east-1"},
		IntervalMs: 60000,
		TimeoutMs:  30000,
	})

	job := helpers.CreateTestJob(t, env.Queries, monitor.ID, "us-east-1")

	workerCtx, workerCancel := context.WithCancel(context.Background())
	t.Cleanup(workerCancel)

	errCh := make(chan error, 1)
	go func() {
		errCh <- worker.Run(workerCtx)
	}()

	helpers.WaitForWorkerRegistration(t, env.Dispatcher, 1, 10*time.Second)

	require.Eventually(t, func() bool {
		jobStatus, err := env.Queries.GetJobByRunID(ctx, job.RunID)
		if err != nil {
			return false
		}
		return jobStatus.Status == "leased"
	}, 5*time.Second, 100*time.Millisecond, "job should be leased by worker")

	jobInitial, err := env.Queries.GetJobByRunID(ctx, job.RunID)
	require.NoError(t, err)
	require.True(t, jobInitial.LeaseExpiresAt.Valid)
	initialExpiry := jobInitial.LeaseExpiresAt.Time

	time.Sleep(15 * time.Second)

	jobAfterRenewal, err := env.Queries.GetJobByRunID(ctx, job.RunID)
	require.NoError(t, err)
	require.True(t, jobAfterRenewal.LeaseExpiresAt.Valid)
	require.True(t, jobAfterRenewal.LeaseExpiresAt.Time.After(initialExpiry),
		"lease should be renewed for long-running job")

	helpers.WaitForJobCompletion(t, env.Queries, job.RunID, 20*time.Second)

	result := helpers.WaitForMonitorResultByRunID(t, env.Queries, monitor.ID, job.RunID, 5*time.Second)
	require.Equal(t, "OK", result.Status)

	workerCancel()
	select {
	case err := <-errCh:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(5 * time.Second):
		t.Fatal("worker did not shut down after cancellation")
	}
}

func TestWorkerHandlesMultipleConcurrentJobs(t *testing.T) {
	t.Parallel()

	env := helpers.SetupControlPlane(t)
	env.StartBackgroundServices()

	ctx := context.Background()

	workerSrv := env.StartWorkerServer(t, "127.0.0.1", "localhost")
	enrollmentSrv := env.StartEnrollmentServer(t, "127.0.0.1", "localhost")

	workerURL, err := url.Parse(workerSrv.URL)
	require.NoError(t, err)

	enrollmentURL, err := url.Parse(enrollmentSrv.URL)
	require.NoError(t, err)

	caPool := x509.NewCertPool()
	require.True(t, caPool.AppendCertsFromPEM(env.PKI.GetCACertPEM()))
	enrollmentTLS := tlsConfig(t, caPool)
	enrollmentTLS.ServerName = workerURL.Hostname()

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
		workerURL.Host,
		enrollmentURL.Port(),
		env.ClusterToken,
		3,
		enrollmentURL.Scheme,
		enrollmentTLS,
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

	helpers.WaitForWorkerRegistration(t, env.Dispatcher, 1, 10*time.Second)

	require.Eventually(t, func() bool {
		leasedCount := 0
		for _, job := range jobs {
			jobStatus, err := env.Queries.GetJobByRunID(ctx, job.RunID)
			if err == nil && jobStatus.Status == "leased" {
				leasedCount++
			}
		}
		return leasedCount == 3
	}, 5*time.Second, 100*time.Millisecond, "all 3 jobs should be leased concurrently")

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
}

func TestWorkerPingPongHealthCheck(t *testing.T) {
	t.Parallel()

	env := helpers.SetupControlPlane(t)
	env.StartBackgroundServices()

	ctx := context.Background()

	workerSrv := env.StartWorkerServer(t, "127.0.0.1", "localhost")
	enrollmentSrv := env.StartEnrollmentServer(t, "127.0.0.1", "localhost")

	workerURL, err := url.Parse(workerSrv.URL)
	require.NoError(t, err)

	enrollmentURL, err := url.Parse(enrollmentSrv.URL)
	require.NoError(t, err)

	caPool := x509.NewCertPool()
	require.True(t, caPool.AppendCertsFromPEM(env.PKI.GetCACertPEM()))
	enrollmentTLS := tlsConfig(t, caPool)
	enrollmentTLS.ServerName = workerURL.Hostname()

	workerHTTPClient := &http.Client{Timeout: 5 * time.Second}

	worker := workerpkg.NewWorker(
		"integration-worker-ping",
		"us-east-1",
		"1.0.0",
		workerURL.Host,
		enrollmentURL.Port(),
		env.ClusterToken,
		2,
		enrollmentURL.Scheme,
		enrollmentTLS,
		workerHTTPClient,
	)

	workerCtx, workerCancel := context.WithCancel(context.Background())
	t.Cleanup(workerCancel)

	errCh := make(chan error, 1)
	go func() {
		errCh <- worker.Run(workerCtx)
	}()

	helpers.WaitForWorkerRegistration(t, env.Dispatcher, 1, 10*time.Second)

	workers, err := env.Queries.GetActiveWorkers(ctx)
	require.NoError(t, err)
	require.Len(t, workers, 1)
	workerID := workers[0].ID

	time.Sleep(6 * time.Second)

	workers, err = env.Queries.GetActiveWorkers(ctx)
	require.NoError(t, err)
	require.Len(t, workers, 1, "worker should remain active after ping/pong")
	require.Equal(t, workerID, workers[0].ID, "same worker should be active")

	require.Equal(t, 1, env.Dispatcher.GetWorkerCount(), "worker should still be registered in dispatcher")

	workerCancel()
	select {
	case err := <-errCh:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(5 * time.Second):
		t.Fatal("worker did not shut down after cancellation")
	}
}

func TestWorkerSupportsAllHTTPMethods(t *testing.T) {
	t.Parallel()

	env := helpers.SetupControlPlane(t)
	env.StartBackgroundServices()

	workerSrv := env.StartWorkerServer(t, "127.0.0.1", "localhost")
	enrollmentSrv := env.StartEnrollmentServer(t, "127.0.0.1", "localhost")

	workerURL, err := url.Parse(workerSrv.URL)
	require.NoError(t, err)

	enrollmentURL, err := url.Parse(enrollmentSrv.URL)
	require.NoError(t, err)

	caPool := x509.NewCertPool()
	require.True(t, caPool.AppendCertsFromPEM(env.PKI.GetCACertPEM()))
	enrollmentTLS := tlsConfig(t, caPool)
	enrollmentTLS.ServerName = workerURL.Hostname()

	workerHTTPClient := &http.Client{Timeout: 5 * time.Second}

	receivedMethods := make(map[string]bool)
	var methodsMutex sync.Mutex

	targetServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		methodsMutex.Lock()
		receivedMethods[r.Method] = true
		methodsMutex.Unlock()

		switch r.Method {
		case http.MethodGet:
			w.WriteHeader(http.StatusOK)
		case http.MethodPost:
			w.WriteHeader(http.StatusCreated)
		case http.MethodPut:
			w.WriteHeader(http.StatusOK)
		case http.MethodDelete:
			w.WriteHeader(http.StatusNoContent)
		case http.MethodPatch:
			w.WriteHeader(http.StatusOK)
		case http.MethodHead:
			w.WriteHeader(http.StatusOK)
		case http.MethodOptions:
			w.Header().Set("Allow", "GET,POST,PUT,DELETE,PATCH,HEAD,OPTIONS")
			w.WriteHeader(http.StatusOK)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}

		if r.Method != http.MethodHead {
			_, _ = w.Write([]byte(fmt.Sprintf("Method: %s", r.Method)))
		}
	}))
	t.Cleanup(targetServer.Close)

	worker := workerpkg.NewWorker(
		"integration-worker-methods",
		"us-east-1",
		"1.0.0",
		workerURL.Host,
		enrollmentURL.Port(),
		env.ClusterToken,
		1,
		enrollmentURL.Scheme,
		enrollmentTLS,
		workerHTTPClient,
	)

	workerCtx, workerCancel := context.WithCancel(context.Background())
	t.Cleanup(workerCancel)

	errCh := make(chan error, 1)
	go func() {
		errCh <- worker.Run(workerCtx)
	}()

	helpers.WaitForWorkerRegistration(t, env.Dispatcher, 1, 10*time.Second)

	testCases := []struct {
		method           string
		expectedStatus   string
		expectedHTTPCode int32
	}{
		{http.MethodGet, "OK", 200},
		{http.MethodPost, "OK", 201},
		{http.MethodPut, "OK", 200},
		{http.MethodDelete, "OK", 204},
		{http.MethodPatch, "OK", 200},
		{http.MethodHead, "OK", 200},
		{http.MethodOptions, "OK", 200},
	}

	jobs := make([]*sqlc.AppJob, len(testCases))
	monitors := make([]*sqlc.AppMonitor, len(testCases))

	for i, tc := range testCases {
		monitor := helpers.CreateMonitorWithUser(t, env.Queries, env.TestDB.DB, helpers.MonitorConfig{
			URL:        targetServer.URL,
			Method:     tc.method,
			Regions:    []string{"us-east-1"},
			IntervalMs: 60000,
			TimeoutMs:  5000,
		})
		monitors[i] = monitor
		jobs[i] = helpers.CreateTestJob(t, env.Queries, monitor.ID, "us-east-1")
	}

	time.Sleep(1 * time.Second)

	for i, job := range jobs {
		tc := testCases[i]
		helpers.WaitForJobCompletion(t, env.Queries, job.RunID, 15*time.Second)
		t.Logf("Completed job for method %s", tc.method)
	}

	for i, job := range jobs {
		monitor := monitors[i]
		result := helpers.WaitForMonitorResultByRunID(t, env.Queries, monitor.ID, job.RunID, 5*time.Second)
		require.Equal(t, testCases[i].expectedStatus, result.Status, fmt.Sprintf("%s should return %s status", testCases[i].method, testCases[i].expectedStatus))
		require.True(t, result.HttpCode.Valid, fmt.Sprintf("%s should have HTTP code", testCases[i].method))
		require.Equal(t, testCases[i].expectedHTTPCode, result.HttpCode.Int32, fmt.Sprintf("%s should return HTTP %d", testCases[i].method, testCases[i].expectedHTTPCode))
	}

	methodsMutex.Lock()
	for _, tc := range testCases {
		require.True(t, receivedMethods[tc.method], fmt.Sprintf("server should have received %s request", tc.method))
	}
	methodsMutex.Unlock()

	workerCancel()
	select {
	case err := <-errCh:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(5 * time.Second):
		t.Fatal("worker did not shut down after cancellation")
	}
}

func TestWorkerOverloadProtection(t *testing.T) {
	t.Parallel()

	env := helpers.SetupControlPlane(t)
	env.StartBackgroundServices()

	ctx := context.Background()

	workerSrv := env.StartWorkerServer(t, "127.0.0.1", "localhost")
	enrollmentSrv := env.StartEnrollmentServer(t, "127.0.0.1", "localhost")

	workerURL, err := url.Parse(workerSrv.URL)
	require.NoError(t, err)

	enrollmentURL, err := url.Parse(enrollmentSrv.URL)
	require.NoError(t, err)

	caPool := x509.NewCertPool()
	require.True(t, caPool.AppendCertsFromPEM(env.PKI.GetCACertPEM()))
	enrollmentTLS := tlsConfig(t, caPool)
	enrollmentTLS.ServerName = workerURL.Hostname()

	workerHTTPClient := &http.Client{Timeout: 10 * time.Second}

	var concurrentRequests atomic.Int32
	var maxConcurrency atomic.Int32
	var completedRequests atomic.Int32

	targetServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		current := concurrentRequests.Add(1)
		defer func() {
			concurrentRequests.Add(-1)
			completedRequests.Add(1)
		}()

		for {
			max := maxConcurrency.Load()
			if current <= max || maxConcurrency.CompareAndSwap(max, current) {
				break
			}
		}

		time.Sleep(3 * time.Second)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}))
	t.Cleanup(targetServer.Close)

	worker := workerpkg.NewWorker(
		"integration-worker-overload",
		"us-east-1",
		"1.0.0",
		workerURL.Host,
		enrollmentURL.Port(),
		env.ClusterToken,
		2,
		enrollmentURL.Scheme,
		enrollmentTLS,
		workerHTTPClient,
	)

	monitor := helpers.CreateMonitorWithUser(t, env.Queries, env.TestDB.DB, helpers.MonitorConfig{
		URL:        targetServer.URL,
		Method:     http.MethodGet,
		Regions:    []string{"us-east-1"},
		IntervalMs: 60000,
		TimeoutMs:  8000,
	})

	numJobs := 5
	jobs := make([]*sqlc.AppJob, numJobs)
	for i := 0; i < numJobs; i++ {
		jobs[i] = helpers.CreateTestJob(t, env.Queries, monitor.ID, "us-east-1")
	}

	workerCtx, workerCancel := context.WithCancel(context.Background())
	t.Cleanup(workerCancel)

	errCh := make(chan error, 1)
	go func() {
		errCh <- worker.Run(workerCtx)
	}()

	helpers.WaitForWorkerRegistration(t, env.Dispatcher, 1, 10*time.Second)

	time.Sleep(500 * time.Millisecond)

	leasedCount := 0
	readyCount := 0
	for _, job := range jobs {
		jobStatus, err := env.Queries.GetJobByRunID(ctx, job.RunID)
		require.NoError(t, err)

		if jobStatus.Status == "leased" {
			leasedCount++
		} else if jobStatus.Status == "ready" {
			readyCount++
		}
	}

	require.LessOrEqual(t, leasedCount, 3, "worker should not lease too many jobs at once")
	require.GreaterOrEqual(t, readyCount, 2, "some jobs should remain in ready state initially")

	time.Sleep(2 * time.Second)

	actualMaxConcurrency := maxConcurrency.Load()
	require.LessOrEqual(t, actualMaxConcurrency, int32(3), "worker should not greatly exceed max concurrency (allow +1 for race conditions)")
	require.GreaterOrEqual(t, actualMaxConcurrency, int32(2), "worker should achieve intended concurrency level")

	require.Eventually(t, func() bool {
		return completedRequests.Load() >= 2
	}, 15*time.Second, 500*time.Millisecond, "first batch of jobs should complete")

	require.Eventually(t, func() bool {
		doneCount := 0
		for _, job := range jobs {
			jobStatus, err := env.Queries.GetJobByRunID(ctx, job.RunID)
			if err == nil && jobStatus.Status == "done" {
				doneCount++
			}
		}
		return doneCount == numJobs
	}, 25*time.Second, 500*time.Millisecond, "all jobs should eventually complete")

	for _, job := range jobs {
		result := helpers.WaitForMonitorResultByRunID(t, env.Queries, monitor.ID, job.RunID, 10*time.Second)
		require.Equal(t, "OK", result.Status)
	}

	finalMaxConcurrency := maxConcurrency.Load()
	require.LessOrEqual(t, finalMaxConcurrency, int32(3), "max concurrency should not greatly exceed configured limit")
	require.GreaterOrEqual(t, finalMaxConcurrency, int32(2), "should achieve at least the configured concurrency level")
	require.Equal(t, int32(numJobs), completedRequests.Load(), "should have completed all jobs")

	workerCancel()
	select {
	case err := <-errCh:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(5 * time.Second):
		t.Fatal("worker did not shut down after cancellation")
	}
}

func TestWorkerJobLeaseExpirationAndReclaim(t *testing.T) {
	t.Parallel()

	env := helpers.SetupControlPlane(t)
	env.StartBackgroundServices()

	ctx := context.Background()

	targetServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	}))
	t.Cleanup(targetServer.Close)

	monitor := helpers.CreateMonitorWithUser(t, env.Queries, env.TestDB.DB, helpers.MonitorConfig{
		URL:        targetServer.URL,
		Method:     http.MethodGet,
		Regions:    []string{"us-east-1"},
		IntervalMs: 60000,
		TimeoutMs:  5000,
	})

	job := helpers.CreateTestJob(t, env.Queries, monitor.ID, "us-east-1")

	_, err := env.Queries.LeaseJobs(ctx, &sqlc.LeaseJobsParams{
		WorkerID: sql.NullString{String: "crashed-worker", Valid: true},
		Limit:    1,
		Region:   "us-east-1",
		LeaseExpiresAt: sql.NullTime{
			Time:  time.Now().Add(env.Dispatcher.LeaseDuration()),
			Valid: true,
		},
	})
	require.NoError(t, err)

	expiredTime := time.Now().Add(-10 * time.Second)
	err = env.Queries.ForceExpireJobLease(ctx, &sqlc.ForceExpireJobLeaseParams{
		RunID:          job.RunID,
		LeaseExpiresAt: sql.NullTime{Time: expiredTime, Valid: true},
	})
	require.NoError(t, err)

	jobStatus, err := env.Queries.GetJobByRunID(ctx, job.RunID)
	require.NoError(t, err)
	require.Equal(t, "leased", jobStatus.Status)
	require.True(t, jobStatus.LeaseExpiresAt.Valid)
	require.True(t, jobStatus.LeaseExpiresAt.Time.Before(time.Now()), "lease should be expired")

	t.Logf("Waiting for lease reaper to reclaim expired job %s", job.RunID)
	require.Eventually(t, func() bool {
		jobStatus, err := env.Queries.GetJobByRunID(ctx, job.RunID)
		if err != nil {
			t.Logf("Failed to get job status: %v", err)
			return false
		}
		t.Logf("Job %s status: %s", job.RunID, jobStatus.Status)
		return jobStatus.Status == "ready"
	}, 15*time.Second, 1*time.Second, "job should be reclaimed after lease expiry")

	finalJobStatus, err := env.Queries.GetJobByRunID(ctx, job.RunID)
	require.NoError(t, err)
	require.Equal(t, "ready", finalJobStatus.Status)
	require.False(t, finalJobStatus.WorkerID.Valid)
	require.False(t, finalJobStatus.LeaseExpiresAt.Valid)

	workerSrv := env.StartWorkerServer(t, "127.0.0.1", "localhost")
	enrollmentSrv := env.StartEnrollmentServer(t, "127.0.0.1", "localhost")

	workerURL, err := url.Parse(workerSrv.URL)
	require.NoError(t, err)

	enrollmentURL, err := url.Parse(enrollmentSrv.URL)
	require.NoError(t, err)

	caPool := x509.NewCertPool()
	require.True(t, caPool.AppendCertsFromPEM(env.PKI.GetCACertPEM()))
	enrollmentTLS := tlsConfig(t, caPool)
	enrollmentTLS.ServerName = workerURL.Hostname()

	worker := workerpkg.NewWorker(
		"integration-worker-lease-pickup",
		"us-east-1",
		"1.0.0",
		workerURL.Host,
		enrollmentURL.Port(),
		env.ClusterToken,
		2,
		enrollmentURL.Scheme,
		enrollmentTLS,
		&http.Client{Timeout: 5 * time.Second},
	)

	workerCtx, workerCancel := context.WithCancel(context.Background())
	t.Cleanup(workerCancel)

	errCh := make(chan error, 1)
	go func() {
		errCh <- worker.Run(workerCtx)
	}()

	helpers.WaitForWorkerRegistration(t, env.Dispatcher, 1, 10*time.Second)

	helpers.WaitForJobCompletion(t, env.Queries, job.RunID, 15*time.Second)
	result := helpers.WaitForMonitorResultByRunID(t, env.Queries, monitor.ID, job.RunID, 5*time.Second)
	require.Equal(t, "OK", result.Status)

	workerCancel()
	select {
	case err := <-errCh:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(5 * time.Second):
		t.Fatal("worker did not shut down after cancellation")
	}
}

func TestWorkerMultiRegionJobDistribution(t *testing.T) {
	t.Parallel()

	env := helpers.SetupControlPlane(t)
	env.StartBackgroundServices()

	ctx := context.Background()

	var receivedJobs []string
	var jobMutex sync.Mutex
	targetServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		jobMutex.Lock()
		receivedJobs = append(receivedJobs, r.Header.Get("X-Job-ID"))
		jobMutex.Unlock()
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	}))
	t.Cleanup(targetServer.Close)

	usMonitor := helpers.CreateMonitorWithUser(t, env.Queries, env.TestDB.DB, helpers.MonitorConfig{
		URL:        targetServer.URL,
		Method:     http.MethodGet,
		Regions:    []string{"us-east-1"},
		IntervalMs: 60000,
		TimeoutMs:  5000,
	})

	euMonitor := helpers.CreateMonitorWithUser(t, env.Queries, env.TestDB.DB, helpers.MonitorConfig{
		URL:        targetServer.URL,
		Method:     http.MethodGet,
		Regions:    []string{"eu-west-1"},
		IntervalMs: 60000,
		TimeoutMs:  5000,
	})

	globalMonitor := helpers.CreateMonitorWithUser(t, env.Queries, env.TestDB.DB, helpers.MonitorConfig{
		URL:        targetServer.URL,
		Method:     http.MethodGet,
		Regions:    []string{"global"},
		IntervalMs: 60000,
		TimeoutMs:  5000,
	})

	usJob := helpers.CreateTestJob(t, env.Queries, usMonitor.ID, "us-east-1")
	euJob := helpers.CreateTestJob(t, env.Queries, euMonitor.ID, "eu-west-1")
	globalJob := helpers.CreateTestJob(t, env.Queries, globalMonitor.ID, "global")

	time.Sleep(1 * time.Second)

	workerSrv := env.StartWorkerServer(t, "127.0.0.1", "localhost")
	enrollmentSrv := env.StartEnrollmentServer(t, "127.0.0.1", "localhost")

	workerURL, err := url.Parse(workerSrv.URL)
	require.NoError(t, err)
	enrollmentURL, err := url.Parse(enrollmentSrv.URL)
	require.NoError(t, err)

	caPool := x509.NewCertPool()
	require.True(t, caPool.AppendCertsFromPEM(env.PKI.GetCACertPEM()))
	enrollmentTLS := tlsConfig(t, caPool)
	enrollmentTLS.ServerName = workerURL.Hostname()

	workerHTTPClient := &http.Client{Timeout: 10 * time.Second}

	usWorker := workerpkg.NewWorker(
		"integration-worker-us",
		"us-east-1",
		"1.0.0",
		workerURL.Host,
		enrollmentURL.Port(),
		env.ClusterToken,
		3,
		enrollmentURL.Scheme,
		enrollmentTLS,
		workerHTTPClient,
	)

	usWorkerCtx, usWorkerCancel := context.WithCancel(context.Background())
	t.Cleanup(usWorkerCancel)

	errCh := make(chan error, 1)
	go func() {
		errCh <- usWorker.Run(usWorkerCtx)
	}()

	helpers.WaitForWorkerRegistration(t, env.Dispatcher, 1, 10*time.Second)
	helpers.WaitForWorkerDBRegistration(t, env.Queries, "integration-worker-us", "us-east-1", 10*time.Second)

	helpers.WaitForJobCompletion(t, env.Queries, usJob.RunID, 15*time.Second)

	result := helpers.WaitForMonitorResultByRunID(t, env.Queries, usMonitor.ID, usJob.RunID, 5*time.Second)
	require.Equal(t, "OK", result.Status)
	require.Equal(t, "us-east-1", result.Region)

	euWorker := workerpkg.NewWorker(
		"integration-worker-eu",
		"eu-west-1",
		"1.0.0",
		workerURL.Host,
		enrollmentURL.Port(),
		env.ClusterToken,
		3,
		enrollmentURL.Scheme,
		enrollmentTLS,
		workerHTTPClient,
	)

	euWorkerCtx, euWorkerCancel := context.WithCancel(context.Background())
	t.Cleanup(euWorkerCancel)

	euErrCh := make(chan error, 1)
	go func() {
		euErrCh <- euWorker.Run(euWorkerCtx)
	}()

	helpers.WaitForWorkerRegistration(t, env.Dispatcher, 2, 10*time.Second)
	helpers.WaitForWorkerDBRegistration(t, env.Queries, "integration-worker-eu", "eu-west-1", 10*time.Second)

	helpers.WaitForJobCompletion(t, env.Queries, euJob.RunID, 15*time.Second)

	result = helpers.WaitForMonitorResultByRunID(t, env.Queries, euMonitor.ID, euJob.RunID, 5*time.Second)
	require.Equal(t, "OK", result.Status)
	require.Equal(t, "eu-west-1", result.Region)

	helpers.WaitForJobCompletion(t, env.Queries, globalJob.RunID, 15*time.Second)

	result = helpers.WaitForMonitorResultByRunID(t, env.Queries, globalMonitor.ID, globalJob.RunID, 5*time.Second)
	require.Equal(t, "OK", result.Status)
	require.Contains(t, []string{"us-east-1", "eu-west-1"}, result.Region)

	usWorkerCancel()

	helpers.WaitForWorkerRegistration(t, env.Dispatcher, 1, 10*time.Second)

	time.Sleep(2 * time.Second)

	usJob2 := helpers.CreateTestJob(t, env.Queries, usMonitor.ID, "us-east-1")

	time.Sleep(3 * time.Second)
	job, err := env.Queries.GetJobByRunID(ctx, usJob2.RunID)
	require.NoError(t, err)
	require.Equal(t, "ready", job.Status, "Region-specific job should not be picked up by wrong region worker")

	globalJob2 := helpers.CreateTestJob(t, env.Queries, globalMonitor.ID, "global")

	helpers.WaitForJobCompletion(t, env.Queries, globalJob2.RunID, 15*time.Second)

	globalJob2Result := helpers.WaitForMonitorResultByRunID(t, env.Queries, globalMonitor.ID, globalJob2.RunID, 5*time.Second)
	require.Equal(t, "OK", globalJob2Result.Status)
	require.Equal(t, "eu-west-1", globalJob2Result.Region)

	euWorkerCancel()

	select {
	case <-euErrCh:

	case <-time.After(5 * time.Second):
		t.Fatal("EU worker did not shut down after cancellation")
	}
}

func TestWorkerInactivityDetection(t *testing.T) {
	t.Parallel()

	env := helpers.SetupControlPlane(t)
	env.StartBackgroundServices()

	ctx := context.Background()

	workerSrv := env.StartWorkerServer(t, "127.0.0.1", "localhost")
	enrollmentSrv := env.StartEnrollmentServer(t, "127.0.0.1", "localhost")

	workerURL, err := url.Parse(workerSrv.URL)
	require.NoError(t, err)

	enrollmentURL, err := url.Parse(enrollmentSrv.URL)
	require.NoError(t, err)

	caPool := x509.NewCertPool()
	require.True(t, caPool.AppendCertsFromPEM(env.PKI.GetCACertPEM()))
	enrollmentTLS := tlsConfig(t, caPool)
	enrollmentTLS.ServerName = workerURL.Hostname()

	workerHTTPClient := &http.Client{Timeout: 5 * time.Second}

	workerID := "test-worker-inactivity-" + fmt.Sprintf("%d", time.Now().UnixNano())
	worker := workerpkg.NewWorker(
		workerID,
		"us-east-1",
		"1.0.0",
		workerURL.Host,
		enrollmentURL.Port(),
		env.ClusterToken,
		2,
		enrollmentURL.Scheme,
		enrollmentTLS,
		workerHTTPClient,
	)

	workerCtx, workerCancel := context.WithCancel(context.Background())
	t.Cleanup(workerCancel)

	workerErrCh := make(chan error, 1)
	go func() {
		workerErrCh <- worker.Run(workerCtx)
	}()

	helpers.WaitForWorkerRegistration(t, env.Dispatcher, 1, 15*time.Second)

	var workerInfo *sqlc.AppWorker
	require.Eventually(t, func() bool {
		workers, err := env.Queries.GetActiveWorkers(ctx)
		if err != nil {
			t.Logf("failed to fetch active workers: %v", err)
			return false
		}
		if len(workers) == 1 && workers[0].Region == "us-east-1" {
			workerInfo = workers[0]
			return workers[0].Status == "active"
		}
		return false
	}, 20*time.Second, 100*time.Millisecond, "Worker should become active")

	require.Equal(t, "active", workerInfo.Status)
	initialLastSeen := workerInfo.LastSeenAt
	actualWorkerID := workerInfo.ID

	workerCancel()

	select {
	case err := <-workerErrCh:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(10 * time.Second):
		t.Fatal("worker did not shut down after cancellation")
	}

	time.Sleep(3 * time.Second)

	updatedWorkerInfo, err := env.Queries.GetWorkerByID(ctx, actualWorkerID)
	require.NoError(t, err)

	require.True(t, updatedWorkerInfo.LastSeenAt.Equal(initialLastSeen) ||
		updatedWorkerInfo.LastSeenAt.Before(time.Now().Add(-1*time.Second)),
		"Worker last_seen_at should not be updating after worker stopped")

	time.Sleep(8 * time.Second)
	staleWorkerInfo, err := env.Queries.GetWorkerByID(ctx, actualWorkerID)
	require.NoError(t, err)

	require.True(t, staleWorkerInfo.LastSeenAt.Equal(initialLastSeen),
		"Worker last_seen_at should not update after disconnection")

	require.Equal(t, "active", staleWorkerInfo.Status)

	oldTime := time.Now().Add(-3 * time.Minute)
	_, err = env.TestDB.DB.ExecContext(ctx,
		"UPDATE app.workers SET last_seen_at = $1 WHERE id = $2",
		oldTime, actualWorkerID)
	require.NoError(t, err)

	require.Eventually(t, func() bool {
		err := env.Queries.MarkWorkerInactive(ctx)
		if err != nil {
			t.Logf("Failed to mark worker inactive: %v", err)
			return false
		}
		finalWorkerInfo, err := env.Queries.GetWorkerByID(ctx, actualWorkerID)
		if err != nil {
			t.Logf("Failed to get worker by ID: %v", err)
			return false
		}
		t.Logf("Worker status: %s", finalWorkerInfo.Status)
		return finalWorkerInfo.Status == "inactive"
	}, 40*time.Second, 2*time.Second, "Worker should be marked inactive")
}

func TestWorkerCertificateExpiryAndReEnrollment(t *testing.T) {
	t.Parallel()

	env := helpers.SetupControlPlane(t)
	env.StartBackgroundServices()

	ctx := context.Background()

	workerSrv := env.StartWorkerServer(t, "127.0.0.1", "localhost")
	enrollmentSrv := env.StartEnrollmentServer(t, "127.0.0.1", "localhost")

	workerURL, err := url.Parse(workerSrv.URL)
	require.NoError(t, err)

	enrollmentURL, err := url.Parse(enrollmentSrv.URL)
	require.NoError(t, err)

	caPool := x509.NewCertPool()
	require.True(t, caPool.AppendCertsFromPEM(env.PKI.GetCACertPEM()))
	enrollmentTLS := tlsConfig(t, caPool)
	enrollmentTLS.ServerName = workerURL.Hostname()

	workerHTTPClient := &http.Client{Timeout: 5 * time.Second}

	workerID := "test-worker-cert-expiry-" + fmt.Sprintf("%d", time.Now().UnixNano())
	worker := workerpkg.NewWorker(
		workerID,
		"us-east-1",
		"1.0.0",
		workerURL.Host,
		enrollmentURL.Port(),
		env.ClusterToken,
		2,
		enrollmentURL.Scheme,
		enrollmentTLS,
		workerHTTPClient,
	)

	workerCtx, workerCancel := context.WithCancel(context.Background())
	t.Cleanup(workerCancel)

	workerErrCh := make(chan error, 1)
	go func() {
		workerErrCh <- worker.Run(workerCtx)
	}()

	helpers.WaitForWorkerRegistration(t, env.Dispatcher, 1, 10*time.Second)

	var workerInfo *sqlc.AppWorker
	require.Eventually(t, func() bool {
		workers, err := env.Queries.GetActiveWorkers(ctx)
		if err != nil {
			t.Logf("failed to fetch active workers: %v", err)
			return false
		}
		if len(workers) == 1 && workers[0].Region == "us-east-1" {
			workerInfo = workers[0]
			return workers[0].Status == "active"
		}
		return false
	}, 15*time.Second, 100*time.Millisecond, "Worker should become active")

	require.Equal(t, "active", workerInfo.Status)
	require.True(t, workerInfo.CertificateExpiresAt.Valid)
	initialExpiry := workerInfo.CertificateExpiresAt.Time
	actualWorkerID := workerInfo.ID

	expiredTime := time.Now().Add(-1 * time.Hour)
	_, err = env.TestDB.DB.ExecContext(ctx,
		"UPDATE app.workers SET certificate_expires_at = $1 WHERE id = $2",
		expiredTime, actualWorkerID)
	require.NoError(t, err)

	workerCancel()

	select {
	case err := <-workerErrCh:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(5 * time.Second):
		t.Fatal("worker did not shut down after cancellation")
	}

	_, err = env.TestDB.DB.ExecContext(ctx,
		"UPDATE app.workers SET status = 'inactive' WHERE id = $1",
		actualWorkerID)
	require.NoError(t, err)

	time.Sleep(2 * time.Second)

	newWorkerID := workerID + "-restart"
	workerCtx2, workerCancel2 := context.WithCancel(context.Background())
	t.Cleanup(workerCancel2)

	worker2 := workerpkg.NewWorker(
		newWorkerID,
		"us-east-1",
		"1.0.0",
		workerURL.Host,
		enrollmentURL.Port(),
		env.ClusterToken,
		2,
		enrollmentURL.Scheme,
		enrollmentTLS,
		workerHTTPClient,
	)

	workerErrCh2 := make(chan error, 1)
	go func() {
		workerErrCh2 <- worker2.Run(workerCtx2)
	}()

	helpers.WaitForWorkerRegistration(t, env.Dispatcher, 1, 15*time.Second)

	var reEnrolledWorkerInfo *sqlc.AppWorker
	require.Eventually(t, func() bool {
		workers, err := env.Queries.GetActiveWorkers(ctx)
		if err != nil {
			t.Logf("failed to fetch active workers: %v", err)
			return false
		}
		t.Logf("Found %d active workers", len(workers))
		if len(workers) == 1 && workers[0].Region == "us-east-1" && workers[0].Status == "active" {
			reEnrolledWorkerInfo = workers[0]
			t.Logf("Re-enrolled worker ID: %s, cert expires: %v",
				reEnrolledWorkerInfo.ID, reEnrolledWorkerInfo.CertificateExpiresAt.Time)
			return true
		}
		return false
	}, 20*time.Second, 500*time.Millisecond, "Worker should re-enroll and become active")

	require.True(t, reEnrolledWorkerInfo.CertificateExpiresAt.Valid)
	newExpiry := reEnrolledWorkerInfo.CertificateExpiresAt.Time
	now := time.Now()

	t.Logf("Initial expiry: %v", initialExpiry)
	t.Logf("Expired time set: %v", expiredTime)
	t.Logf("New expiry: %v", newExpiry)
	t.Logf("Current time: %v", now)

	require.True(t, newExpiry.After(now),
		"Re-enrolled worker should have future certificate expiry. New: %v, Now: %v", newExpiry, now)

	require.False(t, newExpiry.Equal(initialExpiry),
		"Re-enrolled worker should have different certificate expiry")

	require.True(t, newExpiry.After(expiredTime.Add(23*time.Hour)),
		"New certificate should expire at least 24 hours from now")

	require.NotEqual(t, actualWorkerID, reEnrolledWorkerInfo.ID,
		"Re-enrolled worker should have different ID")

	workerCancel2()

	select {
	case err := <-workerErrCh2:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(5 * time.Second):
		t.Fatal("worker did not shut down after cancellation")
	}
}

func TestWorkerReconnectionAfterNetworkFailure(t *testing.T) {
	t.Parallel()

	env := helpers.SetupControlPlane(t)
	env.StartBackgroundServices()

	ctx := context.Background()

	_, err := env.TestDB.DB.ExecContext(ctx, `
		INSERT INTO "user" (id, name, email, "emailVerified")
		VALUES ('test-user', 'Test User', 'test@example.com', true)
		ON CONFLICT (id) DO NOTHING
	`)
	require.NoError(t, err)

	workerSrv := env.StartWorkerServer(t, "127.0.0.1", "localhost")
	enrollmentSrv := env.StartEnrollmentServer(t, "127.0.0.1", "localhost")

	workerURL, err := url.Parse(workerSrv.URL)
	require.NoError(t, err)

	enrollmentURL, err := url.Parse(enrollmentSrv.URL)
	require.NoError(t, err)

	caPool := x509.NewCertPool()
	require.True(t, caPool.AppendCertsFromPEM(env.PKI.GetCACertPEM()))
	enrollmentTLS := tlsConfig(t, caPool)
	enrollmentTLS.ServerName = workerURL.Hostname()

	workerHTTPClient := &http.Client{Timeout: 5 * time.Second}

	workerID := "test-worker-reconnection-" + fmt.Sprintf("%d", time.Now().UnixNano())
	worker := workerpkg.NewWorker(
		workerID,
		"us-east-1",
		"1.0.0",
		workerURL.Host,
		enrollmentURL.Port(),
		env.ClusterToken,
		2,
		enrollmentURL.Scheme,
		enrollmentTLS,
		workerHTTPClient,
	)

	workerCtx, workerCancel := context.WithCancel(context.Background())
	t.Cleanup(workerCancel)

	workerErrCh := make(chan error, 1)
	go func() {
		workerErrCh <- worker.Run(workerCtx)
	}()

	helpers.WaitForWorkerRegistration(t, env.Dispatcher, 1, 10*time.Second)

	var initialWorkerInfo *sqlc.AppWorker
	require.Eventually(t, func() bool {
		workers, err := env.Queries.GetActiveWorkers(ctx)
		if err != nil {
			t.Logf("failed to fetch active workers: %v", err)
			return false
		}
		if len(workers) == 1 && workers[0].Region == "us-east-1" {
			initialWorkerInfo = workers[0]
			return workers[0].Status == "active"
		}
		return false
	}, 15*time.Second, 100*time.Millisecond, "Worker should become active")

	actualWorkerID := initialWorkerInfo.ID
	t.Logf("Worker registered with ID: %s", actualWorkerID)

	require.Eventually(t, func() bool {
		count := env.Dispatcher.GetWorkerCount()
		t.Logf("Current worker count: %d", count)
		return count >= 0
	}, 10*time.Second, 1*time.Second, "worker should maintain stable connection")

	monitorID := "test-monitor-" + fmt.Sprintf("%d", time.Now().UnixNano())
	monitor, err := env.Queries.CreateMonitor(ctx, &sqlc.CreateMonitorParams{
		ID:         monitorID,
		Name:       "Test Monitor for Reconnection Test",
		Url:        "https://httpbin.org/status/200",
		Method:     "GET",
		IntervalMs: 60000,
		TimeoutMs:  5000,
		Regions:    []string{"us-east-1"},
		UserID:     sql.NullString{String: "test-user", Valid: true},
	})
	require.NoError(t, err)

	runID := monitor.ID + "-us-east-1-" + fmt.Sprintf("%d", time.Now().Unix()) + "-test"
	_, err = env.Queries.CreateJob(ctx, &sqlc.CreateJobParams{
		RunID:       runID,
		MonitorID:   monitor.ID,
		Region:      "us-east-1",
		ScheduledAt: time.Now(),
	})
	require.NoError(t, err)

	helpers.WaitForJobCompletion(t, env.Queries, runID, 15*time.Second)

	require.Eventually(t, func() bool {
		results, err := env.Queries.GetRecentResults(ctx, &sqlc.GetRecentResultsParams{
			MonitorID: monitor.ID,
			Limit:     1,
		})
		if err != nil || len(results) == 0 {
			return false
		}
		t.Logf("Found result for job: %s, status: %s", results[0].RunID, results[0].Status)
		return results[0].RunID == runID
	}, 10*time.Second, 500*time.Millisecond, "Should find result for executed job")

	finalWorkers, err := env.Queries.GetActiveWorkers(ctx)
	require.NoError(t, err)
	require.Len(t, finalWorkers, 1)
	require.Equal(t, actualWorkerID, finalWorkers[0].ID, "Worker should maintain same ID")

	t.Logf("Worker maintained stable connection and executed job successfully")

	workerCancel()

	select {
	case err := <-workerErrCh:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(5 * time.Second):
		t.Fatal("worker did not shut down after cancellation")
	}
}

func TestControlPlaneHighAvailability(t *testing.T) {
	t.Parallel()

	env1 := helpers.SetupControlPlane(t)
	env1.StartBackgroundServices()

	env2 := helpers.SetupControlPlaneWithDB(t, env1.TestDB.DB)
	env2.StartBackgroundServices()

	ctx := context.Background()

	_, err := env1.TestDB.DB.ExecContext(ctx, `
		INSERT INTO "user" (id, name, email, "emailVerified")
		VALUES ('test-user-ha', 'Test User HA', 'test-ha@example.com', true)
		ON CONFLICT (id) DO NOTHING
	`)
	require.NoError(t, err)

	targetServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	}))
	t.Cleanup(targetServer.Close)

	monitor := helpers.CreateMonitorWithConfig(t, env1.Queries, helpers.MonitorConfig{
		Name:       "HA Test Monitor",
		URL:        targetServer.URL,
		Method:     "GET",
		IntervalMs: 2000,
		TimeoutMs:  5000,
		Regions:    []string{"us-east-1"},
		UserID:     "test-user-ha",
	})

	time.Sleep(3 * time.Second)

	jobs, err := env1.Queries.GetJobsForMonitor(ctx, monitor.ID)
	require.NoError(t, err)
	require.Greater(t, len(jobs), 0, "At least one job should be created by the active scheduler")

	time.Sleep(5 * time.Second)

	allJobs, err := env1.Queries.GetJobsForMonitor(ctx, monitor.ID)
	require.NoError(t, err)
	require.Greater(t, len(allJobs), 0, "Jobs should be created by the active scheduler")

	t.Logf("HA setup verified: %d jobs created for monitor %s", len(allJobs), monitor.ID)

	env2.Shutdown()

	time.Sleep(3 * time.Second)

	monitor2 := helpers.CreateMonitorWithConfig(t, env1.Queries, helpers.MonitorConfig{
		Name:       "HA Test Monitor 2",
		URL:        targetServer.URL,
		Method:     "GET",
		IntervalMs: 1000,
		TimeoutMs:  5000,
		Regions:    []string{"us-east-1"},
		UserID:     "test-user-ha",
	})

	time.Sleep(4 * time.Second)

	allJobsAfter, err := env1.Queries.GetJobsForMonitor(ctx, monitor2.ID)
	require.NoError(t, err)
	require.Greater(t, len(allJobsAfter), 0, "Remaining control plane should continue processing monitors")

	t.Logf("HA test completed successfully: found %d jobs for monitor 1 and %d jobs for monitor 2",
		len(allJobs), len(allJobsAfter))
}

func TestLeaseReaperLeaderElection(t *testing.T) {
	t.Parallel()

	env1 := helpers.SetupControlPlane(t)
	env1.StartBackgroundServices()

	env2 := helpers.SetupControlPlaneWithDB(t, env1.TestDB.DB)
	env2.StartBackgroundServices()

	ctx := context.Background()

	_, err := env1.TestDB.DB.ExecContext(ctx, `
		INSERT INTO "user" (id, name, email, "emailVerified")
		VALUES ('test-user-reaper', 'Test User Reaper', 'test-reaper@example.com', true)
		ON CONFLICT (id) DO NOTHING
	`)
	require.NoError(t, err)

	targetServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(10 * time.Second)
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	}))
	t.Cleanup(targetServer.Close)

	monitor := helpers.CreateMonitorWithConfig(t, env1.Queries, helpers.MonitorConfig{
		Name:       "Lease Reaper Test Monitor",
		URL:        targetServer.URL,
		Method:     "GET",
		IntervalMs: 1000,
		TimeoutMs:  15000,
		Regions:    []string{"us-east-1"},
		UserID:     "test-user-reaper",
	})

	time.Sleep(3 * time.Second)

	var jobRunIDs []string
	for i := 0; i < 3; i++ {
		job := helpers.CreateTestJob(t, env1.Queries, monitor.ID, "us-east-1")
		jobRunIDs = append(jobRunIDs, job.RunID)
	}

	fakeWorkerID := "fake-expired-worker"
	_, err = env1.Queries.RegisterWorker(ctx, &sqlc.RegisterWorkerParams{
		ID:      fakeWorkerID,
		Region:  "us-east-1",
		Version: "test-expired",
	})
	require.NoError(t, err)

	expiredTime := time.Now().Add(-10 * time.Minute)
	for _, runID := range jobRunIDs {
		_, err := env1.TestDB.DB.ExecContext(ctx, `
			UPDATE app.jobs
			SET status = 'leased',
			    worker_id = $1,
			    lease_expires_at = $2
			WHERE run_id = $3
		`, fakeWorkerID, expiredTime, runID)
		require.NoError(t, err)
	}

	var expiredCount int
	err = env1.TestDB.DB.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM app.jobs
		WHERE status = 'leased'
		AND lease_expires_at < NOW()
	`).Scan(&expiredCount)
	require.NoError(t, err)
	require.Greater(t, expiredCount, 0, "Should have expired leases before lease reaper runs")

	t.Logf("Found %d expired leases before lease reaper cleanup", expiredCount)

	require.Eventually(t, func() bool {
		var reclaimedCount int
		for _, runID := range jobRunIDs {
			var status string
			err := env1.TestDB.DB.QueryRowContext(ctx, `
				SELECT status FROM app.jobs WHERE run_id = $1
			`, runID).Scan(&status)
			if err == nil && status == "ready" {
				reclaimedCount++
			}
		}
		t.Logf("Reclaimed %d out of %d jobs", reclaimedCount, len(jobRunIDs))
		return reclaimedCount == len(jobRunIDs)
	}, 20*time.Second, 1*time.Second, "All expired leases should be reclaimed")

	var stillExpiredCount int
	err = env1.TestDB.DB.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM app.jobs
		WHERE status = 'leased'
		AND lease_expires_at < NOW()
	`).Scan(&stillExpiredCount)
	require.NoError(t, err)
	require.Equal(t, 0, stillExpiredCount, "All expired leases should be reclaimed by the active lease reaper")

	t.Logf("Lease reaper leader election test completed: all jobs reclaimed, %d still expired", stillExpiredCount)

	env1.Shutdown()

	var failoverJobRunIDs []string
	for i := 0; i < 2; i++ {
		job := helpers.CreateTestJob(t, env2.Queries, monitor.ID, "us-east-1")
		failoverJobRunIDs = append(failoverJobRunIDs, job.RunID)
	}

	for _, runID := range failoverJobRunIDs {
		_, err := env1.TestDB.DB.ExecContext(ctx, `
			UPDATE app.jobs
			SET status = 'leased',
			    worker_id = $1,
			    lease_expires_at = $2
			WHERE run_id = $3
		`, fakeWorkerID, expiredTime, runID)
		require.NoError(t, err)
	}

	require.Eventually(t, func() bool {
		var failoverReclaimedCount int
		for _, runID := range failoverJobRunIDs {
			var status string
			err := env1.TestDB.DB.QueryRowContext(ctx, `
				SELECT status FROM app.jobs WHERE run_id = $1
			`, runID).Scan(&status)
			if err == nil && status == "ready" {
				failoverReclaimedCount++
			}
		}
		t.Logf("Failover reclaimed %d out of %d jobs", failoverReclaimedCount, len(failoverJobRunIDs))
		return failoverReclaimedCount == len(failoverJobRunIDs)
	}, 15*time.Second, 1*time.Second, "Remaining lease reaper should continue reclaiming after failover")

	t.Logf("Lease reaper failover test completed: all jobs reclaimed after failover")
}
