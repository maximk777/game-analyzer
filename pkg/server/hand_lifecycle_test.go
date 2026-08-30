package server

import (
	"testing"

	"poker-game-analyzer/pkg/storage"
	"poker-game-analyzer/pkg/table"
	"poker-game-analyzer/pkg/vision"
)

// End to end over the live path: the vision layer never sends a showdown, so
// this is the only route by which a hand can reach the database. Before the
// stabiliser owned the hand lifecycle, a full session left hand_histories and
// player_stats both empty.
func TestProcessEvent_PersistsHandsWithoutAShowdown(t *testing.T) {
	db, err := storage.NewSQLiteDB(":memory:")
	if err != nil {
		t.Fatalf("opening database: %v", err)
	}
	defer db.Close()

	srv := NewServer(storage.NewMemoryCache(), db, nil)

	board, err := table.ParseCards("10c 8s 2c")
	if err != nil {
		t.Fatalf("parsing board: %v", err)
	}

	// A hand plays out, exactly as the vision layer reports it: no showdown,
	// hand id always the placeholder.
	flop := &table.HandState{
		TableID: "t", HandID: "live-hand", Street: table.StreetFlop, Pot: 4280,
		CommunityCards: board,
		Seats: []table.SeatState{
			{PlayerID: "villain", PlayerName: "villain", Stack: 5000, IsActive: true},
		},
	}
	if _, err := srv.ProcessEvent(vision.VisionEvent{TableID: "t", HandState: flop}); err != nil {
		t.Fatalf("ProcessEvent flop: %v", err)
	}

	// The next hand begins: board cleared, pot reset.
	next := &table.HandState{
		TableID: "t", HandID: "live-hand", Street: table.StreetPreflop, Pot: 300,
		Seats: []table.SeatState{
			{PlayerID: "villain", PlayerName: "villain", Stack: 5000, IsActive: true},
		},
	}
	if _, err := srv.ProcessEvent(vision.VisionEvent{TableID: "t", HandState: next}); err != nil {
		t.Fatalf("ProcessEvent next hand: %v", err)
	}

	hands, err := db.ListHandHistories(10)
	if err != nil {
		t.Fatalf("listing hand histories: %v", err)
	}
	if len(hands) != 1 {
		t.Fatalf("expected the finished hand to be saved, got %d hands", len(hands))
	}
	if hands[0].Pot != 4280 {
		t.Errorf("saved the wrong hand: pot %.0f, want 4280", hands[0].Pot)
	}
	if hands[0].HandID == "" || hands[0].HandID == "live-hand" {
		t.Errorf("hand saved under the placeholder id %q, so the next hand would overwrite it", hands[0].HandID)
	}
}

// A player who has folded has no decision left. The fold badge on the nameplate
// is read, but nothing consulted it before advising: live, hero had folded and
// the HUD went on recommending an all-in, sized off another player's stack.
func TestProcessEvent_NoAdviceAfterHeroFolds(t *testing.T) {
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
			HeroCards:      [2]table.Card{hero[0], hero[1]},
			Seats: []table.SeatState{
				{PlayerID: "hero", PlayerName: "hero", Stack: 153200, IsActive: true, IsFolded: folded},
				{PlayerID: "villain", PlayerName: "villain", Stack: 301607, IsActive: true},
			},
		}
	}

	rec, err := srv.ProcessEvent(vision.VisionEvent{TableID: "t", HandState: state(false)})
	if err != nil {
		t.Fatalf("ProcessEvent while live: %v", err)
	}
	if rec == nil {
		t.Fatal("expected advice while hero is still in the hand")
	}

	rec, err = srv.ProcessEvent(vision.VisionEvent{TableID: "t", HandState: state(true)})
	if err != nil {
		t.Fatalf("ProcessEvent after folding: %v", err)
	}
	if rec != nil {
		t.Errorf("advised %s %.0f after hero folded", rec.PrimaryAction, rec.RecommendedAmount)
	}
}

// A hand that actually reaches a showdown must be saved like any other: with a
// minted id, and with the board and seats it accumulated over the hand. It used
// to bypass the stabiliser entirely, so it kept the vision placeholder id and
// every showdown hand overwrote the same row.
func TestProcessEvent_ShowdownHandIsSavedWithMintedID(t *testing.T) {
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

	// The hand plays to the river.
	river := &table.HandState{
		TableID: "t", HandID: "live-hand", Street: table.StreetRiver, Pot: 80000,
		CommunityCards: board,
		Seats: []table.SeatState{
			{PlayerID: "steen", PlayerName: "steen", Stack: 5000, IsActive: true},
			{PlayerID: "jaffeth", PlayerName: "jaffeth", Stack: 5000, IsActive: true},
		},
	}
	if _, err := srv.ProcessEvent(vision.VisionEvent{TableID: "t", HandState: river}); err != nil {
		t.Fatalf("ProcessEvent river: %v", err)
	}

	// Cards are turned over.
	showdown := &table.HandState{
		TableID: "t", HandID: "live-hand", Street: table.StreetShowdown, Pot: 80000,
		CommunityCards: board,
		Seats: []table.SeatState{
			{PlayerID: "steen", PlayerName: "steen", Stack: 5000, IsActive: true,
				Cards: []table.Card{shown[0], shown[1]}},
			{PlayerID: "jaffeth", PlayerName: "jaffeth", Stack: 5000, IsActive: true},
		},
	}
	if _, err := srv.ProcessEvent(vision.VisionEvent{TableID: "t", HandState: showdown}); err != nil {
		t.Fatalf("ProcessEvent showdown: %v", err)
	}

	hands, err := db.ListHandHistories(10)
	if err != nil {
		t.Fatalf("listing hand histories: %v", err)
	}
	if len(hands) != 1 {
		t.Fatalf("expected the showdown hand to be saved once, got %d", len(hands))
	}
	saved := hands[0]

	if saved.HandID == "" || saved.HandID == "live-hand" {
		t.Errorf("showdown hand saved under the placeholder id %q", saved.HandID)
	}
	if saved.Street != table.StreetShowdown {
		t.Errorf("street: got %q, want %q", saved.Street, table.StreetShowdown)
	}
	if len(saved.CommunityCards) != 5 {
		t.Errorf("the board was not carried into the showdown: %v", saved.CommunityCards)
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
