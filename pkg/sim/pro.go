package sim

import (
	"math"
	"math/rand"

	"poker-game-analyzer/pkg/equity"
	"poker-game-analyzer/pkg/evaluator"
	"poker-game-analyzer/pkg/preflop"
	"poker-game-analyzer/pkg/table"
)

// Pro is the seasoned opponent: the player the tool actually has to beat.
//
// The archetypes in bots.go are a population -- they exist so that a win rate
// means something about a real table, and most of them are there to lose money
// in the particular ways real players lose it. None of them is hard. A strategy
// can beat all of them by folding a lot and value-betting, which is roughly
// what the tool does, and a harness that says so has not tested anything.
//
// This one is built to be hard, along the axes the archetypes are soft on:
//
//   - It opens off the same positional charts the tool does, so nothing is
//     given away before the flop and the measurement is about postflop play.
//   - It knows what its own hand is, not only what its equity is. A pair with
//     no redraw and a flush draw with none can share an equity number and want
//     completely different lines.
//   - It sizes by board texture rather than by a fixed fraction, and it does
//     not put a stack in without a hand that wants the stack in. That single
//     rule is what separates a regular from a maniac.
//   - It semi-bluffs with draws and gives up with air, instead of bluffing at
//     random. Random bluffing is what makes an aggressive bot a losing bot.
//
// It is a strong heuristic player, not a solver, and it is not claimed to be
// unexploitable. It is claimed to be a fair test.
type Pro struct {
	rng   *rand.Rand
	iters int
	// aggression scales how often the marginal spots are played aggressively,
	// so several pros at a table are not the same player.
	aggression float64
}

// NewPro seats a regular.
func NewPro(rng *rand.Rand) *Pro {
	return &Pro{rng: rng, iters: 900, aggression: 0.85 + 0.3*rng.Float64()}
}

func (p *Pro) Name() string { return "pro" }

func (p *Pro) Act(s Spot) Move {
	if s.State.Street == table.StreetPreflop {
		return p.preflop(s)
	}
	return p.postflop(s)
}

// preflop plays the standard six-max charts, which is what a competent regular
// is doing whether or not they would put it that way.
func (p *Pro) preflop(s Spot) Move {
	pos, known := preflop.HeroPosition(s.State)
	if !known {
		return foldOrCheck(s)
	}
	sit := preflop.SituationOf(s.State)
	act, charted := preflop.Recommend(pos, sit, s.State.HeroCards)
	if !charted {
		return foldOrCheck(s)
	}

	// Short stacks: the chart's raise becomes a shove, because there is no
	// postflop left to play for.
	if effectiveBB(s) <= 18 && act == preflop.Raise {
		return Move{Action: table.ActionAllIn}
	}

	switch act {
	case preflop.Raise:
		switch sit {
		case preflop.Unopened:
			return p.raise(s, 2.5-s.ToCall+s.ToCall) // an open to 2.5bb
		case preflop.FacingLimpers:
			limpers := 0.0
			for _, seat := range s.State.Seats {
				if seat.LastAction == "call" {
					limpers++
				}
			}
			return p.raise(s, 3.0+limpers)
		case preflop.FacingRaise:
			// Three-bet to three times what is owed, which is the usual shape
			// in position and a shade small out of it. Close enough either way.
			return p.raise(s, 3.0*s.ToCall)
		default:
			return p.raise(s, 2.3*s.ToCall)
		}
	case preflop.Call:
		return Move{Action: table.ActionCall}
	}
	return foldOrCheck(s)
}

func foldOrCheck(s Spot) Move {
	if s.ToCall <= 0 {
		return Move{Action: table.ActionCheck}
	}
	return Move{Action: table.ActionFold}
}

func (p *Pro) raise(s Spot, amount float64) Move {
	if s.MaxRaise <= 0 {
		return Move{Action: table.ActionCall}
	}
	if amount < s.MinRaise {
		amount = s.MinRaise
	}
	if amount >= s.MaxRaise*0.85 {
		return Move{Action: table.ActionAllIn}
	}
	return Move{Action: table.ActionRaise, Amount: amount}
}

func (p *Pro) postflop(s Spot) Move {
	opps := 0
	inPosition := true
	heroSeat := -1
	for i, seat := range s.State.Seats {
		if seat.PlayerID == s.State.HeroID {
			heroSeat = i
			continue
		}
		if !seat.IsFolded {
			opps++
		}
	}
	if opps < 1 {
		opps = 1
	}
	// Acting last is worth a great deal and costs nothing to notice: whoever
	// has already acted this street is in front of hero.
	for i, seat := range s.State.Seats {
		if i == heroSeat || seat.IsFolded {
			continue
		}
		if seat.LastAction == "" {
			inPosition = false
		}
	}

	width := opponentWidth(s.State)
	ranges := make([]equity.Range, opps)
	r := topRange(width)
	for i := range ranges {
		ranges[i] = r
	}
	sim := equity.SimulateEquityRNG(s.State.HeroCards, s.State.CommunityCards, ranges, p.iters, p.rng)
	eq := sim.WinRate + sim.TieRate*0.5

	made, draw := classify(s.State.HeroCards, s.State.CommunityCards)
	wet := boardWetness(s.State.CommunityCards)

	pot := s.State.Pot
	if pot <= 0 {
		pot = 1
	}

	// A stack goes in with a hand that wants it in. Everything else is sized
	// out of the pot, and the size is capped so that no single street can
	// commit what the hand has not earned.
	stackWorthy := made >= evaluator.TwoPair || eq >= 0.80
	commit := func(want float64) float64 {
		if !stackWorthy && want > s.MaxRaise*0.45 {
			want = s.MaxRaise * 0.45
		}
		return want
	}

	// Size by texture: a dry board needs a third of the pot to do the same
	// work that two thirds does on a wet one, and betting big into a dry board
	// only folds out the hands that were paying.
	size := 0.4
	if wet {
		size = 0.7
	}

	if s.ToCall > 0 {
		required := s.ToCall / (pot + s.ToCall)
		// A regular does not call at exactly the price. Being right on the line
		// means being wrong whenever the read is off, and the read is usually
		// off.
		margin := 0.03
		if !inPosition {
			margin = 0.06
		}

		switch {
		case eq >= 0.78 && p.rng.Float64() < p.aggression:
			return p.raise(s, commit(s.ToCall*2+pot*size))
		case draw >= drawStrong && eq >= required && p.rng.Float64() < 0.45*p.aggression:
			// Semi-bluff: the raise wins now often enough, and when it does not
			// there are outs behind it. This is the only bluff worth making.
			return p.raise(s, commit(s.ToCall*2+pot*size))
		case eq >= required+margin:
			return Move{Action: table.ActionCall}
		default:
			return Move{Action: table.ActionFold}
		}
	}

	switch {
	case eq >= 0.68:
		// Value. Bet it, and bet it bigger with more players to get through.
		return p.raise(s, commit(pot*size*(1+0.15*float64(opps-1))))
	case draw >= drawStrong && p.rng.Float64() < 0.6*p.aggression:
		return p.raise(s, commit(pot*size))
	case made == evaluator.HighCard && draw == drawNone && inPosition &&
		opps == 1 && p.rng.Float64() < 0.35*p.aggression:
		// A single bluff with nothing, heads-up and in position, where it is
		// cheapest and works most often. Any more than this and the bluffing
		// is what loses the money.
		return p.raise(s, commit(pot*0.45))
	}
	return Move{Action: table.ActionCheck}
}

// Draw strength, because a hand's equity does not say whether it is drawing.
const (
	drawNone = iota
	drawWeak
	drawStrong
)

// classify says what hero has made and what hero is drawing to.
//
// Equity alone cannot tell a pair with no way to improve from a flush draw with
// none: they can share a number and want opposite lines. The category comes
// from the same evaluator the showdown uses; the draw is counted off the
// suits and the ranks.
func classify(hole [2]table.Card, board []table.Card) (evaluator.HandCategory, int) {
	if len(board) < 3 {
		return evaluator.HighCard, drawNone
	}
	cards := append([]table.Card{hole[0], hole[1]}, board...)
	_, made := evaluator.Evaluate7(cards)

	// A draw is only a draw while there is a card to come.
	if len(board) >= 5 {
		return made, drawNone
	}

	suits := map[table.Suit]int{}
	var ranks [15]bool
	for _, c := range cards {
		suits[c.Suit]++
		ranks[c.Rank] = true
	}
	draw := drawNone
	for _, n := range suits {
		if n == 4 {
			draw = drawStrong
		}
	}
	// Straight draws: four to a straight open at one end is weak, at both ends
	// is strong. Counted by sliding a five-card window across the ranks, with
	// the wheel handled by treating the ace as a one as well.
	low := ranks[table.RankAce]
	for start := 1; start <= 10; start++ {
		n := 0
		for i := 0; i < 5; i++ {
			r := start + i
			if r == 1 {
				if low {
					n++
				}
				continue
			}
			if r <= 14 && ranks[r] {
				n++
			}
		}
		if n == 4 && draw < drawWeak {
			draw = drawWeak
		}
	}
	// Open-ended: four in a row with both ends live.
	for start := table.RankTwo; start <= table.RankJack; start++ {
		if ranks[start] && ranks[start+1] && ranks[start+2] && ranks[start+3] {
			lowEnd := start > table.RankTwo && !ranks[start-1]
			highEnd := start+4 <= table.RankAce && !ranks[start+4]
			if lowEnd || highEnd {
				draw = drawStrong
			}
		}
	}
	return made, draw
}

// boardWetness reports whether the board is one where hands connect: a flush
// draw available, or three cards inside a five-rank span.
func boardWetness(board []table.Card) bool {
	if len(board) < 3 {
		return false
	}
	suits := map[table.Suit]int{}
	var rs []int
	for _, c := range board {
		suits[c.Suit]++
		rs = append(rs, int(c.Rank))
	}
	for _, n := range suits {
		if n >= 2 {
			return true
		}
	}
	for i := range rs {
		for j := range rs {
			if i != j && math.Abs(float64(rs[i]-rs[j])) <= 4 {
				return true
			}
		}
	}
	return false
}
