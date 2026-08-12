import { describe, it, expect } from 'vitest';
import { computeStats } from './stats';
import { EMPTY_SNAPSHOT, EMPTY_GARLIC } from './types';
import type { Snapshot } from './types';

function snapshotWithGarlicBytes(originated: number, relayed: number): Snapshot {
  return {
    ...EMPTY_SNAPSHOT,
    garlic: { ...EMPTY_GARLIC, enabled: true, stats: { ...EMPTY_GARLIC.stats, originatedBytes: originated, relayedBytes: relayed } }
  };
}

describe('computeStats', () => {
  it('reports transitPercent as exactly 0, not NaN, when no Garlic traffic has happened yet', () => {
    const stats = computeStats(snapshotWithGarlicBytes(0, 0));
    expect(stats.garlic.transitPercent).toBe(0);
  });

  it('computes transitPercent as relayed / (originated + relayed) * 100', () => {
    const stats = computeStats(snapshotWithGarlicBytes(300, 700));
    expect(stats.garlic.transitPercent).toBeCloseTo(70, 5);
  });

  it('sums peer bytes_recvd/bytes_sent for peer-link totals, separate from session totals', () => {
    const snap: Snapshot = {
      ...EMPTY_SNAPSHOT,
      peers: [
        { key: 'a', up: true, inbound: false, port: 1, priority: 0, cost: 1, bytes_recvd: 100, bytes_sent: 50 },
        { key: 'b', up: true, inbound: true, port: 1, priority: 0, cost: 1, bytes_recvd: 200, bytes_sent: 25 }
      ],
      sessions: [{ address: '200::1', key: 'a', bytes_recvd: 10, bytes_sent: 5, uptime: 1 }]
    };
    const stats = computeStats(snap);
    expect(stats.rxTotalPeerLink).toBe(300);
    expect(stats.txTotalPeerLink).toBe(75);
    expect(stats.rxTotalSessions).toBe(10);
    expect(stats.txTotalSessions).toBe(5);
  });
});
