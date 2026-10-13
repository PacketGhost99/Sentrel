# Installing the server

Sentrel runs on Linux. The release artifacts (built by `scripts/build.sh`) are `sentrel-server-linux-<arch>`, the `.deb`/`.rpm` packages, `install-server.sh`, `install-agent.sh`, `install-agent.ps1` and `SHA256SUMS`. The server binary embeds the web UI, the agent builds (Linux amd64/arm64, Windows amd64) and, when `nfpm` and `wixl` were available at build time, the agent `.deb`, `.rpm` and `.msi` packages, so a single file is all you deploy. The agent packages are offered for download on the **Agents** page; server packages are never embedded.

## Linux with systemd

```sh
sha256sum -c SHA256SUMS --ignore-missing
sudo sh install-server.sh --binary ./sentrel-server-linux-amd64
```

Options:

| Option | Meaning | Default |
|---|---|---|
| `--binary PATH` | install a local binary | |
| `--url URL` | download the binary (use with `--sha256 HEX`) | |
| `--listen ADDR` | listen address | `0.0.0.0:8080` |
| `--data-dir DIR` | data directory | `/var/lib/sentrel` |
| `--uninstall` | stop and remove the service and binary; the data directory is kept | |

One of `--binary` or `--url` is required, and the script must run as root. What it does:

1. Verifies the binary runs (`sentrel-server version`).
2. Creates the system user `sentrel` and the data directory (`0750`).
3. Installs `/usr/local/bin/sentrel-server` and runs `setcap 'cap_net_raw,cap_net_bind_service+ep'` on it.
4. Writes `/etc/systemd/system/sentrel.service`, enables and starts it, waits for `/healthz`, and prints the URL and the first-run setup token.

The unit runs `sentrel-server serve --data-dir <dir> --listen <addr>` as `sentrel` with `Restart=always`, `AmbientCapabilities=CAP_NET_RAW CAP_NET_BIND_SERVICE`, `NoNewPrivileges`, `ProtectSystem=strict`, `ProtectHome`, `PrivateTmp` and `ReadWritePaths=<data dir>`.

Logs: `journalctl -u sentrel -f`. Set `SENTREL_LOG_LEVEL` (`debug`, `info`, `warn`, `error`) or `SENTREL_LOG_FORMAT=json` with a systemd drop-in (`systemctl edit sentrel`).

### Capabilities

| Capability | Why |
|---|---|
| `cap_net_raw` | ICMP ping checks. Without it the server falls back to unprivileged ICMP, which works only if `net.ipv4.ping_group_range` covers the server's group; otherwise ping checks report "ICMP not permitted". |
| `cap_net_bind_service` | Binding ports 80/443 (Let's Encrypt mode, or `--listen :443`). |

If `setcap` is missing (install `libcap2-bin`), the systemd ambient capabilities still apply. A `setcap` grant is lost whenever the binary is replaced; the installer re-applies it on every run.

## Linux packages (.deb and .rpm)

Each release attaches `.deb` and `.rpm` packages for the agent and the server, for amd64 and arm64. Download them from the GitHub release page (and verify them, see [Verifying downloads](#verifying-downloads)).

```sh
# Debian, Ubuntu
sudo apt install ./sentrel-server_<version>_amd64.deb
# RHEL, Rocky, Alma, Fedora
sudo dnf install ./sentrel-server-<version>-1.x86_64.rpm
```

Server package: installs `/usr/local/bin/sentrel-server` (with `cap_net_raw` and `cap_net_bind_service` when `setcap` is available), the systemd unit `sentrel.service`, the `sentrel` user and `/var/lib/sentrel`. It does not start the service on a fresh install (an upgrade restarts a running one):

```sh
sudo systemctl enable --now sentrel
sudo cat /var/lib/sentrel/setup-token
```

Agent package (`sentrel-agent`): installs `/usr/local/bin/sentrel-agent`, the `sentrel-agent` user (added to `adm` and `systemd-journal` when they exist), the sudoers rule that lets the agent run only `sentrel-agent priv *` as root, and the systemd unit. It does not register the host. Take a one-time token from the **Agents** page in the UI (which also offers this package for download and generates the commands) and run:

```sh
sudo sentrel-agent register --server https://SERVER --token ABCD-1234
sudo systemctl enable --now sentrel-agent
```

Removing the package stops and disables the service. Config, state and the user are kept. Container monitoring needs the agent user in the `docker` group, which is root-equivalent: add it yourself (`sudo usermod -aG docker sentrel-agent`) if you want it.

Build the packages yourself with `scripts/build.sh --packages` (needs Go; nfpm is fetched with `go run`). Without `--packages`, `scripts/build.sh` still builds and embeds the agent packages when `nfpm` (and `wixl` for the MSI) are on the PATH, and just lists fewer packages otherwise. All product, binary and package names are variables at the top of that script.

### APT and YUM repositories

There is no hosted package repository yet; install the packages from the release page. A signed repository (for example on GitHub Pages) would let `apt upgrade` and `dnf upgrade` keep hosts current.

## Container image (GHCR)

Images are published for linux/amd64 and linux/arm64 as `ghcr.io/packetghost99/sentrel` (the repository name in lower case) with tags `edge` (latest main), the release tag (for example `v1.2.0`) and `latest` (latest release).

```sh
docker run -d --name sentrel --cap-add NET_RAW -p 8080:8080 -v sentrel-data:/data \
  ghcr.io/packetghost99/sentrel:latest
```

In the compose file, replace the `build:` section with `image: ghcr.io/packetghost99/sentrel:latest`.

## Verifying downloads

Releases carry several independent checks:

- `SHA256SUMS` lists every binary, package and installer: `sha256sum -c SHA256SUMS --ignore-missing`.
- `SHA256SUMS.sig` is the project's own signature (checked by the server and agents when they self-update, with `sentrel-sign verify`).
- `SHA256SUMS.cosign.bundle` is a keyless [Sigstore](https://www.sigstore.dev) signature made by the release workflow. Check that the checksums came from this repository's workflow, then check the files against them:

```sh
cosign verify-blob \
  --bundle SHA256SUMS.cosign.bundle \
  --certificate-identity-regexp '^https://github.com/PacketGhost99/Sentrel/\.github/workflows/ci\.yml@refs/tags/v' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com \
  SHA256SUMS
sha256sum -c SHA256SUMS --ignore-missing
```

- `<binary>.spdx.json` is an SPDX software bill of materials for each binary.
- The container image is signed keyless as well:

```sh
cosign verify ghcr.io/packetghost99/sentrel:<tag> \
  --certificate-identity-regexp '^https://github.com/PacketGhost99/Sentrel/\.github/workflows/ci\.yml@' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com
```

Replace `PacketGhost99/Sentrel` with the GitHub repository (case-sensitive in the certificate identity).

## Docker

```sh
docker compose -f deploy/docker/docker-compose.yml up -d --build
docker compose -f deploy/docker/docker-compose.yml logs sentrel | grep -i "setup token"
```

The image (`deploy/docker/Dockerfile`) is a multi-stage build (Node for the UI, Go for the binaries, Debian slim at runtime). It runs as UID 10001, stores data in the `/data` volume, exposes 8080, and has a `healthcheck` using `sentrel-server healthcheck`. The compose file adds `NET_RAW` for ICMP checks and mounts the `sentrel-data` volume. For Let's Encrypt, publish ports 80 and 443 instead of 8080 and pass `--listen 0.0.0.0:443` in `command`.

To run the image directly:

```sh
docker run -d --name sentrel --cap-add NET_RAW -p 8080:8080 -v sentrel-data:/data sentrel:latest
```

## Running by hand

```sh
sentrel-server serve --data-dir ./data --listen 0.0.0.0:8080
```

| Flag | Meaning |
|---|---|
| `--data-dir DIR` | data directory. Default: `$SENTREL_DATA_DIR`, else `/var/lib/sentrel` when root or writable, else `~/.sentrel` |
| `--listen ADDR` | listen address; overrides the stored setting (`$SENTREL_LISTEN`) |
| `--force-http` | ignore the stored TLS mode and serve plain HTTP (recovery) |
| `--agent-bin-dir DIR` | serve agent binaries from a directory instead of the embedded ones |
| `--dev-ui DIR` | serve the UI from a directory (development) |

Other commands: `backup`, `restore`, `reset-password`, `healthcheck`, `version` (see [operations.md](operations.md)).

## First start

On first start with no users, the server creates a one-time setup token, logs it, prints it to stderr and writes it to `<data dir>/setup-token` (mode `0600`). The setup wizard needs this token to create the first administrator; after setup the file is removed. If the token is lost, read the file, or restart the server (it reuses the file while setup is pending).

## Data directory

```
<data dir>/
  sentrel.db (+ -wal, -shm)   SQLite: settings, hosts, alerts, audit log, findings, sessions
  pki/                          ca.crt, ca.key, job.key (Ed25519), server.crt/server.key/server.sans (self-signed mode)
  tsdb/                         Prometheus TSDB blocks (metrics)
  vulndb/                       Grype vulnerability database (around 3 GB)
  acme/                         Let's Encrypt account and certificate cache (ACME mode)
  snmp-profiles/                custom SNMP profile YAML files
  setup-token                   only until the first admin exists
```

Everything the server needs to restart elsewhere is in `sentrel.db` plus `pki/`. The `pki/` directory holds the keys that authenticate every agent and sign every job: treat it like a secret and back it up (see [operations.md](operations.md)). Plan disk space for `vulndb/` and `tsdb/`; the server raises an alert when the data disk passes the configured percentage (default 85).

## Ports and firewall

| Port | Direction | Purpose |
|---|---|---|
| 8080 (configurable) | inbound to server | UI, REST API, agent gRPC, installers, `/metrics`, `/healthz` |
| 80, 443 | inbound to server | only in Let's Encrypt mode (443 serves everything, 80 does HTTP-01 and redirects) |
| 161/udp | outbound from server | SNMP polling (configurable per device) |
| 443/tcp | outbound from server | version check (`api.github.com`) and update download (`github.com`), vulnerability database download, notification services |

Agents only need outbound access to the server port. The agent honours `HTTPS_PROXY` and `NO_PROXY`.

## Upgrading

On a systemd install an administrator can update from **Settings, Updates** with one click; see [Updating](operations.md#updating) for every method and the signing model. The manual steps are:

1. Take a backup (`sentrel-server backup`, see [operations.md](operations.md)).
2. Run `install-server.sh --binary <new binary>` again (or pull and rebuild the container: `docker compose up -d --build`). The script stops the service, replaces the binary, re-applies capabilities and starts it. Database migrations run automatically at startup; there is no automated downgrade, so restore the backup to roll back.
3. Upgrade agents from the UI (host page, update agent) or per host with a job. The new server embeds its own agent builds and signs them. Agents older than the server keep working while the protocol schema version matches.

## Uninstalling

```sh
sudo sh install-server.sh --uninstall      # removes the unit and /usr/local/bin/sentrel-server
sudo rm -rf /var/lib/sentrel             # only if you want to delete all data
sudo userdel sentrel
```

Docker: `docker compose -f deploy/docker/docker-compose.yml down -v` (the `-v` deletes the data volume).
