# Garlic Routing Overlay — Rendezvous

## Purpose

A Garlic service (Bob) should not have to publish his real Yggdrasil
IPv6 address as the price of being reachable through Garlic — that would
defeat the point. The `Rendezvous` abstraction decouples "how do I find
this service" from "what is its underlying network address."

```go
// src/garlic/rendezvous.go
type Rendezvous interface {
    Publish(gid GID, points []IntroPoint, ttl time.Duration) error
    Lookup(gid GID) ([]IntroPoint, error)
}
```

`GID` (`docs/garlic-protocol.md` §6) is a self-certifying identifier —
`BLAKE2b-256(domain_separator || garlic_public_key || service_id)` —
computable by anyone who already knows the service's public key and
chosen `service_id`, without querying any directory. The directory
(`Rendezvous`) only needs to map that GID to a current list of
`IntroPoint`s: node keys of Garlic-capable relays willing to help
establish contact with the service.

## What's implemented: `StaticRendezvous`

`src/garlic/rendezvous.go`. An in-memory, TTL-expiring map, safe for
concurrent use, capacity-bounded per publication
(`MaxIntroPoints = 16`, so a single `Publish` call can't make the
implementation store unbounded state). This is intentionally the
simplest possible implementation — it exists so circuit-construction and
capability-negotiation logic could be built and tested (including the
full `Garlic.PublishService`/`LookupService` API and the admin-socket
`publishGarlicService`/`lookupGarlicService` handlers) completely
independent of any distributed system, per the phased plan in
`docs/garlic-architecture.md` §26.

`StaticRendezvous` is what every test and the current `garlic.New`
wiring in `cmd/yggdrasil/main.go` uses today. It has an obvious
limitation as a real deployment mechanism: it only knows about
publications made to *that specific node's own in-memory map* — it is
not shared across the network. Two nodes each running `StaticRendezvous`
have no way to discover each other's published services unless something
outside this project synchronizes their maps (e.g. a shared config file,
or an out-of-band channel).

## What's deliberately not implemented

A distributed rendezvous — a DHT, gossip protocol, or similar mechanism
so `Publish`/`Lookup` calls actually reach other nodes over the network —
is out of scope for this phase, per the original brief's explicit
instruction not to build a "full global DHT" up front. The `Rendezvous`
interface exists specifically so this can be added later as a second
implementation with no change to anything that consumes it
(`Garlic.PublishService`/`LookupService`, the admin handlers, or circuit
construction).

## Threat-relevant properties of the interface itself

Independent of which implementation backs it:

- **A rendezvous operator learns which GIDs are looked up, and roughly
  when.** `Lookup` necessarily reveals the GID being queried to whatever
  implements `Rendezvous`. This is true of any name-resolution system and
  is called out again in `docs/garlic-threat-model.md`.
- **Publishing is itself a fingerprint.** The act of calling `Publish`
  for a GID reveals that *some* Garlic identity controls that GID, to
  whatever sees the publication (for `StaticRendezvous`, nothing beyond
  the local node; for a future distributed implementation, potentially
  many nodes).
- **Introduction points are a trust boundary**, covered in
  `docs/garlic-threat-model.md` under "Malicious introduction point" —
  they learn that they've been designated as a way to reach a particular
  GID, and see connection-establishment traffic destined for it, but
  (given the circuit-hop design in `docs/garlic-protocol.md` §4) do not
  themselves decrypt application payload unless they are also the
  circuit's terminal hop.

## Future direction (not built)

A distributed `Rendezvous` implementation would most naturally reuse
Yggdrasil's existing DHT machinery in ironwood rather than building a
second one — GIDs and introduction-point lists are small, bounded
records well-suited to a key-value DHT. This is noted as the intended
next step, not designed in detail here; doing so properly requires its
own threat-model pass (a distributed directory changes the "malicious
introduction point" and "global passive adversary" analyses in
`docs/garlic-threat-model.md` materially) before implementation begins.
