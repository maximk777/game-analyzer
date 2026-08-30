package vision

import (
	"image"
	"image/color"
	"image/draw"
	"testing"

	"poker-game-analyzer/pkg/table"
)

func TestROIConfig_DefaultCoinPoker6Max(t *testing.T) {
	cfg := DefaultCoinPoker6MaxROI()

	if len(cfg.HeroCards) != 2 {
		t.Fatalf("expected 2 Hero cards ROI, got %d", len(cfg.HeroCards))
	}
	if len(cfg.CommunityCards) != 5 {
		t.Fatalf("expected 5 Community cards ROI, got %d", len(cfg.CommunityCards))
	}
	if len(cfg.Seats) != 6 {
		t.Fatalf("expected 6 seats ROI, got %d", len(cfg.Seats))
	}

	// Verify all RectF coords are within [0.0, 1.0]
	checkRect := func(name string, r RectF) {
		if r.X < 0 || r.X > 1.0 || r.Y < 0 || r.Y > 1.0 || r.Width <= 0 || r.Width > 1.0 || r.Height <= 0 || r.Height > 1.0 {
			t.Errorf("invalid ROI coords for %s: %+v", name, r)
		}
	}

	checkRect("Pot", cfg.Pot)
	checkRect("HeroCard0", cfg.HeroCards[0])
	checkRect("HeroCard1", cfg.HeroCards[1])
	for i, c := range cfg.CommunityCards {
		checkRect("CommunityCard", c)
		_ = i
	}
	for _, s := range cfg.Seats {
		checkRect("Seat.Avatar", s.Avatar)
		checkRect("Seat.Stack", s.Stack)
		checkRect("Seat.Bet", s.Bet)
	}
}

func TestRectF_Crop(t *testing.T) {
	baseImg := image.NewRGBA(image.Rect(0, 0, 1000, 800))
	draw.Draw(baseImg, baseImg.Bounds(), &image.Uniform{C: color.RGBA{R: 50, G: 50, B: 50, A: 255}}, image.Point{}, draw.Src)

	// Draw a red square at (100, 100, 200, 200) -> X: 0.1, Y: 0.125, W: 0.1, H: 0.125
	rect := image.Rect(100, 100, 200, 200)
	draw.Draw(baseImg, rect, &image.Uniform{C: color.RGBA{R: 255, G: 0, B: 0, A: 255}}, image.Point{}, draw.Src)

	rf := RectF{X: 0.1, Y: 0.125, Width: 0.1, Height: 0.125}
	cropped := rf.Crop(baseImg)

	if cropped == nil {
		t.Fatal("expected non-nil cropped image")
	}
	bounds := cropped.Bounds()
	if bounds.Dx() != 100 || bounds.Dy() != 100 {
		t.Errorf("expected cropped size 100x100, got %dx%d", bounds.Dx(), bounds.Dy())
	}
}

func TestStateDiffer_DetectEvents(t *testing.T) {
	differ := NewStateDiffer()

	heroCard1 := table.Card{Rank: table.RankAce, Suit: table.Spades}
	heroCard2 := table.Card{Rank: table.RankKing, Suit: table.Hearts}
	flop1 := table.Card{Rank: table.RankTen, Suit: table.Diamonds}
	flop2 := table.Card{Rank: table.RankNine, Suit: table.Clubs}
	flop3 := table.Card{Rank: table.RankTwo, Suit: table.Hearts}

	// 1. Initial State (Preflop hand start)
	hand1 := &table.HandState{
		HandID:         "hand-1",
		TableID:        "table-1",
		Street:         table.StreetPreflop,
		Pot:            1.5,
		CurrentBet:     1.0,
		HeroID:         "player-0",
		HeroCards:      [2]table.Card{heroCard1, heroCard2},
		CommunityCards: []table.Card{},
		Seats: []table.SeatState{
			{SeatNumber: 0, PlayerID: "player-0", PlayerName: "Hero", Stack: 100, CurrentBet: 0.5, IsActive: true, IsFolded: false},
			{SeatNumber: 1, PlayerID: "player-1", PlayerName: "Villain1", Stack: 100, CurrentBet: 1.0, IsActive: true, IsFolded: false},
		},
	}

	events1 := differ.DetectEvents(nil, hand1)
	if len(events1) == 0 {
		t.Fatal("expected events on hand start from nil state, got none")
	}

	hasHandStart := false
	for _, ev := range events1 {
		if ev.Type == EventHandStart {
			hasHandStart = true
		}
	}
	if !hasHandStart {
		t.Error("expected EventHandStart event")
	}

	// 2. Player Action (Villain raises to 3.0)
	hand2 := &table.HandState{
		HandID:         "hand-1",
		TableID:        "table-1",
		Street:         table.StreetPreflop,
		Pot:            3.5,
		CurrentBet:     3.0,
		HeroID:         "player-0",
		HeroCards:      [2]table.Card{heroCard1, heroCard2},
		CommunityCards: []table.Card{},
		Seats: []table.SeatState{
			{SeatNumber: 0, PlayerID: "player-0", PlayerName: "Hero", Stack: 100, CurrentBet: 0.5, IsActive: true, IsFolded: false},
			{SeatNumber: 1, PlayerID: "player-1", PlayerName: "Villain1", Stack: 97, CurrentBet: 3.0, IsActive: true, IsFolded: false},
		},
	}

	events2 := differ.DetectEvents(hand1, hand2)
	hasAction := false
	for _, ev := range events2 {
		if ev.Type == EventPlayerAction {
			hasAction = true
		}
	}
	if !hasAction {
		t.Error("expected EventPlayerAction when player raises")
	}

	// 3. Flop Dealt
	hand3 := &table.HandState{
		HandID:         "hand-1",
		TableID:        "table-1",
		Street:         table.StreetFlop,
		Pot:            6.0,
		CurrentBet:     0.0,
		HeroID:         "player-0",
		HeroCards:      [2]table.Card{heroCard1, heroCard2},
		CommunityCards: []table.Card{flop1, flop2, flop3},
		Seats: []table.SeatState{
			{SeatNumber: 0, PlayerID: "player-0", PlayerName: "Hero", Stack: 97, CurrentBet: 0.0, IsActive: true, IsFolded: false},
			{SeatNumber: 1, PlayerID: "player-1", PlayerName: "Villain1", Stack: 97, CurrentBet: 0.0, IsActive: true, IsFolded: false},
		},
	}

	events3 := differ.DetectEvents(hand2, hand3)
	hasCardDealt := false
	for _, ev := range events3 {
		if ev.Type == EventCardDealt {
			hasCardDealt = true
		}
	}
	if !hasCardDealt {
		t.Error("expected EventCardDealt on Flop")
	}

	// 4. Hero Turn
	eventsHero := differ.DetectHeroTurn(hand3, true)
	if len(eventsHero) == 0 || eventsHero[0].Type != EventHeroTurn {
		t.Error("expected EventHeroTurn event")
	}

	// 5. Hand End / Showdown
	handEnd := &table.HandState{
		HandID:         "hand-1",
		TableID:        "table-1",
		Street:         table.StreetShowdown,
		Pot:            0.0,
		CurrentBet:     0.0,
		HeroID:         "player-0",
		HeroCards:      [2]table.Card{heroCard1, heroCard2},
		CommunityCards: []table.Card{flop1, flop2, flop3},
		Seats: []table.SeatState{
			{SeatNumber: 0, PlayerID: "player-0", PlayerName: "Hero", Stack: 103, CurrentBet: 0.0, IsActive: true, IsFolded: false},
			{SeatNumber: 1, PlayerID: "player-1", PlayerName: "Villain1", Stack: 97, CurrentBet: 0.0, IsActive: true, IsFolded: true},
		},
	}

	eventsEnd := differ.DetectEvents(hand3, handEnd)
	hasHandEnd := false
	for _, ev := range eventsEnd {
		if ev.Type == EventHandEnd {
			hasHandEnd = true
		}
	}
	if !hasHandEnd {
		t.Error("expected EventHandEnd on showdown / winner pot distribution")
	}
}

func TestFrameParser_ParseFrame(t *testing.T) {
	matcher := NewCardMatcher()
	ocr := NewTextOCR()
	parser := NewFrameParser(matcher, ocr)

	// Create a 1000x800 synthetic table image
	tableImg := image.NewRGBA(image.Rect(0, 0, 1000, 800))
	// Dark green felt
	draw.Draw(tableImg, tableImg.Bounds(), &image.Uniform{C: color.RGBA{R: 20, G: 70, B: 30, A: 255}}, image.Point{}, draw.Src)

	cfg := DefaultCoinPoker6MaxROI()

	// Draw Hero cards at cfg.HeroCards
	heroCard0 := table.Card{Rank: table.RankAce, Suit: table.Spades}
	heroCard1 := table.Card{Rank: table.RankKing, Suit: table.Hearts}
	matcher.RegisterTemplate(heroCard0, generateSyntheticCard(heroCard0, 32, 48))
	matcher.RegisterTemplate(heroCard1, generateSyntheticCard(heroCard1, 32, 48))

	// Paste Hero cards into tableImg at ROI
	pasteCard := func(r RectF, card table.Card) {
		cardImg := generateSyntheticCard(card, int(r.Width*1000), int(r.Height*800))
		minX := int(r.X * 1000)
		minY := int(r.Y * 800)
		targetRect := image.Rect(minX, minY, minX+int(r.Width*1000), minY+int(r.Height*800))
		draw.Draw(tableImg, targetRect, cardImg, image.Point{}, draw.Src)
	}

	pasteCard(cfg.HeroCards[0], heroCard0)
	pasteCard(cfg.HeroCards[1], heroCard1)

	// Draw Community cards (Flop 3 cards)
	cCard0 := table.Card{Rank: table.RankTen, Suit: table.Diamonds}
	cCard1 := table.Card{Rank: table.RankNine, Suit: table.Clubs}
	cCard2 := table.Card{Rank: table.RankTwo, Suit: table.Hearts}
	matcher.RegisterTemplate(cCard0, generateSyntheticCard(cCard0, 32, 48))
	matcher.RegisterTemplate(cCard1, generateSyntheticCard(cCard1, 32, 48))
	matcher.RegisterTemplate(cCard2, generateSyntheticCard(cCard2, 32, 48))

	pasteCard(cfg.CommunityCards[0], cCard0)
	pasteCard(cfg.CommunityCards[1], cCard1)
	pasteCard(cfg.CommunityCards[2], cCard2)

	// Paste Pot text into tableImg at cfg.Pot
	potImg := renderTextImage("Pot: 18.23", 2)
	potMinX := int(cfg.Pot.X * 1000)
	potMinY := int(cfg.Pot.Y * 800)
	draw.Draw(tableImg, image.Rect(potMinX, potMinY, potMinX+potImg.Bounds().Dx(), potMinY+potImg.Bounds().Dy()), potImg, image.Point{}, draw.Src)

	state, err := parser.ParseFrame(tableImg, cfg)
	if err != nil {
		t.Fatalf("unexpected error parsing frame: %v", err)
	}

	if state == nil {
		t.Fatal("expected non-nil HandState")
	}

	if state.Street != table.StreetFlop {
		t.Errorf("expected StreetFlop, got %s", state.Street)
	}
	if len(state.CommunityCards) != 3 {
		t.Errorf("expected 3 community cards, got %d", len(state.CommunityCards))
	}
	if state.HeroCards[0] != heroCard0 || state.HeroCards[1] != heroCard1 {
		t.Errorf("expected Hero cards [%s, %s], got [%s, %s]", heroCard0, heroCard1, state.HeroCards[0], state.HeroCards[1])
	}
}
