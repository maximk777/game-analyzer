// Command harness plays the advisor out over whole hands against a table of
// simulated opponents and reports what following it would have won or lost.
//
//	go run ./cmd/harness -hands 2000 -lineups 40
//	go run ./cmd/harness -field station,station,whale,nit,tag -candidates tag,tool
//
// The first candidate listed is the baseline every other is compared against,
// hand by hand on identical decks.
package main

import (
	"flag"
	"fmt"
	"math/rand"
	"os"
	"strings"
	"time"

	"poker-game-analyzer/pkg/advice"
	"poker-game-analyzer/pkg/sim"
)

// candidateByName resolves one name from the -candidates list.
//
// The tool takes a suffix naming how much it is allowed to know:
// tool:off, tool:stats, tool:full, tool:full+counted. Several of them can sit
// in the same run, which is the only way to compare them fairly -- they then
// play the identical decks, and the difference between knowing nothing about
// the opponents and knowing their fold frequencies is measured directly rather
// than across two runs with their own variance.
func candidateByName(name string, level sim.ReadLevel, iters, vsTop int) (sim.Candidate, error) {
	base, suffix, hasSuffix := strings.Cut(name, ":")
	// A novice is a person doing whatever the tool says, and takes the same
	// read-level suffix plus an optional adherence: novice:full@0.9.
	novice := false
	discipline := 1.0
	if base == "novice" {
		novice = true
		base = "tool"
		if d, rest, has := strings.Cut(suffix, "@"); has {
			suffix = d
			fmt.Sscanf(rest, "%f", &discipline)
		}
		if suffix == "" {
			hasSuffix = false
		}
	}
	if base == "tool" {
		if hasSuffix {
			// "+counted" was a switch for letting counted tendencies carry
			// weight in the fold model. It is how the strategy works now --
			// measured, it is worth four big blinds per hundred hands against
			// the population -- so the switch is gone and the suffix is
			// accepted and ignored, rather than silently meaning something
			// different from what it used to.
			switch strings.TrimSuffix(suffix, "+counted") {
			case "off":
				level = sim.ReadsOff
			case "stats":
				level = sim.ReadsStats
			case "full":
				level = sim.ReadsFull
			default:
				return sim.Candidate{}, fmt.Errorf("unknown read level %q in %q", suffix, name)
			}
		}
		label := name
		return sim.Candidate{Label: label, New: func(seed int64, tr *sim.Tracker) sim.Agent {
			opt := sim.HarnessOptions(rand.New(rand.NewSource(seed)))
			if iters > 0 {
				opt.Iterations = iters
			}
			if vsTop > 0 {
				opt.VsTopIterations = vsTop
			}
			tool := sim.NewTool(label, tr, level, opt)
			if novice {
				return sim.NewNovice(tool, rand.New(rand.NewSource(seed*7+3)), discipline)
			}
			return tool
		}}, nil
	}
	switch name {
	case "tag":
		return sim.Candidate{Label: "tag", New: func(seed int64, _ *sim.Tracker) sim.Agent {
			return sim.TAGBot(rand.New(rand.NewSource(seed)))
		}}, nil
	case "fold":
		return sim.Candidate{Label: "always-fold", New: func(int64, *sim.Tracker) sim.Agent { return sim.FoldBot{} }}, nil
	case "call":
		return sim.Candidate{Label: "always-call", New: func(int64, *sim.Tracker) sim.Agent { return sim.CallBot{} }}, nil
	}
	if opp, ok := sim.OpponentByName(name); ok {
		return sim.Candidate{Label: opp.Name, New: func(seed int64, _ *sim.Tracker) sim.Agent {
			return opp.New(rand.New(rand.NewSource(seed)))
		}}, nil
	}
	return sim.Candidate{}, fmt.Errorf("unknown candidate %q", name)
}

func main() {
	var (
		hands      = flag.Int("hands", 1000, "recorded hands per lineup per candidate")
		lineups    = flag.Int("lineups", 20, "how many random table compositions to draw")
		seed       = flag.Int64("seed", 1, "run seed; the same seed replays the same cards")
		seats      = flag.Int("seats", 6, "players at the table, hero included")
		candidates = flag.String("candidates", "pro,tool:stats,fold", "comma-separated; the first is the baseline.\n\ttool[:off|stats|full][+counted] seats the advisor; novice[:level][@0..1] seats a beginner following it")
		field      = flag.String("field", "", "fixed opponents, comma-separated (pro, tag, nit, lag, station, whale, maniac, rock); empty draws from the population")
		stackMin   = flag.Float64("stack-min", 40, "shortest starting stack, in big blinds")
		stackMax   = flag.Float64("stack-max", 200, "deepest starting stack, in big blinds")
		reads      = flag.String("reads", "stats", "what the tool knows about opponents: off, stats, full")
		warmup     = flag.Int("warmup", 200, "hands played before recording, so reads are not measured while being gathered")
		iters      = flag.Int("iters", 0, "equity samples per decision; 0 uses the harness default")
		vsTop      = flag.Int("vstop-iters", 0, "conditional equity samples; 0 uses the harness default")
		workers    = flag.Int("workers", 0, "parallel workers; 0 uses every core")
		potHidden  = flag.Bool("pot-hides-street-bets", false, "report the pot without the chips currently out in front")
		heroStack  = flag.Float64("hero-stack", 0, "hero's starting stack in big blinds; 0 gives hero the same stack as the field")
		churn      = flag.Float64("churn", 0, "chance per hand that an opponent gets up and a stranger sits down; 0.004 is roughly one new face every four orbits")
		buyIns     = flag.Int("buy-ins", 1, "how many stacks hero has behind them in session mode; one buy-in is not a bankroll and busts most sessions whatever the strategy")
		carry      = flag.Bool("session", false, "carry stacks across the hands of a session instead of resetting each hand, and report the trajectory: whether the money grew and how often it busted")
	)
	flag.Parse()

	level := map[string]sim.ReadLevel{"off": sim.ReadsOff, "stats": sim.ReadsStats, "full": sim.ReadsFull}[*reads]

	cfg := sim.RunConfig{
		Hands:       *hands,
		Lineups:     *lineups,
		Seed:        *seed,
		Seats:       *seats,
		StackMinBB:  *stackMin,
		StackMaxBB:  *stackMax,
		Level:       level,
		Warmup:      *warmup,
		Workers:     *workers,
		Cfg:         sim.DefaultConfig(),
		HeroStackBB: *heroStack,
		CarryStacks: *carry,
		BuyIns:      *buyIns,
		SeatChurn:   *churn,
	}
	cfg.Cfg.PotHidesStreetBets = *potHidden

	for _, name := range strings.Split(*candidates, ",") {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		c, err := candidateByName(name, level, *iters, *vsTop)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
		cfg.Candidates = append(cfg.Candidates, c)
	}
	if *field != "" {
		for _, name := range strings.Split(*field, ",") {
			opp, ok := sim.OpponentByName(strings.TrimSpace(name))
			if !ok {
				fmt.Fprintf(os.Stderr, "unknown opponent %q\n", name)
				os.Exit(2)
			}
			cfg.Field = append(cfg.Field, opp)
		}
	}

	// Keep the pipeline honest about what it is being asked for.
	_ = advice.Options{}

	start := time.Now()
	rep := sim.Run(cfg)
	fmt.Print(rep.Render())
	fmt.Printf("\nrun took %s\n", time.Since(start).Round(time.Millisecond))
}
