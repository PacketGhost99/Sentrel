# Operations

## Backup

What matters: `heartbeat.db` (all configuration, hosts, history) and `pki/` (the CA and signing keys). Losing `pki/` means every agent must re-enrol.

### CLI

```sh
sudo -u heartbeat heartbeat-server backup --data-dir /var/lib/heartbeat --out /backups/heartbeat-$(date +%F).tar.gz
```

| Flag | Meaning |
|---|---|
| `--data-dir DIR` | data directory (default as for `serve`) |
| `--out FILE` | output `.tar.gz`; `-` writes to stdout; default `heartbeat-backup-<UTC timestamp>.tar.gz` in the current directory. The file is created with mode `0600` |
| `--with-tsdb` | also include `tsdb/` (metrics). Stop the server first for a consistent copy |

The command can run while the server is running: the database is copied with `VACUUM INTO`, which gives a consistent snapshot under WAL. The archive contains `heartbeat.db`, `pki/`, `snmp-profiles/` (and `tsdb/` with the flag) plus a `MANIFEST`. It does **not** contain `vulndb/` (re-downloaded) or `acme/` (the certificate is re-issued; Let's Encrypt rate limits apply if you restore repeatedly).

### UI and API

**Settings, Server & backup** (`GET /api/v1/server/backup`, settings permission) downloads the same archive without metrics and records `server.backup_downloaded` in the audit log.

### Protect backups

They contain the CA private key, the job signing key and clear-text integration secrets ([security.md](security.md)). Encrypt them (for example `age` or `gpg`), store them off the server and test restores. A systemd timer is enough for scheduling:

```ini
# /etc/systemd/system/heartbeat-backup.service
[Service]
Type=oneshot
User=heartbeat
ExecStart=/usr/local/bin/heartbeat-server backup --data-dir /var/lib/heartbeat --out /var/backups/heartbeat/hb.tar.gz
```

## Restore

The server must be stopped.

```sh
sudo systemctl stop heartbeat
sudo -u heartbeat heartbeat-server restore --data-dir /var/lib/heartbeat --in hb.tar.gz --force
sudo systemctl start heartbeat
```

`--force` is required when the data directory already holds a database. Files are written next to their destination and renamed into place, stale `-wal`/`-shm` files are removed, and `pki/` files are written `0600`. Because the CA is restored, agents reconnect on their own, including on a new machine, as long as the server URL they were registered with resolves to it. The vulnerability database downloads again on start, and metrics are present only if the backup included `tsdb/`.

Run the commands as the service user (or fix ownership with `chown -R heartbeat:heartbeat /var/lib/heartbeat` afterwards).

## Forgotten password or lost second factor

```sh
sudo -u heartbeat heartbeat-server reset-password --data-dir /var/lib/heartbeat --user admin [--disable-2fa]
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

Disk budget: `tsdb/` (grows with hosts times series times retention) plus `vulndb/` (about 3 GB) plus `heartbeat.db`.

## Resource usage

Measured on a 2-vCPU, 3.8 GB Linux VPS running the server and its own agent, with 3 hosts (2 agents, 1 other), about 5 hours after start. Sizes and rates grow with hosts, series and retention, so treat them as a guide.

| Item | Measured |
|---|---|
| Server idle memory (RSS) | about 120 to 150 MB; the same with scanning off and with an existing vulnerability database opened |
| Server peak memory (VmHWM) | about 575 MB, reached while scanning hosts against the vulnerability database |
| Server CPU at idle | under 0.1% of one core (8 CPU-minutes over 5 hours) |
| Agent memory (RSS) | 13 to 15 MB (peak 22 MB) |
| Agent CPU | about 0.2% of one core over 5 minutes at the default 30 s interval |
| `heartbeat.db` | about 60 MB plus WAL with 48,000 vulnerability findings and 1,300 packages; a few MB without scanning |
| Metric store (`tsdb/`) | about 2 MB for 3 hosts after 10 hours; budget roughly 0.5 to 1 MB per host per day at default collection settings |
| Vulnerability database (`vulndb/`) | about 3.1 GB on disk, only when scanning is enabled |
| Server binary | about 116 MB (UI and agent binaries embedded; built with `-trimpath -s -w`) |
| Agent binary | about 14 MB |

Vulnerability scanning is off by default. While it is off, the server never downloads or opens the database. Enabling it (Settings, Vulnerabilities, or the setup wizard) downloads about 3 GB in the background. Disabling it releases the database; tick "Also delete the downloaded database" (`delete_db: true` in the request) to reclaim the disk space.

### Hardware recommendations

| Host | Scanning | CPU | RAM | Disk |
|---|---|---|---|---|
| Raspberry Pi 4 or a 1 GB VPS | Off (default) | 1 core | 1 GB | 2 GB plus metrics (about 1 MB per host per day) |
| Small server | On | 2 cores | 2 GB or more | 6 GB or more (the database is about 3 GB and is replaced on update) |
| Larger fleet (50+ hosts) with scanning | On | 2 to 4 cores | 4 GB | 10 GB or more |

## Logs and health

- Logs go to stderr: `journalctl -u heartbeat` (systemd) or `docker logs heartbeat`. `HEARTBEAT_LOG_LEVEL` is `debug`, `info` (default), `warn` or `error`; `HEARTBEAT_LOG_FORMAT=json` switches to JSON lines.
- `GET /healthz` returns `ok` (HTTP 200) when the database answers, 503 otherwise. `heartbeat-server healthcheck [--url http://127.0.0.1:8080/healthz]` wraps it for scripts and containers; if the URL is `http://` and fails it retries over `https://` with certificate verification skipped, so it keeps working after enabling TLS on the same port.
- The audit log records who did what; application logs record agent connects and disconnects, rejected jobs, notification failures and listener changes.
- To monitor Heartbeat itself, scrape `/metrics` (`hb_server_agents_connected`, `hb_alerts_firing`) and add an external HTTP check on `/healthz`.

## Upgrading

See [install.md](install.md#upgrading). In short: back up, replace the binary (or rebuild the image), let the server migrate the database at start, then upgrade agents from the UI. Check **Settings** and the host list afterwards; agents show their version.

## Server settings that are not in the UI

The listen address can be forced with `--listen` / `HEARTBEAT_LISTEN`, TLS can be overridden with `--force-http` ([tls-and-proxies.md](tls-and-proxies.md)), and the data directory is `--data-dir` / `HEARTBEAT_DATA_DIR`. All other settings live in the database and are edited in the UI or with `PUT /api/v1/settings/{section}` (sections: `general`, `tls`, `smtp`, `ldap`, `radius`, `security`, `retention`, `agent_defaults`, `alerting`, `vuln`).
