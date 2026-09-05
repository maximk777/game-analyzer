// Package calib marks the advisor's model of an opponent against the cards that
// opponent actually held.
//
// The model states one thing at every decision: the opponent holds the top W%
// of hands, and within that, some fraction ranked by strength on this board
// (advice.RangeWidthFor, equity.TopRange, equity.RankOnBoard). It is a claim
// about facts, so where the facts are available it can simply be marked --
// rather than inferred from whether the hand was won, which is what a win rate
// does and why a win rate needs hundreds of thousands of hands to say anything.
//
// Two sources of facts feed this package. Slumbot returns its own hole cards in
// every hand it plays, including the ones that end in a fold; and our own engine
// deals every card and knows them all. The first is an opponent nobody here
// wrote, at the wrong stake and the wrong table size. The second is the right
// game and the right stake against opponents we wrote. Neither answers alone --
// together they separate "the model's machinery is wrong" from "these charts are
// for another game", which is the whole difficulty.
package calib

import (
	"fmt"
	"io"
	"sort"
	"strings"

	"poker-game-analyzer/pkg/equity"
	"poker-game-analyzer/pkg/table"
)

// Bucket accumulates the statistics for one kind of spot.
type Bucket struct {
	Name string
	N    int

	// Outside counts hands the model said the opponent could not hold.
	Outside int
	USum    float64
	UN      int
	UHist   [10]int

	// VAll is the opponent's strength rank on the board among *every* holding.
	//
	// Taken among every holding rather than among the range the model assigned,
	// on purpose. A rank inside the assigned range is only defined when the hand
	// was inside it -- and being inside it means being strong -- so that version
	// measures shape on a sample selected for strength.
	VAllSum  float64
	VAllN    int
	VAllHist [10]int

	// Priced counts decisions where a call was being priced, and InSlice how
	// often the opponent really held one of the hands it was priced against.
	//
	// Deliberately not conditioned on the hand being inside the assigned range:
	// the model names a concrete set of combinations, and a hand outside the
	// range is simply not in that set. Requiring containment first would drop
	// exactly the misses this is meant to count.
	Priced   int
	InSlice  int
	FracSum  float64
	WidthSum float64
	// PctHist bins every opponent hand's rank among all starting hands, as a
	// percentage. A histogram rather than the values themselves because the
	// harness marks millions of decisions across parallel workers: the bins
	// bound the memory and make merging two workers' counts an addition.
	PctHist [pctBins]int
}

// pctBins is half a percentage point per bin, far finer than any read behind
// these numbers justifies.
const pctBins = 200

// NeededWidth is the width that would have held nine of their hands in ten.
func (b *Bucket) NeededWidth() float64 { return b.PctQuantile(0.9) }

// PctQuantile is the width holding the given share of their hands.
func (b *Bucket) PctQuantile(q float64) float64 {
	total := 0
	for _, c := range b.PctHist {
		total += c
	}
	if total == 0 {
		return 0
	}
	want := q * float64(total)
	seen := 0
	for i, c := range b.PctHist {
		if float64(seen+c) >= want {
			return 100 * float64(i+1) / pctBins
		}
		seen += c
	}
	return 100
}

// Merge folds another bucket's counts into this one. The two must be the same
// spot; the caller keys them by name.
func (b *Bucket) Merge(o *Bucket) {
	b.N += o.N
	b.Outside += o.Outside
	b.USum += o.USum
	b.UN += o.UN
	b.VAllSum += o.VAllSum
	b.VAllN += o.VAllN
	b.Priced += o.Priced
	b.InSlice += o.InSlice
	b.FracSum += o.FracSum
	b.WidthSum += o.WidthSum
	for i := range b.UHist {
		b.UHist[i] += o.UHist[i]
	}
	for i := range b.VAllHist {
		b.VAllHist[i] += o.VAllHist[i]
	}
	for i := range b.PctHist {
		b.PctHist[i] += o.PctHist[i]
	}
}

// AssignedWidth is the mean width the model gave them.
func (b *Bucket) AssignedWidth() float64 {
	if b.N == 0 {
		return 0
	}
	return b.WidthSum / float64(b.N)
}

// MeanU is the mean position inside the assigned width, over the hands that
// were inside it at all.
func (b *Bucket) MeanU() float64 {
	if in := b.UN - b.Outside; in > 0 {
		return b.USum / float64(in)
	}
	return 0
}

// MeanV is the mean strength rank on the board among every holding.
func (b *Bucket) MeanV() float64 {
	if b.VAllN == 0 {
		return 0
	}
	return b.VAllSum / float64(b.VAllN)
}

// OutsideShare is how often the opponent held what the model forbade.
func (b *Bucket) OutsideShare() float64 {
	if b.UN == 0 {
		return 0
	}
	return float64(b.Outside) / float64(b.UN)
}

// Set collects buckets by name.
type Set struct{ m map[string]*Bucket }

func NewSet() *Set { return &Set{m: map[string]*Bucket{}} }

func (s *Set) get(name string) *Bucket {
	b, ok := s.m[name]
	if !ok {
		b = &Bucket{Name: name}
		s.m[name] = b
	}
	return b
}

// Obs is one decision to be marked.
type Obs struct {
	// Names are the buckets this decision belongs to; a decision usually
	// belongs to several, from "all decisions" down to one spot.
	Names []string
	// Width is the share of all hands, as a percentage, the model gave the
	// opponent here.
	Width float64
	// CallFrac is the part of that range the call was priced against, and
	// Priced says whether a call was being priced at all. With nothing owed the
	// advisor reports the whole range, and counting that makes every spot look
	// priced against everything.
	CallFrac float64
	Priced   bool

	Hero    [2]table.Card
	Villain [2]table.Card
	Board   []table.Card
}

// Add marks one decision.
func (s *Set) Add(o Obs) {
	if o.Width <= 0 {
		return
	}
	pct := equity.HandPercentile(o.Villain) * 100
	u := pct / o.Width

	assigned := equity.TopRange(o.Width)
	rank := equity.RankOnBoard(o.Hero, o.Board, assigned)
	idx := comboIndex(rank.Combos, o.Villain)
	inSlice := idx >= 0 && len(rank.Combos) > 1 &&
		float64(idx)/float64(len(rank.Combos)-1) <= o.CallFrac

	all := equity.RankOnBoard(o.Hero, o.Board, equity.TopRange(100))
	allIdx := comboIndex(all.Combos, o.Villain)
	haveV := allIdx >= 0 && len(all.Combos) > 1
	vAll := 0.0
	if haveV {
		vAll = float64(allIdx) / float64(len(all.Combos)-1)
	}

	for _, name := range o.Names {
		b := s.get(name)
		b.N++
		b.WidthSum += o.Width
		b.PctHist[min(max(int(pct*pctBins/100), 0), pctBins-1)]++

		b.UN++
		if u > 1 {
			b.Outside++
		} else {
			b.USum += u
			b.UHist[min(max(int(u*10), 0), 9)]++
		}

		if haveV {
			b.VAllN++
			b.VAllSum += vAll
			b.VAllHist[min(max(int(vAll*10), 0), 9)]++
		}
		if o.Priced && o.CallFrac > 0 {
			b.Priced++
			b.FracSum += o.CallFrac
			if inSlice {
				b.InSlice++
			}
		}
	}
}

// Merge folds another set into this one, which is how parallel workers combine
// what each of them marked.
func (s *Set) Merge(o *Set) {
	for name, b := range o.m {
		s.get(name).Merge(b)
	}
}

// Buckets returns the buckets, the overall one first and the rest by size.
func (s *Set) Buckets() []*Bucket {
	out := make([]*Bucket, 0, len(s.m))
	for _, b := range s.m {
		out = append(out, b)
	}
	sort.Slice(out, func(i, j int) bool {
		if (out[i].Name == All) != (out[j].Name == All) {
			return out[i].Name == All
		}
		return out[i].N > out[j].N
	})
	return out
}

// All is the name of the bucket every decision joins.
const All = "all decisions"

func comboIndex(combos [][2]table.Card, hole [2]table.Card) int {
	want := equity.ComboToMask(hole)
	for i, c := range combos {
		if equity.ComboToMask(c) == want {
			return i
		}
	}
	return -1
}

func histLine(h [10]int, n int) string {
	if n == 0 {
		return ""
	}
	var b strings.Builder
	for _, c := range h {
		switch share := float64(c) / float64(n); {
		case share >= 0.16:
			b.WriteString("#")
		case share >= 0.12:
			b.WriteString("+")
		case share >= 0.08:
			b.WriteString("-")
		case share > 0:
			b.WriteString(".")
		default:
			b.WriteString(" ")
		}
	}
	return b.String()
}

// Render writes the report.
func Render(w io.Writer, buckets []*Bucket, minN int) {
	fmt.Fprint(w, "u -- where the opponent's real hand fell inside the width the model gave them.\n")
	fmt.Fprint(w, "Uniform, mean 0.50, if the width is right. Bins run strongest to weakest.\n")
	fmt.Fprint(w, "\"outside\" is hands the model said they could not hold: the width was too tight.\n")
	fmt.Fprint(w, "\"model\" is the width it assigned; \"needed\" is what would have held nine of\n")
	fmt.Fprint(w, "their hands in ten. The gap between those two is the whole of the width error.\n\n")
	fmt.Fprintf(w, "%-30s %6s %8s %6s %7s %7s  %s\n",
		"spot", "n", "outside", "model", "needed", "mean u", "strong<--u-->weak")
	for _, b := range buckets {
		if b.N < minN {
			continue
		}
		fmt.Fprintf(w, "%-30s %6d %7.1f%% %5.0f%% %6.0f%% %7.2f  %s\n",
			b.Name, b.N, 100*b.OutsideShare(), b.AssignedWidth(), b.NeededWidth(),
			b.MeanU(), histLine(b.UHist, b.UN-b.Outside))
	}

	fmt.Fprint(w, "\nv -- where the same hand fell by strength on the board, among every holding.\n")
	fmt.Fprint(w, "This is the shape test. `polar` says a betting range is bimodal -- strong hands\n")
	fmt.Fprint(w, "and bluffs with no middle -- and `capped` says a calling range is missing its\n")
	fmt.Fprint(w, "top. Note that before the river RankOnBoard floors a draw at about two pair,\n")
	fmt.Fprint(w, "so a flop bluff sits at the strong end here and bimodality is a river claim.\n\n")
	fmt.Fprintf(w, "%-30s %6s %7s  %s\n", "spot", "n", "mean v", "strong<--v-->weak")
	for _, b := range buckets {
		if b.VAllN < minN {
			continue
		}
		fmt.Fprintf(w, "%-30s %6d %7.2f  %s\n",
			b.Name, b.VAllN, b.MeanV(), histLine(b.VAllHist, b.VAllN))
	}

	printed := false
	for _, b := range buckets {
		if b.Priced < minN {
			continue
		}
		if !printed {
			fmt.Fprint(w, "\nWhat the calls were priced against, and what was really there:\n\n")
			printed = true
		}
		fmt.Fprintf(w, "  %-28s priced against the top %3.0f%% of their range; they really held\n",
			b.Name, 100*b.FracSum/float64(b.Priced))
		fmt.Fprintf(w, "  %-28s one of those hands %3.0f%% of the time (n=%d)\n",
			"", 100*float64(b.InSlice)/float64(b.Priced), b.Priced)
	}
}
