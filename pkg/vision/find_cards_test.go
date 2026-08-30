package vision

import (
	"image"
	_ "image/png"
	"os"
	"testing"
)

func TestFindWhiteCardsInImage(t *testing.T) {
	f, err := os.Open("../../testdata/coinpoker_live_sample.png")
	if err != nil {
		t.Skip("sample not found")
		return
	}
	defer f.Close()

	img, _, err := image.Decode(f)
	if err != nil {
		t.Fatalf("decode failed: %v", err)
	}

	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	t.Logf("Full image size: %dx%d", w, h)

	// Scan in blocks of 50x50 to find high-density white regions (cards)
	blockW := 60
	blockH := 80
	for y := 0; y < h-blockH; y += 30 {
		for x := 0; x < w-blockW; x += 30 {
			whitePixels := 0
			for dy := 0; dy < blockH; dy++ {
				for dx := 0; dx < blockW; dx++ {
					r, g, b, _ := img.At(x+dx, y+dy).RGBA()
					rf := float64(r) / 65535.0
					gf := float64(g) / 65535.0
					bf := float64(b) / 65535.0
					if rf > 0.80 && gf > 0.80 && bf > 0.80 {
						whitePixels++
					}
				}
			}
			ratio := float64(whitePixels) / float64(blockW*blockH)
			if ratio > 0.60 {
				t.Logf("Found Card region at X=%d (%.3f), Y=%d (%.3f) with white ratio %.2f", x, float64(x)/float64(w), y, float64(y)/float64(h), ratio)
			}
		}
	}
}
