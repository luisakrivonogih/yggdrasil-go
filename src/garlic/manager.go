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
	"bytes"
	"crypto/ed25519"
	"encoding/hex"
	"errors"
	"net"
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

	// JitterEnabled controls random delay before actually transmitting a
	// circuitData packet (origin send or relay forward), independently
	// re-rolled per packet - the timing half of the traffic-correlation
	// defense described on PaddingEnabled. Delivered via a bounded
	// worker pool (jitter.go), never by blocking the caller.
	JitterEnabled   bool
	MinJitter       time.Duration
	MaxJitter       time.Duration
	JitterQueueSize int

	// Discovery: gossip of known Garlic-capable peers, entirely over the
	// existing typeSessionGarlic channel (see discovery.go's doc
	// comment for why this can't be seen by non-Garlic parties).
	// MaxDiscoveredPeers bounds the local registry; GossipInterval and
	// GossipFanout control how often, and to how many already-verified
	// peers, this node proactively shares a sample of what it knows.
	MaxDiscoveredPeers int
	GossipInterval     time.Duration
	GossipFanout       int
	GossipSampleSize   int

	// MinHopCount is SelectPath's minimum mesh distance (see
	// SelectDiversePath) - a Sybil-resistance measure: a candidate too
	// close to this node (e.g. a direct peer) is more likely to be run
	// by the same operator or network than one several hops away.
	MinHopCount int
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
		JitterEnabled:        true,
		MinJitter:            0,
		MaxJitter:            75 * time.Millisecond,
		JitterQueueSize:      1024,
		MaxDiscoveredPeers:   1024,
		GossipInterval:       30 * time.Second,
		GossipFanout:         2,
		GossipSampleSize:     16,
		MinHopCount:          2,
	}
}

// jitterWorkers is the fixed size of the jitter scheduler's worker pool.
// Not exposed in Config: it bounds concurrency, not memory, and the
// queue size is the DoS-relevant knob.
const jitterWorkers = 16

var (
	ErrInvalidPath       = errors.New("garlic: invalid circuit path")
	ErrCircuitNotFound   = errors.New("garlic: circuit not found")
	ErrCapabilityTimeout = errors.New("garlic: capability request timed out")
	ErrRecvTimeout       = errors.New("garlic: no message received before timeout")
	ErrPoolNotFound      = errors.New("garlic: circuit pool not found")
	ErrEmptyPool         = errors.New("garlic: circuit pool must have at least one path")
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
	scheduler  *jitterScheduler
	discovery  *discoveryRegistry

	delivered chan DeliveredMessage

	mu              sync.Mutex
	capabilityCache map[string]*CapabilityMessage
	pending         map[string]chan *CapabilityMessage
	originEphemeral map[CircuitID][]byte
	pools           map[PoolID]*circuitPool

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
		discovery:       newDiscoveryRegistry(cfg.MaxDiscoveredPeers),
		delivered:       make(chan DeliveredMessage, 256),
		capabilityCache: make(map[string]*CapabilityMessage),
		pending:         make(map[string]chan *CapabilityMessage),
		originEphemeral: make(map[CircuitID][]byte),
		pools:           make(map[PoolID]*circuitPool),
		stop:            make(chan struct{}),
	}
	g.scheduler = newJitterScheduler(func(data []byte, addr net.Addr) error {
		_, err := c.WriteGarlic(data, addr)
		return err
	}, cfg.JitterQueueSize, jitterWorkers)
	c.SetGarlicHandler(g.handleIncoming)
	go g.cleanupLoop()
	return g
}

// Close unregisters from core.Core and stops the background cleanup
// loop and the jitter scheduler. It does not close the underlying
// core.Core.
func (g *Garlic) Close() {
	g.core.SetGarlicHandler(nil)
	g.scheduler.Stop()
	close(g.stop)
}

// sendCircuitData transmits a circuitData wire message to addr, applying
// Config.JitterEnabled's random delay if configured. A jitter computation
// or scheduling failure falls back to sending immediately, so a
// misconfiguration degrades to unjittered delivery rather than dropping
// an otherwise-valid packet.
func (g *Garlic) sendCircuitData(msg []byte, addr net.Addr) {
	var delay time.Duration
	if g.cfg.JitterEnabled {
		if d, err := randomJitter(g.cfg.MinJitter, g.cfg.MaxJitter); err == nil {
			delay = d
		}
	}
	if !g.scheduler.enqueue(msg, addr, delay) {
		_, _ = g.core.WriteGarlic(msg, addr)
	}
}

func (g *Garlic) cleanupLoop() {
	t := time.NewTicker(30 * time.Second)
	defer t.Stop()
	gossip := time.NewTicker(max(g.cfg.GossipInterval, time.Second))
	defer gossip.Stop()
	for {
		select {
		case <-t.C:
			g.circuits.ExpireStale()
			g.relayState.expireStale(2 * g.cfg.CircuitLifetime)
			g.limiter.Cleanup(time.Hour)
		case <-gossip.C:
			g.gossipTick()
		case <-g.stop:
			return
		}
	}
}

// gossipTick sends this node's known-peer sample to a few
// already-capability-verified peers (from capabilityCache, i.e. peers
// this node has itself confirmed answer garlic-v2 - never an unverified
// discovery candidate), so discovery propagates without needing a
// distributed directory.
func (g *Garlic) gossipTick() {
	g.mu.Lock()
	targets := make([]string, 0, len(g.capabilityCache))
	for key := range g.capabilityCache {
		targets = append(targets, key)
	}
	g.mu.Unlock()

	if len(targets) > g.cfg.GossipFanout {
		targets = targets[:g.cfg.GossipFanout]
	}
	for _, hexKey := range targets {
		peerKey, err := hex.DecodeString(hexKey)
		if err != nil {
			continue
		}
		_ = g.GossipAnnounce(peerKey)
	}
}

// GossipAnnounce sends to as a sample of this node's known Garlic peers
// (Config.GossipSampleSize of them), so it can discover peers it hasn't
// directly queried itself. Intended to be called with an already
// capability-verified peer, though nothing technically prevents calling
// it otherwise - an unverified recipient simply can't parse the message
// if it isn't running src/garlic, same as any other Garlic message type.
func (g *Garlic) GossipAnnounce(to ed25519.PublicKey) error {
	sample := g.discovery.sample(g.cfg.GossipSampleSize)
	peers := make([]AnnouncePeer, len(sample))
	for i, p := range sample {
		peers[i] = AnnouncePeer{NodeKey: p.NodeKey, GarlicPublicKey: p.GarlicPublicKey}
	}
	body, err := (&AnnounceMessage{Peers: peers}).Marshal()
	if err != nil {
		return err
	}
	msg := append([]byte{msgTypeAnnounce}, body...)
	_, err = g.core.WriteGarlic(msg, iwt.Addr(to))
	return err
}

// KnownPeers returns every Garlic peer this node currently knows about,
// whether learned directly (a successful capability query) or via
// gossip from another peer (msgTypeAnnounce) - candidates for circuit
// hop selection, not yet capability-verified by this node itself unless
// QueryCapability has also been called for that specific key.
func (g *Garlic) KnownPeers() []DiscoveredPeer {
	return g.discovery.list()
}

// candidatePool builds a HopCandidate for every known/discovered peer
// this node has a resolved mesh path to, annotated with hop count and
// tree parent (see SelectDiversePath). A peer with no resolved path yet
// is skipped rather than scored with a meaningless zero.
func (g *Garlic) candidatePool() []HopCandidate {
	tree := g.core.GetTree()
	parentOf := make(map[string][]byte, len(tree))
	for _, t := range tree {
		parentOf[string(t.Key)] = t.Parent
	}

	known := g.discovery.list()
	pool := make([]HopCandidate, 0, len(known))
	for _, p := range known {
		hops, ok := g.HopCount(p.NodeKey)
		if !ok {
			continue
		}
		pool = append(pool, HopCandidate{
			NodeKey:         p.NodeKey,
			GarlicPublicKey: p.GarlicPublicKey,
			HopCount:        hops,
			TreeParent:      parentOf[string(p.NodeKey)],
		})
	}
	return pool
}

// SelectPath chooses n topologically diverse circuit hops from this
// node's known/discovered Garlic peers (see SelectDiversePath and
// Config.MinHopCount). The result still needs each hop's capability
// re-verified (e.g. via QueryCapability) before CreateCircuit, in case a
// discovered/gossiped entry has gone stale.
func (g *Garlic) SelectPath(n int) ([]HopCandidate, error) {
	return SelectDiversePath(g.candidatePool(), n, g.cfg.MinHopCount)
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
		g.dispatchAction(g.processCircuitData(data[1:]))
	case msgTypeAnnounce:
		g.processAnnounce(data[1:])
	case msgTypeCircuitDataBundle:
		for _, action := range g.processCircuitDataBundle(data[1:]) {
			g.dispatchAction(action)
		}
	}
}

// dispatchAction carries out a single circuitAction: deliver locally, or
// forward to the next hop. actionDrop is a no-op (nothing to do).
func (g *Garlic) dispatchAction(action circuitAction) {
	switch action.kind {
	case actionDeliver:
		select {
		case g.delivered <- DeliveredMessage{CircuitID: action.circuitID, Payload: action.payload}:
		default:
		}
	case actionForward:
		g.sendCircuitData(action.forwardMsg, iwt.Addr(action.forwardTo))
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

	// A successful, self-reported garlic-v2 response is exactly the
	// verification discovery candidates need before they're worth
	// remembering - see discovery.go's doc comment.
	if msg.SupportsGarlicV2() && len(msg.PublicKey) > 0 {
		g.discovery.record(DiscoveredPeer{NodeKey: append([]byte(nil), from...), GarlicPublicKey: msg.PublicKey})
	}

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
	g.mu.Unlock()
	return g.requestCapability(peer)
}

// PingCapability behaves like QueryCapability but always sends a fresh
// request - ignoring any cached result - and additionally reports the
// measured round-trip time. Intended for topology-aware hop selection
// (see HopCount), where a stale cached answer wouldn't reflect current
// latency. The result still updates the capability cache, same as
// QueryCapability.
func (g *Garlic) PingCapability(peer ed25519.PublicKey) (*CapabilityMessage, time.Duration, error) {
	start := time.Now()
	msg, err := g.requestCapability(peer)
	if err != nil {
		return nil, 0, err
	}
	return msg, time.Since(start), nil
}

func (g *Garlic) requestCapability(peer ed25519.PublicKey) (*CapabilityMessage, error) {
	key := hex.EncodeToString(peer)
	ch := make(chan *CapabilityMessage, 1)
	g.mu.Lock()
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

// HopCount returns the number of mesh hops to peer, if this node has a
// cached path to it (e.g. from a prior capability query or any other
// traffic exchanged with that key) - see core.Core.GetPaths. ok is false
// if no path is cached yet; querying capability first typically resolves
// one as a side effect of the round trip.
func (g *Garlic) HopCount(peer ed25519.PublicKey) (hops int, ok bool) {
	return hopCountFromPaths(g.core.GetPaths(), peer)
}

func hopCountFromPaths(paths []core.PathEntryInfo, peer ed25519.PublicKey) (int, bool) {
	for _, p := range paths {
		if bytes.Equal(p.Key, peer) {
			return len(p.Path), true
		}
	}
	return 0, false
}

// CreateCircuit builds and tracks a new circuit over path, an ordered
// list of hops the caller has already confirmed (e.g. via
// QueryCapability) are Garlic-capable. It returns the circuit's ID, used
// with SendGarlic and CloseCircuit.
func (g *Garlic) CreateCircuit(path []CapabilityMessage, nodeKeys [][]byte) (CircuitID, error) {
	if len(path) == 0 || len(path) != len(nodeKeys) {
		return CircuitID{}, ErrInvalidPath
	}

	ephemeralPubs := make([][]byte, len(path))
	ephemeralPrivs := make([][]byte, len(path))
	for i := range path {
		pub, priv, err := GenerateKeypair()
		if err != nil {
			return CircuitID{}, err
		}
		ephemeralPubs[i], ephemeralPrivs[i] = pub, priv
	}

	hops := make([]Hop, len(path))
	for i := range path {
		secret, err := ECDH(ephemeralPrivs[i], path[i].PublicKey)
		if err != nil {
			return CircuitID{}, err
		}
		key, err := deriveLayerKey(secret)
		if err != nil {
			return CircuitID{}, err
		}
		var nextEphemeral []byte
		if i+1 < len(path) {
			nextEphemeral = ephemeralPubs[i+1]
		}
		hops[i] = Hop{NodeKey: nodeKeys[i], Key: key, NextEphemeralPub: nextEphemeral}
	}

	c, err := g.circuits.Add(hops, g.cfg.CircuitLifetime, g.cfg.MaxPacketsPerCircuit, g.cfg.MaxBytesPerCircuit)
	if err != nil {
		return CircuitID{}, err
	}

	g.mu.Lock()
	g.originEphemeral[c.ID] = ephemeralPubs[0]
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

// CreateCircuitPool builds len(paths) independent circuits (paths[i]/
// nodeKeys[i] passed to CreateCircuit exactly as if called separately -
// they need not share any hops) and groups them under one PoolID for
// SendGarlicMultipath. If any path fails to build, every circuit already
// created for this pool is closed and the error is returned - a pool is
// all-or-nothing, never partially built.
func (g *Garlic) CreateCircuitPool(paths [][]CapabilityMessage, nodeKeys [][][]byte) (PoolID, error) {
	if len(paths) == 0 || len(paths) != len(nodeKeys) {
		return 0, ErrEmptyPool
	}
	circuits := make([]CircuitID, 0, len(paths))
	for i := range paths {
		id, err := g.CreateCircuit(paths[i], nodeKeys[i])
		if err != nil {
			for _, c := range circuits {
				g.CloseCircuit(c)
			}
			return 0, err
		}
		circuits = append(circuits, id)
	}

	poolID, err := randomPoolID()
	if err != nil {
		for _, c := range circuits {
			g.CloseCircuit(c)
		}
		return 0, err
	}
	g.mu.Lock()
	g.pools[poolID] = newCircuitPool(circuits)
	g.mu.Unlock()
	return poolID, nil
}

// ClosePool closes every circuit in pool and stops tracking it.
func (g *Garlic) ClosePool(pool PoolID) {
	g.mu.Lock()
	p, ok := g.pools[pool]
	delete(g.pools, pool)
	g.mu.Unlock()
	if !ok {
		return
	}
	for _, id := range p.all() {
		g.CloseCircuit(id)
	}
}

// SendGarlicMultipath sends payload over the next circuit in pool
// (round-robin), so consecutive calls spread traffic across every path
// in the pool rather than concentrating it on one - see multipath.go's
// doc comment for why this matters against a Sybil or traffic-
// correlation adversary who doesn't control every path.
func (g *Garlic) SendGarlicMultipath(pool PoolID, payload []byte) error {
	g.mu.Lock()
	p, ok := g.pools[pool]
	g.mu.Unlock()
	if !ok {
		return ErrPoolNotFound
	}
	id, ok := p.nextCircuit()
	if !ok {
		return ErrPoolNotFound
	}
	return g.SendGarlic(id, payload)
}

// SendGarlic seals payload as one packet over the circuit id (previously
// created with CreateCircuit) and hands it to the jitter scheduler for
// transmission. A returned nil error means the packet was successfully
// sealed and queued (or sent immediately, if Config.JitterEnabled is
// false) - not that the first hop has received it, since
// Config.JitterEnabled delays the actual send.
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

	g.sendCircuitData(msg, iwt.Addr(firstHop))
	return nil
}

// SendGarlicMaxCoverMessages bounds the coverCount parameter to
// SendGarlicBundled, leaving room in the same MaxBundleMessages limit
// Bundle.Marshal itself enforces (see bundle.go) for the one real
// message every call also includes.
const SendGarlicMaxCoverMessages = MaxBundleMessages - 1

// SendGarlicBundled behaves like SendGarlic, but sends the real
// circuitData alongside coverCount cover entries (random bytes, sized
// like a real entry) in one Bundle - see processCircuitDataBundle's doc
// comment for why an observer, or even the receiving hop itself before
// it attempts decryption, cannot tell which bundled entry (if any) is
// real. coverCount is clamped to SendGarlicMaxCoverMessages.
func (g *Garlic) SendGarlicBundled(id CircuitID, payload []byte, coverCount int) error {
	if coverCount > SendGarlicMaxCoverMessages {
		coverCount = SendGarlicMaxCoverMessages
	}
	if coverCount < 0 {
		coverCount = 0
	}

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
	realEntry, err := buildCircuitDataBody(ephemeralPub, id, counter, expiration, onion, g.cfg)
	if err != nil {
		return err
	}

	bundle := &Bundle{Messages: [][]byte{realEntry}}
	for range coverCount {
		if err := bundle.AddCoverMessage(len(realEntry)); err != nil {
			break // a full/oversized bundle still sends the real entry alone
		}
	}
	if err := shuffleBundleMessages(bundle); err != nil {
		return err
	}
	body, err := bundle.Marshal()
	if err != nil {
		return err
	}

	g.sendCircuitData(append([]byte{msgTypeCircuitDataBundle}, body...), iwt.Addr(firstHop))
	return nil
}

// buildCircuitDataMessage assembles the wire message for one circuitData
// packet: msgTypeCircuitData || ephemeralPub || Envelope. It performs no
// I/O, so it's testable without a running core.Core - see protocol.go's
// doc comment on why the pure/I/O split matters here. If cfg.PaddingEnabled,
// the envelope's wire size is independently re-randomized per call (see
// Envelope.PadToRandomRange); a padding failure (e.g. misconfigured
// Min/MaxPaddedSize) degrades to unpadded rather than failing the send.
func buildCircuitDataMessage(ephemeralPub []byte, id CircuitID, counter, expiration uint64, onion []byte, cfg Config) ([]byte, error) {
	body, err := buildCircuitDataBody(ephemeralPub, id, counter, expiration, onion, cfg)
	if err != nil {
		return nil, err
	}
	return append([]byte{msgTypeCircuitData}, body...), nil
}

// buildCircuitDataBody builds a circuitData message body - ephemeralPub
// || Envelope - without the leading msgTypeCircuitData byte. This is the
// exact shape processCircuitData (and, by extension, a Bundle entry in a
// msgTypeCircuitDataBundle message - see processCircuitDataBundle)
// expects; buildCircuitDataMessage is this plus the type byte, for a
// standalone (non-bundled) send.
func buildCircuitDataBody(ephemeralPub []byte, id CircuitID, counter, expiration uint64, onion []byte, cfg Config) ([]byte, error) {
	env := &Envelope{
		Version:       EnvelopeVersion1,
		CircuitID:     id,
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
	body := make([]byte, 0, len(ephemeralPub)+len(envBytes))
	body = append(body, ephemeralPub...)
	body = append(body, envBytes...)
	return body, nil
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
