<script lang="ts">
  import { truncateKey } from '$lib/format';

  let { value, prefixLen = 8, suffixLen = 4 }: { value: string; prefixLen?: number; suffixLen?: number } = $props();
  let copied = $state(false);

  async function copy() {
    await navigator.clipboard.writeText(value);
    copied = true;
    setTimeout(() => (copied = false), 1200);
  }
</script>

<span class="key">
  <span class="mono" title={value}>{truncateKey(value, prefixLen, suffixLen)}</span>
  <button type="button" onclick={copy} aria-label="Copy full value">
    {copied ? '✓' : 'copy'}
  </button>
</span>

<style>
  .key {
    display: inline-flex;
    align-items: center;
    gap: 0.4em;
  }
  .mono {
    font-family: var(--mono);
    font-size: 0.85em;
  }
  button {
    font-size: 0.7em;
    background: transparent;
    border: 1px solid var(--border);
    border-radius: 4px;
    color: var(--text-dim);
    padding: 0.05em 0.4em;
    cursor: pointer;
  }
  button:hover {
    color: var(--text);
    border-color: var(--accent);
  }
</style>
