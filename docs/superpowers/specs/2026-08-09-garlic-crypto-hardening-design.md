# Garlic crypto/protocol hardening — design spec

Status: approved, not yet implemented. Covers Parts 1-6 of the hardening
task: per-hop ephemeral key isolation, circuit ID/replay/direction
hardening, service descriptor authentication, and the corresponding
threat-model/terminology/test updates. The operator dashboard (Parts
7-22) is a separate, independent subsystem with its own spec, sequenced
after this one.

## Problem

Three confirmed weaknesses in the current implementation (`src/garlic/`,
verified by reading the code directly, not the docs):

1. **Ephemeral-key linkability** (`src/garlic/manager.go` `CreateCircuit`,
   `src/garlic/protocol.go` `processCircuitData`): one ephemeral X25519
   keypair is generated per circuit and reused for ECDH with every hop.
   Worse, the same ephemeral public key is forwarded **unchanged, as a
   plaintext wire prefix**, hop to hop
   (`forwardMsg = append(forwardMsg, ephemeralPub...)`, protocol.go:128).
   Any relay — not just adjacent ones — can byte-compare it and link
   circuits.
2. **No service descriptor authentication** (`src/garlic/rendezvous.go`,
   `src/garlic/gid.go`): `StaticRendezvous.Publish`/`Lookup` store and
   return `IntroPoint{NodeKey}` values with no signature anywhere.
   `ComputeGID` is a bare hash with no binding to a key capable of
   proving authorship. A malicious or compromised rendezvous can return
   an attacker-controlled introduction point for any GID today.
3. **Narrow, non-domain-separated key derivation**: `CircuitID` is a
   `uint64` (crypto-random, but narrow); only one HKDF label
   (`LabelLayerKey`) is actually used, `LabelCircuitKey` is defined but
   dead code; there is no reserved label space for a future reply
   direction.

Circuits are confirmed **unidirectional only** — `SendGarlic` seals
outbound traffic, `RecvGarlic` delivers payloads at the final hop, and
there is no return-path code anywhere in `src/garlic/`. Direction
separation is therefore forward-looking hardening (reserve the label
space, make reflection fail by construction), not a fix for a live
bidirectional-context bug.

## Compatibility decision

This is a flag-day wire format change for Garlic-to-Garlic hop
communication: `LayerPlaintext` gains a field and `CircuitID` widens
from 64 to 128 bits, both inside the AEAD-encrypted layer and the
`Envelope` header respectively. Garlic is experimental/pre-release with
no deployed compatibility guarantee, so there is no dual-version
negotiation — old and new Garlic builds simply fail capability
negotiation cleanly (see below) rather than attempting a broken
exchange. This does **not** affect vanilla (non-Garlic) Yggdrasil nodes
or IPv6 routing in any way — `garlic.enabled: false` continues to behave
identically to no Garlic support.

The capability string advertised in `CapabilityMessage` moves from
`"garlic-v1"` to `"garlic-v2"` (constant renamed
`CapabilityGarlicV1` → `CapabilityGarlicV2`) so a mixed old/new
deployment fails capability negotiation explicitly (peer treated as
legacy, never selected as a circuit hop) instead of two incompatible
parsers silently misinterpreting each other's bytes.

## A. Per-hop ephemeral keys (Part 1)

**Construction: chained per-hop ephemeral, not Sphinx.** The circuit
originator generates one independent ephemeral X25519 keypair `E_i` per
hop (not one for the whole circuit). `E_1`'s public key travels as the
wire prefix to hop 1, exactly as today. Each hop's own encrypted layer
additionally carries `NextHopEphemeral = E_{i+1}.pub` — a hop only
learns the *next* hop's ephemeral key by successfully decrypting its own
layer, never before.

This is the same shape as Tor's classical (non-Sphinx) telescoping
circuit construction, chosen over a Sphinx-style blinded construction
because the existing telescoping/onion structure already supports it
without a redesign, per the "prefer a simpler correct construction"
guidance.

Security properties this gives (see Testing section for the tests that
prove each):

- Non-adjacent relays never observe a common ephemeral public key.
  Relay 1 and Relay 3 in a 3-hop circuit cannot link the circuit by
  comparing ephemeral keys — Relay 1 only ever sees `E_1`, `E_2`; Relay 3
  only ever sees `E_3`.
- Relay 1 learns `E_2.pub` (it must, to relay it onward) but never
  `E_2.priv`, so it cannot derive Relay 2's session key. This is
  inherent to any non-interactive telescoping construction: the
  immediate predecessor necessarily carries the next hop's ephemeral
  public key bytes as part of what it forwards. It is not a weaker
  property than what the task asks for — the task's own test list names
  the *non-adjacent* (Relay 1 + Relay 3) collusion case specifically.

**Data structure changes:**

`Hop` (`src/garlic/layer.go`) gains a field:

```go
type Hop struct {
    NodeKey          []byte
    Key              []byte
    Counter          uint64
    NextEphemeralPub []byte // this hop's successor's ephemeral X25519 pubkey; nil for the final hop
}
```

`LayerPlaintext` gains a field and its wire encoding changes:

```go
type LayerPlaintext struct {
    NextHop          []byte
    NextHopEphemeral []byte // KeySize bytes, or absent for the final hop
    Inner            []byte
}
```

Wire encoding (replacing the current `nextHopLen(4)+nextHop+innerLen(4)+inner`):

```
next_hop_len(4) next_hop(next_hop_len)
has_next_ephemeral(1)             // 0 or 1
next_hop_ephemeral(32)            // present only if has_next_ephemeral == 1
inner_len(4) inner(inner_len)
```

A fixed-size presence-flagged field (not length-prefixed) keeps parsing
simple and trivially bounded — no allocation is possible from an
attacker-controlled length here.

`BuildOnion` sets `LayerPlaintext.NextHopEphemeral: hops[i].NextEphemeralPub`
for each layer. `CreateCircuit` (`src/garlic/manager.go`) generates `N`
ephemeral keypairs instead of one, computes each hop's key via
`ECDH(E_i.priv, path[i].PublicKey)`, and sets
`hops[i].NextEphemeralPub = E_{i+1}.pub` (nil for the last hop). Only
`E_1.pub` is retained in `originEphemeral` for building the outbound
wire prefix.

`processCircuitData` (`src/garlic/protocol.go`) changes in exactly one
way: when forwarding, it uses `layer.NextHopEphemeral` (just revealed by
decrypting its own layer) as the next wire prefix, instead of
re-forwarding the `ephemeralPub` it received unchanged.

## B. Key derivation domain separation (Part 1 cont'd, Part 2 direction)

The protocol is fully non-interactive — there is no separate handshake
message distinct from data packets, so "circuit establishment" and
"circuit data" cannot be modeled as two different wire phases without
fabricating one that doesn't exist. Instead they become two distinct
*stages* of one HKDF chain, which gives real, checkable domain
separation without inventing a protocol phase: the raw per-hop ECDH
output is first specialized into an establishment secret, and the
actual per-packet layer key is derived *from that*, not straight from
the ECDH output.

```go
const (
    LabelCircuitEstablish = "yggdrasil-garlic-v2-circuit-establish"
    LabelCircuitDataSend  = "yggdrasil-garlic-v2-circuit-data-send"
    LabelCircuitDataRecv  = "yggdrasil-garlic-v2-circuit-data-recv" // reserved, unused until a reply path exists
)

func deriveLayerKey(ecdhSecret []byte) ([]byte, error) {
    establishSecret, err := DeriveKey(ecdhSecret, nil, LabelCircuitEstablish)
    if err != nil {
        return nil, err
    }
    return DeriveKey(establishSecret, nil, LabelCircuitDataSend)
}
```

`LabelCircuitDataRecv` is carved out now (not wired to anything yet,
since no reply path exists) specifically so a future return-path
feature is structurally unable to derive the same key material as the
forward direction — direction separation is built into the label space
from the start, not bolted on later. `LabelCircuitKey` (currently dead
code) and `LabelLayerKey` are both removed in favor of this chain.

"Authentication where applicable" (per the task's requirement list) is
satisfied by construction, not a separate key: XChaCha20-Poly1305 is an
AEAD — confidentiality and authenticity are bound under the single
layer key by the primitive itself, so a bolted-on separate MAC key
would be redundant rather than an omission.

## C. Circuit ID widening (Part 2)

`CircuitID` changes from `uint64` to `[16]byte`, generated directly from
`crypto/rand` as opaque random bytes (no integer semantics needed — it's
only ever compared for equality and used as a map key). `Envelope`'s
wire header grows accordingly:
`version(1) circuit_id(16) packet_counter(8) expiration(8) body_len(4)`.

The existing relay-side replay/bounds machinery
(`relayCircuitState`, `CircuitManager`) is already correctly capacity-bounded
and already expires stale entries — this change is purely about
collision resistance (128-bit random ID vs 64-bit), not fixing a bounds
bug. `CircuitManager`/`relayCircuitState` map types update from
`map[CircuitID]...` to the same with the new `CircuitID` type — no
structural change needed since Go arrays are comparable/hashable.

## D. Service descriptor signing (Part 3)

**New signing identity**, always part of a Garlic identity (generated
alongside the existing X25519 circuit-ECDH keypair, not derived from it
— per the "no ad-hoc X25519-from-Ed25519 derivation" constraint, this is
two independently generated keypairs, not one key wearing two hats):

```go
type Identity struct {
    PublicKey         []byte            // X25519 — circuit-hop ECDH (unchanged)
    PrivateKey        []byte            // X25519 (unchanged)
    SigningPublicKey  ed25519.PublicKey // NEW — service descriptor signing
    SigningPrivateKey ed25519.PrivateKey
}
```

`NewIdentity` generates both keypairs. `LoadIdentity`/
`LoadIdentityFromPrivateKey` load/derive both from two independently
persisted secrets (config gains a second key field) — never one from the
other.

**Service descriptor** (new file `src/garlic/descriptor.go`):

```go
type ServiceDescriptor struct {
    Version       uint8
    ServicePublicKey []byte // ed25519 pubkey — GID derives from this
    ServiceID     []byte
    IntroPoints   []IntroPoint
    PublishedAt   uint64
    ExpiresAt     uint64
    Signature     []byte // ed25519, over everything above
}
```

`GID = ComputeGID(descriptor.ServicePublicKey, descriptor.ServiceID)` —
same hash construction as today, now bound to the Ed25519 signing key
instead of the X25519 circuit-ECDH key. This is what makes the GID
self-certifying: nobody can produce a descriptor that both signs
correctly *and* hashes to a given GID without holding that GID's
signing private key.

**Exactly what is signed:** `{Version, ServicePublicKey, ServiceID,
IntroPoints, PublishedAt, ExpiresAt}` — the descriptor's own wire
encoding (same field order, same length-prefixed layout as the rest of
this codebase's marshal functions) with the trailing `Signature` field
omitted, signed as one byte string with `ed25519.Sign`. Verification
re-marshals the received descriptor the same way (again omitting
`Signature`) and checks it against the received `Signature`. No
rendezvous-added metadata (receipt timestamps, sequence numbers,
storage hints) is ever part of that marshaled form — the rendezvous is
untrusted storage/relay, not a co-signer.

`Rendezvous` interface changes to carry descriptors instead of bare
`IntroPoint` lists:

```go
type Rendezvous interface {
    Publish(gid GID, descriptor *ServiceDescriptor) error
    Lookup(gid GID) (*ServiceDescriptor, error)
}
```

`StaticRendezvous` stores/returns the descriptor verbatim — it does not
verify anything (it's the thing being defended against). Verification
moves to the client: `Garlic.LookupService` (manager.go) now (1)
recomputes the GID from the returned descriptor's own
`ServicePublicKey`/`ServiceID` and rejects on mismatch
(`ErrDescriptorGIDMismatch`), (2) verifies the Ed25519 signature
(`ErrInvalidDescriptorSignature`), (3) checks `ExpiresAt` against the
local clock (`ErrDescriptorExpired`), and only then returns
`descriptor.IntroPoints` to the caller. A malicious rendezvous can still
withhold, reorder, or serve a stale-but-still-validly-signed descriptor
— it cannot fabricate one for a GID it doesn't hold the signing key for.
Descriptor lifetime is bounded (`ExpiresAt - PublishedAt` capped by a new
`MaxDescriptorLifetime` constant) so a service can't itself mint a
descriptor "valid" for an unreasonable span.

`PublishService` builds the descriptor, signs it with
`identity.SigningPrivateKey`, and calls the new `Rendezvous.Publish`.

## E. Threat model and terminology (Parts 4-5)

Updates to `docs/garlic-threat-model.md`: add the three adversary classes
verbatim from the task (malicious client / DoS surface, malicious relay
availability attacker, active timing/watermark attacker), each stating
what's mitigated today vs. explicitly future work. No new claims beyond
what A-D actually implement — in particular, the active-timing-attacker
section states plainly that jitter does not defend against an adversary
who can selectively delay chosen packets, since nothing in this plan
changes that.

Terminology pass across `garlic-architecture.md`, `garlic-threat-model.md`,
`garlic-protocol.md`, `garlic-security.md`: consistent use of "Garlic
circuit path" (the logical relay sequence) vs. "Yggdrasil transport
path" (the underlying ironwood mesh path between two Garlic-adjacent
nodes), and replacing unqualified "anonymous" with the more precise
terms the task specifies (privacy-enhanced, unlinkability, correlation
resistance, traffic-analysis cost) wherever the current docs overclaim.

`docs/garlic-protocol.md` gets the exact new wire encodings from A-C
(LayerPlaintext, Envelope, ServiceDescriptor, the `garlic-v2` capability
string).

## F. Tests

Expand `src/garlic/*_test.go` per the task's Part 6 checklist (key
isolation, circuit lifecycle, direction, replay, service identity, DoS
bounds) — the implementation plan enumerates these as concrete test
functions per package file touched. Two categories worth calling out at
the design level:

- **Fuzz tests** (`src/garlic/fuzz_test.go` already exists and covers
  the envelope/capability/announce parsers) extend to the new
  `LayerPlaintext` encoding and `ServiceDescriptor` parsing — both are
  attacker-controlled-input parsers with the same bounded-allocation
  discipline as the existing ones.
- **Linkability tests** are the novel category: build a circuit,
  capture what each hop actually observes (ephemeral pubkeys, session
  keys derivable), and assert the non-adjacent-collusion property
  directly rather than only testing confidentiality/tampering as today.

## Out of scope (this spec)

- The operator dashboard (separate spec, sequenced after this work).
- A reply/return path for circuits (the direction-separation labels are
  reserved for it, but building it is not part of this plan).
- DHT-backed rendezvous (still `StaticRendezvous` only — descriptor
  authentication is orthogonal to how descriptors get distributed).
- Sphinx-style circuit construction (explicitly rejected above in favor
  of the simpler chained-ephemeral design).
