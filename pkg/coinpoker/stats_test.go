package coinpoker

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Two real replies, kept verbatim. The real-money pool omits total_hands
// entirely and sends the id as a number; the play-money pool reports the count
// and sends the id as a string. Both shapes have to read the same.
const (
	cashReply = `{"status":"success","api_version":"1.0.0","api_code":1,"response":{"data":[` +
		`{"user_id":508957,"version":"v1.0.0","timestamp":1788386375,"ratios":[` +
		`{"mini_game_type":1,"3bet":0.0961,"cbet":0.6759,"check_raise":0.077,` +
		`"fold_to_3bet":0.5627,"fold_to_cbet":0.3147,"pfr":0.2107,"steal":0.452,` +
		`"vpip":0.2669,"wsd":0.57,"wtsd":0.2634,"allin":0.0019,"fta":0.4959,"fold":0.7528}]}]}}`

	f2pReply = `{"status":"success","api_version":"1.0.0","api_code":1,"response":{"data":[` +
		`{"user_id":"1783560","version":"v1.0.0","ratios":[` +
		`{"mini_game_type":1,"vpip":0.27,"pfr":0.05,"3bet":0.02,"fold_to_3bet":0,` +
		`"cbet":0.33,"fold_to_cbet":0.67,"steal":0.38,"check_raise":0.11,"wtsd":0.48,` +
		`"wsd":0.54,"allin":0.04,"fta":0.62,"fold":0.64,"total_hands":139}]}]}}`

	// The same request against the pool a player has no hands in: a record per
	// player, every frequency zero. It reads like a table of strangers, which is
	// the whole reason the pool has to be chosen rather than assumed.
	emptyReply = `{"status":"success","response":{"data":[` +
		`{"user_id":508957,"ratios":[{"mini_game_type":1,"vpip":0,"pfr":0,"3bet":0,` +
		`"cbet":0,"fold_to_cbet":0,"wtsd":0,"total_hands":0}]}]}}`
)

func TestParseRealMoneyReply(t *testing.T) {
	recs, err := ParseStats([]byte(cashReply), ScopeCash)
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 1 {
		t.Fatalf("records = %d, want 1", len(recs))
	}
	got := recs[0]
	if got.UserID != "508957" {
		t.Errorf("user id = %q, want 508957 (the pool sends it as a number)", got.UserID)
	}
	// Fractions, kept as the site states them.
	if got.Stats.VPIP != 0.2669 || got.Stats.PFR != 0.2107 || got.Stats.ThreeBet != 0.0961 {
		t.Errorf("preflop frequencies = %+v", got.Stats)
	}
	if got.Stats.FoldToCBet != 0.3147 || got.Stats.FoldToThreeBet != 0.5627 {
		t.Errorf("fold frequencies = %+v", got.Stats)
	}
	if got.Stats.Hands != 0 {
		t.Errorf("hands = %d, want 0: this pool reports no count", got.Stats.Hands)
	}
	if !got.Stats.Any() {
		t.Error("a populated record must not read as empty just because it has no hand count")
	}
	if got.Stats.Pool != ScopeCash {
		t.Errorf("pool = %q, want %q", got.Stats.Pool, ScopeCash)
	}
}

func TestParsePlayMoneyReplyKeepsHandCount(t *testing.T) {
	recs, err := ParseStats([]byte(f2pReply), ScopeF2P)
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 1 || recs[0].UserID != "1783560" {
		t.Fatalf("records = %+v", recs)
	}
	if recs[0].Stats.Hands != 139 {
		t.Errorf("hands = %d, want 139", recs[0].Stats.Hands)
	}
}

func TestEmptyPoolReadsAsNoData(t *testing.T) {
	recs, err := ParseStats([]byte(emptyReply), ScopeCash)
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 1 {
		t.Fatalf("records = %d, want 1: the pool answers for every player asked about", len(recs))
	}
	if recs[0].Stats.Any() {
		t.Error("all-zero record read as data")
	}
}

// poolServer serves a body per path, and counts what was asked for.
func poolServer(t *testing.T, cash, f2p string) (*Client, *[]string) {
	t.Helper()
	var asked []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("authorization") == "" {
			t.Error("request went out without a bearer token")
		}
		if strings.HasSuffix(r.URL.Path, "/f2p") {
			asked = append(asked, ScopeF2P)
			fmt.Fprint(w, f2p)
			return
		}
		asked = append(asked, ScopeCash)
		fmt.Fprint(w, cash)
	}))
	t.Cleanup(srv.Close)
	return &Client{Endpoint: srv.URL + "/stats/cash", HTTP: srv.Client()}, &asked
}

func liveToken() Token {
	return Token{Value: "token", UserID: "1783560", Expiry: time.Now().Add(time.Hour)}
}

// A real-money table costs one request: the first pool asked has the hands.
func TestFetchAutoStaysInThePoolWithTheHands(t *testing.T) {
	c, asked := poolServer(t, cashReply, f2pReply)
	recs, scope, err := c.FetchAuto(context.Background(), []string{"508957"}, liveToken(), "1783560", ScopeCash)
	if err != nil {
		t.Fatal(err)
	}
	if scope != ScopeCash {
		t.Errorf("pool = %q, want cash", scope)
	}
	if len(*asked) != 1 {
		t.Errorf("pools asked = %v, want one request", *asked)
	}
	if len(recs) != 1 || recs[0].Stats.VPIP == 0 {
		t.Errorf("records = %+v", recs)
	}
}

// A play-money table: the preferred pool answers with zeroes, so the other one
// is tried.
func TestFetchAutoFallsBackWhenTheOpponentsHaveNoHands(t *testing.T) {
	c, asked := poolServer(t, emptyReply, f2pReply)
	_, scope, err := c.FetchAuto(context.Background(), []string{"1783560"}, liveToken(), "", ScopeCash)
	if err != nil {
		t.Fatal(err)
	}
	if scope != ScopeF2P {
		t.Errorf("pool = %q, want f2p", scope)
	}
	if len(*asked) != 2 || (*asked)[0] != ScopeCash {
		t.Errorf("pools asked = %v, want cash then f2p", *asked)
	}
}

// Hero is left out of the test for "does this pool have the hands": hero has a
// history in both pools, and counting it would pin the table to the wrong one
// and leave every opponent at zero.
func TestFetchAutoIgnoresHerosOwnHistory(t *testing.T) {
	heroOnly := `{"response":{"data":[` +
		`{"user_id":"1783560","ratios":[{"vpip":0.27,"pfr":0.05,"total_hands":139}]},` +
		`{"user_id":"508957","ratios":[{"vpip":0,"pfr":0,"total_hands":0}]}]}}`
	c, asked := poolServer(t, heroOnly, f2pReply)
	_, scope, err := c.FetchAuto(context.Background(), []string{"1783560", "508957"}, liveToken(), "1783560", ScopeCash)
	if err != nil {
		t.Fatal(err)
	}
	if scope != ScopeF2P {
		t.Errorf("pool = %q, want f2p: only hero had hands in the first pool", scope)
	}
	if len(*asked) != 2 {
		t.Errorf("pools asked = %v, want both", *asked)
	}
}

func TestFetchRefusesWithoutIDsOrToken(t *testing.T) {
	c, _ := poolServer(t, cashReply, f2pReply)
	if _, err := c.Fetch(context.Background(), nil, liveToken(), ScopeCash); err == nil {
		t.Error("asking about nobody should be an error, not an empty read")
	}
	stale := Token{Value: "old", Expiry: time.Now().Add(-time.Minute)}
	if _, err := c.Fetch(context.Background(), []string{"1"}, stale, ScopeCash); err == nil {
		t.Error("an expired token should be refused before the request goes out")
	}
}

// jwt builds a token the way the client's storage holds one.
func jwt(t *testing.T, claims map[string]any) string {
	t.Helper()
	enc := func(v any) string {
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		return base64.RawURLEncoding.EncodeToString(b)
	}
	head := enc(map[string]any{"alg": "RS256", "kid": "abcdefgh"})
	return head + "." + enc(claims) + ".c2lnbmF0dXJlLWJ5dGVz"
}

func TestReadTokenPicksTheFreshestOursAndNothingElse(t *testing.T) {
	now := time.Now()
	dir := t.TempDir()

	ours := func(exp time.Time, uid string) map[string]any {
		return map[string]any{
			"iss": "https://securetoken.google.com/coinpoker-prod", "aud": "coinpoker-prod",
			"exp": exp.Unix(), "user_id": uid,
		}
	}
	body := strings.Join([]string{
		"binary junk \x00\x01",
		jwt(t, ours(now.Add(10*time.Minute), "111")), // valid, older
		jwt(t, ours(now.Add(-time.Minute), "222")),   // expired
		jwt(t, map[string]any{"iss": "https://securetoken.google.com/other", "aud": "other", "exp": now.Add(time.Hour).Unix(), "user_id": "333"}),
		jwt(t, ours(now.Add(40*time.Minute), "1783560")), // valid, freshest
	}, "\n")
	if err := os.WriteFile(filepath.Join(dir, "000003.ldb"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	// A file the storage keeps that is not one of the two kinds read.
	if err := os.WriteFile(filepath.Join(dir, "LOCK"), []byte(jwt(t, ours(now.Add(9*time.Hour), "999"))), 0o600); err != nil {
		t.Fatal(err)
	}

	tok, err := ReadTokenAt(dir, now)
	if err != nil {
		t.Fatal(err)
	}
	if tok.UserID != "1783560" {
		t.Errorf("token uid = %q, want the freshest of ours (1783560)", tok.UserID)
	}
	if !tok.Valid(now) {
		t.Error("token read as invalid at the time it was chosen for")
	}
}

func TestReadTokenSaysSoWhenThereIsNone(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "000001.log"), []byte("nothing here"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadTokenAt(dir, time.Now()); err == nil {
		t.Error("storage with no token should be an error, not an empty token")
	}
}
