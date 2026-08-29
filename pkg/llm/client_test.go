package llm_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"poker-game-analyzer/pkg/llm"
	"poker-game-analyzer/pkg/storage"
	"poker-game-analyzer/pkg/table"
)

func TestMockClient_AnalyzePlayer_DefaultDeterministic(t *testing.T) {
	client := llm.NewMockClient()

	// Test TAG stats
	tagStats := storage.PlayerStats{
		PlayerID:   "p_tag",
		PlayerName: "TightAggro",
		HandsCount: 50,
		VPIP:       22.0,
		PFR:        19.0,
		ThreeBet:   8.0,
		AF:         2.8,
	}

	prof, err := client.AnalyzePlayer(context.Background(), nil, tagStats)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if prof == nil {
		t.Fatalf("expected non-nil profile")
	}
	if prof.PlayerID != tagStats.PlayerID {
		t.Errorf("expected PlayerID %s, got %s", tagStats.PlayerID, prof.PlayerID)
	}
	if prof.Archetype != "TAG" {
		t.Errorf("expected TAG archetype, got %s", prof.Archetype)
	}
	if prof.BluffFrequency < 0 || prof.BluffFrequency > 1 {
		t.Errorf("bluff frequency out of bounds: %f", prof.BluffFrequency)
	}

	// Test Fish / Whale stats
	fishStats := storage.PlayerStats{
		PlayerID:   "p_fish",
		PlayerName: "CallingStation",
		HandsCount: 40,
		VPIP:       45.0,
		PFR:        8.0,
		ThreeBet:   2.0,
		AF:         0.5,
	}

	profFish, err := client.AnalyzePlayer(context.Background(), nil, fishStats)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if profFish.Archetype != "Fish/Whale" && profFish.Archetype != "Loose-Passive" {
		t.Errorf("expected Fish archetype, got %s", profFish.Archetype)
	}
}

func TestMockClient_AnalyzePlayer_CustomFunc(t *testing.T) {
	client := llm.NewMockClient()
	client.AnalyzeFunc = func(ctx context.Context, history []table.HandState, stats storage.PlayerStats) (*storage.LLMProfile, error) {
		return &storage.LLMProfile{
			PlayerID:       stats.PlayerID,
			PlayerName:     stats.PlayerName,
			Archetype:      "CustomArchetype",
			BluffFrequency: 0.42,
			FoldTo3Bet:     0.70,
			FoldToCBet:     0.65,
			Exploits:       "Custom exploit",
			Notes:          "Custom notes",
		}, nil
	}

	stats := storage.PlayerStats{PlayerID: "p1", PlayerName: "Test"}
	prof, err := client.AnalyzePlayer(context.Background(), nil, stats)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if prof.Archetype != "CustomArchetype" || prof.BluffFrequency != 0.42 {
		t.Errorf("unexpected profile: %+v", prof)
	}
}

func TestOpenAIClient_AnalyzePlayer_Success(t *testing.T) {
	mockResponseJSON := `{
		"archetype": "LAG",
		"bluff_frequency": 0.38,
		"fold_to_3bet": 0.45,
		"fold_to_cbet": 0.50,
		"tilt_risk": "High",
		"exploits": "4-bet bluff wider and call down lighter on river.",
		"notes": "Plays very aggressively preflop and barrels turn aggressively."
	}`

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST method, got %s", r.Method)
		}
		if r.Header.Get("Authorization") != "Bearer test-key" {
			t.Errorf("expected Bearer test-key, got %s", r.Header.Get("Authorization"))
		}
		if r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("expected application/json, got %s", r.Header.Get("Content-Type"))
		}

		resp := map[string]interface{}{
			"id": "chatcmpl-123",
			"choices": []map[string]interface{}{
				{
					"message": map[string]interface{}{
						"role":    "assistant",
						"content": mockResponseJSON,
					},
				},
			},
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := llm.NewOpenAIClient("test-key", server.URL, "gpt-4o-mini")

	stats := storage.PlayerStats{
		PlayerID:   "p_lag",
		PlayerName: "AggroManiac",
		HandsCount: 30,
		VPIP:       32.0,
		PFR:        26.0,
		ThreeBet:   12.0,
		AF:         3.5,
	}

	c1, _ := table.ParseCard("Ah")
	c2, _ := table.ParseCard("Kh")
	history := []table.HandState{
		{
			HandID:    "h1",
			TableID:   "t1",
			Street:    table.StreetRiver,
			Pot:       100,
			HeroID:    "hero",
			HeroCards: [2]table.Card{c1, c2},
			ActionHistory: []table.ActionRecord{
				{PlayerID: "p_lag", Street: table.StreetPreflop, Action: table.ActionRaise, Amount: 3.0},
			},
		},
	}

	prof, err := client.AnalyzePlayer(context.Background(), history, stats)
	if err != nil {
		t.Fatalf("AnalyzePlayer failed: %v", err)
	}
	if prof == nil {
		t.Fatalf("expected non-nil profile")
	}

	if prof.PlayerID != "p_lag" || prof.PlayerName != "AggroManiac" {
		t.Errorf("player metadata mismatch: %+v", prof)
	}
	if prof.Archetype != "LAG" {
		t.Errorf("expected LAG, got %s", prof.Archetype)
	}
	if prof.BluffFrequency != 0.38 {
		t.Errorf("expected bluff freq 0.38, got %f", prof.BluffFrequency)
	}
	if prof.FoldTo3Bet != 0.45 {
		t.Errorf("expected fold to 3bet 0.45, got %f", prof.FoldTo3Bet)
	}
	if prof.FoldToCBet != 0.50 {
		t.Errorf("expected fold to cbet 0.50, got %f", prof.FoldToCBet)
	}
	if prof.Exploits == "" || prof.Notes == "" {
		t.Errorf("expected non-empty exploits and notes: %+v", prof)
	}
}

func TestOpenAIClient_AnalyzePlayer_MarkdownFencedJSON(t *testing.T) {
	mockResponseJSON := "```json\n{\n\"archetype\": \"Nit\",\n\"bluff_frequency\": 0.05,\n\"fold_to_3bet\": 0.85,\n\"fold_to_cbet\": 0.75,\n\"exploits\": \"Steal blinds aggressively.\",\n\"notes\": \"Only continues with premium hands.\"\n}\n```"

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]interface{}{
			"choices": []map[string]interface{}{
				{
					"message": map[string]interface{}{
						"role":    "assistant",
						"content": mockResponseJSON,
					},
				},
			},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := llm.NewOpenAIClient("test-key", server.URL, "gpt-4o-mini")
	stats := storage.PlayerStats{PlayerID: "p_nit", PlayerName: "Rock"}

	prof, err := client.AnalyzePlayer(context.Background(), nil, stats)
	if err != nil {
		t.Fatalf("AnalyzePlayer failed on markdown JSON: %v", err)
	}
	if prof.Archetype != "Nit" || prof.BluffFrequency != 0.05 {
		t.Errorf("unexpected profile: %+v", prof)
	}
}

func TestOpenAIClient_AnalyzePlayer_Errors(t *testing.T) {
	// 1. HTTP 500 error
	serverErr := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "internal server error", http.StatusInternalServerError)
	}))
	defer serverErr.Close()

	client := llm.NewOpenAIClient("test-key", serverErr.URL, "gpt-4o-mini")
	stats := storage.PlayerStats{PlayerID: "p1"}
	_, err := client.AnalyzePlayer(context.Background(), nil, stats)
	if err == nil {
		t.Errorf("expected error on HTTP 500, got nil")
	}

	// 2. Malformed JSON
	serverBadJSON := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]interface{}{
			"choices": []map[string]interface{}{
				{
					"message": map[string]interface{}{
						"content": "{invalid json",
					},
				},
			},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer serverBadJSON.Close()

	clientBadJSON := llm.NewOpenAIClient("test-key", serverBadJSON.URL, "gpt-4o-mini")
	_, err = clientBadJSON.AnalyzePlayer(context.Background(), nil, stats)
	if err == nil {
		t.Errorf("expected error on bad JSON content, got nil")
	}

	// 3. Context cancelled
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = client.AnalyzePlayer(ctx, nil, stats)
	if err == nil {
		t.Errorf("expected error on cancelled context, got nil")
	}
}
