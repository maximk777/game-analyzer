package advisor

import (
	"testing"

	"poker-game-analyzer/pkg/equity"
	"poker-game-analyzer/pkg/table"
)

func board(t *testing.T, s string) []table.Card {
	t.Helper()
	cards, err := table.ParseCards(s)
	if err != nil {
		t.Fatalf("board %q: %v", s, err)
	}
	return cards
}

func TestReadTexture(t *testing.T) {
	cases := []struct {
		board     string
		paired    bool
		monotone  bool
		connected bool
		// wetter is a board this one must be drier than, or "" for none.
		drierThan string
	}{
		{board: "As 7d 2c", drierThan: "Jh Th 9h"},
		{board: "Kd Kc 7s", paired: true, drierThan: "9h 8h 7c"},
		{board: "Jh Th 9h", monotone: true, connected: true},
		{board: "9h 8h 7c", connected: true, drierThan: "Jh Th 9h"},
		{board: "Qs 7d 2c", drierThan: "9h 8h 7c"},
	}

	for _, c := range cases {
		t.Run(c.board, func(t *testing.T) {
			got := ReadTexture(board(t, c.board))
			if got.Paired != c.paired {
				t.Errorf("paired = %v, want %v", got.Paired, c.paired)
			}
			if got.Monotone != c.monotone {
				t.Errorf("monotone = %v, want %v", got.Monotone, c.monotone)
			}
			if got.Connected != c.connected {
				t.Errorf("connected = %v, want %v", got.Connected, c.connected)
			}
			if c.drierThan != "" {
				other := ReadTexture(board(t, c.drierThan))
				if got.Wet >= other.Wet {
					t.Errorf("wetness %.2f is not below %s at %.2f", got.Wet, c.drierThan, other.Wet)
				}
			}
		})
	}
}

// A dry board gets small sizes and a wet one gets large ones. This is the whole
// of what the policy is for.
func TestPolicySizesFollowTheBoard(t *testing.T) {
	dry := PolicyFor(table.StreetFlop, board(t, "As 7d 2c"), 8, 1, 0.4)
	wet := PolicyFor(table.StreetFlop, board(t, "Jh Th 9h"), 8, 1, 0.4)

	if dry.Fractions[0] > 0.30 {
		t.Errorf("smallest size on a dry board is %.2f, want a quarter-pot option", dry.Fractions[0])
	}
	if wet.Fractions[0] <= dry.Fractions[0] {
		t.Errorf("wet board's smallest size %.2f is not above the dry board's %.2f",
			wet.Fractions[0], dry.Fractions[0])
	}
	if wet.Fractions[len(wet.Fractions)-1] <= dry.Fractions[len(dry.Fractions)-1] {
		t.Errorf("wet board's largest size %.2f is not above the dry board's %.2f",
			wet.Fractions[len(wet.Fractions)-1], dry.Fractions[len(dry.Fractions)-1])
	}
}

// The measured leak: a hundred big blinds shoved into a pot of seven, priced by
// a model that cannot see the streets it is skipping. Deep, with a hand that is
// not ahead of what calls, the stack must not be on the menu at all.
func TestNoShoveDeepWithoutAHand(t *testing.T) {
	deep := PolicyFor(table.StreetFlop, board(t, "As 7d 2c"), 13, 1, 0.45)
	if deep.AllIn {
		t.Fatal("offered the stack at SPR 13 with 45% against the calling range")
	}

	strong := PolicyFor(table.StreetFlop, board(t, "As 7d 2c"), 13, 1, 0.85)
	if !strong.AllIn {
		t.Fatal("refused the stack at SPR 13 with 85% against the calling range")
	}

	committed := PolicyFor(table.StreetFlop, board(t, "As 7d 2c"), 1.2, 1, 0.45)
	if !committed.AllIn {
		t.Fatal("refused the stack at SPR 1.2, where the money is going in anyway")
	}
}

// Multiway everything tightens: a second caller is a second range to beat.
func TestMultiwayTightens(t *testing.T) {
	heads := PolicyFor(table.StreetFlop, board(t, "As 7d 2c"), 13, 1, 0.80)
	three := PolicyFor(table.StreetFlop, board(t, "As 7d 2c"), 13, 2, 0.80)

	if len(three.Fractions) >= len(heads.Fractions) {
		t.Errorf("three-handed offers %d sizes, heads-up %d", len(three.Fractions), len(heads.Fractions))
	}
	if three.AllIn {
		t.Error("offered the stack three-handed at SPR 13 on 80% against one calling range")
	}
	if !heads.AllIn {
		t.Error("refused the stack heads-up on 80% against the calling range")
	}
}

// Without a simulator there is no equity against a calling range, and then the
// stack is offered only where the pot has already committed it. Nothing is
// invented from the absence of a number.
func TestNoEquityMeansNoShoveOnEquity(t *testing.T) {
	if p := PolicyFor(table.StreetFlop, board(t, "As 7d 2c"), 9, 1, 0); p.AllIn {
		t.Fatal("offered the stack with no measurement behind it")
	}
}

// Without a counted read on every live opponent the tool could not bet below
// 50% equity at all, so it never bluffed and never semi-bluffed -- live, where
// the reads are empty for the first orbit, that is every hand. The switch opens
// the branch for a hand with something to improve to, and only for that.
func TestSemiBluffOpensTheAggressiveBranch(t *testing.T) {
	state := table.HandState{
		Street: table.StreetFlop, Pot: 20, CurrentBet: 0, SmallBlind: 1, BigBlind: 2,
		HeroID:         "hero",
		HeroCards:      [2]table.Card{{Rank: table.RankNine, Suit: table.Hearts}, {Rank: table.RankEight, Suit: table.Hearts}},
		CommunityCards: parseBoardCards(t, "Ah 5h 2c"),
		Seats: []table.SeatState{
			{PlayerID: "hero", Position: table.PosBTN, Stack: 100, IsActive: true},
			{PlayerID: "villain", Position: table.PosBB, Stack: 100, IsActive: true},
		},
	}

	// A flush draw: behind the 50% gate, and not behind by much against what
	// would actually call. Betting it is worth more than checking it, and the
	// gate is the only thing that was stopping the tool from finding that out.
	in := Inputs{
		State:       state,
		Equity:      equity.EquityResult{WinRate: 0.45},
		EquityVsTop: func(float64) float64 { return 0.44 },
	}

	shut := Calculate(in)
	if isAggressive(shut.PrimaryAction) {
		t.Fatalf("bet %v with the branch shut", shut.PrimaryAction)
	}

	in.AllowSemiBluff = true
	open := Calculate(in)
	if !isAggressive(open.PrimaryAction) {
		t.Fatalf("checked a flush draw with the branch open: %v", open.PrimaryAction)
	}
}

// Air stays shut. The relaxation is "a hand with outs", not "any hand".
func TestSemiBluffDoesNotOpenForAir(t *testing.T) {
	state := table.HandState{
		Street: table.StreetFlop, Pot: 20, CurrentBet: 0, SmallBlind: 1, BigBlind: 2,
		HeroID:         "hero",
		HeroCards:      [2]table.Card{{Rank: table.RankSeven, Suit: table.Hearts}, {Rank: table.RankTwo, Suit: table.Clubs}},
		CommunityCards: parseBoardCards(t, "Ad Kc 5s"),
		Seats: []table.SeatState{
			{PlayerID: "hero", Position: table.PosBTN, Stack: 100, IsActive: true},
			{PlayerID: "villain", Position: table.PosBB, Stack: 100, IsActive: true},
		},
	}
	in := Inputs{
		State:          state,
		Equity:         equity.EquityResult{WinRate: 0.12},
		EquityVsTop:    func(float64) float64 { return 0.08 },
		AllowSemiBluff: true,
	}
	if got := Calculate(in); isAggressive(got.PrimaryAction) {
		t.Fatalf("bet air: %v", got.PrimaryAction)
	}
}

func isAggressive(a table.ActionType) bool {
	return a == table.ActionBet || a == table.ActionRaise || a == table.ActionAllIn
}
