package main

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"poker-game-analyzer/pkg/sim"
)

// Keeping the answer, so the next run does not have to ask the question again.
//
// Until now a run printed to the terminal and died there. Every "is this
// better" therefore meant replaying the baseline as well, and a session spent
// half its time re-measuring things it had already measured. Worse, the
// baseline being re-measured came from whatever the tree happened to contain,
// so two numbers compared across an afternoon were not necessarily numbers
// about the same program.
//
// A line per run fixes both. It carries the commit, whether the tree was dirty
// when it ran, and the configuration that produced it -- so a later run can
// find the last run of the same shape and say what moved, and a comparison
// against a dirty tree announces itself instead of quietly meaning nothing.

// ledgerEntry is one recorded run.
type ledgerEntry struct {
	At      string         `json:"at"`
	Commit  string         `json:"commit"`
	Dirty   bool           `json:"dirty"`
	Config  ledgerConfig   `json:"config"`
	Results []ledgerResult `json:"results"`
}

// ledgerConfig is everything that has to match for two runs to be comparable.
// Its string form is the key a run is looked up by.
type ledgerConfig struct {
	Hands      int     `json:"hands"`
	Lineups    int     `json:"lineups"`
	Seed       int64   `json:"seed"`
	Seats      int     `json:"seats"`
	StackMinBB float64 `json:"stack_min_bb"`
	StackMaxBB float64 `json:"stack_max_bb"`
	Reads      string  `json:"reads"`
	Field      string  `json:"field"`
	Warmup     int     `json:"warmup"`
	Churn      float64 `json:"churn"`
	AllInEV    bool    `json:"allin_ev"`
}

// Key is the shape of a run. Two entries with the same key measured the same
// thing and may be compared; two with different keys may not.
func (c ledgerConfig) Key() string {
	return fmt.Sprintf("h%d/l%d/s%d/seats%d/%g-%g/%s/f=%s/w%d/c%g/ev=%t",
		c.Hands, c.Lineups, c.Seed, c.Seats, c.StackMinBB, c.StackMaxBB,
		c.Reads, c.Field, c.Warmup, c.Churn, c.AllInEV)
}

type ledgerResult struct {
	Label        string  `json:"label"`
	BB100        float64 `json:"bb100"`
	StdErr       float64 `json:"stderr"`
	PairedDiff   float64 `json:"paired_diff"`
	PairedStdErr float64 `json:"paired_stderr"`
	Paired       bool    `json:"paired"`
	// DivergedShare is the fraction of hands this candidate finished
	// differently from the baseline -- the effective sample behind the paired
	// figure. See sim.Result.Divergence.
	DivergedShare float64 `json:"diverged_share"`
	Hands         int     `json:"hands"`
}

// gitState is the commit the run measured, and whether anything was uncommitted
// at the time. A number from a dirty tree is not attributable to a commit, and
// saying so is the whole reason the field exists.
func gitState() (commit string, dirty bool) {
	out, err := exec.Command("git", "rev-parse", "--short", "HEAD").Output()
	if err != nil {
		return "", false
	}
	commit = strings.TrimSpace(string(out))
	st, err := exec.Command("git", "status", "--porcelain").Output()
	if err != nil {
		return commit, false
	}
	return commit, len(strings.TrimSpace(string(st))) > 0
}

func buildEntry(rep sim.Report, cfg ledgerConfig) ledgerEntry {
	commit, dirty := gitState()
	e := ledgerEntry{
		At:     time.Now().UTC().Format(time.RFC3339),
		Commit: commit,
		Dirty:  dirty,
		Config: cfg,
	}
	for _, r := range rep.Results {
		rate, se := r.BB100()
		row := ledgerResult{Label: r.Label, BB100: rate, StdErr: se, Hands: len(r.Nets)}
		if diff, dse, ok := r.PairedDiff(rep.Baseline); ok && r != rep.Baseline {
			row.PairedDiff, row.PairedStdErr, row.Paired = diff, dse, true
		}
		if share, _, ok := r.Divergence(rep.Baseline); ok {
			row.DivergedShare = share
		}
		e.Results = append(e.Results, row)
	}
	return e
}

// appendLedger writes the entry and returns the most recent earlier entry of
// the same shape, if there was one.
func appendLedger(path string, e ledgerEntry) (prev *ledgerEntry, err error) {
	prev = lastMatching(path, e.Config.Key())

	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return prev, err
		}
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return prev, err
	}
	defer f.Close()

	line, err := json.Marshal(e)
	if err != nil {
		return prev, err
	}
	_, err = f.Write(append(line, '\n'))
	return prev, err
}

// lastMatching is the newest recorded run with the same configuration, or nil.
func lastMatching(path, key string) *ledgerEntry {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var found *ledgerEntry
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var e ledgerEntry
		if json.Unmarshal([]byte(line), &e) != nil {
			continue
		}
		if e.Config.Key() != key {
			continue
		}
		cp := e
		found = &cp
	}
	return found
}

// compareToPrevious describes what moved since the last run of the same shape.
//
// It compares each candidate to its own earlier self, which is the question a
// ledger is kept to answer: not "is this candidate better than the baseline"
// -- the report already says that -- but "did what I just changed move it".
//
// The interval printed is the two runs' standard errors added in quadrature.
// They are independent runs on the same decks against the same field, so their
// errors do not cancel the way a paired difference does, and pretending
// otherwise would turn every rerun into a discovery.
func compareToPrevious(e ledgerEntry, prev *ledgerEntry) string {
	if prev == nil {
		return "\nжурнал: первый прогон этой конфигурации, сравнивать не с чем\n"
	}

	var b strings.Builder
	fmt.Fprintf(&b, "\nжурнал: против прогона %s", prev.At)
	if prev.Commit != "" {
		fmt.Fprintf(&b, " на %s", prev.Commit)
		if prev.Dirty {
			b.WriteString(" (дерево было грязным)")
		}
	}
	b.WriteString("\n")

	before := make(map[string]ledgerResult, len(prev.Results))
	for _, r := range prev.Results {
		before[r.Label] = r
	}
	for _, r := range e.Results {
		p, ok := before[r.Label]
		if !ok {
			fmt.Fprintf(&b, "  %-28s %8.2f  (новый кандидат)\n", r.Label, r.BB100)
			continue
		}
		delta := r.BB100 - p.BB100
		band := math.Hypot(r.StdErr, p.StdErr)
		verdict := "в шуме"
		if math.Abs(delta) > 2*band {
			verdict = "* хуже"
			if delta > 0 {
				verdict = "* лучше"
			}
		}
		fmt.Fprintf(&b, "  %-28s %8.2f  было %8.2f   %+7.2f ± %5.2f  %s\n",
			r.Label, r.BB100, p.BB100, delta, band, verdict)
	}
	return b.String()
}

// regressed reports whether a named candidate is more than two combined
// standard errors below where it was in the previous run of the same shape.
// This is what a build gate asks: not "is it good" but "did I just break it".
func regressed(e ledgerEntry, prev *ledgerEntry, label string) (bad bool, msg string) {
	if prev == nil {
		return false, fmt.Sprintf("сторож: %q — первый прогон этой конфигурации, сравнивать не с чем", label)
	}
	var now, was *ledgerResult
	for i := range e.Results {
		if e.Results[i].Label == label {
			now = &e.Results[i]
		}
	}
	for i := range prev.Results {
		if prev.Results[i].Label == label {
			was = &prev.Results[i]
		}
	}
	if now == nil {
		return true, fmt.Sprintf("сторож: кандидата %q нет в этом прогоне", label)
	}
	if was == nil {
		return false, fmt.Sprintf("сторож: кандидата %q не было в прошлом прогоне", label)
	}
	delta := now.BB100 - was.BB100
	band := math.Hypot(now.StdErr, was.StdErr)
	if delta < -2*band {
		return true, fmt.Sprintf("сторож: %s просел на %.2f bb/100 (%.2f -> %.2f, интервал %.2f)",
			label, -delta, was.BB100, now.BB100, band)
	}
	return false, fmt.Sprintf("сторож: %s в порядке (%+.2f ± %.2f)", label, delta, band)
}
