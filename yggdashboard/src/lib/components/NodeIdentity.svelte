<script lang="ts">
  import CopyableKey from './CopyableKey.svelte';

  let {
    buildName,
    buildVersion,
    address,
    publicKey
  }: { buildName: string; buildVersion: string; address: string; publicKey: string } = $props();
</script>

<section class="identity">
  <h2>Yggdrasil</h2>
  <dl>
    <dt>Build</dt>
    <dd>{buildName} {buildVersion}</dd>
    <dt>Public key</dt>
    <dd><CopyableKey value={publicKey} /></dd>
    <dt>Address</dt>
    <dd class="mono">{address}</dd>
  </dl>
</section>

<style>
  .identity {
    background: var(--bg-raised);
    border: 1px solid var(--border);
    border-radius: var(--radius);
    padding: 0.75rem 1rem;
  }
  h2 {
    font-size: 0.9rem;
    margin: 0 0 0.5rem;
    color: var(--text-dim);
    text-transform: uppercase;
    letter-spacing: 0.04em;
  }
  dl {
    display: grid;
    grid-template-columns: max-content 1fr;
    gap: 0.3rem 1rem;
    margin: 0;
  }
  dt {
    color: var(--text-dim);
    font-size: 0.85rem;
  }
  .mono {
    font-family: var(--mono);
  }
</style>
