#!/usr/bin/env bash
# Build release artifacts into dist/.
#
#   scripts/build.sh [--version X.Y.Z] [--skip-web] [--server-arch "amd64 arm64"]
#
# Steps: web UI (Vite) -> agents (linux/amd64, linux/arm64, windows/amd64)
# -> agents embedded into the server -> server binaries -> SHA256SUMS.
# An MSI is built with wixl (msitools) when available.
set -euo pipefail
cd "$(dirname "$0")/.."

VERSION="${VERSION:-}"
SKIP_WEB=0
SERVER_ARCHES="amd64"
while [ $# -gt 0 ]; do
  case "$1" in
    --version) VERSION="$2"; shift 2 ;;
    --skip-web) SKIP_WEB=1; shift ;;
    --server-arch) SERVER_ARCHES="$2"; shift 2 ;;
    *) echo "unknown option $1" >&2; exit 2 ;;
  esac
done
if [ -z "$VERSION" ]; then
  VERSION=$(git describe --tags --always --dirty 2>/dev/null || echo "0.0.0-dev")
fi
COMMIT=$(git rev-parse --short HEAD 2>/dev/null || echo none)
DATE=$(date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS="-s -w -X heartbeat/internal/version.Version=$VERSION -X heartbeat/internal/version.Commit=$COMMIT -X heartbeat/internal/version.Date=$DATE"
export CGO_ENABLED=0

mkdir -p dist
echo "==> version $VERSION"

if [ "$SKIP_WEB" = 0 ]; then
  echo "==> web UI"
  (cd web && { [ -d node_modules ] || npm ci --no-audit --no-fund; } && npm run build)
fi
[ -f web/dist/index.html ] || { echo "web/dist missing: build the UI or drop --skip-web" >&2; exit 1; }

echo "==> agents"
rm -f server/agentdist/bin/heartbeat-agent-* server/agentdist/bin/VERSION
for target in linux/amd64 linux/arm64 windows/amd64; do
  os=${target%/*}; arch=${target#*/}
  out="dist/heartbeat-agent-$os-$arch"
  [ "$os" = windows ] && out="$out.exe"
  GOOS=$os GOARCH=$arch go build -trimpath -ldflags "$LDFLAGS" -o "$out" ./cmd/heartbeat-agent
  cp "$out" server/agentdist/bin/
  echo "    $out ($(du -h "$out" | cut -f1))"
done
echo "$VERSION" > server/agentdist/bin/VERSION

if command -v wixl >/dev/null 2>&1; then
  echo "==> MSI"
  sed "s/@VERSION@/$(echo "$VERSION" | sed -E 's/^v//; s/[^0-9.].*$//; s/^$/0.0.0/')/" deploy/agent.wxs > dist/agent.wxs
  (cd dist && wixl -a x64 -o "heartbeat-agent-$VERSION-x64.msi" agent.wxs && rm agent.wxs)
fi

echo "==> server"
for arch in $SERVER_ARCHES; do
  out="dist/heartbeat-server-linux-$arch"
  GOOS=linux GOARCH=$arch go build -trimpath -ldflags "$LDFLAGS" -o "$out" ./cmd/heartbeat-server
  echo "    $out ($(du -h "$out" | cut -f1))"
done
cp deploy/install-server.sh deploy/install-agent.sh deploy/install-agent.ps1 dist/

(cd dist && sha256sum heartbeat-* install-* > SHA256SUMS)
echo "==> done: dist/"
