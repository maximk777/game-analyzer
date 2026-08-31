package sim

import (
	"math/rand"
	"testing"

	"poker-game-analyzer/pkg/table"
)

func botTable(t *testing.T, seed int64, styles []Style, stacks []Chips) *Table {
	t.Helper()
	rng := rand.New(rand.NewSource(seed))
	players := make([]*Player, 0, len(styles))
	for i, st := range styles {
		players = append(players, &Player{
			ID:    string(rune('A' + i)),
			Name:  st.Name,
			Agent: NewBot(st, rand.New(rand.NewSource(seed+int64(i)*7919))),
			Stack: stacks[i],
		})
	}
	return NewTable("t", DefaultConfig(), players, rng)
}

// Chips do not appear or disappear. Everything the harness reports is a sum of
// net results, so a leak here would be indistinguishable from a strategy edge.
func TestChipsAreConserved(t *testing.T) {
	styles := []Style{StyleNit, StyleTAG, StyleLAG, StyleStation, StyleManiac, StyleWhale}
	stacks := []Chips{10000, 5000, 25000, 1500, 8000, 40000}
	tb := botTable(t, 42, styles, stacks)

	var total Chips
	for _, p := range tb.Players() {
		total += p.Stack
	}

	for i := 0; i < 2000; i++ {
		res := tb.PlayHand()
		var now, net Chips
		for _, p := range tb.Players() {
			now += p.Stack
		}
		for _, v := range res.Net {
			net += v
		}
		if now != total {
			t.Fatalf("hand %d: table holds %d chips, started with %d", i, now, total)
		}
		if net != 0 {
			t.Fatalf("hand %d: net results sum to %d, not zero", i, net)
		}
		// Reload anybody who busted, the way a real table refills.
		for _, p := range tb.Players() {
			if p.Stack < 2000 {
				total += 10000 - p.Stack
				p.Stack = 10000
			}
		}
	}
}

// Nobody may ever be owed money they did not put in, and no stack may go
// negative.
func TestStacksStayNonNegative(t *testing.T) {
	styles := []Style{StyleManiac, StyleManiac, StyleLAG, StyleStation, StyleManiac, StyleLAG}
	stacks := []Chips{300, 900, 12000, 150, 60000, 4000}
	tb := botTable(t, 7, styles, stacks)
	for i := 0; i < 1000; i++ {
		tb.PlayHand()
		for _, p := range tb.Players() {
			if p.Stack < 0 {
				t.Fatalf("hand %d: %s has %d chips", i, p.ID, p.Stack)
			}
			if p.Stack < 500 {
				p.Stack = 20000
			}
		}
	}
}

// A player who never voluntarily puts money in never voluntarily puts money
// in, and cannot lose more than the blinds they post.
//
// The obvious form of this test -- "loses exactly the blinds" -- is wrong, and
// finding out why was worth the test: checking is free, so a fold-bot that is
// given a free look at five cards sometimes wins the pot. It measured -13.3
// bb/100 rather than the -25 the blinds alone would cost. That is the real
// floor a strategy has to beat, and it is not the one arithmetic suggests.
func TestFoldBotNeverInvests(t *testing.T) {
	cfg := DefaultConfig()
	players := []*Player{{ID: "hero", Name: "hero", Agent: FoldBot{}, Stack: 1000000}}
	for i := 1; i < 6; i++ {
		players = append(players, &Player{
			ID: string(rune('a' + i)), Name: "bot", Stack: 1000000,
			Agent: NewBot(StyleTAG, rand.New(rand.NewSource(int64(i)))),
		})
	}
	tb := NewTable("t", cfg, players, rand.New(rand.NewSource(3)))
	tb.SetObserver(newObserverFunc(func(d DecisionRecord) {
		if d.PlayerID == "hero" && d.Invested != 0 {
			t.Fatalf("fold-bot invested %d chips", d.Invested)
		}
	}, nil))

	start := players[0].Stack
	const hands = 600
	for i := 0; i < hands; i++ {
		tb.PlayHand()
	}
	lost := start - players[0].Stack
	blinds := Chips(hands/6) * (cfg.SmallBlind + cfg.BigBlind)
	if lost <= 0 {
		t.Fatalf("fold-bot came out %d ahead over %d hands", -lost, hands)
	}
	if lost > blinds {
		t.Fatalf("fold-bot lost %d, more than the %d it posted in blinds", lost, blinds)
	}
}

// A short all-in makes a side pot, and the short stack cannot win chips the
// deep players put in after they were already all in.
func TestSidePotCapsTheShortStack(t *testing.T) {
	cfg := DefaultConfig()
	// Three seats: the short stack shoves, the other two go to war behind.
	players := []*Player{
		{ID: "short", Name: "short", Agent: CallBot{}, Stack: 400},
		{ID: "deepA", Name: "deepA", Agent: CallBot{}, Stack: 50000},
		{ID: "deepB", Name: "deepB", Agent: shoveBot{}, Stack: 50000},
	}
	tb := NewTable("t", cfg, players, rand.New(rand.NewSource(11)))

	for i := 0; i < 200; i++ {
		res := tb.PlayHand()
		if res.Net["short"] > 800 {
			t.Fatalf("hand %d: short stack won %d, more than the 400 anyone could match twice over",
				i, res.Net["short"])
		}
		for _, p := range tb.Players() {
			switch p.ID {
			case "short":
				p.Stack = 400
			default:
				p.Stack = 50000
			}
		}
	}
}

// shoveBot moves all in at every opportunity, which is the fastest way to
// exercise the side-pot code.
type shoveBot struct{}

func (shoveBot) Name() string { return "shove" }
func (shoveBot) Act(s Spot) Move {
	if s.MaxRaise > 0 {
		return Move{Action: table.ActionAllIn}
	}
	return Move{Action: table.ActionCall}
}

// The state handed to an Agent has to look like a frame off the screen: hero's
// own cards and nobody else's, a pot that includes what is already in, and
// buttons that say what is on offer.
func TestSpotLooksLikeAScreenReading(t *testing.T) {
	styles := []Style{StyleTAG, StyleTAG, StyleLAG, StyleNit, StyleStation, StyleWhale}
	stacks := []Chips{10000, 10000, 10000, 10000, 10000, 10000}
	tb := botTable(t, 99, styles, stacks)

	var seen int
	tb.SetObserver(newObserverFunc(func(d DecisionRecord) {
		seen++
		st := d.Spot.State
		if !st.HeroCards[0].Known() || !st.HeroCards[1].Known() {
			t.Fatalf("hero cards missing from the view")
		}
		if !st.HeroCanAct() {
			t.Fatalf("a spot that is not hero's turn was handed to an agent")
		}
		for _, seat := range st.Seats {
			if seat.PlayerID != st.HeroID && len(seat.Cards) > 0 {
				t.Fatalf("an opponent's cards leaked into the view")
			}
		}
		if st.Pot <= 0 {
			t.Fatalf("pot is %v", st.Pot)
		}
		if d.Spot.ToCall > 0 && !st.HeroFacesABet() {
			t.Fatalf("owed %.2f with no call button", d.Spot.ToCall)
		}
		if mayCheck, known := st.HeroMayCheck(); known && mayCheck != (d.Spot.ToCall <= 0) {
			t.Fatalf("check button says %v with %.2f owed", mayCheck, d.Spot.ToCall)
		}
	}, nil))

	for i := 0; i < 200; i++ {
		tb.PlayHand()
		for _, p := range tb.Players() {
			p.Stack = 10000
		}
	}
	if seen == 0 {
		t.Fatal("no decisions were observed")
	}
}

type observerFunc struct {
	dec func(DecisionRecord)
	end func(HandResult)
}

func newObserverFunc(d func(DecisionRecord), e func(HandResult)) observerFunc {
	return observerFunc{dec: d, end: e}
}
func (o observerFunc) OnDecision(d DecisionRecord) {
	if o.dec != nil {
		o.dec(d)
	}
}
func (o observerFunc) OnHandEnd(r HandResult) {
	if o.end != nil {
		o.end(r)
	}
}
