# Gemini Code Assist instruktionen für "beer-with-me"

Zweck
-----

Diese Datei gibt Hinweise und Kontext speziell für Gemini Code Assist (oder ähnliche LLM-basierte IDE-Assistenten), damit das Projekt nahtlos gestartet, getestet und debugged werden kann.

Kurzübersicht
-------------

- Projekt: beer-with-me
- Bot-Service: `bot/` (Go, Socket Mode Slack Bot)
- Build: Docker Multi-stage (Go 1.23)
- DB: SQLite-Datei `bot/data/data/bot.db`

Was Gemini wissen sollte
------------------------

1) Hauptverantwortliche Dateien:
   - `bot/main.go` — Socket-mode event loop, message parsing, REST API
   - `bot/store.go` — SQLite migrations, beer persistence, processed_events dedupe
   - `bot/Dockerfile` und `bot/docker-compose.yml`
2) Laufzeitanforderungen:
   - Envvars `BOT_TOKEN`, `APP_TOKEN`, `CHANNEL` müssen gesetzt sein, damit der Bot sich verbindet
   - `GO >= 1.23` ist erforderlich für Build
3) Typische Fehlerfälle:
   - `no such column: ts_rfc` -> Migration mismatch
   - Duplicate message handling -> Slack may deliver events multiple times; check `processed_events` usage and preferrable pre-insert dedupe.

Schnellbefehle (für die IDE-Terminal-Ausführung)
------------------------------------------------

- Build & Run (mit Docker Compose):

```bash
cd bot
docker compose -f docker-compose.yml up -d --build
# Logs
docker compose -f docker-compose.yml logs -f --tail=200 bot
```

- Tests (im Container):

```bash
docker run --rm -v $(pwd):/src -w /src golang:1.23-alpine sh -c "apk add --no-cache git build-base && go test ./..."
```

Debugging-Hints (Schnell-Checklist):

- Prüfe, ob `TryMarkEventProcessed` existiert und dass die App dieses vor dem Verarbeiten eines Events aufruft.
- Prüfe `ev.SubType` und `ev.BotID` in `main.go`: normale User-Messages haben `SubType == ""`.
- Überprüfe die Slack-App-Konfiguration (Event Subscriptions): deaktiviere doppelte Subscriptions (z. B. `message.channels` + `app_mention`) falls möglich.

Empfohlene Gemini-Prompts
-------------------------

- "Finde alle Stellen im Projekt, die `processed_events` referenzieren und bestätige, dass wir atomar vor dem Verarbeiten markieren."
- "Zeige mir die Slack Events, die `main.go` verarbeitet: welche SubTypes kommen vor, und welche Tests sollten wir schreiben?"

Weitere Hinweise
----------------

- Wenn Du das Projekt später öffnest, starte mit `docker-compose` (siehe oben) um sicherzustellen, dass Gemini/Assistant die gleiche Laufzeitumgebung hat wie beim letzten Test.
- Lass Gemini wissen, wenn Du Slack Tokens / Channel IDs nicht in die Repo-Umgebung legen möchtest — die Anleitung verwendet ein `.env`-File.

Zuletzt aktualisiert: 19.10.2025
