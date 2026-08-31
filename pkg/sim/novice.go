package sim

import (
	"math"
	"math/rand"

	"poker-game-analyzer/pkg/table"
)

// Novice is the person the tool is actually for.
//
// Someone who knows what a flush is, learned the word "preflop" yesterday, and
// has no strategy of their own at all: whatever the panel says, they do. That
// is a different thing from measuring the advisor directly, and it is the more
// honest thing to measure, because it removes the one filter that flatters
// every advisory tool -- a competent user quietly ignoring the advice that is
// obviously wrong. A regular who saw "all-in for 66 times the pot" would not
// click it. This player clicks it.
//
// The deviations modelled are the ones beginners actually make, and they all
// lean the same way: towards passivity and towards curiosity. Nobody who does
// not know the game invents an aggressive line the tool did not suggest; they
// call when they were told to fold because they want to see it, and they check
// when they were told to bet because betting is frightening.
type Novice struct {
	tool *Tool
	rng  *rand.Rand

	// Discipline is how often the advice is followed exactly. One is a player
	// who is a pair of hands for the tool and nothing else.
	Discipline float64
	// RoundSizes makes the player type a human number instead of the exact
	// amount -- half the pot, the pot, twice the bet. Clients offer those as
	// buttons and that is what gets clicked.
	RoundSizes bool
}

// NewNovice wraps a tool in a person.
func NewNovice(tool *Tool, rng *rand.Rand, discipline float64) *Novice {
	return &Novice{tool: tool, rng: rng, Discipline: discipline, RoundSizes: true}
}

func (n *Novice) Name() string { return "novice:" + n.tool.Name() }

// Observer is the panel's tracker: the person is not learning anything, the
// tool in front of them is.
func (n *Novice) Observer() Observer { return n.tool.Observer() }

// NoAdviceCount is how many turns the panel had nothing to say, which a person
// still has to answer somehow.
func (n *Novice) NoAdviceCount() int { return n.tool.NoAdviceCount() }

func (n *Novice) Act(s Spot) Move {
	m := n.tool.Act(s)

	if n.rng.Float64() >= n.Discipline {
		m = n.slip(s, m)
	}
	if n.RoundSizes && (m.Action == table.ActionRaise || m.Action == table.ActionBet) {
		m.Amount = humanSize(s, m.Amount)
	}
	return m
}

// slip is the beginner's mistake, and it is always the same two mistakes.
func (n *Novice) slip(s Spot, m Move) Move {
	switch m.Action {
	case table.ActionFold:
		// Curiosity, but only at a price that feels small. Nobody new to the
		// game calls off a stack out of curiosity; they call the small one.
		pot := s.State.Pot
		if pot > 0 && s.ToCall/pot < 0.5 {
			return Move{Action: table.ActionCall}
		}
	case table.ActionRaise, table.ActionBet:
		if s.ToCall <= 0 {
			return Move{Action: table.ActionCheck}
		}
		return Move{Action: table.ActionCall}
	case table.ActionAllIn:
		// The one button a beginner hesitates over. Half the time they put in
		// something smaller instead, which is usually worse than either.
		if n.rng.Float64() < 0.5 && s.MaxRaise > 0 {
			return Move{Action: table.ActionRaise, Amount: math.Max(s.MinRaise, s.MaxRaise*0.5)}
		}
	}
	return m
}

// humanSize snaps an amount to something a person would actually click: the
// pot, half of it, three quarters, or twice what is owed.
func humanSize(s Spot, amount float64) float64 {
	pot := s.State.Pot
	if pot <= 0 {
		return amount
	}
	options := []float64{pot * 0.5, pot * 0.75, pot, pot * 1.5, s.ToCall * 2, s.ToCall * 3}
	best, bestGap := amount, math.Inf(1)
	for _, o := range options {
		if o < s.MinRaise || o > s.MaxRaise {
			continue
		}
		if gap := math.Abs(o - amount); gap < bestGap {
			best, bestGap = o, gap
		}
	}
	// A size far from every human button is one the player types in as it
	// stands -- which is what a shove is.
	if bestGap > pot {
		return amount
	}
	return best
}
