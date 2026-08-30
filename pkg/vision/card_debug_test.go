package vision

import (
	"image"
	"image/draw"
	"image/png"
	_ "image/png"
	"os"
	"testing"
)

func TestDebugCardOCR(t *testing.T) {
	file, err := os.Open("../../testdata/coinpoker_live_sample.png")
	if err != nil {
		t.Skip("sample not found")
		return
	}
	defer file.Close()

	img, _, _ := image.Decode(file)
	cfg := DefaultCoinPoker6MaxROI()
	ocr := NewTextOCR()

	// Hero Card 0 (2♥)
	c0 := cfg.HeroCards[0].Crop(img)
	if c0 != nil {
		bounds := c0.Bounds()
		w, h := bounds.Dx(), bounds.Dy()
		rankRect := image.Rect(bounds.Min.X, bounds.Min.Y, bounds.Min.X+int(float64(w)*0.45), bounds.Min.Y+int(float64(h)*0.45))
		dst := image.NewRGBA(image.Rect(0, 0, rankRect.Dx(), rankRect.Dy()))
		draw.Draw(dst, dst.Bounds(), c0, rankRect.Min, draw.Src)

		f, _ := os.Create("/tmp/poker_crops/hero_0_rank_crop.png")
		_ = png.Encode(f, dst)
		f.Close()

		txt, _ := ocr.ParseString(dst)
		suit, conf := detectSuit(c0)
		t.Logf("Hero Card 0 (2h): Rank OCR txt=%q, Suit=%v (conf=%.2f)", txt, suit, conf)
	}

	// Community Card 0 (10s)
	b0 := cfg.CommunityCards[0].Crop(img)
	if b0 != nil {
		bounds := b0.Bounds()
		w, h := bounds.Dx(), bounds.Dy()
		rankRect := image.Rect(bounds.Min.X, bounds.Min.Y, bounds.Min.X+int(float64(w)*0.45), bounds.Min.Y+int(float64(h)*0.45))
		dst := image.NewRGBA(image.Rect(0, 0, rankRect.Dx(), rankRect.Dy()))
		draw.Draw(dst, dst.Bounds(), b0, rankRect.Min, draw.Src)

		f, _ := os.Create("/tmp/poker_crops/board_0_rank_crop.png")
		_ = png.Encode(f, dst)
		f.Close()

		txt, _ := ocr.ParseString(dst)
		suit, conf := detectSuit(b0)
		t.Logf("Board Card 0 (10s): Rank OCR txt=%q, Suit=%v (conf=%.2f)", txt, suit, conf)
	}
}
