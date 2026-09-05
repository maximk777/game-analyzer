package main

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"poker-game-analyzer/pkg/llm"
	"poker-game-analyzer/pkg/profiler"
	"poker-game-analyzer/pkg/server"
	"poker-game-analyzer/pkg/storage"
)

func TestSimulator_EndToEndIntegration(t *testing.T) {
	// 1. Setup in-memory dependencies
	cache := storage.NewMemoryCache()
	db, err := storage.NewSQLiteDB(":memory:")
	if err != nil {
		t.Fatalf("failed to create sqlite in-memory db: %v", err)
	}
	defer db.Close()

	mockLLM := llm.NewMockClient()
	prof := profiler.NewProfiler(cache, db, mockLLM)
	defer prof.Close()

	srv := server.NewServer(cache, db, prof)
	ts := httptest.NewServer(srv.Router())
	defer ts.Close()

	tableID := "test-table-sim"

	// 2. Connect test WebSocket subscriber to verify real-time broadcasts
	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/ws/tables/" + tableID
	wsConn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("failed to connect websocket client: %v", err)
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

	// 3. Run Simulator for 3 hands at speedMS = 0 (instant execution)
	sim := NewSimulator(SimulatorConfig{
		ServerURL: ts.URL,
		TableID:   tableID,
		Hands:     3,
		SpeedMS:   0,
		HeroSeat:  0,
	})

	simCtx, simCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer simCancel()

	if err := sim.Run(simCtx); err != nil {
		t.Fatalf("simulator run failed: %v", err)
	}

	// Wait briefly for all goroutines and DB writes to complete
	time.Sleep(150 * time.Millisecond)
	prof.Flush()

	// 4. Verify in-memory cache has valid table state
	state := cache.GetTableState(tableID)
	if state == nil {
		t.Fatalf("expected cache to have state for table %s, got nil", tableID)
	}
	if state.TableID != tableID {
		t.Errorf("unexpected table ID in cache: %s", state.TableID)
	}

	// 5. Verify WebSocket messages received
	mu.Lock()
	statesCount := receivedStates
	recsCount := receivedRecs
	eventsCount := receivedEvents
	mu.Unlock()

	if statesCount == 0 {
		t.Errorf("expected to receive WS state_update messages, got 0")
	}
	if recsCount == 0 {
		t.Errorf("expected to receive WS recommendation messages, got 0")
	}
	if eventsCount == 0 {
		t.Errorf("expected to receive WS event messages, got 0")
	}

	t.Logf("Simulator End-to-End Success! WS stats: %d state updates, %d recommendations, %d events",
		statesCount, recsCount, eventsCount)

	// 6. Verify Player Stats were accumulated in Profiler and Database
	heroStats := prof.GetStats("player-0")
	if heroStats == nil || heroStats.HandsCount < 1 {
		t.Errorf("expected player-0 stats to be recorded with >=1 hand, got: %+v", heroStats)
	}

	villainStats, err := db.GetPlayerStats("player-1")
	if err != nil {
		t.Fatalf("error querying DB for player-1: %v", err)
	}
	if villainStats == nil || villainStats.HandsCount < 1 {
		t.Errorf("expected player-1 stats in DB with >=1 hand, got: %+v", villainStats)
	}
}
