# Garlic Hop-Local Envelope Metadata Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Close the documented `CircuitID`/`PacketCounter`/`Expiration` linkability gap in the Garlic Routing Overlay by giving every hop-to-hop leg of a circuit its own independent identifier, counter, and expiration, invisible to any hop but the two endpoints of that one leg.

**Architecture:** Add a new `EnvelopeVersion2` wire format and a parallel `garlic-v3` capability. New circuits are always built in the new hop-local format (gated so every selected hop must advertise `garlic-v3`); the new per-leg metadata is embedded inside each hop's existing encrypted onion layer via three new `LayerPlaintext` fields (`NextLocalCircuitID`/`NextLocalCounter`/`NextLocalExpiration`), reusing the exact mechanism `NextHopEphemeral` already uses. Key derivation for the new format uses new HKDF labels, so a version/label mismatch fails closed at the AEAD authentication step even if the version byte were somehow bypassed. All existing `EnvelopeVersion1` code paths, functions, and tests are left untouched (only extended alongside, never modified in place, except one internal DRY refactor of `unmarshalLayerPlaintext` that is behavior-preserving and covered by the existing test suite) so this node keeps relaying other, not-yet-upgraded peers' legacy circuits correctly.

**Tech Stack:** Go, `golang.org/x/crypto/hkdf`, `golang.org/x/crypto/chacha20poly1305` (XChaCha20-Poly1305 AEAD), `golang.org/x/crypto/curve25519` (X25519) — all already in use in `src/garlic`, no new dependencies.

**Spec:** `docs/superpowers/specs/2026-08-24-garlic-hop-local-metadata-design.md`

## Global Constraints

- Every existing test in `src/garlic` must keep passing, unmodified in behavior (one file, `src/garlic/layer.go`, gets a behavior-preserving internal refactor — see Task 4 — verified by its own existing test suite passing unchanged).
- No new round trips: all per-leg metadata is computed and embedded by the circuit originator in one pass, exactly as `NextHopEphemeral` already is. Nothing in this plan adds an interactive handshake.
- Fail closed, never silently reinterpret: an `EnvelopeVersion2` message reaching v2-only code must be rejected at `Unmarshal` (wrong version byte) or, if that were ever bypassed, at AEAD authentication (wrong HKDF label → wrong key). Never partially parse and fall back silently.
- New Go identifiers for this feature use a `HopLocal` suffix/prefix, never `V3`, to avoid colliding in code and comments with the pre-existing, unrelated `msgTypeCircuitDataV3` (auto-pool wire tag). The one exception is the capability string itself, `CapabilityGarlicV3 = "garlic-v3"`, which is intentionally named this way — document the distinction inline at both definitions.
- `CircuitID` stays `[16]byte`. `Envelope.Version` and the new `EnvelopeVersion2` constant are `uint8`, exactly like the existing `EnvelopeVersion1`.
- Outbound/inbound tunnel separation, leases, rendezvous redesign, topology-aware selection, and decoy branching are explicitly out of scope for every task below — see `docs/superpowers/backlog/2026-08-24-garlic-tunnel-separation-request.md`.

---

## File Map

- `src/garlic/crypto.go` — new HKDF labels + `deriveLayerKeyHopLocal`.
- `src/garlic/envelope.go` — `EnvelopeVersion2` constant; `Unmarshal` accepts both versions; new `jitteredExpiration` helper.
- `src/garlic/capability.go` — `CapabilityGarlicV3` constant + `SupportsGarlicV3()`.
- `src/garlic/layer.go` — `Hop.LocalCircuitID` field; `LayerPlaintext`'s three new fields; `marshalHopLocal`/`unmarshalLayerPlaintextHopLocal` (with a shared, behavior-preserving `parseLayerPlaintextPrefix` refactor); `EncryptLayerHopLocal`/`DecryptLayerHopLocal`; `BuildOnionHopLocal`.
- `src/garlic/circuit.go` — `randomCounterOffset` helper; `Circuit.SealHopLocal`.
- `src/garlic/manager.go` — `CreateCircuit`'s per-hop `LocalCircuitID`/counter-offset generation and new `garlic-v3` gate; `processCapabilityRequest`'s advertised versions (this function actually lives in `src/garlic/protocol.go` — see Task 3); `SendGarlic`/`SendGarlicBundled`/`sendAutoPayload` switched to the HopLocal path.
- `src/garlic/protocol.go` — `buildCircuitDataMessageHopLocal`/`buildCircuitDataBodyHopLocal`; `processCircuitData` split into version-branching plus `processCircuitDataLegacy`/`processCircuitDataHopLocal`.
- `src/garlic/crypto_test.go`, `src/garlic/envelope_test.go`, `src/garlic/capability_test.go`, `src/garlic/layer_test.go`, `src/garlic/circuit_test.go`, `src/garlic/manager_test.go` — one new unit test each, per task.
- `src/garlic/hoplocal_test.go` — new file: the cross-cutting adversarial/regression tests (Task 8), following the existing `src/garlic/linkability_test.go` harness pattern.
- `src/garlic/fuzz_test.go` — extend three existing fuzz targets' seed corpora (Task 9).
- `docs/garlic-threat-model.md`, `docs/garlic-protocol.md`, `docs/garlic-security.md`, `docs/garlic-compatibility.md` — Task 10.

---

### Task 1: HKDF labels for the hop-local key chain

**Files:**
- Modify: `src/garlic/crypto.go`
- Test: `src/garlic/crypto_test.go`

**Interfaces:**
- Produces: `LabelCircuitEstablishHopLocal`, `LabelCircuitDataSendHopLocal` (string constants); `deriveLayerKeyHopLocal(ecdhSecret []byte) ([]byte, error)`.

- [ ] **Step 1: Write the failing test**

Add to `src/garlic/crypto_test.go`:

```go
func TestDeriveLayerKeyHopLocalDiffersFromLegacy(t *testing.T) {
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		t.Fatalf("rand.Read returned error: %v", err)
	}
	legacyKey, err := deriveLayerKey(secret)
	if err != nil {
		t.Fatalf("deriveLayerKey returned error: %v", err)
	}
	hopLocalKey, err := deriveLayerKeyHopLocal(secret)
	if err != nil {
		t.Fatalf("deriveLayerKeyHopLocal returned error: %v", err)
	}
	if bytes.Equal(legacyKey, hopLocalKey) {
		t.Fatal("deriveLayerKeyHopLocal produced the same key as deriveLayerKey from the same secret - labels are not actually separating the two chains")
	}
	if len(hopLocalKey) != KeySize {
		t.Fatalf("deriveLayerKeyHopLocal key length = %d, want %d", len(hopLocalKey), KeySize)
	}

	// Deterministic: same secret must always derive the same hop-local key.
	again, err := deriveLayerKeyHopLocal(secret)
	if err != nil {
		t.Fatalf("deriveLayerKeyHopLocal (second call) returned error: %v", err)
	}
	if !bytes.Equal(hopLocalKey, again) {
		t.Fatal("deriveLayerKeyHopLocal is not deterministic for the same input secret")
	}
}
```

Check the top of `src/garlic/crypto_test.go` for existing `"bytes"`/`"crypto/rand"` imports before adding this test; add whichever is missing.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./src/garlic/... -run TestDeriveLayerKeyHopLocalDiffersFromLegacy -v`
Expected: FAIL with `undefined: deriveLayerKeyHopLocal`

- [ ] **Step 3: Write minimal implementation**

In `src/garlic/crypto.go`, add alongside the existing `Label*` constants (after line 47's closing `)`):

```go
// Domain-separation labels for the hop-local envelope format
// (EnvelopeVersion2, capability garlic-v3 - see capability.go's doc
// comment for why that name is unrelated to msgTypeCircuitDataV3).
// Parallel structure to the v2 labels above by design: this is the same
// establish-then-data HKDF chaining, just under a distinct label so a
// v2-only relay that received a hop-local-format packet would derive a
// completely different key and fail AEAD authentication outright, never
// a plausible-looking wrong plaintext.
const (
	LabelCircuitEstablishHopLocal = "yggdrasil-garlic-hoplocal-circuit-establish"
	LabelCircuitDataSendHopLocal  = "yggdrasil-garlic-hoplocal-circuit-data-send"
)
```

And after the existing `deriveLayerKey` function (end of file):

```go
// deriveLayerKeyHopLocal is deriveLayerKey's counterpart for the
// hop-local envelope format (EnvelopeVersion2) - same two-stage HKDF
// chain, under LabelCircuitEstablishHopLocal/LabelCircuitDataSendHopLocal
// instead of the v2 labels, so a key derived here is cryptographically
// unrelated to one derived by deriveLayerKey from the same raw ECDH
// secret.
func deriveLayerKeyHopLocal(ecdhSecret []byte) ([]byte, error) {
	establishSecret, err := DeriveKey(ecdhSecret, nil, LabelCircuitEstablishHopLocal)
	if err != nil {
		return nil, err
	}
	return DeriveKey(establishSecret, nil, LabelCircuitDataSendHopLocal)
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./src/garlic/... -run TestDeriveLayerKeyHopLocalDiffersFromLegacy -v`
Expected: PASS

- [ ] **Step 5: Run the full existing crypto test file to confirm no regression**

Run: `go test ./src/garlic/... -run TestDeriveLayerKey -v` and `go test ./src/garlic/... -run TestSeal -v`
Expected: all PASS, unchanged

- [ ] **Step 6: Commit**

```bash
git add src/garlic/crypto.go src/garlic/crypto_test.go
git commit -m "garlic: add hop-local HKDF label chain (deriveLayerKeyHopLocal)"
```

---

### Task 2: `EnvelopeVersion2` and expiration jitter

**Files:**
- Modify: `src/garlic/envelope.go`
- Test: `src/garlic/envelope_test.go`

**Interfaces:**
- Consumes: `randomIntInRange(lo, hi int) (int, error)` (already defined in `envelope.go`).
- Produces: `EnvelopeVersion2` (uint8 constant); `Unmarshal` now accepts `EnvelopeVersion1` or `EnvelopeVersion2`; `jitteredExpiration(ttl time.Duration) (uint64, error)`.

- [ ] **Step 1: Write the failing test**

Add to `src/garlic/envelope_test.go`:

```go
func TestUnmarshalAcceptsEnvelopeVersion2(t *testing.T) {
	e := &Envelope{
		Version:       EnvelopeVersion2,
		CircuitID:     testCircuitID(7),
		PacketCounter: 3,
		Expiration:    9999999999,
		Body:          []byte("hello"),
	}
	data, err := e.Marshal()
	if err != nil {
		t.Fatalf("Marshal returned error: %v", err)
	}
	got, err := Unmarshal(data)
	if err != nil {
		t.Fatalf("Unmarshal returned error: %v", err)
	}
	if got.Version != EnvelopeVersion2 {
		t.Fatalf("Version = %d, want %d", got.Version, EnvelopeVersion2)
	}
}

func TestUnmarshalRejectsUnknownVersion(t *testing.T) {
	e := &Envelope{Version: EnvelopeVersion2 + 1, CircuitID: testCircuitID(1), Body: []byte("x")}
	data, err := e.Marshal()
	if err != nil {
		t.Fatalf("Marshal returned error: %v", err)
	}
	if _, err := Unmarshal(data); err != ErrUnsupportedVersion {
		t.Fatalf("Unmarshal error = %v, want ErrUnsupportedVersion", err)
	}
}

func TestJitteredExpirationVariesAcrossCalls(t *testing.T) {
	ttl := 60 * time.Second
	seen := make(map[uint64]bool)
	for range 20 {
		exp, err := jitteredExpiration(ttl)
		if err != nil {
			t.Fatalf("jitteredExpiration returned error: %v", err)
		}
		now := uint64(time.Now().Unix())
		if exp < now || exp > now+uint64(ttl/time.Second)+10 {
			t.Fatalf("jitteredExpiration = %d, out of plausible range around now+ttl (%d)", exp, now+uint64(ttl/time.Second))
		}
		seen[exp] = true
	}
	if len(seen) < 2 {
		t.Fatal("jitteredExpiration returned the same value on every call - jitter isn't actually independent")
	}
}
```

Check `envelope_test.go` already imports `"time"` and defines `testCircuitID` (used elsewhere in the package, e.g. `fuzz_test.go`); if `testCircuitID` isn't visible from this file already (it's package-level, so it will be), no change needed.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./src/garlic/... -run 'TestUnmarshalAcceptsEnvelopeVersion2|TestUnmarshalRejectsUnknownVersion|TestJitteredExpirationVariesAcrossCalls' -v`
Expected: FAIL with `undefined: EnvelopeVersion2` / `undefined: jitteredExpiration`

- [ ] **Step 3: Write minimal implementation**

In `src/garlic/envelope.go`, change:

```go
// EnvelopeVersion1 is the only Garlic Envelope wire version defined so far.
const EnvelopeVersion1 uint8 = 1
```

to:

```go
// EnvelopeVersion1 is the original Garlic Envelope wire version: CircuitID,
// PacketCounter, and Expiration are chosen once by the circuit originator
// and copied unchanged at every relay hop. Kept only so this node can
// still correctly relay another, not-yet-upgraded peer's legacy circuit -
// this node itself never originates EnvelopeVersion1 traffic once
// EnvelopeVersion2 is available (see manager.go's CreateCircuit).
const EnvelopeVersion1 uint8 = 1

// EnvelopeVersion2 is the hop-local envelope format: CircuitID,
// PacketCounter, and Expiration are independent per hop-to-hop leg,
// carried forward via LayerPlaintext's NextLocalCircuitID/NextLocalCounter/
// NextLocalExpiration fields (layer.go) rather than copied verbatim. See
// docs/superpowers/specs/2026-08-24-garlic-hop-local-metadata-design.md.
const EnvelopeVersion2 uint8 = 2
```

And change `Unmarshal`'s version check from:

```go
	if e.Version != EnvelopeVersion1 {
		return nil, ErrUnsupportedVersion
	}
```

to:

```go
	if e.Version != EnvelopeVersion1 && e.Version != EnvelopeVersion2 {
		return nil, ErrUnsupportedVersion
	}
```

(This check stays positioned exactly where it already is in `Unmarshal` - right after decoding the fixed header, before parsing `Body`/`Padding` - no reordering needed.)

Then add, right after the existing `randomIntInRange` function at the end of the file:

```go
// jitteredExpiration returns a Unix-seconds Expiration value for
// now+ttl, independently jittered by up to +-10% of ttl (in whole
// seconds, floor of 1 second either way) - the same per-hop-independent
// jitter principle as PadToRandomRange's padding size, applied to
// expiration instead: two legs of the same packet must not carry a
// bit-identical wire Expiration (see docs/garlic-threat-model.md). Jitter
// is computed in whole seconds, not ttl's native nanosecond resolution,
// so the bound passed to randomIntInRange never risks overflowing a
// 32-bit int on a 32-bit build target.
func jitteredExpiration(ttl time.Duration) (uint64, error) {
	base := time.Now().Add(ttl)
	ttlSeconds := int(ttl / time.Second)
	if ttlSeconds <= 0 {
		return uint64(base.Unix()), nil
	}
	span := max(ttlSeconds/10, 1)
	offsetSeconds, err := randomIntInRange(-span, span)
	if err != nil {
		return 0, err
	}
	return uint64(base.Add(time.Duration(offsetSeconds) * time.Second).Unix()), nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./src/garlic/... -run 'TestUnmarshalAcceptsEnvelopeVersion2|TestUnmarshalRejectsUnknownVersion|TestJitteredExpirationVariesAcrossCalls' -v`
Expected: PASS

- [ ] **Step 5: Run the full existing envelope test file to confirm no regression**

Run: `go test ./src/garlic/... -run TestEnvelope -v` and `go test ./src/garlic/... -run TestPad -v` and `go test ./src/garlic/... -run TestUnmarshal -v`
Expected: all PASS, unchanged

- [ ] **Step 6: Commit**

```bash
git add src/garlic/envelope.go src/garlic/envelope_test.go
git commit -m "garlic: add EnvelopeVersion2 and per-leg expiration jitter"
```

---

### Task 3: `garlic-v3` capability

**Files:**
- Modify: `src/garlic/capability.go`
- Modify: `src/garlic/protocol.go` (`processCapabilityRequest`)
- Test: `src/garlic/capability_test.go`

**Interfaces:**
- Produces: `CapabilityGarlicV3` (string constant); `(*CapabilityMessage) SupportsGarlicV3() bool`.

- [ ] **Step 1: Write the failing test**

Add to `src/garlic/capability_test.go`:

```go
func TestSupportsGarlicV3(t *testing.T) {
	withV3 := &CapabilityMessage{Versions: []string{CapabilityGarlicV2, CapabilityGarlicV3}}
	if !withV3.SupportsGarlicV3() {
		t.Fatal("SupportsGarlicV3() = false, want true when garlic-v3 is present")
	}
	withoutV3 := &CapabilityMessage{Versions: []string{CapabilityGarlicV2}}
	if withoutV3.SupportsGarlicV3() {
		t.Fatal("SupportsGarlicV3() = true, want false when garlic-v3 is absent")
	}
}

func TestProcessCapabilityRequestAdvertisesGarlicV3(t *testing.T) {
	id, err := NewIdentity()
	if err != nil {
		t.Fatalf("NewIdentity returned error: %v", err)
	}
	g := &Garlic{identity: id}
	payload := g.processCapabilityRequest()
	msg, err := UnmarshalCapabilityMessage(payload)
	if err != nil {
		t.Fatalf("UnmarshalCapabilityMessage returned error: %v", err)
	}
	if !msg.SupportsGarlicV3() {
		t.Fatalf("advertised versions = %v, want garlic-v3 present", msg.Versions)
	}
	if !msg.SupportsGarlicV2() {
		t.Fatalf("advertised versions = %v, want garlic-v2 still present for backward compat", msg.Versions)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./src/garlic/... -run 'TestSupportsGarlicV3|TestProcessCapabilityRequestAdvertisesGarlicV3' -v`
Expected: FAIL with `undefined: CapabilityGarlicV3`

- [ ] **Step 3: Write minimal implementation**

In `src/garlic/capability.go`, change:

```go
const CapabilityGarlicV2 = "garlic-v2"
```

to add a new constant right after it (leave `CapabilityGarlicV2` and its own doc comment untouched):

```go
// CapabilityGarlicV3 is advertised by a node whose code understands the
// hop-local envelope format (EnvelopeVersion2, src/garlic/envelope.go) -
// see docs/superpowers/specs/2026-08-24-garlic-hop-local-metadata-design.md.
// This name is unrelated to msgTypeCircuitDataV3 (src/garlic/protocol.go,
// the auto-pool cover-traffic wire tag from the autonomous-routing work) -
// the two "v3"s are two entirely different, coincidentally-numbered
// version counters. A node that hasn't advertised garlic-v3 is never
// selected as a hop for a circuit this node originates (manager.go's
// CreateCircuit) - new circuits are always built in the hop-local format
// once available, never intentionally falling back to garlic-v2's global
// CircuitID/PacketCounter/Expiration.
const CapabilityGarlicV3 = "garlic-v3"
```

Then, after the existing `SupportsGarlicV2` method, add:

```go
// SupportsGarlicV3 reports whether the message advertises
// CapabilityGarlicV3.
func (m *CapabilityMessage) SupportsGarlicV3() bool {
	for _, v := range m.Versions {
		if v == CapabilityGarlicV3 {
			return true
		}
	}
	return false
}
```

In `src/garlic/protocol.go`, change `processCapabilityRequest`'s message construction from:

```go
	msg := &CapabilityMessage{
		Versions:  []string{CapabilityGarlicV2, CapabilityAutoCircuit},
		PublicKey: g.identity.PublicKey,
	}
```

to:

```go
	msg := &CapabilityMessage{
		Versions:  []string{CapabilityGarlicV2, CapabilityGarlicV3, CapabilityAutoCircuit},
		PublicKey: g.identity.PublicKey,
	}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./src/garlic/... -run 'TestSupportsGarlicV3|TestProcessCapabilityRequestAdvertisesGarlicV3' -v`
Expected: PASS

- [ ] **Step 5: Run the full existing capability test file to confirm no regression**

Run: `go test ./src/garlic/... -run TestCapability -v` and `go test ./src/garlic/... -run TestMarshal -v` and `go test ./src/garlic/... -run TestUnmarshalCapability -v`
Expected: all PASS, unchanged

- [ ] **Step 6: Commit**

```bash
git add src/garlic/capability.go src/garlic/protocol.go src/garlic/capability_test.go
git commit -m "garlic: advertise garlic-v3 capability alongside garlic-v2"
```

---

### Task 4: Hop-local `LayerPlaintext` wire format

**Files:**
- Modify: `src/garlic/layer.go`
- Test: `src/garlic/layer_test.go`

**Interfaces:**
- Consumes: `EncryptLayer`/`DecryptLayer`/`Seal`/`Open` semantics unchanged (Task 1's `deriveLayerKeyHopLocal` is not called from this file - it's called by the circuit/protocol layer, which passes the already-derived key in).
- Produces: `Hop.LocalCircuitID CircuitID` field; `LayerPlaintext.NextLocalCircuitID []byte` / `NextLocalCounter uint64` / `NextLocalExpiration uint64` fields; `EncryptLayerHopLocal(key []byte, counter uint64, layer *LayerPlaintext) ([]byte, error)`; `DecryptLayerHopLocal(key []byte, counter uint64, ciphertext []byte) (*LayerPlaintext, error)`; `BuildOnionHopLocal(hops []Hop, payload []byte, legExpirations []uint64) ([]byte, error)`.

- [ ] **Step 1: Write the failing test**

Add to `src/garlic/layer_test.go`:

```go
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
```

Check `layer_test.go`'s existing `testHops(n int) []Hop` helper (used elsewhere in that file) builds `n` hops with real `Key`s - if it doesn't already set `NextEphemeralPub` for non-terminal hops, that's fine, this test only inspects `NextLocalCircuitID`/`NextLocalCounter`/`NextLocalExpiration`.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./src/garlic/... -run 'TestBuildOnionHopLocalEmbedsNextLegMetadata|TestUnmarshalLayerPlaintextHopLocalRoundTrips|TestUnmarshalLayerPlaintextUnchangedForLegacyShape' -v`
Expected: FAIL with `undefined: BuildOnionHopLocal` (the third test should already pass once compilation succeeds - it exercises only pre-existing behavior)

- [ ] **Step 3: Write minimal implementation**

In `src/garlic/layer.go`, add `LocalCircuitID` to the `Hop` struct:

```go
type Hop struct {
	NodeKey          []byte   // this hop's Yggdrasil public key (routing address)
	Key              []byte   // per-hop symmetric key, already derived (e.g. via ECDH + deriveLayerKey)
	Counter          uint64   // nonce/replay counter for this hop's layer
	NextEphemeralPub []byte   // ephemeral X25519 pubkey for the hop that follows this one; nil for the final hop
	LocalCircuitID   CircuitID // this hop's own leg identifier for the hop-local envelope format (EnvelopeVersion2) - the CircuitID this hop is told to expect on its incoming leg. Unused by the legacy (EnvelopeVersion1) format.
}
```

Add three fields to `LayerPlaintext`:

```go
type LayerPlaintext struct {
	NextHop          []byte
	NextHopEphemeral []byte
	Inner            []byte
	// NextLocalCircuitID/NextLocalCounter/NextLocalExpiration are set only
	// by the hop-local marshal/unmarshal pair (marshalHopLocal/
	// unmarshalLayerPlaintextHopLocal), for a non-terminal hop: the
	// CircuitID/PacketCounter/Expiration this hop must write into the
	// outgoing Envelope when it forwards to NextHop. Never set by the
	// legacy marshal/unmarshal pair. See
	// docs/superpowers/specs/2026-08-24-garlic-hop-local-metadata-design.md.
	NextLocalCircuitID  []byte
	NextLocalCounter    uint64
	NextLocalExpiration uint64
}
```

Add two new sentinel errors alongside the existing ones:

```go
var (
	ErrEmptyPath                   = errors.New("garlic: onion path must have at least one hop")
	ErrLayerTooShort               = errors.New("garlic: layer plaintext shorter than fixed header")
	ErrLayerTruncated              = errors.New("garlic: layer plaintext truncated")
	ErrNextHopTooLarge             = errors.New("garlic: next-hop field exceeds maximum size")
	ErrLayerInnerTooLarge          = errors.New("garlic: layer inner field exceeds maximum size")
	ErrInvalidNextHopEphemeralSize = errors.New("garlic: next-hop ephemeral key has invalid size")
	ErrInvalidNextHopEphemeralFlag = errors.New("garlic: invalid next-hop-ephemeral presence flag")
	ErrInvalidNextLocalCircuitIDSize = errors.New("garlic: next-local circuit ID has invalid size")
	ErrInvalidNextLocalFlag          = errors.New("garlic: invalid next-local-metadata presence flag")
	ErrLegExpirationCountMismatch    = errors.New("garlic: leg expiration count does not match hop count")
)
```

Refactor `unmarshalLayerPlaintext` to share its prefix-parsing with the new hop-local unmarshal function - replace the existing function body:

```go
func unmarshalLayerPlaintext(data []byte) (*LayerPlaintext, error) {
	l, _, err := parseLayerPlaintextPrefix(data)
	return l, err
}

// parseLayerPlaintextPrefix parses the NextHop/NextHopEphemeral/Inner
// prefix shared by both the legacy and hop-local LayerPlaintext wire
// shapes, returning any not-yet-parsed trailing bytes so each format's
// own unmarshal function can interpret them differently (nothing, for
// the legacy shape; NextLocalCircuitID/NextLocalCounter/
// NextLocalExpiration, for the hop-local shape). This is a pure refactor
// of what unmarshalLayerPlaintext already did inline - behavior for the
// legacy shape is unchanged, verified by the existing layer_test.go/
// FuzzLayerPlaintextUnmarshal suite passing unmodified.
func parseLayerPlaintextPrefix(data []byte) (*LayerPlaintext, []byte, error) {
	if len(data) < 4 {
		return nil, nil, ErrLayerTooShort
	}
	nextHopLen := binary.BigEndian.Uint32(data[:4])
	rest := data[4:]
	if nextHopLen > MaxNextHopSize {
		return nil, nil, ErrNextHopTooLarge
	}
	if uint64(nextHopLen) > uint64(len(rest)) {
		return nil, nil, ErrLayerTruncated
	}
	l := &LayerPlaintext{}
	if nextHopLen > 0 {
		l.NextHop = append([]byte(nil), rest[:nextHopLen]...)
	}
	rest = rest[nextHopLen:]

	if len(rest) < 1 {
		return nil, nil, ErrLayerTruncated
	}
	hasNextEphemeral := rest[0]
	rest = rest[1:]
	switch hasNextEphemeral {
	case 1:
		if uint64(KeySize) > uint64(len(rest)) {
			return nil, nil, ErrLayerTruncated
		}
		l.NextHopEphemeral = append([]byte(nil), rest[:KeySize]...)
		rest = rest[KeySize:]
	case 0:
		// no next-hop ephemeral key - terminal hop.
	default:
		return nil, nil, ErrInvalidNextHopEphemeralFlag
	}

	if len(rest) < 4 {
		return nil, nil, ErrLayerTruncated
	}
	innerLen := binary.BigEndian.Uint32(rest[:4])
	rest = rest[4:]
	if innerLen > MaxLayerInnerSize {
		return nil, nil, ErrLayerInnerTooLarge
	}
	if uint64(innerLen) > uint64(len(rest)) {
		return nil, nil, ErrLayerTruncated
	}
	if innerLen > 0 {
		l.Inner = append([]byte(nil), rest[:innerLen]...)
	}
	rest = rest[innerLen:]
	return l, rest, nil
}

// unmarshalLayerPlaintextHopLocal decodes a layer plaintext produced by
// marshalHopLocal: the legacy prefix, plus a trailing hasNextLocal(1)
// flag and, if set, NextLocalCircuitID(16) NextLocalCounter(8)
// NextLocalExpiration(8).
func unmarshalLayerPlaintextHopLocal(data []byte) (*LayerPlaintext, error) {
	l, rest, err := parseLayerPlaintextPrefix(data)
	if err != nil {
		return nil, err
	}
	if len(rest) < 1 {
		return nil, ErrLayerTruncated
	}
	hasNextLocal := rest[0]
	rest = rest[1:]
	switch hasNextLocal {
	case 1:
		const nextLocalSize = 16 + 8 + 8
		if len(rest) < nextLocalSize {
			return nil, ErrLayerTruncated
		}
		l.NextLocalCircuitID = append([]byte(nil), rest[:16]...)
		l.NextLocalCounter = binary.BigEndian.Uint64(rest[16:24])
		l.NextLocalExpiration = binary.BigEndian.Uint64(rest[24:32])
	case 0:
		// terminal hop - no next leg.
	default:
		return nil, ErrInvalidNextLocalFlag
	}
	return l, nil
}
```

Remove the OLD inline body of `unmarshalLayerPlaintext` (the one currently between its `func` line and closing `}`) since it's now `parseLayerPlaintextPrefix`'s body, called from the two-line replacement above.

Add `marshalHopLocal` right after the existing `marshal` method:

```go
// marshalHopLocal encodes the same legacy prefix marshal already
// produces, followed by a hasNextLocal(1) flag and, if
// NextLocalCircuitID is set, NextLocalCircuitID(16) NextLocalCounter(8)
// NextLocalExpiration(8).
func (l *LayerPlaintext) marshalHopLocal() ([]byte, error) {
	prefix, err := l.marshal()
	if err != nil {
		return nil, err
	}
	if len(l.NextLocalCircuitID) == 0 {
		return append(prefix, 0), nil
	}
	if len(l.NextLocalCircuitID) != 16 {
		return nil, ErrInvalidNextLocalCircuitIDSize
	}
	buf := make([]byte, 0, len(prefix)+1+16+8+8)
	buf = append(buf, prefix...)
	buf = append(buf, 1)
	buf = append(buf, l.NextLocalCircuitID...)
	buf = binary.BigEndian.AppendUint64(buf, l.NextLocalCounter)
	buf = binary.BigEndian.AppendUint64(buf, l.NextLocalExpiration)
	return buf, nil
}
```

Add `EncryptLayerHopLocal`/`DecryptLayerHopLocal` right after the existing `EncryptLayer`/`DecryptLayer`:

```go
// EncryptLayerHopLocal is EncryptLayer's counterpart for the hop-local
// format: same AEAD call, marshalHopLocal instead of marshal.
func EncryptLayerHopLocal(key []byte, counter uint64, layer *LayerPlaintext) ([]byte, error) {
	pt, err := layer.marshalHopLocal()
	if err != nil {
		return nil, err
	}
	return Seal(key, counter, pt, nil)
}

// DecryptLayerHopLocal is DecryptLayer's counterpart for the hop-local
// format: same AEAD call, unmarshalLayerPlaintextHopLocal instead of
// unmarshalLayerPlaintext.
func DecryptLayerHopLocal(key []byte, counter uint64, ciphertext []byte) (*LayerPlaintext, error) {
	pt, err := Open(key, counter, ciphertext, nil)
	if err != nil {
		return nil, err
	}
	return unmarshalLayerPlaintextHopLocal(pt)
}
```

Add `BuildOnionHopLocal` right after `BuildOnion`:

```go
// BuildOnionHopLocal is BuildOnion's counterpart for the hop-local
// format. legExpirations must have exactly len(hops) entries:
// legExpirations[0] is the leg the caller (the circuit originator) writes
// directly into the first outer Envelope; legExpirations[i] for i>=1 is
// embedded into hop (i-1)'s layer as NextLocalExpiration, the value hop
// (i-1) must write when it forwards to hop i. hops[i].LocalCircuitID and
// hops[i].Counter (at call time) are embedded the same way, for i>=1, as
// NextLocalCircuitID/NextLocalCounter.
func BuildOnionHopLocal(hops []Hop, payload []byte, legExpirations []uint64) ([]byte, error) {
	if len(hops) == 0 {
		return nil, ErrEmptyPath
	}
	if len(legExpirations) != len(hops) {
		return nil, ErrLegExpirationCountMismatch
	}

	inner := payload
	for i := len(hops) - 1; i >= 0; i-- {
		layer := &LayerPlaintext{
			NextHopEphemeral: hops[i].NextEphemeralPub,
			Inner:            inner,
		}
		if i+1 < len(hops) {
			layer.NextHop = hops[i+1].NodeKey
			nextID := hops[i+1].LocalCircuitID
			layer.NextLocalCircuitID = nextID[:]
			layer.NextLocalCounter = hops[i+1].Counter
			layer.NextLocalExpiration = legExpirations[i+1]
		}
		ct, err := EncryptLayerHopLocal(hops[i].Key, hops[i].Counter, layer)
		if err != nil {
			return nil, err
		}
		inner = ct
	}
	return inner, nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./src/garlic/... -run 'TestBuildOnionHopLocalEmbedsNextLegMetadata|TestUnmarshalLayerPlaintextHopLocalRoundTrips|TestUnmarshalLayerPlaintextUnchangedForLegacyShape' -v`
Expected: PASS

- [ ] **Step 5: Run the full existing layer test file and fuzz target to confirm the refactor is behavior-preserving**

Run: `go test ./src/garlic/... -run TestBuildOnion -v` and `go test ./src/garlic/... -run TestUnmarshalLayerPlaintext -v` and `go test ./src/garlic/... -run TestEncryptLayer -v` and `go test ./src/garlic/... -run TestDecryptLayer -v` and `go test ./src/garlic/... -fuzz=FuzzLayerPlaintextUnmarshal -fuzztime=15s`
Expected: all PASS, unchanged (the fuzz run finds no new crashers - its existing seed corpus already exercises the refactored `parseLayerPlaintextPrefix` code path since `unmarshalLayerPlaintext` still calls it)

- [ ] **Step 6: Commit**

```bash
git add src/garlic/layer.go src/garlic/layer_test.go
git commit -m "garlic: add hop-local LayerPlaintext wire format (BuildOnionHopLocal)"
```

---

### Task 5: `Circuit.SealHopLocal` and per-hop random counter offsets

**Files:**
- Modify: `src/garlic/circuit.go`
- Test: `src/garlic/circuit_test.go`

**Interfaces:**
- Consumes: `BuildOnionHopLocal` (Task 4), `jitteredExpiration` (Task 2), `randomCircuitID` (already exists in `circuit.go`).
- Produces: `randomCounterOffset() (uint64, error)`; `(*Circuit) SealHopLocal(payload []byte, packetTTL time.Duration) (onion []byte, firstHop []byte, circuitID CircuitID, counter uint64, expiration uint64, err error)`.

- [ ] **Step 1: Write the failing test**

Add to `src/garlic/circuit_test.go`:

```go
func TestSealHopLocalReturnsFirstLegMetadata(t *testing.T) {
	hops := testHops(2)
	hops[0].LocalCircuitID = CircuitID{9, 9, 9}
	hops[0].Counter = 500
	hops[1].LocalCircuitID = CircuitID{8, 8, 8}
	hops[1].Counter = 900
	c, err := NewCircuit(hops, time.Minute, 100, 100000)
	if err != nil {
		t.Fatalf("NewCircuit returned error: %v", err)
	}

	onion, firstHop, circuitID, counter, expiration, err := c.SealHopLocal([]byte("hi"), 60*time.Second)
	if err != nil {
		t.Fatalf("SealHopLocal returned error: %v", err)
	}
	if circuitID != (CircuitID{9, 9, 9}) {
		t.Fatalf("circuitID = %x, want hops[0].LocalCircuitID", circuitID)
	}
	if counter != 500 {
		t.Fatalf("counter = %d, want 500 (hops[0]'s pre-increment Counter)", counter)
	}
	if expiration == 0 {
		t.Fatal("expiration = 0, want a real Unix timestamp")
	}
	if !bytes.Equal(firstHop, hops[0].NodeKey) {
		t.Fatalf("firstHop = %x, want %x", firstHop, hops[0].NodeKey)
	}
	if len(onion) == 0 {
		t.Fatal("onion is empty")
	}

	// A second call must use the now-incremented counters, still
	// independently per hop (not reset, not resynced across hops).
	_, _, _, counter2, _, err := c.SealHopLocal([]byte("second"), 60*time.Second)
	if err != nil {
		t.Fatalf("SealHopLocal (second call) returned error: %v", err)
	}
	if counter2 != 501 {
		t.Fatalf("counter (second call) = %d, want 501", counter2)
	}
}

func TestRandomCounterOffsetVaries(t *testing.T) {
	a, err := randomCounterOffset()
	if err != nil {
		t.Fatalf("randomCounterOffset returned error: %v", err)
	}
	b, err := randomCounterOffset()
	if err != nil {
		t.Fatalf("randomCounterOffset returned error: %v", err)
	}
	if a == b {
		t.Fatal("randomCounterOffset returned the same value twice in a row - not actually random")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./src/garlic/... -run 'TestSealHopLocalReturnsFirstLegMetadata|TestRandomCounterOffsetVaries' -v`
Expected: FAIL with `undefined: randomCounterOffset` / `c.SealHopLocal undefined`

- [ ] **Step 3: Write minimal implementation**

In `src/garlic/circuit.go`, add right after `randomCircuitID`:

```go
// randomCounterOffset draws a random 64-bit starting value for a hop's
// per-leg packet counter (see Hop.Counter's doc comment and
// docs/superpowers/specs/2026-08-24-garlic-hop-local-metadata-design.md).
// Not a cryptographic requirement - each hop's AEAD key already differs,
// so (key, counter) uniqueness never depended on cross-hop distinctness -
// this exists purely so two colluding hops don't observe literally
// identical counter values by construction, the same way they no longer
// observe identical LocalCircuitIDs.
func randomCounterOffset() (uint64, error) {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return 0, err
	}
	return binary.BigEndian.Uint64(b[:]), nil
}
```

Add the `"encoding/binary"` import to `circuit.go`'s import block if not already present (check the current import list first - `circuit.go` currently imports `"crypto/rand"`, `"errors"`, `"sync"`, `"time"`).

Add `SealHopLocal` right after the existing `Seal` method:

```go
// SealHopLocal is Seal's counterpart for the hop-local envelope format
// (EnvelopeVersion2): same closed/expired/budget checks and the same
// per-hop counter-increment loop, but building the onion via
// BuildOnionHopLocal (each leg's own independent, jittered expiration
// computed here via jitteredExpiration) and returning the first leg's own
// CircuitID/counter/expiration - what the caller must write into the very
// first outer Envelope - instead of Seal's single shared counter value.
func (c *Circuit) SealHopLocal(payload []byte, packetTTL time.Duration) (onion []byte, firstHop []byte, circuitID CircuitID, counter uint64, expiration uint64, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.closed {
		return nil, nil, CircuitID{}, 0, 0, ErrCircuitClosed
	}
	if time.Now().After(c.ExpiresAt) {
		return nil, nil, CircuitID{}, 0, 0, ErrCircuitExpired
	}
	if c.packetsSent+1 > c.MaxPackets {
		return nil, nil, CircuitID{}, 0, 0, ErrCircuitPacketLimitExceeded
	}
	if c.bytesSent+uint64(len(payload)) > c.MaxBytes {
		return nil, nil, CircuitID{}, 0, 0, ErrCircuitByteLimitExceeded
	}

	legExpirations := make([]uint64, len(c.hops))
	for i := range c.hops {
		exp, jerr := jitteredExpiration(packetTTL)
		if jerr != nil {
			return nil, nil, CircuitID{}, 0, 0, jerr
		}
		legExpirations[i] = exp
	}

	onion, err = BuildOnionHopLocal(c.hops, payload, legExpirations)
	if err != nil {
		return nil, nil, CircuitID{}, 0, 0, err
	}
	circuitID = c.hops[0].LocalCircuitID
	counter = c.hops[0].Counter
	expiration = legExpirations[0]
	for i := range c.hops {
		c.hops[i].Counter++
	}
	c.packetsSent++
	c.bytesSent += uint64(len(payload))
	return onion, c.hops[0].NodeKey, circuitID, counter, expiration, nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./src/garlic/... -run 'TestSealHopLocalReturnsFirstLegMetadata|TestRandomCounterOffsetVaries' -v`
Expected: PASS

- [ ] **Step 5: Run the full existing circuit test file to confirm no regression**

Run: `go test ./src/garlic/... -run TestCircuit -v` and `go test ./src/garlic/... -run TestSeal -v` and `go test ./src/garlic/... -run TestNewCircuit -v`
Expected: all PASS, unchanged

- [ ] **Step 6: Commit**

```bash
git add src/garlic/circuit.go src/garlic/circuit_test.go
git commit -m "garlic: add Circuit.SealHopLocal and per-hop random counter offsets"
```

---

### Task 6: `CreateCircuit`'s `garlic-v3` gate and per-hop `LocalCircuitID` generation

**Files:**
- Modify: `src/garlic/manager.go`
- Test: `src/garlic/manager_test.go`

**Interfaces:**
- Consumes: `randomCircuitID` (`circuit.go`), `randomCounterOffset` (Task 5), `SupportsGarlicV3` (Task 3).
- Produces: `ErrHopMissingGarlicV3Support` (error); `CreateCircuit` now sets `LocalCircuitID`/randomized `Counter` on every `Hop` it builds and rejects any `path[i]` missing `garlic-v3`.

- [ ] **Step 1: Write the failing test**

Add to `src/garlic/manager_test.go`:

```go
func TestCreateCircuitSetsLocalCircuitIDsAndRejectsV2OnlyHop(t *testing.T) {
	g, hopIDs := buildThreeHopOriginator(t)

	// Every hop advertises garlic-v3: CreateCircuit must succeed and give
	// each hop a distinct, non-zero LocalCircuitID.
	path := make([]CapabilityMessage, len(hopIDs))
	nodeKeys := make([][]byte, len(hopIDs))
	for i, id := range hopIDs {
		path[i] = CapabilityMessage{Versions: []string{CapabilityGarlicV2, CapabilityGarlicV3}, PublicKey: id.PublicKey}
		nodeKeys[i] = []byte{byte('A' + i)}
	}
	circuitID, err := g.CreateCircuit(path, nodeKeys)
	if err != nil {
		t.Fatalf("CreateCircuit returned error: %v", err)
	}
	c, ok := g.circuits.Get(circuitID)
	if !ok {
		t.Fatal("circuit not found after CreateCircuit")
	}
	seen := make(map[CircuitID]bool)
	for i, h := range c.hops {
		if h.LocalCircuitID == (CircuitID{}) {
			t.Fatalf("hop %d has zero-value LocalCircuitID", i)
		}
		if seen[h.LocalCircuitID] {
			t.Fatalf("hop %d reused another hop's LocalCircuitID", i)
		}
		seen[h.LocalCircuitID] = true
	}

	// A hop that never advertised garlic-v3 must be rejected outright.
	pathMissingV3 := make([]CapabilityMessage, len(hopIDs))
	copy(pathMissingV3, path)
	pathMissingV3[1] = CapabilityMessage{Versions: []string{CapabilityGarlicV2}, PublicKey: hopIDs[1].PublicKey}
	if _, err := g.CreateCircuit(pathMissingV3, nodeKeys); err != ErrHopMissingGarlicV3Support {
		t.Fatalf("CreateCircuit error = %v, want ErrHopMissingGarlicV3Support", err)
	}
}
```

This reuses `buildThreeHopOriginator` from `src/garlic/linkability_test.go` (same package, already visible).

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./src/garlic/... -run TestCreateCircuitSetsLocalCircuitIDsAndRejectsV2OnlyHop -v`
Expected: FAIL - either a compile error (`undefined: ErrHopMissingGarlicV3Support`) or the "every hop has a real LocalCircuitID" assertions failing (all zero today)

- [ ] **Step 3: Write minimal implementation**

In `src/garlic/manager.go`, add the new sentinel error alongside the existing ones (near `ErrHopMissingAutoCircuitSupport`):

```go
	ErrHopMissingAutoCircuitSupport = errors.New("garlic: candidate hop does not support CapabilityAutoCircuit")
	ErrHopMissingGarlicV3Support    = errors.New("garlic: candidate hop does not support CapabilityGarlicV3 (hop-local envelope format)")
```

Change `CreateCircuit`'s body: add the v3 gate right after the existing length check, and set `LocalCircuitID`/randomized `Counter` when building each `Hop`. The current function:

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

becomes:

```go
func (g *Garlic) CreateCircuit(path []CapabilityMessage, nodeKeys [][]byte) (CircuitID, error) {
	if len(path) == 0 || len(path) != len(nodeKeys) {
		return CircuitID{}, ErrInvalidPath
	}
	for i := range path {
		if !path[i].SupportsGarlicV3() {
			return CircuitID{}, ErrHopMissingGarlicV3Support
		}
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
		key, err := deriveLayerKeyHopLocal(secret)
		if err != nil {
			return CircuitID{}, err
		}
		var nextEphemeral []byte
		if i+1 < len(path) {
			nextEphemeral = ephemeralPubs[i+1]
		}
		localID, err := randomCircuitID()
		if err != nil {
			return CircuitID{}, err
		}
		counterOffset, err := randomCounterOffset()
		if err != nil {
			return CircuitID{}, err
		}
		hops[i] = Hop{
			NodeKey:          nodeKeys[i],
			Key:              key,
			Counter:          counterOffset,
			NextEphemeralPub: nextEphemeral,
			LocalCircuitID:   localID,
		}
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

Note the key derivation line also changes from `deriveLayerKey(secret)` to `deriveLayerKeyHopLocal(secret)` - every circuit this node originates from here on uses the hop-local key chain, matching the "always originate EnvelopeVersion2" global constraint. `c.ID` (from `g.circuits.Add`/`NewCircuit`'s own unrelated `randomCircuitID()` call) is untouched and keeps being the map key returned to the caller and used for `g.originEphemeral`/admin RPCs - it is a separate value from `hops[0].LocalCircuitID`, never itself placed on the wire.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./src/garlic/... -run TestCreateCircuitSetsLocalCircuitIDsAndRejectsV2OnlyHop -v`
Expected: PASS

- [ ] **Step 5: Fix now-broken existing tests that build a `path` without `garlic-v3`**

`CreateCircuit` is called by many existing tests via `buildTestPath` (`src/garlic/linkability_test.go`), which currently sets `Versions: []string{CapabilityGarlicV2}` only (see that file's own comment: it deliberately doesn't set `CapabilityGarlicV3` yet). Update `buildTestPath` in `src/garlic/linkability_test.go`:

```go
func buildTestPath(hopIdentities []*Identity) ([]CapabilityMessage, [][]byte) {
	path := make([]CapabilityMessage, len(hopIdentities))
	nodeKeys := make([][]byte, len(hopIdentities))
	for i, id := range hopIdentities {
		path[i] = CapabilityMessage{Versions: []string{CapabilityGarlicV2, CapabilityGarlicV3}, PublicKey: id.PublicKey}
		nodeKeys[i] = []byte{byte('A' + i)} // stand-in Yggdrasil routing key
	}
	return path, nodeKeys
}
```

(Removes the stale comment about a since-completed earlier task; adds `CapabilityGarlicV3` to the advertised versions.)

Then grep the whole package for every other test-local helper that builds a `[]CapabilityMessage` for `CreateCircuit`/`AutoCreateCircuit` without `CapabilityGarlicV3`, and add it there too:

Run: `grep -rn "CapabilityMessage{Versions:" src/garlic/*_test.go`

For each match found that's used as input to `CreateCircuit`/`AutoCreateCircuit` (not e.g. a capability-response-only test that never reaches `CreateCircuit`), add `CapabilityGarlicV3` to its `Versions` slice the same way. (`TestSupportsGarlicV3`/`TestProcessCapabilityRequestAdvertisesGarlicV3` from Task 3, and any test specifically asserting `ErrHopMissingGarlicV3Support`/`ErrHopMissingAutoCircuitSupport` behavior, are deliberately excluded - they need a hop that's missing a capability.)

- [ ] **Step 6: Run the full existing manager/linkability/relay_logic/admin test files to confirm no regression**

Run: `go test ./src/garlic/... -run 'TestCreateCircuit|TestNonAdjacentHopsCannotLinkViaEphemeralKeys|TestRelay1CannotDeriveRelay2SessionKey|TestDifferentHopsGetDifferentEphemeralPublicKeys|TestAutoCreateCircuit' -v`
Expected: all PASS

- [ ] **Step 7: Commit**

```bash
git add src/garlic/manager.go src/garlic/manager_test.go src/garlic/linkability_test.go
git commit -m "garlic: gate CreateCircuit on garlic-v3 support, generate per-hop LocalCircuitIDs"
```

---

### Task 7: Wire `SendGarlic`/`SendGarlicBundled`/`sendAutoPayload` to the hop-local path

**Files:**
- Modify: `src/garlic/protocol.go` (new `buildCircuitDataMessageHopLocal`/`buildCircuitDataBodyHopLocal`)
- Modify: `src/garlic/manager.go` (`SendGarlic`, `SendGarlicBundled`, `sendAutoPayload`)
- Test: `src/garlic/manager_test.go`

**Interfaces:**
- Consumes: `Circuit.SealHopLocal` (Task 5).
- Produces: `buildCircuitDataMessageHopLocal(ephemeralPub []byte, id CircuitID, counter, expiration uint64, onion []byte, cfg Config) ([]byte, error)`; `buildCircuitDataBodyHopLocal(...)` (same signature as `buildCircuitDataBody`).

- [ ] **Step 1: Write the failing test**

`sendCircuitData` (`src/garlic/manager.go:296`) is a plain concrete method with no test-interception seam, and adding one is out of scope for this task. Test the new code directly at the layer below it - `buildCircuitDataMessageHopLocal`, called with what `SealHopLocal` actually returns - which exercises the exact same production code path `SendGarlic` will call, with no new scaffolding needed. Add to `src/garlic/manager_test.go`:

```go
func TestBuildCircuitDataMessageHopLocalUsesEnvelopeVersion2(t *testing.T) {
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
	msg, err := buildCircuitDataMessageHopLocal(ephemeralPub, legID, counter, expiration, onion, g.cfg)
	if err != nil {
		t.Fatalf("buildCircuitDataMessageHopLocal returned error: %v", err)
	}
	if msg[0] != msgTypeCircuitData {
		t.Fatalf("message type = %d, want msgTypeCircuitData", msg[0])
	}
	env, err := Unmarshal(msg[1+KeySize:])
	if err != nil {
		t.Fatalf("Unmarshal returned error: %v", err)
	}
	if env.Version != EnvelopeVersion2 {
		t.Fatalf("Envelope.Version = %d, want EnvelopeVersion2", env.Version)
	}
	if env.CircuitID != legID {
		t.Fatalf("Envelope.CircuitID = %x, want %x", env.CircuitID, legID)
	}
}
```

Use this second test instead of the first if `sendCircuitData` has no existing interception seam - it exercises the exact same new code (`buildCircuitDataMessageHopLocal`) with less scaffolding risk. Delete the first draft.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./src/garlic/... -run TestBuildCircuitDataMessageHopLocalUsesEnvelopeVersion2 -v`
Expected: FAIL with `undefined: buildCircuitDataMessageHopLocal`

- [ ] **Step 3: Write minimal implementation**

In `src/garlic/protocol.go`, add right after the existing `buildCircuitDataMessage`/`buildCircuitDataBody` pair:

```go
// buildCircuitDataMessageHopLocal is buildCircuitDataMessage's
// counterpart for the hop-local envelope format: same shape
// (msgTypeCircuitData || ephemeralPub || Envelope), EnvelopeVersion2
// instead of EnvelopeVersion1.
func buildCircuitDataMessageHopLocal(ephemeralPub []byte, id CircuitID, counter, expiration uint64, onion []byte, cfg Config) ([]byte, error) {
	body, err := buildCircuitDataBodyHopLocal(ephemeralPub, id, counter, expiration, onion, cfg)
	if err != nil {
		return nil, err
	}
	return append([]byte{msgTypeCircuitData}, body...), nil
}

// buildCircuitDataBodyHopLocal is buildCircuitDataBody's counterpart for
// the hop-local envelope format.
func buildCircuitDataBodyHopLocal(ephemeralPub []byte, id CircuitID, counter, expiration uint64, onion []byte, cfg Config) ([]byte, error) {
	env := &Envelope{
		Version:       EnvelopeVersion2,
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
```

In `src/garlic/manager.go`, change `SendGarlic`:

```go
	onion, firstHop, counter, err := c.Seal(payload)
	if err != nil {
		return err
	}
	expiration := uint64(time.Now().Add(g.cfg.PacketTTL).Unix())
	msg, err := buildCircuitDataMessage(ephemeralPub, id, counter, expiration, onion, g.cfg)
```

to:

```go
	onion, firstHop, legID, counter, expiration, err := c.SealHopLocal(payload, g.cfg.PacketTTL)
	if err != nil {
		return err
	}
	msg, err := buildCircuitDataMessageHopLocal(ephemeralPub, legID, counter, expiration, onion, g.cfg)
```

Change `SendGarlicBundled` the same way:

```go
	onion, firstHop, counter, err := c.Seal(payload)
	if err != nil {
		return err
	}
	expiration := uint64(time.Now().Add(g.cfg.PacketTTL).Unix())
	realEntry, err := buildCircuitDataBody(ephemeralPub, id, counter, expiration, onion, g.cfg)
```

to:

```go
	onion, firstHop, legID, counter, expiration, err := c.SealHopLocal(payload, g.cfg.PacketTTL)
	if err != nil {
		return err
	}
	realEntry, err := buildCircuitDataBodyHopLocal(ephemeralPub, legID, counter, expiration, onion, g.cfg)
```

Change `sendAutoPayload` the same way:

```go
	onion, firstHop, counter, err := c.Seal(tagged)
	if err != nil {
		return err
	}
	expiration := uint64(time.Now().Add(g.cfg.PacketTTL).Unix())
	body, err := buildCircuitDataBody(ephemeralPub, id, counter, expiration, onion, g.cfg)
```

to:

```go
	onion, firstHop, legID, counter, expiration, err := c.SealHopLocal(tagged, g.cfg.PacketTTL)
	if err != nil {
		return err
	}
	body, err := buildCircuitDataBodyHopLocal(ephemeralPub, legID, counter, expiration, onion, g.cfg)
```

In all three functions, every other line (the `g.circuits.Get(id)`/`g.originEphemeral[id]` lookups, the final `g.sendCircuitData(...)` call using `firstHop`) is unchanged - `id` (the `Circuit.ID` map key) keeps being used for those local lookups; only the value handed to the `buildCircuitData*` call switches from `id`/a separately-computed `expiration` to the new `legID`/`expiration` `SealHopLocal` returns.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./src/garlic/... -run TestBuildCircuitDataMessageHopLocalUsesEnvelopeVersion2 -v`
Expected: PASS

- [ ] **Step 5: Run the full existing manager test file to confirm no regression, and check every other direct `buildCircuitDataMessage`/`buildCircuitDataBody`/`c.Seal(` caller still compiles**

Run: `grep -rn "buildCircuitDataMessage(\|buildCircuitDataBody(\|\.Seal(" src/garlic/*.go | grep -v _test.go`
Expected: only `protocol.go`'s own definitions remain as non-test callers of the legacy `buildCircuitDataMessage`/`buildCircuitDataBody` (they're kept, unused by production code now, but still directly tested by whatever existing tests call them - do not delete them)

Run: `go test ./src/garlic/... -run 'TestSendGarlic|TestSendAutoPayload|TestSendGarlicBundled|TestSendGarlicMultipath' -v`
Expected: all PASS

- [ ] **Step 6: Commit**

```bash
git add src/garlic/protocol.go src/garlic/manager.go src/garlic/manager_test.go
git commit -m "garlic: switch SendGarlic/SendGarlicBundled/sendAutoPayload to the hop-local envelope"
```

---

### Task 8: `processCircuitData` version branching (the relay side)

**Files:**
- Modify: `src/garlic/protocol.go`
- Test: `src/garlic/relay_logic_test.go`

**Interfaces:**
- Consumes: `deriveLayerKeyHopLocal` (Task 1), `DecryptLayerHopLocal` (Task 4).
- Produces: `processCircuitData` now branches on `env.Version`; new unexported helpers `processCircuitDataLegacy`/`processCircuitDataHopLocal`.

- [ ] **Step 1: Write the failing test**

Add to `src/garlic/relay_logic_test.go`. First check that file's existing helpers (`buildTestCircuitData` or similar, used by its other tests) to match style; then add:

```go
func TestProcessCircuitDataHopLocalForwardsNextLegMetadata(t *testing.T) {
	relayID, err := NewIdentity()
	if err != nil {
		t.Fatalf("NewIdentity (relay) returned error: %v", err)
	}
	nextID, err := NewIdentity()
	if err != nil {
		t.Fatalf("NewIdentity (next hop) returned error: %v", err)
	}

	ephemeralPub, ephemeralPriv, err := GenerateKeypair()
	if err != nil {
		t.Fatalf("GenerateKeypair returned error: %v", err)
	}
	secret, err := ECDH(ephemeralPriv, relayID.PublicKey)
	if err != nil {
		t.Fatalf("ECDH returned error: %v", err)
	}
	relayKey, err := deriveLayerKeyHopLocal(secret)
	if err != nil {
		t.Fatalf("deriveLayerKeyHopLocal returned error: %v", err)
	}

	nextLegID := CircuitID{7, 7, 7}
	layer := &LayerPlaintext{
		NextHop:             nextID.PublicKey,
		NextHopEphemeral:    make([]byte, KeySize),
		Inner:               []byte("forwarded ciphertext placeholder"),
		NextLocalCircuitID:  nextLegID[:],
		NextLocalCounter:    123,
		NextLocalExpiration: uint64(time.Now().Add(time.Minute).Unix()),
	}
	ciphertext, err := EncryptLayerHopLocal(relayKey, 0, layer)
	if err != nil {
		t.Fatalf("EncryptLayerHopLocal returned error: %v", err)
	}
	env := &Envelope{
		Version:       EnvelopeVersion2,
		CircuitID:     CircuitID{1, 1, 1},
		PacketCounter: 0,
		Expiration:    uint64(time.Now().Add(time.Minute).Unix()),
		Body:          ciphertext,
	}
	envBytes, err := env.Marshal()
	if err != nil {
		t.Fatalf("Marshal returned error: %v", err)
	}
	body := append(append([]byte(nil), ephemeralPub...), envBytes...)

	relay := hopGarlicFor(relayID)
	action := relay.processCircuitData(body, msgTypeCircuitData)
	if action.kind != actionForward {
		t.Fatalf("action.kind = %v, want actionForward", action.kind)
	}

	forwardedEnv, err := Unmarshal(action.forwardMsg[1+KeySize:])
	if err != nil {
		t.Fatalf("Unmarshal (forwarded envelope) returned error: %v", err)
	}
	if forwardedEnv.Version != EnvelopeVersion2 {
		t.Fatalf("forwarded Envelope.Version = %d, want EnvelopeVersion2", forwardedEnv.Version)
	}
	if forwardedEnv.CircuitID != nextLegID {
		t.Fatalf("forwarded Envelope.CircuitID = %x, want %x (NextLocalCircuitID, not the incoming leg's ID)", forwardedEnv.CircuitID, nextLegID)
	}
	if forwardedEnv.PacketCounter != 123 {
		t.Fatalf("forwarded Envelope.PacketCounter = %d, want 123 (NextLocalCounter, not the incoming leg's counter)", forwardedEnv.PacketCounter)
	}
	if forwardedEnv.Expiration != layer.NextLocalExpiration {
		t.Fatalf("forwarded Envelope.Expiration = %d, want %d (NextLocalExpiration, not the incoming leg's expiration)", forwardedEnv.Expiration, layer.NextLocalExpiration)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./src/garlic/... -run TestProcessCircuitDataHopLocalForwardsNextLegMetadata -v`
Expected: FAIL - today's `processCircuitData` always uses the legacy path regardless of `env.Version`, so `forwardedEnv.CircuitID` would equal the INCOMING leg's ID (`{1,1,1}`), not `nextLegID`

- [ ] **Step 3: Write minimal implementation**

In `src/garlic/protocol.go`, replace `processCircuitData`'s body from the `secret, err := ECDH(...)` line onward. The current full function:

```go
func (g *Garlic) processCircuitData(body []byte, msgType byte) circuitAction {
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

	circuitID := env.CircuitID
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
	key, err := deriveLayerKey(secret)
	if err != nil {
		g.security.authFailures.Add(1)
		return circuitAction{kind: actionDrop}
	}

	layer, err := DecryptLayer(key, env.PacketCounter, env.Body)
	if err != nil {
		g.security.authFailures.Add(1)
		return circuitAction{kind: actionDrop}
	}

	if len(layer.NextHop) == 0 {
		return circuitAction{kind: actionDeliver, circuitID: circuitID, payload: layer.Inner, tagged: msgType == msgTypeCircuitDataV3}
	}
	if len(layer.NextHopEphemeral) != KeySize {
		return circuitAction{kind: actionDrop}
	}

	nextEnv := &Envelope{
		Version:       EnvelopeVersion1,
		CircuitID:     env.CircuitID,
		PacketCounter: env.PacketCounter,
		Expiration:    env.Expiration,
		Body:          layer.Inner,
	}
	if g.cfg.PaddingEnabled {
		_ = nextEnv.PadToRandomRange(g.cfg.MinPaddedSize, g.cfg.MaxPaddedSize)
	}
	nextBytes, err := nextEnv.Marshal()
	if err != nil {
		g.security.malformedPackets.Add(1)
		return circuitAction{kind: actionDrop}
	}
	forwardMsg := make([]byte, 0, 1+KeySize+len(nextBytes))
	forwardMsg = append(forwardMsg, msgType)
	forwardMsg = append(forwardMsg, layer.NextHopEphemeral...)
	forwardMsg = append(forwardMsg, nextBytes...)

	return circuitAction{kind: actionForward, circuitID: circuitID, forwardTo: layer.NextHop, forwardMsg: forwardMsg}
}
```

becomes:

```go
func (g *Garlic) processCircuitData(body []byte, msgType byte) circuitAction {
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
	if time.Now().Unix() > int64(env.Expiration) {
		g.security.expiredPackets.Add(1)
		return circuitAction{kind: actionDrop}
	}

	circuitID := env.CircuitID
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

	switch env.Version {
	case EnvelopeVersion1:
		return g.processCircuitDataLegacy(secret, env, msgType)
	case EnvelopeVersion2:
		return g.processCircuitDataHopLocal(secret, env, msgType)
	default:
		// Unreachable: Unmarshal already rejects any other Version.
		g.security.malformedPackets.Add(1)
		return circuitAction{kind: actionDrop}
	}
}

// processCircuitDataLegacy handles an EnvelopeVersion1 message: unchanged
// from processCircuitData's original body (deriveLayerKey/DecryptLayer,
// verbatim CircuitID/PacketCounter/Expiration forwarding). Kept only so
// this node can still relay another, not-yet-upgraded peer's legacy
// circuit - see EnvelopeVersion1's doc comment (envelope.go).
func (g *Garlic) processCircuitDataLegacy(secret []byte, env *Envelope, msgType byte) circuitAction {
	circuitID := env.CircuitID
	key, err := deriveLayerKey(secret)
	if err != nil {
		g.security.authFailures.Add(1)
		return circuitAction{kind: actionDrop}
	}
	layer, err := DecryptLayer(key, env.PacketCounter, env.Body)
	if err != nil {
		g.security.authFailures.Add(1)
		return circuitAction{kind: actionDrop}
	}
	if len(layer.NextHop) == 0 {
		return circuitAction{kind: actionDeliver, circuitID: circuitID, payload: layer.Inner, tagged: msgType == msgTypeCircuitDataV3}
	}
	if len(layer.NextHopEphemeral) != KeySize {
		return circuitAction{kind: actionDrop}
	}
	nextEnv := &Envelope{
		Version:       EnvelopeVersion1,
		CircuitID:     env.CircuitID,
		PacketCounter: env.PacketCounter,
		Expiration:    env.Expiration,
		Body:          layer.Inner,
	}
	if g.cfg.PaddingEnabled {
		_ = nextEnv.PadToRandomRange(g.cfg.MinPaddedSize, g.cfg.MaxPaddedSize)
	}
	nextBytes, err := nextEnv.Marshal()
	if err != nil {
		g.security.malformedPackets.Add(1)
		return circuitAction{kind: actionDrop}
	}
	forwardMsg := make([]byte, 0, 1+KeySize+len(nextBytes))
	forwardMsg = append(forwardMsg, msgType)
	forwardMsg = append(forwardMsg, layer.NextHopEphemeral...)
	forwardMsg = append(forwardMsg, nextBytes...)
	return circuitAction{kind: actionForward, circuitID: circuitID, forwardTo: layer.NextHop, forwardMsg: forwardMsg}
}

// processCircuitDataHopLocal handles an EnvelopeVersion2 message:
// deriveLayerKeyHopLocal/DecryptLayerHopLocal, and - the actual fix this
// plan exists for - forwards using the just-decrypted layer's
// NextLocalCircuitID/NextLocalCounter/NextLocalExpiration instead of
// copying env's own (incoming-leg) values. See
// docs/superpowers/specs/2026-08-24-garlic-hop-local-metadata-design.md.
func (g *Garlic) processCircuitDataHopLocal(secret []byte, env *Envelope, msgType byte) circuitAction {
	circuitID := env.CircuitID
	key, err := deriveLayerKeyHopLocal(secret)
	if err != nil {
		g.security.authFailures.Add(1)
		return circuitAction{kind: actionDrop}
	}
	layer, err := DecryptLayerHopLocal(key, env.PacketCounter, env.Body)
	if err != nil {
		g.security.authFailures.Add(1)
		return circuitAction{kind: actionDrop}
	}
	if len(layer.NextHop) == 0 {
		return circuitAction{kind: actionDeliver, circuitID: circuitID, payload: layer.Inner, tagged: msgType == msgTypeCircuitDataV3}
	}
	if len(layer.NextHopEphemeral) != KeySize {
		return circuitAction{kind: actionDrop}
	}
	if len(layer.NextLocalCircuitID) != 16 {
		return circuitAction{kind: actionDrop}
	}
	var nextID CircuitID
	copy(nextID[:], layer.NextLocalCircuitID)
	nextEnv := &Envelope{
		Version:       EnvelopeVersion2,
		CircuitID:     nextID,
		PacketCounter: layer.NextLocalCounter,
		Expiration:    layer.NextLocalExpiration,
		Body:          layer.Inner,
	}
	if g.cfg.PaddingEnabled {
		_ = nextEnv.PadToRandomRange(g.cfg.MinPaddedSize, g.cfg.MaxPaddedSize)
	}
	nextBytes, err := nextEnv.Marshal()
	if err != nil {
		g.security.malformedPackets.Add(1)
		return circuitAction{kind: actionDrop}
	}
	forwardMsg := make([]byte, 0, 1+KeySize+len(nextBytes))
	forwardMsg = append(forwardMsg, msgType)
	forwardMsg = append(forwardMsg, layer.NextHopEphemeral...)
	forwardMsg = append(forwardMsg, nextBytes...)
	return circuitAction{kind: actionForward, circuitID: circuitID, forwardTo: layer.NextHop, forwardMsg: forwardMsg}
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./src/garlic/... -run TestProcessCircuitDataHopLocalForwardsNextLegMetadata -v`
Expected: PASS

- [ ] **Step 5: Run the full existing relay_logic/protocol/integration test files and fuzz targets to confirm the v1 path is unaffected**

Run: `go test ./src/garlic/... -run TestProcessCircuitData -v` and `go test ./src/garlic/... -run TestRelay -v` and `go test ./src/garlic/... -run TestIntegration -v` and `go test ./src/garlic/... -fuzz=FuzzProcessCircuitData -fuzztime=15s`
Expected: all PASS, unchanged

- [ ] **Step 6: Commit**

```bash
git add src/garlic/protocol.go src/garlic/relay_logic_test.go
git commit -m "garlic: branch processCircuitData on Envelope.Version, forward hop-local metadata"
```

---

### Task 9: Adversarial and end-to-end regression tests

**Files:**
- Create: `src/garlic/hoplocal_test.go`

**Interfaces:**
- Consumes: everything from Tasks 1-8. This task adds no new production code.

- [ ] **Step 1: Write the tests**

Create `src/garlic/hoplocal_test.go`:

```go
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
```

- [ ] **Step 2: Run tests to verify they pass**

Run: `go test ./src/garlic/... -run 'TestNonAdjacentHopsCannotLinkViaEnvelopeMetadata|TestRelayCannotDecryptNonAdjacentLayer|TestV3CircuitRejectsV2OnlyHop|TestEnvelopeV1CircuitsStillRelayCorrectly' -v`
Expected: all PASS. (The "reject a v3 envelope at a v2-only relay" guarantee is already covered by `TestUnmarshalRejectsUnknownVersion` (Task 2, the actual rejection mechanism) and `TestV3CircuitRejectsV2OnlyHop` above (the actual compatibility guarantee this repo enforces: such an envelope is never produced for a hop that didn't advertise support) - a third test rebuilding the same scenario inside this single-codebase repo can't meaningfully simulate a genuinely separate v2-only build, so it's deliberately not included here.)

- [ ] **Step 3: Run the complete `src/garlic` suite**

Run: `go test ./src/garlic/... -v 2>&1 | tail -100` and separately `go test ./src/garlic/...` (non-verbose, for the final PASS/FAIL summary)
Expected: `ok` for the whole package, zero FAIL lines

- [ ] **Step 4: Commit**

```bash
git add src/garlic/hoplocal_test.go
git commit -m "garlic: add adversarial and regression tests for the hop-local envelope"
```

---

### Task 10: Fuzz seed corpus extensions and documentation

**Files:**
- Modify: `src/garlic/fuzz_test.go`
- Modify: `docs/garlic-threat-model.md`
- Modify: `docs/garlic-protocol.md`
- Modify: `docs/garlic-security.md`
- Modify: `docs/garlic-compatibility.md`

**Interfaces:**
- Consumes: everything from Tasks 1-9. No new production code.

- [ ] **Step 1: Extend `FuzzEnvelopeUnmarshal`'s seed corpus**

In `src/garlic/fuzz_test.go`, add one more seed to `FuzzEnvelopeUnmarshal` right after the existing `f.Add(validBytes)`:

```go
	validV2 := &Envelope{
		Version:       EnvelopeVersion2,
		CircuitID:     testCircuitID(2),
		PacketCounter: 1,
		Expiration:    9999999999,
		Body:          []byte("hello"),
		Padding:       []byte{0, 0, 0},
	}
	validV2Bytes, _ := validV2.Marshal()
	f.Add(validV2Bytes)
```

- [ ] **Step 2: Extend `FuzzLayerPlaintextUnmarshal`'s seed corpus**

Add a hop-local seed right after the existing `f.Add(validBytes)`:

```go
	validHopLocal := &LayerPlaintext{
		NextHop:             []byte("next-hop-key"),
		NextHopEphemeral:    make([]byte, KeySize),
		Inner:               []byte("inner ciphertext"),
		NextLocalCircuitID:  make([]byte, 16),
		NextLocalCounter:    1,
		NextLocalExpiration: 9999999999,
	}
	validHopLocalBytes, _ := validHopLocal.marshalHopLocal()
	f.Add(validHopLocalBytes)
```

Change the fuzz function itself from `_, _ = unmarshalLayerPlaintext(data)` to also exercise the new parser:

```go
	f.Fuzz(func(t *testing.T, data []byte) {
		_, _ = unmarshalLayerPlaintext(data)
		_, _ = unmarshalLayerPlaintextHopLocal(data)
	})
```

- [ ] **Step 3: Extend `FuzzProcessCircuitData`'s seed corpus**

Add a hop-local seed. First add a small helper right after the existing `buildTestCircuitDataForFuzz`:

```go
// buildTestCircuitDataHopLocalForFuzz is buildTestCircuitDataForFuzz's
// counterpart for the hop-local envelope format.
func buildTestCircuitDataHopLocalForFuzz(id *Identity, payload []byte, ttl time.Duration) ([]byte, error) {
	ephemeralPub, ephemeralPriv, err := GenerateKeypair()
	if err != nil {
		return nil, err
	}
	secret, err := ECDH(ephemeralPriv, id.PublicKey)
	if err != nil {
		return nil, err
	}
	key, err := deriveLayerKeyHopLocal(secret)
	if err != nil {
		return nil, err
	}
	localID, err := randomCircuitID()
	if err != nil {
		return nil, err
	}
	c, err := NewCircuit([]Hop{{NodeKey: id.PublicKey, Key: key, LocalCircuitID: localID}}, time.Minute, 100, 100000)
	if err != nil {
		return nil, err
	}
	onion, _, legID, counter, expiration, err := c.SealHopLocal(payload, ttl)
	if err != nil {
		return nil, err
	}
	env := &Envelope{
		Version:       EnvelopeVersion2,
		CircuitID:     legID,
		PacketCounter: counter,
		Expiration:    expiration,
		Body:          onion,
	}
	envBytes, err := env.Marshal()
	if err != nil {
		return nil, err
	}
	return append(append([]byte(nil), ephemeralPub...), envBytes...), nil
}
```

Then in `FuzzProcessCircuitData`, add right after the existing `f.Add(valid)`:

```go
	validHopLocal, _ := buildTestCircuitDataHopLocalForFuzz(id, []byte("payload"), time.Minute)
	f.Add(validHopLocal)
```

- [ ] **Step 4: Run all three extended fuzz targets briefly to confirm the new seeds are valid and nothing panics**

Run: `go test ./src/garlic/... -fuzz=FuzzEnvelopeUnmarshal -fuzztime=15s`
Run: `go test ./src/garlic/... -fuzz=FuzzLayerPlaintextUnmarshal -fuzztime=15s`
Run: `go test ./src/garlic/... -fuzz=FuzzProcessCircuitData -fuzztime=15s`
Expected: all report `ok`, no crashers found

- [ ] **Step 5: Update `docs/garlic-threat-model.md`**

Find the "Single malicious relay" row in the threat-model table (the one quoting: *"CircuitID/PacketCounter/Expiration are still copied verbatim, unencrypted, hop-to-hop, and remain a linkability signal for colluding non-adjacent hops"*). Replace that row's text with:

```
| Single malicious relay (chosen Garlic hop) | Sees its own hop's real-key neighbors (unavoidable, via ironwood's own unencrypted `source`/`dest` fields, not something Garlic hides); cannot decrypt other layers; per-hop ephemeral keys mean non-adjacent colluding hops share no ephemeral key to compare (2026-08-09 pass); for circuits built with the hop-local envelope format (`EnvelopeVersion2`/`garlic-v3`, 2026-08-24 pass), `CircuitID`/`PacketCounter`/`Expiration` are also independent per leg - two non-adjacent colluding hops observe different values for all three fields on the same logical packet, and cannot recover a non-adjacent leg's values without the adjacent hop's key. A legacy `EnvelopeVersion1` circuit (relayed on behalf of a not-yet-upgraded peer) still exhibits the old verbatim-copy behavior for that circuit specifically - this node itself never originates one. |
```

Add a new subsection right after that table (find a natural insertion point near the existing "Traffic correlation" section) documenting the non-goal explicitly:

```markdown
## Hop-local envelope metadata: what it does and does not close

Closing the `CircuitID`/`PacketCounter`/`Expiration` verbatim-copy signal
(above) removes one specific, cheap correlation technique available to two
colluding non-adjacent Garlic relays. It does **not** change what a global
passive adversary observing the underlying Yggdrasil/ironwood mesh can see:
`ironwood/network`'s `traffic` structure still exposes source/destination
node keys, volume, and timing at every hop, regardless of Garlic envelope
version - see "Global passive adversary" above. Hop-local envelope
metadata raises the cost of one specific *metadata-comparison* attack; it
is not a mixnet and does not defend against traffic confirmation via
timing/volume correlation, which remains covered by padding, jitter, and
cover traffic (with their own, separately-documented limits) rather than
by this mechanism.

Teardown was already hop-local before this pass, by construction: there is
no explicit teardown message in the protocol at all.
`Garlic.CloseCircuit` only clears the *originator's* local bookkeeping
(`Circuit`/`originEphemeral` map entries); every relay forgets a circuit
purely via `relaystate.go`'s local `expireStale` timeout, independent of
any other hop. A relay never learns anything about a circuit's teardown
beyond its own two immediate neighbors aging out.
```

- [ ] **Step 6: Update `docs/garlic-protocol.md`**

Add a new section documenting the wire format (find where the existing `Envelope`/`LayerPlaintext` wire formats are already documented and add alongside):

```markdown
## Hop-local envelope format (EnvelopeVersion2 / garlic-v3)

`Envelope.Version = 2` marks a hop-local circuit: `CircuitID`,
`PacketCounter`, and `Expiration` are scoped to one hop-to-hop leg, not
the whole circuit. A relay decrypting its own onion layer under this
format finds three additional fields (see `LayerPlaintext` below) telling
it what to write for the *next* leg when it forwards - it never reuses
its own incoming leg's values.

`LayerPlaintext` (hop-local shape, `marshalHopLocal`/
`unmarshalLayerPlaintextHopLocal`, `src/garlic/layer.go`): the existing
`next_hop_len(4) next_hop(N) has_ephemeral(1) [ephemeral(32)]
inner_len(4) inner(M)` prefix, followed by `has_next_local(1)
[next_local_circuit_id(16) next_local_counter(8)
next_local_expiration(8)]` - present for every non-terminal hop, absent
for the circuit's final hop.

Key derivation uses a distinct HKDF label chain
(`LabelCircuitEstablishHopLocal`/`LabelCircuitDataSendHopLocal`,
`src/garlic/crypto.go`) from the legacy `garlic-v2` chain, so a version
mismatch fails at AEAD authentication even if the `Envelope.Version` byte
check were somehow bypassed.

A node advertises support via the `garlic-v3` capability string
(`CapabilityGarlicV3`, `src/garlic/capability.go`) - unrelated to
`msgTypeCircuitDataV3`, the pre-existing auto-pool cover-traffic wire tag.
`Garlic.CreateCircuit` refuses to build a circuit through any hop that
hasn't advertised `garlic-v3`; every circuit this node originates uses
this format once available, never falling back to `EnvelopeVersion1`
intentionally.
```

- [ ] **Step 7: Update `docs/garlic-security.md`**

Add a short paragraph (find the existing HKDF/label-separation discussion and extend it) noting: the hop-local format reuses the exact establish-then-data HKDF chaining pattern already used to separate `garlic-v1` from `garlic-v2` in the 2026-08-09 crypto-hardening pass, under a new label pair, and that this is why a version/label mismatch is a hard cryptographic failure (`ErrDecryptionFailed`) rather than a parsing ambiguity.

- [ ] **Step 8: Update `docs/garlic-compatibility.md`**

Add a paragraph documenting: a mixed-version mesh is supported - this node correctly relays `EnvelopeVersion1` circuits on behalf of other, not-yet-upgraded Garlic peers - but this node itself never originates anything but `EnvelopeVersion2` once available, gated by requiring every selected hop to advertise `garlic-v3` (`CreateCircuit`'s gate, `ErrHopMissingGarlicV3Support`).

- [ ] **Step 9: Commit**

```bash
git add src/garlic/fuzz_test.go docs/garlic-threat-model.md docs/garlic-protocol.md docs/garlic-security.md docs/garlic-compatibility.md
git commit -m "garlic: extend fuzz seed corpora and document the hop-local envelope format"
```

---

## Final Verification (after all 10 tasks)

- [ ] Run the complete package test suite: `go test ./src/garlic/... -v 2>&1 | tail -150`
  Expected: `ok`, zero FAIL lines, every test from Tasks 1-9 present and passing alongside every pre-existing test.
- [ ] Run `go build ./...` and `go vet ./...` from the repo root.
  Expected: clean, no errors.
- [ ] Run `gofmt -l src/garlic/`.
  Expected: no output (nothing needs formatting).
- [ ] Run each of the three extended fuzz targets for a longer window: `go test ./src/garlic/... -fuzz=FuzzEnvelopeUnmarshal -fuzztime=60s`, `-fuzz=FuzzLayerPlaintextUnmarshal -fuzztime=60s`, `-fuzz=FuzzProcessCircuitData -fuzztime=60s`.
  Expected: all `ok`, no crashers.
- [ ] Confirm `grep -rn "CapabilityGarlicV2\"}" src/garlic/*_test.go` (a `Versions` slice containing ONLY `garlic-v2`, no `garlic-v3`) returns matches only in tests that are deliberately exercising the missing-v3-support rejection path (`TestV3CircuitRejectsV2OnlyHop` and its equivalents) - not in any test that expects `CreateCircuit`/`AutoCreateCircuit` to succeed.
