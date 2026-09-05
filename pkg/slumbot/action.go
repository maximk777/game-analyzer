// Package slumbot plays the advisor against Slumbot and records what the
// advisor believed about the opponent's range against what the opponent
// actually held.
//
// The point is not the win rate. Slumbot is heads-up 200bb and the tool is
// 6-max 100bb, so a win rate here would answer a question nobody asked. What it
// has that no bot of ours can have is `bot_hole_cards` in every hand, including
// the ones that end in a fold -- and the advisor's whole model of an opponent is
// a claim about exactly that. See docs/STRATEGY.md §6.
package slumbot

import (
	"fmt"
	"strconv"
	"strings"

	"poker-game-analyzer/pkg/table"
)

// Chip amounts. Slumbot's stake is fixed and its numbers are whole chips.
const (
	SmallBlind = 50
	BigBlind   = 100
	// Stack is what each player starts with. Slumbot is a 200 big blind game;
	// StartHand verifies this against the wire rather than trusting it, because
	// every pot-relative number in the advisor is scaled by it.
	Stack = 20000
)

// Seat identifies one of the two players by Slumbot's `client_pos`: 0 is the big
// blind, 1 is the small blind, who is also the button.
//
// Confirmed on the wire rather than read off documentation: folding at
// client_pos 0 costs 100 and at client_pos 1 costs 50.
type Seat int

const (
	SeatBB Seat = 0
	SeatSB Seat = 1
)

func (s Seat) Other() Seat {
	if s == SeatBB {
		return SeatSB
	}
	return SeatBB
}

func (s Seat) Position() table.Position {
	if s == SeatBB {
		return table.PosBB
	}
	// Heads-up, the small blind is on the button. The advisor's positional
	// logic keys off BTN for "acts last after the flop", which is what matters
	// here; calling it SB would tell it the opposite.
	return table.PosBTN
}

// Act is one entry in the action string.
type Act struct {
	Actor Seat
	Kind  table.ActionType
	// To is the actor's total contribution *for the current street* after this
	// act, in chips. It is not the increment, despite Slumbot's field for
	// sending one being called `incr`: replying "b600" to an opening "b200"
	// produces the string "b200b600f". Reading this as an increment silently
	// misprices every pot in the run rather than failing.
	To int
}

// State is a hand as far as the action string describes it.
type State struct {
	Street table.Street
	// Acts holds the parsed acts per street, preflop first.
	Acts [][]Act
	// Committed is each seat's total contribution across the whole hand, and
	// StreetIn its contribution on the current street only.
	Committed [2]int
	StreetIn  [2]int
	// ToAct is whose turn it is; meaningless when Closed.
	ToAct Seat
	// Closed is true when the string ends in a fold. A hand that reached
	// showdown is not Closed by the string: the caller learns that from the
	// response carrying `winnings`.
	Closed bool
	// Folded is the seat that folded, when Closed.
	Folded Seat
}

// Pot is everything committed by both seats.
func (s State) Pot() int { return s.Committed[0] + s.Committed[1] }

// Owed is what ToAct must put in to continue: zero means checking is on offer.
func (s State) Owed() int {
	d := s.StreetIn[s.ToAct.Other()] - s.StreetIn[s.ToAct]
	if d < 0 {
		return 0
	}
	return d
}

// streetOrder is the streets in the order the action string lists them.
var streetOrder = []table.Street{
	table.StreetPreflop, table.StreetFlop, table.StreetTurn, table.StreetRiver,
}

// firstToAct is the seat that opens a street.
//
// Heads-up inverts between the streets: the button posts the small blind and
// acts first before the flop, then acts last on every street after it. Getting
// this backwards would attribute each act to the wrong player, which is a
// mistake that parses cleanly and produces a plausible, wrong measurement.
func firstToAct(streetIdx int) Seat {
	if streetIdx == 0 {
		return SeatSB
	}
	return SeatBB
}

// ParseAction reads Slumbot's cumulative action string.
//
// Streets are separated by "/" and, within a street, the two players strictly
// alternate, so the actor of each token follows from which street it is on and
// how many tokens came before it. The string carries its own street boundaries,
// so no rule about when a street closes has to be reimplemented here -- which
// matters preflop, where a call by the small blind does not end the street but a
// call by the big blind does.
func ParseAction(s string) (State, error) {
	st := State{
		Committed: [2]int{BigBlind, SmallBlind},
		StreetIn:  [2]int{BigBlind, SmallBlind},
		ToAct:     SeatSB,
		Street:    table.StreetPreflop,
	}

	for i, chunk := range strings.Split(s, "/") {
		if i >= len(streetOrder) {
			return State{}, fmt.Errorf("action %q has more than four streets", s)
		}
		if i > 0 {
			// A new street: bets return to zero, the big blind opens.
			st.Street = streetOrder[i]
			st.StreetIn = [2]int{}
			st.ToAct = firstToAct(i)
		}
		st.Acts = append(st.Acts, nil)

		acts, err := parseStreet(chunk, firstToAct(i), &st)
		if err != nil {
			return State{}, fmt.Errorf("action %q, street %d: %w", s, i, err)
		}
		st.Acts[i] = acts
		if st.Closed {
			return st, nil
		}
	}
	return st, nil
}

func parseStreet(chunk string, first Seat, st *State) ([]Act, error) {
	var acts []Act
	actor := first
	for i := 0; i < len(chunk); {
		var a Act
		a.Actor = actor
		switch chunk[i] {
		case 'f':
			a.Kind, a.To = table.ActionFold, st.StreetIn[actor]
			st.Closed, st.Folded = true, actor
			i++
		case 'k':
			a.Kind, a.To = table.ActionCheck, st.StreetIn[actor]
			i++
		case 'c':
			// A call matches whatever the other seat has out there.
			owed := st.StreetIn[actor.Other()]
			a.Kind, a.To = table.ActionCall, owed
			i++
		case 'b':
			j := i + 1
			for j < len(chunk) && chunk[j] >= '0' && chunk[j] <= '9' {
				j++
			}
			if j == i+1 {
				return nil, fmt.Errorf("bet with no amount at %d", i)
			}
			n, err := strconv.Atoi(chunk[i+1 : j])
			if err != nil {
				return nil, fmt.Errorf("bet amount at %d: %w", i, err)
			}
			if n <= st.StreetIn[actor.Other()] && st.StreetIn[actor.Other()] > 0 {
				return nil, fmt.Errorf("b%d does not cover %d already out there",
					n, st.StreetIn[actor.Other()])
			}
			a.Kind, a.To = table.ActionBet, n
			if st.StreetIn[actor.Other()] > 0 {
				a.Kind = table.ActionRaise
			}
			i = j
		default:
			return nil, fmt.Errorf("unknown token %q at %d", chunk[i], i)
		}

		st.Committed[actor] += a.To - st.StreetIn[actor]
		st.StreetIn[actor] = a.To
		acts = append(acts, a)
		if st.Closed {
			return acts, nil
		}
		actor = actor.Other()
		st.ToAct = actor
	}
	return acts, nil
}
