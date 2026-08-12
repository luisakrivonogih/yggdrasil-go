<script lang="ts">
  import type { GarlicSecurityCounters } from '$lib/api-types';

  let { counters }: { counters: GarlicSecurityCounters } = $props();

  const rows: Array<{ key: keyof GarlicSecurityCounters; label: string }> = [
    { key: 'replayDrops', label: 'Replay drops' },
    { key: 'authFailures', label: 'Auth failures' },
    { key: 'malformedPackets', label: 'Malformed packets' },
    { key: 'expiredPackets', label: 'Expired packets' },
    { key: 'relayTableFull', label: 'Relay table full' }
  ];
</script>

<section class="security">
  <h2>Security</h2>
  <dl>
    {#each rows as row (row.key)}
      <dt>{row.label}</dt>
      <dd>{counters[row.key]}</dd>
    {/each}
  </dl>
  <p class="note">Cumulative since this node last started. Local-only - never sent over the wire, and no field here reveals *which specific packet* failed, only the count in each category.</p>
</section>

<style>
  .security {
    background: var(--bg-raised);
    border: 1px solid var(--border);
    border-radius: var(--radius);
    padding: 0.75rem 1rem;
  }
  h2 {
    font-size: 0.9rem;
    text-transform: uppercase;
    letter-spacing: 0.04em;
    color: var(--text-dim);
    margin: 0 0 0.5rem;
  }
  dl {
    display: grid;
    grid-template-columns: max-content 1fr;
    gap: 0.25rem 1rem;
    margin: 0;
    font-family: var(--mono);
    font-size: 0.9rem;
  }
  dt {
    font-family: var(--sans);
    color: var(--text-dim);
    font-size: 0.85rem;
  }
  .note {
    font-family: var(--sans);
    font-size: 0.75rem;
    color: var(--text-dim);
    margin: 0.75rem 0 0;
  }
</style>
