package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"math/rand"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"poker-game-analyzer/pkg/table"
	"poker-game-analyzer/pkg/vision"
)

var defaultPlayerNames = []string{
	"Hero",
	"mamayazareyzil",
	"AbdulTaxi",
	"mko6969",
	"HundPatron",
	"internazional",
	"glorious9677862",
}

// SimulatorConfig contains parameters controlling the synthetic game generation.
type SimulatorConfig struct {
	ServerURL string
	TableID   string
	Hands     int
	SpeedMS   int
	HeroSeat  int
}

// Simulator generates synthetic poker hands and streams them to the analyzer server.
type Simulator struct {
	cfg        SimulatorConfig
	httpClient *http.Client
	rng        *rand.Rand
}

// NewSimulator creates a new Simulator instance.
func NewSimulator(cfg SimulatorConfig) *Simulator {
	return &Simulator{
		cfg: cfg,
		httpClient: &http.Client{
			Timeout: 5 * time.Second,
		},
		rng: rand.New(rand.NewSource(time.Now().UnixNano())),
	}
}

// PostEvent sends a VisionEvent to the server's event ingestion API.
func (s *Simulator) PostEvent(ctx context.Context, event vision.VisionEvent) error {
	url := fmt.Sprintf("%s/api/v1/tables/%s/events", strings.TrimSuffix(s.cfg.ServerURL, "/"), s.cfg.TableID)

	data, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("failed to marshal vision event: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("failed to create http request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("http post failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("server returned non-200 code (%d): %s", resp.StatusCode, string(body))
	}

	return nil
}

func (s *Simulator) generateFullDeck() []table.Card {
	var deck []table.Card
	for rank := table.RankTwo; rank <= table.RankAce; rank++ {
		for suit := table.Spades; suit <= table.Clubs; suit++ {
			deck = append(deck, table.Card{Rank: rank, Suit: suit})
		}
	}

	// Fisher-Yates shuffle
	s.rng.Shuffle(len(deck), func(i, j int) {
		deck[i], deck[j] = deck[j], deck[i]
	})

	return deck
}

func (s *Simulator) sleep(ctx context.Context) {
	if s.cfg.SpeedMS <= 0 {
		return
	}
	select {
	case <-ctx.Done():
	case <-time.After(time.Duration(s.cfg.SpeedMS) * time.Millisecond):
	}
}

// Run executes the simulation loop for the configured number of hands or until context is canceled.
func (s *Simulator) Run(ctx context.Context) error {
	log.Printf("[SIMULATOR] Connecting to server at %s for table %s...", s.cfg.ServerURL, s.cfg.TableID)

	positions := []table.Position{table.PosBTN, table.PosSB, table.PosBB, table.PosUTG, table.PosMP, table.PosCO}

	// Player initial stacks
	stacks := make([]float64, 6)
	for i := range stacks {
		stacks[i] = 200.0 // 200 BBs
	}

	handCount := 0
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}

		if s.cfg.Hands > 0 && handCount >= s.cfg.Hands {
			log.Printf("[SIMULATOR] Completed %d requested simulation hands.", handCount)
			break
		}

		handCount++
		handID := fmt.Sprintf("sim-hand-%d-%d", time.Now().Unix(), handCount)
		log.Printf("\n[SIMULATOR] === Starting Hand #%d (%s) ===", handCount, handID)

		deck := s.generateFullDeck()
		deckIdx := 0

		dealCard := func() table.Card {
			c := deck[deckIdx]
			deckIdx++
			return c
		}

		// Configure 6 Seats
		heroID := fmt.Sprintf("player-%d", s.cfg.HeroSeat)
		seats := make([]table.SeatState, 6)
		for i := 0; i < 6; i++ {
			pName := defaultPlayerNames[i%len(defaultPlayerNames)]
			if i == s.cfg.HeroSeat {
				pName = "Hero"
			}
			pID := fmt.Sprintf("player-%d", i)
			pos := positions[(i+handCount)%6]

			if stacks[i] < 20.0 {
				stacks[i] = 200.0 // Reload
			}

			seats[i] = table.SeatState{
				SeatNumber: i,
				PlayerID:   pID,
				PlayerName: pName,
				Stack:      stacks[i],
				CurrentBet: 0.0,
				IsActive:   true,
				IsFolded:   false,
				Position:   pos,
			}
		}

		// Blinds
		sbIdx := (handCount + 1) % 6
		bbIdx := (handCount + 2) % 6

		seats[sbIdx].CurrentBet = 0.50
		seats[sbIdx].Stack -= 0.50

		seats[bbIdx].CurrentBet = 1.00
		seats[bbIdx].Stack -= 1.00

		heroHoleCards := [2]table.Card{dealCard(), dealCard()}

		handState := &table.HandState{
			HandID:         handID,
			TableID:        s.cfg.TableID,
			Street:         table.StreetPreflop,
			Pot:            1.50,
			CurrentBet:     1.00,
			MinRaise:       2.00,
			HeroID:         heroID,
			HeroCards:      heroHoleCards,
			CommunityCards: make([]table.Card, 0, 5),
			Seats:          seats,
			ActionHistory:  make([]table.ActionRecord, 0),
		}

		// 1. Hand Start Event
		err := s.PostEvent(ctx, vision.VisionEvent{
			Type:        vision.EventHandStart,
			TableID:     s.cfg.TableID,
			HandState:   handState,
			Description: fmt.Sprintf("Hand %s started. Hero dealt [%s, %s]", handID, heroHoleCards[0], heroHoleCards[1]),
		})
		if err != nil {
			log.Printf("[SIMULATOR] Error posting HandStart: %v", err)
		}
		s.sleep(ctx)

		// 2. Preflop Betting Round
		s.simulateBettingRound(ctx, handState, table.StreetPreflop)

		// 3. Flop
		if s.activePlayerCount(handState) > 1 {
			handState.Street = table.StreetFlop
			handState.CommunityCards = append(handState.CommunityCards, dealCard(), dealCard(), dealCard())
			s.resetCurrentBets(handState)

			_ = s.PostEvent(ctx, vision.VisionEvent{
				Type:        vision.EventCardDealt,
				TableID:     s.cfg.TableID,
				HandState:   handState,
				Description: fmt.Sprintf("Flop dealt: [%s, %s, %s] (Pot: $%.2f)", handState.CommunityCards[0], handState.CommunityCards[1], handState.CommunityCards[2], handState.Pot),
			})
			s.sleep(ctx)

			s.simulateBettingRound(ctx, handState, table.StreetFlop)
		}

		// 4. Turn
		if s.activePlayerCount(handState) > 1 {
			handState.Street = table.StreetTurn
			handState.CommunityCards = append(handState.CommunityCards, dealCard())
			s.resetCurrentBets(handState)

			_ = s.PostEvent(ctx, vision.VisionEvent{
				Type:        vision.EventCardDealt,
				TableID:     s.cfg.TableID,
				HandState:   handState,
				Description: fmt.Sprintf("Turn dealt: [%s] (Pot: $%.2f)", handState.CommunityCards[3], handState.Pot),
			})
			s.sleep(ctx)

			s.simulateBettingRound(ctx, handState, table.StreetTurn)
		}

		// 5. River
		if s.activePlayerCount(handState) > 1 {
			handState.Street = table.StreetRiver
			handState.CommunityCards = append(handState.CommunityCards, dealCard())
			s.resetCurrentBets(handState)

			_ = s.PostEvent(ctx, vision.VisionEvent{
				Type:        vision.EventCardDealt,
				TableID:     s.cfg.TableID,
				HandState:   handState,
				Description: fmt.Sprintf("River dealt: [%s] (Pot: $%.2f)", handState.CommunityCards[4], handState.Pot),
			})
			s.sleep(ctx)

			s.simulateBettingRound(ctx, handState, table.StreetRiver)
		}

		// 6. Showdown / Hand End
		handState.Street = table.StreetShowdown
		s.resetCurrentBets(handState)

		// Award pot to a winner
		winnerSeat := s.determineWinnerSeat(handState)
		if winnerSeat >= 0 {
			handState.Seats[winnerSeat].Stack += handState.Pot
			for i := range stacks {
				stacks[i] = handState.Seats[i].Stack
			}
		}

		_ = s.PostEvent(ctx, vision.VisionEvent{
			Type:        vision.EventHandEnd,
			TableID:     s.cfg.TableID,
			HandState:   handState,
			Description: fmt.Sprintf("Hand %s ended. Pot $%.2f awarded to %s.", handID, handState.Pot, handState.Seats[winnerSeat].PlayerName),
		})
		s.sleep(ctx)
	}

	return nil
}

func (s *Simulator) activePlayerCount(h *table.HandState) int {
	cnt := 0
	for _, seat := range h.Seats {
		if seat.IsActive && !seat.IsFolded {
			cnt++
		}
	}
	return cnt
}

func (s *Simulator) resetCurrentBets(h *table.HandState) {
	for i := range h.Seats {
		h.Seats[i].CurrentBet = 0.0
	}
	h.CurrentBet = 0.0
	h.MinRaise = 2.00
}

func (s *Simulator) determineWinnerSeat(h *table.HandState) int {
	for i, seat := range h.Seats {
		if seat.IsActive && !seat.IsFolded {
			return i
		}
	}
	return 0
}

func (s *Simulator) simulateBettingRound(ctx context.Context, h *table.HandState, street table.Street) {
	for i := 0; i < len(h.Seats); i++ {
		seat := &h.Seats[i]
		if !seat.IsActive || seat.IsFolded {
			continue
		}
		if s.activePlayerCount(h) <= 1 {
			return
		}

		// Check if it's Hero's turn
		if seat.PlayerID == h.HeroID {
			_ = s.PostEvent(ctx, vision.VisionEvent{
				Type:        vision.EventHeroTurn,
				TableID:     s.cfg.TableID,
				HandState:   h,
				PlayerID:    h.HeroID,
				Description: fmt.Sprintf("Hero turn to act on %s facing $%.2f bet", street, h.CurrentBet),
			})
			s.sleep(ctx)

			// Hero makes standard action
			toCall := h.CurrentBet - seat.CurrentBet
			if toCall <= 0 {
				// Check
				h.ActionHistory = append(h.ActionHistory, table.ActionRecord{
					PlayerID: h.HeroID,
					Street:   street,
					Action:   table.ActionCheck,
					Amount:   0,
				})
			} else {
				// Call
				callAmt := toCall
				if callAmt > seat.Stack {
					callAmt = seat.Stack
				}
				seat.CurrentBet += callAmt
				seat.Stack -= callAmt
				h.Pot += callAmt

				h.ActionHistory = append(h.ActionHistory, table.ActionRecord{
					PlayerID: h.HeroID,
					Street:   street,
					Action:   table.ActionCall,
					Amount:   callAmt,
				})

				_ = s.PostEvent(ctx, vision.VisionEvent{
					Type:        vision.EventPlayerAction,
					TableID:     s.cfg.TableID,
					HandState:   h,
					SeatNumber:  seat.SeatNumber,
					PlayerID:    seat.PlayerID,
					Action:      table.ActionCall,
					Amount:      callAmt,
					Description: fmt.Sprintf("Hero called $%.2f", callAmt),
				})
			}
			s.sleep(ctx)
			continue
		}

		// Opponent decision heuristics based on name/archetype
		toCall := h.CurrentBet - seat.CurrentBet
		r := s.rng.Float64()

		if toCall == 0 {
			// Check or Bet
			if r < 0.35 && street != table.StreetPreflop {
				// Bet $2.00 or 50% Pot
				betAmt := 2.00
				if h.Pot > 4.0 {
					betAmt = float64(int(h.Pot * 0.5))
				}
				if betAmt > seat.Stack {
					betAmt = seat.Stack
				}

				seat.CurrentBet += betAmt
				seat.Stack -= betAmt
				h.Pot += betAmt
				h.CurrentBet = seat.CurrentBet
				h.MinRaise = h.CurrentBet * 2.0

				h.ActionHistory = append(h.ActionHistory, table.ActionRecord{
					PlayerID: seat.PlayerID,
					Street:   street,
					Action:   table.ActionBet,
					Amount:   betAmt,
				})

				_ = s.PostEvent(ctx, vision.VisionEvent{
					Type:        vision.EventPlayerAction,
					TableID:     s.cfg.TableID,
					HandState:   h,
					SeatNumber:  seat.SeatNumber,
					PlayerID:    seat.PlayerID,
					Action:      table.ActionBet,
					Amount:      betAmt,
					Description: fmt.Sprintf("%s bet $%.2f", seat.PlayerName, betAmt),
				})
			} else {
				// Check
				h.ActionHistory = append(h.ActionHistory, table.ActionRecord{
					PlayerID: seat.PlayerID,
					Street:   street,
					Action:   table.ActionCheck,
					Amount:   0,
				})
			}
		} else {
			// Facing a bet: Call, Raise, or Fold
			if r < 0.20 && street == table.StreetPreflop {
				// Fold
				seat.IsFolded = true
				h.ActionHistory = append(h.ActionHistory, table.ActionRecord{
					PlayerID: seat.PlayerID,
					Street:   street,
					Action:   table.ActionFold,
					Amount:   0,
				})

				_ = s.PostEvent(ctx, vision.VisionEvent{
					Type:        vision.EventPlayerAction,
					TableID:     s.cfg.TableID,
					HandState:   h,
					SeatNumber:  seat.SeatNumber,
					PlayerID:    seat.PlayerID,
					Action:      table.ActionFold,
					Description: fmt.Sprintf("%s folded", seat.PlayerName),
				})
			} else {
				// Call
				callAmt := toCall
				if callAmt > seat.Stack {
					callAmt = seat.Stack
				}
				seat.CurrentBet += callAmt
				seat.Stack -= callAmt
				h.Pot += callAmt

				h.ActionHistory = append(h.ActionHistory, table.ActionRecord{
					PlayerID: seat.PlayerID,
					Street:   street,
					Action:   table.ActionCall,
					Amount:   callAmt,
				})

				_ = s.PostEvent(ctx, vision.VisionEvent{
					Type:        vision.EventPlayerAction,
					TableID:     s.cfg.TableID,
					HandState:   h,
					SeatNumber:  seat.SeatNumber,
					PlayerID:    seat.PlayerID,
					Action:      table.ActionCall,
					Amount:      callAmt,
					Description: fmt.Sprintf("%s called $%.2f", seat.PlayerName, callAmt),
				})
			}
		}

		s.sleep(ctx)
	}
}

func main() {
	var (
		serverURL = flag.String("server-url", "http://localhost:8080", "Server base URL")
		tableID   = flag.String("table-id", "table-1", "Table identifier to simulate")
		hands     = flag.Int("hands", 10, "Number of hands to simulate (0 = infinite)")
		speedMS   = flag.Int("speed-ms", 1000, "Millisecond delay between poker actions")
		heroSeat  = flag.Int("hero-seat", 0, "Seat number assigned to Hero (0-5)")
	)
	flag.Parse()

	log.Printf("[SIMULATOR] Initializing Synthetic Game Generator...")
	log.Printf("[SIMULATOR] Target: %s | Table: %s | Hands: %d | Speed: %dms | Hero Seat: %d",
		*serverURL, *tableID, *hands, *speedMS, *heroSeat)

	sim := NewSimulator(SimulatorConfig{
		ServerURL: *serverURL,
		TableID:   *tableID,
		Hands:     *hands,
		SpeedMS:   *speedMS,
		HeroSeat:  *heroSeat,
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)

	go func() {
		<-sigCh
		log.Printf("[SIMULATOR] Interrupt received. Stopping simulation gracefully...")
		cancel()
	}()

	if err := sim.Run(ctx); err != nil && err != context.Canceled {
		log.Fatalf("[SIMULATOR] Simulation ended with error: %v", err)
	}

	log.Printf("[SIMULATOR] Simulation completed.")
}
