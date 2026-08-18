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
