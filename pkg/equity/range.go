package equity

import (
	"fmt"
	"math/rand"
	"sort"
	"strconv"
	"strings"

	"poker-game-analyzer/pkg/table"
)

type Range struct {
	Combos [][2]table.Card
	masks  []uint64
}

func (r *Range) initMasks() {
	if len(r.masks) == len(r.Combos) {
		return
	}
	r.masks = make([]uint64, len(r.Combos))
	for i, c := range r.Combos {
		r.masks[i] = ComboToMask(c)
	}
}

var all52Cards = generateDeck()

func generateDeck() []table.Card {
	deck := make([]table.Card, 0, 52)
	for r := table.RankTwo; r <= table.RankAce; r++ {
		for s := table.Spades; s <= table.Clubs; s++ {
			deck = append(deck, table.Card{Rank: r, Suit: s})
		}
	}
	return deck
}

func CardToIndex(c table.Card) int {
	return int(c.Rank-2)*4 + int(c.Suit)
}

func IndexToCard(idx int) table.Card {
	return table.Card{
		Rank: table.Rank(idx/4 + 2),
		Suit: table.Suit(idx % 4),
	}
}

func CardToMask(c table.Card) uint64 {
	idx := CardToIndex(c)
	if idx < 0 || idx >= 52 {
		return 0
	}
	return uint64(1) << idx
}

func ComboToMask(c [2]table.Card) uint64 {
	return CardToMask(c[0]) | CardToMask(c[1])
}

// 169 starting hands in standard preflop power ranking
var preflopHandRankings = []string{
	"AA", "KK", "QQ", "AKs", "JJ", "AQs", "KQs", "AJs", "AKo", "TT",
	"ATs", "AQo", "KJs", "QJs", "JTs", "99", "KTs", "QTs", "A9s", "AJo",
	"88", "KJo", "A8s", "A5s", "A4s", "A3s", "A2s", "A7s", "A6s", "QJo",
	"77", "J9s", "T9s", "ATo", "K9s", "66", "KQo", "98s", "87s", "Q9s",
	"55", "44", "76s", "K8s", "J8s", "33", "22", "65s", "T8s", "K7s",
	"97s", "86s", "54s", "Q8s", "K6s", "K5s", "K4s", "K3s", "K2s", "75s",
	"64s", "53s", "A9o", "43s", "J7s", "T7s", "Q7s", "KTo", "QTo", "JTo",
	"96s", "85s", "74s", "63s", "52s", "42s", "32s", "Q6s", "Q5s", "Q4s",
	"Q3s", "Q2s", "J6s", "J5s", "J4s", "J3s", "J2s", "T6s", "T5s", "T4s",
	"T3s", "T2s", "A8o", "A7o", "A6o", "A5o", "A4o", "A3o", "A2o", "95s",
	"94s", "93s", "92s", "84s", "83s", "82s", "73s", "72s", "62s", "K9o",
	"Q9o", "J9o", "T9o", "K8o", "K7o", "K6o", "K5o", "K4o", "K3o", "K2o",
	"Q8o", "Q7o", "Q6o", "Q5o", "Q4o", "Q3o", "Q2o", "J8o", "J7o", "J6o",
	"J5o", "J4o", "J3o", "J2o", "98o", "87o", "76o", "65o", "54o", "T8o",
	"97o", "86o", "75o", "64o", "53o", "43o", "T7o", "96o", "85o", "74o",
	"63o", "52o", "42o", "32o", "T6o", "95o", "84o", "73o", "62o", "T5o",
	"94o", "83o", "72o", "T4o", "93o", "82o", "T3o", "92o", "T2o",
}

func parseRankChar(c byte) (table.Rank, bool) {
	switch c {
	case 'A', 'a':
		return table.RankAce, true
	case 'K', 'k':
		return table.RankKing, true
	case 'Q', 'q':
		return table.RankQueen, true
	case 'J', 'j':
		return table.RankJack, true
	case 'T', 't':
		return table.RankTen, true
	case '9':
		return table.RankNine, true
	case '8':
		return table.RankEight, true
	case '7':
		return table.RankSeven, true
	case '6':
		return table.RankSix, true
	case '5':
		return table.RankFive, true
	case '4':
		return table.RankFour, true
	case '3':
		return table.RankThree, true
	case '2':
		return table.RankTwo, true
	default:
		return 0, false
	}
}

func makePairCombos(r table.Rank) [][2]table.Card {
	suits := []table.Suit{table.Spades, table.Hearts, table.Diamonds, table.Clubs}
	var res [][2]table.Card
	for i := 0; i < len(suits); i++ {
		for j := i + 1; j < len(suits); j++ {
			res = append(res, [2]table.Card{
				{Rank: r, Suit: suits[i]},
				{Rank: r, Suit: suits[j]},
			})
		}
	}
	return res
}

func makeSuitedCombos(r1, r2 table.Rank) [][2]table.Card {
	suits := []table.Suit{table.Spades, table.Hearts, table.Diamonds, table.Clubs}
	var res [][2]table.Card
	for _, s := range suits {
		res = append(res, [2]table.Card{
			{Rank: r1, Suit: s},
			{Rank: r2, Suit: s},
		})
	}
	return res
}

func makeOffsuitCombos(r1, r2 table.Rank) [][2]table.Card {
	suits := []table.Suit{table.Spades, table.Hearts, table.Diamonds, table.Clubs}
	var res [][2]table.Card
	for _, s1 := range suits {
		for _, s2 := range suits {
			if s1 != s2 {
				res = append(res, [2]table.Card{
					{Rank: r1, Suit: s1},
					{Rank: r2, Suit: s2},
				})
			}
		}
	}
	return res
}

func makeAllCombos() [][2]table.Card {
	var combos [][2]table.Card
	for i := 0; i < len(all52Cards); i++ {
		for j := i + 1; j < len(all52Cards); j++ {
			combos = append(combos, [2]table.Card{all52Cards[i], all52Cards[j]})
		}
	}
	return combos
}

func parseSingleToken(tok string) [][2]table.Card {
	tok = strings.TrimSpace(tok)
	if tok == "" {
		return nil
	}
	lower := strings.ToLower(tok)

	// Presets: random / any / 100%
	if lower == "random" || lower == "any" || lower == "all" || lower == "100%" || lower == "100" {
		return makeAllCombos()
	}

	// Presets: topX or topX%
	if strings.HasPrefix(lower, "top") {
		numStr := strings.TrimPrefix(lower, "top")
		numStr = strings.TrimSuffix(numStr, "%")
		if pct, err := strconv.ParseFloat(numStr, 64); err == nil && pct > 0 {
			targetCombos := int(1326.0 * (pct / 100.0))
			if targetCombos < 1 {
				targetCombos = 1
			}
			if targetCombos > 1326 {
				targetCombos = 1326
			}
			var res [][2]table.Card
			seen := make(map[uint64]bool)
			for _, handStr := range preflopHandRankings {
				combos := parseSingleToken(handStr)
				for _, c := range combos {
					m := ComboToMask(c)
					if !seen[m] {
						seen[m] = true
						res = append(res, c)
						if len(res) >= targetCombos {
							return res
						}
					}
				}
			}
			return res
		}
	}

	// Check if this is a 2-card specific holding (e.g. "AhKd", "AsKs", "10sJh", "TsJh")
	if len(tok) >= 4 {
		// Attempt parsing as two distinct cards
		var c1, c2 table.Card
		var err1, err2 error
		if strings.HasPrefix(tok, "10") {
			c1, err1 = table.ParseCard(tok[:3])
			c2, err2 = table.ParseCard(tok[3:])
		} else if len(tok) >= 5 && tok[2:4] == "10" {
			c1, err1 = table.ParseCard(tok[:2])
			c2, err2 = table.ParseCard(tok[2:])
		} else if len(tok) == 4 {
			c1, err1 = table.ParseCard(tok[:2])
			c2, err2 = table.ParseCard(tok[2:])
		}
		if err1 == nil && err2 == nil && c1 != c2 && c1.Rank > 0 && c2.Rank > 0 {
			return [][2]table.Card{{c1, c2}}
		}
	}

	// Pair range: e.g. "88-TT", "22-66"
	if strings.Contains(tok, "-") {
		parts := strings.Split(tok, "-")
		if len(parts) == 2 {
			p1 := strings.TrimSpace(parts[0])
			p2 := strings.TrimSpace(parts[1])
			if len(p1) == 2 && len(p2) == 2 && p1[0] == p1[1] && p2[0] == p2[1] {
				r1, ok1 := parseRankChar(p1[0])
				r2, ok2 := parseRankChar(p2[0])
				if ok1 && ok2 {
					minR := r1
					maxR := r2
					if minR > maxR {
						minR, maxR = maxR, minR
					}
					var res [][2]table.Card
					for r := minR; r <= maxR; r++ {
						res = append(res, makePairCombos(r)...)
					}
					return res
				}
			}
		}
	}

	// Kicker range under a fixed top rank: e.g. "K2s-K8s", "A2o-A8o". The two
	// ends share their high card and differ only in the kicker.
	if strings.Contains(tok, "-") {
		parts := strings.SplitN(tok, "-", 2)
		lo := strings.TrimSpace(parts[0])
		hi := strings.TrimSpace(parts[1])
		if len(lo) == 3 && len(hi) == 3 &&
			strings.EqualFold(string(lo[0]), string(hi[0])) &&
			strings.EqualFold(string(lo[2]), string(hi[2])) {
			modifier := strings.ToLower(string(lo[2]))
			top, ok1 := parseRankChar(lo[0])
			k1, ok2 := parseRankChar(lo[1])
			k2, ok3 := parseRankChar(hi[1])
			if ok1 && ok2 && ok3 && (modifier == "s" || modifier == "o") {
				lowK, highK := k1, k2
				if lowK > highK {
					lowK, highK = highK, lowK
				}
				if highK < top {
					var res [][2]table.Card
					for k := lowK; k <= highK; k++ {
						if modifier == "s" {
							res = append(res, makeSuitedCombos(top, k)...)
						} else {
							res = append(res, makeOffsuitCombos(top, k)...)
						}
					}
					return res
				}
			}
		}
	}

	// Same-gap range walking down: e.g. "T9s-54s", "AQo-JTo". Both ends must
	// share the gap between their ranks, and the walk goes one rank at a time.
	if strings.Contains(tok, "-") {
		parts := strings.SplitN(tok, "-", 2)
		hi := strings.TrimSpace(parts[0])
		lo := strings.TrimSpace(parts[1])
		if len(hi) == 3 && len(lo) == 3 &&
			strings.EqualFold(string(hi[2]), string(lo[2])) {
			modifier := strings.ToLower(string(hi[2]))
			h1, ok1 := parseRankChar(hi[0])
			h2, ok2 := parseRankChar(hi[1])
			l1, ok3 := parseRankChar(lo[0])
			l2, ok4 := parseRankChar(lo[1])
			if ok1 && ok2 && ok3 && ok4 && h1 > h2 && l1 > l2 &&
				h1-h2 == l1-l2 && h1 >= l1 && (modifier == "s" || modifier == "o") {
				var res [][2]table.Card
				for top, bottom := h1, h2; top >= l1; top, bottom = top-1, bottom-1 {
					if modifier == "s" {
						res = append(res, makeSuitedCombos(top, bottom)...)
					} else {
						res = append(res, makeOffsuitCombos(top, bottom)...)
					}
				}
				return res
			}
		}
	}

	// Pair plus: e.g. "TT+", "88+", "22+"
	if len(tok) == 3 && tok[0] == tok[1] && tok[2] == '+' {
		r, ok := parseRankChar(tok[0])
		if ok {
			var res [][2]table.Card
			for rank := r; rank <= table.RankAce; rank++ {
				res = append(res, makePairCombos(rank)...)
			}
			return res
		}
	}

	// Exact pair: e.g. "AA", "KK", "22"
	if len(tok) == 2 && tok[0] == tok[1] {
		r, ok := parseRankChar(tok[0])
		if ok {
			return makePairCombos(r)
		}
	}

	// Suited / offsuit with plus: e.g. "AJs+", "AQo+", "AQ+", "AK+"
	if strings.HasSuffix(tok, "+") {
		base := strings.TrimSuffix(tok, "+")
		if len(base) == 3 {
			r1, ok1 := parseRankChar(base[0])
			r2, ok2 := parseRankChar(base[1])
			modifier := strings.ToLower(string(base[2]))
			if ok1 && ok2 && r1 > r2 {
				var res [][2]table.Card
				for kicker := r2; kicker < r1; kicker++ {
					if modifier == "s" {
						res = append(res, makeSuitedCombos(r1, kicker)...)
					} else if modifier == "o" {
						res = append(res, makeOffsuitCombos(r1, kicker)...)
					}
				}
				return res
			}
		} else if len(base) == 2 {
			r1, ok1 := parseRankChar(base[0])
			r2, ok2 := parseRankChar(base[1])
			if ok1 && ok2 && r1 > r2 {
				var res [][2]table.Card
				for kicker := r2; kicker < r1; kicker++ {
					res = append(res, makeSuitedCombos(r1, kicker)...)
					res = append(res, makeOffsuitCombos(r1, kicker)...)
				}
				return res
			}
		}
	}

	// Exact non-pair: e.g. "AKs", "AKo", "AK"
	if len(tok) == 3 {
		r1, ok1 := parseRankChar(tok[0])
		r2, ok2 := parseRankChar(tok[1])
		modifier := strings.ToLower(string(tok[2]))
		if ok1 && ok2 {
			if r1 < r2 {
				r1, r2 = r2, r1
			}
			if modifier == "s" {
				return makeSuitedCombos(r1, r2)
			} else if modifier == "o" {
				return makeOffsuitCombos(r1, r2)
			}
		}
	}

	if len(tok) == 2 {
		r1, ok1 := parseRankChar(tok[0])
		r2, ok2 := parseRankChar(tok[1])
		if ok1 && ok2 {
			if r1 < r2 {
				r1, r2 = r2, r1
			}
			var res [][2]table.Card
			res = append(res, makeSuitedCombos(r1, r2)...)
			res = append(res, makeOffsuitCombos(r1, r2)...)
			return res
		}
	}

	return nil
}

// ParseRangeStrict is ParseRange with the silent widening removed. ParseRange
// answers an unrecognised range with every hand in the deck, which is a
// reasonable default for a guessed opponent range and a dangerous one for a
// chart: a typo would turn "raise these hands" into "raise everything".
func ParseRangeStrict(s string) (Range, error) {
	tokens := strings.FieldsFunc(s, func(c rune) bool {
		return c == ',' || c == ';' || c == ' ' || c == '\t' || c == '\n'
	})
	for _, tok := range tokens {
		if len(parseSingleToken(tok)) == 0 {
			return Range{}, fmt.Errorf("unrecognised range token %q in %q", tok, s)
		}
	}
	if len(tokens) == 0 {
		return Range{}, fmt.Errorf("empty range %q", s)
	}
	return ParseRange(s), nil
}

// Contains reports whether a specific holding is in the range.
func (r Range) Contains(hole [2]table.Card) bool {
	if hole[0].Rank == 0 || hole[1].Rank == 0 {
		return false
	}
	want := ComboToMask(hole)
	for _, c := range r.Combos {
		if ComboToMask(c) == want {
			return true
		}
	}
	return false
}

func ParseRange(s string) Range {
	s = strings.TrimSpace(s)
	if s == "" {
		combos := makeAllCombos()
		r := Range{Combos: combos}
		r.initMasks()
		return r
	}

	// Split by comma, semicolon, space
	separators := func(c rune) bool {
		return c == ',' || c == ';' || c == ' ' || c == '\t' || c == '\n'
	}
	tokens := strings.FieldsFunc(s, separators)

	var allCombos [][2]table.Card
	seenMasks := make(map[uint64]bool)

	for _, tok := range tokens {
		combos := parseSingleToken(tok)
		for _, c := range combos {
			m := ComboToMask(c)
			if !seenMasks[m] {
				seenMasks[m] = true
				allCombos = append(allCombos, c)
			}
		}
	}

	if len(allCombos) == 0 {
		allCombos = makeAllCombos()
	}

	// Sort combos deterministically
	sort.Slice(allCombos, func(i, j int) bool {
		m1 := ComboToMask(allCombos[i])
		m2 := ComboToMask(allCombos[j])
		return m1 < m2
	})

	r := Range{Combos: allCombos}
	r.initMasks()
	return r
}

func (r Range) SampleCombo(deadCards map[string]bool, rng *rand.Rand) ([2]table.Card, bool) {
	if len(r.Combos) == 0 {
		return [2]table.Card{}, false
	}
	var deadMask uint64
	for k, v := range deadCards {
		if v {
			if c, err := table.ParseCard(k); err == nil {
				deadMask |= CardToMask(c)
			}
		}
	}
	return r.SampleComboMask(deadMask, rng)
}

func (r Range) SampleComboMask(deadMask uint64, rng *rand.Rand) ([2]table.Card, bool) {
	n := len(r.Combos)
	if n == 0 {
		return [2]table.Card{}, false
	}
	if len(r.masks) != n {
		r.initMasks()
	}

	// Fast random probes
	for attempt := 0; attempt < 8; attempt++ {
		idx := rng.Intn(n)
		if (r.masks[idx] & deadMask) == 0 {
			return r.Combos[idx], true
		}
	}

	// Linear scan from random offset if probes miss
	start := rng.Intn(n)
	for i := 0; i < n; i++ {
		idx := (start + i) % n
		if (r.masks[idx] & deadMask) == 0 {
			return r.Combos[idx], true
		}
	}

	return [2]table.Card{}, false
}
