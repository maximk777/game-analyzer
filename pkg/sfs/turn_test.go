package sfs

import (
	"os"
	"testing"

	"poker-game-analyzer/pkg/table"
)

// heroSeatMsg is a seat update for the hero used by the turn tests.
func heroSeatMsg(caption, lastAction string) Message {
	return Message{Kind: KindSeat, Seat: &SeatMsg{
		SeatID:     3,
		UserID:     712757,
		UserName:   "hero",
		UserChips:  table.FromFloat(10),
		Caption:    caption,
		LastAction: lastAction,
		IsPlaying:  true,
	}}
}

func heroRoster() *Roster {
	r := NewRoster()
	r.HeroID = 712757
	r.HeroName = "hero"
	return r
}

// A fold badge belongs to the hand it was earned in. The wire leaves it on the
// seat until that seat next changes, and at a cash table hero's seat is not
// re-sent before hero's own turn -- so read into the new hand it said hero had
// folded before the cards were dealt, and the advisor refused to advise for
// exactly the decision the advice was wanted for.
func TestHandStartClearsLastHandsActionBadges(t *testing.T) {
	r := heroRoster()
	r.Apply(heroSeatMsg("Fold", "Fold"))

	if hs := r.HandState(); !hs.Seats[0].IsFolded {
		t.Fatal("seat should be folded while the hand it folded in is current")
	}

	r.Apply(Message{Kind: KindHandStart, HandStart: &HandStartMsg{
		GameID: 128005500406, SBAmount: table.FromFloat(0.05), BBAmount: table.FromFloat(0.1),
		DealerSeatID: 1, SBSeatID: 2, BBSeatID: 3,
	}})

	hs := r.HandState()
	if hs.Seats[0].IsFolded {
		t.Error("fold badge from the previous hand survived the hand boundary")
	}
	if hs.Seats[0].LastAction != "" {
		t.Errorf("last action = %q, want cleared at the hand boundary", hs.Seats[0].LastAction)
	}
}

// Sitting out is not something a player did this hand, so a hand boundary does
// not forget it: clearing it would put a player who is not in the game back in
// it until the server happened to mention them again.
func TestHandStartKeepsSeatStatusThatOutlivesTheHand(t *testing.T) {
	r := heroRoster()
	r.Apply(heroSeatMsg("Sitout", ""))
	r.Apply(Message{Kind: KindHandStart, HandStart: &HandStartMsg{GameID: 1}})

	if got := r.Seats[3].Status; got != "Sitout" {
		t.Errorf("status = %q, want Sitout kept across the hand boundary", got)
	}
}

// A new hand id in the action history is a hand boundary too -- it is how the
// hand is recognised when the hand-start message is missed -- so it clears the
// same state, including hole cards that would otherwise be shown for the wrong
// hand.
func TestNewHandIDInHistoryClearsPerHandState(t *testing.T) {
	r := heroRoster()
	r.Apply(heroSeatMsg("Fold", "Fold"))
	r.HeroCards = []table.Card{{Rank: 14, Suit: table.Spades}, {Rank: 13, Suit: table.Spades}}
	r.HandID = 128005500405

	r.Apply(Message{Kind: KindActionHistory, ActionHistory: &ActionHistoryMsg{
		History: []ActionEntry{{HandID: 128005500406, RoundName: "PREFLOP", Username: "x", Action: "FOLD"}},
	}})

	if len(r.HeroCards) != 0 {
		t.Errorf("hero cards = %v, want cleared for the new hand", r.HeroCards)
	}
	if hs := r.HandState(); hs.Seats[0].IsFolded {
		t.Error("fold badge from the previous hand survived a hand id change")
	}
}

// The client is also sent a pre-action panel -- check-fold, call-any, under its
// own option codes -- addressed to nobody, ahead of its turn. Read as hero's
// buttons it left a check on the panel for the rest of the hand: advice on
// every opponent's turn, priced off a roundMaxBet of zero.
func TestOfferAddressedToNobodyIsNotHerosButtons(t *testing.T) {
	r := heroRoster()
	r.Apply(heroSeatMsg("", ""))
	r.Apply(Message{Kind: KindTurnOptions, TurnOptions: &TurnOptionsMsg{
		WhoseTurn: "",
		UserTurnOptions: map[string][]table.Money{
			ActionCodeCheck: {0}, "8": {0}, "9": {0}, "10": {0},
		},
	}})

	hs := r.HandState()
	if len(hs.HeroButtons) != 0 {
		t.Errorf("hero buttons = %v, want none from an offer addressed to nobody", hs.HeroButtons)
	}
	if hs.HeroCanAct() {
		t.Error("hero can act on an offer that was not theirs")
	}
}

// An offer is one player's, on one street. Once the turn passes it says what
// the last decision cost, not what hero's costs.
func TestOfferExpiresWhenTheTurnPasses(t *testing.T) {
	r := heroRoster()
	r.Apply(heroSeatMsg("", ""))
	r.Apply(Message{Kind: KindTurnOptions, TurnOptions: &TurnOptionsMsg{
		WhoseTurn:   "hero",
		RoundMaxBet: table.FromFloat(0.5),
		UserTurnOptions: map[string][]table.Money{
			ActionCodeFold: {0}, ActionCodeCall: {table.FromFloat(0.5)},
			ActionCodeRaise: {table.FromFloat(1), table.FromFloat(10)},
		},
	}})

	hs := r.HandState()
	if !hs.IsHeroTurn || len(hs.HeroButtons) != 3 {
		t.Fatalf("hero's own offer not read: turn=%v buttons=%v", hs.IsHeroTurn, hs.HeroButtons)
	}
	if hs.CurrentBet != 0.5 {
		t.Errorf("current bet = %v, want 0.5 from hero's offer", hs.CurrentBet)
	}

	r.Apply(Message{Kind: KindTurn, Turn: &TurnMsg{WhoseTurn: "villain"}})

	hs = r.HandState()
	if hs.HeroCanAct() {
		t.Errorf("hero can still act after the turn passed: turn=%v buttons=%v", hs.IsHeroTurn, hs.HeroButtons)
	}
	if hs.CurrentBet != 0 {
		t.Errorf("current bet = %v, want 0 once hero's offer is spent", hs.CurrentBet)
	}
}

// A street change ends the offer as well: hero checking the flop is not hero
// facing a call on the turn.
func TestOfferExpiresOnStreetChange(t *testing.T) {
	r := heroRoster()
	r.Apply(heroSeatMsg("", ""))
	r.Street = "FLOP"
	r.Apply(Message{Kind: KindTurnOptions, TurnOptions: &TurnOptionsMsg{
		WhoseTurn:       "hero",
		UserTurnOptions: map[string][]table.Money{ActionCodeCheck: {0}},
	}})
	if !r.HandState().HeroCanAct() {
		t.Fatal("hero's own offer not read on the flop")
	}

	r.Apply(Message{Kind: KindPot, Pot: &PotMsg{
		TotalPotAmount: table.FromFloat(1), RoundName: "TURN",
	}})

	if hs := r.HandState(); hs.HeroCanAct() {
		t.Errorf("flop offer still standing on the turn: buttons=%v", hs.HeroButtons)
	}
}

// The whole of it on real traffic: replaying a captured session, hero may act
// only when the wire says it is hero's turn, and no hand opens with hero
// wearing the previous hand's fold badge.
func TestCaptureGivesHeroTheTurnOnlyWhenItIsTheirs(t *testing.T) {
	f, err := os.Open("../../testdata/sfs/session_hero.pcap")
	if err != nil {
		t.Skipf("fixture missing: %v", err)
	}
	defer f.Close()

	var canAct, wrongTurn, foldedAtHandStart, hands int
	lastHand := ""
	live := &Live{
		HeroName: "unusual05393719",
		OnState: func(hs *table.HandState) {
			if hs.HeroCanAct() {
				canAct++
				if !hs.IsHeroTurn {
					wrongTurn++
				}
			}
			if hs.HandID == lastHand {
				return
			}
			lastHand = hs.HandID
			hands++
			for _, s := range hs.Seats {
				if s.PlayerID == hs.HeroID && hs.HeroID != "" && s.IsFolded {
					foldedAtHandStart++
				}
			}
		},
	}
	if err := live.Run(f, nil); err != nil {
		t.Fatal(err)
	}

	if hands < 2 {
		t.Fatalf("hands seen = %d, want at least 2", hands)
	}
	if canAct == 0 {
		t.Fatal("hero never got a turn in a capture of hero's own session")
	}
	if wrongTurn != 0 {
		t.Errorf("%d states let hero act while the wire named somebody else", wrongTurn)
	}
	if foldedAtHandStart != 0 {
		t.Errorf("%d hands opened with hero already folded", foldedAtHandStart)
	}
}
