package worker

import (
	"context"
	"fmt"
	"log"
	"time"

	"connectrpc.com/connect"
	openseerv1 "github.com/crisog/openseer/gen/openseer/v1"
	"github.com/crisog/openseer/internal/pkg/recovery"
)

func (w *Worker) pollLoop(ctx context.Context) error {
	ticker := time.NewTicker(w.pollInterval)
	heartbeatTicker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	defer heartbeatTicker.Stop()

	time.Sleep(100 * time.Millisecond)
	w.pollForJobs(ctx)

	for {
		select {
		case <-ctx.Done():
			w.cancelAllJobs()
			return ctx.Err()
		case <-ticker.C:
			w.pollForJobs(ctx)
		case <-heartbeatTicker.C:
			w.logWorkerStatus()
		}
	}
}

func (w *Worker) pollForJobs(ctx context.Context) {
	w.mu.RLock()
	activeJobCount := int32(len(w.activeJobs))
	w.mu.RUnlock()

	available := w.maxConcurrency - activeJobCount
	if available <= 0 {
		return
	}

	req := connect.NewRequest(&openseerv1.GetJobsRequest{
		MaxJobs: available,
	})
	for k, v := range w.authHeader() {
		req.Header()[k] = v
	}

	resp, err := w.workerClient.GetJobs(ctx, req)
	if err != nil {
		log.Printf("Failed to get jobs: %v", err)
		return
	}

	for _, job := range resp.Msg.Jobs {
		log.Printf("Received job: %s for monitor %s", job.RunId, job.MonitorId)
		go recovery.WithRecover(
			fmt.Sprintf("job-execution-%s", job.RunId),
			func() { w.executeCheck(ctx, job) },
		)()
	}
}

func (w *Worker) logWorkerStatus() {
	w.mu.RLock()
	activeJobCount := len(w.activeJobs)
	workerID := w.id
	w.mu.RUnlock()

	log.Printf("Worker %s: active jobs=%d/%d, polling for work", workerID, activeJobCount, w.maxConcurrency)
}

func (w *Worker) cancelAllJobs() {
	w.mu.Lock()
	defer w.mu.Unlock()

	for runID, cancel := range w.activeJobs {
		cancel()
		delete(w.activeJobs, runID)
	}
	log.Printf("Cancelled all active jobs on shutdown")
}

func (w *Worker) sendResult(result *openseerv1.MonitorResult) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	req := connect.NewRequest(&openseerv1.SubmitResultRequest{
		Result: result,
	})
	for k, v := range w.authHeader() {
		req.Header()[k] = v
	}

	resp, err := w.workerClient.SubmitResult(ctx, req)
	if err != nil {
		log.Printf("Failed to submit result for %s: %v", result.RunId, err)
		return
	}

	if resp.Msg.Committed {
		log.Printf("Result committed for %s", result.RunId)
		w.completeJob(result.RunId)
	} else {
		log.Printf("Result not committed for %s, will retry", result.RunId)
		go w.retryResult(result)
	}

	httpCode := "nil"
	if result.HttpCode != nil {
		httpCode = fmt.Sprintf("%d", *result.HttpCode)
	}
	totalMs := "nil"
	if result.TotalMs != nil {
		totalMs = fmt.Sprintf("%d", *result.TotalMs)
	}
	log.Printf("Sent result for %s: status=%s, http_code=%s, total_ms=%s", result.RunId, result.Status, httpCode, totalMs)
}

func (w *Worker) completeJob(runID string) {
	w.mu.Lock()
	defer w.mu.Unlock()

	if cancel, exists := w.activeJobs[runID]; exists {
		cancel()
		delete(w.activeJobs, runID)
		log.Printf("Completed job: %s", runID)
	}
}

func (w *Worker) renewLease(ctx context.Context, runID string) {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			req := connect.NewRequest(&openseerv1.RenewLeaseRequest{
				RunId: runID,
			})
			for k, v := range w.authHeader() {
				req.Header()[k] = v
			}

			renewCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			resp, err := w.workerClient.RenewLease(renewCtx, req)
			cancel()

			if err != nil {
				log.Printf("Failed to renew lease for job %s: %v", runID, err)
				return
			}

			if resp.Msg.Renewed {
				log.Printf("Renewed lease for job %s", runID)
			} else {
				log.Printf("Lease renewal failed for job %s", runID)
				return
			}
		}
	}
}

func (w *Worker) retryResult(result *openseerv1.MonitorResult) {
	time.Sleep(5 * time.Second)

	w.mu.RLock()
	_, exists := w.activeJobs[result.RunId]
	w.mu.RUnlock()

	if !exists {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	req := connect.NewRequest(&openseerv1.SubmitResultRequest{
		Result: result,
	})
	for k, v := range w.authHeader() {
		req.Header()[k] = v
	}

	resp, err := w.workerClient.SubmitResult(ctx, req)
	if err != nil {
		log.Printf("Retry failed for %s: %v", result.RunId, err)
		return
	}

	if resp.Msg.Committed {
		log.Printf("Retry successful for %s", result.RunId)
		w.completeJob(result.RunId)
	}
}
