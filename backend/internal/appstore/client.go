package appstore

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"github.com/brf-tech/filex/backend/internal/netguard"
)

// Error codes the HTTP layer answers with and the panel switches on.
const (
	CodeBadStore         = "store_invalid"            // the store address is not an origin filex talks to
	CodeStoreRefused     = "store_not_allowed"        // FILEX_APP_STORE_URLS is set and does not list it (an allow list: no trust on first use)
	CodeTrustRequired    = "store_trust_required"     // not trusted yet: the administrator is asked
	CodeKeyChanged       = "store_key_changed"        // trusted, but its keys changed: asked again
	CodeKeyNotConfigured = "store_key_not_configured" // a configured store signed with a key FILEX_APP_STORE_KEYS lacks
	CodeUnreachable      = "store_unreachable"        // the store could not be reached
	CodeBadAnswer        = "store_bad_answer"         // the store answered something filex cannot read
	CodeSignature        = "store_signature_invalid"
	CodeIntentUnknown    = "intent_unknown"        // 404
	CodeIntentGone       = "intent_gone"           // 410: used or expired at the store
	CodeIntentExpired    = "intent_expired"        // its expires_at has passed
	CodeIntentUsed       = "intent_used"           // this filex already used it
	CodeIntentInvalid    = "intent_invalid"        // fields missing or not for this store
	CodeWrongInstance    = "intent_wrong_instance" // made for another filex (filex_origin)
	CodePinMismatch      = "intent_pin_mismatch"
	CodeVersionRollback  = "intent_version_rollback" // older than (or the same as) what is installed
	CodeSourceChanged    = "store_source_changed"    // the app installed came from another store or repository: remove it first
	CodeIntentNotFound   = "intent_session_unknown"  // no reviewed intent under that handle
	CodeLicenseKey       = "license_key_invalid"
)

// Error is a refusal with a code; Detail carries what the panel draws (the
// fingerprints of a store to trust, the pins that differ).
type Error struct {
	Code    string         `json:"error"`
	Message string         `json:"message"`
	Detail  map[string]any `json:"detail,omitempty"`
}

func (e *Error) Error() string { return e.Code + ": " + e.Message }

func errf(code, format string, args ...any) *Error {
	return &Error{Code: code, Message: fmt.Sprintf(format, args...)}
}

// AsError unwraps an *Error.
func AsError(err error) (*Error, bool) {
	var e *Error
	ok := errors.As(err, &e)
	return e, ok
}

// Size ceilings for what a store answers: these are small JSON documents.
const (
	maxKeysBytes   = 64 << 10
	maxAnswerBytes = 256 << 10
)

var tokenRe = regexp.MustCompile(`^[A-Za-z0-9._~-]{8,512}$`)

// ValidToken reports whether an install link's token may be put in a path:
// the URL-safe characters a store's random token is made of.
func ValidToken(t string) bool { return tokenRe.MatchString(t) }

// NormalizeOrigin turns what a link or an administrator names into the one
// spelling a store is known by - `https://host[:port]`, lower-case, no path,
// no default port. Plain http is accepted for this machine only and only with
// loopback (FILEX_PLUGIN_LOOPBACK_SOURCES), the rule every plugin download
// follows.
func NormalizeOrigin(raw string, loopback bool) (string, error) {
	raw = strings.TrimSpace(raw)
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return "", errf(CodeBadStore, "%q is not a store address", raw)
	}
	if u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return "", errf(CodeBadStore, "a store is named by its origin alone (scheme and host), not %q", raw)
	}
	host := strings.ToLower(u.Hostname())
	port := u.Port()
	switch strings.ToLower(u.Scheme) {
	case "https":
		if port == "443" {
			port = ""
		}
	case "http":
		ip := net.ParseIP(host)
		isLoop := host == "localhost" || (ip != nil && ip.IsLoopback())
		if !loopback || !isLoop {
			return "", errf(CodeBadStore, "a store is reached over https (plain http only for this machine, in development)")
		}
		if port == "80" {
			port = ""
		}
	default:
		return "", errf(CodeBadStore, "unsupported scheme %q", u.Scheme)
	}
	if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	origin := strings.ToLower(u.Scheme) + "://" + host
	if port != "" {
		origin += ":" + port
	}
	return origin, nil
}

// Client talks to stores: one guarded HTTP client (netguard: public
// addresses only, judged after DNS), small size ceilings, JSON only - and no
// redirect followed (netguard.APIClient): a license check carries the key,
// a completion the instance id, and a redirect must not carry them to another
// host. (The app's own files - manifest, module, interface, from a GitHub
// release that redirects to its asset host - are downloaded by
// wasmplugin's DownloadClient, which follows redirects and sends nothing.)
type Client struct {
	HTTP  *http.Client
	Guard *netguard.Policy
	// UserAgent names this filex.
	UserAgent string
}

// NewClient builds the guarded, non-redirecting client under policy.
func NewClient(policy netguard.Policy, userAgent string) *Client {
	p := policy
	return &Client{HTTP: p.APIClient(httpTimeout), Guard: &p, UserAgent: userAgent}
}

// do sends one request to origin+path and answers the status and the body.
func (c *Client) do(ctx context.Context, method, origin, path string, body any, limit int64) (int, []byte, error) {
	u := origin + path
	parsed, err := url.Parse(u)
	if err != nil {
		return 0, nil, errf(CodeBadStore, "bad store address")
	}
	if c.Guard != nil {
		if ip := net.ParseIP(parsed.Hostname()); ip != nil && c.Guard.Refused(ip) {
			return 0, nil, errf(CodeBadStore, "%s %s; stores are reached at public addresses only", parsed.Hostname(), netguard.ErrPrivateTarget.Error())
		}
	}
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return 0, nil, err
		}
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, u, rdr)
	if err != nil {
		return 0, nil, errf(CodeBadStore, "bad store address")
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.UserAgent != "" {
		req.Header.Set("User-Agent", c.UserAgent)
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		if errors.Is(err, netguard.ErrPrivateTarget) || errors.Is(err, netguard.ErrDowngrade) {
			return 0, nil, errf(CodeBadStore, "%v", err)
		}
		return 0, nil, &Error{Code: CodeUnreachable, Message: "the store could not be reached: " + err.Error()}
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 == 3 {
		return 0, nil, errf(CodeBadAnswer, "the store answered %d, a redirect; a store's API is answered at its own address and filex does not follow it elsewhere", resp.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return 0, nil, &Error{Code: CodeUnreachable, Message: "the store's answer was cut off: " + err.Error()}
	}
	if int64(len(b)) > limit {
		return 0, nil, errf(CodeBadAnswer, "the store's answer is larger than %d bytes", limit)
	}
	return resp.StatusCode, b, nil
}

// Keys reads `<store>/v1/keys.json`, validated.
func (c *Client) Keys(ctx context.Context, origin string) (*KeySet, error) {
	status, b, err := c.do(ctx, http.MethodGet, origin, "/v1/keys.json", nil, maxKeysBytes)
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		return nil, errf(CodeBadAnswer, "the store answered %d for its keys", status)
	}
	var ks KeySet
	if err := json.Unmarshal(b, &ks); err != nil {
		return nil, errf(CodeBadAnswer, "the store's keys.json does not read: %v", err)
	}
	if err := ks.Validate(); err != nil {
		return nil, errf(CodeBadAnswer, "the store's keys.json: %v", err)
	}
	return &ks, nil
}

// Intent reads `GET <store>/v1/install/{token}` as an envelope. 404 and 410
// are said as such.
func (c *Client) Intent(ctx context.Context, origin, token string) (*Envelope, error) {
	if !ValidToken(token) {
		return nil, errf(CodeIntentInvalid, "the link's token is not one a store hands out")
	}
	status, b, err := c.do(ctx, http.MethodGet, origin, "/v1/install/"+url.PathEscape(token), nil, maxAnswerBytes)
	if err != nil {
		return nil, err
	}
	switch status {
	case http.StatusOK:
	case http.StatusNotFound:
		return nil, errf(CodeIntentUnknown, "the store does not know this install link")
	case http.StatusGone:
		return nil, errf(CodeIntentGone, "this install link was used already, or has expired")
	default:
		return nil, errf(CodeBadAnswer, "the store answered %d for the install link", status)
	}
	env, err := ParseEnvelope(b)
	if err != nil {
		return nil, errf(CodeBadAnswer, "%v", err)
	}
	return env, nil
}

// Complete tells the store how an install link ended:
// `POST <store>/v1/install/{token}/complete`.
func (c *Client) Complete(ctx context.Context, origin, token, instanceID, result string) error {
	status, _, err := c.do(ctx, http.MethodPost, origin, "/v1/install/"+url.PathEscape(token)+"/complete",
		map[string]string{"instance_id": instanceID, "result": result}, maxAnswerBytes)
	if err != nil {
		return err
	}
	if status/100 != 2 {
		return errf(CodeBadAnswer, "the store answered %d to the completion", status)
	}
	return nil
}

// VerifyLicense asks `POST <store>/v1/licenses/verify` and answers the
// envelope.
func (c *Client) VerifyLicense(ctx context.Context, origin string, req LicenseRequest) (*Envelope, error) {
	status, b, err := c.do(ctx, http.MethodPost, origin, "/v1/licenses/verify", req, maxAnswerBytes)
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		return nil, errf(CodeUnreachable, "the store answered %d to the license check", status)
	}
	env, err := ParseEnvelope(b)
	if err != nil {
		return nil, errf(CodeBadAnswer, "%v", err)
	}
	return env, nil
}

// LicenseRequest is the body of a license check.
type LicenseRequest struct {
	Key          string `json:"key"`
	App          string `json:"app"`
	InstanceID   string `json:"instance_id"`
	FilexVersion string `json:"filex_version,omitempty"`
}
