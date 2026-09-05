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
	"slices"
	"strings"
	"time"

	"poker-game-analyzer/pkg/advice"
	"poker-game-analyzer/pkg/calib"
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
func candidateByName(name string, level sim.ReadLevel, iters, vsTop int, flips bool, wideScaleOverride float64) (sim.Candidate, error) {
	// A "/noise" segment says what the screen reader does to the state before
	// the tool sees it. Without one the tool decides on the engine's own state,
	// which is how every run before this existed and is not how the tool plays.
	name, noiseSpec, hasNoise := strings.Cut(name, "/")
	noise, err := noiseByName(noiseSpec, hasNoise)
	if err != nil {
		return sim.Candidate{}, fmt.Errorf("%s: %w", name, err)
	}
	noise.MeasureFlips = flips

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
		// "+ranges" narrows each opponent's range by what they did this hand
		// rather than holding it at their VPIP. A switch only so the two can
		// be measured on the same decks -- see pkg/advice/ranges.go.
		// Suffixes name the parts of the strategy that are still switches.
		// Several may be worn at once: tool:stats+sizing+bluff.
		//
		// "+ranges", "+capped" and "+polar" were three of them and are the
		// strategy now -- measured together at +4.05 and +6.39 bb/100 over 1.28
		// million hands, see docs/STRATEGY.md §5. They are accepted and ignored,
		// rather than rejected, so that a command written yesterday still runs
		// and still means what it meant. Removing them would be worse than
		// keeping them: an unknown suffix is an error, and an error is how a
		// long-running comparison ends at the first candidate.
		sizingPolicy, semiBluff, defend, wide := false, false, false, false
		for again := true; again; {
			again = false
			for _, gone := range []string{"+ranges", "+capped", "+polar"} {
				if s, ok := strings.CutSuffix(suffix, gone); ok {
					suffix, again = s, true
				}
			}
			if s, ok := strings.CutSuffix(suffix, "+sizing"); ok {
				sizingPolicy, suffix, again = true, s, true
			}
			if s, ok := strings.CutSuffix(suffix, "+bluff"); ok {
				semiBluff, suffix, again = true, s, true
			}
			if s, ok := strings.CutSuffix(suffix, "+defend"); ok {
				defend, suffix, again = true, s, true
			}
			// "+wide" holds opponents to the width the marked cards say they
			// really have, rather than the width the model reasoned its way to.
			// See advice.CalibratedShape and docs/STRATEGY.md §5a.
			if s, ok := strings.CutSuffix(suffix, "+wide"); ok {
				wide, suffix, again = true, s, true
			}
		}
		if suffix == "" {
			hasSuffix = false
		}
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
		if hasNoise {
			label = name + "/" + noiseSpec
		}
		shape := advice.Shape{}
		if wide {
			shape = advice.CalibratedShape()
			if wideScaleOverride > 0 {
				shape.PostflopScale = wideScaleOverride
			}
		}
		return sim.Candidate{Label: label, Shape: shape, New: func(seed int64, tr *sim.Tracker) sim.Agent {
			opt := sim.HarnessOptions(rand.New(rand.NewSource(seed)))
			if iters > 0 {
				opt.Iterations = iters
			}
			if vsTop > 0 {
				opt.VsTopIterations = vsTop
			}
			opt.SizingPolicy = sizingPolicy
			opt.SemiBluff = semiBluff
			opt.Defend = defend
			opt.Shape = shape
			tool := sim.NewTool(label, tr, level, opt)
			tool.Noise = noise
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

// noiseByName resolves the "/noise" segment of a candidate name.
//
//	tool:full            the engine's own state, the harness as it always was
//	tool:full/live       every defect at the rate the live session showed
//	tool:full/button_lost   that one defect at its live rate, nothing else
//	tool:full/-button_lost  every live defect except that one
//
// The last two are the pair that prices a repair. Seat /live and /-button_lost
// together and the paired difference between them is what fixing the dealer
// button is worth while everything else stays broken; seat a clean tool and
// /button_lost and the difference is what that defect costs on its own. The two
// numbers are not the same when defects interact, and knowing which is which is
// the point of having both.
func noiseByName(spec string, has bool) (sim.Noise, error) {
	if !has || spec == "" || spec == "clean" {
		return sim.Noise{}, nil
	}
	// "live+hero_unnamed=0.26" is the live session with one rate replaced, which
	// is how a repair is priced: measure what the fix achieves on a recorded
	// session, put that number here, and read the bb/100 it buys.
	if base, override, ok := strings.Cut(spec, "+"); ok && base == "live" {
		n := sim.LiveNoise()
		for _, part := range strings.Split(override, "+") {
			name, val, ok := strings.Cut(part, "=")
			if !ok {
				return sim.Noise{}, fmt.Errorf("override %q wants defect=rate", part)
			}
			d := sim.Defect(name)
			if !slices.Contains(sim.AllDefects, d) {
				return sim.Noise{}, fmt.Errorf("unknown defect %q in override", name)
			}
			var rate float64
			if _, err := fmt.Sscanf(val, "%f", &rate); err != nil || rate < 0 || rate > 1 {
				return sim.Noise{}, fmt.Errorf("rate %q for %s must be between 0 and 1", val, name)
			}
			n = n.Set(d, rate)
		}
		return n, nil
	}
	if spec == "live" {
		return sim.LiveNoise(), nil
	}
	without := strings.HasPrefix(spec, "-")
	d := sim.Defect(strings.TrimPrefix(spec, "-"))
	if !slices.Contains(sim.AllDefects, d) {
		names := make([]string, len(sim.AllDefects))
		for i, x := range sim.AllDefects {
			names[i] = string(x)
		}
		return sim.Noise{}, fmt.Errorf("unknown defect %q; known: live, clean, %s", spec, strings.Join(names, ", "))
	}
	if without {
		return sim.WithoutNoise(d), nil
	}
	return sim.OnlyNoise(d), nil
}

func main() {
	var (
		hands      = flag.Int("hands", 1000, "recorded hands per lineup per candidate")
		lineups    = flag.Int("lineups", 20, "how many random table compositions to draw")
		seed       = flag.Int64("seed", 1, "run seed; the same seed replays the same cards")
		seats      = flag.Int("seats", 6, "players at the table, hero included")
		candidates = flag.String("candidates", "pro,tool:stats,fold", "comma-separated; the first is the baseline.\n\ttool[:off|stats|full][/noise] seats the advisor; novice[:level][@0..1] seats a beginner following it.\n\t/noise is clean (default), live, a defect name, or -defect for live-without-it")
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
		flips      = flag.Bool("flips", false, "measure decision stability: decide twice, on the clean state and the noisy one, and report how often the noise changed the answer. Roughly doubles the run")
		carry      = flag.Bool("session", false, "carry stacks across the hands of a session instead of resetting each hand, and report the trajectory: whether the money grew and how often it busted")
		allInEV    = flag.Bool("allin-ev", true, "score a pot that went all-in with cards to come by the equity it had when the last chip went in, instead of by the card that came. The hand still plays out for real; only the reported money changes")
		ledger     = flag.String("ledger", "bench/results.jsonl", "append the run to this file and compare against the last run of the same shape; empty disables it")
		wideScale  = flag.Float64("wide-scale", 0, "override how much +wide widens the postflop range; 0 uses the calibrated 1.9. Exists to sweep it: the width came out right at 1.9 but the opponent is not uniform inside it, so the best number for a decision may be smaller than the best number for a histogram")
		calibrate  = flag.Bool("calib", false, "mark the opponent range model against the cards actually dealt, instead of only against the money. Reports where the model put the opponent and where they really were; see docs/HARNESS.md §3e")
		calibMin   = flag.Int("calib-min", 200, "smallest spot the calibration report will print")
		guard      = flag.String("guard", "", "exit non-zero if this candidate is more than two combined standard errors below its last recorded run of the same shape")
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
		AllInEV:     *allInEV,
		Calib:       *calibrate,
	}
	cfg.Cfg.PotHidesStreetBets = *potHidden

	for _, name := range strings.Split(*candidates, ",") {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		c, err := candidateByName(name, level, *iters, *vsTop, *flips, *wideScale)
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

	// Calibration is reported per candidate, because two candidates that differ
	// in how they model an opponent are exactly the pair worth comparing here.
	for _, r := range rep.Results {
		if r.Calib == nil {
			continue
		}
		fmt.Printf("\n=== %s: the range model against the cards dealt ===\n\n", r.Label)
		calib.Render(os.Stdout, r.Calib.Buckets(), *calibMin)
	}
	fmt.Printf("\nrun took %s\n", time.Since(start).Round(time.Millisecond))

	if *ledger == "" {
		return
	}
	entry := buildEntry(rep, ledgerConfig{
		Hands: *hands, Lineups: *lineups, Seed: *seed, Seats: *seats,
		StackMinBB: *stackMin, StackMaxBB: *stackMax, Reads: *reads,
		Field: *field, Warmup: *warmup, Churn: *churn, AllInEV: *allInEV,
	})
	prev, err := appendLedger(*ledger, entry)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ledger: %v\n", err)
	}
	fmt.Print(compareToPrevious(entry, prev))
	if entry.Dirty {
		fmt.Println("журнал: дерево было грязным при сборке — эта строка не приписана коммиту")
	}
	// The binary is what measured; the tree is what you would read if you went
	// looking for the code. When they differ, they are two different programs.
	if tree := treeState(); tree != "" && entry.Commit != "" && tree != entry.Commit {
		fmt.Printf("журнал: ВНИМАНИЕ — бинарник собран из %s, а в дереве %s. Мерили не то, что лежит рядом; пересоберите (make harness)\n",
			entry.Commit, tree)
	}
	if *guard != "" {
		bad, msg := regressed(entry, prev, *guard)
		fmt.Println(msg)
		if bad {
			os.Exit(1)
		}
	}
}
