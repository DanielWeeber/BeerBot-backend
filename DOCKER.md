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
```
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
```bash
docker run -d \
  --name beerbot-backend \
  -p 8080:8080 \
  -e BOT_TOKEN="xoxb-your-token" \
  -e APP_TOKEN="xapp-your-token" \
  -e CHANNEL="C1234567890" \
  -e API_TOKEN="your-api-token" \
  danielweeber/beerbot-backend:latest
```

### With Docker Compose
```yaml
version: '3.8'
services:
  backend:
    image: danielweeber/beerbot-backend:latest
    ports:
      - "8080:8080"
    environment:
      - BOT_TOKEN=${BOT_TOKEN}
      - APP_TOKEN=${APP_TOKEN}
      - CHANNEL=${CHANNEL}
      - API_TOKEN=${API_TOKEN}
    volumes:
      - ./data:/data
    restart: unless-stopped
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