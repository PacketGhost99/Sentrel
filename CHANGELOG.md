# Changelog

All notable changes are listed here. The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/). Sentrel is pre-1.0, so any release may contain breaking changes; they are called out under "Changed" or "Removed" with migration notes.

## [Unreleased]

First public release (0.9.x). Nothing has been tagged yet; the entries below summarise the development history.

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

### Changed

- Renamed the product from Heartbeat to Sentrel (Elastic already ships a monitor called Heartbeat). Binaries, services, paths, the Go module, the protocol package, signing contexts, cookies, environment variables, the Windows service and MSI (new UpgradeCode) and the metric prefix all changed. Installs made before the rename must be migrated by hand.
- Vulnerabilities are counted per CVE per host, not per binary package.
- Agent service install is idempotent, so MSI reinstall and repair work.

### Removed

- Remote script execution: the `script` job type, the `scripts` permission, the per-host `allow_scripts` option (column dropped by migration 00009) and the Run script UI. Agents now reject `script` jobs as an unknown job type. Package upgrades, IP blocking, disk cleanup, kernel removal and agent updates are unchanged.
- Push install over SSH and WinRM, including the `/push/*` API routes, the `push_installs` table (dropped by migration 00006) and the SSH and WinRM libraries. Install agents with the one-liner or a package from the Agents page instead. `--insecure-download` / `-InsecureDownload` still work for old scripts but are no longer generated.

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
