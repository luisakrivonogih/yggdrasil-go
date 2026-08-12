<script lang="ts">
  import GarlicPanel from '$lib/components/GarlicPanel.svelte';
  import { createGarlicResource } from '$lib/stores/dashboard.svelte';

  let { data } = $props();

  const garlicResource = createGarlicResource();
  $effect(() => {
    garlicResource.start();
    return () => garlicResource.stop();
  });

  let garlic = $derived(garlicResource.data ?? data.garlic);
</script>

<svelte:head>
  <title>yggdashboard · garlic</title>
</svelte:head>

<GarlicPanel {garlic} />
