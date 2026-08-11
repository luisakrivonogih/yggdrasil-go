import type { Snapshot } from './types';

export function computePeers(snap: Snapshot) {
  const garlicKnownKeys = new Set(snap.garlic.knownPeers.map((p) => p.nodeKey));
  const peers = snap.peers.map((p) => ({
    key: p.key,
    remote: p.remote ?? null,
    address: p.address ?? null,
    up: p.up,
    inbound: p.inbound,
    bytesRecvd: p.bytes_recvd ?? 0,
    bytesSent: p.bytes_sent ?? 0,
    rateRecvd: p.rate_recvd ?? 0,
    rateSent: p.rate_sent ?? 0,
    uptime: p.uptime ?? 0,
    latencyNs: p.latency ?? null,
    lastError: p.last_error ?? null,
    garlicCapable: garlicKnownKeys.has(p.key)
  }));
  return { peers, polledAt: snap.polledAt };
}
