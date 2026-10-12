# Sentrel

Sentrel is a self-hosted infrastructure monitor. One Go server binary (with the React UI, a time-series store, a vulnerability matcher and an internal certificate authority built in) plus one small Go agent for Linux and Windows. SNMP devices and agentless targets are monitored from the server.

- Single static binaries, no external database: state lives in SQLite and an embedded Prometheus TSDB under one data directory.
- One TCP port serves the web UI, the REST API, the agent gRPC stream, installers and `/metrics`.
- Agents connect outbound only. Remote actions (upgrades, IP blocks, scripts) are off by default, signed by the server and verified by the agent.
- Apache-2.0 licensed.

## Features

**Monitoring**
- Agent metrics for Linux and Windows: CPU (incl. iowait/steal), memory, swap, load per core, disks and inodes (with a "disk full in N hours" forecast), disk and network throughput, network errors, temperatures, processes, services, listening ports, Docker containers, uptime.
- Server-side checks: `ping`, `tcp`, `http` (status, keyword/regex, latency limit, basic auth, optional certificate expiry), `cert` (expiry with 30/14/7 day escalation), `dns` (A/AAAA/CNAME/MX/TXT/NS with expected value).
- Agent-evaluated checks: `service`, `process`, `port`, `container`.
- SNMP v2c and v3 devices with YAML device profiles (generic, net-snmp, Cisco, Fortinet, HP/Aruba, MikroTik, Ubiquiti built in), interface counters, LLDP neighbours, custom profiles and extra OIDs.
- Agentless "target" hosts that are monitored only by checks.
- Public or token-protected status pages; daily uptime history.
- Prometheus-format `/metrics` and a tenant-scoped PromQL query API.

**Alerting**
- Thresholds with warning/critical levels and "N consecutive samples" confirmation, inherited global to tenant to group to host, with per-instance overrides (for example one mount point).
- Flap detection, grouped notifications, repeat reminders, acknowledgements, maintenance windows (one-off or recurring, with optional job scheduling).
- Channels: generic webhook (Go template body), Discord, Slack, Microsoft Teams, Telegram, ntfy, Gotify, Pushover, email (SMTP).

**Security**
- Package inventory per host (apt, dnf/yum, zypper, pacman, apk; installed applications and pending updates on Windows).
- Vulnerability matching of Linux package inventories with the embedded Grype engine, with severity, CVSS, EPSS, KEV, fix availability, ignore/reopen, schedules and alerts. Works air-gapped with a mirrored database.
- SSH (Linux) and failed-logon (Windows) brute-force detection, fail2ban status, manual and automatic IP blocking (nftables, iptables or Windows Firewall).

**Operations**
- Remote package upgrades (with dry run), IP blocks, optional scripts and agent self-update, all as signed jobs that must be enabled per host.
- Agent self-update with signature verification, self-test and automatic rollback.
- Push install of agents over SSH or WinRM.
- Backup and restore from the CLI or the UI.

**Access and tenancy**
- Roles (admin, operator, viewer, tenant admin, tenant viewer), multi-tenant isolation with per-tenant branding.
- Local users, LDAP and RADIUS sign-in, TOTP and passkeys (WebAuthn), optional 2FA enforcement, lockout, audit log.
- REST API with API keys, OpenAPI document at `/api/v1/openapi.json`.

## Quick start

### 1. Install the server

**Linux with systemd** (installs to `/usr/local/bin`, data in `/var/lib/sentrel`, runs as the `sentrel` user):

```sh
sudo sh install-server.sh --binary ./sentrel-server-linux-amd64
# or: sudo sh install-server.sh --url https://example.com/sentrel-server-linux-amd64 --sha256 <hex>
```

The script prints the URL and the one-time setup token when it finishes.

**Docker Compose** (builds the image from this repository):

```sh
docker compose -f deploy/docker-compose.yml up -d --build
docker compose -f deploy/docker-compose.yml logs sentrel | grep -i "setup token"
```

### 2. First-run setup

Open `http://<server>:8080/`. Enter the setup token (printed in the server log and stored in `<data dir>/setup-token`; it is deleted once setup completes), then create the administrator, choose the public URL and a TLS mode. See [docs/install.md](docs/install.md) and [docs/tls-and-proxies.md](docs/tls-and-proxies.md).

### 3. Add agents

In the UI choose **Add host** to create a one-time enrolment token and copy the command it shows.

**Linux** (systemd required; amd64 and arm64):

```sh
curl -fsSL https://monitor.example.com/install/agent.sh | sudo sh -s -- --server https://monitor.example.com --token ABCD-1234-EFGH
```

**Windows** (elevated PowerShell, amd64):

```powershell
& ([scriptblock]::Create((irm https://monitor.example.com/install/agent.ps1))) -Server https://monitor.example.com -Token ABCD-1234-EFGH
```

**Windows MSI** (silent, for deployment tools):

```powershell
msiexec /i sentrel-agent-<version>-x64.msi SERVER=https://monitor.example.com TOKEN=ABCD-1234-EFGH /qn
```

> `scripts/build.sh` produces the MSI from `deploy/agent.wxs` when `wixl` (msitools) is installed; builds without it have no MSI.

Other options (SSH/WinRM push install, manual `sentrel-agent register`) are in [docs/agents.md](docs/agents.md).

### 4. Add SNMP devices and targets

**Add host** also offers SNMP devices (v2c or v3, polled by the server) and agentless targets (ping/TCP/HTTP checks only). See [docs/snmp.md](docs/snmp.md).

## Architecture

```
  Browser ──HTTPS/HTTP──┐
  API clients ──────────┤            ┌──────────────────────── sentrel-server ───────────────────────┐
  Prometheus ───────────┤            │  REST API + UI + /metrics   gRPC AgentService (same port)       │
                        ├── :8080 ───┤  alert engine · notifier · check runner · SNMP poller · jobs    │
  sentrel-agent ──────┘  (outbound │  vulnerability matcher (Grype)   internal CA (mTLS, job keys)   │
   gRPC over HTTP/2        from agent)│  SQLite (WAL)   Prometheus TSDB   data directory                │
                                      └──────────────────────────────────────────────────────────────────┘
```

- **Server** (`cmd/sentrel-server`): everything in one process. Plain HTTP (with HTTP/2 cleartext), self-signed (internal CA) or Let's Encrypt TLS on one port; gRPC requests are routed by content type.
- **Agent** (`cmd/sentrel-agent`): holds a long-lived gRPC stream to the server, sends metrics, state, inventory and security reports, and executes signed jobs. On Linux it runs as an unprivileged user and calls a root helper (`sentrel-agent priv ...`) through a narrow sudo rule.
- **Trust**: the server creates an internal CA at first start. Agents enrol once with a one-time token, receive a client certificate, and the server signs jobs and agent binaries with an Ed25519 key whose public half is delivered at enrolment. Details in [docs/security.md](docs/security.md).
- **Data**: `sentrel.db` (settings, hosts, alerts, audit, findings), `tsdb/` (metrics), `pki/` (CA and keys), `vulndb/` (Grype database, around 3 GB).

## Documentation

| Document | Contents |
|---|---|
| [docs/install.md](docs/install.md) | Server install, upgrade, uninstall, data directory, systemd, Docker, ports |
| [docs/tls-and-proxies.md](docs/tls-and-proxies.md) | TLS modes, reverse proxies (Caddy), `trust_proxy`, recovery |
| [docs/agents.md](docs/agents.md) | Agent install, data collected, privilege model, self-update, troubleshooting |
| [docs/security.md](docs/security.md) | Threat model and controls |
| [docs/sso.md](docs/sso.md) | Single sign-on: OpenID Connect (Authentik, Authelia, Keycloak, Google, Entra) and forward-auth headers |
| [docs/alerting.md](docs/alerting.md) | Thresholds, checks, flapping, maintenance, notification channels |
| [docs/snmp.md](docs/snmp.md) | SNMP devices and writing profiles |
| [docs/vulnerabilities.md](docs/vulnerabilities.md) | Vulnerability matching, database, air-gapped mirror |
| [docs/api.md](docs/api.md) | REST API, API keys, Prometheus endpoint |
| [docs/operations.md](docs/operations.md) | Backup, restore, password reset, retention, upgrades |
| [docs/development.md](docs/development.md) | Repository layout, build, tests, protobuf |
| [CONTRIBUTING.md](CONTRIBUTING.md) | Contribution rules and DCO sign-off |

## Renamed from Heartbeat

This project was previously named Heartbeat. Binaries, services, paths, the Go module, the protocol package, cookies, environment variables and metric prefixes were all renamed to Sentrel. Installs made before the rename must be migrated by hand (data directory, service and agent paths).

## License

Apache License 2.0, see [LICENSE](LICENSE). Third-party components and their licences are listed in [NOTICE](NOTICE).
