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
	traceEvents  bool
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
	traceEvents := strings.EqualFold(os.Getenv("TRACE_EVENTS"), "true") || os.Getenv("TRACE_EVENTS") == "1"

	return &MinimalSlackBot{
		api:          api,
		client:       client,
		logger:       logger,
		store:        store,
		eventCounter: eventCounter,
		errorCounter: errorCounter,
		maxGift:      maxGift,
		readOnly:     readOnly,
		traceEvents:  traceEvents,
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
	bot.logger.Info().Msg("Event handler loop started - waiting for Socket Mode events...")
	for event := range bot.client.Events {
		bot.logger.Debug().Msg(">>> NEW EVENT FROM SOCKET MODE CHANNEL <<<")
		bot.processEvent(event)
	}
	bot.logger.Warn().Msg("Event handler loop ended - Socket Mode channel closed")
}

// processEvent handles individual Slack events
func (bot *MinimalSlackBot) processEvent(evt socketmode.Event) {
	// RAW EVENT LOG: Log every incoming socket event before any processing
	envelopeID := ""
	if evt.Request != nil {
		envelopeID = evt.Request.EnvelopeID
	}
	bot.logger.Debug().
		Str("socket_event_type", string(evt.Type)).
		Interface("event_data", evt.Data).
		Bool("has_request", evt.Request != nil).
		Str("envelope_id", envelopeID).
		Msg("RAW SOCKET EVENT RECEIVED")

	// ACK only EventsAPI & Interaction events that carry a request envelope
	if (evt.Type == socketmode.EventTypeEventsAPI || evt.Type == socketmode.EventTypeSlashCommand) && evt.Request != nil {
		bot.client.Ack(*evt.Request)
	}

	if bot.traceEvents {
		bot.logger.Debug().Str("socket_event_type", string(evt.Type)).Msg("Received socket event")
	}

	switch evt.Type {
	case socketmode.EventTypeEventsAPI:
		eventsAPIEvent, ok := evt.Data.(slackevents.EventsAPIEvent)
		if !ok {
			bot.logger.Error().Msg("Failed to cast event to EventsAPIEvent")
			bot.errorCounter.WithLabelValues("cast_error").Inc()
			return
		}
		bot.handleEventsAPIEvent(eventsAPIEvent, envelopeID)
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
func (bot *MinimalSlackBot) handleEventsAPIEvent(event slackevents.EventsAPIEvent, envelopeID string) {
	// RAW EVENTSAPI LOG: Log the full event structure before any parsing
	bot.logger.Debug().
		Str("api_event_type", string(event.Type)).
		Interface("full_event", event).
		Str("envelope_id", envelopeID).
		Msg("RAW EVENTSAPI EVENT RECEIVED")

	if bot.traceEvents {
		bot.logger.Debug().Str("api_event_type", string(event.Type)).Msg("Processing EventsAPIEvent")
	}
	switch event.Type {
	case slackevents.CallbackEvent:
		innerEvent := event.InnerEvent
		bot.logger.Debug().
			Str("inner_type", innerEvent.Type).
			Interface("inner_data", innerEvent.Data).
			Msg("RAW CALLBACK INNER EVENT")
		if bot.traceEvents {
			bot.logger.Debug().Str("inner_type", innerEvent.Type).Msg("Inner callback event")
		}
		switch ev := innerEvent.Data.(type) {
		case *slackevents.MessageEvent:
			// Pass the envelope_id for deduplication
			bot.handleMessage(ev, envelopeID)
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
func (bot *MinimalSlackBot) handleMessage(event *slackevents.MessageEvent, envelopeID string) {
	// Skip bot messages, empty text, edits (subtypes), and thread replies (only handle top-level)
	if event.BotID != "" || event.Text == "" || event.SubType != "" {
		return
	}
	if event.ThreadTimeStamp != "" && event.ThreadTimeStamp != event.EventTimeStamp {
		return // ignore replies in threads
	}

	if bot.traceEvents {
		bot.logger.Debug().Str("channel", event.Channel).Str("user", event.User).Str("text", event.Text).Str("envelope_id", envelopeID).Msg("MessageEvent candidate")
	}

	bot.eventCounter.WithLabelValues("message", "received").Inc()

	if bot.isBeerGiving(event.Text) {
		bot.processBeerGiving(event, envelopeID)
	}
}

// isBeerGiving checks if the message is giving beer to someone
// compiledGiftPatterns contains a broadened set of regexes that indicate a beer gift intent.
// We support both emoji-first and mention-first ordering, optional "give" verbs, quantity numbers,
// and textual/emoji beer variants. Matching only signals intent; quantity extraction is handled separately.
// NOTE: Keep patterns simple to avoid catastrophic backtracking; prefer multiple explicit regexes.
var compiledGiftPatterns = []*regexp.Regexp{
	// Original forms: emoji/keyword before mention (with word boundaries for text)
	regexp.MustCompile(`🍺\s*<@[A-Z0-9]+>`),
	regexp.MustCompile(`🍻\s*<@[A-Z0-9]+>`),
	regexp.MustCompile(`:beer:\s*<@[A-Z0-9]+>`),
	regexp.MustCompile(`:beers:\s*<@[A-Z0-9]+>`),
	regexp.MustCompile(`(?i)\bbeer\s+<@[A-Z0-9]+>`),
	regexp.MustCompile(`(?i)\bbeers\s+<@[A-Z0-9]+>`),

	// Mention-first ordering with emojis or keywords immediately or with minimal spacing
	regexp.MustCompile(`<@[A-Z0-9]+>\s*🍺+`),          // one or many single beer emojis
	regexp.MustCompile(`<@[A-Z0-9]+>\s*🍻+`),          // one or many clinking beer emojis
	regexp.MustCompile(`<@[A-Z0-9]+>\s*:beer:`),      // textual beer emoji after mention
	regexp.MustCompile(`<@[A-Z0-9]+>\s*:beers:`),     // textual beers emoji after mention
	regexp.MustCompile(`(?i)<@[A-Z0-9]+>\s*beer\b`),  // mention then 'beer'
	regexp.MustCompile(`(?i)<@[A-Z0-9]+>\s*beers\b`), // mention then 'beers'

	// Give/gives/giving/gift phrasing before mention (emoji or keyword after optional quantity with word boundaries)
	regexp.MustCompile(`(?i)\bgive\s+<@[A-Z0-9]+>\s*(?:\d+\s*)?(?:🍺+|🍻+|:beer:|:beers:|\bbeer\b|\bbeers\b)`),
	regexp.MustCompile(`(?i)\bgives\s+<@[A-Z0-9]+>\s*(?:\d+\s*)?(?:🍺+|🍻+|:beer:|:beers:|\bbeer\b|\bbeers\b)`),
	regexp.MustCompile(`(?i)\bgiving\s+<@[A-Z0-9]+>\s*(?:\d+\s*)?(?:🍺+|🍻+|:beer:|:beers:|\bbeer\b|\bbeers\b)`),
	regexp.MustCompile(`(?i)\bgift\s+<@[A-Z0-9]+>\s*(?:\d+\s*)?(?:🍺+|🍻+|:beer:|:beers:|\bbeer\b|\bbeers\b)`),
	regexp.MustCompile(`(?i)\bgifting\s+<@[A-Z0-9]+>\s*(?:\d+\s*)?(?:🍺+|🍻+|:beer:|:beers:|\bbeer\b|\bbeers\b)`),

	// Verb after mention: <@U123> gives 3 beers / <@U123> give beer (with word boundaries)
	regexp.MustCompile(`<@[A-Z0-9]+>\s+(?i:gives?|giving|gift|gifting)\s*(?:\d+\s*)?(?:🍺+|🍻+|:beer:|:beers:|\bbeer\b|\bbeers\b)`),
}

func (bot *MinimalSlackBot) isBeerGiving(text string) bool {
	for _, rx := range compiledGiftPatterns {
		if rx.MatchString(text) {
			if bot.traceEvents {
				bot.logger.Debug().Str("pattern", rx.String()).Msg("Beer gift pattern matched")
			}
			return true
		}
	}
	return false
}

// processBeerGiving handles beer giving events
func (bot *MinimalSlackBot) processBeerGiving(event *slackevents.MessageEvent, envelopeID string) {
	// Use the Socket Mode envelope_id for deduplication, fallback to timestamp if not available
	dedupKey := envelopeID
	if dedupKey == "" {
		dedupKey = event.EventTimeStamp
		bot.logger.Warn().Str("timestamp", event.EventTimeStamp).Msg("No envelope_id available, falling back to timestamp for deduplication")
	}

	// Check for event deduplication
	eventTime := parseSlackTS(event.EventTimeStamp)
	isNewEvent, err := bot.store.TryMarkEventProcessed(dedupKey, eventTime)
	if err != nil {
		_ = bot.store.RecordBeerEventOutcome(dedupKey, event.User, "", 0, "error", eventTime)
		bot.logger.Error().
			Err(err).
			Str("envelope_id", envelopeID).
			Str("timestamp", event.EventTimeStamp).
			Msg("Error checking event deduplication")
		bot.errorCounter.WithLabelValues("dedup_error").Inc()
		return
	}
	if !isNewEvent {
		_ = bot.store.RecordBeerEventOutcome(dedupKey, event.User, "", 0, "duplicate", eventTime)
		bot.logger.Debug().
			Str("envelope_id", envelopeID).
			Str("timestamp", event.EventTimeStamp).
			Msg("Event already processed, skipping")
		bot.eventCounter.WithLabelValues("beer_giving", "duplicate").Inc()
		return
	}

	// Extract recipient user ID
	recipient := bot.extractRecipient(event.Text)
	if recipient == "" {
		_ = bot.store.RecordBeerEventOutcome(dedupKey, event.User, "", 0, "invalid_recipient", eventTime)
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
		_ = bot.store.RecordBeerEventOutcome(dedupKey, event.User, recipient, quantity, "self_gift", eventTime)
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
			_ = bot.store.RecordBeerEventOutcome(dedupKey, event.User, recipient, quantity, "error", eventTime)
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

	_ = bot.store.RecordBeerEventOutcome(dedupKey, event.User, recipient, quantity, "success", eventTime)
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
