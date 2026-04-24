# syntax=docker/dockerfile:1

# ── Stage 1: build ──────────────────────────────────────────────────────────
FROM golang:1.24-alpine AS builder

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /pincher ./cmd/pinch/

# ── Stage 2: minimal runtime ─────────────────────────────────────────────────
# scratch + CA certs = smallest possible image; no shell attack surface.
FROM scratch

COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=builder /pincher /pincher

# Data directory — mount a volume here for persistent index storage.
VOLUME ["/data"]

# HTTP port advertised to Docker. The actual listen address comes from the
# --http flag or $PINCHER_HTTP_ADDR (the env var is the fallback when the
# flag is empty). To bind a different port, either pass --http or set the
# env var — and map the published port to match.
EXPOSE 8080

# Env-driven defaults so `docker run -e PINCHER_HTTP_ADDR=:9000 <img>` works
# without rewriting argv. Unset (empty) disables HTTP and leaves only stdio.
#   PINCHER_HTTP_ADDR=:0   → OS picks a free port (logged at startup)
#   PINCHER_HTTP_KEY=...   → require this bearer token on every request
ENV PINCHER_HTTP_ADDR=:8080

ENTRYPOINT ["/pincher", "--data-dir", "/data"]

# Extra args are forwarded to the entrypoint. Override to run a different
# subcommand, e.g. `docker run <img> index /data/repo`.
CMD []
