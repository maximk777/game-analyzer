package llm

import (
	"context"
	"errors"
	"fmt"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
)

// vertexScope is the one scope a Cloud project's models are served under.
const vertexScope = "https://www.googleapis.com/auth/cloud-platform"

// Tokens supplies the bearer token for a request.
//
// An API key is a constant and a service account's token is not: it lasts an
// hour and has to be minted again. Holding the credential as a string worked
// for the first hour of a session and then failed for the rest of it, which is
// the kind of fault that only ever shows up at the table.
type Tokens interface {
	Token(ctx context.Context) (string, error)
}

// staticToken is an API key, which never changes.
type staticToken string

func (t staticToken) Token(context.Context) (string, error) { return string(t), nil }

// googleTokens mints access tokens from whatever credentials the machine has:
// the service account named by GOOGLE_APPLICATION_CREDENTIALS, or the
// application default credentials left by `gcloud auth application-default
// login`. It caches and refreshes on its own.
type googleTokens struct {
	src oauth2.TokenSource
}

func (g googleTokens) Token(ctx context.Context) (string, error) {
	t, err := g.src.Token()
	if err != nil {
		return "", fmt.Errorf("no access token for the service account: %w", err)
	}
	return t.AccessToken, nil
}

// NewClient builds the client the settings describe, and returns the settings
// as they were actually resolved -- which is what the startup line should say,
// because a project discovered from the credentials is not one anybody typed.
//
// An API key wins where there is one: it is the simpler path and needs nothing
// from the machine. Where there is none, the credentials on the machine are
// tried, which is how a Cloud project with Vertex enabled works without a key
// at all.
func NewClient(ctx context.Context, s Settings) (*OpenAIClient, Settings, error) {
	if s.Live() {
		return NewOpenAIClient(s.Key, s.BaseURL, s.Model), s, nil
	}

	creds, err := google.FindDefaultCredentials(ctx, vertexScope)
	if err != nil {
		return nil, s, fmt.Errorf("no API key and no Google credentials: %w", err)
	}

	if s.Project == "" {
		s.Project = creds.ProjectID
	}
	if s.Project == "" {
		return nil, s, errors.New("credentials found but no project: set VERTEX_PROJECT or GOOGLE_CLOUD_PROJECT")
	}
	if s.Location == "" {
		s.Location = DefaultVertexLocation
	}
	if s.BaseURL == "" {
		s.BaseURL = VertexBaseURL(s.Project, s.Location)
	}
	s.Model = VertexModel(s.Model)
	s.KeySource = "service account"

	return newTokenClient(googleTokens{src: creds.TokenSource}, s.BaseURL, s.Model), s, nil
}
