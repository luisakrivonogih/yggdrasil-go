import { describe, it, expect, vi } from 'vitest';

vi.mock('$lib/server/instance', () => ({
  poller: {
    waitUntilReady: vi.fn().mockResolvedValue(undefined),
    getSnapshot: vi.fn(() => ({
      garlic: {
        enabled: true,
        circuits: {
          originated: [
            {
              circuitId: 'c1',
              hops: ['h1', 'h2'],
              closed: false,
              createdAt: '2026-08-10T00:00:00.000Z',
              expiresAt: '2026-08-10T01:00:00.000Z',
              packets: 5,
              bytes: 500,
              // Simulates a hypothetical future admin field on
              // getGarlicCircuits' originated entries that must not leak.
              privateKey: 'must-not-leak-from-originated'
            }
          ],
          relayed: [
            {
              circuitId: 'c2',
              previousHop: 'p1',
              nextHop: 'n1',
              firstSeen: '2026-08-10T00:00:00.000Z',
              lastActive: '2026-08-10T00:05:00.000Z',
              packetsRelayed: 3,
              bytesRelayed: 300,
              // Same, for relayed entries.
              privateKey: 'must-not-leak-from-relayed'
            }
          ]
        }
      },
      polledAt: '2026-08-10T00:00:00.000Z'
    }))
  }
}));

const { GET } = await import('./+server');

describe('GET /api/circuits', () => {
  it('never includes a privateKey field', async () => {
    const response = await GET({} as never);
    const body = await response.json();
    expect(JSON.stringify(body)).not.toContain('privateKey');
    expect(body.originated[0].circuitId).toBe('c1');
    expect(body.relayed[0].circuitId).toBe('c2');
  });
});
