package sim

import "testing"

// The number that says whether a run could ever have seen what it was
// measuring: two candidates that played alike contribute nothing to the paired
// difference, so the effective sample is the hands they disagreed on.
func TestDivergenceCountsOnlyHandsThatWentDifferently(t *testing.T) {
	base := &Result{Nets: []float64{1, -2, 3, -4, 5}}
	same := &Result{Nets: []float64{1, -2, 3, -4, 5}}
	some := &Result{Nets: []float64{1, -2, 7, -4, 9}}

	if share, n, ok := same.Divergence(base); !ok || n != 0 || share != 0 {
		t.Fatalf("identical results diverged: share %.3f, n %d, ok %v", share, n, ok)
	}
	share, n, ok := some.Divergence(base)
	if !ok || n != 2 {
		t.Fatalf("n = %d, ok = %v, want 2 and true", n, ok)
	}
	if share != 0.4 {
		t.Fatalf("share = %.3f, want 0.400", share)
	}
}

// Runs of different lengths are not comparable hand by hand, and saying so is
// better than lining them up and pretending.
func TestDivergenceRefusesMismatchedRuns(t *testing.T) {
	if _, _, ok := (&Result{Nets: []float64{1, 2}}).Divergence(&Result{Nets: []float64{1}}); ok {
		t.Fatal("compared runs of different lengths")
	}
	if _, _, ok := (&Result{}).Divergence(nil); ok {
		t.Fatal("compared against nothing")
	}
}
