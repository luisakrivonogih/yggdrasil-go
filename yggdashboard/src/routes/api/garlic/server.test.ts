import { describe, it, expect, vi } from 'vitest';

vi.mock('$lib/server/instance', () => ({
  poller: {
    waitUntilReady: vi.fn().mockResolvedValue(undefined),
    getSnapshot: vi.fn(() => ({
      garlic: {
        enabled: true,
        identity: { publicKey: 'garlic-pub', privateKey: 'must-not-leak' },
        stats: {
          originatedCircuits: 1,
          relayedCircuits: 0,
          originatedPackets: 1,
          originatedBytes: 100,
          relayedPackets: 0,
          relayedBytes: 0,
          security: { replayDrops: 0, malformedPackets: 0, expiredPackets: 0, authFailures: 0, relayTableFull: 0 },
          // Simulates a hypothetical future admin field on getGarlicStats
          // that this builder must not blindly pass through.
          privateKey: 'must-not-leak-from-stats'
        },
        circuits: { originated: [], relayed: [] },
        knownPeers: [
          {
            nodeKey: 'a',
            garlicPublicKey: 'b',
            lastSeen: '2026-08-10T00:00:00.000Z',
            selfVerified: true,
            // Simulates a hypothetical future admin field on
            // getGarlicKnownPeers entries that must not leak either.
            privateKey: 'must-not-leak-from-knownpeers'
          }
        ],
        autoPool: [{ circuitId: 'c1', createdAt: '2026-08-10T00:00:00.000Z', hops: 3 }]
      }
    }))
  }
}));

const { GET } = await import('./+server');

describe('GET /api/garlic', () => {
  it('never includes a privateKey field', async () => {
    const response = await GET({} as never);
    const body = await response.json();
    expect(JSON.stringify(body)).not.toContain('privateKey');
    expect(body.identity.publicKey).toBe('garlic-pub');
  });
});
