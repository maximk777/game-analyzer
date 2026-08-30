package vision

import (
	"image"
	_ "image/png"
	"os"
	"testing"
)

func TestCoinPokerLiveSample(t *testing.T) {
	file, err := os.Open("../../testdata/coinpoker_live_sample.png")
	if err != nil {
		t.Skip("testdata/coinpoker_live_sample.png not found, skipping live sample test")
		return
	}
	defer file.Close()

	img, _, err := image.Decode(file)
	if err != nil {
		t.Fatalf("failed to decode live sample PNG: %v", err)
	}

	bounds := img.Bounds()
	t.Logf("Live sample bounds: %v (Dx: %d, Dy: %d)", bounds, bounds.Dx(), bounds.Dy())

	cfg := DefaultCoinPoker6MaxROI()
	parser := NewFrameParser(nil, nil)

	state, err := parser.ParseFrame(img, cfg)
	if err != nil {
		t.Fatalf("ParseFrame failed on live sample: %v", err)
	}

	t.Logf("Parsed Table State: %+v", state)
	t.Logf("Hero Cards: %v %v", state.HeroCards[0], state.HeroCards[1])
	t.Logf("Community Cards (%d): %v", len(state.CommunityCards), state.CommunityCards)
	t.Logf("Pot: %.2f, CurrentBet: %.2f", state.Pot, state.CurrentBet)
	for _, seat := range state.Seats {
		t.Logf("Seat %d: Player=%s (ID=%s), Stack=%.2f, Bet=%.2f", seat.SeatNumber, seat.PlayerName, seat.PlayerID, seat.Stack, seat.CurrentBet)
	}
}
