# Garlic Autonomous Routing — Design

Status: **approved design, not yet implemented.**

This extends the Garlic Routing Overlay (`docs/garlic-architecture.md`,
`docs/garlic-protocol.md`, `docs/garlic-threat-model.md`) with the pieces
needed to point a node at a couple of bootstrap peers and have it build,
maintain, and rotate its own circuits — the same "point at a couple of
peers, the rest resolves itself" experience Yggdrasil already gives you at
the mesh-routing layer (`docs/garlic-architecture.md` §1.5), applied to the
Garlic overlay's circuit-hop selection.

This document assumes the reader has read the three docs above. It does
not repeat their content except where this design changes it.

## 1. Motivation

Today, everything that makes automatic, trustworthy path selection
*possible* already exists in `src/garlic`: `discoveryRegistry` (gossip of
known peers), `SelectDiversePath` (topologically diverse hop choice),
`Garlic.SelectPath` (wires the two together). None of it is reachable
without already knowing every hop's key by hand — `createGarlicCircuit`
(the only admin RPC that builds a circuit) requires an explicit
comma-separated hop list, and `SelectPath` has exactly one caller, in a
test (`docs/garlic-architecture.md` §"Route manipulation": *"SelectPath(n)
is available but not mandatory"*).

Separately, `docs/garlic-threat-model.md`'s "Sybil nodes" and "Intersection
attacks" sections flag two open, acknowledged gaps this design also closes:
no distinction between a personally-verified peer and one only ever heard
about secondhand, and no enforced circuit-rotation policy ("left to the
caller; nothing in this version enforces one").

## 2. Non-goals

- **No TUN integration.** This does not route real application IPv6
  traffic through Garlic. Circuits remain reachable via `sendGarlic`/
  `recvGarlic` and the dashboard, same as today — just built and rotated
  automatically instead of by hand.
- **No persistent/sticky guard hop.** Per explicit discussion: the first
  hop is drawn only from self-verified candidates (see §3), but is
  re-selected on every rotation like any other hop — no Tor-style
  weeks-long guard pinning.
- **No shipped public bootstrap list.** `BootstrapPeers` (§5) is
  operator-supplied config, analogous to Yggdrasil's own `Peers` — this
  project does not stand up or endorse a directory-authority-style
  well-known bootstrap set.
- **Does not defeat a global passive adversary.** Cover traffic (§7)
  raises the cost of volume/timing correlation; `docs/garlic-threat-model.md`'s
  existing "Global passive adversary" and "Traffic correlation" verdicts
  still apply, just with default-on cover traffic added to the mitigation
  list.
- **No proof-of-work / resource-cost Sybil defense.** Self-verified/gossiped
  trust tiers (§3) narrow the simplest Sybil strategies further; they do
  not add IP/ASN diversity, reputation scoring, or an admission cost, which
  `docs/garlic-threat-model.md` already lists as unsolved.

## 3. Trust tiers in discovery

`DiscoveredPeer` (`src/garlic/discovery.go`) gains a field:

```go
type DiscoveredPeer struct {
    NodeKey         []byte
    GarlicPublicKey []byte
    LastSeen        time.Time
    SelfVerified    bool // true iff this node itself completed a capability handshake with this peer
}
```

`discoveryRegistry.record` merge rule: refreshing an existing entry never
downgrades `SelfVerified` from true to false — a later gossip mention of an
already self-verified peer still counts as self-verified. A fresh entry
takes whatever `SelfVerified` value its first `record` call carries.

Call sites:
- `handleCapabilityResponse` (`manager.go`) — this node's own successful
  `QueryCapability` round trip — records `SelfVerified: true`.
- `processAnnounce` (`protocol.go`) — a gossiped mention from a third party
  — records `SelfVerified: false`.

`HopCandidate` (`src/garlic/selection.go`) mirrors the field through
`candidatePool()` (`manager.go`), unchanged otherwise.

### First-hop policy

New `SelectPathWithGuardPolicy(pool []HopCandidate, n, minHopCount int)
([]HopCandidate, error)` in `selection.go`:

1. Select hop 0 via `SelectDiversePath` restricted to `SelfVerified` candidates only.
2. Select hops 1..n-1 via `SelectDiversePath` over the **full** pool
   (self-verified + gossiped), seeded with hop 0's tree parent already
   marked used — so hop 1 can't share hop 0's tree parent either, same
   diversity guarantee as today, just spanning the two-stage selection.
3. If step 1 finds no self-verified candidate at all, return
   `ErrNoSelfVerifiedCandidates` — a node must have personally verified at
   least one peer (its bootstrap peers, at minimum — see §5) before it can
   auto-build anything. This is the one unavoidable manual bootstrap step,
   same as Yggdrasil itself needing at least one configured `Peers` entry
   or multicast neighbor to join the mesh at all.

This is additive: `SelectDiversePath` itself is unchanged, existing callers
unaffected.

## 4. Gossip pull

**Problem:** `gossipTick` (`manager.go`) only pushes this node's known-peer
sample to peers already in its `capabilityCache` — i.e., peers *this node*
has queried. A freshly bootstrapped node is not in *anyone else's*
`capabilityCache` yet, so nobody proactively gossips to it; it would have
to wait for some other node to coincidentally query it first. That defeats
"point at two peers and get a candidate map."

**Fix:** new message type, `msgTypeAnnounceRequest` (`protocol.go`, next
`iota` after `msgTypeCircuitDataBundle`), empty body. `handleIncoming`
(`manager.go`) gets a new case: on receipt, immediately call
`g.GossipAnnounce(from)` (existing function, unchanged) — i.e., "pull" is
implemented as "ask them to push to you right now."

New `Garlic.RequestGossip(peer ed25519.PublicKey) error` sends the
request. Called automatically once per bootstrap peer after its initial
`QueryCapability` succeeds (§5), and exposed as a new admin RPC
`garlicGossipPull key=<hex>` for manual triggering (mirrors the existing
`garlicGossip` push RPC).

**Compatibility:** `handleIncoming`'s `switch data[0]` has no `default`
case — an unrecognized type byte is already silently ignored (Go
zero-value switch fallthrough). A peer running code without this feature
simply never responds to the pull; the requester falls back to whatever
the periodic push-gossip eventually delivers. No capability-version bump
needed for this half of the feature.

**DoS note for the threat-model update (§9):** answering a pull request
costs this node one outbound `GossipAnnounce` (bounded to
`Config.GossipSampleSize` entries, itself ≤ `maxAnnouncePeers` = 32). This
is a small, fixed amplification factor per request, already gated by the
existing per-peer `RateLimiter` on the *inbound* pull message — no new
unbounded-response surface. Worth one line in the threat model's "Malicious
client" section, not a new category.

## 5. Bootstrap config

New field on the runtime `garlic.Config` (`manager.go`) and the
corresponding `NodeConfig.Garlic` block (`src/config/config.go` — mirror
whatever fields that struct already exposes for `path_length` etc.,
following the same hjson-additive-block convention `docs/garlic-architecture.md`
§1.9 describes):

```go
BootstrapPeers []string // hex-encoded node keys, analogous to NodeConfig.Peers
```

On `Garlic.New` (or a short delay after, to let the link layer settle),
for each configured bootstrap key: `QueryCapability` → on success,
`RequestGossip` (§4). Both are best-effort; a bootstrap peer that's
temporarily unreachable is retried on the existing periodic cleanup/gossip
ticker, not specially scheduled.

This is the only manual step an operator performs — matching Yggdrasil's
own `Peers`, and satisfying §3's "at least one self-verified candidate"
requirement.

## 6. Automatic circuit construction

New `Garlic.AutoCreateCircuit(n int) (CircuitID, error)`:

1. `pool := g.candidatePool()`.
2. `hops, err := SelectPathWithGuardPolicy(pool, n, g.cfg.MinHopCount)`.
3. Fresh `QueryCapability` re-verification per selected hop (identical to
   what `createGarlicCircuit` already does today for manually-supplied
   hops) — a stale or now-unresponsive gossiped candidate fails here
   rather than silently building a broken circuit.
4. Every selected hop must additionally support `CapabilityAutoCircuit`
   (§7) — required for **all** positions, not just the terminal one; see
   §7 for why.
5. `g.CreateCircuit(path, nodeKeys)` (existing, unchanged).

New admin RPC `createGarlicCircuitAuto [hopCount]` (defaults to
`Config.PathLength`) → `{"circuitId": ...}`, same response shape as the
existing `createGarlicCircuit`. The manual, explicit-hop-list RPC is
unchanged and remains available.

## 7. Auto circuit pool + rotation

New background loop (`autoPoolLoop`, started from `Garlic.New` alongside
the existing `cleanupLoop`, only if `Config.AutoPoolEnabled`):

- Maintains `Config.AutoPoolSize` circuits built via `AutoCreateCircuit`,
  tracked in a new `g.autoPool map[CircuitID]time.Time` (creation time),
  separate from `CircuitManager`'s general bookkeeping (which still tracks
  them too, for the existing caps/stats — this map is purely "which of my
  circuits are pool-managed").
- Every `Config.AutoRotationInterval` tick, retires **one** pool circuit
  (oldest first) via `CloseCircuit` and immediately rebuilds it — never all
  of them at once, so pool-wide circuit-build bursts aren't themselves a
  distinguishing traffic pattern.
- A circuit that hits `Config.CircuitLifetime` and gets reaped by the
  existing `ExpireStale` is detected on the next loop tick (pool size below
  target) and backfilled the same way.

New admin RPC `getGarlicAutoPool` — lists current pool circuit IDs, hop
count, and age. New admin RPC `recvGarlicAuto [timeoutSeconds]` — mirrors
`recvGarlic`, but reads from the new tagged-delivery channel (§8) instead
of the existing `g.delivered`, so manual `sendGarlic`/`recvGarlic` users
are completely unaffected by anything in this document.

## 8. Wire format for auto-pool circuits, and cover traffic

This is the one place this design touches the wire protocol beyond §4's
additive message type, and it's worth being precise about *why*, because
the naive approaches don't work:

- **Reusing the existing `Bundle` cover-entry mechanism** (garbage bytes
  that fail AEAD auth and drop at whichever hop first tries to decrypt
  them) was considered and rejected for *continuous* cover traffic: garbage
  entries fail at hop 1, so links deeper in the circuit (hop 2→3, hop
  3→terminal) see no cover volume at all — a circuit's per-link traffic
  would systematically thin out with hop depth, itself a correlation
  signal. Continuous cover traffic needs to be **real, validly-encrypted,
  full-depth onion traffic** that actually reaches the terminal hop and
  gets silently discarded *there*, not garbage that dies at hop 1.
- **Tagging the payload inside the existing `LayerPlaintext`/`Inner`
  format** (a leading kind byte marking real-vs-cover) was considered and
  rejected: every hop parses its own `LayerPlaintext`, so an old relay that
  doesn't know about the tag would still parse the struct fine (it never
  looks at `Inner`'s content) — but an old *terminal* hop would deliver the
  tag byte as if it were real payload, corrupting `recvGarlic`'s output by
  one leading byte for anyone still using the plain manual API on a mixed
  old/new circuit.

**Adopted approach:** a new outer message type, `msgTypeCircuitDataV3`
(`protocol.go`), used for every hop-to-hop packet of an auto-pool circuit
— both the origin→hop1 send and every relay-to-relay forward — instead of
`msgTypeCircuitData`. The existing `msgTypeCircuitData` path, and
everything built on it (`SendGarlic`, `RecvGarlic`, `CreateCircuit`,
manual `createGarlicCircuit`), is **untouched**.

Mechanics:
- `processCircuitData` gains a `tagged bool` parameter (or a thin sibling
  wrapper sharing its core logic — implementation's choice) so it knows
  whether it's processing a V3 packet.
- **Forwarding must echo the same outer type byte it received**, not
  hardcode `msgTypeCircuitData` the way it does today
  (`forwardMsg = append(forwardMsg, msgTypeCircuitData)` in the current
  code becomes conditional on which type the packet arrived as). This is
  the single most safety-critical line in this whole feature: if a relay
  silently downgrades a V3 packet to plain `msgTypeCircuitData` on
  forward, the terminal hop's tag-aware delivery path never triggers and
  the payload (real or cover) is delivered through the wrong channel or
  misparsed. **Requires a dedicated test**
  (`TestForwardPreservesV3MessageType` or equivalent) asserting the
  forwarded packet's leading byte matches the inbound one across both
  `msgTypeCircuitData` and `msgTypeCircuitDataV3`.
- On terminal delivery of a tagged packet, `Inner[0]` is the kind byte
  (`0` = real, `1` = cover) and `Inner[1:]` is the actual payload. A
  `kind=cover` delivery is silently discarded (bump a stats counter only —
  no channel push). A `kind=real` delivery pushes to a new
  `g.autoDelivered chan AutoDeliveredMessage`, read by the new
  `recvGarlicAuto` RPC (§7) — never `g.delivered`.

**Why gating every position (not just terminal) on `CapabilityAutoCircuit`
is required:** the compatibility argument for `msgTypeAnnounceRequest`
(§4) relied on unrecognized message *types* being safely ignored. That
still holds for `msgTypeCircuitDataV3` at the point a legacy node first
receives one addressed to itself — but a legacy node acting as an
*intermediate* relay for a V3 circuit would still need to correctly
forward it (it doesn't decrypt intermediate layers, so type-preservation
forwarding is actually type-agnostic plumbing a legacy relay *could*
technically get right by accident) — the real risk is a legacy *terminal*
hop, which would successfully decrypt its layer, see `Inner` starting with
an unexpected kind byte, and either misdeliver or reject it depending on
what its old code expects there. Requiring `CapabilityAutoCircuit` support
at every position sidesteps needing to reason about which specific
position is the risky one; it also means only nodes that opted into
running this feature's code ever see V3 traffic at all, keeping the
existing v2-only network entirely unaffected by construction — same
"disabled ≈ vanilla" property the original Garlic rollout relied on
(`docs/garlic-architecture.md` §3.3).

`CapabilityMessage.Versions` gains `CapabilityAutoCircuit = "garlic-v2-auto"`
(new constant, `capability.go`), and `SupportsAutoCircuit() bool` (mirrors
existing `SupportsGarlicV2()`). `processCapabilityRequest` advertises it
whenever the node's code supports it at all — **not** gated on
`Config.AutoPoolEnabled`/`CoverTrafficEnabled` (those are this operator's
choice to *originate* auto-pool traffic; the ability to *relay/terminate*
someone else's is a code-version fact, and every Garlic-capable node
already relays regardless of what it personally originates — no
"client-only mode," per `docs/garlic-threat-model.md`'s Intersection
Attacks section).

### Cover traffic scheduling

Per circuit currently in the auto-pool, a jittered scheduler sends a
`kind=cover` message on average every `Config.CoverTrafficInterval`
(randomized ±50%, so it's not perfectly periodic — a fixed interval would
itself be fingerprintable, exactly as `docs/garlic-threat-model.md`'s
"Traffic correlation" section already cautions about non-default padding
ranges). Payload is random bytes, sized within the existing
`Config.MinPaddedSize`/`MaxPaddedSize` range so it's shape-indistinguishable
from real traffic at every hop.

## 9. Config surface

`garlic.Config` (`manager.go`) additions:

```go
BootstrapPeers        []string      // hex node keys, queried + gossip-pulled at startup
AutoPoolEnabled        bool          // originate auto-built circuits at all
AutoPoolSize            int          // circuits the pool maintains (suggested default: 3)
AutoRotationInterval    time.Duration // suggested default: 15m
CoverTrafficEnabled     bool          // suggested default: true (per explicit decision)
CoverTrafficInterval    time.Duration // suggested default: ~75s (low-bandwidth default, per explicit decision)
```

Exact defaults are `DefaultConfig()`'s call to make at implementation time,
conservative per the "low budget by default" decision — this document
fixes the *fields and behavior*, not the tuned constants.

## 10. Admin API surface (new RPCs, `src/garlic/admin.go`)

| RPC | Args | Notes |
|---|---|---|
| `createGarlicCircuitAuto` | `[hopCount]` | §6 |
| `getGarlicAutoPool` | — | §7, lists pool circuit IDs/ages/hop counts |
| `recvGarlicAuto` | `[timeoutSeconds]` | §7, reads `g.autoDelivered` |
| `garlicGossipPull` | `key` | §4, manual trigger |
| `getGarlicKnownPeers` (existing) | — | response gains `selfVerified` per entry (§3) |

## 11. Dashboard surface

`yggdashboard`'s existing `/garlic` and known-peers views gain: a
self-verified/gossiped badge per known peer, and an auto-pool status panel
(pool size, next rotation, per-circuit age) sourced from `getGarlicAutoPool`.
No new pages — extends existing polling/snapshot plumbing
(`yggdashboard/src/lib/server/poll.ts` already polls `getGarlicCircuits`/
`getGarlicKnownPeers`; add `getGarlicAutoPool` to the same `Promise.allSettled`
batch).

## 12. install.sh surface

New optional env var `GARLIC_BOOTSTRAP_PEERS` (comma-separated hex node
keys), written into the generated config's `garlic.bootstrapPeers` alongside
the existing `Garlic.Enabled`/`Dashboard.Enabled` JSON patch step. Empty by
default (a single freshly-installed node has nobody to bootstrap from yet;
an operator installing on server B after server A already exists passes
A's key). Undocumented for now beyond the script's own comment — this is
an operator convenience, not a new public interface.

## 13. Docs to update at implementation time

- `docs/garlic-protocol.md`: new §for `msgTypeAnnounceRequest` and
  `msgTypeCircuitDataV3` wire format, `CapabilityAutoCircuit`.
- `docs/garlic-threat-model.md`:
  - "Sybil nodes" — add self-verified/first-hop-guard-policy as a third
    partial mitigation; keep the "what remains genuinely unmitigated" list
    otherwise intact (still no IP/ASN diversity, no resource cost).
  - "Traffic correlation" — cover traffic moves from "opt-in per call, only
    via `SendGarlicBundled`" to "default-on for auto-pool circuits, still
    opt-in/absent for manually-built ones."
  - "Malicious client" — one line for the gossip-pull amplification bound
    (§4).
  - "Route manipulation" — update "SelectPath(n) is available but not
    mandatory" now that `AutoCreateCircuit`/the admin RPC exist; it is
    still not *mandatory* (manual `createGarlicCircuit` remains available
    and unchanged), just materially easier to reach for.

## 14. Testing strategy (high level — full cases at plan time)

- `discovery.go`: merge-never-downgrades-SelfVerified, gossip-recorded
  entries start `SelfVerified: false`, capability-response-recorded
  entries start `true`.
- `selection.go`: `SelectPathWithGuardPolicy` — hop 0 always self-verified,
  `ErrNoSelfVerifiedCandidates` when none exist, tree-parent diversity
  holds across the two-stage selection (hop 0 and hop 1 never share a
  parent).
- `protocol.go`: `msgTypeAnnounceRequest` round-trip (request → immediate
  `GossipAnnounce` reply); **forwarding preserves `msgTypeCircuitDataV3`**
  (the safety-critical case flagged in §8) across a multi-hop relay chain;
  `kind=cover` never reaches `g.autoDelivered`; `kind=real` does; a
  `msgTypeCircuitData` (non-V3) packet is entirely unaffected by any of
  this (regression coverage for the existing path).
- `manager.go`: `AutoCreateCircuit` rejects hops lacking
  `CapabilityAutoCircuit`; auto-pool loop maintains target size across a
  simulated expiry; rotation retires one circuit per tick, not all.
- Fuzz: extend the existing `Fuzz*` targets' pattern
  (`docs/garlic-threat-model.md`'s "Malformed packets" section) to the new
  `msgTypeAnnounceRequest`'s (trivial, fixed-shape) body and the V3 packet
  path's length validation, consistent with every other parser in this
  package.

## 15. Rollout / compatibility summary

- `Config.Garlic.Enabled = false` (existing top-level switch): unaffected,
  no behavior change, as today.
- `Config.AutoPoolEnabled = false` (new, independent switch): a node with
  Garlic on but auto-pool off never originates V3 traffic or advertises
  intent to use it beyond the bare `CapabilityAutoCircuit` flag (which just
  says "I can relay/terminate this format if asked") — it can still be
  selected as a hop *by another node's* auto-pool circuit. This mirrors
  how a node can relay for others' manual circuits without ever building
  its own.
- Existing `SendGarlic`/`RecvGarlic`/`CreateCircuit`/`createGarlicCircuit`
  RPC: zero wire or behavior change. Fully isolated from everything in
  this document via the separate `msgTypeCircuitDataV3` type and the
  separate `g.autoDelivered` channel.
