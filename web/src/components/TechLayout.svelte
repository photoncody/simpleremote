<script lang="ts">
  import type { Snippet } from 'svelte';
  import { logout, type Me } from '../lib/api';
  import { link, router } from '../lib/router.svelte';

  let { me, onSignedOut, children }: { me: Me; onSignedOut: () => void; children: Snippet } = $props();

  async function signOut() {
    try {
      await logout();
    } finally {
      onSignedOut();
    }
  }
</script>

<header>
  <a class="brand" href="/tech" use:link>
    <img src="/favicon.svg" alt="" width="24" height="24" />
    SimpleRemote
  </a>
  <nav>
    <a href="/tech" use:link class:current={router.path === '/tech'}>Sessions</a>
    <a href="/tech/audit" use:link class:current={router.path === '/tech/audit'}>Audit log</a>
  </nav>
  <div class="user">
    <span title={me.email}>{me.name}{#if me.method === 'local'} <span class="badge">backup admin</span>{/if}</span>
    <button class="small" onclick={signOut}>Sign out</button>
  </div>
</header>
<main>
  {@render children()}
</main>

<style>
  header {
    display: flex;
    align-items: center;
    gap: 1.5rem;
    padding: 0.7rem 1.5rem;
    background: var(--surface);
    border-bottom: 1px solid var(--border);
    flex-wrap: wrap;
  }
  .brand {
    display: flex;
    align-items: center;
    gap: 0.5rem;
    font-weight: 700;
    color: var(--text);
    text-decoration: none;
  }
  nav {
    display: flex;
    gap: 1rem;
    flex: 1;
  }
  nav a {
    color: var(--muted);
    text-decoration: none;
    padding: 0.3rem 0;
    border-bottom: 2px solid transparent;
  }
  nav a.current {
    color: var(--text);
    border-bottom-color: var(--accent);
  }
  .user {
    display: flex;
    align-items: center;
    gap: 0.8rem;
  }
  main {
    max-width: 1100px;
    margin: 0 auto;
    padding: 1.5rem;
  }
</style>
