import { describe, it, expect } from 'vitest';
import { filterAndSortPeers } from './PeerTable.svelte';
import type { ApiPeer } from '$lib/api-types';

function peer(overrides: Partial<ApiPeer>): ApiPeer {
  return {
    key: 'key',
    remote: null,
    address: null,
    up: true,
    inbound: false,
    bytesRecvd: 0,
    bytesSent: 0,
    rateRecvd: 0,
    rateSent: 0,
    uptime: 0,
    latencyNs: null,
    lastError: null,
    garlicCapable: false,
    ...overrides
  };
}

describe('filterAndSortPeers', () => {
  it('filters by substring match on key, remote, or address', () => {
    const peers = [peer({ key: 'abc' }), peer({ key: 'xyz', remote: 'tls://abc.example' }), peer({ key: 'zzz', address: '200::abc' })];
    expect(filterAndSortPeers(peers, 'abc', 'uptime', -1)).toHaveLength(3);
    expect(filterAndSortPeers(peers, 'nomatch', 'uptime', -1)).toHaveLength(0);
  });

  it('sorts by uptime descending by default', () => {
    const peers = [peer({ key: 'a', uptime: 10 }), peer({ key: 'b', uptime: 100 }), peer({ key: 'c', uptime: 50 })];
    const sorted = filterAndSortPeers(peers, '', 'uptime', -1);
    expect(sorted.map((p) => p.key)).toEqual(['b', 'c', 'a']);
  });

  it('sorts ascending when direction is 1', () => {
    const peers = [peer({ key: 'a', rateRecvd: 30 }), peer({ key: 'b', rateRecvd: 10 })];
    const sorted = filterAndSortPeers(peers, '', 'rateRecvd', 1);
    expect(sorted.map((p) => p.key)).toEqual(['b', 'a']);
  });

  it('returns an empty array for an empty peer list', () => {
    expect(filterAndSortPeers([], '', 'uptime', -1)).toEqual([]);
  });
});
