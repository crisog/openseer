package controlplane

import (
	"context"
	"database/sql"
	"log"
	"time"

	"github.com/crisog/openseer/internal/app/control-plane/store/sqlc"
)

type LeaseReaper struct {
	queries  *sqlc.Queries
	db       *sql.DB
	interval time.Duration
}

func NewLeaseReaper(queries *sqlc.Queries, db *sql.DB, interval time.Duration) *LeaseReaper {
	if interval <= 0 {
		interval = 5 * time.Second
	}
	return &LeaseReaper{
		queries:  queries,
		db:       db,
		interval: interval,
	}
}

func (r *LeaseReaper) Start(ctx context.Context) {
	ticker := time.NewTicker(r.interval)
	defer ticker.Stop()

	log.Printf("Lease reaper started (%v interval)", r.interval)

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			const advisoryLockID = 54321

			lockCtx, cancel := context.WithTimeout(ctx, 5*time.Second)

			var gotLock bool
			err := r.db.QueryRowContext(lockCtx, "SELECT pg_try_advisory_lock($1)", advisoryLockID).Scan(&gotLock)
			cancel()

			if err != nil {
				log.Printf("Failed to acquire advisory lock for lease reaper: %v", err)
				continue
			}

			if !gotLock {
				continue
			}

			workCtx, workCancel := context.WithTimeout(ctx, 20*time.Second)
			if err := r.queries.ReclaimExpiredLeases(workCtx); err != nil {
				log.Printf("Error reclaiming expired leases: %v", err)
			}
			workCancel()

			unlockCtx, unlockCancel := context.WithTimeout(context.Background(), 10*time.Second)
			_, err = r.db.ExecContext(unlockCtx, "SELECT pg_advisory_unlock($1)", advisoryLockID)
			unlockCancel()
			if err != nil {
				log.Printf("CRITICAL: Failed to release advisory lock %d for lease reaper: %v - manual intervention may be required", advisoryLockID, err)
			}
		}
	}
}

