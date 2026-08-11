import type { Snapshot } from './types';

export function computeStats(snap: Snapshot) {
  const rxTotal = snap.peers.reduce((sum, p) => sum + (p.bytes_recvd ?? 0), 0);
  const txTotal = snap.peers.reduce((sum, p) => sum + (p.bytes_sent ?? 0), 0);
  const sessionRx = snap.sessions.reduce((sum, s) => sum + s.bytes_recvd, 0);
  const sessionTx = snap.sessions.reduce((sum, s) => sum + s.bytes_sent, 0);
  const latest = snap.history.at(-1);
  const totalGarlicBytes = snap.garlic.stats.originatedBytes + snap.garlic.stats.relayedBytes;

  return {
    rxRate: latest?.rxRate ?? 0,
    txRate: latest?.txRate ?? 0,
    rxTotalPeerLink: rxTotal,
    txTotalPeerLink: txTotal,
    rxTotalSessions: sessionRx,
    txTotalSessions: sessionTx,
    garlic: {
      enabled: snap.garlic.enabled,
      originatedBytes: snap.garlic.stats.originatedBytes,
      relayedBytes: snap.garlic.stats.relayedBytes,
      originatedRate: latest?.garlicOriginatedRate ?? 0,
      relayedRate: latest?.garlicRelayedRate ?? 0,
      // "Share of Garlic circuit traffic relayed for others" - never
      // presented as an all-Yggdrasil-traffic figure. See the design
      // spec's "Metrics" section for why a global transit % isn't
      // implementable.
      transitPercent: totalGarlicBytes > 0 ? (snap.garlic.stats.relayedBytes / totalGarlicBytes) * 100 : 0
    },
    history: snap.history,
    polledAt: snap.polledAt
  };
}
