package garlic

// Garlic ties the protocol pieces from earlier phases to a running
// Yggdrasil node: it registers with core.Core's optional Garlic transport
// hook (src/core/garlic.go) and implements the request/response and
// circuit-relay logic in protocol.go over it. See
// docs/garlic-architecture.md §3.3 for why this is the only integration
// point needed, and §3.12 for the API shape this follows.
//
// Circuit construction here is deliberately non-interactive: the
// originator generates one fresh ephemeral X25519 keypair per circuit
// and computes ECDH against each hop's already-known long-term Garlic
// public key (learned via capability negotiation) to derive that hop's
// layer key. Every hop can independently redo the same ECDH on receipt
// using its own long-term private key, so no telescoping handshake is
// needed to set up a circuit - at the cost of every hop sharing the same
// ephemeral public key for a given circuit, a known linkability
// limitation documented in docs/garlic-security.md.

import (
	"crypto/ed25519"
	"encoding/hex"
	"errors"
	"sync"
	"time"

	iwt "github.com/Arceliar/ironwood/types"

	"github.com/yggdrasil-network/yggdrasil-go/src/core"
)

// Config holds the tunable, DoS-relevant parameters for a Garlic
// instance. See docs/garlic-architecture.md §3.11 for the corresponding
// YAML config block.
type Config struct {
	PathLength           int
	CircuitLifetime      time.Duration
	MaxCircuits          int
	MaxCircuitsPerPeer   int
	MaxRelayCircuits     int
	PacketTTL            time.Duration
	MaxPacketsPerCircuit uint64
	MaxBytesPerCircuit   uint64
	RatePerSecond        float64
	RateBurst            float64
	MaxTrackedPeers      int
	CapabilityTimeout    time.Duration

	// PaddingEnabled controls per-hop packet size randomization (see
	// Envelope.PadToRandomRange's doc comment): both the originator and
	// every relay independently re-randomize the wire size of the
	// envelope they send within [MinPaddedSize, MaxPaddedSize], so a
	// given hop-to-hop link's packet sizes don't match the sizes seen on
	// the next link - a defense against size-based traffic correlation
	// (docs/garlic-threat-model.md, "Traffic correlation").
	PaddingEnabled bool
	MinPaddedSize  int
	MaxPaddedSize  int
}

// DefaultConfig returns conservative defaults suitable for a small
// deployment.
func DefaultConfig() Config {
	return Config{
		PathLength:           3,
		CircuitLifetime:      10 * time.Minute,
		MaxCircuits:          1024,
		MaxCircuitsPerPeer:   64,
		MaxRelayCircuits:     4096,
		PacketTTL:            60 * time.Second,
		MaxPacketsPerCircuit: 100000,
		MaxBytesPerCircuit:   100 * 1024 * 1024,
		RatePerSecond:        50,
		RateBurst:            200,
		MaxTrackedPeers:      4096,
		CapabilityTimeout:    6 * time.Second,
		PaddingEnabled:       true,
		MinPaddedSize:        512,
		MaxPaddedSize:        1400,
	}
}

var (
	ErrInvalidPath       = errors.New("garlic: invalid circuit path")
	ErrCircuitNotFound   = errors.New("garlic: circuit not found")
	ErrCapabilityTimeout = errors.New("garlic: capability request timed out")
	ErrRecvTimeout       = errors.New("garlic: no message received before timeout")
)

// DeliveredMessage is an application payload that arrived because this
// node was the final hop of someone else's circuit.
type DeliveredMessage struct {
	CircuitID CircuitID
	Payload   []byte
}

// Garlic is one node's Garlic Routing Overlay state. Construct with New;
// it registers itself with the given core.Core and is usable
// immediately.
type Garlic struct {
	core     *core.Core
	identity *Identity
	cfg      Config

	circuits   *CircuitManager
	relayState *relayCircuitState
	limiter    *RateLimiter
	rendezvous Rendezvous

	delivered chan DeliveredMessage

	mu              sync.Mutex
	capabilityCache map[string]*CapabilityMessage
	pending         map[string]chan *CapabilityMessage
	originEphemeral map[CircuitID][]byte

	stop chan struct{}
}

// New constructs a Garlic instance bound to c, using identity as this
// node's long-term Garlic identity and rendezvous for service
// publish/lookup. It registers a handler with c immediately (see
// core.Core.SetGarlicHandler) and starts a background cleanup loop;
// call Close to stop both.
func New(c *core.Core, identity *Identity, cfg Config, rendezvous Rendezvous) *Garlic {
	g := &Garlic{
		core:            c,
		identity:        identity,
		cfg:             cfg,
		circuits:        NewCircuitManager(CircuitManagerConfig{MaxCircuits: cfg.MaxCircuits, MaxCircuitsPerPeer: cfg.MaxCircuitsPerPeer}),
		relayState:      newRelayCircuitState(cfg.MaxRelayCircuits),
		limiter:         NewRateLimiter(cfg.RatePerSecond, cfg.RateBurst, cfg.MaxTrackedPeers),
		rendezvous:      rendezvous,
		delivered:       make(chan DeliveredMessage, 256),
		capabilityCache: make(map[string]*CapabilityMessage),
		pending:         make(map[string]chan *CapabilityMessage),
		originEphemeral: make(map[CircuitID][]byte),
		stop:            make(chan struct{}),
	}
	c.SetGarlicHandler(g.handleIncoming)
	go g.cleanupLoop()
	return g
}

// Close unregisters from core.Core and stops the background cleanup
// loop. It does not close the underlying core.Core.
func (g *Garlic) Close() {
	g.core.SetGarlicHandler(nil)
	close(g.stop)
}

func (g *Garlic) cleanupLoop() {
	t := time.NewTicker(30 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-t.C:
			g.circuits.ExpireStale()
			g.relayState.expireStale(2 * g.cfg.CircuitLifetime)
			g.limiter.Cleanup(time.Hour)
		case <-g.stop:
			return
		}
	}
}

// Identity returns this node's long-term Garlic identity.
func (g *Garlic) Identity() *Identity {
	return g.identity
}

// handleIncoming is the core.GarlicHandler registered with core.Core. It
// must not block (see GarlicHandler's doc comment), so every branch here
// either returns immediately or hands off a bounded amount of work.
func (g *Garlic) handleIncoming(from ed25519.PublicKey, data []byte) {
	if !g.limiter.Allow(from) {
		return
	}
	if len(data) == 0 {
		return
	}
	switch data[0] {
	case msgTypeCapabilityRequest:
		resp := append([]byte{msgTypeCapabilityResponse}, g.processCapabilityRequest()...)
		_, _ = g.core.WriteGarlic(resp, iwt.Addr(from))
	case msgTypeCapabilityResponse:
		g.handleCapabilityResponse(from, data[1:])
	case msgTypeCircuitData:
		action := g.processCircuitData(data[1:])
		switch action.kind {
		case actionDeliver:
			select {
			case g.delivered <- DeliveredMessage{CircuitID: action.circuitID, Payload: action.payload}:
			default:
			}
		case actionForward:
			_, _ = g.core.WriteGarlic(action.forwardMsg, iwt.Addr(action.forwardTo))
		}
	}
}

func (g *Garlic) handleCapabilityResponse(from ed25519.PublicKey, body []byte) {
	msg, err := UnmarshalCapabilityMessage(body)
	if err != nil {
		return
	}
	key := hex.EncodeToString(from)

	g.mu.Lock()
	g.capabilityCache[key] = msg
	ch := g.pending[key]
	g.mu.Unlock()

	if ch != nil {
		select {
		case ch <- msg:
		default:
		}
	}
}

// QueryCapability asks peer which Garlic protocol versions it supports
// and, if any, its Garlic identity public key. It returns a cached
// result if one is already known, otherwise it sends a request and waits
// up to cfg.CapabilityTimeout. A timeout (ErrCapabilityTimeout) means
// peer should be treated as legacy: it must never be selected as a
// circuit hop or rendezvous point.
func (g *Garlic) QueryCapability(peer ed25519.PublicKey) (*CapabilityMessage, error) {
	key := hex.EncodeToString(peer)

	g.mu.Lock()
	if cached, ok := g.capabilityCache[key]; ok {
		g.mu.Unlock()
		return cached, nil
	}
	ch := make(chan *CapabilityMessage, 1)
	g.pending[key] = ch
	g.mu.Unlock()
	defer func() {
		g.mu.Lock()
		delete(g.pending, key)
		g.mu.Unlock()
	}()

	if _, err := g.core.WriteGarlic([]byte{msgTypeCapabilityRequest}, iwt.Addr(peer)); err != nil {
		return nil, err
	}
	select {
	case msg := <-ch:
		return msg, nil
	case <-time.After(g.cfg.CapabilityTimeout):
		return nil, ErrCapabilityTimeout
	}
}

// CreateCircuit builds and tracks a new circuit over path, an ordered
// list of hops the caller has already confirmed (e.g. via
// QueryCapability) are Garlic-capable. It returns the circuit's ID, used
// with SendGarlic and CloseCircuit.
func (g *Garlic) CreateCircuit(path []CapabilityMessage, nodeKeys [][]byte) (CircuitID, error) {
	if len(path) == 0 || len(path) != len(nodeKeys) {
		return 0, ErrInvalidPath
	}
	ephemeralPub, ephemeralPriv, err := GenerateKeypair()
	if err != nil {
		return 0, err
	}
	hops := make([]Hop, len(path))
	for i := range path {
		secret, err := ECDH(ephemeralPriv, path[i].PublicKey)
		if err != nil {
			return 0, err
		}
		key, err := DeriveKey(secret, nil, LabelLayerKey)
		if err != nil {
			return 0, err
		}
		hops[i] = Hop{NodeKey: nodeKeys[i], Key: key}
	}

	c, err := g.circuits.Add(hops, g.cfg.CircuitLifetime, g.cfg.MaxPacketsPerCircuit, g.cfg.MaxBytesPerCircuit)
	if err != nil {
		return 0, err
	}

	g.mu.Lock()
	g.originEphemeral[c.ID] = ephemeralPub
	g.mu.Unlock()
	return c.ID, nil
}

// CloseCircuit closes and stops tracking id.
func (g *Garlic) CloseCircuit(id CircuitID) {
	g.circuits.Close(id)
	g.mu.Lock()
	delete(g.originEphemeral, id)
	g.mu.Unlock()
}

// SendGarlic sends payload as one packet over the circuit id (previously
// created with CreateCircuit).
func (g *Garlic) SendGarlic(id CircuitID, payload []byte) error {
	c, ok := g.circuits.Get(id)
	if !ok {
		return ErrCircuitNotFound
	}
	g.mu.Lock()
	ephemeralPub := g.originEphemeral[id]
	g.mu.Unlock()
	if ephemeralPub == nil {
		return ErrCircuitNotFound
	}

	onion, firstHop, counter, err := c.Seal(payload)
	if err != nil {
		return err
	}
	expiration := uint64(time.Now().Add(g.cfg.PacketTTL).Unix())
	msg, err := buildCircuitDataMessage(ephemeralPub, id, counter, expiration, onion, g.cfg)
	if err != nil {
		return err
	}

	_, err = g.core.WriteGarlic(msg, iwt.Addr(firstHop))
	return err
}

// buildCircuitDataMessage assembles the wire message for one circuitData
// packet: msgTypeCircuitData || ephemeralPub || Envelope. It performs no
// I/O, so it's testable without a running core.Core - see protocol.go's
// doc comment on why the pure/I/O split matters here. If cfg.PaddingEnabled,
// the envelope's wire size is independently re-randomized per call (see
// Envelope.PadToRandomRange); a padding failure (e.g. misconfigured
// Min/MaxPaddedSize) degrades to unpadded rather than failing the send.
func buildCircuitDataMessage(ephemeralPub []byte, id CircuitID, counter, expiration uint64, onion []byte, cfg Config) ([]byte, error) {
	env := &Envelope{
		Version:       EnvelopeVersion1,
		CircuitID:     uint64(id),
		PacketCounter: counter,
		Expiration:    expiration,
		Body:          onion,
	}
	if cfg.PaddingEnabled {
		_ = env.PadToRandomRange(cfg.MinPaddedSize, cfg.MaxPaddedSize)
	}
	envBytes, err := env.Marshal()
	if err != nil {
		return nil, err
	}
	msg := make([]byte, 0, 1+len(ephemeralPub)+len(envBytes))
	msg = append(msg, msgTypeCircuitData)
	msg = append(msg, ephemeralPub...)
	msg = append(msg, envBytes...)
	return msg, nil
}

// RecvGarlic waits up to timeout for the next payload delivered to this
// node as a circuit's final hop.
func (g *Garlic) RecvGarlic(timeout time.Duration) (*DeliveredMessage, error) {
	select {
	case m := <-g.delivered:
		return &m, nil
	case <-time.After(timeout):
		return nil, ErrRecvTimeout
	}
}

// PublishService advertises this node's identity as reachable at
// introPoints for serviceID, returning the resulting GID.
func (g *Garlic) PublishService(serviceID []byte, introPoints []IntroPoint, ttl time.Duration) (GID, error) {
	gid := ComputeGID(g.identity.PublicKey, serviceID)
	if err := g.rendezvous.Publish(gid, introPoints, ttl); err != nil {
		return GID{}, err
	}
	return gid, nil
}

// LookupService returns the currently-published introduction points for
// gid.
func (g *Garlic) LookupService(gid GID) ([]IntroPoint, error) {
	return g.rendezvous.Lookup(gid)
}

// Stats summarizes a Garlic instance's current state, for GetStats.
type Stats struct {
	OriginatedCircuits int
	RelayedCircuits    int
}

// GetStats returns a snapshot of this instance's current circuit counts.
func (g *Garlic) GetStats() Stats {
	return Stats{
		OriginatedCircuits: g.circuits.Count(),
		RelayedCircuits:    g.relayState.count(),
	}
}
