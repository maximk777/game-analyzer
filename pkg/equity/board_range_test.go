package equity

import (
	"testing"

	"poker-game-analyzer/pkg/table"
)

func hole(t *testing.T, s string) [2]table.Card {
	t.Helper()
	c := cards(t, s)
	if len(c) != 2 {
		t.Fatalf("want two cards, got %d", len(c))
	}
	return [2]table.Card{c[0], c[1]}
}

// The board decides what a range is, and this is the spot that made it worth
// proving. Tc Ad 5d As, hero holding queens: the strongest part of an
// opponent's range is anything with an ace, and by the preflop ranking that
// part is kings, queens and ace-king -- which on this board is a pair of tens
// with an ace kicker. The tool shoved 32,766 into 62,947 on exactly this.
func TestTopOfRangeIsRankedByTheBoardNotByTheRanking(t *testing.T) {
	hero := hole(t, "Qh Qd")
	board := cards(t, "Tc Ad 5d As")
	ranking := RankOnBoard(hero, board, ParseRange("random"))
	if ranking.Preflop {
		t.Fatal("a four-card board was ranked as though it were preflop")
	}
	top := ranking.Top(0.05)

	withAce, total := 0, len(top.Combos)
	for _, c := range top.Combos {
		if c[0].Rank == table.RankAce || c[1].Rank == table.RankAce {
			withAce++
		}
	}
	if total == 0 {
		t.Fatal("the top of the range came out empty")
	}
	// Trip aces, and the boats made by pairing the board, are what beats hero
	// here. Nearly all of it holds an ace.
	if share := float64(withAce) / float64(total); share < 0.6 {
		t.Errorf("only %.0f%% of the strongest 5%% holds an ace on a two-ace board", share*100)
	}

	// And the point of it: hero's equity against that slice has to be far below
	// hero's equity against the range as a whole. It was not, and that is why
	// the shove looked profitable.
	wide := SimulateEquity(hero, board, []Range{ParseRange("random")}, 8000)
	narrow := SimulateEquity(hero, board, []Range{top}, 8000)
	wideEq := wide.WinRate + wide.TieRate*0.5
	narrowEq := narrow.WinRate + narrow.TieRate*0.5
	if narrowEq > wideEq-0.4 {
		t.Errorf("equity against everything %.2f, against the strongest 5%% %.2f -- the slice is not narrowing anything",
			wideEq, narrowEq)
	}
	t.Logf("QQ on Tc Ad 5d As: vs all %.3f, vs strongest 5%% on the board %.3f", wideEq, narrowEq)
}

// Trip deuces are the counter-example that has to keep working. Their strength
// does not come from the opponent's range being wide, so narrowing it must not
// take much away -- this is the hand whose shove is correct, and a blanket cap
// on sizing would have cost it as much as it cost the queens.
func TestTripsHoldUpAgainstTheTopOfTheRange(t *testing.T) {
	hero := hole(t, "3h 2c")
	board := cards(t, "2s 2h 4c")
	ranking := RankOnBoard(hero, board, ParseRange("random"))
	top := ranking.Top(0.10)

	wide := SimulateEquity(hero, board, []Range{ParseRange("random")}, 8000)
	narrow := SimulateEquity(hero, board, []Range{top}, 8000)
	wideEq := wide.WinRate + wide.TieRate*0.5
	narrowEq := narrow.WinRate + narrow.TieRate*0.5
	t.Logf("32 on 2s 2h 4c: vs all %.3f, vs strongest 10%% on the board %.3f", wideEq, narrowEq)
	if narrowEq < 0.55 {
		t.Errorf("trip deuces fell to %.2f against the strongest tenth; the shove should survive", narrowEq)
	}
}

// A draw is a hand that calls, and a ranking that puts every draw below every
// pair says otherwise.
func TestDrawsReachTheTopOfTheRange(t *testing.T) {
	hero := hole(t, "Ks Kc")
	board := cards(t, "Qh 7h 2d")
	ranking := RankOnBoard(hero, board, ParseRange("random"))
	top := ranking.Top(0.15)

	hearts := 0
	for _, c := range top.Combos {
		if c[0].Suit == table.Hearts && c[1].Suit == table.Hearts {
			hearts++
		}
	}
	if hearts == 0 {
		t.Error("no flush draw made the strongest 15% of the range with two cards to come")
	}
}

// Preflop there is no board to rank on, and the function must say so rather
// than inventing an order.
func TestPreflopFallsBackToTheRanking(t *testing.T) {
	hero := hole(t, "7h 2c")
	ranking := RankOnBoard(hero, nil, ParseRange("random"))
	if !ranking.Preflop {
		t.Fatal("an empty board was not reported as preflop")
	}
	top := ranking.Top(0.01)
	for _, c := range top.Combos {
		if c[0].Rank != c[1].Rank || c[0].Rank < table.RankQueen {
			t.Errorf("the strongest 1%% of all hands contains %v%v", c[0], c[1])
		}
	}
}

// A band is a slice of the ordering, and Band(0, x) is Top(x) by construction.
func TestBandIsTopFromZero(t *testing.T) {
	hero := [2]table.Card{{Rank: table.RankAce, Suit: table.Spades}, {Rank: table.RankKing, Suit: table.Hearts}}
	board, err := table.ParseCards("Qd 7c 2h")
	if err != nil {
		t.Fatal(err)
	}
	rk := RankOnBoard(hero, board, ParseRange("random"))

	top := rk.Top(0.3)
	band := rk.Band(0, 0.3)
	if len(top.Combos) != len(band.Combos) {
		t.Fatalf("Top(0.3) has %d combos, Band(0,0.3) has %d", len(top.Combos), len(band.Combos))
	}
	for i := range top.Combos {
		if top.Combos[i] != band.Combos[i] {
			t.Fatalf("combo %d differs", i)
		}
	}
}

// The point of the band: it takes the lid off. A range that called is weaker
// than the top slice of the same size, because the hands above it raised.
func TestBandBelowTheTopIsWeaker(t *testing.T) {
	hero := [2]table.Card{{Rank: table.RankAce, Suit: table.Spades}, {Rank: table.RankKing, Suit: table.Hearts}}
	board, err := table.ParseCards("Qd 7c 2h")
	if err != nil {
		t.Fatal(err)
	}
	rk := RankOnBoard(hero, board, ParseRange("random"))

	top := rk.Top(0.4)
	capped := rk.Band(0.1, 0.5)
	// Rounding each edge independently can differ by one combination, which is
	// nothing to the equity and everything to an equality test.
	if d := len(top.Combos) - len(capped.Combos); d < -1 || d > 1 {
		t.Fatalf("bands of the same width differ in size: %d and %d", len(top.Combos), len(capped.Combos))
	}
	// The ranking is strongest first, so the capped band must start further in.
	if top.Combos[0] == capped.Combos[0] {
		t.Fatal("the capped band starts at the same hand as the top slice")
	}
}

// Rounding must never produce an empty range: equity against nothing is not a
// number, and callers cannot check for it.
func TestBandNeverEmpty(t *testing.T) {
	hero := [2]table.Card{{Rank: table.RankAce, Suit: table.Spades}, {Rank: table.RankKing, Suit: table.Hearts}}
	board, err := table.ParseCards("Qd 7c 2h")
	if err != nil {
		t.Fatal(err)
	}
	rk := RankOnBoard(hero, board, ParseRange("random"))

	for _, c := range [][2]float64{{0, 0}, {0.5, 0.5}, {1, 1}, {0.999, 1}, {0.4, 0.2}} {
		if got := rk.Band(c[0], c[1]); len(got.Combos) == 0 {
			t.Fatalf("Band(%g, %g) is empty", c[0], c[1])
		}
	}
}
