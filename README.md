# SimpleRemote

Simple, self-hosted, **attended** remote support. A technician creates a one-time code, the user enters it on your support website, downloads a small app, and clicks **Allow**. The technician then sees and controls the user's computer from a web browser. When the session ends, the app closes. Nothing is installed and nothing keeps running.

- **No accounts for end users.** Technicians sign in with your identity provider (OIDC), with an environment-configured backup admin for when single sign-on is down.
- **Windows, macOS, and Linux** agents, built by GitHub Actions on every push.
- **One Docker container** (plus an optional TURN relay), configured entirely by environment variables.
- Screen viewing and control, multiple monitors, clipboard sync, file transfer, chat, a Windows "run as administrator" handoff, and an audit log.

## How it works

```
 Technician's browser                 SimpleRemote server                End user's computer
 ───────────────────                  ───────────────────                ───────────────────
 Sign in (OIDC) ───────────────────▶  checks group/email/domain rules
 "New session" ────────────────────▶  issues code 482913     
                                      (single-use, expires in 10 min)
                                                                   ◀──── enters code on the website
                                      stamps server URL + code     ────▶ downloads SimpleRemote-482913.exe
                                      into the agent download            runs it
                                                                   ◀──── agent joins with the code
                                      relays tech's name and org   ────▶ "Allow Jane (IT) to control this computer?"
                                                                   ◀──── user clicks Allow
 WebRTC offer/answer ◀──────────────  signaling relay  ───────────────▶  WebRTC (peer to peer, or via TURN)
 screen, input, files, chat  ◀═══════════════════ encrypted ═══════════▶  screen, input, files, chat
```

The server only relays signaling, and nothing reaches the agent's WebRTC stack until the user has clicked **Allow**. Screen and input data travel peer to peer over DTLS-encrypted WebRTC data channels, through the TURN relay when a direct path isn't possible.

### Built-in safeguards

SimpleRemote is for helping people who asked for help. The design reflects that:

- The agent runs only when the user starts it. It doesn't install a service or stay running after the session ends. "Stay connected after restart" (off unless the tech turns it on) adds only a per-user autostart that rejoins the same session once after a reboot and is removed when the session ends; it never survives the session.
- The user sees the technician's name, email, and organization, plus a warning about unexpected callers, and must click **Allow** before anything is shared. Unanswered prompts are declined after 5 minutes.
- The agent window stays open with an **End session** button for the whole session, and closing it ends the session.
- Codes are single-use, short-lived, and rate-limited against guessing.
- File transfers, clipboard sharing, and administrator requests are shown to the user. Elevation always goes through the normal Windows UAC prompt.
- Clipboard text that a password manager marks as sensitive is never sent.
- Every session and its key events (download, consent, technician joins, files, elevation, end) are recorded in the audit log.
- On macOS the user grants Screen Recording and Accessibility in System Settings. The agent never works around OS permission prompts.

## Quick start (Docker Compose)

1. Copy the files in [`deploy/`](deploy/) to your server and create the config:

   ```bash
   cp .env.example .env
   ```

2. Edit `.env`. At minimum set `PUBLIC_URL`, `SESSION_SECRET`, `TURN_SECRET`, an OIDC provider and allow-list, and/or the backup admin. Generate secrets with `openssl rand -hex 32` and the admin password hash with:

   ```bash
   docker run --rm -i ghcr.io/photoncody/simpleremote hash-password
   ```

   In `.env`, write each `$` in the hash as `$$`, or use `ADMIN_PASSWORD_HASH_FILE`.

3. Start it:

   ```bash
   docker compose up -d
   ```

4. Put it behind a TLS reverse proxy that forwards WebSockets. With Caddy:

   ```
   support.example.com {
       reverse_proxy 127.0.0.1:8080
   }
   ```

   Set `TRUSTED_PROXIES` to the proxy's address so audit logs record real client IPs.

5. For the TURN relay (coturn, host networking), open UDP/TCP 3478 and UDP 49160–49250 on the firewall.

Technicians use `https://support.example.com/tech`; end users go to `https://support.example.com/`.

## Configuration

Everything is an environment variable. Each one can also be read from a file with `NAME_FILE=/path`, which works with Docker secrets. See [`deploy/.env.example`](deploy/.env.example) for a commented template.

| Variable | Default | Description |
| --- | --- | --- |
| `PUBLIC_URL` | *required* | External `https://` URL. Used for OIDC redirects and baked into agent downloads. |
| `ORG_NAME` | `SimpleRemote` | Shown to end users on the website and in the consent prompt. |
| `SESSION_SECRET` | random | 32+ characters. Signs sign-in state. Set it so sign-ins survive restarts. |
| `OIDC_ISSUER` | | Issuer URL of your identity provider. Enables single sign-on. |
| `OIDC_CLIENT_ID` / `OIDC_CLIENT_SECRET` | | OAuth client credentials. Redirect URI: `${PUBLIC_URL}/auth/oidc/callback`. |
| `OIDC_SCOPES` | `openid profile email` | Add `groups` if your provider needs it. |
| `OIDC_AUTO_LOGIN` | `true` | Send techs straight to the identity provider. |
| `OIDC_PROVIDER_NAME` | `Single sign-on` | Sign-in button label. |
| `OIDC_ALLOWED_GROUPS` | | Comma-separated groups allowed in (from the `OIDC_GROUPS_CLAIM` claim). |
| `OIDC_ALLOWED_EMAILS` | | Comma-separated email addresses allowed in. |
| `OIDC_ALLOWED_DOMAINS` | | Comma-separated email domains allowed in. |
| `OIDC_GROUPS_CLAIM` | `groups` | Claim holding group names (Keycloak paths like `/helpdesk` also match). |
| `OIDC_ALLOW_ALL` | `false` | Admit everyone your provider authenticates. One of the allow rules, or this, is required. |
| `ADMIN_USERNAME` | | Backup admin, signed in at `/tech/login/local`. |
| `ADMIN_PASSWORD_HASH` | | bcrypt hash (`simpleremote-server hash-password`). |
| `ADMIN_PASSWORD` | | Plain-text alternative (12+ characters, discouraged). |
| `STUN_URLS` | | e.g. `stun:turn.example.com:3478` |
| `TURN_URLS` | | e.g. `turn:turn.example.com:3478?transport=udp,turn:turn.example.com:3478?transport=tcp` |
| `TURN_SECRET` | | Shared with coturn's `--static-auth-secret`. Short-lived credentials are issued per session. |
| `TURN_CRED_TTL` | max duration + 15m | Lifetime of TURN credentials. |
| `CODE_TTL` | `10m` | How long a new code stays valid. |
| `SESSION_MAX_DURATION` | `4h` | Hard limit on a session's length. |
| `REBOOT_RECONNECT` | `true` | Allow "stay connected after restart": the agent rejoins the same session after the user's computer reboots. |
| `REBOOT_GRACE` | `10m` | How long the server holds a session open while the computer restarts. |
| `TECH_SESSION_TTL` | `12h` | How long a tech stays signed in. |
| `AUDIT_RETENTION_DAYS` | `365` | Days of audit history to keep (`0` keeps it forever). |
| `TRUSTED_PROXIES` | | IPs/CIDRs whose `X-Forwarded-For` is trusted. |
| `LISTEN_ADDR` | `:8080` | |
| `DATA_DIR` | `/data` | SQLite database location. |
| `AGENT_DIR` | `/app/agents` | Agent builds to serve (mount your own signed builds here). |
| `LOG_LEVEL` | `info` | `debug`, `info`, `warn`, or `error`. |

The server refuses to start with an unsafe or incomplete configuration (for example, OIDC with no allow-list) and explains what to fix.

### Identity provider notes

- **Microsoft Entra ID:** issuer `https://login.microsoftonline.com/<tenant-id>/v2.0`. To use groups, add a *groups claim* to the ID token in the app registration. Entra sends group object IDs, so put those IDs in `OIDC_ALLOWED_GROUPS`.
- **Keycloak:** issuer `https://<host>/realms/<realm>`. Add a *Group Membership* mapper (claim name `groups`) to the client.
- **Authentik:** issuer `https://<host>/application/o/<slug>/`. Groups are included in the `groups` claim with the default scope mappings.
- **Google Workspace:** issuer `https://accounts.google.com`. Google doesn't send groups, so use `OIDC_ALLOWED_DOMAINS` or `OIDC_ALLOWED_EMAILS`.

## The agent

| Platform | Download | Notes |
| --- | --- | --- |
| Windows 10/11 (x64, and Arm via emulation) | `SimpleRemote-<code>.exe` | Unsigned builds show SmartScreen: *More info → Run anyway*. |
| macOS 12.3+ (Apple silicon and Intel) | `SimpleRemote-<code>.zip` | Needs Screen Recording and Accessibility permissions. Unsigned apps need *Control-click → Open*. |
| Linux (x86-64, arm64) | `SimpleRemote-<code>` | X11 sessions. Wayland isn't supported yet. |

The server writes the server URL and session code into each download (a small trailer the OS loader ignores), so users only have to run it. If that information is missing, the agent asks for the website and code.

### Code signing

Unsigned agents work, but signed ones are much friendlier for end users. The release workflow signs automatically when these repository secrets are set:

- **Windows:** `WINDOWS_CERT_PFX_BASE64`, `WINDOWS_CERT_PASSWORD`. The server stamps signed `.exe` files inside the Authenticode certificate table, so the signature stays valid (CI checks this).
- **macOS:** `APPLE_CERT_P12_BASE64`, `APPLE_CERT_PASSWORD`, `APPLE_SIGN_IDENTITY`, plus `APPLE_ID`, `APPLE_TEAM_ID`, `APPLE_APP_PASSWORD` for notarization. A notarized app can't be modified, so signed builds also need `MACOS_DEFAULT_SERVER` (your `PUBLIC_URL`). The server then only renames the app to carry the code.

Signing identities belong to your organization, so for signed agents build from your own fork and serve the results with `AGENT_DIR`.

## Development

Requirements: Go 1.26+, Node 24+, and a C toolchain for the agent (on Linux: `libgl1-mesa-dev xorg-dev libxtst-dev libwayland-dev libxkbcommon-dev`).

```bash
make web                     # build the web UI (embedded into the server)
make dev-oidc                # mock identity provider on :9999 (dev only)
PUBLIC_URL=http://localhost:8080 OIDC_ISSUER=http://localhost:9999 \
  OIDC_CLIENT_ID=simpleremote OIDC_CLIENT_SECRET=dev-secret OIDC_ALLOWED_GROUPS=helpdesk \
  DATA_DIR=./data AGENT_DIR=./dist/agents go run ./server/cmd/simpleremote-server
cd web && npm run dev        # optional: UI with hot reload on :5173, proxied to :8080
make agent                   # agent for this OS -> bin/simpleremote-agent
make test test-web           # unit tests
make e2e                     # full session test (needs Xvfb/X11 and xdotool)
```

Repository layout:

```
server/   Go server: config, auth (OIDC + backup admin), sessions & signaling, downloads, audit
agent/    Go agent: consent UI (Fyne), WebRTC (pion), screen capture, input, files, clipboard
web/      Svelte + TypeScript: public code page, tech console, remote viewer, audit log
internal/ Wire protocol and download stamping shared by server and agent
deploy/   Dockerfile, docker-compose.yml with coturn, .env.example
```

## Limitations and roadmap

- One server instance. Live sessions are held in memory, and sessions in progress end on restart.
- The screen is sent as JPEG tiles over a data channel. That works well on broadband; a video codec would use less bandwidth on slow links.
- Windows: the agent can't see the secure desktop (UAC prompts and the lock screen) or send Ctrl+Alt+Del, because those need a system service, which SimpleRemote deliberately doesn't install.
- Linux: Wayland support (via the xdg-desktop-portal RemoteDesktop API) is planned.

## License

[MIT](LICENSE)
