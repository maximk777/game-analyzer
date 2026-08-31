package capture

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"testing"
)

// The Swift table analyser is where card recognition actually lives, so the
// only meaningful regression test for it runs the real thing against a real
// captured frame. Building and running it costs a few seconds; that is worth
// paying, because every card bug so far has been a geometry or thresholding
// mistake that no Go-level unit test could have caught.

type parsedSample struct {
	CommunityCards []string `json:"community_cards"`
	HeroCards      []string `json:"hero_cards"`
	Pot            float64  `json:"pot"`
	Street         string   `json:"street"`
	HeroID         string   `json:"hero_id"`
	MinRaise       float64  `json:"min_raise"`
	Seats          []struct {
		PlayerName string  `json:"player_name"`
		Stack      float64 `json:"stack"`
		Position   string  `json:"position"`
		IsFolded   bool    `json:"is_folded"`
		LastAction string  `json:"last_action"`
	} `json:"seats"`
}

func TestVisionSample_CoinPokerLiveFrame(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("Vision/ScreenCaptureKit table analyser is macOS only")
	}
	if _, err := exec.LookPath("swiftc"); err != nil {
		t.Skip("swiftc not available")
	}

	// Tests run with the package directory as the working directory.
	pkgDir, err := filepath.Abs(".")
	if err != nil {
		t.Fatalf("resolving package dir: %v", err)
	}
	root := filepath.Dir(filepath.Dir(pkgDir))

	sample := filepath.Join(root, "testdata", "coinpoker_live_sample.png")
	if _, err := os.Stat(sample); err != nil {
		t.Skip("testdata/coinpoker_live_sample.png not present")
	}

	bin := filepath.Join(t.TempDir(), "parse_image")
	build := exec.Command("swiftc", "-parse-as-library",
		filepath.Join(pkgDir, "table_vision.swift"),
		filepath.Join(pkgDir, "card_templates.swift"),
		filepath.Join(pkgDir, "rank_bitmap_templates.swift"),
		filepath.Join(pkgDir, "parse_image_tool.swift"),
		"-o", bin)
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("building the Swift table analyser failed: %v\n%s", err, out)
	}

	// Card references are built from assets extracted out of the client
	// bundle. They live under bin/ and are not in the repository, so the test
	// points at them when they exist and exercises the text-recognition
	// fallback when they do not.
	run := exec.Command(bin, sample)
	run.Env = append(os.Environ(),
		"POKER_RTA_ASSETS="+filepath.Join(root, "bin", "assets", "coinpoker"))
	out, err := run.CombinedOutput()
	if err != nil {
		t.Fatalf("parse_image failed: %v\n%s", err, out)
	}

	// The tool prints an "image: WxH" banner before the JSON body.
	start := slices.Index(out, '{')
	if start < 0 {
		t.Fatalf("no JSON in parse_image output:\n%s", out)
	}

	var got parsedSample
	if err := json.Unmarshal(out[start:], &got); err != nil {
		t.Fatalf("decoding parse_image output: %v\n%s", err, out)
	}

	// Ground truth read off the screenshot by eye: board 10s 10d 2s 7s As,
	// hero Qh Qd, pot 70,560 on the river.
	wantBoard := []string{"10s", "10d", "2s", "7s", "As"}
	wantHero := []string{"Qh", "Qd"}

	if !slices.Equal(got.CommunityCards, wantBoard) {
		t.Errorf("board cards: got %v, want %v", got.CommunityCards, wantBoard)
	}
	if !slices.Equal(got.HeroCards, wantHero) {
		t.Errorf("hero cards: got %v, want %v", got.HeroCards, wantHero)
	}
	if got.Pot != 70560 {
		t.Errorf("pot: got %.0f, want 70560", got.Pot)
	}
	if got.Street != "river" {
		t.Errorf("street: got %q, want %q", got.Street, "river")
	}

	// Hero is the bottom seat, not a constant. Before this, hero_id was always
	// the literal "Hero", matched no seat, and hero's own stack was never read.
	if got.HeroID != "exemplary766180" {
		t.Errorf("hero id: got %q, want %q", got.HeroID, "exemplary766180")
	}

	// Only real nameplates. A recorded session turned "ALL-IN", "Play Next
	// Game", "84.44%" and "1K/2K" into players, putting seven of them on a
	// six-max table and inflating the opponent count the EV maths depends on.
	if len(got.Seats) != 4 {
		names := make([]string, len(got.Seats))
		for i, s := range got.Seats {
			names[i] = s.PlayerName
		}
		t.Errorf("expected 4 seated players, got %d: %v", len(got.Seats), names)
	}
	for _, s := range got.Seats {
		if s.Stack <= 0 {
			t.Errorf("seat %q has no stack, so it is interface text, not a player", s.PlayerName)
		}
	}

	// The amount is read off the action button; the sample shows "Bet 2,000".
	if got.MinRaise != 2000 {
		t.Errorf("min raise from the action button: got %.0f, want 2000", got.MinRaise)
	}

	byName := map[string]int{}
	for i, s := range got.Seats {
		byName[s.PlayerName] = i
	}

	// Positions come from the dealer button, located by colour and shape. Every
	// range-based decision needs them: with no position there is no equity
	// realisation and no preflop chart.
	wantPositions := map[string]string{
		"AngryWhteSam...": "BTN",
		"exemplary766180": "SB",
		"cluttered603261": "BB",
		"healthy25562109": "UTG",
	}
	for name, want := range wantPositions {
		i, ok := byName[name]
		if !ok {
			t.Errorf("seat %q missing, cannot check position", name)
			continue
		}
		if got.Seats[i].Position != want {
			t.Errorf("position of %q: got %q, want %q", name, got.Seats[i].Position, want)
		}
	}

	// The action badge printed on a nameplate is the only observable record of
	// what a player did. A folded player who still counts as live inflates the
	// opponent count the EV maths depends on.
	if i, ok := byName["AngryWhteSam..."]; ok {
		if !got.Seats[i].IsFolded || got.Seats[i].LastAction != "fold" {
			t.Errorf("the FOLD badge was not read: folded=%v action=%q",
				got.Seats[i].IsFolded, got.Seats[i].LastAction)
		}
	}
}

// A second real frame, kept because it caught a failure the first one could
// not: hero's two cards overlap, the corner pip of the left one runs to the
// edge of its index crop, and a filter meant to exclude the large centre pip
// discarded it -- leaving one row where two are needed, so the card went
// unread with rank and pip both plainly visible.
func TestVisionSample_OverlappingHeroCards(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("Vision/ScreenCaptureKit table analyser is macOS only")
	}
	if _, err := exec.LookPath("swiftc"); err != nil {
		t.Skip("swiftc not available")
	}

	pkgDir, err := filepath.Abs(".")
	if err != nil {
		t.Fatalf("resolving package dir: %v", err)
	}
	root := filepath.Dir(filepath.Dir(pkgDir))

	sample := filepath.Join(root, "testdata", "coinpoker_hero_overlap_sample.png")
	if _, err := os.Stat(sample); err != nil {
		t.Skip("testdata/coinpoker_hero_overlap_sample.png not present")
	}

	bin := filepath.Join(t.TempDir(), "parse_image")
	build := exec.Command("swiftc", "-parse-as-library",
		filepath.Join(pkgDir, "table_vision.swift"),
		filepath.Join(pkgDir, "card_templates.swift"),
		filepath.Join(pkgDir, "rank_bitmap_templates.swift"),
		filepath.Join(pkgDir, "parse_image_tool.swift"),
		"-o", bin)
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("building the Swift table analyser failed: %v\n%s", err, out)
	}

	run := exec.Command(bin, sample)
	run.Env = append(os.Environ(),
		"POKER_RTA_ASSETS="+filepath.Join(root, "bin", "assets", "coinpoker"))
	out, err := run.CombinedOutput()
	if err != nil {
		t.Fatalf("parse_image failed: %v\n%s", err, out)
	}

	start := slices.Index(out, '{')
	if start < 0 {
		t.Fatalf("no JSON in parse_image output:\n%s", out)
	}
	var got parsedSample
	if err := json.Unmarshal(out[start:], &got); err != nil {
		t.Fatalf("decoding parse_image output: %v\n%s", err, out)
	}

	want := []string{"6d", "3d"}
	if !slices.Equal(got.HeroCards, want) {
		t.Errorf("hero cards: got %v, want %v", got.HeroCards, want)
	}
}

// An all-in opponent is the seat the reader is most likely to lose, and the one
// it can least afford to.
//
// Their stack is rendered as a lone "0", which the recogniser does not return
// at this size, and a nameplate with no number under it was not counted as a
// seat. So the player who had just put their whole stack in vanished from the
// table: the largest bet the tool could see was the big blind, and on 2026-09-01
// hero held 3h2d in the big blind facing an all-in of 1,420,000 and was advised
// to check, "nothing to pay", while the client's button read Call 181,840.
//
// The frame is that hand. It also carries the wager itself, which came back
// from the recogniser as "1.42MR" -- one stray character against the chip
// graphic, enough to make the biggest bet on the table parse as no number.
func TestVisionSample_AllInSeatSurvives(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("Vision/ScreenCaptureKit table analyser is macOS only")
	}
	if _, err := exec.LookPath("swiftc"); err != nil {
		t.Skip("swiftc not available")
	}

	pkgDir, err := filepath.Abs(".")
	if err != nil {
		t.Fatalf("resolving package dir: %v", err)
	}
	root := filepath.Dir(filepath.Dir(pkgDir))

	sample := filepath.Join(root, "testdata", "coinpoker_allin_seat_sample.png")
	if _, err := os.Stat(sample); err != nil {
		t.Skip("testdata/coinpoker_allin_seat_sample.png not present")
	}

	bin := filepath.Join(t.TempDir(), "parse_image")
	build := exec.Command("swiftc", "-parse-as-library",
		filepath.Join(pkgDir, "table_vision.swift"),
		filepath.Join(pkgDir, "card_templates.swift"),
		filepath.Join(pkgDir, "rank_bitmap_templates.swift"),
		filepath.Join(pkgDir, "parse_image_tool.swift"),
		"-o", bin)
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("building the Swift table analyser failed: %v\n%s", err, out)
	}

	// The stake is supplied the way the system supplies it live, from the
	// window title. Without it there is no scale, and wagers are not read at
	// all -- which is correct, and is why the title is passed rather than a
	// floor invented.
	run := exec.Command(bin, sample, "--title", "NLH 1237978 - 1K/2K (320)")
	run.Env = append(os.Environ(),
		"POKER_RTA_ASSETS="+filepath.Join(root, "bin", "assets", "coinpoker"))
	out, err := run.CombinedOutput()
	if err != nil {
		t.Fatalf("parse_image failed: %v\n%s", err, out)
	}

	start := slices.Index(out, '{')
	if start < 0 {
		t.Fatalf("no JSON in parse_image output:\n%s", out)
	}

	var got struct {
		parsedSample
		CurrentBet float64 `json:"current_bet"`
		BigBlind   float64 `json:"big_blind"`
		Seats      []struct {
			PlayerID   string  `json:"player_id"`
			Stack      float64 `json:"stack"`
			CurrentBet float64 `json:"current_bet"`
			LastAction string  `json:"last_action"`
		} `json:"seats"`
	}
	if err := json.Unmarshal(out[start:], &got); err != nil {
		t.Fatalf("decoding parse_image output: %v\n%s", err, out)
	}

	var allIn *struct {
		PlayerID   string  `json:"player_id"`
		Stack      float64 `json:"stack"`
		CurrentBet float64 `json:"current_bet"`
		LastAction string  `json:"last_action"`
	}
	for i := range got.Seats {
		if got.Seats[i].PlayerID == "mike8989" {
			allIn = &got.Seats[i]
		}
	}
	if allIn == nil {
		var names []string
		for _, s := range got.Seats {
			names = append(names, s.PlayerID)
		}
		t.Fatalf("the all-in player is not a seat; got %v", names)
	}
	if allIn.LastAction != "all-in" {
		t.Errorf("all-in badge: got %q, want %q", allIn.LastAction, "all-in")
	}
	if allIn.CurrentBet != 1420000 {
		t.Errorf("all-in wager: got %.0f, want 1420000", allIn.CurrentBet)
	}

	// The number that decides the hand. Hero has 2,000 posted in the big blind
	// against this, so anything short of it means the advice is formed on a
	// price that does not exist.
	if got.CurrentBet != 1420000 {
		t.Errorf("current bet: got %.0f, want 1420000", got.CurrentBet)
	}
	if got.BigBlind != 2000 {
		t.Errorf("big blind: got %.0f, want 2000 from the title", got.BigBlind)
	}
	if got.HeroID != "ludoStarik" {
		t.Errorf("hero id: got %q, want %q", got.HeroID, "ludoStarik")
	}
}
