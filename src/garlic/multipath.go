package garlic

// Multipath (Phase "не одна дорога, а разные" of the design
// conversation this implements): instead of sending an entire
// conversation over one circuit, spread it across several independently
// built ones. Whoever observes traffic on any single path sees only a
// fraction of the sender's total traffic to a destination, and a Sybil
// adversary needs to control every path in the pool - not just one - to
// reconstruct the whole picture. This is a real strengthening of both
// concerns, not just cosmetic: it directly raises the cost analyzed in
// docs/garlic-threat-model.md's Sybil and traffic-correlation sections.

import (
	"crypto/rand"
	"encoding/binary"
	"sync"
)

// PoolID identifies a circuit pool, chosen at random by its creator.
type PoolID uint64

func randomPoolID() (PoolID, error) {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return 0, err
	}
	return PoolID(binary.BigEndian.Uint64(b[:])), nil
}

// circuitPool round-robins sends across a fixed set of circuits. Safe for
// concurrent use.
type circuitPool struct {
	mu       sync.Mutex
	circuits []CircuitID
	next     int
}

func newCircuitPool(circuits []CircuitID) *circuitPool {
	return &circuitPool{circuits: append([]CircuitID(nil), circuits...)}
}

// nextCircuit returns the next circuit ID in round-robin order. ok is
// false if the pool has no circuits.
func (p *circuitPool) nextCircuit() (id CircuitID, ok bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.circuits) == 0 {
		return CircuitID{}, false
	}
	id = p.circuits[p.next%len(p.circuits)]
	p.next++
	return id, true
}

// all returns every circuit ID in the pool.
func (p *circuitPool) all() []CircuitID {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]CircuitID(nil), p.circuits...)
}
