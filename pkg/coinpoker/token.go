// Package coinpoker reads the operator's own player statistics -- the numbers
// its client shows in the player popup -- from the same REST endpoint that
// client calls.
//
// The wire (pkg/sfs) says everything about the hand being played and almost
// nothing about the players: a seat carries a VPIP over the hands played this
// session, which on the first hand at a new table is one hand or none. The site
// itself holds a thirty-day aggregate over every table the player has sat at,
// and hands it to its own client on request. A read we cannot have for two
// hundred hands is available before the first card, so it is worth asking for.
//
// Nothing here writes to the site or plays for the operator: one authenticated
// GET, with the operator's own token, for the operator's own table.
package coinpoker

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Token is a bearer token for the stats API, with what it says about itself.
type Token struct {
	Value string
	// UserID is the account the token was issued to -- hero, since it is hero's
	// client that holds it. Used to tell hero's own record apart from the
	// opponents' in a reply.
	UserID string
	Expiry time.Time
}

// Valid reports whether the token is still good at t.
func (tk Token) Valid(at time.Time) bool {
	return tk.Value != "" && tk.Expiry.After(at)
}

// IndexedDBDir is where the client keeps the browser storage its auth state
// lives in. The token is a Firebase ID token, rotated about hourly, and the
// running client keeps the current one on disk -- so it is read fresh rather
// than captured, and no packet capture or interception is involved.
func IndexedDBDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, "Library", "Application Support", "CoinPoker", "IndexedDB")
}

// jwtPattern matches a JSON Web Token as it sits in the storage files: three
// base64url segments, the first two of which begin with an encoded '{"'.
var jwtPattern = regexp.MustCompile(`eyJ[A-Za-z0-9_-]{10,}\.eyJ[A-Za-z0-9_-]{20,}\.[A-Za-z0-9_-]{10,}`)

// ErrNoToken is returned when the storage holds no unexpired token, which in
// practice means the client is not running or not logged in.
var ErrNoToken = errors.New("coinpoker: no valid token in the client's storage")

// ReadToken returns the freshest unexpired token found in the client's storage.
//
// Freshest, not first: the files keep every token the session has been issued,
// so the one with the furthest expiry is the current one. A token from another
// account or another service that happens to be stored alongside is rejected by
// its claims rather than by where it was found.
func ReadToken() (Token, error) { return ReadTokenAt(IndexedDBDir(), time.Now()) }

// ReadTokenAt is ReadToken over a given directory and clock, for the tests.
func ReadTokenAt(dir string, now time.Time) (Token, error) {
	if dir == "" {
		return Token{}, ErrNoToken
	}
	var best Token
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			// An unreadable file is not a reason to stop: the client writes
			// these while we read them.
			return nil //nolint:nilerr // keep walking
		}
		if d.IsDir() {
			return nil
		}
		switch strings.ToLower(filepath.Ext(path)) {
		case ".ldb", ".log":
		default:
			return nil
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		for _, m := range jwtPattern.FindAll(body, -1) {
			tk, ok := parseToken(string(m), now)
			if ok && tk.Expiry.After(best.Expiry) {
				best = tk
			}
		}
		return nil
	})
	if err != nil && best.Value == "" {
		return Token{}, err
	}
	if best.Value == "" {
		return Token{}, ErrNoToken
	}
	return best, nil
}

// tokenClaims is the part of a token's payload that says whether it is ours.
type tokenClaims struct {
	Issuer   string          `json:"iss"`
	Audience string          `json:"aud"`
	Expiry   int64           `json:"exp"`
	UserID   json.RawMessage `json:"user_id"`
	Subject  string          `json:"sub"`
}

// parseToken reads a token's claims and reports whether it is a live CoinPoker
// token. The signature is not checked: it is the site's business to verify it,
// and we could not -- the point here is to avoid sending a token that belongs to
// something else, and to know when this one goes stale.
func parseToken(raw string, now time.Time) (Token, bool) {
	parts := strings.Split(raw, ".")
	if len(parts) != 3 {
		return Token{}, false
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return Token{}, false
	}
	var c tokenClaims
	if err := json.Unmarshal(payload, &c); err != nil {
		return Token{}, false
	}
	if !strings.Contains(c.Issuer, "securetoken") || !strings.Contains(c.Audience, "coinpoker") {
		return Token{}, false
	}
	exp := time.Unix(c.Expiry, 0)
	if !exp.After(now) {
		return Token{}, false
	}
	return Token{Value: raw, UserID: claimString(c.UserID, c.Subject), Expiry: exp}, true
}

// claimString reads the user id, which arrives as a string in some tokens and a
// number in others, falling back to the subject claim.
func claimString(raw json.RawMessage, fallback string) string {
	if len(raw) > 0 {
		var s string
		if err := json.Unmarshal(raw, &s); err == nil && s != "" {
			return s
		}
		var n json.Number
		if err := json.Unmarshal(raw, &n); err == nil && n.String() != "" {
			return n.String()
		}
	}
	return fallback
}

// idsFromStrings keeps the numeric ids in order, without repeats -- the query
// takes a comma-separated list and a repeated id wastes a slot in it.
func idsFromStrings(ids []string) []string {
	seen := make(map[string]bool, len(ids))
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		if id == "" || seen[id] {
			continue
		}
		if _, err := strconv.ParseInt(id, 10, 64); err != nil {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	return out
}
