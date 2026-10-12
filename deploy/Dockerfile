# Heartbeat server image. Build from the repository root:
#   docker build -f deploy/Dockerfile -t heartbeat .
FROM --platform=$BUILDPLATFORM node:22-bookworm-slim AS web
WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN npm ci --no-audit --no-fund
COPY web/ ./
RUN npm run build

FROM --platform=$BUILDPLATFORM golang:1.26-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=web /src/web/dist ./web/dist
ARG VERSION=dev
ARG TARGETARCH
RUN scripts/build.sh --skip-web --server-arch "$TARGETARCH" --version "$VERSION" && cp "dist/heartbeat-server-linux-$TARGETARCH" /heartbeat-server

FROM debian:bookworm-slim
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates tzdata libcap2-bin \
    && rm -rf /var/lib/apt/lists/* \
    && useradd --system --uid 10001 --home-dir /data heartbeat \
    && mkdir -p /data && chown heartbeat:heartbeat /data
COPY --from=build /heartbeat-server /usr/local/bin/heartbeat-server
RUN setcap 'cap_net_raw,cap_net_bind_service+ep' /usr/local/bin/heartbeat-server
USER heartbeat
VOLUME /data
EXPOSE 8080
HEALTHCHECK --interval=30s --timeout=5s CMD ["/usr/local/bin/heartbeat-server", "healthcheck", "--url", "http://127.0.0.1:8080/healthz"]
ENTRYPOINT ["/usr/local/bin/heartbeat-server"]
CMD ["serve", "--data-dir", "/data", "--listen", "0.0.0.0:8080"]
