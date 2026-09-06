package sfs

import (
	"strconv"
	"strings"

	"poker-game-analyzer/pkg/table"
)

// HandState projects the roster into the table.HandState the rest of the tool
// already consumes -- the same type the frame-reader produced through the
// stabilizer, so the advisor, profiler and HUD need no change to run off the
// wire. Everything in it was stated by the server, so none of the inference the
// stabilizer did (min-raise from the largest bet, street from the board count,
// blinds from the window title) is needed or done here.
//
// It returns nil until there is a table to describe -- a hand id from a
// hand-start or an action, or at least seats read off the felt. Joining
// mid-hand, the seats arrive before any hand boundary, and showing the table
// then is better than a blank panel until the next hand.
func (r *Roster) HandState() *table.HandState {
	if r.HandID == 0 && len(r.Seats) == 0 {
		return nil
	}
	heroSeat := r.HeroSeat()
	positions := r.positions()

	handID := ""
	if r.HandID != 0 {
		handID = strconv.FormatInt(r.HandID, 10)
	}
	hs := &table.HandState{
		HandID:         handID,
		TableID:        r.TableID(),
		Street:         wireStreet(r.Street),
		Pot:            r.Pot.Float(),
		SmallBlind:     r.SmallBlind.Float(),
		BigBlind:       r.BigBlind.Float(),
		CommunityCards: append([]table.Card(nil), r.Board...),
	}

	if heroSeat != nil {
		hs.HeroID = strconv.FormatInt(heroSeat.UserID, 10)
		hs.IsHeroTurn = r.WhoseTurn != "" && r.WhoseTurn == heroSeat.UserName
	}
	if len(r.HeroCards) == 2 {
		hs.HeroCards = [2]table.Card{r.HeroCards[0], r.HeroCards[1]}
	}

	// The amount owed and the min raise come from the action offer when we have
	// it -- they are exact there -- and are left at zero (unknown) otherwise
	// rather than invented, the same contract HandState documents for blinds.
	//
	// Only hero's own offer counts. Another player's says what their decision
	// costs, not what hero's does, and an offer that has been spent says what
	// the last decision cost: read as hero's buttons, a stale one is a fold and
	// a call and a raise on a street where hero has nothing in front of them.
	if r.Turn != nil && heroSeat != nil && r.TurnFor == heroSeat.UserName {
		hs.CurrentBet = r.Turn.RoundMaxBet.Float()
		if raise := r.Turn.UserTurnOptions[ActionCodeRaise]; len(raise) >= 1 {
			hs.MinRaise = raise[0].Float()
		}
		hs.HeroButtons = heroButtons(r.Turn)
	}

	for _, s := range r.Ordered() {
		seat := table.SeatState{
			SeatNumber:  s.SeatID,
			PlayerID:    strconv.FormatInt(s.UserID, 10),
			PlayerName:  s.UserName,
			Stack:       s.Chips.Float(),
			CurrentBet:  s.Bet.Float(),
			IsActive:    s.IsPlaying,
			IsFolded:    isFolded(s),
			Position:    positions[s.SeatID],
			LastAction:  s.LastAction,
			Cards:       r.HoleCards[s.SeatID],
			ServerVPIP:  s.Vpip,
			ServerHands: s.HandsPlayed,
			WonHand:     r.Winners[s.SeatID],
		}
		hs.Seats = append(hs.Seats, seat)
	}

	for _, a := range r.Actions {
		hs.ActionHistory = append(hs.ActionHistory, table.ActionRecord{
			PlayerID: strconv.FormatInt(a.UserID, 10),
			Street:   wireStreet(a.RoundName),
			Action:   wireAction(a.Action),
			Amount:   a.ActionAmount.Float(),
		})
	}

	return hs
}

// TableID is the table's own number, recovered from the hand id, whose prefix is
// the table number (a hand at "Big Ben 1280514" has ids like 128051400033). One
// TCP connection carries several tables, and this is how their messages are told
// apart without decoding the SFS room envelope.
func (r *Roster) TableID() string {
	id := strconv.FormatInt(r.HandID, 10)
	// The hand counter is the last five digits; the rest is the table number.
	if len(id) > 5 {
		return id[:len(id)-5]
	}
	return id
}

// positions assigns a position to each occupied seat from the button and blind
// seats the hand-start message named. SB, BB and BTN are taken directly; the
// seats between the big blind and the button, in clockwise order, are the early-
// to-late positions.
func (r *Roster) positions() map[int]table.Position {
	out := make(map[int]table.Position)
	if r.SBSeat != 0 {
		out[r.SBSeat] = table.PosSB
	}
	if r.BBSeat != 0 {
		out[r.BBSeat] = table.PosBB
	}
	if r.DealerSeat != 0 {
		out[r.DealerSeat] = table.PosBTN
	}

	// Walk clockwise from the seat after the big blind up to (not including) the
	// button, labelling the players in between from earliest to latest.
	middle := r.seatsBetween(r.BBSeat, r.DealerSeat)
	labels := middlePositions(len(middle))
	for i, seat := range middle {
		if _, taken := out[seat]; !taken {
			out[seat] = labels[i]
		}
	}
	return out
}

// seatsBetween lists the occupied seat ids strictly after `from` and strictly
// before `to`, walking clockwise (increasing seat id, wrapping). Seats are
// numbered 1..maxSeat around the table.
func (r *Roster) seatsBetween(from, to int) []int {
	occupied := make(map[int]bool)
	max := 0
	for id := range r.Seats {
		occupied[id] = true
		if id > max {
			max = id
		}
	}
	var out []int
	for i := 1; i <= max; i++ {
		seat := ((from-1+i-1)%max + max) % max // step i seats past `from`
		seat++
		if seat == to {
			break
		}
		if occupied[seat] {
			out = append(out, seat)
		}
	}
	return out
}

// middlePositions names n seats between the blinds and the button, earliest
// first. With three it is UTG, MP, CO; with fewer, the latest positions are
// kept (a short table has no under-the-gun grind, it has a cutoff).
func middlePositions(n int) []table.Position {
	full := []table.Position{table.PosUTG, table.PosMP, table.PosCO}
	if n >= len(full) {
		// More middle seats than names (7+ handed): pad the front with UTG.
		out := make([]table.Position, n)
		for i := range out {
			out[i] = table.PosUTG
		}
		copy(out[n-len(full):], full)
		return out
	}
	return full[len(full)-n:]
}

// isFolded reports whether a seat has folded this hand, read from its status and
// last action -- the wire says "Fold" in the caption and the last action alike.
func isFolded(s *Seat) bool {
	return strings.EqualFold(s.Status, "Fold") || strings.EqualFold(s.LastAction, "Fold")
}

// heroButtons turns the action-offer codes into the lowercase button names the
// HandState carries.
func heroButtons(t *TurnOptionsMsg) []string {
	var out []string
	if _, ok := t.UserTurnOptions[ActionCodeFold]; ok {
		out = append(out, "fold")
	}
	if _, ok := t.UserTurnOptions[ActionCodeCheck]; ok {
		out = append(out, "check")
	}
	if _, ok := t.UserTurnOptions[ActionCodeCall]; ok {
		out = append(out, "call")
	}
	if _, ok := t.UserTurnOptions[ActionCodeRaise]; ok {
		out = append(out, "raise")
	}
	return out
}

// wireStreet maps the server's round name to a table street.
func wireStreet(round string) table.Street {
	switch strings.ToUpper(round) {
	case "PREFLOP":
		return table.StreetPreflop
	case "FLOP":
		return table.StreetFlop
	case "TURN":
		return table.StreetTurn
	case "RIVER":
		return table.StreetRiver
	case "SHOWDOWN":
		return table.StreetShowdown
	default:
		return table.StreetPreflop
	}
}

// wireAction maps the server's action name to a table action.
func wireAction(a string) table.ActionType {
	switch strings.ToUpper(a) {
	case "FOLD":
		return table.ActionFold
	case "CHECK":
		return table.ActionCheck
	case "CALL":
		return table.ActionCall
	case "BET":
		return table.ActionBet
	case "RAISE":
		return table.ActionRaise
	case "ALLIN", "ALL_IN", "ALL IN":
		return table.ActionAllIn
	default:
		return table.ActionType(strings.ToLower(a))
	}
}
