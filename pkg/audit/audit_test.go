package audit

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"poker-game-analyzer/pkg/advisor"
	"poker-game-analyzer/pkg/table"
)

func TestBuild_NamesTheMissingInputs(t *testing.T) {
	// The spectator case seen live: cards on the board, players and stacks
	// read, but no hole cards, no bets and no reads.
	board, err := table.ParseCards("10c 8s 2c")
	if err != nil {
		t.Fatalf("parsing board: %v", err)
	}

	state := &table.HandState{
		HandID:         "live-hand",
		TableID:        "coinpoker-live",
		Street:         table.StreetFlop,
		Pot:            176000,
		CommunityCards: board,
		HeroID:         "Hero",
		Seats: []table.SeatState{
			{PlayerID: "Spock24", PlayerName: "Spock24", Stack: 179750, IsActive: true},
			{PlayerID: "majid2c", PlayerName: "majid2c", Stack: 895748, IsActive: true},
		},
	}

	rec := Build(state, nil, nil)

	for _, want := range []Gap{
		GapNoHeroCards, GapHeroNotSeated, GapNoBets,
		GapNoReads, GapPlaceholderID, GapNoActionStream,
	} {
		if !slices.Contains(rec.Gaps, want) {
			t.Errorf("expected gap %q to be reported, got %v", want, rec.Gaps)
		}
	}
	// Stacks were read, so that must not be reported as missing.
	if slices.Contains(rec.Gaps, GapNoStacks) {
		t.Errorf("stacks were present but reported missing: %v", rec.Gaps)
	}
	if len(rec.Board) != 3 {
		t.Errorf("expected 3 board cards recorded, got %v", rec.Board)
	}
}

func TestBuild_FullyObservedStateHasNoGapsBeyondHistory(t *testing.T) {
	hero, err := table.ParseCards("Qh Qd")
	if err != nil {
		t.Fatalf("parsing hero cards: %v", err)
	}

	state := &table.HandState{
		HandID:     "hand-42",
		TableID:    "t",
		Street:     table.StreetPreflop,
		Pot:        4500,
		CurrentBet: 3000,
		HeroID:     "hero",
		HeroCards:  [2]table.Card{hero[0], hero[1]},
		Seats: []table.SeatState{
			{PlayerID: "hero", Stack: 99000, CurrentBet: 1000, IsActive: true},
			{PlayerID: "villain", Stack: 97000, CurrentBet: 3000, IsActive: true},
		},
		ActionHistory: []table.ActionRecord{
			{PlayerID: "villain", Street: table.StreetPreflop, Action: table.ActionRaise, Amount: 3000},
		},
	}
	reads := map[string]map[string]float64{"villain": {"fold_to_3bet": 0.55}}

	rec := Build(state, nil, reads)

	if len(rec.Gaps) != 0 {
		t.Errorf("fully observed state should report no gaps, got %v", rec.Gaps)
	}
	if rec.Seats[1].Tendencies["fold_to_3bet"] != 0.55 {
		t.Errorf("opponent coefficients were not attached to the seat: %+v", rec.Seats[1])
	}
}

func TestLogger_CollapsesIdenticalDecisions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "decisions.jsonl")
	lg, err := NewLogger(path)
	if err != nil {
		t.Fatalf("opening logger: %v", err)
	}

	rec := Record{HandID: "h1", Street: "flop", Pot: 1000,
		Advice: &AdviceSnapshot{PrimaryAction: "check"}}

	// The capture loop repeats the same state many times per second.
	for range 50 {
		if err := lg.Log(rec); err != nil {
			t.Fatalf("logging: %v", err)
		}
	}
	changed := rec
	changed.Pot = 2000
	if err := lg.Log(changed); err != nil {
		t.Fatalf("logging: %v", err)
	}
	if err := lg.Close(); err != nil {
		t.Fatalf("closing: %v", err)
	}

	if got := lg.Written(); got != 2 {
		t.Errorf("expected 2 distinct decisions recorded, got %d", got)
	}

	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("reopening log: %v", err)
	}
	defer f.Close()

	lines := 0
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		var back Record
		if err := json.Unmarshal(sc.Bytes(), &back); err != nil {
			t.Fatalf("record %d is not valid JSON: %v", lines, err)
		}
		lines++
	}
	if err := sc.Err(); err != nil {
		t.Fatalf("scanning log: %v", err)
	}
	if lines != 2 {
		t.Errorf("expected 2 lines on disk, got %d", lines)
	}
}

func TestLogger_GapSummaryCounts(t *testing.T) {
	path := filepath.Join(t.TempDir(), "decisions.jsonl")
	lg, err := NewLogger(path)
	if err != nil {
		t.Fatalf("opening logger: %v", err)
	}
	defer lg.Close()

	for i, pot := range []float64{100, 200, 300} {
		gaps := []Gap{GapNoBets}
		if i < 2 {
			gaps = append(gaps, GapNoReads)
		}
		if err := lg.Log(Record{HandID: "h", Pot: pot, Gaps: gaps}); err != nil {
			t.Fatalf("logging: %v", err)
		}
	}

	sum := lg.GapSummary()
	if sum[GapNoBets] != 3 {
		t.Errorf("expected no_bets 3 times, got %d", sum[GapNoBets])
	}
	if sum[GapNoReads] != 2 {
		t.Errorf("expected no_reads 2 times, got %d", sum[GapNoReads])
	}
}

func TestBuild_RecordsEveryOptionEV(t *testing.T) {
	advice := &advisor.AdvisorResponse{
		PrimaryAction:     table.ActionBet,
		RecommendedAmount: 660,
		Equity:            0.85,
		Opponents:         1,
		Actions: []advisor.ActionRecommendation{
			{Action: table.ActionFold, SizingLabel: "Fold", EV: 0},
			{Action: table.ActionBet, SizingLabel: "66% Pot", Amount: 660, EV: 1010, FoldEquity: 0.46, IsPrimary: true},
		},
	}

	rec := Build(&table.HandState{HandID: "h", Street: table.StreetFlop, Pot: 1000}, advice, nil)

	if rec.Advice == nil {
		t.Fatal("advice was not recorded")
	}
	if len(rec.Advice.Options) != 2 {
		t.Fatalf("expected every option recorded, got %d", len(rec.Advice.Options))
	}
	// Knowing the runner-up's EV is what separates "picked wrong" from
	// "ranked wrong" when reviewing a session.
	if rec.Advice.Options[1].EV != 1010 || !rec.Advice.Options[1].IsPrimary {
		t.Errorf("primary option not recorded faithfully: %+v", rec.Advice.Options[1])
	}
}
