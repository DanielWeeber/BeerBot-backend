![BeerBot Logo](logo.svg)

# 🍺 BeerBot Backend

[![Go](https://img.shields.io/badge/Go-1.21+-00ADD8?style=flat-square&logo=go)](https://golang.org/)
[![Docker Hub](https://img.shields.io/badge/Docker%20Hub-danielweeber%2Fbeerbot--backend-2496ED?style=flat-square&logo=docker)](https://hub.docker.com/r/danielweeber/beerbot-backend)
[![SQLite](https://img.shields.io/badge/Database-SQLite-003B57?style=flat-square&logo=sqlite)](https://www.sqlite.org/)

**A modern Slack bot backend for virtual team appreciation with beer emojis! 🍻**

The BeerBot backend is a high-performance Go service that powers virtual beer giving in Slack workspaces. Built with modern architecture, it provides real-time event processing, comprehensive APIs, and robust data management.

---

## 🌟 Overview

BeerBot Backend is the core service that handles Slack events, processes beer transactions, and provides APIs for frontend applications. It features advanced date range queries, user management, and efficient SQLite storage with proper indexing for scalability.

## ✨ Features

### Core Functionality

- **🎯 Smart Beer Detection**: Automatically detects beer emojis and user mentions in Slack messages
- **📊 Advanced Analytics**: Track beer giving/receiving with flexible date range queries
- **👥 User Management**: Complete user profile integration with Slack avatars
- **🔄 Real-time Processing**: Socket mode integration for instant event handling

### Technical Features

- **🛡️ Secure API**: Bearer token authentication for all endpoints
- **📈 Performance Optimized**: SQLite with custom indexing for fast queries
- **🐳 Docker Ready**: Multiple environment configurations (dev, test, prod)
- **🔍 Event Deduplication**: Prevents duplicate processing of Slack events
- **🧾 Structured Logging**: Zerolog-based logging with configurable `LOG_LEVEL`
- **📊 Prometheus Metrics**: Built-in `/metrics` endpoint with HTTP, Slack and processing metrics

## 🏗️ Architecture

### Core Components

- **Event Handler** (`bot/slack.go`): Processes Slack Socket Mode events, deduplication and limits
- **HTTP Layer** (`bot/http_handlers.go`): REST API endpoints and auth middleware
- **Metrics** (`bot/metrics.go`): Prometheus collectors and helper functions
- **Application Wiring** (`bot/main.go`): Flags/env, logging, server startup, Slack wiring
- **Store Layer** (`bot/store.go`): SQLite database, migrations and queries
- **Authentication**: Bearer middleware for API security

### Database Schema

- `beers`: Beer transaction records with giver/recipient tracking
- `processed_events`: Event deduplication table
- `emoji_counts`: User emoji statistics (extensible for future features)

## 🚀 Quick Start

### Prerequisites

- [Docker](https://www.docker.com/) & [Docker Compose](https://docs.docker.com/compose/)
- [Go 1.21+](https://golang.org/) (for development)
- Slack workspace with admin permissions

### 🐳 Using Docker Images

**Quick Start with Docker Hub:**

```bash
# Pull and run the latest image
docker run -d \
  --name beerbot-backend \
  -p 8080:8080 \
  -e BOT_TOKEN="xoxb-your-bot-token" \
  -e APP_TOKEN="xapp-your-app-token" \
  -e API_TOKEN="your-secure-api-token" \
  -v beerbot-data:/data \
  danielweeber/beerbot-backend:latest
```

**Docker Compose Example:**

```yaml
version: '3.8'
services:
  beerbot-backend:
    image: danielweeber/beerbot-backend:latest
    container_name: beerbot-backend
    restart: unless-stopped
    ports:
      - "8080:8080"
    environment:
      BOT_TOKEN: "xoxb-your-bot-token"
      APP_TOKEN: "xapp-your-app-token"
      API_TOKEN: "your-secure-api-token"
      CHANNEL: "C1234567890"  # Optional: specific channel
      EMOJI: ":beer:"         # Optional: default emoji
      MAX_PER_DAY: "10"       # Optional: daily limit
      LOG_LEVEL: "warn"       # Optional: trace|debug|info|warn|error|fatal|panic
    volumes:
      - beerbot-data:/data
    healthcheck:
      test: ["CMD", "curl", "-f", "http://localhost:8080/api/health"]
      interval: 30s
      timeout: 10s
      retries: 3

volumes:
  beerbot-data:
```

### Installation from Source

1. **Clone the repository:**

   ```bash
   git clone https://github.com/DanielWeeber/BeerBot-backend.git
   cd BeerBot-backend
   ```

2. **Set up Slack App:**
   - Go to [https://api.slack.com/apps](https://api.slack.com/apps)
   - Create new app → "From scratch"
   - Enable **Socket Mode** in app settings
   - Add Bot Token Scopes: `chat:write`, `users:read`, `channels:history`, `groups:history`, `im:history`, `mpim:history`
   - Generate App-Level Token with `connections:write` scope
   - Install app to workspace

3. **Configure Environment:**
   Create `docker-compose.override.yml` from the example:

   ```bash
   cp docker-compose.override.yml.example docker-compose.override.yml
   ```

   Edit the file with your Slack tokens:

   ```yaml
   version: '3.8'
   services:
     bot:
       environment:
         BOT_TOKEN: "xoxb-your-bot-token"
         APP_TOKEN: "xapp-your-app-token"
         CHANNEL: "C1234567890"  # Channel ID where bot operates
         API_TOKEN: "your-secure-api-token"
         EMOJI: ":beer:"
   ```

4. **Launch the service:**

   ```bash
   # Development with hot-reload
   docker-compose -f docker-compose.yml -f docker-compose.dev.yml up

   # Production
   docker-compose up -d

   # Testing
   docker-compose -f docker-compose.test.yml up --abort-on-container-exit
   ```

## 🛠️ Usage

### Slack Integration

Once deployed, invite the bot to your channels and start giving beers:

```slack
Hey @john great job on that PR! 🍺
@sarah @mike excellent presentation 🍺🍺
```

The bot automatically:

- Detects beer emojis (🍺 or :beer:)
- Associates them with mentioned users
- Tracks the giving/receiving relationships
- Enforces daily limits per user

### REST API

#### Authentication

All API endpoints require Bearer token authentication:

```bash
curl -H "Authorization: Bearer YOUR_API_TOKEN" \
     http://localhost:8080/api/endpoint
```

#### Endpoints

**🍺 Beer Statistics**

```http
GET /api/given?user={user_id}&start={date}&end={date}
GET /api/received?user={user_id}&start={date}&end={date}
```

**Date Formats:**

- Specific date: `day=YYYY-MM-DD`
- Range: `start=YYYY-MM-DD&end=YYYY-MM-DD`

**Example Responses:**

```json
{
  "user": "U1234567890",
  "start": "2024-10-19",
  "end": "2024-10-19", 
  "given": 5
}
```

**👥 User Management**

```http
GET /api/user?user={user_id}
```

Response includes Slack profile data:

```json
{
  "real_name": "John Doe",
  "profile_image": "https://avatars.slack-edge.com/..."
}
```

**📋 User Lists**

```http
GET /api/givers     # All users who have given beers
GET /api/recipients # All users who have received beers
```

**🔍 Health Check**

```http
GET /api/health
```

## 🏃‍♂️ Development

### Local Setup

1. **Install Go dependencies:**

   ```bash
   cd bot && go mod tidy
   ```

2. **Run locally:**

   ```bash
   cd bot
   go run . -addr=:8080 -db=/tmp/beerbot.db
   ```

3. **Run tests:**

   ```bash
   go test ./...
   ```

### Docker Development

```bash
# Build and run with live reload
docker-compose -f docker-compose.dev.yml up --build

# Run tests in Docker
docker-compose -f docker-compose.test.yml up --build
```

### Environment Variables

| Variable | Required | Default | Description |
|----------|----------|---------|-------------|
| `BOT_TOKEN` | ✅ | - | Slack Bot User OAuth Token (`xoxb-...`) |
| `APP_TOKEN` | ✅ | - | Slack App-Level Token (`xapp-...`) |
| `CHANNEL` | ❌ | - | Specific channel ID to monitor |
| `API_TOKEN` | ✅ | - | Bearer token for REST API |
| `ADDR` | ❌ | `:8080` | HTTP server bind address |
| `MAX_PER_DAY` | ❌ | `10` | Maximum beers per user per day |
| `DB_PATH` | ❌ | `/data/beerbot.db` | SQLite database file path |
| `EMOJI` | ❌ | `:beer:` | Emoji to track (can be Unicode or Slack format) |
| `LOG_LEVEL` | ❌ | `warn` | Zerolog level: trace, debug, info, warn, error, fatal, panic |

### Command-line Flags (equivalents)

The service also supports flags equivalent to environment variables:

```bash
go run ./bot \
  -db=/data/beerbot.db \
  -bot-token=$BOT_TOKEN \
  -app-token=$APP_TOKEN \
  -channel=$CHANNEL \
  -api-token=$API_TOKEN \
  -addr=:8080 \
  -max-per-day=10
```

## 📦 Deployment

### Docker Compose (Recommended)

**Using Docker Hub Image:**

Create a `docker-compose.yml` file:

```yaml
version: '3.8'
services:
  beerbot-backend:
    image: danielweeber/beerbot-backend:latest
    container_name: beerbot-backend
    restart: unless-stopped
    ports:
      - "8080:8080"
    environment:
      BOT_TOKEN: "xoxb-your-bot-token"
      APP_TOKEN: "xapp-your-app-token"
      API_TOKEN: "your-secure-api-token"
      CHANNEL: "C1234567890"  # Optional
      EMOJI: ":beer:"         # Optional
      MAX_PER_DAY: "10"       # Optional
    volumes:
      - beerbot-data:/data
    healthcheck:
      test: ["CMD", "curl", "-f", "http://localhost:8080/api/health"]
      interval: 30s
      timeout: 10s
      retries: 3

volumes:
  beerbot-data:
```

Then run:

```bash
docker-compose up -d
```

**Building from Source:**

```bash
# Clone and configure
git clone https://github.com/DanielWeeber/BeerBot-backend.git
cd BeerBot-backend
cp docker-compose.override.yml.example docker-compose.override.yml

# Edit docker-compose.override.yml with your tokens
# Deploy
docker-compose up -d
```

### Manual Deployment

```bash
# Build binary
CGO_ENABLED=1 go build -o beerbot ./bot

# Run
BOT_TOKEN=xoxb-... APP_TOKEN=xapp-... ./beerbot
```

## 🔧 Configuration

### Slack App Setup

Required OAuth Scopes:

- `channels:history` - Read channel messages
- `groups:history` - Read private channel messages  
- `im:history` - Read direct messages
- `mpim:history` - Read group direct messages
- `users:read` - Access user profile information
- `chat:write` - Send messages (for future features)

Required App-Level Token Scopes:

- `connections:write` - Socket Mode connection

#### Event Subscriptions (required even with Socket Mode)

- Enable: App → Event Subscriptions → toggle ON.
- Subscribe to bot events:
  - `message.channels` (public channels)
  - If using private channels: `message.groups` and invite the bot to that private channel
  - Optional for quick testing: `app_mention`
- Save changes and click “Reinstall to Workspace” when prompted.

#### Invite bot and set channel

- Invite the bot user to the target Slack channel.
- Find the channel ID (e.g., starts with `C...`) and set it as `CHANNEL`.

#### Verify delivery

- On startup you should see logs like “Slack socket mode: hello/connected”.
- When Events API is configured, logs will include:
  - “Slack events API: received request, sending ack”
  - “Slack events API: outer event”
  - Debug details for message events in the target channel

#### Quick test message

- In the configured channel, send a message that includes:
  - A real user mention (Slack raw text contains `<@U...>`)
  - The emoji configured by `EMOJI` (default `:beer:`)
- Example: `@alice great job! :beer:`

If no events arrive: re-check Event Subscriptions are enabled, required bot events are added, the app is reinstalled, and the bot is a member of the channel.

### Database

The application automatically creates and migrates the SQLite database. Key features:

- **Automatic migrations**: Schema updates on startup
- **Optimized indexing**: Fast queries on user/date combinations  
- **Event deduplication**: Prevents duplicate processing
- **ACID compliance**: Reliable transaction processing

## 🐛 Troubleshooting

### Common Issues

**Connection Issues:**

```bash
# Check Slack connectivity
curl -H "Authorization: Bearer $BOT_TOKEN" https://slack.com/api/auth.test

# Verify socket mode
docker-compose logs bot
```

**Database Issues:**

```bash
# Reset database
rm -f ./bot/data/beerbot.db
docker-compose restart bot

# Check database integrity
sqlite3 ./bot/data/beerbot.db "PRAGMA integrity_check;"
```

**API Authentication:**

```bash
# Test API endpoint
curl -H "Authorization: Bearer YOUR_API_TOKEN" \
     http://localhost:8080/api/health
```

## 📈 Observability

- **Logging**: Structured via Zerolog. Set `LOG_LEVEL` to control verbosity.
  - Default: `warn`. Options: `trace`, `debug`, `info`, `warn`, `error`, `fatal`, `panic`.
- **Metrics**: Exposed at `/metrics` (Prometheus format). Included collectors:
  - `bwm_messages_processed_total{channel}`
  - `beer_message_outcomes_total{channel,reason}` (e.g., `subtype`, `no_mentions`, `limit_reached`, `stored`)
  - `http_requests_total{path,method,status}`
  - `http_request_duration_seconds{path,method,status}`
  - `slack_reconnects_total`
  - `slack_connected` (gauge)

Example scrape config:

```yaml
scrape_configs:
  - job_name: 'beerbot'
    static_configs:
      - targets: ['beerbot-backend:8080']
    metrics_path: /metrics
```

## 🤝 Contributing

We welcome contributions! Please see our contributing guidelines:

1. Fork the repository
2. Create a feature branch: `git checkout -b feature/amazing-feature`
3. Commit changes: `git commit -m 'Add amazing feature'`
4. Push to branch: `git push origin feature/amazing-feature`
5. Open a Pull Request

### Code Standards

- Follow Go best practices and formatting (`go fmt`)
- Add tests for new functionality
- Update documentation for API changes
- Use conventional commit messages

## 📄 License

This project is licensed under the MIT License - see the [LICENSE](LICENSE) file for details.

## 🙏 Acknowledgments

- Built with [Slack Go SDK](https://github.com/slack-go/slack)
- Uses [Zerolog](https://github.com/rs/zerolog) for structured logging
- Database powered by [SQLite](https://www.sqlite.org/)
- Containerized with [Docker](https://www.docker.com/)

---

**Ready to spread some virtual appreciation? Deploy BeerBot and let the team building begin! 🍻**
