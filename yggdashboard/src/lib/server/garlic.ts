import type { Snapshot } from './types';

export function computeGarlic(snap: Snapshot) {
  return {
    enabled: snap.garlic.enabled,
    identity: snap.garlic.identity ? { publicKey: snap.garlic.identity.publicKey } : null,
    stats: snap.garlic.stats,
    knownPeers: snap.garlic.knownPeers,
    polledAt: snap.polledAt
  };
}
