package worker

import (
	"sync"
	"time"
)

type heartbeatTracker struct {
	mu       sync.Mutex
	interval time.Duration
	lastSeen map[string]time.Time
}

func newHeartbeatTracker(interval time.Duration) *heartbeatTracker {
	return &heartbeatTracker{
		interval: interval,
		lastSeen: make(map[string]time.Time),
	}
}

func (h *heartbeatTracker) ShouldUpdate(workerID string) bool {
	if workerID == "" {
		return false
	}
	if h == nil || h.interval <= 0 {
		return true
	}

	now := time.Now()

	h.mu.Lock()
	defer h.mu.Unlock()

	last, ok := h.lastSeen[workerID]
	if ok && now.Sub(last) < h.interval {
		return false
	}

	h.lastSeen[workerID] = now
	return true
}

func (h *heartbeatTracker) Invalidate(workerID string) {
	if h == nil || workerID == "" {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.lastSeen, workerID)
}
