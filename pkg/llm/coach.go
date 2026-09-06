package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"poker-game-analyzer/pkg/advisor"
	"poker-game-analyzer/pkg/storage"
	"poker-game-analyzer/pkg/table"
)

// Coach is a second opinion on the spot in front of hero.
//
// It is not the decision. The tool's own answer is computed from equity, pot
// odds and charts and is reproducible; this one is a model reading the same
// table in words. Where the two agree there is nothing to think about, and
// where they disagree there is -- which is the whole reason to show it.
type Coach interface {
	AdviseHand(ctx context.Context, in CoachInput) (*CoachAdvice, error)
}

// CoachInput is the spot, as the tool understands it.
type CoachInput struct {
	State *table.HandState
	// Own is the tool's own recommendation, given to the model so its answer
	// can be read as agreement or objection rather than as a second guess in
	// isolation.
	Own *advisor.AdvisorResponse
	// Profiles are what is known about the opponents still in the hand.
	Profiles []storage.LLMProfile
}

// CoachAdvice is what the model would do.
type CoachAdvice struct {
	Action     string  `json:"action"`
	Amount     float64 `json:"amount"`
	Confidence float64 `json:"confidence"`
	// Agrees is the model's own reading of whether it lands on the tool's
	// answer. It is checked against Action rather than trusted.
	Agrees    bool   `json:"agrees"`
	Reasoning string `json:"reasoning"`
	Model     string `json:"model"`
}

const coachSystemPrompt = `You are a second opinion for a No-Limit Hold'em assistant.
You are given one spot and the assistant's own recommendation, which was computed from
equity, pot odds and preflop charts. Say what you would do and why, in at most two
sentences. Write the reasoning in Russian.
Output STRICT JSON and nothing else:
{"action":"fold|check|call|bet|raise|all-in","amount":0,"confidence":0.0,"agrees":true,"reasoning":"..."}
"amount" is in chips and is 0 for fold and check. "confidence" is 0.0 to 1.0.
"agrees" is whether your action matches the assistant's.`

// AdviseHand asks the model what it would do here.
func (c *OpenAIClient) AdviseHand(ctx context.Context, in CoachInput) (*CoachAdvice, error) {
	if in.State == nil {
		return nil, fmt.Errorf("no table state to advise on")
	}

	raw, err := c.chat(ctx, coachSystemPrompt, describeSpot(in))
	if err != nil {
		return nil, err
	}

	var out CoachAdvice
	if err := json.Unmarshal([]byte(stripFence(raw)), &out); err != nil {
		return nil, fmt.Errorf("failed to parse coach json from %q: %w", raw, err)
	}
	out.Action = strings.ToLower(strings.TrimSpace(out.Action))
	out.Model = c.model
	if out.Confidence > 1 && out.Confidence <= 100 {
		out.Confidence /= 100
	}
	// The model's own idea of whether it agrees is a sentence it wrote, not a
	// comparison it made. The comparison is cheap and is the point of the
	// panel, so it is made here.
	if in.Own != nil {
		out.Agrees = sameAction(out.Action, in.Own.PrimaryAction)
	}
	return &out, nil
}

// describeSpot writes the table out for the model. Chips, not units: the stake
// is stated so the model can scale for itself.
func describeSpot(in CoachInput) string {
	h := in.State
	var b strings.Builder

	fmt.Fprintf(&b, "Street: %s\n", h.Street)
	if h.BigBlind > 0 {
		fmt.Fprintf(&b, "Blinds: %g/%g\n", h.SmallBlind, h.BigBlind)
	}
	fmt.Fprintf(&b, "Pot: %g\nTo call: %g\n", h.Pot, h.CurrentBet)
	fmt.Fprintf(&b, "Board: %s\n", cardList(h.CommunityCards))
	fmt.Fprintf(&b, "Hero holds: %s %s\n", h.HeroCards[0], h.HeroCards[1])
	if len(h.HeroButtons) > 0 {
		fmt.Fprintf(&b, "Buttons on screen: %s\n", strings.Join(h.HeroButtons, ", "))
	}

	b.WriteString("Seats:\n")
	profiles := map[string]storage.LLMProfile{}
	for _, p := range in.Profiles {
		profiles[p.PlayerID] = p
	}
	for _, s := range h.Seats {
		who := "opponent"
		if s.PlayerID == h.HeroID {
			who = "HERO"
		}
		fmt.Fprintf(&b, "- %s %s (%s): stack %g, wagered %g", who, s.PlayerName, s.Position, s.Stack, s.CurrentBet)
		if s.IsFolded {
			b.WriteString(", folded")
		}
		if s.LastAction != "" {
			fmt.Fprintf(&b, ", last action %s", s.LastAction)
		}
		if p, ok := profiles[s.PlayerID]; ok && p.Archetype != "" {
			fmt.Fprintf(&b, ", read: %s, bluffs %.0f%%", p.Archetype, p.BluffFrequency*100)
		}
		b.WriteString("\n")
	}

	if in.Own != nil {
		fmt.Fprintf(&b, "\nThe assistant recommends: %s %g (equity %.1f%%, pot odds %.1f%%, %d opponents).\n",
			in.Own.PrimaryAction, in.Own.RecommendedAmount,
			in.Own.Equity*100, in.Own.PotOdds*100, in.Own.Opponents)
		if in.Own.Reasoning != "" {
			fmt.Fprintf(&b, "Its reasoning: %s\n", in.Own.Reasoning)
		}
	}
	return b.String()
}

func cardList(cards []table.Card) string {
	if len(cards) == 0 {
		return "(none)"
	}
	parts := make([]string, 0, len(cards))
	for _, c := range cards {
		parts = append(parts, c.String())
	}
	return strings.Join(parts, " ")
}

// sameAction compares the model's word with the tool's action. "all-in" is a
// raise that happens to be for everything, and the two names are used
// interchangeably by anyone describing a hand.
func sameAction(word string, own table.ActionType) bool {
	word = strings.TrimSpace(strings.ToLower(word))
	own = table.ActionType(strings.ToLower(string(own)))
	switch word {
	case "all-in", "allin", "all in", "shove":
		return own == table.ActionRaise || own == table.ActionAllIn || own == table.ActionBet
	case "bet", "raise":
		return own == table.ActionType(word) || own == table.ActionAllIn
	default:
		return own == table.ActionType(word)
	}
}

// stripFence removes the ```json wrapper a model puts round its answer when the
// endpoint would not take a response format.
func stripFence(s string) string {
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(s, "```") {
		return s
	}
	lines := strings.Split(s, "\n")
	if len(lines) >= 2 {
		lines = lines[1:]
		if strings.HasPrefix(strings.TrimSpace(lines[len(lines)-1]), "```") {
			lines = lines[:len(lines)-1]
		}
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}
