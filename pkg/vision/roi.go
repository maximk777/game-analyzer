package vision

import (
	"image"
	"image/draw"
	"math"
)

// RectF defines a relative coordinate rectangle [0.0..1.0] relative to frame dimensions.
type RectF struct {
	X      float64 `json:"x"`
	Y      float64 `json:"y"`
	Width  float64 `json:"width"`
	Height float64 `json:"height"`
}

// Crop extracts the sub-image defined by RectF from the source image.
func (r RectF) Crop(img image.Image) image.Image {
	if img == nil {
		return nil
	}
	bounds := img.Bounds()
	w := float64(bounds.Dx())
	h := float64(bounds.Dy())

	minX := bounds.Min.X + int(math.Round(r.X*w))
	minY := bounds.Min.Y + int(math.Round(r.Y*h))
	maxX := minX + int(math.Round(r.Width*w))
	maxY := minY + int(math.Round(r.Height*h))

	if minX < bounds.Min.X {
		minX = bounds.Min.X
	}
	if minY < bounds.Min.Y {
		minY = bounds.Min.Y
	}
	if maxX > bounds.Max.X {
		maxX = bounds.Max.X
	}
	if maxY > bounds.Max.Y {
		maxY = bounds.Max.Y
	}

	if minX >= maxX || minY >= maxY {
		return nil
	}

	cropRect := image.Rect(minX, minY, maxX, maxY)

	type subImager interface {
		SubImage(r image.Rectangle) image.Image
	}
	if si, ok := img.(subImager); ok {
		return si.SubImage(cropRect)
	}

	dst := image.NewRGBA(image.Rect(0, 0, cropRect.Dx(), cropRect.Dy()))
	draw.Draw(dst, dst.Bounds(), img, cropRect.Min, draw.Src)
	return dst
}

// SeatROI holds regions of interest for a single seat at the poker table.
type SeatROI struct {
	SeatNumber  int      `json:"seat_number"`
	Avatar      RectF    `json:"avatar"`
	Nameplate   RectF    `json:"nameplate"`
	Stack       RectF    `json:"stack"`
	Bet         RectF    `json:"bet"`
	Cards       [2]RectF `json:"cards"`
	ActiveBadge RectF    `json:"active_badge"`
	IsHero      bool     `json:"is_hero"`
}

// ROIConfig holds region of interest definitions for a poker table.
type ROIConfig struct {
	HeroCards      [2]RectF  `json:"hero_cards"`
	CommunityCards [5]RectF  `json:"community_cards"`
	Pot            RectF     `json:"pot"`
	Seats          []SeatROI `json:"seats"`
	ActionButtons  RectF     `json:"action_buttons"`
	TimerBar       RectF     `json:"timer_bar"`
}

// DefaultCoinPoker6MaxROI returns normalized ROI layout for a 6-max CoinPoker table.
func DefaultCoinPoker6MaxROI() ROIConfig {
	return ROIConfig{
		HeroCards: [2]RectF{
			{X: 0.440, Y: 0.740, Width: 0.055, Height: 0.110},
			{X: 0.505, Y: 0.740, Width: 0.055, Height: 0.110},
		},
		CommunityCards: [5]RectF{
			{X: 0.285, Y: 0.410, Width: 0.058, Height: 0.105},
			{X: 0.355, Y: 0.410, Width: 0.058, Height: 0.105},
			{X: 0.425, Y: 0.410, Width: 0.058, Height: 0.105},
			{X: 0.495, Y: 0.410, Width: 0.058, Height: 0.105},
			{X: 0.565, Y: 0.410, Width: 0.058, Height: 0.105},
		},
		Pot: RectF{
			X: 0.410, Y: 0.320, Width: 0.180, Height: 0.050,
		},
		ActionButtons: RectF{
			X: 0.650, Y: 0.880, Width: 0.320, Height: 0.090,
		},
		TimerBar: RectF{
			X: 0.420, Y: 0.710, Width: 0.160, Height: 0.015,
		},
		Seats: []SeatROI{
			// Seat 0: Hero (Bottom Center)
			{
				SeatNumber: 0,
				Avatar:     RectF{X: 0.440, Y: 0.860, Width: 0.120, Height: 0.070},
				Nameplate:  RectF{X: 0.440, Y: 0.935, Width: 0.120, Height: 0.035},
				Stack:      RectF{X: 0.440, Y: 0.970, Width: 0.120, Height: 0.028},
				Bet:        RectF{X: 0.450, Y: 0.680, Width: 0.100, Height: 0.040},
				Cards: [2]RectF{
					{X: 0.440, Y: 0.740, Width: 0.055, Height: 0.110},
					{X: 0.505, Y: 0.740, Width: 0.055, Height: 0.110},
				},
				ActiveBadge: RectF{X: 0.400, Y: 0.720, Width: 0.035, Height: 0.035},
				IsHero:      true,
			},
			// Seat 1: Bottom Left
			{
				SeatNumber: 1,
				Avatar:     RectF{X: 0.100, Y: 0.640, Width: 0.110, Height: 0.070},
				Nameplate:  RectF{X: 0.100, Y: 0.715, Width: 0.110, Height: 0.035},
				Stack:      RectF{X: 0.100, Y: 0.750, Width: 0.110, Height: 0.028},
				Bet:        RectF{X: 0.220, Y: 0.620, Width: 0.090, Height: 0.035},
				Cards: [2]RectF{
					{X: 0.120, Y: 0.560, Width: 0.040, Height: 0.070},
					{X: 0.165, Y: 0.560, Width: 0.040, Height: 0.070},
				},
				ActiveBadge: RectF{X: 0.220, Y: 0.660, Width: 0.030, Height: 0.030},
				IsHero:      false,
			},
			// Seat 2: Top Left
			{
				SeatNumber: 2,
				Avatar:     RectF{X: 0.120, Y: 0.180, Width: 0.110, Height: 0.070},
				Nameplate:  RectF{X: 0.120, Y: 0.255, Width: 0.110, Height: 0.035},
				Stack:      RectF{X: 0.120, Y: 0.290, Width: 0.110, Height: 0.028},
				Bet:        RectF{X: 0.240, Y: 0.260, Width: 0.090, Height: 0.035},
				Cards: [2]RectF{
					{X: 0.130, Y: 0.100, Width: 0.040, Height: 0.070},
					{X: 0.175, Y: 0.100, Width: 0.040, Height: 0.070},
				},
				ActiveBadge: RectF{X: 0.240, Y: 0.220, Width: 0.030, Height: 0.030},
				IsHero:      false,
			},
			// Seat 3: Top Center
			{
				SeatNumber: 3,
				Avatar:     RectF{X: 0.440, Y: 0.050, Width: 0.120, Height: 0.070},
				Nameplate:  RectF{X: 0.440, Y: 0.125, Width: 0.120, Height: 0.035},
				Stack:      RectF{X: 0.440, Y: 0.160, Width: 0.120, Height: 0.028},
				Bet:        RectF{X: 0.450, Y: 0.220, Width: 0.100, Height: 0.035},
				Cards: [2]RectF{
					{X: 0.445, Y: 0.010, Width: 0.040, Height: 0.070},
					{X: 0.490, Y: 0.010, Width: 0.040, Height: 0.070},
				},
				ActiveBadge: RectF{X: 0.410, Y: 0.200, Width: 0.030, Height: 0.030},
				IsHero:      false,
			},
			// Seat 4: Top Right
			{
				SeatNumber: 4,
				Avatar:     RectF{X: 0.770, Y: 0.180, Width: 0.110, Height: 0.070},
				Nameplate:  RectF{X: 0.770, Y: 0.255, Width: 0.110, Height: 0.035},
				Stack:      RectF{X: 0.770, Y: 0.290, Width: 0.110, Height: 0.028},
				Bet:        RectF{X: 0.670, Y: 0.260, Width: 0.090, Height: 0.035},
				Cards: [2]RectF{
					{X: 0.780, Y: 0.100, Width: 0.040, Height: 0.070},
					{X: 0.825, Y: 0.100, Width: 0.040, Height: 0.070},
				},
				ActiveBadge: RectF{X: 0.730, Y: 0.220, Width: 0.030, Height: 0.030},
				IsHero:      false,
			},
			// Seat 5: Bottom Right
			{
				SeatNumber: 5,
				Avatar:     RectF{X: 0.790, Y: 0.640, Width: 0.110, Height: 0.070},
				Nameplate:  RectF{X: 0.790, Y: 0.715, Width: 0.110, Height: 0.035},
				Stack:      RectF{X: 0.790, Y: 0.750, Width: 0.110, Height: 0.028},
				Bet:        RectF{X: 0.690, Y: 0.620, Width: 0.090, Height: 0.035},
				Cards: [2]RectF{
					{X: 0.795, Y: 0.560, Width: 0.040, Height: 0.070},
					{X: 0.840, Y: 0.560, Width: 0.040, Height: 0.070},
				},
				ActiveBadge: RectF{X: 0.740, Y: 0.660, Width: 0.030, Height: 0.030},
				IsHero:      false,
			},
		},
	}
}
