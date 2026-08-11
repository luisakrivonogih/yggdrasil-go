import type { Snapshot } from './types';

export function computeCircuits(snap: Snapshot) {
  return {
    enabled: snap.garlic.enabled,
    originated: snap.garlic.circuits.originated,
    relayed: snap.garlic.circuits.relayed,
    polledAt: snap.polledAt
  };
}
