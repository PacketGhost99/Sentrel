# Security policy

## Supported versions

Sentrel is pre-1.0. Only the latest release receives security fixes. If you run an older version, upgrade first and check whether the problem still exists.

## Reporting a vulnerability

Please report privately. Do not open a public issue or pull request for a suspected vulnerability.

Use GitHub private vulnerability reporting: on the repository page choose **Security**, then **Report a vulnerability**.

Useful things to include: the version (`sentrel-server version`), how it is deployed (TLS mode, reverse proxy, agent write policy), what you did, what you expected and what happened, and whether the issue needs authentication or a particular role.

## What to expect

This is a small project with one maintainer, so these are targets, not guarantees:

- Acknowledgement within 7 days.
- A first assessment (accepted, need more information, or not a vulnerability) within 14 days.
- Fixes for confirmed issues in the next release, sooner for serious ones. We will agree a disclosure date with you, normally within 90 days of the report, and credit you in the changelog if you want that.

## Scope

In scope: the server, the agent (including the root helper), the web UI, the installers and packages, the release signing process, and the documented security properties in [docs/security.md](docs/security.md) and [docs/threat-model.md](docs/threat-model.md). Examples: authentication or CSRF bypass, access across tenants, privilege escalation from the agent user to root, getting an agent to run a job that was not signed, SSRF guard bypass.

Out of scope, or known and documented:

- Things that require control of the server or its `pki/` directory against agents on the default `server` write policy. That is documented as trusted. Bypassing the `operator` or read-only policies is in scope.
- Plain HTTP mode exposing traffic on the network, `/metrics` being open by default, and clear-text storage of integration secrets in the database. All are documented limits (see the threat model). A way to make them worse than documented is in scope.
- Provider-owned (tenant 0) checks and notifications reaching private addresses; that is intended.
- Findings in the third-party Grype vulnerability database content, or in dependencies without a way to reach them through Sentrel.
- Denial of service by an authenticated administrator, or by flooding an exposed instance without a bypass of the documented rate limits.
- Reports from automated scanners without a demonstrated impact.

Please test only against your own instance.
