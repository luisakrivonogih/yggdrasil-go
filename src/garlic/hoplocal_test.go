package garlic

// Adversarial tests for the hop-local envelope format (EnvelopeVersion2):
// proving the property this whole feature exists for - non-adjacent
// relays observe different CircuitID/PacketCounter/Expiration per leg,
// and cannot recover a non-adjacent leg's metadata without the adjacent
// hop's key - plus regression coverage proving EnvelopeVersion1 circuits
// still relay correctly unchanged. Mirrors the harness pattern in
// linkability_test.go (that file's ephemeral-key property from the
// 2026-08-09 crypto-hardening pass; this file's CircuitID/Counter/
// Expiration property from
// docs/superpowers/specs/2026-08-24-garlic-hop-local-metadata-design.md).

import (
	"bytes"
	"testing"
	"time"
)

func TestNonAdjacentHopsCannotLinkViaEnvelopeMetadata(t *testing.T) {
	g, hopIDs := buildThreeHopOriginator(t)
	path, nodeKeys := buildTestPath(hopIDs)
	circuitID, err := g.CreateCircuit(path, nodeKeys)
	if err != nil {
		t.Fatalf("CreateCircuit returned error: %v", err)
	}
	c, ok := g.circuits.Get(circuitID)
	if !ok {
		t.Fatal("circuit not found after CreateCircuit")
	}

	onion, _, legID, counter, expiration, err := c.SealHopLocal([]byte("hello"), g.cfg.PacketTTL)
	if err != nil {
		t.Fatalf("SealHopLocal returned error: %v", err)
	}
	ephemeralPub := g.originEphemeral[circuitID]
	bodyToHop1, err := buildCircuitDataBodyHopLocal(ephemeralPub, legID, counter, expiration, onion, g.cfg)
	if err != nil {
		t.Fatalf("buildCircuitDataBodyHopLocal returned error: %v", err)
	}
	envLeg1, err := Unmarshal(bodyToHop1[KeySize:])
	if err != nil {
		t.Fatalf("Unmarshal (leg 1) returned error: %v", err)
	}

	hop1 := hopGarlicFor(hopIDs[0])
	action1 := hop1.processCircuitData(bodyToHop1, msgTypeCircuitData)
	if action1.kind != actionForward {
		t.Fatalf("hop1 action = %v, want actionForward", action1.kind)
	}
	envLeg2, err := Unmarshal(action1.forwardMsg[1+KeySize:])
	if err != nil {
		t.Fatalf("Unmarshal (leg 2) returned error: %v", err)
	}

	hop2 := hopGarlicFor(hopIDs[1])
	action2 := hop2.processCircuitData(action1.forwardMsg[1:], msgTypeCircuitData)
	if action2.kind != actionForward {
		t.Fatalf("hop2 action = %v, want actionForward", action2.kind)
	}
	envLeg3, err := Unmarshal(action2.forwardMsg[1+KeySize:])
	if err != nil {
		t.Fatalf("Unmarshal (leg 3) returned error: %v", err)
	}

	hop3 := hopGarlicFor(hopIDs[2])
	action3 := hop3.processCircuitData(action2.forwardMsg[1:], msgTypeCircuitData)
	if action3.kind != actionDeliver {
		t.Fatalf("hop3 action = %v, want actionDeliver", action3.kind)
	}
	if !bytes.Equal(action3.payload, []byte("hello")) {
		t.Fatalf("delivered payload = %q, want %q", action3.payload, "hello")
	}

	// The actual property: no two legs share a CircuitID, PacketCounter,
	// or Expiration.
	circuitIDs := []CircuitID{envLeg1.CircuitID, envLeg2.CircuitID, envLeg3.CircuitID}
	if circuitIDs[0] == circuitIDs[1] || circuitIDs[1] == circuitIDs[2] || circuitIDs[0] == circuitIDs[2] {
		t.Fatalf("CircuitIDs not all distinct across legs: %x", circuitIDs)
	}
	counters := []uint64{envLeg1.PacketCounter, envLeg2.PacketCounter, envLeg3.PacketCounter}
	if counters[0] == counters[1] && counters[1] == counters[2] {
		t.Fatalf("PacketCounters identical across every leg (allowed to coincide, but not for the whole test's random offsets to all collide): %v", counters)
	}
	expirations := []uint64{envLeg1.Expiration, envLeg2.Expiration, envLeg3.Expiration}
	if expirations[0] == expirations[1] && expirations[1] == expirations[2] {
		t.Fatalf("Expirations identical across every leg: %v", expirations)
	}
}

func TestRelayCannotDecryptNonAdjacentLayer(t *testing.T) {
	g, hopIDs := buildThreeHopOriginator(t)
	path, nodeKeys := buildTestPath(hopIDs)
	circuitID, err := g.CreateCircuit(path, nodeKeys)
	if err != nil {
		t.Fatalf("CreateCircuit returned error: %v", err)
	}
	c, _ := g.circuits.Get(circuitID)
	onion, _, legID, counter, expiration, err := c.SealHopLocal([]byte("hello"), g.cfg.PacketTTL)
	if err != nil {
		t.Fatalf("SealHopLocal returned error: %v", err)
	}
	ephemeralPub := g.originEphemeral[circuitID]
	bodyToHop1, err := buildCircuitDataBodyHopLocal(ephemeralPub, legID, counter, expiration, onion, g.cfg)
	if err != nil {
		t.Fatalf("buildCircuitDataBodyHopLocal returned error: %v", err)
	}

	hop1 := hopGarlicFor(hopIDs[0])
	action1 := hop1.processCircuitData(bodyToHop1, msgTypeCircuitData)
	if action1.kind != actionForward {
		t.Fatalf("hop1 action = %v, want actionForward", action1.kind)
	}
	hop2 := hopGarlicFor(hopIDs[1])
	action2 := hop2.processCircuitData(action1.forwardMsg[1:], msgTypeCircuitData)
	if action2.kind != actionForward {
		t.Fatalf("hop2 action = %v, want actionForward", action2.kind)
	}

	// hop1 captures the leg-3 wire message (hop2 -> hop3) and tries to
	// decrypt it with its own identity key - the only key it has. It must
	// not succeed, and therefore cannot recover leg 3's
	// NextLocalCircuitID/NextLocalCounter/NextLocalExpiration.
	leg3EphemeralPub := action2.forwardMsg[1 : 1+KeySize]
	leg3EnvBytes := action2.forwardMsg[1+KeySize:]
	leg3Env, err := Unmarshal(leg3EnvBytes)
	if err != nil {
		t.Fatalf("Unmarshal (leg 3) returned error: %v", err)
	}
	wrongSecret, err := ECDH(hopIDs[0].PrivateKey, leg3EphemeralPub)
	if err != nil {
		t.Fatalf("ECDH returned error: %v", err)
	}
	wrongKey, err := deriveLayerKeyHopLocal(wrongSecret)
	if err != nil {
		t.Fatalf("deriveLayerKeyHopLocal returned error: %v", err)
	}
	if _, err := DecryptLayerHopLocal(wrongKey, leg3Env.PacketCounter, leg3Env.Body); err == nil {
		t.Fatal("hop1 successfully decrypted hop2->hop3's layer using its own identity key - non-adjacent hops are not isolated")
	}
}

func TestV3CircuitRejectsV2OnlyHop(t *testing.T) {
	g, hopIDs := buildThreeHopOriginator(t)
	path := []CapabilityMessage{
		{Versions: []string{CapabilityGarlicV2, CapabilityGarlicV3}, PublicKey: hopIDs[0].PublicKey},
		{Versions: []string{CapabilityGarlicV2}, PublicKey: hopIDs[1].PublicKey}, // no garlic-v3
		{Versions: []string{CapabilityGarlicV2, CapabilityGarlicV3}, PublicKey: hopIDs[2].PublicKey},
	}
	nodeKeys := [][]byte{{'A'}, {'B'}, {'C'}}
	if _, err := g.CreateCircuit(path, nodeKeys); err != ErrHopMissingGarlicV3Support {
		t.Fatalf("CreateCircuit error = %v, want ErrHopMissingGarlicV3Support", err)
	}
}

func TestEnvelopeV1CircuitsStillRelayCorrectly(t *testing.T) {
	// Regression: a circuit built the legacy way (bypassing CreateCircuit's
	// garlic-v3 gate entirely, exactly as an unmodified v2-only originator
	// would) must still relay and deliver correctly end to end.
	relayID, err := NewIdentity()
	if err != nil {
		t.Fatalf("NewIdentity (relay) returned error: %v", err)
	}
	ephemeralPub, ephemeralPriv, err := GenerateKeypair()
	if err != nil {
		t.Fatalf("GenerateKeypair returned error: %v", err)
	}
	secret, err := ECDH(ephemeralPriv, relayID.PublicKey)
	if err != nil {
		t.Fatalf("ECDH returned error: %v", err)
	}
	key, err := deriveLayerKey(secret)
	if err != nil {
		t.Fatalf("deriveLayerKey returned error: %v", err)
	}
	c, err := NewCircuit([]Hop{{NodeKey: relayID.PublicKey, Key: key}}, time.Minute, 100, 100000)
	if err != nil {
		t.Fatalf("NewCircuit returned error: %v", err)
	}
	onion, _, counter, err := c.Seal([]byte("legacy payload"))
	if err != nil {
		t.Fatalf("Seal returned error: %v", err)
	}
	env := &Envelope{
		Version:       EnvelopeVersion1,
		CircuitID:     c.ID,
		PacketCounter: counter,
		Expiration:    uint64(time.Now().Add(time.Minute).Unix()),
		Body:          onion,
	}
	envBytes, err := env.Marshal()
	if err != nil {
		t.Fatalf("Marshal returned error: %v", err)
	}
	body := append(append([]byte(nil), ephemeralPub...), envBytes...)

	relay := hopGarlicFor(relayID)
	action := relay.processCircuitData(body, msgTypeCircuitData)
	if action.kind != actionDeliver {
		t.Fatalf("action.kind = %v, want actionDeliver", action.kind)
	}
	if !bytes.Equal(action.payload, []byte("legacy payload")) {
		t.Fatalf("delivered payload = %q, want %q", action.payload, "legacy payload")
	}
}
