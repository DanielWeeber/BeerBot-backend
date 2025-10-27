# Step 1: Build the application
FROM golang:1.25-alpine AS builder
WORKDIR /src

# Copy dependency information first (better layer caching)
COPY go.mod go.sum ./

# Download dependencies with build cache
RUN --mount=type=cache,target=/go/pkg/mod,id=gomod \
    --mount=type=cache,target=/root/.cache/go-build,id=gobuild \
    go mod download

# Copy source code
COPY . .

# Accept build-time platform args (for docker buildx)
ARG TARGETOS
ARG TARGETARCH

# Build with cache mounts and optimizations (no CGO needed now!)
RUN --mount=type=cache,target=/go/pkg/mod,id=gomod \
    --mount=type=cache,target=/root/.cache/go-build,id=gobuild \
    CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH:-amd64} \
    go build -ldflags="-w -s -X main.Version=${GIT_SHA:-dev}" -trimpath -o /bin/bot ./

# Test stage (optional) - can be targeted separately
FROM builder AS test
RUN go test -count=1 ./...

# Step 2: Create the final image
FROM alpine:3.20 AS final
RUN apk add --no-cache ca-certificates tzdata curl && \
    adduser -D -u 10001 botuser && \
    cp /usr/share/zoneinfo/Europe/Berlin /etc/localtime

USER botuser
WORKDIR /data
VOLUME /data

COPY --from=builder /bin/bot /bin/bot

LABEL org.opencontainers.image.title="BeerBot Backend" \
      org.opencontainers.image.source="https://github.com/DanielWeeber/BeerBot-backend" \
      org.opencontainers.image.description="Slack Beer tracking bot" \
      org.opencontainers.image.licenses="MIT"

EXPOSE 9090
ENV METRICS_PORT=9090

HEALTHCHECK --interval=30s --timeout=5s --start-period=40s --retries=3 \
    CMD curl -fsS http://localhost:9090/health || exit 1

ENTRYPOINT ["/bin/bot"]