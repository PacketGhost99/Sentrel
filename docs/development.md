# Development

## Requirements

- Go 1.26 (see `go.mod`) and Node.js 22 with npm (the Dockerfile uses `node:22`).
- For the release build: bash, and optionally `wixl` (msitools) for the Windows MSI.
- For protobuf changes: `protoc` with `protoc-gen-go` and `protoc-gen-go-grpc`.

The server embeds the compiled UI (`web/dist`, via `go:embed all:dist`), so `go build` and `go test` for the server need `web/dist` to exist. Build the UI once first (`cd web && npm ci && npm run build`).

## Repository layout

```
cmd/heartbeat-server/   server CLI: serve, backup, restore, reset-password, healthcheck, version
cmd/heartbeat-agent/    agent CLI: register, run, self-test, status, service, priv, version
server/app/             the server: routes.go (every REST endpoint), api*.go handlers, hub.go (agent gRPC),
                        alerting.go, notifier.go, checks.go, jobs.go, ingest.go, http.go (listeners, TLS),
                        settings.go, thresholds.go, housekeeping.go, snmppoll.go, vulnscan.go, auth.go
server/db/              SQLite open + goose migrations (migrations/*.sql, embedded)
server/metrics/         embedded Prometheus TSDB wrapper and PromQL
server/pki/             internal CA, agent certificates, job signing key
server/notify/          notification channels
server/probe/           ping/tcp/http/cert/dns probes
server/snmp/            SNMP client and profile engine; profiles/*.yaml are embedded built-ins
server/vuln/            Grype integration
server/pushinstall/     SSH and WinRM push installer
server/authn/           password hashing, LDAP, RADIUS
server/agentdist/       agent binaries embedded in the server (filled by scripts/build.sh)
agent/                  the agent: collector, inventory, security, jobs, priv helper, transport (per-OS files)
internal/sigs/          message formats signed by the server and checked by agents
internal/version/       build metadata set by -ldflags
proto/heartbeat/v1/     agent.proto
gen/heartbeatv1/        generated protobuf and gRPC code (committed)
web/                    React 19 + Vite + Tailwind UI (TypeScript)
deploy/                 install-server.sh, install-agent.sh, install-agent.ps1, Dockerfile, docker-compose.yml
scripts/                build.sh, e2e.sh, sync.sh, dev-deploy.sh
```

Adding an API endpoint means adding a `routeDef` in `server/app/routes.go`; the OpenAPI document and permission checks are generated from that table. Settings sections and their defaults live in `server/app/settings.go`; new metrics that should accept thresholds go in the catalog in `server/app/thresholds.go`. Database changes need a new numbered goose migration in `server/db/migrations/`.

## Building

```sh
scripts/build.sh [--version X.Y.Z] [--skip-web] [--server-arch "amd64 arm64"]
```

Steps: build the UI (unless `--skip-web`), cross-compile agents for `linux/amd64`, `linux/arm64` and `windows/amd64` (`CGO_ENABLED=0`, `-trimpath`), copy them into `server/agentdist/bin/` so they are embedded, build the MSI if `wixl` exists, build `heartbeat-server-linux-<arch>`, copy the installer scripts and write `dist/SHA256SUMS`. The version defaults to `git describe`. The agent builds are rebuilt before the server on purpose: the server serves and signs exactly the agent binaries it embeds. Output is in `dist/`.

Docker: `docker build -f deploy/Dockerfile -t heartbeat .` (build argument `VERSION`).

### Running a development server

```sh
(cd web && npm ci && npm run build)
go run ./cmd/heartbeat-server serve --data-dir ./.data --listen 127.0.0.1:8080
# UI hot reload, proxying /api, /download and /install to the server:
(cd web && HEARTBEAT_URL=http://127.0.0.1:8080 npm run dev)
```

`--dev-ui web/dist` serves the UI from disk, and `--agent-bin-dir dist` serves agent binaries from a directory instead of the embedded ones. Create the first user through the setup wizard using the token from `.data/setup-token`.

### Remote build host

`scripts/sync.sh` mirrors the working tree to a Linux machine over SSH (default host `HeartbeatSandbox`, directory `heartbeat`; override with `HB_BUILD_HOST` and `HB_REMOTE_DIR`):

```sh
scripts/sync.sh push                       # copy sources
scripts/sync.sh run 'go test ./...'        # run in the remote checkout (with ~/go/bin on PATH)
scripts/sync.sh pull gen/                  # copy generated files back
scripts/dev-deploy.sh --version 0.9.0-dev  # push, build, install and restart the systemd service there
```

## Tests

```sh
go test ./...                              # unit and in-process integration tests
(cd web && npm run typecheck)            # TypeScript check (`npm test` runs vitest; there are no UI tests yet)
```

`server/app/integration_test.go` starts the whole server in-process against a temporary data directory and drives the REST API.

### SNMP live test

`server/snmp/live_test.go` polls a real device and is skipped unless `HB_SNMP_ADDR` is set:

| Variable | Meaning |
|---|---|
| `HB_SNMP_ADDR` | target address, for example a local net-snmp `snmpd` (required to run the test) |
| `HB_SNMP_COMMUNITY` | v2c community |
| `HB_SNMP_V3_USER`, `HB_SNMP_V3_AUTH`, `HB_SNMP_V3_PRIV` | enable the v3 case (`authPriv`, SHA256 and AES) |

```sh
HB_SNMP_ADDR=127.0.0.1 HB_SNMP_COMMUNITY=public go test ./server/snmp -run TestPollLive -v
```

The test expects `cpu_usage_percent`, `mem_used_percent` and `disk_used_percent` metrics, so the device should expose HOST-RESOURCES or UCD-SNMP data, and it checks that wrong credentials fail.

### End-to-end test

`scripts/e2e.sh` exercises a real running server and a connected Linux agent. Run it on the agent host:

```sh
HB_URL=http://127.0.0.1:8080 HB_USER=admin HB_PASSWORD=... scripts/e2e.sh
```

Needs `curl`, `jq`, `python3`, `nft` and passwordless `sudo` for `nft`; the host must run `sshd`, a Docker container named `hb-test-nginx`, and have Internet access (a certificate check and a DNS check use public names). It creates checks, a webhook channel, maintenance windows, IP blocks, jobs and a status page, all prefixed `e2e-`, verifies alerts, notifications, thresholds, nftables blocking (with a temporary opt-in), upgrade dry-runs, the status page, `/metrics`, backup contents and the audit log, and removes what it created. It changes the alerting settings (`group_wait_seconds` 5) and sets the host's `allow_ip_block` back to false, so do not run it against a production server.

## Protocol buffers

The agent protocol is `proto/heartbeat/v1/agent.proto`. Fields are only ever added, never renumbered; a breaking change bumps `SchemaVersion`. After editing it, regenerate the committed code from the repository root:

```sh
protoc -I proto --go_out=. --go_opt=module=heartbeat --go-grpc_out=. --go-grpc_opt=module=heartbeat proto/heartbeat/v1/agent.proto
```

Install the plugins with `go install google.golang.org/protobuf/cmd/protoc-gen-go@latest` and `go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest`. Output goes to `gen/heartbeatv1/`. On the remote build host: `scripts/sync.sh push`, run the command with `scripts/sync.sh run '...'`, then `scripts/sync.sh pull gen/`.

## Licences of dependencies

When you add a Go or npm dependency, check its licence (`go-licenses report ./cmd/...`, `npx license-checker --production --summary`) and update `NOTICE`. Strong-copyleft licences (GPL, AGPL, LGPL, SSPL) are not accepted; MPL-2.0 is acceptable if listed.
