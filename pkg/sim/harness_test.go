package sim

import (
	"math/rand"
	"sort"
	"testing"

	"poker-game-analyzer/pkg/table"
)

// The whole paired comparison rests on this: two candidates in the same seat
// must be dealt the same cards off the same seed, however differently they
// play. If the deck stream ever depends on the action, the difference between
// two strategies is contaminated by the difference between two card sequences,
// which is the noise the pairing exists to remove.
func TestDecksAreIdenticalAcrossCandidates(t *testing.T) {
	deal := func(hero Agent) ([][2]table.Card, [][]table.Card) {
		players := []*Player{{ID: "p0", Name: "hero", Agent: hero, Stack: 10000}}
		for i := 1; i < 6; i++ {
			players = append(players, &Player{
				ID: string(rune('a' + i)), Stack: 10000,
				Agent: NewBot(Archetypes[i%len(Archetypes)], rand.New(rand.NewSource(int64(i)))),
			})
		}
		tb := NewTable("t", DefaultConfig(), players, rand.New(rand.NewSource(1234)))
		var holes [][2]table.Card
		var boards [][]table.Card
		for i := 0; i < 300; i++ {
			for _, p := range players {
				p.Stack = 10000
			}
			r := tb.PlayHand()
			holes = append(holes, r.Holes["p0"])
			boards = append(boards, r.Board)
		}
		return holes, boards
	}

	h1, b1 := deal(FoldBot{})
	h2, b2 := deal(CallBot{})
	h3, _ := deal(NewBot(StyleManiac, rand.New(rand.NewSource(5))))

	for i := range h1 {
		if h1[i] != h2[i] || h1[i] != h3[i] {
			t.Fatalf("hand %d: hero was dealt %v, %v and %v under three strategies", i, h1[i], h2[i], h3[i])
		}
	}
	// Boards only run out as far as the action takes them, so only the cards
	// both hands actually saw can be compared -- but those must agree.
	for i := range b1 {
		n := len(b1[i])
		if len(b2[i]) < n {
			n = len(b2[i])
		}
		for j := 0; j < n; j++ {
			if b1[i][j] != b2[i][j] {
				t.Fatalf("hand %d card %d: board diverged, %v vs %v", i, j, b1[i], b2[i])
			}
		}
	}
}

// The tool must have an answer for every turn the engine gives it. A refusal
// live means "the frame was not readable"; here there is no frame and no
// reader, so a refusal means the harness is handing it something it does not
// understand, and every number in the run would be measuring that instead.
func TestToolAlwaysHasAnAnswer(t *testing.T) {
	rep := Run(RunConfig{
		Hands: 120, Lineups: 3, Seed: 5, Seats: 6, Warmup: 20,
		StackMinBB: 30, StackMaxBB: 180, Level: ReadsStats,
		Candidates: []Candidate{{Label: "tool", New: func(seed int64, tr *Tracker) Agent {
			return NewTool("tool", tr, ReadsStats, HarnessOptions(rand.New(rand.NewSource(seed))))
		}}},
	})
	if len(rep.Results) != 1 {
		t.Fatalf("expected one result, got %d", len(rep.Results))
	}
	if n := rep.Results[0].NoAdvice; n != 0 {
		t.Fatalf("the pipeline declined to answer %d times", n)
	}
	if rep.Results[0].Style.Hands == 0 {
		t.Fatal("no hands were recorded")
	}
}

// A run is reproducible from its seed, or nothing measured with it can be
// compared to anything measured later.
func TestRunIsDeterministic(t *testing.T) {
	mk := func() Report {
		return Run(RunConfig{
			Hands: 80, Lineups: 4, Seed: 77, Seats: 6, Warmup: 10, Workers: 3,
			StackMinBB: 50, StackMaxBB: 150, Level: ReadsFull,
			Candidates: []Candidate{
				{Label: "tag", New: func(seed int64, _ *Tracker) Agent { return TAGBot(rand.New(rand.NewSource(seed))) }},
				{Label: "tool", New: func(seed int64, tr *Tracker) Agent {
					return NewTool("tool", tr, ReadsFull, HarnessOptions(rand.New(rand.NewSource(seed))))
				}},
			},
		})
	}
	a, b := mk(), mk()
	for i := range a.Results {
		ra, sa := a.Results[i].BB100()
		rb, sb := b.Results[i].BB100()
		if ra != rb || sa != sb {
			t.Fatalf("%s: %v±%v then %v±%v", a.Results[i].Label, ra, sa, rb, sb)
		}
	}
}

// Sizes the tool will never be able to defend.
//
// Measured against a table of strong opponents, the tool's ninetieth-percentile
// bet was sixteen times the pot and its largest was sixty-six -- a whole stack
// pushed into a pot of a big blind and a half. It came from pricing the calling
// range off a preflop hand ranking that had never seen the board, so the model
// believed nothing that called a shove was any stronger than average.
//
// That is fixed in pkg/equity/board_range.go, and this is the guard on it. The
// threshold is deliberately loose: an all-in is a legitimate bet and short
// stacks make it a large multiple of the pot, so what is asserted is the shape
// of the distribution rather than the absence of any large bet.
func TestToolDoesNotOverbetTheWorldAfterTheFlop(t *testing.T) {
	rep := Run(RunConfig{
		Hands: 700, Lineups: 6, Seed: 90, Seats: 6, Warmup: 60,
		StackMinBB: 100, StackMaxBB: 100, Level: ReadsStats,
		Field: []Opponent{ProOpponent, ProOpponent, ProOpponent, ProOpponent, ProOpponent},
		Candidates: []Candidate{{Label: "tool", New: func(seed int64, tr *Tracker) Agent {
			return NewTool("tool", tr, ReadsStats, HarnessOptions(rand.New(rand.NewSource(seed))))
		}}},
	})
	// Postflop only. A standard preflop open is 1.67 times a pot of one and a
	// half blinds, so pooling the streets makes the median a preflop open and
	// hides everything this test is about.
	var sizes []float64
	for _, street := range []table.Street{table.StreetFlop, table.StreetTurn, table.StreetRiver} {
		sizes = append(sizes, rep.Results[0].SizingsBy[street]...)
	}
	if len(sizes) < 200 {
		t.Fatalf("only %d postflop bets to measure", len(sizes))
	}
	sort.Float64s(sizes)
	p90 := sizes[int(0.90*float64(len(sizes)-1))]
	if p90 > 4 {
		t.Errorf("the ninetieth-percentile postflop bet is %.1f times the pot; the tool is overbetting again", p90)
	}
	p99 := sizes[int(0.99*float64(len(sizes)-1))]
	t.Logf("postflop bet / pot: median %.2f  p90 %.2f  p99 %.2f  max %.2f",
		sizes[len(sizes)/2], p90, p99, sizes[len(sizes)-1])
}

// Session mode has to carry hero's money and stop when it runs out, because
// the question it exists for -- can this turn ten dollars into forty at a table
// where everybody has thirty -- is a question about a trajectory, and a
// trajectory that is reset every hand is not one.
func TestSessionModeCarriesTheStackAndEndsAtZero(t *testing.T) {
	rep := Run(RunConfig{
		Hands: 400, Lineups: 12, Seed: 71, Seats: 6, Warmup: 0,
		StackMinBB: 300, StackMaxBB: 300, HeroStackBB: 100,
		CarryStacks: true, Level: ReadsStats,
		Field: []Opponent{ProOpponent, ProOpponent, ProOpponent, ProOpponent, ProOpponent},
		Candidates: []Candidate{{Label: "shove", New: func(seed int64, _ *Tracker) Agent {
			// A strategy that puts it all in every hand must bust every
			// session, which is the sharpest test that busting is detected and
			// that the money really carries.
			return shoveBot{}
		}}},
	})
	sessions := rep.Results[0].Sessions
	if len(sessions) != 12 {
		t.Fatalf("expected one outcome per lineup, got %d", len(sessions))
	}
	for i, s := range sessions {
		if s.StartBB != 100 {
			t.Fatalf("session %d started with %.0f bb, wanted the 100 hero was given", i, s.StartBB)
		}
		if !s.Busted {
			t.Errorf("session %d: a strategy that shoves every hand finished with %.1f bb", i, s.FinalBB)
		}
		if s.Hands >= 400 {
			t.Errorf("session %d ran all %d hands; busting should have ended it", i, s.Hands)
		}
	}
}
