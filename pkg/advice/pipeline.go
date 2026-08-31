// Package advice is the whole decision, from a table state to a
// recommendation: opponent ranges, equity, the conditional equity the sizing
// comparison needs, the risk profile, and the advisor itself.
//
// It was inside pkg/server, wound through the HTTP handler that produced it.
// That was fine while the only caller was the live path and became the central
// obstacle the moment there was a second one: a harness that plays the strategy
// out over a hundred thousand hands has to reach the same decision through the
// same code, or it is measuring a reimplementation of the tool rather than the
// tool. Everything here is a move, not a rewrite -- see pipeline_test.go, which
// pins the two against each other on real states.
package advice

import (
	"math/rand"
	"time"

	"poker-game-analyzer/pkg/advisor"
	"poker-game-analyzer/pkg/equity"
	"poker-game-analyzer/pkg/table"
)

// Reads is what is known about the opponents, keyed by player id.
type Reads struct {
	// Tendencies is what the profiler reports for a player: vpip, pfr,
	// hands_count, and whatever the language model added.
	Tendencies map[string]map[string]float64
	// RangeWidth is the share of hands a player enters with, as a percentage.
	// Absent or zero means unknown, which is a hundred.
	RangeWidth map[string]float64
}

// TendenciesFor is the read on one player, or nil.
func (r Reads) TendenciesFor(id string) map[string]float64 {
	if r.Tendencies == nil {
		return nil
	}
	return r.Tendencies[id]
}

// Options tunes the simulation. The zero value is the live setting.
type Options struct {
	// Iterations is the sample count for the headline equity number, and
	// VsTopIterations for each conditional slice. Zero means the live values.
	//
	// They are settings rather than constants because the harness runs
	// millions of these and the live path runs one every hundred milliseconds:
	// the accuracy that is cheap in one is the entire cost in the other.
	Iterations      int
	VsTopIterations int

	// Rng makes a run reproducible. Nil seeds from the clock, which is what the
	// live path wants and what a harness cannot use.
	Rng *rand.Rand

	// ActionRanges narrows each opponent's range by what they have done this
	// hand -- position, the raises they made, the streets they continued on --
	// instead of holding it at their VPIP for the whole hand and at the whole
	// deck when nothing is known about them. See ranges.go.
	//
	// It is a switch only so that the two can be measured against each other on
	// the same decks. See docs/HARNESS.md.
	ActionRanges bool

	// SizingPolicy decides which bet sizes exist in a spot from the board and
	// the stack-to-pot ratio, rather than letting the one-street expected-value
	// comparison pick from a fixed list it systematically overprices the top of.
	// See pkg/advisor/sizing.go. A switch for the same reason.
	SizingPolicy bool

	// SemiBluff lets a hand with outs bet without a counted read on every
	// opponent at the table. See advisor.Inputs.AllowSemiBluff.
	SemiBluff bool
}

const (
	liveIterations      = 12000
	liveVsTopIterations = 8000
)

// Result is the recommendation, or the reason there is not one.
type Result struct {
	Recommendation *advisor.AdvisorResponse
	// NoAdvice is a human-readable reason, in Russian, matching what the HUD
	// shows. Empty when there is a recommendation.
	NoAdvice string
	// SeatReads is the tendency map actually used, per seat, for the audit log.
	SeatReads map[string]map[string]float64
}

// Evaluate answers "what should hero do here", or says why the question does
// not arise.
func Evaluate(h *table.HandState, reads Reads, opt Options) Result {
	if h == nil {
		return Result{NoAdvice: "Нет состояния стола"}
	}

	hasHeroCards := h.HeroCards[0].Rank > 0 && h.HeroCards[1].Rank > 0

	// A player who has folded has no decision left. The fold badge on the
	// nameplate is read, but nothing consulted it before advising: live, hero
	// had folded and the HUD went on recommending an all-in, sized off another
	// player's stack.
	heroFolded := false
	for _, seat := range h.Seats {
		if seat.PlayerID == h.HeroID && h.HeroID != "" && seat.IsFolded {
			heroFolded = true
			break
		}
	}

	switch {
	case !hasHeroCards:
		return Result{NoAdvice: "Карты героя не прочитаны"}
	case heroFolded:
		return Result{NoAdvice: "Вы сбросили — решать нечего"}
	case !h.HeroCanAct():
		return Result{NoAdvice: "Не ваш ход"}
	}

	iters := opt.Iterations
	if iters <= 0 {
		iters = liveIterations
	}
	vsTopIters := opt.VsTopIterations
	if vsTopIters <= 0 {
		vsTopIters = liveVsTopIterations
	}
	rng := opt.Rng
	if rng == nil {
		rng = rand.New(rand.NewSource(time.Now().UnixNano()))
	}

	var oppTendencies map[string]float64
	seatReads := make(map[string]map[string]float64)

	// One range per live opponent, always. Previously a range was only added
	// for players with recorded stats, so with no history the list came out
	// empty and equity was simulated against a single random hand -- no matter
	// how many players were actually in the pot. Range width is a percentage of
	// all hands: 100 means unknown.
	var rangeWidths []float64
	var oppReads []advisor.OpponentRead
	for _, seat := range h.Seats {
		if seat.PlayerID == "" || seat.PlayerID == h.HeroID || !seat.IsActive || seat.IsFolded {
			continue
		}
		width := 100.0
		t := reads.TendenciesFor(seat.PlayerID)
		if len(t) > 0 {
			seatReads[seat.PlayerID] = t
			if oppTendencies == nil {
				oppTendencies = t
			}
		}
		vpip, hasVPIP := reads.RangeWidth[seat.PlayerID]
		hasVPIP = hasVPIP && vpip > 0
		if hasVPIP {
			width = vpip
		}
		// What they have done this hand, which is a fact about this hand and
		// not about the player, and so is available whether or not anybody has
		// ever seen them before.
		if opt.ActionRanges {
			width = RangeWidthFor(*h, seat, vpip, hasVPIP)
		}
		rangeWidths = append(rangeWidths, width)

		// Each opponent is modelled separately. Whether the model is allowed to
		// believe any of it is decided per player by the sample behind it, so
		// the read on the regular who has been here two hundred hands does not
		// carry the seat that just sat down.
		o := advisor.OpponentRead{PlayerID: seat.PlayerID, Tendencies: t, Stack: seat.Stack}
		if n, ok := t["hands_count"]; ok && n > 0 {
			o.Hands = int(n)
		}
		oppReads = append(oppReads, o)
	}
	if len(rangeWidths) == 0 {
		rangeWidths = []float64{100.0}
	}

	rangesAt := func(frac float64) []equity.Range {
		out := make([]equity.Range, 0, len(rangeWidths))
		for _, w := range rangeWidths {
			out = append(out, equity.TopRange(w*frac))
		}
		return out
	}

	// The call decision is a threshold comparison against pot odds, and live it
	// sat 0.4% from the line: at 3,000 iterations the sampling noise alone
	// flipped the same state between call and fold from one frame to the next.
	eqResult := equity.SimulateEquityRNG(h.HeroCards, h.CommunityCards, rangesAt(1.0), iters, rng)

	// Equity against the part of the range that would call a given size.
	//
	// The slice is taken by strength *on this board*, not by the preflop
	// ranking, and that is the whole of the difference. Ranked before the flop,
	// the strongest twentieth of a range on Tc Ad 5d As is aces, kings and
	// ace-king, and hero's queens are 54% against it; ranked on the board it is
	// the hands holding an ace, and the queens are 4%. The tool shoved that
	// spot. Measured against a table of strong opponents it shoved a great many
	// like it -- its ninetieth-percentile bet was sixteen times the pot -- and
	// every one of them was priced off a range that had never seen the board.
	//
	// Each range is ranked once and cut as many times as the advisor asks,
	// because the sort is the expensive half and does not depend on the cut.
	// Preflop there is no board and the ranking falls back to the preflop
	// order, which makes this exactly what it was before the flop.
	baseRanges := rangesAt(1.0)
	rankings := make([]equity.BoardRanking, len(baseRanges))
	ranked := make([]bool, len(baseRanges))
	narrowedAt := func(frac float64) []equity.Range {
		out := make([]equity.Range, len(baseRanges))
		for i := range baseRanges {
			if !ranked[i] {
				rankings[i] = equity.RankOnBoard(h.HeroCards, h.CommunityCards, baseRanges[i])
				ranked[i] = true
			}
			out[i] = rankings[i].Top(frac)
		}
		return out
	}

	cache := make(map[int]float64, 8)
	equityVsTop := func(frac float64) float64 {
		if frac <= 0 {
			return 0
		}
		if frac > 1 {
			frac = 1
		}
		key := int(frac * 100)
		if v, ok := cache[key]; ok {
			return v
		}
		r := equity.SimulateEquityRNG(h.HeroCards, h.CommunityCards, narrowedAt(frac), vsTopIters, rng)
		v := r.WinRate + r.TieRate*0.5
		cache[key] = v
		return v
	}

	// What one opponent's range already beats hero with, counted exactly rather
	// than sampled. Equity says how often hero wins; this says what the losses
	// are made of, and on a paired board those are not the same question.
	var risk *equity.RiskProfile
	if len(h.CommunityCards) >= 3 {
		ranges := baseRanges
		widest := 0
		for i := range ranges {
			if len(ranges[i].Combos) > len(ranges[widest].Combos) {
				widest = i
			}
		}
		if len(ranges) > 0 {
			r := equity.Risk(h.HeroCards, h.CommunityCards, ranges[widest])
			risk = &r
		}
	}

	in := advisor.Inputs{
		State:         *h,
		Equity:        eqResult,
		OppTendencies: oppTendencies,
		Opponents:     oppReads,
		EquityVsTop:   equityVsTop,
		Risk:          risk,

		UseSizingPolicy: opt.SizingPolicy,
		AllowSemiBluff:  opt.SemiBluff,
	}

	advice := advisor.Calculate(in)
	return Result{Recommendation: &advice, SeatReads: seatReads}
}
