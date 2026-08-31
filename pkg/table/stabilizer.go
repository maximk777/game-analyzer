package table

import (
	"fmt"
	"strings"
	"sync"
	"time"
)

// StateStabilizer maintains a smoothed, monotonically consistent table state across noisy/glitchy video frames.
//
// It also owns the hand lifecycle: minting a hand id and deciding when a hand
// has ended. That has to live here rather than in the vision layer, because the
// client never announces a showdown in any form the screen reader can see --
// street is inferred from the number of board cards, which tops out at five.
// Nothing ever produced a terminal state, so no hand was ever persisted and no
// opponent profile ever accumulated. A hand is instead recognised as finished
// the moment the next one begins.
type StateStabilizer struct {
	mu           sync.RWMutex
	currentHand  *HandState
	lastUpdateAt time.Time
	handCount    int
	completed    *HandState
	sessionID    string

	// A candidate pot or hand transition must be seen twice before it is
	// believed. One misread frame used to be enough to poison the state for the
	// rest of the session.
	pendingPot      float64
	pendingPotSeen  int
	pendingDropSeen int

	// Hero's hole cards do not change within a hand, so a frame that disagrees
	// with the hand in progress is either the next hand or a misread -- and the
	// two are told apart the only way they can be, by whether the disagreement
	// survives into the next frame.
	pendingHero     [2]Card
	pendingHeroSeen int

	// A board never shrinks within a hand, so a board that has gone is the next
	// hand -- confirmed the same way, by surviving into the following frame.
	pendingBoardClearSeen int

	// Events observed but not yet taken. The stabiliser is where the action
	// stream is derived, so it is the only place that knows an action is new
	// rather than the same badge seen again; anything downstream would have to
	// re-derive it and would get it wrong the same way the profiler did.
	//
	// Drained with TakeEvents, the same way a finished hand is drained with
	// TakeCompletedHand: this stays a pure function of the frames it is given,
	// and the caller decides what to do with what it produced.
	events []HandEvent
	// seq counts events within the hand in progress, so a re-emitted event is
	// a no-op in the store rather than a duplicate.
	seq int
}

// NewStateStabilizer creates a new StateStabilizer instance.
func NewStateStabilizer() *StateStabilizer {
	return &StateStabilizer{sessionID: time.Now().UTC().Format("20060102-150405")}
}

// TakeEvents drains what has been observed since the last call.
//
// Returned rather than written: the stabiliser has no database and should not
// grow one. What it has is the only view in the system of what changed between
// two frames, which is exactly what an event is.
func (s *StateStabilizer) TakeEvents() []HandEvent {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := s.events
	s.events = nil
	return out
}

// SetSessionID fixes the session identity instead of taking it from the clock.
//
// For the live agent the clock is right: two runs are two observations and
// should not be confused. For anything replaying a recording it is wrong --
// replaying the same file twice would mint two sets of hands and count
// everything in it twice. A replay that names its session after its input
// re-derives rather than accumulates, which is what makes a cursor safe to
// reset and run again.
func (s *StateStabilizer) SetSessionID(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if id != "" {
		s.sessionID = id
	}
}

// SessionID identifies this run of the agent. It goes on every event because
// data has to say what read it: a recorded session in this repository holds
// frames from an older build whose vision could not read action badges, and a
// measurement across the two was quietly wrong.
func (s *StateStabilizer) SessionID() string { return s.sessionID }

// record appends an event for the hand in progress, numbering it.
//
// The card slices are copied. An event is a statement about a moment, and it
// has to keep saying the same thing after the moment has passed: handed the
// caller's slice, it was still pointing at the live state when the stabiliser
// blanked a card it had decided was impossible, and a showdown already recorded
// turned into two unread cards between being written and being drained.
func (s *StateStabilizer) record(e HandEvent) {
	e.SessionID = s.sessionID
	e.TableKey = TableKeyOf(e.TableID)
	e.Seq = s.seq
	s.seq++
	if e.At.IsZero() {
		e.At = time.Now()
	}
	e.Cards = append([]Card(nil), e.Cards...)
	e.Board = append([]Card(nil), e.Board...)
	s.events = append(s.events, e)
}

// mintHandID assigns a hand a durable identity. Without one every hand arrived
// as the vision placeholder "live-hand", and since hand history is keyed by
// hand id with an upsert, each hand overwrote the last -- a whole session
// collapsed into a single row.
func (s *StateStabilizer) mintHandID(st *HandState) {
	if st.HandID != "" && st.HandID != placeholderHandID {
		return
	}
	s.handCount++
	st.HandID = fmt.Sprintf("%s-%s-%04d", tableSlug(st.TableID), s.sessionID, s.handCount)
}

// tableSlug reduces a table title to something safe for an identifier.
func tableSlug(tableID string) string {
	if tableID == "" {
		return "table"
	}
	var b strings.Builder
	for _, r := range tableID {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r >= 'A' && r <= 'Z':
			b.WriteRune(r + 32)
		case b.Len() > 0 && b.String()[b.Len()-1] != '-':
			b.WriteByte('-')
		}
		if b.Len() >= 24 {
			break
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		return "table"
	}
	return out
}

// TakeCompletedHand returns the hand that finished at the most recent
// transition and clears it, so each completed hand is reported exactly once.
// Returns nil when no hand has just ended.
func (s *StateStabilizer) TakeCompletedHand() *HandState {
	s.mu.Lock()
	defer s.mu.Unlock()
	done := s.completed
	s.completed = nil
	return done
}

// potsAgree treats two readings as the same pot, allowing for the pot ticking
// up slightly between frames as chips settle.
func potsAgree(a, b float64) bool {
	if b <= 0 {
		return false
	}
	diff := a - b
	if diff < 0 {
		diff = -diff
	}
	return diff <= b*0.02
}

// rejectImpossibleCards blanks any card that cannot coexist with the others.
//
// A deck holds one of each card, so a duplicate is proof that a reading is
// wrong -- and it is proof available without re-examining a single pixel. Live,
// hero's six of spades was read as a second eight of spades, and the engine
// went on to compute equity for a hand that cannot be dealt and recommend a
// call on it. A card contradicted this way is set back to unknown, and because
// unknown slots are filled from later frames, the next clean frame supplies the
// real one.
func rejectImpossibleCards(h *HandState) {
	if h == nil {
		return
	}

	seen := make(map[Card]bool, 7)
	keep := func(c *Card) {
		if c.Rank == 0 {
			return
		}
		if seen[*c] {
			*c = Card{}
			return
		}
		seen[*c] = true
	}

	// Board first: community cards are read from a fixed rack and are the more
	// reliable of the two, so a clash is resolved against hero's hand.
	for i := range h.CommunityCards {
		keep(&h.CommunityCards[i])
	}
	for i := range h.HeroCards {
		keep(&h.HeroCards[i])
	}
	for i := range h.Seats {
		for j := range h.Seats[i].Cards {
			keep(&h.Seats[i].Cards[j])
		}
	}
}

// worthRecording keeps frames of noise out of the hand history: a hand that
// never had a pot or a board is a transition artefact, not a hand played.
func worthRecording(h *HandState) bool {
	return h != nil && (h.Pot > 0 || len(h.CommunityCards) > 0)
}

// Stabilize merges an incoming raw frame state with the previous verified state, filtering out animation glitches and dropouts.
func (s *StateStabilizer) Stabilize(raw *HandState) *HandState {
	if raw == nil {
		s.mu.RLock()
		defer s.mu.RUnlock()
		return s.currentHand
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()

	// Initial state
	if s.currentHand == nil {
		st := cloneHandState(raw)
		rejectImpossibleCards(st)
		s.mintHandID(st)
		// Badges on screen at the first frame are actions already taken, the
		// same as at any other hand boundary. Only the recognised-hand path
		// seeded them, so the first hand of every session began blind.
		st.ActionHistory = newActions(&HandState{}, st.Seats, st.Street)
		s.currentHand = st
		s.lastUpdateAt = now
		return s.currentHand
	}

	prev := s.currentHand

	// A terminal state is authoritative and passes through untouched. Merging
	// it would recompute Street from the board and silently demote showdown to
	// river, which is what stops a hand ever being persisted or profiled.
	if raw.Street == StreetShowdown {
		st := cloneHandState(raw)
		rejectImpossibleCards(st)
		// The placeholder counts as no id at all: treating it as one saved every
		// showdown hand under the same key, so each overwrote the last.
		if st.HandID == "" || st.HandID == placeholderHandID {
			st.HandID = prev.HandID
		}
		if st.HandID == "" || st.HandID == placeholderHandID {
			st.HandID = ""
			s.mintHandID(st)
		}
		if st.TableID == "" {
			st.TableID = prev.TableID
		}
		if len(st.CommunityCards) < len(prev.CommunityCards) {
			st.CommunityCards = append([]Card(nil), prev.CommunityCards...)
		}
		if len(st.Seats) == 0 {
			st.Seats = append([]SeatState(nil), prev.Seats...)
		}
		// A showdown is where the hand record is built, and a record whose hero
		// is the placeholder attributes the result to nobody.
		if !seatedIn(st.Seats, st.HeroID) && seatedIn(st.Seats, prev.HeroID) {
			st.HeroID = prev.HeroID
		}
		// The stake is a property of the table, not of the hand.
		if st.SmallBlind == 0 {
			st.SmallBlind = prev.SmallBlind
		}
		if st.BigBlind == 0 {
			st.BigBlind = prev.BigBlind
		}
		s.currentHand = st
		s.lastUpdateAt = now
		return s.currentHand
	}

	// 1. Check if a brand new hand has started.
	isNewHand := false
	switch {
	case raw.HandID != "" && raw.HandID != placeholderHandID && prev.HandID != "" && raw.HandID != prev.HandID:
		// The client gave us a hand id and it changed -- the strongest signal
		// there is, so it is checked before any heuristic.
		isNewHand = true
	case prev.Street == StreetShowdown && raw.Street != StreetShowdown:
		isNewHand = true
	case len(prev.CommunityCards) > 0 && len(raw.CommunityCards) == 0:
		// The board was cleared. Thresholds on the absolute pot size are
		// deliberately avoided: they are stake-dependent and were wrong at
		// anything but the stake they were tuned at.
		//
		// A shrinking pot confirms it immediately. It is not required, though,
		// and requiring it was a live freeze: a hand ended on the river with a
		// pot of 8,920, the next hand's blinds and limps came to the same 8,920,
		// and "less than" is false on equal numbers. The old hand stayed in
		// progress -- hero had folded it, so the guard against advising a folded
		// hand silenced the panel for the whole of the next one, while hero sat
		// with a live decision and a running clock.
		//
		// Without the pot, the board clearing is confirmed the way every other
		// transition here is confirmed: by surviving one more frame. That costs
		// a frame against a vision dropout that reads no board at all, and a
		// dropout is exactly what the confirmation is there to absorb.
		if raw.Pot < prev.Pot {
			isNewHand = true
		} else {
			s.pendingBoardClearSeen++
			if s.pendingBoardClearSeen >= 2 {
				isNewHand = true
			}
		}
	case heroCardsBothKnown(prev.HeroCards) && heroCardsBothKnown(raw.HeroCards) && !sameHoleCards(prev.HeroCards, raw.HeroCards):
		// Hero was dealt different cards, so this is the next hand -- but only
		// once the same different cards have been seen twice.
		//
		// Unconfirmed, this was the most destructive misread in the pipeline. A
		// single frame that read both hole cards wrongly did not merely produce
		// bad advice for that frame: it ended the hand in progress, recorded it
		// as complete, and opened a new one. The hand came back the next frame
		// and was recorded a second time, so one live hand reached the database
		// as two, each with a different holding. The pot already waits for a
		// second frame for exactly this reason a few lines below; hole cards
		// are at least as easy to misread and were not waiting for anything.
		if sameHoleCards(s.pendingHero, raw.HeroCards) {
			s.pendingHeroSeen++
		} else {
			s.pendingHero = raw.HeroCards
			s.pendingHeroSeen = 1
		}
		if s.pendingHeroSeen >= 2 {
			isNewHand = true
		}
	default:
		// Any frame that agrees with the hand in progress withdraws a pending
		// change: two disagreeing frames have to be consecutive to count.
		if heroCardsBothKnown(raw.HeroCards) {
			s.pendingHero = [2]Card{}
			s.pendingHeroSeen = 0
		}
	}
	if len(raw.CommunityCards) > 0 {
		s.pendingBoardClearSeen = 0
	}

	// A pot only grows within a hand, so a pot that shrinks means the next hand
	// has begun. This is the only signal available when a hand ends before the
	// flop: with no board to clear, the rule above cannot fire, and the state
	// carried across into the next hand and stuck there.
	//
	// The drop must be seen twice: a single frame that misreads the pot low
	// would otherwise split one hand in two.
	if !isNewHand && raw.Pot > 0 && raw.Pot < prev.Pot*0.6 {
		s.pendingDropSeen++
		if s.pendingDropSeen >= 2 {
			isNewHand = true
		}
	} else if raw.Pot >= prev.Pot*0.6 {
		s.pendingDropSeen = 0
	}

	if isNewHand {
		s.pendingDropSeen = 0
		s.pendingPot = 0
		s.pendingPotSeen = 0
		s.pendingHero = [2]Card{}
		s.pendingHeroSeen = 0
		s.pendingBoardClearSeen = 0

		// The hand that was in progress is now over. Handing it back is the
		// only end-of-hand signal this pipeline has.
		if worthRecording(prev) {
			s.completed = cloneHandState(prev)
		}

		st := cloneHandState(raw)
		rejectImpossibleCards(st)
		st.HandID = ""
		s.mintHandID(st)
		if st.Street == "" {
			st.Street = streetForBoard(len(st.CommunityCards))
		}

		// Hero does not become a different person between hands at the same
		// table. Without this the identity is rebuilt from the raw frame every
		// deal, and the raw frame only names hero once the hole cards read --
		// which is a beat or two into preflop, exactly where the chart matters
		// most and where losing hero's position silently hands the decision to
		// the expected-value comparison.
		if !seatedIn(st.Seats, st.HeroID) && seatedIn(st.Seats, prev.HeroID) {
			st.HeroID = prev.HeroID
		}

		// Badges already on the nameplates when the hand is recognised are
		// actions that have already happened in it, not the background against
		// which later ones are measured.
		//
		// A hand is only recognised once the evidence is in -- a pot drop has
		// to be seen twice, hole cards likewise -- so recognition lags the deal
		// by two or three frames, and at the capture rate that is most of a
		// second. The opening raise routinely lands inside that window. It was
		// then baked into the baseline and never emitted, and the player who
		// made it looked like they had done nothing.
		//
		// Measured by replaying a recorded session (cmd/replay): of 107 hands,
		// 18 recorded no action whatever, and seeding the badges present at
		// recognition recovers 5 of them. It does not recover a missing preflop
		// raise -- the action stream keeps 38 of the 42 it is given -- so the
		// remaining gap is upstream, in how often a raise badge is read at all.
		seeded := newActions(&HandState{}, st.Seats, st.Street)
		st.ActionHistory = seeded

		// Those same badges also produced a tail on the hand that just ended:
		// they were on screen for the two or three frames before this hand was
		// recognised, and were read there as actions in the old one. So the
		// tail is trimmed off before it is recorded.
		//
		// Trimming can cost a real final action when the last thing to happen
		// in one hand looks exactly like the first thing in the next. That is
		// the better error of the two: an action under-counted is a smaller lie
		// than an action invented in a hand where nobody made it.
		if s.completed != nil {
			s.completed.ActionHistory = trimSeededTail(s.completed.ActionHistory, seeded)
			s.recordHand(s.completed)
		}

		s.currentHand = st
		s.lastUpdateAt = now
		return s.currentHand
	}

	// 2. Merge Community Board Cards (Monotonic Growth)
	mergedBoard := make([]Card, len(prev.CommunityCards))
	copy(mergedBoard, prev.CommunityCards)

	if len(raw.CommunityCards) > len(prev.CommunityCards) {
		// New cards dealt on turn or river
		mergedBoard = make([]Card, len(raw.CommunityCards))
		copy(mergedBoard, raw.CommunityCards)
	} else if len(raw.CommunityCards) == len(prev.CommunityCards) && len(raw.CommunityCards) > 0 {
		// Update cards if new detection has valid rank and prev was unknown
		for i := 0; i < len(raw.CommunityCards); i++ {
			if raw.CommunityCards[i].Rank > 0 {
				mergedBoard[i] = raw.CommunityCards[i]
			}
		}
	}

	// 3. Merge Hero Hole Cards. Each slot fills independently: one card
	// resolving a frame before the other is the normal case, and only handling
	// slot 0 left the second card stuck unknown for the whole hand.
	mergedHero := prev.HeroCards
	switch {
	case !heroCardsBothKnown(prev.HeroCards) && heroCardsBothKnown(raw.HeroCards):
		// Nothing settled yet, so the first complete reading stands.
		mergedHero = raw.HeroCards
	case heroCardsBothKnown(raw.HeroCards):
		// A complete reading that disagrees with the settled hand does not
		// replace it on its own word. Reaching here means the disagreement was
		// not confirmed above, so it was a misread; the settled hand is kept
		// and the next frame decides. Taking the newest reading unconditionally
		// meant one bad frame changed what hero was holding mid-hand, and the
		// advisor sized a bet for a hand hero did not have.
		if sameHoleCards(prev.HeroCards, raw.HeroCards) {
			mergedHero = raw.HeroCards
		}
	default:
		// Each slot fills independently: one card resolving a frame before the
		// other is the normal case, and only handling slot 0 left the second
		// card stuck unknown for the whole hand.
		for i := 0; i < 2; i++ {
			if raw.HeroCards[i].Known() && !mergedHero[i].Known() {
				mergedHero[i] = raw.HeroCards[i]
			}
		}
	}

	// 4. Merge Pot. The pot only grows within a hand, which protects it from
	// the chip animation blanking it out -- but that same rule means one
	// misread frame is permanent. Live, a single frame read 401,920 into a
	// 4,920 pot and the figure stuck for the rest of the session, because
	// nothing may ever lower it again.
	//
	// A new maximum therefore has to be confirmed by a second frame before it
	// is adopted. That costs one frame of latency and makes a spike harmless.
	mergedPot := prev.Pot
	switch {
	case raw.Pot > 0 && prev.Pot == 0:
		mergedPot = raw.Pot
	case raw.Pot > prev.Pot:
		if potsAgree(raw.Pot, s.pendingPot) {
			s.pendingPotSeen++
		} else {
			s.pendingPot = raw.Pot
			s.pendingPotSeen = 1
		}
		if s.pendingPotSeen >= 2 {
			mergedPot = raw.Pot
		}
	default:
		s.pendingPot = 0
		s.pendingPotSeen = 0
	}

	// 5. Merge Seats / Players (Retain player names and stacks)
	mergedSeats := mergeSeats(prev.Seats, raw.Seats)

	// 6. Determine Street from the merged board, but never move backwards
	// within a hand. Vision can miss a board card for a frame; demoting turn
	// back to flop on that frame would make the advisor re-evaluate the hand on
	// the wrong street.
	street := streetForBoard(len(mergedBoard))
	if streetRank(prev.Street) > streetRank(street) {
		street = prev.Street
	}
	if streetRank(raw.Street) > streetRank(street) {
		street = raw.Street
	}

	// Actions are read off the badges the client prints on each nameplate. A
	// badge appearing, or changing, is a player having acted. This is the whole
	// action stream: the profiler counts VPIP, PFR and 3-bets from it, and
	// until it existed every opponent looked like a nit who never voluntarily
	// entered a pot, so every range fell back to random.
	mergedHistory := append([]ActionRecord(nil), prev.ActionHistory...)
	mergedHistory = append(mergedHistory, newActions(prev, mergedSeats, street)...)

	handID := prev.HandID
	if raw.HandID != "" && raw.HandID != placeholderHandID {
		handID = raw.HandID
	}

	mergedState := &HandState{
		HandID:         handID,
		TableID:        raw.TableID,
		Street:         street,
		Pot:            mergedPot,
		CurrentBet:     raw.CurrentBet,
		MinRaise:       raw.MinRaise,
		SmallBlind:     raw.SmallBlind,
		BigBlind:       raw.BigBlind,
		CommunityCards: mergedBoard,
		HeroID:         raw.HeroID,
		HeroCards:      mergedHero,
		Seats:          mergedSeats,
		ActionHistory:  mergedHistory,
		// Straight through from the frame. What the client is offering is a
		// fact about right now, not something to smooth: carrying last frame's
		// buttons forward would be carrying forward permission to check.
		HeroButtons: raw.HeroButtons,
		IsHeroTurn:  raw.IsHeroTurn,
	}

	if mergedState.TableID == "" {
		mergedState.TableID = prev.TableID
	}

	// The stake does not change between frames of a hand, and the title it is
	// read from is not always readable. Measured over a recorded session, blinds
	// resolved on 1028 frames out of 9788 -- nine frames in ten ran with no idea
	// of the stake, and each one of those was a frame with no scale to size or
	// classify anything by.
	if mergedState.SmallBlind == 0 {
		mergedState.SmallBlind = prev.SmallBlind
	}
	if mergedState.BigBlind == 0 {
		mergedState.BigBlind = prev.BigBlind
	}

	// Hero's identity is sticky across frames, the same way the hand id is: the
	// placeholder counts as no id at all rather than as a name, which is why
	// the carry-forward below never used to fire.
	if !seatedIn(mergedState.Seats, mergedState.HeroID) && seatedIn(mergedState.Seats, prev.HeroID) {
		mergedState.HeroID = prev.HeroID
	}

	rejectImpossibleCards(mergedState)

	s.currentHand = mergedState
	s.lastUpdateAt = now
	return s.currentHand
}

// Reset explicitly clears the stabilized state for testing or table change.
func (s *StateStabilizer) Reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.currentHand = nil
}

// placeholderHandID is what the vision agent emits before a real hand id is
// known; it must never be treated as a hand changing.
const placeholderHandID = "live-hand"

// placeholderHeroID is what the vision agent emits when it could not say which
// seat is hero's; like the hand id placeholder it counts as no id at all.
//
// The screen reader names hero only on frames where hero's hole cards actually
// read, because that is how it distinguishes sitting at a table from watching
// one. Hero's chair does not move when hero's cards stop being legible, and
// they stop constantly: a folded hand, a card mid-animation, a corner behind
// the position badge. In the session of 2026-08-31 the placeholder arrived on
// 79% of frames, and each one cost hero's position and hero's stack at once --
// no seat matched, so the preflop chart had no opinion and the effective stack
// was taken from whoever else was at the table, once reading 1,190,000 against
// hero's real 68,000. Measured in the harness that is worth about 50 bb/100,
// more than the other six screen-reader defects put together.
const placeholderHeroID = "Hero"

// seatedIn reports whether a player is at the table. Hero's identity is carried
// between frames only while this holds, or hero would keep the name of whoever
// used to sit in that chair.
func seatedIn(seats []SeatState, id string) bool {
	if id == "" || id == placeholderHeroID {
		return false
	}
	for _, s := range seats {
		if s.PlayerID == id {
			return true
		}
	}
	return false
}

func streetForBoard(cards int) Street {
	switch {
	case cards >= 5:
		return StreetRiver
	case cards == 4:
		return StreetTurn
	case cards >= 1:
		return StreetFlop
	default:
		return StreetPreflop
	}
}

func streetRank(s Street) int {
	switch s {
	case StreetFlop:
		return 1
	case StreetTurn:
		return 2
	case StreetRiver:
		return 3
	case StreetShowdown:
		return 4
	default:
		return 0
	}
}

func heroCardsBothKnown(c [2]Card) bool {
	return c[0].Rank > 0 && c[1].Rank > 0
}

func sameHoleCards(a, b [2]Card) bool {
	return (a[0] == b[0] && a[1] == b[1]) || (a[0] == b[1] && a[1] == b[0])
}

// newActions returns the actions implied by badges that changed since the
// previous frame.
func newActions(prev *HandState, seats []SeatState, street Street) []ActionRecord {
	previous := make(map[string]string, len(prev.Seats))
	for _, s := range prev.Seats {
		previous[s.PlayerID] = s.LastAction
	}

	var out []ActionRecord
	for _, s := range seats {
		if s.PlayerID == "" || s.LastAction == "" {
			continue
		}
		if previous[s.PlayerID] == s.LastAction {
			continue
		}
		act := actionFromBadge(s.LastAction)
		if act == "" {
			continue
		}
		out = append(out, ActionRecord{
			PlayerID: s.PlayerID,
			Street:   street,
			Action:   act,
			Amount:   s.CurrentBet,
		})
	}
	return out
}

func actionFromBadge(badge string) ActionType {
	switch badge {
	case "fold":
		return ActionFold
	case "check":
		return ActionCheck
	case "call":
		return ActionCall
	case "bet":
		return ActionBet
	case "raise":
		return ActionRaise
	case "all-in":
		return ActionAllIn
	default:
		return ""
	}
}

func mergeSeats(prevSeats, rawSeats []SeatState) []SeatState {
	if len(rawSeats) == 0 {
		return prevSeats
	}
	if len(prevSeats) == 0 {
		return rawSeats
	}

	// Keyed by player id, with the display name as a fallback. Keying on the
	// name alone meant a seat whose name had not been read carried nothing
	// forward, so anything the merge is meant to preserve -- stacks, fold
	// state, revealed cards -- was dropped for exactly the seats that needed
	// it most.
	seatKey := func(s SeatState) string {
		if s.PlayerID != "" && s.PlayerID != "Player" {
			return s.PlayerID
		}
		if s.PlayerName != "" && s.PlayerName != "Player" {
			return s.PlayerName
		}
		return ""
	}

	seatMap := make(map[string]SeatState)
	for _, s := range prevSeats {
		if k := seatKey(s); k != "" {
			seatMap[k] = s
		}
	}

	res := make([]SeatState, 0, len(rawSeats))
	seen := make(map[string]bool, len(rawSeats))

	for _, r := range rawSeats {
		merged := r
		if p, exists := seatMap[seatKey(r)]; exists {
			if r.Stack == 0 && p.Stack > 0 {
				merged.Stack = p.Stack
			}
			if r.PlayerID == "" || r.PlayerID == "Player" {
				merged.PlayerID = p.PlayerID
			}
			// A badge that flickers out for a frame must not un-fold a player:
			// folding is not undone within a hand.
			if r.LastAction == "" && p.LastAction != "" {
				merged.LastAction = p.LastAction
			}
			// Cards stay revealed once shown. A showdown lasts only a few
			// frames before the client clears the table, and losing it would
			// lose the one moment an opponent's actual holding is observable.
			if len(r.Cards) == 0 && len(p.Cards) > 0 {
				merged.Cards = append([]Card(nil), p.Cards...)
			}
			if p.IsFolded {
				merged.IsFolded = true
			}
			if merged.Position == "" {
				merged.Position = p.Position
			}
		}
		res = append(res, merged)
		if k := seatKey(merged); k != "" {
			seen[k] = true
		}
	}

	// Players do not leave the table in the middle of a hand. A nameplate the
	// recogniser missed for one frame used to drop the player from the state
	// entirely, and since the live opponent count now drives both the equity
	// simulation and the EV formula, that turned a three-way all-in into a
	// heads-up one: pocket threes scored 53% instead of 24%, and the tool
	// called off a whole stack.
	for _, p := range prevSeats {
		k := seatKey(p)
		if k == "" || seen[k] {
			continue
		}
		res = append(res, p)
	}

	return res
}

func cloneHandState(h *HandState) *HandState {
	if h == nil {
		return nil
	}
	c := *h
	c.CommunityCards = make([]Card, len(h.CommunityCards))
	copy(c.CommunityCards, h.CommunityCards)
	c.Seats = make([]SeatState, len(h.Seats))
	copy(c.Seats, h.Seats)
	c.ActionHistory = make([]ActionRecord, len(h.ActionHistory))
	copy(c.ActionHistory, h.ActionHistory)
	return &c
}

// recordHand writes everything the stabiliser concluded about a finished hand.
//
// Emitted at the end rather than as it happens, and that is the whole design.
// A hand is recognised two or three frames after it is dealt -- the pot drop
// has to be confirmed, and so do the hole cards -- and whatever players did
// inside that window is on screen before the hand it belongs to exists. Written
// as it happened, those actions went into the previous hand and then again into
// this one: the same open recorded twice, against two different hands, one of
// them wrong.
//
// Written at the end, an event says what the stabiliser finally concluded, and
// says it once. The cost is that the log lags by a hand, which matters to
// nothing: statistics do not care, and a hand is over in a minute.
func (s *StateStabilizer) recordHand(h *HandState) {
	if h == nil {
		return
	}
	s.seq = 0

	s.record(HandEvent{
		TableID: h.TableID, HandID: h.HandID,
		Kind: EventHandStart, Street: h.Street, Board: h.CommunityCards,
		PotBefore: FromFloat(h.Pot),
	})
	for _, seat := range h.Seats {
		if seat.PlayerID == "" {
			continue
		}
		s.record(HandEvent{
			TableID: h.TableID, HandID: h.HandID,
			Kind: EventHandStart, Street: h.Street,
			PlayerID: seat.PlayerID, PlayerName: seat.PlayerName,
			Position: seat.Position, Amount: FromFloat(seat.Stack),
		})
	}

	// Amounts come across as exact money. What a player wagered is the whole
	// difference between a minimum raise and a shove, and a statistic built on
	// "raise" without "how much" cannot tell them apart.
	for _, a := range h.ActionHistory {
		if a.PlayerID == "" {
			continue
		}
		var position Position
		var name string
		for _, seat := range h.Seats {
			if seat.PlayerID == a.PlayerID {
				position, name = seat.Position, seat.PlayerName
				break
			}
		}
		s.record(HandEvent{
			TableID: h.TableID, HandID: h.HandID,
			Kind: EventAction, Street: a.Street,
			PlayerID: a.PlayerID, PlayerName: name, Position: position,
			Action: a.Action, Amount: FromFloat(a.Amount),
			PotBefore: FromFloat(h.Pot), Board: h.CommunityCards,
		})
	}

	// A showdown has a board. Cards attributed to a seat with no board are not
	// a reveal -- nobody has shown anything -- and recording one puts a holding
	// in the log that was never on display.
	//
	// Showdowns are the most valuable thing here: frequencies say how often
	// someone bets, a showdown says what they were betting with. A quarter of
	// an hour of watching yields three or four, and each is worth more than the
	// frequencies gathered beside it.
	if len(h.CommunityCards) < 3 {
		return
	}
	for _, seat := range h.Seats {
		if seat.PlayerID == "" || len(seat.Cards) == 0 {
			continue
		}
		known := 0
		for _, c := range seat.Cards {
			if c.Known() {
				known++
			}
		}
		if known == 0 {
			continue
		}
		s.record(HandEvent{
			TableID: h.TableID, HandID: h.HandID,
			Kind: EventReveal, Street: h.Street,
			PlayerID: seat.PlayerID, PlayerName: seat.PlayerName,
			Position: seat.Position, Cards: seat.Cards, Board: h.CommunityCards,
			PotBefore: FromFloat(h.Pot),
		})
	}
}

// trimSeededTail removes from a finished hand the trailing actions that belong
// to the hand which has just been recognised.
//
// A hand is recognised two or three frames after it is dealt, and the badges of
// the new hand are on the nameplates throughout that window. Read there, they
// went into the hand that was still current -- so the same open appears at the
// end of one hand and the start of the next, and the first of the two never
// happened.
func trimSeededTail(history, seeded []ActionRecord) []ActionRecord {
	if len(history) == 0 || len(seeded) == 0 {
		return history
	}
	wanted := map[string]int{}
	for _, a := range seeded {
		wanted[string(a.Action)+"|"+a.PlayerID]++
	}

	cut := len(history)
	for cut > 0 && len(history)-cut < len(seeded) {
		a := history[cut-1]
		key := string(a.Action) + "|" + a.PlayerID
		if wanted[key] == 0 {
			break
		}
		wanted[key]--
		cut--
	}
	return history[:cut]
}
