package advice

import (
	"testing"

	"poker-game-analyzer/pkg/table"
)

// Hero has folded: there is no decision to advise. The guard matches a seat
// against HeroID and, finding hero's seat folded, withholds advice. Off the
// wire hero's identity is always known and the fold is stated exactly, so the
// state handed to the pipeline is unambiguous -- this is the guard on its own.
//
// It closes a live failure: on 2026-08-31 the tool went on advising check,
// check, check, fold through a hand hero had folded preflop, still holding the
// dead 9c3s.
func TestFoldedHeroIsNotAdvised(t *testing.T) {
	nine, _ := table.ParseCard("9c")
	three, _ := table.ParseCard("3s")

	folded := &table.HandState{
		TableID: "t", Street: table.StreetFlop, Pot: 4960,
		HeroID:    "ludoStarik",
		HeroCards: [2]table.Card{nine, three},
		Seats: []table.SeatState{
			{PlayerID: "ludoStarik", PlayerName: "ludoStarik", Stack: 67940, IsActive: true, IsFolded: true},
			{PlayerID: "Rafidamage", PlayerName: "Rafidamage", Stack: 1190000, IsActive: true},
		},
		IsHeroTurn: true,
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
