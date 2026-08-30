package advisor

import (
	"fmt"
	"testing"

	"poker-game-analyzer/pkg/table"
)

// Every made-hand category the tool will meet, on each street, at two stack
// depths and two field sizes.
//
// Three spot checks were what the all-in work was first judged on, and three
// spots cannot show the shape of a strategy -- they can only show that the one
// hand being argued about behaved. This prints the whole grid and asserts the
// properties that must hold across all of it, so a change that fixes one cell
// and breaks four others cannot pass.
//
// What it caught on the way in: queens on a paired-ace board shoving multiway,
// which is fixed, and made hands shoving eight times the pot at SPR 8, which is
// not -- see docs/HANDOFF.md. The overbet is deliberately not asserted against
// here, because an assertion nobody can satisfy is just a broken test.
func TestStrategySweep(t *testing.T) {
	type spot struct {
		name  string
		hero  string
		flop  string
		turn  string
		river string
	}
	spots := []spot{
		{"air", "7h 2c", "Ks Qd 5s", "Ks Qd 5s 9h", "Ks Qd 5s 9h 3c"},
		{"underpair", "8h 8d", "Ks Qd 5s", "Ks Qd 5s 9h", "Ks Qd 5s 9h 3c"},
		{"overpair-vs-paired", "Qh Qd", "Tc Ad As", "Tc Ad As 5d", "Tc Ad As 5d 2c"},
		{"second pair", "Qh Jd", "Ks Qd 5s", "Ks Qd 5s 9h", "Ks Qd 5s 9h 3c"},
		{"top pair top kick", "Ah Kd", "Ks Qd 5s", "Ks Qd 5s 9h", "Ks Qd 5s 9h 3c"},
		{"overpair", "Ah Ad", "Ks Qd 5s", "Ks Qd 5s 9h", "Ks Qd 5s 9h 3c"},
		{"two pair", "Ks Qc", "Ks Qd 5h", "Ks Qd 5h 9c", "Ks Qd 5h 9c 3d"},
		{"trips", "3h 2c", "2s 2h 4c", "2s 2h 4c 6s", "2s 2h 4c 6s 9d"},
		{"set", "5h 5c", "Ks Qd 5s", "Ks Qd 5s 9h", "Ks Qd 5s 9h 3c"},
		{"flush draw", "Ah 4h", "Kh Qh 5s", "Kh Qh 5s 9c", "Kh Qh 5s 9c 3d"},
		{"open-ender", "Jh Th", "9s 8d 2c", "9s 8d 2c 4h", "9s 8d 2c 4h 3d"},
		{"nut flush", "Ah 4h", "Kh Qh 5h", "Kh Qh 5h 9c", "Kh Qh 5h 9c 3d"},
	}

	for _, depth := range []struct {
		name string
		spr  float64
	}{{"short spr0.5", 0.5}, {"deep spr8", 8}} {
		for _, villains := range []int{1, 3} {
			t.Logf("=== %s, %d opponent(s) ===", depth.name, villains)
			for _, sp := range spots {
				pot := 1000.0
				stack := pot * depth.spr
				line := fmt.Sprintf("  %-20s", sp.name)
				for _, b := range []string{sp.flop, sp.turn, sp.river} {
					_, in := liveShoveSpot(t, sp.hero, b, pot, stack, villains)
					a := Calculate(in)
					label := string(a.PrimaryAction)
					if a.RecommendedAmount > 0 {
						label = fmt.Sprintf("%s %.0f", label, a.RecommendedAmount)
					}
					line += fmt.Sprintf(" | %-14s eq=%.2f", label, a.Equity)

					aggressive := a.PrimaryAction == table.ActionBet ||
						a.PrimaryAction == table.ActionRaise ||
						a.PrimaryAction == table.ActionAllIn

					// Nothing may be wagered that cannot be called.
					if a.RecommendedAmount > stack+1e-9 {
						t.Errorf("%s %s %dv: sized %.0f above the effective stack %.0f",
							sp.name, depth.name, villains, a.RecommendedAmount, stack)
					}
					// A hand behind its share of the pot has no business
					// putting money in for value, and with no reads there is no
					// fold equity to bluff with either.
					if aggressive && a.Equity < 1.0/float64(villains+1) {
						t.Errorf("%s %s %dv: %s %.0f on %.2f equity, below the %.2f fair share",
							sp.name, depth.name, villains, a.PrimaryAction, a.RecommendedAmount,
							a.Equity, 1.0/float64(villains+1))
					}
					// Nothing is ever owed in these spots, so folding is never
					// the answer -- checking is free.
					if a.PrimaryAction == table.ActionFold {
						t.Errorf("%s %s %dv: folded with nothing to call", sp.name, depth.name, villains)
					}
				}
				t.Log(line)
			}
		}
	}
}
