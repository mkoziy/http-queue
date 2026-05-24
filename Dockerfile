# syntax=docker/dockerfile:1
FROM --platform=$BUILDPLATFORM golang:1.26-alpine AS builder
ARG BUILDPLATFORM TARGETOS TARGETARCH

WORKDIR /src

# Install CA certificates for TLS support
RUN apk add --no-cache ca-certificates

# Cache dependencies
COPY go.mod go.sum ./
RUN go mod download

# Copy the rest of the source
COPY . .

# Build a statically linked binary
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build \
    -trimpath \
    -ldflags="-s -w" \
    -o /usr/local/bin/http-queue \
    .

# ────────────────────────────────────────────────────────────
FROM alpine:3.21

# Create a non‑root user
RUN addgroup -S appgroup && adduser -S appuser -G appgroup

# Bundle CA certificates from builder
COPY --from=builder /etc/ssl/certs /etc/ssl/certs

# Copy the compiled binary
COPY --from=builder /usr/local/bin/http-queue /usr/local/bin/http-queue

# Document the default port (configurable via PORT env var)
EXPOSE 8080

USER appuser

ENV PORT=8080

ENTRYPOINT ["http-queue"]
