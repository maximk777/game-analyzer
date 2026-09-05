package slumbot

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"poker-game-analyzer/pkg/calib"
	"poker-game-analyzer/pkg/table"
)

// Analyse reads a run log and marks every decision in it.
//
// The statistics themselves live in pkg/calib, because the same marking is done
// against our own engine, where the cards are known without asking anyone. What
// belongs here is only how a Slumbot log names its spots.
func Analyse(r io.Reader) ([]*calib.Bucket, error) {
	set := calib.NewSet()
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 1<<20), 1<<22)

	for line := 1; sc.Scan(); line++ {
		text := strings.TrimSpace(sc.Text())
		if text == "" {
			continue
		}
		var hr HandRecord
		if err := json.Unmarshal([]byte(text), &hr); err != nil {
			return nil, fmt.Errorf("line %d: %w", line, err)
		}
		bot, err := table.ParseCards(hr.Bot)
		if err != nil || len(bot) != 2 {
			// A hand with no revealed cards cannot be marked. Slumbot reveals
			// every hand, so this should not happen; it is skipped rather than
			// guessed at, and the missing count shows up as a smaller n.
			continue
		}
		villain := [2]table.Card{bot[0], bot[1]}

		for _, d := range hr.Decs {
			hero, err := table.ParseCards(d.Hero)
			if err != nil || len(hero) != 2 {
				continue
			}
			board, err := table.ParseCards(d.Board)
			if err != nil {
				continue
			}
			set.Add(calib.Obs{
				Names:    names(d),
				Width:    d.Width,
				CallFrac: d.CallFrac,
				Priced:   d.Owed > 0,
				Hero:     [2]table.Card{hero[0], hero[1]},
				Villain:  villain,
				Board:    board,
			})
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return set.Buckets(), nil
}

// names is every bucket one decision belongs to.
func names(d DecisionRecord) []string {
	facing := "checked to"
	if d.Owed > 0 {
		facing = "facing a bet"
	}
	out := []string{
		calib.All,
		fmt.Sprintf("%s, %s", d.Street, facing),
	}
	// The shape claims are about what their range does, so the split that tests
	// them is by their action rather than by ours -- and by street as well,
	// because `polar` is a river rule: before the river the bluffs in a betting
	// range are draws, and RankOnBoard floors a draw at about two pair, so a
	// polarised flop range shows at the strong end rather than at both.
	if d.VillainLast != "" && d.Street != string(table.StreetPreflop) {
		out = append(out,
			"after they "+d.VillainLast,
			fmt.Sprintf("  %s, after they %s", d.Street, d.VillainLast))
	}
	return out
}

// Render writes the calibration report.
func Render(w io.Writer, buckets []*calib.Bucket, minN int) {
	fmt.Fprint(w, "Calibration against the cards Slumbot reveals.\n")
	fmt.Fprint(w, "Heads-up 200bb: the preflop widths below are 6-max charts read into another\n")
	fmt.Fprint(w, "game, so a gap there is expected. See docs/HARNESS.md §3e.\n\n")
	calib.Render(w, buckets, minN)
}
