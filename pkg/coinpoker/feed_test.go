package coinpoker

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"poker-game-analyzer/pkg/table"
)

// feedFor makes a feed whose requests go to a test server, with the token
// already in hand so nothing reads the real client's storage.
func feedFor(t *testing.T, body string) (*Feed, *atomic.Int32) {
	t.Helper()
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if strings.HasSuffix(r.URL.Path, "/f2p") {
			_, _ = w.Write([]byte(emptyReply))
			return
		}
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)

	f := NewFeed()
	f.client = &Client{Endpoint: srv.URL + "/stats/cash", HTTP: srv.Client()}
	f.token = liveToken()
	f.MinInterval = 0
	f.Log = func(string, ...any) {}
	f.SetHero("1783560")
	return f, &calls
}

func stateWith(ids ...string) *table.HandState {
	hs := &table.HandState{HandID: "h", TableID: "t", HeroID: "1783560"}
	for _, id := range ids {
		hs.Seats = append(hs.Seats, table.SeatState{PlayerID: id, IsActive: true})
	}
	return hs
}

// Annotate never waits on the network: the first state goes past unannotated and
// the read appears on a later one.
func TestFeedAnnotatesOnceTheReadArrives(t *testing.T) {
	f, calls := feedFor(t, cashReply)

	first := stateWith("508957", "1783560")
	f.Annotate(first)
	if first.Seats[0].Site != nil {
		t.Error("the first state was annotated, which means Annotate waited on the request")
	}

	var got *table.SiteStats
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		hs := stateWith("508957", "1783560")
		f.Annotate(hs)
		if hs.Seats[0].Site != nil {
			got = hs.Seats[0].Site
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if got == nil {
		t.Fatal("no read after the fetch had time to land")
	}
	if got.VPIP != 0.2669 || got.Pool != ScopeCash {
		t.Errorf("read = %+v, want the cash pool's 0.2669", got)
	}
	if got.Hands != 0 {
		t.Errorf("hands = %d, want 0: the real-money pool reports none", got.Hands)
	}
	if n := calls.Load(); n != 1 {
		t.Errorf("requests = %d, want 1 for a settled table", n)
	}
}

// A player the site has never heard of must not be asked about again on every
// state: the attempt is what is remembered, not just the answer.
func TestFeedDoesNotRepeatItselfForPlayersWithNoHistory(t *testing.T) {
	f, calls := feedFor(t, emptyReply)

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		f.Annotate(stateWith("508957"))
		if calls.Load() > 0 && !f.busy() {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	before := calls.Load()
	if before == 0 {
		t.Fatal("no request was made at all")
	}
	for range 20 {
		f.Annotate(stateWith("508957"))
	}
	if got := calls.Load(); got != before {
		t.Errorf("requests = %d, want no more than the %d already made", got, before)
	}
}

// A feed nobody configured is inert rather than fatal: the agent runs with the
// site read turned off, and every state still goes through the same call.
func TestNilFeedIsInert(t *testing.T) {
	var f *Feed
	hs := stateWith("1")
	f.Annotate(hs)
	f.SetHero("2")
	if _, ok := f.Get("1"); ok {
		t.Error("a nil feed answered with a read")
	}
	if hs.Seats[0].Site != nil {
		t.Error("a nil feed annotated a state")
	}
}
