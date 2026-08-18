import { describe, it, expect } from 'vitest';
import { computeGraph } from './graph';
import { EMPTY_SNAPSHOT } from './types';
import type { Snapshot } from './types';

describe('computeGraph', () => {
  it('returns no nodes or edges for a snapshot with nothing known', () => {
    const graph = computeGraph(EMPTY_SNAPSHOT);
    expect(graph.nodes).toEqual([{ key: '', address: '', isSelf: true }]); // self always included, even with empty fields
    expect(graph.yggdrasilEdges).toEqual([]);
    expect(graph.garlicEdges).toEqual([]);
  });

  it('builds a yggdrasil edge from each tree entry with a real parent', () => {
    const snap: Snapshot = {
      ...EMPTY_SNAPSHOT,
      self: { ...EMPTY_SNAPSHOT.self, key: 'root' },
      tree: [{ address: '200::2', key: 'child', parent: 'root', sequence: 1 }]
    };
    const graph = computeGraph(snap);
    expect(graph.yggdrasilEdges).toEqual([{ from: 'child', to: 'root', type: 'yggdrasil' }]);
  });

  it('builds a full originated-circuit chain from LOCAL through every hop', () => {
    const snap: Snapshot = {
      ...EMPTY_SNAPSHOT,
      self: { ...EMPTY_SNAPSHOT.self, key: 'local' },
      garlic: {
        ...EMPTY_SNAPSHOT.garlic,
        circuits: {
          originated: [{ circuitId: '1', hops: ['a', 'b'], closed: false, createdAt: '', expiresAt: '', packets: 0, bytes: 0 }],
          relayed: []
        }
      }
    };
    const graph = computeGraph(snap);
    expect(graph.garlicEdges).toEqual([
      { from: 'local', to: 'a', type: 'garlic', circuitId: '1', active: true },
      { from: 'a', to: 'b', type: 'garlic', circuitId: '1', active: true }
    ]);
  });

  it('builds only previous-hop and next-hop edges for a relayed circuit, never a fabricated full path', () => {
    const snap: Snapshot = {
      ...EMPTY_SNAPSHOT,
      self: { ...EMPTY_SNAPSHOT.self, key: 'local' },
      garlic: {
        ...EMPTY_SNAPSHOT.garlic,
        circuits: {
          originated: [],
          relayed: [{ circuitId: '2', previousHop: 'x', nextHop: 'y', firstSeen: '', lastActive: '', packetsRelayed: 0, bytesRelayed: 0 }]
        }
      }
    };
    const graph = computeGraph(snap);
    expect(graph.garlicEdges).toEqual([
      { from: 'x', to: 'local', type: 'garlic', circuitId: '2', active: true },
      { from: 'local', to: 'y', type: 'garlic', circuitId: '2', active: true }
    ]);
  });

  it('never returns an edge whose endpoint is missing from the node list, even for hops absent from the tree', () => {
    // A Garlic circuit can route through nodes this node has no direct
    // Yggdrasil peering with, so their keys never appear in getTree.
    // The graph must still carry a node for each of them, otherwise the
    // renderer draws an edge to a node that doesn't exist.
    const snap: Snapshot = {
      ...EMPTY_SNAPSHOT,
      self: { ...EMPTY_SNAPSHOT.self, key: 'local', address: '200::1' },
      // Only 'local' and 'known' exist at the Yggdrasil layer.
      tree: [{ address: '200::2', key: 'known', parent: 'local', sequence: 1 }],
      garlic: {
        ...EMPTY_SNAPSHOT.garlic,
        circuits: {
          originated: [
            { circuitId: '1', hops: ['known', 'unknown-mid', 'unknown-exit'], closed: false, createdAt: '', expiresAt: '', packets: 0, bytes: 0 }
          ],
          relayed: [
            { circuitId: '2', previousHop: 'unknown-prev', nextHop: 'unknown-next', firstSeen: '', lastActive: '', packetsRelayed: 0, bytesRelayed: 0 }
          ]
        }
      }
    };

    const graph = computeGraph(snap);
    const nodeKeys = new Set(graph.nodes.map((n) => n.key));

    expect(graph.garlicEdges.length).toBeGreaterThan(0);
    expect(graph.garlicEdges.every((e) => nodeKeys.has(e.from) && nodeKeys.has(e.to))).toBe(true);
    expect(graph.yggdrasilEdges.every((e) => nodeKeys.has(e.from) && nodeKeys.has(e.to))).toBe(true);

    // The synthesized nodes are present but carry no invented address,
    // and the tree-sourced ones keep theirs.
    expect(nodeKeys).toContain('unknown-mid');
    expect(nodeKeys).toContain('unknown-exit');
    expect(nodeKeys).toContain('unknown-prev');
    expect(nodeKeys).toContain('unknown-next');
    expect(graph.nodes.find((n) => n.key === 'unknown-mid')).toEqual({ key: 'unknown-mid', address: '', isSelf: false });
    expect(graph.nodes.find((n) => n.key === 'known')).toEqual({ key: 'known', address: '200::2', isSelf: false });
    // A node added from the tree is not duplicated by the garlic pass.
    expect(graph.nodes.filter((n) => n.key === 'known')).toHaveLength(1);
    expect(graph.nodes.filter((n) => n.key === 'local')).toHaveLength(1);
    expect(graph.nodes.find((n) => n.key === 'local')?.isSelf).toBe(true);
  });

  it('never includes a privateKey field, even if present on the snapshot', () => {
    // A hypothetical future admin field on a tree/circuit entry must not
    // be blindly passed through into the graph payload.
    const snap = {
      ...EMPTY_SNAPSHOT,
      self: { ...EMPTY_SNAPSHOT.self, key: 'local', privateKey: 'must-not-leak-from-self' },
      tree: [{ address: '200::2', key: 'known', parent: 'local', sequence: 1, privateKey: 'must-not-leak-from-tree' }],
      garlic: {
        ...EMPTY_SNAPSHOT.garlic,
        circuits: {
          originated: [
            { circuitId: '1', hops: ['h1'], closed: false, createdAt: '', expiresAt: '', packets: 0, bytes: 0, privateKey: 'must-not-leak-from-originated' }
          ],
          relayed: [
            { circuitId: '2', previousHop: 'p1', nextHop: 'n1', firstSeen: '', lastActive: '', packetsRelayed: 0, bytesRelayed: 0, privateKey: 'must-not-leak-from-relayed' }
          ]
        }
      }
    } as unknown as Snapshot;

    const graph = computeGraph(snap);
    expect(JSON.stringify(graph)).not.toContain('privateKey');
    expect(JSON.stringify(graph)).not.toContain('must-not-leak');
  });
});
