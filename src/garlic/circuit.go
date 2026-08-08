package garlic

// Circuit state (Phase 5 of the roadmap): a built path of hops plus the
// bookkeeping needed to bound its lifetime - expiration, and max
// packets/bytes, per docs/garlic-architecture.md §3.10 and §14. Rekeying
// in this design means building a replacement Circuit and retiring this
// one once any of those limits is hit; there is no in-place key rotation
// within a single Circuit.

import (
	"crypto/rand"
	"encoding/binary"
	"errors"
	"sync"
	"time"
)

// MaxPathLength bounds how many hops a single circuit may have. This
// exists to keep both the onion's size and a single Seal call's cost
// bounded, independent of anything a remote peer controls.
const MaxPathLength = 8

var (
	ErrPathTooLong                = errors.New("garlic: circuit path exceeds maximum length")
	ErrCircuitClosed              = errors.New("garlic: circuit is closed")
	ErrCircuitExpired             = errors.New("garlic: circuit has expired")
	ErrCircuitPacketLimitExceeded = errors.New("garlic: circuit packet limit exceeded")
	ErrCircuitByteLimitExceeded   = errors.New("garlic: circuit byte limit exceeded")
)

// CircuitID identifies a circuit to the hops that make it up. It is
// chosen at random by the circuit's creator.
type CircuitID uint64

// Circuit is one Garlic circuit as seen by its originator: an ordered
// path of hops with already-derived per-hop keys, plus expiry and
// packet/byte budgets. It is safe for concurrent use.
type Circuit struct {
	ID         CircuitID
	CreatedAt  time.Time
	ExpiresAt  time.Time
	MaxPackets uint64
	MaxBytes   uint64

	mu          sync.Mutex
	hops        []Hop
	closed      bool
	packetsSent uint64
	bytesSent   uint64
}

// NewCircuit builds a new Circuit over path hops (hops[0] is the first
// hop the sender transmits to). hops is copied, so the caller's slice may
// be reused/modified afterward.
func NewCircuit(hops []Hop, lifetime time.Duration, maxPackets, maxBytes uint64) (*Circuit, error) {
	if len(hops) == 0 {
		return nil, ErrEmptyPath
	}
	if len(hops) > MaxPathLength {
		return nil, ErrPathTooLong
	}
	id, err := randomCircuitID()
	if err != nil {
		return nil, err
	}
	now := time.Now()
	return &Circuit{
		ID:         id,
		CreatedAt:  now,
		ExpiresAt:  now.Add(lifetime),
		MaxPackets: maxPackets,
		MaxBytes:   maxBytes,
		hops:       append([]Hop(nil), hops...),
	}, nil
}

func randomCircuitID() (CircuitID, error) {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return 0, err
	}
	return CircuitID(binary.BigEndian.Uint64(b[:])), nil
}

// FirstHop returns the node key of the circuit's first hop.
func (c *Circuit) FirstHop() []byte {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.hops[0].NodeKey
}

// Seal builds a layered-encrypted onion carrying payload over the
// circuit's path, using and then advancing each hop's per-hop counter so
// a later call never reuses a (key, counter) pair. Every hop's counter
// stays in lockstep (all start at 0 and are incremented together each
// call), so Seal also returns that shared counter value - the caller
// puts it in the wire Envelope's PacketCounter field unchanged, and every
// hop along the path uses that same field for its own DecryptLayer call,
// with no need for hops to otherwise coordinate per-hop counter state.
//
// It returns the onion to transmit, the node key of the first hop to
// send it to, and the counter used.
//
// It fails - without mutating any state - once the circuit is closed,
// expired, or would exceed its packet/byte budget; the caller is expected
// to build a replacement circuit (rekey) in that case rather than retry.
func (c *Circuit) Seal(payload []byte) (onion []byte, firstHop []byte, counter uint64, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.closed {
		return nil, nil, 0, ErrCircuitClosed
	}
	if time.Now().After(c.ExpiresAt) {
		return nil, nil, 0, ErrCircuitExpired
	}
	if c.packetsSent+1 > c.MaxPackets {
		return nil, nil, 0, ErrCircuitPacketLimitExceeded
	}
	if c.bytesSent+uint64(len(payload)) > c.MaxBytes {
		return nil, nil, 0, ErrCircuitByteLimitExceeded
	}

	onion, err = BuildOnion(c.hops, payload)
	if err != nil {
		return nil, nil, 0, err
	}
	counter = c.hops[0].Counter
	for i := range c.hops {
		c.hops[i].Counter++
	}
	c.packetsSent++
	c.bytesSent += uint64(len(payload))
	return onion, c.hops[0].NodeKey, counter, nil
}

// Close marks the circuit unusable for further Seal calls.
func (c *Circuit) Close() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.closed = true
}

// Expired reports whether the circuit's lifetime has elapsed.
func (c *Circuit) Expired() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return time.Now().After(c.ExpiresAt)
}
