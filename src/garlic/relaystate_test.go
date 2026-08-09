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
