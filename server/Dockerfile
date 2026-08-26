FROM node:24-bookworm-slim@sha256:d7e12182ce18b85b93007c1dedf31f2d29e01ccf3182cc4017c709b6259bc132 AS web

WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN npm ci

COPY web/ ./
RUN npm run build

FROM golang:1.26.6-bookworm@sha256:a9f5f7c91a432850b2a8a7797adf5eadb6c733ceed61167806cee7ea7fbc29df AS build

WORKDIR /src/server
COPY server/go.mod server/go.sum ./
RUN go mod download

COPY server/ ./
COPY --from=web /src/server/internal/webui/dist ./internal/webui/dist
ARG VERSION=dev
RUN CGO_ENABLED=0 go build \
    -trimpath \
    -ldflags="-s -w -X main.version=${VERSION}" \
    -o /out/rivune \
    ./cmd/rivune

FROM debian:trixie-slim@sha256:116d58cbd88c1297624acc6e967a060012422bacf9930927e23fb719189c6f36
ARG DEBIAN_SNAPSHOT=20260825T000000Z
RUN printf '%s\n' \
      "deb [check-valid-until=no] https://snapshot.debian.org/archive/debian/${DEBIAN_SNAPSHOT}/ trixie main" \
      "deb [check-valid-until=no] https://snapshot.debian.org/archive/debian/${DEBIAN_SNAPSHOT}/ trixie-updates main" \
      "deb [check-valid-until=no] https://snapshot.debian.org/archive/debian-security/${DEBIAN_SNAPSHOT}/ trixie-security main" \
      > /etc/apt/sources.list \
    && rm -f /etc/apt/sources.list.d/debian.sources \
    && apt-get update \
    && apt-get install -y --no-install-recommends \
      ca-certificates=20250419 \
      ffmpeg=7:7.1.5-0+deb13u1 \
      mesa-va-drivers=25.0.7-2+deb13u1 \
      mesa-vulkan-drivers=25.0.7-2+deb13u1 \
      tzdata=2026b-0+deb13u1 \
    && if [ "$(dpkg --print-architecture)" = "amd64" ]; then \
        apt-get install -y --no-install-recommends intel-media-va-driver=25.2.3+dfsg1-1; \
    fi \
    && rm -rf /var/lib/apt/lists/*
ENV XDG_CACHE_HOME=/tmp/.cache

COPY --from=build /out/rivune /usr/local/bin/rivune
COPY --chmod=755 server/docker-entrypoint.sh /usr/local/bin/docker-entrypoint.sh
EXPOSE 8080
HEALTHCHECK --interval=10s --timeout=3s --start-period=20s --retries=5 CMD ["/usr/local/bin/docker-entrypoint.sh", "--healthcheck"]
ENTRYPOINT ["/usr/local/bin/docker-entrypoint.sh"]
CMD ["/usr/local/bin/rivune"]
