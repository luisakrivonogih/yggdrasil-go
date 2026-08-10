package garlic

import "testing"

func TestSecurityCountersStartAtZero(t *testing.T) {
	var s SecurityCounters
	snap := s.snapshot()
	if snap != (SecurityCounterSnapshot{}) {
		t.Fatalf("snapshot() = %+v, want all zeros", snap)
	}
}

func TestSecurityCountersSnapshotReflectsIncrements(t *testing.T) {
	var s SecurityCounters
	s.replayDrops.Add(1)
	s.replayDrops.Add(1)
	s.malformedPackets.Add(1)
	s.expiredPackets.Add(3)
	s.authFailures.Add(1)
	s.relayTableFull.Add(1)

	snap := s.snapshot()
	want := SecurityCounterSnapshot{
		ReplayDrops:      2,
		MalformedPackets: 1,
		ExpiredPackets:   3,
		AuthFailures:     1,
		RelayTableFull:   1,
	}
	if snap != want {
		t.Fatalf("snapshot() = %+v, want %+v", snap, want)
	}
}
