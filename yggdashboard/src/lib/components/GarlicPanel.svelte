<script lang="ts">
  import type { GarlicResponse } from '$lib/api-types';
  import CopyableKey from './CopyableKey.svelte';
  import MetricCard from './MetricCard.svelte';
  import SecurityCounters from './SecurityCounters.svelte';
  import { formatBytes, formatUptime } from '$lib/format';

  let { garlic }: { garlic: GarlicResponse } = $props();

  function ageSeconds(createdAt: string, now: number): number {
    return Math.max(0, (now - new Date(createdAt).getTime()) / 1000);
  }
</script>

<div class="grid">
  <MetricCard label="Garlic" value={garlic.enabled ? 'Enabled' : 'Disabled'} />
  <MetricCard label="Originated circuits" value={String(garlic.stats.originatedCircuits)} />
  <MetricCard label="Relayed circuits" value={String(garlic.stats.relayedCircuits)} />
  <MetricCard label="Known Garlic peers" value={String(garlic.knownPeers.length)} />
</div>

{#if garlic.enabled}
  <section class="identity">
    <h2>Identity</h2>
    {#if garlic.identity}
      <div class="row">
        <span class="label">Garlic public key</span>
        <CopyableKey value={garlic.identity.publicKey} prefixLen={16} suffixLen={8} />
      </div>
    {/if}
  </section>

  <div class="grid">
    <MetricCard label="Originated" value={formatBytes(garlic.stats.originatedBytes)} sublabel={`${garlic.stats.originatedPackets} packets`} />
    <MetricCard label="Relayed" value={formatBytes(garlic.stats.relayedBytes)} sublabel={`${garlic.stats.relayedPackets} packets`} />
  </div>

  <SecurityCounters counters={garlic.stats.security} />

  <section class="auto-pool">
    <h2>Auto-built circuit pool ({garlic.autoPool.length})</h2>
    {#if garlic.autoPool.length === 0}
      <p class="empty">No auto-built circuits yet.</p>
    {:else}
      <table>
        <thead>
          <tr>
            <th>Circuit</th>
            <th>Hops</th>
            <th>Age</th>
          </tr>
        </thead>
        <tbody>
          {#each garlic.autoPool as c (c.circuitId)}
            <tr>
              <td><CopyableKey value={c.circuitId} prefixLen={6} suffixLen={4} /></td>
              <td>{c.hops}</td>
              <td>{formatUptime(ageSeconds(c.createdAt, Date.now()))}</td>
            </tr>
          {/each}
        </tbody>
      </table>
    {/if}
  </section>

  <section class="known-peers">
    <h2>Known Garlic peers ({garlic.knownPeers.length})</h2>
    {#if garlic.knownPeers.length === 0}
      <p class="empty">None known yet.</p>
    {:else}
      <table>
        <thead>
          <tr>
            <th>Node key</th>
            <th>Garlic public key</th>
            <th>Last seen</th>
            <th>Verified</th>
          </tr>
        </thead>
        <tbody>
          {#each garlic.knownPeers as p (p.nodeKey)}
            <tr>
              <td><CopyableKey value={p.nodeKey} /></td>
              <td><CopyableKey value={p.garlicPublicKey} /></td>
              <td>{new Date(p.lastSeen).toLocaleString()}</td>
              <td>
                <span class="verify-badge" class:verified={p.selfVerified}>
                  {p.selfVerified ? 'Self-verified' : 'Gossiped'}
                </span>
              </td>
            </tr>
          {/each}
        </tbody>
      </table>
    {/if}
  </section>
{:else}
  <p class="disabled-note">Garlic is disabled on this node. Enable it in the node's config (Garlic.Enabled) to see identity, circuit, and security data here.</p>
{/if}

<style>
  .grid {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(160px, 1fr));
    gap: 0.75rem;
    margin-bottom: 1rem;
  }
  .identity,
  .auto-pool,
  .known-peers {
    background: var(--bg-raised);
    border: 1px solid var(--border);
    border-radius: var(--radius);
    padding: 0.75rem 1rem;
    margin-bottom: 1rem;
  }
  h2 {
    font-size: 0.9rem;
    text-transform: uppercase;
    letter-spacing: 0.04em;
    color: var(--text-dim);
    margin: 0 0 0.5rem;
  }
  .row {
    display: flex;
    align-items: center;
    gap: 0.5rem;
  }
  .label {
    color: var(--text-dim);
    font-size: 0.85rem;
  }
  table {
    width: 100%;
    border-collapse: collapse;
    font-size: 0.85rem;
  }
  th,
  td {
    text-align: left;
    padding: 0.35rem 0.6rem;
    border-bottom: 1px solid var(--border);
  }
  .empty,
  .disabled-note {
    color: var(--text-dim);
    font-size: 0.85rem;
  }
  .verify-badge {
    font-size: 0.75rem;
    font-weight: 600;
    color: var(--text-dim);
  }
  .verify-badge.verified {
    color: var(--ok);
  }
</style>
