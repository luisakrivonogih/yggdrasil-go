<script lang="ts">
  import { forceSimulation, forceLink, forceManyBody, forceCenter, forceCollide } from 'd3-force';
  import type { GraphNode, GraphEdge } from '$lib/api-types';
  import { truncateKey } from '$lib/format';

  let {
    nodes,
    yggdrasilEdges,
    garlicEdges,
    onSelectNode,
    onSelectEdge
  }: {
    nodes: GraphNode[];
    yggdrasilEdges: GraphEdge[];
    garlicEdges: GraphEdge[];
    onSelectNode: (n: GraphNode) => void;
    onSelectEdge: (e: GraphEdge) => void;
  } = $props();

  const width = 800;
  const height = 480;

  type SimNode = GraphNode & { x?: number; y?: number };
  let simNodes = $state<SimNode[]>([]);

  $effect(() => {
    const nodeCopies: SimNode[] = nodes.map((n) => ({ ...n }));
    const keys = new Set(nodeCopies.map((n) => n.key));
    const linkData = yggdrasilEdges
      .filter((e) => keys.has(e.from) && keys.has(e.to))
      .map((e) => ({ source: e.from, target: e.to }));

    if (nodeCopies.length === 0) {
      simNodes = [];
      return;
    }

    const sim = forceSimulation(nodeCopies as never[])
      .force(
        'link',
        forceLink(linkData)
          .id((d: unknown) => (d as SimNode).key)
          .distance(70)
      )
      .force('charge', forceManyBody().strength(-120))
      .force('center', forceCenter(width / 2, height / 2))
      .force('collide', forceCollide(24))
      .stop();

    for (let i = 0; i < 200; i++) sim.tick();
    simNodes = nodeCopies;

    return () => sim.stop();
  });

  function nodePos(key: string): { x: number; y: number } {
    const n = simNodes.find((node) => node.key === key);
    return { x: n?.x ?? width / 2, y: n?.y ?? height / 2 };
  }
</script>

<div class="graph">
  {#if nodes.length === 0}
    <p class="empty">No known nodes yet.</p>
  {:else}
    <svg viewBox="0 0 {width} {height}" role="img" aria-label="Network topology">
      {#each yggdrasilEdges as edge, i (edge.from + edge.to + i)}
        {@const from = nodePos(edge.from)}
        {@const to = nodePos(edge.to)}
        <line x1={from.x} y1={from.y} x2={to.x} y2={to.y} class="edge yggdrasil" onclick={() => onSelectEdge(edge)} />
      {/each}
      {#each garlicEdges as edge, i (edge.from + edge.to + (edge.circuitId ?? '') + i)}
        {@const from = nodePos(edge.from)}
        {@const to = nodePos(edge.to)}
        <line x1={from.x} y1={from.y} x2={to.x} y2={to.y} class="edge garlic" class:active={edge.active} onclick={() => onSelectEdge(edge)} />
      {/each}
      {#each simNodes as node (node.key)}
        <g class="node" onclick={() => onSelectNode(node)}>
          <circle cx={node.x} cy={node.y} r={node.isSelf ? 10 : 6} class:self={node.isSelf} />
          <text x={node.x} y={(node.y ?? 0) - 12} text-anchor="middle">{node.isSelf ? 'LOCAL' : truncateKey(node.key, 4, 0)}</text>
        </g>
      {/each}
    </svg>
  {/if}
</div>

<style>
  .graph {
    background: var(--bg-raised);
    border: 1px solid var(--border);
    border-radius: var(--radius);
    padding: 0.5rem;
  }
  svg {
    width: 100%;
    height: 480px;
    display: block;
  }
  .empty {
    color: var(--text-dim);
    font-size: 0.85rem;
    padding: 1rem;
  }
  .edge {
    stroke: var(--text-dim);
    stroke-width: 1.5;
    cursor: pointer;
  }
  .edge.garlic {
    stroke: var(--warn);
    stroke-dasharray: 4 3;
  }
  .edge.garlic.active {
    stroke: var(--bad);
    stroke-width: 2.5;
    stroke-dasharray: none;
  }
  .node circle {
    fill: var(--accent);
    cursor: pointer;
  }
  .node circle.self {
    fill: var(--ok);
  }
  .node text {
    fill: var(--text-dim);
    font-size: 9px;
    font-family: var(--mono);
    pointer-events: none;
  }
</style>
