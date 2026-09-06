package server

import (
	"testing"

	"poker-game-analyzer/pkg/storage"
	"poker-game-analyzer/pkg/table"
)

// A player who has folded has no decision left. The guard against advising a
// folded hand lives in the advisor; this checks the server honours it: live,
// hero had folded and the HUD went on recommending an all-in, sized off another
// player's stack.
func TestIngest_NoAdviceAfterHeroFolds(t *testing.T) {
	srv := NewServer(storage.NewMemoryCache(), nil, nil)

	hero, err := table.ParseCards("8h 5h")
	if err != nil {
		t.Fatalf("parsing hero cards: %v", err)
	}
	board, err := table.ParseCards("5d 3c 6h 8c")
	if err != nil {
		t.Fatalf("parsing board: %v", err)
	}

	state := func(folded bool) *table.HandState {
		return &table.HandState{
			TableID: "t", HandID: "h1", Street: table.StreetTurn, Pot: 34800,
			CommunityCards: board,
			HeroID:         "hero",
			// The client is waiting on hero, which is the only condition under
			// which there is a decision to advise about.
			IsHeroTurn: true,
			HeroCards:  [2]table.Card{hero[0], hero[1]},
			Seats: []table.SeatState{
				{PlayerID: "hero", PlayerName: "hero", Stack: 153200, IsActive: true, IsFolded: folded},
				{PlayerID: "villain", PlayerName: "villain", Stack: 301607, IsActive: true},
			},
		}
	}

	rec, err := srv.IngestLiveState(state(false))
	if err != nil {
		t.Fatalf("ingest while live: %v", err)
	}
	if rec == nil {
		t.Fatal("expected advice while hero is still in the hand")
	}

	rec, err = srv.IngestLiveState(state(true))
	if err != nil {
		t.Fatalf("ingest after folding: %v", err)
	}
	if rec != nil {
		t.Errorf("advised %s %.0f after hero folded", rec.PrimaryAction, rec.RecommendedAmount)
	}
}

// A hand is persisted when, and only when, it ends -- the wire marks the end by
// setting the street to showdown. A live (river) state is not saved; the
// showdown state that follows is, once, with its wire hand id, the board it
// finished on, and the hands that were shown.
func TestIngest_ShowdownHandIsPersistedOnce(t *testing.T) {
	db, err := storage.NewSQLiteDB(":memory:")
	if err != nil {
		t.Fatalf("opening database: %v", err)
	}
	defer db.Close()

	srv := NewServer(storage.NewMemoryCache(), db, nil)

	board, err := table.ParseCards("10c 8s 2c 7h 4d")
	if err != nil {
		t.Fatalf("parsing board: %v", err)
	}
	shown, err := table.ParseCards("6c 6h")
	if err != nil {
		t.Fatalf("parsing revealed cards: %v", err)
	}

	// The hand plays to the river -- not terminal, so it is not saved.
	river := &table.HandState{
		TableID: "t", HandID: "128051400099", Street: table.StreetRiver, Pot: 80000,
		CommunityCards: board,
		Seats: []table.SeatState{
			{PlayerID: "steen", PlayerName: "steen", Stack: 5000, IsActive: true},
			{PlayerID: "jaffeth", PlayerName: "jaffeth", Stack: 5000, IsActive: true},
		},
	}
	if _, err := srv.IngestLiveState(river); err != nil {
		t.Fatalf("ingest river: %v", err)
	}
	if hands, _ := db.ListHandHistories(10); len(hands) != 0 {
		t.Fatalf("a non-terminal state was persisted: %d hands", len(hands))
	}

	// Cards are turned over: the showdown state ends the hand.
	showdown := &table.HandState{
		TableID: "t", HandID: "128051400099", Street: table.StreetShowdown, Pot: 80000,
		CommunityCards: board,
		Seats: []table.SeatState{
			{PlayerID: "steen", PlayerName: "steen", Stack: 5000, IsActive: true,
				Cards: []table.Card{shown[0], shown[1]}},
			{PlayerID: "jaffeth", PlayerName: "jaffeth", Stack: 5000, IsActive: true},
		},
	}
	if _, err := srv.IngestLiveState(showdown); err != nil {
		t.Fatalf("ingest showdown: %v", err)
	}

	hands, err := db.ListHandHistories(10)
	if err != nil {
		t.Fatalf("listing hand histories: %v", err)
	}
	if len(hands) != 1 {
		t.Fatalf("expected the showdown hand saved once, got %d", len(hands))
	}
	saved := hands[0]
	if saved.HandID != "128051400099" {
		t.Errorf("hand id: got %q, want the wire id 128051400099", saved.HandID)
	}
	if saved.Street != table.StreetShowdown {
		t.Errorf("street: got %q, want %q", saved.Street, table.StreetShowdown)
	}
	if len(saved.CommunityCards) != 5 {
		t.Errorf("board not carried into the showdown: %v", saved.CommunityCards)
	}
	var revealed int
	for _, s := range saved.Seats {
		if len(s.Cards) == 2 {
			revealed++
		}
	}
	if revealed != 1 {
		t.Errorf("expected one revealed hand recorded, got %d", revealed)
	}
}
