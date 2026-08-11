<script lang="ts">
  let { status }: { status: 'online' | 'degraded' | 'disconnected' } = $props();

  const labels = { online: 'Online', degraded: 'Degraded', disconnected: 'Disconnected' } as const;
</script>

<span class="badge {status}">
  <span class="dot"></span>
  {labels[status]}
</span>

<style>
  .badge {
    display: inline-flex;
    align-items: center;
    gap: 0.4em;
    font-size: 0.85rem;
    font-weight: 600;
  }
  .dot {
    width: 0.6em;
    height: 0.6em;
    border-radius: 50%;
    background: currentColor;
  }
  .online {
    color: var(--ok);
  }
  .degraded {
    color: var(--warn);
  }
  .disconnected {
    color: var(--bad);
  }
</style>
