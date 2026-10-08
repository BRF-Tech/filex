package onlyoffice

// What a save callback is trusted for (filex 0.54).
//
// The callback route is public: the document server posts it, not a signed-in
// browser. Everything the callback acts on therefore comes from what the
// document server SIGNED, and from nothing else:
//
//   - The fields - key, status, url, filetype, users, changesurl, history - are
//     read from the verified token's payload alone (callbackFromToken). The
//     body is read only to find the token; a field beside it is never used.
//   - The token must be a callback. filex signs its editor configurations with
//     the same secret (the document server checks them with it), and every
//     person who opens a document - in view mode too - is handed one; the
//     document server's own session tokens can share the secret as well. Both
//     are refused by their shape: a callback names a `key` and a `status` and
//     has no `document`, `editorConfig` or `documentType`.
//   - The key must be one filex made for the document the callback names
//     (`?node=`, which is not signed): keyFor seals each key it hands out with
//     the document's id (keySeal), so a key is checked without a lookup, on
//     every instance, after a restart. A key filex recorded for the document
//     in office_sessions also counts: a session opened before 0.54, whose key
//     carries no seal, or one opened before the secret was changed. GetNode is
//     not bound to a tenant, so this is also what keeps a callback inside the
//     tenant whose person was handed the document.
//   - The saved document is downloaded only from the document server's own
//     origin, the address filex is configured with (sameOrigin, as a
//     conversion's result is - convert.go), and a redirect off it is not
//     followed. `changesurl` is never downloaded.
//   - A token that says when it expires (`exp`) or when it starts (`nbf`) is
//     held to it, a few minutes of clock difference allowed (tokenLeeway).
//
// ⚠ `exp` is not required. ONLYOFFICE Docs signs its callbacks with one
// (services.CoAuthoring.token.outbox.expires, five minutes by default), but a
// token without one is not a forgery, and refusing it would lose the edits of
// every save a document server so configured sends. What a token without an
// expiry could still be used for is a replay of the same document server's
// callback for the same document, from its own address: the shape rule, the
// key and the origin are what stop everything else. A token that carries an
// `iat` and no `exp` is held to callbackMaxAge.

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"strings"
	"time"

	"github.com/brf-tech/filex/backend/internal/model"
)

// tokenLeeway is the clock difference allowed between filex and the document
// server when a token's times are checked.
const tokenLeeway = 5 * time.Minute

// callbackMaxAge is how old a callback token that names when it was issued
// (`iat`) but not when it expires may be.
const callbackMaxAge = time.Hour

// The ways a callback token is refused.
var (
	errNotSigned     = errors.New("the callback is not signed")
	errEditorToken   = errors.New("an editor configuration is not a callback")
	errNotACallback  = errors.New("the token is not a document server callback")
	errTokenExpired  = errors.New("the token has expired")
	errTokenNotYet   = errors.New("the token is not valid yet")
	errTokenTooOld   = errors.New("the token is too old")
	errBadTokenTimes = errors.New("the token's times are not numbers")
)

// editorShaped: m has a part of an editor configuration (filex's, or the
// document server's session token, which carries the same parts).
func editorShaped(m map[string]any) bool {
	for _, k := range []string{"document", "editorConfig", "documentType"} {
		if _, ok := m[k]; ok {
			return true
		}
	}
	return false
}

// claimTime reads a NumericDate claim: (0, false, nil) when it is absent.
func claimTime(claims map[string]any, name string) (int64, bool, error) {
	v, ok := claims[name]
	if !ok || v == nil {
		return 0, false, nil
	}
	f, ok := v.(float64)
	if !ok || math.IsNaN(f) || math.IsInf(f, 0) {
		return 0, false, errBadTokenTimes
	}
	return int64(f), true, nil
}

// checkTokenTimes holds a verified token to the times it names (see the
// header for why none of them is required).
func checkTokenTimes(claims map[string]any, now time.Time) error {
	exp, hasExp, err := claimTime(claims, "exp")
	if err != nil {
		return err
	}
	nbf, hasNbf, err := claimTime(claims, "nbf")
	if err != nil {
		return err
	}
	iat, hasIat, err := claimTime(claims, "iat")
	if err != nil {
		return err
	}
	if hasExp && now.After(time.Unix(exp, 0).Add(tokenLeeway)) {
		return errTokenExpired
	}
	if hasNbf && now.Add(tokenLeeway).Before(time.Unix(nbf, 0)) {
		return errTokenNotYet
	}
	if !hasExp && hasIat && now.After(time.Unix(iat, 0).Add(callbackMaxAge+tokenLeeway)) {
		return errTokenTooOld
	}
	return nil
}

// callbackFromToken is the callback a verified token says, and only what it
// says. The document server signs the callback's body itself when the token
// travels in the body (`token`), and wraps it in {"payload": ...} when it
// travels in the Authorization header; both are read.
func callbackFromToken(tok, secret string, now time.Time) (CallbackPayload, error) {
	var p CallbackPayload
	claims, err := verifyHS256(tok, secret)
	if err != nil {
		return p, err
	}
	if err := checkTokenTimes(claims, now); err != nil {
		return p, err
	}
	inner := claims
	if wrapped, ok := claims["payload"].(map[string]any); ok {
		inner = wrapped
	}
	if editorShaped(claims) || editorShaped(inner) {
		return p, errEditorToken
	}
	key, keyOK := inner["key"].(string)
	status, statusOK := inner["status"].(float64)
	if !keyOK || strings.TrimSpace(key) == "" || !statusOK || status != math.Trunc(status) {
		return p, errNotACallback
	}
	// `users` is read apart (claimUsers takes numbers too), everything else
	// through the struct.
	rest := make(map[string]any, len(inner))
	for k, v := range inner {
		if k != "users" && k != "token" {
			rest[k] = v
		}
	}
	raw, err := json.Marshal(rest)
	if err != nil {
		return p, errNotACallback
	}
	if err := json.Unmarshal(raw, &p); err != nil {
		return CallbackPayload{}, fmt.Errorf("%w: %v", errNotACallback, err)
	}
	p.Token = ""
	if users, ok := claimUsers(inner); ok {
		p.Users = users
	}
	return p, nil
}

// callbackToken is where the callback's token is: the body's `token`, else
// the Authorization header's bearer token.
func callbackToken(r *http.Request, body []byte) (string, error) {
	var envelope struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return "", fmt.Errorf("bad json: %w", err)
	}
	if tok := strings.TrimSpace(envelope.Token); tok != "" {
		return tok, nil
	}
	if auth := r.Header.Get("Authorization"); strings.HasPrefix(auth, "Bearer ") {
		if tok := strings.TrimSpace(strings.TrimPrefix(auth, "Bearer ")); tok != "" {
			return tok, nil
		}
	}
	return "", errNotSigned
}

// ── the key of an editing session ───────────────────────────────────────

// keySeal is the part of a document key that binds it to its document: an
// HMAC of the node id and the key's version part under the secret.
func keySeal(nodeID int64, version, secret string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = fmt.Fprintf(mac, "filex-onlyoffice-key|%d|%s", nodeID, version)
	return hex.EncodeToString(mac.Sum(nil))[:32]
}

// sealKey is the document key made of version for the node: `<version>-<seal>`
// (65 characters of [0-9a-f-]; the document server takes 128 of
// [0-9A-Za-z.=_-]). Without a secret there is nothing to seal with, and the
// version alone is the key (OnlyOffice is then off anyway).
func (s *Service) sealKey(ctx context.Context, nodeID int64, version string) string {
	_, secret := s.settings(ctx)
	if secret == "" {
		return version
	}
	return version + "-" + keySeal(nodeID, version, secret)
}

// DocumentKey is the document key an editing session of node gets now
// (keyFor): for whoever plays the document server outside this package, the
// handler tests.
func (s *Service) DocumentKey(ctx context.Context, node *model.Node) string {
	return s.keyFor(ctx, node)
}

// KeyBelongsTo reports whether key is one filex made for node: sealed for it
// under the secret in force, or recorded for it (see the header).
func (s *Service) KeyBelongsTo(ctx context.Context, key string, node *model.Node) bool {
	key = strings.TrimSpace(key)
	if s == nil || key == "" || node == nil {
		return false
	}
	if version, seal, ok := strings.Cut(key, "-"); ok && version != "" {
		if _, secret := s.settings(ctx); secret != "" &&
			hmac.Equal([]byte(keySeal(node.ID, version, secret)), []byte(seal)) {
			return true
		}
	}
	b := s.freshBase(ctx, key)
	return b != nil && b.nodeID == node.ID
}

// ── where the saved document comes from ─────────────────────────────────

// errForeignSave: the callback names a saved document that is not on the
// document server's address.
var errForeignSave = errors.New("the saved document is not on the document server's address")

// savedClient downloads a saved document from the document server at docURL,
// and follows a redirect only to that same origin.
func savedClient(docURL string) *http.Client {
	return &http.Client{
		Timeout: 60 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return errors.New("too many redirects")
			}
			if !sameOrigin(req.URL.String(), docURL) {
				return errForeignSave
			}
			return nil
		},
	}
}
