package vision

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSaveAndLoadRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "roi.json")
	want := DefaultCoinPoker6MaxROI()

	if err := SaveROIConfig(path, want); err != nil {
		t.Fatal(err)
	}
	got, found, err := LoadROIConfig(path)
	if err != nil || !found {
		t.Fatalf("found %v, err %v", found, err)
	}
	if len(got.Seats) != len(want.Seats) || got.Pot != want.Pot {
		t.Errorf("layout did not survive the round trip")
	}
}

// Nothing calibrated yet is not a failure: the caller falls back to the
// built-in layout.
func TestLoadOnAMachineThatHasNotCalibrated(t *testing.T) {
	_, found, err := LoadROIConfig(filepath.Join(t.TempDir(), "roi.json"))
	if err != nil || found {
		t.Fatalf("found %v, err %v", found, err)
	}
}

func TestLoadRefusesWhatItCannotRead(t *testing.T) {
	dir := t.TempDir()
	broken := filepath.Join(dir, "broken.json")
	if err := os.WriteFile(broken, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := LoadROIConfig(broken); err == nil {
		t.Error("a file that is not a layout should be refused")
	}

	empty := filepath.Join(dir, "empty.json")
	if err := os.WriteFile(empty, []byte(`{"seats":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := LoadROIConfig(empty); err == nil {
		t.Error("a layout with no seats should be refused")
	}
}

// A crash mid-write must not leave half a calibration that loads as a whole one.
func TestSaveLeavesNoHalfFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "roi.json")
	if err := SaveROIConfig(path, DefaultCoinPoker6MaxROI()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path + ".tmp"); !os.IsNotExist(err) {
		t.Error("the temporary file was left behind")
	}
}

func TestValidateRefusesALayoutThatCouldNotReadATable(t *testing.T) {
	base := DefaultCoinPoker6MaxROI()

	cases := []struct {
		name string
		make func() ROIConfig
		want string
	}{
		{"no seats", func() ROIConfig { c := base; c.Seats = nil; return c }, "no seats"},
		{"pot with no area", func() ROIConfig {
			c := base
			c.Pot.Width = 0
			return c
		}, "no area"},
		{"card off the frame", func() ROIConfig {
			c := base
			c.CommunityCards[0].X = 0.98
			c.CommunityCards[0].Width = 0.1
			return c
		}, "past the edge"},
		{"negative origin", func() ROIConfig {
			c := base
			c.TimerBar.Y = -0.1
			return c
		}, "outside the frame"},
		{"seat defined twice", func() ROIConfig {
			c := base
			c.Seats = append(append([]SeatROI(nil), c.Seats...), c.Seats[1])
			return c
		}, "defined twice"},
		{"nameplate with no area", func() ROIConfig {
			c := base
			c.Seats = append([]SeatROI(nil), c.Seats...)
			c.Seats[1].Nameplate.Height = 0
			return c
		}, "nameplate"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := c.make().Validate()
			if err == nil {
				t.Fatal("want refusal")
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Errorf("reason %q, want it to mention %q", err, c.want)
			}
		})
	}
}

// Hero is how the tool knows which hand is its own.
func TestValidateWantsExactlyOneHero(t *testing.T) {
	base := DefaultCoinPoker6MaxROI()

	none := base
	none.Seats = append([]SeatROI(nil), base.Seats...)
	for i := range none.Seats {
		none.Seats[i].IsHero = false
	}
	if err := none.Validate(); err == nil || !strings.Contains(err.Error(), "0 seats as hero") {
		t.Errorf("error %v", err)
	}

	two := base
	two.Seats = append([]SeatROI(nil), base.Seats...)
	for i := range two.Seats {
		two.Seats[i].IsHero = true
	}
	if err := two.Validate(); err == nil {
		t.Error("two heroes is a layout that cannot say which hand is ours")
	}
}

func TestROIConfigPathHonoursTheEnvironment(t *testing.T) {
	t.Setenv(ROIConfigEnv, "/somewhere/roi.json")
	got, err := ROIConfigPath()
	if err != nil || got != "/somewhere/roi.json" {
		t.Fatalf("path %q, err %v", got, err)
	}

	t.Setenv(ROIConfigEnv, "")
	got, err = ROIConfigPath()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(got, filepath.Join("game-analyzer", "roi.json")) {
		t.Errorf("default path %q", got)
	}
}
