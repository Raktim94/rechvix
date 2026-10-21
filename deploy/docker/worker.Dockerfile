# billing-worker — the background composition root (apps/worker): the
# outbox poller driving e-Invoice/e-Way Bill/webhook/notification delivery.
# Genuinely a separate process/container from billing-server, never sharing
# its request path (docs/architecture.md §9, brief Rule 12).

FROM golang:1.27.3-bookworm AS build
WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/worker ./apps/worker

FROM alpine:3.22 AS runtime
# apps/worker has no HTTP endpoint (it's a pure background poller, not a
# server) — ca-certificates only, plus busybox's pidof for the process-
# liveness HEALTHCHECK below (no HTTP surface to probe instead).
# Alpine's CDN mirror occasionally returns a transient fetch error under load
# ("temporary error (try again later)") which apk doesn't retry on its own —
# retry a few times before failing the build.
RUN n=0; until apk add --no-cache ca-certificates; do \
      n=$((n+1)); [ "$n" -ge 5 ] && exit 1; \
      echo "apk add failed, retrying ($n/5)..."; sleep 5; \
    done \
    && addgroup -S billing && adduser -S billing -G billing
WORKDIR /app
COPY --from=build /out/worker /app/worker

HEALTHCHECK --interval=15s --timeout=3s --start-period=5s --retries=3 \
    CMD pidof worker || exit 1

USER billing:billing

# Exec form — SIGTERM must reach the Go process directly for
# apps/worker's signal.NotifyContext(SIGINT, SIGTERM) graceful shutdown to
# actually run (it lets an in-flight outbox event finish rather than being
# killed mid-processing).
ENTRYPOINT ["/app/worker"]
