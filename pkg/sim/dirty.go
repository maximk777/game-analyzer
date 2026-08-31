package sim

import (
	"fmt"
	"math/rand"
	"slices"

	"poker-game-analyzer/pkg/table"
)

// Dirty state.
//
// Every number the harness reported before this file was measured on a table
// state the engine handed over intact: hero in his seat, positions assigned,
// one seat per player, the pot exact. The live tool never sees that state. It
// sees whatever the screen reader made of a screenshot, and a session logged on
// 2026-08-31 says what that is:
//
//	hero_id was not hero's name          79% of frames
//	seat numbers collided                99%
//	more than six seats at a six-max     28%
//	min_raise came through as zero       97%
//	no reads available                   68%
//	pot jumped more than threefold        5%
//
// Those are not cosmetic. A frame where the dealer button is not matched leaves
// every seat's position empty, and an empty position is how preflop.HeroPosition
// fails, and that failure is how advisor.chartedAction returns false, and that
// is how the preflop decision falls through to the expected-value comparison --
// the branch whose own comment in advisor.go records that it "folded pocket
// threes getting better than 2 to 1 with thirty-seven calls behind". In the
// logged session 28% of preflop decisions fell through that hole, and one of
// them folded pocket eights getting 2.5 to 1.
//
// So the harness measures a strategy that is not the one being played. This
// file closes that gap: it corrupts the state on its way into the tool, and
// only into the tool. The engine keeps the truth, deals the same cards and
// legalises the resulting move exactly as the poker client would legalise a
// number typed by somebody following bad advice.
//
// Two things fall out of that, and both use machinery the harness already has:
//
//   - The cost of a defect in bb/100 is a Candidate. Seat the clean tool and
//     the same tool under one defect as two candidates and PairedDiff prices
//     that defect on identical decks, which is far tighter than comparing two
//     independent win rates.
//   - Stability is free. With MeasureFlips set the tool decides twice, once on
//     the clean state and once on the dirty one, plays the dirty answer, and
//     counts how often the two disagreed. That is the number the logged session
//     shows most directly -- fold, then raise, then fold, then call on the same
//     hand within twenty seconds -- and it does not survive averaging into a
//     win rate.

// Defect is one failure mode of the screen reader.
type Defect string

const (
	// DefectButtonLost is a frame where the dealer button was not matched, so
	// no seat has a position. table_vision.swift assigns positions only inside
	// `if let button = findDealerButton(...)`, with no fallback, so a single
	// missed template match blanks all six.
	//
	// This is the expensive one: without a position the preflop chart has no
	// opinion and the decision falls through to a comparison that prices a call
	// as though the hand were about to be shown down.
	DefectButtonLost Defect = "button_lost"

	// DefectHeroUnnamed is hero_id arriving as the placeholder "Hero", matching
	// no seat. Hero's position and hero's stack both go with it, so the
	// effective stack is taken from somebody else: in the logged session it
	// came out as 1190000 against hero's real 68000.
	DefectHeroUnnamed Defect = "hero_unnamed"

	// DefectGhostSeats is one opponent's name read several ways in one frame --
	// Rafidamage also appearing as Rafk, aage, adge and nafidamage -- each
	// variant becoming a seat with a stack and a bet. The count of live
	// opponents is what this corrupts, and the count of live opponents is what
	// multiway equity is computed against.
	DefectGhostSeats Defect = "ghost_seats"

	// DefectStaleBet is a bet left on a nameplate after the chips have been
	// swept into the pot, so the tool believes there is something to call when
	// the action is checked to it. In the logged session this produced calls of
	// 1000 into a pot of 490960 with two percent equity.
	DefectStaleBet Defect = "stale_bet"

	// DefectPotJitter is the pot reading picking up a different number from the
	// screen -- a stack, usually. The pot is the denominator of every price the
	// tool quotes.
	DefectPotJitter Defect = "pot_jitter"

	// DefectMinRaiseLost is MinRaise arriving as zero, which parser.go produces
	// whenever no bet is on the felt, since it derives the minimum from
	// maxBet*2. With no big blind anywhere in the state there is nothing else
	// to size from, and the open collapsed to five chips at blinds of 1000/2000.
	DefectMinRaiseLost Defect = "min_raise_lost"

	// DefectBadgeLost is a nameplate whose action badge was not read, so the
	// player who raised looks like a player who has done nothing.
	//
	// Badges are the only record of what somebody just did, and they are small
	// text over a busy background. Losing them is how a raised pot came to be
	// charted as an unraised one -- and a caller of an all-in wears the same
	// "call" badge as a limper, so even a badge that is read says nothing about
	// size. The repair is to classify the spot by the money instead, which is
	// what this defect exists to price.
	DefectBadgeLost Defect = "badge_lost"

	// DefectBetsSwept is a frame caught after the client has pulled the bets
	// into the pot but before the next street's are posted: the pot is right and
	// the felt is bare.
	//
	// Everything that inferred a scale from the largest bet on the table then
	// had nothing to infer it from. A chart open of two and a half big blinds
	// was sized off a big blind assumed to be one chip, and came out as "raise
	// 5" at blinds of 1000/2000.
	DefectBetsSwept Defect = "bets_swept"

	// DefectReadsLost is the table identity churning -- "NLH 1234677" also read
	// as "© NLH 1234677" and "NLH 1234677-1K/2K(320)" -- so statistics land
	// under five different tables and no player ever accumulates a sample. The
	// harness prices reads at +17 bb/100; this is how much of that reaches the
	// felt.
	DefectReadsLost Defect = "reads_lost"
)

// AllDefects is every failure mode, in a fixed order so reports are stable.
var AllDefects = []Defect{
	DefectButtonLost,
	DefectHeroUnnamed,
	DefectGhostSeats,
	DefectStaleBet,
	DefectPotJitter,
	DefectMinRaiseLost,
	DefectBadgeLost,
	DefectBetsSwept,
	DefectReadsLost,
}

// Noise is how often each defect appears, as a probability per decision.
//
// The zero value corrupts nothing, so a harness run that does not mention noise
// behaves exactly as it did before.
type Noise struct {
	ButtonLost   float64
	HeroUnnamed  float64
	GhostSeats   float64
	StaleBet     float64
	PotJitter    float64
	MinRaiseLost float64
	BadgeLost    float64
	BetsSwept    float64
	ReadsLost    float64

	// MeasureFlips makes the tool decide twice, on the clean state and on the
	// dirty one, and count the disagreements. It roughly doubles the cost of a
	// run, which is why it is a flag and not the default.
	MeasureFlips bool
}

// Any reports whether this noise corrupts anything at all.
func (n Noise) Any() bool {
	return n.ButtonLost > 0 || n.HeroUnnamed > 0 || n.GhostSeats > 0 ||
		n.StaleBet > 0 || n.PotJitter > 0 || n.MinRaiseLost > 0 ||
		n.BadgeLost > 0 || n.BetsSwept > 0 || n.ReadsLost > 0
}

// rate is the probability of one named defect.
func (n Noise) rate(d Defect) float64 {
	switch d {
	case DefectButtonLost:
		return n.ButtonLost
	case DefectHeroUnnamed:
		return n.HeroUnnamed
	case DefectGhostSeats:
		return n.GhostSeats
	case DefectStaleBet:
		return n.StaleBet
	case DefectPotJitter:
		return n.PotJitter
	case DefectMinRaiseLost:
		return n.MinRaiseLost
	case DefectBadgeLost:
		return n.BadgeLost
	case DefectBetsSwept:
		return n.BetsSwept
	case DefectReadsLost:
		return n.ReadsLost
	}
	return 0
}

// LiveNoise is the defect rate measured on the session of 2026-08-31, 220
// decisions over six minutes at NLH 1234677.
//
// Every rate here is counted from bin/logs/decisions.jsonl rather than chosen.
// ButtonLost is the exception in how it was counted: the frames do not record
// seat positions, so it is measured by its consequence -- the share of preflop
// recommendations whose reasoning does not come from the chart, 12 of 43.
func LiveNoise() Noise {
	return Noise{
		ButtonLost:   0.28,
		HeroUnnamed:  0.79,
		GhostSeats:   0.28,
		StaleBet:     0.34,
		PotJitter:    0.05,
		MinRaiseLost: 0.97,
		// These two are counted over the same session but need an
		// operationalisation the others do not, because "the badge is missing"
		// and "this player has not acted yet" look identical in a frame.
		//
		// BadgeLost: of the 4,203 frames where the pot was larger than the
		// blinds -- so somebody must have acted -- 1,771 carried no action
		// badge on any live opponent at all.
		//
		// BetsSwept: of the 2,584 preflop frames with a pot, 369 showed a bet
		// of zero on every seat. Preflop the blinds are always on the felt, so
		// a bare felt there is a misread rather than a checked round.
		BadgeLost: 0.42,
		BetsSwept: 0.14,
		ReadsLost: 0.68,
	}
}

// OnlyNoise is the live rate of one defect and nothing else, which is what a
// candidate needs in order to be priced against the clean tool.
func OnlyNoise(d Defect) Noise {
	live := LiveNoise()
	var n Noise
	switch d {
	case DefectButtonLost:
		n.ButtonLost = live.ButtonLost
	case DefectHeroUnnamed:
		n.HeroUnnamed = live.HeroUnnamed
	case DefectGhostSeats:
		n.GhostSeats = live.GhostSeats
	case DefectStaleBet:
		n.StaleBet = live.StaleBet
	case DefectPotJitter:
		n.PotJitter = live.PotJitter
	case DefectMinRaiseLost:
		n.MinRaiseLost = live.MinRaiseLost
	case DefectBadgeLost:
		n.BadgeLost = live.BadgeLost
	case DefectBetsSwept:
		n.BetsSwept = live.BetsSwept
	case DefectReadsLost:
		n.ReadsLost = live.ReadsLost
	}
	return n
}

// WithoutNoise is every live defect except one, which prices a fix: the
// difference between this and LiveNoise is what repairing that one defect is
// worth while the others stay broken.
func WithoutNoise(d Defect) Noise {
	n := LiveNoise()
	switch d {
	case DefectButtonLost:
		n.ButtonLost = 0
	case DefectHeroUnnamed:
		n.HeroUnnamed = 0
	case DefectGhostSeats:
		n.GhostSeats = 0
	case DefectStaleBet:
		n.StaleBet = 0
	case DefectPotJitter:
		n.PotJitter = 0
	case DefectMinRaiseLost:
		n.MinRaiseLost = 0
	case DefectBadgeLost:
		n.BadgeLost = 0
	case DefectBetsSwept:
		n.BetsSwept = 0
	case DefectReadsLost:
		n.ReadsLost = 0
	}
	return n
}

// set overrides one defect's rate. It exists so that a repair can be priced
// before it is trusted: measure what the fix achieves on a recorded session,
// then run the live noise with that one rate lowered and see what it buys in
// bb/100. Without it the only choices are "broken" and "perfect", and no repair
// is ever perfect.
func (n *Noise) set(d Defect, rate float64) {
	switch d {
	case DefectButtonLost:
		n.ButtonLost = rate
	case DefectHeroUnnamed:
		n.HeroUnnamed = rate
	case DefectGhostSeats:
		n.GhostSeats = rate
	case DefectStaleBet:
		n.StaleBet = rate
	case DefectPotJitter:
		n.PotJitter = rate
	case DefectMinRaiseLost:
		n.MinRaiseLost = rate
	case DefectBadgeLost:
		n.BadgeLost = rate
	case DefectBetsSwept:
		n.BetsSwept = rate
	case DefectReadsLost:
		n.ReadsLost = rate
	}
}

// Set returns this noise with one defect's rate replaced.
func (n Noise) Set(d Defect, rate float64) Noise {
	n.set(d, rate)
	return n
}

// ghostSuffixes are how one name came back from OCR in the logged session:
// Rafidamage also read as Rafk, aage, adge, Rafida and nafidamage. The exact
// spelling does not matter -- what matters is that the id differs, because a
// different id is a different player to everything downstream.
var ghostSuffixes = []string{"k", "ge", "ae", "a", "e"}

// Corrupt returns the state as the screen reader would have reported it, and
// the defects that fired.
//
// The returned state shares nothing mutable with the input: Seats is copied
// before it is touched, because the caller's copy is the engine's truth and
// corrupting that would change the hand rather than the view of it.
func (n Noise) Corrupt(st table.HandState, rng *rand.Rand) (table.HandState, []Defect) {
	if !n.Any() || rng == nil {
		return st, nil
	}

	var fired []Defect
	hit := func(d Defect) bool {
		if p := n.rate(d); p > 0 && rng.Float64() < p {
			fired = append(fired, d)
			return true
		}
		return false
	}

	out := st
	out.Seats = make([]table.SeatState, len(st.Seats))
	copy(out.Seats, st.Seats)

	// The button is not matched, so no seat carries a position. Ordering the
	// checks this way matters: hero_unnamed below also costs the position, and
	// the two together are what the live session mostly showed.
	if hit(DefectButtonLost) {
		for i := range out.Seats {
			out.Seats[i].Position = ""
		}
	}

	// hero_id arrives as the placeholder that matches no seat. Hero's stack and
	// position both become unreadable, and the effective stack falls to whoever
	// else the advisor finds.
	if hit(DefectHeroUnnamed) {
		out.HeroID = "Hero"
	}

	// One opponent's name comes back several ways in the same frame. The ghost
	// copies a live opponent -- same stack, same bet, same folded flag -- under
	// an id nothing has ever seen, which is what makes it count as an extra
	// live player and an extra unknown.
	if hit(DefectGhostSeats) {
		var live []int
		for i, s := range out.Seats {
			if !s.IsFolded && s.PlayerID != st.HeroID {
				live = append(live, i)
			}
		}
		if len(live) > 0 {
			src := out.Seats[live[rng.Intn(len(live))]]
			ghosts := 1 + rng.Intn(2)
			for g := 0; g < ghosts; g++ {
				ghost := src
				ghost.PlayerID = src.PlayerID + ghostSuffixes[rng.Intn(len(ghostSuffixes))]
				ghost.PlayerName = ghost.PlayerID
				ghost.Cards = nil
				out.Seats = append(out.Seats, ghost)
			}
		}
	}

	// A bet stays on a nameplate after the chips have gone into the pot. Only a
	// seat that is not already betting can acquire one, or this would be
	// raising a live bet rather than inventing one.
	if hit(DefectStaleBet) {
		var idle []int
		for i, s := range out.Seats {
			if !s.IsFolded && s.CurrentBet == 0 && s.PlayerID != st.HeroID {
				idle = append(idle, i)
			}
		}
		if len(idle) > 0 {
			i := idle[rng.Intn(len(idle))]
			stale := staleAmount(st, rng)
			if stale > out.Seats[i].Stack {
				stale = out.Seats[i].Stack
			}
			out.Seats[i].CurrentBet = stale
		}
	}

	// The pot reading picks up a stack instead. Using a number that is actually
	// on the screen is the point -- a random multiplier would be a different
	// kind of wrong from the one the logs show, where the pot became 758640 and
	// then 943400 on an unchanged flop.
	if hit(DefectPotJitter) {
		if len(out.Seats) > 0 {
			if v := out.Seats[rng.Intn(len(out.Seats))].Stack; v > 0 {
				out.Pot = v
			}
		}
	}

	// No bet on the felt, so parser.go's maxBet*2 comes out zero and there is
	// no big blind in the state to size from instead.
	if hit(DefectMinRaiseLost) {
		out.MinRaise = 0
	}

	// A badge is small text over a busy background, and losing it turns the
	// player who raised into a player who has done nothing.
	if hit(DefectBadgeLost) {
		var acted []int
		for i, s := range out.Seats {
			if !s.IsFolded && s.PlayerID != st.HeroID && s.LastAction != "" {
				acted = append(acted, i)
			}
		}
		if len(acted) > 0 {
			out.Seats[acted[rng.Intn(len(acted))]].LastAction = ""
		}
	}

	// The frame landed between the client sweeping the bets into the pot and
	// the next ones going out. The pot is right; the felt is bare.
	if hit(DefectBetsSwept) {
		out.CurrentBet = 0
		for i := range out.Seats {
			out.Seats[i].CurrentBet = 0
		}
	}

	// Reads are suppressed by the caller, not here -- they are not part of the
	// state -- but the defect is drawn here so that one decision draws all of
	// its noise from one place and one seed.
	if hit(DefectReadsLost) { //nolint:staticcheck // recorded in fired, applied by the caller
		_ = out
	}

	slices.Sort(fired)
	return out, fired
}

// staleAmount is the size of a bet left behind. It is drawn from what is
// already in the hand rather than invented: the last bet anybody made, or a
// blind-sized dribble when nobody has bet yet, which is the case that produced
// "call 1000" into a pot of half a million.
func staleAmount(st table.HandState, rng *rand.Rand) float64 {
	var maxBet float64
	for _, s := range st.Seats {
		if s.CurrentBet > maxBet {
			maxBet = s.CurrentBet
		}
	}
	if maxBet > 0 {
		return maxBet
	}
	if st.MinRaise > 0 {
		return st.MinRaise / 2
	}
	if st.Pot > 0 {
		// A hundredth of the pot, which at the logged blinds is the 1000 the
		// tool kept being told to call.
		return st.Pot / 100 * float64(1+rng.Intn(2))
	}
	return 0
}

// FlipReport is how unstable the advice was under noise.
//
// Flips are counted against the clean state, not against the previous decision,
// because that is the question worth asking: given the table as it really is,
// how often does the screen reader turn the right answer into a different one.
type FlipReport struct {
	// Decisions is how many turns were measured.
	Decisions int
	// ActionFlips is how many of those changed the action -- fold became raise,
	// check became call. These are the ones a player notices.
	ActionFlips int
	// SizingFlips is how many kept the action but changed the amount by more
	// than a tenth. Counted separately because a raise that is twice the size
	// is a different mistake from a raise that should have been a fold.
	SizingFlips int
	// ReversedFlips is the subset of ActionFlips that inverted the hand:
	// aggression became surrender or the other way round. A fold turning into a
	// check is a flip; a fold turning into an all-in is a reversal.
	ReversedFlips int
	// ByDefect attributes flips to the defects that were active on the flipped
	// decision. A decision with two defects live credits both, so the columns
	// sum to more than ActionFlips.
	ByDefect map[Defect]int
	// FiredByDefect is how often each defect was active at all, flip or not.
	//
	// Without it ByDefect cannot be read: min_raise_lost fires on 97% of frames,
	// so it is present at almost every flip whether or not it caused any. The
	// ratio of the two is the number worth quoting -- when this defect was
	// there, how often did the answer change -- and even that is association
	// rather than cause, because defects arrive together. The clean causal
	// figure is the paired bb/100 of a candidate carrying one defect alone.
	FiredByDefect map[Defect]int
}

// FlipRateGiven is how often the answer changed on the decisions where this
// defect was active. Association, not attribution: see FiredByDefect.
func (f FlipReport) FlipRateGiven(d Defect) (rate float64, ok bool) {
	n := f.FiredByDefect[d]
	if n == 0 {
		return 0, false
	}
	return float64(f.ByDefect[d]) / float64(n), true
}

// ActionFlipRate is the share of decisions whose action the noise changed.
func (f FlipReport) ActionFlipRate() float64 {
	if f.Decisions == 0 {
		return 0
	}
	return float64(f.ActionFlips) / float64(f.Decisions)
}

// ReversalRate is the share of decisions the noise turned inside out.
func (f FlipReport) ReversalRate() float64 {
	if f.Decisions == 0 {
		return 0
	}
	return float64(f.ReversedFlips) / float64(f.Decisions)
}

// SizingFlipRate is the share of decisions that kept the action and changed the
// number.
func (f FlipReport) SizingFlipRate() float64 {
	if f.Decisions == 0 {
		return 0
	}
	return float64(f.SizingFlips) / float64(f.Decisions)
}

func (f *FlipReport) record(clean, dirty table.ActionType, cleanAmt, dirtyAmt float64, fired []Defect) {
	f.Decisions++
	for _, d := range fired {
		if f.FiredByDefect == nil {
			f.FiredByDefect = map[Defect]int{}
		}
		f.FiredByDefect[d]++
	}
	switch {
	case clean != dirty:
		f.ActionFlips++
		if reversal(clean, dirty) {
			f.ReversedFlips++
		}
	case differsBy(cleanAmt, dirtyAmt, 0.10):
		f.SizingFlips++
	default:
		return
	}
	if f.ByDefect == nil {
		f.ByDefect = map[Defect]int{}
	}
	for _, d := range fired {
		f.ByDefect[d]++
	}
}

// aggressive is whether an action puts money in to make somebody else decide.
func aggressive(a table.ActionType) bool {
	return a == table.ActionBet || a == table.ActionRaise || a == table.ActionAllIn
}

// reversal is a flip that crossed between giving up and applying pressure.
func reversal(clean, dirty table.ActionType) bool {
	return (clean == table.ActionFold && aggressive(dirty)) ||
		(aggressive(clean) && dirty == table.ActionFold)
}

func differsBy(a, b, frac float64) bool {
	if a == b {
		return false
	}
	ref := a
	if b > ref {
		ref = b
	}
	if ref <= 0 {
		return false
	}
	return (a-b)/ref > frac || (b-a)/ref > frac
}

// String renders the report as the harness prints it.
func (f FlipReport) String() string {
	if f.Decisions == 0 {
		return "no decisions measured"
	}
	s := fmt.Sprintf("%d decisions: action flipped %.1f%% (reversed %.1f%%), sizing flipped %.1f%%",
		f.Decisions, 100*f.ActionFlipRate(), 100*f.ReversalRate(), 100*f.SizingFlipRate())
	if len(f.ByDefect) == 0 {
		return s
	}
	for _, d := range AllDefects {
		if c := f.ByDefect[d]; c > 0 {
			s += fmt.Sprintf("\n  %-15s %d", d, c)
		}
	}
	return s
}

// merge folds another report into this one. Lineups are run in parallel and
// their results merged, so every counter here has to be addition rather than an
// average that would need to know the weights.
func (f *FlipReport) merge(other FlipReport) {
	f.Decisions += other.Decisions
	f.ActionFlips += other.ActionFlips
	f.SizingFlips += other.SizingFlips
	f.ReversedFlips += other.ReversedFlips
	for d, c := range other.ByDefect {
		if f.ByDefect == nil {
			f.ByDefect = map[Defect]int{}
		}
		f.ByDefect[d] += c
	}
	for d, c := range other.FiredByDefect {
		if f.FiredByDefect == nil {
			f.FiredByDefect = map[Defect]int{}
		}
		f.FiredByDefect[d] += c
	}
}
