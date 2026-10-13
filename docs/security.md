# Security model

This page describes what Sentrel protects, how, and where the limits are. To report a vulnerability, contact the maintainers privately rather than opening a public issue.

## Threat model

| Asset | Main threats | Controls |
|---|---|---|
| Server and its CA keys | remote compromise, stolen backup | one port, hardened systemd unit, restrictive file modes, authenticated API |
| Monitored hosts | a compromised server pushing malicious jobs; a stolen network position | per-host opt-in for remote actions, signed and expiring jobs, agent-side write policy (read-only or operator-signed), narrow sudo boundary, binary signatures |
| Agent identity | enrolling a rogue host, replaying a captured session | one-time enrolment tokens, client certificates, signed hello with replay guard, revocation |
| Operators and API clients | credential stuffing, CSRF, privilege escalation across tenants | lockout, 2FA and passkeys, CSRF tokens, RBAC, tenant isolation, audit log |

Important limits. Whoever controls the server (or holds `pki/ca.key` and `pki/job.key`) can enrol agents and sign jobs. With the default `server` write policy the per-host opt-ins are the only protection that remains; agents installed as read-only or operator-signed (see below) stay safe even then. Sentrel does not encrypt its database at rest. A TLS-terminating proxy or plain HTTP mode removes transport confidentiality between agent and server (see below).

## Enrolment

- A token is 12 characters from an unambiguous 32-symbol alphabet, shown as `XXXX-XXXX-XXXX`. Only its SHA-256 hash is stored.
- Tokens are single use (consumed in the same transaction that creates the host), default to 15 minutes and are limited to 7 days. They can pre-select tenant, group, tags and name.
- Registration is rate limited to 20 attempts per minute per source IP, and failed attempts are logged.
- The server signs the agent's CSR (ECDSA or Ed25519 keys only). The agent's private key never leaves the host.
- In self-signed TLS mode the agent pins the CA fingerprint supplied by the installer command, so the very first connection cannot be intercepted by someone without the CA.

## Internal CA and agent authentication

The CA (ECDSA P-256, 20 years, path length 0) is created at first start in `pki/`. Agent certificates have `CN=<agent uuid>`, client-auth usage only and a 10-year lifetime; they are revoked server-side (a revoked serial or a deleted host gets a "revoked" goodbye and the agent stops retrying).

Every connection starts with a `Hello`. The server authenticates it in one of two ways:

1. **mTLS**: when the server terminates TLS it requests a client certificate (verified against the internal CA when presented) and uses it.
2. **Signed hello**: when TLS is terminated elsewhere (proxy) or not used, the agent sends its certificate and an ECDSA signature over `agent id + timestamp`. The server verifies the signature and certificate chain, accepts a clock skew of at most 5 minutes, and rejects any hello not newer than the last accepted one for that agent (replay guard), which is persisted in the database (`agent_hello`) so a server restart does not reopen the replay window.

In both cases the certificate serial must match the host record and must not be revoked. The protocol is versioned (`SCHEMA_VERSION`); unsupported versions are refused.

## Signed jobs and binaries

The server holds an Ed25519 key (`pki/job.key`); agents receive the public key at enrolment and (on Linux) keep a root-owned copy. Every job is a protobuf message containing the job id, target agent id, type, parameters, issue and expiry times, dry-run flag and timeout, serialised deterministically and signed. The agent rejects a job when:

- the signature is invalid, the agent id differs, it has expired (jobs are valid for 15 minutes after dispatch), or it is issued more than 5 minutes in the future;
- its id has been seen before (the replay set survives restarts).

Agent updates are signed over `OS, architecture, version, SHA-256`. The unprivileged agent checks hash and signature, runs the candidate's `self-test`, and the root helper repeats the verification on a root-owned copy of the file before installing it, so the agent user cannot swap the file between check and install. A failed update rolls back automatically (see [agents.md](agents.md)).

## Write policy: surviving a server compromise

Jobs fall into two classes, defined in one shared package (`internal/jobpolicy`) that both server and agent use:

- **SAFE**: `refresh_inventory`, `collect_now`, `disk_usage`, `unblock_ip`. They only read state or remove a block.
- **WRITE**: everything else (`upgrade`, `block_ip`, `disk_cleanup`, `self_update`) and any job type the agent does not know.

Each agent enforces a **write policy** that is set only locally, at install or register time, and stored in the root-owned `agent.yaml` (`write_jobs`, `operator_keys`). Nothing the server sends (agent options, the pushed agent config, jobs) can change it: the pushed configuration message has no such fields, and tests assert this.

| `write_jobs` | WRITE jobs |
|---|---|
| `server` (default) | accepted with the server's signature, as before |
| `operator` | also need an Ed25519 signature over the exact same job bytes from one of the operator public keys in `operator_keys`; the server's own signature is still required |
| `none` | refused with `rejected: agent is read-only` |

Threat model: an attacker who fully controls the server (database, `pki/job.key`, the API) can issue SAFE jobs to every agent and nothing else on `operator` and `none` hosts. They cannot upgrade packages, change firewall rules, clean disks or push an agent binary. The operator private key is never stored on, or sent to, the server; sign on a separate machine or in your browser. A self-update job is a WRITE job: the operator signature covers the download path, SHA-256 and version in the job, and the agent still verifies the binary signature.

The agent reports its policy and the fingerprints of its operator keys to the server in its hello. This is only used to show badges, refuse impossible requests early (a `none` host gets `403`) and to verify signatures before queueing; the agent re-checks everything itself. Hosts that are not on the `server` policy are skipped by automatic agent updates and automatic IP blocking. Jobs waiting for a signature (`awaiting_signature`) expire like queued jobs, after 24 hours by default.

## Privilege boundary and opt-in remote actions

Linux agents run unprivileged. The only sudo rule allows `sentrel-agent priv *`; each verb validates its arguments (see [agents.md](agents.md)). Because the helper trusts nothing from the caller, a compromised agent process can at worst run the limited verbs: refresh or upgrade packages, add or remove an IP block, read fail2ban status, install or refresh the port-scan detection rules (counting only, never dropping traffic), install a validly signed agent update.

Remote actions are controlled by permission settings. Each has a global default under **Settings, Agents, Host defaults**, and every host inherits it unless the host overrides it. A host shows the value in effect, where it comes from ("Inherited from global" or "Overridden") and a "Reset to global" link:

| Setting | Default | Where it is set on a host | Enables |
|---|---|---|---|
| `allow_upgrades` | off | Packages tab | package upgrades and kernel removal; a dry run is always permitted |
| `allow_cleanup` | on | Storage breakdown (Overview tab), next to Clean up | disk cleanup; a dry run needs it too |
| `allow_ip_block` | on | Security tab, Blocking | manual and automatic IP blocks |
| `auto_block` | off | Security tab, Blocking | automatic blocks from brute-force detection and port scans; needs `allow_ip_block` |

The detection and blocking numbers inherit the same way: `bruteforce_threshold` (default 10 failed attempts), `bruteforce_window_minutes` (5), `auto_block_threshold` (20), `auto_block_minutes` (60), `portscan_enabled` (on) and `portscan_threshold_per_min` (20).

`GET /hosts/{id}` returns a `settings` object with one entry per setting: `{effective, override, global, inherited}`. `override` is `null` when the host inherits. The flat fields (`allow_upgrades`, `allow_cleanup`, `allow_ip_block`, `auto_block`) are the effective values. `PUT /hosts/{id}` accepts `{"settings": {"allow_cleanup": false, "auto_block_minutes": null}}`: a value sets an override, `null` clears it. Job validation, auto-block, the agent configuration and the failed-login alert threshold all use the effective value.

### Never-block list

`never_block` is a list of IPs and CIDR ranges that are never blocked, neither manually nor automatically. The global list (Settings, Agents) applies to every host; a host can add entries (they are added, not substituted). The Sentrel server's own addresses, including the host of the configured server URL, are always protected. A manual block of a listed address returns `400` ("address is on the never-block list"), automatic blocking skips it, and the agent-side port-scan detector also ignores it.

State-changing jobs also need an explicit `confirm`, are recorded in the audit log with the requesting user and (truncated) parameters, and can be scheduled for a maintenance window. Agent self-update needs only `confirm` and the `jobs` permission.

## Port-scan detection

On Linux hosts with nftables the agent installs a detection-only chain in the `inet sentrel` table (details in [agents.md](agents.md#port-scan-detection-linux)). It counts TCP SYNs to ports nobody listens on, per source; a source above the threshold (default 20 per minute) is flagged for 10 minutes, reported to the server as a `portscan` security event, and raises the `portscan` alert (warning at 1 source, enabled by default, mutable per host like the failed-login alert). Hosts with auto-block enabled block flagged sources through the usual `block_ip` job. The agent's own overhead is a 30-second poll; the per-packet work happens in the kernel and the chain never drops traffic. Sources on the allowlist, the Sentrel server and loopback are never flagged. Windows is not yet supported.

## Roles, permissions and tenancy

| Permission | admin | operator | viewer | tenant_admin | tenant_viewer |
|---|:-:|:-:|:-:|:-:|:-:|
| read | x | x | x | x | x |
| ack (acknowledge alerts) | x | x | | x | |
| monitoring (checks, thresholds, maintenance, channels, status pages, groups, vulnerability ignore) | x | x | | x | |
| hosts (enrol, delete, SNMP/target hosts, agent install) | x | x | | | |
| host_settings (per-host options and opt-ins) | x | x | | x | |
| jobs (upgrades, IP blocks, scans, agent update) | x | x | | x | |
| users | x | | | x (own tenant) | |
| tenants | x | | | | |
| settings | x | | | | |
| branding | x | | | x (own tenant) | |
| audit | x | | | x (own tenant) | |

`admin`, `operator` and `viewer` belong to the provider (tenant 0) and see every tenant; `tenant_admin` and `tenant_viewer` are bound to one tenant and every query is restricted to it (hosts, alerts, checks, metrics, channels, status pages, API keys). Threshold rules inherit provider, tenant, group and host in that order. Tenants can have their own branding and a login hostname.

Sign-in sources: local password (Argon2id, minimum length 10 by default and at least 8, common weak passwords refused), LDAP and RADIUS (users are provisioned on first login with a role from the group/attribute mapping; no mapping and no default role means denied).

## Two-factor authentication

Users can enrol TOTP (with recovery codes) and passkeys (WebAuthn, usable as a second factor or for password-less login). **Settings, Security, Require 2FA** can enforce it for `none`, `admins` or `all`; users who are required but not enrolled are held in a limited session that can only enrol. An administrator can reset another user's second factors, and `sentrel-server reset-password --disable-2fa` does so from the console.

## Sessions, CSRF and brute force

- The session cookie `sentrel_session` is `HttpOnly`, `SameSite=Lax`, `Secure` over HTTPS; the token is stored hashed. Lifetime defaults to 12 hours (`session_hours`). Users can list sessions and revoke the others.
- Cookie-authenticated requests that change state must carry the `X-CSRF-Token` header (issued at login); requests authenticated with an API key (`Authorization: Bearer`) are not subject to CSRF checks.
- Lockout: 8 failed logins (default) within 15 minutes for the same username and IP return HTTP 429; a per-IP ceiling of five times that applies across usernames. Counters follow the client IP, so configure `trust_proxy` correctly ([tls-and-proxies.md](tls-and-proxies.md)).
- Responses carry `X-Content-Type-Options`, `Referrer-Policy`, `X-Frame-Options: DENY` (except public status pages, which may be embedded) and, over TLS, HSTS. The UI is served with a strict Content-Security-Policy.

## API keys

Keys look like `sentrel_<prefix>_<secret>`; only a hash of the secret is stored and the key is shown once. A key acts as its owner, limited to the intersection of the owner's role and the key's scopes (`read`, `write`, `jobs`, `admin`); it can expire; keys cannot create keys; a disabled owner disables the key. See [api.md](api.md).

## Audit log

The audit log records sign-ins and failures, setup, user, tenant, key, host, check, threshold, channel, maintenance, status-page, settings, vulnerability and job actions with actor, target, source IP and details (secrets in settings changes are redacted). It is retained for 365 days by default (`audit_days`) and visible to users with the `audit` permission, scoped to their tenant.

## Handling of secrets

- **Stored in clear text in `sentrel.db`** (the database is not encrypted): SMTP, LDAP bind and RADIUS secrets, notification channel tokens and webhook credentials, SNMP communities and v3 passwords, HTTP-check basic-auth passwords, TOTP secrets. They are redacted from every API response, and sending a redacted or omitted value on update keeps the stored one.
- **Stored hashed**: user passwords (Argon2id), session tokens, API keys, enrolment tokens.
- **Key files**: `pki/ca.key`, `pki/job.key`, `pki/server.key` are mode `0600`; the data directory is `0750`.
- **Backups contain all of the above.** Encrypt and restrict them (see [operations.md](operations.md)).

## Outbound targets (SSRF)

Server-side checks (HTTP, TCP, TLS certificate, DNS with a custom resolver, ping) and notification channels (webhook, Slack, Teams, Discord, Gotify, ntfy, Telegram, Pushover) connect to addresses supplied by users. All of them dial through one guarded dialer (`server/netguard`) whose `Control` hook checks the address after DNS resolution and before the socket connects, so DNS rebinding and redirects cannot bypass it.

- Objects owned by the provider (tenant 0) may reach any address, because the provider legitimately monitors its own network.
- Objects owned by a tenant are refused loopback, private (RFC 1918, ULA), link-local (including 169.254.169.254 cloud metadata), CGNAT, unspecified and multicast addresses. The check or notification result says `target address is not allowed`. Environment proxies are not used for these connections.
- Setting `allow_private_targets_for_tenants` (Settings, Security; provider only; default off) lifts the restriction for tenants who monitor internal systems.
- SMTP goes to the server the administrator configured and is not restricted.

## Public surface

Unauthenticated endpoints: `/healthz`, `/api/v1/public/info`, `/api/v1/public/status/{slug}` (public pages, or token-protected ones), the installer scripts under `/install/`, agent binaries and packages under `/download/agent/` (packages only from a fixed manifest), and the login/setup endpoints. `/metrics` is **open by default**; turn on **Settings, Security, "Require an API key for /metrics"** (`metrics_auth`) to require an API key with `read` scope. Setup is protected by the one-time setup token until the first administrator exists.

## Hardening checklist

1. Use TLS (`selfsigned` or `acme`, or a proxy with `trust_proxy`); do not expose plain HTTP.
2. Enable 2FA for admins; set `Require 2FA` to `admins` or `all`.
3. Enable `metrics_auth` unless `/metrics` is only reachable from a trusted network.
4. Leave remote actions off for hosts that do not need them.
5. Docker group membership is root-equivalent, so the installer leaves it off by default; pass `--docker` only on hosts where container metrics are worth that risk.
6. Protect and encrypt backups; keep `pki/` out of shared storage.
7. Keep the server and (via self-update) the agents current. Server updates are Ed25519-signed and verified twice; see [Updating](operations.md#updating) and the [threat model](threat-model.md#server-self-update). Set `SENTREL_DISABLE_SELF_UPDATE=1` if you prefer to update only through your own tooling.
