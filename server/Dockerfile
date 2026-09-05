FROM node:24-bookworm-slim@sha256:a9f5f7c91a432850b2a8a7797adf5eadb6c733ceed61167806cee7ea7fbc29df AS web

WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN npm ci

COPY web/ ./
RUN npm run build

FROM golang:1.27.1-bookworm@sha256:648f440f42a0958804efb24df176f806f9d353b41f1c0627f666428e40310f6b AS build

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

FROM debian:trixie-slim@sha256:d7e12182ce18b85b93007c1dedf31f2d29e01ccf3182cc4017c709b6259bc132
ARG DEBIAN_SNAPSHOT=20260825T000000Z
COPY --from=build /etc/ssl/certs/ca-certificates.crt /usr/local/share/rivune-bootstrap-ca.crt
RUN printf '%s\n' \
      "deb [check-valid-until=no] https://snapshot.debian.org/archive/debian/${DEBIAN_SNAPSHOT}/ trixie main" \
      "deb [check-valid-until=no] https://snapshot.debian.org/archive/debian/${DEBIAN_SNAPSHOT}/ trixie-updates main" \
      "deb [check-valid-until=no] https://snapshot.debian.org/archive/debian-security/${DEBIAN_SNAPSHOT}/ trixie-security main" \
      > /etc/apt/sources.list \
    && rm -f /etc/apt/sources.list.d/debian.sources \
    && printf 'Acquire::https::CaInfo "/usr/local/share/rivune-bootstrap-ca.crt";\n' > /etc/apt/apt.conf.d/99rivune-bootstrap-ca \
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
    && rm -f /etc/apt/apt.conf.d/99rivune-bootstrap-ca /usr/local/share/rivune-bootstrap-ca.crt \
    && rm -rf /var/lib/apt/lists/*
ENV XDG_CACHE_HOME=/tmp/.cache

COPY --from=build /out/rivune /usr/local/bin/rivune
COPY --chmod=755 server/docker-entrypoint.sh /usr/local/bin/docker-entrypoint.sh
EXPOSE 8080
HEALTHCHECK --interval=10s --timeout=3s --start-period=20s --retries=5 CMD ["/usr/local/bin/docker-entrypoint.sh", "--healthcheck"]
ENTRYPOINT ["/usr/local/bin/docker-entrypoint.sh"]
CMD ["/usr/local/bin/rivune"]
