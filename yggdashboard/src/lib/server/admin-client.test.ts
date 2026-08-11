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
