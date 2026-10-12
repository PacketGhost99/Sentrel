# Installing the server

Heartbeat runs on Linux. The release artifacts (built by `scripts/build.sh`) are `heartbeat-server-linux-<arch>`, `install-server.sh`, `install-agent.sh`, `install-agent.ps1` and `SHA256SUMS`. The server binary embeds the web UI and the agent builds (Linux amd64/arm64, Windows amd64), so a single file is all you deploy.

## Linux with systemd

```sh
sha256sum -c SHA256SUMS --ignore-missing
sudo sh install-server.sh --binary ./heartbeat-server-linux-amd64
```

Options:

| Option | Meaning | Default |
|---|---|---|
| `--binary PATH` | install a local binary | |
| `--url URL` | download the binary (use with `--sha256 HEX`) | |
| `--listen ADDR` | listen address | `0.0.0.0:8080` |
| `--data-dir DIR` | data directory | `/var/lib/heartbeat` |
| `--uninstall` | stop and remove the service and binary; the data directory is kept | |

One of `--binary` or `--url` is required, and the script must run as root. What it does:

1. Verifies the binary runs (`heartbeat-server version`).
2. Creates the system user `heartbeat` and the data directory (`0750`).
3. Installs `/usr/local/bin/heartbeat-server` and runs `setcap 'cap_net_raw,cap_net_bind_service+ep'` on it.
4. Writes `/etc/systemd/system/heartbeat.service`, enables and starts it, waits for `/healthz`, and prints the URL and the first-run setup token.

The unit runs `heartbeat-server serve --data-dir <dir> --listen <addr>` as `heartbeat` with `Restart=always`, `AmbientCapabilities=CAP_NET_RAW CAP_NET_BIND_SERVICE`, `NoNewPrivileges`, `ProtectSystem=strict`, `ProtectHome`, `PrivateTmp` and `ReadWritePaths=<data dir>`.

Logs: `journalctl -u heartbeat -f`. Set `HEARTBEAT_LOG_LEVEL` (`debug`, `info`, `warn`, `error`) or `HEARTBEAT_LOG_FORMAT=json` with a systemd drop-in (`systemctl edit heartbeat`).

### Capabilities

| Capability | Why |
|---|---|
| `cap_net_raw` | ICMP ping checks. Without it the server falls back to unprivileged ICMP, which works only if `net.ipv4.ping_group_range` covers the server's group; otherwise ping checks report "ICMP not permitted". |
| `cap_net_bind_service` | Binding ports 80/443 (Let's Encrypt mode, or `--listen :443`). |

If `setcap` is missing (install `libcap2-bin`), the systemd ambient capabilities still apply. A `setcap` grant is lost whenever the binary is replaced; the installer re-applies it on every run.

## Docker

```sh
docker compose -f deploy/docker-compose.yml up -d --build
docker compose -f deploy/docker-compose.yml logs heartbeat | grep -i "setup token"
```

The image (`deploy/Dockerfile`) is a multi-stage build (Node for the UI, Go for the binaries, Debian slim at runtime). It runs as UID 10001, stores data in the `/data` volume, exposes 8080, and has a `healthcheck` using `heartbeat-server healthcheck`. The compose file adds `NET_RAW` for ICMP checks and mounts the `heartbeat-data` volume. For Let's Encrypt, publish ports 80 and 443 instead of 8080 and pass `--listen 0.0.0.0:443` in `command`.

To run the image directly:

```sh
docker run -d --name heartbeat --cap-add NET_RAW -p 8080:8080 -v heartbeat-data:/data heartbeat:latest
```

## Running by hand

```sh
heartbeat-server serve --data-dir ./data --listen 0.0.0.0:8080
```

| Flag | Meaning |
|---|---|
| `--data-dir DIR` | data directory. Default: `$HEARTBEAT_DATA_DIR`, else `/var/lib/heartbeat` when root or writable, else `~/.heartbeat` |
| `--listen ADDR` | listen address; overrides the stored setting (`$HEARTBEAT_LISTEN`) |
| `--force-http` | ignore the stored TLS mode and serve plain HTTP (recovery) |
| `--agent-bin-dir DIR` | serve agent binaries from a directory instead of the embedded ones |
| `--dev-ui DIR` | serve the UI from a directory (development) |

Other commands: `backup`, `restore`, `reset-password`, `healthcheck`, `version` (see [operations.md](operations.md)).

## First start

On first start with no users, the server creates a one-time setup token, logs it, prints it to stderr and writes it to `<data dir>/setup-token` (mode `0600`). The setup wizard needs this token to create the first administrator; after setup the file is removed. If the token is lost, read the file, or restart the server (it reuses the file while setup is pending).

## Data directory

```
<data dir>/
  heartbeat.db (+ -wal, -shm)   SQLite: settings, hosts, alerts, audit log, findings, sessions
  pki/                          ca.crt, ca.key, job.key (Ed25519), server.crt/server.key/server.sans (self-signed mode)
  tsdb/                         Prometheus TSDB blocks (metrics)
  vulndb/                       Grype vulnerability database (around 3 GB)
  acme/                         Let's Encrypt account and certificate cache (ACME mode)
  snmp-profiles/                custom SNMP profile YAML files
  setup-token                   only until the first admin exists
```

Everything the server needs to restart elsewhere is in `heartbeat.db` plus `pki/`. The `pki/` directory holds the keys that authenticate every agent and sign every job: treat it like a secret and back it up (see [operations.md](operations.md)). Plan disk space for `vulndb/` and `tsdb/`; the server raises an alert when the data disk passes the configured percentage (default 85).

## Ports and firewall

| Port | Direction | Purpose |
|---|---|---|
| 8080 (configurable) | inbound to server | UI, REST API, agent gRPC, installers, `/metrics`, `/healthz` |
| 80, 443 | inbound to server | only in Let's Encrypt mode (443 serves everything, 80 does HTTP-01 and redirects) |
| 161/udp | outbound from server | SNMP polling (configurable per device) |
| 443/tcp | outbound from server | vulnerability database download, notification services |

Agents only need outbound access to the server port. The agent honours `HTTPS_PROXY` and `NO_PROXY`.

## Upgrading

1. Take a backup (`heartbeat-server backup`, see [operations.md](operations.md)).
2. Run `install-server.sh --binary <new binary>` again (or pull and rebuild the container: `docker compose up -d --build`). The script stops the service, replaces the binary, re-applies capabilities and starts it. Database migrations run automatically at startup; there is no automated downgrade, so restore the backup to roll back.
3. Upgrade agents from the UI (host page, update agent) or per host with a job. The new server embeds its own agent builds and signs them. Agents older than the server keep working while the protocol schema version matches.

## Uninstalling

```sh
sudo sh install-server.sh --uninstall      # removes the unit and /usr/local/bin/heartbeat-server
sudo rm -rf /var/lib/heartbeat             # only if you want to delete all data
sudo userdel heartbeat
```

Docker: `docker compose -f deploy/docker-compose.yml down -v` (the `-v` deletes the data volume).
