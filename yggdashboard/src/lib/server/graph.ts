import type { Snapshot } from './types';

export function computeGraph(snap: Snapshot) {
  // Yggdrasil connectivity layer: real edges from getTree (key -> parent).
  const yggdrasilEdges = snap.tree
    .filter((entry) => entry.parent !== '' && entry.parent !== entry.key)
    .map((entry) => ({ from: entry.key, to: entry.parent, type: 'yggdrasil' as const }));

  const nodes = new Map<string, { key: string; address: string; isSelf: boolean }>();
  for (const entry of snap.tree) {
    nodes.set(entry.key, { key: entry.key, address: entry.address, isSelf: entry.key === snap.self.key });
  }
  nodes.set(snap.self.key, { key: snap.self.key, address: snap.self.address, isSelf: true });

  // Garlic circuit layer: originator's own chosen hop chain, and each
  // relayed circuit's real previous/next hop only - never a fabricated
  // full path for circuits this node only relays.
  const garlicEdges: Array<{ from: string; to: string; type: 'garlic'; circuitId: string; active: boolean }> = [];
  for (const c of snap.garlic.circuits.originated) {
    const chain = [snap.self.key, ...c.hops];
    for (let i = 0; i < chain.length - 1; i++) {
      garlicEdges.push({ from: chain[i], to: chain[i + 1], type: 'garlic', circuitId: c.circuitId, active: !c.closed });
    }
  }
  for (const r of snap.garlic.circuits.relayed) {
    garlicEdges.push({ from: r.previousHop, to: snap.self.key, type: 'garlic', circuitId: r.circuitId, active: true });
    garlicEdges.push({ from: snap.self.key, to: r.nextHop, type: 'garlic', circuitId: r.circuitId, active: true });
  }

  // A Garlic circuit can reference a hop this node has no direct
  // Yggdrasil peering with (e.g. a multi-hop originated circuit's
  // middle/exit hop) - add any edge endpoint not already known from the
  // tree, so no edge is ever drawn to a node that doesn't exist in the
  // returned node list.
  for (const e of garlicEdges) {
    if (!nodes.has(e.from)) nodes.set(e.from, { key: e.from, address: '', isSelf: e.from === snap.self.key });
    if (!nodes.has(e.to)) nodes.set(e.to, { key: e.to, address: '', isSelf: e.to === snap.self.key });
  }

  return {
    nodes: Array.from(nodes.values()),
    yggdrasilEdges,
    garlicEdges,
    polledAt: snap.polledAt
  };
}
