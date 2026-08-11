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

interface QueuedSend {
  name: string;
  args: Record<string, unknown> | undefined;
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
 *
 * Every request is registered into sendQueue immediately and flushed
 * synchronously the instant a socket is usable - either right away (an
 * open socket already exists) or directly inside the 'connect' handler
 * (a fresh connection) - closing a race where an await between "socket
 * became ready" and "request is written and tracked" could silently
 * miss a close or an already-arrived response, permanently hanging the
 * caller.
 */
export class AdminClient {
  private address: string;
  private socket: net.Socket | null = null;
  private connecting = false;
  // activeSocket identifies which physical socket object, if any, is
  // allowed to affect this client's state right now - the socket
  // currently connecting, or the socket currently connected. Once a
  // socket is superseded (a newer attempt started), its event handlers
  // keep firing (Node doesn't guarantee synchronous listener removal)
  // but are guarded to no-op, so a stale socket's late 'close' can never
  // clobber a healthier, subsequent connection's state.
  private activeSocket: net.Socket | null = null;
  private buffer = '';
  private queue: PendingRequest[] = [];
  private sendQueue: QueuedSend[] = [];

  constructor(address: string) {
    this.address = address;
  }

  private ensureConnected(): void {
    if (this.socket || this.connecting) return;
    this.connecting = true;

    let socket: net.Socket;
    try {
      const opts = parseAdminAddress(this.address);
      socket = 'path' in opts ? net.createConnection({ path: opts.path }) : net.createConnection(opts);
    } catch (err) {
      // A synchronous throw here (e.g. an unparseable address) must not
      // leave `connecting` stuck true forever - that would silently hang
      // every request after the first.
      this.connecting = false;
      this.failSendQueue(err instanceof Error ? err : new Error(String(err)));
      return;
    }

    this.activeSocket = socket;

    socket.once('connect', () => {
      if (socket !== this.activeSocket) return;
      this.connecting = false;
      this.socket = socket;
      this.flushSendQueue();
    });
    // net.Socket's 'close' event always fires directly following 'error'
    // (whether or not the socket ever successfully connected) - so all
    // cleanup lives in the 'close' handler below, and this listener only
    // exists to stop an unhandled 'error' event from crashing the process.
    socket.once('error', () => {});
    socket.on('data', (chunk: Buffer) => {
      if (socket !== this.activeSocket) return;
      this.onData(chunk);
    });
    socket.on('close', () => {
      if (socket !== this.activeSocket) return;
      this.activeSocket = null;
      this.onClose();
    });
  }

  private flushSendQueue(): void {
    if (!this.socket) return;
    const socket = this.socket;
    const toSend = this.sendQueue.splice(0);
    for (const item of toSend) {
      const payload = { request: item.name, arguments: item.args ?? {}, keepalive: true };
      this.queue.push({ resolve: item.resolve, reject: item.reject });
      socket.write(JSON.stringify(payload) + '\n');
    }
  }

  private failSendQueue(err: Error): void {
    const pending = this.sendQueue.splice(0);
    for (const p of pending) {
      p.reject(err);
    }
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
    this.connecting = false;
    const pending = this.queue.splice(0);
    for (const p of pending) {
      p.reject(new Error('admin socket connection closed'));
    }
    this.failSendQueue(new Error('admin socket connection closed'));
  }

  request<T = unknown>(name: string, args?: Record<string, unknown>): Promise<T> {
    return new Promise<T>((resolve, reject) => {
      this.sendQueue.push({
        name,
        args,
        resolve: (result: AdminResponse) => {
          if (result.status !== 'success') {
            reject(new Error(result.error || `admin request '${name}' failed`));
            return;
          }
          resolve(result.response as T);
        },
        reject
      });
      this.ensureConnected();
      if (this.socket) this.flushSendQueue();
    });
  }

  close(): void {
    this.socket?.destroy();
    this.socket = null;
  }
}
