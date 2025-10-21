package main

import (
    "net/http"
    "strconv"
    "time"

    "github.com/prometheus/client_golang/prometheus"
)

var (
    messagesProcessed = prometheus.NewCounterVec(
        prometheus.CounterOpts{
            Name: "bwm_messages_processed_total",
            Help: "Number of Slack messages processed by the bot",
        },
        []string{"channel"},
    )

    httpRequestsTotal = prometheus.NewCounterVec(
        prometheus.CounterOpts{
            Name: "http_requests_total",
            Help: "Total number of HTTP requests",
        },
        []string{"path", "method", "status"},
    )

    httpRequestDuration = prometheus.NewHistogramVec(
        prometheus.HistogramOpts{
            Name:    "http_request_duration_seconds",
            Help:    "Duration of HTTP requests in seconds",
            Buckets: prometheus.DefBuckets,
        },
        []string{"path", "method", "status"},
    )

    slackReconnectsTotal = prometheus.NewCounter(
        prometheus.CounterOpts{
            Name: "slack_reconnects_total",
            Help: "Total number of Slack reconnect attempts",
        },
    )

    slackConnected = prometheus.NewGauge(
        prometheus.GaugeOpts{
            Name: "slack_connected",
            Help: "Slack connection state (1=connected, 0=disconnected)",
        },
    )

    beerMessageOutcomes = prometheus.NewCounterVec(
        prometheus.CounterOpts{
            Name: "beer_message_outcomes_total",
            Help: "Outcomes of Slack beer message processing",
        },
        []string{"channel", "reason"},
    )
)

// InitMetrics registers all collectors. Call once at startup.
func InitMetrics() {
    prometheus.MustRegister(messagesProcessed)
    prometheus.MustRegister(httpRequestsTotal)
    prometheus.MustRegister(httpRequestDuration)
    prometheus.MustRegister(slackReconnectsTotal)
    prometheus.MustRegister(slackConnected)
    prometheus.MustRegister(beerMessageOutcomes)
}

// IncMessagesProcessed increments the processed message counter for a channel.
func IncMessagesProcessed(channel string) {
    messagesProcessed.WithLabelValues(channel).Inc()
}

// ObserveHTTPRequest records an HTTP request count and duration.
func ObserveHTTPRequest(path, method string, status int, started time.Time) {
    code := strconv.Itoa(status)
    httpRequestsTotal.WithLabelValues(path, method, code).Inc()
    httpRequestDuration.WithLabelValues(path, method, code).Observe(time.Since(started).Seconds())
}

// IncSlackReconnect increments reconnect counter.
func IncSlackReconnect() { slackReconnectsTotal.Inc() }

// SetSlackConnected sets the Slack connection gauge.
func SetSlackConnected(connected bool) {
    if connected {
        slackConnected.Set(1)
    } else {
        slackConnected.Set(0)
    }
}

// IncBeerOutcome increments the outcome counter with a reason.
func IncBeerOutcome(channel, reason string) {
    beerMessageOutcomes.WithLabelValues(channel, reason).Inc()
}

// statusRecorder helps capture HTTP status codes for metrics.
type statusRecorder struct {
    http.ResponseWriter
    status int
}

func (sr *statusRecorder) WriteHeader(code int) {
    sr.status = code
    sr.ResponseWriter.WriteHeader(code)
}
