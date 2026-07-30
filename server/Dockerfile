FROM node:22-bookworm-slim AS web

WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN npm ci

COPY web/ ./
RUN npm run build

FROM golang:1.26-bookworm AS build

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

FROM debian:bookworm-slim
RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates ffmpeg intel-media-va-driver mesa-va-drivers \
    && rm -rf /var/lib/apt/lists/*

COPY --from=build /out/rivune /usr/local/bin/rivune
EXPOSE 8080
USER 65532:65532
ENTRYPOINT ["/usr/local/bin/rivune"]
