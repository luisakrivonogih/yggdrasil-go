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

func TestRelayCircuitStateSnapshotOmitsUnconfirmedEntries(t *testing.T) {
	s := newRelayCircuitState(1024)
	// replayWindowFor reserves the entry before ECDH/decrypt has
	// succeeded - this node's relay role for the circuit is not yet
	// confirmed and it has no real previous/next hop to report.
	if _, ok := s.replayWindowFor(CircuitID(1)); !ok {
		t.Fatal("replayWindowFor(1) ok = false, want true")
	}

	if snap := s.snapshot(); len(snap) != 0 {
		t.Fatalf("snapshot() = %+v, want empty (a reserved-but-unconfirmed entry must not surface as a phantom relay)", snap)
	}

	// The reserved entry still occupies a real capacity slot - only the
	// dashboard-facing snapshot excludes it, never the accounting.
	if n := s.count(); n != 1 {
		t.Fatalf("count() = %d, want 1 (a pending entry still counts against the capacity bound)", n)
	}

	// Once the relay role is confirmed by an actual forward, it appears.
	s.recordForward(CircuitID(1), []byte("prev"), []byte("next"), 10)
	if snap := s.snapshot(); len(snap) != 1 {
		t.Fatalf("snapshot() after recordForward = %+v, want 1 entry", snap)
	}
}

func TestRelayCircuitStateSnapshotIsSortedByCircuitID(t *testing.T) {
	s := newRelayCircuitState(1024)
	// Deliberately unsorted insertion order - a snapshot() that just
	// ranged over the map would come back in Go's randomized order.
	for _, id := range []CircuitID{9, 2, 7, 1, 40, 3} {
		if _, ok := s.replayWindowFor(id); !ok {
			t.Fatalf("replayWindowFor(%d) ok = false, want true", id)
		}
		s.recordForward(id, []byte("prev"), []byte("next"), 10)
	}

	snap := s.snapshot()
	if len(snap) != 6 {
		t.Fatalf("snapshot() returned %d entries, want 6", len(snap))
	}
	for i := 1; i < len(snap); i++ {
		if snap[i-1].ID >= snap[i].ID {
			got := make([]CircuitID, 0, len(snap))
			for _, e := range snap {
				got = append(got, e.ID)
			}
			t.Fatalf("snapshot() IDs = %v, want ascending order", got)
		}
	}
}

func TestRelayCircuitStateCountIncludesUnconfirmedEntriesForCapacity(t *testing.T) {
	// A reserved-but-unconfirmed entry must still consume capacity,
	// otherwise a peer could reserve unbounded entries that never get
	// confirmed and never count against the bound.
	s := newRelayCircuitState(1)
	if _, ok := s.replayWindowFor(CircuitID(1)); !ok {
		t.Fatal("replayWindowFor(1) ok = false, want true")
	}
	if n := s.count(); n != 1 {
		t.Fatalf("count() = %d, want 1", n)
	}
	if _, ok := s.replayWindowFor(CircuitID(2)); ok {
		t.Fatal("replayWindowFor(2) ok = true, want false (an unconfirmed entry still fills the table)")
	}
}
