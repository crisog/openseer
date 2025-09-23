package controlplane

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"sync"
	"time"

	openseerv1 "github.com/crisog/openseer/gen/openseer/v1"
	"github.com/crisog/openseer/internal/app/control-plane/store/sqlc"
	"github.com/sqlc-dev/pqtype"
)

type Dispatcher struct {
	queries                  *sqlc.Queries
	db                       *sql.DB
	mu                       sync.RWMutex
	workers                  map[string]*WorkerInfo
	leaseTimeout             time.Duration
	leaseReaperInterval      time.Duration
	streamHealthInterval     time.Duration
	workerInactivityInterval time.Duration
}

type WorkerStream interface {
	Send(*openseerv1.ServerMessage) error
}

type WorkerInfo struct {
	ID           string
	Region       string
	Stream       WorkerStream
	LastSeen     time.Time
	LastPingSent time.Time
	PendingPings int
}

func New(queries *sqlc.Queries, db *sql.DB, leaseTimeout, leaseReaperInterval, streamHealthInterval, workerInactivityInterval time.Duration) *Dispatcher {
	if leaseReaperInterval <= 0 {
		leaseReaperInterval = 5 * time.Second
	}
	if streamHealthInterval <= 0 {
		streamHealthInterval = 15 * time.Second
	}
	if workerInactivityInterval <= 0 {
		workerInactivityInterval = 30 * time.Second
	}
	return &Dispatcher{
		queries:                  queries,
		db:                       db,
		workers:                  make(map[string]*WorkerInfo),
		leaseTimeout:             leaseTimeout,
		leaseReaperInterval:      leaseReaperInterval,
		streamHealthInterval:     streamHealthInterval,
		workerInactivityInterval: workerInactivityInterval,
	}
}

func (d *Dispatcher) RegisterWorker(workerID, region string, stream WorkerStream) {
	d.mu.Lock()
	defer d.mu.Unlock()

	d.workers[workerID] = &WorkerInfo{
		ID:           workerID,
		Region:       region,
		Stream:       stream,
		LastSeen:     time.Now(),
		LastPingSent: time.Time{},
		PendingPings: 0,
	}

	log.Printf("Worker %s registered from region %s", workerID, region)
}

func (d *Dispatcher) UnregisterWorker(workerID string) {
	d.mu.Lock()
	defer d.mu.Unlock()

	delete(d.workers, workerID)
	log.Printf("Worker %s unregistered", workerID)
}

func (d *Dispatcher) UpdateWorkerLastSeen(workerID string) {
	d.mu.Lock()
	defer d.mu.Unlock()

	if worker, exists := d.workers[workerID]; exists {
		worker.LastSeen = time.Now()
	}
}

func (d *Dispatcher) LeaseJobs(ctx context.Context, workerID string, maxJobs int32) ([]*openseerv1.MonitorJob, error) {
	var workerRegion string

	d.mu.Lock()
	worker, exists := d.workers[workerID]
	if !exists {
		d.mu.Unlock()
		return nil, fmt.Errorf("worker %s not found", workerID)
	}

	if time.Since(worker.LastSeen) > 2*time.Minute {
		log.Printf("Worker %s hasn't been seen for %v, appears to be disconnected", workerID, time.Since(worker.LastSeen))
		d.mu.Unlock()
		return nil, fmt.Errorf("worker %s appears to be disconnected", workerID)
	}

	worker.LastSeen = time.Now()
	workerRegion = worker.Region
	d.mu.Unlock()

	jobsWithMonitors, err := d.queries.LeaseJobsWithMonitorData(ctx, &sqlc.LeaseJobsWithMonitorDataParams{
		WorkerID: sql.NullString{String: workerID, Valid: true},
		Limit:    maxJobs,
		Region:   workerRegion,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to lease jobs: %w", err)
	}

	apiJobs := make([]*openseerv1.MonitorJob, 0, len(jobsWithMonitors))
	for _, jobData := range jobsWithMonitors {
		apiJobs = append(apiJobs, &openseerv1.MonitorJob{
			RunId:     jobData.RunID,
			MonitorId: jobData.MonitorID,
			Url:       jobData.Url,
			TimeoutMs: jobData.TimeoutMs,
			Method:    jobData.Method,
			Headers:   convertHeaders(jobData.Headers),
		})
	}

	if len(apiJobs) > 0 {
		log.Printf("Leased %d jobs to worker %s", len(apiJobs), workerID)
	}

	return apiJobs, nil
}

func (d *Dispatcher) CompleteJob(ctx context.Context, workerID, runID string) error {
	job, err := d.queries.CompleteJob(ctx, &sqlc.CompleteJobParams{
		RunID:    runID,
		WorkerID: sql.NullString{String: workerID, Valid: true},
	})
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			existing, getErr := d.queries.GetJobByRunID(ctx, runID)
			if getErr == nil {
				if existing.Status == "done" {
					log.Printf("Job %s already marked done (worker: %s)", runID, workerID)
					return nil
				}
			}
			return fmt.Errorf("failed to complete job: %w", err)
		}
		return fmt.Errorf("failed to complete job: %w", err)
	}

	log.Printf("Job %s completed by worker %s", job.RunID, workerID)
	return nil
}

func (d *Dispatcher) HandleStreamFailure(ctx context.Context, workerID string) {
	d.mu.Lock()
	defer d.mu.Unlock()

	if _, exists := d.workers[workerID]; exists {
		log.Printf("CRITICAL: Stream failure for worker %s, removing from active workers", workerID)
		delete(d.workers, workerID)

		log.Printf("Jobs leased to worker %s will be reclaimed by lease reaper", workerID)
	}
}

func (d *Dispatcher) SendJobToWorker(workerID string, job *openseerv1.MonitorJob) error {
	d.mu.RLock()
	worker, exists := d.workers[workerID]
	if !exists {
		d.mu.RUnlock()
		return fmt.Errorf("worker %s not found", workerID)
	}

	stream := worker.Stream
	d.mu.RUnlock()

	if stream == nil {
		return fmt.Errorf("worker %s has nil stream", workerID)
	}

	err := stream.Send(&openseerv1.ServerMessage{
		Message: &openseerv1.ServerMessage_Job{
			Job: job,
		},
	})

	if err != nil {
		log.Printf("Stream send failed for worker %s: %v", workerID, err)
		go d.HandleStreamFailure(context.Background(), workerID)
		return fmt.Errorf("stream failed for worker %s: %w", workerID, err)
	}

	d.mu.Lock()
	if worker, exists := d.workers[workerID]; exists {
		worker.LastSeen = time.Now()
	}
	d.mu.Unlock()
	return nil
}

func (d *Dispatcher) StartLeaseReaper(ctx context.Context) {
	ticker := time.NewTicker(d.leaseReaperInterval)
	defer ticker.Stop()

	log.Printf("Lease reaper started (%v interval)", d.leaseReaperInterval)

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			// Use advisory lock for leader election among multiple control plane instances
			// Lock ID 54321 is arbitrary but must be consistent across all reaper instances
			const advisoryLockID = 54321

			lockCtx, cancel := context.WithTimeout(ctx, 5*time.Second)

			var gotLock bool
			err := d.db.QueryRowContext(lockCtx, "SELECT pg_try_advisory_lock($1)", advisoryLockID).Scan(&gotLock)
			cancel()

			if err != nil {
				log.Printf("Failed to acquire advisory lock for lease reaper: %v", err)
				continue
			}

			if !gotLock {
				continue
			}

			workCtx, workCancel := context.WithTimeout(ctx, 20*time.Second)
			if err := d.reclaimExpiredLeases(workCtx); err != nil {
				log.Printf("Error reclaiming expired leases: %v", err)
			}
			workCancel()

			unlockCtx, unlockCancel := context.WithTimeout(context.Background(), 10*time.Second)
			_, err = d.db.ExecContext(unlockCtx, "SELECT pg_advisory_unlock($1)", advisoryLockID)
			unlockCancel()
			if err != nil {
				log.Printf("CRITICAL: Failed to release advisory lock %d for lease reaper: %v - manual intervention may be required", advisoryLockID, err)
			}
		}
	}
}

func (d *Dispatcher) reclaimExpiredLeases(ctx context.Context) error {
	err := d.queries.ReclaimExpiredLeases(ctx)
	if err != nil {
		return fmt.Errorf("failed to reclaim expired leases: %w", err)
	}
	return nil
}

func (d *Dispatcher) HandlePong(workerID string) {
	d.mu.Lock()
	defer d.mu.Unlock()

	if worker, exists := d.workers[workerID]; exists {
		worker.LastSeen = time.Now()
		worker.PendingPings = 0
	}
}

func (d *Dispatcher) StartStreamHealthMonitor(ctx context.Context) {
	ticker := time.NewTicker(d.streamHealthInterval)
	defer ticker.Stop()

	log.Printf("Stream health monitor started (%v interval)", d.streamHealthInterval)

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			d.checkStreamHealth()
		}
	}
}

func (d *Dispatcher) StartWorkerInactivityMonitor(ctx context.Context) {
	ticker := time.NewTicker(d.workerInactivityInterval)
	defer ticker.Stop()

	log.Printf("Worker inactivity monitor started (%v interval)", d.workerInactivityInterval)

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			markCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
			if err := d.queries.MarkWorkerInactive(markCtx); err != nil {
				log.Printf("Failed to mark inactive workers: %v", err)
			}
			cancel()
		}
	}
}

func (d *Dispatcher) checkStreamHealth() {
	now := time.Now()

	pingTargets := make(map[string]WorkerStream)

	d.mu.Lock()
	for workerID, worker := range d.workers {
		if now.Sub(worker.LastPingSent) > 30*time.Second {
			pingTargets[workerID] = worker.Stream
		}
	}
	d.mu.Unlock()

	deadWorkers := make(map[string]struct{})
	successfulPings := make([]string, 0)
	failedPings := make([]string, 0)

	for workerID, stream := range pingTargets {
		err := stream.Send(&openseerv1.ServerMessage{
			Message: &openseerv1.ServerMessage_Ping{
				Ping: &openseerv1.Ping{
					Timestamp: now.Unix(),
				},
			},
		})

		if err != nil {
			log.Printf("Failed to send ping to worker %s: %v", workerID, err)
			failedPings = append(failedPings, workerID)
		} else {
			successfulPings = append(successfulPings, workerID)
		}
	}

	d.mu.Lock()
	defer d.mu.Unlock()

	for _, workerID := range successfulPings {
		if worker, exists := d.workers[workerID]; exists && worker.Stream == pingTargets[workerID] {
			worker.LastPingSent = now
			worker.PendingPings++
		}
	}

	for _, workerID := range failedPings {
		if worker, exists := d.workers[workerID]; exists && worker.Stream == pingTargets[workerID] {
			deadWorkers[workerID] = struct{}{}
		}
	}

	for workerID, worker := range d.workers {
		timeSinceLastSeen := now.Sub(worker.LastSeen)
		if worker.PendingPings >= 3 || timeSinceLastSeen > 90*time.Second {
			log.Printf("Worker %s appears dead (pending pings: %d, last seen: %v ago)",
				workerID, worker.PendingPings, timeSinceLastSeen)
			deadWorkers[workerID] = struct{}{}
		}
	}

	for workerID := range deadWorkers {
		if _, exists := d.workers[workerID]; exists {
			delete(d.workers, workerID)
			log.Printf("Removed dead worker %s from active workers", workerID)
		}
	}
}

func (d *Dispatcher) GetWorkerCount() int {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return len(d.workers)
}

func convertHeaders(headersJSON pqtype.NullRawMessage) map[string]string {
	if !headersJSON.Valid || len(headersJSON.RawMessage) == 0 {
		return make(map[string]string)
	}

	var headers map[string]string
	if err := json.Unmarshal(headersJSON.RawMessage, &headers); err != nil {
		log.Printf("Error parsing headers JSON: %v", err)
		return make(map[string]string)
	}

	return headers
}
