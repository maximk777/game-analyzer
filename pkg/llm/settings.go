package llm

import "fmt"

// Endpoints and default models. Both services speak the same wire format, so
// the only thing that separates them is the address and the model name.
const (
	GeminiBaseURL = "https://generativelanguage.googleapis.com/v1beta/openai"
	GeminiModel   = "gemini-3.8-flash"

	OpenAIBaseURL = "https://api.openai.com/v1"
	OpenAIModel   = "gpt-4o-mini"
)

// Environment variables searched for a key, in order. Gemini comes first
// because it is the default service.
var keyEnv = []string{"GEMINI_API_KEY", "GOOGLE_API_KEY", "OPENAI_API_KEY"}

// Settings say which service the opponent profiler talks to.
type Settings struct {
	Key     string
	BaseURL string
	Model   string
	// KeySource names where the key was found, so the startup line can say it.
	// Empty when there is no key, which is what puts the profiler offline.
	KeySource string
}

// Live reports whether these settings can reach a model at all.
func (s Settings) Live() bool { return s.Key != "" }

// Describe is the one line worth printing at startup. It never contains the
// key: which service is answering and under which model is what anyone
// debugging needs, and the key is what nobody should paste into a bug report.
func (s Settings) Describe() string {
	if !s.Live() {
		return fmt.Sprintf("offline (no key in %v)", keyEnv)
	}
	return fmt.Sprintf("%s at %s (key from %s)", s.Model, s.BaseURL, s.KeySource)
}

// Resolve fills in whatever the command line left out.
//
// Gemini Flash is the default. Which service a bare key belongs to is decided
// by the variable it was found in, so setting GEMINI_API_KEY is the whole
// configuration -- no flags, and no chance of sending a Google key to OpenAI's
// address and getting an unauthorised error with no hint as to why.
func Resolve(key, baseURL, model string, lookup func(string) string) Settings {
	s := Settings{Key: key, BaseURL: baseURL, Model: model, KeySource: "flag"}

	if s.Key == "" {
		s.KeySource = ""
		for _, name := range keyEnv {
			if v := lookup(name); v != "" {
				s.Key, s.KeySource = v, name
				break
			}
		}
	}

	if s.BaseURL == "" {
		if s.KeySource == "OPENAI_API_KEY" {
			s.BaseURL = OpenAIBaseURL
		} else {
			s.BaseURL = GeminiBaseURL
		}
	}

	if s.Model == "" {
		if s.BaseURL == OpenAIBaseURL {
			s.Model = OpenAIModel
		} else {
			s.Model = GeminiModel
		}
	}

	return s
}
