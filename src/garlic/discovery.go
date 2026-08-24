package garlic

// Discovery: gossip of known Garlic-capable peers, so a node can find
// candidates it has never directly queried, without any non-Garlic party
// ever seeing the exchange. This works entirely over the existing
// typeSessionGarlic channel (msgTypeAnnounce, handled the same way as
// every other Garlic message type) - a node that never runs src/garlic
// cannot construct, send, or even parse an announce message, so
// "only Garlic nodes discover Garlic nodes" holds by construction, the
// same way capability negotiation already does. It does not (and
// cannot) prevent a party that already knows a specific node's key from
// probing whether *that* key answers capability requests - that
// probeability is inherent to having an unauthenticated capability
// handshake at all, and is a separate concern from discovering
// previously-unknown nodes. See docs/garlic-architecture.md's
// discovery discussion for that distinction.

import (
	"encoding/binary"
	"errors"
	"sync"
	"time"
)

const (
	maxAnnouncePeers   = 32
	maxAnnounceKeySize = 64
)

var (
	ErrTooManyAnnouncePeers = errors.New("garlic: too many announced peers")
	ErrAnnounceKeyTooLarge  = errors.New("garlic: announced key too large")
	ErrAnnounceTruncated    = errors.New("garlic: announce message truncated")
)

// AnnouncePeer is one peer entry in an AnnounceMessage: enough to add it
// as a discovery candidate without any further round trip (a capability
// query still happens before it's ever used as a circuit hop - discovery
// only seeds the candidate pool, it doesn't establish trust).
type AnnouncePeer struct {
	NodeKey         []byte
	GarlicPublicKey []byte
}

// AnnounceMessage is the body of a msgTypeAnnounce message: a bounded
// list of Garlic peers the sender already knows about.
type AnnounceMessage struct {
	Peers []AnnouncePeer
}

// Marshal encodes the message as count(4) followed by, per peer,
// node_key_len(1)+bytes and garlic_key_len(1)+bytes.
func (m *AnnounceMessage) Marshal() ([]byte, error) {
	if len(m.Peers) > maxAnnouncePeers {
		return nil, ErrTooManyAnnouncePeers
	}
	var buf []byte
	buf = binary.BigEndian.AppendUint32(buf, uint32(len(m.Peers)))
	for _, p := range m.Peers {
		if len(p.NodeKey) > maxAnnounceKeySize || len(p.GarlicPublicKey) > maxAnnounceKeySize {
			return nil, ErrAnnounceKeyTooLarge
		}
		buf = append(buf, byte(len(p.NodeKey)))
		buf = append(buf, p.NodeKey...)
		buf = append(buf, byte(len(p.GarlicPublicKey)))
		buf = append(buf, p.GarlicPublicKey...)
	}
	return buf, nil
}

// UnmarshalAnnounceMessage decodes a message produced by Marshal, never
// trusting a declared count or length before validating it against both
// the configured maximum and the bytes actually remaining.
func UnmarshalAnnounceMessage(data []byte) (*AnnounceMessage, error) {
	if len(data) < 4 {
		return nil, ErrAnnounceTruncated
	}
	count := binary.BigEndian.Uint32(data[:4])
	if count > maxAnnouncePeers {
		return nil, ErrTooManyAnnouncePeers
	}
	rest := data[4:]

	peers := make([]AnnouncePeer, 0, count)
	for range count {
		nodeKey, next, err := chopAnnounceKey(rest)
		if err != nil {
			return nil, err
		}
		rest = next
		garlicKey, next, err := chopAnnounceKey(rest)
		if err != nil {
			return nil, err
		}
		rest = next
		peers = append(peers, AnnouncePeer{NodeKey: nodeKey, GarlicPublicKey: garlicKey})
	}
	return &AnnounceMessage{Peers: peers}, nil
}

func chopAnnounceKey(data []byte) (key []byte, rest []byte, err error) {
	if len(data) < 1 {
		return nil, nil, ErrAnnounceTruncated
	}
	n := int(data[0])
	data = data[1:]
	if n > maxAnnounceKeySize {
		return nil, nil, ErrAnnounceKeyTooLarge
	}
	if n > len(data) {
		return nil, nil, ErrAnnounceTruncated
	}
	if n == 0 {
		return nil, data, nil
	}
	return append([]byte(nil), data[:n]...), data[n:], nil
}

// DiscoveredPeer is one entry in a discoveryRegistry.
type DiscoveredPeer struct {
	NodeKey         []byte
	GarlicPublicKey []byte
	LastSeen        time.Time
	// SelfVerified is true iff this node itself completed a capability
	// handshake with this peer (handleCapabilityResponse), as opposed to
	// only ever hearing about it secondhand via gossip (processAnnounce).
	// Never downgraded by record() once true - see its doc comment.
	SelfVerified bool
}

// discoveryRegistry is the bounded set of Garlic peers this node has
// learned about, directly (a successful capability query) or indirectly
// (gossiped to it by another peer). Capacity-bounded like every other
// remote-input-driven collection in this package: recording a new peer
// once at capacity evicts the least-recently-seen entry rather than
// growing without bound.
type discoveryRegistry struct {
	mu    sync.Mutex
	max   int
	peers map[string]DiscoveredPeer
}

func newDiscoveryRegistry(max int) *discoveryRegistry {
	return &discoveryRegistry{max: max, peers: make(map[string]DiscoveredPeer)}
}

// record adds or refreshes a peer's entry, stamping LastSeen as now.
// SelfVerified is never downgraded: once a peer has been personally
// capability-verified, a later secondhand gossip mention of the same key
// still leaves it marked self-verified.
func (r *discoveryRegistry) record(p DiscoveredPeer) {
	key := string(p.NodeKey)
	p.LastSeen = time.Now()

	r.mu.Lock()
	defer r.mu.Unlock()
	existing, exists := r.peers[key]
	if !exists && len(r.peers) >= r.max {
		r.evictOldestLocked()
	}
	if exists && existing.SelfVerified {
		p.SelfVerified = true
	}
	r.peers[key] = p
}

func (r *discoveryRegistry) evictOldestLocked() {
	var oldestKey string
	var oldestTime time.Time
	first := true
	for k, p := range r.peers {
		if first || p.LastSeen.Before(oldestTime) {
			oldestKey, oldestTime, first = k, p.LastSeen, false
		}
	}
	if !first {
		delete(r.peers, oldestKey)
	}
}

// list returns every currently-tracked peer.
func (r *discoveryRegistry) list() []DiscoveredPeer {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]DiscoveredPeer, 0, len(r.peers))
	for _, p := range r.peers {
		out = append(out, p)
	}
	return out
}

// sample returns up to n peers (all of them, if fewer than n are known).
// Not cryptographically random - this selects gossip fan-out and
// announce content, not anything security-critical.
func (r *discoveryRegistry) sample(n int) []DiscoveredPeer {
	all := r.list()
	if n >= len(all) {
		return all
	}
	return all[:n]
}
