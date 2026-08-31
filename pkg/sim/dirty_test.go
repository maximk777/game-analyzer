package sim

import (
	"math/rand"
	"reflect"
	"testing"

	"poker-game-analyzer/pkg/table"
)

// cleanState is a table as the engine holds it: hero named and seated, every
// seat with a position, one seat per player.
func cleanState() table.HandState {
	return table.HandState{
		HandID:   "h1",
		TableID:  "t1",
		Street:   table.StreetPreflop,
		Pot:      4920,
		MinRaise: 4000,
		HeroID:   "hero",
		Seats: []table.SeatState{
			{SeatNumber: 0, PlayerID: "hero", Stack: 67940, Position: table.PosCO},
			{SeatNumber: 1, PlayerID: "villain", Stack: 199680, CurrentBet: 2000, Position: table.PosBTN},
			{SeatNumber: 2, PlayerID: "third", Stack: 80000, Position: table.PosBB},
		},
	}
}

func always(d Defect) Noise {
	var n Noise
	switch d {
	case DefectButtonLost:
		n.ButtonLost = 1
	case DefectHeroUnnamed:
		n.HeroUnnamed = 1
	case DefectGhostSeats:
		n.GhostSeats = 1
	case DefectStaleBet:
		n.StaleBet = 1
	case DefectPotJitter:
		n.PotJitter = 1
	case DefectMinRaiseLost:
		n.MinRaiseLost = 1
	case DefectReadsLost:
		n.ReadsLost = 1
	}
	return n
}

// The zero value has to be inert, because every harness run that predates this
// file leaves Noise unset and must keep reporting what it reported.
func TestZeroNoiseChangesNothing(t *testing.T) {
	in := cleanState()
	out, fired := Noise{}.Corrupt(in, rand.New(rand.NewSource(1)))
	if len(fired) != 0 {
		t.Fatalf("zero noise fired %v", fired)
	}
	if out.HeroID != in.HeroID || out.Pot != in.Pot || out.MinRaise != in.MinRaise || len(out.Seats) != len(in.Seats) {
		t.Fatalf("zero noise altered the state: %+v", out)
	}
	for i := range out.Seats {
		if !reflect.DeepEqual(out.Seats[i], in.Seats[i]) {
			t.Fatalf("seat %d changed: %+v -> %+v", i, in.Seats[i], out.Seats[i])
		}
	}
}

// The engine's copy is the truth the hand plays out on. Corrupting the view
// must not reach it, or the noise would be changing the game rather than the
// picture of it.
func TestCorruptDoesNotTouchTheCaller(t *testing.T) {
	in := cleanState()
	before := append([]table.SeatState(nil), in.Seats...)
	n := LiveNoise()
	n.GhostSeats, n.StaleBet, n.ButtonLost = 1, 1, 1
	for seed := int64(0); seed < 50; seed++ {
		n.Corrupt(in, rand.New(rand.NewSource(seed)))
	}
	if len(in.Seats) != len(before) {
		t.Fatalf("caller's seats grew to %d", len(in.Seats))
	}
	for i := range before {
		if !reflect.DeepEqual(in.Seats[i], before[i]) {
			t.Fatalf("caller's seat %d mutated: %+v -> %+v", i, before[i], in.Seats[i])
		}
	}
}

func TestEachDefectDoesItsOwnDamage(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	in := cleanState()

	t.Run("button lost blanks every position", func(t *testing.T) {
		out, fired := always(DefectButtonLost).Corrupt(in, rng)
		if len(fired) != 1 || fired[0] != DefectButtonLost {
			t.Fatalf("fired %v", fired)
		}
		for _, s := range out.Seats {
			if s.Position != "" {
				t.Fatalf("seat %s kept position %q", s.PlayerID, s.Position)
			}
		}
	})

	t.Run("hero unnamed matches no seat", func(t *testing.T) {
		out, _ := always(DefectHeroUnnamed).Corrupt(in, rng)
		for _, s := range out.Seats {
			if s.PlayerID == out.HeroID {
				t.Fatalf("hero id %q still matches a seat", out.HeroID)
			}
		}
	})

	t.Run("ghosts add live opponents", func(t *testing.T) {
		out, _ := always(DefectGhostSeats).Corrupt(in, rng)
		if len(out.Seats) <= len(in.Seats) {
			t.Fatalf("no ghost appeared: %d seats", len(out.Seats))
		}
		// A ghost has to carry an id nobody has seen, or the tracker would hand
		// it the read belonging to the player it was copied from and it would
		// cost nothing.
		seen := map[string]bool{}
		for _, s := range in.Seats {
			seen[s.PlayerID] = true
		}
		ghosts := 0
		for _, s := range out.Seats {
			if !seen[s.PlayerID] {
				ghosts++
			}
		}
		if ghosts == 0 {
			t.Fatal("every ghost reused an existing id")
		}
	})

	t.Run("stale bet invents something to call", func(t *testing.T) {
		state := cleanState()
		state.Seats[1].CurrentBet = 0 // nobody has bet; the action is checked round
		out, _ := always(DefectStaleBet).Corrupt(state, rng)
		var maxBet float64
		for _, s := range out.Seats {
			if s.CurrentBet > maxBet {
				maxBet = s.CurrentBet
			}
		}
		if maxBet <= 0 {
			t.Fatal("no stale bet appeared on a checked-round table")
		}
	})

	t.Run("min raise lost", func(t *testing.T) {
		out, _ := always(DefectMinRaiseLost).Corrupt(in, rng)
		if out.MinRaise != 0 {
			t.Fatalf("MinRaise = %v, want 0", out.MinRaise)
		}
	})

	t.Run("pot jitter takes a number off the screen", func(t *testing.T) {
		out, _ := always(DefectPotJitter).Corrupt(in, rng)
		if out.Pot == in.Pot {
			t.Fatal("pot unchanged")
		}
		// The wrong number has to be one that was actually on the table, which
		// is the difference between this and a random multiplier.
		onScreen := false
		for _, s := range in.Seats {
			if s.Stack == out.Pot {
				onScreen = true
			}
		}
		if !onScreen {
			t.Fatalf("pot became %v, which is on no nameplate", out.Pot)
		}
	})

	t.Run("reads lost is reported but not written into the state", func(t *testing.T) {
		out, fired := always(DefectReadsLost).Corrupt(in, rng)
		if len(fired) != 1 || fired[0] != DefectReadsLost {
			t.Fatalf("fired %v", fired)
		}
		if out.Pot != in.Pot || out.HeroID != in.HeroID || len(out.Seats) != len(in.Seats) {
			t.Fatal("reads_lost altered the state; it is the caller's to apply")
		}
	})
}

// The button is the defect that matters most, and it matters through a chain
// that runs out of this package: no position, so preflop.HeroPosition fails, so
// the chart has no opinion, so the preflop decision falls to a comparison that
// folds pocket pairs getting a price. This pins the first link, which is the
// one this package owns.
func TestButtonLostIsWhatCostsTheChartItsOpinion(t *testing.T) {
	in := cleanState()
	out, _ := always(DefectButtonLost).Corrupt(in, rand.New(rand.NewSource(3)))
	for _, s := range out.Seats {
		if s.PlayerID != out.HeroID {
			continue
		}
		if s.Position != "" {
			t.Fatalf("hero kept position %q, so the chart would still answer", s.Position)
		}
		return
	}
	t.Fatal("hero vanished from the seats, which is a different defect")
}

func TestOnlyAndWithoutArePairs(t *testing.T) {
	live := LiveNoise()
	for _, d := range AllDefects {
		only, without := OnlyNoise(d), WithoutNoise(d)
		if only.rate(d) != live.rate(d) {
			t.Errorf("OnlyNoise(%s) rate %v, want the live %v", d, only.rate(d), live.rate(d))
		}
		if without.rate(d) != 0 {
			t.Errorf("WithoutNoise(%s) still has rate %v", d, without.rate(d))
		}
		for _, other := range AllDefects {
			if other == d {
				continue
			}
			if only.rate(other) != 0 {
				t.Errorf("OnlyNoise(%s) also carries %s", d, other)
			}
			if without.rate(other) != live.rate(other) {
				t.Errorf("WithoutNoise(%s) changed %s", d, other)
			}
		}
	}
}

// Every defect must be reachable from LiveNoise, or a preset would be quietly
// measuring nothing.
func TestLiveNoiseCoversEveryDefect(t *testing.T) {
	live := LiveNoise()
	for _, d := range AllDefects {
		if live.rate(d) <= 0 {
			t.Errorf("%s has no live rate", d)
		}
	}
	if !live.Any() {
		t.Fatal("LiveNoise reports itself inert")
	}
}

func TestFlipReportCountsWhatItSays(t *testing.T) {
	var f FlipReport
	// A fold turned into an all-in: a flip, and a reversal.
	f.record(table.ActionFold, table.ActionAllIn, 0, 5000, []Defect{DefectButtonLost})
	// A raise turned into a check: a flip, not a reversal -- giving up pressure
	// without surrendering the hand.
	f.record(table.ActionRaise, table.ActionCheck, 3000, 0, []Defect{DefectGhostSeats})
	// Same action, twice the size: a sizing flip.
	f.record(table.ActionRaise, table.ActionRaise, 3000, 7000, []Defect{DefectMinRaiseLost})
	// Same action, same size: nothing, and no defect credited.
	f.record(table.ActionCall, table.ActionCall, 2000, 2000, []Defect{DefectPotJitter})

	if f.Decisions != 4 {
		t.Errorf("Decisions = %d, want 4", f.Decisions)
	}
	if f.ActionFlips != 2 {
		t.Errorf("ActionFlips = %d, want 2", f.ActionFlips)
	}
	if f.ReversedFlips != 1 {
		t.Errorf("ReversedFlips = %d, want 1", f.ReversedFlips)
	}
	if f.SizingFlips != 1 {
		t.Errorf("SizingFlips = %d, want 1", f.SizingFlips)
	}
	if f.ByDefect[DefectPotJitter] != 0 {
		t.Error("an unchanged decision credited its defect")
	}
	if f.ByDefect[DefectButtonLost] != 1 || f.ByDefect[DefectMinRaiseLost] != 1 {
		t.Errorf("attribution wrong: %v", f.ByDefect)
	}
	if got := f.ActionFlipRate(); got != 0.5 {
		t.Errorf("ActionFlipRate = %v, want 0.5", got)
	}
}

func TestFlipReportMergeIsAddition(t *testing.T) {
	var a, b FlipReport
	a.record(table.ActionFold, table.ActionRaise, 0, 100, []Defect{DefectButtonLost})
	b.record(table.ActionFold, table.ActionRaise, 0, 100, []Defect{DefectButtonLost})
	b.record(table.ActionCall, table.ActionCall, 10, 10, nil)
	a.merge(b)
	if a.Decisions != 3 || a.ActionFlips != 2 || a.ByDefect[DefectButtonLost] != 2 {
		t.Fatalf("merge lost counts: %+v", a)
	}
}

// Noise draws from the tool's own stream, so the same seed has to produce the
// same corruption. Without that a run is not replayable and no paired
// comparison means anything.
func TestCorruptIsDeterministic(t *testing.T) {
	in := cleanState()
	n := LiveNoise()
	a, firedA := n.Corrupt(in, rand.New(rand.NewSource(42)))
	b, firedB := n.Corrupt(in, rand.New(rand.NewSource(42)))
	if len(firedA) != len(firedB) {
		t.Fatalf("same seed fired %v then %v", firedA, firedB)
	}
	for i := range firedA {
		if firedA[i] != firedB[i] {
			t.Fatalf("same seed fired %v then %v", firedA, firedB)
		}
	}
	if a.Pot != b.Pot || a.HeroID != b.HeroID || len(a.Seats) != len(b.Seats) {
		t.Fatal("same seed produced different states")
	}
}
