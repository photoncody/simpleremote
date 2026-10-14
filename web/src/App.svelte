<script lang="ts">
  import { router } from './lib/router.svelte';
  import { getAuthConfig, loadMe, type Me } from './lib/api';
  import Home from './pages/Home.svelte';
  import TechLogin from './pages/TechLogin.svelte';
  import LocalLogin from './pages/LocalLogin.svelte';
  import Dashboard from './pages/Dashboard.svelte';
  import SessionView from './pages/SessionView.svelte';
  import Audit from './pages/Audit.svelte';
  import TechLayout from './components/TechLayout.svelte';

  let me = $state<Me | null>(null);
  let checking = $state(false);
  let loadError = $state('');

  const path = $derived(router.path.replace(/\/+$/, '') || '/');
  const isLogin = $derived(path.startsWith('/tech/login'));
  const isTech = $derived(path.startsWith('/tech') && !isLogin);
  const sessionMatch = $derived(path.match(/^\/tech\/session\/([A-Za-z0-9_-]+)$/));

  $effect(() => {
    if (isTech && !me && !checking) void ensureSignedIn();
  });

  async function ensureSignedIn() {
    checking = true;
    loadError = '';
    try {
      const m = await loadMe();
      if (m) {
        me = m;
        return;
      }
      const next = encodeURIComponent(location.pathname + location.search);
      const cfg = await getAuthConfig();
      if (cfg.auto_login) location.href = `/auth/oidc/login?next=${next}`;
      else router.navigate(`/tech/login?next=${next}`, true);
    } catch (e) {
      loadError = e instanceof Error ? e.message : String(e);
    } finally {
      checking = false;
    }
  }

  function signedOut() {
    me = null;
    router.navigate('/tech/login?signed_out=1');
  }
</script>

{#if path === '/'}
  <Home />
{:else if path === '/tech/login'}
  <TechLogin />
{:else if path === '/tech/login/local'}
  <LocalLogin />
{:else if isTech}
  {#if loadError}
    <main class="center"><p class="error">Could not reach the server: {loadError}</p></main>
  {:else if !me}
    <main class="center"><p class="muted">Loading…</p></main>
  {:else if sessionMatch}
    {#key sessionMatch[1]}
      <SessionView id={sessionMatch[1]} />
    {/key}
  {:else}
    <TechLayout {me} onSignedOut={signedOut}>
      {#if path === '/tech'}
        <Dashboard />
      {:else if path === '/tech/audit'}
        <Audit />
      {:else}
        <p>Page not found. <a href="/tech">Back to sessions</a></p>
      {/if}
    </TechLayout>
  {/if}
{:else}
  <main class="center">
    <p>Page not found. <a href="/">Go to the start page</a></p>
  </main>
{/if}

<style>
  .center {
    min-height: 100vh;
    display: grid;
    place-items: center;
    padding: 1rem;
  }
</style>
