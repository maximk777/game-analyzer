package capture

import (
	"image"
	"runtime"
	"testing"
)

func TestFilterPokerWindow(t *testing.T) {
	windows := []WindowInfo{
		{ID: 101, Title: "CoinPoker - NL Hold'em 0.25/0.50 Table 1", OwnerName: "CoinPoker", Bounds: image.Rect(100, 100, 900, 700), IsOnScreen: true},
		{ID: 102, Title: "Terminal", OwnerName: "Terminal", Bounds: image.Rect(0, 0, 800, 600), IsOnScreen: true},
		{ID: 103, Title: "Lobby", OwnerName: "CoinPoker", Bounds: image.Rect(50, 50, 400, 500), IsOnScreen: true},
		{ID: 104, Title: "CoinPoker - NLH 1/2 Table 2", OwnerName: "CoinPoker", Bounds: image.Rect(150, 150, 950, 750), IsOnScreen: false}, // off screen
	}

	// 1. Prioritize active table window over lobby
	target := FilterPokerWindow(windows, "CoinPoker")
	if target == nil || target.ID != 101 {
		t.Fatalf("expected to find window 101 (Hold'em table), got %+v", target)
	}

	// 2. Off screen table (104) should not be selected even if it matches "NLH"
	targetOffScreen := FilterPokerWindow([]WindowInfo{windows[3]}, "CoinPoker")
	if targetOffScreen != nil {
		t.Fatalf("expected nil for off-screen window, got %+v", targetOffScreen)
	}

	// 3. Fallback to generic window (e.g. Lobby) if no table keyword
	lobbyOnly := []WindowInfo{
		{ID: 103, Title: "Main Lobby", OwnerName: "CoinPoker", Bounds: image.Rect(50, 50, 400, 500), IsOnScreen: true},
	}
	targetLobby := FilterPokerWindow(lobbyOnly, "coinpoker")
	if targetLobby == nil || targetLobby.ID != 103 {
		t.Fatalf("expected fallback to window 103, got %+v", targetLobby)
	}

	// 4. Case insensitivity and keywords (pot / table / nlh)
	potWindow := []WindowInfo{
		{ID: 201, Title: "POT LIMIT OMAHA #4", OwnerName: "PokerApp", Bounds: image.Rect(0, 0, 800, 600), IsOnScreen: true},
	}
	targetPot := FilterPokerWindow(potWindow, "pokerapp")
	if targetPot == nil || targetPot.ID != 201 {
		t.Fatalf("expected to match pot limit window 201, got %+v", targetPot)
	}

	// 5. No match returns nil
	targetNone := FilterPokerWindow(windows, "NonExistentApp")
	if targetNone != nil {
		t.Fatalf("expected nil for non-matching query, got %+v", targetNone)
	}
}

func TestListAllWindows(t *testing.T) {
	if runtime.GOOS != "darwin" {
		_, err := ListAllWindows()
		if err == nil {
			t.Fatal("expected error on non-darwin OS")
		}
		return
	}

	windows, err := ListAllWindows()
	if err != nil {
		t.Logf("ListAllWindows returned error (may lack permissions in CI/headless): %v", err)
		return
	}
	t.Logf("Found %d windows on macOS", len(windows))
	for _, w := range windows {
		if w.ID == 0 {
			t.Errorf("expected non-zero window ID: %+v", w)
		}
	}
}
