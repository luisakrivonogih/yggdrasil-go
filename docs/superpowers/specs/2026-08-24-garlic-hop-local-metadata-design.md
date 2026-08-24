# Garlic Routing Overlay — Hop-Local Envelope Metadata (Design)

## Status

Approved in conversation 2026-08-24. This is "Project A" of a larger,
explicitly deferred two-project split — see
`docs/superpowers/backlog/2026-08-24-garlic-tunnel-separation-request.md` for
"Project B" (I2P-inspired outbound/inbound tunnel separation, leases,
rendezvous redesign, topology-aware selection, decoy branching), which is
out of scope here and will get its own dedicated brainstorm after this ships.

## Problem

`docs/garlic-threat-model.md` already documents this as an open gap (see its
"Single malicious relay" row): the `Envelope`'s `CircuitID`, `PacketCounter`,
and `Expiration` fields are chosen once by the circuit's originator and
copied byte-for-byte, unencrypted, at every relay hop
(`src/garlic/protocol.go:145-149`, `processCircuitData`'s forwarding path).
Two colluding, non-adjacent relays on the same circuit can trivially confirm
they are relaying the same circuit by comparing these three fields — no
decryption needed. The 2026-08-09 crypto-hardening pass (per-hop ephemeral
X25519 keys, `docs/superpowers/specs/2026-08-09-garlic-crypto-hardening-design.md`)
closed the analogous ephemeral-key linkability signal but explicitly left
this one open.

This document specifies closing it: every hop-to-hop leg gets its own
independent `CircuitID`, `PacketCounter` sequence, and `Expiration`, invisible
to any hop but the two endpoints of that one leg.

**Out of scope for this document** (see the backlog file above): outbound/
inbound tunnel separation, inbound leases, rendezvous redesign,
topology-aware/reliability-scored route selection, decoy branching. Those
require their own dedicated design pass once this lands, since several of
them (leases, rendezvous) build on top of the existing `Rendezvous`/
`ServiceDescriptor`/`IntroPoint` model in ways not yet decided.

## Current mechanics (for implementers)

- `Envelope` (`src/garlic/envelope.go`): fixed header is
  `version(1) circuit_id(16) packet_counter(8) expiration(8) body_len(4)`.
  `Unmarshal` rejects any `Version != EnvelopeVersion1` outright.
- `Circuit`/`Hop` (`src/garlic/circuit.go`, `src/garlic/layer.go`): `Hop` is
  per-hop already (`NodeKey`, `Key`, `Counter`, `NextEphemeralPub`) — but
  `Circuit.Seal` increments every hop's `Counter` together, in lockstep,
  from a shared 0, and returns one `counter` value (`hops[0]`'s pre-increment
  value) that becomes `Envelope.PacketCounter` for every leg.
- `Circuit.ID` is one global `CircuitID`, chosen once in `NewCircuit`, used
  unchanged as `Envelope.CircuitID` for every leg by every relay
  (`processCircuitData`'s `nextEnv.CircuitID = env.CircuitID`).
- `LayerPlaintext` (`src/garlic/layer.go`) already carries `NextHop` and
  `NextHopEphemeral` — fields visible only to the hop that decrypts that
  exact layer, invisible to any other hop. `BuildOnion` is called fresh on
  *every* `Circuit.Seal` call (not once at circuit creation), so the
  originator already has a mechanism, exercised every packet, for handing a
  hop information that's meant only for what *that hop* forwards next.
- `deriveLayerKey` (`src/garlic/crypto.go`) chains two HKDF stages under
  labels literally named `"yggdrasil-garlic-v2-circuit-establish"` /
  `"...-circuit-data-send"` — i.e. the wire format is already
  version-namespaced at the key-derivation level. This is the same
  mechanism that separated garlic-v1 from garlic-v2 during the 2026-08-09
  pass.
- `relayCircuitState` (`src/garlic/relaystate.go`) is already keyed by
  whatever `CircuitID` arrives on the wire — it requires no structural
  change; it becomes correctly per-leg automatically once the ID itself is
  per-leg.
- There is no explicit teardown message in the protocol today.
  `CloseCircuit` only clears the *originator's* local state; every relay
  forgets a circuit purely via `relaystate.go`'s `expireStale` timeout. This
  already satisfies "hop-local teardown" by construction — nothing to build,
  only to document.
- `SupportsGarlicV2()`/`CapabilityGarlicV2` gates whether a capability
  response is ever recorded as a discovery candidate at all
  (`handleCapabilityResponse`, `manager.go:989`) — this is the actual
  enforcement point for "which peers are even eligible to be selected as a
  hop," not a check inlined into `SelectPath` itself.

## Design

### 1. Per-leg identifiers and counters

- `Hop` gains `LocalCircuitID CircuitID`: a fresh random 128-bit value,
  generated once per hop in `CreateCircuit` (one independent draw per hop,
  not derived from anything else).
- `Hop.Counter`'s initial value stops being `0` for every hop. Each hop draws
  its own random 64-bit starting offset at `CreateCircuit` time
  (`crypto/rand`), then increments independently by 1 on every `Seal` call
  exactly as today — no other change to the increment logic. This is not a
  cryptographic requirement (each hop's AEAD key already differs, so
  `(key, counter)` uniqueness never depended on cross-hop distinctness) — it
  exists purely so two colluding hops don't observe literally identical
  counter values by construction.
- `LayerPlaintext` gains three fields, populated only for non-terminal hops
  (mirroring `NextHopEphemeral`'s existing "only set when there is a next
  hop" pattern):
  - `NextLocalCircuitID []byte` — `hops[i+1].LocalCircuitID`.
  - `NextLocalCounter uint64` — `hops[i+1]`'s counter value *for this
    specific `Seal` call* (the same value `BuildOnion` used as the AEAD
    nonce when encrypting hop `i+1`'s own layer this call).
  - `NextLocalExpiration uint64` — see §3 below.
- `BuildOnion` sets these three fields when constructing hop `i`'s layer,
  for every `i < len(hops)-1`.
- `processCircuitData` (`src/garlic/protocol.go`) replaces
  `nextEnv.CircuitID = env.CircuitID` / `PacketCounter = env.PacketCounter` /
  `Expiration = env.Expiration` with the three `NextLocal*` values read from
  the just-decrypted `layer`.
- The very first leg (originator → `hops[0]`) is written directly by the
  originator using `hops[0]`'s own `LocalCircuitID`/current `Counter` — this
  replaces today's single `Circuit.ID` as what goes into the first wire
  `Envelope`. `Circuit.ID` itself is kept as a Go-level field for local
  bookkeeping only (admin RPCs like `closeGarlicCircuit`/
  `getGarlicCircuits` key off it) — it is never itself placed on the wire
  past leg 0, and there is no requirement that it equal `hops[0].LocalCircuitID`
  (keeping them equal is simplest and has no downside, so that's the
  implementation choice: `Circuit.ID = hops[0].LocalCircuitID`).

### 2. Wire/crypto versioning — no version byte inside the encrypted layer

- `EnvelopeVersion2` is added alongside the existing `EnvelopeVersion1`.
  `Unmarshal` accepts both. `EnvelopeVersion1` keeps today's global-ID
  semantics and today's `LayerPlaintext` shape (no `NextLocal*` fields) —
  needed only so this node can still correctly *relay* an
  `EnvelopeVersion1` circuit some other, not-yet-upgraded Garlic node
  originates through it. `EnvelopeVersion2` is the hop-local format
  described in §1.
- New HKDF labels, parallel to the existing v2 ones:
  `LabelCircuitEstablishV3 = "yggdrasil-garlic-v3-circuit-establish"`,
  `LabelCircuitDataSendV3 = "yggdrasil-garlic-v3-circuit-data-send"`.
  `deriveLayerKey` takes an envelope-version parameter and picks the label
  pair accordingly (or: two small functions, `deriveLayerKeyV2`/`V3`, called
  from the version-branch in `processCircuitData`/`CreateCircuit`/
  `BuildOnion` — implementer's choice, whichever reads cleaner).
- This node **always originates new circuits as `EnvelopeVersion2`**, and
  only if every selected hop has advertised the new capability (below) — new
  circuit construction never intentionally falls back to
  `EnvelopeVersion1`. `EnvelopeVersion1` support exists solely for relaying
  other originators' older circuits.
- New capability string `CapabilityGarlicV3 = "garlic-v3"` in
  `capability.go`, alongside the existing `CapabilityGarlicV2`,
  `CapabilityAutoCircuit`. `processCapabilityRequest` advertises all three.
  Add `SupportsGarlicV3()`, mirroring `SupportsGarlicV2()`.
  **Naming note to document inline, at both definitions**: this is
  unrelated to the existing `msgTypeCircuitDataV3` constant
  (`protocol.go`) — that's the auto-pool cover-traffic wire tag from the
  autonomous-routing work, a completely different "v3." The coincidence is
  purely nominal; add a one-line comment at each so a future reader isn't
  confused into thinking they're related.
- `candidatePool`/`SelectPath`/`SelectPathWithGuardPolicy`
  (`src/garlic/manager.go`, `src/garlic/selection.go`) gain a
  `garlic-v3`-support filter alongside the existing diversity/hop-count
  filtering — a candidate that hasn't advertised `garlic-v3` is never
  selected as a hop for a circuit this node originates. Combined with
  `handleCapabilityResponse`'s existing gate (only a `SupportsGarlicV2`
  response is ever recorded as a candidate at all), this is what makes
  "never build a v3 circuit through a v2-only relay" actually true, not
  just a documentation claim.
- **Fail-closed, twice over, for a misbehaving/stale/lying peer**: if a
  `EnvelopeVersion2` message somehow reaches a genuinely old (`v2`-only)
  node, `Unmarshal` rejects it outright on the version byte before any
  crypto runs (`ErrUnsupportedVersion`). If a future implementation ever
  tried the wrong label pair against the right version byte instead, the
  AEAD authentication tag simply fails to verify
  (`ErrDecryptionFailed`) — there is no code path where a version/label
  mismatch produces a plausible-looking decrypted plaintext.

### 3. Expiration jitter

- `LocalExpiration` for a leg = `now + Config.PacketTTL`, independently
  jittered per leg by a small bounded amount — same shape as the existing
  `coverTrafficStagger`/`PadToRandomRange` jitter already in this package,
  applied here to expiration instead of padding size or send delay. Bound
  the jitter to a fraction of `PacketTTL` (e.g. ±10%) so it never
  meaningfully changes real expiry behavior; it only needs to be large
  enough that two legs' `Expiration` values aren't bit-identical by
  construction.
- The relay-side expiry check itself (`time.Now().Unix() >
  int64(env.Expiration)`) is unchanged — it already operates per-message,
  independent of any other leg.

### 4. What does not change

- `relayCircuitState`/`ReplayWindow`: no structural change. Already keyed
  by whatever `CircuitID` is on the wire; becomes correctly per-leg for
  free once §1 lands.
- Teardown: no new message type. Document in `garlic-threat-model.md` that
  this was already hop-local (no relay ever learns of a teardown beyond its
  own two neighbors; state dies by local timeout).
- Multipath, cover traffic, jitter, padding, auto-pool: unaffected in
  shape — they already operate per-circuit and will simply operate on
  `EnvelopeVersion2` circuits once those exist. No functional change
  expected; covered by regression tests (§5) to confirm.
- Rendezvous, `ServiceDescriptor`, `IntroPoint`, `SendGarlicBundled`:
  entirely out of scope (Project B).

## Testing

New adversarial/security tests (package `src/garlic`, `_test.go`):

- `TestNonAdjacentHopsCannotLinkViaEnvelopeMetadata` — build a real
  multi-hop (3+) circuit over the existing linked-test-node harness, capture
  the raw wire envelope at each leg, assert `CircuitID`, `PacketCounter`,
  and `Expiration` all differ pairwise across legs for the same logical
  packet.
- `TestRelayCannotDecryptNonAdjacentLayer` — a hop attempts
  `DecryptLayer` on a captured non-adjacent leg's envelope using its own
  key; assert `ErrDecryptionFailed`, confirming it cannot recover that
  leg's `NextLocalCircuitID`/`NextLocalCounter` without holding the
  adjacent hop's key.
- `TestV3CircuitRejectsV2OnlyHop` — attempt to select/build a circuit
  through a candidate that only advertises `garlic-v2`; assert it's
  excluded at selection (not merely rejected later).
- `TestV2OnlyRelayRejectsV3Envelope` — feed an `EnvelopeVersion2` message
  into a relay path exercising only the `EnvelopeVersion1` decode branch;
  assert `ErrUnsupportedVersion`, not a crash or silent misparse.
- `TestEnvelopeV1CircuitsStillRelayCorrectly` — regression: an
  `EnvelopeVersion1` circuit (global ID, old label) still relays and
  delivers correctly end to end, unaffected by the new code paths.

Existing tests that must keep passing unmodified: `TestBuildOnionHopCannotDecryptAnotherHopsLayer`,
`TestNonAdjacentHopsCannotLinkViaEphemeralKeys`, `TestRelay1CannotDeriveRelay2SessionKey`,
`TestReplayWindowMemoryStaysBounded`, `TestCircuitManagerEnforcesMaxCircuits`,
`TestRelayCircuitStateBoundedCapacity`, the full existing integration suite
(auto-pool, cover traffic, discovery/gossip — including this session's
star-topology reciprocal-discovery tests), and all existing fuzz targets
(`FuzzEnvelopeUnmarshal`, `FuzzLayerPlaintextUnmarshal`, etc. — extend their
seed corpus to include `EnvelopeVersion2` and the new `LayerPlaintext`
fields rather than adding new fuzz targets, since the existing ones already
target exactly these unmarshalers).

## Documentation updates

- `docs/garlic-threat-model.md`: replace the "Single malicious relay" row's
  `CircuitID`/`PacketCounter`/`Expiration` caveat with the new guarantee;
  add the explicit non-goal that a global passive adversary still sees
  ironwood-level routing metadata (source/dest keys, volume, timing) —
  unchanged by this work, already covered elsewhere in the same doc.
- `docs/garlic-protocol.md`: document `EnvelopeVersion2`,
  the extended `LayerPlaintext` wire format, and the new HKDF labels.
- `docs/garlic-security.md`: note the label-based version separation
  mechanism and why it's fail-closed.
- `docs/garlic-compatibility.md`: document that a mixed-version mesh is
  supported (this node relays `EnvelopeVersion1` for others) but this node
  never originates anything but `EnvelopeVersion2` once available.

## Explicitly deferred (Project B, separate spec later)

Outbound/inbound tunnel separation, inbound tunnel leases and rotation,
rendezvous redesign, topology-aware/reliability-scored route selection,
controlled decoy branching, Garlic-clove-style multi-category bundling.
See `docs/superpowers/backlog/2026-08-24-garlic-tunnel-separation-request.md`.
