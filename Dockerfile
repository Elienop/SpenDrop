# The release this image is being built for, WITH its leading "v" (e.g.
# v0.35.0). The release workflow passes the tag it has just computed; every
# other build — docker-compose.dev.yml, a local `docker build`, the CI image
# gate — passes nothing and gets "dev".
#
# Declared once here, ahead of every FROM, and consumed by both builder
# stages below. That is what makes it impossible for one image to carry two
# different version strings: the Go binary and the JS bundle are stamped from
# this single value inside a single build.
ARG APP_VERSION=dev

# Build Go backend
FROM golang:1.26-alpine AS go-builder
RUN apk add --no-cache gcc musl-dev
WORKDIR /app
COPY go.mod go.sum ./
# The --mount=type=cache mounts here and below keep the Go module/compile
# caches and the npm download cache on the build host, OUTSIDE the layer
# cache: a rebuild whose lockfile changed re-downloads only the delta instead
# of the whole dependency tree. On a fresh builder (CI runners) the mounts
# start empty and the step behaves exactly as before — the caches only pay
# off where builds repeat, i.e. local dev rebuilds. Requires BuildKit, the
# default engine since Docker 23; a legacy-builder invocation fails loudly on
# the --mount flag rather than silently skipping the cache.
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY cmd/ cmd/
COPY internal/ internal/
# Re-declared to pull the global ARG into this stage's scope (it inherits the
# global default). Kept as late as possible: an ARG invalidates the cache for
# every layer after it and this value changes on every single release, so
# declaring it above `go mod download` would re-download the module cache each
# time.
ARG APP_VERSION
# -X bakes the version into the binary at link time, so the running server
# needs no env var, config file, or mounted file to know its own release. The
# symbol path is plain text with nothing to typo-check it — a wrong path
# builds cleanly and silently ships "dev" — so
# TestDockerfileStampsTheLinkedVersionVar pins this line against the Go
# source.
# The module cache mount must repeat here — mounts are per-RUN, and without
# it the modules `go mod download` cached would be invisible to the build.
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=1 go build \
    -ldflags "-X github.com/elienop/spendrop/internal/version.version=${APP_VERSION}" \
    -o spendrop ./cmd/spendrop

# Build React frontend
FROM node:24-alpine AS web-builder
WORKDIR /app/web
COPY web/package*.json ./
# Same scheme as the Go stage: /root/.npm is npm's content-addressed download
# cache, so `npm ci` still rebuilds node_modules from scratch (its contract)
# but fetches from disk instead of the network for every version the cache
# has seen before. The flags make the cache actually bite: without
# --prefer-offline npm revalidates against the registry even on full cache
# hits, and the audit/fund calls are pure network chatter a build doesn't
# need (Dependabot owns advisory scanning for this repo). Integrity is not
# weakened — every tarball is still checked against the lockfile's SHA pins.
RUN --mount=type=cache,target=/root/.npm npm ci --prefer-offline --no-audit --no-fund
COPY web/ .
# Same value, second artifact. Vite inlines VITE_-prefixed env vars into the
# bundle at build time, so the frontend reads it as
# import.meta.env.VITE_APP_VERSION. Declared after `npm ci` for the same cache
# reason as the Go stage. This ENV belongs to the builder stage only and does
# not reach the final image.
ARG APP_VERSION
ENV VITE_APP_VERSION=${APP_VERSION}
RUN npm run build

# Final image
FROM alpine:3.24
RUN apk add --no-cache ca-certificates sqlite-libs su-exec shadow tzdata \
    && addgroup -g 911 spendrop && adduser -u 911 -G spendrop -D spendrop
WORKDIR /app
COPY --from=go-builder /app/spendrop .
COPY --from=web-builder /app/web/dist ./web/dist
COPY entrypoint.sh /entrypoint.sh
RUN chmod +x /entrypoint.sh \
    && mkdir -p /app/data /app/data/backups /app/data/migration-snapshots \
    && chown -R spendrop:spendrop /app /app/data
EXPOSE 8080
# Scrape GET /healthz/data, the DB-aware endpoint — NOT the cheap /api/health,
# which only proves the HTTP server is accepting. That distinction is the whole
# point: a container whose database is corrupt answers /api/health 200 forever,
# so an external monitor reports the stack green while the ledger is unreadable.
# Measured on a throwaway container 2026-08-07 — after three pages of
# spendrop.db were overwritten, /healthz/data returned 503 on the same boot
# where /api/health was still returning 200.
#
# One request covers: the per-request PRAGMA quick_check, the cached result of
# the daily full integrity_check, schema version and transaction counts, the
# balance-checkpoint freshness sweep, and the scheduled-backup subsystem. Any
# degraded sub-check answers 503, which is what flips the container.
#
# A database that has gone READ-ONLY is covered too, since backlog B7 piece 2:
# one sub-check is a single-row upsert, so "attempt to write a readonly
# database" — what a restore leaves behind when the file ends up owned by root
# — answers 503 on the next scrape and turns the container unhealthy after the
# retry budget, ~90s, instead of waiting up to 24h for a backup to fail. Every
# other sub-check is a read and passes cleanly in that state, which is exactly
# why the write probe had to exist.
#
# --interval=30s: the endpoint's own operational note (internal/api/router.go)
# asks for >=10s so quick_check does not become a hot path that starves WAL
# writers. 30s sits well inside that, and three retries put worst-case
# detection at ~90s.
# --timeout=10s, raised from the 5s /api/health used: that probe touched no
# database, this one does real reads, and the pool is capped at a single
# connection (cmd/spendrop/db.go), so a scrape can queue behind an in-flight
# import or export. The handler itself measured sub-millisecond; the headroom
# is for connection contention, not for the query. Still under the interval, so
# probes never overlap.
# --start-period=60s covers a large legacy DB's first boot: the synchronous
# migration backfill + integrity scan can run well past 20s and would otherwise
# flap the container unhealthy before the server starts answering. It matters
# more now than it did — this endpoint cannot answer at all until migrations
# and the startup integrity check are done.
#
# Under `restart: unless-stopped` Docker does not restart on unhealthy, so a
# degraded database is REPORTED, never turned into a crash loop. That is what
# makes it safe to point the probe at a check that can legitimately say "no".
#
# busybox wget (alpine ships it; curl is not installed). -O /dev/null rather
# than --spider: chi registers /healthz/data for GET only and answers HEAD with
# 405 (verified against the running container), and --spider's documented
# contract — check that the URL exists, download nothing — never promises GET.
# GNU wget's implementation probes with HEAD; this busybox build happens to
# send GET, so --spider works today by implementation accident, and a build
# that switched to HEAD would pin the container unhealthy forever. An explicit
# GET cannot drift that way. -q silences
# transfer chatter while still printing wget's "server returned error" line,
# which Docker records in .State.Health.Log[].Output.
HEALTHCHECK --interval=30s --timeout=10s --start-period=60s --retries=3 \
    CMD wget -q -O /dev/null http://127.0.0.1:8080/healthz/data || exit 1
ENV DB_PATH=/app/data/spendrop.db
ENV BACKUP_DIR=/app/data/backups
ENTRYPOINT ["/entrypoint.sh"]
CMD ["./spendrop"]
