package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
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
	"github.com/slack-go/slack"
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
		b, err := os.ReadFile(p)
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

	// Initialize database with comprehensive diagnostics
	logger.Info().Str("db_path", dbPath).Msg("Initializing database")

	// Check if DB path is absolute or relative
	logger.Debug().
		Str("db_path", dbPath).
		Bool("is_absolute", filepath.IsAbs(dbPath)).
		Msg("Database path analysis")

	// Check parent directory existence and permissions
	dbDir := filepath.Dir(dbPath)
	if dirInfo, err := os.Stat(dbDir); err != nil {
		logger.Error().
			Err(err).
			Str("db_dir", dbDir).
			Msg("Database directory does not exist or is not accessible")
		logger.Fatal().
			Str("db_dir", dbDir).
			Msg("Cannot proceed - database directory must exist and be writable")
	} else {
		logger.Debug().
			Str("db_dir", dbDir).
			Str("permissions", dirInfo.Mode().String()).
			Bool("is_dir", dirInfo.IsDir()).
			Msg("Database directory info")

		// Check if directory is writable
		testFile := filepath.Join(dbDir, ".write_test")
		if err := os.WriteFile(testFile, []byte("test"), 0644); err != nil {
			logger.Error().
				Err(err).
				Str("db_dir", dbDir).
				Str("permissions", dirInfo.Mode().String()).
				Msg("Database directory is not writable")
			logger.Fatal().
				Str("db_dir", dbDir).
				Msg("Cannot proceed - database directory must be writable")
		} else {
			os.Remove(testFile)
			logger.Debug().
				Str("db_dir", dbDir).
				Msg("Database directory is writable")
		}

		// Additional filesystem check: try to write and then modify the test file
		// This detects read-only filesystem mounts even when permissions look correct
		fsTestFile := filepath.Join(dbDir, ".fs_write_test")
		if err := os.WriteFile(fsTestFile, []byte("initial"), 0644); err != nil {
			logger.Error().
				Err(err).
				Str("db_dir", dbDir).
				Msg("Filesystem appears to be read-only (write failed)")
			logger.Fatal().
				Str("db_dir", dbDir).
				Msg("Cannot proceed - filesystem is mounted read-only")
		}

		// Try to modify the file to ensure filesystem is truly writable
		if err := os.WriteFile(fsTestFile, []byte("modified"), 0644); err != nil {
			os.Remove(fsTestFile)
			logger.Error().
				Err(err).
				Str("db_dir", dbDir).
				Msg("Filesystem appears to be read-only (modification failed)")
			logger.Fatal().
				Str("db_dir", dbDir).
				Msg("Cannot proceed - filesystem is mounted read-only or has restrictions")
		}
		os.Remove(fsTestFile)
		logger.Debug().
			Str("db_dir", dbDir).
			Msg("Filesystem is fully writable (create and modify successful)")
	}

	// Check if database file exists and log its permissions
	if fileInfo, err := os.Stat(dbPath); err == nil {
		logger.Debug().
			Str("db_path", dbPath).
			Str("permissions", fileInfo.Mode().String()).
			Int64("size_bytes", fileInfo.Size()).
			Msg("Existing database file found")

		// Check if file is writable by owner (user permission bit)
		mode := fileInfo.Mode()
		isWritable := mode&0200 != 0 // Owner write permission

		// Check if permissions meet minimum requirement: rw-rw-rw- (0666)
		requiredPerms := os.FileMode(0666)
		currentPerms := mode.Perm()
		hasMinimumPerms := (currentPerms & requiredPerms) == requiredPerms

		if !isWritable || !hasMinimumPerms {
			logger.Warn().
				Str("db_path", dbPath).
				Str("current_permissions", mode.String()).
				Str("required_minimum", requiredPerms.String()).
				Bool("owner_writable", isWritable).
				Bool("has_minimum_perms", hasMinimumPerms).
				Msg("Database file permissions insufficient - attempting to fix")

			// Set to rw-rw-rw- (0666)
			newMode := requiredPerms
			if chmodErr := os.Chmod(dbPath, newMode); chmodErr != nil {
				logger.Error().
					Err(chmodErr).
					Str("db_path", dbPath).
					Str("current_permissions", mode.String()).
					Str("attempted_permissions", newMode.String()).
					Msg("Failed to fix database file permissions")
				logger.Fatal().
					Str("db_path", dbPath).
					Str("permissions", mode.String()).
					Str("required", requiredPerms.String()).
					Msg("Database file must have at least rw-rw-rw- (0666) permissions")
			} else {
				logger.Info().
					Str("db_path", dbPath).
					Str("old_permissions", mode.String()).
					Str("new_permissions", newMode.String()).
					Msg("Successfully updated database file permissions to rw-rw-rw-")
			}
		} else {
			logger.Debug().
				Str("db_path", dbPath).
				Str("permissions", mode.String()).
				Msg("Database file permissions are sufficient (minimum rw-rw-rw-)")
		}

		// Test if we can actually open the file for writing (even with correct permissions, filesystem might be read-only)
		testWrite, openErr := os.OpenFile(dbPath, os.O_WRONLY|os.O_APPEND, 0644)
		if openErr != nil {
			logger.Error().
				Err(openErr).
				Str("db_path", dbPath).
				Str("permissions", mode.String()).
				Msg("Cannot open database file for writing - filesystem may be read-only")
			logger.Fatal().
				Str("db_path", dbPath).
				Msg("Database file cannot be opened for writing despite correct permissions")
		}
		testWrite.Close()
		logger.Debug().
			Str("db_path", dbPath).
			Msg("Database file can be opened for writing")
	} else if os.IsNotExist(err) {
		logger.Debug().
			Str("db_path", dbPath).
			Msg("Database file does not exist - will be created")
	} else {
		logger.Warn().
			Err(err).
			Str("db_path", dbPath).
			Msg("Could not stat database file")
	}

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		logger.Fatal().
			Err(err).
			Str("db_path", dbPath).
			Msg("Failed to open database")
	}
	defer db.Close()

	// Test database connection
	if err := db.Ping(); err != nil {
		logger.Error().
			Err(err).
			Str("db_path", dbPath).
			Msg("Database ping failed - connection error")

		// Additional diagnostics after ping failure
		if fileInfo, statErr := os.Stat(dbPath); statErr == nil {
			logger.Debug().
				Str("db_path", dbPath).
				Str("permissions", fileInfo.Mode().String()).
				Int64("size_bytes", fileInfo.Size()).
				Msg("Database file after failed ping")
		}

		logger.Fatal().
			Err(err).
			Str("db_path", dbPath).
			Msg("Failed to ping database")
	}

	logger.Debug().
		Str("db_path", dbPath).
		Msg("Database connection successful")

	// Initialize store
	store, err := NewSQLiteStore(db)
	if err != nil {
		logger.Error().
			Err(err).
			Str("db_path", dbPath).
			Msg("Store initialization failed during migration")

		// Check file permissions after migration failure
		if fileInfo, statErr := os.Stat(dbPath); statErr == nil {
			logger.Debug().
				Str("db_path", dbPath).
				Str("permissions", fileInfo.Mode().String()).
				Int64("size_bytes", fileInfo.Size()).
				Msg("Database file after migration failure")
		}

		logger.Fatal().
			Err(err).
			Str("db_path", dbPath).
			Msg("Failed to initialize store")
	}

	logger.Debug().
		Str("db_path", dbPath).
		Msg("Store initialized successfully")

	// Get API token for authentication
	apiToken := os.Getenv("API_TOKEN")
	if apiToken == "" {
		apiToken = "my-secret-token" // fallback for development
	}

	// HTTP server (API + metrics + health) - START THIS FIRST before Slack connection
	// This ensures the API is always available even if Slack is down
	serverPort := os.Getenv("SERVER_PORT")
	if serverPort == "" {
		serverPort = "8080"
	}
	metricsPort := os.Getenv("METRICS_PORT")
	if metricsPort == "" {
		metricsPort = "9090"
	}

	// Track Slack connection status for health checks
	var slackConnected bool
	var slackClient *slack.Client

	mux := http.NewServeMux()

	// Metrics endpoint
	mux.Handle("/metrics", promhttp.Handler())

	// Health endpoints
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		status := "healthy"
		statusCode := http.StatusOK
		if !slackConnected {
			status = "degraded"
			statusCode = http.StatusServiceUnavailable
		}
		w.WriteHeader(statusCode)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"status":          status,
			"slack_connected": slackConnected,
			"service":         "beerbot-backend",
		})
	})

	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	mux.HandleFunc("/api/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"status":          "healthy",
			"slack_connected": slackConnected,
			"service":         "beerbot-backend",
		})
	})

	// API endpoints with authentication
	givenHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user := r.URL.Query().Get("user")
		if user == "" {
			http.Error(w, "user required", http.StatusBadRequest)
			return
		}
		start, end, err := parseDateRangeFromParams(r)
		if err != nil {
			http.Error(w, "invalid or missing date range: "+err.Error(), http.StatusBadRequest)
			return
		}
		c, err := store.CountGivenInDateRange(user, start, end)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"user":  user,
			"start": start.Format("2006-01-02"),
			"end":   end.Format("2006-01-02"),
			"given": c,
		})
	})

	receivedHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user := r.URL.Query().Get("user")
		if user == "" {
			http.Error(w, "user required", http.StatusBadRequest)
			return
		}
		start, end, err := parseDateRangeFromParams(r)
		if err != nil {
			http.Error(w, "invalid or missing date range: "+err.Error(), http.StatusBadRequest)
			return
		}
		c, err := store.CountReceivedInDateRange(user, start, end)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"user":     user,
			"start":    start.Format("2006-01-02"),
			"end":      end.Format("2006-01-02"),
			"received": c,
		})
	})

	userHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		userID := r.URL.Query().Get("user")
		if userID == "" {
			http.Error(w, "user required", http.StatusBadRequest)
			return
		}
		
		w.Header().Set("Content-Type", "application/json")
		
		// If Slack is not connected, return user ID as fallback
		if slackClient == nil {
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"real_name":     userID, // Fallback to user ID
				"profile_image": nil,
			})
			return
		}
		
		// Try to get user info from Slack
		user, err := slackClient.GetUserInfo(userID)
		if err != nil {
			// On error, return user ID as fallback instead of 500 error
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"real_name":     userID, // Fallback to user ID
				"profile_image": nil,
			})
			return
		}
		
		// Success - return real name and avatar
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"real_name":     user.RealName,
			"profile_image": user.Profile.Image192,
		})
	})

	giversHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		list, err := store.GetAllGivers()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(list)
	})

	recipientsHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		list, err := store.GetAllRecipients()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(list)
	})

	mux.Handle("/api/given", authMiddleware(apiToken, givenHandler))
	mux.Handle("/api/received", authMiddleware(apiToken, receivedHandler))
	mux.Handle("/api/user", authMiddleware(apiToken, userHandler))
	mux.Handle("/api/givers", authMiddleware(apiToken, giversHandler))
	mux.Handle("/api/recipients", authMiddleware(apiToken, recipientsHandler))

	server := &http.Server{Addr: ":" + serverPort, Handler: mux}
	go func() {
		logger.Info().
			Str("port", serverPort).
			Msg("Starting HTTP API server")
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error().Err(err).Msg("HTTP server error")
		}
	}()

	// Create minimal Slack bot (non-fatal if fails)
	bot, err := NewMinimalSlackBot(botToken, appToken, store, logger)
	if err != nil {
		logger.Warn().Err(err).Msg("Failed to create Slack bot - will continue without Slack functionality")
		slackConnected = false
	} else {
		// Test Slack connection (non-fatal if fails)
		if err := bot.TestConnection(); err != nil {
			logger.Warn().Err(err).Msg("Failed to connect to Slack - will continue without Slack functionality")
			slackConnected = false
		} else {
			slackConnected = true
			slackClient = bot.GetAPIClient()
			logger.Info().Msg("Slack connection successful")
		}
	}

	// Run bot in background (only if connected)
	botErrCh := make(chan error, 1)
	if slackConnected && bot != nil {
		go func() {
			logger.Info().Msg("Starting minimal Slack bot with Socket Mode")
			botErrCh <- bot.Start()
		}()
	} else {
		logger.Warn().Msg("Slack bot not started due to connection issues - API server running in degraded mode")
	}

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

// parseDateRangeFromParams parses date range from query parameters
// Accepts either day=YYYY-MM-DD or start=YYYY-MM-DD&end=YYYY-MM-DD
func parseDateRangeFromParams(r *http.Request) (time.Time, time.Time, error) {
	day := r.URL.Query().Get("day")
	startStr := r.URL.Query().Get("start")
	endStr := r.URL.Query().Get("end")

	layout := "2006-01-02"
	if day != "" {
		t, err := time.Parse(layout, day)
		if err != nil {
			return time.Time{}, time.Time{}, err
		}
		return t, t, nil
	}
	if startStr != "" && endStr != "" {
		start, err1 := time.Parse(layout, startStr)
		end, err2 := time.Parse(layout, endStr)
		if err1 != nil || err2 != nil {
			return time.Time{}, time.Time{}, fmt.Errorf("invalid start or end date")
		}
		return start, end, nil
	}
	return time.Time{}, time.Time{}, fmt.Errorf("must provide either day=YYYY-MM-DD or start=YYYY-MM-DD&end=YYYY-MM-DD")
}

// authMiddleware validates Bearer token authentication
func authMiddleware(apiToken string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authHeader := r.Header.Get("Authorization")
		if authHeader == "" {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}

		bearerToken := strings.Split(authHeader, " ")
		if len(bearerToken) != 2 || bearerToken[0] != "Bearer" {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}

		if bearerToken[1] != apiToken {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}

		next.ServeHTTP(w, r)
	})
}
