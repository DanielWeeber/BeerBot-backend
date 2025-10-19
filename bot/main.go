package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/rs/zerolog"
	zlog "github.com/rs/zerolog/log"
	"github.com/slack-go/slack"
	"github.com/slack-go/slack/slackevents"
	"github.com/slack-go/slack/socketmode"
)

func parseRelativeDate(dateStr string) (time.Time, error) {
	dateStr = strings.TrimPrefix(dateStr, "-")
	unit := string(dateStr[len(dateStr)-1])
	value, err := strconv.Atoi(dateStr[:len(dateStr)-1])
	if err != nil {
		return time.Time{}, err
	}

	now := time.Now()
	switch unit {
	case "y":
		return now.AddDate(-value, 0, 0), nil
	case "m":
		return now.AddDate(0, -value, 0), nil
	case "d":
		return now.AddDate(0, 0, -value), nil
	default:
		return time.Time{}, fmt.Errorf("invalid time unit: %s", unit)
	}
}

// parseDateRangeFromParams parses day=, start=, end= from query params. If only day is set, returns that day. If start/end are set, returns the range. If none, returns error.
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

func main() {
	emoji := ":beer:"
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
	maxPerDay := flag.Int("max-per-day", maxPerDayDefault, "max beers a user may give per day")
	flag.Parse()

	if *botToken == "" || *appToken == "" || *channelID == "" {
		log.Fatal("bot-token, app-token and channel must be provided via flags or env (BOT_TOKEN, APP_TOKEN, CHANNEL)")
	}

	// open sqlite
	log.Printf("[DEBUG] DB_PATH env: %s", os.Getenv("DB_PATH"))
	log.Printf("[DEBUG] dbPath flag: %s", *dbPath)
	dbDir := ""
	dbFile := *dbPath
	if idx := strings.LastIndex(dbFile, "/"); idx != -1 {
		dbDir = dbFile[:idx]
	}
	if dbDir != "" {
		if _, err := os.Stat(dbDir); os.IsNotExist(err) {
			log.Printf("[DEBUG] DB directory %s does not exist, creating...", dbDir)
			if err := os.MkdirAll(dbDir, 0o755); err != nil {
				log.Fatalf("[DEBUG] Failed to create DB directory: %v", err)
			}
		} else {
			log.Printf("[DEBUG] DB directory %s exists", dbDir)
		}
	}
	if _, err := os.Stat(dbFile); err == nil {
		log.Printf("[DEBUG] DB file %s exists before init", dbFile)
	} else {
		log.Printf("[DEBUG] DB file %s does not exist before init", dbFile)
	}
	log.Printf("[DEBUG] Opening SQLite DB at path: %s", dbFile)
	db, err := sql.Open("sqlite3", dbFile)
	if err != nil {
		log.Fatalf("open db: %v", err)
	}
	defer db.Close()

	log.Printf("[DEBUG] Running DB migration...")
	store, err := NewSQLiteStore(db)
	if err != nil {
		log.Fatalf("init store: %v", err)
	}
	log.Printf("[DEBUG] DB migration complete. DB file: %s", dbFile)

	// init Slack client
	client := slack.New(*botToken, slack.OptionAppLevelToken(*appToken))
	socketClient := socketmode.New(client)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// structured logger (zerolog) + keep stdlib logger for simple messages
	zerolog.TimeFieldFormat = time.RFC3339
	zlogger := zerolog.New(zerolog.ConsoleWriter{Out: os.Stderr}).With().Timestamp().Logger()
	zlog.Logger = zlogger

	// Prometheus metrics
	msgsProcessed := prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "bwm_messages_processed_total",
		Help: "Number of messages processed by the bot",
	}, []string{"channel"})
	prometheus.MustRegister(msgsProcessed)

	// HTTP server for health + metrics
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	mux.Handle("/metrics", promhttp.Handler())
	// REST API: given and received
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
		_, _ = w.Write([]byte(fmt.Sprintf(`{"user":"%s","start":"%s","end":"%s","given":%d}`,
			user, start.Format("2006-01-02"), end.Format("2006-01-02"), c)))
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
		_, _ = w.Write([]byte(fmt.Sprintf(`{"user":"%s","start":"%s","end":"%s","received":%d}`,
			user, start.Format("2006-01-02"), end.Format("2006-01-02"), c)))
	})
	userHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		userID := r.URL.Query().Get("user")
		if userID == "" {
			http.Error(w, "user required", http.StatusBadRequest)
			return
		}
		user, err := client.GetUserInfo(userID)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		response := map[string]string{
			"real_name":     user.RealName,
			"profile_image": user.Profile.Image192, // or Image72 for smaller, Image512 for larger
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(response); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
	})
	mux.Handle("/api/given", authMiddleware(*apiToken, givenHandler))
	mux.Handle("/api/received", authMiddleware(*apiToken, receivedHandler))
	mux.Handle("/api/user", authMiddleware(*apiToken, userHandler))

	// list of all givers
	giversHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		list, err := store.GetAllGivers()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(list); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
	})

	// list of all recipients
	recipientsHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		list, err := store.GetAllRecipients()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(list); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
	})

	mux.Handle("/api/givers", giversHandler)
	mux.Handle("/api/recipients", recipientsHandler)
	srv := &http.Server{Addr: *addr, Handler: mux}
	go func() {
		log.Printf("http listening on %s", *addr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("http failed: %v", err)
		}
	}()

	// signal handling
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)

	// Slack event loop (Socket Mode Events API)
	go func() {
		for evt := range socketClient.Events {
			logEvt := zlog.Debug().Str("type", string(evt.Type))
			if evt.Request != nil && evt.Request.EnvelopeID != "" {
				logEvt = logEvt.Str("envelope_id", evt.Request.EnvelopeID)
			}
			logEvt.Msg("received slack event")
			switch evt.Type {
			case socketmode.EventTypeEventsAPI:
				socketClient.Ack(*evt.Request)
				eventsAPIEvent, ok := evt.Data.(slackevents.EventsAPIEvent)
				if !ok {
					log.Printf("unexpected event data type: %T", evt.Data)
					continue
				}
				// Deduplicate based on the Events API envelope ID when available.
				// Try to get a stable envelope id from the socketmode event request
				envelopeID := ""
				if evt.Request != nil && evt.Request.EnvelopeID != "" {
					envelopeID = evt.Request.EnvelopeID
				}
				if eventsAPIEvent.Type == slackevents.CallbackEvent {
					inner := eventsAPIEvent.InnerEvent
					switch ev := inner.Data.(type) {
					case *slackevents.MessageEvent:
						// limit to the configured channel and only user messages
						if ev.Channel == *channelID && ev.User != "" {
							// ignore message subtypes (edits, bot messages, etc.) -- only plain messages
							// SubType is empty for normal user messages
							if ev.SubType != "" {
								zlog.Debug().Str("subtype", ev.SubType).Str("channel", ev.Channel).Str("user", ev.User).Msg("ignoring message with subtype")
								continue
							}

							// Debug: emit event envelope id, fallback event id, timestamp and raw event types
							zlog.Debug().Str("envelope_id", envelopeID).Str("channel", ev.Channel).Str("user", ev.User).Str("ts", ev.TimeStamp).Msg("received message event (debug)")

							// compute a stable event id for message events: prefer envelopeID if present,
							// otherwise build one from channel|user|ts which is stable across redeliveries
							eventID := envelopeID
							if eventID == "" {
								eventID = fmt.Sprintf("msg|%s|%s|%s", ev.Channel, ev.User, ev.TimeStamp)
							}
							// Attempt to mark the event as processed before doing work.
							// INSERT OR IGNORE will return 0 affected rows if the event
							// already exists; in that case we skip processing. This
							// avoids the race where two deliveries check IsEventProcessed
							// concurrently and both proceed to write/Log.
							if eventID != "" {
								if ok, err := store.TryMarkEventProcessed(eventID, time.Now()); err != nil {
									zlog.Error().Err(err).Msg("failed to try-mark event processed")
									continue
								} else if !ok {
									// already processed
									continue
								}
							}
							// New logic: associate beers with the last seen mention

							mentionRe := regexp.MustCompile(`<@([A-Z0-9]+)>`)
							emojiRe := regexp.MustCompile(regexp.QuoteMeta(emoji))

							mentions := mentionRe.FindAllStringSubmatch(ev.Text, -1)
							mentionIndices := mentionRe.FindAllStringSubmatchIndex(ev.Text, -1)
							emojiIndices := emojiRe.FindAllStringIndex(ev.Text, -1)

							if len(mentions) == 0 || len(emojiIndices) == 0 {
								continue
							}

							recipientBeers := make(map[string]int)
							for _, emojiIdx := range emojiIndices {
								lastMentionIdx := -1
								var recipientID string
								for i, mentionIdx := range mentionIndices {
									if emojiIdx[0] > mentionIdx[1] { // emoji is after mention
										if mentionIdx[0] > lastMentionIdx {
											lastMentionIdx = mentionIdx[0]
											recipientID = mentions[i][1]
										}
									}
								}
								if recipientID != "" {
									recipientBeers[recipientID]++
								}
							}

							totalBeersToGive := 0
							for _, count := range recipientBeers {
								totalBeersToGive += count
							}

							if totalBeersToGive == 0 {
								continue
							}

							today := time.Now().UTC().Format("2006-01-02")
							givenToday, err := store.CountGivenOnDate(ev.User, today)
							if err != nil {
								zlog.Error().Err(err).Msg("count given on date failed")
								continue
							}

							if givenToday >= *maxPerDay {
								zlog.Info().Str("user", ev.User).Int("givenToday", givenToday).Msg("daily limit reached")
								message := fmt.Sprintf("Sorry <@%s>, you have reached your daily limit of %d beers.", ev.User, *maxPerDay)
								if _, _, err := client.PostMessage(ev.Channel, slack.MsgOptionText(message, false)); err != nil {
									zlog.Error().Err(err).Msg("failed to post message")
								}
								continue
							}

							allowed := *maxPerDay - givenToday
							if totalBeersToGive > allowed {
								zlog.Info().Str("user", ev.User).Int("givenToday", givenToday).Int("totalBeersToGive", totalBeersToGive).Int("allowed", allowed).Msg("daily limit would be exceeded")
								message := fmt.Sprintf("Sorry <@%s>, you are trying to give %d beers, but you only have %d left for today.", ev.User, totalBeersToGive, allowed)
								if _, _, err := client.PostMessage(ev.Channel, slack.MsgOptionText(message, false)); err != nil {
									zlog.Error().Err(err).Msg("failed to post message")
								}
								continue
							}

							for recipient, count := range recipientBeers {
								var eventTime time.Time
								if ev.TimeStamp != "" {
									if t, err := parseSlackTimestamp(ev.TimeStamp); err == nil {
										eventTime = t
									} else {
										eventTime = time.Now()
									}
								} else {
									eventTime = time.Now()
								}
								if err := store.AddBeer(ev.User, recipient, ev.TimeStamp, eventTime, count); err != nil {
									zlog.Error().Err(err).Msg("failed to add beer")
								}
								zlog.Info().Str("giver", ev.User).Str("recipient", recipient).Int("count", count).Msg("beer given")
							}
							// event was pre-marked via TryMarkEventProcessed
						}
					default:
						zlog.Debug().Str("event_type", eventsAPIEvent.Type).Msg("ignoring non-message callback event")
					}
				}
			case socketmode.EventTypeHello:
				log.Printf("socketmode: hello")
			default:
				// ignore others
			}
		}
	}()

	// Start socketmode
	go func() {
		if err := socketClient.RunContext(ctx); err != nil {
			log.Printf("socketmode run: %v", err)
		}
	}()

	select {
	case <-sigs:
		log.Println("shutdown signal")
	case <-ctx.Done():
	}

	// shutdown http
	ctxShutdown, cancelShutdown := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelShutdown()
	_ = srv.Shutdown(ctxShutdown)
	// socketmode client will stop when context is cancelled / RunContext returns
}

// parseSlackTimestamp parses Slack timestamps of the form "1234567890.123456"
func authMiddleware(apiToken string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		zlog.Debug().Str("apiToken", apiToken).Str("authHeader", r.Header.Get("Authorization")).Msg("auth middleware")
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

// and returns a time.Time preserving fractional seconds.
func parseSlackTimestamp(ts string) (time.Time, error) {
	// Slack ts format: seconds[.fraction]
	var secPart string
	var fracPart string
	if idx := strings.IndexByte(ts, '.'); idx >= 0 {
		secPart = ts[:idx]
		fracPart = ts[idx+1:]
	} else {
		secPart = ts
	}
	secs, err := strconv.ParseInt(secPart, 10, 64)
	if err != nil {
		return time.Time{}, err
	}
	nsec := int64(0)
	if fracPart != "" {
		// pad or trim to nanoseconds
		if len(fracPart) > 9 {
			fracPart = fracPart[:9]
		} else {
			for len(fracPart) < 9 {
				fracPart += "0"
			}
		}
		if nf, err := strconv.ParseInt(fracPart, 10, 64); err == nil {
			nsec = nf
		}
	}
	return time.Unix(secs, nsec), nil
}
