# syntax=docker/dockerfile:1

FROM node:22-bookworm-slim AS frontend-builder
WORKDIR /src/frontend

COPY frontend/package.json frontend/package-lock.json ./
RUN npm ci

COPY frontend/ ./
RUN npm run build

FROM golang:1.26-bookworm AS go-builder
WORKDIR /src

RUN apt-get update \
    && apt-get install -y --no-install-recommends gcc libc6-dev \
    && rm -rf /var/lib/apt/lists/*

COPY go.mod go.sum ./
RUN go mod download

COPY cmd/ ./cmd/
COPY internal/ ./internal/
COPY pkg/ ./pkg/
COPY lib/ ./lib/
COPY --from=frontend-builder /src/FrontEndDist ./FrontEndDist/

RUN CGO_ENABLED=1 go build \
    -trimpath \
    -ldflags="-s -w" \
    -o /out/mentis-server \
    ./cmd/server

FROM debian:bookworm-slim
WORKDIR /app

RUN apt-get update \
    && apt-get install -y --no-install-recommends \
        ca-certificates \
        sqlite3 \
    && rm -rf /var/lib/apt/lists/*

ENV ADDR=:8080 \
    DB_PATH=/data/mentis.db \
    VEC_EXT_PATH=/app/lib \
    MEDIA_CACHE_DIR=/data/media-cache

COPY --from=go-builder /out/mentis-server /app/mentis-server
COPY --from=go-builder /src/FrontEndDist /app/FrontEndDist
COPY --from=go-builder /src/lib /app/lib

EXPOSE 8080
VOLUME ["/data"]

ENTRYPOINT ["/app/mentis-server"]
