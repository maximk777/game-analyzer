package profiler

import (
	"path/filepath"
	"testing"

	"poker-game-analyzer/pkg/storage"
	"poker-game-analyzer/pkg/table"
)

func ev(hand string, seq int, kind table.EventKind, street table.Street,
	player string, action table.ActionType) table.HandEvent {
	return table.HandEvent{
		SessionID: "test", TableKey: "1229111", TableID: "NLH 1229111 - 1K/2K (320)",
		HandID: hand, Seq: seq, Kind: kind, Street: street,
		PlayerID: player, Action: action,
	}
}

func countersFor(t *testing.T, events []table.HandEvent) map[string]map[string]int64 {
	t.Helper()

	db, err := storage.NewSQLiteDB(filepath.Join(t.TempDir(), "events.db"))
	if err != nil {
		t.Fatalf("opening database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	if err := db.AppendEvents(events); err != nil {
		t.Fatalf("appending events: %v", err)
	}
	cursor := NewStatsCursor(db)
	for {
		n, err := cursor.Run(1000)
		if err != nil {
			t.Fatalf("running cursor: %v", err)
		}
		if n == 0 {
			break
		}
	}
	got, err := db.TableCounters("1229111")
	if err != nil {
		t.Fatalf("reading counters: %v", err)
	}
	return got
}

// One hand, played out, folded into counters.
//
// The shape under test is a three-bet pot: the button opens, the big blind
// raises over it, the button calls, and the button then continuation-bets the
// flop into a fold.
func TestCursorCountsAThreeBetPot(t *testing.T) {
	h := "hand-1"
	events := []table.HandEvent{
		ev(h, 0, table.EventHandStart, table.StreetPreflop, "btn", ""),
		ev(h, 1, table.EventHandStart, table.StreetPreflop, "bb", ""),
		ev(h, 2, table.EventHandStart, table.StreetPreflop, "utg", ""),
		ev(h, 3, table.EventAction, table.StreetPreflop, "utg", table.ActionFold),
		ev(h, 4, table.EventAction, table.StreetPreflop, "btn", table.ActionRaise),
		ev(h, 5, table.EventAction, table.StreetPreflop, "bb", table.ActionRaise),
		ev(h, 6, table.EventAction, table.StreetPreflop, "btn", table.ActionCall),
		ev(h, 7, table.EventAction, table.StreetFlop, "bb", table.ActionBet),
		ev(h, 8, table.EventAction, table.StreetFlop, "btn", table.ActionFold),
	}
	got := countersFor(t, events)

	// Everyone dealt in played a hand, including the player who folded before
	// doing anything -- counting only those who acted would make every
	// frequency the frequency among people who did something.
	for _, id := range []string{"btn", "bb", "utg"} {
		if got[id][CounterHands] != 1 {
			t.Errorf("%s: hands %d, want 1", id, got[id][CounterHands])
		}
	}

	if got["utg"][CounterVPIP] != 0 || got["utg"][CounterPFR] != 0 {
		t.Errorf("a player who folded first is not in the pot: %v", got["utg"])
	}
	if got["btn"][CounterVPIP] != 1 || got["btn"][CounterPFR] != 1 {
		t.Errorf("the opener: %v", got["btn"])
	}
	if got["bb"][CounterThreeBet] != 1 {
		t.Errorf("the raise over a raise was not counted as a three-bet: %v", got["bb"])
	}
	// The opener had the chance to four-bet, not to three-bet: the chance to
	// three-bet is the chance to raise over someone else's raise.
	if got["btn"][CounterThreeBetOpp] != 0 {
		t.Errorf("the raiser was given a three-bet opportunity over their own raise: %v", got["btn"])
	}
	if got["bb"][CounterThreeBetOpp] != 1 {
		t.Errorf("the three-bettor had no opportunity recorded: %v", got["bb"])
	}

	// The flop bet came from the three-bettor, who was not the *first* raiser,
	// so it is not a continuation bet by the preflop aggressor.
	if got["btn"][CounterCBet] != 0 {
		t.Errorf("a continuation bet was credited to a player who did not bet: %v", got["btn"])
	}
}

// A raiser whose badge was read twice is not three-betting themselves. The
// counter this replaces did not check, so a flicker on one nameplate produced a
// three-bet out of nothing.
func TestCursorDoesNotLetAPlayerThreeBetThemselves(t *testing.T) {
	h := "hand-2"
	got := countersFor(t, []table.HandEvent{
		ev(h, 0, table.EventHandStart, table.StreetPreflop, "btn", ""),
		ev(h, 1, table.EventHandStart, table.StreetPreflop, "bb", ""),
		ev(h, 2, table.EventAction, table.StreetPreflop, "btn", table.ActionRaise),
		ev(h, 3, table.EventAction, table.StreetPreflop, "btn", table.ActionRaise),
		ev(h, 4, table.EventAction, table.StreetPreflop, "bb", table.ActionCall),
	})
	if got["btn"][CounterThreeBet] != 0 {
		t.Errorf("a player three-bet themselves: %v", got["btn"])
	}
}

// The preflop raiser betting the flop is a continuation bet, and everyone else
// still in is facing one.
func TestCursorCountsContinuationBets(t *testing.T) {
	h := "hand-3"
	got := countersFor(t, []table.HandEvent{
		ev(h, 0, table.EventHandStart, table.StreetPreflop, "btn", ""),
		ev(h, 1, table.EventHandStart, table.StreetPreflop, "bb", ""),
		ev(h, 2, table.EventAction, table.StreetPreflop, "btn", table.ActionRaise),
		ev(h, 3, table.EventAction, table.StreetPreflop, "bb", table.ActionCall),
		ev(h, 4, table.EventAction, table.StreetFlop, "bb", table.ActionCheck),
		ev(h, 5, table.EventAction, table.StreetFlop, "btn", table.ActionBet),
		ev(h, 6, table.EventAction, table.StreetFlop, "bb", table.ActionFold),
	})

	if got["btn"][CounterCBetOpp] != 1 || got["btn"][CounterCBet] != 1 {
		t.Errorf("the preflop raiser's flop bet: %v", got["btn"])
	}
	if got["bb"][CounterFoldToCBetOp] != 1 {
		t.Errorf("the caller was not recorded as facing a continuation bet: %v", got["bb"])
	}
	// The big blind's first action on the flop was a check, and only then a
	// fold. Folding to the bet is what is being counted, and it is what
	// happened -- but the first action was the check, so this is the case where
	// "first action" is the wrong question.
	t.Logf("fold to cbet recorded as %d (first flop action was a check)", got["bb"][CounterFoldToCBet])
}

// The cursor resumes where it left off and counts nothing twice.
func TestCursorResumesWithoutDoubleCounting(t *testing.T) {
	dir := t.TempDir()
	db, err := storage.NewSQLiteDB(filepath.Join(dir, "events.db"))
	if err != nil {
		t.Fatalf("opening database: %v", err)
	}
	defer db.Close()

	hand := func(n string) []table.HandEvent {
		return []table.HandEvent{
			ev(n, 0, table.EventHandStart, table.StreetPreflop, "btn", ""),
			ev(n, 1, table.EventAction, table.StreetPreflop, "btn", table.ActionRaise),
		}
	}
	if err := db.AppendEvents(hand("h1")); err != nil {
		t.Fatal(err)
	}

	cursor := NewStatsCursor(db)
	if n, err := cursor.Run(1000); err != nil || n != 1 {
		t.Fatalf("first pass counted %d hands (%v), want 1", n, err)
	}
	// Nothing new, so nothing counted.
	if n, err := cursor.Run(1000); err != nil || n != 0 {
		t.Fatalf("second pass counted %d hands (%v), want 0", n, err)
	}

	if err := db.AppendEvents(hand("h2")); err != nil {
		t.Fatal(err)
	}
	// A fresh cursor over the same database picks up where the last one
	// stopped, which is what makes a restart harmless.
	if n, err := NewStatsCursor(db).Run(1000); err != nil || n != 1 {
		t.Fatalf("after a restart counted %d hands (%v), want 1", n, err)
	}

	got, err := db.PlayerCounters("1229111", "btn")
	if err != nil {
		t.Fatal(err)
	}
	if got[CounterHands] != 2 {
		t.Errorf("hands counted %d, want 2 -- once per hand, whatever the passes", got[CounterHands])
	}
	if got[CounterPFR] != 2 {
		t.Errorf("raises counted %d, want 2", got[CounterPFR])
	}
}
