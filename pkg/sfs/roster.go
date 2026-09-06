package sfs

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"poker-game-analyzer/pkg/table"
)

// Seat is the current known truth about one seat, accumulated from game.seat
// messages. It is exactly what OCR spent its effort trying to read, delivered
// by the server without ambiguity: the name in full, the id behind it, the
// stack to the minor unit.
type Seat struct {
	SeatID     int
	UserID     int64
	UserName   string
	Chips      table.Money
	Bet        table.Money
	LastAction string
	Status     string // caption/newCaption: Fold, Raise, Disconnected, Sitout...
	IsPlaying  bool
	// Vpip and HandsPlayed are the server's own per-seat stats, sent with every
	// seat update. Vpip is a percentage (0..100), not a fraction.
	Vpip        float64
	HandsPlayed int
}

// Roster is the live reconstruction of one table from its message stream. It is
// the wire equivalent of what pkg/table/stabilizer produced from frames, minus
// the smoothing -- the wire needs none, because every value here was stated by
// the server rather than inferred from pixels.
type Roster struct {
	// HandID is the current hand, taken from the action history (the bare
	// {"gameId":N} messages are a correlation counter, not the hand id).
	HandID    int64
	Street    string               // roundName: PREFLOP, FLOP, TURN, RIVER
	Seats     map[int]*Seat        // by seatId
	Board     []table.Card         // community cards in dealing order
	HoleCards map[int][]table.Card // by seatId; hero's during play, all at showdown
	Pot       table.Money
	WhoseTurn string

	// Blinds, ante and the button/blind seats, from the hand-start message.
	SmallBlind table.Money
	BigBlind   table.Money
	Ante       table.Money
	DealerSeat int
	SBSeat     int
	BBSeat     int

	// HeroID and HeroName identify the local player, and are configured, not
	// inferred: the per-user settings blob is broadcast for every player at the
	// table, so it cannot say which one is us. Either one finds the hero's seat
	// (the seat carrying this id or name). The hero's own cards do not need it --
	// they arrive on a private channel (KindHeroCards) and are always ours.
	HeroID    int64
	HeroName  string
	HeroCards []table.Card

	// Turn holds the action offer in force -- the legal actions and their
	// amounts -- and TurnFor names the player it was addressed to. An offer is
	// one player's, on one street: it is dropped when the turn passes, when the
	// street ends and when the hand does, so it can never be read as somebody
	// else's buttons.
	Turn    *TurnOptionsMsg
	TurnFor string

	// Actions is the action history of the current hand, as last sent. The wire
	// sends it cumulatively, so the latest message is the whole hand so far.
	Actions []ActionEntry

	// Winners are the seat ids that won this hand, from the showdown message.
	// Cleared at the start of each hand.
	Winners map[int]bool
}

// HeroSeat returns the hero's seat, or nil if the hero is not seated at this
// table (or the hero id is not yet known). The hero is the seat carrying the
// local player's userId, learned from the settings message.
func (r *Roster) HeroSeat() *Seat {
	if r.HeroID == 0 && r.HeroName == "" {
		return nil
	}
	for _, s := range r.Seats {
		if r.HeroID != 0 && s.UserID == r.HeroID {
			return s
		}
		if r.HeroName != "" && s.UserName == r.HeroName {
			return s
		}
	}
	return nil
}

// NewRoster makes an empty roster.
func NewRoster() *Roster {
	return &Roster{Seats: make(map[int]*Seat), HoleCards: make(map[int][]table.Card)}
}

// Apply folds one decoded message into the roster and reports whether anything
// observable changed -- a caller printing on change, or emitting a table event,
// uses that to avoid repeating itself when the server re-sends an unchanged
// seat.
func (r *Roster) Apply(m Message) bool {
	switch m.Kind {
	case KindSeat:
		return r.applySeat(m.Seat)
	case KindPot:
		changed := false
		if r.Pot != m.Pot.TotalPotAmount {
			r.Pot = m.Pot.TotalPotAmount
			changed = true
		}
		if m.Pot.RoundName != "" && r.Street != m.Pot.RoundName {
			r.Street = m.Pot.RoundName
			r.endStreet()
			changed = true
		}
		return changed
	case KindTurn:
		if r.WhoseTurn == m.Turn.WhoseTurn {
			return false
		}
		r.WhoseTurn = m.Turn.WhoseTurn
		// The turn has passed to somebody else, so the offer in hand is spent.
		if r.TurnFor != m.Turn.WhoseTurn {
			r.endTurn()
		}
		return true
	case KindActionHistory:
		return r.applyHistory(m.ActionHistory)
	case KindBoard:
		b := m.Board.Board()
		if len(b) == len(r.Board) {
			return false
		}
		r.Board = b
		return true
	case KindHoleCards:
		changed := false
		for seatStr, ws := range m.HoleCards.Map {
			seat, err := strconv.Atoi(seatStr)
			if err != nil {
				continue
			}
			cards := wireCards(ws)
			if len(cards) == 0 {
				continue
			}
			r.HoleCards[seat] = cards
			changed = true
		}
		return changed
	case KindHandStart:
		return r.applyHandStart(m.HandStart)
	case KindHeroCards:
		c := m.HeroCards.Cards()
		if len(c) == 0 || sameCards(r.HeroCards, c) {
			return false
		}
		r.HeroCards = c
		return true
	case KindSettings:
		// The settings blob is broadcast for every player, so it cannot tell us
		// which one is the hero; the hero is configured, not inferred. Decoded
		// only so it is not left to the scanner as an unknown object.
		return false
	case KindTurnOptions:
		// An offer addressed to nobody is not a turn. The client is also sent a
		// pre-action panel -- check-fold and call-any, ahead of its turn, under
		// its own option codes -- with an empty whoseTurn, and reading that as
		// the hero's buttons left a check on the panel for the rest of the hand:
		// advice on every opponent's turn, priced off a roundMaxBet of zero.
		if m.TurnOptions.WhoseTurn == "" {
			return false
		}
		r.Turn = m.TurnOptions
		r.TurnFor = m.TurnOptions.WhoseTurn
		r.WhoseTurn = m.TurnOptions.WhoseTurn
		return true
	case KindGameInfo:
		// The bare gameId is a correlation counter, not the hand id -- it
		// changes many times a second and means nothing about the table.
		// Recognised only so the scanner does not treat it as unknown.
		return false
	case KindShowdown:
		// The showdown is the hand's end. Marking the street as showdown is what
		// tells the pipeline the hand is over -- to persist it and fold it into
		// the profiles -- rather than to advise on it. The winners are recorded
		// so a showdown can be counted as won, not merely reached.
		if r.Winners == nil {
			r.Winners = make(map[int]bool)
		}
		for _, w := range m.Showdown.Winners {
			r.Winners[w.SeatID] = true
		}
		if r.Street == "SHOWDOWN" {
			return false
		}
		r.Street = "SHOWDOWN"
		r.endStreet()
		return true
	default:
		// Dealer chat carries no roster field the seat, pot and history messages
		// do not already hold; callers that want the words read it directly.
		return false
	}
}

// applyHistory reads the real hand id and street from the action history, and
// starts a new hand when the id changes -- clearing per-hand state but keeping
// the seats, who persist across hands.
func (r *Roster) applyHistory(h *ActionHistoryMsg) bool {
	if len(h.History) == 0 {
		return false
	}
	r.Actions = h.History
	last := h.History[len(h.History)-1]
	changed := false
	if last.HandID != 0 && last.HandID != r.HandID {
		r.HandID = last.HandID
		r.startHand()
		r.Street = ""
		changed = true
	}
	if last.RoundName != "" && r.Street != last.RoundName {
		r.Street = last.RoundName
		r.endStreet()
		changed = true
	}
	return changed
}

// startHand clears everything that belonged to the hand just finished. The
// seats themselves persist -- the players are still there -- but nothing they
// did last hand does.
//
// The action badges are the part that mattered. The wire leaves a seat's last
// caption standing until that seat next changes, so "Fold" from the previous
// hand was still on hero's seat when the new one was dealt; the advisor read it
// and refused to advise -- correctly, for what it had been told -- until hero
// acted and the seat was re-sent. At a cash table a player who is not in the
// blinds is not re-sent before their own turn, which is exactly the first
// decision the advice was wanted for.
func (r *Roster) startHand() {
	r.Pot = table.Zero
	r.WhoseTurn = ""
	r.Board = nil
	r.HoleCards = make(map[int][]table.Card)
	r.HeroCards = nil
	r.Winners = nil
	r.endTurn()
	for _, s := range r.Seats {
		s.Bet = table.Zero
		s.LastAction = ""
		if perHandCaption(s.Status) {
			s.Status = ""
		}
	}
}

// endTurn drops the action offer in force. An offer is one player's, for one
// street: past that it describes neither whose turn it is nor what it costs.
func (r *Roster) endTurn() {
	r.Turn = nil
	r.TurnFor = ""
}

// endStreet drops the offer and forgets whose turn it was. A street cannot end
// while somebody is still to act on it, so the name standing there is the last
// player who acted, not the next one -- and left standing on hero it says hero
// is being asked to act on a street the wire has not asked them about yet.
func (r *Roster) endStreet() {
	r.endTurn()
	r.WhoseTurn = ""
}

// perHandCaption reports whether a seat caption describes what the player did
// this hand, rather than how they are sitting at the table. The first kind is
// cleared when a hand starts; the second -- sitting out, disconnected, waiting
// for a big blind -- outlives the hand and is left alone, because clearing it
// would forget a player is not in the game until the server mentions it again.
func perHandCaption(status string) bool {
	switch strings.ToUpper(status) {
	case "FOLD", "CHECK", "CALL", "BET", "RAISE", "ALLIN", "ALL IN", "ALL-IN",
		"ANTE", "SB", "BB", "POST", "POSTBB", "POST BB", "STRADDLE", "MUCK",
		"WIN", "WINNER", "SHOW":
		return true
	default:
		return false
	}
}

// applyHandStart opens a new hand: it sets the blinds, ante and button/blind
// seats, and clears the per-hand state. It is the authoritative hand boundary --
// its gameId is a real hand id -- so it also resets the board, cards and pot.
func (r *Roster) applyHandStart(h *HandStartMsg) bool {
	r.HandID = h.GameID
	r.SmallBlind = h.SBAmount
	r.BigBlind = h.BBAmount
	r.Ante = h.AnteAmount
	r.DealerSeat = h.DealerSeatID
	r.SBSeat = h.SBSeatID
	r.BBSeat = h.BBSeatID
	r.startHand()
	r.Street = "PREFLOP"
	return true
}

func (r *Roster) applySeat(s *SeatMsg) bool {
	status := s.NewCaption
	if status == "" {
		status = s.Caption
	}
	cur, ok := r.Seats[s.SeatID]
	next := &Seat{
		SeatID:      s.SeatID,
		UserID:      s.UserID,
		UserName:    s.UserName,
		Chips:       s.UserChips,
		Bet:         s.BetAmount,
		LastAction:  s.LastAction,
		Status:      status,
		IsPlaying:   s.IsPlaying,
		Vpip:        s.VpipPercentage,
		HandsPlayed: s.SessionHandsPlayed,
	}
	if ok && *cur == *next {
		return false
	}
	r.Seats[s.SeatID] = next
	return true
}

// Ordered returns the seats sorted by seat id, for a stable render.
func (r *Roster) Ordered() []*Seat {
	out := make([]*Seat, 0, len(r.Seats))
	for _, s := range r.Seats {
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].SeatID < out[j].SeatID })
	return out
}

// String renders the roster as a compact table, the way you would want to see
// it scroll past while a capture runs.
func (r *Roster) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "hand %d  %s  %s/%s  pot %s  board %s  turn %s  hero %s\n",
		r.HandID, r.Street, r.SmallBlind, r.BigBlind, r.Pot,
		cardsStr(r.Board), r.WhoseTurn, cardsStr(r.HeroCards))
	heroSeat := r.HeroSeat()
	for _, s := range r.Ordered() {
		hero := "  "
		if heroSeat != nil && s.SeatID == heroSeat.SeatID {
			hero = "* "
		}
		bet := ""
		if !s.Bet.IsZero() {
			bet = "bet " + s.Bet.String()
		}
		cards := ""
		if hc := r.HoleCards[s.SeatID]; len(hc) > 0 {
			cards = cardsStr(hc)
		}
		fmt.Fprintf(&b, "%ss%d %-18s id=%-8d %12s  vpip%3.0f/%-4d %-12s %-12s %s\n",
			hero, s.SeatID, trunc(s.UserName, 18), s.UserID, s.Chips,
			s.Vpip, s.HandsPlayed, s.Status, bet, cards)
	}
	return b.String()
}

// cardsStr renders a slice of cards compactly, e.g. "Ac Ts".
func cardsStr(cs []table.Card) string {
	if len(cs) == 0 {
		return "-"
	}
	parts := make([]string, len(cs))
	for i, c := range cs {
		parts[i] = c.String()
	}
	return strings.Join(parts, " ")
}

func trunc(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}
