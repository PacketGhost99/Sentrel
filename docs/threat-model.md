# Threat model

This page lists what Sentrel protects, where the trust boundaries are, who might attack it, and what each attacker can and cannot do. [security.md](security.md) describes the controls in detail; this page puts them against attackers and is explicit about what is not covered. Sentrel has had one internal security review (findings fixed) but no independent audit.

## Assets

| Asset | Why it matters |
|---|---|
| `pki/ca.key` and `pki/job.key` | Whoever holds them can enrol agents and sign jobs for every agent |
| `sentrel.db` | Users, password hashes, sessions, API key hashes, hosts, findings, audit log, and integration secrets in clear text (SMTP, LDAP bind, RADIUS, webhook and channel tokens, SNMP communities and v3 passwords, HTTP-check basic-auth passwords, TOTP secrets) |
| Monitored hosts | The agent can install packages, change firewall rules and run scripts if enabled; on Windows it runs as SYSTEM |
| Operator signing key (optional) | Held off-server; gates write jobs on `operator` agents |
| Tenant data | One tenant's hosts, metrics and alerts must not be visible to another |
| Backups | Contain the database and `pki/` |

## Trust boundaries

1. **Browser and API clients to server.** Authenticated by session cookie (with CSRF header), API key, OIDC or trusted proxy headers.
2. **Server and agent.** The agent connects outbound over gRPC. Authentication is mTLS or a signed hello. Server-to-agent instructions are signed jobs.
3. **Agent and root helper (Linux).** The unprivileged `sentrel-agent` user calls `sudo sentrel-agent priv <verb>`.
4. **Tenant and tenant.** Users bound to one tenant share the server with every other tenant.

## Attackers

### 1. Internet attacker (no account)

Reaches whatever you expose: the web port, and the agent port if agents come in from the internet (it is the same port).

Cannot, by design:
- Enrol an agent without a valid one-time token (12 characters, SHA-256 hashed at rest, single use, 15 minutes by default, 7 days at most). Registration is limited to 20 attempts per minute per source IP. Only ECDSA and Ed25519 CSRs are signed.
- Guess passwords efficiently: passwords are Argon2id; 8 failed logins in 15 minutes for the same username and IP return HTTP 429, with a per-IP ceiling of five times that across usernames. The counters key on the client IP, so `trust_proxy` and `trusted_proxies` must be correct, otherwise every client appears to be the proxy.
- Forge `Remote-User` style headers: forward-auth headers are only believed from direct peers in the trusted proxy list (loopback only by default).
- Replay a captured agent hello: the signed hello carries a timestamp (5 minute skew), must be newer than the last accepted one for that agent, and the last value is persisted in the database. Job IDs are tracked for replay in the agent, also across restarts.
- Run the setup wizard on a configured server: it needs the one-time setup token and is closed once the first administrator exists.

Can:
- Read the unauthenticated surface: `/healthz`, `/api/v1/public/info`, public status pages (and token-protected ones with the token), installer scripts and agent binaries. **`/metrics` is open by default** and exposes host names and metrics; enable `metrics_auth` to require an API key.
- Attempt denial of service. There is no general rate limiting beyond login lockout and registration limits. Put a proxy in front if you are exposed.
- Observe and modify traffic in plain HTTP mode (below).

### 2. Malicious tenant user (authenticated as `tenant_admin` or `tenant_viewer`)

Cannot:
- Read or modify another tenant's data. Every table has a `tenant_id` and handlers check the caller's tenant (`CanTenant`). A test calls every `{id}` route with a user from another tenant, and a second test covers the live event stream. Tests reduce risk; they do not prove the absence of a missed route.
- Exceed their role. A tenant viewer cannot write; a tenant admin cannot create scripts jobs (the `scripts` permission is admin-only), manage other tenants, settings or provider-level objects.
- Use checks or notification channels as an SSRF probe against the server's network. For tenant-owned checks and channels, the guarded dialer refuses loopback, RFC 1918, ULA, link-local (including 169.254.169.254 cloud metadata), CGNAT, unspecified and multicast addresses. The check happens after DNS resolution and at connect time, so DNS rebinding and redirects do not bypass it, and environment proxies are not used. Setting `allow_private_targets_for_tenants` turns the protection off for tenants; it is off by default.
- Use API keys beyond their owner: keys are limited to the owner's role intersected with key scopes and cannot create keys.
- Perform a cross-site request with their session: state-changing cookie requests need the `X-CSRF-Token` header, the cookie is `SameSite=Lax`, and the UI has a strict Content-Security-Policy.

Can:
- Create checks and channels in their own tenant, with the server making the outbound requests from the Sentrel host to public addresses. A malicious tenant can therefore use your server as a source of outbound requests to the internet.
- Run jobs on their own tenant's hosts, if those hosts have opted in to the action and the user has the `jobs` permission.
- Cause load (many checks).

### 3. Compromised server (attacker controls the process, database or key files)

This is the attacker the write policy exists for.

Can, always:
- Read everything on the server, including clear-text integration secrets, and impersonate any user.
- Enrol rogue agents, sign SAFE jobs (`refresh_inventory`, `collect_now`, `disk_usage`, `unblock_ip`) to every agent, and read data every agent reports. Disk usage reports reveal file names and sizes.
- Send arbitrary notifications.

Can, against agents on the default `server` write policy:
- Everything the host-level opt-ins allow: package upgrades, IP blocks (loopback and unspecified refused), disk cleanup, scripts (as the agent user on Linux, SYSTEM on Windows), and agent self-update to a binary the server signs. The opt-in flags live in the server database, so a compromised server can set them. **On these agents the server is fully trusted.** This is the default.

Cannot, against agents on `operator` or read-only policy:
- Run any WRITE job. `operator` agents also require an Ed25519 signature from an operator key over the exact job bytes; the private key is never on the server, and the agent re-checks the signature itself. Read-only agents refuse WRITE jobs outright. The policy is stored in the root-owned `agent.yaml` and the pushed agent configuration has no field that can change it.
- Deliver a malicious agent binary: self-update is a WRITE job and the binary signature is verified again by the root helper.

### 4. Compromised agent process (Linux, runs as `sentrel-agent`)

Can:
- Send false metrics and state to its own host record, and try to open sudo calls.
- Run the allowed `priv` verbs: `pkg-refresh`, `pkg-upgrade`, `block-ip`, `unblock-ip`, `list-blocks`, `fail2ban-status`, `disk-usage`, `disk-cleanup`, `install-update`, `rollback-update`.

Cannot:
- Make the helper run arbitrary commands. The only sudo rule is `sentrel-agent priv *`; verbs validate their own arguments against allowlists (package name and version patterns, parsed IPs, bounded block durations, a fixed set of cleanup tasks) and never use a shell.
- Trick root into following symlinks. Root-side writes and chowns of state avoid following links, and cleanup and disk walks go through `os.Root` so a directory swapped for a symlink mid-operation does not escape the root.
- Install an unsigned update: `install-update` operates on a root-owned copy and verifies hash and signature against `/etc/sentrel-agent/job.pub`, which the agent user cannot write.
- Impersonate other hosts: its certificate is bound to its own agent ID, and it never sees other agents' keys.
- Reach other tenants: it is one host in one tenant.

Residual: the `docker` group is root-equivalent. The installer does not add the agent to it unless you pass `--docker`, but if you do, a compromised agent is root. On Windows there is no privilege boundary at all: the service is SYSTEM.

### 5. Local unprivileged user on an agent host

Cannot (Linux):
- Read the agent's key and state: `/var/lib/sentrel-agent` is `0700`, owned by the agent user; `/etc/sentrel-agent/job.pub` and `agent.yaml` are root-owned.
- Use the sudo rule: it is granted only to the `sentrel-agent` user.

Cannot (Windows):
- Read or alter `C:\ProgramData\Sentrel Agent`: the directory and files get a protected ACL that grants only SYSTEM and Administrators. Updates are staged in a protected directory and verified from a private copy, to avoid time-of-check races.

Can:
- Read what any local user can read, such as the process list and listening ports.
- If they can become the agent user (for example through another vulnerability), they have the capabilities in section 4.

## Transport

| Mode | Confidentiality | Integrity of jobs and auth |
|---|---|---|
| `selfsigned` or `acme` | TLS; mTLS to agents when the server terminates it | Yes |
| `http` behind a proxy that terminates TLS | TLS to the proxy; plain between proxy and server (keep it on loopback) | Yes; signed hello and signed jobs |
| `http` direct | None | Jobs are signed and agents authenticate with a signed hello, so an on-path attacker cannot forge jobs. They can read all metrics, enrolment tokens, the session cookie and API keys |

In self-signed mode the first agent connection pins the CA fingerprint given by the installer command. The agent binary download in the install command may use `--insecure-download` for the same reason, which means the downloaded binary is not authenticated by TLS; the server's signature on updates covers later updates, not the first install. Compare the fingerprint, or use a locally verified binary (`--binary`).

## Residual risks

- **Default agents trust the server fully.** Only `operator` and read-only agents survive a server compromise. Choose these for hosts that matter more than the monitoring server.
- **Clear-text secrets in the database.** SMTP, LDAP bind, RADIUS shared secrets, channel tokens, SNMP credentials, HTTP-check passwords and TOTP secrets are stored unencrypted. They are redacted from API responses. Anyone who reads `sentrel.db` or a backup has them. Use dedicated, least-privilege credentials (for example a read-only LDAP bind account).
- **Backups contain the CA and job keys.** A stolen backup is a stolen server. Encrypt backups and keep them off-site and access-controlled.
- **Plain HTTP mode** is supported and documented, and is unsafe on untrusted networks.
- **Proxy and header trust.** `trust_proxy` and forward-auth are only as good as the proxy: if it fails to strip client-supplied identity headers, anyone can sign in as any user. Sentrel cannot detect that.
- **LDAP and RADIUS** use the server as the intermediary; compromise of the server exposes the bind credentials.
- **Open `/metrics` by default.**
- **Scripts.** `allow_scripts` is remote code execution by design for admins. Leave it off unless needed.
- **Third-party code.** The vulnerability matcher embeds Grype and its dependency tree; the vulnerability database is downloaded from Anchore. Sentrel does not verify the data beyond what Grype does.
- **Single-process design.** Everything runs in one process with one data directory; there is no separation between the web tier and the signing key holder.
- **AI-assisted code.** Much of the code was written with an AI assistant. It has been reviewed and tested, but treat it as you would any young codebase.
- **No independent audit.**
