package advisor

import (
	"fmt"
	"math"

	"poker-game-analyzer/pkg/table"
)

// Which sizes are on the menu, and why the expected-value comparison is not
// allowed to answer that question by itself.
//
// The comparison is a one-street model. It prices a bet by how often the field
// folds now and what happens if somebody calls now, and it has no term at all
// for the streets after. Fold equity rises with size, so the biggest size on
// the list wins the comparison far more often than it should -- and the biggest
// size is always the stack. Measured against the population, the tool's
// ninety-ninth-percentile flop bet was thirteen times the pot: an open shove of
// a hundred big blinds into seven, with a hand that had no business risking it.
//
// The same blindness was already settled once, before the flop, by taking the
// choice away from the comparison and giving it to a chart. This is that repair
// after the flop. It does not price anything; it says which sizes a hand may
// even be offered, from the two things a one-street model cannot see:
//
//   - the board, which decides how much of the field has equity worth charging
//     and therefore how big a bet has to be to charge it;
//   - the stack-to-pot ratio, which decides whether the money left is a threat
//     to be used over three streets or a pile to be shoved now.
//
// The numbers below are the standard reference. Small and frequent on dry,
// static boards -- 25 to 33% of the pot, where equilibrium continuation-betting
// ranges run 60 to 70% of range; larger and less frequent on boards where
// draws are live -- 66 to 100%, where the bet has a job beyond folding out air.
// Monotone and straight-completing boards get the smallest frequencies of all.
// Multiway, everything tightens: equilibrium continuation-betting drops from
// around 70% heads-up to around 35% three-handed, because a second caller has
// to be beaten too.

// Texture is the board in the terms a sizing decision is made in.
type Texture struct {
	Paired    bool
	Monotone  bool
	TwoTone   bool
	Connected bool
	// Wet runs 0 for a rainbow, disconnected, low board to 1 for one where a
	// flush and a straight are both live.
	Wet float64
}

// ReadTexture classifies the community cards. A board of fewer than three cards
// has no texture and comes back as the zero value, which reads as dry.
func ReadTexture(board []table.Card) Texture {
	var t Texture
	if len(board) < 3 {
		return t
	}

	suits := map[table.Suit]int{}
	counts := map[table.Rank]int{}
	ranks := make([]int, 0, len(board))
	for _, c := range board {
		suits[c.Suit]++
		counts[c.Rank]++
		ranks = append(ranks, int(c.Rank))
	}

	for _, n := range counts {
		if n >= 2 {
			t.Paired = true
		}
	}
	most := 0
	for _, n := range suits {
		if n > most {
			most = n
		}
	}
	switch {
	case most >= 3:
		t.Monotone = true
	case most == 2:
		t.TwoTone = true
	}

	// Connected: any two board cards within four ranks of each other, which is
	// the span a straight draw can bridge. The ace plays low as well.
	gaps := 0
	for i := range ranks {
		for j := i + 1; j < len(ranks); j++ {
			d := int(math.Abs(float64(ranks[i] - ranks[j])))
			if ranks[i] == int(table.RankAce) || ranks[j] == int(table.RankAce) {
				if alt := int(math.Abs(float64(min(ranks[i], ranks[j]) - 1))); alt < d {
					d = alt
				}
			}
			if d <= 4 {
				gaps++
			}
		}
	}
	t.Connected = gaps >= 2

	wet := 0.0
	if t.TwoTone {
		wet += 0.35
	}
	if t.Monotone {
		wet += 0.60
	}
	if t.Connected {
		wet += 0.40
	} else if gaps == 1 {
		wet += 0.15
	}
	// A pair on the board takes cards out of the deck that would otherwise make
	// straights and flushes, and it is the one feature that makes a board more
	// static rather than less.
	if t.Paired {
		wet -= 0.15
	}
	t.Wet = math.Min(math.Max(wet, 0), 1)
	return t
}

// SizingPolicy is which wagers a hand may be offered in one spot.
type SizingPolicy struct {
	// Fractions of the pot to offer, smallest first.
	Fractions []float64
	// AllIn is whether the stack may be put in at all.
	AllIn bool
}

// commitmentSPR is the stack-to-pot ratio below which the money is going in
// whatever anybody does, so offering it now costs nothing. Above it a shove is
// three streets of leverage spent in one.
const commitmentSPR = 1.5

// shoveEquity is the equity against the range that would call, above which a
// shove is a value bet rather than a size chosen because the model cannot see
// past the flop. Higher off the river, because a card is still to come.
const (
	shoveEquity      = 0.78
	shoveEquityRiver = 0.68
)

// PolicyFor is the menu of sizes for this street, board, depth and field.
//
// callEq is hero's equity against the part of the range that would call, which
// is the only equity a bet is answerable to. Zero when it could not be measured,
// and then no shove is offered on equity alone.
func PolicyFor(street table.Street, board []table.Card, spr float64, opponents int, callEq float64) SizingPolicy {
	t := ReadTexture(board)

	var p SizingPolicy
	switch {
	case street == table.StreetPreflop:
		// Preflop the charts decide, and the comparison only needs somewhere to
		// put the numbers.
		p.Fractions = []float64{0.33, 0.66, 1.00}
	case t.Wet >= 0.55:
		// Draws are live and a small bet charges nobody. Bet bigger, and the
		// frequency falls out of the comparison declining the marginal hands.
		p.Fractions = []float64{0.50, 0.75, 1.00}
	case t.Wet >= 0.30:
		p.Fractions = []float64{0.33, 0.60, 0.85}
	default:
		// Static and dry. The whole range can bet, and it bets small.
		p.Fractions = []float64{0.25, 0.33, 0.60}
	}

	// The stack is on the menu when it is going in anyway, or when there is a
	// hand to put in. It is never on the menu merely because it folds people
	// out: that is the term the one-street model overprices.
	want := shoveEquity
	if street == table.StreetRiver || street == table.StreetShowdown {
		want = shoveEquityRiver
	}
	p.AllIn = spr > 0 && spr <= commitmentSPR
	if callEq >= want {
		p.AllIn = true
	}

	// Multiway, every size has to beat one more range. The top size comes off
	// and the stack stays in the pocket unless the pot is already committed.
	if opponents >= 2 {
		if len(p.Fractions) > 2 {
			p.Fractions = p.Fractions[:2]
		}
		if callEq < want+0.07 {
			p.AllIn = spr > 0 && spr <= commitmentSPR
		}
	}
	return p
}

// potLabel names a fraction of the pot the way the interface shows it.
func potLabel(f float64) string {
	if f >= 1 {
		return "Pot"
	}
	return fmt.Sprintf("%.0f%% Pot", f*100)
}
