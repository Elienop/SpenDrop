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
    && mkdir -p /app/data \
    && chown -R spendrop:spendrop /app /app/data
EXPOSE 8080
ENV DB_PATH=/app/data/spendrop.db
ENTRYPOINT ["/entrypoint.sh"]
CMD ["./spendrop"]
