package slumbot

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// DefaultBase is the public endpoint. No key, no registration.
const DefaultBase = "https://slumbot.com/api"

// Response is one reply from either endpoint. The money fields are pointers
// because zero is a real value for all of them and their absence is what says
// the hand is still running.
type Response struct {
	OldAction    string   `json:"old_action"`
	Action       string   `json:"action"`
	ClientPos    int      `json:"client_pos"`
	HoleCards    []string `json:"hole_cards"`
	Board        []string `json:"board"`
	Token        string   `json:"token"`
	BotHoleCards []string `json:"bot_hole_cards"`

	Winnings         *int `json:"winnings"`
	BaselineWinnings *int `json:"baseline_winnings"`
	SessionNumHands  int  `json:"session_num_hands"`
	SessionTotal     int  `json:"session_total"`
	// SessionBaselineTotal is Slumbot's own variance reduction, accumulated
	// over the session. We cannot deal paired decks against a server, so this
	// is the only variance reduction available here.
	SessionBaselineTotal int `json:"session_baseline_total"`

	ErrorMsg string `json:"error_msg"`
}

// Over reports whether the hand has finished and been paid.
func (r *Response) Over() bool { return r != nil && r.Winnings != nil }

// HeroSeat is which seat this hand was dealt to.
func (r *Response) HeroSeat() Seat { return Seat(r.ClientPos) }

// Client plays a session. It is not safe for concurrent use: a session is a
// single conversation, and position alternates with it.
type Client struct {
	HTTP *http.Client
	Base string
	// Pace is the minimum gap between requests. Slumbot answers a fast caller
	// with an error rather than a rate-limit status, and that error is
	// indistinguishable from a real one except by retrying it, so the gap is
	// cheaper than the recovery.
	Pace time.Duration
	// Retries is how many times a transient error is retried before giving up.
	Retries int

	// token is held across hands deliberately. A new_hand without one always
	// deals the big blind -- verified over eight consecutive hands -- and only
	// a carried token alternates the button. Terminal responses carry no token
	// of their own, so the old one stays valid and must not be cleared.
	token string
	last  time.Time
	hands int
}

func NewClient() *Client {
	return &Client{
		HTTP:    &http.Client{Timeout: 30 * time.Second},
		Base:    DefaultBase,
		Pace:    600 * time.Millisecond,
		Retries: 5,
	}
}

// Hands is how many hands this session has been paid for.
func (c *Client) Hands() int { return c.hands }

// fatalErrors are Slumbot's replies to a request that was wrong rather than
// early. Retrying one is pointless and hides a bug in the action we sent.
var fatalErrors = []string{
	"unexpected action", "bet size too big", "illegal bet", "bad token",
}

// ActionRejected is Slumbot refusing the action we sent, as opposed to the
// network failing or the server being busy. It is a bug in what we asked for,
// so the caller can decide to fall back rather than retry the same thing.
type ActionRejected struct {
	Incr string
	Msg  string
}

func (e *ActionRejected) Error() string {
	return fmt.Sprintf("slumbot rejected %q: %s", e.Incr, e.Msg)
}

func isFatal(msg string) bool {
	m := strings.ToLower(msg)
	for _, f := range fatalErrors {
		if strings.Contains(m, f) {
			return true
		}
	}
	return false
}

func (c *Client) post(ctx context.Context, path string, payload map[string]any) (*Response, error) {
	var lastErr error
	for attempt := 0; attempt <= c.Retries; attempt++ {
		if gap := c.Pace - time.Since(c.last); gap > 0 {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(gap):
			}
		}
		r, err := c.postOnce(ctx, path, payload)
		c.last = time.Now()
		switch {
		case err != nil:
			lastErr = err
		case r.ErrorMsg == "":
			return r, nil
		case isFatal(r.ErrorMsg):
			if incr, ok := payload["incr"].(string); ok {
				return nil, &ActionRejected{Incr: incr, Msg: r.ErrorMsg}
			}
			return nil, fmt.Errorf("%s: %s", path, r.ErrorMsg)
		default:
			lastErr = fmt.Errorf("%s: %s", path, r.ErrorMsg)
		}
		// Back off: the failures seen in practice clear within a second.
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(time.Duration(attempt+1) * time.Second):
		}
	}
	return nil, fmt.Errorf("after %d retries: %w", c.Retries, lastErr)
}

func (c *Client) postOnce(ctx context.Context, path string, payload map[string]any) (*Response, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.Base+"/"+path, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s: http %d: %s", path, resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	var r Response
	if err := json.Unmarshal(raw, &r); err != nil {
		return nil, fmt.Errorf("%s: %w (body %.200q)", path, err, raw)
	}
	return &r, nil
}

// NewHand deals the next hand of the session.
func (c *Client) NewHand(ctx context.Context) (*Response, error) {
	payload := map[string]any{}
	if c.token != "" {
		payload["token"] = c.token
	}
	r, err := c.post(ctx, "new_hand", payload)
	if err != nil {
		return nil, err
	}
	if r.Token != "" {
		c.token = r.Token
	}
	if c.token == "" {
		return nil, fmt.Errorf("new_hand returned no token and none was held")
	}
	return r, nil
}

// Act sends one action: "f", "k", "c", or "b" followed by the actor's total
// contribution for the current street.
func (c *Client) Act(ctx context.Context, incr string) (*Response, error) {
	r, err := c.post(ctx, "act", map[string]any{"token": c.token, "incr": incr})
	if err != nil {
		return nil, err
	}
	if r.Over() {
		c.hands++
	}
	return r, nil
}
