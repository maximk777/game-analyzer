package audit

import (
	"strings"
	"testing"

	"poker-game-analyzer/pkg/table"
)

func seats(n int, live bool) []table.SeatState {
	out := make([]table.SeatState, n)
	for i := range out {
		out[i] = table.SeatState{
			SeatNumber: i + 1,
			PlayerID:   string(rune('a' + i)),
			IsActive:   live,
			IsFolded:   false,
		}
	}
	return out
}

func TestUnreadableAcceptsATableThatFits(t *testing.T) {
	state := &table.HandState{HeroID: "a", Seats: seats(6, true)}
	if reason := Unreadable(state); reason != "" {
		t.Errorf("six-max should read fine: %q", reason)
	}
}

// Six-max is the only layout this reads, so eleven seats is a misread frame
// rather than a hard hand.
func TestUnreadableRefusesMoreSeatsThanTheTableHolds(t *testing.T) {
	state := &table.HandState{HeroID: "a", Seats: seats(11, true)}
	reason := Unreadable(state)
	if !strings.Contains(reason, "11 seats") {
		t.Fatalf("reason %q", reason)
	}
	if !strings.Contains(reason, "holds 6") {
		t.Errorf("the reason should say what the table holds: %q", reason)
	}
}

// The count that reached the simulator is live opponents, so that is the count
// checked, not just the row count.
func TestUnreadableRefusesTooManyLiveOpponents(t *testing.T) {
	state := &table.HandState{HeroID: "hero", Seats: seats(6, true)}
	for i := range state.Seats {
		state.Seats[i].PlayerID = "opp" + string(rune('a'+i))
	}
	if reason := Unreadable(state); !strings.Contains(reason, "live opponents") {
		t.Fatalf("six opponents besides hero do not fit six-max: %q", reason)
	}
}

func TestUnreadableOnNothing(t *testing.T) {
	if Unreadable(nil) == "" {
		t.Error("there is nothing to advise on")
	}
}

// A folded or empty seat is not an opponent, so it must not count against the
// table.
func TestUnreadableIgnoresFoldedAndEmptySeats(t *testing.T) {
	state := &table.HandState{HeroID: "hero", Seats: seats(6, true)}
	for i := range state.Seats {
		state.Seats[i].PlayerID = "opp" + string(rune('a'+i))
	}
	state.Seats[0].IsFolded = true
	state.Seats[1].PlayerID = ""
	if reason := Unreadable(state); reason != "" {
		t.Errorf("four live opponents fit six-max: %q", reason)
	}
}
