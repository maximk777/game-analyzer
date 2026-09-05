package server

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"poker-game-analyzer/pkg/vision"
)

func serverWithLayoutAt(t *testing.T, path string) *Server {
	t.Helper()
	s := &Server{roiConfig: vision.DefaultCoinPoker6MaxROI(), roiPath: path}
	return s
}

// A calibration is done by hand against one client's layout, so it has to
// survive the process that took it.
func TestSetROIConfigKeepsTheCalibration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "roi.json")
	s := serverWithLayoutAt(t, path)

	cfg := vision.DefaultCoinPoker6MaxROI()
	cfg.Pot.X = 0.111
	if err := s.SetROIConfig(cfg); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("the calibration was not written: %v", err)
	}

	// A fresh process finds it.
	next := serverWithLayoutAt(t, path)
	gotPath, loaded, err := next.LoadROIConfig()
	if err != nil || !loaded {
		t.Fatalf("loaded %v, err %v", loaded, err)
	}
	if gotPath != path {
		t.Errorf("path %q", gotPath)
	}
	if next.GetROIConfig().Pot.X != 0.111 {
		t.Errorf("the layout that came back is not the one saved")
	}
}

// A layout that could not read a table is refused rather than accepted and
// discovered later as an empty board and nameless players.
func TestSetROIConfigRefusesAnUnusableLayout(t *testing.T) {
	s := serverWithLayoutAt(t, filepath.Join(t.TempDir(), "roi.json"))
	broken := vision.DefaultCoinPoker6MaxROI()
	broken.Seats = nil

	err := s.SetROIConfig(broken)
	if err == nil || !strings.Contains(err.Error(), "no seats") {
		t.Fatalf("error %v", err)
	}
	if len(s.GetROIConfig().Seats) == 0 {
		t.Error("the working layout was replaced by the refused one")
	}
}

// Nothing calibrated yet leaves the built-in layout in place and says so.
func TestLoadROIConfigWithNothingSaved(t *testing.T) {
	s := serverWithLayoutAt(t, filepath.Join(t.TempDir(), "roi.json"))
	_, loaded, err := s.LoadROIConfig()
	if err != nil || loaded {
		t.Fatalf("loaded %v, err %v", loaded, err)
	}
	if len(s.GetROIConfig().Seats) == 0 {
		t.Error("the built-in layout should still be there")
	}
}

// A machine that will not say where configuration lives still runs, it just
// does not keep the calibration.
func TestServerWithNowhereToKeepALayout(t *testing.T) {
	s := serverWithLayoutAt(t, "")
	if err := s.SetROIConfig(vision.DefaultCoinPoker6MaxROI()); err != nil {
		t.Fatalf("it should still apply: %v", err)
	}
	if _, loaded, err := s.LoadROIConfig(); err != nil || loaded {
		t.Fatalf("loaded %v, err %v", loaded, err)
	}
}

func TestLoadROIConfigReportsABrokenFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "roi.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	s := serverWithLayoutAt(t, path)
	if _, _, err := s.LoadROIConfig(); err == nil {
		t.Fatal("a file that is not a layout should be reported")
	}
	if len(s.GetROIConfig().Seats) == 0 {
		t.Error("the built-in layout should still be there")
	}
}
