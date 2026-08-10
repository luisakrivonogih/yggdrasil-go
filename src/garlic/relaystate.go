package garlic

// relayCircuitState tracks everything a relay node keeps about a
// circuit it forwards traffic on (as opposed to Circuit/CircuitManager
// in circuit.go, which is the *originator's* view of a circuit it
// created): the per-circuit ReplayWindow, and - for dashboard
// visibility - the immediate previous/next hop and traffic counters. A
// relay never learns, and this never stores, anything beyond its own
// two neighbors on a circuit; see manager.go's dispatchAction, the only
// place recordForward is called from. The table is itself
// capacity-bounded - a new circuit ID is refused once at capacity,
// exactly like RateLimiter's tracked-peer bound - so a remote peer
// can't make a relay accumulate unbounded per-circuit state just by
// sending traffic for new circuit IDs.

import (
	"sync"
	"time"
)

type relayCircuitInfo struct {
	window         *ReplayWindow
	previousHop    []byte
	nextHop        []byte
	firstSeen      time.Time
	lastActive     time.Time
	packetsRelayed uint64
	bytesRelayed   uint64
}

// RelayCircuitInfo is a point-in-time, serializable snapshot of one
// relayed circuit's locally-known state - used by the getGarlicCircuits
// admin handler (Task 7). PreviousHop/NextHop are exactly what this
// node, as an intermediate hop, actually knows: never a fabricated
// full path.
type RelayCircuitInfo struct {
	ID             CircuitID
	PreviousHop    []byte
	NextHop        []byte
	FirstSeen      time.Time
	LastActive     time.Time
	PacketsRelayed uint64
	BytesRelayed   uint64
}

type relayCircuitState struct {
	mu       sync.Mutex
	max      int
	circuits map[CircuitID]*relayCircuitInfo
}

func newRelayCircuitState(max int) *relayCircuitState {
	return &relayCircuitState{
		max:      max,
		circuits: make(map[CircuitID]*relayCircuitInfo),
	}
}

// replayWindowFor returns the ReplayWindow to use for circuit id,
// creating one on first use. ok is false if the table is at capacity and
// id is not already tracked, meaning the caller should refuse to relay
// for this circuit rather than grow unboundedly.
func (s *relayCircuitState) replayWindowFor(id CircuitID) (w *ReplayWindow, ok bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if info, exists := s.circuits[id]; exists {
		info.lastActive = time.Now()
		return info.window, true
	}
	if len(s.circuits) >= s.max {
		return nil, false
	}
	now := time.Now()
	info := &relayCircuitInfo{
		window:     NewReplayWindow(),
		firstSeen:  now,
		lastActive: now,
	}
	s.circuits[id] = info
	return info.window, true
}

// recordForward records that this node forwarded n bytes for id,
// arriving from previousHop and sent on to nextHop. A no-op if id isn't
// already tracked (recordForward is only ever called after a successful
// processCircuitData -> replayWindowFor call for the same id, so this
// only guards against being called out of order).
func (s *relayCircuitState) recordForward(id CircuitID, previousHop, nextHop []byte, n int) {
	s.mu.Lock()
	defer s.mu.Unlock()

	info, ok := s.circuits[id]
	if !ok {
		return
	}
	info.previousHop = append([]byte(nil), previousHop...)
	info.nextHop = append([]byte(nil), nextHop...)
	info.packetsRelayed++
	info.bytesRelayed += uint64(n)
	info.lastActive = time.Now()
}

// snapshot returns a point-in-time copy of every currently-tracked
// relayed circuit.
func (s *relayCircuitState) snapshot() []RelayCircuitInfo {
	s.mu.Lock()
	defer s.mu.Unlock()

	out := make([]RelayCircuitInfo, 0, len(s.circuits))
	for id, info := range s.circuits {
		out = append(out, RelayCircuitInfo{
			ID:             id,
			PreviousHop:    append([]byte(nil), info.previousHop...),
			NextHop:        append([]byte(nil), info.nextHop...),
			FirstSeen:      info.firstSeen,
			LastActive:     info.lastActive,
			PacketsRelayed: info.packetsRelayed,
			BytesRelayed:   info.bytesRelayed,
		})
	}
	return out
}

// count returns the number of circuits currently tracked.
func (s *relayCircuitState) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.circuits)
}

// expireStale removes tracked circuits not touched within maxAge,
// returning how many were removed.
func (s *relayCircuitState) expireStale(maxAge time.Duration) int {
	cutoff := time.Now().Add(-maxAge)

	s.mu.Lock()
	defer s.mu.Unlock()

	var stale []CircuitID
	for id, info := range s.circuits {
		if info.lastActive.Before(cutoff) {
			stale = append(stale, id)
		}
	}
	for _, id := range stale {
		delete(s.circuits, id)
	}
	return len(stale)
}
