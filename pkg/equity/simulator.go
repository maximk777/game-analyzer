package equity

import (
	"math/rand"
	"time"

	"poker-game-analyzer/pkg/evaluator"
	"poker-game-analyzer/pkg/table"
)

type EquityResult struct {
	WinRate      float64 `json:"win_rate"`
	TieRate      float64 `json:"tie_rate"`
	LoseRate     float64 `json:"lose_rate"`
	SamplesCount int     `json:"samples_count"`
	ElapsedMs    float64 `json:"elapsed_ms"`
}

func SimulateEquity(hero [2]table.Card, board []table.Card, opponentRanges []Range, iterations int) EquityResult {
	return SimulateEquityRNG(hero, board, opponentRanges, iterations, rand.New(rand.NewSource(time.Now().UnixNano())))
}

// SimulateEquityRNG is SimulateEquity with the randomness supplied.
//
// It exists because a harness that plays a hundred thousand hands has to be
// able to play the same hundred thousand hands again. Seeding inside the
// function made every equity call depend on the wall clock, so the same board
// could produce a call one run and a fold the next, and no change to the
// strategy could be told apart from that noise.
//
// The rng is not safe for concurrent use, so a caller running tables in
// parallel gives each one its own.
func SimulateEquityRNG(hero [2]table.Card, board []table.Card, opponentRanges []Range, iterations int, rng *rand.Rand) EquityResult {
	start := time.Now()
	if iterations <= 0 {
		iterations = 10000
	}

	numOpponents := len(opponentRanges)
	if numOpponents == 0 {
		numOpponents = 1
		opponentRanges = []Range{ParseRange("random")}
	}

	for i := range opponentRanges {
		if len(opponentRanges[i].masks) != len(opponentRanges[i].Combos) {
			opponentRanges[i].initMasks()
		}
	}

	heroMask := CardToMask(hero[0]) | CardToMask(hero[1])
	var boardMask uint64
	for _, b := range board {
		boardMask |= CardToMask(b)
	}
	baseDeadMask := heroMask | boardMask

	var baseRemainingDeck [52]uint8
	numRemaining := 0
	for idx := uint8(0); idx < 52; idx++ {
		if (baseDeadMask & (uint64(1) << idx)) == 0 {
			baseRemainingDeck[numRemaining] = idx
			numRemaining++
		}
	}

	boardNeeded := 5 - len(board)
	if boardNeeded < 0 {
		boardNeeded = 0
	}

	wins := 0
	ties := 0
	losses := 0
	validSamples := 0

	var oppCombos [10][2]table.Card
	var fullBoard [5]table.Card
	for i, b := range board {
		if i < 5 {
			fullBoard[i] = b
		}
	}

	var hero7 [7]table.Card
	hero7[0] = hero[0]
	hero7[1] = hero[1]

	var opp7 [7]table.Card

	for iter := 0; iter < iterations; iter++ {
		currentDeadMask := baseDeadMask
		valid := true

		for i := 0; i < numOpponents; i++ {
			combo, ok := opponentRanges[i].SampleComboMask(currentDeadMask, rng)
			if !ok {
				valid = false
				break
			}
			oppCombos[i] = combo
			currentDeadMask |= CardToMask(combo[0]) | CardToMask(combo[1])
		}

		if !valid {
			continue
		}

		if boardNeeded > 0 {
			drawn := 0
			boardOffset := len(board)
			for drawn < boardNeeded {
				rIdx := baseRemainingDeck[rng.Intn(numRemaining)]
				rMask := uint64(1) << rIdx
				if (currentDeadMask & rMask) == 0 {
					currentDeadMask |= rMask
					fullBoard[boardOffset+drawn] = IndexToCard(int(rIdx))
					drawn++
				}
			}
		}

		hero7[2] = fullBoard[0]
		hero7[3] = fullBoard[1]
		hero7[4] = fullBoard[2]
		hero7[5] = fullBoard[3]
		hero7[6] = fullBoard[4]

		heroScore, _ := evaluator.Evaluate7(hero7[:])

		heroWins := true
		heroTies := false

		for i := 0; i < numOpponents; i++ {
			opp7[0] = oppCombos[i][0]
			opp7[1] = oppCombos[i][1]
			opp7[2] = fullBoard[0]
			opp7[3] = fullBoard[1]
			opp7[4] = fullBoard[2]
			opp7[5] = fullBoard[3]
			opp7[6] = fullBoard[4]

			oppScore, _ := evaluator.Evaluate7(opp7[:])

			if oppScore > heroScore {
				heroWins = false
				break
			} else if oppScore == heroScore {
				heroTies = true
			}
		}

		validSamples++
		if heroWins && !heroTies {
			wins++
		} else if heroWins && heroTies {
			ties++
		} else {
			losses++
		}
	}

	if validSamples == 0 {
		return EquityResult{
			ElapsedMs: float64(time.Since(start).Microseconds()) / 1000.0,
		}
	}

	total := float64(validSamples)
	return EquityResult{
		WinRate:      float64(wins) / total,
		TieRate:      float64(ties) / total,
		LoseRate:     float64(losses) / total,
		SamplesCount: validSamples,
		ElapsedMs:    float64(time.Since(start).Microseconds()) / 1000.0,
	}
}
