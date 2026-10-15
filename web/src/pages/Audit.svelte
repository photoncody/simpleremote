<script lang="ts">
  import { api, type AuditEvent, type SupportSession } from '../lib/api';
  import { formatDuration, formatTime, osLabel, stateLabels } from '../lib/format';

  const PAGE = 50;
  let tab = $state<'sessions' | 'events'>('sessions');
  let sessions = $state<SupportSession[]>([]);
  let events = $state<AuditEvent[]>([]);
  let offset = $state(0);
  let open = $state<string | null>(null);
  let detail = $state<AuditEvent[]>([]);
  let error = $state('');

  $effect(() => {
    const t = tab;
    const o = offset;
    error = '';
    const req =
      t === 'sessions'
        ? api<SupportSession[]>('GET', `/api/audit/sessions?limit=${PAGE}&offset=${o}`).then((r) => (sessions = r))
        : api<AuditEvent[]>('GET', `/api/audit/events?limit=${PAGE}&offset=${o}`).then((r) => (events = r));
    req.catch((e) => (error = e.message));
  });

  async function toggle(id: string) {
    if (open === id) {
      open = null;
      return;
    }
    open = id;
    detail = [];
    try {
      detail = await api<AuditEvent[]>('GET', `/api/audit/sessions/${id}/events`);
    } catch (e) {
      error = e instanceof Error ? e.message : String(e);
    }
  }

  function switchTab(t: 'sessions' | 'events') {
    tab = t;
    offset = 0;
    open = null;
  }

  const count = $derived(tab === 'sessions' ? sessions.length : events.length);
</script>

<div class="top">
  <h1>Audit log</h1>
  <a class="button" href="/api/audit/sessions.csv" download>Export sessions (CSV)</a>
</div>

<div class="tabs" role="tablist">
  <button role="tab" aria-selected={tab === 'sessions'} class:on={tab === 'sessions'} onclick={() => switchTab('sessions')}>Sessions</button>
  <button role="tab" aria-selected={tab === 'events'} class:on={tab === 'events'} onclick={() => switchTab('events')}>All events</button>
</div>

{#if error}<p class="error">{error}</p>{/if}

{#if tab === 'sessions'}
  <table>
    <thead>
      <tr><th>Started</th><th>Tech</th><th>Computer</th><th>Status</th><th>Duration</th><th></th></tr>
    </thead>
    <tbody>
      {#each sessions as s (s.id)}
        <tr>
          <td>{formatTime(s.created_at)}</td>
          <td>{s.tech_name}<br /><span class="muted small">{s.tech_email}</span></td>
          <td>
            {#if s.agent_hostname}
              {s.agent_hostname}<br /><span class="muted small">{s.agent_user} · {osLabel(s.agent_os)} · {s.agent_ip}</span>
            {:else}<span class="muted">—</span>{/if}
          </td>
          <td>{stateLabels[s.state] ?? s.state}{#if s.end_reason}<br /><span class="muted small">{s.end_reason}</span>{/if}</td>
          <td>{s.consented_at && !s.consented_at.startsWith('0001-') ? formatDuration(s.consented_at, s.ended_at) : '—'}</td>
          <td><button class="small" onclick={() => toggle(s.id)} aria-expanded={open === s.id}>{open === s.id ? 'Hide' : 'Events'}</button></td>
        </tr>
        {#if open === s.id}
          <tr class="detail">
            <td colspan="6">
              <table>
                <tbody>
                  {#each detail as e (e.id)}
                    <tr><td>{formatTime(e.at)}</td><td>{e.actor}</td><td><strong>{e.kind}</strong></td><td>{e.detail}</td><td class="muted">{e.ip}</td></tr>
                  {:else}
                    <tr><td class="muted">Loading…</td></tr>
                  {/each}
                </tbody>
              </table>
            </td>
          </tr>
        {/if}
      {:else}
        <tr><td colspan="6" class="muted">No sessions yet.</td></tr>
      {/each}
    </tbody>
  </table>
{:else}
  <table>
    <thead><tr><th>Time</th><th>Actor</th><th>Event</th><th>Detail</th><th>IP</th></tr></thead>
    <tbody>
      {#each events as e (e.id)}
        <tr><td>{formatTime(e.at)}</td><td>{e.actor}</td><td><strong>{e.kind}</strong></td><td>{e.detail}</td><td class="muted">{e.ip}</td></tr>
      {:else}
        <tr><td colspan="5" class="muted">No events.</td></tr>
      {/each}
    </tbody>
  </table>
{/if}

<div class="pager">
  <button class="small" disabled={offset === 0} onclick={() => (offset = Math.max(0, offset - PAGE))}>← Newer</button>
  <button class="small" disabled={count < PAGE} onclick={() => (offset += PAGE)}>Older →</button>
</div>

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
  .tabs {
    display: flex;
    gap: 0.4rem;
    margin-bottom: 1rem;
  }
  .tabs button.on {
    border-color: var(--accent);
    background: var(--surface-2);
  }
  .small {
    font-size: 0.85em;
  }
  tr.detail > td {
    background: var(--surface-2);
    padding: 0.3rem 1rem;
  }
  .pager {
    display: flex;
    justify-content: center;
    gap: 0.5rem;
    margin: 1rem 0;
  }
</style>
