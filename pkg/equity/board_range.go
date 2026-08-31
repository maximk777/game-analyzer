package equity

import (
	"sort"

	"poker-game-analyzer/pkg/evaluator"
	"poker-game-analyzer/pkg/table"
)

// Ranking a range by what it is on the board, rather than by what it was
// before the flop.
//
// This is the missing piece named in every one of the advisor's own comments
// about sizing. When the model asks "if I bet this much, the strongest tenth of
// their range calls -- what am I up against", the answer it was given came from
// a preflop hand ranking that had never looked at the board. On Tc Ad 5d As the
// strongest tenth of a *preflop* ranking is aces, kings and ace-king: hands
// which, on that board, are mostly a pair of tens with an ace kicker. The
// hands that actually call there are the ones holding an ace, and a preflop
// ranking cannot see them.
//
// The consequence was measurable and large. Against the simulated field the
// tool's ninetieth-percentile bet was sixty-six times the pot: the model
// believed a shove folded out 98% of the range and that the 2% which called was
// no stronger than the rest, so the largest size won the expected-value
// comparison almost every time. Ranking the range on the board is what makes
// the 2% look like what it is.
//
// A range is ranked once per board and cut at as many fractions as the caller
// asks for, because the sort is the expensive half and it does not depend on
// where the cut falls.

// BoardRanking is a range ordered by strength on a particular board, strongest
// first, with hero's cards and the board removed.
type BoardRanking struct {
	Combos [][2]table.Card
	// Preflop is true when the board was too short to rank on and the preflop
	// ordering was used instead. The caller may want to know that the answer is
	// the old approximation.
	Preflop bool
}

// RankOnBoard orders a range by what each holding is worth on the board as it
// stands.
//
// Made strength comes from the same evaluator the showdown uses. Draws are
// given a floor rather than their own scale: a flush draw with two cards to
// come is treated as no worse than two pair, because that is roughly how it
// fares all-in against a made hand and because the alternative -- ranking every
// draw below every pair -- says no draw ever calls a big bet, which is plainly
// false. The floor shrinks as the cards run out and is gone on the river, where
// a draw is nothing at all.
func RankOnBoard(hero [2]table.Card, board []table.Card, r Range) BoardRanking {
	if len(board) < 3 {
		// Nothing to rank against. The combos in a Range are stored in mask
		// order, so the preflop ordering has to be applied explicitly.
		out := make([][2]table.Card, 0, len(r.Combos))
		for _, c := range r.Combos {
			out = append(out, c)
		}
		sort.SliceStable(out, func(i, j int) bool {
			return HandPercentile(out[i]) < HandPercentile(out[j])
		})
		return BoardRanking{Combos: out, Preflop: true}
	}

	dead := CardToMask(hero[0]) | CardToMask(hero[1])
	for _, b := range board {
		dead |= CardToMask(b)
	}
	toCome := 5 - len(board)

	type scored struct {
		combo [2]table.Card
		key   uint32
	}
	ranked := make([]scored, 0, len(r.Combos))
	seven := make([]table.Card, 0, 7)
	for _, c := range r.Combos {
		if ComboToMask(c)&dead != 0 {
			continue
		}
		seven = append(seven[:0], c[0], c[1])
		seven = append(seven, board...)
		score, _ := evaluator.Evaluate7(seven)
		key := uint32(score)
		if f := drawFloor(seven, toCome); f > key {
			key = f
		}
		ranked = append(ranked, scored{c, key})
	}
	sort.Slice(ranked, func(i, j int) bool { return ranked[i].key > ranked[j].key })

	out := make([][2]table.Card, len(ranked))
	for i, s := range ranked {
		out[i] = s.combo
	}
	return BoardRanking{Combos: out}
}

// Top is the strongest `frac` of the ranking, as a Range ready to sample from.
// A fraction of one or more returns the whole of it; anything positive returns
// at least one holding, because a range that calls nothing is not a range.
func (b BoardRanking) Top(frac float64) Range {
	n := len(b.Combos)
	if n == 0 {
		return Range{}
	}
	if frac >= 1 {
		frac = 1
	}
	take := int(float64(n)*frac + 0.5)
	if take < 1 {
		take = 1
	}
	if take > n {
		take = n
	}
	out := Range{Combos: b.Combos[:take]}
	out.initMasks()
	return out
}

// drawFloor is the strength a drawing hand is credited with while cards are
// still to come.
func drawFloor(cards []table.Card, toCome int) uint32 {
	if toCome <= 0 {
		return 0
	}
	var suits [4]int
	var ranks [15]bool
	for _, c := range cards {
		suits[c.Suit]++
		ranks[c.Rank] = true
	}

	flushDraw := false
	for _, n := range suits {
		if n == 4 {
			flushDraw = true
		}
	}
	openEnded := false
	for start := table.RankTwo; start <= table.RankJack; start++ {
		if ranks[start] && ranks[start+1] && ranks[start+2] && ranks[start+3] {
			lowLive := start > table.RankTwo && !ranks[start-1]
			highLive := start+4 <= table.RankAce && !ranks[start+4]
			if lowLive || highLive {
				openEnded = true
			}
		}
	}

	switch {
	case flushDraw && toCome >= 2:
		return uint32(evaluator.TwoPair) << 24
	case flushDraw:
		return uint32(evaluator.OnePair) << 24
	case openEnded && toCome >= 2:
		return uint32(evaluator.OnePair) << 24
	}
	return 0
}
