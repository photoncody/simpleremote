# Security policy

SimpleRemote provides temporary technician access to unmanaged PCs and VMs, including machine setup and RMM installation, as well as client support. Access requires local approval in the current portable agent. Security reports are taken seriously.

## Current access model

- Technician sign-in and session ownership restrict who can connect. The person at the target checks the technician identity and approves access before screen sharing or control begins.
- Windows elevation uses the normal UAC prompt. The portable agent cannot control the secure desktop or provide access before login.
- Optional reconnect after restart stores temporary session state and adds per-user autostart. It requires the approving user's login, remains bound to the session, and is removed when the agent processes session end or an invalid/expired resume. An offline machine cannot immediately receive server-side revocation.
- No system service is installed by the current agent. A temporary Windows service and explicit access-window approval are planned in the [roadmap](ROADMAP.md), not current capabilities.

See [Workflows](docs/WORKFLOWS.md) for operational limits. Installing a separate RMM agent is an explicit technician action and gives that product its own access lifecycle.

## Reporting a vulnerability

Please **don't open a public issue**. Use GitHub's private vulnerability reporting instead: open the repository's **Security** tab and choose **Report a vulnerability**. Include steps to reproduce and the version or commit you tested.

You should get a reply within a week. Once a fix is released, the advisory is published with credit, if you want it.

## Scope

In scope: the server, the agent, the web UI, and the deployment files in this repository. Examples include authentication or authorization bypass, ways to connect to an agent without the user's consent, code reuse or guessing, cross-site attacks on the tech console, and stamping or download tampering.

Out of scope: social engineering of end users. The consent prompt and warnings are the defense there, and suggestions to improve them are welcome as regular issues.
