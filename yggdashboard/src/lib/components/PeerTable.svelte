<script lang="ts" module>
  import type { ApiPeer } from '$lib/api-types';

  export type PeerSortKey = 'key' | 'uptime' | 'rateRecvd' | 'rateSent';

  /** Pure filter+sort logic, exported for direct unit testing. */
  export function filterAndSortPeers(peers: ApiPeer[], filter: string, sortKey: PeerSortKey, sortDir: 1 | -1): ApiPeer[] {
    const q = filter.trim().toLowerCase();
    const filtered = q
      ? peers.filter(
          (p) => p.key.toLowerCase().includes(q) || (p.remote ?? '').toLowerCase().includes(q) || (p.address ?? '').toLowerCase().includes(q)
        )
      : peers;
    return [...filtered].sort((a, b) => {
      const av = a[sortKey];
      const bv = b[sortKey];
      if (av < bv) return -sortDir;
      if (av > bv) return sortDir;
      return 0;
    });
  }
</script>

<script lang="ts">
  import { formatRate, formatLatency, formatUptime, truncateKey } from '$lib/format';

  let { peers, onSelect }: { peers: ApiPeer[]; onSelect: (peer: ApiPeer) => void } = $props();

  let filter = $state('');
  let sortKey = $state<PeerSortKey>('uptime');
  let sortDir = $state<1 | -1>(-1);

  let rows = $derived(filterAndSortPeers(peers, filter, sortKey, sortDir));

  function setSort(key: PeerSortKey) {
    if (sortKey === key) {
      sortDir = sortDir === 1 ? -1 : 1;
    } else {
      sortKey = key;
      sortDir = -1;
    }
  }
</script>

<div class="table-wrap">
  <input type="search" placeholder="Filter by key, remote, or address…" bind:value={filter} />
  {#if peers.length === 0}
    <p class="empty">No peers connected.</p>
  {:else if rows.length === 0}
    <p class="empty">No peers match this filter.</p>
  {:else}
    <table>
      <thead>
        <tr>
          <th>Peer</th>
          <th>Transport</th>
          <th>State</th>
          <th><button type="button" onclick={() => setSort('uptime')}>Uptime</button></th>
          <th>Latency</th>
          <th><button type="button" onclick={() => setSort('rateRecvd')}>RX</button></th>
          <th><button type="button" onclick={() => setSort('rateSent')}>TX</button></th>
          <th>Garlic</th>
        </tr>
      </thead>
      <tbody>
        {#each rows as peer (peer.key + (peer.remote ?? ''))}
          <tr onclick={() => onSelect(peer)}>
            <td class="mono">{truncateKey(peer.key)}</td>
            <td>{peer.remote ?? '—'}</td>
            <td class:up={peer.up} class:down={!peer.up}>{peer.up ? 'up' : 'down'} · {peer.inbound ? 'in' : 'out'}</td>
            <td>{formatUptime(peer.uptime)}</td>
            <td>{formatLatency(peer.latencyNs)}</td>
            <td>{formatRate(peer.rateRecvd)}</td>
            <td>{formatRate(peer.rateSent)}</td>
            <td>{peer.garlicCapable ? '✓' : '—'}</td>
          </tr>
        {/each}
      </tbody>
    </table>
  {/if}
</div>

<style>
  .table-wrap {
    background: var(--bg-raised);
    border: 1px solid var(--border);
    border-radius: var(--radius);
    padding: 0.75rem;
  }
  input[type='search'] {
    width: 100%;
    background: var(--bg);
    border: 1px solid var(--border);
    border-radius: 4px;
    color: var(--text);
    padding: 0.4rem 0.6rem;
    margin-bottom: 0.6rem;
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
    white-space: nowrap;
  }
  th button {
    background: none;
    border: none;
    color: var(--text-dim);
    font: inherit;
    cursor: pointer;
    padding: 0;
  }
  tbody tr {
    cursor: pointer;
  }
  tbody tr:hover {
    background: rgba(255, 255, 255, 0.03);
  }
  .mono {
    font-family: var(--mono);
  }
  .up {
    color: var(--ok);
  }
  .down {
    color: var(--bad);
  }
  .empty {
    color: var(--text-dim);
    font-size: 0.85rem;
    padding: 0.5rem 0;
  }
  @media (max-width: 640px) {
    .table-wrap {
      overflow-x: auto;
    }
  }
</style>
