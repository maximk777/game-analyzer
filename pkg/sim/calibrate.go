package sim

import (
	"fmt"

	"poker-game-analyzer/pkg/advice"
	"poker-game-analyzer/pkg/calib"
	"poker-game-analyzer/pkg/table"
)

// Calibrator marks the tool's model of each opponent against the cards the
// engine dealt them.
//
// This is the same measurement pkg/slumbot makes, without its one real defect.
// Slumbot is heads-up at 200 big blinds and the tool's preflop charts are 6-max
// at 100, so a width that comes out too tight there might be the model failing
// or might be a chart answering a question nobody asked. Here the game is the
// game the tool is for and the opponents' ranges are whatever bots.go actually
// deals itself into, so a gap is the model's.
//
// What it deliberately does not do is score the priced call. That statistic --
// "the advisor paid believing they held one of these hands; did they" -- needs
// the fraction the advisor priced against, which is one number for the whole
// decision. In a multiway pot it is a claim about several opponents at once and
// splitting it per opponent would invent a precision that is not there.
type Calibrator struct {
	Set    *calib.Set
	heroID string
	// reads supplies what the tool knew, so the width recomputed here is the
	// width the tool actually used rather than one it never saw. Nil is the
	// reads-off case, which is how the strategy in docs/STRATEGY.md §5 was
	// measured.
	reads func(table.HandState) advice.Reads
	// shape is the candidate's own range model, so a candidate that widens it
	// is marked on what it actually believed rather than on what the default
	// would have believed.
	shape   advice.Shape
	pending map[string][]calibPending
}

type calibPending struct {
	names     []string
	width     float64
	hero      [2]table.Card
	board     []table.Card
	villainID string
}

// NewCalibrator watches one seat's decisions.
func NewCalibrator(heroID string, reads func(table.HandState) advice.Reads, shape advice.Shape) *Calibrator {
	return &Calibrator{
		Set:     calib.NewSet(),
		heroID:  heroID,
		reads:   reads,
		shape:   shape,
		pending: map[string][]calibPending{},
	}
}

// OnDecision records what the model believed about every live opponent.
func (c *Calibrator) OnDecision(d DecisionRecord) {
	if d.PlayerID != c.heroID {
		return
	}
	st := d.Spot.State
	if !st.HeroCards[0].Known() || !st.HeroCards[1].Known() {
		return
	}
	var rd advice.Reads
	if c.reads != nil {
		rd = c.reads(st)
	}

	facing := "checked to"
	if d.Spot.ToCall > 0 {
		facing = "facing a bet"
	}
	board := append([]table.Card(nil), st.CommunityCards...)

	for _, seat := range st.Seats {
		if seat.PlayerID == "" || seat.PlayerID == st.HeroID || !seat.IsActive || seat.IsFolded {
			continue
		}
		vpip, known := rd.RangeWidth[seat.PlayerID]
		known = known && vpip > 0
		width := advice.RangeWidthFor(st, seat, vpip, known, c.shape)
		if width <= 0 {
			continue
		}
		names := []string{
			calib.All,
			fmt.Sprintf("%s, %s", st.Street, facing),
		}
		if last := lastActionOf(st, seat.PlayerID); last != "" && st.Street != table.StreetPreflop {
			names = append(names,
				"after they "+string(last),
				fmt.Sprintf("  %s, after they %s", st.Street, last))
		}
		c.pending[d.HandID] = append(c.pending[d.HandID], calibPending{
			names: names, width: width, hero: st.HeroCards,
			board: board, villainID: seat.PlayerID,
		})
	}
}

// OnHandEnd marks every belief recorded this hand against what was really held.
func (c *Calibrator) OnHandEnd(r HandResult) {
	for _, p := range c.pending[r.HandID] {
		hole, ok := r.Holes[p.villainID]
		if !ok || !hole[0].Known() || !hole[1].Known() {
			continue
		}
		c.Set.Add(calib.Obs{
			Names: p.names, Width: p.width,
			Hero: p.hero, Villain: hole, Board: p.board,
		})
	}
	delete(c.pending, r.HandID)
}

// lastActionOf is a player's most recent action after the flop, which is what
// the shape claims are about: `polar` describes a range that bet, `capped` one
// that called.
func lastActionOf(st table.HandState, id string) table.ActionType {
	var last table.ActionType
	for _, a := range st.ActionHistory {
		if a.PlayerID == id && a.Street != table.StreetPreflop {
			last = a.Action
		}
	}
	return last
}

// trackerReads is what the tracker knows about the players in a state.
//
// Note what state this sees. An Observer is handed the engine's own spot, not
// the corrupted view the tool decided on, so with -noise configured the width
// marked here is the one the model would have formed on a clean table and not
// the one it acted on. That makes calibration a measurement of the model rather
// than of the screen reader, which is what it is for -- but it means a
// calibration run and a -flips run are not asking about the same thing.
func trackerReads(tr *Tracker, st table.HandState, level ReadLevel) advice.Reads {
	reads := advice.Reads{}
	if tr == nil || level == ReadsOff {
		return reads
	}
	reads.Tendencies = make(map[string]map[string]float64)
	reads.RangeWidth = make(map[string]float64)
	for _, seat := range st.Seats {
		if seat.PlayerID == st.HeroID || seat.IsFolded {
			continue
		}
		if td := tr.Tendencies(seat.PlayerID, level); len(td) > 0 {
			reads.Tendencies[seat.PlayerID] = td
		}
		if w := tr.VPIP(seat.PlayerID, level); w > 0 {
			reads.RangeWidth[seat.PlayerID] = w
		}
	}
	return reads
}
