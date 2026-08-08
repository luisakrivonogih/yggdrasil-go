package garlic

// relayCircuitState tracks the per-circuit ReplayWindow a relay node
// maintains for circuits it forwards traffic on (as opposed to Circuit/
// CircuitManager in circuit.go, which is the *originator's* view of a
// circuit it created). The table is itself capacity-bounded - a new
// circuit ID is refused once at capacity, exactly like RateLimiter's
// tracked-peer bound - so a remote peer can't make a relay accumulate
// unbounded per-circuit state just by sending traffic for new circuit
// IDs.

import (
	"sync"
	"time"
)

type relayCircuitState struct {
	mu      sync.Mutex
	max     int
	windows map[CircuitID]*ReplayWindow
	touched map[CircuitID]time.Time
}

func newRelayCircuitState(max int) *relayCircuitState {
	return &relayCircuitState{
		max:     max,
		windows: make(map[CircuitID]*ReplayWindow),
		touched: make(map[CircuitID]time.Time),
	}
}

// replayWindowFor returns the ReplayWindow to use for circuit id,
// creating one on first use. ok is false if the table is at capacity and
// id is not already tracked, meaning the caller should refuse to relay
// for this circuit rather than grow unboundedly.
func (s *relayCircuitState) replayWindowFor(id CircuitID) (w *ReplayWindow, ok bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if w, exists := s.windows[id]; exists {
		s.touched[id] = time.Now()
		return w, true
	}
	if len(s.windows) >= s.max {
		return nil, false
	}
	w = NewReplayWindow()
	s.windows[id] = w
	s.touched[id] = time.Now()
	return w, true
}

// count returns the number of circuits currently tracked.
func (s *relayCircuitState) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.windows)
}

// expireStale removes tracked circuits not touched within maxAge,
// returning how many were removed.
func (s *relayCircuitState) expireStale(maxAge time.Duration) int {
	cutoff := time.Now().Add(-maxAge)

	s.mu.Lock()
	defer s.mu.Unlock()

	var stale []CircuitID
	for id, t := range s.touched {
		if t.Before(cutoff) {
			stale = append(stale, id)
		}
	}
	for _, id := range stale {
		delete(s.windows, id)
		delete(s.touched, id)
	}
	return len(stale)
}
