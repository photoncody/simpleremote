<script lang="ts">
  import { api } from '../lib/api';
  import { formatBytes, formatTime, osLabel } from '../lib/format';
  import type { Display, Hello, Status } from '../lib/proto';
  import { RemoteSession, type ConnState, type Listing, type Transfer } from '../lib/remote';
  import { link, router } from '../lib/router.svelte';

  let { id }: { id: string } = $props();

  let canvas = $state<HTMLCanvasElement>();
  let remote: RemoteSession | null = null;

  let hello = $state<Hello | null>(null);
  let status = $state<Status | null>(null);
  let conn = $state<ConnState>('signaling');
  let code = $state('');
  let error = $state('');
  let endedReason = $state('');
  let displays = $state<Display[]>([]);
  let currentDisplay = $state(0);
  let quality = $state(65);
  let clipSync = $state(false);
  let remoteClip = $state('');
  let elevated = $state(false);
  let stats = $state({ fps: 0, kbps: 0 });
  let panel = $state<'' | 'chat' | 'files'>('');
  let chat = $state<{ from: 'tech' | 'user'; text: string; at: Date }[]>([]);
  let unread = $state(0);
  let chatText = $state('');
  let listing = $state<Listing | null>(null);
  let transfers = $state<Transfer[]>([]);
  let fileInput = $state<HTMLInputElement>();
  let dragOver = $state(false);

  const agent = $derived(status?.agent);
  const live = $derived(conn === 'connected');

  $effect(() => {
    api<{ session: { code: string } }>('GET', `/api/sessions/${id}`)
      .then((r) => (code = r.session.code))
      .catch((e) => (error = e.message));
  });

  $effect(() => {
    if (!canvas) return;
    const r = new RemoteSession(id, canvas, {
      hello: (h) => (hello = h),
      status: (s) => (status = s),
      conn: (c) => (conn = c),
      displays: (list, cur) => {
        displays = list;
        currentDisplay = cur;
      },
      chat: (text, from) => {
        chat = [...chat, { from, text, at: new Date() }];
        if (from === 'user' && panel !== 'chat') unread++;
      },
      clipboard: (text) => {
        remoteClip = text;
        navigator.clipboard?.writeText(text).catch(() => {});
      },
      listing: (l) => (listing = l),
      transfer: (t) => {
        const i = transfers.findIndex((x) => x.id === t.id);
        transfers = i < 0 ? [...transfers, t] : transfers.map((x) => (x.id === t.id ? t : x));
      },
      ended: (reason) => {
        endedReason = reason;
        panel = '';
      },
      error: (msg) => (error = msg),
      agentState: (s) => (elevated = s.elevated),
      stats: (s) => (stats = s),
    });
    remote = r;
    r.start();
    return () => r.close();
  });

  function endSession() {
    if (!confirm('End the session? The user will be disconnected.')) return;
    remote?.end();
    router.navigate('/tech');
  }

  function togglePanel(p: 'chat' | 'files') {
    panel = panel === p ? '' : p;
    if (panel === 'chat') unread = 0;
    if (panel === 'files' && !listing) remote?.listDir('');
  }

  function sendChat(e: SubmitEvent) {
    e.preventDefault();
    const t = chatText.trim();
    if (!t) return;
    remote?.sendChat(t);
    chatText = '';
  }

  function onFiles(files: FileList | null | undefined) {
    if (!files) return;
    for (const f of Array.from(files)) void remote?.upload(f);
    panel = 'files';
  }

  async function pasteClipboard() {
    try {
      const text = await navigator.clipboard.readText();
      remote?.sendClipboard(text);
    } catch {
      const text = prompt('Paste the text to send to the remote clipboard:');
      if (text) remote?.sendClipboard(text);
    }
  }

  const combos: { label: string; keys: string[] }[] = [
    { label: 'Ctrl+Shift+Esc', keys: ['ControlLeft', 'ShiftLeft', 'Escape'] },
    { label: 'Windows key', keys: ['MetaLeft'] },
    { label: 'Alt+Tab', keys: ['AltLeft', 'Tab'] },
    { label: 'Alt+F4', keys: ['AltLeft', 'F4'] },
    { label: 'Print Screen', keys: ['PrintScreen'] },
  ];

  function sendCombo(e: Event) {
    const sel = e.currentTarget as HTMLSelectElement;
    const c = combos[Number(sel.value)];
    if (c) remote?.sendKeys(c.keys);
    sel.value = '';
    canvas?.focus();
  }
</script>

<div class="viewer">
  <header>
    <a class="back" href="/tech" use:link title="Back to sessions">←</a>
    <div class="who">
      {#if agent}
        <strong>{agent.hostname}</strong>
        <span class="muted">{agent.username} · {osLabel(agent.os)} {agent.arch}{elevated ? ' · admin' : ''}</span>
      {:else}
        <strong>Session {code}</strong>
      {/if}
    </div>

    {#if live}
      <div class="tools">
        {#if displays.length > 1}
          <select aria-label="Display" value={currentDisplay} onchange={(e) => remote?.selectDisplay(Number(e.currentTarget.value))}>
            {#each displays as d (d.id)}
              <option value={d.id}>{d.name || `Display ${d.id + 1}`} ({d.width}×{d.height}){d.primary ? ' ★' : ''}</option>
            {/each}
          </select>
        {/if}
        <select aria-label="Quality" bind:value={quality} onchange={() => remote?.setQuality(quality)}>
          <option value={40}>Low quality</option>
          <option value={65}>Balanced</option>
          <option value={85}>High quality</option>
        </select>
        <select aria-label="Send keys" onchange={sendCombo}>
          <option value="">Send keys…</option>
          {#each combos as c, i (c.label)}<option value={i}>{c.label}</option>{/each}
        </select>
        <label class="check"><input type="checkbox" bind:checked={clipSync} onchange={() => remote?.setClipboardSync(clipSync)} /> Sync clipboard</label>
        <button class="small" onclick={pasteClipboard} title="Send your clipboard text to the remote computer">Send clipboard</button>
        <button class="small" class:on={panel === 'files'} onclick={() => togglePanel('files')}>Files</button>
        <button class="small" class:on={panel === 'chat'} onclick={() => togglePanel('chat')}>
          Chat{#if unread}<span class="dot">{unread}</span>{/if}
        </button>
        {#if agent?.os === 'windows' && !elevated}
          <button class="small" onclick={() => remote?.requestElevation()} title="Ask the user to relaunch the agent as administrator">Request admin</button>
        {/if}
        <span class="stats muted">{stats.fps} fps · {stats.kbps} kbps</span>
      </div>
    {/if}
    <button class="danger small" onclick={endSession}>End session</button>
  </header>

  <div class="body">
    <!-- svelte-ignore a11y_no_noninteractive_tabindex -->
    <div
      class="stage"
      class:drag={dragOver}
      role="application"
      aria-label="Remote screen"
      ondragover={(e) => { e.preventDefault(); dragOver = true; }}
      ondragleave={() => (dragOver = false)}
      ondrop={(e) => { e.preventDefault(); dragOver = false; onFiles(e.dataTransfer?.files); }}
    >
      <canvas bind:this={canvas} tabindex="0" width="1" height="1" class:hidden={!live}></canvas>
      {#if !live}
        <div class="overlay">
          {#if endedReason}
            <h2>Session ended</h2>
            <p>{endedReason}</p>
            <a class="button primary" href="/tech" use:link>Back to sessions</a>
          {:else if error && conn !== 'connecting'}
            <p class="error">{error}</p>
            <a class="button" href="/tech" use:link>Back to sessions</a>
          {:else if !status || status.state === 'pending'}
            <div class="spinner" aria-hidden="true"></div>
            <h2>Waiting for the user</h2>
            <p>Ask them to go to <strong>{location.origin}</strong> and enter <span class="code">{code}</span>.</p>
          {:else if status.state === 'awaiting_consent'}
            <div class="spinner" aria-hidden="true"></div>
            <h2>Waiting for the user to allow the connection</h2>
            {#if agent}<p class="muted">{agent.username} on {agent.hostname}</p>{/if}
          {:else if conn === 'reconnecting' || !status.agent_connected}
            <div class="spinner" aria-hidden="true"></div>
            <h2>Reconnecting…</h2>
            <p class="muted">The agent is reconnecting (for example after relaunching as administrator).</p>
          {:else}
            <div class="spinner" aria-hidden="true"></div>
            <h2>Connecting…</h2>
          {/if}
        </div>
      {/if}
    </div>

    {#if panel === 'chat'}
      <aside>
        <h3>Chat with user</h3>
        <ol class="chat">
          {#each chat as m, i (i)}
            <li class:me={m.from === 'tech'}><span>{m.text}</span><time>{m.at.toLocaleTimeString()}</time></li>
          {:else}
            <li class="muted">Messages appear in the user's SimpleRemote window.</li>
          {/each}
        </ol>
        <form onsubmit={sendChat}>
          <input bind:value={chatText} placeholder="Type a message" aria-label="Message" maxlength="2000" />
          <button class="primary small">Send</button>
        </form>
      </aside>
    {:else if panel === 'files'}
      <aside>
        <h3>Files</h3>
        <div class="row">
          <button class="small" onclick={() => fileInput?.click()}>Send files…</button>
          <span class="muted small">or drop them on the screen. Saved to the user's Downloads.</span>
          <input type="file" multiple bind:this={fileInput} class="sr-only" onchange={(e) => onFiles(e.currentTarget.files)} />
        </div>
        {#if transfers.length}
          <ul class="transfers">
            {#each [...transfers].reverse() as t (t.id)}
              <li>
                <div>{t.direction === 'up' ? '↑' : '↓'} {t.name}</div>
                {#if t.state === 'active'}
                  <progress max={t.size || 1} value={t.done}></progress>
                  <button class="small" onclick={() => remote?.cancel(t.id)}>Cancel</button>
                {:else}
                  <span class="muted small">{t.state === 'done' ? (t.message ?? 'Done') : t.state === 'error' ? `Failed: ${t.message}` : 'Cancelled'} · {formatBytes(t.size)}</span>
                {/if}
              </li>
            {/each}
          </ul>
        {/if}
        <h4>Browse the user's files</h4>
        {#if listing}
          <p class="path code" title={listing.path}>{listing.path}</p>
          <ul class="listing">
            {#if listing.parent}
              <li><button class="entry" onclick={() => remote?.listDir(listing!.parent)}>⬑ ..</button></li>
            {/if}
            {#each listing.entries as e (e.name)}
              <li>
                {#if e.dir}
                  <button class="entry" onclick={() => remote?.listDir(listing!.path + (listing!.path.endsWith('/') || listing!.path.endsWith('\\') ? '' : agent?.os === 'windows' ? '\\' : '/') + e.name)}>📁 {e.name}</button>
                {:else}
                  <button class="entry" title="Download {formatTime(new Date(e.mtime * 1000).toISOString())}" onclick={() => remote?.download(listing!.path + (agent?.os === 'windows' ? '\\' : '/') + e.name, e.size)}>
                    📄 {e.name} <span class="muted">{formatBytes(e.size)}</span>
                  </button>
                {/if}
              </li>
            {/each}
          </ul>
        {:else}
          <p class="muted">Loading…</p>
        {/if}
      </aside>
    {/if}
  </div>
  {#if remoteClip && clipSync}
    <div class="toast" role="status">Remote clipboard copied ({remoteClip.length} chars)</div>
  {/if}
</div>

<style>
  .viewer {
    height: 100vh;
    display: flex;
    flex-direction: column;
    background: #0c0d10;
  }
  header {
    display: flex;
    align-items: center;
    gap: 0.8rem;
    padding: 0.4rem 0.8rem;
    background: var(--surface);
    border-bottom: 1px solid var(--border);
    flex-wrap: wrap;
  }
  .back {
    font-size: 1.3rem;
    text-decoration: none;
    color: var(--text);
    padding: 0 0.3rem;
  }
  .who {
    display: flex;
    flex-direction: column;
    line-height: 1.2;
    margin-right: auto;
  }
  .who .muted {
    font-size: 0.85em;
  }
  .tools {
    display: flex;
    align-items: center;
    gap: 0.4rem;
    flex-wrap: wrap;
  }
  .tools select {
    padding: 0.3em 0.5em;
    font-size: 0.9em;
  }
  .check {
    display: flex;
    gap: 0.3rem;
    align-items: center;
    font-weight: 400;
    font-size: 0.9em;
    margin: 0;
  }
  button.on {
    background: var(--surface-2);
    border-color: var(--accent);
  }
  .dot {
    background: var(--accent);
    color: #fff;
    border-radius: 999px;
    font-size: 0.75em;
    padding: 0 0.45em;
  }
  .stats {
    font-size: 0.8em;
    min-width: 7.5em;
    text-align: right;
  }
  .body {
    flex: 1;
    display: flex;
    min-height: 0;
  }
  .stage {
    flex: 1;
    display: flex;
    align-items: center;
    justify-content: center;
    min-width: 0;
    min-height: 0;
    position: relative;
  }
  .stage.drag {
    outline: 3px dashed var(--accent);
    outline-offset: -6px;
  }
  canvas {
    max-width: 100%;
    max-height: 100%;
    cursor: default;
    image-rendering: auto;
  }
  canvas:focus {
    outline: none;
  }
  canvas.hidden {
    display: none;
  }
  .overlay {
    color: #e8eaed;
    text-align: center;
    max-width: 32rem;
    padding: 1rem;
  }
  .overlay .muted {
    color: #9aa3ae;
  }
  .spinner {
    width: 36px;
    height: 36px;
    margin: 0 auto 1rem;
    border: 3px solid #333;
    border-top-color: var(--accent);
    border-radius: 50%;
    animation: spin 0.9s linear infinite;
  }
  @keyframes spin {
    to {
      transform: rotate(360deg);
    }
  }
  aside {
    width: 22rem;
    background: var(--surface);
    border-left: 1px solid var(--border);
    padding: 0.8rem;
    display: flex;
    flex-direction: column;
    overflow: auto;
  }
  aside h3 {
    font-size: 1rem;
  }
  aside h4 {
    font-size: 0.9rem;
    margin: 1rem 0 0.3rem;
  }
  .chat {
    list-style: none;
    padding: 0;
    margin: 0 0 0.5rem;
    flex: 1;
    overflow: auto;
    display: flex;
    flex-direction: column;
    gap: 0.4rem;
  }
  .chat li {
    background: var(--surface-2);
    padding: 0.4em 0.7em;
    border-radius: 10px;
    max-width: 85%;
    align-self: flex-start;
    word-wrap: break-word;
  }
  .chat li.me {
    align-self: flex-end;
    background: var(--accent);
    color: #fff;
  }
  .chat time {
    display: block;
    font-size: 0.7em;
    opacity: 0.7;
  }
  aside form {
    display: flex;
    gap: 0.4rem;
  }
  aside form input {
    flex: 1;
    min-width: 0;
  }
  .row {
    display: flex;
    gap: 0.5rem;
    align-items: center;
    flex-wrap: wrap;
  }
  .small {
    font-size: 0.85em;
  }
  .transfers,
  .listing {
    list-style: none;
    padding: 0;
    margin: 0.5rem 0;
  }
  .transfers li {
    padding: 0.3rem 0;
    border-bottom: 1px solid var(--border);
    font-size: 0.9em;
  }
  .transfers progress {
    width: 70%;
  }
  .path {
    font-size: 0.8em;
    word-break: break-all;
    margin: 0;
  }
  .entry {
    width: 100%;
    justify-content: flex-start;
    border: none;
    background: none;
    padding: 0.25em 0.3em;
    font-size: 0.9em;
    white-space: normal;
    text-align: left;
  }
  .toast {
    position: fixed;
    bottom: 1rem;
    left: 50%;
    transform: translateX(-50%);
    background: var(--surface);
    border: 1px solid var(--border);
    box-shadow: var(--shadow);
    padding: 0.4rem 0.9rem;
    border-radius: 8px;
    font-size: 0.85em;
    animation: fade 3s forwards;
  }
  @keyframes fade {
    0%,
    80% {
      opacity: 1;
    }
    100% {
      opacity: 0;
    }
  }
</style>
