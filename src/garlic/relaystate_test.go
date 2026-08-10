package garlic

import (
	"testing"
	"time"
)

func TestRelayCircuitStateCreatesWindowOnFirstUse(t *testing.T) {
	s := newRelayCircuitState(1024)
	w, ok := s.replayWindowFor(CircuitID(1))
	if !ok {
		t.Fatal("replayWindowFor ok = false, want true")
	}
	if w == nil {
		t.Fatal("replayWindowFor returned a nil window")
	}
}

func TestRelayCircuitStateReturnsSameWindowForSameCircuit(t *testing.T) {
	s := newRelayCircuitState(1024)
	w1, _ := s.replayWindowFor(CircuitID(1))
	w2, _ := s.replayWindowFor(CircuitID(1))
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
	if _, ok := s.replayWindowFor(CircuitID(1)); !ok {
		t.Fatal("replayWindowFor(1) ok = false, want true")
	}
	if _, ok := s.replayWindowFor(CircuitID(2)); ok {
		t.Fatal("replayWindowFor(2) ok = true, want false (table at capacity)")
	}
}

func TestRelayCircuitStateExpireStaleFreesCapacity(t *testing.T) {
	s := newRelayCircuitState(1)
	if _, ok := s.replayWindowFor(CircuitID(1)); !ok {
		t.Fatal("replayWindowFor(1) ok = false, want true")
	}
	time.Sleep(5 * time.Millisecond)

	if n := s.expireStale(time.Millisecond); n != 1 {
		t.Fatalf("expireStale removed %d, want 1", n)
	}
	if _, ok := s.replayWindowFor(CircuitID(2)); !ok {
		t.Fatal("replayWindowFor(2) after expireStale ok = false, want true (capacity freed)")
	}
}

func TestRelayCircuitStateRecordForwardTracksHopsAndTraffic(t *testing.T) {
	s := newRelayCircuitState(1024)
	if _, ok := s.replayWindowFor(CircuitID(1)); !ok {
		t.Fatal("replayWindowFor(1) ok = false, want true")
	}
	s.recordForward(CircuitID(1), []byte("prev-hop"), []byte("next-hop"), 100)
	s.recordForward(CircuitID(1), []byte("prev-hop"), []byte("next-hop"), 50)

	snap := s.snapshot()
	if len(snap) != 1 {
		t.Fatalf("snapshot() returned %d entries, want 1", len(snap))
	}
	info := snap[0]
	if info.ID != CircuitID(1) {
		t.Fatalf("info.ID = %d, want 1", info.ID)
	}
	if string(info.PreviousHop) != "prev-hop" || string(info.NextHop) != "next-hop" {
		t.Fatalf("info.PreviousHop, NextHop = %q, %q, want \"prev-hop\", \"next-hop\"", info.PreviousHop, info.NextHop)
	}
	if info.PacketsRelayed != 2 {
		t.Fatalf("info.PacketsRelayed = %d, want 2", info.PacketsRelayed)
	}
	if info.BytesRelayed != 150 {
		t.Fatalf("info.BytesRelayed = %d, want 150", info.BytesRelayed)
	}
	if info.FirstSeen.IsZero() || info.LastActive.IsZero() {
		t.Fatal("FirstSeen/LastActive must be set")
	}
	if info.LastActive.Before(info.FirstSeen) {
		t.Fatal("LastActive must not be before FirstSeen")
	}
}

func TestRelayCircuitStateRecordForwardIsNoOpForUntrackedCircuit(t *testing.T) {
	s := newRelayCircuitState(1024)
	// No replayWindowFor call first - this circuit was never admitted.
	s.recordForward(CircuitID(99), []byte("prev"), []byte("next"), 10)
	if snap := s.snapshot(); len(snap) != 0 {
		t.Fatalf("snapshot() = %+v, want empty (recordForward must not create untracked circuits)", snap)
	}
}

func TestRelayCircuitStateSnapshotEmptyInitially(t *testing.T) {
	s := newRelayCircuitState(1024)
	if snap := s.snapshot(); len(snap) != 0 {
		t.Fatalf("snapshot() = %+v, want empty", snap)
	}
}

func TestRelayCircuitStateSnapshotOmitsExpiredEntries(t *testing.T) {
	s := newRelayCircuitState(1)
	if _, ok := s.replayWindowFor(CircuitID(1)); !ok {
		t.Fatal("replayWindowFor(1) ok = false, want true")
	}
	s.recordForward(CircuitID(1), []byte("prev"), []byte("next"), 10)
	time.Sleep(5 * time.Millisecond)
	s.expireStale(time.Millisecond)

	if snap := s.snapshot(); len(snap) != 0 {
		t.Fatalf("snapshot() after expireStale = %+v, want empty", snap)
	}
}
