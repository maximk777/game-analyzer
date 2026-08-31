package advisor

import (
	"testing"

	"poker-game-analyzer/pkg/table"
)

// Warming up at a table, and what it costs to skip it.
//
// The tool's advice used to be identical whether it had watched an opponent for
// four hundred hands or had never seen them before: an unread player fell back
// to the equilibrium baseline, which is a reasonable average over strategies
// and not a description of the person in the seat. The spread between the rock
// and the maniac that average is taken over is exactly the money at risk, and
// nothing in the model was paying for it.
//
// Now it is: a commitment against a stranger is charged for the not knowing,
// quadratically in how much of the stack goes in, and the charge falls away as
// the reads come in. Playing carefully while learning the table and pressing
// once it is learnt is one rule with a dial, not two modes with a switch.
func knownOpponent(stack float64, hands int) OpponentRead {
	t := map[string]float64{
		"vpip":        24,
		"pfr":         18,
		"hands_count": float64(hands),
	}
	if hands > 0 {
		t["fold_to_cbet"] = 0.55
		t["fold_to_cbet_n"] = float64(hands)
		t["fold_to_raise_post"] = 0.40
		t["fold_to_raise_post_n"] = float64(hands)
	}
	return OpponentRead{PlayerID: "v0", Stack: stack, Tendencies: t, Hands: hands}
}

func TestStrangerCostsMoreThanARegularToPlayBigPotsAgainst(t *testing.T) {
	const pot, stack = 100, 900

	_, base := liveShoveSpot(t, "Ah Kd", "Ks Qd 5s", pot, stack, 1)

	stranger := base
	stranger.Opponents = []OpponentRead{knownOpponent(stack, 0)}
	studied := base
	studied.Opponents = []OpponentRead{knownOpponent(stack, 400)}

	a, b := Calculate(stranger), Calculate(studied)

	t.Logf("stranger: knowledge %.2f phase %q -> %s %.0f", a.TableKnowledge, a.Phase, a.PrimaryAction, a.RecommendedAmount)
	t.Logf("studied:  knowledge %.2f phase %q -> %s %.0f", b.TableKnowledge, b.Phase, b.PrimaryAction, b.RecommendedAmount)

	if a.TableKnowledge != 0 {
		t.Errorf("an opponent never seen before scored %.2f on knowledge", a.TableKnowledge)
	}
	if b.TableKnowledge <= a.TableKnowledge {
		t.Errorf("four hundred hands of history scored %.2f, no better than the %.2f of never having met",
			b.TableKnowledge, a.TableKnowledge)
	}
	if a.Phase != "разведка" {
		t.Errorf("a table of strangers is in phase %q", a.Phase)
	}

	// The charge is on the size, so the largest option is the one that must
	// move. Whatever both come out preferring, the all-in has to be worth less
	// against the stranger.
	allIn := func(r AdvisorResponse) (float64, bool) {
		for _, act := range r.Actions {
			if act.Action == table.ActionAllIn {
				return act.EV, true
			}
		}
		return 0, false
	}
	evStranger, ok1 := allIn(a)
	evStudied, ok2 := allIn(b)
	if !ok1 || !ok2 {
		t.Fatal("no all-in among the options in one of the two")
	}
	if evStranger >= evStudied {
		t.Errorf("shoving is worth %.1f against a stranger and %.1f against a player read over four hundred hands; the not-knowing is free",
			evStranger, evStudied)
	}
	t.Logf("all-in EV: stranger %.1f, studied %.1f", evStranger, evStudied)
}

// The charge must be about size, not a blanket tax: a small bet against a
// stranger is barely touched, which is what keeps the tool playing rather than
// hiding while it learns.
func TestCautionScalesWithHowMuchOfTheStackGoesIn(t *testing.T) {
	const pot, stack = 100, 2000
	_, in := liveShoveSpot(t, "Ah Ad", "Ks Qd 5s", pot, stack, 1)
	in.Opponents = []OpponentRead{knownOpponent(stack, 0)}
	a := Calculate(in)

	var small, big float64
	for _, act := range a.Actions {
		if act.SizingLabel == "33% Pot" {
			small = act.Amount
		}
		if act.Action == table.ActionAllIn {
			big = act.Amount
		}
	}
	if small <= 0 || big <= 0 {
		t.Fatalf("expected both a small bet and an all-in on offer, got %.0f and %.0f", small, big)
	}
	// The charge is darkHorseCaution * (amount/stack) * amount, so at a third
	// of a hundred-chip pot out of two thousand it is a fraction of a chip, and
	// at the shove it is four hundred.
	wantSmall := darkHorseCaution * (small / stack) * small
	wantBig := darkHorseCaution * (big / stack) * big
	if wantSmall > 1 {
		t.Errorf("a %.0f-chip bet is charged %.2f for not knowing the opponent; that is not a small bet any more", small, wantSmall)
	}
	if wantBig < 100 {
		t.Errorf("a %.0f-chip shove is charged only %.2f", big, wantBig)
	}
	t.Logf("caution: %.0f-chip bet costs %.2f, %.0f-chip shove costs %.0f", small, wantSmall, big, wantBig)
}
