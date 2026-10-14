# Operations

## Backup

What matters: `sentrel.db` (all configuration, hosts, history) and `pki/` (the CA and signing keys). Losing `pki/` means every agent must re-enrol.

### CLI

```sh
sudo -u sentrel sentrel-server backup --data-dir /var/lib/sentrel --out /backups/sentrel-$(date +%F).tar.gz
```

| Flag | Meaning |
|---|---|
| `--data-dir DIR` | data directory (default as for `serve`) |
| `--out FILE` | output `.tar.gz`; `-` writes to stdout; default `sentrel-backup-<UTC timestamp>.tar.gz` in the current directory. The file is created with mode `0600` |
| `--with-tsdb` | also include `tsdb/` (metrics). Stop the server first for a consistent copy |

The command can run while the server is running: the database is copied with `VACUUM INTO`, which gives a consistent snapshot under WAL. The archive contains `sentrel.db`, `pki/`, `snmp-profiles/` (and `tsdb/` with the flag) plus a `MANIFEST`. It does **not** contain `vulndb/` (re-downloaded) or `acme/` (the certificate is re-issued; Let's Encrypt rate limits apply if you restore repeatedly).

### UI and API

**Settings, Server & backup** (`GET /api/v1/server/backup`, settings permission) downloads the same archive without metrics and records `server.backup_downloaded` in the audit log.

### Protect backups

They contain the CA private key, the job signing key and clear-text integration secrets ([security.md](security.md)). Encrypt them (for example `age` or `gpg`), store them off the server and test restores. A systemd timer is enough for scheduling:

```ini
# /etc/systemd/system/sentrel-backup.service
[Service]
Type=oneshot
User=sentrel
ExecStart=/usr/local/bin/sentrel-server backup --data-dir /var/lib/sentrel --out /var/backups/sentrel/sentrel.tar.gz
```

## Restore

The server must be stopped.

```sh
sudo systemctl stop sentrel
sudo -u sentrel sentrel-server restore --data-dir /var/lib/sentrel --in sentrel.tar.gz --force
sudo systemctl start sentrel
```

`--force` is required when the data directory already holds a database. Files are written next to their destination and renamed into place, stale `-wal`/`-shm` files are removed, and `pki/` files are written `0600`. Because the CA is restored, agents reconnect on their own, including on a new machine, as long as the server URL they were registered with resolves to it. The vulnerability database downloads again on start, and metrics are present only if the backup included `tsdb/`.

Run the commands as the service user (or fix ownership with `chown -R sentrel:sentrel /var/lib/sentrel` afterwards).

## Forgotten password or lost second factor

```sh
sudo -u sentrel sentrel-server reset-password --data-dir /var/lib/sentrel --user admin [--disable-2fa]
```

It prints a random one-time password. The account is switched to local authentication, enabled, flagged "must change password", all its sessions are ended, and `user.password_reset_cli` is written to the audit log. With `--disable-2fa` the user's TOTP and passkeys are removed too. This needs access to the data directory; there is no remote equivalent for the last administrator.

## Retention and storage

**Settings, Data retention** (defaults):

| Setting | Default | Effect |
|---|---|---|
| `days` | 31 | metric (time-series) retention; each host can override its own `retention_days` and the store keeps the longest value |
| `alert_history_days` | 180 | resolved alerts and the alert timeline |
| `audit_days` | 365 | audit log |
| `job_history_days` | 180 | finished jobs and their output |
| `security_event_days` | 30 | failed-login summaries |
| `host_storage_warn_mb` | 512 | alert when one host's stored metrics exceed this (`host_storage_mb` threshold) |
| `disk_warn_percent` | 85 | warning when the data disk passes this, critical at 95% |

Cleanup runs periodically; per-host metric retention and per-host storage accounting run nightly. A host's storage grows with its collection interval and the number of series (disks, interfaces, containers, processes), so the cheapest fixes for a large host are a longer interval or a shorter `retention_days`, set per host. The storage warning, the server-disk warning and the vulnerability database update failure appear as "system" alerts in the normal alert list.

Disk budget: `tsdb/` (grows with hosts times series times retention) plus `vulndb/` (about 3 GB) plus `sentrel.db`.

## Resource usage

Measured on a 2-vCPU, 3.8 GB Linux VPS running the server and its own agent, with 3 hosts (2 agents, 1 other), about 5 hours after start. Sizes and rates grow with hosts, series and retention, so treat them as a guide.

| Item | Measured |
|---|---|
| Server idle memory (RSS) | about 120 to 150 MB; the same with scanning off and with an existing vulnerability database opened |
| Server peak memory (VmHWM) | about 575 MB, reached while scanning hosts against the vulnerability database |
| Server CPU at idle | under 0.1% of one core (8 CPU-minutes over 5 hours) |
| Agent memory (RSS) | 13 to 15 MB (peak 22 MB) |
| Agent CPU | about 0.2% of one core over 5 minutes at the default 30 s interval |
| `sentrel.db` | about 60 MB plus WAL with 48,000 vulnerability findings and 1,300 packages; a few MB without scanning |
| Metric store (`tsdb/`) | about 2 MB for 3 hosts after 10 hours; budget roughly 0.5 to 1 MB per host per day at default collection settings |
| Vulnerability database (`vulndb/`) | about 3.1 GB on disk, only when scanning is enabled |
| Server binary | about 150 MB (UI, agent binaries and agent packages embedded; built with `-trimpath -s -w`) |
| Agent binary | about 14 MB |

Vulnerability scanning is off by default. While it is off, the server never downloads or opens the database. Enabling it (Settings, Vulnerabilities, or the setup wizard) downloads about 3 GB in the background. Disabling it releases the database; tick "Also delete the downloaded database" (`delete_db: true` in the request) to reclaim the disk space.

### Hardware recommendations

| Host | Scanning | CPU | RAM | Disk |
|---|---|---|---|---|
| Raspberry Pi 4 or a 1 GB VPS | Off (default) | 1 core | 1 GB | 2 GB plus metrics (about 1 MB per host per day) |
| Small server | On | 2 cores | 2 GB or more | 6 GB or more (the database is about 3 GB and is replaced on update) |
| Larger fleet (50+ hosts) with scanning | On | 2 to 4 cores | 4 GB | 10 GB or more |

## Capacity

How many agents can one server handle? These numbers come from simulated agents on a laptop, so read them as an indication of the shape of the load, not a guarantee for your hardware.

**Test machine:** Windows 11 laptop, Intel Core i3-1215U (6 cores, 8 threads), 7.7 GB RAM, SSD. The load generator ran on the same machine as the server, so the two competed for the same CPU, memory and loopback network. The server process is measured on its own.

**Method:** Each step is a fresh set of simulated agents (1 to 4000) against one server (plain HTTP, SQLite, built-in metric store), each agent reporting every 30 s (the default). Each report is about 60 metric samples plus a host state (4 disks, 3 interfaces, 30 services, 8 ports, 10 top processes, 3 containers), and every agent sends an inventory of 450 packages once when it connects. CVE scanning was off. Agents connected over a 45 s ramp, settled for 45 s, then CPU, memory, data growth and API latency were measured for 3 minutes. CPU is the server process's average, as a percentage of one core (100% is one full core; this laptop has 8 logical cores).

| Agents | Server CPU (% of one core) | Server RSS (avg / max) | `GET /api/v1/hosts` median (response size) | Metric WAL growth |
|---:|---:|---:|---:|---:|
| 50 | 1.1% | 110 / 112 MB | 11 ms (71 KB) | 3 MB/hour |
| 200 | 4.2% | 178 / 182 MB | 25 ms (284 KB) | 14 MB/hour |
| 500 | 9.9% | 297 / 310 MB | 48 ms (709 KB) | 34 MB/hour |
| 1,000 | 20.6% | 509 / 527 MB | 88 ms (1.4 MB) | 68 MB/hour |
| 2,000 | 34.6% | 857 / 938 MB | 155 ms (2.8 MB) | 137 MB/hour |
| 4,000 | 64% | 1,537 / 1,606 MB | 389 ms (5.7 MB) | 280 MB/hour |

What the numbers say:

- **Cost is close to linear in agents:** about 2% of a core and 0.35 MB of memory per 100 agents on top of a baseline of about 150 MB. A 1,000-agent run held for 15 minutes in an earlier pass stayed at 18% of a core and about 540 MB.
- **No errors:** in every step all agents stayed connected with no reconnects, connection failures or send errors.
- **Outliers on the host list:** `GET /hosts` returns every host in one response and is not paginated, so it grows with the fleet. At 2,000 and 4,000 agents single requests took 1.1 s and 5.0 s (the medians above are typical). The UI polls this endpoint, so very large fleets will feel slower there before anything else does.
- **Connection storms are the weak spot:** when 4,000 agents connected within 45 s, the time from hello to welcome was 17 s at the median and 27 s at the 95th percentile (2,000 agents: 5 ms and 153 ms). Every agent got in with no failures, and the load settled afterwards. The cause was not profiled; the likely culprit is the database writes done on connect (host update and inventory ingest). After a server restart a large fleet reconnects over a minute or more, which the agents' own back-off handles.

Storage figures need care:

- *Metric WAL growth* is the metric store's write-ahead log, which is written uncompressed (about 10 bytes per sample) and later folded into compressed 2-hour blocks. It is an upper bound on the live write rate, not the long-term disk cost. The test did not run long enough to measure compacted blocks, so for long-term disk use keep the budget of 0.5 to 1 MB per host per day given under [Resource usage](#resource-usage), which was measured at default collection settings on a small fleet.
- `sentrel.db` grew by under 1 MB per hour up to 1,000 agents and about 12 MB per hour at 4,000 while agents were steadily reporting (inventory data is replaced on each connect, not appended).

Limits of this test:

- Simulated agents report plausible but synthetic data and do not run jobs, security scans or port-scan detection. Real agents with Docker, many services or large process lists send somewhat larger states.
- It was one laptop on Windows, not a server on Linux, and storage was an SSD. Other disks, especially slow ones, will change the database behaviour.
- Alert rules, notification channels, checks, SNMP, status pages, many users in the UI, CVE scanning and long retention add load and were not included. Beyond 4,000 agents nothing was measured.
- Each result is a single 3-minute window per step, not a repeated run.

### Reproduce it

`sentrel-loadtest` ships in the source tree (`cmd/sentrel-loadtest`). It uses the real agent protocol, gives every simulated agent its own certificate and hostname (`loadtest-0001`, `loadtest-0002`, and so on), and caches identities so reruns skip enrolment. Run it only against a server you own, preferably a throwaway one.

```sh
go build -o sentrel-loadtest ./cmd/sentrel-loadtest
# Start a scratch server and finish setup (web UI or POST /api/v1/setup) with an admin account that has no MFA.
sentrel-loadtest -server http://127.0.0.1:18090 -user admin -password '...' -agents 1000 -interval 30s -ramp 45s -duration 8m -data ./lt-identities
```

Enrolment tokens are single-use, so the tool logs in with `-user` and `-password` (or `SENTREL_PASSWORD`) and creates one token per new agent. The server limits enrolment to 20 per minute per source IP, so for a loopback server the tool enrols from several `127.x.y.1` addresses; disable that with `-spread-source=false`. Run `sentrel-loadtest -h` for the other flags (`-enrol-only` to pre-enrol, `-probe` to time `GET /api/v1/hosts`, `-packages` to size the inventory). The tool prints connection counts and the report rate every 10 s. Measure the server process's CPU and memory with your platform's tools (for example `Get-Process` on Windows or `pidstat` on Linux). Delete the loadtest hosts from the server afterwards.

## Logs and health

- Logs go to stderr: `journalctl -u sentrel` (systemd) or `docker logs sentrel`. `SENTREL_LOG_LEVEL` is `debug`, `info` (default), `warn` or `error`; `SENTREL_LOG_FORMAT=json` switches to JSON lines.
- `GET /healthz` returns `ok` (HTTP 200) when the database answers, 503 otherwise. `sentrel-server healthcheck [--url http://127.0.0.1:8080/healthz]` wraps it for scripts and containers; if the URL is `http://` and fails it retries over `https://` with certificate verification skipped, so it keeps working after enabling TLS on the same port.
- The audit log records who did what; application logs record agent connects and disconnects, rejected jobs, notification failures and listener changes.
- To monitor Sentrel itself, scrape `/metrics` (`sentrel_server_agents_connected`, `sentrel_alerts_firing`) and add an external HTTP check on `/healthz`.

## Exporting metrics

On a host's Metrics tab:

- **Export data** downloads every graph series for the chosen range (1h, 6h, 24h, 7d, 30d or a custom range) as CSV (one row per timestamp, one column per series) or JSON.
- **Export all charts (PNG)** saves every panel as one tall image.
- The menu in a chart's top-right corner has **Download PNG** and **Copy image**. Images include the title, host, time range and legend, use the current light or dark theme, and are named `<host>-<metric>-<YYYYMMDD-HHMM>.png`.

Scripts can fetch the same data from `GET /api/v1/hosts/{id}/metrics/export` (see the API reference). Exports are limited to 50,000 rows; for long ranges pick a coarser step.

## Updating

Sentrel tells administrators when a new release exists: a green **Update vX.Y.Z** pill appears next to the version at the bottom of the sidebar (administrators only), with a dismissible banner once per version. **Settings, Updates** shows the release notes, the install method and what to do about it. Back up before updating ([Backup](#backup)).

### The version check

Every 6 hours (and on **Check now**) the server sends `GET https://api.github.com/repos/PacketGhost99/Sentrel/releases/latest` with `User-Agent: Sentrel/<version>` and an `If-None-Match` ETag. Nothing else is sent, and drafts and prereleases are ignored. This is the only automatic outbound call besides the ones you configure. Turn it off in **Settings, Updates**, or set `SENTREL_DISABLE_UPDATE_CHECK=1` in the server's environment (which overrides the setting; the UI then says it is disabled by the administrator of the host). `SENTREL_DISABLE_SELF_UPDATE=1` disables installing.

### Methods

The server detects how it was installed:

| Method | Detected by | How to update |
|---|---|---|
| `systemd` | Started by systemd from `/usr/local/bin/sentrel-server` with the `sentrel-updater` units installed by `install-server.sh` | **Install update** in Settings, Updates (one click) |
| `docker` | `/.dockerenv`, or `SENTREL_INSTALL_METHOD=docker` (set in the official image) | `docker pull ghcr.io/packetghost99/sentrel:<version>`, then `docker compose up -d` |
| `package` | The binary is owned by a dpkg or rpm package | `sudo apt update && sudo apt install --only-upgrade sentrel-server`, or `sudo dnf upgrade sentrel-server` |
| `manual` | Anything else | Download the binary, verify it, replace it and restart (or re-run `install-server.sh`) |

Installs that predate the updater get its units by re-running `install-server.sh` with the new script (`sudo sh install-server.sh --binary ./sentrel-server`, or `--updater-units-only` to add just the units).

### One-click update (systemd)

1. An administrator clicks **Install update** and types the hostname to confirm. The request is audited.
2. The `sentrel` user downloads the server binary for its architecture, `SHA256SUMS` and `SHA256SUMS.sig` from the release, stages them in `<data-dir>/update/staged/`, verifies the signature and the checksum itself, and writes `<data-dir>/update/request.json`.
3. `sentrel-updater.path` notices the file and starts `sentrel-updater.service` as root. The updater is the currently installed binary (`sentrel-server self-update apply`). It copies the staged files to a root-owned directory (refusing symlinks), verifies the signature with the key compiled into itself, checks the binary's hash, runs `<new> version` and checks it reports the requested version, and refuses downgrades and equal versions.
4. It copies the current binary to `sentrel-server.old`, installs the new one with an atomic rename, re-applies `setcap 'cap_net_raw,cap_net_bind_service+ep'`, and restarts `sentrel.service`.
5. It polls `/healthz` and `/api/v1/public/info` (using the configured listen address, over HTTP or HTTPS) for up to 90 seconds. If the new version does not report itself healthy, it restores `.old`, restarts, and records a rollback.
6. It writes `<data-dir>/update/result.json` and the server records `server.update_applied` or `server.update_rolled_back` in the audit log. The page shows each phase (downloading, verifying, waiting for the updater, installing, restarting, health check) and reloads when the new version answers. Agents then update through the usual agent auto-update.

If the page says the updater has not started, check `systemctl status sentrel-updater.path` and `journalctl -u sentrel-updater`.

### Signing model

Each release's `SHA256SUMS` is signed in the release workflow with an Ed25519 key (`sentrel-sign sign`), producing `SHA256SUMS.sig`. The public key is compiled into the server in one place, `internal/release/key.go`, and cannot be overridden by an environment variable, setting or flag. Verify a download by hand with `sentrel-sign verify -pub <key file> SHA256SUMS SHA256SUMS.sig`; the releases are additionally signed with cosign. Why this design is safe against a compromised server process is described in the [threat model](threat-model.md#server-self-update).

### Rolling back by hand

`/usr/local/bin/sentrel-server.old` is the previous binary. To go back: `sudo install -m 0755 /usr/local/bin/sentrel-server.old /usr/local/bin/sentrel-server && sudo setcap 'cap_net_raw,cap_net_bind_service+ep' /usr/local/bin/sentrel-server && sudo systemctl restart sentrel`. Database migrations are not reversed; restore a backup if a release changed the schema and you need the old schema back.

## Upgrading

See [install.md](install.md#upgrading). In short: back up, replace the binary (or rebuild the image), let the server migrate the database at start, then upgrade agents from the UI. Check **Settings** and the host list afterwards; agents show their version.

## Server settings that are not in the UI

The listen address can be forced with `--listen` / `SENTREL_LISTEN`, TLS can be overridden with `--force-http` ([tls-and-proxies.md](tls-and-proxies.md)), and the data directory is `--data-dir` / `SENTREL_DATA_DIR`. All other settings live in the database and are edited in the UI or with `PUT /api/v1/settings/{section}` (sections: `general`, `tls`, `smtp`, `ldap`, `radius`, `security`, `retention`, `agent_defaults`, `alerting`, `vuln`).
