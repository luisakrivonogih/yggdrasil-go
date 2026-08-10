# Garlic Routing Overlay — Rendezvous

## Purpose

A Garlic service (Bob) should not have to publish his real Yggdrasil
IPv6 address as the price of being reachable through Garlic — that would
defeat the point. The `Rendezvous` abstraction decouples "how do I find
this service" from "what is its underlying network address."

```go
// src/garlic/rendezvous.go
type Rendezvous interface {
    Publish(gid GID, descriptor *ServiceDescriptor) error
    Lookup(gid GID) (*ServiceDescriptor, error)
}
```

**Note on this signature (updated as of the crypto-hardening pass):**
this interface used to carry plain `Publish(gid, []IntroPoint, ttl)` /
`Lookup(gid) ([]IntroPoint, error)` — no signature anywhere, so a
malicious or compromised rendezvous could return attacker-controlled
introduction points for any GID. It now carries a signed
`*ServiceDescriptor` instead; see "Descriptor authentication" below for
what that closes and what it doesn't.

`GID` (`docs/garlic-protocol.md` §6) is a self-certifying identifier —
`BLAKE2b-256(domain_separator || garlic_signing_public_key ||
service_id)` — computable by anyone who already knows the service's
Ed25519 *signing* public key and chosen `service_id`, without querying
any directory. (GID binds to this signing key, not the X25519
circuit-ECDH key the rest of Garlic uses — see "Descriptor
authentication" below for why that's what makes it self-certifying.) The
directory (`Rendezvous`) maps that GID to the service's current signed
`ServiceDescriptor`, which itself carries the `IntroPoint`s (node keys of
Garlic-capable relays willing to help establish contact with the
service) among other fields.

## Descriptor authentication

`ServiceDescriptor` (`src/garlic/descriptor.go`):

```go
type ServiceDescriptor struct {
    Version          uint8
    ServicePublicKey ed25519.PublicKey // GID = ComputeGID(ServicePublicKey, ServiceID)
    ServiceID        []byte
    IntroPoints      []IntroPoint
    PublishedAt      uint64
    ExpiresAt        uint64
    Signature        []byte // ed25519, over everything above
}
```

`GID = ComputeGID(descriptor.ServicePublicKey, descriptor.ServiceID)` —
the same hash construction as before, now bound to a service's Ed25519
signing key (`Identity.SigningPublicKey`, `src/garlic/identity.go` —
generated independently of the X25519 circuit-ECDH keypair, never
derived from it) instead of the X25519 key. This is what makes the GID
self-certifying: nobody can produce a descriptor that both signs
correctly *and* hashes to a given GID without holding that GID's signing
private key.

**The rendezvous does not verify anything — it's the thing being
defended against.** `StaticRendezvous.Publish`/`Lookup` store and return
the descriptor verbatim, exactly as the old `IntroPoint`-list version
did; the trust boundary moved to the *client*, not the storage layer.
`Garlic.PublishService` (`src/garlic/manager.go`) builds the descriptor
and signs it with `identity.SigningPrivateKey` before calling
`Rendezvous.Publish`. `Garlic.LookupService` runs every descriptor a
`Rendezvous` returns through `VerifyServiceDescriptor`
(`src/garlic/descriptor.go`) before trusting its `IntroPoints`:

1. recomputes the GID from the returned descriptor's own
   `ServicePublicKey`/`ServiceID` and rejects on mismatch
   (`ErrDescriptorGIDMismatch`);
2. verifies the Ed25519 signature over the descriptor's own wire
   encoding, with `Signature` itself omitted from what's signed
   (`ErrInvalidDescriptorSignature`) — no rendezvous-added metadata
   (receipt timestamps, sequence numbers, storage hints) is ever part of
   the signed form, so a rendezvous can't influence what's verified by
   adding fields of its own;
3. checks `ExpiresAt` against the local clock (`ErrDescriptorExpired`).

Only after all three checks pass does `LookupService` return
`descriptor.IntroPoints` to the caller. Descriptor lifetime is itself
bounded — `ExpiresAt - PublishedAt` is capped by `MaxDescriptorLifetime`
(`src/garlic/descriptor.go`) — so a service can't mint a descriptor
"valid" for an unreasonable span either.

**What this does and does not defend against:** a malicious or
compromised rendezvous can still withhold a descriptor entirely, reorder
which one it serves if multiple were ever published, or serve a
stale-but-still-validly-signed one (nothing here defeats availability
attacks, only forgery). It cannot fabricate a descriptor for a GID it
doesn't hold the signing key for, and it cannot tamper with a genuine
descriptor's `IntroPoints` without invalidating the signature.

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
- **Forgery is not among the rendezvous's remaining capabilities.**
  Interface-level authentication (see "Descriptor authentication" above)
  means this property now holds for *any* `Rendezvous` implementation,
  not just `StaticRendezvous` — a future distributed backend inherits it
  automatically, since verification happens in `Garlic.LookupService`,
  outside any particular `Rendezvous` implementation. What a rendezvous
  (of any kind) can still do is withhold, reorder, or serve a stale
  descriptor — availability and freshness are not solved by signing.

## Future direction (not built)

A distributed `Rendezvous` implementation would most naturally reuse
Yggdrasil's existing DHT machinery in ironwood rather than building a
second one — GIDs and signed service descriptors are small, bounded
records well-suited to a key-value DHT. This is noted as the intended
next step, not designed in detail here; doing so properly requires its
own threat-model pass (a distributed directory changes the "malicious
introduction point" and "global passive adversary" analyses in
`docs/garlic-threat-model.md` materially) before implementation begins.
Descriptor authentication is orthogonal to this and would not need to be
redesigned — a distributed backend is still just untrusted storage/relay
from the verifying client's point of view.
