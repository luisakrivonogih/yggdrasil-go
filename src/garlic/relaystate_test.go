package garlic

import (
	"testing"
	"time"
)

func TestRelayCircuitStateCreatesWindowOnFirstUse(t *testing.T) {
	s := newRelayCircuitState(1024)
	w, ok := s.replayWindowFor(testCircuitID(1))
	if !ok {
		t.Fatal("replayWindowFor ok = false, want true")
	}
	if w == nil {
		t.Fatal("replayWindowFor returned a nil window")
	}
}

func TestRelayCircuitStateReturnsSameWindowForSameCircuit(t *testing.T) {
	s := newRelayCircuitState(1024)
	w1, _ := s.replayWindowFor(testCircuitID(1))
	w2, _ := s.replayWindowFor(testCircuitID(1))
	if w1 != w2 {
		t.Error("replayWindowFor returned different windows for the same circuit ID")
	}
	// And that window must actually behave as replay protection across
	// the two calls - a counter accepted once is rejected the second time.
	if !w1.CheckAndSet(5) {
		t.Fatal("first CheckAndSet(5) = false, want true")
	}
	if w2.CheckAndSet(5) {
		t.Fatal("second CheckAndSet(5) via the same circuit's window = true, want false (replay)")
	}
}

func TestRelayCircuitStateBoundedCapacity(t *testing.T) {
	s := newRelayCircuitState(1)
	if _, ok := s.replayWindowFor(testCircuitID(1)); !ok {
		t.Fatal("replayWindowFor(1) ok = false, want true")
	}
	if _, ok := s.replayWindowFor(testCircuitID(2)); ok {
		t.Fatal("replayWindowFor(2) ok = true, want false (table at capacity)")
	}
}

func TestRelayCircuitStateExpireStaleFreesCapacity(t *testing.T) {
	s := newRelayCircuitState(1)
	if _, ok := s.replayWindowFor(testCircuitID(1)); !ok {
		t.Fatal("replayWindowFor(1) ok = false, want true")
	}
	time.Sleep(5 * time.Millisecond)

	if n := s.expireStale(time.Millisecond); n != 1 {
		t.Fatalf("expireStale removed %d, want 1", n)
	}
	if _, ok := s.replayWindowFor(testCircuitID(2)); !ok {
		t.Fatal("replayWindowFor(2) after expireStale ok = false, want true (capacity freed)")
	}
}

func TestRelayCircuitStateDifferentCircuitsHaveIndependentReplayWindows(t *testing.T) {
	s := newRelayCircuitState(1024)
	wA, _ := s.replayWindowFor(testCircuitID(1))
	wB, _ := s.replayWindowFor(testCircuitID(2))

	if !wA.CheckAndSet(5) {
		t.Fatal("first CheckAndSet(5) on circuit A = false, want true")
	}
	// The same counter value on a *different* circuit ID must be
	// unaffected - replay state is scoped per circuit, not global, so
	// two circuits never accidentally share replay-window context.
	if !wB.CheckAndSet(5) {
		t.Fatal("CheckAndSet(5) on circuit B = false, want true (independent window from circuit A)")
	}
}

// TestRelayCircuitStateEvictedWindowStartsFreshOnReuse documents the
// deliberate bounded-memory tradeoff (Part 2 of the hardening task,
// "replay cache eviction"): once a circuit's replay window has been
// evicted (expireStale), a later message claiming that same circuit ID
// gets a *fresh* window, not a resurrected one - this relay has no
// memory of what counters it saw before eviction. This is expected
// behavior of a capacity-bounded cache, not a defect - callers must not
// assume eviction-proof replay protection.
func TestRelayCircuitStateEvictedWindowStartsFreshOnReuse(t *testing.T) {
	s := newRelayCircuitState(1024)
	id := testCircuitID(1)
	w, _ := s.replayWindowFor(id)
	if !w.CheckAndSet(5) {
		t.Fatal("CheckAndSet(5) = false, want true")
	}
	time.Sleep(5 * time.Millisecond)
	if n := s.expireStale(time.Millisecond); n != 1 {
		t.Fatalf("expireStale removed %d, want 1", n)
	}

	w2, ok := s.replayWindowFor(id)
	if !ok {
		t.Fatal("replayWindowFor after eviction ok = false, want true")
	}
	if !w2.CheckAndSet(5) {
		t.Fatal("CheckAndSet(5) on the post-eviction window = false, want true (a fresh window, not resurrected replay state)")
	}
}
