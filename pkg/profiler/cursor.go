package profiler

import (
	"fmt"
	"sort"

	"poker-game-analyzer/pkg/storage"
	"poker-game-analyzer/pkg/table"
)

// StatsCursor folds the event log into per-player counters.
//
// It is a cursor rather than a running total because a running total can only
// ever answer the questions it was written to answer. This repository went
// looking for fold-to-continuation-bet and for who raised second before the
// flop, and neither could be had: the aggregate did not hold them and nothing
// underneath it had been kept. A cursor can be named, reset and run again over
// the same events, so a statistic nobody has thought of yet costs one name.
//
// It also survives a restart, which the running total did not. The old profiler
// built its counters in memory and wrote them out with an upsert of the whole
// row, so a player with fifty recorded hands who played one more came back with
// a hands count of one -- which is why a long session produced reads over five
// and eighteen hands.
type StatsCursor struct {
	db   *storage.SQLiteDB
	name string
}

// Counter names. Kept as constants because a cursor that is reset and replayed
// has to produce the same names it produced before, or the old rows linger
// under a spelling nothing reads.
const (
	CounterHands        = "hands"
	CounterVPIP         = "vpip"
	CounterPFR          = "pfr"
	CounterThreeBet     = "three_bet"
	CounterThreeBetOpp  = "three_bet_opp"
	CounterSawFlop      = "saw_flop"
	CounterCBet         = "cbet"
	CounterCBetOpp      = "cbet_opp"
	CounterFoldToCBet   = "fold_to_cbet"
	CounterFoldToCBetOp = "fold_to_cbet_opp"
	CounterShowdowns    = "showdowns"
	CounterAggressive   = "postflop_aggressive"
	CounterPassive      = "postflop_passive"
)

func NewStatsCursor(db *storage.SQLiteDB) *StatsCursor {
	return &StatsCursor{db: db, name: "stats"}
}

// Run reads what has arrived since last time and folds it in. Returns how many
// hands were counted.
//
// A hand is counted whole or not at all. Its events are written in one batch,
// so they are contiguous, but a read can still end in the middle of one -- so
// the last hand in a full batch is left for the next pass rather than counted
// half-finished.
func (c *StatsCursor) Run(limit int) (int, error) {
	at, err := c.db.CursorAt(c.name)
	if err != nil {
		return 0, fmt.Errorf("reading cursor: %w", err)
	}
	if limit <= 0 {
		limit = 2000
	}
	events, err := c.db.ReadEventsAfter(at, limit)
	if err != nil {
		return 0, fmt.Errorf("reading events: %w", err)
	}
	if len(events) == 0 {
		return 0, nil
	}

	// Group by hand, keeping the order they arrived in.
	order := []string{}
	byHand := map[string][]table.HandEvent{}
	for _, e := range events {
		if _, seen := byHand[e.HandID]; !seen {
			order = append(order, e.HandID)
		}
		byHand[e.HandID] = append(byHand[e.HandID], e)
	}

	// A short read reached the end of the log, so every hand in it is whole.
	// A full one may have stopped inside the last hand.
	if len(events) == limit && len(order) > 1 {
		delete(byHand, order[len(order)-1])
		order = order[:len(order)-1]
	}
	if len(order) == 0 {
		return 0, nil
	}

	var deltas []storage.CounterDelta
	var upTo int64
	for _, handID := range order {
		hand := byHand[handID]
		for _, e := range hand {
			if e.ID > upTo {
				upTo = e.ID
			}
		}
		deltas = append(deltas, foldHand(hand)...)
	}

	if err := c.db.AdvanceCursor(c.name, upTo, deltas); err != nil {
		return 0, fmt.Errorf("advancing cursor: %w", err)
	}
	return len(order), nil
}

// Reset puts the cursor back to the beginning. What it built is not cleared
// here: replaying onto existing counters would double them, so a reset is
// paired with dropping the counters by whoever asked for it.
func (c *StatsCursor) Reset() error { return c.db.ResetCursor(c.name) }

type handPlayer struct {
	vpip, pfr             bool
	threeBet, threeBetOpp bool
	sawFlop               bool
	cbet, cbetOpp         bool
	foldToCBet, faceCBet  bool
	showdown              bool
	aggressive, passive   int
}

func isAggression(a table.ActionType) bool {
	return a == table.ActionBet || a == table.ActionRaise || a == table.ActionAllIn
}

// foldHand turns one hand into counter additions.
func foldHand(events []table.HandEvent) []storage.CounterDelta {
	if len(events) == 0 {
		return nil
	}
	tableKey := events[0].TableKey

	players := map[string]*handPlayer{}
	seat := func(id string) *handPlayer {
		if players[id] == nil {
			players[id] = &handPlayer{}
		}
		return players[id]
	}

	// Everyone dealt in, so a player who folded before acting still counts as
	// having played a hand. Counting only those who acted would make every
	// frequency the frequency among people who did something.
	for _, e := range events {
		if e.Kind == table.EventHandStart && e.PlayerID != "" {
			seat(e.PlayerID)
		}
		if e.Kind == table.EventReveal && e.PlayerID != "" {
			seat(e.PlayerID).showdown = true
		}
	}

	// Preflop, in order.
	var raises int
	var firstRaiser, threeBettor string
	raisedYet := false
	for _, e := range events {
		if e.Kind != table.EventAction || e.Street != table.StreetPreflop {
			continue
		}
		p := seat(e.PlayerID)

		// The chance to three-bet is the chance to raise over a raise. Anyone
		// still to act once someone has raised has it, except the raiser.
		if raisedYet && e.PlayerID != firstRaiser {
			p.threeBetOpp = true
		}

		switch {
		case isAggression(e.Action):
			p.vpip, p.pfr = true, true
			raises++
			if raises == 1 {
				firstRaiser = e.PlayerID
				raisedYet = true
			} else if raises == 2 && threeBettor == "" && e.PlayerID != firstRaiser {
				// A different player, which the old counter did not check: a
				// raiser whose badge flickered and was read twice counted as
				// having three-bet themselves.
				threeBettor = e.PlayerID
			}
		case e.Action == table.ActionCall:
			p.vpip = true
		}
	}
	if threeBettor != "" {
		seat(threeBettor).threeBet = true
	}

	// Postflop.
	var flopBettor string
	firstFlopAction := map[string]table.ActionType{}
	for _, e := range events {
		if e.Kind != table.EventAction || e.Street == table.StreetPreflop {
			continue
		}
		p := seat(e.PlayerID)
		p.sawFlop = true

		if isAggression(e.Action) {
			p.aggressive++
		} else if e.Action == table.ActionCall {
			p.passive++
		}

		if e.Street != table.StreetFlop {
			continue
		}
		if _, seen := firstFlopAction[e.PlayerID]; !seen {
			firstFlopAction[e.PlayerID] = e.Action
		}
		if flopBettor == "" && isAggression(e.Action) {
			flopBettor = e.PlayerID
		}
	}

	// A continuation bet is the preflop raiser betting the flop. The chance to
	// make one is having raised preflop and still being there on the flop.
	if firstRaiser != "" {
		p := seat(firstRaiser)
		if p.sawFlop {
			p.cbetOpp = true
			if flopBettor == firstRaiser {
				p.cbet = true
			}
		}
	}
	if flopBettor != "" && flopBettor == firstRaiser {
		for id, p := range players {
			if id == firstRaiser || !p.sawFlop {
				continue
			}
			p.faceCBet = true
			if firstFlopAction[id] == table.ActionFold {
				p.foldToCBet = true
			}
		}
	}

	// Deterministic order so a replay produces the same rows in the same
	// sequence, which is what makes two runs comparable.
	ids := make([]string, 0, len(players))
	for id := range players {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	var out []storage.CounterDelta
	add := func(id, counter string, v int64) {
		if v == 0 {
			return
		}
		out = append(out, storage.CounterDelta{
			TableKey: tableKey, PlayerID: id, Counter: counter, Value: v,
		})
	}
	b := func(v bool) int64 {
		if v {
			return 1
		}
		return 0
	}
	for _, id := range ids {
		p := players[id]
		add(id, CounterHands, 1)
		add(id, CounterVPIP, b(p.vpip))
		add(id, CounterPFR, b(p.pfr))
		add(id, CounterThreeBet, b(p.threeBet))
		add(id, CounterThreeBetOpp, b(p.threeBetOpp))
		add(id, CounterSawFlop, b(p.sawFlop))
		add(id, CounterCBet, b(p.cbet))
		add(id, CounterCBetOpp, b(p.cbetOpp))
		add(id, CounterFoldToCBet, b(p.foldToCBet))
		add(id, CounterFoldToCBetOp, b(p.faceCBet))
		add(id, CounterShowdowns, b(p.showdown))
		add(id, CounterAggressive, int64(p.aggressive))
		add(id, CounterPassive, int64(p.passive))
	}
	return out
}
