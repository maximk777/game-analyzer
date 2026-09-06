package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"poker-game-analyzer/pkg/capture"
	"poker-game-analyzer/pkg/table"
	"poker-game-analyzer/pkg/vision"
)

// Two readers of one table is not redundancy, it is two answers.
//
// Both used to run: the ScreenCaptureKit helper and the built-in ROI reader,
// posting to the same server, and the stabiliser merged whatever arrived.
// Live on 2026-09-06 the panel showed the right five nicknames beside a pot of
// 7.9 and stacks of 5, 0.6 and 55, at a table playing 1K/2K with two hundred
// thousand in front of every seat.
func TestOnlyOneReaderRunsAtATime(t *testing.T) {
	roiCfg := vision.DefaultCoinPoker6MaxROI()
	c1 := table.Card{Rank: table.RankAce, Suit: table.Spades}
	c2 := table.Card{Rank: table.RankKing, Suit: table.Hearts}
	frame := createSyntheticTableImage([2]table.Card{c1, c2}, []table.Card{}, 10.0, roiCfg)

	helper := filepath.Join(t.TempDir(), "helper.sh")
	script := "#!/bin/sh\nsleep 30\n"
	if err := os.WriteFile(helper, []byte(script), 0o755); err != nil {
		t.Fatalf("writing the stand-in helper: %v", err)
	}

	start := func(t *testing.T, helperPath string) *AgentApp {
		t.Helper()
		app, err := NewAgentApp(Config{
			WindowQuery:      "CoinPoker",
			Port:             getFreePort(t),
			FPS:              20,
			DBPath:           ":memory:",
			TableID:          "one-reader",
			HeroID:           "Hero",
			MockLLM:          true,
			WebDir:           "../../web",
			VisionHelperPath: helperPath,
		}, capture.NewMockGrabber(frame))
		if err != nil {
			t.Fatalf("NewAgentApp: %v", err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		t.Cleanup(cancel)
		if err := app.Start(ctx); err != nil {
			t.Fatalf("Start: %v", err)
		}
		t.Cleanup(func() { _ = app.Stop(context.Background()) })
		return app
	}

	t.Run("helper running means the ROI reader stays out", func(t *testing.T) {
		app := start(t, helper)
		if app.LiveAgent().IsRunning() {
			t.Fatal("both readers are running; their answers will be merged into one wrong table")
		}
	})

	t.Run("no helper means the ROI reader takes over", func(t *testing.T) {
		app := start(t, filepath.Join(t.TempDir(), "not-built"))
		if !app.LiveAgent().IsRunning() {
			t.Fatal("no helper and no ROI reader: nothing is reading the table")
		}
	})
}
