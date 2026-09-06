package sfs

import (
	"testing"

	"github.com/google/gopacket"
	"github.com/google/gopacket/layers"
)

// collect runs bytes through a Scanner and returns every message it emitted.
// chunks are fed in order as separate Feed calls, which is how packet
// boundaries reach the scanner -- splitting a message across two chunks is the
// case the buffering exists for.
func collect(t *testing.T, chunks ...[]byte) []Message {
	t.Helper()
	var got []Message
	flow, _ := gopacket.FlowFromEndpoints(
		layers.NewIPEndpoint(nil), layers.NewIPEndpoint(nil))
	s := NewScanner(flow, func(_ gopacket.Flow, _ Direction, m Message) {
		got = append(got, m)
	})
	for _, c := range chunks {
		s.Feed(ServerToClient, c)
	}
	return got
}

// framed wraps a JSON blob in the kind of binary noise the SFS2X envelope puts
// around it: a length-ish prefix with a stray '{' byte (0x7b) in it, and a
// trailing type byte. The scanner has to find the real object despite the
// decoy brace.
func framed(json string) []byte {
	prefix := []byte{0x80, 0x00, 0x7b, 0x11, 0x03} // 0x7b == '{', a false start
	suffix := []byte{0x00, 0xff}
	return append(append(prefix, []byte(json)...), suffix...)
}

func TestScanExtractsSeat(t *testing.T) {
	seat := `{"seatId":3,"caption":"Raise","userChips":142114.20,"betAmout":3828.00,"userName":"axiom10","userId":1795427,"lastAction":"Raise","isAutoAction":false}`
	got := collect(t, framed(seat))
	if len(got) != 1 {
		t.Fatalf("got %d messages, want 1", len(got))
	}
	m := got[0]
	if m.Kind != KindSeat || m.Seat == nil {
		t.Fatalf("kind = %v, seat = %v", m.Kind, m.Seat)
	}
	if m.Seat.UserName != "axiom10" || m.Seat.UserID != 1795427 || m.Seat.SeatID != 3 {
		t.Errorf("seat = %+v", m.Seat)
	}
	// Money must be exact: 142114.20 -> 1421142000 minor units, no float drift.
	if got, want := m.Seat.UserChips.String(), "142114.2"; got != want {
		t.Errorf("userChips = %q, want %q", got, want)
	}
	if got, want := m.Seat.BetAmount.String(), "3828"; got != want {
		t.Errorf("betAmout = %q, want %q", got, want)
	}
}

func TestScanMessageSplitAcrossChunks(t *testing.T) {
	seat := `{"seatId":1,"userName":"Faisal101","userId":256364,"userChips":270088.00,"lastAction":"Fold"}`
	full := framed(seat)
	// Split mid-object, so the first Feed ends inside the JSON.
	cut := len(full) - 20
	got := collect(t, full[:cut], full[cut:])
	if len(got) != 1 {
		t.Fatalf("got %d messages, want 1", len(got))
	}
	if got[0].Seat == nil || got[0].Seat.UserName != "Faisal101" {
		t.Fatalf("seat = %+v", got[0].Seat)
	}
}

func TestScanTwoMessagesOneChunk(t *testing.T) {
	a := framed(`{"seatId":5,"userName":"MajorPayn","userId":1560602,"userChips":103796.00,"lastAction":"Fold"}`)
	b := framed(`{"whoseTurn":"MajorPayn","turnTime":16,"timerName":"playerHandTimer"}`)
	got := collect(t, append(a, b...))
	if len(got) != 2 {
		t.Fatalf("got %d messages, want 2", len(got))
	}
	if got[0].Kind != KindSeat || got[1].Kind != KindTurn {
		t.Errorf("kinds = %v, %v", got[0].Kind, got[1].Kind)
	}
	if got[1].Turn.WhoseTurn != "MajorPayn" {
		t.Errorf("turn = %+v", got[1].Turn)
	}
}

func TestScanClassification(t *testing.T) {
	cases := []struct {
		name string
		json string
		want Kind
	}{
		{"pot", `{"totalPotAmount":15428.00,"potAmountList":[15428.00],"isRoundEnd":false}`, KindPot},
		{"turn", `{"whoseTurn":"MajorPayn","turnTime":16}`, KindTurn},
		{"dealer", `{"dealerMessage":"axiom10 Raises To 3828.00","initTimeStamp":"x"}`, KindDealerChat},
		{"history", `{"gameActionMessagesHistory":[{"username":"axiom10","userId":1795427,"action":"RAISE","actionAmount":3828.00,"handId":128051400025,"roundName":"TURN","playerPosition":"SB"}]}`, KindActionHistory},
		{"showdown", `{"totalPotAmount":0,"winnersData":[{"userName":"axiom10","seatId":3,"cumulativeProfitLoss":11020.00,"winType":"Two Pair"}],"playersData":[{"playerName":"axiom10","handStrength":"Two Pair"}]}`, KindShowdown},
		{"gameinfo", `{"gameId":998877}`, KindGameInfo},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := collect(t, framed(c.json))
			if len(got) != 1 {
				t.Fatalf("got %d messages, want 1", len(got))
			}
			if got[0].Kind != c.want {
				t.Errorf("kind = %v, want %v", got[0].Kind, c.want)
			}
		})
	}
}

func TestScanShowdownBeforePot(t *testing.T) {
	// A showdown carries totalPotAmount (as 0) too; winnersData must win the
	// classification so it is not read as a pot update.
	sd := `{"totalPotAmount":0,"cumulativePotAmount":204960.00,"winnersData":[{"userName":"axiom10","seatId":3,"cumulativeProfitLoss":11020.00,"winType":"Two Pair"}],"playersData":[{"playerName":"Blffd","handStrength":"One Pair"}]}`
	got := collect(t, framed(sd))
	if len(got) != 1 || got[0].Kind != KindShowdown {
		t.Fatalf("got %+v, want one showdown", got)
	}
	if len(got[0].Showdown.Winners) != 1 || got[0].Showdown.Winners[0].UserName != "axiom10" {
		t.Errorf("winners = %+v", got[0].Showdown.Winners)
	}
	if got, want := got[0].Showdown.Winners[0].CumulativeProfitLoss.String(), "11020"; got != want {
		t.Errorf("profit = %q, want %q", got, want)
	}
}

func TestScanChipsToReturnIsNotASeat(t *testing.T) {
	// The "uncalled bet returned" message has seatId and userName but no userId;
	// it must not be read as a seat update, which would blank the stack to 0.
	ctr := `{"userName":"Blffd","chipsToReturn":255448.00,"seatId":1,"initTimeStamp":"x"}`
	got := collect(t, framed(ctr))
	if len(got) != 0 {
		t.Fatalf("got %d messages, want 0 (chipsToReturn is not a seat)", len(got))
	}
}

func TestScanActionHistoryHandID(t *testing.T) {
	h := `{"gameActionMessagesHistory":[{"username":"Blffd","userId":1793809,"action":"RAISE","actionAmount":74000.00,"playerPosition":"SB","handId":128051400025,"roundName":"TURN"}]}`
	got := collect(t, framed(h))
	if len(got) != 1 || got[0].ActionHistory == nil {
		t.Fatalf("got %+v, want one action history", got)
	}
	e := got[0].ActionHistory.History[0]
	if e.HandID != 128051400025 || e.PlayerPosition != "SB" || e.RoundName != "TURN" {
		t.Errorf("entry = %+v", e)
	}
	if got, want := e.ActionAmount.String(), "74000"; got != want {
		t.Errorf("amount = %q, want %q", got, want)
	}
}

func TestScanIgnoresUnknownObjects(t *testing.T) {
	// A valid JSON object with none of our keys is not a game message.
	got := collect(t, framed(`{"heartbeat":1,"ping":true}`))
	if len(got) != 0 {
		t.Fatalf("got %d messages, want 0", len(got))
	}
}

func TestScanFalseBraceDoesNotStall(t *testing.T) {
	// A lone '{' of binary with no closing brace must not pin the buffer: a
	// real message arriving after it still parses.
	noise := make([]byte, maxObjBytes+100)
	for i := range noise {
		noise[i] = 0x7b // all '{', the worst case for a brace scanner
	}
	seat := framed(`{"seatId":2,"userName":"Vlasttr","userId":1787981,"userChips":196576.00,"lastAction":"Disconnected"}`)
	got := collect(t, append(noise, seat...))
	if len(got) != 1 {
		t.Fatalf("got %d messages, want 1", len(got))
	}
	if got[0].Seat.UserName != "Vlasttr" {
		t.Errorf("seat = %+v", got[0].Seat)
	}
}

func TestScanBoard(t *testing.T) {
	board := `{"dealerCards":{"FLOP":[{"suit":"HEARTS","value":"KING"},{"suit":"DIAMONDS","value":"QUEEN"},{"suit":"HEARTS","value":"THREE"}],"TURN":[{"suit":"CLUBS","value":"SEVEN"}],"RIVER":[{"suit":"CLUBS","value":"QUEEN"}]},"initTimeStamp":"x"}`
	got := collect(t, framed(board))
	if len(got) != 1 || got[0].Kind != KindBoard {
		t.Fatalf("got %+v, want one board", got)
	}
	b := got[0].Board.Board()
	if len(b) != 5 {
		t.Fatalf("board len %d, want 5", len(b))
	}
	want := []string{"Kh", "Qd", "3h", "7c", "Qc"}
	for i, c := range b {
		if c.String() != want[i] {
			t.Errorf("board[%d] = %s, want %s", i, c.String(), want[i])
		}
	}
}

func TestScanHoleCards(t *testing.T) {
	hc := `{"userCardListMap":{"1":[{"suit":"CLUBS","value":"ACE"},{"suit":"SPADES","value":"TEN"}],"6":[{"suit":"HEARTS","value":"FIVE"},{"suit":"DIAMONDS","value":"FIVE"}]},"initTimeStamp":"x"}`
	got := collect(t, framed(hc))
	if len(got) != 1 || got[0].Kind != KindHoleCards {
		t.Fatalf("got %+v, want one hole-cards", got)
	}
	m := got[0].HoleCards.Map
	if len(m) != 2 {
		t.Fatalf("map len %d, want 2", len(m))
	}
	s1 := wireCards(m["1"])
	if len(s1) != 2 || s1[0].String() != "Ac" || s1[1].String() != "Ts" {
		t.Errorf("seat1 = %v", s1)
	}
}
