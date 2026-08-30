package vision

import (
	"image"
	_ "image/png"
	"os"
	"testing"
)

func TestAnalyzeSingleCrops(t *testing.T) {
	files := []string{
		"/tmp/poker_crops/hero_card_0.png",
		"/tmp/poker_crops/hero_card_1.png",
		"/tmp/poker_crops/board_card_0.png",
		"/tmp/poker_crops/board_card_1.png",
		"/tmp/poker_crops/board_card_2.png",
		"/tmp/poker_crops/board_card_3.png",
		"/tmp/poker_crops/board_card_4.png",
		"/tmp/poker_crops/pot.png",
	}

	ocr := NewTextOCR()
	matcher := NewDefaultCardMatcher()

	for _, p := range files {
		f, err := os.Open(p)
		if err != nil {
			continue
		}
		img, _, err := image.Decode(f)
		f.Close()
		if err != nil {
			continue
		}

		hasCard := hasCardFeatures(img)
		card, conf, matchErr := matcher.MatchCard(img)
		txt, _ := ocr.ParseString(img)
		num, _ := ocr.ParseNumber(img)

		t.Logf("[%s] hasCard=%v, Card=%v (conf=%.2f, err=%v), OCR txt=%q, num=%.2f", p, hasCard, card, conf, matchErr, txt, num)
	}
}
