package advice

import (
	"testing"

	"poker-game-analyzer/pkg/table"
)

func heroState(pot float64) *table.HandState {
	ah, _ := table.ParseCard("Ah")
	kh, _ := table.ParseCard("Kh")
	f1, _ := table.ParseCard("2c")
	f2, _ := table.ParseCard("7d")
	f3, _ := table.ParseCard("Js")
	return &table.HandState{
		TableID:        "t",
		Street:         table.StreetFlop,
		Pot:            pot,
		CurrentBet:     2000,
		SmallBlind:     1000,
		BigBlind:       2000,
		CommunityCards: []table.Card{f1, f2, f3},
		HeroID:         "hero",
		HeroCards:      [2]table.Card{ah, kh},
		HeroButtons:    []string{"fold", "call", "raise"},
		IsHeroTurn:     true,
		Seats: []table.SeatState{
			{SeatNumber: 0, PlayerID: "hero", PlayerName: "hero", Stack: 100000, IsActive: true, Position: "BTN"},
			{SeatNumber: 3, PlayerID: "opp", PlayerName: "opp", Stack: 100000, CurrentBet: 2000, IsActive: true, Position: "BB"},
		},
	}
}

// The screen is read about twelve times a second. On a table where nothing is
// happening, every frame used to draw a different Monte Carlo sample, so the
// equity moved, the EV moved, and near a threshold the recommendation flipped
// between two answers several times a second with nothing on screen changing.
func TestSameStateGivesTheSameAdvice(t *testing.T) {
	first := Evaluate(heroState(10000), Reads{}, Options{})
	for i := range 4 {
		again := Evaluate(heroState(10000), Reads{}, Options{})
		if again.NoAdvice != first.NoAdvice {
			t.Fatalf("run %d: no-advice changed: %q then %q", i, first.NoAdvice, again.NoAdvice)
		}
		if first.Recommendation == nil || again.Recommendation == nil {
			t.Fatalf("run %d: no recommendation to compare", i)
		}
		if again.Recommendation.PrimaryAction != first.Recommendation.PrimaryAction {
			t.Fatalf("run %d: action moved on an unchanged table: %v then %v",
				i, first.Recommendation.PrimaryAction, again.Recommendation.PrimaryAction)
		}
		if again.Recommendation.Equity != first.Recommendation.Equity {
			t.Fatalf("run %d: equity moved on an unchanged table: %v then %v",
				i, first.Recommendation.Equity, again.Recommendation.Equity)
		}
	}
}

// A different table must not inherit the same sample: seeding from the state
// has to vary with the state, or every hand shares one draw.
func TestADifferentStateGetsADifferentSample(t *testing.T) {
	if seedFor(heroState(10000)) == seedFor(heroState(40000)) {
		t.Fatal("two different pots produced the same seed")
	}
}
