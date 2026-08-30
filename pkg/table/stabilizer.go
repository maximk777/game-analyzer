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
}

// NewStateStabilizer creates a new StateStabilizer instance.
func NewStateStabilizer() *StateStabilizer {
	return &StateStabilizer{sessionID: time.Now().UTC().Format("20060102-150405")}
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
	case len(prev.CommunityCards) > 0 && len(raw.CommunityCards) == 0 && raw.Pot < prev.Pot:
		// The board was cleared and the pot shrank. Thresholds on the absolute
		// pot size are deliberately avoided: they are stake-dependent and were
		// wrong at anything but the stake they were tuned at.
		isNewHand = true
	case heroCardsBothKnown(prev.HeroCards) && heroCardsBothKnown(raw.HeroCards) && !sameHoleCards(prev.HeroCards, raw.HeroCards):
		isNewHand = true
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
	if heroCardsBothKnown(raw.HeroCards) {
		mergedHero = raw.HeroCards
	} else {
		for i := 0; i < 2; i++ {
			if raw.HeroCards[i].Rank > 0 && mergedHero[i].Rank == 0 {
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
		CommunityCards: mergedBoard,
		HeroID:         raw.HeroID,
		HeroCards:      mergedHero,
		Seats:          mergedSeats,
		ActionHistory:  mergedHistory,
	}

	if mergedState.TableID == "" {
		mergedState.TableID = prev.TableID
	}
	if mergedState.HeroID == "" {
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
