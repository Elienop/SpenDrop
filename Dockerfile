# Build Go backend
FROM golang:1.26-alpine AS go-builder
RUN apk add --no-cache gcc musl-dev
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY cmd/ cmd/
COPY internal/ internal/
RUN CGO_ENABLED=1 go build -o spendrop ./cmd/spendrop

# Build React frontend
FROM node:20-alpine AS web-builder
WORKDIR /app/web
COPY web/package*.json ./
RUN npm ci
COPY web/ .
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
# --spider does a HEAD-like fetch and -q silences output. start-period=20s
# covers first-boot migrations/content-hash backfill so the legitimate sweep
# does not flap unhealthy; ~90s (interval*retries) of failures before unhealthy.
HEALTHCHECK --interval=30s --timeout=5s --start-period=20s --retries=3 \
    CMD wget -q --spider http://127.0.0.1:8080/api/health || exit 1
ENV DB_PATH=/app/data/spendrop.db
ENV BACKUP_DIR=/app/data/backups
ENTRYPOINT ["/entrypoint.sh"]
CMD ["./spendrop"]
