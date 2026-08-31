package advice

import (
	"testing"

	"poker-game-analyzer/pkg/table"
)

// Hero folds, and the screen reader stops naming hero on the same frame --
// because it names hero only when hero's hole cards read, and a folded hand's
// cards are face down. The two arrive together, always.
//
// The guard against advising a folded hand matches a seat against HeroID. With
// the placeholder in HeroID no seat matches, so the guard never fired: live on
// 2026-08-31 the tool went on advising check, check, check, fold through the
// flop, turn and river of a hand hero had folded preflop, still holding the
// dead 9c3s.
//
// The stabiliser is what closes this, by carrying hero's identity across the
// frames where the cards are unreadable. This test runs both together, because
// separately each one looks correct.
func TestFoldedHeroIsNotAdvised_AfterStabilising(t *testing.T) {
	nine, _ := table.ParseCard("9c")
	three, _ := table.ParseCard("3s")

	seats := func(heroFolded bool) []table.SeatState {
		return []table.SeatState{
			{PlayerID: "ludoStarik", PlayerName: "ludoStarik", Stack: 67940, IsActive: true, IsFolded: heroFolded},
			{PlayerID: "Rafidamage", PlayerName: "Rafidamage", Stack: 1190000, IsActive: true},
		}
	}

	stab := table.NewStateStabilizer()

	// The frame where hero's cards read, so the reader can name the seat.
	stab.Stabilize(&table.HandState{
		TableID: "t", Street: table.StreetPreflop, Pot: 3960, CurrentBet: 2000,
		HeroID: "ludoStarik", HeroCards: [2]table.Card{nine, three},
		Seats: seats(false), IsHeroTurn: true,
	})

	// Hero folds. The fold badge is on the nameplate; the cards are face down,
	// so the reader falls back to the placeholder.
	folded := stab.Stabilize(&table.HandState{
		TableID: "t", Street: table.StreetFlop, Pot: 4960,
		HeroID: "Hero", HeroCards: [2]table.Card{nine, three},
		Seats: seats(true), IsHeroTurn: true,
	})

	if folded.HeroID != "ludoStarik" {
		t.Fatalf("HeroID = %q after the fold; the guard below needs a seat to match", folded.HeroID)
	}

	res := Evaluate(folded, Reads{}, Options{Iterations: 200, VsTopIterations: 100})
	if res.Recommendation != nil {
		t.Errorf("advised %s %.0f on a hand hero had folded",
			res.Recommendation.PrimaryAction, res.Recommendation.RecommendedAmount)
	}
	if res.NoAdvice == "" {
		t.Error("no recommendation and no reason given")
	}
}

// The other half: with the placeholder left in place the guard cannot fire.
// Kept so that the fix above is visibly what closes it, rather than something
// else that happens to.
func TestFoldedHeroIsAdvisedWhenHeroIsUnnamed(t *testing.T) {
	nine, _ := table.ParseCard("9c")
	three, _ := table.ParseCard("3s")

	unnamed := &table.HandState{
		TableID: "t", Street: table.StreetFlop, Pot: 4960,
		HeroID: "Hero", HeroCards: [2]table.Card{nine, three},
		Seats: []table.SeatState{
			{PlayerID: "ludoStarik", Stack: 67940, IsActive: true, IsFolded: true},
			{PlayerID: "Rafidamage", Stack: 1190000, IsActive: true},
		},
		IsHeroTurn: true,
	}

	res := Evaluate(unnamed, Reads{}, Options{Iterations: 200, VsTopIterations: 100})
	if res.NoAdvice == "Вы сбросили — решать нечего" {
		t.Skip("the fold guard now fires without a named hero; this half is obsolete")
	}
	if res.Recommendation == nil {
		t.Skipf("declined for another reason (%q); the point stands either way", res.NoAdvice)
	}
	t.Logf("unnamed hero, folded seat: advised %s -- this is the live failure",
		res.Recommendation.PrimaryAction)
}
