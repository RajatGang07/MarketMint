package groww

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"sync"
	"time"
)

// TokenSource hands out a valid Groww access token, minting a new one when the
// current one is missing or close to expiry.
//
// Groww supports two shapes:
//   - a pre-generated access token pasted into the environment, and
//   - an API key + secret pair exchanged for a short-lived token via
//     POST /token/api/access with a SHA256(secret + epochSeconds) checksum.
//
// Minted tokens expire the next morning (06:00 IST), so the second shape is the
// one you want for anything long-running.
type TokenSource struct {
	baseURL string
	apiKey  string
	secret  string
	http    *http.Client

	mu      sync.Mutex
	token   string
	expires time.Time // zero means "static token, never refresh"
}

// NewTokenSource builds a source. staticToken wins if present; otherwise
// apiKey+secret are used to mint tokens on demand.
func NewTokenSource(baseURL, apiKey, secret, staticToken string, hc *http.Client) *TokenSource {
	return &TokenSource{
		baseURL: baseURL,
		apiKey:  apiKey,
		secret:  secret,
		token:   staticToken,
		http:    hc,
	}
}

// Token returns a usable access token, refreshing if needed.
func (t *TokenSource) Token(ctx context.Context) (string, error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	// Static token supplied by the operator: use as-is.
	if t.token != "" && t.expires.IsZero() {
		return t.token, nil
	}
	// Refresh a minute early to avoid racing the boundary.
	if t.token != "" && time.Now().Add(time.Minute).Before(t.expires) {
		return t.token, nil
	}
	if t.apiKey == "" || t.secret == "" {
		return "", fmt.Errorf("groww: no access token and no api key/secret to mint one")
	}
	return t.mint(ctx)
}

// Invalidate drops the cached token so the next call mints a fresh one. Called
// when the API answers 401 mid-flight.
func (t *TokenSource) Invalidate() {
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.expires.IsZero() {
		t.token = ""
	}
}

type tokenResponse struct {
	Token   string `json:"token"`
	Expiry  string `json:"expiry"` // e.g. "2026-07-24T06:00:00" (IST, no zone)
	Active  bool   `json:"active"`
	Session string `json:"sessionName"`
}

// mint performs the checksum handshake. Caller must hold t.mu.
func (t *TokenSource) mint(ctx context.Context) (string, error) {
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	sum := sha256.Sum256([]byte(t.secret + ts))

	body, err := json.Marshal(map[string]string{
		"key_type":  "approval",
		"checksum":  hex.EncodeToString(sum[:]),
		"timestamp": ts,
	})
	if err != nil {
		return "", err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, t.baseURL+"/token/api/access", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+t.apiKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := t.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("groww: token request: %w", err)
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("groww: token request failed (%d): %s", resp.StatusCode, truncate(raw, 300))
	}

	var tr tokenResponse
	if err := json.Unmarshal(raw, &tr); err != nil {
		return "", fmt.Errorf("groww: decode token response: %w", err)
	}
	if tr.Token == "" {
		return "", fmt.Errorf("groww: token response had no token: %s", truncate(raw, 300))
	}

	t.token = tr.Token
	t.expires = parseExpiry(tr.Expiry)
	return t.token, nil
}

// parseExpiry reads Groww's zone-less IST timestamp. Anything unparseable falls
// back to a conservative one-hour lifetime.
func parseExpiry(s string) time.Time {
	ist := time.FixedZone("IST", 5*3600+1800)
	if ts, err := time.ParseInLocation("2006-01-02T15:04:05", s, ist); err == nil {
		return ts
	}
	return time.Now().Add(time.Hour)
}

func truncate(b []byte, n int) string {
	if len(b) <= n {
		return string(b)
	}
	return string(b[:n]) + "…"
}
