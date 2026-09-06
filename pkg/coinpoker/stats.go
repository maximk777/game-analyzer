package coinpoker

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// The stats API, and the two pools behind it.
//
// A real-money table (the stake is quoted in ₮, the site's USDT) and a
// free-to-play table are separate games with separate histories, and the
// endpoint splits them: the base path is the real-money pool and /f2p is the
// play-money one. A player can have hands in one, both or neither, and the
// client picks the pool by the table it opened.
//
// Getting that wrong is silent. Asking the play-money pool about real-money
// opponents answers with a record per player, all zeroes, which reads exactly
// like a table of strangers -- every opponent 0/0/0 in the panel while the
// client's own popup, beside it, showed 25/20/9.
const (
	statsEndpoint = "https://nxtgenapi.thecloudinfra.com/pbshots/v1/stats/cash"

	// ScopeCash is the real-money pool, ScopeF2P the play-money one.
	ScopeCash = "cash"
	ScopeF2P  = "f2p"
)

// Scopes is both pools, in the order a table is most likely to be.
var Scopes = []string{ScopeCash, ScopeF2P}

// clientVersion and userAgent make the request the client's own. The endpoint is
// the client's private API, reached with the client's token from the machine the
// client is running on; a request that announces itself as something else is
// answered by a bot check rather than by the stats.
const (
	clientVersion = "1.21.0"
	userAgent     = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 " +
		"(KHTML, like Gecko) CoinPoker/1.21.0 Chrome/142.0.7444.265 Electron/39.8.7 Safari/537.36"
)

// Stats is one player's aggregate as the site computes it.
//
// Every frequency is a fraction, the way the wire states it: 0.27 is a VPIP of
// twenty-seven percent. The tool's own tendencies are a mixture of percentages
// and fractions for historical reasons; converting happens where they meet, not
// here, so that what this package holds is what the site said.
type Stats struct {
	VPIP           float64 `json:"vpip"`
	PFR            float64 `json:"pfr"`
	ThreeBet       float64 `json:"three_bet"`
	FoldToThreeBet float64 `json:"fold_to_3bet"`
	CBet           float64 `json:"cbet"`
	FoldToCBet     float64 `json:"fold_to_cbet"`
	Steal          float64 `json:"steal"`
	CheckRaise     float64 `json:"check_raise"`
	WTSD           float64 `json:"wtsd"`
	WSD            float64 `json:"wsd"`

	// Hands is the sample behind the aggregate, and zero means the pool did not
	// report one -- which the real-money pool does not, even when the numbers
	// are populated. So the presence of data cannot be tested by this, and a
	// weight cannot be derived from it; see Any.
	Hands int `json:"hands"`

	// Pool is which of the two pools these came from, kept so a read can say
	// what game it describes.
	Pool string `json:"pool"`
}

// Any reports whether this record says anything at all. It is the test for
// "does this player have hands in this pool", since the pool that has them may
// still report no count.
func (s Stats) Any() bool {
	return s.VPIP > 0 || s.PFR > 0 || s.ThreeBet > 0 || s.CBet > 0 ||
		s.FoldToCBet > 0 || s.FoldToThreeBet > 0 || s.WTSD > 0 || s.Hands > 0
}

// Record is one player's stats as returned.
type Record struct {
	UserID string
	Stats  Stats
}

// Client fetches from the stats API. The zero value works: it uses the real
// endpoint and a client with a timeout.
type Client struct {
	HTTP *http.Client
	// Endpoint overrides the real-money pool's URL; /f2p is appended for the
	// other pool. For the tests.
	Endpoint string
}

func (c *Client) httpClient() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{Timeout: 20 * time.Second}
}

func (c *Client) urlFor(scope string) string {
	base := c.Endpoint
	if base == "" {
		base = statsEndpoint
	}
	if scope == ScopeF2P {
		return base + "/f2p"
	}
	return base
}

// Fetch asks one pool about the given players. An empty id list, or a token that
// has expired, is an error rather than an empty answer: both mean the caller is
// about to record a table of strangers for a reason that has nothing to do with
// the players.
func (c *Client) Fetch(ctx context.Context, ids []string, tok Token, scope string) ([]Record, error) {
	body, code, err := c.FetchRaw(ctx, ids, tok, scope)
	if err != nil {
		return nil, err
	}
	if code != http.StatusOK {
		return nil, fmt.Errorf("coinpoker: stats %s: HTTP %d: %s", scope, code,
			firstLine(string(body), 200))
	}
	return ParseStats(body, scope)
}

// FetchRaw is Fetch without the parsing: the reply body and its status. It is
// what the diagnostic tool prints, and what makes a change in the reply's shape
// visible instead of merely producing no records.
func (c *Client) FetchRaw(ctx context.Context, ids []string, tok Token, scope string) ([]byte, int, error) {
	ids = idsFromStrings(ids)
	if len(ids) == 0 {
		return nil, 0, fmt.Errorf("coinpoker: no player ids to ask about")
	}
	if !tok.Valid(time.Now()) {
		return nil, 0, ErrNoToken
	}

	q := url.Values{
		"mini_game_type":    {"1"},
		"period":            {"30"},
		"player_count_type": {"non_heads_up"},
		"user_id":           {strings.Join(ids, ",")},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.urlFor(scope)+"?"+q.Encode(), nil)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("accept", "application/json")
	req.Header.Set("accept-language", "en-US")
	req.Header.Set("app-version", clientVersion)
	req.Header.Set("cache-control", "no-cache")
	req.Header.Set("authorization", "Bearer "+tok.Value)
	req.Header.Set("sec-ch-ua", `"Not_A Brand";v="99", "Chromium";v="142"`)
	req.Header.Set("sec-ch-ua-mobile", "?0")
	req.Header.Set("sec-ch-ua-platform", `"macOS"`)
	req.Header.Set("sec-fetch-dest", "empty")
	req.Header.Set("sec-fetch-mode", "cors")
	req.Header.Set("sec-fetch-site", "cross-site")
	req.Header.Set("user-agent", userAgent)

	resp, err := c.httpClient().Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, resp.StatusCode, err
	}
	return body, resp.StatusCode, nil
}

// FetchAuto asks the pool the opponents' hands are actually in.
//
// It tries prefer first and falls back to the other pool only when no opponent
// in the reply said anything -- hero is left out of that test, because hero
// usually has hands in both pools and would keep the wrong one looking right.
// The pool that answered is returned so a caller can hand it back as prefer: a
// table that has settled then costs one request.
func (c *Client) FetchAuto(ctx context.Context, ids []string, tok Token, heroID, prefer string) ([]Record, string, error) {
	if prefer == "" {
		prefer = ScopeCash
	}
	order := []string{prefer}
	for _, s := range Scopes {
		if s != prefer {
			order = append(order, s)
		}
	}

	var (
		lastRecs  []Record
		lastScope = prefer
		lastErr   error
	)
	for _, scope := range order {
		recs, err := c.Fetch(ctx, ids, tok, scope)
		if err != nil {
			lastErr = err
			continue
		}
		lastRecs, lastScope, lastErr = recs, scope, nil
		if hasOpponentData(recs, heroID) {
			return recs, scope, nil
		}
	}
	return lastRecs, lastScope, lastErr
}

// hasOpponentData reports whether any player other than hero has a history in
// this pool.
func hasOpponentData(recs []Record, heroID string) bool {
	for _, r := range recs {
		if heroID != "" && r.UserID == heroID {
			continue
		}
		if r.Stats.Any() {
			return true
		}
	}
	return false
}

// ratioFields maps the wire's names for the frequencies onto the record. The
// names it does not use are left out rather than kept blindly: a field nothing
// reads is a field nobody checks the scale of.
var ratioFields = map[string]func(*Stats, float64){
	"vpip":         func(s *Stats, v float64) { s.VPIP = v },
	"pfr":          func(s *Stats, v float64) { s.PFR = v },
	"3bet":         func(s *Stats, v float64) { s.ThreeBet = v },
	"fold_to_3bet": func(s *Stats, v float64) { s.FoldToThreeBet = v },
	"cbet":         func(s *Stats, v float64) { s.CBet = v },
	"fold_to_cbet": func(s *Stats, v float64) { s.FoldToCBet = v },
	"steal":        func(s *Stats, v float64) { s.Steal = v },
	"check_raise":  func(s *Stats, v float64) { s.CheckRaise = v },
	"wtsd":         func(s *Stats, v float64) { s.WTSD = v },
	"wsd":          func(s *Stats, v float64) { s.WSD = v },
}

// ParseStats reads a reply into one record per player.
//
// The interesting objects are found by shape rather than by path -- anything
// carrying a user id and a ratios list -- because the envelope around them has
// changed more than once and the payload has not.
func ParseStats(body []byte, scope string) ([]Record, error) {
	var payload any
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("coinpoker: stats reply: %w", err)
	}
	var out []Record
	walkJSON(payload, func(obj map[string]any) {
		rawID, hasID := obj["user_id"]
		ratios, hasRatios := obj["ratios"].([]any)
		if !hasID || !hasRatios || len(ratios) == 0 {
			return
		}
		first, ok := ratios[0].(map[string]any)
		if !ok {
			return
		}
		id := scalarString(rawID)
		if id == "" {
			return
		}
		s := Stats{Pool: scope}
		for wire, set := range ratioFields {
			if v, ok := numberOf(first[wire]); ok {
				set(&s, v)
			}
		}
		// The count is reported for one pool and not the other, so it is read
		// when it is there and never required.
		if v, ok := numberOf(first["total_hands"]); ok {
			s.Hands = int(v)
		}
		out = append(out, Record{UserID: id, Stats: s})
	})
	return out, nil
}

// walkJSON visits every object in a decoded document.
func walkJSON(v any, visit func(map[string]any)) {
	switch t := v.(type) {
	case map[string]any:
		visit(t)
		for _, child := range t {
			walkJSON(child, visit)
		}
	case []any:
		for _, child := range t {
			walkJSON(child, visit)
		}
	}
}

// numberOf reads a JSON number, rejecting the null the empty pool sends.
func numberOf(v any) (float64, bool) {
	switch t := v.(type) {
	case float64:
		return t, true
	case json.Number:
		f, err := t.Float64()
		return f, err == nil
	default:
		return 0, false
	}
}

// scalarString reads an id that may arrive as a string or a number.
func scalarString(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case float64:
		return fmt.Sprintf("%.0f", t)
	case json.Number:
		return t.String()
	default:
		return ""
	}
}

// firstLine trims a reply for an error message: the body of a refusal is a page,
// and the useful part of it is the beginning.
func firstLine(s string, max int) string {
	if i := strings.IndexAny(s, "\r\n"); i >= 0 {
		s = s[:i]
	}
	if len(s) > max {
		s = s[:max] + "…"
	}
	return strings.TrimSpace(s)
}
