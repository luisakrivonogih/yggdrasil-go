package garlic

import (
	"bytes"
	"testing"
	"time"
)

// buildTestCircuitData constructs a circuitData message *body* - i.e.
// everything processCircuitData expects, which is the wire message with
// its leading msgTypeCircuitData byte already stripped, matching how
// handleIncoming calls it (data[1:]) - for a path of relayIdentities
// (each a *Identity), terminating in payload, so relay-logic tests can
// feed realistic input to processCircuitData without a real core.Core.
func buildTestCircuitData(t *testing.T, relayIdentities []*Identity, nodeKeys [][]byte, payload []byte, ttl time.Duration) (body []byte, circuitID CircuitID) {
	t.Helper()
	ephemeralPub, ephemeralPriv, err := GenerateKeypair()
	if err != nil {
		t.Fatalf("GenerateKeypair returned error: %v", err)
	}
	hops := make([]Hop, len(relayIdentities))
	for i, id := range relayIdentities {
		secret, err := ECDH(ephemeralPriv, id.PublicKey)
		if err != nil {
			t.Fatalf("ECDH returned error: %v", err)
		}
		key, err := DeriveKey(secret, nil, LabelLayerKey)
		if err != nil {
			t.Fatalf("DeriveKey returned error: %v", err)
		}
		hops[i] = Hop{NodeKey: nodeKeys[i], Key: key}
	}
	c, err := NewCircuit(hops, time.Minute, 100, 100000)
	if err != nil {
		t.Fatalf("NewCircuit returned error: %v", err)
	}
	onion, _, counter, err := c.Seal(payload)
	if err != nil {
		t.Fatalf("Seal returned error: %v", err)
	}
	env := &Envelope{
		Version:       EnvelopeVersion1,
		CircuitID:     uint64(c.ID),
		PacketCounter: counter,
		Expiration:    uint64(time.Now().Add(ttl).Unix()),
		Body:          onion,
	}
	envBytes, err := env.Marshal()
	if err != nil {
		t.Fatalf("Marshal returned error: %v", err)
	}
	body = append(append([]byte(nil), ephemeralPub...), envBytes...)
	return body, c.ID
}

// newTestGarlic returns a *Garlic with just enough state set up to
// exercise its pure relay-decision logic (processCircuitData,
// processCapabilityRequest) - no real core.Core involved. The full
// wiring to a running node is covered separately by the integration
// tests, which construct a *Garlic via New.
func newTestGarlic(t *testing.T) *Garlic {
	t.Helper()
	id, err := NewIdentity()
	if err != nil {
		t.Fatalf("NewIdentity returned error: %v", err)
	}
	return &Garlic{
		identity:   id,
		cfg:        DefaultConfig(),
		relayState: newRelayCircuitState(1024),
		delivered:  make(chan DeliveredMessage, 256),
	}
}

func TestProcessCircuitDataTerminalHopDelivers(t *testing.T) {
	g := newTestGarlic(t)
	payload := []byte("hello bob")
	msg, circuitID := buildTestCircuitData(t, []*Identity{g.identity}, [][]byte{g.identity.PublicKey}, payload, time.Minute)

	action := g.processCircuitData(msg)
	if action.kind != actionDeliver {
		t.Fatalf("action.kind = %v, want actionDeliver", action.kind)
	}
	if action.circuitID != circuitID {
		t.Fatalf("action.circuitID = %d, want %d", action.circuitID, circuitID)
	}
	if !bytes.Equal(action.payload, payload) {
		t.Fatalf("action.payload = %q, want %q", action.payload, payload)
	}
}

func TestProcessCircuitDataIntermediateHopForwards(t *testing.T) {
	relay := newTestGarlic(t)
	destID, err := NewIdentity()
	if err != nil {
		t.Fatalf("NewIdentity returned error: %v", err)
	}
	destNodeKey := []byte("dest-node-key")
	payload := []byte("hello bob")

	msg, _ := buildTestCircuitData(t,
		[]*Identity{relay.identity, destID},
		[][]byte{[]byte("relay-node-key"), destNodeKey},
		payload, time.Minute)

	action := relay.processCircuitData(msg)
	if action.kind != actionForward {
		t.Fatalf("action.kind = %v, want actionForward", action.kind)
	}
	if !bytes.Equal(action.forwardTo, destNodeKey) {
		t.Fatalf("action.forwardTo = %q, want %q", action.forwardTo, destNodeKey)
	}
	if len(action.forwardMsg) == 0 {
		t.Fatal("action.forwardMsg is empty")
	}

	// The forwarded message must itself be a valid circuitData message
	// that the destination can process to completion (proves the relay
	// correctly reconstructed the next envelope, not just that it didn't
	// crash).
	final := destID
	finalGarlic := &Garlic{identity: final, relayState: newRelayCircuitState(1024)}
	finalAction := finalGarlic.processCircuitData(action.forwardMsg[1:]) // strip the msgTypeCircuitData prefix, as handleIncoming would
	if finalAction.kind != actionDeliver {
		t.Fatalf("final hop action.kind = %v, want actionDeliver", finalAction.kind)
	}
	if !bytes.Equal(finalAction.payload, payload) {
		t.Fatalf("final hop payload = %q, want %q", finalAction.payload, payload)
	}
}

func TestProcessCircuitDataDropsWrongRecipient(t *testing.T) {
	g := newTestGarlic(t)
	other, err := NewIdentity()
	if err != nil {
		t.Fatalf("NewIdentity returned error: %v", err)
	}
	msg, _ := buildTestCircuitData(t, []*Identity{other}, [][]byte{[]byte("someone-else")}, []byte("payload"), time.Minute)

	action := g.processCircuitData(msg)
	if action.kind != actionDrop {
		t.Fatalf("action.kind = %v, want actionDrop (message encrypted for a different identity)", action.kind)
	}
}

func TestProcessCircuitDataDropsReplay(t *testing.T) {
	g := newTestGarlic(t)
	msg, _ := buildTestCircuitData(t, []*Identity{g.identity}, [][]byte{g.identity.PublicKey}, []byte("payload"), time.Minute)

	first := g.processCircuitData(msg)
	if first.kind != actionDeliver {
		t.Fatalf("first action.kind = %v, want actionDeliver", first.kind)
	}
	second := g.processCircuitData(msg)
	if second.kind != actionDrop {
		t.Fatalf("second (replayed) action.kind = %v, want actionDrop", second.kind)
	}
}

func TestProcessCircuitDataDropsExpired(t *testing.T) {
	g := newTestGarlic(t)
	msg, _ := buildTestCircuitData(t, []*Identity{g.identity}, [][]byte{g.identity.PublicKey}, []byte("payload"), -time.Minute)

	action := g.processCircuitData(msg)
	if action.kind != actionDrop {
		t.Fatalf("action.kind = %v, want actionDrop (expired)", action.kind)
	}
}

func TestProcessCircuitDataDropsMalformedTooShort(t *testing.T) {
	g := newTestGarlic(t)
	action := g.processCircuitData([]byte{1, 2, 3})
	if action.kind != actionDrop {
		t.Fatalf("action.kind = %v, want actionDrop (too short to contain an ephemeral key)", action.kind)
	}
}

func TestProcessCircuitDataDropsMalformedEnvelope(t *testing.T) {
	g := newTestGarlic(t)
	junk := make([]byte, KeySize+10)
	action := g.processCircuitData(junk)
	if action.kind != actionDrop {
		t.Fatalf("action.kind = %v, want actionDrop (malformed envelope)", action.kind)
	}
}

func TestProcessCircuitDataDropsWhenRelayTableFull(t *testing.T) {
	g := newTestGarlic(t)
	g.relayState = newRelayCircuitState(0) // no room for any circuit
	msg, _ := buildTestCircuitData(t, []*Identity{g.identity}, [][]byte{g.identity.PublicKey}, []byte("payload"), time.Minute)

	action := g.processCircuitData(msg)
	if action.kind != actionDrop {
		t.Fatalf("action.kind = %v, want actionDrop (relay circuit table full)", action.kind)
	}
}

func TestProcessCapabilityRequestAdvertisesGarlicV1(t *testing.T) {
	g := newTestGarlic(t)
	resp := g.processCapabilityRequest()
	msg, err := UnmarshalCapabilityMessage(resp)
	if err != nil {
		t.Fatalf("UnmarshalCapabilityMessage returned error: %v", err)
	}
	if !msg.SupportsGarlicV1() {
		t.Error("response does not advertise garlic-v1")
	}
	if !bytes.Equal(msg.PublicKey, g.identity.PublicKey) {
		t.Errorf("response PublicKey = %x, want %x", msg.PublicKey, g.identity.PublicKey)
	}
}
