package advice

import (
	"maps"

	"poker-game-analyzer/pkg/table"
)

// What the site has counted about a player, and where it stands relative to what
// we have counted ourselves.
//
// The tool's own read is worth more per hand: it holds the spots nobody else
// records -- how often this player folded to *our* raise, on *this* street. But
// for the first hour at a table it does not exist, and a read that arrives after
// two hundred hands is not a read for the session being played. The site's own
// aggregate covers a month of every table the player sat at, and its client will
// hand it over on request (pkg/coinpoker), so the opening hands can be played
// with a read instead of against a stranger.
//
// So they are layered rather than chosen between: ours on top where we have a
// sample, the site's underneath, and nothing invented where neither has one.

// ThinSample is the number of hands below which our own frequencies are noise
// rather than a read. It matches the prior the advisor shrinks reads towards --
// at this many hands a counted tendency has earned half the weight it can ever
// have -- so below it the site's month of hands is the better description of
// the same player.
const ThinSample = 25

// WithSiteRead layers the site's aggregate under what we counted ourselves.
//
// Ours wins wherever we have it: a counted fold frequency (which the site does
// not compute for our lines) and preflop frequencies once the sample is past
// ThinSample. The site fills the rest -- including, notably, the two fold
// frequencies the language model used to be asked to guess at.
//
// The returned map is new; neither input is modified.
func WithSiteRead(own map[string]float64, site *table.SiteStats) map[string]float64 {
	out := make(map[string]float64, len(own)+8)
	maps.Copy(out, own)
	if site == nil {
		return out
	}

	// Preflop frequencies, as percentages, the way every other tendency in this
	// map states them.
	ownHands := own["hands_count"]
	if site.VPIP > 0 && (ownHands < ThinSample || out["vpip"] == 0) {
		out["vpip"] = site.VPIP * 100
		if site.PFR > 0 || out["pfr"] == 0 {
			out["pfr"] = site.PFR * 100
		}
		if site.ThreeBet > 0 || out["three_bet"] == 0 {
			out["three_bet"] = site.ThreeBet * 100
		}
	}

	// Fold frequencies, as fractions. Ours count only where we have the sample
	// behind them -- the _n keys -- so a value the language model supplied is
	// replaced by one the site measured.
	if site.FoldToThreeBet > 0 && own["fold_to_3bet_n"] == 0 {
		out["fold_to_3bet"] = site.FoldToThreeBet
	}
	if site.FoldToCBet > 0 && own["fold_to_cbet_n"] == 0 {
		out["fold_to_cbet"] = site.FoldToCBet
	}

	// The sample, when the site reports one. The real-money pool does not, and
	// nothing may be inferred from that absence: a read with no count behind it
	// is weighted as an opinion rather than as a fact (see the "modelled" flag
	// in pkg/advisor), which is what a frequency measured elsewhere over a
	// sample nobody stated is.
	if site.Hands > 0 && float64(site.Hands) > ownHands {
		out["hands_count"] = float64(site.Hands)
	} else if out["hands_count"] == 0 {
		out["modelled"] = 1
	}
	out["site"] = 1
	return out
}

// RangeWidthVPIP is the share of hands a player enters with, for building their
// range: the best-founded VPIP available for them, as a percentage.
//
// In order: our own, once we have a real sample of it; the site's month-long
// aggregate; our own thin sample; and the site's per-session figure off the wire,
// which is a handful of hands but is at least this table. False means nobody
// knows, and a hundred -- every hand -- is what the caller must then assume.
func RangeWidthVPIP(ownVPIP float64, ownHands int, site *table.SiteStats, sessionVPIP float64) (float64, bool) {
	if ownVPIP > 0 && ownHands >= ThinSample {
		return ownVPIP, true
	}
	if site != nil && site.VPIP > 0 {
		return site.VPIP * 100, true
	}
	if ownVPIP > 0 {
		return ownVPIP, true
	}
	if sessionVPIP > 0 {
		return sessionVPIP, true
	}
	return 0, false
}
