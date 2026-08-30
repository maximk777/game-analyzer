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
