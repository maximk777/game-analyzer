package sim

import (
	"math/rand"
	"slices"

	"poker-game-analyzer/pkg/advice"
	"poker-game-analyzer/pkg/table"
)

// Tool is the thing under test: the real advisor, seated as a player.
//
// It reaches its decision through pkg/advice, the same entry point the live
// server uses, and it is handed the same table.HandState the screen reader
// produces. Nothing about the strategy is reimplemented here -- this file only
// translates a recommendation into a move and gathers the reads the live
// profiler would have gathered.
type Tool struct {
	// Tracker supplies opponent tendencies; nil means the tool plays blind.
	Tracker *Tracker
	Level   ReadLevel
	Opt     advice.Options

	label string
	rng   *rand.Rand
	last  advice.Result

	// phases counts decisions by how well the table was understood at the time,
	// and knowledgeSum averages it. The three waves -- learn the table, start
	// using it, press -- are a claim about behaviour over a session, and a
	// claim about behaviour has to be visible in the report or it is a story.
	phases       map[string]int
	knowledgeSum float64
	decisions    int
	// noAdvice counts turns where the pipeline declined to answer. In the
	// harness that should be zero, and if it is not, the harness is handing the
	// tool states it would refuse live -- which is worth knowing before any
	// number from the run is believed.
	noAdvice int

	// Noise is the screen reader's failure rate. The zero value hands the tool
	// the engine's own state, which is how every run before dirty.go worked.
	// Anything else means the tool decides on a corrupted view while the engine
	// keeps the truth, which is the live arrangement.
	Noise Noise
	flips FlipReport
}

// Flips is how often the noise changed this tool's mind. Empty unless
// Noise.MeasureFlips was set.
func (t *Tool) Flips() FlipReport { return t.flips }

// NewTool seats the advisor. The label distinguishes configurations of it in a
// report, since several usually run against each other.
func NewTool(label string, tracker *Tracker, level ReadLevel, opt advice.Options) *Tool {
	t := &Tool{Tracker: tracker, Level: level, Opt: opt, label: label, rng: opt.Rng}
	if t.rng == nil {
		t.rng = rand.New(rand.NewSource(1))
	}
	return t
}

func (t *Tool) Name() string { return t.label }

// Observer hands back the tracker so the table can feed it. Without this a tool
// seated as an opponent would never learn anything about the table it is at.
func (t *Tool) Observer() Observer {
	if t.Tracker == nil {
		return nil
	}
	return t.Tracker
}

// LastAdvice is the recommendation behind the most recent move, for a caller
// that wants the reasoning as well as the action. Valid only until the next
// call to Act.
func (t *Tool) LastAdvice() advice.Result { return t.last }

// NoAdviceCount is how many turns the pipeline declined to answer.
func (t *Tool) NoAdviceCount() int { return t.noAdvice }

// Phases is how many decisions were taken at each level of knowledge about the
// table, and MeanKnowledge the average of that knowledge.
func (t *Tool) Phases() map[string]int { return t.phases }

// MeanKnowledge is the average table knowledge behind this tool's decisions.
func (t *Tool) MeanKnowledge() float64 {
	if t.decisions == 0 {
		return 0
	}
	return t.knowledgeSum / float64(t.decisions)
}

// readsFor is what the tracker knows about the players in this state. Passing
// the state in rather than reading s.State is what lets the clean and the dirty
// pass ask the same question of different tables: a ghost seat looks up an id
// the tracker has never seen and contributes nothing, which is the correct
// answer rather than a missing one.
func (t *Tool) readsFor(st table.HandState, lost bool) advice.Reads {
	reads := advice.Reads{}
	if t.Tracker == nil || t.Level == ReadsOff || lost {
		return reads
	}
	reads.Tendencies = make(map[string]map[string]float64)
	reads.RangeWidth = make(map[string]float64)
	for _, seat := range st.Seats {
		if seat.PlayerID == st.HeroID || seat.IsFolded {
			continue
		}
		if td := t.Tracker.Tendencies(seat.PlayerID, t.Level); len(td) > 0 {
			reads.Tendencies[seat.PlayerID] = td
		}
		if w := t.Tracker.VPIP(seat.PlayerID, t.Level); w > 0 {
			reads.RangeWidth[seat.PlayerID] = w
		}
	}
	return reads
}

func (t *Tool) Act(s Spot) Move {
	// The state the tool decides on is not necessarily the state the engine is
	// holding. Corrupting here rather than in the engine is the whole point:
	// the hand plays out on the truth, the advice is formed on the view, and
	// the difference between the two is what the run is measuring.
	dirty, fired := t.Noise.Corrupt(s.State, t.rng)
	readsLost := slices.Contains(fired, DefectReadsLost)

	// Walked over the dirty seats, not the engine's: a ghost has an id nothing
	// has ever seen, so it comes back with no read, and being an unknown at the
	// table is exactly what a ghost costs.
	reads := t.readsFor(dirty, readsLost)

	// The tool's Monte Carlo draws from its own stream, never from the one
	// that deals the cards -- otherwise how many samples it happened to take
	// would change what the next hand was dealt, and two strategies could not
	// be compared on the same deck.
	opt := t.Opt
	opt.Rng = t.rng
	st := dirty
	res := advice.Evaluate(&st, reads, opt)
	t.last = res

	// Stability is measured against the clean state rather than against the
	// previous decision: the question is how often the screen reader turns the
	// right answer into a different one, and only the engine knows the right
	// answer. The clean pass is thrown away -- the dirty answer is the one
	// played, because that is the one a player would have been shown.
	if t.Noise.MeasureFlips && t.Noise.Any() {
		clean := s.State
		cleanRes := advice.Evaluate(&clean, t.readsFor(s.State, false), opt)
		if res.Recommendation != nil && cleanRes.Recommendation != nil {
			t.flips.record(
				cleanRes.Recommendation.PrimaryAction, res.Recommendation.PrimaryAction,
				cleanRes.Recommendation.RecommendedAmount, res.Recommendation.RecommendedAmount,
				fired,
			)
		}
	}

	if res.Recommendation == nil {
		t.noAdvice++
		if s.ToCall <= 0 {
			return Move{Action: table.ActionCheck}
		}
		return Move{Action: table.ActionFold}
	}

	r := res.Recommendation
	if t.phases == nil {
		t.phases = map[string]int{}
	}
	t.phases[r.Phase]++
	t.knowledgeSum += r.TableKnowledge
	t.decisions++

	switch r.PrimaryAction {
	case table.ActionFold:
		return Move{Action: table.ActionFold}
	case table.ActionCheck:
		return Move{Action: table.ActionCheck}
	case table.ActionCall:
		return Move{Action: table.ActionCall}
	case table.ActionAllIn:
		return Move{Action: table.ActionAllIn}
	default:
		// Bet and raise are the same move to the engine: chips added now.
		// A size at or beyond what can still be wagered is a shove, and saying
		// so keeps the engine from having to guess.
		if r.RecommendedAmount >= s.MaxRaise && s.MaxRaise > 0 {
			return Move{Action: table.ActionAllIn}
		}
		return Move{Action: table.ActionRaise, Amount: r.RecommendedAmount}
	}
}

// HarnessOptions is the pipeline setting a long run uses.
//
// Fewer samples than live, deliberately. The live path spends 12,000 iterations
// on the headline number because one decision every hundred milliseconds can
// afford it; a run of a hundred thousand hands makes that same choice a quarter
// of a million times. The count below keeps the sampling error on an equity
// figure near half a per cent, which is well inside the difference any change
// worth making produces -- and the number of hands is what the confidence
// interval actually depends on.
func HarnessOptions(rng *rand.Rand) advice.Options {
	return advice.Options{Iterations: 2500, VsTopIterations: 1500, Rng: rng}
}
