package evaluator

import (
	"testing"

	"poker-game-analyzer/pkg/table"
)

func TestEvaluate5Ranks(t *testing.T) {
	tests := []struct {
		name         string
		cards        string
		wantCategory HandCategory
	}{
		{
			name:         "Royal Flush",
			cards:        "Ah Kh Qh Jh 10h",
			wantCategory: StraightFlush,
		},
		{
			name:         "Steel Wheel Straight Flush",
			cards:        "5h 4h 3h 2h Ah",
			wantCategory: StraightFlush,
		},
		{
			name:         "Four of a Kind",
			cards:        "Ac Ad Ah As Kd",
			wantCategory: FourOfAKind,
		},
		{
			name:         "Full House",
			cards:        "Ac Ad Ah Kc Kd",
			wantCategory: FullHouse,
		},
		{
			name:         "Flush",
			cards:        "Ac 2c 5c 8c Jc",
			wantCategory: Flush,
		},
		{
			name:         "Straight Broadway",
			cards:        "10c Jd Qh Ks Ac",
			wantCategory: Straight,
		},
		{
			name:         "Straight Wheel",
			cards:        "Ac 2d 3h 4s 5c",
			wantCategory: Straight,
		},
		{
			name:         "Three of a Kind",
			cards:        "Qc Qd Qs 2h 5d",
			wantCategory: ThreeOfAKind,
		},
		{
			name:         "Two Pair",
			cards:        "Jc Jd 10s 10d 2c",
			wantCategory: TwoPair,
		},
		{
			name:         "One Pair",
			cards:        "Ac Ad 2c 5h 8d",
			wantCategory: OnePair,
		},
		{
			name:         "High Card",
			cards:        "Ac Kd Qs Jh 9c",
			wantCategory: HighCard,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cards, err := table.ParseCards(tt.cards)
			if err != nil {
				t.Fatalf("ParseCards(%q) failed: %v", tt.cards, err)
			}
			if len(cards) != 5 {
				t.Fatalf("expected 5 cards, got %d", len(cards))
			}
			var five [5]table.Card
			copy(five[:], cards)

			score, cat := Evaluate5(five)
			if cat != tt.wantCategory {
				t.Errorf("Evaluate5 category = %v, want %v (score: %d)", cat, tt.wantCategory, score)
			}
		})
	}
}

func TestEvaluate7Ranks(t *testing.T) {
	tests := []struct {
		name         string
		cards        string
		wantCategory HandCategory
	}{
		{
			name:         "Royal Flush",
			cards:        "Ah Kh Qh Jh 10h 2c 3d",
			wantCategory: StraightFlush,
		},
		{
			name:         "Steel Wheel Straight Flush in 7 cards",
			cards:        "5c 4c 3c 2c Ac Kd Qh",
			wantCategory: StraightFlush,
		},
		{
			name:         "Straight Flush with 6 flush cards selects highest straight flush",
			cards:        "9c 8c 7c 6c 5c 4c Kd",
			wantCategory: StraightFlush,
		},
		{
			name:         "7 Flush cards selects top 5",
			cards:        "Ac Kc Qc Jc 9c 8c 2c",
			wantCategory: Flush,
		},
		{
			name:         "Straight with three of a kind evaluates to Straight",
			cards:        "9c 9d 9h 8s 7c 6d 5h",
			wantCategory: Straight,
		},
		{
			name:         "Full House with straight cards evaluates to Full House",
			cards:        "9c 9d 9h 8s 8c 7d 6h",
			wantCategory: FullHouse,
		},
		{
			name:         "Four of a Kind with straight cards evaluates to Four of a Kind",
			cards:        "9c 9d 9h 9s 8c 7d 6h",
			wantCategory: FourOfAKind,
		},
		{
			name:         "Four of a Kind",
			cards:        "Ac Ad Ah As Kd 2c 3d",
			wantCategory: FourOfAKind,
		},
		{
			name:         "Full House",
			cards:        "Ac Ad Ah Kc Kd 2c 3d",
			wantCategory: FullHouse,
		},
		{
			name:         "Full House with two 3-of-a-kinds selects higher trips",
			cards:        "Ac Ad Ah Kc Kd Ks 2c",
			wantCategory: FullHouse,
		},
		{
			name:         "Flush selecting best 5",
			cards:        "Ac 2c 5c 8c Jc Kd Qd",
			wantCategory: Flush,
		},
		{
			name:         "Straight 6-card run selects top 5",
			cards:        "9c 8d 7h 6s 5c 4d Kh",
			wantCategory: Straight,
		},
		{
			name:         "Straight Wheel in 7 cards",
			cards:        "Ac 2d 3h 4s 5c 9d Kh",
			wantCategory: Straight,
		},
		{
			name:         "Three of a Kind",
			cards:        "Qc Qd Qs 2h 5d 8c 9s",
			wantCategory: ThreeOfAKind,
		},
		{
			name:         "Two Pair with 3 pairs selects top 2",
			cards:        "Jc Jd 10s 10d 8c 8h 2c",
			wantCategory: TwoPair,
		},
		{
			name:         "One Pair",
			cards:        "Ac Ad 2c 5h 8d 9s Jc",
			wantCategory: OnePair,
		},
		{
			name:         "High Card",
			cards:        "Ac Kd Qs Jh 9c 4d 2s",
			wantCategory: HighCard,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cards, err := table.ParseCards(tt.cards)
			if err != nil {
				t.Fatalf("ParseCards failed: %v", err)
			}
			score, cat := Evaluate7(cards)
			if cat != tt.wantCategory {
				t.Errorf("Evaluate7 category = %v, want %v (score: %d)", cat, tt.wantCategory, score)
			}
		})
	}
}

func TestEvaluate6Cards(t *testing.T) {
	cards, err := table.ParseCards("Ac Ad Ah 2c 3d 4s")
	if err != nil {
		t.Fatalf("ParseCards failed: %v", err)
	}
	score, cat := Evaluate7(cards)
	if cat != ThreeOfAKind {
		t.Errorf("Evaluate7(6 cards) category = %v, want %v (score: %d)", cat, ThreeOfAKind, score)
	}
}

func TestCompareHands(t *testing.T) {
	tests := []struct {
		name     string
		handA    string
		handB    string
		expected int
	}{
		{
			name:     "Four of a Kind: same quads, kicker decides",
			handA:    "Ac Ad Ah As Kd 2c 3d",
			handB:    "Ac Ad Ah As Qd 2c 3d",
			expected: 1,
		},
		{
			name:     "Straight Flush beats Four of a Kind",
			handA:    "Ah Kh Qh Jh 10h 2c 3d",
			handB:    "Ac Ad Ah As Kd 2c 3d",
			expected: 1,
		},
		{
			name:     "Four of a Kind loses to Straight Flush",
			handA:    "Ac Ad Ah As Kd 2c 3d",
			handB:    "Ah Kh Qh Jh 10h 2c 3d",
			expected: -1,
		},
		{
			name:     "Full House: AAA KK beats AAA QQ",
			handA:    "Ac Ad Ah Kc Kd 2c 3d",
			handB:    "Ac Ad Ah Qc Qd 2c 3d",
			expected: 1,
		},
		{
			name:     "Full House: AAA 22 beats KKK AA",
			handA:    "Ac Ad Ah 2c 2d 4c 5d",
			handB:    "Kc Kd Kh Ac Ad 4c 5d",
			expected: 1,
		},
		{
			name:     "Flush: higher top card wins",
			handA:    "Ac Kc 9c 5c 2c 3d 4d",
			handB:    "Qc Jc 9c 8c 2c 3d 4d",
			expected: 1,
		},
		{
			name:     "Flush: same top 4 cards, 5th kicker decides",
			handA:    "Ac Kc Qc Jc 9c 2d 3d",
			handB:    "Ac Kc Qc Jc 8c 2d 3d",
			expected: 1,
		},
		{
			name:     "Straight: 6-high beats 5-high wheel",
			handA:    "6c 5d 4h 3s 2c 8d Kh",
			handB:    "5c 4d 3h 2s Ac 8d Kh",
			expected: 1,
		},
		{
			name:     "Two Pair: same pairs, kicker decides",
			handA:    "Ac Ad Kc Kd Qc 2s 3d",
			handB:    "Ac Ad Kc Kd Jc 2s 3d",
			expected: 1,
		},
		{
			name:     "One Pair: same pair, third kicker decides",
			handA:    "Ac Ad Kc Qc 9c 2s 3d",
			handB:    "Ac Ad Kc Qc 8c 2s 3d",
			expected: 1,
		},
		{
			name:     "Split Pot: identical 5 cards on board",
			handA:    "Ah Kd Qs Jh 9c 2c 3d",
			handB:    "Ah Kd Qs Jh 9c 4s 5c",
			expected: 0,
		},
		{
			name:     "Split Pot: same straight",
			handA:    "5c 6d 7h 8s 9c 2c 3d",
			handB:    "5c 6d 7h 8s 9c 2h 4h",
			expected: 0,
		},
		{
			name:     "Split Pot: Two Pair with same pairs and same kicker",
			handA:    "Ah Ad Kd Kc 9h 2c 3d",
			handB:    "As Ac Kh Ks 9c 4s 5d",
			expected: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cardsA, errA := table.ParseCards(tt.handA)
			cardsB, errB := table.ParseCards(tt.handB)
			if errA != nil || errB != nil {
				t.Fatalf("ParseCards failed: %v, %v", errA, errB)
			}
			res := CompareHands(cardsA, cardsB)
			if res != tt.expected {
				scoreA, catA := Evaluate7(cardsA)
				scoreB, catB := Evaluate7(cardsB)
				t.Errorf("CompareHands() = %d, want %d (A: %v %d, B: %v %d)", res, tt.expected, catA, scoreA, catB, scoreB)
			}
		})
	}
}

func TestHandCategoryString(t *testing.T) {
	tests := []struct {
		cat      HandCategory
		expected string
	}{
		{HighCard, "High Card"},
		{OnePair, "One Pair"},
		{TwoPair, "Two Pair"},
		{ThreeOfAKind, "Three of a Kind"},
		{Straight, "Straight"},
		{Flush, "Flush"},
		{FullHouse, "Full House"},
		{FourOfAKind, "Four of a Kind"},
		{StraightFlush, "Straight Flush"},
		{HandCategory(999), "Unknown"},
	}

	for _, tt := range tests {
		if got := tt.cat.String(); got != tt.expected {
			t.Errorf("HandCategory(%d).String() = %q, want %q", tt.cat, got, tt.expected)
		}
	}
}

func BenchmarkEvaluate7(b *testing.B) {
	cards, _ := table.ParseCards("Ah Kh Qh Jh 10h 2c 3d")
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		Evaluate7(cards)
	}
}

func BenchmarkEvaluate5(b *testing.B) {
	cards, _ := table.ParseCards("Ah Kh Qh Jh 10h")
	var five [5]table.Card
	copy(five[:], cards)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		Evaluate5(five)
	}
}

func BenchmarkCompareHands(b *testing.B) {
	handA, _ := table.ParseCards("Ac Ad Kc Kd Qc 2s 3d")
	handB, _ := table.ParseCards("Ac Ad Kc Kd Jc 2s 3d")
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		CompareHands(handA, handB)
	}
}
