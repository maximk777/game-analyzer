package advisor

import (
	"testing"

	"poker-game-analyzer/pkg/table"
)

// Trapping: a check is worth what somebody bets into it.
//
// The model used to price a check as giving up -- equity times the pot as it
// stands -- so a hand strong enough to raise a bet that had not happened yet
// was always better off betting. Slowplaying was not played badly, it was
// absent: there was no term in the arithmetic that could contain it.
//
// What makes the branch computable is the counted read: how often this player
// bets when they are checked to. The three cases below are the whole of it --
// against somebody who bets nearly every flop the trap is worth more than
// betting, against somebody who almost never does it is not, and with no read
// at all nothing changes.
func trapSpot(t *testing.T, hero, board string, pot, stack float64, betFreq float64, sample int) Inputs {
	t.Helper()
	_, in := liveShoveSpot(t, hero, board, pot, stack, 1)
	if betFreq >= 0 {
		in.Opponents = []OpponentRead{{
			PlayerID: "v0",
			Stack:    stack,
			Tendencies: map[string]float64{
				"bet_freq_flop":   betFreq,
				"bet_freq_flop_n": float64(sample),
				"hands_count":     float64(sample) * 3,
			},
		}}
	}
	return in
}

func TestTrapPrefersCheckingToAPlayerWhoAlwaysBets(t *testing.T) {
	// A set on a dry board against a stack-deep opponent: the hand that most
	// wants the opponent to keep betting.
	const pot, stack = 100, 900

	noRead := Calculate(trapSpot(t, "5h 5c", "Ks 5s 2d", pot, stack, -1, 0))
	station := Calculate(trapSpot(t, "5h 5c", "Ks 5s 2d", pot, stack, 0.88, 120))
	nit := Calculate(trapSpot(t, "5h 5c", "Ks 5s 2d", pot, stack, 0.12, 120))

	checkEV := func(a AdvisorResponse) float64 {
		for _, act := range a.Actions {
			if act.Action == table.ActionCheck {
				return act.EV
			}
		}
		t.Fatal("no check among the options")
		return 0
	}

	t.Logf("check EV: no read %.1f, bets 88%% %.1f, bets 12%% %.1f",
		checkEV(noRead), checkEV(station), checkEV(nit))

	if checkEV(station) <= checkEV(noRead) {
		t.Errorf("checking to a player who bets 88%% of flops is worth %.1f, no more than the %.1f it is worth against a stranger",
			checkEV(station), checkEV(noRead))
	}
	if checkEV(nit) > checkEV(station) {
		t.Errorf("checking to a player who bets 12%% (%.1f) came out better than checking to one who bets 88%% (%.1f)",
			checkEV(nit), checkEV(station))
	}
	// The point of the whole thing: against the player who cannot stop betting,
	// the set checks.
	if station.PrimaryAction != table.ActionCheck {
		t.Errorf("a set against a player betting 88%% of flops was advised to %s %.0f rather than trap",
			station.PrimaryAction, station.RecommendedAmount)
	}
}

// And the guard on it: without a read the branch must not exist at all. There
// is no equilibrium figure for "how often does a stranger bet when checked to"
// worth trapping on, and inventing one would be the fabricated-fold-equity
// mistake in a new place.
func TestTrapDoesNothingWithoutACountedRead(t *testing.T) {
	const pot, stack = 100, 900
	bare := Calculate(trapSpot(t, "5h 5c", "Ks 5s 2d", pot, stack, -1, 0))
	// A read over two flops is not a read.
	thin := Calculate(trapSpot(t, "5h 5c", "Ks 5s 2d", pot, stack, 0.95, 0))

	find := func(a AdvisorResponse) float64 {
		for _, act := range a.Actions {
			if act.Action == table.ActionCheck {
				return act.EV
			}
		}
		return 0
	}
	// The equity behind both is sampled, so they will not agree to the last
	// decimal. The read that does fire is worth seventy-five big blinds on this
	// hand; anything inside a couple is the Monte Carlo and not the branch.
	if diff := find(thin) - find(bare); diff > 2 || diff < -2 {
		t.Errorf("a bet frequency with no sample behind it moved the check from %.1f to %.1f",
			find(bare), find(thin))
	}
}
