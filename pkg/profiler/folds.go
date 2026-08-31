package profiler

import "poker-game-analyzer/pkg/table"

// How often a player folds when somebody bets at them.
//
// This is the statistic the advisor asks for by name -- observedFoldRate looks
// for fold_to_3bet, fold_to_raise, fold_to_cbet and fold_to_bet, and nothing
// else in the tendency map can stand in for them -- and until now the only
// thing that ever produced one was the language model. With no model configured
// the advisor found no read on any street, fold equity stayed at the
// equilibrium baseline for every opponent alike, and the whole exploitative
// half of the strategy was unreachable: it cannot bet without a read unless it
// already holds half the equity.
//
// Every one of these is countable from the actions that were already being
// recorded. Nothing new has to be observed; it only has to be counted.

// facedCounts is one player's record of being bet at.
type facedCounts struct {
	// Preflop, facing at least one raise, and facing a reraise on top of it.
	raiseFaced, raiseFolded     int
	threeBetFaced, threeBetFold int
	// The flop, where the first bet is a continuation bet often enough for the
	// two to be the same statistic in every tracker anyone uses.
	cbetFaced, cbetFolded int
	// Turn and river.
	betFaced, betFolded int
	// Facing a raise after the flop, which is a different question from facing
	// a bet and gets a very different answer.
	//
	// Conflating them cost real money. The advisor priced its own flop raises
	// off how often the opponent folded to a *bet*; a player who folds to half
	// the bets aimed at them folds to far fewer of the raises, because by the
	// time they are facing one they have already put money in with something.
	// Measured, letting counted reads drive the sizing that way turned a
	// +3.5 bb/100 strategy into -5.8, and the loss was concentrated in exactly
	// one line of the report: raising into a bet on the flop, -15 big blinds a
	// hand over six hundred hands.
	raiseFacedPost, raiseFoldedPost int

	// How often this player bets when nothing is owed. It is the width of the
	// range they bet with, measured rather than assumed, and it is the read
	// that decides whether hero's call is priced against a player betting nine
	// flops in ten or three.
	betFlop, betFlopSpots int
	betLate, betLateSpots int
}

// minFoldSample is how many opportunities a fold frequency needs before it is
// reported at all.
//
// A player who folded the one time anybody bet at them has a measured fold
// frequency of 100%, and handing that to a model that sizes bluffs off it is
// worse than handing it nothing. readWeight already shrinks a read towards the
// baseline by sample size, but it shrinks by *hands played*, which is not the
// same count -- a player can be at the table for eighty hands and have faced
// three bets.
const minFoldSample = 10

// facedAggression walks a hand and counts, for each player, the times they had
// a bet in front of them and what they did about it.
//
// A player who calls a raise and then faces a reraise faced aggression twice,
// and both are counted. Trackers usually count once per street; counting each
// occasion is what a fold-frequency model actually wants, since it is asking
// "if I bet at this player now, do they fold".
func facedAggression(hist []table.ActionRecord) map[string]*facedCounts {
	out := make(map[string]*facedCounts)
	get := func(id string) *facedCounts {
		c, ok := out[id]
		if !ok {
			c = &facedCounts{}
			out[id] = c
		}
		return c
	}

	street := table.Street("")
	aggressions := 0
	lastAggressor := ""

	for _, a := range hist {
		if a.Street != street {
			street = a.Street
			aggressions = 0
			lastAggressor = ""
		}

		facing := aggressions > 0 && lastAggressor != a.PlayerID
		if !facing && street != table.StreetPreflop {
			c := get(a.PlayerID)
			bet := a.Action == table.ActionBet || a.Action == table.ActionRaise || a.Action == table.ActionAllIn
			if street == table.StreetFlop {
				c.betFlopSpots++
				if bet {
					c.betFlop++
				}
			} else {
				c.betLateSpots++
				if bet {
					c.betLate++
				}
			}
		}
		if facing {
			c := get(a.PlayerID)
			folded := a.Action == table.ActionFold
			switch street {
			case table.StreetPreflop:
				c.raiseFaced++
				if folded {
					c.raiseFolded++
				}
				if aggressions >= 2 {
					c.threeBetFaced++
					if folded {
						c.threeBetFold++
					}
				}
			case table.StreetFlop:
				c.cbetFaced++
				if folded {
					c.cbetFolded++
				}
			default:
				c.betFaced++
				if folded {
					c.betFolded++
				}
			}
			if street != table.StreetPreflop && aggressions >= 2 {
				c.raiseFacedPost++
				if folded {
					c.raiseFoldedPost++
				}
			}
		}

		switch a.Action {
		case table.ActionBet, table.ActionRaise, table.ActionAllIn:
			aggressions++
			lastAggressor = a.PlayerID
		}
	}
	return out
}

// foldRate is a counted frequency, and whether there is enough behind it to
// report.
func foldRate(folded, faced int) (float64, bool) {
	if faced < minFoldSample {
		return 0, false
	}
	return float64(folded) / float64(faced), true
}
