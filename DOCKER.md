# Docker Hub Deployment Setup

This repository is configured to automatically build and push Docker images to Docker Hub.

## 🚀 Automatic Deployment

### Triggers

- **Push to main**: Builds and pushes `latest` tag
- **Push tags**: Builds semantic version tags (e.g., `v1.0.0`, `v1.0`, `v1`)
- **Pull requests**: Builds images for testing (no push)
- **Manual trigger**: Via GitHub Actions UI

### Image Repository

- **Docker Hub**: `danielweeber/beerbot-backend`
- **Registry**: `docker.io`

## ⚙️ Setup Requirements

### 1. Docker Hub Credentials

Add these secrets to your GitHub repository:

1. Go to Repository Settings → Secrets and variables → Actions
2. Add the following secrets:

| Secret Name | Description | Value |
|------------|-------------|--------|
| `DOCKER_USERNAME` | Docker Hub username | `danielweeber` |
| `DOCKER_PASSWORD` | Docker Hub password/token | Your Docker Hub password or access token |

### 2. Docker Hub Access Token (Recommended)

Instead of using your password, create an access token:

1. Log in to [Docker Hub](https://hub.docker.com/)
2. Go to Account Settings → Security → Access Tokens
3. Create a new token with Read/Write permissions
4. Use this token as `DOCKER_PASSWORD` secret

## 🏗️ Build Configuration

### Multi-Platform Support

Images are built for:

- `linux/amd64` (Intel/AMD)
- `linux/arm64` (Apple Silicon/ARM)

### Tagging Strategy

```bash
danielweeber/beerbot-backend:latest      # Latest main branch
danielweeber/beerbot-backend:main        # Main branch
danielweeber/beerbot-backend:sha-main-abc123  # Commit SHA
danielweeber/beerbot-backend:v1.0.0      # Semantic version
danielweeber/beerbot-backend:v1.0        # Major.minor
danielweeber/beerbot-backend:v1          # Major version
```

### Build Context

- **Context**: `./bot` directory (where Dockerfile is located)
- **Dockerfile**: `./bot/Dockerfile`
- **Cache**: GitHub Actions cache for faster builds

## 🐳 Usage

### Pull Latest Image

```bash
docker pull danielweeber/beerbot-backend:latest
```

### Run Container

Port alignment: the bot exposes only metrics and health on `9090`.

```bash
docker run -d \
  --name beerbot-backend \
  -p 9090:9090 \
  -e BOT_TOKEN="xoxb-your-token" \
  -e APP_TOKEN="xapp-your-token" \
  -e DB_PATH="/data/beerbot.db" \
  -e LOG_LEVEL="info" \
  -e METRICS_PORT=9090 \
  -e SHUTDOWN_TIMEOUT=5s \
  -v $(pwd)/data:/data \
  danielweeber/beerbot-backend:latest
```

### With Docker Compose

```yaml
services:
  bot:
    image: danielweeber/beerbot-backend:latest
    ports:
      - "9090:9090"
    environment:
      BOT_TOKEN: ${BOT_TOKEN}
      APP_TOKEN: ${APP_TOKEN}
      DB_PATH: /data/beerbot.db
      METRICS_PORT: 9090
      LOG_LEVEL: info
    volumes:
      - ./data:/data
    restart: unless-stopped
```

### Using Docker Secrets (Recommended)

Create secrets:
```bash
printf "%s" "$BOT_TOKEN" | docker secret create slack_bot_token -
printf "%s" "$APP_TOKEN" | docker secret create slack_app_token -
```

In a swarm deploy spec:
```yaml
services:
  bot:
    image: danielweeber/beerbot-backend:latest
    secrets:
      - slack_bot_token
      - slack_app_token
    environment:
      DB_PATH: /data/beerbot.db
      METRICS_PORT: 9090
secrets:
  slack_bot_token:
    external: true
  slack_app_token:
    external: true
```

The application auto-detects `/run/secrets/slack_bot_token` and `/run/secrets/slack_app_token` if env vars are absent.

### Multi-Arch Build (local)

```bash
docker buildx build \
  --platform linux/amd64,linux/arm64 \
  --build-arg GIT_SHA=$(git rev-parse --short HEAD) \
  -t danielweeber/beerbot-backend:multi . --load
```

### Version Flag

Binary exposes `-version`. Example:

```bash
docker run --rm danielweeber/beerbot-backend:latest /bin/bot -version
```

## 🔍 Monitoring

### GitHub Actions

- View build status in the Actions tab
- Check build summaries for deployment details
- Monitor build times and cache effectiveness

### Docker Hub

- View image layers and vulnerabilities
- Check pull statistics
- Manage image tags and retention

## 🛡️ Security

### Best Practices

- ✅ Uses official Docker Hub registry
- ✅ Multi-platform builds for compatibility
- ✅ Secure credential management via GitHub Secrets
- ✅ Build cache optimization
- ✅ Only pushes on main branch (not PRs)
- ✅ Vulnerability scanning available on Docker Hub

### Secrets Management

- Never commit Docker Hub credentials to git
- Use access tokens instead of passwords
- Rotate tokens regularly
- Monitor access logs on Docker Hub
