package llm

import (
	"fmt"
	"strings"
)

// Endpoints and default models. Both services speak the same wire format, so
// the only thing that separates them is the address and the model name.
const (
	GeminiBaseURL = "https://generativelanguage.googleapis.com/v1beta/openai"
	GeminiModel   = "gemini-3.8-flash"

	OpenAIBaseURL = "https://api.openai.com/v1"
	OpenAIModel   = "gpt-4o-mini"

	// Vertex serves the same models to a Google Cloud project, on a service
	// account rather than an API key. The model name carries a publisher
	// prefix there, and nowhere else.
	VertexModelPrefix     = "google/"
	DefaultVertexLocation = "global"
)

// Environment variables naming the Cloud project to bill Vertex to.
var projectEnv = []string{"VERTEX_PROJECT", "GOOGLE_CLOUD_PROJECT", "GCLOUD_PROJECT"}

// Environment variables searched for a key, in order. Gemini comes first
// because it is the default service.
var keyEnv = []string{"GEMINI_API_KEY", "GOOGLE_API_KEY", "OPENAI_API_KEY"}

// Settings say which service the opponent profiler talks to.
type Settings struct {
	Key     string
	BaseURL string
	Model   string
	// KeySource names where the key was found, so the startup line can say it.
	// Empty when there is no key, which does not mean offline: a Cloud project
	// reached on a service account has no key at all.
	KeySource string

	// Project and Location address Vertex. They are only consulted when there
	// is no API key, because a key is the simpler path and wins where it
	// exists.
	Project  string
	Location string
}

// Vertex reports whether these settings point at a Cloud project rather than
// at an API key.
func (s Settings) Vertex() bool { return s.Key == "" && s.Project != "" }

// Live reports whether these settings can reach a model at all.
func (s Settings) Live() bool { return s.Key != "" }

// Describe is the one line worth printing at startup. It never contains the
// key: which service is answering and under which model is what anyone
// debugging needs, and the key is what nobody should paste into a bug report.
func (s Settings) Describe() string {
	switch {
	case s.Vertex():
		return fmt.Sprintf("%s on Vertex, project %s in %s (%s)", s.Model, s.Project, s.Location, s.KeySource)
	case s.Live():
		return fmt.Sprintf("%s at %s (key from %s)", s.Model, s.BaseURL, s.KeySource)
	default:
		return fmt.Sprintf("offline (no key in %v, no project in %v)", keyEnv, projectEnv)
	}
}

// VertexBaseURL is the OpenAI-compatible address of a project's models.
func VertexBaseURL(project, location string) string {
	host := "aiplatform.googleapis.com"
	if location != "" && location != DefaultVertexLocation {
		host = location + "-" + host
	}
	if location == "" {
		location = DefaultVertexLocation
	}
	return fmt.Sprintf("https://%s/v1/projects/%s/locations/%s/endpoints/openapi", host, project, location)
}

// VertexModel adds the publisher prefix Vertex requires and no other service
// accepts.
func VertexModel(model string) string {
	if model == "" {
		model = GeminiModel
	}
	if strings.Contains(model, "/") {
		return model
	}
	return VertexModelPrefix + model
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

	for _, name := range projectEnv {
		if v := lookup(name); v != "" {
			s.Project = v
			break
		}
	}
	s.Location = lookup("VERTEX_LOCATION")

	// With no key there is nothing more to decide here: whether a Cloud
	// project can actually be reached is a question for the credentials on the
	// machine, which NewClient asks.
	if s.Key == "" {
		return s
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
