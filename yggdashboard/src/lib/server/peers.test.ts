import { describe, it, expect } from 'vitest';
import { computePeers } from './peers';
import { EMPTY_SNAPSHOT } from './types';
import type { Snapshot } from './types';

describe('computePeers', () => {
  it('returns an empty peer list unchanged', () => {
    expect(computePeers(EMPTY_SNAPSHOT).peers).toEqual([]);
  });

  it('marks a peer garlicCapable when its key appears in garlic.knownPeers', () => {
    const snap: Snapshot = {
      ...EMPTY_SNAPSHOT,
      peers: [
        { key: 'aaa', up: true, inbound: false, port: 1, priority: 0, cost: 1 },
        { key: 'bbb', up: true, inbound: false, port: 1, priority: 0, cost: 1 }
      ],
      garlic: { ...EMPTY_SNAPSHOT.garlic, knownPeers: [{ nodeKey: 'aaa', garlicPublicKey: 'gp', lastSeen: '2026-01-01T00:00:00Z', selfVerified: true }] }
    };
    const { peers } = computePeers(snap);
    expect(peers.find((p) => p.key === 'aaa')?.garlicCapable).toBe(true);
    expect(peers.find((p) => p.key === 'bbb')?.garlicCapable).toBe(false);
  });

  it('defaults optional numeric fields to 0 and optional string fields to null', () => {
    const snap: Snapshot = { ...EMPTY_SNAPSHOT, peers: [{ key: 'a', up: false, inbound: false, port: 1, priority: 0, cost: 1 }] };
    const [peer] = computePeers(snap).peers;
    expect(peer.bytesRecvd).toBe(0);
    expect(peer.rateSent).toBe(0);
    expect(peer.remote).toBeNull();
    expect(peer.latencyNs).toBeNull();
  });
});
