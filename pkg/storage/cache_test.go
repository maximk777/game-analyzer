package storage_test

import (
	"fmt"
	"sync"
	"testing"

	"poker-game-analyzer/pkg/storage"
	"poker-game-analyzer/pkg/table"
)

func TestMemoryCache_TableState(t *testing.T) {
	cache := storage.NewMemoryCache()

	// 1. Get non-existent
	if state := cache.GetTableState("table_unknown"); state != nil {
		t.Fatalf("expected nil for missing table, got %+v", state)
	}

	// 2. Set and Get
	c1, _ := table.ParseCard("Ah")
	c2, _ := table.ParseCard("Kh")
	state1 := &table.HandState{
		HandID:    "h1",
		TableID:   "t1",
		Street:    table.StreetPreflop,
		Pot:       10.0,
		HeroID:    "p1",
		HeroCards: [2]table.Card{c1, c2},
	}

	cache.SetTableState("t1", state1)

	retrieved := cache.GetTableState("t1")
	if retrieved == nil {
		t.Fatalf("expected state for t1, got nil")
	}
	if retrieved.HandID != "h1" || retrieved.Pot != 10.0 {
		t.Errorf("retrieved state mismatch: %+v", retrieved)
	}

	// 4. Delete table state
	cache.DeleteTableState("t1")
	if state := cache.GetTableState("t1"); state != nil {
		t.Fatalf("expected nil after DeleteTableState, got %+v", state)
	}

	// 5. Setting nil removes entry
	cache.SetTableState("t2", state1)
	cache.SetTableState("t2", nil)
	if state := cache.GetTableState("t2"); state != nil {
		t.Fatalf("expected nil after SetTableState with nil, got %+v", state)
	}
}

func TestMemoryCache_LLMProfile(t *testing.T) {
	cache := storage.NewMemoryCache()

	// 1. Get non-existent
	if prof := cache.GetProfile("player_unknown"); prof != nil {
		t.Fatalf("expected nil for missing profile, got %+v", prof)
	}

	// 2. Set and Get
	prof1 := &storage.LLMProfile{
		PlayerID:       "p_001",
		PlayerName:     "NitPlayer",
		Archetype:      "Rock",
		BluffFrequency: 0.02,
		FoldTo3Bet:     0.85,
		FoldToCBet:     0.75,
		Exploits:       "Steal blinds aggressively; fold to their aggression.",
		Notes:          "Never bets without the nuts.",
	}

	cache.SetProfile("p_001", prof1)

	retrieved := cache.GetProfile("p_001")
	if retrieved == nil {
		t.Fatalf("expected profile for p_001, got nil")
	}
	if retrieved.Archetype != "Rock" || retrieved.FoldTo3Bet != 0.85 {
		t.Errorf("retrieved profile mismatch: %+v", retrieved)
	}

	// 3. Delete profile
	cache.DeleteProfile("p_001")
	if prof := cache.GetProfile("p_001"); prof != nil {
		t.Fatalf("expected nil after DeleteProfile, got %+v", prof)
	}

	// 4. Setting nil profile deletes entry
	cache.SetProfile("p_002", prof1)
	cache.SetProfile("p_002", nil)
	if prof := cache.GetProfile("p_002"); prof != nil {
		t.Fatalf("expected nil after SetProfile(nil), got %+v", prof)
	}
}

func TestMemoryCache_PlayerStatsAndClear(t *testing.T) {
	cache := storage.NewMemoryCache()

	// 1. PlayerStats get/set
	if stats := cache.GetPlayerStats("p1"); stats != nil {
		t.Fatalf("expected nil stats for missing player, got %+v", stats)
	}

	stats1 := &storage.PlayerStats{
		PlayerID:   "p1",
		PlayerName: "Fish",
		HandsCount: 50,
		VPIP:       45.0,
		PFR:        10.0,
		ThreeBet:   2.0,
		AF:         0.8,
	}

	cache.SetPlayerStats("p1", stats1)
	if retrieved := cache.GetPlayerStats("p1"); retrieved == nil || retrieved.PlayerName != "Fish" {
		t.Fatalf("failed to retrieve player stats: %+v", retrieved)
	}

	// Set nil removes entry
	cache.SetPlayerStats("p1", nil)
	if stats := cache.GetPlayerStats("p1"); stats != nil {
		t.Fatalf("expected nil stats after SetPlayerStats(nil), got %+v", stats)
	}

	// 2. Clear all
	cache.SetTableState("t1", &table.HandState{HandID: "h1"})
	cache.SetProfile("p1", &storage.LLMProfile{PlayerID: "p1"})
	cache.SetPlayerStats("p1", stats1)

	cache.Clear()

	if cache.GetTableState("t1") != nil || cache.GetProfile("p1") != nil || cache.GetPlayerStats("p1") != nil {
		t.Fatalf("expected all cache entries to be cleared")
	}
}

func TestMemoryCache_Concurrency(t *testing.T) {
	cache := storage.NewMemoryCache()
	const workers = 50
	const iterations = 100

	var wg sync.WaitGroup
	wg.Add(workers * 2)

	// Goroutines writing & reading table states
	for w := 0; w < workers; w++ {
		workerID := w
		go func() {
			defer wg.Done()
			tableID := fmt.Sprintf("table_%d", workerID%5)
			for i := 0; i < iterations; i++ {
				cache.SetTableState(tableID, &table.HandState{
					HandID:  fmt.Sprintf("hand_%d_%d", workerID, i),
					TableID: tableID,
					Pot:     float64(i * 10),
				})
				_ = cache.GetTableState(tableID)
			}
		}()
	}

	// Goroutines writing & reading profiles
	for w := 0; w < workers; w++ {
		workerID := w
		go func() {
			defer wg.Done()
			playerID := fmt.Sprintf("player_%d", workerID%5)
			for i := 0; i < iterations; i++ {
				cache.SetProfile(playerID, &storage.LLMProfile{
					PlayerID:   playerID,
					PlayerName: fmt.Sprintf("Player_%d", workerID),
					Archetype:  "TAG",
					Notes:      fmt.Sprintf("Iter %d", i),
				})
				_ = cache.GetProfile(playerID)
			}
		}()
	}

	wg.Wait()
}
