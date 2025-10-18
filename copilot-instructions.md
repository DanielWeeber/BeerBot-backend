copilot-instructions for the "beer-with-me" project

Zweck
-----
Diese Datei enthält alle notwendigen Hinweise, Befehle und Kontextinformationen, damit ein Assistenzsystem (z. B. GitHub Copilot oder ein ähnlicher Coding Assistant) das Projekt sofort versteht, lokal starten, testen und debuggen kann. Sie ist auf Deutsch geschrieben, weil das Team hier meist Deutsch verwendet.

Wichtigste Pfade
-----------------
- Bot-Service: `bot/` (enthält `main.go`, `store.go`, `Dockerfile`, `docker-compose*.yml`)
- Host-DB (im Entwicklungs-Setup): `bot/data/data/bot.db`
- Compose-Datei für den Bot: `bot/docker-compose.yml` (dev: `bot/docker-compose.dev.yml`)
- Tests: `bot/*_test.go`

Umgebung / Voraussetzungen
--------------------------
- Docker & docker-compose (empfohlen für Entwicklung und reproduzierbare Builds)
- Go >= 1.23 (das Dockerfile benutzt `golang:1.23-alpine` für Build)
- sqlite3 (nur für lokale Inspektion; nicht zwingend im Container)

Erforderliche Umgebungsvariablen (für den Bot-Container)
--------------------------------------------------------
- BOT_TOKEN: Slack Bot Token (xoxb-...)
- APP_TOKEN: Slack App-level Token für Socket Mode (xapp-...)
- CHANNEL: Slack Channel ID, den der Bot beobachten soll (z. B. C01234567)

Schnellstart (lokal, Docker Compose)
------------------------------------
1) .env vorbereiten (im `bot/`-Ordner oder als Umgebungsvariablen exportieren):

```bash
# in bot/.env
BOT_TOKEN=xoxb-...
APP_TOKEN=xapp-...
CHANNEL=C01234567
```

2) Build & Start (Projektordner `bot/`):

```bash
cd bot
# baut das Bild und startet den Service
docker-compose -f docker-compose.yml up -d --build
# Bzw. für die dev-Variante (Mount des Quellcodes):
# docker-compose -f docker-compose.dev.yml up -d --build
```

3) Logs ansehen:

```bash
docker-compose -f docker-compose.yml logs -f --tail=200 bot
```

4) API endpoints (lokal erreichbar, Port in compose: 8080):
- Health: `GET http://localhost:8080/healthz`
- Metrics: `GET http://localhost:8080/metrics`
- Given: `GET http://localhost:8080/api/given?user=<USER>&date=YYYY-MM-DD`
- Received: `GET http://localhost:8080/api/received?user=<USER>&date=YYYY-MM-DD`

Datenbank & Migration
---------------------
- Die App führt Migrationen beim Start über `store.go` aus. Die erwarteten Spalten der `beers`-Tabelle sind:
  - id INTEGER PRIMARY KEY
  - giver_id TEXT
  - recipient_id TEXT
  - ts TEXT (original Slack ts, z.B. "1234567890.123456")
  - ts_rfc DATETIME (RFC3339 für Datum-Abfragen)
  - count INTEGER (Anzahl der Biere in einer Message)
- Wenn die App neu gestartet wird und die DB fehlt, erzeugt die Migration die Tabelle mit den korrekten Spalten.
- Falls Du eine vorhandene DB manuell reparieren musst: Backup zuerst!

DB-Pflege Befehle (Host):
```bash
# Backup
cp bot/data/data/bot.db bot/data/data/bot.db.bak
# Prüfen
sqlite3 bot/data/data/bot.db "PRAGMA table_info(beers);"
# Beispiel: ts_rfc ergänzen (falls fehlt)
# (Achtung: SQLite-Version kann ALTER-Varianten NICHT unterstützen)
docker run --rm -v $(pwd)/bot/data:/data alpine:3.18 sh -c "apk add --no-cache sqlite && sqlite3 /data/data/bot.db \"BEGIN; ALTER TABLE beers ADD COLUMN ts_rfc TEXT; UPDATE beers SET ts_rfc = datetime(substr(ts,1,instr(ts,'.')-1), 'unixepoch'); COMMIT;\""
```

Debugging: Doppelte Log-Einträge (Slack liefert Events mehrfach)
----------------------------------------------------------------
Symptom: Eine einzelne gesendete Slack-Nachricht wird mehrfach in den Logs angezeigt (meist mit unterschiedlichen `envelope_id`).

Ursachen/Diagnose:
- Slack kann Events mehrfach liefern (retries, multiple delivery mechanisms).
- Socket Mode liefert ein Events API Envelope + den inneren Callback; abhängig von App-Konfiguration können z. B. "message" Events und "app_mention" beide eintreffen.
- `ev.SubType` ist für normale User-Nachrichten leer; bot- oder thread-Events haben SubType gesetzt.
- Wir nutzen eine `processed_events` Tabelle mit `event_id` und führen `INSERT OR IGNORE` ein, um Duplikate zu vermeiden.

Empfohlene Schritte zur Fehlersuche:
1) Aktiviere Debug-Logs (in `main.go` wird zerolog verwendet). Du kannst das Binärprogramm so starten, dass Debug-Logging erscheint (oder LOG_LEVEL env var, falls implementiert).
2) Achte in den Logs auf:
   - socket envelope id (`envelope_id`) — ist sie unterschiedlich?
   - inner event type / subtype und `ev.TimeStamp`
   - ev.BotID (bot Nachrichten) oder ev.SubType (edits, thread_broadcast etc.)
3) Filter in `main.go`: akzeptiere nur Events mit leerer `SubType` und `ev.BotID == ""` und nur die gewünschte `channel`.
4) Markiere Events MIT `INSERT OR IGNORE` in `processed_events` VOR dem Verarbeiten (atomic pre-check). Das verhindert eine race condition, bei der 2 parallele Lieferungen beide Prozessschritte ausführen.

Kommando zum sicheren Testen (neue DB):
```bash
# entferne die alte DB (oder verschiebe sie)
mv bot/data/data/bot.db bot/data/data/bot.db.bak
# Neustart
docker-compose -f bot/docker-compose.yml up -d --build
# Logs streamen und dann im Slack-Kanal 1 Nachricht posten
docker-compose -f bot/docker-compose.yml logs -f --tail=200 bot
```

Historische Dedupe / Aggregation (optional)
-------------------------------------------
Falls Du vorhandene Duplikate zusammenführen willst (nicht rückgängig):
1) Backup anfertigen
2) Aggregation durchführen (Summierung der `count` nach `giver_id, recipient_id, ts`) und die Tabelle ersetzen.

SQL (Beispiel, handle mit Backup):
```sql
-- Beispielablauf in sqlite3:
BEGIN;
CREATE TABLE beers_new (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  giver_id TEXT NOT NULL,
  recipient_id TEXT NOT NULL,
  ts TEXT NOT NULL,
  ts_rfc DATETIME,
  count INTEGER NOT NULL DEFAULT 1,
  UNIQUE (giver_id, recipient_id, ts)
);
INSERT INTO beers_new (giver_id, recipient_id, ts, ts_rfc, count)
SELECT giver_id, recipient_id, ts, COALESCE(ts_rfc, datetime(substr(ts,1,instr(ts,'.')-1), 'unixepoch')),
       COALESCE(SUM(count), COUNT(1)) AS count
FROM beers
GROUP BY giver_id, recipient_id, ts;
DROP TABLE beers;
ALTER TABLE beers_new RENAME TO beers;
COMMIT;
```

Tests / Build
--------------
- Lokale Unit Tests (im `bot/`-Ordner):
```bash
cd bot
# in einem go-enabled environment
go test ./...
# oder im Container (empfohlen wenn Go nicht auf Host installiert):
docker run --rm -v $(pwd):/src -w /src golang:1.23-alpine sh -c "apk add --no-cache git build-base && go test ./..."
```

Wichtige Hinweise für Assistenzsysteme (Copilot/Gemini)
-----------------------------------------------------
- Wichtige Dateien, die beim Debuggen zu prüfen sind: `bot/main.go`, `bot/store.go`, `bot/Dockerfile`, `bot/docker-compose.yml`.
- Prüfe, ob die `TryMarkEventProcessed`-Logik vorhanden ist: Events sollten vor dem Schreiben atomar markiert werden.
- Prüfe die Slack App-Konfiguration: welche Events sind aktiviert (message.channels, app_mention, message.im etc.). Deaktiviere doppelte Event-Subscriptions in der Slack-App, wenn möglich.

Kontakt / Verantwortlicher
--------------------------
Bei Rückfragen zu Slack-IDs oder Tokens wende dich an den Repo-Inhaber.


Zuletzt aktualisiert: 18.10.2025
