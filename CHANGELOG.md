# Changelog

All notable changes are listed here. The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/). Sentrel is pre-1.0, so any release may contain breaking changes; they are called out under "Changed" or "Removed" with migration notes.

## [Unreleased]

### Changed

- CI and release workflows use the current major versions of their GitHub Actions (Node 24 runtimes).

## [0.9.2] - 2026-10-14

### Added

- `sentrel-loadtest` (`cmd/sentrel-loadtest`): a development tool that simulates many agents against a test server, and a Capacity section in the operations docs with measured CPU, memory and API latency from 50 to 4,000 simulated agents.
- Push (heartbeat) checks: a new `push` check type with a secret URL, `GET`/`POST /api/push/{token}?status=up|down&msg=...`. The check goes down when no beat arrives within the interval plus grace (defaults 3600 s and 300 s). Tokens are 192 random bits, looked up by hash and compared in constant time, unknown tokens all return 404, and requests are rate-limited per token and per client. The editor shows the URL, `curl` examples and a Regenerate button; API `POST /checks/{id}/regenerate-token`.
- Dependencies: hosts and checks can name a parent host or check in the same tenant (cycles are rejected). While the parent is down, dependent alerts are still raised and shown as "suppressed: parent X is down" but are not notified, including repeats. "Depends on" pickers in host Settings and the check editor; `parent_type`/`parent_id` on host and check updates.
- Repeat notification help text: the per-channel repeat interval (and the global default in Settings, Alerting) now documents that repeats stop on acknowledgement, resolution, muting and suppression.
- Disk health for Linux agents: SMART (through the new root helper verb `priv smart`, needs smartmontools), software RAID from `/proc/mdstat` and ZFS pools, collected every 30 minutes. A Disk health card on the host System tab, SMART metrics, and alerts for failed SMART, a rising bad-sector count, NVMe wear at 90% or more, degraded md arrays and ZFS pools that are not online. API `GET /hosts/{id}/disk-health`. Windows is not covered.
- Update schedules (auto-patching): recurring daily, weekly, monthly or maintenance-window upgrade jobs per scope, security updates only by default, never rebooting. Hosts with upgrades off, read-only agents and operator-signed agents are skipped and listed in the last run. A maintenance-window schedule queues at most one job per host every 12 hours. Jobs, Schedules now holds both update and scan schedules; API `/update-schedules`.
- CI security scanning: CodeQL, gosec, govulncheck and `npm audit` on every push and weekly, and Dependabot for Go modules, npm and GitHub Actions.

### Changed

- Less cluttered web UI:
  - Explanations moved behind (i) tooltips.
  - Host security, upgrade and cleanup settings moved into a Configure panel, as one compact row each with a Default or Custom marker.
  - Global Settings has a grouped section list, and sign-in providers hide their fields until enabled.
  - The dashboard has a single Health card.
  - Stat cards are equal height.
  - Chart legends use readable series names.
  - Host lists, host pages and dashboards fit phone screens.
- Go 1.26.9.
- Dependabot no longer opens major-version updates; those are done by hand with testing.

### Fixed

- Exported chart PNGs were blank: the image was captured before the chart had drawn.
- Restoring a backup caps the size of each restored file.

## [0.9.1] - 2026-10-13

### Added

- Update notifications: the server checks the latest GitHub release every 6 hours (and on demand) and shows a green "Update vX.Y.Z" pill next to the version in the sidebar and a one-time dismissible banner, both for administrators only. Settings, Updates shows the release notes, the install method and a "Check now" button. The check sends only a `User-Agent` and an ETag; turn it off in Settings, Updates or with `SENTREL_DISABLE_UPDATE_CHECK=1`.
- One-click, signed server updates for systemd installs. The server downloads and verifies the release, then a root-owned updater (`sentrel-updater.path` and `.service`, installed by `install-server.sh`) re-verifies the Ed25519 signature with the key compiled into the installed binary, refuses downgrades, swaps the binary, restarts, checks health for up to 90 seconds and rolls back on failure. Disable with `SENTREL_DISABLE_SELF_UPDATE=1`. Docker and package installs show the exact `docker pull` or `apt`/`dnf` commands instead.
- Release `SHA256SUMS.sig` verification shared with `sentrel-sign` in `internal/release`; the public key is the trust root and cannot be overridden at run time.
- API: `GET /server/updates`, `POST /server/updates/check`, `POST /server/updates/install`, `GET /server/updates/status`; audit entries `server.update_requested`, `server.update_applied`, `server.update_rolled_back`.
- `scripts/dev-deploy.sh` defaults to the next patch version after the latest tag with `-dev` and installs the updater units.

### Changed

- Documentation no longer says Sentrel never calls out: the version check is the one automatic outbound request, and it can be disabled.

## [0.9.0] - 2026-10-13

First public release.

### Added

- Monitoring server, agent and web UI: one server binary with embedded UI, SQLite state, embedded Prometheus time-series store and internal certificate authority; agents for Linux (amd64, arm64) and Windows (amd64) that connect outbound over gRPC.
- Agent metrics, services, listening ports, processes, Docker containers, package inventory and security events; server-side checks (ping, TCP, HTTP, certificate expiry, DNS) and agent-evaluated checks (service, process, port, container); SNMP v2c/v3 with YAML device profiles; agentless target hosts.
- Alerting: inherited thresholds, flap detection, grouping, acknowledgements, maintenance windows, and notification channels (webhook, Discord, Slack, Teams, Telegram, ntfy, Gotify, Pushover, email).
- Public and token-protected status pages, Prometheus `/metrics`, tenant-scoped PromQL API, REST API with API keys and OpenAPI document.
- Multi-tenancy with roles, per-tenant branding, LDAP and RADIUS sign-in, TOTP and passkeys, audit log.
- Signed remote jobs (package upgrades, IP blocks, agent self-update with rollback), each off by default per host.
- Backup and restore CLI and UI.
- Documentation, CONTRIBUTING with DCO, GitHub Actions CI and signed releases.
- Windows agent fixes (read-only service enumeration, per-user installed applications, clearer message when the Security log is unreadable) and an MSI installer built from `deploy/windows/agent.wxs` when `wixl` is available.
- Redesigned web UI: light theme by default with dark toggle, beat bars and uptime pills for hosts and checks, refreshed dashboard, self-hosted Inter font.
- Port-scan detection on Linux (nftables): a detection-only kernel chain counts SYNs to closed ports per source, the agent polls the result, and flagged sources raise a `portscan` alert and can be auto-blocked. Windows is not yet supported.
- Per-rule notification muting and a host toggle for failed-logon alerts.
- Disk usage analysis (run automatically when disk alerts fire) and opt-in disk cleanup jobs for Linux and Windows.
- Vulnerability findings show the available fixed version and can queue an upgrade to it.
- Agent write policy (`server`, `operator`, read-only), set only on the host. Operator-signed jobs use a second Ed25519 signature made offline or in the browser (`sentrel-sign`).
- OpenID Connect login and forward-auth header login.
- Optional CVE scanning: Grype matching is off by default, and the database is only downloaded when you enable it.
- Agents page (`/agents`): create an enrolment token with a write policy, pick a platform and architecture, and copy a one-liner or download the `.deb`, `.rpm` or `.msi` from this server with the exact follow-up commands. Shows agents by version and write policy and lists outdated hosts with an Update action. `GET /api/v1/agents/install-info` and `GET /download/agent/pkg/{file}` back it; `scripts/build.sh` embeds the agent packages (not the server ones) with a manifest when `nfpm` and `wixl` are present.
- Self-signed TLS mode: generated install commands pin the server certificate (`curl --pinnedpubkey`, `install-agent.sh --pin-sha256`, `install-agent.ps1 -PinSha256`) instead of disabling certificate checks.
- `.deb` and `.rpm` packages for the server and agent, a multi-arch container image on GHCR, cosign-signed release checksums and SPDX SBOMs.
- Resource usage measurements in `docs/operations.md`.
- `SECURITY.md`, `docs/threat-model.md`, `docs/comparison.md`.

### Security

Fixes from an internal security review:

- The Linux privileged helper no longer follows symlinks when writing or changing ownership of state as root (agent user to root escalation).
- The Windows agent data folder is restricted to SYSTEM and Administrators; updates are staged in a protected directory and verified from a private copy.
- SSRF guard for tenant-owned checks and notification channels, applied after DNS resolution.
- A trusted-proxy list for client IP and scheme headers.

- Replay state for agent hellos and job IDs persists across restarts.
- Disk usage analysis is limited to mount points; logout cookie flags corrected; job-cancel events carry tenant tags; hardened systemd unit for the server.
- Added a test that calls every `{id}` route as a user from another tenant, and a live-event tenancy test.

[Unreleased]: https://github.com/PacketGhost99/Sentrel/commits/main
