package vision

import (
	"image"
	"image/color"
	"image/draw"
	"math"
	"testing"
)

// Simple 5x7 bitmap font for digits 0-9 and chars '.', '$', 'k', 'P', 'o', 't', ':', ' ', 'F', 'C', 'R'
var testGlyphMap = map[rune][]string{
	'0': {
		" 111 ",
		"1   1",
		"1   1",
		"1   1",
		"1   1",
		"1   1",
		" 111 ",
	},
	'1': {
		"  11 ",
		" 1 1 ",
		"   1 ",
		"   1 ",
		"   1 ",
		"   1 ",
		" 1111",
	},
	'2': {
		" 111 ",
		"1   1",
		"    1",
		"  11 ",
		" 1   ",
		"1    ",
		"11111",
	},
	'3': {
		"1111 ",
		"    1",
		"    1",
		" 111 ",
		"    1",
		"    1",
		"1111 ",
	},
	'4': {
		"1  1 ",
		"1  1 ",
		"1  1 ",
		"11111",
		"   1 ",
		"   1 ",
		"   1 ",
	},
	'5': {
		"11111",
		"1    ",
		"1111 ",
		"    1",
		"    1",
		"1   1",
		" 111 ",
	},
	'6': {
		" 111 ",
		"1    ",
		"1111 ",
		"1   1",
		"1   1",
		"1   1",
		" 111 ",
	},
	'7': {
		"11111",
		"    1",
		"   1 ",
		"  1  ",
		" 1   ",
		" 1   ",
		" 1   ",
	},
	'8': {
		" 111 ",
		"1   1",
		"1   1",
		" 111 ",
		"1   1",
		"1   1",
		" 111 ",
	},
	'9': {
		" 111 ",
		"1   1",
		"1   1",
		" 1111",
		"    1",
		"   1 ",
		" 111 ",
	},
	',': {
		"     ",
		"     ",
		"     ",
		"     ",
		"  11 ",
		"  11 ",
		" 1   ",
	},
	'.': {
		"     ",
		"     ",
		"     ",
		"     ",
		"     ",
		" 111 ",
		" 111 ",
	},
	'$': {
		"  1  ",
		" 1111",
		"1 1  ",
		" 111 ",
		"  1 1",
		"1111 ",
		"  1  ",
	},
	'P': {
		"1111 ",
		"1   1",
		"1   1",
		"1111 ",
		"1    ",
		"1    ",
		"1    ",
	},
	'o': {
		"     ",
		"     ",
		" 111 ",
		"1   1",
		"1   1",
		"1   1",
		" 111 ",
	},
	't': {
		" 1   ",
		" 1   ",
		"1111 ",
		" 1   ",
		" 1   ",
		" 1  1",
		"  11 ",
	},
	':': {
		"     ",
		" 11  ",
		" 11  ",
		"     ",
		" 11  ",
		" 11  ",
		"     ",
	},
	'k': {
		"1    ",
		"1   1",
		"1  1 ",
		"111  ",
		"1  1 ",
		"1   1",
		"1   1",
	},
	' ': {
		"     ",
		"     ",
		"     ",
		"     ",
		"     ",
		"     ",
		"     ",
	},
}

func renderTextImage(text string, scale int) image.Image {
	charW, charH := 5*scale, 7*scale
	spacing := 2 * scale
	padding := 4 * scale

	totalW := padding*2 + len(text)*charW + (len(text)-1)*spacing
	totalH := padding*2 + charH

	img := image.NewRGBA(image.Rect(0, 0, totalW, totalH))
	// Black background
	draw.Draw(img, img.Bounds(), &image.Uniform{C: color.RGBA{R: 10, G: 10, B: 10, A: 255}}, image.Point{}, draw.Src)

	fg := color.RGBA{R: 250, G: 250, B: 250, A: 255}

	curX := padding
	for _, r := range text {
		pattern, ok := testGlyphMap[r]
		if !ok {
			pattern = testGlyphMap[' ']
		}
		for row := 0; row < 7; row++ {
			rowStr := pattern[row]
			for col := 0; col < 5; col++ {
				if col < len(rowStr) && rowStr[col] == '1' {
					for dy := 0; dy < scale; dy++ {
						for dx := 0; dx < scale; dx++ {
							img.Set(curX+col*scale+dx, padding+row*scale+dy, fg)
						}
					}
				}
			}
		}
		curX += charW + spacing
	}

	return img
}

func TestTextOCR_ParseNumber(t *testing.T) {
	ocr := NewTextOCR()

	testCases := []struct {
		input    string
		expected float64
	}{
		{"24.65", 24.65},
		{"0.85", 0.85},
		{"18.23", 18.23},
		{"$100.50", 100.50},
		{"Pot: 5.40", 5.40},
		{"148,772", 148772.0},
		{"80,000", 80000.0},
		{"7", 7.0},
		{"1.5k", 1500.0},
	}

	for _, tc := range testCases {
		t.Run(tc.input, func(t *testing.T) {
			img := renderTextImage(tc.input, 3)
			val, err := ocr.ParseNumber(img)
			if err != nil {
				t.Fatalf("unexpected error parsing number from %q: %v", tc.input, err)
			}
			if math.Abs(val-tc.expected) > 0.01 {
				t.Errorf("expected %f, got %f for %q", tc.expected, val, tc.input)
			}
		})
	}
}

func TestTextOCR_ParseString(t *testing.T) {
	ocr := NewTextOCR()

	testCases := []string{
		"Pot: 5.40",
		"24.65",
		"$100",
	}

	for _, text := range testCases {
		t.Run(text, func(t *testing.T) {
			img := renderTextImage(text, 3)
			parsed, err := ocr.ParseString(img)
			if err != nil {
				t.Fatalf("unexpected error parsing string %q: %v", text, err)
			}
			if parsed == "" {
				t.Errorf("expected non-empty parsed text for %q", text)
			}
		})
	}
}

func TestTextOCR_EmptyOrNil(t *testing.T) {
	ocr := NewTextOCR()

	_, err := ocr.ParseNumber(nil)
	if err == nil {
		t.Error("expected error for nil image in ParseNumber")
	}

	_, err = ocr.ParseString(nil)
	if err == nil {
		t.Error("expected error for nil image in ParseString")
	}
}
