# =============================================
# Pull API v2 - multi-stage Docker build
# =============================================
# Stage 1: Build static Linux binary
FROM golang:1.21-alpine AS builder

WORKDIR /app

# Cache deps separately
COPY go.mod go.sum ./
RUN go mod download

# Copy source and build
COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build \
    -ldflags="-s -w" \
    -o /app/pull-api-v2 \
    ./main.go

# Stage 2: minimal runtime
FROM alpine:3.19

RUN apk add --no-cache ca-certificates tzdata && \
    addgroup -S app && adduser -S app -G app

WORKDIR /app
COPY --from=builder /app/pull-api-v2 /app/pull-api-v2
USER app

EXPOSE 8080
ENV PORT=8080

HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
    CMD wget -qO- http://127.0.0.1:8080/health || exit 1

CMD ["/app/pull-api-v2"]
