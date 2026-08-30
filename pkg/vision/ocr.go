package vision

import (
	"errors"
	"image"
	"math"
	"strconv"
	"strings"
	"unicode"
)

var (
	ErrEmptyOCRImage = errors.New("empty or nil image for OCR")
	ErrNoTextFound   = errors.New("no text found in image")
	ErrInvalidNumber = errors.New("failed to parse valid number from OCR text")
)

type glyphBounds struct {
	minX, minY int
	maxX, maxY int
}

// Standard 5x7 glyph patterns for OCR matching
var ocrFontTemplates = map[rune][7]string{
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
	'.': {
		"     ",
		"     ",
		"     ",
		"     ",
		"     ",
		" 111 ",
		" 111 ",
	},
	':': {
		"     ",
		" 111 ",
		" 111 ",
		"     ",
		" 111 ",
		" 111 ",
		"     ",
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
	'k': {
		"1    ",
		"1   1",
		"1  1 ",
		"111  ",
		"1  1 ",
		"1   1",
		"1   1",
	},
	'm': {
		"     ",
		"     ",
		"11 11",
		"1 1 1",
		"1 1 1",
		"1   1",
		"1   1",
	},
	'F': {
		"11111",
		"1    ",
		"1111 ",
		"1    ",
		"1    ",
		"1    ",
		"1    ",
	},
	'C': {
		" 1111",
		"1    ",
		"1    ",
		"1    ",
		"1    ",
		"1    ",
		" 1111",
	},
	'R': {
		"1111 ",
		"1   1",
		"1   1",
		"1111 ",
		"1  1 ",
		"1   1",
		"1   1",
	},
	'A': {
		" 111 ",
		"1   1",
		"1   1",
		"11111",
		"1   1",
		"1   1",
		"1   1",
	},
}

// TextOCR provides fast OCR for reading bets, stacks, pot, and player labels.
type TextOCR struct {
	templates map[rune][35]bool
}

// NewTextOCR creates and initializes a TextOCR instance.
func NewTextOCR() *TextOCR {
	t := make(map[rune][35]bool)
	for r, rows := range ocrFontTemplates {
		var bits [35]bool
		idx := 0
		for row := 0; row < 7; row++ {
			rowStr := rows[row]
			for col := 0; col < 5; col++ {
				if col < len(rowStr) && rowStr[col] == '1' {
					bits[idx] = true
				}
				idx++
			}
		}
		t[r] = bits
	}
	return &TextOCR{templates: t}
}

// ParseString extracts string content from the image.
func (ocr *TextOCR) ParseString(img image.Image) (string, error) {
	if img == nil {
		return "", ErrEmptyOCRImage
	}

	bin, w, h := binarizeImage(img)
	if w < 3 || h < 3 {
		return "", ErrNoTextFound
	}

	glyphs, lineMinY, lineMaxY := segmentGlyphsWithLine(bin, w, h)
	if len(glyphs) == 0 {
		return "", ErrNoTextFound
	}

	lineH := lineMaxY - lineMinY
	if lineH <= 0 {
		lineH = 1
	}

	var sb strings.Builder
	var lastMaxX int = -1

	for _, g := range glyphs {
		gw := g.maxX - g.minX
		gh := g.maxY - g.minY

		// Add space only on true word space gap
		if lastMaxX >= 0 && (g.minX-lastMaxX) > int(float64(lineH)*0.85) {
			sb.WriteRune(' ')
		}
		lastMaxX = g.maxX

		// Check dot / period near bottom of line
		if gh <= int(float64(lineH)*0.45) && g.minY >= lineMinY+int(float64(lineH)*0.40) {
			sb.WriteRune('.')
			continue
		}

		// Check colon (two dots centered vertically)
		if gh <= int(float64(lineH)*0.85) && gh >= int(float64(lineH)*0.35) && gw <= int(float64(lineH)*0.55) {
			midY := (g.minY + g.maxY) / 2
			topHas := false
			botHas := false
			midHas := false
			for x := g.minX; x < g.maxX; x++ {
				if bin[g.minY*w+x] || (g.minY+1 < h && bin[(g.minY+1)*w+x]) {
					topHas = true
				}
				if bin[(g.maxY-1)*w+x] || (g.maxY-2 >= 0 && bin[(g.maxY-2)*w+x]) {
					botHas = true
				}
				if bin[midY*w+x] {
					midHas = true
				}
			}
			if topHas && botHas && !midHas {
				sb.WriteRune(':')
				continue
			}
		}

		// Normalize glyph with vertical position relative to line
		norm := normalizeGlyphInLine(bin, w, g, lineMinY, lineMaxY)
		r, score := ocr.matchGlyph(norm)
		if score >= 0.35 {
			sb.WriteRune(r)
		}
	}

	res := strings.TrimSpace(sb.String())
	if res == "" {
		return "", ErrNoTextFound
	}
	return res, nil
}

// ParseNumber extracts a numeric value (pot, bet, stack) from the image.
func (ocr *TextOCR) ParseNumber(img image.Image) (float64, error) {
	if img == nil {
		return 0, ErrEmptyOCRImage
	}

	text, err := ocr.ParseString(img)
	if err != nil {
		return 0, err
	}

	return parseAmountString(text)
}

func parseAmountString(s string) (float64, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, ErrInvalidNumber
	}

	tokens := strings.Fields(s)
	var bestVal float64
	var found bool

	for _, token := range tokens {
		hasLabelLetters := strings.ContainsAny(token, "PotPOTBetBETCallCALLFoldFOLDRaiseRAISE:")

		multiplier := 1.0
		cleanToken := strings.TrimFunc(token, func(r rune) bool {
			return !unicode.IsDigit(r) && r != '.' && r != 'k' && r != 'K' && r != 'm' && r != 'M'
		})
		if cleanToken == "" {
			continue
		}

		lower := strings.ToLower(cleanToken)
		if strings.HasSuffix(lower, "k") {
			multiplier = 1000.0
			cleanToken = cleanToken[:len(cleanToken)-1]
		} else if strings.HasSuffix(lower, "m") {
			multiplier = 1000000.0
			cleanToken = cleanToken[:len(cleanToken)-1]
		}

		var numSb strings.Builder
		for _, r := range cleanToken {
			if unicode.IsDigit(r) || r == '.' {
				numSb.WriteRune(r)
			}
		}

		numStr := numSb.String()
		if numStr == "" {
			continue
		}

		if strings.Count(numStr, ".") > 1 {
			parts := strings.Split(numStr, ".")
			numStr = strings.Join(parts[:len(parts)-1], "") + "." + parts[len(parts)-1]
		}

		val, err := strconv.ParseFloat(numStr, 64)
		if err == nil {
			if !hasLabelLetters {
				return val * multiplier, nil
			}
			bestVal = val * multiplier
			found = true
		}
	}

	if found {
		return bestVal, nil
	}
	return 0, ErrInvalidNumber
}

func (ocr *TextOCR) matchGlyph(bits [35]bool) (rune, float64) {
	var bestRune rune = '?'
	var bestScore float64 = -1.0

	for r, tmpl := range ocr.templates {
		intersection := 0
		union := 0
		bgMatch := 0

		for i := 0; i < 35; i++ {
			if bits[i] && tmpl[i] {
				intersection++
			}
			if bits[i] || tmpl[i] {
				union++
			}
			if !bits[i] && !tmpl[i] {
				bgMatch++
			}
		}

		var iou float64
		if union > 0 {
			iou = float64(intersection) / float64(union)
		}

		score := 0.75*iou + 0.25*(float64(bgMatch)/35.0)

		if score > bestScore {
			bestScore = score
			bestRune = r
		}
	}
	return bestRune, bestScore
}

// binarizeImage converts image to a 2D binary grid (true = foreground text, false = background).
func binarizeImage(img image.Image) ([]bool, int, int) {
	bounds := img.Bounds()
	w := bounds.Dx()
	h := bounds.Dy()
	lums := make([]float64, w*h)

	var totalLum float64
	var minLum float64 = 1.0
	var maxLum float64 = 0.0

	idx := 0
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			r, g, b, _ := img.At(x, y).RGBA()
			lum := (0.299*float64(r) + 0.587*float64(g) + 0.114*float64(b)) / 65535.0
			lums[idx] = lum
			totalLum += lum
			if lum < minLum {
				minLum = lum
			}
			if lum > maxLum {
				maxLum = lum
			}
			idx++
		}
	}

	if w*h == 0 {
		return nil, 0, 0
	}

	avgLum := totalLum / float64(w*h)
	threshold := (minLum + maxLum) / 2.0
	if math.Abs(maxLum-minLum) < 0.1 {
		threshold = avgLum
	}

	brightCount := 0
	for _, l := range lums {
		if l > threshold {
			brightCount++
		}
	}

	brightIsForeground := brightCount <= (w*h)/2

	bin := make([]bool, w*h)
	for i, l := range lums {
		if brightIsForeground {
			bin[i] = l > threshold
		} else {
			bin[i] = l <= threshold
		}
	}

	return bin, w, h
}

func segmentGlyphsWithLine(bin []bool, w, h int) ([]glyphBounds, int, int) {
	colCount := make([]int, w)
	for x := 0; x < w; x++ {
		count := 0
		for y := 0; y < h; y++ {
			if bin[y*w+x] {
				count++
			}
		}
		colCount[x] = count
	}

	var glyphs []glyphBounds
	inGlyph := false
	startCol := 0

	overallMinY := h
	overallMaxY := 0

	for x := 0; x < w; x++ {
		if colCount[x] > 0 {
			if !inGlyph {
				inGlyph = true
				startCol = x
			}
		} else {
			if inGlyph {
				inGlyph = false
				endCol := x
				if g, ok := computeGlyphRowBounds(bin, w, h, startCol, endCol); ok {
					glyphs = append(glyphs, g)
					if g.minY < overallMinY {
						overallMinY = g.minY
					}
					if g.maxY > overallMaxY {
						overallMaxY = g.maxY
					}
				}
			}
		}
	}
	if inGlyph {
		if g, ok := computeGlyphRowBounds(bin, w, h, startCol, w); ok {
			glyphs = append(glyphs, g)
			if g.minY < overallMinY {
				overallMinY = g.minY
			}
			if g.maxY > overallMaxY {
				overallMaxY = g.maxY
			}
		}
	}

	if overallMinY > overallMaxY {
		overallMinY = 0
		overallMaxY = h
	}

	return glyphs, overallMinY, overallMaxY
}

func computeGlyphRowBounds(bin []bool, w, h, minX, maxX int) (glyphBounds, bool) {
	minY := h
	maxY := 0
	found := false

	for y := 0; y < h; y++ {
		for x := minX; x < maxX; x++ {
			if bin[y*w+x] {
				if y < minY {
					minY = y
				}
				if y > maxY {
					maxY = y
				}
				found = true
			}
		}
	}

	if !found {
		return glyphBounds{}, false
	}

	return glyphBounds{
		minX: minX,
		maxX: maxX,
		minY: minY,
		maxY: maxY + 1,
	}, true
}

// normalizeGlyphInLine maps the glyph to a 5x7 binary matrix preserving relative baseline.
func normalizeGlyphInLine(bin []bool, totalW int, g glyphBounds, lineMinY, lineMaxY int) [35]bool {
	var res [35]bool
	gw := g.maxX - g.minX
	lineH := lineMaxY - lineMinY
	if gw <= 0 || lineH <= 0 {
		return res
	}

	// Calculate vertical row placement in 7 rows
	idx := 0
	for row := 0; row < 7; row++ {
		srcYStart := lineMinY + int(float64(row)*float64(lineH)/7.0)
		srcYEnd := lineMinY + int(float64(row+1)*float64(lineH)/7.0)
		if srcYEnd <= srcYStart {
			srcYEnd = srcYStart + 1
		}

		for col := 0; col < 5; col++ {
			srcXStart := g.minX + int(float64(col)*float64(gw)/5.0)
			srcXEnd := g.minX + int(float64(col+1)*float64(gw)/5.0)
			if srcXEnd <= srcXStart {
				srcXEnd = srcXStart + 1
			}

			fgCount := 0
			cellTotal := 0
			for y := srcYStart; y < srcYEnd && y < lineMaxY; y++ {
				for x := srcXStart; x < srcXEnd && x < g.maxX; x++ {
					if y >= g.minY && y < g.maxY {
						if bin[y*totalW+x] {
							fgCount++
						}
					}
					cellTotal++
				}
			}

			if cellTotal > 0 && float64(fgCount)/float64(cellTotal) >= 0.20 {
				res[idx] = true
			}
			idx++
		}
	}

	return res
}
