<script lang="ts">
  import '$lib/styles/tokens.css';
  import NavBar from '$lib/components/NavBar.svelte';
  import StatusBadge from '$lib/components/StatusBadge.svelte';
  import { createStatusResource } from '$lib/stores/dashboard.svelte';
  import { formatUptime } from '$lib/format';

  let { data, children } = $props();

  const statusResource = createStatusResource();
  $effect(() => {
    statusResource.start();
    return () => statusResource.stop();
  });

  // SSR-rendered data from +layout.server.ts is the initial paint (no
  // JS required); once hydrated, the live-polled resource takes over -
  // falling back to the SSR value if a poll hasn't landed yet.
  let status = $derived(statusResource.data ?? data.status);
</script>

<div class="shell">
  <header>
    <div class="brand">YGGDRASIL / GARLIC</div>
    <StatusBadge status={status.status} />
    <span class="meta">uptime {formatUptime(status.uptime)}</span>
    <span class="meta">v{status.buildVersion}</span>
    <span class="meta">Garlic {status.garlicEnabled ? 'enabled' : 'disabled'}</span>
    <span class="meta conn" class:offline={!statusResource.connected}>
      {statusResource.connected ? 'connected' : 'reconnecting…'}
      {#if statusResource.latencyMs !== null}
        · {statusResource.latencyMs}ms
      {/if}
    </span>
  </header>
  <NavBar />
  <main>
    {@render children()}
  </main>
</div>

<style>
  .shell {
    max-width: 1200px;
    margin: 0 auto;
    padding: 1rem 1.5rem 3rem;
  }
  header {
    display: flex;
    align-items: center;
    gap: 1rem;
    flex-wrap: wrap;
    padding-bottom: 0.75rem;
    border-bottom: 1px solid var(--border);
    margin-bottom: 0.75rem;
  }
  .brand {
    font-weight: 700;
    letter-spacing: 0.04em;
    font-size: 0.9rem;
  }
  .meta {
    font-size: 0.8rem;
    color: var(--text-dim);
  }
  .conn.offline {
    color: var(--bad);
  }
  nav {
    margin-bottom: 1rem;
  }
</style>
