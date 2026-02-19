package worker

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"connectrpc.com/connect"
	openseerv1 "github.com/crisog/openseer/gen/openseer/v1"
)

type mockWorkerClient struct {
	getJobsFn      func(context.Context, *connect.Request[openseerv1.GetJobsRequest]) (*connect.Response[openseerv1.GetJobsResponse], error)
	submitResultFn func(context.Context, *connect.Request[openseerv1.SubmitResultRequest]) (*connect.Response[openseerv1.SubmitResultResponse], error)
	renewLeaseFn   func(context.Context, *connect.Request[openseerv1.RenewLeaseRequest]) (*connect.Response[openseerv1.RenewLeaseResponse], error)
}

func (m *mockWorkerClient) GetJobs(ctx context.Context, req *connect.Request[openseerv1.GetJobsRequest]) (*connect.Response[openseerv1.GetJobsResponse], error) {
	if m.getJobsFn != nil {
		return m.getJobsFn(ctx, req)
	}
	return nil, errors.New("GetJobs not implemented")
}

func (m *mockWorkerClient) SubmitResult(ctx context.Context, req *connect.Request[openseerv1.SubmitResultRequest]) (*connect.Response[openseerv1.SubmitResultResponse], error) {
	if m.submitResultFn != nil {
		return m.submitResultFn(ctx, req)
	}
	return nil, errors.New("SubmitResult not implemented")
}

func (m *mockWorkerClient) RenewLease(ctx context.Context, req *connect.Request[openseerv1.RenewLeaseRequest]) (*connect.Response[openseerv1.RenewLeaseResponse], error) {
	if m.renewLeaseFn != nil {
		return m.renewLeaseFn(ctx, req)
	}
	return nil, errors.New("RenewLease not implemented")
}

func TestSendResultReleasesSlotAfterRetryBudgetExhausted(t *testing.T) {
	t.Parallel()

	var submitCalls int
	client := &mockWorkerClient{
		submitResultFn: func(context.Context, *connect.Request[openseerv1.SubmitResultRequest]) (*connect.Response[openseerv1.SubmitResultResponse], error) {
			submitCalls++
			return nil, connect.NewError(connect.CodeUnavailable, errors.New("temporary failure"))
		},
	}

	w := NewWorker("worker-1", "us-east-1", "1.0.0", "http://localhost:8080", "cluster-token", 1, http.DefaultClient).
		WithResultRetryConfig(3, 5*time.Millisecond, 50*time.Millisecond)
	w.workerClient = client

	runID := "run-retry-exhausted"
	jobCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	w.activeJobs[runID] = cancel

	w.sendResult(jobCtx, &openseerv1.MonitorResult{
		RunId:  runID,
		Status: "ERROR",
	})

	if submitCalls != 3 {
		t.Fatalf("expected 3 submit attempts, got %d", submitCalls)
	}
	if w.isJobActive(runID) {
		t.Fatalf("expected job %s to be removed from active jobs after retry exhaustion", runID)
	}
}

func TestSendResultCommitsAndReleasesSlot(t *testing.T) {
	t.Parallel()

	var submitCalls int
	client := &mockWorkerClient{
		submitResultFn: func(context.Context, *connect.Request[openseerv1.SubmitResultRequest]) (*connect.Response[openseerv1.SubmitResultResponse], error) {
			submitCalls++
			return connect.NewResponse(&openseerv1.SubmitResultResponse{
				Committed: true,
			}), nil
		},
	}

	w := NewWorker("worker-1", "us-east-1", "1.0.0", "http://localhost:8080", "cluster-token", 1, http.DefaultClient)
	w.workerClient = client

	runID := "run-committed"
	jobCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	w.activeJobs[runID] = cancel

	w.sendResult(jobCtx, &openseerv1.MonitorResult{
		RunId:  runID,
		Status: "OK",
	})

	if submitCalls != 1 {
		t.Fatalf("expected exactly 1 submit attempt, got %d", submitCalls)
	}
	if w.isJobActive(runID) {
		t.Fatalf("expected job %s to be removed from active jobs after successful commit", runID)
	}
}

func TestNextPollIntervalAdaptiveBackoff(t *testing.T) {
	t.Parallel()

	w := NewWorker("worker-1", "us-east-1", "1.0.0", "http://localhost:8080", "cluster-token", 1, http.DefaultClient).
		WithPollingConfig(100*time.Millisecond, 800*time.Millisecond)

	if got := w.nextPollInterval(0, 1, nil); got != 100*time.Millisecond {
		t.Fatalf("expected base interval after receiving jobs, got %v", got)
	}

	if got := w.nextPollInterval(100*time.Millisecond, 0, nil); got != 200*time.Millisecond {
		t.Fatalf("expected interval to back off to 200ms, got %v", got)
	}

	if got := w.nextPollInterval(400*time.Millisecond, 0, errors.New("poll error")); got != 800*time.Millisecond {
		t.Fatalf("expected interval to back off and cap at 800ms, got %v", got)
	}

	if got := w.nextPollInterval(800*time.Millisecond, 0, nil); got != 800*time.Millisecond {
		t.Fatalf("expected interval to remain capped at max, got %v", got)
	}

	if got := w.nextPollInterval(800*time.Millisecond, -1, nil); got != 100*time.Millisecond {
		t.Fatalf("expected no-capacity poll to reset to base interval, got %v", got)
	}
}
