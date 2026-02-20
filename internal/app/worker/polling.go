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
	heartbeatTicker := time.NewTicker(30 * time.Second)
	defer heartbeatTicker.Stop()

	currentInterval := w.pollBaseInterval
	pollTimer := time.NewTimer(100 * time.Millisecond)
	defer pollTimer.Stop()

	for {
		select {
		case <-ctx.Done():
			w.cancelAllJobs()
			return ctx.Err()
		case <-pollTimer.C:
			jobsReceived, err := w.pollForJobs(ctx)
			currentInterval = w.nextPollInterval(currentInterval, jobsReceived, err)
			pollTimer.Reset(currentInterval)
		case <-heartbeatTicker.C:
			w.logWorkerStatus()
		}
	}
}

func (w *Worker) nextPollInterval(current time.Duration, jobsReceived int, pollErr error) time.Duration {
	if current < w.pollBaseInterval {
		current = w.pollBaseInterval
	}

	if jobsReceived < 0 {
		return w.pollBaseInterval
	}

	if pollErr == nil && jobsReceived > 0 {
		return w.pollBaseInterval
	}

	next := current * 2
	if next < w.pollBaseInterval {
		next = w.pollBaseInterval
	}
	if next > w.pollMaxInterval {
		next = w.pollMaxInterval
	}
	return next
}

func (w *Worker) pollForJobs(ctx context.Context) (int, error) {
	w.mu.RLock()
	activeJobCount := int32(len(w.activeJobs))
	w.mu.RUnlock()

	available := w.maxConcurrency - activeJobCount
	if available <= 0 {
		return -1, nil
	}

	resp, err := w.getJobsWithAuthRecovery(ctx, available)
	if err != nil {
		log.Printf("Failed to get jobs: %v", err)
		return 0, err
	}

	for _, job := range resp.Msg.Jobs {
		log.Printf("Received job: %s for monitor %s", job.RunId, job.MonitorId)
		go recovery.WithRecover(
			fmt.Sprintf("job-execution-%s", job.RunId),
			func() { w.executeCheck(ctx, job) },
		)()
	}

	return len(resp.Msg.Jobs), nil
}

func (w *Worker) getJobsWithAuthRecovery(ctx context.Context, available int32) (*connect.Response[openseerv1.GetJobsResponse], error) {
	resp, err := w.getJobs(ctx, available)
	if err == nil {
		return resp, nil
	}

	if !isUnauthenticatedError(err) {
		return nil, err
	}

	refreshCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	refreshErr := w.refreshAPIToken(refreshCtx)
	cancel()
	if refreshErr != nil {
		return nil, fmt.Errorf("failed to refresh API token after unauthenticated response: %w", refreshErr)
	}

	return w.getJobs(ctx, available)
}

func (w *Worker) getJobs(ctx context.Context, available int32) (*connect.Response[openseerv1.GetJobsResponse], error) {
	req := connect.NewRequest(&openseerv1.GetJobsRequest{
		MaxJobs: available,
	})
	for k, v := range w.authHeader() {
		req.Header()[k] = v
	}
	return w.workerClient.GetJobs(ctx, req)
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

func (w *Worker) sendResult(ctx context.Context, result *openseerv1.MonitorResult) {
	committed := false

	for attempt := 1; attempt <= w.resultSubmitMaxAttempts; attempt++ {
		if !w.isJobActive(result.RunId) {
			return
		}

		submitCtx, cancel := context.WithTimeout(ctx, w.resultSubmitTimeout)
		resp, err := w.submitResultOnce(submitCtx, result)
		cancel()

		if err != nil {
			if isUnauthenticatedError(err) {
				refreshCtx, refreshCancel := context.WithTimeout(ctx, 10*time.Second)
				refreshErr := w.refreshAPIToken(refreshCtx)
				refreshCancel()
				if refreshErr != nil {
					log.Printf("Failed to refresh API token while submitting result for %s: %v", result.RunId, refreshErr)
				} else {
					attempt--
					continue
				}
			}

			if attempt == w.resultSubmitMaxAttempts {
				log.Printf("Failed to submit result for %s after %d attempts: %v", result.RunId, attempt, err)
				break
			}

			log.Printf("Failed to submit result for %s on attempt %d/%d: %v", result.RunId, attempt, w.resultSubmitMaxAttempts, err)
			if !waitWithContext(ctx, w.resultSubmitRetryInterval) {
				return
			}
			continue
		}

		if resp.Msg.Committed {
			committed = true
			log.Printf("Result committed for %s", result.RunId)
			break
		}

		if attempt == w.resultSubmitMaxAttempts {
			log.Printf("Result for %s was not committed after %d attempts", result.RunId, attempt)
			break
		}

		log.Printf("Result not committed for %s on attempt %d/%d, retrying", result.RunId, attempt, w.resultSubmitMaxAttempts)
		if !waitWithContext(ctx, w.resultSubmitRetryInterval) {
			return
		}
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

	if committed {
		w.completeJob(result.RunId)
		return
	}

	// Release local worker capacity and stop lease renewals. The control-plane lease reaper
	// will reclaim uncommitted jobs after lease expiry.
	w.completeJob(result.RunId)
	log.Printf("Released local slot for uncommitted result %s; job will be reclaimed after lease expiry", result.RunId)
}

func (w *Worker) submitResultOnce(ctx context.Context, result *openseerv1.MonitorResult) (*connect.Response[openseerv1.SubmitResultResponse], error) {
	req := connect.NewRequest(&openseerv1.SubmitResultRequest{
		Result: result,
	})
	for k, v := range w.authHeader() {
		req.Header()[k] = v
	}
	return w.workerClient.SubmitResult(ctx, req)
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

func (w *Worker) isJobActive(runID string) bool {
	w.mu.RLock()
	defer w.mu.RUnlock()
	_, exists := w.activeJobs[runID]
	return exists
}

func (w *Worker) renewLease(ctx context.Context, runID string) {
	ticker := time.NewTicker(w.leaseRenewalInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			renewCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			resp, err := w.renewLeaseOnce(renewCtx, runID)
			cancel()

			if err != nil {
				if isUnauthenticatedError(err) {
					refreshCtx, refreshCancel := context.WithTimeout(ctx, 10*time.Second)
					refreshErr := w.refreshAPIToken(refreshCtx)
					refreshCancel()
					if refreshErr == nil {
						renewCtx, retryCancel := context.WithTimeout(ctx, 5*time.Second)
						resp, err = w.renewLeaseOnce(renewCtx, runID)
						retryCancel()
					}
				}
			}

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

func (w *Worker) renewLeaseOnce(ctx context.Context, runID string) (*connect.Response[openseerv1.RenewLeaseResponse], error) {
	req := connect.NewRequest(&openseerv1.RenewLeaseRequest{
		RunId: runID,
	})
	for k, v := range w.authHeader() {
		req.Header()[k] = v
	}
	return w.workerClient.RenewLease(ctx, req)
}

func waitWithContext(ctx context.Context, d time.Duration) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

func isUnauthenticatedError(err error) bool {
	return connect.CodeOf(err) == connect.CodeUnauthenticated
}
