package advice

import (
	"math"
	"testing"

	"poker-game-analyzer/pkg/table"
)

// seat is a live opponent at a position with chips out in front of them.
func seat(id string, pos table.Position, bet float64) table.SeatState {
	return table.SeatState{PlayerID: id, Position: pos, CurrentBet: bet, IsActive: true}
}

func act(id string, street table.Street, a table.ActionType, amt float64) table.ActionRecord {
	return table.ActionRecord{PlayerID: id, Street: street, Action: a, Amount: amt}
}

// The whole point of the model: what a player did this hand moves their range,
// and it moves it whether or not anybody has ever seen them before.
func TestRangeWidthFromAction(t *testing.T) {
	base := table.HandState{
		Street: table.StreetPreflop, SmallBlind: 1, BigBlind: 2, HeroID: "hero",
	}

	cases := []struct {
		name    string
		history []table.ActionRecord
		seats   []table.SeatState
		who     string
		want    float64
	}{
		{
			name:    "under the gun open",
			history: []table.ActionRecord{act("utg", table.StreetPreflop, table.ActionRaise, 5)},
			seats:   []table.SeatState{seat("utg", table.PosUTG, 5)},
			want:    16,
		},
		{
			name:    "button open is far wider than the same open under the gun",
			history: []table.ActionRecord{act("btn", table.StreetPreflop, table.ActionRaise, 5)},
			seats:   []table.SeatState{seat("btn", table.PosBTN, 5)},
			want:    43,
		},
		{
			name: "three-bet",
			history: []table.ActionRecord{
				act("utg", table.StreetPreflop, table.ActionRaise, 5),
				act("btn", table.StreetPreflop, table.ActionRaise, 16),
			},
			seats: []table.SeatState{seat("btn", table.PosBTN, 16)},
			who:   "btn",
			want:  threeBetWidth,
		},
		{
			name: "four-bet",
			history: []table.ActionRecord{
				act("utg", table.StreetPreflop, table.ActionRaise, 5),
				act("btn", table.StreetPreflop, table.ActionRaise, 16),
				act("utg", table.StreetPreflop, table.ActionRaise, 40),
			},
			seats: []table.SeatState{seat("utg", table.PosUTG, 40)},
			who:   "utg",
			want:  fourBetWidth,
		},
		{
			name: "cold call of a raise",
			history: []table.ActionRecord{
				act("co", table.StreetPreflop, table.ActionRaise, 5),
				act("btn", table.StreetPreflop, table.ActionCall, 5),
			},
			seats: []table.SeatState{seat("btn", table.PosBTN, 5)},
			who:   "btn",
			want:  coldCallWidth,
		},
		{
			name:    "limp",
			history: []table.ActionRecord{act("mp", table.StreetPreflop, table.ActionCall, 2)},
			seats:   []table.SeatState{seat("mp", table.PosMP, 2)},
			who:     "mp",
			want:    limpWidth,
		},
		{
			name:    "big blind nobody raised",
			history: []table.ActionRecord{act("mp", table.StreetPreflop, table.ActionCall, 2)},
			seats:   []table.SeatState{seat("bb", table.PosBB, 2)},
			who:     "bb",
			want:    bigBlindCheckWidth,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := base
			h.ActionHistory = c.history
			h.Seats = c.seats
			who := c.who
			if who == "" {
				who = c.seats[0].PlayerID
			}
			var s table.SeatState
			for _, st := range c.seats {
				if st.PlayerID == who {
					s = st
				}
			}
			got := RangeWidthFor(h, s, 0, false, Shape{})
			if math.Abs(got-c.want) > 0.01 {
				t.Fatalf("width = %.2f, want %.2f", got, c.want)
			}
		})
	}
}

// A range narrows further as the hand goes on, and a raise narrows it far more
// than a bet does -- which is the finding that separates this from the simulated
// bots' single factor.
func TestPostflopNarrowing(t *testing.T) {
	h := table.HandState{
		Street: table.StreetFlop, SmallBlind: 1, BigBlind: 2, HeroID: "hero",
		Seats: []table.SeatState{seat("btn", table.PosBTN, 0), seat("hero", table.PosBB, 0)},
		ActionHistory: []table.ActionRecord{
			act("btn", table.StreetPreflop, table.ActionRaise, 5),
			act("hero", table.StreetPreflop, table.ActionCall, 5),
		},
	}
	s := h.Seats[0]

	open := RangeWidthFor(h, s, 0, false, Shape{})
	if math.Abs(open-43) > 0.01 {
		t.Fatalf("button open = %.2f, want 43", open)
	}

	betting := h
	betting.ActionHistory = append(append([]table.ActionRecord{}, h.ActionHistory...),
		act("hero", table.StreetFlop, table.ActionCheck, 0),
		act("btn", table.StreetFlop, table.ActionBet, 4))
	afterBet := RangeWidthFor(betting, s, 0, false, Shape{})
	if math.Abs(afterBet-43*betNarrowing) > 0.01 {
		t.Fatalf("after a c-bet = %.2f, want %.2f", afterBet, 43*betNarrowing)
	}

	raising := h
	raising.ActionHistory = append(append([]table.ActionRecord{}, h.ActionHistory...),
		act("hero", table.StreetFlop, table.ActionBet, 4),
		act("btn", table.StreetFlop, table.ActionRaise, 14))
	afterRaise := RangeWidthFor(raising, s, 0, false, Shape{})
	if math.Abs(afterRaise-43*raiseNarrowing) > 0.01 {
		t.Fatalf("after a raise = %.2f, want %.2f", afterRaise, 43*raiseNarrowing)
	}
	if afterRaise >= afterBet {
		t.Fatalf("a raise (%.2f) must narrow more than a bet (%.2f)", afterRaise, afterBet)
	}
}

// A read moves the width and does not become it: the same open is wider from a
// station than from a nit, and neither replaces the positional figure.
func TestReadScalesWidth(t *testing.T) {
	h := table.HandState{
		Street: table.StreetPreflop, SmallBlind: 1, BigBlind: 2, HeroID: "hero",
		Seats:         []table.SeatState{seat("co", table.PosCO, 5)},
		ActionHistory: []table.ActionRecord{act("co", table.StreetPreflop, table.ActionRaise, 5)},
	}
	s := h.Seats[0]

	unknown := RangeWidthFor(h, s, 0, false, Shape{})
	nit := RangeWidthFor(h, s, 12, true, Shape{})
	station := RangeWidthFor(h, s, 48, true, Shape{})

	if !(nit < unknown && unknown < station) {
		t.Fatalf("nit %.1f < unknown %.1f < station %.1f does not hold", nit, unknown, station)
	}
	if station > 100 {
		t.Fatalf("width %.1f is more than every hand", station)
	}
}

// Live, the action history is reconstructed from nameplate badges and is often
// missing. The chips in front of a player are a direct observation, and a
// three-bet has to still read as one without any history at all.
func TestWidthFromChipsWithoutHistory(t *testing.T) {
	h := table.HandState{
		Street: table.StreetPreflop, SmallBlind: 1, BigBlind: 2, HeroID: "hero",
		Seats: []table.SeatState{
			seat("co", table.PosCO, 5),
			seat("btn", table.PosBTN, 17),
		},
	}

	opener := RangeWidthFor(h, h.Seats[0], 0, false, Shape{})
	threeBettor := RangeWidthFor(h, h.Seats[1], 0, false, Shape{})

	if math.Abs(opener-27) > 0.01 {
		t.Fatalf("cutoff open from chips = %.2f, want 27", opener)
	}
	if math.Abs(threeBettor-threeBetWidth) > 0.01 {
		t.Fatalf("three-bet from chips = %.2f, want %.2f", threeBettor, threeBetWidth)
	}
}

// Nothing observed means nothing claimed. A player who has put in no voluntary
// money is still holding any two cards, and the model must not invent a read.
func TestUnknownStaysWide(t *testing.T) {
	h := table.HandState{
		Street: table.StreetPreflop, SmallBlind: 1, BigBlind: 2, HeroID: "hero",
		Seats: []table.SeatState{seat("mp", table.PosMP, 0)},
	}
	if got := RangeWidthFor(h, h.Seats[0], 0, false, Shape{}); got != unknownWidth {
		t.Fatalf("width = %.2f, want %.2f", got, unknownWidth)
	}
}

// After the flop with no history at all -- the live fallback -- the chips in
// front of a player describe this street and not the last one. What is left is
// the pot: a pot that was raised is not a pot full of random hands.
func TestPostflopWithoutHistoryReadsThePot(t *testing.T) {
	raised := table.HandState{
		Street: table.StreetFlop, SmallBlind: 1, BigBlind: 2, HeroID: "hero", Pot: 12,
		Seats: []table.SeatState{seat("x", table.PosCO, 0)},
	}
	if got := RangeWidthFor(raised, raised.Seats[0], 0, false, Shape{}); got != raisedPotWidth {
		t.Fatalf("width in a raised pot = %.2f, want %.2f", got, raisedPotWidth)
	}

	limped := raised
	limped.Pot = 4
	if got := RangeWidthFor(limped, limped.Seats[0], 0, false, Shape{}); got != limpWidth {
		t.Fatalf("width in an unraised pot = %.2f, want %.2f", got, limpWidth)
	}
}

// A big blind who called a raise has the same chips in front of them as the
// player who raised. Only the nameplate badge tells the two apart, and calling
// a defending range an opening range is the expensive way to get it wrong.
func TestCallerIsNotAnOpener(t *testing.T) {
	h := table.HandState{
		Street: table.StreetPreflop, SmallBlind: 1, BigBlind: 2, HeroID: "hero",
		Seats: []table.SeatState{
			{PlayerID: "co", Position: table.PosCO, CurrentBet: 5, LastAction: "raise", IsActive: true},
			{PlayerID: "bb", Position: table.PosBB, CurrentBet: 5, LastAction: "call", IsActive: true},
		},
	}

	opener := RangeWidthFor(h, h.Seats[0], 0, false, Shape{})
	caller := RangeWidthFor(h, h.Seats[1], 0, false, Shape{})

	if math.Abs(opener-27) > 0.01 {
		t.Fatalf("cutoff open = %.2f, want 27", opener)
	}
	if math.Abs(caller-coldCallWidth) > 0.01 {
		t.Fatalf("big blind defending = %.2f, want %.2f", caller, coldCallWidth)
	}
}

// coldCaller is a player who called an open before the flop: the commonest
// postflop opponent there is, and the spot the calibration found worst.
func coldCaller() (table.HandState, table.SeatState) {
	s := seat("v", table.PosCO, 0)
	h := table.HandState{
		Street: table.StreetPreflop, SmallBlind: 1, BigBlind: 2, HeroID: "hero",
		Seats: []table.SeatState{s},
		ActionHistory: []table.ActionRecord{
			act("btn", table.StreetPreflop, table.ActionRaise, 6),
			act("v", table.StreetPreflop, table.ActionCall, 6),
		},
	}
	return h, s
}

// The zero Shape has to mean "the model as it was", because every existing
// caller passes one and none of them is asking for a change.
func TestShape_ZeroValueIsTheOldModel(t *testing.T) {
	h, s := coldCaller()
	if got, want := RangeWidthFor(h, s, 0, false, Shape{}),
		RangeWidthFor(h, s, 0, false, DefaultShape()); got != want {
		t.Errorf("zero Shape gave %v, the default gave %v", got, want)
	}
}

// The calibrated shape widens opponents after the flop and leaves them alone
// before it: marking the model against dealt cards found the preflop half
// right -- 81% assigned against 88% needed -- and only the postflop half wrong.
func TestShape_CalibratedWidensOnlyAfterTheFlop(t *testing.T) {
	h, s := coldCaller()

	pre := RangeWidthFor(h, s, 0, false, DefaultShape())
	preWide := RangeWidthFor(h, s, 0, false, CalibratedShape())
	if pre != preWide {
		t.Errorf("preflop width moved from %v to %v; it was measured correct", pre, preWide)
	}

	flop := h
	flop.Street = table.StreetFlop
	narrow := RangeWidthFor(flop, s, 0, false, DefaultShape())
	wide := RangeWidthFor(flop, s, 0, false, CalibratedShape())
	if wide <= narrow {
		t.Errorf("postflop width %v is not wider than %v", wide, narrow)
	}
	if ratio := wide / narrow; ratio < 1.5 || ratio > 2.5 {
		t.Errorf("postflop width moved by %.2fx; the measurement said about 1.9", ratio)
	}
}

// A raise is where the model was most wrong -- 9% assigned against 32% needed --
// so the calibrated shape has to narrow least on exactly that action.
func TestShape_CalibratedNarrowsLeastOnARaise(t *testing.T) {
	d, c := DefaultShape(), CalibratedShape()
	if !(c.Raise/d.Raise > c.Bet/d.Bet && c.Bet/d.Bet > c.Call/d.Call) {
		t.Errorf("the correction is not largest on a raise: raise %v->%v, bet %v->%v, call %v->%v",
			d.Raise, c.Raise, d.Bet, c.Bet, d.Call, c.Call)
	}
}
