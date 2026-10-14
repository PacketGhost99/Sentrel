<div align="center">

<picture>
  <source media="(prefers-color-scheme: dark)" srcset="docs/assets/banner-dark.png">
  <img src="docs/assets/banner-light.png" alt="Sentrel: monitor everything, own your data" width="760">
</picture>

**Self-hosted monitoring for your servers, sites and network.**<br>
Agents, uptime checks, security, patching and CVEs in one place.

[![CI](https://github.com/XEDigital/Sentrel/actions/workflows/ci.yml/badge.svg)](https://github.com/XEDigital/Sentrel/actions/workflows/ci.yml)
[![CodeQL](https://github.com/XEDigital/Sentrel/actions/workflows/codeql.yml/badge.svg)](https://github.com/XEDigital/Sentrel/actions/workflows/codeql.yml)
[![Release](https://img.shields.io/github/v/release/XEDigital/Sentrel)](https://github.com/XEDigital/Sentrel/releases/latest)
[![Licence](https://img.shields.io/github/license/XEDigital/Sentrel)](LICENSE)
[![Container image](https://img.shields.io/badge/ghcr.io-xedigital%2Fsentrel-blue?logo=docker&logoColor=white)](https://github.com/XEDigital/Sentrel/pkgs/container/sentrel)

<picture>
  <source media="(prefers-color-scheme: dark)" srcset="docs/screenshots/dashboard-dark.png">
  <img src="docs/screenshots/dashboard-light.png" alt="Sentrel dashboard" width="900">
</picture>

</div>

## Why Sentrel

- 🎨 **Clean dashboard and status pages.** Light and dark themes, live beat bars, public or secret-link status pages.
- 🖥️ **Agents for Linux and Windows.** Copy one line from the Agents page and the host appears.
- 🌐 **Uptime checks.** HTTP, TCP, ping, DNS and TLS certificate expiry, plus SNMP switches, firewalls and printers.
- 🛡️ **Intrusion detection built in.** Spots SSH and Windows logon brute-force attempts and port scans (Linux), shows fail2ban jails, and blocks attacking IPs from the UI or automatically.
- 🩹 **One-click patching and CVE scanning.** Pending updates per host, remote upgrades, and CVE matching that tells you exactly what fixes each finding.
- 💽 **Disk full? See why.** When a disk alert fires, Sentrel shows the biggest folders and files and can clean up caches, old logs and old kernels.
- 🔔 **Alerts where you already are.** Email, Slack, Discord, Teams, Telegram, ntfy, Pushover, Gotify and webhooks.
- 🏢 **Multi-tenant for MSPs.** Isolated customers, branding, roles and single sign-on (OIDC, LDAP, RADIUS).
- 🔒 **Secure by design.** Unprivileged agent, signed jobs, and read-only agents that a compromised server cannot command.
- 🪶 **Lightweight.** One Go binary with its own database and time-series store, about 120 MB RAM idle with CVE scanning off; the agent uses about 15 MB. All state lives in one data directory.

## Quick start

> Sentrel is an early release (v0.9). Expect changes before 1.0; see the [changelog](CHANGELOG.md).

**Docker**

```sh
docker run -d --name sentrel --cap-add NET_RAW -p 8080:8080 -v sentrel-data:/data \
  ghcr.io/xedigital/sentrel:latest
```

**Or on Linux** (systemd)

```sh
curl -fsSLO https://github.com/XEDigital/Sentrel/releases/latest/download/install-server.sh
curl -fsSLO https://github.com/XEDigital/Sentrel/releases/latest/download/sentrel-server-linux-amd64
curl -fsSLO https://github.com/XEDigital/Sentrel/releases/latest/download/SHA256SUMS
sha256sum -c SHA256SUMS --ignore-missing
sudo sh install-server.sh --binary ./sentrel-server-linux-amd64
```

Open `http://your-server:8080`, follow the setup wizard, then go to **Agents** to add your first host.
Packages (`.deb`, `.rpm`), Compose, TLS and upgrades are covered in [Installing](docs/install.md).

## Screenshots

<table>
  <tr>
    <td><img src="docs/screenshots/hosts-light.png" alt="Hosts"></td>
    <td><img src="docs/screenshots/host-light.png" alt="Host detail"></td>
  </tr>
  <tr>
    <td><img src="docs/screenshots/checks-light.png" alt="Checks"></td>
    <td><img src="docs/screenshots/vulnerabilities-light.png" alt="Vulnerabilities"></td>
  </tr>
</table>

## Documentation

- [Getting started](docs/getting-started.md): install, add an agent, a check and a notification channel in five steps
- [Installing the server](docs/install.md) and [agents](docs/agents.md)
- [Checks and alerting](docs/alerting.md), [vulnerabilities](docs/vulnerabilities.md), [SSO](docs/sso.md)
- [Security](docs/security.md) and [threat model](docs/threat-model.md)
- [API](docs/api.md), [comparison with other tools](docs/comparison.md), [FAQ](docs/faq.md)
- [Roadmap](ROADMAP.md) and [changelog](CHANGELOG.md)
- [All documentation](docs/README.md)

## Community & support

- Questions and ideas: [GitHub Discussions](https://github.com/XEDigital/Sentrel/discussions)
- Bugs and feature requests: [Issues](https://github.com/XEDigital/Sentrel/issues)
- Security reports: please follow [SECURITY.md](SECURITY.md) and do not open a public issue
- Contributing: [CONTRIBUTING.md](CONTRIBUTING.md)

---

<div align="center">

Apache License 2.0, see [LICENSE](LICENSE).<br>
Built with AI assistance (Claude) and checked by an automated test suite; see the [FAQ](docs/faq.md) for details.

</div>
