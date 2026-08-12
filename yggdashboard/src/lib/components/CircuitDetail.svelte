<script lang="ts">
  import type { OriginatedCircuit, RelayedCircuit } from '$lib/api-types';
  import CopyableKey from './CopyableKey.svelte';
  import { formatBytes } from '$lib/format';

  let {
    circuit,
    onClose
  }: { circuit: { kind: 'originated'; data: OriginatedCircuit } | { kind: 'relayed'; data: RelayedCircuit }; onClose: () => void } = $props();
</script>

<aside class="detail">
  <button type="button" class="close" onclick={onClose} aria-label="Close">×</button>
  <h2>Circuit detail</h2>
  <dl>
    <dt>Circuit ID</dt>
    <dd><CopyableKey value={circuit.data.circuitId} prefixLen={8} suffixLen={4} /></dd>
    <dt>Role</dt>
    <dd>{circuit.kind === 'originated' ? 'Originator' : 'Relay'}</dd>
    {#if circuit.kind === 'originated'}
      <dt>Hops</dt>
      <dd class="mono">
        LOCAL
        {#each circuit.data.hops as hop (hop)}
          → <CopyableKey value={hop} prefixLen={6} suffixLen={2} />
        {/each}
      </dd>
      <dt>State</dt>
      <dd>{circuit.data.closed ? 'Closed' : 'Active'}</dd>
      <dt>Created</dt>
      <dd>{new Date(circuit.data.createdAt).toLocaleString()}</dd>
      <dt>Expires</dt>
      <dd>{new Date(circuit.data.expiresAt).toLocaleString()}</dd>
      <dt>Packets / Bytes</dt>
      <dd>{circuit.data.packets} / {formatBytes(circuit.data.bytes)}</dd>
    {:else}
      <dt>Previous hop</dt>
      <dd><CopyableKey value={circuit.data.previousHop} prefixLen={8} suffixLen={4} /></dd>
      <dt>Next hop</dt>
      <dd><CopyableKey value={circuit.data.nextHop} prefixLen={8} suffixLen={4} /></dd>
      <dt>First seen</dt>
      <dd>{new Date(circuit.data.firstSeen).toLocaleString()}</dd>
      <dt>Last active</dt>
      <dd>{new Date(circuit.data.lastActive).toLocaleString()}</dd>
      <dt>Packets / Bytes relayed</dt>
      <dd>{circuit.data.packetsRelayed} / {formatBytes(circuit.data.bytesRelayed)}</dd>
      <dt class="note-row">
        This node only ever knows its own two neighbors on this circuit - not the full path.
      </dt>
    {/if}
  </dl>
</aside>

<style>
  .detail {
    background: var(--bg-raised);
    border: 1px solid var(--border);
    border-radius: var(--radius);
    padding: 1rem;
    position: relative;
  }
  .close {
    position: absolute;
    top: 0.5rem;
    right: 0.5rem;
    background: none;
    border: none;
    color: var(--text-dim);
    font-size: 1.2rem;
    cursor: pointer;
  }
  h2 {
    font-size: 0.9rem;
    text-transform: uppercase;
    letter-spacing: 0.04em;
    color: var(--text-dim);
    margin: 0 0 0.75rem;
  }
  dl {
    display: grid;
    grid-template-columns: max-content 1fr;
    gap: 0.35rem 1rem;
    margin: 0;
  }
  dt {
    color: var(--text-dim);
    font-size: 0.8rem;
  }
  .mono {
    font-family: var(--mono);
  }
  .note-row {
    grid-column: 1 / -1;
    font-size: 0.75rem;
    color: var(--text-dim);
    margin-top: 0.4rem;
  }
</style>
