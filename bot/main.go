package main

import (
	"context"
	"database/sql"
	"flag"
	"io/ioutil"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	_ "modernc.org/sqlite"
)

// Store interface defines the storage operations needed by the bot
type Store interface {
	CountGivenInDateRange(user string, start, end time.Time) (int, error)
	CountReceivedInDateRange(user string, start, end time.Time) (int, error)
	CountGivenOnDate(user string, date string) (int, error)
	GetAllGivers() ([]string, error)
	GetAllRecipients() ([]string, error)
	TryMarkEventProcessed(eventID string, t time.Time) (bool, error)
	AddBeer(giver string, recipient string, ts string, eventTime time.Time, count int) error
	RecordBeerEventOutcome(eventID, giverID, recipientID string, quantity int, status string, t time.Time) error
	TopGivers(start, end time.Time, limit int) ([][2]string, error)
	TopReceivers(start, end time.Time, limit int) ([][2]string, error)
}

func parseLogLevel(levelStr string) zerolog.Level {
	switch strings.ToLower(strings.TrimSpace(levelStr)) {
	case "trace":
		return zerolog.TraceLevel
	case "debug":
		return zerolog.DebugLevel
	case "info":
		return zerolog.InfoLevel
	case "warn":
		return zerolog.WarnLevel
	case "error":
		return zerolog.ErrorLevel
	case "fatal":
		return zerolog.FatalLevel
	case "panic":
		return zerolog.PanicLevel
	default:
		return zerolog.InfoLevel
	}
}

var Version = "dev"

func readSecretFile(name string) string {
	paths := []string{
		filepath.Join("/run/secrets", name),
		filepath.Join("/secrets", name),
	}
	for _, p := range paths {
		b, err := ioutil.ReadFile(p)
		if err == nil {
			return strings.TrimSpace(string(b))
		}
	}
	return ""
}

func main() {
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.Parse()
	if *showVersion {
		println(Version)
		return
	}
	// Configure logging
	logLevel := parseLogLevel(os.Getenv("LOG_LEVEL"))
	zerolog.SetGlobalLevel(logLevel)
	log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr})

	logger := log.With().Str("component", "main").Logger()
	logger.Info().Msg("Starting minimal BeerBot...")

	// Get configuration from environment (matching docker-compose variable names)
	botToken := os.Getenv("BOT_TOKEN")
	if botToken == "" {
		botToken = readSecretFile("slack_bot_token")
	}
	if botToken == "" {
		// Fallback to SLACK_BOT_TOKEN for compatibility
		botToken = os.Getenv("SLACK_BOT_TOKEN")
		if botToken == "" {
			botToken = readSecretFile("slack_bot_token")
		}
		if botToken == "" {
			logger.Fatal().Msg("BOT_TOKEN or SLACK_BOT_TOKEN environment variable is required")
		}
	}

	appToken := os.Getenv("APP_TOKEN")
	if appToken == "" {
		appToken = readSecretFile("slack_app_token")
	}
	if appToken == "" {
		// Fallback to SLACK_APP_TOKEN for compatibility
		appToken = os.Getenv("SLACK_APP_TOKEN")
		if appToken == "" {
			appToken = readSecretFile("slack_app_token")
		}
		if appToken == "" {
			logger.Fatal().Msg("APP_TOKEN or SLACK_APP_TOKEN environment variable is required")
		}
	}

	dbPath := os.Getenv("DB_PATH")
	if dbPath == "" {
		dbPath = "/data/beerbot.db"
	}

	// Initialize database
	logger.Info().Str("db_path", dbPath).Msg("Initializing database")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		logger.Fatal().Err(err).Msg("Failed to open database")
	}
	defer db.Close()

	// Test database connection
	if err := db.Ping(); err != nil {
		logger.Fatal().Err(err).Msg("Failed to ping database")
	}

	// Initialize store
	store, err := NewSQLiteStore(db)
	if err != nil {
		logger.Fatal().Err(err).Msg("Failed to initialize store")
	}

	// Create minimal Slack bot
	bot, err := NewMinimalSlackBot(botToken, appToken, store, logger)
	if err != nil {
		logger.Fatal().Err(err).Msg("Failed to create Slack bot")
	}

	// Test Slack connection
	if err := bot.TestConnection(); err != nil {
		logger.Fatal().Err(err).Msg("Failed to connect to Slack")
	}

	// HTTP server (metrics + health)
	metricsPort := os.Getenv("METRICS_PORT")
	if metricsPort == "" {
		metricsPort = "9090"
	}
	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.Handler())
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	server := &http.Server{Addr: ":" + metricsPort, Handler: mux}
	go func() {
		logger.Info().Str("port", metricsPort).Msg("Starting HTTP server (/metrics, /health)")
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error().Err(err).Msg("HTTP server error")
		}
	}()

	// Run bot in background
	botErrCh := make(chan error, 1)
	go func() {
		logger.Info().Msg("Starting minimal Slack bot with Socket Mode")
		botErrCh <- bot.Start()
	}()

	// Graceful shutdown
	sigCh := make(chan os.Signal, 2)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	select {
	case sig := <-sigCh:
		logger.Info().Str("signal", sig.String()).Msg("Shutdown requested")
	case err := <-botErrCh:
		if err != nil {
			logger.Error().Err(err).Msg("Bot returned error; shutting down")
		}
	}

	shutdownTimeout := 5 * time.Second
	if v := strings.TrimSpace(os.Getenv("SHUTDOWN_TIMEOUT")); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			shutdownTimeout = d
		}
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	bot.Stop()
	if err := server.Shutdown(shutdownCtx); err != nil {
		logger.Warn().Err(err).Msg("HTTP server shutdown error")
	}
	logger.Info().Msg("Shutdown complete")
}
