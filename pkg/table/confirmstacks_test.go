package table

import "testing"

func stacked(number int, stack float64) SeatState {
	return SeatState{SeatNumber: number, PlayerID: "p", PlayerName: "p", IsActive: true, Stack: stack}
}

// A stack that reads differently on every frame is being misread, not spent.
func TestConfirmStacksHoldsAOneFrameMisread(t *testing.T) {
	s := &StateStabilizer{}
	prev := []SeatState{stacked(1, 5.18)}

	got := s.confirmStacks(prev, []SeatState{stacked(1, 740.01)})
	if got[0].Stack != 5.18 {
		t.Fatalf("a single odd reading should not reach the panel: %.2f", got[0].Stack)
	}

	// The same odd reading again is a real change, not a misreading.
	got = s.confirmStacks(prev, []SeatState{stacked(1, 740.01)})
	if got[0].Stack != 740.01 {
		t.Fatalf("a reading seen twice is believed: %.2f", got[0].Stack)
	}
}

// Numbers that jump to a different value every frame never settle, so the
// settled one is what the panel keeps showing.
func TestConfirmStacksSurvivesAStreamOfNoise(t *testing.T) {
	s := &StateStabilizer{}
	prev := []SeatState{stacked(1, 5.18)}
	for _, noise := range []float64{740.01, 1500, 3.24, 912.7, 0.02} {
		got := s.confirmStacks(prev, []SeatState{stacked(1, noise)})
		if got[0].Stack != 5.18 {
			t.Fatalf("noise %.2f reached the panel", noise)
		}
	}
}

// The first figure read for a seat is what there is: there is nothing settled
// to hold on to.
func TestConfirmStacksTakesTheFirstReading(t *testing.T) {
	s := &StateStabilizer{}
	got := s.confirmStacks([]SeatState{stacked(1, 0)}, []SeatState{stacked(1, 5.18)})
	if got[0].Stack != 5.18 {
		t.Fatalf("the first reading should land: %.2f", got[0].Stack)
	}
}

// A stack that does not move must not be held up by the confirmation.
func TestConfirmStacksPassesAnUnchangedStack(t *testing.T) {
	s := &StateStabilizer{}
	prev := []SeatState{stacked(1, 5.18)}
	for range 5 {
		got := s.confirmStacks(prev, []SeatState{stacked(1, 5.18)})
		if got[0].Stack != 5.18 {
			t.Fatalf("stack %.2f", got[0].Stack)
		}
	}
}

// Money moving is a real change and has to arrive, one frame late.
func TestConfirmStacksLetsARealChangeThrough(t *testing.T) {
	s := &StateStabilizer{}
	prev := []SeatState{stacked(1, 5.18)}
	if got := s.confirmStacks(prev, []SeatState{stacked(1, 4.18)}); got[0].Stack != 5.18 {
		t.Fatalf("the first frame of a change waits: %.2f", got[0].Stack)
	}
	if got := s.confirmStacks(prev, []SeatState{stacked(1, 4.18)}); got[0].Stack != 4.18 {
		t.Fatalf("the second frame confirms it: %.2f", got[0].Stack)
	}
}

// The last cent is not always read the same way twice.
func TestStacksAgreeWithinACent(t *testing.T) {
	if !stacksAgree(5.18, 5.18) || !stacksAgree(5.18, 5.185) {
		t.Error("the same figure should agree with itself")
	}
	if stacksAgree(5.18, 5.30) || stacksAgree(0, 5.18) {
		t.Error("different figures must not agree")
	}
}
