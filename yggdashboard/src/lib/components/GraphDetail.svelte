<script lang="ts">
  import type { GraphNode, GraphEdge } from '$lib/api-types';
  import CopyableKey from './CopyableKey.svelte';

  let {
    selection,
    onClose
  }: { selection: { kind: 'node'; data: GraphNode } | { kind: 'edge'; data: GraphEdge }; onClose: () => void } = $props();
</script>

<aside class="detail">
  <button type="button" class="close" onclick={onClose} aria-label="Close">×</button>
  {#if selection.kind === 'node'}
    <h2>Node</h2>
    <dl>
      <dt>Public key</dt>
      <dd><CopyableKey value={selection.data.key} prefixLen={12} suffixLen={6} /></dd>
      <dt>Address</dt>
      <dd class="mono">{selection.data.address}</dd>
      <dt>Role</dt>
      <dd>{selection.data.isSelf ? 'This node' : 'Peer in the mesh'}</dd>
    </dl>
  {:else}
    <h2>Edge</h2>
    <dl>
      <dt>Type</dt>
      <dd>{selection.data.type === 'garlic' ? 'Garlic circuit' : 'Yggdrasil connection'}</dd>
      <dt>From</dt>
      <dd><CopyableKey value={selection.data.from} prefixLen={12} suffixLen={6} /></dd>
      <dt>To</dt>
      <dd><CopyableKey value={selection.data.to} prefixLen={12} suffixLen={6} /></dd>
      {#if selection.data.type === 'garlic'}
        <dt>Circuit</dt>
        <dd class="mono">{selection.data.circuitId}</dd>
        <dt>Active relay</dt>
        <dd>{selection.data.active ? 'Yes' : 'No'}</dd>
      {/if}
    </dl>
  {/if}
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
</style>
