package storage

import (
	"database/sql"
	"fmt"
	"strings"
	"time"

	"poker-game-analyzer/pkg/table"
)

// The event log and the cursors that read it.
//
// Statistics used to be counted in memory as hands finished and written out as
// percentages. Two things follow from that, and both were live defects. The
// counters started empty on every restart and the write was an upsert of the
// whole row, so a player with fifty recorded hands who played one more came
// back with a hands count of one -- which is why a long session produced reads
// over five and eighteen hands. And a percentage cannot be re-derived: asking
// this data how often someone folds to a continuation bet is asking a question
// the aggregate was never built to answer, and there was nothing underneath it
// to ask instead.
//
// So: events are written once and never changed, and everything else is derived
// from them by a cursor that can be reset and replayed. A statistic nobody has
// thought of yet costs one new cursor.

const eventSchema = `
CREATE TABLE IF NOT EXISTS hand_events (
	id          INTEGER PRIMARY KEY AUTOINCREMENT,
	session_id  TEXT NOT NULL,
	table_key   TEXT NOT NULL,
	table_id    TEXT NOT NULL,
	hand_id     TEXT NOT NULL,
	seq         INTEGER NOT NULL,
	at_ms       INTEGER NOT NULL,
	kind        TEXT NOT NULL,
	street      TEXT NOT NULL,
	player_id   TEXT NOT NULL,
	player_name TEXT NOT NULL DEFAULT '',
	position    TEXT NOT NULL DEFAULT '',
	action      TEXT NOT NULL DEFAULT '',
	amount      INTEGER NOT NULL DEFAULT 0,
	pot_before  INTEGER NOT NULL DEFAULT 0,
	to_call     INTEGER NOT NULL DEFAULT 0,
	cards       TEXT NOT NULL DEFAULT '',
	board       TEXT NOT NULL DEFAULT '',
	UNIQUE (hand_id, seq)
);
CREATE INDEX IF NOT EXISTS idx_hand_events_table ON hand_events(table_key, player_id);
CREATE INDEX IF NOT EXISTS idx_hand_events_hand ON hand_events(hand_id, seq);

CREATE TABLE IF NOT EXISTS cursors (
	name       TEXT PRIMARY KEY,
	last_id    INTEGER NOT NULL DEFAULT 0,
	updated_at INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS player_counters (
	table_key  TEXT NOT NULL,
	player_id  TEXT NOT NULL,
	counter    TEXT NOT NULL,
	value      INTEGER NOT NULL DEFAULT 0,
	PRIMARY KEY (table_key, player_id, counter)
);
`

// MigrateEvents creates the event log, the cursor table and the derived
// counters. Separate from the original migration so the two can be reasoned
// about apart: everything here is derivable and can be dropped and rebuilt,
// and nothing in the older schema can.
func (s *SQLiteDB) MigrateEvents() error {
	for _, q := range strings.Split(eventSchema, ";") {
		q = strings.TrimSpace(q)
		if q == "" {
			continue
		}
		if _, err := s.db.Exec(q); err != nil {
			return fmt.Errorf("event schema (%.60s): %w", q, err)
		}
	}
	return nil
}

func cardsToText(cs []table.Card) string {
	if len(cs) == 0 {
		return ""
	}
	parts := make([]string, 0, len(cs))
	for _, c := range cs {
		parts = append(parts, c.String())
	}
	return strings.Join(parts, " ")
}

func cardsFromText(s string) []table.Card {
	if s == "" {
		return nil
	}
	fields := strings.Fields(s)
	out := make([]table.Card, 0, len(fields))
	for _, f := range fields {
		c, err := table.ParseCard(f)
		if err != nil {
			// An unread card was written as "??" and comes back as the zero
			// card, which is what it means.
			out = append(out, table.Card{})
			continue
		}
		out = append(out, c)
	}
	return out
}

// AppendEvents writes a batch in one transaction.
//
// Re-writing an event is a no-op rather than a duplicate: (hand_id, seq) is
// unique, so a retry after a partial failure, or a frame replayed twice, cannot
// count anything twice. That property is what lets the writer retry freely.
func (s *SQLiteDB) AppendEvents(events []table.HandEvent) error {
	if len(events) == 0 {
		return nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	stmt, err := tx.Prepare(`INSERT OR IGNORE INTO hand_events
		(session_id, table_key, table_id, hand_id, seq, at_ms, kind, street,
		 player_id, player_name, position, action, amount, pot_before, to_call, cards, board)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	for _, e := range events {
		at := e.At
		if at.IsZero() {
			at = time.Now()
		}
		if _, err := stmt.Exec(
			e.SessionID, e.TableKey, e.TableID, e.HandID, e.Seq,
			at.UnixMilli(), string(e.Kind), string(e.Street),
			e.PlayerID, e.PlayerName, string(e.Position), string(e.Action),
			int64(e.Amount), int64(e.PotBefore), int64(e.ToCall),
			cardsToText(e.Cards), cardsToText(e.Board),
		); err != nil {
			return fmt.Errorf("appending event %s/%d: %w", e.HandID, e.Seq, err)
		}
	}
	return tx.Commit()
}

// ReadEventsAfter returns up to limit events with an id above after, in order.
// This is how every cursor reads: the id is dense enough to resume from and
// monotonic, so "where did I get to" is a single number.
func (s *SQLiteDB) ReadEventsAfter(after int64, limit int) ([]table.HandEvent, error) {
	if limit <= 0 {
		limit = 500
	}
	rows, err := s.db.Query(`SELECT id, session_id, table_key, table_id, hand_id, seq, at_ms,
		kind, street, player_id, player_name, position, action, amount, pot_before, to_call, cards, board
		FROM hand_events WHERE id > ? ORDER BY id LIMIT ?`, after, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []table.HandEvent
	for rows.Next() {
		var e table.HandEvent
		var atMs int64
		var kind, street, position, action, cards, board string
		if err := rows.Scan(&e.ID, &e.SessionID, &e.TableKey, &e.TableID, &e.HandID, &e.Seq, &atMs,
			&kind, &street, &e.PlayerID, &e.PlayerName, &position, &action,
			&e.Amount, &e.PotBefore, &e.ToCall, &cards, &board); err != nil {
			return nil, err
		}
		e.At = time.UnixMilli(atMs)
		e.Kind = table.EventKind(kind)
		e.Street = table.Street(street)
		e.Position = table.Position(position)
		e.Action = table.ActionType(action)
		e.Cards = cardsFromText(cards)
		e.Board = cardsFromText(board)
		out = append(out, e)
	}
	return out, rows.Err()
}

// CursorAt reports where a named consumer has read to. An unknown cursor starts
// at the beginning, which is what makes adding one a matter of naming it.
func (s *SQLiteDB) CursorAt(name string) (int64, error) {
	var at int64
	err := s.db.QueryRow(`SELECT last_id FROM cursors WHERE name = ?`, name).Scan(&at)
	if err == sql.ErrNoRows {
		return 0, nil
	}
	return at, err
}

// AdvanceCursor moves a cursor and applies the state it produced, in one
// transaction.
//
// The two have to move together. Applied without advancing, a restart counts
// the same events again; advanced without applying, it skips them. Since the
// counter update is an addition, the only way to make that safe is to make the
// two atomic -- which is why this takes the deltas rather than exposing the
// transaction and trusting the caller to use it.
func (s *SQLiteDB) AdvanceCursor(name string, to int64, deltas []CounterDelta) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	if len(deltas) > 0 {
		stmt, err := tx.Prepare(`INSERT INTO player_counters (table_key, player_id, counter, value)
			VALUES (?,?,?,?)
			ON CONFLICT(table_key, player_id, counter) DO UPDATE SET value = value + excluded.value`)
		if err != nil {
			return err
		}
		defer stmt.Close()
		for _, d := range deltas {
			if _, err := stmt.Exec(d.TableKey, d.PlayerID, d.Counter, d.Value); err != nil {
				return fmt.Errorf("counter %s/%s/%s: %w", d.TableKey, d.PlayerID, d.Counter, err)
			}
		}
	}

	if _, err := tx.Exec(`INSERT INTO cursors (name, last_id, updated_at) VALUES (?,?,?)
		ON CONFLICT(name) DO UPDATE SET last_id = excluded.last_id, updated_at = excluded.updated_at`,
		name, to, time.Now().UnixMilli()); err != nil {
		return err
	}
	return tx.Commit()
}

// CounterDelta is one addition to one counter. Counters are counted, never
// averaged: a percentage stored is a percentage whose denominator is gone, and
// 66% over three hands then reads exactly like 66% over three hundred.
type CounterDelta struct {
	TableKey string
	PlayerID string
	Counter  string
	Value    int64
}

// ResetCursor puts a consumer back to the beginning and clears what it built,
// which is how a new statistic gets its history: name it, reset, replay.
func (s *SQLiteDB) ResetCursor(name string) error {
	_, err := s.db.Exec(`DELETE FROM cursors WHERE name = ?`, name)
	return err
}

// PlayerCounters returns every counter held for one player at one table.
func (s *SQLiteDB) PlayerCounters(tableKey, playerID string) (map[string]int64, error) {
	rows, err := s.db.Query(`SELECT counter, value FROM player_counters
		WHERE table_key = ? AND player_id = ?`, tableKey, playerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := map[string]int64{}
	for rows.Next() {
		var k string
		var v int64
		if err := rows.Scan(&k, &v); err != nil {
			return nil, err
		}
		out[k] = v
	}
	return out, rows.Err()
}

// TableCounters returns the counters for every player seen at a table, which is
// what a warm-up display needs: who has been observed, and how much.
func (s *SQLiteDB) TableCounters(tableKey string) (map[string]map[string]int64, error) {
	rows, err := s.db.Query(`SELECT player_id, counter, value FROM player_counters
		WHERE table_key = ?`, tableKey)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := map[string]map[string]int64{}
	for rows.Next() {
		var p, k string
		var v int64
		if err := rows.Scan(&p, &k, &v); err != nil {
			return nil, err
		}
		if out[p] == nil {
			out[p] = map[string]int64{}
		}
		out[p][k] = v
	}
	return out, rows.Err()
}

// EventCount is the number of events held, for retention and for tests.
func (s *SQLiteDB) EventCount() (int64, error) {
	var n int64
	err := s.db.QueryRow(`SELECT COUNT(*) FROM hand_events`).Scan(&n)
	return n, err
}
