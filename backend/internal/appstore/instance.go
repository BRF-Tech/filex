package appstore

// A connected store (#162, the store side is fapps #163): this filex holds an
// ed25519 key for one trusted store and asks it, in its own name, for a FRESH
// install intent when an administrator approves a person's request from the
// embedded store screen. The intent is then read and reviewed exactly like a
// magic link's (ReadIntent, the install review): the connection adds a way to
// ask for a link, never a way around the review.
//
//	the store's "My instances" > Connect  ->  a one-time code (fxc_...)
//	Apps > Trusted stores > Connect       ->  Connect: a new key, the code, this
//	                                          filex's own address and id sent to
//	                                          POST <store>/v1/instances/connect;
//	                                          the answer signed with the store's
//	                                          index key, naming this address and
//	                                          this key, or nothing is kept
//	an approval                           ->  RequestIntent: a signed
//	                                          POST <store>/v1/instances/{id}/intents
//
// The private key is sealed with FILEX_SECRET_KEY (app_store_state
// `conn:<store>`); nothing of it leaves this filex. Every request is signed
// over its method, path, instance, body, a timestamp and a single-use nonce
// (RequestText; the store's contract, docs/APP-PLUGINS-API.md → The store
// contract, and its test vector in testdata/instance-request-vector.json).

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/brf-tech/filex/backend/internal/secretbox"
)

// The headers a connected filex signs its requests with (the store's
// contract).
const (
	HeaderInstance  = "Fapps-Instance"
	HeaderKey       = "Fapps-Key"
	HeaderTimestamp = "Fapps-Timestamp"
	HeaderNonce     = "Fapps-Nonce"
	HeaderSignature = "Fapps-Signature"
)

// requestScheme is the first line of what a request signs.
const requestScheme = "FAPPS-INSTANCE-REQUEST-1"

// State keys of the connections and of the requests an approval asked a
// link for.
const (
	keyConnPrefix    = "conn:"
	keyReqLinkPrefix = "reqlink:"
)

var (
	connectCodeRe = regexp.MustCompile(`^fxc_[A-Za-z0-9_-]{43}$`)
	instanceUUID  = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
	tokenIDRe     = regexp.MustCompile(`^[0-9a-f]{16}$`)
)

// ValidConnectCode reports whether s has the shape of a store's connection
// code (what a person pastes; nothing else is sent).
func ValidConnectCode(s string) bool { return connectCodeRe.MatchString(s) }

// RequestText is what a connected filex signs for one request: the scheme,
// the method in upper case, the path as sent (no query), the store's instance
// id, the key's fingerprint, the timestamp (Unix seconds), the nonce, and
// the lower-hex sha256 of the body (of no bytes when there is none), one per
// line. ⚠ The store computes the same text (fapps internal/install
// RequestText); testdata/instance-request-vector.json holds both to one
// vector made with Node's crypto.
func RequestText(method, path, instanceID, keyFingerprint, timestamp, nonce string, body []byte) string {
	sum := sha256.Sum256(body)
	return strings.Join([]string{requestScheme, strings.ToUpper(method), path, instanceID, keyFingerprint, timestamp, nonce,
		hex.EncodeToString(sum[:])}, "\n")
}

// SignRequest answers the request's five headers, signed with priv over the
// lower-hex sha256 of RequestText (the one signature rule of a store).
func SignRequest(priv ed25519.PrivateKey, method, path, instanceID string, body []byte, at time.Time) map[string]string {
	pub := priv.Public().(ed25519.PublicKey)
	fpr := Key{Ed25519: hex.EncodeToString(pub)}.Fingerprint()
	nb := make([]byte, 18)
	_, _ = rand.Read(nb)
	nonce := base64.RawURLEncoding.EncodeToString(nb)
	ts := strconv.FormatInt(at.Unix(), 10)
	text := RequestText(method, path, instanceID, fpr, ts, nonce, body)
	sig := ed25519.Sign(priv, []byte(sha256Hex([]byte(text))))
	return map[string]string{
		HeaderInstance:  instanceID,
		HeaderKey:       fpr,
		HeaderTimestamp: ts,
		HeaderNonce:     nonce,
		HeaderSignature: hex.EncodeToString(sig),
	}
}

// connection is what filex keeps of a store it is connected to.
type connection struct {
	Store          string    `json:"store"`
	InstanceID     string    `json:"instance_id"`
	KeyFingerprint string    `json:"key_fingerprint"`
	KeySealed      string    `json:"key_sealed"` // the ed25519 seed, sealed (secretbox, connAAD)
	ConnectedAt    time.Time `json:"connected_at"`
	ConnectedBy    *int64    `json:"connected_by,omitempty"`
	ConnectedName  string    `json:"connected_by_name,omitempty"`
}

// ConnectionView is a connection as the panel shows it: never the key.
type ConnectionView struct {
	Store           string    `json:"store"`
	Connected       bool      `json:"connected"`
	InstanceID      string    `json:"instance_id,omitempty"`
	KeyFingerprint  string    `json:"key_fingerprint,omitempty"`
	ConnectedAt     time.Time `json:"connected_at,omitempty"`
	ConnectedByName string    `json:"connected_by_name,omitempty"`
}

func connAAD(origin string) string { return "app_store_state:" + keyConnPrefix + origin }

func (c *connection) view() *ConnectionView {
	return &ConnectionView{Store: c.Store, Connected: true, InstanceID: c.InstanceID, KeyFingerprint: c.KeyFingerprint,
		ConnectedAt: c.ConnectedAt, ConnectedByName: c.ConnectedName}
}

func (s *Service) connectionOf(ctx context.Context, origin string) (*connection, error) {
	var c connection
	ok, err := s.getJSON(ctx, keyConnPrefix+origin, &c)
	if err != nil || !ok {
		return nil, err
	}
	return &c, nil
}

// Connection answers origin's connection for the panel ("not connected" when
// there is none).
func (s *Service) Connection(ctx context.Context, origin string) (*ConnectionView, error) {
	c, err := s.connectionOf(ctx, origin)
	if err != nil {
		return nil, err
	}
	if c == nil {
		return &ConnectionView{Store: origin}, nil
	}
	return c.view(), nil
}

// Connected reports whether this filex holds a key for origin.
func (s *Service) Connected(ctx context.Context, origin string) bool {
	c, err := s.connectionOf(ctx, origin)
	return err == nil && c != nil
}

// connectPayload is the store's signed answer to a connection.
type connectPayload struct {
	Store          string `json:"store"`
	InstanceID     string `json:"instance_id"`
	FilexOrigin    string `json:"filex_origin"`
	KeyFingerprint string `json:"key_fingerprint"`
	ConnectedAt    string `json:"connected_at"`
}

// storeError reads a store's `{"error", "message"}` answer.
func storeError(b []byte) (code, message string) {
	var e struct {
		Error   string `json:"error"`
		Message string `json:"message"`
	}
	_ = json.Unmarshal(b, &e)
	return e.Error, e.Message
}

// Connect binds a new key of this filex to the store instance a connection
// code was made for. self is this filex's own origin (InstanceOrigin): the
// store must name it in its signed answer, as an install link must. The
// store must be trusted already (its index key checks the answer).
func (s *Service) Connect(ctx context.Context, origin, code, self string, actorID *int64, actorName string) (*ConnectionView, error) {
	code = strings.TrimSpace(code)
	if !ValidConnectCode(code) {
		return nil, errf(CodeConnectCode, "a connection code is fxc_ and 43 characters; copy it again from the store's My instances page")
	}
	if s.opts.Box == nil || !s.opts.Box.Enabled() {
		return nil, &Error{Code: CodeNotConnected, Message: "a store connection's key is kept encrypted, and this installation has no FILEX_SECRET_KEY"}
	}
	if self == "" {
		return nil, errf(CodeWrongInstance, "this filex does not know its own address (FILEX_PUBLIC_URL); a store connection is held to it")
	}
	if _, err := s.trustFor(ctx, origin, true); err != nil {
		return nil, err
	}
	instanceID, err := s.InstanceID(ctx)
	if err != nil {
		return nil, err
	}
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	fpr := Key{Ed25519: hex.EncodeToString(pub)}.Fingerprint()
	body, _ := json.Marshal(map[string]string{"code": code, "public_key": hex.EncodeToString(pub), "filex_origin": self,
		"filex_instance_id": instanceID})
	status, b, err := s.opts.Client.doRaw(ctx, http.MethodPost, origin, "/v1/instances/connect", body, nil, maxAnswerBytes)
	if err != nil {
		return nil, err
	}
	switch status {
	case http.StatusOK:
	case http.StatusNotFound, http.StatusGone:
		_, msg := storeError(b)
		return nil, &Error{Code: CodeConnectCode, Message: "the store did not take this connection code: " + msg}
	case http.StatusConflict:
		sc, msg := storeError(b)
		return nil, &Error{Code: CodeConnectCode, Message: "the store refused the connection: " + msg, Detail: map[string]any{"store_error": sc}}
	default:
		return nil, errf(CodeBadAnswer, "the store answered %d to the connection", status)
	}
	env, err := ParseEnvelope(b)
	if err != nil {
		return nil, errf(CodeBadAnswer, "%v", err)
	}
	if _, err := s.verifyWith(ctx, origin, env, UseIndex, false); err != nil {
		return nil, err
	}
	var p connectPayload
	if err := json.Unmarshal(env.Payload, &p); err != nil {
		return nil, errf(CodeBadAnswer, "the store's signed connection does not read: %v", err)
	}
	store, serr := NormalizeOrigin(p.Store, true)
	target, terr := instanceOrigin(p.FilexOrigin, false)
	switch {
	case serr != nil || store != origin:
		return nil, errf(CodeBadAnswer, "the store signed a connection for %q, not for %s", p.Store, origin)
	case terr != nil || target != self:
		return nil, &Error{Code: CodeWrongInstance, Message: "the store bound this code to " + p.FilexOrigin + ", not to this filex (" + self + ")",
			Detail: map[string]any{"filex_origin": p.FilexOrigin, "this_filex": self}}
	case p.KeyFingerprint != fpr:
		return nil, errf(CodeBadAnswer, "the store bound another key than the one this filex sent")
	case !instanceUUID.MatchString(p.InstanceID):
		return nil, errf(CodeBadAnswer, "the store's instance id %q is not one it hands out", p.InstanceID)
	}
	sealed, err := s.opts.Box.SealFor(base64.StdEncoding.EncodeToString(priv.Seed()), connAAD(origin))
	if err != nil {
		return nil, err
	}
	c := &connection{Store: origin, InstanceID: p.InstanceID, KeyFingerprint: fpr, KeySealed: sealed,
		ConnectedAt: s.opts.Now().UTC(), ConnectedBy: actorID, ConnectedName: actorName}
	prev, _ := s.connectionOf(ctx, origin)
	if err := s.putJSON(ctx, keyConnPrefix+origin, c); err != nil {
		return nil, err
	}
	meta := map[string]any{"store": origin, "instance_id": p.InstanceID, "key_fingerprint": fpr}
	if prev != nil {
		meta["previous_key_fingerprint"] = prev.KeyFingerprint
	}
	s.audit(ctx, actorID, "app_store.connect", "app_store", origin, meta)
	return c.view(), nil
}

// privateKey opens a connection's key.
func (s *Service) privateKey(c *connection) (ed25519.PrivateKey, error) {
	if s.opts.Box == nil || !s.opts.Box.Enabled() || !secretbox.IsSealed(c.KeySealed) {
		return nil, errf(CodeNotConnected, "this filex's key for %s does not open (FILEX_SECRET_KEY changed?); connect it again", c.Store)
	}
	raw, err := s.opts.Box.OpenFor(c.KeySealed, connAAD(c.Store))
	if err != nil {
		return nil, errf(CodeNotConnected, "this filex's key for %s does not open (FILEX_SECRET_KEY changed?); connect it again", c.Store)
	}
	seed, err := base64.StdEncoding.DecodeString(raw)
	if err != nil || len(seed) != ed25519.SeedSize {
		return nil, errf(CodeNotConnected, "this filex's key for %s does not open; connect it again", c.Store)
	}
	return ed25519.NewKeyFromSeed(seed), nil
}

// signed sends one signed request to a connected store.
func (s *Service) signed(ctx context.Context, origin, method, path string, body any) (int, []byte, error) {
	c, err := s.connectionOf(ctx, origin)
	if err != nil {
		return 0, nil, err
	}
	if c == nil {
		return 0, nil, errf(CodeNotConnected, "this filex is not connected to %s: an administrator connects it (Apps, Trusted stores, Connect) with a code from the store", origin)
	}
	priv, err := s.privateKey(c)
	if err != nil {
		return 0, nil, err
	}
	var raw []byte
	if body != nil {
		if raw, err = json.Marshal(body); err != nil {
			return 0, nil, err
		}
	}
	full := strings.ReplaceAll(path, "{id}", url.PathEscape(c.InstanceID))
	hdr := SignRequest(priv, method, full, c.InstanceID, raw, time.Now())
	return s.opts.Client.doRaw(ctx, method, origin, full, raw, hdr, maxAnswerBytes)
}

// Disconnect forgets this filex's key for origin and tells the store to
// forget it too (best effort: a store that cannot be told keeps a key nobody
// holds any more).
func (s *Service) Disconnect(ctx context.Context, origin string, actorID *int64) (bool, error) {
	c, err := s.connectionOf(ctx, origin)
	if err != nil || c == nil {
		return false, err
	}
	if status, _, err := s.signed(ctx, origin, http.MethodDelete, "/v1/instances/{id}/connection", nil); err != nil || (status != http.StatusNoContent && status != http.StatusUnauthorized) {
		s.log.Warn("app-store: the store was not told to forget this filex's key", "store", origin, "status", status, "err", err)
	}
	ok, err := s.opts.Store.DeleteAppStoreState(ctx, keyConnPrefix+origin)
	if err != nil {
		return false, err
	}
	if ok {
		s.audit(ctx, actorID, "app_store.disconnect", "app_store", origin, map[string]any{"store": origin, "instance_id": c.InstanceID})
	}
	return ok, nil
}

// RequestedIntent is a fresh install link a connected store made for this
// filex: read it with ReadIntent like any other.
type RequestedIntent struct {
	Store     string    `json:"store"`
	Token     string    `json:"token"`
	TokenID   string    `json:"token_id"`
	App       string    `json:"app"`
	Version   string    `json:"version"`
	ExpiresAt time.Time `json:"expires_at"`
}

// RequestIntent asks a connected store for a fresh install link for app
// (version "" = the store's newest; licenseKey "" = none: the administrator
// may still give one at the install). The link is for this filex's address
// (the store holds it to the address the connection was made for). actorID
// is the approving administrator (the audit row).
func (s *Service) RequestIntent(ctx context.Context, origin, app, version, licenseKey string, actorID *int64) (*RequestedIntent, error) {
	if _, err := s.trustFor(ctx, origin, false); err != nil {
		return nil, err
	}
	body := map[string]string{"app": app}
	if v := strings.TrimSpace(version); v != "" {
		body["version"] = v
	}
	if k := strings.TrimSpace(licenseKey); k != "" {
		body["license_key"] = k
	}
	status, b, err := s.signed(ctx, origin, http.MethodPost, "/v1/instances/{id}/intents", body)
	if err != nil {
		return nil, err
	}
	switch {
	case status == http.StatusCreated:
	case status == http.StatusUnauthorized:
		sc, msg := storeError(b)
		return nil, &Error{Code: CodeConnectRefused, Message: "the store refused this filex's signed request (" + sc + "): " + msg +
			". If the store disconnected this filex, connect it again with a new code.", Detail: map[string]any{"store_error": sc}}
	case status == http.StatusTooManyRequests:
		return nil, errf(CodeStoreRefusal, "the store asks this filex to slow down; try again later")
	case status >= 400 && status < 500:
		sc, msg := storeError(b)
		return nil, &Error{Code: CodeStoreRefusal, Message: "the store refused the install link: " + msg, Detail: map[string]any{"store_error": sc}}
	default:
		return nil, errf(CodeBadAnswer, "the store answered %d for an install link", status)
	}
	var out struct {
		Token     string `json:"token"`
		TokenID   string `json:"token_id"`
		ExpiresAt string `json:"expires_at"`
		App       string `json:"app"`
		Version   string `json:"version"`
	}
	if err := json.Unmarshal(b, &out); err != nil || !ValidToken(out.Token) || !tokenIDRe.MatchString(out.TokenID) || out.App != app {
		return nil, errf(CodeBadAnswer, "the store's install link does not read")
	}
	exp, err := time.Parse(time.RFC3339, out.ExpiresAt)
	if err != nil {
		return nil, errf(CodeBadAnswer, "the store's install link has no expiry")
	}
	s.audit(ctx, actorID, "app_store.intent_request", "app_plugin", app, map[string]any{
		"store": origin, "app": app, "version": out.Version, "token_id": out.TokenID, "license_key": licenseKey != "",
	})
	return &RequestedIntent{Store: origin, Token: out.Token, TokenID: out.TokenID, App: out.App, Version: out.Version, ExpiresAt: exp}, nil
}

// reqLink is the plugin request an install link was asked for.
type reqLink struct {
	RequestID int64     `json:"request_id"`
	Expires   time.Time `json:"expires"`
}

// RememberRequest records that the link token_id of origin was asked for the
// plugin request id: the review that reads it carries the request, and the
// install that ends it closes the request (handlers/app_store_intent.go).
func (s *Service) RememberRequest(ctx context.Context, origin, tokenID string, requestID int64, expires time.Time) error {
	return s.putJSON(ctx, keyReqLinkPrefix+origin+"/"+tokenID, reqLink{RequestID: requestID, Expires: expires})
}

// RequestFor answers the plugin request a link was asked for (0: none).
func (s *Service) RequestFor(ctx context.Context, origin, tokenID string) int64 {
	var l reqLink
	if ok, err := s.getJSON(ctx, keyReqLinkPrefix+origin+"/"+tokenID, &l); err != nil || !ok {
		return 0
	}
	return l.RequestID
}

// pruneReqLinks drops the records of links long expired.
func (s *Service) pruneReqLinks(ctx context.Context, now time.Time) {
	rows, err := s.opts.Store.ListAppStoreState(ctx, keyReqLinkPrefix)
	if err != nil {
		return
	}
	for k, raw := range rows {
		var l reqLink
		if unmarshal(raw, &l) == nil && !l.Expires.IsZero() && now.Sub(l.Expires) > 30*24*time.Hour {
			_, _ = s.opts.Store.DeleteAppStoreState(ctx, k)
		}
	}
}
