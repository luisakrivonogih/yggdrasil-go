# Garlic Routing Overlay — Compatibility

This document exists to answer one question precisely, for each of the
four combinations the project requires: **does this combination keep
working, and why?**

Backing evidence: `src/core/garlic_test.go` (unit-level: a node that
never calls `SetGarlicHandler` silently drops `typeSessionGarlic`
packets and keeps serving ordinary traffic) and
`src/garlic/integration_test.go` (`TestIntegrationSendGarlicThroughLegacyRelay`,
a real 5-node in-process mesh — Garlic — Legacy — Legacy — Garlic relay
— Garlic destination — proving actual end-to-end delivery through two
nodes that never call `garlic.New`).

## Old ↔ Old

Unaffected. Neither side has any Garlic code path; nothing in this
project changes.

## New ↔ Old (New initiates)

The new node behaves as an ordinary Yggdrasil peer for everything that
isn't Garlic traffic — peering, routing, IPv6 connectivity are all
untouched (`docs/garlic-architecture.md` §3 explains why no core routing
or handshake code needed to change).

If the new node ever addresses a `typeSessionGarlic`-tagged packet
(capability request, or a circuit hop) *directly to* the old node, the
old node's own unmodified `Core.ReadFrom` hits the pre-existing
`default: continue` branch (`src/core/core.go`) — the packet is silently
dropped, no error, no observable state change. From the old node's
perspective this looks exactly like garbage arriving on an unrecognized
in-band tag, which is precisely what it is to a node that predates this
feature.

**Consequence for the new node:** `Garlic.QueryCapability` against an old
node always times out (`ErrCapabilityTimeout`). The new node's own logic
treats a timeout as "legacy" and must never select that peer as a
circuit hop or rendezvous point — enforced by construction, since
`CreateCircuit` requires an already-obtained `CapabilityMessage` per hop,
which only exists for peers that actually answered.

## Old ↔ New (Old initiates)

Symmetric to the above: an old node has no Garlic code at all, so it
never sends `typeSessionGarlic` traffic, and nothing it does is affected
by the new node's presence.

## New ↔ New

Full negotiation: each side's `QueryCapability` succeeds, returning the
peer's supported versions and Garlic public key. If both advertise
`garlic-v2` (`CapabilityGarlicV2`, `src/garlic/capability.go` — bumped
from the original `garlic-v1` by the crypto-hardening pass, since that
pass's wire-format changes are not compatible with a `garlic-v1` peer),
circuits, capability caching, and delivery all work as described in
`docs/garlic-protocol.md`. If either side has `garlic.enabled = false`
in config, it behaves exactly like an "Old" node from the other's
perspective — the *feature flag*, not the software version, determines
behavior here.

A fifth combination this section's title doesn't name but is worth
stating explicitly: **a `garlic-v1`-only build talking to a `garlic-v2`
build.** Both sides have Garlic *enabled*, so this isn't "Old ↔ New" in
the sense above, but `SupportsGarlicV2` (`src/garlic/capability.go`)
returns false for a peer that doesn't advertise the new string, so the
`garlic-v2` side treats the `garlic-v1` peer exactly like a capability
timeout — never selected as a circuit hop or rendezvous point. This is
deliberate: Garlic has no deployed compatibility guarantee to preserve,
so a version mismatch fails capability negotiation cleanly rather than
two incompatible wire-format parsers attempting to interpret each
other's bytes.

## The nuance the original request's diagrams don't quite capture

> Alice(Garlic) → New Ygg → Old Ygg → Old Ygg → New Ygg → Bob

This is correct, but conflates two different roles a node can play,
worth separating explicitly:

1. **Mesh-level relay.** A legacy node forwarding the encrypted routing
   frames that make up the path *between* two Garlic-capable nodes that
   aren't directly peered. This requires **zero Garlic awareness** and
   was never going to need any — it's exactly what Yggdrasil's existing
   routing already does for any two nodes' traffic, Garlic or not. The
   "Old Ygg → Old Ygg" segment in the diagram above is this role.
2. **Circuit hop.** A node that receives a `msgTypeCircuitData` message
   addressed to *it*, peels its onion layer, and forwards the next one.
   This requires running `src/garlic` and holding a Garlic identity — a
   legacy node cannot do this, by construction (§4.3 of
   `docs/garlic-protocol.md`; its `Core.ReadFrom` silently drops the
   packet before any Garlic logic ever runs).

So: legacy nodes may appear any number of times *between* circuit hops
(role 1), but never *as* a circuit hop (role 2). The five-node
integration test's topology — `A(garlic) — L1(legacy) — L2(legacy) —
R(garlic relay) — B(garlic destination)` — has the circuit
`[R, B]` (two Garlic-capable hops), with L1 and L2 filling role 1 for
the mesh-level path between A and R. That is the combination this
document claims works, and the integration test runs it against real
`core.Core` instances, not a mock.

## No breaking change anywhere

Nothing in this project modifies: the wire link handshake
(`src/core/version.go`), ironwood's routing/DHT, `address.AddrForKey`/
`GetKey` (IPv6 addressing), or the encryption ironwood already provides
between any two node keys. The only change to `src/core` at all is one
new in-band tag byte and its accompanying handler-registration hook
(`src/core/garlic.go`) — additive, and inert unless something calls
`SetGarlicHandler`.
