package sim

import (
	"hash/fnv"
	"math/rand"
	"sort"

	"poker-game-analyzer/pkg/evaluator"
	"poker-game-analyzer/pkg/table"
)

// Scoring an all-in by the equity it had rather than by the card that came.
//
// Everything a strategy decides is over by the time the last chip goes in. What
// happens after is a coin, and paying it into the measurement is paying for
// noise. The harness felt it: the paired standard error on a change worth a big
// blind or two came back at 2.5, and a change nobody can see is a change nobody
// can keep.
//
// The arithmetic that made the case. The paired difference between two
// candidates carried a standard deviation of 7.3 big blinds a hand. Every bit
// of it comes from hands where the two played differently -- where they played
// alike the difference is exactly zero and contributes nothing -- and a hand
// where they played differently ends all-in far more often than one where they
// did not. That tail is what this file removes.
//
// The engine is untouched. The hand plays out on the card that came, the stacks
// move by what was actually won, and a session's bankroll is the real one. Only
// the number the report is built from changes, which is the standard "all-in
// adjusted" accounting and is what every serious tracker shows next to the raw
// win rate.
//
// Two properties are worth stating because they are what make it legitimate:
//
//   - It is unbiased. The completions are enumerated out of the deck the runout
//     was actually drawn from -- the same forty-odd cards, folded players'
//     holdings already gone -- so the average over completions is the
//     expectation of the realised result, not an approximation of it.
//   - It is independent of the deck stream. The sampling it needs for the rare
//     five-cards-to-come case is seeded from the hand's own identity, so it
//     cannot shift what the next hand deals and two candidates still meet
//     identical cards.

// allInSamples is how many completions are drawn when there are too many to
// enumerate, which in practice means both players shoved before the flop.
// Three cards to come is 9,880 combinations off a forty-card deck and could be
// enumerated; five is 658,008 and cannot be, per hand. A thousand samples cuts
// the standard deviation of the runout by more than thirty times, which is the
// whole of what is being asked for here.
const allInSamples = 1000

// exactToCome is the largest number of cards still to come that is enumerated
// exactly. Two off a thirty-seven-card deck is 666 completions.
const exactToCome = 2

// markAllIn records the moment the betting ended with cards still to come.
//
// It is called after every betting round. The first call that finds nobody left
// to bet and more than one player still in is the all-in point, and the board
// and the undealt deck are taken as they stand -- before the engine deals the
// rest, which is what makes the remaining cards exactly the ones the runout
// came from.
func (h *hand) markAllIn() {
	if h.allInMarked || h.livePlayers() < 2 || h.canStillBet() || len(h.board) >= 5 {
		return
	}
	h.allInMarked = true
	h.allInBoard = append([]table.Card(nil), h.board...)
	h.allInDeck = h.di
}

// potLayer is one pot -- main or side -- and who may win it.
type potLayer struct {
	amount   Chips
	eligible []*seatState
}

// potLayers splits everything committed into the layers a showdown is settled
// in. A player who folded still paid into every layer below whatever they
// reached, and a short all-in caps the layer they can win.
func (h *hand) potLayers() []potLayer {
	levels := make([]Chips, 0, len(h.seats))
	for _, s := range h.seats {
		if s.total > 0 {
			levels = append(levels, s.total)
		}
	}
	sort.Slice(levels, func(i, j int) bool { return levels[i] < levels[j] })

	var out []potLayer
	var prev Chips
	for i, lv := range levels {
		if i > 0 && lv == levels[i-1] {
			continue
		}
		var amount Chips
		var eligible []*seatState
		for _, s := range h.seats {
			c := s.total
			if c > lv {
				c = lv
			}
			if c > prev {
				amount += c - prev
			}
			if !s.folded && s.total >= lv {
				eligible = append(eligible, s)
			}
		}
		prev = lv
		if amount <= 0 || len(eligible) == 0 {
			continue
		}
		out = append(out, potLayer{amount: amount, eligible: eligible})
	}
	return out
}

// adjustedNets is what everybody would have won on average, given the cards
// that were known when the last bet went in.
//
// Nil when there is nothing to adjust: the hand was decided by folding, or the
// last chip went in on the river, where there is no card left to be lucky with.
func (h *hand) adjustedNets() map[string]float64 {
	if !h.allInMarked {
		return nil
	}
	toCome := 5 - len(h.allInBoard)
	if toCome <= 0 {
		return nil
	}

	live := make([]*seatState, 0, len(h.seats))
	for _, s := range h.seats {
		if !s.folded {
			live = append(live, s)
		}
	}
	if len(live) < 2 {
		return nil
	}

	rem := h.deck[h.allInDeck:]
	if len(rem) < toCome {
		return nil
	}

	// Every completion is scored once for every live seat, and the layers then
	// read off those scores. Side pots share the runout, so evaluating per
	// layer would do the same work several times over.
	shares := make(map[*seatState]float64, len(live))
	layers := h.potLayers()

	// Per layer, the total "pot fractions" won across completions.
	layerShare := make([]map[*seatState]float64, len(layers))
	for i := range layerShare {
		layerShare[i] = make(map[*seatState]float64, len(live))
	}

	scores := make([]evaluator.HandScore, len(live))
	seven := make([]table.Card, 0, 7)

	count := 0
	onCompletion := func(completion []table.Card) {
		for i, s := range live {
			seven = seven[:0]
			seven = append(seven, s.hole[0], s.hole[1])
			seven = append(seven, h.allInBoard...)
			seven = append(seven, completion...)
			sc, _ := evaluator.Evaluate7(seven)
			scores[i] = sc
		}
		for li, layer := range layers {
			best := evaluator.HandScore(0)
			winners := 0
			for i, s := range live {
				if !layerEligible(layer, s) {
					continue
				}
				switch {
				case scores[i] > best:
					best, winners = scores[i], 1
				case scores[i] == best:
					winners++
				}
			}
			if winners == 0 {
				continue
			}
			for i, s := range live {
				if layerEligible(layer, s) && scores[i] == best {
					layerShare[li][s] += 1 / float64(winners)
				}
			}
		}
		count++
	}

	if toCome <= exactToCome {
		enumerate(rem, toCome, onCompletion)
	} else {
		sampleCompletions(rem, toCome, allInSamples, h.evRng(), onCompletion)
	}
	if count == 0 {
		return nil
	}

	for li, layer := range layers {
		// A layer only one player is eligible for is theirs outright; the
		// enumeration says so too, but stating it costs nothing and keeps the
		// uncontested side pot exact.
		if len(layer.eligible) == 1 {
			shares[layer.eligible[0]] += float64(layer.amount)
			continue
		}
		for s, w := range layerShare[li] {
			shares[s] += float64(layer.amount) * w / float64(count)
		}
	}

	out := make(map[string]float64, len(h.seats))
	for _, s := range h.seats {
		out[s.p.ID] = shares[s] - float64(s.total)
	}
	return out
}

func layerEligible(l potLayer, s *seatState) bool {
	for _, e := range l.eligible {
		if e == s {
			return true
		}
	}
	return false
}

// enumerate calls fn with every combination of k cards from deck.
func enumerate(deck []table.Card, k int, fn func([]table.Card)) {
	pick := make([]table.Card, k)
	var rec func(start, depth int)
	rec = func(start, depth int) {
		if depth == k {
			fn(pick)
			return
		}
		for i := start; i <= len(deck)-(k-depth); i++ {
			pick[depth] = deck[i]
			rec(i+1, depth+1)
		}
	}
	rec(0, 0)
}

// sampleCompletions draws n runouts of k cards from deck without replacement.
func sampleCompletions(deck []table.Card, k, n int, rng *rand.Rand, fn func([]table.Card)) {
	scratch := append([]table.Card(nil), deck...)
	pick := make([]table.Card, k)
	for i := 0; i < n; i++ {
		// A partial Fisher-Yates: only the first k positions need to be right.
		for j := 0; j < k; j++ {
			m := j + rng.Intn(len(scratch)-j)
			scratch[j], scratch[m] = scratch[m], scratch[j]
			pick[j] = scratch[j]
		}
		fn(pick)
	}
}

// evRng is randomness for the accounting, and it must not be the randomness
// that deals the cards.
//
// Taking numbers from the table's deck stream here would make how many samples
// the adjustment happened to need change what the next hand deals -- the exact
// failure the engine's separate streams exist to prevent, arriving through the
// measurement instead of through the agents. Seeding from the hand's own
// identity keeps it deterministic and keeps it out of the way.
func (h *hand) evRng() *rand.Rand {
	f := fnv.New64a()
	_, _ = f.Write([]byte(h.id))
	return rand.New(rand.NewSource(int64(f.Sum64())))
}
