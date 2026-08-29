package table

import (
	"testing"
)

func TestParseCard(t *testing.T) {
	tests := []struct {
		input    string
		expected Card
		wantErr  bool
	}{
		{"Ah", Card{Rank: RankAce, Suit: Hearts}, false},
		{"Kd", Card{Rank: RankKing, Suit: Diamonds}, false},
		{"10s", Card{Rank: RankTen, Suit: Spades}, false},
		{"Ts", Card{Rank: RankTen, Suit: Spades}, false},
		{"2c", Card{Rank: RankTwo, Suit: Clubs}, false},
		{"9h", Card{Rank: RankNine, Suit: Hearts}, false},
		{"Jc", Card{Rank: RankJack, Suit: Clubs}, false},
		{"Qd", Card{Rank: RankQueen, Suit: Diamonds}, false},
		{"8s", Card{Rank: RankEight, Suit: Spades}, false},
		{"7h", Card{Rank: RankSeven, Suit: Hearts}, false},
		{"6d", Card{Rank: RankSix, Suit: Diamonds}, false},
		{"5c", Card{Rank: RankFive, Suit: Clubs}, false},
		{"4s", Card{Rank: RankFour, Suit: Spades}, false},
		{"3h", Card{Rank: RankThree, Suit: Hearts}, false},
		// Unicode suits
		{"A♠", Card{Rank: RankAce, Suit: Spades}, false},
		{"K♥", Card{Rank: RankKing, Suit: Hearts}, false},
		{"Q♦", Card{Rank: RankQueen, Suit: Diamonds}, false},
		{"J♣", Card{Rank: RankJack, Suit: Clubs}, false},
		// Invalid cases
		{"Xx", Card{}, true},
		{"", Card{}, true},
		{"A", Card{}, true},
		{"10", Card{}, true},
		{"100s", Card{}, true},
		{"1s", Card{}, true},
		{"14h", Card{}, true},
		{"Kx", Card{}, true},
		{"Ahd", Card{}, true},
	}

	for _, tt := range tests {
		c, err := ParseCard(tt.input)
		if (err != nil) != tt.wantErr {
			t.Errorf("ParseCard(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
			continue
		}
		if !tt.wantErr && c != tt.expected {
			t.Errorf("ParseCard(%q) = %v, want %v", tt.input, c, tt.expected)
		}
	}
}

func TestCardString(t *testing.T) {
	tests := []struct {
		card     Card
		expected string
	}{
		{Card{Rank: RankAce, Suit: Hearts}, "Ah"},
		{Card{Rank: RankKing, Suit: Diamonds}, "Kd"},
		{Card{Rank: RankQueen, Suit: Clubs}, "Qc"},
		{Card{Rank: RankJack, Suit: Spades}, "Js"},
		{Card{Rank: RankTen, Suit: Spades}, "Ts"},
		{Card{Rank: RankNine, Suit: Hearts}, "9h"},
		{Card{Rank: RankTwo, Suit: Clubs}, "2c"},
	}

	for _, tt := range tests {
		if got := tt.card.String(); got != tt.expected {
			t.Errorf("Card.String() = %q, want %q", got, tt.expected)
		}
	}
}

func TestParseCards(t *testing.T) {
	tests := []struct {
		input     string
		wantCount int
		wantErr   bool
	}{
		{"Ah Kd 10s", 3, false},
		{"2c 7d", 2, false},
		{"  Ah   Kd  ", 2, false},
		{"", 0, false},
		{"Ah invalid Kd", 0, true},
	}

	for _, tt := range tests {
		cards, err := ParseCards(tt.input)
		if (err != nil) != tt.wantErr {
			t.Errorf("ParseCards(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
			continue
		}
		if !tt.wantErr && len(cards) != tt.wantCount {
			t.Errorf("ParseCards(%q) count = %d, want %d", tt.input, len(cards), tt.wantCount)
		}
	}
}
