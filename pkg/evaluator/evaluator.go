package evaluator

import (
	"poker-game-analyzer/pkg/table"
)

type HandCategory int

const (
	HighCard HandCategory = iota + 1
	OnePair
	TwoPair
	ThreeOfAKind
	Straight
	Flush
	FullHouse
	FourOfAKind
	StraightFlush
)

func (c HandCategory) String() string {
	switch c {
	case HighCard:
		return "High Card"
	case OnePair:
		return "One Pair"
	case TwoPair:
		return "Two Pair"
	case ThreeOfAKind:
		return "Three of a Kind"
	case Straight:
		return "Straight"
	case Flush:
		return "Flush"
	case FullHouse:
		return "Full House"
	case FourOfAKind:
		return "Four of a Kind"
	case StraightFlush:
		return "Straight Flush"
	default:
		return "Unknown"
	}
}

type HandScore uint32

func checkStraight(mask uint16) (bool, table.Rank) {
	for r := 14; r >= 6; r-- {
		straightMask := uint16(0x1F) << (r - 4)
		if (mask & straightMask) == straightMask {
			return true, table.Rank(r)
		}
	}
	const wheelMask = (1 << 14) | (1 << 2) | (1 << 3) | (1 << 4) | (1 << 5)
	if (mask & wheelMask) == wheelMask {
		return true, table.Rank(5)
	}
	return false, 0
}

// evaluateSlice evaluates 5, 6, 7 (or more) cards directly using bitwise logic.
func evaluateSlice(cards []table.Card) (HandScore, HandCategory) {
	n := len(cards)
	if n < 5 {
		return 0, HighCard
	}

	var suitCounts [4]uint8
	var suitMasks [4]uint16
	var rankCounts [15]uint8
	var rankMask uint16

	for i := 0; i < n; i++ {
		c := cards[i]
		suitCounts[c.Suit]++
		suitMasks[c.Suit] |= 1 << c.Rank
		rankCounts[c.Rank]++
		rankMask |= 1 << c.Rank
	}

	// 1. Check Flush / Straight Flush
	var flushSuit int = -1
	for s := 0; s < 4; s++ {
		if suitCounts[s] >= 5 {
			flushSuit = s
			break
		}
	}

	if flushSuit >= 0 {
		fMask := suitMasks[flushSuit]
		isSF, sfHigh := checkStraight(fMask)
		if isSF {
			score := uint32(StraightFlush)<<24 | uint32(sfHigh)<<16
			return HandScore(score), StraightFlush
		}

		// Flush: select top 5 ranks of this suit
		var score uint32 = uint32(Flush) << 24
		shift := 16
		count := 0
		for r := 14; r >= 2; r-- {
			if (fMask & (1 << r)) != 0 {
				score |= uint32(r) << shift
				shift -= 4
				count++
				if count == 5 {
					break
				}
			}
		}
		return HandScore(score), Flush
	}

	// 2. Classify rank frequencies
	var fourRank, three1, three2 table.Rank
	var pair1, pair2, pair3 table.Rank
	for r := 14; r >= 2; r-- {
		switch rankCounts[r] {
		case 4:
			fourRank = table.Rank(r)
		case 3:
			if three1 == 0 {
				three1 = table.Rank(r)
			} else if three2 == 0 {
				three2 = table.Rank(r)
			}
		case 2:
			if pair1 == 0 {
				pair1 = table.Rank(r)
			} else if pair2 == 0 {
				pair2 = table.Rank(r)
			} else if pair3 == 0 {
				pair3 = table.Rank(r)
			}
		}
	}

	// 3. Four of a Kind
	if fourRank > 0 {
		var kicker uint32
		for r := 14; r >= 2; r-- {
			if table.Rank(r) != fourRank && rankCounts[r] > 0 {
				kicker = uint32(r)
				break
			}
		}
		score := uint32(FourOfAKind)<<24 | uint32(fourRank)<<16 | kicker<<8
		return HandScore(score), FourOfAKind
	}

	// 4. Full House
	if three1 > 0 {
		if three2 > 0 {
			score := uint32(FullHouse)<<24 | uint32(three1)<<16 | uint32(three2)<<8
			return HandScore(score), FullHouse
		}
		if pair1 > 0 {
			score := uint32(FullHouse)<<24 | uint32(three1)<<16 | uint32(pair1)<<8
			return HandScore(score), FullHouse
		}
	}

	// 5. Straight
	isStraight, straightHigh := checkStraight(rankMask)
	if isStraight {
		score := uint32(Straight)<<24 | uint32(straightHigh)<<16
		return HandScore(score), Straight
	}

	// 6. Three of a Kind
	if three1 > 0 {
		var score uint32 = uint32(ThreeOfAKind)<<24 | uint32(three1)<<16
		shift := 12
		kCount := 0
		for r := 14; r >= 2; r-- {
			if table.Rank(r) != three1 && rankCounts[r] > 0 {
				score |= uint32(r) << shift
				shift -= 4
				kCount++
				if kCount == 2 {
					break
				}
			}
		}
		return HandScore(score), ThreeOfAKind
	}

	// 7. Two Pair
	if pair1 > 0 && pair2 > 0 {
		var kicker uint32
		for r := 14; r >= 2; r-- {
			if table.Rank(r) != pair1 && table.Rank(r) != pair2 && rankCounts[r] > 0 {
				kicker = uint32(r)
				break
			}
		}
		score := uint32(TwoPair)<<24 | uint32(pair1)<<16 | uint32(pair2)<<8 | kicker<<4
		return HandScore(score), TwoPair
	}

	// 8. One Pair
	if pair1 > 0 {
		var score uint32 = uint32(OnePair)<<24 | uint32(pair1)<<16
		shift := 12
		kCount := 0
		for r := 14; r >= 2; r-- {
			if table.Rank(r) != pair1 && rankCounts[r] > 0 {
				score |= uint32(r) << shift
				shift -= 4
				kCount++
				if kCount == 3 {
					break
				}
			}
		}
		return HandScore(score), OnePair
	}

	// 9. High Card
	var score uint32 = uint32(HighCard) << 24
	shift := 16
	kCount := 0
	for r := 14; r >= 2; r-- {
		if rankCounts[r] > 0 {
			score |= uint32(r) << shift
			shift -= 4
			kCount++
			if kCount == 5 {
				break
			}
		}
	}
	return HandScore(score), HighCard
}

// Evaluate5 evaluates the rank and score of a 5-card hand.
func Evaluate5(cards [5]table.Card) (HandScore, HandCategory) {
	return evaluateSlice(cards[:])
}

// Evaluate7 evaluates the best 5-card hand out of 5, 6, 7 (or more) cards.
func Evaluate7(cards []table.Card) (HandScore, HandCategory) {
	return evaluateSlice(cards)
}

// CompareHands compares two hands (5 to 7 cards each).
// Returns -1 if handA < handB, 0 if handA == handB, 1 if handA > handB.
func CompareHands(handA, handB []table.Card) int {
	scoreA, _ := Evaluate7(handA)
	scoreB, _ := Evaluate7(handB)
	if scoreA < scoreB {
		return -1
	}
	if scoreA > scoreB {
		return 1
	}
	return 0
}
