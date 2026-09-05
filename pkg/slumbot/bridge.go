package slumbot

import (
	"fmt"
	"math"

	"poker-game-analyzer/pkg/advice"
	"poker-game-analyzer/pkg/equity"
	"poker-game-analyzer/pkg/table"
)

// Preflop here is a fixed heads-up policy rather than the advisor's charts.
//
// pkg/preflop holds a 6-max chart set at 100 big blinds. Slumbot is heads-up at
// 200, where the button opens something close to every hand and the big blind
// defends most of what it is offered -- so those charts are not a weaker answer
// to this question, they are an answer to a different one. Running them would
// mix "the range model is wrong" with "the charts are for another game", and
// the whole point of measuring here is to tell the machinery from the strategy.
//
// The numbers below are not claimed to be good. They are claimed to be roughly
// the right shape for heads-up and, more importantly, to be fixed: they do not
// depend on the range model under test, so they cannot flatter it.
const (
	buttonOpenWidth  = 85.0
	bigBlindCallOpen = 60.0
	buttonVs3Bet     = 35.0
	deepPreflopWidth = 12.0
)

// Decision is one action the bridge took, with what the advisor believed when
// it took it.
type Decision struct {
	Street table.Street
	// AssumedWidth is the share of all hands, as a percentage, that the model
	// gave the opponent at this decision. This is the claim being tested.
	AssumedWidth float64
	// CallRangeFraction is the part of that range, ranked by strength on this
	// board, the call was priced against. Zero when there was no call to price.
	CallRangeFraction float64
	Action            table.ActionType
	Amount            int
	// FromAdvisor is false for the fixed preflop policy.
	FromAdvisor bool
	// VillainLast is the opponent's most recent action in this hand. It is what
	// splits the shape test: `polar` is a claim about a range that bet and
	// `capped` a claim about one that called, and without this they cannot be
	// told apart in the log.
	VillainLast table.ActionType
	Board       []table.Card
	HeroCards   [2]table.Card
	Pot         int
	Owed        int
}

// Anomaly is the bridge being asked for something the state said was impossible.
// It means the state handed to the advisor did not describe the spot, which is a
// bug worth failing loudly over rather than a hand worth playing on.
type Anomaly struct {
	Action table.ActionType
	Owed   int
	Reason string
}

// Decide picks hero's action and returns it in Slumbot's wire form.
func Decide(r *Response, st State, opt advice.Options) (incr string, d Decision, an *Anomaly, err error) {
	h, err := HandState(r, st)
	if err != nil {
		return "", Decision{}, nil, err
	}
	hero := r.HeroSeat()
	d = Decision{
		Street:    st.Street,
		Board:     h.CommunityCards,
		HeroCards: h.HeroCards,
		Pot:       st.Pot(),
		Owed:      st.Owed(),
	}
	// The width the model gives the opponent is recorded whatever we then do
	// with it, including preflop where our own action does not come from it.
	// A preflop belief is still a belief, and the cards at the end judge it.
	d.AssumedWidth = advice.RangeWidthFor(*h, VillainSeatState(h), 0, false, opt.Shape)
	d.VillainLast = lastActionBy(st, hero.Other())

	if st.Street == table.StreetPreflop {
		incr, d.Action, d.Amount = preflopPolicy(st, hero, h)
		return incr, d, nil, nil
	}

	res := advice.Evaluate(h, advice.Reads{}, opt)
	if res.Recommendation == nil {
		// No advice is a real answer -- check when it is free, fold when it is
		// not -- and matches what pkg/sim does with the same state.
		if st.Owed() == 0 {
			d.Action = table.ActionCheck
			return "k", d, nil, nil
		}
		d.Action = table.ActionFold
		return "f", d, nil, nil
	}

	rec := res.Recommendation
	d.FromAdvisor = true
	d.CallRangeFraction = rec.CallRangeFraction
	d.Action = rec.PrimaryAction

	switch rec.PrimaryAction {
	case table.ActionFold:
		if st.Owed() == 0 {
			// Folding for free is never right and never offered; the advisor
			// asking for it means the state did not say checking was possible.
			d.Action = table.ActionCheck
			return "k", d, &Anomaly{rec.PrimaryAction, st.Owed(), "fold with nothing owed"}, nil
		}
		return "f", d, nil, nil
	case table.ActionCheck:
		if st.Owed() > 0 {
			d.Action = table.ActionFold
			return "f", d, &Anomaly{rec.PrimaryAction, st.Owed(), "check facing a bet"}, nil
		}
		return "k", d, nil, nil
	case table.ActionCall:
		if st.Owed() == 0 {
			d.Action = table.ActionCheck
			return "k", d, nil, nil
		}
		return "c", d, nil, nil
	case table.ActionAllIn:
		to := maxTo(st, hero)
		d.Amount = to
		return fmt.Sprintf("b%d", to), d, nil, nil
	default:
		// Bet and raise are the same move, and the advisor states it as chips
		// added now (pkg/sim/engine.go:82). Slumbot wants the actor's total for
		// the street. Adding hero's existing wager is the whole conversion, and
		// leaving it out underbets every raised pot by exactly what is already in.
		to := raiseTo(st, hero, rec.RecommendedAmount)
		d.Amount = to
		return fmt.Sprintf("b%d", to), d, nil, nil
	}
}

// raiseTo converts the advisor's size into Slumbot's.
//
// The advisor states a raise as chips added now (pkg/sim/engine.go:82). Slumbot
// wants the actor's total for the street. The two agree only in an unraised pot,
// which is why getting it wrong survives a smoke test: it underbets every raised
// pot by exactly what hero already had out, and every bet still parses.
func raiseTo(st State, hero Seat, addChips float64) int {
	return clampTo(st.StreetIn[hero]+int(math.Round(addChips)), st, hero)
}

// lastActionBy is a seat's most recent act in the hand, or empty if it has not
// acted.
func lastActionBy(st State, s Seat) table.ActionType {
	for i := len(st.Acts) - 1; i >= 0; i-- {
		for j := len(st.Acts[i]) - 1; j >= 0; j-- {
			if st.Acts[i][j].Actor == s {
				return st.Acts[i][j].Kind
			}
		}
	}
	return ""
}

// maxTo is the largest street total hero can put out: everything behind, plus
// what is already out there.
func maxTo(st State, hero Seat) int {
	return Stack - st.Committed[hero] + st.StreetIn[hero]
}

func clampTo(to int, st State, hero Seat) int {
	if hi := maxTo(st, hero); to > hi {
		return hi
	}
	if lo := minRaiseTo(st); to < lo {
		// Naming a size is a decision to put money in, so a size too small to
		// be legal becomes the smallest legal one rather than a call. This is
		// the rule pkg/sim/engine.go already applies to the same request.
		if lo <= maxTo(st, hero) {
			return lo
		}
		return maxTo(st, hero)
	}
	return to
}

// preflopPolicy is the fixed heads-up opening and defending scheme.
func preflopPolicy(st State, hero Seat, h *table.HandState) (incr string, act table.ActionType, amount int) {
	pct := equity.HandPercentile(h.HeroCards) * 100
	raises := 0
	for _, a := range st.Acts[0] {
		if a.Kind == table.ActionBet || a.Kind == table.ActionRaise {
			raises++
		}
	}

	switch {
	case raises == 0:
		// Button, first in. Open to two big blinds or give up the small one.
		if pct <= buttonOpenWidth {
			to := clampTo(2*BigBlind, st, hero)
			return fmt.Sprintf("b%d", to), table.ActionRaise, to
		}
		return "f", table.ActionFold, 0
	case raises == 1 && st.Owed() > 0:
		// Facing the open. Flat it or fold; three-betting is left out on
		// purpose, because every hand that goes to a flop is a hand this run
		// can measure and a three-bet pot is the one our own range shapes most.
		if pct <= bigBlindCallOpen {
			return "c", table.ActionCall, st.StreetIn[hero.Other()]
		}
		return "f", table.ActionFold, 0
	case raises == 2:
		if pct <= buttonVs3Bet {
			return "c", table.ActionCall, st.StreetIn[hero.Other()]
		}
		return "f", table.ActionFold, 0
	case st.Owed() > 0:
		if pct <= deepPreflopWidth {
			return "c", table.ActionCall, st.StreetIn[hero.Other()]
		}
		return "f", table.ActionFold, 0
	default:
		return "k", table.ActionCheck, st.StreetIn[hero]
	}
}
