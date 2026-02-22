# Multi-stage build for gostwriter
# Stage 1: build the Go binary
FROM golang:1.26-alpine3.23 AS builder

# Install git for modules if needed (and reproducibility)
RUN apk add --no-cache git

WORKDIR /src

# Cache dependencies first
COPY go.mod go.sum ./
RUN go mod download

# Copy the rest of the source
COPY . .

# Build static binary (modernc sqlite is pure Go; no CGO required)
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /out/gostwriter ./cmd/gostwriter

# Stage 2: minimal runtime with git available (required by targets/git)
FROM alpine:3.23

# Install runtime dependencies
# - ca-certificates: for HTTPS (git over https)
# - git: required by gostwriter git target
# - tini: minimal init for proper signal handling
RUN apk add --no-cache ca-certificates git tini

WORKDIR /app

# Create directories for config and data (mounted via Helm chart)
RUN mkdir -p /app/config /app/data

# Copy binary
COPY --from=builder /out/gostwriter /app/gostwriter

# Default config path is read from env var if set (GOSTWRITER_CONFIG)
ENV GOSTWRITER_CONFIG=/app/config/config.yaml

# Use tini as an init to handle signals/children properly
ENTRYPOINT ["/sbin/tini", "--"]

# Start the server
CMD ["/app/gostwriter"]