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
RUN go mod download
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
RUN CGO_ENABLED=1 go build \
    -ldflags "-X github.com/elienop/spendrop/internal/version.version=${APP_VERSION}" \
    -o spendrop ./cmd/spendrop

# Build React frontend
FROM node:25-alpine AS web-builder
WORKDIR /app/web
COPY web/package*.json ./
RUN npm ci
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
FROM alpine:3.20
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
# Scrape the cheap GET /api/health (200 {"status":"ok"}) — NOT /healthz/data,
# which runs PRAGMA quick_check + several SELECTs per call and must not be
# polled sub-10s. busybox wget ships in alpine:3.20 (curl is not installed);
# --spider does a HEAD-like fetch and -q silences output. start-period=60s
# covers a large legacy DB's first-boot: the synchronous migration backfill +
# integrity scan can run well past 20s and would otherwise flap the container
# unhealthy before the server starts answering. The healthcheck is purely
# observational under `restart: unless-stopped` (no restart-on-unhealthy), so a
# generous start-period only delays the first status report, it costs nothing.
HEALTHCHECK --interval=30s --timeout=5s --start-period=60s --retries=3 \
    CMD wget -q --spider http://127.0.0.1:8080/api/health || exit 1
ENV DB_PATH=/app/data/spendrop.db
ENV BACKUP_DIR=/app/data/backups
ENTRYPOINT ["/entrypoint.sh"]
CMD ["./spendrop"]
