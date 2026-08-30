package table

import "time"

// EventKind is what a recorded event says happened.
type EventKind string

const (
	// EventHandStart marks a hand beginning, with the seats as they were dealt.
	EventHandStart EventKind = "hand_start"
	// EventAction is one player acting: the whole of what a player tells you
	// about themselves, short of showing their cards.
	EventAction EventKind = "action"
	// EventReveal is a hand shown at showdown. Rare -- a handful in a session --
	// and worth more than anything else in the log, because frequencies say how
	// often someone bets and a showdown says with what.
	EventReveal EventKind = "reveal"
	// EventHandEnd closes a hand, with the pot and board it finished on.
	EventHandEnd EventKind = "hand_end"
)

// HandEvent is one observation about one player, written once and never
// changed.
//
// The log is append-only because that is what makes a statistic recomputable.
// Counters accumulated in memory can only ever answer the questions they were
// written to answer: this session went looking for fold-to-cbet and for the
// order of preflop raises, and neither could be had from the aggregates,
// because the events they would have been derived from were never kept.
//
// It records actions rather than frames. Frames are the input to this, not the
// content of it: at three a second a day of them is half a gigabyte and almost
// all of it repeats. Every event carries the time it happened, so how long a
// player took over a decision is the gap between their event and the one
// before -- a tell, for free, with no second stream to keep.
type HandEvent struct {
	// ID is assigned by the store and is the coordinate cursors move along.
	ID int64 `json:"id"`

	// SessionID is which run of the agent observed this.
	//
	// Not decoration. A recorded session in this repository turned out to hold
	// frames from an older build whose vision could not read action badges at
	// all, appended into the same file, and a measurement over the whole thing
	// was quietly wrong for an hour. Data has to say what read it.
	SessionID string `json:"session_id"`

	// TableKey identifies the table across sessions -- the client's own table
	// number, not the window title. The title carries the stake and the ante
	// and whatever text recognition made of them, so the same table has been
	// seen as "NLH 1229111 - 1K/2K (320)", "NLH 1229111- 1K/2K (320)" and
	// "@ NLH 1229111 - 1K/2K (320)". Keyed on that, one table becomes four and
	// everything counted about it is split between them.
	TableKey string `json:"table_key"`
	TableID  string `json:"table_id"`

	HandID string `json:"hand_id"`
	// Seq orders events within a hand and makes a re-emitted event a no-op
	// rather than a duplicate.
	Seq int `json:"seq"`

	At     time.Time `json:"at"`
	Kind   EventKind `json:"kind"`
	Street Street    `json:"street"`

	PlayerID   string     `json:"player_id"`
	PlayerName string     `json:"player_name,omitempty"`
	Position   Position   `json:"position,omitempty"`
	Action     ActionType `json:"action,omitempty"`

	// Amount, PotBefore and ToCall are exact. Money read off a table is a
	// fixed-point quantity and float64 only nearly holds one; at a big blind of
	// 0.1 that difference is the difference between a figure and a figure that
	// nearly matches what the player saw.
	Amount    Money `json:"amount,omitempty"`
	PotBefore Money `json:"pot_before,omitempty"`
	ToCall    Money `json:"to_call,omitempty"`

	// Cards is what was shown, on a reveal.
	Cards []Card `json:"cards,omitempty"`
	// Board as it stood when this happened.
	Board []Card `json:"board,omitempty"`
}

// TableKeyOf reduces a table title to the client's own table number, which is
// the only part of it that identifies the table rather than describing it.
//
//	"NLH 1229111 - 1K/2K (320)"   -> "1229111"
//	"@ NLH 1229111- 1K/2K (320)"  -> "1229111"
//	"coinpoker-live"              -> "coinpoker-live"
//
// The longest run of digits is taken: the stake and the ante are digits too,
// but a table number is longer than either, and the alternative -- matching the
// title's exact shape -- is what text recognition keeps breaking.
func TableKeyOf(tableID string) string {
	best, cur := "", ""
	for _, r := range tableID {
		if r >= '0' && r <= '9' {
			cur += string(r)
			if len(cur) > len(best) {
				best = cur
			}
			continue
		}
		cur = ""
	}
	// Four digits is where a table number starts and a stake stops. Below that
	// there is nothing to key on and the title is used whole, which at least
	// does not merge two tables into one.
	if len(best) >= 4 {
		return best
	}
	return tableID
}
