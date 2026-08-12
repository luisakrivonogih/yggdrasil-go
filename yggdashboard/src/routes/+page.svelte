<script lang="ts">
  import MetricCard from '$lib/components/MetricCard.svelte';
  import NodeIdentity from '$lib/components/NodeIdentity.svelte';
  import TrafficChart from '$lib/components/TrafficChart.svelte';
  import { createStatsResource } from '$lib/stores/dashboard.svelte';
  import { formatRate, formatPercent } from '$lib/format';

  let { data } = $props();

  const statsResource = createStatsResource();
  $effect(() => {
    statsResource.start();
    return () => statsResource.stop();
  });

  let stats = $derived(statsResource.data ?? data.stats);
</script>

<svelte:head>
  <title>yggdashboard</title>
</svelte:head>

<section class="grid">
  <MetricCard label="Peers" value={`${data.peersUp} / ${data.peerCount}`} sublabel="up / known" />
  <MetricCard label="Download" value={formatRate(stats.rxRate)} sublabel="peer-link aggregate" />
  <MetricCard label="Upload" value={formatRate(stats.txRate)} sublabel="peer-link aggregate" />
  <MetricCard
    label="Garlic transit"
    value={stats.garlic.enabled ? formatPercent(stats.garlic.transitPercent) : 'disabled'}
    sublabel="share of Garlic traffic relayed for others"
  />
</section>

<NodeIdentity buildName={data.self.buildName} buildVersion={data.self.buildVersion} address={data.self.address} publicKey={data.self.key} />

<h2 class="section-title">Traffic</h2>
<TrafficChart history={stats.history} />

{#if stats.garlic.enabled}
  <section class="garlic-summary">
    <MetricCard label="Garlic originated" value={formatRate(stats.garlic.originatedRate)} />
    <MetricCard label="Garlic relayed" value={formatRate(stats.garlic.relayedRate)} />
  </section>
{/if}

<style>
  .grid {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(160px, 1fr));
    gap: 0.75rem;
    margin-bottom: 1rem;
  }
  .garlic-summary {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(160px, 1fr));
    gap: 0.75rem;
    margin-top: 1rem;
  }
  .section-title {
    font-size: 0.9rem;
    text-transform: uppercase;
    letter-spacing: 0.04em;
    color: var(--text-dim);
    margin: 1.5rem 0 0.5rem;
  }
</style>
