// Command slumbot plays the advisor against Slumbot and marks what it believed
// about the opponent's range against the cards Slumbot reveals.
//
//	go run ./cmd/slumbot -hands 500 -out bench/slumbot.jsonl
//	go run ./cmd/slumbot -report bench/slumbot.jsonl
//
// The run is deliberately not a win-rate measurement; see pkg/slumbot/report.go.
package main

import (
	"context"
	"flag"
	"fmt"
	"math/rand"
	"os"
	"os/signal"
	"syscall"
	"time"

	"poker-game-analyzer/pkg/advice"
	"poker-game-analyzer/pkg/slumbot"
)

func main() {
	var (
		hands  = flag.Int("hands", 200, "hands to play")
		out    = flag.String("out", "bench/slumbot.jsonl", "where to append the run log")
		report = flag.String("report", "", "read a run log and print the calibration report")
		minN   = flag.Int("min", 30, "smallest bucket the report will print")
		pace   = flag.Duration("pace", 600*time.Millisecond, "minimum gap between requests")
		seed   = flag.Int64("seed", 1, "seed for the advisor's sampling")
		iters  = flag.Int("iters", 2500, "equity iterations per decision")
	)
	flag.Parse()

	if *report != "" {
		f, err := os.Open(*report)
		if err != nil {
			fail(err)
		}
		defer f.Close()
		buckets, err := slumbot.Analyse(f)
		if err != nil {
			fail(err)
		}
		slumbot.Render(os.Stdout, buckets, *minN)
		return
	}

	// Append rather than truncate: a run is expensive in wall-clock time and
	// stopping one halfway should leave what it already measured.
	f, err := os.OpenFile(*out, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		fail(err)
	}
	defer f.Close()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	c := slumbot.NewClient()
	c.Pace = *pace
	opt := advice.Options{
		Iterations:      *iters,
		VsTopIterations: *iters * 3 / 5,
		Rng:             rand.New(rand.NewSource(*seed)),
	}

	last := time.Now()
	stats, runErr := slumbot.Run(ctx, c, *hands, opt, f, func(s slumbot.RunStats) {
		if time.Since(last) < 10*time.Second && s.Hands != *hands {
			return
		}
		last = time.Now()
		fmt.Fprintf(os.Stderr, "%d/%d hands, %d decisions (%d from the advisor), %s elapsed\n",
			s.Hands, *hands, s.Decisions, s.AdvisorDecs, s.Elapsed.Round(time.Second))
	})

	fmt.Fprintf(os.Stderr, "\n%d hands in %s, %d decisions, %d from the advisor\n",
		stats.Hands, stats.Elapsed.Round(time.Second), stats.Decisions, stats.AdvisorDecs)
	if stats.Hands > 0 {
		// Reported for completeness and not to be read as a result: heads-up
		// 200bb against one CFR bot is not the game the tool is for.
		fmt.Fprintf(os.Stderr, "chips %+d (Slumbot's own baseline %+d) over %d hands\n",
			stats.Winnings, stats.BaselineTotal, stats.Hands)
	}
	if n := len(stats.Anomalies); n > 0 {
		fmt.Fprintf(os.Stderr, "\n%d anomalies -- the state handed to the advisor did not describe the spot:\n", n)
		seen := map[string]int{}
		for _, a := range stats.Anomalies {
			seen[a.Reason]++
		}
		for reason, count := range seen {
			fmt.Fprintf(os.Stderr, "  %s: %d\n", reason, count)
		}
	}
	if runErr != nil {
		fail(runErr)
	}
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "slumbot:", err)
	os.Exit(1)
}
