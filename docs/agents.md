# Agents

The agent (`heartbeat-agent`) is a single static binary for Linux (amd64, arm64) and Windows (amd64). It makes one outbound gRPC connection to the server, reconnects with exponential backoff (2 s up to 60 s, with jitter), buffers metric batches while offline, and applies the configuration the server sends.

## Installing

Create an enrolment token under **Add host**. It is one-time, valid 15 minutes by default (up to 7 days), and can pre-assign a group, tags and a display name. The UI shows ready-to-paste commands. Registration generates an ECDSA P-256 key on the host, sends a CSR plus the token, and receives a client certificate, the CA certificate and the server's job-verification public key.

### Linux

Requires systemd. Run as root:

```sh
curl -fsSL https://monitor.example.com/install/agent.sh | sudo sh -s -- --server https://monitor.example.com --token ABCD-1234-EFGH
```

| Option | Meaning |
|---|---|
| `--server URL`, `--token TOKEN` | required |
| `--ca-fingerprint FP` | pin the server CA (self-signed TLS mode; the UI adds it for you) |
| `--name NAME` | display name |
| `--binary PATH` | use a local binary instead of downloading from `<server>/download/agent/linux/<arch>` |
| `--no-docker` | do not add the agent user to the `docker` group |
| `--insecure-download` | skip TLS verification for the binary download only |
| `--uninstall` | remove the agent |

The installer: creates the system user `heartbeat-agent` and adds it to the `adm`, `systemd-journal` and (unless `--no-docker`) `docker` groups; installs `/usr/local/bin/heartbeat-agent`; writes `/etc/heartbeat-agent/agent.yaml`, `/etc/heartbeat-agent/job.pub` (root-owned) and state in `/var/lib/heartbeat-agent` (`0700`); validates and installs the sudoers rule `/etc/sudoers.d/heartbeat-agent` (validated before registering, so a failure does not burn the token); installs the unit `heartbeat-agent.service` (`MemoryMax=192M`, `CPUQuota=25%`, `Nice=10`, `Restart=always`).

### Windows

From an elevated PowerShell:

```powershell
& ([scriptblock]::Create((irm https://monitor.example.com/install/agent.ps1))) -Server https://monitor.example.com -Token ABCD-1234-EFGH
```

Parameters: `-Server`, `-Token`, `-CaFingerprint`, `-BinaryPath`, `-Name`, `-InsecureDownload`, `-Uninstall`. The script installs to `C:\Program Files\Heartbeat Agent\heartbeat-agent.exe`, keeps config and keys in `C:\ProgramData\Heartbeat Agent` (restricted to SYSTEM and Administrators), registers the `HeartbeatAgent` service (automatic delayed start, restart on failure) and starts it.

For fleet deployment an MSI can be built (`msiexec /i heartbeat-agent-<version>-x64.msi SERVER=https://monitor.example.com TOKEN=ABCD-1234-EFGH /qn`). `scripts/build.sh` builds it only when `wixl` and `deploy/agent.wxs` exist; the `.wxs` is not in the repository yet.

### Push install, manual install

- **Push install** (Add host): the server connects over SSH (Linux, key or password, optional host-key fingerprint pin, sudo password) or WinRM (Windows Server), uploads the agent and runs the installer. The credentials are used for that session only; the server stores the target, user name and log, not the secrets.
- **Manual**: `heartbeat-agent register SERVER_URL TOKEN [--ca-fingerprint FP] [--name NAME] [--config FILE] [--state-dir DIR]`, then `heartbeat-agent service install` and `service start` on Windows, or run `heartbeat-agent run` under your own supervisor.

## Collected data

Intervals and switches come from **Settings, Agent defaults**, overridable per host (host settings). Defaults: collect every 30 s; inventory hourly; top 10 processes; services, listening ports, Docker and temperatures on; security scan every 60 s.

| Data | Details |
|---|---|
| Host info | hostname, OS, platform and version, kernel, architecture, CPU model and cores, memory, boot time, IP addresses, virtualisation, `os-release` ID |
| Metrics | CPU total/iowait/steal, memory, swap, load 1/5/15 and per core, disk used/total and I/O, inode usage, disk-full ETA, network throughput and errors, temperatures, process count, uptime, agent CPU and memory |
| State | services (systemd units or Windows services; all, or only watched ones), listening ports, top processes and the list of process names, Docker containers (state, health, restarts, CPU, memory), reboot-required flag |
| Inventory | installed and upgradable packages (apt, dnf, yum, zypper, pacman, apk) with security flags; on Windows installed applications and pending Windows Updates |
| Security | failed SSH logins from `auth.log`/`secure` or journald (Linux), failed logons event 4625 (Windows), fail2ban jails, active IP blocks |

Services named in `service` checks are always reported even when service collection is off.

## Privilege model

**Linux.** The agent runs as the unprivileged user `heartbeat-agent`. Anything needing root goes through `sudo -n /usr/local/bin/heartbeat-agent priv <verb>`, and the only sudo rule is:

```
heartbeat-agent ALL=(root) NOPASSWD: /usr/local/bin/heartbeat-agent priv *
```

The helper validates its own arguments. Verbs: `pkg-refresh`, `pkg-upgrade`, `block-ip`, `unblock-ip`, `list-blocks`, `fail2ban-status`, `install-update`, `rollback-update`. Package names and versions are matched against strict patterns, IPs must parse and not be loopback or unspecified, block durations are 1 to 129600 minutes, and update files must be regular files inside the agent state directory, verified against the root-owned key (`/etc/heartbeat-agent/job.pub`). Package upgrades run in their own `systemd-run --scope` unit, so they are outside the agent's CPU and memory limits and survive an agent restart.

IP blocking uses an `inet heartbeat` nftables table with timeout sets (and falls back to iptables/ip6tables rules tagged `heartbeat-block:<expiry>`). Uninstalling removes the nftables table.

Script jobs run as the unprivileged agent user. Note that membership of the `docker` group is equivalent to root on the host; pass `--no-docker` if you do not want container metrics at that price.

**Windows.** The service runs as LocalSystem (the default for a service without an account), so there is no privilege boundary: upgrades install Windows Updates through the Windows Update API, blocks are Windows Firewall rules named `Heartbeat block <ip>`, and scripts run as SYSTEM in PowerShell (or cmd).

Remote actions (upgrade, IP block, scripts) are disabled for every host until an operator enables them in the host settings; see [security.md](security.md).

## Configuration file

`/etc/heartbeat-agent/agent.yaml` (Windows: `C:\ProgramData\Heartbeat Agent\agent.yaml`) is written by `register`:

```yaml
server_url: https://monitor.example.com
agent_id: 1b3c...-uuid
state_dir: /var/lib/heartbeat-agent
ca_fingerprint: sha256:...   # only when pinned
name: web-1                  # optional display name
log_level: info              # debug | info | warn (optional)
```

What to collect is not configured here; it comes from the server and changes apply to connected agents immediately. State directory contents: `agent.key`, `agent.crt`, `ca.pem`, update staging and `update-pending.json` during an update. Use `heartbeat-agent run --config FILE` to start with a different file.

## Self-update and rollback

Start an update from the host page or via the API (`POST /api/v1/hosts/{id}/update-agent`, needs `confirm: true`). The server embeds the agent builds it was compiled with. The job carries the download path, SHA-256, version and an Ed25519 signature over OS, architecture, version and hash. The agent then:

1. downloads the binary over its normal TLS channel (system trust store plus the server CA received at registration), and checks the size, SHA-256 and signature;
2. runs the new binary's `self-test`, which must authenticate against the server;
3. hands it to the root helper (`priv install-update`), which copies it, verifies hash and signature again against the root-owned key, keeps the old binary as `heartbeat-agent.old`, swaps the file and writes `update-pending.json`;
4. exits so systemd (or the Windows service manager's restart action) starts the new version.

Rollback is automatic: the update marker counts start attempts, and if the new version starts more than three times, or has not connected to the server within three minutes, the helper restores `heartbeat-agent.old` and the old version restarts. A successful connection clears the marker, and the server marks the job succeeded when the agent reconnects with the new version. The update, success and rollback events appear in the job output and audit log.

## Uninstalling

- Linux: `curl -fsSL https://monitor.example.com/install/agent.sh | sudo sh -s -- --uninstall`. This stops the service, deletes the nftables table, the unit, sudoers rule, binaries, `/etc/heartbeat-agent`, `/var/lib/heartbeat-agent` and the user.
- Windows: run the script with `-Uninstall`; it also removes `Heartbeat block *` firewall rules.
- Then delete the host in the UI (this revokes the certificate and removes its data) or use **Revoke** to keep the history.

## Troubleshooting

`heartbeat-agent status` prints the version, server URL, agent id and state directory, then tries an authenticated self-test connection:

```
server:   reachable, credentials accepted
server:   NOT REACHABLE - <reason>
```

`heartbeat-agent self-test` does the same and exits non-zero on failure. Common reasons:

| Message | Cause and fix |
|---|---|
| `invalid or expired enrollment token` | token already used, expired or mistyped; create a new one |
| `too many registration attempts` | more than 20 registrations per minute from one IP; wait |
| certificate or "pinned CA" errors | wrong server URL or TLS mode; in self-signed mode pass `--ca-fingerprint` |
| `clock skew too large` | the signed hello is accepted within 5 minutes only; fix NTP |
| `replayed hello`, `hello signature invalid` | duplicate agent running with the same identity, or a proxy mangling the stream |
| `agent certificate revoked or host deleted` | the host was deleted or revoked; the agent writes a `REVOKED` file in its state directory and stops retrying. Re-register with a new token |
| `server unavailable` | server down, firewall, or a proxy without HTTP/2 (see [tls-and-proxies.md](tls-and-proxies.md)) |
| `sudo: a password is required` in job output | sudoers rule missing; re-run the installer |

Logs: `journalctl -u heartbeat-agent -f` on Linux; on Windows stop the service and run `heartbeat-agent run` in an elevated console to see the log output. Set `log_level: debug` in `agent.yaml` and restart. If nothing appears in the UI, also check that the host's clock is correct and that outbound connections to the server port are allowed.
