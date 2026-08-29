package profiler_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"poker-game-analyzer/pkg/llm"
	"poker-game-analyzer/pkg/profiler"
	"poker-game-analyzer/pkg/storage"
	"poker-game-analyzer/pkg/table"
)

func createTestHand(handID string, actions []table.ActionRecord, seats []table.SeatState) table.HandState {
	c1, _ := table.ParseCard("As")
	c2, _ := table.ParseCard("Kd")
	f1, _ := table.ParseCard("2c")
	f2, _ := table.ParseCard("7d")
	f3, _ := table.ParseCard("Th")

	if seats == nil {
		seats = []table.SeatState{
			{SeatNumber: 1, PlayerID: "p1", PlayerName: "Player1", Stack: 100, IsActive: true, Position: table.PosBTN},
			{SeatNumber: 2, PlayerID: "p2", PlayerName: "Player2", Stack: 100, IsActive: true, Position: table.PosSB},
			{SeatNumber: 3, PlayerID: "p3", PlayerName: "Player3", Stack: 100, IsActive: true, Position: table.PosBB},
		}
	}

	return table.HandState{
		HandID:         handID,
		TableID:        "table_1",
		Street:         table.StreetShowdown,
		Pot:            50.0,
		HeroID:         "p1",
		HeroCards:      [2]table.Card{c1, c2},
		CommunityCards: []table.Card{f1, f2, f3},
		Seats:          seats,
		ActionHistory:  actions,
	}
}

func TestProfiler_StatisticalFormulas(t *testing.T) {
	cache := storage.NewMemoryCache()
	db, err := storage.NewSQLiteDB(":memory:")
	if err != nil {
		t.Fatalf("failed to create sqlite db: %v", err)
	}
	defer db.Close()

	mockLLM := llm.NewMockClient()
	prof := profiler.NewProfiler(cache, db, mockLLM)
	defer prof.Close()

	// Hand 1:
	// p1 raises preflop (VPIP=1, PFR=1, 3Bet=0)
	// p2 calls preflop (VPIP=1, PFR=0, 3Bet=0)
	// p3 folds preflop (VPIP=0, PFR=0, 3Bet=0)
	// Flop:
	// p2 checks, p1 bets (Bet=1)
	// p2 calls (Call=1)
	// Turn:
	// p2 checks, p1 bets (Bet=2)
	// p2 raises (Raise=1)
	// p1 calls (Call=1)
	hand1Actions := []table.ActionRecord{
		{PlayerID: "p1", Street: table.StreetPreflop, Action: table.ActionRaise, Amount: 3.0},
		{PlayerID: "p2", Street: table.StreetPreflop, Action: table.ActionCall, Amount: 3.0},
		{PlayerID: "p3", Street: table.StreetPreflop, Action: table.ActionFold, Amount: 0.0},
		{PlayerID: "p2", Street: table.StreetFlop, Action: table.ActionCheck, Amount: 0.0},
		{PlayerID: "p1", Street: table.StreetFlop, Action: table.ActionBet, Amount: 5.0},
		{PlayerID: "p2", Street: table.StreetFlop, Action: table.ActionCall, Amount: 5.0},
		{PlayerID: "p2", Street: table.StreetTurn, Action: table.ActionCheck, Amount: 0.0},
		{PlayerID: "p1", Street: table.StreetTurn, Action: table.ActionBet, Amount: 10.0},
		{PlayerID: "p2", Street: table.StreetTurn, Action: table.ActionRaise, Amount: 25.0},
		{PlayerID: "p1", Street: table.StreetTurn, Action: table.ActionCall, Amount: 25.0},
	}

	prof.ProcessHandEnd(createTestHand("hand_1", hand1Actions, nil))

	// Hand 2:
	// p1 opens (raise), p2 3-bets (raise), p3 folds, p1 calls.
	// Postflop:
	// Flop: p1 checks, p2 bets, p1 folds.
	hand2Actions := []table.ActionRecord{
		{PlayerID: "p1", Street: table.StreetPreflop, Action: table.ActionRaise, Amount: 3.0},
		{PlayerID: "p2", Street: table.StreetPreflop, Action: table.ActionRaise, Amount: 9.0}, // 3-bet!
		{PlayerID: "p3", Street: table.StreetPreflop, Action: table.ActionFold, Amount: 0.0},
		{PlayerID: "p1", Street: table.StreetPreflop, Action: table.ActionCall, Amount: 9.0},
		{PlayerID: "p1", Street: table.StreetFlop, Action: table.ActionCheck, Amount: 0.0},
		{PlayerID: "p2", Street: table.StreetFlop, Action: table.ActionBet, Amount: 12.0},
		{PlayerID: "p1", Street: table.StreetFlop, Action: table.ActionFold, Amount: 0.0},
	}

	prof.ProcessHandEnd(createTestHand("hand_2", hand2Actions, nil))

	// Verify Stats for P1:
	// Total Hands: 2
	// Hand 1: VPIP=yes, PFR=yes. Postflop: 2 bets, 1 call -> AF = 2 / 1 = 2.0
	// Hand 2: VPIP=yes, PFR=yes. Postflop: 0 bets, 0 raises, 0 calls
	// Combined P1:
	// Hands: 2
	// VPIP: 2/2 = 100.0%
	// PFR: 2/2 = 100.0%
	// 3Bet: 0/0 = 0.0%
	// Total postflop bets=2, raises=0, calls=1 -> AF = (2+0)/1 = 2.0
	s1 := prof.GetStats("p1")
	if s1 == nil {
		t.Fatalf("expected stats for p1")
	}
	if s1.HandsCount != 2 {
		t.Errorf("p1 HandsCount expected 2, got %d", s1.HandsCount)
	}
	if s1.VPIP != 100.0 {
		t.Errorf("p1 VPIP expected 100.0, got %f", s1.VPIP)
	}
	if s1.PFR != 100.0 {
		t.Errorf("p1 PFR expected 100.0, got %f", s1.PFR)
	}
	if s1.AF != 2.0 {
		t.Errorf("p1 AF expected 2.0, got %f", s1.AF)
	}

	// Verify Stats for P2:
	// Total Hands: 2
	// Hand 1: VPIP=yes (call), PFR=no. Postflop: 1 call, 1 raise -> bets=0, raises=1, calls=1
	// Hand 2: VPIP=yes (raise), PFR=yes, 3Bet=yes. Postflop: 1 bet -> bets=1, raises=0, calls=0
	// Combined P2:
	// Hands: 2
	// VPIP: 2/2 = 100.0%
	// PFR: 1/2 = 50.0%
	// 3Bet: 1/1 = 100.0% (1 3-bet out of 1 opportunity)
	// Postflop: bets=1, raises=1, calls=1 -> AF = (1+1)/1 = 2.0
	s2 := prof.GetStats("p2")
	if s2 == nil {
		t.Fatalf("expected stats for p2")
	}
	if s2.HandsCount != 2 {
		t.Errorf("p2 HandsCount expected 2, got %d", s2.HandsCount)
	}
	if s2.VPIP != 100.0 {
		t.Errorf("p2 VPIP expected 100.0, got %f", s2.VPIP)
	}
	if s2.PFR != 50.0 {
		t.Errorf("p2 PFR expected 50.0, got %f", s2.PFR)
	}
	if s2.ThreeBet != 50.0 {
		t.Errorf("p2 ThreeBet expected 50.0, got %f", s2.ThreeBet)
	}
	if s2.AF != 2.0 {
		t.Errorf("p2 AF expected 2.0, got %f", s2.AF)
	}

	// Verify Stats for P3:
	// Total Hands: 2
	// Hand 1: Fold preflop -> VPIP=0, PFR=0
	// Hand 2: Fold preflop -> VPIP=0, PFR=0
	// Combined P3:
	// Hands: 2, VPIP=0.0%, PFR=0.0%, 3Bet=0.0%, AF=0.0
	s3 := prof.GetStats("p3")
	if s3 == nil {
		t.Fatalf("expected stats for p3")
	}
	if s3.VPIP != 0.0 || s3.PFR != 0.0 || s3.AF != 0.0 {
		t.Errorf("p3 unexpected stats: %+v", s3)
	}
}

func TestProfiler_AutoSync_CacheAndDB(t *testing.T) {
	cache := storage.NewMemoryCache()
	db, err := storage.NewSQLiteDB(":memory:")
	if err != nil {
		t.Fatalf("failed to create sqlite db: %v", err)
	}
	defer db.Close()

	mockLLM := llm.NewMockClient()
	prof := profiler.NewProfiler(cache, db, mockLLM)
	defer prof.Close()

	hand := createTestHand("hand_sync_1", []table.ActionRecord{
		{PlayerID: "p1", Street: table.StreetPreflop, Action: table.ActionCall, Amount: 2.0},
	}, nil)

	prof.ProcessHandEnd(hand)
	prof.Flush()

	// Check stats in Cache
	cachedStats := cache.GetPlayerStats("p1")
	if cachedStats == nil || cachedStats.HandsCount != 1 {
		t.Fatalf("cache missing or invalid player stats: %+v", cachedStats)
	}

	// Check stats in DB
	dbStats, err := db.GetPlayerStats("p1")
	if err != nil || dbStats == nil || dbStats.HandsCount != 1 {
		t.Fatalf("db missing or invalid player stats: %+v, err: %v", dbStats, err)
	}

	// Check Hand history saved in DB
	dbHand, err := db.GetHandHistory("hand_sync_1")
	if err != nil || dbHand == nil {
		t.Fatalf("db missing hand history: %+v, err: %v", dbHand, err)
	}

	// Check LLM Profile in Cache and DB
	cachedProf := cache.GetProfile("p1")
	if cachedProf == nil {
		t.Fatalf("cache missing LLM profile for p1")
	}

	dbProf, err := db.GetLLMProfile("p1")
	if err != nil || dbProf == nil {
		t.Fatalf("db missing LLM profile for p1: %v", err)
	}
	if cachedProf.Archetype != dbProf.Archetype {
		t.Errorf("cache profile archetype %s != db profile archetype %s", cachedProf.Archetype, dbProf.Archetype)
	}
}

func TestProfiler_AsyncLLMProfiling_RateLimitAndDebounce(t *testing.T) {
	cache := storage.NewMemoryCache()
	db, err := storage.NewSQLiteDB(":memory:")
	if err != nil {
		t.Fatalf("failed to create sqlite db: %v", err)
	}
	defer db.Close()

	var analyzeCallCount int32
	mockLLM := llm.NewMockClient()
	mockLLM.AnalyzeFunc = func(ctx context.Context, history []table.HandState, stats storage.PlayerStats) (*storage.LLMProfile, error) {
		atomic.AddInt32(&analyzeCallCount, 1)
		return &storage.LLMProfile{
			PlayerID:       stats.PlayerID,
			PlayerName:     stats.PlayerName,
			Archetype:      "TAG",
			BluffFrequency: 0.25,
			FoldTo3Bet:     0.50,
			FoldToCBet:     0.55,
		}, nil
	}

	// Configure profiler with analyze interval of 5 hands and debounce duration
	prof := profiler.NewProfiler(cache, db, mockLLM,
		profiler.WithAnalyzeInterval(5),
		profiler.WithDebounceDuration(100*time.Millisecond),
	)
	defer prof.Close()

	// Process 10 hands in rapid succession
	for i := 1; i <= 10; i++ {
		hand := createTestHand(fmt.Sprintf("hand_rl_%d", i), []table.ActionRecord{
			{PlayerID: "p1", Street: table.StreetPreflop, Action: table.ActionCall, Amount: 2.0},
		}, nil)
		prof.ProcessHandEnd(hand)
	}

	prof.Flush()

	calls := atomic.LoadInt32(&analyzeCallCount)
	// With 10 hands and interval 5 (or first hand + hand 5 + hand 10), it should not be called 10 times
	if calls > 4 {
		t.Errorf("expected rate-limited analyze calls <= 4, got %d", calls)
	}
}

func TestProfiler_ConcurrentHandProcessing(t *testing.T) {
	cache := storage.NewMemoryCache()
	db, err := storage.NewSQLiteDB(":memory:")
	if err != nil {
		t.Fatalf("failed to create sqlite db: %v", err)
	}
	defer db.Close()

	mockLLM := llm.NewMockClient()
	prof := profiler.NewProfiler(cache, db, mockLLM,
		profiler.WithWorkerCount(4),
	)
	defer prof.Close()

	var wg sync.WaitGroup
	numGoroutines := 10
	handsPerGoroutine := 5

	for g := 0; g < numGoroutines; g++ {
		wg.Add(1)
		go func(gID int) {
			defer wg.Done()
			for h := 0; h < handsPerGoroutine; h++ {
				handID := fmt.Sprintf("concurrent_hand_%d_%d", gID, h)
				hand := createTestHand(handID, []table.ActionRecord{
					{PlayerID: "p1", Street: table.StreetPreflop, Action: table.ActionRaise, Amount: 3.0},
					{PlayerID: "p2", Street: table.StreetPreflop, Action: table.ActionCall, Amount: 3.0},
				}, nil)
				prof.ProcessHandEnd(hand)
			}
		}(g)
	}

	wg.Wait()
	prof.Flush()

	totalExpected := numGoroutines * handsPerGoroutine
	s1 := prof.GetStats("p1")
	if s1 == nil || s1.HandsCount != totalExpected {
		t.Fatalf("expected p1 hands count %d, got %+v", totalExpected, s1)
	}
	if s1.VPIP != 100.0 || s1.PFR != 100.0 {
		t.Errorf("unexpected stats: %+v", s1)
	}
}

func TestProfiler_GetPlayerTendencies(t *testing.T) {
	cache := storage.NewMemoryCache()
	db, err := storage.NewSQLiteDB(":memory:")
	if err != nil {
		t.Fatalf("failed to create sqlite db: %v", err)
	}
	defer db.Close()

	mockLLM := llm.NewMockClient()
	mockLLM.AnalyzeFunc = func(ctx context.Context, history []table.HandState, stats storage.PlayerStats) (*storage.LLMProfile, error) {
		return &storage.LLMProfile{
			PlayerID:       stats.PlayerID,
			PlayerName:     stats.PlayerName,
			Archetype:      "LAG",
			BluffFrequency: 0.35,
			FoldTo3Bet:     0.45,
			FoldToCBet:     0.55,
		}, nil
	}

	prof := profiler.NewProfiler(cache, db, mockLLM)
	defer prof.Close()

	hand := createTestHand("hand_tend_1", []table.ActionRecord{
		{PlayerID: "p1", Street: table.StreetPreflop, Action: table.ActionRaise, Amount: 3.0},
	}, nil)

	prof.ProcessHandEnd(hand)
	prof.Flush()

	tendencies := prof.GetPlayerTendencies("p1")
	if tendencies == nil {
		t.Fatalf("expected non-nil tendencies")
	}

	if val, ok := tendencies["vpip"]; !ok || val != 100.0 {
		t.Errorf("expected vpip 100.0, got %f", val)
	}
	if val, ok := tendencies["bluff_frequency"]; !ok || val != 0.35 {
		t.Errorf("expected bluff_frequency 0.35, got %f", val)
	}
	if val, ok := tendencies["fold_to_3bet"]; !ok || val != 0.45 {
		t.Errorf("expected fold_to_3bet 0.45, got %f", val)
	}
	if val, ok := tendencies["fold_to_cbet"]; !ok || val != 0.55 {
		t.Errorf("expected fold_to_cbet 0.55, got %f", val)
	}

	// Missing player returns empty map
	missingTendencies := prof.GetPlayerTendencies("missing_player")
	if missingTendencies == nil {
		t.Fatalf("expected non-nil empty map for missing player")
	}
	if len(missingTendencies) != 0 {
		t.Errorf("expected 0 entries for missing player, got %d", len(missingTendencies))
	}
}

func TestProfiler_LLMErrorHandling(t *testing.T) {
	cache := storage.NewMemoryCache()
	db, err := storage.NewSQLiteDB(":memory:")
	if err != nil {
		t.Fatalf("failed to create sqlite db: %v", err)
	}
	defer db.Close()

	mockLLM := llm.NewMockClient()
	mockLLM.AnalyzeFunc = func(ctx context.Context, history []table.HandState, stats storage.PlayerStats) (*storage.LLMProfile, error) {
		return nil, errors.New("llm service unavailable")
	}

	prof := profiler.NewProfiler(cache, db, mockLLM)
	defer prof.Close()

	hand := createTestHand("hand_err_1", []table.ActionRecord{
		{PlayerID: "p1", Street: table.StreetPreflop, Action: table.ActionCall, Amount: 2.0},
	}, nil)

	// Should not panic or crash
	prof.ProcessHandEnd(hand)
	prof.Flush()

	// Stats should still be correctly saved
	stats := prof.GetStats("p1")
	if stats == nil || stats.HandsCount != 1 {
		t.Errorf("stats not saved despite LLM error: %+v", stats)
	}
}
