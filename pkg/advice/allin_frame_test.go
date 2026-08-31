package advice

import (
	"encoding/json"
	"testing"

	"poker-game-analyzer/pkg/table"
)

// The frame behind the all-in screenshot of 2026-09-01, exactly as the screen
// reader produced it -- bin/parse_image on testdata/coinpoker_allin_seat_sample.png.
//
// Hero holds 3h2d in the big blind. mike8989 is all-in for 1,420,000 and the
// client's button reads "Call 181,840", which is hero's whole stack. The tool
// said CHECK, "nothing to pay", because the all-in player was not a seat: their
// stack renders as a lone "0", the recogniser does not return it, and a
// nameplate with no number under it was not counted as a player. With that seat
// gone the largest bet on the table was the big blind hero had already posted.
//
// The frame is decoded straight into a HandState, which is how the flat payload
// from the reader actually arrives at the server.
const allInFrameJSON = `{
 "table_id": "NLH 1237978 - 1K/2K (320)",
 "street": "preflop",
 "pot": 1420000,
 "current_bet": 1420000,
 "min_raise": 0,
 "small_blind": 1000,
 "big_blind": 2000,
 "hero_id": "ludoStarik",
 "hero_cards": [
  "3h",
  "2d"
 ],
 "is_hero_turn": true,
 "hero_buttons": [
  "fold",
  "call"
 ],
 "community_cards": [],
 "seats": [
  {
   "seat_number": 1,
   "player_id": "mike8989",
   "player_name": "mike8989",
   "stack": 0,
   "current_bet": 1420000,
   "is_active": true,
   "is_folded": false,
   "position": "SB",
   "last_action": "all-in"
  },
  {
   "seat_number": 2,
   "player_id": "prey78",
   "player_name": "prey78",
   "stack": 695504,
   "current_bet": 2000,
   "is_active": true,
   "is_folded": false,
   "position": "BTN",
   "last_action": "call"
  },
  {
   "seat_number": 3,
   "player_id": "mature0977813...",
   "player_name": "mature0977813...",
   "stack": 77680,
   "current_bet": 2000,
   "is_active": true,
   "is_folded": false,
   "position": "CO",
   "last_action": "call"
  },
  {
   "seat_number": 4,
   "player_id": "Dukex1701",
   "player_name": "Dukex1701",
   "stack": 200000,
   "current_bet": 0,
   "is_active": true,
   "is_folded": false,
   "position": "MP",
   "last_action": ""
  },
  {
   "seat_number": 4,
   "player_id": "GerodotPH",
   "player_name": "GerodotPH",
   "stack": 100000,
   "current_bet": 0,
   "is_active": true,
   "is_folded": false,
   "position": "UTG",
   "last_action": ""
  },
  {
   "seat_number": 5,
   "player_id": "ludoStarik",
   "player_name": "ludoStarik",
   "stack": 181840,
   "current_bet": 2000,
   "is_active": true,
   "is_folded": false,
   "position": "BB",
   "last_action": ""
  }
 ]
}`

func TestAllInFrame_HeroIsFacingABetNotAFreeCheck(t *testing.T) {
	var state table.HandState
	if err := json.Unmarshal([]byte(allInFrameJSON), &state); err != nil {
		t.Fatalf("decoding the recorded frame: %v", err)
	}

	if state.BigBlind != 2000 {
		t.Errorf("big blind = %.0f, want 2000 -- the stake is dropped at the boundary again",
			state.BigBlind)
	}

	var allIn, hero *table.SeatState
	for i := range state.Seats {
		switch state.Seats[i].PlayerID {
		case "mike8989":
			allIn = &state.Seats[i]
		case state.HeroID:
			hero = &state.Seats[i]
		}
	}
	if allIn == nil {
		t.Fatal("the all-in player is not a seat, so there is nothing to call")
	}
	if hero == nil {
		t.Fatal("hero matches no seat")
	}

	toCall := allIn.CurrentBet - hero.CurrentBet
	if toCall <= 0 {
		t.Fatalf("hero owes %.0f; the client's button read 181,840", toCall)
	}

	res := Evaluate(&state, Reads{}, Options{Iterations: 400, VsTopIterations: 200})
	if res.Recommendation == nil {
		t.Fatalf("no advice: %q", res.NoAdvice)
	}
	if res.Recommendation.PrimaryAction == table.ActionCheck {
		t.Errorf("advised check facing an all-in of %.0f: %s",
			allIn.CurrentBet, res.Recommendation.Reasoning)
	}
	// 3h2d against an all-in for the stack is a fold, and the tool should say so
	// rather than merely avoiding the word check.
	if res.Recommendation.PrimaryAction != table.ActionFold {
		t.Errorf("advised %s %.0f with 3h2d facing an all-in; want fold. %s",
			res.Recommendation.PrimaryAction, res.Recommendation.RecommendedAmount,
			res.Recommendation.Reasoning)
	}
}
