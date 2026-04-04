# STAGE 1: Build the binary
FROM golang:1.26.1-alpine3.23 AS builder

# Install git and ca-certificates (needed for HTTPS requests)
RUN apk update && apk add --no-cache git ca-certificates && update-ca-certificates

WORKDIR /app

# Copy dependency files first to leverage Docker caching
COPY go.mod go.sum ./
RUN go mod download

# Copy the rest of the source code
COPY . .

# Build a statically linked binary
# CGO_ENABLED=0: Disables C libraries (essential for scratch images)
# -ldflags="-s -w": Strips debug info and symbol tables (~20-30% smaller)
RUN CGO_ENABLED=0 GOOS=linux go build \
    -ldflags="-s -w" \
    -o /app/main .

# STAGE 2: Create the minimal runtime
FROM scratch

LABEL org.opencontainers.image.source=https://github.com/mjsully/webhook-proxy

# Copy the SSL certificates from the builder stage
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/

# Copy the compiled binary
COPY --from=builder /app/main /main

# (Optional) Run as a non-privileged user for security
# requires adding a user in the builder stage first
# USER 10001 

ENTRYPOINT ["/main"]
