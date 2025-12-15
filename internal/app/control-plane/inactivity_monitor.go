package controlplane

import (
	"context"
	"log"
	"time"

	"github.com/crisog/openseer/internal/app/control-plane/store/sqlc"
)

type WorkerInactivityMonitor struct {
	queries  *sqlc.Queries
	interval time.Duration
}

func NewWorkerInactivityMonitor(queries *sqlc.Queries, interval time.Duration) *WorkerInactivityMonitor {
	if interval <= 0 {
		interval = 30 * time.Second
	}
	return &WorkerInactivityMonitor{
		queries:  queries,
		interval: interval,
	}
}

func (m *WorkerInactivityMonitor) Start(ctx context.Context) {
	ticker := time.NewTicker(m.interval)
	defer ticker.Stop()

	log.Printf("Worker inactivity monitor started (%v interval)", m.interval)

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			markCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
			if err := m.queries.MarkWorkerInactive(markCtx); err != nil {
				log.Printf("Failed to mark inactive workers: %v", err)
			}
			cancel()
		}
	}
}
