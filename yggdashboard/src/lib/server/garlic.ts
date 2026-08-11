import type { Snapshot } from './types';

export function computeGarlic(snap: Snapshot) {
  return {
    enabled: snap.garlic.enabled,
    identity: snap.garlic.identity ? { publicKey: snap.garlic.identity.publicKey } : null,
    stats: {
      originatedCircuits: snap.garlic.stats.originatedCircuits,
      relayedCircuits: snap.garlic.stats.relayedCircuits,
      originatedPackets: snap.garlic.stats.originatedPackets,
      originatedBytes: snap.garlic.stats.originatedBytes,
      relayedPackets: snap.garlic.stats.relayedPackets,
      relayedBytes: snap.garlic.stats.relayedBytes,
      security: {
        replayDrops: snap.garlic.stats.security.replayDrops,
        malformedPackets: snap.garlic.stats.security.malformedPackets,
        expiredPackets: snap.garlic.stats.security.expiredPackets,
        authFailures: snap.garlic.stats.security.authFailures,
        relayTableFull: snap.garlic.stats.security.relayTableFull
      }
    },
    knownPeers: snap.garlic.knownPeers.map((p) => ({
      nodeKey: p.nodeKey,
      garlicPublicKey: p.garlicPublicKey,
      lastSeen: p.lastSeen
    })),
    polledAt: snap.polledAt
  };
}
