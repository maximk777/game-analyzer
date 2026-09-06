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
	// Stats are the opponents' numeric tendencies, keyed by player id: VPIP,
	// PFR, 3-bet and the fold-to figures. They are what turns "he might fold"
	// into a bluff worth making -- a player who folds to two-thirds of c-bets is
	// a different spot from one who folds to a fifth.
	Stats map[string]*storage.PlayerStats
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
	// Bluff is whether the model thinks this is a spot to bluff, and BluffReason
	// says which opponent tendency makes it one. A bluff the model would make is
	// carried even when it is not the headline action, so the panel can surface
	// "there is a bluff here" as its own line.
	Bluff       bool   `json:"bluff"`
	BluffReason string `json:"bluff_reason"`
	// Opinion is the model's own read of the spot, in its own words -- where it
	// disagrees with the tool and why, or what it would watch for. This is the
	// point of a second voice: it is invited to say something the reproducible
	// engine cannot.
	Opinion string `json:"opinion"`
	Model   string `json:"model"`
}

const coachSystemPrompt = `You are a sharp, exploitative second opinion for a No-Limit Hold'em assistant.
You are given one spot, the opponents' statistics, and the assistant's own recommendation,
which was computed from equity, pot odds and preflop charts.

Two things are asked of you that the reproducible engine cannot do:

1. Hunt for bluffs the statistics justify. Opponent tendencies are given as percentages:
   VPIP, PFR, 3bet, and the fold-to figures (fold to c-bet, to 3-bet, to a bet). A high
   fold-to-c-bet or fold-to-bet, a tight range, few hands that reach showdown -- these are
   what make a bluff or a light 3-bet profitable regardless of your own cards. If the spot
   is a good bluff, set "bluff": true and name the tendency in "bluff_reason". Do not invent
   a read the numbers do not support, and be cautious when the hands count is small.

2. Say what you actually think in "opinion" (in Russian): where you disagree with the
   assistant and why, or what you would watch for. Disagreement is welcome -- it is the
   reason a second voice is shown at all.

Write "reasoning" and "opinion" in Russian, each at most two sentences.
Output STRICT JSON and nothing else:
{"action":"fold|check|call|bet|raise|all-in","amount":0,"confidence":0.0,"agrees":true,"reasoning":"...","bluff":false,"bluff_reason":"","opinion":"..."}
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
		// Numeric tendencies for the opponents still in the hand -- this is what
		// a bluff decision is made from.
		if s.PlayerID != h.HeroID && !s.IsFolded {
			if line := statLine(in.Stats[s.PlayerID], s.ServerVPIP, s.ServerHands); line != "" {
				fmt.Fprintf(&b, "\n    %s", line)
			}
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

// statLine renders one opponent's tendencies for the prompt. It prefers our own
// accumulated stats, and falls back to the operator's session VPIP off the wire
// when we have no sample -- a read from the first hand is better than none. The
// hands count is stated so the model can weigh how much to trust it.
func statLine(st *storage.PlayerStats, serverVPIP float64, serverHands int) string {
	if st != nil && st.HandsCount > 0 {
		parts := []string{fmt.Sprintf("stats over %d hands: VPIP %.0f%% PFR %.0f%% 3bet %.0f%%",
			st.HandsCount, st.VPIP, st.PFR, st.ThreeBet)}
		if st.FoldToCBetN > 0 {
			parts = append(parts, fmt.Sprintf("fold-to-cbet %.0f%%", st.FoldToCBet*100))
		}
		if st.FoldTo3BetN > 0 {
			parts = append(parts, fmt.Sprintf("fold-to-3bet %.0f%%", st.FoldTo3Bet*100))
		}
		if st.FoldToBetN > 0 {
			parts = append(parts, fmt.Sprintf("fold-to-bet %.0f%%", st.FoldToBet*100))
		}
		return strings.Join(parts, ", ")
	}
	if serverVPIP > 0 {
		return fmt.Sprintf("session VPIP %.0f%% over %d hands (site's own count, no history yet)",
			serverVPIP, serverHands)
	}
	return ""
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
