# Garlic Crypto/Protocol Hardening Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Fix the confirmed ephemeral-key linkability bug, widen circuit IDs, add HKDF domain separation, and add signed service descriptors to the Garlic Routing Overlay (`src/garlic/`), per `docs/superpowers/specs/2026-08-09-garlic-crypto-hardening-design.md`.

**Architecture:** A flag-day wire format change (`garlic-v1` → `garlic-v2`). Each circuit hop gets independent ephemeral X25519 key material, revealed to the next hop only inside that hop's own decrypted layer (chained telescoping, not Sphinx). `CircuitID` widens to 128-bit random. Key derivation becomes a two-stage HKDF chain (establish → data) with reserved send/recv direction labels. Service descriptors gain a separate Ed25519 signing identity and are Ed25519-signed; the client verifies GID/signature/expiry, never trusting the rendezvous.

**Tech Stack:** Go, `golang.org/x/crypto` (curve25519, hkdf, chacha20poly1305), stdlib `crypto/ed25519`, stdlib `testing` (including `testing.F` fuzz targets already in use).

## Global Constraints

- This is `src/garlic/` and its direct config/wiring in `src/config/config.go` and `cmd/yggdrasil/main.go` only. Do not touch ironwood, IPv6 addressing, or non-Garlic packet handling.
- `garlic.enabled: false` must continue to behave identically to no Garlic support — every task that touches `cmd/yggdrasil/main.go` or `src/config/config.go` must preserve this.
- Flag-day wire break: no `garlic-v1`/`garlic-v2` interop. Old and new Garlic builds fail capability negotiation cleanly (peer treated as legacy).
- No custom cryptographic primitives: only `curve25519`/`hkdf`/`chacha20poly1305` (already used) and stdlib `ed25519` (new).
- For purely mechanical propagation (a type or constant rename rippling through many call sites with no behavioral choice involved), the step says "grep for X, replace with Y, then `go build ./src/garlic/...` and fix what it reports" rather than diffing every call site — this is still exact and verifiable, just not spelled out byte-for-byte. Everywhere a real design or algorithmic decision is involved, the step contains the actual code.
- Run `go build ./... && go vet ./...` at the end of every task, and `go test ./src/garlic/...` after every task that touches `src/garlic/`. Commit only on green.

---

### Task 1: Widen CircuitID to 128 bits

**Files:**
- Modify: `src/garlic/circuit.go` (`CircuitID` type, `randomCircuitID`)
- Modify: `src/garlic/envelope.go` (`Envelope.CircuitID` field type, `Marshal`/`Unmarshal`, `envelopeFixedHeaderSize`)
- Modify: `src/garlic/protocol.go` (drop the now-redundant `CircuitID(env.CircuitID)` conversion)
- Modify: `src/garlic/manager.go` (`buildCircuitDataBody`'s `Envelope{CircuitID: ...}` literal)
- Modify: `src/garlic/admin.go` (`circuitIDToString`/`circuitIDFromString`/`parseCircuitIDRequest`)
- Modify: `src/garlic/circuit_test.go` (add `testCircuitID` helper)
- Modify: `src/garlic/circuit_manager_test.go`, `src/garlic/relaystate_test.go`, `src/garlic/manager_test.go`, `src/garlic/fuzz_test.go` (replace `CircuitID(n)` literals)
- Test: `src/garlic/circuit_test.go`, `src/garlic/envelope_test.go`

**Interfaces:**
- Consumes: nothing (first task).
- Produces: `type CircuitID [16]byte`, `Envelope.CircuitID CircuitID` — every later task's code that touches a `CircuitID` or `Envelope.CircuitID` uses this type.

- [ ] **Step 1: Write the failing tests**

Add to `src/garlic/circuit_test.go` (new imports: add `"encoding/binary"` to the existing import block):

```go
// testCircuitID builds a distinguishable CircuitID for tests, encoding n
// into the last 8 bytes so distinct small integers remain distinct
// distinguishable IDs (the type itself carries no integer semantics -
// production code only ever compares CircuitID for equality).
func testCircuitID(n uint64) CircuitID {
	var id CircuitID
	binary.BigEndian.PutUint64(id[8:], n)
	return id
}

func TestRandomCircuitIDsAreNotDuplicated(t *testing.T) {
	ids := make(map[CircuitID]bool)
	for i := 0; i < 1000; i++ {
		id, err := randomCircuitID()
		if err != nil {
			t.Fatalf("randomCircuitID returned error: %v", err)
		}
		if ids[id] {
			t.Fatalf("randomCircuitID produced a duplicate after %d draws", i)
		}
		ids[id] = true
	}
}
```

Add to `src/garlic/envelope_test.go`:

```go
func TestEnvelopeCircuitIDRoundTripsFull16Bytes(t *testing.T) {
	var id CircuitID
	for i := range id {
		id[i] = byte(i + 1) // every byte position distinct and non-zero
	}
	e := &Envelope{Version: EnvelopeVersion1, CircuitID: id, PacketCounter: 1, Expiration: 1}
	data, err := e.Marshal()
	if err != nil {
		t.Fatalf("Marshal returned error: %v", err)
	}
	got, err := Unmarshal(data)
	if err != nil {
		t.Fatalf("Unmarshal returned error: %v", err)
	}
	if got.CircuitID != id {
		t.Fatalf("CircuitID = %x, want %x (must round-trip all 16 bytes, not the old 8-byte width)", got.CircuitID, id)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail to compile**

Run: `go test ./src/garlic/... -run TestRandomCircuitIDsAreNotDuplicated`
Expected: FAIL to compile — `CircuitID` is still `uint64`, `testCircuitID`/the new test reference a 16-byte array.

- [ ] **Step 3: Change `CircuitID` to `[16]byte` in `src/garlic/circuit.go`**

Replace:

```go
type CircuitID uint64
```

with:

```go
type CircuitID [16]byte
```

Replace `randomCircuitID`:

```go
func randomCircuitID() (CircuitID, error) {
	var id CircuitID
	if _, err := rand.Read(id[:]); err != nil {
		return CircuitID{}, err
	}
	return id, nil
}
```

Remove the now-unused `"encoding/binary"` import from `circuit.go` (it was only used by the old `randomCircuitID`).

- [ ] **Step 4: Update `src/garlic/envelope.go`**

Change the fixed header size constant:

```go
// envelopeFixedHeaderSize is the size, in bytes, of the fixed-length
// portion of the wire format: version(1) + circuit_id(16) + packet_counter(8)
// + expiration(8) + body_len(4).
const envelopeFixedHeaderSize = 1 + 16 + 8 + 8 + 4
```

Change the struct field:

```go
type Envelope struct {
	Version       uint8
	CircuitID     CircuitID
	PacketCounter uint64
	Expiration    uint64
	Body          []byte
	Padding       []byte
}
```

Update `Marshal`:

```go
func (e *Envelope) Marshal() ([]byte, error) {
	if len(e.Body) > MaxBodySize {
		return nil, ErrBodyTooLarge
	}
	if len(e.Padding) > MaxPaddingSize {
		return nil, ErrPaddingTooLarge
	}

	buf := make([]byte, 0, envelopeFixedHeaderSize+len(e.Body)+4+len(e.Padding))
	buf = append(buf, e.Version)
	buf = append(buf, e.CircuitID[:]...)
	buf = binary.BigEndian.AppendUint64(buf, e.PacketCounter)
	buf = binary.BigEndian.AppendUint64(buf, e.Expiration)
	buf = binary.BigEndian.AppendUint32(buf, uint32(len(e.Body)))
	buf = append(buf, e.Body...)
	buf = binary.BigEndian.AppendUint32(buf, uint32(len(e.Padding)))
	buf = append(buf, e.Padding...)
	return buf, nil
}
```

Update `Unmarshal`'s fixed-header parsing (the body/padding parsing below it is unchanged, just re-index the offsets):

```go
func Unmarshal(data []byte) (*Envelope, error) {
	if len(data) < envelopeFixedHeaderSize {
		return nil, ErrEnvelopeTooShort
	}

	e := &Envelope{Version: data[0]}
	copy(e.CircuitID[:], data[1:17])
	e.PacketCounter = binary.BigEndian.Uint64(data[17:25])
	e.Expiration = binary.BigEndian.Uint64(data[25:33])
	if e.Version != EnvelopeVersion1 {
		return nil, ErrUnsupportedVersion
	}

	rest := data[envelopeFixedHeaderSize:]
	bodyLen := binary.BigEndian.Uint32(data[33:37])
	if bodyLen > MaxBodySize {
		return nil, ErrBodyTooLarge
	}
	if uint64(bodyLen) > uint64(len(rest)) {
		return nil, ErrEnvelopeTruncated
	}
	if bodyLen > 0 {
		e.Body = append([]byte(nil), rest[:bodyLen]...)
	}
	rest = rest[bodyLen:]

	if len(rest) < 4 {
		return nil, ErrEnvelopeTruncated
	}
	paddingLen := binary.BigEndian.Uint32(rest[:4])
	rest = rest[4:]
	if paddingLen > MaxPaddingSize {
		return nil, ErrPaddingTooLarge
	}
	if uint64(paddingLen) > uint64(len(rest)) {
		return nil, ErrEnvelopeTruncated
	}
	if paddingLen > 0 {
		e.Padding = append([]byte(nil), rest[:paddingLen]...)
	}

	return e, nil
}
```

- [ ] **Step 5: Update `src/garlic/protocol.go`**

In `processCircuitData`, replace:

```go
	circuitID := CircuitID(env.CircuitID)
```

with:

```go
	circuitID := env.CircuitID
```

(the rest of the function is unchanged by this task — its `Envelope{CircuitID: env.CircuitID, ...}` literal already just copies the field, which now carries the right type automatically).

- [ ] **Step 6: Update `src/garlic/manager.go`**

In `buildCircuitDataBody`, replace:

```go
	env := &Envelope{
		Version:       EnvelopeVersion1,
		CircuitID:     uint64(id),
		PacketCounter: counter,
		Expiration:    expiration,
		Body:          onion,
	}
```

with:

```go
	env := &Envelope{
		Version:       EnvelopeVersion1,
		CircuitID:     id,
		PacketCounter: counter,
		Expiration:    expiration,
		Body:          onion,
	}
```

- [ ] **Step 7: Update `src/garlic/admin.go`**

Replace `circuitIDToString`, `circuitIDFromString`, and `parseCircuitIDRequest`:

```go
func circuitIDToString(id CircuitID) string {
	return hex.EncodeToString(id[:])
}

func circuitIDFromString(s string) (CircuitID, error) {
	b, err := hex.DecodeString(s)
	if err != nil {
		return CircuitID{}, fmt.Errorf("invalid circuitId: %w", err)
	}
	if len(b) != len(CircuitID{}) {
		return CircuitID{}, fmt.Errorf("invalid circuitId: want %d bytes, got %d", len(CircuitID{}), len(b))
	}
	var id CircuitID
	copy(id[:], b)
	return id, nil
}

func parseCircuitIDRequest(in json.RawMessage) (CircuitID, error) {
	var req struct {
		CircuitID string `json:"circuitId"`
	}
	if err := json.Unmarshal(in, &req); err != nil {
		return CircuitID{}, err
	}
	return circuitIDFromString(req.CircuitID)
}
```

- [ ] **Step 8: Fix every remaining compile error by rebuilding**

Run: `go build ./src/garlic/...`

Fix each reported error by replacing bare `CircuitID(n)` conversions in test files with the new `testCircuitID(n)` helper (from Step 1) and `0`/zero-value returns of type `CircuitID` with `CircuitID{}`. Specifically:

- `src/garlic/circuit_manager_test.go`: `CircuitID(12345)` → `testCircuitID(12345)`.
- `src/garlic/relaystate_test.go`: `CircuitID(1)` → `testCircuitID(1)`, `CircuitID(2)` → `testCircuitID(2)` (each occurrence).
- `src/garlic/manager_test.go`: `CircuitID(1)` → `testCircuitID(1)`, `CircuitID(42)` → `testCircuitID(42)`; and `env.CircuitID != 42` → `env.CircuitID != testCircuitID(42)`.
- `src/garlic/fuzz_test.go`: in `FuzzEnvelopeUnmarshal`'s seed `Envelope{..., CircuitID: 1, ...}` → `CircuitID: testCircuitID(1)` (move/duplicate the `testCircuitID` helper here if `circuit_test.go`'s isn't visible — it is, same package, no duplication needed); in `buildTestCircuitDataForFuzz`, `CircuitID: uint64(c.ID)` → `CircuitID: c.ID`.

Re-run `go build ./src/garlic/...` until it succeeds.

- [ ] **Step 9: Run the full package test suite**

Run: `go test ./src/garlic/... -run . -v 2>&1 | tail -80`
Expected: PASS — all existing tests plus the two new ones from Step 1.

- [ ] **Step 10: Commit**

```bash
git add src/garlic/circuit.go src/garlic/envelope.go src/garlic/protocol.go src/garlic/manager.go \
  src/garlic/admin.go src/garlic/circuit_test.go src/garlic/envelope_test.go \
  src/garlic/circuit_manager_test.go src/garlic/relaystate_test.go src/garlic/manager_test.go src/garlic/fuzz_test.go
git commit -m "garlic: widen CircuitID to 128-bit random"
```

---

### Task 2: Two-stage HKDF key derivation with direction labels

**Files:**
- Modify: `src/garlic/crypto.go` (labels, `deriveLayerKey`)
- Modify: `src/garlic/crypto_test.go`, `src/garlic/layer_test.go`, `src/garlic/circuit_test.go`, `src/garlic/fuzz_test.go` (replace `LabelLayerKey`/`LabelCircuitKey` references)
- Test: `src/garlic/crypto_test.go`

**Interfaces:**
- Consumes: nothing new.
- Produces: `LabelCircuitEstablish`, `LabelCircuitDataSend`, `LabelCircuitDataRecv` (string constants), `func deriveLayerKey(ecdhSecret []byte) ([]byte, error)` — used by Task 4's `CreateCircuit`/`processCircuitData` and Task 4's linkability tests.

- [ ] **Step 1: Write the failing tests**

Add to `src/garlic/crypto_test.go`:

```go
func TestDeriveLayerKeyIsTwoStageNotEqualToRawEstablishSecret(t *testing.T) {
	ecdhSecret := []byte("a shared ECDH output")

	establishSecret, err := DeriveKey(ecdhSecret, nil, LabelCircuitEstablish)
	if err != nil {
		t.Fatalf("DeriveKey returned error: %v", err)
	}
	dataKey, err := deriveLayerKey(ecdhSecret)
	if err != nil {
		t.Fatalf("deriveLayerKey returned error: %v", err)
	}
	if bytes.Equal(dataKey, establishSecret) {
		t.Error("deriveLayerKey's output equals the intermediate establish-stage secret - the two stages collapsed into one")
	}

	wantDataKey, err := DeriveKey(establishSecret, nil, LabelCircuitDataSend)
	if err != nil {
		t.Fatalf("DeriveKey returned error: %v", err)
	}
	if !bytes.Equal(dataKey, wantDataKey) {
		t.Error("deriveLayerKey does not match manually chaining DeriveKey(secret, EstablishLabel) then DeriveKey(that, DataSendLabel)")
	}
}

func TestDeriveLayerKeyDeterministic(t *testing.T) {
	ecdhSecret := []byte("a shared ECDH output")
	k1, err := deriveLayerKey(ecdhSecret)
	if err != nil {
		t.Fatalf("deriveLayerKey returned error: %v", err)
	}
	k2, err := deriveLayerKey(ecdhSecret)
	if err != nil {
		t.Fatalf("deriveLayerKey returned error: %v", err)
	}
	if !bytes.Equal(k1, k2) {
		t.Error("deriveLayerKey produced different keys for identical inputs")
	}
}

func TestSendAndRecvDirectionLabelsProduceDifferentKeys(t *testing.T) {
	establishSecret := []byte("an establishment-stage secret")
	sendKey, err := DeriveKey(establishSecret, nil, LabelCircuitDataSend)
	if err != nil {
		t.Fatalf("DeriveKey returned error: %v", err)
	}
	recvKey, err := DeriveKey(establishSecret, nil, LabelCircuitDataRecv)
	if err != nil {
		t.Fatalf("DeriveKey returned error: %v", err)
	}
	if bytes.Equal(sendKey, recvKey) {
		t.Error("send and recv direction labels produced the same key from the same establish secret - a reflected packet would decrypt under the wrong direction's key")
	}
}
```

Replace the existing `TestDeriveKeyDiffersByLabel` (currently uses the about-to-be-removed `LabelCircuitKey`) with:

```go
func TestDeriveKeyDiffersByLabel(t *testing.T) {
	secret := []byte("shared secret material")

	k1, err := DeriveKey(secret, nil, LabelCircuitDataSend)
	if err != nil {
		t.Fatalf("DeriveKey returned error: %v", err)
	}
	k2, err := DeriveKey(secret, nil, LabelCircuitDataRecv)
	if err != nil {
		t.Fatalf("DeriveKey returned error: %v", err)
	}
	if bytes.Equal(k1, k2) {
		t.Error("DeriveKey produced the same key for two different domain-separation labels")
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail to compile**

Run: `go test ./src/garlic/... -run TestDeriveLayerKey`
Expected: FAIL to compile — `deriveLayerKey`, `LabelCircuitEstablish`, `LabelCircuitDataSend`, `LabelCircuitDataRecv` don't exist yet.

- [ ] **Step 3: Implement in `src/garlic/crypto.go`**

Replace the existing label block:

```go
const (
	LabelLayerKey   = "yggdrasil-garlic-v1-layer-key"
	LabelCircuitKey = "yggdrasil-garlic-v1-circuit-key"
)
```

with:

```go
// Domain-separation labels for HKDF-derived keys, under the garlic-v2
// wire format (see CapabilityGarlicV2). LabelCircuitDataRecv is reserved
// but unused until a reply/return path exists - see deriveLayerKey's
// doc comment for why establish/data are two chained stages rather than
// two labels on the same derivation.
const (
	LabelCircuitEstablish = "yggdrasil-garlic-v2-circuit-establish"
	LabelCircuitDataSend  = "yggdrasil-garlic-v2-circuit-data-send"
	LabelCircuitDataRecv  = "yggdrasil-garlic-v2-circuit-data-recv"
)

// deriveLayerKey derives a per-hop layer encryption key from a raw ECDH
// output in two HKDF stages: first into a circuit-establishment secret,
// then from that into the forward-direction circuit-data key. The
// protocol is fully non-interactive (there is no separate handshake
// message distinct from data packets), so "circuit establishment" and
// "circuit data" are modeled as two stages of one chain rather than two
// wire phases that don't actually exist - this still gives real,
// checkable domain separation: the establishment secret and the data
// key are cryptographically distinct values, not just different labels
// applied to the same input. Chaining through LabelCircuitEstablish also
// means a future reply path, keying off LabelCircuitDataRecv from the
// same establishment secret, is structurally unable to derive the
// forward-direction key.
func deriveLayerKey(ecdhSecret []byte) ([]byte, error) {
	establishSecret, err := DeriveKey(ecdhSecret, nil, LabelCircuitEstablish)
	if err != nil {
		return nil, err
	}
	return DeriveKey(establishSecret, nil, LabelCircuitDataSend)
}
```

- [ ] **Step 4: Fix every remaining compile error by rebuilding**

Run: `go build ./src/garlic/...` and `go vet ./src/garlic/...`

Replace every remaining reference to the now-removed `LabelLayerKey` with `LabelCircuitDataSend` (these tests exercise generic `Seal`/`Open`/`DecryptLayer`/`EncryptLayer`/`BuildOnion` behavior with an arbitrary key — `LabelCircuitDataSend` is the correct semantic successor). This affects, at minimum:

- `src/garlic/crypto_test.go`: every remaining `LabelLayerKey` occurrence.
- `src/garlic/layer_test.go`: every `LabelLayerKey` occurrence (in `TestEncryptLayerDecryptLayerRoundTripWithNextHop`, `TestEncryptLayerDecryptLayerRoundTripTerminal`, `TestDecryptLayerRejectsWrongKey`, `TestDecryptLayerRejectsTamperedCiphertext`, `TestDecryptLayerRejectsMalformedPlaintext`, `threeTestHops`, `TestBuildOnionSingleHop`).
- `src/garlic/circuit_test.go`: `testHops`'s `LabelLayerKey` occurrence.
- `src/garlic/fuzz_test.go`: `buildTestCircuitDataForFuzz`'s `LabelLayerKey` occurrence.

Re-run `go build ./src/garlic/...` until it succeeds.

- [ ] **Step 5: Run the full package test suite**

Run: `go test ./src/garlic/... -v 2>&1 | tail -100`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add src/garlic/crypto.go src/garlic/crypto_test.go src/garlic/layer_test.go src/garlic/circuit_test.go src/garlic/fuzz_test.go
git commit -m "garlic: two-stage HKDF key derivation with reserved direction labels"
```

---

### Task 3: Per-hop ephemeral field in `Hop`/`LayerPlaintext`

**Files:**
- Modify: `src/garlic/layer.go` (`Hop.NextEphemeralPub`, `LayerPlaintext.NextHopEphemeral`, marshal/unmarshal, `BuildOnion`)
- Modify: `src/garlic/layer_test.go`
- Test: `src/garlic/layer_test.go`

**Interfaces:**
- Consumes: nothing new.
- Produces: `Hop.NextEphemeralPub []byte`, `LayerPlaintext.NextHopEphemeral []byte` — used by Task 4's `CreateCircuit`/`processCircuitData`.

- [ ] **Step 1: Write the failing tests**

Add to `src/garlic/layer_test.go`:

```go
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
```

Update `TestBuildOnionThreeHopsEachHopPeelsOneLayer` and `threeTestHops` to also carry and check `NextEphemeralPub`/`NextHopEphemeral`:

```go
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
```

Add an assertion at the end of `TestBuildOnionThreeHopsEachHopPeelsOneLayer` (after the existing hop-A assertions):

```go
	if !bytes.Equal(atA.NextHopEphemeral, hops[0].NextEphemeralPub) {
		t.Fatalf("hop A NextHopEphemeral = %x, want %x", atA.NextHopEphemeral, hops[0].NextEphemeralPub)
	}
```

and similarly after the hop-B assertions:

```go
	if !bytes.Equal(atB.NextHopEphemeral, hops[1].NextEphemeralPub) {
		t.Fatalf("hop B NextHopEphemeral = %x, want %x", atB.NextHopEphemeral, hops[1].NextEphemeralPub)
	}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./src/garlic/... -run 'TestLayerPlaintext|TestUnmarshalLayerPlaintext|TestBuildOnionThreeHops'`
Expected: FAIL — `Hop.NextEphemeralPub`/`LayerPlaintext.NextHopEphemeral` don't exist yet.

- [ ] **Step 3: Implement in `src/garlic/layer.go`**

Add new error variables alongside the existing ones:

```go
var (
	ErrEmptyPath                    = errors.New("garlic: onion path must have at least one hop")
	ErrLayerTooShort                = errors.New("garlic: layer plaintext shorter than fixed header")
	ErrLayerTruncated               = errors.New("garlic: layer plaintext truncated")
	ErrNextHopTooLarge               = errors.New("garlic: next-hop field exceeds maximum size")
	ErrLayerInnerTooLarge            = errors.New("garlic: layer inner field exceeds maximum size")
	ErrInvalidNextHopEphemeralSize   = errors.New("garlic: next-hop ephemeral key has invalid size")
	ErrInvalidNextHopEphemeralFlag   = errors.New("garlic: invalid next-hop-ephemeral presence flag")
)
```

Update the two structs:

```go
type Hop struct {
	NodeKey          []byte // this hop's Yggdrasil public key (routing address)
	Key              []byte // per-hop symmetric key, already derived (e.g. via ECDH + deriveLayerKey)
	Counter          uint64 // nonce/replay counter for this hop's layer
	NextEphemeralPub []byte // ephemeral X25519 pubkey for the hop that follows this one; nil for the final hop
}

// LayerPlaintext is what a hop recovers after decrypting its layer:
// either forwarding instructions (NextHop and NextHopEphemeral set,
// Inner is the ciphertext to forward there) or, for the final hop, the
// delivered payload (NextHop and NextHopEphemeral both empty, Inner is
// the payload itself). NextHopEphemeral only ever becomes visible to
// the hop that decrypts this exact layer - see docs/superpowers/specs/
// 2026-08-09-garlic-crypto-hardening-design.md section A for why this
// is what gives non-adjacent hops no ephemeral key in common.
type LayerPlaintext struct {
	NextHop          []byte
	NextHopEphemeral []byte
	Inner            []byte
}
```

Replace `marshal`:

```go
func (l *LayerPlaintext) marshal() ([]byte, error) {
	if len(l.NextHop) > MaxNextHopSize {
		return nil, ErrNextHopTooLarge
	}
	if len(l.NextHopEphemeral) != 0 && len(l.NextHopEphemeral) != KeySize {
		return nil, ErrInvalidNextHopEphemeralSize
	}
	if len(l.Inner) > MaxLayerInnerSize {
		return nil, ErrLayerInnerTooLarge
	}
	buf := make([]byte, 0, 4+len(l.NextHop)+1+len(l.NextHopEphemeral)+4+len(l.Inner))
	buf = binary.BigEndian.AppendUint32(buf, uint32(len(l.NextHop)))
	buf = append(buf, l.NextHop...)
	if len(l.NextHopEphemeral) == KeySize {
		buf = append(buf, 1)
		buf = append(buf, l.NextHopEphemeral...)
	} else {
		buf = append(buf, 0)
	}
	buf = binary.BigEndian.AppendUint32(buf, uint32(len(l.Inner)))
	buf = append(buf, l.Inner...)
	return buf, nil
}
```

Replace `unmarshalLayerPlaintext`:

```go
func unmarshalLayerPlaintext(data []byte) (*LayerPlaintext, error) {
	if len(data) < 4 {
		return nil, ErrLayerTooShort
	}
	nextHopLen := binary.BigEndian.Uint32(data[:4])
	rest := data[4:]
	if nextHopLen > MaxNextHopSize {
		return nil, ErrNextHopTooLarge
	}
	if uint64(nextHopLen) > uint64(len(rest)) {
		return nil, ErrLayerTruncated
	}
	l := &LayerPlaintext{}
	if nextHopLen > 0 {
		l.NextHop = append([]byte(nil), rest[:nextHopLen]...)
	}
	rest = rest[nextHopLen:]

	if len(rest) < 1 {
		return nil, ErrLayerTruncated
	}
	hasNextEphemeral := rest[0]
	rest = rest[1:]
	switch hasNextEphemeral {
	case 1:
		if uint64(KeySize) > uint64(len(rest)) {
			return nil, ErrLayerTruncated
		}
		l.NextHopEphemeral = append([]byte(nil), rest[:KeySize]...)
		rest = rest[KeySize:]
	case 0:
		// no next-hop ephemeral key - terminal hop.
	default:
		return nil, ErrInvalidNextHopEphemeralFlag
	}

	if len(rest) < 4 {
		return nil, ErrLayerTruncated
	}
	innerLen := binary.BigEndian.Uint32(rest[:4])
	rest = rest[4:]
	if innerLen > MaxLayerInnerSize {
		return nil, ErrLayerInnerTooLarge
	}
	if uint64(innerLen) > uint64(len(rest)) {
		return nil, ErrLayerTruncated
	}
	if innerLen > 0 {
		l.Inner = append([]byte(nil), rest[:innerLen]...)
	}
	return l, nil
}
```

Update `BuildOnion`'s layer construction (only the composite literal changes):

```go
		ct, err := EncryptLayer(hops[i].Key, hops[i].Counter, &LayerPlaintext{
			NextHop:          nextHop,
			NextHopEphemeral: hops[i].NextEphemeralPub,
			Inner:            inner,
		})
```

- [ ] **Step 4: Run gofmt and rebuild**

Run: `gofmt -w src/garlic/layer.go && go build ./src/garlic/...`

- [ ] **Step 5: Run the full package test suite**

Run: `go test ./src/garlic/... -v 2>&1 | tail -100`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add src/garlic/layer.go src/garlic/layer_test.go
git commit -m "garlic: add per-hop NextHopEphemeral to LayerPlaintext"
```

---

### Task 4: Wire chained per-hop ephemeral keys into circuit build + relay forwarding (Part 1 core fix)

**Files:**
- Modify: `src/garlic/manager.go` (`CreateCircuit`)
- Modify: `src/garlic/protocol.go` (`processCircuitData`'s forward path)
- Create: `src/garlic/linkability_test.go`
- Test: `src/garlic/linkability_test.go`, `src/garlic/manager_test.go` (unaffected but re-verified)

**Interfaces:**
- Consumes: `deriveLayerKey` (Task 2), `Hop.NextEphemeralPub`/`LayerPlaintext.NextHopEphemeral` (Task 3), `CircuitID` (Task 1).
- Produces: the fixed `CreateCircuit`/`processCircuitData` behavior every later task (5-10) builds and tests against.

- [ ] **Step 1: Write the failing tests**

Create `src/garlic/linkability_test.go`:

```go
package garlic

// Tests proving the per-hop ephemeral key property Part 1 of the
// hardening task exists to guarantee: non-adjacent relays never observe
// a common ephemeral public key, and a relay cannot derive another
// hop's session key from what it actually receives. See
// docs/superpowers/specs/2026-08-09-garlic-crypto-hardening-design.md
// section A.

import (
	"bytes"
	"testing"
	"time"
)

// hopGarlicFor returns a minimal *Garlic usable to call
// processCircuitData as the given hop identity, independent of any
// running core.Core or admin socket.
func hopGarlicFor(id *Identity) *Garlic {
	return &Garlic{
		identity:   id,
		cfg:        DefaultConfig(),
		relayState: newRelayCircuitState(1024),
		delivered:  make(chan DeliveredMessage, 16),
	}
}

// buildThreeHopOriginator returns a *Garlic configured to originate
// circuits, plus three independent hop Identities the circuit will run
// over (each with its own real X25519 keypair, so the test can inspect
// what each hop's own view of the wire traffic actually is).
func buildThreeHopOriginator(t *testing.T) (originator *Garlic, hopIdentities []*Identity) {
	t.Helper()
	originatorID, err := NewIdentity()
	if err != nil {
		t.Fatalf("NewIdentity (originator) returned error: %v", err)
	}
	g := &Garlic{
		identity:        originatorID,
		cfg:             DefaultConfig(),
		circuits:        NewCircuitManager(CircuitManagerConfig{MaxCircuits: 16, MaxCircuitsPerPeer: 16}),
		relayState:      newRelayCircuitState(1024),
		originEphemeral: make(map[CircuitID][]byte),
		delivered:       make(chan DeliveredMessage, 16),
	}

	hops := make([]*Identity, 3)
	for i := range hops {
		id, err := NewIdentity()
		if err != nil {
			t.Fatalf("NewIdentity (hop %d) returned error: %v", i, err)
		}
		hops[i] = id
	}
	return g, hops
}

func buildTestPath(hopIdentities []*Identity) ([]CapabilityMessage, [][]byte) {
	path := make([]CapabilityMessage, len(hopIdentities))
	nodeKeys := make([][]byte, len(hopIdentities))
	for i, id := range hopIdentities {
		// Uses CapabilityGarlicV1 deliberately - Task 5 (later in this
		// plan) renames it to CapabilityGarlicV2 and its grep-based
		// propagation step picks up this reference along with every
		// other one, so this test stays buildable at the point Task 4
		// itself is executed.
		path[i] = CapabilityMessage{Versions: []string{CapabilityGarlicV1}, PublicKey: id.PublicKey}
		nodeKeys[i] = []byte{byte('A' + i)} // stand-in Yggdrasil routing key
	}
	return path, nodeKeys
}

func TestNonAdjacentHopsCannotLinkViaEphemeralKeys(t *testing.T) {
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
	onion, _, counter, err := c.Seal([]byte("hello"))
	if err != nil {
		t.Fatalf("Seal returned error: %v", err)
	}
	e1Pub := g.originEphemeral[circuitID]
	bodyToHop1, err := buildCircuitDataBody(e1Pub, circuitID, counter, uint64(time.Now().Add(time.Minute).Unix()), onion, g.cfg)
	if err != nil {
		t.Fatalf("buildCircuitDataBody returned error: %v", err)
	}
	e1 := append([]byte(nil), bodyToHop1[:KeySize]...)

	hop1 := hopGarlicFor(hopIDs[0])
	action1 := hop1.processCircuitData(bodyToHop1)
	if action1.kind != actionForward {
		t.Fatalf("hop1 action = %v, want actionForward", action1.kind)
	}
	e2 := append([]byte(nil), action1.forwardMsg[1:1+KeySize]...)

	hop2 := hopGarlicFor(hopIDs[1])
	action2 := hop2.processCircuitData(action1.forwardMsg[1:])
	if action2.kind != actionForward {
		t.Fatalf("hop2 action = %v, want actionForward", action2.kind)
	}
	e3 := append([]byte(nil), action2.forwardMsg[1:1+KeySize]...)

	hop3 := hopGarlicFor(hopIDs[2])
	action3 := hop3.processCircuitData(action2.forwardMsg[1:])
	if action3.kind != actionDeliver {
		t.Fatalf("hop3 action = %v, want actionDeliver", action3.kind)
	}
	if !bytes.Equal(action3.payload, []byte("hello")) {
		t.Fatalf("delivered payload = %q, want %q", action3.payload, "hello")
	}

	// Each hop's message used a distinct ephemeral key.
	if bytes.Equal(e1, e2) || bytes.Equal(e2, e3) || bytes.Equal(e1, e3) {
		t.Fatalf("ephemeral keys not all distinct: e1=%x e2=%x e3=%x", e1, e2, e3)
	}

	// Hop 1's observed set is {e1, e2} (e1: what it received; e2: what it
	// had to forward on). Hop 3 only ever observes {e3}. The two sets
	// must not intersect - this is the anti-linkability property itself:
	// colluding hop1+hop3 (non-adjacent) cannot link the circuit by
	// comparing ephemeral keys.
	for _, seen := range [][]byte{e1, e2} {
		if bytes.Equal(seen, e3) {
			t.Fatalf("hop1 observed an ephemeral key (%x) that hop3 also sees - circuits are linkable", seen)
		}
	}
}

func TestRelay1CannotDeriveRelay2SessionKey(t *testing.T) {
	g, hopIDs := buildThreeHopOriginator(t)
	path, nodeKeys := buildTestPath(hopIDs[:2])

	circuitID, err := g.CreateCircuit(path, nodeKeys)
	if err != nil {
		t.Fatalf("CreateCircuit returned error: %v", err)
	}
	c, _ := g.circuits.Get(circuitID)
	onion, _, counter, err := c.Seal([]byte("payload"))
	if err != nil {
		t.Fatalf("Seal returned error: %v", err)
	}
	e1Pub := g.originEphemeral[circuitID]
	bodyToHop1, err := buildCircuitDataBody(e1Pub, circuitID, counter, uint64(time.Now().Add(time.Minute).Unix()), onion, g.cfg)
	if err != nil {
		t.Fatalf("buildCircuitDataBody returned error: %v", err)
	}

	hop1 := hopGarlicFor(hopIDs[0])
	action1 := hop1.processCircuitData(bodyToHop1)
	if action1.kind != actionForward {
		t.Fatalf("hop1 action = %v, want actionForward", action1.kind)
	}
	e2 := action1.forwardMsg[1 : 1+KeySize]

	// The only Diffie-Hellman computation relay1 could actually attempt
	// with key material it possesses is ECDH(relay1's own identity
	// private key, e2) - it has no other private scalar available. That
	// must not equal hop 2's real session key.
	wrongSecret, err := ECDH(hopIDs[0].PrivateKey, e2)
	if err != nil {
		t.Fatalf("ECDH returned error: %v", err)
	}
	wrongKey, err := deriveLayerKey(wrongSecret)
	if err != nil {
		t.Fatalf("deriveLayerKey returned error: %v", err)
	}

	realSecret, err := ECDH(hopIDs[1].PrivateKey, e2)
	if err != nil {
		t.Fatalf("ECDH returned error: %v", err)
	}
	realKey, err := deriveLayerKey(realSecret)
	if err != nil {
		t.Fatalf("deriveLayerKey returned error: %v", err)
	}

	if bytes.Equal(wrongKey, realKey) {
		t.Fatal("relay1 derived the same session key as relay2 using only its own identity key - session keys are not hop-isolated")
	}
}

func TestDifferentHopsGetDifferentEphemeralPublicKeys(t *testing.T) {
	g, hopIDs := buildThreeHopOriginator(t)
	path, nodeKeys := buildTestPath(hopIDs)

	circuitID, err := g.CreateCircuit(path, nodeKeys)
	if err != nil {
		t.Fatalf("CreateCircuit returned error: %v", err)
	}
	c, _ := g.circuits.Get(circuitID)
	if len(c.hops) != 3 {
		t.Fatalf("circuit has %d hops, want 3", len(c.hops))
	}
	e1 := g.originEphemeral[circuitID]
	e2 := c.hops[0].NextEphemeralPub
	e3 := c.hops[1].NextEphemeralPub
	if len(c.hops[2].NextEphemeralPub) != 0 {
		t.Errorf("final hop NextEphemeralPub = %x, want empty", c.hops[2].NextEphemeralPub)
	}
	if bytes.Equal(e1, e2) || bytes.Equal(e2, e3) || bytes.Equal(e1, e3) {
		t.Fatalf("CreateCircuit reused an ephemeral public key across hops: e1=%x e2=%x e3=%x", e1, e2, e3)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./src/garlic/... -run 'TestNonAdjacentHopsCannotLink|TestRelay1CannotDerive|TestDifferentHopsGetDifferent'`
Expected: FAIL — `CreateCircuit` still generates one ephemeral keypair for the whole circuit, and `processCircuitData` still forwards the received ephemeral pubkey unchanged, so `e1`/`e2`/`e3` will all be equal.

- [ ] **Step 3: Fix `CreateCircuit` in `src/garlic/manager.go`**

Replace the existing `CreateCircuit`:

```go
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
```

- [ ] **Step 4: Fix `processCircuitData`'s forward path in `src/garlic/protocol.go`**

Add a guard right after the existing terminal-hop check, and change what gets forwarded as the ephemeral prefix:

```go
	if len(layer.NextHop) == 0 {
		return circuitAction{kind: actionDeliver, circuitID: circuitID, payload: layer.Inner}
	}
	if len(layer.NextHopEphemeral) != KeySize {
		// A well-formed intermediate layer always carries the next hop's
		// ephemeral key; anything else is malformed or malicious input,
		// treated identically to any other unforwardable message.
		return circuitAction{kind: actionDrop}
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
		return circuitAction{kind: actionDrop}
	}
	forwardMsg := make([]byte, 0, 1+KeySize+len(nextBytes))
	forwardMsg = append(forwardMsg, msgTypeCircuitData)
	forwardMsg = append(forwardMsg, layer.NextHopEphemeral...)
	forwardMsg = append(forwardMsg, nextBytes...)

	return circuitAction{kind: actionForward, circuitID: circuitID, forwardTo: layer.NextHop, forwardMsg: forwardMsg}
```

(The `ephemeralPub := body[:KeySize]` variable earlier in the function is still used for this hop's own `ECDH(g.identity.PrivateKey, ephemeralPub)` — that part is unchanged. Only what gets forwarded changes.)

- [ ] **Step 5: Rebuild and run the new tests**

Run: `go build ./src/garlic/... && go test ./src/garlic/... -run 'TestNonAdjacentHopsCannotLink|TestRelay1CannotDerive|TestDifferentHopsGetDifferent' -v`
Expected: PASS.

- [ ] **Step 6: Run the full package test suite (confirms existing confidentiality/tampering tests still pass)**

Run: `go test ./src/garlic/... -v 2>&1 | tail -150`
Expected: PASS — in particular `TestBuildOnionHopCannotDecryptAnotherHopsLayer`, `TestDecryptLayerRejectsTamperedCiphertext`, `TestDecryptLayerRejectsWrongKey` (Task 3), and everything from Tasks 1-3, all still green.

- [ ] **Step 7: Commit**

```bash
git add src/garlic/manager.go src/garlic/protocol.go src/garlic/linkability_test.go
git commit -m "garlic: chained per-hop ephemeral keys - fixes cross-hop ephemeral-key linkability"
```

---

### Task 5: Capability version bump (garlic-v1 → garlic-v2)

**Files:**
- Modify: `src/garlic/capability.go` (`CapabilityGarlicV1` → `CapabilityGarlicV2`, `SupportsGarlicV1` → `SupportsGarlicV2`)
- Modify: every call site (found via grep) in `src/garlic/*.go` and `src/garlic/*_test.go`

**Interfaces:**
- Consumes: nothing new.
- Produces: `CapabilityGarlicV2 = "garlic-v2"`, `(*CapabilityMessage) SupportsGarlicV2() bool` — used everywhere hop/rendezvous capability is checked (already used by Task 4's `linkability_test.go`).

- [ ] **Step 1: Write the failing test**

Update `src/garlic/capability_test.go`'s `TestSupportsGarlicV1` (rename and repoint):

```go
func TestSupportsGarlicV2(t *testing.T) {
	yes := &CapabilityMessage{Versions: []string{CapabilityGarlicV2}}
	if !yes.SupportsGarlicV2() {
		t.Error("SupportsGarlicV2() = false, want true")
	}
	no := &CapabilityMessage{Versions: []string{"something-else"}}
	if no.SupportsGarlicV2() {
		t.Error("SupportsGarlicV2() = true, want false")
	}
	empty := &CapabilityMessage{}
	if empty.SupportsGarlicV2() {
		t.Error("SupportsGarlicV2() on empty message = true, want false")
	}
}
```

Also update `TestCapabilityMessageMarshalUnmarshalRoundTrip`'s use of `CapabilityGarlicV1` to `CapabilityGarlicV2`.

- [ ] **Step 2: Run the test to verify it fails to compile**

Run: `go test ./src/garlic/... -run TestSupportsGarlicV2`
Expected: FAIL to compile — `CapabilityGarlicV2`/`SupportsGarlicV2` don't exist yet.

- [ ] **Step 3: Rename in `src/garlic/capability.go`**

```go
// CapabilityGarlicV2 is the capability string a Garlic-v2-capable node
// advertises. Bumped from garlic-v1 as part of the crypto hardening
// pass (per-hop ephemeral keys, wider CircuitID, new HKDF labels) - see
// docs/superpowers/specs/2026-08-09-garlic-crypto-hardening-design.md.
// There is deliberately no v1/v2 dual negotiation: a peer that doesn't
// advertise garlic-v2 is treated as legacy and never selected as a
// circuit hop or rendezvous point.
const CapabilityGarlicV2 = "garlic-v2"
```

```go
// SupportsGarlicV2 reports whether the message advertises
// CapabilityGarlicV2.
func (m *CapabilityMessage) SupportsGarlicV2() bool {
	for _, v := range m.Versions {
		if v == CapabilityGarlicV2 {
			return true
		}
	}
	return false
}
```

- [ ] **Step 4: Find and fix every remaining reference**

Run: `grep -rln 'CapabilityGarlicV1\|SupportsGarlicV1' src/garlic/ cmd/ src/config/`

For each match, replace `CapabilityGarlicV1` → `CapabilityGarlicV2` and `SupportsGarlicV1` → `SupportsGarlicV2`. Then:

Run: `go build ./... && go vet ./...`

Fix any remaining compile errors the same way until it's clean.

- [ ] **Step 5: Run the full package test suite**

Run: `go test ./src/garlic/... -v 2>&1 | tail -100`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add -A src/garlic src/config cmd
git commit -m "garlic: bump capability version to garlic-v2"
```

---

### Task 6: Ed25519 service signing identity

**Files:**
- Modify: `src/garlic/identity.go`
- Modify: `src/garlic/identity_test.go`
- Modify: `src/config/config.go` (`GarlicConfig.SigningPrivateKey`)
- Modify: `cmd/yggdrasil/main.go` (identity loading block)
- Test: `src/garlic/identity_test.go`, `src/config/config_test.go`

**Interfaces:**
- Consumes: nothing new.
- Produces: `Identity.SigningPublicKey ed25519.PublicKey`, `Identity.SigningPrivateKey ed25519.PrivateKey`, `NewIdentity() (*Identity, error)` (extended), `LoadIdentity(publicKey, privateKey, signingPublicKey, signingPrivateKeySeed []byte) (*Identity, error)` (extended), `LoadIdentityFromPrivateKeys(privateKey, signingPrivateKeySeed []byte) (*Identity, error)` (replaces `LoadIdentityFromPrivateKey`) — used by Task 8's `PublishService`/`LookupService`.

- [ ] **Step 1: Write the failing tests**

Replace `src/garlic/identity_test.go`'s contents:

```go
package garlic

import (
	"bytes"
	"testing"
)

func TestNewIdentityProducesDistinctKeypairs(t *testing.T) {
	id1, err := NewIdentity()
	if err != nil {
		t.Fatalf("NewIdentity returned error: %v", err)
	}
	id2, err := NewIdentity()
	if err != nil {
		t.Fatalf("NewIdentity returned error: %v", err)
	}
	if bytes.Equal(id1.PublicKey, id2.PublicKey) {
		t.Error("two identities got the same X25519 public key")
	}
	if bytes.Equal(id1.PrivateKey, id2.PrivateKey) {
		t.Error("two identities got the same X25519 private key")
	}
	if bytes.Equal(id1.SigningPublicKey, id2.SigningPublicKey) {
		t.Error("two identities got the same Ed25519 signing public key")
	}
}

func TestNewIdentitySigningKeyIsIndependentOfEncryptionKey(t *testing.T) {
	id, err := NewIdentity()
	if err != nil {
		t.Fatalf("NewIdentity returned error: %v", err)
	}
	// The two keypairs must not be trivially related - in particular,
	// the signing public key must not equal the X25519 public key (they
	// are different key types generated independently, never one
	// derived from the other).
	if bytes.Equal(id.PublicKey, id.SigningPublicKey) {
		t.Error("SigningPublicKey equals the X25519 PublicKey - keys are not independent")
	}
}

func TestLoadIdentityRoundTrip(t *testing.T) {
	id, err := NewIdentity()
	if err != nil {
		t.Fatalf("NewIdentity returned error: %v", err)
	}
	loaded, err := LoadIdentity(id.PublicKey, id.PrivateKey, id.SigningPublicKey, id.SigningPrivateKey.Seed())
	if err != nil {
		t.Fatalf("LoadIdentity returned error: %v", err)
	}
	if !bytes.Equal(loaded.PublicKey, id.PublicKey) {
		t.Errorf("PublicKey = %x, want %x", loaded.PublicKey, id.PublicKey)
	}
	if !bytes.Equal(loaded.PrivateKey, id.PrivateKey) {
		t.Errorf("PrivateKey = %x, want %x", loaded.PrivateKey, id.PrivateKey)
	}
	if !bytes.Equal(loaded.SigningPublicKey, id.SigningPublicKey) {
		t.Errorf("SigningPublicKey = %x, want %x", loaded.SigningPublicKey, id.SigningPublicKey)
	}
	if !bytes.Equal(loaded.SigningPrivateKey, id.SigningPrivateKey) {
		t.Errorf("SigningPrivateKey = %x, want %x", loaded.SigningPrivateKey, id.SigningPrivateKey)
	}
}

func TestLoadIdentityRejectsWrongSizePublicKey(t *testing.T) {
	id, _ := NewIdentity()
	if _, err := LoadIdentity(id.PublicKey[:16], id.PrivateKey, id.SigningPublicKey, id.SigningPrivateKey.Seed()); err == nil {
		t.Fatal("expected error for wrong-size public key, got nil")
	}
}

func TestLoadIdentityRejectsWrongSizePrivateKey(t *testing.T) {
	id, _ := NewIdentity()
	if _, err := LoadIdentity(id.PublicKey, id.PrivateKey[:16], id.SigningPublicKey, id.SigningPrivateKey.Seed()); err == nil {
		t.Fatal("expected error for wrong-size private key, got nil")
	}
}

func TestLoadIdentityRejectsWrongSizeSigningKey(t *testing.T) {
	id, _ := NewIdentity()
	if _, err := LoadIdentity(id.PublicKey, id.PrivateKey, id.SigningPublicKey[:16], id.SigningPrivateKey.Seed()); err == nil {
		t.Fatal("expected error for wrong-size signing public key, got nil")
	}
	if _, err := LoadIdentity(id.PublicKey, id.PrivateKey, id.SigningPublicKey, id.SigningPrivateKey.Seed()[:16]); err == nil {
		t.Fatal("expected error for wrong-size signing private key seed, got nil")
	}
}

func TestLoadIdentityFromPrivateKeysDerivesMatchingPublicKeys(t *testing.T) {
	id, err := NewIdentity()
	if err != nil {
		t.Fatalf("NewIdentity returned error: %v", err)
	}
	loaded, err := LoadIdentityFromPrivateKeys(id.PrivateKey, id.SigningPrivateKey.Seed())
	if err != nil {
		t.Fatalf("LoadIdentityFromPrivateKeys returned error: %v", err)
	}
	if !bytes.Equal(loaded.PublicKey, id.PublicKey) {
		t.Errorf("derived X25519 PublicKey = %x, want %x", loaded.PublicKey, id.PublicKey)
	}
	if !bytes.Equal(loaded.SigningPublicKey, id.SigningPublicKey) {
		t.Errorf("derived SigningPublicKey = %x, want %x", loaded.SigningPublicKey, id.SigningPublicKey)
	}
}

func TestLoadIdentityFromPrivateKeysRejectsWrongSize(t *testing.T) {
	id, _ := NewIdentity()
	if _, err := LoadIdentityFromPrivateKeys(make([]byte, 16), id.SigningPrivateKey.Seed()); err == nil {
		t.Fatal("expected error for wrong-size X25519 private key, got nil")
	}
	if _, err := LoadIdentityFromPrivateKeys(id.PrivateKey, make([]byte, 16)); err == nil {
		t.Fatal("expected error for wrong-size signing private key seed, got nil")
	}
}

func TestLoadIdentityFromPrivateKeysNeverDerivesX25519FromEd25519OrViceVersa(t *testing.T) {
	// The two private keys are independently generated - loading from
	// one must not somehow determine the other. Build an identity from
	// two *unrelated* keys and confirm both halves come out exactly as
	// given, not cross-derived.
	x25519ID, err := NewIdentity()
	if err != nil {
		t.Fatalf("NewIdentity returned error: %v", err)
	}
	ed25519ID, err := NewIdentity()
	if err != nil {
		t.Fatalf("NewIdentity returned error: %v", err)
	}
	loaded, err := LoadIdentityFromPrivateKeys(x25519ID.PrivateKey, ed25519ID.SigningPrivateKey.Seed())
	if err != nil {
		t.Fatalf("LoadIdentityFromPrivateKeys returned error: %v", err)
	}
	if !bytes.Equal(loaded.PublicKey, x25519ID.PublicKey) {
		t.Error("X25519 public key does not match the X25519 identity it was loaded from")
	}
	if !bytes.Equal(loaded.SigningPublicKey, ed25519ID.SigningPublicKey) {
		t.Error("Ed25519 signing public key does not match the Ed25519 identity it was loaded from")
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail to compile**

Run: `go test ./src/garlic/... -run TestNewIdentity`
Expected: FAIL to compile — `Identity.SigningPublicKey`/`SigningPrivateKey`, `LoadIdentityFromPrivateKeys` don't exist yet.

- [ ] **Step 3: Implement in `src/garlic/identity.go`**

Replace the file's contents:

```go
package garlic

// Long-term Garlic identities (Phase 8 of the roadmap, extended by the
// crypto hardening pass): a node's X25519 keypair (circuit-hop ECDH,
// unchanged from before) plus an independently generated Ed25519
// keypair used only to sign service descriptors (Part 3 of the
// hardening task - see docs/superpowers/specs/
// 2026-08-09-garlic-crypto-hardening-design.md section D). The two
// keypairs are always generated/loaded together but never derived one
// from the other - compromise of one type does not implicate the other,
// and there is no ad-hoc X25519-from-Ed25519 (or reverse) conversion
// anywhere in this file.

import (
	"crypto/ed25519"
	"errors"
)

var (
	ErrInvalidIdentityKeySize = errors.New("garlic: identity key has invalid size")
	ErrInvalidSigningKeySeed  = errors.New("garlic: signing private key seed has invalid size")
)

// Identity is a node's long-term Garlic identity: an X25519 keypair for
// circuit-hop ECDH, and an independent Ed25519 keypair for signing
// service descriptors.
type Identity struct {
	PublicKey  []byte // X25519
	PrivateKey []byte // X25519

	SigningPublicKey  ed25519.PublicKey
	SigningPrivateKey ed25519.PrivateKey
}

// NewIdentity generates a fresh long-term Garlic identity: a new X25519
// keypair and a new, independent Ed25519 signing keypair.
func NewIdentity() (*Identity, error) {
	pub, priv, err := GenerateKeypair()
	if err != nil {
		return nil, err
	}
	signingPub, signingPriv, err := ed25519.GenerateKey(nil)
	if err != nil {
		return nil, err
	}
	return &Identity{
		PublicKey:         pub,
		PrivateKey:        priv,
		SigningPublicKey:  signingPub,
		SigningPrivateKey: signingPriv,
	}, nil
}

// LoadIdentity reconstructs an Identity from previously-persisted key
// material, validating every size. signingPrivateKeySeed is the 32-byte
// Ed25519 seed (not the 64-byte expanded private key) - the same
// persisted-secret shape as the X25519 privateKey, for a consistent
// config format.
func LoadIdentity(publicKey, privateKey, signingPublicKey, signingPrivateKeySeed []byte) (*Identity, error) {
	if len(publicKey) != KeySize || len(privateKey) != KeySize {
		return nil, ErrInvalidIdentityKeySize
	}
	if len(signingPublicKey) != ed25519.PublicKeySize {
		return nil, ErrInvalidIdentityKeySize
	}
	if len(signingPrivateKeySeed) != ed25519.SeedSize {
		return nil, ErrInvalidSigningKeySeed
	}
	return &Identity{
		PublicKey:         append([]byte(nil), publicKey...),
		PrivateKey:        append([]byte(nil), privateKey...),
		SigningPublicKey:  append(ed25519.PublicKey(nil), signingPublicKey...),
		SigningPrivateKey: ed25519.NewKeyFromSeed(signingPrivateKeySeed),
	}, nil
}

// LoadIdentityFromPrivateKeys reconstructs an Identity from just the two
// private secrets, deriving both matching public keys. This is what
// lets config persist two 32-byte secrets (the X25519 private scalar
// and the Ed25519 seed) for a stable Garlic identity across restarts,
// the same way the node's main Yggdrasil identity only persists a
// private key. The two secrets are independently generated and loaded
// independently here - neither is ever derived from the other.
func LoadIdentityFromPrivateKeys(privateKey, signingPrivateKeySeed []byte) (*Identity, error) {
	if len(privateKey) != KeySize {
		return nil, ErrInvalidIdentityKeySize
	}
	if len(signingPrivateKeySeed) != ed25519.SeedSize {
		return nil, ErrInvalidSigningKeySeed
	}
	publicKey, err := DerivePublicKey(privateKey)
	if err != nil {
		return nil, err
	}
	signingPrivateKey := ed25519.NewKeyFromSeed(signingPrivateKeySeed)
	return &Identity{
		PublicKey:         publicKey,
		PrivateKey:        append([]byte(nil), privateKey...),
		SigningPublicKey:  signingPrivateKey.Public().(ed25519.PublicKey),
		SigningPrivateKey: signingPrivateKey,
	}, nil
}
```

- [ ] **Step 4: Rebuild and run the identity tests**

Run: `go build ./src/garlic/... && go test ./src/garlic/... -run TestNewIdentity -run TestLoadIdentity -v`
Expected: PASS.

- [ ] **Step 5: Wire config and `cmd/yggdrasil/main.go`**

In `src/config/config.go`, add a field to `GarlicConfig` right after `PrivateKey`:

```go
	PrivateKey         KeyBytes            `json:",omitempty" comment:"This node's long-term Garlic identity private key. Independent of\nyour main Yggdrasil PrivateKey above - compromise of one does not\nimplicate the other. If left unset while Enabled is true, a fresh\nkey is generated at startup and your Garlic identity will not be\nstable across restarts."`
	SigningPrivateKey  KeyBytes            `json:",omitempty" comment:"This node's Garlic service-descriptor signing key (Ed25519 seed,\n32 bytes). Independent of both PrivateKey above and your main\nYggdrasil key. Used only when publishing a Garlic service - see\ndocs/garlic-protocol.md section 6. If left unset while Enabled is\ntrue, a fresh key is generated at startup."`
```

In `cmd/yggdrasil/main.go`, replace the identity-loading block:

```go
			var identity *garlic.Identity
			if len(cfg.Garlic.PrivateKey) > 0 && len(cfg.Garlic.SigningPrivateKey) > 0 {
				if identity, err = garlic.LoadIdentityFromPrivateKeys(cfg.Garlic.PrivateKey, cfg.Garlic.SigningPrivateKey); err != nil {
					panic(err)
				}
			} else {
				if identity, err = garlic.NewIdentity(); err != nil {
					panic(err)
				}
				logger.Warnln("No Garlic.PrivateKey/SigningPrivateKey configured - generated ephemeral Garlic identity keys for this run only")
			}
```

- [ ] **Step 6: Add a config default/round-trip test**

Add to `src/config/config_test.go`:

```go
func TestGarlicConfigSigningPrivateKeyDefaultsEmpty(t *testing.T) {
	cfg := GenerateConfig()
	if len(cfg.Garlic.SigningPrivateKey) != 0 {
		t.Error("Garlic.SigningPrivateKey is non-empty by default, want empty (generated fresh at startup until configured)")
	}
}
```

- [ ] **Step 7: Rebuild everything and run the full suite**

Run: `go build ./... && go vet ./... && go test ./src/garlic/... ./src/config/... -v 2>&1 | tail -150`
Expected: PASS.

- [ ] **Step 8: Commit**

```bash
git add src/garlic/identity.go src/garlic/identity_test.go src/config/config.go src/config/config_test.go cmd/yggdrasil/main.go
git commit -m "garlic: add independent Ed25519 signing identity for service descriptors"
```

---

### Task 7: Signed `ServiceDescriptor` type

**Files:**
- Create: `src/garlic/descriptor.go`
- Create: `src/garlic/descriptor_test.go`

**Interfaces:**
- Consumes: `IntroPoint` (existing, `rendezvous.go`), `GID`/`ComputeGID` (existing, `gid.go`), `MaxIntroPoints`/`ErrTooManyIntroPoints` (existing, `rendezvous.go`), `maxCapabilityKeyLen`/`ErrCapabilityKeyTooLong` (existing, `capability.go`).
- Produces: `ServiceDescriptor`, `SignServiceDescriptor(...)`, `VerifyServiceDescriptor(...)` — used by Task 8's `rendezvous.go`/`manager.go` rewrite.

- [ ] **Step 1: Write the failing tests**

Create `src/garlic/descriptor_test.go`:

```go
package garlic

import (
	"bytes"
	"testing"
)

func testDescriptorIdentity(t *testing.T) *Identity {
	t.Helper()
	id, err := NewIdentity()
	if err != nil {
		t.Fatalf("NewIdentity returned error: %v", err)
	}
	return id
}

func TestSignAndVerifyServiceDescriptorRoundTrip(t *testing.T) {
	id := testDescriptorIdentity(t)
	serviceID := []byte("my-service")
	points := []IntroPoint{{NodeKey: []byte("intro-1")}, {NodeKey: []byte("intro-2")}}

	d, err := SignServiceDescriptor(id.SigningPublicKey, id.SigningPrivateKey, serviceID, points, 1000, 2000)
	if err != nil {
		t.Fatalf("SignServiceDescriptor returned error: %v", err)
	}
	gid := ComputeGID(id.SigningPublicKey, serviceID)

	if err := VerifyServiceDescriptor(d, gid, 1500); err != nil {
		t.Fatalf("VerifyServiceDescriptor returned error: %v", err)
	}
}

func TestVerifyServiceDescriptorRejectsWrongServiceKey(t *testing.T) {
	realID := testDescriptorIdentity(t)
	attackerID := testDescriptorIdentity(t)
	serviceID := []byte("victim-service")
	points := []IntroPoint{{NodeKey: []byte("attacker-controlled-intro")}}

	// The attacker signs a descriptor with their own key, but claims to
	// be publishing under the victim's GID by computing the GID from
	// their own key/serviceID pair - which necessarily produces a
	// *different* GID (self-certifying), not the victim's.
	forged, err := SignServiceDescriptor(attackerID.SigningPublicKey, attackerID.SigningPrivateKey, serviceID, points, 1000, 2000)
	if err != nil {
		t.Fatalf("SignServiceDescriptor returned error: %v", err)
	}
	victimGID := ComputeGID(realID.SigningPublicKey, serviceID)

	if err := VerifyServiceDescriptor(forged, victimGID, 1500); err == nil {
		t.Fatal("expected error verifying an attacker-signed descriptor against the victim's GID, got nil")
	}
}

func TestVerifyServiceDescriptorRejectsForgedSignature(t *testing.T) {
	id := testDescriptorIdentity(t)
	serviceID := []byte("svc")
	points := []IntroPoint{{NodeKey: []byte("intro-1")}}

	d, err := SignServiceDescriptor(id.SigningPublicKey, id.SigningPrivateKey, serviceID, points, 1000, 2000)
	if err != nil {
		t.Fatalf("SignServiceDescriptor returned error: %v", err)
	}
	// Tamper with an intro point after signing - a bogus rendezvous
	// substituting its own introduction point must be caught here.
	d.IntroPoints[0].NodeKey = []byte("attacker-substituted-intro")
	gid := ComputeGID(id.SigningPublicKey, serviceID)

	if err := VerifyServiceDescriptor(d, gid, 1500); err == nil {
		t.Fatal("expected error verifying a descriptor with a tampered intro point, got nil")
	}
}

func TestVerifyServiceDescriptorRejectsModifiedSignatureBytes(t *testing.T) {
	id := testDescriptorIdentity(t)
	serviceID := []byte("svc")
	d, err := SignServiceDescriptor(id.SigningPublicKey, id.SigningPrivateKey, serviceID, nil, 1000, 2000)
	if err != nil {
		t.Fatalf("SignServiceDescriptor returned error: %v", err)
	}
	d.Signature[0] ^= 0xFF
	gid := ComputeGID(id.SigningPublicKey, serviceID)

	if err := VerifyServiceDescriptor(d, gid, 1500); err == nil {
		t.Fatal("expected error verifying a descriptor with corrupted signature bytes, got nil")
	}
}

func TestVerifyServiceDescriptorRejectsExpired(t *testing.T) {
	id := testDescriptorIdentity(t)
	serviceID := []byte("svc")
	d, err := SignServiceDescriptor(id.SigningPublicKey, id.SigningPrivateKey, serviceID, nil, 1000, 2000)
	if err != nil {
		t.Fatalf("SignServiceDescriptor returned error: %v", err)
	}
	gid := ComputeGID(id.SigningPublicKey, serviceID)

	if err := VerifyServiceDescriptor(d, gid, 2001); err == nil {
		t.Fatal("expected error verifying an expired descriptor, got nil")
	}
}

func TestVerifyServiceDescriptorRejectsWrongGID(t *testing.T) {
	id := testDescriptorIdentity(t)
	d, err := SignServiceDescriptor(id.SigningPublicKey, id.SigningPrivateKey, []byte("svc-a"), nil, 1000, 2000)
	if err != nil {
		t.Fatalf("SignServiceDescriptor returned error: %v", err)
	}
	wrongGID := ComputeGID(id.SigningPublicKey, []byte("svc-b"))

	if err := VerifyServiceDescriptor(d, wrongGID, 1500); err == nil {
		t.Fatal("expected error verifying a valid descriptor against an unrelated GID, got nil")
	}
}

func TestSignServiceDescriptorRejectsExcessiveLifetime(t *testing.T) {
	id := testDescriptorIdentity(t)
	if _, err := SignServiceDescriptor(id.SigningPublicKey, id.SigningPrivateKey, []byte("svc"), nil, 1000, 1000+MaxDescriptorLifetime+1); err == nil {
		t.Fatal("expected error for a descriptor lifetime exceeding MaxDescriptorLifetime, got nil")
	}
}

func TestSignServiceDescriptorRejectsTooManyIntroPoints(t *testing.T) {
	id := testDescriptorIdentity(t)
	points := make([]IntroPoint, MaxIntroPoints+1)
	for i := range points {
		points[i] = IntroPoint{NodeKey: []byte{byte(i)}}
	}
	if _, err := SignServiceDescriptor(id.SigningPublicKey, id.SigningPrivateKey, []byte("svc"), points, 1000, 2000); err == nil {
		t.Fatal("expected error for too many introduction points, got nil")
	}
}

func TestSignedBytesExcludeSignatureField(t *testing.T) {
	id := testDescriptorIdentity(t)
	d, err := SignServiceDescriptor(id.SigningPublicKey, id.SigningPrivateKey, []byte("svc"), nil, 1000, 2000)
	if err != nil {
		t.Fatalf("SignServiceDescriptor returned error: %v", err)
	}
	signed, err := d.signedBytes()
	if err != nil {
		t.Fatalf("signedBytes returned error: %v", err)
	}
	if bytes.Contains(signed, d.Signature) {
		t.Error("signedBytes includes the Signature field itself - the signature would cover its own bytes")
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail to compile**

Run: `go test ./src/garlic/... -run TestSignAndVerifyServiceDescriptor`
Expected: FAIL to compile — `descriptor.go` doesn't exist yet.

- [ ] **Step 3: Implement `src/garlic/descriptor.go`**

```go
package garlic

// Signed service descriptors (Part 3 of the hardening task): the
// authenticated binding between a GID and the introduction points a
// client should trust for it. A Rendezvous implementation is untrusted
// storage/relay - it can withhold, reorder, or serve a stale copy, but
// it cannot forge a descriptor for a GID it doesn't hold the signing
// key for, because the GID is derived from the signing public key
// (self-certifying, ComputeGID) and the descriptor is Ed25519-signed by
// that same key. See docs/superpowers/specs/
// 2026-08-09-garlic-crypto-hardening-design.md section D for the full
// rationale, in particular what is and isn't part of the signed
// payload - no rendezvous-added metadata is ever signed.

import (
	"crypto/ed25519"
	"encoding/binary"
	"errors"
)

const (
	maxServiceIDSize = 64
	// MaxDescriptorLifetime bounds ExpiresAt-PublishedAt (seconds) so a
	// service can't mint a descriptor "valid" for an unreasonable span.
	MaxDescriptorLifetime = 7 * 24 * 60 * 60
)

const ServiceDescriptorVersion1 uint8 = 1

var (
	ErrServiceIDTooLarge            = errors.New("garlic: service ID exceeds maximum size")
	ErrUnsupportedDescriptorVersion = errors.New("garlic: unsupported service descriptor version")
	ErrInvalidSigningKeySize        = errors.New("garlic: invalid signing public key size")
	ErrDescriptorLifetimeTooLong    = errors.New("garlic: service descriptor lifetime exceeds maximum")
	ErrInvalidDescriptorSignature   = errors.New("garlic: service descriptor signature invalid")
	ErrDescriptorGIDMismatch        = errors.New("garlic: service descriptor does not match requested GID")
	ErrDescriptorExpired            = errors.New("garlic: service descriptor expired")
)

// ServiceDescriptor is the signed, self-certifying binding between a
// service's GID and its current introduction points.
type ServiceDescriptor struct {
	Version          uint8
	ServicePublicKey ed25519.PublicKey // GID = ComputeGID(ServicePublicKey, ServiceID)
	ServiceID        []byte
	IntroPoints      []IntroPoint
	PublishedAt      uint64
	ExpiresAt        uint64
	Signature        []byte // ed25519, over signedBytes()
}

// signedBytes returns the descriptor's canonical encoding with
// Signature omitted - exactly what SignServiceDescriptor signs and what
// VerifyServiceDescriptor re-derives from a received descriptor to
// check the signature against. No field the rendezvous itself might add
// (receipt timestamps, sequence numbers, storage hints) is ever part of
// this encoding.
func (d *ServiceDescriptor) signedBytes() ([]byte, error) {
	if len(d.ServicePublicKey) != ed25519.PublicKeySize {
		return nil, ErrInvalidSigningKeySize
	}
	if len(d.ServiceID) > maxServiceIDSize {
		return nil, ErrServiceIDTooLarge
	}
	if len(d.IntroPoints) > MaxIntroPoints {
		return nil, ErrTooManyIntroPoints
	}

	buf := []byte{d.Version}
	buf = append(buf, d.ServicePublicKey...)
	buf = binary.BigEndian.AppendUint32(buf, uint32(len(d.ServiceID)))
	buf = append(buf, d.ServiceID...)
	buf = binary.BigEndian.AppendUint32(buf, uint32(len(d.IntroPoints)))
	for _, p := range d.IntroPoints {
		if len(p.NodeKey) > maxCapabilityKeyLen {
			return nil, ErrCapabilityKeyTooLong
		}
		buf = append(buf, byte(len(p.NodeKey)))
		buf = append(buf, p.NodeKey...)
	}
	buf = binary.BigEndian.AppendUint64(buf, d.PublishedAt)
	buf = binary.BigEndian.AppendUint64(buf, d.ExpiresAt)
	return buf, nil
}

// SignServiceDescriptor builds and signs a ServiceDescriptor for
// serviceID/introPoints, valid from publishedAt to expiresAt (span
// capped at MaxDescriptorLifetime), using signingPrivateKey.
func SignServiceDescriptor(signingPublicKey ed25519.PublicKey, signingPrivateKey ed25519.PrivateKey, serviceID []byte, introPoints []IntroPoint, publishedAt, expiresAt uint64) (*ServiceDescriptor, error) {
	if expiresAt < publishedAt || expiresAt-publishedAt > MaxDescriptorLifetime {
		return nil, ErrDescriptorLifetimeTooLong
	}
	d := &ServiceDescriptor{
		Version:          ServiceDescriptorVersion1,
		ServicePublicKey: signingPublicKey,
		ServiceID:        serviceID,
		IntroPoints:      introPoints,
		PublishedAt:      publishedAt,
		ExpiresAt:        expiresAt,
	}
	toSign, err := d.signedBytes()
	if err != nil {
		return nil, err
	}
	d.Signature = ed25519.Sign(signingPrivateKey, toSign)
	return d, nil
}

// VerifyServiceDescriptor checks that d is a validly-signed descriptor
// for gid, not expired as of now. This is the client-side trust
// boundary: Rendezvous.Lookup returns d unverified (the rendezvous is
// untrusted), and every caller of Lookup must run the result through
// this before trusting d.IntroPoints.
func VerifyServiceDescriptor(d *ServiceDescriptor, gid GID, now uint64) error {
	if d.Version != ServiceDescriptorVersion1 {
		return ErrUnsupportedDescriptorVersion
	}
	if ComputeGID(d.ServicePublicKey, d.ServiceID) != gid {
		return ErrDescriptorGIDMismatch
	}
	toVerify, err := d.signedBytes()
	if err != nil {
		return err
	}
	if !ed25519.Verify(d.ServicePublicKey, toVerify, d.Signature) {
		return ErrInvalidDescriptorSignature
	}
	if now > d.ExpiresAt {
		return ErrDescriptorExpired
	}
	return nil
}
```

- [ ] **Step 4: Rebuild and run**

Run: `go build ./src/garlic/... && go test ./src/garlic/... -run 'TestSign|TestVerify' -v`
Expected: PASS, all descriptor tests green.

- [ ] **Step 5: Commit**

```bash
git add src/garlic/descriptor.go src/garlic/descriptor_test.go
git commit -m "garlic: add signed ServiceDescriptor type"
```

---

### Task 8: Wire descriptor signing into Rendezvous / PublishService / LookupService

**Files:**
- Modify: `src/garlic/rendezvous.go`
- Modify: `src/garlic/rendezvous_test.go`
- Modify: `src/garlic/manager.go` (`PublishService`, `LookupService`)
- Modify: `src/garlic/manager_test.go` (add coverage if any existing test constructs a `Rendezvous`/calls `PublishService`/`LookupService` directly — check via grep first)

**Interfaces:**
- Consumes: `ServiceDescriptor`, `SignServiceDescriptor`, `VerifyServiceDescriptor` (Task 7); `Identity.SigningPublicKey`/`SigningPrivateKey` (Task 6).
- Produces: `Rendezvous.Publish(gid GID, descriptor *ServiceDescriptor) error`, `Rendezvous.Lookup(gid GID) (*ServiceDescriptor, error)` — `PublishService`/`LookupService`'s own signatures on `*Garlic` are unchanged, so `src/garlic/admin.go` needs no changes at all (verified: it only calls these two methods and formats their existing return types).

- [ ] **Step 1: Write the failing tests**

Replace `src/garlic/rendezvous_test.go`'s contents:

```go
package garlic

import (
	"bytes"
	"testing"
)

func testDescriptor(t *testing.T, id *Identity, serviceID string, points []IntroPoint, publishedAt, expiresAt uint64) (*ServiceDescriptor, GID) {
	t.Helper()
	d, err := SignServiceDescriptor(id.SigningPublicKey, id.SigningPrivateKey, []byte(serviceID), points, publishedAt, expiresAt)
	if err != nil {
		t.Fatalf("SignServiceDescriptor returned error: %v", err)
	}
	return d, ComputeGID(id.SigningPublicKey, []byte(serviceID))
}

func TestStaticRendezvousPublishThenLookup(t *testing.T) {
	id, err := NewIdentity()
	if err != nil {
		t.Fatalf("NewIdentity returned error: %v", err)
	}
	r := NewStaticRendezvous()
	points := []IntroPoint{{NodeKey: []byte("intro-1")}, {NodeKey: []byte("intro-2")}}
	d, gid := testDescriptor(t, id, "svc", points, 1000, 2000)

	if err := r.Publish(gid, d); err != nil {
		t.Fatalf("Publish returned error: %v", err)
	}
	got, err := r.Lookup(gid)
	if err != nil {
		t.Fatalf("Lookup returned error: %v", err)
	}
	if len(got.IntroPoints) != len(points) {
		t.Fatalf("Lookup returned %d intro points, want %d", len(got.IntroPoints), len(points))
	}
	for i := range points {
		if !bytes.Equal(got.IntroPoints[i].NodeKey, points[i].NodeKey) {
			t.Errorf("intro point %d = %q, want %q", i, got.IntroPoints[i].NodeKey, points[i].NodeKey)
		}
	}
	if err := VerifyServiceDescriptor(got, gid, 1500); err != nil {
		t.Errorf("VerifyServiceDescriptor on the round-tripped descriptor returned error: %v", err)
	}
}

func TestStaticRendezvousLookupUnpublishedReturnsError(t *testing.T) {
	r := NewStaticRendezvous()
	id, _ := NewIdentity()
	gid := ComputeGID(id.SigningPublicKey, []byte("svc"))
	if _, err := r.Lookup(gid); err == nil {
		t.Fatal("expected error looking up an unpublished GID, got nil")
	}
}

func TestStaticRendezvousPublishOverwritesPreviousEntry(t *testing.T) {
	id, _ := NewIdentity()
	r := NewStaticRendezvous()
	old, gid := testDescriptor(t, id, "svc", []IntroPoint{{NodeKey: []byte("old")}}, 1000, 2000)
	if err := r.Publish(gid, old); err != nil {
		t.Fatalf("first Publish returned error: %v", err)
	}
	fresh, _ := testDescriptor(t, id, "svc", []IntroPoint{{NodeKey: []byte("new")}}, 1500, 2500)
	if err := r.Publish(gid, fresh); err != nil {
		t.Fatalf("second Publish returned error: %v", err)
	}
	got, err := r.Lookup(gid)
	if err != nil {
		t.Fatalf("Lookup returned error: %v", err)
	}
	if len(got.IntroPoints) != 1 || !bytes.Equal(got.IntroPoints[0].NodeKey, []byte("new")) {
		t.Fatalf("Lookup = %+v, want a single intro point %q", got.IntroPoints, "new")
	}
}

func TestStaticRendezvousPublishRejectsTooManyIntroPoints(t *testing.T) {
	id, _ := NewIdentity()
	r := NewStaticRendezvous()
	points := make([]IntroPoint, MaxIntroPoints+1)
	for i := range points {
		points[i] = IntroPoint{NodeKey: []byte{byte(i)}}
	}
	d := &ServiceDescriptor{ServicePublicKey: id.SigningPublicKey, ServiceID: []byte("svc"), IntroPoints: points}
	gid := ComputeGID(id.SigningPublicKey, []byte("svc"))
	if err := r.Publish(gid, d); err == nil {
		t.Fatal("expected error publishing more than MaxIntroPoints, got nil")
	}
}

// TestStaticRendezvousServesStaleDescriptorUncritically documents the
// deliberate trust boundary: StaticRendezvous is untrusted storage, so
// it hands back exactly what was published even after ExpiresAt has
// passed - enforcement of freshness is the *client's* job
// (VerifyServiceDescriptor), not the rendezvous's. This is what makes
// the "malicious/buggy rendezvous serves a stale descriptor" scenario
// (Part 3 of the hardening task) actually testable end to end.
func TestStaticRendezvousServesStaleDescriptorUncritically(t *testing.T) {
	id, _ := NewIdentity()
	r := NewStaticRendezvous()
	d, gid := testDescriptor(t, id, "svc", []IntroPoint{{NodeKey: []byte("intro")}}, 1000, 2000)
	if err := r.Publish(gid, d); err != nil {
		t.Fatalf("Publish returned error: %v", err)
	}

	got, err := r.Lookup(gid)
	if err != nil {
		t.Fatalf("Lookup on a stale-but-present entry returned error: %v, want the entry returned uncritically", err)
	}
	if err := VerifyServiceDescriptor(got, gid, 9999); err == nil {
		t.Fatal("expected the client's own VerifyServiceDescriptor to reject the now-expired descriptor, got nil")
	}
}

// Rendezvous is implemented by StaticRendezvous; this is a compile-time
// check that the interface and implementation stay in sync.
var _ Rendezvous = (*StaticRendezvous)(nil)
```

Add to a new or existing manager-level test (append to `src/garlic/manager_test.go`):

```go
func TestPublishServiceThenLookupServiceRoundTrips(t *testing.T) {
	id, err := NewIdentity()
	if err != nil {
		t.Fatalf("NewIdentity returned error: %v", err)
	}
	g := &Garlic{
		identity:   id,
		cfg:        DefaultConfig(),
		rendezvous: NewStaticRendezvous(),
	}
	points := []IntroPoint{{NodeKey: []byte("intro-1")}}

	gid, err := g.PublishService([]byte("svc"), points, time.Hour)
	if err != nil {
		t.Fatalf("PublishService returned error: %v", err)
	}
	got, err := g.LookupService(gid)
	if err != nil {
		t.Fatalf("LookupService returned error: %v", err)
	}
	if len(got) != 1 || !bytes.Equal(got[0].NodeKey, []byte("intro-1")) {
		t.Fatalf("LookupService = %+v, want one intro point %q", got, "intro-1")
	}
}

func TestLookupServiceRejectsBogusRendezvousResponse(t *testing.T) {
	id, err := NewIdentity()
	if err != nil {
		t.Fatalf("NewIdentity returned error: %v", err)
	}
	attacker, err := NewIdentity()
	if err != nil {
		t.Fatalf("NewIdentity returned error: %v", err)
	}
	rendezvous := NewStaticRendezvous()
	g := &Garlic{identity: id, cfg: DefaultConfig(), rendezvous: rendezvous}

	gid, err := g.PublishService([]byte("svc"), []IntroPoint{{NodeKey: []byte("real-intro")}}, time.Hour)
	if err != nil {
		t.Fatalf("PublishService returned error: %v", err)
	}

	// A malicious rendezvous overwrites the entry with an
	// attacker-signed descriptor claiming attacker-controlled intro
	// points - but it cannot make this validate against the real GID,
	// since the GID is derived from the real service's signing key.
	forged, err := SignServiceDescriptor(attacker.SigningPublicKey, attacker.SigningPrivateKey, []byte("svc"), []IntroPoint{{NodeKey: []byte("attacker-intro")}}, 0, uint64(time.Now().Add(time.Hour).Unix()))
	if err != nil {
		t.Fatalf("SignServiceDescriptor returned error: %v", err)
	}
	if err := rendezvous.Publish(gid, forged); err != nil {
		t.Fatalf("Publish returned error: %v", err)
	}

	if _, err := g.LookupService(gid); err == nil {
		t.Fatal("expected LookupService to reject the bogus rendezvous response, got nil")
	}
}
```

(`manager_test.go` already imports `"bytes"` and `"time"` — check its import block; add `"bytes"` if not already present.)

- [ ] **Step 2: Run the tests to verify they fail to compile**

Run: `go test ./src/garlic/... -run 'TestStaticRendezvous|TestPublishService|TestLookupService'`
Expected: FAIL to compile — `Rendezvous.Publish`/`Lookup` still take/return `[]IntroPoint`.

- [ ] **Step 3: Rewrite `src/garlic/rendezvous.go`**

```go
package garlic

// Rendezvous abstraction (Phase 9 of the roadmap, extended by Part 3 of
// the crypto hardening pass): endpoint discovery decoupled from circuit
// construction. A Rendezvous implementation is untrusted storage/relay
// - it can withhold, reorder, or serve a stale descriptor, but every
// descriptor it hands back is independently verified by the caller
// (VerifyServiceDescriptor, descriptor.go) before its IntroPoints are
// trusted. A DHT-backed implementation is future work behind the same
// interface.

import (
	"errors"
	"sync"
)

// MaxIntroPoints bounds how many introduction points a single
// descriptor may list, so a remote publisher can't make a Rendezvous
// implementation store unbounded per-GID state.
const MaxIntroPoints = 16

var (
	ErrGIDNotFound        = errors.New("garlic: GID not found")
	ErrTooManyIntroPoints = errors.New("garlic: too many introduction points")
)

// IntroPoint is one introduction point for a Garlic service: a
// Garlic-capable node willing to forward circuit-extension requests to
// the service on its behalf, without itself being the service's
// Yggdrasil address.
type IntroPoint struct {
	NodeKey []byte
}

// Rendezvous maps Garlic Service IDs (GID) to their current signed
// service descriptor.
type Rendezvous interface {
	// Publish advertises descriptor as gid's current service descriptor.
	// A later Publish for the same gid replaces the previous one.
	Publish(gid GID, descriptor *ServiceDescriptor) error
	// Lookup returns the currently-published descriptor for gid,
	// unverified - the caller must run it through
	// VerifyServiceDescriptor before trusting its IntroPoints. Returns
	// ErrGIDNotFound if nothing has been published for gid.
	Lookup(gid GID) (*ServiceDescriptor, error)
}

// StaticRendezvous is an in-memory Rendezvous implementation, suitable
// for local testing and small statically-configured deployments
// independent of any distributed directory. It performs no verification
// and no expiry enforcement of its own - see Lookup's doc comment; it
// is deliberately as "dumb" as a real untrusted rendezvous would be, so
// tests against it exercise the actual client-side trust boundary. It
// is safe for concurrent use.
type StaticRendezvous struct {
	mu      sync.Mutex
	entries map[GID]*ServiceDescriptor
}

// NewStaticRendezvous returns an empty StaticRendezvous.
func NewStaticRendezvous() *StaticRendezvous {
	return &StaticRendezvous{entries: make(map[GID]*ServiceDescriptor)}
}

func (s *StaticRendezvous) Publish(gid GID, descriptor *ServiceDescriptor) error {
	if len(descriptor.IntroPoints) > MaxIntroPoints {
		return ErrTooManyIntroPoints
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.entries[gid] = descriptor
	return nil
}

// Lookup returns whatever is currently stored for gid, including a
// descriptor whose ExpiresAt has already passed - StaticRendezvous does
// not check expiry itself (see the type's doc comment). Callers must
// verify via VerifyServiceDescriptor.
func (s *StaticRendezvous) Lookup(gid GID) (*ServiceDescriptor, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	d, ok := s.entries[gid]
	if !ok {
		return nil, ErrGIDNotFound
	}
	return d, nil
}
```

- [ ] **Step 4: Update `PublishService`/`LookupService` in `src/garlic/manager.go`**

```go
// PublishService signs and advertises this node's identity as reachable
// at introPoints for serviceID, returning the resulting GID. The
// descriptor is signed with this node's Garlic signing identity
// (Identity.SigningPrivateKey), never the X25519 circuit-hop key.
func (g *Garlic) PublishService(serviceID []byte, introPoints []IntroPoint, ttl time.Duration) (GID, error) {
	gid := ComputeGID(g.identity.SigningPublicKey, serviceID)
	now := uint64(time.Now().Unix())
	descriptor, err := SignServiceDescriptor(g.identity.SigningPublicKey, g.identity.SigningPrivateKey, serviceID, introPoints, now, now+uint64(ttl.Seconds()))
	if err != nil {
		return GID{}, err
	}
	if err := g.rendezvous.Publish(gid, descriptor); err != nil {
		return GID{}, err
	}
	return gid, nil
}

// LookupService returns the currently-published introduction points for
// gid, after verifying the descriptor the rendezvous returned actually
// matches gid, is validly signed, and is not expired (VerifyServiceDescriptor)
// - a malicious or buggy rendezvous cannot make this return
// attacker-controlled introduction points for a GID it doesn't hold the
// signing key for.
func (g *Garlic) LookupService(gid GID) ([]IntroPoint, error) {
	descriptor, err := g.rendezvous.Lookup(gid)
	if err != nil {
		return nil, err
	}
	if err := VerifyServiceDescriptor(descriptor, gid, uint64(time.Now().Unix())); err != nil {
		return nil, err
	}
	return descriptor.IntroPoints, nil
}
```

- [ ] **Step 5: Rebuild and run**

Run: `go build ./src/garlic/... && go test ./src/garlic/... -v 2>&1 | tail -150`
Expected: PASS, no changes needed in `src/garlic/admin.go` (confirm with `git status`/`git diff src/garlic/admin.go` — should show no changes from this task).

- [ ] **Step 6: Commit**

```bash
git add src/garlic/rendezvous.go src/garlic/rendezvous_test.go src/garlic/manager.go src/garlic/manager_test.go
git commit -m "garlic: authenticate service descriptors end to end (Rendezvous, PublishService, LookupService)"
```

---

### Task 9: Circuit ID collision guard + remaining replay/direction tests

**Files:**
- Modify: `src/garlic/circuit_manager.go` (`insertCircuitLocked`, `ErrCircuitIDCollision`)
- Modify: `src/garlic/circuit_manager_test.go`
- Modify: `src/garlic/relaystate_test.go`

**Interfaces:**
- Consumes: `CircuitManager` (Task 1's type, unchanged shape).
- Produces: `ErrCircuitIDCollision`, `(*CircuitManager) insertCircuitLocked(c *Circuit) error` (package-private, test-only visibility beyond `Add`).

- [ ] **Step 1: Write the failing tests**

Add to `src/garlic/circuit_manager_test.go`:

```go
func TestCircuitManagerInsertCircuitLockedRejectsIDCollision(t *testing.T) {
	m := NewCircuitManager(testManagerConfig())
	id := testCircuitID(7)
	first := &Circuit{ID: id}
	if err := m.insertCircuitLocked(first); err != nil {
		t.Fatalf("first insert returned error: %v", err)
	}
	second := &Circuit{ID: id}
	if err := m.insertCircuitLocked(second); err == nil {
		t.Fatal("expected error inserting a circuit with a colliding ID, got nil")
	}
	if got := m.circuits[id]; got != first {
		t.Fatal("colliding insert replaced the original tracked circuit")
	}
}
```

Add to `src/garlic/relaystate_test.go`:

```go
func TestRelayCircuitStateDifferentCircuitsHaveIndependentReplayWindows(t *testing.T) {
	s := newRelayCircuitState(1024)
	wA, _ := s.replayWindowFor(testCircuitID(1))
	wB, _ := s.replayWindowFor(testCircuitID(2))

	if !wA.CheckAndSet(5) {
		t.Fatal("first CheckAndSet(5) on circuit A = false, want true")
	}
	// The same counter value on a *different* circuit ID must be
	// unaffected - replay state is scoped per circuit, not global, so
	// two circuits never accidentally share replay-window context.
	if !wB.CheckAndSet(5) {
		t.Fatal("CheckAndSet(5) on circuit B = false, want true (independent window from circuit A)")
	}
}

// TestRelayCircuitStateEvictedWindowStartsFreshOnReuse documents the
// deliberate bounded-memory tradeoff (Part 2 of the hardening task,
// "replay cache eviction"): once a circuit's replay window has been
// evicted (expireStale), a later message claiming that same circuit ID
// gets a *fresh* window, not a resurrected one - this relay has no
// memory of what counters it saw before eviction. This is expected
// behavior of a capacity-bounded cache, not a defect - callers must not
// assume eviction-proof replay protection.
func TestRelayCircuitStateEvictedWindowStartsFreshOnReuse(t *testing.T) {
	s := newRelayCircuitState(1024)
	id := testCircuitID(1)
	w, _ := s.replayWindowFor(id)
	if !w.CheckAndSet(5) {
		t.Fatal("CheckAndSet(5) = false, want true")
	}
	time.Sleep(5 * time.Millisecond)
	if n := s.expireStale(time.Millisecond); n != 1 {
		t.Fatalf("expireStale removed %d, want 1", n)
	}

	w2, ok := s.replayWindowFor(id)
	if !ok {
		t.Fatal("replayWindowFor after eviction ok = false, want true")
	}
	if !w2.CheckAndSet(5) {
		t.Fatal("CheckAndSet(5) on the post-eviction window = false, want true (a fresh window, not resurrected replay state)")
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./src/garlic/... -run 'TestCircuitManagerInsertCircuitLocked|TestRelayCircuitStateDifferentCircuits|TestRelayCircuitStateEvictedWindow'`
Expected: `TestCircuitManagerInsertCircuitLockedRejectsIDCollision` fails to compile (`insertCircuitLocked` doesn't exist); the two `relaystate_test.go` additions should already pass against the existing implementation (confirming current behavior) but run them anyway to establish the baseline.

- [ ] **Step 3: Implement the collision guard in `src/garlic/circuit_manager.go`**

Add the error:

```go
var (
	ErrTooManyCircuits        = errors.New("garlic: too many circuits")
	ErrTooManyCircuitsForPeer = errors.New("garlic: too many circuits through this peer")
	ErrCircuitIDCollision     = errors.New("garlic: circuit ID collision")
)
```

Add the helper and use it from `Add` (replace the direct map write):

```go
// insertCircuitLocked inserts c into m.circuits if its ID is not
// already tracked. Caller must hold m.mu. Separated from Add so the
// collision path itself - vanishingly unlikely with a 128-bit random
// ID, but not something to silently paper over if it ever happens - is
// directly testable without needing to force randomCircuitID to
// collide.
func (m *CircuitManager) insertCircuitLocked(c *Circuit) error {
	if _, exists := m.circuits[c.ID]; exists {
		return ErrCircuitIDCollision
	}
	m.circuits[c.ID] = c
	return nil
}
```

In `Add`, replace:

```go
	c, err := NewCircuit(hops, lifetime, maxPackets, maxBytes)
	if err != nil {
		return nil, err
	}
	m.circuits[c.ID] = c
	m.perPeer[peer]++
	return c, nil
```

with:

```go
	c, err := NewCircuit(hops, lifetime, maxPackets, maxBytes)
	if err != nil {
		return nil, err
	}
	if err := m.insertCircuitLocked(c); err != nil {
		return nil, err
	}
	m.perPeer[peer]++
	return c, nil
```

- [ ] **Step 4: Rebuild and run**

Run: `go build ./src/garlic/... && go test ./src/garlic/... -v 2>&1 | tail -150`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add src/garlic/circuit_manager.go src/garlic/circuit_manager_test.go src/garlic/relaystate_test.go
git commit -m "garlic: guard against circuit ID collisions; document replay-cache eviction tradeoff"
```

---

### Task 10: Fuzz coverage for the new parsers

**Files:**
- Modify: `src/garlic/fuzz_test.go`

**Interfaces:**
- Consumes: `unmarshalLayerPlaintext` (Task 3, unexported — fuzz target lives in the same package), `ServiceDescriptor`/marshal path (Task 7 — needs a raw-bytes entry point; see Step 3).

- [ ] **Step 1: Add `FuzzLayerPlaintextUnmarshal`**

Append to `src/garlic/fuzz_test.go`:

```go
func FuzzLayerPlaintextUnmarshal(f *testing.F) {
	valid := &LayerPlaintext{
		NextHop:          []byte("next-hop-key"),
		NextHopEphemeral: make([]byte, KeySize),
		Inner:            []byte("inner ciphertext"),
	}
	validBytes, _ := valid.marshal()
	f.Add(validBytes)
	f.Add([]byte{})
	f.Add([]byte{0, 0, 0, 0})       // empty next_hop, truncated before the flag byte
	f.Add([]byte{0, 0, 0, 0, 1})    // flag says "ephemeral present" but provides none
	f.Add([]byte{0, 0, 0, 0, 2})    // invalid flag byte
	f.Fuzz(func(t *testing.T, data []byte) {
		_, _ = unmarshalLayerPlaintext(data)
	})
}
```

- [ ] **Step 2: Add a raw-bytes `ServiceDescriptor` unmarshal path, then a fuzz target**

`ServiceDescriptor` currently only has `signedBytes()` (private, used for signing/verification, not general unmarshal — it has no independent "parse an untrusted byte slice into a `ServiceDescriptor`" entry point, because in the current design a descriptor only ever arrives as a Go struct from a `Rendezvous.Lookup` call, not as raw wire bytes `src/garlic` itself parses). Confirm this by checking: does anything in `src/garlic` deserialize a `ServiceDescriptor` from `[]byte`? (No — `Rendezvous` is an in-process interface, `StaticRendezvous` stores/returns the struct directly, never bytes.) Given that, add fuzzing at the layer that *does* parse untrusted bytes into descriptor-shaped fields: `d.signedBytes()` is the encoder; add a corresponding raw-bytes decoder `unmarshalServiceDescriptorFields` used only by this fuzz target's harness, mirroring the encoding exactly, so the fuzz target exercises the same bounds-checking discipline as every other parser in this package even though production code doesn't need a decoder path yet.

Add to `src/garlic/descriptor.go` (after `signedBytes`):

```go
// unmarshalServiceDescriptorFields parses the signedBytes() encoding
// back into field values, without a Signature (there is none in that
// encoding) or version-specific dispatch beyond checking Version. This
// exists for fuzz coverage of the encoding's bounds-checking - nothing
// in this package currently deserializes a ServiceDescriptor from raw
// bytes in production (descriptors flow through Rendezvous as Go
// structs, not wire bytes), but the encoding shares the same untrusted-
// length-prefix shape as every parser in this package that does, so it
// gets the same fuzz discipline.
func unmarshalServiceDescriptorFields(data []byte) (*ServiceDescriptor, error) {
	if len(data) < 1+ed25519.PublicKeySize {
		return nil, ErrDescriptorTruncated
	}
	d := &ServiceDescriptor{Version: data[0]}
	if d.Version != ServiceDescriptorVersion1 {
		return nil, ErrUnsupportedDescriptorVersion
	}
	rest := data[1:]
	d.ServicePublicKey = append(ed25519.PublicKey(nil), rest[:ed25519.PublicKeySize]...)
	rest = rest[ed25519.PublicKeySize:]

	if len(rest) < 4 {
		return nil, ErrDescriptorTruncated
	}
	serviceIDLen := binary.BigEndian.Uint32(rest[:4])
	rest = rest[4:]
	if serviceIDLen > maxServiceIDSize {
		return nil, ErrServiceIDTooLarge
	}
	if uint64(serviceIDLen) > uint64(len(rest)) {
		return nil, ErrDescriptorTruncated
	}
	d.ServiceID = append([]byte(nil), rest[:serviceIDLen]...)
	rest = rest[serviceIDLen:]

	if len(rest) < 4 {
		return nil, ErrDescriptorTruncated
	}
	pointCount := binary.BigEndian.Uint32(rest[:4])
	rest = rest[4:]
	if pointCount > MaxIntroPoints {
		return nil, ErrTooManyIntroPoints
	}
	d.IntroPoints = make([]IntroPoint, 0, pointCount)
	for range pointCount {
		if len(rest) < 1 {
			return nil, ErrDescriptorTruncated
		}
		n := int(rest[0])
		rest = rest[1:]
		if n > maxCapabilityKeyLen {
			return nil, ErrCapabilityKeyTooLong
		}
		if n > len(rest) {
			return nil, ErrDescriptorTruncated
		}
		d.IntroPoints = append(d.IntroPoints, IntroPoint{NodeKey: append([]byte(nil), rest[:n]...)})
		rest = rest[n:]
	}

	if len(rest) < 16 {
		return nil, ErrDescriptorTruncated
	}
	d.PublishedAt = binary.BigEndian.Uint64(rest[:8])
	d.ExpiresAt = binary.BigEndian.Uint64(rest[8:16])
	return d, nil
}
```

Add the new error to the existing `var (...)` block in `descriptor.go`:

```go
	ErrDescriptorTruncated = errors.New("garlic: service descriptor truncated")
```

Add a round-trip test to `descriptor_test.go` proving the decoder matches the encoder (this is the "test" for this new function, run before the fuzz target):

```go
func TestUnmarshalServiceDescriptorFieldsRoundTripsSignedBytes(t *testing.T) {
	id := testDescriptorIdentity(t)
	d, err := SignServiceDescriptor(id.SigningPublicKey, id.SigningPrivateKey, []byte("svc"), []IntroPoint{{NodeKey: []byte("intro")}}, 1000, 2000)
	if err != nil {
		t.Fatalf("SignServiceDescriptor returned error: %v", err)
	}
	encoded, err := d.signedBytes()
	if err != nil {
		t.Fatalf("signedBytes returned error: %v", err)
	}
	got, err := unmarshalServiceDescriptorFields(encoded)
	if err != nil {
		t.Fatalf("unmarshalServiceDescriptorFields returned error: %v", err)
	}
	if got.Version != d.Version || !bytes.Equal(got.ServicePublicKey, d.ServicePublicKey) ||
		!bytes.Equal(got.ServiceID, d.ServiceID) || got.PublishedAt != d.PublishedAt || got.ExpiresAt != d.ExpiresAt {
		t.Fatalf("round-tripped fields = %+v, want to match %+v", got, d)
	}
	if len(got.IntroPoints) != 1 || !bytes.Equal(got.IntroPoints[0].NodeKey, []byte("intro")) {
		t.Fatalf("round-tripped IntroPoints = %+v", got.IntroPoints)
	}
}
```

Add the fuzz target to `src/garlic/fuzz_test.go`:

```go
func FuzzServiceDescriptorFieldsUnmarshal(f *testing.F) {
	id, err := NewIdentity()
	if err != nil {
		f.Fatalf("NewIdentity returned error: %v", err)
	}
	d, err := SignServiceDescriptor(id.SigningPublicKey, id.SigningPrivateKey, []byte("svc"), []IntroPoint{{NodeKey: []byte("intro")}}, 1000, 2000)
	if err != nil {
		f.Fatalf("SignServiceDescriptor returned error: %v", err)
	}
	validBytes, _ := d.signedBytes()
	f.Add(validBytes)
	f.Add([]byte{})
	f.Add([]byte{0})
	f.Add([]byte{255})
	f.Fuzz(func(t *testing.T, data []byte) {
		_, _ = unmarshalServiceDescriptorFields(data)
	})
}
```

- [ ] **Step 3: Run the new tests and a short fuzz pass**

Run: `go test ./src/garlic/... -run 'TestUnmarshalServiceDescriptorFields|FuzzLayerPlaintextUnmarshal|FuzzServiceDescriptorFieldsUnmarshal' -v`
Expected: PASS.

Run: `go test ./src/garlic/ -fuzz=FuzzLayerPlaintextUnmarshal -fuzztime=30s`
Expected: no crashes reported.

Run: `go test ./src/garlic/ -fuzz=FuzzServiceDescriptorFieldsUnmarshal -fuzztime=30s`
Expected: no crashes reported.

- [ ] **Step 4: Run the full package test suite**

Run: `go test ./src/garlic/... -v 2>&1 | tail -150`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add src/garlic/fuzz_test.go src/garlic/descriptor.go src/garlic/descriptor_test.go
git commit -m "garlic: add fuzz coverage for LayerPlaintext and ServiceDescriptor parsers"
```

---

### Task 11: Threat-model updates (`docs/garlic-threat-model.md`)

**Files:**
- Modify: `docs/garlic-threat-model.md`

**Interfaces:** none (documentation only).

- [ ] **Step 1: Update the "Malicious relay" section's ephemeral-key paragraph**

In `docs/garlic-threat-model.md`, under `## Malicious relay (one Garlic-capable circuit hop, not colluding)`, replace the paragraph starting `**Known weakness — ephemeral key reuse across hops.**` with:

```markdown
**Fixed — per-hop ephemeral keys.** Per `docs/garlic-protocol.md` §4.1,
a circuit's originator now generates an independent ephemeral X25519
keypair for *every* hop. A hop only learns the next hop's ephemeral
public key by successfully decrypting its own layer - it is never
carried as a value shared unchanged across the whole circuit. Two
non-adjacent colluding relays (e.g. hop 1 and hop 3 of a 3-hop circuit)
therefore have no ephemeral public key in common to compare
(`TestNonAdjacentHopsCannotLinkViaEphemeralKeys`). Adjacent hops (hop 1
and hop 2) unavoidably share knowledge of the ephemeral key *between*
them - hop 1 must relay hop 2's ephemeral public key onward as part of
ordinary forwarding - but hop 1 never learns hop 2's corresponding
private key, and so cannot derive hop 2's session key
(`TestRelay1CannotDeriveRelay2SessionKey`). This is the same property a
Tor-style (non-Sphinx) telescoping circuit gives; it is not full Sphinx-
style blinding, which would also hide the next hop's ephemeral public
key from its immediate predecessor - see the crypto hardening design
spec for why that additional step wasn't judged necessary here.
```

- [ ] **Step 2: Add "Malicious relay / availability attacker" after the existing "Malicious relay" section**

Insert a new section directly after the paragraph from Step 1 (before `## Mesh-path intermediate node`):

```markdown
## Malicious relay / availability attacker

Separate from the confidentiality/linkability question above: any relay
on a circuit path can, at will:

- drop packets it's asked to forward,
- delay packets by an arbitrary amount before forwarding,
- reorder packets relative to how it received them,
- selectively drop or delay only packets on one particular circuit while
  forwarding others normally,
- stop forwarding for a circuit entirely, at any point, with no
  notification to anyone.

Garlic has no mechanism to distinguish a relay doing any of the above
deliberately from an ordinary network failure (a dropped UDP datagram, a
congested link, a peer that legitimately went offline) - both present
identically to the originator and to every other hop. This is not a gap
specific to this implementation; no purely reactive circuit protocol
without an independent liveness/acknowledgment channel can make this
distinction, and Garlic does not have one. A circuit that stops
producing traffic is evidence of *something* having gone wrong, not
evidence of which of these causes it was.
```

- [ ] **Step 3: Add "Malicious client" before "Global passive adversary"**

Insert a new section directly before `## Global passive adversary`:

```markdown
## Malicious client

A remote peer sending this node arbitrary Garlic protocol messages,
without being a chosen circuit hop for anything this node originated.
What's mitigated today, and what remains future work:

**Mitigated today:**

- **Circuit creation flood / circuit state exhaustion** —
  `CircuitManager` enforces `MaxCircuits` (global) and
  `MaxCircuitsPerPeer` (per first-hop peer); `relayCircuitState`
  enforces a capacity bound on how many circuits this node will track
  replay state for as a relay, refusing new circuit IDs once full
  (`TestCircuitManagerEnforcesMaxCircuits`, `TestRelayCircuitStateBoundedCapacity`).
- **Malformed packets / oversized declared lengths** — every parser in
  `src/garlic` (`Envelope`, `LayerPlaintext`, `CapabilityMessage`,
  `Bundle`, `AnnounceMessage`, `ServiceDescriptor`'s field encoding)
  validates a declared length against both a fixed maximum and the
  bytes actually present *before* using it to size an allocation or
  slice operation - proven by the `Fuzz*` targets in `fuzz_test.go`,
  whose only invariant is "never panics, never allocates unboundedly."
- **Excessive nesting** — `MaxPathLength` (8) bounds circuit depth;
  onion construction cost is therefore bounded independent of anything a
  remote peer controls.
- **Huge bundles** — `Bundle`'s `message_count` and per-message length
  are both bounded (`maxBundleMessages`, per-entry max size).
- **Huge GID counts / excessive service publishing** — `MaxIntroPoints`
  bounds a single descriptor's introduction-point list;
  `StaticRendezvous` stores one descriptor per GID (a later `Publish`
  replaces, not accumulates).
- **Replay-cache exhaustion** — `ReplayWindow` is a fixed 2048-bit
  bitmap regardless of how far or erratically an attacker drives the
  counter (`TestReplayWindowMemoryStaysBounded`); the relay-side table
  of these windows is itself capacity-bounded (above).
- **CPU exhaustion during X25519/AEAD** — bounded indirectly by the
  circuit/path-length caps above: the amount of ECDH/AEAD work a single
  message can force is a function of `MaxPathLength`, not attacker-
  controlled input size.

**Future work, not currently implemented:**

- No per-source rate limiting on capability requests or circuit-creation
  attempts below the `MaxCircuits`/`MaxCircuitsPerPeer` ceiling itself -
  a peer can still burn CPU cycling up to those ceilings repeatedly if
  circuits are closed and recreated faster than any cooldown.
- No proof-of-work or other admission cost on circuit creation requests,
  so the ceilings above are the only defense against a peer that is
  Garlic-capable but otherwise unvetted.
- Service descriptor publishing (`PublishService`) has no rate limit of
  its own beyond whatever the `Rendezvous` implementation in use chooses
  to enforce - `StaticRendezvous` enforces none.
```

- [ ] **Step 4: Add "Active timing/watermark attacker" after "Traffic correlation / traffic confirmation"**

Insert a new section directly after the existing `## Traffic correlation / traffic confirmation` section (before `## Replay`):

```markdown
## Active timing/watermark attacker

Distinct from the passive correlation adversary above: a relay (or any
on-path node) that *actively* manipulates the timing of packets it
forwards, rather than merely observing them, to inject or detect a
timing pattern ("watermark") that survives the hops in between.

`Config.JitterEnabled`'s random pre-send delay defends against a
*passive* observer trying to correlate exact send timestamps across two
points it watches. It does **not** defend against an adversary that can
selectively delay chosen packets - such an adversary can, in principle,
impose its own timing pattern on a flow regardless of what jitter any
single hop adds on top, since the watermark is injected by the attacker
controlling one hop's forwarding delay, not inferred from otherwise-
unperturbed timing. Nothing in this implementation detects or defends
against this specifically. Do not read the jitter defense described
above as covering this case - it does not, and no claim to the contrary
appears anywhere else in this document or in `docs/garlic-protocol.md`.
```

- [ ] **Step 5: Read the full file back and check consistency**

Run: `grep -n "^##" docs/garlic-threat-model.md` and confirm the new sections appear in the intended order, then read the "Summary table" section (`## Summary table`) at the end of the file and update any row that references the old ephemeral-key-reuse weakness or omits the new adversary classes, so the table doesn't contradict the prose above it.

- [ ] **Step 6: Commit**

```bash
git add docs/garlic-threat-model.md
git commit -m "docs: update Garlic threat model for the crypto hardening pass"
```

---

### Task 12: Protocol spec updates (`docs/garlic-protocol.md`)

**Files:**
- Modify: `docs/garlic-protocol.md`

**Interfaces:** none (documentation only).

- [ ] **Step 1: Update §2 (Garlic Envelope) for the wider CircuitID**

Find the wire-format diagram in `## 2. Garlic Envelope` and update the `circuit_id` row's size from `8` to `16` bytes, and any prose nearby stating the total fixed header size, to match `envelopeFixedHeaderSize = 1 + 16 + 8 + 8 + 4 = 37`.

- [ ] **Step 2: Rewrite §4 header and §4.1 (per-hop key derivation) entirely**

Replace `## 4. Circuit data message (onion routing)`'s intro paragraph and offset table to reflect the wider CircuitID-driven `circuitDataMinSize` (`32 + 37 = 69` bytes), and replace all of `### 4.1 Per-hop key derivation (non-interactive)` with:

```markdown
### 4.1 Per-hop key derivation (chained per-hop ephemeral, non-interactive)

The circuit's originator generates an **independent ephemeral X25519
keypair per hop** (not one for the whole circuit). For hop *i* with
long-term Garlic public key `P_i` (learned via §3), the originator
computes:

```
secret_i          = X25519(ephemeral_i_private, P_i)
establish_secret_i = HKDF-SHA256(secret_i, salt=nil, info="yggdrasil-garlic-v2-circuit-establish")
key_i              = HKDF-SHA256(establish_secret_i, salt=nil, info="yggdrasil-garlic-v2-circuit-data-send")
```

Only `ephemeral_1_public` is sent as the wire prefix to hop 1 (§4, byte
offset 0). Every other hop's ephemeral public key,
`ephemeral_{i+1}_public`, is carried *inside* hop *i*'s own encrypted
layer as `LayerPlaintext.next_hop_ephemeral` (§4.2) - a hop only learns
the next hop's ephemeral key by successfully decrypting its own layer,
never before. Hop *i*, on receipt, independently computes the same
`secret_i` via `X25519(P_i_private, ephemeral_i_public)`
(Diffie-Hellman symmetry) and the same `key_i` via the identical
two-stage HKDF chain - no interactive handshake is needed to establish
`key_i`.

This gives the property that non-adjacent hops (e.g. hop 1 and hop 3 of
a 3-hop circuit) never observe a common ephemeral public key and cannot
link a circuit by comparing them - see
`docs/garlic-threat-model.md`'s "Malicious relay" section and
`TestNonAdjacentHopsCannotLinkViaEphemeralKeys`
(`src/garlic/linkability_test.go`). It is the same shape as Tor's
classical (non-Sphinx) telescoping circuit construction: an immediate
predecessor hop necessarily relays its successor's ephemeral public key
as plain routing information (it has to, to address the next hop) but
never learns that key's private half.

`LabelCircuitDataRecv` (`"yggdrasil-garlic-v2-circuit-data-recv"`) is
reserved in the same derivation chain for a future reply/return path -
no circuit today carries traffic in that direction, so it is currently
unused.
```

- [ ] **Step 3: Update §4.2 (Layer plaintext) wire diagram**

Replace the offset table in `### 4.2 Layer plaintext` with:

```markdown
```
offset  size          field
0       4             next_hop_len          (max 256)
4       next_hop_len  next_hop_key          (empty ⟺ this is the terminal hop)
...     1             has_next_ephemeral    (0 or 1)
...     0 or 32       next_hop_ephemeral    (present ⟺ has_next_ephemeral == 1;
                                              the ephemeral X25519 pubkey for the
                                              hop after this one)
...     4             inner_len             (max 65535, = MaxBodySize)
...     inner_len     inner                 (next layer's ciphertext, or the
                                              final payload if next_hop is empty)
```
```

- [ ] **Step 4: Update §4.3 (Relay behavior) step 7**

Replace point 7 in the numbered list with:

```markdown
7. Otherwise: rebuild an `Envelope` with the same `CircuitID`,
   `PacketCounter`, and `Expiration`, `Body = Inner`. If
   `Config.PaddingEnabled`, this hop independently re-rolls
   `Envelope.PadToRandomRange(MinPaddedSize, MaxPaddedSize)` before
   marshaling - the outgoing wire size on this hop's outbound link is
   unrelated to the size this hop received on its inbound link, by
   design (§9). Forward
   `msgTypeCircuitData || next_hop_ephemeral || new_envelope` to
   `NextHop`, where `next_hop_ephemeral` is the value this hop just
   decrypted from its own layer's `LayerPlaintext.next_hop_ephemeral`
   (§4.2) - **not** the ephemeral public key this hop itself received.
   A message whose decrypted layer has a non-empty `next_hop` but an
   absent `next_hop_ephemeral` is malformed and dropped rather than
   forwarded.
```

- [ ] **Step 5: Update §5 (Replay protection)**

Append a short note after the existing bullet list:

```markdown
`CircuitID` is a 128-bit value drawn from `crypto/rand`
(`src/garlic/circuit.go`, `randomCircuitID`) — see §6's note on why this
width was chosen. `CircuitManager` (the originator's own circuit table)
additionally guards against the vanishingly unlikely case of a locally-
generated ID colliding with one it's already tracking, refusing the
insert rather than silently overwriting the existing circuit's state
(`ErrCircuitIDCollision`).
```

- [ ] **Step 6: Rewrite §6 (Identity and GID)**

Replace the entire section:

```markdown
## 6. Identity and GID

`src/garlic/identity.go`, `src/garlic/gid.go`, `src/garlic/descriptor.go`.
A node's long-term Garlic identity now carries two independent
keypairs, neither derived from the other:

- an X25519 keypair (`Identity.PublicKey`/`PrivateKey`) for circuit-hop
  ECDH, unchanged from before, and
- an Ed25519 keypair (`Identity.SigningPublicKey`/`SigningPrivateKey`)
  used only to sign service descriptors.

A Garlic Service ID is now bound to the *signing* key:

```
GID = version_byte(1) || BLAKE2b-256("yggdrasil-garlic-v1-gid" || signing_public_key || service_id)
```

(the GID domain separator string itself is unchanged; only which public
key feeds it changed, from the X25519 identity key to the Ed25519
signing key). 35 bytes total, canonically encoded as unpadded base32.
Computable and verifiable by anyone who knows `signing_public_key` and
`service_id`; never derived from or convertible to the underlying
Yggdrasil IPv6 address.

A published service is a signed `ServiceDescriptor`
(`src/garlic/descriptor.go`), not a bare introduction-point list. What's
signed (`ServiceDescriptor.signedBytes()`) is exactly:

```
offset  size            field
0       1               version
1       32              service_public_key   (ed25519)
33      4               service_id_len        (max 64)
...     service_id_len  service_id
...     4               intro_point_count     (max MaxIntroPoints = 16)
...     ...             per intro point: node_key_len(1) + node_key
...     8                published_at          (unix seconds)
...     8                expires_at             (unix seconds; expires_at -
                                                   published_at capped at
                                                   MaxDescriptorLifetime,
                                                   7 days)
```

followed by a 64-byte Ed25519 `signature` over exactly those bytes - no
field a rendezvous itself might add (receipt timestamps, sequence
numbers, storage hints) is ever part of what's signed.

`Rendezvous.Lookup` returns this descriptor **unverified** — the
rendezvous is untrusted storage/relay, not a co-signer, and can
withhold, reorder, or serve a stale copy. `Garlic.LookupService`
(`src/garlic/manager.go`) is the client-side trust boundary: it
recomputes the GID from the descriptor's own `service_public_key` and
`service_id` (rejecting a mismatch — this is what makes the GID
self-certifying), verifies the Ed25519 signature, and checks
`expires_at` against the local clock, before returning the descriptor's
introduction points to the caller. A malicious or buggy rendezvous
cannot make a client accept an attacker-controlled service as the
legitimate owner of a GID it doesn't hold the signing key for.
```

- [ ] **Step 7: Update §11 (What this version does not define) if it references the old single-ephemeral-key design or unsigned descriptors**

Read `## 11. What this version does not define` and remove/update any bullet that's now stale given Tasks 1-10 (e.g. anything describing ephemeral key reuse or unsigned service descriptors as current-version limitations — they're fixed now; keep bullets about genuinely still-undefined things like a reply path or DHT-backed rendezvous).

- [ ] **Step 8: Commit**

```bash
git add docs/garlic-protocol.md
git commit -m "docs: update Garlic protocol spec for wire format changes (garlic-v2)"
```

---

### Task 13: Final verification

**Files:** none (verification only).

**Interfaces:** none.

- [ ] **Step 1: Full build and vet**

Run: `go build ./... && go vet ./...`
Expected: clean, no errors.

- [ ] **Step 2: Full test suite**

Run: `go test ./... 2>&1 | tail -100`
Expected: PASS across every package, not just `src/garlic`.

- [ ] **Step 3: Race detector on the garlic package**

Run: `go test -race ./src/garlic/... 2>&1 | tail -150`
Expected: PASS, no data races reported.

- [ ] **Step 4: Fuzz targets, short pass each**

Run each of the following for a bounded time, confirming no crashes:

```bash
go test ./src/garlic/ -fuzz=FuzzEnvelopeUnmarshal -fuzztime=30s
go test ./src/garlic/ -fuzz=FuzzBundleUnmarshal -fuzztime=30s
go test ./src/garlic/ -fuzz=FuzzCapabilityMessageUnmarshal -fuzztime=30s
go test ./src/garlic/ -fuzz=FuzzProcessCircuitData -fuzztime=30s
go test ./src/garlic/ -fuzz=FuzzLayerPlaintextUnmarshal -fuzztime=30s
go test ./src/garlic/ -fuzz=FuzzServiceDescriptorFieldsUnmarshal -fuzztime=30s
```

- [ ] **Step 5: Compatibility check — garlic disabled behaves as vanilla**

Run: `git diff develop --stat -- src/core src/ipv6rwc src/address src/tun src/multicast` (or the equivalent range covering everything this plan's tasks touched)
Expected: empty — confirms no task in this plan touched IPv6 addressing, routing, or link-transport code outside `src/garlic`, `src/config` (Garlic-only fields), and the Garlic wiring block in `cmd/yggdrasil/main.go`.

Run: `go test ./src/config/... -run TestGarlicConfig -v`
Expected: PASS, including `TestGarlicConfigAbsentFromInputStaysDisabled` and `TestGarlicConfigDefaultsDisabled` (pre-existing tests) plus Task 6's new `TestGarlicConfigSigningPrivateKeyDefaultsEmpty`.

- [ ] **Step 6: Self-review against the spec**

Read `docs/superpowers/specs/2026-08-09-garlic-crypto-hardening-design.md` sections A-F top to bottom. For each, confirm a task in this plan implemented it:

- A (per-hop ephemeral keys): Tasks 3, 4.
- B (key derivation labels): Task 2.
- C (circuit ID widening): Task 1, Task 9's collision guard.
- D (service descriptor signing): Tasks 6, 7, 8.
- E (threat model / terminology): Task 11 (terminology pass across the remaining docs — `garlic-architecture.md`, `garlic-security.md` — is explicitly deferred; note this as a follow-up if not folded in during Task 11/12, since the design spec calls for it across all four docs and this plan only edited `garlic-threat-model.md` and `garlic-protocol.md`).
- F (tests): Tasks 4, 8, 9, 10 (linkability, descriptor forgery, replay/collision, fuzzing).

If the terminology pass across `garlic-architecture.md` and `garlic-security.md` (part of spec section E) was not completed, do it now: grep both files for `garlic-v1`, unqualified `anonymous`, and any remaining prose describing the single-ephemeral-key or unsigned-descriptor designs, and update to match the new implementation, following the same pattern as Task 11/12's edits. Also check `docs/garlic-rendezvous.md` specifically — it predates Task 8's signed-descriptor rewrite and near-certainly still describes the old bare-`IntroPoint`-list `Rendezvous` interface; update it to describe `ServiceDescriptor` and the client-side verification boundary instead. Skim `docs/garlic-compatibility.md` and `docs/garlic-testing.md` for the same staleness (any `garlic-v1` capability string, any circuit-ID-as-uint64 assumption, any walkthrough that calls the old `Rendezvous.Publish([]IntroPoint, ttl)` signature) and fix what's actually wrong; both are lower priority than the three docs named in the original task (architecture/threat-model/protocol) but should not be left contradicting the code.

- [ ] **Step 7: Report results**

Summarize, with actual command output (not paraphrased): build status, full test suite pass/fail counts, race detector result, fuzz results, and the self-review's task-to-spec-section mapping. Do not report any step as passing without having actually run it in this task.
