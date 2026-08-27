<script lang="ts">
  import { forceSimulation, forceLink, forceManyBody, forceCenter, forceCollide, type Simulation } from 'd3-force';
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

  // The rendered box is responsive (bind:clientWidth below); the
  // simulation always runs in this same coordinate space, so the
  // boundary clamp in the tick handler and the viewBox always agree -
  // that mismatch (fixed 800x480 physics vs. a CSS box of a different
  // aspect ratio) was why nodes used to drift outside the visible area.
  let containerWidth = $state(800);
  let width = $derived(Math.max(360, containerWidth));
  let height = $derived(Math.max(320, Math.min(560, Math.round(width * 0.56))));
  const nodePadding = 28;

  type SimNode = GraphNode & { x?: number; y?: number; vx?: number; vy?: number; fx?: number | null; fy?: number | null };
  let simNodes = $state<SimNode[]>([]);

  // Positions survive across polls (keyed by node key) so a re-fetch
  // doesn't teleport the whole layout back to scratch every couple of
  // seconds - only genuinely new nodes get a fresh starting spot.
  const lastKnownPos = new Map<string, { x: number; y: number }>();

  let sim: Simulation<SimNode, undefined> | undefined;
  let draggingKey: string | null = $state(null);
  let hoveredKey: string | null = $state(null);

  let svgEl: SVGSVGElement | undefined = $state();
  let worldEl: SVGGElement | undefined = $state();

  let zoom = $state(1);
  let panX = $state(0);
  let panY = $state(0);
  let panning = $state(false);
  let panPointerId: number | null = null;
  let panStart = { x: 0, y: 0 };

  $effect(() => {
    sim?.stop();

    const nodeCopies: SimNode[] = nodes.map((n) => {
      const prev = lastKnownPos.get(n.key);
      return { ...n, x: prev?.x, y: prev?.y };
    });
    const keys = new Set(nodeCopies.map((n) => n.key));
    const linkData = yggdrasilEdges
      .filter((e) => keys.has(e.from) && keys.has(e.to))
      .map((e) => ({ source: e.from, target: e.to }));

    if (nodeCopies.length === 0) {
      simNodes = [];
      return;
    }

    function clampToBounds() {
      for (const n of nodeCopies) {
        if (n.x === undefined || n.y === undefined) continue;
        n.x = Math.min(width - nodePadding, Math.max(nodePadding, n.x));
        n.y = Math.min(height - nodePadding, Math.max(nodePadding, n.y));
      }
    }

    function onTick() {
      clampToBounds();
      for (const n of nodeCopies) {
        if (n.x !== undefined && n.y !== undefined) lastKnownPos.set(n.key, { x: n.x, y: n.y });
      }
      simNodes = nodeCopies;
    }

    sim = forceSimulation(nodeCopies)
      .force(
        'link',
        forceLink(linkData)
          .id((d: unknown) => (d as SimNode).key)
          .distance(70)
      )
      .force('charge', forceManyBody().strength(-140))
      .force('center', forceCenter(width / 2, height / 2).strength(0.06))
      .force('collide', forceCollide(24))
      .alpha(0.6)
      .on('tick', onTick);

    // The simulation's own internal timer (driven by requestAnimationFrame)
    // animates it live from here on - that's the whole fix for the graph
    // never visibly settling. This first tick just paints an initial,
    // already-spread-out frame synchronously so there's no flash of every
    // node stacked at dead center (or, in a test/SSR-ish render, no frame
    // at all if the timer hasn't fired yet).
    sim.tick();
    onTick();

    return () => sim?.stop();
  });

  function nodePos(key: string): { x: number; y: number } {
    const n = simNodes.find((node) => node.key === key);
    return { x: n?.x ?? width / 2, y: n?.y ?? height / 2 };
  }

  function screenToWorld(clientX: number, clientY: number): { x: number; y: number } {
    if (!svgEl || !worldEl || !svgEl.createSVGPoint) return { x: clientX, y: clientY };
    const ctm = worldEl.getScreenCTM();
    if (!ctm) return { x: clientX, y: clientY };
    const pt = svgEl.createSVGPoint();
    pt.x = clientX;
    pt.y = clientY;
    const world = pt.matrixTransform(ctm.inverse());
    return { x: world.x, y: world.y };
  }

  function startDrag(node: SimNode, event: PointerEvent) {
    event.stopPropagation();
    (event.currentTarget as Element).setPointerCapture(event.pointerId);
    draggingKey = node.key;
    sim?.alphaTarget(0.3).restart();
    dragTo(node, event);
  }

  function dragMove(node: SimNode, event: PointerEvent) {
    if (draggingKey !== node.key) return;
    dragTo(node, event);
  }

  function dragTo(node: SimNode, event: PointerEvent) {
    const world = screenToWorld(event.clientX, event.clientY);
    node.fx = world.x;
    node.fy = world.y;
    node.x = world.x;
    node.y = world.y;
    simNodes = simNodes;
  }

  function endDrag(node: SimNode, event: PointerEvent) {
    if (draggingKey !== node.key) return;
    draggingKey = null;
    node.fx = null;
    node.fy = null;
    sim?.alphaTarget(0);
  }

  function onWheel(event: WheelEvent) {
    event.preventDefault();
    const factor = event.deltaY < 0 ? 1.15 : 1 / 1.15;
    zoom = Math.min(4, Math.max(0.4, zoom * factor));
  }

  function onBackgroundPointerDown(event: PointerEvent) {
    if (event.button !== 0) return;
    panning = true;
    panPointerId = event.pointerId;
    panStart = { x: event.clientX - panX, y: event.clientY - panY };
    (event.currentTarget as Element).setPointerCapture(event.pointerId);
  }

  function onBackgroundPointerMove(event: PointerEvent) {
    if (!panning || event.pointerId !== panPointerId) return;
    panX = event.clientX - panStart.x;
    panY = event.clientY - panStart.y;
  }

  function onBackgroundPointerUp(event: PointerEvent) {
    if (event.pointerId !== panPointerId) return;
    panning = false;
    panPointerId = null;
  }

  function resetView() {
    zoom = 1;
    panX = 0;
    panY = 0;
  }

  function connects(edge: { from: string; to: string }, key: string): boolean {
    return edge.from === key || edge.to === key;
  }
</script>

<div class="graph" bind:clientWidth={containerWidth}>
  {#if nodes.length === 0}
    <p class="empty">No known nodes yet.</p>
  {:else}
    {#if zoom !== 1 || panX !== 0 || panY !== 0}
      <button type="button" class="reset-view" onclick={resetView}>Reset view</button>
    {/if}
    <svg
      bind:this={svgEl}
      viewBox="0 0 {width} {height}"
      style="height: {height}px"
      role="img"
      aria-label="Network topology"
      onwheel={onWheel}
      onpointerdown={onBackgroundPointerDown}
      onpointermove={onBackgroundPointerMove}
      onpointerup={onBackgroundPointerUp}
      onpointerleave={onBackgroundPointerUp}
      class:panning
    >
      <g bind:this={worldEl} transform="translate({panX} {panY}) scale({zoom})">
        {#each yggdrasilEdges as edge, i (edge.from + edge.to + i)}
          {@const from = nodePos(edge.from)}
          {@const to = nodePos(edge.to)}
          {@const dim = hoveredKey !== null && !connects(edge, hoveredKey)}
          <line
            x1={from.x}
            y1={from.y}
            x2={to.x}
            y2={to.y}
            class="edge yggdrasil"
            class:dim
            onclick={() => onSelectEdge(edge)}
          />
        {/each}
        {#each garlicEdges as edge, i (edge.from + edge.to + (edge.circuitId ?? '') + i)}
          {@const from = nodePos(edge.from)}
          {@const to = nodePos(edge.to)}
          {@const dim = hoveredKey !== null && !connects(edge, hoveredKey)}
          <line
            x1={from.x}
            y1={from.y}
            x2={to.x}
            y2={to.y}
            class="edge garlic"
            class:active={edge.active}
            class:dim
            onclick={() => onSelectEdge(edge)}
          />
        {/each}
        {#each simNodes as node (node.key)}
          {@const dim = hoveredKey !== null && hoveredKey !== node.key}
          <g
            class="node"
            class:dim
            onclick={() => onSelectNode(node)}
            onpointerdown={(e: PointerEvent) => startDrag(node, e)}
            onpointermove={(e: PointerEvent) => dragMove(node, e)}
            onpointerup={(e: PointerEvent) => endDrag(node, e)}
            onpointerenter={() => (hoveredKey = node.key)}
            onpointerleave={() => (hoveredKey = null)}
          >
            <circle cx={node.x} cy={node.y} r={node.isSelf ? 10 : 6} class:self={node.isSelf} />
            <text x={node.x} y={(node.y ?? 0) - 12} text-anchor="middle">{node.isSelf ? 'LOCAL' : truncateKey(node.key, 4, 0)}</text>
          </g>
        {/each}
      </g>
    </svg>
  {/if}
</div>

<style>
  .graph {
    position: relative;
    background:
      radial-gradient(ellipse at 50% 40%, rgba(88, 166, 255, 0.07), transparent 65%),
      var(--bg-raised);
    border: 1px solid var(--border);
    border-radius: var(--radius);
    padding: 0.5rem;
  }
  svg {
    width: 100%;
    display: block;
    touch-action: none;
    cursor: grab;
  }
  svg.panning {
    cursor: grabbing;
  }
  .empty {
    color: var(--text-dim);
    font-size: 0.85rem;
    padding: 1rem;
  }
  .reset-view {
    position: absolute;
    top: 0.75rem;
    right: 0.75rem;
    z-index: 1;
    background: var(--bg);
    border: 1px solid var(--border);
    border-radius: 4px;
    color: var(--text-dim);
    font-size: 0.75rem;
    padding: 0.3rem 0.6rem;
    cursor: pointer;
  }
  .reset-view:hover {
    color: var(--text);
    border-color: var(--accent);
  }
  .edge {
    stroke: var(--text-dim);
    stroke-width: 1.5;
    stroke-linecap: round;
    cursor: pointer;
    opacity: 0.85;
    transition:
      opacity 150ms ease,
      stroke-width 150ms ease;
  }
  .edge.dim {
    opacity: 0.15;
  }
  .edge.garlic {
    stroke: var(--warn);
    stroke-dasharray: 4 3;
  }
  .edge.garlic.active {
    stroke: var(--bad);
    stroke-width: 2.5;
    stroke-dasharray: 5 4;
    opacity: 1;
    animation: flow 0.9s linear infinite;
  }
  @keyframes flow {
    to {
      stroke-dashoffset: -18;
    }
  }
  .node {
    transition: opacity 150ms ease;
  }
  .node.dim {
    opacity: 0.35;
  }
  .node circle {
    fill: var(--accent);
    stroke: var(--bg-raised);
    stroke-width: 2;
    cursor: grab;
    transition: r 120ms ease;
  }
  .node circle.self {
    fill: var(--ok);
    filter: drop-shadow(0 0 6px var(--ok));
  }
  .node:hover circle {
    r: 8;
  }
  .node text {
    fill: var(--text-dim);
    font-size: 9px;
    font-family: var(--mono);
    paint-order: stroke;
    stroke: var(--bg-raised);
    stroke-width: 3px;
    pointer-events: none;
  }
</style>
