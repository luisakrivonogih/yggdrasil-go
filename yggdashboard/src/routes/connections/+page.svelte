<script lang="ts">
  import PeerTable from '$lib/components/PeerTable.svelte';
  import PeerDetail from '$lib/components/PeerDetail.svelte';
  import { createPeersResource } from '$lib/stores/dashboard.svelte';
  import type { ApiPeer } from '$lib/api-types';

  let { data } = $props();

  const peersResource = createPeersResource();
  $effect(() => {
    peersResource.start();
    return () => peersResource.stop();
  });

  let peers = $derived(peersResource.data?.peers ?? data.peers.peers);
  let selected = $state<ApiPeer | null>(null);
</script>

<svelte:head>
  <title>yggdashboard · connections</title>
</svelte:head>

<div class="layout">
  <div class="table-col">
    <PeerTable {peers} onSelect={(p) => (selected = p)} />
  </div>
  {#if selected}
    <div class="detail-col">
      <PeerDetail peer={selected} onClose={() => (selected = null)} />
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
