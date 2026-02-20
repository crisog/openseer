package worker

import (
	"testing"
	"time"
)

func TestHeartbeatTrackerShouldUpdate(t *testing.T) {
	t.Parallel()

	tracker := newHeartbeatTracker(40 * time.Millisecond)
	workerID := "worker-1"

	if !tracker.ShouldUpdate(workerID) {
		t.Fatalf("expected first update to be allowed")
	}
	if tracker.ShouldUpdate(workerID) {
		t.Fatalf("expected immediate second update to be coalesced")
	}

	time.Sleep(50 * time.Millisecond)
	if !tracker.ShouldUpdate(workerID) {
		t.Fatalf("expected update after interval to be allowed")
	}
}

func TestHeartbeatTrackerInvalidate(t *testing.T) {
	t.Parallel()

	tracker := newHeartbeatTracker(time.Minute)
	workerID := "worker-1"

	if !tracker.ShouldUpdate(workerID) {
		t.Fatalf("expected first update to be allowed")
	}
	if tracker.ShouldUpdate(workerID) {
		t.Fatalf("expected immediate second update to be coalesced")
	}

	tracker.Invalidate(workerID)
	if !tracker.ShouldUpdate(workerID) {
		t.Fatalf("expected update after invalidate to be allowed")
	}
}
