# Garlic Routing Overlay — Security Review

Self-review pass over the implementation in `src/garlic` and the
`src/core` changes in `src/core/garlic.go`/`types.go`/`core.go`, run
after Phase 13 (fuzzing/integration tests). Findings are organized by
the checklist in the original brief. Where a finding is a real,
unaddressed gap, it's stated as one — this document's job is to be
useful to the next person hardening this code, not to reassure.

## Cryptographic primitives (per docs/garlic-architecture.md §15)

| Operation | Primitive | Notes |
|---|---|---|
| Key agreement | X25519 (`golang.org/x/crypto/curve25519`) | `ECDH`, `crypto.go` |
| Key derivation | HKDF-SHA256 (`golang.org/x/crypto/hkdf`) | `DeriveKey`, two-stage chain with an explicit domain-separation label per stage: the raw per-hop ECDH output is first specialized via `LabelCircuitEstablish`, then the packet key is derived from *that* via `LabelCircuitDataSend`. `LabelCircuitDataRecv` is a third label, reserved but unwired until a reply path exists, so a future return direction structurally cannot derive the same key material as the forward direction. (The original `LabelLayerKey`/`LabelCircuitKey` pair this row used to describe was removed in the crypto-hardening pass in favor of this chain.) |
| Authenticated encryption | XChaCha20-Poly1305 (`golang.org/x/crypto/chacha20poly1305`) | `Seal`/`Open`, 24-byte nonce |
| Nonce generation | Deterministic from caller-supplied counter | Right-aligned into a zero-padded 24-byte buffer (`nonceFromCounter`) |
| Service identifier hash | BLAKE2b-256 (`golang.org/x/crypto/blake2b`) | `ComputeGID`, with domain separator |
| Key lifetime | Long-term `Identity`: until rotated by config change. Ephemeral per-circuit key: one circuit's lifetime (`Config.CircuitLifetime`, default 10m) | |
| Rekey procedure | Build a new `Circuit` (fresh ephemeral keypair) and retire the old one once its lifetime/packet/byte budget is exhausted | No in-place key rotation within one `Circuit` |

No custom cipher, no bare-hash-as-encryption, no unauthenticated
encryption anywhere in this package — every ciphertext produced by
`Seal`/`EncryptLayer` carries a Poly1305 tag, and every `Open`/
`DecryptLayer` call verifies it before returning plaintext.

The hop-local envelope format (`EnvelopeVersion2`/`garlic-v3`,
`Circuit.SealHopLocal`) reuses this exact two-stage
establish-then-data HKDF chaining pattern — the same shape already used
to separate `garlic-v1` from `garlic-v2` in the 2026-08-09
crypto-hardening pass — under a new label pair,
`LabelCircuitEstablishHopLocal`/`LabelCircuitDataSendHopLocal`
(`deriveLayerKeyHopLocal`, `src/garlic/crypto.go`), independent of the
legacy `LabelCircuitEstablish`/`LabelCircuitDataSend` chain above. This
is why a version/label mismatch — decrypting a hop-local layer with the
legacy chain's key, or vice versa — is a hard cryptographic failure
(`ErrDecryptionFailed`, from Poly1305 tag verification) rather than a
parsing ambiguity: the two chains never derive the same key material
from the same raw ECDH secret, so there is no wrong-format plaintext
that happens to parse.

## Identity correlation

- **Long-term Garlic identity is independent of the Yggdrasil node
  identity** (`docs/garlic-architecture.md` §1.1) — an X25519 keypair,
  never derived from the node's ed25519 key. Compromise of one doesn't
  reveal the other.

  **Update:** the `Identity` also now carries a second, independently
  generated Ed25519 keypair (`SigningPublicKey`/`SigningPrivateKey`,
  `src/garlic/identity.go`), used only for service-descriptor signing
  (below) — generated fresh alongside the X25519 circuit-ECDH keypair,
  never derived from it or from the Yggdrasil node identity, per the
  same "no ad-hoc X25519-from-Ed25519 derivation" constraint as the
  original bullet above. Compromising any one of the three identities
  (Yggdrasil node key, Garlic X25519 key, Garlic signing key) does not
  reveal the other two.
- **Capability responses correlate a node key to "runs Garlic-v2" and to
  a specific Garlic public key.** This is an intentional, necessary
  disclosure (you can't select a hop you can't verify), but it does mean
  a passive-ish observer who can send capability requests (anyone) can
  build a map of which Yggdrasil node keys are Garlic-capable and what
  their Garlic public keys are. Not mitigated, and not mitigable without
  removing the capability-response feature itself.
- **Fixed — per-hop ephemeral keys.** The circuit originator now
  generates an independent ephemeral X25519 keypair per hop rather than
  reusing one for every hop's ECDH; a hop only learns the next hop's
  ephemeral public key by successfully decrypting its own layer
  (`src/garlic/manager.go`'s `CreateCircuit`, `docs/garlic-protocol.md`
  §4.1). This closes the ephemeral-key-reuse linkability weakness this
  bullet used to flag — see `docs/garlic-threat-model.md`'s "Malicious
  relay" section for the exact property proven
  (`TestNonAdjacentHopsCannotLinkViaEphemeralKeys`) and its residual
  scope: `CircuitID`/`PacketCounter`/`Expiration` still travel outside
  the AEAD-encrypted layer and are copied verbatim hop-to-hop, so
  colluding non-adjacent relays can still confirm they're on the same
  circuit by comparing those three fields even though they now share no
  ephemeral key. That residual was unaddressed by this pass and was a
  concrete item for a future one.

  **Update:** the hop-local envelope format (`EnvelopeVersion2`/
  `garlic-v3`, 2026-08-24 — see the new paragraph in "Cryptographic
  primitives" above and `docs/garlic-protocol.md`'s "Hop-local envelope
  format" section) closes the *bit-identical-by-construction* form of
  this residual for every circuit this node originates:
  `CircuitID`/`PacketCounter`/`Expiration` are now independent per
  hop-to-hop leg rather than copied verbatim, so two non-adjacent
  colluding relays no longer observe identical values for all three
  fields on the same logical packet by construction. It does not close
  every form of cross-leg correlation via these fields, though: each
  leg's `PacketCounter` starts at an independent random offset but
  still increments by exactly 1 per packet, so the *delta* between two
  legs' counters stays constant for the circuit's life and becomes a
  free, repeatable correlator once a pair of colluding relays anchors
  it with even one independently-obtained packet pairing — see
  `docs/garlic-threat-model.md`'s "Hop-local envelope metadata: what it
  does and does not close" section for that residual in full. This node
  itself never originates a legacy `EnvelopeVersion1` circuit once it
  understands `garlic-v3`; it still correctly relays one on behalf of a
  not-yet-upgraded peer, and that specific circuit keeps exhibiting the
  old verbatim-copy behavior, since this node doesn't control the
  format a peer it relays for chose at origination.

## IP / address leakage

`address.AddrForKey`/`GetKey` are untouched by this project — Garlic
never derives, publishes, or logs a node's Yggdrasil IPv6 address as
part of any Garlic-layer identifier. `GID` (§6 of
`docs/garlic-protocol.md`) is a hash, not an address. The first hop of a
circuit necessarily learns the *sender's* real node key (someone has to
address the first packet), which is standard for onion routing and
stated plainly in the threat model rather than hidden.

**Correction:** this section originally stopped there, implying the
*only* leakage was the expected first-hop one. Re-reading
`ironwood/network/traffic.go` and `pathfinder.go` directly found more:
the real node keys of **every** hop-to-hop link in a circuit (not just
the first) are visible in cleartext to any node on the underlying mesh
path between those two hops - not because Garlic leaks them, but because
ironwood's own `network` layer carries `source`/`dest` as unencrypted
wire fields, separate from the `encrypted` package's payload protection.
See `docs/garlic-threat-model.md`'s "Mesh-path intermediate node"
section for the full analysis. This doesn't change what Garlic itself
does, but it does mean "a relay only sees its own neighbors" understates
who can gain that same visibility.

## Timing leakage

Partially mitigated, default on. `Garlic.sendCircuitData` (both the
originator's `SendGarlic`/`SendGarlicBundled` path and every relay's
forward path in `processCircuitData`) routes every send through
`src/garlic/jitter.go`'s bounded worker-pool scheduler, delaying actual
transmission by an amount drawn uniformly from `[Config.MinJitter,
Config.MaxJitter]` (default `[0, 75ms]`), independently re-rolled per
packet. This is not batching or fixed-interval sending — it's
per-packet randomized delay — so it raises the cost of exact
send-timestamp correlation across hops without providing the stronger
guarantees a real mix would. See `docs/garlic-threat-model.md`'s
"Traffic correlation" section for what this does and does not defend
against, and jitter.go's doc comment for why it's built as a
non-blocking bounded scheduler rather than a simple `time.Sleep`
(relay forwarding runs synchronously inside `core.Core.ReadFrom`'s read
loop and must never block it).

## Packet size leakage

Mitigated, default on. `Envelope.PadToRandomRange(MinPaddedSize,
MaxPaddedSize)` (default `[512, 1400]` bytes) is called by
`buildCircuitDataBody` — shared by `SendGarlic`, `SendGarlicBundled`,
and every relay's forward path in `processCircuitData` — whenever
`Config.PaddingEnabled` (default true). Each of those three call sites
re-rolls independently, so a packet's size on the link into a hop
carries no information about its size on the link out of that hop.
`Bundle.AddCoverMessage` (`docs/garlic-protocol.md` §7) is now also
wired into the send path via `SendGarlicBundled`, letting a real
message travel alongside indistinguishable cover entries — opt-in per
call via `coverCount`, not automatic for every `SendGarlic` call.

## Route / destination leakage

Covered per-adversary-class in `docs/garlic-threat-model.md`. Summary:
each *Garlic* hop learns only its immediate neighbors at the onion-layer
level; the terminal hop learns the payload (by design) and its immediate
predecessor; nothing learns the full path except the originator, who
chose it. That summary is still accurate for the onion layer itself, but
per the correction above, "learns its immediate neighbors" is also true
of any node on the mesh path *between* two Garlic hops, whether or not
it's a chosen hop - the onion layer's own confidentiality guarantees are
unaffected, but the pool of parties who can observe a given hop-pair's
existence is larger than "the two hops" alone.

## Replay

Verified: `TestProcessCircuitDataDropsReplay` (protocol level),
`TestReplayWindow*` (primitive level, 6 tests including an explicit
bounded-memory check, `TestReplayWindowMemoryStaysBounded`). Two
independent replay windows (relay-side per circuit ID, and implicitly
none needed on the origin side since it's the sole source of increasing
counters) — see `docs/garlic-protocol.md` §5.

## Nonce reuse

The single most safety-critical invariant in this codebase:
`(key, packet_counter)` must never repeat under `Seal`/`EncryptLayer`.
Enforced by construction, not by runtime checking:

- Every derived key (`DeriveKey` output) is either (a) unique per
  circuit, because it's derived from a fresh ephemeral keypair's ECDH
  output each time `CreateCircuit` runs, or (b) a test-only fixed key,
  never reused in the actual send path.
- `Circuit.Seal` is the **only** place that increments and consumes
  per-hop counters, under a mutex, and does so atomically with the
  actual `EncryptLayer` call (`circuit.go`) — there is no code path that
  can call `EncryptLayer` twice with the same `Circuit`'s counter value
  without an intervening increment.
- Verified directly: `TestCircuitSealIncrementsPerHopCounters` asserts
  that re-encrypting at a stale counter fails to decrypt the new
  ciphertext (i.e. the nonce genuinely changed, not just the counter
  field).

Residual risk: if a future change ever allows constructing two
`Circuit`s that share both an ephemeral keypair *and* a hop's public
key (impossible today, since `CreateCircuit` always calls
`GenerateKeypair` fresh) — flagging this as an invariant to preserve,
not a currently-exploitable path.

## Key reuse

Long-term `Identity` keys are intentionally reused across all circuits a
node participates in (that's what makes them "long-term identity" rather
than ephemeral) — this is standard and expected; what must never repeat
is the *(derived symmetric key, counter)* pair, addressed above. The
long-term private key is never used directly as a symmetric key or AEAD
key anywhere — it only ever feeds into `ECDH`, whose *output* is then
passed through `DeriveKey` before any encryption happens.

## Forward secrecy

**Partial, not complete.** Per-hop ephemeral keys (one independent
X25519 keypair per hop, not one shared across the whole circuit — see
"Identity correlation" above) mean compromising one circuit's derived
keys doesn't expose other circuits (past or future) between the same two
identities, and non-adjacent hops within the same circuit share no
ephemeral key material either — this is real forward secrecy at both the
circuit and hop granularity. However, compromising a hop's **long-term**
Garlic private key retroactively allows recomputing that hop's
`secret_i = ECDH(hop_private, ephemeral_public)` for any circuit whose
traffic was recorded, *if* that hop's ephemeral public key was observed
(§4.1 of the protocol doc — each hop's ephemeral public key travels in
the clear-to-the-hop portion of the circuitData message addressed to
it). This is inherent to any non-interactive telescoping construction
(the crypto-hardening design spec's section A: "the immediate
predecessor necessarily carries the next hop's ephemeral public key
bytes as part of what it forwards") and is not something per-hop
ephemeral keys were meant to close — only a mutual/reply-path ECDH on
the hop side, which is out of scope (no reply path exists yet; see
`LabelCircuitDataRecv`, reserved for future use), would close it.

## Memory DoS

Every mutable, remote-input-driven collection in this codebase is
capacity-bounded, verified by a dedicated test in each case:

- `CircuitManager`: global (`MaxCircuits`) and per-first-hop-peer
  (`MaxCircuitsPerPeer`) caps (`TestCircuitManagerEnforcesMaxCircuits`,
  `TestCircuitManagerEnforcesMaxCircuitsPerPeer`).
- `relayCircuitState`: capacity-bounded relay replay-window table
  (`TestRelayCircuitStateBoundedCapacity`).
- `ReplayWindow`: fixed 2048-bit bitmap regardless of counter magnitude
  (`TestReplayWindowMemoryStaysBounded`).
- `RateLimiter`: bounded tracked-peer count, fails closed once full
  (`TestRateLimiterBoundedTrackedPeers`).
- Every wire parser (`Envelope`, `LayerPlaintext`, `Bundle`,
  `CapabilityMessage`) validates a declared length against both a
  maximum constant and the actual remaining buffer *before* allocating
  or slicing — never trusts a length prefix enough to allocate based on
  it alone.

## CPU DoS

`RateLimiter` (token bucket per peer node key) gates every incoming
Garlic message in `Garlic.handleIncoming` before any parsing or crypto
runs (`if !g.limiter.Allow(from) { return }`) — the most expensive
operations in the receive path (ECDH: ~79μs per the benchmarks in
`docs/garlic-architecture.md`'s companion benchmark run) are gated
behind this check, so a peer that floods traffic cannot force unbounded
ECDH computation. `handleIncoming` never blocks
(`core.GarlicHandler`'s documented contract) — it's called synchronously
from `Core.ReadFrom`'s loop, so a slow handler would stall ordinary
traffic too; every branch in `handleIncoming` is O(1) bounded work.

## Sybil attacks

Partially mitigated — `SelectDiversePath` (`src/garlic/selection.go`)
and multipath pools (`src/garlic/multipath.go`) both raise the cost of
the simplest Sybil strategies (see `docs/garlic-threat-model.md`'s
Sybil section for the detailed breakdown). Neither is a general
solution: there is still no reputation system, no resource cost to
registering as a Garlic identity, and no IP/ASN diversity signal —
tree position (spanning-tree parent, mesh hop count) is the only
diversity signal available, and an adversary with genuinely diverse
tree positions defeats it entirely.

## Intersection attacks

Narrowed by an architectural property, not a dedicated defense: every
Garlic-capable node is structurally both a possible circuit originator
and a relay for other nodes' circuits, so an adversary observing that a
node sent/received Garlic traffic cannot, from that fact alone, tell
whether it was the real endpoint or just relaying. See
`docs/garlic-threat-model.md`'s Intersection attacks section for what
this does and does not rule out — it is eroded by the same
traffic-correlation limits discussed there, and there is no active
circuit-rotation *policy* enforced by this codebase (only
`Config.CircuitLifetime`'s upper bound).

## Malformed input handling

Every parser was fuzzed (`src/garlic/fuzz_test.go`): `Envelope.Unmarshal`,
`UnmarshalBundle`, `UnmarshalCapabilityMessage`, and the full
`processCircuitData` receive pipeline (which chains envelope parsing,
ECDH, KDF, and AEAD decryption) — roughly 3.6M generated executions
across all four targets in a 15s/target local run, zero crashes.
Malformed input at every layer returns a generic error rather than
panicking; `Core.ReadFrom` (`src/core/core.go`) itself has no code path
that can panic on an unrecognized or malformed `typeSessionGarlic`
payload — verified by `TestCore_GarlicHandler_UnregisteredHandlerDropsSilently`
and, transitively, the fuzz corpus.

## Error handling / information leakage in errors

Per the "generic protocol errors" requirement: `processCircuitData`
returns the same `actionDrop` outcome for every failure mode (expired,
replayed, relay table full, wrong recipient, tampered ciphertext,
malformed envelope) — nothing in the wire protocol distinguishes them,
verified by `TestProcessCircuitDataDropsWrongRecipient` and its sibling
tests all asserting the identical `actionDrop` result. This does mean
Go-level error *values* (`ErrDecryptionFailed`, `ErrEnvelopeTruncated`,
etc.) exist and are descriptive for local debugging/logging — they are
never serialized back to a remote peer, since there is no error-response
message type in the protocol at all (§8 of `docs/garlic-protocol.md`).

## Summary: what would most improve this implementation next

Padding, jitter, discovery/gossip, diverse hop selection, multipath
pools, and cover-traffic bundling (items 1 and 3 from the prior version
of this list) are now implemented and described above. Per-hop ephemeral
keys, HKDF domain separation, 128-bit `CircuitID`s, and signed service
descriptors (the crypto-hardening pass — see
`docs/superpowers/specs/2026-08-09-garlic-crypto-hardening-design.md`)
are now also implemented, closing out what was items 1 and (new) 4 from
an earlier version of this list — see "Identity correlation" and
"Forward secrecy" above for what they closed and their residual scope,
and the new "Service descriptor authentication" note below. Per-hop
rewriting of `CircuitID`/`PacketCounter`/`Expiration` — the residual
linkability signal item 1 used to flag in an earlier version of this
list — is now also implemented, via the hop-local envelope format
(`EnvelopeVersion2`/`garlic-v3`, the 2026-08-24 pass — see the new
paragraph above and `docs/garlic-protocol.md`'s "Hop-local envelope
format" section) for every circuit this node originates; a circuit
relayed on behalf of a not-yet-upgraded peer still uses the legacy,
verbatim-copy `EnvelopeVersion1` format for that circuit specifically,
since this node never controls what format a peer it relays for
originated with. Remaining priority order:

1. IP/ASN-diversity-aware Sybil resistance — `SelectDiversePath`'s only
   signal today is spanning-tree position, which a topologically
   diverse adversary defeats; a real improvement needs a diversity
   signal that doesn't itself leak relay operators' real IPs through
   gossip (see `docs/garlic-threat-model.md`'s Sybil section for why
   that tradeoff isn't free).
2. A deliberate circuit-rotation policy (when to build a new circuit,
   how much to relay for others as camouflage) to further narrow
   intersection attacks — today only `Config.CircuitLifetime`'s upper
   bound exists; there is no policy actively deciding *when* within
   that bound to rotate.
3. A distributed `Rendezvous` implementation, with its own threat-model
   pass first (`docs/garlic-rendezvous.md`). Descriptor *authentication*
   (GID self-certification, Ed25519 signature, expiry) is now solved
   independent of how descriptors get distributed — a distributed
   backend would inherit that authentication for free, since
   verification lives in `Garlic.LookupService`, not in any particular
   `Rendezvous` implementation — but distribution itself remains
   `StaticRendezvous`-only.

## Service descriptor authentication (new since the version of this document above predates it)

`ServiceDescriptor` (`src/garlic/descriptor.go`) replaced the original
unsigned, unauthenticated `IntroPoint` list this document's "Route /
destination leakage" section above still described implicitly through
`docs/garlic-protocol.md` §6. `GID = ComputeGID(ServicePublicKey,
ServiceID)` is now bound to the new Ed25519 signing key (not the X25519
circuit-ECDH key), making the GID self-certifying: nobody can produce a
descriptor that both signs correctly *and* hashes to a given GID without
holding that GID's signing private key. `Garlic.LookupService`
(`src/garlic/manager.go`) verifies every descriptor a `Rendezvous`
returns — GID match, Ed25519 signature, and `ExpiresAt` against the
local clock — before trusting its `IntroPoints`
(`VerifyServiceDescriptor`, `src/garlic/descriptor.go`). A malicious or
compromised rendezvous can still withhold, reorder, or serve a
stale-but-still-validly-signed descriptor; it cannot forge one for a GID
it doesn't hold the signing key for. See `docs/garlic-rendezvous.md` for
the full trust-boundary description.
