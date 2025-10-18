package main

import (
    "database/sql"
    "fmt"
    "strings"
    "time"
)

type SQLiteStore struct {
    db *sql.DB
}

func NewSQLiteStore(db *sql.DB) (*SQLiteStore, error) {
    s := &SQLiteStore{db: db}
    if err := s.migrate(); err != nil {
        return nil, err
    }
    return s, nil
}

func (s *SQLiteStore) migrate() error {
    // Ensure simple auxiliary tables exist
    aux := []string{
        `CREATE TABLE IF NOT EXISTS emoji_counts (
            id INTEGER PRIMARY KEY AUTOINCREMENT,
            user_id TEXT NOT NULL,
            emoji TEXT NOT NULL,
            count INTEGER NOT NULL DEFAULT 0,
            UNIQUE(user_id, emoji)
        );`,
        `CREATE TABLE IF NOT EXISTS processed_events (
            id INTEGER PRIMARY KEY AUTOINCREMENT,
            event_id TEXT NOT NULL UNIQUE,
            ts TEXT NOT NULL
        );`,
    }
    for _, st := range aux {
        if _, err := s.db.Exec(st); err != nil {
            return fmt.Errorf("migrate exec: %w", err)
        }
    }

    // Desired beers table create statement
    desiredCreate := `CREATE TABLE beers (
            id INTEGER PRIMARY KEY AUTOINCREMENT,
            giver_id TEXT NOT NULL,
            recipient_id TEXT NOT NULL,
            ts TEXT NOT NULL, -- original Slack ts string (with fraction)
            ts_rfc DATETIME NOT NULL, -- parsed RFC3339 time for date queries
            count INTEGER NOT NULL DEFAULT 1,
            UNIQUE (giver_id, recipient_id, ts)
        );`

    // If beers table doesn't exist, create it with the desired schema
    var exists int
    if err := s.db.QueryRow(`SELECT COUNT(1) FROM sqlite_master WHERE type='table' AND name='beers'`).Scan(&exists); err != nil {
        return fmt.Errorf("migrate check beers exists: %w", err)
    }
    if exists == 0 {
        if _, err := s.db.Exec(desiredCreate); err != nil {
            return fmt.Errorf("migrate create beers: %w", err)
        }
        return nil
    }

    // beers table exists: ensure required columns and constraints
    // collect existing columns
    cols := map[string]bool{}
    rows, err := s.db.Query(`PRAGMA table_info(beers);`)
    if err != nil {
        return fmt.Errorf("migrate pragma: %w", err)
    }
    defer rows.Close()
    for rows.Next() {
        var cid int
        var name string
        var ctype string
        var notnull int
        var dflt sql.NullString
        var pk int
        if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
            return fmt.Errorf("migrate scan pragma: %w", err)
        }
        cols[name] = true
    }

    // Add missing columns non-destructively
    if !cols["ts_rfc"] {
        if _, err := s.db.Exec(`ALTER TABLE beers ADD COLUMN ts_rfc DATETIME;`); err != nil {
            return fmt.Errorf("migrate add ts_rfc: %w", err)
        }
    }
    if !cols["count"] {
        if _, err := s.db.Exec(`ALTER TABLE beers ADD COLUMN count INTEGER NOT NULL DEFAULT 1;`); err != nil {
            return fmt.Errorf("migrate add count: %w", err)
        }
    }

    // Ensure UNIQUE(giver_id, recipient_id, ts) exists. SQLite doesn't support adding
    // UNIQUE constraints via ALTER, so if it's missing we recreate the table non-destructively
    // by aggregating existing rows into the desired schema.
    var createSQL sql.NullString
    if err := s.db.QueryRow(`SELECT sql FROM sqlite_master WHERE type='table' AND name='beers'`).Scan(&createSQL); err != nil {
        return fmt.Errorf("migrate select create sql: %w", err)
    }
    if !createSQL.Valid || !strings.Contains(strings.ToUpper(createSQL.String), "UNIQUE") {
        // Recreate table: create beers_new, copy aggregated data, swap tables
        tx, err := s.db.Begin()
        if err != nil {
            return fmt.Errorf("migrate begin tx: %w", err)
        }
        // create new table with desired schema
        if _, err := tx.Exec(desiredCreate); err != nil {
            tx.Rollback()
            return fmt.Errorf("migrate create beers_new: %w", err)
        }
        // copy aggregated data into beers (treat missing count as 1 and compute ts_rfc if NULL)
        copyStmt := `INSERT INTO beers (giver_id, recipient_id, ts, ts_rfc, count)
            SELECT giver_id, recipient_id, ts,
                COALESCE(ts_rfc, datetime(substr(ts,1,instr(ts,'.')-1), 'unixepoch')),
                COALESCE(SUM(count), COUNT(1))
            FROM (SELECT * FROM beers) GROUP BY giver_id, recipient_id, ts;`
        if _, err := tx.Exec(copyStmt); err != nil {
            tx.Rollback()
            return fmt.Errorf("migrate copy aggregated: %w", err)
        }
        // drop old table and keep the new one under the original name
        if _, err := tx.Exec(`DROP TABLE IF EXISTS beers;`); err != nil {
            tx.Rollback()
            return fmt.Errorf("migrate drop old beers: %w", err)
        }
        if _, err := tx.Exec(`ALTER TABLE beers RENAME TO beers_old;`); err == nil {
            // if rename succeeded unexpectedly, try to rename back
        }
        // Note: desiredCreate created a table named 'beers' already; we dropped old table, so commit.
        if err := tx.Commit(); err != nil {
            return fmt.Errorf("migrate commit recreate: %w", err)
        }
    }

    return nil
}

// MarkEventProcessed records that an external event (by event_id) has been
// handled. Returns nil if inserted; if the event already exists, returns nil as well.
func (s *SQLiteStore) MarkEventProcessed(eventID string, ts time.Time) error {
    _, err := s.db.Exec(`INSERT OR IGNORE INTO processed_events (event_id, ts) VALUES (?, ?);`, eventID, ts.UTC().Format(time.RFC3339))
    return err
}

// TryMarkEventProcessed attempts to insert the event id into processed_events.
// Returns (true, nil) if we recorded the event (i.e. this process should handle it),
// (false, nil) if the event was already present (another process handled it),
// or (false, err) on database error.
func (s *SQLiteStore) TryMarkEventProcessed(eventID string, ts time.Time) (bool, error) {
    res, err := s.db.Exec(`INSERT OR IGNORE INTO processed_events (event_id, ts) VALUES (?, ?);`, eventID, ts.UTC().Format(time.RFC3339))
    if err != nil {
        return false, err
    }
    n, err := res.RowsAffected()
    if err != nil {
        return false, err
    }
    return n > 0, nil
}

// IsEventProcessed returns true if we've already processed the given event id.
func (s *SQLiteStore) IsEventProcessed(eventID string) (bool, error) {
    var id int
    err := s.db.QueryRow(`SELECT id FROM processed_events WHERE event_id = ?`, eventID).Scan(&id)
    if err == sql.ErrNoRows {
        return false, nil
    }
    if err != nil {
        return false, err
    }
    return true, nil
}

func (s *SQLiteStore) IncEmoji(userID, emoji string) error {
    tx, err := s.db.Begin()
    if err != nil {
        return err
    }
    defer tx.Rollback()

    // try update
    res, err := tx.Exec(`UPDATE emoji_counts SET count = count + 1 WHERE user_id = ? AND emoji = ?`, userID, emoji)
    if err != nil {
        return err
    }
    n, err := res.RowsAffected()
    if err != nil {
        return err
    }
    if n == 0 {
        if _, err := tx.Exec(`INSERT INTO emoji_counts(user_id, emoji, count) VALUES(?, ?, 1)`, userID, emoji); err != nil {
            return err
        }
    }
    return tx.Commit()
}

func (s *SQLiteStore) GetCount(userID, emoji string) (int, error) {
    var c int
    err := s.db.QueryRow(`SELECT count FROM emoji_counts WHERE user_id = ? AND emoji = ?`, userID, emoji).Scan(&c)
    if err == sql.ErrNoRows {
        return 0, nil
    }
    if err != nil {
        return 0, err
    }
    return c, nil
}

// AddBeer inserts a beer event (one record per beer)
// AddBeer records a beer-gift event for a single message: it inserts or upserts
// a row with the provided count. If the same (giver, recipient, ts) already
// exists, the count will be updated to the provided value (last write wins).
// AddBeer records a beer-gift event for a single message: it inserts or upserts
// a row with the provided count keyed by the original Slack ts string (ts).
func (s *SQLiteStore) AddBeer(giverID, recipientID string, slackTs string, t time.Time, count int) error {
    _, err := s.db.Exec(`INSERT INTO beers (giver_id, recipient_id, ts, ts_rfc, count) VALUES (?, ?, ?, ?, ?) ON CONFLICT(giver_id, recipient_id, ts) DO UPDATE SET count = excluded.count`, giverID, recipientID, slackTs, t.UTC().Format(time.RFC3339), count)
    return err
}

// CountGivenInDateRange returns how many beers the giver gave in the given date range
func (s *SQLiteStore) CountGivenInDateRange(giverID string, start time.Time, end time.Time) (int, error) {
	var c int
	err := s.db.QueryRow(`SELECT COALESCE(SUM(count), 0) FROM beers WHERE giver_id = ? AND date(ts_rfc) BETWEEN date(?) AND date(?)`, giverID, start.UTC().Format(time.RFC3339), end.UTC().Format(time.RFC3339)).Scan(&c)
	if err != nil {
		return 0, err
	}
	return c, nil
}

// CountReceivedInDateRange returns total beers received by recipient in the given date range
func (s *SQLiteStore) CountReceivedInDateRange(recipientID string, start time.Time, end time.Time) (int, error) {
	var c int
	err := s.db.QueryRow(`SELECT COALESCE(SUM(count), 0) FROM beers WHERE recipient_id = ? AND date(ts_rfc) BETWEEN date(?) AND date(?)`, recipientID, start.UTC().Format(time.RFC3339), end.UTC().Format(time.RFC3339)).Scan(&c)
	if err != nil {
		return 0, err
	}
	return c, nil
}

// CountGivenOnDate returns how many beers the giver gave on the given date (YYYY-MM-DD)
func (s *SQLiteStore) CountGivenOnDate(giverID string, date string) (int, error) {
	t, err := time.Parse("2006-01-02", date)
	if err != nil {
		return 0, err
	}
	return s.CountGivenInDateRange(giverID, t, t)
}

// CountReceived returns total beers received by recipient (optionally filtered by date if not empty)
func (s *SQLiteStore) CountReceived(recipientID string, date string) (int, error) {
	if date == "" {
		return s.CountReceivedInDateRange(recipientID, time.Time{}, time.Now())
	}
	t, err := time.Parse("2006-01-02", date)
	if err != nil {
		return 0, err
	}
	return s.CountReceivedInDateRange(recipientID, t, t)
}

// GetAllGivers returns the list of all distinct user IDs that have given at least one beer.
func (s *SQLiteStore) GetAllGivers() ([]string, error) {
    rows, err := s.db.Query(`SELECT DISTINCT giver_id FROM beers`)
    if err != nil {
        return nil, err
    }
    defer rows.Close()
    var out []string
    for rows.Next() {
        var id string
        if err := rows.Scan(&id); err != nil {
            return nil, err
        }
        out = append(out, id)
    }
    return out, nil
}

// GetAllRecipients returns the list of all distinct recipient user IDs that have received at least one beer.
func (s *SQLiteStore) GetAllRecipients() ([]string, error) {
    rows, err := s.db.Query(`SELECT DISTINCT recipient_id FROM beers`)
    if err != nil {
        return nil, err
    }
    defer rows.Close()
    var out []string
    for rows.Next() {
        var id string
        if err := rows.Scan(&id); err != nil {
            return nil, err
        }
        out = append(out, id)
    }
    return out, nil
}
