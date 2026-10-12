# How Sentrel compares

Sentrel is a young project. Most of the tools below are older, more widely used and better tested, and some are simply a better fit for certain jobs. This page tries to be fair. Statements about other projects are limited to things the author is confident of; where unsure, the cell says "check upstream", because those projects change and I would rather send you to their docs than guess. Last reviewed against the author's knowledge, not against current releases.

## Summary

| | Agent metrics | Uptime checks | SNMP | Patching / updates | CVE scanning | Multi-tenant | Status pages | SSO | Footprint | Maturity |
|---|---|---|---|---|---|---|---|---|---|---|
| **Sentrel** | Yes (Linux, Windows) | Ping, TCP, HTTP, cert, DNS | v2c, v3, YAML profiles | Package inventory and opt-in remote upgrades (Linux packages, Windows Updates) | Linux packages via embedded Grype, opt-in, ~3 GB database | Yes (tenants, roles, branding) | Yes | OIDC, forward-auth, LDAP, RADIUS | Server idle ~120-150 MB RAM, agent ~15 MB (3-host test) | Pre-1.0, new, one maintainer |
| **Uptime Kuma** | No (it is an uptime monitor) | Yes, many types | check upstream | No | No | check upstream | Yes | check upstream | check upstream | Widely used |
| **Beszel** | Yes (hub and agent, Docker stats) | check upstream | check upstream | No | No | check upstream | check upstream | check upstream | Designed to be light; check upstream for numbers | Young, active |
| **Netdata** | Yes, per-second, many collectors | check upstream | Yes (collector available; check upstream for details) | No | No | check upstream | check upstream | check upstream (some features need Netdata Cloud) | Agent per node; check upstream | Mature |
| **Zabbix** | Yes | Yes (simple and web checks) | Yes | Not a patch manager; check upstream | No | check upstream (user groups and host-group permissions exist) | check upstream | check upstream | Server plus a separate database; check upstream | Mature, large estates |
| **Checkmk Raw** | Yes | Yes | Yes | check upstream | No | check upstream | check upstream | check upstream | check upstream | Mature |
| **Prometheus + Grafana** | Via exporters (node_exporter, windows_exporter) | Via blackbox_exporter | Via snmp_exporter | No | No | Grafana has organisations; Prometheus itself has no tenancy | No built-in status pages | Grafana supports several; check upstream | Several components; scales with metric volume | Very mature |

"No" means "not a built-in feature as far as I know"; plugins and add-ons may fill gaps. A column left as "check upstream" is not a claim that the feature is missing.

## What Sentrel is trying to be

A small, single-binary service in which one operator (or an MSP with customers) gets host metrics, uptime checks, SNMP, patch state, CVE matches and status pages from one UI, with an agent that runs unprivileged and a security model that tries to limit what a compromised server can do. It trades breadth, ecosystem and maturity for having those pieces already wired together.

What it is not: a replacement for a metrics platform at scale, a log or trace system, an APM tool, or a mature patch-management product. Remote upgrades run the OS package manager on a host you opted in; there are no staged rollouts, approvals workflow or compliance reporting.

## When to use something else

- **Pure uptime and status pages: Uptime Kuma.** If you only need HTTP/TCP/ping checks and a status page, it is simpler and has more notification and monitor types and a larger community. Sentrel's checks are comparable for the basics but you would be running more than you need.
- **Light host and Docker stats: Beszel.** If you want a small hub with CPU, memory, disk and container charts for a few machines and nothing else, it is a closer fit than Sentrel.
- **Deep, per-second troubleshooting on a node: Netdata.** It has far more collectors and much finer resolution than Sentrel's default 30 second interval.
- **Large metric volumes, custom instrumentation, long retention: Prometheus (plus Grafana).** Sentrel embeds a Prometheus TSDB and exposes PromQL, but it is built for fleets of modest size, not for millions of series, application metrics, federation or remote write. If you already run Prometheus, you can also scrape Sentrel's `/metrics`.
- **Huge enterprise estates, templating and discovery at scale: Zabbix.** It has been run on very large installations for years, with templates, low-level discovery and proxies. Sentrel has not been tested beyond a few hosts in the resource measurements, and has no distributed collectors.
- **Established Nagios-style check ecosystems: Checkmk Raw.** If you want a mature check library with automatic service discovery, it has years of coverage across many device types.
- **Dashboards over many data sources: Grafana.** Sentrel's UI shows Sentrel's data only.
- **Dedicated vulnerability management or patch compliance.** Sentrel matches installed Linux packages against Grype's database; it does not scan containers, application dependencies, or Windows software for CVEs, and it does not produce compliance reports. Use a scanner or patch tool built for that if it is a requirement.
- **You need a vendor or a support contract.** Sentrel is a pre-1.0 project with one maintainer.

## Where Sentrel may be the better fit

- You want agent metrics, uptime checks, patch state and CVEs in one install with a UI, without assembling several tools.
- You run a service for several customers and need strict tenant isolation, per-tenant branding and status pages.
- You care about the agent's blast radius: unprivileged agent, signed jobs, and an optional agent mode that a compromised server cannot use to run commands.
- You have modest hardware (a Raspberry Pi 4 or a 1 GB VPS runs the server with CVE scanning off).
