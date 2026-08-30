package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"poker-game-analyzer/pkg/profiler"
	"poker-game-analyzer/pkg/storage"
	"poker-game-analyzer/pkg/table"
	"poker-game-analyzer/pkg/vision"
)

func setupTestServer(t *testing.T) (*Server, *storage.MemoryCache, *storage.SQLiteDB, *profiler.Profiler) {
	t.Helper()

	cache := storage.NewMemoryCache()
	db, err := storage.NewSQLiteDB(":memory:")
	if err != nil {
		t.Fatalf("failed to create sqlite db: %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
	})

	prof := profiler.NewProfiler(cache, db, nil)
	t.Cleanup(func() {
		prof.Close()
	})

	srv := NewServer(cache, db, prof)
	return srv, cache, db, prof
}

func TestInitTable_Success(t *testing.T) {
	srv, cache, _, _ := setupTestServer(t)

	c1, _ := table.ParseCard("As")
	c2, _ := table.ParseCard("Kh")

	initReq := TableInitRequest{
		TableID: "table-1",
		HeroID:  "player-hero",
		Seats: []table.SeatState{
			{SeatNumber: 1, PlayerID: "player-hero", PlayerName: "Hero", Stack: 200, CurrentBet: 0, IsActive: true, Position: table.PosBTN},
			{SeatNumber: 2, PlayerID: "player-villain", PlayerName: "Villain", Stack: 200, CurrentBet: 2, IsActive: true, Position: table.PosBB},
		},
		Pot:      3.0,
		MinRaise: 4.0,
	}

	body, err := json.Marshal(initReq)
	if err != nil {
		t.Fatalf("marshal error: %v", err)
	}

	req := httptest.NewRequest("POST", "/api/v1/tables", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	srv.Router().ServeHTTP(w, req)

	if w.Code != http.StatusOK && w.Code != http.StatusCreated {
		t.Fatalf("expected 200 or 201, got %d: %s", w.Code, w.Body.String())
	}

	state := cache.GetTableState("table-1")
	if state == nil {
		t.Fatalf("expected table-1 to be cached, got nil")
	}
	if state.TableID != "table-1" || state.HeroID != "player-hero" {
		t.Errorf("unexpected table state in cache: %+v", state)
	}

	_ = c1
	_ = c2
}

func TestInitTable_InvalidBody(t *testing.T) {
	srv, _, _, _ := setupTestServer(t)

	req := httptest.NewRequest("POST", "/api/v1/tables", bytes.NewReader([]byte("invalid json")))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	srv.Router().ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 Bad Request, got %d", w.Code)
	}
}

func TestGetTableState_Found(t *testing.T) {
	srv, cache, _, _ := setupTestServer(t)

	c1, _ := table.ParseCard("Ah")
	c2, _ := table.ParseCard("Kd")

	initialState := &table.HandState{
		HandID:    "hand-101",
		TableID:   "table-99",
		Street:    table.StreetFlop,
		Pot:       45.0,
		HeroID:    "hero-1",
		HeroCards: [2]table.Card{c1, c2},
		CommunityCards: []table.Card{
			{Rank: table.RankAce, Suit: table.Spades},
			{Rank: table.RankKing, Suit: table.Diamonds},
			{Rank: table.RankTwo, Suit: table.Clubs},
		},
	}
	cache.SetTableState("table-99", initialState)

	req := httptest.NewRequest("GET", "/api/v1/tables/table-99/state", nil)
	w := httptest.NewRecorder()

	srv.Router().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d: %s", w.Code, w.Body.String())
	}

	var resp table.HandState
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if resp.HandID != "hand-101" || resp.TableID != "table-99" || len(resp.CommunityCards) != 3 {
		t.Errorf("unexpected hand state returned: %+v", resp)
	}
}

func TestGetTableState_NotFound(t *testing.T) {
	srv, _, _, _ := setupTestServer(t)

	req := httptest.NewRequest("GET", "/api/v1/tables/nonexistent/state", nil)
	w := httptest.NewRecorder()

	srv.Router().ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404 Not Found, got %d", w.Code)
	}
}

func TestIngestEvent_HeroTurnAndRecommendation(t *testing.T) {
	srv, cache, _, _ := setupTestServer(t)

	c1, _ := table.ParseCard("As")
	c2, _ := table.ParseCard("Ks")
	b1, _ := table.ParseCard("Ah")
	b2, _ := table.ParseCard("Kd")
	b3, _ := table.ParseCard("2c")

	handState := &table.HandState{
		HandID:         "hand-555",
		TableID:        "table-555",
		Street:         table.StreetFlop,
		Pot:            100.0,
		CurrentBet:     20.0,
		MinRaise:       40.0,
		HeroID:         "hero-p1",
		HeroCards:      [2]table.Card{c1, c2},
		CommunityCards: []table.Card{b1, b2, b3},
		Seats: []table.SeatState{
			{SeatNumber: 1, PlayerID: "hero-p1", PlayerName: "Hero", Stack: 300, CurrentBet: 0, IsActive: true},
			{SeatNumber: 2, PlayerID: "villain-p2", PlayerName: "Villain", Stack: 300, CurrentBet: 20, IsActive: true},
		},
		ActionHistory: []table.ActionRecord{
			{PlayerID: "villain-p2", Street: table.StreetFlop, Action: table.ActionBet, Amount: 20},
		},
	}

	event := vision.VisionEvent{
		Type:        vision.EventHeroTurn,
		TableID:     "table-555",
		HandState:   handState,
		Description: "Hero turn facing 20 bet",
	}

	body, err := json.Marshal(event)
	if err != nil {
		t.Fatalf("marshal error: %v", err)
	}

	req := httptest.NewRequest("POST", "/api/v1/tables/table-555/events", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	srv.Router().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d: %s", w.Code, w.Body.String())
	}

	var resp EventIngestResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if resp.Status != "ok" {
		t.Errorf("expected status 'ok', got %s", resp.Status)
	}
	if resp.Recommendation == nil {
		t.Fatalf("expected non-nil recommendation, got nil")
	}

	rec := resp.Recommendation
	if rec.Equity <= 0.5 {
		t.Errorf("expected high equity for top two pair (AK on AK2), got %f", rec.Equity)
	}
	if rec.PrimaryAction == "" {
		t.Errorf("expected primary action to be populated, got empty")
	}

	// Verify table state in cache
	cached := cache.GetTableState("table-555")
	if cached == nil || cached.HandID != "hand-555" {
		t.Errorf("expected cache to hold hand-555, got: %+v", cached)
	}
}

func TestIngestEvent_HandEnd_Persistence(t *testing.T) {
	srv, _, db, prof := setupTestServer(t)

	c1, _ := table.ParseCard("Qh")
	c2, _ := table.ParseCard("Qd")

	completedHand := &table.HandState{
		HandID:         "hand-ended-777",
		TableID:        "table-777",
		Street:         table.StreetShowdown,
		Pot:            250.0,
		HeroID:         "hero-p1",
		HeroCards:      [2]table.Card{c1, c2},
		CommunityCards: []table.Card{},
		Seats: []table.SeatState{
			{SeatNumber: 1, PlayerID: "hero-p1", PlayerName: "Hero", Stack: 400, CurrentBet: 50, IsActive: true},
			{SeatNumber: 2, PlayerID: "villain-p2", PlayerName: "Villain", Stack: 350, CurrentBet: 50, IsActive: true},
		},
		ActionHistory: []table.ActionRecord{
			{PlayerID: "hero-p1", Street: table.StreetPreflop, Action: table.ActionRaise, Amount: 10},
			{PlayerID: "villain-p2", Street: table.StreetPreflop, Action: table.ActionCall, Amount: 10},
		},
	}

	event := vision.VisionEvent{
		Type:        vision.EventHandEnd,
		TableID:     "table-777",
		HandState:   completedHand,
		Description: "Hand completed",
	}

	body, _ := json.Marshal(event)
	req := httptest.NewRequest("POST", "/api/v1/tables/table-777/events", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	srv.Router().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d: %s", w.Code, w.Body.String())
	}

	// Verify DB persisted hand history
	saved, err := db.GetHandHistory("hand-ended-777")
	if err != nil {
		t.Fatalf("error getting hand history from db: %v", err)
	}
	if saved == nil {
		t.Fatalf("expected hand to be persisted in DB, got nil")
	}
	if saved.HandID != "hand-ended-777" || saved.Pot != 250.0 {
		t.Errorf("persisted hand mismatch: %+v", saved)
	}

	// Verify profiler recorded stats
	stats := prof.GetStats("hero-p1")
	if stats == nil || stats.HandsCount != 1 {
		t.Errorf("expected hero-p1 to have 1 hand recorded, got: %+v", stats)
	}
}

func TestGetPlayerProfile_FoundAndNotFound(t *testing.T) {
	srv, _, db, _ := setupTestServer(t)

	// Prepopulate player stats and LLM profile in DB
	pStats := storage.PlayerStats{
		PlayerID:   "player-fish-1",
		PlayerName: "FishMaster",
		HandsCount: 42,
		VPIP:       65.5,
		PFR:        12.0,
		ThreeBet:   4.5,
		AF:         0.8,
	}
	if err := db.SavePlayerStats(pStats); err != nil {
		t.Fatalf("failed to save stats: %v", err)
	}

	pProfile := storage.LLMProfile{
		PlayerID:       "player-fish-1",
		PlayerName:     "FishMaster",
		Archetype:      "Calling Station",
		BluffFrequency: 0.1,
		FoldTo3Bet:     0.2,
		FoldToCBet:     0.25,
		Exploits:       "Never bluff, value bet heavy",
		Notes:          "Loves suited connectors",
	}
	if err := db.SaveLLMProfile(pProfile); err != nil {
		t.Fatalf("failed to save profile: %v", err)
	}

	// 1. GET existing player profile
	req := httptest.NewRequest("GET", "/api/v1/players/player-fish-1/profile", nil)
	w := httptest.NewRecorder()
	srv.Router().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d: %s", w.Code, w.Body.String())
	}

	var resp PlayerProfileResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to unmarshal player profile: %v", err)
	}

	if resp.PlayerID != "player-fish-1" {
		t.Errorf("expected player_id 'player-fish-1', got %s", resp.PlayerID)
	}
	if resp.Stats == nil || resp.Stats.HandsCount != 42 || resp.Stats.VPIP != 65.5 {
		t.Errorf("unexpected stats: %+v", resp.Stats)
	}
	if resp.Profile == nil || resp.Profile.Archetype != "Calling Station" {
		t.Errorf("unexpected profile: %+v", resp.Profile)
	}
	if resp.Tendencies["vpip"] != 65.5 || resp.Tendencies["bluff_frequency"] != 0.1 {
		t.Errorf("unexpected tendencies map: %+v", resp.Tendencies)
	}

	// 2. GET non-existent player profile
	req404 := httptest.NewRequest("GET", "/api/v1/players/ghost-player/profile", nil)
	w404 := httptest.NewRecorder()
	srv.Router().ServeHTTP(w404, req404)

	if w404.Code != http.StatusNotFound {
		t.Errorf("expected 404 Not Found for ghost-player, got %d", w404.Code)
	}
}

func TestServerStartStop(t *testing.T) {
	srv, _, _, _ := setupTestServer(t)

	errCh := make(chan error, 1)
	go func() {
		// Start on random local port
		errCh <- srv.Start("127.0.0.1:0")
	}()

	// Wait briefly for server to bind
	time.Sleep(50 * time.Millisecond)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if err := srv.Stop(ctx); err != nil {
		t.Fatalf("failed to stop server gracefully: %v", err)
	}

	select {
	case err := <-errCh:
		if err != nil && err != http.ErrServerClosed {
			t.Errorf("unexpected error on server stop: %v", err)
		}
	case <-time.After(1 * time.Second):
		t.Errorf("timed out waiting for server to shut down")
	}
}

func TestInitTable_MissingTableID(t *testing.T) {
	srv, _, _, _ := setupTestServer(t)

	initReq := TableInitRequest{
		TableID: "",
		HeroID:  "player-hero",
	}
	body, _ := json.Marshal(initReq)
	req := httptest.NewRequest("POST", "/api/v1/tables", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	srv.Router().ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 Bad Request, got %d", w.Code)
	}
}

func TestIngestEvent_InvalidJSON(t *testing.T) {
	srv, _, _, _ := setupTestServer(t)

	req := httptest.NewRequest("POST", "/api/v1/tables/table-1/events", bytes.NewReader([]byte("not-valid-json")))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	srv.Router().ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 Bad Request, got %d", w.Code)
	}
}

func TestIngestEvent_NoHeroCards_NoRecommendation(t *testing.T) {
	srv, cache, _, _ := setupTestServer(t)

	handState := &table.HandState{
		HandID:     "hand-no-cards",
		TableID:    "table-no-cards",
		Street:     table.StreetPreflop,
		Pot:        10.0,
		CurrentBet: 2.0,
		HeroID:     "hero-1",
		// HeroCards zero value
		Seats: []table.SeatState{
			{SeatNumber: 1, PlayerID: "hero-1", PlayerName: "Hero", Stack: 100, IsActive: true},
			{SeatNumber: 2, PlayerID: "villain-1", PlayerName: "Villain", Stack: 100, IsActive: true},
		},
	}

	event := vision.VisionEvent{
		Type:        vision.EventHandStart,
		TableID:     "table-no-cards",
		HandState:   handState,
		Description: "Hand started, hero cards not visible",
	}

	body, _ := json.Marshal(event)
	req := httptest.NewRequest("POST", "/api/v1/tables/table-no-cards/events", bytes.NewReader(body))
	w := httptest.NewRecorder()

	srv.Router().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", w.Code)
	}

	var resp EventIngestResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}

	if resp.Recommendation != nil {
		t.Errorf("expected nil recommendation when no hero cards, got: %+v", resp.Recommendation)
	}

	if cache.GetTableState("table-no-cards") == nil {
		t.Errorf("expected state to be cached")
	}
}

func TestMountStatic(t *testing.T) {
	srv, _, _, _ := setupTestServer(t)
	srv.MountStatic("../../web")

	req := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()

	srv.Router().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for /, got %d", w.Code)
	}

	if !bytes.Contains(w.Body.Bytes(), []byte("POKER RTA HUD")) {
		t.Errorf("expected index.html content to contain 'POKER RTA HUD'")
	}

	// Test static CSS file
	reqCSS := httptest.NewRequest("GET", "/style.css", nil)
	wCSS := httptest.NewRecorder()
	srv.Router().ServeHTTP(wCSS, reqCSS)

	if wCSS.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for /style.css, got %d", wCSS.Code)
	}
}
