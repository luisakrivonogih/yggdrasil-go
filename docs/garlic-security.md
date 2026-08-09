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
| Key derivation | HKDF-SHA256 (`golang.org/x/crypto/hkdf`) | `DeriveKey`, explicit domain-separation label per purpose (`LabelLayerKey`, `LabelCircuitKey` — the latter currently unused, reserved) |
| Authenticated encryption | XChaCha20-Poly1305 (`golang.org/x/crypto/chacha20poly1305`) | `Seal`/`Open`, 24-byte nonce |
| Nonce generation | Deterministic from caller-supplied counter | Right-aligned into a zero-padded 24-byte buffer (`nonceFromCounter`) |
| Service identifier hash | BLAKE2b-256 (`golang.org/x/crypto/blake2b`) | `ComputeGID`, with domain separator |
| Key lifetime | Long-term `Identity`: until rotated by config change. Ephemeral per-circuit key: one circuit's lifetime (`Config.CircuitLifetime`, default 10m) | |
| Rekey procedure | Build a new `Circuit` (fresh ephemeral keypair) and retire the old one once its lifetime/packet/byte budget is exhausted | No in-place key rotation within one `Circuit` |

No custom cipher, no bare-hash-as-encryption, no unauthenticated
encryption anywhere in this package — every ciphertext produced by
`Seal`/`EncryptLayer` carries a Poly1305 tag, and every `Open`/
`DecryptLayer` call verifies it before returning plaintext.

## Identity correlation

- **Long-term Garlic identity is independent of the Yggdrasil node
  identity** (`docs/garlic-architecture.md` §1.1) — an X25519 keypair,
  never derived from the node's ed25519 key. Compromise of one doesn't
  reveal the other.
- **Capability responses correlate a node key to "runs Garlic-v1" and to
  a specific Garlic public key.** This is an intentional, necessary
  disclosure (you can't select a hop you can't verify), but it does mean
  a passive-ish observer who can send capability requests (anyone) can
  build a map of which Yggdrasil node keys are Garlic-capable and what
  their Garlic public keys are. Not mitigated, and not mitigable without
  removing the capability-response feature itself.
- **Ephemeral-key reuse across a circuit's hops** (flagged in
  `docs/garlic-threat-model.md` under "malicious relay") is the concrete
  identity/circuit-correlation weakness in this version.

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

Not actively mitigated. `SendGarlic` sends immediately; there is no
jitter, batching, or fixed-interval sending. This is explicit in
`docs/garlic-threat-model.md`'s "global passive adversary" and "traffic
correlation" sections.

## Packet size leakage

`Envelope.PadTo` and `Bundle.AddCoverMessage` exist and are tested
(`docs/garlic-protocol.md` §7) but are **not called by `SendGarlic`** in
this version — packets sent today are exactly the size of their
(unpadded) content. This is the single highest-value near-term follow-up
for traffic-analysis resistance: wiring `PadTo` into `SendGarlic`
using `Config`'s (currently unused for this purpose) cell-size concept
requires no new cryptography, just plumbing.

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

**Partial, not complete.** Per-circuit ephemeral keys mean compromising
one circuit's derived keys doesn't expose other circuits (past or
future) between the same two identities — this is real forward secrecy
at the circuit granularity. However, compromising a hop's **long-term**
Garlic private key retroactively allows recomputing every past circuit's
`secret_i = ECDH(hop_private, ephemeral_public)` for any circuit whose
traffic was recorded, *if* the ephemeral public key was observed
(§4.1 of the protocol doc — it travels in the clear-to-the-hop portion of
every circuitData message). A design with per-circuit hop-side ephemeral
keys too (mutual ECDH) would close this gap; this version does not
attempt it.

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

Not mitigated — stated plainly in `docs/garlic-threat-model.md`. No
reputation or diversity-weighted path selection exists in this version.

## Intersection attacks

Not mitigated — stated plainly in `docs/garlic-threat-model.md`.

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

In priority order, based on this review:

1. Wire `Envelope.PadTo`/`Bundle.AddCoverMessage` into the default send
   path (packet-size leakage is currently the largest gap between
   "implemented" and "designed for").
2. Per-hop ephemeral keys (not one shared per circuit) to remove the
   relay-collusion linkability signal and improve forward secrecy.
3. Sybil-resistant path selection once any automated hop-selection logic
   is built (none exists yet — today a human or caller picks the path).
4. A distributed `Rendezvous` implementation, with its own threat-model
   pass first (`docs/garlic-rendezvous.md`).
