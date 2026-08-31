// Package sim plays whole hands of no-limit hold'em so that a strategy can be
// judged by what it wins rather than by whether its individual answers look
// sensible.
//
// The advisor is tested spot by spot: given this board and this bet, does it
// say something defensible. That is necessary and it is not sufficient. A
// strategy is a sequence of decisions with money flowing between them, and the
// only question that matters -- would somebody following this advice come out
// ahead -- cannot be answered one spot at a time. It needs opponents, a deck,
// blinds that keep coming, and enough hands for the answer to mean anything.
//
// So the engine here is a real one: side pots, minimum raises, all-ins that do
// not reopen the action, showdowns settled by the same evaluator the equity
// model uses. Every seat is filled by an Agent, and the tool itself is one of
// them (see brain.go). What each Agent is handed is a table.HandState built to
// look exactly like what the screen reader produces from that seat, so the
// strategy under test is reached through the same door it is reached through
// live.
package sim

import (
	"fmt"
	"math/rand"
	"sort"

	"poker-game-analyzer/pkg/evaluator"
	"poker-game-analyzer/pkg/table"
)

// Chips is an exact amount in the table's smallest unit.
//
// Integers, not floats, for the reason table.Money exists: a pot accumulated
// over a hand out of thirds and two-thirds of previous pots drifts, and a
// harness whose whole output is a sum of net results cannot afford to drift.
// The unit is a hundredth of a big blind, which is finer than any client
// allows, so nothing the engine does needs rounding beyond the point where an
// Agent names a size.
type Chips int64

// Config is the stake and the rules. Blinds are in Chips; BigBlind also fixes
// what one unit means when amounts are shown to an Agent, so a big blind is
// always 1.0 in a HandState whatever the stake.
type Config struct {
	SmallBlind Chips
	BigBlind   Chips
	Ante       Chips

	// PotHidesStreetBets makes the pot figure in a HandState exclude the chips
	// currently out in front of players, the way some clients draw it.
	//
	// It is a switch rather than a decision because it changes every pot-odds
	// comparison the advisor makes, and which of the two the screen reader
	// actually produces is a question about the client, not about strategy.
	// Being able to measure the cost of getting it wrong is the point.
	PotHidesStreetBets bool
}

// DefaultConfig is a 0.50/1.00 table, one unit of Chips being a hundredth of a
// big blind.
func DefaultConfig() Config {
	return Config{SmallBlind: 50, BigBlind: 100}
}

// Move is what an Agent decides to do.
//
// Amount is the number of chips put in *now*, on top of whatever this player
// already has out this street -- not the total the bet is raised to. That is
// the convention advisor.ActionRecommendation already uses (evRaise adds
// raiseTo to the pot and asks callers for raiseTo-toCall), and having one
// convention throughout is worth more than having the more usual one.
type Move struct {
	Action table.ActionType
	Amount float64
}

// Spot is a player's turn to act: what they can see, and what the rules allow.
type Spot struct {
	// State is the table as the screen reader would report it from this seat:
	// HeroID and HeroCards are this player's, every other holding is absent.
	State table.HandState

	// ToCall, MinRaise and MaxRaise are all incremental -- chips added now.
	// MinRaise is zero when raising is not allowed at all (the action was
	// closed by a short all-in).
	ToCall   float64
	MinRaise float64
	MaxRaise float64

	// Rng is the hand's randomness, handed to the Agent so that a mixed
	// strategy stays reproducible from the harness seed.
	Rng *rand.Rand
}

// Agent fills a seat.
type Agent interface {
	Name() string
	Act(Spot) Move
}

// Watcher is an Agent that learns from what it sees.
//
// The tool is one, and it has to be, because a tool sitting in an opponent's
// chair -- which is how a strategy is tested against itself -- needs the same
// stream of observations the live profiler would be gathering for it. Returning
// nil means the agent learns nothing, which is every bot in bots.go.
type Watcher interface {
	Observer() Observer
}

// Observer watches a table. The harness uses it to attribute money to the
// decisions that produced it; nothing in the engine depends on it.
type Observer interface {
	OnDecision(DecisionRecord)
	OnHandEnd(HandResult)
}

// DecisionRecord is one turn: what was seen, and what was done about it.
type DecisionRecord struct {
	HandID   string
	Seat     int
	PlayerID string
	Agent    string
	Street   table.Street
	Spot     Spot
	Move     Move
	// Invested is the chips this move actually put in, after the engine
	// legalised it.
	Invested Chips
}

// HandResult is the settled hand.
type HandResult struct {
	HandID   string
	Button   int
	Board    []table.Card
	Pot      Chips
	Showdown bool
	// Net is each player's change in stack over the hand, blinds included.
	Net map[string]Chips
	// AdjNet is the same, with any all-in scored by the equity it had when the
	// last chip went in rather than by the card that came. Nil when the hand
	// needed no adjustment -- it was decided by folding, or the money went in
	// on the river. See allin_ev.go for why this exists.
	AdjNet map[string]float64
	// Holes is what everyone was dealt, which only the harness may see.
	Holes map[string][2]table.Card
	// Positions is where everyone sat this hand.
	Positions map[string]table.Position
}

// Player is a seat's occupant, and its stack persists across hands.
type Player struct {
	ID    string
	Name  string
	Agent Agent
	Stack Chips
}

// Table deals hands to a fixed set of players, rotating the button.
type Table struct {
	cfg      Config
	players  []*Player
	button   int
	rng      *rand.Rand // deals the cards, and nothing else
	spotRng  *rand.Rand // handed to agents for mixed strategies
	handNum  int
	observer Observer
	id       string
}

// NewTable seats the players. The button starts to the left of seat zero, so
// the first hand puts seat zero under the gun at a full table.
//
// The supplied rng deals the cards and does nothing else. That separation is
// what makes two runs comparable: the same seed produces the same decks
// whatever the players do with them, so two strategies can be measured against
// each other on identical cards instead of on independent samples. Agents that
// need randomness get spotRng, which is derived from the same seed but consumed
// at a rate that depends on how the hand goes.
func NewTable(id string, cfg Config, players []*Player, rng *rand.Rand) *Table {
	return &Table{
		cfg: cfg, players: players, rng: rng, id: id, button: len(players) - 1,
		spotRng: rand.New(rand.NewSource(rng.Int63())),
	}
}

// SetObserver installs the watcher. One at a time; the harness needs no more.
func (t *Table) SetObserver(o Observer) { t.observer = o }

// Reseat replaces the occupant of a seat between hands, which is what happens
// when somebody gets up and a stranger sits down. The chips stay with the seat.
func (t *Table) Reseat(seat int, p *Player) {
	if seat < 0 || seat >= len(t.players) {
		return
	}
	t.players[seat] = p
}

// Players exposes the seats, stacks included.
func (t *Table) Players() []*Player { return t.players }

// unit converts engine chips to what an Agent is shown: big blinds.
func (t *Table) unit(c Chips) float64 { return float64(c) / float64(t.cfg.BigBlind) }

// chips converts an Agent's amount back, rounding to the nearest chip. An
// Agent that names 3.333 big blinds is asking for a payable amount, and the
// engine decides which one that is.
func (t *Table) chips(v float64) Chips {
	x := v * float64(t.cfg.BigBlind)
	if x < 0 {
		return 0
	}
	return Chips(x + 0.5)
}

// seatState is a player's standing within one hand.
type seatState struct {
	p       *Player
	seat    int
	hole    [2]table.Card
	street  Chips // committed this street
	total   Chips // committed this hand
	folded  bool
	allIn   bool
	acted   bool   // has acted since the last aggression on this street
	last    string // the badge on the nameplate
	pos     table.Position
	startBB Chips // stack at the start of the hand, for the net result
}

// positionsFor names the seats clockwise from the button.
//
// Only the six-handed row is a real chart position; the shorter tables name
// what they can and leave the rest as the closest equivalent, because the
// preflop charts are written for six.
func positionsFor(n int) []table.Position {
	switch n {
	case 2:
		return []table.Position{table.PosBTN, table.PosBB}
	case 3:
		return []table.Position{table.PosBTN, table.PosSB, table.PosBB}
	case 4:
		return []table.Position{table.PosBTN, table.PosSB, table.PosBB, table.PosCO}
	case 5:
		return []table.Position{table.PosBTN, table.PosSB, table.PosBB, table.PosUTG, table.PosCO}
	default:
		return []table.Position{table.PosBTN, table.PosSB, table.PosBB, table.PosUTG, table.PosMP, table.PosCO}
	}
}

type hand struct {
	t     *Table
	cfg   Config
	seats []*seatState
	board []table.Card
	deck  []table.Card
	di    int

	pot        Chips // everything committed this hand, all streets
	currentBet Chips // the largest street commitment anyone has made
	minRaiseBy Chips // the smallest legal raise increment right now
	reopened   bool  // whether the last aggression reopened the betting
	street     table.Street
	history    []table.ActionRecord
	id         string

	// Where the betting ended with cards still to come, for the all-in
	// adjusted accounting in allin_ev.go. The board and the deck position are
	// taken before the rest is dealt, so the completions enumerated there are
	// exactly the cards the runout was drawn from.
	allInMarked bool
	allInBoard  []table.Card
	allInDeck   int
}

// PlayHand deals and settles one hand, returning what everybody won or lost.
//
// Players with no chips are sat out rather than dealt in; refilling them is the
// harness's business, not the engine's.
func (t *Table) PlayHand() HandResult {
	t.handNum++
	t.button = (t.button + 1) % len(t.players)

	h := &hand{
		t:      t,
		cfg:    t.cfg,
		street: table.StreetPreflop,
		id:     fmt.Sprintf("%s-%d", t.id, t.handNum),
		deck:   shuffledDeck(t.rng),
	}

	// Seats in clockwise order starting from the button, which is the order
	// positions are named in and the order the action moves in.
	n := len(t.players)
	names := positionsFor(n)
	for k := 0; k < n; k++ {
		p := t.players[(t.button+k)%n]
		s := &seatState{p: p, seat: (t.button + k) % n, pos: names[k], startBB: p.Stack}
		if p.Stack <= 0 {
			s.folded = true
		}
		h.seats = append(h.seats, s)
	}

	res := HandResult{
		HandID:    h.id,
		Button:    t.button,
		Net:       make(map[string]Chips, n),
		Holes:     make(map[string][2]table.Card, n),
		Positions: make(map[string]table.Position, n),
	}
	for _, s := range h.seats {
		res.Positions[s.p.ID] = s.pos
	}

	if h.livePlayers() < 2 {
		for _, s := range h.seats {
			res.Net[s.p.ID] = 0
		}
		return res
	}

	h.deal()
	h.postBlinds()

	// Preflop is opened by the first seat after the big blind; at two-handed
	// that is the button.
	first := 3
	if n == 2 {
		first = 0
	}
	h.bettingRound(first % n)
	h.markAllIn()

	for _, street := range []table.Street{table.StreetFlop, table.StreetTurn, table.StreetRiver} {
		if h.livePlayers() < 2 {
			break
		}
		h.street = street
		switch street {
		case table.StreetFlop:
			h.board = append(h.board, h.draw(), h.draw(), h.draw())
		default:
			h.board = append(h.board, h.draw())
		}
		h.resetStreet()
		if h.canStillBet() {
			// Postflop the action starts to the left of the button.
			h.bettingRound(1 % n)
		}
		h.markAllIn()
	}

	res.AdjNet = h.adjustedNets()
	h.settle(&res)

	for _, s := range h.seats {
		res.Net[s.p.ID] = s.p.Stack - s.startBB
		res.Holes[s.p.ID] = s.hole
	}
	res.Board = h.board
	res.Pot = h.pot

	if t.observer != nil {
		t.observer.OnHandEnd(res)
	}
	return res
}

func shuffledDeck(rng *rand.Rand) []table.Card {
	deck := make([]table.Card, 0, 52)
	for r := table.RankTwo; r <= table.RankAce; r++ {
		for s := table.Spades; s <= table.Clubs; s++ {
			deck = append(deck, table.Card{Rank: r, Suit: s})
		}
	}
	rng.Shuffle(len(deck), func(i, j int) { deck[i], deck[j] = deck[j], deck[i] })
	return deck
}

func (h *hand) draw() table.Card {
	c := h.deck[h.di]
	h.di++
	return c
}

// deal gives everybody two cards, sat-out seats included.
//
// Dealing only to the players in the hand would be more realistic and would
// wreck the measurement: the number of cards taken off the deck would depend on
// who happened to have chips, so the board on hand nine hundred would differ
// between two strategies for no reason but that one of them had busted somebody
// earlier. Twelve cards always come off, so hand i deals hero the same holding
// and runs out the same board in every run from the same seed.
func (h *hand) deal() {
	for round := 0; round < 2; round++ {
		for _, s := range h.seats {
			s.hole[round] = h.draw()
		}
	}
}

// livePlayers counts seats with chips that were dealt in.
func (h *hand) livePlayers() int {
	n := 0
	for _, s := range h.seats {
		if !s.folded {
			n++
		}
	}
	return n
}

// canStillBet reports whether more than one player has chips left to wager.
// With one or none, the remaining streets are dealt out without betting.
func (h *hand) canStillBet() bool {
	n := 0
	for _, s := range h.seats {
		if !s.folded && !s.allIn {
			n++
		}
	}
	return n >= 2
}

func (h *hand) postBlinds() {
	n := len(h.seats)
	if h.cfg.Ante > 0 {
		for _, s := range h.seats {
			if !s.folded {
				h.commit(s, h.cfg.Ante)
			}
		}
		// Antes are dead money: they belong to the pot, not to anybody's
		// street commitment, so nothing is owed on account of them.
		for _, s := range h.seats {
			s.street = 0
		}
	}

	sb, bb := 1%n, 2%n
	if n == 2 {
		sb, bb = 0, 1
	}
	h.commit(h.seats[sb], h.cfg.SmallBlind)
	h.commit(h.seats[bb], h.cfg.BigBlind)
	h.currentBet = h.seats[bb].street
	if h.seats[sb].street > h.currentBet {
		h.currentBet = h.seats[sb].street
	}
	h.minRaiseBy = h.cfg.BigBlind
	h.reopened = true
}

// commit moves chips from a stack into the pot, capping at what is there.
func (h *hand) commit(s *seatState, amount Chips) Chips {
	if amount > s.p.Stack {
		amount = s.p.Stack
	}
	if amount <= 0 {
		return 0
	}
	s.p.Stack -= amount
	s.street += amount
	s.total += amount
	h.pot += amount
	if s.p.Stack == 0 {
		s.allIn = true
	}
	return amount
}

func (h *hand) resetStreet() {
	for _, s := range h.seats {
		s.street = 0
		s.acted = false
		if !s.folded {
			s.last = ""
		}
	}
	h.currentBet = 0
	h.minRaiseBy = h.cfg.BigBlind
	h.reopened = true
}

// bettingRound runs one street, starting from the given index into h.seats.
func (h *hand) bettingRound(start int) {
	n := len(h.seats)
	idx := start
	for {
		if h.livePlayers() < 2 {
			return
		}
		// Find the next seat that owes the table an answer: one that has not
		// acted since the last aggression, or one that has not matched the bet.
		found := -1
		for k := 0; k < n; k++ {
			j := (idx + k) % n
			s := h.seats[j]
			if s.folded || s.allIn {
				continue
			}
			if !s.acted || s.street < h.currentBet {
				found = j
				break
			}
		}
		if found < 0 {
			return
		}
		h.act(h.seats[found])
		idx = (found + 1) % n
	}
}

func (h *hand) act(s *seatState) {
	toCall := h.currentBet - s.street
	if toCall < 0 {
		toCall = 0
	}
	if toCall > s.p.Stack {
		toCall = s.p.Stack
	}

	// The smallest legal raise puts the bet up by the last raise increment. A
	// player without enough chips for that may still move all in; a player who
	// only faces a short all-in that did not reopen the betting may not raise
	// at all.
	maxRaise := s.p.Stack
	minRaise := toCall + h.minRaiseBy
	if minRaise > maxRaise {
		minRaise = maxRaise
	}
	if !h.reopened && s.acted {
		minRaise, maxRaise = 0, 0
	}
	if maxRaise <= toCall {
		minRaise, maxRaise = 0, 0
	}

	spot := Spot{
		State:    h.viewFor(s),
		ToCall:   h.t.unit(toCall),
		MinRaise: h.t.unit(minRaise),
		MaxRaise: h.t.unit(maxRaise),
		Rng:      h.t.spotRng,
	}
	move := s.p.Agent.Act(spot)
	invested := h.apply(s, move, toCall, minRaise, maxRaise)

	if h.t.observer != nil {
		h.t.observer.OnDecision(DecisionRecord{
			HandID: h.id, Seat: s.seat, PlayerID: s.p.ID, Agent: s.p.Agent.Name(),
			Street: h.street, Spot: spot, Move: move, Invested: invested,
		})
	}
}

// apply legalises a Move and puts the chips in. An Agent cannot break the
// rules: an illegal amount is clamped, and an action that is not on offer
// becomes the closest one that is -- folding when nothing is owed becomes a
// check, because throwing a free hand away is a mistake the engine should not
// let an Agent make by accident. It is still allowed to fold facing a bet, of
// course; that is a decision, not an accident.
func (h *hand) apply(s *seatState, m Move, toCall, minRaise, maxRaise Chips) Chips {
	act := m.Action
	amount := h.t.chips(m.Amount)

	switch act {
	case table.ActionAllIn:
		act = table.ActionRaise
		amount = maxRaise
		if maxRaise == 0 {
			amount = s.p.Stack
		}
	case table.ActionBet:
		act = table.ActionRaise
	case table.ActionFold:
		if toCall <= 0 {
			act = table.ActionCheck
		}
	case table.ActionCheck:
		if toCall > 0 {
			act = table.ActionFold
		}
	case table.ActionCall:
		// A call with nothing owed is a check.
		if toCall <= 0 {
			act = table.ActionCheck
		}
	}

	switch act {
	case table.ActionFold:
		s.folded = true
		s.acted = true
		s.last = "fold"
		h.history = append(h.history, table.ActionRecord{PlayerID: s.p.ID, Street: h.street, Action: table.ActionFold})
		return 0

	case table.ActionCheck:
		s.acted = true
		s.last = "check"
		h.history = append(h.history, table.ActionRecord{PlayerID: s.p.ID, Street: h.street, Action: table.ActionCheck})
		return 0

	case table.ActionRaise:
		if maxRaise <= 0 {
			// Raising is not on offer; treat it as a call.
			act = table.ActionCall
			break
		}
		if amount > maxRaise {
			amount = maxRaise
		}
		if amount < minRaise {
			// Too small to be a raise. An Agent that asks for one anyway is
			// asking for the smallest legal raise, not for a call: naming a
			// size is a decision to put money in.
			amount = minRaise
		}
		put := h.commit(s, amount)
		s.acted = true
		raiseBy := s.street - h.currentBet
		if raiseBy >= h.minRaiseBy {
			h.minRaiseBy = raiseBy
			h.reopened = true
			for _, o := range h.seats {
				if o != s && !o.folded && !o.allIn {
					o.acted = false
				}
			}
		} else {
			// A short all-in raises the price without reopening the betting.
			h.reopened = false
			for _, o := range h.seats {
				if o != s && !o.folded && !o.allIn && o.street < s.street {
					o.acted = false
				}
			}
		}
		if s.street > h.currentBet {
			h.currentBet = s.street
		}
		kind := table.ActionRaise
		s.last = "raise"
		if toCall == 0 {
			kind = table.ActionBet
			s.last = "bet"
		}
		if s.allIn {
			s.last = "all-in"
		}
		h.history = append(h.history, table.ActionRecord{PlayerID: s.p.ID, Street: h.street, Action: kind, Amount: h.t.unit(put)})
		return put
	}

	// Call, including a call that is all-in for less.
	put := h.commit(s, toCall)
	s.acted = true
	s.last = "call"
	if s.allIn {
		s.last = "all-in"
	}
	h.history = append(h.history, table.ActionRecord{PlayerID: s.p.ID, Street: h.street, Action: table.ActionCall, Amount: h.t.unit(put)})
	return put
}

// viewFor builds the HandState this player would read off the screen.
func (h *hand) viewFor(s *seatState) table.HandState {
	pot := h.pot
	if h.cfg.PotHidesStreetBets {
		for _, o := range h.seats {
			pot -= o.street
		}
	}

	seats := make([]table.SeatState, 0, len(h.seats))
	for _, o := range h.seats {
		seats = append(seats, table.SeatState{
			SeatNumber: o.seat,
			PlayerID:   o.p.ID,
			PlayerName: o.p.Name,
			Stack:      h.t.unit(o.p.Stack),
			CurrentBet: h.t.unit(o.street),
			IsActive:   true,
			IsFolded:   o.folded,
			Position:   o.pos,
			LastAction: o.last,
		})
	}
	// Seats are reported in table order, as the reader sees them, not in
	// action order.
	sort.Slice(seats, func(i, j int) bool { return seats[i].SeatNumber < seats[j].SeatNumber })

	toCall := h.currentBet - s.street
	if toCall < 0 {
		toCall = 0
	}
	if toCall > s.p.Stack {
		toCall = s.p.Stack
	}

	buttons := []string{"fold"}
	if toCall > 0 {
		buttons = append(buttons, "call")
	} else {
		buttons = append(buttons, "check")
	}
	if s.p.Stack > toCall {
		buttons = append(buttons, "raise")
	}

	minRaise := toCall + h.minRaiseBy
	if minRaise > s.p.Stack {
		minRaise = s.p.Stack
	}

	board := make([]table.Card, len(h.board))
	copy(board, h.board)
	hist := make([]table.ActionRecord, len(h.history))
	copy(hist, h.history)

	return table.HandState{
		HandID:     h.id,
		TableID:    h.t.id,
		Street:     h.street,
		Pot:        h.t.unit(pot),
		CurrentBet: h.t.unit(h.currentBet),
		MinRaise:   h.t.unit(minRaise),
		// The stake, which the screen reader gets from the window title and the
		// engine has always known. Leaving it out was leaving two rules
		// untested: the preflop spot is classified by money against the big
		// blind, and a chart open is sized from it, and both fall silent when
		// it is zero. So the harness was exercising neither, and each was
		// covered only by the unit test that came with it.
		SmallBlind:     h.t.unit(h.cfg.SmallBlind),
		BigBlind:       h.t.unit(h.cfg.BigBlind),
		CommunityCards: board,
		HeroID:         s.p.ID,
		HeroCards:      s.hole,
		Seats:          seats,
		ActionHistory:  hist,
		HeroButtons:    buttons,
		IsHeroTurn:     true,
	}
}

// settle builds the side pots and pays them out.
func (h *hand) settle(res *HandResult) {
	// Everything anybody put in, in layers. A player who folded still paid into
	// the layers below whatever they reached. The same split settles the hand
	// and prices the all-in adjustment, so it lives in one place.
	for _, layer := range h.potLayers() {
		h.award(layer.amount, layer.eligible, res)
	}
}

func (h *hand) award(amount Chips, eligible []*seatState, res *HandResult) {
	winners := eligible
	if len(eligible) > 1 {
		res.Showdown = true
		best := evaluator.HandScore(0)
		winners = nil
		for _, s := range eligible {
			seven := append([]table.Card{s.hole[0], s.hole[1]}, h.board...)
			score, _ := evaluator.Evaluate7(seven)
			switch {
			case score > best:
				best = score
				winners = []*seatState{s}
			case score == best:
				winners = append(winners, s)
			}
		}
	}
	if len(winners) == 0 {
		return
	}
	share := amount / Chips(len(winners))
	odd := amount - share*Chips(len(winners))
	// Odd chips go to the first winner clockwise from the button, which is how
	// every room does it. h.seats is already in that order.
	for _, s := range h.seats {
		for _, w := range winners {
			if w == s {
				extra := Chips(0)
				if odd > 0 {
					extra = 1
					odd--
				}
				s.p.Stack += share + extra
			}
		}
	}
}
