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

type mcpOAuthGrant struct {
	FamilyID string
	ClientID string
	UserID   int64
	TokenID  int64
	Scope    string
	Resource string
	Label    string
	Expires  int64
}

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

func (h *MCPOAuth) tokenClient(r *http.Request) (*mcpOAuthClientRecord, error) {
	id, secret, basic := r.BasicAuth()
	method := "client_secret_basic"
	if basic {
		if r.PostForm.Get("client_id") != "" || r.PostForm.Get("client_secret") != "" {
			return nil, errors.New("Multiple client credentials")
		}
		var err error
		id, err = url.QueryUnescape(id)
		if err != nil {
			return nil, err
		}
		secret, err = url.QueryUnescape(secret)
		if err != nil {
			return nil, err
		}
	} else {
		if r.Header.Get("Authorization") != "" {
			return nil, errors.New("Unsupported client authentication")
		}
		id = r.PostForm.Get("client_id")
		secret = r.PostForm.Get("client_secret")
		method = "client_secret_post"
	}
	c, err := h.client(r.Context(), id)
	if err != nil {
		return nil, err
	}
	if c.Client.Method == "none" {
		if basic || secret != "" {
			return nil, errors.New("Public client must not send a secret")
		}
	} else if c.Client.Method != method || secret == "" || subtle.ConstantTimeCompare([]byte(c.SecretHash), []byte(apitoken.HashToken(secret))) != 1 {
		return nil, errors.New("Invalid client secret")
	}
	return c, nil
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

func (h *MCPOAuth) Token(w http.ResponseWriter, r *http.Request) {
	if !h.ready(w) {
		return
	}
	if !oauthParseForm(w, r) {
		return
	}
	c, err := h.tokenClient(r)
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
			var req mcpOAuthRequest
			code := r.PostForm.Get("code")
			if !oauthNonce(code) || !oauthVerifierValid(r.PostForm.Get("code_verifier")) {
				return errors.New("invalid grant")
			}
			if err := h.read(ctx, "code", apitoken.HashToken(code), &req, false); err != nil {
				return err
			}
			sum := sha256.Sum256([]byte(r.PostForm.Get("code_verifier")))
			challenge := base64.RawURLEncoding.EncodeToString(sum[:])
			if req.ClientID != c.Client.ID || req.RedirectURI != r.PostForm.Get("redirect_uri") || subtle.ConstantTimeCompare([]byte(challenge), []byte(req.Challenge)) != 1 {
				return errors.New("invalid grant")
			}
			if v := r.PostForm.Get("resource"); v != "" && v != req.Resource {
				return errors.New("invalid resource")
			}
			if err := h.read(ctx, "code", apitoken.HashToken(code), &req, true); err != nil {
				return err
			}
			grant = mcpOAuthGrant{ClientID: req.ClientID, UserID: req.UserID, Scope: req.Scope, Resource: req.Resource, Label: "MCP OAuth: " + req.ClientName, Expires: time.Now().Add(30 * 24 * time.Hour).Unix()}
			var err error
			grant.FamilyID, err = randHex(32)
			if err != nil {
				return err
			}
		} else {
			refresh := r.PostForm.Get("refresh_token")
			if !oauthNonce(refresh) {
				return errors.New("invalid refresh")
			}
			if err := h.read(ctx, "refresh", apitoken.HashToken(refresh), &grant, true); err != nil {
				// A used refresh credential proves this authorization may have
				// been stolen. Revoke its active successor and COMMIT that
				// revocation before answering invalid_grant (RFC 9700).
				if h.read(ctx, "spent_refresh", apitoken.HashToken(refresh), &grant, false) == nil && grant.ClientID == c.Client.ID {
					if v := r.PostForm.Get("resource"); v != "" && v != grant.Resource {
						return errors.New("invalid resource")
					}
					replayed = true
					return h.revokeFamily(ctx, grant.FamilyID)
				}
				return err
			}
			if grant.ClientID != c.Client.ID || grant.Resource != h.resource || grant.Expires <= time.Now().Unix() {
				return errors.New("invalid refresh")
			}
			if v := r.PostForm.Get("resource"); v != "" && v != grant.Resource {
				return errors.New("invalid resource")
			}
			if v := r.PostForm.Get("scope"); v != "" && v != strings.ReplaceAll(grant.Scope, ",", " ") {
				return errors.New("Refresh cannot change granted scopes")
			}
			var active mcpOAuthGrant
			if err := h.read(ctx, "grant", grant.FamilyID, &active, true); err != nil {
				return err
			}
			if active.TokenID != grant.TokenID || active.UserID != grant.UserID || active.ClientID != grant.ClientID {
				return errors.New("invalid grant family")
			}
			tok, err := h.store.GetAPITokenByID(ctx, grant.TokenID)
			if err != nil || tok == nil || tok.UserID != grant.UserID || tok.Source != model.TokenSourceMCPOAuth {
				return errors.New("revoked grant")
			}
			// An edited key may have narrower permission levels. Refresh must never
			// restore levels its owner removed in API / MCP.
			grant.Scope = tok.Scopes
			if err := h.save(ctx, "spent_refresh", apitoken.HashToken(refresh), grant, time.Until(time.Unix(grant.Expires, 0))); err != nil {
				return err
			}
			if err := h.store.DeleteAPIToken(ctx, grant.TokenID); err != nil {
				return err
			}
		}
		if grant.Resource != h.resource {
			return errors.New("invalid resource")
		}
		u, err := h.store.GetUser(ctx, grant.UserID)
		if err != nil || u == nil || auth.AccountBlock(ctx, h.store, u) != "" {
			return errors.New("account unavailable")
		}
		userctx := auth.WithUser(ctx, u)
		if !callerCan(userctx, h.acl, perm.AccessAPI) || !callerCan(userctx, h.acl, perm.AIUse) || auth.TwoFactorPending(userctx, h.store, u) {
			return errors.New("MCP permission denied")
		}
		scope, err := h.scopes.cappedScopes(userctx, u, grant.Scope)
		if err != nil {
			return err
		}
		grant.Scope = scope
		plain, err := randHex(32)
		if err != nil {
			return err
		}
		plain = "fxo_" + plain
		expires := time.Now().Add(time.Hour)
		if deadline := time.Unix(grant.Expires, 0); deadline.Before(expires) {
			expires = deadline
		}
		tok, err := h.store.CreateAPIToken(ctx, &model.APIToken{UserID: u.ID, Label: grant.Label, TokenHash: auth.HashMCPOAuthToken(plain), Scopes: grant.Scope, Kind: model.TokenKindApp, Source: model.TokenSourceMCPOAuth, ExpiresAt: &expires})
		if err != nil {
			return err
		}
		grant.TokenID = tok.ID
		if err := h.save(ctx, "grant", grant.FamilyID, grant, time.Until(time.Unix(grant.Expires, 0))); err != nil {
			return err
		}
		refresh, err := randHex(32)
		if err != nil {
			return err
		}
		if err = h.save(ctx, "refresh", apitoken.HashToken(refresh), grant, time.Until(time.Unix(grant.Expires, 0))); err != nil {
			return err
		}
		// Round the fresh lifetime to the advertised 3600 seconds. For a grant
		// near its absolute end, never advertise beyond that end.
		lifetime := int64(3600)
		if remaining := grant.Expires - time.Now().Unix(); remaining < lifetime {
			lifetime = remaining
		}
		out = map[string]any{"access_token": plain, "token_type": "Bearer", "expires_in": lifetime, "refresh_token": refresh, "scope": strings.ReplaceAll(grant.Scope, ",", " ")}
		return nil
	})
	if err != nil || replayed {
		oauthError(w, 400, "invalid_grant", "Authorization is invalid, expired, revoked, or does not match this client and resource")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Pragma", "no-cache")
	writeJSON(w, 200, out)
}

func (h *MCPOAuth) Revoke(w http.ResponseWriter, r *http.Request) {
	if !h.ready(w) {
		return
	}
	if !oauthParseForm(w, r) {
		return
	}
	c, err := h.tokenClient(r)
	if err != nil {
		oauthError(w, 401, "invalid_client", "Client authentication failed")
		return
	}
	raw := r.PostForm.Get("token")
	err = h.store.WithTx(r.Context(), func(ctx context.Context) error {
		var g mcpOAuthGrant
		if h.read(ctx, "refresh", apitoken.HashToken(raw), &g, false) == nil && g.ClientID == c.Client.ID {
			if err := h.read(ctx, "refresh", apitoken.HashToken(raw), &g, true); err != nil {
				return err
			}
			return h.revokeFamily(ctx, g.FamilyID)
		}
		// Access-token revocation requires proof that this client owns the grant;
		// a public client's id alone is not proof. Its refresh token is the handle
		// supported here; unknown tokens deliberately produce the same 200.
		return nil
	})
	if err != nil {
		oauthError(w, 503, "temporarily_unavailable", "Revocation is unavailable")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(200)
}

func (h *MCPOAuth) revokeFamily(ctx context.Context, id string) error {
	var active mcpOAuthGrant
	if err := h.read(ctx, "grant", id, &active, true); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		return err
	}
	return h.store.DeleteAPIToken(ctx, active.TokenID)
}
