# Build Go backend
FROM golang:1.23-alpine AS go-builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
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
RUN apk add --no-cache ca-certificates sqlite
WORKDIR /app
COPY --from=go-builder /app/spendrop .
COPY --from=web-builder /app/web/dist ./web/dist
EXPOSE 8080
CMD ["./spendrop"]
