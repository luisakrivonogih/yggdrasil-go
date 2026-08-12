<script lang="ts">
  import { page } from '$app/stores';

  const links = [
    { href: '/', label: 'Overview' },
    { href: '/connections', label: 'Connections' },
    { href: '/circuits', label: 'Circuits' },
    { href: '/garlic', label: 'Garlic' },
    { href: '/graph', label: 'Graph' }
  ];
</script>

<nav>
  {#each links as link (link.href)}
    <a href={link.href} class:active={$page.url.pathname === link.href}>{link.label}</a>
  {/each}
</nav>

<style>
  nav {
    display: flex;
    gap: 1rem;
    flex-wrap: wrap;
  }
  a {
    color: var(--text-dim);
    text-decoration: none;
    font-size: 0.9rem;
    padding: 0.25rem 0;
    border-bottom: 2px solid transparent;
  }
  a.active {
    color: var(--text);
    border-bottom-color: var(--accent);
  }
  a:hover {
    color: var(--text);
  }
</style>
