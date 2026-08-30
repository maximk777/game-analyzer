package advisor

import (
	"fmt"
	"testing"

	"poker-game-analyzer/pkg/equity"
	"poker-game-analyzer/pkg/table"
)

// Each test here pins a defect found by auditing the advisor against live
// screenshots. Statement coverage was already high before any of them existed:
// the old tests exercised the code without ever asking whether the advice was
// right.

func seatsFor(heroStack float64, villainStacks ...float64) []table.SeatState {
	seats := []table.SeatState{{PlayerID: "hero", Stack: heroStack, IsActive: true}}
	for i, s := range villainStacks {
		seats = append(seats, table.SeatState{
			PlayerID: fmt.Sprintf("villain-%d", i), Stack: s, IsActive: true,
		})
	}
	return seats
}

// With 20% equity against 33% pot odds the only correct answer is fold. The
// previous model recommended a raise, because it assumed every unknown opponent
// folds 35% of the time and priced a bluff off that invented number.
func TestNoReads_WeakEquityFacingBet_Folds(t *testing.T) {
	state := table.HandState{
		HandID: "h", Street: table.StreetFlop, HeroID: "hero",
		Pot: 1000, CurrentBet: 500,
		Seats: seatsFor(20000, 20000),
	}

	resp := CalculateAdvice(state, equity.EquityResult{WinRate: 0.20}, nil)

	if resp.PrimaryAction != table.ActionFold {
		t.Errorf("20%% equity vs 33%% pot odds should fold, got %s %.2f",
			resp.PrimaryAction, resp.RecommendedAmount)
	}
	if resp.HasReads {
		t.Error("HasReads must be false when no tendencies were supplied")
	}
}

// Fold equity may only come from observation. Without a read the aggressive
// branch stays shut, so the tool cannot invent a bluff.
func TestNoReads_BluffBranchIsClosed(t *testing.T) {
	state := table.HandState{
		HandID: "h", Street: table.StreetFlop, HeroID: "hero",
		Pot: 1000, Seats: seatsFor(20000, 20000),
	}

	resp := CalculateAdvice(state, equity.EquityResult{WinRate: 0.30}, nil)

	switch resp.PrimaryAction {
	case table.ActionBet, table.ActionRaise, table.ActionAllIn:
		t.Errorf("bluffed with 30%% equity and no reads: %s %.2f",
			resp.PrimaryAction, resp.RecommendedAmount)
	}
}

// The same equity in a heads-up pot and a six-way pot is not the same spot.
// The old EV formula assumed exactly one caller, so advice was byte-identical
// regardless of how many players were live.
func TestMultiway_ChangesTheAdvice(t *testing.T) {
	reads := map[string]float64{"fold_to_cbet": 0.55}

	// Equity high enough that betting is chosen in both, so the comparison is
	// between two aggressive lines rather than between two checks.
	headsUp := CalculateAdvice(table.HandState{
		HandID: "h", Street: table.StreetFlop, HeroID: "hero",
		Pot: 1000, Seats: seatsFor(50000, 50000),
	}, equity.EquityResult{WinRate: 0.85}, reads)

	sixWay := CalculateAdvice(table.HandState{
		HandID: "h", Street: table.StreetFlop, HeroID: "hero",
		Pot: 1000, Seats: seatsFor(50000, 50000, 50000, 50000, 50000, 50000),
	}, equity.EquityResult{WinRate: 0.85}, reads)

	if headsUp.Opponents != 1 {
		t.Errorf("expected 1 opponent heads-up, got %d", headsUp.Opponents)
	}
	if sixWay.Opponents != 5 {
		t.Errorf("expected 5 opponents six-way, got %d", sixWay.Opponents)
	}

	hu := primaryOf(headsUp)
	sw := primaryOf(sixWay)
	if hu.EV == sw.EV && hu.Amount == sw.Amount && hu.Action == sw.Action {
		t.Errorf("multiway made no difference: both %s %.2f EV %.2f",
			hu.Action, hu.Amount, hu.EV)
	}
	// Everyone folding is strictly less likely with more players to get through.
	if sixWay.Opponents > headsUp.Opponents && sw.FoldEquity >= hu.FoldEquity {
		t.Errorf("fold equity did not fall with more opponents: %.3f six-way vs %.3f heads-up",
			sw.FoldEquity, hu.FoldEquity)
	}
}

// Chips beyond the shortest live opponent's stack can never be called, so no
// sizing may exceed the effective stack. Opponent stacks were captured by the
// vision layer but never consulted by the maths.
func TestEffectiveStack_CapsEverySizing(t *testing.T) {
	state := table.HandState{
		HandID: "h", Street: table.StreetFlop, HeroID: "hero",
		Pot: 1000, Seats: seatsFor(100000, 300),
	}

	resp := CalculateAdvice(state, equity.EquityResult{WinRate: 0.72}, nil)

	if resp.EffectiveStack != 300 {
		t.Errorf("effective stack should be the short opponent's 300, got %.2f", resp.EffectiveStack)
	}
	for _, act := range resp.Actions {
		if act.Amount > 300.0001 {
			t.Errorf("%s sizes %.2f above the effective stack of 300", act.SizingLabel, act.Amount)
		}
	}
}

// Nothing may be invented when stacks were never observed: no fabricated
// ceiling, and no all-in whose amount would be guesswork.
func TestUnknownStacks_NoFabricatedAllIn(t *testing.T) {
	state := table.HandState{
		HandID: "h", Street: table.StreetFlop, Pot: 1000,
	}

	resp := CalculateAdvice(state, equity.EquityResult{WinRate: 0.72}, nil)

	if resp.EffectiveStack != 0 {
		t.Errorf("effective stack should be 0 (unobserved), got %.2f", resp.EffectiveStack)
	}
	for _, act := range resp.Actions {
		if act.Action == table.ActionAllIn {
			t.Errorf("offered an all-in of %.2f with no stack information", act.Amount)
		}
	}
}

// All-in was computed, displayed, and then excluded from selection, so it could
// never be recommended however correct it was.
func TestAllIn_IsReachable(t *testing.T) {
	// Short effective stack and a huge edge: shoving is the whole strategy.
	state := table.HandState{
		HandID: "h", Street: table.StreetRiver, HeroID: "hero",
		Pot: 1000, Seats: seatsFor(400, 400),
	}

	resp := CalculateAdvice(state, equity.EquityResult{WinRate: 0.97}, map[string]float64{"fold_to_bet": 0.20})

	if resp.PrimaryAction != table.ActionAllIn {
		t.Errorf("expected all-in with 97%% equity and a 400-chip effective stack, got %s %.2f",
			resp.PrimaryAction, resp.RecommendedAmount)
	}
}

// Sizing used to be a step function of equity: below 50% always the smallest
// option, above 50% always the largest, with nothing in between and no
// dependence on anything else.
func TestSizing_IsNotAStepAtFiftyPercent(t *testing.T) {
	reads := map[string]float64{"fold_to_cbet": 0.50}
	sizes := map[float64]float64{}

	for _, eqv := range []float64{0.55, 0.65, 0.75, 0.85, 0.95} {
		resp := CalculateAdvice(table.HandState{
			HandID: "h", Street: table.StreetFlop, HeroID: "hero",
			Pot: 1000, Seats: seatsFor(50000, 50000),
		}, equity.EquityResult{WinRate: eqv}, reads)
		sizes[eqv] = resp.RecommendedAmount
	}

	distinct := map[float64]bool{}
	for _, v := range sizes {
		distinct[v] = true
	}
	if len(distinct) < 2 {
		t.Errorf("sizing did not respond to equity at all across 55-95%%: %v", sizes)
	}
}

// Being called by a large bet means being called by a stronger range. Without
// this, EV rose without bound in the bet size and the model would shove any
// stack whenever raw equity passed 50%.
func TestEquityWhenCalled_ShrinksWithBetSize(t *testing.T) {
	if got := equityWhenCalled(0.70, 0.0); got != 0.70 {
		t.Errorf("with nothing folding, equity when called should be unchanged: got %.3f", got)
	}
	small := equityWhenCalled(0.70, 0.25)
	large := equityWhenCalled(0.70, 0.60)
	if !(small > large) {
		t.Errorf("equity when called must fall as the calling range tightens: %.3f then %.3f", small, large)
	}
	if got := equityWhenCalled(0.70, 0.95); got != 0 {
		t.Errorf("when only the top 5%% calls, 70%% raw equity should collapse: got %.3f", got)
	}
}

func primaryOf(r AdvisorResponse) ActionRecommendation {
	for _, a := range r.Actions {
		if a.IsPrimary {
			return a
		}
	}
	return ActionRecommendation{}
}

// Recorded live: hero held Qc Td on 9s 9c Js 2d As -- the only pair was on the
// board -- and faced a half-pot river bet of 32,374 into 64,748. Against a
// random hand that scored 33.7% equity against 33.3% pot odds, so the tool said
// call, then said fold five seconds later when the simulation re-rolled. A
// player betting the river does not hold a random hand.
func TestRiverBet_EquityIsMeasuredAgainstTheBettingRange(t *testing.T) {
	state := table.HandState{
		HandID: "h", Street: table.StreetRiver, HeroID: "hero",
		Pot: 64748, CurrentBet: 32374,
		Seats: seatsFor(97480, 97480),
	}

	// Equity against everything is 33.7%; against the range that actually bets
	// half pot, queen-high is drawing nearly dead.
	var askedFor float64
	resp := Calculate(Inputs{
		State:  state,
		Equity: equity.EquityResult{WinRate: 0.337},
		EquityVsTop: func(frac float64) float64 {
			askedFor = frac
			return 0.04
		},
	})

	if askedFor <= 0 || askedFor >= 1 {
		t.Fatalf("the call was not priced against a narrowed range: asked for top %.2f", askedFor)
	}
	// A half-pot bet implies roughly the top half of the range, and never the
	// whole of it.
	if askedFor > 0.6 {
		t.Errorf("a half-pot bet should imply a range meaningfully tighter than random, got top %.2f", askedFor)
	}
	if resp.PrimaryAction != table.ActionFold {
		t.Errorf("expected fold with 4%% equity against the betting range, got %s %.0f",
			resp.PrimaryAction, resp.RecommendedAmount)
	}
}

// The narrowing must respond to size: a small bet implies a wide range, a large
// one a tight range.
func TestBettorRangeFraction_TightensWithSize(t *testing.T) {
	quarter := bettorRangeFraction(25, 125)   // 25 into a pot that was 100
	half := bettorRangeFraction(50, 150)      // 50 into a pot that was 100
	potSized := bettorRangeFraction(100, 200) // 100 into a pot that was 100

	if !(quarter > half && half > potSized) {
		t.Errorf("range should tighten as the bet grows: quarter=%.3f half=%.3f pot=%.3f",
			quarter, half, potSized)
	}
	if potSized > 0.4 {
		t.Errorf("a pot-sized bet should imply roughly the top third, got %.3f", potSized)
	}
}

// Preflop the money owed is largely forced: blinds and straddles are posted
// with whatever the dealer gave out. Treating that as a chosen bet priced
// hero's ace-king against a top-39% range and folded it getting better than
// 3 to 1.
func TestPreflop_ForcedMoneyDoesNotNarrowTheRange(t *testing.T) {
	resp := Calculate(Inputs{
		State: table.HandState{
			HandID: "h", Street: table.StreetPreflop, HeroID: "hero",
			Pot: 4600, CurrentBet: 2000,
			Seats: seatsFor(78256, 100000, 237786, 197680, 802080, 235006),
		},
		Equity:      equity.EquityResult{WinRate: 0.35},
		EquityVsTop: func(frac float64) float64 { return 0.16 },
	})

	// Only the narrowing is asserted here. Whether this particular marginal
	// spot calls or folds is settled by equity realisation, which is a separate
	// question with its own tests -- five-way and out of position, 35% raw
	// equity does not survive to showdown intact.
	if resp.CallRangeFraction != 1.0 {
		t.Errorf("the preflop call was priced against the top %.0f%% instead of the whole range",
			resp.CallRangeFraction*100)
	}
}

// After the flop a bet is a choice, and the range behind it must narrow.
func TestPostflop_BetDoesNarrowTheRange(t *testing.T) {
	resp := Calculate(Inputs{
		State: table.HandState{
			HandID: "h", Street: table.StreetFlop, HeroID: "hero",
			Pot: 64748, CurrentBet: 32374,
			Seats: seatsFor(97480, 97480),
		},
		Equity:      equity.EquityResult{WinRate: 0.337},
		EquityVsTop: func(frac float64) float64 { return 0.04 },
	})

	if resp.CallRangeFraction >= 1.0 {
		t.Error("a postflop bet was priced against the opponent's whole range")
	}
}

// The two hands that proved preflop cannot be decided by equity against pot
// odds. Both were folded live; both are standard chart continues.
func TestPreflop_ChartOverridesTheEquityComparison(t *testing.T) {
	seats := func(heroPos table.Position) []table.SeatState {
		return []table.SeatState{
			{PlayerID: "hero", Stack: 75616, IsActive: true, Position: heroPos},
			{PlayerID: "v1", Stack: 200000, IsActive: true, Position: table.PosBTN, LastAction: "raise"},
			{PlayerID: "v2", Stack: 200000, IsActive: true, Position: table.PosCO},
		}
	}

	threes, err := table.ParseCards("3h 3c")
	if err != nil {
		t.Fatalf("parsing threes: %v", err)
	}
	resp := Calculate(Inputs{
		State: table.HandState{
			HandID: "h", Street: table.StreetPreflop, HeroID: "hero",
			Pot: 4920, CurrentBet: 2000,
			HeroCards: [2]table.Card{threes[0], threes[1]},
			Seats:     seats(table.PosBB),
		},
		// Raw equity is far below the 28.9% the pot odds demand -- which is
		// exactly why this cannot be a pot-odds decision.
		Equity: equity.EquityResult{WinRate: 0.159},
	})
	if resp.PrimaryAction != table.ActionCall {
		t.Errorf("pocket threes in the big blind: got %s, want call", resp.PrimaryAction)
	}

	ak, err := table.ParseCards("Ac Kc")
	if err != nil {
		t.Fatalf("parsing ace-king: %v", err)
	}
	resp = Calculate(Inputs{
		State: table.HandState{
			HandID: "h", Street: table.StreetPreflop, HeroID: "hero",
			Pot: 4600, CurrentBet: 2000,
			HeroCards: [2]table.Card{ak[0], ak[1]},
			Seats:     seats(table.PosBB),
		},
		Equity: equity.EquityResult{WinRate: 0.229},
	})
	switch resp.PrimaryAction {
	case table.ActionRaise, table.ActionBet, table.ActionAllIn:
	default:
		t.Errorf("ace-king suited in the big blind: got %s, want a raise", resp.PrimaryAction)
	}
}

// Junk stays a fold, so the charts are not simply making the tool call more.
func TestPreflop_ChartStillFoldsJunk(t *testing.T) {
	junk, err := table.ParseCards("7h 2d")
	if err != nil {
		t.Fatalf("parsing seven-deuce: %v", err)
	}
	resp := Calculate(Inputs{
		State: table.HandState{
			HandID: "h", Street: table.StreetPreflop, HeroID: "hero",
			Pot: 4600, CurrentBet: 2000,
			HeroCards: [2]table.Card{junk[0], junk[1]},
			Seats: []table.SeatState{
				{PlayerID: "hero", Stack: 75616, IsActive: true, Position: table.PosBB},
				{PlayerID: "v1", Stack: 200000, IsActive: true, LastAction: "raise"},
			},
		},
		Equity: equity.EquityResult{WinRate: 0.30},
	})
	if resp.PrimaryAction != table.ActionFold {
		t.Errorf("seven-deuce offsuit facing a raise: got %s, want fold", resp.PrimaryAction)
	}
}

// Without a position there is no chart, and the EV comparison stands. Nothing
// is invented to fill the gap.
func TestPreflop_NoPositionMeansNoChart(t *testing.T) {
	threes, err := table.ParseCards("3h 3c")
	if err != nil {
		t.Fatalf("parsing threes: %v", err)
	}
	resp := Calculate(Inputs{
		State: table.HandState{
			HandID: "h", Street: table.StreetPreflop, HeroID: "hero",
			Pot: 4920, CurrentBet: 2000,
			HeroCards: [2]table.Card{threes[0], threes[1]},
			Seats: []table.SeatState{
				{PlayerID: "hero", Stack: 75616, IsActive: true},
				{PlayerID: "v1", Stack: 200000, IsActive: true, LastAction: "raise"},
			},
		},
		Equity: equity.EquityResult{WinRate: 0.159},
	})
	if resp.PrimaryAction != table.ActionFold {
		t.Errorf("with no position the EV comparison should still fold this: got %s", resp.PrimaryAction)
	}
}

// A one-street model scored the flop and the river identically at any stack
// depth, which is the clearest sign that the streets still to come were
// invisible to it. Continuing without committing does not realise a hand's
// whole all-in equity.
func TestEquityRealisation_DependsOnStreetPositionAndOpponents(t *testing.T) {
	// The river has no streets left, so equity is realised in full.
	if got := equityRealisation(table.StreetRiver, table.PosBB, 3, false); got != 1.0 {
		t.Errorf("river realisation: got %.2f, want 1.0", got)
	}
	// Nor does an all-in leave anything to realise.
	if got := equityRealisation(table.StreetFlop, table.PosBB, 3, true); got != 1.0 {
		t.Errorf("all-in realisation: got %.2f, want 1.0", got)
	}

	inPosition := equityRealisation(table.StreetFlop, table.PosBTN, 1, false)
	outOfPosition := equityRealisation(table.StreetFlop, table.PosBB, 1, false)
	if !(inPosition > outOfPosition) {
		t.Errorf("position should help realisation: button %.2f, big blind %.2f",
			inPosition, outOfPosition)
	}

	headsUp := equityRealisation(table.StreetFlop, table.PosBTN, 1, false)
	fiveWay := equityRealisation(table.StreetFlop, table.PosBTN, 4, false)
	if !(fiveWay < headsUp) {
		t.Errorf("more opponents should cost realisation: heads-up %.2f, five-way %.2f",
			headsUp, fiveWay)
	}
}

// Realisation must not be applied to bet sizing. It gives an all-in the full
// value of its equity while discounting every smaller size, so applying it
// there makes shoving look better the less the model understands about the
// streets it skips -- the opposite of the correction intended.
func TestEquityRealisation_DoesNotBiasTowardsShoving(t *testing.T) {
	state := table.HandState{
		HandID: "h", Street: table.StreetFlop, HeroID: "hero",
		Pot: 1000,
		Seats: []table.SeatState{
			{PlayerID: "hero", Stack: 12000, IsActive: true, Position: table.PosBTN},
			{PlayerID: "v", Stack: 12000, IsActive: true},
		},
	}
	resp := Calculate(Inputs{
		State: state, Equity: equity.EquityResult{WinRate: 0.715},
		OppTendencies: map[string]float64{"fold_to_cbet": 0.50}, ReadHands: 200,
	})

	if resp.PrimaryAction == table.ActionAllIn {
		t.Errorf("shoved a stack twelve times the pot with 71.5%% equity: %s %.0f",
			resp.PrimaryAction, resp.RecommendedAmount)
	}
}
