package advisor

import (
	"testing"

	"poker-game-analyzer/pkg/equity"
	"poker-game-analyzer/pkg/table"
)

// Being covered is not the same as being level, and the advice must not come
// out the same.
//
// Live: hero holding 68,080 against an opponent with 3.14M. Hero shoving is
// hero's whole stack and two per cent of theirs -- so they are committed to
// nothing and call as wide as they please, and every hand of hero's that was
// only good because it folded people out stops being good. The model measured
// commitment against the *effective* stack, which is hero's when hero is
// shorter, and so treated the caller as pot-committed by a bet that barely
// touched them.
func TestBeingCoveredIsNotTheSameAsBeingLevel(t *testing.T) {
	// A hand with no showdown value: everything it is worth comes from folding
	// people out, which is precisely what being covered takes away.
	build := func(villainStack float64) Inputs {
		state, in := liveShoveSpot(t, "7h 2c", "Ks Qd 5s Ah 9d", 60000, 30000, 1)
		state.Seats[0].Stack = 30000
		state.Seats[1].Stack = villainStack
		in.State = state
		return in
	}

	level := Calculate(build(30000))
	covered := Calculate(build(3140000))

	evOf := func(r AdvisorResponse, act table.ActionType) (ev, fold float64) {
		for _, a := range r.Actions {
			if a.Action == act {
				return a.EV, a.FoldEquity
			}
		}
		return 0, 0
	}
	levelEV, levelFold := evOf(level, table.ActionAllIn)
	coveredEV, coveredFold := evOf(covered, table.ActionAllIn)

	t.Logf("level   : %-7s %-9.0f  shove ev=%.0f foldEq=%.3f",
		level.PrimaryAction, level.RecommendedAmount, levelEV, levelFold)
	t.Logf("covered : %-7s %-9.0f  shove ev=%.0f foldEq=%.3f",
		covered.PrimaryAction, covered.RecommendedAmount, coveredEV, coveredFold)

	// Somebody risking one per cent of their stack does not fold as often as
	// somebody risking all of it.
	if coveredFold >= levelFold {
		t.Errorf("fold equity against a stack a hundred times deeper (%.3f) is not below level stacks (%.3f)",
			coveredFold, levelFold)
	}
	// And so a hand with nothing but fold equity is worth less.
	if coveredEV >= levelEV {
		t.Errorf("shoving air is worth as much when covered (%.0f) as when level (%.0f)", coveredEV, levelEV)
	}
}

// The other side, so this is a correction and not a blanket tax on being short:
// a hand that is ahead of a wide calling range gains when the deep opponent
// calls light, and must not be discouraged from betting it.
func TestValueDoesNotSufferFromBeingCovered(t *testing.T) {
	build := func(villainStack float64) Inputs {
		state, in := liveShoveSpot(t, "5h 5c", "5s Kd 2c 7h 9d", 60000, 30000, 1)
		state.Seats[0].Stack = 30000
		state.Seats[1].Stack = villainStack
		in.State = state
		return in
	}

	level := Calculate(build(30000))
	covered := Calculate(build(3140000))
	t.Logf("set, level   : %s %.0f", level.PrimaryAction, level.RecommendedAmount)
	t.Logf("set, covered : %s %.0f", covered.PrimaryAction, covered.RecommendedAmount)

	for _, r := range []AdvisorResponse{level, covered} {
		switch r.PrimaryAction {
		case table.ActionBet, table.ActionRaise, table.ActionAllIn:
		default:
			t.Errorf("a set stopped betting: %s", r.PrimaryAction)
		}
	}
}

// A call cannot be cheaper than the chips already in front of somebody else.
//
// Live: two players all-in for 199,680 apiece, and the tool recommended CALL
// 2,000 -- the big blind. The amount owed comes off the call button, which is
// the one place the client states it outright, and when that does not read what
// is left is a number from somewhere else. The felt is the check: those chips
// are read separately and cannot be smaller than the wager they represent.
func TestAmountOwedIsCheckedAgainstTheChipsOnTheFelt(t *testing.T) {
	heroCards := parseHeroCards(t, "Ks 5h")
	state := table.HandState{
		HandID: "h", Street: table.StreetPreflop, HeroID: "hero",
		Pot: 406280, HeroCards: heroCards, IsHeroTurn: true,
		// The button misread: the big blind instead of the all-in.
		CurrentBet: 2000,
		Seats: []table.SeatState{
			{PlayerID: "hero", Stack: 185760, CurrentBet: 0, IsActive: true, Position: table.PosBTN},
			{PlayerID: "shover", Stack: 0, CurrentBet: 199680, IsActive: true, Position: table.PosCO},
			{PlayerID: "folded", Stack: 4030000, CurrentBet: 199680, IsActive: true, IsFolded: true},
		},
	}

	advice := CalculateAdvice(state, equity.EquityResult{WinRate: 0.14, LoseRate: 0.86}, nil)
	t.Logf("%s %.0f: %s", advice.PrimaryAction, advice.RecommendedAmount, advice.Reasoning)

	// Calling costs everything hero has: the price is 199,680 and there is
	// 185,760 behind, so the call is for the stack. What must not happen is
	// being offered the big blind as the price of an all-in.
	for _, a := range advice.Actions {
		if a.Action == table.ActionCall && a.Amount < 185760 {
			t.Errorf("offered to call %.0f against 199,680 with 185,760 behind", a.Amount)
		}
	}
	// And with fourteen per cent against that price, the answer is to fold.
	if advice.PrimaryAction == table.ActionCall {
		t.Errorf("called an all-in with 14%% equity: %s %.0f", advice.PrimaryAction, advice.RecommendedAmount)
	}
}
