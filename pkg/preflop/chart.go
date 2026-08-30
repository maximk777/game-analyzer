// Package preflop decides preflop actions from positional charts rather than
// from equity against pot odds.
//
// The equity comparison the advisor uses after the flop is structurally wrong
// before it. It prices a call as though the hand were about to be shown down,
// which ignores everything that makes a preflop call worth making: position,
// the streets still to be played, and above all implied odds. Live, that folded
// pocket threes getting better than 2 to 1 with thirty-seven calls behind --
// a hand whose whole value is flopping a set and winning a stack -- and folded
// ace-king in the blinds. No amount of tuning that comparison fixes either
// hand, because the value it is measuring is not the value being offered.
//
// The charts below are a standard 6-max baseline at around 100 big blinds. They
// are not solver output and are not claimed to be: they are the reference every
// competent player already carries, which is a far better answer than a
// calculation that cannot see past the flop.
package preflop

import (
	"fmt"
	"sync"

	"poker-game-analyzer/pkg/equity"
	"poker-game-analyzer/pkg/table"
)

// Situation is the preflop spot hero faces.
type Situation string

const (
	// Unopened: folded round to hero, with nobody yet in the pot.
	Unopened Situation = "unopened"
	// FacingLimpers: players have entered by calling, but nobody has raised.
	// Treating this as an unopened pot priced the small blind out of completing
	// for a fraction of a pot it was already invested in -- a fold at nine to
	// one, which no opening chart is meant to describe.
	FacingLimpers Situation = "facing_limpers"
	// FacingRaise: exactly one raise ahead of hero.
	FacingRaise Situation = "facing_raise"
	// FacingThreeBet: hero raised and was raised again.
	FacingThreeBet Situation = "facing_3bet"
)

// Action is what the chart says to do.
type Action string

const (
	Raise Action = "raise"
	Call  Action = "call"
	Fold  Action = "fold"
)

// spot keys a chart entry.
type spot struct {
	position  table.Position
	situation Situation
}

// chartText holds the ranges as written, so they can be read and argued with
// directly. Anything not covered by the raise or call range is a fold.
type chartText struct {
	raise string
	call  string
}

// Opening ranges, roughly 15% under the gun widening to 42% on the button.
// The big blind has no unopened spot: it can see the flop for nothing.
var charts = map[spot]chartText{
	{table.PosUTG, Unopened}: {raise: "77+, ATs+, KQs, AQo+"},
	{table.PosMP, Unopened}:  {raise: "66+, A9s+, KTs+, QTs+, JTs, AJo+, KQo"},
	{table.PosCO, Unopened}:  {raise: "33+, A2s+, K9s+, Q9s+, J9s+, T9s, 98s, ATo+, KJo+"},
	{table.PosBTN, Unopened}: {raise: "22+, A2s+, K5s+, Q8s+, J8s+, T8s, 98s, 87s, 76s, 65s, A8o+, KTo+, QTo+, JTo"},
	{table.PosSB, Unopened}:  {raise: "22+, A2s+, K7s+, Q8s+, J8s+, T8s, 98s, 87s, 76s, 65s, A7o+, KTo+, QTo+, JTo"},

	// Limped pots. The blinds are getting a price no opening range reflects, so
	// completing is close to automatic and the chart's job is only to say which
	// hands are worth raising instead.
	{table.PosSB, FacingLimpers}: {
		raise: "77+, A9s+, KTs+, QTs+, JTs, AJo+, KQo",
		call:  "22+, A2s+, K2s+, Q2s+, J2s+, T2s+, 92s+, 82s+, 72s+, 62s+, 52s+, 42s+, 32s, A2o+, K5o+, Q8o+, J8o+, T8o+, 97o+, 87o, 76o, 65o",
	},
	{table.PosBB, FacingLimpers}: {
		raise: "66+, A8s+, KTs+, QTs+, JTs, ATo+, KQo",
		call:  "22+, A2s+, K2s+, Q2s+, J2s+, T2s+, 92s+, 82s+, 72s+, 62s+, 52s+, 42s+, 32s, A2o+, K2o+, Q2o+, J2o+, T2o+, 92o+, 82o+, 72o+, 62o+, 52o+, 42o+, 32o",
	},
	// In position behind limpers, raising is the point; the calling range stays
	// tight because there is no price being offered.
	{table.PosBTN, FacingLimpers}: {
		raise: "22+, A2s+, K8s+, Q9s+, J9s+, T9s, 98s, 87s, A9o+, KTo+, QTo+, JTo",
		call:  "K2s-K7s, Q2s-Q8s, J2s-J8s, T2s-T8s, 97s, 86s, 76s, 65s, A2o-A8o",
	},
	{table.PosCO, FacingLimpers}: {
		raise: "22+, A2s+, K9s+, Q9s+, J9s+, T9s, 98s, ATo+, KJo+",
		call:  "K2s-K8s, Q2s-Q8s, J2s-J8s, T8s, 87s, 76s, 65s",
	},
	{table.PosMP, FacingLimpers}: {
		raise: "55+, A8s+, KTs+, QTs+, JTs, T9s, AJo+, KQo",
		call:  "22-44, A2s-A7s, K9s, Q9s, J9s, 98s, 87s",
	},
	{table.PosUTG, FacingLimpers}: {
		raise: "66+, ATs+, KJs+, QJs, AJo+, KQo",
		call:  "22-55, A2s-A9s, KTs, QTs, JTs, T9s",
	},

	// Facing a single raise. The raise range is the three-bet; the call range
	// is everything worth continuing with but not worth three-betting.
	{table.PosMP, FacingRaise}: {
		raise: "JJ+, AQs+, AKo",
		call:  "22-TT, AJs, ATs, KQs, QJs, JTs",
	},
	{table.PosCO, FacingRaise}: {
		raise: "JJ+, AQs+, AKo",
		call:  "22-TT, AJs, ATs, KQs, KJs, QJs, JTs, T9s",
	},
	{table.PosBTN, FacingRaise}: {
		raise: "TT+, AQs+, AKo",
		call:  "22-99, AJs, ATs, KQs, KJs, QJs, JTs, T9s, 98s, AQo",
	},
	{table.PosSB, FacingRaise}: {
		raise: "TT+, AQs+, AKo",
		call:  "22-99, AJs, ATs, KQs, QJs, JTs",
	},
	{table.PosBB, FacingRaise}: {
		raise: "JJ+, AQs+, AKo",
		call:  "22-TT, A2s+, K9s+, Q9s+, J9s+, T9s, 98s, 87s, 76s, 65s, A8o+, KJo+, QJo, JTo",
	},

	// Facing a three-bet after opening. Position matters far less here than the
	// sheer strength needed to continue.
	{table.PosUTG, FacingThreeBet}: {raise: "QQ+, AKs", call: "JJ, TT, AQs, AKo"},
	{table.PosMP, FacingThreeBet}:  {raise: "QQ+, AKs", call: "JJ, TT, AQs, AKo"},
	{table.PosCO, FacingThreeBet}:  {raise: "QQ+, AKs, AKo", call: "JJ, TT, 99, AQs, AJs, KQs"},
	{table.PosBTN, FacingThreeBet}: {raise: "QQ+, AKs, AKo", call: "JJ, TT, 99, AQs, AJs, KQs"},
	{table.PosSB, FacingThreeBet}:  {raise: "QQ+, AKs, AKo", call: "JJ, TT, AQs"},
	{table.PosBB, FacingThreeBet}:  {raise: "QQ+, AKs, AKo", call: "JJ, TT, AQs, AJs"},
}

type compiledChart struct {
	raise equity.Range
	call  equity.Range
}

var (
	compileOnce sync.Once
	compiled    map[spot]compiledChart
	compileErr  error
)

// compile parses every chart once. Parsing is strict: an unrecognised token
// widens a range to the whole deck under the lenient parser, which would turn
// a typo into "raise everything".
func compile() {
	compiled = make(map[spot]compiledChart, len(charts))
	for key, text := range charts {
		var entry compiledChart
		if text.raise != "" {
			r, err := equity.ParseRangeStrict(text.raise)
			if err != nil {
				compileErr = fmt.Errorf("%s %s raise range: %w", key.position, key.situation, err)
				return
			}
			entry.raise = r
		}
		if text.call != "" {
			r, err := equity.ParseRangeStrict(text.call)
			if err != nil {
				compileErr = fmt.Errorf("%s %s call range: %w", key.position, key.situation, err)
				return
			}
			entry.call = r
		}
		compiled[key] = entry
	}
}

// Validate reports whether every chart parses. Tests call it so a bad range is
// a build failure rather than silent bad advice.
func Validate() error {
	compileOnce.Do(compile)
	return compileErr
}

// Recommend returns the charted action for a holding, and whether the charts
// cover the spot at all. They do not cover an unknown position, and there is
// deliberately no default: a guess dressed as a chart is worse than saying
// nothing.
func Recommend(position table.Position, situation Situation, hole [2]table.Card) (Action, bool) {
	compileOnce.Do(compile)
	if compileErr != nil {
		return "", false
	}
	if hole[0].Rank == 0 || hole[1].Rank == 0 {
		return "", false
	}

	entry, ok := compiled[spot{position, situation}]
	if !ok {
		return "", false
	}

	if len(entry.raise.Combos) > 0 && entry.raise.Contains(hole) {
		return Raise, true
	}
	if len(entry.call.Combos) > 0 && entry.call.Contains(hole) {
		return Call, true
	}
	return Fold, true
}

// SituationOf works out which preflop spot hero is in from the actions visible
// on the table.
//
// The nameplate badges show only each player's most recent action, so this
// counts aggression rather than reconstructing the betting sequence: no raise
// seen means the pot is unopened, one means hero faces a raise, and more than
// one means the raising has already been re-raised.
func SituationOf(state table.HandState) Situation {
	raises := 0
	limpers := 0
	for _, seat := range state.Seats {
		if seat.PlayerID == state.HeroID || seat.IsFolded {
			continue
		}
		switch seat.LastAction {
		case "raise", "bet", "all-in":
			raises++
		case "call":
			limpers++
		}
	}

	switch {
	case raises >= 2:
		return FacingThreeBet
	case raises == 1:
		return FacingRaise
	case limpers > 0:
		return FacingLimpers
	default:
		return Unopened
	}
}

// HeroPosition returns hero's charted position, and whether it is known.
func HeroPosition(state table.HandState) (table.Position, bool) {
	for _, seat := range state.Seats {
		if seat.PlayerID == state.HeroID && state.HeroID != "" {
			if seat.Position == "" {
				return "", false
			}
			return seat.Position, true
		}
	}
	return "", false
}
