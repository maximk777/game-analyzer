package llm_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"poker-game-analyzer/pkg/llm"
	"poker-game-analyzer/pkg/storage"
)

func answer(content string) map[string]any {
	return map[string]any{
		"choices": []map[string]any{
			{"message": map[string]any{"role": "assistant", "content": content}},
		},
	}
}

const profileJSON = `{"archetype":"TAG","bluff_frequency":0.22,"fold_to_3bet":0.55,
"fold_to_cbet":0.45,"exploits":"Barrel turns.","notes":"Solid."}`

// Not every OpenAI-compatible endpoint accepts the JSON response format, and a
// rejected field returns a 400 that reads exactly like a bad key. The call is
// retried without it, once, and the endpoint is not asked again.
func TestOpenAIClientRetriesWithoutTheJSONFormat(t *testing.T) {
	var calls, withFormat int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		var body struct {
			ResponseFormat map[string]string `json:"response_format"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body.ResponseFormat != nil {
			atomic.AddInt32(&withFormat, 1)
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":{"message":"Unknown name \"response_format\""}}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(answer(profileJSON))
	}))
	defer server.Close()

	client := llm.NewOpenAIClient("k", server.URL, llm.GeminiModel)
	stats := storage.PlayerStats{PlayerID: "p", PlayerName: "p", HandsCount: 40, VPIP: 22, PFR: 18, AF: 2.1}

	got, err := client.AnalyzePlayer(context.Background(), nil, stats)
	if err != nil {
		t.Fatalf("the retry did not recover the call: %v", err)
	}
	if got.Archetype != "TAG" {
		t.Fatalf("archetype: got %q, want TAG", got.Archetype)
	}
	if calls != 2 {
		t.Fatalf("expected one rejected call and one retry, got %d calls", calls)
	}

	// The second analysis must not pay for the same rejection again.
	if _, err := client.AnalyzePlayer(context.Background(), nil, stats); err != nil {
		t.Fatalf("second analysis: %v", err)
	}
	if withFormat != 1 {
		t.Fatalf("the response format was asked for %d times, want 1", withFormat)
	}
}

// A 400 that is not about the response format is still a failure, and it must
// be reported rather than retried into a second identical failure.
func TestOpenAIClientDoesNotRetryForever(t *testing.T) {
	var calls int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"message":"model not found"}}`))
	}))
	defer server.Close()

	client := llm.NewOpenAIClient("k", server.URL, "nope")
	_, err := client.AnalyzePlayer(context.Background(), nil, storage.PlayerStats{PlayerID: "p"})
	if err == nil {
		t.Fatal("a failing endpoint reported success")
	}
	if calls > 2 {
		t.Fatalf("retried %d times", calls)
	}
}

// A trailing slash on the base URL is what Google's own documentation prints,
// and it must not turn into a double slash in the path.
func TestOpenAIClientToleratesATrailingSlash(t *testing.T) {
	var path string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(answer(profileJSON))
	}))
	defer server.Close()

	client := llm.NewOpenAIClient("k", server.URL+"/", llm.GeminiModel)
	if _, err := client.AnalyzePlayer(context.Background(), nil, storage.PlayerStats{PlayerID: "p"}); err != nil {
		t.Fatalf("analyze: %v", err)
	}
	if path != "/chat/completions" {
		t.Fatalf("endpoint path: got %q, want %q", path, "/chat/completions")
	}
}

// The model name reaches the service unchanged: a typo here fails as an
// unhelpful 404 hours later, at the table.
func TestOpenAIClientSendsTheConfiguredModel(t *testing.T) {
	var sent string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Model string `json:"model"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		sent = body.Model
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(answer(profileJSON))
	}))
	defer server.Close()

	client := llm.NewOpenAIClient("k", server.URL, llm.GeminiModel)
	if _, err := client.AnalyzePlayer(context.Background(), nil, storage.PlayerStats{PlayerID: "p"}); err != nil {
		t.Fatalf("analyze: %v", err)
	}
	if sent != llm.GeminiModel {
		t.Fatalf("model: got %q, want %q", sent, llm.GeminiModel)
	}
}

// Google answers a disabled API with about fifteen hundred characters of JSON
// that says the same thing five times, in a details array, a localized message
// and an activation URL. Verbatim, that went into the error and from there onto
// the panel, where it pushed the table off the screen.
func TestAPIRefusalIsReducedToOneSentence(t *testing.T) {
	body := `[{ "error": { "code": 403, "message": "Gemini API has not been used in project 220159039139 before or it is disabled. Enable it by visiting https://console.developers.google.com/apis/api/generativelanguage.googleapis.com/overview?project=220159039139 then retry. If you enabled this API recently, wait a few minutes for the action to propagate to our systems and retry.", "status": "PERMISSION_DENIED", "details": [ { "@type": "type.googleapis.com/google.rpc.ErrorInfo", "reason": "SERVICE_DISABLED", "domain": "googleapis.com" } ] } }]`

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(body))
	}))
	defer server.Close()

	_, err := llm.NewOpenAIClient("k", server.URL, llm.GeminiModel).
		AnalyzePlayer(context.Background(), nil, storage.PlayerStats{PlayerID: "p"})
	if err == nil {
		t.Fatal("a 403 reported success")
	}

	got := err.Error()
	if len(got) > 200 {
		t.Errorf("the error is %d characters long:\n%s", len(got), got)
	}
	if !strings.Contains(got, "has not been used in project") {
		t.Errorf("the reason was lost: %q", got)
	}
	for _, noise := range []string{"@type", "SERVICE_DISABLED", "console.developers.google.com"} {
		if strings.Contains(got, noise) {
			t.Errorf("%q survived into the message: %q", noise, got)
		}
	}
}
