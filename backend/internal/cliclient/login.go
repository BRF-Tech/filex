package cliclient

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// ───────────────────────── login ─────────────────────────

// LoginRequest is what a password sign-in sends to POST /api/auth/login.
type LoginRequest struct {
	// Email is what the person signs in with: an e-mail address, or a user
	// name (the server resolves both).
	Email    string
	Password string
	// TOTP is the second-factor code, for accounts with TOTP enabled.
	TOTP string
	// Realm names the tenant on a multi-tenant server (docs/MULTI-TENANCY.md,
	// Realms): "" is the tenant the server's address names, else the
	// platform's own accounts. Sent only when set; a single-tenant server - or
	// an older one - does not read it.
	Realm string
}

// LoginResponse is the subset of the sign-in answer the CLI needs.
type LoginResponse struct {
	Token string `json:"token"`
	// URL is the address the session belongs to: the server signed in to, or -
	// when that server handed the sign-in to the tenant's own address - that
	// address, which later commands should use.
	URL string `json:"-"`
	// HandedOff reports that the server answered with a handoff and the session
	// was opened at URL, the tenant's own address.
	HandedOff bool   `json:"-"`
	Raw       []byte `json:"-"`
}

// handoffCodeLife is how long the server keeps a handoff code (handoffTTL in
// internal/api/handlers), for the messages that explain a refused one.
const handoffCodeLife = "60 seconds"

// Login exchanges the credentials for a session token. Call on a token-less
// Client.
//
// On a multi-tenant server a sign-in whose realm names a tenant with an address
// of its own is not given a session at the platform's address: the answer is a
// one-use handoff code for the tenant's address ({"handoff": {origin, code}}).
// Login follows it - it redeems the code there (POST /api/auth/handoff) and
// returns that session, with URL set to the tenant's address.
//
// A realm nobody has is answered by the server exactly as a wrong password,
// and Login reports it as the same error.
func (c *Client) Login(ctx context.Context, in LoginRequest) (*LoginResponse, error) {
	fields := map[string]string{
		"email":    in.Email,
		"password": in.Password,
		"totp":     in.TOTP,
	}
	if realm := strings.TrimSpace(in.Realm); realm != "" {
		fields["realm"] = realm
	}
	body, err := json.Marshal(fields)
	if err != nil {
		return nil, err
	}
	req, err := c.newRequest(ctx, http.MethodPost, "/api/auth/login", nil, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	raw, err := c.doJSON(req)
	if err != nil {
		if IsTOTPRequired(err) {
			return nil, fmt.Errorf("%w (this account has two-factor auth - pass --totp <code>)", err)
		}
		return nil, err
	}
	var ans struct {
		Token   string `json:"token"`
		Handoff *struct {
			Origin string `json:"origin"`
			Code   string `json:"code"`
		} `json:"handoff"`
	}
	if err := json.Unmarshal(raw, &ans); err != nil {
		return nil, fmt.Errorf("parse login response: %w", err)
	}
	switch {
	case ans.Token != "":
		return &LoginResponse{Token: ans.Token, URL: c.BaseURL, Raw: raw}, nil
	case ans.Handoff != nil:
		return c.redeemHandoff(ctx, ans.Handoff.Origin, ans.Handoff.Code)
	}
	return nil, errors.New("login response carried no token")
}

// redeemHandoff opens the session at the tenant's own address with the code
// the platform's address handed out.
//
// ⚠ The code is a session for as long as it lives, so it goes only to an
// http(s) address, never from an encrypted address to an unencrypted one, and
// is never printed - an error names the address and what to do instead.
func (c *Client) redeemHandoff(ctx context.Context, origin, code string) (*LoginResponse, error) {
	target, err := handoffTarget(c.BaseURL, origin)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(code) == "" {
		return nil, fmt.Errorf("the server handed this sign-in to %s without a code - sign in there directly: filex client login --url %s", target, target)
	}
	body, err := json.Marshal(map[string]string{"code": code})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target+"/api/auth/handoff", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	raw, err := c.doJSON(req)
	if err != nil {
		return nil, fmt.Errorf("the server handed this sign-in to the tenant's own address %s, which did not accept it: %w - the code it carried works once, there only, for %s; sign in there directly: filex client login --url %s",
			target, err, handoffCodeLife, target)
	}
	var lr LoginResponse
	if err := json.Unmarshal(raw, &lr); err != nil {
		return nil, fmt.Errorf("parse handoff response from %s: %w", target, err)
	}
	if lr.Token == "" {
		return nil, fmt.Errorf("the handoff at %s carried no token - sign in there directly: filex client login --url %s", target, target)
	}
	lr.URL, lr.HandedOff, lr.Raw = target, true, raw
	return &lr, nil
}

// handoffTarget checks the address a handoff names and returns it without a
// trailing slash (it carries the server's base path, as the web app's does).
func handoffTarget(base, origin string) (string, error) {
	origin = strings.TrimSpace(origin)
	u, err := url.Parse(origin)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return "", fmt.Errorf("the server handed this sign-in to an address the CLI does not use (%q) - sign in at the tenant's own address with --url", origin)
	}
	if b, err := url.Parse(base); err == nil && strings.EqualFold(b.Scheme, "https") && u.Scheme == "http" {
		return "", fmt.Errorf("the server handed this sign-in from an encrypted address to an unencrypted one (%s); the CLI does not send the one-use code there - sign in at the tenant's https address with --url, and ask the operator to check the tenant's address and FILEX_PUBLIC_URL", origin)
	}
	return strings.TrimRight(origin, "/"), nil
}

// IsTOTPRequired reports whether err is the server asking for (or refusing)
// the second factor: an answer whose body carries totp_required.
func IsTOTPRequired(err error) bool {
	var ae *APIError
	if !errors.As(err, &ae) {
		return false
	}
	var extra struct {
		TotpRequired bool `json:"totp_required"`
	}
	_ = json.Unmarshal(ae.Body, &extra)
	return extra.TotpRequired
}
