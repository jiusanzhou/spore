# Multi-stage Docker build for spore.
#
# Final image ships THREE binaries:
#   /usr/local/bin/spore             — the main CLI (default ENTRYPOINT)
#   /usr/local/bin/spore-acp-server  — RFC-001 Stage 2: spore as ACP agent
#   /usr/local/bin/spore-mcp-server  — RFC-001 Stage 3: swarm as MCP tools
#
# Pick which one to run via `--entrypoint`:
#   docker run --rm spore                                 # spore (default)
#   docker run --rm --entrypoint spore-mcp-server spore   # MCP server
#   docker run --rm --entrypoint spore-acp-server spore   # ACP server
#
# Image is alpine-based (~30MB) because we need libc for the embedded
# IPFS node and for go-sqlite3's CGO bindings. distroless/scratch would
# require switching memory store to a pure-Go SQLite (modernc.org/sqlite)
# — tracked as future work.

# ---------- Frontend bundle ----------
FROM node:22-alpine AS frontend

WORKDIR /web
COPY web/package.json web/package-lock.json ./
# --legacy-peer-deps tolerates the peer-dep conflicts in the existing
# react-scripts setup; matches what's expected locally.
RUN npm ci --prefer-offline --no-audit --legacy-peer-deps
COPY web/ .
RUN BUILD_PATH=dist npx react-scripts build

# ---------- Go build ----------
FROM golang:1.25-alpine AS builder

RUN apk add --no-cache gcc musl-dev git

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download

COPY . .
COPY --from=frontend /web/dist /src/web/dist

# Inject version metadata so `spore --version` reports something useful
# inside the container too.
ARG VERSION=docker
ARG COMMIT=unknown
ARG BUILD_DATE=unknown
ENV CGO_ENABLED=1

RUN go build \
        -trimpath \
        -ldflags="-s -w -X go.zoe.im/x/version.Version=${VERSION} -X go.zoe.im/x/version.Commit=${COMMIT} -X go.zoe.im/x/version.BuildDate=${BUILD_DATE}" \
        -o /out/spore ./cmd/spore && \
    go build \
        -trimpath \
        -ldflags="-s -w -X go.zoe.im/x/version.Version=${VERSION}" \
        -o /out/spore-acp-server ./cmd/spore-acp-server && \
    go build \
        -trimpath \
        -ldflags="-s -w -X go.zoe.im/x/version.Version=${VERSION}" \
        -o /out/spore-mcp-server ./cmd/spore-mcp-server

# ---------- Runtime ----------
FROM alpine:3.20

RUN apk add --no-cache ca-certificates && \
    addgroup -S spore && adduser -S -G spore spore && \
    mkdir -p /home/spore/.spore && chown -R spore:spore /home/spore

COPY --from=builder /out/spore             /usr/local/bin/spore
COPY --from=builder /out/spore-acp-server  /usr/local/bin/spore-acp-server
COPY --from=builder /out/spore-mcp-server  /usr/local/bin/spore-mcp-server

USER spore
WORKDIR /home/spore

# 9292: HTTP API + dashboard
# 9000: libp2p TCP transport (default in cfg)
EXPOSE 9292 9000

# OCI labels — surface in `docker inspect` and on registry pages.
LABEL org.opencontainers.image.title="spore"
LABEL org.opencontainers.image.description="Decentralized AI agent swarm protocol and runtime — bidirectional ACP+MCP node"
LABEL org.opencontainers.image.source="https://github.com/jiusanzhou/spore"
LABEL org.opencontainers.image.licenses="Apache-2.0"

ENTRYPOINT ["spore"]
CMD ["run"]
