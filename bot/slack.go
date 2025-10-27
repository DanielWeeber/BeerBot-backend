package main

import (
	"errors"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/rs/zerolog"
	"github.com/slack-go/slack"
	"github.com/slack-go/slack/slackevents"
	"github.com/slack-go/slack/socketmode"
)

// MinimalSlackBot represents a minimal Slack bot using Socket Mode
// Following the xNok/slack-go-demo-socketmode pattern for simplicity and reliability
type MinimalSlackBot struct {
	api          *slack.Client
	client       *socketmode.Client
	logger       zerolog.Logger
	store        Store
	eventCounter *prometheus.CounterVec
	errorCounter *prometheus.CounterVec
	maxGift      int
	readOnly     bool
}

// NewMinimalSlackBot creates a new minimal Slack bot instance
func NewMinimalSlackBot(botToken, appToken string, store Store, logger zerolog.Logger) (*MinimalSlackBot, error) {
	if botToken == "" {
		return nil, errors.New("bot token is required")
	}
	if appToken == "" {
		return nil, errors.New("app token is required")
	}

	// Create Slack API client
	api := slack.New(
		botToken,
		slack.OptionDebug(false),
		slack.OptionAppLevelToken(appToken),
	)

	// Create Socket Mode client - minimal setup
	client := socketmode.New(api)

	// Initialize metrics
	eventCounter := prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "slack_events_total",
			Help: "Total number of Slack events processed",
		},
		[]string{"type", "status"},
	)

	errorCounter := prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "slack_errors_total",
			Help: "Total number of Slack errors",
		},
		[]string{"type"},
	)

	prometheus.MustRegister(eventCounter, errorCounter)

	// Configurable limits / modes
	maxGift := 10
	if v := strings.TrimSpace(os.Getenv("MAX_BEER_GIFT")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			maxGift = n
		}
	}
	readOnly := strings.EqualFold(os.Getenv("READ_ONLY"), "true") || os.Getenv("READ_ONLY") == "1"

	return &MinimalSlackBot{
		api:          api,
		client:       client,
		logger:       logger,
		store:        store,
		eventCounter: eventCounter,
		errorCounter: errorCounter,
		maxGift:      maxGift,
		readOnly:     readOnly,
	}, nil
}

// Start runs the Slack bot with minimal Socket Mode setup
func (bot *MinimalSlackBot) Start() error {
	bot.logger.Info().Msg("Starting minimal Slack bot...")

	// Set up event handlers
	go bot.handleEvents()

	// Run the Socket Mode client - this is the key simplification
	// No custom reconnection logic, just use the library's built-in handling
	return bot.client.Run()
}

// Stop attempts to close the socketmode client connection.
func (bot *MinimalSlackBot) Stop() {
	if bot.client != nil {
		// Best-effort: socketmode.Client offers a Debugf and internal connection handling; no public close.
		bot.logger.Info().Msg("Stopping Slack bot (process will exit)")
	}
}

// handleEvents processes incoming Slack events
func (bot *MinimalSlackBot) handleEvents() {
	for event := range bot.client.Events {
		bot.processEvent(event)
	}
}

// processEvent handles individual Slack events
func (bot *MinimalSlackBot) processEvent(evt socketmode.Event) {
	// ACK only EventsAPI & Interaction events that carry a request envelope
	if (evt.Type == socketmode.EventTypeEventsAPI || evt.Type == socketmode.EventTypeSlashCommand) && evt.Request != nil {
		bot.client.Ack(*evt.Request)
	}

	switch evt.Type {
	case socketmode.EventTypeEventsAPI:
		eventsAPIEvent, ok := evt.Data.(slackevents.EventsAPIEvent)
		if !ok {
			bot.logger.Error().Msg("Failed to cast event to EventsAPIEvent")
			bot.errorCounter.WithLabelValues("cast_error").Inc()
			return
		}
		bot.handleEventsAPIEvent(eventsAPIEvent)
	case socketmode.EventTypeSlashCommand:
		cmd, ok := evt.Data.(slack.SlashCommand)
		if !ok {
			bot.logger.Error().Msg("Failed to cast event to SlashCommand")
			bot.errorCounter.WithLabelValues("cast_error").Inc()
			return
		}
		bot.handleSlashCommand(cmd)
	default:
		bot.logger.Trace().Str("event_type", string(evt.Type)).Msg("Ignoring non-EventsAPI event")
	}
}

// handleEventsAPIEvent processes Events API events
func (bot *MinimalSlackBot) handleEventsAPIEvent(event slackevents.EventsAPIEvent) {
	switch event.Type {
	case slackevents.CallbackEvent:
		innerEvent := event.InnerEvent
		switch ev := innerEvent.Data.(type) {
		case *slackevents.MessageEvent:
			bot.handleMessage(ev)
		default:
			bot.logger.Debug().
				Str("inner_event_type", innerEvent.Type).
				Msg("Unhandled inner event type")
		}
	default:
		bot.logger.Debug().
			Str("event_type", event.Type).
			Msg("Unhandled Events API event type")
	}
}

// handleMessage processes message events for beer giving
func (bot *MinimalSlackBot) handleMessage(event *slackevents.MessageEvent) {
	// Skip bot messages, empty text, edits (subtypes), and thread replies (only handle top-level)
	if event.BotID != "" || event.Text == "" || event.SubType != "" {
		return
	}
	if event.ThreadTimeStamp != "" && event.ThreadTimeStamp != event.EventTimeStamp {
		return // ignore replies in threads
	}

	bot.eventCounter.WithLabelValues("message", "received").Inc()

	if bot.isBeerGiving(event.Text) {
		bot.processBeerGiving(event)
	}
}

// isBeerGiving checks if the message is giving beer to someone
var compiledGiftPatterns = []*regexp.Regexp{
	regexp.MustCompile(`🍺\s*<@[A-Z0-9]+>`),
	regexp.MustCompile(`(?i)beer\s+<@[A-Z0-9]+>`),
	regexp.MustCompile(`:beer:\s*<@[A-Z0-9]+>`),
	regexp.MustCompile(`🍻\s*<@[A-Z0-9]+>`),
	regexp.MustCompile(`:beers:\s*<@[A-Z0-9]+>`),
}

func (bot *MinimalSlackBot) isBeerGiving(text string) bool {
	for _, rx := range compiledGiftPatterns {
		if rx.MatchString(text) {
			return true
		}
	}
	return false
}

// processBeerGiving handles beer giving events
func (bot *MinimalSlackBot) processBeerGiving(event *slackevents.MessageEvent) {
	// Check for event deduplication
	eventTime := parseSlackTS(event.EventTimeStamp)
	alreadyProcessed, err := bot.store.TryMarkEventProcessed(event.EventTimeStamp, eventTime)
	if err != nil {
		_ = bot.store.RecordBeerEventOutcome(event.EventTimeStamp, event.User, "", 0, "error", eventTime)
		bot.logger.Error().
			Err(err).
			Str("timestamp", event.EventTimeStamp).
			Msg("Error checking event deduplication")
		bot.errorCounter.WithLabelValues("dedup_error").Inc()
		return
	}
	if alreadyProcessed {
		_ = bot.store.RecordBeerEventOutcome(event.EventTimeStamp, event.User, "", 0, "duplicate", eventTime)
		bot.logger.Debug().
			Str("timestamp", event.EventTimeStamp).
			Msg("Event already processed, skipping")
		bot.eventCounter.WithLabelValues("beer_giving", "duplicate").Inc()
		return
	}

	// Extract recipient user ID
	recipient := bot.extractRecipient(event.Text)
	if recipient == "" {
		_ = bot.store.RecordBeerEventOutcome(event.EventTimeStamp, event.User, "", 0, "invalid_recipient", eventTime)
		bot.logger.Warn().
			Str("text", event.Text).
			Msg("Could not extract recipient from beer message")
		bot.eventCounter.WithLabelValues("beer_giving", "invalid_recipient").Inc()
		// Ephemeral feedback
		bot.postEphemeral(event.Channel, event.User, "⚠️ Could not find a valid recipient in your beer message.")
		return
	}

	// Extract quantity (default to 1)
	quantity := bot.extractQuantity(event.Text)
	if quantity > bot.maxGift {
		bot.logger.Debug().Int("requested", quantity).Int("capped", bot.maxGift).Msg("Capping beer quantity")
		quantity = bot.maxGift
	}

	// Prevent self gifting
	if recipient == event.User {
		_ = bot.store.RecordBeerEventOutcome(event.EventTimeStamp, event.User, recipient, quantity, "self_gift", eventTime)
		bot.eventCounter.WithLabelValues("beer_giving", "self_gift").Inc()
		bot.postEphemeral(event.Channel, event.User, "🍺 You can't gift beer to yourself. Find a teammate!")
		return
	}

	bot.logger.Info().
		Str("giver", event.User).
		Str("recipient", recipient).
		Int("quantity", quantity).
		Str("channel", event.Channel).
		Msg("Processing beer giving")

	if bot.readOnly {
		bot.logger.Info().Str("mode", "read-only").Msg("Skipping DB write (READ_ONLY enabled)")
	} else {
		storeErr := bot.store.AddBeer(event.User, recipient, event.EventTimeStamp, eventTime, quantity)
		if storeErr != nil {
			_ = bot.store.RecordBeerEventOutcome(event.EventTimeStamp, event.User, recipient, quantity, "error", eventTime)
			bot.logger.Error().
				Err(storeErr).
				Str("giver", event.User).
				Str("recipient", recipient).
				Int("quantity", quantity).
				Msg("Failed to store beer transaction")
			bot.errorCounter.WithLabelValues("storage_error").Inc()
			return
		}
	}

	_ = bot.store.RecordBeerEventOutcome(event.EventTimeStamp, event.User, recipient, quantity, "success", eventTime)
	bot.eventCounter.WithLabelValues("beer_giving", "success").Inc()
	bot.sendBeerConfirmation(event.Channel, event.User, recipient, quantity)
}

// extractRecipient extracts the recipient user ID from the message text
func (bot *MinimalSlackBot) extractRecipient(text string) string {
	re := regexp.MustCompile(`<@([A-Z0-9]+)>`)
	matches := re.FindStringSubmatch(text)
	if len(matches) > 1 {
		return matches[1]
	}
	return ""
}

// extractQuantity extracts the beer quantity from the message text
func (bot *MinimalSlackBot) extractQuantity(text string) int {
	// Look for numbers in the message
	re := regexp.MustCompile(`\b(\d+)\b`)
	matches := re.FindAllString(text, -1)

	for _, match := range matches {
		if num, err := strconv.Atoi(match); err == nil && num > 0 && num <= 10 {
			return num
		}
	}

	// Count beer emojis
	beerCount := strings.Count(text, "🍺") + strings.Count(text, "🍻")
	if beerCount > 0 && beerCount <= 10 {
		return beerCount
	}

	return 1 // Default to 1 beer
}

// parseSlackTS converts a Slack ts (e.g. "1717691574.123456") to time.Time (seconds precision)
func parseSlackTS(ts string) time.Time {
	if ts == "" {
		return time.Now().UTC()
	}
	parts := strings.Split(ts, ".")
	sec, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		return time.Now().UTC()
	}
	return time.Unix(sec, 0).UTC()
}

// sendBeerConfirmation sends a confirmation message for beer giving
func (bot *MinimalSlackBot) sendBeerConfirmation(channel, giver, recipient string, quantity int) {
	beerEmoji := "🍺"
	if quantity > 1 {
		beerEmoji = "🍻"
	}

	message := fmt.Sprintf(
		"%s <@%s> gave %d beer%s to <@%s>!",
		beerEmoji,
		giver,
		quantity,
		func() string {
			if quantity == 1 {
				return ""
			}
			return "s"
		}(),
		recipient,
	)

	_, _, err := bot.api.PostMessage(
		channel,
		slack.MsgOptionText(message, false),
	)

	if err != nil {
		bot.logger.Error().
			Err(err).
			Str("channel", channel).
			Msg("Failed to send confirmation message")
		bot.errorCounter.WithLabelValues("message_error").Inc()
	} else {
		bot.logger.Debug().
			Str("channel", channel).
			Str("message", message).
			Msg("Sent beer confirmation message")
	}
}

// postEphemeral sends an ephemeral message (best-effort, logs errors only).
func (bot *MinimalSlackBot) postEphemeral(channel, user, text string) {
	if channel == "" || user == "" {
		return
	}
	_, err := bot.api.PostEphemeral(channel, user, slack.MsgOptionText(text, false))
	if err != nil {
		bot.logger.Debug().Err(err).Msg("Failed to post ephemeral message")
	}
}

// handleSlashCommand processes slash commands (e.g., /beer-stats)
func (bot *MinimalSlackBot) handleSlashCommand(cmd slack.SlashCommand) {
	// Only support /beer-stats for now
	if cmd.Command != "/beer-stats" {
		bot.api.PostEphemeral(cmd.ChannelID, cmd.UserID, slack.MsgOptionText("Unsupported command.", false))
		return
	}
	// Parse optional args: timeframe=7 limit=5
	days := 7
	limit := 5
	parts := strings.Fields(cmd.Text)
	for _, p := range parts {
		kv := strings.SplitN(p, "=", 2)
		if len(kv) != 2 {
			continue
		}
		switch strings.ToLower(kv[0]) {
		case "timeframe", "days":
			if n, err := strconv.Atoi(kv[1]); err == nil && n > 0 && n <= 365 {
				days = n
			}
		case "limit":
			if n, err := strconv.Atoi(kv[1]); err == nil && n > 0 && n <= 25 {
				limit = n
			}
		}
	}
	end := time.Now()
	start := end.AddDate(0, 0, -days)
	givers, gErr := bot.store.TopGivers(start, end, limit)
	receivers, rErr := bot.store.TopReceivers(start, end, limit)

	if gErr != nil || rErr != nil {
		bot.api.PostEphemeral(cmd.ChannelID, cmd.UserID, slack.MsgOptionText("Error generating stats.", false))
		return
	}

	var b strings.Builder
	b.WriteString(fmt.Sprintf("*Beer Stats* (last %d days)\n", days))
	b.WriteString("*Top Givers:*\n")
	if len(givers) == 0 {
		b.WriteString("(none)\n")
	} else {
		for i, row := range givers {
			b.WriteString(fmt.Sprintf("%d. <@%s> — %s\n", i+1, row[0], row[1]))
		}
	}
	b.WriteString("*Top Receivers:*\n")
	if len(receivers) == 0 {
		b.WriteString("(none)\n")
	} else {
		for i, row := range receivers {
			b.WriteString(fmt.Sprintf("%d. <@%s> — %s\n", i+1, row[0], row[1]))
		}
	}
	bot.api.PostEphemeral(cmd.ChannelID, cmd.UserID, slack.MsgOptionText(b.String(), false))
}

// TestConnection verifies the Slack connection and bot info
func (bot *MinimalSlackBot) TestConnection() error {
	authTest, err := bot.api.AuthTest()
	if err != nil {
		return fmt.Errorf("auth test failed: %w", err)
	}

	bot.logger.Info().
		Str("bot_id", authTest.BotID).
		Str("user_id", authTest.UserID).
		Str("team", authTest.Team).
		Msg("Slack connection verified")

	return nil
}
