package table

type Street string

const (
	StreetPreflop  Street = "preflop"
	StreetFlop     Street = "flop"
	StreetTurn     Street = "turn"
	StreetRiver    Street = "river"
	StreetShowdown Street = "showdown"
)

type ActionType string

const (
	ActionFold  ActionType = "fold"
	ActionCheck ActionType = "check"
	ActionCall  ActionType = "call"
	ActionBet   ActionType = "bet"
	ActionRaise ActionType = "raise"
	ActionAllIn ActionType = "all_in"
)

type Position string

const (
	PosBTN Position = "BTN"
	PosSB  Position = "SB"
	PosBB  Position = "BB"
	PosUTG Position = "UTG"
	PosMP  Position = "MP"
	PosCO  Position = "CO"
)

type SeatState struct {
	SeatNumber int      `json:"seat_number"`
	PlayerID   string   `json:"player_id"`
	PlayerName string   `json:"player_name"`
	Stack      float64  `json:"stack"`
	CurrentBet float64  `json:"current_bet"`
	IsActive   bool     `json:"is_active"`
	IsFolded   bool     `json:"is_folded"`
	Position   Position `json:"position"`
}

type ActionRecord struct {
	PlayerID string     `json:"player_id"`
	Street   Street     `json:"street"`
	Action   ActionType `json:"action"`
	Amount   float64    `json:"amount"`
}

type HandState struct {
	HandID         string         `json:"hand_id"`
	TableID        string         `json:"table_id"`
	Street         Street         `json:"street"`
	Pot            float64        `json:"pot"`
	CurrentBet     float64        `json:"current_bet"`
	MinRaise       float64        `json:"min_raise"`
	CommunityCards []Card         `json:"community_cards"`
	HeroID         string         `json:"hero_id"`
	HeroCards      [2]Card        `json:"hero_cards"`
	Seats          []SeatState    `json:"seats"`
	ActionHistory  []ActionRecord `json:"action_history"`
}
