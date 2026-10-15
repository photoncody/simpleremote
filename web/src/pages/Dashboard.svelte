<script lang="ts">
  import { api, ApiError, type CreatedSession, type LiveSession } from '../lib/api';
  import { formatDuration, osLabel, stateLabels } from '../lib/format';
  import { link, router } from '../lib/router.svelte';

  let sessions = $state<LiveSession[]>([]);
  let created = $state<CreatedSession | null>(null);
  // Local deadline computed from the server's "expires_in" so clock skew
  // between the browser and server doesn't matter.
  let deadline = $state(0);
  let error = $state('');
  let busy = $state(false);
  let now = $state(Date.now());
  let copied = $state(false);

  const mine = $derived(sessions.filter((s) => s.mine).sort((a, b) => b.created_at.localeCompare(a.created_at)));
  const others = $derived(sessions.filter((s) => !s.mine));
  const createdLive = $derived(created ? sessions.find((s) => s.id === created!.id) : undefined);
  const secondsLeft = $derived(created ? Math.max(0, Math.round((deadline - now) / 1000)) : 0);

  async function refresh() {
    try {
      sessions = await api<LiveSession[]>('GET', '/api/sessions');
      error = '';
    } catch (e) {
      if (e instanceof ApiError && e.status === 401) location.reload();
      else error = 'Could not load sessions.';
    }
  }

  $effect(() => {
    void refresh();
    const poll = setInterval(refresh, 3000);
    const tick = setInterval(() => (now = Date.now()), 1000);
    return () => {
      clearInterval(poll);
      clearInterval(tick);
    };
  });

  // Jump into the viewer once the user has connected.
  $effect(() => {
    if (createdLive && createdLive.state !== 'pending') router.navigate(`/tech/session/${createdLive.id}`);
  });

  async function create() {
    busy = true;
    error = '';
    try {
      created = await api<CreatedSession>('POST', '/api/sessions');
      deadline = Date.now() + created.expires_in * 1000;
      now = Date.now();
      copied = false;
      await refresh();
    } catch (e) {
      error = e instanceof Error ? e.message : String(e);
    } finally {
      busy = false;
    }
  }

  async function end(id: string) {
    if (!confirm('End this session?')) return;
    try {
      await api('DELETE', `/api/sessions/${id}`);
      if (created?.id === id) created = null;
      await refresh();
    } catch (e) {
      error = e instanceof Error ? e.message : String(e);
    }
  }

  async function copyInstructions() {
    if (!created) return;
    const text = `Go to ${created.join_url} and enter code ${created.code}`;
    try {
      await navigator.clipboard.writeText(text);
      copied = true;
    } catch {
      prompt('Copy these instructions:', text);
    }
  }

  function badgeClass(state: string) {
    return state === 'active' ? 'active' : state === 'pending' || state === 'awaiting_consent' ? 'waiting' : '';
  }
</script>

<div class="top">
  <h1>Sessions</h1>
  <button class="primary" onclick={create} disabled={busy}>+ New session</button>
</div>

{#if error}<p class="error" role="alert">{error}</p>{/if}

{#if created}
  <section class="card newsession" aria-live="polite">
    {#if secondsLeft > 0 && (!createdLive || createdLive.state === 'pending')}
      <p class="muted">Ask the user to go to <strong>{created.join_url}</strong> and enter:</p>
      <p class="bigcode code">{created.code}</p>
      <p class="muted">
        Code expires in {Math.floor(secondsLeft / 60)}:{String(secondsLeft % 60).padStart(2, '0')}. Waiting for the user…
      </p>
      <div class="row">
        <button onclick={copyInstructions}>{copied ? 'Copied' : 'Copy instructions'}</button>
        <a class="button" href="/tech/session/{created.id}" use:link>Open viewer</a>
        <button class="danger" onclick={() => end(created!.id)}>Cancel</button>
      </div>
    {:else if secondsLeft === 0 && (!createdLive || createdLive.state === 'pending')}
      <p>The code <span class="code">{created.code}</span> expired before it was used.</p>
      <button onclick={() => (created = null)}>Dismiss</button>
    {/if}
  </section>
{/if}

<section>
  <h2>Your live sessions</h2>
  {#if mine.length === 0}
    <p class="muted">No live sessions. Click <strong>New session</strong> to get a code for a user.</p>
  {:else}
    <table>
      <thead>
        <tr><th>Code</th><th>Status</th><th>Computer</th><th>Started</th><th></th></tr>
      </thead>
      <tbody>
        {#each mine as s (s.id)}
          <tr>
            <td class="code">{s.code}</td>
            <td><span class="badge {badgeClass(s.state)}">{stateLabels[s.state] ?? s.state}</span>
              {#if s.state !== 'pending' && !s.agent_connected}<span class="badge waiting">agent offline</span>{/if}</td>
            <td>
              {#if s.agent_hostname}
                {s.agent_hostname} <span class="muted">({s.agent_user}, {osLabel(s.agent_os)})</span>
              {:else}<span class="muted">—</span>{/if}
            </td>
            <td>{formatDuration(s.created_at)} ago</td>
            <td class="actions">
              <a class="button small" href="/tech/session/{s.id}" use:link>Open</a>
              <button class="small danger" onclick={() => end(s.id)}>End</button>
            </td>
          </tr>
        {/each}
      </tbody>
    </table>
  {/if}
</section>

{#if others.length}
  <section>
    <h2>Other techs' sessions</h2>
    <table>
      <thead><tr><th>Tech</th><th>Status</th><th>Computer</th><th>Started</th></tr></thead>
      <tbody>
        {#each others as s (s.id)}
          <tr>
            <td>{s.tech_name}</td>
            <td><span class="badge {badgeClass(s.state)}">{stateLabels[s.state] ?? s.state}</span></td>
            <td>{s.agent_hostname || '—'}</td>
            <td>{formatDuration(s.created_at)} ago</td>
          </tr>
        {/each}
      </tbody>
    </table>
  </section>
{/if}

<style>
  .top {
    display: flex;
    justify-content: space-between;
    align-items: center;
    margin-bottom: 1rem;
  }
  .top h1 {
    margin: 0;
  }
  section {
    margin-bottom: 2rem;
  }
  h2 {
    font-size: 1.1rem;
  }
  .newsession {
    text-align: center;
  }
  .bigcode {
    font-size: 3rem;
    font-weight: 700;
    margin: 0.2em 0;
  }
  .row {
    display: flex;
    gap: 0.6rem;
    justify-content: center;
    flex-wrap: wrap;
  }
  .actions {
    display: flex;
    gap: 0.4rem;
    justify-content: flex-end;
  }
  .actions .button {
    padding: 0.3em 0.7em;
    font-size: 0.9em;
  }
</style>
