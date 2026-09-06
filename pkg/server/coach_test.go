package server

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"poker-game-analyzer/pkg/llm"
	"poker-game-analyzer/pkg/storage"
	"poker-game-analyzer/pkg/table"
	"poker-game-analyzer/pkg/vision"
)

type countingCoach struct {
	calls   int32
	release chan struct{}
	mu      sync.Mutex
	last    llm.CoachInput
}

func (c *countingCoach) AdviseHand(ctx context.Context, in llm.CoachInput) (*llm.CoachAdvice, error) {
	atomic.AddInt32(&c.calls, 1)
	c.mu.Lock()
	c.last = in
	c.mu.Unlock()
	if c.release != nil {
		select {
		case <-c.release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return &llm.CoachAdvice{Action: "call", Amount: 2000, Confidence: 0.6, Reasoning: "ок"}, nil
}

func coachState(pot float64) *table.HandState {
	ah, _ := table.ParseCard("Ah")
	kh, _ := table.ParseCard("Kh")
	return &table.HandState{
		TableID:     "coach-table",
		Street:      table.StreetPreflop,
		Pot:         pot,
		CurrentBet:  2000,
		SmallBlind:  1000,
		BigBlind:    2000,
		HeroID:      "hero",
		HeroCards:   [2]table.Card{ah, kh},
		HeroButtons: []string{"fold", "call"},
		IsHeroTurn:  true,
		Seats: []table.SeatState{
			{SeatNumber: 0, PlayerID: "hero", PlayerName: "hero", Stack: 100000, IsActive: true},
			{SeatNumber: 3, PlayerID: "opp", PlayerName: "villain", Stack: 90000, CurrentBet: 2000, IsActive: true},
		},
	}
}

func newCoachServer(t *testing.T, c llm.Coach) *Server {
	t.Helper()
	srv := NewServer(storage.NewMemoryCache(), nil, nil)
	srv.SetCoach(c)
	return srv
}

func ingest(t *testing.T, srv *Server, h *table.HandState) {
	t.Helper()
	if _, err := srv.ProcessEvent(vision.VisionEvent{
		Type:      vision.EventHeroTurn,
		TableID:   "coach-table",
		HandState: h,
	}); err != nil {
		t.Fatalf("ProcessEvent: %v", err)
	}
}

// The screen is read about twelve times a second. Asking the model per frame
// would be a queue that never drains, a bill to match, and answers arriving
// against tables that have moved on.
func TestCoachIsAskedOncePerSpot(t *testing.T) {
	c := &countingCoach{}
	srv := newCoachServer(t, c)

	for range 12 {
		ingest(t, srv, coachState(10000))
	}
	waitFor(t, func() bool { return atomic.LoadInt32(&c.calls) >= 1 })
	time.Sleep(50 * time.Millisecond)

	if got := atomic.LoadInt32(&c.calls); got != 1 {
		t.Fatalf("twelve identical frames asked the model %d times", got)
	}

	// A new decision is a new question. The pot is fed twice because a new one
	// has to be read twice before the stabiliser believes it, so a single
	// frame is genuinely not yet a new spot.
	ingest(t, srv, coachState(40000))
	ingest(t, srv, coachState(40000))
	waitFor(t, func() bool { return atomic.LoadInt32(&c.calls) == 2 })
}

// A spot the model is still reading must not be asked about again, or a slow
// answer turns into a pile of them.
func TestCoachDoesNotStackUpWhileThinking(t *testing.T) {
	c := &countingCoach{release: make(chan struct{})}
	srv := newCoachServer(t, c)

	ingest(t, srv, coachState(10000))
	waitFor(t, func() bool { return atomic.LoadInt32(&c.calls) == 1 })
	for range 20 {
		ingest(t, srv, coachState(10000))
	}
	if got := atomic.LoadInt32(&c.calls); got != 1 {
		t.Fatalf("a spot already being read was asked about %d times", got)
	}
	close(c.release)
}

// Nothing is asked when hero has no decision: the panel exists to answer "what
// do I do now", and that question only arises on hero's turn.
func TestCoachIsSilentWhenHeroCannotAct(t *testing.T) {
	c := &countingCoach{}
	srv := newCoachServer(t, c)

	h := coachState(10000)
	h.IsHeroTurn = false
	h.HeroButtons = nil
	for range 5 {
		ingest(t, srv, h)
	}
	time.Sleep(60 * time.Millisecond)
	if got := atomic.LoadInt32(&c.calls); got != 0 {
		t.Fatalf("the model was asked %d times about somebody else's turn", got)
	}
}

// With no key there is no coach, and nothing anywhere may depend on there
// being one.
func TestNoCoachIsNotAnError(t *testing.T) {
	srv := NewServer(storage.NewMemoryCache(), nil, nil)
	if srv.HasCoach() {
		t.Fatal("a server with no coach reported one")
	}
	ingest(t, srv, coachState(10000))
}

// The model is handed the table, not a summary of it.
func TestCoachIsGivenTheStateAndTheToolsOwnAnswer(t *testing.T) {
	c := &countingCoach{}
	srv := newCoachServer(t, c)
	ingest(t, srv, coachState(10000))
	waitFor(t, func() bool { return atomic.LoadInt32(&c.calls) == 1 })

	c.mu.Lock()
	in := c.last
	c.mu.Unlock()

	if in.State == nil || in.State.Pot != 10000 {
		t.Fatalf("state not passed through: %+v", in.State)
	}
	if in.Own == nil {
		t.Fatal("the model was not told what the tool itself decided, so its answer cannot be read as agreement")
	}
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("timed out waiting for the coach")
}

// A hand where hero has no decision must not leave the previous spot's second
// opinion standing. The card is cleared, and the spot is forgotten -- so if
// the same decision comes back round, it is a question again rather than a
// stale answer.
func TestCoachForgetsTheSpotWhenThereIsNoDecision(t *testing.T) {
	c := &countingCoach{}
	srv := newCoachServer(t, c)

	ingest(t, srv, coachState(10000))
	waitFor(t, func() bool { return atomic.LoadInt32(&c.calls) == 1 })

	folded := coachState(10000)
	folded.IsHeroTurn = false
	folded.HeroButtons = nil
	ingest(t, srv, folded)

	srv.coachRunner.mu.Lock()
	spot := srv.coachRunner.lastSpot["coach-table"]
	srv.coachRunner.mu.Unlock()
	if spot != "" {
		t.Fatalf("the spot survived a frame with no decision: %q", spot)
	}

	ingest(t, srv, coachState(10000))
	waitFor(t, func() bool { return atomic.LoadInt32(&c.calls) == 2 })
}
