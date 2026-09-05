package vision

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// ROIConfigEnv names the file the table layout is kept in, overriding the
// default location.
const ROIConfigEnv = "POKER_ROI_CONFIG"

// ROIConfigPath is where a calibration is kept unless the environment says
// otherwise.
//
// A calibration is worth keeping: it is done by hand against one client's
// layout, and until now it lived in the process that took it and died with it,
// which made the calibration screen a thing you could use but not benefit from.
func ROIConfigPath() (string, error) {
	if p := os.Getenv(ROIConfigEnv); p != "" {
		return p, nil
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "game-analyzer", "roi.json"), nil
}

// LoadROIConfig reads a saved calibration.
//
// A missing file is not a failure: it means nothing has been calibrated yet,
// and the caller falls back to the built-in layout.
func LoadROIConfig(path string) (cfg ROIConfig, found bool, err error) {
	data, err := os.ReadFile(path) //nolint:gosec // the path is the operator's own configuration
	if os.IsNotExist(err) {
		return ROIConfig{}, false, nil
	}
	if err != nil {
		return ROIConfig{}, false, err
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return ROIConfig{}, false, fmt.Errorf("vision: reading %s: %w", path, err)
	}
	if err := cfg.Validate(); err != nil {
		return ROIConfig{}, false, fmt.Errorf("vision: %s: %w", path, err)
	}
	return cfg, true, nil
}

// SaveROIConfig writes a calibration, refusing one that would read nothing.
func SaveROIConfig(path string, cfg ROIConfig) error {
	if err := cfg.Validate(); err != nil {
		return err
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return err
	}
	// Written beside and moved into place, so a crash mid-write cannot leave a
	// half a calibration that loads as a valid one.
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// Validate refuses a layout that could not read a table.
func (c ROIConfig) Validate() error {
	if len(c.Seats) == 0 {
		return fmt.Errorf("vision: the layout has no seats, so no player can be read")
	}
	named := map[string]RectF{
		"pot": c.Pot, "action buttons": c.ActionButtons, "timer bar": c.TimerBar,
	}
	for i, r := range c.HeroCards {
		named[fmt.Sprintf("hero card %d", i+1)] = r
	}
	for i, r := range c.CommunityCards {
		named[fmt.Sprintf("community card %d", i+1)] = r
	}
	for name, r := range named {
		if err := r.validate(); err != nil {
			return fmt.Errorf("vision: %s: %w", name, err)
		}
	}

	seen := make(map[int]bool, len(c.Seats))
	heroes := 0
	for _, s := range c.Seats {
		if seen[s.SeatNumber] {
			return fmt.Errorf("vision: seat %d is defined twice", s.SeatNumber)
		}
		seen[s.SeatNumber] = true
		if s.IsHero {
			heroes++
		}
		for name, r := range map[string]RectF{
			"nameplate": s.Nameplate, "stack": s.Stack, "bet": s.Bet, "avatar": s.Avatar,
		} {
			if err := r.validate(); err != nil {
				return fmt.Errorf("vision: seat %d %s: %w", s.SeatNumber, name, err)
			}
		}
	}
	// Hero is how the tool knows which hand is its own; two of them, or none,
	// is a layout that cannot say.
	if heroes != 1 {
		return fmt.Errorf("vision: the layout marks %d seats as hero, and exactly one is needed", heroes)
	}
	return nil
}

// validate refuses a rectangle that falls outside the frame or has no area.
func (r RectF) validate() error {
	switch {
	case r.Width <= 0 || r.Height <= 0:
		return fmt.Errorf("has no area")
	case r.X < 0 || r.Y < 0:
		return fmt.Errorf("starts outside the frame")
	case r.X+r.Width > 1.0001 || r.Y+r.Height > 1.0001:
		return fmt.Errorf("runs past the edge of the frame")
	}
	return nil
}
