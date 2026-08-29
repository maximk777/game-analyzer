package equity

import (
	"testing"

	"poker-game-analyzer/pkg/table"
)

func TestSimulateEquityPreflopAA_vs_KK(t *testing.T) {
	hero, err := table.ParseCards("Ah As")
	if err != nil {
		t.Fatalf("ParseCards failed: %v", err)
	}
	var heroCards [2]table.Card
	copy(heroCards[:], hero)

	villainRange := ParseRange("KK")
	res := SimulateEquity(heroCards, nil, []Range{villainRange}, 10000)

	// Theoretical: AA vs KK preflop is ~81.7% win, ~0.5% tie, ~17.8% loss
	if res.WinRate < 0.78 || res.WinRate > 0.85 {
		t.Errorf("AA vs KK WinRate expected ~81.7%%, got %.2f%%", res.WinRate*100)
	}
	if res.LoseRate < 0.14 || res.LoseRate > 0.22 {
		t.Errorf("AA vs KK LoseRate expected ~17.8%%, got %.2f%%", res.LoseRate*100)
	}
	if res.SamplesCount != 10000 {
		t.Errorf("expected 10000 samples, got %d", res.SamplesCount)
	}
}

func TestSimulateEquityPreflopAKs_vs_QQ(t *testing.T) {
	hero, _ := table.ParseCards("Ah Kh")
	var heroCards [2]table.Card
	copy(heroCards[:], hero)

	villainRange := ParseRange("QQ")
	res := SimulateEquity(heroCards, nil, []Range{villainRange}, 10000)

	// Theoretical: AKs vs QQ is ~46% win for AKs
	if res.WinRate < 0.42 || res.WinRate > 0.50 {
		t.Errorf("AKs vs QQ WinRate expected ~46%%, got %.2f%%", res.WinRate*100)
	}
}

func TestSimulateEquityPreflopAKo_vs_22(t *testing.T) {
	hero, _ := table.ParseCards("Ah Kd")
	var heroCards [2]table.Card
	copy(heroCards[:], hero)

	villainRange := ParseRange("22")
	res := SimulateEquity(heroCards, nil, []Range{villainRange}, 10000)

	// Theoretical coin flip: AKo vs 22 is ~47-53%
	if res.WinRate < 0.45 || res.WinRate > 0.55 {
		t.Errorf("AKo vs 22 WinRate expected ~50%%, got %.2f%%", res.WinRate*100)
	}
}

func TestSimulateEquityFlopFlushDraw(t *testing.T) {
	hero, _ := table.ParseCards("Ah Kh")
	var heroCards [2]table.Card
	copy(heroCards[:], hero)

	// Flop: Qh Jh 2c -> Hero has Nut Flush Draw + Straight Gutshot (12 outs) + 2 overcards
	board, _ := table.ParseCards("Qh Jh 2c")
	villainRange := ParseRange("top25")

	res := SimulateEquity(heroCards, board, []Range{villainRange}, 5000)

	// Ah Kh against top25 on Qh Jh 2c has ~65-72% equity
	if res.WinRate < 0.60 || res.WinRate > 0.75 {
		t.Errorf("Flush draw on flop vs top25 WinRate expected ~65-72%%, got %.2f%%", res.WinRate*100)
	}
}

func TestSimulateEquityFlopFlushDrawVsTwoPair(t *testing.T) {
	hero, _ := table.ParseCards("Ah Kh")
	var heroCards [2]table.Card
	copy(heroCards[:], hero)

	// Flop: Qh Jh 2c -> Hero has Nut Flush Draw + Straight Gutshot (12 clean outs)
	// Villain has made top two pair (Qd Jd)
	board, _ := table.ParseCards("Qh Jh 2c")
	villainRange := ParseRange("QdJd")

	res := SimulateEquity(heroCards, board, []Range{villainRange}, 5000)

	// 12 outs on flop with 2 cards to come is ~45% equity
	if res.WinRate < 0.40 || res.WinRate > 0.50 {
		t.Errorf("Flush draw vs Made Two Pair WinRate expected ~45%%, got %.2f%%", res.WinRate*100)
	}
}

func TestSimulateEquityFlopSetVsOverpair(t *testing.T) {
	hero, _ := table.ParseCards("2h 2d")
	var heroCards [2]table.Card
	copy(heroCards[:], hero)

	// Flop: 2s 7c 9d -> Hero has bottom set (222)
	board, _ := table.ParseCards("2s 7c 9d")
	// Villain has AA
	villainRange := ParseRange("AA")

	res := SimulateEquity(heroCards, board, []Range{villainRange}, 5000)

	// Hero with set against AA is huge favorite (~91% win)
	if res.WinRate < 0.85 {
		t.Errorf("Set vs AA on flop WinRate expected > 85%%, got %.2f%%", res.WinRate*100)
	}
}

func TestSimulateEquityTurnMadeFlushVsSet(t *testing.T) {
	hero, _ := table.ParseCards("Ah Kh")
	var heroCards [2]table.Card
	copy(heroCards[:], hero)

	// Board: Qh Jh 2h 8s -> Hero has nut flush on turn
	board, _ := table.ParseCards("Qh Jh 2h 8s")
	// Villain has set of 8s (8h 8c / 8d 8c, etc.) -> 10 outs on river for full house/quads (~22% win)
	villainRange := ParseRange("88")

	res := SimulateEquity(heroCards, board, []Range{villainRange}, 5000)

	// Hero should win ~75-80%
	if res.WinRate < 0.70 || res.WinRate > 0.85 {
		t.Errorf("Turn Made Flush vs Set WinRate expected ~77%%, got %.2f%%", res.WinRate*100)
	}
}

func TestSimulateEquityMultiOpponent(t *testing.T) {
	hero, _ := table.ParseCards("Ah As")
	var heroCards [2]table.Card
	copy(heroCards[:], hero)

	// AA equity vs random opponents decreases monotonically as player count rises
	res1 := SimulateEquity(heroCards, nil, []Range{ParseRange("random")}, 3000)
	res2 := SimulateEquity(heroCards, nil, []Range{ParseRange("random"), ParseRange("random")}, 3000)
	res3 := SimulateEquity(heroCards, nil, []Range{ParseRange("random"), ParseRange("random"), ParseRange("random")}, 3000)
	res5 := SimulateEquity(heroCards, nil, []Range{
		ParseRange("random"), ParseRange("random"), ParseRange("random"),
		ParseRange("random"), ParseRange("random"),
	}, 3000)

	if res1.WinRate <= res2.WinRate {
		t.Errorf("1 opp WinRate (%.2f) should be > 2 opps (%.2f)", res1.WinRate, res2.WinRate)
	}
	if res2.WinRate <= res3.WinRate {
		t.Errorf("2 opps WinRate (%.2f) should be > 3 opps (%.2f)", res2.WinRate, res3.WinRate)
	}
	if res3.WinRate <= res5.WinRate {
		t.Errorf("3 opps WinRate (%.2f) should be > 5 opps (%.2f)", res3.WinRate, res5.WinRate)
	}
}

func TestSimulateEquityDeadCardsTracking(t *testing.T) {
	hero, _ := table.ParseCards("Ah As")
	var heroCards [2]table.Card
	copy(heroCards[:], hero)

	// Board contains Ks
	board, _ := table.ParseCards("Ks 2c 3d")
	// Villain has KK range
	villainRange := ParseRange("KK")

	res := SimulateEquity(heroCards, board, []Range{villainRange}, 3000)
	if res.SamplesCount == 0 {
		t.Fatalf("expected non-zero samples")
	}
}

func TestSimulateEquityEdgeCases(t *testing.T) {
	hero, _ := table.ParseCards("Ah Kd")
	var heroCards [2]table.Card
	copy(heroCards[:], hero)

	// Iterations <= 0 should default to 10000
	res := SimulateEquity(heroCards, nil, nil, 0)
	if res.SamplesCount != 10000 {
		t.Errorf("expected default 10000 iterations, got %d", res.SamplesCount)
	}

	// Full river board (5 cards)
	riverBoard, _ := table.ParseCards("2c 7d Jh 10s 4c")
	resRiver := SimulateEquity(heroCards, riverBoard, []Range{ParseRange("random")}, 1000)
	if resRiver.SamplesCount != 1000 {
		t.Errorf("expected 1000 samples on river, got %d", resRiver.SamplesCount)
	}
}

func BenchmarkSimulateEquityPreflop5k(b *testing.B) {
	hero, _ := table.ParseCards("Ah As")
	var heroCards [2]table.Card
	copy(heroCards[:], hero)
	villainRange := ParseRange("KK")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		SimulateEquity(heroCards, nil, []Range{villainRange}, 5000)
	}
}

func BenchmarkSimulateEquityFlop5k(b *testing.B) {
	hero, _ := table.ParseCards("Ah Kh")
	var heroCards [2]table.Card
	copy(heroCards[:], hero)
	board, _ := table.ParseCards("Qh Jh 2c")
	villainRange := ParseRange("top25")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		SimulateEquity(heroCards, board, []Range{villainRange}, 5000)
	}
}

func BenchmarkSimulateEquityMultiOpponent5k(b *testing.B) {
	hero, _ := table.ParseCards("Ah As")
	var heroCards [2]table.Card
	copy(heroCards[:], hero)
	opps := []Range{ParseRange("top10"), ParseRange("top25"), ParseRange("random")}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		SimulateEquity(heroCards, nil, opps, 5000)
	}
}

func BenchmarkSimulateEquity10k(b *testing.B) {
	hero, _ := table.ParseCards("Ah Kh")
	var heroCards [2]table.Card
	copy(heroCards[:], hero)
	board, _ := table.ParseCards("Qh Jh 2c")
	villainRange := ParseRange("top25")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		SimulateEquity(heroCards, board, []Range{villainRange}, 10000)
	}
}
