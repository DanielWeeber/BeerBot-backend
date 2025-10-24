package main

import (
    "database/sql"
    "os"
    "testing"
    "time"
    "fmt"

    _ "modernc.org/sqlite"
)

func TestSQLiteStore_IncGet(t *testing.T) {
    dbPath := "./testdata/test.db"
    _ = os.Remove(dbPath)
    if err := os.MkdirAll("./testdata", 0o755); err != nil {
        t.Fatalf("mkdir testdata: %v", err)
    }
    db, err := sql.Open("sqlite", dbPath+"?_foreign_keys=1")
    if err != nil {
        t.Fatalf("open db: %v", err)
    }
    defer func() {
        db.Close()
        _ = os.Remove(dbPath)
    }()

    s, err := NewSQLiteStore(db)
    if err != nil {
        t.Fatalf("new store: %v", err)
    }

    user := "U123"
    emoji := "beer"

    if c, _ := s.GetCount(user, emoji); c != 0 {
        t.Fatalf("expected 0, got %d", c)
    }

    if err := s.IncEmoji(user, emoji); err != nil {
        t.Fatalf("inc1: %v", err)
    }
    if c, _ := s.GetCount(user, emoji); c != 1 {
        t.Fatalf("expected 1, got %d", c)
    }

    if err := s.IncEmoji(user, emoji); err != nil {
        t.Fatalf("inc2: %v", err)
    }
    if c, _ := s.GetCount(user, emoji); c != 2 {
        t.Fatalf("expected 2, got %d", c)
    }
}

func TestSQLiteStore_Beers(t *testing.T) {
    dbPath := "./testdata/test_beers.db"
    _ = os.Remove(dbPath)
    if err := os.MkdirAll("./testdata", 0o755); err != nil {
        t.Fatalf("mkdir testdata: %v", err)
    }
    db, err := sql.Open("sqlite", dbPath+"?_foreign_keys=1")
    if err != nil { t.Fatalf("open db: %v", err) }
    defer func(){ db.Close(); _ = os.Remove(dbPath) }()

    s, err := NewSQLiteStore(db)
    if err != nil { t.Fatalf("new store: %v", err) }

    giver := "U1"
    recv := "U2"
    now := time.Now().UTC()
    ts1 := fmt.Sprintf("%d.000000", now.Unix())
    ts2 := fmt.Sprintf("%d.000000", now.Add(time.Second).Unix())

    // simulate two separate message events each giving 1 beer
    if err := s.AddBeer(giver, recv, ts1, now, 1); err != nil { t.Fatalf("addbeer: %v", err) }
    if err := s.AddBeer(giver, recv, ts2, now.Add(time.Second), 1); err != nil { t.Fatalf("addbeer2: %v", err) }

    date := now.UTC().Format("2006-01-02")
    g, err := s.CountGivenOnDate(giver, date)
    if err != nil { t.Fatalf("count given: %v", err) }
    if g != 2 { t.Fatalf("expected 2 given, got %d", g) }

    r, err := s.CountReceived(recv, "")
    if err != nil { t.Fatalf("count recv: %v", err) }
    if r != 2 { t.Fatalf("expected 2 received, got %d", r) }
}
