package garlic

// CircuitManager bounds and tracks the circuits a node has originated
// (Phase 5/12 of the roadmap): a global cap and a per-first-hop-peer cap,
// so a remote peer can never make this node accumulate unbounded circuit
// state just by being reachable.

import (
	"encoding/hex"
	"errors"
	"sync"
	"time"
)

var (
	ErrTooManyCircuits        = errors.New("garlic: too many circuits")
	ErrTooManyCircuitsForPeer = errors.New("garlic: too many circuits through this peer")
)

// CircuitManagerConfig holds the DoS-relevant bounds for a CircuitManager.
type CircuitManagerConfig struct {
	MaxCircuits        int
	MaxCircuitsPerPeer int
}

// CircuitManager tracks live circuits under the bounds in its config. It
// is safe for concurrent use.
type CircuitManager struct {
	cfg CircuitManagerConfig

	mu       sync.Mutex
	circuits map[CircuitID]*Circuit
	perPeer  map[string]int
}

// NewCircuitManager returns an empty CircuitManager enforcing cfg.
func NewCircuitManager(cfg CircuitManagerConfig) *CircuitManager {
	return &CircuitManager{
		cfg:      cfg,
		circuits: make(map[CircuitID]*Circuit),
		perPeer:  make(map[string]int),
	}
}

func peerKeyOf(hops []Hop) string {
	return hex.EncodeToString(hops[0].NodeKey)
}

// Add builds a new circuit over hops and tracks it, subject to
// MaxCircuits and MaxCircuitsPerPeer. On success the circuit counts
// against both budgets until it is removed via Close or ExpireStale.
func (m *CircuitManager) Add(hops []Hop, lifetime time.Duration, maxPackets, maxBytes uint64) (*Circuit, error) {
	if len(hops) == 0 {
		return nil, ErrEmptyPath
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if len(m.circuits) >= m.cfg.MaxCircuits {
		return nil, ErrTooManyCircuits
	}
	peer := peerKeyOf(hops)
	if m.perPeer[peer] >= m.cfg.MaxCircuitsPerPeer {
		return nil, ErrTooManyCircuitsForPeer
	}

	c, err := NewCircuit(hops, lifetime, maxPackets, maxBytes)
	if err != nil {
		return nil, err
	}
	m.circuits[c.ID] = c
	m.perPeer[peer]++
	return c, nil
}

// Count returns the number of circuits currently tracked.
func (m *CircuitManager) Count() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.circuits)
}

// Get returns the circuit with the given ID, if tracked.
func (m *CircuitManager) Get(id CircuitID) (*Circuit, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	c, ok := m.circuits[id]
	return c, ok
}

// List returns a snapshot slice of every circuit currently tracked. The
// returned slice is a copy of the map's contents at the time of the
// call - safe to range over without holding m's lock, at the cost of
// possibly being immediately stale (fine for the admin-facing snapshot
// this exists for; nothing here is a hot path).
func (m *CircuitManager) List() []*Circuit {
	m.mu.Lock()
	defer m.mu.Unlock()
	list := make([]*Circuit, 0, len(m.circuits))
	for _, c := range m.circuits {
		list = append(list, c)
	}
	return list
}

// Close closes and stops tracking the circuit with the given ID, freeing
// its slot in both the global and per-peer budgets. It is a no-op if the
// ID isn't tracked.
func (m *CircuitManager) Close(id CircuitID) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m._remove(id)
}

// _remove closes and stops tracking id. Caller must hold m.mu.
func (m *CircuitManager) _remove(id CircuitID) {
	c, ok := m.circuits[id]
	if !ok {
		return
	}
	c.Close()
	delete(m.circuits, id)
	peer := peerKeyOf([]Hop{{NodeKey: c.FirstHop()}})
	if m.perPeer[peer] > 0 {
		m.perPeer[peer]--
		if m.perPeer[peer] == 0 {
			delete(m.perPeer, peer)
		}
	}
}

// ExpireStale closes and removes every tracked circuit whose lifetime has
// elapsed, returning how many were removed. Call periodically so a
// node's circuit table doesn't grow purely from circuits nobody
// explicitly closed.
func (m *CircuitManager) ExpireStale() int {
	m.mu.Lock()
	defer m.mu.Unlock()

	var expired []CircuitID
	for id, c := range m.circuits {
		if c.Expired() {
			expired = append(expired, id)
		}
	}
	for _, id := range expired {
		m._remove(id)
	}
	return len(expired)
}
