// Command cpstats asks CoinPoker for what it knows about a list of players --
// the same request the client makes to fill its own player popup -- and prints
// the answer.
//
// It exists to check the read the assistant runs on without the assistant: the
// token, which pool a table's players live in, and the numbers themselves.
//
//	cpstats -ids 508957,1783560          # both pools, whichever has the hands
//	cpstats -ids 508957 -scope f2p       # one pool
//	cpstats -ids 508957 -raw             # the reply as it arrived
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"poker-game-analyzer/pkg/coinpoker"
)

func main() {
	var (
		ids   = flag.String("ids", "", "comma-separated player ids to ask about")
		scope = flag.String("scope", "", "one pool only: cash (real money) or f2p")
		raw   = flag.Bool("raw", false, "print the reply body instead of the parsed records")
		token = flag.String("token", "", "bearer token; read from the client's storage when empty")
	)
	flag.Parse()

	list := strings.Split(*ids, ",")
	if strings.TrimSpace(*ids) == "" {
		fmt.Fprintln(os.Stderr, "cpstats: -ids is required")
		os.Exit(2)
	}

	tok := coinpoker.Token{Value: *token, Expiry: time.Now().Add(time.Hour)}
	if *token == "" {
		var err error
		tok, err = coinpoker.ReadToken()
		if err != nil {
			fmt.Fprintf(os.Stderr, "cpstats: %v (is the client running and logged in?)\n", err)
			os.Exit(1)
		}
		fmt.Printf("token: uid=%s valid for %s\n", tok.UserID, time.Until(tok.Expiry).Truncate(time.Second))
	}

	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	c := &coinpoker.Client{}

	if *raw {
		body, code, err := c.FetchRaw(ctx, list, tok, orDefault(*scope, coinpoker.ScopeCash))
		fmt.Printf("HTTP %d err=%v\n%s\n", code, err, string(body))
		return
	}

	var (
		recs []coinpoker.Record
		pool string
		err  error
	)
	if *scope != "" {
		pool = *scope
		recs, err = c.Fetch(ctx, list, tok, *scope)
	} else {
		recs, pool, err = c.FetchAuto(ctx, list, tok, tok.UserID, coinpoker.ScopeCash)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "cpstats: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("pool=%s records=%d\n", pool, len(recs))
	for _, r := range recs {
		s := r.Stats
		fmt.Printf("  %-10s vpip=%-5.1f pfr=%-5.1f 3bet=%-5.1f f3b=%-5.1f cbet=%-5.1f fcb=%-5.1f wtsd=%-5.1f hands=%d\n",
			r.UserID, pct(s.VPIP), pct(s.PFR), pct(s.ThreeBet), pct(s.FoldToThreeBet),
			pct(s.CBet), pct(s.FoldToCBet), pct(s.WTSD), s.Hands)
	}
	if len(recs) == 0 {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(recs)
	}
}

func pct(v float64) float64 { return v * 100 }

func orDefault(v, def string) string {
	if v == "" {
		return def
	}
	return v
}
