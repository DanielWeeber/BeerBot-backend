package main

import (
	"fmt"
	"testing"
	"time"
)

func TestParseSlackTS_Valid(t *testing.T) {
	now := time.Now().Unix()
	ts := fmt.Sprintf("%d.123456", now)
	parsed := parseSlackTS(ts)
	if parsed.Unix() != now {
		t.Fatalf("expected %d got %d", now, parsed.Unix())
	}
}

func TestParseSlackTS_Invalid(t *testing.T) {
	parsed := parseSlackTS("not-a-ts")
	// Should return a time close to now (within 2s)
	if time.Since(parsed) > 2*time.Second {
		t.Fatalf("expected recent time, got %v", parsed)
	}
}

func TestExtractQuantity_Number(t *testing.T) {
	bot := &MinimalSlackBot{}
	q := bot.extractQuantity("I give 5 beers to <@U123>")
	if q != 5 {
		t.Fatalf("expected 5 got %d", q)
	}
}

func TestExtractQuantity_EmojiCount(t *testing.T) {
	bot := &MinimalSlackBot{}
	q := bot.extractQuantity("🍺🍺 <@U123>")
	if q != 2 {
		t.Fatalf("expected 2 got %d", q)
	}
}

func TestExtractQuantity_Default(t *testing.T) {
	bot := &MinimalSlackBot{}
	q := bot.extractQuantity("beer <@U123>")
	if q != 1 {
		t.Fatalf("expected 1 got %d", q)
	}
}
