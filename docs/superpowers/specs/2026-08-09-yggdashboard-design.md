# yggdashboard — design spec

Status: superseded by
`docs/superpowers/specs/2026-08-10-yggdashboard-v2-design.md` (2026-08-10)
- kept for history. Its Phase 1 was fully implemented in an isolated,
never-merged git worktree (`.claude/worktrees/yggdashboard-phase1`,
branch `worktree-yggdashboard-phase1`); the v2 spec supersedes this
document's architecture (WebSocket push, single page, standalone Node
process, env-var config) with polling, multiple routed pages, a
yggdrasil-spawned process, and HJSON node-config integration, while
salvaging the old worktree's tested, architecture-agnostic protocol-level
code (JSON stream extractor, admin-socket client framing) where it still
fits. Companion to the Garlic Routing Overlay work (`docs/garlic-*.md`)
but independent of it - this dashboard shows any Yggdrasil node's state,
Garlic-specific panels are additive.

## Problem

There's no way to see a running node's live state (peers, traffic,
routing, Garlic circuits) without hand-typing `yggdrasilctl` commands.
Want a small web dashboard, run alongside the node, that shows this
continuously.

## Architecture

A new, independent SvelteKit project at `yggdashboard/` (the directory
already exists, empty). It is **not** part of the Go module or the
`./build` script - separate toolchain (Node), separate build, separate
process, started and stopped independently of `yggdrasil`.

```
┌─────────────┐        admin socket          ┌──────────────────┐
│  yggdrasil   │◄─────(unix or tcp, JSON)─────┤  yggdashboard     │
│  (Go, on the │                              │  SvelteKit SSR    │
│  same host)  │                              │  Node process     │
└─────────────┘                              └──────────┬────────┘
                                                          │ WebSocket
                                                          ▼
                                                    ┌───────────┐
                                                    │  Browser   │
                                                    │  (localhost│
                                                    │  or SSH    │
                                                    │  tunnel)   │
                                                    └───────────┘
```

Runs on the same host as the node it's showing. No multi-node
aggregation in this design - one dashboard instance, one node.

## Admin socket client

The admin socket protocol (`src/admin/admin.go`) is simple and already
fully understood from reading the Go implementation directly:

- Transport: `net.Listen("unix", path)` or `net.Listen("tcp", addr)`,
  whichever `AdminListen` in the node's config specifies.
- Wire format: no length prefix or delimiter - `encoding/json`'s
  `Decoder`/`Encoder` write/read back-to-back JSON values on the raw
  stream. A client mirrors this: write one JSON object, read one JSON
  object back.
- Request: `{"request": "<name>", "arguments": {...}, "keepalive": bool}`
- Response: `{"status": "success"|"error", "error": "...", "request": {...}, "response": {...}}`
- **`keepalive: true` matters for this project specifically**: without
  it, the server closes the connection after exactly one
  request/response (`src/admin/admin.go`, `handleRequest`'s `if
  !req.KeepAlive { break }`). With it, the same connection accepts
  further requests. The dashboard's poll loop needs many requests every
  1-2 seconds - reconnecting per request would be wasteful and racy
  under load - so it opens **one persistent keepalive connection** and
  pipelines every poll request/response over it, with reconnect-on-drop
  if the node restarts.

Implementation: a small hand-written TypeScript module
(`yggdashboard/src/lib/server/admin-client.ts` or similar) using
Node's `net.Socket` (unix path or host:port from config). No external
dependency - the protocol is a few dozen lines to implement correctly,
and a hand-rolled client is easier to keep in sync with the Go side
than a generic JSON-RPC library would be.

## Data polled (Phase 1)

All existing admin handlers, no new Go code needed:

| Handler | Source | Gives |
|---|---|---|
| `getSelf` | `src/admin/getself.go` | build name/version, own key, IPv6 address/subnet, routing table size |
| `getPeers` | `src/admin/getpeers.go` | per peer-link: remote URI, up/inbound, key, RX/TX bytes, RX/TX rate, uptime, latency, last error |
| `getSessions` | `src/admin/getsessions.go` | per end-to-end session (where this node is a party): key, RX/TX bytes, uptime |
| `getGarlicStats` | `src/garlic/admin.go` | `{originatedCircuits, relayedCircuits}` - zero-valued/absent gracefully if Garlic disabled |
| `getGarlicKnownPeers` | `src/garlic/admin.go` | `{peers: [{nodeKey, garlicPublicKey, lastSeen}]}` |

Traffic display: **`getPeers` totals and `getSessions` totals are shown
as two separate, honestly-labeled numbers, never subtracted from each
other.** Peer-link totals include this node's own traffic *and*
anything it's relaying for others (Yggdrasil doesn't separate these at
the link level); session totals are only traffic where this node is
itself an endpoint. The difference is not exposed as a computed
"relay" figure - it would silently fold in protocol/DHT overhead and
mislead.

If Garlic is disabled on the polled node, `getGarlicStats` still
returns zero counts (per `docs/garlic-architecture.md`'s "behaves
identically to no Garlic support" guarantee) - the dashboard shows the
Garlic panel with zeros rather than hiding it, so it's visible that
Garlic *could* be enabled.

## Real-time updates

The SSR server's poll loop (default interval: 2s, overridable via env
var) runs the table above over the one persistent keepalive
connection, then broadcasts the combined snapshot as one JSON message
to every connected WebSocket client. One upstream poll serves every
open browser tab - polling is not duplicated per client.

Client-side: Svelte 5 runes hold the latest snapshot; components
subscribe reactively. Reconnect-with-backoff if the WebSocket drops.

## Phase 1 scope (tables)

Pages/sections, all on what is effectively one dashboard view (no
routing complexity needed yet):

- **Node**: build name/version, key, IPv6 address/subnet, routing
  table size (from `getSelf`).
- **Peers**: table from `getPeers` - remote, up/inbound, key
  (truncated + copyable), RX/TX bytes, RX/TX rate, uptime, latency.
- **Sessions**: table from `getSessions` - this node's own end-to-end
  traffic, separate from the Peers table, per the traffic-split
  decision above.
- **Garlic**: `originatedCircuits`/`relayedCircuits` counters plus a
  table of known Garlic peers (`getGarlicKnownPeers`).

Styling: plain CSS in Svelte component `<style>` blocks. No Tailwind,
no component/CSS framework.

## Phase 2 scope (topology graph) - separate follow-up, not this round

Graph built from `getTree` (edges: key → parent) with `getPaths`
overlaid (highlight resolved paths). Layout: `d3-force` (layout math
only, ~10KB, no opinions about rendering) driving a plain SVG element
rendered by a Svelte component - keeps full styling control and avoids
pulling in a heavier graph-visualization library's own DOM/CSS/theming.
Revisit this choice at Phase 2 kickoff if `d3-force`'s output doesn't
look good enough for the tree shapes actually seen in practice;
Cytoscape.js is the fallback (more built-in layout/interaction, less
code to write, heavier and less stylable).

Explicitly not designed in this document - Phase 2 gets its own
planning pass before implementation starts.

## Access control

No authentication in the dashboard's own code, matching the admin
socket's own trust model (it has none either - `yggdrasilctl` assumes
whoever can reach the socket is trusted). The dashboard's HTTP/WS
listener binds to `127.0.0.1` only by default. Remote access is via
`ssh -L <local-port>:127.0.0.1:<dashboard-port> user@host`, not a
built-in feature. This is a deliberate v1 scope limit, documented as
such in `yggdashboard/README.md` - not an oversight to flag later.

## Testing

- Vitest unit tests on the admin-socket client module: request
  framing, response parsing, keepalive reuse across multiple
  request/response pairs on one mocked socket, reconnect-on-drop
  behavior. This is the one piece of custom protocol code in the
  project and the most likely place for a subtle bug (e.g. two
  responses arriving concatenated in one TCP read).
- No Playwright/e2e in this phase - revisit if the UI grows complex
  enough to need it.

## Repo layout

```
yggdashboard/
  package.json          # separate Node toolchain, not part of go.mod/./build
  svelte.config.js
  src/
    lib/
      server/
        admin-client.ts # the protocol client described above
        poll.ts         # poll loop + WS broadcast
      components/       # Peers.svelte, Sessions.svelte, Node.svelte, Garlic.svelte
    routes/
      +page.svelte      # Phase 1: single-page dashboard
  README.md             # how to configure (admin socket path, listen host:port), run, and the access-control note above
```

SvelteKit + `adapter-node` doesn't give WebSockets a first-class route
type the way HTTP routes get `+server.ts` - the standard pattern is a
custom Node server entry point that creates the HTTP server itself,
hands requests to the SvelteKit handler, and attaches a `ws` (or
similar) WebSocket server to the same underlying `http.Server`. Exact
file layout for that (`vite.config.ts` plugin vs a custom
`build/index.js` wrapper) is an implementation detail to pin down
during planning, not a design decision - it doesn't change anything
described above.

Config: environment variables (admin socket address, dashboard listen
host:port, poll interval) - no separate config file format needed for
this scope.

## Out of scope (this spec)

- Multi-node aggregation / fleet view.
- Any authentication/authorization.
- Historical data / time-series storage (dashboard shows current live
  state only, no database).
- Phase 2 (topology graph) implementation detail - scoped above only
  at the "what and roughly how" level, gets its own pass later.
