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
});
