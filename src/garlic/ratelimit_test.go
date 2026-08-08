package garlic

import (
	"testing"
	"time"
)

func TestRateLimiterAllowsWithinBurst(t *testing.T) {
	r := NewRateLimiter(1, 5, 1024)
	peer := []byte("peer-A")
	for i := range 5 {
		if !r.Allow(peer) {
			t.Fatalf("Allow() call %d = false, want true (within burst)", i+1)
		}
	}
	if r.Allow(peer) {
		t.Fatal("Allow() call 6 = true, want false (burst exhausted)")
	}
}

func TestRateLimiterRefillsOverTime(t *testing.T) {
	r := NewRateLimiter(1000, 1, 1024)
	peer := []byte("peer-A")
	if !r.Allow(peer) {
		t.Fatal("first Allow() = false, want true")
	}
	if r.Allow(peer) {
		t.Fatal("second immediate Allow() = true, want false (burst of 1 exhausted)")
	}
	time.Sleep(5 * time.Millisecond) // at 1000/sec, several tokens should have refilled
	if !r.Allow(peer) {
		t.Fatal("Allow() after refill delay = false, want true")
	}
}

func TestRateLimiterTracksPeersIndependently(t *testing.T) {
	r := NewRateLimiter(1, 1, 1024)
	peerA := []byte("peer-A")
	peerB := []byte("peer-B")

	if !r.Allow(peerA) {
		t.Fatal("Allow(peerA) #1 = false, want true")
	}
	if r.Allow(peerA) {
		t.Fatal("Allow(peerA) #2 = true, want false (peerA's burst exhausted)")
	}
	if !r.Allow(peerB) {
		t.Fatal("Allow(peerB) #1 = false, want true (peerB has its own budget)")
	}
}

func TestRateLimiterBoundedTrackedPeers(t *testing.T) {
	r := NewRateLimiter(1, 1, 1)
	if !r.Allow([]byte("peer-A")) {
		t.Fatal("Allow(peer-A) = false, want true (first peer, room available)")
	}
	if r.Allow([]byte("peer-B")) {
		t.Fatal("Allow(peer-B) = true, want false (tracked-peer limit reached, fail closed)")
	}
}

func TestRateLimiterCleanupRemovesStaleBuckets(t *testing.T) {
	r := NewRateLimiter(1, 1, 1)
	if !r.Allow([]byte("peer-A")) {
		t.Fatal("Allow(peer-A) = false, want true")
	}
	time.Sleep(5 * time.Millisecond)

	if n := r.Cleanup(time.Millisecond); n != 1 {
		t.Fatalf("Cleanup() removed %d buckets, want 1", n)
	}
	// With peer-A's bucket cleaned up, a different peer must now fit
	// within the tracked-peer bound.
	if !r.Allow([]byte("peer-B")) {
		t.Fatal("Allow(peer-B) after Cleanup = false, want true (slot freed)")
	}
}
