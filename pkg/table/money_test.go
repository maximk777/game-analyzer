package table

import (
	"encoding/json"
	"testing"
)

func TestMoneyParsesWithoutGoingThroughFloat(t *testing.T) {
	cases := []struct {
		in   string
		want Money
	}{
		{"0", 0},
		{"0.1", 1000},
		{"0.10", 1000},
		{"0.05", 500},
		{"0.01", 100},
		{"1", 10000},
		{"2000", 20000000},
		{"1229111", 12291110000},
		{"-0.05", -500},
		{".5", 5000},
		// More places than are kept round to the nearest unit rather than down.
		{"0.00005", 1},
		{"0.00004", 0},
	}
	for _, c := range cases {
		got, err := ParseMoney(c.in)
		if err != nil {
			t.Errorf("ParseMoney(%q): %v", c.in, err)
			continue
		}
		if got != c.want {
			t.Errorf("ParseMoney(%q) = %d, want %d", c.in, got, c.want)
		}
	}

	for _, bad := range []string{"", "abc", "1.2.3", "0.1a"} {
		if _, err := ParseMoney(bad); err == nil {
			t.Errorf("ParseMoney(%q) accepted a non-amount", bad)
		}
	}
}

// The whole point of the type: amounts that float64 cannot hold exactly are
// held exactly, and they still add up to what a player would count.
func TestMoneyAddsExactly(t *testing.T) {
	tenth, _ := ParseMoney("0.1")
	fifth, _ := ParseMoney("0.2")
	if got, want := tenth.Add(fifth).String(), "0.3"; got != want {
		t.Errorf("0.1 + 0.2 = %s, want %s", got, want)
	}

	// A big blind of 0.1 posted thirty times is exactly three, not 2.9999...
	total := Zero
	for i := 0; i < 30; i++ {
		total = total.Add(tenth)
	}
	if got, want := total.String(), "3"; got != want {
		t.Errorf("thirty big blinds of 0.1 came to %s, want %s", got, want)
	}
	if total.Float() != 3.0 {
		t.Errorf("as a float that is %v, want 3", total.Float())
	}
}

func TestMoneyRoundTripsThroughJSON(t *testing.T) {
	for _, s := range []string{"0", "0.1", "0.05", "2000", "1229111", "-0.25"} {
		want, err := ParseMoney(s)
		if err != nil {
			t.Fatalf("ParseMoney(%q): %v", s, err)
		}
		blob, err := json.Marshal(want)
		if err != nil {
			t.Fatalf("marshalling %s: %v", s, err)
		}
		var got Money
		if err := json.Unmarshal(blob, &got); err != nil {
			t.Fatalf("unmarshalling %s: %v", blob, err)
		}
		if got != want {
			t.Errorf("%s round-tripped to %s (json %s)", s, got, blob)
		}
	}

	// Whatever a screen reader or another tool sends: a bare number, a quoted
	// one, null, or an exponent.
	for _, blob := range []string{`0.1`, `"0.1"`, `null`, `1e2`} {
		var got Money
		if err := json.Unmarshal([]byte(blob), &got); err != nil {
			t.Errorf("unmarshalling %s: %v", blob, err)
		}
	}
}

func TestMoneyScaleRoundsToAPayableAmount(t *testing.T) {
	pot, _ := ParseMoney("0.15")
	if got, want := pot.Scale(2.0/3.0).String(), "0.1"; got != want {
		t.Errorf("two thirds of 0.15 = %s, want %s", got, want)
	}
	big, _ := ParseMoney("70560")
	if got, want := big.Scale(0.66).String(), "46569.6"; got != want {
		t.Errorf("66%% of 70560 = %s, want %s", got, want)
	}
}
