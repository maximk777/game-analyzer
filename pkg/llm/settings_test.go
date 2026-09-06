package llm

import (
	"strings"
	"testing"
)

func env(pairs map[string]string) func(string) string {
	return func(name string) string { return pairs[name] }
}

// Setting one variable is the whole configuration.
func TestResolveDefaultsToGemini(t *testing.T) {
	got := Resolve("", "", "", env(map[string]string{"GEMINI_API_KEY": "k"}))
	if got.BaseURL != GeminiBaseURL || got.Model != GeminiModel {
		t.Fatalf("got %s at %s, want %s at %s", got.Model, got.BaseURL, GeminiModel, GeminiBaseURL)
	}
	if got.KeySource != "GEMINI_API_KEY" || !got.Live() {
		t.Fatalf("key source %q, live %v", got.KeySource, got.Live())
	}
}

// A key found in OpenAI's variable is an OpenAI key. Sending it to Google's
// address returns an unauthorised error with nothing in it that says why.
func TestResolveFollowsTheVariableTheKeyCameFrom(t *testing.T) {
	got := Resolve("", "", "", env(map[string]string{"OPENAI_API_KEY": "k"}))
	if got.BaseURL != OpenAIBaseURL || got.Model != OpenAIModel {
		t.Fatalf("got %s at %s, want the OpenAI pair", got.Model, got.BaseURL)
	}
}

// Gemini wins when both are set, because it is the default service.
func TestResolvePrefersGeminiOverOpenAI(t *testing.T) {
	got := Resolve("", "", "", env(map[string]string{
		"OPENAI_API_KEY": "openai",
		"GEMINI_API_KEY": "gemini",
	}))
	if got.Key != "gemini" || got.BaseURL != GeminiBaseURL {
		t.Fatalf("got key %q at %s", got.Key, got.BaseURL)
	}
}

func TestResolveKeepsWhatWasGiven(t *testing.T) {
	got := Resolve("mine", "http://localhost:1234/v1", "llama", env(nil))
	if got.Key != "mine" || got.BaseURL != "http://localhost:1234/v1" || got.Model != "llama" {
		t.Fatalf("a fully specified setting was overwritten: %+v", got)
	}
	if got.KeySource != "flag" {
		t.Fatalf("key source: got %q, want %q", got.KeySource, "flag")
	}
}

// A model named without an address is still a model for the default service.
func TestResolveTakesAModelWithoutAnAddress(t *testing.T) {
	got := Resolve("", "", "gemini-3.5-flash-lite", env(map[string]string{"GEMINI_API_KEY": "k"}))
	if got.Model != "gemini-3.5-flash-lite" || got.BaseURL != GeminiBaseURL {
		t.Fatalf("got %s at %s", got.Model, got.BaseURL)
	}
}

// No key anywhere is the offline case, and it has to be visible rather than
// silent: the profiler falls back to a rule of thumb and says nothing.
func TestResolveWithoutAKeyIsOffline(t *testing.T) {
	got := Resolve("", "", "", env(nil))
	if got.Live() {
		t.Fatal("no key, yet reported live")
	}
	if got.Describe() == "" {
		t.Fatal("offline settings describe themselves as nothing")
	}
}

// The key must never reach a log line or a bug report.
func TestDescribeHidesTheKey(t *testing.T) {
	got := Resolve("sk-do-not-print-me", "", "", env(nil))
	if strings.Contains(got.Describe(), "sk-do-not-print-me") {
		t.Fatalf("Describe leaked the key: %q", got.Describe())
	}
}
