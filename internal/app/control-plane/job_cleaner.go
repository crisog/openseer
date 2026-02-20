package controlplane

import (
	"context"
	"database/sql"
	"log"
	"time"

	"github.com/crisog/openseer/internal/app/control-plane/store/sqlc"
)

type JobCleaner struct {
	queries         *sqlc.Queries
	db              *sql.DB
	interval        time.Duration
	retentionPeriod time.Duration
	batchSize       int32
}

func NewJobCleaner(
	queries *sqlc.Queries,
	db *sql.DB,
	interval time.Duration,
	retentionPeriod time.Duration,
	batchSize int32,
) *JobCleaner {
	if interval <= 0 {
		interval = 1 * time.Minute
	}
	if retentionPeriod <= 0 {
		retentionPeriod = 7 * 24 * time.Hour
	}
	if batchSize <= 0 {
		batchSize = 1000
	}

	return &JobCleaner{
		queries:         queries,
		db:              db,
		interval:        interval,
		retentionPeriod: retentionPeriod,
		batchSize:       batchSize,
	}
}

func (c *JobCleaner) Start(ctx context.Context) {
	ticker := time.NewTicker(c.interval)
	defer ticker.Stop()

	log.Printf(
		"Job cleaner started (interval=%v retention=%v batch_size=%d)",
		c.interval,
		c.retentionPeriod,
		c.batchSize,
	)

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			c.runCleanup(ctx)
		}
	}
}

func (c *JobCleaner) runCleanup(ctx context.Context) {
	const advisoryLockID = 98765

	lockCtx, lockCancel := context.WithTimeout(ctx, 5*time.Second)
	defer lockCancel()

	var gotLock bool
	if err := c.db.QueryRowContext(lockCtx, "SELECT pg_try_advisory_lock($1)", advisoryLockID).Scan(&gotLock); err != nil {
		log.Printf("Failed to acquire advisory lock for job cleaner: %v", err)
		return
	}
	if !gotLock {
		return
	}

	defer func() {
		unlockCtx, unlockCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer unlockCancel()

		if _, err := c.db.ExecContext(unlockCtx, "SELECT pg_advisory_unlock($1)", advisoryLockID); err != nil {
			log.Printf("CRITICAL: Failed to release advisory lock %d for job cleaner: %v", advisoryLockID, err)
		}
	}()

	cutoff := time.Now().Add(-c.retentionPeriod)
	totalDeleted := int64(0)

	for {
		workCtx, workCancel := context.WithTimeout(ctx, 20*time.Second)
		deleted, err := c.queries.DeleteDoneJobsBefore(workCtx, &sqlc.DeleteDoneJobsBeforeParams{
			ScheduledAt: cutoff,
			Limit:       c.batchSize,
		})
		workCancel()
		if err != nil {
			log.Printf("Failed cleaning old done jobs: %v", err)
			return
		}

		totalDeleted += deleted
		if deleted < int64(c.batchSize) {
			break
		}
	}

	if totalDeleted > 0 {
		log.Printf("Job cleaner removed %d completed jobs older than %s", totalDeleted, cutoff.Format(time.RFC3339))
	}
}
