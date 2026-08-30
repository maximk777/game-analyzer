package vision

import (
	"image"
	_ "image/png"
	"os"
	"testing"
)

func TestInspectCropPixels(t *testing.T) {
	f, err := os.Open("/tmp/poker_crops/hero_card_0.png")
	if err != nil {
		t.Skip("not found")
		return
	}
	defer f.Close()

	img, _, _ := image.Decode(f)
	b := img.Bounds()
	t.Logf("hero_card_0 bounds: %v", b)

	var avgR, avgG, avgB float64
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			r, g, b, _ := img.At(x, y).RGBA()
			avgR += float64(r) / 65535.0
			avgG += float64(g) / 65535.0
			avgB += float64(b) / 65535.0
		}
	}
	n := float64(b.Dx() * b.Dy())
	t.Logf("Avg RGB: R=%.3f, G=%.3f, B=%.3f", avgR/n, avgG/n, avgB/n)
}
