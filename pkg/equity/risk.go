package equity

import (
	"sort"

	"poker-game-analyzer/pkg/evaluator"
	"poker-game-analyzer/pkg/table"
)

// CategoryShare is one kind of hand an opponent could be holding, and how much
// of their range holds it.
type CategoryShare struct {
	// Category names the made hand, e.g. "Full House".
	Category string `json:"category"`
	// Share is the fraction of the opponent's range, 0..1.
	Share float64 `json:"share"`
	// Combos is how many two-card holdings that is, which is what makes the
	// share checkable rather than something to be taken on trust.
	Combos int `json:"combos"`
}

// RiskProfile is what an opponent's range holds against hero on the board as it
// stands: how much of it is already winning, and with what.
type RiskProfile struct {
	// Behind is the fraction of the range that beats hero right now.
	Behind float64 `json:"behind"`
	// Tied is the fraction that would split.
	Tied float64 `json:"tied"`
	// BeatenBy lists what is beating hero, largest share first.
	BeatenBy []CategoryShare `json:"beaten_by"`
	// HeroHand is hero's own made hand, so the two can be read together.
	HeroHand string `json:"hero_hand"`
	// Combos is the size of the range once hero's cards and the board are
	// removed from it.
	Combos int `json:"combos"`
}

// Risk enumerates an opponent's range against the board as it stands and
// reports what is already beating hero.
//
// Equity answers "how often do I win", and it answers it well -- but it answers
// it as one number, and one number cannot distinguish a hand that is 88% because
// the opponent usually has nothing from a hand that is 88% because the 12% is
// sitting in exactly the holdings that will raise. Kings on 9-9-7-5-Q are 88%
// against everything and drawing dead against the queens; nothing in a single
// equity figure says so.
//
// This is an exact count, not a sample: every combination in the range is
// played against the board. There is no runout here on purpose -- the question
// is what beats hero now, which is the question a player is asking when they
// look at a paired board and wonder whether to put more money in.
func Risk(hero [2]table.Card, board []table.Card, r Range) RiskProfile {
	out := RiskProfile{}
	if len(board) < 3 {
		return out
	}

	dead := CardToMask(hero[0]) | CardToMask(hero[1])
	for _, b := range board {
		dead |= CardToMask(b)
	}

	heroAll := make([]table.Card, 0, len(board)+2)
	heroAll = append(heroAll, hero[0], hero[1])
	heroAll = append(heroAll, board...)
	_, heroCat := evaluator.Evaluate7(heroAll)
	out.HeroHand = heroCat.String()

	villAll := make([]table.Card, 0, len(board)+2)
	byCategory := map[string]int{}
	total, behind, tied := 0, 0, 0

	for _, combo := range r.Combos {
		if dead&(CardToMask(combo[0])|CardToMask(combo[1])) != 0 {
			continue
		}
		total++

		villAll = villAll[:0]
		villAll = append(villAll, combo[0], combo[1])
		villAll = append(villAll, board...)

		switch evaluator.CompareHands(villAll, heroAll) {
		case 1:
			behind++
			_, cat := evaluator.Evaluate7(villAll)
			byCategory[cat.String()]++
		case 0:
			tied++
		}
	}

	if total == 0 {
		return out
	}
	out.Combos = total
	out.Behind = float64(behind) / float64(total)
	out.Tied = float64(tied) / float64(total)

	for cat, n := range byCategory {
		out.BeatenBy = append(out.BeatenBy, CategoryShare{
			Category: cat,
			Share:    float64(n) / float64(total),
			Combos:   n,
		})
	}
	sort.Slice(out.BeatenBy, func(i, j int) bool {
		if out.BeatenBy[i].Combos != out.BeatenBy[j].Combos {
			return out.BeatenBy[i].Combos > out.BeatenBy[j].Combos
		}
		return out.BeatenBy[i].Category < out.BeatenBy[j].Category
	})
	return out
}
