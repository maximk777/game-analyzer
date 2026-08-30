package vision

import (
	"errors"
	"image"
	"image/color"
	"image/draw"
	"math"
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
			synthImg := generateSyntheticCard(c, tmplWidth, tmplHeight)
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

// MatchCard matches the given image against registered templates.
// Returns the matched table.Card, a confidence score [0.0..1.0], and an error if no match or blank.
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

	if bestScore < m.minConf {
		return bestCard, bestScore, ErrLowConfidence
	}

	return bestCard, bestScore, nil
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

// hasCardFeatures checks if the region contains a card (high brightness / contrast vs dark green table).
func hasCardFeatures(img image.Image) bool {
	bounds := img.Bounds()
	var totalLum float64
	var count float64
	var greenCount float64

	for y := bounds.Min.Y; y < bounds.Max.Y; y += 2 {
		for x := bounds.Min.X; x < bounds.Max.X; x += 2 {
			r, g, b, _ := img.At(x, y).RGBA()
			rf := float64(r) / 65535.0
			gf := float64(g) / 65535.0
			bf := float64(b) / 65535.0

			lum := 0.299*rf + 0.587*gf + 0.114*bf
			totalLum += lum
			count++

			// Check if predominantly table felt (greenish dark background)
			if gf > rf+0.15 && gf > bf+0.15 && gf < 0.6 {
				greenCount++
			}
		}
	}

	if count == 0 {
		return false
	}

	avgLum := totalLum / count
	greenRatio := greenCount / count

	if greenRatio > 0.65 && avgLum < 0.45 {
		return false
	}

	return avgLum > 0.20
}

// generateSyntheticCard renders a synthetic card for default templates.
func generateSyntheticCard(c table.Card, width, height int) image.Image {
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
