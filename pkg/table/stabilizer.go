package table

import (
	"sync"
	"time"
)

// StateStabilizer maintains a smoothed, monotonically consistent table state across noisy/glitchy video frames.
type StateStabilizer struct {
	mu           sync.RWMutex
	currentHand  *HandState
	lastUpdateAt time.Time
	handCount    int
}

// NewStateStabilizer creates a new StateStabilizer instance.
func NewStateStabilizer() *StateStabilizer {
	return &StateStabilizer{}
}

// Stabilize merges an incoming raw frame state with the previous verified state, filtering out animation glitches and dropouts.
func (s *StateStabilizer) Stabilize(raw *HandState) *HandState {
	if raw == nil {
		s.mu.RLock()
		defer s.mu.RUnlock()
		return s.currentHand
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()

	// Initial state
	if s.currentHand == nil {
		s.currentHand = cloneHandState(raw)
		s.lastUpdateAt = now
		return s.currentHand
	}

	prev := s.currentHand

	// 1. Check if a brand new hand has started
	// A new hand starts if:
	// - Previous hand had board cards (>0) and raw has 0 board cards AND pot reset to low value
	// - OR Previous pot was large (>0) and raw pot dropped significantly (< 20% of prev) with no board cards
	isNewHand := false
	if len(prev.CommunityCards) > 0 && len(raw.CommunityCards) == 0 && raw.Pot <= prev.Pot*0.25 {
		isNewHand = true
	} else if prev.Pot > 1000 && raw.Pot > 0 && raw.Pot <= prev.Pot*0.15 && len(raw.CommunityCards) == 0 {
		isNewHand = true
	} else if prev.Street == StreetShowdown && raw.Street != StreetShowdown {
		isNewHand = true
	}

	if isNewHand {
		s.handCount++
		st := cloneHandState(raw)
		if st.Street == "" {
			if len(st.CommunityCards) == 0 {
				st.Street = StreetPreflop
			} else if len(st.CommunityCards) == 3 {
				st.Street = StreetFlop
			} else if len(st.CommunityCards) == 4 {
				st.Street = StreetTurn
			} else {
				st.Street = StreetRiver
			}
		}
		s.currentHand = st
		s.lastUpdateAt = now
		return s.currentHand
	}

	// 2. Merge Community Board Cards (Monotonic Growth)
	mergedBoard := make([]Card, len(prev.CommunityCards))
	copy(mergedBoard, prev.CommunityCards)

	if len(raw.CommunityCards) > len(prev.CommunityCards) {
		// New cards dealt on turn or river
		mergedBoard = make([]Card, len(raw.CommunityCards))
		copy(mergedBoard, raw.CommunityCards)
	} else if len(raw.CommunityCards) == len(prev.CommunityCards) && len(raw.CommunityCards) > 0 {
		// Update cards if new detection has valid rank and prev was unknown
		for i := 0; i < len(raw.CommunityCards); i++ {
			if raw.CommunityCards[i].Rank > 0 {
				mergedBoard[i] = raw.CommunityCards[i]
			}
		}
	}

	// 3. Merge Hero Hole Cards
	var mergedHero [2]Card = prev.HeroCards
	if raw.HeroCards[0].Rank > 0 && raw.HeroCards[1].Rank > 0 {
		mergedHero = raw.HeroCards
	} else if raw.HeroCards[0].Rank > 0 && mergedHero[0].Rank == 0 {
		mergedHero[0] = raw.HeroCards[0]
	}

	// 4. Merge Pot (Monotonically Non-Decreasing within the same hand)
	mergedPot := prev.Pot
	if raw.Pot > prev.Pot {
		mergedPot = raw.Pot
	} else if raw.Pot > 0 && prev.Pot == 0 {
		mergedPot = raw.Pot
	}

	// 5. Merge Seats / Players (Retain player names and stacks)
	mergedSeats := mergeSeats(prev.Seats, raw.Seats)

	// 6. Determine Street
	var street Street = StreetPreflop
	switch len(mergedBoard) {
	case 0:
		street = StreetPreflop
	case 3:
		street = StreetFlop
	case 4:
		street = StreetTurn
	case 5:
		street = StreetRiver
	default:
		if len(mergedBoard) > 0 {
			street = StreetFlop
		}
	}

	handID := prev.HandID
	if raw.HandID != "" && raw.HandID != "live-hand" {
		handID = raw.HandID
	}

	mergedState := &HandState{
		HandID:         handID,
		TableID:        raw.TableID,
		Street:         street,
		Pot:            mergedPot,
		CurrentBet:     raw.CurrentBet,
		MinRaise:       raw.MinRaise,
		CommunityCards: mergedBoard,
		HeroID:         raw.HeroID,
		HeroCards:      mergedHero,
		Seats:          mergedSeats,
		ActionHistory:  raw.ActionHistory,
	}

	if mergedState.TableID == "" {
		mergedState.TableID = prev.TableID
	}
	if mergedState.HeroID == "" {
		mergedState.HeroID = prev.HeroID
	}

	s.currentHand = mergedState
	s.lastUpdateAt = now
	return s.currentHand
}

// Reset explicitly clears the stabilized state for testing or table change.
func (s *StateStabilizer) Reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.currentHand = nil
}

func mergeSeats(prevSeats, rawSeats []SeatState) []SeatState {
	if len(rawSeats) == 0 {
		return prevSeats
	}
	if len(prevSeats) == 0 {
		return rawSeats
	}

	seatMap := make(map[string]SeatState)
	for _, s := range prevSeats {
		if s.PlayerName != "" && s.PlayerName != "Player" {
			seatMap[s.PlayerName] = s
		}
	}

	res := make([]SeatState, len(rawSeats))
	for i, r := range rawSeats {
		res[i] = r
		if p, exists := seatMap[r.PlayerName]; exists {
			if r.Stack == 0 && p.Stack > 0 {
				res[i].Stack = p.Stack
			}
			if r.PlayerID == "" || r.PlayerID == "Player" {
				res[i].PlayerID = p.PlayerID
			}
		}
	}
	return res
}

func cloneHandState(h *HandState) *HandState {
	if h == nil {
		return nil
	}
	c := *h
	c.CommunityCards = make([]Card, len(h.CommunityCards))
	copy(c.CommunityCards, h.CommunityCards)
	c.Seats = make([]SeatState, len(h.Seats))
	copy(c.Seats, h.Seats)
	c.ActionHistory = make([]ActionRecord, len(h.ActionHistory))
	copy(c.ActionHistory, h.ActionHistory)
	return &c
}
