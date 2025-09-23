package worker

import (
	"context"
	"fmt"
	"log"
	"time"

	openseerv1 "github.com/crisog/openseer/gen/openseer/v1"
	"github.com/crisog/openseer/internal/pkg/recovery"
)

func (w *Worker) jobRequester(ctx context.Context) {
	ticker := time.NewTicker(500 * time.Millisecond)
	heartbeatTicker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	defer heartbeatTicker.Stop()

	time.Sleep(100 * time.Millisecond)
	w.requestJobsIfNeeded()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			w.requestJobsIfNeeded()
		case <-heartbeatTicker.C:
			w.logWorkerStatus()
		}
	}
}

func (w *Worker) requestJobsIfNeeded() {
	w.mu.RLock()
	activeJobCount := int32(len(w.activeJobs))
	connected := w.connected
	w.mu.RUnlock()

	available := w.maxConcurrency - activeJobCount
	if available <= 0 {
		return
	}

	if !connected {
		return
	}

	err := w.sendMessage(&openseerv1.WorkerMessage{
		Message: &openseerv1.WorkerMessage_JobRequest{
			JobRequest: &openseerv1.JobRequest{
				Count: available,
			},
		},
	})
	if err != nil {
		log.Printf("Failed to request jobs: %v", err)
		return
	}
}

func (w *Worker) logWorkerStatus() {
	w.mu.RLock()
	activeJobCount := len(w.activeJobs)
	connected := w.connected
	workerID := w.id
	w.mu.RUnlock()

	if connected {
		log.Printf("Worker %s: active jobs=%d/%d, polling for work", workerID, activeJobCount, w.maxConcurrency)
	} else {
		log.Printf("Worker %s: disconnected, attempting to reconnect", workerID)
	}
}

func (w *Worker) receiveLoop(ctx context.Context) {
	defer func() {
		w.mu.Lock()
		w.connected = false
		cancelFn := w.receiveCancel
		w.receiveCancel = nil

		activeCount := len(w.activeJobs)
		for runID, cancel := range w.activeJobs {
			cancel()
			delete(w.activeJobs, runID)
		}
		pendingCount := len(w.pendingResults)
		for runID := range w.pendingResults {
			delete(w.pendingResults, runID)
		}
		w.mu.Unlock()

		if cancelFn != nil {
			cancelFn()
		}

		log.Printf("Receive loop stopped. Canceled %d active jobs, cleared %d pending results.", activeCount, pendingCount)
	}()

	for {
		msgCh := make(chan *openseerv1.ServerMessage, 1)
		errCh := make(chan error, 1)

		go func() {
			msg, err := w.stream.Receive()
			if err != nil {
				errCh <- err
			} else {
				msgCh <- msg
			}
		}()

		select {
		case <-ctx.Done():
			return
		case err := <-errCh:
			log.Printf("Stream recv error: %v", err)
			return
		case msg := <-msgCh:
			w.processMessage(ctx, msg)
		}
	}
}

func (w *Worker) processMessage(ctx context.Context, msg *openseerv1.ServerMessage) {
	switch m := msg.Message.(type) {
	case *openseerv1.ServerMessage_Registered:
		w.mu.Lock()
		w.id = m.Registered.WorkerId
		workerID := w.id
		w.mu.Unlock()
		log.Printf("Registered with ID: %s", workerID)

	case *openseerv1.ServerMessage_Job:
		log.Printf("Received job: %s for monitor %s", m.Job.RunId, m.Job.MonitorId)
		go recovery.WithRecover(
			fmt.Sprintf("job-execution-%s", m.Job.RunId),
			func() { w.executeCheck(ctx, m.Job) },
		)()

	case *openseerv1.ServerMessage_Ack:
		if m.Ack.GetCommitted() {
			log.Printf("Result acknowledged: %s", m.Ack.RunId)
			w.completeJob(m.Ack.RunId)
			w.mu.Lock()
			delete(w.pendingResults, m.Ack.RunId)
			w.mu.Unlock()
		} else {
			log.Printf("Result not committed, will retry: %s", m.Ack.RunId)
			w.scheduleResultRetry(ctx, m.Ack.RunId)
		}

	case *openseerv1.ServerMessage_Ping:
		if err := w.sendMessage(&openseerv1.WorkerMessage{
			Message: &openseerv1.WorkerMessage_Pong{
				Pong: &openseerv1.Pong{
					Timestamp: m.Ping.Timestamp,
				},
			},
		}); err != nil {
			log.Printf("Failed to send pong: %v", err)
		}
	}
}

func (w *Worker) sendResult(result *openseerv1.MonitorResult) {
	w.mu.Lock()
	isConnected := w.connected
	w.pendingResults[result.RunId] = result
	w.mu.Unlock()

	if isConnected {
		if err := w.sendMessage(&openseerv1.WorkerMessage{
			Message: &openseerv1.WorkerMessage_Result{
				Result: result,
			},
		}); err != nil {
			log.Printf("Failed to send result: %v", err)
			return
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
	} else {
		log.Printf("Failed to send result %s: worker not connected", result.RunId)
	}
}

func (w *Worker) completeJob(runID string) {
	w.mu.Lock()
	defer w.mu.Unlock()

	if cancel, exists := w.activeJobs[runID]; exists {
		cancel()
		delete(w.activeJobs, runID)
		delete(w.pendingResults, runID)
		log.Printf("Completed job: %s", runID)
		go recovery.WithRecover("request-jobs-after-completion", w.requestJobsIfNeeded)()
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
			w.mu.RLock()
			isConnected := w.connected
			w.mu.RUnlock()

			if !isConnected {
				continue
			}

			if err := w.sendMessage(&openseerv1.WorkerMessage{
				Message: &openseerv1.WorkerMessage_LeaseRenewal{
					LeaseRenewal: &openseerv1.LeaseRenewal{RunId: runID},
				},
			}); err != nil {
				log.Printf("Failed to renew lease for job %s: %v", runID, err)
				return
			}
			log.Printf("Renewed lease for job %s", runID)
		}
	}
}

func (w *Worker) scheduleResultRetry(ctx context.Context, runID string) {
	go func() {
		select {
		case <-time.After(5 * time.Second):
		case <-ctx.Done():
			return
		}

		w.mu.RLock()
		result, exists := w.pendingResults[runID]
		isConnected := w.connected
		w.mu.RUnlock()

		if !exists {
			return
		}

		if isConnected {
			log.Printf("Retrying result submission for job %s", runID)
			if err := w.sendMessage(&openseerv1.WorkerMessage{
				Message: &openseerv1.WorkerMessage_Result{
					Result: result,
				},
			}); err != nil {
				log.Printf("Failed to retry result for job %s: %v", runID, err)
			} else {
				log.Printf("Successfully retried result submission for job %s", runID)
			}
		} else {
			log.Printf("Cannot retry result for job %s: worker not connected", runID)
		}
	}()
}
