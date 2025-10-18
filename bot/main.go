package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	stdlog "log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"regexp"
	"strconv"
	"strings"

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

func parseDateRange(dateStr string) (time.Time, time.Time, error) {
	if !strings.HasPrefix(dateStr, "-") {
		t, err := time.Parse("2006-01-02", dateStr)
		if err != nil {
			return time.Time{}, time.Time{}, err
		}
		return t, t, nil
	}

	t, err := parseRelativeDate(dateStr)
	if err != nil {
		return time.Time{}, time.Time{}, err
	}

	return t, time.Now(), nil
}

func main() {
	var (
		dbPath    = flag.String("db", "./data/bot.db", "sqlite database path")
		botToken  = flag.String("bot-token", "", "slack bot token (xoxb-...)")
		appToken  = flag.String("app-token", "", "slack app-level token (xapp-...)")
		channelID = flag.String("channel", "", "channel id to monitor")
		addr      = flag.String("addr", ":8080", "health/metrics listen address")
		maxPerDay = flag.Int("max-per-day", 10, "max beers a user may give per day")
		apiToken  = flag.String("api-token", "", "api token for authentication")
	)
	flag.Parse()

	if *botToken == "" || *appToken == "" || *channelID == "" {
		stdlog.Fatal("bot-token, app-token and channel must be provided via flags or env")
	}

	// open sqlite
	if err := os.MkdirAll("./data", 0o755); err != nil {
		stdlog.Fatalf("create data dir: %v", err)
	}
	db, err := sql.Open("sqlite3", *dbPath+"?_foreign_keys=1")
	if err != nil {
		stdlog.Fatalf("open db: %v", err)
	}
	defer db.Close()

	store, err := NewSQLiteStore(db)
	if err != nil {
		stdlog.Fatalf("init store: %v", err)
	}

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
		date := r.URL.Query().Get("date")
		if user == "" || date == "" {
			http.Error(w, "user and date required", http.StatusBadRequest)
			return
		}
		start, end, err := parseDateRange(date)
		if err != nil {
			http.Error(w, "invalid date format", http.StatusBadRequest)
			return
		}
		c, err := store.CountGivenInDateRange(user, start, end)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(fmt.Sprintf(`{"user":"%s","date":"%s","given":%d}`, user, date, c)))
	})
	receivedHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user := r.URL.Query().Get("user")
		date := r.URL.Query().Get("date")
		if user == "" {
			http.Error(w, "user required", http.StatusBadRequest)
			return
		}
		start, end, err := parseDateRange(date)
		if err != nil {
			http.Error(w, "invalid date format", http.StatusBadRequest)
			return
		}
		c, err := store.CountReceivedInDateRange(user, start, end)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(fmt.Sprintf(`{"user":"%s","date":"%s","received":%d}`, user, date, c)))
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
		response := map[string]string{"real_name": user.RealName}
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

	mux.Handle("/api/givers", authMiddleware(*apiToken, giversHandler))
	mux.Handle("/api/recipients", authMiddleware(*apiToken, recipientsHandler))
	srv := &http.Server{Addr: *addr, Handler: mux}
	go func() {
		stdlog.Printf("http listening on %s", *addr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			stdlog.Fatalf("http failed: %v", err)
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
					stdlog.Printf("unexpected event data type: %T", evt.Data)
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
							beerRe := regexp.MustCompile(`:beer:`)

							mentions := mentionRe.FindAllStringSubmatch(ev.Text, -1)
							mentionIndices := mentionRe.FindAllStringSubmatchIndex(ev.Text, -1)
							beerIndices := beerRe.FindAllStringIndex(ev.Text, -1)

							if len(mentions) == 0 || len(beerIndices) == 0 {
								continue
							}

							recipientBeers := make(map[string]int)
							for _, beerIdx := range beerIndices {
								lastMentionIdx := -1
								var recipientID string
								for i, mentionIdx := range mentionIndices {
									if beerIdx[0] > mentionIdx[1] { // beer is after mention
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
				stdlog.Printf("socketmode: hello")
			default:
				// ignore others
			}
		}
	}()

	// Start socketmode
	go func() {
		if err := socketClient.RunContext(ctx); err != nil {
			stdlog.Printf("socketmode run: %v", err)
		}
	}()

	select {
	case <-sigs:
		stdlog.Println("shutdown signal")
	case <-ctx.Done():
	}

	// shutdown http
	ctxShutdown, cancelShutdown := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelShutdown()
	_ = srv.Shutdown(ctxShutdown)
	// socketmode client will stop when context is cancelled / RunContext returns
}

// parseSlackTimestamp parses Slack timestamps of the form "1234567890.123456"
// and returns a time.Time preserving fractional seconds.
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
