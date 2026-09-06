package advice

import (
	"encoding/json"
	"hash/fnv"

	"poker-game-analyzer/pkg/table"
)

// seedFor derives the Monte Carlo seed from the table state itself.
//
// The seed used to come from the wall clock, which made the answer to a
// question depend on when it was asked. Live that is not a subtlety: the
// screen is read about twelve times a second, and on a table where nothing is
// happening every one of those frames drew a different sample, so the equity
// moved, the EV moved, and near a threshold the recommended action flipped
// between two answers several times a second. Nothing on screen had changed.
//
// It also defeated the check that stops the panel being redrawn when there is
// nothing new to say: that check compares the state and the advice, and the
// advice was never twice the same.
//
// Seeding from the state means the same table gets the same answer, and a
// different table gets a different sample. The state carries no clock of its
// own, so anything that moves the seed is something that moved on the felt.
func seedFor(h *table.HandState) int64 {
	sum := fnv.New64a()
	if data, err := json.Marshal(h); err == nil {
		_, _ = sum.Write(data)
	}
	return int64(sum.Sum64())
}
