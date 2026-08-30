package storage

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	stdpath "path/filepath"

	_ "modernc.org/sqlite"

	"poker-game-analyzer/pkg/table"
)

// PlayerStats represents aggregated statistical indicators for a player.
type PlayerStats struct {
	PlayerID   string  `json:"player_id"`
	PlayerName string  `json:"player_name"`
	HandsCount int     `json:"hands_count"`
	VPIP       float64 `json:"vpip"`
	PFR        float64 `json:"pfr"`
	ThreeBet   float64 `json:"three_bet"`
	AF         float64 `json:"af"`
}

// LLMProfile represents qualitative and exploitative profile data generated for a player.
type LLMProfile struct {
	PlayerID       string  `json:"player_id"`
	PlayerName     string  `json:"player_name"`
	Archetype      string  `json:"archetype"`
	BluffFrequency float64 `json:"bluff_frequency"`
	FoldTo3Bet     float64 `json:"fold_to_3bet"`
	FoldToCBet     float64 `json:"fold_to_cbet"`
	Exploits       string  `json:"exploits"`
	Notes          string  `json:"notes"`
}

// SQLiteDB provides persistent storage for poker analytics and histories.
type SQLiteDB struct {
	db *sql.DB
}

// NewSQLiteDB creates a new SQLiteDB instance and runs schema migrations.
func NewSQLiteDB(filepath string) (*SQLiteDB, error) {
	if filepath != ":memory:" {
		dir := stdpath.Dir(filepath)
		if dir != "." && dir != "" {
			if err := os.MkdirAll(dir, 0755); err != nil {
				return nil, fmt.Errorf("failed to create db directory %q: %w", dir, err)
			}
		}
	}

	db, err := sql.Open("sqlite", filepath)
	if err != nil {
		return nil, fmt.Errorf("failed to open sqlite database: %w", err)
	}

	// Set connection pragmas
	pragmas := []string{
		"PRAGMA foreign_keys = ON;",
		"PRAGMA busy_timeout = 5000;",
	}
	if filepath != ":memory:" {
		pragmas = append(pragmas, "PRAGMA journal_mode = WAL;")
	} else {
		db.SetMaxOpenConns(1)
	}

	for _, p := range pragmas {
		if _, err := db.Exec(p); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("failed to apply pragma %q: %w", p, err)
		}
	}

	s := &SQLiteDB{db: db}
	if err := s.migrate(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("failed to run migrations: %w", err)
	}

	return s, nil
}

// migrate creates the necessary database tables and indexes if they do not exist.
func (s *SQLiteDB) migrate() error {
	queries := []string{
		`CREATE TABLE IF NOT EXISTS player_stats (
			player_id TEXT PRIMARY KEY,
			player_name TEXT NOT NULL,
			hands_count INTEGER NOT NULL DEFAULT 0,
			vpip REAL NOT NULL DEFAULT 0.0,
			pfr REAL NOT NULL DEFAULT 0.0,
			three_bet REAL NOT NULL DEFAULT 0.0,
			af REAL NOT NULL DEFAULT 0.0,
			updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
		);`,
		`CREATE TABLE IF NOT EXISTS player_llm_profiles (
			player_id TEXT PRIMARY KEY,
			player_name TEXT NOT NULL,
			archetype TEXT NOT NULL,
			bluff_frequency REAL NOT NULL DEFAULT 0.0,
			fold_to_3bet REAL NOT NULL DEFAULT 0.0,
			fold_to_cbet REAL NOT NULL DEFAULT 0.0,
			exploits TEXT NOT NULL DEFAULT '',
			notes TEXT NOT NULL DEFAULT '',
			updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
		);`,
		`CREATE TABLE IF NOT EXISTS hand_histories (
			hand_id TEXT PRIMARY KEY,
			table_id TEXT NOT NULL,
			street TEXT NOT NULL,
			pot REAL NOT NULL,
			hero_id TEXT NOT NULL,
			state_json TEXT NOT NULL,
			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
		);`,
		`CREATE INDEX IF NOT EXISTS idx_hand_histories_table_id ON hand_histories(table_id);`,
	}

	for _, q := range queries {
		if _, err := s.db.Exec(q); err != nil {
			return fmt.Errorf("migration query failed (%s): %w", q, err)
		}
	}
	return nil
}

// Close closes the underlying SQLite database connection.
func (s *SQLiteDB) Close() error {
	return s.db.Close()
}

// SavePlayerStats inserts or updates statistical data for a player.
func (s *SQLiteDB) SavePlayerStats(p PlayerStats) error {
	query := `INSERT INTO player_stats (player_id, player_name, hands_count, vpip, pfr, three_bet, af, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP)
		ON CONFLICT(player_id) DO UPDATE SET
			player_name = excluded.player_name,
			hands_count = excluded.hands_count,
			vpip = excluded.vpip,
			pfr = excluded.pfr,
			three_bet = excluded.three_bet,
			af = excluded.af,
			updated_at = CURRENT_TIMESTAMP;`

	_, err := s.db.Exec(query, p.PlayerID, p.PlayerName, p.HandsCount, p.VPIP, p.PFR, p.ThreeBet, p.AF)
	if err != nil {
		return fmt.Errorf("failed to save player stats for %s: %w", p.PlayerID, err)
	}
	return nil
}

// GetPlayerStats retrieves stats for the given playerID. Returns nil, nil if player is not found.
func (s *SQLiteDB) GetPlayerStats(playerID string) (*PlayerStats, error) {
	query := `SELECT player_id, player_name, hands_count, vpip, pfr, three_bet, af
		FROM player_stats WHERE player_id = ?;`

	var p PlayerStats
	err := s.db.QueryRow(query, playerID).Scan(
		&p.PlayerID,
		&p.PlayerName,
		&p.HandsCount,
		&p.VPIP,
		&p.PFR,
		&p.ThreeBet,
		&p.AF,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to query player stats for %s: %w", playerID, err)
	}
	return &p, nil
}

// SaveLLMProfile inserts or updates LLM profile and exploit information for a player.
func (s *SQLiteDB) SaveLLMProfile(p LLMProfile) error {
	query := `INSERT INTO player_llm_profiles (player_id, player_name, archetype, bluff_frequency, fold_to_3bet, fold_to_cbet, exploits, notes, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP)
		ON CONFLICT(player_id) DO UPDATE SET
			player_name = excluded.player_name,
			archetype = excluded.archetype,
			bluff_frequency = excluded.bluff_frequency,
			fold_to_3bet = excluded.fold_to_3bet,
			fold_to_cbet = excluded.fold_to_cbet,
			exploits = excluded.exploits,
			notes = excluded.notes,
			updated_at = CURRENT_TIMESTAMP;`

	_, err := s.db.Exec(query, p.PlayerID, p.PlayerName, p.Archetype, p.BluffFrequency, p.FoldTo3Bet, p.FoldToCBet, p.Exploits, p.Notes)
	if err != nil {
		return fmt.Errorf("failed to save LLM profile for %s: %w", p.PlayerID, err)
	}
	return nil
}

// GetLLMProfile retrieves the LLM profile for the given playerID. Returns nil, nil if profile is not found.
func (s *SQLiteDB) GetLLMProfile(playerID string) (*LLMProfile, error) {
	query := `SELECT player_id, player_name, archetype, bluff_frequency, fold_to_3bet, fold_to_cbet, exploits, notes
		FROM player_llm_profiles WHERE player_id = ?;`

	var p LLMProfile
	err := s.db.QueryRow(query, playerID).Scan(
		&p.PlayerID,
		&p.PlayerName,
		&p.Archetype,
		&p.BluffFrequency,
		&p.FoldTo3Bet,
		&p.FoldToCBet,
		&p.Exploits,
		&p.Notes,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to query LLM profile for %s: %w", playerID, err)
	}
	return &p, nil
}

// SaveHandHistory serializes and saves a HandState snapshot to the hand_histories table.
func (s *SQLiteDB) SaveHandHistory(h table.HandState) error {
	data, err := json.Marshal(h)
	if err != nil {
		return fmt.Errorf("failed to marshal hand state for %s: %w", h.HandID, err)
	}

	query := `INSERT INTO hand_histories (hand_id, table_id, street, pot, hero_id, state_json, created_at)
		VALUES (?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP)
		ON CONFLICT(hand_id) DO UPDATE SET
			table_id = excluded.table_id,
			street = excluded.street,
			pot = excluded.pot,
			hero_id = excluded.hero_id,
			state_json = excluded.state_json,
			created_at = CURRENT_TIMESTAMP;`

	_, err = s.db.Exec(query, h.HandID, h.TableID, string(h.Street), h.Pot, h.HeroID, string(data))
	if err != nil {
		return fmt.Errorf("failed to save hand history for %s: %w", h.HandID, err)
	}
	return nil
}

// GetHandHistory retrieves and unmarshals a HandState snapshot by handID. Returns nil, nil if not found.
func (s *SQLiteDB) GetHandHistory(handID string) (*table.HandState, error) {
	query := `SELECT state_json FROM hand_histories WHERE hand_id = ?;`

	var stateJSON string
	err := s.db.QueryRow(query, handID).Scan(&stateJSON)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to query hand history for %s: %w", handID, err)
	}

	var h table.HandState
	if err := json.Unmarshal([]byte(stateJSON), &h); err != nil {
		return nil, fmt.Errorf("failed to unmarshal hand state for %s: %w", handID, err)
	}
	return &h, nil
}
