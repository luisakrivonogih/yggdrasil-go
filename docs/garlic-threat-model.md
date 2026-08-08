# Garlic Routing Overlay — Threat Model

This system is **privacy-enhanced routing**. It is not described as
"anonymous" anywhere in this project, and this document is why: several
of the adversary classes below retain real capability. Treat every
"mitigated" claim below as bounded by its stated adversary, not absolute.

Scope: this analyzes what's actually implemented (`docs/garlic-protocol.md`),
not the aspirational full design in `docs/garlic-architecture.md`.

## Passive observer (watches network traffic, not a participant)

**Can see:** that two Yggdrasil nodes are exchanging encrypted traffic
(ironwood's own transport-layer encryption already hides payload from
anyone who isn't one of the two communicating keys — true for Garlic
traffic exactly as it's true for ordinary IPv6 traffic, and not a
property this project adds). Packet sizes and timing on links it can
observe. Whether a given link carries `typeSessionGarlic`-tagged traffic
is **not** visible — the tag byte is inside ironwood's own encrypted
payload.

**Cannot see:** the plaintext of any onion layer, which of possibly
several bundled/relayed messages correspond to which real sender, or
(without controlling a circuit's hops) the full path a circuit takes.

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

Retains real capability. Multi-hop relaying raises the cost of
correlating a circuit's endpoints — an adversary must observe (or
compromise) enough of the path simultaneously — but this project does
**not** claim to defeat a global adversary. No padding, cover traffic, or
timing obfuscation is active by default in this version (`Envelope.PadTo`
and `Bundle.AddCoverMessage` are implemented, tested primitives — see
`docs/garlic-protocol.md` §7 — but are not wired into `SendGarlic`'s
default send path). Until they are, packet size and timing on a given
link are exactly what they'd be without Garlic, which is meaningful
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
| Passive observer | Sees encrypted traffic exists; not Garlic-specific, not payload |
| Single malicious relay | Sees only its own hop's neighbors; cannot decrypt other layers; ephemeral-key reuse is a linkability signal if colluding with another hop |
| Malicious introduction point | Sees GID lookups; payload only if also the terminal hop |
| Malicious endpoint | Sees delivered payload (expected) and its own previous hop |
| Global passive adversary | Real capability - no padding/cover traffic active by default |
| Traffic correlation | Real capability - same reason |
| Replay | Mitigated within the bounded replay window |
| Packet tagging | Mitigated by AEAD authentication |
| Route manipulation | N/A - no automated path selection exists yet to manipulate |
| Sybil | Not mitigated - no diversity/reputation mechanism |
| Intersection attacks | Not mitigated |
