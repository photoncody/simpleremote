# SimpleRemote workflows

## Purpose

SimpleRemote provides fast, temporary access to a PC or VM that does not yet have an RMM agent. The primary workflow is a technician preparing a machine, either physically at it or with a client on the phone, then completing work from their normal workstation. Helping friends, family, and other clients is a supported secondary workflow.

SimpleRemote supplies the temporary connection. Your RMM supplies permanent management once installed and verified. Ending SimpleRemote does not uninstall or revoke access granted by a separate RMM product.

## Primary: prepare an unmanaged PC or VM

1. From your workstation, sign in to the technician console at `/tech` using the configured identity provider or backup administrator account.
2. Create a **New session**. Codes are single-use and expire after 10 minutes by default; the server operator can change this.
3. At the target machine, open the support website, enter the code, and download the agent for its operating system. If you are on the phone with a client, ask them to do this.
4. Run the downloaded agent, verify the technician name, email, and organization, and click **Allow**. When connecting to your own technician session, verify your own identity. No technician sign-in is required on the target.
5. On Windows, the approval screen offers administrator access with its checkbox initially selected. Windows asks for confirmation through normal UAC. Declining UAC leaves the session at normal privileges. Administrator access can also be requested later.
6. Open the session viewer from your workstation. Configure the machine, transfer installers, or install your RMM software manually using the remote desktop. Approved installer shortcuts and automatic enrollment verification are not available yet.
7. If you need a reboot, follow the reconnect requirements below before restarting.
8. Confirm you can reach the machine through your RMM, then end the SimpleRemote session using the console or target's **End session** control.

You currently need to create the session in the console before joining from the target. Agent-generated codes and a dedicated reusable technician download are planned. An unstamped agent already has manual website/code entry; that is not yet the planned reusable-download workflow.

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

- Reusable technician downloads and agent-generated join codes.
- Machine labels, ticket references, and notes.
- Explicit locally approved temporary access windows and improved elevation reliability.
- An optional temporary Windows service for supported access across logout, reboot, and desktop transitions. Login, lock, and UAC behavior needs Windows validation; no secure-desktop capability is promised yet.
- More reliable continuity across multiple reboots and server restart recovery for approved service sessions.
- Resumable installer transfer queues and approved RMM installation/handoff workflows.

Portable mode will remain available. Future temporary service mode must have explicit administrator approval, bounded access, visible status where supported, and cleanup; it is not a permanent RMM replacement.

See the [README](../README.md) for deployment and configuration and the [security policy](../SECURITY.md) for the current access model and vulnerability reporting.
