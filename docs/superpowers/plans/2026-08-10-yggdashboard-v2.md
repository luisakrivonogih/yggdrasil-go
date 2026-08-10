# yggdashboard v2 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build a real, working local operator dashboard for Yggdrasil/Garlic nodes — Go backend instrumentation (config, process spawning, minimal Garlic read-only additions) plus a SvelteKit 5 SSR/SPA frontend with five routed pages, per `docs/superpowers/specs/2026-08-10-yggdashboard-v2-design.md`.

**Architecture:** `yggdrasil` gains a `Dashboard` config block (disabled/loopback by default) and a small `src/dashboard` package that spawns a SvelteKit `adapter-node` server (`yggdashboard/`) as a child process when enabled, passing it the node's own `AdminListen` address. That Node process is the only thing that talks to the admin socket; the browser only ever talks to the Node process's own `/api/*` routes and SSR'd pages, polling every 1-2s. Garlic gains a handful of new read-only accessors and local-only counters — no protocol/crypto changes.

**Tech Stack:** Go (existing toolchain, no new dependencies). SvelteKit 2 + Svelte 5, `@sveltejs/adapter-node`, `tsx`, Vitest, `d3-force` (layout math only, for `/graph`).

## Global Constraints

- No changes to Garlic's wire format, cryptography, or the onion-processing decision logic's external (network-observable) behavior. Every backend addition is a new read-only accessor over data that already exists privately, or a local-only counter incremented at a call site that already exists.
- `dashboard.enabled: false` and `dashboard.listen: "127.0.0.1:8080"` by default; never `0.0.0.0`/`::`. One `Enabled`/`Listen` pair controls the dashboard UI and its `/api/*` endpoints together — no separate toggle or bind for the API.
- No private keys, session/AEAD keys, or decrypted payloads may ever appear in an admin response the dashboard consumes, or in any `/api/*` response. Every new admin handler response is a hand-picked field map, never a struct/field passthrough.
- `yggdashboard/` is a separate Node/SvelteKit project: its own `package.json`, own toolchain, **not** part of `go.mod` or the root `./build` script.
- No WebSockets. HTTP polling only, 1-2s interval, from one shared background poller per dashboard server process (not per browser tab).
- No Tailwind, no CSS framework, no component library — plain CSS in each Svelte component's `<style>` block. Dark, neutral, high-density, monospace for keys/addresses/counters.
- Go tests: `go test ./...` from the repo root must stay green after every task. Follow existing test conventions exactly (see each task's file for the pattern already in use).
- Frontend tests: Vitest, following the naming/assertion style already established in the superseded worktree's admin-client/json-stream tests (ported into this plan).
- Every step with code must contain complete, real code — this plan has no placeholders for an implementer to fill in.

---

## Part A — Go backend

### Task 1: Dashboard config block

**Files:**
- Modify: `src/config/config.go`
- Modify: `src/config/config_test.go`

**Interfaces:**
- Consumes: nothing (first task).
- Produces: `config.DashboardConfig{Enabled bool, Listen string, Path string}`, embedded on `NodeConfig.Dashboard`, defaulted in `GenerateConfig()`. Used by Task 8 (`dashboard.Config`) and Task 9 (`cmd/yggdrasil/main.go`).

- [ ] **Step 1: Write the failing test**

Add to `src/config/config_test.go` (append at end of file):

```go
func TestDashboardConfigDefaults(t *testing.T) {
	cfg := GenerateConfig()
	if cfg.Dashboard.Enabled {
		t.Error("Dashboard.Enabled = true by default, want false")
	}
	if cfg.Dashboard.Listen != "127.0.0.1:8080" {
		t.Errorf("Dashboard.Listen = %q, want \"127.0.0.1:8080\"", cfg.Dashboard.Listen)
	}
	if cfg.Dashboard.Path != "" {
		t.Errorf("Dashboard.Path = %q, want empty (tries conventional install paths)", cfg.Dashboard.Path)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./src/config/... -run TestDashboardConfigDefaults -v`
Expected: FAIL — `cfg.Dashboard` undefined (compile error, `DashboardConfig`/`Dashboard` field don't exist yet).

- [ ] **Step 3: Add `DashboardConfig` and wire it into `NodeConfig`**

In `src/config/config.go`, add the `Dashboard` field to `NodeConfig` (insert after the existing `Garlic` field):

```go
	Garlic              GarlicConfig               `comment:"Configuration for the experimental Garlic Routing Overlay, an optional\nprivacy-enhanced routing layer built on top of Yggdrasil - see\ndocs/garlic-architecture.md. When Enabled is false (the default),\nbehavior is identical to a node with no Garlic support at all."`
	Dashboard           DashboardConfig            `comment:"Configuration for the local operator dashboard - a web UI and\nread-only API showing this node's live status, traffic, peers, and\n(if enabled) Garlic circuits. Disabled by default. When enabled, the\nlistener should stay loopback-only - the dashboard and its API have\nno authentication of their own."`
}

// DashboardConfig holds configuration for the local operator dashboard.
// The zero value (Enabled: false) means yggdrasil starts no dashboard
// process and behaves exactly as it does today.
type DashboardConfig struct {
	Enabled bool   `comment:"Enables the local operator dashboard HTTP server (UI and its\nread-only API together) as a subprocess yggdrasil manages. Default is\nfalse."`
	Listen  string `comment:"Listen address (host:port) for the dashboard's HTTP server. Must\ndefault to a loopback address (127.0.0.1 or ::1). Changing this to a\nnon-loopback address is your own choice and your own risk - the\ndashboard and its API have no authentication."`
	Path    string `comment:"Directory containing the dashboard's built assets (the 'npm run\nbuild' output's build/ directory). Empty tries conventional install\npaths, then a path relative to the yggdrasil binary for development."`
}
```

Note the closing `}` of `NodeConfig` moves to after the new `Dashboard` field — you're inserting a field, not appending after the struct closes.

In `GenerateConfig()`, add defaults right after the existing `cfg.Garlic = GarlicConfig{...}` block:

```go
	cfg.Dashboard = DashboardConfig{
		Enabled: false,
		Listen:  "127.0.0.1:8080",
		Path:    "",
	}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test ./src/config/... -run TestDashboardConfigDefaults -v`
Expected: PASS.

- [ ] **Step 5: Run the full config package test suite**

Run: `go test ./src/config/...`
Expected: PASS, no regressions (`TestConfigReadFromEmpty`, `TestGarlicConfigDefaultsDisabled`, etc. still green).

- [ ] **Step 6: Commit**

```bash
git add src/config/config.go src/config/config_test.go
git commit -m "config: add Dashboard config block, disabled/loopback by default"
```

---

### Task 2: Node uptime

**Files:**
- Modify: `src/core/core.go`
- Create: `src/core/core_test.go`
- Modify: `src/admin/getself.go`

**Interfaces:**
- Consumes: nothing new.
- Produces: `func (c *Core) Uptime() time.Duration`. Used by `getSelf`'s admin response (`GetSelfResponse.Uptime`), which Task 9's dashboard build consumes for the status bar and "Online/Degraded/Offline" determination described in the spec.

- [ ] **Step 1: Write the failing test**

Create `src/core/core_test.go`:

```go
package core

import (
	"testing"
	"time"
)

func TestCoreUptimeIncreasesFromStart(t *testing.T) {
	c := &Core{started: time.Now()}
	time.Sleep(5 * time.Millisecond)
	u := c.Uptime()
	if u <= 0 {
		t.Fatalf("Uptime() = %v, want > 0 shortly after start", u)
	}
	if u > time.Second {
		t.Fatalf("Uptime() = %v, want a small duration just after start", u)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./src/core/... -run TestCoreUptimeIncreasesFromStart -v`
Expected: FAIL — `started` field and `Uptime` method don't exist yet.

- [ ] **Step 3: Add the `started` field and `Uptime` method**

In `src/core/core.go`, add `"time"` to the import block, add a `started time.Time` field to the `Core` struct (next to `pathNotify`/`garlicHandler`), and set it in `New`:

```go
	pathNotify    func(ed25519.PublicKey)
	garlicHandler atomic.Pointer[GarlicHandler]
	started       time.Time
}

func New(cert *tls.Certificate, logger Logger, opts ...SetupOption) (*Core, error) {
	c := &Core{
		log:     logger,
		started: time.Now(),
	}
```

Add the accessor (near the other simple accessors in `api.go`, or directly below `New` in `core.go` — place it in `core.go` right after `New` for this task, since `api.go` isn't otherwise touched):

```go
// Uptime returns how long this Core has been running.
func (c *Core) Uptime() time.Duration {
	return time.Since(c.started)
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test ./src/core/... -run TestCoreUptimeIncreasesFromStart -v`
Expected: PASS.

- [ ] **Step 5: Wire `Uptime` into `getSelf`**

In `src/admin/getself.go`, add the field and populate it:

```go
type GetSelfResponse struct {
	BuildName      string  `json:"build_name"`
	BuildVersion   string  `json:"build_version"`
	PublicKey      string  `json:"key"`
	IPAddress      string  `json:"address"`
	RoutingEntries uint64  `json:"routing_entries"`
	Subnet         string  `json:"subnet"`
	Uptime         float64 `json:"uptime"`
}

func (a *AdminSocket) getSelfHandler(_ *GetSelfRequest, res *GetSelfResponse) error {
	self := a.core.GetSelf()
	snet := a.core.Subnet()
	res.BuildName = version.BuildName()
	res.BuildVersion = version.BuildVersion()
	res.PublicKey = hex.EncodeToString(self.Key[:])
	res.IPAddress = a.core.Address().String()
	res.Subnet = snet.String()
	res.RoutingEntries = self.RoutingEntries
	res.Uptime = a.core.Uptime().Seconds()
	return nil
}
```

- [ ] **Step 6: Run the full core and admin package test suites**

Run: `go test ./src/core/... ./src/admin/...`
Expected: PASS, no regressions.

- [ ] **Step 7: Commit**

```bash
git add src/core/core.go src/core/core_test.go src/admin/getself.go
git commit -m "core, admin: add node uptime, expose via getSelf"
```

---

### Task 3: Circuit accessors (originator side)

**Files:**
- Modify: `src/garlic/circuit.go`
- Modify: `src/garlic/circuit_test.go`

**Interfaces:**
- Consumes: nothing new.
- Produces: `func (c *Circuit) HopKeys() [][]byte`, `func (c *Circuit) TrafficStats() (packets, bytes uint64)`, `func (c *Circuit) IsClosed() bool`. Used by Task 7's `getGarlicCircuits` admin handler.

- [ ] **Step 1: Write the failing tests**

Append to `src/garlic/circuit_test.go`:

```go
func TestCircuitHopKeysReturnsOrderedNodeKeys(t *testing.T) {
	hops := []Hop{
		{NodeKey: []byte("node-a"), Key: make([]byte, 32)},
		{NodeKey: []byte("node-b"), Key: make([]byte, 32)},
	}
	c, err := NewCircuit(hops, time.Minute, 100, 100000)
	if err != nil {
		t.Fatalf("NewCircuit returned error: %v", err)
	}
	keys := c.HopKeys()
	if len(keys) != 2 {
		t.Fatalf("HopKeys() returned %d keys, want 2", len(keys))
	}
	if string(keys[0]) != "node-a" || string(keys[1]) != "node-b" {
		t.Fatalf("HopKeys() = %q, want [node-a node-b]", keys)
	}
}

func TestCircuitHopKeysIsACopy(t *testing.T) {
	hops := []Hop{{NodeKey: []byte("node-a"), Key: make([]byte, 32)}}
	c, err := NewCircuit(hops, time.Minute, 100, 100000)
	if err != nil {
		t.Fatalf("NewCircuit returned error: %v", err)
	}
	keys := c.HopKeys()
	keys[0][0] = 'X' // mutate the returned slice
	if string(c.HopKeys()[0]) != "node-a" {
		t.Fatal("mutating HopKeys()'s return value affected the circuit's internal hop state")
	}
}

func TestCircuitTrafficStatsTracksSeals(t *testing.T) {
	hops := []Hop{{NodeKey: []byte("node-a"), Key: make([]byte, 32)}}
	c, err := NewCircuit(hops, time.Minute, 100, 100000)
	if err != nil {
		t.Fatalf("NewCircuit returned error: %v", err)
	}
	packets, bytes := c.TrafficStats()
	if packets != 0 || bytes != 0 {
		t.Fatalf("TrafficStats() before any Seal = (%d, %d), want (0, 0)", packets, bytes)
	}
	if _, _, _, err := c.Seal([]byte("hello")); err != nil {
		t.Fatalf("Seal returned error: %v", err)
	}
	packets, bytes = c.TrafficStats()
	if packets != 1 || bytes != 5 {
		t.Fatalf("TrafficStats() after one 5-byte Seal = (%d, %d), want (1, 5)", packets, bytes)
	}
}

func TestCircuitIsClosedReflectsCloseCall(t *testing.T) {
	hops := []Hop{{NodeKey: []byte("node-a"), Key: make([]byte, 32)}}
	c, err := NewCircuit(hops, time.Minute, 100, 100000)
	if err != nil {
		t.Fatalf("NewCircuit returned error: %v", err)
	}
	if c.IsClosed() {
		t.Fatal("IsClosed() = true before Close(), want false")
	}
	c.Close()
	if !c.IsClosed() {
		t.Fatal("IsClosed() = false after Close(), want true")
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./src/garlic/... -run TestCircuitHopKeys -run TestCircuitTrafficStats -run TestCircuitIsClosed -v`
Expected: FAIL — `HopKeys`, `TrafficStats`, `IsClosed` undefined.

- [ ] **Step 3: Implement the accessors**

Append to `src/garlic/circuit.go`:

```go
// HopKeys returns a copy of this circuit's ordered hop node keys - the
// path the originator itself chose when building the circuit. Safe to
// expose: the originator already knows its own path in plaintext: this
// isn't derived from decrypting anyone else's traffic.
func (c *Circuit) HopKeys() [][]byte {
	c.mu.Lock()
	defer c.mu.Unlock()
	keys := make([][]byte, len(c.hops))
	for i, h := range c.hops {
		keys[i] = append([]byte(nil), h.NodeKey...)
	}
	return keys
}

// TrafficStats returns how many packets and payload bytes this circuit
// has sent via Seal so far.
func (c *Circuit) TrafficStats() (packets, bytes uint64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.packetsSent, c.bytesSent
}

// IsClosed reports whether Close has been called on this circuit.
func (c *Circuit) IsClosed() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.closed
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./src/garlic/... -run TestCircuitHopKeys -run TestCircuitTrafficStats -run TestCircuitIsClosed -v`
Expected: PASS, all 4 new tests green.

- [ ] **Step 5: Run the full garlic package test suite**

Run: `go test ./src/garlic/...`
Expected: PASS, no regressions.

- [ ] **Step 6: Commit**

```bash
git add src/garlic/circuit.go src/garlic/circuit_test.go
git commit -m "garlic: add read-only Circuit accessors for hops, traffic, closed state"
```

---

### Task 4: CircuitManager.List()

**Files:**
- Modify: `src/garlic/circuit_manager.go`
- Modify: `src/garlic/circuit_manager_test.go`

**Interfaces:**
- Consumes: `Circuit` from Task 3 (only `HopKeys`/`TrafficStats`/`IsClosed`/exported fields — this task's test only needs `.ID`).
- Produces: `func (m *CircuitManager) List() []*Circuit`. Used by Task 7's `Garlic.OriginatedCircuits()` and `getGarlicCircuits` admin handler.

- [ ] **Step 1: Write the failing test**

Append to `src/garlic/circuit_manager_test.go`:

```go
func TestCircuitManagerListReturnsAllTrackedCircuits(t *testing.T) {
	m := NewCircuitManager(CircuitManagerConfig{MaxCircuits: 10, MaxCircuitsPerPeer: 10})
	c1, err := m.Add([]Hop{{NodeKey: []byte("peer-a")}}, time.Minute, 100, 100000)
	if err != nil {
		t.Fatalf("Add returned error: %v", err)
	}
	c2, err := m.Add([]Hop{{NodeKey: []byte("peer-b")}}, time.Minute, 100, 100000)
	if err != nil {
		t.Fatalf("Add returned error: %v", err)
	}

	list := m.List()
	if len(list) != 2 {
		t.Fatalf("List() returned %d circuits, want 2", len(list))
	}
	found := map[CircuitID]bool{}
	for _, c := range list {
		found[c.ID] = true
	}
	if !found[c1.ID] || !found[c2.ID] {
		t.Fatalf("List() = %+v, want to include %d and %d", list, c1.ID, c2.ID)
	}
}

func TestCircuitManagerListEmptyWhenNoCircuits(t *testing.T) {
	m := NewCircuitManager(CircuitManagerConfig{MaxCircuits: 10, MaxCircuitsPerPeer: 10})
	if list := m.List(); len(list) != 0 {
		t.Fatalf("List() = %+v, want empty", list)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./src/garlic/... -run TestCircuitManagerList -v`
Expected: FAIL — `List` undefined.

- [ ] **Step 3: Implement `List`**

Append to `src/garlic/circuit_manager.go`:

```go
// List returns a snapshot slice of every circuit currently tracked. The
// returned slice is a copy of the map's contents at the time of the
// call - safe to range over without holding m's lock, at the cost of
// possibly being immediately stale (fine for the admin-facing snapshot
// this exists for; nothing here is a hot path).
func (m *CircuitManager) List() []*Circuit {
	m.mu.Lock()
	defer m.mu.Unlock()
	list := make([]*Circuit, 0, len(m.circuits))
	for _, c := range m.circuits {
		list = append(list, c)
	}
	return list
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./src/garlic/... -run TestCircuitManagerList -v`
Expected: PASS.

- [ ] **Step 5: Run the full garlic package test suite**

Run: `go test ./src/garlic/...`
Expected: PASS, no regressions.

- [ ] **Step 6: Commit**

```bash
git add src/garlic/circuit_manager.go src/garlic/circuit_manager_test.go
git commit -m "garlic: add CircuitManager.List for admin-facing circuit enumeration"
```

---

### Task 5: Local-only security counters

**Files:**
- Create: `src/garlic/security.go`
- Create: `src/garlic/security_test.go`
- Modify: `src/garlic/manager.go` (add `security SecurityCounters` field)
- Modify: `src/garlic/protocol.go` (increment counters at existing drop sites)
- Modify: `src/garlic/relay_logic_test.go` (add drop-category assertions)

**Interfaces:**
- Consumes: nothing new.
- Produces: `type SecurityCounters struct{...}` (unexported fields, `Add`-only from within the package), `type SecurityCounterSnapshot struct{ReplayDrops, MalformedPackets, ExpiredPackets, AuthFailures, RelayTableFull uint64}`, `func (s *SecurityCounters) snapshot() SecurityCounterSnapshot`. Used by Task 7's extended `GetStats()`/`getGarlicStats`.

- [ ] **Step 1: Write the failing test for the counters themselves**

Create `src/garlic/security_test.go`:

```go
package garlic

import "testing"

func TestSecurityCountersStartAtZero(t *testing.T) {
	var s SecurityCounters
	snap := s.snapshot()
	if snap != (SecurityCounterSnapshot{}) {
		t.Fatalf("snapshot() = %+v, want all zeros", snap)
	}
}

func TestSecurityCountersSnapshotReflectsIncrements(t *testing.T) {
	var s SecurityCounters
	s.replayDrops.Add(1)
	s.replayDrops.Add(1)
	s.malformedPackets.Add(1)
	s.expiredPackets.Add(3)
	s.authFailures.Add(1)
	s.relayTableFull.Add(1)

	snap := s.snapshot()
	want := SecurityCounterSnapshot{
		ReplayDrops:      2,
		MalformedPackets: 1,
		ExpiredPackets:   3,
		AuthFailures:     1,
		RelayTableFull:   1,
	}
	if snap != want {
		t.Fatalf("snapshot() = %+v, want %+v", snap, want)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./src/garlic/... -run TestSecurityCounters -v`
Expected: FAIL — `SecurityCounters` undefined.

- [ ] **Step 3: Implement `SecurityCounters`**

Create `src/garlic/security.go`:

```go
package garlic

// SecurityCounters tracks local-only counts of why this node dropped an
// incoming Garlic circuit-data message, for operator visibility (e.g.
// via the dashboard). These are never transmitted to peers - the wire
// protocol still returns the same undifferentiated actionDrop behavior
// in every case (see protocol.go's processCircuitData doc comment on
// not leaking which check failed); only this node's own admin socket,
// reachable by the same locally-trusted audience that can already run
// yggdrasilctl, exposes the category breakdown. Cumulative since
// process start. The zero value is ready to use.
type SecurityCounters struct {
	replayDrops      atomicUint64
	malformedPackets atomicUint64
	expiredPackets   atomicUint64
	authFailures     atomicUint64
	relayTableFull   atomicUint64
}

// SecurityCounterSnapshot is a point-in-time copy of SecurityCounters,
// safe to serialize (used directly in the getGarlicStats admin
// response).
type SecurityCounterSnapshot struct {
	ReplayDrops      uint64
	MalformedPackets uint64
	ExpiredPackets   uint64
	AuthFailures     uint64
	RelayTableFull   uint64
}

func (s *SecurityCounters) snapshot() SecurityCounterSnapshot {
	return SecurityCounterSnapshot{
		ReplayDrops:      s.replayDrops.Load(),
		MalformedPackets: s.malformedPackets.Load(),
		ExpiredPackets:   s.expiredPackets.Load(),
		AuthFailures:     s.authFailures.Load(),
		RelayTableFull:   s.relayTableFull.Load(),
	}
}
```

This uses a small `atomicUint64` type alias for readability at call sites in `protocol.go` (`g.security.replayDrops.Add(1)`); add it to the same file:

```go
import "sync/atomic"

type atomicUint64 = atomic.Uint64
```

Place the `import` and type alias at the top of `security.go`, before the `SecurityCounters` struct.

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test ./src/garlic/... -run TestSecurityCounters -v`
Expected: PASS.

- [ ] **Step 5: Add the `security` field to `Garlic` and instrument `protocol.go`'s drop sites**

In `src/garlic/manager.go`, add `security SecurityCounters` to the `Garlic` struct (it's a plain value field — no constructor change needed, the zero value is ready to use, and `newTestGarlic`'s struct literals in existing tests keep compiling unchanged since Go zero-initializes omitted fields):

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

	delivered chan DeliveredMessage
	...
```

(Insert `security SecurityCounters` right after `discovery *discoveryRegistry` — the rest of the struct is unchanged.)

In `src/garlic/protocol.go`, instrument every `actionDrop` return in `processCircuitData` (also note the existing combined `if !ok || !window.CheckAndSet(...)` is split into two separate checks below, so relay-table-full and replay can be counted in their own categories — the *behavior* for both is identical to today, actionDrop either way):

```go
func (g *Garlic) processCircuitData(body []byte) circuitAction {
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

	circuitID := CircuitID(env.CircuitID)
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
	key, err := DeriveKey(secret, nil, LabelLayerKey)
	if err != nil {
		g.security.authFailures.Add(1)
		return circuitAction{kind: actionDrop}
	}

	layer, err := DecryptLayer(key, env.PacketCounter, env.Body)
	if err != nil {
		// Wrong key (message wasn't encrypted for us), tampered
		// ciphertext, or malformed plaintext all look identical here by
		// design - see ErrNotForThisIdentity's doc comment.
		g.security.authFailures.Add(1)
		return circuitAction{kind: actionDrop}
	}

	if len(layer.NextHop) == 0 {
		return circuitAction{kind: actionDeliver, circuitID: circuitID, payload: layer.Inner}
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
	forwardMsg = append(forwardMsg, msgTypeCircuitData)
	forwardMsg = append(forwardMsg, ephemeralPub...)
	forwardMsg = append(forwardMsg, nextBytes...)

	return circuitAction{kind: actionForward, circuitID: circuitID, forwardTo: layer.NextHop, forwardMsg: forwardMsg}
}
```

Only the lines calling `g.security.*.Add(1)` are new; every other line is unchanged from the current file — this is purely additive instrumentation, not a logic change.

- [ ] **Step 6: Add drop-category assertions to the existing relay-logic tests**

In `src/garlic/relay_logic_test.go`, extend the existing drop tests to also assert the right counter moved (append these checks to the existing test bodies — shown here as the diffs to make; each is one added `if` block right after the existing `action.kind` assertion):

In `TestProcessCircuitDataDropsWrongRecipient`, after the existing assertion:

```go
	if got := g.security.snapshot().AuthFailures; got != 1 {
		t.Fatalf("security.AuthFailures = %d, want 1", got)
	}
```

In `TestProcessCircuitDataDropsReplay`, after the existing `second.kind` assertion:

```go
	if got := g.security.snapshot().ReplayDrops; got != 1 {
		t.Fatalf("security.ReplayDrops = %d, want 1", got)
	}
```

In `TestProcessCircuitDataDropsExpired`, after the existing assertion:

```go
	if got := g.security.snapshot().ExpiredPackets; got != 1 {
		t.Fatalf("security.ExpiredPackets = %d, want 1", got)
	}
```

In `TestProcessCircuitDataDropsMalformedTooShort`, after the existing assertion:

```go
	if got := g.security.snapshot().MalformedPackets; got != 1 {
		t.Fatalf("security.MalformedPackets = %d, want 1", got)
	}
```

In `TestProcessCircuitDataDropsMalformedEnvelope`, after the existing assertion:

```go
	if got := g.security.snapshot().MalformedPackets; got != 1 {
		t.Fatalf("security.MalformedPackets = %d, want 1", got)
	}
```

In `TestProcessCircuitDataDropsWhenRelayTableFull`, after the existing assertion:

```go
	if got := g.security.snapshot().RelayTableFull; got != 1 {
		t.Fatalf("security.RelayTableFull = %d, want 1", got)
	}
```

- [ ] **Step 7: Run the full garlic package test suite**

Run: `go test ./src/garlic/...`
Expected: PASS — the six extended tests and the new `security_test.go` tests all green, no regressions elsewhere.

- [ ] **Step 8: Commit**

```bash
git add src/garlic/security.go src/garlic/security_test.go src/garlic/manager.go src/garlic/protocol.go src/garlic/relay_logic_test.go
git commit -m "garlic: add local-only security drop counters, never sent over the wire"
```

---

### Task 6: Relay hop and traffic tracking

**Files:**
- Modify: `src/garlic/relaystate.go`
- Modify: `src/garlic/relaystate_test.go`
- Modify: `src/garlic/manager.go`

**Interfaces:**
- Consumes: nothing new from this plan (uses `CircuitID`, `ed25519.PublicKey` already in the package).
- Produces: `type RelayCircuitInfo struct{ID CircuitID; PreviousHop, NextHop []byte; FirstSeen, LastActive time.Time; PacketsRelayed, BytesRelayed uint64}`, `func (s *relayCircuitState) snapshot() []RelayCircuitInfo`, `func (g *Garlic) RelayCircuits() []RelayCircuitInfo`. Used by Task 7's `getGarlicCircuits` admin handler.

- [ ] **Step 1: Write the failing tests for `relayCircuitState`'s new behavior**

Append to `src/garlic/relaystate_test.go`:

```go
func TestRelayCircuitStateRecordForwardTracksHopsAndTraffic(t *testing.T) {
	s := newRelayCircuitState(1024)
	if _, ok := s.replayWindowFor(CircuitID(1)); !ok {
		t.Fatal("replayWindowFor(1) ok = false, want true")
	}
	s.recordForward(CircuitID(1), []byte("prev-hop"), []byte("next-hop"), 100)
	s.recordForward(CircuitID(1), []byte("prev-hop"), []byte("next-hop"), 50)

	snap := s.snapshot()
	if len(snap) != 1 {
		t.Fatalf("snapshot() returned %d entries, want 1", len(snap))
	}
	info := snap[0]
	if info.ID != CircuitID(1) {
		t.Fatalf("info.ID = %d, want 1", info.ID)
	}
	if string(info.PreviousHop) != "prev-hop" || string(info.NextHop) != "next-hop" {
		t.Fatalf("info.PreviousHop, NextHop = %q, %q, want \"prev-hop\", \"next-hop\"", info.PreviousHop, info.NextHop)
	}
	if info.PacketsRelayed != 2 {
		t.Fatalf("info.PacketsRelayed = %d, want 2", info.PacketsRelayed)
	}
	if info.BytesRelayed != 150 {
		t.Fatalf("info.BytesRelayed = %d, want 150", info.BytesRelayed)
	}
	if info.FirstSeen.IsZero() || info.LastActive.IsZero() {
		t.Fatal("FirstSeen/LastActive must be set")
	}
	if info.LastActive.Before(info.FirstSeen) {
		t.Fatal("LastActive must not be before FirstSeen")
	}
}

func TestRelayCircuitStateRecordForwardIsNoOpForUntrackedCircuit(t *testing.T) {
	s := newRelayCircuitState(1024)
	// No replayWindowFor call first - this circuit was never admitted.
	s.recordForward(CircuitID(99), []byte("prev"), []byte("next"), 10)
	if snap := s.snapshot(); len(snap) != 0 {
		t.Fatalf("snapshot() = %+v, want empty (recordForward must not create untracked circuits)", snap)
	}
}

func TestRelayCircuitStateSnapshotEmptyInitially(t *testing.T) {
	s := newRelayCircuitState(1024)
	if snap := s.snapshot(); len(snap) != 0 {
		t.Fatalf("snapshot() = %+v, want empty", snap)
	}
}

func TestRelayCircuitStateSnapshotOmitsExpiredEntries(t *testing.T) {
	s := newRelayCircuitState(1)
	if _, ok := s.replayWindowFor(CircuitID(1)); !ok {
		t.Fatal("replayWindowFor(1) ok = false, want true")
	}
	s.recordForward(CircuitID(1), []byte("prev"), []byte("next"), 10)
	time.Sleep(5 * time.Millisecond)
	s.expireStale(time.Millisecond)

	if snap := s.snapshot(); len(snap) != 0 {
		t.Fatalf("snapshot() after expireStale = %+v, want empty", snap)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./src/garlic/... -run TestRelayCircuitStateRecordForward -run TestRelayCircuitStateSnapshot -v`
Expected: FAIL — `recordForward`/`snapshot` undefined.

- [ ] **Step 3: Refactor `relayCircuitState` to a single per-circuit info map and implement the new methods**

Replace the full contents of `src/garlic/relaystate.go`:

```go
package garlic

// relayCircuitState tracks everything a relay node keeps about a
// circuit it forwards traffic on (as opposed to Circuit/CircuitManager
// in circuit.go, which is the *originator's* view of a circuit it
// created): the per-circuit ReplayWindow, and - for dashboard
// visibility - the immediate previous/next hop and traffic counters. A
// relay never learns, and this never stores, anything beyond its own
// two neighbors on a circuit; see manager.go's dispatchAction, the only
// place recordForward is called from. The table is itself
// capacity-bounded - a new circuit ID is refused once at capacity,
// exactly like RateLimiter's tracked-peer bound - so a remote peer
// can't make a relay accumulate unbounded per-circuit state just by
// sending traffic for new circuit IDs.

import (
	"sync"
	"time"
)

type relayCircuitInfo struct {
	window         *ReplayWindow
	previousHop    []byte
	nextHop        []byte
	firstSeen      time.Time
	lastActive     time.Time
	packetsRelayed uint64
	bytesRelayed   uint64
}

// RelayCircuitInfo is a point-in-time, serializable snapshot of one
// relayed circuit's locally-known state - used by the getGarlicCircuits
// admin handler (Task 7). PreviousHop/NextHop are exactly what this
// node, as an intermediate hop, actually knows: never a fabricated
// full path.
type RelayCircuitInfo struct {
	ID             CircuitID
	PreviousHop    []byte
	NextHop        []byte
	FirstSeen      time.Time
	LastActive     time.Time
	PacketsRelayed uint64
	BytesRelayed   uint64
}

type relayCircuitState struct {
	mu       sync.Mutex
	max      int
	circuits map[CircuitID]*relayCircuitInfo
}

func newRelayCircuitState(max int) *relayCircuitState {
	return &relayCircuitState{
		max:      max,
		circuits: make(map[CircuitID]*relayCircuitInfo),
	}
}

// replayWindowFor returns the ReplayWindow to use for circuit id,
// creating one on first use. ok is false if the table is at capacity and
// id is not already tracked, meaning the caller should refuse to relay
// for this circuit rather than grow unboundedly.
func (s *relayCircuitState) replayWindowFor(id CircuitID) (w *ReplayWindow, ok bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if info, exists := s.circuits[id]; exists {
		info.lastActive = time.Now()
		return info.window, true
	}
	if len(s.circuits) >= s.max {
		return nil, false
	}
	now := time.Now()
	info := &relayCircuitInfo{
		window:     NewReplayWindow(),
		firstSeen:  now,
		lastActive: now,
	}
	s.circuits[id] = info
	return info.window, true
}

// recordForward records that this node forwarded n bytes for id,
// arriving from previousHop and sent on to nextHop. A no-op if id isn't
// already tracked (recordForward is only ever called after a successful
// processCircuitData -> replayWindowFor call for the same id, so this
// only guards against being called out of order).
func (s *relayCircuitState) recordForward(id CircuitID, previousHop, nextHop []byte, n int) {
	s.mu.Lock()
	defer s.mu.Unlock()

	info, ok := s.circuits[id]
	if !ok {
		return
	}
	info.previousHop = append([]byte(nil), previousHop...)
	info.nextHop = append([]byte(nil), nextHop...)
	info.packetsRelayed++
	info.bytesRelayed += uint64(n)
	info.lastActive = time.Now()
}

// snapshot returns a point-in-time copy of every currently-tracked
// relayed circuit.
func (s *relayCircuitState) snapshot() []RelayCircuitInfo {
	s.mu.Lock()
	defer s.mu.Unlock()

	out := make([]RelayCircuitInfo, 0, len(s.circuits))
	for id, info := range s.circuits {
		out = append(out, RelayCircuitInfo{
			ID:             id,
			PreviousHop:    append([]byte(nil), info.previousHop...),
			NextHop:        append([]byte(nil), info.nextHop...),
			FirstSeen:      info.firstSeen,
			LastActive:     info.lastActive,
			PacketsRelayed: info.packetsRelayed,
			BytesRelayed:   info.bytesRelayed,
		})
	}
	return out
}

// count returns the number of circuits currently tracked.
func (s *relayCircuitState) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.circuits)
}

// expireStale removes tracked circuits not touched within maxAge,
// returning how many were removed.
func (s *relayCircuitState) expireStale(maxAge time.Duration) int {
	cutoff := time.Now().Add(-maxAge)

	s.mu.Lock()
	defer s.mu.Unlock()

	var stale []CircuitID
	for id, info := range s.circuits {
		if info.lastActive.Before(cutoff) {
			stale = append(stale, id)
		}
	}
	for _, id := range stale {
		delete(s.circuits, id)
	}
	return len(stale)
}
```

This preserves `replayWindowFor(id) (*ReplayWindow, bool)`, `count() int`, and `expireStale(maxAge) int`'s exact existing signatures and behavior (the pre-existing `relaystate_test.go` tests from before this task must keep passing unmodified) while consolidating the old two parallel maps (`windows`, `touched`) into one `circuits map[CircuitID]*relayCircuitInfo`.

- [ ] **Step 4: Thread the previous-hop key through `manager.go`'s dispatch and call `recordForward`**

In `src/garlic/manager.go`, change `handleIncoming` and `dispatchAction`:

```go
func (g *Garlic) handleIncoming(from ed25519.PublicKey, data []byte) {
	if !g.limiter.Allow(from) {
		return
	}
	if len(data) == 0 {
		return
	}
	switch data[0] {
	case msgTypeCapabilityRequest:
		resp := append([]byte{msgTypeCapabilityResponse}, g.processCapabilityRequest()...)
		_, _ = g.core.WriteGarlic(resp, iwt.Addr(from))
	case msgTypeCapabilityResponse:
		g.handleCapabilityResponse(from, data[1:])
	case msgTypeCircuitData:
		g.dispatchAction(g.processCircuitData(data[1:]), from)
	case msgTypeAnnounce:
		g.processAnnounce(data[1:])
	case msgTypeCircuitDataBundle:
		for _, action := range g.processCircuitDataBundle(data[1:]) {
			g.dispatchAction(action, from)
		}
	}
}

// dispatchAction carries out a single circuitAction: deliver locally, or
// forward to the next hop. actionDrop is a no-op (nothing to do). from
// is the peer this data arrived from - recorded as the relayed
// circuit's previous hop when forwarding, never used or stored for any
// other action kind.
func (g *Garlic) dispatchAction(action circuitAction, from ed25519.PublicKey) {
	switch action.kind {
	case actionDeliver:
		select {
		case g.delivered <- DeliveredMessage{CircuitID: action.circuitID, Payload: action.payload}:
		default:
		}
	case actionForward:
		g.relayState.recordForward(action.circuitID, from, action.forwardTo, len(action.forwardMsg))
		g.sendCircuitData(action.forwardMsg, iwt.Addr(action.forwardTo))
	}
}
```

Note `processCircuitData` and `processCircuitDataBundle`'s own signatures are unchanged — `relay_logic_test.go`'s existing tests call `processCircuitData` directly and must keep compiling and passing unmodified.

Add the exported accessor, near `GetStats` later in the same file (for now, add it directly after `dispatchAction`):

```go
// RelayCircuits returns a snapshot of every circuit this node is
// currently relaying (i.e. is an intermediate hop for) - real, locally
// known previous/next hop and traffic data, never a fabricated full
// path. Used by the getGarlicCircuits admin handler.
func (g *Garlic) RelayCircuits() []RelayCircuitInfo {
	return g.relayState.snapshot()
}
```

- [ ] **Step 5: Run the full garlic package test suite**

Run: `go test ./src/garlic/...`
Expected: PASS — new relaystate tests green, all pre-existing relaystate/relay_logic/integration tests still green (they never depended on `relayCircuitState`'s internal storage shape, only its public methods).

- [ ] **Step 6: Commit**

```bash
git add src/garlic/relaystate.go src/garlic/relaystate_test.go src/garlic/manager.go
git commit -m "garlic: track previous/next hop and traffic per relayed circuit"
```

---

### Task 7: `getGarlicStats` traffic/security totals, new `getGarlicCircuits` handler

**Files:**
- Modify: `src/garlic/manager.go`
- Modify: `src/garlic/admin.go`
- Create: `src/garlic/admin_test.go`

**Interfaces:**
- Consumes: `Circuit.HopKeys/TrafficStats/IsClosed` (Task 3), `CircuitManager.List` (Task 4), `SecurityCounters.snapshot` (Task 5), `relayCircuitState.snapshot`/`Garlic.RelayCircuits` (Task 6).
- Produces: `func (g *Garlic) OriginatedCircuits() []*Circuit`, extended `Stats` struct, extended `getGarlicStats` admin response, new `getGarlicCircuits` admin response. Used directly by Task 9's dashboard build (via the admin socket) — nothing further in this Go plan depends on it.

- [ ] **Step 1: Write the failing tests for `GetStats`'s new fields**

Append to `src/garlic/manager_test.go`:

```go
func TestGetStatsIncludesTrafficAndSecurityTotals(t *testing.T) {
	g := newTestGarlic(t)
	stats := g.GetStats()
	if stats.OriginatedCircuits != 0 || stats.RelayedCircuits != 0 {
		t.Fatalf("stats = %+v, want zero circuit counts with nothing set up", stats)
	}
	if stats.OriginatedBytes != 0 || stats.RelayedBytes != 0 {
		t.Fatalf("stats = %+v, want zero traffic totals with nothing set up", stats)
	}
	if stats.Security != (SecurityCounterSnapshot{}) {
		t.Fatalf("stats.Security = %+v, want all zeros", stats.Security)
	}
}

func TestOriginatedCircuitsExposesCircuitManagerList(t *testing.T) {
	g := newTestGarlic(t)
	g.circuits = NewCircuitManager(CircuitManagerConfig{MaxCircuits: 10, MaxCircuitsPerPeer: 10})
	c, err := g.circuits.Add([]Hop{{NodeKey: []byte("peer-a"), Key: make([]byte, 32)}}, time.Minute, 100, 100000)
	if err != nil {
		t.Fatalf("Add returned error: %v", err)
	}
	if _, _, _, err := c.Seal([]byte("hi")); err != nil {
		t.Fatalf("Seal returned error: %v", err)
	}

	list := g.OriginatedCircuits()
	if len(list) != 1 || list[0].ID != c.ID {
		t.Fatalf("OriginatedCircuits() = %+v, want [circuit %d]", list, c.ID)
	}

	stats := g.GetStats()
	if stats.OriginatedCircuits != 1 {
		t.Fatalf("stats.OriginatedCircuits = %d, want 1", stats.OriginatedCircuits)
	}
	if stats.OriginatedPackets != 1 || stats.OriginatedBytes != 2 {
		t.Fatalf("stats.OriginatedPackets, OriginatedBytes = %d, %d, want 1, 2", stats.OriginatedPackets, stats.OriginatedBytes)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./src/garlic/... -run TestGetStatsIncludesTrafficAndSecurityTotals -run TestOriginatedCircuitsExposesCircuitManagerList -v`
Expected: FAIL — `stats.OriginatedBytes` etc. and `OriginatedCircuits()` don't exist yet.

- [ ] **Step 3: Extend `Stats` and `GetStats`, add `OriginatedCircuits`**

In `src/garlic/manager.go`, replace the existing `Stats`/`GetStats`:

```go
// Stats is a point-in-time summary of this node's Garlic circuit
// activity - live counts and cumulative traffic totals across
// currently-tracked circuits, plus the local-only security counters.
// Computed on demand from the same live circuit/relay tables GetStats
// always read - not a separately-maintained running total, so there's
// only one place this data can drift from reality.
type Stats struct {
	OriginatedCircuits int
	RelayedCircuits    int
	OriginatedPackets  uint64
	OriginatedBytes    uint64
	RelayedPackets     uint64
	RelayedBytes       uint64
	Security           SecurityCounterSnapshot
}

func (g *Garlic) GetStats() Stats {
	circuits := g.circuits.List()
	var origPackets, origBytes uint64
	for _, c := range circuits {
		p, b := c.TrafficStats()
		origPackets += p
		origBytes += b
	}

	relayed := g.relayState.snapshot()
	var relPackets, relBytes uint64
	for _, r := range relayed {
		relPackets += r.PacketsRelayed
		relBytes += r.BytesRelayed
	}

	return Stats{
		OriginatedCircuits: len(circuits),
		RelayedCircuits:    len(relayed),
		OriginatedPackets:  origPackets,
		OriginatedBytes:    origBytes,
		RelayedPackets:     relPackets,
		RelayedBytes:       relBytes,
		Security:           g.security.snapshot(),
	}
}

// OriginatedCircuits returns a snapshot of every circuit this node has
// originated (built itself, as opposed to relaying for someone else).
func (g *Garlic) OriginatedCircuits() []*Circuit {
	return g.circuits.List()
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./src/garlic/... -run TestGetStatsIncludesTrafficAndSecurityTotals -run TestOriginatedCircuitsExposesCircuitManagerList -v`
Expected: PASS.

- [ ] **Step 5: Extend `getGarlicStats` and add `getGarlicCircuits` in `admin.go`**

In `src/garlic/admin.go`, replace the existing `getGarlicStats` handler registration:

```go
	_ = a.AddHandler("getGarlicStats", "Show this node's current Garlic circuit counts, traffic totals, and security counters", []string{},
		func(in json.RawMessage) (interface{}, error) {
			stats := g.GetStats()
			return map[string]interface{}{
				"originatedCircuits": stats.OriginatedCircuits,
				"relayedCircuits":    stats.RelayedCircuits,
				"originatedPackets":  stats.OriginatedPackets,
				"originatedBytes":    stats.OriginatedBytes,
				"relayedPackets":     stats.RelayedPackets,
				"relayedBytes":       stats.RelayedBytes,
				"security": map[string]uint64{
					"replayDrops":      stats.Security.ReplayDrops,
					"malformedPackets": stats.Security.MalformedPackets,
					"expiredPackets":   stats.Security.ExpiredPackets,
					"authFailures":     stats.Security.AuthFailures,
					"relayTableFull":   stats.Security.RelayTableFull,
				},
			}, nil
		})
```

Add the new handler directly after it:

```go
	_ = a.AddHandler("getGarlicCircuits", "List this node's active originated and relayed Garlic circuits", []string{},
		func(in json.RawMessage) (interface{}, error) {
			originated := g.OriginatedCircuits()
			origOut := make([]map[string]interface{}, len(originated))
			for i, c := range originated {
				hops := c.HopKeys()
				hopStrs := make([]string, len(hops))
				for j, h := range hops {
					hopStrs[j] = hex.EncodeToString(h)
				}
				packets, bytes := c.TrafficStats()
				origOut[i] = map[string]interface{}{
					"circuitId": circuitIDToString(c.ID),
					"hops":      hopStrs,
					"closed":    c.IsClosed(),
					"createdAt": c.CreatedAt.UTC().Format(time.RFC3339),
					"expiresAt": c.ExpiresAt.UTC().Format(time.RFC3339),
					"packets":   packets,
					"bytes":     bytes,
				}
			}

			relayed := g.RelayCircuits()
			relOut := make([]map[string]interface{}, len(relayed))
			for i, r := range relayed {
				relOut[i] = map[string]interface{}{
					"circuitId":      circuitIDToString(r.ID),
					"previousHop":    hex.EncodeToString(r.PreviousHop),
					"nextHop":        hex.EncodeToString(r.NextHop),
					"firstSeen":      r.FirstSeen.UTC().Format(time.RFC3339),
					"lastActive":     r.LastActive.UTC().Format(time.RFC3339),
					"packetsRelayed": r.PacketsRelayed,
					"bytesRelayed":   r.BytesRelayed,
				}
			}

			return map[string]interface{}{"originated": origOut, "relayed": relOut}, nil
		})
```

Both handlers only reference `time`, `hex`, `json` — all already imported in `admin.go`.

- [ ] **Step 6: Write the wire-level "no secret leak" tests**

Create `src/garlic/admin_test.go`:

```go
package garlic_test

// Wire-level tests for the Garlic admin handlers the dashboard (Task 9
// of the yggdashboard v2 plan) consumes: verifies the exact JSON a
// client sees on the admin socket, not just Go-level return values -
// this is what actually reaches the browser-facing /api/* layer, so
// it's the right place to assert no private-key-shaped field ever
// appears.

import (
	"encoding/json"
	"io"
	"net"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gologme/log"

	"github.com/yggdrasil-network/yggdrasil-go/src/admin"
	"github.com/yggdrasil-network/yggdrasil-go/src/core"
	"github.com/yggdrasil-network/yggdrasil-go/src/garlic"
)

func newTestGarlicWithCore(t *testing.T) (*garlic.Garlic, *core.Core) {
	t.Helper()
	c := newLinkedTestNode(t)
	id, err := garlic.NewIdentity()
	if err != nil {
		t.Fatalf("NewIdentity returned error: %v", err)
	}
	g := garlic.New(c, id, garlic.DefaultConfig(), garlic.NewStaticRendezvous())
	t.Cleanup(g.Close)
	return g, c
}

// newTestAdminSocket wires a real admin.AdminSocket, listening on a
// temporary unix socket, with garlicInst's handlers registered - the
// same SetupAdminHandlers call cmd/yggdrasil/main.go makes.
func newTestAdminSocket(t *testing.T, c *core.Core, garlicInst *garlic.Garlic) string {
	t.Helper()
	sockPath := filepath.Join(t.TempDir(), "admin.sock")
	logger := log.New(io.Discard, "", 0)
	a, err := admin.New(c, logger, admin.ListenAddress("unix://"+sockPath))
	if err != nil {
		t.Fatalf("admin.New returned error: %v", err)
	}
	if a == nil {
		t.Fatal("admin.New returned a nil AdminSocket for a real unix listen address")
	}
	garlicInst.SetupAdminHandlers(a)
	return sockPath
}

// callAdmin sends one request to the admin socket at sockPath and
// returns the decoded "response" object.
func callAdmin(t *testing.T, sockPath, request string) map[string]interface{} {
	t.Helper()
	conn, err := net.Dial("unix", sockPath)
	if err != nil {
		t.Fatalf("net.Dial returned error: %v", err)
	}
	defer conn.Close()

	enc := json.NewEncoder(conn)
	if err := enc.Encode(map[string]interface{}{"request": request, "arguments": map[string]interface{}{}}); err != nil {
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

func TestGetGarlicStatsResponseShapeAndNoSecrets(t *testing.T) {
	g, c := newTestGarlicWithCore(t)
	sockPath := newTestAdminSocket(t, c, g)

	resp := callAdmin(t, sockPath, "getGarlicStats")
	for _, want := range []string{"originatedCircuits", "relayedCircuits", "originatedBytes", "relayedBytes", "security"} {
		if _, ok := resp[want]; !ok {
			t.Errorf("getGarlicStats response missing expected field %q, got %+v", want, resp)
		}
	}
	body, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("Marshal returned error: %v", err)
	}
	for _, forbidden := range []string{"privateKey", "PrivateKey", "secret", "Secret", "sessionKey", "aeadKey"} {
		if strings.Contains(string(body), forbidden) {
			t.Errorf("getGarlicStats response contains forbidden substring %q: %s", forbidden, body)
		}
	}
}

func TestGetGarlicCircuitsResponseShapeAndNoSecrets(t *testing.T) {
	g, c := newTestGarlicWithCore(t)
	sockPath := newTestAdminSocket(t, c, g)

	resp := callAdmin(t, sockPath, "getGarlicCircuits")
	for _, want := range []string{"originated", "relayed"} {
		if _, ok := resp[want]; !ok {
			t.Errorf("getGarlicCircuits response missing expected field %q, got %+v", want, resp)
		}
	}
	body, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("Marshal returned error: %v", err)
	}
	for _, forbidden := range []string{"privateKey", "PrivateKey", "secret", "Secret", "sessionKey", "aeadKey"} {
		if strings.Contains(string(body), forbidden) {
			t.Errorf("getGarlicCircuits response contains forbidden substring %q: %s", forbidden, body)
		}
	}
}

func TestGetGarlicIdentityOnlyExposesPublicKey(t *testing.T) {
	g, c := newTestGarlicWithCore(t)
	sockPath := newTestAdminSocket(t, c, g)

	resp := callAdmin(t, sockPath, "getGarlicIdentity")
	if _, ok := resp["publicKey"]; !ok {
		t.Error("getGarlicIdentity response missing publicKey field")
	}
	if _, ok := resp["privateKey"]; ok {
		t.Error("getGarlicIdentity response must never contain a privateKey field")
	}
}

func TestGetSelfResponseHasNoPrivateKeyField(t *testing.T) {
	c := newLinkedTestNode(t)
	sockPath := filepath.Join(t.TempDir(), "admin2.sock")
	logger := log.New(io.Discard, "", 0)
	a, err := admin.New(c, logger, admin.ListenAddress("unix://"+sockPath))
	if err != nil {
		t.Fatalf("admin.New returned error: %v", err)
	}
	a.SetupAdminHandlers()

	resp := callAdmin(t, sockPath, "getSelf")
	if _, ok := resp["uptime"]; !ok {
		t.Error("getSelf response missing uptime field")
	}
	body, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("Marshal returned error: %v", err)
	}
	if strings.Contains(string(body), "privateKey") || strings.Contains(string(body), "PrivateKey") {
		t.Errorf("getSelf response contains a private key field: %s", body)
	}
}
```

This file is `package garlic_test` (same external test package as `integration_test.go`), so it reuses `newLinkedTestNode(t) *core.Core` directly - no new test harness needed.

- [ ] **Step 7: Run the new tests**

Run: `go test ./src/garlic/... -run TestGetGarlicStatsResponseShapeAndNoSecrets -run TestGetGarlicCircuitsResponseShapeAndNoSecrets -run TestGetGarlicIdentityOnlyExposesPublicKey -run TestGetSelfResponseHasNoPrivateKeyField -v`
Expected: PASS, all 4 green.

- [ ] **Step 8: Run the full garlic and admin package test suites**

Run: `go test ./src/garlic/... ./src/admin/...`
Expected: PASS, no regressions.

- [ ] **Step 9: Commit**

```bash
git add src/garlic/manager.go src/garlic/admin.go src/garlic/admin_test.go src/garlic/manager_test.go
git commit -m "garlic: expose traffic/security totals and circuit listing over the admin socket"
```

---

### Task 8: Dashboard process supervisor package

**Files:**
- Create: `src/dashboard/dashboard.go`
- Create: `src/dashboard/dashboard_test.go`

**Interfaces:**
- Consumes: nothing from earlier tasks (standalone package).
- Produces: `type Config struct{Listen, Path, AdminListen string}`, `type Logger interface{...}`, `func Start(cfg Config, logger Logger) (*Process, error)`, `func (p *Process) Stop() error`. Used by Task 9's `cmd/yggdrasil/main.go`.

- [ ] **Step 1: Write the failing tests**

Create `src/dashboard/dashboard_test.go`:

```go
package dashboard

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

type fakeLogger struct{}

func (fakeLogger) Printf(format string, args ...interface{}) {}
func (fakeLogger) Warnln(args ...interface{})                {}
func (fakeLogger) Errorln(args ...interface{})               {}

func TestResolveEntryPointUsesConfiguredPath(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "index.js"), []byte("// fake"), 0o644); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}
	entry, err := resolveEntryPoint(dir)
	if err != nil {
		t.Fatalf("resolveEntryPoint returned error: %v", err)
	}
	want := filepath.Join(dir, "index.js")
	if entry != want {
		t.Fatalf("entry = %q, want %q", entry, want)
	}
}

func TestResolveEntryPointErrorsWhenNotFound(t *testing.T) {
	dir := t.TempDir() // empty, no index.js
	if _, err := resolveEntryPoint(dir); err == nil {
		t.Fatal("resolveEntryPoint returned nil error, want an error for a missing build")
	}
}

func TestSplitHostPort(t *testing.T) {
	host, port, err := splitHostPort("127.0.0.1:8080")
	if err != nil {
		t.Fatalf("splitHostPort returned error: %v", err)
	}
	if host != "127.0.0.1" || port != "8080" {
		t.Fatalf("host, port = %q, %q, want \"127.0.0.1\", \"8080\"", host, port)
	}
}

func TestSplitHostPortRejectsMissingColon(t *testing.T) {
	if _, _, err := splitHostPort("notahostport"); err == nil {
		t.Fatal("splitHostPort returned nil error, want an error")
	}
}

func TestStartRejectsDisabledAdminSocket(t *testing.T) {
	if _, err := Start(Config{Listen: "127.0.0.1:8080", AdminListen: "none"}, fakeLogger{}); err == nil {
		t.Fatal("Start returned nil error, want an error when AdminListen is \"none\"")
	}
}

func TestStartErrorsWhenNoDashboardBuildFound(t *testing.T) {
	empty := t.TempDir()
	cfg := Config{Listen: "127.0.0.1:8080", AdminListen: "unix:///tmp/test.sock", Path: empty}
	if _, err := Start(cfg, fakeLogger{}); err == nil {
		t.Fatal("Start returned nil error, want an error for a missing dashboard build")
	}
}

func TestStartAndStopSpawnsRealNodeProcess(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node not installed, skipping real-spawn test")
	}
	dir := t.TempDir()
	script := "process.stdout.write('dashboard test process running\\n'); setInterval(() => {}, 1000);"
	if err := os.WriteFile(filepath.Join(dir, "index.js"), []byte(script), 0o644); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}
	cfg := Config{Listen: "127.0.0.1:0", AdminListen: "unix:///tmp/test.sock", Path: dir}
	p, err := Start(cfg, fakeLogger{})
	if err != nil {
		t.Fatalf("Start returned error: %v", err)
	}
	if err := p.Stop(); err != nil {
		t.Fatalf("Stop returned error: %v", err)
	}
}

func TestStopIsSafeOnNilProcess(t *testing.T) {
	var p *Process
	if err := p.Stop(); err != nil {
		t.Fatalf("Stop on nil *Process returned error: %v", err)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./src/dashboard/... -v`
Expected: FAIL — package `dashboard` doesn't exist yet (build error).

- [ ] **Step 3: Implement the package**

Create `src/dashboard/dashboard.go`:

```go
// Package dashboard spawns and supervises the local operator
// dashboard's Node.js child process (a separately-built SvelteKit
// adapter-node app, see yggdashboard/) when configured. It never talks
// to the admin socket itself - it only starts the process that does,
// passing it the node's own AdminListen address as an environment
// variable so the dashboard needs no separate admin-socket
// configuration. A missing `node` binary or missing build output is
// always returned as an error for the caller to log as a warning - this
// package must never be the reason yggdrasil itself fails to start.
package dashboard

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// Config holds what Start needs to spawn the dashboard.
type Config struct {
	Listen      string // dashboard's own host:port
	Path        string // directory containing build/index.js; "" tries defaultPaths
	AdminListen string // the node's own admin socket address, reused as-is
}

// Logger is the minimal logging interface Start/Process need - already
// satisfied by *log.Logger (gologme/log), the logger used throughout
// cmd/yggdrasil/main.go.
type Logger interface {
	Printf(format string, args ...interface{})
	Warnln(args ...interface{})
	Errorln(args ...interface{})
}

// defaultPaths are conventional locations for the dashboard's built
// assets, tried in order when Config.Path is empty.
var defaultPaths = []string{
	"/usr/lib/yggdrasil/dashboard",
	"/usr/share/yggdrasil/dashboard",
	"./yggdashboard/build",
}

// resolveEntryPoint returns the path to a build/index.js under the
// configured directory, or - if configured is empty - the first
// defaultPaths entry that has one.
func resolveEntryPoint(configured string) (string, error) {
	candidates := defaultPaths
	if configured != "" {
		candidates = []string{configured}
	}
	for _, dir := range candidates {
		entry := filepath.Join(dir, "index.js")
		if info, err := os.Stat(entry); err == nil && !info.IsDir() {
			return entry, nil
		}
	}
	return "", fmt.Errorf("dashboard: no built dashboard found (tried %v) - run 'npm run build' in yggdashboard/ and set dashboard.path, or install it to a conventional location", candidates)
}

// splitHostPort splits a "host:port" listen address into its parts for
// the environment variables the dashboard process expects.
func splitHostPort(listen string) (host, port string, err error) {
	idx := bytes.LastIndexByte([]byte(listen), ':')
	if idx < 0 {
		return "", "", fmt.Errorf("dashboard: invalid listen address %q, want host:port", listen)
	}
	return listen[:idx], listen[idx+1:], nil
}

// Process supervises the dashboard's Node.js child process.
type Process struct {
	cmd *exec.Cmd
}

// Start validates cfg, resolves the dashboard's built entry point and
// the `node` binary, and spawns it. Every failure mode here is returned
// as an error rather than panicking - the caller (cmd/yggdrasil) must
// treat a failed Start as a warning, not a reason to stop the daemon.
func Start(cfg Config, logger Logger) (*Process, error) {
	if cfg.AdminListen == "" || cfg.AdminListen == "none" {
		return nil, fmt.Errorf("dashboard: AdminListen is disabled (\"none\") - the dashboard has nothing to poll")
	}
	host, port, err := splitHostPort(cfg.Listen)
	if err != nil {
		return nil, err
	}
	entry, err := resolveEntryPoint(cfg.Path)
	if err != nil {
		return nil, err
	}
	nodeBin, err := exec.LookPath("node")
	if err != nil {
		return nil, fmt.Errorf("dashboard: 'node' not found on PATH: %w", err)
	}

	cmd := exec.Command(nodeBin, entry)
	cmd.Env = append(os.Environ(),
		"ADMIN_SOCKET="+cfg.AdminListen,
		// HOST/PORT (not a custom name) - @sveltejs/adapter-node's
		// built server reads these itself when run directly as
		// `node build/index.js`; no custom server wrapper needed.
		"HOST="+host,
		"PORT="+port,
	)
	cmd.Stdout = &prefixWriter{logger: logger}
	cmd.Stderr = &prefixWriter{logger: logger}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("dashboard: failed to start: %w", err)
	}
	logger.Printf("Dashboard started (pid %d), listening on http://%s", cmd.Process.Pid, cfg.Listen)
	return &Process{cmd: cmd}, nil
}

// Stop terminates the dashboard child process. Safe to call on a nil
// *Process.
func (p *Process) Stop() error {
	if p == nil || p.cmd == nil || p.cmd.Process == nil {
		return nil
	}
	return p.cmd.Process.Kill()
}

// prefixWriter forwards each line written to it to logger.Printf,
// prefixed so the dashboard child process's output is visibly distinct
// from yggdrasil's own log lines in combined output (journald, log
// files).
type prefixWriter struct {
	logger Logger
	buf    []byte
}

func (w *prefixWriter) Write(p []byte) (int, error) {
	w.buf = append(w.buf, p...)
	for {
		i := bytes.IndexByte(w.buf, '\n')
		if i < 0 {
			break
		}
		w.logger.Printf("dashboard: %s", string(w.buf[:i]))
		w.buf = w.buf[i+1:]
	}
	return len(p), nil
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./src/dashboard/... -v`
Expected: PASS. `TestStartAndStopSpawnsRealNodeProcess` skips with a clear message if `node` isn't installed in this environment — that's expected, not a failure.

- [ ] **Step 5: Commit**

```bash
git add src/dashboard/dashboard.go src/dashboard/dashboard_test.go
git commit -m "dashboard: add Node.js dashboard process supervisor package"
```

---

### Task 9: Wire the dashboard into `cmd/yggdrasil/main.go`

**Files:**
- Modify: `cmd/yggdrasil/main.go`

**Interfaces:**
- Consumes: `config.DashboardConfig` (Task 1), `dashboard.Config`/`Start`/`Process.Stop` (Task 8).
- Produces: a running (or gracefully-absent) dashboard child process as part of the normal `yggdrasil` daemon lifecycle. Nothing further in this Go plan depends on it — Task 9 is the last backend task.

- [ ] **Step 1: Add the import and the `node` struct field**

In `cmd/yggdrasil/main.go`, add the import:

```go
	"github.com/yggdrasil-network/yggdrasil-go/src/core"
	"github.com/yggdrasil-network/yggdrasil-go/src/dashboard"
	"github.com/yggdrasil-network/yggdrasil-go/src/multicast"
```

(Insert `dashboard` alphabetically between `core` and `multicast` in the existing import block.)

Add the field to the `node` struct:

```go
type node struct {
	core      *core.Core
	tun       *tun.TunAdapter
	multicast *multicast.Multicast
	admin     *admin.AdminSocket
	garlic    *garlic.Garlic
	dashboard *dashboard.Process
}
```

- [ ] **Step 2: Add the dashboard setup block**

Insert this block directly after the existing Garlic setup block (which ends with its closing `}` right before the `//Windows service shutdown` comment):

```go
	// Set up the local operator dashboard (optional, disabled by
	// default). A failure here is always a warning, never fatal - the
	// dashboard must never be the reason yggdrasil itself won't start.
	{
		if cfg.Dashboard.Enabled {
			dcfg := dashboard.Config{
				Listen:      cfg.Dashboard.Listen,
				Path:        cfg.Dashboard.Path,
				AdminListen: cfg.AdminListen,
			}
			if n.dashboard, err = dashboard.Start(dcfg, logger); err != nil {
				logger.Warnln("Dashboard not started:", err)
			}
		}
	}

```

- [ ] **Step 3: Extend the pledge promises when the dashboard is enabled**

Find the existing `promises` block (right before `protect.Pledge(...)`):

```go
	promises := []string{"stdio", "rpath", "cpath", "inet", "unix", "dns"}
	if len(cfg.MulticastInterfaces) > 0 {
		promises = append(promises, "mcast")
	}
```

Add a dashboard-conditional append right after the `mcast` one:

```go
	promises := []string{"stdio", "rpath", "cpath", "inet", "unix", "dns"}
	if len(cfg.MulticastInterfaces) > 0 {
		promises = append(promises, "mcast")
	}
	if cfg.Dashboard.Enabled {
		// Only relevant on OpenBSD, where protect.Pledge actually
		// enforces this - "proc" is needed to signal/wait on the
		// already-spawned dashboard child process at shutdown. The
		// exec() itself already happened above, before this pledge
		// call, so "exec" doesn't need to be a standing promise.
		promises = append(promises, "proc")
	}
```

- [ ] **Step 4: Stop the dashboard on shutdown**

Find the shutdown sequence:

```go
	// Shut down the node.
	if n.garlic != nil {
		n.garlic.Close()
	}
	_ = n.admin.Stop()
	_ = n.multicast.Stop()
	_ = n.tun.Stop()
	n.core.Stop()
```

Add the dashboard stop first:

```go
	// Shut down the node.
	if n.dashboard != nil {
		_ = n.dashboard.Stop()
	}
	if n.garlic != nil {
		n.garlic.Close()
	}
	_ = n.admin.Stop()
	_ = n.multicast.Stop()
	_ = n.tun.Stop()
	n.core.Stop()
```

- [ ] **Step 5: Build**

Run: `go build ./...`
Expected: builds cleanly, no errors.

- [ ] **Step 6: Run the full Go test suite**

Run: `go test ./...`
Expected: PASS across every package, no regressions from any earlier task.

- [ ] **Step 7: Manually verify dashboard-disabled behavior is unchanged**

```bash
go build -o /tmp/yggdashboard-verify/yggdrasil ./cmd/yggdrasil
/tmp/yggdashboard-verify/yggdrasil -genconf > /tmp/yggdashboard-verify/yggdrasil.conf
/tmp/yggdashboard-verify/yggdrasil -useconffile /tmp/yggdashboard-verify/yggdrasil.conf &
sleep 2
# No process should be listening on the default dashboard port when Dashboard.Enabled defaults to false:
(ss -ltnp 2>/dev/null || netstat -ltnp 2>/dev/null) | grep ':8080' && echo "FAIL: something is listening on 8080" || echo "OK: nothing listening on 8080 by default"
kill %1
```

Expected: prints `OK: nothing listening on 8080 by default`.

- [ ] **Step 8: Manually verify dashboard-enabled behavior binds loopback-only and degrades gracefully without a build**

```bash
sed -i 's/"Dashboard": {\n\?\s*"Enabled": false/"Dashboard": {"Enabled": true/' /tmp/yggdashboard-verify/yggdrasil.conf 2>/dev/null || \
  python3 -c "
import json
with open('/tmp/yggdashboard-verify/yggdrasil.conf') as f:
    cfg = json.load(f)
cfg['Dashboard']['Enabled'] = True
with open('/tmp/yggdashboard-verify/yggdrasil.conf', 'w') as f:
    json.dump(cfg, f)
"
/tmp/yggdashboard-verify/yggdrasil -useconffile /tmp/yggdashboard-verify/yggdrasil.conf 2>&1 | grep -i "dashboard not started" &
sleep 2
kill %1 2>/dev/null
```

Expected: the log line `Dashboard not started: dashboard: no built dashboard found ...` appears (since `yggdashboard/build` doesn't exist until Part B is built) — proving yggdrasil itself kept running rather than crashing. This step gets re-verified for real, with a real build, in Task 23.

- [ ] **Step 9: Commit**

```bash
git add cmd/yggdrasil/main.go
git commit -m "cmd/yggdrasil: spawn the dashboard subprocess when enabled"
```

---

**Part A (Go backend) complete at this point.** `go test ./...` is green, `dashboard.enabled: false` (the default) leaves yggdrasil's behavior completely unchanged, and `dashboard.enabled: true` degrades gracefully until Part B produces something to spawn.

---

## Part B — Frontend (`yggdashboard/`)

A note on structure that deviates slightly from the design spec's indicative layout: server-only code (the admin-socket client, poller, anything touching `ADMIN_SOCKET`) lives under `src/lib/server/`, not a project-root `server/` directory. SvelteKit enforces at build time that anything under `src/lib/server/` can never be imported from client-side code — that's a real, compiler-checked security boundary for "the browser must never receive secrets," strictly better than relying on discipline alone. `@sveltejs/adapter-node`'s built `build/index.js` also already reads `PORT`/`HOST` itself when run directly, so there's no custom server entrypoint or WebSocket-attach wiring to write at all — simpler than the superseded design.

### Task 10: Project scaffold

**Files:**
- Create: `yggdashboard/package.json`
- Create: `yggdashboard/svelte.config.js`
- Create: `yggdashboard/vite.config.ts`
- Create: `yggdashboard/tsconfig.json`
- Create: `yggdashboard/src/app.html`
- Create: `yggdashboard/src/app.d.ts`
- Create: `yggdashboard/src/routes/+layout.svelte`
- Create: `yggdashboard/src/routes/+page.svelte`
- Create: `yggdashboard/.gitignore`

**Interfaces:**
- Consumes: nothing (first frontend task).
- Produces: a working `npm run dev` / `npm run build` / `npm start` SvelteKit project, ready for later tasks to add `src/lib/server/` modules and real routes.

- [ ] **Step 1: Create `yggdashboard/package.json`**

```json
{
  "name": "yggdashboard",
  "version": "0.2.0",
  "private": true,
  "type": "module",
  "scripts": {
    "dev": "vite dev",
    "build": "vite build",
    "start": "node build/index.js",
    "test": "vitest run",
    "check": "svelte-kit sync && svelte-check --tsconfig ./tsconfig.json"
  },
  "devDependencies": {
    "@sveltejs/adapter-node": "^5.2.0",
    "@sveltejs/kit": "^2.9.0",
    "@sveltejs/vite-plugin-svelte": "^5.0.0",
    "@types/node": "^22.10.0",
    "svelte": "^5.16.0",
    "svelte-check": "^4.1.0",
    "typescript": "^5.7.0",
    "vite": "^6.0.0",
    "vitest": "^3.0.0"
  }
}
```

- [ ] **Step 2: Create `yggdashboard/svelte.config.js`**

```js
import adapter from '@sveltejs/adapter-node';
import { vitePreprocess } from '@sveltejs/vite-plugin-svelte';

export default {
  preprocess: vitePreprocess(),
  kit: {
    adapter: adapter()
  }
};
```

- [ ] **Step 3: Create `yggdashboard/vite.config.ts`**

```ts
import { sveltekit } from '@sveltejs/kit/vite';
import { defineConfig } from 'vite';

export default defineConfig({
  plugins: [sveltekit()],
  test: {
    include: ['src/**/*.test.ts']
  }
});
```

- [ ] **Step 4: Create `yggdashboard/tsconfig.json`**

```json
{
  "extends": "./.svelte-kit/tsconfig.json",
  "compilerOptions": {
    "allowJs": true,
    "checkJs": true,
    "esModuleInterop": true,
    "forceConsistentCasingInFileNames": true,
    "resolveJsonModule": true,
    "skipLibCheck": true,
    "sourceMap": true,
    "strict": true,
    "moduleResolution": "bundler"
  }
}
```

- [ ] **Step 5: Create `yggdashboard/src/app.html`**

```html
<!doctype html>
<html lang="en">
  <head>
    <meta charset="utf-8" />
    <meta name="viewport" content="width=device-width, initial-scale=1" />
    %sveltekit.head%
  </head>
  <body data-sveltekit-preload-data="hover">
    <div style="display: contents">%sveltekit.body%</div>
  </body>
</html>
```

- [ ] **Step 6: Create `yggdashboard/src/app.d.ts`**

```ts
declare global {
  namespace App {}
}

export {};
```

- [ ] **Step 7: Create `yggdashboard/src/routes/+layout.svelte`**

```svelte
<script lang="ts">
  let { children } = $props();
</script>

{@render children()}
```

- [ ] **Step 8: Create `yggdashboard/src/routes/+page.svelte`** (placeholder, replaced in Task 17)

```svelte
<h1>yggdashboard</h1>
<p>Scaffold OK.</p>
```

- [ ] **Step 9: Create `yggdashboard/.gitignore`**

```
node_modules
/build
/.svelte-kit
/package
.env
.env.*
!.env.example
vite.config.ts.timestamp-*
vite.config.js.timestamp-*
```

- [ ] **Step 10: Install dependencies and verify the dev server boots**

Run:
```bash
cd yggdashboard && npm install
```
Expected: installs without error, creates `node_modules/` and `package-lock.json`.

Run:
```bash
cd yggdashboard && npm run dev -- --port 5173 &
sleep 3
curl -s http://localhost:5173 | grep -q "yggdashboard" && echo SCAFFOLD_OK
kill %1
```
Expected: prints `SCAFFOLD_OK`.

- [ ] **Step 11: Commit**

```bash
git add yggdashboard/package.json yggdashboard/svelte.config.js yggdashboard/vite.config.ts \
  yggdashboard/tsconfig.json yggdashboard/src/app.html yggdashboard/src/app.d.ts \
  yggdashboard/src/routes/+layout.svelte yggdashboard/src/routes/+page.svelte \
  yggdashboard/.gitignore yggdashboard/package-lock.json
git commit -m "yggdashboard: scaffold SvelteKit 5 project"
```

---

### Task 11: Admin-socket protocol client

**Files:**
- Create: `yggdashboard/src/lib/server/json-stream.ts`
- Test: `yggdashboard/src/lib/server/json-stream.test.ts`
- Create: `yggdashboard/src/lib/server/admin-client.ts`
- Test: `yggdashboard/src/lib/server/admin-client.test.ts`

**Interfaces:**
- Consumes: nothing.
- Produces:
  - `extractJSONValues(buffer: string): { values: unknown[]; rest: string }`.
  - `parseAdminAddress(address: string): { path: string } | { host: string; port: number }`
  - `class AdminClient { constructor(address: string); request<T = unknown>(name: string, args?: Record<string, unknown>): Promise<T>; close(): void }`
  - Used by Task 13's `Poller`.

This is a direct, unmodified port of the already-tested, architecture-agnostic admin-socket protocol client — polling vs. push doesn't change how the wire protocol itself is framed or pipelined, only how results reach the browser (a later task's concern, not this one's).

- [ ] **Step 1: Write the failing tests for the JSON stream extractor**

Create `yggdashboard/src/lib/server/json-stream.test.ts`:

```ts
import { describe, it, expect } from 'vitest';
import { extractJSONValues } from './json-stream';

describe('extractJSONValues', () => {
  it('returns nothing for an empty buffer', () => {
    expect(extractJSONValues('')).toEqual({ values: [], rest: '' });
  });

  it('extracts a single complete JSON object', () => {
    const result = extractJSONValues('{"a":1}');
    expect(result).toEqual({ values: [{ a: 1 }], rest: '' });
  });

  it('leaves a partial JSON object in rest, extracting nothing', () => {
    const result = extractJSONValues('{"a":1,"b":');
    expect(result.values).toEqual([]);
    expect(result.rest).toBe('{"a":1,"b":');
  });

  it('extracts multiple values concatenated with no separator (simulates two Encode() calls arriving in one TCP read)', () => {
    const result = extractJSONValues('{"a":1}{"b":2}');
    expect(result).toEqual({ values: [{ a: 1 }, { b: 2 }], rest: '' });
  });

  it("extracts multiple values separated by newlines (matches encoding/json.Encoder's trailing newline)", () => {
    const result = extractJSONValues('{"a":1}\n{"b":2}\n');
    expect(result).toEqual({ values: [{ a: 1 }, { b: 2 }], rest: '' });
  });

  it('extracts complete values and leaves a trailing partial one in rest', () => {
    const result = extractJSONValues('{"a":1}{"b":2');
    expect(result.values).toEqual([{ a: 1 }]);
    expect(result.rest).toBe('{"b":2');
  });

  it('does not miscount braces that appear inside a JSON string', () => {
    const result = extractJSONValues('{"a":"} { not real braces"}');
    expect(result).toEqual({ values: [{ a: '} { not real braces' }], rest: '' });
  });

  it('handles an escaped quote inside a string without ending the string early', () => {
    const result = extractJSONValues('{"a":"quote: \\" still inside"}{"b":2}');
    expect(result).toEqual({
      values: [{ a: 'quote: " still inside' }, { b: 2 }],
      rest: ''
    });
  });

  it('handles nested objects and arrays', () => {
    const result = extractJSONValues('{"a":{"nested":[1,2,{"deep":true}]}}');
    expect(result).toEqual({
      values: [{ a: { nested: [1, 2, { deep: true }] } }],
      rest: ''
    });
  });
});
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `cd yggdashboard && npx vitest run src/lib/server/json-stream.test.ts`
Expected: FAIL — `Cannot find module './json-stream'`.

- [ ] **Step 3: Implement `yggdashboard/src/lib/server/json-stream.ts`**

```ts
/**
 * Extracts every complete top-level JSON value from the front of buffer,
 * in order. The admin socket protocol (src/admin/admin.go) has no length
 * prefix or delimiter - encoding/json's Decoder/Encoder just write and
 * read back-to-back JSON values on the raw stream - so a client has to
 * track object/array depth itself (respecting strings and escapes) to
 * find where one value ends and the next begins. Any trailing bytes that
 * don't yet form a complete value are returned as `rest`, to be
 * prepended to the next chunk read from the socket.
 */
export function extractJSONValues(buffer: string): { values: unknown[]; rest: string } {
  const values: unknown[] = [];
  let i = 0;

  while (i < buffer.length) {
    while (i < buffer.length && /\s/.test(buffer[i])) i++;
    if (i >= buffer.length) break;

    const start = i;
    let depth = 0;
    let inString = false;
    let escaped = false;
    let end = -1;

    for (; i < buffer.length; i++) {
      const ch = buffer[i];
      if (inString) {
        if (escaped) {
          escaped = false;
        } else if (ch === '\\') {
          escaped = true;
        } else if (ch === '"') {
          inString = false;
        }
        continue;
      }
      if (ch === '"') {
        inString = true;
      } else if (ch === '{' || ch === '[') {
        depth++;
      } else if (ch === '}' || ch === ']') {
        depth--;
        if (depth === 0) {
          end = i + 1;
          i++;
          break;
        }
      }
    }

    if (end === -1) {
      return { values, rest: buffer.slice(start) };
    }
    values.push(JSON.parse(buffer.slice(start, end)));
  }

  return { values, rest: '' };
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `cd yggdashboard && npx vitest run src/lib/server/json-stream.test.ts`
Expected: PASS, all 9 tests green.

- [ ] **Step 5: Write the failing tests for the admin-socket client**

Create `yggdashboard/src/lib/server/admin-client.test.ts`:

```ts
import { describe, it, expect, vi, beforeEach } from 'vitest';
import { EventEmitter } from 'node:events';

class FakeSocket extends EventEmitter {
  written: string[] = [];
  destroyed = false;
  write(data: string) {
    this.written.push(data);
    return true;
  }
  destroy() {
    this.destroyed = true;
    this.emit('close');
  }
}

let lastSocket: FakeSocket | null = null;
const createConnectionMock = vi.fn(() => {
  const socket = new FakeSocket();
  lastSocket = socket;
  queueMicrotask(() => socket.emit('connect'));
  return socket;
});

vi.mock('node:net', () => ({
  default: {
    createConnection: (...args: unknown[]) => createConnectionMock(...args)
  }
}));

const { AdminClient, parseAdminAddress } = await import('./admin-client');

function respond(socket: FakeSocket, payload: unknown) {
  socket.emit('data', Buffer.from(JSON.stringify(payload) + '\n'));
}

describe('parseAdminAddress', () => {
  it('parses a unix:// address into a path', () => {
    expect(parseAdminAddress('unix:///var/run/yggdrasil/yggdrasil.sock')).toEqual({
      path: '/var/run/yggdrasil/yggdrasil.sock'
    });
  });

  it('parses a tcp:// address into host and port', () => {
    expect(parseAdminAddress('tcp://127.0.0.1:9001')).toEqual({
      host: '127.0.0.1',
      port: 9001
    });
  });

  it('throws on an unsupported scheme', () => {
    expect(() => parseAdminAddress('http://127.0.0.1:9001')).toThrow();
  });
});

describe('AdminClient', () => {
  beforeEach(() => {
    lastSocket = null;
    createConnectionMock.mockClear();
  });

  it('sends a request and resolves with the response payload on success', async () => {
    const client = new AdminClient('tcp://127.0.0.1:9001');
    const pending = client.request<{ key: string }>('getSelf');
    await vi.waitUntil(() => lastSocket !== null && lastSocket.written.length > 0);
    const sent = JSON.parse(lastSocket!.written[0]);
    expect(sent).toEqual({ request: 'getSelf', arguments: {}, keepalive: true });

    respond(lastSocket!, { status: 'success', response: { key: 'abc' } });
    await expect(pending).resolves.toEqual({ key: 'abc' });
  });

  it('rejects when the admin socket reports an error status', async () => {
    const client = new AdminClient('tcp://127.0.0.1:9001');
    const pending = client.request('createGarlicCircuit', { hops: 'deadbeef' });
    await vi.waitUntil(() => lastSocket !== null && lastSocket.written.length > 0);
    respond(lastSocket!, { status: 'error', error: 'circuit not found' });
    await expect(pending).rejects.toThrow('circuit not found');
  });

  it('reuses one connection across multiple sequential requests (keepalive)', async () => {
    const client = new AdminClient('tcp://127.0.0.1:9001');
    const first = client.request('getSelf');
    await vi.waitUntil(() => lastSocket !== null && lastSocket.written.length > 0);
    respond(lastSocket!, { status: 'success', response: { a: 1 } });
    await first;

    const second = client.request('getPeers');
    await vi.waitUntil(() => lastSocket!.written.length > 1);
    respond(lastSocket!, { status: 'success', response: { peers: [] } });
    await second;

    expect(createConnectionMock).toHaveBeenCalledTimes(1);
  });

  it('correlates pipelined requests to responses in FIFO order, even when both responses arrive in one data event', async () => {
    const client = new AdminClient('tcp://127.0.0.1:9001');
    const first = client.request<{ n: number }>('getSelf');
    const second = client.request<{ n: number }>('getPeers');
    await vi.waitUntil(() => lastSocket !== null && lastSocket.written.length === 2);

    lastSocket!.emit(
      'data',
      Buffer.from(
        JSON.stringify({ status: 'success', response: { n: 1 } }) +
          JSON.stringify({ status: 'success', response: { n: 2 } })
      )
    );

    await expect(first).resolves.toEqual({ n: 1 });
    await expect(second).resolves.toEqual({ n: 2 });
  });

  it('rejects in-flight requests and clears the connection when the socket closes', async () => {
    const client = new AdminClient('tcp://127.0.0.1:9001');
    const pending = client.request('getSelf');
    await vi.waitUntil(() => lastSocket !== null && lastSocket.written.length > 0);

    lastSocket!.emit('close');
    await expect(pending).rejects.toThrow();
  });

  it('reconnects (opens a new socket) after a prior connection has closed', async () => {
    const client = new AdminClient('tcp://127.0.0.1:9001');
    const first = client.request('getSelf');
    await vi.waitUntil(() => lastSocket !== null);
    lastSocket!.emit('close');
    await expect(first).rejects.toThrow();

    const second = client.request('getSelf');
    await vi.waitUntil(() => createConnectionMock.mock.calls.length === 2);
    respond(lastSocket!, { status: 'success', response: { ok: true } });
    await expect(second).resolves.toEqual({ ok: true });
  });
});
```

- [ ] **Step 6: Run the tests to verify they fail**

Run: `cd yggdashboard && npx vitest run src/lib/server/admin-client.test.ts`
Expected: FAIL — `Cannot find module './admin-client'`.

- [ ] **Step 7: Implement `yggdashboard/src/lib/server/admin-client.ts`**

```ts
import net from 'node:net';
import { extractJSONValues } from './json-stream';

export interface AdminResponse<T = unknown> {
  status: 'success' | 'error';
  error?: string;
  request?: { request: string; arguments?: unknown; keepalive?: boolean };
  response: T;
}

interface PendingRequest {
  resolve: (value: AdminResponse) => void;
  reject: (err: Error) => void;
}

/**
 * Parses an admin socket address using the same unix://path or
 * tcp://host:port convention Yggdrasil's own AdminListen config uses.
 */
export function parseAdminAddress(address: string): { path: string } | { host: string; port: number } {
  const url = new URL(address);
  if (url.protocol === 'unix:') {
    return { path: url.pathname };
  }
  if (url.protocol === 'tcp:') {
    return { host: url.hostname, port: Number(url.port) };
  }
  throw new Error(`unsupported admin socket address: ${address}`);
}

/**
 * Client for Yggdrasil's admin socket protocol (src/admin/admin.go).
 * Holds one persistent connection (keepalive: true on every request) and
 * pipelines requests over it - multiple requests may be in flight at
 * once; responses are matched to requests strictly in the order they
 * were sent, which is safe because the Go server processes requests on
 * one connection sequentially, one at a time, so responses are written
 * back in the same order requests were read.
 */
export class AdminClient {
  private address: string;
  private socket: net.Socket | null = null;
  private connecting: Promise<net.Socket> | null = null;
  private buffer = '';
  private queue: PendingRequest[] = [];

  constructor(address: string) {
    this.address = address;
  }

  private connect(): Promise<net.Socket> {
    if (this.socket && !this.socket.destroyed) {
      return Promise.resolve(this.socket);
    }
    if (this.connecting) {
      return this.connecting;
    }
    this.connecting = new Promise<net.Socket>((resolve, reject) => {
      const opts = parseAdminAddress(this.address);
      const socket = 'path' in opts ? net.createConnection({ path: opts.path }) : net.createConnection(opts);

      const onConnect = () => {
        this.socket = socket;
        this.connecting = null;
        resolve(socket);
      };
      const onError = (err: Error) => {
        this.connecting = null;
        reject(err);
      };
      socket.once('connect', onConnect);
      socket.once('error', onError);
      socket.on('data', (chunk: Buffer) => this.onData(chunk));
      socket.on('close', () => this.onClose());
    });
    return this.connecting;
  }

  private onData(chunk: Buffer): void {
    this.buffer += chunk.toString('utf8');
    const { values, rest } = extractJSONValues(this.buffer);
    this.buffer = rest;
    for (const value of values) {
      const pending = this.queue.shift();
      pending?.resolve(value as AdminResponse);
    }
  }

  private onClose(): void {
    this.socket = null;
    const pending = this.queue.splice(0);
    for (const p of pending) {
      p.reject(new Error('admin socket connection closed'));
    }
  }

  async request<T = unknown>(name: string, args?: Record<string, unknown>): Promise<T> {
    const socket = await this.connect();
    const payload = { request: name, arguments: args ?? {}, keepalive: true };
    const result = await new Promise<AdminResponse>((resolve, reject) => {
      this.queue.push({ resolve, reject });
      socket.write(JSON.stringify(payload) + '\n');
    });
    if (result.status !== 'success') {
      throw new Error(result.error || `admin request '${name}' failed`);
    }
    return result.response as T;
  }

  close(): void {
    this.socket?.destroy();
    this.socket = null;
  }
}
```

- [ ] **Step 8: Run the tests to verify they pass**

Run: `cd yggdashboard && npx vitest run src/lib/server/admin-client.test.ts`
Expected: PASS, all 9 tests green.

- [ ] **Step 9: Commit**

```bash
git add yggdashboard/src/lib/server/json-stream.ts yggdashboard/src/lib/server/json-stream.test.ts \
  yggdashboard/src/lib/server/admin-client.ts yggdashboard/src/lib/server/admin-client.test.ts
git commit -m "yggdashboard: add admin socket protocol client (JSON framing, keepalive, reconnect)"
```

---

### Task 12: Wire types and config

**Files:**
- Create: `yggdashboard/src/lib/server/types.ts`
- Create: `yggdashboard/src/lib/server/config.ts`
- Test: `yggdashboard/src/lib/server/config.test.ts`

**Interfaces:**
- Consumes: nothing.
- Produces: every wire type the poller/API layer needs, plus `loadConfig(): DashboardConfig`. Used by Task 13 (`Poller`) and Task 14 (`+server.ts` routes).

A field-name note verified directly against the Go source (not assumed): `PeerEntry`'s byte/rate fields use Go's `DataUnit` (a plain `uint64` with no custom JSON marshaling — it serializes as a plain number, its `String()` method is only for `yggdrasilctl`'s own text output). `Latency`/`LastErrorTime` are `time.Duration`, which also has no custom JSON marshaling — plain nanosecond integers. Also: **when `Garlic.Enabled` is false, `cmd/yggdrasil/main.go` never registers any `getGarlic*` admin handler at all** (see the `if cfg.Garlic.Enabled { ... n.garlic.SetupAdminHandlers(...) }` block) - calling one returns an admin "error" status (unknown command), not zero-valued JSON. The superseded spec assumed the handlers always exist and return zeros; Task 13's poller is written to catch that error and synthesize the zeroed/disabled shape itself, which is what actually makes "show the Garlic panel with zeros rather than hiding it" true in practice.

- [ ] **Step 1: Create `yggdashboard/src/lib/server/types.ts`** (no test — plain type declarations and constants, nothing to assert against beyond what Task 13's poller tests already cover)

```ts
/**
 * Wire types for Yggdrasil's admin socket responses. Field names and
 * optionality mirror the Go structs exactly - see:
 * - SelfInfo: src/admin/getself.go GetSelfResponse
 * - PeerEntry: src/admin/getpeers.go PeerEntry
 * - SessionEntry: src/admin/getsessions.go SessionEntry
 * - TreeEntry: src/admin/gettree.go TreeEntry
 * - PathEntry: src/admin/getpaths.go PathEntry
 * - Garlic*: src/garlic/admin.go's handlers - only registered at all
 *   when Garlic.Enabled is true, see the note above.
 */

export interface SelfInfo {
  build_name: string;
  build_version: string;
  key: string;
  address: string;
  subnet: string;
  routing_entries: number;
  /** Seconds since the node process started. */
  uptime: number;
}

export interface PeerEntry {
  remote?: string;
  up: boolean;
  inbound: boolean;
  address?: string;
  key: string;
  port: number;
  priority: number;
  cost: number;
  bytes_recvd?: number;
  bytes_sent?: number;
  rate_recvd?: number;
  rate_sent?: number;
  /** Seconds. */
  uptime?: number;
  /** Nanoseconds (Go time.Duration, plain number over JSON). */
  latency?: number;
  /** Nanoseconds elapsed since the last error - not a timestamp. */
  last_error_time?: number;
  last_error?: string;
}

export interface SessionEntry {
  address: string;
  key: string;
  bytes_recvd: number;
  bytes_sent: number;
  uptime: number;
}

export interface TreeEntry {
  address: string;
  key: string;
  parent: string;
  sequence: number;
}

export interface PathEntry {
  address: string;
  key: string;
  path: number[];
  sequence: number;
}

export interface GarlicCircuitOriginated {
  circuitId: string;
  hops: string[];
  closed: boolean;
  /** RFC3339. */
  createdAt: string;
  expiresAt: string;
  packets: number;
  bytes: number;
}

export interface GarlicCircuitRelayed {
  circuitId: string;
  previousHop: string;
  nextHop: string;
  firstSeen: string;
  lastActive: string;
  packetsRelayed: number;
  bytesRelayed: number;
}

export interface GarlicCircuits {
  originated: GarlicCircuitOriginated[];
  relayed: GarlicCircuitRelayed[];
}

export interface GarlicSecurityCounters {
  replayDrops: number;
  malformedPackets: number;
  expiredPackets: number;
  authFailures: number;
  relayTableFull: number;
}

export interface GarlicStats {
  originatedCircuits: number;
  relayedCircuits: number;
  originatedPackets: number;
  originatedBytes: number;
  relayedPackets: number;
  relayedBytes: number;
  security: GarlicSecurityCounters;
}

export interface GarlicIdentity {
  publicKey: string;
}

export interface GarlicKnownPeer {
  nodeKey: string;
  garlicPublicKey: string;
  lastSeen: string;
}

/**
 * The dashboard's own view of Garlic: `enabled` is explicit (derived by
 * the poller from whether the getGarlic* admin calls succeed at all),
 * rather than inferred from all-zero fields - matches the top-level
 * status bar's "Garlic: Enabled/Disabled" requirement directly.
 */
export interface GarlicSnapshot {
  enabled: boolean;
  identity: GarlicIdentity | null;
  stats: GarlicStats;
  circuits: GarlicCircuits;
  knownPeers: GarlicKnownPeer[];
}

/** One historical sample of the live-updating metrics (Task 13). */
export interface HistorySample {
  /** Unix milliseconds. */
  t: number;
  rxRate: number;
  txRate: number;
  garlicRelayedRate: number;
  garlicOriginatedRate: number;
}

/** The combined, per-poll snapshot every /api/* route reads from. */
export interface Snapshot {
  self: SelfInfo;
  peers: PeerEntry[];
  sessions: SessionEntry[];
  tree: TreeEntry[];
  paths: PathEntry[];
  garlic: GarlicSnapshot;
  history: HistorySample[];
  polledAt: string;
  /** False until the very first successful poll completes. */
  ready: boolean;
}

export const EMPTY_SELF: SelfInfo = {
  build_name: '',
  build_version: '',
  key: '',
  address: '',
  subnet: '',
  routing_entries: 0,
  uptime: 0
};

export const EMPTY_GARLIC_STATS: GarlicStats = {
  originatedCircuits: 0,
  relayedCircuits: 0,
  originatedPackets: 0,
  originatedBytes: 0,
  relayedPackets: 0,
  relayedBytes: 0,
  security: {
    replayDrops: 0,
    malformedPackets: 0,
    expiredPackets: 0,
    authFailures: 0,
    relayTableFull: 0
  }
};

export const EMPTY_GARLIC: GarlicSnapshot = {
  enabled: false,
  identity: null,
  stats: EMPTY_GARLIC_STATS,
  circuits: { originated: [], relayed: [] },
  knownPeers: []
};

export const EMPTY_SNAPSHOT: Snapshot = {
  self: EMPTY_SELF,
  peers: [],
  sessions: [],
  tree: [],
  paths: [],
  garlic: EMPTY_GARLIC,
  history: [],
  polledAt: '',
  ready: false
};
```

- [ ] **Step 2: Write the failing test for config**

Create `yggdashboard/src/lib/server/config.test.ts`:

```ts
import { describe, it, expect, beforeEach, afterEach } from 'vitest';
import { loadConfig } from './config';

const ENV_KEYS = ['ADMIN_SOCKET', 'POLL_INTERVAL_MS', 'HISTORY_WINDOW_MS'] as const;
const savedEnv: Record<string, string | undefined> = {};

beforeEach(() => {
  for (const key of ENV_KEYS) {
    savedEnv[key] = process.env[key];
    delete process.env[key];
  }
});

afterEach(() => {
  for (const key of ENV_KEYS) {
    if (savedEnv[key] === undefined) delete process.env[key];
    else process.env[key] = savedEnv[key];
  }
});

describe('loadConfig', () => {
  it('defaults to the platform admin socket path, a 1.5s poll interval, and 5 minutes of history', () => {
    const config = loadConfig();
    expect(config).toEqual({
      adminSocket: 'unix:///var/run/yggdrasil.sock',
      pollIntervalMs: 1500,
      historyWindowMs: 5 * 60 * 1000
    });
  });

  it('reads every field from the environment when set', () => {
    process.env.ADMIN_SOCKET = 'tcp://127.0.0.1:9001';
    process.env.POLL_INTERVAL_MS = '2000';
    process.env.HISTORY_WINDOW_MS = '60000';

    expect(loadConfig()).toEqual({
      adminSocket: 'tcp://127.0.0.1:9001',
      pollIntervalMs: 2000,
      historyWindowMs: 60000
    });
  });
});
```

- [ ] **Step 3: Run the test to verify it fails**

Run: `cd yggdashboard && npx vitest run src/lib/server/config.test.ts`
Expected: FAIL — `Cannot find module './config'`.

- [ ] **Step 4: Implement `yggdashboard/src/lib/server/config.ts`**

```ts
export interface DashboardConfig {
  adminSocket: string;
  pollIntervalMs: number;
  historyWindowMs: number;
}

// unix:///var/run/yggdrasil.sock matches src/config/defaults_linux.go's
// DefaultAdminListen exactly - verified against the Go source, not
// assumed. HOST/PORT are deliberately not read here: this dashboard
// process is a normal @sveltejs/adapter-node app, which already reads
// those itself when started via `node build/index.js`.
export function loadConfig(): DashboardConfig {
  return {
    adminSocket: process.env.ADMIN_SOCKET ?? 'unix:///var/run/yggdrasil.sock',
    pollIntervalMs: Number(process.env.POLL_INTERVAL_MS ?? 1500),
    historyWindowMs: Number(process.env.HISTORY_WINDOW_MS ?? 5 * 60 * 1000)
  };
}
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `cd yggdashboard && npx vitest run src/lib/server/config.test.ts`
Expected: PASS, both tests green.

- [ ] **Step 6: Commit**

```bash
git add yggdashboard/src/lib/server/types.ts yggdashboard/src/lib/server/config.ts yggdashboard/src/lib/server/config.test.ts
git commit -m "yggdashboard: add wire types and env-based config"
```

---

### Task 13: Poller and bounded history

**Files:**
- Create: `yggdashboard/src/lib/server/poll.ts`
- Test: `yggdashboard/src/lib/server/poll.test.ts`

**Interfaces:**
- Consumes: `AdminClient` (Task 11, only its `request<T>(name, args?)` shape — tests use a fake), every type from Task 12's `types.ts`.
- Produces: `class Poller { constructor(client, intervalMs, historyWindowMs); start(): void; stop(): void; getSnapshot(): Snapshot; waitUntilReady(timeoutMs: number): Promise<void> }`. Used by Task 14 (`+server.ts` routes) and Task 17+ (`+page.server.ts` load functions) via one shared singleton instance created in Task 14.

- [ ] **Step 1: Write the failing tests**

Create `yggdashboard/src/lib/server/poll.test.ts`:

```ts
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { Poller } from './poll';
import type { AdminClient } from './admin-client';

function fakeClient(responses: Record<string, unknown>, calls: string[] = []): AdminClient {
  return {
    request: vi.fn(async (name: string) => {
      calls.push(name);
      if (!(name in responses)) throw new Error(`unexpected request '${name}'`);
      const value = responses[name];
      if (value instanceof Error) throw value;
      return value;
    })
  } as unknown as AdminClient;
}

const CORE_RESPONSES = {
  getSelf: { build_name: 'yggdrasil', build_version: '0.5.14', key: 'abc', address: '200::1', subnet: '300::/64', routing_entries: 1, uptime: 42 },
  getPeers: { peers: [{ key: 'peer1', up: true, inbound: false, port: 1, priority: 0, cost: 1, rate_recvd: 100, rate_sent: 50 }] },
  getSessions: { sessions: [] },
  getTree: { tree: [] },
  getPaths: { paths: [] }
};

const GARLIC_RESPONSES = {
  getGarlicStats: { originatedCircuits: 1, relayedCircuits: 0, originatedPackets: 0, originatedBytes: 0, relayedPackets: 0, relayedBytes: 0, security: { replayDrops: 0, malformedPackets: 0, expiredPackets: 0, authFailures: 0, relayTableFull: 0 } },
  getGarlicIdentity: { publicKey: 'garlic-pub' },
  getGarlicCircuits: { originated: [], relayed: [] },
  getGarlicKnownPeers: { peers: [] }
};

describe('Poller', () => {
  beforeEach(() => vi.useFakeTimers());
  afterEach(() => vi.useRealTimers());

  it('builds a snapshot from core + garlic responses and marks it ready', async () => {
    const client = fakeClient({ ...CORE_RESPONSES, ...GARLIC_RESPONSES });
    const poller = new Poller(client, 2000, 300000);
    poller.start();
    await vi.advanceTimersByTimeAsync(0);

    const snap = poller.getSnapshot();
    expect(snap.ready).toBe(true);
    expect(snap.self.build_name).toBe('yggdrasil');
    expect(snap.peers).toHaveLength(1);
    expect(snap.garlic.enabled).toBe(true);
    expect(snap.garlic.identity).toEqual({ publicKey: 'garlic-pub' });
    poller.stop();
  });

  it('treats a rejected getGarlicStats as Garlic disabled and skips the other Garlic calls', async () => {
    const calls: string[] = [];
    const client = fakeClient(
      { ...CORE_RESPONSES, getGarlicStats: new Error('unknown command') },
      calls
    );
    const poller = new Poller(client, 2000, 300000);
    poller.start();
    await vi.advanceTimersByTimeAsync(0);

    const snap = poller.getSnapshot();
    expect(snap.garlic.enabled).toBe(false);
    expect(snap.garlic.stats.originatedCircuits).toBe(0);
    expect(calls).not.toContain('getGarlicIdentity');
    expect(calls).not.toContain('getGarlicCircuits');
    expect(calls).not.toContain('getGarlicKnownPeers');
    poller.stop();
  });

  it('keeps the last known value for a field whose request rejects, and still updates the rest', async () => {
    const client = {
      request: vi.fn(async (name: string) => {
        if (name === 'getPeers') throw new Error('boom');
        if (name in GARLIC_RESPONSES) return (GARLIC_RESPONSES as Record<string, unknown>)[name];
        return (CORE_RESPONSES as Record<string, unknown>)[name];
      })
    } as unknown as AdminClient;
    const poller = new Poller(client, 2000, 300000);
    poller.start();
    await vi.advanceTimersByTimeAsync(0);

    const snap = poller.getSnapshot();
    expect(snap.peers).toEqual([]); // fell back to the empty initial snapshot's peers
    expect(snap.self.build_name).toBe('yggdrasil'); // unaffected field still updates
    poller.stop();
  });

  it('computes an aggregate rx/tx rate from peers.rate_recvd/rate_sent', async () => {
    const client = fakeClient({ ...CORE_RESPONSES, ...GARLIC_RESPONSES });
    const poller = new Poller(client, 2000, 300000);
    poller.start();
    await vi.advanceTimersByTimeAsync(0);

    const sample = poller.getSnapshot().history.at(-1)!;
    expect(sample.rxRate).toBe(100);
    expect(sample.txRate).toBe(50);
    poller.stop();
  });

  it('computes Garlic relayed/originated rate from byte-counter deltas across two polls', async () => {
    let relayedBytes = 1000;
    const client = {
      request: vi.fn(async (name: string) => {
        if (name === 'getGarlicStats') {
          return { ...GARLIC_RESPONSES.getGarlicStats, relayedBytes, originatedBytes: 0 };
        }
        if (name in GARLIC_RESPONSES) return (GARLIC_RESPONSES as Record<string, unknown>)[name];
        return (CORE_RESPONSES as Record<string, unknown>)[name];
      })
    } as unknown as AdminClient;
    const poller = new Poller(client, 2000, 300000);
    poller.start();
    await vi.advanceTimersByTimeAsync(0); // first tick: no prior sample, rate = 0

    relayedBytes = 3000; // +2000 bytes over the next 2000ms interval = 1000 B/s
    await vi.advanceTimersByTimeAsync(2000);

    const history = poller.getSnapshot().history;
    expect(history.length).toBe(2);
    expect(history[0].garlicRelayedRate).toBe(0);
    expect(history[1].garlicRelayedRate).toBeCloseTo(1000, 0);
    poller.stop();
  });

  it('bounds history to the configured window', async () => {
    const client = fakeClient({ ...CORE_RESPONSES, ...GARLIC_RESPONSES });
    const poller = new Poller(client, 1000, 2500); // 2.5s window, 1s interval
    poller.start();
    for (let i = 0; i < 5; i++) {
      await vi.advanceTimersByTimeAsync(1000);
    }
    const history = poller.getSnapshot().history;
    expect(history.length).toBeLessThanOrEqual(3);
    poller.stop();
  });

  it('waitUntilReady resolves once the first poll completes', async () => {
    const client = fakeClient({ ...CORE_RESPONSES, ...GARLIC_RESPONSES });
    const poller = new Poller(client, 2000, 300000);
    poller.start();
    const ready = poller.waitUntilReady(5000);
    await vi.advanceTimersByTimeAsync(0);
    await expect(ready).resolves.toBeUndefined();
    poller.stop();
  });

  it('waitUntilReady resolves after the timeout even if no poll ever completes', async () => {
    const client = { request: vi.fn(() => new Promise(() => {})) } as unknown as AdminClient; // never resolves
    const poller = new Poller(client, 2000, 300000);
    poller.start();
    const ready = poller.waitUntilReady(1000);
    await vi.advanceTimersByTimeAsync(1000);
    await expect(ready).resolves.toBeUndefined();
    poller.stop();
  });

  it('stop halts further polling', async () => {
    const client = fakeClient({ ...CORE_RESPONSES, ...GARLIC_RESPONSES });
    const poller = new Poller(client, 2000, 300000);
    poller.start();
    await vi.advanceTimersByTimeAsync(0);
    poller.stop();

    const before = poller.getSnapshot().polledAt;
    await vi.advanceTimersByTimeAsync(10000);
    expect(poller.getSnapshot().polledAt).toBe(before);
  });
});
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `cd yggdashboard && npx vitest run src/lib/server/poll.test.ts`
Expected: FAIL — `Cannot find module './poll'`.

- [ ] **Step 3: Implement `yggdashboard/src/lib/server/poll.ts`**

```ts
import type { AdminClient } from './admin-client';
import {
  EMPTY_SNAPSHOT,
  EMPTY_GARLIC,
  type Snapshot,
  type SelfInfo,
  type PeerEntry,
  type SessionEntry,
  type TreeEntry,
  type PathEntry,
  type GarlicSnapshot,
  type GarlicIdentity,
  type GarlicStats,
  type GarlicCircuits,
  type GarlicKnownPeer,
  type HistorySample
} from './types';

/**
 * Polls every admin endpoint the dashboard needs over one shared
 * AdminClient (one persistent keepalive connection, pipelined - see
 * admin-client.ts), keeps the latest Snapshot plus a bounded in-memory
 * history ring buffer, and serves every caller (every /api/* route,
 * every SSR load function) from that one copy - the admin-socket poll
 * rate never scales with how many browser tabs are open.
 *
 * Garlic calls are tried as a group: if getGarlicStats fails (the admin
 * socket has no such handler at all when Garlic.Enabled is false on the
 * node), the whole Garlic snapshot for this tick is the explicit
 * disabled/zeroed shape, and the other three Garlic calls aren't even
 * attempted that tick - not treated as an error to log, just the normal
 * disabled state.
 */
export class Poller {
  private client: AdminClient;
  private intervalMs: number;
  private historyWindowMs: number;
  private timer: ReturnType<typeof setInterval> | null = null;
  private latest: Snapshot = EMPTY_SNAPSHOT;
  private history: HistorySample[] = [];
  private prevGarlicBytes: { originated: number; relayed: number; t: number } | null = null;
  private readyWaiters: Array<() => void> = [];
  private hasPolledOnce = false;

  constructor(client: AdminClient, intervalMs: number, historyWindowMs: number) {
    this.client = client;
    this.intervalMs = intervalMs;
    this.historyWindowMs = historyWindowMs;
  }

  start(): void {
    if (this.timer) return;
    void this.tick();
    this.timer = setInterval(() => void this.tick(), this.intervalMs);
  }

  stop(): void {
    if (this.timer) clearInterval(this.timer);
    this.timer = null;
  }

  getSnapshot(): Snapshot {
    return this.latest;
  }

  /**
   * Resolves once the first poll has completed, or after timeoutMs -
   * whichever comes first. Lets an SSR load function show real data on
   * the very first request after the dashboard process starts, without
   * blocking indefinitely if the admin socket is unreachable.
   */
  waitUntilReady(timeoutMs: number): Promise<void> {
    if (this.hasPolledOnce) return Promise.resolve();
    return new Promise<void>((resolve) => {
      const timer = setTimeout(resolve, timeoutMs);
      this.readyWaiters.push(() => {
        clearTimeout(timer);
        resolve();
      });
    });
  }

  private async tick(): Promise<void> {
    const [selfRes, peersRes, sessionsRes, treeRes, pathsRes] = await Promise.allSettled([
      this.client.request<SelfInfo>('getSelf'),
      this.client.request<{ peers: PeerEntry[] }>('getPeers'),
      this.client.request<{ sessions: SessionEntry[] }>('getSessions'),
      this.client.request<{ tree: TreeEntry[] }>('getTree'),
      this.client.request<{ paths: PathEntry[] }>('getPaths')
    ]);
    const garlic = await this.pollGarlic();

    const self = selfRes.status === 'fulfilled' ? selfRes.value : this.latest.self;
    const peers = peersRes.status === 'fulfilled' ? peersRes.value.peers : this.latest.peers;
    const sessions = sessionsRes.status === 'fulfilled' ? sessionsRes.value.sessions : this.latest.sessions;
    const tree = treeRes.status === 'fulfilled' ? treeRes.value.tree : this.latest.tree;
    const paths = pathsRes.status === 'fulfilled' ? pathsRes.value.paths : this.latest.paths;

    for (const [label, r] of [
      ['getSelf', selfRes],
      ['getPeers', peersRes],
      ['getSessions', sessionsRes],
      ['getTree', treeRes],
      ['getPaths', pathsRes]
    ] as const) {
      if (r.status === 'rejected') {
        console.error(`yggdashboard: poll request ${label} failed:`, r.reason);
      }
    }

    const now = Date.now();
    const rxRate = peers.reduce((sum, p) => sum + (p.rate_recvd ?? 0), 0);
    const txRate = peers.reduce((sum, p) => sum + (p.rate_sent ?? 0), 0);

    let garlicRelayedRate = 0;
    let garlicOriginatedRate = 0;
    if (garlic.enabled && this.prevGarlicBytes) {
      const elapsedSeconds = (now - this.prevGarlicBytes.t) / 1000;
      if (elapsedSeconds > 0) {
        garlicRelayedRate = Math.max(0, (garlic.stats.relayedBytes - this.prevGarlicBytes.relayed) / elapsedSeconds);
        garlicOriginatedRate = Math.max(0, (garlic.stats.originatedBytes - this.prevGarlicBytes.originated) / elapsedSeconds);
      }
    }
    this.prevGarlicBytes = garlic.enabled
      ? { originated: garlic.stats.originatedBytes, relayed: garlic.stats.relayedBytes, t: now }
      : null;

    this.history.push({ t: now, rxRate, txRate, garlicRelayedRate, garlicOriginatedRate });
    this.history = this.history.filter((s) => now - s.t <= this.historyWindowMs);

    this.latest = {
      self,
      peers,
      sessions,
      tree,
      paths,
      garlic,
      history: this.history,
      polledAt: new Date(now).toISOString(),
      ready: true
    };

    if (!this.hasPolledOnce) {
      this.hasPolledOnce = true;
      const waiters = this.readyWaiters.splice(0);
      for (const resolve of waiters) resolve();
    }
  }

  private async pollGarlic(): Promise<GarlicSnapshot> {
    let stats: GarlicStats;
    try {
      stats = await this.client.request<GarlicStats>('getGarlicStats');
    } catch {
      return EMPTY_GARLIC;
    }

    const [identityRes, circuitsRes, knownPeersRes] = await Promise.allSettled([
      this.client.request<GarlicIdentity>('getGarlicIdentity'),
      this.client.request<GarlicCircuits>('getGarlicCircuits'),
      this.client.request<{ peers: GarlicKnownPeer[] }>('getGarlicKnownPeers')
    ]);

    return {
      enabled: true,
      identity: identityRes.status === 'fulfilled' ? identityRes.value : this.latest.garlic.identity,
      stats,
      circuits: circuitsRes.status === 'fulfilled' ? circuitsRes.value : this.latest.garlic.circuits,
      knownPeers: knownPeersRes.status === 'fulfilled' ? knownPeersRes.value.peers : this.latest.garlic.knownPeers
    };
  }
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `cd yggdashboard && npx vitest run src/lib/server/poll.test.ts`
Expected: PASS, all 9 tests green.

- [ ] **Step 5: Commit**

```bash
git add yggdashboard/src/lib/server/poll.ts yggdashboard/src/lib/server/poll.test.ts
git commit -m "yggdashboard: add poller with bounded history and Garlic-disabled handling"
```

---

### Task 14: Shared poller instance and `/api/*` endpoints

**Files:**
- Create: `yggdashboard/src/lib/server/instance.ts`
- Create: `yggdashboard/src/lib/server/status.ts`
- Create: `yggdashboard/src/lib/server/stats.ts`
- Create: `yggdashboard/src/lib/server/peers.ts`
- Create: `yggdashboard/src/lib/server/circuits.ts`
- Create: `yggdashboard/src/lib/server/garlic.ts`
- Create: `yggdashboard/src/lib/server/graph.ts`
- Create: `yggdashboard/src/routes/api/status/+server.ts`
- Create: `yggdashboard/src/routes/api/stats/+server.ts`
- Create: `yggdashboard/src/routes/api/peers/+server.ts`
- Create: `yggdashboard/src/routes/api/circuits/+server.ts`
- Create: `yggdashboard/src/routes/api/garlic/+server.ts`
- Create: `yggdashboard/src/routes/api/graph/+server.ts`
- Test: `yggdashboard/src/routes/api/status/server.test.ts`
- Test: `yggdashboard/src/routes/api/garlic/server.test.ts`

**Interfaces:**
- Consumes: `AdminClient` (Task 11), `Poller`/`loadConfig` (Tasks 12-13).
- Produces: one shared `poller: Poller` singleton (module-level, created once per server process on first import - the standard SvelteKit pattern for a background service, since there's no custom server entrypoint in this design); six pure `computeX(snap: Snapshot)` builder functions, each returning hand-picked field subsets, never a passthrough of the raw `Snapshot`; six GET routes that are thin wrappers around those builders. Used by Task 15 (client store) and every `+page.server.ts` in Tasks 17-21 - both a route and its page's SSR load call the *same* builder function, so the two can never drift apart.

- [ ] **Step 1: Create `yggdashboard/src/lib/server/instance.ts`**

```ts
import { AdminClient } from './admin-client';
import { loadConfig } from './config';
import { Poller } from './poll';

const config = loadConfig();
const client = new AdminClient(config.adminSocket);

/**
 * The one Poller instance for this server process. Created at module
 * load time (Node caches modules, so every importer gets this same
 * instance) and started immediately - every /api/* route and every
 * +page.server.ts load function reads from it, none of them poll the
 * admin socket themselves.
 */
export const poller = new Poller(client, config.pollIntervalMs, config.historyWindowMs);
poller.start();
```

- [ ] **Step 2: Create the shared status-derivation helper**

Both `/api/status` (this task) and the root layout's SSR load (Task 17, for the top status bar to render with real data with no JS) need the exact same "online/degraded/disconnected" logic. Factor it out once rather than duplicating it:

Create `yggdashboard/src/lib/server/status.ts`:

```ts
import type { Snapshot } from './types';

export interface StatusPayload {
  status: 'online' | 'degraded' | 'disconnected';
  uptime: number;
  buildName: string;
  buildVersion: string;
  garlicEnabled: boolean;
  peerCount: number;
  peersUp: number;
  polledAt: string;
}

/**
 * Derives the top-level node status from what this dashboard process
 * can actually observe: Online = at least one peer up, Degraded =
 * admin socket reachable but zero peers up, Disconnected = the poller
 * has never completed a successful poll at all. No invented health
 * checks beyond what's directly derivable from getSelf/getPeers.
 */
export function computeStatus(snap: Snapshot): StatusPayload {
  const peersUp = snap.peers.filter((p) => p.up).length;
  let status: StatusPayload['status'];
  if (!snap.ready) {
    status = 'disconnected';
  } else if (peersUp === 0) {
    status = 'degraded';
  } else {
    status = 'online';
  }
  return {
    status,
    uptime: snap.self.uptime,
    buildName: snap.self.build_name,
    buildVersion: snap.self.build_version,
    garlicEnabled: snap.garlic.enabled,
    peerCount: snap.peers.length,
    peersUp,
    polledAt: snap.polledAt
  };
}
```

- [ ] **Step 3: Write the failing "no secret leak" tests for two representative routes**

Create `yggdashboard/src/routes/api/status/server.test.ts`:

```ts
import { describe, it, expect, vi } from 'vitest';

vi.mock('$lib/server/instance', () => ({
  poller: {
    waitUntilReady: vi.fn().mockResolvedValue(undefined),
    getSnapshot: vi.fn(() => ({
      self: {
        build_name: 'yggdrasil',
        build_version: '0.5.14',
        key: 'abc123',
        address: '200::1',
        subnet: '300::/64',
        routing_entries: 3,
        uptime: 120,
        // A field that must never leak, simulating a hypothetical
        // future admin field this route must not blindly pass through.
        privateKey: 'should-never-appear'
      },
      peers: [{ up: true }, { up: false }, { up: true }],
      garlic: { enabled: true },
      ready: true,
      polledAt: '2026-08-10T00:00:00.000Z'
    }))
  }
}));

const { GET } = await import('./+server');

describe('GET /api/status', () => {
  it('never includes a privateKey field, even if present on the snapshot', async () => {
    const response = await GET({} as never);
    const body = await response.json();
    expect(JSON.stringify(body)).not.toContain('privateKey');
  });

  it('reports status Online with at least one up peer', async () => {
    const response = await GET({} as never);
    const body = await response.json();
    expect(body.status).toBe('online');
    expect(body.peerCount).toBe(3);
    expect(body.peersUp).toBe(2);
  });
});
```

Create `yggdashboard/src/routes/api/garlic/server.test.ts`:

```ts
import { describe, it, expect, vi } from 'vitest';

vi.mock('$lib/server/instance', () => ({
  poller: {
    waitUntilReady: vi.fn().mockResolvedValue(undefined),
    getSnapshot: vi.fn(() => ({
      garlic: {
        enabled: true,
        identity: { publicKey: 'garlic-pub', privateKey: 'must-not-leak' },
        stats: {
          originatedCircuits: 1,
          relayedCircuits: 0,
          originatedPackets: 1,
          originatedBytes: 100,
          relayedPackets: 0,
          relayedBytes: 0,
          security: { replayDrops: 0, malformedPackets: 0, expiredPackets: 0, authFailures: 0, relayTableFull: 0 }
        },
        circuits: { originated: [], relayed: [] },
        knownPeers: []
      }
    }))
  }
}));

const { GET } = await import('./+server');

describe('GET /api/garlic', () => {
  it('never includes a privateKey field', async () => {
    const response = await GET({} as never);
    const body = await response.json();
    expect(JSON.stringify(body)).not.toContain('privateKey');
    expect(body.identity.publicKey).toBe('garlic-pub');
  });
});
```

- [ ] **Step 3: Run the tests to verify they fail**

Run: `cd yggdashboard && npx vitest run src/routes/api/status/server.test.ts src/routes/api/garlic/server.test.ts`
Expected: FAIL — `./+server` modules don't exist yet.

- [ ] **Step 4: Create the remaining five builder functions**

Each `/api/*` route's response-building logic is factored into a pure function up front, because Task 17-21's `+page.server.ts` load functions (SSR) need to build the exact same shapes without an internal HTTP round-trip to their own `+server.ts` sibling - both call the same builder.

Create `yggdashboard/src/lib/server/stats.ts`:

```ts
import type { Snapshot } from './types';

export function computeStats(snap: Snapshot) {
  const rxTotal = snap.peers.reduce((sum, p) => sum + (p.bytes_recvd ?? 0), 0);
  const txTotal = snap.peers.reduce((sum, p) => sum + (p.bytes_sent ?? 0), 0);
  const sessionRx = snap.sessions.reduce((sum, s) => sum + s.bytes_recvd, 0);
  const sessionTx = snap.sessions.reduce((sum, s) => sum + s.bytes_sent, 0);
  const latest = snap.history.at(-1);
  const totalGarlicBytes = snap.garlic.stats.originatedBytes + snap.garlic.stats.relayedBytes;

  return {
    rxRate: latest?.rxRate ?? 0,
    txRate: latest?.txRate ?? 0,
    rxTotalPeerLink: rxTotal,
    txTotalPeerLink: txTotal,
    rxTotalSessions: sessionRx,
    txTotalSessions: sessionTx,
    garlic: {
      enabled: snap.garlic.enabled,
      originatedBytes: snap.garlic.stats.originatedBytes,
      relayedBytes: snap.garlic.stats.relayedBytes,
      originatedRate: latest?.garlicOriginatedRate ?? 0,
      relayedRate: latest?.garlicRelayedRate ?? 0,
      // "Share of Garlic circuit traffic relayed for others" - never
      // presented as an all-Yggdrasil-traffic figure. See the design
      // spec's "Metrics" section for why a global transit % isn't
      // implementable.
      transitPercent: totalGarlicBytes > 0 ? (snap.garlic.stats.relayedBytes / totalGarlicBytes) * 100 : 0
    },
    history: snap.history,
    polledAt: snap.polledAt
  };
}
```

Create `yggdashboard/src/lib/server/peers.ts`:

```ts
import type { Snapshot } from './types';

export function computePeers(snap: Snapshot) {
  const garlicKnownKeys = new Set(snap.garlic.knownPeers.map((p) => p.nodeKey));
  const peers = snap.peers.map((p) => ({
    key: p.key,
    remote: p.remote ?? null,
    address: p.address ?? null,
    up: p.up,
    inbound: p.inbound,
    bytesRecvd: p.bytes_recvd ?? 0,
    bytesSent: p.bytes_sent ?? 0,
    rateRecvd: p.rate_recvd ?? 0,
    rateSent: p.rate_sent ?? 0,
    uptime: p.uptime ?? 0,
    latencyNs: p.latency ?? null,
    lastError: p.last_error ?? null,
    garlicCapable: garlicKnownKeys.has(p.key)
  }));
  return { peers, polledAt: snap.polledAt };
}
```

Create `yggdashboard/src/lib/server/circuits.ts`:

```ts
import type { Snapshot } from './types';

export function computeCircuits(snap: Snapshot) {
  return {
    enabled: snap.garlic.enabled,
    originated: snap.garlic.circuits.originated,
    relayed: snap.garlic.circuits.relayed,
    polledAt: snap.polledAt
  };
}
```

Create `yggdashboard/src/lib/server/garlic.ts`:

```ts
import type { Snapshot } from './types';

export function computeGarlic(snap: Snapshot) {
  return {
    enabled: snap.garlic.enabled,
    identity: snap.garlic.identity ? { publicKey: snap.garlic.identity.publicKey } : null,
    stats: snap.garlic.stats,
    knownPeers: snap.garlic.knownPeers,
    polledAt: snap.polledAt
  };
}
```

Create `yggdashboard/src/lib/server/graph.ts`:

```ts
import type { Snapshot } from './types';

export function computeGraph(snap: Snapshot) {
  // Yggdrasil connectivity layer: real edges from getTree (key -> parent).
  const yggdrasilEdges = snap.tree
    .filter((entry) => entry.parent !== '' && entry.parent !== entry.key)
    .map((entry) => ({ from: entry.key, to: entry.parent, type: 'yggdrasil' as const }));

  const yggdrasilNodes = new Map<string, { key: string; address: string; isSelf: boolean }>();
  for (const entry of snap.tree) {
    yggdrasilNodes.set(entry.key, { key: entry.key, address: entry.address, isSelf: entry.key === snap.self.key });
  }
  yggdrasilNodes.set(snap.self.key, { key: snap.self.key, address: snap.self.address, isSelf: true });

  // Garlic circuit layer: originator's own chosen hop chain, and each
  // relayed circuit's real previous/next hop only - never a fabricated
  // full path for circuits this node only relays.
  const garlicEdges: Array<{ from: string; to: string; type: 'garlic'; circuitId: string; active: boolean }> = [];
  for (const c of snap.garlic.circuits.originated) {
    const chain = [snap.self.key, ...c.hops];
    for (let i = 0; i < chain.length - 1; i++) {
      garlicEdges.push({ from: chain[i], to: chain[i + 1], type: 'garlic', circuitId: c.circuitId, active: !c.closed });
    }
  }
  for (const r of snap.garlic.circuits.relayed) {
    garlicEdges.push({ from: r.previousHop, to: snap.self.key, type: 'garlic', circuitId: r.circuitId, active: true });
    garlicEdges.push({ from: snap.self.key, to: r.nextHop, type: 'garlic', circuitId: r.circuitId, active: true });
  }

  return {
    nodes: Array.from(yggdrasilNodes.values()),
    yggdrasilEdges,
    garlicEdges,
    polledAt: snap.polledAt
  };
}
```

- [ ] **Step 5: Implement the six thin `+server.ts` routes**

Each route is now just: wait for data, call its builder, return JSON.

Create `yggdashboard/src/routes/api/status/+server.ts`:

```ts
import { json } from '@sveltejs/kit';
import { poller } from '$lib/server/instance';
import { computeStatus } from '$lib/server/status';
import type { RequestHandler } from './$types';

export const GET: RequestHandler = async () => {
  await poller.waitUntilReady(2000);
  return json(computeStatus(poller.getSnapshot()));
};
```

Create `yggdashboard/src/routes/api/stats/+server.ts`:

```ts
import { json } from '@sveltejs/kit';
import { poller } from '$lib/server/instance';
import { computeStats } from '$lib/server/stats';
import type { RequestHandler } from './$types';

export const GET: RequestHandler = async () => {
  await poller.waitUntilReady(2000);
  return json(computeStats(poller.getSnapshot()));
};
```

Create `yggdashboard/src/routes/api/peers/+server.ts`:

```ts
import { json } from '@sveltejs/kit';
import { poller } from '$lib/server/instance';
import { computePeers } from '$lib/server/peers';
import type { RequestHandler } from './$types';

export const GET: RequestHandler = async () => {
  await poller.waitUntilReady(2000);
  return json(computePeers(poller.getSnapshot()));
};
```

Create `yggdashboard/src/routes/api/circuits/+server.ts`:

```ts
import { json } from '@sveltejs/kit';
import { poller } from '$lib/server/instance';
import { computeCircuits } from '$lib/server/circuits';
import type { RequestHandler } from './$types';

export const GET: RequestHandler = async () => {
  await poller.waitUntilReady(2000);
  return json(computeCircuits(poller.getSnapshot()));
};
```

Create `yggdashboard/src/routes/api/garlic/+server.ts`:

```ts
import { json } from '@sveltejs/kit';
import { poller } from '$lib/server/instance';
import { computeGarlic } from '$lib/server/garlic';
import type { RequestHandler } from './$types';

export const GET: RequestHandler = async () => {
  await poller.waitUntilReady(2000);
  return json(computeGarlic(poller.getSnapshot()));
};
```

Create `yggdashboard/src/routes/api/graph/+server.ts`:

```ts
import { json } from '@sveltejs/kit';
import { poller } from '$lib/server/instance';
import { computeGraph } from '$lib/server/graph';
import type { RequestHandler } from './$types';

export const GET: RequestHandler = async () => {
  await poller.waitUntilReady(2000);
  return json(computeGraph(poller.getSnapshot()));
};
```

- [ ] **Step 6: Run the tests to verify they pass**

Run: `cd yggdashboard && npx vitest run src/routes/api/status/server.test.ts src/routes/api/garlic/server.test.ts`
Expected: PASS, all 3 tests green (note the status route test asserts `body.status === 'online'` because the mocked snapshot has `peersUp: 2 > 0` and `ready: true`).

- [ ] **Step 7: Commit**

```bash
git add yggdashboard/src/lib/server/instance.ts yggdashboard/src/lib/server/status.ts \
  yggdashboard/src/lib/server/stats.ts yggdashboard/src/lib/server/peers.ts \
  yggdashboard/src/lib/server/circuits.ts yggdashboard/src/lib/server/garlic.ts \
  yggdashboard/src/lib/server/graph.ts yggdashboard/src/routes/api
git commit -m "yggdashboard: add shared poller instance, response builders, and /api/* endpoints"
```

---

### Task 15: Client-facing API types and polled-resource store

**Files:**
- Create: `yggdashboard/src/lib/api-types.ts`
- Create: `yggdashboard/src/lib/stores/dashboard.svelte.ts`
- Test: `yggdashboard/src/lib/stores/dashboard.svelte.test.ts`

**Interfaces:**
- Consumes: nothing from `src/lib/server/*` (client code is compiler-forbidden from importing it - these are the dashboard's own `/api/*` response shapes, defined independently).
- Produces: `createStatusResource/createStatsResource/createPeersResource/createCircuitsResource/createGarlicResource/createGraphResource(intervalMs?)`, each returning `{ readonly data: T | null; readonly connected: boolean; readonly latencyMs: number | null; start(): void; stop(): void }` (Svelte 5 runes-based). Used by Task 17-21's pages and Task 22's status bar.

Each page/layout instantiates only the resource(s) it actually needs (the overview page doesn't poll `/api/circuits`, the circuits page doesn't poll `/api/graph`, etc.) - "one central store" per the spec means one shared *implementation*, reused everywhere, not one fetch loop hitting every endpoint on every page regardless of relevance.

- [ ] **Step 1: Create `yggdashboard/src/lib/api-types.ts`**

```ts
/**
 * Types for this dashboard's own /api/* responses (src/routes/api/) -
 * distinct from src/lib/server/types.ts's raw Yggdrasil admin wire
 * types, which client code can never import (SvelteKit enforces that
 * boundary at build time). These are the hand-picked shapes the server
 * routes actually return.
 */

export interface StatusResponse {
  status: 'online' | 'degraded' | 'disconnected';
  uptime: number;
  buildName: string;
  buildVersion: string;
  garlicEnabled: boolean;
  peerCount: number;
  peersUp: number;
  polledAt: string;
}

export interface HistorySample {
  t: number;
  rxRate: number;
  txRate: number;
  garlicRelayedRate: number;
  garlicOriginatedRate: number;
}

export interface StatsResponse {
  rxRate: number;
  txRate: number;
  rxTotalPeerLink: number;
  txTotalPeerLink: number;
  rxTotalSessions: number;
  txTotalSessions: number;
  garlic: {
    enabled: boolean;
    originatedBytes: number;
    relayedBytes: number;
    originatedRate: number;
    relayedRate: number;
    transitPercent: number;
  };
  history: HistorySample[];
  polledAt: string;
}

export interface ApiPeer {
  key: string;
  remote: string | null;
  address: string | null;
  up: boolean;
  inbound: boolean;
  bytesRecvd: number;
  bytesSent: number;
  rateRecvd: number;
  rateSent: number;
  uptime: number;
  latencyNs: number | null;
  lastError: string | null;
  garlicCapable: boolean;
}

export interface PeersResponse {
  peers: ApiPeer[];
  polledAt: string;
}

export interface OriginatedCircuit {
  circuitId: string;
  hops: string[];
  closed: boolean;
  createdAt: string;
  expiresAt: string;
  packets: number;
  bytes: number;
}

export interface RelayedCircuit {
  circuitId: string;
  previousHop: string;
  nextHop: string;
  firstSeen: string;
  lastActive: string;
  packetsRelayed: number;
  bytesRelayed: number;
}

export interface CircuitsResponse {
  enabled: boolean;
  originated: OriginatedCircuit[];
  relayed: RelayedCircuit[];
  polledAt: string;
}

export interface GarlicSecurityCounters {
  replayDrops: number;
  malformedPackets: number;
  expiredPackets: number;
  authFailures: number;
  relayTableFull: number;
}

export interface GarlicResponse {
  enabled: boolean;
  identity: { publicKey: string } | null;
  stats: {
    originatedCircuits: number;
    relayedCircuits: number;
    originatedPackets: number;
    originatedBytes: number;
    relayedPackets: number;
    relayedBytes: number;
    security: GarlicSecurityCounters;
  };
  knownPeers: Array<{ nodeKey: string; garlicPublicKey: string; lastSeen: string }>;
  polledAt: string;
}

export interface GraphNode {
  key: string;
  address: string;
  isSelf: boolean;
}

export interface GraphEdge {
  from: string;
  to: string;
  type: 'yggdrasil' | 'garlic';
  circuitId?: string;
  active?: boolean;
}

export interface GraphResponse {
  nodes: GraphNode[];
  yggdrasilEdges: GraphEdge[];
  garlicEdges: GraphEdge[];
  polledAt: string;
}
```

- [ ] **Step 2: Write the failing tests**

Create `yggdashboard/src/lib/stores/dashboard.svelte.test.ts`:

```ts
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';

describe('createPolledResource (via createStatusResource)', () => {
  beforeEach(() => {
    vi.useFakeTimers();
    vi.stubGlobal('fetch', vi.fn());
  });
  afterEach(() => {
    vi.useRealTimers();
    vi.unstubAllGlobals();
  });

  it('fetches immediately on start and exposes the parsed JSON as data', async () => {
    (fetch as ReturnType<typeof vi.fn>).mockResolvedValue({ ok: true, json: async () => ({ hello: 'world' }) });
    const { createStatusResource } = await import('./dashboard.svelte');
    const resource = createStatusResource(1000);
    resource.start();
    await vi.advanceTimersByTimeAsync(0);
    expect(resource.data).toEqual({ hello: 'world' });
    expect(resource.connected).toBe(true);
    resource.stop();
  });

  it('marks connected false when the fetch rejects, without clearing prior data', async () => {
    (fetch as ReturnType<typeof vi.fn>)
      .mockResolvedValueOnce({ ok: true, json: async () => ({ a: 1 }) })
      .mockRejectedValueOnce(new Error('network down'));
    const { createStatusResource } = await import('./dashboard.svelte');
    const resource = createStatusResource(1000);
    resource.start();
    await vi.advanceTimersByTimeAsync(0);
    expect(resource.data).toEqual({ a: 1 });

    await vi.advanceTimersByTimeAsync(1000);
    expect(resource.connected).toBe(false);
    expect(resource.data).toEqual({ a: 1 });
    resource.stop();
  });

  it('marks connected false on a non-ok HTTP response', async () => {
    (fetch as ReturnType<typeof vi.fn>).mockResolvedValue({ ok: false, status: 500, json: async () => ({}) });
    const { createStatusResource } = await import('./dashboard.svelte');
    const resource = createStatusResource(1000);
    resource.start();
    await vi.advanceTimersByTimeAsync(0);
    expect(resource.connected).toBe(false);
    resource.stop();
  });

  it('stop halts further polling', async () => {
    (fetch as ReturnType<typeof vi.fn>).mockResolvedValue({ ok: true, json: async () => ({ n: 1 }) });
    const { createStatusResource } = await import('./dashboard.svelte');
    const resource = createStatusResource(1000);
    resource.start();
    await vi.advanceTimersByTimeAsync(0);
    resource.stop();
    const callsBefore = (fetch as ReturnType<typeof vi.fn>).mock.calls.length;
    await vi.advanceTimersByTimeAsync(5000);
    expect((fetch as ReturnType<typeof vi.fn>).mock.calls.length).toBe(callsBefore);
  });

  it('records a non-negative latencyMs after a successful fetch', async () => {
    (fetch as ReturnType<typeof vi.fn>).mockResolvedValue({ ok: true, json: async () => ({}) });
    const { createStatusResource } = await import('./dashboard.svelte');
    const resource = createStatusResource(1000);
    resource.start();
    await vi.advanceTimersByTimeAsync(0);
    expect(resource.latencyMs).not.toBeNull();
    expect(resource.latencyMs!).toBeGreaterThanOrEqual(0);
    resource.stop();
  });
});
```

- [ ] **Step 3: Run the tests to verify they fail**

Run: `cd yggdashboard && npx vitest run src/lib/stores/dashboard.svelte.test.ts`
Expected: FAIL — `Cannot find module './dashboard.svelte'`.

- [ ] **Step 4: Implement `yggdashboard/src/lib/stores/dashboard.svelte.ts`**

```ts
import type {
  StatusResponse,
  StatsResponse,
  PeersResponse,
  CircuitsResponse,
  GarlicResponse,
  GraphResponse
} from '$lib/api-types';

export interface PolledResource<T> {
  readonly data: T | null;
  readonly connected: boolean;
  readonly latencyMs: number | null;
  start(): void;
  stop(): void;
}

/**
 * A small reactive polling primitive: fetches url every intervalMs,
 * exposing the parsed JSON as $state, plus connection health. On a
 * failed fetch, `connected` goes false but `data` keeps its last good
 * value - matches the "stale metrics, not a blank screen" requirement.
 * Uses Svelte 5 runes, hence the .svelte.ts extension.
 */
export function createPolledResource<T>(url: string, intervalMs: number): PolledResource<T> {
  let data = $state<T | null>(null);
  let connected = $state(true);
  let latencyMs = $state<number | null>(null);
  let timer: ReturnType<typeof setInterval> | undefined;

  async function pollOnce() {
    const start = performance.now();
    try {
      const res = await fetch(url);
      if (!res.ok) throw new Error(`${url} returned HTTP ${res.status}`);
      data = (await res.json()) as T;
      connected = true;
      latencyMs = Math.round(performance.now() - start);
    } catch {
      connected = false;
    }
  }

  function start() {
    if (timer) return;
    void pollOnce();
    timer = setInterval(() => void pollOnce(), intervalMs);
  }

  function stop() {
    if (timer) clearInterval(timer);
    timer = undefined;
  }

  return {
    get data() {
      return data;
    },
    get connected() {
      return connected;
    },
    get latencyMs() {
      return latencyMs;
    },
    start,
    stop
  };
}

export function createStatusResource(intervalMs = 1500): PolledResource<StatusResponse> {
  return createPolledResource<StatusResponse>('/api/status', intervalMs);
}
export function createStatsResource(intervalMs = 1500): PolledResource<StatsResponse> {
  return createPolledResource<StatsResponse>('/api/stats', intervalMs);
}
export function createPeersResource(intervalMs = 2000): PolledResource<PeersResponse> {
  return createPolledResource<PeersResponse>('/api/peers', intervalMs);
}
export function createCircuitsResource(intervalMs = 2000): PolledResource<CircuitsResponse> {
  return createPolledResource<CircuitsResponse>('/api/circuits', intervalMs);
}
export function createGarlicResource(intervalMs = 2000): PolledResource<GarlicResponse> {
  return createPolledResource<GarlicResponse>('/api/garlic', intervalMs);
}
export function createGraphResource(intervalMs = 2000): PolledResource<GraphResponse> {
  return createPolledResource<GraphResponse>('/api/graph', intervalMs);
}
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `cd yggdashboard && npx vitest run src/lib/stores/dashboard.svelte.test.ts`
Expected: PASS, all 5 tests green.

- [ ] **Step 6: Commit**

```bash
git add yggdashboard/src/lib/api-types.ts yggdashboard/src/lib/stores/dashboard.svelte.ts \
  yggdashboard/src/lib/stores/dashboard.svelte.test.ts
git commit -m "yggdashboard: add client API types and reactive polled-resource store"
```

---

### Task 16: Format helpers and shared components

**Files:**
- Create: `yggdashboard/src/lib/format.ts`
- Test: `yggdashboard/src/lib/format.test.ts`
- Create: `yggdashboard/src/lib/components/StatusBadge.svelte`
- Create: `yggdashboard/src/lib/components/MetricCard.svelte`
- Create: `yggdashboard/src/lib/components/CopyableKey.svelte`
- Create: `yggdashboard/src/lib/styles/tokens.css`

**Interfaces:**
- Consumes: nothing.
- Produces: `formatBytes/formatRate/formatLatency/formatUptime/formatPercent/truncateKey`, `<StatusBadge status>`, `<MetricCard label value sublabel?>`, `<CopyableKey value label? prefixLen? suffixLen?>`, and a shared CSS custom-properties file (dark, neutral, high-density palette) every later component imports. Used by every page task (17-21).

- [ ] **Step 1: Write the failing tests for format helpers**

Create `yggdashboard/src/lib/format.test.ts`:

```ts
import { describe, it, expect } from 'vitest';
import { formatBytes, formatRate, formatLatency, formatUptime, formatPercent, truncateKey } from './format';

describe('formatBytes', () => {
  it('formats bytes under 1KB as whole bytes', () => {
    expect(formatBytes(512)).toBe('512 B');
  });
  it('formats kilobytes', () => {
    expect(formatBytes(2048)).toBe('2.0 KB');
  });
  it('formats megabytes', () => {
    expect(formatBytes(5 * 1024 * 1024)).toBe('5.0 MB');
  });
  it('formats gigabytes', () => {
    expect(formatBytes(3 * 1024 * 1024 * 1024)).toBe('3.0 GB');
  });
});

describe('formatRate', () => {
  it('appends /s to a byte-rate value', () => {
    expect(formatRate(1024)).toBe('1.0 KB/s');
  });
});

describe('formatLatency', () => {
  it('converts nanoseconds to milliseconds', () => {
    expect(formatLatency(1_500_000)).toBe('1.5 ms');
  });
  it('renders an em-dash for null (no latency known)', () => {
    expect(formatLatency(null)).toBe('—');
  });
});

describe('formatUptime', () => {
  it('formats seconds only under a minute', () => {
    expect(formatUptime(45)).toBe('45s');
  });
  it('formats minutes and seconds under an hour', () => {
    expect(formatUptime(125)).toBe('2m 5s');
  });
  it('formats hours and minutes under a day', () => {
    expect(formatUptime(3 * 3600 + 20 * 60)).toBe('3h 20m');
  });
  it('formats days and hours at or over a day', () => {
    expect(formatUptime(3 * 86400 + 14 * 3600)).toBe('3d 14h');
  });
});

describe('formatPercent', () => {
  it('formats to one decimal place with a % sign', () => {
    expect(formatPercent(63.44)).toBe('63.4%');
  });
});

describe('truncateKey', () => {
  it('shortens a long key to prefix...suffix', () => {
    expect(truncateKey('abcdef1234567890', 8, 4)).toBe('abcdef12...7890');
  });
  it('returns a short key unchanged', () => {
    expect(truncateKey('short', 8, 4)).toBe('short');
  });
});
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `cd yggdashboard && npx vitest run src/lib/format.test.ts`
Expected: FAIL — `Cannot find module './format'`.

- [ ] **Step 3: Implement `yggdashboard/src/lib/format.ts`**

```ts
export function formatBytes(n: number): string {
  if (n >= 1024 ** 3) return (n / 1024 ** 3).toFixed(1) + ' GB';
  if (n >= 1024 ** 2) return (n / 1024 ** 2).toFixed(1) + ' MB';
  if (n >= 1024) return (n / 1024).toFixed(1) + ' KB';
  return Math.round(n) + ' B';
}

export function formatRate(bytesPerSecond: number): string {
  return formatBytes(bytesPerSecond) + '/s';
}

export function formatLatency(ns: number | null): string {
  if (ns === null) return '—';
  return (ns / 1e6).toFixed(1) + ' ms';
}

export function formatUptime(totalSeconds: number): string {
  const seconds = Math.floor(totalSeconds);
  const days = Math.floor(seconds / 86400);
  const hours = Math.floor((seconds % 86400) / 3600);
  const minutes = Math.floor((seconds % 3600) / 60);
  const secs = seconds % 60;
  if (days > 0) return `${days}d ${hours}h`;
  if (hours > 0) return `${hours}h ${minutes}m`;
  if (minutes > 0) return `${minutes}m ${secs}s`;
  return `${secs}s`;
}

export function formatPercent(n: number): string {
  return `${n.toFixed(1)}%`;
}

/** Shortens key to "prefix...suffix"; returns key unchanged if it's already that short or shorter. */
export function truncateKey(key: string, prefixLen = 8, suffixLen = 4): string {
  if (key.length <= prefixLen + suffixLen + 3) return key;
  return `${key.slice(0, prefixLen)}...${key.slice(-suffixLen)}`;
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `cd yggdashboard && npx vitest run src/lib/format.test.ts`
Expected: PASS, all 12 tests green.

- [ ] **Step 5: Create the shared style tokens**

Create `yggdashboard/src/lib/styles/tokens.css`:

```css
:root {
  --bg: #0d1117;
  --bg-raised: #161b22;
  --border: #30363d;
  --text: #e6edf3;
  --text-dim: #8b949e;
  --accent: #58a6ff;
  --ok: #3fb950;
  --warn: #d29922;
  --bad: #f85149;
  --mono: ui-monospace, 'SF Mono', Consolas, monospace;
  --sans: ui-sans-serif, system-ui, sans-serif;
  --radius: 6px;
  --gap: 0.75rem;
}

body {
  background: var(--bg);
  color: var(--text);
  font-family: var(--sans);
}
```

Import this once, in Task 17's root layout - not repeated per component.

- [ ] **Step 6: Create `yggdashboard/src/lib/components/StatusBadge.svelte`**

```svelte
<script lang="ts">
  let { status }: { status: 'online' | 'degraded' | 'disconnected' } = $props();

  const labels = { online: 'Online', degraded: 'Degraded', disconnected: 'Disconnected' } as const;
</script>

<span class="badge {status}">
  <span class="dot"></span>
  {labels[status]}
</span>

<style>
  .badge {
    display: inline-flex;
    align-items: center;
    gap: 0.4em;
    font-size: 0.85rem;
    font-weight: 600;
  }
  .dot {
    width: 0.6em;
    height: 0.6em;
    border-radius: 50%;
    background: currentColor;
  }
  .online {
    color: var(--ok);
  }
  .degraded {
    color: var(--warn);
  }
  .disconnected {
    color: var(--bad);
  }
</style>
```

- [ ] **Step 7: Create `yggdashboard/src/lib/components/MetricCard.svelte`**

```svelte
<script lang="ts">
  let { label, value, sublabel = '' }: { label: string; value: string; sublabel?: string } = $props();
</script>

<div class="card">
  <div class="label">{label}</div>
  <div class="value">{value}</div>
  {#if sublabel}
    <div class="sublabel">{sublabel}</div>
  {/if}
</div>

<style>
  .card {
    background: var(--bg-raised);
    border: 1px solid var(--border);
    border-radius: var(--radius);
    padding: 0.75rem 1rem;
  }
  .label {
    font-size: 0.75rem;
    text-transform: uppercase;
    letter-spacing: 0.04em;
    color: var(--text-dim);
  }
  .value {
    font-family: var(--mono);
    font-size: 1.4rem;
    font-weight: 600;
    margin-top: 0.2rem;
  }
  .sublabel {
    font-size: 0.75rem;
    color: var(--text-dim);
    margin-top: 0.15rem;
  }
</style>
```

- [ ] **Step 8: Create `yggdashboard/src/lib/components/CopyableKey.svelte`**

```svelte
<script lang="ts">
  import { truncateKey } from '$lib/format';

  let { value, prefixLen = 8, suffixLen = 4 }: { value: string; prefixLen?: number; suffixLen?: number } = $props();
  let copied = $state(false);

  async function copy() {
    await navigator.clipboard.writeText(value);
    copied = true;
    setTimeout(() => (copied = false), 1200);
  }
</script>

<span class="key">
  <span class="mono" title={value}>{truncateKey(value, prefixLen, suffixLen)}</span>
  <button type="button" onclick={copy} aria-label="Copy full value">
    {copied ? '✓' : 'copy'}
  </button>
</span>

<style>
  .key {
    display: inline-flex;
    align-items: center;
    gap: 0.4em;
  }
  .mono {
    font-family: var(--mono);
    font-size: 0.85em;
  }
  button {
    font-size: 0.7em;
    background: transparent;
    border: 1px solid var(--border);
    border-radius: 4px;
    color: var(--text-dim);
    padding: 0.05em 0.4em;
    cursor: pointer;
  }
  button:hover {
    color: var(--text);
    border-color: var(--accent);
  }
</style>
```

- [ ] **Step 9: Commit**

```bash
git add yggdashboard/src/lib/format.ts yggdashboard/src/lib/format.test.ts \
  yggdashboard/src/lib/styles/tokens.css yggdashboard/src/lib/components/StatusBadge.svelte \
  yggdashboard/src/lib/components/MetricCard.svelte yggdashboard/src/lib/components/CopyableKey.svelte
git commit -m "yggdashboard: add format helpers, style tokens, and shared status/metric/key components"
```

---

### Task 17: Root layout (nav + status bar) and the overview page (`/`)

**Files:**
- Create: `yggdashboard/src/routes/+layout.server.ts`
- Modify: `yggdashboard/src/routes/+layout.svelte`
- Create: `yggdashboard/src/lib/components/NavBar.svelte`
- Create: `yggdashboard/src/lib/components/TrafficChart.svelte`
- Create: `yggdashboard/src/lib/components/NodeIdentity.svelte`
- Modify: `yggdashboard/src/routes/+page.svelte`
- Create: `yggdashboard/src/routes/+page.server.ts`
- Test: `yggdashboard/src/lib/components/TrafficChart.test.ts`

**Interfaces:**
- Consumes: `computeStatus` (Task 14), `computeStats` (Task 14), `createStatusResource`/`createStatsResource` (Task 15), `StatusBadge`/`MetricCard`/`CopyableKey` (Task 16), `formatUptime`/`formatRate`/`formatPercent` (Task 16).
- Produces: the site-wide nav/status bar (present on every route via `+layout.svelte`) and the full overview page. Nothing downstream in this plan depends on this task's exports directly - later page tasks each follow the same `+page.server.ts` (SSR via a builder) + `+page.svelte` (hydrates, then polls via a Task 15 resource) pattern established here.

- [ ] **Step 1: Create `yggdashboard/src/routes/+layout.server.ts`**

```ts
import { poller } from '$lib/server/instance';
import { computeStatus } from '$lib/server/status';
import type { LayoutServerLoad } from './$types';

export const load: LayoutServerLoad = async () => {
  await poller.waitUntilReady(2000);
  return { status: computeStatus(poller.getSnapshot()) };
};
```

- [ ] **Step 2: Write the failing test for `TrafficChart`'s pure scaling logic**

`TrafficChart.svelte` itself isn't unit-tested here (Task 22 adds component-level render tests with mock data for every page/component per the spec's testing section) - but its point-scaling math is pure and worth testing in isolation before wiring it into markup. Create `yggdashboard/src/lib/components/TrafficChart.test.ts`:

```ts
import { describe, it, expect } from 'vitest';
import { scalePoints } from './TrafficChart.svelte';

describe('scalePoints', () => {
  it('maps a two-sample series across the full width and height', () => {
    const history = [
      { t: 0, rxRate: 0, txRate: 0, garlicRelayedRate: 0, garlicOriginatedRate: 0 },
      { t: 1000, rxRate: 100, txRate: 0, garlicRelayedRate: 0, garlicOriginatedRate: 0 }
    ];
    const points = scalePoints(history, 'rxRate', 100, 200, 100, 0);
    expect(points).toBe('0.0,200.0 100.0,0.0');
  });

  it('returns an empty string for fewer than two samples', () => {
    expect(scalePoints([], 'rxRate', 100, 200, 10, 0)).toBe('');
    expect(
      scalePoints([{ t: 0, rxRate: 1, txRate: 0, garlicRelayedRate: 0, garlicOriginatedRate: 0 }], 'rxRate', 100, 200, 10, 0)
    ).toBe('');
  });

  it('clamps against a maxValue of at least 1 to avoid division by zero when every sample is 0', () => {
    const history = [
      { t: 0, rxRate: 0, txRate: 0, garlicRelayedRate: 0, garlicOriginatedRate: 0 },
      { t: 1000, rxRate: 0, txRate: 0, garlicRelayedRate: 0, garlicOriginatedRate: 0 }
    ];
    expect(() => scalePoints(history, 'rxRate', 100, 200, 0, 0)).not.toThrow();
  });
});
```

- [ ] **Step 3: Run the test to verify it fails**

Run: `cd yggdashboard && npx vitest run src/lib/components/TrafficChart.test.ts`
Expected: FAIL — `scalePoints` isn't exported from `TrafficChart.svelte` yet (module doesn't exist).

- [ ] **Step 4: Create `yggdashboard/src/lib/components/TrafficChart.svelte`**

```svelte
<script lang="ts" module>
  import type { HistorySample } from '$lib/api-types';

  export type SeriesKey = 'rxRate' | 'txRate' | 'garlicRelayedRate' | 'garlicOriginatedRate';

  /**
   * Maps history's samples for one series onto an SVG polyline's
   * "x,y x,y ..." points string, scaled into [padding, width-padding] x
   * [padding, height-padding]. Pure and exported for direct unit
   * testing - the rest of this component is just markup around it.
   */
  export function scalePoints(
    history: HistorySample[],
    key: SeriesKey,
    width: number,
    height: number,
    maxValue: number,
    padding: number
  ): string {
    if (history.length < 2) return '';
    const safeMax = Math.max(1, maxValue);
    const minT = history[0].t;
    const maxT = history[history.length - 1].t;
    const spanT = Math.max(1, maxT - minT);
    return history
      .map((s) => {
        const x = padding + ((s.t - minT) / spanT) * (width - 2 * padding);
        const y = height - padding - (s[key] / safeMax) * (height - 2 * padding);
        return `${x.toFixed(1)},${y.toFixed(1)}`;
      })
      .join(' ');
  }
</script>

<script lang="ts">
  let { history }: { history: HistorySample[] } = $props();

  const SERIES: Array<{ key: SeriesKey; label: string; color: string }> = [
    { key: 'rxRate', label: 'Download', color: 'var(--accent)' },
    { key: 'txRate', label: 'Upload', color: 'var(--ok)' },
    { key: 'garlicRelayedRate', label: 'Garlic transit', color: 'var(--warn)' },
    { key: 'garlicOriginatedRate', label: 'Garlic originated', color: 'var(--bad)' }
  ];

  let enabled = $state<Record<SeriesKey, boolean>>({
    rxRate: true,
    txRate: true,
    garlicRelayedRate: true,
    garlicOriginatedRate: true
  });

  const width = 800;
  const height = 200;
  const padding = 8;

  let maxValue = $derived(
    Math.max(1, ...history.flatMap((s) => SERIES.filter((series) => enabled[series.key]).map((series) => s[series.key])))
  );

  function toggle(key: SeriesKey) {
    enabled[key] = !enabled[key];
  }
</script>

<div class="chart">
  <svg viewBox="0 0 {width} {height}" preserveAspectRatio="none" role="img" aria-label="Traffic over the last 5 minutes">
    {#if history.length < 2}
      <text x={width / 2} y={height / 2} text-anchor="middle" class="empty">Waiting for data…</text>
    {:else}
      {#each SERIES as series (series.key)}
        {#if enabled[series.key]}
          <polyline points={scalePoints(history, series.key, width, height, maxValue, padding)} fill="none" stroke={series.color} stroke-width="2" />
        {/if}
      {/each}
    {/if}
  </svg>
  <div class="legend">
    {#each SERIES as series (series.key)}
      <button type="button" class:enabled={enabled[series.key]} onclick={() => toggle(series.key)} style="--series-color: {series.color}">
        <span class="swatch"></span>
        {series.label}
      </button>
    {/each}
  </div>
</div>

<style>
  .chart {
    background: var(--bg-raised);
    border: 1px solid var(--border);
    border-radius: var(--radius);
    padding: 0.75rem;
  }
  svg {
    width: 100%;
    height: 200px;
    display: block;
  }
  .empty {
    fill: var(--text-dim);
    font-size: 14px;
  }
  .legend {
    display: flex;
    flex-wrap: wrap;
    gap: 0.75rem;
    margin-top: 0.5rem;
  }
  .legend button {
    display: inline-flex;
    align-items: center;
    gap: 0.35em;
    background: transparent;
    border: none;
    color: var(--text-dim);
    font-size: 0.8rem;
    cursor: pointer;
    opacity: 0.5;
  }
  .legend button.enabled {
    color: var(--text);
    opacity: 1;
  }
  .swatch {
    width: 0.7em;
    height: 0.7em;
    border-radius: 2px;
    background: var(--series-color);
  }
</style>
```

(The `<script module>` block is Svelte 5's mechanism for exporting plain functions/types from a `.svelte` file for both the component itself and outside importers like the test above.)

- [ ] **Step 5: Run the test to verify it passes**

Run: `cd yggdashboard && npx vitest run src/lib/components/TrafficChart.test.ts`
Expected: PASS, all 3 tests green.

- [ ] **Step 6: Create `yggdashboard/src/lib/components/NavBar.svelte`**

```svelte
<script lang="ts">
  import { page } from '$app/stores';

  const links = [
    { href: '/', label: 'Overview' },
    { href: '/connections', label: 'Connections' },
    { href: '/circuits', label: 'Circuits' },
    { href: '/garlic', label: 'Garlic' },
    { href: '/graph', label: 'Graph' }
  ];
</script>

<nav>
  {#each links as link (link.href)}
    <a href={link.href} class:active={$page.url.pathname === link.href}>{link.label}</a>
  {/each}
</nav>

<style>
  nav {
    display: flex;
    gap: 1rem;
    flex-wrap: wrap;
  }
  a {
    color: var(--text-dim);
    text-decoration: none;
    font-size: 0.9rem;
    padding: 0.25rem 0;
    border-bottom: 2px solid transparent;
  }
  a.active {
    color: var(--text);
    border-bottom-color: var(--accent);
  }
  a:hover {
    color: var(--text);
  }
</style>
```

- [ ] **Step 7: Create `yggdashboard/src/lib/components/NodeIdentity.svelte`**

```svelte
<script lang="ts">
  import CopyableKey from './CopyableKey.svelte';

  let {
    buildName,
    buildVersion,
    address,
    publicKey
  }: { buildName: string; buildVersion: string; address: string; publicKey: string } = $props();
</script>

<section class="identity">
  <h2>Yggdrasil</h2>
  <dl>
    <dt>Build</dt>
    <dd>{buildName} {buildVersion}</dd>
    <dt>Public key</dt>
    <dd><CopyableKey value={publicKey} /></dd>
    <dt>Address</dt>
    <dd class="mono">{address}</dd>
  </dl>
</section>

<style>
  .identity {
    background: var(--bg-raised);
    border: 1px solid var(--border);
    border-radius: var(--radius);
    padding: 0.75rem 1rem;
  }
  h2 {
    font-size: 0.9rem;
    margin: 0 0 0.5rem;
    color: var(--text-dim);
    text-transform: uppercase;
    letter-spacing: 0.04em;
  }
  dl {
    display: grid;
    grid-template-columns: max-content 1fr;
    gap: 0.3rem 1rem;
    margin: 0;
  }
  dt {
    color: var(--text-dim);
    font-size: 0.85rem;
  }
  .mono {
    font-family: var(--mono);
  }
</style>
```

- [ ] **Step 8: Replace `yggdashboard/src/routes/+layout.svelte`**

```svelte
<script lang="ts">
  import '$lib/styles/tokens.css';
  import NavBar from '$lib/components/NavBar.svelte';
  import StatusBadge from '$lib/components/StatusBadge.svelte';
  import { createStatusResource } from '$lib/stores/dashboard.svelte';
  import { formatUptime } from '$lib/format';

  let { data, children } = $props();

  const statusResource = createStatusResource();
  $effect(() => {
    statusResource.start();
    return () => statusResource.stop();
  });

  // SSR-rendered data from +layout.server.ts is the initial paint (no
  // JS required); once hydrated, the live-polled resource takes over -
  // falling back to the SSR value if a poll hasn't landed yet.
  let status = $derived(statusResource.data ?? data.status);
</script>

<div class="shell">
  <header>
    <div class="brand">YGGDRASIL / GARLIC</div>
    <StatusBadge status={status.status} />
    <span class="meta">uptime {formatUptime(status.uptime)}</span>
    <span class="meta">v{status.buildVersion}</span>
    <span class="meta">Garlic {status.garlicEnabled ? 'enabled' : 'disabled'}</span>
    <span class="meta conn" class:offline={!statusResource.connected}>
      {statusResource.connected ? 'connected' : 'reconnecting…'}
      {#if statusResource.latencyMs !== null}
        · {statusResource.latencyMs}ms
      {/if}
    </span>
  </header>
  <NavBar />
  <main>
    {@render children()}
  </main>
</div>

<style>
  .shell {
    max-width: 1200px;
    margin: 0 auto;
    padding: 1rem 1.5rem 3rem;
  }
  header {
    display: flex;
    align-items: center;
    gap: 1rem;
    flex-wrap: wrap;
    padding-bottom: 0.75rem;
    border-bottom: 1px solid var(--border);
    margin-bottom: 0.75rem;
  }
  .brand {
    font-weight: 700;
    letter-spacing: 0.04em;
    font-size: 0.9rem;
  }
  .meta {
    font-size: 0.8rem;
    color: var(--text-dim);
  }
  .conn.offline {
    color: var(--bad);
  }
  nav {
    margin-bottom: 1rem;
  }
</style>
```

- [ ] **Step 9: Create `yggdashboard/src/routes/+page.server.ts`**

```ts
import { poller } from '$lib/server/instance';
import { computeStats } from '$lib/server/stats';
import type { PageServerLoad } from './$types';

export const load: PageServerLoad = async () => {
  await poller.waitUntilReady(2000);
  const snap = poller.getSnapshot();
  return {
    stats: computeStats(snap),
    self: { buildName: snap.self.build_name, buildVersion: snap.self.build_version, address: snap.self.address, key: snap.self.key },
    peerCount: snap.peers.length,
    peersUp: snap.peers.filter((p) => p.up).length
  };
};
```

- [ ] **Step 10: Replace `yggdashboard/src/routes/+page.svelte`**

```svelte
<script lang="ts">
  import MetricCard from '$lib/components/MetricCard.svelte';
  import NodeIdentity from '$lib/components/NodeIdentity.svelte';
  import TrafficChart from '$lib/components/TrafficChart.svelte';
  import { createStatsResource } from '$lib/stores/dashboard.svelte';
  import { formatRate, formatPercent } from '$lib/format';

  let { data } = $props();

  const statsResource = createStatsResource();
  $effect(() => {
    statsResource.start();
    return () => statsResource.stop();
  });

  let stats = $derived(statsResource.data ?? data.stats);
</script>

<svelte:head>
  <title>yggdashboard</title>
</svelte:head>

<section class="grid">
  <MetricCard label="Peers" value={`${data.peersUp} / ${data.peerCount}`} sublabel="up / known" />
  <MetricCard label="Download" value={formatRate(stats.rxRate)} sublabel="peer-link aggregate" />
  <MetricCard label="Upload" value={formatRate(stats.txRate)} sublabel="peer-link aggregate" />
  <MetricCard
    label="Garlic transit"
    value={stats.garlic.enabled ? formatPercent(stats.garlic.transitPercent) : 'disabled'}
    sublabel="share of Garlic traffic relayed for others"
  />
</section>

<NodeIdentity buildName={data.self.buildName} buildVersion={data.self.buildVersion} address={data.self.address} publicKey={data.self.key} />

<h2 class="section-title">Traffic</h2>
<TrafficChart history={stats.history} />

{#if stats.garlic.enabled}
  <section class="garlic-summary">
    <MetricCard label="Garlic originated" value={formatRate(stats.garlic.originatedRate)} />
    <MetricCard label="Garlic relayed" value={formatRate(stats.garlic.relayedRate)} />
  </section>
{/if}

<style>
  .grid {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(160px, 1fr));
    gap: 0.75rem;
    margin-bottom: 1rem;
  }
  .garlic-summary {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(160px, 1fr));
    gap: 0.75rem;
    margin-top: 1rem;
  }
  .section-title {
    font-size: 0.9rem;
    text-transform: uppercase;
    letter-spacing: 0.04em;
    color: var(--text-dim);
    margin: 1.5rem 0 0.5rem;
  }
</style>
```

- [ ] **Step 11: Manually verify the scaffold renders with real data**

```bash
cd yggdashboard && ADMIN_SOCKET=unix:///var/run/yggdrasil.sock npm run dev -- --port 5173 &
sleep 3
curl -s http://localhost:5173 | grep -q "YGGDRASIL / GARLIC" && echo OVERVIEW_OK
kill %1
```
Expected: prints `OVERVIEW_OK`. Requires a real reachable `yggdrasil` admin socket for full data — a `Connecting…`/zeroed page without one is still expected to render without crashing (Task 22 formally tests the disconnected state with mocks).

- [ ] **Step 12: Commit**

```bash
git add yggdashboard/src/routes/+layout.server.ts yggdashboard/src/routes/+layout.svelte \
  yggdashboard/src/lib/components/NavBar.svelte yggdashboard/src/lib/components/TrafficChart.svelte \
  yggdashboard/src/lib/components/TrafficChart.test.ts yggdashboard/src/lib/components/NodeIdentity.svelte \
  yggdashboard/src/routes/+page.svelte yggdashboard/src/routes/+page.server.ts
git commit -m "yggdashboard: add nav/status bar layout and the overview page"
```

---

### Task 18: Connections page (`/connections`)

**Files:**
- Create: `yggdashboard/src/lib/components/PeerTable.svelte`
- Test: `yggdashboard/src/lib/components/PeerTable.test.ts`
- Create: `yggdashboard/src/lib/components/PeerDetail.svelte`
- Create: `yggdashboard/src/routes/connections/+page.server.ts`
- Create: `yggdashboard/src/routes/connections/+page.svelte`

**Interfaces:**
- Consumes: `computePeers` (Task 14), `createPeersResource` (Task 15), `ApiPeer` (Task 15), `CopyableKey`/format helpers (Task 16).
- Produces: the connections page. Nothing downstream depends on this task's exports.

- [ ] **Step 1: Write the failing test for sorting/filtering logic**

Create `yggdashboard/src/lib/components/PeerTable.test.ts`:

```ts
import { describe, it, expect } from 'vitest';
import { filterAndSortPeers } from './PeerTable.svelte';
import type { ApiPeer } from '$lib/api-types';

function peer(overrides: Partial<ApiPeer>): ApiPeer {
  return {
    key: 'key',
    remote: null,
    address: null,
    up: true,
    inbound: false,
    bytesRecvd: 0,
    bytesSent: 0,
    rateRecvd: 0,
    rateSent: 0,
    uptime: 0,
    latencyNs: null,
    lastError: null,
    garlicCapable: false,
    ...overrides
  };
}

describe('filterAndSortPeers', () => {
  it('filters by substring match on key, remote, or address', () => {
    const peers = [peer({ key: 'abc' }), peer({ key: 'xyz', remote: 'tls://abc.example' }), peer({ key: 'zzz', address: '200::abc' })];
    expect(filterAndSortPeers(peers, 'abc', 'uptime', -1)).toHaveLength(3);
    expect(filterAndSortPeers(peers, 'nomatch', 'uptime', -1)).toHaveLength(0);
  });

  it('sorts by uptime descending by default', () => {
    const peers = [peer({ key: 'a', uptime: 10 }), peer({ key: 'b', uptime: 100 }), peer({ key: 'c', uptime: 50 })];
    const sorted = filterAndSortPeers(peers, '', 'uptime', -1);
    expect(sorted.map((p) => p.key)).toEqual(['b', 'c', 'a']);
  });

  it('sorts ascending when direction is 1', () => {
    const peers = [peer({ key: 'a', rateRecvd: 30 }), peer({ key: 'b', rateRecvd: 10 })];
    const sorted = filterAndSortPeers(peers, '', 'rateRecvd', 1);
    expect(sorted.map((p) => p.key)).toEqual(['b', 'a']);
  });

  it('returns an empty array for an empty peer list', () => {
    expect(filterAndSortPeers([], '', 'uptime', -1)).toEqual([]);
  });
});
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `cd yggdashboard && npx vitest run src/lib/components/PeerTable.test.ts`
Expected: FAIL — `filterAndSortPeers` isn't exported yet.

- [ ] **Step 3: Create `yggdashboard/src/lib/components/PeerTable.svelte`**

```svelte
<script lang="ts" module>
  import type { ApiPeer } from '$lib/api-types';

  export type PeerSortKey = 'key' | 'uptime' | 'rateRecvd' | 'rateSent';

  /** Pure filter+sort logic, exported for direct unit testing. */
  export function filterAndSortPeers(peers: ApiPeer[], filter: string, sortKey: PeerSortKey, sortDir: 1 | -1): ApiPeer[] {
    const q = filter.trim().toLowerCase();
    const filtered = q
      ? peers.filter(
          (p) => p.key.toLowerCase().includes(q) || (p.remote ?? '').toLowerCase().includes(q) || (p.address ?? '').toLowerCase().includes(q)
        )
      : peers;
    return [...filtered].sort((a, b) => {
      const av = a[sortKey];
      const bv = b[sortKey];
      if (av < bv) return -sortDir;
      if (av > bv) return sortDir;
      return 0;
    });
  }
</script>

<script lang="ts">
  import { formatRate, formatLatency, formatUptime, truncateKey } from '$lib/format';

  let { peers, onSelect }: { peers: ApiPeer[]; onSelect: (peer: ApiPeer) => void } = $props();

  let filter = $state('');
  let sortKey = $state<PeerSortKey>('uptime');
  let sortDir = $state<1 | -1>(-1);

  let rows = $derived(filterAndSortPeers(peers, filter, sortKey, sortDir));

  function setSort(key: PeerSortKey) {
    if (sortKey === key) {
      sortDir = sortDir === 1 ? -1 : 1;
    } else {
      sortKey = key;
      sortDir = -1;
    }
  }
</script>

<div class="table-wrap">
  <input type="search" placeholder="Filter by key, remote, or address…" bind:value={filter} />
  {#if peers.length === 0}
    <p class="empty">No peers connected.</p>
  {:else if rows.length === 0}
    <p class="empty">No peers match this filter.</p>
  {:else}
    <table>
      <thead>
        <tr>
          <th>Peer</th>
          <th>Transport</th>
          <th>State</th>
          <th><button type="button" onclick={() => setSort('uptime')}>Uptime</button></th>
          <th>Latency</th>
          <th><button type="button" onclick={() => setSort('rateRecvd')}>RX</button></th>
          <th><button type="button" onclick={() => setSort('rateSent')}>TX</button></th>
          <th>Garlic</th>
        </tr>
      </thead>
      <tbody>
        {#each rows as peer (peer.key + (peer.remote ?? ''))}
          <tr onclick={() => onSelect(peer)}>
            <td class="mono">{truncateKey(peer.key)}</td>
            <td>{peer.remote ?? '—'}</td>
            <td class:up={peer.up} class:down={!peer.up}>{peer.up ? 'up' : 'down'} · {peer.inbound ? 'in' : 'out'}</td>
            <td>{formatUptime(peer.uptime)}</td>
            <td>{formatLatency(peer.latencyNs)}</td>
            <td>{formatRate(peer.rateRecvd)}</td>
            <td>{formatRate(peer.rateSent)}</td>
            <td>{peer.garlicCapable ? '✓' : '—'}</td>
          </tr>
        {/each}
      </tbody>
    </table>
  {/if}
</div>

<style>
  .table-wrap {
    background: var(--bg-raised);
    border: 1px solid var(--border);
    border-radius: var(--radius);
    padding: 0.75rem;
  }
  input[type='search'] {
    width: 100%;
    background: var(--bg);
    border: 1px solid var(--border);
    border-radius: 4px;
    color: var(--text);
    padding: 0.4rem 0.6rem;
    margin-bottom: 0.6rem;
    font-size: 0.85rem;
  }
  table {
    width: 100%;
    border-collapse: collapse;
    font-size: 0.85rem;
  }
  th,
  td {
    text-align: left;
    padding: 0.35rem 0.6rem;
    border-bottom: 1px solid var(--border);
    white-space: nowrap;
  }
  th button {
    background: none;
    border: none;
    color: var(--text-dim);
    font: inherit;
    cursor: pointer;
    padding: 0;
  }
  tbody tr {
    cursor: pointer;
  }
  tbody tr:hover {
    background: rgba(255, 255, 255, 0.03);
  }
  .mono {
    font-family: var(--mono);
  }
  .up {
    color: var(--ok);
  }
  .down {
    color: var(--bad);
  }
  .empty {
    color: var(--text-dim);
    font-size: 0.85rem;
    padding: 0.5rem 0;
  }
  @media (max-width: 640px) {
    .table-wrap {
      overflow-x: auto;
    }
  }
</style>
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `cd yggdashboard && npx vitest run src/lib/components/PeerTable.test.ts`
Expected: PASS, all 4 tests green.

- [ ] **Step 5: Create `yggdashboard/src/lib/components/PeerDetail.svelte`**

```svelte
<script lang="ts">
  import type { ApiPeer } from '$lib/api-types';
  import CopyableKey from './CopyableKey.svelte';
  import { formatBytes, formatRate, formatLatency, formatUptime } from '$lib/format';

  let { peer, onClose }: { peer: ApiPeer; onClose: () => void } = $props();
</script>

<aside class="detail">
  <button type="button" class="close" onclick={onClose} aria-label="Close">×</button>
  <h2>Peer detail</h2>
  <dl>
    <dt>Public key</dt>
    <dd><CopyableKey value={peer.key} prefixLen={16} suffixLen={8} /></dd>
    <dt>Transport</dt>
    <dd>{peer.remote ?? '—'}</dd>
    <dt>Address</dt>
    <dd class="mono">{peer.address ?? '—'}</dd>
    <dt>State</dt>
    <dd>{peer.up ? 'up' : 'down'} · {peer.inbound ? 'inbound' : 'outbound'}</dd>
    <dt>Uptime</dt>
    <dd>{formatUptime(peer.uptime)}</dd>
    <dt>Latency</dt>
    <dd>{formatLatency(peer.latencyNs)}</dd>
    <dt>RX rate / total</dt>
    <dd>{formatRate(peer.rateRecvd)} / {formatBytes(peer.bytesRecvd)}</dd>
    <dt>TX rate / total</dt>
    <dd>{formatRate(peer.rateSent)} / {formatBytes(peer.bytesSent)}</dd>
    <dt>Garlic capable</dt>
    <dd>{peer.garlicCapable ? 'Yes' : 'No'}</dd>
    {#if peer.lastError}
      <dt>Last error</dt>
      <dd class="error">{peer.lastError}</dd>
    {/if}
  </dl>
</aside>

<style>
  .detail {
    background: var(--bg-raised);
    border: 1px solid var(--border);
    border-radius: var(--radius);
    padding: 1rem;
    position: relative;
  }
  .close {
    position: absolute;
    top: 0.5rem;
    right: 0.5rem;
    background: none;
    border: none;
    color: var(--text-dim);
    font-size: 1.2rem;
    cursor: pointer;
  }
  h2 {
    font-size: 0.9rem;
    text-transform: uppercase;
    letter-spacing: 0.04em;
    color: var(--text-dim);
    margin: 0 0 0.75rem;
  }
  dl {
    display: grid;
    grid-template-columns: max-content 1fr;
    gap: 0.35rem 1rem;
    margin: 0;
  }
  dt {
    color: var(--text-dim);
    font-size: 0.8rem;
  }
  .mono {
    font-family: var(--mono);
  }
  .error {
    color: var(--bad);
  }
</style>
```

- [ ] **Step 6: Create `yggdashboard/src/routes/connections/+page.server.ts`**

```ts
import { poller } from '$lib/server/instance';
import { computePeers } from '$lib/server/peers';
import type { PageServerLoad } from './$types';

export const load: PageServerLoad = async () => {
  await poller.waitUntilReady(2000);
  return { peers: computePeers(poller.getSnapshot()) };
};
```

- [ ] **Step 7: Create `yggdashboard/src/routes/connections/+page.svelte`**

```svelte
<script lang="ts">
  import PeerTable from '$lib/components/PeerTable.svelte';
  import PeerDetail from '$lib/components/PeerDetail.svelte';
  import { createPeersResource } from '$lib/stores/dashboard.svelte';
  import type { ApiPeer } from '$lib/api-types';

  let { data } = $props();

  const peersResource = createPeersResource();
  $effect(() => {
    peersResource.start();
    return () => peersResource.stop();
  });

  let peers = $derived(peersResource.data?.peers ?? data.peers.peers);
  let selected = $state<ApiPeer | null>(null);
</script>

<svelte:head>
  <title>yggdashboard · connections</title>
</svelte:head>

<div class="layout">
  <div class="table-col">
    <PeerTable {peers} onSelect={(p) => (selected = p)} />
  </div>
  {#if selected}
    <div class="detail-col">
      <PeerDetail peer={selected} onClose={() => (selected = null)} />
    </div>
  {/if}
</div>

<style>
  .layout {
    display: grid;
    grid-template-columns: 1fr;
    gap: 1rem;
  }
  @media (min-width: 900px) {
    .layout {
      grid-template-columns: 1fr 320px;
    }
  }
</style>
```

- [ ] **Step 8: Commit**

```bash
git add yggdashboard/src/lib/components/PeerTable.svelte yggdashboard/src/lib/components/PeerTable.test.ts \
  yggdashboard/src/lib/components/PeerDetail.svelte yggdashboard/src/routes/connections
git commit -m "yggdashboard: add connections page with sortable/filterable peer table and detail panel"
```

---

### Task 19: Circuits page (`/circuits`)

**Files:**
- Create: `yggdashboard/src/lib/components/CircuitTable.svelte`
- Test: `yggdashboard/src/lib/components/CircuitTable.test.ts`
- Create: `yggdashboard/src/lib/components/CircuitDetail.svelte`
- Create: `yggdashboard/src/routes/circuits/+page.server.ts`
- Create: `yggdashboard/src/routes/circuits/+page.svelte`

**Interfaces:**
- Consumes: `computeCircuits` (Task 14), `createCircuitsResource` (Task 15), `OriginatedCircuit`/`RelayedCircuit` (Task 15), `CopyableKey`/format helpers (Task 16).
- Produces: the circuits page. Nothing downstream depends on this task's exports.

This is where the protocol's real privacy boundary must be visible in the UI, not just the data: an **originated** circuit shows its full chosen hop chain (this node built it, already knows it). A **relayed** circuit shows only `Previous → LOCAL → Next` - never a fabricated full path, because a relay genuinely never learns more than that.

- [ ] **Step 1: Write the failing test for age/remaining-lifetime math**

Create `yggdashboard/src/lib/components/CircuitTable.test.ts`:

```ts
import { describe, it, expect } from 'vitest';
import { ageSeconds, remainingSeconds } from './CircuitTable.svelte';

describe('ageSeconds', () => {
  it('returns elapsed seconds since createdAt', () => {
    const createdAt = new Date(Date.now() - 65_000).toISOString();
    expect(ageSeconds(createdAt, Date.now())).toBeCloseTo(65, 0);
  });
});

describe('remainingSeconds', () => {
  it('returns seconds until expiresAt', () => {
    const expiresAt = new Date(Date.now() + 30_000).toISOString();
    expect(remainingSeconds(expiresAt, Date.now())).toBeCloseTo(30, 0);
  });

  it('clamps to zero once past expiry', () => {
    const expiresAt = new Date(Date.now() - 5_000).toISOString();
    expect(remainingSeconds(expiresAt, Date.now())).toBe(0);
  });
});
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `cd yggdashboard && npx vitest run src/lib/components/CircuitTable.test.ts`
Expected: FAIL — `ageSeconds`/`remainingSeconds` aren't exported yet.

- [ ] **Step 3: Create `yggdashboard/src/lib/components/CircuitTable.svelte`**

```svelte
<script lang="ts" module>
  export function ageSeconds(createdAt: string, now: number): number {
    return Math.max(0, (now - new Date(createdAt).getTime()) / 1000);
  }

  export function remainingSeconds(expiresAt: string, now: number): number {
    return Math.max(0, (new Date(expiresAt).getTime() - now) / 1000);
  }
</script>

<script lang="ts">
  import type { OriginatedCircuit, RelayedCircuit } from '$lib/api-types';
  import { formatBytes, formatUptime, truncateKey } from '$lib/format';

  let {
    originated,
    relayed,
    onSelectOriginated,
    onSelectRelayed
  }: {
    originated: OriginatedCircuit[];
    relayed: RelayedCircuit[];
    onSelectOriginated: (c: OriginatedCircuit) => void;
    onSelectRelayed: (c: RelayedCircuit) => void;
  } = $props();

  const now = Date.now();
</script>

<section>
  <h2>Originated ({originated.length})</h2>
  <p class="note">Circuits this node built - the full hop chain is shown because this node chose it and already knows it.</p>
  {#if originated.length === 0}
    <p class="empty">No originated circuits.</p>
  {:else}
    <table>
      <thead>
        <tr>
          <th>Circuit</th>
          <th>Path</th>
          <th>State</th>
          <th>Age</th>
          <th>Remaining</th>
          <th>Packets</th>
          <th>Bytes</th>
        </tr>
      </thead>
      <tbody>
        {#each originated as c (c.circuitId)}
          <tr onclick={() => onSelectOriginated(c)}>
            <td class="mono">{truncateKey(c.circuitId, 6, 4)}</td>
            <td class="mono">LOCAL → {c.hops.map((h) => truncateKey(h, 4, 2)).join(' → ')}</td>
            <td class:up={!c.closed} class:down={c.closed}>{c.closed ? 'closed' : 'active'}</td>
            <td>{formatUptime(ageSeconds(c.createdAt, now))}</td>
            <td>{formatUptime(remainingSeconds(c.expiresAt, now))}</td>
            <td>{c.packets}</td>
            <td>{formatBytes(c.bytes)}</td>
          </tr>
        {/each}
      </tbody>
    </table>
  {/if}
</section>

<section>
  <h2>Relayed ({relayed.length})</h2>
  <p class="note">Circuits this node relays for others - only the immediate previous/next hop is ever shown, because that's all a relay actually knows.</p>
  {#if relayed.length === 0}
    <p class="empty">No relayed circuits.</p>
  {:else}
    <table>
      <thead>
        <tr>
          <th>Circuit</th>
          <th>Path</th>
          <th>First seen</th>
          <th>Last active</th>
          <th>Packets</th>
          <th>Bytes</th>
        </tr>
      </thead>
      <tbody>
        {#each relayed as c (c.circuitId)}
          <tr onclick={() => onSelectRelayed(c)}>
            <td class="mono">{truncateKey(c.circuitId, 6, 4)}</td>
            <td class="mono">{truncateKey(c.previousHop, 4, 2)} → LOCAL → {truncateKey(c.nextHop, 4, 2)}</td>
            <td>{formatUptime(ageSeconds(c.firstSeen, now))} ago</td>
            <td>{formatUptime(ageSeconds(c.lastActive, now))} ago</td>
            <td>{c.packetsRelayed}</td>
            <td>{formatBytes(c.bytesRelayed)}</td>
          </tr>
        {/each}
      </tbody>
    </table>
  {/if}
</section>

<style>
  section {
    background: var(--bg-raised);
    border: 1px solid var(--border);
    border-radius: var(--radius);
    padding: 0.75rem;
    margin-bottom: 1rem;
  }
  h2 {
    font-size: 0.9rem;
    text-transform: uppercase;
    letter-spacing: 0.04em;
    color: var(--text-dim);
    margin: 0 0 0.25rem;
  }
  .note {
    font-size: 0.75rem;
    color: var(--text-dim);
    margin: 0 0 0.5rem;
  }
  table {
    width: 100%;
    border-collapse: collapse;
    font-size: 0.85rem;
  }
  th,
  td {
    text-align: left;
    padding: 0.35rem 0.6rem;
    border-bottom: 1px solid var(--border);
    white-space: nowrap;
  }
  tbody tr {
    cursor: pointer;
  }
  tbody tr:hover {
    background: rgba(255, 255, 255, 0.03);
  }
  .mono {
    font-family: var(--mono);
  }
  .up {
    color: var(--ok);
  }
  .down {
    color: var(--text-dim);
  }
  .empty {
    color: var(--text-dim);
    font-size: 0.85rem;
  }
  @media (max-width: 640px) {
    section {
      overflow-x: auto;
    }
  }
</style>
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `cd yggdashboard && npx vitest run src/lib/components/CircuitTable.test.ts`
Expected: PASS, all 3 tests green.

- [ ] **Step 5: Create `yggdashboard/src/lib/components/CircuitDetail.svelte`**

```svelte
<script lang="ts">
  import type { OriginatedCircuit, RelayedCircuit } from '$lib/api-types';
  import CopyableKey from './CopyableKey.svelte';
  import { formatBytes } from '$lib/format';

  let {
    circuit,
    onClose
  }: { circuit: { kind: 'originated'; data: OriginatedCircuit } | { kind: 'relayed'; data: RelayedCircuit }; onClose: () => void } = $props();
</script>

<aside class="detail">
  <button type="button" class="close" onclick={onClose} aria-label="Close">×</button>
  <h2>Circuit detail</h2>
  <dl>
    <dt>Circuit ID</dt>
    <dd><CopyableKey value={circuit.data.circuitId} prefixLen={8} suffixLen={4} /></dd>
    <dt>Role</dt>
    <dd>{circuit.kind === 'originated' ? 'Originator' : 'Relay'}</dd>
    {#if circuit.kind === 'originated'}
      <dt>Hops</dt>
      <dd class="mono">
        LOCAL
        {#each circuit.data.hops as hop (hop)}
          → <CopyableKey value={hop} prefixLen={6} suffixLen={2} />
        {/each}
      </dd>
      <dt>State</dt>
      <dd>{circuit.data.closed ? 'Closed' : 'Active'}</dd>
      <dt>Created</dt>
      <dd>{new Date(circuit.data.createdAt).toLocaleString()}</dd>
      <dt>Expires</dt>
      <dd>{new Date(circuit.data.expiresAt).toLocaleString()}</dd>
      <dt>Packets / Bytes</dt>
      <dd>{circuit.data.packets} / {formatBytes(circuit.data.bytes)}</dd>
    {:else}
      <dt>Previous hop</dt>
      <dd><CopyableKey value={circuit.data.previousHop} prefixLen={8} suffixLen={4} /></dd>
      <dt>Next hop</dt>
      <dd><CopyableKey value={circuit.data.nextHop} prefixLen={8} suffixLen={4} /></dd>
      <dt>First seen</dt>
      <dd>{new Date(circuit.data.firstSeen).toLocaleString()}</dd>
      <dt>Last active</dt>
      <dd>{new Date(circuit.data.lastActive).toLocaleString()}</dd>
      <dt>Packets / Bytes relayed</dt>
      <dd>{circuit.data.packetsRelayed} / {formatBytes(circuit.data.bytesRelayed)}</dd>
      <dt class="note-row" colspan="2">
        This node only ever knows its own two neighbors on this circuit - not the full path.
      </dt>
    {/if}
  </dl>
</aside>

<style>
  .detail {
    background: var(--bg-raised);
    border: 1px solid var(--border);
    border-radius: var(--radius);
    padding: 1rem;
    position: relative;
  }
  .close {
    position: absolute;
    top: 0.5rem;
    right: 0.5rem;
    background: none;
    border: none;
    color: var(--text-dim);
    font-size: 1.2rem;
    cursor: pointer;
  }
  h2 {
    font-size: 0.9rem;
    text-transform: uppercase;
    letter-spacing: 0.04em;
    color: var(--text-dim);
    margin: 0 0 0.75rem;
  }
  dl {
    display: grid;
    grid-template-columns: max-content 1fr;
    gap: 0.35rem 1rem;
    margin: 0;
  }
  dt {
    color: var(--text-dim);
    font-size: 0.8rem;
  }
  .mono {
    font-family: var(--mono);
  }
  .note-row {
    grid-column: 1 / -1;
    font-size: 0.75rem;
    color: var(--text-dim);
    margin-top: 0.4rem;
  }
</style>
```

Note: a `<dt>` isn't a valid host for `colspan` (that's a table attribute) — this is decorative-only markup inside a `<dl>`, so drop the `colspan="2"` attribute; it has no effect either way but isn't valid here. Use `<dt class="note-row">` alone.

- [ ] **Step 6: Create `yggdashboard/src/routes/circuits/+page.server.ts`**

```ts
import { poller } from '$lib/server/instance';
import { computeCircuits } from '$lib/server/circuits';
import type { PageServerLoad } from './$types';

export const load: PageServerLoad = async () => {
  await poller.waitUntilReady(2000);
  return { circuits: computeCircuits(poller.getSnapshot()) };
};
```

- [ ] **Step 7: Create `yggdashboard/src/routes/circuits/+page.svelte`**

```svelte
<script lang="ts">
  import CircuitTable from '$lib/components/CircuitTable.svelte';
  import CircuitDetail from '$lib/components/CircuitDetail.svelte';
  import { createCircuitsResource } from '$lib/stores/dashboard.svelte';
  import type { OriginatedCircuit, RelayedCircuit } from '$lib/api-types';

  let { data } = $props();

  const circuitsResource = createCircuitsResource();
  $effect(() => {
    circuitsResource.start();
    return () => circuitsResource.stop();
  });

  let circuits = $derived(circuitsResource.data ?? data.circuits);
  let selected = $state<{ kind: 'originated'; data: OriginatedCircuit } | { kind: 'relayed'; data: RelayedCircuit } | null>(null);
</script>

<svelte:head>
  <title>yggdashboard · circuits</title>
</svelte:head>

{#if !circuits.enabled}
  <p class="disabled-note">Garlic is disabled on this node - no circuits to show.</p>
{:else}
  <div class="layout">
    <div class="table-col">
      <CircuitTable
        originated={circuits.originated}
        relayed={circuits.relayed}
        onSelectOriginated={(c) => (selected = { kind: 'originated', data: c })}
        onSelectRelayed={(c) => (selected = { kind: 'relayed', data: c })}
      />
    </div>
    {#if selected}
      <div class="detail-col">
        <CircuitDetail circuit={selected} onClose={() => (selected = null)} />
      </div>
    {/if}
  </div>
{/if}

<style>
  .disabled-note {
    color: var(--text-dim);
  }
  .layout {
    display: grid;
    grid-template-columns: 1fr;
    gap: 1rem;
  }
  @media (min-width: 900px) {
    .layout {
      grid-template-columns: 1fr 320px;
    }
  }
</style>
```

- [ ] **Step 8: Commit**

```bash
git add yggdashboard/src/lib/components/CircuitTable.svelte yggdashboard/src/lib/components/CircuitTable.test.ts \
  yggdashboard/src/lib/components/CircuitDetail.svelte yggdashboard/src/routes/circuits
git commit -m "yggdashboard: add circuits page, respecting the originator-vs-relay visibility boundary"
```

---

### Task 20: Garlic overview page (`/garlic`)

**Files:**
- Create: `yggdashboard/src/lib/components/GarlicPanel.svelte`
- Create: `yggdashboard/src/lib/components/SecurityCounters.svelte`
- Create: `yggdashboard/src/routes/garlic/+page.server.ts`
- Create: `yggdashboard/src/routes/garlic/+page.svelte`

**Interfaces:**
- Consumes: `computeGarlic` (Task 14), `createGarlicResource` (Task 15), `GarlicResponse` (Task 15), `CopyableKey`/`MetricCard`/format helpers (Task 16).
- Produces: the Garlic overview page. Nothing downstream depends on this task's exports.

- [ ] **Step 1: Create `yggdashboard/src/lib/components/SecurityCounters.svelte`**

No dedicated unit test - this is a direct, non-branching render of five numbers (Task 22 adds a mock-data render test for this component alongside every other page/component, per the spec's testing section).

```svelte
<script lang="ts">
  import type { GarlicSecurityCounters } from '$lib/api-types';

  let { counters }: { counters: GarlicSecurityCounters } = $props();

  const rows: Array<{ key: keyof GarlicSecurityCounters; label: string }> = [
    { key: 'replayDrops', label: 'Replay drops' },
    { key: 'authFailures', label: 'Auth failures' },
    { key: 'malformedPackets', label: 'Malformed packets' },
    { key: 'expiredPackets', label: 'Expired packets' },
    { key: 'relayTableFull', label: 'Relay table full' }
  ];
</script>

<section class="security">
  <h2>Security</h2>
  <dl>
    {#each rows as row (row.key)}
      <dt>{row.label}</dt>
      <dd>{counters[row.key]}</dd>
    {/each}
  </dl>
  <p class="note">Cumulative since this node last started. Local-only - never sent over the wire, and no field here reveals *which specific packet* failed, only the count in each category.</p>
</section>

<style>
  .security {
    background: var(--bg-raised);
    border: 1px solid var(--border);
    border-radius: var(--radius);
    padding: 0.75rem 1rem;
  }
  h2 {
    font-size: 0.9rem;
    text-transform: uppercase;
    letter-spacing: 0.04em;
    color: var(--text-dim);
    margin: 0 0 0.5rem;
  }
  dl {
    display: grid;
    grid-template-columns: max-content 1fr;
    gap: 0.25rem 1rem;
    margin: 0;
    font-family: var(--mono);
    font-size: 0.9rem;
  }
  dt {
    font-family: var(--sans);
    color: var(--text-dim);
    font-size: 0.85rem;
  }
  .note {
    font-family: var(--sans);
    font-size: 0.75rem;
    color: var(--text-dim);
    margin: 0.75rem 0 0;
  }
</style>
```

- [ ] **Step 2: Create `yggdashboard/src/lib/components/GarlicPanel.svelte`**

```svelte
<script lang="ts">
  import type { GarlicResponse } from '$lib/api-types';
  import CopyableKey from './CopyableKey.svelte';
  import MetricCard from './MetricCard.svelte';
  import SecurityCounters from './SecurityCounters.svelte';
  import { formatBytes } from '$lib/format';

  let { garlic }: { garlic: GarlicResponse } = $props();
</script>

<div class="grid">
  <MetricCard label="Garlic" value={garlic.enabled ? 'Enabled' : 'Disabled'} />
  <MetricCard label="Originated circuits" value={String(garlic.stats.originatedCircuits)} />
  <MetricCard label="Relayed circuits" value={String(garlic.stats.relayedCircuits)} />
  <MetricCard label="Known Garlic peers" value={String(garlic.knownPeers.length)} />
</div>

{#if garlic.enabled}
  <section class="identity">
    <h2>Identity</h2>
    {#if garlic.identity}
      <div class="row">
        <span class="label">Garlic public key</span>
        <CopyableKey value={garlic.identity.publicKey} prefixLen={16} suffixLen={8} />
      </div>
    {/if}
  </section>

  <div class="grid">
    <MetricCard label="Originated" value={formatBytes(garlic.stats.originatedBytes)} sublabel={`${garlic.stats.originatedPackets} packets`} />
    <MetricCard label="Relayed" value={formatBytes(garlic.stats.relayedBytes)} sublabel={`${garlic.stats.relayedPackets} packets`} />
  </div>

  <SecurityCounters counters={garlic.stats.security} />

  <section class="known-peers">
    <h2>Known Garlic peers ({garlic.knownPeers.length})</h2>
    {#if garlic.knownPeers.length === 0}
      <p class="empty">None known yet.</p>
    {:else}
      <table>
        <thead>
          <tr>
            <th>Node key</th>
            <th>Garlic public key</th>
            <th>Last seen</th>
          </tr>
        </thead>
        <tbody>
          {#each garlic.knownPeers as p (p.nodeKey)}
            <tr>
              <td><CopyableKey value={p.nodeKey} /></td>
              <td><CopyableKey value={p.garlicPublicKey} /></td>
              <td>{new Date(p.lastSeen).toLocaleString()}</td>
            </tr>
          {/each}
        </tbody>
      </table>
    {/if}
  </section>
{:else}
  <p class="disabled-note">Garlic is disabled on this node. Enable it in the node's config (Garlic.Enabled) to see identity, circuit, and security data here.</p>
{/if}

<style>
  .grid {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(160px, 1fr));
    gap: 0.75rem;
    margin-bottom: 1rem;
  }
  .identity,
  .known-peers {
    background: var(--bg-raised);
    border: 1px solid var(--border);
    border-radius: var(--radius);
    padding: 0.75rem 1rem;
    margin-bottom: 1rem;
  }
  h2 {
    font-size: 0.9rem;
    text-transform: uppercase;
    letter-spacing: 0.04em;
    color: var(--text-dim);
    margin: 0 0 0.5rem;
  }
  .row {
    display: flex;
    align-items: center;
    gap: 0.5rem;
  }
  .label {
    color: var(--text-dim);
    font-size: 0.85rem;
  }
  table {
    width: 100%;
    border-collapse: collapse;
    font-size: 0.85rem;
  }
  th,
  td {
    text-align: left;
    padding: 0.35rem 0.6rem;
    border-bottom: 1px solid var(--border);
  }
  .empty,
  .disabled-note {
    color: var(--text-dim);
    font-size: 0.85rem;
  }
</style>
```

- [ ] **Step 3: Create `yggdashboard/src/routes/garlic/+page.server.ts`**

```ts
import { poller } from '$lib/server/instance';
import { computeGarlic } from '$lib/server/garlic';
import type { PageServerLoad } from './$types';

export const load: PageServerLoad = async () => {
  await poller.waitUntilReady(2000);
  return { garlic: computeGarlic(poller.getSnapshot()) };
};
```

- [ ] **Step 4: Create `yggdashboard/src/routes/garlic/+page.svelte`**

```svelte
<script lang="ts">
  import GarlicPanel from '$lib/components/GarlicPanel.svelte';
  import { createGarlicResource } from '$lib/stores/dashboard.svelte';

  let { data } = $props();

  const garlicResource = createGarlicResource();
  $effect(() => {
    garlicResource.start();
    return () => garlicResource.stop();
  });

  let garlic = $derived(garlicResource.data ?? data.garlic);
</script>

<svelte:head>
  <title>yggdashboard · garlic</title>
</svelte:head>

<GarlicPanel {garlic} />
```

- [ ] **Step 5: Commit**

```bash
git add yggdashboard/src/lib/components/GarlicPanel.svelte yggdashboard/src/lib/components/SecurityCounters.svelte \
  yggdashboard/src/routes/garlic
git commit -m "yggdashboard: add Garlic overview page with identity, circuits, and security counters"
```

---

### Task 21: Network graph page (`/graph`)

**Files:**
- Modify: `yggdashboard/package.json` (add `d3-force`)
- Create: `yggdashboard/src/lib/components/NetworkGraph.svelte`
- Create: `yggdashboard/src/lib/components/GraphLegend.svelte`
- Create: `yggdashboard/src/lib/components/GraphDetail.svelte`
- Create: `yggdashboard/src/routes/graph/+page.server.ts`
- Create: `yggdashboard/src/routes/graph/+page.svelte`

**Interfaces:**
- Consumes: `computeGraph` (Task 14), `createGraphResource` (Task 15), `GraphNode`/`GraphEdge` (Task 15), `CopyableKey` (Task 16).
- Produces: the network graph page. Nothing downstream depends on this task's exports - last page task.

Edges use line style (solid vs. dashed vs. thick-solid), not color alone, per the brief's explicit requirement - see the legend and `NetworkGraph.svelte`'s CSS below.

- [ ] **Step 1: Add `d3-force` as a dependency**

Edit `yggdashboard/package.json`'s `devDependencies`, adding a new top-level `dependencies` block (this is a runtime dependency, not dev-only):

```json
  "dependencies": {
    "d3-force": "^3.0.0"
  },
```

Run:
```bash
cd yggdashboard && npm install
```
Expected: installs `d3-force` and its type declarations without error (the package ships its own types; no separate `@types/d3-force` needed for v3).

- [ ] **Step 2: Create `yggdashboard/src/lib/components/GraphLegend.svelte`**

```svelte
<div class="legend">
  <div class="item"><span class="swatch solid"></span> Yggdrasil connection</div>
  <div class="item"><span class="swatch dashed"></span> Garlic circuit</div>
  <div class="item"><span class="swatch active"></span> Active relay traffic</div>
</div>

<style>
  .legend {
    display: flex;
    gap: 1.25rem;
    flex-wrap: wrap;
    font-size: 0.8rem;
    color: var(--text-dim);
    margin-bottom: 0.5rem;
  }
  .item {
    display: inline-flex;
    align-items: center;
    gap: 0.4em;
  }
  .swatch {
    display: inline-block;
    width: 1.5em;
    height: 0;
    border-top-width: 2px;
    border-top-style: solid;
  }
  .swatch.solid {
    border-top-color: var(--text-dim);
  }
  .swatch.dashed {
    border-top-color: var(--warn);
    border-top-style: dashed;
  }
  .swatch.active {
    border-top-color: var(--bad);
    border-top-width: 3px;
  }
</style>
```

- [ ] **Step 3: Create `yggdashboard/src/lib/components/NetworkGraph.svelte`**

```svelte
<script lang="ts">
  import { forceSimulation, forceLink, forceManyBody, forceCenter, forceCollide } from 'd3-force';
  import type { GraphNode, GraphEdge } from '$lib/api-types';
  import { truncateKey } from '$lib/format';

  let {
    nodes,
    yggdrasilEdges,
    garlicEdges,
    onSelectNode,
    onSelectEdge
  }: {
    nodes: GraphNode[];
    yggdrasilEdges: GraphEdge[];
    garlicEdges: GraphEdge[];
    onSelectNode: (n: GraphNode) => void;
    onSelectEdge: (e: GraphEdge) => void;
  } = $props();

  const width = 800;
  const height = 480;

  type SimNode = GraphNode & { x?: number; y?: number };
  let simNodes = $state<SimNode[]>([]);

  $effect(() => {
    const nodeCopies: SimNode[] = nodes.map((n) => ({ ...n }));
    const keys = new Set(nodeCopies.map((n) => n.key));
    const linkData = yggdrasilEdges
      .filter((e) => keys.has(e.from) && keys.has(e.to))
      .map((e) => ({ source: e.from, target: e.to }));

    if (nodeCopies.length === 0) {
      simNodes = [];
      return;
    }

    const sim = forceSimulation(nodeCopies as never[])
      .force(
        'link',
        forceLink(linkData)
          .id((d: unknown) => (d as SimNode).key)
          .distance(70)
      )
      .force('charge', forceManyBody().strength(-120))
      .force('center', forceCenter(width / 2, height / 2))
      .force('collide', forceCollide(24))
      .stop();

    for (let i = 0; i < 200; i++) sim.tick();
    simNodes = nodeCopies;

    return () => sim.stop();
  });

  function nodePos(key: string): { x: number; y: number } {
    const n = simNodes.find((node) => node.key === key);
    return { x: n?.x ?? width / 2, y: n?.y ?? height / 2 };
  }
</script>

<div class="graph">
  {#if nodes.length === 0}
    <p class="empty">No known nodes yet.</p>
  {:else}
    <svg viewBox="0 0 {width} {height}" role="img" aria-label="Network topology">
      {#each yggdrasilEdges as edge, i (edge.from + edge.to + i)}
        {@const from = nodePos(edge.from)}
        {@const to = nodePos(edge.to)}
        <line x1={from.x} y1={from.y} x2={to.x} y2={to.y} class="edge yggdrasil" onclick={() => onSelectEdge(edge)} />
      {/each}
      {#each garlicEdges as edge, i (edge.from + edge.to + (edge.circuitId ?? '') + i)}
        {@const from = nodePos(edge.from)}
        {@const to = nodePos(edge.to)}
        <line x1={from.x} y1={from.y} x2={to.x} y2={to.y} class="edge garlic" class:active={edge.active} onclick={() => onSelectEdge(edge)} />
      {/each}
      {#each simNodes as node (node.key)}
        <g class="node" onclick={() => onSelectNode(node)}>
          <circle cx={node.x} cy={node.y} r={node.isSelf ? 10 : 6} class:self={node.isSelf} />
          <text x={node.x} y={(node.y ?? 0) - 12} text-anchor="middle">{node.isSelf ? 'LOCAL' : truncateKey(node.key, 4, 0)}</text>
        </g>
      {/each}
    </svg>
  {/if}
</div>

<style>
  .graph {
    background: var(--bg-raised);
    border: 1px solid var(--border);
    border-radius: var(--radius);
    padding: 0.5rem;
  }
  svg {
    width: 100%;
    height: 480px;
    display: block;
  }
  .empty {
    color: var(--text-dim);
    font-size: 0.85rem;
    padding: 1rem;
  }
  .edge {
    stroke: var(--text-dim);
    stroke-width: 1.5;
    cursor: pointer;
  }
  .edge.garlic {
    stroke: var(--warn);
    stroke-dasharray: 4 3;
  }
  .edge.garlic.active {
    stroke: var(--bad);
    stroke-width: 2.5;
    stroke-dasharray: none;
  }
  .node circle {
    fill: var(--accent);
    cursor: pointer;
  }
  .node circle.self {
    fill: var(--ok);
  }
  .node text {
    fill: var(--text-dim);
    font-size: 9px;
    font-family: var(--mono);
    pointer-events: none;
  }
</style>
```

- [ ] **Step 4: Create `yggdashboard/src/lib/components/GraphDetail.svelte`**

```svelte
<script lang="ts">
  import type { GraphNode, GraphEdge } from '$lib/api-types';
  import CopyableKey from './CopyableKey.svelte';

  let {
    selection,
    onClose
  }: { selection: { kind: 'node'; data: GraphNode } | { kind: 'edge'; data: GraphEdge }; onClose: () => void } = $props();
</script>

<aside class="detail">
  <button type="button" class="close" onclick={onClose} aria-label="Close">×</button>
  {#if selection.kind === 'node'}
    <h2>Node</h2>
    <dl>
      <dt>Public key</dt>
      <dd><CopyableKey value={selection.data.key} prefixLen={12} suffixLen={6} /></dd>
      <dt>Address</dt>
      <dd class="mono">{selection.data.address}</dd>
      <dt>Role</dt>
      <dd>{selection.data.isSelf ? 'This node' : 'Peer in the mesh'}</dd>
    </dl>
  {:else}
    <h2>Edge</h2>
    <dl>
      <dt>Type</dt>
      <dd>{selection.data.type === 'garlic' ? 'Garlic circuit' : 'Yggdrasil connection'}</dd>
      <dt>From</dt>
      <dd><CopyableKey value={selection.data.from} prefixLen={12} suffixLen={6} /></dd>
      <dt>To</dt>
      <dd><CopyableKey value={selection.data.to} prefixLen={12} suffixLen={6} /></dd>
      {#if selection.data.type === 'garlic'}
        <dt>Circuit</dt>
        <dd class="mono">{selection.data.circuitId}</dd>
        <dt>Active relay</dt>
        <dd>{selection.data.active ? 'Yes' : 'No'}</dd>
      {/if}
    </dl>
  {/if}
</aside>

<style>
  .detail {
    background: var(--bg-raised);
    border: 1px solid var(--border);
    border-radius: var(--radius);
    padding: 1rem;
    position: relative;
  }
  .close {
    position: absolute;
    top: 0.5rem;
    right: 0.5rem;
    background: none;
    border: none;
    color: var(--text-dim);
    font-size: 1.2rem;
    cursor: pointer;
  }
  h2 {
    font-size: 0.9rem;
    text-transform: uppercase;
    letter-spacing: 0.04em;
    color: var(--text-dim);
    margin: 0 0 0.75rem;
  }
  dl {
    display: grid;
    grid-template-columns: max-content 1fr;
    gap: 0.35rem 1rem;
    margin: 0;
  }
  dt {
    color: var(--text-dim);
    font-size: 0.8rem;
  }
  .mono {
    font-family: var(--mono);
  }
</style>
```

- [ ] **Step 5: Create `yggdashboard/src/routes/graph/+page.server.ts`**

```ts
import { poller } from '$lib/server/instance';
import { computeGraph } from '$lib/server/graph';
import type { PageServerLoad } from './$types';

export const load: PageServerLoad = async () => {
  await poller.waitUntilReady(2000);
  return { graph: computeGraph(poller.getSnapshot()) };
};
```

- [ ] **Step 6: Create `yggdashboard/src/routes/graph/+page.svelte`**

```svelte
<script lang="ts">
  import NetworkGraph from '$lib/components/NetworkGraph.svelte';
  import GraphLegend from '$lib/components/GraphLegend.svelte';
  import GraphDetail from '$lib/components/GraphDetail.svelte';
  import { createGraphResource } from '$lib/stores/dashboard.svelte';
  import type { GraphNode, GraphEdge } from '$lib/api-types';

  let { data } = $props();

  const graphResource = createGraphResource();
  $effect(() => {
    graphResource.start();
    return () => graphResource.stop();
  });

  let graph = $derived(graphResource.data ?? data.graph);
  let selection = $state<{ kind: 'node'; data: GraphNode } | { kind: 'edge'; data: GraphEdge } | null>(null);
</script>

<svelte:head>
  <title>yggdashboard · graph</title>
</svelte:head>

<GraphLegend />
<div class="layout">
  <div class="graph-col">
    <NetworkGraph
      nodes={graph.nodes}
      yggdrasilEdges={graph.yggdrasilEdges}
      garlicEdges={graph.garlicEdges}
      onSelectNode={(n) => (selection = { kind: 'node', data: n })}
      onSelectEdge={(e) => (selection = { kind: 'edge', data: e })}
    />
  </div>
  {#if selection}
    <div class="detail-col">
      <GraphDetail {selection} onClose={() => (selection = null)} />
    </div>
  {/if}
</div>

<style>
  .layout {
    display: grid;
    grid-template-columns: 1fr;
    gap: 1rem;
  }
  @media (min-width: 900px) {
    .layout {
      grid-template-columns: 1fr 320px;
    }
  }
</style>
```

- [ ] **Step 7: Manually verify the graph renders**

```bash
cd yggdashboard && ADMIN_SOCKET=unix:///var/run/yggdrasil.sock npm run dev -- --port 5173 &
sleep 3
curl -s http://localhost:5173/graph | grep -q "Yggdrasil connection" && echo GRAPH_OK
kill %1
```
Expected: prints `GRAPH_OK`.

- [ ] **Step 8: Commit**

```bash
git add yggdashboard/package.json yggdashboard/package-lock.json yggdashboard/src/lib/components/NetworkGraph.svelte \
  yggdashboard/src/lib/components/GraphLegend.svelte yggdashboard/src/lib/components/GraphDetail.svelte \
  yggdashboard/src/routes/graph
git commit -m "yggdashboard: add network graph page (Yggdrasil + Garlic layers, d3-force layout)"
```

---

### Task 22: Builder unit tests, empty/disabled-state component tests, responsive verification

A note on "loading state," since the design spec's testing section names it explicitly: this architecture doesn't have a separate loading-spinner state to test. SSR (`+page.server.ts`, Tasks 17-21) always provides real-or-honestly-empty data on the very first response - there is no client-side-only "waiting for the first fetch" gap, because `$derived(resource.data ?? data.x)` (every page built in Tasks 17-21) falls back to the SSR value until the first client poll lands, then swaps seamlessly. A dedicated "loading" component test would be testing a state the app deliberately never produces. What *is* tested: `StatusBadge`'s `disconnected` variant (below) and `PolledResource.connected` (Task 15) together cover what actually happens when data can't be fetched at all - which is the real-world equivalent of what "loading" was standing in for in the spec.

**Files:**
- Modify: `yggdashboard/package.json` (add `@testing-library/svelte`, `jsdom`)
- Create: `yggdashboard/src/lib/server/stats.test.ts`
- Create: `yggdashboard/src/lib/server/peers.test.ts`
- Create: `yggdashboard/src/lib/server/graph.test.ts`
- Create: `yggdashboard/src/lib/components/PeerTable.render.test.ts`
- Create: `yggdashboard/src/lib/components/CircuitTable.render.test.ts`
- Create: `yggdashboard/src/lib/components/NetworkGraph.render.test.ts`
- Create: `yggdashboard/src/lib/components/GarlicPanel.render.test.ts`
- Create: `yggdashboard/src/lib/components/StatusBadge.render.test.ts`

**Interfaces:**
- Consumes: every builder function (Task 14) and component (Tasks 16-21) written so far.
- Produces: closes the gap between what's been directly unit-tested so far (pure logic: extraction, framing, sort/filter, scaling math) and what the design spec's testing section explicitly asks for (component rendering with mock data covering empty/disabled/no-data states). Nothing downstream depends on this task.

- [ ] **Step 1: Add component-testing dependencies**

Edit `yggdashboard/package.json`'s `devDependencies`, adding:

```json
    "@testing-library/svelte": "^5.2.0",
    "jsdom": "^25.0.0",
```

Run:
```bash
cd yggdashboard && npm install
```
Expected: installs without error.

- [ ] **Step 2: Write and run the builder unit tests**

Create `yggdashboard/src/lib/server/stats.test.ts`:

```ts
import { describe, it, expect } from 'vitest';
import { computeStats } from './stats';
import { EMPTY_SNAPSHOT, EMPTY_GARLIC } from './types';
import type { Snapshot } from './types';

function snapshotWithGarlicBytes(originated: number, relayed: number): Snapshot {
  return {
    ...EMPTY_SNAPSHOT,
    garlic: { ...EMPTY_GARLIC, enabled: true, stats: { ...EMPTY_GARLIC.stats, originatedBytes: originated, relayedBytes: relayed } }
  };
}

describe('computeStats', () => {
  it('reports transitPercent as exactly 0, not NaN, when no Garlic traffic has happened yet', () => {
    const stats = computeStats(snapshotWithGarlicBytes(0, 0));
    expect(stats.garlic.transitPercent).toBe(0);
  });

  it('computes transitPercent as relayed / (originated + relayed) * 100', () => {
    const stats = computeStats(snapshotWithGarlicBytes(300, 700));
    expect(stats.garlic.transitPercent).toBeCloseTo(70, 5);
  });

  it('sums peer bytes_recvd/bytes_sent for peer-link totals, separate from session totals', () => {
    const snap: Snapshot = {
      ...EMPTY_SNAPSHOT,
      peers: [
        { key: 'a', up: true, inbound: false, port: 1, priority: 0, cost: 1, bytes_recvd: 100, bytes_sent: 50 },
        { key: 'b', up: true, inbound: true, port: 1, priority: 0, cost: 1, bytes_recvd: 200, bytes_sent: 25 }
      ],
      sessions: [{ address: '200::1', key: 'a', bytes_recvd: 10, bytes_sent: 5, uptime: 1 }]
    };
    const stats = computeStats(snap);
    expect(stats.rxTotalPeerLink).toBe(300);
    expect(stats.txTotalPeerLink).toBe(75);
    expect(stats.rxTotalSessions).toBe(10);
    expect(stats.txTotalSessions).toBe(5);
  });
});
```

Create `yggdashboard/src/lib/server/peers.test.ts`:

```ts
import { describe, it, expect } from 'vitest';
import { computePeers } from './peers';
import { EMPTY_SNAPSHOT } from './types';
import type { Snapshot } from './types';

describe('computePeers', () => {
  it('returns an empty peer list unchanged', () => {
    expect(computePeers(EMPTY_SNAPSHOT).peers).toEqual([]);
  });

  it('marks a peer garlicCapable when its key appears in garlic.knownPeers', () => {
    const snap: Snapshot = {
      ...EMPTY_SNAPSHOT,
      peers: [
        { key: 'aaa', up: true, inbound: false, port: 1, priority: 0, cost: 1 },
        { key: 'bbb', up: true, inbound: false, port: 1, priority: 0, cost: 1 }
      ],
      garlic: { ...EMPTY_SNAPSHOT.garlic, knownPeers: [{ nodeKey: 'aaa', garlicPublicKey: 'gp', lastSeen: '2026-01-01T00:00:00Z' }] }
    };
    const { peers } = computePeers(snap);
    expect(peers.find((p) => p.key === 'aaa')?.garlicCapable).toBe(true);
    expect(peers.find((p) => p.key === 'bbb')?.garlicCapable).toBe(false);
  });

  it('defaults optional numeric fields to 0 and optional string fields to null', () => {
    const snap: Snapshot = { ...EMPTY_SNAPSHOT, peers: [{ key: 'a', up: false, inbound: false, port: 1, priority: 0, cost: 1 }] };
    const [peer] = computePeers(snap).peers;
    expect(peer.bytesRecvd).toBe(0);
    expect(peer.rateSent).toBe(0);
    expect(peer.remote).toBeNull();
    expect(peer.latencyNs).toBeNull();
  });
});
```

Create `yggdashboard/src/lib/server/graph.test.ts`:

```ts
import { describe, it, expect } from 'vitest';
import { computeGraph } from './graph';
import { EMPTY_SNAPSHOT } from './types';
import type { Snapshot } from './types';

describe('computeGraph', () => {
  it('returns no nodes or edges for a snapshot with nothing known', () => {
    const graph = computeGraph(EMPTY_SNAPSHOT);
    expect(graph.nodes).toEqual([{ key: '', address: '', isSelf: true }]); // self always included, even with empty fields
    expect(graph.yggdrasilEdges).toEqual([]);
    expect(graph.garlicEdges).toEqual([]);
  });

  it('builds a yggdrasil edge from each tree entry with a real parent', () => {
    const snap: Snapshot = {
      ...EMPTY_SNAPSHOT,
      self: { ...EMPTY_SNAPSHOT.self, key: 'root' },
      tree: [{ address: '200::2', key: 'child', parent: 'root', sequence: 1 }]
    };
    const graph = computeGraph(snap);
    expect(graph.yggdrasilEdges).toEqual([{ from: 'child', to: 'root', type: 'yggdrasil' }]);
  });

  it('builds a full originated-circuit chain from LOCAL through every hop', () => {
    const snap: Snapshot = {
      ...EMPTY_SNAPSHOT,
      self: { ...EMPTY_SNAPSHOT.self, key: 'local' },
      garlic: {
        ...EMPTY_SNAPSHOT.garlic,
        circuits: {
          originated: [{ circuitId: '1', hops: ['a', 'b'], closed: false, createdAt: '', expiresAt: '', packets: 0, bytes: 0 }],
          relayed: []
        }
      }
    };
    const graph = computeGraph(snap);
    expect(graph.garlicEdges).toEqual([
      { from: 'local', to: 'a', type: 'garlic', circuitId: '1', active: true },
      { from: 'a', to: 'b', type: 'garlic', circuitId: '1', active: true }
    ]);
  });

  it('builds only previous-hop and next-hop edges for a relayed circuit, never a fabricated full path', () => {
    const snap: Snapshot = {
      ...EMPTY_SNAPSHOT,
      self: { ...EMPTY_SNAPSHOT.self, key: 'local' },
      garlic: {
        ...EMPTY_SNAPSHOT.garlic,
        circuits: {
          originated: [],
          relayed: [{ circuitId: '2', previousHop: 'x', nextHop: 'y', firstSeen: '', lastActive: '', packetsRelayed: 0, bytesRelayed: 0 }]
        }
      }
    };
    const graph = computeGraph(snap);
    expect(graph.garlicEdges).toEqual([
      { from: 'x', to: 'local', type: 'garlic', circuitId: '2', active: true },
      { from: 'local', to: 'y', type: 'garlic', circuitId: '2', active: true }
    ]);
  });
});
```

Run: `cd yggdashboard && npx vitest run src/lib/server/stats.test.ts src/lib/server/peers.test.ts src/lib/server/graph.test.ts`
Expected: PASS, all 10 tests green.

- [ ] **Step 3: Write and run the empty/disabled-state component render tests**

These use `@testing-library/svelte`'s `render`, and need the `jsdom` environment - add the per-file pragma comment so only these files run under `jsdom` (every other test file in this project stays in Vitest's default `node` environment, which the admin-socket/poller tests rely on).

Create `yggdashboard/src/lib/components/PeerTable.render.test.ts`:

```ts
// @vitest-environment jsdom
import { describe, it, expect } from 'vitest';
import { render, screen } from '@testing-library/svelte';
import PeerTable from './PeerTable.svelte';

describe('PeerTable render', () => {
  it('shows an empty-state message and no table when there are no peers', () => {
    render(PeerTable, { props: { peers: [], onSelect: () => {} } });
    expect(screen.getByText('No peers connected.')).toBeInTheDocument();
    expect(screen.queryByRole('table')).not.toBeInTheDocument();
  });
});
```

Create `yggdashboard/src/lib/components/CircuitTable.render.test.ts`:

```ts
// @vitest-environment jsdom
import { describe, it, expect } from 'vitest';
import { render, screen } from '@testing-library/svelte';
import CircuitTable from './CircuitTable.svelte';

describe('CircuitTable render', () => {
  it('shows both empty-state messages when there are no circuits at all', () => {
    render(CircuitTable, { props: { originated: [], relayed: [], onSelectOriginated: () => {}, onSelectRelayed: () => {} } });
    expect(screen.getByText('No originated circuits.')).toBeInTheDocument();
    expect(screen.getByText('No relayed circuits.')).toBeInTheDocument();
  });
});
```

Create `yggdashboard/src/lib/components/NetworkGraph.render.test.ts`:

```ts
// @vitest-environment jsdom
import { describe, it, expect } from 'vitest';
import { render, screen } from '@testing-library/svelte';
import NetworkGraph from './NetworkGraph.svelte';

describe('NetworkGraph render', () => {
  it('shows an empty-state message and no svg when there are no nodes', () => {
    render(NetworkGraph, { props: { nodes: [], yggdrasilEdges: [], garlicEdges: [], onSelectNode: () => {}, onSelectEdge: () => {} } });
    expect(screen.getByText('No known nodes yet.')).toBeInTheDocument();
  });

  it('renders a node circle for each known node, including self', () => {
    const { container } = render(NetworkGraph, {
      props: {
        nodes: [
          { key: 'local', address: '200::1', isSelf: true },
          { key: 'peer1', address: '200::2', isSelf: false }
        ],
        yggdrasilEdges: [{ from: 'peer1', to: 'local', type: 'yggdrasil' }],
        garlicEdges: [],
        onSelectNode: () => {},
        onSelectEdge: () => {}
      }
    });
    expect(container.querySelectorAll('circle').length).toBe(2);
    expect(container.querySelectorAll('line.yggdrasil').length).toBe(1);
  });

  it('renders a dashed garlic edge distinctly from a solid yggdrasil edge (not color-only)', () => {
    const { container } = render(NetworkGraph, {
      props: {
        nodes: [
          { key: 'local', address: '200::1', isSelf: true },
          { key: 'peer1', address: '200::2', isSelf: false }
        ],
        yggdrasilEdges: [],
        garlicEdges: [{ from: 'local', to: 'peer1', type: 'garlic', circuitId: '1', active: false }],
        onSelectNode: () => {},
        onSelectEdge: () => {}
      }
    });
    expect(container.querySelectorAll('line.garlic').length).toBe(1);
  });
});
```

Create `yggdashboard/src/lib/components/GarlicPanel.render.test.ts`:

```ts
// @vitest-environment jsdom
import { describe, it, expect } from 'vitest';
import { render, screen } from '@testing-library/svelte';
import GarlicPanel from './GarlicPanel.svelte';
import type { GarlicResponse } from '$lib/api-types';

// Zeroed inline, not imported from $lib/server/* - component/client test
// files must never reach across that boundary, even though it would
// happen to type-check here (GarlicStats and GarlicResponse['stats']
// are structurally identical by design).
const EMPTY_STATS: GarlicResponse['stats'] = {
  originatedCircuits: 0,
  relayedCircuits: 0,
  originatedPackets: 0,
  originatedBytes: 0,
  relayedPackets: 0,
  relayedBytes: 0,
  security: { replayDrops: 0, malformedPackets: 0, expiredPackets: 0, authFailures: 0, relayTableFull: 0 }
};

describe('GarlicPanel render', () => {
  it('shows the disabled explanation and no identity/security sections when Garlic is off', () => {
    render(GarlicPanel, {
      props: { garlic: { enabled: false, identity: null, stats: EMPTY_STATS, knownPeers: [], polledAt: '' } }
    });
    expect(screen.getByText(/Garlic is disabled on this node/)).toBeInTheDocument();
    expect(screen.queryByText('Security')).not.toBeInTheDocument();
  });
});
```

Create `yggdashboard/src/lib/components/StatusBadge.render.test.ts`:

```ts
// @vitest-environment jsdom
import { describe, it, expect } from 'vitest';
import { render, screen } from '@testing-library/svelte';
import StatusBadge from './StatusBadge.svelte';

describe('StatusBadge render', () => {
  it.each([
    ['online', 'Online'],
    ['degraded', 'Degraded'],
    ['disconnected', 'Disconnected']
  ] as const)('renders the %s label for status %s', (status, label) => {
    render(StatusBadge, { props: { status } });
    expect(screen.getByText(label)).toBeInTheDocument();
  });
});
```

Run: `cd yggdashboard && npx vitest run src/lib/components/*.render.test.ts`
Expected: PASS, all 8 tests green.

- [ ] **Step 4: Manually verify responsive behavior**

The layout already collapses connections/circuits/graph's two-column `1fr 320px` grid to a single column under 900px (each page's own `@media (min-width: 900px)` rule), and every wide table sits in a `overflow-x: auto` container rather than overflowing the page - both written directly into each component in Tasks 18-21, not deferred. Verify in a real browser:

```bash
cd yggdashboard && ADMIN_SOCKET=unix:///var/run/yggdrasil.sock npm run dev -- --port 5173 &
sleep 3
```

Open `http://localhost:5173` and, using the browser's device toolbar, check at 375px (mobile), 768px (tablet), and 1440px (desktop) widths on `/`, `/connections`, `/circuits`, `/garlic`, and `/graph`:
- No horizontal scroll on the page body at any width.
- Tables scroll within their own container if narrower than their content, rather than breaking the layout.
- The detail side panel (connections/circuits/graph) stacks below its table/graph on narrow widths instead of squeezing it.

```bash
kill %1
```

- [ ] **Step 5: Run the full Vitest suite**

Run: `cd yggdashboard && npm test`
Expected: PASS across every test file written in Tasks 10-22.

- [ ] **Step 6: Commit**

```bash
git add yggdashboard/package.json yggdashboard/package-lock.json yggdashboard/src/lib/server/stats.test.ts \
  yggdashboard/src/lib/server/peers.test.ts yggdashboard/src/lib/server/graph.test.ts \
  yggdashboard/src/lib/components/PeerTable.render.test.ts yggdashboard/src/lib/components/CircuitTable.render.test.ts \
  yggdashboard/src/lib/components/NetworkGraph.render.test.ts yggdashboard/src/lib/components/GarlicPanel.render.test.ts \
  yggdashboard/src/lib/components/StatusBadge.render.test.ts
git commit -m "yggdashboard: add builder unit tests and empty/disabled-state component render tests"
```

---

### Task 23: README and full end-to-end verification

**Files:**
- Create: `yggdashboard/README.md`

**Interfaces:**
- Consumes: everything built in Tasks 1-22.
- Produces: operator-facing documentation and final, real, end-to-end proof the whole system works together - not a static mockup. Last task in this plan.

- [ ] **Step 1: Create `yggdashboard/README.md`**

```markdown
# yggdashboard

A local operator dashboard for a running Yggdrasil/Garlic node: live node
status, traffic, peers, Garlic circuits, and network topology, updated
every 1-2 seconds over plain HTTP polling (no WebSockets). Server-side
rendered - the initial page has real data with no JavaScript required.

Disabled by default. See "Enabling" below.

## Architecture

`yggdrasil` itself spawns this as a child process when configured to -
you never run it by hand in production. It's a normal
`@sveltejs/adapter-node` SvelteKit app; the only thing custom about it is
that its background poller talks to Yggdrasil's admin socket (the same
protocol `yggdrasilctl` uses) instead of a database. The browser never
touches the admin socket directly - only this process does, and only the
hand-picked fields under `src/routes/api/*` (never a raw admin-response
passthrough) ever reach it.

## Enabling

In the node's own config (HJSON, e.g. `/etc/yggdrasil/yggdrasil.conf`):

```json
"Dashboard": {
  "Enabled": true,
  "Listen": "127.0.0.1:8080",
  "Path": "/usr/lib/yggdrasil/dashboard"
}
```

`Path` must point at this project's `build/` directory (the `npm run
build` output, containing `index.js`) - `yggdrasil` execs `node
<Path>/index.js` directly. Leaving `Path` empty tries, in order:
`/usr/lib/yggdrasil/dashboard`, `/usr/share/yggdrasil/dashboard`, then
`./yggdashboard/build` relative to wherever `yggdrasil` was started from
(convenient when running from a source checkout).

Restart `yggdrasil`. If `node` isn't on `PATH` or nothing is found at
`Path`, yggdrasil logs a warning and keeps running normally - a missing
or misconfigured dashboard never stops the node itself.

## Building

```sh
npm install
npm run build
```

Produces `build/index.js` and friends - point `Dashboard.Path` in the
node's config at this `build/` directory (or copy it to one of the
conventional install paths above).

## Development

Run against a real node's admin socket without involving `yggdrasil`'s
own process-spawning at all:

```sh
npm install
ADMIN_SOCKET=unix:///var/run/yggdrasil.sock npm run dev
```

## Configuration (environment variables)

| Variable | Default | Meaning |
|---|---|---|
| `ADMIN_SOCKET` | `unix:///var/run/yggdrasil.sock` | The node's admin socket address - same `unix://path` or `tcp://host:port` format as `AdminListen`. Set automatically by `yggdrasil` itself when it spawns this process; only needed by hand in `npm run dev`. |
| `POLL_INTERVAL_MS` | `1500` | How often the background poller polls the admin socket. |
| `HISTORY_WINDOW_MS` | `300000` (5 minutes) | How much traffic history the in-memory ring buffer keeps for the overview chart. Resets when this process restarts. |
| `HOST`, `PORT` | set by `@sveltejs/adapter-node` | This dashboard's own HTTP listen address - set automatically by `yggdrasil` from `Dashboard.Listen`. |

## Running the test suite

```sh
npm test
```

## Access control

**There is no authentication in this dashboard**, matching the admin
socket it talks to (`yggdrasilctl` itself has none either - anyone who
can reach the socket is trusted). The listener binds to `127.0.0.1` (or
`::1`) only by default and must never default to `0.0.0.0`/`::`. To view
it from another machine, use an SSH tunnel:

```sh
ssh -L 8080:127.0.0.1:8080 user@your-server
```

then open `http://localhost:8080` locally. This is a deliberate scope
limit, not an oversight.

## What this dashboard cannot show (and why)

- **A global "transit %" across all Yggdrasil traffic.** Yggdrasil
  delegates actual mesh routing to the vendored `ironwood` library; the
  node has no visibility into forwarded-vs-own traffic at that layer.
  Ordinary traffic is shown as two honest, separate numbers instead
  (peer-link totals vs. session totals). Garlic circuit traffic *is*
  this repo's own code, so a Garlic-scoped transit % is shown.
- **A relayed circuit's full path.** A relay only ever knows its own two
  neighbors on a circuit - shown as `Previous → LOCAL → Next`, never a
  fabricated end-to-end chain.
- **Distributed/DHT-backed introduction points.** The backend's
  rendezvous implementation is in-memory/static-config only today.
```

- [ ] **Step 2: Run the full backend test suite**

Run: `go test ./...`
Expected: PASS across every Go package.

- [ ] **Step 3: Run the full frontend test suite**

Run: `cd yggdashboard && npm test`
Expected: PASS across every Vitest file from Tasks 10-22.

- [ ] **Step 4: Type-check the frontend**

Run: `cd yggdashboard && npm run check`
Expected: no type errors.

- [ ] **Step 5: Full end-to-end verification - build and wire everything together**

```bash
cd yggdashboard && npm run build
cd ..
go build -o /tmp/yggdashboard-e2e/yggdrasil ./cmd/yggdrasil
mkdir -p /tmp/yggdashboard-e2e
/tmp/yggdashboard-e2e/yggdrasil -genconf > /tmp/yggdashboard-e2e/yggdrasil.conf
python3 -c "
import json
with open('/tmp/yggdashboard-e2e/yggdrasil.conf') as f:
    cfg = json.load(f)
cfg['Dashboard']['Enabled'] = True
cfg['Dashboard']['Path'] = '$(pwd)/yggdashboard/build'
with open('/tmp/yggdashboard-e2e/yggdrasil.conf', 'w') as f:
    json.dump(cfg, f)
"
/tmp/yggdashboard-e2e/yggdrasil -useconffile /tmp/yggdashboard-e2e/yggdrasil.conf &
sleep 3
```

- [ ] **Step 6: Verify the dashboard is reachable, loopback-only, and shows real data**

```bash
curl -s -o /dev/null -w "%{http_code}\n" http://127.0.0.1:8080
# Expected: 200

curl -s http://127.0.0.1:8080/api/status | python3 -m json.tool
# Expected: {"status": "degraded" or "online", "buildName": "yggdrasil", ...} - real data, not zeros/nulls
# ("degraded" is expected and correct if this test node has no peers configured.)

(ss -ltnp 2>/dev/null || netstat -ltnp 2>/dev/null) | grep ':8080'
# Expected: shows 127.0.0.1:8080, never 0.0.0.0:8080 or :::8080
```

- [ ] **Step 7: Verify no secret material appears anywhere in the dashboard's responses**

```bash
for path in status stats peers circuits garlic graph; do
  echo "=== /api/$path ==="
  curl -s "http://127.0.0.1:8080/api/$path" | grep -iE "privatekey|secret|sessionkey|aeadkey" && echo "FAIL: secret-shaped field found in /api/$path" || echo "OK: no secret-shaped fields"
done
curl -s http://127.0.0.1:8080/ | grep -iE "privatekey|secret" && echo "FAIL: secret-shaped field found in the rendered page" || echo "OK: no secret-shaped fields in SSR HTML"
```
Expected: every path prints `OK`.

- [ ] **Step 8: Verify graceful degradation when the admin socket is unreachable**

```bash
kill %1
sleep 1
ADMIN_SOCKET=unix:///tmp/nonexistent-for-real.sock HOST=127.0.0.1 PORT=8081 node yggdashboard/build/index.js &
sleep 3
curl -s -o /dev/null -w "%{http_code}\n" http://127.0.0.1:8081
# Expected: 200 - the page itself still renders (Disconnected status, empty tables), not a crash
curl -s http://127.0.0.1:8081/api/peers
# Expected: {"peers":[],"polledAt":""} or similar - empty, not an HTTP 500
kill %1
```

- [ ] **Step 9: Verify `dashboard.enabled: false` leaves yggdrasil's behavior completely unchanged**

```bash
python3 -c "
import json
with open('/tmp/yggdashboard-e2e/yggdrasil.conf') as f:
    cfg = json.load(f)
cfg['Dashboard']['Enabled'] = False
with open('/tmp/yggdashboard-e2e/yggdrasil.conf', 'w') as f:
    json.dump(cfg, f)
"
/tmp/yggdashboard-e2e/yggdrasil -useconffile /tmp/yggdashboard-e2e/yggdrasil.conf &
sleep 2
(ss -ltnp 2>/dev/null || netstat -ltnp 2>/dev/null) | grep ':8080' && echo "FAIL: dashboard listening while disabled" || echo "OK: nothing listening on 8080"
kill %1
```
Expected: prints `OK: nothing listening on 8080`.

- [ ] **Step 10: Confirm the Go module tree is exactly what this plan touched**

```bash
git status --porcelain=v1 -- src/ cmd/ go.mod go.sum
```
Expected: empty (everything already committed by Tasks 1-9) or shows only files this plan's tasks created/modified - no stray changes.

- [ ] **Step 11: Commit**

```bash
git add yggdashboard/README.md
git commit -m "yggdashboard: add README and complete end-to-end verification"
```

---

**Plan complete.** Both parts (Go backend, SvelteKit frontend) are independently tested and committed task-by-task, and Task 23 proves them working together for real: dashboard disabled leaves yggdrasil unchanged, dashboard enabled binds loopback-only and shows live real data, no secret material appears anywhere in its output, and it degrades gracefully rather than crashing when the admin socket is unreachable.
