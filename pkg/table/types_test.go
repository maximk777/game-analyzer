package table

import (
	"encoding/json"
	"testing"
)

func TestPositionConstants(t *testing.T) {
	positions := []Position{PosBTN, PosSB, PosBB, PosUTG, PosMP, PosCO}
	expected := []string{"BTN", "SB", "BB", "UTG", "MP", "CO"}

	for i, p := range positions {
		if string(p) != expected[i] {
			t.Errorf("Position %v != %s", p, expected[i])
		}
	}
}

func TestStreetConstants(t *testing.T) {
	streets := []Street{StreetPreflop, StreetFlop, StreetTurn, StreetRiver, StreetShowdown}
	expected := []string{"preflop", "flop", "turn", "river", "showdown"}

	for i, s := range streets {
		if string(s) != expected[i] {
			t.Errorf("Street %v != %s", s, expected[i])
		}
	}
}

func TestActionTypeConstants(t *testing.T) {
	actions := []ActionType{ActionFold, ActionCheck, ActionCall, ActionBet, ActionRaise, ActionAllIn}
	expected := []string{"fold", "check", "call", "bet", "raise", "all_in"}

	for i, a := range actions {
		if string(a) != expected[i] {
			t.Errorf("ActionType %v != %s", a, expected[i])
		}
	}
}

func TestHandStateJSONSerialization(t *testing.T) {
	heroCard1 := Card{Rank: RankAce, Suit: Spades}
	heroCard2 := Card{Rank: RankKing, Suit: Hearts}
	comm1 := Card{Rank: RankQueen, Suit: Diamonds}
	comm2 := Card{Rank: RankJack, Suit: Clubs}
	comm3 := Card{Rank: RankTen, Suit: Spades}

	state := HandState{
		HandID:         "hand-12345",
		TableID:        "table-001",
		Street:         StreetFlop,
		Pot:            15.50,
		CurrentBet:     5.00,
		MinRaise:       10.00,
		CommunityCards: []Card{comm1, comm2, comm3},
		HeroID:         "player-hero",
		HeroCards:      [2]Card{heroCard1, heroCard2},
		Seats: []SeatState{
			{
				SeatNumber: 1,
				PlayerID:   "player-hero",
				PlayerName: "HeroUser",
				Stack:      95.0,
				CurrentBet: 5.0,
				IsActive:   true,
				IsFolded:   false,
				Position:   PosBTN,
			},
			{
				SeatNumber: 2,
				PlayerID:   "player-villain",
				PlayerName: "Villain1",
				Stack:      120.0,
				CurrentBet: 5.0,
				IsActive:   true,
				IsFolded:   false,
				Position:   PosBB,
			},
		},
		ActionHistory: []ActionRecord{
			{
				PlayerID: "player-hero",
				Street:   StreetPreflop,
				Action:   ActionRaise,
				Amount:   5.0,
			},
			{
				PlayerID: "player-villain",
				Street:   StreetPreflop,
				Action:   ActionCall,
				Amount:   5.0,
			},
		},
	}

	data, err := json.Marshal(state)
	if err != nil {
		t.Fatalf("failed to marshal HandState: %v", err)
	}

	var unmarshaled HandState
	if err := json.Unmarshal(data, &unmarshaled); err != nil {
		t.Fatalf("failed to unmarshal HandState: %v", err)
	}

	if unmarshaled.HandID != state.HandID {
		t.Errorf("HandID = %s, want %s", unmarshaled.HandID, state.HandID)
	}
	if unmarshaled.Street != state.Street {
		t.Errorf("Street = %s, want %s", unmarshaled.Street, state.Street)
	}
	if unmarshaled.Pot != state.Pot {
		t.Errorf("Pot = %f, want %f", unmarshaled.Pot, state.Pot)
	}
	if len(unmarshaled.CommunityCards) != 3 {
		t.Errorf("CommunityCards len = %d, want 3", len(unmarshaled.CommunityCards))
	}
	if len(unmarshaled.Seats) != 2 {
		t.Errorf("Seats len = %d, want 2", len(unmarshaled.Seats))
	}
	if len(unmarshaled.ActionHistory) != 2 {
		t.Errorf("ActionHistory len = %d, want 2", len(unmarshaled.ActionHistory))
	}
}
