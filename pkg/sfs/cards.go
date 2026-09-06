package sfs

import "poker-game-analyzer/pkg/table"

// WireCard is a card as the game server names it: suit and value spelled out in
// full ("CLUBS", "ACE"). The dealer-chat lines abbreviate ("14C"), but every
// structured card message -- the board, the hole cards, the made hands -- uses
// this form, so it is the one we decode.
type WireCard struct {
	Suit  string `json:"suit"`
	Value string `json:"value"`
}

// wireSuits maps the server's suit names to table suits.
var wireSuits = map[string]table.Suit{
	"SPADES":   table.Spades,
	"HEARTS":   table.Hearts,
	"DIAMONDS": table.Diamonds,
	"CLUBS":    table.Clubs,
}

// wireRanks maps the server's value names to table ranks.
var wireRanks = map[string]table.Rank{
	"TWO":   table.RankTwo,
	"THREE": table.RankThree,
	"FOUR":  table.RankFour,
	"FIVE":  table.RankFive,
	"SIX":   table.RankSix,
	"SEVEN": table.RankSeven,
	"EIGHT": table.RankEight,
	"NINE":  table.RankNine,
	"TEN":   table.RankTen,
	"JACK":  table.RankJack,
	"QUEEN": table.RankQueen,
	"KING":  table.RankKing,
	"ACE":   table.RankAce,
}

// Card converts a wire card to a table card. The zero table.Card ("not read")
// is returned for anything unrecognised, so a malformed card is absent rather
// than wrong -- the same contract the frame reader keeps.
func (w WireCard) Card() table.Card {
	r, okR := wireRanks[w.Value]
	s, okS := wireSuits[w.Suit]
	if !okR || !okS {
		return table.Card{}
	}
	return table.Card{Rank: r, Suit: s}
}

// sameCards reports whether two card slices are equal in order.
func sameCards(a, b []table.Card) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// wireCards converts a slice of wire cards, dropping any that do not decode.
func wireCards(ws []WireCard) []table.Card {
	out := make([]table.Card, 0, len(ws))
	for _, w := range ws {
		if c := w.Card(); c.Known() {
			out = append(out, c)
		}
	}
	return out
}
