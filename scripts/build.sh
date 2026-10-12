#!/usr/bin/env bash
# Build release artifacts into dist/.
#
#   scripts/build.sh [--version X.Y.Z] [--skip-web] [--server-arch "amd64 arm64"] [--packages]
#
# Steps: web UI (Vite) -> agents (linux/amd64, linux/arm64, windows/amd64)
# -> agents embedded into the server -> server binaries -> SHA256SUMS.
# An MSI is built with wixl (msitools) when available.
# --packages also builds .deb and .rpm packages (agent and server, amd64 and
# arm64) with nfpm, and builds the server for both architectures.
set -euo pipefail
cd "$(dirname "$0")/.."

# ---- Names: the only place product, binary and package names are defined. ----
# Each can be overridden from the environment (the CI workflow does this).
PRODUCT_TITLE="${PRODUCT_TITLE:-Sentrel}"
AGENT_BIN="${AGENT_BIN:-sentrel-agent}"        # binary name, ./cmd/<AGENT_BIN>
SERVER_BIN="${SERVER_BIN:-sentrel-server}"     # binary name, ./cmd/<SERVER_BIN>
AGENT_PKG="${AGENT_PKG:-$AGENT_BIN}"             # deb/rpm package names
SERVER_PKG="${SERVER_PKG:-$SERVER_BIN}"
AGENT_USER="${AGENT_USER:-sentrel-agent}"      # system user
SERVER_USER="${SERVER_USER:-sentrel}"
SERVER_UNIT="${SERVER_UNIT:-sentrel}"          # systemd unit name of the server
FW_TABLE="${FW_TABLE:-sentrel}"                # nft table created by the agent
GO_MODULE="${GO_MODULE:-sentrel}"              # module path in go.mod
HOMEPAGE="${HOMEPAGE:-https://github.com/notspark1999/Sentrel}"
DOCS_URL="${DOCS_URL:-$HOMEPAGE}"
MAINTAINER="${MAINTAINER:-Sentrel maintainers <noreply@users.noreply.github.com>}"
LICENSE="${LICENSE:-proprietary}"
NFPM_VERSION="${NFPM_VERSION:-v2.41.3}"
# Install locations. The agent path is referenced by the sudoers rule.
AGENT_PATH="${AGENT_PATH:-/usr/local/bin/$AGENT_BIN}"
SERVER_PATH="${SERVER_PATH:-/usr/local/bin/$SERVER_BIN}"
AGENT_ETC="${AGENT_ETC:-/etc/$AGENT_BIN}"
AGENT_STATE="${AGENT_STATE:-/var/lib/$AGENT_BIN}"
AGENT_SHARE="${AGENT_SHARE:-/usr/share/$AGENT_BIN}"
SERVER_DATA="${SERVER_DATA:-/var/lib/$SERVER_UNIT}"
# -----------------------------------------------------------------------------

VERSION="${VERSION:-}"
SKIP_WEB=0
PACKAGES=0
SERVER_ARCHES="amd64"
while [ $# -gt 0 ]; do
  case "$1" in
    --version) VERSION="$2"; shift 2 ;;
    --skip-web) SKIP_WEB=1; shift ;;
    --server-arch) SERVER_ARCHES="$2"; shift 2 ;;
    --packages) PACKAGES=1; shift ;;
    *) echo "unknown option $1" >&2; exit 2 ;;
  esac
done
PKG_ARCHES="${PKG_ARCHES:-amd64 arm64}"   # architectures to package
[ "$PACKAGES" = 1 ] && SERVER_ARCHES="$PKG_ARCHES"
if [ -z "$VERSION" ]; then
  VERSION=$(git describe --tags --always --dirty 2>/dev/null || echo "0.0.0-dev")
fi
COMMIT=$(git rev-parse --short HEAD 2>/dev/null || echo none)
DATE=$(date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS="-s -w -X $GO_MODULE/internal/version.Version=$VERSION -X $GO_MODULE/internal/version.Commit=$COMMIT -X $GO_MODULE/internal/version.Date=$DATE"
export CGO_ENABLED=0

mkdir -p dist
echo "==> version $VERSION"

if [ "$SKIP_WEB" = 0 ]; then
  echo "==> web UI"
  (cd web && { [ -d node_modules ] || npm ci --no-audit --no-fund; } && npm run build)
fi
[ -f web/dist/index.html ] || { echo "web/dist missing: build the UI or drop --skip-web" >&2; exit 1; }

echo "==> agents"
find server/agentdist/bin -type f ! -name README.txt ! -name .gitignore -delete
for target in linux/amd64 linux/arm64 windows/amd64; do
  os=${target%/*}; arch=${target#*/}
  out="dist/$AGENT_BIN-$os-$arch"
  [ "$os" = windows ] && out="$out.exe"
  GOOS=$os GOARCH=$arch go build -trimpath -ldflags "$LDFLAGS" -o "$out" ./cmd/$AGENT_BIN
  cp "$out" server/agentdist/bin/
  echo "    $out ($(du -h "$out" | cut -f1))"
done
echo "$VERSION" > server/agentdist/bin/VERSION

if command -v wixl >/dev/null 2>&1; then
  echo "==> MSI"
  sed "s/@VERSION@/$(echo "$VERSION" | sed -E 's/^v//; s/[^0-9.].*$//; s/^$/0.0.0/')/" deploy/agent.wxs > dist/agent.wxs
  (cd dist && wixl -a x64 -o "$AGENT_BIN-$VERSION-x64.msi" agent.wxs && rm agent.wxs)
fi

echo "==> server"
for arch in $SERVER_ARCHES; do
  out="dist/$SERVER_BIN-linux-$arch"
  GOOS=linux GOARCH=$arch go build -trimpath -ldflags "$LDFLAGS" -o "$out" ./cmd/$SERVER_BIN
  echo "    $out ($(du -h "$out" | cut -f1))"
done
cp deploy/install-server.sh deploy/install-agent.sh deploy/install-agent.ps1 dist/

if [ "$PACKAGES" = 1 ]; then
  echo "==> packages (nfpm $NFPM_VERSION)"
  STAGE="$PWD/dist/.pkgstage"
  rm -rf "$STAGE"; mkdir -p "$STAGE/bin"
  cp -r packaging/agent packaging/server "$STAGE/"
  # Substitute the @NAME@ placeholders with the names defined above.
  subst="$STAGE/subst.sed"; : > "$subst"
  for v in PRODUCT_TITLE AGENT_BIN SERVER_BIN AGENT_PKG SERVER_PKG AGENT_USER SERVER_USER SERVER_UNIT \
           FW_TABLE HOMEPAGE DOCS_URL MAINTAINER LICENSE AGENT_PATH SERVER_PATH AGENT_ETC AGENT_STATE \
           AGENT_SHARE SERVER_DATA STAGE; do
    val=$(printf '%s' "${!v}" | sed 's/[\\&|]/\\&/g')
    printf "s|@%s@|%s|g
" "$v" "$val" >> "$subst"
  done

  find "$STAGE/agent" "$STAGE/server" -type f -exec sed -i -f "$subst" {} +
  for arch in $PKG_ARCHES; do
    cp "dist/$AGENT_BIN-linux-$arch" "dist/$SERVER_BIN-linux-$arch" "$STAGE/bin/"
  done
  NFPM="go run github.com/goreleaser/nfpm/v2/cmd/nfpm@$NFPM_VERSION"
  if command -v nfpm >/dev/null 2>&1 && [ -z "${NFPM_USE_GO_RUN:-}" ]; then NFPM=nfpm; fi
  for arch in $PKG_ARCHES; do
    for comp in agent server; do
      sed "s|@ARCH@|$arch|g" "$STAGE/$comp/nfpm.yaml" > "$STAGE/$comp/nfpm-$arch.yaml"
      for fmt in deb rpm; do
        ARCH=$arch VERSION="${VERSION#v}" $NFPM package --config "$STAGE/$comp/nfpm-$arch.yaml" --packager $fmt --target dist/
      done
    done
  done
  rm -rf "$STAGE"
fi

(cd dist && sha256sum "$AGENT_BIN"* "$SERVER_BIN"* install-* > SHA256SUMS)
echo "==> done: dist/"
