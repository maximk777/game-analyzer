package sfs

import (
	"os"
	"testing"

	"poker-game-analyzer/pkg/table"
)

func TestLiveEmitsStatesFromFixture(t *testing.T) {
	f, err := os.Open("../../testdata/sfs/session_showdown.pcap")
	if err != nil {
		t.Skipf("fixture missing: %v", err)
	}
	defer f.Close()

	var states int
	var lastBoard []table.Card
	live := &Live{
		HeroName: "Blffd",
		OnState: func(hs *table.HandState) {
			states++
			if len(hs.CommunityCards) > 0 {
				lastBoard = hs.CommunityCards
			}
		},
	}
	if err := live.Run(f, nil); err != nil {
		t.Fatal(err)
	}
	if states == 0 {
		t.Fatal("no states emitted")
	}
	if len(lastBoard) != 5 {
		t.Errorf("final board = %v, want 5 cards", lastBoard)
	}
	// The hero configured by name resolved to a seat with cards by showdown.
	if seat := live.Roster().HeroSeat(); seat == nil || seat.UserID != 1793809 {
		t.Errorf("hero seat = %+v, want Blffd 1793809", seat)
	}
}
