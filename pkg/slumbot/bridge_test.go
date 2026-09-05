package slumbot

import (
	"testing"

	"poker-game-analyzer/pkg/table"
)

// The advisor sizes a raise as chips added now; Slumbot is told the actor's
// total for the street. In an unraised pot the two numbers are equal, which is
// why the mistake survives any test that only plays limped pots -- so the cases
// that matter here are the ones where hero already has money out.
func TestRaiseTo_ConvertsIncrementToStreetTotal(t *testing.T) {
	cases := []struct {
		name string
		// action is the string as it stands when hero is asked to act.
		action string
		hero   Seat
		add    float64
		want   int
	}{{
		// Flop, nothing out there: adding 300 is a total of 300.
		name:   "bet into an unraised pot is unchanged",
		action: "b200c/", hero: SeatBB, add: 300, want: 300,
	}, {
		// Flop, hero already called 200 of a 200 bet... no: hero faces 200 and
		// raises by 600 more. Hero has 0 out, villain has 200: the total is 600
		// and it must cover the 200.
		name:   "raise from nothing out is the increment itself",
		action: "b200c/kb200", hero: SeatBB, add: 600, want: 600,
	}, {
		// Preflop, hero is the big blind with 100 already posted and facing a
		// raise to 200. Adding 400 is a total of 500, not 400.
		name:   "the posted blind counts toward the street total",
		action: "b200", hero: SeatBB, add: 400, want: 500,
	}, {
		// Preflop, hero is the button with 50 posted, opening for 150 more.
		name:   "the small blind counts too",
		action: "", hero: SeatSB, add: 150, want: 200,
	}}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			st, err := ParseAction(c.action)
			if err != nil {
				t.Fatalf("ParseAction(%q): %v", c.action, err)
			}
			if got := raiseTo(st, c.hero, c.add); got != c.want {
				t.Errorf("raiseTo(%q, %v, %v) = %d, want %d",
					c.action, c.hero, c.add, got, c.want)
			}
		})
	}
}

// 20000 chips is the whole stack: Slumbot accepts b20000 and answers b20001
// with "Unexpected action". A size past it must be pulled back rather than sent.
func TestRaiseTo_ClampsToTheStack(t *testing.T) {
	st, err := ParseAction("b200")
	if err != nil {
		t.Fatal(err)
	}
	if got := raiseTo(st, SeatBB, 1e9); got != Stack {
		t.Errorf("a huge raise came out as %d, want the stack %d", got, Stack)
	}
}

// A size too small to be legal is the smallest legal raise, not a call: naming a
// size is a decision to put money in. This is the rule pkg/sim/engine.go applies
// to the same request, and the two have to agree or the harness and this bridge
// are measuring different strategies.
func TestRaiseTo_LiftsAnIllegalSize(t *testing.T) {
	st, err := ParseAction("b200")
	if err != nil {
		t.Fatal(err)
	}
	// Hero has 100 posted and faces 200. A raise of 10 more is not legal; the
	// smallest legal raise is to 300.
	if got, want := raiseTo(st, SeatBB, 10), 300; got != want {
		t.Errorf("tiny raise came out as %d, want the minimum %d", got, want)
	}
}

func TestMinRaiseTo(t *testing.T) {
	cases := []struct {
		action string
		want   int
	}{
		// Preflop unopened: the minimum is two big blinds.
		{"", 200},
		// Facing an open to 200, the increment was 100, so 300.
		{"b200", 300},
		// Facing a three-bet to 600 over 200, the increment was 400, so 1000.
		{"b200b600", 1000},
		// A fresh street with nothing out: the minimum bet is one big blind.
		{"b200c/", 100},
		// Facing a flop bet of 400, the increment was 400, so 800.
		{"b200c/kb400", 800},
	}
	for _, c := range cases {
		st, err := ParseAction(c.action)
		if err != nil {
			t.Fatalf("ParseAction(%q): %v", c.action, err)
		}
		if got := minRaiseTo(st); got != c.want {
			t.Errorf("minRaiseTo(%q) = %d, want %d", c.action, got, c.want)
		}
	}
}

// The state handed to the advisor has to describe the spot, because everything
// the advisor concludes -- and everything this run measures -- is keyed off it.
func TestHandState_DescribesTheSpot(t *testing.T) {
	r := &Response{
		ClientPos: 0, // hero is the big blind
		HoleCards: []string{"Ah", "Kd"},
		Board:     []string{"Jc", "7h", "2s"},
		Action:    "b200c/kb400",
	}
	st, err := ParseAction(r.Action)
	if err != nil {
		t.Fatal(err)
	}
	h, err := HandState(r, st)
	if err != nil {
		t.Fatal(err)
	}

	if h.Street != table.StreetFlop {
		t.Errorf("street = %v, want flop", h.Street)
	}
	if h.Pot != 800 {
		t.Errorf("pot = %v, want 800 (400 preflop plus the 400 bet)", h.Pot)
	}
	if h.CurrentBet != 400 {
		t.Errorf("current bet = %v, want 400", h.CurrentBet)
	}
	if !h.IsHeroTurn {
		t.Error("it is hero's turn and the state says otherwise")
	}
	if !h.HeroFacesABet() {
		t.Error("hero faces a bet of 400 and the buttons do not say so")
	}
	if may, known := h.HeroMayCheck(); may || !known {
		t.Errorf("checking is not on offer: may=%v known=%v", may, known)
	}

	hero, vill := HeroSeatState(h), VillainSeatState(h)
	if hero.Position != table.PosBB {
		t.Errorf("hero position = %v, want BB", hero.Position)
	}
	// Heads-up the small blind is on the button, and the advisor's positional
	// logic keys off BTN for "acts last after the flop".
	if vill.Position != table.PosBTN {
		t.Errorf("villain position = %v, want BTN", vill.Position)
	}
	if hero.Stack != Stack-200 {
		t.Errorf("hero stack = %v, want %v", hero.Stack, Stack-200)
	}
	if vill.Stack != Stack-600 {
		t.Errorf("villain stack = %v, want %v", vill.Stack, Stack-600)
	}

	// The action history is what the range model reads; without it the model
	// falls back to counting chips and this whole measurement is of something
	// else.
	if len(h.ActionHistory) != 4 {
		t.Fatalf("action history has %d entries, want 4: %+v", len(h.ActionHistory), h.ActionHistory)
	}
	want := []struct {
		id     string
		street table.Street
		act    table.ActionType
	}{
		{VillainID, table.StreetPreflop, table.ActionRaise},
		{HeroID, table.StreetPreflop, table.ActionCall},
		{HeroID, table.StreetFlop, table.ActionCheck},
		{VillainID, table.StreetFlop, table.ActionBet},
	}
	for i, w := range want {
		got := h.ActionHistory[i]
		if got.PlayerID != w.id || got.Street != w.street || got.Action != w.act {
			t.Errorf("history[%d] = %+v, want %s %s %s", i, got, w.id, w.street, w.act)
		}
	}
}

// Hero on the button after the flop: the seat that posted the small blind is
// the one that acts last, and mixing this up hands every act to the wrong player.
func TestHandState_ButtonHero(t *testing.T) {
	r := &Response{
		ClientPos: 1,
		HoleCards: []string{"Qs", "Qd"},
		Board:     []string{"9c", "5h", "2s"},
		Action:    "b200c/k",
	}
	st, err := ParseAction(r.Action)
	if err != nil {
		t.Fatal(err)
	}
	h, err := HandState(r, st)
	if err != nil {
		t.Fatal(err)
	}
	if HeroSeatState(h).Position != table.PosBTN {
		t.Errorf("hero position = %v, want BTN", HeroSeatState(h).Position)
	}
	if !h.IsHeroTurn {
		t.Error("the big blind checked to hero; it is hero's turn")
	}
	if may, known := h.HeroMayCheck(); !may || !known {
		t.Errorf("checking is on offer: may=%v known=%v", may, known)
	}
	// The opening raise preflop was hero's, on the button.
	if got := h.ActionHistory[0]; got.PlayerID != HeroID || got.Action != table.ActionRaise {
		t.Errorf("history[0] = %+v, want hero raising", got)
	}
}
