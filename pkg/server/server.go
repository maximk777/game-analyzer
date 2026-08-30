package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"poker-game-analyzer/pkg/advisor"
	"poker-game-analyzer/pkg/equity"
	"poker-game-analyzer/pkg/profiler"
	"poker-game-analyzer/pkg/storage"
	"poker-game-analyzer/pkg/table"
	"poker-game-analyzer/pkg/vision"
)

// TableInitRequest is the request body for initializing a poker table.
type TableInitRequest struct {
	TableID  string            `json:"table_id"`
	HeroID   string            `json:"hero_id,omitempty"`
	Seats    []table.SeatState `json:"seats,omitempty"`
	Pot      float64           `json:"pot,omitempty"`
	MinRaise float64           `json:"min_raise,omitempty"`
}

// EventIngestResponse is the HTTP response for ingested vision/game events.
type EventIngestResponse struct {
	Status         string                    `json:"status"`
	Recommendation *advisor.AdvisorResponse `json:"recommendation,omitempty"`
	Event          *vision.VisionEvent       `json:"event,omitempty"`
}

// PlayerProfileResponse encapsulates player statistics and LLM behavioral analysis.
type PlayerProfileResponse struct {
	PlayerID   string               `json:"player_id"`
	Stats      *storage.PlayerStats `json:"stats,omitempty"`
	Profile    *storage.LLMProfile  `json:"profile,omitempty"`
	Tendencies map[string]float64   `json:"tendencies,omitempty"`
}

// Server serves REST API and WebSocket subscriptions for the poker game analyzer.
type Server struct {
	cache      *storage.MemoryCache
	db         *storage.SQLiteDB
	prof       *profiler.Profiler
	hub        *WSHub
	mux        *http.ServeMux
	httpServer *http.Server
	upgrader   websocket.Upgrader
	mu         sync.Mutex
}

// NewServer initializes and configures a new Server instance.
func NewServer(cache *storage.MemoryCache, db *storage.SQLiteDB, prof *profiler.Profiler) *Server {
	hub := NewWSHub()
	s := &Server{
		cache: cache,
		db:    db,
		prof:  prof,
		hub:   hub,
		mux:   http.NewServeMux(),
		upgrader: websocket.Upgrader{
			CheckOrigin: func(r *http.Request) bool {
				return true
			},
			ReadBufferSize:  1024,
			WriteBufferSize: 1024,
		},
	}

	s.routes()
	return s
}

func (s *Server) routes() {
	s.mux.HandleFunc("POST /api/v1/tables", s.handleInitTable)
	s.mux.HandleFunc("GET /api/v1/tables/{id}/state", s.handleGetTableState)
	s.mux.HandleFunc("POST /api/v1/tables/{id}/events", s.handleIngestEvent)
	s.mux.HandleFunc("GET /api/v1/players/{id}/profile", s.handleGetPlayerProfile)
	s.mux.HandleFunc("GET /ws/tables/{id}", s.handleWebSocket)
}

// Router returns the configured http.Handler for the server.
func (s *Server) Router() http.Handler {
	return s.mux
}

// MountStatic serves static assets from the specified local directory.
func (s *Server) MountStatic(dir string) {
	s.mux.Handle("GET /", http.FileServer(http.Dir(dir)))
}

// Hub returns the underlying WebSocket hub.
func (s *Server) Hub() *WSHub {
	return s.hub
}

// Start binds and starts the HTTP server on the given address.
func (s *Server) Start(addr string) error {
	s.mu.Lock()
	s.httpServer = &http.Server{
		Addr:              addr,
		Handler:           s.mux,
		ReadHeaderTimeout: 5 * time.Second,
	}
	s.mu.Unlock()

	return s.httpServer.ListenAndServe()
}

// Stop gracefully shuts down the HTTP server and closes all WebSocket connections.
func (s *Server) Stop(ctx context.Context) error {
	s.hub.Close()

	s.mu.Lock()
	srv := s.httpServer
	s.mu.Unlock()

	if srv != nil {
		return srv.Shutdown(ctx)
	}
	return nil
}

// ProcessEvent executes the end-to-end real-time analysis pipeline for an incoming event.
func (s *Server) ProcessEvent(event vision.VisionEvent) (*advisor.AdvisorResponse, error) {
	tableID := event.TableID
	if tableID == "" && event.HandState != nil {
		tableID = event.HandState.TableID
	}
	if tableID == "" {
		return nil, errors.New("missing table id in event")
	}

	// 1. Cache update
	if event.HandState != nil {
		if event.HandState.TableID == "" {
			event.HandState.TableID = tableID
		}
		if s.cache != nil {
			s.cache.SetTableState(tableID, event.HandState)
		}
	}

	// 2. Hand end persistence & profiler updates
	isHandEnd := event.Type == vision.EventHandEnd || (event.HandState != nil && event.HandState.Street == table.StreetShowdown)
	if isHandEnd && event.HandState != nil {
		if s.prof != nil {
			s.prof.ProcessHandEnd(*event.HandState)
		}
		if s.db != nil {
			_ = s.db.SaveHandHistory(*event.HandState)
		}
	}

	// 3. Real-time Equity and Advisor Recommendation calculation
	var rec *advisor.AdvisorResponse
	if event.HandState != nil && !isHandEnd {
		h := event.HandState
		hasHeroCards := h.HeroCards[0].Rank > 0 && h.HeroCards[1].Rank > 0

		if hasHeroCards {
			var oppTendencies map[string]float64
			var oppRanges []equity.Range

			for _, seat := range h.Seats {
				if seat.PlayerID != "" && seat.PlayerID != h.HeroID && seat.IsActive && !seat.IsFolded {
					if s.prof != nil {
						t := s.prof.GetPlayerTendencies(seat.PlayerID)
						if oppTendencies == nil && len(t) > 0 {
							oppTendencies = t
						}
						stats := s.prof.GetStats(seat.PlayerID)
						if stats != nil && stats.VPIP > 0 {
							oppRanges = append(oppRanges, equity.ParseRange(fmt.Sprintf("top%.0f%%", stats.VPIP)))
						}
					}
				}
			}

			if len(oppRanges) == 0 {
				oppRanges = []equity.Range{equity.ParseRange("random")}
			}

			// Sub-5ms fast Monte Carlo simulation
			eqResult := equity.SimulateEquity(h.HeroCards, h.CommunityCards, oppRanges, 3000)
			advice := advisor.CalculateAdvice(*h, eqResult, oppTendencies)
			rec = &advice
		}
	}

	// 4. WebSocket broadcast
	now := time.Now().UnixMilli()

	s.hub.BroadcastToTable(tableID, WSMessage{
		Type:      WSMsgEvent,
		TableID:   tableID,
		Payload:   event,
		Timestamp: now,
	})

	if event.HandState != nil {
		s.hub.BroadcastToTable(tableID, WSMessage{
			Type:      WSMsgStateUpdate,
			TableID:   tableID,
			Payload:   event.HandState,
			Timestamp: now,
		})
	}

	if rec != nil {
		s.hub.BroadcastToTable(tableID, WSMessage{
			Type:      WSMsgRecommendation,
			TableID:   tableID,
			Payload:   rec,
			Timestamp: now,
		})
	}

	return rec, nil
}

func (s *Server) handleInitTable(w http.ResponseWriter, r *http.Request) {
	var req TableInitRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request payload", http.StatusBadRequest)
		return
	}

	if req.TableID == "" {
		http.Error(w, "table_id is required", http.StatusBadRequest)
		return
	}

	state := &table.HandState{
		HandID:         fmt.Sprintf("hand-%s-%d", req.TableID, time.Now().Unix()),
		TableID:        req.TableID,
		Street:         table.StreetPreflop,
		Pot:            req.Pot,
		MinRaise:       req.MinRaise,
		HeroID:         req.HeroID,
		Seats:          req.Seats,
		CommunityCards: make([]table.Card, 0, 5),
		ActionHistory:  make([]table.ActionRecord, 0),
	}

	if s.cache != nil {
		s.cache.SetTableState(req.TableID, state)
	}

	s.hub.BroadcastToTable(req.TableID, WSMessage{
		Type:      WSMsgStateUpdate,
		TableID:   req.TableID,
		Payload:   state,
		Timestamp: time.Now().UnixMilli(),
	})

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(state)
}

func (s *Server) handleGetTableState(w http.ResponseWriter, r *http.Request) {
	tableID := r.PathValue("id")
	if tableID == "" {
		http.Error(w, "table id required", http.StatusBadRequest)
		return
	}

	if s.cache == nil {
		http.Error(w, "table not found", http.StatusNotFound)
		return
	}

	state := s.cache.GetTableState(tableID)
	if state == nil {
		http.Error(w, "table not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(state)
}

func (s *Server) handleIngestEvent(w http.ResponseWriter, r *http.Request) {
	tableID := r.PathValue("id")
	if tableID == "" {
		http.Error(w, "table id required", http.StatusBadRequest)
		return
	}

	var event vision.VisionEvent
	if err := json.NewDecoder(r.Body).Decode(&event); err != nil {
		http.Error(w, "invalid event payload", http.StatusBadRequest)
		return
	}

	if event.TableID == "" {
		event.TableID = tableID
	}

	rec, err := s.ProcessEvent(event)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	resp := EventIngestResponse{
		Status:         "ok",
		Recommendation: rec,
		Event:          &event,
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

func (s *Server) handleGetPlayerProfile(w http.ResponseWriter, r *http.Request) {
	playerID := r.PathValue("id")
	if playerID == "" {
		http.Error(w, "player id required", http.StatusBadRequest)
		return
	}

	var stats *storage.PlayerStats
	var prof *storage.LLMProfile
	var tendencies map[string]float64

	if s.prof != nil {
		stats = s.prof.GetStats(playerID)
		prof = s.prof.GetProfile(playerID)
		tendencies = s.prof.GetPlayerTendencies(playerID)
	} else {
		if s.cache != nil {
			stats = s.cache.GetPlayerStats(playerID)
			prof = s.cache.GetProfile(playerID)
		}
		if s.db != nil {
			if stats == nil {
				stats, _ = s.db.GetPlayerStats(playerID)
			}
			if prof == nil {
				prof, _ = s.db.GetLLMProfile(playerID)
			}
		}
	}

	if stats == nil && prof == nil {
		http.Error(w, "player profile not found", http.StatusNotFound)
		return
	}

	resp := PlayerProfileResponse{
		PlayerID:   playerID,
		Stats:      stats,
		Profile:    prof,
		Tendencies: tendencies,
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

func (s *Server) handleWebSocket(w http.ResponseWriter, r *http.Request) {
	tableID := r.PathValue("id")
	if tableID == "" {
		http.Error(w, "missing table id in path", http.StatusBadRequest)
		return
	}

	conn, err := s.upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}

	client := NewWSClient(s.hub, conn, tableID)
	s.hub.Register(client)

	// Send immediate initial state snapshot if available
	if s.cache != nil {
		if state := s.cache.GetTableState(tableID); state != nil {
			client.SendMessage(WSMessage{
				Type:      WSMsgStateUpdate,
				TableID:   tableID,
				Payload:   state,
				Timestamp: time.Now().UnixMilli(),
			})
		}
	}

	go client.writePump()
	go client.readPump()
}
