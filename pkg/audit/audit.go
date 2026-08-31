// Package audit records every decision the engine makes, together with the
// inputs it had and — more usefully — the inputs it did not.
//
// Debugging this system from the HUD alone has been the bottleneck: a
// recommendation looks plausible whether it was computed from a full table read
// or from three zeroes and a default. Each record here names its own gaps, so a
// session can be triaged by asking "which inputs were missing when the advice
// went wrong" instead of replaying screenshots by eye.
package audit

import (
	"encoding/json"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"time"

	"poker-game-analyzer/pkg/advisor"
	"poker-game-analyzer/pkg/table"
)

// Gap names an input the engine needed and did not have.
type Gap string

const (
	GapHeroNotSeated  Gap = "hero_not_seated"   // hero_id matches no seat, so hero stack is unknown
	GapNoHeroCards    Gap = "no_hero_cards"     // spectator, or hole cards unread
	GapNoBets         Gap = "no_bets"           // no current bet and no per-seat bets: pot odds cannot exist
	GapNoStacks       Gap = "no_stacks"         // no stack read anywhere: effective stack unknown
	GapNoReads        Gap = "no_reads"          // no opponent tendencies: fold equity is theoretical
	GapNoBoard        Gap = "no_board"          // postflop street with no board cards read
	GapPlaceholderID  Gap = "placeholder_hand"  // hand id is still the vision placeholder
	GapNoActionStream Gap = "no_action_history" // nothing to profile or review from

	// GapDuplicateSeats is two seats reported at the same seat number, and
	// GapImpossibleSeatCount more players than the table can hold.
	//
	// These were the most common defect in the session of 2026-08-31 and
	// neither was named at all: seat numbers collided on 217 frames out of 220,
	// and 61 frames put more than six players at a six-max table. One name came
	// back six ways -- Rafidamage also as Rafk, aage, adge, nafidamage and
	// Rafida -- and the interface button "Enter Amount" arrived as a player
	// with a stack.
	//
	// The cost is not cosmetic: the count of live opponents is what multiway
	// equity is computed against, and every extra ghost is also an unknown, so
	// the stranger tax is charged for a player who does not exist.
	GapDuplicateSeats      Gap = "duplicate_seats"       // two seats share a seat number
	GapImpossibleSeatCount Gap = "impossible_seat_count" // more seats than the table holds
)

// maxSeats is what a table can hold. Six-max is the only layout this reads, and
// a frame reporting more than this has invented somebody.
const maxSeats = 6

// SeatSnapshot is the per-player view, including the coefficients that drove
// the advice. The UI shows these beside each player, so they are recorded in
// the same shape.
type SeatSnapshot struct {
	PlayerID   string             `json:"player_id"`
	PlayerName string             `json:"player_name"`
	SeatNumber int                `json:"seat_number"`
	Stack      float64            `json:"stack"`
	CurrentBet float64            `json:"current_bet"`
	IsFolded   bool               `json:"is_folded"`
	Tendencies map[string]float64 `json:"tendencies,omitempty"`
}

// Record is one decision, self-describing enough to diagnose without the frame.
type Record struct {
	Timestamp time.Time `json:"ts"`
	TableID   string    `json:"table_id"`
	HandID    string    `json:"hand_id"`
	Street    string    `json:"street"`

	Pot        float64  `json:"pot"`
	CurrentBet float64  `json:"current_bet"`
	MinRaise   float64  `json:"min_raise"`
	Board      []string `json:"board"`
	HeroID     string   `json:"hero_id"`
	HeroCards  []string `json:"hero_cards"`

	Seats []SeatSnapshot `json:"seats"`

	// Advice is absent when none was produced, which is itself the finding.
	Advice *AdviceSnapshot `json:"advice,omitempty"`

	Gaps []Gap `json:"gaps"`
}

// AdviceSnapshot flattens the recommendation, keeping the EV of every option so
// a wrong choice can be told apart from a wrong ranking.
type AdviceSnapshot struct {
	PrimaryAction  string          `json:"primary_action"`
	Amount         float64         `json:"amount"`
	Equity         float64         `json:"equity"`
	PotOdds        float64         `json:"pot_odds"`
	EffectiveStack float64         `json:"effective_stack"`
	Opponents      int             `json:"opponents"`
	HasReads       bool            `json:"has_reads"`
	Reasoning      string          `json:"reasoning"`
	Options        []OptionSnaphot `json:"options"`
}

type OptionSnaphot struct {
	Label      string  `json:"label"`
	Action     string  `json:"action"`
	Amount     float64 `json:"amount"`
	EV         float64 `json:"ev"`
	FoldEquity float64 `json:"fold_equity"`
	IsPrimary  bool    `json:"is_primary"`
}

// Logger appends decision records to a JSONL file.
type Logger struct {
	mu   sync.Mutex
	file *os.File
	enc  *json.Encoder

	// Consecutive identical decisions are collapsed: the capture loop runs at
	// 3 fps and would otherwise bury the interesting frames under thousands of
	// duplicates.
	lastKey string
	repeats int

	gapCounts map[Gap]int
	written   int
}

// NewLogger opens (creating parent directories) the given JSONL path.
func NewLogger(path string) (*Logger, error) {
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, err
		}
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, err
	}
	return &Logger{file: f, enc: json.NewEncoder(f), gapCounts: map[Gap]int{}}, nil
}

// Log writes one decision, unless it is identical to the previous one.
func (l *Logger) Log(rec Record) error {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()

	key := decisionKey(rec)
	if key == l.lastKey {
		l.repeats++
		return nil
	}
	l.lastKey = key
	l.repeats = 0

	for _, g := range rec.Gaps {
		l.gapCounts[g]++
	}
	l.written++

	return l.enc.Encode(rec)
}

// GapSummary reports how often each gap occurred, for an end-of-session triage.
func (l *Logger) GapSummary() map[Gap]int {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make(map[Gap]int, len(l.gapCounts))
	maps.Copy(out, l.gapCounts)
	return out
}

// Written is the number of distinct decisions recorded.
func (l *Logger) Written() int {
	if l == nil {
		return 0
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.written
}

func (l *Logger) Close() error {
	if l == nil || l.file == nil {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.file.Close()
}

func decisionKey(rec Record) string {
	b, _ := json.Marshal(struct {
		H string
		S string
		P float64
		C float64
		B []string
		A string
		M float64
	}{rec.HandID, rec.Street, rec.Pot, rec.CurrentBet, rec.Board,
		primaryAction(rec), primaryAmount(rec)})
	return string(b)
}

func primaryAction(rec Record) string {
	if rec.Advice == nil {
		return ""
	}
	return rec.Advice.PrimaryAction
}

func primaryAmount(rec Record) float64 {
	if rec.Advice == nil {
		return 0
	}
	return rec.Advice.Amount
}

// Build assembles a record from the state the engine actually worked with,
// deriving the gap list rather than trusting a caller to remember them.
func Build(state *table.HandState, advice *advisor.AdvisorResponse, tendencies map[string]map[string]float64) Record {
	rec := Record{Timestamp: time.Now()}
	if state == nil {
		rec.Gaps = []Gap{GapNoHeroCards, GapNoBets, GapNoStacks, GapNoReads}
		return rec
	}

	rec.TableID = state.TableID
	rec.HandID = state.HandID
	rec.Street = string(state.Street)
	rec.Pot = state.Pot
	rec.CurrentBet = state.CurrentBet
	rec.MinRaise = state.MinRaise
	rec.HeroID = state.HeroID

	for _, c := range state.CommunityCards {
		rec.Board = append(rec.Board, c.String())
	}
	if state.HeroCards[0].Rank > 0 || state.HeroCards[1].Rank > 0 {
		rec.HeroCards = []string{state.HeroCards[0].String(), state.HeroCards[1].String()}
	}

	var (
		heroSeated  bool
		anyStack    bool
		anySeatBet  bool
		anyReads    bool
		liveOppSeen bool
	)

	for _, s := range state.Seats {
		snap := SeatSnapshot{
			PlayerID:   s.PlayerID,
			PlayerName: s.PlayerName,
			SeatNumber: s.SeatNumber,
			Stack:      s.Stack,
			CurrentBet: s.CurrentBet,
			IsFolded:   s.IsFolded,
		}
		if t, ok := tendencies[s.PlayerID]; ok && len(t) > 0 {
			snap.Tendencies = t
			anyReads = true
		}
		if s.Stack > 0 {
			anyStack = true
		}
		if s.CurrentBet > 0 {
			anySeatBet = true
		}
		if s.PlayerID == state.HeroID && state.HeroID != "" {
			heroSeated = true
		} else if s.IsActive && !s.IsFolded {
			liveOppSeen = true
		}
		rec.Seats = append(rec.Seats, snap)
	}

	if advice != nil {
		snap := &AdviceSnapshot{
			PrimaryAction:  string(advice.PrimaryAction),
			Amount:         advice.RecommendedAmount,
			Equity:         advice.Equity,
			PotOdds:        advice.PotOdds,
			EffectiveStack: advice.EffectiveStack,
			Opponents:      advice.Opponents,
			HasReads:       advice.HasReads,
			Reasoning:      advice.Reasoning,
		}
		for _, a := range advice.Actions {
			snap.Options = append(snap.Options, OptionSnaphot{
				Label:      a.SizingLabel,
				Action:     string(a.Action),
				Amount:     a.Amount,
				EV:         a.EV,
				FoldEquity: a.FoldEquity,
				IsPrimary:  a.IsPrimary,
			})
		}
		rec.Advice = snap
	}

	var gaps []Gap
	if len(rec.HeroCards) == 0 {
		gaps = append(gaps, GapNoHeroCards)
	}
	if !heroSeated {
		gaps = append(gaps, GapHeroNotSeated)
	}
	if state.CurrentBet == 0 && !anySeatBet {
		gaps = append(gaps, GapNoBets)
	}
	if !anyStack {
		gaps = append(gaps, GapNoStacks)
	}
	if liveOppSeen && !anyReads {
		gaps = append(gaps, GapNoReads)
	}
	if state.Street != table.StreetPreflop && len(rec.Board) == 0 {
		gaps = append(gaps, GapNoBoard)
	}
	if state.HandID == "" || state.HandID == "live-hand" {
		gaps = append(gaps, GapPlaceholderID)
	}
	if len(state.ActionHistory) == 0 {
		gaps = append(gaps, GapNoActionStream)
	}
	// Seat numbers are how the parser keys a player between frames, so a
	// collision does not merely duplicate a row: it merges two players into one
	// history and loses whichever arrived first.
	//
	// Checked only when the field is in use. A state whose seats are all at
	// number zero has not numbered them at all -- the harness builds states
	// that way, and so do most tests -- and calling that a collision would
	// report a defect on every well-formed frame that happens not to care about
	// seat order.
	numbered := false
	for _, s := range state.Seats {
		if s.SeatNumber != 0 {
			numbered = true
			break
		}
	}
	if numbered {
		seen := make(map[int]bool, len(state.Seats))
		for _, s := range state.Seats {
			if seen[s.SeatNumber] {
				gaps = append(gaps, GapDuplicateSeats)
				break
			}
			seen[s.SeatNumber] = true
		}
	}
	if len(state.Seats) > maxSeats {
		gaps = append(gaps, GapImpossibleSeatCount)
	}

	slices.Sort(gaps)
	rec.Gaps = gaps

	return rec
}
