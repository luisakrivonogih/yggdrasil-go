import { describe, it, expect } from 'vitest';
import { computeStatus } from './status';
import { EMPTY_SNAPSHOT } from './types';
import type { Snapshot, PeerEntry } from './types';

function peer(up: boolean): PeerEntry {
  return { key: 'peer', up, inbound: false, port: 1, priority: 0, cost: 1 };
}

describe('computeStatus', () => {
  it('reports disconnected when the last poll could not reach the admin socket, even with stale data present', () => {
    // The distinguishing case: the poller HAS polled successfully at
    // some point (ready), so the snapshot still carries real peers -
    // but the most recent tick could not reach the admin socket, so
    // what's being served is stale. That must read as disconnected, not
    // online/degraded off the back of cached data.
    const snap: Snapshot = {
      ...EMPTY_SNAPSHOT,
      peers: [peer(true), peer(true)],
      ready: true,
      adminReachable: false
    };
    const status = computeStatus(snap);
    expect(status.status).toBe('disconnected');
    expect(status.peersUp).toBe(2); // the stale counts are still reported, just not treated as health
  });

  it('reports online when the admin socket is reachable and at least one peer is up', () => {
    const snap: Snapshot = {
      ...EMPTY_SNAPSHOT,
      peers: [peer(true), peer(false)],
      ready: true,
      adminReachable: true
    };
    const status = computeStatus(snap);
    expect(status.status).toBe('online');
    expect(status.peerCount).toBe(2);
    expect(status.peersUp).toBe(1);
  });

  it('reports degraded when the admin socket is reachable but no peer is up', () => {
    const snap: Snapshot = {
      ...EMPTY_SNAPSHOT,
      peers: [peer(false), peer(false)],
      ready: true,
      adminReachable: true
    };
    const status = computeStatus(snap);
    expect(status.status).toBe('degraded');
    expect(status.peersUp).toBe(0);
  });

  it('reports disconnected before the first poll has completed at all', () => {
    expect(computeStatus(EMPTY_SNAPSHOT).status).toBe('disconnected');
  });
});
