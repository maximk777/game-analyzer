package advisor

import (
	"math"
	"testing"

	"poker-game-analyzer/pkg/equity"
	"poker-game-analyzer/pkg/table"
)

func parseHeroCards(t *testing.T, s string) [2]table.Card {
	t.Helper()
	cards, err := table.ParseCards(s)
	if err != nil || len(cards) != 2 {
		t.Fatalf("failed to parse hero cards %q: %v", s, err)
	}
	return [2]table.Card{cards[0], cards[1]}
}

func parseBoardCards(t *testing.T, s string) []table.Card {
	t.Helper()
	if s == "" {
		return nil
	}
	cards, err := table.ParseCards(s)
	if err != nil {
		t.Fatalf("failed to parse board cards %q: %v", s, err)
	}
	return cards
}

func almostEqual(a, b, epsilon float64) bool {
	return math.Abs(a-b) <= epsilon
}

func TestPotOddsCalculation(t *testing.T) {
	heroCards := parseHeroCards(t, "Ah Kd")

	// Case 1: Facing a bet of 50 into a pot of 100 -> to_call = 50, pot = 100 -> PO = 50 / 150 = 0.3333...
	state := table.HandState{
		HandID:     "h-po-1",
		Street:     table.StreetFlop,
		Pot:        100.0,
		CurrentBet: 50.0,
		HeroCards:  heroCards,
	}
	eq := equity.EquityResult{WinRate: 0.40, TieRate: 0.0, LoseRate: 0.60}
	resp := CalculateAdvice(state, eq, nil)

	expectedPO := 50.0 / 150.0
	if !almostEqual(resp.PotOdds, expectedPO, 0.001) {
		t.Errorf("expected PotOdds %.4f, got %.4f", expectedPO, resp.PotOdds)
	}

	// Case 2: No bet to call -> to_call = 0 -> PO = 0.0
	stateNoBet := table.HandState{
		HandID:     "h-po-2",
		Street:     table.StreetFlop,
		Pot:        100.0,
		CurrentBet: 0.0,
		HeroCards:  heroCards,
	}
	respNoBet := CalculateAdvice(stateNoBet, eq, nil)
	if respNoBet.PotOdds != 0.0 {
		t.Errorf("expected PotOdds 0.0 for unbet pot, got %.4f", respNoBet.PotOdds)
	}
}

func TestEVFormulas(t *testing.T) {
	heroCards := parseHeroCards(t, "Ah Kh")
	state := table.HandState{
		HandID:     "h-ev-1",
		Street:     table.StreetFlop,
		Pot:        100.0,
		CurrentBet: 50.0,
		MinRaise:   100.0,
		HeroCards:  heroCards,
	}
	// Equity = 60%, Tie = 0%
	eq := equity.EquityResult{WinRate: 0.60, TieRate: 0.0, LoseRate: 0.40}
	oppTendencies := map[string]float64{"fold_to_cbet": 0.40}

	resp := CalculateAdvice(state, eq, oppTendencies)

	// Check EV(Fold) == 0
	var foldAction, callAction *ActionRecommendation
	for i := range resp.Actions {
		if resp.Actions[i].Action == table.ActionFold {
			foldAction = &resp.Actions[i]
		} else if resp.Actions[i].Action == table.ActionCall {
			callAction = &resp.Actions[i]
		}
	}

	if foldAction == nil {
		t.Fatalf("expected Fold action in recommendations")
	}
	if foldAction.EV != 0.0 {
		t.Errorf("expected EV(Fold) == 0.0, got %.4f", foldAction.EV)
	}

	// EV(Call) = Equity * (Pot + to_call) - to_call
	// EV(Call) = 0.60 * (100 + 50) - 50 = 0.60 * 150 - 50 = 90 - 50 = 40.0
	if callAction == nil {
		t.Fatalf("expected Call action in recommendations")
	}
	expectedCallEV := 0.60*(100.0+50.0) - 50.0
	if !almostEqual(callAction.EV, expectedCallEV, 0.01) {
		t.Errorf("expected EV(Call) == %.2f, got %.2f", expectedCallEV, callAction.EV)
	}

	// EV(Raise) = P_fold * Pot + (1 - P_fold) * (Equity * (Pot + 2*Raise) - Raise)
	pFold := 0.40
	for _, act := range resp.Actions {
		if act.Action == table.ActionRaise {
			expectedRaiseEV := pFold*100.0 + (1.0-pFold)*(0.60*(100.0+2*act.Amount)-act.Amount)
			if !almostEqual(act.EV, expectedRaiseEV, 0.05) {
				t.Errorf("action %s amount %.2f: expected EV %.2f, got %.2f", act.SizingLabel, act.Amount, expectedRaiseEV, act.EV)
			}
		}
	}
}

func TestPreflopThreeBetVsOpen(t *testing.T) {
	heroCards := parseHeroCards(t, "Ah Kh")

	state := table.HandState{
		HandID:     "h-preflop-3bet",
		Street:     table.StreetPreflop,
		Pot:        0.85,
		CurrentBet: 0.50,
		MinRaise:   1.00,
		HeroCards:  heroCards,
	}

	eq := equity.EquityResult{WinRate: 0.70, TieRate: 0.05, LoseRate: 0.25}
	oppTendencies := map[string]float64{"fold_to_3bet": 0.45}

	advice := CalculateAdvice(state, eq, oppTendencies)

	if advice.PrimaryAction != table.ActionRaise {
		t.Errorf("expected primary action Raise for strong equity preflop, got %v", advice.PrimaryAction)
	}
	if advice.RecommendedAmount <= 0.50 {
		t.Errorf("expected raise amount > current bet 0.50, got %.2f", advice.RecommendedAmount)
	}
	if advice.HeroCards[0] != "Ah" || advice.HeroCards[1] != "Kh" {
		t.Errorf("unexpected HeroCards in response: %v", advice.HeroCards)
	}
	if advice.Reasoning == "" {
		t.Errorf("expected non-empty reasoning")
	}
}

func TestPreflopWeakFoldVsOpen(t *testing.T) {
	heroCards := parseHeroCards(t, "7c 2d")

	state := table.HandState{
		HandID:     "h-preflop-fold",
		Street:     table.StreetPreflop,
		Pot:        1.50,
		CurrentBet: 1.00,
		MinRaise:   2.00,
		HeroCards:  heroCards,
	}

	// 18% equity vs open raise range, pot odds = 1.0 / (1.5 + 1.0) = 40%
	eq := equity.EquityResult{WinRate: 0.18, TieRate: 0.02, LoseRate: 0.80}
	oppTendencies := map[string]float64{"fold_to_3bet": 0.20}

	advice := CalculateAdvice(state, eq, oppTendencies)

	if advice.PrimaryAction != table.ActionFold {
		t.Errorf("expected primary action Fold for junk preflop, got %v", advice.PrimaryAction)
	}
	if advice.RecommendedAmount != 0.0 {
		t.Errorf("expected recommended amount 0.0 for fold, got %.2f", advice.RecommendedAmount)
	}
}

func TestFlopCBetValueAndBluff(t *testing.T) {
	// Scenario A: Flop Value C-Bet with Top Set (85% equity, checked to hero)
	heroCardsA := parseHeroCards(t, "Kh Kd")
	boardCardsA := parseBoardCards(t, "Ks 7d 2c")

	stateA := table.HandState{
		HandID:         "h-flop-cbet-val",
		Street:         table.StreetFlop,
		Pot:            6.0,
		CurrentBet:     0.0,
		MinRaise:       2.0,
		CommunityCards: boardCardsA,
		HeroCards:      heroCardsA,
	}
	eqA := equity.EquityResult{WinRate: 0.85, TieRate: 0.01, LoseRate: 0.14}
	oppTendenciesA := map[string]float64{"fold_to_cbet": 0.40}

	adviceA := CalculateAdvice(stateA, eqA, oppTendenciesA)
	if adviceA.PrimaryAction != table.ActionBet && adviceA.PrimaryAction != table.ActionRaise {
		t.Errorf("expected primary action Bet/Raise for strong value flop, got %v", adviceA.PrimaryAction)
	}
	if adviceA.RecommendedAmount <= 0.0 {
		t.Errorf("expected positive bet amount, got %.2f", adviceA.RecommendedAmount)
	}

	// Scenario B: Flop Bluff C-Bet with high opponent fold to cbet (70% fold, weak equity 25%)
	heroCardsB := parseHeroCards(t, "Qh Jh")
	boardCardsB := parseBoardCards(t, "As 8d 3c")

	stateB := table.HandState{
		HandID:         "h-flop-cbet-bluff",
		Street:         table.StreetFlop,
		Pot:            10.0,
		CurrentBet:     0.0,
		MinRaise:       2.0,
		CommunityCards: boardCardsB,
		HeroCards:      heroCardsB,
	}
	eqB := equity.EquityResult{WinRate: 0.25, TieRate: 0.0, LoseRate: 0.75}
	oppTendenciesB := map[string]float64{"fold_to_cbet": 0.70}

	adviceB := CalculateAdvice(stateB, eqB, oppTendenciesB)
	if adviceB.PrimaryAction != table.ActionBet && adviceB.PrimaryAction != table.ActionRaise {
		t.Errorf("expected primary action Bet/Raise for high fold-equity bluff, got %v", adviceB.PrimaryAction)
	}
}

func TestFlopCheckWhenFreeAndMediumEquity(t *testing.T) {
	heroCards := parseHeroCards(t, "8h 7h")
	boardCards := parseBoardCards(t, "8s Kd 2c")

	state := table.HandState{
		HandID:         "h-flop-check",
		Street:         table.StreetFlop,
		Pot:            10.0,
		CurrentBet:     0.0,
		CommunityCards: boardCards,
		HeroCards:      heroCards,
	}
	// 50% equity, opponent fold_to_cbet is low (10% - calling station)
	eq := equity.EquityResult{WinRate: 0.45, TieRate: 0.05, LoseRate: 0.50}
	oppTendencies := map[string]float64{"fold_to_cbet": 0.10}

	advice := CalculateAdvice(state, eq, oppTendencies)

	// In an unbet pot with low fold equity and medium strength, Check is preferred over Fold and negative/marginal bets
	if advice.PrimaryAction != table.ActionCheck && advice.PrimaryAction != table.ActionBet {
		t.Errorf("expected Check or Bet for medium hand in unbet pot, got %v", advice.PrimaryAction)
	}
	if advice.PrimaryAction == table.ActionFold {
		t.Errorf("never fold when checking is free")
	}
}

func TestTurnCheckCallWithDraw(t *testing.T) {
	// Hero has flush draw on turn (35% equity) facing a 1/3 pot bet (20 to call into pot of 80)
	// PotOdds = 20 / (80 + 20) = 20% < 35% Equity -> Profitable Call
	heroCards := parseHeroCards(t, "Ah 9h")
	boardCards := parseBoardCards(t, "Kh 7h 2c 4s")

	state := table.HandState{
		HandID:         "h-turn-call",
		Street:         table.StreetTurn,
		Pot:            80.0,
		CurrentBet:     20.0,
		MinRaise:       40.0,
		CommunityCards: boardCards,
		HeroCards:      heroCards,
	}
	eq := equity.EquityResult{WinRate: 0.35, TieRate: 0.0, LoseRate: 0.65}
	oppTendencies := map[string]float64{"fold_to_raise": 0.15} // Opponent doesn't fold to raise

	advice := CalculateAdvice(state, eq, oppTendencies)

	if advice.PrimaryAction != table.ActionCall {
		t.Errorf("expected primary action Call with 35%% equity > 20%% pot odds, got %v", advice.PrimaryAction)
	}
	if advice.RecommendedAmount != 20.0 {
		t.Errorf("expected recommended call amount 20.0, got %.2f", advice.RecommendedAmount)
	}
}

func TestRiverBluffCatching(t *testing.T) {
	heroCards := parseHeroCards(t, "As Jc")
	boardCards := parseBoardCards(t, "Ad 9s 4c 2h 7d")

	// Pot is 100, opponent bets 50 on river -> to_call = 50, pot = 100, PotOdds = 50 / 150 = 33.3%
	// Hero has top pair with 45% equity against opponent's river betting range (which includes 40% bluffs)
	state := table.HandState{
		HandID:         "h-river-bluff-catch",
		Street:         table.StreetRiver,
		Pot:            100.0,
		CurrentBet:     50.0,
		MinRaise:       100.0,
		CommunityCards: boardCards,
		HeroCards:      heroCards,
	}

	eq := equity.EquityResult{WinRate: 0.45, TieRate: 0.0, LoseRate: 0.55}
	oppTendencies := map[string]float64{
		"bluff_frequency": 0.40,
		"fold_to_raise":   0.05,
	}

	advice := CalculateAdvice(state, eq, oppTendencies)

	if advice.PrimaryAction != table.ActionCall {
		t.Errorf("expected primary action Call for river bluff catcher with 45%% equity > 33.3%% pot odds, got %v", advice.PrimaryAction)
	}
	if advice.RecommendedAmount != 50.0 {
		t.Errorf("expected call amount 50.0, got %.2f", advice.RecommendedAmount)
	}
}

func TestRiverBustedDrawFold(t *testing.T) {
	heroCards := parseHeroCards(t, "Jh Th")
	boardCards := parseBoardCards(t, "Ad 9s 4c 2h 7d")

	// Hero has busted draw (0% equity) facing a river bet of 75 into 100
	state := table.HandState{
		HandID:         "h-river-fold",
		Street:         table.StreetRiver,
		Pot:            100.0,
		CurrentBet:     75.0,
		MinRaise:       150.0,
		CommunityCards: boardCards,
		HeroCards:      heroCards,
	}

	eq := equity.EquityResult{WinRate: 0.0, TieRate: 0.0, LoseRate: 1.0}
	oppTendencies := map[string]float64{
		"fold_to_raise": 0.10,
	}

	advice := CalculateAdvice(state, eq, oppTendencies)

	if advice.PrimaryAction != table.ActionFold {
		t.Errorf("expected primary action Fold with 0%% equity, got %v", advice.PrimaryAction)
	}
	if advice.RecommendedAmount != 0.0 {
		t.Errorf("expected 0.0 amount for fold, got %.2f", advice.RecommendedAmount)
	}
}

func TestBetSizingOptionsGeneration(t *testing.T) {
	heroCards := parseHeroCards(t, "Ah As")
	boardCards := parseBoardCards(t, "Ac Kd 4s")

	state := table.HandState{
		HandID:         "h-sizings",
		Street:         table.StreetFlop,
		Pot:            100.0,
		CurrentBet:     0.0,
		MinRaise:       10.0,
		CommunityCards: boardCards,
		HeroCards:      heroCards,
		HeroID:         "hero",
		Seats: []table.SeatState{
			{PlayerID: "hero", Stack: 250.0, CurrentBet: 0.0, IsActive: true},
			{PlayerID: "villain", Stack: 300.0, CurrentBet: 0.0, IsActive: true},
		},
	}

	eq := equity.EquityResult{WinRate: 0.95, TieRate: 0.0, LoseRate: 0.05}
	oppTendencies := map[string]float64{"fold_to_cbet": 0.35}

	advice := CalculateAdvice(state, eq, oppTendencies)

	// Verify all expected action options exist
	labelMap := make(map[string]ActionRecommendation)
	for _, act := range advice.Actions {
		labelMap[act.SizingLabel] = act
	}

	expectedLabels := []string{"Fold", "Check", "33% Pot", "66% Pot", "Pot", "All-In"}
	for _, lbl := range expectedLabels {
		if _, ok := labelMap[lbl]; !ok {
			t.Errorf("expected sizing option %q in Actions, but was missing. Available: %v", lbl, getLabels(advice.Actions))
		}
	}

	// Verify primary recommendation is selected
	hasPrimary := false
	for _, act := range advice.Actions {
		if act.IsPrimary {
			hasPrimary = true
			if act.Action != advice.PrimaryAction {
				t.Errorf("primary action mismatch: %v vs %v", act.Action, advice.PrimaryAction)
			}
			if act.Amount != advice.RecommendedAmount {
				t.Errorf("recommended amount mismatch: %.2f vs %.2f", act.Amount, advice.RecommendedAmount)
			}
		}
	}
	if !hasPrimary {
		t.Errorf("expected exactly one primary action recommendation")
	}
}

func getLabels(actions []ActionRecommendation) []string {
	res := make([]string, len(actions))
	for i, a := range actions {
		res[i] = a.SizingLabel
	}
	return res
}

func TestCalculateAdviceWithHeroSeatCurrentBet(t *testing.T) {
	// Hero in BB posted 1.0. Villain in BTN opened to 3.0.
	// state.CurrentBet = 3.0, heroSeat.CurrentBet = 1.0 -> toCall = 2.0.
	// Pot = 4.5 (0.5 SB + 1.0 BB + 3.0 BTN).
	heroCards := parseHeroCards(t, "Ac Kc")
	state := table.HandState{
		HandID:     "h-bb-defend",
		Street:     table.StreetPreflop,
		Pot:        4.5,
		CurrentBet: 3.0,
		MinRaise:   5.0,
		HeroID:     "hero-1",
		HeroCards:  heroCards,
		Seats: []table.SeatState{
			{PlayerID: "hero-1", Stack: 99.0, CurrentBet: 1.0, IsActive: true, Position: table.PosBB},
			{PlayerID: "villain-1", Stack: 97.0, CurrentBet: 3.0, IsActive: true, Position: table.PosBTN},
		},
	}

	eq := equity.EquityResult{WinRate: 0.68, TieRate: 0.04, LoseRate: 0.28}
	oppTendencies := map[string]float64{"fold_to_3bet": 0.40}

	resp := CalculateAdvice(state, eq, oppTendencies)

	// Pot odds to call 2.0 into (4.5 + 2.0) = 2.0 / 6.5 = 30.77%
	expectedPO := 2.0 / (4.5 + 2.0)
	if !almostEqual(resp.PotOdds, expectedPO, 0.01) {
		t.Errorf("expected PotOdds %.4f, got %.4f", expectedPO, resp.PotOdds)
	}

	// Hero has 70% equity -> should 3-bet / raise
	if resp.PrimaryAction != table.ActionRaise {
		t.Errorf("expected primary action Raise, got %v", resp.PrimaryAction)
	}
	if resp.RecommendedAmount < 5.0 {
		t.Errorf("expected 3-bet amount >= min raise (5.0), got %.2f", resp.RecommendedAmount)
	}
}

func TestCalculateAdviceFacingBetSizingLabels(t *testing.T) {
	heroCards := parseHeroCards(t, "Kh Qh")
	state := table.HandState{
		HandID:     "h-raise-labels",
		Street:     table.StreetFlop,
		Pot:        20.0,
		CurrentBet: 10.0,
		MinRaise:   20.0,
		HeroCards:  heroCards,
	}

	eq := equity.EquityResult{WinRate: 0.55, TieRate: 0.0, LoseRate: 0.45}
	resp := CalculateAdvice(state, eq, map[string]float64{"fold_to_raise": 0.35})

	labelMap := make(map[string]bool)
	for _, a := range resp.Actions {
		labelMap[a.SizingLabel] = true
	}

	expected := []string{"Fold", "Call", "Min-Raise", "2.5x", "66% Pot", "Pot", "All-In"}
	for _, exp := range expected {
		if !labelMap[exp] {
			t.Errorf("expected action label %q when facing bet, got: %v", exp, getLabels(resp.Actions))
		}
	}
}

func TestCalculateAdviceEdgeCases(t *testing.T) {
	// Test with 0 pot, nil tendencies, empty state
	var zeroCards [2]table.Card
	state := table.HandState{
		HandID:     "h-edge",
		Street:     table.StreetPreflop,
		Pot:        0.0,
		CurrentBet: 0.0,
		HeroCards:  zeroCards,
	}

	eq := equity.EquityResult{WinRate: 0.50, TieRate: 0.0, LoseRate: 0.50}
	resp := CalculateAdvice(state, eq, nil)

	if resp.HandID != "h-edge" {
		t.Errorf("expected HandID 'h-edge', got %s", resp.HandID)
	}
	if resp.PrimaryAction != table.ActionCheck && resp.PrimaryAction != table.ActionBet && resp.PrimaryAction != table.ActionRaise {
		t.Errorf("unexpected primary action: %v", resp.PrimaryAction)
	}
}

func BenchmarkCalculateAdvice(b *testing.B) {
	heroCards := [2]table.Card{
		{Rank: table.RankAce, Suit: table.Hearts},
		{Rank: table.RankKing, Suit: table.Diamonds},
	}
	state := table.HandState{
		HandID:     "bench-h1",
		Street:     table.StreetFlop,
		Pot:        50.0,
		CurrentBet: 20.0,
		MinRaise:   40.0,
		HeroCards:  heroCards,
		HeroID:     "hero",
		Seats: []table.SeatState{
			{PlayerID: "hero", Stack: 200.0, CurrentBet: 0.0, IsActive: true},
			{PlayerID: "villain", Stack: 200.0, CurrentBet: 20.0, IsActive: true},
		},
	}
	eq := equity.EquityResult{WinRate: 0.65, TieRate: 0.02, LoseRate: 0.33}
	oppTendencies := map[string]float64{"fold_to_cbet": 0.40, "bluff_frequency": 0.25}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = CalculateAdvice(state, eq, oppTendencies)
	}
}
