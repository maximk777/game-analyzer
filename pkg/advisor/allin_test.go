package advisor

import (
	"fmt"
	"testing"

	"poker-game-analyzer/pkg/equity"
	"poker-game-analyzer/pkg/table"
)

// liveShoveSpot rebuilds a decision the tool actually made, with the same
// opponent-range model the server supplies live: with no reads on a player,
// their range is every hand, ranked by preflop strength.
func liveShoveSpot(t *testing.T, hero, board string, pot, effStack float64, villains int) (table.HandState, Inputs) {
	t.Helper()

	heroCards := parseHeroCards(t, hero)
	boardCards := parseBoardCards(t, board)

	seats := []table.SeatState{
		{PlayerID: "hero", Stack: effStack, IsActive: true, Position: table.PosBTN},
	}
	for i := 0; i < villains; i++ {
		seats = append(seats, table.SeatState{
			PlayerID: fmt.Sprintf("v%d", i), Stack: effStack, IsActive: true, Position: table.PosBB,
		})
	}

	street := table.StreetFlop
	switch len(boardCards) {
	case 4:
		street = table.StreetTurn
	case 5:
		street = table.StreetRiver
	}

	state := table.HandState{
		HandID:         "live-shove",
		Street:         street,
		Pot:            pot,
		HeroCards:      heroCards,
		CommunityCards: boardCards,
		HeroID:         "hero",
		Seats:          seats,
	}

	// The server's model, reproduced exactly: one range per live opponent,
	// width 100% because nothing is known about them, narrowed to the strongest
	// `frac` for the part that would call a given size.
	rangesAt := func(frac float64) []equity.Range {
		out := make([]equity.Range, 0, villains)
		for i := 0; i < villains; i++ {
			w := 100.0 * frac
			if w >= 100 {
				out = append(out, equity.ParseRange("random"))
				continue
			}
			if w < 1 {
				w = 1
			}
			out = append(out, equity.ParseRange(fmt.Sprintf("top%.0f%%", w)))
		}
		return out
	}

	eq := equity.SimulateEquity(heroCards, boardCards, rangesAt(1.0), 12000)
	cache := map[int]float64{}
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
		r := equity.SimulateEquity(heroCards, boardCards, rangesAt(frac), 8000)
		v := r.WinRate + r.TieRate*0.5
		cache[key] = v
		return v
	}

	return state, Inputs{State: state, Equity: eq, EquityVsTop: equityVsTop}
}

// Queens on a board with two aces, shoved.
//
// From bin/logs/decisions.jsonl: pot 62,947, effective stack 32,766, one
// opponent, board Tc Ad 5d As, and the tool recommended all-in on "54% equity".
// Fifty-four per cent is against the strongest two thirds of a preflop hand
// ranking, which on this board is mostly hands that missed it entirely. Anyone
// who calls an all-in here holds an ace far more often than a preflop ranking
// suggests, and against an ace the queens are drawing to two outs.
func TestShoveWithQueensIntoPairedAces(t *testing.T) {
	_, in := liveShoveSpot(t, "Qh Qd", "Tc Ad 5d As", 62947, 32766, 1)
	advice := Calculate(in)

	t.Logf("action=%s amount=%.0f equity=%.3f", advice.PrimaryAction, advice.RecommendedAmount, advice.Equity)
	for _, a := range advice.Actions {
		t.Logf("   %-7s amount=%-10.0f ev=%-12.0f foldEq=%.3f %s", a.Action, a.Amount, a.EV, a.FoldEquity, a.SizingLabel)
	}

	if advice.PrimaryAction == table.ActionAllIn {
		t.Errorf("queens shoved a board with two aces: %s %.0f", advice.PrimaryAction, advice.RecommendedAmount)
	}
}

// The other side of the same rule, and the reason it is a narrowing and not a
// cap on sizing.
//
// From the same log: trip deuces on 2s 2h 4c, five-way, shoving 387,040 into
// 365,480. That shove is correct and has to survive. Measured, this hand's
// equity does not care how narrow the range is -- 0.935 against everything,
// 0.961 against the strongest tenth -- because it beats strong hands too. A
// blanket ceiling on bet size would have taken this away along with the queens.
func TestTripsStillShoveIntoAWideField(t *testing.T) {
	_, in := liveShoveSpot(t, "3h 2c", "2s 2h 4c", 365480, 387040, 4)
	advice := Calculate(in)

	t.Logf("action=%s amount=%.0f equity=%.3f", advice.PrimaryAction, advice.RecommendedAmount, advice.Equity)
	for _, a := range advice.Actions {
		t.Logf("   %-7s amount=%-10.0f ev=%-12.0f foldEq=%.3f %s", a.Action, a.Amount, a.EV, a.FoldEquity, a.SizingLabel)
	}

	switch advice.PrimaryAction {
	case table.ActionBet, table.ActionRaise, table.ActionAllIn:
	default:
		t.Errorf("trip deuces stopped betting a board they crush: %s", advice.PrimaryAction)
	}
}

// A committing bet is discounted; a small bet in a deep pot is not. Without
// this the narrowing would just be a tax on betting, and the model would check
// hands it should be value-betting.
func TestSmallBetInADeepPotIsNotDiscounted(t *testing.T) {
	// Top pair, top kicker, one opponent, and stacks far deeper than the pot,
	// so no size on offer risks much of anyone's stack.
	_, in := liveShoveSpot(t, "Ah Kh", "Kc 7d 2s", 600, 60000, 1)
	advice := Calculate(in)

	t.Logf("action=%s amount=%.0f equity=%.3f", advice.PrimaryAction, advice.RecommendedAmount, advice.Equity)
	for _, a := range advice.Actions {
		t.Logf("   %-7s amount=%-10.0f ev=%-12.2f foldEq=%.3f %s", a.Action, a.Amount, a.EV, a.FoldEquity, a.SizingLabel)
	}

	switch advice.PrimaryAction {
	case table.ActionBet, table.ActionRaise:
	default:
		t.Errorf("top pair top kicker checked a deep pot instead of value betting: %s", advice.PrimaryAction)
	}
	if advice.PrimaryAction == table.ActionAllIn {
		t.Errorf("top pair shoved 100 times the pot")
	}
}
