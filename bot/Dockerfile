# Step 1: Build the application
FROM golang:1.25-alpine AS builder
WORKDIR /src

# Copy dependency information first (better layer caching)
COPY go.mod go.sum ./

# Download dependencies with build cache
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    go mod download

# Copy source code
COPY . .

# Build with cache mounts and optimizations (no CGO needed now!)
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build -ldflags="-w -s" -a -installsuffix cgo -o /bin/bot ./

# Step 2: Create the final image
FROM alpine:3.20
RUN apk add --no-cache ca-certificates wget curl tzdata && \
    cp /usr/share/zoneinfo/Europe/Berlin /etc/localtime

COPY --from=builder /bin/bot /bin/bot

VOLUME /data
WORKDIR /data

EXPOSE 8080
ENTRYPOINT ["/bin/bot"]