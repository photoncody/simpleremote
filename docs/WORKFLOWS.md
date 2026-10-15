# SimpleRemote workflows

## Purpose

SimpleRemote provides fast, temporary access to a PC or VM that does not yet have an RMM agent. The primary workflow is a technician preparing a machine, either physically at it or with a client on the phone, then completing work from their normal workstation. Helping friends, family, and other clients is a supported secondary workflow.

SimpleRemote supplies the temporary connection. Your RMM supplies permanent management once installed and verified. Ending SimpleRemote does not uninstall or revoke access granted by a separate RMM product.

## Primary: prepare an unmanaged PC or VM

1. From your workstation, sign in to the technician console at `/tech` using the configured identity provider or backup administrator account.
2. Create a **New session**. Codes are single-use and expire after 10 minutes by default; the server operator can change this.
3. At the target machine, run a reusable technician agent from USB, a network share, or local storage and enter the code. Alternatively, open the support website, enter the code, and download the session-specific agent. If you are on the phone with a client, ask them to use that website workflow.
4. Run the downloaded agent, verify the technician name, email, and organization, and click **Allow**. When connecting to your own technician session, verify your own identity. No technician sign-in is required on the target.
5. On Windows, the approval screen offers administrator access with its checkbox initially selected. Windows asks for confirmation through normal UAC. Declining UAC leaves the session at normal privileges. Administrator access can also be requested later.
6. Open the session viewer from your workstation. Configure the machine, transfer installers, or install your RMM software manually using the remote desktop. Approved installer shortcuts and automatic enrollment verification are not available yet.
7. If you need a reboot, follow the reconnect requirements below before restarting.
8. Confirm you can reach the machine through your RMM, then end the SimpleRemote session using the console or target's **End session** control.

Create the session in the console before joining from the target; agent-generated codes remain planned. Download reusable agents from **Reusable technician agent** in `/tech`. They display the configured server and ask for a code each time; no session credentials are embedded. The existing website/code download workflow remains supported.

Reusable agents can run from a read-only USB drive or share without writing credentials beside the executable. Optional reconnect state goes in the user configuration directory (Windows `%APPDATA%/SimpleRemote`, macOS `~/Library/Application Support/SimpleRemote`, Linux `$XDG_CONFIG_HOME/SimpleRemote` or `~/.config/SimpleRemote`). Reconnect needs the executable at its original path and the medium available after login. Copy to local storage before removing USB or disconnecting the share; an unavailable executable cannot be launched, and the OS may need its backing file even while running. A failed state write is reported when enabling reconnect; ordinary sessions do not require reconnect storage.

Signed macOS reusable assets require a matching build-time server URL and the new signed URL marker; legacy empty markers and mismatches fail download. The [README code-signing and distribution instructions](../README.md#reusable-technician-download) describe unsigned, Windows signed, and macOS signing restrictions.

## Secondary: help someone remotely

1. Sign in to `/tech`, create a session, and give the person the support website and code over your existing trusted conversation. **Copy instructions** in the console provides these details.
2. Ask them to download and run the agent, check your identity, and click **Allow**. They do not need an account.
3. Explain any administrator prompt, file transfer, or clipboard sharing before using it. Stay in contact while working.
4. Use the browser viewer for screen control, monitor selection, file transfer, clipboard text, and chat. The target has a visible session bar or window with an **End session** control and can open chat/activity details.
5. End the session when finished. The portable agent exits; the downloaded executable remains a file the person can remove.

Unexpected callers should not be granted access. Both workflows retain local approval and the technician's visible identity.

## Current elevation and reboot behavior

### Administrator elevation

On Windows, the initial approval prompt offers elevation, and the technician can request it during an active session. The agent relaunches through normal UAC and rejoins the same session. Someone at the target must respond to UAC as necessary; elevation does not let the portable agent view or control UAC's secure desktop, the lock screen, or the login screen. Ctrl+Alt+Del is not supported.

### Reconnect after restart

The server can allow this capability with `REBOOT_RECONNECT` (enabled by default). It is off for an individual session until the technician enables **Stay connected after restart**. The target displays that reconnect is enabled.

- Enable it before rebooting. The agent writes temporary resume state and creates a per-user autostart entry; it does not install a system service.
- Keep the original executable at its original path. Moving/deleting it or removing its USB drive can prevent reconnect.
- The same approving user must log in again. The current agent cannot provide access before login or as another user.
- The server holds the disconnected agent's session for `REBOOT_GRACE` (10 minutes by default). The session's overall hard limit still applies: `SESSION_MAX_DURATION` defaults to four hours from session creation. Keep the technician viewer connected; technician disconnect limits still apply.
- The agent re-arms reconnect after a successful resume while the option remains enabled. It is limited to the active session, not an indefinite access mechanism. More robust multiple-reboot continuity is planned.
- Ending the session or disabling reconnect removes the temporary arrangement when the agent receives the change. An offline target cannot immediately receive it. On a later launch, an invalid or expired resume is cleaned up and access is refused. Reconnect does not survive a SimpleRemote server restart because live sessions are currently held in memory.

## Current platform and deployment limits

| Area | Current behavior |
| --- | --- |
| Windows | Portable agent with optional administrator elevation; no secure-desktop, lock-screen, login-screen, or Ctrl+Alt+Del control. |
| macOS | Requires Screen Recording and Accessibility permission through System Settings. |
| Linux | Requires X11; Wayland is not supported yet. |
| Connection | Screen, input, and files use encrypted WebRTC, directly or through a configured TURN relay. |
| Server | One instance; restarting it ends active sessions. SQLite retains audit history. |
| Cleanup | Portable mode installs no system service. Optional per-user reconnect state/autostart is session-bound; downloaded files and separately installed software remain. |

## Planned improvements

The [roadmap](../ROADMAP.md) describes proposed features and implementation order. None of the following should be read as already delivered:

- Agent-generated join codes.
- Machine labels, ticket references, and notes.
- Explicit locally approved temporary access windows and improved elevation reliability.
- An optional temporary Windows service for supported access across logout, reboot, and desktop transitions. Login, lock, and UAC behavior needs Windows validation; no secure-desktop capability is promised yet.
- More reliable continuity across multiple reboots and server restart recovery for approved service sessions.
- Resumable installer transfer queues and approved RMM installation/handoff workflows.

Portable mode will remain available. Future temporary service mode must have explicit administrator approval, bounded access, visible status where supported, and cleanup; it is not a permanent RMM replacement.

See the [README](../README.md) for deployment and configuration and the [security policy](../SECURITY.md) for the current access model and vulnerability reporting.

## Reusable-agent platform validation checklist

Automated checks cover server-only stamps, authenticated distribution, missing builds, invalid URLs, signed macOS payload preservation, the manual setup form, and user-config storage from a read-only working directory on Linux. These checks do not establish native Windows/macOS runtime or signature validity.

Before release, use disposable targets:

1. Windows: run unsigned and Authenticode-signed reusable downloads from a read-only USB/share; verify the displayed server, blank code, Allow/Decline, successful session, signature validity, and no session files beside the executable. Repeat the session-specific download flow.
2. macOS: test stampable and signed/notarized builds, normal Gatekeeper and OS permission prompts, the blank-code setup, and session-specific bundle naming. Verify signature/notarization after download. Confirm legacy or mismatched signed configuration fails reusable download visibly.
3. Linux X11: launch the built reusable agent from read-only media, enter a new console code, approve and end a session, and check no state is written to the medium. Repeat from a network share.
4. Enable reconnect using a disposable session: verify state lives in the user configuration directory and is removed on end. Keep the executable available for the reboot test; then verify missing media prevents relaunch without putting credentials on that media. Do not treat this as pre-login service access.
