package server

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"poker-game-analyzer/pkg/profiler"
	"poker-game-analyzer/pkg/storage"
	"poker-game-analyzer/pkg/table"
	"poker-game-analyzer/pkg/vision"
)

func TestWebSocket_ConnectAndInitialState(t *testing.T) {
	cache := storage.NewMemoryCache()
	db, err := storage.NewSQLiteDB(":memory:")
	if err != nil {
		t.Fatalf("sqlite open error: %v", err)
	}
	defer db.Close()

	prof := profiler.NewProfiler(cache, db, nil)
	defer prof.Close()

	srv := NewServer(cache, db, prof)
	ts := httptest.NewServer(srv.Router())
	defer ts.Close()

	c1, _ := table.ParseCard("As")
	c2, _ := table.ParseCard("Kd")
	initialState := &table.HandState{
		HandID:    "hand-init-ws",
		TableID:   "table-ws-1",
		Street:    table.StreetPreflop,
		Pot:       15.0,
		HeroID:    "hero-1",
		HeroCards: [2]table.Card{c1, c2},
		Seats: []table.SeatState{
			{SeatNumber: 1, PlayerID: "hero-1", PlayerName: "Hero", Stack: 100, IsActive: true},
		},
	}
	cache.SetTableState("table-ws-1", initialState)

	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/ws/tables/table-ws-1"

	conn, resp, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("failed to dial websocket: %v (resp: %+v)", err, resp)
	}
	defer conn.Close()

	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, msgBytes, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("failed to read initial message from ws: %v", err)
	}

	var wsMsg WSMessage
	if err := json.Unmarshal(msgBytes, &wsMsg); err != nil {
		t.Fatalf("failed to parse ws message: %v", err)
	}

	if wsMsg.Type != WSMsgStateUpdate {
		t.Errorf("expected msg type %s, got %s", WSMsgStateUpdate, wsMsg.Type)
	}
	if wsMsg.TableID != "table-ws-1" {
		t.Errorf("expected table_id table-ws-1, got %s", wsMsg.TableID)
	}
}

func TestWebSocket_EventBroadcastAndTableIsolation(t *testing.T) {
	cache := storage.NewMemoryCache()
	db, _ := storage.NewSQLiteDB(":memory:")
	defer db.Close()
	prof := profiler.NewProfiler(cache, db, nil)
	defer prof.Close()

	srv := NewServer(cache, db, prof)
	ts := httptest.NewServer(srv.Router())
	defer ts.Close()

	baseURL := "ws" + strings.TrimPrefix(ts.URL, "http")

	// Client 1 connected to table A
	connA1, _, err := websocket.DefaultDialer.Dial(baseURL+"/ws/tables/table-A", nil)
	if err != nil {
		t.Fatalf("dial error A1: %v", err)
	}
	defer connA1.Close()

	// Client 2 connected to table A
	connA2, _, err := websocket.DefaultDialer.Dial(baseURL+"/ws/tables/table-A", nil)
	if err != nil {
		t.Fatalf("dial error A2: %v", err)
	}
	defer connA2.Close()

	// Client 3 connected to table B
	connB, _, err := websocket.DefaultDialer.Dial(baseURL+"/ws/tables/table-B", nil)
	if err != nil {
		t.Fatalf("dial error B: %v", err)
	}
	defer connB.Close()

	// Allow connection registration
	time.Sleep(50 * time.Millisecond)

	c1, _ := table.ParseCard("Ah")
	c2, _ := table.ParseCard("Kh")
	b1, _ := table.ParseCard("Qh")
	b2, _ := table.ParseCard("Jh")
	b3, _ := table.ParseCard("Th") // Royal Flush!

	handStateA := &table.HandState{
		HandID:         "hand-rf",
		TableID:        "table-A",
		Street:         table.StreetFlop,
		Pot:            200.0,
		CurrentBet:     50.0,
		HeroID:         "hero-1",
		HeroCards:      [2]table.Card{c1, c2},
		CommunityCards: []table.Card{b1, b2, b3},
		Seats: []table.SeatState{
			{SeatNumber: 1, PlayerID: "hero-1", PlayerName: "Hero", Stack: 500, IsActive: true},
			{SeatNumber: 2, PlayerID: "villain-1", PlayerName: "Villain", Stack: 500, CurrentBet: 50, IsActive: true},
		},
	}

	eventA := vision.VisionEvent{
		Type:      vision.EventHeroTurn,
		TableID:   "table-A",
		HandState: handStateA,
	}

	// Ingest event for table A
	_, err = srv.ProcessEvent(eventA)
	if err != nil {
		t.Fatalf("ProcessEvent error: %v", err)
	}

	// Verify both A1 and A2 receive recommendations/updates
	var wg sync.WaitGroup
	wg.Add(2)

	verifyClient := func(conn *websocket.Conn, name string) {
		defer wg.Done()
		_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))

		receivedRecommendation := false
		for i := 0; i < 3; i++ {
			_, msgBytes, err := conn.ReadMessage()
			if err != nil {
				break
			}
			var msg WSMessage
			if err := json.Unmarshal(msgBytes, &msg); err == nil {
				if msg.Type == WSMsgRecommendation && msg.TableID == "table-A" {
					receivedRecommendation = true
					break
				}
			}
		}

		if !receivedRecommendation {
			t.Errorf("[%s] did not receive recommendation message for table-A", name)
		}
	}

	go verifyClient(connA1, "connA1")
	go verifyClient(connA2, "connA2")
	wg.Wait()

	// Verify Client B on table-B receives NOTHING from table A
	_ = connB.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
	_, _, errB := connB.ReadMessage()
	if errB == nil {
		t.Errorf("expected timeout on connB, but received message from other table")
	}
}

func TestWebSocket_PingPongHeartbeat(t *testing.T) {
	cache := storage.NewMemoryCache()
	db, _ := storage.NewSQLiteDB(":memory:")
	defer db.Close()
	prof := profiler.NewProfiler(cache, db, nil)
	defer prof.Close()

	srv := NewServer(cache, db, prof)
	ts := httptest.NewServer(srv.Router())
	defer ts.Close()

	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/ws/tables/table-ping"
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial error: %v", err)
	}
	defer conn.Close()

	pongReceived := make(chan string, 1)
	conn.SetPongHandler(func(appData string) error {
		pongReceived <- appData
		return nil
	})

	if err := conn.WriteControl(websocket.PingMessage, []byte("hello-heartbeat"), time.Now().Add(1*time.Second)); err != nil {
		t.Fatalf("failed to write ping: %v", err)
	}

	// gorilla connection needs a read to process control frames
	go func() {
		_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}()

	select {
	case data := <-pongReceived:
		if data != "hello-heartbeat" {
			t.Errorf("expected pong payload 'hello-heartbeat', got %q", data)
		}
	case <-time.After(2 * time.Second):
		t.Errorf("timed out waiting for pong response")
	}
}

func TestWebSocket_BroadcastPerformance(t *testing.T) {
	cache := storage.NewMemoryCache()
	db, _ := storage.NewSQLiteDB(":memory:")
	defer db.Close()
	prof := profiler.NewProfiler(cache, db, nil)
	defer prof.Close()

	srv := NewServer(cache, db, prof)
	ts := httptest.NewServer(srv.Router())
	defer ts.Close()

	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/ws/tables/table-perf"
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial error: %v", err)
	}
	defer conn.Close()

	time.Sleep(30 * time.Millisecond)

	c1, _ := table.ParseCard("As")
	c2, _ := table.ParseCard("Ad")
	b1, _ := table.ParseCard("Ks")
	b2, _ := table.ParseCard("7h")
	b3, _ := table.ParseCard("2c")

	state := &table.HandState{
		HandID:         "hand-perf-1",
		TableID:        "table-perf",
		Street:         table.StreetFlop,
		Pot:            50.0,
		CurrentBet:     10.0,
		HeroID:         "hero-1",
		HeroCards:      [2]table.Card{c1, c2},
		CommunityCards: []table.Card{b1, b2, b3},
		Seats: []table.SeatState{
			{SeatNumber: 1, PlayerID: "hero-1", PlayerName: "Hero", Stack: 200, IsActive: true},
			{SeatNumber: 2, PlayerID: "villain-1", PlayerName: "Villain", Stack: 200, CurrentBet: 10, IsActive: true},
		},
	}

	event := vision.VisionEvent{
		Type:      vision.EventHeroTurn,
		TableID:   "table-perf",
		HandState: state,
	}

	start := time.Now()
	_, err = srv.ProcessEvent(event)
	if err != nil {
		t.Fatalf("ProcessEvent error: %v", err)
	}

	_ = conn.SetReadDeadline(time.Now().Add(1 * time.Second))
	for {
		_, msgBytes, err := conn.ReadMessage()
		if err != nil {
			t.Fatalf("read error during perf test: %v", err)
		}
		var msg WSMessage
		if err := json.Unmarshal(msgBytes, &msg); err == nil && msg.Type == WSMsgRecommendation {
			break
		}
	}
	elapsed := time.Since(start)

	t.Logf("Total event processing + Monte Carlo + Advisor + WS delivery time: %v", elapsed)
	if elapsed > 100*time.Millisecond {
		t.Errorf("processing took longer than expected: %v", elapsed)
	}
}

func TestWebSocket_MultiClientBroadcastAndCounts(t *testing.T) {
	cache := storage.NewMemoryCache()
	db, _ := storage.NewSQLiteDB(":memory:")
	defer db.Close()
	prof := profiler.NewProfiler(cache, db, nil)
	defer prof.Close()

	srv := NewServer(cache, db, prof)
	ts := httptest.NewServer(srv.Router())
	defer ts.Close()

	baseURL := "ws" + strings.TrimPrefix(ts.URL, "http")

	conns := make([]*websocket.Conn, 5)
	for i := 0; i < 5; i++ {
		c, _, err := websocket.DefaultDialer.Dial(baseURL+"/ws/tables/tbl-multi", nil)
		if err != nil {
			t.Fatalf("dial error client %d: %v", i, err)
		}
		conns[i] = c
		defer c.Close()
	}

	time.Sleep(50 * time.Millisecond)

	if count := srv.Hub().ClientCount("tbl-multi"); count != 5 {
		t.Errorf("expected 5 clients on tbl-multi, got %d", count)
	}
	if total := srv.Hub().TotalClientCount(); total != 5 {
		t.Errorf("expected 5 total clients, got %d", total)
	}

	// Test BroadcastAll
	srv.Hub().BroadcastAll(WSMessage{
		Type:    WSMsgEvent,
		TableID: "system",
		Payload: "broadcast-all-test",
	})

	for i, c := range conns {
		_ = c.SetReadDeadline(time.Now().Add(1 * time.Second))
		_, msgBytes, err := c.ReadMessage()
		if err != nil {
			t.Fatalf("client %d read error: %v", i, err)
		}
		var msg WSMessage
		if err := json.Unmarshal(msgBytes, &msg); err != nil {
			t.Fatalf("client %d unmarshal error: %v", i, err)
		}
		if msg.Payload != "broadcast-all-test" {
			t.Errorf("client %d unexpected payload: %+v", i, msg.Payload)
		}
	}

	// Close two clients
	_ = conns[0].Close()
	_ = conns[1].Close()
	time.Sleep(50 * time.Millisecond)

	// Verify unregister
	srv.Hub().Unregister(NewWSClient(srv.Hub(), conns[0], "tbl-multi"))
}

func TestWebSocket_HubClose(t *testing.T) {
	hub := NewWSHub()
	if hub.TotalClientCount() != 0 {
		t.Errorf("expected 0 clients in new hub")
	}

	hub.Close()
	// Idempotent close
	hub.Close()

	// Broadcast on closed hub should not panic
	hub.BroadcastToTable("tbl-closed", WSMessage{Type: WSMsgPing})
	hub.BroadcastAll(WSMessage{Type: WSMsgPing})
}

