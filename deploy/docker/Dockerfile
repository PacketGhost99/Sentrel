# Sentrel server image. Build from the repository root:
#   docker build -f deploy/docker/Dockerfile -t sentrel .
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
RUN scripts/build.sh --skip-web --server-arch "$TARGETARCH" --version "$VERSION" && cp "dist/sentrel-server-linux-$TARGETARCH" /sentrel-server

FROM debian:bookworm-slim
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates tzdata libcap2-bin \
    && rm -rf /var/lib/apt/lists/* \
    && useradd --system --uid 10001 --home-dir /data sentrel \
    && mkdir -p /data && chown sentrel:sentrel /data
COPY --from=build /sentrel-server /usr/local/bin/sentrel-server
RUN setcap 'cap_net_raw,cap_net_bind_service+ep' /usr/local/bin/sentrel-server
USER sentrel
VOLUME /data
EXPOSE 8080
HEALTHCHECK --interval=30s --timeout=5s CMD ["/usr/local/bin/sentrel-server", "healthcheck", "--url", "http://127.0.0.1:8080/healthz"]
ENTRYPOINT ["/usr/local/bin/sentrel-server"]
CMD ["serve", "--data-dir", "/data", "--listen", "0.0.0.0:8080"]
