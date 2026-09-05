package table

import "testing"

// A new pot has to be read twice before it is believed, so a single wild
// misreading cannot stick. A frame that read no pot at all is neither a
// confirmation nor a contradiction, and it used to count as the latter: nine
// frames in a hundred come back blank, and each one reset the count, so a pot
// could sit at the blinds for a whole hand while the felt filled up.
func TestPotCandidateSurvivesABlankFrame(t *testing.T) {
	st := NewStateStabilizer()

	frame := func(pot float64) *HandState {
		return &HandState{TableID: "t", Street: StreetPreflop, Pot: pot}
	}

	if got := st.Stabilize(frame(0.02)).Pot; got != 0.02 {
		t.Fatalf("first pot: got %.2f, want 0.02", got)
	}
	if got := st.Stabilize(frame(0.22)).Pot; got != 0.02 {
		t.Fatalf("a pot seen once is not yet believed: got %.2f", got)
	}
	if got := st.Stabilize(frame(0)).Pot; got != 0.02 {
		t.Fatalf("a blank frame must not raise the pot: got %.2f", got)
	}
	if got := st.Stabilize(frame(0.22)).Pot; got != 0.22 {
		t.Fatalf("the blank frame threw away the candidate: got %.2f, want 0.22", got)
	}
}

// Two different wild readings in a row still confirm nothing.
func TestPotIgnoresAStreamOfDifferentSpikes(t *testing.T) {
	st := NewStateStabilizer()
	st.Stabilize(&HandState{TableID: "t", Street: StreetPreflop, Pot: 0.02})

	for _, spike := range []float64{401.92, 88.5, 12.75, 401.93} {
		got := st.Stabilize(&HandState{TableID: "t", Street: StreetPreflop, Pot: spike}).Pot
		if got != 0.02 {
			t.Fatalf("spike %.2f reached the panel as %.2f", spike, got)
		}
	}
}
