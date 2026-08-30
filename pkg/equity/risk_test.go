package equity

import (
	"testing"

	"poker-game-analyzer/pkg/table"
)

func cards(t *testing.T, s string) []table.Card {
	t.Helper()
	c, err := table.ParseCards(s)
	if err != nil {
		t.Fatalf("parsing %q: %v", s, err)
	}
	return c
}

// The hand that prompted this: kings on a board of two nines, a seven, a five
// and a queen on the river. Two pair, 88% against everything -- and dead against
// the queens the opponent actually had. Equity alone never said so.
func TestRiskNamesWhatBeatsHero(t *testing.T) {
	hero := cards(t, "Kh Kd")
	board := cards(t, "9s 9c 7d 5h Qs")

	got := Risk([2]table.Card{hero[0], hero[1]}, board, ParseRange("random"))

	if got.HeroHand != "Two Pair" {
		t.Errorf("hero hand read as %q, want Two Pair", got.HeroHand)
	}
	if got.Combos == 0 {
		t.Fatal("no combinations counted")
	}
	if got.Behind <= 0 {
		t.Fatalf("nothing beats two pair on a paired board with a queen: %+v", got)
	}

	// A full house has to be named: it is the danger, and it is the one the
	// opponent actually held.
	var sawFullHouse bool
	for _, c := range got.BeatenBy {
		t.Logf("  %-16s %5.2f%% (%d combos)", c.Category, c.Share*100, c.Combos)
		if c.Category == "Full House" {
			sawFullHouse = true
		}
	}
	if !sawFullHouse {
		t.Errorf("a full house is possible here and was not named: %+v", got.BeatenBy)
	}
	t.Logf("behind %.2f%% of %d combos, hero holds %s", got.Behind*100, got.Combos, got.HeroHand)
}

// The count is exact, so the shares have to add up to what is behind.
func TestRiskSharesAddUp(t *testing.T) {
	hero := cards(t, "Ah Kh")
	board := cards(t, "Kc 7d 2s")

	got := Risk([2]table.Card{hero[0], hero[1]}, board, ParseRange("random"))
	sum := 0.0
	for _, c := range got.BeatenBy {
		sum += c.Share
	}
	if diff := sum - got.Behind; diff > 1e-9 || diff < -1e-9 {
		t.Errorf("categories sum to %.6f but %.6f is behind", sum, got.Behind)
	}
}

// Fewer than three board cards is not a question this can answer.
func TestRiskIsSilentPreflop(t *testing.T) {
	hero := cards(t, "Ah Kh")
	got := Risk([2]table.Card{hero[0], hero[1]}, nil, ParseRange("random"))
	if got.Combos != 0 || len(got.BeatenBy) != 0 {
		t.Errorf("preflop risk should be empty, got %+v", got)
	}
}
