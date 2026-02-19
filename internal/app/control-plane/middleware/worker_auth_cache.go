package middleware

import (
	"sync"
	"time"
)

type workerAuthCacheEntry struct {
	workerID  string
	expiresAt time.Time
}

// WorkerAuthCache stores worker auth lookups by token hash for a short TTL to
// reduce read pressure on the workers table.
type WorkerAuthCache struct {
	mu sync.Mutex

	ttl        time.Duration
	maxEntries int

	entries        map[string]workerAuthCacheEntry
	tokensByWorker map[string]map[string]struct{}
}

func NewWorkerAuthCache(ttl time.Duration, maxEntries int) *WorkerAuthCache {
	if maxEntries <= 0 {
		maxEntries = 1
	}
	return &WorkerAuthCache{
		ttl:            ttl,
		maxEntries:     maxEntries,
		entries:        make(map[string]workerAuthCacheEntry),
		tokensByWorker: make(map[string]map[string]struct{}),
	}
}

func (c *WorkerAuthCache) enabled() bool {
	return c != nil && c.ttl > 0 && c.maxEntries > 0
}

func (c *WorkerAuthCache) Get(tokenHash string) (string, bool) {
	if !c.enabled() || tokenHash == "" {
		return "", false
	}

	now := time.Now()

	c.mu.Lock()
	defer c.mu.Unlock()

	entry, ok := c.entries[tokenHash]
	if !ok {
		return "", false
	}
	if now.After(entry.expiresAt) {
		c.removeTokenLocked(tokenHash)
		return "", false
	}

	return entry.workerID, true
}

func (c *WorkerAuthCache) Set(tokenHash, workerID string) {
	if !c.enabled() || tokenHash == "" || workerID == "" {
		return
	}

	now := time.Now()
	expiresAt := now.Add(c.ttl)

	c.mu.Lock()
	defer c.mu.Unlock()

	// Remove old worker index entry if token hash is reassigned.
	if existing, ok := c.entries[tokenHash]; ok && existing.workerID != workerID {
		if hashes, exists := c.tokensByWorker[existing.workerID]; exists {
			delete(hashes, tokenHash)
			if len(hashes) == 0 {
				delete(c.tokensByWorker, existing.workerID)
			}
		}
	}

	c.entries[tokenHash] = workerAuthCacheEntry{
		workerID:  workerID,
		expiresAt: expiresAt,
	}

	hashes, ok := c.tokensByWorker[workerID]
	if !ok {
		hashes = make(map[string]struct{})
		c.tokensByWorker[workerID] = hashes
	}
	hashes[tokenHash] = struct{}{}

	c.evictLocked(now)
}

func (c *WorkerAuthCache) InvalidateWorker(workerID string) {
	if !c.enabled() || workerID == "" {
		return
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	hashes, ok := c.tokensByWorker[workerID]
	if !ok {
		return
	}

	for tokenHash := range hashes {
		c.removeTokenLocked(tokenHash)
	}
}

func (c *WorkerAuthCache) removeTokenLocked(tokenHash string) {
	entry, ok := c.entries[tokenHash]
	if !ok {
		return
	}

	delete(c.entries, tokenHash)
	hashes, exists := c.tokensByWorker[entry.workerID]
	if !exists {
		return
	}

	delete(hashes, tokenHash)
	if len(hashes) == 0 {
		delete(c.tokensByWorker, entry.workerID)
	}
}

func (c *WorkerAuthCache) evictLocked(now time.Time) {
	// Prune expired entries first.
	for tokenHash, entry := range c.entries {
		if now.After(entry.expiresAt) {
			c.removeTokenLocked(tokenHash)
		}
	}

	for len(c.entries) > c.maxEntries {
		var oldestHash string
		var oldestExpiry time.Time
		first := true
		for tokenHash, entry := range c.entries {
			if first || entry.expiresAt.Before(oldestExpiry) {
				oldestHash = tokenHash
				oldestExpiry = entry.expiresAt
				first = false
			}
		}
		if first {
			return
		}
		c.removeTokenLocked(oldestHash)
	}
}
