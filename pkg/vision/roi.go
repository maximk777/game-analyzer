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

// DefaultCoinPoker6MaxROI returns normalized ROI layout calibrated for CoinPoker 6-max tables.
func DefaultCoinPoker6MaxROI() ROIConfig {
	return ROIConfig{
		HeroCards: [2]RectF{
			{X: 0.435, Y: 0.715, Width: 0.060, Height: 0.125},
			{X: 0.500, Y: 0.715, Width: 0.060, Height: 0.125},
		},
		CommunityCards: [5]RectF{
			{X: 0.320, Y: 0.390, Width: 0.055, Height: 0.110},
			{X: 0.395, Y: 0.390, Width: 0.055, Height: 0.110},
			{X: 0.470, Y: 0.390, Width: 0.055, Height: 0.110},
			{X: 0.545, Y: 0.390, Width: 0.055, Height: 0.110},
			{X: 0.620, Y: 0.390, Width: 0.055, Height: 0.110},
		},
		Pot: RectF{
			X: 0.440, Y: 0.345, Width: 0.120, Height: 0.040,
		},
		ActionButtons: RectF{
			X: 0.550, Y: 0.850, Width: 0.420, Height: 0.120,
		},
		TimerBar: RectF{
			X: 0.440, Y: 0.840, Width: 0.120, Height: 0.015,
		},
		Seats: []SeatROI{
			// Seat 0: Hero (Bottom Center)
			{
				SeatNumber: 0,
				Avatar:     RectF{X: 0.450, Y: 0.780, Width: 0.100, Height: 0.080},
				Nameplate:  RectF{X: 0.420, Y: 0.855, Width: 0.160, Height: 0.035},
				Stack:      RectF{X: 0.440, Y: 0.885, Width: 0.120, Height: 0.035},
				Bet:        RectF{X: 0.450, Y: 0.580, Width: 0.100, Height: 0.040},
				Cards: [2]RectF{
					{X: 0.435, Y: 0.715, Width: 0.060, Height: 0.125},
					{X: 0.500, Y: 0.715, Width: 0.060, Height: 0.125},
				},
				ActiveBadge: RectF{X: 0.400, Y: 0.700, Width: 0.035, Height: 0.035},
				IsHero:      true,
			},
			// Seat 1: Bottom Left (SB)
			{
				SeatNumber: 1,
				Avatar:     RectF{X: 0.070, Y: 0.580, Width: 0.110, Height: 0.090},
				Nameplate:  RectF{X: 0.060, Y: 0.675, Width: 0.130, Height: 0.035},
				Stack:      RectF{X: 0.060, Y: 0.705, Width: 0.130, Height: 0.035},
				Bet:        RectF{X: 0.200, Y: 0.640, Width: 0.090, Height: 0.040},
				Cards: [2]RectF{
					{X: 0.060, Y: 0.580, Width: 0.050, Height: 0.080},
					{X: 0.100, Y: 0.580, Width: 0.050, Height: 0.080},
				},
				ActiveBadge: RectF{X: 0.060, Y: 0.640, Width: 0.030, Height: 0.030},
				IsHero:      false,
			},
			// Seat 2: Top Left (BB)
			{
				SeatNumber: 2,
				Avatar:     RectF{X: 0.070, Y: 0.150, Width: 0.110, Height: 0.090},
				Nameplate:  RectF{X: 0.050, Y: 0.245, Width: 0.140, Height: 0.035},
				Stack:      RectF{X: 0.060, Y: 0.275, Width: 0.120, Height: 0.035},
				Bet:        RectF{X: 0.200, Y: 0.310, Width: 0.090, Height: 0.040},
				Cards: [2]RectF{
					{X: 0.060, Y: 0.150, Width: 0.050, Height: 0.080},
					{X: 0.100, Y: 0.150, Width: 0.050, Height: 0.080},
				},
				ActiveBadge: RectF{X: 0.070, Y: 0.230, Width: 0.030, Height: 0.030},
				IsHero:      false,
			},
			// Seat 3: Top Center
			{
				SeatNumber: 3,
				Avatar:     RectF{X: 0.440, Y: 0.080, Width: 0.120, Height: 0.090},
				Nameplate:  RectF{X: 0.420, Y: 0.175, Width: 0.160, Height: 0.035},
				Stack:      RectF{X: 0.440, Y: 0.205, Width: 0.120, Height: 0.035},
				Bet:        RectF{X: 0.450, Y: 0.270, Width: 0.100, Height: 0.040},
				Cards: [2]RectF{
					{X: 0.440, Y: 0.080, Width: 0.050, Height: 0.080},
					{X: 0.480, Y: 0.080, Width: 0.050, Height: 0.080},
				},
				ActiveBadge: RectF{X: 0.410, Y: 0.160, Width: 0.040, Height: 0.025},
				IsHero:      false,
			},
			// Seat 4: Top Right
			{
				SeatNumber: 4,
				Avatar:     RectF{X: 0.770, Y: 0.170, Width: 0.110, Height: 0.090},
				Nameplate:  RectF{X: 0.740, Y: 0.255, Width: 0.160, Height: 0.035},
				Stack:      RectF{X: 0.760, Y: 0.285, Width: 0.120, Height: 0.035},
				Bet:        RectF{X: 0.670, Y: 0.310, Width: 0.090, Height: 0.040},
				Cards: [2]RectF{
					{X: 0.770, Y: 0.170, Width: 0.050, Height: 0.080},
					{X: 0.810, Y: 0.170, Width: 0.050, Height: 0.080},
				},
				ActiveBadge: RectF{X: 0.730, Y: 0.240, Width: 0.040, Height: 0.025},
				IsHero:      false,
			},
			// Seat 5: Bottom Right
			{
				SeatNumber: 5,
				Avatar:     RectF{X: 0.790, Y: 0.560, Width: 0.110, Height: 0.090},
				Nameplate:  RectF{X: 0.760, Y: 0.655, Width: 0.160, Height: 0.035},
				Stack:      RectF{X: 0.780, Y: 0.685, Width: 0.120, Height: 0.035},
				Bet:        RectF{X: 0.680, Y: 0.610, Width: 0.090, Height: 0.040},
				Cards: [2]RectF{
					{X: 0.780, Y: 0.560, Width: 0.050, Height: 0.080},
					{X: 0.820, Y: 0.560, Width: 0.050, Height: 0.080},
				},
				ActiveBadge: RectF{X: 0.740, Y: 0.620, Width: 0.040, Height: 0.025},
				IsHero:      false,
			},
		},
	}
}
