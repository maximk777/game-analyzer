package audit

import (
	"slices"
	"testing"

	"poker-game-analyzer/pkg/table"
)

// The two defects the audit could not name, taken from the session of
// 2026-08-31: seat numbers collided on 217 frames out of 220, and 61 frames put
// more than six players at a six-max table. One nickname came back six ways --
// Rafidamage also as Rafk, aage, adge, nafidamage and Rafida -- and the
// interface button "Enter Amount" arrived as a player with a stack.
//
// Nothing downstream can act on what nothing names, and the count of live
// opponents is what multiway equity is computed against: every ghost is both an
// extra player and an extra unknown, so the stranger tax is charged for
// somebody who does not exist.
func TestBuild_NamesDuplicateSeatsAndImpossibleCounts(t *testing.T) {
	// Seat numbers exactly as the reader produced them on the hand hero busted.
	state := table.HandState{
		HandID: "h", TableID: "t", Street: table.StreetPreflop,
		Pot: 65600, CurrentBet: 18000, HeroID: "ludoStarik",
		HeroCards: [2]table.Card{
			{Rank: 8, Suit: table.Hearts},
			{Rank: 8, Suit: table.Diamonds},
		},
		Seats: []table.SeatState{
			{SeatNumber: 0, PlayerID: "ludoStarik", Stack: 35020, CurrentBet: 18000, IsActive: true},
			{SeatNumber: 1, PlayerID: "Faisal101", Stack: 78570, IsActive: true, IsFolded: true},
			{SeatNumber: 1, PlayerID: "Rafidamage", Stack: 1160000, IsActive: true},
			{SeatNumber: 3, PlayerID: "GrabaRobi13", Stack: 184760, IsActive: true},
			{SeatNumber: 3, PlayerID: "d33mar", Stack: 675040, IsActive: true},
			{SeatNumber: 2, PlayerID: "Rafk", Stack: 1170000, CurrentBet: 1000, IsActive: true},
			{SeatNumber: 1, PlayerID: "aage", Stack: 1170000, CurrentBet: 1000, IsActive: true},
			{SeatNumber: 1, PlayerID: "adge", Stack: 1170000, CurrentBet: 1000, IsActive: true},
		},
		ActionHistory: []table.ActionRecord{
			{PlayerID: "ludoStarik", Street: table.StreetPreflop, Action: table.ActionBet, Amount: 18000},
		},
	}

	rec := Build(&state, nil, nil)

	if !slices.Contains(rec.Gaps, GapDuplicateSeats) {
		t.Errorf("seat numbers 0,1,1,3,3,2,1,1 were not reported as colliding; gaps %v", rec.Gaps)
	}
	if !slices.Contains(rec.Gaps, GapImpossibleSeatCount) {
		t.Errorf("eight seats at a six-max table were not reported; gaps %v", rec.Gaps)
	}
}

// A state that does not use seat numbers at all has not numbered them wrongly.
// The harness builds states this way, and reporting a collision on every one of
// them would bury the real ones.
func TestBuild_UnnumberedSeatsAreNotACollision(t *testing.T) {
	state := table.HandState{
		HandID: "h", TableID: "t", Street: table.StreetPreflop,
		Pot: 4500, CurrentBet: 3000, HeroID: "hero",
		HeroCards: [2]table.Card{
			{Rank: 14, Suit: table.Spades},
			{Rank: 13, Suit: table.Spades},
		},
		Seats: []table.SeatState{
			{PlayerID: "hero", Stack: 99000, CurrentBet: 1000, IsActive: true},
			{PlayerID: "villain", Stack: 97000, CurrentBet: 3000, IsActive: true},
			{PlayerID: "third", Stack: 50000, IsActive: true},
		},
		ActionHistory: []table.ActionRecord{
			{PlayerID: "villain", Street: table.StreetPreflop, Action: table.ActionRaise, Amount: 3000},
		},
	}

	rec := Build(&state, nil, nil)

	if slices.Contains(rec.Gaps, GapDuplicateSeats) {
		t.Errorf("unnumbered seats reported as colliding; gaps %v", rec.Gaps)
	}
	if slices.Contains(rec.Gaps, GapImpossibleSeatCount) {
		t.Errorf("three seats reported as too many; gaps %v", rec.Gaps)
	}
}

// A full six-max table is not an impossible one. The boundary is worth pinning
// because the check exists to catch invented players, and a table that is
// simply full is the case it must not fire on.
func TestBuild_SixSeatsIsNotTooMany(t *testing.T) {
	seats := make([]table.SeatState, 0, 6)
	for i := range 6 {
		seats = append(seats, table.SeatState{
			SeatNumber: i, PlayerID: string(rune('a' + i)), Stack: 10000, IsActive: true,
		})
	}
	state := table.HandState{
		HandID: "h", TableID: "t", Street: table.StreetPreflop,
		Pot: 300, CurrentBet: 200, HeroID: "a",
		HeroCards: [2]table.Card{
			{Rank: 14, Suit: table.Spades},
			{Rank: 14, Suit: table.Hearts},
		},
		Seats: seats,
		ActionHistory: []table.ActionRecord{
			{PlayerID: "b", Street: table.StreetPreflop, Action: table.ActionRaise, Amount: 200},
		},
	}

	rec := Build(&state, nil, nil)

	if slices.Contains(rec.Gaps, GapImpossibleSeatCount) {
		t.Errorf("a full six-max table was reported as too many; gaps %v", rec.Gaps)
	}
	if slices.Contains(rec.Gaps, GapDuplicateSeats) {
		t.Errorf("six distinct seat numbers reported as colliding; gaps %v", rec.Gaps)
	}
}
