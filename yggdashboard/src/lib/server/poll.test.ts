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

  it('does not let a later poll mutate an already-returned snapshot\'s history array', async () => {
    const client = fakeClient({ ...CORE_RESPONSES, ...GARLIC_RESPONSES });
    const poller = new Poller(client, 2000, 300000);
    poller.start();
    await vi.advanceTimersByTimeAsync(0);

    const firstHistory = poller.getSnapshot().history;
    expect(firstHistory.length).toBe(1);

    await vi.advanceTimersByTimeAsync(2000); // second poll

    expect(poller.getSnapshot().history.length).toBe(2);
    // The array reference an earlier caller already holds must be
    // untouched by the later poll - same length, not the same reference
    // as the new history array.
    expect(firstHistory.length).toBe(1);
    expect(poller.getSnapshot().history).not.toBe(firstHistory);
    poller.stop();
  });

  it('discards an in-flight tick that completes after stop() was called', async () => {
    let releaseGate: (() => void) | null = null;
    const gate = new Promise<void>((resolve) => {
      releaseGate = resolve;
    });
    const client = {
      request: vi.fn(async (name: string) => {
        if (name === 'getSelf') await gate; // block this tick mid-flight
        if (name in GARLIC_RESPONSES) return (GARLIC_RESPONSES as Record<string, unknown>)[name];
        return (CORE_RESPONSES as Record<string, unknown>)[name];
      })
    } as unknown as AdminClient;

    const poller = new Poller(client, 2000, 300000);
    poller.start();
    await vi.advanceTimersByTimeAsync(0);

    // The tick is still blocked on the gate - nothing has landed yet.
    expect(poller.getSnapshot().ready).toBe(false);

    poller.stop();
    releaseGate!();
    for (let i = 0; i < 5; i++) {
      await vi.advanceTimersByTimeAsync(0);
    }

    // The tick that was in flight when stop() was called must not
    // overwrite the snapshot after the fact, even though it eventually
    // completed.
    const snap = poller.getSnapshot();
    expect(snap.ready).toBe(false);
    expect(snap.self.build_name).toBe('');
  });
});
