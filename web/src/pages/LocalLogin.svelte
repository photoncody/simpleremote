<script lang="ts">
  import { api, ApiError, getAuthConfig } from '../lib/api';
  import { link, router } from '../lib/router.svelte';

  let username = $state('');
  let password = $state('');
  let busy = $state(false);
  let error = $state('');
  let oidc = $state(false);

  $effect(() => {
    getAuthConfig()
      .then((c) => (oidc = c.oidc_enabled))
      .catch(() => {});
  });

  async function submit(e: SubmitEvent) {
    e.preventDefault();
    busy = true;
    error = '';
    try {
      await api('POST', '/api/auth/local', { username, password });
      const next = router.param('next');
      location.href = next && next.startsWith('/tech') && !next.startsWith('//') ? next : '/tech';
    } catch (err) {
      error = err instanceof ApiError ? err.message : 'Could not reach the server.';
      busy = false;
    }
  }
</script>

<main>
  <form class="card" onsubmit={submit}>
    <h1>Backup admin sign-in</h1>
    <p class="muted">For use when single sign-on is unavailable.</p>
    {#if error}<p class="error" role="alert">{error}</p>{/if}
    <div class="field">
      <label for="u">Username</label>
      <input id="u" autocomplete="username" bind:value={username} required />
    </div>
    <div class="field">
      <label for="p">Password</label>
      <input id="p" type="password" autocomplete="current-password" bind:value={password} required />
    </div>
    <button class="primary wide" disabled={busy}>{busy ? 'Signing in…' : 'Sign in'}</button>
    {#if oidc}<p class="alt"><a href="/tech/login" use:link>Back to single sign-on</a></p>{/if}
  </form>
</main>

<style>
  main {
    min-height: 100vh;
    display: grid;
    place-items: center;
    padding: 1rem;
  }
  .card {
    width: 100%;
    max-width: 24rem;
  }
  .field {
    margin-bottom: 1rem;
  }
  .field input {
    width: 100%;
  }
  .wide {
    width: 100%;
    padding: 0.7em;
  }
  .alt {
    margin: 1rem 0 0;
    font-size: 0.9rem;
    text-align: center;
  }
</style>
