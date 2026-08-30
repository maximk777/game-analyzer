package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"image"
	"image/color"
	"image/draw"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"poker-game-analyzer/pkg/capture"
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
		HandID:     "hand-555",
		TableID:    "table-555",
		Street:     table.StreetFlop,
		Pot:        100.0,
		CurrentBet: 20.0,
		MinRaise:   40.0,
		HeroID:     "hero-p1",
		// The client is waiting on hero: without that there is no decision
		// to advise about, and the tool used to advise on hands hero had
		// already folded.
		IsHeroTurn:     true,
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

	// Test Floating HUD Widget
	reqHUD := httptest.NewRequest("GET", "/hud.html", nil)
	wHUD := httptest.NewRecorder()
	srv.Router().ServeHTTP(wHUD, reqHUD)

	if wHUD.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for /hud.html, got %d", wHUD.Code)
	}
	if !bytes.Contains(wHUD.Body.Bytes(), []byte("hud-widget")) {
		t.Errorf("expected hud.html to contain 'hud-widget'")
	}

	// Test ROI Calibration UI
	reqCal := httptest.NewRequest("GET", "/calibrate.html", nil)
	wCal := httptest.NewRecorder()
	srv.Router().ServeHTTP(wCal, reqCal)

	if wCal.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for /calibrate.html, got %d", wCal.Code)
	}
	if !bytes.Contains(wCal.Body.Bytes(), []byte("ROI Calibrator")) {
		t.Errorf("expected calibrate.html to contain 'ROI Calibrator'")
	}
}

func TestGetSnapshot_NotFound(t *testing.T) {
	srv, _, _, _ := setupTestServer(t)

	req := httptest.NewRequest("GET", "/api/v1/snapshot", nil)
	w := httptest.NewRecorder()
	srv.Router().ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404 Not Found when no snapshot, got %d", w.Code)
	}
}

func TestGetSnapshot_Found(t *testing.T) {
	srv, _, _, _ := setupTestServer(t)

	// Create a synthetic test image
	img := image.NewRGBA(image.Rect(0, 0, 100, 100))
	draw.Draw(img, img.Bounds(), &image.Uniform{C: color.RGBA{R: 200, G: 50, B: 50, A: 255}}, image.Point{}, draw.Src)

	srv.SetSnapshot(img)

	req := httptest.NewRequest("GET", "/api/v1/snapshot", nil)
	w := httptest.NewRecorder()
	srv.Router().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d: %s", w.Code, w.Body.String())
	}

	contentType := w.Header().Get("Content-Type")
	if contentType != "image/jpeg" {
		t.Errorf("expected Content-Type 'image/jpeg', got %q", contentType)
	}

	body := w.Body.Bytes()
	if len(body) < 4 {
		t.Fatalf("expected non-empty JPEG body, got %d bytes", len(body))
	}
	// Verify JPEG magic bytes: 0xFF, 0xD8
	if body[0] != 0xFF || body[1] != 0xD8 {
		t.Errorf("expected JPEG magic bytes 0xFFD8, got 0x%X 0x%X", body[0], body[1])
	}
}

func TestSetSnapshotBytesAndClear(t *testing.T) {
	srv, _, _, _ := setupTestServer(t)

	customPayload := []byte("custom-image-payload")
	srv.SetSnapshotBytes(customPayload, "image/jpeg")

	req := httptest.NewRequest("GET", "/api/v1/snapshot", nil)
	w := httptest.NewRecorder()
	srv.Router().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", w.Code)
	}
	if !bytes.Equal(w.Body.Bytes(), customPayload) {
		t.Errorf("expected body %q, got %q", customPayload, w.Body.Bytes())
	}

	// Clearing snapshot
	srv.SetSnapshot(nil)
	req2 := httptest.NewRequest("GET", "/api/v1/snapshot", nil)
	w2 := httptest.NewRecorder()
	srv.Router().ServeHTTP(w2, req2)

	if w2.Code != http.StatusNotFound {
		t.Errorf("expected 404 after clearing snapshot, got %d", w2.Code)
	}
}

func TestGetAndSetROIConfig(t *testing.T) {
	srv, _, _, _ := setupTestServer(t)

	// 1. GET initial default ROI
	reqGet := httptest.NewRequest("GET", "/api/v1/roi", nil)
	wGet := httptest.NewRecorder()
	srv.Router().ServeHTTP(wGet, reqGet)

	if wGet.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for GET /api/v1/roi, got %d", wGet.Code)
	}

	var initialROI vision.ROIConfig
	if err := json.Unmarshal(wGet.Body.Bytes(), &initialROI); err != nil {
		t.Fatalf("failed to parse initial ROI JSON: %v", err)
	}

	if len(initialROI.Seats) != 6 || len(initialROI.CommunityCards) != 5 {
		t.Errorf("unexpected default ROI structure: %+v", initialROI)
	}

	// 2. POST updated ROI
	updatedROI := initialROI
	updatedROI.Pot = vision.RectF{X: 0.45, Y: 0.35, Width: 0.20, Height: 0.08}
	updatedROI.HeroCards[0].X = 0.41

	postBody, err := json.Marshal(updatedROI)
	if err != nil {
		t.Fatalf("failed to marshal updated ROI: %v", err)
	}

	reqPost := httptest.NewRequest("POST", "/api/v1/roi", bytes.NewReader(postBody))
	reqPost.Header.Set("Content-Type", "application/json")
	wPost := httptest.NewRecorder()
	srv.Router().ServeHTTP(wPost, reqPost)

	if wPost.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for POST /api/v1/roi, got %d: %s", wPost.Code, wPost.Body.String())
	}

	var postResp vision.ROIConfig
	if err := json.Unmarshal(wPost.Body.Bytes(), &postResp); err != nil {
		t.Fatalf("failed to decode POST response: %v", err)
	}
	if postResp.Pot.X != 0.45 || postResp.HeroCards[0].X != 0.41 {
		t.Errorf("POST response did not match updated ROI: %+v", postResp)
	}

	// 3. GET verify persistence
	reqGet2 := httptest.NewRequest("GET", "/api/v1/roi", nil)
	wGet2 := httptest.NewRecorder()
	srv.Router().ServeHTTP(wGet2, reqGet2)

	var savedROI vision.ROIConfig
	_ = json.Unmarshal(wGet2.Body.Bytes(), &savedROI)
	if savedROI.Pot.X != 0.45 || savedROI.HeroCards[0].X != 0.41 {
		t.Errorf("saved ROI did not reflect POST changes: %+v", savedROI)
	}
}

func TestSetROIConfig_InvalidJSON(t *testing.T) {
	srv, _, _, _ := setupTestServer(t)

	req := httptest.NewRequest("POST", "/api/v1/roi", bytes.NewReader([]byte("{invalid-json-body")))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.Router().ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 Bad Request for invalid ROI JSON, got %d", w.Code)
	}
}

func TestGetWindows_MockProvider(t *testing.T) {
	srv, _, _, _ := setupTestServer(t)

	mockWindows := []capture.WindowInfo{
		{
			ID:         1001,
			Title:      "CoinPoker - Table NLH 100",
			OwnerName:  "CoinPoker",
			Bounds:     image.Rect(100, 100, 900, 700),
			IsOnScreen: true,
		},
		{
			ID:         1002,
			Title:      "Terminal",
			OwnerName:  "iTerm2",
			Bounds:     image.Rect(0, 0, 800, 600),
			IsOnScreen: true,
		},
	}

	srv.SetWindowsProvider(func() ([]capture.WindowInfo, error) {
		return mockWindows, nil
	})

	req := httptest.NewRequest("GET", "/api/v1/windows", nil)
	w := httptest.NewRecorder()
	srv.Router().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for GET /api/v1/windows, got %d: %s", w.Code, w.Body.String())
	}

	var wins []capture.WindowInfo
	if err := json.Unmarshal(w.Body.Bytes(), &wins); err != nil {
		t.Fatalf("failed to decode windows response: %v", err)
	}

	if len(wins) != 2 {
		t.Fatalf("expected 2 windows, got %d", len(wins))
	}
	if wins[0].ID != 1001 || wins[0].Title != "CoinPoker - Table NLH 100" {
		t.Errorf("unexpected first window: %+v", wins[0])
	}
}

func TestGetWindows_Error(t *testing.T) {
	srv, _, _, _ := setupTestServer(t)

	srv.SetWindowsProvider(func() ([]capture.WindowInfo, error) {
		return nil, errors.New("simulated window server failure")
	})

	req := httptest.NewRequest("GET", "/api/v1/windows", nil)
	w := httptest.NewRecorder()
	srv.Router().ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500 Internal Server Error when window provider fails, got %d", w.Code)
	}
}
