package sim

import (
	"fmt"
	"math"
	"math/rand"
	"runtime"
	"sort"
	"strings"
	"sync"

	"poker-game-analyzer/pkg/table"
)

// The harness.
//
// It answers one question -- would somebody following this advice come out
// ahead -- and it has to answer it against the variance of no-limit hold'em,
// where a hand's result has a standard deviation of roughly one big blind per
// hand. Measured naively, telling a strategy that wins 5 bb/100 from one that
// loses 5 bb/100 needs on the order of a hundred thousand hands before the
// confidence intervals stop overlapping.
//
// Two things are done about that, and they are the reason this is a harness and
// not a loop around PlayHand:
//
//   - Duplicate cards. Every candidate strategy plays the same decks, in the
//     same seat, against the same opponents. The comparison is then made on the
//     per-hand *difference*, which cancels most of the card luck: the same
//     aces, the same coolers, the same runouts on both sides.
//   - Fixed stacks per hand. Stacks reset at the start of every hand from the
//     lineup's configuration, so a strategy that busts early does not go on to
//     play a different game from the one it is being compared against, and
//     stack depth stays a variable the harness sets rather than one the results
//     drift into.
//
// What comes out is a bb/100 for each candidate with an interval around it, a
// paired difference against the baseline that is far tighter than either, and a
// breakdown of where the money went.

// Candidate is a strategy to seat in the hero chair.
type Candidate struct {
	Label string
	// New builds a fresh instance. The tracker is the one watching this run, so
	// a strategy that wants opponent reads can take them from it.
	New func(seed int64, tr *Tracker) Agent
}

// RunConfig describes the whole experiment.
type RunConfig struct {
	// Hands recorded per lineup, per candidate.
	Hands int
	// Lineups is how many random table compositions to draw. More lineups with
	// fewer hands each covers more of the population; fewer with more hands
	// measures each one more precisely.
	Lineups int
	Seed    int64
	// Candidates: the first one is the baseline every other is compared to.
	Candidates []Candidate
	// Seats at the table, hero included.
	Seats int
	// StackMinBB and StackMaxBB bound the starting stacks drawn per seat, so a
	// run covers short, standard and deep play rather than one depth.
	StackMinBB, StackMaxBB float64
	// Level is how much the tool is told about its opponents.
	Level ReadLevel
	// Warmup hands are played and discarded before recording, so that a
	// strategy relying on reads is not measured over the orbit it spent
	// gathering them.
	Warmup  int
	Workers int
	Cfg     Config
	// HeroStackBB, when set, gives hero a different stack from the field. The
	// case it exists for is the real one: sitting down with ten dollars at a
	// table where everybody else has thirty.
	HeroStackBB float64

	// CarryStacks makes stacks persist across the hands of a session instead of
	// resetting to the lineup's configuration every hand.
	//
	// Resetting is right for measuring a win rate -- it makes hands independent
	// and keeps stack depth a variable the harness sets -- and it is precisely
	// wrong for the question "can this turn ten dollars into forty". That
	// question is about a trajectory: whether the strategy compounds, and
	// whether it busts before it can. With stacks carried the two questions can
	// both be asked, of the same strategy, off the same decks.
	//
	// Hero busting ends the session unless there are buy-ins left. The
	// opponents top up, the way a table refills around a player who has just
	// lost a stack.
	CarryStacks bool

	// SeatChurn is the chance, per hand, that one opponent gets up and a
	// stranger sits down in their place.
	//
	// Online tables are not a fixed cast. Somebody leaves, somebody new sits
	// down, and for the next hour that seat is a dark horse: the tool knows
	// nothing about them and everything it thinks it knows about the table is
	// now partly about somebody who is no longer at it. A harness whose six
	// players never change measures a strategy in a game nobody plays, and it
	// measures the one thing a learning strategy is best at -- accumulating a
	// read that never goes stale.
	//
	// The replacement is a fresh draw from the population with a new identity,
	// so the reads on them start empty. Their stack is the seat's stack: the
	// money stays at the table even when the player does not.
	SeatChurn float64

	// BuyIns is how many stacks hero has behind them. One is a player sitting
	// down with their whole bankroll on the table, and it is the wrong thing to
	// measure a strategy with.
	//
	// Measured: with a single buy-in, every strategy busts most sessions --
	// the tool 78% of the time, the strongest opponent 93%, a simple
	// tight-aggressive bot 96% -- and that ordering is the only signal in it.
	// The bust rate itself is arithmetic, not strategy: at a hundred big blinds
	// of bankroll, a standard deviation near a hundred big blinds per hundred
	// hands and a win rate of twenty, the risk of ruin is about two thirds
	// whatever anybody does. A strategy is worth judging on money it is
	// possible to survive with.
	BuyIns int

	// Field, when set, fixes the opponents instead of drawing them from the
	// population. Used to ask "what does this strategy do against a table of
	// regulars", which is a different question from "against the population".
	Field []Opponent
}

// Report is everything the run measured.
type Report struct {
	Cfg      RunConfig
	Results  []*Result
	Baseline *Result
}

// Result is one candidate's record.
type Result struct {
	Label string
	// Net per recorded hand, in big blinds, in a stable order: lineup-major,
	// so the same index means the same deck for every candidate.
	Nets []float64

	Style   StyleReport
	Buckets map[string]*Bucket
	// Sizings is every bet or raise the strategy made, as a multiple of the
	// pot, so an overbetting habit shows up as a number. Kept per street,
	// because pooling them hides everything: a standard preflop open is 1.67
	// times a pot of one and a half blinds, so the pooled median is a preflop
	// open however the strategy plays after the flop.
	Sizings   []float64
	SizingsBy map[table.Street][]float64
	// NoAdvice counts turns where the pipeline declined to answer.
	NoAdvice int
	// Phases counts decisions by how well the table was understood at the time.
	Phases map[string]int
	// KnowledgeSum over decisions, for the average.
	KnowledgeSum float64
	Decisions    int

	// Sessions is one entry per lineup when stacks are carried: what happened
	// to hero's money over a whole sitting.
	Sessions []SessionOutcome
}

// SessionOutcome is one sitting: what hero sat down with, what they got up
// with, and how close they came to losing it.
type SessionOutcome struct {
	// StartBB and FinalBB are the whole bankroll -- what is on the table plus
	// what is behind -- so a session that rebought three times and ended even
	// reads as even and not as a triumph.
	StartBB, FinalBB float64
	PeakBB, TroughBB float64
	// BuyIn is one stack, so growth can be read in buy-ins as well as in blinds.
	BuyIn  float64
	Hands  int
	Rebuys int
	Busted bool
}

// StyleReport is the strategy's own measured profile: the same statistics it
// would be profiled by if it sat down at a table being watched. Counts, not
// rates -- the rates are derived, so that merging two partial runs is addition
// rather than a weighted average that has to know the weights.
type StyleReport struct {
	Hands int

	VPIPHands     int
	PFRHands      int
	ThreeBetHands int
	ThreeBetOpps  int

	AggrActions int // postflop bets and raises
	CallActions int // postflop calls

	ShowdownHands      int
	WonShowdown        int
	WonWithoutShowdown int
	AllIns             int

	Actions       map[table.ActionType]int
	StreetActions map[string]int
}

func pct(a, b int) float64 {
	if b == 0 {
		return 0
	}
	return float64(a) / float64(b) * 100
}

// VPIP, PFR, ThreeBet and WTSD are percentages; AF is the ratio of postflop
// aggression to postflop calling, and W$SD the share of showdowns won.
func (s StyleReport) VPIP() float64     { return pct(s.VPIPHands, s.Hands) }
func (s StyleReport) PFR() float64      { return pct(s.PFRHands, s.Hands) }
func (s StyleReport) ThreeBet() float64 { return pct(s.ThreeBetHands, s.ThreeBetOpps) }
func (s StyleReport) WTSD() float64     { return pct(s.ShowdownHands, s.Hands) }
func (s StyleReport) WonAtSD() float64  { return pct(s.WonShowdown, s.ShowdownHands) }
func (s StyleReport) AF() float64 {
	if s.CallActions == 0 {
		return float64(s.AggrActions)
	}
	return float64(s.AggrActions) / float64(s.CallActions)
}

// Bucket is one kind of spot: how often it came up, and how the hands
// containing it turned out.
type Bucket struct {
	Key   string
	Hands int
	Net   float64
}

// Run plays the whole experiment and returns the report.
func Run(cfg RunConfig) Report {
	if cfg.Seats <= 0 {
		cfg.Seats = 6
	}
	if cfg.Workers <= 0 {
		cfg.Workers = runtime.NumCPU()
	}
	if cfg.Cfg.BigBlind == 0 {
		cfg.Cfg = DefaultConfig()
	}
	if cfg.StackMinBB <= 0 {
		cfg.StackMinBB = 100
	}
	if cfg.StackMaxBB < cfg.StackMinBB {
		cfg.StackMaxBB = cfg.StackMinBB
	}

	type job struct{ lineup, cand int }
	var jobs []job
	for l := 0; l < cfg.Lineups; l++ {
		for c := range cfg.Candidates {
			jobs = append(jobs, job{l, c})
		}
	}

	// One slot per (candidate, lineup) so results land in a deterministic
	// order however the workers interleave.
	slots := make([][]*Result, len(cfg.Candidates))
	for i := range slots {
		slots[i] = make([]*Result, cfg.Lineups)
	}

	var wg sync.WaitGroup
	ch := make(chan job)
	for w := 0; w < cfg.Workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range ch {
				slots[j.cand][j.lineup] = runLineup(cfg, j.lineup, cfg.Candidates[j.cand])
			}
		}()
	}
	for _, j := range jobs {
		ch <- j
	}
	close(ch)
	wg.Wait()

	rep := Report{Cfg: cfg}
	for c, cand := range cfg.Candidates {
		merged := &Result{Label: cand.Label, Buckets: map[string]*Bucket{}}
		merged.Style.Actions = map[table.ActionType]int{}
		merged.Style.StreetActions = map[string]int{}
		for _, part := range slots[c] {
			if part == nil {
				continue
			}
			merged.Nets = append(merged.Nets, part.Nets...)
			merged.Sizings = append(merged.Sizings, part.Sizings...)
			if merged.SizingsBy == nil {
				merged.SizingsBy = map[table.Street][]float64{}
			}
			for st, v := range part.SizingsBy {
				merged.SizingsBy[st] = append(merged.SizingsBy[st], v...)
			}
			merged.NoAdvice += part.NoAdvice
			merged.KnowledgeSum += part.KnowledgeSum
			merged.Decisions += part.Decisions
			for k, v := range part.Phases {
				if merged.Phases == nil {
					merged.Phases = map[string]int{}
				}
				merged.Phases[k] += v
			}
			merged.Sessions = append(merged.Sessions, part.Sessions...)
			mergeStyle(&merged.Style, part.Style)
			for k, b := range part.Buckets {
				m, ok := merged.Buckets[k]
				if !ok {
					m = &Bucket{Key: k}
					merged.Buckets[k] = m
				}
				m.Hands += b.Hands
				m.Net += b.Net
			}
		}
		rep.Results = append(rep.Results, merged)
	}
	if len(rep.Results) > 0 {
		rep.Baseline = rep.Results[0]
	}
	return rep
}

// lineupFor draws a table composition. It depends only on the run seed and the
// lineup index, so every candidate meets exactly the same opponents with
// exactly the same stacks.
func lineupFor(cfg RunConfig, lineup int) (opps []Opponent, stacks []Chips, heroSeat int) {
	rng := rand.New(rand.NewSource(cfg.Seed*1_000_003 + int64(lineup)))
	heroSeat = rng.Intn(cfg.Seats)
	for i := 0; i < cfg.Seats; i++ {
		if len(cfg.Field) > 0 {
			opps = append(opps, cfg.Field[i%len(cfg.Field)])
		} else {
			opps = append(opps, DrawOpponent(rng))
		}
		bb := cfg.StackMinBB + rng.Float64()*(cfg.StackMaxBB-cfg.StackMinBB)
		if i == heroSeat && cfg.HeroStackBB > 0 {
			bb = cfg.HeroStackBB
		}
		stacks = append(stacks, Chips(bb*float64(cfg.Cfg.BigBlind)))
	}
	return opps, stacks, heroSeat
}

func runLineup(cfg RunConfig, lineup int, cand Candidate) *Result {
	opps, stacks, heroSeat := lineupFor(cfg, lineup)
	seed := cfg.Seed*1_000_003 + int64(lineup)

	tracker := NewTracker()
	heroAgent := cand.New(seed*31+7, tracker)

	players := make([]*Player, cfg.Seats)
	for i := 0; i < cfg.Seats; i++ {
		id := fmt.Sprintf("p%d", i)
		if i == heroSeat {
			players[i] = &Player{ID: id, Name: "hero", Agent: heroAgent, Stack: stacks[i]}
			continue
		}
		players[i] = &Player{
			ID: id, Name: opps[i].Name, Stack: stacks[i],
			Agent: opps[i].New(rand.New(rand.NewSource(seed + int64(i)*7919))),
		}
	}

	// The deck stream depends only on the lineup, never on the candidate.
	tb := NewTable(fmt.Sprintf("L%d", lineup), cfg.Cfg, players, rand.New(rand.NewSource(seed)))

	res := &Result{Label: cand.Label, Buckets: map[string]*Bucket{}}
	res.Style.Actions = map[table.ActionType]int{}
	res.Style.StreetActions = map[string]int{}
	col := &collector{res: res, heroID: players[heroSeat].ID, bb: float64(cfg.Cfg.BigBlind)}

	// Every agent that learns gets fed. Hero's tool shares the lineup tracker;
	// a tool seated as an opponent brings its own, and must see the table too
	// or it plays every hand as though it had just sat down.
	observers := rebuildObservers(nil, col, players, tracker)
	tb.SetObserver(observers)

	buyIns := cfg.BuyIns
	if buyIns < 1 {
		buyIns = 1
	}
	buyIn := stacks[heroSeat]
	reserve := Chips(buyIns-1) * buyIn
	rollBB := func() float64 {
		return float64(players[heroSeat].Stack+reserve) / float64(cfg.Cfg.BigBlind)
	}
	session := SessionOutcome{
		StartBB:  float64(buyIn) * float64(buyIns) / float64(cfg.Cfg.BigBlind),
		TroughBB: float64(buyIn) * float64(buyIns) / float64(cfg.Cfg.BigBlind),
		PeakBB:   float64(buyIn) * float64(buyIns) / float64(cfg.Cfg.BigBlind),
		BuyIn:    float64(buyIn) / float64(cfg.Cfg.BigBlind),
	}
	churn := rand.New(rand.NewSource(seed * 104729))
	newcomers := 0
	for i := 0; i < cfg.Warmup+cfg.Hands; i++ {
		col.recording = i >= cfg.Warmup

		// Somebody gets up, somebody else sits down.
		if cfg.SeatChurn > 0 && churn.Float64() < cfg.SeatChurn {
			seat := churn.Intn(cfg.Seats)
			if seat == heroSeat {
				seat = (seat + 1) % cfg.Seats
			}
			newcomers++
			opp := DrawOpponent(churn)
			if len(cfg.Field) > 0 {
				opp = cfg.Field[churn.Intn(len(cfg.Field))]
			}
			players[seat] = &Player{
				// A new identity, so every read on the old occupant is gone.
				ID:    fmt.Sprintf("p%d-n%d", seat, newcomers),
				Name:  opp.Name,
				Stack: players[seat].Stack,
				Agent: opp.New(rand.New(rand.NewSource(seed + int64(newcomers)*1299709))),
			}
			stacks[seat] = players[seat].Stack
			tb.Reseat(seat, players[seat])
			observers = rebuildObservers(observers, col, players, tracker)
			tb.SetObserver(observers)
		}
		for s := 0; s < cfg.Seats; s++ {
			if cfg.CarryStacks && s == heroSeat {
				// Hero's money is the thing being measured, so it is the one
				// stack that is not put back.
				continue
			}
			players[s].Stack = stacks[s]
		}
		if cfg.CarryStacks && players[heroSeat].Stack <= 0 {
			// Sit back down out of what is behind, or the session is over.
			if reserve < buyIn {
				session.Busted = true
				break
			}
			reserve -= buyIn
			players[heroSeat].Stack = buyIn
			session.Rebuys++
		}
		tb.PlayHand()
		session.Hands++
		if cfg.CarryStacks {
			bb := rollBB()
			if bb > session.PeakBB {
				session.PeakBB = bb
			}
			if bb < session.TroughBB {
				session.TroughBB = bb
			}
		}
	}
	if cfg.CarryStacks {
		session.FinalBB = rollBB()
		if players[heroSeat].Stack <= 0 && reserve < buyIn {
			session.Busted = true
		}
		res.Sessions = append(res.Sessions, session)
	}
	if t, ok := heroAgent.(*Tool); ok {
		res.NoAdvice = t.NoAdviceCount()
		res.Phases = t.Phases()
		res.KnowledgeSum = t.MeanKnowledge() * float64(t.decisions)
		res.Decisions = t.decisions
	}
	return res
}

// rebuildObservers wires up everybody at the table who learns from watching it.
//
// Hero's tool shares the lineup tracker; a tool seated as an opponent brings its
// own, and must be fed or it plays every hand as though it had just sat down.
// It is rebuilt rather than appended to whenever a seat changes, so that a
// departed player's tracker stops being fed -- otherwise a stranger would
// inherit the reads of the person whose chair they took.
func rebuildObservers(_ multiObserver, col Observer, players []*Player, tracker *Tracker) multiObserver {
	out := multiObserver{col}
	seen := map[Observer]bool{}
	for _, p := range players {
		w, ok := p.Agent.(Watcher)
		if !ok {
			continue
		}
		o := w.Observer()
		if o == nil || seen[o] {
			continue
		}
		seen[o] = true
		out = append(out, o)
	}
	if !seen[Observer(tracker)] {
		out = append(out, tracker)
	}
	return out
}

type multiObserver []Observer

func (m multiObserver) OnDecision(d DecisionRecord) {
	for _, o := range m {
		o.OnDecision(d)
	}
}
func (m multiObserver) OnHandEnd(r HandResult) {
	for _, o := range m {
		o.OnHandEnd(r)
	}
}

// collector turns the stream of decisions and results into the report.
type collector struct {
	res       *Result
	heroID    string
	bb        float64
	recording bool

	// per-hand scratch
	spots     map[string]bool
	vpip      bool
	pfr       bool
	tb, tbOpp bool
	folded    bool
	aggr      int
	pass      int
}

func (c *collector) OnDecision(d DecisionRecord) {
	if !c.recording || d.PlayerID != c.heroID {
		return
	}
	if c.spots == nil {
		c.spots = map[string]bool{}
	}
	st := c.res
	act := d.Move.Action
	st.Style.Actions[act]++
	st.Style.StreetActions[fmt.Sprintf("%s/%s", d.Street, act)]++

	aggressive := act == table.ActionBet || act == table.ActionRaise || act == table.ActionAllIn
	if act == table.ActionAllIn {
		st.Style.AllIns++
	}
	if act == table.ActionFold {
		c.folded = true
	}
	if aggressive && d.Invested > 0 {
		pot := d.Spot.State.Pot
		if pot > 0 {
			size := float64(d.Invested) / c.bb / pot
			st.Sizings = append(st.Sizings, size)
			if st.SizingsBy == nil {
				st.SizingsBy = map[table.Street][]float64{}
			}
			st.SizingsBy[d.Street] = append(st.SizingsBy[d.Street], size)
		}
	}

	pos := "?"
	for _, s := range d.Spot.State.Seats {
		if s.PlayerID == c.heroID && s.Position != "" {
			pos = string(s.Position)
		}
	}

	if d.Street == table.StreetPreflop {
		if aggressive || act == table.ActionCall {
			c.vpip = true
		}
		if aggressive {
			c.pfr = true
		}
		raisesAhead := 0
		for _, s := range d.Spot.State.Seats {
			if s.PlayerID == c.heroID || s.IsFolded {
				continue
			}
			switch s.LastAction {
			case "raise", "bet", "all-in":
				raisesAhead++
			}
		}
		if raisesAhead >= 1 {
			c.tbOpp = true
			if aggressive {
				c.tb = true
			}
		}
		c.spots[fmt.Sprintf("preflop %-3s %s", pos, act)] = true
		return
	}

	if aggressive {
		c.aggr++
	} else if act == table.ActionCall {
		c.pass++
	}
	facing := "checked-to"
	if d.Spot.ToCall > 0 {
		facing = "facing-bet"
	}
	c.spots[fmt.Sprintf("%-7s %s %s", d.Street, facing, act)] = true

	// Stack-to-pot ratio is the single most informative thing about a postflop
	// spot: it decides whether a hand can be played in more than one bet.
	pot := d.Spot.State.Pot
	if pot > 0 {
		spr := d.Spot.MaxRaise / pot
		c.spots[fmt.Sprintf("%-7s spr%s %s", d.Street, sprBucket(spr), act)] = true
	}
}

func sprBucket(spr float64) string {
	switch {
	case spr < 1:
		return "<1 "
	case spr < 3:
		return "1-3"
	case spr < 8:
		return "3-8"
	default:
		return ">8 "
	}
}

func (c *collector) OnHandEnd(r HandResult) {
	if !c.recording {
		c.reset()
		return
	}
	net := float64(r.Net[c.heroID]) / c.bb
	st := c.res
	st.Nets = append(st.Nets, net)
	st.Style.Hands++
	if c.vpip {
		st.Style.VPIPHands++
	}
	if c.pfr {
		st.Style.PFRHands++
	}
	if c.tbOpp {
		st.Style.ThreeBetOpps++
		if c.tb {
			st.Style.ThreeBetHands++
		}
	}
	st.Style.AggrActions += c.aggr
	st.Style.CallActions += c.pass

	// r.Showdown says a pot had more than one player eligible for it at the
	// end. Hero went to that showdown if hero was one of them, which is the
	// same as hero not having folded.
	if r.Showdown && !c.folded {
		st.Style.ShowdownHands++
		if net > 0 {
			st.Style.WonShowdown++
		}
	} else if net > 0 {
		st.Style.WonWithoutShowdown++
	}

	for k := range c.spots {
		b, ok := st.Buckets[k]
		if !ok {
			b = &Bucket{Key: k}
			st.Buckets[k] = b
		}
		b.Hands++
		b.Net += net
	}
	c.reset()
}

func (c *collector) reset() {
	c.spots = nil
	c.vpip, c.pfr, c.tb, c.tbOpp, c.folded = false, false, false, false, false
	c.aggr, c.pass = 0, 0
}

func mergeStyle(dst *StyleReport, src StyleReport) {
	dst.Hands += src.Hands
	dst.VPIPHands += src.VPIPHands
	dst.PFRHands += src.PFRHands
	dst.ThreeBetHands += src.ThreeBetHands
	dst.ThreeBetOpps += src.ThreeBetOpps
	dst.AggrActions += src.AggrActions
	dst.CallActions += src.CallActions
	dst.ShowdownHands += src.ShowdownHands
	dst.WonShowdown += src.WonShowdown
	dst.WonWithoutShowdown += src.WonWithoutShowdown
	dst.AllIns += src.AllIns
	for k, v := range src.Actions {
		dst.Actions[k] += v
	}
	for k, v := range src.StreetActions {
		dst.StreetActions[k] += v
	}
}

// BB100 is the win rate in big blinds per hundred hands, with the standard
// error of that estimate.
func (r *Result) BB100() (rate, stderr float64) {
	n := float64(len(r.Nets))
	if n == 0 {
		return 0, 0
	}
	var sum float64
	for _, v := range r.Nets {
		sum += v
	}
	mean := sum / n
	var ss float64
	for _, v := range r.Nets {
		d := v - mean
		ss += d * d
	}
	sd := math.Sqrt(ss / math.Max(n-1, 1))
	return mean * 100, sd / math.Sqrt(n) * 100
}

// PairedDiff is this candidate's advantage over another, measured hand by hand
// on the same cards.
//
// This is the number worth looking at. The unpaired win rates of two strategies
// over fifty thousand hands each carry an interval of several big blinds; the
// difference between them on identical decks carries a fraction of that,
// because the aces and the coolers appear on both sides of the subtraction.
func (r *Result) PairedDiff(base *Result) (diff, stderr float64, ok bool) {
	if base == nil || len(base.Nets) != len(r.Nets) || len(r.Nets) == 0 {
		return 0, 0, false
	}
	n := float64(len(r.Nets))
	var sum float64
	for i := range r.Nets {
		sum += r.Nets[i] - base.Nets[i]
	}
	mean := sum / n
	var ss float64
	for i := range r.Nets {
		d := (r.Nets[i] - base.Nets[i]) - mean
		ss += d * d
	}
	sd := math.Sqrt(ss / math.Max(n-1, 1))
	return mean * 100, sd / math.Sqrt(n) * 100, true
}

// LearningCurve is the win rate over the run, split into equal stretches of a
// session.
//
// It is the answer to "does this strategy get better as it learns the table".
// The nets arrive lineup by lineup, each lineup contributing handsPerLineup
// entries in the order they were played, so the position within a lineup is the
// number of hands of history the strategy had when it made that decision.
// Averaging across lineups at the same position gives the curve.
func (r *Result) LearningCurve(handsPerLineup, segments int) []float64 {
	if handsPerLineup <= 0 || segments <= 0 || len(r.Nets) < handsPerLineup {
		return nil
	}
	sums := make([]float64, segments)
	counts := make([]int, segments)
	per := handsPerLineup / segments
	if per == 0 {
		return nil
	}
	for i, v := range r.Nets {
		seg := (i % handsPerLineup) / per
		if seg >= segments {
			seg = segments - 1
		}
		sums[seg] += v
		counts[seg]++
	}
	out := make([]float64, segments)
	for i := range out {
		if counts[i] > 0 {
			out[i] = sums[i] / float64(counts[i]) * 100
		}
	}
	return out
}

// LeaksVersus lists the spots where this candidate did worse than the baseline
// did in the same kind of spot, worst first.
//
// A candidate's own worst spots are nearly useless on their own: the biggest
// losses are always "called a bet on the turn", because that is where the money
// is, and every strategy loses there. What matters is losing more of it than
// somebody else does. The comparison is descriptive rather than causal -- the
// two candidates did not face these spots on the same hands, only off the same
// decks -- but it is the difference between "the tool loses 31 big blinds a
// hand calling flop bets" and "the tool loses twice what a competent player
// loses in the same spot, over a fifth as many hands".
type LeakDiff struct {
	Key         string
	Hands       int
	PerHand     float64
	BasePerHand float64
	BaseHands   int
	// Cost is the difference per hand multiplied by how often it came up: how
	// many big blinds this spot handed away relative to the baseline.
	Cost float64
}

func (r *Result) LeaksVersus(base *Result, minHands, limit int) []LeakDiff {
	if base == nil || base == r {
		return nil
	}
	var out []LeakDiff
	for key, b := range r.Buckets {
		if b.Hands < minHands {
			continue
		}
		bb, ok := base.Buckets[key]
		if !ok || bb.Hands < minHands {
			continue
		}
		per := b.Net / float64(b.Hands)
		basePer := bb.Net / float64(bb.Hands)
		out = append(out, LeakDiff{
			Key: key, Hands: b.Hands, PerHand: per,
			BasePerHand: basePer, BaseHands: bb.Hands,
			Cost: (per - basePer) * float64(b.Hands),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Cost < out[j].Cost })
	if len(out) > limit {
		out = out[:limit]
	}
	return out
}

// Leaks lists the spots that cost the most, worst first. A spot with few hands
// behind it is noise, so a minimum count is required.
func (r *Result) Leaks(minHands int, limit int) []*Bucket {
	var out []*Bucket
	for _, b := range r.Buckets {
		if b.Hands >= minHands {
			out = append(out, b)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Net < out[j].Net })
	if len(out) > limit {
		out = out[:limit]
	}
	return out
}

// Render writes the report as plain text.
func (rep Report) Render() string {
	var b strings.Builder
	c := rep.Cfg
	fmt.Fprintf(&b, "%d lineups x %d hands x %d candidates = %d hands played, %d-handed, %.0f-%.0f bb deep, reads=%s\n\n",
		c.Lineups, c.Hands, len(c.Candidates), c.Lineups*c.Hands*len(c.Candidates), c.Seats,
		c.StackMinBB, c.StackMaxBB, readLevelName(c.Level))

	if c.CarryStacks {
		b.WriteString("stacks carried across each session; hero busting ends it, so the hand counts\n" +
			"differ between candidates and the paired comparison is not available here.\n\n")
	}
	fmt.Fprintf(&b, "%-22s %10s %9s   %-22s\n", "strategy", "bb/100", "±", "paired vs baseline")
	for _, r := range rep.Results {
		rate, se := r.BB100()
		line := fmt.Sprintf("%-22s %10.2f %9.2f", r.Label, rate, se)
		if diff, dse, ok := r.PairedDiff(rep.Baseline); ok && r != rep.Baseline {
			verdict := "  (inside the noise)"
			if math.Abs(diff) > 2*dse {
				verdict = "  *"
				if diff < 0 {
					verdict = "  * worse"
				} else {
					verdict = "  * better"
				}
			}
			line += fmt.Sprintf("   %+8.2f ± %5.2f%s", diff, dse, verdict)
		}
		b.WriteString(line + "\n")
	}

	for _, r := range rep.Results {
		s := r.Style
		fmt.Fprintf(&b, "\n--- %s ---\n", r.Label)
		fmt.Fprintf(&b, "  vpip %.1f  pfr %.1f  3bet %.1f  af %.2f  wtsd %.1f  w$sd %.1f  all-ins %d",
			s.VPIP(), s.PFR(), s.ThreeBet(), s.AF(), s.WTSD(), s.WonAtSD(), s.AllIns)
		if r.NoAdvice > 0 {
			fmt.Fprintf(&b, "  no-advice %d", r.NoAdvice)
		}
		b.WriteString("\n")
		if r.Decisions > 0 && len(r.Phases) > 0 {
			fmt.Fprintf(&b, "  table known %.0f%% on average; decisions by phase:", 100*r.KnowledgeSum/float64(r.Decisions))
			for _, name := range []string{"разведка", "применение", "давление"} {
				if n := r.Phases[name]; n > 0 {
					fmt.Fprintf(&b, "  %s %.0f%%", name, 100*float64(n)/float64(r.Decisions))
				}
			}
			b.WriteString("\n")
		}
		for _, street := range []table.Street{table.StreetPreflop, table.StreetFlop, table.StreetTurn, table.StreetRiver} {
			sizes := r.SizingsBy[street]
			if len(sizes) < 20 {
				continue
			}
			sort.Float64s(sizes)
			p := func(q float64) float64 { return sizes[int(q*float64(len(sizes)-1))] }
			fmt.Fprintf(&b, "  %-7s bet/pot: median %.2f  p90 %.2f  p99 %.2f  max %.2f  (%d)\n",
				street, p(0.5), p(0.9), p(0.99), sizes[len(sizes)-1], len(sizes))
		}
		if curve := r.LearningCurve(c.Hands, 5); curve != nil {
			fmt.Fprintf(&b, "  over the session (bb/100 by fifth):")
			for _, v := range curve {
				fmt.Fprintf(&b, " %8.1f", v)
			}
			b.WriteString("\n")
		}
		if sr := sessionReport(r.Sessions); sr != "" {
			b.WriteString(sr)
		}
		fmt.Fprintf(&b, "  worst spots (hands, bb total, bb/hand):\n")
		for _, lk := range r.Leaks(30, 10) {
			fmt.Fprintf(&b, "    %-34s %6d %10.1f %8.3f\n", lk.Key, lk.Hands, lk.Net, lk.Net/float64(lk.Hands))
		}
		if diffs := r.LeaksVersus(rep.Baseline, 40, 10); diffs != nil {
			fmt.Fprintf(&b, "  worse than %s in the same spot (bb/hand here vs there, hands, bb given away):\n", rep.Baseline.Label)
			for _, d := range diffs {
				fmt.Fprintf(&b, "    %-34s %8.3f vs %8.3f  %6d %10.1f\n",
					d.Key, d.PerHand, d.BasePerHand, d.Hands, d.Cost)
			}
		}
	}
	return b.String()
}

// sessionReport is the trajectory, which is a different question from the win
// rate and the one that decides whether a bankroll grows.
//
// A strategy can have a positive expectation and still be no use to somebody
// sitting down with a third of what everybody else has: the expectation is an
// average over sessions that ended, and the sessions that ended at zero are in
// it. What is wanted is how often the money survives, and how much of it there
// is at the end.
func sessionReport(sessions []SessionOutcome) string {
	if len(sessions) == 0 {
		return ""
	}
	busted := 0
	var finals, growth []float64
	var startSum, finalSum float64
	doubled := 0
	for _, s := range sessions {
		if s.Busted {
			busted++
		}
		finals = append(finals, s.FinalBB)
		startSum += s.StartBB
		finalSum += s.FinalBB
		if s.StartBB > 0 {
			growth = append(growth, s.FinalBB/s.StartBB)
			if s.FinalBB >= 2*s.StartBB {
				doubled++
			}
		}
	}
	sort.Float64s(growth)
	median := growth[len(growth)/2]

	// Hands survived, and what the survivors ended with. The average final
	// stack alone hides the shape completely: a strategy that busts four
	// sessions in five and sextuples the fifth has the same average as one that
	// grinds every session up by a fifth, and they are not the same strategy to
	// sit down with.
	var handsSurvived []float64
	var survivorGrowth []float64
	for _, s := range sessions {
		handsSurvived = append(handsSurvived, float64(s.Hands))
		if !s.Busted && s.StartBB > 0 {
			survivorGrowth = append(survivorGrowth, s.FinalBB/s.StartBB)
		}
	}
	sort.Float64s(handsSurvived)
	sort.Float64s(survivorGrowth)

	var b strings.Builder
	fmt.Fprintf(&b, "  session: sat with %.0f bb, left with %.0f on average (x%.2f), median x%.2f\n",
		startSum/float64(len(sessions)), finalSum/float64(len(sessions)),
		finalSum/startSum, median)
	rebuys := 0
	for _, s := range sessions {
		rebuys += s.Rebuys
	}
	fmt.Fprintf(&b, "           busted %d/%d (%.0f%%), doubled %d (%.0f%%), hands survived: median %.0f, rebuys %.1f/session\n",
		busted, len(sessions), 100*float64(busted)/float64(len(sessions)),
		doubled, 100*float64(doubled)/float64(len(sessions)),
		handsSurvived[len(handsSurvived)/2], float64(rebuys)/float64(len(sessions)))
	if len(survivorGrowth) > 0 {
		fmt.Fprintf(&b, "           of the %d that survived: median x%.2f, best x%.2f\n",
			len(survivorGrowth), survivorGrowth[len(survivorGrowth)/2], survivorGrowth[len(survivorGrowth)-1])
	}
	return b.String()
}

func readLevelName(l ReadLevel) string {
	switch l {
	case ReadsOff:
		return "off"
	case ReadsStats:
		return "stats"
	default:
		return "full"
	}
}
