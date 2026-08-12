<script lang="ts" module>
  export function ageSeconds(createdAt: string, now: number): number {
    return Math.max(0, (now - new Date(createdAt).getTime()) / 1000);
  }

  export function remainingSeconds(expiresAt: string, now: number): number {
    return Math.max(0, (new Date(expiresAt).getTime() - now) / 1000);
  }
</script>

<script lang="ts">
  import type { OriginatedCircuit, RelayedCircuit } from '$lib/api-types';
  import { formatBytes, formatUptime, truncateKey } from '$lib/format';

  let {
    originated,
    relayed,
    onSelectOriginated,
    onSelectRelayed
  }: {
    originated: OriginatedCircuit[];
    relayed: RelayedCircuit[];
    onSelectOriginated: (c: OriginatedCircuit) => void;
    onSelectRelayed: (c: RelayedCircuit) => void;
  } = $props();

  const now = Date.now();
</script>

<section>
  <h2>Originated ({originated.length})</h2>
  <p class="note">Circuits this node built - the full hop chain is shown because this node chose it and already knows it.</p>
  {#if originated.length === 0}
    <p class="empty">No originated circuits.</p>
  {:else}
    <table>
      <thead>
        <tr>
          <th>Circuit</th>
          <th>Path</th>
          <th>State</th>
          <th>Age</th>
          <th>Remaining</th>
          <th>Packets</th>
          <th>Bytes</th>
        </tr>
      </thead>
      <tbody>
        {#each originated as c (c.circuitId)}
          <tr onclick={() => onSelectOriginated(c)}>
            <td class="mono">{truncateKey(c.circuitId, 6, 4)}</td>
            <td class="mono">LOCAL → {c.hops.map((h) => truncateKey(h, 4, 2)).join(' → ')}</td>
            <td class:up={!c.closed} class:down={c.closed}>{c.closed ? 'closed' : 'active'}</td>
            <td>{formatUptime(ageSeconds(c.createdAt, now))}</td>
            <td>{formatUptime(remainingSeconds(c.expiresAt, now))}</td>
            <td>{c.packets}</td>
            <td>{formatBytes(c.bytes)}</td>
          </tr>
        {/each}
      </tbody>
    </table>
  {/if}
</section>

<section>
  <h2>Relayed ({relayed.length})</h2>
  <p class="note">Circuits this node relays for others - only the immediate previous/next hop is ever shown, because that's all a relay actually knows.</p>
  {#if relayed.length === 0}
    <p class="empty">No relayed circuits.</p>
  {:else}
    <table>
      <thead>
        <tr>
          <th>Circuit</th>
          <th>Path</th>
          <th>First seen</th>
          <th>Last active</th>
          <th>Packets</th>
          <th>Bytes</th>
        </tr>
      </thead>
      <tbody>
        {#each relayed as c (c.circuitId)}
          <tr onclick={() => onSelectRelayed(c)}>
            <td class="mono">{truncateKey(c.circuitId, 6, 4)}</td>
            <td class="mono">{truncateKey(c.previousHop, 4, 2)} → LOCAL → {truncateKey(c.nextHop, 4, 2)}</td>
            <td>{formatUptime(ageSeconds(c.firstSeen, now))} ago</td>
            <td>{formatUptime(ageSeconds(c.lastActive, now))} ago</td>
            <td>{c.packetsRelayed}</td>
            <td>{formatBytes(c.bytesRelayed)}</td>
          </tr>
        {/each}
      </tbody>
    </table>
  {/if}
</section>

<style>
  section {
    background: var(--bg-raised);
    border: 1px solid var(--border);
    border-radius: var(--radius);
    padding: 0.75rem;
    margin-bottom: 1rem;
  }
  h2 {
    font-size: 0.9rem;
    text-transform: uppercase;
    letter-spacing: 0.04em;
    color: var(--text-dim);
    margin: 0 0 0.25rem;
  }
  .note {
    font-size: 0.75rem;
    color: var(--text-dim);
    margin: 0 0 0.5rem;
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
    color: var(--text-dim);
  }
  .empty {
    color: var(--text-dim);
    font-size: 0.85rem;
  }
  @media (max-width: 640px) {
    section {
      overflow-x: auto;
    }
  }
</style>
