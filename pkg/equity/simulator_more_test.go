package equity

import (
	"math/rand"
	"testing"

	"poker-game-analyzer/pkg/table"
)

func heroHand(t *testing.T) [2]table.Card {
	t.Helper()
	c := cards(t, "As 7h")
	return [2]table.Card{c[0], c[1]}
}

func randomRanges(n int) []Range {
	out := make([]Range, n)
	for i := range out {
		out[i] = ParseRange("random")
	}
	return out
}

// A live read that invents an eleventh player used to take the server down
// with an index panic, which is a poor way to learn that the table was misread.
func TestManyOpponentsDoNotPanic(t *testing.T) {
	res := SimulateEquityRNG(heroHand(t), nil, randomRanges(12), 200, rand.New(rand.NewSource(1)))
	if res.SamplesCount == 0 {
		t.Fatalf("twelve opponents still fit the deck: %+v", res)
	}
}

// More players than the deck can seat is not a hard hand, it is a misread, and
// there is no number worth returning for it.
func TestMoreOpponentsThanTheDeckAllowsReturnsNothing(t *testing.T) {
	res := SimulateEquityRNG(heroHand(t), nil, randomRanges(30), 200, rand.New(rand.NewSource(1)))
	if res.SamplesCount != 0 || res.WinRate != 0 {
		t.Fatalf("want an empty result, got %+v", res)
	}
}
