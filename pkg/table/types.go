package table

import "strings"

type Street string

const (
	StreetPreflop  Street = "preflop"
	StreetFlop     Street = "flop"
	StreetTurn     Street = "turn"
	StreetRiver    Street = "river"
	StreetShowdown Street = "showdown"
)

type ActionType string

const (
	ActionFold  ActionType = "fold"
	ActionCheck ActionType = "check"
	ActionCall  ActionType = "call"
	ActionBet   ActionType = "bet"
	ActionRaise ActionType = "raise"
	ActionAllIn ActionType = "all_in"
)

type Position string

const (
	PosBTN Position = "BTN"
	PosSB  Position = "SB"
	PosBB  Position = "BB"
	PosUTG Position = "UTG"
	PosMP  Position = "MP"
	PosCO  Position = "CO"
)

type SeatState struct {
	SeatNumber int      `json:"seat_number"`
	PlayerID   string   `json:"player_id"`
	PlayerName string   `json:"player_name"`
	Stack      float64  `json:"stack"`
	CurrentBet float64  `json:"current_bet"`
	IsActive   bool     `json:"is_active"`
	IsFolded   bool     `json:"is_folded"`
	Position   Position `json:"position"`
	// LastAction is the action badge currently shown on this player's
	// nameplate. It is the only observable record of what a player just did,
	// and changes to it are what the action stream is derived from.
	LastAction string `json:"last_action,omitempty"`
	// Cards are this player's holdings once they have been turned face up.
	// Only a showdown reveals them, and they are the only ground truth about
	// what someone actually held for a line: frequencies say how often a player
	// bets, showdowns say with what.
	Cards []Card `json:"cards,omitempty"`
}

type ActionRecord struct {
	PlayerID string     `json:"player_id"`
	Street   Street     `json:"street"`
	Action   ActionType `json:"action"`
	Amount   float64    `json:"amount"`
}

type HandState struct {
	HandID     string  `json:"hand_id"`
	TableID    string  `json:"table_id"`
	Street     Street  `json:"street"`
	Pot        float64 `json:"pot"`
	CurrentBet float64 `json:"current_bet"`
	MinRaise   float64 `json:"min_raise"`

	// SmallBlind and BigBlind are the stake. The screen reader has always sent
	// them -- they come from the window title, which the system hands over
	// exactly -- and there was nowhere in Go to put them, so they were dropped
	// at the boundary.
	//
	// Everything that needs a scale was then derived from whatever money
	// happened to be on the felt. MinRaise came out of the vision parser as
	// twice the largest bet, so a frame with no bets on it made the minimum
	// zero and a chart open collapsed to five chips at blinds of 1000/2000.
	// And a spot cannot be told from a raised pot without knowing what an
	// unraised one costs.
	//
	// Zero means unknown, and nothing may be inferred from that: at a big
	// blind of 0.1 every real wager is smaller than 1, so inventing a floor
	// destroys a micro table outright.
	SmallBlind float64 `json:"small_blind"`
	BigBlind   float64 `json:"big_blind"`

	CommunityCards []Card         `json:"community_cards"`
	HeroID         string         `json:"hero_id"`
	HeroCards      [2]Card        `json:"hero_cards"`
	Seats          []SeatState    `json:"seats"`
	ActionHistory  []ActionRecord `json:"action_history"`

	// HeroButtons is what the client is offering hero, lowercased, as read off
	// the screen. Empty means it was not read -- from a replay, a test, or a
	// frame where the buttons were not visible -- and nothing may be concluded
	// from that.
	//
	// It exists because the amount owed and the *option* to check are separate
	// facts, and the second one is the reliable half. A frame where the call
	// amount failed to come out used to be indistinguishable from a frame where
	// nothing was owed.
	HeroButtons []string `json:"hero_buttons,omitempty"`

	// IsHeroTurn is whether the client is waiting on hero.
	//
	// The screen reader has always reported this and nothing in Go ever read
	// it. A recommendation is an answer to "what do I do now", and that
	// question only exists while it is hero's turn -- so without it the tool
	// went on advising a hand hero had already folded, and did it with the hole
	// cards the stabiliser was still holding. On screen the nameplate said FOLD
	// and the assistant said BET 46,400.
	IsHeroTurn bool `json:"is_hero_turn"`
}

// HeroCanAct reports whether hero has a decision in front of them.
//
// Either signal will do: the client waiting on hero, or action buttons on
// screen. A frame that carries neither is one where nothing said hero could
// act, and advice on it is advice about somebody else's turn.
func (h *HandState) HeroCanAct() bool {
	if h == nil {
		return false
	}
	return h.IsHeroTurn || len(h.HeroButtons) > 0
}

// HeroMayCheck reports whether checking is on offer.
//
// Three answers, not two: yes, no, and not read. A caller that cannot tell the
// third from the first will eventually offer a free check to a player facing an
// all-in, which is what happened.
func (h *HandState) HeroMayCheck() (mayCheck bool, known bool) {
	if h == nil || len(h.HeroButtons) == 0 {
		return false, false
	}
	for _, b := range h.HeroButtons {
		if b == "check" {
			return true, true
		}
	}
	return false, true
}

// HeroFacesABet reports whether the client is offering a call, which means
// there is something to pay whatever the amount came out as.
func (h *HandState) HeroFacesABet() bool {
	if h == nil {
		return false
	}
	for _, b := range h.HeroButtons {
		if strings.Contains(b, "call") {
			return true
		}
	}
	return false
}
