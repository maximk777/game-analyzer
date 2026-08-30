package vision

import (
	"image"
	"image/color"
	"image/draw"
	"testing"

	"poker-game-analyzer/pkg/table"
)

// Helper to draw a synthetic card image for testing.
func createTestCardImage(card table.Card, width, height int) image.Image {
	return generateSyntheticCard(card, width, height)
}

func TestCardMatcher_MatchCard(t *testing.T) {
	matcher := NewCardMatcher()

	testCards := []table.Card{
		{Rank: table.RankAce, Suit: table.Spades},
		{Rank: table.RankKing, Suit: table.Hearts},
		{Rank: table.RankQueen, Suit: table.Diamonds},
		{Rank: table.RankJack, Suit: table.Clubs},
		{Rank: table.RankTen, Suit: table.Spades},
		{Rank: table.RankNine, Suit: table.Hearts},
		{Rank: table.RankTwo, Suit: table.Clubs},
	}

	for _, tc := range testCards {
		t.Run(tc.String(), func(t *testing.T) {
			img := createTestCardImage(tc, 40, 60)
			// Register template or test matcher
			matcher.RegisterTemplate(tc, img)

			matched, conf, err := matcher.MatchCard(img)
			if err != nil {
				t.Fatalf("unexpected error matching card %s: %v", tc, err)
			}
			if matched != tc {
				t.Errorf("expected card %s, got %s", tc, matched)
			}
			if conf < 0.8 {
				t.Errorf("expected confidence >= 0.8, got %f", conf)
			}
		})
	}
}

func TestCardMatcher_NoCard(t *testing.T) {
	matcher := NewCardMatcher()

	// Empty green table background image
	emptyImg := image.NewRGBA(image.Rect(0, 0, 40, 60))
	draw.Draw(emptyImg, emptyImg.Bounds(), &image.Uniform{C: color.RGBA{R: 35, G: 80, B: 35, A: 255}}, image.Point{}, draw.Src)

	card, conf, err := matcher.MatchCard(emptyImg)
	if err == nil && conf > 0.5 {
		t.Errorf("expected error or low confidence for blank slot, got card %s with conf %f", card, conf)
	}
}

func TestCardMatcher_DefaultTemplates(t *testing.T) {
	matcher := NewDefaultCardMatcher()
	card, conf, err := matcher.MatchCard(nil)
	if err == nil {
		t.Fatalf("expected error on nil image, got card %s with conf %f", card, conf)
	}
}
