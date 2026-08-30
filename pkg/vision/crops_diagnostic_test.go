package vision

import (
	"fmt"
	"image"
	"image/png"
	_ "image/png"
	"os"
	"testing"
)

func TestSaveCropsDiagnostic(t *testing.T) {
	file, err := os.Open("../../testdata/coinpoker_live_sample.png")
	if err != nil {
		t.Skip("sample image not found")
		return
	}
	defer file.Close()

	img, _, err := image.Decode(file)
	if err != nil {
		t.Fatalf("decode failed: %v", err)
	}

	_ = os.MkdirAll("/tmp/poker_crops", 0755)

	cfg := DefaultCoinPoker6MaxROI()

	// 1. Save Hero Cards crops
	for i, r := range cfg.HeroCards {
		c := r.Crop(img)
		if c != nil {
			f, _ := os.Create(fmt.Sprintf("/tmp/poker_crops/hero_card_%d.png", i))
			_ = png.Encode(f, c)
			f.Close()
		}
	}

	// 2. Save Community Cards crops
	for i, r := range cfg.CommunityCards {
		c := r.Crop(img)
		if c != nil {
			f, _ := os.Create(fmt.Sprintf("/tmp/poker_crops/board_card_%d.png", i))
			_ = png.Encode(f, c)
			f.Close()
		}
	}

	// 3. Save Pot crop
	if c := cfg.Pot.Crop(img); c != nil {
		f, _ := os.Create("/tmp/poker_crops/pot.png")
		_ = png.Encode(f, c)
		f.Close()
	}

	// 4. Save Seats crops
	for i, s := range cfg.Seats {
		if c := s.Nameplate.Crop(img); c != nil {
			f, _ := os.Create(fmt.Sprintf("/tmp/poker_crops/seat_%d_name.png", i))
			_ = png.Encode(f, c)
			f.Close()
		}
		if c := s.Stack.Crop(img); c != nil {
			f, _ := os.Create(fmt.Sprintf("/tmp/poker_crops/seat_%d_stack.png", i))
			_ = png.Encode(f, c)
			f.Close()
		}
	}

	t.Log("Saved crops to /tmp/poker_crops/")
}
