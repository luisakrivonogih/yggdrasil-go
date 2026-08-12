<script lang="ts">
  import type { ApiPeer } from '$lib/api-types';
  import CopyableKey from './CopyableKey.svelte';
  import { formatBytes, formatRate, formatLatency, formatUptime } from '$lib/format';

  let { peer, onClose }: { peer: ApiPeer; onClose: () => void } = $props();
</script>

<aside class="detail">
  <button type="button" class="close" onclick={onClose} aria-label="Close">×</button>
  <h2>Peer detail</h2>
  <dl>
    <dt>Public key</dt>
    <dd><CopyableKey value={peer.key} prefixLen={16} suffixLen={8} /></dd>
    <dt>Transport</dt>
    <dd>{peer.remote ?? '—'}</dd>
    <dt>Address</dt>
    <dd class="mono">{peer.address ?? '—'}</dd>
    <dt>State</dt>
    <dd>{peer.up ? 'up' : 'down'} · {peer.inbound ? 'inbound' : 'outbound'}</dd>
    <dt>Uptime</dt>
    <dd>{formatUptime(peer.uptime)}</dd>
    <dt>Latency</dt>
    <dd>{formatLatency(peer.latencyNs)}</dd>
    <dt>RX rate / total</dt>
    <dd>{formatRate(peer.rateRecvd)} / {formatBytes(peer.bytesRecvd)}</dd>
    <dt>TX rate / total</dt>
    <dd>{formatRate(peer.rateSent)} / {formatBytes(peer.bytesSent)}</dd>
    <dt>Garlic capable</dt>
    <dd>{peer.garlicCapable ? 'Yes' : 'No'}</dd>
    {#if peer.lastError}
      <dt>Last error</dt>
      <dd class="error">{peer.lastError}</dd>
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
  .error {
    color: var(--bad);
  }
</style>
