package table

import (
	"fmt"
	"testing"
)

func TestStateStabilizer_GlitchFiltering(t *testing.T) {
	st := NewStateStabilizer()

	c1, _ := ParseCard("10s")
	c2, _ := ParseCard("Ks")
	c3, _ := ParseCard("3s")

	// 1. Initial Flop Frame with 3 cards & 10,600 pot
	f1 := &HandState{
		TableID:        "coinpoker-live",
		Pot:            10600,
		CommunityCards: []Card{c1, c2, c3},
		Seats: []SeatState{
			{PlayerID: "AdamSD84", PlayerName: "AdamSD84", Stack: 195000},
			{PlayerID: "CoolioTDW", PlayerName: "CoolioTDW", Stack: 187000},
		},
	}
	s1 := st.Stabilize(f1)
	if len(s1.CommunityCards) != 3 || s1.Pot != 10600 {
		t.Fatalf("expected 3 cards and 10600 pot on flop, got %d cards, %.2f pot", len(s1.CommunityCards), s1.Pot)
	}

	// 2. Glitchy intermediate frame where 1 card was blurry / missed (only 2 cards in raw)
	f2 := &HandState{
		TableID:        "coinpoker-live",
		Pot:            0, // Pot missed due to chip animation
		CommunityCards: []Card{c1, c2},
		Seats:          []SeatState{},
	}
	s2 := st.Stabilize(f2)
	// Must keep 3 cards and 10600 pot!
	if len(s2.CommunityCards) != 3 {
		t.Errorf("glitch frame caused board card drop! Expected 3 cards, got %d", len(s2.CommunityCards))
	}
	if s2.Pot != 10600 {
		t.Errorf("glitch frame reset pot to 0! Expected 10600, got %.2f", s2.Pot)
	}
	if len(s2.Seats) != 2 {
		t.Errorf("glitch frame dropped seats! Expected 2 seats, got %d", len(s2.Seats))
	}

	// 3. Turn card dealt (8d) with bet increasing pot to 21,200
	c4, _ := ParseCard("8d")
	f3 := &HandState{
		TableID:        "coinpoker-live",
		Pot:            21200,
		CommunityCards: []Card{c1, c2, c3, c4},
		Seats: []SeatState{
			{PlayerID: "AdamSD84", PlayerName: "AdamSD84", Stack: 185000},
			{PlayerID: "CoolioTDW", PlayerName: "CoolioTDW", Stack: 177000},
		},
	}
	// The capture runs at several frames a second and repeats each state, so a
	// real pot increase always arrives more than once. A new maximum is adopted
	// only on the second sighting, which is what stops one misread frame -- live,
	// 401,920 into a 4,920 pot -- from sticking for the rest of the session.
	st.Stabilize(f3)
	s3 := st.Stabilize(f3)
	if len(s3.CommunityCards) != 4 || s3.Pot != 21200 || s3.Street != StreetTurn {
		t.Errorf("turn update failed: cards=%d, pot=%.2f, street=%s", len(s3.CommunityCards), s3.Pot, s3.Street)
	}

	// 4. New hand starts: board empty & pot resets to blinds (1,600)
	f4 := &HandState{
		TableID:        "coinpoker-live",
		Pot:            1600,
		CommunityCards: []Card{},
		Seats: []SeatState{
			{PlayerID: "AdamSD84", PlayerName: "AdamSD84", Stack: 185000},
			{PlayerID: "CoolioTDW", PlayerName: "CoolioTDW", Stack: 177000},
		},
	}
	s4 := st.Stabilize(f4)
	if len(s4.CommunityCards) != 0 || s4.Pot != 1600 || s4.Street != StreetPreflop {
		t.Errorf("new hand reset failed: cards=%d, pot=%.2f, street=%s", len(s4.CommunityCards), s4.Pot, s4.Street)
	}
}

// A showdown frame is the trigger for persisting a hand and updating opponent
// profiles. Smoothing used to recompute Street from the board and demote it
// back to river, so hands stopped being saved entirely.
func TestStateStabilizer_ShowdownIsNotSmoothedAway(t *testing.T) {
	st := NewStateStabilizer()

	c1, _ := ParseCard("10s")
	c2, _ := ParseCard("Ks")
	c3, _ := ParseCard("3s")

	st.Stabilize(&HandState{
		HandID:         "hand-1",
		TableID:        "coinpoker-live",
		Street:         StreetFlop,
		Pot:            10600,
		CommunityCards: []Card{c1, c2, c3},
	})

	end := st.Stabilize(&HandState{
		HandID:         "hand-1",
		TableID:        "coinpoker-live",
		Street:         StreetShowdown,
		Pot:            42000,
		CommunityCards: []Card{c1, c2, c3},
	})

	if end.Street != StreetShowdown {
		t.Errorf("showdown was smoothed away: got street %q, want %q", end.Street, StreetShowdown)
	}
	if end.HandID != "hand-1" {
		t.Errorf("hand id lost: got %q, want %q", end.HandID, "hand-1")
	}
	if end.Pot != 42000 {
		t.Errorf("final pot lost: got %.0f, want 42000", end.Pot)
	}
}

// Vision resolves the two hole cards independently; only filling slot 0 left
// the second card unknown for the rest of the hand.
func TestStateStabilizer_HoleCardsFillIndependently(t *testing.T) {
	st := NewStateStabilizer()
	qh, _ := ParseCard("Qh")
	qd, _ := ParseCard("Qd")

	st.Stabilize(&HandState{TableID: "t", Street: StreetPreflop, Pot: 300})
	st.Stabilize(&HandState{TableID: "t", Street: StreetPreflop, Pot: 300, HeroCards: [2]Card{{}, qd}})
	got := st.Stabilize(&HandState{TableID: "t", Street: StreetPreflop, Pot: 300, HeroCards: [2]Card{qh, {}}})

	if got.HeroCards[0] != qh || got.HeroCards[1] != qd {
		t.Errorf("hole cards did not accumulate: got %v %v, want %v %v",
			got.HeroCards[0], got.HeroCards[1], qh, qd)
	}
}

// A missed board card for one frame must not walk the street backwards.
func TestStateStabilizer_StreetNeverGoesBackwards(t *testing.T) {
	st := NewStateStabilizer()
	c := make([]Card, 0, 4)
	for _, s := range []string{"10s", "Ks", "3s", "8d"} {
		card, _ := ParseCard(s)
		c = append(c, card)
	}

	st.Stabilize(&HandState{TableID: "t", Pot: 1000, CommunityCards: c})
	got := st.Stabilize(&HandState{TableID: "t", Pot: 1000, CommunityCards: c[:3]})

	if got.Street != StreetTurn {
		t.Errorf("street walked backwards on a dropped card: got %q, want %q", got.Street, StreetTurn)
	}
	if len(got.CommunityCards) != 4 {
		t.Errorf("board shrank on a dropped card: got %d cards, want 4", len(got.CommunityCards))
	}
}

// A card slot that fails to read for a frame arrives as an unknown card in its
// own position, not as a shorter list. Live, hero's king was covered by the
// position badge, the queen beside it slid into slot 0, and hero looked like a
// one-card hand -- so no advice was produced at all.
func TestStateStabilizer_PartialHoleCardsFillInPlace(t *testing.T) {
	st := NewStateStabilizer()
	kd, _ := ParseCard("Kd")
	qc, _ := ParseCard("Qc")

	// Frame 1: only the second slot resolved.
	st.Stabilize(&HandState{TableID: "t", Street: StreetPreflop, Pot: 4280,
		HeroCards: [2]Card{{}, qc}})

	// Frame 2: the first slot resolves; the second is momentarily lost.
	got := st.Stabilize(&HandState{TableID: "t", Street: StreetPreflop, Pot: 4280,
		HeroCards: [2]Card{kd, {}}})

	if got.HeroCards[0] != kd || got.HeroCards[1] != qc {
		t.Errorf("hole cards did not accumulate in place: got %v %v, want %v %v",
			got.HeroCards[0], got.HeroCards[1], kd, qc)
	}
}

// The same for the board: an unread middle card must not shift the ones after
// it, or the stabiliser has nothing to line up against between frames.
func TestStateStabilizer_BoardHoleFillsInPlace(t *testing.T) {
	st := NewStateStabilizer()
	c1, _ := ParseCard("10c")
	c2, _ := ParseCard("8s")
	c3, _ := ParseCard("2c")

	st.Stabilize(&HandState{TableID: "t", Pot: 1000,
		CommunityCards: []Card{c1, {}, c3}})
	got := st.Stabilize(&HandState{TableID: "t", Pot: 1000,
		CommunityCards: []Card{c1, c2, c3}})

	if len(got.CommunityCards) != 3 {
		t.Fatalf("expected 3 board cards, got %d", len(got.CommunityCards))
	}
	if got.CommunityCards[1] != c2 {
		t.Errorf("the missing middle card did not fill in place: got %v, want %v",
			got.CommunityCards[1], c2)
	}
	if got.Street != StreetFlop {
		t.Errorf("street: got %q, want %q", got.Street, StreetFlop)
	}
}

// Hand identity has to be minted here, because the vision layer only ever
// sends the placeholder. Hand history is keyed by hand id with an upsert, so a
// constant id meant every hand overwrote the last and a whole session
// collapsed into one row.
func TestStateStabilizer_MintsDistinctHandIDs(t *testing.T) {
	st := NewStateStabilizer()
	c1, _ := ParseCard("10c")
	c2, _ := ParseCard("8s")
	c3, _ := ParseCard("2c")

	first := st.Stabilize(&HandState{TableID: "NLH 1228078 - 1K/2K", HandID: "live-hand",
		Pot: 4280, CommunityCards: []Card{c1, c2, c3}})
	firstID := first.HandID

	if firstID == "" || firstID == "live-hand" {
		t.Fatalf("expected a minted hand id, got %q", firstID)
	}

	// Board cleared and pot shrank: the next hand has begun.
	second := st.Stabilize(&HandState{TableID: "NLH 1228078 - 1K/2K", HandID: "live-hand",
		Pot: 300, CommunityCards: nil})

	if second.HandID == firstID {
		t.Errorf("second hand reused the first hand's id %q", second.HandID)
	}
	if second.HandID == "" || second.HandID == "live-hand" {
		t.Errorf("expected a minted id for the second hand, got %q", second.HandID)
	}
}

// The client shows no showdown the screen reader can recognise, so the end of a
// hand is the start of the next one. Without this signal nothing was ever
// persisted or profiled.
func TestStateStabilizer_ReportsCompletedHandOnce(t *testing.T) {
	st := NewStateStabilizer()
	c1, _ := ParseCard("10c")
	c2, _ := ParseCard("8s")
	c3, _ := ParseCard("2c")

	st.Stabilize(&HandState{TableID: "t", HandID: "live-hand", Pot: 4280,
		CommunityCards: []Card{c1, c2, c3},
		Seats:          []SeatState{{PlayerID: "villain", Stack: 5000, IsActive: true}}})

	if got := st.TakeCompletedHand(); got != nil {
		t.Fatalf("no hand has ended yet, got %+v", got)
	}

	st.Stabilize(&HandState{TableID: "t", HandID: "live-hand", Pot: 300})

	done := st.TakeCompletedHand()
	if done == nil {
		t.Fatal("the finished hand was not reported")
	}
	if done.Pot != 4280 || len(done.CommunityCards) != 3 {
		t.Errorf("the reported hand is not the one that finished: pot %.0f, %d board cards",
			done.Pot, len(done.CommunityCards))
	}

	// Reported exactly once, or the same hand is saved and profiled repeatedly.
	if again := st.TakeCompletedHand(); again != nil {
		t.Errorf("the same completed hand was reported twice: %+v", again)
	}
}

// A transition with nothing in it is an artefact, not a hand played.
func TestStateStabilizer_DoesNotRecordEmptyHands(t *testing.T) {
	st := NewStateStabilizer()
	qh, _ := ParseCard("Qh")
	qd, _ := ParseCard("Qd")
	kd, _ := ParseCard("Kd")
	kc, _ := ParseCard("Kc")

	st.Stabilize(&HandState{TableID: "t", HeroCards: [2]Card{qh, qd}})
	// Different hole cards: a new hand, but the previous one had no pot at all.
	st.Stabilize(&HandState{TableID: "t", HeroCards: [2]Card{kd, kc}})

	if got := st.TakeCompletedHand(); got != nil {
		t.Errorf("recorded a hand that never had a pot or a board: %+v", got)
	}
}

// One misread frame must not poison the pot for the rest of the session. Live,
// a single frame read 401,920 into a 4,920 pot; because the pot may only grow
// within a hand, that figure could never be lowered again and the HUD showed it
// for minutes while the table sat at 8,920.
func TestStateStabilizer_SinglePotSpikeIsRejected(t *testing.T) {
	st := NewStateStabilizer()

	base := &HandState{TableID: "t", Street: StreetPreflop, Pot: 4920}
	st.Stabilize(base)
	st.Stabilize(base)

	spike := &HandState{TableID: "t", Street: StreetPreflop, Pot: 401920}
	got := st.Stabilize(spike)
	if got.Pot != 4920 {
		t.Errorf("an unconfirmed pot spike was adopted: got %.0f, want 4920", got.Pot)
	}

	// The table carries on at the real figure.
	got = st.Stabilize(&HandState{TableID: "t", Street: StreetPreflop, Pot: 6920})
	got = st.Stabilize(&HandState{TableID: "t", Street: StreetPreflop, Pot: 6920})
	if got.Pot != 6920 {
		t.Errorf("the pot did not recover after a spike: got %.0f, want 6920", got.Pot)
	}
}

// A genuine all-in multiplies the pot many times over, and must be believed --
// it simply has to be seen in more than one frame, which a real one always is.
func TestStateStabilizer_ConfirmedPotJumpIsAccepted(t *testing.T) {
	st := NewStateStabilizer()

	base := &HandState{TableID: "t", Street: StreetPreflop, Pot: 4920}
	st.Stabilize(base)
	st.Stabilize(base)

	shove := &HandState{TableID: "t", Street: StreetPreflop, Pot: 401920}
	st.Stabilize(shove)
	got := st.Stabilize(shove)

	if got.Pot != 401920 {
		t.Errorf("a confirmed all-in pot was rejected: got %.0f, want 401920", got.Pot)
	}
}

// A hand that ends before the flop leaves no board to clear, so the board-based
// transition rule cannot fire. Without another signal the state carried
// straight into the next hand and stuck: hero's old cards, old board and an
// inflated pot stayed on screen while a new hand was dealt.
func TestStateStabilizer_PreflopHandTransitionIsDetected(t *testing.T) {
	st := NewStateStabilizer()
	qc, _ := ParseCard("Qc")
	td, _ := ParseCard("Td")

	big := &HandState{TableID: "t", Street: StreetPreflop, Pot: 97122,
		HeroCards: [2]Card{qc, td}}
	st.Stabilize(big)
	st.Stabilize(big)
	firstID := st.Stabilize(big).HandID

	// The next hand starts: the pot resets to the blinds.
	small := &HandState{TableID: "t", Street: StreetPreflop, Pot: 1920}
	st.Stabilize(small)
	got := st.Stabilize(small)

	if got.HandID == firstID {
		t.Errorf("a preflop hand transition was missed: still hand %q", got.HandID)
	}
	if got.Pot != 1920 {
		t.Errorf("the pot did not reset with the new hand: got %.0f, want 1920", got.Pot)
	}
	if got.HeroCards[0].Rank > 0 {
		t.Errorf("the previous hand's hole cards carried over: %v", got.HeroCards[0])
	}
}

// A showdown is visible for only a few frames before the client clears the
// table. Losing it would lose the one moment an opponent's actual holding can
// be observed -- which is the only ground truth a range model can be built on.
func TestStateStabilizer_RevealedCardsSurviveTheHand(t *testing.T) {
	st := NewStateStabilizer()
	ah, _ := ParseCard("Ah")
	kd, _ := ParseCard("Kd")
	board, _ := ParseCards("10c 8s 2c")

	st.Stabilize(&HandState{TableID: "t", Street: StreetFlop, Pot: 4280,
		CommunityCards: board,
		Seats:          []SeatState{{PlayerID: "villain", Stack: 5000, IsActive: true}}})

	// The villain turns their cards over.
	st.Stabilize(&HandState{TableID: "t", Street: StreetFlop, Pot: 4280,
		CommunityCards: board,
		Seats: []SeatState{
			{PlayerID: "villain", Stack: 5000, IsActive: true, Cards: []Card{ah, kd}},
		}})

	// The next frame no longer shows them.
	got := st.Stabilize(&HandState{TableID: "t", Street: StreetFlop, Pot: 4280,
		CommunityCards: board,
		Seats:          []SeatState{{PlayerID: "villain", Stack: 5000, IsActive: true}}})

	if len(got.Seats) != 1 || len(got.Seats[0].Cards) != 2 {
		t.Fatalf("the revealed hand was lost: %+v", got.Seats)
	}
	if got.Seats[0].Cards[0] != ah || got.Seats[0].Cards[1] != kd {
		t.Errorf("wrong cards retained: got %v, want %v %v", got.Seats[0].Cards, ah, kd)
	}
}

// Players do not leave in the middle of a hand. A nameplate missed for a single
// frame used to drop the player from the state entirely -- and since the live
// opponent count drives both the equity simulation and the EV formula, that
// turned a three-way all-in into a heads-up one. Recorded live: pocket threes
// scored 53% instead of 24% and the tool called off a whole stack.
func TestStateStabilizer_SeatsSurviveAMissedFrame(t *testing.T) {
	st := NewStateStabilizer()

	full := []SeatState{
		{PlayerID: "a", PlayerName: "a", Stack: 100000, IsActive: true},
		{PlayerID: "b", PlayerName: "b", Stack: 200000, IsActive: true},
		{PlayerID: "c", PlayerName: "c", Stack: 300000, IsActive: true},
	}
	st.Stabilize(&HandState{TableID: "t", Street: StreetPreflop, Pot: 6920, Seats: full})

	// The next frame reads only one nameplate.
	got := st.Stabilize(&HandState{TableID: "t", Street: StreetPreflop, Pot: 6920,
		Seats: []SeatState{{PlayerID: "a", PlayerName: "a", Stack: 100000, IsActive: true}}})

	if len(got.Seats) != 3 {
		names := make([]string, len(got.Seats))
		for i, s := range got.Seats {
			names[i] = s.PlayerID
		}
		t.Fatalf("players vanished on a missed frame: %v", names)
	}

	live := 0
	for _, s := range got.Seats {
		if s.IsActive && !s.IsFolded {
			live++
		}
	}
	if live != 3 {
		t.Errorf("expected 3 live players, got %d", live)
	}
}

// A player who actually folds must still leave the live count.
func TestStateStabilizer_FoldedPlayerStopsBeingLive(t *testing.T) {
	st := NewStateStabilizer()

	st.Stabilize(&HandState{TableID: "t", Street: StreetPreflop, Pot: 6920, Seats: []SeatState{
		{PlayerID: "a", PlayerName: "a", Stack: 100000, IsActive: true},
		{PlayerID: "b", PlayerName: "b", Stack: 200000, IsActive: true},
	}})

	got := st.Stabilize(&HandState{TableID: "t", Street: StreetPreflop, Pot: 6920, Seats: []SeatState{
		{PlayerID: "a", PlayerName: "a", Stack: 100000, IsActive: true},
		{PlayerID: "b", PlayerName: "b", Stack: 200000, IsActive: true, IsFolded: true, LastAction: "fold"},
	}})

	live := 0
	for _, s := range got.Seats {
		if s.IsActive && !s.IsFolded {
			live++
		}
	}
	if live != 1 {
		t.Errorf("a folded player is still counted live: %d live of %d seats", live, len(got.Seats))
	}
}

// A deck holds one of each card, so a duplicate is proof that a reading is
// wrong -- proof available without re-examining a pixel. Live, hero's six of
// spades was read as a second eight of spades and the engine computed equity
// for a hand that cannot be dealt, then recommended a call on it.
func TestStateStabilizer_RejectsImpossibleDuplicateCards(t *testing.T) {
	st := NewStateStabilizer()
	eight, _ := ParseCard("8s")

	got := st.Stabilize(&HandState{
		TableID: "t", Street: StreetPreflop, Pot: 12600,
		HeroCards: [2]Card{eight, eight},
	})

	if got.HeroCards[0] == got.HeroCards[1] && got.HeroCards[0].Rank > 0 {
		t.Errorf("kept a hand holding the same card twice: %v %v", got.HeroCards[0], got.HeroCards[1])
	}
	// One reading survives; the contradicted one is blanked so a later frame
	// can supply the real card.
	if got.HeroCards[0] != eight || got.HeroCards[1].Rank != 0 {
		t.Errorf("expected the first card kept and the duplicate blanked, got %v %v",
			got.HeroCards[0], got.HeroCards[1])
	}
}

// A board card that clashes with hero's hand is likewise impossible.
func TestStateStabilizer_RejectsBoardClashingWithHero(t *testing.T) {
	st := NewStateStabilizer()
	ace, _ := ParseCard("As")
	king, _ := ParseCard("Kd")
	queen, _ := ParseCard("Qh")

	got := st.Stabilize(&HandState{
		TableID: "t", Street: StreetFlop, Pot: 5000,
		CommunityCards: []Card{ace, king, queen},
		HeroCards:      [2]Card{ace, king},
	})

	// The board is read from a fixed rack and is the more reliable of the two,
	// so hero's clashing cards are the ones blanked.
	if got.HeroCards[0].Rank != 0 || got.HeroCards[1].Rank != 0 {
		t.Errorf("hero kept cards already on the board: %v %v", got.HeroCards[0], got.HeroCards[1])
	}
	if len(got.CommunityCards) != 3 || got.CommunityCards[0] != ace {
		t.Errorf("the board was altered: %v", got.CommunityCards)
	}
}

// A frame that misreads both of hero's hole cards must not be allowed to end
// the hand in progress.
//
// This was the most destructive misread in the pipeline, and it did not look
// like a card bug at all. One bad frame ended the hand, recorded it as
// complete, and opened a new one; the real hand came back on the next frame and
// was recorded again. A single live hand reached the database as two, each with
// a different holding, and every statistic built on that history was wrong by
// however often a card was misread.
//
// The vision side has since stopped producing confident garbage -- see
// pkg/capture/vision_overlap_test.go -- but a reading that clears every
// threshold can still be wrong, and this is the layer that has more than one
// frame to work with.
func TestStateStabilizer_SingleBadHoleCardFrameDoesNotSplitTheHand(t *testing.T) {
	st := NewStateStabilizer()

	tenClubs, _ := ParseCard("10c")
	threeSpades, _ := ParseCard("3s")
	kingClubs, _ := ParseCard("Kc")

	settled := func() *HandState {
		return &HandState{
			TableID:   "coinpoker-live",
			Pot:       6000,
			HeroCards: [2]Card{tenClubs, threeSpades},
			Seats:     []SeatState{{PlayerID: "hero", PlayerName: "hero", Stack: 100000}},
		}
	}

	first := st.Stabilize(settled())
	handID := first.HandID
	if handID == "" {
		t.Fatal("first frame produced no hand id")
	}

	// The live misread: 10c 3s came back as 10c Kc. Wrong rank and wrong suit
	// on the second card, and nothing in the frame says so.
	misread := settled()
	misread.HeroCards = [2]Card{tenClubs, kingClubs}
	got := st.Stabilize(misread)

	if got.HandID != handID {
		t.Errorf("one misread frame started a new hand: %q became %q", handID, got.HandID)
	}
	if !sameHoleCards(got.HeroCards, [2]Card{tenClubs, threeSpades}) {
		t.Errorf("one misread frame changed hero's hand: got %v %v, want 10c 3s",
			got.HeroCards[0], got.HeroCards[1])
	}
	if done := st.TakeCompletedHand(); done != nil {
		t.Errorf("one misread frame recorded a completed hand: %+v", done.HeroCards)
	}

	// The next frame reads correctly again, which has to withdraw the pending
	// change rather than leave it half-armed for the rest of the hand.
	back := st.Stabilize(settled())
	if back.HandID != handID {
		t.Errorf("recovering frame started a new hand: %q became %q", handID, back.HandID)
	}
}

// The other half of the same rule: hero really is dealt a new hand, and the
// stabiliser has to notice. Two consecutive frames agreeing on different cards
// is the evidence, and it is the same evidence the pot rule already demands.
func TestStateStabilizer_ConfirmedHoleCardChangeStartsNewHand(t *testing.T) {
	st := NewStateStabilizer()

	tenClubs, _ := ParseCard("10c")
	threeSpades, _ := ParseCard("3s")
	aceHearts, _ := ParseCard("Ah")
	aceSpades, _ := ParseCard("As")

	frame := func(a, b Card) *HandState {
		return &HandState{
			TableID:   "coinpoker-live",
			Pot:       6000,
			HeroCards: [2]Card{a, b},
			Seats:     []SeatState{{PlayerID: "hero", PlayerName: "hero", Stack: 100000}},
		}
	}

	first := st.Stabilize(frame(tenClubs, threeSpades))
	handID := first.HandID

	if got := st.Stabilize(frame(aceHearts, aceSpades)); got.HandID != handID {
		t.Fatalf("new cards were believed on one frame: %q became %q", handID, got.HandID)
	}
	next := st.Stabilize(frame(aceHearts, aceSpades))
	if next.HandID == handID {
		t.Errorf("new cards confirmed twice did not start a new hand, still %q", handID)
	}
	if !sameHoleCards(next.HeroCards, [2]Card{aceHearts, aceSpades}) {
		t.Errorf("new hand kept the old hole cards: got %v %v", next.HeroCards[0], next.HeroCards[1])
	}
	if done := st.TakeCompletedHand(); done == nil {
		t.Error("the finished hand was not handed back for recording")
	}
}

// An unread card is not a spade.
//
// Suit is an enum whose zero value is Spades, so the zero Card -- meaning "not
// read" -- rendered as "?s". Hand histories in the database carry entries like
// ["?s", "3d"], which were taken for half-read cards and are nothing of the
// kind: the recogniser only ever emits a card when both halves are read.
func TestCard_UnreadCardIsNotASpade(t *testing.T) {
	if got := (Card{}).String(); got != "??" {
		t.Errorf("unread card rendered as %q, want %q", got, "??")
	}
	if (Card{}).Known() {
		t.Error("the zero card reports itself as known")
	}

	// It still round-trips, so nothing already written can fail to load.
	var c Card
	if err := c.UnmarshalJSON([]byte(`"??"`)); err != nil {
		t.Fatalf("decoding %q: %v", "??", err)
	}
	if c.Known() {
		t.Errorf("%q decoded to a known card %v", "??", c)
	}
	if err := c.UnmarshalJSON([]byte(`"?s"`)); err != nil {
		t.Fatalf("decoding the legacy %q: %v", "?s", err)
	}
	if c.Known() {
		t.Errorf("legacy %q decoded to a known card %v", "?s", c)
	}
}

// A hand is recognised two or three frames after it is dealt -- a pot drop has
// to be seen twice, hole cards likewise -- and at the capture rate that is most
// of a second. Whatever players did inside that window is already on their
// nameplates when the hand is recognised, and it used to become the baseline
// that later badges were measured against, so those actions were never
// recorded at all.
//
// Replaying a recorded session showed 18 of 107 hands with no action recorded
// whatever; seeding recovers 5 of them.
func TestStateStabilizer_ActionsAlreadyOnScreenWhenTheHandStarts(t *testing.T) {
	st := NewStateStabilizer()

	seats := func(badges ...string) []SeatState {
		out := make([]SeatState, 0, len(badges))
		for i, b := range badges {
			out = append(out, SeatState{
				PlayerID:   fmt.Sprintf("p%d", i),
				PlayerName: fmt.Sprintf("p%d", i),
				Stack:      100000,
				LastAction: b,
				IsActive:   true,
			})
		}
		return out
	}

	// A hand in progress, so the next one has to be recognised rather than
	// simply adopted as the first state seen.
	prior := &HandState{TableID: "coinpoker-live", Pot: 40000, Seats: seats("call", "call")}
	st.Stabilize(prior)

	// The next hand. The pot has collapsed, and by the time that is confirmed
	// the under-the-gun player has already opened.
	dealt := func() *HandState {
		return &HandState{TableID: "coinpoker-live", Pot: 5000, Seats: seats("raise", "fold")}
	}
	st.Stabilize(dealt())
	got := st.Stabilize(dealt())

	var raises, folds int
	for _, a := range got.ActionHistory {
		switch a.Action {
		case ActionRaise:
			raises++
		case ActionFold:
			folds++
		}
	}
	if raises != 1 {
		t.Errorf("the open on screen when the hand was recognised was not recorded: %d raises in %v",
			raises, got.ActionHistory)
	}
	if folds != 1 {
		t.Errorf("the fold on screen when the hand was recognised was not recorded: %d folds in %v",
			folds, got.ActionHistory)
	}
	// And it is recorded once, not once per frame for as long as the badge sits
	// on the nameplate.
	if len(got.ActionHistory) != 2 {
		t.Errorf("badges were recorded more than once: %v", got.ActionHistory)
	}
}

// The screen reader names hero only on the frames where hero's hole cards
// actually read; on every other frame it sends the placeholder, which matches
// no seat. Hero's chair does not move when hero's cards stop being legible, so
// the identity has to survive those frames.
//
// This is the defect the harness priced highest of the seven -- about
// 50 bb/100 -- because losing the seat loses hero's position and hero's stack
// at once: the preflop chart falls silent and the effective stack is taken from
// whoever else is at the table.
func TestStateStabilizer_HeroIdentitySurvivesUnreadableHoleCards(t *testing.T) {
	st := NewStateStabilizer()

	seats := func() []SeatState {
		return []SeatState{
			{PlayerID: "ludoStarik", PlayerName: "ludoStarik", Stack: 67940, Position: PosCO},
			{PlayerID: "Rafidamage", PlayerName: "Rafidamage", Stack: 1190000, Position: PosBTN},
		}
	}

	// A frame where hero's cards read: the reader can name the seat.
	named := st.Stabilize(&HandState{
		TableID: "t", Street: StreetPreflop, Pot: 4920,
		HeroID: "ludoStarik", Seats: seats(),
	})
	if named.HeroID != "ludoStarik" {
		t.Fatalf("HeroID = %q on a frame that named hero", named.HeroID)
	}

	// The next frame could not read them -- hero folded, or a card was mid
	// animation -- so the placeholder arrives instead.
	blind := st.Stabilize(&HandState{
		TableID: "t", Street: StreetPreflop, Pot: 4920,
		HeroID: "Hero", Seats: seats(),
	})
	if blind.HeroID != "ludoStarik" {
		t.Fatalf("HeroID = %q after the placeholder arrived, want it carried forward", blind.HeroID)
	}

	// The seat has to be findable, because that is the whole point: hero's
	// stack and hero's position are read off it.
	var found *SeatState
	for i := range blind.Seats {
		if blind.Seats[i].PlayerID == blind.HeroID {
			found = &blind.Seats[i]
		}
	}
	if found == nil {
		t.Fatal("hero id matches no seat, so stack and position are both unreadable")
	}
	if found.Stack != 67940 {
		t.Errorf("hero stack = %v, want hero's own 67940", found.Stack)
	}
	if found.Position != PosCO {
		t.Errorf("hero position = %q, want CO", found.Position)
	}
}

// Hero keeps their seat across a hand boundary. The reader only names hero once
// the hole cards read, which is a beat or two into preflop -- so without this
// every hand opens with hero unidentified, in the street where the chart
// matters most.
func TestStateStabilizer_HeroIdentitySurvivesTheNextDeal(t *testing.T) {
	st := NewStateStabilizer()
	c1, _ := ParseCard("Jh")
	c2, _ := ParseCard("Td")
	c3, _ := ParseCard("8s")

	seats := func() []SeatState {
		return []SeatState{
			{PlayerID: "ludoStarik", Stack: 67940},
			{PlayerID: "Rafidamage", Stack: 1190000},
		}
	}

	st.Stabilize(&HandState{
		TableID: "t", Street: StreetFlop, Pot: 65600, HeroID: "ludoStarik",
		CommunityCards: []Card{c1, c2, c3}, Seats: seats(),
	})

	// Board cleared and the pot shrank: the next hand has been dealt, and the
	// reader has not managed to read hero's cards yet.
	next := st.Stabilize(&HandState{
		TableID: "t", Street: StreetPreflop, Pot: 3000, HeroID: "Hero",
		Seats: seats(),
	})
	if next.HeroID != "ludoStarik" {
		t.Fatalf("HeroID = %q on the first frame of the next hand", next.HeroID)
	}
}

// Carried forward only while that player is still at the table. Somebody who
// gets up must not leave their name on hero, or the next person in the chair
// inherits hero's identity along with it.
func TestStateStabilizer_HeroIdentityIsNotCarriedToAnEmptyChair(t *testing.T) {
	st := NewStateStabilizer()
	c1, _ := ParseCard("Jh")
	c2, _ := ParseCard("Td")
	c3, _ := ParseCard("8s")

	st.Stabilize(&HandState{
		TableID: "t", Street: StreetFlop, Pot: 65600, HeroID: "ludoStarik",
		CommunityCards: []Card{c1, c2, c3},
		Seats: []SeatState{
			{PlayerID: "ludoStarik", Stack: 67940},
			{PlayerID: "Rafidamage", Stack: 1190000},
		},
	})

	// Hero busted and got up; a stranger took the chair; the next hand is dealt
	// and the reader cannot name hero.
	gone := st.Stabilize(&HandState{
		TableID: "t", Street: StreetPreflop, Pot: 3000, HeroID: "Hero",
		Seats: []SeatState{
			{PlayerID: "newcomer", Stack: 80000},
			{PlayerID: "Rafidamage", Stack: 1190000},
		},
	})
	if gone.HeroID == "ludoStarik" {
		t.Error("hero's name was carried to a table hero has left")
	}
	if gone.HeroID == "newcomer" {
		t.Error("hero's identity was handed to whoever took the chair")
	}
}

// A real name on the frame always wins. The carry-forward is a fallback for the
// placeholder, not a lock on the first identity ever seen.
func TestStateStabilizer_HeroIdentityYieldsToANamedFrame(t *testing.T) {
	st := NewStateStabilizer()
	seats := []SeatState{
		{PlayerID: "ludoStarik", Stack: 67940},
		{PlayerID: "Rafidamage", Stack: 1190000},
	}
	st.Stabilize(&HandState{TableID: "t", Street: StreetPreflop, Pot: 100, HeroID: "ludoStarik", Seats: seats})
	moved := st.Stabilize(&HandState{TableID: "t", Street: StreetPreflop, Pot: 100, HeroID: "Rafidamage", Seats: seats})
	if moved.HeroID != "Rafidamage" {
		t.Fatalf("HeroID = %q, want the name the frame carried", moved.HeroID)
	}
}

// Nothing to carry forward yet: a session that opens on the placeholder must
// not invent a hero, because a spectator view has none.
func TestStateStabilizer_NoHeroInventedOnTheFirstFrame(t *testing.T) {
	st := NewStateStabilizer()
	first := st.Stabilize(&HandState{
		TableID: "t", Street: StreetPreflop, Pot: 100, HeroID: "Hero",
		Seats: []SeatState{{PlayerID: "a", Stack: 1}, {PlayerID: "b", Stack: 2}},
	})
	if seatedIn(first.Seats, first.HeroID) {
		t.Fatalf("HeroID = %q matched a seat with nothing to carry forward", first.HeroID)
	}
}

// A hand ends on the river and the next one is dealt with the same pot.
//
// The board clearing used to need a shrinking pot to confirm it, and "less
// than" is false on equal numbers. Live on 2026-09-01 a hand ended on the river
// with a pot of 8,920; the next hand's blinds and limps came to the same 8,920;
// the old hand never ended. Hero had folded it, so the guard against advising a
// folded hand silenced the panel for the whole of the following hand -- while
// hero sat looking at Fold / Call 2,000 / Raise 4,000 and a running clock.
func TestStateStabilizer_NewHandWhenTheBoardClearsAndThePotDoesNot(t *testing.T) {
	st := NewStateStabilizer()
	j, _ := ParseCard("Jh")
	two, _ := ParseCard("2d")
	jd, _ := ParseCard("Jd")
	six, _ := ParseCard("6h")
	ace, _ := ParseCard("As")

	seats := func(folded bool) []SeatState {
		return []SeatState{
			{PlayerID: "ludoStarik", Stack: 184160, IsFolded: folded},
			{PlayerID: "prey78", Stack: 1160000},
		}
	}

	river := st.Stabilize(&HandState{
		TableID: "t", Street: StreetRiver, Pot: 8920,
		CommunityCards: []Card{j, two, jd, six, ace},
		HeroID:         "ludoStarik", Seats: seats(true),
	})
	if len(river.CommunityCards) != 5 {
		t.Fatalf("board on the river: %d cards", len(river.CommunityCards))
	}

	// The next hand, dealt to exactly the same pot.
	next := &HandState{
		TableID: "t", Street: StreetPreflop, Pot: 8920,
		HeroID: "ludoStarik", Seats: seats(false),
	}
	first := st.Stabilize(next)
	if len(first.CommunityCards) != 0 {
		// One frame of confirmation is allowed; the board must not come back.
		t.Logf("first frame still carries %d board cards", len(first.CommunityCards))
	}

	second := st.Stabilize(next)
	if len(second.CommunityCards) != 0 {
		t.Errorf("board still %d cards two frames into the next hand", len(second.CommunityCards))
	}
	if second.Street != StreetPreflop {
		t.Errorf("street = %q, want preflop", second.Street)
	}
	// The decisive one: hero's fold belonged to the hand that ended.
	for _, s := range second.Seats {
		if s.PlayerID == "ludoStarik" && s.IsFolded {
			t.Error("hero is still folded in the next hand, so no advice will be given in it")
		}
	}
}

// The pot shrinking still ends the hand on the first frame. Waiting for a
// second one in the common case would cost a frame of advice at every deal.
func TestStateStabilizer_ShrinkingPotEndsTheHandAtOnce(t *testing.T) {
	st := NewStateStabilizer()
	j, _ := ParseCard("Jh")
	two, _ := ParseCard("2d")
	jd, _ := ParseCard("Jd")

	st.Stabilize(&HandState{
		TableID: "t", Street: StreetFlop, Pot: 65600,
		CommunityCards: []Card{j, two, jd},
		HeroID:         "hero", Seats: []SeatState{{PlayerID: "hero", Stack: 100}},
	})
	next := st.Stabilize(&HandState{
		TableID: "t", Street: StreetPreflop, Pot: 3000,
		HeroID: "hero", Seats: []SeatState{{PlayerID: "hero", Stack: 100}},
	})
	if len(next.CommunityCards) != 0 {
		t.Errorf("board carried into the next hand: %d cards", len(next.CommunityCards))
	}
}

// A single frame that reads no board at all is a dropout, not a new hand. This
// is what the confirmation is for, and it is why the board rule cannot fire on
// one frame without a shrinking pot to corroborate it.
func TestStateStabilizer_SingleBoardDropoutIsNotANewHand(t *testing.T) {
	st := NewStateStabilizer()
	j, _ := ParseCard("Jh")
	two, _ := ParseCard("2d")
	jd, _ := ParseCard("Jd")
	board := []Card{j, two, jd}

	st.Stabilize(&HandState{
		TableID: "t", Street: StreetFlop, Pot: 8920,
		CommunityCards: board,
		HeroID:         "hero", Seats: []SeatState{{PlayerID: "hero", Stack: 100}},
	})
	// The recogniser misses the board for one frame; the pot is unchanged.
	dropped := st.Stabilize(&HandState{
		TableID: "t", Street: StreetFlop, Pot: 8920,
		HeroID: "hero", Seats: []SeatState{{PlayerID: "hero", Stack: 100}},
	})
	if len(dropped.CommunityCards) != 3 {
		t.Fatalf("a one-frame dropout ended the hand: board is %d cards", len(dropped.CommunityCards))
	}
	// And the board coming back withdraws the pending transition.
	back := st.Stabilize(&HandState{
		TableID: "t", Street: StreetFlop, Pot: 8920,
		CommunityCards: board,
		HeroID:         "hero", Seats: []SeatState{{PlayerID: "hero", Stack: 100}},
	})
	if len(back.CommunityCards) != 3 {
		t.Errorf("board after recovery: %d cards", len(back.CommunityCards))
	}
}
