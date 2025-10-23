package main

import (
    "context"
    "fmt"
    "math"
    "regexp"
    "strconv"
    "strings"
    "sync"
    "time"

    zlog "github.com/rs/zerolog/log"
    "github.com/slack-go/slack"
    "github.com/slack-go/slack/slackevents"
    "github.com/slack-go/slack/socketmode"
)

// SlackConnectionManager manages the Slack socket mode connection, including automatic
// reconnection using an exponential backoff strategy. The struct is safe for concurrent
// use due to an internal sync.RWMutex that protects connection state and related fields.
type SlackConnectionManager struct {
    client         *slack.Client
    socketClient   *socketmode.Client
    botToken       string
    appToken       string
    isConnected    bool
    lastPing       time.Time
    reconnectCount int
    mu             sync.RWMutex
}

// NewSlackConnectionManager creates a new connection manager
func NewSlackConnectionManager(botToken, appToken string) *SlackConnectionManager {
    client := slack.New(botToken, slack.OptionAppLevelToken(appToken))
    socketClient := socketmode.New(client)

    return &SlackConnectionManager{
        client:       client,
        socketClient: socketClient,
        botToken:     botToken,
        appToken:     appToken,
        lastPing:     time.Now(),
    }
}

// IsConnected returns the current connection status
func (scm *SlackConnectionManager) IsConnected() bool {
    scm.mu.RLock()
    defer scm.mu.RUnlock()
    return scm.isConnected
}

// SetConnected updates the connection status
func (scm *SlackConnectionManager) setConnected(connected bool) {
    scm.mu.Lock()
    defer scm.mu.Unlock()
    scm.isConnected = connected
    if connected {
        scm.lastPing = time.Now()
        zlog.Info().Int("reconnect_count", scm.reconnectCount).Msg("Slack connection established")
        SetSlackConnected(true)
    } else {
        zlog.Warn().Msg("Slack connection lost")
        SetSlackConnected(false)
    }
}

// GetClient returns the Slack client
func (scm *SlackConnectionManager) GetClient() *slack.Client {
    return scm.client
}

// GetSocketClient returns the socket mode client
func (scm *SlackConnectionManager) GetSocketClient() *socketmode.Client {
    return scm.socketClient
}

// TestConnection tests the Slack API connection
func (scm *SlackConnectionManager) TestConnection(ctx context.Context) error {
    ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
    defer cancel()

    _, err := scm.client.AuthTestContext(ctx)
    return err
}

// StartWithReconnection starts the socket mode client with automatic reconnection
func (scm *SlackConnectionManager) StartWithReconnection(ctx context.Context, eventHandler func(socketmode.Event)) {
    const maxReconnectDelay = 5 * time.Minute

    go func() {
        for {
            select {
            case <-ctx.Done():
                zlog.Info().Msg("Connection manager shutting down")
                return
            default:
                // Calculate exponential backoff delay
                delay := time.Duration(math.Pow(2, float64(scm.reconnectCount))) * time.Second
                if delay > maxReconnectDelay {
                    delay = maxReconnectDelay
                }

                if scm.reconnectCount > 0 {
                    zlog.Warn().Dur("delay", delay).Int("attempt", scm.reconnectCount+1).Msg("Reconnecting to Slack...")
                    select {
                    case <-time.After(delay):
                    case <-ctx.Done():
                        return
                    }
                }

                // Test connection first
                if err := scm.TestConnection(ctx); err != nil {
                    zlog.Error().Err(err).Msg("Slack API connection test failed")
                    IncSlackReconnect()
                    scm.reconnectCount++
                    continue
                }

                // Create new socket client for this connection attempt
                scm.socketClient = socketmode.New(scm.client)
                scm.setConnected(true)
                scm.reconnectCount = 0

                // Start event processing
                go scm.processEvents(eventHandler)

                // Run the socket mode client
                zlog.Info().Msg("Starting Slack socket mode client...")
                if err := scm.socketClient.RunContext(ctx); err != nil {
                    scm.setConnected(false)
                    if ctx.Err() != nil {
                        zlog.Info().Err(err).Msg("Socket mode client stopped due to context cancellation")
                        return
                    } else {
                        zlog.Error().Err(err).Msg("Socket mode client error, will reconnect")
                        IncSlackReconnect()
                        scm.reconnectCount++
                    }
                } else {
                    scm.setConnected(false)
                    zlog.Info().Msg("Socket mode client stopped gracefully")
                }
            }
        }
    }()
}

// processEvents handles socket mode events
func (scm *SlackConnectionManager) processEvents(eventHandler func(socketmode.Event)) {
    for evt := range scm.socketClient.Events {
        scm.mu.Lock()
        scm.lastPing = time.Now()
        scm.mu.Unlock()

        // Handle special events
        if evt.Type == socketmode.EventTypeHello {
            zlog.Debug().Msg("Slack socket mode: hello")
            scm.setConnected(true)
        }

        zlog.Debug().Str("type", string(evt.Type)).Msg("Slack socket mode event")

        // Call the custom event handler
        if eventHandler != nil {
            eventHandler(evt)
        }
    }
}

func startConnectionMonitor(ctx context.Context, slackManager *SlackConnectionManager) {
    go func() {
        ticker := time.NewTicker(30 * time.Second)
        defer ticker.Stop()

        for {
            select {
            case <-ticker.C:
                connected := slackManager.IsConnected()
                if !connected {
                    zlog.Warn().Msg("Slack connection monitor: DISCONNECTED")
                } else {
                    // Test actual API connection periodically
                    if err := slackManager.TestConnection(ctx); err != nil {
                        zlog.Error().Err(err).Msg("Slack connection monitor: API test failed")
                    }
                }
            case <-ctx.Done():
                zlog.Info().Msg("Connection monitor stopping")
                return
            }
        }
    }()
}

func buildEventHandler(store Store, client *slack.Client, slackManager *SlackConnectionManager, channelID string, emoji string, maxPerDay int) func(socketmode.Event) {
    return func(evt socketmode.Event) {
        switch evt.Type {
        case socketmode.EventTypeEventsAPI:
            if evt.Request != nil {
                slackManager.GetSocketClient().Ack(*evt.Request)
            }
            eventsAPIEvent, ok := evt.Data.(slackevents.EventsAPIEvent)
            if !ok {
                zlog.Warn().Str("type", fmt.Sprintf("%T", evt.Data)).Msg("unexpected event data type")
                return
            }
            if eventsAPIEvent.Type == slackevents.CallbackEvent {
                inner := eventsAPIEvent.InnerEvent
                switch ev := inner.Data.(type) {
                case *slackevents.MessageEvent:
                    if ev.Channel == channelID && ev.User != "" {
                        if ev.SubType != "" {
                            IncBeerOutcome(ev.Channel, "subtype")
                            return
                        }
                        envelopeID := ""
                        if evt.Request != nil && evt.Request.EnvelopeID != "" {
                            envelopeID = evt.Request.EnvelopeID
                        }
                        eventID := envelopeID
                        if eventID == "" {
                            eventID = fmt.Sprintf("msg|%s|%s|%s", ev.Channel, ev.User, ev.TimeStamp)
                        }
                        if eventID != "" {
                            if ok, err := store.TryMarkEventProcessed(eventID, time.Now()); err != nil {
                                zlog.Error().Err(err).Msg("failed to try-mark event processed")
                                IncBeerOutcome(ev.Channel, "event_mark_error")
                                return
                            } else if !ok {
                                IncBeerOutcome(ev.Channel, "duplicate")
                                return
                            }
                        }

                        mentionRe := regexp.MustCompile(`<@([A-Z0-9]+)>`)
                        emojiRe := regexp.MustCompile(regexp.QuoteMeta(emoji))

                        mentions := mentionRe.FindAllStringSubmatch(ev.Text, -1)
                        mentionIndices := mentionRe.FindAllStringSubmatchIndex(ev.Text, -1)
                        emojiIndices := emojiRe.FindAllStringIndex(ev.Text, -1)

                        if len(mentions) == 0 || len(emojiIndices) == 0 {
                            if len(mentions) == 0 {
                                IncBeerOutcome(ev.Channel, "no_mentions")
                            } else {
                                IncBeerOutcome(ev.Channel, "no_emoji")
                            }
                            return
                        }

                        recipientBeers := make(map[string]int)
                        for _, emojiIdx := range emojiIndices {
                            lastMentionIdx := -1
                            var recipientID string
                            for i, mentionIdx := range mentionIndices {
                                if emojiIdx[0] > mentionIdx[1] {
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
                            IncBeerOutcome(ev.Channel, "zero_total")
                            return
                        }

                        today := time.Now().UTC().Format("2006-01-02")
                        givenToday, err := store.CountGivenOnDate(ev.User, today)
                        if err != nil {
                            zlog.Error().Err(err).Msg("count given on date failed")
                            IncBeerOutcome(ev.Channel, "count_error")
                            return
                        }

                        if givenToday >= maxPerDay {
                            zlog.Info().Str("user", ev.User).Int("givenToday", givenToday).Msg("daily limit reached")
                            message := fmt.Sprintf("Sorry <@%s>, you have reached your daily limit of %d beers.", ev.User, maxPerDay)
                            if _, _, err := client.PostMessage(ev.Channel, slack.MsgOptionText(message, false)); err != nil {
                                zlog.Error().Err(err).Msg("failed to post message")
                            }
                            IncBeerOutcome(ev.Channel, "limit_reached")
                            return
                        }

                        allowed := maxPerDay - givenToday
                        if totalBeersToGive > allowed {
                            zlog.Info().Str("user", ev.User).Int("givenToday", givenToday).Int("totalBeersToGive", totalBeersToGive).Int("allowed", allowed).Msg("daily limit would be exceeded")
                            message := fmt.Sprintf("Sorry <@%s>, you are trying to give %d beers, but you only have %d left for today.", ev.User, totalBeersToGive, allowed)
                            if _, _, err := client.PostMessage(ev.Channel, slack.MsgOptionText(message, false)); err != nil {
                                zlog.Error().Err(err).Msg("failed to post message")
                            }
                            IncBeerOutcome(ev.Channel, "exceeded_allowed")
                            return
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
                                IncBeerOutcome(ev.Channel, "store_error")
                            }
                            zlog.Info().Str("giver", ev.User).Str("recipient", recipient).Int("count", count).Msg("beer given")
                        }
                        IncMessagesProcessed(ev.Channel)
                        IncBeerOutcome(ev.Channel, "stored")
                    }
                default:
                    // ignore
                }
            }
        default:
            // ignore other event types
        }
    }
}

// parseSlackTimestamp parses Slack timestamps of the form "1234567890.123456"
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
