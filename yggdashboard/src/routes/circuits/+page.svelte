<script lang="ts">
  import CircuitTable from '$lib/components/CircuitTable.svelte';
  import CircuitDetail from '$lib/components/CircuitDetail.svelte';
  import { createCircuitsResource } from '$lib/stores/dashboard.svelte';
  import type { OriginatedCircuit, RelayedCircuit } from '$lib/api-types';

  let { data } = $props();

  const circuitsResource = createCircuitsResource();
  $effect(() => {
    circuitsResource.start();
    return () => circuitsResource.stop();
  });

  let circuits = $derived(circuitsResource.data ?? data.circuits);

  let selectedKind = $state<'originated' | 'relayed' | null>(null);
  let selectedCircuitId = $state<string | null>(null);

  let selected = $derived.by(() => {
    if (!selectedKind || !selectedCircuitId) return null;
    if (selectedKind === 'originated') {
      const found = circuits.originated.find((c) => c.circuitId === selectedCircuitId);
      return found ? { kind: 'originated' as const, data: found } : null;
    }
    const found = circuits.relayed.find((c) => c.circuitId === selectedCircuitId);
    return found ? { kind: 'relayed' as const, data: found } : null;
  });

  function selectOriginated(c: OriginatedCircuit) {
    selectedKind = 'originated';
    selectedCircuitId = c.circuitId;
  }

  function selectRelayed(c: RelayedCircuit) {
    selectedKind = 'relayed';
    selectedCircuitId = c.circuitId;
  }

  function closeDetail() {
    selectedKind = null;
    selectedCircuitId = null;
  }
</script>

<svelte:head>
  <title>yggdashboard · circuits</title>
</svelte:head>

{#if !circuits.enabled}
  <p class="disabled-note">Garlic is disabled on this node - no circuits to show.</p>
{:else}
  <div class="layout">
    <div class="table-col">
      <CircuitTable
        originated={circuits.originated}
        relayed={circuits.relayed}
        onSelectOriginated={selectOriginated}
        onSelectRelayed={selectRelayed}
      />
    </div>
    {#if selected}
      <div class="detail-col">
        <CircuitDetail circuit={selected} onClose={closeDetail} />
      </div>
    {/if}
  </div>
{/if}

<style>
  .disabled-note {
    color: var(--text-dim);
  }
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
