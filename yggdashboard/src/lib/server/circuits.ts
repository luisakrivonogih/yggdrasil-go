import type { Snapshot } from './types';

export function computeCircuits(snap: Snapshot) {
  return {
    enabled: snap.garlic.enabled,
    originated: snap.garlic.circuits.originated.map((c) => ({
      circuitId: c.circuitId,
      hops: c.hops,
      closed: c.closed,
      createdAt: c.createdAt,
      expiresAt: c.expiresAt,
      packets: c.packets,
      bytes: c.bytes
    })),
    relayed: snap.garlic.circuits.relayed.map((r) => ({
      circuitId: r.circuitId,
      previousHop: r.previousHop,
      nextHop: r.nextHop,
      firstSeen: r.firstSeen,
      lastActive: r.lastActive,
      packetsRelayed: r.packetsRelayed,
      bytesRelayed: r.bytesRelayed
    })),
    polledAt: snap.polledAt
  };
}
