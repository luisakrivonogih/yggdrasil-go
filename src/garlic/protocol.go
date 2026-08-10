package garlic

// In-band Garlic message types (Phase 6/7 of the roadmap): every
// typeSessionGarlic-tagged packet's payload starts with one of these
// bytes. This is entirely internal to src/garlic - core.Core never
// inspects it, it just delivers the opaque payload to whoever registered
// via SetGarlicHandler (see docs/garlic-architecture.md §3.3).
//
// This file holds the *pure* decision logic: given already-received
// bytes and this node's own state, decide what to do next (deliver,
// forward, or drop) without performing any I/O itself. manager.go is the
// thin wrapper that actually calls core.Core.WriteGarlic with the
// results. Separating the two makes the security-relevant logic - replay
// checks, expiry, decrypt-failure handling - testable without a running
// mesh.

import (
	"errors"
	"time"
)

const (
	msgTypeCapabilityRequest byte = iota + 1
	msgTypeCapabilityResponse
	msgTypeCircuitData
	msgTypeAnnounce
	msgTypeCircuitDataBundle
)

// circuitDataMinSize is the minimum length of a circuitData message body
// (after the type byte): an ephemeral public key plus at least an empty
// Envelope's fixed header.
const circuitDataMinSize = KeySize + envelopeFixedHeaderSize

var (
	ErrNotForThisIdentity = errors.New("garlic: message not encrypted for this identity")
	ErrPacketExpired      = errors.New("garlic: packet expired")
	ErrReplayed           = errors.New("garlic: packet replayed or circuit table full")
)

type actionKind int

const (
	actionDrop actionKind = iota
	actionDeliver
	actionForward
)

// circuitAction is the outcome of processing one circuitData message:
// either nothing further to do (actionDrop - never explained further, see
// docs/garlic-architecture.md §17 on not leaking which check failed),
// deliver payload locally (this node is the circuit's final hop), or
// forward forwardMsg to forwardTo (this node is an intermediate hop).
type circuitAction struct {
	kind       actionKind
	circuitID  CircuitID
	payload    []byte
	forwardTo  []byte
	forwardMsg []byte
}

// processCircuitData decides what to do with the body of a
// msgTypeCircuitData message (i.e. everything after that leading type
// byte). It performs no I/O.
func (g *Garlic) processCircuitData(body []byte) circuitAction {
	if len(body) < circuitDataMinSize {
		g.security.malformedPackets.Add(1)
		return circuitAction{kind: actionDrop}
	}
	ephemeralPub := body[:KeySize]
	env, err := Unmarshal(body[KeySize:])
	if err != nil {
		g.security.malformedPackets.Add(1)
		return circuitAction{kind: actionDrop}
	}
	if env.Version != EnvelopeVersion1 {
		g.security.malformedPackets.Add(1)
		return circuitAction{kind: actionDrop}
	}
	if time.Now().Unix() > int64(env.Expiration) {
		g.security.expiredPackets.Add(1)
		return circuitAction{kind: actionDrop}
	}

	circuitID := CircuitID(env.CircuitID)
	window, ok := g.relayState.replayWindowFor(circuitID)
	if !ok {
		g.security.relayTableFull.Add(1)
		return circuitAction{kind: actionDrop}
	}
	if !window.CheckAndSet(env.PacketCounter) {
		g.security.replayDrops.Add(1)
		return circuitAction{kind: actionDrop}
	}

	secret, err := ECDH(g.identity.PrivateKey, ephemeralPub)
	if err != nil {
		g.security.authFailures.Add(1)
		return circuitAction{kind: actionDrop}
	}
	key, err := DeriveKey(secret, nil, LabelLayerKey)
	if err != nil {
		g.security.authFailures.Add(1)
		return circuitAction{kind: actionDrop}
	}

	layer, err := DecryptLayer(key, env.PacketCounter, env.Body)
	if err != nil {
		// Wrong key (message wasn't encrypted for us), tampered
		// ciphertext, or malformed plaintext all look identical here by
		// design - see ErrNotForThisIdentity's doc comment.
		g.security.authFailures.Add(1)
		return circuitAction{kind: actionDrop}
	}

	if len(layer.NextHop) == 0 {
		return circuitAction{kind: actionDeliver, circuitID: circuitID, payload: layer.Inner}
	}

	nextEnv := &Envelope{
		Version:       EnvelopeVersion1,
		CircuitID:     env.CircuitID,
		PacketCounter: env.PacketCounter,
		Expiration:    env.Expiration,
		Body:          layer.Inner,
	}
	// Independently re-randomize this hop's outgoing wire size (see
	// Config.PaddingEnabled's doc comment) - a config error here (e.g.
	// MaxPaddedSize too small for this body) degrades to unpadded
	// forwarding rather than dropping an otherwise-valid packet.
	if g.cfg.PaddingEnabled {
		_ = nextEnv.PadToRandomRange(g.cfg.MinPaddedSize, g.cfg.MaxPaddedSize)
	}
	nextBytes, err := nextEnv.Marshal()
	if err != nil {
		g.security.malformedPackets.Add(1)
		return circuitAction{kind: actionDrop}
	}
	forwardMsg := make([]byte, 0, 1+KeySize+len(nextBytes))
	forwardMsg = append(forwardMsg, msgTypeCircuitData)
	forwardMsg = append(forwardMsg, ephemeralPub...)
	forwardMsg = append(forwardMsg, nextBytes...)

	return circuitAction{kind: actionForward, circuitID: circuitID, forwardTo: layer.NextHop, forwardMsg: forwardMsg}
}

// processAnnounce parses body and records every valid peer entry into
// this node's discovery registry, seeding future circuit-hop candidates
// this node has never directly queried. It performs no network I/O.
// Malformed input, or an entry with an empty key, is silently skipped -
// there is no response to send on an unauthenticated gossip channel (see
// docs/garlic-architecture.md §17), and this is best-effort discovery,
// not a trust decision (every candidate is still capability-verified
// before it's ever used as a circuit hop).
func (g *Garlic) processAnnounce(body []byte) {
	msg, err := UnmarshalAnnounceMessage(body)
	if err != nil {
		return
	}
	for _, p := range msg.Peers {
		if len(p.NodeKey) == 0 || len(p.GarlicPublicKey) == 0 {
			continue
		}
		g.discovery.record(DiscoveredPeer{NodeKey: p.NodeKey, GarlicPublicKey: p.GarlicPublicKey})
	}
}

// processCircuitDataBundle decides what to do with the body of a
// msgTypeCircuitDataBundle message: a Bundle whose entries are each
// shaped like a circuitData body (ephemeralPub || Envelope). Every entry
// is run through the exact same processCircuitData used for a
// non-bundled message - no new cryptography, no weaker guarantees - so
// a cover entry (random bytes, indistinguishable in shape from a real
// one) simply fails to decrypt and drops silently, exactly as a
// corrupted or misdirected message already does. This is what makes a
// bundle a real "garlic" rather than a single onion stream: an observer
// who can't decrypt any entry has no way to tell how many of them, if
// any, are real. See docs/garlic-protocol.md §7 and Bundle's own doc
// comment. Returns every non-drop action found, in bundle order; a
// caller acts on each independently (deliver locally, or forward - to
// potentially different next hops, since bundled entries need not
// belong to the same circuit).
func (g *Garlic) processCircuitDataBundle(body []byte) []circuitAction {
	bundle, err := UnmarshalBundle(body)
	if err != nil {
		return nil
	}
	var actions []circuitAction
	for _, sub := range bundle.Messages {
		if action := g.processCircuitData(sub); action.kind != actionDrop {
			actions = append(actions, action)
		}
	}
	return actions
}

// processCapabilityRequest returns the marshaled CapabilityMessage this
// node advertises in response to a capability request. It performs no I/O.
func (g *Garlic) processCapabilityRequest() []byte {
	msg := &CapabilityMessage{
		Versions:  []string{CapabilityGarlicV1},
		PublicKey: g.identity.PublicKey,
	}
	// A fixed, well-formed message built from this node's own identity
	// can't fail to marshal (both fields are always within bounds), so a
	// marshal error here would indicate a bug rather than bad input.
	payload, err := msg.Marshal()
	if err != nil {
		panic("garlic: failed to marshal own capability message: " + err.Error())
	}
	return payload
}
