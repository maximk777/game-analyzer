package sfs

import (
	"os"
	"testing"

	"github.com/google/gopacket"
	"poker-game-analyzer/pkg/table"
)

// replayFixture feeds a captured pcap through one roster and returns it. The
// fixtures are real CoinPoker traffic; a single roster mixes the connection's
// tables, which is enough to prove the projection end to end.
func replayFixture(t *testing.T, path string) *Roster {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Skipf("fixture %s not present: %v", path, err)
	}
	defer f.Close()

	r := NewRoster()
	err = Read(f, func(flow gopacket.Flow) StreamConsumer {
		return NewScanner(flow, func(_ gopacket.Flow, _ Direction, m Message) {
			r.Apply(m)
		})
	})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestBridgeBlindsFromHeroCapture(t *testing.T) {
	r := replayFixture(t, "../../testdata/sfs/session_hero.pcap")

	// The hand-start message set the Big Ben stake of 1000/2000 with a 320 ante.
	if got := r.SmallBlind.String(); got != "1000" {
		t.Errorf("small blind = %s, want 1000", got)
	}
	if got := r.BigBlind.String(); got != "2000" {
		t.Errorf("big blind = %s, want 2000", got)
	}
	if got := r.Ante.String(); got != "320" {
		t.Errorf("ante = %s, want 320", got)
	}
	hs := r.HandState()
	if hs == nil {
		t.Fatal("no hand state built")
	}
	if hs.BigBlind != 2000 {
		t.Errorf("hand state big blind = %v, want 2000", hs.BigBlind)
	}
	// The table id is the hand id without its five-digit hand counter.
	if hs.TableID != "1280514" {
		t.Errorf("table id = %q, want 1280514", hs.TableID)
	}
	// Positions: the blinds and button seats named by hand-start must be mapped.
	var haveSB, haveBB, haveBTN bool
	for _, s := range hs.Seats {
		switch s.Position {
		case table.PosSB:
			haveSB = true
		case table.PosBB:
			haveBB = true
		case table.PosBTN:
			haveBTN = true
		}
	}
	if !haveSB || !haveBB || !haveBTN {
		t.Errorf("positions incomplete: SB=%v BB=%v BTN=%v", haveSB, haveBB, haveBTN)
	}
}

func TestBridgeShowdownCaptureReconstructs(t *testing.T) {
	r := replayFixture(t, "../../testdata/sfs/session_showdown.pcap")

	hs := r.HandState()
	if hs == nil {
		t.Fatal("no hand state built")
	}
	// This capture ran to a five-card board.
	if len(hs.CommunityCards) != 5 {
		t.Errorf("board = %v, want 5 cards", hs.CommunityCards)
	}
	// Both players' shown hands were captured as seat cards.
	shown := 0
	for _, s := range hs.Seats {
		if len(s.Cards) == 2 {
			shown++
		}
	}
	if shown < 2 {
		t.Errorf("shown hands = %d, want >= 2", shown)
	}
}

func TestBridgeHeroByConfiguredName(t *testing.T) {
	r := replayFixture(t, "../../testdata/sfs/session_showdown.pcap")
	// Hero is configured by nick; the seat carrying that name is the hero.
	r.HeroName = "Blffd"
	seat := r.HeroSeat()
	if seat == nil {
		t.Fatal("hero seat not resolved by name")
	}
	if seat.UserID != 1793809 {
		t.Errorf("hero userId = %d, want 1793809", seat.UserID)
	}
	hs := r.HandState()
	if hs.HeroID != "1793809" {
		t.Errorf("hand state hero id = %q, want 1793809", hs.HeroID)
	}
}
