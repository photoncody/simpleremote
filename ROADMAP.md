# SimpleRemote implementation roadmap

Created: October 15, 2026

## Product mission

SimpleRemote is a self-hosted alternative to ScreenConnect manual sessions: fast, temporary technician access to a PC or VM that does not yet have an RMM agent installed.

The primary workflow is a technician physically at a machine, or on the phone with a client, launching SimpleRemote and completing setup remotely from their normal workstation. Installing the organization's RMM agent is a common successful outcome. Helping friends, family, and other end users remains a supported secondary workflow.

Temporary access may span elevation, logout, and multiple reboots when explicitly enabled on the target. The product should make the technician, granted capabilities, expiration, and cleanup visible. A permanent unattended management agent is outside this roadmap; that role belongs to the RMM.

## How to use this roadmap

Build one numbered item per Codex task, in the order below. Copy the shared instructions and that item's prompt together. Each prompt asks for implementation except item 07, which produces a concrete service design before the separate implementation task.

Start each task by checking the actual code and completed dependencies. This roadmap describes intended behavior, not a claim that every feature is absent. Elevation, reboot reconnect, file transfer, clipboard sharing, and audit history already have implementations to extend.

Each task should update its status below and describe what was delivered, verification performed, and remaining limitations. Do not mark a feature complete based only on compilation when its acceptance criteria require real Windows behavior. A task can leave implementation complete with platform validation pending, explicitly recorded.

## Sequence and dependencies

| Item | Phase | Deliverable | Depends on | Status |
| --- | --- | --- | --- | --- |
| 01 | Foundation | Mission and workflow alignment | — | Complete |
| 02 | Fast access | Reusable technician agent | 01 | Implementation complete; native platform validation pending |
| 03 | Fast access | Agent-generated join code | 02 | Planned |
| 04 | Session organization | Machine labels and notes | 03 | Planned |
| 05 | Access continuity | Explicit temporary access window | 03 | Planned |
| 06 | Access continuity | Reliable initial elevation | 02, 05 | Planned |
| 07 | Windows service | Temporary service architecture | 05, 06 | Planned |
| 08 | Windows service | Temporary service and desktop access | 07 | Planned |
| 09 | Access continuity | Multiple-reboot continuity | 05, 06, 08 | Planned |
| 10 | Provisioning | Reliable installer transfer queue | 04, 06 | Planned |
| 11 | Provisioning | Approved RMM installer workflow | 09, 10 | Planned |

Items 04 and 10 can ship independently once their listed dependencies are complete. Keep the existing client-download and technician-created code workflow operational throughout.

## Shared instructions for every Codex task

Copy this block before the individual feature prompt:

```text
Work in the SimpleRemote repository. Read applicable AGENTS.md instructions and ROADMAP.md, then inspect the current implementation before editing. Follow the user's development-machine and SSH requirements; if the required development VM is unreachable, ask before falling back locally. Do not assume a previous task's local fallback approval applies to this new task.

SimpleRemote's primary mission is fast, temporary technician access to unmanaged PCs and VMs, including setup and RMM installation. Attended support for other people remains supported. Implement only the numbered roadmap item supplied below and prerequisites that are genuinely necessary for it. Preserve existing supported workflows and unrelated uncommitted work.

Use the project's existing Go server/agent, Svelte/TypeScript UI, protocol, SQLite storage, authentication, distribution, and audit patterns. Enforce permissions in the server and agent, not only in UI controls. Keep secrets, session credentials, private infrastructure details, and installer credentials out of logs and public artifacts. Make protocol compatibility and required deployment order explicit when changing messages.

Run relevant automated checks and add meaningful tests for changed behavior, authorization, failure recovery, and cleanup. Do not claim Windows/macOS/Linux runtime validation from cross-compilation. If the required platform is unavailable, implement what can be verified and provide a precise manual validation checklist with the outstanding status recorded.

Update documentation affected by the change and this roadmap item's status. Report delivered behavior, tests actually run, and material limitations. Do not deploy, merge, publish releases, start real migrations, or install an RMM agent on a real machine as part of this task. Use disposable test machines and dummy installers for integration testing. Do not create a PR unless requested in the task's conversation.
```

## 01 — Align the product mission and workflows

**Outcome:** Documentation and user-facing language accurately describe the primary technician workflow and the secondary client-support workflow.

**Scope:** README, relevant agent/website copy, and a concise workflow document. Establish language for portable access, elevation, temporary service access, and expiration. Future functionality must be labeled as planned.

**Acceptance criteria:**

- A new reader can understand the technician-at-machine scenario immediately.
- Existing consent and support instructions still make sense for a client on the phone.
- Documentation distinguishes today's per-user reconnect from future pre-login service access.
- Claims such as “nothing is installed” describe portable mode specifically once service mode exists.

**Codex prompt:**

```text
Implement ROADMAP.md item 01: align SimpleRemote's documentation and workflow language with its primary mission as an alternative to ScreenConnect manual sessions for unmanaged PCs and VMs.

Inspect README.md, SECURITY.md, web/src/pages, and agent/internal/ui. Revise the README introduction and workflow explanation around a technician launching temporary access at a machine, returning to their workstation, performing setup, and optionally installing RMM. Preserve the phone-assisted client workflow. Add docs/WORKFLOWS.md describing both workflows, current capabilities, and planned portable/service modes. Adjust misleading existing UI copy only where today's behavior supports the revised wording.

Clearly label future features as planned. Do not remove existing consent checks or imply current support for the Windows login screen, secure desktop, or an installed service. Validate internal documentation links and review affected screens/copy. Satisfy item 01's acceptance criteria and update its status.
```

### Step 01 delivery notes — October 15, 2026

- Reframed README around temporary technician access to unmanaged PCs/VMs, setup, and manual RMM handoff; retained the client-support workflow.
- Added `docs/WORKFLOWS.md` with both workflows, elevation details, per-user reconnect requirements, cleanup timing, and current versus planned capabilities.
- Updated the security policy and wording in the public code page, technician dashboard, and agent setup/approval screens. Authentication, consent controls, elevation defaults, and session behavior are unchanged.
- Verified all relative links in README, SECURITY, WORKFLOWS, and ROADMAP. Svelte check passed with zero errors/warnings, all seven frontend tests passed, and the production web build passed.
- Linux agent compilation passed; Go formatting and diff whitespace checks passed. Windows/macOS runtime and visual checks were not performed for this wording-only change.

## 02 — Reusable technician agent

**Outcome:** A technician can keep a reusable agent on USB or a network share and launch it without generating a new download for every machine.

**Acceptance criteria:**

- Provide a generic download with the organization's public server URL configured and no session credentials embedded.
- The agent asks for a join code and clearly displays the server it will contact.
- Existing session-specific downloads still work.
- An unavailable/read-only USB or share does not prevent normal operation or cause credential state to be written beside the executable.
- Document unsigned and signed build behavior, including existing macOS restrictions.

**Codex prompt:**

```text
Implement ROADMAP.md item 02: a reusable technician agent.

Inspect agent startup, its existing manual server/code UI and defaultServer support, internal/stamp, server/internal/dist, and download routes. Reuse existing capabilities where possible. Add a discoverable technician-console download for an agent configured with the public server URL but no session code, resume token, or technician authentication secret. Running it should show the configured server and request a code. Preserve session-specific stamped downloads and existing platform signing constraints.

Make execution from a read-only removable drive or network share work without writing session state beside the binary. Validate configurable server URLs using existing production transport rules. Add tests for generic versus session-specific downloads, missing configuration, and credential-free reusable assets. Document the USB/share workflow and satisfy item 02's acceptance criteria.
```

### Step 02 delivery notes — October 15, 2026

- Added authenticated reusable agent downloads and a discoverable technician-console section. Assets carry only the configured public server URL and organization, ask for a fresh code, and retain local consent. Session-specific downloads remain supported.
- Extended the shared stamp format to accept an empty code; validated HTTP(S) URLs without embedded credentials, queries, or fragments at server startup and in stamps, setup, and agent transport. Existing HTTP development deployments remain supported; HTTPS remains recommended for production.
- Reusable downloads reject already-stamped base assets to avoid retaining previous session information. Signed Windows builds use the existing certificate-table stamping path. Signed macOS builds preserve executable bytes and require the matching build-time URL in the signed no-stamp marker; legacy markers still work for session-specific downloads but require rebuilding for reusable downloads.
- Existing reconnect state already uses user configuration storage. Added a read-only-working-directory test covering state permissions and cleanup; no credential storage beside the reusable binary was introduced. Media must remain available for reboot reconnect, and OS executable paging can still depend on it while running.
- Verified Go vet, server/shared tests with race detection, all agent tests, Svelte check (zero errors/warnings), seven frontend tests, production web build, server build, and native Linux agent build. Added focused download/authentication/configuration/signing tests and a headless Fyne setup-form render/check.
- Native Windows/macOS execution, real signature/notarization checks, and USB/network-share session/reboot integration remain pending. See the manual checklist in `docs/WORKFLOWS.md`. Implementation completion does not claim these platform results.
- Deployment compatibility: server-only stamps require updated agents. Deploy updated server/UI with newly built agent assets; no wire-protocol or SQLite migration changes. No deployment or PR was performed for this task.

### Step 02 review follow-up — October 17, 2026

- Normalized trailing slashes when comparing signed macOS marker URLs with the public server URL, preserving signed executable bytes and rejecting genuinely different servers or paths.
- Cached one immutable reusable artifact per platform and serialized preparation across requests. Each response streams through an independent reader; source-build or configuration changes replace the cache entry. Invalid builds still fail before download headers, and missing source files cannot serve stale cached downloads.
- Added regression coverage for signed URLs with trailing slashes and base paths, concurrent reusable readers, cache reuse/replacement, failed-preparation recovery, and HTTP range streaming. Distribution/API race tests, server vet/build, and diff checks passed. A warm-cache benchmark with a 1 MiB artifact measured 560 B/op (5 allocations), rather than a per-request artifact-sized copy. Native signing/media validation remains pending as documented above.

## 03 — Agent-generated join code

**Outcome:** Launch the agent first; enter its displayed code in the technician console to claim the machine.

**Acceptance criteria:**

- An unclaimed agent receives a short-lived code without a technician account on the target.
- An authenticated, authorized technician can claim it atomically; competing claims cannot both succeed.
- The target shows the claiming technician's identity before approving access.
- Separate code types cannot be confused or used against the wrong endpoint.
- Expired codes, disconnects, declines, claim races, and repeated guesses have defined behavior.
- The original technician-created session flow continues to work.

**Codex prompt:**

```text
Implement ROADMAP.md item 03: an agent-first connection flow.

Extend the reusable agent with a clear “Get a connection code” action. The server should register a waiting agent, issue an expiring single-use code, and allow an authenticated authorized technician to enter it in the console and claim that agent. Define protocol messages and session states explicitly. Make claiming atomic, rate-limit code generation and redemption, bound waiting-agent resources, and ensure codes from existing technician-created sessions cannot be redeemed as agent-generated codes.

Show the claiming technician's identity on the target and require local approval before screen capture/control. Keep both connection workflows discoverable. Test unauthorized claims, simultaneous claims, expired/reused codes, malformed messages, disconnect/retry behavior, decline, and the existing workflow. Satisfy item 03's acceptance criteria.
```

## 04 — Machine labels and session notes

**Outcome:** Several machines being prepared at once are easy to distinguish.

**Acceptance criteria:**

- Sessions have an editable friendly label, optional ticket/reference, and plain-text notes.
- Hostname and OS remain available independently of the friendly label.
- Labels appear in the dashboard/viewer and survive into audit history.
- Search/filter helps locate sessions without exposing another technician's unauthorized sessions.
- Editing permissions, input limits, and audit behavior are explicit.

**Codex prompt:**

```text
Implement ROADMAP.md item 04: machine labels, ticket references, and session notes.

Inspect existing session ownership, dashboard, viewer, audit pages, and SQLite migration conventions. Add bounded plain-text fields for a friendly label, optional ticket/reference, and technician notes. Keep discovered hostname separate. Allow authorized edits, persist metadata to session history, and add useful dashboard/history search or filtering. Record who changed metadata without unnecessarily duplicating potentially sensitive note contents in logs.

Use additive schema changes and maintain existing session authorization. Test persistence, edit permissions, validation, hostile text rendering, filtering, and existing unlabeled records. Document the fields and satisfy item 04's acceptance criteria.
```

## 05 — Explicit temporary access window

**Outcome:** A technician can authorize a bounded period of access at the target, including permitted reconnects, with a visible deadline and revoke control.

**Acceptance criteria:**

- Local approval identifies the technician, requested capabilities, and absolute expiration.
- Server policy limits duration; join-code expiry and access-window expiry are separate concepts.
- Reconnect credentials are scoped to the session/authorized technician and expire with the window.
- Extending the window or broadening capabilities requires explicit local approval.
- Ending/revoking the session removes reconnect authorization; expiry works locally while offline.
- Default client support remains a straightforward Allow/Decline workflow.

**Codex prompt:**

```text
Implement ROADMAP.md item 05: explicit temporary access windows.

Inspect existing SESSION_MAX_DURATION, CODE_TTL, resume tokens, consent state, persist state, and audit events. Extend these rather than creating a conflicting authorization mechanism. Add a target-approved access window with a server-configured maximum, clear absolute expiration in target and console UI, and a visible revoke/end control. Separate the time to redeem a join code from the approved time to reconnect.

Bind access and resume credentials to the authorized session and technician. Require fresh local approval for extensions or expanded privileges. Define existing capabilities in the approval model and reserve service access as unavailable until implemented. Enforce expiration in the agent as well as the server, including offline expiry and a documented clock-change policy. Audit grants, extensions, expiry, and revocation without logging credentials.

Test boundary times, reconnect after expiry, revoke during disconnect, extension denial, capability changes, and cleanup. Document that server-restart survival is not yet promised. Satisfy item 05's acceptance criteria.
```

## 06 — Reliable initial elevation

**Outcome:** A technician can choose administrator access at launch and reliably retain the approved session through the UAC handoff.

**Acceptance criteria:**

- Reuse and improve the existing initial administrator checkbox and elevation handoff.
- UAC cancellation leaves a usable unelevated path with an accurate status.
- Exactly one active agent owns the session after successful handoff.
- Approved identity, deadline, and reconnect settings remain intact without exposing tokens in logs.
- Administrator access is clearly distinguished from future secure-desktop/service access.

**Codex prompt:**

```text
Implement ROADMAP.md item 06: dependable administrator access from launch.

Inspect agent/internal/ui's initial administrator option, platform.RelaunchElevated, resume-token handling, and process ownership/cleanup. Improve the existing flow so a technician can explicitly request administrator access during setup or later during a session. Use normal Windows UAC and preserve the approved session identity, access deadline, and reconnect policy through the handoff.

Handle UAC rejection, startup failure, different administrator credentials, duplicate processes, and parent/child exit races. Do not let the old process revoke a successfully transferred session or leave two agents controlling it. Review how handoff credentials are transported and protected. Preserve platform-specific behavior on macOS/Linux. Do not claim secure-desktop access from elevation alone.

Add meaningful automated tests around handoff state and failures. Provide and run, when Windows is available, manual tests for standard/admin users and alternate administrator credentials. Satisfy item 06's acceptance criteria.
```

## 07 — Design the temporary Windows service

**Outcome:** A reviewable architecture for temporary access across logout, reboot, and Windows desktop transitions.

This is a design task. Review its output before starting item 08; it does not install a service.

**Acceptance criteria:**

- Explain the service, interactive desktop worker, and authenticated local IPC responsibilities.
- Evaluate supported Windows APIs for capture/input across user desktops, login, lock, and UAC; identify uncertain behavior requiring a disposable VM spike.
- Define least-privilege boundaries, credential storage/ACLs, session selection, expiry, revocation, and cleanup after failures.
- Define how same-session restart recovery works without resurrecting revoked sessions.
- Give a concrete implementation and Windows validation plan, with explicit unsupported cases.

**Codex prompt:**

```text
Complete ROADMAP.md item 07 as an architecture task. Create docs/TEMPORARY-WINDOWS-SERVICE.md; do not implement or install the service yet.

Inspect current capture/input, elevation, persist, session authorization, and SQLite state. Design an opt-in administrator-approved temporary Windows service plus interactive worker arrangement for access across logout, reboot, and supported secure-desktop transitions. Consult current primary Microsoft Windows API documentation and cite the relevant sources. Explain why running the existing GUI agent directly as a service is insufficient.

Specify process privileges, authenticated IPC and ACLs, worker launch and desktop selection, console/RDP/multiple-user policy, handling of UAC and lock/login desktops, installer location, binary integrity, resumable state, local offline expiry, revocation, server-restart behavior, and idempotent cleanup. Prevent an arbitrary local process or another logged-in user from gaining privileged control through the service. Never weaken UAC or Windows security settings.

Give a failure/recovery matrix and identify technical uncertainties for a disposable Windows VM prototype. Propose scoped server/agent protocol changes and an ordered implementation plan. Mark unsupported behavior honestly, including Ctrl+Alt+Del unless a supported implementation is proven. Satisfy item 07's acceptance criteria and record the design's review status in the roadmap.
```

## 08 — Implement temporary Windows service access

**Outcome:** Optional temporary service access with enforced expiration and cleanup; portable mode remains available.

**Acceptance criteria:**

- Installation requires explicit local administrator approval and shows capabilities and deadline.
- A visible indicator and revoke control appear on supported interactive desktops; secure-screen indicator limitations are documented.
- Privileged IPC, executable paths, and stored credentials are protected with appropriate Windows ACLs.
- Cleanup handles explicit end, expiry, connection loss, reboot, partial installation, and repeated uninstall.
- No remote connection can silently extend the deadline or turn temporary access into indefinite access.
- Real Windows tests establish which login/lock/UAC transitions work; unverified cases remain labeled pending.

**Codex prompt:**

```text
Implement ROADMAP.md item 08 using docs/TEMPORARY-WINDOWS-SERVICE.md. First check the design's review status and resolve recorded blockers or unsupported assumptions. Use disposable Windows testing for desktop-transition prototypes before claiming those capabilities.

Build the explicitly opt-in temporary service, interactive worker, authenticated local IPC, protected session state, and desktop-transition support established by the design. Keep portable mode the default. Use normal administrator authorization, show the access deadline/capabilities, and preserve a visible local revoke path wherever the desktop supports it. Do not disable UAC, change secure-desktop policy, bypass login, or store Windows passwords.

Enforce local expiration and server revocation, implement bounded restart recovery, and perform idempotent cleanup of service registration, workers, temporary executable copies, reconnect state, and credentials. Handle removal of a running executable through a bounded cleanup mechanism. Reject tampered worker/IPC requests and prevent use of the service as a general privileged command launcher.

Test end/expiry/offline expiry, crash and partial-install recovery, failed cleanup, reboot after expiry, cross-user IPC, console versus RDP sessions, logout/login, lock/unlock, and UAC transitions. Document supported Windows versions and exact validated behavior. Satisfy item 08's acceptance criteria; record unavailable platform validation separately.
```

## 09 — Reliable continuity through multiple reboots

**Outcome:** The same approved session survives repeated setup reboots within its access window.

**Acceptance criteria:**

- Portable mode accurately states that reconnect requires the approving user's login.
- Service mode reconnects before login where validated by item 08.
- Console distinguishes confirmed states from inferred/offline states; an offline agent cannot reliably report that it is waiting for login.
- Repeated reboots, network changes, elevation, and reconnect races do not duplicate sessions or expand authorization.
- Service-mode server restart recovery restores only approved, unexpired sessions; portable behavior is documented.
- Revoked/expired sessions cannot return after reboot, agent restart, or server restart.

**Codex prompt:**

```text
Implement ROADMAP.md item 09: reliable continuity across multiple installation reboots.

Inspect current per-user persist/autostart, REBOOT_GRACE, reconnect retry logic, in-memory server sessions, and the temporary service from item 08. Support repeated reconnects to the same approved session until its deadline. Retain clear portable-mode limitations and implement service-mode pre-login reconnect only where validated.

Add useful connection states and last-seen information without presenting guesses as facts. Define and implement persistence/reconciliation for approved service sessions across server restarts, including durable revocation and expiry. Never restore a session merely because an old agent presents stale state. Protect or hash persisted bearer credentials as appropriate; document protocol/deployment compatibility.

Test multiple reboots, long boot/network delays, network changes, agent/service/server crashes, token rotation and replay, simultaneous reconnects, elevation handoffs, revocation while offline, and cleanup. Use a controllable clock in automated lifecycle tests and disposable Windows VMs for boot/login behavior. Satisfy item 09's acceptance criteria.
```

## 10 — Reliable installer transfer queue

**Outcome:** A technician can send several installers or a setup folder and understand what arrived successfully.

**Acceptance criteria:**

- Multiple files and folders have per-file and overall progress, cancel, retry, and visible destination.
- Resume after interruption verifies the destination and existing bytes; partial transfers cannot be launched as complete installers.
- Folder paths cannot escape the approved destination; symlinks/reparse points and overwrite conflicts have explicit handling.
- Completed files have integrity verification and useful disk-space/permission errors.
- Existing download/upload behavior remains available; file transfer does not automatically execute files.

**Codex prompt:**

```text
Implement ROADMAP.md item 10: a reliable installer/file transfer queue.

Inspect agent/internal/files, WebRTC file messages, browser upload/download code, and session viewer. Add queued multi-file transfers and folder selection where supported, with per-file and total progress, explicit destination, cancel, retry, and integrity verification. Preserve a usable fallback for browsers without folder APIs.

Design bounded resumable transfers across connection loss with protected session-scoped metadata and verified partial files. Avoid loading large files entirely into memory. Handle overwrite conflicts, insufficient space, permissions, expired sessions, and cancellation cleanup. Validate destination paths and reject traversal, unsafe symlinks/reparse points, and unapproved destinations. Do not launch transferred files automatically.

Test corrupted chunks, interrupted/resumed transfers, duplicate requests, zero-byte and large files, malicious paths, collisions, cancellation, and expiry cleanup. Document transfer-size/storage limits and what survives browser or agent restarts. Satisfy item 10's acceptance criteria.
```

## 11 — Approved RMM installer workflow

**Outcome:** A technician can select an approved installer, transfer it, run it under the approved access level, and track the handoff to permanent management.

**Acceptance criteria:**

- An operator-managed catalog defines platform/architecture, artifact source, integrity requirements, structured arguments, and elevation needs.
- Only authorized technicians can launch approved entries; a changed executable cannot inherit approval accidentally.
- Use structured process execution, not concatenated shell commands. Protect any enrollment credentials.
- Show transfer, launch, running, exit, reboot-required, failed, and verification states where observable.
- Exit code zero is reported as installer success, not proof of RMM enrollment.
- Verification is vendor-specific or an explicit manual confirmation, with clear uncertainty and timeout behavior.
- End SimpleRemote only after the technician confirms RMM access, or an explicitly enabled verified-handoff policy succeeds.

**Codex prompt:**

```text
Implement ROADMAP.md item 11: approved RMM installer shortcuts and a clear handoff workflow.

Build on the transfer queue, access window, elevation, and reboot continuity. Add an operator-configured installer catalog using existing deployment/config patterns. Each entry should declare supported OS/architecture, an artifact source, integrity verification, structured execution arguments, elevation requirements, supported exit/reboot codes, and optional narrowly scoped enrollment verification. Keep arbitrary remote shell execution outside this feature.

Expose catalog entries only to authorized technicians and enforce permission checks at launch. Tie approval to the exact installer identity and requested privilege. Restrict remote downloads to configured sources with size/time limits, redirect validation, and integrity checks; never accept a target-supplied arbitrary download URL. Handle catalog enrollment secrets without exposing them in web responses, logs, audit details, or session notes, and document unavoidable process-argument exposure when a vendor requires it.

Show transfer/launch/execution/exit/reboot/verification progress, useful errors, and retry behavior without duplicate installs. Distinguish installer completion from confirmed RMM connectivity. Provide manual “RMM access confirmed” handoff; automatic verification must be explicitly configured and accurately describe what it proves. Keep temporary access available until confirmed or expired, then clean up SimpleRemote service/reconnect artifacts while preserving the installed RMM.

Use dummy installers for automated and disposable-VM integration tests covering success, failure, reboot, privilege rejection, tampered artifacts, unauthorized launch, duplicate requests, expired sessions, secret redaction, and handoff cleanup. Do not install a real RMM agent or enroll any real machine. Satisfy item 11's acceptance criteria.
```

## Release gates

These are completion criteria for the relevant milestones, not additional permission requests.

- **Fast-access milestone (01–04):** both agent-first and technician-first workflows work; reusable downloads contain no session credentials; waiting-agent claims are single-use and authorized.
- **Temporary-access milestone (05–09):** expiration/revocation/cleanup have failure-path coverage; the Windows support matrix reflects actual tests; restart recovery cannot revive ended sessions.
- **Provisioning milestone (10–11):** transfers have integrity checks; launch permissions are enforced; installer success is distinct from confirmed enrollment; handoff cleans up temporary access.
- Before a release, validate the supported platforms and existing support workflow, review affected security documentation, and confirm code-signing/distribution behavior for changed agents. Record untested combinations explicitly.

## Later candidates

After the primary workflow is dependable: clearer connection diagnostics, adaptive screen quality, view-only access, pause sharing, annotations, approved technician handoff, and Wayland support. These remain useful, but do not take priority over quickly reaching and preparing an unmanaged machine.
