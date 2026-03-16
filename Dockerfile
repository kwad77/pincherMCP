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

# HTTP port (used when --http flag is passed).
EXPOSE 8080

ENTRYPOINT ["/pincher", "--data-dir", "/data"]

# Default: HTTP mode on :8080
# Override CMD to run in stdio mode: docker run ... pincher (no extra args)
CMD ["--http", ":8080"]
