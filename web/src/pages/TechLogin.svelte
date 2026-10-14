<script lang="ts">
  import { getAuthConfig, type AuthConfig } from '../lib/api';
  import { link, router } from '../lib/router.svelte';

  let cfg = $state<AuthConfig | null>(null);
  let failed = $state(false);

  const messages: Record<string, string> = {
    not_allowed: "Your account isn't allowed to use SimpleRemote. Ask an administrator to add you.",
    provider_unavailable: "The sign-in provider couldn't be reached. Try again, or use the backup admin sign-in.",
    provider_error: 'The sign-in provider reported an error. Try again.',
    session_expired: 'The sign-in attempt expired. Try again.',
    exchange_failed: "Sign-in didn't complete. Try again.",
    invalid_token: "The sign-in provider's response couldn't be verified.",
    server_error: 'Something went wrong on the server. Try again.',
  };

  const error = $derived(router.param('error'));
  const next = $derived(router.param('next') ?? '/tech');

  $effect(() => {
    getAuthConfig()
      .then((c) => {
        cfg = c;
        if (!c.oidc_enabled && c.local_enabled) router.navigate('/tech/login/local', true);
      })
      .catch(() => (failed = true));
  });
</script>

<main>
  <div class="card">
    <h1>Technician sign-in</h1>
    {#if cfg}<p class="muted">{cfg.org_name}</p>{/if}
    {#if error}<p class="error" role="alert">{messages[error] ?? 'Sign-in failed.'}</p>{/if}
    {#if router.param('signed_out')}<p class="notice">You have been signed out.</p>{/if}
    {#if failed}
      <p class="error">Could not reach the server.</p>
    {:else if cfg?.oidc_enabled}
      <a class="button primary wide" href="/auth/oidc/login?next={encodeURIComponent(next)}">
        Sign in with {cfg.oidc_provider_name}
      </a>
    {/if}
    {#if cfg?.local_enabled}
      <p class="alt"><a href="/tech/login/local?next={encodeURIComponent(next)}" use:link>Backup admin sign-in</a></p>
    {/if}
  </div>
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
  .wide {
    width: 100%;
    padding: 0.8em;
    margin-top: 0.5rem;
  }
  .alt {
    margin: 1.2rem 0 0;
    font-size: 0.9rem;
    text-align: center;
  }
</style>
