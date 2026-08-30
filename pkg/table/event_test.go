package table

import "testing"

// The same table has to key the same way however text recognition mangled its
// title, or a warmed-up read is split across identities and starts again.
func TestTableKeyIsStableAcrossTitleNoise(t *testing.T) {
	same := []string{
		"NLH 1229111 - 1K/2K (320)",
		"NLH 1229111- 1K/2K (320)",
		"@ NLH 1229111 - 1K/2K (320)",
		"C NLH 1229111 - 1K/2K (320)",
		// The stake changing does not make it a different table.
		"NLH 1229111 - 2K/4K (640)",
		// Nor does a micro stake, where the digits around it are shorter still.
		"NLH 1229111 - 0.05/0.1 (0.01)",
	}
	want := TableKeyOf(same[0])
	if want != "1229111" {
		t.Fatalf("table key came out %q, want 1229111", want)
	}
	for _, title := range same[1:] {
		if got := TableKeyOf(title); got != want {
			t.Errorf("%q keyed as %q, want %q", title, got, want)
		}
	}

	// Different tables must stay different.
	if TableKeyOf("NLH 1228078 - 1K/2K (320)") == want {
		t.Error("two different tables share a key")
	}

	// Nothing to key on: the title is kept whole rather than collapsing every
	// such table into one.
	for _, odd := range []string{"coinpoker-live", "", "NLH"} {
		if got := TableKeyOf(odd); got != odd {
			t.Errorf("%q keyed as %q, want it kept whole", odd, got)
		}
	}
}
