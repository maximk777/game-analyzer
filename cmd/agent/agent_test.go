package main

import (
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"poker-game-analyzer/pkg/capture"
	"poker-game-analyzer/pkg/server"
	"poker-game-analyzer/pkg/table"
	"poker-game-analyzer/pkg/vision"
)

// getFreePort asks the kernel for a free open TCP port.
func getFreePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to find free port: %v", err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

// createSyntheticTableImage generates a 1000x800 test image with hero and community cards.
func createSyntheticTableImage(heroCards [2]table.Card, board []table.Card, potVal float64, cfg vision.ROIConfig) image.Image {
	img := image.NewRGBA(image.Rect(0, 0, 1000, 800))

	// Green felt background
	draw.Draw(img, img.Bounds(), &image.Uniform{C: color.RGBA{R: 25, G: 75, B: 35, A: 255}}, image.Point{}, draw.Src)

	// Draw Hero Cards
	for i := 0; i < 2 && i < len(cfg.HeroCards); i++ {
		if heroCards[i].Rank > 0 {
			r := cfg.HeroCards[i]
			cardImg := vision.GenerateSyntheticCard(heroCards[i], int(r.Width*1000), int(r.Height*800))
			minX := int(r.X * 1000)
			minY := int(r.Y * 800)
			draw.Draw(img, image.Rect(minX, minY, minX+int(r.Width*1000), minY+int(r.Height*800)), cardImg, image.Point{}, draw.Src)
		}
	}

	// Draw Community Cards
	for i := 0; i < len(board) && i < len(cfg.CommunityCards); i++ {
		r := cfg.CommunityCards[i]
		cardImg := vision.GenerateSyntheticCard(board[i], int(r.Width*1000), int(r.Height*800))
		minX := int(r.X * 1000)
		minY := int(r.Y * 800)
		draw.Draw(img, image.Rect(minX, minY, minX+int(r.Width*1000), minY+int(r.Height*800)), cardImg, image.Point{}, draw.Src)
	}

	return img
}

func TestAgent_EndToEndIntegration(t *testing.T) {
	roiCfg := vision.DefaultCoinPoker6MaxROI()

	c1 := table.Card{Rank: table.RankAce, Suit: table.Spades}
	c2 := table.Card{Rank: table.RankKing, Suit: table.Hearts}
	preflopFrame := createSyntheticTableImage([2]table.Card{c1, c2}, []table.Card{}, 10.0, roiCfg)

	mockGrabber := capture.NewMockGrabber(preflopFrame)
	port := getFreePort(t)
	tableID := "test-agent-live-table"

	cfg := Config{
		WindowQuery: "CoinPoker",
		Port:        port,
		FPS:         20,
		DBPath:      ":memory:",
		OpenHUD:     false,
		TableID:     tableID,
		HeroID:      "Hero",
		MockLLM:     true,
		WebDir:      "../../web",
	}

	app, err := NewAgentApp(cfg, mockGrabber)
	if err != nil {
		t.Fatalf("NewAgentApp failed: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := app.Start(ctx); err != nil {
		t.Fatalf("app.Start failed: %v", err)
	}

	if !app.LiveAgent().IsRunning() {
		t.Fatalf("expected LiveAgent to be running")
	}

	// 1. Connect WebSocket client
	wsURL := fmt.Sprintf("ws://127.0.0.1:%d/ws/tables/%s", port, tableID)
	wsConn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("failed to dial websocket at %s: %v", wsURL, err)
	}
	defer wsConn.Close()

	var (
		mu             sync.Mutex
		receivedStates int
		receivedRecs   int
		receivedEvents int
	)

	wsCtx, wsCancel := context.WithCancel(context.Background())
	defer wsCancel()

	go func() {
		for {
			select {
			case <-wsCtx.Done():
				return
			default:
				_ = wsConn.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
				_, msgData, err := wsConn.ReadMessage()
				if err != nil {
					return
				}

				var wsMsg server.WSMessage
				if err := json.Unmarshal(msgData, &wsMsg); err == nil {
					mu.Lock()
					switch wsMsg.Type {
					case server.WSMsgStateUpdate:
						receivedStates++
					case server.WSMsgRecommendation:
						receivedRecs++
					case server.WSMsgEvent:
						receivedEvents++
					}
					mu.Unlock()
				}
			}
		}
	}()

	// 2. Poll for initial preflop frame ingestion
	pollDeadline := time.Now().Add(3 * time.Second)
	var cachedPreflop *table.HandState
	for time.Now().Before(pollDeadline) {
		s := app.Cache().GetTableState(tableID)
		if s != nil && s.HeroCards[0] == c1 && s.HeroCards[1] == c2 {
			cachedPreflop = s
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	if cachedPreflop == nil {
		t.Fatalf("timed out waiting for cached preflop table state for %s", tableID)
	}

	// 3. Update frame to Flop
	b1 := table.Card{Rank: table.RankAce, Suit: table.Hearts}
	b2 := table.Card{Rank: table.RankKing, Suit: table.Diamonds}
	b3 := table.Card{Rank: table.RankTwo, Suit: table.Clubs}
	flopFrame := createSyntheticTableImage([2]table.Card{c1, c2}, []table.Card{b1, b2, b3}, 40.0, roiCfg)
	mockGrabber.SetFrame(flopFrame)

	// Poll for flop update in cache
	pollFlopDeadline := time.Now().Add(3 * time.Second)
	var cachedFlop *table.HandState
	for time.Now().Before(pollFlopDeadline) {
		s := app.Cache().GetTableState(tableID)
		if s != nil && len(s.CommunityCards) == 3 {
			cachedFlop = s
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	if cachedFlop == nil {
		t.Fatalf("timed out waiting for 3 community cards in cache on flop")
	}

	// 4. Verify WebSocket messages were broadcasted
	pollWSDuration := time.Now().Add(3 * time.Second)
	for time.Now().Before(pollWSDuration) {
		mu.Lock()
		st := receivedStates
		rc := receivedRecs
		mu.Unlock()
		if st >= 1 && rc >= 1 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	mu.Lock()
	states := receivedStates
	recs := receivedRecs
	mu.Unlock()

	if states == 0 {
		t.Errorf("expected at least 1 WS state update message, got 0")
	}
	if recs == 0 {
		t.Errorf("expected at least 1 WS recommendation message, got 0")
	}

	// 5. Simulate Hand End and verify DB persistence & Profiler stats
	handEndState := *cachedFlop
	handEndState.Street = table.StreetShowdown
	handEndState.HandID = "live-hand-end-123"
	handEndState.Seats = []table.SeatState{
		{SeatNumber: 0, PlayerID: "Hero", PlayerName: "Hero", Stack: 250, IsActive: true},
		{SeatNumber: 1, PlayerID: "Villain-1", PlayerName: "Villain", Stack: 150, IsActive: true},
	}

	endEv := vision.VisionEvent{
		Type:        vision.EventHandEnd,
		TableID:     tableID,
		HandState:   &handEndState,
		Description: "Hand completed at showdown",
	}

	if _, err := app.Server().ProcessEvent(endEv); err != nil {
		t.Fatalf("ProcessEvent HandEnd failed: %v", err)
	}

	app.Profiler().Flush()

	// Verify persistence in SQLite
	savedHand, err := app.DB().GetHandHistory("live-hand-end-123")
	if err != nil {
		t.Fatalf("error querying DB for hand history: %v", err)
	}
	if savedHand == nil || savedHand.HandID != "live-hand-end-123" {
		t.Errorf("expected hand saved in SQLite DB, got %+v", savedHand)
	}

	// 6. Graceful Stop
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer shutdownCancel()

	if err := app.Stop(shutdownCtx); err != nil {
		t.Fatalf("app.Stop failed: %v", err)
	}

	if app.LiveAgent().IsRunning() {
		t.Errorf("expected LiveAgent to be stopped")
	}
}

func TestAgent_DefaultsAndAccessors(t *testing.T) {
	app, err := NewAgentApp(Config{
		DBPath: ":memory:",
	}, nil)
	if err != nil {
		t.Fatalf("NewAgentApp failed: %v", err)
	}
	defer func() {
		_ = app.Stop(context.Background())
	}()

	cfg := app.Config()
	if cfg.Port != 8080 {
		t.Errorf("expected default port 8080, got %d", cfg.Port)
	}
	if cfg.FPS != 3 {
		t.Errorf("expected default FPS 3, got %d", cfg.FPS)
	}
	if cfg.TableID != "coinpoker-live" {
		t.Errorf("expected default TableID 'coinpoker-live', got %s", cfg.TableID)
	}
	if cfg.HeroID != "Hero" {
		t.Errorf("expected default HeroID 'Hero', got %s", cfg.HeroID)
	}
	if cfg.WindowQuery != "CoinPoker" {
		t.Errorf("expected default WindowQuery 'CoinPoker', got %s", cfg.WindowQuery)
	}

	if app.DB() == nil {
		t.Errorf("expected non-nil DB")
	}
	if app.Cache() == nil {
		t.Errorf("expected non-nil Cache")
	}
	if app.Server() == nil {
		t.Errorf("expected non-nil Server")
	}
	if app.LiveAgent() == nil {
		t.Errorf("expected non-nil LiveAgent")
	}
	if app.Profiler() == nil {
		t.Errorf("expected non-nil Profiler")
	}
	if app.Errors() == nil {
		t.Errorf("expected non-nil Errors channel")
	}
}

func TestAgent_StaticEndpoints(t *testing.T) {
	port := getFreePort(t)
	app, err := NewAgentApp(Config{
		Port:    port,
		DBPath:  ":memory:",
		WebDir:  "../../web",
		TableID: "test-static-tbl",
	}, capture.NewMockGrabber(image.NewRGBA(image.Rect(0, 0, 100, 100))))
	if err != nil {
		t.Fatalf("NewAgentApp failed: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := app.Start(ctx); err != nil {
		t.Fatalf("app.Start failed: %v", err)
	}
	defer func() {
		_ = app.Stop(context.Background())
	}()

	baseURL := fmt.Sprintf("http://127.0.0.1:%d", port)
	client := &http.Client{Timeout: 2 * time.Second}

	// 1. GET /hud.html
	resp, err := client.Get(baseURL + "/hud.html")
	if err != nil {
		t.Fatalf("GET /hud.html failed: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200 OK for /hud.html, got %d", resp.StatusCode)
	}
	if !strings.Contains(string(body), "hud-widget") {
		t.Errorf("expected hud.html to contain 'hud-widget'")
	}

	// 2. GET /calibrate.html
	respCal, err := client.Get(baseURL + "/calibrate.html")
	if err != nil {
		t.Fatalf("GET /calibrate.html failed: %v", err)
	}
	bodyCal, _ := io.ReadAll(respCal.Body)
	respCal.Body.Close()

	if respCal.StatusCode != http.StatusOK {
		t.Errorf("expected 200 OK for /calibrate.html, got %d", respCal.StatusCode)
	}
	if !strings.Contains(string(bodyCal), "ROI Calibrator") {
		t.Errorf("expected calibrate.html to contain 'ROI Calibrator'")
	}

	// 3. GET /api/v1/roi
	respROI, err := client.Get(baseURL + "/api/v1/roi")
	if err != nil {
		t.Fatalf("GET /api/v1/roi failed: %v", err)
	}
	if respROI.StatusCode != http.StatusOK {
		t.Errorf("expected 200 OK for /api/v1/roi, got %d", respROI.StatusCode)
	}
	respROI.Body.Close()

	// 4. GET /api/v1/windows
	respWin, err := client.Get(baseURL + "/api/v1/windows")
	if err != nil {
		t.Fatalf("GET /api/v1/windows failed: %v", err)
	}
	if respWin.StatusCode != http.StatusOK && respWin.StatusCode != http.StatusInternalServerError {
		t.Errorf("unexpected status for /api/v1/windows: %d", respWin.StatusCode)
	}
	respWin.Body.Close()
}

func TestLaunchBrowserHUD_Helper(t *testing.T) {
	// Call helper with a dummy URL - should not panic
	_ = LaunchBrowserHUD("http://127.0.0.1:65530/hud.html")
}
