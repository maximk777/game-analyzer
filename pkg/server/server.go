package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/jpeg"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"poker-game-analyzer/pkg/advisor"
	"poker-game-analyzer/pkg/capture"
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
	stabilizer *table.StateStabilizer
	mu         sync.Mutex

	roiConfig       vision.ROIConfig
	roiMu           sync.RWMutex
	snapshotData    []byte
	snapshotType    string
	snapshotMu      sync.RWMutex
	windowsProvider func() ([]capture.WindowInfo, error)
	windowsMu       sync.RWMutex
}

// NewServer initializes and configures a new Server instance.
func NewServer(cache *storage.MemoryCache, db *storage.SQLiteDB, prof *profiler.Profiler) *Server {
	hub := NewWSHub()
	s := &Server{
		cache:      cache,
		db:         db,
		prof:       prof,
		hub:        hub,
		mux:        http.NewServeMux(),
		roiConfig:  vision.DefaultCoinPoker6MaxROI(),
		windowsProvider: capture.ListAllWindows,
		stabilizer: table.NewStateStabilizer(),
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

	// Live capture, ROI calibration & Window discovery endpoints
	s.mux.HandleFunc("GET /api/v1/snapshot", s.handleGetSnapshot)
	s.mux.HandleFunc("GET /api/v1/roi", s.handleGetROI)
	s.mux.HandleFunc("POST /api/v1/roi", s.handleSetROI)
	s.mux.HandleFunc("GET /api/v1/windows", s.handleGetWindows)
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

	// 0. State Stabilization (filter frame glitches, dropouts & maintain monotonic card/pot state)
	if event.HandState != nil && s.stabilizer != nil {
		event.HandState = s.stabilizer.Stabilize(event.HandState)
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

// IngestLiveState updates the table state in cache, runs Monte Carlo equity and Advisor recommendation calculations, and broadcasts the state update over WebSocket.
func (s *Server) IngestLiveState(state *table.HandState) (*advisor.AdvisorResponse, error) {
	if state == nil {
		return nil, errors.New("nil hand state provided")
	}
	event := vision.VisionEvent{
		Type:      vision.EventHeroTurn,
		TableID:   state.TableID,
		HandState: state,
	}
	return s.ProcessEvent(event)
}

// IngestEvent processes an incoming game/vision event, updating cache, profiler, and broadcasting over WebSocket.
func (s *Server) IngestEvent(event vision.VisionEvent) (*advisor.AdvisorResponse, error) {
	return s.ProcessEvent(event)
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

	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "failed to read body", http.StatusBadRequest)
		return
	}

	var event vision.VisionEvent
	if err := json.Unmarshal(bodyBytes, &event); err == nil && (event.Type != "" || event.HandState != nil) {
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
		return
	}

	// Try unmarshaling directly as HandState
	var state table.HandState
	if err := json.Unmarshal(bodyBytes, &state); err == nil {
		if state.TableID == "" {
			state.TableID = tableID
		}
		rec, err := s.IngestLiveState(&state)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"status":         "ok",
			"recommendation": rec,
			"state":          state,
		})
		return
	}

	http.Error(w, "invalid event or hand state payload", http.StatusBadRequest)
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

// SetSnapshot encodes the provided image to JPEG format and updates the server's live snapshot frame.
func (s *Server) SetSnapshot(img image.Image) {
	if img == nil {
		s.snapshotMu.Lock()
		s.snapshotData = nil
		s.snapshotType = ""
		s.snapshotMu.Unlock()
		return
	}

	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 85}); err != nil {
		return
	}

	s.snapshotMu.Lock()
	s.snapshotData = buf.Bytes()
	s.snapshotType = "image/jpeg"
	s.snapshotMu.Unlock()
}

// SetSnapshotBytes sets the raw snapshot image payload and content type.
func (s *Server) SetSnapshotBytes(data []byte, contentType string) {
	s.snapshotMu.Lock()
	defer s.snapshotMu.Unlock()
	s.snapshotData = data
	if contentType == "" {
		contentType = "image/jpeg"
	}
	s.snapshotType = contentType
}

// GetSnapshot retrieves a copy of the current snapshot bytes and its content type.
func (s *Server) GetSnapshot() ([]byte, string) {
	s.snapshotMu.RLock()
	defer s.snapshotMu.RUnlock()
	if len(s.snapshotData) == 0 {
		return nil, ""
	}
	out := make([]byte, len(s.snapshotData))
	copy(out, s.snapshotData)
	return out, s.snapshotType
}

// SetROIConfig updates the active table Region of Interest layout.
func (s *Server) SetROIConfig(cfg vision.ROIConfig) {
	s.roiMu.Lock()
	defer s.roiMu.Unlock()
	s.roiConfig = cfg
}

// GetROIConfig returns the active table Region of Interest layout.
func (s *Server) GetROIConfig() vision.ROIConfig {
	s.roiMu.RLock()
	defer s.roiMu.RUnlock()
	return s.roiConfig
}

// SetWindowsProvider sets a custom window enumeration provider (useful for testing).
func (s *Server) SetWindowsProvider(provider func() ([]capture.WindowInfo, error)) {
	s.windowsMu.Lock()
	defer s.windowsMu.Unlock()
	s.windowsProvider = provider
}

// GetWindows discovers and returns on-screen windows.
func (s *Server) GetWindows() ([]capture.WindowInfo, error) {
	s.windowsMu.RLock()
	provider := s.windowsProvider
	s.windowsMu.RUnlock()

	if provider != nil {
		return provider()
	}
	return capture.ListAllWindows()
}

func (s *Server) handleGetSnapshot(w http.ResponseWriter, r *http.Request) {
	data, contentType := s.GetSnapshot()
	if len(data) == 0 {
		http.Error(w, "no snapshot available", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

func (s *Server) handleGetROI(w http.ResponseWriter, r *http.Request) {
	cfg := s.GetROIConfig()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(cfg)
}

func (s *Server) handleSetROI(w http.ResponseWriter, r *http.Request) {
	var cfg vision.ROIConfig
	if err := json.NewDecoder(r.Body).Decode(&cfg); err != nil {
		http.Error(w, "invalid roi json payload", http.StatusBadRequest)
		return
	}

	s.SetROIConfig(cfg)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(cfg)
}

func (s *Server) handleGetWindows(w http.ResponseWriter, r *http.Request) {
	windows, err := s.GetWindows()
	if err != nil {
		http.Error(w, fmt.Sprintf("failed to list windows: %v", err), http.StatusInternalServerError)
		return
	}

	if windows == nil {
		windows = []capture.WindowInfo{}
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(windows)
}
