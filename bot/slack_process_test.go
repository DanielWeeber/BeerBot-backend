package main

import (
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/slack-go/slack/slackevents"
)

// mockStore implements Store for testing processBeerGiving logic
type mockStore struct{ outcomes []string }

func (m *mockStore) CountGivenInDateRange(user string, start, end time.Time) (int, error) {
	return 0, nil
}
func (m *mockStore) CountReceivedInDateRange(user string, start, end time.Time) (int, error) {
	return 0, nil
}
func (m *mockStore) CountGivenOnDate(user string, date string) (int, error) { return 0, nil }
func (m *mockStore) GetAllGivers() ([]string, error)                        { return nil, nil }
func (m *mockStore) GetAllRecipients() ([]string, error)                    { return nil, nil }
func (m *mockStore) TryMarkEventProcessed(eventID string, t time.Time) (bool, error) {
	// Always indicate not processed so self_gift path reachable
	return false, nil
}
func (m *mockStore) AddBeer(giver string, recipient string, ts string, eventTime time.Time, count int) error {
	return nil
}
func (m *mockStore) RecordBeerEventOutcome(eventID, giverID, recipientID string, quantity int, status string, t time.Time) error {
	m.outcomes = append(m.outcomes, status)
	return nil
}
func (m *mockStore) TopGivers(start, end time.Time, limit int) ([][2]string, error) { return nil, nil }
func (m *mockStore) TopReceivers(start, end time.Time, limit int) ([][2]string, error) {
	return nil, nil
}

func TestProcessBeerGiving_SelfGift(t *testing.T) {
	ms := &mockStore{}
	eventCounter := prometheus.NewCounterVec(prometheus.CounterOpts{Name: "test_events", Help: ""}, []string{"type", "status"})
	errorCounter := prometheus.NewCounterVec(prometheus.CounterOpts{Name: "test_errors", Help: ""}, []string{"type"})
	// Provide bot without Slack client; use empty channel so postEphemeral is skipped (avoids nil deref)
	bot := &MinimalSlackBot{store: ms, maxGift: 10, eventCounter: eventCounter, errorCounter: errorCounter}
	// message giving beer to self should trigger self_gift outcome; ensure pattern matches isBeerGiving
	ev := &slackevents.MessageEvent{Text: "🍺 <@USELF>", User: "USELF", Channel: "", EventTimeStamp: "1717691574.000000"}
	if !bot.isBeerGiving(ev.Text) {
		t.Fatalf("test precondition failed: text not recognized as beer giving")
	}
	// Call logic directly; ignore ephemeral post errors (stub client)
	bot.processBeerGiving(ev)
	found := false
	for _, status := range ms.outcomes {
		if status == "self_gift" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected self_gift status recorded, outcomes=%v", ms.outcomes)
	}
}
