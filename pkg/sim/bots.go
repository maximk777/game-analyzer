package sim

import (
	"fmt"
	"math"
	"math/rand"
	"sync"

	"poker-game-analyzer/pkg/equity"
	"poker-game-analyzer/pkg/preflop"
	"poker-game-analyzer/pkg/table"
)

// The opponents.
//
// These are not solvers and are not trying to be. They are the population: the
// nit who folds to every second barrel, the station who calls three streets
// with a pair, the maniac who raises whatever they hold. A strategy that beats
// a solver is out of reach; a strategy that cannot beat this table is not worth
// running against real money, and knowing which of them it loses to is the
// whole diagnostic value of the exercise.
//
// Each is one Style with different numbers, so an archetype is a row of
// parameters that can be read and argued with, not a bespoke decision tree.

// Style is an opponent archetype.
type Style struct {
	Name string

	// Preflop, as a share of the ranked hand universe: 0.22 means the top 22%
	// of hands by the ranking equity.HandPercentile uses.
	Open     float64 // opens when the pot is unopened
	Call     float64 // continues against a raise
	ThreeBet float64 // reraises

	// Postflop. Tightness scales the pot odds a call must beat, so 1.6 folds
	// well inside the price and 0.55 calls far outside it -- which is what a
	// station actually is.
	Tightness float64
	// ValueBet is the equity at which betting for value starts, CBet how often
	// it actually happens, Bluff how often a hand below that bets anyway.
	ValueBet float64
	CBet     float64
	Bluff    float64
	// RaiseEq is the equity at which a bet gets raised rather than called;
	// BluffRaise how often a hand well below it raises anyway.
	RaiseEq    float64
	BluffRaise float64
	// Sizing is the bet as a fraction of the pot.
	Sizing float64
}

// The archetypes. VPIP/PFR figures in the comments are what these settings come
// out as when measured over a long run, not inputs.
var (
	// Nit: 12/10, folds to almost everything without the goods.
	StyleNit = Style{Name: "nit", Open: 0.12, Call: 0.08, ThreeBet: 0.03,
		Tightness: 1.7, ValueBet: 0.68, CBet: 0.50, Bluff: 0.03,
		RaiseEq: 0.82, BluffRaise: 0.01, Sizing: 0.55}

	// Tight-aggressive: the competent regular. 22/18.
	StyleTAG = Style{Name: "tag", Open: 0.22, Call: 0.14, ThreeBet: 0.07,
		Tightness: 1.25, ValueBet: 0.58, CBet: 0.65, Bluff: 0.14,
		RaiseEq: 0.72, BluffRaise: 0.05, Sizing: 0.66}

	// Loose-aggressive: plays too many hands and bets all of them. 34/28.
	StyleLAG = Style{Name: "lag", Open: 0.34, Call: 0.24, ThreeBet: 0.12,
		Tightness: 1.0, ValueBet: 0.52, CBet: 0.78, Bluff: 0.30,
		RaiseEq: 0.66, BluffRaise: 0.12, Sizing: 0.75}

	// Calling station: enters wide, never raises, never folds. 45/6.
	StyleStation = Style{Name: "station", Open: 0.18, Call: 0.55, ThreeBet: 0.02,
		Tightness: 0.42, ValueBet: 0.72, CBet: 0.20, Bluff: 0.02,
		RaiseEq: 0.88, BluffRaise: 0.00, Sizing: 0.45}

	// Maniac: raises anything, bets everything. 60/50.
	StyleManiac = Style{Name: "maniac", Open: 0.52, Call: 0.45, ThreeBet: 0.16,
		Tightness: 0.85, ValueBet: 0.45, CBet: 0.90, Bluff: 0.32,
		RaiseEq: 0.58, BluffRaise: 0.12, Sizing: 0.80}

	// Rock: a nit that only ever calls. The passive end of the population.
	StyleRock = Style{Name: "rock", Open: 0.09, Call: 0.12, ThreeBet: 0.02,
		Tightness: 1.4, ValueBet: 0.75, CBet: 0.30, Bluff: 0.01,
		RaiseEq: 0.88, BluffRaise: 0.00, Sizing: 0.5}

	// Whale: the recreational player. Wide, passive, and pays off.
	StyleWhale = Style{Name: "whale", Open: 0.40, Call: 0.50, ThreeBet: 0.04,
		Tightness: 0.55, ValueBet: 0.62, CBet: 0.30, Bluff: 0.06,
		RaiseEq: 0.80, BluffRaise: 0.02, Sizing: 0.5}
)

// Archetypes is every style, in no particular order, for a caller that wants
// to name one.
var Archetypes = []Style{StyleNit, StyleTAG, StyleLAG, StyleStation, StyleManiac, StyleRock, StyleWhale}

// Opponent is one seat filler: a name and a way to build it. Not every
// opponent is a Style -- Pro is its own decision procedure -- so the population
// is drawn over these rather than over archetype rows.
type Opponent struct {
	Name string
	New  func(rng *rand.Rand) Agent
}

func styleOpponent(st Style) Opponent {
	return Opponent{Name: st.Name, New: func(rng *rand.Rand) Agent { return NewBot(st, rng) }}
}

// ProOpponent is the seasoned regular, defined in pro.go.
var ProOpponent = Opponent{Name: "pro", New: func(rng *rand.Rand) Agent { return NewPro(rng) }}

// ToolOpponent seats the advisor itself in an opponent's chair, with its own
// tracker, so the tool can be played against itself.
//
// It is the sharpest available test of a change: a strategy that only beats
// bots written by the same hand is being graded by its author, and a strategy
// that cannot beat its own previous self is not an improvement. It also
// produces the one opponent guaranteed to punish whatever the current version
// does badly, because it does the same thing.
var ToolOpponent = Opponent{Name: "tool", New: func(rng *rand.Rand) Agent {
	return NewTool("tool-opp", NewTracker(), ReadsFull, HarnessOptions(rng))
}}

// population is the mix a random seat is drawn from, and the weights are the
// point of it.
//
// Drawing uniformly from the archetypes puts a maniac at every second table and
// makes the field a charity: the win rate of anything sensible against it says
// more about how many maniacs were dealt in than about the strategy. Measured,
// that field paid a simple tight-aggressive bot 106 big blinds per hundred
// hands, which is not a table that exists.
//
// These weights are an online six-max table as it actually looks: a couple of
// competent regulars, a spread of ordinary players, two or three recreational
// ones, and the extremes rare enough to be variance in the sample rather than
// the sample.
var population = []struct {
	opp    Opponent
	weight int
}{
	{ProOpponent, 18},
	{styleOpponent(StyleTAG), 20},
	{styleOpponent(StyleNit), 13},
	{styleOpponent(StyleLAG), 13},
	{styleOpponent(StyleRock), 9},
	{styleOpponent(StyleStation), 13},
	{styleOpponent(StyleWhale), 9},
	{styleOpponent(StyleManiac), 5},
}

// DrawOpponent picks a seat filler from the population.
func DrawOpponent(rng *rand.Rand) Opponent {
	total := 0
	for _, e := range population {
		total += e.weight
	}
	n := rng.Intn(total)
	for _, e := range population {
		n -= e.weight
		if n < 0 {
			return e.opp
		}
	}
	return ProOpponent
}

// OpponentByName resolves a name from the command line.
func OpponentByName(name string) (Opponent, bool) {
	switch name {
	case "pro":
		return ProOpponent, true
	case "tool":
		return ToolOpponent, true
	}
	for _, st := range Archetypes {
		if st.Name == name {
			return styleOpponent(st), true
		}
	}
	return Opponent{}, false
}

// Ranges a bot measures its equity against, built once each and shared.
//
// Building a Range walks the whole ranking and allocates 1326 combinations;
// doing it inside a decision costs more than the simulation that follows it,
// and a bot makes millions of decisions.
var (
	rangeCacheMu sync.RWMutex
	rangeCache   = map[int]equity.Range{}
)

// topRange is the strongest `width` per cent of starting hands, cached. The
// width is rounded to five points, which is far finer than any read justifies
// and keeps the cache to twenty entries.
func topRange(width float64) equity.Range {
	w := int(math.Round(width/5) * 5)
	if w >= 100 {
		w = 100
	}
	if w < 5 {
		w = 5
	}
	rangeCacheMu.RLock()
	r, ok := rangeCache[w]
	rangeCacheMu.RUnlock()
	if ok {
		return r
	}
	spec := "random"
	if w < 100 {
		spec = fmt.Sprintf("top%d%%", w)
	}
	r = equity.ParseRange(spec)
	rangeCacheMu.Lock()
	rangeCache[w] = r
	rangeCacheMu.Unlock()
	return r
}

// opponentWidth is how wide the live opponents' holdings still are, as a
// percentage of all starting hands.
//
// It is the single most important thing a simulated player needs and the thing
// the first version of these bots did not have: equity was measured against a
// random hand, so middle pair looked like sixty per cent heads-up and every
// archetype stacked off with it. Measured, the loose archetypes were giving
// away 150 big blinds per hundred hands -- four or five times what a bad player
// really loses -- and that inflated everybody else's win rate to match, which
// made the whole field useless as a yardstick.
//
// The model is crude and it is the right kind of crude: money going in narrows
// a range, and it narrows it more preflop than after, because preflop money is
// the only statement a player has made.
func opponentWidth(st table.HandState) float64 {
	preflopRaises, postflopAggr := 0, 0
	for _, a := range st.ActionHistory {
		if a.PlayerID == st.HeroID {
			continue
		}
		aggressive := a.Action == table.ActionBet || a.Action == table.ActionRaise || a.Action == table.ActionAllIn
		if !aggressive {
			continue
		}
		if a.Street == table.StreetPreflop {
			preflopRaises++
		} else {
			postflopAggr++
		}
	}

	width := 65.0
	switch {
	case preflopRaises >= 2:
		width = 9
	case preflopRaises == 1:
		width = 25
	}
	for i := 0; i < postflopAggr && width > 4; i++ {
		width *= 0.6
	}
	if width < 4 {
		width = 4
	}
	return width
}

// Bot plays a Style.
type Bot struct {
	style Style
	rng   *rand.Rand
	// iters is how many Monte Carlo samples a postflop decision rests on. Bots
	// are allowed to be noisy -- a human is -- and the harness runs millions of
	// these, so the count is low on purpose.
	iters int
}

// NewBot seats an archetype. The rng is the bot's own, so that two bots of the
// same style at the same table do not act in lockstep.
func NewBot(style Style, rng *rand.Rand) *Bot {
	return &Bot{style: style, rng: rng, iters: 400}
}

func (b *Bot) Name() string { return b.style.Name }

// jitter spreads a threshold so that a bot is not a step function on hand
// strength. Real players are not, and a strategy tuned against a step function
// learns the step.
func (b *Bot) jitter(thr float64) float64 { return thr * (0.8 + 0.4*b.rng.Float64()) }

func (b *Bot) Act(s Spot) Move {
	if s.State.Street == table.StreetPreflop {
		return b.preflop(s)
	}
	return b.postflop(s)
}

// effectiveBB is how deep the shorter of hero and the field is, in big blinds.
func effectiveBB(s Spot) float64 {
	var hero, deepest float64
	for _, seat := range s.State.Seats {
		if seat.PlayerID == s.State.HeroID {
			hero = seat.Stack + seat.CurrentBet
			continue
		}
		if seat.IsFolded {
			continue
		}
		if v := seat.Stack + seat.CurrentBet; v > deepest {
			deepest = v
		}
	}
	return math.Min(hero, deepest)
}

func (b *Bot) preflop(s Spot) Move {
	pct := equity.HandPercentile(s.State.HeroCards)
	sit := preflop.SituationOf(s.State)

	// Short stacks do not play postflop poker. Below fifteen big blinds the
	// whole strategy is which hands go in and which do not.
	if effectiveBB(s) <= 15 && s.MaxRaise > 0 {
		shove := b.style.Open * 1.6
		if sit != preflop.Unopened {
			shove = b.style.Call
		}
		if pct <= b.jitter(shove) {
			return Move{Action: table.ActionAllIn}
		}
		if s.ToCall <= 0 {
			return Move{Action: table.ActionCheck}
		}
		return Move{Action: table.ActionFold}
	}

	open := b.jitter(b.style.Open)
	call := b.jitter(b.style.Call)
	three := b.jitter(b.style.ThreeBet)

	switch sit {
	case preflop.Unopened:
		if pct <= open {
			return b.raiseTo(s, 2.5*1.0+s.ToCall)
		}
	case preflop.FacingLimpers:
		limpers := 0.0
		for _, seat := range s.State.Seats {
			if seat.LastAction == "call" {
				limpers++
			}
		}
		if pct <= open*0.9 {
			return b.raiseTo(s, (3.0+limpers)*1.0)
		}
		if pct <= call*1.4 {
			return Move{Action: table.ActionCall}
		}
	case preflop.FacingRaise:
		if pct <= three {
			return b.raiseTo(s, 3.0*s.ToCall)
		}
		if pct <= call {
			return Move{Action: table.ActionCall}
		}
	case preflop.FacingThreeBet:
		if pct <= three*0.35 {
			return b.raiseTo(s, 2.4*s.ToCall)
		}
		if pct <= three*2.0 {
			return Move{Action: table.ActionCall}
		}
	}

	if s.ToCall <= 0 {
		return Move{Action: table.ActionCheck}
	}
	return Move{Action: table.ActionFold}
}

// raiseTo turns a wanted size into a legal Move. Amounts are incremental --
// chips added now -- so a "3x" against a bet of 3 is 9 more, not 9 in total.
func (b *Bot) raiseTo(s Spot, amount float64) Move {
	if s.MaxRaise <= 0 {
		if s.ToCall <= 0 {
			return Move{Action: table.ActionCheck}
		}
		return Move{Action: table.ActionCall}
	}
	if amount < s.MinRaise {
		amount = s.MinRaise
	}
	// Anything within a sliver of the stack is a shove; leaving one big blind
	// behind is not a strategy anybody plays.
	if amount >= s.MaxRaise*0.9 {
		return Move{Action: table.ActionAllIn}
	}
	return Move{Action: table.ActionRaise, Amount: amount}
}

func (b *Bot) postflop(s Spot) Move {
	opps := 0
	for _, seat := range s.State.Seats {
		if seat.PlayerID != s.State.HeroID && !seat.IsFolded {
			opps++
		}
	}
	if opps < 1 {
		opps = 1
	}
	r := topRange(opponentWidth(s.State))
	ranges := make([]equity.Range, opps)
	for i := range ranges {
		ranges[i] = r
	}
	sim := equity.SimulateEquityRNG(s.State.HeroCards, s.State.CommunityCards, ranges, b.iters, b.rng)
	eq := sim.WinRate + sim.TieRate*0.5

	pot := s.State.Pot
	if pot <= 0 {
		pot = 1
	}

	// Committing the stack is a separate decision from betting, and running
	// them together is what made the loose archetypes lose four times what a
	// loose player really loses. A hand may bet two thirds of the pot on any
	// pretext; it may not put half a stack in without something to put it in
	// with.
	commit := func(want float64) float64 {
		if s.MaxRaise <= 0 {
			return 0
		}
		if want > s.MaxRaise*0.5 && eq < 0.75 {
			want = math.Min(want, s.MaxRaise*0.5)
		}
		return want
	}

	if s.ToCall > 0 {
		required := s.ToCall / (pot + s.ToCall)
		if eq >= b.jitter(b.style.RaiseEq) {
			return b.raiseTo(s, commit(s.ToCall*2+pot*b.style.Sizing))
		}
		if b.rng.Float64() < b.style.BluffRaise && eq < 0.3 {
			return b.raiseTo(s, commit(s.ToCall*2+pot*b.style.Sizing))
		}
		if eq >= required*b.style.Tightness {
			return Move{Action: table.ActionCall}
		}
		return Move{Action: table.ActionFold}
	}

	if eq >= b.jitter(b.style.ValueBet) && b.rng.Float64() < b.style.CBet {
		return b.raiseTo(s, commit(pot*b.style.Sizing))
	}
	if b.rng.Float64() < b.style.Bluff {
		return b.raiseTo(s, commit(pot*b.style.Sizing*0.8))
	}
	return Move{Action: table.ActionCheck}
}

// The controls.
//
// A bb/100 figure on its own says nothing -- the field decides what a good one
// is. These are the yardsticks the tool is placed against in the same seat with
// the same cards: the two degenerate strategies that bound the problem, and one
// competent baseline it has to beat to be worth following.

// FoldBot folds everything it is not given for free. It loses exactly the
// blinds it posts, which is the floor: -75 bb/100 at six-handed.
type FoldBot struct{}

func (FoldBot) Name() string { return "always-fold" }
func (FoldBot) Act(s Spot) Move {
	if s.ToCall <= 0 {
		return Move{Action: table.ActionCheck}
	}
	return Move{Action: table.ActionFold}
}

// CallBot calls everything, all the way down. The other bound.
type CallBot struct{}

func (CallBot) Name() string { return "always-call" }
func (CallBot) Act(s Spot) Move {
	if s.ToCall <= 0 {
		return Move{Action: table.ActionCheck}
	}
	return Move{Action: table.ActionCall}
}

// TAGBot is the competent baseline: the same Style machinery as the population,
// set to the numbers a winning regular plays. If the tool cannot beat this in
// the same seat off the same deck, following it is worse than playing simply.
func TAGBot(rng *rand.Rand) *Bot {
	b := NewBot(StyleTAG, rng)
	b.iters = 1200
	return b
}
