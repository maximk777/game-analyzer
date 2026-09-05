package slumbot

import (
	"testing"

	"poker-game-analyzer/pkg/table"
)

// Every string here came off the wire on 2026-09-05, together with the
// `winnings` Slumbot reported for it. The committed total is checked against
// that number because it is the only external truth available: a parser that
// reads the amounts as increments rather than street totals still parses all of
// these, and only the money says it is wrong.
func TestParseAction_RealHands(t *testing.T) {
	cases := []struct {
		name string
		s    string
		// heroSeat is the client_pos the hand was dealt at.
		heroSeat Seat
		// winnings is what Slumbot paid, in chips, from hero's point of view.
		winnings int
		wantHero int // hero's total commitment
		wantVill int
	}{{
		name: "called down and lost at showdown",
		// bot bet flop and river, we called both
		s: "b200c/kb200c/kk/kb400c", heroSeat: SeatBB,
		winnings: -800, wantHero: 800, wantVill: 800,
	}, {
		name: "called down and won at showdown",
		s:    "b200c/kb200c/kb400c/kk", heroSeat: SeatBB,
		winnings: 800, wantHero: 800, wantVill: 800,
	}, {
		name: "we three-bet and the button folded",
		s:    "b200b600f", heroSeat: SeatBB,
		winnings: 200, wantHero: 600, wantVill: 200,
	}, {
		name: "we folded the big blind",
		s:    "b200f", heroSeat: SeatBB,
		winnings: -100, wantHero: 100, wantVill: 200,
	}}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			st, err := ParseAction(c.s)
			if err != nil {
				t.Fatalf("ParseAction(%q): %v", c.s, err)
			}
			hero, vill := st.Committed[c.heroSeat], st.Committed[c.heroSeat.Other()]
			if hero != c.wantHero || vill != c.wantVill {
				t.Fatalf("committed hero=%d villain=%d, want %d and %d",
					hero, vill, c.wantHero, c.wantVill)
			}
			// The money has to close. Either hero folded and lost what was
			// committed, or hero won or lost the smaller of the two stacks in.
			if c.winnings < 0 && -c.winnings > hero {
				t.Fatalf("lost %d having only committed %d", -c.winnings, hero)
			}
			if c.winnings > 0 && c.winnings != vill {
				t.Fatalf("won %d but villain committed %d", c.winnings, vill)
			}
		})
	}
}

// Heads-up flips who speaks first between preflop and the streets after it. Get
// it backwards and every act is credited to the wrong player -- which parses
// perfectly and measures the opponent's range against our own actions.
func TestParseAction_WhoActedIsNotGuessed(t *testing.T) {
	st, err := ParseAction("b200c/kb200c")
	if err != nil {
		t.Fatalf("ParseAction: %v", err)
	}
	want := []struct {
		street int
		idx    int
		actor  Seat
		kind   table.ActionType
		to     int
	}{
		// Preflop the big blind is already out there, so the button opening
		// over it is a raise and not a bet. The advisor reads the two
		// differently, and this is the only street where the distinction is
		// decided by a blind rather than by a player.
		{0, 0, SeatSB, table.ActionRaise, 200},
		{0, 1, SeatBB, table.ActionCall, 200},
		// After the flop the big blind speaks first, and with nothing out
		// there the button's 200 is a bet.
		{1, 0, SeatBB, table.ActionCheck, 0},
		{1, 1, SeatSB, table.ActionBet, 200},
		{1, 2, SeatBB, table.ActionCall, 200},
	}
	for _, w := range want {
		got := st.Acts[w.street][w.idx]
		if got.Actor != w.actor || got.Kind != w.kind || got.To != w.to {
			t.Errorf("street %d act %d = %+v, want actor=%v kind=%v to=%d",
				w.street, w.idx, got, w.actor, w.kind, w.to)
		}
	}
}

// A trailing slash is how Slumbot says "the next card is out and it is your
// turn"; it is the most common state the bridge ever has to act on.
func TestParseAction_StreetJustOpened(t *testing.T) {
	st, err := ParseAction("b200c/")
	if err != nil {
		t.Fatalf("ParseAction: %v", err)
	}
	if st.Street != table.StreetFlop {
		t.Errorf("street = %v, want flop", st.Street)
	}
	if st.ToAct != SeatBB {
		t.Errorf("to act = %v, want the big blind", st.ToAct)
	}
	if st.Owed() != 0 {
		t.Errorf("owed = %d, want 0 -- checking is on offer", st.Owed())
	}
	if st.Pot() != 400 {
		t.Errorf("pot = %d, want 400", st.Pot())
	}
}

// The opening state of a hand dealt to the big blind: the button has raised and
// we owe the difference, not the whole of its bet.
func TestParseAction_FacingTheOpen(t *testing.T) {
	st, err := ParseAction("b200")
	if err != nil {
		t.Fatalf("ParseAction: %v", err)
	}
	if st.ToAct != SeatBB {
		t.Fatalf("to act = %v, want big blind", st.ToAct)
	}
	if st.Owed() != 100 {
		t.Errorf("owed = %d, want 100: the blind is already out there", st.Owed())
	}
}

// An empty string is a hand dealt to the button, where nobody has acted.
func TestParseAction_ButtonToOpen(t *testing.T) {
	st, err := ParseAction("")
	if err != nil {
		t.Fatalf("ParseAction: %v", err)
	}
	if st.ToAct != SeatSB {
		t.Errorf("to act = %v, want small blind", st.ToAct)
	}
	if st.Owed() != 50 {
		t.Errorf("owed = %d, want 50", st.Owed())
	}
	if st.Pot() != 150 {
		t.Errorf("pot = %d, want 150", st.Pot())
	}
}

func TestParseAction_Rejects(t *testing.T) {
	for _, s := range []string{"b200x", "b", "zz", "b200c/kb200c/kk/kk/kk"} {
		if _, err := ParseAction(s); err == nil {
			t.Errorf("ParseAction(%q) succeeded, want an error", s)
		}
	}
}
