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
this project does **not** claim to defeat a global adversary.

Since this section was first written, `Config.PaddingEnabled` and
`Config.JitterEnabled` (both default on) are wired into the actual send
and relay-forward path (`docs/garlic-protocol.md` §9): every packet's
wire size is independently re-randomized per hop
(`Envelope.PadToRandomRange`), and every packet's send is independently
delayed by a random amount before transmission
(`src/garlic/jitter.go`). `SendGarlicBundled` (§7 of the protocol doc)
additionally lets a real message travel alongside cover entries an
observer cannot distinguish from it. These raise the cost of the
size/timing correlation a global adversary would otherwise get for
free, but do **not** defeat one: there is no fixed-interval batching
(a determined adversary can still average over enough samples to erode
jitter's effect), padding ranges are configurable and therefore
fingerprintable if left at non-default values, and bundling only
helps for calls that actually opt into `SendGarlicBundled` with a
non-zero `coverCount` — `SendGarlic`'s plain path sends one real packet
with no chaff. A global adversary correlating enough traffic over
enough time is not something this project claims to defeat, and this
document should be read accordingly: real cost has been added, not a
guarantee.

## Traffic correlation / traffic confirmation

An adversary who can watch traffic at both the entry and exit of a
circuit simultaneously can attempt classic timing/size correlation to
confirm (not just suspect) that two observed flows are the same
circuit. This project now has three independent mitigations engaged by
default, each raising the cost of this attack without eliminating it:

- **Per-hop size re-randomization** (`Config.PaddingEnabled`,
  `docs/garlic-protocol.md` §9): breaks the naive "same size in and out
  at every hop" correlation signal. Does not hide the *distribution* of
  sizes a sustained flow produces — an adversary with enough samples on
  both ends can still attempt statistical (not exact) size correlation.
- **Per-packet jitter** (`Config.JitterEnabled`, same section): breaks
  exact send-timestamp correlation across hops. A bounded worker pool
  and queue mean jitter is skipped (packet sent immediately) once the
  queue is full under load — an adversary who can induce that load
  degrades this defense as a side effect.
- **Cover traffic via bundling** (`SendGarlicBundled`,
  `docs/garlic-protocol.md` §7): the strongest of the three, but
  opt-in per call — a caller that never sets `coverCount > 0` gets none
  of this benefit, and even with cover traffic, an adversary correlating
  *volume* (not individual packet identity) across many bundles over
  time is not addressed.

None of this amounts to a mixnet with formal anonymity-set guarantees.
This is a standard limitation of onion routing without a dedicated
mixing protocol (fixed-size, fixed-interval batching with a real
anonymity set), not specific to this implementation, but it remains
real: a sufficiently patient, sufficiently well-positioned adversary
retains a statistical correlation attack.

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

Circuit paths are chosen entirely by the **originator** — there is no
path-selection input an intermediate or remote party can inject.
`CreateCircuit` still takes an explicit, caller-supplied hop list, so
whoever builds that list is responsible for its quality; but a caller
now has a real option instead of picking hops by hand:
`Garlic.SelectPath(n)` (`docs/garlic-protocol.md` §10) builds that list
from topologically diverse candidates automatically. This does not
close route manipulation as a category (an adversary still cannot
inject path-selection input either way, so there's nothing new to
manipulate), but it does mean "diverse selection" is no longer purely
aspirational — it exists and a caller must actively choose not to use
it.

## Sybil nodes

An adversary running many Garlic-capable nodes can bias a naive
path-selection strategy toward paths it controls end-to-end. Two real,
partial mitigations now exist, alongside real remaining gaps:

- **`SelectDiversePath`** (`docs/garlic-protocol.md` §10,
  `src/garlic/selection.go`) prefers topologically distant candidates
  (mesh hop count via `core.Core.GetPaths()`) and rejects picking two
  candidates that share an immediate spanning-tree parent
  (`core.Core.GetTree()`). This raises the cost of the *simplest* Sybil
  strategy — deploying several identities on the same link or local
  segment and hoping a naive selector picks more than one of them.
- **Multipath pools** (`docs/garlic-protocol.md` §10,
  `src/garlic/multipath.go`) mean an adversary controlling one path in a
  pool sees only the fraction of traffic routed over that path, not the
  whole conversation — it must control *every* path in the pool to
  reconstruct the full picture, which is strictly more expensive than
  controlling a single circuit.

**What remains genuinely unmitigated:** neither mechanism has any
concept of IP/ASN diversity or real-world operator identity — an
adversary who deploys nodes with genuinely diverse tree positions (not
sharing a tree parent, not close in hop count) defeats `SelectDiversePath`
entirely, since tree position is the only signal available, and
propagating a hop's real IP through gossip would itself be a privacy
cost for relay operators (a deliberate design choice, not an oversight
— see `docs/garlic-protocol.md` §8). There is no reputation system, no
proof-of-work or other resource cost to registering as a Garlic node,
and no mechanism that makes running many identities expensive. Treat
Sybil resistance here as "raises the bar above picking uniformly at
random or whatever answered first," not as solved.

## Intersection attacks

An adversary who observes a target's activity over multiple
sessions/circuits and correlates what's common across them (classic
intersection-attack methodology) is not defended against by anything
that identifies "this traffic came from a client, not a relay" — which
is the structural property intersection attacks exploit. This project's
mitigation is architectural, not a dedicated intersection-attack
defense: every Garlic-capable node is, by construction, both a
potential circuit originator *and* a relay for other nodes' circuits
(there is no separate "client-only" mode) — so an adversary observing
"key X sent/received Garlic-tagged traffic at time T" cannot conclude
from that alone whether X was the real source/destination or simply
relaying for someone else. This narrows what an intersection attack can
conclude from participation alone, but does **not** defeat one: an
adversary who can also correlate size/timing/volume across sessions
(the "Traffic correlation" section above) can still narrow down which
of a node's flows are its own versus relayed, especially against a
target with low relayed-traffic volume where "this node is relaying
right now" is itself a distinguishing signal. `Config.CircuitLifetime`
bounds how long a single circuit lives, which limits — but does not
eliminate — how much traffic correlates to one circuit's identity
across sessions. Deliberate circuit-rotation *policy* (when to build a
new circuit, how much to relay for others as camouflage) is left to the
caller; nothing in this version enforces one.

## Summary table

| Adversary | Real capability retained |
|---|---|
| Passive observer | Sees traffic exists, sizes, timing; not payload. Cannot see the Garlic tag itself (inside the encrypted session) |
| Single malicious relay (chosen Garlic hop) | Sees its own hop's real-key neighbors (unavoidable, via ironwood's own unencrypted `source`/`dest` fields, not something Garlic hides); cannot decrypt other layers; ephemeral-key reuse is a linkability signal if colluding with another hop |
| Mesh-path intermediate node (not a chosen hop) | Same real-key-pair visibility as a malicious relay, for any hop-pair its position sits between - without ever being selected as a circuit hop |
| Malicious introduction point | Sees GID lookups; payload only if also the terminal hop |
| Malicious endpoint | Sees delivered payload (expected) and its own previous hop |
| Global passive adversary | Real capability - routing metadata (who talks to whom) is not encrypted at the ironwood network layer at all; per-hop padding/jitter/bundling (default on) raise the cost of correlation but do not defeat a patient, well-positioned adversary |
| Traffic correlation | Raised cost via default-on per-hop size randomization and send jitter, plus opt-in cover traffic (`SendGarlicBundled`) - not a mixnet, statistical correlation over enough samples remains possible |
| Replay | Mitigated within the bounded replay window |
| Packet tagging | Mitigated by AEAD authentication |
| Route manipulation | N/A - no path-selection input an intermediate/remote party can inject either way; `SelectPath` is available but not mandatory |
| Sybil | Partially mitigated - `SelectDiversePath` (tree-position diversity) and multipath pools raise the cost of the simplest strategies; no IP/ASN diversity, reputation, or resource-cost mechanism exists |
| Intersection attacks | Narrowed, not defeated - every node is structurally both a possible originator and a relay for others, so participation alone doesn't distinguish "this is my traffic" from "I'm relaying"; still erodable via traffic-correlation across sessions |
