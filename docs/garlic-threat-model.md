# Garlic Routing Overlay — Threat Model

This system is **privacy-enhanced routing**. It is not described as
"anonymous" anywhere in this project, and this document is why: several
of the adversary classes below retain real capability. Treat every
"mitigated" claim below as bounded by its stated adversary, not absolute.

Scope: this analyzes what's actually implemented (`docs/garlic-protocol.md`),
not the aspirational full design in `docs/garlic-architecture.md`.

## Passive observer (watches network traffic, not a participant)

**Can see:** that two Yggdrasil nodes are exchanging encrypted traffic.
Packet sizes and timing on links it can observe. Whether a given link
carries `typeSessionGarlic`-tagged traffic is **not** visible from
payload content alone — the tag byte is inside the `encrypted` package's
per-session ciphertext (`golang.org/x/crypto`-based box seal in
`ironwood/encrypted/session.go`), exactly like the tag byte for ordinary
IPv6 traffic or NodeInfo queries. This part of the original doc's claim
holds.

**Correction from an earlier version of this document — read carefully,
this is not a property Garlic adds or can fix:** ironwood's `network`
package (the layer *below* `encrypted`, doing tree/DHT-based routing) is
**not** payload encryption and does not hide *who is talking to whom*.
The wire `traffic` struct (`ironwood/network/traffic.go`) carries
`source publicKey` and `dest publicKey` as plain, unencrypted fields
alongside the (encrypted) `payload` — only the payload is protected by
`encrypted`'s session layer. Route discovery is worse: when a node has
no cached path to a destination, it sends a `pathLookup{source, dest}`
that gets **multicast to a bloom-filter-scoped subset of the tree**
(`network/pathfinder.go`, `_sendLookup`/`_handleLookup`) — meaning
several real intermediate nodes, not just the two endpoints, see the
plaintext key pair "A wants to reach B" as a matter of normal protocol
operation, not a compromise. **Any node that decodes a `traffic` or
`pathLookup` packet - which every relay does as part of ordinary
forwarding - has `source`/`dest` sitting in memory**, whether or not its
own forwarding logic happens to need them (for already-pathed
`traffic`, the fast path only consults `path`/`peerPort` and doesn't
need `source`/`dest` to forward correctly - but the fields are decoded
into the struct regardless, and nothing stops a modified build from
logging them). This is true of vanilla Yggdrasil today, independent of
Garlic entirely, and it materially affects every "can this relay learn
who's talking to whom" question below - see `docs/garlic-security.md`'s
discussion of what this changes.

**Cannot see (still true):** the plaintext of any onion layer, or which
of possibly several bundled messages correspond to which real sender.
The above correction is about *routing metadata* (which keys exchanged
traffic), not payload.

## Malicious relay (one Garlic-capable circuit hop, not colluding)

**Can see:** the previous hop's node key (whoever sent it the packet, at
the ironwood/transport level — this is unavoidable, someone has to
address the message), the next hop's node key (from its own peeled
`LayerPlaintext.NextHop`), and packet timing/size at its own position in
the circuit.

**Cannot see:** anything about hops before the previous one or after the
next one, or the payload of any other layer (proven directly by
`TestBuildOnionHopCannotDecryptAnotherHopsLayer`). Cannot distinguish "I
am hop 2 of 5" from "I am hop 2 of 2" from the message alone.

**Known weakness — ephemeral key reuse across hops.** Per
`docs/garlic-protocol.md` §4.1, a circuit's originator uses **one**
ephemeral public key for every hop's ECDH, carried unchanged in every
forwarded message. Two colluding relays on the same circuit (see "Sybil"
below) can trivially confirm they're on the same circuit by comparing
that ephemeral public key byte-for-byte — a real linkability signal a
design with per-hop-blinded key material (as Tor/Sphinx use) would not
have. This is a deliberate simplification (documented in
`docs/garlic-architecture.md`'s roadmap as trading a telescoping
handshake for a much simpler non-interactive construction) and a
concrete item for a future hardening pass, not a hidden defect.

## Mesh-path intermediate node (not a chosen circuit hop, sits on the route between two of them)

This category didn't exist in the original version of this document and
follows directly from the correction above. When circuit hop *i* sends a
`msgTypeCircuitData` message to hop *i+1*, that's one ironwood
`encrypted` session, and per the mesh routing layer's own design, it may
transit any number of ordinary Yggdrasil nodes at the `network` layer to
get there (`docs/garlic-compatibility.md` calls this "role 1"). Per the
correction above, **any one of those in-between nodes can see the real
node keys of hop *i* and hop *i+1*** (via `traffic.source`/`dest`, or via
a `pathLookup` if no path was cached yet) — the same visibility a
"malicious relay" (a chosen Garlic hop) has into its *own* immediate
neighbors, except this adversary never had to be selected as a circuit
hop at all. It still cannot decrypt the `encrypted` session payload
(so no `LayerPlaintext`, no onion content), and it still can't tell
which position in the circuit it's observing traffic for, or correlate
it to a specific circuit ID without also being one of the two Garlic
identities exchanging that traffic. But it can build a graph of
"Garlic-tagged-looking traffic volumes between key X and key Y" (traffic
*is* observable in size/timing/existence even without decrypting it or
knowing it's Garlic) for every hop-pair a circuit's path happens to
route through — for free, without running any Garlic code, just by
sitting in the right place in the mesh topology.

**Why this matters for path selection:** an adversary doesn't need to be
*chosen* as a Garlic hop to gain this visibility for a given hop-pair —
they only need to be topologically positioned on the route ironwood's
routing would pick between two chosen hops. This is a concrete point in
favor of the "topologically diverse hop selection" idea discussed
separately (see the conversation this document was updated from) as a
way to make an adversary's job harder without needing every relay to run
Garlic-aware code.

## Malicious introduction point

Not exercised by any current code path beyond `Rendezvous.Publish` — see
`docs/garlic-rendezvous.md`. An introduction point learns that it has
been designated for a given GID and observes lookup/connection-setup
traffic naming that GID, but under the current circuit-hop model, only
decrypts application payload if it also happens to be the circuit's
terminal hop (not automatic — an intro point and a circuit's final hop
are different concepts that are not currently linked by any code).

## Malicious endpoint (the circuit's final hop / the service itself)

**Learns:** the full payload delivered to it (it's the intended
recipient — this is not a leak, it's the point) and the identity of
whichever node is immediately before it in the circuit (the previous
hop's key, same as any relay). **Does not learn:** the true originator's
identity, unless the circuit has fewer than 2 hops (a 1-hop "circuit" —
supported, see `TestBuildOnionSingleHop` — gives the sole hop full
visibility into both ends; this is expected of a 1-hop path and is why
`Config.PathLength` defaults to 3, not 1).

## Global passive adversary (observes a large fraction of the network)

Retains real capability, **more than the pre-correction version of this
document implied.** Because ironwood's own routing layer exposes real
source/dest keys to intermediate nodes (see the correction above), a
global adversary doesn't need to compromise payload encryption at all —
observing enough of the mesh already gives it the same
"key X talked to key Y, this much data, at this time" graph an adversary
watching an unencrypted network would have, for every hop-to-hop link a
circuit's path touches, chosen-hop or not. Multi-hop relaying still
raises the cost of correlating a circuit's *true* endpoints specifically
(the adversary has to link multiple such hop-pair observations into one
circuit, which requires more than any single vantage point gives it) but
this project does **not** claim to defeat a global adversary, and should
be read as claiming *less* than before this correction, not the same. No
padding, cover traffic, or timing obfuscation is active by default in
this version (`Envelope.PadTo` and `Bundle.AddCoverMessage` are
implemented, tested primitives — see `docs/garlic-protocol.md` §7 — but
are not wired into `SendGarlic`'s default send path). Until they are,
packet size and timing on a given link are exactly what they'd be
without Garlic, which is meaningful
metadata to a global adversary.

## Traffic correlation / traffic confirmation

Follows directly from the above: without active padding/cover
traffic/jitter, an adversary who can watch traffic at both the entry and
exit of a circuit simultaneously can attempt classic timing/size
correlation to confirm (not just suspect) that two observed flows are
the same circuit. This is a standard limitation of onion routing without
active traffic-shaping, not specific to this implementation, but it's
real and unmitigated here.

## Replay

Mitigated for the threat it targets (a captured packet being
retransmitted to trigger duplicate processing): every hop maintains a
bounded 2048-bit sliding-window `ReplayWindow` keyed by
`(circuit ID, packet counter)` (`docs/garlic-protocol.md` §5), proven by
`TestProcessCircuitDataDropsReplay` and the `ReplayWindow` unit tests.
Not a defense against an adversary who can prevent the *original* packet
from arriving and substitute their own timing (that's a routing/
availability concern, separate from replay).

## Packet tagging (attacker marks a packet to trace it through the network)

An AEAD-authenticated ciphertext cannot be modified without detection —
`Seal`/`Open` (and by extension `EncryptLayer`/`DecryptLayer`) reject any
tampered input (`TestOpenRejectsTamperedCiphertext`,
`TestDecryptLayerRejectsTamperedCiphertext`). An attacker who can't
forge a valid tag can't tag a packet in a way that survives to the next
hop; a hop that receives a tampered packet drops it rather than
forwarding a marked one.

## Route manipulation (attacker tries to influence path selection)

Circuit paths in this version are chosen entirely by the **originator**,
from hops it has already directly queried via `QueryCapability` — there
is no path-selection input an intermediate or remote party can inject.
The weakness here is upstream of route manipulation: nothing in this
version implements diverse/weighted random path selection at all (no
"pick N hops from a pool with diversity constraints" logic exists yet);
`CreateCircuit` takes an explicit, caller-supplied hop list. Whoever
calls `CreateCircuit` (a human, or future selection logic) is entirely
responsible for path quality and diversity today.

## Sybil nodes

An adversary running many Garlic-capable nodes can bias a naive
path-selection strategy toward paths it controls end-to-end, because
`QueryCapability`/hop selection has no reputation, diversity, or
resource-cost mechanism to make running many identities expensive.
**Not mitigated** in this version — flagged explicitly as unsolved,
consistent with the instruction not to claim protection this codebase
doesn't provide. A future path-selection implementation should treat
Sybil resistance as a first-class requirement (e.g. weighting by
independent network/AS diversity, not just by capability response), not
retrofit it.

## Intersection attacks

Not addressed by anything in this version. An adversary who can observe
a target's activity over multiple sessions/circuits and correlate what's
common across them (classic intersection-attack methodology) is not
defended against by per-circuit relaying alone. This would require
active cover traffic and/or careful circuit-rotation policy that doesn't
exist yet (`Config.CircuitLifetime` bounds how long a single circuit
lives, which limits — but does not eliminate — how much traffic
correlates to one circuit's identity).

## Summary table

| Adversary | Real capability retained |
|---|---|
| Passive observer | Sees traffic exists, sizes, timing; not payload. Cannot see the Garlic tag itself (inside the encrypted session) |
| Single malicious relay (chosen Garlic hop) | Sees its own hop's real-key neighbors (unavoidable, via ironwood's own unencrypted `source`/`dest` fields, not something Garlic hides); cannot decrypt other layers; ephemeral-key reuse is a linkability signal if colluding with another hop |
| Mesh-path intermediate node (not a chosen hop) | Same real-key-pair visibility as a malicious relay, for any hop-pair its position sits between - without ever being selected as a circuit hop. New finding, see dedicated section above |
| Malicious introduction point | Sees GID lookups; payload only if also the terminal hop |
| Malicious endpoint | Sees delivered payload (expected) and its own previous hop |
| Global passive adversary | Real capability, stronger than a naive reading of "payload is encrypted" suggests - routing metadata (who talks to whom) is not encrypted at the ironwood network layer at all |
| Traffic correlation | Real capability - no padding/cover traffic active by default |
| Replay | Mitigated within the bounded replay window |
| Packet tagging | Mitigated by AEAD authentication |
| Route manipulation | N/A - no automated path selection exists yet to manipulate |
| Sybil | Not mitigated - no diversity/reputation mechanism |
| Intersection attacks | Not mitigated |
