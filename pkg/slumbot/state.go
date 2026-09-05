package slumbot

import (
	"fmt"

	"poker-game-analyzer/pkg/table"
)

// Player ids. They are stable strings because the advisor keys reads and action
// history by them, and because a calibration record has to name who it is about.
const (
	HeroID    = "hero"
	VillainID = "slumbot"
)

func parseCards(ss []string) ([]table.Card, error) {
	out := make([]table.Card, 0, len(ss))
	for _, s := range ss {
		c, err := table.ParseCard(s)
		if err != nil {
			return nil, fmt.Errorf("card %q: %w", s, err)
		}
		out = append(out, c)
	}
	return out, nil
}

func idFor(hero, s Seat) string {
	if s == hero {
		return HeroID
	}
	return VillainID
}

// minRaiseTo is the smallest legal raise on the current street.
//
// Derived rather than assumed: the advisor sizes off it, and a MinRaise that is
// merely plausible produces bets the server rejects, which ends a run.
func minRaiseTo(st State) int {
	level, inc := 0, BigBlind
	if st.Street == table.StreetPreflop {
		level = BigBlind
	}
	street := st.Acts[len(st.Acts)-1]
	for _, a := range street {
		if a.Kind != table.ActionBet && a.Kind != table.ActionRaise {
			continue
		}
		if d := a.To - level; d > inc {
			inc = d
		}
		level = a.To
	}
	return level + inc
}

// HandState renders the parsed action as the state the advisor consumes.
//
// Chips are the unit throughout, not big blinds: HandState carries the blinds
// explicitly and everything downstream scales by them, so converting here would
// mean converting back.
func HandState(r *Response, st State) (*table.HandState, error) {
	hero := r.HeroSeat()
	holeSlice, err := parseCards(r.HoleCards)
	if err != nil {
		return nil, err
	}
	if len(holeSlice) != 2 {
		return nil, fmt.Errorf("expected two hole cards, got %d", len(holeSlice))
	}
	board, err := parseCards(r.Board)
	if err != nil {
		return nil, err
	}

	h := &table.HandState{
		HandID:         fmt.Sprintf("slumbot-%d", r.SessionNumHands+1),
		TableID:        "slumbot",
		Street:         st.Street,
		Pot:            float64(st.Pot()),
		CurrentBet:     float64(max(st.StreetIn[0], st.StreetIn[1])),
		MinRaise:       float64(minRaiseTo(st)),
		SmallBlind:     SmallBlind,
		BigBlind:       BigBlind,
		CommunityCards: board,
		HeroID:         HeroID,
		HeroCards:      [2]table.Card{holeSlice[0], holeSlice[1]},
		IsHeroTurn:     !st.Closed && st.ToAct == hero,
	}

	for _, s := range []Seat{SeatBB, SeatSB} {
		h.Seats = append(h.Seats, table.SeatState{
			SeatNumber: int(s),
			PlayerID:   idFor(hero, s),
			PlayerName: idFor(hero, s),
			Stack:      float64(Stack - st.Committed[s]),
			CurrentBet: float64(st.StreetIn[s]),
			IsActive:   true,
			IsFolded:   st.Closed && st.Folded == s,
			Position:   s.Position(),
		})
	}

	for i, acts := range st.Acts {
		for _, a := range acts {
			h.ActionHistory = append(h.ActionHistory, table.ActionRecord{
				PlayerID: idFor(hero, a.Actor),
				Street:   streetOrder[i],
				Action:   a.Kind,
				Amount:   float64(a.To),
			})
		}
	}

	// The buttons are what the client would be offering. They are not cosmetic:
	// HeroMayCheck and HeroFacesABet are how the advisor tells "nothing owed"
	// from "the amount failed to read", and it decides differently on each.
	if h.IsHeroTurn {
		if st.Owed() > 0 {
			h.HeroButtons = []string{"fold", "call", "raise"}
		} else {
			h.HeroButtons = []string{"check", "bet"}
		}
	}
	return h, nil
}

// HeroSeatState is hero's seat in a built state.
func HeroSeatState(h *table.HandState) table.SeatState {
	for _, s := range h.Seats {
		if s.PlayerID == HeroID {
			return s
		}
	}
	return table.SeatState{}
}

// VillainSeatState is the opponent's seat in a built state, which is what the
// range width is computed for.
func VillainSeatState(h *table.HandState) table.SeatState {
	for _, s := range h.Seats {
		if s.PlayerID == VillainID {
			return s
		}
	}
	return table.SeatState{}
}
