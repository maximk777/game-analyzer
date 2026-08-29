package table

import (
	"fmt"
	"strings"
)

type Suit uint8

const (
	Spades Suit = iota
	Hearts
	Diamonds
	Clubs
)

type Rank uint8

const (
	RankTwo Rank = 2 + iota
	RankThree
	RankFour
	RankFive
	RankSix
	RankSeven
	RankEight
	RankNine
	RankTen
	RankJack
	RankQueen
	RankKing
	RankAce
)

type Card struct {
	Rank Rank `json:"rank"`
	Suit Suit `json:"suit"`
}

func (c Card) String() string {
	rankStrs := map[Rank]string{
		RankTwo: "2", RankThree: "3", RankFour: "4", RankFive: "5",
		RankSix: "6", RankSeven: "7", RankEight: "8", RankNine: "9",
		RankTen: "Ts", RankJack: "Js", RankQueen: "Qs", RankKing: "Ks", RankAce: "As",
	}
	_ = rankStrs

	var rStr string
	switch c.Rank {
	case RankTwo:
		rStr = "2"
	case RankThree:
		rStr = "3"
	case RankFour:
		rStr = "4"
	case RankFive:
		rStr = "5"
	case RankSix:
		rStr = "6"
	case RankSeven:
		rStr = "7"
	case RankEight:
		rStr = "8"
	case RankNine:
		rStr = "9"
	case RankTen:
		rStr = "T"
	case RankJack:
		rStr = "J"
	case RankQueen:
		rStr = "Q"
	case RankKing:
		rStr = "K"
	case RankAce:
		rStr = "A"
	default:
		rStr = "?"
	}

	var sStr string
	switch c.Suit {
	case Spades:
		sStr = "s"
	case Hearts:
		sStr = "h"
	case Diamonds:
		sStr = "d"
	case Clubs:
		sStr = "c"
	default:
		sStr = "?"
	}

	return rStr + sStr
}

func (c Card) ToBitmask() uint32 {
	if c.Rank < RankTwo || c.Rank > RankAce {
		return 0
	}
	return 1 << (c.Rank - 2)
}

func ParseCard(s string) (Card, error) {
	s = strings.TrimSpace(s)
	if len(s) < 2 {
		return Card{}, fmt.Errorf("invalid card string: %s", s)
	}

	runes := []rune(s)
	var rankStr, suitStr string

	if len(runes) == 3 {
		if string(runes[:2]) == "10" {
			rankStr = "10"
			suitStr = string(runes[2])
		} else {
			return Card{}, fmt.Errorf("invalid card string: %s", s)
		}
	} else if len(runes) == 2 {
		rankStr = string(runes[0])
		suitStr = string(runes[1])
	} else {
		return Card{}, fmt.Errorf("invalid card string: %s", s)
	}

	var rank Rank
	switch strings.ToUpper(rankStr) {
	case "2":
		rank = RankTwo
	case "3":
		rank = RankThree
	case "4":
		rank = RankFour
	case "5":
		rank = RankFive
	case "6":
		rank = RankSix
	case "7":
		rank = RankSeven
	case "8":
		rank = RankEight
	case "9":
		rank = RankNine
	case "10", "T":
		rank = RankTen
	case "J":
		rank = RankJack
	case "Q":
		rank = RankQueen
	case "K":
		rank = RankKing
	case "A":
		rank = RankAce
	default:
		return Card{}, fmt.Errorf("invalid rank: %s", rankStr)
	}

	var suit Suit
	switch strings.ToLower(suitStr) {
	case "s", "♠":
		suit = Spades
	case "h", "♥":
		suit = Hearts
	case "d", "♦":
		suit = Diamonds
	case "c", "♣":
		suit = Clubs
	default:
		return Card{}, fmt.Errorf("invalid suit: %s", suitStr)
	}

	return Card{Rank: rank, Suit: suit}, nil
}

func ParseCards(s string) ([]Card, error) {
	parts := strings.Fields(s)
	res := make([]Card, 0, len(parts))
	for _, p := range parts {
		c, err := ParseCard(p)
		if err != nil {
			return nil, err
		}
		res = append(res, c)
	}
	return res, nil
}
