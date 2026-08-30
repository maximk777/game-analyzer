package equity

import (
	"math/rand"
	"testing"

	"poker-game-analyzer/pkg/table"
)

func TestParseRange_Pairs(t *testing.T) {
	tests := []struct {
		input     string
		wantCount int
	}{
		{"AA", 6},
		{"KK", 6},
		{"QQ", 6},
		{"JJ", 6},
		{"TT", 6},
		{"99", 6},
		{"22", 6},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			r := ParseRange(tt.input)
			if len(r.Combos) != tt.wantCount {
				t.Errorf("ParseRange(%q) got %d combos, want %d", tt.input, len(r.Combos), tt.wantCount)
			}
			for _, c := range r.Combos {
				if c[0].Rank != c[1].Rank {
					t.Errorf("expected pair, got %s %s", c[0], c[1])
				}
				if c[0] == c[1] {
					t.Errorf("identical card in combo: %s %s", c[0], c[1])
				}
			}
		})
	}
}

func TestParseRange_PairPlus(t *testing.T) {
	tests := []struct {
		input     string
		wantCount int
	}{
		{"KK+", 12}, // KK, AA (2 pairs * 6 = 12)
		{"QQ+", 18}, // QQ, KK, AA (3 pairs * 6 = 18)
		{"JJ+", 24}, // JJ, QQ, KK, AA (4 pairs * 6 = 24)
		{"TT+", 30}, // TT, JJ, QQ, KK, AA (5 pairs * 6 = 30)
		{"22+", 78}, // All 13 pairs * 6 = 78
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			r := ParseRange(tt.input)
			if len(r.Combos) != tt.wantCount {
				t.Errorf("ParseRange(%q) got %d combos, want %d", tt.input, len(r.Combos), tt.wantCount)
			}
		})
	}
}

func TestParseRange_PairRanges(t *testing.T) {
	tests := []struct {
		input     string
		wantCount int
	}{
		{"88-TT", 18}, // 88, 99, TT (3 pairs * 6 = 18)
		{"22-66", 30}, // 22, 33, 44, 55, 66 (5 pairs * 6 = 30)
		{"TT-88", 18}, // Reversible
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			r := ParseRange(tt.input)
			if len(r.Combos) != tt.wantCount {
				t.Errorf("ParseRange(%q) got %d combos, want %d", tt.input, len(r.Combos), tt.wantCount)
			}
		})
	}
}

func TestParseRange_SuitedAndOffsuit(t *testing.T) {
	tests := []struct {
		input     string
		wantCount int
	}{
		{"AKs", 4},   // 4 suited combos
		{"AKo", 12},  // 12 offsuit combos
		{"AK", 16},   // 4 suited + 12 offsuit = 16
		{"AJs+", 12}, // AJs, AQs, AKs (3 * 4 = 12)
		{"AQo+", 24}, // AQo, AKo (2 * 12 = 24)
		{"AQ+", 32},  // AQ (16) + AK (16) = 32
		{"KQs+", 4},  // KQs (1 * 4 = 4)
		{"QJs+", 4},  // QJs
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			r := ParseRange(tt.input)
			if len(r.Combos) != tt.wantCount {
				t.Errorf("ParseRange(%q) got %d combos, want %d", tt.input, len(r.Combos), tt.wantCount)
			}
		})
	}
}

func TestParseRange_SpecificCards(t *testing.T) {
	tests := []struct {
		input     string
		wantCount int
	}{
		{"AhKd", 1},
		{"AsKs", 1},
		{"10sJh", 1},
		{"TsJh", 1},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			r := ParseRange(tt.input)
			if len(r.Combos) != tt.wantCount {
				t.Errorf("ParseRange(%q) got %d combos, want %d", tt.input, len(r.Combos), tt.wantCount)
			}
		})
	}
}

func TestParseRange_Presets(t *testing.T) {
	tests := []struct {
		input    string
		minCount int
		maxCount int
	}{
		{"random", 1326, 1326},
		{"any", 1326, 1326},
		{"100%", 1326, 1326},
		{"", 1326, 1326},
		{"top10", 110, 160},
		{"top10%", 110, 160},
		{"top25", 300, 360},
		{"top25%", 300, 360},
		{"top50", 600, 700},
		{"top50%", 600, 700},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			r := ParseRange(tt.input)
			if len(r.Combos) < tt.minCount || len(r.Combos) > tt.maxCount {
				t.Errorf("ParseRange(%q) got %d combos, want between [%d, %d]", tt.input, len(r.Combos), tt.minCount, tt.maxCount)
			}
		})
	}
}

func TestParseRange_MultipleEntriesAndDeduplication(t *testing.T) {
	// "AA, KK, QQ, AKs": 6 + 6 + 6 + 4 = 22 combos
	r1 := ParseRange("AA, KK, QQ, AKs")
	if len(r1.Combos) != 22 {
		t.Errorf("ParseRange('AA, KK, QQ, AKs') got %d combos, want 22", len(r1.Combos))
	}

	// "QQ+, AKs, AKo": QQ (6), KK (6), AA (6), AKs (4), AKo (12) = 34
	r2 := ParseRange("QQ+, AKs, AKo")
	if len(r2.Combos) != 34 {
		t.Errorf("ParseRange('QQ+, AKs, AKo') got %d combos, want 34", len(r2.Combos))
	}

	// Deduplication: "AA, AA, AA" -> 6 combos
	r3 := ParseRange("AA, AA, AA")
	if len(r3.Combos) != 6 {
		t.Errorf("ParseRange('AA, AA, AA') got %d combos, want 6", len(r3.Combos))
	}

	// Overlapping: "JJ+, QQ" -> JJ+ already contains QQ -> 24 combos
	r4 := ParseRange("JJ+, QQ")
	if len(r4.Combos) != 24 {
		t.Errorf("ParseRange('JJ+, QQ') got %d combos, want 24", len(r4.Combos))
	}
}

func TestRange_SampleCombo(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	r := ParseRange("KK") // 6 combos

	// 1. Sample with empty dead cards
	dead := make(map[string]bool)
	combo, ok := r.SampleCombo(dead, rng)
	if !ok {
		t.Fatalf("expected SampleCombo to succeed with empty dead cards")
	}
	if combo[0].Rank != table.RankKing || combo[1].Rank != table.RankKing {
		t.Fatalf("expected King pair, got %s %s", combo[0], combo[1])
	}

	// 2. Dead cards: 3 Kings are dead (Kh, Ks, Kd)
	// Remaining King: Kc -> No pair possible with only 1 King!
	dead["Kh"] = true
	dead["Ks"] = true
	dead["Kd"] = true
	_, ok = r.SampleCombo(dead, rng)
	if ok {
		t.Fatalf("expected SampleCombo to fail when 3 Kings are dead")
	}

	// 3. Dead cards: 2 Kings dead (Kh, Ks)
	// Remaining Kings: Kd, Kc -> Exactly 1 combo: [Kd, Kc]
	dead = map[string]bool{"Kh": true, "Ks": true}
	for i := 0; i < 20; i++ {
		combo, ok = r.SampleCombo(dead, rng)
		if !ok {
			t.Fatalf("expected SampleCombo to succeed with 2 live Kings")
		}
		if !((combo[0].String() == "Kd" && combo[1].String() == "Kc") || (combo[0].String() == "Kc" && combo[1].String() == "Kd")) {
			t.Fatalf("expected [Kd Kc], got %s %s", combo[0], combo[1])
		}
	}
}

// Range notation used by the preflop charts. The strict parser refuses what it
// does not understand rather than silently widening to the whole deck, so every
// form the charts rely on has to be covered here.
func TestParseRange_NotationUsedByCharts(t *testing.T) {
	cases := []struct {
		notation string
		combos   int
	}{
		{"AA", 6},
		{"AKs", 4},
		{"AKo", 12},
		{"77+", 48},     // 77 through AA
		{"ATs+", 16},    // ATs, AJs, AQs, AKs
		{"AA-JJ", 24},   // four pairs
		{"T9s-54s", 24}, // six connectors, same gap
		{"K2s-K8s", 28}, // seven kickers under a fixed high card
		{"A2o-A8o", 84}, // the same, offsuit
		{"22+, ATs+, KQo", 106},
	}

	for _, c := range cases {
		r, err := ParseRangeStrict(c.notation)
		if err != nil {
			t.Errorf("%q: %v", c.notation, err)
			continue
		}
		if len(r.Combos) != c.combos {
			t.Errorf("%q: got %d combos, want %d", c.notation, len(r.Combos), c.combos)
		}
	}
}

// The lenient parser answers an unknown range with every hand in the deck,
// which is a reasonable default for a guessed opponent range and a dangerous
// one for a chart: a typo would turn "raise these hands" into "raise anything".
func TestParseRangeStrict_RefusesNonsense(t *testing.T) {
	for _, bad := range []string{"ZZ", "K2s-Q8s", "hello", ""} {
		if _, err := ParseRangeStrict(bad); err == nil {
			t.Errorf("%q was accepted as a range", bad)
		}
	}
}
