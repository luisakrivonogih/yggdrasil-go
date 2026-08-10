# Garlic Routing Overlay — Protocol Specification v1

Status: experimental. Describes what is actually implemented in
`src/garlic` as of this writing, not an aspirational future design. See
`docs/garlic-architecture.md` for the integration rationale and
`docs/garlic-threat-model.md` for what this protocol does and does not
protect against.

All integers are big-endian. All byte offsets are 0-indexed. "MUST NOT
panic" applies to every parser described here regardless of input.

## 1. Transport framing

Every Garlic message travels as the payload of a `core.Core.WriteGarlic`
call, which core.go tags with a single byte (`typeSessionGarlic`) ahead of
it before handing it to ironwood's already end-to-end-encrypted
`PacketConn`. That tag byte is core.go's concern, not this document's —
everything below describes the bytes *after* that tag, i.e. what a
registered `core.GarlicHandler` receives.

The first byte of that payload is the **Garlic message type**
(`src/garlic/protocol.go`):

| Value | Name | Meaning |
|-------|------|---------|
| `0x01` | `msgTypeCapabilityRequest` | "Do you support Garlic, and what's your public key?" No body. |
| `0x02` | `msgTypeCapabilityResponse` | Answer to the above; body is a `CapabilityMessage` (§3). |
| `0x03` | `msgTypeCircuitData` | One onion-routed packet; body is described in §4. |
| `0x04` | `msgTypeAnnounce` | Gossip of known Garlic-capable peers; body is an `AnnounceMessage` (§8). |
| `0x05` | `msgTypeCircuitDataBundle` | Several `msgTypeCircuitData`-shaped entries (real traffic mixed with cover entries) carried together; body is a `Bundle` (§7). |

Any other value, or an empty payload, is silently dropped by
`Garlic.handleIncoming` — no error, no response, matching the "generic
protocol errors" requirement (§17 of the original brief).

## 2. Garlic Envelope

`src/garlic/envelope.go`. The structure every `msgTypeCircuitData`
message's onion-layer ciphertext is wrapped in on the wire.
`Envelope.PadTo` normalizes to a fixed size; `Envelope.PadToRandomRange`
picks a fresh random target size in `[min, max]` on every call instead —
this is what `Config.PaddingEnabled` actually drives on the send/relay
path (§9), so consecutive envelopes on the same link don't share a
size an observer could use as a fingerprint.

```
offset  size  field
0       1     version            (currently always 1)
1       16    circuit_id         (CircuitID, 128-bit random value)
17      8     packet_counter     (uint64)
25      8     expiration         (uint64, Unix seconds)
33      4     body_len           (uint32)
37      body_len   body          (opaque - AEAD ciphertext at the layer level)
37+body_len  4      padding_len  (uint32)
...     padding_len  padding    (opaque, ignored on decode)
```

Fixed header size: 37 bytes (`envelopeFixedHeaderSize = 1 + 16 + 8 + 8 + 4`).
`MaxBodySize` and `MaxPaddingSize` are both
65535 (matching `core.Core.MTU()`'s own cap) — `Unmarshal` rejects a
declared `body_len`/`padding_len` against this cap *before* checking it
against the actual remaining buffer, so an attacker's claimed length can
never drive an allocation before it's validated.

`Unmarshal` never aliases its input: `Body`/`Padding` are always copied
out, so mutating the caller's buffer after `Unmarshal` returns cannot
retroactively corrupt the parsed `Envelope`.

## 3. CapabilityMessage

`src/garlic/capability.go`. The body of a `msgTypeCapabilityResponse`
message.

```
offset  size  field
0       1     version_count           (max 16)
1       ...   per version: len(1) + bytes   (max 32 bytes each)
...     1     key_len                 (max 64)
...     key_len   public_key
```

`Versions` currently only ever contains the single string
`"garlic-v2"` (`CapabilityGarlicV2`) in this implementation, but the
format allows a future node to advertise several. `PublicKey` is the
responder's long-term Garlic X25519 public key (§6).

A **timeout** (no response within `Config.CapabilityTimeout`, default 6s)
is the only signal a querying node has that a peer is legacy or
Garlic-disabled — indistinguishable from an unresponsive Garlic-capable
node, by design (there is nothing to distinguish; both cases mean "do not
use this node as a circuit hop").

## 4. Circuit data message (onion routing)

Body of a `msgTypeCircuitData` message:

```
offset  size       field
0       32         ephemeral_public_key   (X25519, KeySize)
32      ...        Envelope (§2), whose Body is this hop's layer ciphertext
```

`circuitDataMinSize = 32 + 37 = 69` bytes (`KeySize + envelopeFixedHeaderSize`)
is the minimum a well-formed message can be; anything shorter is dropped
immediately.

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
layer as `LayerPlaintext.next_hop_ephemeral` (§4.2) — a hop only learns
the next hop's ephemeral key by successfully decrypting its own layer,
never before. Hop *i*, on receipt, independently computes the same
`secret_i` via `X25519(P_i_private, ephemeral_i_public)`
(Diffie-Hellman symmetry) and the same `key_i` via the identical
two-stage HKDF chain — no interactive handshake is needed to establish
`key_i`.

This gives the property that non-adjacent hops (e.g. hop 1 and hop 3 of
a 3-hop circuit) never observe a common ephemeral public key and cannot
link a circuit by comparing them — see
`docs/garlic-threat-model.md`'s "Malicious relay" section and
`TestNonAdjacentHopsCannotLinkViaEphemeralKeys`
(`src/garlic/linkability_test.go`). It is the same shape as Tor's
classical (non-Sphinx) telescoping circuit construction: an immediate
predecessor hop necessarily relays its successor's ephemeral public key
as plain routing information (it has to, to address the next hop) but
never learns that key's private half.

`LabelCircuitDataRecv` (`"yggdrasil-garlic-v2-circuit-data-recv"`) is
reserved in the same derivation chain for a future reply/return path —
no circuit today carries traffic in that direction, so it is currently
unused.

### 4.2 Layer plaintext

`src/garlic/layer.go`. What `key_i` decrypts `Envelope.Body` into:

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

AEAD: XChaCha20-Poly1305 (`golang.org/x/crypto/chacha20poly1305`), 24-byte
nonce derived deterministically as `packet_counter` right-aligned into a
zero-padded 24-byte buffer — safe only because `(key_i, packet_counter)`
is never reused (§5).

### 4.3 Relay behavior

On receipt of a `msgTypeCircuitData` body (`Garlic.processCircuitData`,
pure/no I/O — see `docs/garlic-architecture.md` §3.1 for why this is
split from the I/O wrapper):

1. Reject if shorter than `circuitDataMinSize`.
2. Parse the `Envelope`; reject on any parse error or unsupported version.
3. Reject if `Envelope.Expiration` is in the past.
4. Look up (or create, capacity permitting) this circuit ID's relay-side
   `ReplayWindow` (`src/garlic/relaystate.go`); reject if the table is
   full or `PacketCounter` is a replay.
5. Derive `key_i` per §4.1; attempt `DecryptLayer`. Any failure here
   (wrong key because the message wasn't meant for this identity,
   tampered ciphertext, or a malformed plaintext after decryption) is
   treated identically: drop, no error surfaced.
6. If the recovered `NextHop` is empty: deliver `Inner` locally
   (`Garlic.RecvGarlic`).
7. Otherwise: rebuild an `Envelope` with the same `CircuitID`,
   `PacketCounter`, and `Expiration`, `Body = Inner`. If
   `Config.PaddingEnabled`, this hop independently re-rolls
   `Envelope.PadToRandomRange(MinPaddedSize, MaxPaddedSize)` before
   marshaling — the outgoing wire size on this hop's outbound link is
   unrelated to the size this hop received on its inbound link, by
   design (§9). Forward
   `msgTypeCircuitData || next_hop_ephemeral || new_envelope` to
   `NextHop`, where `next_hop_ephemeral` is the value this hop just
   decrypted from its own layer's `LayerPlaintext.next_hop_ephemeral`
   (§4.2) — **not** the ephemeral public key this hop itself received.
   A message whose decrypted layer has a non-empty `next_hop` but an
   absent `next_hop_ephemeral` is malformed and dropped rather than
   forwarded.

## 5. Replay protection

Two independent replay windows exist, both a fixed 2048-bit sliding
bitmap (`src/garlic/replay.go`), bounded regardless of how far or
erratically an attacker drives the counter:

- **Relay-side**, keyed by `CircuitID`, one per circuit a node is
  currently relaying for (`relayCircuitState`, itself capacity-bounded —
  a new circuit ID is refused once the table is full).
- The **originator** never replay-checks its own sends; it is the sole
  source of `PacketCounter` values for a circuit it created, and
  `Circuit.Seal` guarantees they strictly increase per hop, per call.

`CircuitID` is a 128-bit value drawn from `crypto/rand`
(`src/garlic/circuit.go`, `randomCircuitID`) — widened from the original
64 bits purely for collision resistance under random generation (there
is no other bound it needs to satisfy: it carries no integer semantics,
only equality comparison and use as a map key). `CircuitManager` (the
originator's own circuit table) additionally guards against the
vanishingly unlikely case of a locally-generated ID colliding with one
it's already tracking, refusing the insert rather than silently
overwriting the existing circuit's state (`ErrCircuitIDCollision`).

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

followed by a 64-byte Ed25519 `signature` over exactly those bytes — no
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

## 7. Bundling

`src/garlic/bundle.go`. Wire format:

```
offset  size   field
0       4      message_count   (max 32)
4       ...    per message: len(4) + bytes   (max 65535 bytes each)
```

Wired into the send path via `Garlic.SendGarlicBundled(id, payload,
coverCount)`: it builds the one real `circuitData` entry (§4, same
shape as a standalone message's body — `ephemeral_public_key ||
Envelope`), appends `coverCount` cover entries via
`Bundle.AddCoverMessage` (random bytes, sized to match the real entry),
shuffles the entry order (Fisher-Yates, `shuffleBundleMessages`), and
sends the result as one `msgTypeCircuitDataBundle` message.

On receipt, `Garlic.processCircuitDataBundle` unmarshals the bundle and
runs **every** entry through the exact same `processCircuitData`
pipeline used for a standalone message (§4.3) — no separate code path,
no weaker checks. A cover entry has no valid ephemeral key/ciphertext
relationship, so it fails `DecryptLayer` and is dropped exactly like a
corrupted or misdirected message already is; the real entry (if this
node is a hop for it) is delivered or forwarded normally. Each
non-drop outcome is acted on independently, so a bundle's entries need
not belong to the same circuit or even be addressed through the same
next hop.

This is the actual "garlic" property (as opposed to a single onion
stream): an observer — including a hop that isn't the intended
recipient of any entry in the bundle — cannot decrypt any entry, and
therefore cannot tell which one, if any, is real, or even how many of
the bundle's entries are real versus chaff. See
`docs/garlic-threat-model.md`'s traffic-correlation section for what
this does and does not defend against.

## 8. Discovery / gossip (`msgTypeAnnounce`)

`src/garlic/discovery.go`. Lets a node learn about Garlic-capable peers
it has never directly queried itself, entirely over the
`typeSessionGarlic` channel — a node that doesn't run `src/garlic`
cannot construct, parse, or respond to this message type, so discovery
is only ever visible to other Garlic nodes, by construction (the same
property capability negotiation already has).

Body of a `msgTypeAnnounce` message (`AnnounceMessage`):

```
offset  size  field
0       4     peer_count            (max 32)
4       ...   per peer:
              1     node_key_len    (max 64)
              ...   node_key
              1     garlic_key_len  (max 64)
              ...   garlic_key
```

`Garlic.processAnnounce` records every entry with both keys non-empty
into the local `discoveryRegistry` (bounded, evicts the
least-recently-seen entry once full) — it does **not** treat this as
trust: a gossiped entry is only ever used as a circuit hop after its
own `QueryCapability` round trip succeeds, same as a directly-learned
peer. `Garlic.GossipAnnounce` sends a sample of the local registry
(`Config.GossipSampleSize`) to one peer; a background tick
(`Config.GossipInterval`) calls it for up to `Config.GossipFanout`
peers this node has itself already capability-verified
(`capabilityCache`), never an unverified discovery candidate — so
gossip only propagates outward from nodes this instance has confirmed
are running Garlic.

## 9. Timing and size defenses

Two independent, per-packet randomizations apply to every
`msgTypeCircuitData` send or forward when enabled (both default on,
`Config.PaddingEnabled`/`Config.JitterEnabled`):

- **Size** (§2): `Envelope.PadToRandomRange(MinPaddedSize,
  MaxPaddedSize)`, re-rolled independently by the originator and by
  every relay forwarding the packet — so a packet's size on one
  hop-to-hop link carries no information about its size on the next.
- **Timing**: `Garlic.sendCircuitData` hands every outgoing packet to a
  bounded worker-pool scheduler (`src/garlic/jitter.go`) with a delay
  drawn uniformly from `[MinJitter, MaxJitter]`, independently rolled
  per packet, before it's actually transmitted. The scheduler never
  blocks the caller (a full queue falls back to sending immediately)
  — required because relay forwarding happens synchronously inside
  `core.Core.ReadFrom`'s read loop.

Neither is a general-purpose mixnet: there is no fixed-interval
batching, and a global adversary watching both ends of a circuit
simultaneously can still attempt statistical correlation over enough
samples. See `docs/garlic-threat-model.md`'s "Traffic correlation"
section for what this does and does not raise the cost of.

## 10. Path selection and multipath

Node-local behavior, not a wire message, but it materially changes
what's observable on the wire:

- `Garlic.SelectPath(n)` (`src/garlic/selection.go`) picks `n`
  circuit-hop candidates from this node's discovered/verified Garlic
  peers, preferring topologically distant candidates
  (`core.Core.GetPaths()`'s hop count) and avoiding two candidates that
  share an immediate tree parent (`core.Core.GetTree()`) — a cheap
  signal they might be run by the same operator or sit on the same
  local segment. This is a heuristic, not Sybil resistance (see
  `docs/garlic-threat-model.md`'s Sybil section for what it does not
  solve).
- `Garlic.CreateCircuitPool`/`SendGarlicMultipath`
  (`src/garlic/multipath.go`) build several independent circuits and
  round-robin sends across them, so a given circuit's link carries only
  a fraction of one conversation's total traffic — an adversary
  positioned on (or colluding across) only some of the pool's paths
  sees only that fraction.

## 11. What this version does not define

- No wire format for circuit teardown/error signaling — a dead or
  uncooperative hop is currently only detected by the originator's own
  `SendGarlic`/timeout logic at the application layer, not a protocol
  message.
- No reply/return-path mechanism — `RecvGarlic` delivers what arrives at
  the terminal hop of someone else's circuit; there is no built-in way
  for that node to talk back over the same circuit.
- No distributed rendezvous wire protocol — see
  `docs/garlic-rendezvous.md`.
