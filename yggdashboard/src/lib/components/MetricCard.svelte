<script lang="ts">
  let { label, value, sublabel = '' }: { label: string; value: string; sublabel?: string } = $props();
</script>

<div class="card">
  <div class="label">{label}</div>
  <div class="value">{value}</div>
  {#if sublabel}
    <div class="sublabel">{sublabel}</div>
  {/if}
</div>

<style>
  .card {
    background: var(--bg-raised);
    border: 1px solid var(--border);
    border-radius: var(--radius);
    padding: 0.75rem 1rem;
  }
  .label {
    font-size: 0.75rem;
    text-transform: uppercase;
    letter-spacing: 0.04em;
    color: var(--text-dim);
  }
  .value {
    font-family: var(--mono);
    font-size: 1.4rem;
    font-weight: 600;
    margin-top: 0.2rem;
  }
  .sublabel {
    font-size: 0.75rem;
    color: var(--text-dim);
    margin-top: 0.15rem;
  }
</style>
