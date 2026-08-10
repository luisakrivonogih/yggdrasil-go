# yggdashboard v2 — local operator dashboard — design spec

Status: approved, not yet implemented. Supersedes
`docs/superpowers/specs/2026-08-09-yggdashboard-design.md` and its Phase 1
plan (`docs/superpowers/plans/2026-08-09-yggdashboard-phase1.md`) — see
"Relationship to prior work" below.

## Problem

There's no way to see a running Yggdrasil/Garlic node's live state (peers,
traffic, routing, Garlic circuits, security counters) without hand-typing
`yggdrasilctl` commands. Want a polished, information-dense local web
dashboard, started automatically alongside the node, covering node health,
traffic, connections, Garlic circuits, and network topology.

## Relationship to prior work

An earlier, narrower spec was written and its Phase 1 fully implemented in
an isolated git worktree (`.claude/worktrees/yggdashboard-phase1`, branch
`worktree-yggdashboard-phase1`) — never merged to `develop`. That
implementation is a single-page dashboard, pushed over WebSocket, run as a
manually-started standalone Node process configured via environment
variables. It conflicts with this spec on several explicit points: this
spec uses HTTP polling (not WebSocket), multiple routed pages, and a
process that `yggdrasil` itself spawns, configured through the node's own
HJSON config (not env vars).

Decision (confirmed with the user): keep the old spec/plan as historical
record but treat them as superseded by this document. Do not build on top
of the old worktree's app layer. Do salvage its already-tested,
architecture-agnostic pieces where they still fit:

- `json-stream.ts` (incremental JSON value extractor for the admin
  socket's delimiter-free wire format) — reusable as-is.
- The admin-socket client's framing/keepalive/reconnect logic — reusable
  with minor adaptation (this spec's server-side poller still wants one
  persistent keepalive connection; only the push-to-browser mechanism
  changes from WebSocket to polling).
- Wire type definitions mirroring the Go admin response structs — reusable
  as a starting point, extended for the new fields this spec adds.

## Architecture

```
yggdrasil (Go binary)
  ├─ existing admin socket (unix or tcp, unauthenticated, unchanged
  │  protocol — src/admin/admin.go)
  ├─ new: src/dashboard/ — reads the node's Dashboard config; if
  │  Enabled, spawns and supervises `node <Path>/build/index.js` as a
  │  child process, passing the node's own AdminListen address and the
  │  dashboard's Listen host:port as environment variables. Child
  │  stdout/stderr piped into yggdrasil's own logger, prefixed
  │  "dashboard: ". Killed on yggdrasil shutdown (same signal-driven
  │  context already used for other subsystems in cmd/yggdrasil/main.go).
  │  If `node` isn't found on PATH, or the built app isn't found at
  │  Path (or any conventional fallback path), logs one clear warning
  │  and yggdrasil continues running normally — never crashes the
  │  daemon over a missing dashboard.
  └─ yggdashboard/ — separate SvelteKit 5 (adapter-node) project, own
     package.json/toolchain, not part of go.mod or ./build:

       Browser --HTTP polling (1-2s)--> SvelteKit server
                                            |
                                            | one persistent keepalive
                                            | admin-socket connection
                                            v
                                        yggdrasil process (same host)
```

Operator experience: set `dashboard.enabled: true` in the node's config,
restart yggdrasil, open `http://127.0.0.1:8080`. No second process to
start by hand. Building the dashboard's static assets (`npm run build`)
and placing them at the configured `Path` remains a manual/packaging step
in this pass — see "Out of scope" below.

For local development, `yggdashboard/` still runs standalone via
`npm run dev` against a real node's admin socket, same as the superseded
design allowed.

## Backend changes (Go)

No changes to Garlic cryptography, wire formats, or the onion-processing
decision logic's external behavior — every addition below is either a new
read-only accessor over data that already exists privately, or a new
counter incremented at a call site that already exists. Nothing changes
what any hop tells another hop.

### Config (`src/config/config.go`)

New nested block, following `GarlicConfig`'s existing pattern (tagged
struct, `comment:` tags for the generated HJSON):

```go
type DashboardConfig struct {
    Enabled bool   `comment:"Enables the local operator dashboard HTTP server\n(UI and its read-only API together) as a subprocess yggdrasil manages.\nDefault is false."`
    Listen  string `comment:"Listen address (host:port) for the dashboard's HTTP\nserver. Must default to a loopback address. Changing this to a\nnon-loopback address is your own choice and your own risk - the\ndashboard and its API have no authentication."`
    Path    string `comment:"Directory containing the dashboard's built assets (npm run\nbuild output). Empty tries conventional install paths, then a path\nrelative to the yggdrasil binary for development."`
}
```

Default: `Enabled: false`, `Listen: "127.0.0.1:8080"`, `Path: ""`. One
`Enabled`/`Listen` pair controls the dashboard UI and its `/api/*`
read-only endpoints together — they're served by the same HTTP listener
in the same spawned process, so there is no separate toggle or bind for
the API; disabling the dashboard disables the API, and the loopback
default covers both. If `Enabled: true` but the node's own `AdminListen`
is `"none"`, log an error and skip spawning — the dashboard has nothing to
poll.

### Process spawn/lifecycle (`src/dashboard/`, new package)

- `Start(cfg DashboardConfig, adminListen string, logger Logger) (*Process, error)`:
  resolves `node` via `exec.LookPath`, resolves the dashboard directory
  (configured `Path`, else conventional install locations, else a
  `./yggdashboard/build` relative fallback for running from a checkout),
  execs `node build/index.js` with env vars `ADMIN_SOCKET` (copied from
  the node's own `AdminListen` — no separate admin-socket configuration
  needed on the dashboard side), `DASHBOARD_HOST`, `DASHBOARD_PORT`. Wired
  into `cmd/yggdrasil/main.go`'s `node` struct and shutdown path exactly
  like the existing `garlic`/`admin`/`multicast` subsystems.
- `Stop()`: terminates the child process.
- Any failure to start (missing `node`, missing build output) is a logged
  warning, not a fatal error.

### Garlic package additions (`src/garlic/`)

- `Circuit` (`circuit.go`, originator's view): add read-only accessors for
  hop count, ordered hop node keys (already known plaintext to the
  originator — it chose this path), and the already-tracked
  `bytesSent`/`packetsSent` counters (currently private, just need
  exposing).
- `relayCircuitState` (`relaystate.go`) + `manager.go`: thread the
  already-available `from` parameter (the previous hop, currently read in
  `handleIncoming` but not carried further) through to where a circuit's
  replay window is created/touched, and record: previous hop key, next
  hop key (`action.forwardTo`, already computed in `dispatchAction`),
  first-seen time, last-active time, and byte/packet counters incremented
  at the same forwarding point. This is what makes an honest
  "Previous → LOCAL → Next" relay view possible — the relay never learns,
  and the dashboard never shows, anything beyond its own two neighbors.
- `CircuitManager` (`circuit_manager.go`): add a `List() []CircuitSummary`
  (today only `Count()`/`Get(id)` exist) so an admin handler can enumerate
  live originated circuits without exposing the mutex-guarded map itself.
- **Local-only security counters** (new small struct, incremented at each
  existing `actionDrop` return point in `protocol.go`): replay drops,
  malformed packets, expired packets, decrypt/auth failures,
  relay-table-full. Atomic counters (`sync/atomic`), cumulative since
  process start, incremented inline at drop sites that already exist —
  no new hot-path work beyond one atomic add. These never leave this
  node: the wire protocol still returns the same undifferentiated
  `actionDrop` behavior to peers (nothing about *which* check failed is
  observable over the network, preserving the documented "don't leak
  which check failed" property in `docs/garlic-security.md`); only this
  node's own admin socket exposes the category breakdown, to the same
  locally-trusted audience that can already run `yggdrasilctl`.
- `getSelf` (`src/admin/getself.go`) gains an `Uptime` field (seconds),
  sourced from a start time recorded once in `core.Core`'s constructor —
  the one piece of node-level health data this spec needs that doesn't
  exist anywhere yet, and the minimum instrumentation to get it.

### New/extended admin handlers (`src/garlic/admin.go`)

- `getGarlicStats` (extended): existing `originatedCircuits`/
  `relayedCircuits` plus `originatedBytes`, `originatedPackets`,
  `relayedBytes`, `relayedPackets` (summed from the accessors above), and
  a `security` object with the five counters above.
- `getGarlicCircuits` (new): originated circuits (id, hop keys, state
  derived from expiry/closed, createdAt, expiresAt, packetsSent,
  bytesSent) and relayed circuits (id, previousHop, nextHop, firstSeen,
  lastActive, packetsRelayed, bytesRelayed) as two separate lists — never
  merged into one fabricated end-to-end path.

No new handlers needed on the plain-Yggdrasil side — `getSelf`, `getPeers`,
`getSessions`, `getTree`, `getPaths` already cover what this spec needs;
the dashboard's own `/api/*` layer aggregates them.

## Metrics: what's real, what's newly added, what's honestly not shown

- **Global "transit %"** (all Yggdrasil traffic): **not implemented.**
  Yggdrasil delegates actual mesh packet routing to the vendored
  `ironwood` library; `src/core` only sees per-link byte totals with no
  visibility into forwarded-vs-own traffic. Computing this would require
  patching a third-party dependency — out of scope. Instead, ordinary
  traffic is shown as two separate, honestly-labeled numbers: peer-link
  totals (`getPeers` — includes this node's own traffic and anything
  relayed at the link level, indistinguishable) and session totals
  (`getSessions` — only traffic where this node is itself an endpoint).
  Never subtracted into a derived figure.
- **"Transit % (Garlic)"**: implemented, scoped specifically to Garlic
  circuit traffic (which is this repo's own Go code, not delegated to
  ironwood): `relayedBytes / (originatedBytes + relayedBytes) × 100`,
  labeled exactly that way — "share of Garlic circuit traffic relayed for
  others" — never presented as an all-traffic figure.
- **Circuit topology**: originator may show its own full chosen path
  (already known to it, never derived from decrypting anyone else's
  traffic). A relay only ever shows "Previous → LOCAL → Next."
- **Security counters**: new local-only aggregates, as above.
- **Rendezvous/introduction points**: real code
  (`src/garlic/rendezvous.go`), but explicitly in-memory/static-config
  only per its own doc comment — shown as configured local state, not
  fabricated distributed topology.
- **Network graph**: Yggdrasil connectivity layer from `getTree`/
  `getPaths` (existing, real topology primitives); Garlic circuit layer
  from `getGarlicCircuits` above. Two distinct edge styles (section 14 of
  the brief), never inventing hops beyond what each handler actually
  reports.
- **Node status** (Online/Degraded/Offline): Online = admin socket
  reachable, `getSelf` responds. Degraded = admin socket reachable but
  zero peers currently up (`getPeers`) — a real, locally-derivable
  signal, not an invented health check. Offline/Disconnected = the
  dashboard server can't reach the admin socket at all (ambiguous whether
  yggdrasil itself is down vs. just unreachable from here — labeled
  honestly as "Disconnected," not asserted as "node offline").

## Frontend (`yggdashboard/`)

- SvelteKit 5, routes: `/`, `/connections`, `/circuits`, `/garlic`,
  `/graph`. Each has a `+page.server.ts` load function — real per-request
  SSR, so the initial HTML has real data with no JS required.
- `/api/status`, `/api/stats`, `/api/peers`, `/api/circuits`,
  `/api/garlic`, `/api/graph` as `+server.ts` endpoints — the only things
  the browser ever talks to. The admin socket itself never reaches the
  browser; response shapes are hand-picked allowlists of fields, never a
  pass-through of raw admin responses (so a field added to an admin
  handler later doesn't silently leak into the browser).
- One background poller inside the SvelteKit server (not per-browser-tab)
  polls the admin socket every 1-2s over a single persistent keepalive
  connection, and keeps a bounded ~5-minute in-memory ring buffer per
  live metric (RX/TX rate, transit rate, Garlic rate, active circuits,
  peer count). Every browser's poll to `/api/stats` reads the current
  buffer — admin-socket load doesn't scale with open tabs. History resets
  when the dashboard server restarts, per the brief.
- Client-side: a central `dashboard.svelte.ts` runes-based store
  (`$state`) polls the `/api/*` routes every 1-2s; components consume it
  reactively via `$derived`. No WebSockets — matches the brief's explicit
  preference and avoids the extra moving part the superseded design had.
- Graph rendering: plain SVG, `d3-force` for layout math only (~10KB, no
  rendering opinions) — not a heavyweight graph library, per the brief.
- Styling: dark neutral, high-density, monospace for keys/addresses/
  counters, restrained borders — no component/CSS framework, plain CSS in
  each component's `<style>` block (matches the superseded design's
  choice, still appropriate here).
- Components (indicative, not exhaustive): `StatusBar`, `NodeIdentity`,
  `MetricCard`, `TrafficChart`, `PeerTable`, `PeerDetail`,
  `CircuitTable`, `CircuitDetail`, `GarlicPanel`, `SecurityCounters`,
  `NetworkGraph`, `GraphLegend`.

## Security & access control

- `dashboard.enabled: false` and `dashboard.listen: 127.0.0.1:8080` by
  default. Never defaults to `0.0.0.0`/`::`. `::1` is an acceptable
  operator-chosen alternative.
- No authentication in the dashboard itself, matching the admin socket's
  own trust model (no authentication either — anyone who can reach it is
  already trusted). Remote access is via SSH tunnel, documented as a
  deliberate scope limit, not an oversight.
- No private keys, X25519/session/AEAD keys, decrypted payloads, or
  complete onion layers ever serialize into any `/api/*` response —
  enforced structurally (hand-picked response fields) and by an explicit
  backend test asserting no admin handler used by the dashboard ever
  returns a private-key field.
- The admin socket is reachable only from the spawned SvelteKit server
  process; the browser never receives its address or touches it directly.

## Testing

- Go: table tests for the new Garlic accessors/counters (hop exposure,
  relay prev/next tracking, security counter increments at each drop
  site), an admin-handler test asserting no private-key field ever
  appears in `getGarlicCircuits`/`getGarlicStats`/`getSelf` responses, a
  config test for the loopback default and the `AdminListen: "none"`
  rejection case.
- Vitest (adapted from the superseded worktree where applicable): admin
  socket client framing/keepalive/reconnect, the JSON stream extractor,
  the server-side poller and ring-buffer history, transit-%(Garlic)
  calculation, component rendering with mock data covering loading state,
  disconnected state, empty peer list, empty circuit list, graph with no
  nodes, graph with peers, graph with circuits.
- Manual verification against the real running Garlic-enabled node used
  earlier in this project, for both `dashboard.enabled: false` (no
  behavior change to yggdrasil) and `true` (loopback-only bind, live
  data, peers connecting/disconnecting, circuits opening/closing, Garlic
  disabled case still renders zeros rather than hiding the panel).

## Build/deployment

`yggdashboard/` builds via `npm run build` (adapter-node) independent of
the Go build (`./build` script untouched). The Go binary spawns the built
output when configured to. This is a real, working local dashboard for
anyone who builds and places the assets — not a static mockup.

## Out of scope / limitations

- **Packaging integration**: bundling the built dashboard and a Node.js
  runtime dependency into the `.deb`/`.rpm` packages
  (`contrib/{deb,rpm}/generate.sh`, `install.sh`) is real, sizable
  packaging work on its own and is explicitly not part of this pass.
  Manual build/placement instructions are documented in
  `yggdashboard/README.md` instead. This will be reported as a known
  limitation, not silently dropped.
- **Distributed rendezvous/introduction points**: the backend only has an
  in-memory/static implementation today (see `rendezvous.go`'s own doc
  comment); the dashboard can only ever show that, not a distributed
  DHT-backed view, because the latter doesn't exist in the backend yet.
- **Global (non-Garlic) transit %**: not implemented, and not
  implementable without patching the vendored `ironwood` dependency — see
  "Metrics" above.
