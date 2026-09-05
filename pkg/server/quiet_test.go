package server

import (
	"testing"

	"poker-game-analyzer/pkg/advisor"
	"poker-game-analyzer/pkg/table"
)

func state(pot float64) *table.HandState {
	return &table.HandState{
		TableID: "t1", HandID: "h1", Pot: pot,
		Seats: []table.SeatState{{SeatNumber: 1, PlayerID: "a", Stack: 5, IsActive: true}},
	}
}

// Vision delivers a frame every few dozen milliseconds and most of them say
// exactly what the one before said. Broadcasting each is what makes the panel
// redraw constantly.
func TestAlreadySentSuppressesARepeatedFrame(t *testing.T) {
	s := &Server{}
	if s.alreadySent("t1", state(1.34), nil, "") {
		t.Fatal("the first frame is news")
	}
	for range 20 {
		if !s.alreadySent("t1", state(1.34), nil, "") {
			t.Fatal("the same frame was treated as news")
		}
	}
}

func TestAlreadySentLetsARealChangeThrough(t *testing.T) {
	s := &Server{}
	s.alreadySent("t1", state(1.34), nil, "")
	if s.alreadySent("t1", state(2.68), nil, "") {
		t.Error("a changed pot is news")
	}
	if s.alreadySent("t1", state(2.68), &advisor.AdvisorResponse{}, "") {
		t.Error("advice appearing is news")
	}
	if s.alreadySent("t1", state(2.68), nil, "state not readable") {
		t.Error("losing advice is news")
	}
}

// Two tables do not silence each other.
func TestAlreadySentIsPerTable(t *testing.T) {
	s := &Server{}
	s.alreadySent("t1", state(1.34), nil, "")
	if s.alreadySent("t2", state(1.34), nil, "") {
		t.Error("another table has heard nothing yet")
	}
}

// Advice going away has to reach the panel, or it holds the previous hand's
// recommendation with nothing to say it expired.
func TestAlreadySentNoticesAdviceExpiring(t *testing.T) {
	s := &Server{}
	rec := &advisor.AdvisorResponse{}
	s.alreadySent("t1", state(1.34), rec, "")
	if s.alreadySent("t1", state(1.34), nil, "Раздача закончена") {
		t.Error("advice expiring is news")
	}
}
