package coinpoker

import (
	"context"
	"log"
	"sync"
	"time"

	"poker-game-analyzer/pkg/table"
)

// Feed keeps the site's reads for whoever is at the table and stamps them onto
// the states going past.
//
// The capture loop must never wait on a network. It reconstructs the table from
// packets and a request that blocks it costs an action, so Annotate only ever
// reads what is already cached and notes which players are missing; the asking
// happens on its own goroutine. A read that arrives a second into the hand is
// worth having -- these are thirty-day aggregates, not the current bet.
type Feed struct {
	// TTL is how long a read stays fresh. The aggregate is a month long, so
	// this is about a player being new to the table, not about the numbers
	// moving.
	TTL time.Duration
	// MinInterval is the floor between requests, so a table that keeps
	// producing states does not keep producing requests.
	MinInterval time.Duration

	client *Client
	// Log is where a failure is reported, at most once per interval. Nil is
	// silent.
	Log func(format string, args ...any)

	mu       sync.Mutex
	stats    map[string]table.SiteStats
	fetched  map[string]time.Time
	heroID   string
	scope    string
	lastTry  time.Time
	inFlight bool
	token    Token
}

// NewFeed makes a feed with the live client and the usual timings.
func NewFeed() *Feed {
	return &Feed{
		TTL:         30 * time.Minute,
		MinInterval: 10 * time.Second,
		client:      &Client{},
		stats:       make(map[string]table.SiteStats),
		fetched:     make(map[string]time.Time),
		scope:       ScopeCash,
		Log:         func(f string, a ...any) { log.Printf(f, a...) },
	}
}

// SetHero tells the feed which id is ours. It is used to pick the pool: hero
// usually has hands in both, so hero's own record cannot say which pool the
// table is in.
func (f *Feed) SetHero(id string) {
	if f == nil {
		return
	}
	f.mu.Lock()
	f.heroID = id
	f.mu.Unlock()
}

// Pool is the pool the last successful request answered from.
func (f *Feed) Pool() string {
	if f == nil {
		return ""
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.scope
}

// busy reports whether a request is in flight.
func (f *Feed) busy() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.inFlight
}

// Get is the cached read for a player, if there is one.
func (f *Feed) Get(playerID string) (table.SiteStats, bool) {
	if f == nil {
		return table.SiteStats{}, false
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	s, ok := f.stats[playerID]
	return s, ok
}

// Annotate fills in what is known about the players in this state and asks for
// what is not. It never blocks: a state is annotated with whatever has arrived
// by the time it passes.
func (f *Feed) Annotate(hs *table.HandState) {
	if f == nil || hs == nil {
		return
	}
	ids := make([]string, 0, len(hs.Seats))
	for _, seat := range hs.Seats {
		if seat.PlayerID != "" {
			ids = append(ids, seat.PlayerID)
		}
	}
	if len(ids) == 0 {
		return
	}

	f.mu.Lock()
	now := time.Now()
	stale := false
	for _, id := range ids {
		if at, ok := f.fetched[id]; !ok || now.Sub(at) > f.TTL {
			stale = true
			break
		}
	}
	for i := range hs.Seats {
		if s, ok := f.stats[hs.Seats[i].PlayerID]; ok {
			copyOf := s
			hs.Seats[i].Site = &copyOf
		}
	}
	start := stale && !f.inFlight && now.Sub(f.lastTry) >= f.MinInterval
	if start {
		f.inFlight = true
		f.lastTry = now
	}
	f.mu.Unlock()

	if start {
		go f.refresh(ids)
	}
}

// refresh asks about the whole table at once -- the endpoint takes a list, and
// one request for six players is what the client itself does.
func (f *Feed) refresh(ids []string) {
	defer func() {
		f.mu.Lock()
		f.inFlight = false
		f.mu.Unlock()
	}()

	tok, err := f.currentToken()
	if err != nil {
		f.logf("[STATS] no token: %v", err)
		return
	}

	f.mu.Lock()
	hero, prefer := f.heroID, f.scope
	f.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	recs, scope, err := f.client.FetchAuto(ctx, ids, tok, hero, prefer)
	if err != nil {
		f.logf("[STATS] %v", err)
		return
	}

	now := time.Now()
	f.mu.Lock()
	f.scope = scope
	// Every id asked about is stamped, answered or not: a player the site has
	// never seen must not be asked about again on the next state.
	for _, id := range ids {
		f.fetched[id] = now
	}
	counted := 0
	for _, r := range recs {
		if !r.Stats.Any() {
			continue
		}
		f.stats[r.UserID] = table.SiteStats{
			VPIP: r.Stats.VPIP, PFR: r.Stats.PFR, ThreeBet: r.Stats.ThreeBet,
			FoldToThreeBet: r.Stats.FoldToThreeBet, CBet: r.Stats.CBet,
			FoldToCBet: r.Stats.FoldToCBet, Steal: r.Stats.Steal,
			CheckRaise: r.Stats.CheckRaise, WTSD: r.Stats.WTSD, WSD: r.Stats.WSD,
			Hands: r.Stats.Hands, Pool: scope,
		}
		counted++
	}
	f.mu.Unlock()

	f.logf("[STATS] pool=%s %d/%d players with a history", scope, counted, len(ids))
}

// currentToken returns a live token, reading a fresh one from the client's
// storage when the one in hand is gone. The site rotates them about hourly.
func (f *Feed) currentToken() (Token, error) {
	f.mu.Lock()
	tok := f.token
	f.mu.Unlock()
	// A minute of margin, so a token is not spent on a request that outlives it.
	if tok.Valid(time.Now().Add(time.Minute)) {
		return tok, nil
	}
	tok, err := ReadToken()
	if err != nil {
		return Token{}, err
	}
	f.mu.Lock()
	f.token = tok
	if f.heroID == "" {
		// The token was issued to hero's account, so it names hero when the
		// caller has not.
		f.heroID = tok.UserID
	}
	f.mu.Unlock()
	return tok, nil
}

func (f *Feed) logf(format string, args ...any) {
	if f.Log != nil {
		f.Log(format, args...)
	}
}
