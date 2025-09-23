package controlplane

import (
	"context"
	"crypto/sha1"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"hash/fnv"
	"log"
	"time"

	"github.com/crisog/openseer/internal/app/control-plane/store/sqlc"
)

type Scheduler struct {
	queries      *sqlc.Queries
	db           *sql.DB
	pollInterval time.Duration
}

func NewScheduler(queries *sqlc.Queries, db *sql.DB, pollInterval time.Duration) *Scheduler {
	if pollInterval > 1*time.Second {
		log.Printf("WARNING: Scheduler poll interval of %v may cause monitoring delays. Consider using 1s or less for critical monitoring.", pollInterval)
	}
	return &Scheduler{
		queries:      queries,
		db:           db,
		pollInterval: pollInterval,
	}
}

func (s *Scheduler) Start(ctx context.Context) {
	log.Println("Scheduler starting...")
	ticker := time.NewTicker(s.pollInterval)
	defer ticker.Stop()

	s.scheduleIteration(ctx)

	for {
		select {
		case <-ctx.Done():
			log.Println("Scheduler shutting down...")
			return
		case <-ticker.C:
			s.scheduleIteration(ctx)
		}
	}
}

func (s *Scheduler) scheduleIteration(ctx context.Context) {
	const advisoryLockID = 12345

	lockCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	var gotLock bool
	err := s.db.QueryRowContext(lockCtx, "SELECT pg_try_advisory_lock($1)", advisoryLockID).Scan(&gotLock)
	if err != nil {
		log.Printf("Failed to acquire advisory lock: %v", err)
		return
	}

	if !gotLock {
		return
	}

	lockAcquired := time.Now()
	defer func() {
		lockDuration := time.Since(lockAcquired)
		if lockDuration > 30*time.Second {
			log.Printf("WARNING: Held advisory lock for %v - consider optimizing scheduling logic", lockDuration)
		}
	}()

	defer func() {
		unlockCtx, unlockCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer unlockCancel()

		_, err := s.db.ExecContext(unlockCtx, "SELECT pg_advisory_unlock($1)", advisoryLockID)
		if err != nil {
			log.Printf("CRITICAL: Failed to release advisory lock %d: %v - manual intervention may be required", advisoryLockID, err)
		}
	}()

	now := time.Now()
	lookAhead := now.Add(5 * time.Second)

	workCtx, workCancel := context.WithTimeout(ctx, 30*time.Second)
	defer workCancel()

	tx, err := s.db.BeginTx(workCtx, &sql.TxOptions{
		Isolation: sql.LevelRepeatableRead,
	})
	if err != nil {
		log.Printf("Failed to begin scheduling transaction: %v", err)
		return
	}
	defer func() {
		if err := tx.Rollback(); err != nil && err != sql.ErrTxDone {
			log.Printf("Failed to rollback scheduling transaction: %v", err)
		}
	}()

	qtx := s.queries.WithTx(tx)

	monitors, err := qtx.ListDueMonitors(workCtx, sql.NullTime{Time: lookAhead, Valid: true})
	if err != nil {
		log.Printf("Failed to list due checks: %v", err)
		return
	}

	if len(monitors) == 0 {
		if err := tx.Commit(); err != nil {
			log.Printf("Failed to commit empty scheduling transaction: %v", err)
		}
		return
	}

	log.Printf("Scheduler found %d monitors due for scheduling", len(monitors))

	scheduledCount := 0

	for _, monitor := range monitors {
		select {
		case <-workCtx.Done():
			log.Printf("WARNING: Scheduling work timeout reached, releasing lock early to prevent system-wide deadlock")
			return
		default:
		}

		if err := s.scheduleMonitorTx(workCtx, qtx, *monitor, now); err != nil {
			log.Printf("Failed to schedule monitor %s: %v", monitor.ID, err)
			continue
		}
		scheduledCount++
	}

	if err := tx.Commit(); err != nil {
		log.Printf("CRITICAL: Failed to commit scheduling transaction: %v", err)
		if scheduledCount > 0 {
			log.Printf("CRITICAL: %d successfully scheduled monitors were lost due to transaction commit failure", scheduledCount)
		}
		return
	}

	log.Printf("Successfully scheduled %d monitors", scheduledCount)
}

func (s *Scheduler) scheduleMonitorTx(ctx context.Context, qtx *sqlc.Queries, monitor sqlc.AppMonitor, now time.Time) error {
	var scheduledAt time.Time
	if monitor.NextDueAt.Valid && monitor.NextDueAt.Time.After(now) {
		scheduledAt = monitor.NextDueAt.Time
	} else if monitor.NextDueAt.Valid {
		scheduledAt = now
		log.Printf("Monitor %s is overdue (was due at %s), scheduling immediately",
			monitor.ID, monitor.NextDueAt.Time.Format(time.RFC3339))
	} else {
		scheduledAt = now
		log.Printf("Monitor %s first run, scheduling immediately", monitor.ID)
	}

	baseInterval := time.Duration(monitor.IntervalMs) * time.Millisecond
	jitteredInterval := s.applyJitter(baseInterval, monitor.ID, monitor.JitterSeed)
	nextDueAt := scheduledAt.Add(jitteredInterval)

	for _, region := range monitor.Regions {
		runID := generateRunID(monitor.ID, region, scheduledAt)

		windowStart := scheduledAt.Add(-1 * time.Minute)
		windowEnd := scheduledAt.Add(1 * time.Minute)

		job, err := qtx.CreateJobIdempotent(ctx, &sqlc.CreateJobIdempotentParams{
			RunID:         runID,
			MonitorID:     monitor.ID,
			Region:        region,
			ScheduledAt:   scheduledAt,
			ScheduledAt_2: windowStart,
			ScheduledAt_3: windowEnd,
		})
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				log.Printf("Job already exists for monitor %s in region %s within time window", monitor.ID, region)
				continue
			}
			return fmt.Errorf("create job for region %s: %w", region, err)
		}

		if job == nil {
			log.Printf("Job already exists for monitor %s in region %s within time window", monitor.ID, region)
			continue
		}

		log.Printf("Created job %s for monitor %s in region %s, scheduled for %s",
			runID, monitor.ID, region, scheduledAt.Format(time.RFC3339))
	}

	_, err := qtx.UpdateMonitorSchedulingTime(ctx, &sqlc.UpdateMonitorSchedulingTimeParams{
		ID:              monitor.ID,
		LastScheduledAt: sql.NullTime{Time: scheduledAt, Valid: true},
		NextDueAt:       sql.NullTime{Time: nextDueAt, Valid: true},
	})
	if err != nil {
		return fmt.Errorf("update check scheduling time: %w", err)
	}

	log.Printf("Successfully scheduled monitor %s, next due at %s", monitor.ID, nextDueAt.Format(time.RFC3339))
	return nil
}

func (s *Scheduler) applyJitter(interval time.Duration, monitorID string, seed int32) time.Duration {
	if interval <= 10*time.Second {
		return interval
	}

	var maxJitterPercent int64 = 10
	if interval <= 30*time.Second {
		maxJitterPercent = 1
	}

	h := fnv.New32a()
	h.Write([]byte(monitorID))
	fmt.Fprintf(h, "%d", seed)
	hash := h.Sum32()

	maxJitterNanos := (interval.Nanoseconds() * maxJitterPercent) / 100
	if maxJitterNanos == 0 {
		return interval
	}

	jitterNanos := int64(hash) % maxJitterNanos
	jitter := time.Duration(jitterNanos)

	if hash%2 == 0 {
		return interval + jitter
	}
	return interval - jitter
}

func generateRunID(monitorID, region string, scheduledAt time.Time) string {
	timestamp := scheduledAt.Format("20060102150405")
	h := sha1.New()
	h.Write([]byte(fmt.Sprintf("%s-%s-%s", monitorID, region, timestamp)))
	hash := hex.EncodeToString(h.Sum(nil))
	return fmt.Sprintf("%s-%s-%s-%s", monitorID, region, timestamp, hash[:8])
}
