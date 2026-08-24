package garlic

import (
	"bytes"
	"testing"
)

func TestEncryptLayerDecryptLayerRoundTripWithNextHop(t *testing.T) {
	key, _ := DeriveKey([]byte("hop secret"), nil, LabelCircuitDataSend)
	layer := &LayerPlaintext{
		NextHop: []byte("next-hop-node-key-bytes"),
		Inner:   []byte("inner ciphertext to forward"),
	}

	ct, err := EncryptLayer(key, 1, layer)
	if err != nil {
		t.Fatalf("EncryptLayer returned error: %v", err)
	}
	got, err := DecryptLayer(key, 1, ct)
	if err != nil {
		t.Fatalf("DecryptLayer returned error: %v", err)
	}
	if !bytes.Equal(got.NextHop, layer.NextHop) {
		t.Errorf("NextHop = %q, want %q", got.NextHop, layer.NextHop)
	}
	if !bytes.Equal(got.Inner, layer.Inner) {
		t.Errorf("Inner = %q, want %q", got.Inner, layer.Inner)
	}
}

func TestEncryptLayerDecryptLayerRoundTripTerminal(t *testing.T) {
	key, _ := DeriveKey([]byte("hop secret"), nil, LabelCircuitDataSend)
	layer := &LayerPlaintext{
		NextHop: nil,
		Inner:   []byte("final delivered payload"),
	}

	ct, err := EncryptLayer(key, 7, layer)
	if err != nil {
		t.Fatalf("EncryptLayer returned error: %v", err)
	}
	got, err := DecryptLayer(key, 7, ct)
	if err != nil {
		t.Fatalf("DecryptLayer returned error: %v", err)
	}
	if len(got.NextHop) != 0 {
		t.Errorf("NextHop = %q, want empty (terminal hop)", got.NextHop)
	}
	if !bytes.Equal(got.Inner, layer.Inner) {
		t.Errorf("Inner = %q, want %q", got.Inner, layer.Inner)
	}
}

func TestDecryptLayerRejectsWrongKey(t *testing.T) {
	keyA, _ := DeriveKey([]byte("secret A"), nil, LabelCircuitDataSend)
	keyB, _ := DeriveKey([]byte("secret B"), nil, LabelCircuitDataSend)
	layer := &LayerPlaintext{Inner: []byte("payload")}

	ct, err := EncryptLayer(keyA, 1, layer)
	if err != nil {
		t.Fatalf("EncryptLayer returned error: %v", err)
	}
	if _, err := DecryptLayer(keyB, 1, ct); err == nil {
		t.Fatal("expected error decrypting layer with the wrong key, got nil")
	}
}

func TestDecryptLayerRejectsTamperedCiphertext(t *testing.T) {
	key, _ := DeriveKey([]byte("secret"), nil, LabelCircuitDataSend)
	layer := &LayerPlaintext{Inner: []byte("payload")}

	ct, err := EncryptLayer(key, 1, layer)
	if err != nil {
		t.Fatalf("EncryptLayer returned error: %v", err)
	}
	ct[len(ct)-1] ^= 0xFF

	if _, err := DecryptLayer(key, 1, ct); err == nil {
		t.Fatal("expected error decrypting tampered layer ciphertext, got nil")
	}
}

func TestDecryptLayerRejectsMalformedPlaintext(t *testing.T) {
	key, _ := DeriveKey([]byte("secret"), nil, LabelCircuitDataSend)
	// A validly-authenticated ciphertext whose plaintext is not a valid
	// LayerPlaintext encoding (too short to contain the length prefixes).
	ct, err := Seal(key, 1, []byte{0, 0}, nil)
	if err != nil {
		t.Fatalf("Seal returned error: %v", err)
	}
	if _, err := DecryptLayer(key, 1, ct); err == nil {
		t.Fatal("expected error decrypting malformed layer plaintext, got nil")
	}
}

func TestLayerPlaintextRoundTripsNextHopEphemeral(t *testing.T) {
	key, _ := DeriveKey([]byte("hop secret"), nil, LabelCircuitDataSend)
	nextEphemeral := bytes.Repeat([]byte{0xAB}, KeySize)
	layer := &LayerPlaintext{
		NextHop:          []byte("next-hop-node-key-bytes"),
		NextHopEphemeral: nextEphemeral,
		Inner:            []byte("inner ciphertext to forward"),
	}

	ct, err := EncryptLayer(key, 1, layer)
	if err != nil {
		t.Fatalf("EncryptLayer returned error: %v", err)
	}
	got, err := DecryptLayer(key, 1, ct)
	if err != nil {
		t.Fatalf("DecryptLayer returned error: %v", err)
	}
	if !bytes.Equal(got.NextHopEphemeral, nextEphemeral) {
		t.Errorf("NextHopEphemeral = %x, want %x", got.NextHopEphemeral, nextEphemeral)
	}
}

func TestLayerPlaintextTerminalHopHasNoNextHopEphemeral(t *testing.T) {
	key, _ := DeriveKey([]byte("hop secret"), nil, LabelCircuitDataSend)
	layer := &LayerPlaintext{Inner: []byte("final payload")}

	ct, err := EncryptLayer(key, 1, layer)
	if err != nil {
		t.Fatalf("EncryptLayer returned error: %v", err)
	}
	got, err := DecryptLayer(key, 1, ct)
	if err != nil {
		t.Fatalf("DecryptLayer returned error: %v", err)
	}
	if len(got.NextHopEphemeral) != 0 {
		t.Errorf("NextHopEphemeral = %x, want empty (terminal hop)", got.NextHopEphemeral)
	}
}

func TestLayerPlaintextMarshalRejectsWrongSizeNextHopEphemeral(t *testing.T) {
	l := &LayerPlaintext{NextHopEphemeral: []byte("too short")}
	if _, err := l.marshal(); err == nil {
		t.Fatal("expected error for a NextHopEphemeral that isn't exactly KeySize bytes, got nil")
	}
}

func TestUnmarshalLayerPlaintextRejectsInvalidEphemeralFlag(t *testing.T) {
	// A hand-built plaintext: next_hop_len=0, then a flag byte that is
	// neither 0 nor 1.
	data := []byte{0, 0, 0, 0, 2}
	if _, err := unmarshalLayerPlaintext(data); err == nil {
		t.Fatal("expected error for an invalid has-next-ephemeral flag byte, got nil")
	}
}

func TestUnmarshalLayerPlaintextRejectsTruncatedEphemeral(t *testing.T) {
	// Claims a next ephemeral key is present (flag=1) but provides fewer
	// than KeySize bytes for it.
	data := []byte{0, 0, 0, 0, 1, 0xAB, 0xCD}
	if _, err := unmarshalLayerPlaintext(data); err == nil {
		t.Fatal("expected error for a truncated next-hop-ephemeral field, got nil")
	}
}

func threeTestHops(t *testing.T) []Hop {
	t.Helper()
	keyA, _ := DeriveKey([]byte("secret A"), nil, LabelCircuitDataSend)
	keyB, _ := DeriveKey([]byte("secret B"), nil, LabelCircuitDataSend)
	keyC, _ := DeriveKey([]byte("secret C"), nil, LabelCircuitDataSend)
	ephB := bytes.Repeat([]byte{0x02}, KeySize)
	ephC := bytes.Repeat([]byte{0x03}, KeySize)
	return []Hop{
		{NodeKey: []byte("node-A-key"), Key: keyA, Counter: 1, NextEphemeralPub: ephB},
		{NodeKey: []byte("node-B-key"), Key: keyB, Counter: 1, NextEphemeralPub: ephC},
		{NodeKey: []byte("node-C-key"), Key: keyC, Counter: 1},
	}
}

func TestBuildOnionThreeHopsEachHopPeelsOneLayer(t *testing.T) {
	hops := threeTestHops(t)
	payload := []byte("hello bob, from alice")

	onion, err := BuildOnion(hops, payload)
	if err != nil {
		t.Fatalf("BuildOnion returned error: %v", err)
	}

	// Hop A peels its layer: learns to forward to B.
	atA, err := DecryptLayer(hops[0].Key, hops[0].Counter, onion)
	if err != nil {
		t.Fatalf("hop A DecryptLayer returned error: %v", err)
	}
	if !bytes.Equal(atA.NextHop, hops[1].NodeKey) {
		t.Fatalf("hop A NextHop = %q, want %q", atA.NextHop, hops[1].NodeKey)
	}
	if !bytes.Equal(atA.NextHopEphemeral, hops[0].NextEphemeralPub) {
		t.Fatalf("hop A NextHopEphemeral = %x, want %x", atA.NextHopEphemeral, hops[0].NextEphemeralPub)
	}

	// Hop B peels its layer: learns to forward to C.
	atB, err := DecryptLayer(hops[1].Key, hops[1].Counter, atA.Inner)
	if err != nil {
		t.Fatalf("hop B DecryptLayer returned error: %v", err)
	}
	if !bytes.Equal(atB.NextHop, hops[2].NodeKey) {
		t.Fatalf("hop B NextHop = %q, want %q", atB.NextHop, hops[2].NodeKey)
	}
	if !bytes.Equal(atB.NextHopEphemeral, hops[1].NextEphemeralPub) {
		t.Fatalf("hop B NextHopEphemeral = %x, want %x", atB.NextHopEphemeral, hops[1].NextEphemeralPub)
	}

	// Hop C peels its layer: this is terminal, recovers the real payload.
	atC, err := DecryptLayer(hops[2].Key, hops[2].Counter, atB.Inner)
	if err != nil {
		t.Fatalf("hop C DecryptLayer returned error: %v", err)
	}
	if len(atC.NextHop) != 0 {
		t.Fatalf("hop C NextHop = %q, want empty (terminal)", atC.NextHop)
	}
	if !bytes.Equal(atC.Inner, payload) {
		t.Fatalf("hop C Inner = %q, want %q", atC.Inner, payload)
	}
}

func TestBuildOnionHopCannotDecryptAnotherHopsLayer(t *testing.T) {
	hops := threeTestHops(t)
	onion, err := BuildOnion(hops, []byte("payload"))
	if err != nil {
		t.Fatalf("BuildOnion returned error: %v", err)
	}

	atA, err := DecryptLayer(hops[0].Key, hops[0].Counter, onion)
	if err != nil {
		t.Fatalf("hop A DecryptLayer returned error: %v", err)
	}

	// Hop A must not be able to decrypt hop B's layer with its own key.
	if _, err := DecryptLayer(hops[0].Key, hops[1].Counter, atA.Inner); err == nil {
		t.Fatal("expected hop A to be unable to decrypt hop B's layer, got no error")
	}
}

func TestBuildOnionRejectsEmptyPath(t *testing.T) {
	if _, err := BuildOnion(nil, []byte("payload")); err == nil {
		t.Fatal("expected error for empty hop path, got nil")
	}
}

func TestBuildOnionHopLocalEmbedsNextLegMetadata(t *testing.T) {
	hops := testHops(3)
	for i := range hops {
		id, err := randomCircuitID()
		if err != nil {
			t.Fatalf("randomCircuitID returned error: %v", err)
		}
		hops[i].LocalCircuitID = id
		hops[i].Counter = uint64(1000 * (i + 1)) // distinct starting points, mirrors CreateCircuit's random-offset intent
	}
	legExpirations := []uint64{111, 222, 333}
	onion, err := BuildOnionHopLocal(hops, []byte("payload"), legExpirations)
	if err != nil {
		t.Fatalf("BuildOnionHopLocal returned error: %v", err)
	}

	// Hop 0 decrypts its own layer and must find leg 1's metadata (hops[1]'s
	// LocalCircuitID/Counter, and legExpirations[1]) - not its own.
	layer0, err := DecryptLayerHopLocal(hops[0].Key, hops[0].Counter, onion)
	if err != nil {
		t.Fatalf("DecryptLayerHopLocal (hop 0) returned error: %v", err)
	}
	if !bytes.Equal(layer0.NextLocalCircuitID, hops[1].LocalCircuitID[:]) {
		t.Fatalf("hop0's NextLocalCircuitID = %x, want hops[1].LocalCircuitID = %x", layer0.NextLocalCircuitID, hops[1].LocalCircuitID[:])
	}
	if layer0.NextLocalCounter != hops[1].Counter {
		t.Fatalf("hop0's NextLocalCounter = %d, want %d", layer0.NextLocalCounter, hops[1].Counter)
	}
	if layer0.NextLocalExpiration != legExpirations[1] {
		t.Fatalf("hop0's NextLocalExpiration = %d, want %d", layer0.NextLocalExpiration, legExpirations[1])
	}

	// The terminal hop's own layer carries no NextLocal* fields at all.
	layer1, err := DecryptLayerHopLocal(hops[1].Key, hops[1].Counter, layer0.Inner)
	if err != nil {
		t.Fatalf("DecryptLayerHopLocal (hop 1) returned error: %v", err)
	}
	layer2, err := DecryptLayerHopLocal(hops[2].Key, hops[2].Counter, layer1.Inner)
	if err != nil {
		t.Fatalf("DecryptLayerHopLocal (hop 2, terminal) returned error: %v", err)
	}
	if len(layer2.NextLocalCircuitID) != 0 {
		t.Fatalf("terminal hop's NextLocalCircuitID = %x, want empty", layer2.NextLocalCircuitID)
	}
	if !bytes.Equal(layer2.Inner, []byte("payload")) {
		t.Fatalf("delivered payload = %q, want %q", layer2.Inner, "payload")
	}
}

func TestUnmarshalLayerPlaintextHopLocalRoundTrips(t *testing.T) {
	nextID := CircuitID{1, 2, 3}
	l := &LayerPlaintext{
		NextHop:             []byte("next-hop-key"),
		NextHopEphemeral:    make([]byte, KeySize),
		Inner:               []byte("inner ciphertext"),
		NextLocalCircuitID:  nextID[:],
		NextLocalCounter:    42,
		NextLocalExpiration: 999,
	}
	data, err := l.marshalHopLocal()
	if err != nil {
		t.Fatalf("marshalHopLocal returned error: %v", err)
	}
	got, err := unmarshalLayerPlaintextHopLocal(data)
	if err != nil {
		t.Fatalf("unmarshalLayerPlaintextHopLocal returned error: %v", err)
	}
	if !bytes.Equal(got.NextLocalCircuitID, nextID[:]) || got.NextLocalCounter != 42 || got.NextLocalExpiration != 999 {
		t.Fatalf("round-trip mismatch: got %+v", got)
	}
}

func TestUnmarshalLayerPlaintextUnchangedForLegacyShape(t *testing.T) {
	// Existing legacy marshal/unmarshal pair must still round-trip exactly
	// as before this task's parseLayerPlaintextPrefix refactor.
	l := &LayerPlaintext{
		NextHop:          []byte("next-hop-key"),
		NextHopEphemeral: make([]byte, KeySize),
		Inner:            []byte("inner ciphertext"),
	}
	data, err := l.marshal()
	if err != nil {
		t.Fatalf("marshal returned error: %v", err)
	}
	got, err := unmarshalLayerPlaintext(data)
	if err != nil {
		t.Fatalf("unmarshalLayerPlaintext returned error: %v", err)
	}
	if !bytes.Equal(got.NextHop, l.NextHop) || !bytes.Equal(got.NextHopEphemeral, l.NextHopEphemeral) || !bytes.Equal(got.Inner, l.Inner) {
		t.Fatalf("legacy round-trip mismatch: got %+v, want %+v", got, l)
	}
}

func TestBuildOnionSingleHop(t *testing.T) {
	key, _ := DeriveKey([]byte("secret"), nil, LabelCircuitDataSend)
	hops := []Hop{{NodeKey: []byte("node-A-key"), Key: key, Counter: 1}}
	payload := []byte("direct payload")

	onion, err := BuildOnion(hops, payload)
	if err != nil {
		t.Fatalf("BuildOnion returned error: %v", err)
	}
	got, err := DecryptLayer(hops[0].Key, hops[0].Counter, onion)
	if err != nil {
		t.Fatalf("DecryptLayer returned error: %v", err)
	}
	if len(got.NextHop) != 0 {
		t.Errorf("NextHop = %q, want empty (single-hop path is terminal)", got.NextHop)
	}
	if !bytes.Equal(got.Inner, payload) {
		t.Errorf("Inner = %q, want %q", got.Inner, payload)
	}
}
