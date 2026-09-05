package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
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

	"poker-game-analyzer/pkg/advice"
	"poker-game-analyzer/pkg/advisor"
	"poker-game-analyzer/pkg/audit"
	"poker-game-analyzer/pkg/capture"
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
	Status         string                   `json:"status"`
	Recommendation *advisor.AdvisorResponse `json:"recommendation,omitempty"`
	Event          *vision.VisionEvent      `json:"event,omitempty"`
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
	auditLog   *audit.Logger
	mu         sync.Mutex

	// What was last sent for each table. Vision delivers a frame every few
	// dozen milliseconds and most of them say exactly what the one before
	// said; broadcasting each one made the panel redraw constantly, which is
	// what "the whole screen jumps" is.
	lastSent   map[string]string
	lastSentMu sync.Mutex

	roiConfig       vision.ROIConfig
	roiPath         string
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
		cache:           cache,
		db:              db,
		prof:            prof,
		hub:             hub,
		mux:             http.NewServeMux(),
		roiConfig:       vision.DefaultCoinPoker6MaxROI(),
		roiPath:         roiConfigPath(),
		windowsProvider: capture.ListAllWindows,
		stabilizer:      table.NewStateStabilizer(),
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

// SetAuditLogger attaches a decision audit log. Every recommendation, and
// every state that failed to produce one, is recorded with the inputs that
// were missing at the time.
func (s *Server) SetAuditLogger(l *audit.Logger) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.auditLog = l
}

func (s *Server) auditLogger() *audit.Logger {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.auditLog
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

	// Whether a hand has ended is decided from the incoming event, before any
	// smoothing. The stabilizer works on vision noise and must not get a vote
	// on the terminal state, or a showdown frame can be smoothed back into a
	// river frame and the hand is never persisted or profiled.
	isHandEnd := event.Type == vision.EventHandEnd || (event.HandState != nil && event.HandState.Street == table.StreetShowdown)

	// 0. State Stabilization (filter frame glitches, dropouts & maintain monotonic card/pot state)
	//
	// Showdown states go through it too. Skipping them left the stabiliser's
	// showdown branch dead: a hand that actually reached a showdown was saved
	// under the vision placeholder id instead of a minted one, so every such
	// hand overwrote the same row, and the seats and board it had accumulated
	// over the hand were dropped in favour of whatever the final frame happened
	// to read. Whether the hand has ended is still decided from the raw event
	// above, before any smoothing.
	var completed *table.HandState
	if event.HandState != nil && s.stabilizer != nil {
		event.HandState = s.stabilizer.Stabilize(event.HandState)
		// A hand is otherwise recognised as finished when the next one starts:
		// most hands end with everyone folding, and no showdown is shown at all.
		completed = s.stabilizer.TakeCompletedHand()
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
	finished := completed
	if isHandEnd && event.HandState != nil {
		finished = event.HandState
	}
	if finished != nil {
		if finished.TableID == "" {
			finished.TableID = tableID
		}
		if s.prof != nil {
			s.prof.ProcessHandEnd(*finished)
		}
		if s.db != nil {
			_ = s.db.SaveHandHistory(*finished)
		}
	}

	// 3. Real-time Equity and Advisor Recommendation calculation
	//
	// The whole of it now lives in pkg/advice, because a second caller appeared
	// that has to reach the same decision: the harness in pkg/sim, which plays
	// the strategy out over whole hands and measures what it wins. What is left
	// here is gathering the reads the profiler holds and handing them over.
	var rec *advisor.AdvisorResponse
	var auditReads map[string]map[string]float64
	noAdvice := ""
	if reason := audit.Unreadable(event.HandState); reason != "" && !isHandEnd {
		// A misread frame is withheld rather than advised on. The panel keeps
		// whatever it last had and says why, which is steadier than advice
		// recomputed from a table that was not there.
		noAdvice = "state not readable: " + reason
	} else if event.HandState != nil && !isHandEnd {
		h := event.HandState
		reads := advice.Reads{
			Tendencies: make(map[string]map[string]float64),
			RangeWidth: make(map[string]float64),
		}
		if s.prof != nil {
			for _, seat := range h.Seats {
				if seat.PlayerID == "" || seat.PlayerID == h.HeroID {
					continue
				}
				if t := s.prof.GetPlayerTendencies(seat.PlayerID); len(t) > 0 {
					reads.Tendencies[seat.PlayerID] = t
				}
				if stats := s.prof.GetStats(seat.PlayerID); stats != nil && stats.VPIP > 0 {
					reads.RangeWidth[seat.PlayerID] = stats.VPIP
				}
			}
		}

		res := advice.Evaluate(h, reads, advice.Options{})
		rec = res.Recommendation
		noAdvice = res.NoAdvice
		auditReads = res.SeatReads
	}

	if lg := s.auditLogger(); lg != nil {
		_ = lg.Log(audit.Build(event.HandState, rec, auditReads))
	}

	// 4. WebSocket broadcast
	now := time.Now().UnixMilli()

	// A frame that says what the last one said is not news. The panel is
	// redrawn from these messages, so sending them anyway is what makes it
	// flicker; nothing downstream loses anything by not hearing the same
	// thing twice.
	if s.alreadySent(tableID, event.HandState, rec, noAdvice) {
		return rec, nil
	}

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

	// A recommendation is broadcast on every processed state, including the
	// ones that produced none. Sending only when advice exists leaves the HUD
	// holding the previous hand's recommendation with nothing to tell it the
	// advice has expired -- live, that showed a confident CHECK, computed for a
	// pot of 4,280, while the table sat at 61,680 and hero was facing a raise.
	// A null payload is the signal that there is currently no advice.
	if isHandEnd && rec == nil && noAdvice == "" {
		noAdvice = "Раздача закончена"
	}
	s.hub.BroadcastToTable(tableID, WSMessage{
		Type:      WSMsgRecommendation,
		TableID:   tableID,
		Payload:   rec,
		Timestamp: now,
		Reason:    noAdvice,
	})

	return rec, nil
}

// alreadySent reports whether this table has just been told exactly this, and
// records it when it has not.
//
// The fingerprint covers what the panel draws: the state and the advice. A
// timestamp is deliberately not part of it, because the time a frame arrived
// is the one thing that differs on every frame and nothing on screen shows it.
func (s *Server) alreadySent(tableID string, state *table.HandState,
	rec *advisor.AdvisorResponse, noAdvice string,
) bool {
	shot, err := json.Marshal(struct {
		State    *table.HandState         `json:"state"`
		Rec      *advisor.AdvisorResponse `json:"rec"`
		NoAdvice string                   `json:"no_advice"`
	}{state, rec, noAdvice})
	if err != nil {
		// Unable to tell, so say it is new: a redundant frame costs a redraw,
		// a dropped one costs the panel being wrong.
		return false
	}
	sum := sha256.Sum256(shot)
	fingerprint := hex.EncodeToString(sum[:])

	s.lastSentMu.Lock()
	defer s.lastSentMu.Unlock()
	if s.lastSent == nil {
		s.lastSent = make(map[string]string)
	}
	if s.lastSent[tableID] == fingerprint {
		return true
	}
	s.lastSent[tableID] = fingerprint
	return false
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

// SetROIConfig updates the active table layout and keeps it.
//
// Calibration is done by hand against one client's layout, so it has to
// survive the process that took it. A layout that cannot be written is still
// applied: the operator has calibrated, and refusing to use it because it
// could not be filed would be the wrong way round.
func (s *Server) SetROIConfig(cfg vision.ROIConfig) error {
	if err := cfg.Validate(); err != nil {
		return err
	}
	s.roiMu.Lock()
	s.roiConfig = cfg
	path := s.roiPath
	s.roiMu.Unlock()

	if path == "" {
		return nil
	}
	return vision.SaveROIConfig(path, cfg)
}

// LoadROIConfig applies the saved calibration, if there is one.
//
// Reports what it did so a start-up line can say which layout is in use: the
// built-in one and a hand-made one behave very differently, and until now
// there was no way to tell them apart from the outside.
func (s *Server) LoadROIConfig() (path string, loaded bool, err error) {
	s.roiMu.RLock()
	path = s.roiPath
	s.roiMu.RUnlock()
	if path == "" {
		return "", false, nil
	}
	cfg, found, err := vision.LoadROIConfig(path)
	if err != nil || !found {
		return path, false, err
	}
	s.roiMu.Lock()
	s.roiConfig = cfg
	s.roiMu.Unlock()
	return path, true, nil
}

// roiConfigPath is where calibrations are kept, or empty when this machine
// will not say where its configuration lives.
func roiConfigPath() string {
	p, err := vision.ROIConfigPath()
	if err != nil {
		return ""
	}
	return p
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

	// A layout that cannot read a table is refused here rather than accepted
	// and discovered later as an empty board and nameless players.
	if err := s.SetROIConfig(cfg); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
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
