package vision

import (
	"errors"
	"fmt"
	"image"

	"poker-game-analyzer/pkg/table"
)

// VisionEventType represents the type of table vision event.
type VisionEventType string

const (
	EventHandStart    VisionEventType = "HandStart"
	EventCardDealt    VisionEventType = "CardDealt"
	EventPlayerAction VisionEventType = "PlayerAction"
	EventHeroTurn     VisionEventType = "HeroTurn"
	EventHandEnd      VisionEventType = "HandEnd"
)

// VisionEvent encapsulates a detected game state change event.
type VisionEvent struct {
	Type        VisionEventType  `json:"type"`
	TableID     string           `json:"table_id"`
	HandState   *table.HandState `json:"hand_state"`
	Description string           `json:"description,omitempty"`
	SeatNumber  int              `json:"seat_number,omitempty"`
	PlayerID    string           `json:"player_id,omitempty"`
	Action      table.ActionType `json:"action,omitempty"`
	Amount      float64          `json:"amount,omitempty"`
}

// FrameParser parses video frames into table.HandState using ROI and OCR/Matchers.
type FrameParser struct {
	matcher *CardMatcher
	ocr     *TextOCR
}

// NewFrameParser creates a new FrameParser.
func NewFrameParser(matcher *CardMatcher, ocr *TextOCR) *FrameParser {
	if matcher == nil {
		matcher = NewDefaultCardMatcher()
	}
	if ocr == nil {
		ocr = NewTextOCR()
	}
	return &FrameParser{
		matcher: matcher,
		ocr:     ocr,
	}
}

// ParseFrame extracts the complete HandState from a table screenshot using the given ROI layout.
func (p *FrameParser) ParseFrame(img image.Image, cfg ROIConfig) (*table.HandState, error) {
	if img == nil {
		return nil, errors.New("nil image provided to ParseFrame")
	}

	state := &table.HandState{
		HandID:         "live-hand",
		TableID:        "coinpoker-table-1",
		Street:         table.StreetPreflop,
		CommunityCards: make([]table.Card, 0, 5),
		Seats:          make([]table.SeatState, 0, len(cfg.Seats)),
	}

	// 1. Parse Hero Cards
	var heroCards [2]table.Card
	for i := 0; i < 2 && i < len(cfg.HeroCards); i++ {
		crop := cfg.HeroCards[i].Crop(img)
		if crop != nil {
			if card, _, err := p.matcher.MatchCard(crop); err == nil {
				heroCards[i] = card
			}
		}
	}
	state.HeroCards = heroCards
	state.HeroID = "seat-0"

	// 2. Parse Community Cards
	for i := 0; i < 5 && i < len(cfg.CommunityCards); i++ {
		crop := cfg.CommunityCards[i].Crop(img)
		if crop != nil {
			if card, _, err := p.matcher.MatchCard(crop); err == nil {
				state.CommunityCards = append(state.CommunityCards, card)
			}
		}
	}

	// 3. Determine Street based on board count
	switch len(state.CommunityCards) {
	case 0:
		state.Street = table.StreetPreflop
	case 3:
		state.Street = table.StreetFlop
	case 4:
		state.Street = table.StreetTurn
	case 5:
		state.Street = table.StreetRiver
	default:
		state.Street = table.StreetPreflop
	}

	// 4. Parse Pot
	potCrop := cfg.Pot.Crop(img)
	if potCrop != nil {
		if potVal, err := p.ocr.ParseNumber(potCrop); err == nil {
			state.Pot = potVal
		}
	}

	// 5. Parse Seats
	var maxBet float64
	for _, seatROI := range cfg.Seats {
		seat := table.SeatState{
			SeatNumber: seatROI.SeatNumber,
			PlayerID:   fmt.Sprintf("player-%d", seatROI.SeatNumber),
			PlayerName: fmt.Sprintf("Player %d", seatROI.SeatNumber),
			IsActive:   true,
			IsFolded:   false,
		}

		if seatROI.IsHero {
			seat.PlayerID = "player-0"
			seat.PlayerName = "Hero"
			state.HeroID = seat.PlayerID
		}

		// Parse Player Name if present
		nameCrop := seatROI.Nameplate.Crop(img)
		if nameCrop != nil {
			if nameStr, err := p.ocr.ParseString(nameCrop); err == nil && len(nameStr) > 0 {
				seat.PlayerName = nameStr
				if !seatROI.IsHero {
					seat.PlayerID = nameStr
				}
			}
		}

		// Parse Stack
		stackCrop := seatROI.Stack.Crop(img)
		if stackCrop != nil {
			if stackVal, err := p.ocr.ParseNumber(stackCrop); err == nil {
				seat.Stack = stackVal
			}
		}

		// Parse Bet
		betCrop := seatROI.Bet.Crop(img)
		if betCrop != nil {
			if betVal, err := p.ocr.ParseNumber(betCrop); err == nil {
				seat.CurrentBet = betVal
				if betVal > maxBet {
					maxBet = betVal
				}
			}
		}

		state.Seats = append(state.Seats, seat)
	}

	state.CurrentBet = maxBet
	state.MinRaise = maxBet * 2.0

	return state, nil
}

// StateDiffer tracks frame state transitions and emits semantic game events.
type StateDiffer struct{}

// NewStateDiffer creates a new StateDiffer.
func NewStateDiffer() *StateDiffer {
	return &StateDiffer{}
}

// DetectEvents compares previous and current HandState and returns a list of VisionEvents.
func (d *StateDiffer) DetectEvents(prev, curr *table.HandState) []VisionEvent {
	if curr == nil {
		return nil
	}

	var events []VisionEvent

	// 1. Initial State / Hand Start
	if prev == nil || prev.HandID != curr.HandID {
		events = append(events, VisionEvent{
			Type:        EventHandStart,
			TableID:     curr.TableID,
			HandState:   curr,
			Description: fmt.Sprintf("Hand %s started", curr.HandID),
		})

		if len(curr.CommunityCards) > 0 {
			events = append(events, VisionEvent{
				Type:        EventCardDealt,
				TableID:     curr.TableID,
				HandState:   curr,
				Description: fmt.Sprintf("Community cards dealt: %d cards", len(curr.CommunityCards)),
			})
		}
		return events
	}

	// 2. Community Cards Dealt
	if len(curr.CommunityCards) > len(prev.CommunityCards) {
		events = append(events, VisionEvent{
			Type:        EventCardDealt,
			TableID:     curr.TableID,
			HandState:   curr,
			Description: fmt.Sprintf("New street %s, %d board cards", curr.Street, len(curr.CommunityCards)),
		})
	}

	// 3. Player Actions
	prevSeatMap := make(map[int]table.SeatState)
	for _, s := range prev.Seats {
		prevSeatMap[s.SeatNumber] = s
	}

	for _, currSeat := range curr.Seats {
		prevSeat, exists := prevSeatMap[currSeat.SeatNumber]
		if !exists {
			continue
		}

		// Folded action
		if !prevSeat.IsFolded && currSeat.IsFolded {
			events = append(events, VisionEvent{
				Type:        EventPlayerAction,
				TableID:     curr.TableID,
				HandState:   curr,
				SeatNumber:  currSeat.SeatNumber,
				PlayerID:    currSeat.PlayerID,
				Action:      table.ActionFold,
				Description: fmt.Sprintf("%s folded", currSeat.PlayerName),
			})
		} else if currSeat.CurrentBet > prevSeat.CurrentBet {
			// Bet or Raise action
			var act table.ActionType = table.ActionBet
			if prev.CurrentBet > 0 {
				act = table.ActionRaise
			}
			diff := currSeat.CurrentBet - prevSeat.CurrentBet

			events = append(events, VisionEvent{
				Type:        EventPlayerAction,
				TableID:     curr.TableID,
				HandState:   curr,
				SeatNumber:  currSeat.SeatNumber,
				PlayerID:    currSeat.PlayerID,
				Action:      act,
				Amount:      diff,
				Description: fmt.Sprintf("%s %s to %.2f", currSeat.PlayerName, act, currSeat.CurrentBet),
			})
		}
	}

	// 4. Hand End / Showdown
	if curr.Street == table.StreetShowdown || (prev.Pot > 0 && curr.Pot == 0) {
		events = append(events, VisionEvent{
			Type:        EventHandEnd,
			TableID:     curr.TableID,
			HandState:   curr,
			Description: fmt.Sprintf("Hand %s completed", curr.HandID),
		})
	}

	return events
}

// DetectHeroTurn creates a HeroTurn event when hero action is detected.
func (d *StateDiffer) DetectHeroTurn(curr *table.HandState, isHeroTurn bool) []VisionEvent {
	if !isHeroTurn || curr == nil {
		return nil
	}

	return []VisionEvent{
		{
			Type:        EventHeroTurn,
			TableID:     curr.TableID,
			HandState:   curr,
			PlayerID:    curr.HeroID,
			Description: "Hero turn to act",
		},
	}
}
