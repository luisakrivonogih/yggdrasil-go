# Garlic Autonomous Routing Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let a Garlic-enabled node build, maintain, and rotate its own circuits from a couple of operator-supplied bootstrap peers — no manual hop-key entry — with a self-verified/gossiped trust split and default-on cover traffic, none of it touching the existing manual Garlic API.

**Architecture:** Additive throughout. A new `SelfVerified` trust bit on discovery entries; a two-stage guard-then-diverse hop selector; a new gossip-pull wire message; a fully separate `msgTypeCircuitDataV3` wire path (own delivery channel, own send helper) carrying both real auto-pool traffic and cover traffic, so the shipped `SendGarlic`/`RecvGarlic`/`createGarlicCircuit` path is provably untouched; a background loop that fills/rotates a small circuit pool and schedules cover packets over it.

**Tech Stack:** Go (`src/garlic`, `src/config`, `cmd/yggdrasil`), existing hjson-based `NodeConfig`, existing admin-socket JSON RPC convention, SvelteKit dashboard (`yggdashboard`).

**Spec:** `docs/superpowers/specs/2026-08-23-garlic-autonomous-routing-design.md`

## Global Constraints

- Existing `SendGarlic`, `RecvGarlic`, `CreateCircuit`, manual `createGarlicCircuit` RPC, and `msgTypeCircuitData` wire format: **zero behavior change**. Every new wire/delivery path in this plan is additive and separate.
- `Config.Garlic.Enabled = false` (existing top-level switch): unaffected, as today.
- First hop of any auto-built circuit is always drawn from self-verified candidates only; no persistent/guard pinning across rotations (per explicit decision).
- `CapabilityAutoCircuit` is advertised unconditionally by any node running this code, independent of whether that operator has `AutoPoolEnabled`/`CoverTrafficEnabled` on — a node can relay/terminate for others' auto-pool circuits without running its own (no "client-only mode", matching the existing Garlic relay-participation design).
- Forwarding an auto-pool packet must echo the same outer message type it received (`msgTypeCircuitDataV3`), never hardcode `msgTypeCircuitData` — the single most safety-critical line in this plan (Task 6).
- Go version/build conventions, test style (table-driven where the existing file already uses it, `t.Fatalf`/`t.Errorf` per existing convention), and doc-comment style: follow whatever the file being edited already does.

---

### Task 1: Discovery trust tiers

**Files:**
- Modify: `src/garlic/discovery.go`
- Test: `src/garlic/discovery_test.go`

**Interfaces:**
- Produces: `DiscoveredPeer.SelfVerified bool` field; `discoveryRegistry.record` never downgrades an existing `SelfVerified: true` entry to `false`.

- [ ] **Step 1: Write the failing tests**

Add to `src/garlic/discovery_test.go`:

```go
func TestDiscoveryRegistryRecordSelfVerifiedDefaultsFalse(t *testing.T) {
	r := newDiscoveryRegistry(16)
	r.record(DiscoveredPeer{NodeKey: []byte("a"), GarlicPublicKey: []byte("ga")})

	peers := r.list()
	if len(peers) != 1 || peers[0].SelfVerified {
		t.Fatalf("SelfVerified = %v, want false for a plain gossip-recorded entry", peers[0].SelfVerified)
	}
}

func TestDiscoveryRegistryRecordSelfVerifiedTrue(t *testing.T) {
	r := newDiscoveryRegistry(16)
	r.record(DiscoveredPeer{NodeKey: []byte("a"), GarlicPublicKey: []byte("ga"), SelfVerified: true})

	peers := r.list()
	if len(peers) != 1 || !peers[0].SelfVerified {
		t.Fatalf("SelfVerified = %v, want true", peers[0].SelfVerified)
	}
}

func TestDiscoveryRegistryRecordNeverDowngradesSelfVerified(t *testing.T) {
	r := newDiscoveryRegistry(16)
	r.record(DiscoveredPeer{NodeKey: []byte("a"), GarlicPublicKey: []byte("ga"), SelfVerified: true})
	// A later gossip mention of the same key, unverified by us directly.
	r.record(DiscoveredPeer{NodeKey: []byte("a"), GarlicPublicKey: []byte("ga-refreshed"), SelfVerified: false})

	peers := r.list()
	if len(peers) != 1 {
		t.Fatalf("list() returned %d peers, want 1", len(peers))
	}
	if !peers[0].SelfVerified {
		t.Fatal("a later gossip-sourced record downgraded an existing self-verified entry, want it to stay true")
	}
	if string(peers[0].GarlicPublicKey) != "ga-refreshed" {
		t.Fatalf("GarlicPublicKey = %q, want %q (other fields still refresh)", peers[0].GarlicPublicKey, "ga-refreshed")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd src/garlic && go test ./... -run TestDiscoveryRegistryRecordSelfVerified -v`
Expected: compile failure (`SelfVerified` field does not exist).

- [ ] **Step 3: Implement**

In `src/garlic/discovery.go`, change the `DiscoveredPeer` struct (around line 120):

```go
// DiscoveredPeer is one entry in a discoveryRegistry.
type DiscoveredPeer struct {
	NodeKey         []byte
	GarlicPublicKey []byte
	LastSeen        time.Time
	// SelfVerified is true iff this node itself completed a capability
	// handshake with this peer (handleCapabilityResponse), as opposed to
	// only ever hearing about it secondhand via gossip (processAnnounce).
	// Never downgraded by record() once true - see its doc comment.
	SelfVerified bool
}
```

Replace `record` (around line 143):

```go
// record adds or refreshes a peer's entry, stamping LastSeen as now.
// SelfVerified is never downgraded: once a peer has been personally
// capability-verified, a later secondhand gossip mention of the same key
// still leaves it marked self-verified.
func (r *discoveryRegistry) record(p DiscoveredPeer) {
	key := string(p.NodeKey)
	p.LastSeen = time.Now()

	r.mu.Lock()
	defer r.mu.Unlock()
	existing, exists := r.peers[key]
	if !exists && len(r.peers) >= r.max {
		r.evictOldestLocked()
	}
	if exists && existing.SelfVerified {
		p.SelfVerified = true
	}
	r.peers[key] = p
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd src/garlic && go test ./... -run TestDiscoveryRegistry -v`
Expected: PASS (all `TestDiscoveryRegistry*` tests, old and new).

- [ ] **Step 5: Commit**

```bash
git add src/garlic/discovery.go src/garlic/discovery_test.go
git commit -m "garlic: add SelfVerified trust tier to discovered peers"
```

---

### Task 2: HopCandidate.SelfVerified plumbing

**Files:**
- Modify: `src/garlic/selection.go`
- Modify: `src/garlic/manager.go` (`candidatePool`, `handleCapabilityResponse`)
- Modify: `src/garlic/protocol.go` (`processAnnounce`)
- Test: `src/garlic/integration_test.go`

**Interfaces:**
- Consumes: `DiscoveredPeer.SelfVerified` (Task 1).
- Produces: `HopCandidate.SelfVerified bool`, carried through by `candidatePool()`.

**Note on test placement:** `candidatePool()` calls `g.HopCount()` →
`core.Core.GetPaths()`, so it can only be meaningfully exercised against a
real `core.Core` that has actually resolved a path to a candidate (a
capability query does this as a side effect). This package's existing
convention for that is `integration_test.go`'s real-mesh harness (see
`TestIntegrationSelectPathAgainstRealTopology`, already in that file) -
this task's test reuses that harness rather than `manager_test.go`'s
no-network `newTestGarlic` fixture, which has no `core` set at all.

- [ ] **Step 1: Write the failing test**

Add to `src/garlic/integration_test.go`, next to the existing
`TestIntegrationSelectPathAgainstRealTopology` (reuses the exact same
`newLinkedTestNode`/`connectChain`/`pumpAll`/`waitForCapability` helpers
already defined earlier in that file):

```go
func TestIntegrationCandidatePoolCarriesSelfVerifiedThrough(t *testing.T) {
	nodeA := newLinkedTestNode(t)
	nodeB := newLinkedTestNode(t)
	all := []*core.Core{nodeA, nodeB}
	for _, n := range all {
		defer n.Stop()
	}

	connectChain(t, all) // A -- B
	pumpAll(all)

	idA, err := garlic.NewIdentity()
	if err != nil {
		t.Fatalf("NewIdentity (A) returned error: %v", err)
	}
	idB, err := garlic.NewIdentity()
	if err != nil {
		t.Fatalf("NewIdentity (B) returned error: %v", err)
	}

	cfg := garlic.DefaultConfig()
	cfg.CapabilityTimeout = 2 * time.Second

	gA := garlic.New(nodeA, idA, cfg, garlic.NewStaticRendezvous())
	defer gA.Close()
	gB := garlic.New(nodeB, idB, cfg, garlic.NewStaticRendezvous())
	defer gB.Close()

	// A directly capability-queries B, which resolves a real mesh path
	// AND records B as self-verified (Task 1/2's handleCapabilityResponse
	// change).
	waitForCapability(t, gA, nodeB.PublicKey(), 60*time.Second)

	selected, err := gA.SelectPath(1)
	if err != nil {
		t.Fatalf("SelectPath returned error: %v", err)
	}
	if len(selected) != 1 || !selected[0].SelfVerified {
		t.Fatalf("SelectPath(1) = %+v, want one self-verified candidate (B, directly queried by A)", selected)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd src/garlic && go test ./... -run TestIntegrationCandidatePoolCarriesSelfVerifiedThrough -v`
Expected: compile failure (`HopCandidate.SelfVerified` undefined) or FAIL.

- [ ] **Step 3: Implement**

In `src/garlic/selection.go`, add a field to `HopCandidate` (around line 28):

```go
// HopCandidate is one candidate for SelectDiversePath, combining a
// discovered peer's identity with topology data about it.
type HopCandidate struct {
	NodeKey         []byte
	GarlicPublicKey []byte
	HopCount        int
	TreeParent      []byte // this candidate's immediate parent in core.Core.GetTree(), if known
	SelfVerified    bool   // mirrors DiscoveredPeer.SelfVerified - see discovery.go
}
```

In `src/garlic/manager.go`, update `candidatePool` (around line 324):

```go
		pool = append(pool, HopCandidate{
			NodeKey:         p.NodeKey,
			GarlicPublicKey: p.GarlicPublicKey,
			HopCount:        hops,
			TreeParent:      parentOf[string(p.NodeKey)],
			SelfVerified:    p.SelfVerified,
		})
```

Update `handleCapabilityResponse` (around line 417) to mark personally-verified entries:

```go
	if msg.SupportsGarlicV2() && len(msg.PublicKey) > 0 {
		g.discovery.record(DiscoveredPeer{
			NodeKey:         append([]byte(nil), from...),
			GarlicPublicKey: msg.PublicKey,
			SelfVerified:    true,
		})
	}
```

In `src/garlic/protocol.go`, update `processAnnounce` to mark gossip-sourced entries explicitly (around line 166):

```go
	for _, p := range msg.Peers {
		if len(p.NodeKey) == 0 || len(p.GarlicPublicKey) == 0 {
			continue
		}
		g.discovery.record(DiscoveredPeer{
			NodeKey:         p.NodeKey,
			GarlicPublicKey: p.GarlicPublicKey,
			SelfVerified:    false,
		})
	}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd src/garlic && go test ./... -v`
Expected: PASS, full package (this touches shared call sites — run the whole package, not just the new test).

- [ ] **Step 5: Commit**

```bash
git add src/garlic/selection.go src/garlic/manager.go src/garlic/protocol.go src/garlic/integration_test.go
git commit -m "garlic: carry SelfVerified through candidatePool, tag verified/gossiped call sites"
```

---

### Task 3: Guard-first hop selection policy

**Files:**
- Modify: `src/garlic/selection.go`
- Test: `src/garlic/selection_test.go`

**Interfaces:**
- Consumes: `HopCandidate.SelfVerified` (Task 2).
- Produces: `SelectPathWithGuardPolicy(pool []HopCandidate, n, minHopCount int) ([]HopCandidate, error)`; `ErrNoSelfVerifiedCandidates`.

- [ ] **Step 1: Write the failing tests**

Add to `src/garlic/selection_test.go`:

```go
func TestSelectPathWithGuardPolicyFirstHopIsSelfVerified(t *testing.T) {
	pool := []HopCandidate{
		{NodeKey: []byte("gossiped-far"), HopCount: 10, SelfVerified: false},
		{NodeKey: []byte("verified-near"), HopCount: 2, SelfVerified: true},
	}
	selected, err := SelectPathWithGuardPolicy(pool, 1, 0)
	if err != nil {
		t.Fatalf("SelectPathWithGuardPolicy returned error: %v", err)
	}
	if len(selected) != 1 || string(selected[0].NodeKey) != "verified-near" {
		t.Fatalf("selected = %+v, want the self-verified candidate even though it has a lower hop count", selected)
	}
}

func TestSelectPathWithGuardPolicyErrorsWithNoSelfVerified(t *testing.T) {
	pool := []HopCandidate{
		{NodeKey: []byte("gossiped-1"), HopCount: 10, SelfVerified: false},
		{NodeKey: []byte("gossiped-2"), HopCount: 9, SelfVerified: false},
	}
	if _, err := SelectPathWithGuardPolicy(pool, 2, 0); !errors.Is(err, ErrNoSelfVerifiedCandidates) {
		t.Fatalf("err = %v, want ErrNoSelfVerifiedCandidates", err)
	}
}

func TestSelectPathWithGuardPolicyRemainingHopsFromFullPool(t *testing.T) {
	pool := []HopCandidate{
		{NodeKey: []byte("guard"), HopCount: 5, SelfVerified: true, TreeParent: []byte("p-guard")},
		{NodeKey: []byte("gossiped-far"), HopCount: 8, SelfVerified: false, TreeParent: []byte("p-other")},
	}
	selected, err := SelectPathWithGuardPolicy(pool, 2, 0)
	if err != nil {
		t.Fatalf("SelectPathWithGuardPolicy returned error: %v", err)
	}
	if len(selected) != 2 || string(selected[0].NodeKey) != "guard" || string(selected[1].NodeKey) != "gossiped-far" {
		t.Fatalf("selected = %+v, want [guard, gossiped-far] (second hop may be gossip-sourced)", selected)
	}
}

func TestSelectPathWithGuardPolicySecondHopAvoidsGuardsTreeParent(t *testing.T) {
	pool := []HopCandidate{
		{NodeKey: []byte("guard"), HopCount: 5, SelfVerified: true, TreeParent: []byte("shared-parent")},
		{NodeKey: []byte("sibling-of-guard"), HopCount: 9, SelfVerified: false, TreeParent: []byte("shared-parent")},
		{NodeKey: []byte("diverse"), HopCount: 4, SelfVerified: false, TreeParent: []byte("other-parent")},
	}
	selected, err := SelectPathWithGuardPolicy(pool, 2, 0)
	if err != nil {
		t.Fatalf("SelectPathWithGuardPolicy returned error: %v", err)
	}
	if len(selected) != 2 || string(selected[1].NodeKey) != "diverse" {
		t.Fatalf("selected = %+v, want second hop to skip the guard's tree-parent sibling", selected)
	}
}

func TestSelectDiversePathStillWorksAfterRefactor(t *testing.T) {
	// Regression: SelectDiversePath's own signature/behavior must be
	// unchanged by the internal refactor this task makes.
	pool := []HopCandidate{
		{NodeKey: []byte("near"), HopCount: 1},
		{NodeKey: []byte("far"), HopCount: 10},
	}
	selected, err := SelectDiversePath(pool, 1, 0)
	if err != nil || len(selected) != 1 || string(selected[0].NodeKey) != "far" {
		t.Fatalf("SelectDiversePath(pool, 1, 0) = %+v, %v; want [far], nil", selected, err)
	}
}
```

Add `"errors"` to the test file's imports if not already present.

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd src/garlic && go test ./... -run TestSelectPathWithGuardPolicy -v`
Expected: compile failure (`SelectPathWithGuardPolicy` undefined).

- [ ] **Step 3: Implement**

In `src/garlic/selection.go`, refactor `SelectDiversePath` to delegate to a new lower-level helper that accepts a pre-seeded `usedParents` set, then add the guard-policy function:

```go
var ErrNoSelfVerifiedCandidates = errors.New("garlic: no self-verified candidates available for the first hop")

// SelectDiversePath greedily selects n candidates from pool: sorted by
// descending HopCount (farther/more topologically distant preferred),
// skipping any candidate whose TreeParent matches an already-selected
// candidate's TreeParent. A candidate with an empty/unknown TreeParent
// never conflicts with anything (missing data isn't evidence of a shared
// parent). Candidates with HopCount below minHopCount are excluded
// entirely. Returns ErrInsufficientDiverseCandidates if fewer than n
// candidates can be selected under these constraints.
func SelectDiversePath(pool []HopCandidate, n, minHopCount int) ([]HopCandidate, error) {
	return selectDiversePathFrom(pool, n, minHopCount, map[string]bool{})
}

// selectDiversePathFrom is SelectDiversePath's implementation, taking an
// already-populated usedParents set so a caller (SelectPathWithGuardPolicy)
// can seed it with tree parents used by hops chosen in an earlier stage -
// diversity then holds across both stages, not just within either one.
func selectDiversePathFrom(pool []HopCandidate, n, minHopCount int, usedParents map[string]bool) ([]HopCandidate, error) {
	candidates := make([]HopCandidate, 0, len(pool))
	for _, c := range pool {
		if c.HopCount >= minHopCount {
			candidates = append(candidates, c)
		}
	}
	sortByHopCountDescending(candidates)

	selected := make([]HopCandidate, 0, n)
	for _, c := range candidates {
		if len(selected) == n {
			break
		}
		parentKey := string(c.TreeParent)
		if parentKey != "" && usedParents[parentKey] {
			continue
		}
		selected = append(selected, c)
		if parentKey != "" {
			usedParents[parentKey] = true
		}
	}
	if len(selected) < n {
		return nil, ErrInsufficientDiverseCandidates
	}
	return selected, nil
}

// SelectPathWithGuardPolicy chooses n circuit hops the same way
// SelectDiversePath does, with one added rule: the first hop (position
// 0) is drawn only from self-verified candidates - the position most
// sensitive to Sybil/deanonymization risk (docs/garlic-threat-model.md's
// Sybil section). Remaining hops are drawn from the full pool
// (self-verified + gossiped), diversity-checked against the guard's tree
// parent too, so hop 1 can't share it either. No persistence across
// calls - the guard is re-selected every call, by design (see
// docs/superpowers/specs/2026-08-23-garlic-autonomous-routing-design.md
// §3, "no Tor-style guard pinning").
func SelectPathWithGuardPolicy(pool []HopCandidate, n, minHopCount int) ([]HopCandidate, error) {
	if n <= 0 {
		return nil, ErrInsufficientDiverseCandidates
	}

	selfVerified := make([]HopCandidate, 0, len(pool))
	for _, c := range pool {
		if c.SelfVerified {
			selfVerified = append(selfVerified, c)
		}
	}
	usedParents := map[string]bool{}
	guard, err := selectDiversePathFrom(selfVerified, 1, minHopCount, usedParents)
	if err != nil {
		return nil, ErrNoSelfVerifiedCandidates
	}
	if n == 1 {
		return guard, nil
	}

	rest := make([]HopCandidate, 0, len(pool))
	for _, c := range pool {
		if bytes.Equal(c.NodeKey, guard[0].NodeKey) {
			continue
		}
		rest = append(rest, c)
	}
	remaining, err := selectDiversePathFrom(rest, n-1, minHopCount, usedParents)
	if err != nil {
		return nil, err
	}
	return append(guard, remaining...), nil
}
```

Add `"bytes"` to `src/garlic/selection.go`'s imports (currently only `"errors"`):

```go
import (
	"bytes"
	"errors"
)
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd src/garlic && go test ./... -run 'TestSelectPath|TestSelectDiversePath' -v`
Expected: PASS, all old and new selection tests.

- [ ] **Step 5: Commit**

```bash
git add src/garlic/selection.go src/garlic/selection_test.go
git commit -m "garlic: add SelectPathWithGuardPolicy (self-verified first hop)"
```

---

### Task 4: CapabilityAutoCircuit flag

**Files:**
- Modify: `src/garlic/capability.go`
- Modify: `src/garlic/protocol.go` (`processCapabilityRequest`)
- Test: `src/garlic/capability_test.go`

**Interfaces:**
- Produces: `CapabilityAutoCircuit` constant, `CapabilityMessage.SupportsAutoCircuit() bool`. `processCapabilityRequest` now advertises both `CapabilityGarlicV2` and `CapabilityAutoCircuit`.

- [ ] **Step 1: Write the failing tests**

Add to `src/garlic/capability_test.go`:

```go
func TestSupportsAutoCircuit(t *testing.T) {
	yes := &CapabilityMessage{Versions: []string{CapabilityGarlicV2, CapabilityAutoCircuit}}
	if !yes.SupportsAutoCircuit() {
		t.Fatal("SupportsAutoCircuit() = false, want true")
	}
	no := &CapabilityMessage{Versions: []string{CapabilityGarlicV2}}
	if no.SupportsAutoCircuit() {
		t.Fatal("SupportsAutoCircuit() = true, want false")
	}
}
```

Add to `src/garlic/manager_test.go` (or wherever `processCapabilityRequest` is already exercised — check for an existing `TestProcessCapabilityRequest*` test and place this alongside it):

```go
func TestProcessCapabilityRequestAdvertisesAutoCircuit(t *testing.T) {
	g := newTestGarlic(t)
	msg, err := UnmarshalCapabilityMessage(g.processCapabilityRequest())
	if err != nil {
		t.Fatalf("UnmarshalCapabilityMessage returned error: %v", err)
	}
	if !msg.SupportsAutoCircuit() {
		t.Fatal("processCapabilityRequest() does not advertise CapabilityAutoCircuit")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd src/garlic && go test ./... -run 'TestSupportsAutoCircuit|TestProcessCapabilityRequestAdvertisesAutoCircuit' -v`
Expected: compile failure (`CapabilityAutoCircuit`/`SupportsAutoCircuit` undefined).

- [ ] **Step 3: Implement**

In `src/garlic/capability.go`, add after `CapabilityGarlicV2` (around line 19):

```go
// CapabilityAutoCircuit is advertised by a node whose code understands
// the auto-pool wire path (msgTypeAnnounceRequest, msgTypeCircuitDataV3
// - see protocol.go) - independent of whether this operator has chosen
// to originate auto-pool circuits or cover traffic themselves
// (Config.AutoPoolEnabled/CoverTrafficEnabled). Every position in an
// auto-built circuit, not just the terminal hop, must advertise this
// before being selected - see
// docs/superpowers/specs/2026-08-23-garlic-autonomous-routing-design.md
// §8 for why the compatibility argument requires gating every position.
const CapabilityAutoCircuit = "garlic-v2-auto"
```

Add after `SupportsGarlicV2` (around line 51):

```go
// SupportsAutoCircuit reports whether the message advertises
// CapabilityAutoCircuit.
func (m *CapabilityMessage) SupportsAutoCircuit() bool {
	for _, v := range m.Versions {
		if v == CapabilityAutoCircuit {
			return true
		}
	}
	return false
}
```

In `src/garlic/protocol.go`, update `processCapabilityRequest` (around line 206):

```go
func (g *Garlic) processCapabilityRequest() []byte {
	msg := &CapabilityMessage{
		Versions:  []string{CapabilityGarlicV2, CapabilityAutoCircuit},
		PublicKey: g.identity.PublicKey,
	}
	payload, err := msg.Marshal()
	if err != nil {
		panic("garlic: failed to marshal own capability message: " + err.Error())
	}
	return payload
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd src/garlic && go test ./... -v`
Expected: PASS, full package.

- [ ] **Step 5: Commit**

```bash
git add src/garlic/capability.go src/garlic/protocol.go src/garlic/capability_test.go src/garlic/manager_test.go
git commit -m "garlic: add CapabilityAutoCircuit flag, advertise unconditionally"
```

---

### Task 5: Gossip-pull wire message

**Files:**
- Modify: `src/garlic/protocol.go` (message type constant)
- Modify: `src/garlic/manager.go` (`RequestGossip`, `handleIncoming`)
- Test: `src/garlic/integration_test.go`

**Interfaces:**
- Produces: `msgTypeAnnounceRequest`; `Garlic.RequestGossip(peer ed25519.PublicKey) error`.

- [ ] **Step 1: Write the failing test**

Add to `src/garlic/integration_test.go`, reusing the same real 2-node
harness as `TestIntegrationGossipDiscoversUnknownPeer` (which already
proves the existing push-only `GossipAnnounce` works end to end — this
test proves the new pull half):

```go
func TestIntegrationAnnounceRequestTriggersImmediateGossipAnnounce(t *testing.T) {
	nodeA := newLinkedTestNode(t)
	nodeB := newLinkedTestNode(t)
	nodeC := newLinkedTestNode(t)
	all := []*core.Core{nodeA, nodeB, nodeC}
	for _, n := range all {
		defer n.Stop()
	}

	connectChain(t, all) // A -- B -- C
	pumpAll(all)

	idA, err := garlic.NewIdentity()
	if err != nil {
		t.Fatalf("NewIdentity (A) returned error: %v", err)
	}
	idB, err := garlic.NewIdentity()
	if err != nil {
		t.Fatalf("NewIdentity (B) returned error: %v", err)
	}
	idC, err := garlic.NewIdentity()
	if err != nil {
		t.Fatalf("NewIdentity (C) returned error: %v", err)
	}

	cfg := garlic.DefaultConfig()
	cfg.CapabilityTimeout = 2 * time.Second

	gA := garlic.New(nodeA, idA, cfg, garlic.NewStaticRendezvous())
	defer gA.Close()
	gB := garlic.New(nodeB, idB, cfg, garlic.NewStaticRendezvous())
	defer gB.Close()
	gC := garlic.New(nodeC, idC, cfg, garlic.NewStaticRendezvous())
	defer gC.Close()

	// B learns about C directly. A learns about B directly, but never
	// queries C, and critically: B never queries A either, so (unlike
	// TestIntegrationGossipDiscoversUnknownPeer) A is NOT in B's
	// capabilityCache and B's periodic gossipTick would never target A.
	waitForCapability(t, gA, nodeB.PublicKey(), 60*time.Second)
	waitForCapability(t, gB, nodeC.PublicKey(), 60*time.Second)

	for _, p := range gA.KnownPeers() {
		if bytes.Equal(p.NodeKey, nodeC.PublicKey()) {
			t.Fatal("A already knows about C before any gossip happened - test setup is invalid")
		}
	}

	if err := gA.RequestGossip(nodeB.PublicKey()); err != nil {
		t.Fatalf("RequestGossip returned error: %v", err)
	}

	deadline := time.Now().Add(10 * time.Second)
	for {
		found := false
		for _, p := range gA.KnownPeers() {
			if bytes.Equal(p.NodeKey, nodeC.PublicKey()) && bytes.Equal(p.GarlicPublicKey, idC.PublicKey) {
				found = true
			}
		}
		if found {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("A never learned about C via RequestGossip's pull within the deadline; known peers: %+v", gA.KnownPeers())
		}
		time.Sleep(50 * time.Millisecond)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd src/garlic && go test ./... -run TestIntegrationAnnounceRequestTriggersImmediateGossipAnnounce -v`
Expected: compile failure (`RequestGossip` undefined) or FAIL once it compiles against a stub.

- [ ] **Step 3: Implement**

In `src/garlic/protocol.go`, extend the message-type block (around line 22):

```go
const (
	msgTypeCapabilityRequest byte = iota + 1
	msgTypeCapabilityResponse
	msgTypeCircuitData
	msgTypeAnnounce
	msgTypeCircuitDataBundle
	// msgTypeAnnounceRequest asks the recipient to immediately send back
	// a msgTypeAnnounce with its known-peer sample (empty body) - a
	// "pull" complementing the existing periodic gossipTick "push", so a
	// freshly bootstrapped node (not yet in anyone's capabilityCache, so
	// never a gossipTick target) can populate its candidate pool in one
	// round trip. See docs/superpowers/specs/2026-08-23-garlic-autonomous-routing-design.md §4.
	msgTypeAnnounceRequest
	// msgTypeCircuitDataV3 is the auto-pool circuit wire type - see §8 of
	// the same design doc and Task 6/7 of its implementation plan.
	msgTypeCircuitDataV3
)
```

In `src/garlic/manager.go`, add after `GossipAnnounce` (around line 295):

```go
// RequestGossip asks peer to immediately send this node its known-peer
// gossip sample (msgTypeAnnounceRequest, empty body) - see
// docs/superpowers/specs/2026-08-23-garlic-autonomous-routing-design.md
// §4. A peer running code without this feature simply never answers;
// handleIncoming's switch has no default case, so an unrecognized type
// byte is already silently ignored (Go zero-value switch fallthrough) -
// no capability check needed before sending this specific message.
func (g *Garlic) RequestGossip(peer ed25519.PublicKey) error {
	_, err := g.core.WriteGarlic([]byte{msgTypeAnnounceRequest}, iwt.Addr(peer))
	return err
}
```

Add a case to `handleIncoming`'s switch (around line 358):

```go
	switch data[0] {
	case msgTypeCapabilityRequest:
		resp := append([]byte{msgTypeCapabilityResponse}, g.processCapabilityRequest()...)
		_, _ = g.core.WriteGarlic(resp, iwt.Addr(from))
	case msgTypeCapabilityResponse:
		g.handleCapabilityResponse(from, data[1:])
	case msgTypeCircuitData:
		g.dispatchAction(g.processCircuitData(data[1:], msgTypeCircuitData), from)
	case msgTypeAnnounce:
		g.processAnnounce(data[1:])
	case msgTypeCircuitDataBundle:
		for _, action := range g.processCircuitDataBundle(data[1:]) {
			g.dispatchAction(action, from)
		}
	case msgTypeAnnounceRequest:
		_ = g.GossipAnnounce(from)
	case msgTypeCircuitDataV3:
		g.dispatchAction(g.processCircuitData(data[1:], msgTypeCircuitDataV3), from)
	}
```

(Note: this pre-applies Task 6's `processCircuitData` signature change so `handleIncoming` compiles in one consistent state — Task 6 is the task that actually implements the new signature and the `msgTypeCircuitDataV3` handling inside `processCircuitData`. If executing tasks in strict order, leave the `msgTypeCircuitDataV3` case and the `processCircuitData(..., msgType)` calls as shown here now; Task 6 changes `processCircuitData`'s definition to match, and Task 6 is the one that makes the whole package build again if Task 5 is committed alone with a stubbed second parameter. To keep every task's "tests pass" step honestly green in commit order, do Task 5 and Task 6 as one combined commit if your workflow requires each commit to build — call this out to the reviewer rather than silently reordering.)

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd src/garlic && go build ./... && go test ./... -v`
Expected: PASS once Task 6's `processCircuitData` signature change is also in place (see note above).

- [ ] **Step 5: Commit**

```bash
git add src/garlic/protocol.go src/garlic/manager.go src/garlic/integration_test.go
git commit -m "garlic: add gossip-pull wire message (msgTypeAnnounceRequest)"
```

---

### Task 6: msgTypeCircuitDataV3 — tagged processing and type-preserving forwarding

**This is the highest-risk task in this plan** — see the design doc §8 for why the naive alternatives were rejected, and the Global Constraints note on forwarding.

**Files:**
- Modify: `src/garlic/protocol.go` (`processCircuitData`, `processCircuitDataBundle`, `circuitAction`)
- Test: `src/garlic/relay_logic_test.go`

**Interfaces:**
- Consumes: `msgTypeCircuitDataV3` (Task 5).
- Produces: `processCircuitData(body []byte, msgType byte) circuitAction`; `circuitAction.tagged bool`.

- [ ] **Step 1: Write the failing tests**

`relay_logic_test.go` already has exactly the fixtures this needs:
`newTestGarlic(t) *Garlic` (a no-network `Garlic` with just enough state
for pure relay-decision logic) and `buildTestCircuitData(t, relayIdentities []*Identity, nodeKeys [][]byte, payload []byte, ttl time.Duration) (body []byte, circuitID CircuitID)`
(builds a real onion-wrapped circuitData *body* — i.e. exactly what
`processCircuitData` expects, the wire message with its leading type byte
already stripped). Add:

```go
func TestProcessCircuitDataV3ForwardPreservesMessageType(t *testing.T) {
	relay := newTestGarlic(t)
	destID, err := NewIdentity()
	if err != nil {
		t.Fatalf("NewIdentity returned error: %v", err)
	}
	destNodeKey := []byte("dest-node-key")
	payload := []byte("hello bob")

	msg, _ := buildTestCircuitData(t,
		[]*Identity{relay.identity, destID},
		[][]byte{[]byte("relay-node-key"), destNodeKey},
		payload, time.Minute)

	action := relay.processCircuitData(msg, msgTypeCircuitDataV3)
	if action.kind != actionForward {
		t.Fatalf("action.kind = %v, want actionForward", action.kind)
	}
	if got := action.forwardMsg[0]; got != msgTypeCircuitDataV3 {
		t.Fatalf("forwardMsg[0] = %d, want msgTypeCircuitDataV3 (%d) - forwarding must preserve the inbound type, never hardcode msgTypeCircuitData", got, msgTypeCircuitDataV3)
	}
}

func TestProcessCircuitDataPlainForwardStillUsesPlainType(t *testing.T) {
	// Regression: the existing msgTypeCircuitData path must be completely
	// unaffected by this task.
	relay := newTestGarlic(t)
	destID, err := NewIdentity()
	if err != nil {
		t.Fatalf("NewIdentity returned error: %v", err)
	}
	destNodeKey := []byte("dest-node-key")
	payload := []byte("hello bob")

	msg, _ := buildTestCircuitData(t,
		[]*Identity{relay.identity, destID},
		[][]byte{[]byte("relay-node-key"), destNodeKey},
		payload, time.Minute)

	action := relay.processCircuitData(msg, msgTypeCircuitData)
	if action.kind != actionForward {
		t.Fatalf("action.kind = %v, want actionForward", action.kind)
	}
	if got := action.forwardMsg[0]; got != msgTypeCircuitData {
		t.Fatalf("forwardMsg[0] = %d, want msgTypeCircuitData (%d)", got, msgTypeCircuitData)
	}
}

func TestProcessCircuitDataV3DeliverIsTagged(t *testing.T) {
	g := newTestGarlic(t)
	payload := []byte("hello bob")
	msg, _ := buildTestCircuitData(t, []*Identity{g.identity}, [][]byte{g.identity.PublicKey}, payload, time.Minute)

	action := g.processCircuitData(msg, msgTypeCircuitDataV3)
	if action.kind != actionDeliver {
		t.Fatalf("action.kind = %v, want actionDeliver", action.kind)
	}
	if !action.tagged {
		t.Fatal("action.tagged = false, want true for a msgTypeCircuitDataV3 delivery")
	}
}

func TestProcessCircuitDataPlainDeliverIsNotTagged(t *testing.T) {
	g := newTestGarlic(t)
	payload := []byte("hello bob")
	msg, _ := buildTestCircuitData(t, []*Identity{g.identity}, [][]byte{g.identity.PublicKey}, payload, time.Minute)

	action := g.processCircuitData(msg, msgTypeCircuitData)
	if action.kind != actionDeliver {
		t.Fatalf("action.kind = %v, want actionDeliver", action.kind)
	}
	if action.tagged {
		t.Fatal("action.tagged = true, want false for a plain msgTypeCircuitData delivery")
	}
}
```

**Important — this is a breaking signature change to an already-widely-used
method.** Every existing call to `processCircuitData(body)` (one argument)
in `relay_logic_test.go` (there are roughly a dozen — every
`TestProcessCircuitData*` test in that file, e.g.
`TestProcessCircuitDataTerminalHopDelivers`,
`TestProcessCircuitDataIntermediateHopForwards` and all its
`TestProcessCircuitDataForward*`/`TestProcessCircuitDataDrops*` siblings)
and in `protocol.go`'s own `processCircuitDataBundle` must be updated to
pass `msgTypeCircuitData` as the second argument. The compiler will fail
loudly, one call site at a time, until every one is fixed — go through
them all before moving to Step 2; do not leave any unfixed (the package
will not build otherwise, and this is a signature change every other test
file's compile depends on).

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd src/garlic && go test ./... -run TestProcessCircuitData -v`
Expected: compile failure (`processCircuitData` still takes one argument; `circuitAction.tagged` undefined) — this confirms the tests exist and the signature isn't implemented yet, before Step 3 fixes both the definition and every call site.

- [ ] **Step 3: Implement**

In `src/garlic/protocol.go`, update `circuitAction` (around line 54):

```go
// circuitAction is the outcome of processing one circuitData message:
// either nothing further to do (actionDrop - never explained further, see
// docs/garlic-architecture.md §17 on not leaking which check failed),
// deliver payload locally (this node is the circuit's final hop), or
// forward forwardMsg to forwardTo (this node is an intermediate hop).
type circuitAction struct {
	kind       actionKind
	circuitID  CircuitID
	payload    []byte
	forwardTo  []byte
	forwardMsg []byte
	// tagged is true iff this action arose from a msgTypeCircuitDataV3
	// packet - only actionDeliver consults it (see manager.go's
	// dispatchAction/deliverTagged); forwarding already preserves the
	// type byte directly in forwardMsg.
	tagged bool
}
```

Replace `processCircuitData`'s signature and its two type-dependent lines (around line 65):

```go
// processCircuitData decides what to do with the body of a
// msgTypeCircuitData or msgTypeCircuitDataV3 message (i.e. everything
// after that leading type byte) - msgType is that leading byte, needed
// so a forwarded packet echoes the same type it arrived as (never
// hardcoded - see docs/superpowers/specs/2026-08-23-garlic-autonomous-routing-design.md
// §8) and so a terminal delivery knows whether to tag the resulting
// circuitAction. It performs no I/O.
func (g *Garlic) processCircuitData(body []byte, msgType byte) circuitAction {
	if len(body) < circuitDataMinSize {
		g.security.malformedPackets.Add(1)
		return circuitAction{kind: actionDrop}
	}
	ephemeralPub := body[:KeySize]
	env, err := Unmarshal(body[KeySize:])
	if err != nil {
		g.security.malformedPackets.Add(1)
		return circuitAction{kind: actionDrop}
	}
	if env.Version != EnvelopeVersion1 {
		g.security.malformedPackets.Add(1)
		return circuitAction{kind: actionDrop}
	}
	if time.Now().Unix() > int64(env.Expiration) {
		g.security.expiredPackets.Add(1)
		return circuitAction{kind: actionDrop}
	}

	circuitID := env.CircuitID
	window, ok := g.relayState.replayWindowFor(circuitID)
	if !ok {
		g.security.relayTableFull.Add(1)
		return circuitAction{kind: actionDrop}
	}
	if !window.CheckAndSet(env.PacketCounter) {
		g.security.replayDrops.Add(1)
		return circuitAction{kind: actionDrop}
	}

	secret, err := ECDH(g.identity.PrivateKey, ephemeralPub)
	if err != nil {
		g.security.authFailures.Add(1)
		return circuitAction{kind: actionDrop}
	}
	key, err := deriveLayerKey(secret)
	if err != nil {
		g.security.authFailures.Add(1)
		return circuitAction{kind: actionDrop}
	}

	layer, err := DecryptLayer(key, env.PacketCounter, env.Body)
	if err != nil {
		g.security.authFailures.Add(1)
		return circuitAction{kind: actionDrop}
	}

	if len(layer.NextHop) == 0 {
		return circuitAction{kind: actionDeliver, circuitID: circuitID, payload: layer.Inner, tagged: msgType == msgTypeCircuitDataV3}
	}
	if len(layer.NextHopEphemeral) != KeySize {
		return circuitAction{kind: actionDrop}
	}

	nextEnv := &Envelope{
		Version:       EnvelopeVersion1,
		CircuitID:     env.CircuitID,
		PacketCounter: env.PacketCounter,
		Expiration:    env.Expiration,
		Body:          layer.Inner,
	}
	if g.cfg.PaddingEnabled {
		_ = nextEnv.PadToRandomRange(g.cfg.MinPaddedSize, g.cfg.MaxPaddedSize)
	}
	nextBytes, err := nextEnv.Marshal()
	if err != nil {
		g.security.malformedPackets.Add(1)
		return circuitAction{kind: actionDrop}
	}
	forwardMsg := make([]byte, 0, 1+KeySize+len(nextBytes))
	forwardMsg = append(forwardMsg, msgType)
	forwardMsg = append(forwardMsg, layer.NextHopEphemeral...)
	forwardMsg = append(forwardMsg, nextBytes...)

	return circuitAction{kind: actionForward, circuitID: circuitID, forwardTo: layer.NextHop, forwardMsg: forwardMsg}
}
```

Update `processCircuitDataBundle`'s call site (around line 189) — bundles stay on the plain type, unaffected by this feature:

```go
func (g *Garlic) processCircuitDataBundle(body []byte) []circuitAction {
	bundle, err := UnmarshalBundle(body)
	if err != nil {
		return nil
	}
	var actions []circuitAction
	for _, sub := range bundle.Messages {
		if action := g.processCircuitData(sub, msgTypeCircuitData); action.kind != actionDrop {
			actions = append(actions, action)
		}
	}
	return actions
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd src/garlic && go build ./... && go test ./... -v`
Expected: PASS, full package (this is a signature change touching every caller — a full package build is the real check, not just the new tests).

- [ ] **Step 5: Commit**

```bash
git add src/garlic/protocol.go src/garlic/relay_logic_test.go
git commit -m "garlic: add msgTypeCircuitDataV3 tagged processing, type-preserving forward"
```

---

### Task 7: Tagged delivery channel and auto-send helper

**Files:**
- Modify: `src/garlic/manager.go` (`Garlic` struct, `New`, `dispatchAction`, new helpers)
- Test: `src/garlic/integration_test.go`

**Interfaces:**
- Consumes: `circuitAction.tagged` (Task 6).
- Produces: `AutoDeliveredMessage`; `Garlic.autoDelivered chan AutoDeliveredMessage`; `Garlic.RecvGarlicAuto(timeout time.Duration) (*AutoDeliveredMessage, error)`; `Garlic.sendAutoPayload(id CircuitID, kind byte, payload []byte) error`; `Garlic.SendGarlicAuto(id CircuitID, payload []byte) error`; `autoPayloadKindReal`/`autoPayloadKindCover` constants.

**Note on test placement:** `SendGarlicAuto`/`sendAutoPayload` go through
`sendCircuitData` → the jitter scheduler → `g.core.WriteGarlic`, so they
need a `Garlic` built via the real `New(...)` constructor (a real
`core.Core`, real scheduler) — `manager_test.go`'s no-network
`newTestGarlic` fixture (from `relay_logic_test.go`, used by Task 1-6's
pure-logic tests) has neither `core` nor `scheduler` set and would nil-
panic. This task's tests reuse `integration_test.go`'s real 2-node
harness, mirroring `TestIntegrationSendGarlicThroughLegacyRelay`'s
existing shape but for the new auto-pool send/receive pair. Note also
that `sendAutoPayload` and `autoPayloadKindCover` are unexported — the
cover-traffic test below calls the exported `SendGarlicAuto` twice isn't
possible for that case, so it instead asserts the behavior it actually
needs to prove (a real payload round-trips, and nothing on the auto
channel leaks to the plain channel) without needing direct access to the
unexported cover-send path; Task 11 exercises `sendAutoPayload`'s cover
branch directly from `package garlic`-internal tests instead, where it's
reachable.

- [ ] **Step 1: Write the failing tests**

Add to `src/garlic/integration_test.go`:

```go
func TestIntegrationSendGarlicAutoThenRecvGarlicAutoRoundTrips(t *testing.T) {
	nodeA := newLinkedTestNode(t)
	nodeB := newLinkedTestNode(t)
	all := []*core.Core{nodeA, nodeB}
	for _, n := range all {
		defer n.Stop()
	}
	connectChain(t, all)
	pumpAll(all)

	idA, err := garlic.NewIdentity()
	if err != nil {
		t.Fatalf("NewIdentity (A) returned error: %v", err)
	}
	idB, err := garlic.NewIdentity()
	if err != nil {
		t.Fatalf("NewIdentity (B) returned error: %v", err)
	}

	cfg := garlic.DefaultConfig()
	cfg.CapabilityTimeout = 2 * time.Second

	gA := garlic.New(nodeA, idA, cfg, garlic.NewStaticRendezvous())
	defer gA.Close()
	gB := garlic.New(nodeB, idB, cfg, garlic.NewStaticRendezvous())
	defer gB.Close()

	capB := waitForCapability(t, gA, nodeB.PublicKey(), 60*time.Second)
	if !capB.SupportsAutoCircuit() {
		t.Fatal("B's capability response does not advertise CapabilityAutoCircuit")
	}

	circuitID, err := gA.CreateCircuit([]garlic.CapabilityMessage{*capB}, [][]byte{nodeB.PublicKey()})
	if err != nil {
		t.Fatalf("CreateCircuit returned error: %v", err)
	}

	if err := gA.SendGarlicAuto(circuitID, []byte("auto-hello")); err != nil {
		t.Fatalf("SendGarlicAuto returned error: %v", err)
	}
	msg, err := gB.RecvGarlicAuto(10 * time.Second)
	if err != nil {
		t.Fatalf("RecvGarlicAuto returned error: %v", err)
	}
	if string(msg.Payload) != "auto-hello" {
		t.Fatalf("Payload = %q, want %q", msg.Payload, "auto-hello")
	}
	if msg.CircuitID != circuitID {
		t.Fatalf("CircuitID = %x, want %x", msg.CircuitID, circuitID)
	}

	// Nothing sent via SendGarlicAuto should ever surface on B's plain
	// RecvGarlic channel.
	if _, err := gB.RecvGarlic(200 * time.Millisecond); !errors.Is(err, garlic.ErrRecvTimeout) {
		t.Fatalf("RecvGarlic err = %v, want ErrRecvTimeout (auto-pool traffic must stay off the manual delivery channel)", err)
	}
}
```

Add `"errors"` to this file's imports if not already present.

- [ ] **Step 2: Run test to verify it fails**

Run: `cd src/garlic && go test ./... -run TestIntegrationSendGarlicAutoThenRecvGarlicAutoRoundTrips -v`
Expected: compile failure (`SendGarlicAuto`/`RecvGarlicAuto`/`CapabilityAutoCircuit` undefined).

- [ ] **Step 3: Implement**

In `src/garlic/manager.go`, add near `DeliveredMessage` (around line 143):

```go
// AutoDeliveredMessage is an application payload that arrived because
// this node was the final hop of someone else's auto-pool circuit (see
// AutoCreateCircuit). Kept entirely separate from DeliveredMessage/
// g.delivered - a cover-traffic packet is silently discarded before it
// ever reaches this type, and nothing sent via SendGarlicAuto ever
// reaches the plain g.delivered/RecvGarlic path either.
type AutoDeliveredMessage struct {
	CircuitID CircuitID
	Payload   []byte
}

// autoPayloadKindReal/autoPayloadKindCover are the leading byte of every
// auto-pool circuit's Inner payload (see sendAutoPayload/deliverTagged) -
// entirely internal to this node's own auto-pool traffic, invisible to
// every intermediate hop (they never parse Inner) and meaningful only to
// the terminal hop that decrypts it.
const (
	autoPayloadKindReal  byte = 0
	autoPayloadKindCover byte = 1
)

// coverPayloadSize is the plaintext size of a cover packet's Inner
// content before AEAD encryption. AEAD ciphertext is indistinguishable
// from random regardless of plaintext content, and per-hop wire size is
// independently re-randomized by Config.PaddingEnabled/PadToRandomRange
// on top of this - a fixed small plaintext size is sufficient, no
// crypto/rand needed here.
const coverPayloadSize = 32
```

Add fields to the `Garlic` struct (around line 151):

```go
type Garlic struct {
	core     *core.Core
	identity *Identity
	cfg      Config

	circuits   *CircuitManager
	relayState *relayCircuitState
	limiter    *RateLimiter
	rendezvous Rendezvous
	scheduler  *jitterScheduler
	discovery  *discoveryRegistry
	security   SecurityCounters

	delivered     chan DeliveredMessage
	autoDelivered chan AutoDeliveredMessage

	mu              sync.Mutex
	capabilityCache map[string]*CapabilityMessage
	pending         map[string]chan *CapabilityMessage
	originEphemeral map[CircuitID][]byte
	pools           map[PoolID]*circuitPool
	autoPool        map[CircuitID]time.Time

	stop chan struct{}
}
```

Update `New` (around line 180) to initialize the new fields:

```go
func New(c *core.Core, identity *Identity, cfg Config, rendezvous Rendezvous) *Garlic {
	g := &Garlic{
		core:            c,
		identity:        identity,
		cfg:             cfg,
		circuits:        NewCircuitManager(CircuitManagerConfig{MaxCircuits: cfg.MaxCircuits, MaxCircuitsPerPeer: cfg.MaxCircuitsPerPeer}),
		relayState:      newRelayCircuitState(cfg.MaxRelayCircuits),
		limiter:         NewRateLimiter(cfg.RatePerSecond, cfg.RateBurst, cfg.MaxTrackedPeers),
		rendezvous:      rendezvous,
		discovery:       newDiscoveryRegistry(cfg.MaxDiscoveredPeers),
		delivered:       make(chan DeliveredMessage, 256),
		autoDelivered:   make(chan AutoDeliveredMessage, 256),
		capabilityCache: make(map[string]*CapabilityMessage),
		pending:         make(map[string]chan *CapabilityMessage),
		originEphemeral: make(map[CircuitID][]byte),
		pools:           make(map[PoolID]*circuitPool),
		autoPool:        make(map[CircuitID]time.Time),
		stop:            make(chan struct{}),
	}
	g.scheduler = newJitterScheduler(func(data []byte, addr net.Addr) error {
		_, err := c.WriteGarlic(data, addr)
		return err
	}, cfg.JitterQueueSize, jitterWorkers)
	c.SetGarlicHandler(g.handleIncoming)
	go g.cleanupLoop()
	return g
}
```

(Task 8/10/11 add more `go g....()` lines here — leave a placeholder comment `// Task 8/10/11 add bootstrap/auto-pool loop launches here` only if your workflow processes tasks out of order; otherwise add them directly when those tasks run.)

Update `dispatchAction` (around line 380):

```go
func (g *Garlic) dispatchAction(action circuitAction, from ed25519.PublicKey) {
	switch action.kind {
	case actionDeliver:
		if action.tagged {
			g.deliverTagged(action.circuitID, action.payload)
			return
		}
		select {
		case g.delivered <- DeliveredMessage{CircuitID: action.circuitID, Payload: action.payload}:
		default:
		}
	case actionForward:
		g.relayState.recordForward(action.circuitID, from, action.forwardTo, len(action.forwardMsg))
		g.sendCircuitData(action.forwardMsg, iwt.Addr(action.forwardTo))
	}
}

// deliverTagged interprets a msgTypeCircuitDataV3 delivery's leading kind
// byte: a cover packet (autoPayloadKindCover) is silently discarded here
// - the whole point of continuous cover traffic is that it travels the
// full circuit depth and looks exactly like real traffic to every hop,
// including this delivery step, right up until this one deliberate
// discard. A malformed payload (empty, or an unrecognized kind byte) is
// dropped the same way any other malformed Garlic input is - no error,
// no observable difference from a legitimate cover discard.
func (g *Garlic) deliverTagged(id CircuitID, payload []byte) {
	if len(payload) == 0 {
		return
	}
	kind, real := payload[0], payload[1:]
	if kind != autoPayloadKindReal {
		return
	}
	select {
	case g.autoDelivered <- AutoDeliveredMessage{CircuitID: id, Payload: append([]byte(nil), real...)}:
	default:
	}
}
```

Add near `SendGarlic`/`RecvGarlic` (around line 654):

```go
// sendAutoPayload seals a kind-tagged payload (see autoPayloadKindReal/
// autoPayloadKindCover) over circuit id and sends it as
// msgTypeCircuitDataV3 - the shared plumbing behind both SendGarlicAuto
// and the cover-traffic scheduler (Task 11). Mirrors SendGarlic's shape
// exactly except for the tag byte and the V3 outer type.
func (g *Garlic) sendAutoPayload(id CircuitID, kind byte, payload []byte) error {
	c, ok := g.circuits.Get(id)
	if !ok {
		return ErrCircuitNotFound
	}
	g.mu.Lock()
	ephemeralPub := g.originEphemeral[id]
	g.mu.Unlock()
	if ephemeralPub == nil {
		return ErrCircuitNotFound
	}

	tagged := make([]byte, 0, 1+len(payload))
	tagged = append(tagged, kind)
	tagged = append(tagged, payload...)

	onion, firstHop, counter, err := c.Seal(tagged)
	if err != nil {
		return err
	}
	expiration := uint64(time.Now().Add(g.cfg.PacketTTL).Unix())
	body, err := buildCircuitDataBody(ephemeralPub, id, counter, expiration, onion, g.cfg)
	if err != nil {
		return err
	}

	g.sendCircuitData(append([]byte{msgTypeCircuitDataV3}, body...), iwt.Addr(firstHop))
	return nil
}

// SendGarlicAuto sends a real application payload over an auto-pool
// circuit (previously created with AutoCreateCircuit). Delivered on the
// remote end via RecvGarlicAuto/g.autoDelivered - never the plain
// SendGarlic/RecvGarlic path, even if the same circuit ID were somehow
// reused (it can't be - auto-pool and manual circuits are never the
// same CircuitManager entry shared between the two APIs).
func (g *Garlic) SendGarlicAuto(id CircuitID, payload []byte) error {
	return g.sendAutoPayload(id, autoPayloadKindReal, payload)
}

// RecvGarlicAuto waits up to timeout for the next real (non-cover)
// payload delivered to this node as an auto-pool circuit's final hop.
func (g *Garlic) RecvGarlicAuto(timeout time.Duration) (*AutoDeliveredMessage, error) {
	select {
	case m := <-g.autoDelivered:
		return &m, nil
	case <-time.After(timeout):
		return nil, ErrRecvTimeout
	}
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd src/garlic && go build ./... && go test ./... -v`
Expected: PASS, full package.

- [ ] **Step 5: Commit**

```bash
git add src/garlic/manager.go src/garlic/integration_test.go
git commit -m "garlic: add tagged auto-pool delivery channel and send helper"
```

---

### Task 8: Bootstrap peers

**Files:**
- Modify: `src/garlic/manager.go` (`Config`, `New`, new `bootstrap` method)
- Test: `src/garlic/integration_test.go`

**Interfaces:**
- Consumes: `Garlic.QueryCapability`, `Garlic.RequestGossip` (Task 5).
- Produces: `Config.BootstrapPeers []string`.

**Note on test placement:** `bootstrap` is unexported and runs
automatically from `New` — this package's existing convention (see
`TestIntegrationGossipDiscoversUnknownPeer`) is to test this kind of
background-triggered behavior through its *observable effect* via the
exported API (`KnownPeers()`), not by calling the private method
directly. This test does the same: construct `Garlic` via `New` with
`Config.BootstrapPeers` already set, then poll `KnownPeers()`.

- [ ] **Step 1: Write the failing test**

Add to `src/garlic/integration_test.go`:

```go
func TestIntegrationBootstrapPeersRecordedAsSelfVerified(t *testing.T) {
	nodeA := newLinkedTestNode(t)
	nodeB := newLinkedTestNode(t)
	all := []*core.Core{nodeA, nodeB}
	for _, n := range all {
		defer n.Stop()
	}
	connectChain(t, all)
	pumpAll(all)

	idA, err := garlic.NewIdentity()
	if err != nil {
		t.Fatalf("NewIdentity (A) returned error: %v", err)
	}
	idB, err := garlic.NewIdentity()
	if err != nil {
		t.Fatalf("NewIdentity (B) returned error: %v", err)
	}

	// B must exist and be Garlic-capable before A starts, since A's
	// bootstrap step (launched from New, best-effort) queries it once.
	cfgB := garlic.DefaultConfig()
	cfgB.CapabilityTimeout = 2 * time.Second
	gB := garlic.New(nodeB, idB, cfgB, garlic.NewStaticRendezvous())
	defer gB.Close()

	cfgA := garlic.DefaultConfig()
	cfgA.CapabilityTimeout = 2 * time.Second
	cfgA.BootstrapPeers = []string{hex.EncodeToString(nodeB.PublicKey())}
	gA := garlic.New(nodeA, idA, cfgA, garlic.NewStaticRendezvous())
	defer gA.Close()

	deadline := time.Now().Add(10 * time.Second)
	for {
		found := false
		for _, p := range gA.KnownPeers() {
			if bytes.Equal(p.NodeKey, nodeB.PublicKey()) && p.SelfVerified {
				found = true
			}
		}
		if found {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("A never recorded its configured BootstrapPeers entry as self-verified; known peers: %+v", gA.KnownPeers())
		}
		time.Sleep(50 * time.Millisecond)
	}
}
```

Add `"encoding/hex"` to this file's imports if not already present.

- [ ] **Step 2: Run test to verify it fails**

Run: `cd src/garlic && go test ./... -run TestIntegrationBootstrapPeersRecordedAsSelfVerified -v`
Expected: compile failure (`Config.BootstrapPeers` undefined).

- [ ] **Step 3: Implement**

In `src/garlic/manager.go`, add to `Config` (around line 87, after the discovery fields):

```go
	// BootstrapPeers seeds the discovery registry at startup: this node
	// queries each entry (hex-encoded node key) for its Garlic
	// capability and, on success, immediately requests its known-peer
	// gossip sample (RequestGossip) - the one manual step needed before
	// AutoCreateCircuit has anything to work with, analogous to
	// Yggdrasil's own NodeConfig.Peers. Best-effort: an unreachable
	// bootstrap peer is simply skipped, not retried on a tight loop.
	BootstrapPeers []string
```

Add a method near `gossipTick` (around line 275):

```go
// bootstrap resolves Config.BootstrapPeers into self-verified discovery
// entries: QueryCapability (records the entry as SelfVerified via
// handleCapabilityResponse) followed by RequestGossip, per peer,
// best-effort. Called once from New in its own goroutine so New itself
// returns immediately, matching this package's existing convention.
func (g *Garlic) bootstrap() {
	for _, hexKey := range g.cfg.BootstrapPeers {
		key, err := hex.DecodeString(hexKey)
		if err != nil {
			continue
		}
		if _, err := g.QueryCapability(key); err != nil {
			continue
		}
		_ = g.RequestGossip(key)
	}
}
```

Update `New` (the line `go g.cleanupLoop()`):

```go
	c.SetGarlicHandler(g.handleIncoming)
	go g.cleanupLoop()
	go g.bootstrap()
	return g
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd src/garlic && go test ./... -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add src/garlic/manager.go src/garlic/integration_test.go
git commit -m "garlic: add Config.BootstrapPeers, resolved at startup"
```

---

### Task 9: AutoCreateCircuit

**Files:**
- Modify: `src/garlic/manager.go`
- Test: `src/garlic/integration_test.go`

**Interfaces:**
- Consumes: `SelectPathWithGuardPolicy` (Task 3), `CapabilityMessage.SupportsAutoCircuit` (Task 4), `candidatePool` (Task 2), `CreateCircuit` (existing).
- Produces: `Garlic.AutoCreateCircuit(n int) (CircuitID, error)`; `ErrHopMissingAutoCircuitSupport`.

**Scope note:** this task covers two of the three behaviors
`AutoCreateCircuit` needs — the self-verified-guard success path, and the
`ErrNoSelfVerifiedCandidates` failure path — as real integration tests.
The third property (rejecting a hop that answers capability but doesn't
advertise `CapabilityAutoCircuit`) is **not** given an automated test
here: constructing a genuinely non-conforming peer would require a
hand-rolled fake responder built directly on `core.Core.SetGarlicHandler`
using this package's unexported wire message-type constants
(`msgTypeCapabilityRequest`/`msgTypeCapabilityResponse`), which aren't
reachable from `integration_test.go`'s external `garlic_test` package,
and every node built via the real `garlic.New` in this codebase now
always advertises `CapabilityAutoCircuit` (Task 4) — there is no way to
get a real, otherwise-conforming "legacy" peer without bypassing `New`
entirely. `SupportsAutoCircuit()`'s own logic is already unit-tested
(Task 4); the loop in `AutoCreateCircuit` that calls it and returns
`ErrHopMissingAutoCircuitSupport` is straightforward enough to verify by
code review at PR/task-review time. Exercise real interop with an
actually-older build manually, per this plan's post-plan checklist.

- [ ] **Step 1: Write the failing tests**

Add to `src/garlic/integration_test.go`:

```go
func TestIntegrationAutoCreateCircuitUsesSelfVerifiedGuard(t *testing.T) {
	nodeA := newLinkedTestNode(t)
	nodeB := newLinkedTestNode(t)
	all := []*core.Core{nodeA, nodeB}
	for _, n := range all {
		defer n.Stop()
	}
	connectChain(t, all)
	pumpAll(all)

	idA, err := garlic.NewIdentity()
	if err != nil {
		t.Fatalf("NewIdentity (A) returned error: %v", err)
	}
	idB, err := garlic.NewIdentity()
	if err != nil {
		t.Fatalf("NewIdentity (B) returned error: %v", err)
	}

	cfg := garlic.DefaultConfig()
	cfg.CapabilityTimeout = 2 * time.Second
	cfg.MinHopCount = 0 // this tiny topology has no room for a real distance filter

	gA := garlic.New(nodeA, idA, cfg, garlic.NewStaticRendezvous())
	defer gA.Close()
	gB := garlic.New(nodeB, idB, cfg, garlic.NewStaticRendezvous())
	defer gB.Close()

	waitForCapability(t, gA, nodeB.PublicKey(), 60*time.Second)

	id, err := gA.AutoCreateCircuit(1)
	if err != nil {
		t.Fatalf("AutoCreateCircuit returned error: %v", err)
	}

	var found *garlic.Circuit
	for _, c := range gA.OriginatedCircuits() {
		if c.ID == id {
			found = c
		}
	}
	if found == nil {
		t.Fatal("AutoCreateCircuit's returned ID is not in OriginatedCircuits()")
	}
	hops := found.HopKeys()
	if len(hops) != 1 || !bytes.Equal(hops[0], nodeB.PublicKey()) {
		t.Fatalf("hops = %x, want [%x] (B, the only self-verified candidate)", hops, nodeB.PublicKey())
	}
}

func TestIntegrationAutoCreateCircuitFailsWithoutSelfVerifiedCandidate(t *testing.T) {
	nodeA := newLinkedTestNode(t) // deliberately unpeered - candidatePool() will be empty
	defer nodeA.Stop()

	idA, err := garlic.NewIdentity()
	if err != nil {
		t.Fatalf("NewIdentity (A) returned error: %v", err)
	}
	gA := garlic.New(nodeA, idA, garlic.DefaultConfig(), garlic.NewStaticRendezvous())
	defer gA.Close()

	if _, err := gA.AutoCreateCircuit(1); !errors.Is(err, garlic.ErrNoSelfVerifiedCandidates) {
		t.Fatalf("err = %v, want ErrNoSelfVerifiedCandidates", err)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd src/garlic && go test ./... -run TestIntegrationAutoCreateCircuit -v`
Expected: compile failure (`AutoCreateCircuit`/`ErrNoSelfVerifiedCandidates` undefined).

- [ ] **Step 3: Implement**

In `src/garlic/manager.go`, add to the error `var` block (around line 132):

```go
var (
	ErrInvalidPath                  = errors.New("garlic: invalid circuit path")
	ErrCircuitNotFound              = errors.New("garlic: circuit not found")
	ErrCapabilityTimeout            = errors.New("garlic: capability request timed out")
	ErrRecvTimeout                  = errors.New("garlic: no message received before timeout")
	ErrPoolNotFound                 = errors.New("garlic: circuit pool not found")
	ErrEmptyPool                    = errors.New("garlic: circuit pool must have at least one path")
	ErrHopMissingAutoCircuitSupport = errors.New("garlic: candidate hop does not support CapabilityAutoCircuit")
)
```

Add near `SelectPath` (around line 341):

```go
// AutoCreateCircuit builds an n-hop circuit entirely from this node's
// discovery pool: SelectPathWithGuardPolicy chooses hops (first from
// self-verified candidates only), each is freshly re-verified via
// QueryCapability (catching a stale/now-unresponsive gossiped candidate
// before it's used, same as the manual createGarlicCircuit admin RPC
// already does), and every hop must additionally advertise
// CapabilityAutoCircuit - see
// docs/superpowers/specs/2026-08-23-garlic-autonomous-routing-design.md
// §6/§8 for why every position, not just the terminal one, is gated.
func (g *Garlic) AutoCreateCircuit(n int) (CircuitID, error) {
	hops, err := SelectPathWithGuardPolicy(g.candidatePool(), n, g.cfg.MinHopCount)
	if err != nil {
		return CircuitID{}, err
	}

	path := make([]CapabilityMessage, len(hops))
	nodeKeys := make([][]byte, len(hops))
	for i, h := range hops {
		capability, err := g.QueryCapability(h.NodeKey)
		if err != nil {
			return CircuitID{}, fmt.Errorf("hop %d: %w", i, err)
		}
		if !capability.SupportsAutoCircuit() {
			return CircuitID{}, fmt.Errorf("hop %d: %w", i, ErrHopMissingAutoCircuitSupport)
		}
		path[i] = *capability
		nodeKeys[i] = h.NodeKey
	}
	return g.CreateCircuit(path, nodeKeys)
}
```

Add `"fmt"` to `src/garlic/manager.go`'s imports:

```go
import (
	"bytes"
	"crypto/ed25519"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"sync"
	"time"

	iwt "github.com/Arceliar/ironwood/types"

	"github.com/yggdrasil-network/yggdrasil-go/src/core"
)
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd src/garlic && go build ./... && go test ./... -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add src/garlic/manager.go src/garlic/integration_test.go
git commit -m "garlic: add AutoCreateCircuit"
```

---

### Task 10: Auto-pool loop (fill + rotate)

**Files:**
- Modify: `src/garlic/manager.go`
- Test: `src/garlic/integration_test.go`

**Interfaces:**
- Consumes: `AutoCreateCircuit` (Task 9).
- Produces: `Config.AutoPoolEnabled bool`, `Config.AutoPoolSize int`, `Config.AutoRotationInterval time.Duration`; `Garlic.AutoPoolStatus() []AutoPoolEntry`; `AutoPoolEntry`.

**Note on test placement and approach:** `fillAutoPool`/`rotateAutoPool`
are unexported and, like `bootstrap` (Task 8), are exercised here through
their externally observable effect (`AutoPoolStatus()`, exported) on a
`Garlic` built via the real `New(...)` with `Config.AutoPoolEnabled: true`
— not called directly. This is deliberate, not a workaround: it tests the
real `autoPoolLoop` wiring end to end (Task 11 wires the loop into `New`;
if executing tasks in strict order, these two tests won't pass until
Task 11's wiring lands — call this out to the reviewer the same way
Task 5/6's ordering dependency was, or do Tasks 10 and 11 as one combined
commit).

- [ ] **Step 1: Write the failing tests**

Add to `src/garlic/integration_test.go`:

```go
func TestIntegrationAutoPoolFillsToTargetSize(t *testing.T) {
	nodeA := newLinkedTestNode(t)
	nodeB := newLinkedTestNode(t)
	all := []*core.Core{nodeA, nodeB}
	for _, n := range all {
		defer n.Stop()
	}
	connectChain(t, all)
	pumpAll(all)

	idA, err := garlic.NewIdentity()
	if err != nil {
		t.Fatalf("NewIdentity (A) returned error: %v", err)
	}
	idB, err := garlic.NewIdentity()
	if err != nil {
		t.Fatalf("NewIdentity (B) returned error: %v", err)
	}

	cfgB := garlic.DefaultConfig()
	cfgB.CapabilityTimeout = 2 * time.Second
	gB := garlic.New(nodeB, idB, cfgB, garlic.NewStaticRendezvous())
	defer gB.Close()

	cfgA := garlic.DefaultConfig()
	cfgA.CapabilityTimeout = 2 * time.Second
	cfgA.MinHopCount = 0
	cfgA.PathLength = 1
	cfgA.BootstrapPeers = []string{hex.EncodeToString(nodeB.PublicKey())}
	cfgA.AutoPoolEnabled = true
	cfgA.AutoPoolSize = 1
	cfgA.CoverTrafficEnabled = false // isolate fill/rotate behavior from cover-traffic noise
	gA := garlic.New(nodeA, idA, cfgA, garlic.NewStaticRendezvous())
	defer gA.Close()

	deadline := time.Now().Add(15 * time.Second)
	for {
		if len(gA.AutoPoolStatus()) == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("auto-pool never reached target size 1; status: %+v", gA.AutoPoolStatus())
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func TestIntegrationAutoPoolRotatesOneCircuitAtATime(t *testing.T) {
	nodeA := newLinkedTestNode(t)
	nodeB := newLinkedTestNode(t)
	all := []*core.Core{nodeA, nodeB}
	for _, n := range all {
		defer n.Stop()
	}
	connectChain(t, all)
	pumpAll(all)

	idA, err := garlic.NewIdentity()
	if err != nil {
		t.Fatalf("NewIdentity (A) returned error: %v", err)
	}
	idB, err := garlic.NewIdentity()
	if err != nil {
		t.Fatalf("NewIdentity (B) returned error: %v", err)
	}

	cfgB := garlic.DefaultConfig()
	cfgB.CapabilityTimeout = 2 * time.Second
	gB := garlic.New(nodeB, idB, cfgB, garlic.NewStaticRendezvous())
	defer gB.Close()

	cfgA := garlic.DefaultConfig()
	cfgA.CapabilityTimeout = 2 * time.Second
	cfgA.MinHopCount = 0
	cfgA.PathLength = 1
	cfgA.BootstrapPeers = []string{hex.EncodeToString(nodeB.PublicKey())}
	cfgA.AutoPoolEnabled = true
	cfgA.AutoPoolSize = 2 // two circuits, both through the only candidate B, so rotation has something to distinguish
	cfgA.AutoRotationInterval = 1100 * time.Millisecond
	cfgA.CoverTrafficEnabled = false
	gA := garlic.New(nodeA, idA, cfgA, garlic.NewStaticRendezvous())
	defer gA.Close()

	var before []garlic.AutoPoolEntry
	deadline := time.Now().Add(15 * time.Second)
	for {
		before = gA.AutoPoolStatus()
		if len(before) == 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("auto-pool never reached target size 2; status: %+v", before)
		}
		time.Sleep(100 * time.Millisecond)
	}

	time.Sleep(1500 * time.Millisecond) // past one rotation tick, comfortably short of a second one

	after := gA.AutoPoolStatus()
	if len(after) != 2 {
		t.Fatalf("AutoPoolStatus() after rotation = %d entries, want 2 (pool stays at target size)", len(after))
	}
	changed := 0
	for _, a := range after {
		stillPresent := false
		for _, b := range before {
			if a.ID == b.ID {
				stillPresent = true
			}
		}
		if !stillPresent {
			changed++
		}
	}
	if changed != 1 {
		t.Fatalf("%d circuits changed after ~1 rotation interval, want exactly 1 (before=%+v after=%+v)", changed, before, after)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd src/garlic && go test ./... -run TestIntegrationAutoPool -v`
Expected: compile failure (`Config.AutoPoolEnabled`/`AutoPoolStatus` undefined).

- [ ] **Step 3: Implement**

In `src/garlic/manager.go`'s `Config` struct, add:

```go
	// AutoPoolEnabled turns on the background circuit pool + rotation +
	// (if CoverTrafficEnabled) cover traffic. A node can still relay/
	// terminate for another node's auto-pool circuits with this off -
	// see CapabilityAutoCircuit's doc comment.
	AutoPoolEnabled bool
	// AutoPoolSize is how many circuits the pool maintains.
	AutoPoolSize int
	// AutoRotationInterval is how often one pool circuit (the oldest) is
	// retired and rebuilt - never the whole pool at once.
	AutoRotationInterval time.Duration
```

Add matching defaults to `DefaultConfig()`:

```go
		AutoPoolEnabled:       false,
		AutoPoolSize:          3,
		AutoRotationInterval:  15 * time.Minute,
```

Add near `AutoCreateCircuit`:

```go
// AutoPoolEntry is a point-in-time summary of one auto-pool circuit, for
// the getGarlicAutoPool admin RPC / dashboard.
type AutoPoolEntry struct {
	ID        CircuitID
	CreatedAt time.Time
	HopCount  int
}

// AutoPoolStatus returns every circuit currently managed by the auto-pool
// loop, sorted by ascending circuit ID for stable admin/dashboard output
// (same reasoning as CircuitManager.List's doc comment).
func (g *Garlic) AutoPoolStatus() []AutoPoolEntry {
	g.mu.Lock()
	entries := make([]AutoPoolEntry, 0, len(g.autoPool))
	for id, at := range g.autoPool {
		entries = append(entries, AutoPoolEntry{ID: id, CreatedAt: at})
	}
	g.mu.Unlock()

	for i := range entries {
		if c, ok := g.circuits.Get(entries[i].ID); ok {
			entries[i].HopCount = len(c.HopKeys())
		}
	}
	slices.SortFunc(entries, func(a, b AutoPoolEntry) int { return bytes.Compare(a.ID[:], b.ID[:]) })
	return entries
}

// fillAutoPool tops the auto-pool up to Config.AutoPoolSize, best-effort:
// a candidate shortage (ErrNoSelfVerifiedCandidates,
// ErrInsufficientDiverseCandidates, or any AutoCreateCircuit failure)
// just leaves the pool under target until more peers are discovered - no
// tight retry loop.
func (g *Garlic) fillAutoPool() {
	g.mu.Lock()
	n := len(g.autoPool)
	g.mu.Unlock()
	for ; n < g.cfg.AutoPoolSize; n++ {
		id, err := g.AutoCreateCircuit(g.cfg.PathLength)
		if err != nil {
			return
		}
		g.mu.Lock()
		g.autoPool[id] = time.Now()
		g.mu.Unlock()
	}
}

// rotateAutoPool retires exactly one pool circuit (the oldest) per call
// and immediately tries to rebuild the pool back to target size - never
// the whole pool at once, so a rotation tick isn't itself a
// burst-of-circuit-builds fingerprint (see the design doc §7).
func (g *Garlic) rotateAutoPool() {
	g.mu.Lock()
	var oldestID CircuitID
	var oldestAt time.Time
	first := true
	for id, at := range g.autoPool {
		if first || at.Before(oldestAt) {
			oldestID, oldestAt, first = id, at, false
		}
	}
	g.mu.Unlock()

	if first {
		g.fillAutoPool()
		return
	}

	g.CloseCircuit(oldestID)
	g.mu.Lock()
	delete(g.autoPool, oldestID)
	g.mu.Unlock()
	g.fillAutoPool()
}
```

Add `"slices"` to `src/garlic/manager.go`'s imports (alongside `"fmt"` from Task 9).

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd src/garlic && go build ./... && go test ./... -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add src/garlic/manager.go src/garlic/integration_test.go
git commit -m "garlic: add auto-pool fill/rotate (no background loop wiring yet)"
```

---

### Task 11: Cover traffic + background loop wiring

**Files:**
- Modify: `src/garlic/manager.go`
- Test: `src/garlic/integration_test.go`

**Interfaces:**
- Consumes: `fillAutoPool`/`rotateAutoPool` (Task 10), `sendAutoPayload` (Task 7).
- Produces: `Config.CoverTrafficEnabled bool`, `Config.CoverTrafficInterval time.Duration`; `autoPoolLoop` wired into `New`.

**Note on test approach:** `sendCoverTraffic` is unexported; this test
proves its externally-visible contract instead — with
`Config.CoverTrafficEnabled: true` and a short interval, real cover
packets are actively flowing over the auto-pool circuit's full path
(both nodes are up, the circuit is real), yet nothing ever reaches
`RecvGarlicAuto`, across several cover-traffic intervals' worth of
waiting.

- [ ] **Step 1: Write the failing test**

Add to `src/garlic/integration_test.go`:

```go
func TestIntegrationCoverTrafficNeverReachesRecvGarlicAuto(t *testing.T) {
	nodeA := newLinkedTestNode(t)
	nodeB := newLinkedTestNode(t)
	all := []*core.Core{nodeA, nodeB}
	for _, n := range all {
		defer n.Stop()
	}
	connectChain(t, all)
	pumpAll(all)

	idA, err := garlic.NewIdentity()
	if err != nil {
		t.Fatalf("NewIdentity (A) returned error: %v", err)
	}
	idB, err := garlic.NewIdentity()
	if err != nil {
		t.Fatalf("NewIdentity (B) returned error: %v", err)
	}

	cfgB := garlic.DefaultConfig()
	cfgB.CapabilityTimeout = 2 * time.Second
	gB := garlic.New(nodeB, idB, cfgB, garlic.NewStaticRendezvous())
	defer gB.Close()

	cfgA := garlic.DefaultConfig()
	cfgA.CapabilityTimeout = 2 * time.Second
	cfgA.MinHopCount = 0
	cfgA.PathLength = 1
	cfgA.BootstrapPeers = []string{hex.EncodeToString(nodeB.PublicKey())}
	cfgA.AutoPoolEnabled = true
	cfgA.AutoPoolSize = 1
	cfgA.CoverTrafficEnabled = true
	cfgA.CoverTrafficInterval = 300 * time.Millisecond // fast, for test purposes
	gA := garlic.New(nodeA, idA, cfgA, garlic.NewStaticRendezvous())
	defer gA.Close()

	deadline := time.Now().Add(15 * time.Second)
	for len(gA.AutoPoolStatus()) != 1 {
		if time.Now().After(deadline) {
			t.Fatalf("auto-pool never reached target size 1; status: %+v", gA.AutoPoolStatus())
		}
		time.Sleep(100 * time.Millisecond)
	}

	// Cover traffic has had several intervals to fire (real packets, real
	// circuit, both nodes up) - none of it must ever surface as a real
	// delivery on B's auto channel.
	if _, err := gB.RecvGarlicAuto(2 * time.Second); !errors.Is(err, garlic.ErrRecvTimeout) {
		t.Fatalf("RecvGarlicAuto err = %v, want ErrRecvTimeout (cover packets must never surface as a real delivery)", err)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd src/garlic && go test ./... -run TestIntegrationCoverTrafficNeverReachesRecvGarlicAuto -v`
Expected: compile failure (`Config.CoverTrafficEnabled` undefined).

- [ ] **Step 3: Implement**

In `src/garlic/manager.go`'s `Config` struct, add:

```go
	// CoverTrafficEnabled sends a periodic dummy payload over every
	// auto-pool circuit, even when there's nothing real to send - raises
	// the cost of volume-based traffic correlation for auto-pool
	// circuits specifically (docs/garlic-threat-model.md's "Traffic
	// correlation" section already covers the general limits of this
	// class of defense).
	CoverTrafficEnabled bool
	// CoverTrafficInterval is the average spacing between cover packets
	// per circuit, randomized ±50% per send so it isn't perfectly
	// periodic (a fixed interval is itself a fingerprint).
	CoverTrafficInterval time.Duration
```

Add matching defaults to `DefaultConfig()`:

```go
		CoverTrafficEnabled:   true,
		CoverTrafficInterval:  75 * time.Second,
```

Add near `fillAutoPool`:

```go
// sendCoverTraffic sends one autoPayloadKindCover packet over every
// circuit currently in the auto-pool. Best-effort - a send failure
// (e.g. a hop temporarily unreachable) is not retried here; the next
// scheduled tick tries again.
func (g *Garlic) sendCoverTraffic() {
	g.mu.Lock()
	ids := make([]CircuitID, 0, len(g.autoPool))
	for id := range g.autoPool {
		ids = append(ids, id)
	}
	g.mu.Unlock()

	for _, id := range ids {
		_ = g.sendAutoPayload(id, autoPayloadKindCover, make([]byte, coverPayloadSize))
	}
}

// coverTrafficDelay returns Config.CoverTrafficInterval jittered ±50%,
// so per-circuit cover-packet timing isn't a fixed, fingerprintable
// period.
func (g *Garlic) coverTrafficDelay() time.Duration {
	base := g.cfg.CoverTrafficInterval
	if base <= 0 {
		return time.Second
	}
	jitterRange := int64(base) // ±50% of base = a uniform draw over [0.5*base, 1.5*base]
	offset := mrand.Int63n(jitterRange) - jitterRange/2
	d := time.Duration(int64(base) + offset)
	if d < time.Second {
		d = time.Second
	}
	return d
}

// autoPoolLoop maintains the auto-pool (fill on start, rotate one
// circuit at a time on Config.AutoRotationInterval) and, if
// Config.CoverTrafficEnabled, sends jittered cover traffic over every
// pool circuit. No-op entirely if Config.AutoPoolEnabled is false - a
// node can still relay/terminate for other nodes' auto-pool circuits
// without running this loop itself.
func (g *Garlic) autoPoolLoop() {
	if !g.cfg.AutoPoolEnabled {
		return
	}
	g.fillAutoPool()

	rotate := time.NewTicker(max(g.cfg.AutoRotationInterval, time.Second))
	defer rotate.Stop()

	for {
		var coverTimer *time.Timer
		var coverC <-chan time.Time
		if g.cfg.CoverTrafficEnabled {
			coverTimer = time.NewTimer(g.coverTrafficDelay())
			coverC = coverTimer.C
		}

		select {
		case <-rotate.C:
			g.rotateAutoPool()
		case <-coverC:
			g.sendCoverTraffic()
		case <-g.stop:
			if coverTimer != nil {
				coverTimer.Stop()
			}
			return
		}
		if coverTimer != nil {
			coverTimer.Stop()
		}
	}
}
```

Add `mrand "math/rand"` to `src/garlic/manager.go`'s imports (this package uses `crypto/rand`-backed primitives elsewhere for anything security-relevant — cover-traffic *scheduling jitter* is explicitly not one of those, same class of non-cryptographic randomness already used by `discoveryRegistry.sample`'s doc comment; keep the alias so it's never confused with a crypto-relevant `rand` import elsewhere in this file).

Update `New` (from Task 8's `go g.bootstrap()` line):

```go
	c.SetGarlicHandler(g.handleIncoming)
	go g.cleanupLoop()
	go g.bootstrap()
	go g.autoPoolLoop()
	return g
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd src/garlic && go build ./... && go test ./... -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add src/garlic/manager.go src/garlic/integration_test.go
git commit -m "garlic: wire cover traffic + auto-pool background loop"
```

---

### Task 12: Admin RPC surface

**Files:**
- Modify: `src/garlic/admin.go`
- Test: `src/garlic/admin_test.go`

**Interfaces:**
- Consumes: `AutoCreateCircuit` (9), `AutoPoolStatus` (10), `RecvGarlicAuto` (7), `RequestGossip` (5), `DiscoveredPeer.SelfVerified` (1).
- Produces: admin RPCs `createGarlicCircuitAuto`, `getGarlicAutoPool`, `recvGarlicAuto`, `garlicGossipPull`; `getGarlicKnownPeers` response gains `selfVerified`.

**Files (expanded):**
- Modify: `src/garlic/admin.go`
- Test: `src/garlic/admin_test.go`

`admin_test.go` is `package garlic_test` — the same external test package
as `integration_test.go` in the same directory, so `newLinkedTestNode`,
`connectChain`, `pumpAll`, and `waitForCapability` (all defined in
`integration_test.go`) are directly usable here too, alongside this
file's own existing `newTestGarlicWithCore`, `newTestAdminSocket`, and
`callAdmin(t, sockPath, request string) map[string]interface{}` (which
**always sends an empty arguments object** — fine for
`createGarlicCircuitAuto`/`getGarlicAutoPool`/`getGarlicKnownPeers` below,
since none of the arguments this task needs from them are required, but
not enough for `garlicGossipPull`'s required `key` argument — Step 3 adds
a small sibling helper for that rather than changing `callAdmin`'s
signature and risking every existing caller of it).

- [ ] **Step 1: Write the failing tests**

Add to `src/garlic/admin_test.go`:

```go
func TestCreateGarlicCircuitAutoHandlerDefaultsHopCountToPathLength(t *testing.T) {
	cA := newLinkedTestNode(t)
	defer cA.Stop()
	cB := newLinkedTestNode(t)
	defer cB.Stop()
	connectChain(t, []*core.Core{cA, cB})
	pumpAll([]*core.Core{cA, cB})

	idA, err := garlic.NewIdentity()
	if err != nil {
		t.Fatalf("NewIdentity (A) returned error: %v", err)
	}
	idB, err := garlic.NewIdentity()
	if err != nil {
		t.Fatalf("NewIdentity (B) returned error: %v", err)
	}
	cfg := garlic.DefaultConfig()
	cfg.CapabilityTimeout = 2 * time.Second
	cfg.MinHopCount = 0
	cfg.PathLength = 1

	gB := garlic.New(cB, idB, cfg, garlic.NewStaticRendezvous())
	defer gB.Close()
	gA := garlic.New(cA, idA, cfg, garlic.NewStaticRendezvous())
	defer gA.Close()

	waitForCapability(t, gA, cB.PublicKey(), 60*time.Second)

	sockPath := newTestAdminSocket(t, cA, gA)
	resp := callAdmin(t, sockPath, "createGarlicCircuitAuto")
	if id, _ := resp["circuitId"].(string); id == "" {
		t.Fatalf("createGarlicCircuitAuto response = %+v, want a non-empty circuitId", resp)
	}
}

func TestGetGarlicAutoPoolHandlerListsPool(t *testing.T) {
	cA := newLinkedTestNode(t)
	defer cA.Stop()
	cB := newLinkedTestNode(t)
	defer cB.Stop()
	connectChain(t, []*core.Core{cA, cB})
	pumpAll([]*core.Core{cA, cB})

	idA, err := garlic.NewIdentity()
	if err != nil {
		t.Fatalf("NewIdentity (A) returned error: %v", err)
	}
	idB, err := garlic.NewIdentity()
	if err != nil {
		t.Fatalf("NewIdentity (B) returned error: %v", err)
	}

	cfgB := garlic.DefaultConfig()
	cfgB.CapabilityTimeout = 2 * time.Second
	gB := garlic.New(cB, idB, cfgB, garlic.NewStaticRendezvous())
	defer gB.Close()

	cfgA := garlic.DefaultConfig()
	cfgA.CapabilityTimeout = 2 * time.Second
	cfgA.MinHopCount = 0
	cfgA.PathLength = 1
	cfgA.BootstrapPeers = []string{hex.EncodeToString(cB.PublicKey())}
	cfgA.AutoPoolEnabled = true
	cfgA.AutoPoolSize = 1
	cfgA.CoverTrafficEnabled = false
	gA := garlic.New(cA, idA, cfgA, garlic.NewStaticRendezvous())
	defer gA.Close()

	deadline := time.Now().Add(15 * time.Second)
	for len(gA.AutoPoolStatus()) != 1 {
		if time.Now().After(deadline) {
			t.Fatal("auto-pool never reached target size 1")
		}
		time.Sleep(100 * time.Millisecond)
	}

	sockPath := newTestAdminSocket(t, cA, gA)
	resp := callAdmin(t, sockPath, "getGarlicAutoPool")
	pool, ok := resp["pool"].([]interface{})
	if !ok || len(pool) != 1 {
		t.Fatalf("getGarlicAutoPool response pool = %+v, want 1 entry", resp["pool"])
	}
}

func TestGetGarlicKnownPeersHandlerIncludesSelfVerified(t *testing.T) {
	cA := newLinkedTestNode(t)
	defer cA.Stop()
	cB := newLinkedTestNode(t)
	defer cB.Stop()
	connectChain(t, []*core.Core{cA, cB})
	pumpAll([]*core.Core{cA, cB})

	idA, err := garlic.NewIdentity()
	if err != nil {
		t.Fatalf("NewIdentity (A) returned error: %v", err)
	}
	idB, err := garlic.NewIdentity()
	if err != nil {
		t.Fatalf("NewIdentity (B) returned error: %v", err)
	}
	cfg := garlic.DefaultConfig()
	cfg.CapabilityTimeout = 2 * time.Second
	gB := garlic.New(cB, idB, cfg, garlic.NewStaticRendezvous())
	defer gB.Close()
	gA := garlic.New(cA, idA, cfg, garlic.NewStaticRendezvous())
	defer gA.Close()

	waitForCapability(t, gA, cB.PublicKey(), 60*time.Second)

	sockPath := newTestAdminSocket(t, cA, gA)
	resp := callAdmin(t, sockPath, "getGarlicKnownPeers")
	peers, ok := resp["peers"].([]interface{})
	if !ok || len(peers) != 1 {
		t.Fatalf("getGarlicKnownPeers response peers = %+v, want 1 entry", resp["peers"])
	}
	entry, ok := peers[0].(map[string]interface{})
	if !ok {
		t.Fatalf("peers[0] = %#v, want a JSON object", peers[0])
	}
	if sv, ok := entry["selfVerified"].(bool); !ok || !sv {
		t.Fatalf("peers[0][\"selfVerified\"] = %v, want true", entry["selfVerified"])
	}
}

func TestGarlicGossipPullHandlerTriggersRequestGossip(t *testing.T) {
	cA := newLinkedTestNode(t)
	defer cA.Stop()
	cB := newLinkedTestNode(t)
	defer cB.Stop()
	cC := newLinkedTestNode(t)
	defer cC.Stop()
	connectChain(t, []*core.Core{cA, cB, cC}) // A -- B -- C
	pumpAll([]*core.Core{cA, cB, cC})

	idA, err := garlic.NewIdentity()
	if err != nil {
		t.Fatalf("NewIdentity (A) returned error: %v", err)
	}
	idB, err := garlic.NewIdentity()
	if err != nil {
		t.Fatalf("NewIdentity (B) returned error: %v", err)
	}
	idC, err := garlic.NewIdentity()
	if err != nil {
		t.Fatalf("NewIdentity (C) returned error: %v", err)
	}
	cfg := garlic.DefaultConfig()
	cfg.CapabilityTimeout = 2 * time.Second
	gA := garlic.New(cA, idA, cfg, garlic.NewStaticRendezvous())
	defer gA.Close()
	gB := garlic.New(cB, idB, cfg, garlic.NewStaticRendezvous())
	defer gB.Close()
	gC := garlic.New(cC, idC, cfg, garlic.NewStaticRendezvous())
	defer gC.Close()

	waitForCapability(t, gA, cB.PublicKey(), 60*time.Second)
	waitForCapability(t, gB, cC.PublicKey(), 60*time.Second)

	sockPath := newTestAdminSocket(t, cA, gA)
	callAdminWithArgs(t, sockPath, "garlicGossipPull", map[string]interface{}{"key": hex.EncodeToString(cB.PublicKey())})

	deadline := time.Now().Add(10 * time.Second)
	for {
		found := false
		for _, p := range gA.KnownPeers() {
			if bytes.Equal(p.NodeKey, cC.PublicKey()) {
				found = true
			}
		}
		if found {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("A never learned about C via the garlicGossipPull admin RPC")
		}
		time.Sleep(50 * time.Millisecond)
	}
}
```

Also add this new helper to `admin_test.go`, next to the existing `callAdmin` (needed by `TestGarlicGossipPullHandlerTriggersRequestGossip` above — `callAdmin` itself always sends an empty arguments object, which every existing caller relies on, so this is an additive sibling rather than a change to `callAdmin`'s signature):

```go
// callAdminWithArgs behaves like callAdmin but sends a non-empty
// arguments object - needed for handlers that take a required argument
// (e.g. garlicGossipPull's "key").
func callAdminWithArgs(t *testing.T, sockPath, request string, args map[string]interface{}) map[string]interface{} {
	t.Helper()
	conn, err := net.Dial("unix", sockPath)
	if err != nil {
		t.Fatalf("net.Dial returned error: %v", err)
	}
	defer conn.Close()

	enc := json.NewEncoder(conn)
	if err := enc.Encode(map[string]interface{}{"request": request, "arguments": args}); err != nil {
		t.Fatalf("Encode returned error: %v", err)
	}
	var resp map[string]interface{}
	dec := json.NewDecoder(conn)
	if err := dec.Decode(&resp); err != nil {
		t.Fatalf("Decode returned error: %v", err)
	}
	if resp["status"] != "success" {
		t.Fatalf("admin request %q failed: %v", request, resp["error"])
	}
	respBody, _ := resp["response"].(map[string]interface{})
	return respBody
}
```

Add `"bytes"` and `"time"` to `admin_test.go`'s imports if not already present (check the existing import block first — some of these tests may already need `time` for `CreateCircuit`'s lifetime argument).

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd src/garlic && go test ./... -run 'TestCreateGarlicCircuitAuto|TestGetGarlicAutoPool|TestGetGarlicKnownPeersHandlerIncludesSelfVerified|TestGarlicGossipPullHandler' -v`
Expected: compile failure (handlers/`callAdminWithArgs` undefined).

- [ ] **Step 3: Implement**

In `src/garlic/admin.go`, add after the existing `createGarlicCircuit` handler (around line 79):

```go
	_ = a.AddHandler("createGarlicCircuitAuto", "Automatically build a circuit from topologically diverse, capability-verified candidates (first hop restricted to self-verified peers)", []string{"[hopCount]"},
		func(in json.RawMessage) (interface{}, error) {
			var req struct {
				HopCount string `json:"hopCount"`
			}
			if err := json.Unmarshal(in, &req); err != nil {
				return nil, err
			}
			n := g.cfg.PathLength
			if req.HopCount != "" {
				if _, err := fmt.Sscanf(req.HopCount, "%d", &n); err != nil {
					return nil, fmt.Errorf("invalid hopCount: %w", err)
				}
			}
			id, err := g.AutoCreateCircuit(n)
			if err != nil {
				return nil, err
			}
			return map[string]string{"circuitId": circuitIDToString(id)}, nil
		})
```

Add after the existing `getGarlicCircuits` handler (around line 280):

```go
	_ = a.AddHandler("getGarlicAutoPool", "List this node's auto-managed circuit pool", []string{},
		func(in json.RawMessage) (interface{}, error) {
			entries := g.AutoPoolStatus()
			out := make([]map[string]interface{}, len(entries))
			for i, e := range entries {
				out[i] = map[string]interface{}{
					"circuitId": circuitIDToString(e.ID),
					"createdAt": e.CreatedAt.UTC().Format(time.RFC3339),
					"hops":      e.HopCount,
				}
			}
			return map[string]interface{}{"pool": out}, nil
		})

	_ = a.AddHandler("recvGarlicAuto", "Wait for the next real (non-cover) payload delivered to this node as an auto-pool circuit's final hop", []string{"[timeoutSeconds]"},
		func(in json.RawMessage) (interface{}, error) {
			var req struct {
				TimeoutSeconds string `json:"timeoutSeconds"`
			}
			if err := json.Unmarshal(in, &req); err != nil {
				return nil, err
			}
			timeout, err := parseSecondsOrDefault(req.TimeoutSeconds, 5*time.Second)
			if err != nil {
				return nil, err
			}
			msg, err := g.RecvGarlicAuto(timeout)
			if err != nil {
				return nil, err
			}
			return map[string]string{
				"circuitId": circuitIDToString(msg.CircuitID),
				"payload":   hex.EncodeToString(msg.Payload),
			}, nil
		})
```

Update the existing `getGarlicKnownPeers` handler (around line 282) to add `selfVerified` and change the response slice's element type:

```go
	_ = a.AddHandler("getGarlicKnownPeers", "List Garlic peers this node knows about (direct or via gossip)", []string{},
		func(in json.RawMessage) (interface{}, error) {
			peers := g.KnownPeers()
			out := make([]map[string]interface{}, len(peers))
			for i, p := range peers {
				out[i] = map[string]interface{}{
					"nodeKey":         hex.EncodeToString(p.NodeKey),
					"garlicPublicKey": hex.EncodeToString(p.GarlicPublicKey),
					"lastSeen":        p.LastSeen.UTC().Format(time.RFC3339),
					"selfVerified":    p.SelfVerified,
				}
			}
			return map[string]interface{}{"peers": out}, nil
		})
```

Add after the existing `garlicGossip` handler (around line 312):

```go
	_ = a.AddHandler("garlicGossipPull", "Ask an already capability-verified peer to send its known-peer sample now", []string{"key"},
		func(in json.RawMessage) (interface{}, error) {
			var req struct {
				Key string `json:"key"`
			}
			if err := json.Unmarshal(in, &req); err != nil {
				return nil, err
			}
			key, err := hex.DecodeString(req.Key)
			if err != nil {
				return nil, fmt.Errorf("invalid key: %w", err)
			}
			if err := g.RequestGossip(key); err != nil {
				return nil, err
			}
			return map[string]interface{}{}, nil
		})
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd src/garlic && go build ./... && go test ./... -v`
Expected: PASS, full package.

- [ ] **Step 5: Commit**

```bash
git add src/garlic/admin.go src/garlic/admin_test.go
git commit -m "garlic: add auto-pool/gossip-pull admin RPCs, selfVerified in getGarlicKnownPeers"
```

---

### Task 13: NodeConfig.Garlic surface

**Files:**
- Modify: `src/config/config.go`

**Interfaces:**
- Consumes: nothing new (pure config plumbing).
- Produces: `GarlicConfig.BootstrapPeers`, `.AutoPoolEnabled`, `.AutoPoolSize`, `.AutoRotationInterval`, `.CoverTrafficEnabled`, `.CoverTrafficInterval`, all populated by `GenerateConfig()`.

- [ ] **Step 1: Write the failing test**

Check whether `src/config` has an existing test file (e.g. `config_test.go`) that already asserts `GenerateConfig()`'s Garlic defaults; if so add to it, otherwise skip a dedicated test for this task (pure config-struct plumbing with defaults set inline — the real correctness check is Task 14's `cmd/yggdrasil/main.go` wiring compiling and Task 9-11's own tests, which already cover the runtime `garlic.Config` behavior these fields feed into). If a test file exists, add:

```go
func TestGenerateConfigSetsGarlicAutoPoolDefaults(t *testing.T) {
	cfg := GenerateConfig()
	if cfg.Garlic.AutoPoolSize != 3 {
		t.Errorf("Garlic.AutoPoolSize = %d, want 3", cfg.Garlic.AutoPoolSize)
	}
	if !cfg.Garlic.CoverTrafficEnabled {
		t.Error("Garlic.CoverTrafficEnabled = false, want true (default-on per design decision)")
	}
}
```

- [ ] **Step 2: Run test to verify it fails (if a test file exists)**

Run: `cd src/config && go test ./... -run TestGenerateConfigSetsGarlicAutoPoolDefaults -v`
Expected: compile failure (fields undefined) or FAIL.

- [ ] **Step 3: Implement**

In `src/config/config.go`, update `GarlicConfig` (around line 65):

```go
type GarlicConfig struct {
	Enabled            bool                `comment:"Enables the experimental Garlic Routing Overlay. Default is false."`
	PrivateKey         KeyBytes            `json:",omitempty" comment:"This node's long-term Garlic identity private key. Independent of\nyour main Yggdrasil PrivateKey above - compromise of one does not\nimplicate the other. If left unset while Enabled is true, a fresh\nkey is generated at startup and your Garlic identity will not be\nstable across restarts."`
	SigningPrivateKey  KeyBytes            `json:",omitempty" comment:"This node's Garlic service-descriptor signing key (Ed25519 seed,\n32 bytes). Independent of both PrivateKey above and your main\nYggdrasil key. Used only when publishing a Garlic service - see\ndocs/garlic-protocol.md section 6. If left unset while Enabled is\ntrue, a fresh key is generated at startup."`
	PathLength         int                 `comment:"Number of hops for circuits this node originates."`
	CircuitLifetime    string              `comment:"Maximum lifetime of a circuit before it must be rebuilt (Go duration\nformat, e.g. \"10m\")."`
	MaxCircuits        int                 `comment:"Maximum number of circuits this node will originate at once."`
	MaxCircuitsPerPeer int                 `comment:"Maximum number of originated circuits through any single first-hop\npeer at once."`
	MaxRelayCircuits   int                 `comment:"Maximum number of other nodes' circuits this node will relay at once."`
	Padding            GarlicPaddingConfig `comment:"Per-hop packet size randomization: the originator and every relay\nindependently pick a new random wire size within [MinSize, MaxSize]\nfor each packet, so a hop-to-hop link's packet sizes don't match\nthose on the next link - see docs/garlic-threat-model.md's\ndiscussion of traffic correlation."`
	Jitter             GarlicJitterConfig  `comment:"Random delay before actually transmitting a circuit packet (origin\nsend or relay forward), independently re-rolled per packet - the\ntiming half of the same traffic-correlation defense as Padding."`
	MaxDiscoveredPeers int                 `comment:"Maximum number of other Garlic nodes this node will remember, learned\neither directly (a successful capability query) or via gossip from\nanother already-verified Garlic peer. Never exposed to, or\ndiscoverable by, a non-Garlic node."`
	MinHopCount        int                 `comment:"Minimum mesh hop distance for a candidate to be selected as a circuit\nhop by SelectPath - a node too close is more likely to be run by the\nsame operator or network as this one. Does not affect hops supplied\ndirectly to CreateCircuit."`
	BootstrapPeers        []string `comment:"Hex-encoded node keys of a few known Garlic-capable peers, queried at\nstartup so this node's candidate pool starts non-empty - analogous to\nthe top-level Peers setting, but for Garlic circuit-hop discovery\nrather than mesh transport. Empty by default."`
	AutoPoolEnabled       bool     `comment:"Maintains a small background pool of automatically-built circuits\n(no manual hop keys needed) for sendGarlic/recvGarlic-style use and\nthe dashboard. Default is false; a node can still relay/terminate for\nother nodes' auto-pool circuits with this off."`
	AutoPoolSize          int      `comment:"Number of circuits the auto-pool maintains."`
	AutoRotationInterval  string   `comment:"How often one auto-pool circuit (the oldest) is retired and rebuilt\n(Go duration format, e.g. \"15m\"). Never the whole pool at once."`
	CoverTrafficEnabled   bool     `comment:"Sends periodic dummy traffic over every auto-pool circuit, even when\nthere's nothing real to send - raises the cost of traffic-volume\ncorrelation. Real, ongoing bandwidth cost - see docs/garlic-threat-model.md.\nDefault is true, with a low-bandwidth default interval."`
	CoverTrafficInterval  string   `comment:"Average spacing between cover packets per auto-pool circuit (Go\nduration format), jittered +/-50%% so it isn't perfectly periodic."`
}
```

Update `GenerateConfig()`'s `cfg.Garlic = GarlicConfig{...}` block (around line 128):

```go
	cfg.Garlic = GarlicConfig{
		Enabled:            false,
		PathLength:         3,
		CircuitLifetime:    "10m",
		MaxCircuits:        1024,
		MaxCircuitsPerPeer: 64,
		MaxRelayCircuits:   4096,
		Padding: GarlicPaddingConfig{
			Enabled: true,
			MinSize: 512,
			MaxSize: 1400,
		},
		Jitter: GarlicJitterConfig{
			Enabled:  true,
			MinDelay: "0s",
			MaxDelay: "75ms",
		},
		MaxDiscoveredPeers:   1024,
		MinHopCount:          2,
		BootstrapPeers:       []string{},
		AutoPoolEnabled:      false,
		AutoPoolSize:         3,
		AutoRotationInterval: "15m",
		CoverTrafficEnabled:  true,
		CoverTrafficInterval: "75s",
	}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd src/config && go build ./... && go test ./... -v`
Expected: PASS (or a clean build if no test file exists for this package's Garlic defaults).

- [ ] **Step 5: Commit**

```bash
git add src/config/config.go
git commit -m "config: add Garlic auto-discovery/auto-pool/cover-traffic settings"
```

---

### Task 14: cmd/yggdrasil wiring

**Files:**
- Modify: `cmd/yggdrasil/main.go`

**Interfaces:**
- Consumes: `config.GarlicConfig` new fields (Task 13), `garlic.Config` new fields (Tasks 8/10/11).

- [ ] **Step 1: N/A — this task is pure wiring with no independent unit to test in isolation**

The correctness check for this task is a full build plus the existing integration/end-to-end path (`go build ./...` at the repo root, and — if time allows in review — a manual smoke test per `docs/garlic-testing.md` with `Garlic.AutoPoolEnabled: true` and a couple of `BootstrapPeers` set between two locally-run instances, the same style of setup used earlier in this project for live verification). Skip the write-test/run-test steps below; go straight to implementation.

- [ ] **Step 2: Implement**

In `cmd/yggdrasil/main.go`, in the `if cfg.Garlic.Enabled {` block, after the existing `gcfg.MinHopCount = cfg.Garlic.MinHopCount` line (around line 346), add:

```go
			gcfg.MinHopCount = cfg.Garlic.MinHopCount
			gcfg.BootstrapPeers = cfg.Garlic.BootstrapPeers
			gcfg.AutoPoolEnabled = cfg.Garlic.AutoPoolEnabled
			gcfg.AutoPoolSize = cfg.Garlic.AutoPoolSize
			if gcfg.AutoRotationInterval, err = time.ParseDuration(cfg.Garlic.AutoRotationInterval); err != nil {
				panic(fmt.Sprintf("invalid Garlic.AutoRotationInterval %q: %v", cfg.Garlic.AutoRotationInterval, err))
			}
			gcfg.CoverTrafficEnabled = cfg.Garlic.CoverTrafficEnabled
			if gcfg.CoverTrafficInterval, err = time.ParseDuration(cfg.Garlic.CoverTrafficInterval); err != nil {
				panic(fmt.Sprintf("invalid Garlic.CoverTrafficInterval %q: %v", cfg.Garlic.CoverTrafficInterval, err))
			}
```

(This follows the exact pattern the existing `Jitter.MinDelay`/`MaxDelay` parsing two lines above already uses — same `time.ParseDuration` + `panic(fmt.Sprintf(...))` shape, same `err` variable already in scope from the surrounding block.)

- [ ] **Step 3: Verify the build**

Run: `go build ./...` (repo root)
Expected: clean build.

- [ ] **Step 4: Commit**

```bash
git add cmd/yggdrasil/main.go
git commit -m "cmd/yggdrasil: wire Garlic auto-pool/bootstrap/cover-traffic config"
```

---

### Task 15: install.sh bootstrap peers

**Files:**
- Modify: `install.sh`

**Interfaces:**
- Consumes: the existing JSON-patch step that already sets `Garlic.Enabled`/`Dashboard.Enabled` (from the earlier dashboard-install work).

- [ ] **Step 1: N/A — shell script, no unit test harness in this repo**

Verification is manual: run `install.sh` (or its config-patch step in isolation) against a `yggdrasil -genconf -json` output and confirm the resulting config's `garlic.bootstrapPeers` array matches `GARLIC_BOOTSTRAP_PEERS`, the same way this env var's sibling `ENABLE_GARLIC`/`ENABLE_DASHBOARD` were validated in the prior session's work on this file.

- [ ] **Step 2: Implement**

Read the current JSON-patch step in `install.sh` (the one that sets both `Garlic.Enabled` and `Dashboard.Enabled` in one pass, per this repo's prior work) before editing — match its existing jq/python3 dual-path style exactly. Add:

- A new env var `GARLIC_BOOTSTRAP_PEERS` (default empty string), documented in the header comment block alongside `ENABLE_GARLIC`/`ENABLE_DASHBOARD`: comma-separated hex node keys.
- In the jq variant of the patch: convert `GARLIC_BOOTSTRAP_PEERS` (if non-empty) to a JSON array via `jq -R 'split(",")'` and merge it into `.garlic.bootstrapPeers`.
- In the python3 fallback variant: `GARLIC_BOOTSTRAP_PEERS.split(",") if GARLIC_BOOTSTRAP_PEERS else []` assigned to `cfg["garlic"]["bootstrapPeers"]`.
- Leave `GARLIC_BOOTSTRAP_PEERS` unset/empty by default — a freshly-installed first node has nobody to bootstrap from yet.

- [ ] **Step 3: Manual verification**

```bash
sh -n install.sh   # syntax check
```
Then, if a sandbox/test VM is available (per this repo's prior verification pattern for this file): generate a config, run the patch step with `GARLIC_BOOTSTRAP_PEERS=aabbcc,ddeeff`, confirm `.garlic.bootstrapPeers == ["aabbcc","ddeeff"]` in the result.

- [ ] **Step 4: Commit**

```bash
git add install.sh
git commit -m "install.sh: support GARLIC_BOOTSTRAP_PEERS for multi-server bootstrap"
```

---

### Task 16: Dashboard surface

**Files:**
- Modify: `yggdashboard/src/lib/server/types.ts`
- Modify: `yggdashboard/src/lib/server/poll.ts`
- Modify: whichever route file already renders known-peers/circuits for Garlic (check `yggdashboard/src/routes/` for the existing `/garlic` or `/circuits` page from the prior dashboard project)
- Test: whichever `*.test.ts`/`*.spec.ts` files already cover `poll.ts`'s snapshot shape

**Interfaces:**
- Consumes: `getGarlicAutoPool`, `getGarlicKnownPeers`'s new `selfVerified` field (Task 12).

- [ ] **Step 1: Write the failing test**

Read `yggdashboard/src/lib/server/poll.ts` and its existing test file first — this task must extend the existing `Promise.allSettled` polling batch and `Snapshot` shape exactly the way `getGarlicCircuits`/`getGarlicKnownPeers` were already added there, not introduce a parallel polling mechanism. Add a test (in whatever file already tests `poll.ts`'s snapshot assembly) asserting the new `autoPool` field appears in a built snapshot when the admin client's `getGarlicAutoPool` call succeeds, and is empty/absent gracefully when it fails (matching this file's existing `Promise.allSettled` fallback-to-latest convention for every other per-call failure).

- [ ] **Step 2: Run test to verify it fails**

Run (from `yggdashboard/`): `npm test -- --run poll` (adjust to this project's actual test-runner invocation, found in `package.json`)
Expected: FAIL (`autoPool` not yet part of the snapshot type).

- [ ] **Step 3: Implement**

In `yggdashboard/src/lib/server/types.ts`, extend the `Snapshot` interface (find the existing `garlicCircuits`/`garlicKnownPeers`-shaped fields and add alongside them):

```ts
export interface GarlicAutoPoolEntry {
  circuitId: string;
  createdAt: string;
  hops: number;
}

// Extend the existing Snapshot interface:
//   autoPool: GarlicAutoPoolEntry[];
// Extend the existing known-peers entry type with:
//   selfVerified: boolean;
```

(Match the exact existing field-naming convention in this file — e.g. if `garlicCircuits`/`garlicKnownPeers` are the sibling field names already there, name this one consistently, such as `garlicAutoPool`.)

In `yggdashboard/src/lib/server/poll.ts`, add `getGarlicAutoPool` to the existing `Promise.allSettled` batch alongside the other Garlic calls, following the exact same fallback-to-latest-on-failure pattern already used for `getGarlicCircuits`/`getGarlicKnownPeers` in this file, and thread the `selfVerified` field through wherever known-peers results are already mapped into the snapshot shape.

In the existing `/garlic` (or equivalent) route/component, add: a self-verified/gossiped badge per known-peer row (reading the new `selfVerified` field), and a small auto-pool status panel (pool size, per-circuit age/hop count) reading the new snapshot field — following this project's existing Svelte component conventions (check the existing known-peers table component for styling/structure to match).

- [ ] **Step 4: Run tests to verify they pass**

Run: `npm test` and `npm run check` (from `yggdashboard/`)
Expected: PASS, no new type errors.

- [ ] **Step 5: Commit**

```bash
git add yggdashboard/src/lib/server/types.ts yggdashboard/src/lib/server/poll.ts yggdashboard/src/routes/
git commit -m "yggdashboard: show self-verified/gossiped badge and auto-pool status"
```

---

### Task 17: Documentation updates

**Files:**
- Modify: `docs/garlic-protocol.md`
- Modify: `docs/garlic-threat-model.md`

**Interfaces:**
- Consumes: the whole feature (Tasks 1-16), for accurate documentation.

- [ ] **Step 1: N/A — documentation task, no test**

- [ ] **Step 2: Implement**

In `docs/garlic-protocol.md`, add a new section (following this doc's existing per-message-type documentation style) covering: `msgTypeAnnounceRequest` (empty body, triggers an immediate `GossipAnnounce` reply), `msgTypeCircuitDataV3` (identical onion structure to `msgTypeCircuitData`, distinguished only by the outer type byte; forwarding must preserve it; terminal delivery's `Inner[0]` is a kind byte — real/cover), and `CapabilityAutoCircuit`.

In `docs/garlic-threat-model.md`:
- **"Sybil nodes"**: add a third bullet alongside `SelectDiversePath` and multipath pools — the self-verified/gossiped trust split plus the first-hop guard policy (`SelectPathWithGuardPolicy`) — narrowing (not solving) the same class of attack the existing bullets describe. Keep the "what remains genuinely unmitigated" paragraph's substance intact (still no IP/ASN diversity, no resource cost) — this is a third partial mitigation, not a claim of resolution.
- **"Traffic correlation"**: note that cover traffic is now default-on for auto-pool circuits specifically (`Config.CoverTrafficEnabled`), distinct from the pre-existing opt-in-per-call `SendGarlicBundled` cover entries — both still real-cost, still not a mixnet guarantee, per the existing paragraph's framing.
- **"Malicious client"**: one sentence noting `msgTypeAnnounceRequest`'s bounded amplification (≤ `Config.GossipSampleSize` entries per request, itself ≤ `maxAnnouncePeers`), gated by the existing per-peer `RateLimiter` on receipt — not a new unbounded-response category.
- **"Route manipulation"**: update the existing "SelectPath(n) is available but not mandatory" sentence — `AutoCreateCircuit`/`createGarlicCircuitAuto` now make automatic selection materially easier to reach, while the manual `createGarlicCircuit` path remains available and unchanged (still not mandatory).

- [ ] **Step 3: Commit**

```bash
git add docs/garlic-protocol.md docs/garlic-threat-model.md
git commit -m "docs: document Garlic auto-discovery/auto-pool/cover-traffic wire additions"
```

---

## Post-plan checklist (do once all tasks are merged)

- `go build ./... && go vet ./... && go test ./...` clean at the repo root.
- `cd yggdashboard && npm test && npm run check` clean.
- Manual smoke test per `docs/garlic-testing.md`, extended for this feature: two locally-run instances, each with the other's key in `Garlic.BootstrapPeers`, `Garlic.AutoPoolEnabled: true`; confirm via `yggdrasilctl getGarlicAutoPool` that each fills a pool without any manual `createGarlicCircuit` call, and via `yggdrasilctl getGarlicKnownPeers` that entries show the expected `selfVerified` split.
