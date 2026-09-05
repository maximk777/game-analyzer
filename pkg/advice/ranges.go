package advice

import (
	"poker-game-analyzer/pkg/table"
)

// What an opponent can still be holding, from what they have done this hand.
//
// Until this file the answer was one number per player -- their VPIP -- and
// nothing in the hand moved it. A player who opened under the gun and then
// four-bet was modelled at the share of hands they enter pots with, which is
// the same share whether they folded, limped or shoved. And a player nobody has
// a read on was modelled at a hundred per cent: a random hand, for every
// decision, on every street.
//
// That is the input every other number in the advisor is derived from.
// `EquityVsTop` cuts the strongest slice of *this* range; `bettorRangeFraction`
// prices a bet against *this* range; the risk profile counts what beats hero
// inside *this* range. Handed a random hand, all three answer a question about
// a game nobody is playing: hero's equity comes out inflated, because a random
// range is mostly junk, and the tool calls too wide and value-bets too thin.
//
// The widths below are the standard 6-max reference at around 100 big blinds --
// an open is 15% under the gun and 43% on the button, a three-bet is around 8%,
// a four-bet around 3%. They are the same kind of outside knowledge as
// pkg/preflop's charts, and they are here for the same reason: the alternative
// is not a better estimate, it is no estimate.
//
// Two things this model deliberately cannot express. It says how *wide* a range
// is and not how it is *shaped*, so a caller's condensed range -- one with the
// nuts raised out of it -- comes out as a merely narrower range that still
// contains the nuts. And it treats every player at a width, never at a shape
// their reads imply. Both are worth having; neither is worth blocking this on.

// openWidth is how wide a raise first in is, by the seat it came from. 6-max
// cash, 100 big blinds deep.
var openWidth = map[table.Position]float64{
	table.PosUTG: 16,
	table.PosMP:  20,
	table.PosCO:  27,
	table.PosBTN: 43,
	table.PosSB:  40,
	// The big blind never opens a folded-round pot; this is the raise over
	// limpers, which is wider than an open and narrower than a defence.
	table.PosBB: 30,
}

// Widths for the preflop statements a player can make. Positional only for the
// open: a three-bet is roughly as wide from anywhere, and what varies with
// position is how often it happens, not what it contains.
const (
	fourBetWidth   = 3.0
	threeBetWidth  = 8.0
	openWidthOther = 25.0
	// Cold-calling a raise. Narrow because the hands that beat an opener
	// three-bet instead, and the reason this number cannot be smaller is that
	// the shape is wrong, not the size: the range is capped, not tight.
	coldCallWidth = 18.0
	// Everybody left in a pot that was raised, when who raised it cannot be
	// recovered. Between an opening range and a range that called one.
	raisedPotWidth = 22.0
	// Limping, and completing the small blind. Wide and weak.
	limpWidth     = 40.0
	completeWidth = 45.0
	// The big blind who was not raised and did not raise: everything they did
	// not open with, which is most of the deck.
	bigBlindCheckWidth = 85.0
	// Nothing observed. A player who has put in no voluntary money and taken no
	// action has said nothing, and the honest width is the whole deck.
	unknownWidth = 100.0
	// Nobody is ever narrower than this. A range of one per cent is four
	// combinations, and no read this tool has justifies that.
	narrowestWidth = 3.0
)

// How much one postflop action narrows a range.
//
// A bet barely narrows anything, and that is the finding that makes this table
// worth writing down rather than reusing the simulated bots' single factor.
// Equilibrium continuation-betting ranges are wide -- 60 to 70% of range in
// position on most flops, and higher still with a small size, which is exactly
// what a small size is for. Treating every bet as a strong statement is how a
// model talks itself out of calling.
//
// A raise is the opposite. Facing a bet costs money to continue, so a raise is
// made by the part of a range that beats a betting range, and that is a small
// part of it.
const (
	betNarrowing   = 0.80
	raiseNarrowing = 0.45
	callNarrowing  = 0.85
)

// Shape is the tunable part of the opponent range model: how wide a range is
// once there is a board, and how much each action narrows it.
//
// It exists because these numbers were finally measured rather than reasoned
// about. Marking the model against the cards the engine dealt -- 218,953
// decisions, docs/STRATEGY.md §5a -- says the postflop half of it is about
// twice as tight as the opponents really are, and that a raise, the one action
// the model narrows hardest on, is the one it gets most wrong.
type Shape struct {
	// PostflopScale multiplies the width once the flop is out. One is the model
	// as it stood before that measurement.
	PostflopScale float64
	// Bet, Raise and Call are what one such action does to a range.
	Bet, Raise, Call float64
}

// DefaultShape is the model as measured in docs/STRATEGY.md §5.
func DefaultShape() Shape {
	return Shape{
		PostflopScale: 1,
		Bet:           betNarrowing,
		Raise:         raiseNarrowing,
		Call:          callNarrowing,
	}
}

// CalibratedShape is what the marked cards say those numbers should be.
//
// Read off the table in docs/STRATEGY.md §5a. The width at the first postflop
// decision came out at 21% against a real 40%, which is the scale. The
// narrowing factors are the ratios the opponents' real ranges moved by:
// 36/40 after a bet and 32/40 after a raise.
//
// A call measured at 40/40 -- no narrowing at all. That is left at 0.95 rather
// than 1.0 deliberately: "a call tells you nothing about a range" is a stronger
// claim than the sample behind it supports, and the direction is what this
// change is testing. If it wins, that constant is worth measuring on its own.
func CalibratedShape() Shape {
	return Shape{PostflopScale: 1.9, Bet: 0.90, Raise: 0.80, Call: 0.95}
}

func (s Shape) orDefault() Shape {
	d := DefaultShape()
	if s.PostflopScale <= 0 {
		s.PostflopScale = d.PostflopScale
	}
	if s.Bet <= 0 {
		s.Bet = d.Bet
	}
	if s.Raise <= 0 {
		s.Raise = d.Raise
	}
	if s.Call <= 0 {
		s.Call = d.Call
	}
	return s
}

// typicalVPIP is the share of hands a competent 6-max regular enters with. A
// read is applied as a ratio to this, so a station's open is wider than a nit's
// open and neither replaces the positional figure outright.
const typicalVPIP = 24.0

// preflopStatement is what a player put in before the flop.
type preflopStatement struct {
	// raises is how many raises deep the betting was when this player last
	// raised: one is an open, two a three-bet, three or more a four-bet. It
	// counts the sequence and not this player's own raises, because the player
	// who opens and then four-bets has raised twice and is holding a four-betting
	// range, not a three-betting one.
	raises int
	// entered is whether they voluntarily put money in at all.
	entered bool
	// facedRaise is whether somebody had already raised when they entered,
	// which is the difference between limping and cold-calling.
	facedRaise bool
	// unattributed marks a statement recovered from the size of the pot rather
	// than from anybody's actions: the pot was raised, and who raised it is not
	// recoverable. The width is then one figure for everybody left in it.
	unattributed bool
}

// readStatement works out what a player did preflop.
//
// It prefers the action history, which the harness always has and the screen
// reader reconstructs from the nameplate badges. Where that is empty it falls
// back to what is on the felt, because the amount in front of a player is a
// direct observation and the history is a derived one: a wager of ten big
// blinds is a three-bet whether or not the badge that made it was ever seen.
func readStatement(h table.HandState, seat table.SeatState) preflopStatement {
	var st preflopStatement

	if len(h.ActionHistory) > 0 {
		level := 0
		for _, a := range h.ActionHistory {
			if a.Street != table.StreetPreflop {
				continue
			}
			aggressive := a.Action == table.ActionBet ||
				a.Action == table.ActionRaise ||
				a.Action == table.ActionAllIn
			mine := a.PlayerID == seat.PlayerID
			if aggressive {
				level++
			}
			if !mine {
				continue
			}
			switch {
			case aggressive:
				if !st.entered && level > 1 {
					st.facedRaise = true
				}
				st.raises = level
				st.entered = true
			case a.Action == table.ActionCall:
				if !st.entered && level > 0 {
					st.facedRaise = true
				}
				st.entered = true
			}
		}
		if st.entered || h.Street == table.StreetPreflop {
			return st
		}
	}

	// No usable history. Read the chips instead.
	bb := h.BigBlind
	if bb <= 0 {
		return preflopStatement{entered: seat.CurrentBet > 0}
	}

	// After the flop the chips in front of a player are this street's wager and
	// say nothing about what they did before it. What the pot says is that
	// somebody paid for it: a pot of more than four big blinds was raised, and
	// everybody still in it called that raise. Which of them raised and which
	// called cannot be recovered, so both come out at one width between the two.
	//
	// This is the live fallback and it should almost never fire -- the screen
	// reader reconstructs the history from the nameplate badges. It exists
	// because the alternative when it does fire is a random hand, which is the
	// error this whole file is about.
	if h.Street != table.StreetPreflop {
		if h.Pot > 4*bb {
			return preflopStatement{entered: true, raises: 1, facedRaise: false, unattributed: true}
		}
		return preflopStatement{entered: true}
	}
	blind := 0.0
	switch seat.Position {
	case table.PosBB:
		blind = bb
	case table.PosSB:
		blind = h.SmallBlind
	}
	voluntary := seat.CurrentBet - blind
	st.entered = voluntary > 0 || seat.LastAction == "call"

	// The chips say how much went in; only the nameplate badge says whether
	// this player put it there or matched it. They are not interchangeable and
	// confusing them is the one error this branch can make that matters: a big
	// blind who called a raise to five blinds has exactly as much in front of
	// them as the player who raised, and calling that a raising range is how a
	// defending range gets modelled at sixteen per cent.
	raised := true
	switch seat.LastAction {
	case "call", "check", "fold":
		raised = false
	}

	// The size ladder. An open is two to four blinds, a three-bet seven to
	// fourteen, and anything past that has been raised twice.
	if raised {
		switch {
		case seat.CurrentBet >= 15*bb:
			st.raises = 3
		case seat.CurrentBet >= 6.5*bb:
			st.raises = 2
		case seat.CurrentBet > bb*1.5:
			st.raises = 1
		}
	}
	if st.raises == 0 && st.entered {
		// Entered without raising. Whether that was a limp or a call of a raise
		// is decided by whether anybody has more chips out than they do.
		for _, other := range h.Seats {
			if other.PlayerID == seat.PlayerID || other.IsFolded {
				continue
			}
			if other.CurrentBet > seat.CurrentBet+1e-9 || other.CurrentBet > bb*1.5 {
				st.facedRaise = true
				break
			}
		}
	}
	return st
}

// preflopWidth is how wide one player's range is by the time the flop is dealt.
func preflopWidth(seat table.SeatState, st preflopStatement) float64 {
	switch {
	case st.unattributed:
		// Somebody raised and everybody here paid for it. Between an opening
		// range and a range that called one.
		return raisedPotWidth
	case st.raises >= 3:
		return fourBetWidth
	case st.raises == 2:
		return threeBetWidth
	case st.raises == 1:
		if st.facedRaise {
			// A raise over somebody else's raise is a three-bet however few
			// raises this player has personally made.
			return threeBetWidth
		}
		if w, ok := openWidth[seat.Position]; ok {
			return w
		}
		return openWidthOther
	case st.entered && st.facedRaise:
		return coldCallWidth
	case st.entered && seat.Position == table.PosSB:
		return completeWidth
	case st.entered:
		return limpWidth
	case seat.Position == table.PosBB:
		return bigBlindCheckWidth
	default:
		return unknownWidth
	}
}

// postflopNarrowing is what this player's actions after the flop have done to
// their range, as a multiplier.
func postflopNarrowing(h table.HandState, seat table.SeatState, shape Shape) float64 {
	if len(h.ActionHistory) == 0 {
		return 1
	}
	factor := 1.0
	facingBet := false
	for _, a := range h.ActionHistory {
		if a.Street == table.StreetPreflop {
			continue
		}
		if a.PlayerID != seat.PlayerID {
			switch a.Action {
			case table.ActionBet, table.ActionRaise, table.ActionAllIn:
				facingBet = true
			case table.ActionCheck:
			}
			continue
		}
		switch a.Action {
		case table.ActionBet, table.ActionRaise, table.ActionAllIn:
			if facingBet {
				factor *= shape.Raise
			} else {
				factor *= shape.Bet
			}
			facingBet = false
		case table.ActionCall:
			factor *= shape.Call
			facingBet = false
		}
	}
	return factor
}

// looseness scales a positional width by how loose this player actually is. A
// read moves the estimate and does not become it: an unknown player is played
// as a competent regular, which is the assumption that costs least when wrong.
func looseness(vpip float64, known bool) float64 {
	if !known || vpip <= 0 {
		return 1
	}
	f := vpip / typicalVPIP
	return clamp(f, 0.5, 2.2)
}

// RangeWidthFor is how wide one opponent's holding still is, as a percentage of
// all starting hands, given everything observed about this hand and whatever is
// known about the player.
func RangeWidthFor(h table.HandState, seat table.SeatState, vpip float64, known bool, shape Shape) float64 {
	shape = shape.orDefault()
	st := readStatement(h, seat)
	w := preflopWidth(seat, st)

	// The read widens or tightens what they entered with, and only that. It
	// says nothing about how they play a flop, and applying it to the postflop
	// narrowing as well would count the same fact twice.
	if st.entered || seat.Position == table.PosBB {
		w *= looseness(vpip, known)
	}
	// The scale applies only once there is a board. Before the flop the model
	// measured right -- 81% assigned against 88% needed -- and widening a part
	// that is already correct would be changing it for the sake of one number.
	if h.Street != table.StreetPreflop && h.Street != "" {
		w *= shape.PostflopScale
	}
	w *= postflopNarrowing(h, seat, shape)

	return clamp(w, narrowestWidth, 100)
}

func clamp(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
