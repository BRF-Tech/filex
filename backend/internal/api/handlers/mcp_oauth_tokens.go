package handlers

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/brf-tech/filex/backend/internal/auth"
	"github.com/brf-tech/filex/backend/internal/auth/drivers/apitoken"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/perm"
)

// mcpOAuthGrant is one authorization: a refresh-token family. It is stored
// once, under the hash of its family id (oauthKindGrant), and replaced on
// every refresh - so a grant costs one record however long it lives.
type mcpOAuthGrant struct {
	FamilyID string
	ClientID string
	UserID   int64
	TokenID  int64
	Scope    string
	Resource string
	Label    string
	Expires  int64
	// The site the grant belongs to (mcpOAuthRequest.Issuer, .TenantID).
	Issuer   string
	TenantID int64
	// RefreshHash is the hash of the one refresh token that may be used
	// next. Any other secret presented with this family's id is an old one
	// coming back - the family is revoked (RFC 9700 section 4.14.2).
	RefreshHash string
}

// mcpOAuthSpentCode remembers which grant a used code made.
type mcpOAuthSpentCode struct {
	FamilyID string
	ClientID string
}

// A refresh token is the grant's family id followed by a secret, 64 hex
// characters each. The family id finds the grant; only the secret's hash is
// stored.
func splitRefreshToken(raw string) (family, secret string, ok bool) {
	if len(raw) != 128 || !oauthNonce(raw[:64]) || !oauthNonce(raw[64:]) {
		return "", "", false
	}
	return raw[:64], raw[64:], true
}

func grantKey(family string) string { return apitoken.HashToken(family) }

func oauthChallengeValid(raw string) bool {
	b, err := base64.RawURLEncoding.DecodeString(raw)
	return err == nil && len(b) == 32 && len(raw) == 43
}

func oauthVerifierValid(raw string) bool {
	if len(raw) < 43 || len(raw) > 128 {
		return false
	}
	for _, c := range raw {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.ContainsRune("-._~", c)) {
			return false
		}
	}
	return true
}

func (h *MCPOAuth) tokenClient(r *http.Request, s oauthSite) (*mcpOAuthClientRecord, string, error) {
	id, secret, basic := r.BasicAuth()
	method := "client_secret_basic"
	if basic {
		if r.PostForm.Get("client_id") != "" || r.PostForm.Get("client_secret") != "" {
			return nil, "", errors.New("Multiple client credentials")
		}
		var err error
		id, err = url.QueryUnescape(id)
		if err != nil {
			return nil, "", err
		}
		secret, err = url.QueryUnescape(secret)
		if err != nil {
			return nil, "", err
		}
	} else {
		if r.Header.Get("Authorization") != "" {
			return nil, "", errors.New("Unsupported client authentication")
		}
		id = r.PostForm.Get("client_id")
		secret = r.PostForm.Get("client_secret")
		method = "client_secret_post"
	}
	c, kind, err := h.client(r.Context(), s, id)
	if err != nil {
		return nil, "", err
	}
	if c.Client.Method == "none" {
		if basic || secret != "" {
			return nil, "", errors.New("Public client must not send a secret")
		}
	} else if c.Client.Method != method || secret == "" || subtle.ConstantTimeCompare([]byte(c.SecretHash), []byte(apitoken.HashToken(secret))) != 1 {
		return nil, "", errors.New("Invalid client secret")
	}
	return c, kind, nil
}

func oauthParseForm(w http.ResponseWriter, r *http.Request) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 8192)
	if !strings.HasPrefix(r.Header.Get("Content-Type"), "application/x-www-form-urlencoded") || r.ParseForm() != nil {
		oauthError(w, 400, "invalid_request", "Use an application/x-www-form-urlencoded request body")
		return false
	}
	if r.URL.RawQuery != "" {
		oauthError(w, 400, "invalid_request", "Token parameters belong in the POST body")
		return false
	}
	for _, v := range r.PostForm {
		if len(v) != 1 {
			oauthError(w, 400, "invalid_request", "Duplicate token parameter")
			return false
		}
	}
	return true
}

// errOAuthRevoked ends a token request whose transaction must COMMIT (a
// replayed code or refresh token revoked its grant) and still answer
// invalid_grant.
var errOAuthRevoked = errors.New("grant revoked on replay")

func (h *MCPOAuth) Token(w http.ResponseWriter, r *http.Request) {
	s, ok := h.ready(w, r)
	if !ok {
		return
	}
	if !oauthParseForm(w, r) {
		return
	}
	c, clientKind, err := h.tokenClient(r, s)
	if err != nil {
		w.Header().Set("WWW-Authenticate", `Basic realm="filex OAuth"`)
		oauthError(w, 401, "invalid_client", "Client authentication failed")
		return
	}
	grantType := r.PostForm.Get("grant_type")
	if grantType != "authorization_code" && grantType != "refresh_token" {
		oauthError(w, 400, "unsupported_grant_type", "Use authorization_code or refresh_token")
		return
	}
	var out map[string]any
	replayed := false
	err = h.store.WithTx(r.Context(), func(ctx context.Context) error {
		var grant mcpOAuthGrant
		if grantType == "authorization_code" {
			var err error
			grant, err = h.exchangeCode(ctx, r, s, c, clientKind)
			if errors.Is(err, errOAuthRevoked) {
				replayed = true
				return nil
			}
			if err != nil {
				return err
			}
		} else {
			var err error
			grant, err = h.rotateRefresh(ctx, r, s, c)
			if errors.Is(err, errOAuthRevoked) {
				replayed = true
				return nil
			}
			if err != nil {
				return err
			}
		}
		out, err = h.issue(ctx, grant)
		return err
	})
	if err != nil || replayed {
		oauthError(w, 400, "invalid_grant", "Authorization is invalid, expired, revoked, or does not match this client and resource")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Pragma", "no-cache")
	writeJSON(w, 200, out)
}

// exchangeCode redeems an authorization code: it must be live, this
// client's, for this callback and this site, and the PKCE verifier must
// match. A code that comes back after it was used revokes the grant it made.
func (h *MCPOAuth) exchangeCode(ctx context.Context, r *http.Request, s oauthSite, c *mcpOAuthClientRecord, clientKind string) (mcpOAuthGrant, error) {
	var req mcpOAuthRequest
	code := r.PostForm.Get("code")
	if !oauthNonce(code) || !oauthVerifierValid(r.PostForm.Get("code_verifier")) {
		return mcpOAuthGrant{}, errors.New("invalid grant")
	}
	key := apitoken.HashToken(code)
	if err := h.read(ctx, oauthKindCode, key, &req, false); err != nil {
		var spent mcpOAuthSpentCode
		if errors.Is(err, sql.ErrNoRows) && h.read(ctx, oauthKindSpentCode, key, &spent, true) == nil && spent.ClientID == c.Client.ID {
			if err := h.revokeFamily(ctx, spent.FamilyID); err != nil {
				return mcpOAuthGrant{}, err
			}
			return mcpOAuthGrant{}, errOAuthRevoked
		}
		return mcpOAuthGrant{}, err
	}
	sum := sha256.Sum256([]byte(r.PostForm.Get("code_verifier")))
	challenge := base64.RawURLEncoding.EncodeToString(sum[:])
	if req.ClientID != c.Client.ID || req.RedirectURI != r.PostForm.Get("redirect_uri") || subtle.ConstantTimeCompare([]byte(challenge), []byte(req.Challenge)) != 1 {
		return mcpOAuthGrant{}, errors.New("invalid grant")
	}
	if req.Issuer != s.Issuer || req.Resource != s.Resource {
		return mcpOAuthGrant{}, errors.New("code of another address")
	}
	if v := r.PostForm.Get("resource"); v != "" && v != req.Resource {
		return mcpOAuthGrant{}, errors.New("invalid resource")
	}
	if err := h.read(ctx, oauthKindCode, key, &req, true); err != nil {
		return mcpOAuthGrant{}, err
	}
	family, err := randHex(32)
	if err != nil {
		return mcpOAuthGrant{}, err
	}
	if err := h.save(ctx, oauthKindSpentCode, key, mcpOAuthSpentCode{FamilyID: family, ClientID: c.Client.ID}, oauthSpentCodeLife); err != nil {
		return mcpOAuthGrant{}, err
	}
	if err := h.confirmClient(ctx, c, clientKind); err != nil {
		return mcpOAuthGrant{}, err
	}
	label := "MCP OAuth"
	if req.ClientName != "" {
		label += ": " + req.ClientName
	}
	return mcpOAuthGrant{FamilyID: family, ClientID: req.ClientID, UserID: req.UserID, Scope: req.Scope, Resource: req.Resource,
		Label: label, Expires: time.Now().Add(oauthGrantLife).Unix(), Issuer: req.Issuer, TenantID: req.TenantID}, nil
}

// rotateRefresh spends a refresh token. The grant is taken (and stored again
// by issue with the next token's hash): a secret that is not the current one
// is an old token coming back, and the whole grant is revoked - committed
// before invalid_grant is answered.
func (h *MCPOAuth) rotateRefresh(ctx context.Context, r *http.Request, s oauthSite, c *mcpOAuthClientRecord) (mcpOAuthGrant, error) {
	var grant mcpOAuthGrant
	family, secret, ok := splitRefreshToken(r.PostForm.Get("refresh_token"))
	if !ok {
		return grant, errors.New("invalid refresh")
	}
	if err := h.read(ctx, oauthKindGrant, grantKey(family), &grant, false); err != nil {
		return grant, err
	}
	if grant.ClientID != c.Client.ID || grant.Issuer != s.Issuer || grant.Resource != s.Resource {
		return grant, errors.New("refresh of another client or address")
	}
	if subtle.ConstantTimeCompare([]byte(grant.RefreshHash), []byte(apitoken.HashToken(secret))) != 1 {
		if err := h.revokeFamily(ctx, family); err != nil {
			return grant, err
		}
		return grant, errOAuthRevoked
	}
	if grant.Expires <= time.Now().Unix() {
		return grant, errors.New("grant ended")
	}
	if v := r.PostForm.Get("resource"); v != "" && v != grant.Resource {
		return grant, errors.New("invalid resource")
	}
	if v := r.PostForm.Get("scope"); v != "" && v != strings.ReplaceAll(grant.Scope, ",", " ") {
		return grant, errors.New("Refresh cannot change granted scopes")
	}
	if err := h.read(ctx, oauthKindGrant, grantKey(family), &grant, true); err != nil {
		return grant, err
	}
	tok, err := h.store.GetAPITokenByID(ctx, grant.TokenID)
	if err != nil || tok == nil || tok.UserID != grant.UserID || tok.Source != model.TokenSourceMCPOAuth {
		return grant, errors.New("revoked grant")
	}
	// An edited key may have narrower permission levels. Refresh must never
	// restore levels its owner removed in API / MCP.
	grant.Scope = tok.Scopes
	if err := h.store.DeleteAPIToken(ctx, grant.TokenID); err != nil {
		return grant, err
	}
	return grant, nil
}

// issue mints the access token and the next refresh token of grant, after
// asking again everything consent asked: the account is on, its tenant is
// not suspended, it may use API keys and the AI surface, it has its second
// factor if a rule wants one, it still belongs where the grant was made, and
// the scopes are within its reach.
func (h *MCPOAuth) issue(ctx context.Context, grant mcpOAuthGrant) (map[string]any, error) {
	u, err := h.store.GetUser(ctx, grant.UserID)
	if err != nil || u == nil || auth.AccountBlock(ctx, h.store, u) != "" {
		return nil, errors.New("account unavailable")
	}
	userctx := auth.WithUser(ctx, u)
	if !callerCan(userctx, h.acl, perm.AccessAPI) || !callerCan(userctx, h.acl, perm.AIUse) || auth.TwoFactorPending(userctx, h.store, u) {
		return nil, errors.New("MCP permission denied")
	}
	if !h.admits(ctx, grant.TenantID, u) {
		return nil, errors.New("account of another tenant")
	}
	scope, err := h.scopes.cappedScopes(userctx, u, grant.Scope)
	if err != nil {
		return nil, err
	}
	grant.Scope = scope
	plain, err := randHex(32)
	if err != nil {
		return nil, err
	}
	plain = auth.MCPOAuthTokenPrefix + plain
	expires := time.Now().Add(oauthAccessLife)
	if deadline := time.Unix(grant.Expires, 0); deadline.Before(expires) {
		expires = deadline
	}
	tok, err := h.store.CreateAPIToken(ctx, &model.APIToken{UserID: u.ID, Label: grant.Label, TokenHash: auth.HashMCPOAuthToken(grant.Resource, plain), Scopes: grant.Scope, Kind: model.TokenKindApp, Source: model.TokenSourceMCPOAuth, ExpiresAt: &expires})
	if err != nil {
		return nil, err
	}
	grant.TokenID = tok.ID
	secret, err := randHex(32)
	if err != nil {
		return nil, err
	}
	grant.RefreshHash = apitoken.HashToken(secret)
	if err := h.save(ctx, oauthKindGrant, grantKey(grant.FamilyID), grant, time.Until(time.Unix(grant.Expires, 0))); err != nil {
		return nil, err
	}
	// Round the fresh lifetime to the advertised 3600 seconds. For a grant
	// near its absolute end, never advertise beyond that end.
	lifetime := int64(oauthAccessLife / time.Second)
	if remaining := grant.Expires - time.Now().Unix(); remaining < lifetime {
		lifetime = remaining
	}
	return map[string]any{"access_token": plain, "token_type": "Bearer", "expires_in": lifetime, "refresh_token": grant.FamilyID + secret, "scope": strings.ReplaceAll(grant.Scope, ",", " ")}, nil
}

// Revoke ends a grant by its refresh token or by its access token (RFC
// 7009). A refresh token is honoured from the client it was issued to, and
// only the current one: an old one is already spent. An access token is
// proof enough of itself - whoever holds it could use it - and removing its
// key ends the grant at its next refresh, as removing it on the API keys
// page does (section 2.1 lets the server revoke the refresh token with it).
// Unknown tokens get the same 200.
func (h *MCPOAuth) Revoke(w http.ResponseWriter, r *http.Request) {
	s, ok := h.ready(w, r)
	if !ok {
		return
	}
	if !oauthParseForm(w, r) {
		return
	}
	c, _, err := h.tokenClient(r, s)
	if err != nil {
		w.Header().Set("WWW-Authenticate", `Basic realm="filex OAuth"`)
		oauthError(w, 401, "invalid_client", "Client authentication failed")
		return
	}
	raw := r.PostForm.Get("token")
	err = h.store.WithTx(r.Context(), func(ctx context.Context) error {
		if family, secret, ok := splitRefreshToken(raw); ok {
			var g mcpOAuthGrant
			if h.read(ctx, oauthKindGrant, grantKey(family), &g, false) == nil && g.ClientID == c.Client.ID && g.Issuer == s.Issuer &&
				subtle.ConstantTimeCompare([]byte(g.RefreshHash), []byte(apitoken.HashToken(secret))) == 1 {
				return h.revokeFamily(ctx, family)
			}
			return nil
		}
		if strings.HasPrefix(raw, auth.MCPOAuthTokenPrefix) {
			tok, err := h.store.GetAPITokenByHash(ctx, auth.HashMCPOAuthToken(s.Resource, raw))
			if err == nil && tok != nil && tok.Source == model.TokenSourceMCPOAuth {
				return h.store.DeleteAPIToken(ctx, tok.ID)
			}
		}
		return nil
	})
	if err != nil {
		oauthError(w, 503, "temporarily_unavailable", "Revocation is unavailable")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(200)
}

// revokeFamily ends a grant: its record and the access token it holds.
func (h *MCPOAuth) revokeFamily(ctx context.Context, family string) error {
	var active mcpOAuthGrant
	if err := h.read(ctx, oauthKindGrant, grantKey(family), &active, true); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		return err
	}
	return h.store.DeleteAPIToken(ctx, active.TokenID)
}
