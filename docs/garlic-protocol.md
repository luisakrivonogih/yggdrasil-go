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

Any other value, or an empty payload, is silently dropped by
`Garlic.handleIncoming` — no error, no response, matching the "generic
protocol errors" requirement (§17 of the original brief).

## 2. Garlic Envelope

`src/garlic/envelope.go`. The structure every `msgTypeCircuitData`
message's onion-layer ciphertext is wrapped in on the wire, and the unit
`Envelope.PadTo` normalizes to a fixed size.

```
offset  size  field
0       1     version            (currently always 1)
1       8     circuit_id         (uint64)
9       8     packet_counter     (uint64)
17      8     expiration         (uint64, Unix seconds)
25      4     body_len           (uint32)
29      body_len   body          (opaque - AEAD ciphertext at the layer level)
29+body_len  4      padding_len  (uint32)
...     padding_len  padding    (opaque, ignored on decode)
```

Fixed header size: 29 bytes. `MaxBodySize` and `MaxPaddingSize` are both
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
`"garlic-v1"` (`CapabilityGarlicV1`) in this implementation, but the
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

`circuitDataMinSize = 32 + 29 = 61` bytes is the minimum a well-formed
message can be; anything shorter is dropped immediately.

### 4.1 Per-hop key derivation (non-interactive)

The circuit's originator generates **one ephemeral X25519 keypair per
circuit** (not per hop). For hop *i* with long-term Garlic public key
`P_i` (learned via §3), the originator computes:

```
secret_i = X25519(ephemeral_private, P_i)
key_i    = HKDF-SHA256(secret_i, salt=nil, info="yggdrasil-garlic-v1-layer-key")
```

Hop *i*, on receipt, independently computes the same `secret_i` via
`X25519(P_i_private, ephemeral_public)` (Diffie-Hellman symmetry) and the
same `key_i` via the identical HKDF call — **no interactive handshake is
needed to establish `key_i`.** This is a deliberate simplification over
Tor-style telescoping circuit construction; see
`docs/garlic-security.md` §"Ephemeral key linkability" for the privacy
cost of reusing one ephemeral public key across all hops of a circuit.

### 4.2 Layer plaintext

`src/garlic/layer.go`. What `key_i` decrypts `Envelope.Body` into:

```
offset  size          field
0       4             next_hop_len       (max 256)
4       next_hop_len  next_hop_key       (empty ⟺ this is the terminal hop)
...     4             inner_len          (max 65535, = MaxBodySize)
...     inner_len     inner              (next layer's ciphertext, or the
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
   `PacketCounter`, and `Expiration`, `Body = Inner`, and forward
   `msgTypeCircuitData || ephemeral_public_key || new_envelope` to
   `NextHop` unchanged. The ephemeral public key is passed through
   byte-for-byte so every subsequent hop can perform the same §4.1
   derivation with its own private key.

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

## 6. Identity and GID

`src/garlic/identity.go`, `src/garlic/gid.go`. A node's long-term Garlic
identity is an X25519 keypair, independent of its Yggdrasil ed25519
identity. A Garlic Service ID:

```
GID = version_byte(1) || BLAKE2b-256("yggdrasil-garlic-v1-gid" || public_key || service_id)
```

35 bytes total, canonically encoded as unpadded base32
(`gidEncoding = base32.StdEncoding.WithPadding(base32.NoPadding)`).
Computable and verifiable by anyone who knows `public_key` and
`service_id`; never derived from or convertible to the underlying
Yggdrasil IPv6 address.

## 7. Bundling

`src/garlic/bundle.go`. Not currently wired into the send/receive path
described in §4 — it exists as a standalone, tested primitive for future
use (multiple independent messages per garlic packet, §3.7 of the
architecture doc). Wire format:

```
offset  size   field
0       4      message_count   (max 32)
4       ...    per message: len(4) + bytes   (max 65535 bytes each)
```

## 8. What this version does not define

- No wire format for circuit teardown/error signaling — a dead or
  uncooperative hop is currently only detected by the originator's own
  `SendGarlic`/timeout logic at the application layer, not a protocol
  message.
- No reply/return-path mechanism — `RecvGarlic` delivers what arrives at
  the terminal hop of someone else's circuit; there is no built-in way
  for that node to talk back over the same circuit.
- No distributed rendezvous wire protocol — see
  `docs/garlic-rendezvous.md`.
