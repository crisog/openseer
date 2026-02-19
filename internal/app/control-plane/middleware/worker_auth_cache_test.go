package middleware

import (
	"testing"
	"time"
)

func TestWorkerAuthCacheGetSet(t *testing.T) {
	t.Parallel()

	cache := NewWorkerAuthCache(100*time.Millisecond, 10)
	cache.Set("token-hash-1", "worker-1")

	workerID, ok := cache.Get("token-hash-1")
	if !ok {
		t.Fatalf("expected cache hit")
	}
	if workerID != "worker-1" {
		t.Fatalf("expected worker-1, got %s", workerID)
	}
}

func TestWorkerAuthCacheExpiry(t *testing.T) {
	t.Parallel()

	cache := NewWorkerAuthCache(30*time.Millisecond, 10)
	cache.Set("token-hash-1", "worker-1")

	time.Sleep(40 * time.Millisecond)

	if _, ok := cache.Get("token-hash-1"); ok {
		t.Fatalf("expected entry to expire")
	}
}

func TestWorkerAuthCacheInvalidateWorker(t *testing.T) {
	t.Parallel()

	cache := NewWorkerAuthCache(5*time.Second, 10)
	cache.Set("token-hash-1", "worker-1")
	cache.Set("token-hash-2", "worker-1")
	cache.Set("token-hash-3", "worker-2")

	cache.InvalidateWorker("worker-1")

	if _, ok := cache.Get("token-hash-1"); ok {
		t.Fatalf("expected token-hash-1 to be invalidated")
	}
	if _, ok := cache.Get("token-hash-2"); ok {
		t.Fatalf("expected token-hash-2 to be invalidated")
	}
	if workerID, ok := cache.Get("token-hash-3"); !ok || workerID != "worker-2" {
		t.Fatalf("expected worker-2 token to remain")
	}
}

func TestWorkerAuthCacheMaxEntries(t *testing.T) {
	t.Parallel()

	cache := NewWorkerAuthCache(time.Hour, 2)
	cache.Set("token-hash-1", "worker-1")
	time.Sleep(5 * time.Millisecond)
	cache.Set("token-hash-2", "worker-2")
	time.Sleep(5 * time.Millisecond)
	cache.Set("token-hash-3", "worker-3")

	if _, ok := cache.Get("token-hash-1"); ok {
		t.Fatalf("expected oldest entry to be evicted")
	}
	if _, ok := cache.Get("token-hash-2"); !ok {
		t.Fatalf("expected token-hash-2 to remain")
	}
	if _, ok := cache.Get("token-hash-3"); !ok {
		t.Fatalf("expected token-hash-3 to remain")
	}
}
