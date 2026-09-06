package sfs

import (
	"encoding/json"

	"poker-game-analyzer/pkg/table"
)

// Kind is which game message a decoded JSON blob turned out to be. The wire
// blobs carry no single type tag we can rely on, so a message is recognised by
// the fields it has -- the same way you read a struct off a union by its
// discriminant, except the discriminant here is "which keys are present".
type Kind string

const (
	// KindSeat is game.seat: the full per-seat truth. One per seat, re-sent
	// whenever anything about the seat changes. This is the message that
	// replaces OCR: seatId, userId, userName, stack, last action, all exact.
	KindSeat Kind = "seat"
	// KindPot is game.potInfo: the total pot and its side-pot breakdown.
	KindPot Kind = "pot"
	// KindTurn is game.user_turn: whose turn it is and how long they have.
	KindTurn Kind = "turn"
	// KindDealerChat is game.dealer_chat: the human-readable action line
	// ("axiom10 Raises To 3828.00"). Redundant with KindSeat but a useful
	// cross-check, and it names the action in words when the seat caption is
	// ambiguous.
	KindDealerChat Kind = "dealer_chat"
	// KindActionHistory is the per-hand action list, each entry naming the
	// player, their userId, the action and its amount.
	KindActionHistory Kind = "action_history"
	// KindShowdown is one player's showdown result: the hand shown and what it
	// won. The rarest and most valuable message -- frequencies say how often
	// someone bets, a showdown says with what.
	KindShowdown Kind = "showdown"
	// KindGameInfo is the bare {"gameId":N} message: a correlation counter the
	// server emits many times a second, not the hand id. Recognised only so it
	// is skipped rather than left to the scanner as an unknown object; the real
	// hand id is the handId inside the action history.
	KindGameInfo Kind = "game_info"
	// KindBoard is the dealerCards message: the community cards, grouped by the
	// street they came on. It replaces counting board cards off the screen.
	KindBoard Kind = "board"
	// KindHoleCards is the userCardListMap message: hole cards keyed by seat.
	// Sent privately for the hero during play, and for every shown hand at
	// showdown.
	KindHoleCards Kind = "hole_cards"
	// KindHeroCards is the holeCards message: the local player's own two cards,
	// sent privately at the start of the hand. Distinct from KindHoleCards,
	// which is keyed by seat; this one is a bare array and is always the hero's.
	KindHeroCards Kind = "hero_cards"
	// KindHandStart is the message that opens a hand: it carries the blinds, the
	// ante, and the dealer/blind seat ids -- the blinds and positions the frame
	// reader had to scrape from the window title and the dealer chip. Its gameId
	// (unlike the bare correlation counter) is a real hand id for this table.
	KindHandStart Kind = "hand_start"
	// KindTurnOptions is the message offering a player their actions: which are
	// legal, the call amount, and the min/max for a raise. When its whoseTurn is
	// the hero, it is the hero's buttons and bet sizing.
	KindTurnOptions Kind = "turn_options"
	// KindSettings is the per-user settings blob; we read it only for the
	// userId, which identifies the hero (the local player whose client this is).
	KindSettings Kind = "settings"
)

// Message is one decoded blob. Exactly one of the payload pointers is set,
// matching Kind; Raw keeps the original bytes so an unrecognised-but-useful
// field is never lost to the struct's shape.
type Message struct {
	Kind Kind
	Raw  []byte

	Seat          *SeatMsg
	Pot           *PotMsg
	Turn          *TurnMsg
	DealerChat    *DealerChatMsg
	ActionHistory *ActionHistoryMsg
	Showdown      *ShowdownMsg
	GameInfo      *GameInfoMsg
	Board         *BoardMsg
	HoleCards     *HoleCardsMsg
	HeroCards     *HeroCardsMsg
	HandStart     *HandStartMsg
	TurnOptions   *TurnOptionsMsg
	Settings      *SettingsMsg
}

// SeatMsg is game.seat. betAmout is the client's own spelling, kept on the JSON
// tag so the wire maps straight in; the Go field is spelled correctly.
//
// The server sends more than OCR ever read: alongside the name, id and stack it
// carries the seat's own running VPIP and hands-played this session. Those are
// the operator's own per-seat stats, free for the taking.
type SeatMsg struct {
	SeatID             int         `json:"seatId"`
	UserID             int64       `json:"userId"`
	UserName           string      `json:"userName"`
	UserChips          table.Money `json:"userChips"`
	BetAmount          table.Money `json:"betAmout"`
	Caption            string      `json:"caption"`
	NewCaption         string      `json:"newCaption"`
	PlayerStatus       *string     `json:"playerStatus"`
	LastAction         string      `json:"lastAction"`
	IsAutoAction       bool        `json:"isAutoAction"`
	IsPlaying          bool        `json:"isPlaying"`
	AvatarID           int         `json:"avatarId"`
	VpipPercentage     float64     `json:"vpipPercentage"`
	SessionHandsPlayed int         `json:"sessionHandsPlayed"`
}

// PotMsg is game.potInfo. roundName is the street ("PREFLOP", "FLOP", "TURN",
// "RIVER") and is how the wire names the phase the frame-reader had to infer
// from the count of board cards.
type PotMsg struct {
	TotalPotAmount table.Money   `json:"totalPotAmount"`
	PotAmountList  []table.Money `json:"potAmountList"`
	IsRoundEnd     bool          `json:"isRoundEnd"`
	RoundName      string        `json:"roundName"`
}

// TurnMsg is game.user_turn.
type TurnMsg struct {
	WhoseTurn     string `json:"whoseTurn"`
	TurnTime      int    `json:"turnTime"`
	TimerName     string `json:"timerName"`
	InitTimeStamp string `json:"initTimeStamp"`
}

// DealerChatMsg is game.dealer_chat.
type DealerChatMsg struct {
	DealerMessage string `json:"dealerMessage"`
	InitTimeStamp string `json:"initTimeStamp"`
}

// ActionHistoryMsg wraps the per-hand action list.
type ActionHistoryMsg struct {
	History []ActionEntry `json:"gameActionMessagesHistory"`
}

// ActionEntry is one line of the action history -- the authoritative record of
// one player acting, with everything a table event needs: who, their position,
// the action and amount, the street, and the hand it belongs to. HandID is the
// real per-hand identifier; the bare {"gameId":N} messages on the wire are a
// correlation counter, not this.
type ActionEntry struct {
	Username        string      `json:"username"`
	UserID          int64       `json:"userId"`
	Action          string      `json:"action"`
	NewPlayerAction string      `json:"newPlayerAction"`
	ActionAmount    table.Money `json:"actionAmount"`
	RoundTotalPot   table.Money `json:"roundTotalPot"`
	PlayerPosition  string      `json:"playerPosition"`
	RoundName       string      `json:"roundName"`
	HandID          int64       `json:"handId"`
}

// ShowdownMsg is the end-of-hand result: who won, with what, and every player
// who reached showdown with the strength they held. The hole cards themselves
// are not here -- they arrive as "X Show Cards [..]" dealer-chat lines -- but
// the win amounts and hand types are exact.
type ShowdownMsg struct {
	CumulativePotAmount            table.Money   `json:"cumulativePotAmount"`
	CumulativePotAmountWithoutRake table.Money   `json:"cumulativePotAmountWithoutRake"`
	Winners                        []WinnerEntry `json:"winnersData"`
	Players                        []PlayerShow  `json:"playersData"`
}

// WinnerEntry is one winner of a pot.
type WinnerEntry struct {
	UserName             string      `json:"userName"`
	SeatID               int         `json:"seatId"`
	CumulativeProfitLoss table.Money `json:"cumulativeProfitLoss"`
	WinType              string      `json:"winType"`
}

// PlayerShow is one player's shown hand strength at showdown.
type PlayerShow struct {
	PlayerName   string `json:"playerName"`
	HandStrength string `json:"handStrength"`
}

// BoardMsg is the dealerCards message: the community cards grouped by street.
// Board returns them flattened in dealing order (flop, turn, river).
type BoardMsg struct {
	DealerCards map[string][]WireCard `json:"dealerCards"`
}

// streetOrder is the order the board is dealt, for flattening dealerCards.
var streetOrder = []string{"FLOP", "TURN", "RIVER"}

// Board flattens the by-street board into a single ordered slice of cards.
func (b *BoardMsg) Board() []table.Card {
	var out []table.Card
	for _, st := range streetOrder {
		out = append(out, wireCards(b.DealerCards[st])...)
	}
	return out
}

// HoleCardsMsg is the userCardListMap message: hole cards keyed by seat id (as
// a string on the wire). During play it carries only the hero's seat; at
// showdown it carries every hand that was shown.
type HoleCardsMsg struct {
	Map map[string][]WireCard `json:"userCardListMap"`
}

// HeroCardsMsg is the holeCards message: the hero's own two cards.
type HeroCardsMsg struct {
	HoleCards []WireCard `json:"holeCards"`
}

// Cards returns the hero's cards as table cards.
func (h *HeroCardsMsg) Cards() []table.Card { return wireCards(h.HoleCards) }

// HandStartMsg opens a hand: blinds, ante, and the dealer/blind seat ids. Its
// GameID is a real per-table hand id (it shares the table's number as a prefix),
// unlike the bare correlation counter.
type HandStartMsg struct {
	GameID       int64       `json:"gameId"`
	DealerSeatID int         `json:"dealerSeatId"`
	SBSeatID     int         `json:"sbSeatId"`
	BBSeatID     int         `json:"bbSeatId"`
	AnteAmount   table.Money `json:"anteAmount"`
	SBAmount     table.Money `json:"sbAmount"`
	BBAmount     table.Money `json:"bbAmount"`
}

// TurnOptionsMsg offers a player their legal actions and the amounts. The keys
// of UserTurnOptions are action codes (see the ActionCode* constants); a raise
// entry holds [min, max]. WhoseTurn names the player it is offered to.
type TurnOptionsMsg struct {
	UserTurnOptions map[string][]table.Money `json:"userTurnOptions"`
	CallAmount      table.Money              `json:"callAmount"`
	RoundMaxBet     table.Money              `json:"roundMaxBet"`
	PotAmount       table.Money              `json:"potAmount"`
	PotRaiseValue   table.Money              `json:"potRaiseValue"`
	WhoseTurn       string                   `json:"whoseTurn"`
}

// Action codes used as the keys of userTurnOptions and in the client's own
// userAction messages. Confirmed from live traffic: 4 is call, 5 is raise/bet,
// 7 is fold; 3 is check (the no-cost call). Others are left unnamed until seen.
const (
	ActionCodeCheck = "3"
	ActionCodeCall  = "4"
	ActionCodeRaise = "5"
	ActionCodeFold  = "7"
)

// SettingsMsg is the per-user settings blob, read only for the hero's userId.
type SettingsMsg struct {
	UserID   int64 `json:"userId"`
	AutoMuck int   `json:"autoMuck"`
}

// GameInfoMsg is the bare {"gameId":N} message -- a correlation counter the
// server emits constantly, kept only so it is recognised and skipped rather
// than left to the scanner as an unknown object.
type GameInfoMsg struct {
	GameID int64 `json:"gameId"`
}

// probe is the cheap first pass: only the keys that tell one message kind from
// another, so a blob is classified with one unmarshal before the full,
// kind-specific one. Presence, not value, is the discriminant, so every field
// is a pointer or a slice whose nil-ness answers "was this key here".
type probe struct {
	SeatID         *int             `json:"seatId"`
	UserName       *string          `json:"userName"`
	UserID         *int64           `json:"userId"`
	TotalPotAmount *json.RawMessage `json:"totalPotAmount"`
	WhoseTurn      *string          `json:"whoseTurn"`
	DealerMessage  *string          `json:"dealerMessage"`
	ActionHistory  *json.RawMessage `json:"gameActionMessagesHistory"`
	WinnersData    *json.RawMessage `json:"winnersData"`
	DealerCards    *json.RawMessage `json:"dealerCards"`
	UserCardMap    *json.RawMessage `json:"userCardListMap"`
	HoleCardsArr   *json.RawMessage `json:"holeCards"`
	SBAmount       *json.RawMessage `json:"sbAmount"`
	TurnOptions    *json.RawMessage `json:"userTurnOptions"`
	AutoMuck       *json.RawMessage `json:"autoMuck"`
	GameID         *int64           `json:"gameId"`
}

// classify decides a blob's kind from which keys it carries, or returns false
// if it is a JSON object we have no use for. Order matters where keys overlap:
//
//   - The showdown carries totalPotAmount too (as 0), so winnersData is tested
//     before the pot.
//   - The "uncalled bet returned" message carries seatId and userName but no
//     userId; requiring userId keeps it from being read as a seat update that
//     would blank the stack. A real seat always names its player's id.
func classify(p probe) (Kind, bool) {
	switch {
	case p.ActionHistory != nil:
		return KindActionHistory, true
	case p.WinnersData != nil:
		return KindShowdown, true
	case p.DealerCards != nil:
		return KindBoard, true
	case p.HoleCardsArr != nil:
		return KindHeroCards, true
	case p.UserCardMap != nil:
		return KindHoleCards, true
	case p.SBAmount != nil:
		return KindHandStart, true
	case p.TurnOptions != nil:
		return KindTurnOptions, true
	case p.SeatID != nil && p.UserName != nil && p.UserID != nil:
		return KindSeat, true
	case p.TotalPotAmount != nil:
		return KindPot, true
	// A turn-options message also carries whoseTurn, so it is tested first; a
	// bare whoseTurn (with a timer) is the plain user_turn notice.
	case p.WhoseTurn != nil:
		return KindTurn, true
	case p.DealerMessage != nil:
		return KindDealerChat, true
	case p.AutoMuck != nil:
		return KindSettings, true
	case p.GameID != nil:
		return KindGameInfo, true
	default:
		return "", false
	}
}

// decodeBlob turns one complete JSON object into a Message, or returns false if
// the object is not one we recognise. The blob is any valid JSON object lifted
// out of the byte stream; most such objects on the wire are game messages, but
// binary framing occasionally forms an accidental object, which classify
// rejects for want of any known key.
func decodeBlob(b []byte) (Message, bool) {
	var p probe
	if err := json.Unmarshal(b, &p); err != nil {
		return Message{}, false
	}
	kind, ok := classify(p)
	if !ok {
		return Message{}, false
	}

	m := Message{Kind: kind, Raw: b}
	switch kind {
	case KindSeat:
		var v SeatMsg
		if err := json.Unmarshal(b, &v); err != nil {
			return Message{}, false
		}
		m.Seat = &v
	case KindPot:
		var v PotMsg
		if err := json.Unmarshal(b, &v); err != nil {
			return Message{}, false
		}
		m.Pot = &v
	case KindTurn:
		var v TurnMsg
		if err := json.Unmarshal(b, &v); err != nil {
			return Message{}, false
		}
		m.Turn = &v
	case KindDealerChat:
		var v DealerChatMsg
		if err := json.Unmarshal(b, &v); err != nil {
			return Message{}, false
		}
		m.DealerChat = &v
	case KindActionHistory:
		var v ActionHistoryMsg
		if err := json.Unmarshal(b, &v); err != nil {
			return Message{}, false
		}
		m.ActionHistory = &v
	case KindShowdown:
		var v ShowdownMsg
		if err := json.Unmarshal(b, &v); err != nil {
			return Message{}, false
		}
		m.Showdown = &v
	case KindGameInfo:
		var v GameInfoMsg
		if err := json.Unmarshal(b, &v); err != nil {
			return Message{}, false
		}
		m.GameInfo = &v
	case KindBoard:
		var v BoardMsg
		if err := json.Unmarshal(b, &v); err != nil {
			return Message{}, false
		}
		m.Board = &v
	case KindHoleCards:
		var v HoleCardsMsg
		if err := json.Unmarshal(b, &v); err != nil {
			return Message{}, false
		}
		m.HoleCards = &v
	case KindHeroCards:
		var v HeroCardsMsg
		if err := json.Unmarshal(b, &v); err != nil {
			return Message{}, false
		}
		m.HeroCards = &v
	case KindHandStart:
		var v HandStartMsg
		if err := json.Unmarshal(b, &v); err != nil {
			return Message{}, false
		}
		m.HandStart = &v
	case KindTurnOptions:
		var v TurnOptionsMsg
		if err := json.Unmarshal(b, &v); err != nil {
			return Message{}, false
		}
		m.TurnOptions = &v
	case KindSettings:
		var v SettingsMsg
		if err := json.Unmarshal(b, &v); err != nil {
			return Message{}, false
		}
		m.Settings = &v
	}
	return m, true
}
