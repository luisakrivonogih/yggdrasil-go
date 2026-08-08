# Garlic Routing Overlay — Architecture (Phase 1)

Status: **experimental design proposal, no implementation yet.**
Scope: this document covers Phase 1 of the roadmap only — study the existing
codebase, identify integration points, and propose an architecture. It does
not define the wire format byte-for-byte (`garlic-protocol.md`), the full
threat model (`garlic-threat-model.md`), or the compatibility test matrix
(`garlic-compatibility.md`) — those are later phases and are referenced but
not written here.

Baseline: yggdrasil-go @ `422836e` ("Yggdrasil 0.5.14"), protocol version
`0.5` (`src/core/version.go`).

Terminology note (per project convention): this system must never be called
"anonymous" until a real threat model backs that claim. Use **privacy-enhanced
routing**.

---

## 1. Existing architecture (as found)

Yggdrasil-go does **not** implement its own mesh routing, DHT, or session
crypto. That all lives in the external module `github.com/Arceliar/ironwood`
(`go.mod`). `yggdrasil-go` is the application-facing shell around it: TUN
plumbing, transport/link management, config, and the admin API. This matters
a great deal for the design below — most of what a "Garlic layer" needs
(end-to-end encryption between arbitrary nodes, multi-hop delivery through
nodes that never see the payload) **already exists** in ironwood and doesn't
need to be rebuilt.

### 1.1 Node identity & keys

- Long-term identity is a single `ed25519` keypair (`config.NodeConfig.PrivateKey`,
  [src/config/config.go](../src/config/config.go)). Public key = node identity.
- No separate encryption keypair at the yggdrasil-go level — ironwood derives
  whatever session/box keys it needs internally from this identity; yggdrasil-go
  never touches that machinery directly.
- The private key can be inline (hex/JSON) or loaded from a PEM file
  (`PrivateKeyPath`), and a self-signed TLS certificate is derived from it
  for use by the TLS transport (`GenerateSelfSignedCertificate`).

### 1.2 IPv6 address generation

- [src/address/address.go](../src/address/address.go): `AddrForKey`/`SubnetForKey`
  deterministically derive a `/128` address or `/64` subnet from an ed25519
  public key by bit-inverting the key and counting leading-1s as a
  self-describing length prefix, under a fixed prefix byte (`GetPrefix() = 0x02`).
  `GetKey`/`GetKey` invert this to recover the (partial) public key from an
  address — this is how the DHT resolves "IP → key to look up".
- This scheme is load-bearing for the whole network (every node's address
  *is* a function of its key) and is explicitly out of scope to change
  (constraint from the task, and there is no technical need to touch it —
  see §4).

### 1.3 Transport / peer connections

- `src/core/link*.go`: pluggable transports — TCP, TLS, WS, WSS, QUIC, SOCKS —
  each implementing a common `link` abstraction that ironwood treats as a
  raw framed byte stream between two directly-peered nodes.
- Peering is configured via URIs (`tls://host:port`, etc.) in
  `NodeConfig.Peers` / `Listen`. Optional `AllowedPublicKeys` and
  `GroupPassword` provide connection-level allow-listing, unrelated to Garlic.

### 1.4 Wire handshake

- [src/core/version.go](../src/core/version.go): a small TLV structure
  (`version_metadata`) is exchanged once per *link* (i.e. per directly-peered
  connection, not per arbitrary remote node), signed with the sender's
  ed25519 key over a `GroupPassword`-keyed BLAKE2b hash. Carries major/minor
  protocol version, public key, priority.
- The decoder walks TLV fields by opcode and, verified by reading
  `version_metadata.decode` ([version.go:119-152](../src/core/version.go#L119-L152)),
  already tolerates unrecognized opcodes: unknown `op` values fall through
  the `switch` untouched and the loop still advances by the field's declared
  length, so appending a new TLV field wouldn't break old parsers. Adding a
  capability flag here is *technically* possible without breaking the
  decoder. We still don't propose it — see §3.2 for the scope reason
  (per-link vs. multi-hop), not a technical one.

### 1.5 Routing & packet forwarding

- Entirely delegated to ironwood (`network` package for routing/DHT,
  `encrypted` package for the end-to-end encrypted `PacketConn` built on top
  of it). yggdrasil-go's `core.Core` embeds `*iwe.PacketConn` directly
  ([src/core/core.go:24-29](../src/core/core.go#L24-L29)).
- Practical consequence: **intermediate relay nodes in the mesh never see
  plaintext payload for traffic that isn't addressed to them.** Ironwood's
  `encrypted.PacketConn` gives every pair of node keys an authenticated
  encrypted channel; nodes forwarding frames between two other nodes are
  doing so at the routing layer, blind to payload content, exactly as they
  are for ordinary IPv6 traffic today. Garlic doesn't need to invent this
  property — it's inherited for free from the base network.
- What ironwood's per-hop encryption does *not* hide is the
  metadata/relationship: that node A directly exchanged an end-to-end
  session with node B at all (routing coordinates, tree/DHT structure,
  directly observable by A's and B's own peers, and to some extent by
  passive observers of the topology). That's the actual gap Garlic exists to
  address — see §7.

### 1.6 In-band session multiplexing (the key extension point)

[src/core/types.go](../src/core/types.go) and
[src/core/core.go](../src/core/core.go):

```go
const (
    typeSessionDummy = iota
    typeSessionTraffic  // ordinary IPv6 packets, delivered to TUN
    typeSessionProto    // in-band control protocol (NodeInfo, debug queries)
)
```

`Core.ReadFrom` ([core.go:175-208](../src/core/core.go#L175-L208)) reads a raw
packet off the ironwood `PacketConn`, inspects the first byte, and either:

- hands it to the TUN path (`typeSessionTraffic`),
- hands it to `protoHandler.handleProto` (`typeSessionProto`), which further
  dispatches on a second byte (`typeProtoNodeInfoRequest`,
  `typeProtoNodeInfoResponse`, `typeProtoDebug`, ...;
  [src/core/proto.go](../src/core/proto.go), [src/core/nodeinfo.go](../src/core/nodeinfo.go)), or
- **silently drops it** for any other value (`default: continue`, no error,
  no log — [core.go:196](../src/core/core.go#L196)).

`NodeInfo` is the existing precedent for exactly the kind of thing Garlic
needs: an application-level request/response protocol addressed to an
arbitrary node's public key (`iwt.Addr(key[:])`), riding over the same
already-encrypted, already-routed channel as user traffic, requiring **zero**
changes to links, handshake, or routing. It just adds a byte tag and a
handler.

### 1.7 Existing encryption

All of it is ironwood's: per-link and end-to-end session encryption, plus
BLAKE2b-keyed signing in the link handshake. yggdrasil-go itself does not
currently roll any cryptographic primitive of its own beyond that handshake
signature. This is a good sign for Garlic: there's no existing in-repo crypto
convention to be inconsistent with, and no existing crypto to weaken or
duplicate.

### 1.8 Serialization

No protobuf, no schema-driven serialization anywhere in the repo. The
conventions in use are: hand-rolled TLV binary encoding (`version.go`) for
wire handshake data, plain `encoding/binary` for internal packet type tags,
and JSON (via `encoding/json`, with `hjson` for the human-edited config file)
for config and the admin socket protocol. A Garlic wire format should follow
the binary-TLV convention for on-mesh packets (compactness, no reflection)
and JSON for anything config- or admin-API-facing, matching both existing
style and existing dependencies (no new serialization library needed).

### 1.9 Configuration & module/API conventions

- `config.NodeConfig` ([src/config/config.go](../src/config/config.go)) is
  one flat struct, hjson-encoded, with `GenerateConfig()` producing defaults
  and `ReadFrom` layering a user file on top of those defaults — so adding a
  new nested block is additive and safe as long as its zero value means "off".
- Optional subsystems (`admin`, `multicast`, `tun`) are each an independent
  Go package with a `New(core *core.Core, log core.Logger, opts ...SetupOption) (*T, error)`
  constructor and their own `SetupOption` functional options, instantiated
  conditionally in [cmd/yggdrasil/main.go](../cmd/yggdrasil/main.go#L229-L281).
  None of them are imported by `src/core` — `core` only exposes hooks
  (`SetLogger`, `SetAdmin`, `AddHandler`, etc.) that the module wires itself
  into after construction. This is the dependency direction Garlic must
  follow: **`garlic` depends on `core`, never the reverse.**
- The admin socket ([src/admin/admin.go](../src/admin/admin.go)) is a local
  JSON request/response protocol; handlers are registered by name
  (`AddHandler(name, desc, args, handlerfunc)`) and reachable from
  `yggdrasilctl`. This is the natural home for a future `CreateGarlicIdentity`
  / `GetGarlicStats` style API (§3.10), not a new admin transport.

---

## 2. Integration points identified

Three concrete points, all already visited above:

1. **Where an ordinary IPv6 packet enters Yggdrasil**: the TUN device →
   `ipv6rwc.ReadWriteCloser.Write` → `keyStore.writePC`
   ([src/ipv6rwc/ipv6rwc.go:283](../src/ipv6rwc/ipv6rwc.go#L283)) → resolves
   destination IP to a key → `core.Core.WriteTo` (tags it
   `typeSessionTraffic`) → ironwood `PacketConn.WriteTo`.
2. **Where a packet is delivered toward its destination**: entirely inside
   ironwood; yggdrasil-go has no hook into path selection and this document
   does not propose adding one (§4).
3. **Where a packet can be intercepted before it reaches the application**:
   `Core.ReadFrom`'s type-byte switch ([core.go:187](../src/core/core.go#L187)).
   This is *the* interception point — it's where `typeSessionProto` already
   diverts control traffic away from the TUN/application path today, and
   it's where a new `typeSessionGarlic` tag would divert Garlic traffic the
   same way, before it ever reaches `ipv6rwc`/TUN/the application.

Point 3 is the whole architecture in miniature: Garlic does not need to sit
"in front of" or "behind" Yggdrasil's IPv6 path in the way the prompt's
idealized stack diagram suggests. It sits **beside** it, as a sibling
consumer of the same encrypted per-node channel, selected by a tag byte the
existing demux already supports the pattern for.

---

## 3. Proposed Garlic Overlay architecture

### 3.1 Layering (revised from the idealized version)

The originally-sketched stack (`Application → IPv6 → Yggdrasil
transport/routing → Garlic Overlay → Yggdrasil network`) implies Garlic sits
inline in the IPv6 path. That's not what the codebase supports cleanly, and
it's not what's needed. The actual shape:

```
        Application
             │
   ┌─────────┴─────────┐
   │                    │
  IPv6                 Garlic API (new)
  (TUN)                (circuits, GIDs, SendGarlic)
   │                    │
   ▼                    ▼
core.Core.WriteTo/ReadFrom  (existing; core.PacketConn)
   │  tag=typeSessionTraffic     │  tag=typeSessionGarlic (new)
   │  tag=typeSessionProto       │
   └─────────────┬───────────────┘
                  ▼
     ironwood encrypted.PacketConn
     (end-to-end session crypto, DHT routing — UNCHANGED)
                  │
                  ▼
         Yggdrasil mesh (links, legacy + new nodes alike)
```

Garlic is a **sibling consumer of the same multiplexed channel**, not a
layer the IPv6 path passes through. IPv6 traffic and Garlic traffic never
interact; they're just two tags sharing one already-encrypted, already-routed
pipe. This is the minimal-diff architecture the task calls for: it changes
zero routing logic and zero existing packet paths.

### 3.2 Why the wire handshake and routing are untouched

- Capability discovery does not need to happen at link-handshake time,
  because circuit membership isn't about direct peers — it's about
  arbitrary nodes anywhere in the mesh, most of which a given node never
  link-handshakes with directly. Even though `version_metadata` could
  technically carry an extra TLV field without breaking old decoders
  (§1.4), doing so would only tell a node about its *direct* peers'
  capability, not about the rest of the mesh it needs for path selection.
  An in-band request/response over the existing `typeSessionProto`-style
  channel (§3.4) reaches any node by key regardless of hop count, exactly
  like NodeInfo does today — so it's the strictly more useful mechanism,
  independent of the handshake question.
- Routing/path selection is ironwood's job and already delivers packets
  end-to-end by key. Circuit hops are a Garlic-level concept (a sequence of
  keys the sender chooses), not a routing-level one — each hop of a Garlic
  circuit is just an ordinary `core.PacketConn.WriteTo` call to that hop's
  key, which ironwood routes exactly as it would route anything else,
  through however many legacy nodes happen to sit on the path.

### 3.3 New package: `src/garlic`

Follows the `admin`/`multicast` convention: `garlic.New(core *core.Core, log
core.Logger, opts ...SetupOption) (*Garlic, error)`, wired in
`cmd/yggdrasil/main.go` conditionally on `cfg.Garlic.Enabled`. Two additions
to `src/core` are needed to support it, both additive:

- one new constant, `typeSessionGarlic`, in `src/core/types.go`;
- one new case in `Core.ReadFrom`'s switch and a registration hook
  (e.g. `func (c *Core) SetGarlicHandler(h func(from ed25519.PublicKey, data []byte))`)
  so `core` never imports `garlic`.

When `garlic.enabled = false` (the default), none of this activates: the
handler is nil, `ReadFrom` falls back to the existing `default: continue`
drop for the tag, and behavior is bit-for-bit identical to vanilla
Yggdrasil. This is what makes "garlic disabled ≈ vanilla" true by
construction rather than by careful testing.

### 3.4 Capability negotiation

A dedicated request/response pair under `typeSessionProto`, structurally
identical to NodeInfo (`typeProtoGarlicCapabilityRequest` /
`typeProtoGarlicCapabilityResponse`), returning a small versioned bitset/list
(e.g. `["garlic-v1"]`) plus the node's Garlic public key and GID-relevant
parameters if enabled. A node that gets no response (timeout) or an
unparseable/absent response is assumed **legacy** and is simply never
selected as a circuit hop or rendezvous point. This mirrors the task's
required truth table exactly (A+B garlic-v1 → garlic-v1 usable; either side
legacy-only → falls back to ordinary Yggdrasil, i.e. Garlic is simply not
attempted) and needs no change to the link handshake.

*Alternative considered and rejected*: piggybacking on the existing
`NodeInfo` map. Rejected because NodeInfo is user-controlled, privacy-optional
diagnostic metadata (`NodeInfoPrivacy` can blank it, users can put anything
in it) — overloading it for a functional protocol signal would make Garlic
capability detection unreliable and would couple two unrelated features.

### 3.5 Garlic envelope (conceptual — byte format deferred to `garlic-protocol.md`)

Every packet sent with tag `typeSessionGarlic` carries, at minimum:

- `version` (1 byte) — protocol version, distinct from Yggdrasil's own
  major/minor;
- `session_id` / `circuit_id` — scoped to the sender→hop relationship, used
  for replay-window bookkeeping and per-circuit state lookup;
- `packet_counter` — monotonic per session, doubles as AEAD nonce input
  material (never a raw reused nonce; see §3.6) and as the replay-window
  index;
- `expiration` — short TTL, rejects stale packets outright;
- one AEAD-encrypted body, which is either a bundle of onion-layer messages
  (§3.7) or a control message (circuit setup/teardown, capability data,
  rendezvous request);
- optional padding to a configurable fixed cell size (§3.9).

No field here is meaningful to, or parseable by, a node that hasn't opted
into Garlic — it is simply the encrypted payload of an ordinary
`core.PacketConn.WriteTo` call.

### 3.6 Layered (onion) encryption — primitives, not a new cipher

Per-hop: ephemeral X25519 ECDH between the sender (or previous hop's
ephemeral key, for forward layers) and that hop's long-term Garlic X25519
key, → HKDF with an explicit domain-separation label per key purpose
(`"ygg-garlic-v1-layer-key"`, `"ygg-garlic-v1-circuit-key"`, etc., distinct
from anything ironwood derives) → XChaCha20-Poly1305 AEAD (24-byte nonce,
safe to derive per-packet from the counter rather than requiring a global
random nonce registry) encrypting that hop's `{next_hop, inner_ciphertext}`.
A hop can only decrypt its own layer; it learns the next hop's address and
nothing about layers further in or previously peeled. All from
`golang.org/x/crypto` (`chacha20poly1305`, `hkdf`, `curve25519`) — no custom
primitive, per the hard constraint in the task.

### 3.7 Bundling

The AEAD body of a single garlic packet may contain multiple independently
encrypted sub-messages (each with its own destination/next-hop and payload),
concatenated with per-message length prefixes inside the single outer AEAD
envelope. An intermediate relay decrypting its own outer layer sees only
"N opaque encrypted sub-messages, route each independently" — it cannot tell
which ones share a real-world sender or correlate their plaintext. This
is the hook §13 padding/cover-traffic/batching would extend later without
a format change (additional "junk" sub-messages are indistinguishable from
real ones at the relay).

### 3.8 Garlic Service ID (GID)

```
GID = version_byte || BLAKE2b-256(domain_separator || garlic_pubkey || service_id)
```

canonically encoded (e.g. base32, matching the "unguessable capability
string" ergonomics of Tor/I2P-style names) — a self-certifying identifier
computed by anyone who knows the service's Garlic public key and service_id,
verifiable without a directory. Not derived from, and not convertible to,
the node's Yggdrasil IPv6 address — `address.AddrForKey`/`GetKey` are
untouched. Lookup is via the `Rendezvous` abstraction (§3.9), not via
`address.go`.

### 3.9 Ephemeral identities & rendezvous

- Long-term Garlic keypair (§3.8) authenticates a service's identity across
  sessions. Each circuit/session additionally generates a fresh ephemeral
  X25519 keypair used only for that circuit's ECDH; rotation interval is
  configurable. This decouples "prove you're the same long-term service" from
  "correlate all my traffic by a single reusable transport key."
- `Rendezvous` interface:
  ```go
  type Rendezvous interface {
      Publish(gid GID, introPoints []IntroPoint, ttl time.Duration) error
      Lookup(gid GID) ([]IntroPoint, error)
  }
  ```
  First implementation: `StaticRendezvous`, a config/in-memory GID →
  introduction-point-key-list map, sufficient to test circuit construction
  end-to-end without any DHT work. A distributed implementation is future
  work behind the same interface.

### 3.10 Circuit construction (conceptual)

Alice picks a path of Garlic-capable relay keys (random selection among
known-capable peers, configurable length), builds nested onion layers
(§3.6) addressed hop-by-hop, and extends the circuit incrementally
(standard telescoping construction: each hop only learns the next hop, not
the full path). Circuit state carries: circuit ID, per-hop keys, creation
time, packet/byte counters, and hard caps (`circuit_lifetime`,
`max_packets_per_circuit`, `max_bytes_per_circuit` — all config-driven, §3.11).
Expiry/rekey and failure handling (a dead hop mid-circuit) are Phase 5 work;
flagged here only so the envelope format (§3.5) already has the fields
(`circuit_id`, `expiration`) they'll need.

### 3.11 Configuration sketch

Additive block in `NodeConfig`, zero value = disabled = vanilla behavior:

```yaml
garlic:
  enabled: false
  mode: relay
  path_length: 3
  circuit_lifetime: 10m
  max_circuits: 1024
  padding:
    enabled: true
    cell_size: 1200
  replay:
    window: 5m
  rendezvous:
    type: static
```

### 3.12 API sketch

Following the admin-socket handler convention (§1.9), not a new transport:
`CreateGarlicIdentity`, `GetGarlicIdentity`, `CreateCircuit`, `CloseCircuit`,
`PublishService`, `LookupService`, `SendGarlic`, `GetGarlicStats`, each
registered via `AdminSocket.AddHandler` and reachable through
`yggdrasilctl`, matching how `GetNodeInfoRequest`/`DebugGetSelfRequest` work
today.

### 3.13 DoS-relevant bounds (surface only — enforcement is Phase 12)

Flagging where accounting must exist, sized against the config in §3.11: per-peer
and global circuit counts, handshake/sec, garlic packets+bytes/sec, replay-cache
size (bounded, LRU/window-based, never grows unbounded off remote input),
max bundle size, max path length, max parse depth for nested payloads. None
of this is implemented yet; it's listed so §3.5-3.10 don't get designed in a
way that makes bounding them impossible later (e.g. circuit IDs are
attacker-chosen input and must be validated against a cap before any
allocation).

---

## 4. Legacy-node compatibility argument

Two distinct claims, often conflated in the original prompt's diagrams —
worth stating separately and precisely:

1. **A legacy node can sit on the network path between two Garlic nodes,
   forwarding their traffic, without any changes and without knowing Garlic
   exists.** True today, for free: ironwood routes packets between any two
   keys through whatever intermediate nodes the topology requires, and
   those intermediate nodes only ever handle encrypted routing frames — this
   has nothing to do with the payload's tag byte, which only the two
   *endpoints* of a given end-to-end ironwood session ever inspect
   (`Core.ReadFrom`, §1.6). A legacy node was never going to decode
   `typeSessionGarlic` because it never decodes anyone else's payload at
   all, Garlic or not.
2. **A legacy node cannot itself act as a Garlic circuit hop** (it can't
   peel a layer and forward the next one) — it doesn't run `src/garlic`, so
   a `typeSessionGarlic` packet addressed *to it specifically* hits the
   existing `default: continue` in `Core.ReadFrom` and is silently dropped,
   with no error and no observable behavior change on that node. This is
   correct and expected: circuit hops must be Garlic-capable by definition.
   Legacy nodes fill role (1) — invisible mesh transport between circuit
   hops — never role (2).

Combined with §3.4 (capability negotiation controls hop selection, so
Garlic never tries to route a circuit through a node it knows is legacy) and
§3.3 (`garlic.enabled = false` behaves bit-identically to vanilla), all four
combinations from the task hold:

- **Old ↔ Old**: unaffected; no Garlic code path exists on either side.
- **Old ↔ New**: the New node behaves as an ordinary Yggdrasil peer to the
  Old node for anything that isn't Garlic; any stray Garlic-tagged traffic
  addressed to the Old node is silently dropped by its own unmodified
  `ReadFrom`.
- **New ↔ Old**: symmetric to the above.
- **New ↔ New**: full Garlic capability negotiated and available; falls
  back to ordinary behavior if either side reports `legacy`-only via §3.4.

No breaking change to wire protocol, routing, IPv6 connectivity, or peering
is required or proposed anywhere in this design.

---

## 5. Preliminary privacy-leak list

A full threat model is out of scope for this document (`garlic-threat-model.md`,
later phase). Flagging what's already visible from the architecture alone,
so it isn't lost before that document exists:

- **Capability + GID responses are themselves a fingerprint.** Answering a
  capability probe or publishing to a rendezvous reveals "this key runs
  Garlic," which is itself metadata a passive observer of DHT/routing
  traffic could try to correlate against.
- **First/last hop still knows an endpoint.** The entry hop learns Alice's
  real Yggdrasil key (she has to reach it somehow); the exit/rendezvous
  side ultimately learns which node key is answering for a GID unless the
  service itself is also relayed. Standard onion-routing limitation, not a
  Garlic-specific defect, but must be stated, not hidden.
- **Packet-size and timing correlation** across relays remain possible until
  §3.9's padding and (future) batching/jitter are actually implemented —
  Phase 1 only reserves the fields/API for it (§3.11 `padding.cell_size`).
- **Global passive adversary** watching enough of the mesh could attempt
  traffic-confirmation correlation between circuit hops; multi-hop relaying
  raises the cost but does not claim to defeat this class of adversary.
- **Sybil relays**: since relay selection depends on capability-negotiation
  responses from nodes anyone can run, an adversary running many
  Garlic-capable nodes can bias path selection toward itself. Mitigations
  (diversity constraints, reputation, etc.) are explicitly deferred; not
  solved by this design.
- **Rendezvous/introduction-point operators** learn which GID is being
  looked up and roughly when, even under `StaticRendezvous`.

None of the above should be read as "solved" or "mitigated" by this
document — they're the starting list `garlic-threat-model.md` must expand
and address per adversary class.

---

## 6. Explicitly out of scope for this document / this phase

Per the agreed Phase-1-only scope: no code, no `garlic-protocol.md` byte
layout, no threat model writeup, no rendezvous implementation, no crypto
implementation, no tests. Sections 3.5–3.13 above are proposals to be
validated (and likely adjusted) once Phase 2 (protocol types/serialization)
actually starts.

## 7. Architectural risks / open questions to revisit before Phase 2

- The exact shape of `core.SetGarlicHandler` (single handler vs. registry,
  actor/goroutine model matching `phony.Inbox` used elsewhere in `core`)
  needs to match `core`'s existing concurrency conventions — deserves a
  closer read of `phony` usage in `proto.go`/`nodeinfo.go` before Phase 2.
- Whether ephemeral X25519 keys should be derived from the ed25519 identity
  via a birational map (simpler key management) or generated fully
  independently (better isolation, our current recommendation, §1.1) is
  worth a second look once real key-lifecycle/config code is written.
- MTU: `core.MTU()` already subtracts 1 byte for the session-type tag
  ([core.go:166-173](../src/core/core.go#L166-L173)); the Garlic envelope
  overhead (§3.5) further shrinks usable payload per hop and needs to be
  budgeted explicitly once cell sizes are chosen (§3.11 `padding.cell_size`).

## 8. Roadmap

This document corresponds to Phase 1 of the 14-phase plan (research +
architecture). Suggested next step: brainstorm/spec Phase 2 (protocol types
and serialization) as its own follow-up design, scoped independently, once
this document is reviewed.
