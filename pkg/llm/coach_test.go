package llm_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"poker-game-analyzer/pkg/advisor"
	"poker-game-analyzer/pkg/llm"
	"poker-game-analyzer/pkg/storage"
	"poker-game-analyzer/pkg/table"
)

func spot() llm.CoachInput {
	ah, _ := table.ParseCard("Ah")
	kh, _ := table.ParseCard("Kh")
	f1, _ := table.ParseCard("2c")
	f2, _ := table.ParseCard("7d")
	f3, _ := table.ParseCard("Js")
	return llm.CoachInput{
		State: &table.HandState{
			Street:         table.StreetFlop,
			Pot:            10000,
			CurrentBet:     2000,
			SmallBlind:     1000,
			BigBlind:       2000,
			CommunityCards: []table.Card{f1, f2, f3},
			HeroID:         "hero",
			HeroCards:      [2]table.Card{ah, kh},
			HeroButtons:    []string{"fold", "call", "raise"},
			IsHeroTurn:     true,
			Seats: []table.SeatState{
				{SeatNumber: 0, PlayerID: "hero", PlayerName: "hero", Stack: 100000, Position: "BTN"},
				{SeatNumber: 3, PlayerID: "opp", PlayerName: "villain", Stack: 90000, CurrentBet: 2000, Position: "BB", LastAction: "bet"},
			},
		},
		Own: &advisor.AdvisorResponse{
			PrimaryAction:     table.ActionCall,
			RecommendedAmount: 2000,
			Equity:            0.44,
			PotOdds:           0.17,
			Opponents:         1,
			Reasoning:         "Equity beats the price.",
		},
	}
}

func serve(t *testing.T, body string, capture *string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if capture != nil {
			var req struct {
				Messages []struct {
					Content string `json:"content"`
				} `json:"messages"`
			}
			_ = json.NewDecoder(r.Body).Decode(&req)
			for _, m := range req.Messages {
				*capture += m.Content + "\n"
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{
				{"message": map[string]any{"role": "assistant", "content": body}},
			},
		})
	}))
}

func TestCoachReadsBackAnAction(t *testing.T) {
	srv := serve(t, `{"action":"raise","amount":7000,"confidence":0.7,"agrees":true,
"reasoning":"Пара тузов на сухой доске, есть за что платить."}`, nil)
	defer srv.Close()

	got, err := llm.NewOpenAIClient("k", srv.URL, llm.GeminiModel).AdviseHand(context.Background(), spot())
	if err != nil {
		t.Fatalf("AdviseHand: %v", err)
	}
	if got.Action != "raise" || got.Amount != 7000 {
		t.Fatalf("action: got %q %g", got.Action, got.Amount)
	}
	if got.Model != llm.GeminiModel {
		t.Errorf("model: got %q, want %q", got.Model, llm.GeminiModel)
	}
	// The tool said call, so this is a disagreement whatever the model wrote
	// about itself. Believing its own claim would hide the one thing the panel
	// exists to show.
	if got.Agrees {
		t.Error("a raise against a call was reported as agreement")
	}
}

func TestCoachAgreementIsComputed(t *testing.T) {
	srv := serve(t, `{"action":"call","amount":2000,"confidence":0.6,"agrees":false,"reasoning":"Цена нормальная."}`, nil)
	defer srv.Close()

	got, err := llm.NewOpenAIClient("k", srv.URL, llm.GeminiModel).AdviseHand(context.Background(), spot())
	if err != nil {
		t.Fatalf("AdviseHand: %v", err)
	}
	if !got.Agrees {
		t.Error("a call against a call was reported as disagreement")
	}
}

// A fenced answer is what a model returns when the endpoint would not take a
// response format, which is the ordinary case on some services.
func TestCoachAcceptsAFencedAnswer(t *testing.T) {
	srv := serve(t, "```json\n{\"action\":\"fold\",\"amount\":0,\"confidence\":0.9,\"reasoning\":\"Нечего ловить.\"}\n```", nil)
	defer srv.Close()

	got, err := llm.NewOpenAIClient("k", srv.URL, llm.GeminiModel).AdviseHand(context.Background(), spot())
	if err != nil {
		t.Fatalf("AdviseHand: %v", err)
	}
	if got.Action != "fold" {
		t.Fatalf("action: got %q", got.Action)
	}
}

// The model cannot judge a spot it was not told about. Every number the
// decision rests on has to reach it.
func TestCoachIsToldTheWholeSpot(t *testing.T) {
	var sent string
	srv := serve(t, `{"action":"call","amount":2000,"confidence":0.5,"reasoning":"ok"}`, &sent)
	defer srv.Close()

	if _, err := llm.NewOpenAIClient("k", srv.URL, llm.GeminiModel).AdviseHand(context.Background(), spot()); err != nil {
		t.Fatalf("AdviseHand: %v", err)
	}
	for _, want := range []string{"Ah", "Kh", "2c", "7d", "Js", "10000", "2000", "villain", "BTN", "BB", "call"} {
		if !strings.Contains(sent, want) {
			t.Errorf("the model was not told %q\n%s", want, sent)
		}
	}
}

// Percent is a plausible thing for a model to write where a fraction was
// asked for, and a confidence of 70 would render as 7000%.
func TestCoachNormalisesAPercentConfidence(t *testing.T) {
	srv := serve(t, `{"action":"call","amount":2000,"confidence":70,"reasoning":"ok"}`, nil)
	defer srv.Close()

	got, err := llm.NewOpenAIClient("k", srv.URL, llm.GeminiModel).AdviseHand(context.Background(), spot())
	if err != nil {
		t.Fatalf("AdviseHand: %v", err)
	}
	if got.Confidence != 0.7 {
		t.Fatalf("confidence: got %v, want 0.7", got.Confidence)
	}
}

// The opponents' statistics must reach the model -- they are what a bluff
// decision is made from -- and the bluff and opinion fields must read back.
func TestCoachGetsStatsAndReturnsBluffAndOpinion(t *testing.T) {
	var prompt string
	srv := serve(t, `{"action":"raise","amount":6000,"confidence":0.65,"agrees":false,
"reasoning":"Оппонент часто пасует на к-бет.","bluff":true,"bluff_reason":"fold-to-cbet 72% на малой выборке",
"opinion":"Тул зовёт, но против такого фолдера ставка забирает банк чаще, чем окупается колл."}`, &prompt)
	defer srv.Close()

	in := spot()
	in.Stats = map[string]*storage.PlayerStats{
		"opp": {PlayerID: "opp", HandsCount: 140, VPIP: 28, PFR: 20, ThreeBet: 6,
			FoldToCBet: 0.72, FoldToCBetN: 40},
	}

	got, err := llm.NewOpenAIClient("k", srv.URL, llm.GeminiModel).AdviseHand(context.Background(), in)
	if err != nil {
		t.Fatalf("AdviseHand: %v", err)
	}
	if !strings.Contains(prompt, "fold-to-cbet 72%") {
		t.Errorf("opponent fold-to-cbet not in the prompt:\n%s", prompt)
	}
	if !strings.Contains(prompt, "VPIP 28%") {
		t.Errorf("opponent VPIP not in the prompt")
	}
	if !got.Bluff || got.BluffReason == "" {
		t.Errorf("bluff not read back: %+v", got)
	}
	if got.Opinion == "" {
		t.Error("opinion not read back")
	}
}
