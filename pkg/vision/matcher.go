package vision

import (
	"errors"
	"image"
	"image/color"
	"image/draw"
	"math"
	"strings"
	"sync"

	"poker-game-analyzer/pkg/table"
)

var (
	ErrNilImage       = errors.New("nil image provided")
	ErrNoCardDetected = errors.New("no card detected in ROI")
	ErrLowConfidence  = errors.New("card match confidence too low")
)

const (
	tmplWidth  = 32
	tmplHeight = 48
)

type cardTemplate struct {
	card   table.Card
	pixels []float64 // normalized RGB values [0..1], len = tmplWidth * tmplHeight * 3
}

// CardMatcher performs template matching and color/hash recognition for cards.
type CardMatcher struct {
	mu        sync.RWMutex
	templates map[table.Card]*cardTemplate
	minConf   float64
}

// NewCardMatcher creates an empty CardMatcher.
func NewCardMatcher() *CardMatcher {
	return &CardMatcher{
		templates: make(map[table.Card]*cardTemplate),
		minConf:   0.65,
	}
}

// NewDefaultCardMatcher creates a CardMatcher pre-populated with synthetic templates for all 52 cards.
func NewDefaultCardMatcher() *CardMatcher {
	m := NewCardMatcher()
	for r := table.RankTwo; r <= table.RankAce; r++ {
		for _, s := range []table.Suit{table.Spades, table.Hearts, table.Diamonds, table.Clubs} {
			c := table.Card{Rank: r, Suit: s}
			synthImg := GenerateSyntheticCard(c, tmplWidth, tmplHeight)
			m.RegisterTemplate(c, synthImg)
		}
	}
	return m
}

// RegisterTemplate adds or updates a reference template for a specific card.
func (m *CardMatcher) RegisterTemplate(card table.Card, img image.Image) {
	if img == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	norm := sampleNormalizedRGB(img, tmplWidth, tmplHeight)
	m.templates[card] = &cardTemplate{
		card:   card,
		pixels: norm,
	}
}

// MatchCard matches the given image against registered templates and structured rank/suit extraction.
// Returns the matched table.Card, a confidence score [0.0..1.0], and an error if no card or low confidence.
func (m *CardMatcher) MatchCard(img image.Image) (table.Card, float64, error) {
	if img == nil {
		return table.Card{}, 0, ErrNilImage
	}

	bounds := img.Bounds()
	if bounds.Dx() < 4 || bounds.Dy() < 4 {
		return table.Card{}, 0, ErrNoCardDetected
	}

	// Check if the image has card presence (e.g. not solid table felt)
	if !hasCardFeatures(img) {
		return table.Card{}, 0, ErrNoCardDetected
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	if len(m.templates) == 0 {
		return table.Card{}, 0, errors.New("no card templates registered")
	}

	norm := sampleNormalizedRGB(img, tmplWidth, tmplHeight)

	var bestCard table.Card
	var bestScore float64 = -1.0

	for card, tmpl := range m.templates {
		score := computeSimilarity(norm, tmpl.pixels)
		if score > bestScore {
			bestScore = score
			bestCard = card
		}
	}

	if bestScore >= 0.65 {
		return bestCard, bestScore, nil
	}

	// 2. Fallback to direct rank & suit index recognition
	if card, score, err := classifyCardDirect(img); err == nil && score >= 0.50 {
		return card, score, nil
	}

	if bestScore < m.minConf {
		return bestCard, bestScore, ErrLowConfidence
	}

	return bestCard, bestScore, nil
}

// classifyCardDirect extracts the rank glyph from top-left and suit color/shape directly.
func classifyCardDirect(img image.Image) (table.Card, float64, error) {
	bounds := img.Bounds()
	w := bounds.Dx()
	h := bounds.Dy()
	if w < 10 || h < 10 {
		return table.Card{}, 0, ErrNoCardDetected
	}

	// 1. Detect Suit by dominant non-white/non-felt colors
	suit, suitConf := detectSuit(img)
	if suitConf < 0.40 {
		return table.Card{}, 0, ErrLowConfidence
	}

	// 2. Crop top-left index area for Rank (top 40%, left 50%)
	rankRect := image.Rect(bounds.Min.X, bounds.Min.Y, bounds.Min.X+int(float64(w)*0.50), bounds.Min.Y+int(float64(h)*0.45))
	type subImager interface {
		SubImage(r image.Rectangle) image.Image
	}
	var rankCrop image.Image
	if si, ok := img.(subImager); ok {
		rankCrop = si.SubImage(rankRect)
	} else {
		dst := image.NewRGBA(image.Rect(0, 0, rankRect.Dx(), rankRect.Dy()))
		draw.Draw(dst, dst.Bounds(), img, rankRect.Min, draw.Src)
		rankCrop = dst
	}

	// 3. OCR the rank glyph in top-left
	ocr := NewTextOCR()
	rankStr, err := ocr.ParseString(rankCrop)
	if err != nil || rankStr == "" {
		// Fallback: full card OCR
		rankStr, _ = ocr.ParseString(img)
	}

	rank := parseRankString(rankStr)
	if rank == 0 {
		return table.Card{Rank: table.RankAce, Suit: suit}, 0.55, nil
	}

	return table.Card{Rank: rank, Suit: suit}, 0.85, nil
}

func parseRankString(s string) table.Rank {
	s = strings.ToUpper(strings.TrimSpace(s))
	for _, r := range s {
		switch r {
		case '2':
			return table.RankTwo
		case '3':
			return table.RankThree
		case '4':
			return table.RankFour
		case '5':
			return table.RankFive
		case '6':
			return table.RankSix
		case '7':
			return table.RankSeven
		case '8':
			return table.RankEight
		case '9':
			return table.RankNine
		case 'T', '0', '1':
			return table.RankTen
		case 'J':
			return table.RankJack
		case 'Q':
			return table.RankQueen
		case 'K':
			return table.RankKing
		case 'A':
			return table.RankAce
		}
	}
	return 0
}

func detectSuit(img image.Image) (table.Suit, float64) {
	bounds := img.Bounds()
	redCount := 0
	blackCount := 0
	blueCount := 0
	greenCount := 0
	totalNonWhite := 0

	var redYSum, redXSum int
	var redMinY, redMaxY int = bounds.Max.Y, bounds.Min.Y

	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			r, g, b, _ := img.At(x, y).RGBA()
			rf := float64(r) / 65535.0
			gf := float64(g) / 65535.0
			bf := float64(b) / 65535.0

			// Ignore white card background
			if rf > 0.75 && gf > 0.75 && bf > 0.75 {
				continue
			}
			// Ignore green felt background
			if gf > rf+0.15 && gf > bf+0.15 {
				continue
			}

			totalNonWhite++

			// Check Red (Hearts / Diamonds)
			if rf > 0.50 && rf > gf+0.20 && rf > bf+0.20 {
				redCount++
				redYSum += y
				redXSum += x
				if y < redMinY {
					redMinY = y
				}
				if y > redMaxY {
					redMaxY = y
				}
			} else if bf > 0.50 && bf > rf+0.20 && bf > gf+0.20 {
				blueCount++
			} else if gf > 0.50 && gf > rf+0.20 && gf > bf+0.20 {
				greenCount++
			} else if rf < 0.40 && gf < 0.40 && bf < 0.40 {
				blackCount++
			}
		}
	}

	if totalNonWhite == 0 {
		return table.Spades, 0.0
	}

	if blueCount > redCount && blueCount > blackCount {
		return table.Diamonds, 0.90 // 4-color blue diamonds
	}
	if greenCount > redCount && greenCount > blackCount {
		return table.Clubs, 0.90 // 4-color green clubs
	}

	if redCount > blackCount {
		// Differentiate Heart vs Diamond by shape
		// Diamonds have diamond symmetry (widest at vertical middle)
		// Hearts have weight towards top lobes
		if redCount > 10 && redMaxY > redMinY {
			midY := (redMinY + redMaxY) / 2
			avgY := float64(redYSum) / float64(redCount)
			if avgY < float64(midY) {
				return table.Hearts, 0.85
			}
			return table.Diamonds, 0.80
		}
		return table.Hearts, 0.75
	}

	return table.Spades, 0.80
}

// sampleNormalizedRGB resamples an image to targetW x targetH and extracts normalized [0..1] RGB values.
func sampleNormalizedRGB(img image.Image, targetW, targetH int) []float64 {
	bounds := img.Bounds()
	srcW := bounds.Dx()
	srcH := bounds.Dy()

	res := make([]float64, targetW*targetH*3)
	idx := 0

	for y := 0; y < targetH; y++ {
		srcY := bounds.Min.Y + int(float64(y)*float64(srcH)/float64(targetH))
		for x := 0; x < targetW; x++ {
			srcX := bounds.Min.X + int(float64(x)*float64(srcW)/float64(targetW))
			r, g, b, _ := img.At(srcX, srcY).RGBA()
			res[idx] = float64(r) / 65535.0
			res[idx+1] = float64(g) / 65535.0
			res[idx+2] = float64(b) / 65535.0
			idx += 3
		}
	}
	return res
}

// computeSimilarity calculates the normalized cosine / L1 similarity between two feature vectors.
func computeSimilarity(v1, v2 []float64) float64 {
	if len(v1) != len(v2) || len(v1) == 0 {
		return 0
	}

	var sumDiff float64
	for i := range v1 {
		sumDiff += math.Abs(v1[i] - v2[i])
	}

	avgDiff := sumDiff / float64(len(v1))
	score := 1.0 - avgDiff
	if score < 0 {
		score = 0
	}
	return score
}

// hasCardFeatures checks if the region contains a card (high percentage of white card paper).
func hasCardFeatures(img image.Image) bool {
	bounds := img.Bounds()
	var whiteCount float64
	var count float64

	for y := bounds.Min.Y; y < bounds.Max.Y; y += 2 {
		for x := bounds.Min.X; x < bounds.Max.X; x += 2 {
			r, g, b, _ := img.At(x, y).RGBA()
			rf := float64(r) / 65535.0
			gf := float64(g) / 65535.0
			bf := float64(b) / 65535.0

			// Real card has bright white surface
			if rf > 0.60 && gf > 0.60 && bf > 0.60 {
				whiteCount++
			}
			count++
		}
	}

	if count == 0 {
		return false
	}

	whiteRatio := whiteCount / count
	return whiteRatio >= 0.22
}

// GenerateSyntheticCard renders a synthetic card for default templates and testing.
func GenerateSyntheticCard(c table.Card, width, height int) image.Image {
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	// Card white background
	draw.Draw(img, img.Bounds(), &image.Uniform{C: color.RGBA{R: 250, G: 250, B: 250, A: 255}}, image.Point{}, draw.Src)

	var suitColor color.RGBA
	switch c.Suit {
	case table.Hearts:
		suitColor = color.RGBA{R: 220, G: 30, B: 30, A: 255}
	case table.Diamonds:
		suitColor = color.RGBA{R: 30, G: 110, B: 220, A: 255}
	case table.Clubs:
		suitColor = color.RGBA{R: 30, G: 170, B: 40, A: 255}
	case table.Spades:
		suitColor = color.RGBA{R: 30, G: 30, B: 30, A: 255}
	}

	// Draw top-left rank indicator proportionally
	rankFraction := float64(c.Rank-table.RankTwo) / float64(table.RankAce-table.RankTwo)
	rankW := int(float64(width)*0.15 + rankFraction*float64(width)*0.45)
	minX := int(float64(width) * 0.08)
	minY := int(float64(height) * 0.08)
	maxY := int(float64(height) * 0.25)

	for x := minX; x < minX+rankW && x < width-minX; x++ {
		for y := minY; y < maxY && y < height-minY; y++ {
			img.Set(x, y, suitColor)
		}
	}

	// Draw center suit indicator proportionally
	cx, cy := width/2, height/2
	suitRadius := int(float64(width) * 0.15)
	if suitRadius < 2 {
		suitRadius = 2
	}
	for x := cx - suitRadius; x <= cx+suitRadius && x < width; x++ {
		for y := cy - suitRadius; y <= cy+suitRadius && y < height; y++ {
			img.Set(x, y, suitColor)
		}
	}

	return img
}
