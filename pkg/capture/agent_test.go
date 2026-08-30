package capture

import (
	"context"
	"image"
	"image/color"
	"image/draw"
	"testing"
	"time"

	"poker-game-analyzer/pkg/profiler"
	"poker-game-analyzer/pkg/server"
	"poker-game-analyzer/pkg/storage"
	"poker-game-analyzer/pkg/table"
	"poker-game-analyzer/pkg/vision"
)

func setupTestServer(t *testing.T) (*server.Server, *storage.MemoryCache, *storage.SQLiteDB, *profiler.Profiler) {
	t.Helper()

	cache := storage.NewMemoryCache()
	db, err := storage.NewSQLiteDB(":memory:")
	if err != nil {
		t.Fatalf("failed to create in-memory sqlite db: %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
	})

	prof := profiler.NewProfiler(cache, db, nil)
	t.Cleanup(func() {
		prof.Close()
	})

	srv := server.NewServer(cache, db, prof)
	return srv, cache, db, prof
}

// createSyntheticTableImage renders a 1000x800 synthetic poker table image with cards and pot.
func createSyntheticTableImage(heroCards [2]table.Card, board []table.Card, potVal float64, cfg vision.ROIConfig) image.Image {
	img := image.NewRGBA(image.Rect(0, 0, 1000, 800))

	// Table felt (green)
	draw.Draw(img, img.Bounds(), &image.Uniform{C: color.RGBA{R: 25, G: 75, B: 35, A: 255}}, image.Point{}, draw.Src)

	// Draw Hero Cards if rank > 0
	for i := 0; i < 2 && i < len(cfg.HeroCards); i++ {
		if heroCards[i].Rank > 0 {
			r := cfg.HeroCards[i]
			cardImg := vision.GenerateSyntheticCard(heroCards[i], int(r.Width*1000), int(r.Height*800))
			minX := int(r.X * 1000)
			minY := int(r.Y * 800)
			draw.Draw(img, image.Rect(minX, minY, minX+int(r.Width*1000), minY+int(r.Height*800)), cardImg, image.Point{}, draw.Src)
		}
	}

	// Draw Board Cards
	for i := 0; i < len(board) && i < len(cfg.CommunityCards); i++ {
		r := cfg.CommunityCards[i]
		cardImg := vision.GenerateSyntheticCard(board[i], int(r.Width*1000), int(r.Height*800))
		minX := int(r.X * 1000)
		minY := int(r.Y * 800)
		draw.Draw(img, image.Rect(minX, minY, minX+int(r.Width*1000), minY+int(r.Height*800)), cardImg, image.Point{}, draw.Src)
	}

	return img
}

func TestLiveAgent_NewAndDefaults(t *testing.T) {
	srv, _, _, _ := setupTestServer(t)
	grabber := NewMockGrabber(image.NewRGBA(image.Rect(0, 0, 800, 600)))

	cfg := LiveAgentConfig{
		WindowQuery: "CoinPoker",
	}

	agent := NewLiveAgent(grabber, srv, cfg)
	if agent == nil {
		t.Fatal("expected non-nil LiveAgent")
	}

	if agent.GetFPS() != 3 {
		t.Errorf("expected default FPS 3, got %d", agent.GetFPS())
	}
	if agent.GetTableID() != "table-1" {
		t.Errorf("expected default TableID 'table-1', got %s", agent.GetTableID())
	}
	if agent.GetHeroID() != "player-0" {
		t.Errorf("expected default HeroID 'player-0', got %s", agent.GetHeroID())
	}
	if agent.IsRunning() {
		t.Errorf("expected agent not running initially")
	}
	if agent.GetLastFrame() != nil {
		t.Errorf("expected nil initial last frame")
	}
	if agent.GetLastState() != nil {
		t.Errorf("expected nil initial last state")
	}

	roi := agent.GetROIConfig()
	if len(roi.Seats) != 6 || len(roi.HeroCards) != 2 || len(roi.CommunityCards) != 5 {
		t.Errorf("expected default 6-max ROIConfig, got %+v", roi)
	}
}

func TestLiveAgent_SettersAndGetters(t *testing.T) {
	srv, _, _, _ := setupTestServer(t)
	grabber := NewMockGrabber(image.NewRGBA(image.Rect(0, 0, 800, 600)))

	agent := NewLiveAgent(grabber, srv, LiveAgentConfig{
		FPS:         5,
		TableID:     "custom-table",
		HeroID:      "hero-custom",
		WindowQuery: "CoinPoker",
	})

	// 1. Target Window getter/setter
	if agent.GetTargetWindow() != nil {
		t.Errorf("expected initial target window to be nil")
	}

	target := WindowInfo{
		ID:         999,
		Title:      "CoinPoker - Table 1",
		OwnerName:  "CoinPoker",
		Bounds:     image.Rect(10, 20, 810, 620),
		IsOnScreen: true,
	}
	agent.SetTargetWindow(&target)

	gotWin := agent.GetTargetWindow()
	if gotWin == nil || gotWin.ID != 999 || gotWin.Title != "CoinPoker - Table 1" {
		t.Errorf("unexpected target window returned: %+v", gotWin)
	}

	// Verify deep copy / isolation
	target.ID = 1000
	if agent.GetTargetWindow().ID != 999 {
		t.Errorf("expected isolated copy of target window")
	}

	agent.SetTargetWindow(nil)
	if agent.GetTargetWindow() != nil {
		t.Errorf("expected nil target window after SetTargetWindow(nil)")
	}

	// 2. ROI Config setter
	customROI := vision.ROIConfig{
		Seats: []vision.SeatROI{
			{SeatNumber: 0, IsHero: true},
		},
	}
	agent.SetROIConfig(customROI)
	if len(agent.GetROIConfig().Seats) != 1 {
		t.Errorf("expected custom ROIConfig with 1 seat, got: %+v", agent.GetROIConfig())
	}
}

func TestLiveAgent_Step_FrameIngestionAndDiffer(t *testing.T) {
	srv, cache, _, _ := setupTestServer(t)
	roiCfg := vision.DefaultCoinPoker6MaxROI()

	c1 := table.Card{Rank: table.RankAce, Suit: table.Spades}
	c2 := table.Card{Rank: table.RankKing, Suit: table.Hearts}
	b1 := table.Card{Rank: table.RankAce, Suit: table.Hearts}
	b2 := table.Card{Rank: table.RankKing, Suit: table.Diamonds}
	b3 := table.Card{Rank: table.RankTwo, Suit: table.Clubs}

	frameImg := createSyntheticTableImage([2]table.Card{c1, c2}, []table.Card{b1, b2, b3}, 50.0, roiCfg)
	grabber := NewMockGrabber(frameImg)

	agent := NewLiveAgent(grabber, srv, LiveAgentConfig{
		TableID:     "live-table-123",
		HeroID:      "player-0",
		ROIConfig:   roiCfg,
		WindowQuery: "CoinPoker",
	})

	ctx := context.Background()
	if err := agent.Step(ctx); err != nil {
		t.Fatalf("Step failed: %v", err)
	}

	// Verify last frame and last state
	lastFrame := agent.GetLastFrame()
	if lastFrame == nil || lastFrame.Bounds().Dx() != 1000 {
		t.Fatalf("expected last frame 1000x800, got: %v", lastFrame)
	}

	lastState := agent.GetLastState()
	if lastState == nil {
		t.Fatalf("expected non-nil last state")
	}
	if lastState.TableID != "live-table-123" {
		t.Errorf("expected tableID 'live-table-123', got %s", lastState.TableID)
	}
	if lastState.Street != table.StreetFlop {
		t.Errorf("expected StreetFlop, got %s", lastState.Street)
	}
	if len(lastState.CommunityCards) != 3 {
		t.Errorf("expected 3 community cards, got %d", len(lastState.CommunityCards))
	}

	// Verify table state ingested into server cache
	cached := cache.GetTableState("live-table-123")
	if cached == nil {
		t.Fatalf("expected table state in server cache, got nil")
	}
	if cached.HeroCards[0] != c1 || cached.HeroCards[1] != c2 {
		t.Errorf("cached hero cards mismatch: [%s, %s]", cached.HeroCards[0], cached.HeroCards[1])
	}
}

func TestLiveAgent_ContinuousLoop_StartStop(t *testing.T) {
	srv, cache, _, _ := setupTestServer(t)
	roiCfg := vision.DefaultCoinPoker6MaxROI()

	c1 := table.Card{Rank: table.RankQueen, Suit: table.Spades}
	c2 := table.Card{Rank: table.RankQueen, Suit: table.Hearts}
	frame1 := createSyntheticTableImage([2]table.Card{c1, c2}, []table.Card{}, 10.0, roiCfg)

	grabber := NewMockGrabber(frame1)

	agent := NewLiveAgent(grabber, srv, LiveAgentConfig{
		FPS:         20, // Fast ticker for test
		TableID:     "loop-table-999",
		HeroID:      "player-0",
		ROIConfig:   roiCfg,
		WindowQuery: "CoinPoker",
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := agent.Start(ctx); err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	if !agent.IsRunning() {
		t.Errorf("expected agent to be running")
	}

	// Test starting twice returns error
	if err := agent.Start(ctx); err == nil {
		t.Errorf("expected error when starting already running agent")
	}

	// Wait for at least one frame iteration
	time.Sleep(100 * time.Millisecond)

	cached := cache.GetTableState("loop-table-999")
	if cached == nil {
		t.Fatalf("expected table state cached from continuous loop")
	}
	if cached.HeroCards[0] != c1 || cached.HeroCards[1] != c2 {
		t.Errorf("cached hero cards mismatch: [%s, %s]", cached.HeroCards[0], cached.HeroCards[1])
	}

	// Update frame in mock grabber with Flop
	f1 := table.Card{Rank: table.RankKing, Suit: table.Diamonds}
	f2 := table.Card{Rank: table.RankNine, Suit: table.Clubs}
	f3 := table.Card{Rank: table.RankTwo, Suit: table.Hearts}
	frame2 := createSyntheticTableImage([2]table.Card{c1, c2}, []table.Card{f1, f2, f3}, 30.0, roiCfg)
	grabber.SetFrame(frame2)

	// Wait for next ticker cycle
	time.Sleep(100 * time.Millisecond)

	cachedFlop := cache.GetTableState("loop-table-999")
	if cachedFlop == nil || len(cachedFlop.CommunityCards) != 3 {
		t.Errorf("expected updated flop state with 3 community cards in cache")
	}

	// Graceful Stop
	agent.Stop()
	if agent.IsRunning() {
		t.Errorf("expected agent to be stopped")
	}

	// Stop idempotent
	agent.Stop()
}

func TestLiveAgent_HandLifecycleSequence(t *testing.T) {
	srv, _, db, prof := setupTestServer(t)
	roiCfg := vision.DefaultCoinPoker6MaxROI()

	c1 := table.Card{Rank: table.RankAce, Suit: table.Spades}
	c2 := table.Card{Rank: table.RankKing, Suit: table.Spades}
	b1 := table.Card{Rank: table.RankTen, Suit: table.Spades}
	b2 := table.Card{Rank: table.RankJack, Suit: table.Spades}
	b3 := table.Card{Rank: table.RankQueen, Suit: table.Spades}
	b4 := table.Card{Rank: table.RankTwo, Suit: table.Hearts}
	b5 := table.Card{Rank: table.RankThree, Suit: table.Clubs}

	grabber := NewMockGrabber(createSyntheticTableImage([2]table.Card{c1, c2}, []table.Card{}, 10.0, roiCfg))

	agent := NewLiveAgent(grabber, srv, LiveAgentConfig{
		TableID:     "lifecycle-table",
		HeroID:      "player-0",
		ROIConfig:   roiCfg,
		WindowQuery: "CoinPoker",
	})

	ctx := context.Background()

	// 1. Preflop
	if err := agent.Step(ctx); err != nil {
		t.Fatalf("Preflop Step failed: %v", err)
	}
	if agent.GetLastState().Street != table.StreetPreflop {
		t.Errorf("expected StreetPreflop, got %s", agent.GetLastState().Street)
	}

	// 2. Flop
	grabber.SetFrame(createSyntheticTableImage([2]table.Card{c1, c2}, []table.Card{b1, b2, b3}, 30.0, roiCfg))
	if err := agent.Step(ctx); err != nil {
		t.Fatalf("Flop Step failed: %v", err)
	}
	if agent.GetLastState().Street != table.StreetFlop {
		t.Errorf("expected StreetFlop, got %s", agent.GetLastState().Street)
	}

	// 3. Turn
	grabber.SetFrame(createSyntheticTableImage([2]table.Card{c1, c2}, []table.Card{b1, b2, b3, b4}, 60.0, roiCfg))
	if err := agent.Step(ctx); err != nil {
		t.Fatalf("Turn Step failed: %v", err)
	}
	if agent.GetLastState().Street != table.StreetTurn {
		t.Errorf("expected StreetTurn, got %s", agent.GetLastState().Street)
	}

	// 4. River
	grabber.SetFrame(createSyntheticTableImage([2]table.Card{c1, c2}, []table.Card{b1, b2, b3, b4, b5}, 100.0, roiCfg))
	if err := agent.Step(ctx); err != nil {
		t.Fatalf("River Step failed: %v", err)
	}
	if agent.GetLastState().Street != table.StreetRiver {
		t.Errorf("expected StreetRiver, got %s", agent.GetLastState().Street)
	}

	// 5. Showdown / Hand End
	endState := *agent.GetLastState()
	endState.Street = table.StreetShowdown
	endEv := vision.VisionEvent{
		Type:      vision.EventHandEnd,
		TableID:   "lifecycle-table",
		HandState: &endState,
	}
	_, err := srv.ProcessEvent(endEv)
	if err != nil {
		t.Fatalf("ProcessEvent HandEnd failed: %v", err)
	}

	saved, err := db.GetHandHistory(endState.HandID)
	if err != nil || saved == nil {
		t.Fatalf("expected hand history saved in DB for %s: %v", endState.HandID, err)
	}
	if stats := prof.GetStats("player-0"); stats == nil || stats.HandsCount != 1 {
		t.Errorf("expected profiler stats recorded for player-0: %+v", stats)
	}
}

func TestLiveAgent_ErrorHandling(t *testing.T) {
	srv, _, _, _ := setupTestServer(t)

	// 1. Nil grabber Step
	agentNilGrabber := NewLiveAgent(nil, srv, LiveAgentConfig{})
	if err := agentNilGrabber.Step(context.Background()); err == nil {
		t.Errorf("expected error on Step with nil grabber")
	}

	// 2. Mock grabber with nil frame
	grabber := NewMockGrabber(nil)
	agent := NewLiveAgent(grabber, srv, LiveAgentConfig{})
	if err := agent.Step(context.Background()); err == nil {
		t.Errorf("expected error on Step when grabber returns error / nil frame")
	}

	// 3. Context cancelled during Start
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately
	if err := agent.Start(ctx); err != nil {
		t.Fatalf("Start returned unexpected error: %v", err)
	}
	time.Sleep(20 * time.Millisecond)
	// Agent should exit loop due to cancelled context
	agent.Stop()
	if agent.IsRunning() {
		t.Errorf("expected agent not running after context cancel and stop")
	}
}
