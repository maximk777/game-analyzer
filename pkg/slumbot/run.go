package slumbot

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"poker-game-analyzer/pkg/advice"
	"poker-game-analyzer/pkg/table"
)

// DecisionRecord is one decision as written to the log. Cards are strings
// because the log is meant to be readable and re-analysed without this package.
type DecisionRecord struct {
	Street      string  `json:"street"`
	Width       float64 `json:"assumed_width"`
	CallFrac    float64 `json:"call_range_fraction,omitempty"`
	Action      string  `json:"action"`
	Amount      int     `json:"amount,omitempty"`
	FromAdvisor bool    `json:"from_advisor"`
	VillainLast string  `json:"villain_last,omitempty"`
	Board       string  `json:"board"`
	Hero        string  `json:"hero"`
	Pot         int     `json:"pot"`
	Owed        int     `json:"owed"`
}

// HandRecord is one hand: what we believed at each turn, and what the opponent
// turned out to hold. The second half is the reason this run exists.
type HandRecord struct {
	N        int              `json:"n"`
	HeroSeat int              `json:"hero_seat"`
	Hero     string           `json:"hero"`
	Bot      string           `json:"bot"`
	Board    string           `json:"board"`
	Action   string           `json:"action_string"`
	Winnings int              `json:"winnings"`
	Baseline int              `json:"baseline_winnings"`
	Decs     []DecisionRecord `json:"decisions"`
}

func cardsString(cs []table.Card) string {
	out := ""
	for i, c := range cs {
		if i > 0 {
			out += " "
		}
		out += c.String()
	}
	return out
}

func decisionRecord(d Decision) DecisionRecord {
	return DecisionRecord{
		Street:      string(d.Street),
		Width:       d.AssumedWidth,
		CallFrac:    d.CallRangeFraction,
		Action:      string(d.Action),
		Amount:      d.Amount,
		FromAdvisor: d.FromAdvisor,
		VillainLast: string(d.VillainLast),
		Board:       cardsString(d.Board),
		Hero:        cardsString(d.HeroCards[:]),
		Pot:         d.Pot,
		Owed:        d.Owed,
	}
}

// RunStats is what a run reports when it ends.
type RunStats struct {
	Hands           int
	Decisions       int
	AdvisorDecs     int
	Anomalies       []Anomaly
	Winnings        int
	BaselineTotal   int
	Elapsed         time.Duration
	LastSessionHand int
}

// Run plays n hands, writing one HandRecord per line to out.
//
// It stops on the first error rather than skipping the hand: a run whose length
// is a claim about a sample size must not quietly be shorter than it says.
func Run(ctx context.Context, c *Client, n int, opt advice.Options, out io.Writer,
	progress func(RunStats)) (RunStats, error) {

	enc := json.NewEncoder(out)
	stats := RunStats{}
	started := time.Now()

	for i := 0; i < n; i++ {
		if err := ctx.Err(); err != nil {
			stats.Elapsed = time.Since(started)
			return stats, err
		}
		r, err := c.NewHand(ctx)
		if err != nil {
			stats.Elapsed = time.Since(started)
			return stats, fmt.Errorf("hand %d: %w", i+1, err)
		}
		rec := HandRecord{N: i + 1, HeroSeat: int(r.HeroSeat())}

		for turn := 0; !r.Over(); turn++ {
			if turn > 64 {
				stats.Elapsed = time.Since(started)
				return stats, fmt.Errorf("hand %d: no end after %d turns, action %q",
					i+1, turn, r.Action)
			}
			st, err := ParseAction(r.Action)
			if err != nil {
				stats.Elapsed = time.Since(started)
				return stats, fmt.Errorf("hand %d: %w", i+1, err)
			}
			if st.ToAct != r.HeroSeat() {
				stats.Elapsed = time.Since(started)
				return stats, fmt.Errorf("hand %d: server returned on %v's turn, action %q",
					i+1, st.ToAct, r.Action)
			}
			incr, d, an, err := Decide(r, st, opt)
			if err != nil {
				stats.Elapsed = time.Since(started)
				return stats, fmt.Errorf("hand %d: %w", i+1, err)
			}
			if an != nil {
				stats.Anomalies = append(stats.Anomalies, *an)
			}
			rec.Decs = append(rec.Decs, decisionRecord(d))
			stats.Decisions++
			if d.FromAdvisor {
				stats.AdvisorDecs++
			}
			next, err := c.Act(ctx, incr)
			if err != nil {
				var rej *ActionRejected
				if !errors.As(err, &rej) {
					stats.Elapsed = time.Since(started)
					return stats, fmt.Errorf("hand %d, sending %q: %w", i+1, incr, err)
				}
				// Slumbot refused the size. The belief this run measures was
				// recorded before the action was sent, so the hand is not lost
				// -- but the refusal means a rule about legal sizes is not
				// understood here, so it is recorded in full rather than
				// smoothed over, and the run continues on the safe action.
				fallback := "k"
				if st.Owed() > 0 {
					fallback = "c"
				}
				stats.Anomalies = append(stats.Anomalies, Anomaly{
					Action: d.Action,
					Owed:   st.Owed(),
					Reason: fmt.Sprintf("%s (action %q, committed %v, street %v, "+
						"min raise %d, max %d) -- played %q instead",
						rej.Msg, r.Action, st.Committed, st.StreetIn,
						minRaiseTo(st), maxTo(st, r.HeroSeat()), fallback),
				})
				if next, err = c.Act(ctx, fallback); err != nil {
					stats.Elapsed = time.Since(started)
					return stats, fmt.Errorf("hand %d, falling back to %q after %v: %w",
						i+1, fallback, rej, err)
				}
			}
			r = next
		}

		rec.Hero = joinCards(r.HoleCards)
		rec.Bot = joinCards(r.BotHoleCards)
		rec.Board = joinCards(r.Board)
		rec.Action = r.Action
		rec.Winnings = *r.Winnings
		if r.BaselineWinnings != nil {
			rec.Baseline = *r.BaselineWinnings
		}
		if err := enc.Encode(rec); err != nil {
			stats.Elapsed = time.Since(started)
			return stats, fmt.Errorf("hand %d: writing record: %w", i+1, err)
		}

		stats.Hands++
		stats.Winnings += rec.Winnings
		stats.BaselineTotal += rec.Baseline
		stats.LastSessionHand = r.SessionNumHands
		if progress != nil {
			stats.Elapsed = time.Since(started)
			progress(stats)
		}
	}
	stats.Elapsed = time.Since(started)
	return stats, nil
}

func joinCards(ss []string) string {
	out := ""
	for i, s := range ss {
		if i > 0 {
			out += " "
		}
		out += s
	}
	return out
}
