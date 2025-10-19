package main

import (
	"database/sql"
	"os"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

func TestGetAllGiversAndRecipients(t *testing.T) {
	// create temp file for sqlite
	f, err := os.CreateTemp("", "test-beer-*.db")
	if err != nil {
		t.Fatalf("temp file: %v", err)
	}
	path := f.Name()
	f.Close()
	defer os.Remove(path)

	db, err := sql.Open("sqlite3", path+"?_foreign_keys=1")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	store, err := NewSQLiteStore(db)
	if err != nil {
		t.Fatalf("new store: %v", err)
	}

	// insert some beers
	now := time.Now()
	if err := store.AddBeer("giver1", "recipientA", "1000.1", now, 1); err != nil {
		t.Fatalf("addbeer: %v", err)
	}
	if err := store.AddBeer("giver2", "recipientA", "1000.2", now, 2); err != nil {
		t.Fatalf("addbeer: %v", err)
	}
	// duplicate giver1 to another recipient
	if err := store.AddBeer("giver1", "recipientB", "1000.3", now, 1); err != nil {
		t.Fatalf("addbeer: %v", err)
	}

	givers, err := store.GetAllGivers()
	if err != nil {
		t.Fatalf("get all givers: %v", err)
	}
	// expect giver1 and giver2
	found := map[string]bool{}
	for _, g := range givers {
		found[g] = true
	}
	if !found["giver1"] || !found["giver2"] {
		t.Fatalf("unexpected givers list: %v", givers)
	}

	recipients, err := store.GetAllRecipients()
	if err != nil {
		t.Fatalf("get all recipients: %v", err)
	}
	foundR := map[string]bool{}
	for _, r := range recipients {
		foundR[r] = true
	}
	if !foundR["recipientA"] || !foundR["recipientB"] {
		t.Fatalf("unexpected recipients list: %v", recipients)
	}
}
