package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"poker-game-analyzer/pkg/storage"
)

// rotatingTokens is a credential that expires, which is what a service account
// is. An API key is a constant and a service account's token lasts an hour.
type rotatingTokens struct{ n int32 }

func (r *rotatingTokens) Token(context.Context) (string, error) {
	return fmt.Sprintf("token-%d", atomic.AddInt32(&r.n, 1)), nil
}

type failingTokens struct{}

func (failingTokens) Token(context.Context) (string, error) {
	return "", fmt.Errorf("the service account key was revoked")
}

// Holding the credential as a string worked for the first hour of a session
// and failed for the rest of it, which is a fault that only ever shows up at
// the table.
func TestEveryCallAsksForAFreshToken(t *testing.T) {
	var seen []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.Header.Get("Authorization"))
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{
				{"message": map[string]any{"role": "assistant", "content": `{"archetype":"TAG"}`}},
			},
		})
	}))
	defer server.Close()

	c := newTokenClient(&rotatingTokens{}, server.URL, VertexModel(GeminiModel))
	for range 2 {
		if _, err := c.AnalyzePlayer(context.Background(), nil, storage.PlayerStats{PlayerID: "p"}); err != nil {
			t.Fatalf("AnalyzePlayer: %v", err)
		}
	}

	if len(seen) != 2 {
		t.Fatalf("expected two calls, got %d", len(seen))
	}
	if seen[0] == seen[1] {
		t.Fatalf("the same token was sent twice: %q", seen[0])
	}
	for i, h := range seen {
		if want := fmt.Sprintf("Bearer token-%d", i+1); h != want {
			t.Errorf("call %d sent %q, want %q", i, h, want)
		}
	}
}

// A credential that cannot be minted is reported, not sent as an empty bearer
// that comes back as an unauthorised error with nothing in it about why.
func TestATokenThatCannotBeMintedIsReported(t *testing.T) {
	var called int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&called, 1)
	}))
	defer server.Close()

	c := newTokenClient(failingTokens{}, server.URL, VertexModel(GeminiModel))
	_, err := c.AnalyzePlayer(context.Background(), nil, storage.PlayerStats{PlayerID: "p"})
	if err == nil {
		t.Fatal("a missing credential reported success")
	}
	if atomic.LoadInt32(&called) != 0 {
		t.Error("the request was sent with no credential")
	}
}

// An API key is the same string every time, and asking for it must not cost a
// round trip.
func TestAnAPIKeyIsSentAsIs(t *testing.T) {
	var got string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{
				{"message": map[string]any{"role": "assistant", "content": `{"archetype":"TAG"}`}},
			},
		})
	}))
	defer server.Close()

	c := NewOpenAIClient("sk-key", server.URL, GeminiModel)
	if _, err := c.AnalyzePlayer(context.Background(), nil, storage.PlayerStats{PlayerID: "p"}); err != nil {
		t.Fatalf("AnalyzePlayer: %v", err)
	}
	if got != "Bearer sk-key" {
		t.Fatalf("got %q", got)
	}
}
