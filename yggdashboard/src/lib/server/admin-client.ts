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
  private connectingReject: ((err: Error) => void) | null = null;
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
      this.connectingReject = reject;
      const opts = parseAdminAddress(this.address);
      const socket = 'path' in opts ? net.createConnection({ path: opts.path }) : net.createConnection(opts);

      const onConnect = () => {
        if (this.connectingReject) {
          this.socket = socket;
          this.connectingReject = null;
          this.connecting = null;
          resolve(socket);
        }
      };
      const onError = (err: Error) => {
        if (this.connectingReject) {
          this.connectingReject = null;
          this.connecting = null;
          reject(err);
        }
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
    if (this.connectingReject) {
      const reject = this.connectingReject;
      this.connectingReject = null;
      this.connecting = null;
      reject(new Error('admin socket connection closed'));
    }
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
