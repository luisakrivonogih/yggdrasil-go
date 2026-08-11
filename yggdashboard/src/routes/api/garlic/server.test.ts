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
          security: { replayDrops: 0, malformedPackets: 0, expiredPackets: 0, authFailures: 0, relayTableFull: 0 }
        },
        circuits: { originated: [], relayed: [] },
        knownPeers: []
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
