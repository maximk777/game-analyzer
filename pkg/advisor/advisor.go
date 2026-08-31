package advisor

import (
	"fmt"
	"math"
	"strings"

	"poker-game-analyzer/pkg/equity"
	"poker-game-analyzer/pkg/preflop"
	"poker-game-analyzer/pkg/table"
)

type ActionRecommendation struct {
	Action      table.ActionType `json:"action"`
	Amount      float64          `json:"amount,omitempty"`
	EV          float64          `json:"ev"`
	IsPrimary   bool             `json:"is_primary"`
	SizingLabel string           `json:"sizing_label,omitempty"`
	// FoldEquity is the modelled probability that every live opponent folds to
	// this size. Exposed so the UI can show why one size beats another.
	FoldEquity float64 `json:"fold_equity"`
}

type AdvisorResponse struct {
	HandID            string                 `json:"hand_id"`
	HeroCards         [2]string              `json:"hero_cards"`
	Equity            float64                `json:"equity"`
	PotOdds           float64                `json:"pot_odds"`
	Actions           []ActionRecommendation `json:"actions"`
	PrimaryAction     table.ActionType       `json:"primary_action"`
	RecommendedAmount float64                `json:"recommended_amount"`
	Reasoning         string                 `json:"reasoning"`

	// EffectiveStack is the most money that can actually go in: no more than
	// hero has, and no more than the deepest live opponent can call.
	EffectiveStack float64 `json:"effective_stack"`
	// Opponents still live in the hand. EV depends on it, so it is reported.
	Opponents int `json:"opponents"`
	// HasReads is false when no opponent tendency data was available and fold
	// equity had to come from theory rather than observation. The UI must not
	// present a no-reads recommendation as though it were informed.
	HasReads bool `json:"has_reads"`
	// CallRangeFraction is the share of the opponents' range the call was
	// priced against: 1.0 means their whole range. Anything less means the
	// size of their bet was used to narrow it.
	CallRangeFraction float64 `json:"call_range_fraction"`
	// CallEquity is hero's equity against that narrowed range, which is what
	// the call decision actually rests on -- not the headline equity.
	CallEquity float64 `json:"call_equity"`

	// Risk is what beats hero right now, counted rather than sampled, so the
	// interface can show the losing cases instead of only the winning share.
	Risk *equity.RiskProfile `json:"risk,omitempty"`

	// TableKnowledge is how well the least-known live opponent is understood,
	// from 0 for a stranger to 1 for somebody there is a full read on. It is
	// the least-known one and not the average on purpose: one player nobody has
	// a line on makes the whole pot dangerous, whatever is known about the
	// rest.
	TableKnowledge float64 `json:"table_knowledge"`
	// Phase is TableKnowledge said in words, for the interface: the tool plays
	// carefully while it is learning the table and presses when it has learnt
	// it, and a user should be able to see which of those is happening.
	Phase string `json:"phase"`
}

// committedCallNarrowing is how much narrower a calling range is against a bet
// with nothing after it -- an all-in, or any bet on the river -- than minimum
// defence frequency describes. Three quarters of what MDF would defend with is
// gone, scaled by how completely the bet ends the hand, so a small bet in a
// deep pot with streets to come is left alone.
//
// Stated, not derived. It was set by measuring what the slice actually does to
// a hand's equity, on the boards where the tool was shoving live:
//
//	QQ on Tc Ad 5d As   top100% 0.872  top20% 0.641  top10% 0.537
//	32 on 2s 2h 4c      top100% 0.935  top20% 0.946  top10% 0.961
//
// Which is the property that makes this worth doing at all. Trip deuces beat a
// narrow range as comfortably as a wide one, so nothing is taken from them and
// their shove still stands. The queens hold up only while the range is wide,
// and that is exactly the value that is not really there. A blanket cap on
// sizing would have cost both hands equally.
const committedCallNarrowing = 0.75

func roundToTwoDecimals(val float64) float64 {
	return math.Round(val*100) / 100
}

// foldFrequency models how often a single opponent folds to a bet of size
// `bet` into `pot`.
//
// With no read, the anchor is the complement of minimum defence frequency:
// facing a bet of b into p, an opponent must continue with p/(p+b) of their
// range to stop a bluff being automatically profitable, so b/(p+b) folds. It is
// an equilibrium reference rather than a prediction of this particular player,
// but unlike a fixed constant it rises with the size -- which is what makes bet
// sizing a real decision instead of a step at 50% equity.
//
// With a read, the observed rate is anchored as that player's fold frequency
// against a pot-sized bet and carried across sizes by scaling how often they
// *continue* rather than how often they fold. Scaling the fold side saturates:
// a read of 0.55 against a large overbet clamps to 1.0, and once every opponent
// folds with certainty the number of opponents stops mattering at all. Scaling
// the continue side approaches zero without reaching it, so a six-way pot stays
// harder to take down than a heads-up one.
//
// The read never replaces the baseline outright. It is blended in with weight
// `readWeight`, so a confident-sounding tendency can move the estimate but
// cannot become the estimate -- see readWeight for why that matters.
func foldFrequency(bet, pot, observed float64, weight float64) float64 {
	return foldFrequencyAtDepth(bet, pot, observed, weight, 0)
}

// foldFrequencyAtDepth is foldFrequency, told what the bet costs the player
// deciding whether to pay it.
//
// Minimum defence frequency is a ratio of bet to pot and says nothing about
// stacks, so a bet of a pot folded out the same share of a range whether it
// took the caller's last chip or one fiftieth of their stack. It does not:
// somebody risking two per cent of what they have calls light, because the
// price of being wrong is small and busting the short stack is worth something.
//
// Live, hero had 68,080 against 3.14M and the advice came out as it would
// between equal stacks -- which is what "the strategy is straight-line, as
// though we were sitting on level terms" describes. The correction is stated,
// not derived, and it only ever reduces fold equity: at a bet that takes the
// caller's whole stack nothing changes, and at a bet they barely feel a third
// of the folding goes.
func foldFrequencyAtDepth(bet, pot, observed, weight, callerStack float64) float64 {
	base := rawFoldFrequency(bet, pot, observed, weight)
	if callerStack <= 0 || bet <= 0 {
		return base
	}
	risk := math.Min(bet/callerStack, 1)
	// Full weight when the bet commits them, and a third off when it costs
	// them nothing worth the name.
	return base * (1 - shallowRiskFoldPenalty*(1-risk))
}

// shallowRiskFoldPenalty is how much of the modelled folding disappears against
// a player the bet cannot hurt. Stated, not derived, and deliberately modest:
// the point is that being covered enters the decision at all, not that this
// particular number is exact.
const shallowRiskFoldPenalty = 0.33

func rawFoldFrequency(bet, pot, observed float64, weight float64) float64 {
	if bet <= 0 || pot <= 0 {
		return 0
	}
	mdfContinue := pot / (pot + bet)
	baseline := 1 - mdfContinue
	if weight <= 0 {
		return baseline
	}

	// At a pot-sized bet mdfContinue is 0.5, so this returns `observed` exactly.
	continueFreq := mdfContinue * (1 - observed) / 0.5
	fromRead := math.Min(math.Max(1-continueFreq, 0), 1)

	blended := baseline*(1-weight) + fromRead*weight
	return math.Min(math.Max(blended, 0), 1)
}

// Weight limits for opponent reads.
const (
	// priorHands is the sample size at which a counted tendency earns half of
	// the maximum weight. Shrinking towards the equilibrium baseline stops a
	// tendency computed from three hands being treated as a fact.
	priorHands = 25.0
	// maxMeasuredWeight caps even a large counted sample. The baseline always
	// keeps a say, because a player's frequency in the spots we happened to
	// observe is not their frequency in this spot.
	maxMeasuredWeight = 0.60
	// maxModelledWeight caps a tendency that came from the language model
	// rather than from counted events. It is an opinion formed from summary
	// statistics, and it must never be able to carry a decision on its own --
	// "he is definitely bluffing, shove" is exactly the failure being designed
	// out here.
	maxModelledWeight = 0.25
)

// readWeight converts sample size and provenance into how far the model is
// allowed to move from the equilibrium baseline towards a read.
func readWeight(hands int, modelled bool) float64 {
	if hands <= 0 && !modelled {
		return 0
	}
	cap := maxMeasuredWeight
	if modelled {
		cap = maxModelledWeight
	}
	// A modelled read carries some weight even with no counted hands behind it,
	// because it is derived from the statistics that do exist; a counted read
	// with no hands carries none.
	n := float64(hands)
	confidence := n / (n + priorHands)
	if modelled && confidence < 0.5 {
		confidence = 0.5
	}
	return cap * confidence
}

// equityWhenCalled approximates equity against the part of the range that
// continues, for use when no real range simulation is available.
//
// It models opponent hand strength as uniform over their range: we beat the
// bottom `winEq` of it, and they fold the bottom `f` of it, so what calls is the
// top (1-f), of which we beat (winEq - f). Some such adjustment is essential --
// without it, being called was as good as being called by a weak hand, so a
// bigger bet was always better and the model would shove 97bb off 70% raw
// equity.
//
// This pure rank model is deliberately conservative and known to be harsh: real
// equity does not collapse that sharply against a tighter range, because hands
// keep backdoor and draw equity against holdings that beat them right now.
// Inputs.EquityVsTop supersedes it whenever the caller can run the simulation
// for real.
func equityWhenCalled(winEq, f float64) float64 {
	continueFreq := 1 - f
	if continueFreq <= 1e-9 {
		return 0
	}
	adjusted := (winEq - f) / continueFreq
	return math.Min(math.Max(adjusted, 0), 1)
}

// fallbackEquityWhenCalled is what the advisor actually uses when no range
// simulator is supplied.
//
// Both available extremes are demonstrably wrong. Leaving equity unconditional
// made every larger bet better than the last, so the model shoved 97bb off 70%
// raw equity. Taking the rank model literally makes equity vanish whenever the
// fold frequency exceeds it, so a flop bluff with two overcards and a backdoor
// draw scores zero when called and is never bet -- but equity is not a rank:
// a hand that is behind still wins some of the time.
//
// The midpoint is a stated interpolation, not a measurement. It exists so the
// fallback fails in neither direction; Inputs.EquityVsTop replaces it with a
// real simulation, and the live path always supplies one.
func fallbackEquityWhenCalled(winEq, f float64) float64 {
	rank := equityWhenCalled(winEq, f)
	return 0.5*rank + 0.5*winEq
}

// equityRealisation is the share of raw equity a hand actually captures when
// streets remain to be played.
//
// All-in equity assumes every card gets dealt. With money behind that is not
// what happens: hands get folded on later streets, position decides who has to
// commit first, and extra opponents make it likelier someone else improves. A
// one-street model that ignores this treats the flop and the river as the same
// decision -- measured, it scored an identical EV at any stack depth on either
// street, which is the clearest sign that the streets to come were invisible
// to it.
//
// The magnitudes are the standard approximations: in position a hand realises
// somewhat more than its raw equity, out of position somewhat less, and every
// additional opponent costs a little more. They are stated rather than derived,
// and are deliberately mild -- the point is that depth and position enter the
// decision at all, not that these particular numbers are exact.
func equityRealisation(street table.Street, position table.Position, opponents int, allIn bool) float64 {
	// On the river, or when the money is already in, there is nothing left to
	// realise: the raw equity is the equity.
	if allIn || street == table.StreetRiver || street == table.StreetShowdown {
		return 1.0
	}

	r := 1.0
	switch position {
	case table.PosBTN, table.PosCO:
		r += 0.08
	case table.PosSB, table.PosBB, table.PosUTG:
		r -= 0.08
	}

	if opponents > 1 {
		r -= 0.04 * float64(opponents-1)
	}

	return math.Min(math.Max(r, 0.70), 1.15)
}

// heroPosition returns hero's seat position, empty when it was not read.
func heroPosition(state table.HandState) table.Position {
	for _, seat := range state.Seats {
		if seat.PlayerID == state.HeroID && state.HeroID != "" {
			return seat.Position
		}
	}
	return ""
}

// bettorRangeFraction is the share of an opponent's range that a bet of the
// given size represents.
//
// A player who bets is not holding a random hand, and treating them as though
// they were produced the worst advice this tool has given: hero held queen-high
// on a board whose only pair was on the felt, faced a half-pot river bet, and
// was told to call because "equity" against a random hand came to 33.7% against
// 33.3% pot odds.
//
// The anchor is the equilibrium bluff-to-value ratio. A bet of b into a pot of
// p (before the bet) is priced so a caller needs b/(p+2b) equity, and the
// bettor's range is concentrated in roughly that top share: a pot-sized bet
// implies about the top third, a half-pot bet about the top half. Bigger bets
// mean tighter ranges, which is the direction that matters.
func bettorRangeFraction(bet, potIncludingBet float64) float64 {
	potBefore := potIncludingBet - bet
	if potBefore <= 0 || bet <= 0 {
		return 1
	}
	frac := potBefore / (potBefore + 2*bet)
	return math.Min(math.Max(frac, 0.05), 1)
}

// observedFoldRate returns the opponent fold tendency relevant to the action
// hero is about to take, and whether one was actually available. It never
// invents a default: a bluff recommended on fabricated fold equity is worse
// than no recommendation.
//
// Which tendency is relevant depends on whether hero is betting or raising, and
// getting that wrong was expensive. A player folds to half the bets aimed at
// them and to a small fraction of the raises, because by the time they are
// facing a raise they have already put money in with something. The model
// priced hero's own flop raises off fold-to-a-bet, concluded that raising
// folded out half the field, and raised. Measured against a table of strong
// opponents that single substitution turned +3.5 bb/100 into -5.8, and the
// report named the spot outright: raising into a bet on the flop, fifteen big
// blinds a hand.
//
// There is no falling back from one to the other. Without a read that describes
// the action hero is taking there is no read, and the aggressive branch stays
// shut -- which is the conservative answer and the correct one.
func observedFoldRate(street table.Street, t map[string]float64, heroRaising bool) (float64, bool) {
	if t == nil {
		return 0, false
	}
	var keys []string
	switch {
	case heroRaising && street == table.StreetPreflop:
		// Hero is reraising, so what matters is how they answer a reraise.
		keys = []string{"fold_to_3bet"}
	case heroRaising:
		keys = []string{"fold_to_raise_post"}
	case street == table.StreetPreflop:
		// Hero is opening; the opponent will be facing a raise.
		keys = []string{"fold_to_raise", "fold_to_3bet"}
	case street == table.StreetFlop:
		keys = []string{"fold_to_cbet", "fold_to_bet"}
	default:
		keys = []string{"fold_to_bet", "fold_to_cbet"}
	}
	for _, k := range keys {
		if v, ok := t[k]; ok && v >= 0 && v <= 1 {
			return v, true
		}
	}
	return 0, false
}

// liveOpponents counts opponents still in the hand and returns the largest
// stack among them, which bounds how much of a bet can ever be called.
func liveOpponents(state table.HandState) (count int, deepest float64) {
	for _, seat := range state.Seats {
		if seat.PlayerID == "" || seat.PlayerID == state.HeroID {
			continue
		}
		if !seat.IsActive || seat.IsFolded {
			continue
		}
		count++
		if seat.Stack > deepest {
			deepest = seat.Stack
		}
	}
	return count, deepest
}

// Inputs bundles everything the advisor reasons over. It exists so that
// conditional equity can be computed for real rather than approximated: see
// EquityVsTop.
type Inputs struct {
	State         table.HandState
	Equity        equity.EquityResult
	OppTendencies map[string]float64

	// EquityVsTop returns hero equity against the strongest `frac` (0..1) of
	// the opponents' current ranges -- that is, against the part that would
	// actually call a bet of the corresponding size. Optional: when nil, the
	// rank approximation in equityWhenCalled is used instead. Supplying it is
	// what turns "roughly how equity behaves against a tighter range" into a
	// real Monte Carlo answer.
	EquityVsTop func(frac float64) float64

	// Risk is an exact count of what an opponent's range already beats hero
	// with, on the board as it stands. Optional. Equity says how often hero
	// wins; this says what the losses are made of, which is the difference
	// between a hand that is 88% because the opponent usually has nothing and
	// one that is 88% because the other 12% is a full house.
	Risk *equity.RiskProfile

	// Opponents is what is known about each live opponent separately.
	//
	// One read for the whole table was the previous shape, and it is wrong in
	// the place it matters most. Fold equity is the product of everybody
	// folding, so a pot with a nit and a station in it is not the same as a pot
	// with two of either -- and the model was being handed whichever of them
	// happened to be found first and told to raise it to the power of two. The
	// station is the one that decides whether a bluff works, and the old shape
	// could not see him.
	//
	// Empty falls back to OppTendencies, ReadHands and ReadModelled applied to
	// every opponent alike, which is exactly what the code did before.
	Opponents []OpponentRead

	// ReadHands is how many hands the tendencies in OppTendencies were counted
	// over. Zero means they were not counted at all.
	ReadHands int
	// ReadModelled marks tendencies produced by the language model from summary
	// statistics rather than counted from observed actions. Modelled reads are
	// capped far below counted ones: they inform the estimate, never carry it.
	ReadModelled bool
}

// OpponentRead is what is known about one live opponent.
type OpponentRead struct {
	PlayerID string
	// Tendencies is the same shape profiler.GetPlayerTendencies returns.
	Tendencies map[string]float64
	// Hands is the counted sample behind the tendencies, and Modelled marks
	// tendencies the language model produced rather than counted. Together they
	// decide how far the estimate may move -- see readWeight.
	Hands    int
	Modelled bool
	// Stack is what this opponent has left, which decides how much a bet of a
	// given size actually costs them.
	Stack float64
}

// CalculateAdvice is the compatibility entry point for callers that have no
// range simulator to offer.
func CalculateAdvice(state table.HandState, eq equity.EquityResult, oppTendencies map[string]float64) AdvisorResponse {
	return Calculate(Inputs{State: state, Equity: eq, OppTendencies: oppTendencies})
}

func Calculate(in Inputs) AdvisorResponse {
	state := in.State
	eq := in.Equity
	oppTendencies := in.OppTendencies

	pot := state.Pot
	if pot <= 0 {
		pot = 1.0
	}

	heroCurrentBet := 0.0
	heroStack := 0.0
	heroSeated := false
	for _, seat := range state.Seats {
		if seat.PlayerID == state.HeroID && state.HeroID != "" {
			heroCurrentBet = seat.CurrentBet
			heroStack = seat.Stack
			heroSeated = true
			break
		}
	}

	toCall := state.CurrentBet - heroCurrentBet
	if toCall < 0 {
		toCall = 0
	}
	if state.CurrentBet > 0 && toCall == 0 && heroCurrentBet == 0 {
		toCall = state.CurrentBet
	}

	// The amount owed has to be at least what is already in front of somebody
	// else. It comes off the call button, which is the one place the client
	// states it outright -- and when that fails to read, what is left is a
	// number from somewhere else entirely. Live, two players sat all-in for
	// 199,680 apiece and the tool offered to call 2,000, which is the big
	// blind: a price nobody at that table could pay.
	//
	// The chips on the felt are the check. They are read separately, they
	// cannot be smaller than the wager they represent, and a call cannot be
	// cheaper than the largest of them.
	owed := 0.0
	for _, seat := range state.Seats {
		if seat.PlayerID == state.HeroID || seat.IsFolded {
			continue
		}
		if seat.CurrentBet > owed {
			owed = seat.CurrentBet
		}
	}
	if owed > heroCurrentBet {
		if want := owed - heroCurrentBet; want > toCall {
			toCall = want
		}
	}

	potOdds := 0.0
	if toCall > 0 {
		potOdds = toCall / (pot + toCall)
	}

	winEq := eq.WinRate + eq.TieRate*0.5

	var hasReads bool

	opponents, deepestOpponent := liveOpponents(state)
	if opponents < 1 {
		opponents = 1
	}

	// Money above the effective stack can never be called, so no sizing may
	// exceed it. Previously only hero's stack was consulted -- and hero's stack
	// was itself invented when hero could not be found among the seats.
	// Zero means "not observed". Nothing is invented here: without stack data
	// there is no cap and no all-in option, rather than a fabricated ceiling
	// that would silently collapse every sizing onto one number.
	effectiveStack := heroStack
	if !heroSeated || heroStack <= 0 {
		effectiveStack = deepestOpponent
	} else if deepestOpponent > 0 && deepestOpponent < effectiveStack {
		effectiveStack = deepestOpponent
	}

	stacksKnown := effectiveStack > 0
	heroPos := heroPosition(state)

	// One model per live opponent. Each folds at their own rate, feels the bet
	// against their own stack, and is believed to the extent their own sample
	// justifies.
	// Whether hero's aggression would be a bet or a raise decides which read
	// describes it, and it is settled by whether anything is owed.
	models := opponentModels(in, state, opponents, deepestOpponent, toCall > 0)
	hasReads = false
	for _, m := range models {
		if m.weight > 0 {
			hasReads = true
		}
	}

	// evRaise is the EV of putting in `raiseTo` total, with `opponents` players
	// each independently folding at the modelled rate for that size.
	//
	// Every live opponent must fold for the pot to be won uncontested; if any
	// call, each caller adds (raiseTo - toCall) since their existing bet is
	// already counted in pot. Modelling the callers is what the previous
	// formula omitted: it assumed exactly one, so a 5-way pot and a heads-up
	// pot produced byte-identical advice.
	evRaiseAt := func(pot, toCall, raiseTo float64) (ev float64, allFold float64) {
		if raiseTo <= 0 {
			return 0, 0
		}
		// Everybody has to fold for the bet to win the pot uncontested, and
		// they fold at their own rates: the chance of that is the product, not
		// a single rate raised to a power. `f` below is the average, which is
		// what the range-narrowing needs -- how much of a typical opponent's
		// range continues.
		bet := raiseTo - toCall
		allFoldProduct := 1.0
		fSum := 0.0
		for _, m := range models {
			fi := foldFrequencyAtDepth(bet, pot, m.observed, m.weight, m.stack)
			allFoldProduct *= fi
			fSum += fi
		}
		f := fSum / float64(len(models))

		// Minimum defence frequency is a bluff-catching rule, and it describes
		// a defender who still has something to defend with: streets left to
		// play, implied odds, and fold equity of their own on the next one.
		// A bet that puts their whole stack in takes all three away. What is
		// left has to have showdown value now, and that is a much smaller part
		// of a range than MDF allows.
		//
		// This is the one place the model was losing money. Live, queens on
		// Tc Ad 5d As shoved 32,766 into 62,947 -- the equity behind that was
		// measured against the strongest two thirds of a *preflop* hand
		// ranking, which on that board is mostly hands that missed it. Anyone
		// calling an all-in there holds an ace far more often than a preflop
		// ranking suggests, and against an ace the queens have two outs. Tens,
		// treys and trip-deuce hands shoved for the same reason.
		//
		// The proper repair is opponent ranges that know what board they are
		// on; ours are preflop rankings with no board awareness at all. Until
		// that exists, narrowing what calls a committing bet moves the estimate
		// in the direction the missing knowledge points. The factor is stated,
		// not derived, and it scales with how much of the stack is at risk, so
		// a small bet in a deep pot is untouched and only a shove is fully
		// discounted.
		// How often they continue and what they continue with are separate
		// questions, and only the second one is answered badly here. MDF
		// answers the first correctly: it is the frequency a competent
		// opponent must defend at, whatever they hold. The second is answered
		// by a preflop hand ranking that has never looked at the board, and
		// facing an all-in that is where it fails -- a caller with no streets
		// left keeps showdown value and folds the rest, so the part of their
		// range that calls is stronger than its size suggests.
		//
		// So the frequency is left alone and only the slice equity is measured
		// against is narrowed. Making the two consistent was tried first and is
		// worse: hands that stop calling have to fold instead, fold equity on
		// the queens above went from 34% to 67%, and the shove came out better
		// than before. Folding the field out is not a cost the model should be
		// paid for.
		// Only where a simulator can measure the narrower slice. The fallback
		// below has no board and no ranges: it derives equity-when-called from
		// the fold frequency itself, so narrowing the slice as well counts the
		// same discount twice. It did, and a river hand with 97% equity and a
		// four-big-blind stack stopped shoving -- which is the whole strategy
		// with that hand. The same rule already governs bettorRangeFraction on
		// the call side, for the same reason.
		callShare := 1 - f
		if in.EquityVsTop != nil {
			// How completely this bet ends the hand. A bet that takes the
			// caller's stack ends it, and so does any bet on the river --
			// there are no cards to come either way. Both are the same fact,
			// and it is the fact that makes MDF describe the wrong range: a
			// caller with a street left continues with draws and with hands
			// that can bluff later, and a caller with nothing left continues
			// only with showdown value.
			//
			// Live report that added the river half: board paired kings, a
			// queen on the river, hero holding eights. Two pair, and the tool
			// bet the pot. Against everything it is 75%; against the strongest
			// tenth it is 31%, because the hands that call a river bet on that
			// board are the kings and queens that just made a full house.
			// Nothing about that risk reached the decision.
			// Whose stack is at risk is the caller's, not hero's.
			//
			// This was measured against the effective stack, which is hero's
			// whenever hero is the shorter -- so a shove that took all of
			// hero's money was treated as committing whoever called it, however
			// deep they were. Live, hero had 68,080 against 3.14M: calling that
			// shove costs the opponent two per cent of their stack, they are
			// committed to nothing, and they call as wide as they like. Told
			// otherwise, the model credited hero with folding out hands that
			// were never folding, and the advice came out the same as it would
			// between equal stacks. "As though we were sitting on level terms"
			// is exactly what it was doing.
			callerStack := deepestOpponent
			if callerStack <= 0 {
				callerStack = effectiveStack
			}
			finality := 0.0
			if callerStack > 0 {
				finality = math.Min(raiseTo/callerStack, 1)
			}
			if state.Street == table.StreetRiver || state.Street == table.StreetShowdown {
				finality = 1
			}
			callShare *= 1 - committedCallNarrowing*finality
		}
		allFold = allFoldProduct

		expectedCallers := float64(opponents) * (1 - f)
		contested := 1 - allFold
		if contested <= 1e-9 {
			return allFold * pot, allFold
		}
		callersGivenContested := expectedCallers / contested

		callEq := 0.0
		if in.EquityVsTop != nil {
			// The callers are the strongest `callShare` of the range.
			callEq = math.Min(math.Max(in.EquityVsTop(callShare), 0), 1)
		} else {
			callEq = fallbackEquityWhenCalled(winEq, f)
		}

		// Realisation is deliberately not applied here. It would give an all-in
		// the full value of its equity while discounting every smaller size,
		// and so make shoving look better the less the model understands about
		// the streets it skips -- the opposite of the correction intended.
		// Choosing a size needs a multi-street model; continuing does not.
		added := raiseTo - toCall
		finalPot := pot + raiseTo + callersGivenContested*added
		evCalled := callEq*finalPot - raiseTo

		return allFold*pot + contested*evCalled, allFold
	}

	// evRaise is the EV of a wager in the situation actually in front of hero.
	// evRaiseAt is the same calculation asked about a hypothetical one, which
	// is what the trap below needs: what a raise would be worth if this
	// opponent bets after hero checks.
	evRaise := func(raiseTo float64) (float64, float64) {
		return evRaiseAt(pot, toCall, raiseTo)
	}

	// How well the table is understood, and what that costs.
	//
	// A player nobody has a read on is not a neutral opponent, they are an
	// unknown one, and the two are different things to put a stack in against.
	// The equilibrium baseline the model falls back to is a reasonable average
	// over strategies; it is not a description of the person in that seat, who
	// might be a rock or might be a maniac, and the spread between those is
	// exactly the money at risk. Sitting down at a fresh table -- or having
	// somebody get up and a stranger take the chair -- should make the tool
	// careful, and it did not: an unread opponent produced the same advice as a
	// thoroughly read average one.
	//
	// The charge is quadratic in how much of the stack is going in, which is
	// the shape wanted: a third of a pot against a stranger costs almost
	// nothing, and a shove against one costs a fifth of the stack in modelled
	// value. It disappears entirely once the table is known, which is what
	// turns "play carefully while learning" and "press once you have learnt"
	// into one continuous rule instead of two modes with a switch between them.
	knowledge := tableKnowledge(models)
	caution := func(amount float64) float64 {
		if !stacksKnown || effectiveStack <= 0 || amount <= 0 || knowledge >= 1 {
			return 0
		}
		share := math.Min(amount/effectiveStack, 1)
		return darkHorseCaution * (1 - knowledge) * share * amount
	}

	evFold := 0.0

	// Equity for the call decision is measured against the range that actually
	// bet, not against every hand the opponent could hold.
	// Narrowing is applied only where a real simulation can measure it. The
	// rank approximation is far too crude to fold on: it scores equity as the
	// share of the range beaten, which collapses a flush draw to nothing
	// against a tight range when in truth a draw barely cares what it is
	// against. The live path always supplies a simulator, which is where this
	// has to be right.
	// Narrowing applies only after the flop. Preflop the money owed is largely
	// forced -- blinds and straddles are posted with whatever the dealer gave
	// out -- so treating it as a chosen bet priced hero's ace-king against a
	// top-39% range and folded it getting better than 3 to 1.
	callEquity := winEq
	callRangeFraction := 1.0
	if toCall > 0 && state.Street != table.StreetPreflop && in.EquityVsTop != nil {
		callRangeFraction = bettorRangeFraction(toCall, pot)
		// And then: how often does *this* player bet?
		//
		// The equilibrium figure above is a statement about how wide a range
		// has to be to make a bet of that size balanced. It is not a statement
		// about the person who just bet. Someone betting nine flops in ten is
		// betting nearly everything they hold, and hero's hand is far better
		// against that than the sizing suggests; someone betting three in ten
		// has a range hero is far behind. Priced off the equilibrium alone the
		// tool folded to the first and called the second, which is backwards
		// and is where "he studies them and then goes after them" has to
		// actually happen.
		//
		// The read is blended, never substituted, by the same weight rule as
		// everything else: a frequency counted over twelve flops moves the
		// estimate a little and cannot become it.
		if f, w, ok := bettorBetFrequency(in, state); ok {
			callRangeFraction = math.Min(math.Max(callRangeFraction*(1-w)+f*w, 0.05), 1)
		}
		callEquity = math.Min(math.Max(in.EquityVsTop(callRangeFraction), 0), 1)
	}

	// Continuing without committing the stack does not realise the whole of a
	// hand's all-in equity: cards get folded on later streets, position decides
	// who acts first, and every extra opponent is another player who might
	// improve. Calling all-in realises it in full, because every card is dealt.
	callCommits := stacksKnown && toCall >= effectiveStack-1e-9
	callRealisation := equityRealisation(state.Street, heroPos, opponents, callCommits)

	var evCall float64
	if toCall == 0 {
		// Checking sees the next card; how much of the equity survives to be
		// shown down depends on position and on how many players are still in.
		evCall = winEq * callRealisation * pot

		// And sometimes checking is worth a great deal more than that, because
		// somebody bets into it.
		//
		// This is the trap, and it is the first thing in the model that looks
		// past the action in front of it. Until now a check was priced as
		// giving up on the pot: equity times what is already in it. Nothing in
		// that expression contains the money a check-raise wins, so slowplaying
		// and inducing were not merely played badly, they were invisible --
		// there was no term for them. A hand strong enough to raise a bet that
		// has not happened yet was always better off betting, whatever the
		// opponent was going to do.
		//
		// What makes it computable now is the read the profiler counts: how
		// often this player bets when they are checked to. With that, a check
		// is a branch -- they check behind and the hand goes to the next card,
		// or they bet and hero gets the same decision with more money in the
		// middle -- and both halves are things the model can already price.
		//
		// It stays entirely off without the read. There is no equilibrium
		// figure for "how often does an unknown player bet when checked to"
		// that is worth trapping on, and inventing one would be exactly the
		// fabricated-fold-equity mistake in a new place.
		if state.Street != table.StreetPreflop && in.EquityVsTop != nil {
			if pAny, fBettor, w, ok := checkedToBetFrequency(in, state); ok {
				bet := trapBetSize * pot
				if stacksKnown && bet > effectiveStack {
					bet = effectiveStack
				}
				trapPot := pot + bet

				// Hero's equity against the range that would do the betting.
				// A player who bets four flops in five is betting nearly
				// everything they hold, and that is the whole of why trapping
				// them is profitable.
				betEq := math.Min(math.Max(in.EquityVsTop(fBettor), 0), 1)

				// The best answer to that bet: give it up, pay it, or raise it.
				// Folding is the floor, so the branch can never be worth less
				// than nothing.
				answer := 0.0
				if v := betEq*callRealisation*(trapPot+bet) - bet; v > answer {
					answer = v
				}
				for _, size := range []float64{2 * bet, 2*bet + 0.66*pot, effectiveStack} {
					if size <= 0 {
						continue
					}
					if stacksKnown && size > effectiveStack {
						size = effectiveStack
					}
					if ev, _ := evRaiseAt(trapPot, bet, size); ev > answer {
						answer = ev
					}
				}

				trap := (1-pAny)*evCall + pAny*answer
				// Weighted by how much of a read is behind it, like every other
				// read in this file: a frequency counted over a dozen flops
				// moves the estimate and cannot become it.
				evCall = (1-w)*evCall + w*trap
			}
		}
	} else {
		evCall = callEquity*callRealisation*(pot+toCall) - toCall
		// Paying off a stranger is the same risk as betting into one.
		evCall -= caution(toCall)
	}

	cap := func(v float64) float64 {
		if stacksKnown && v > effectiveStack {
			return effectiveStack
		}
		return v
	}

	addSizing := func(actions []ActionRecommendation, act table.ActionType, amount float64, label string) []ActionRecommendation {
		amount = roundToTwoDecimals(cap(amount))
		if amount <= 0 {
			return actions
		}
		// A sizing the effective stack has clipped is an all-in, and must be
		// named one -- otherwise the same amount appears twice under different
		// labels and the plain bet wins the tie, so an all-in can never be
		// recommended even when it is the whole strategy.
		if stacksKnown && amount >= effectiveStack {
			act = table.ActionAllIn
			label = "All-In"
		}
		// Do not offer two sizes that the effective stack has collapsed onto
		// the same number.
		for _, existing := range actions {
			if existing.Amount == amount && existing.Action == act {
				return actions
			}
		}
		ev, fe := evRaise(amount)
		ev -= caution(amount)
		return append(actions, ActionRecommendation{
			Action:      act,
			Amount:      amount,
			EV:          ev,
			SizingLabel: label,
			FoldEquity:  fe,
		})
	}

	var actions []ActionRecommendation

	actions = append(actions, ActionRecommendation{
		Action:      table.ActionFold,
		Amount:      0,
		EV:          evFold,
		SizingLabel: "Fold",
	})

	if toCall == 0 {
		actions = append(actions, ActionRecommendation{
			Action:      table.ActionCheck,
			Amount:      0,
			EV:          evCall,
			SizingLabel: "Check",
		})

		betAction := table.ActionBet
		if state.Street == table.StreetPreflop {
			betAction = table.ActionRaise
		}

		actions = addSizing(actions, betAction, pot*0.33, "33% Pot")
		actions = addSizing(actions, betAction, pot*0.66, "66% Pot")
		actions = addSizing(actions, betAction, pot*1.0, "Pot")
		if stacksKnown {
			actions = addSizing(actions, table.ActionAllIn, effectiveStack, "All-In")
		}
	} else {
		actions = append(actions, ActionRecommendation{
			Action:      table.ActionCall,
			Amount:      roundToTwoDecimals(cap(toCall)),
			EV:          evCall,
			SizingLabel: "Call",
		})

		minRaise := state.MinRaise
		if minRaise < toCall*2.0 {
			minRaise = toCall * 2.0
		}

		actions = addSizing(actions, table.ActionRaise, minRaise, "Min-Raise")
		actions = addSizing(actions, table.ActionRaise, math.Max(toCall*2.5, minRaise), "2.5x")
		actions = addSizing(actions, table.ActionRaise, math.Max(toCall+pot*0.66, minRaise), "66% Pot")
		actions = addSizing(actions, table.ActionRaise, math.Max(pot+2.0*toCall, minRaise), "Pot")
		if stacksKnown {
			actions = addSizing(actions, table.ActionAllIn, effectiveStack, "All-In")
		}
	}

	// Pick the highest-EV action. Aggressive lines are only eligible when they
	// are backed by value or by fold equity we have actually counted -- a bluff
	// priced off a theoretical fold frequency, against an opponent we know
	// nothing about, is a guess dressed as a calculation. A modelled tendency
	// does not open the bluff branch either: it may size a value bet, but it
	// may not be the reason to put money in with a losing hand.
	// A bluff is only priced when every player who could call it has been
	// counted, and only when what was counted says bluffing works.
	//
	// The first half was there and the second was missing, and the second is
	// the one that costs money. Knowing an opponent is not a reason to bluff
	// them; it is a reason to bluff them *if they fold*. As written, any
	// counted read opened the aggressive branch -- including a read saying the
	// player never folds anything -- and the tool duly started betting into
	// stations. Measured against every field: the read-driven version's raises
	// earned less than the read-free version's in every single spot the report
	// broke out, on the flop, on the turn, checked to and facing a bet.
	//
	// What makes a bluff work is folding *above* what the price demands. Facing
	// a pot-sized bet an opponent must continue with half their range, so a
	// player folding more than half of it is paying for the bluff and a player
	// folding less is charging for it. That is the line, it is not a tuned
	// constant, and it comes straight out of the same minimum-defence
	// arithmetic the fold model is built on.
	countedReads := true
	for _, m := range models {
		if !m.counted || m.observed <= bluffWorthwhileFold {
			countedReads = false
			break
		}
	}

	// Which equity opens the aggressive branch depends on whether somebody has
	// already bet.
	//
	// Facing a bet, the range hero is up against is not the opponent's whole
	// range -- it is the part of it that bet, and the call decision already
	// knows that: callEquity is measured against exactly that slice. The gate
	// on raising was measured against everything. So the tool would decline to
	// call a bet on narrowed equity and then raise it on the un-narrowed
	// figure, which is not a strategy, it is two different opinions about the
	// same range in one decision.
	//
	// The harness put a number on it. Raising into a bet on the flop lost the
	// tool sixteen big blinds a hand across three hundred and sixty hands,
	// where a competent opponent in the same spot made four -- twenty big
	// blinds a hand of difference, and the largest single entry in the report.
	gateEquity := winEq
	if toCall > 0 {
		gateEquity = callEquity
	}
	aggressionAllowed := gateEquity >= 0.50 || countedReads

	bestIdx := 0
	bestEV := evFold

	// With nothing owed, folding is strictly worse than checking -- it costs
	// whatever the hand would have won and saves nothing, because checking is
	// free. It stays in the list so the interface still shows what every option
	// is worth, but it can never be the recommendation.
	//
	// The comparison below is a strict greater-than, so a tie leaves whatever
	// was seeded. Seeded with fold at zero, a hand with no equity at all tied
	// with checking and the tool advised folding a free card. Found by the
	// strategy sweep, on air at showdown against three players.
	freeToCheck := toCall == 0
	if freeToCheck {
		for i, act := range actions {
			if act.Action == table.ActionCheck {
				bestIdx, bestEV = i, act.EV
				break
			}
		}
	}

	for i, act := range actions {
		if freeToCheck && act.Action == table.ActionFold {
			continue
		}
		isAggressive := act.Action == table.ActionBet ||
			act.Action == table.ActionRaise ||
			act.Action == table.ActionAllIn
		if isAggressive && !aggressionAllowed {
			continue
		}
		if act.EV > bestEV {
			bestEV = act.EV
			bestIdx = i
		}
	}

	// Preflop, the charts override the EV comparison entirely where they have an
	// opinion. The comparison prices a call as though the hand were about to be
	// shown down, which is blind to position, to the streets left to play and
	// to implied odds -- it folded pocket threes getting better than 2 to 1 with
	// thirty-seven calls behind, and folded ace-king in the blinds. The EV
	// numbers are still computed and still reported per option; only the choice
	// is taken from the chart.
	chartAction, charted := chartedAction(state)
	if charted {
		if chartAction == preflop.Raise {
			// A chart that says "raise" means the raise that chart is written
			// for -- an open of two and a half big blinds at a hundred deep --
			// and not whichever size the expected-value comparison likes best.
			//
			// It was the latter, and the comparison always likes the biggest
			// one: fold equity rises with size, so an open-shove of a hundred
			// big blinds "wins" a pot of one and a half almost every time and
			// scores higher than an open. Measured against a table of strong
			// opponents that is what the tool did with its best hands, at the
			// ninety-ninth percentile of its bet sizes: sixty-six times the
			// pot, preflop, with aces. It is the same blindness that produced
			// the postflop overbets -- a one-street model choosing between
			// sizes whose whole difference is what happens on the streets after
			// -- and the chart is exactly the outside knowledge that settles
			// it.
			//
			// Short stacks are not a special case: a chart open of two and a
			// half blinds against a stack of ten is an all-in, and addSizing
			// names it one.
			if want := chartRaiseAmount(state, toCall, heroCurrentBet); want > 0 {
				actions = addSizing(actions, table.ActionRaise, want, "Chart Open")
				if idx, ok := nearestRaise(actions, roundToTwoDecimals(cap(want))); ok {
					bestIdx = idx
				}
			}
		} else if idx, ok := matchChartAction(actions, chartAction, toCall); ok {
			bestIdx = idx
		}
	}

	actions[bestIdx].IsPrimary = true
	primaryAct := actions[bestIdx]

	bluffFreq := 0.0
	if oppTendencies != nil {
		if bf, ok := oppTendencies["bluff_frequency"]; ok {
			bluffFreq = bf
		}
	}

	reasoning := buildReasoning(reasoningInput{
		risk:           in.Risk,
		action:         primaryAct,
		winEq:          winEq,
		callEquity:     callEquity,
		callRangeFrac:  callRangeFraction,
		potOdds:        potOdds,
		toCall:         toCall,
		pot:            pot,
		evCall:         evCall,
		opponents:      opponents,
		effectiveStack: effectiveStack,
		hasReads:       hasReads,
		bluffFreq:      bluffFreq,
		fromChart:      charted,
	})

	return AdvisorResponse{
		HandID:            state.HandID,
		HeroCards:         [2]string{state.HeroCards[0].String(), state.HeroCards[1].String()},
		Equity:            winEq,
		PotOdds:           potOdds,
		Actions:           actions,
		PrimaryAction:     primaryAct.Action,
		RecommendedAmount: primaryAct.Amount,
		Reasoning:         reasoning,
		TableKnowledge:    roundToTwoDecimals(knowledge),
		Phase:             phaseName(knowledge),
		EffectiveStack:    roundToTwoDecimals(effectiveStack),
		Opponents:         opponents,
		HasReads:          hasReads,
		CallRangeFraction: callRangeFraction,
		CallEquity:        callEquity,
		Risk:              in.Risk,
	}
}

// foldSampleSize is how many times the fold frequency now in use was actually
// observed. Keyed the same way observedFoldRate keys its lookup, with "_n"
// appended, because the count belongs to the frequency and not to the player.
func foldSampleSize(street table.Street, t map[string]float64, heroRaising bool) (int, bool) {
	var keys []string
	switch {
	case heroRaising && street == table.StreetPreflop:
		keys = []string{"fold_to_3bet_n"}
	case heroRaising:
		keys = []string{"fold_to_raise_post_n"}
	case street == table.StreetPreflop:
		keys = []string{"fold_to_raise_n", "fold_to_3bet_n"}
	case street == table.StreetFlop:
		keys = []string{"fold_to_cbet_n", "fold_to_bet_n"}
	default:
		keys = []string{"fold_to_bet_n", "fold_to_cbet_n"}
	}
	for _, k := range keys {
		if v, ok := t[k]; ok && v > 0 {
			return int(v), true
		}
	}
	return 0, false
}

// bluffWorthwhileFold is how often an opponent has to fold before bluffing them
// is worth doing at all: the equilibrium continuation frequency against a
// pot-sized bet. Below it they are defending correctly or better, and a bluff
// is buying nothing.
const bluffWorthwhileFold = 0.50

// darkHorseCaution is the share of a full commitment set aside for not knowing
// who is on the other side of it.
//
// Stated, not derived, and the shape matters more than the number: at a shove
// against a table of strangers it withholds a fifth of the stack in modelled
// value, at a third-pot bet it withholds a fifth of one per cent, and at a
// table that is fully read it withholds nothing at all.
const darkHorseCaution = 0.20

// tableKnowledge is how well the least-known live opponent is understood.
//
// The minimum and not the average, because that is what being at a table with
// one stranger is actually like: whatever is known about the other four, the
// pot can still end up heads-up against the person nobody has a line on. It is
// the same reasoning that closes the bluffing branch unless every opponent has
// been counted.
func tableKnowledge(models []oppModel) float64 {
	if len(models) == 0 {
		return 0
	}
	least := 1.0
	for _, m := range models {
		// The same shrinkage readWeight uses, normalised so that a fully
		// counted opponent reads as one rather than as the weight cap.
		k := readWeight(m.hands, false) / maxMeasuredWeight
		if k > 1 {
			k = 1
		}
		if k < least {
			least = k
		}
	}
	return least
}

// phaseName is table knowledge in words, for the interface.
func phaseName(k float64) string {
	switch {
	case k < 0.34:
		return "разведка"
	case k < 0.75:
		return "применение"
	default:
		return "давление"
	}
}

// trapBetSize is the bet a checked-to opponent is assumed to make, as a
// fraction of the pot. Stated, not derived: two thirds is the standard size
// across every population anybody has measured, and the branch is not sensitive
// to it -- what decides whether trapping is right is how *often* they bet, not
// how much.
const trapBetSize = 0.66

// checkedToBetFrequency is how likely it is that somebody bets if hero checks.
//
// Two numbers come back and they are different questions. pAny is the chance
// that any live opponent bets, which is what weights the branch; fBettor is the
// betting frequency of the player most likely to be doing it, which is how wide
// the range hero would be facing. In a heads-up pot they are the same.
//
// Opponents with no counted read contribute nothing to either. That makes the
// estimate conservative -- the trap is undervalued at a table of strangers,
// which is the right direction to be wrong in.
func checkedToBetFrequency(in Inputs, state table.HandState) (pAny, fBettor, weight float64, ok bool) {
	if len(in.Opponents) == 0 {
		return 0, 0, 0, false
	}
	live := map[string]bool{}
	for _, seat := range state.Seats {
		if seat.PlayerID != state.HeroID && !seat.IsFolded && seat.IsActive {
			live[seat.PlayerID] = true
		}
	}

	key, countKey := "bet_freq_late", "bet_freq_late_n"
	if state.Street == table.StreetFlop {
		key, countKey = "bet_freq_flop", "bet_freq_flop_n"
	}

	noneBets := 1.0
	found := false
	for _, o := range in.Opponents {
		if !live[o.PlayerID] {
			continue
		}
		f, has := o.Tendencies[key]
		if !has || f < 0 || f > 1 {
			continue
		}
		n := 0
		if v, okN := o.Tendencies[countKey]; okN {
			n = int(v)
		}
		modelled := o.Modelled
		if v, okM := o.Tendencies["modelled"]; okM && v > 0 {
			modelled = true
		}
		w := readWeight(n, modelled)
		if w <= 0 {
			continue
		}
		found = true
		noneBets *= 1 - f
		if f > fBettor {
			fBettor = f
			weight = w
		}
	}
	if !found {
		return 0, 0, 0, false
	}
	return 1 - noneBets, fBettor, weight, true
}

// bettorBetFrequency is how often the player who just bet takes that action
// when nothing is owed, and how far the model may move towards it.
//
// The bettor is the live opponent whose chips on the felt match the bet hero is
// facing. When several match -- a bet and a call of it -- the last one to have
// acted aggressively is the one whose range is being priced, so the badge
// decides it.
func bettorBetFrequency(in Inputs, state table.HandState) (freq, weight float64, ok bool) {
	if len(in.Opponents) == 0 {
		return 0, 0, false
	}
	bettor := ""
	for _, seat := range state.Seats {
		if seat.PlayerID == state.HeroID || seat.IsFolded {
			continue
		}
		if seat.CurrentBet+1e-9 < state.CurrentBet {
			continue
		}
		switch seat.LastAction {
		case "bet", "raise", "all-in":
			bettor = seat.PlayerID
		}
	}
	if bettor == "" {
		return 0, 0, false
	}

	key, countKey := "bet_freq_late", "bet_freq_late_n"
	if state.Street == table.StreetFlop {
		key, countKey = "bet_freq_flop", "bet_freq_flop_n"
	}
	for _, o := range in.Opponents {
		if o.PlayerID != bettor {
			continue
		}
		f, has := o.Tendencies[key]
		if !has || f < 0 || f > 1 {
			return 0, 0, false
		}
		n := 0
		if v, okN := o.Tendencies[countKey]; okN {
			n = int(v)
		}
		modelled := o.Modelled
		if v, okM := o.Tendencies["modelled"]; okM && v > 0 {
			modelled = true
		}
		w := readWeight(n, modelled)
		if w <= 0 {
			return 0, 0, false
		}
		return f, w, true
	}
	return 0, 0, false
}

// oppModel is one live opponent as the fold-equity model sees them.
type oppModel struct {
	observed float64
	weight   float64
	stack    float64
	counted  bool
	// hands is how many hands this opponent has been watched for, whatever
	// statistics happen to have come out of them.
	//
	// It is deliberately not the same as weight. Weight says how far a
	// particular fold frequency may move the estimate and is zero when that
	// frequency was never counted; hands says how much of a stranger this
	// person still is, which is a different question and the one that decides
	// how carefully to play. Taking the first for the second put the tool
	// permanently in reconnaissance whenever fold frequencies were not being
	// collected -- which is the ordinary case.
	hands int
}

// opponentModels builds one model per live opponent.
//
// With Inputs.Opponents supplied, each is read on its own terms. Without it the
// single OppTendencies read is applied to every seat alike and the deepest
// opponent's stack stands in for all of them, which is what the code did
// before this existed and is kept so that the older entry points behave as they
// always have.
func opponentModels(in Inputs, state table.HandState, opponents int, deepest float64, heroRaising bool) []oppModel {
	if len(in.Opponents) > 0 {
		out := make([]oppModel, 0, len(in.Opponents))
		for _, o := range in.Opponents {
			m := oppModel{stack: o.Stack, hands: o.Hands}
			if m.stack <= 0 {
				m.stack = deepest
			}
			if n, ok := o.Tendencies["hands_count"]; ok && n > float64(m.hands) {
				m.hands = int(n)
			}
			observed, has := observedFoldRate(state.Street, o.Tendencies, heroRaising)
			if has {
				hands := o.Hands
				modelled := o.Modelled
				// A tendency the language model supplied is an opinion, and it
				// is marked as one whether or not the caller said so.
				if v, ok := o.Tendencies["modelled"]; ok && v > 0 {
					modelled = true
				}
				// The sample that matters is how many times this player was
				// actually bet at, not how many hands they were dealt. A
				// regular can sit for two hundred hands and have faced four
				// raises, and weighting the read by two hundred says the four
				// are a fact.
				if n, ok := foldSampleSize(state.Street, o.Tendencies, heroRaising); ok {
					hands = n
				} else if n, ok := o.Tendencies["hands_count"]; ok && hands == 0 && n > 0 {
					hands = int(n)
				}
				if w := readWeight(hands, modelled); w > 0 {
					m.observed = observed
					m.weight = w
					m.counted = !modelled && hands > 0
				}
			}
			out = append(out, m)
		}
		return out
	}

	observed, has := observedFoldRate(state.Street, in.OppTendencies, heroRaising)
	weight := 0.0
	if has {
		weight = readWeight(in.ReadHands, in.ReadModelled)
	}
	counted := weight > 0 && !in.ReadModelled && in.ReadHands > 0
	if weight <= 0 {
		observed = 0
	}
	hands := in.ReadHands
	if n, ok := in.OppTendencies["hands_count"]; ok && n > float64(hands) {
		hands = int(n)
	}
	out := make([]oppModel, opponents)
	for i := range out {
		out[i] = oppModel{observed: observed, weight: weight, stack: deepest, counted: counted, hands: hands}
	}
	return out
}

// chartRaiseAmount is the size the preflop chart means by "raise", as chips
// added now.
//
// These are the standard shapes at a hundred big blinds and they are stated,
// not derived: an open of two and a half blinds with one more for every limper
// already in, a three-bet of three times what is owed, a four-bet of a little
// over two. The chart is a hundred-big-blind chart and these are the sizes it
// assumes; a model that could work them out for itself would need to see the
// streets they are played on.
func chartRaiseAmount(state table.HandState, toCall, heroCurrentBet float64) float64 {
	switch preflop.SituationOf(state) {
	case preflop.FacingRaise:
		return 3.0 * toCall
	case preflop.FacingThreeBet:
		return 2.2 * toCall
	default:
		// Nobody has raised, so whatever is out there is the big blind.
		bb := state.CurrentBet
		if bb <= 0 {
			bb = 1
		}
		limpers := 0.0
		for _, seat := range state.Seats {
			if seat.PlayerID != state.HeroID && !seat.IsFolded && seat.LastAction == "call" {
				limpers++
			}
		}
		want := (2.5+limpers)*bb - heroCurrentBet
		if want < toCall {
			want = toCall
		}
		return want
	}
}

// nearestRaise is the aggressive option closest to an amount, which is how a
// wanted size is turned into one of the sizes actually on offer -- the list is
// capped by the effective stack and de-duplicated, so the amount asked for is
// not always among them.
func nearestRaise(actions []ActionRecommendation, amount float64) (int, bool) {
	best, bestGap := -1, math.Inf(1)
	for i, a := range actions {
		switch a.Action {
		case table.ActionRaise, table.ActionBet, table.ActionAllIn:
			if gap := math.Abs(a.Amount - amount); gap < bestGap {
				best, bestGap = i, gap
			}
		}
	}
	return best, best >= 0
}

// chartedAction consults the preflop charts. They answer only when the street
// is preflop, hero's seat and position were read, and hero's cards are known.
func chartedAction(state table.HandState) (preflop.Action, bool) {
	if state.Street != table.StreetPreflop {
		return "", false
	}
	if state.HeroCards[0].Rank == 0 || state.HeroCards[1].Rank == 0 {
		return "", false
	}
	position, ok := preflop.HeroPosition(state)
	if !ok {
		return "", false
	}
	return preflop.Recommend(position, preflop.SituationOf(state), state.HeroCards)
}

// matchChartAction finds the option that carries out the chart's instruction.
// A chart says raise, call or fold; which sizing to raise is still an EV
// question, so the highest-EV aggressive option is taken.
func matchChartAction(actions []ActionRecommendation, want preflop.Action, toCall float64) (int, bool) {
	switch want {
	case preflop.Fold:
		if toCall <= 0 {
			// Nothing is owed, so folding is not on offer; check instead.
			for i, a := range actions {
				if a.Action == table.ActionCheck {
					return i, true
				}
			}
			return 0, false
		}
		for i, a := range actions {
			if a.Action == table.ActionFold {
				return i, true
			}
		}
	case preflop.Call:
		if toCall <= 0 {
			for i, a := range actions {
				if a.Action == table.ActionCheck {
					return i, true
				}
			}
			return 0, false
		}
		for i, a := range actions {
			if a.Action == table.ActionCall {
				return i, true
			}
		}
	case preflop.Raise:
		best := -1
		for i, a := range actions {
			switch a.Action {
			case table.ActionRaise, table.ActionBet, table.ActionAllIn:
				if best < 0 || a.EV > actions[best].EV {
					best = i
				}
			}
		}
		if best >= 0 {
			return best, true
		}
	}
	return 0, false
}

type reasoningInput struct {
	action         ActionRecommendation
	winEq          float64
	callEquity     float64
	callRangeFrac  float64
	potOdds        float64
	toCall         float64
	pot            float64
	evCall         float64
	opponents      int
	effectiveStack float64
	hasReads       bool
	bluffFreq      float64
	fromChart      bool
	risk           *equity.RiskProfile
}

// chartReasoning explains a preflop decision in the terms that actually made
// it: the chart for hero's position and the situation in front of them.
//
// It deliberately reports no EV and no equity figure. Preflop, equity to
// showdown is not what the decision rests on -- comparing it with pot odds
// prices a call as though hero were about to turn their cards over, which is
// what folded 3h3c getting 37 to 1 and AcKc from the blinds. Quoting those
// numbers beside a chart decision invites exactly the mistake the chart exists
// to prevent.
func chartReasoning(in reasoningInput, way string, actionRU map[table.ActionType]string) string {
	switch in.action.Action {
	case table.ActionFold:
		return fmt.Sprintf("Фолд по чарту: рука не входит в диапазон этой позиции. %s, цена входа %.0f в банк %.0f.",
			way, in.toCall, in.pot)
	case table.ActionCall:
		return fmt.Sprintf("Колл %.0f по чарту: рука в колл-диапазоне этой позиции. %s, банк %.0f.",
			in.toCall, way, in.pot)
	case table.ActionCheck:
		return fmt.Sprintf("Чек по чарту: рука не в диапазоне повышения, а платить нечего. %s, банк %.0f.",
			way, in.pot)
	default:
		name := actionRU[in.action.Action]
		if name == "" {
			name = string(in.action.Action)
		}
		return fmt.Sprintf("%s %.0f (%s) по чарту: рука в диапазоне повышения для этой позиции. %s, банк %.0f, эффективный стек %.0f.",
			strings.ToUpper(name[:1])+name[1:], in.action.Amount, in.action.SizingLabel,
			way, in.pot, in.effectiveStack)
	}
}

// buildReasoning writes the explanation shown in the HUD. It is in Russian
// because that is the language the operator reads it in under time pressure;
// everything else in this package stays in English.
func buildReasoning(in reasoningInput) string {
	way := "хедз-ап"
	if in.opponents > 1 {
		way = fmt.Sprintf("%d-вей", in.opponents+1)
	}

	actionRU := map[table.ActionType]string{
		table.ActionBet:   "ставка",
		table.ActionRaise: "рейз",
		table.ActionAllIn: "олл-ин",
	}

	// A decision the chart made has to be explained by the chart.
	//
	// Live, a chart raise with KQo six-way was reported as "Полублеф-рейз 4000,
	// фолд-эквити 0% при эквити руки 11.9%, EV -2355", with a line about the
	// chart appended after it. Every number in that sentence comes from the EV
	// model, which did not make the decision and is known to be wrong preflop --
	// being wrong preflop is why the charts exist. So the explanation told the
	// operator that the recommended action loses money, and did it under a
	// heading that was itself wrong. The raise is correct; only the account of
	// it was broken.
	if in.fromChart {
		return chartReasoning(in, way, actionRU)
	}

	// Value or semi-bluff is a question about whether the hand is ahead, and
	// ahead of a field of five is not the same as ahead of one. Measured
	// against a flat 0.50 every multiway preflop raise came out a semi-bluff,
	// because six-way all-in equity is around 12% for anything -- aces are
	// about 35%. The fair share of the pot is the honest comparison, and it is
	// exactly 0.50 heads-up, which is where the constant came from.
	fairShare := 1.0 / float64(in.opponents+1)

	var body string
	switch in.action.Action {
	case table.ActionBet, table.ActionRaise, table.ActionAllIn:
		name := actionRU[in.action.Action]
		if in.winEq >= fairShare {
			body = fmt.Sprintf("%s %.0f — вы впереди: выигрываете %.0f из 100 раз (%s). Соперник сбросит примерно в %.0f случаях из 100; в среднем эта ставка приносит %+.0f.",
				capitalise(name), in.action.Amount, in.winEq*100, way,
				in.action.FoldEquity*100, in.action.EV)
		} else {
			body = fmt.Sprintf("%s %.0f без готовой руки: выигрываете только %.0f из 100 раз (%s), но соперник сбросит примерно в %.0f. За счёт этого в среднем %+.0f.",
				capitalise(name), in.action.Amount, in.winEq*100, way,
				in.action.FoldEquity*100, in.action.EV)
		}
	case table.ActionCall:
		body = fmt.Sprintf("Коллируйте %.0f (%s). Против того, чем он мог сюда поставить, вы выигрываете %.0f из 100 раз, а чтобы колл окупился нужно %.0f. В среднем %+.0f.",
			in.toCall, way, in.callEquity*100, in.potOdds*100, in.action.EV)
		if in.bluffFreq >= 0.30 {
			body += fmt.Sprintf(" Этот игрок блефует часто — примерно в %.0f случаях из 100.", in.bluffFreq*100)
		}
	case table.ActionCheck:
		body = fmt.Sprintf("Чек (%s). Выигрываете %.0f из 100 раз, но ставка себя не окупает: платить будут в основном руки, которые вас уже бьют.",
			way, in.winEq*100)
	default:
		body = fmt.Sprintf("Сбрасывайте (%s). Против того, чем он поставил, вы выигрываете %.0f из 100 раз, а чтобы колл окупился нужно %.0f — колл в среднем даёт %+.0f.",
			way, in.callEquity*100, in.potOdds*100, in.evCall)
	}

	if danger := dangerLine(in.risk, in.opponents); danger != "" {
		body += " " + danger
	}

	if !in.hasReads {
		body += " По этим соперникам ещё нет статистики, так что «сбросит / не сбросит» — это оценка по теории, а не по тому, как они играли."
	}
	return body
}

func capitalise(s string) string {
	if s == "" {
		return s
	}
	r := []rune(s)
	return strings.ToUpper(string(r[0])) + string(r[1:])
}

// dangerLine says what is beating hero and how often, in the words a player
// would use.
//
// Equity is one number, and one number hides the shape of the losses. Kings on
// 9-9-7-5-Q win 88 hands in 100 and are drawing dead against the queens; a
// player looking at that board wants to be told that a full house is live, not
// to infer it from a percentage. The counts behind this are exact -- every
// combination in the opponent's range is played against the board -- so the
// line can name hands and numbers without hedging.
func dangerLine(r *equity.RiskProfile, opponents int) string {
	if r == nil || r.Combos == 0 || len(r.BeatenBy) == 0 {
		return ""
	}
	// Below a percent of the range there is nothing worth interrupting for.
	if r.Behind < 0.01 {
		return ""
	}

	names := map[string]string{
		"High Card":       "старшей картой",
		"One Pair":        "парой",
		"Two Pair":        "двумя парами",
		"Three of a Kind": "тройкой",
		"Straight":        "стритом",
		"Flush":           "флешем",
		"Full House":      "фулл-хаусом",
		"Four of a Kind":  "каре",
		"Straight Flush":  "стрит-флешем",
	}

	var parts []string
	for _, c := range r.BeatenBy {
		if len(parts) == 2 {
			break
		}
		name := names[c.Category]
		if name == "" {
			name = c.Category
		}
		// Beaten by the same hand you hold means beaten by the kicker, and
		// saying "трипс" against a player who also holds trips reads as though
		// something rarer is required. Live, hero held trip aces with a seven
		// and the twenty combinations listed as "трипс" were every ace with a
		// better card beside it -- which is the whole danger of the hand and is
		// not what the category name conveys.
		if c.Category == r.HeroHand {
			name += " со старшим киккером"
		}
		parts = append(parts, fmt.Sprintf("%s — %.0f из 100", name, c.Share*100))
	}

	who := "соперник уже сильнее"
	if opponents > 1 {
		// The count is against one range, so multiway it is a floor and has to
		// be said as one. Claiming it for the whole field would be arithmetic
		// nobody did.
		who = "любой отдельный соперник уже сильнее"
	}
	return fmt.Sprintf("Осторожно: у вас %s, и в %.0f случаях из 100 %s (%s).",
		heroHandRU(r.HeroHand), r.Behind*100, who, strings.Join(parts, ", "))
}

func heroHandRU(s string) string {
	switch s {
	case "High Card":
		return "старшая карта"
	case "One Pair":
		return "пара"
	case "Two Pair":
		return "две пары"
	case "Three of a Kind":
		return "тройка"
	case "Straight":
		return "стрит"
	case "Flush":
		return "флеш"
	case "Full House":
		return "фулл-хаус"
	case "Four of a Kind":
		return "каре"
	case "Straight Flush":
		return "стрит-флеш"
	}
	return s
}
