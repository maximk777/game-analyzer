package advice

import (
	"testing"

	"poker-game-analyzer/pkg/table"
)

// A real-money regular we have never seen: the site's month of hands is the
// whole read, converted to the percentages the tendency map states.
func TestSiteReadFillsAnEmptyProfile(t *testing.T) {
	site := &table.SiteStats{
		VPIP: 0.2669, PFR: 0.2107, ThreeBet: 0.0961,
		FoldToThreeBet: 0.5627, FoldToCBet: 0.3147, Pool: "cash",
	}
	got := WithSiteRead(nil, site)

	if got["vpip"] < 26.6 || got["vpip"] > 26.8 {
		t.Errorf("vpip = %v, want ~26.7 (percent)", got["vpip"])
	}
	if got["pfr"] < 21 || got["pfr"] > 21.2 {
		t.Errorf("pfr = %v, want ~21.1 (percent)", got["pfr"])
	}
	if got["fold_to_cbet"] != 0.3147 {
		t.Errorf("fold_to_cbet = %v, want the fraction as sent", got["fold_to_cbet"])
	}
	// No count behind it, so it is weighted as an opinion rather than a fact.
	if got["modelled"] != 1 {
		t.Errorf("modelled = %v, want 1 when the pool reports no sample", got["modelled"])
	}
	if got["site"] != 1 {
		t.Error("read does not say it came from the site")
	}
	if _, ok := got["hands_count"]; ok {
		t.Errorf("hands_count = %v, want absent: the pool stated none", got["hands_count"])
	}
}

// Our own counted sample wins: it holds the spots the site does not compute --
// how this player answers our raises.
func TestOurOwnSampleWinsOverTheSite(t *testing.T) {
	own := map[string]float64{
		"vpip": 18, "pfr": 14, "three_bet": 5, "hands_count": 240,
		"fold_to_cbet": 0.71, "fold_to_cbet_n": 31,
	}
	site := &table.SiteStats{VPIP: 0.40, PFR: 0.33, FoldToCBet: 0.30}
	got := WithSiteRead(own, site)

	if got["vpip"] != 18 || got["pfr"] != 14 {
		t.Errorf("preflop read = %v/%v, want our own 18/14", got["vpip"], got["pfr"])
	}
	if got["fold_to_cbet"] != 0.71 {
		t.Errorf("fold_to_cbet = %v, want our counted 0.71", got["fold_to_cbet"])
	}
	if got["modelled"] != 0 {
		t.Error("a counted read must not be marked as an opinion")
	}
}

// A sample of four hands is noise; the site's month describes the same player
// better, and the fold frequency the language model guessed at is replaced by
// one somebody measured.
func TestThinSampleYieldsToTheSite(t *testing.T) {
	own := map[string]float64{
		"vpip": 75, "pfr": 0, "three_bet": 0, "hands_count": 4,
		"fold_to_3bet": 0.80, "modelled": 1, // the model's opinion, uncounted
	}
	site := &table.SiteStats{VPIP: 0.22, PFR: 0.18, ThreeBet: 0.07, FoldToThreeBet: 0.56}
	got := WithSiteRead(own, site)

	if got["vpip"] != 22 {
		t.Errorf("vpip = %v, want the site's 22 over a four-hand 75", got["vpip"])
	}
	if got["fold_to_3bet"] != 0.56 {
		t.Errorf("fold_to_3bet = %v, want the site's measurement over the model's guess", got["fold_to_3bet"])
	}
}

// The play-money pool states its sample, and then it is a counted read: the
// count is carried and the opinion flag is not set.
func TestReportedSampleIsCarried(t *testing.T) {
	got := WithSiteRead(nil, &table.SiteStats{VPIP: 0.27, PFR: 0.05, Hands: 139, Pool: "f2p"})
	if got["hands_count"] != 139 {
		t.Errorf("hands_count = %v, want 139", got["hands_count"])
	}
	if got["modelled"] != 0 {
		t.Error("a stated sample should not be marked as an opinion")
	}
}

func TestWithSiteReadWithoutSiteIsWhatWeHad(t *testing.T) {
	own := map[string]float64{"vpip": 20, "hands_count": 100}
	got := WithSiteRead(own, nil)
	if len(got) != 2 || got["vpip"] != 20 {
		t.Errorf("read = %v, want our own unchanged", got)
	}
	got["vpip"] = 99
	if own["vpip"] != 20 {
		t.Error("the caller's map was modified")
	}
}

func TestRangeWidthPrecedence(t *testing.T) {
	site := &table.SiteStats{VPIP: 0.33}
	cases := []struct {
		name        string
		ownVPIP     float64
		ownHands    int
		site        *table.SiteStats
		sessionVPIP float64
		want        float64
		known       bool
	}{
		{"our own real sample", 21, 300, site, 44, 21, true},
		{"our own thin sample yields to the site", 80, 3, site, 44, 33, true},
		{"no site read, thin sample is still a read", 80, 3, nil, 44, 80, true},
		{"nothing of ours, the wire's session figure", 0, 0, nil, 44, 44, true},
		{"nobody knows", 0, 0, nil, 0, 0, false},
	}
	for _, c := range cases {
		got, ok := RangeWidthVPIP(c.ownVPIP, c.ownHands, c.site, c.sessionVPIP)
		if ok != c.known || (ok && got != c.want) {
			t.Errorf("%s: got %v,%v want %v,%v", c.name, got, ok, c.want, c.known)
		}
	}
}
