# SimpleRemote

Self-hosted, **temporary remote access for unmanaged PCs and VMs**, intended as an alternative to ScreenConnect manual sessions. Launch SimpleRemote at a machine that does not yet have your RMM agent, approve the session locally, and finish setup from your workstation's browser. Installing your organization's RMM agent and confirming access is a common next step.

Phone-assisted client support and helping friends or family are also supported: give the person a session code, have them run the downloaded app, and connect after they click **Allow**. In today's portable mode, the agent installs no system service and exits when the session ends. Optional reconnect after restart uses temporary per-user autostart.

- **No accounts for end users.** Technicians sign in with your identity provider (OIDC), with an environment-configured backup admin for when single sign-on is down.
- **Windows, macOS, and Linux** agents, built by GitHub Actions on every push.
- **One Docker container** (plus an optional TURN relay), configured entirely by environment variables.
- Screen viewing and control, multiple monitors, clipboard sync, file transfer, chat, a Windows "run as administrator" handoff, and an audit log.

## Technician workflow

1. Sign in at `/tech` from your workstation and create a session.
2. At the unmanaged PC or VM, run your reusable technician agent and enter the session code, or open the support website, enter the code, and run the session-specific download.
3. Check the technician identity and click **Allow**. On Windows, the initial prompt offers administrator access; Windows UAC asks for confirmation separately.
4. Return to your workstation and open the session viewer to configure the machine, exchange files, or install your RMM software manually.
5. If setup requires a reboot, enable **Stay connected after restart** before restarting. Reconnect currently requires the approving user's login and the original agent executable to remain available.
6. Confirm your RMM access, then end the SimpleRemote session.

For a client on the phone, ask them to perform the download and approval steps while you remain in the technician console. Neither workflow requires a technician account on the target machine.

See [Workflows](docs/WORKFLOWS.md) for current behavior and limitations, and the [implementation roadmap](ROADMAP.md) for planned improvements.

## How it works today

```
 Technician's browser                 SimpleRemote server                Target computer
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

Temporary access starts with local approval, whether a technician is preparing a machine or a client is receiving help:

- Portable mode installs no system service. The agent starts locally and exits when the session ends. "Stay connected after restart" is off until enabled by the technician; it adds temporary per-user autostart to rejoin the same session after login. The agent removes reconnect state when it receives session end or encounters an invalid/expired resume. See [Workflows](docs/WORKFLOWS.md) for offline cleanup and reconnect limits.
- The user sees the technician's name, email, and organization, plus a warning about unexpected callers, and must click **Allow** before anything is shared. Unanswered prompts are declined after 5 minutes.
- A visible session bar (or the agent window where the bar is unavailable) shows the technician and an **End session** button. The full window provides chat and activity details.
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

Technicians use `https://support.example.com/tech`. On the target machine, the technician or client enters the code at `https://support.example.com/`.

## Configuration

Everything is an environment variable. Each one can also be read from a file with `NAME_FILE=/path`, which works with Docker secrets. See [`deploy/.env.example`](deploy/.env.example) for a commented template.

| Variable | Default | Description |
| --- | --- | --- |
| `PUBLIC_URL` | *required* | External `https://` URL without credentials, query, or fragment. Used for OIDC redirects and baked into agent downloads. |
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

### Reusable technician download

The **Reusable technician agent** section in `/tech` offers available platform builds configured with this server's public URL. Keep one on USB or a network share, create a session in the console, then run it at the target and enter the code. The setup screen displays the server before connecting; local consent still identifies the technician and requires **Allow**. These assets contain only the public server URL and organization name, with no join code, resume token, or technician sign-in secret. Downloads require technician authentication.

Execution does not require writing beside the executable. Optional reconnect state is stored in the current user's configuration directory, not on USB or the share. If the medium becomes unavailable, reconnect or relaunch from it cannot work: keep it available at the same path for reboot reconnect, or copy the agent to local storage first. A running process does not need the drive for session-state writes; OS executable paging and network-share availability still apply.

Existing session-specific downloads remain available. Unsigned Linux/Windows and stampable macOS builds receive a server-only stamp for reusable downloads. Windows signed builds use the existing certificate-table stamping path. Signed/notarized macOS builds are not modified: rebuild them with `MACOS_DEFAULT_SERVER` matching `PUBLIC_URL` (including any path; trailing slashes are ignored), using the updated build script that records the URL in the signed no-stamp marker. Legacy empty markers or a mismatched server are rejected for reusable downloads; their existing session-specific download behavior is unchanged. macOS still requires its normal OS permissions; unsigned/ad-hoc builds retain Gatekeeper restrictions.

Server-only stamps require an agent built with roadmap item 02; older agents reject stamps without a code. Deploy updated agent assets with the updated server/UI before offering reusable downloads. No WebSocket protocol or database changes are needed.

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
- Windows: today's portable agent cannot view or control the secure desktop (UAC prompts, lock screen, or login screen), or send Ctrl+Alt+Del. Administrator elevation does not remove these limitations.
- Reconnect after restart is per-user and requires login; it does not provide pre-login access. Server restarts end active sessions.
- Linux: Wayland support (via the xdg-desktop-portal RemoteDesktop API) is planned.

The [roadmap](ROADMAP.md) prioritizes agent-generated join codes, session labels, explicit temporary access windows, more reliable elevation and reboot continuity, installer transfer queues, and an approved RMM installation workflow. An optional temporary Windows service is planned for access across logout, reboot, and supported desktop transitions. It is not implemented; login, lock, and UAC access require platform validation before being promised. Portable mode remains part of the plan.

## License

[MIT](LICENSE)
