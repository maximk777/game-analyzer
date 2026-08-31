package sim

import (
	"math"
	"math/rand"
	"testing"

	"poker-game-analyzer/pkg/table"
)

func card(t *testing.T, s string) table.Card {
	t.Helper()
	c, err := table.ParseCard(s)
	if err != nil {
		t.Fatalf("card %q: %v", s, err)
	}
	return c
}

func cards(t *testing.T, ss ...string) []table.Card {
	t.Helper()
	out := make([]table.Card, 0, len(ss))
	for _, s := range ss {
		out = append(out, card(t, s))
	}
	return out
}

// allInHand builds a hand parked at its all-in point with an explicit set of
// cards left to come, so the adjustment can be checked against arithmetic
// anybody can do by hand rather than against a simulation of itself.
func allInHand(t *testing.T, board []table.Card, rem []table.Card, holes [][2]table.Card, totals []Chips) *hand {
	t.Helper()
	h := &hand{id: "T-1", deck: rem, allInMarked: true, allInBoard: board, allInDeck: 0}
	for i := range holes {
		p := &Player{ID: string(rune('a' + i))}
		h.seats = append(h.seats, &seatState{p: p, hole: holes[i], total: totals[i], allIn: true})
	}
	return h
}

// Two thirds and one third, worked out on paper: aces are ahead, the king is
// the only card that changes it, and there are three cards it could be.
func TestAllInAdjustedSplitsByEquity(t *testing.T) {
	h := allInHand(t,
		cards(t, "2c", "7h", "9s", "Jd"),
		cards(t, "Kc", "3h", "4d"),
		[][2]table.Card{
			{card(t, "As"), card(t, "Ad")},
			{card(t, "Ks"), card(t, "Kd")},
		},
		[]Chips{100, 100},
	)

	adj := h.adjustedNets()
	if adj == nil {
		t.Fatal("no adjustment for a hand that went all-in with a card to come")
	}
	if got, want := adj["a"], 200.0*2/3-100; math.Abs(got-want) > 1e-6 {
		t.Errorf("aces net %.4f, want %.4f", got, want)
	}
	if got, want := adj["b"], 200.0/3-100; math.Abs(got-want) > 1e-6 {
		t.Errorf("kings net %.4f, want %.4f", got, want)
	}
}

// Whatever else it does, the adjustment may not create or destroy chips.
func TestAllInAdjustedIsZeroSum(t *testing.T) {
	h := allInHand(t,
		cards(t, "2c", "7h", "9s"),
		cards(t, "Kc", "3h", "4d", "Qs", "8d", "Th"),
		[][2]table.Card{
			{card(t, "As"), card(t, "Ad")},
			{card(t, "Ks"), card(t, "Kd")},
			{card(t, "6c"), card(t, "6h")},
		},
		[]Chips{100, 100, 40},
	)

	adj := h.adjustedNets()
	if adj == nil {
		t.Fatal("no adjustment")
	}
	sum := 0.0
	for _, v := range adj {
		sum += v
	}
	if math.Abs(sum) > 1e-6 {
		t.Fatalf("nets sum to %.6f, want 0", sum)
	}
}

// A side pot only one player can win is theirs whatever comes, and the short
// stack can never be paid more than they could win.
func TestAllInAdjustedRespectsSidePots(t *testing.T) {
	// The short stack put in 40 and can win at most 40 from each of the other
	// two: a main pot of 120. The remaining 120 is a side pot between the deep
	// two, and the short stack is not eligible for a chip of it.
	h := allInHand(t,
		cards(t, "2c", "7h", "9s", "Jd"),
		cards(t, "3h"),
		[][2]table.Card{
			{card(t, "As"), card(t, "Ad")}, // wins everything
			{card(t, "Ks"), card(t, "Kd")},
			{card(t, "6c"), card(t, "6h")},
		},
		[]Chips{100, 100, 40},
	)

	adj := h.adjustedNets()
	if adj == nil {
		t.Fatal("no adjustment")
	}
	if got := adj["a"]; math.Abs(got-140) > 1e-6 {
		t.Errorf("winner net %.4f, want 140", got)
	}
	if got := adj["c"]; math.Abs(got+40) > 1e-6 {
		t.Errorf("short stack net %.4f, want -40", got)
	}
}

// Nothing to adjust when the last chip went in on the river: there is no card
// left to be lucky with, and the realised result is already the expectation.
func TestNoAdjustmentOnTheRiver(t *testing.T) {
	h := allInHand(t,
		cards(t, "2c", "7h", "9s", "Jd", "3h"),
		nil,
		[][2]table.Card{
			{card(t, "As"), card(t, "Ad")},
			{card(t, "Ks"), card(t, "Kd")},
		},
		[]Chips{100, 100},
	)
	if adj := h.adjustedNets(); adj != nil {
		t.Fatalf("adjusted a river all-in: %v", adj)
	}
}

func TestEnumerateCoversEveryCombination(t *testing.T) {
	deck := cards(t, "2c", "3d", "4h", "5s", "6c")
	seen := map[string]bool{}
	enumerate(deck, 2, func(pick []table.Card) {
		seen[pick[0].String()+pick[1].String()] = true
	})
	if len(seen) != 10 {
		t.Fatalf("enumerated %d combinations of 2 from 5, want 10", len(seen))
	}
}

// Playing a hand end to end: the adjustment appears exactly when the engine
// settled a pot that was all-in with cards to come, and the real stacks are
// untouched by it.
func TestEngineRecordsAdjustedNetsAndKeepsRealStacks(t *testing.T) {
	cfg := DefaultConfig()
	// A stack of one big blind: the blinds alone put both players in, so every
	// hand is all-in before the flop with five cards to come. That exercises the
	// sampled path, which is the one that cannot be enumerated.
	stack := cfg.BigBlind
	players := []*Player{
		{ID: "p0", Agent: CallBot{}, Stack: stack},
		{ID: "p1", Agent: CallBot{}, Stack: stack},
	}
	tb := NewTable("T", cfg, players, rand.New(rand.NewSource(7)))

	adjusted, played := 0, 0
	for i := 0; i < 50; i++ {
		for _, p := range tb.Players() {
			p.Stack = stack
		}
		res := tb.PlayHand()
		played++
		if res.AdjNet == nil {
			continue
		}
		adjusted++

		sum := 0.0
		for _, v := range res.AdjNet {
			sum += v
		}
		if math.Abs(sum) > 1e-6 {
			t.Fatalf("hand %s: adjusted nets sum to %.6f, want 0", res.HandID, sum)
		}
		// The realised result must still be the realised result: the engine
		// settled on the card that came, and only the reporting changed.
		realSum := Chips(0)
		for _, v := range res.Net {
			realSum += v
		}
		if realSum != 0 {
			t.Fatalf("hand %s: realised nets sum to %d, want 0", res.HandID, realSum)
		}
	}
	if adjusted == 0 {
		t.Fatalf("no hand out of %d went all-in with cards to come; the test proves nothing", played)
	}
}

// The same deck must produce the same adjustment every time, or two candidates
// stop being comparable for a reason that has nothing to do with either.
func TestAdjustmentIsDeterministic(t *testing.T) {
	run := func() []float64 {
		cfg := DefaultConfig()
		stack := cfg.BigBlind
		players := []*Player{
			{ID: "p0", Agent: CallBot{}, Stack: stack},
			{ID: "p1", Agent: CallBot{}, Stack: stack},
		}
		tb := NewTable("T", cfg, players, rand.New(rand.NewSource(11)))
		var out []float64
		for i := 0; i < 20; i++ {
			for _, p := range tb.Players() {
				p.Stack = stack
			}
			res := tb.PlayHand()
			out = append(out, res.AdjNet["p0"])
		}
		return out
	}
	a, b := run(), run()
	for i := range a {
		if a[i] != b[i] {
			t.Fatalf("hand %d: %.6f then %.6f", i, a[i], b[i])
		}
	}
}
