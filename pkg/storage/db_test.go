package storage_test

import (
	"path/filepath"
	"testing"

	"poker-game-analyzer/pkg/storage"
	"poker-game-analyzer/pkg/table"
)

func TestSQLiteDB_Initialization(t *testing.T) {
	// Test in-memory database initialization
	db, err := storage.NewSQLiteDB(":memory:")
	if err != nil {
		t.Fatalf("failed to create in-memory SQLite DB: %v", err)
	}
	defer db.Close()

	// Test file-based database initialization in temp dir
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test_poker.db")
	fileDB, err := storage.NewSQLiteDB(dbPath)
	if err != nil {
		t.Fatalf("failed to create file SQLite DB at %s: %v", dbPath, err)
	}
	defer fileDB.Close()
}

func TestSQLiteDB_PlayerStatsCRUD(t *testing.T) {
	db, err := storage.NewSQLiteDB(":memory:")
	if err != nil {
		t.Fatalf("NewSQLiteDB failed: %v", err)
	}
	defer db.Close()

	// 1. Query non-existent player
	stats, err := db.GetPlayerStats("player_999")
	if err != nil {
		t.Fatalf("GetPlayerStats unexpected error for missing player: %v", err)
	}
	if stats != nil {
		t.Fatalf("expected nil stats for non-existent player, got %+v", stats)
	}

	// 2. Insert PlayerStats
	p1 := storage.PlayerStats{
		PlayerID:   "p123",
		PlayerName: "AggroShark",
		HandsCount: 150,
		VPIP:       28.5,
		PFR:        22.0,
		ThreeBet:   8.5,
		AF:         2.4,
	}

	if err := db.SavePlayerStats(p1); err != nil {
		t.Fatalf("SavePlayerStats failed: %v", err)
	}

	// 3. Retrieve PlayerStats
	retrieved, err := db.GetPlayerStats("p123")
	if err != nil {
		t.Fatalf("GetPlayerStats failed: %v", err)
	}
	if retrieved == nil {
		t.Fatalf("expected player stats for p123, got nil")
	}

	if retrieved.PlayerID != p1.PlayerID ||
		retrieved.PlayerName != p1.PlayerName ||
		retrieved.HandsCount != p1.HandsCount ||
		retrieved.VPIP != p1.VPIP ||
		retrieved.PFR != p1.PFR ||
		retrieved.ThreeBet != p1.ThreeBet ||
		retrieved.AF != p1.AF {
		t.Errorf("retrieved stats %+v != expected %+v", retrieved, p1)
	}

	// 4. Upsert (update existing player)
	p1Updated := storage.PlayerStats{
		PlayerID:   "p123",
		PlayerName: "AggroShark",
		HandsCount: 200,
		VPIP:       30.0,
		PFR:        24.0,
		ThreeBet:   10.0,
		AF:         3.1,
	}

	if err := db.SavePlayerStats(p1Updated); err != nil {
		t.Fatalf("SavePlayerStats update failed: %v", err)
	}

	updated, err := db.GetPlayerStats("p123")
	if err != nil {
		t.Fatalf("GetPlayerStats after update failed: %v", err)
	}
	if updated == nil {
		t.Fatalf("expected updated stats for p123, got nil")
	}

	if updated.HandsCount != 200 || updated.VPIP != 30.0 || updated.AF != 3.1 {
		t.Errorf("stats not updated correctly: %+v", updated)
	}
}

func TestSQLiteDB_LLMProfileCRUD(t *testing.T) {
	db, err := storage.NewSQLiteDB(":memory:")
	if err != nil {
		t.Fatalf("NewSQLiteDB failed: %v", err)
	}
	defer db.Close()

	// 1. Query non-existent profile
	prof, err := db.GetLLMProfile("non_existent")
	if err != nil {
		t.Fatalf("GetLLMProfile unexpected error for missing profile: %v", err)
	}
	if prof != nil {
		t.Fatalf("expected nil for missing profile, got %+v", prof)
	}

	// 2. Insert LLMProfile
	p := storage.LLMProfile{
		PlayerID:       "fish_456",
		PlayerName:     "CallingStation",
		Archetype:      "Passive Fish",
		BluffFrequency: 0.08,
		FoldTo3Bet:     0.15,
		FoldToCBet:     0.25,
		Exploits:       "Over-value bet for thin value; do not bluff.",
		Notes:          "Loves chasing gutshots and underpairs.",
	}

	if err := db.SaveLLMProfile(p); err != nil {
		t.Fatalf("SaveLLMProfile failed: %v", err)
	}

	// 3. Retrieve LLMProfile
	retrieved, err := db.GetLLMProfile("fish_456")
	if err != nil {
		t.Fatalf("GetLLMProfile failed: %v", err)
	}
	if retrieved == nil {
		t.Fatalf("expected profile for fish_456, got nil")
	}

	if retrieved.PlayerID != p.PlayerID ||
		retrieved.PlayerName != p.PlayerName ||
		retrieved.Archetype != p.Archetype ||
		retrieved.BluffFrequency != p.BluffFrequency ||
		retrieved.FoldTo3Bet != p.FoldTo3Bet ||
		retrieved.FoldToCBet != p.FoldToCBet ||
		retrieved.Exploits != p.Exploits ||
		retrieved.Notes != p.Notes {
		t.Errorf("retrieved profile %+v != expected %+v", retrieved, p)
	}

	// 4. Upsert (update existing profile)
	pUpdate := storage.LLMProfile{
		PlayerID:       "fish_456",
		PlayerName:     "CallingStation",
		Archetype:      "TAG",
		BluffFrequency: 0.22,
		FoldTo3Bet:     0.55,
		FoldToCBet:     0.60,
		Exploits:       "Apply pressure on turn barrels.",
		Notes:          "Tightened up significantly after losing a big pot.",
	}

	if err := db.SaveLLMProfile(pUpdate); err != nil {
		t.Fatalf("SaveLLMProfile update failed: %v", err)
	}

	updated, err := db.GetLLMProfile("fish_456")
	if err != nil {
		t.Fatalf("GetLLMProfile after update failed: %v", err)
	}
	if updated == nil {
		t.Fatalf("expected updated profile, got nil")
	}

	if updated.Archetype != "TAG" || updated.BluffFrequency != 0.22 || updated.FoldTo3Bet != 0.55 {
		t.Errorf("profile not updated properly: %+v", updated)
	}
}

func TestSQLiteDB_SaveHandHistory(t *testing.T) {
	db, err := storage.NewSQLiteDB(":memory:")
	if err != nil {
		t.Fatalf("NewSQLiteDB failed: %v", err)
	}
	defer db.Close()

	c1, _ := table.ParseCard("As")
	c2, _ := table.ParseCard("Kd")
	f1, _ := table.ParseCard("Qh")
	f2, _ := table.ParseCard("Jc")
	f3, _ := table.ParseCard("Th")

	hand := table.HandState{
		HandID:         "hand_1001",
		TableID:        "table_zoom_1",
		Street:         table.StreetFlop,
		Pot:            45.5,
		CurrentBet:     15.0,
		MinRaise:       30.0,
		CommunityCards: []table.Card{f1, f2, f3},
		HeroID:         "hero_007",
		HeroCards:      [2]table.Card{c1, c2},
		Seats: []table.SeatState{
			{SeatNumber: 1, PlayerID: "hero_007", PlayerName: "Hero", Stack: 180.0, CurrentBet: 15.0, IsActive: true, Position: table.PosBTN},
			{SeatNumber: 2, PlayerID: "villain_1", PlayerName: "Villain", Stack: 220.0, CurrentBet: 15.0, IsActive: true, Position: table.PosBB},
		},
		ActionHistory: []table.ActionRecord{
			{PlayerID: "villain_1", Street: table.StreetPreflop, Action: table.ActionRaise, Amount: 3.0},
			{PlayerID: "hero_007", Street: table.StreetPreflop, Action: table.ActionCall, Amount: 3.0},
			{PlayerID: "villain_1", Street: table.StreetFlop, Action: table.ActionBet, Amount: 15.0},
		},
	}

	// 0. Query non-existent hand
	missingHand, err := db.GetHandHistory("non_existent_hand")
	if err != nil {
		t.Fatalf("GetHandHistory error for missing hand: %v", err)
	}
	if missingHand != nil {
		t.Fatalf("expected nil for missing hand, got %+v", missingHand)
	}

	// 1. Save HandState
	if err := db.SaveHandHistory(hand); err != nil {
		t.Fatalf("SaveHandHistory failed: %v", err)
	}

	saved, err := db.GetHandHistory("hand_1001")
	if err != nil {
		t.Fatalf("GetHandHistory failed: %v", err)
	}
	if saved == nil {
		t.Fatalf("expected saved hand history for hand_1001, got nil")
	}
	if saved.HandID != hand.HandID || saved.Pot != hand.Pot || saved.Street != hand.Street || len(saved.CommunityCards) != 3 {
		t.Errorf("saved hand mismatch: %+v", saved)
	}

	// 2. Upsert / update HandState
	hand.Street = table.StreetTurn
	t1, _ := table.ParseCard("2s")
	hand.CommunityCards = append(hand.CommunityCards, t1)
	hand.Pot = 95.5

	if err := db.SaveHandHistory(hand); err != nil {
		t.Fatalf("SaveHandHistory update failed: %v", err)
	}

	updated, err := db.GetHandHistory("hand_1001")
	if err != nil {
		t.Fatalf("GetHandHistory after update failed: %v", err)
	}
	if updated == nil {
		t.Fatalf("expected updated hand history, got nil")
	}
	if updated.Street != table.StreetTurn || updated.Pot != 95.5 || len(updated.CommunityCards) != 4 {
		t.Errorf("updated hand mismatch: %+v", updated)
	}
}
