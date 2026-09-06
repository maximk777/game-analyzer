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

// A seat number is the identity a player keeps between frames. Two of them
// landing on one seat is not a cosmetic error: the later reading overwrites the
// earlier, so a name, a stack and an action all flip between two players ten
// times a second. Hero's own number used to alternate between 0 and 5 for the
// same reason, because the bottom seat sat exactly on the old sector boundary.
func TestSeatNumbersAreDistinctAndHeroSitsAtZero(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("Vision table analyser is macOS only")
	}
	if _, err := exec.LookPath("swiftc"); err != nil {
		t.Skip("swiftc not available")
	}

	pkgDir, err := filepath.Abs(".")
	if err != nil {
		t.Fatalf("resolving package dir: %v", err)
	}
	root := filepath.Dir(filepath.Dir(pkgDir))

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

	samples := []string{
		"coinpoker_live_sample.png",
		"coinpoker_hero_overlap_sample.png",
		"coinpoker_allin_seat_sample.png",
	}

	for _, name := range samples {
		t.Run(name, func(t *testing.T) {
			sample := filepath.Join(root, "testdata", name)
			if _, err := os.Stat(sample); err != nil {
				t.Skipf("testdata/%s not present", name)
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

			var got struct {
				HeroID string `json:"hero_id"`
				Seats  []struct {
					SeatNumber int    `json:"seat_number"`
					PlayerName string `json:"player_name"`
				} `json:"seats"`
			}
			if err := json.Unmarshal(out[start:], &got); err != nil {
				t.Fatalf("decoding parse_image output: %v\n%s", err, out)
			}
			if len(got.Seats) == 0 {
				t.Fatalf("no seats read from %s", name)
			}

			seen := map[int]string{}
			for _, s := range got.Seats {
				if other, dup := seen[s.SeatNumber]; dup {
					t.Errorf("seat %d holds both %q and %q", s.SeatNumber, other, s.PlayerName)
				}
				seen[s.SeatNumber] = s.PlayerName
				if s.SeatNumber < 0 || s.SeatNumber > 5 {
					t.Errorf("%q got seat %d, which is not a seat at a six-max table",
						s.PlayerName, s.SeatNumber)
				}
			}

			// Hero is the bottom-centre seat on every CoinPoker table, so hero
			// is seat 0 in every frame or the numbering means nothing.
			for _, s := range got.Seats {
				if s.PlayerName == got.HeroID && s.SeatNumber != 0 {
					t.Errorf("hero %q sits at seat %d, want 0", got.HeroID, s.SeatNumber)
				}
			}
		})
	}
}

// Hero is the bottom seat, and the client only draws an action strip for
// somebody who is in the hand. Reading the two small overlapping hole cards
// fails often; requiring them left hero unidentified on most frames of a live
// session, and an unidentified hero means no hero stack and no advice.
func TestHeroIsFoundFromTheActionStrip(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("Vision table analyser is macOS only")
	}
	if _, err := exec.LookPath("swiftc"); err != nil {
		t.Skip("swiftc not available")
	}

	pkgDir, err := filepath.Abs(".")
	if err != nil {
		t.Fatalf("resolving package dir: %v", err)
	}
	root := filepath.Dir(filepath.Dir(pkgDir))

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

	read := func(t *testing.T, name string) struct {
		HeroID  string   `json:"hero_id"`
		Buttons []string `json:"hero_buttons"`
		Seats   []struct {
			SeatNumber int    `json:"seat_number"`
			PlayerName string `json:"player_name"`
		} `json:"seats"`
	} {
		t.Helper()
		var got struct {
			HeroID  string   `json:"hero_id"`
			Buttons []string `json:"hero_buttons"`
			Seats   []struct {
				SeatNumber int    `json:"seat_number"`
				PlayerName string `json:"player_name"`
			} `json:"seats"`
		}
		sample := filepath.Join(root, "testdata", name)
		if _, err := os.Stat(sample); err != nil {
			t.Skipf("testdata/%s not present", name)
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
		if err := json.Unmarshal(out[start:], &got); err != nil {
			t.Fatalf("decoding parse_image output: %v\n%s", err, out)
		}
		return got
	}

	// Hero is holding cards on this frame and facing a call button.
	got := read(t, "coinpoker_allin_seat_sample.png")
	if got.HeroID == "Hero" {
		t.Fatalf("hero went unidentified with buttons %v on screen", got.Buttons)
	}
	for _, s := range got.Seats {
		if s.PlayerName == got.HeroID && s.SeatNumber != 0 {
			t.Errorf("hero %q sits at seat %d, want the bottom seat", got.HeroID, s.SeatNumber)
		}
	}

	// The lobby has no action strip and nobody at the bottom of a table.
	if lobby := read(t, "coinpoker_lobby_sample.png"); lobby.HeroID != "Hero" {
		t.Errorf("the lobby produced a hero: %q", lobby.HeroID)
	}
}
