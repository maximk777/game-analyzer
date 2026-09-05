package table

import "testing"

func numberedSeats(names ...string) []SeatState {
	out := make([]SeatState, len(names))
	for i, n := range names {
		out[i] = SeatState{
			SeatNumber: i + 1,
			PlayerID:   n,
			PlayerName: n,
			IsActive:   true,
			Stack:      10,
		}
	}
	return out
}

// The nameplate is read by OCR: the same player arrives under a different
// mangling on every other frame. Keyed on the name, each misreading became a
// new chair, and since an unseen seat is carried forward, six players grew
// into twelve and never shrank.
func TestMergeSeatsKeepsOneChairPerSeatNumber(t *testing.T) {
	prev := numberedSeats("ruddy16923342", "mamayazareyzil", "Pororoka", "kazzam", "yoasis", "HightLvL")
	raw := numberedSeats("P:m:mRm$A$:1", "mmo:", "Pororoka", "kazzam", "yoasis", "HightLvL")

	got := mergeSeats(prev, raw)
	if len(got) != 6 {
		names := make([]string, len(got))
		for i, s := range got {
			names[i] = s.PlayerID
		}
		t.Fatalf("%d seats after a garbled frame, want 6: %v", len(got), names)
	}
	seen := map[int]bool{}
	for _, s := range got {
		if seen[s.SeatNumber] {
			t.Errorf("seat %d appears twice", s.SeatNumber)
		}
		seen[s.SeatNumber] = true
	}
}

// Repeated garbling must not grow the table over time either.
func TestMergeSeatsDoesNotGrowOverFrames(t *testing.T) {
	state := numberedSeats("a", "b", "c", "d", "e", "f")
	for i := range 20 {
		raw := numberedSeats(
			"garble"+string(rune('a'+i)), "b", "c", "d", "e", "f")
		state = mergeSeats(state, raw)
	}
	if len(state) != 6 {
		t.Fatalf("%d seats after twenty garbled frames, want 6", len(state))
	}
}

// A player whose nameplate the recogniser missed for one frame still must not
// vanish: the live opponent count drives the equity simulation.
func TestMergeSeatsCarriesForwardAMissingSeat(t *testing.T) {
	prev := numberedSeats("a", "b", "c")
	raw := prev[:2]

	got := mergeSeats(prev, raw)
	if len(got) != 3 {
		t.Fatalf("%d seats, want the missing player kept", len(got))
	}
}

// States that do not number their seats keep the old behaviour, because there
// a name is all there is.
func TestMergeSeatsFallsBackToNamesWhenNothingIsNumbered(t *testing.T) {
	prev := []SeatState{{PlayerID: "alice", Stack: 10, IsActive: true}}
	raw := []SeatState{{PlayerID: "alice", IsActive: true}}

	got := mergeSeats(prev, raw)
	if len(got) != 1 {
		t.Fatalf("%d seats, want one", len(got))
	}
	if got[0].Stack != 10 {
		t.Errorf("the stack was not carried forward: %+v", got[0])
	}
}

// What the merge exists to preserve still has to survive keying by number.
func TestMergeSeatsStillPreservesWhatItShould(t *testing.T) {
	prev := numberedSeats("a", "b")
	prev[0].IsFolded = true
	prev[0].LastAction = "fold"
	prev[1].Cards = []Card{{}, {}}

	raw := numberedSeats("a", "b")
	raw[0].Stack = 0

	got := mergeSeats(prev, raw)
	if !got[0].IsFolded {
		t.Error("folding is not undone within a hand")
	}
	if got[0].LastAction != "fold" {
		t.Error("a badge that flickers out must not lose the action")
	}
	if got[0].Stack != 10 {
		t.Error("a stack that failed to read must not become zero")
	}
	if len(got[1].Cards) != 2 {
		t.Error("cards stay revealed once shown")
	}
}

// These are the readings the panel actually showed for one table: a full
// nameplate, a truncation to the first glyph, and a spray of punctuation.
func TestMergeSeatsKeepsTheBestReadingOfANameplate(t *testing.T) {
	prev := numberedSeats("ruddy16923342", "Mule1827", "J4zzy")
	raw := numberedSeats("A", "$:$$CmA$A::", "J")

	got := mergeSeats(prev, raw)
	for i, want := range []string{"ruddy16923342", "Mule1827", "J4zzy"} {
		if got[i].PlayerID != want {
			t.Errorf("seat %d reads %q, want %q", i+1, got[i].PlayerID, want)
		}
	}
}

// A first reading caught while the table was still drawing must not lock the
// seat for the rest of the hand.
func TestMergeSeatsImprovesOnATruncatedFirstReading(t *testing.T) {
	state := numberedSeats("A")
	state = mergeSeats(state, numberedSeats("ruddy16923342"))
	if state[0].PlayerID != "ruddy16923342" {
		t.Fatalf("a fuller reading should win: %q", state[0].PlayerID)
	}
	// And once it is held, noise does not take it back.
	state = mergeSeats(state, numberedSeats("$:$$"))
	if state[0].PlayerID != "ruddy16923342" {
		t.Fatalf("noise took the name back: %q", state[0].PlayerID)
	}
}

func TestBetterName(t *testing.T) {
	cases := []struct{ held, fresh, want string }{
		{"ruddy16923342", "A", "ruddy16923342"},
		{"A", "ruddy16923342", "ruddy16923342"},
		{"Mule1827", "$:$$CmA$A::", "Mule1827"},
		{"player-3", "J4zzy", "J4zzy"},
		{"J4zzy", "player-3", "J4zzy"},
		{"J4zzy", "", "J4zzy"},
		{"TomSkyWalker", "TomSkyWalker", "TomSkyWalker"},
	}
	for _, c := range cases {
		if got := betterName(c.held, c.fresh); got != c.want {
			t.Errorf("betterName(%q, %q) = %q, want %q", c.held, c.fresh, got, c.want)
		}
	}
}

// The first real reading has to land, or a seat stays a placeholder forever.
func TestMergeSeatsTakesTheFirstRealName(t *testing.T) {
	prev := []SeatState{{SeatNumber: 1, PlayerID: "player-1", PlayerName: "Player 1", IsActive: true}}
	raw := []SeatState{{SeatNumber: 1, PlayerID: "Pororoka", PlayerName: "Pororoka", IsActive: true}}

	got := mergeSeats(prev, raw)
	if got[0].PlayerID != "Pororoka" || got[0].PlayerName != "Pororoka" {
		t.Fatalf("a placeholder should yield to a real name: %+v", got[0])
	}
}

// A nameplate that fails to read must not undo the one that did.
func TestMergeSeatsSurvivesANameplateThatWentBlank(t *testing.T) {
	prev := numberedSeats("Pororoka")
	raw := []SeatState{{SeatNumber: 1, IsActive: true}}

	got := mergeSeats(prev, raw)
	if got[0].PlayerID != "Pororoka" {
		t.Fatalf("the name was lost: %+v", got[0])
	}
}

// Seat order is the order the panel draws them in, so it has to be the same on
// every frame. A chair the recogniser missed used to be appended at the end
// and jump back into place on the next frame.
func TestMergeSeatsKeepsAStableOrder(t *testing.T) {
	full := numberedSeats("a", "b", "c", "d")
	missing := []SeatState{full[0], full[2], full[3]} // seat 2 not read this frame

	got := mergeSeats(full, missing)
	if len(got) != 4 {
		t.Fatalf("%d seats, want four", len(got))
	}
	for i, s := range got {
		if s.SeatNumber != i+1 {
			nums := make([]int, len(got))
			for j, x := range got {
				nums[j] = x.SeatNumber
			}
			t.Fatalf("seats came back as %v, want them in order", nums)
		}
	}
}
