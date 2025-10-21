package main

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"github.com/rs/zerolog"
	zlog "github.com/rs/zerolog/log"
)

type Store interface {
    CountGivenInDateRange(user string, start, end time.Time) (int, error)
    CountReceivedInDateRange(user string, start, end time.Time) (int, error)
    CountGivenOnDate(user string, date string) (int, error)
    GetAllGivers() ([]string, error)
    GetAllRecipients() ([]string, error)
    TryMarkEventProcessed(eventID string, t time.Time) (bool, error)
    AddBeer(giver string, recipient string, ts string, eventTime time.Time, count int) error
}

func parseLogLevel(levelStr string) zerolog.Level {
    switch strings.ToLower(strings.TrimSpace(levelStr)) {
    case "trace":
        return zerolog.TraceLevel
    case "debug":
        return zerolog.DebugLevel
    case "info":
        return zerolog.InfoLevel
    case "warn", "warning":
        return zerolog.WarnLevel
    case "error":
        return zerolog.ErrorLevel
    case "fatal":
        return zerolog.FatalLevel
    case "panic":
        return zerolog.PanicLevel
    default:
        return zerolog.WarnLevel
    }
}

func main() {
	emoji := ":beer:" //nolint:typecheck // Used in regexp compilation below
	if env := os.Getenv("EMOJI"); env != "" {
		emoji = env
	}
	dbPath := flag.String("db", os.Getenv("DB_PATH"), "sqlite database path")
	botToken := flag.String("bot-token", os.Getenv("BOT_TOKEN"), "slack bot token (xoxb-...)")
	appToken := flag.String("app-token", os.Getenv("APP_TOKEN"), "slack app-level token (xapp-...)")
	channelID := flag.String("channel", os.Getenv("CHANNEL"), "channel id to monitor")
	apiToken := flag.String("api-token", os.Getenv("API_TOKEN"), "api token for authentication")

	addrDefault := ":8080"
	if env := os.Getenv("ADDR"); env != "" {
		addrDefault = env
	}
	addr := flag.String("addr", addrDefault, "health/metrics listen address")

	maxPerDayDefault := 10
	if env := os.Getenv("MAX_PER_DAY"); env != "" {
		if v, err := strconv.Atoi(env); err == nil {
			maxPerDayDefault = v
		}
	}
	maxPerDay := flag.Int("max-per-day", maxPerDayDefault, "max beers a user may give per day") //nolint:typecheck // Used in daily limit checks
	flag.Parse()

	if *botToken == "" || *appToken == "" || *channelID == "" {
		zlog.Fatal().Msg("bot-token, app-token and channel must be provided via flags or env (BOT_TOKEN, APP_TOKEN, CHANNEL)")
	}

	if err := ensureDBDir(*dbPath); err != nil {
		zlog.Fatal().Err(err).Msg("Failed to create DB directory")
	}
	db, err := sql.Open("sqlite3", *dbPath)
	if err != nil {
		zlog.Fatal().Err(err).Msg("open db")
	}
	defer db.Close()
	storeImpl, err := NewSQLiteStore(db)
	if err != nil {
		zlog.Fatal().Err(err).Msg("init store")
	}
	var store Store = storeImpl

	// Logger init
	zerolog.TimeFieldFormat = time.RFC3339
	lvl := parseLogLevel(os.Getenv("LOG_LEVEL"))
	zerolog.SetGlobalLevel(lvl)
	zlogger := zerolog.New(zerolog.ConsoleWriter{Out: os.Stderr}).With().Timestamp().Logger()
	zlog.Logger = zlogger

    // Metrics
    InitMetrics()

	// Slack manager and client
	slackManager := NewSlackConnectionManager(*botToken, *appToken)
	client := slackManager.GetClient()

	// HTTP server
	mux := newMux(*apiToken, client, store, slackManager)
	srv := startHTTPServer(*addr, mux)

	// Slack event handling
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	eventHandler := buildEventHandler(store, client, slackManager, *channelID, emoji, *maxPerDay)
	slackManager.StartWithReconnection(ctx, eventHandler)
	startConnectionMonitor(ctx, slackManager)

	// Shutdown handling
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)
	select {
	case sig := <-sigs:
		zlog.Warn().Str("signal", fmt.Sprintf("%v", sig)).Msg("received shutdown signal")
		cancel()
	case <-ctx.Done():
		zlog.Info().Msg("context cancelled, shutting down")
	}

	zlog.Info().Msg("initiating graceful shutdown...")
	ctxShutdown, cancelShutdown := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelShutdown()
	if err := srv.Shutdown(ctxShutdown); err != nil {
		zlog.Error().Err(err).Msg("http server shutdown error")
	} else {
		zlog.Info().Msg("http server shutdown completed")
	}
	zlog.Info().Msg("shutdown complete")
}

func ensureDBDir(dbPath string) error {
	dbDir := ""
	dbFile := dbPath
	if idx := strings.LastIndex(dbFile, "/"); idx != -1 {
		dbDir = dbFile[:idx]
	}
	if dbDir != "" {
		if _, err := os.Stat(dbDir); os.IsNotExist(err) {
			if err := os.MkdirAll(dbDir, 0o755); err != nil {
				return err
			}
		}
	}
	return nil
}

func startHTTPServer(addr string, mux *http.ServeMux) *http.Server {
	srv := &http.Server{Addr: addr, Handler: mux}
	go func() {
		zlog.Info().Str("addr", addr).Msg("http listening")
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			zlog.Fatal().Err(err).Msg("http failed")
		}
	}()
	return srv
}
