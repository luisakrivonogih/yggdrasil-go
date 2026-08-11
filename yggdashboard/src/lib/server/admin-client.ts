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
 * (a fresh connection). This closes a race where awaiting a Promise
 * between "socket became ready" and "request is written and tracked"
 * left a window for a close or an already-arrived response to be
 * silently missed, permanently hanging the caller.
 */
export class AdminClient {
  private address: string;
  private socket: net.Socket | null = null;
  private connecting = false;
  private buffer = '';
  private queue: PendingRequest[] = [];
  private sendQueue: QueuedSend[] = [];

  constructor(address: string) {
    this.address = address;
  }

  private ensureConnected(): void {
    if (this.socket || this.connecting) return;
    this.connecting = true;
    const opts = parseAdminAddress(this.address);
    const socket = 'path' in opts ? net.createConnection({ path: opts.path }) : net.createConnection(opts);

    socket.once('connect', () => {
      this.connecting = false;
      this.socket = socket;
      this.flushSendQueue();
    });
    socket.once('error', () => {
      this.connecting = false;
      this.failSendQueue(new Error('admin socket connection failed'));
    });
    socket.on('data', (chunk: Buffer) => this.onData(chunk));
    socket.on('close', () => this.onClose());
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
