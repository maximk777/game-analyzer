package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"strings"
	"time"

	"poker-game-analyzer/pkg/storage"
	"poker-game-analyzer/pkg/table"
)

// Client defines the interface for profiling poker opponents using LLMs or local heuristics.
type Client interface {
	AnalyzePlayer(ctx context.Context, history []table.HandState, stats storage.PlayerStats) (*storage.LLMProfile, error)
}

// MockClient is a deterministic local client for tests and offline usage.
type MockClient struct {
	AnalyzeFunc func(ctx context.Context, history []table.HandState, stats storage.PlayerStats) (*storage.LLMProfile, error)
}

// NewMockClient creates a new MockClient.
func NewMockClient() *MockClient {
	return &MockClient{}
}

// AnalyzePlayer generates an LLMProfile deterministically based on player statistics.
func (m *MockClient) AnalyzePlayer(ctx context.Context, history []table.HandState, stats storage.PlayerStats) (*storage.LLMProfile, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if m.AnalyzeFunc != nil {
		return m.AnalyzeFunc(ctx, history, stats)
	}

	var archetype string
	var bluffFreq float64
	var foldTo3Bet float64
	var foldToCBet float64
	var exploits string
	var notes string

	// Classify player archetype based on VPIP and PFR
	if stats.VPIP < 18.0 && stats.PFR < 15.0 {
		archetype = "Nit"
		bluffFreq = 0.05
		foldTo3Bet = 0.80
		foldToCBet = 0.70
		exploits = "Steal blinds relentlessly; fold to their aggression unless holding nutted hands."
		notes = fmt.Sprintf("Extremely tight rock. Only plays premium hands (VPIP: %.1f%%, PFR: %.1f%%).", stats.VPIP, stats.PFR)
	} else if stats.VPIP >= 18.0 && stats.VPIP <= 28.0 && stats.PFR >= 16.0 {
		archetype = "TAG"
		bluffFreq = 0.22
		foldTo3Bet = 0.55
		foldToCBet = 0.45
		exploits = "Apply pressure when they check turn or river; respect river triple-barrel raises."
		notes = fmt.Sprintf("Solid Tight-Aggressive player with balanced ranges (VPIP: %.1f%%, PFR: %.1f%%, AF: %.1f).", stats.VPIP, stats.PFR, stats.AF)
	} else if stats.VPIP > 28.0 && stats.PFR > 22.0 {
		archetype = "LAG"
		bluffFreq = 0.35
		foldTo3Bet = 0.40
		foldToCBet = 0.40
		exploits = "Induce bluffs and call down lighter on non-scare rivers; 4-bet bluff wider preflop."
		notes = fmt.Sprintf("Loose-Aggressive maniac. High preflop and postflop aggression (VPIP: %.1f%%, PFR: %.1f%%, AF: %.1f).", stats.VPIP, stats.PFR, stats.AF)
	} else {
		archetype = "Fish/Whale"
		bluffFreq = 0.10
		foldTo3Bet = 0.20
		foldToCBet = 0.30
		exploits = "Do not bluff; maximize value bets with top pair or better. Size up heavily on wet boards."
		notes = fmt.Sprintf("Loose-Passive calling station. Chases draws and calls down very wide (VPIP: %.1f%%, PFR: %.1f%%).", stats.VPIP, stats.PFR)
	}

	return &storage.LLMProfile{
		PlayerID:       stats.PlayerID,
		PlayerName:     stats.PlayerName,
		Archetype:      archetype,
		BluffFrequency: bluffFreq,
		FoldTo3Bet:     foldTo3Bet,
		FoldToCBet:     foldToCBet,
		Exploits:       exploits,
		Notes:          notes,
	}, nil
}

// OpenAIOption configures OpenAIClient.
type OpenAIOption func(*OpenAIClient)

// OpenAIClient implements Client communicating with OpenAI-compatible chat completion endpoints.
type OpenAIClient struct {
	apiKey     string
	baseURL    string
	model      string
	httpClient *http.Client
	timeout    time.Duration
}

// WithHTTPClient sets a custom HTTP client for OpenAIClient.
func WithHTTPClient(client *http.Client) OpenAIOption {
	return func(c *OpenAIClient) {
		if client != nil {
			c.httpClient = client
		}
	}
}

// WithTimeout sets a custom request timeout for OpenAIClient.
func WithTimeout(d time.Duration) OpenAIOption {
	return func(c *OpenAIClient) {
		c.timeout = d
	}
}

// NewOpenAIClient creates a new OpenAI-compatible LLM client.
func NewOpenAIClient(apiKey string, baseURL string, model string, opts ...OpenAIOption) *OpenAIClient {
	if baseURL == "" {
		baseURL = "https://api.openai.com/v1"
	}
	baseURL = strings.TrimSuffix(baseURL, "/")
	if model == "" {
		model = "gpt-4o-mini"
	}

	c := &OpenAIClient{
		apiKey:     apiKey,
		baseURL:    baseURL,
		model:      model,
		timeout:    30 * time.Second,
		httpClient: &http.Client{Timeout: 30 * time.Second},
	}

	for _, opt := range opts {
		opt(c)
	}

	return c
}

type openAIChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type openAIChatRequest struct {
	Model          string              `json:"model"`
	Messages       []openAIChatMessage `json:"messages"`
	ResponseFormat map[string]string   `json:"response_format,omitempty"`
	Temperature    float64             `json:"temperature"`
}

type openAIChatResponse struct {
	ID      string `json:"id"`
	Choices []struct {
		Message struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
		Type    string `json:"type"`
	} `json:"error,omitempty"`
}

type parsedProfileResponse struct {
	Archetype      string  `json:"archetype"`
	BluffFrequency float64 `json:"bluff_frequency"`
	FoldTo3Bet     float64 `json:"fold_to_3bet"`
	FoldToCBet     float64 `json:"fold_to_cbet"`
	TiltRisk       string  `json:"tilt_risk"`
	Exploits       string  `json:"exploits"`
	Notes          string  `json:"notes"`
	TacticalNotes  string  `json:"tactical_notes"`
}

// AnalyzePlayer invokes the OpenAI chat completions API to analyze the player and returns an LLMProfile.
func (c *OpenAIClient) AnalyzePlayer(ctx context.Context, history []table.HandState, stats storage.PlayerStats) (*storage.LLMProfile, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	systemPrompt := `You are an expert poker game-theory and exploitative profiling engine.
Analyze the player's accumulated statistical metrics and recent hand history actions.
Output STRICT JSON matching this exact schema:
{
  "archetype": "TAG | LAG | Nit | Fish/Whale",
  "bluff_frequency": 0.25,
  "fold_to_3bet": 0.55,
  "fold_to_cbet": 0.60,
  "tilt_risk": "Low | Medium | High",
  "exploits": "Concise actionable tactical exploits",
  "notes": "Key observations regarding sizing and tendencies"
}`

	var historySummary strings.Builder
	if len(history) > 0 {
		historySummary.WriteString(fmt.Sprintf("\nRecent Hands (%d):\n", len(history)))
		for i, h := range history {
			if i >= 10 {
				break
			}
			historySummary.WriteString(fmt.Sprintf("- Hand %s (Pot: %.1f, Street: %s): Actions: ", h.HandID, h.Pot, h.Street))
			for _, act := range h.ActionHistory {
				if act.PlayerID == stats.PlayerID {
					historySummary.WriteString(fmt.Sprintf("[%s %s %.1f] ", act.Street, act.Action, act.Amount))
				}
			}
			historySummary.WriteString("\n")
		}
	}

	userPrompt := fmt.Sprintf(`Player Profile Target:
- Player ID: %s
- Player Name: %s
- Total Hands: %d
- VPIP: %.1f%%
- PFR: %.1f%%
- 3-Bet: %.1f%%
- Aggression Factor (AF): %.1f
%s
Classify the archetype, evaluate bluff frequency and fold tendencies (as 0.0-1.0 probabilities), and provide tactical exploit recommendations.`,
		stats.PlayerID, stats.PlayerName, stats.HandsCount, stats.VPIP, stats.PFR, stats.ThreeBet, stats.AF, historySummary.String())

	reqBody := openAIChatRequest{
		Model: c.model,
		Messages: []openAIChatMessage{
			{Role: "system", Content: systemPrompt},
			{Role: "user", Content: userPrompt},
		},
		ResponseFormat: map[string]string{"type": "json_object"},
		Temperature:    0.2,
	}

	data, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal chat request: %w", err)
	}

	endpoint := c.baseURL + "/chat/completions"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("failed to create http request: %w", err)
	}

	httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("http request failed: %w", err)
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("llm api returned status %d: %s", resp.StatusCode, string(bodyBytes))
	}

	var chatResp openAIChatResponse
	if err := json.Unmarshal(bodyBytes, &chatResp); err != nil {
		return nil, fmt.Errorf("failed to decode chat response: %w", err)
	}

	if chatResp.Error != nil && chatResp.Error.Message != "" {
		return nil, fmt.Errorf("llm api error: %s", chatResp.Error.Message)
	}

	if len(chatResp.Choices) == 0 {
		return nil, errors.New("llm returned empty choices")
	}

	rawContent := strings.TrimSpace(chatResp.Choices[0].Message.Content)
	// Clean markdown json fences if present
	if strings.HasPrefix(rawContent, "```") {
		lines := strings.Split(rawContent, "\n")
		if len(lines) >= 2 {
			if strings.HasPrefix(lines[0], "```") {
				lines = lines[1:]
			}
			if len(lines) > 0 && strings.HasPrefix(lines[len(lines)-1], "```") {
				lines = lines[:len(lines)-1]
			}
			rawContent = strings.TrimSpace(strings.Join(lines, "\n"))
		}
	}

	var parsed parsedProfileResponse
	if err := json.Unmarshal([]byte(rawContent), &parsed); err != nil {
		return nil, fmt.Errorf("failed to parse profile json from LLM output %q: %w", rawContent, err)
	}

	clampProb := func(v float64) float64 {
		if v > 1.0 && v <= 100.0 {
			v = v / 100.0
		}
		if v < 0.0 {
			return 0.0
		}
		if v > 1.0 {
			return 1.0
		}
		return math.Round(v*100) / 100
	}

	notes := parsed.Notes
	if notes == "" && parsed.TacticalNotes != "" {
		notes = parsed.TacticalNotes
	}
	if parsed.TiltRisk != "" && !strings.Contains(strings.ToLower(notes), "tilt") {
		if notes != "" {
			notes = fmt.Sprintf("%s (Tilt Risk: %s)", notes, parsed.TiltRisk)
		} else {
			notes = fmt.Sprintf("Tilt Risk: %s", parsed.TiltRisk)
		}
	}

	return &storage.LLMProfile{
		PlayerID:       stats.PlayerID,
		PlayerName:     stats.PlayerName,
		Archetype:      parsed.Archetype,
		BluffFrequency: clampProb(parsed.BluffFrequency),
		FoldTo3Bet:     clampProb(parsed.FoldTo3Bet),
		FoldToCBet:     clampProb(parsed.FoldToCBet),
		Exploits:       parsed.Exploits,
		Notes:          notes,
	}, nil
}
