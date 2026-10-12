# Heartbeat: notes for Claude

A self-hosted monitoring server, an agent and a React UI in one Go module (`heartbeat`). Read this before exploring.

## Layout
- `server/app/`: everything server-side.
  - `routes.go`: every REST route with its permission. Start here.
  - `api_*.go`: handlers. `models.go`: DB structs.
  - `alerting.go`: threshold evaluation, alert state machine.
  - `notifier.go`: channel routing; muted alerts are skipped in `enqueue`.
  - `jobs.go`: job types, validation, signing, results.
  - `ingest.go`: agent reports.
  - `vulnscan.go`: Grype. `disk.go`: disk reports and cleanup. `api_beats.go`: beat bars, notify toggle.
  - `housekeeping.go`: pruning.
- `server/db/migrations/`: goose SQL. Add a new numbered file; never edit old ones.
- `agent/`: collectors and job handlers in `jobs.go`.
  - Root-only work goes through `heartbeat-agent priv <verb>` (`priv*.go`, sudoers allows only `priv *`).
  - The priv verbs validate all args against allowlists and never use a shell.
- `web/src/`:
  - Primitives in `components/ui.tsx` (Card, Button, Badge, Table, Modal…).
  - Shell in `components/Layout.tsx`. `components/BeatBar.tsx`.
  - API types in `lib/types.ts`. Pages in `pages/`, host tabs in `pages/host/`.
  - Theme tokens in `index.css` (light default, `.dark` class).
- `docs/*.md`: user docs. Keep them in step with features.

## Build and test
There's no Go or Node on the Windows laptop. Build and test on the VPS (`HeartbeatSandbox`):
- `scripts/sync.sh push`: copy the tree to `~/heartbeat`.
- `scripts/sync.sh run '<cmd>'`: run in that dir, e.g.:
  - `go build ./... && go vet ./... && go test ./server/... ./agent/...`
  - `GOOS=windows go vet ./agent/...`
  - `cd web && npm run build && npx vitest run`
- `bash scripts/dev-deploy.sh` (from the laptop): builds, installs and restarts the server.
  - The local agent is NOT updated, because versions match. To update it: `sudo install -m0755 dist/heartbeat-agent-linux-amd64 /usr/local/bin/heartbeat-agent && sudo systemctl restart heartbeat-agent`.
- e2e, on the VPS: `set -a; . ~/.hb-dev-credentials; set +a; HB_USER=$HB_TEST_ADMIN HB_PASSWORD=$HB_TEST_PASSWORD bash scripts/e2e.sh`.
  - It runs against the live server and the VPS's own agent, and restores host flags it changes.
- Pipe build/test output through `tail`/`grep FAIL` to keep context small.
- The VPS has only 2 cores and 3.8 GB RAM, and parallel builds get OOM-killed. Wrap every VPS build/test in the shared lock: `scripts/sync.sh run 'flock ~/hb-build.lock env GOFLAGS=-p=1 <cmd>'`.
  - Test only the packages you touched until the final check.
  - Never compile tools like syft/nfpm from source there; use prebuilt binaries.
  - Run at most 2 agents that build on the VPS at once.

## Conventions
- Every table has `tenant_id`; tenant 0 is the provider.
  - Check `p.CanTenant(...)` in handlers.
  - Permissions: read, write, jobs, host_settings, scripts, admin.
- Destructive jobs need a host opt-in flag (`allow_upgrades`, `allow_cleanup`, `allow_scripts`, `allow_ip_block`) plus `confirm`.
- JSON is snake_case; times are unix seconds.
- User-facing text is neutral product copy: no notes to self, no personal deployment details.
- Commit only when asked. The repo is github.com/notspark1999/Heartbeat, branch main.
