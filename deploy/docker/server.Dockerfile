# billing-server — the HTTP API composition root (apps/server), now with
# apps/web's built SPA served alongside it (internal/platform/http's
# MountSPA — a chi NotFound fallback, not a Go embed, so the two build
# toolchains stay independent and this stage can be skipped entirely by
# anything that only wants the API, e.g. a horizontally-scaled deployment
# behind a CDN/static host for the frontend instead).
FROM node:22-alpine AS webbuild
WORKDIR /web
COPY apps/web/package.json apps/web/package-lock.json* ./
RUN npm ci
COPY apps/web/ .
RUN npm run build

# pgtools: pg_dump/pg_restore for internal/platform/pgtools (Stage 16,
# backup/restore) — copied from the OFFICIAL postgres image (the exact
# version deploy/compose and casaos both run, matching client/server
# major version) rather than installed via apk, since neither pg_dump
# nor pg_restore ship in Alpine's own repos under a matching name, and
# this avoids depending on apk's CDN at all for this specific pair of
# binaries. The two binaries alone won't run on Alpine (glibc, not
# musl) — their runtime .so dependencies come along in the same COPY
# below. Verified for real before relying on this: built this exact
# multi-stage pattern standalone, ran the resulting pg_dump against a
# live Postgres 18 over the network, and confirmed pg_restore --list
# could read the archive back — not assumed to work from reading Docker
# docs.
FROM postgres:18 AS pgtools

FROM golang:1.27.1-bookworm AS build
WORKDIR /src

# Cache module downloads separately from source changes.
COPY go.mod go.sum ./
RUN go mod download

COPY . .

# CGO_ENABLED=0: pgx's default driver is pure Go, no libpq/cgo needed, so a
# fully static binary works on Alpine's musl runtime with zero native deps.
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/server ./apps/server

# Alpine, not distroless: HEALTHCHECK below needs *something* to make an
# HTTP request, and distroless images ship no shell/wget/curl at all —
# adding an HTTP-client dependency to the Go binary just to self-probe
# would be a bigger footprint than the ~1MB wget already in Alpine's
# busybox. ca-certificates is required — pgx dials Postgres over TLS in
# most managed/cloud deployments.
FROM alpine:3.22 AS runtime
# Alpine's CDN mirror occasionally returns a transient fetch error under load
# ("temporary error (try again later)") which apk doesn't retry on its own —
# retry a few times before failing the build.
RUN n=0; until apk add --no-cache ca-certificates wget; do \
      n=$((n+1)); [ "$n" -ge 5 ] && exit 1; \
      echo "apk add failed, retrying ($n/5)..."; sleep 5; \
    done \
    && addgroup -S billing && adduser -S billing -G billing
WORKDIR /app
COPY --from=build /out/server /app/server
COPY --from=webbuild --chown=billing:billing /web/dist /app/web
COPY --from=pgtools /usr/lib/postgresql/18/bin/pg_dump /usr/local/bin/pg_dump
COPY --from=pgtools /usr/lib/postgresql/18/bin/pg_restore /usr/local/bin/pg_restore
COPY --from=pgtools /lib/x86_64-linux-gnu/ /lib/x86_64-linux-gnu/
COPY --from=pgtools /lib64/ld-linux-x86-64.so.2 /lib64/ld-linux-x86-64.so.2

HEALTHCHECK --interval=10s --timeout=3s --start-period=5s --retries=3 \
    CMD wget -q -O- --no-check-certificate http://localhost:8080/health/live || exit 1

USER billing:billing
EXPOSE 8080

# Exec form (no shell) — required for SIGTERM to reach the Go process
# directly, matching apps/server's signal.NotifyContext(SIGINT, SIGTERM)
# graceful-shutdown handling instead of being swallowed by a shell wrapper.
ENTRYPOINT ["/app/server"]
