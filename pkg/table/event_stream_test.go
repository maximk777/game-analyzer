package table

import "testing"

func seatWithBadge(id, badge string, pos Position) SeatState {
	return SeatState{PlayerID: id, PlayerName: id, Stack: 100000, LastAction: badge, Position: pos, IsActive: true}
}

// Events are written once, when a hand closes, and they say what the stabiliser
// finally concluded about it.
//
// Written as each action was observed instead, an open that landed inside the
// two or three frames before the hand was recognised went into the *previous*
// hand and then again into this one -- the same raise recorded twice, against
// two different hands, one of them wrong.
func TestStabilizerEmitsEachActionOnce(t *testing.T) {
	st := NewStateStabilizer()
	const title = "NLH 1229111 - 1K/2K (320)"

	deal := func(pot float64, badges ...string) *HandState {
		seats := make([]SeatState, 0, len(badges))
		for i, b := range badges {
			seats = append(seats, seatWithBadge("p"+string(rune('0'+i)), b, PosBTN))
		}
		return &HandState{TableID: title, Pot: pot, Seats: seats}
	}

	// One hand, played out.
	st.Stabilize(deal(5000, "raise", "fold"))
	st.Stabilize(deal(9000, "raise", "call"))
	st.Stabilize(deal(9000, "raise", "call"))
	if got := st.TakeEvents(); len(got) != 0 {
		t.Fatalf("a hand still in progress produced %d events", len(got))
	}

	// The next one, which is what closes the first.
	st.Stabilize(deal(2000, "check", "check"))
	st.Stabilize(deal(2000, "check", "check"))

	events := st.TakeEvents()
	if len(events) == 0 {
		t.Fatal("closing a hand produced no events")
	}

	var starts, actions int
	byAction := map[ActionType]int{}
	seqByHand := map[string]map[int]bool{}
	for _, e := range events {
		if seqByHand[e.HandID] == nil {
			seqByHand[e.HandID] = map[int]bool{}
		}
		if seqByHand[e.HandID][e.Seq] {
			t.Errorf("hand %s used sequence number %d twice", e.HandID, e.Seq)
		}
		seqByHand[e.HandID][e.Seq] = true

		if e.SessionID == "" {
			t.Error("an event carries no session id, so nothing can tell which build read it")
		}
		if e.TableKey != "1229111" {
			t.Errorf("table key %q, want 1229111 -- the title is not the identity", e.TableKey)
		}
		switch e.Kind {
		case EventHandStart:
			starts++
		case EventAction:
			actions++
			byAction[e.Action]++
		}
	}

	if starts == 0 {
		t.Error("the hand beginning was not recorded")
	}
	if byAction[ActionRaise] != 1 {
		t.Errorf("the open was recorded %d times, want once: %v", byAction[ActionRaise], byAction)
	}
	if byAction[ActionCall] != 1 {
		t.Errorf("the call was recorded %d times, want once: %v", byAction[ActionCall], byAction)
	}
	if byAction[ActionFold] != 1 {
		t.Errorf("the fold was recorded %d times, want once: %v", byAction[ActionFold], byAction)
	}

	// Drained, so the next call returns nothing rather than the same events.
	if again := st.TakeEvents(); len(again) != 0 {
		t.Errorf("events were handed out twice: %d the second time", len(again))
	}
}

// An event is a statement about a moment and has to keep saying the same thing
// afterwards. Handed the live slice, a recorded showdown changed underneath the
// log when the stabiliser later blanked a card it decided was impossible.
func TestRecordedEventsDoNotChangeUnderneath(t *testing.T) {
	st := NewStateStabilizer()

	board, _ := ParseCards("5h 2h 3d")
	shown, _ := ParseCards("8s 8d")
	seat := SeatState{PlayerID: "villain", PlayerName: "villain", IsActive: true, Cards: shown}

	state := &HandState{TableID: "NLH 1229111 - 1K/2K (320)", Pot: 5000,
		CommunityCards: board, Seats: []SeatState{seat}}
	st.Stabilize(state)
	// Close the hand, which is when it is written.
	next := &HandState{TableID: "NLH 1229111 - 1K/2K (320)", Pot: 1000,
		Seats: []SeatState{{PlayerID: "villain", IsActive: true}}}
	st.Stabilize(next)
	st.Stabilize(next)

	// Whatever anyone does to the state afterwards.
	for i := range seat.Cards {
		seat.Cards[i] = Card{}
	}
	for i := range state.CommunityCards {
		state.CommunityCards[i] = Card{}
	}

	for _, e := range st.TakeEvents() {
		if e.Kind != EventReveal {
			continue
		}
		if len(e.Cards) != 2 || !e.Cards[0].Known() || !e.Cards[1].Known() {
			t.Errorf("the recorded showdown lost its cards: %v", e.Cards)
		}
		if len(e.Board) != 3 || !e.Board[0].Known() {
			t.Errorf("the recorded showdown lost its board: %v", e.Board)
		}
	}
}

// Cards attributed to a seat before the flop are not a showdown: nobody has
// shown anything, and recording it puts a holding in the log that was never on
// display.
func TestNoRevealsBeforeTheFlop(t *testing.T) {
	st := NewStateStabilizer()
	shown, _ := ParseCards("As Kd")
	st.Stabilize(&HandState{TableID: "NLH 1229111 - 1K/2K (320)", Pot: 5000,
		Seats: []SeatState{{PlayerID: "villain", IsActive: true, Cards: shown}}})
	closer := &HandState{TableID: "NLH 1229111 - 1K/2K (320)", Pot: 1000,
		Seats: []SeatState{{PlayerID: "villain", IsActive: true}}}
	st.Stabilize(closer)
	st.Stabilize(closer)

	for _, e := range st.TakeEvents() {
		if e.Kind == EventReveal {
			t.Errorf("a showdown was recorded with no board: %v", e.Cards)
		}
	}
}
