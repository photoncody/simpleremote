<script lang="ts">
  import { api, ApiError, type CodeCheck, type Platform } from '../lib/api';
  import { CODE_LENGTH, cleanCode, detectArch, detectOS, formatBytes, type ClientOS } from '../lib/format';

  let orgName = $state('');
  let code = $state('');
  let busy = $state(false);
  let error = $state('');
  let result = $state<CodeCheck | null>(null);
  let os = $state<ClientOS>(detectOS());
  let arch = $state<'amd64' | 'arm64'>('amd64');
  let downloaded = $state(false);

  const digits = $derived(code.replace(/\D/g, ''));
  const recommended = $derived(pick(result?.platforms ?? [], os, arch));
  const others = $derived((result?.platforms ?? []).filter((p) => p.id !== recommended?.id));

  $effect(() => {
    void detectArch().then((a) => (arch = a));
    void api<{ org_name: string }>('GET', '/api/public/config').then((c) => (orgName = c.org_name)).catch(() => {});
  });

  function pick(list: Platform[], os: ClientOS, arch: string): Platform | undefined {
    const forOS = list.filter((p) => p.os === os);
    return forOS.find((p) => p.arch === arch || p.arch === 'universal') ?? forOS[0];
  }

  function onInput(e: Event) {
    const el = e.currentTarget as HTMLInputElement;
    code = cleanCode(el.value);
    el.value = code;
    error = '';
  }

  async function submit(e: SubmitEvent) {
    e.preventDefault();
    if (digits.length !== CODE_LENGTH) {
      error = `The code has ${CODE_LENGTH} digits.`;
      return;
    }
    busy = true;
    error = '';
    try {
      result = await api<CodeCheck>('POST', '/api/public/code', { code: digits });
    } catch (err) {
      error = err instanceof ApiError ? err.message : 'Could not reach the server. Check your internet connection.';
    } finally {
      busy = false;
    }
  }

  function reset() {
    result = null;
    downloaded = false;
    code = '';
  }
</script>

<main>
  <div class="card">
    <div class="brand">
      <img src="/favicon.svg" alt="" width="36" height="36" />
      <div>
        <h1>Remote support</h1>
        {#if orgName}<p class="muted">{orgName}</p>{/if}
      </div>
    </div>

    {#if !result}
      <form onsubmit={submit}>
        <label for="code">Enter the code your support technician gave you</label>
        <input
          id="code"
          class="code big"
          inputmode="numeric"
          autocomplete="one-time-code"
          placeholder="123456"
          maxlength={CODE_LENGTH}
          value={code}
          oninput={onInput}
          aria-invalid={!!error}
          aria-describedby={error ? 'code-error' : undefined}
        />
        {#if error}<p id="code-error" class="error" role="alert">{error}</p>{/if}
        <button class="primary wide" disabled={busy || digits.length !== CODE_LENGTH}>{busy ? 'Checking…' : 'Continue'}</button>
      </form>
      <p class="muted small">
        Only use a code from someone you trust and are talking to right now. Your technician will be able to see and
        control your screen after you allow it, and you can end the session at any time.
      </p>
    {:else}
      <div class="who">
        <p>
          You're connecting to <strong>{result.tech_name}</strong> at <strong>{result.org_name}</strong>.
        </p>
        <p class="notice small">
          If you weren't expecting this, or someone called you out of the blue, stop here and close this page.
        </p>
      </div>

      {#if result.platforms.length === 0}
        <p class="error">The support app isn't available on this server yet. Let your technician know.</p>
      {:else if os === 'other' || !recommended}
        <p class="notice">Remote support works on Windows, macOS, and Linux computers. Choose your system:</p>
      {:else}
        <a
          class="button primary wide"
          href="/d/{result.code}/{recommended.id}"
          download
          onclick={() => (downloaded = true)}
        >
          Download for {recommended.label}
          <span class="size">({formatBytes(recommended.size)})</span>
        </a>
      {/if}

      {#if downloaded || os === 'other' || !recommended}
        <section class="steps">
          <h2>Next steps</h2>
          {#if os === 'windows'}
            <ol>
              <li>Open <strong>SimpleRemote-{result.code}.exe</strong> from your Downloads.</li>
              <li>If Windows shows "Windows protected your PC", click <strong>More info</strong>, then <strong>Run anyway</strong>.</li>
              <li>Check the technician's name, then click <strong>Allow</strong>.</li>
            </ol>
          {:else if os === 'darwin'}
            <ol>
              <li>Open <strong>SimpleRemote-{result.code}.zip</strong> from your Downloads to unzip it.</li>
              <li>Control-click the <strong>SimpleRemote</strong> app and choose <strong>Open</strong>.</li>
              <li>
                When asked, allow <strong>Screen Recording</strong> and <strong>Accessibility</strong> in System Settings,
                then reopen the app.
              </li>
              <li>Check the technician's name, then click <strong>Allow</strong>.</li>
            </ol>
          {:else if os === 'linux'}
            <ol>
              <li>Open a terminal in your Downloads folder.</li>
              <li>
                Run <code>chmod +x SimpleRemote-{result.code} && ./SimpleRemote-{result.code}</code>
              </li>
              <li>Check the technician's name, then click <strong>Allow</strong>.</li>
            </ol>
          {/if}
          <p class="muted small">The app doesn't install anything. It closes when the session ends.</p>
        </section>
      {/if}

      {#if others.length}
        <details open={os === 'other' || !recommended}>
          <summary>Other systems</summary>
          <ul class="others">
            {#each others as p (p.id)}
              <li>
                <a href="/d/{result.code}/{p.id}" download onclick={() => { os = p.os; downloaded = true; }}>{p.label}</a>
                <span class="muted">({formatBytes(p.size)})</span>
              </li>
            {/each}
          </ul>
        </details>
      {/if}

      <button class="link" onclick={reset}>Use a different code</button>
    {/if}
  </div>
  <footer><a href="/tech">Technician sign-in</a></footer>
</main>

<style>
  main {
    min-height: 100vh;
    display: flex;
    flex-direction: column;
    align-items: center;
    justify-content: center;
    padding: 1.5rem 1rem;
    gap: 1rem;
  }
  .card {
    width: 100%;
    max-width: 30rem;
  }
  .brand {
    display: flex;
    gap: 0.8rem;
    align-items: center;
    margin-bottom: 1.5rem;
  }
  .brand h1 {
    margin: 0;
    font-size: 1.4rem;
  }
  .brand p {
    margin: 0;
  }
  form {
    display: grid;
    gap: 0.8rem;
  }
  .big {
    font-size: 1.8rem;
    text-align: center;
    padding: 0.5em;
    width: 100%;
  }
  .wide {
    width: 100%;
    padding: 0.8em;
    font-size: 1.05rem;
  }
  .small {
    font-size: 0.88rem;
  }
  .size {
    font-weight: 400;
    opacity: 0.85;
  }
  .who p {
    margin-top: 0;
  }
  .steps {
    margin-top: 1.2rem;
  }
  .steps h2 {
    font-size: 1.05rem;
  }
  .steps li {
    margin-bottom: 0.4rem;
  }
  code {
    font-family: var(--mono);
    font-size: 0.9em;
    background: var(--surface-2);
    padding: 0.1em 0.3em;
    border-radius: 4px;
    word-break: break-all;
  }
  details {
    margin-top: 1rem;
  }
  .others {
    padding-left: 1.2rem;
  }
  button.link {
    margin-top: 1rem;
    border: none;
    background: none;
    color: var(--accent);
    padding: 0;
    text-decoration: underline;
  }
  footer {
    font-size: 0.85rem;
  }
  footer a {
    color: var(--muted);
  }
</style>
