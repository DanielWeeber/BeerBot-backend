# Step 1: Build the application
FROM golang:1.23-alpine AS builder
WORKDIR /src

# Install build dependencies
RUN apk add --no-cache git build-base

# Copy dependency information and download dependencies
COPY go.mod go.sum ./
RUN go mod download

# Copy the rest of the source code
COPY . .

# Build the application
RUN go build -o /bin/bot ./

# Step 2: Create the final image
FROM alpine:3.18
RUN apk add --no-cache ca-certificates
COPY --from=builder /bin/bot /bin/bot
VOLUME /data
WORKDIR /data
EXPOSE 8080
ENTRYPOINT ["/bin/bot"]