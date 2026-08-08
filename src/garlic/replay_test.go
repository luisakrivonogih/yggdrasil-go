package garlic

import "testing"

func TestReplayWindowAcceptsIncreasingCounters(t *testing.T) {
	w := NewReplayWindow()
	for i := uint64(1); i <= 5; i++ {
		if !w.CheckAndSet(i) {
			t.Fatalf("CheckAndSet(%d) = false, want true (fresh, increasing)", i)
		}
	}
}

func TestReplayWindowRejectsExactDuplicate(t *testing.T) {
	w := NewReplayWindow()
	if !w.CheckAndSet(10) {
		t.Fatal("first CheckAndSet(10) = false, want true")
	}
	if w.CheckAndSet(10) {
		t.Fatal("second CheckAndSet(10) = true, want false (replay)")
	}
}

func TestReplayWindowAcceptsOutOfOrderWithinWindow(t *testing.T) {
	w := NewReplayWindow()
	if !w.CheckAndSet(100) {
		t.Fatal("CheckAndSet(100) = false, want true")
	}
	// 95 is behind the highest-seen (100) but still within the sliding
	// window, and hasn't been seen yet - must be accepted.
	if !w.CheckAndSet(95) {
		t.Fatal("CheckAndSet(95) = false, want true (fresh, within window)")
	}
	// Now that 95 has been seen, it must not be replayable.
	if w.CheckAndSet(95) {
		t.Fatal("replayed CheckAndSet(95) = true, want false")
	}
}

func TestReplayWindowRejectsCounterBelowWindow(t *testing.T) {
	w := NewReplayWindow()
	if !w.CheckAndSet(100000) {
		t.Fatal("CheckAndSet(100000) = false, want true")
	}
	// Something far enough behind the highest-seen counter to have fallen
	// out of the bounded window must be rejected outright, whether or not
	// it was ever actually seen - this is what keeps the cache bounded.
	if w.CheckAndSet(1) {
		t.Fatal("CheckAndSet(1) = true, want false (far below window)")
	}
}

func TestReplayWindowRejectsZeroCounterAfterAnyAdvance(t *testing.T) {
	w := NewReplayWindow()
	if !w.CheckAndSet(0) {
		t.Fatal("first CheckAndSet(0) = false, want true")
	}
	if w.CheckAndSet(0) {
		t.Fatal("second CheckAndSet(0) = true, want false (replay)")
	}
}

func TestReplayWindowMemoryStaysBounded(t *testing.T) {
	w := NewReplayWindow()
	// An attacker driving the counter arbitrarily high must not be able to
	// grow the window's memory footprint - it's a fixed-size bitmap
	// regardless of how far the counter advances.
	for i := uint64(0); i < 1_000_000; i += 997 {
		w.CheckAndSet(i)
	}
	if got := w.memoryBytes(); got > maxReplayWindowMemoryBytes {
		t.Fatalf("replay window memory = %d bytes, want <= %d", got, maxReplayWindowMemoryBytes)
	}
}
