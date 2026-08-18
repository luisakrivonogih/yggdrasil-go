import type { Snapshot } from './types';

export interface StatusPayload {
  status: 'online' | 'degraded' | 'disconnected';
  uptime: number;
  buildName: string;
  buildVersion: string;
  garlicEnabled: boolean;
  peerCount: number;
  peersUp: number;
  polledAt: string;
}

/**
 * Derives the top-level node status from what this dashboard process
 * can actually observe: Online = at least one peer up, Degraded =
 * admin socket reachable but zero peers up, Disconnected = the most
 * recent poll could not reach the admin socket at all (stale/cached
 * data is being served, if any). No invented health checks beyond
 * what's directly derivable from getSelf/getPeers.
 */
export function computeStatus(snap: Snapshot): StatusPayload {
  const peersUp = snap.peers.filter((p) => p.up).length;
  let status: StatusPayload['status'];
  if (!snap.adminReachable) {
    status = 'disconnected';
  } else if (peersUp === 0) {
    status = 'degraded';
  } else {
    status = 'online';
  }
  return {
    status,
    uptime: snap.self.uptime,
    buildName: snap.self.build_name,
    buildVersion: snap.self.build_version,
    garlicEnabled: snap.garlic.enabled,
    peerCount: snap.peers.length,
    peersUp,
    polledAt: snap.polledAt
  };
}
