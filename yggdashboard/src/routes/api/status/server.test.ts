import { describe, it, expect, vi } from 'vitest';

vi.mock('$lib/server/instance', () => ({
  poller: {
    waitUntilReady: vi.fn().mockResolvedValue(undefined),
    getSnapshot: vi.fn(() => ({
      self: {
        build_name: 'yggdrasil',
        build_version: '0.5.14',
        key: 'abc123',
        address: '200::1',
        subnet: '300::/64',
        routing_entries: 3,
        uptime: 120,
        // A field that must never leak, simulating a hypothetical
        // future admin field this route must not blindly pass through.
        privateKey: 'should-never-appear'
      },
      peers: [{ up: true }, { up: false }, { up: true }],
      garlic: { enabled: true },
      ready: true,
      adminReachable: true,
      polledAt: '2026-08-10T00:00:00.000Z'
    }))
  }
}));

const { GET } = await import('./+server');

describe('GET /api/status', () => {
  it('never includes a privateKey field, even if present on the snapshot', async () => {
    const response = await GET({} as never);
    const body = await response.json();
    expect(JSON.stringify(body)).not.toContain('privateKey');
  });

  it('reports status Online with at least one up peer', async () => {
    const response = await GET({} as never);
    const body = await response.json();
    expect(body.status).toBe('online');
    expect(body.peerCount).toBe(3);
    expect(body.peersUp).toBe(2);
  });
});
