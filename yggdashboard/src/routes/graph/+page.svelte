<script lang="ts">
  import NetworkGraph from '$lib/components/NetworkGraph.svelte';
  import GraphLegend from '$lib/components/GraphLegend.svelte';
  import GraphDetail from '$lib/components/GraphDetail.svelte';
  import { createGraphResource } from '$lib/stores/dashboard.svelte';
  import type { GraphNode, GraphEdge } from '$lib/api-types';

  let { data } = $props();

  const graphResource = createGraphResource();
  $effect(() => {
    graphResource.start();
    return () => graphResource.stop();
  });

  let graph = $derived(graphResource.data ?? data.graph);

  function edgeKey(e: GraphEdge): string {
    return `${e.type}:${e.from}:${e.to}:${e.circuitId ?? ''}`;
  }

  let selectedNodeKey = $state<string | null>(null);
  let selectedEdgeKey = $state<string | null>(null);

  let selection = $derived.by(() => {
    if (selectedNodeKey) {
      const found = graph.nodes.find((n) => n.key === selectedNodeKey);
      return found ? { kind: 'node' as const, data: found } : null;
    }
    if (selectedEdgeKey) {
      const found = [...graph.yggdrasilEdges, ...graph.garlicEdges].find((e) => edgeKey(e) === selectedEdgeKey);
      return found ? { kind: 'edge' as const, data: found } : null;
    }
    return null;
  });

  function selectNode(n: GraphNode) {
    selectedNodeKey = n.key;
    selectedEdgeKey = null;
  }

  function selectEdge(e: GraphEdge) {
    selectedEdgeKey = edgeKey(e);
    selectedNodeKey = null;
  }

  function closeDetail() {
    selectedNodeKey = null;
    selectedEdgeKey = null;
  }
</script>

<svelte:head>
  <title>yggdashboard · graph</title>
</svelte:head>

<GraphLegend />
<div class="layout">
  <div class="graph-col">
    <NetworkGraph
      nodes={graph.nodes}
      yggdrasilEdges={graph.yggdrasilEdges}
      garlicEdges={graph.garlicEdges}
      onSelectNode={selectNode}
      onSelectEdge={selectEdge}
    />
  </div>
  {#if selection}
    <div class="detail-col">
      <GraphDetail {selection} onClose={closeDetail} />
    </div>
  {/if}
</div>

<style>
  .layout {
    display: grid;
    grid-template-columns: 1fr;
    gap: 1rem;
  }
  @media (min-width: 900px) {
    .layout {
      grid-template-columns: 1fr 320px;
    }
  }
</style>
