package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"poker-game-analyzer/pkg/advice"
	"poker-game-analyzer/pkg/advisor"
	"poker-game-analyzer/pkg/audit"
	"poker-game-analyzer/pkg/profiler"
	"poker-game-analyzer/pkg/storage"
	"poker-game-analyzer/pkg/table"
)

// TableInitRequest is the request body for initializing a poker table.
type TableInitRequest struct {
	TableID  string            `json:"table_id"`
	HeroID   string            `json:"hero_id,omitempty"`
	Seats    []table.SeatState `json:"seats,omitempty"`
	Pot      float64           `json:"pot,omitempty"`
	MinRaise float64           `json:"min_raise,omitempty"`
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
	auditLog   *audit.Logger
	mu         sync.Mutex

	// What was last sent for each table. The wire re-sends a seat or pot many
	// times a second and most of them say exactly what the one before said;
	// broadcasting each one made the panel redraw constantly, which is what
	// "the whole screen jumps" is.
	lastSent   map[string]string
	lastSentMu sync.Mutex

	coachRunner coachRunner
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

// ingestState folds one table state into the pipeline: the cache, hand-end
// persistence and profiling, the advisor, the audit log, and the WebSocket
// broadcast the HUD is drawn from.
//
// The state comes off the wire exact -- seats, stacks, cards, pot and actions as
// the server stated them -- so nothing here smooths or second-guesses it. A hand
// has ended when its street is showdown; that is the wire's own signal, decided
// by the source, and the moment the hand is persisted and the players profiled.
func (s *Server) ingestState(state *table.HandState) (*advisor.AdvisorResponse, error) {
	if state == nil {
		return nil, errors.New("nil hand state provided")
	}
	tableID := state.TableID
	if tableID == "" {
		return nil, errors.New("missing table id in state")
	}

	isHandEnd := state.Street == table.StreetShowdown

	// 1. Cache
	if s.cache != nil {
		s.cache.SetTableState(tableID, state)
	}

	// 2. Hand end persistence & profiler updates
	if isHandEnd {
		if s.prof != nil {
			s.prof.ProcessHandEnd(*state)
		}
		if s.db != nil {
			_ = s.db.SaveHandHistory(*state)
		}
	}

	// 3. Advisor recommendation. The decision itself lives in pkg/advice,
	// shared with the offline harness; what is left here is gathering the reads
	// the profiler holds and handing them over.
	var rec *advisor.AdvisorResponse
	var auditReads map[string]map[string]float64
	noAdvice := ""
	if reason := audit.Unreadable(state); reason != "" && !isHandEnd {
		noAdvice = "state not readable: " + reason
	} else if !isHandEnd {
		reads := advice.Reads{
			Tendencies: make(map[string]map[string]float64),
			RangeWidth: make(map[string]float64),
		}
		for _, seat := range state.Seats {
			if seat.PlayerID == "" || seat.PlayerID == state.HeroID {
				continue
			}
			var own map[string]float64
			var ownVPIP float64
			var ownHands int
			if s.prof != nil {
				own = s.prof.GetPlayerTendencies(seat.PlayerID)
				if stats := s.prof.GetStats(seat.PlayerID); stats != nil {
					ownVPIP, ownHands = stats.VPIP, stats.HandsCount
				}
			}
			// What we counted, over what the site counted. On a table where we
			// have no history the second is the whole read, and without it every
			// opponent was a stranger for the first hour -- 0/0/0 in the panel
			// beside the client's own popup showing 25/20/9.
			if t := advice.WithSiteRead(own, seat.Site); len(t) > 0 {
				reads.Tendencies[seat.PlayerID] = t
			}
			if w, ok := advice.RangeWidthVPIP(ownVPIP, ownHands, seat.Site, seat.ServerVPIP); ok {
				reads.RangeWidth[seat.PlayerID] = w
			}
		}

		res := advice.Evaluate(state, reads, advice.Options{})
		rec = res.Recommendation
		noAdvice = res.NoAdvice
		auditReads = res.SeatReads
	}

	if lg := s.auditLogger(); lg != nil {
		_ = lg.Log(audit.Build(state, rec, auditReads))
	}

	// 4. WebSocket broadcast
	now := time.Now().UnixMilli()

	// A state that says what the last one said is not news. The panel is
	// redrawn from these messages, so sending them anyway is what makes it
	// flicker; nothing downstream loses anything by not hearing it twice.
	if s.alreadySent(tableID, state, rec, noAdvice) {
		return rec, nil
	}

	s.hub.BroadcastToTable(tableID, WSMessage{
		Type:      WSMsgStateUpdate,
		TableID:   tableID,
		Payload:   state,
		Timestamp: now,
	})

	// A recommendation is broadcast on every processed state, including the
	// ones that produced none: a null payload is the signal that there is
	// currently no advice, so the HUD does not go on showing the last hand's.
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

	// The second opinion is asked for last and answered later. It never holds
	// up the recommendation the panel is drawn from.
	s.askCoach(tableID, state, rec)

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

// IngestLiveState folds a table state into the pipeline: cache, hand-end
// persistence and profiling, the advisor, and the WebSocket broadcast. It is
// the sole ingest path -- the wire source (pkg/sfs) calls it with each state it
// reconstructs.
func (s *Server) IngestLiveState(state *table.HandState) (*advisor.AdvisorResponse, error) {
	return s.ingestState(state)
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
