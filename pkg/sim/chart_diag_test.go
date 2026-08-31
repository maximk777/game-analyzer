package sim

import (
	"fmt"
	"math/rand"
	"sort"
	"testing"

	"poker-game-analyzer/pkg/preflop"
	"poker-game-analyzer/pkg/table"
)

// Diagnostic, not an assertion: where does the tool's preflop play differ from
// the chart it is supposed to be playing?
//
// The harness measures the tool at 13% VPIP against charts that open between
// 15% under the gun and 42% on the button. Those two numbers cannot both be
// right, and this prints which spots account for the difference.
func TestDiagPreflopAgainstTheChart(t *testing.T) {
	if testing.Short() {
		t.Skip("diagnostic")
	}
	counts := map[string]int{}
	tool := NewTool("tool", NewTracker(), ReadsStats, HarnessOptions(rand.New(rand.NewSource(4))))

	players := []*Player{{ID: "p0", Name: "hero", Agent: recorder{tool, func(s Spot, m Move) {
		if s.State.Street != table.StreetPreflop {
			return
		}
		pos, known := preflop.HeroPosition(s.State)
		if !known {
			counts["position not read"]++
			return
		}
		sit := preflop.SituationOf(s.State)
		want, charted := preflop.Recommend(pos, sit, s.State.HeroCards)
		if !charted {
			counts[fmt.Sprintf("%-3s %-14s chart silent", pos, sit)]++
			return
		}
		got := "fold"
		switch m.Action {
		case table.ActionCall:
			got = "call"
		case table.ActionCheck:
			got = "check"
		case table.ActionRaise, table.ActionBet, table.ActionAllIn:
			got = "raise"
		}
		key := fmt.Sprintf("%-3s %-14s chart=%-5s tool=%s", pos, sit, want, got)
		if string(want) == got || (want == preflop.Fold && got == "check") {
			key = fmt.Sprintf("%-3s %-14s agreed (%s)", pos, sit, want)
		}
		counts[key]++
	}}, Stack: 10000}}
	for i := 1; i < 6; i++ {
		players = append(players, &Player{
			ID: fmt.Sprintf("p%d", i), Name: "pro", Stack: 10000,
			Agent: NewPro(rand.New(rand.NewSource(int64(i) * 31))),
		})
	}
	tb := NewTable("d", DefaultConfig(), players, rand.New(rand.NewSource(9)))
	for i := 0; i < 1200; i++ {
		for _, p := range players {
			p.Stack = 10000
		}
		tb.PlayHand()
	}

	keys := make([]string, 0, len(counts))
	for k := range counts {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return counts[keys[i]] > counts[keys[j]] })
	for _, k := range keys {
		t.Logf("%6d  %s", counts[k], k)
	}
}

type recorder struct {
	inner Agent
	seen  func(Spot, Move)
}

func (r recorder) Name() string { return r.inner.Name() }
func (r recorder) Act(s Spot) Move {
	m := r.inner.Act(s)
	r.seen(s, m)
	return m
}
