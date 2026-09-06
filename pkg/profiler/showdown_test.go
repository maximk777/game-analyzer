package profiler

import (
	"testing"

	"poker-game-analyzer/pkg/storage"
	"poker-game-analyzer/pkg/table"
)

// A hand that ends at showdown must count: who reached it, who won, and whose
// cards we saw. Before this the profiler read only the action history and a
// showdown left no trace.
func TestProcessHandEnd_CountsShowdown(t *testing.T) {
	prof := NewProfiler(storage.NewMemoryCache(), nil, nil)
	defer prof.Close()

	ac, _ := table.ParseCard("Ac")
	kd, _ := table.ParseCard("Kd")

	hand := table.HandState{
		HandID: "h1", TableID: "t", Street: table.StreetShowdown, Pot: 1000,
		Seats: []table.SeatState{
			// Reached showdown, won, and showed.
			{PlayerID: "winner", PlayerName: "winner", IsActive: true, WonHand: true,
				Cards: []table.Card{ac, kd}},
			// Reached showdown, lost, mucked (no cards).
			{PlayerID: "loser", PlayerName: "loser", IsActive: true},
			// Folded earlier: did not reach showdown.
			{PlayerID: "folder", PlayerName: "folder", IsActive: true, IsFolded: true},
		},
	}
	prof.ProcessHandEnd(hand)

	w := prof.GetStats("winner")
	if w == nil || w.WTSDN != 1 || w.WTSD != 1 {
		t.Fatalf("winner WTSD = %+v, want reached 1/1", w)
	}
	if w.WonAtSD != 1 || w.ShownHands != 1 {
		t.Errorf("winner won/shown = %.2f/%d, want 1.0/1", w.WonAtSD, w.ShownHands)
	}
	l := prof.GetStats("loser")
	if l == nil || l.WTSD != 1 || l.WonAtSD != 0 || l.ShownHands != 0 {
		t.Errorf("loser = %+v, want reached, not won, not shown", l)
	}
	f := prof.GetStats("folder")
	if f == nil || f.WTSD != 0 {
		t.Errorf("folder WTSD = %v, want 0 (folded before showdown)", f)
	}
}
