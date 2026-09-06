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

// A Cloud project reached on a service account has no API key at all, so
// "no key" must not mean "no model".
func TestResolveTakesTheProjectFromTheEnvironment(t *testing.T) {
	got := Resolve("", "", "", env(map[string]string{
		"GOOGLE_CLOUD_PROJECT": "poker-1234",
		"VERTEX_LOCATION":      "europe-west4",
	}))
	if !got.Vertex() {
		t.Fatalf("a project with no key is a Vertex setting: %+v", got)
	}
	if got.Live() {
		t.Error("Live is about an API key, and there is none")
	}
	if got.Project != "poker-1234" || got.Location != "europe-west4" {
		t.Fatalf("got project %q in %q", got.Project, got.Location)
	}
}

// A key is the simpler path and wins where it exists, project or no project.
func TestAKeyBeatsAProject(t *testing.T) {
	got := Resolve("", "", "", env(map[string]string{
		"GEMINI_API_KEY":       "k",
		"GOOGLE_CLOUD_PROJECT": "poker-1234",
	}))
	if got.Vertex() {
		t.Fatal("a project was preferred to a key that was right there")
	}
	if got.BaseURL != GeminiBaseURL {
		t.Fatalf("base url: got %q", got.BaseURL)
	}
}

// Vertex is the one service that wants a publisher prefix on the model, and
// the only one that will take it.
func TestVertexModelCarriesThePublisher(t *testing.T) {
	if got := VertexModel("gemini-3.8-flash"); got != "google/gemini-3.8-flash" {
		t.Fatalf("got %q", got)
	}
	if got := VertexModel("google/gemini-3.8-flash"); got != "google/gemini-3.8-flash" {
		t.Fatalf("the prefix was added twice: %q", got)
	}
	if got := VertexModel(""); got != "google/"+GeminiModel {
		t.Fatalf("got %q", got)
	}
}

// The global endpoint has no region in its host and every other one does.
func TestVertexBaseURL(t *testing.T) {
	global := VertexBaseURL("poker-1234", DefaultVertexLocation)
	want := "https://aiplatform.googleapis.com/v1/projects/poker-1234/locations/global/endpoints/openapi"
	if global != want {
		t.Errorf("global:\n got %q\nwant %q", global, want)
	}

	regional := VertexBaseURL("poker-1234", "europe-west4")
	want = "https://europe-west4-aiplatform.googleapis.com/v1/projects/poker-1234/locations/europe-west4/endpoints/openapi"
	if regional != want {
		t.Errorf("regional:\n got %q\nwant %q", regional, want)
	}
}

// The startup line has to say which of the three it is, and never the key.
func TestDescribeNamesTheProjectOnVertex(t *testing.T) {
	s := Resolve("", "", "", env(map[string]string{"VERTEX_PROJECT": "poker-1234"}))
	if !strings.Contains(s.Describe(), "poker-1234") {
		t.Fatalf("the project is missing from %q", s.Describe())
	}
}
