package sim

import (
	"sync"

	"poker-game-analyzer/pkg/table"
)

// What the tool is allowed to know about the players it is up against.
//
// This is not a detail. The advisor's aggressive branch is gated on
// `winEq >= 0.50 || countedReads`, and fold equity above the equilibrium
// baseline needs a read with weight. Live, the server passes tendencies but
// never passes the sample size behind them, so readWeight is handed zero and
// every read is discarded -- which means in practice the tool bluffs never and
// bets only what is already ahead. Whether that costs money, and how much, is
// exactly the kind of question a harness exists to answer, so the level of
// knowledge is a dial rather than a constant.

// ReadLevel is how much the tool is told about its opponents.
type ReadLevel int

const (
	// ReadsOff is a table of strangers: no tendencies at all. The first orbit
	// at any new table, and the state the tool spends most of its life in.
	ReadsOff ReadLevel = iota
	// ReadsStats is what the live server actually supplies: vpip, pfr,
	// three_bet, af and hands_count. Note that observedFoldRate looks for none
	// of those, so this level changes range widths and nothing else.
	ReadsStats
	// ReadsFull adds counted fold frequencies -- fold_to_cbet, fold_to_bet,
	// fold_to_3bet -- which live only ever come from the language model. They
	// are countable from observed actions, and this level is the measurement
	// of what counting them would be worth.
	ReadsFull
)

// Tracker counts what the profiler counts, from the same observations, without
// a database behind it.
type Tracker struct {
	mu    sync.Mutex
	stats map[string]*playerCounts
	// hand is the per-hand scratch, cleared at every hand end.
	hand map[string]*handFlags
}

type playerCounts struct {
	hands int

	vpip     int
	pfr      int
	threeBet int
	// threeBetOpp is hands where somebody raised in front and hero had a
	// chance to raise over it.
	threeBetOpp int

	aggressive int // postflop bets and raises
	passive    int // postflop calls

	foldToCBet, cbetFaced       int
	foldToBet, betFaced         int
	foldToThreeBet, threeBetHit int
	// Facing a raise after the flop. Kept apart from facing a bet because they
	// are answered very differently, and the advisor needs the one that
	// matches the action it is considering.
	foldToRaisePost, raisePostFaced int
	// Facing a raise before the flop, which is what an opener runs into.
	foldToRaisePre, raisePreFaced int
	// How often this player bets when nothing is owed, which is how wide the
	// range they bet with actually is.
	betFlop, betFlopSpots int
	betLate, betLateSpots int
}

type handFlags struct {
	vpip, pfr, threeBet, threeBetOpp bool
}

// NewTracker returns an empty tracker.
func NewTracker() *Tracker {
	return &Tracker{stats: make(map[string]*playerCounts), hand: make(map[string]*handFlags)}
}

func (tr *Tracker) counts(id string) *playerCounts {
	c, ok := tr.stats[id]
	if !ok {
		c = &playerCounts{}
		tr.stats[id] = c
	}
	return c
}

func (tr *Tracker) flags(id string) *handFlags {
	f, ok := tr.hand[id]
	if !ok {
		f = &handFlags{}
		tr.hand[id] = f
	}
	return f
}

// OnDecision counts one action.
func (tr *Tracker) OnDecision(d DecisionRecord) {
	tr.mu.Lock()
	defer tr.mu.Unlock()

	c := tr.counts(d.PlayerID)
	f := tr.flags(d.PlayerID)
	act := d.Move.Action
	aggressive := act == table.ActionBet || act == table.ActionRaise || act == table.ActionAllIn

	if d.Street == table.StreetPreflop {
		raisesAhead := 0
		for _, seat := range d.Spot.State.Seats {
			if seat.PlayerID == d.PlayerID || seat.IsFolded {
				continue
			}
			switch seat.LastAction {
			case "raise", "bet", "all-in":
				raisesAhead++
			}
		}
		if aggressive || act == table.ActionCall {
			f.vpip = true
		}
		if aggressive {
			f.pfr = true
		}
		if raisesAhead >= 1 {
			f.threeBetOpp = true
			if aggressive {
				f.threeBet = true
			}
		}
		// Folding to a reraise is a separate, much rarer event than folding to
		// an open, and the advisor asks for it by name on the preflop street.
		if d.Spot.ToCall > 0 && raisesAhead >= 1 {
			c.raisePreFaced++
			if act == table.ActionFold {
				c.foldToRaisePre++
			}
		}
		if raisesAhead >= 2 && d.Spot.ToCall > 0 {
			c.threeBetHit++
			if act == table.ActionFold {
				c.foldToThreeBet++
			}
		}
		return
	}

	if aggressive {
		c.aggressive++
	} else if act == table.ActionCall {
		c.passive++
	}

	if d.Spot.ToCall <= 0 {
		// Nothing owed, so this is a chance to bet, and whether they took it is
		// the measurement of how wide their betting range is.
		if d.Street == table.StreetFlop {
			c.betFlopSpots++
			if aggressive {
				c.betFlop++
			}
		} else {
			c.betLateSpots++
			if aggressive {
				c.betLate++
			}
		}
		return
	}
	aggressorsAhead := 0
	for _, seat := range d.Spot.State.Seats {
		if seat.PlayerID == d.PlayerID || seat.IsFolded {
			continue
		}
		switch seat.LastAction {
		case "raise", "bet", "all-in":
			aggressorsAhead++
		}
	}
	if aggressorsAhead >= 2 {
		c.raisePostFaced++
		if act == table.ActionFold {
			c.foldToRaisePost++
		}
	}
	if d.Street == table.StreetFlop {
		c.cbetFaced++
		if act == table.ActionFold {
			c.foldToCBet++
		}
		return
	}
	c.betFaced++
	if act == table.ActionFold {
		c.foldToBet++
	}
}

// OnHandEnd folds the per-hand flags into the totals.
func (tr *Tracker) OnHandEnd(r HandResult) {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	for id := range r.Net {
		c := tr.counts(id)
		c.hands++
		f, ok := tr.hand[id]
		if !ok {
			continue
		}
		if f.vpip {
			c.vpip++
		}
		if f.pfr {
			c.pfr++
		}
		if f.threeBetOpp {
			c.threeBetOpp++
			if f.threeBet {
				c.threeBet++
			}
		}
	}
	tr.hand = make(map[string]*handFlags, len(r.Net))
}

func ratio(a, b int) float64 {
	if b <= 0 {
		return 0
	}
	return float64(a) / float64(b)
}

// Tendencies returns the read on a player at the requested level, in the same
// shape and with the same keys profiler.GetPlayerTendencies produces -- vpip
// and pfr as percentages, the rest as fractions -- because that is the shape
// the advisor reads.
func (tr *Tracker) Tendencies(id string, level ReadLevel) map[string]float64 {
	if level == ReadsOff {
		return nil
	}
	tr.mu.Lock()
	defer tr.mu.Unlock()
	c, ok := tr.stats[id]
	if !ok || c.hands == 0 {
		return nil
	}
	t := map[string]float64{
		"vpip":        ratio(c.vpip, c.hands) * 100,
		"pfr":         ratio(c.pfr, c.hands) * 100,
		"three_bet":   ratio(c.threeBet, c.threeBetOpp) * 100,
		"af":          ratio(c.aggressive, c.passive),
		"hands_count": float64(c.hands),
	}
	if level >= ReadsFull {
		// Each frequency carries the count behind it, because that count is
		// what the advisor weights the read by. A player can sit for two
		// hundred hands and have faced four raises.
		add := func(key string, folded, faced int) {
			if faced >= 10 {
				t[key] = ratio(folded, faced)
				t[key+"_n"] = float64(faced)
			}
		}
		add("fold_to_cbet", c.foldToCBet, c.cbetFaced)
		add("fold_to_bet", c.foldToBet, c.betFaced)
		add("fold_to_3bet", c.foldToThreeBet, c.threeBetHit)
		add("fold_to_raise", c.foldToRaisePre, c.raisePreFaced)
		add("fold_to_raise_post", c.foldToRaisePost, c.raisePostFaced)
		add("bet_freq_flop", c.betFlop, c.betFlopSpots)
		add("bet_freq_late", c.betLate, c.betLateSpots)
	}
	return t
}

// VPIP is the range width to price an opponent's holdings against, as a
// percentage. Zero when there is nothing counted yet, which the pipeline reads
// as "unknown, so all hands".
func (tr *Tracker) VPIP(id string, level ReadLevel) float64 {
	if level == ReadsOff {
		return 0
	}
	tr.mu.Lock()
	defer tr.mu.Unlock()
	c, ok := tr.stats[id]
	// A width taken from a handful of hands is noise, and it narrows the range
	// every equity number in the decision is measured against.
	if !ok || c.hands < 25 {
		return 0
	}
	return ratio(c.vpip, c.hands) * 100
}
