package server

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	"poker-game-analyzer/pkg/advisor"
	"poker-game-analyzer/pkg/llm"
	"poker-game-analyzer/pkg/storage"
	"poker-game-analyzer/pkg/table"
)

// coachTimeout is how long a second opinion is worth waiting for. A decision at
// the table does not wait, so an answer that arrives after the spot is gone is
// not late, it is wrong.
const coachTimeout = 20 * time.Second

// CoachUpdate is what the panel is told about the second opinion.
//
// Advice is nil while the model is still reading, which is what lets the panel
// clear the previous spot's answer the moment the spot changes instead of
// showing it against a table it was never about.
type CoachUpdate struct {
	Spot    string           `json:"spot"`
	Pending bool             `json:"pending"`
	Advice  *llm.CoachAdvice `json:"advice,omitempty"`
	Error   string           `json:"error,omitempty"`
}

// coachRunner asks the model at most once per decision, in the background.
//
// The screen is read about twelve times a second and the model takes a second
// or two to answer, so asking per frame would mean a queue that never drains,
// a bill to match, and answers arriving against tables that have moved on. One
// question per spot, and a spot already being asked about is not asked again.
type coachRunner struct {
	mu       sync.Mutex
	coach    llm.Coach
	inFlight map[string]bool
	lastSpot map[string]string
	lastFail string
}

// SetCoach attaches the second opinion. Nil turns it off, which is the state
// when no API key was found.
func (s *Server) SetCoach(c llm.Coach) {
	s.coachRunner.mu.Lock()
	defer s.coachRunner.mu.Unlock()
	s.coachRunner.coach = c
	s.coachRunner.inFlight = make(map[string]bool)
	s.coachRunner.lastSpot = make(map[string]string)
}

// HasCoach reports whether a second opinion is configured.
func (s *Server) HasCoach() bool {
	s.coachRunner.mu.Lock()
	defer s.coachRunner.mu.Unlock()
	return s.coachRunner.coach != nil
}

// spotKey identifies one decision. Everything hero's answer depends on is in
// it, and nothing else is: the frame counter and the clock are deliberately
// absent, or every frame would be a new question.
func spotKey(h *table.HandState, rec *advisor.AdvisorResponse) string {
	if h == nil {
		return ""
	}
	key := fmt.Sprintf("%s|%s%s|%v|%.4f|%.4f",
		h.Street, h.HeroCards[0], h.HeroCards[1], h.CommunityCards, h.Pot, h.CurrentBet)
	for _, seat := range h.Seats {
		key += fmt.Sprintf("|%d:%.4f:%.4f:%v", seat.SeatNumber, seat.Stack, seat.CurrentBet, seat.IsFolded)
	}
	if rec != nil {
		key += fmt.Sprintf("|%s:%.4f", rec.PrimaryAction, rec.RecommendedAmount)
	}
	return key
}

// askCoach starts a second opinion on this spot if there is one to have and it
// has not been asked yet. It never blocks the frame it was called from.
func (s *Server) askCoach(tableID string, h *table.HandState, rec *advisor.AdvisorResponse) {
	if h == nil || rec == nil || !h.HeroCanAct() {
		return
	}

	r := &s.coachRunner
	key := spotKey(h, rec)

	r.mu.Lock()
	coach := r.coach
	if coach == nil || key == "" || r.lastSpot[tableID] == key || r.inFlight[key] {
		r.mu.Unlock()
		return
	}
	r.lastSpot[tableID] = key
	r.inFlight[key] = true
	r.mu.Unlock()

	// The panel is told at once that the previous answer no longer applies.
	s.broadcastCoach(tableID, CoachUpdate{Spot: key, Pending: true})

	state := cloneForCoach(h)
	profiles := s.profilesFor(h)

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), coachTimeout)
		defer cancel()

		advice, err := coach.AdviseHand(ctx, llm.CoachInput{State: state, Own: rec, Profiles: profiles})

		r.mu.Lock()
		delete(r.inFlight, key)
		stale := r.lastSpot[tableID] != key
		r.mu.Unlock()

		// The table moved on while the model was reading. Saying so would
		// overwrite the answer to the spot hero is actually in.
		if stale {
			return
		}

		if err != nil {
			s.reportCoachFailure(err)
			s.broadcastCoach(tableID, CoachUpdate{Spot: key, Error: err.Error()})
			return
		}
		s.broadcastCoach(tableID, CoachUpdate{Spot: key, Advice: advice})
	}()
}

func (s *Server) broadcastCoach(tableID string, u CoachUpdate) {
	s.hub.BroadcastToTable(tableID, WSMessage{
		Type:      WSMsgCoach,
		TableID:   tableID,
		Payload:   u,
		Timestamp: time.Now().UnixMilli(),
	})
}

// reportCoachFailure says once why the second opinion is silent. Repeating the
// same error every spot is noise that hides when it started.
func (s *Server) reportCoachFailure(err error) {
	r := &s.coachRunner
	r.mu.Lock()
	defer r.mu.Unlock()
	if err.Error() == r.lastFail {
		return
	}
	r.lastFail = err.Error()
	log.Printf("[COACH] second opinion unavailable: %v", err)
}

// profilesFor is what is known about the opponents still in the hand.
func (s *Server) profilesFor(h *table.HandState) []storage.LLMProfile {
	if s.prof == nil {
		return nil
	}
	var out []storage.LLMProfile
	for _, seat := range h.Seats {
		if seat.IsFolded || seat.PlayerID == h.HeroID || seat.PlayerID == "" {
			continue
		}
		if p := s.prof.GetProfile(seat.PlayerID); p != nil {
			out = append(out, *p)
		}
	}
	return out
}

// cloneForCoach copies the state, because the caller goes on stabilising the
// one it holds while the model reads this one.
func cloneForCoach(h *table.HandState) *table.HandState {
	c := *h
	c.CommunityCards = append([]table.Card(nil), h.CommunityCards...)
	c.Seats = append([]table.SeatState(nil), h.Seats...)
	c.HeroButtons = append([]string(nil), h.HeroButtons...)
	c.ActionHistory = append([]table.ActionRecord(nil), h.ActionHistory...)
	return &c
}
