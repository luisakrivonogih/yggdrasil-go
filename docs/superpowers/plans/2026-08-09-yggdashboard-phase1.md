# yggdashboard Phase 1 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build a live-updating web dashboard (`yggdashboard/`) showing a running Yggdrasil/Garlic node's peers, sessions, and Garlic circuit stats, per `docs/superpowers/specs/2026-08-09-yggdashboard-design.md`.

**Architecture:** A standalone SvelteKit (Svelte 5) app with a custom Node HTTP server. The server holds one persistent, keepalive admin-socket connection to the local `yggdrasil` node, polls it every 2s, and broadcasts the combined snapshot over WebSocket to every connected browser. The browser renders four sections (Node, Peers, Sessions, Garlic) from that snapshot, no client-side polling.

**Tech Stack:** SvelteKit 2 + Svelte 5, `@sveltejs/adapter-node`, `ws` (WebSocket server), `tsx` (run the custom TS server entrypoint without a separate compile step), Vitest.

## Global Constraints

- Node 20 LTS or later.
- `yggdashboard/` is a separate project: its own `package.json`, own toolchain, **not** part of `go.mod` or the root `./build` script. Nothing in `src/`, `cmd/`, or the root Go build is touched by this plan.
- No Tailwind, no CSS framework, no component library - plain CSS in each Svelte component's `<style>` block.
- No authentication anywhere in this phase. The dashboard HTTP/WS listener binds to `127.0.0.1` only by default (env-overridable, but the default must stay loopback-only).
- Traffic is always shown as two separate numbers - peer-link totals (`getPeers`) and session totals (`getSessions`) - **never** subtracted or combined into a derived "relay" figure.
- Vitest unit tests are required for the admin-socket client (framing, keepalive reuse, reconnect-on-drop) per the design spec. This plan also adds cheap, high-value unit tests for the other pure-logic server modules (JSON stream parsing, the poller) using the same TDD discipline - not a scope expansion, just consistent engineering.
- No Playwright/e2e tests this phase. UI tasks are verified manually against the real running node.
- Phase 2 (topology graph, `getTree`/`getPaths`) is explicitly out of scope for this plan.

---

### Task 1: Project scaffold

**Files:**
- Create: `yggdashboard/package.json`
- Create: `yggdashboard/svelte.config.js`
- Create: `yggdashboard/vite.config.ts`
- Create: `yggdashboard/tsconfig.json`
- Create: `yggdashboard/src/app.html`
- Create: `yggdashboard/src/routes/+layout.svelte`
- Create: `yggdashboard/src/routes/+page.svelte`
- Create: `yggdashboard/.gitignore`

**Interfaces:**
- Consumes: nothing (first task).
- Produces: a working `npm run dev` / `npm run build` SvelteKit project at `yggdashboard/`, ready for later tasks to add `server/` modules and real pages.

- [ ] **Step 1: Create `yggdashboard/package.json`**

```json
{
  "name": "yggdashboard",
  "version": "0.1.0",
  "private": true,
  "type": "module",
  "scripts": {
    "dev": "vite dev",
    "build": "vite build",
    "start": "tsx server.ts",
    "test": "vitest run",
    "check": "svelte-kit sync && svelte-check --tsconfig ./tsconfig.json"
  },
  "devDependencies": {
    "@sveltejs/adapter-node": "^5.2.0",
    "@sveltejs/kit": "^2.9.0",
    "@sveltejs/vite-plugin-svelte": "^5.0.0",
    "@types/node": "^22.10.0",
    "@types/ws": "^8.5.13",
    "svelte": "^5.16.0",
    "svelte-check": "^4.1.0",
    "tsx": "^4.19.0",
    "typescript": "^5.7.0",
    "vite": "^6.0.0",
    "vitest": "^3.0.0"
  },
  "dependencies": {
    "ws": "^8.18.0"
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
    include: ['server/**/*.test.ts']
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
  },
  "include": ["src/**/*", "server/**/*", "server.ts", "vite.config.ts"]
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

- [ ] **Step 6: Create `yggdashboard/src/routes/+layout.svelte`**

```svelte
<script lang="ts">
  let { children } = $props();
</script>

{@render children()}
```

- [ ] **Step 7: Create `yggdashboard/src/routes/+page.svelte`** (placeholder, replaced in Task 9)

```svelte
<h1>yggdashboard</h1>
<p>Scaffold OK.</p>
```

- [ ] **Step 8: Create `yggdashboard/.gitignore`**

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

- [ ] **Step 9: Install dependencies and verify the dev server boots**

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

- [ ] **Step 10: Commit**

```bash
git add yggdashboard/package.json yggdashboard/svelte.config.js yggdashboard/vite.config.ts \
  yggdashboard/tsconfig.json yggdashboard/src/app.html yggdashboard/src/routes/+layout.svelte \
  yggdashboard/src/routes/+page.svelte yggdashboard/.gitignore yggdashboard/package-lock.json
git commit -m "yggdashboard: scaffold SvelteKit project"
```

---

### Task 2: JSON stream parser

**Files:**
- Create: `yggdashboard/server/json-stream.ts`
- Test: `yggdashboard/server/json-stream.test.ts`

**Interfaces:**
- Consumes: nothing.
- Produces: `extractJSONValues(buffer: string): { values: unknown[]; rest: string }` - used by Task 3's `AdminClient` to pull complete JSON values off the raw socket stream (the admin socket protocol has no length prefix or delimiter; `encoding/json`'s `Decoder`/`Encoder` just write/read back-to-back JSON values, so a client must track brace/bracket depth itself to find value boundaries).

- [ ] **Step 1: Write the failing tests**

Create `yggdashboard/server/json-stream.test.ts`:

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

  it('extracts multiple values separated by newlines (matches encoding/json.Encoder\'s trailing newline)', () => {
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

Run: `cd yggdashboard && npx vitest run server/json-stream.test.ts`
Expected: FAIL - `Cannot find module './json-stream'` (file doesn't exist yet).

- [ ] **Step 3: Implement `yggdashboard/server/json-stream.ts`**

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

Run: `cd yggdashboard && npx vitest run server/json-stream.test.ts`
Expected: PASS, all 9 tests green.

- [ ] **Step 5: Commit**

```bash
git add yggdashboard/server/json-stream.ts yggdashboard/server/json-stream.test.ts
git commit -m "yggdashboard: add incremental JSON stream value extractor"
```

---

### Task 3: Admin socket client

**Files:**
- Create: `yggdashboard/server/admin-client.ts`
- Test: `yggdashboard/server/admin-client.test.ts`

**Interfaces:**
- Consumes: `extractJSONValues` from Task 2 (`./json-stream`).
- Produces:
  - `parseAdminAddress(address: string): { path: string } | { host: string; port: number }`
  - `class AdminClient { constructor(address: string); request<T = unknown>(name: string, args?: Record<string, unknown>): Promise<T>; close(): void }`
  - Used by Task 5's `Poller`.

- [ ] **Step 1: Write the failing tests**

Create `yggdashboard/server/admin-client.test.ts`:

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

    // Both responses delivered in a single 'data' event, concatenated -
    // this is exactly the scenario extractJSONValues (Task 2) exists for.
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

- [ ] **Step 2: Run the tests to verify they fail**

Run: `cd yggdashboard && npx vitest run server/admin-client.test.ts`
Expected: FAIL - `Cannot find module './admin-client'`.

- [ ] **Step 3: Implement `yggdashboard/server/admin-client.ts`**

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
 * tcp://host:port convention Yggdrasil's own AdminListen config and
 * yggdrasilctl -endpoint flag use.
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
 * were sent, which is safe because the Go server itself processes
 * requests on one connection sequentially, one at a time
 * (AdminSocket.handleRequest's decode/handle/encode loop), so responses
 * are written back in the same order requests were read.
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

- [ ] **Step 4: Run the tests to verify they pass**

Run: `cd yggdashboard && npx vitest run server/admin-client.test.ts`
Expected: PASS, all 9 tests green.

- [ ] **Step 5: Commit**

```bash
git add yggdashboard/server/admin-client.ts yggdashboard/server/admin-client.test.ts
git commit -m "yggdashboard: add admin socket client with keepalive reuse and reconnect"
```

---

### Task 4: Shared types and config

**Files:**
- Create: `yggdashboard/server/types.ts`
- Create: `yggdashboard/server/config.ts`
- Test: `yggdashboard/server/config.test.ts`

**Interfaces:**
- Consumes: nothing.
- Produces:
  - Types: `SelfInfo`, `PeerEntry`, `SessionEntry`, `GarlicStats`, `GarlicKnownPeer`, `Snapshot` - mirror the Go admin handlers' JSON shapes exactly (`src/admin/getself.go`, `src/admin/getpeers.go`, `src/admin/getsessions.go`, `src/garlic/admin.go`).
  - `loadConfig(): DashboardConfig` with fields `adminSocket: string`, `host: string`, `port: number`, `pollIntervalMs: number`.
  - Used by Task 5 (`Poller`), Task 7 (`server.ts`, `vite.config.ts`), and the frontend (Task 8/9, via relative import).

- [ ] **Step 1: Create `yggdashboard/server/types.ts`** (no test - plain type declarations, nothing to assert against)

```ts
/**
 * Wire types for Yggdrasil's admin socket responses. Field names and
 * optionality mirror the Go structs exactly:
 * - SelfInfo: src/admin/getself.go GetSelfResponse (no omitempty - always present)
 * - PeerEntry: src/admin/getpeers.go PeerEntry (several fields omitempty -
 *   optional here, meaning "zero value" on the Go side, e.g. a peer with
 *   0 bytes received omits bytes_recvd entirely)
 * - SessionEntry: src/admin/getsessions.go SessionEntry (no omitempty)
 * - GarlicStats / GarlicKnownPeer: src/garlic/admin.go's getGarlicStats /
 *   getGarlicKnownPeers handlers (hand-built maps, always fully populated)
 */

export interface SelfInfo {
  build_name: string;
  build_version: string;
  key: string;
  address: string;
  subnet: string;
  routing_entries: number;
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
  uptime?: number;
  /** Nanoseconds (Go time.Duration, encoded as a plain int64 by encoding/json). */
  latency?: number;
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

export interface GarlicStats {
  originatedCircuits: number;
  relayedCircuits: number;
}

export interface GarlicKnownPeer {
  nodeKey: string;
  garlicPublicKey: string;
  lastSeen: string;
}

/** The combined, per-poll snapshot broadcast to every connected browser. */
export interface Snapshot {
  self: SelfInfo;
  peers: PeerEntry[];
  sessions: SessionEntry[];
  garlicStats: GarlicStats;
  garlicKnownPeers: GarlicKnownPeer[];
  polledAt: string;
}

export const EMPTY_SELF: SelfInfo = {
  build_name: '',
  build_version: '',
  key: '',
  address: '',
  subnet: '',
  routing_entries: 0
};

export const EMPTY_SNAPSHOT: Snapshot = {
  self: EMPTY_SELF,
  peers: [],
  sessions: [],
  garlicStats: { originatedCircuits: 0, relayedCircuits: 0 },
  garlicKnownPeers: [],
  polledAt: new Date(0).toISOString()
};
```

- [ ] **Step 2: Write the failing test for config**

Create `yggdashboard/server/config.test.ts`:

```ts
import { describe, it, expect, beforeEach, afterEach } from 'vitest';
import { loadConfig } from './config';

const ENV_KEYS = ['ADMIN_SOCKET', 'DASHBOARD_HOST', 'DASHBOARD_PORT', 'POLL_INTERVAL_MS'] as const;
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
  it('defaults to the local unix socket, loopback host, and a 2s poll interval', () => {
    const config = loadConfig();
    expect(config).toEqual({
      adminSocket: 'unix:///var/run/yggdrasil/yggdrasil.sock',
      host: '127.0.0.1',
      port: 8787,
      pollIntervalMs: 2000
    });
  });

  it('reads every field from the environment when set', () => {
    process.env.ADMIN_SOCKET = 'tcp://127.0.0.1:9001';
    process.env.DASHBOARD_HOST = '0.0.0.0';
    process.env.DASHBOARD_PORT = '9090';
    process.env.POLL_INTERVAL_MS = '5000';

    expect(loadConfig()).toEqual({
      adminSocket: 'tcp://127.0.0.1:9001',
      host: '0.0.0.0',
      port: 9090,
      pollIntervalMs: 5000
    });
  });
});
```

- [ ] **Step 3: Run the test to verify it fails**

Run: `cd yggdashboard && npx vitest run server/config.test.ts`
Expected: FAIL - `Cannot find module './config'`.

- [ ] **Step 4: Implement `yggdashboard/server/config.ts`**

```ts
export interface DashboardConfig {
  adminSocket: string;
  host: string;
  port: number;
  pollIntervalMs: number;
}

export function loadConfig(): DashboardConfig {
  return {
    adminSocket: process.env.ADMIN_SOCKET ?? 'unix:///var/run/yggdrasil/yggdrasil.sock',
    host: process.env.DASHBOARD_HOST ?? '127.0.0.1',
    port: Number(process.env.DASHBOARD_PORT ?? 8787),
    pollIntervalMs: Number(process.env.POLL_INTERVAL_MS ?? 2000)
  };
}
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `cd yggdashboard && npx vitest run server/config.test.ts`
Expected: PASS, both tests green.

- [ ] **Step 6: Commit**

```bash
git add yggdashboard/server/types.ts yggdashboard/server/config.ts yggdashboard/server/config.test.ts
git commit -m "yggdashboard: add shared wire types and env-based config"
```

---

### Task 5: Poller

**Files:**
- Create: `yggdashboard/server/poll.ts`
- Test: `yggdashboard/server/poll.test.ts`

**Interfaces:**
- Consumes: `AdminClient` from Task 3 (only its `request<T>(name, args?)` method - tests use a fake implementing the same shape, not a real socket), `Snapshot`/`EMPTY_SNAPSHOT` types from Task 4.
- Produces: `class Poller { constructor(client: AdminClient, intervalMs: number); start(): void; stop(): void; subscribe(listener: (snapshot: Snapshot) => void): () => void }`. Used by Task 6 (`ws-attach.ts`) and Task 7 (`server.ts`, `vite.config.ts`).

- [ ] **Step 1: Write the failing tests**

Create `yggdashboard/server/poll.test.ts`:

```ts
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { Poller } from './poll';
import type { AdminClient } from './admin-client';

function fakeClient(responses: Record<string, unknown>): AdminClient {
  return {
    request: vi.fn(async (name: string) => {
      if (!(name in responses)) throw new Error(`unexpected request '${name}'`);
      return responses[name];
    })
  } as unknown as AdminClient;
}

const FULL_RESPONSES = {
  getSelf: { build_name: 'yggdrasil', build_version: '0.5.14', key: 'abc', address: '200::1', subnet: '300::/64', routing_entries: 1 },
  getPeers: { peers: [{ key: 'peer1', up: true, inbound: false, port: 1, priority: 0, cost: 1 }] },
  getSessions: { sessions: [] },
  getGarlicStats: { originatedCircuits: 1, relayedCircuits: 0 },
  getGarlicKnownPeers: { peers: [] }
};

describe('Poller', () => {
  beforeEach(() => vi.useFakeTimers());
  afterEach(() => vi.useRealTimers());

  it('builds a snapshot from all five admin responses and notifies subscribers', async () => {
    const client = fakeClient(FULL_RESPONSES);
    const poller = new Poller(client, 2000);
    const received: any[] = [];
    poller.subscribe((snapshot) => received.push(snapshot));

    poller.start();
    await vi.advanceTimersByTimeAsync(0);

    expect(received).toHaveLength(1);
    expect(received[0].self.build_name).toBe('yggdrasil');
    expect(received[0].peers).toHaveLength(1);
    expect(received[0].garlicStats.originatedCircuits).toBe(1);
    poller.stop();
  });

  it('sends the current snapshot immediately to a subscriber that joins after the first poll', async () => {
    const client = fakeClient(FULL_RESPONSES);
    const poller = new Poller(client, 2000);
    poller.start();
    await vi.advanceTimersByTimeAsync(0);

    const received: any[] = [];
    poller.subscribe((snapshot) => received.push(snapshot));
    expect(received).toHaveLength(1);
    expect(received[0].self.build_name).toBe('yggdrasil');
    poller.stop();
  });

  it('keeps the last known value for a field whose request rejects, and still notifies with the rest', async () => {
    const client = {
      request: vi.fn(async (name: string) => {
        if (name === 'getPeers') throw new Error('boom');
        return (FULL_RESPONSES as Record<string, unknown>)[name];
      })
    } as unknown as AdminClient;
    const poller = new Poller(client, 2000);
    const received: any[] = [];
    poller.subscribe((snapshot) => received.push(snapshot));

    poller.start();
    await vi.advanceTimersByTimeAsync(0);

    expect(received).toHaveLength(1);
    expect(received[0].peers).toEqual([]); // fell back to the empty initial snapshot's peers
    expect(received[0].self.build_name).toBe('yggdrasil'); // unaffected field still updates
    poller.stop();
  });

  it('polls again after the configured interval', async () => {
    const client = fakeClient(FULL_RESPONSES);
    const poller = new Poller(client, 2000);
    const received: unknown[] = [];
    poller.subscribe((s) => received.push(s));

    poller.start();
    await vi.advanceTimersByTimeAsync(0);
    await vi.advanceTimersByTimeAsync(2000);

    expect(received).toHaveLength(2);
    poller.stop();
  });

  it('stops polling and lets an unsubscribe function remove a listener', async () => {
    const client = fakeClient(FULL_RESPONSES);
    const poller = new Poller(client, 2000);
    const received: unknown[] = [];
    const unsubscribe = poller.subscribe((s) => received.push(s));

    poller.start();
    await vi.advanceTimersByTimeAsync(0);
    unsubscribe();
    await vi.advanceTimersByTimeAsync(2000);

    expect(received).toHaveLength(1); // only the poll before unsubscribing
    poller.stop();
    await vi.advanceTimersByTimeAsync(4000);
    expect(received).toHaveLength(1); // stop() halted further polling entirely
  });
});
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `cd yggdashboard && npx vitest run server/poll.test.ts`
Expected: FAIL - `Cannot find module './poll'`.

- [ ] **Step 3: Implement `yggdashboard/server/poll.ts`**

```ts
import type { AdminClient } from './admin-client';
import { EMPTY_SNAPSHOT, type Snapshot } from './types';

type Listener = (snapshot: Snapshot) => void;

/**
 * Polls the five admin endpoints Phase 1 needs over one shared
 * AdminClient (see design spec: one persistent keepalive connection,
 * pipelined) and broadcasts the combined Snapshot to every subscriber.
 * If an individual request fails, that field falls back to its
 * previous value (or the empty default before any successful poll)
 * rather than dropping the whole update - a transient failure on one
 * endpoint shouldn't blank out data that's still good.
 */
export class Poller {
  private client: AdminClient;
  private intervalMs: number;
  private timer: ReturnType<typeof setInterval> | null = null;
  private listeners = new Set<Listener>();
  private latest: Snapshot = EMPTY_SNAPSHOT;

  constructor(client: AdminClient, intervalMs: number) {
    this.client = client;
    this.intervalMs = intervalMs;
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

  subscribe(listener: Listener): () => void {
    this.listeners.add(listener);
    if (this.latest !== EMPTY_SNAPSHOT) listener(this.latest);
    return () => {
      this.listeners.delete(listener);
    };
  }

  private async tick(): Promise<void> {
    const results = await Promise.allSettled([
      this.client.request('getSelf'),
      this.client.request<{ peers: Snapshot['peers'] }>('getPeers'),
      this.client.request<{ sessions: Snapshot['sessions'] }>('getSessions'),
      this.client.request('getGarlicStats'),
      this.client.request<{ peers: Snapshot['garlicKnownPeers'] }>('getGarlicKnownPeers')
    ]);
    const [selfRes, peersRes, sessionsRes, garlicStatsRes, garlicPeersRes] = results;

    const snapshot: Snapshot = {
      self: selfRes.status === 'fulfilled' ? (selfRes.value as Snapshot['self']) : this.latest.self,
      peers: peersRes.status === 'fulfilled' ? peersRes.value.peers : this.latest.peers,
      sessions: sessionsRes.status === 'fulfilled' ? sessionsRes.value.sessions : this.latest.sessions,
      garlicStats:
        garlicStatsRes.status === 'fulfilled' ? (garlicStatsRes.value as Snapshot['garlicStats']) : this.latest.garlicStats,
      garlicKnownPeers: garlicPeersRes.status === 'fulfilled' ? garlicPeersRes.value.peers : this.latest.garlicKnownPeers,
      polledAt: new Date().toISOString()
    };

    for (const [i, r] of results.entries()) {
      if (r.status === 'rejected') {
        console.error(`yggdashboard: poll request ${i} failed:`, r.reason);
      }
    }

    this.latest = snapshot;
    for (const listener of this.listeners) listener(snapshot);
  }
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `cd yggdashboard && npx vitest run server/poll.test.ts`
Expected: PASS, all 5 tests green.

- [ ] **Step 5: Commit**

```bash
git add yggdashboard/server/poll.ts yggdashboard/server/poll.test.ts
git commit -m "yggdashboard: add poller broadcasting snapshots from the admin client"
```

---

### Task 6: WebSocket attachment

**Files:**
- Create: `yggdashboard/server/ws-attach.ts`

**Interfaces:**
- Consumes: `Poller` from Task 5 (`.subscribe`).
- Produces: `attachWebSocketServer(httpServer: import('node:http').Server, poller: Poller): WebSocketServer` - used by Task 7's `server.ts` (production) and `vite.config.ts` (dev).

No dedicated unit test for this task: it is a thin, 15-line wire-up between two already-tested pieces (`Poller`, tested in Task 5; the `ws` library, third-party and trusted) with no branching logic of its own. Verified manually in Step 2 below and again end-to-end in Task 7.

- [ ] **Step 1: Implement `yggdashboard/server/ws-attach.ts`**

```ts
import type { Server as HTTPServer } from 'node:http';
import { WebSocketServer } from 'ws';
import type { Poller } from './poll';

/**
 * Attaches a WebSocket server to httpServer that pushes every Poller
 * snapshot to every connected client, for as long as it stays
 * connected. One shared Poller serves every browser tab - the poll
 * loop itself doesn't run any more often for more connected clients.
 */
export function attachWebSocketServer(httpServer: HTTPServer, poller: Poller): WebSocketServer {
  const wss = new WebSocketServer({ server: httpServer });
  wss.on('connection', (ws) => {
    const unsubscribe = poller.subscribe((snapshot) => {
      if (ws.readyState === ws.OPEN) {
        ws.send(JSON.stringify(snapshot));
      }
    });
    ws.on('close', unsubscribe);
    ws.on('error', unsubscribe);
  });
  return wss;
}
```

- [ ] **Step 2: Manually verify against a plain HTTP server**

Run this ad hoc script to confirm a connecting client receives a broadcasted snapshot, using a fake `AdminClient` (no real Yggdrasil node needed for this check - Task 7 does the real-node check):

```bash
cd yggdashboard && cat > /tmp/ws-attach-check.mjs << 'EOF'
import { createServer } from 'node:http';
import { WebSocket } from 'ws';
import { Poller } from './server/poll.ts';
import { attachWebSocketServer } from './server/ws-attach.ts';

const fakeClient = { request: async (name) => ({ ok: name }) };
const poller = new Poller(fakeClient, 100000);
poller.start();

const server = createServer((_req, res) => res.end('ok'));
attachWebSocketServer(server, poller);
server.listen(0, '127.0.0.1', () => {
  const port = server.address().port;
  const ws = new WebSocket(`ws://127.0.0.1:${port}`);
  ws.on('message', (data) => {
    console.log('received:', data.toString().slice(0, 40));
    ws.close();
    server.close();
    poller.stop();
    process.exit(0);
  });
  setTimeout(() => { console.error('TIMEOUT - no message received'); process.exit(1); }, 3000);
});
EOF
npx tsx /tmp/ws-attach-check.mjs
rm /tmp/ws-attach-check.mjs
```

Expected: prints `received: {...}` and exits 0. If it times out, check `ws-attach.ts` and `poll.ts` for a wiring mistake before continuing.

- [ ] **Step 3: Commit**

```bash
git add yggdashboard/server/ws-attach.ts
git commit -m "yggdashboard: attach WebSocket server broadcasting poller snapshots"
```

---

### Task 7: Server entrypoints (production + dev)

**Files:**
- Create: `yggdashboard/server.ts`
- Modify: `yggdashboard/vite.config.ts`

**Interfaces:**
- Consumes: `AdminClient` (Task 3), `Poller` (Task 5), `attachWebSocketServer` (Task 6), `loadConfig` (Task 4).
- Produces: a running dashboard server in both `npm run dev` (Vite dev server + WS) and `npm run build && npm start` (adapter-node's built handler + WS) modes.

- [ ] **Step 1: Create `yggdashboard/server.ts`** (production entrypoint, run via `tsx server.ts`, not part of SvelteKit's own build)

```ts
import { createServer } from 'node:http';
import { handler } from './build/handler.js';
import { AdminClient } from './server/admin-client';
import { loadConfig } from './server/config';
import { Poller } from './server/poll';
import { attachWebSocketServer } from './server/ws-attach';

const config = loadConfig();
const client = new AdminClient(config.adminSocket);
const poller = new Poller(client, config.pollIntervalMs);
poller.start();

const httpServer = createServer(handler);
attachWebSocketServer(httpServer, poller);

httpServer.listen(config.port, config.host, () => {
  console.log(`yggdashboard listening on http://${config.host}:${config.port}`);
});
```

- [ ] **Step 2: Update `yggdashboard/vite.config.ts`** to attach the same WebSocket wiring to Vite's dev server

```ts
import { sveltekit } from '@sveltejs/kit/vite';
import { defineConfig } from 'vite';
import { AdminClient } from './server/admin-client';
import { loadConfig } from './server/config';
import { Poller } from './server/poll';
import { attachWebSocketServer } from './server/ws-attach';

export default defineConfig({
  plugins: [
    sveltekit(),
    {
      name: 'yggdashboard-dev-ws',
      configureServer(viteServer) {
        const config = loadConfig();
        const client = new AdminClient(config.adminSocket);
        const poller = new Poller(client, config.pollIntervalMs);
        poller.start();
        if (viteServer.httpServer) {
          attachWebSocketServer(viteServer.httpServer, poller);
        }
      }
    }
  ],
  test: {
    include: ['server/**/*.test.ts']
  }
});
```

- [ ] **Step 3: Verify the dev path against the real running local node**

This machine already has a Garlic-enabled `yggdrasil` node running (installed and verified earlier in this project). Confirm its admin socket path first:

```bash
sudo systemctl show yggdrasil -p ExecStart --no-pager
# or, if AdminListen is set explicitly in the config:
grep -A1 AdminListen /etc/yggdrasil/yggdrasil.conf 2>/dev/null || echo "using compiled-in default: unix:///var/run/yggdrasil/yggdrasil.sock"
```

Run:
```bash
cd yggdashboard
ADMIN_SOCKET=unix:///var/run/yggdrasil/yggdrasil.sock npm run dev -- --port 5173 &
sleep 3
```

In a browser (or via `wscat -c ws://localhost:5173` if installed), open `http://localhost:5173` and confirm the page loads (still the Task 1 placeholder - real UI comes in Task 9), then check the dev server process's stdout for `yggdashboard: poll request` error lines - there should be none if the admin socket is reachable. If `ADMIN_SOCKET` points at a unix socket owned by `root:yggdrasil`, run the dev server with a user that can read it (matches the access pattern already established earlier in this project - either run as root, or as a member of the `yggdrasil` group).

Stop the dev server:
```bash
kill %1
```

- [ ] **Step 4: Verify the production path**

```bash
cd yggdashboard
npm run build
ADMIN_SOCKET=unix:///var/run/yggdrasil/yggdrasil.sock npm start &
sleep 3
curl -s -o /dev/null -w "%{http_code}\n" http://localhost:8787
kill %1
```
Expected: prints `200`.

- [ ] **Step 5: Commit**

```bash
git add yggdashboard/server.ts yggdashboard/vite.config.ts
git commit -m "yggdashboard: wire admin client, poller, and WebSocket into dev and production servers"
```

---

### Task 8: Frontend WebSocket store

**Files:**
- Create: `yggdashboard/src/lib/ws-client.svelte.ts`

**Interfaces:**
- Consumes: `Snapshot` type from `../../server/types` (Task 4).
- Produces: `dashboard: { readonly snapshot: Snapshot | null; readonly connected: boolean }` - a Svelte 5 runes-based reactive object. Used by Task 9's `+page.svelte` and section components.

No dedicated automated test (per the design spec's testing scope): this is browser-only code (uses the global `WebSocket`/`location`), verified manually in the browser in Task 9 once there's a UI to see it render. The file uses the `.svelte.ts` extension specifically because Svelte 5 runes (`$state`) are only usable in files with that extension outside of `.svelte` components.

- [ ] **Step 1: Create `yggdashboard/src/lib/ws-client.svelte.ts`**

```ts
import type { Snapshot } from '../../server/types';

function createDashboardStore() {
  let snapshot = $state<Snapshot | null>(null);
  let connected = $state(false);

  function connect() {
    const protocol = location.protocol === 'https:' ? 'wss' : 'ws';
    const ws = new WebSocket(`${protocol}://${location.host}`);

    ws.onopen = () => {
      connected = true;
    };
    ws.onclose = () => {
      connected = false;
      setTimeout(connect, 1000);
    };
    ws.onerror = () => {
      ws.close();
    };
    ws.onmessage = (event) => {
      snapshot = JSON.parse(event.data) as Snapshot;
    };
  }

  connect();

  return {
    get snapshot() {
      return snapshot;
    },
    get connected() {
      return connected;
    }
  };
}

export const dashboard = createDashboardStore();
```

- [ ] **Step 2: Commit**

```bash
git add yggdashboard/src/lib/ws-client.svelte.ts
git commit -m "yggdashboard: add frontend WebSocket store"
```

---

### Task 9: Dashboard UI

**Files:**
- Create: `yggdashboard/src/lib/components/NodeInfo.svelte`
- Create: `yggdashboard/src/lib/components/PeersTable.svelte`
- Create: `yggdashboard/src/lib/components/SessionsTable.svelte`
- Create: `yggdashboard/src/lib/components/GarlicPanel.svelte`
- Modify: `yggdashboard/src/routes/+page.svelte`

**Interfaces:**
- Consumes: `dashboard` store from Task 8; `SelfInfo`/`PeerEntry`/`SessionEntry`/`GarlicStats`/`GarlicKnownPeer` types from Task 4.
- Produces: the full Phase 1 dashboard page.

- [ ] **Step 1: Create `yggdashboard/src/lib/components/NodeInfo.svelte`**

```svelte
<script lang="ts">
  import type { SelfInfo } from '../../../server/types';
  let { self }: { self: SelfInfo } = $props();
</script>

<section>
  <h2>Node</h2>
  <dl>
    <dt>Build</dt>
    <dd>{self.build_name} {self.build_version}</dd>
    <dt>Public key</dt>
    <dd class="mono">{self.key}</dd>
    <dt>Address</dt>
    <dd class="mono">{self.address}</dd>
    <dt>Subnet</dt>
    <dd class="mono">{self.subnet}</dd>
    <dt>Routing entries</dt>
    <dd>{self.routing_entries}</dd>
  </dl>
</section>

<style>
  section {
    margin-bottom: 2rem;
  }
  dl {
    display: grid;
    grid-template-columns: max-content 1fr;
    gap: 0.25rem 1rem;
  }
  dt {
    font-weight: 600;
    color: #666;
  }
  .mono {
    font-family: ui-monospace, monospace;
  }
</style>
```

- [ ] **Step 2: Create `yggdashboard/src/lib/components/PeersTable.svelte`**

```svelte
<script lang="ts">
  import type { PeerEntry } from '../../../server/types';
  let { peers }: { peers: PeerEntry[] } = $props();

  function formatBytes(n?: number): string {
    if (n === undefined) return '-';
    if (n >= 1024 ** 3) return (n / 1024 ** 3).toFixed(1) + ' GB';
    if (n >= 1024 ** 2) return (n / 1024 ** 2).toFixed(1) + ' MB';
    if (n >= 1024) return (n / 1024).toFixed(1) + ' KB';
    return n + ' B';
  }

  function formatRate(n?: number): string {
    return n === undefined ? '-' : formatBytes(n) + '/s';
  }

  function formatLatency(ns?: number): string {
    return ns === undefined ? '-' : (ns / 1e6).toFixed(1) + ' ms';
  }
</script>

<section>
  <h2>Peers ({peers.length})</h2>
  <p class="note">Totals include this node's own traffic AND anything relayed through it - Yggdrasil doesn't separate the two at the link level.</p>
  <table>
    <thead>
      <tr>
        <th>Remote</th>
        <th>Key</th>
        <th>Up</th>
        <th>Dir</th>
        <th>RX</th>
        <th>TX</th>
        <th>RX rate</th>
        <th>TX rate</th>
        <th>Latency</th>
      </tr>
    </thead>
    <tbody>
      {#each peers as peer (peer.key + (peer.remote ?? ''))}
        <tr>
          <td>{peer.remote ?? '-'}</td>
          <td class="mono">{peer.key.slice(0, 16)}…</td>
          <td>{peer.up ? 'up' : 'down'}</td>
          <td>{peer.inbound ? 'in' : 'out'}</td>
          <td>{formatBytes(peer.bytes_recvd)}</td>
          <td>{formatBytes(peer.bytes_sent)}</td>
          <td>{formatRate(peer.rate_recvd)}</td>
          <td>{formatRate(peer.rate_sent)}</td>
          <td>{formatLatency(peer.latency)}</td>
        </tr>
      {/each}
    </tbody>
  </table>
</section>

<style>
  section {
    margin-bottom: 2rem;
  }
  .note {
    color: #888;
    font-size: 0.85rem;
  }
  table {
    width: 100%;
    border-collapse: collapse;
    font-size: 0.9rem;
  }
  th,
  td {
    text-align: left;
    padding: 0.35rem 0.6rem;
    border-bottom: 1px solid #333;
  }
  .mono {
    font-family: ui-monospace, monospace;
  }
</style>
```

- [ ] **Step 3: Create `yggdashboard/src/lib/components/SessionsTable.svelte`**

```svelte
<script lang="ts">
  import type { SessionEntry } from '../../../server/types';
  let { sessions }: { sessions: SessionEntry[] } = $props();

  function formatBytes(n: number): string {
    if (n >= 1024 ** 3) return (n / 1024 ** 3).toFixed(1) + ' GB';
    if (n >= 1024 ** 2) return (n / 1024 ** 2).toFixed(1) + ' MB';
    if (n >= 1024) return (n / 1024).toFixed(1) + ' KB';
    return n + ' B';
  }
</script>

<section>
  <h2>Sessions ({sessions.length})</h2>
  <p class="note">Only traffic where this node is itself one of the two endpoints - not what it relays for others.</p>
  <table>
    <thead>
      <tr>
        <th>Address</th>
        <th>Key</th>
        <th>RX</th>
        <th>TX</th>
        <th>Uptime</th>
      </tr>
    </thead>
    <tbody>
      {#each sessions as session (session.key)}
        <tr>
          <td class="mono">{session.address}</td>
          <td class="mono">{session.key.slice(0, 16)}…</td>
          <td>{formatBytes(session.bytes_recvd)}</td>
          <td>{formatBytes(session.bytes_sent)}</td>
          <td>{session.uptime.toFixed(0)}s</td>
        </tr>
      {/each}
    </tbody>
  </table>
</section>

<style>
  section {
    margin-bottom: 2rem;
  }
  .note {
    color: #888;
    font-size: 0.85rem;
  }
  table {
    width: 100%;
    border-collapse: collapse;
    font-size: 0.9rem;
  }
  th,
  td {
    text-align: left;
    padding: 0.35rem 0.6rem;
    border-bottom: 1px solid #333;
  }
  .mono {
    font-family: ui-monospace, monospace;
  }
</style>
```

- [ ] **Step 4: Create `yggdashboard/src/lib/components/GarlicPanel.svelte`**

```svelte
<script lang="ts">
  import type { GarlicStats, GarlicKnownPeer } from '../../../server/types';
  let { stats, knownPeers }: { stats: GarlicStats; knownPeers: GarlicKnownPeer[] } = $props();
</script>

<section>
  <h2>Garlic</h2>
  <p>
    Originated circuits: <strong>{stats.originatedCircuits}</strong> ·
    Relayed circuits: <strong>{stats.relayedCircuits}</strong>
  </p>
  <h3>Known Garlic peers ({knownPeers.length})</h3>
  <table>
    <thead>
      <tr>
        <th>Node key</th>
        <th>Garlic public key</th>
        <th>Last seen</th>
      </tr>
    </thead>
    <tbody>
      {#each knownPeers as peer (peer.nodeKey)}
        <tr>
          <td class="mono">{peer.nodeKey.slice(0, 16)}…</td>
          <td class="mono">{peer.garlicPublicKey.slice(0, 16)}…</td>
          <td>{new Date(peer.lastSeen).toLocaleString()}</td>
        </tr>
      {/each}
    </tbody>
  </table>
</section>

<style>
  section {
    margin-bottom: 2rem;
  }
  table {
    width: 100%;
    border-collapse: collapse;
    font-size: 0.9rem;
  }
  th,
  td {
    text-align: left;
    padding: 0.35rem 0.6rem;
    border-bottom: 1px solid #333;
  }
  .mono {
    font-family: ui-monospace, monospace;
  }
</style>
```

- [ ] **Step 5: Replace `yggdashboard/src/routes/+page.svelte`**

```svelte
<script lang="ts">
  import { dashboard } from '$lib/ws-client.svelte';
  import NodeInfo from '$lib/components/NodeInfo.svelte';
  import PeersTable from '$lib/components/PeersTable.svelte';
  import SessionsTable from '$lib/components/SessionsTable.svelte';
  import GarlicPanel from '$lib/components/GarlicPanel.svelte';
</script>

<main>
  <h1>yggdashboard</h1>
  {#if !dashboard.connected}
    <p class="status">Connecting…</p>
  {/if}
  {#if dashboard.snapshot}
    <NodeInfo self={dashboard.snapshot.self} />
    <PeersTable peers={dashboard.snapshot.peers} />
    <SessionsTable sessions={dashboard.snapshot.sessions} />
    <GarlicPanel stats={dashboard.snapshot.garlicStats} knownPeers={dashboard.snapshot.garlicKnownPeers} />
  {/if}
</main>

<style>
  main {
    font-family: system-ui, sans-serif;
    max-width: 1100px;
    margin: 0 auto;
    padding: 1.5rem;
  }
  .status {
    color: #888;
  }
</style>
```

- [ ] **Step 6: Manually verify the full dashboard against the real running local node**

```bash
cd yggdashboard
ADMIN_SOCKET=unix:///var/run/yggdrasil/yggdrasil.sock npm run dev -- --port 5173 &
sleep 3
```

Open `http://localhost:5173` in a browser. Confirm:
- The page shows "Connecting…" briefly, then the four sections render.
- Node section shows this machine's real build version, key, and address (matches `yggdrasilctl getself`).
- Peers section lists the peer(s) configured earlier in this project (the server peering set up in the real-network Garlic test).
- Garlic section shows non-zero `originatedCircuits`/`relayedCircuits` if a circuit was built earlier in this session, and lists known Garlic peers.
- Values update roughly every 2 seconds (watch RX/TX bytes tick up, or `Uptime` climb).

```bash
kill %1
```

- [ ] **Step 7: Commit**

```bash
git add yggdashboard/src/lib/components/NodeInfo.svelte yggdashboard/src/lib/components/PeersTable.svelte \
  yggdashboard/src/lib/components/SessionsTable.svelte yggdashboard/src/lib/components/GarlicPanel.svelte \
  yggdashboard/src/routes/+page.svelte
git commit -m "yggdashboard: build the Phase 1 dashboard UI (node, peers, sessions, garlic)"
```

---

### Task 10: README and final verification

**Files:**
- Create: `yggdashboard/README.md`

**Interfaces:**
- Consumes: nothing new - documents everything built in Tasks 1-9.
- Produces: operator-facing documentation.

- [ ] **Step 1: Create `yggdashboard/README.md`**

```markdown
# yggdashboard

A live dashboard for a running Yggdrasil/Garlic node: peers, this
node's own traffic, and Garlic circuit stats, updated roughly every 2
seconds over a WebSocket.

Phase 1 only - tables, no topology graph yet (see
`docs/superpowers/specs/2026-08-09-yggdashboard-design.md` for the
Phase 2 plan).

## Requirements

- Node 20 LTS or later.
- A running `yggdrasil` node on the same host, with its admin socket
  reachable by whoever runs this dashboard (same user, or a member of
  the `yggdrasil` group for the default unix socket path).

## Configuration

Environment variables, all optional:

| Variable | Default | Meaning |
|---|---|---|
| `ADMIN_SOCKET` | `unix:///var/run/yggdrasil/yggdrasil.sock` | Same `unix://path` or `tcp://host:port` format as Yggdrasil's own `AdminListen` config / `yggdrasilctl -endpoint`. |
| `DASHBOARD_HOST` | `127.0.0.1` | Dashboard's own HTTP/WS listen address. |
| `DASHBOARD_PORT` | `8787` | Dashboard's own listen port. |
| `POLL_INTERVAL_MS` | `2000` | How often the server polls the admin socket. |

## Running

Development (hot reload):

```sh
npm install
npm run dev
```

Production:

```sh
npm install
npm run build
npm start
```

Run the test suite:

```sh
npm test
```

## Access control

**There is no authentication in this dashboard**, matching the admin
socket it talks to (`yggdrasilctl` itself has none either - anyone who
can reach the socket is trusted). The listener binds to `127.0.0.1`
only by default. To view it from another machine, use an SSH tunnel
rather than changing `DASHBOARD_HOST`:

```sh
ssh -L 8787:127.0.0.1:8787 user@your-server
```

then open `http://localhost:8787` locally. This is a deliberate v1
scope limit, not an oversight.
```

- [ ] **Step 2: Run the full test suite one more time**

```bash
cd yggdashboard && npm test
```
Expected: all tests across `json-stream.test.ts`, `admin-client.test.ts`, `config.test.ts`, `poll.test.ts` pass.

- [ ] **Step 3: Confirm the Go side is untouched**

```bash
cd /home/alina/VsCodeProjects/yggdrasil-go
git status --porcelain=v1 -- src/ cmd/ go.mod go.sum
```
Expected: empty output - this plan never modifies Go code.

- [ ] **Step 4: Commit**

```bash
git add yggdashboard/README.md
git commit -m "yggdashboard: add README documenting configuration and access control"
```

---

## Self-review notes

- **Spec coverage:** architecture (Task 1, 7), admin-socket client with keepalive (Task 3), poll loop at 2s default (Task 4, 5), WebSocket broadcast from one shared poll (Task 6, 7), Phase 1 four sections with honest separate traffic numbers (Task 9), localhost-only/no-auth (Task 4 default, Task 10 documented), Vitest coverage of the admin-socket client plus the other pure-logic modules (Tasks 2-5), no Playwright (none added), Phase 2 graph excluded (not present anywhere in this plan). All covered.
- **Type consistency:** `AdminClient.request<T>()` return shape, `Poller` constructor signature, and `Snapshot`/`PeerEntry`/`SessionEntry`/`GarlicStats`/`GarlicKnownPeer` field names are identical everywhere they're used across Tasks 3, 5, 6, 7, 8, 9 - checked field-by-field against the Go source (`src/admin/getself.go`, `getpeers.go`, `getsessions.go`, `src/garlic/admin.go`) while writing `types.ts`.
- **Placeholder scan:** no TBD/TODO markers; every step has real, complete code.
