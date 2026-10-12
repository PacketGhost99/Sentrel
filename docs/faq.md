# FAQ

## Why not Uptime Kuma, Zabbix or something else?

Use whichever fits. Sentrel combines agent-based host monitoring, uptime checks, patch tracking and CVE matching in one small service. See the [comparison](comparison.md) for when another tool is the better choice.

## Does it need a database?

No external one. State lives in a single data directory with a built-in SQLite database and metric store. Backup and restore are in [operations](operations.md).

## Does it run on a Raspberry Pi or ARM?

Yes. The server and the Linux agent are built for amd64 and arm64, and the container image is multi-arch. The server runs on Linux only; agents run on Linux (amd64, arm64) and Windows (amd64). With CVE scanning off, a Raspberry Pi 4 or a 1 GB VPS is enough (see [hardware recommendations](operations.md#hardware-recommendations)).

## How much does it use?

Measured on a 2 vCPU, 3.8 GB VPS monitoring 3 hosts. Your numbers will vary with host count and retention, and there are no figures for fleets of hundreds of hosts. Method and caveats are in [operations](operations.md#resource-usage).

| Item | Measured |
|---|---|
| Server idle memory | about 120 to 150 MB |
| Server peak memory | about 575 MB, while scanning against the vulnerability database |
| Agent memory | 13 to 15 MB (peak 22 MB) |
| Agent CPU | about 0.2% of one core at the default 30 s interval |
| Metric store | roughly 0.5 to 1 MB per host per day |
| Vulnerability database | about 3.1 GB, only if scanning is enabled |
| Binaries | server about 150 MB (UI, agent builds and agent packages embedded), agent about 14 MB |

## Is it production ready? What is the support policy?

Sentrel is pre-1.0. It is young and has not been run at large scale.

- Only the latest release is supported; fixes land there.
- Database migrations run automatically at server start and are never edited after release. There is no automated downgrade: take a backup (`sentrel-server backup`) before upgrading and restore it to roll back.
- Upgrade the server first, then the agents (from the UI). Agents older than the server keep working while the protocol schema version matches. See [upgrading](install.md#upgrading).
- Before 1.0, breaking changes (settings, API, packaging) can happen in any release. They are listed in the [changelog](../CHANGELOG.md) with migration steps. There is no stability promise for the REST API yet.
- Installs from before the rename to Sentrel need a manual migration (data directory, service and agent paths).

## Does it phone home?

No. Sentrel has no telemetry, no analytics, no update check and no phone-home. The server, agent and UI sources were checked for outbound connections. The complete list of places where Sentrel connects out:

- **Vulnerability database download**: only if CVE scanning is enabled (off by default). The embedded Grype library fetches its listing and database from Anchore's public host unless you set a mirror URL in Settings. With scanning off, the database is never downloaded or opened.
- **Agent to your server**: the agent connects to the server URL it was registered with and downloads updates from that same server.
- **Notification channels you configure**: webhook, Slack, Teams, Discord, Gotify, ntfy (defaults to `https://ntfy.sh` if its server field is empty), Telegram, Pushover and SMTP. Nothing is sent unless you create a channel.
- **Checks and targets you configure**: ping, TCP, HTTP, TLS certificate and DNS checks and SNMP polling go where you point them.
- **Identity providers you configure**: OIDC issuer, LDAP, RADIUS.
- **Let's Encrypt**: only if you choose the `acme` TLS mode.
- **Package managers on monitored hosts**: `apt`, `dnf` and the like (or Windows Update) contact their usual repositories. That is the operating system's own traffic.

The web UI loads no external fonts, scripts or other assets (the Inter font is bundled). This is an assertion about code, so you can verify it yourself by watching the server's connections.

## How was it built?

Sentrel was developed with substantial AI assistance (Claude), directed, reviewed and tested by the maintainer. It has a Go test suite (unit tests, route tests that probe every `{id}` route for cross-tenant access, and live-event tenancy tests), an end-to-end script that runs against a real server and real agents, and a security review whose findings were fixed before release. The e2e script is run by hand rather than in CI. Vulnerability reports go through [SECURITY.md](../SECURITY.md).

## Why was Heartbeat renamed?

Elastic already ships an uptime monitor called Heartbeat. Binaries, services, paths, the Go module, the protocol package, cookies, environment variables and metric prefixes were all renamed, so installs made before the rename must be migrated by hand.

## What are the known security limits?

With the default `server` write policy the server is fully trusted by its agents, the database is not encrypted, integration secrets (SMTP, LDAP, RADIUS, webhook tokens) are stored in clear text in it, and backups contain the CA and job keys. Read-only and `operator` agents are not affected by a compromised server. Details are in the [threat model](threat-model.md).
