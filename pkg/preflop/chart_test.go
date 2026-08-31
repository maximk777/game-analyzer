package preflop

import (
	"poker-game-analyzer/pkg/equity"
	"testing"

	"poker-game-analyzer/pkg/table"
)

func hole(t *testing.T, s string) [2]table.Card {
	t.Helper()
	cards, err := table.ParseCards(s)
	if err != nil || len(cards) != 2 {
		t.Fatalf("parsing %q: %v", s, err)
	}
	return [2]table.Card{cards[0], cards[1]}
}

// A bad range token widens to the whole deck under the lenient parser, which
// would silently turn a typo into "raise everything".
func TestCharts_AllParse(t *testing.T) {
	if err := Validate(); err != nil {
		t.Fatalf("a chart range does not parse: %v", err)
	}
}

// The two hands that exposed why preflop needs charts at all. Both were folded
// by the equity-against-pot-odds comparison.
func TestCharts_HandsTheEquityModelGotWrong(t *testing.T) {
	cases := []struct {
		name      string
		hand      string
		position  table.Position
		situation Situation
		want      Action
	}{
		{"pocket threes defend the big blind", "3h 3c", table.PosBB, FacingRaise, Call},
		{"ace-king three-bets from the big blind", "Ac Kc", table.PosBB, FacingRaise, Raise},
		{"ace-king opens under the gun", "Ac Kc", table.PosUTG, Unopened, Raise},
		{"queen-jack offsuit folds under the gun", "Qc Jd", table.PosUTG, Unopened, Fold},
		{"seven-deuce folds on the button", "7h 2d", table.PosBTN, Unopened, Fold},
		{"suited ace opens on the button", "Ad 4d", table.PosBTN, Unopened, Raise},
		{"aces four-bet facing a three-bet", "As Ah", table.PosCO, FacingThreeBet, Raise},
		{"jacks call a three-bet", "Js Jh", table.PosCO, FacingThreeBet, Call},
		{"ace-queen offsuit folds to a three-bet under the gun", "Ac Qd", table.PosUTG, FacingThreeBet, Fold},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := Recommend(c.position, c.situation, hole(t, c.hand))
			if !ok {
				t.Fatalf("the charts do not cover %s %s", c.position, c.situation)
			}
			if got != c.want {
				t.Errorf("%s from %s %s: got %s, want %s", c.hand, c.position, c.situation, got, c.want)
			}
		})
	}
}

// Opening ranges must widen towards the button; a chart that tightens in late
// position has been transcribed wrong.
func TestCharts_OpeningRangesWidenWithPosition(t *testing.T) {
	order := []table.Position{table.PosUTG, table.PosMP, table.PosCO, table.PosBTN}

	counts := make([]int, len(order))
	for i, pos := range order {
		entry := compiled[spot{pos, Unopened}]
		counts[i] = len(entry.raise.Combos)
	}

	for i := 1; i < len(counts); i++ {
		if counts[i] <= counts[i-1] {
			t.Errorf("%s opens %d combos, no wider than %s at %d",
				order[i], counts[i], order[i-1], counts[i-1])
		}
	}
}

// Nothing is charted without a position, and nothing is guessed.
func TestCharts_UnknownPositionIsNotGuessed(t *testing.T) {
	if _, ok := Recommend("", Unopened, hole(t, "As Ah")); ok {
		t.Error("the charts answered for an unknown position")
	}
}

func TestSituationOf_CountsAggression(t *testing.T) {
	base := table.HandState{HeroID: "hero", Seats: []table.SeatState{
		{PlayerID: "hero"},
		{PlayerID: "a"},
		{PlayerID: "b"},
	}}

	if got := SituationOf(base); got != Unopened {
		t.Errorf("no raises: got %s, want %s", got, Unopened)
	}

	base.Seats[1].LastAction = "raise"
	if got := SituationOf(base); got != FacingRaise {
		t.Errorf("one raise: got %s, want %s", got, FacingRaise)
	}

	base.Seats[2].LastAction = "raise"
	if got := SituationOf(base); got != FacingThreeBet {
		t.Errorf("two raises: got %s, want %s", got, FacingThreeBet)
	}

	// A player who folded is no longer applying pressure.
	base.Seats[2].IsFolded = true
	if got := SituationOf(base); got != FacingRaise {
		t.Errorf("a folded raiser still counted: got %s, want %s", got, FacingRaise)
	}
}

// A limped pot is not an unopened one. Treating it as such priced the small
// blind out of completing for a fraction of a pot it was already invested in:
// live, hero held J3s in the small blind, owed 1,000 into 8,920, and the chart
// said fold while the EV model scored the call at +481.
func TestCharts_LimpedPotIsNotAnUnopenedPot(t *testing.T) {
	hand := hole(t, "Jd 3d")

	got, ok := Recommend(table.PosSB, FacingLimpers, hand)
	if !ok {
		t.Fatal("the charts do not cover the small blind against limpers")
	}
	if got == Fold {
		t.Error("folded the small blind against limpers at nine to one")
	}

	// Folded round, with nobody in the pot, the same hand is not worth playing.
	got, ok = Recommend(table.PosSB, Unopened, hand)
	if !ok {
		t.Fatal("the charts do not cover an unopened small blind")
	}
	if got != Fold {
		t.Errorf("J3s should not open the small blind unopened, got %s", got)
	}
}

// Callers with no raise among them are limpers; a raise outranks them.
func TestSituationOf_DistinguishesLimpersFromRaises(t *testing.T) {
	state := table.HandState{HeroID: "hero", Seats: []table.SeatState{
		{PlayerID: "hero"},
		{PlayerID: "a", LastAction: "call"},
		{PlayerID: "b", LastAction: "call"},
	}}
	if got := SituationOf(state); got != FacingLimpers {
		t.Errorf("two callers: got %s, want %s", got, FacingLimpers)
	}

	state.Seats[2].LastAction = "raise"
	if got := SituationOf(state); got != FacingRaise {
		t.Errorf("a raise behind a limper: got %s, want %s", got, FacingRaise)
	}

	state.Seats[1].LastAction = ""
	state.Seats[2].LastAction = ""
	if got := SituationOf(state); got != Unopened {
		t.Errorf("nobody in: got %s, want %s", got, Unopened)
	}
}

// The width of every chart, pinned.
//
// The comment above the charts used to state percentages that the ranges did
// not have: 15% under the gun against 6.9% actual, 42% on the button against
// 26%. Nobody had decided to play half as many hands as the file said; the two
// had simply drifted, and the tool played the drift. A percentage in a comment
// is a wish. This is the same statement as a test.
//
// The tolerance is two points, which is as tight as a range built out of whole
// hand classes can be held.
func TestChartWidths(t *testing.T) {
	all := equity.ParseRange("random")
	const combos = 1326.0

	width := func(pos table.Position, sit Situation) (raise, total float64) {
		var r, c int
		for _, hole := range all.Combos {
			a, ok := Recommend(pos, sit, hole)
			if !ok {
				continue
			}
			switch a {
			case Raise:
				r++
			case Call:
				c++
			}
		}
		return float64(r) / combos * 100, float64(r+c) / combos * 100
	}

	cases := []struct {
		pos       table.Position
		sit       Situation
		wantRaise float64
		wantTotal float64
	}{
		{table.PosUTG, Unopened, 15, 15},
		{table.PosMP, Unopened, 19, 19},
		{table.PosCO, Unopened, 28, 28},
		{table.PosBTN, Unopened, 42, 42},
		{table.PosSB, Unopened, 44, 44},
		{table.PosBB, FacingRaise, 3, 31},
	}
	for _, c := range cases {
		r, total := width(c.pos, c.sit)
		if diff := r - c.wantRaise; diff > 2 || diff < -2 {
			t.Errorf("%s %s: raises %.1f%% of hands, documented as %.0f%%", c.pos, c.sit, r, c.wantRaise)
		}
		if diff := total - c.wantTotal; diff > 2 || diff < -2 {
			t.Errorf("%s %s: plays %.1f%% of hands, documented as %.0f%%", c.pos, c.sit, total, c.wantTotal)
		}
		t.Logf("%-3s %-14s raise %5.1f%%  play %5.1f%%", c.pos, c.sit, r, total)
	}
}
