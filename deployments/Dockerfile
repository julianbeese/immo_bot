# Build stage
FROM golang:1.24-alpine AS builder

WORKDIR /app

# Install build dependencies
RUN apk add --no-cache git

# Copy go mod files
COPY go.mod go.sum ./
RUN go mod download

# Copy source code
COPY . .

# Build the binary
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-w -s" -o immobot ./cmd/immobot

# Runtime stage with Chrome
FROM chromedp/headless-shell:latest

# Install ca-certificates for HTTPS
RUN apt-get update && apt-get install -y --no-install-recommends \
    ca-certificates \
    tzdata \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /app

# Copy binary from builder
COPY --from=builder /app/immobot .

# Copy config files
COPY configs/ ./configs/
COPY migrations/ ./migrations/

# Create data directory
RUN mkdir -p /app/data

# Set timezone
ENV TZ=Europe/Berlin

# Environment variables (can be overridden)
ENV DATABASE_PATH=/app/data/immobot.db
ENV LOG_LEVEL=info

# Health check
HEALTHCHECK --interval=30s --timeout=10s --start-period=5s --retries=3 \
    CMD test -f /app/data/immobot.db || exit 1

# Run as non-root user
RUN useradd -r -u 1000 immobot && chown -R immobot:immobot /app
USER immobot

ENTRYPOINT ["./immobot"]
CMD ["-config", "/app/configs/config.yaml"]
