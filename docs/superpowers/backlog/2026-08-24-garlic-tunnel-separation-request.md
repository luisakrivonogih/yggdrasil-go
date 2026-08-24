# Backlog: Redesign Garlic routing to eliminate cross-hop linkability and introduce I2P-inspired tunnel separation

Status: not yet designed. This is the raw task request as given by the user
on 2026-08-24, preserved verbatim so it isn't lost. It supersedes the
2026-08-09 crypto-hardening task, which addressed per-hop ephemeral keys but
explicitly left `CircuitID`/`PacketCounter`/`Expiration` as known, documented
linkability gaps (see `docs/garlic-threat-model.md`, "Single malicious relay"
row).

This has NOT gone through the brainstorming process yet (approaches, design
doc, plan). See conversation for the proposed decomposition into sub-projects
before that process starts.

---

## Task: Redesign Garlic routing to eliminate cross-hop linkability and introduce I2P-inspired tunnel separation

You are working on the Yggdrasil Garlic Routing Overlay branch.

This task supersedes the previous task that only addressed global `CircuitID`, `PacketCounter` and `Expiration`.

The goal is to make the Garlic routing architecture substantially harder to correlate by separating route construction into independent outbound/inbound tunnels, while preserving Yggdrasil compatibility and the existing Garlic cryptographic design.

IMPORTANT:
Do not claim or implement "anonymous routing" as an absolute property.
The project is privacy-enhanced routing with an explicit threat model.

---

# 1. Current problems

The current Garlic architecture conceptually looks like:

```
Alice
  |
  v
Guard
  |
  v
Middle
  |
  v
Exit
  |
  v
Bob
```

The originator constructs a single end-to-end circuit.

The current protocol has several correlation problems.

## 1.1 Global envelope metadata

The existing Envelope contains fields equivalent to:

```
CircuitID
PacketCounter
Expiration
Body
```

The first three fields are outside the per-hop AEAD and are currently copied between hops.

This means:

```
Alice -> Guard:
    CircuitID = X
    Counter = 194
    Expiration = T

Guard -> Middle:
    CircuitID = X
    Counter = 194
    Expiration = T

Middle -> Exit:
    CircuitID = X
    Counter = 194
    Expiration = T
```

Two non-adjacent colluding relays can trivially determine that they are observing the same circuit.

This MUST be fixed.

## 1.2 Ironwood metadata leakage

The underlying Yggdrasil `ironwood/network` layer exposes source and destination public keys in its `traffic` structure.

Therefore an intermediate Yggdrasil node can observe:

```
source node key
destination node key
traffic volume
timing
```

even if it cannot decrypt the Garlic payload.

This is existing Yggdrasil behavior and is NOT something Garlic should pretend to eliminate.

The design should instead minimize the usefulness of this metadata through:

```
route separation
topology diversity
hop-local identifiers
independent tunnel construction
padding
jitter
cover traffic
decoy traffic
```

---

# 2. Architectural direction

Take inspiration from I2P's architecture, but DO NOT copy I2P's protocol or cryptographic implementation.

The important ideas to borrow are:

1. Separate outbound and inbound tunnels.
2. A destination publishes temporary inbound tunnel endpoints.
3. The sender chooses an outbound tunnel independently.
4. The sender does not need to know the complete destination-side path.
5. Tunnel descriptors/leases are temporary and rotate.
6. Garlic-style bundled/decoy messages can be used as a traffic-shaping mechanism.
7. Peer selection should consider topology and observed reliability, not merely random selection.

Do NOT turn Garlic into a literal I2P implementation.

Keep the existing Yggdrasil overlay as the transport substrate.

---

# 3. Target architecture

Instead of:

```
Alice -> Guard -> Middle -> Exit -> Bob
```

design Garlic conceptually as:

```
                Alice
                  |
                  v
           Outbound Tunnel
                  |
             A -> B -> C
                  |
                  v
             Rendezvous
                  |
                  v
            Inbound Tunnel
                  |
             D -> E -> F
                  |
                  v
                 Bob
```

Alice should construct/control the outbound tunnel.

Bob should have one or more inbound tunnels.

Alice should learn only the information necessary to reach a suitable inbound tunnel gateway / rendezvous point.

Alice should NOT need to know:

```
Bob's complete inbound path
identities of all inbound relays
all future hop-local identifiers
a global circuit identifier shared by both tunnels
```

Bob should NOT learn:

```
Alice's outbound path
Alice's true source node identity through Garlic metadata
the identities of all outbound relays
```

The rendezvous mechanism must not become a new global correlation identifier.

---

# 4. First: inspect the existing implementation

Before editing code, thoroughly inspect:

```
src/garlic/
ironwood/network/
ironwood/encrypted/
docs/garlic-protocol.md
docs/garlic-architecture.md
docs/garlic-threat-model.md
docs/garlic-security.md
docs/garlic-compatibility.md
docs/garlic-rendezvous.md
docs/garlic-testing.md
```

Specifically trace:

```
circuit creation
circuit state
onion construction
hop processing
Envelope parsing
forwarding
replay protection
teardown
expiration
cover traffic
auto-pool
multipath
rendezvous
discovery/gossip
service descriptors
```

Search globally for:

```
CircuitID
PacketCounter
Expiration
Envelope
relayCircuitState
ReplayWindow
Rendezvous
Lease
Tunnel
AutoPool
SendGarlicBundled
msgTypeCircuitData
msgTypeCircuitDataV3
```

Do not begin implementation until you understand the complete data flow.

---

# 5. Hop-local envelope metadata

Replace the current semantic model:

```
CircuitID = global circuit identifier
```

with:

```
LocalCircuitID = hop-local forwarding-state identifier
```

Conceptually:

```
Alice -> Guard:
    LocalCircuitID = A1
    LocalCounter = 819
    LocalExpiration = T1

Guard -> Middle:
    LocalCircuitID = B7
    LocalCounter = 42
    LocalExpiration = T2

Middle -> Exit:
    LocalCircuitID = F3
    LocalCounter = 177
    LocalExpiration = T3
```

Requirements:

* Each LocalCircuitID must be independently random.
* Prefer at least 128 bits of cryptographically secure randomness.
* Different hops MUST NOT share the same identifier.
* A relay must not be able to derive a non-adjacent relay's identifier.
* Do not put the complete list of identifiers in plaintext.
* Do not replace `CircuitID` with another globally stable identifier such as `FlowID`, `SessionID`, etc.

Packet counters must also be hop-local.

Expiration must also be hop-local.

Do not simply rename the existing fields.

---

# 6. Forwarding state

A relay should conceptually maintain:

```
incoming LocalCircuitID
    |
    +-- next hop
    +-- outgoing LocalCircuitID
    +-- replay window
    +-- expiration
    +-- required cryptographic state
    +-- tunnel state
```

The relay knows only the state necessary for its adjacent forwarding segment.

Use existing bounded state mechanisms where possible.

Do NOT introduce unbounded attacker-controlled maps.

Preserve:

```
MaxCircuits
MaxCircuitsPerPeer
MaxTrackedPeers
relayCircuitState capacity
ReplayWindow bounds
MaxPathLength
```

---

# 7. Separate outbound and inbound tunnels

Introduce a conceptual distinction between:

```
OutboundTunnel
InboundTunnel
```

Do not necessarily expose these as public API types if the existing architecture does not require it.

An outbound tunnel is constructed by the sender.

Example:

```
Alice -> A -> B -> C
```

An inbound tunnel is constructed/maintained by Bob:

```
D -> E -> F -> Bob
```

The two should be independently constructed.

Do not create a single circuit containing all:

```
A -> B -> C -> D -> E -> F
```

as one globally identifiable object.

The rendezvous/meeting point should only connect the two tunnel segments.

---

# 8. Inbound tunnel leases

Implement an I2P-inspired temporary descriptor/lease concept.

A destination may publish something conceptually similar to:

```
Lease {
    gateway
    tunnel identifier
    expiration
    epoch/version
}
```

Do not copy I2P's exact format.

Requirements:

* leases are temporary;
* leases expire automatically;
* leases can rotate;
* multiple inbound tunnels may exist;
* descriptors must be bounded;
* old descriptors must eventually disappear;
* publication must not reveal the complete inbound path;
* the descriptor must not contain a global end-to-end circuit identifier.

The destination should be able to rotate inbound tunnels without requiring the sender to know the entire path.

---

# 9. Tunnel construction

Tunnel construction must preserve the existing Garlic onion encryption model.

For each hop:

```
hop i knows:
    previous hop
    itself
    next hop
    its local state
```

It must NOT learn:

```
all later hop identities
all earlier hop identities
a global circuit identifier
identifiers of non-adjacent tunnel segments
```

Preserve the existing per-hop ephemeral X25519 architecture.

Do not regress the existing test:

```
TestNonAdjacentHopsCannotLinkViaEphemeralKeys
```

---

# 10. Rendezvous

Design the rendezvous layer carefully.

The rendezvous point must not become:

```
"Alice talked to Bob"
```

with a stable globally visible identifier.

Avoid a stable rendezvous token reused across all sessions.

Use short-lived, rotating rendezvous identifiers if an identifier is required.

The rendezvous mechanism should conceptually perform:

```
outbound tunnel
      |
      v
  rendezvous
      |
      v
inbound tunnel
```

but it should not become a permanent global mapping:

```
Alice <-> Bob
```

The rendezvous implementation must be explicitly analyzed against:

```
malicious rendezvous point
colluding rendezvous + relay
replay
descriptor enumeration
flooding
correlation
expiration
stale leases
```

---

# 11. Route selection

Do NOT simply select:

```
random(peer)
```

for every hop.

Implement or prepare the architecture for topology-aware diversity.

Selection should consider, where information is actually available:

```
network topology
subnet diversity
ASN diversity if available
observed path diversity
peer reliability
uptime
latency
packet loss
historical failures
```

The goal is not perfect geographical/organizational independence.

The goal is to avoid selecting obviously correlated relays.

For example, avoid:

```
A
A's close topology neighbor
A's same upstream
```

when better alternatives exist.

Do not invent unreliable external geolocation databases.

Use only information already available or safely measurable by the node.

---

# 12. Sybil resistance

Document clearly what the design does and does not protect against.

A malicious operator can generate many Yggdrasil identities.

Therefore:

```
random selection alone is insufficient.
```

At minimum consider:

```
peer history
reliability scoring
topology diversity
maximum number of hops sharing the same apparent neighborhood
diversity constraints
avoidance of repeatedly selecting newly observed peers
```

Do NOT pretend this creates Sybil resistance.

It only raises the cost of influencing route selection.

---

# 13. Branching / decoy traffic

DO NOT make exponential branching the default.

First implement tunnel separation and hop-local metadata.

Then add an abstraction allowing controlled decoy branching.

Conceptually:

```
                ┌── decoy ──> drop
                |
Guard ----------+── real ───> Middle
                |
                └── decoy ──> drop
```

The decoy traffic should be structurally indistinguishable from real Garlic traffic at the relevant observation layer.

Do NOT simply copy the exact same encrypted packet multiple times.

Do NOT use the same CircuitID/counter/expiration.

Every branch must use hop-local metadata.

Bound:

```
branch factor
branch depth
bandwidth overhead
CPU overhead
memory overhead
```

No exponential uncontrolled tree growth.

Make the feature configurable and disabled by default until security/performance testing is complete.

---

# 14. Garlic cloves / bundled traffic

Extend the existing:

```
SendGarlicBundled
```

concept in an I2P-inspired direction where appropriate.

A Garlic message may contain multiple independently meaningful traffic units.

Possible categories:

```
real payload
cover payload
decoy routing unit
control message
```

However, the receiver/relay must not be able to trivially distinguish these based solely on outer metadata.

Do not copy I2P's exact Garlic Clove implementation.

Reuse the project's existing authenticated encryption.

---

# 15. Cover traffic

Preserve and audit:

```
Config.CoverTrafficEnabled
AutoPool
SendGarlicBundled
jitter
padding
```

Cover traffic must follow the same hop-local envelope model.

Do not allow cover traffic to accidentally expose a global circuit identifier.

Document that cover traffic:

```
increases correlation cost
```

but does not provide a formal anonymity set against a global adversary.

---

# 16. Multipath

Preserve multipath support.

However, avoid exposing one stable identifier across all paths.

If Alice uses:

```
outbound path A
outbound path B
```

the two paths should not share a globally visible outer identifier.

If an internal logical flow identifier is required, keep it inside authenticated/encrypted state where possible.

Analyze whether traffic volume itself can still link the paths.

Document remaining limitations.

---

# 17. Replay protection

Preserve replay protection.

Each hop must have independent replay state.

For example:

```
Guard:
    CID=A1
    replay window W1

Middle:
    CID=B7
    replay window W2
```

Do not share one global replay counter/window across the entire route.

Keep all replay state bounded.

---

# 18. Teardown

Redesign teardown so it is also hop-local.

Do NOT introduce:

```
GlobalCircuitID
```

just to make teardown easy.

A relay should be able to tear down its local forwarding state without knowing the entire end-to-end circuit identity.

Handle:

```
normal teardown
timeout
relay failure
destination disappearance
expired lease
stale tunnel
replayed teardown
```

---

# 19. Backward compatibility

Vanilla Yggdrasil must remain unchanged.

With:

```
Garlic.Enabled: false
```

the node behaves exactly like normal Yggdrasil.

Ordinary Yggdrasil nodes must still be capable of transporting Garlic packets without being Garlic-aware.

For Garlic-to-Garlic communication:

* introduce an explicit protocol version/capability if required;
* do not silently reinterpret old wire formats;
* fail closed on unsupported versions;
* preserve the existing capability negotiation architecture.

---

# 20. Security tests

Add tests for all of the following.

## Metadata unlinkability

```
TestEachHopHasDifferentLocalCircuitID

TestPacketCounterIsHopLocal

TestExpirationIsHopLocal

TestNonAdjacentRelaysCannotLinkUsingEnvelopeMetadata

TestNoStableGlobalCircuitIdentifier
```

The key test should capture the exact wire-level envelopes observed by multiple hops and verify:

```
CID(A->B) != CID(B->C)
CID(B->C) != CID(C->D)
```

and:

```
counter(A->B) != counter(B->C)
```

where appropriate.

Do not require counters to differ numerically if the implementation intentionally allows coincidence; instead verify they are independently scoped and not copied.

## Tunnel separation

```
TestOutboundTunnelDoesNotExposeInboundPath

TestInboundTunnelDoesNotExposeOutboundPath

TestLeaseContainsOnlyInboundGatewayInformation

TestLeaseRotation

TestExpiredLeaseRejected
```

## Rendezvous

```
TestRendezvousIdentifierNotGloballyStable

TestRendezvousReplayProtection

TestRendezvousExpiration

TestMaliciousRendezvousCannotDecryptPayload
```

## Branching

```
TestDecoyBranchDoesNotRevealRealBranch

TestBranchMetadataIsHopLocal

TestBranchingIsBounded

TestDecoyTrafficUsesValidWireFormat
```

## Existing security properties

Preserve:

```
TestBuildOnionHopCannotDecryptAnotherHopsLayer

TestNonAdjacentHopsCannotLinkViaEphemeralKeys

TestRelay1CannotDeriveRelay2SessionKey

TestReplayWindowMemoryStaysBounded

TestCircuitManagerEnforcesMaxCircuits

TestRelayCircuitStateBoundedCapacity
```

and all existing fuzz tests.

---

# 21. Fuzzing

Update fuzzing for:

```
Envelope
tunnel descriptors
leases
rendezvous messages
branch/decoy metadata
teardown
replay state
```

Maintain:

```
malformed input never panics
declared lengths are bounded
allocations remain bounded
attacker-controlled counts are capped
```

---

# 22. Threat model update

Update:

```
docs/garlic-threat-model.md
```

Explicitly add these adversaries:

```
malicious outbound relay
malicious inbound relay
malicious rendezvous point
colluding outbound relays
colluding inbound relays
rendezvous + relay collusion
malicious peer attempting Sybil influence
```

Explain what each can see.

Especially document:

```
outbound tunnel does not reveal the inbound tunnel
inbound tunnel does not reveal the outbound tunnel
hop-local IDs cannot be compared across non-adjacent hops
rendezvous does not receive a stable Alice/Bob identifier
```

But explicitly state:

```
a sufficiently powerful global passive adversary can still perform
traffic confirmation/correlation.
```

Do not claim anonymity.

---

# 23. Important security invariant

The following MUST hold:

```
No value outside hop-local encrypted state may serve as a stable
identifier shared by non-adjacent hops belonging to the same logical
communication.
```

This includes fields named:

```
CircuitID
FlowID
SessionID
StreamID
TunnelID
RequestID
RendezvousID
```

unless the identifier is explicitly scoped to a single hop/segment and
cannot be used to correlate non-adjacent observations.

Also:

```
A relay must not be able to derive another non-adjacent relay's
hop-local identifier.
```

---

# 24. Performance requirements

Measure:

```
circuit creation latency
tunnel creation latency
rendezvous setup
packet forwarding overhead
memory per tunnel
memory per circuit
throughput
CPU
bandwidth overhead from cover traffic
bandwidth overhead from decoy branching
```

Do not introduce excessive allocations into the forwarding hot path.

---

# 25. Implementation strategy

Do NOT rewrite the entire Garlic subsystem at once.

Prefer staged changes:

Phase 1:
hop-local Envelope metadata

Phase 2:
hop-local forwarding/replay state

Phase 3:
outbound/inbound tunnel abstraction

Phase 4:
inbound tunnel leases and rotation

Phase 5:
rendezvous integration

Phase 6:
topology-aware route selection

Phase 7:
controlled decoy branching

Phase 8:
security/performance hardening

After each phase:

```
run relevant tests
update documentation
verify wire compatibility
inspect for new correlation identifiers
```

If a complete implementation is too large for one change, implement the
first safe phase completely and leave a clean architectural interface for
the next phase rather than producing a half-working tunnel system.

---

# 26. Self-review before completion

Before declaring success, attack your own implementation.

Assume two non-adjacent malicious relays.

Ask:

```
Can they compare any identical field?
Can they compare packet counters?
Can they compare expiration?
Can they compare tunnel IDs?
Can they compare rendezvous IDs?
Can they compare timestamps?
Can they derive another hop's identifier?
Can they identify the same logical flow from metadata?
Can they infer that two tunnel segments belong to the same session?
```

Then assume:

```
malicious rendezvous + malicious relay
```

Ask the same questions.

Then assume:

```
global passive observer
```

Determine exactly what remains possible.

Do not hide remaining weaknesses.

---

# Final deliverables

At the end provide:

1. Files changed.
2. Protocol changes.
3. New wire format.
4. New outbound/inbound tunnel architecture.
5. Lease format and rotation strategy.
6. Rendezvous design.
7. Forwarding state model.
8. Replay model.
9. Route selection model.
10. Branching design/status.
11. Compatibility implications.
12. Tests added.
13. Fuzzing changes.
14. Benchmarks.
15. Remaining correlation attacks.
16. Remaining Sybil attacks.
17. Remaining global-adversary limitations.

Most importantly:

Do not simply implement an "I2P clone".

Use I2P as architectural inspiration for:

```
tunnel separation
inbound leases
independent route construction
garlic/decoy messages
peer profiling
```

while retaining the existing Yggdrasil transport, Garlic cryptography,
per-hop X25519, AEAD, padding, jitter, cover traffic and compatibility
architecture.

The final design should be something that can be described as:

```
"Yggdrasil transport + Garlic per-hop cryptography +
 independently constructed outbound/inbound privacy tunnels +
 hop-local routing metadata + topology-aware path selection +
 optional bounded decoy traffic"
```

not:

```
"Yggdrasil pretending to be I2P."
```

---

## Related, deferred: small-deployment circuit-build failure

Separately diagnosed the same day: `AutoCreateCircuit` fails with
`ErrInsufficientDiverseCandidates` on the user's real 4-node deployment
(confirmed live via `yggdrasilctl createGarlicCircuitAuto`). Root cause:
`Config.PathLength=3` + `Config.MinHopCount=2` + the no-shared-`TreeParent`
diversity constraint in `SelectDiversePath` cannot be satisfied by a small
and/or star-shaped mesh — e.g. a hub whose only known peers are its direct
1-hop neighbors (excluded by `MinHopCount`), or leaves whose only other
candidates share the hub as `TreeParent` (excluded by the diversity
constraint). `fillAutoPool` (manager.go) silently swallows this error with no
log, and `getGarlicKnownPeers` doesn't expose `HopCount`/`TreeParent`, so
there was no visibility into why circuits weren't building. User asked to
defer this and address it later — it is not part of this backlog item's
scope, just noted here so it isn't lost.
