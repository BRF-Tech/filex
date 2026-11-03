// Package captcha is filex's bot check on the forms anybody can post without
// signing in: the sign-in, a demo's sign-in, the PIN of a protected link and
// the upload into a file request (docs/BOT-PROTECTION.md).
//
// The provider is Cloudflare Turnstile (or Google reCAPTCHA v2). When the
// provider cannot be reached - a network failure, a timeout, a 5xx, an answer
// that is not JSON - or the configuration cannot work (no site key, no
// secret, a secret the provider refuses), the form is NOT left open and NOT
// shut: the browser does a short proof of work instead (local.go), and the
// administrators are told. A provider that answered and said no (4xx, 429
// included, or success:false) is a refusal (422), never an outage: otherwise
// anybody could make the provider rate-limit a form and get the cheap check.
//
// ⚠⚠ The settings names, the answers (422 / 409 / 503), the outage flag and
// the local fallback are a contract shared with the other products that run
// the same check; filex implements it, it does not vary it.
package captcha

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// The providers' addresses.
const (
	TurnstileSiteverify = "https://challenges.cloudflare.com/turnstile/v0/siteverify"
	RecaptchaSiteverify = "https://www.google.com/recaptcha/api/siteverify"
	TurnstileScript     = "https://challenges.cloudflare.com/turnstile/v0/api.js"
	RecaptchaScript     = "https://www.google.com/recaptcha/api.js"
	// The origins the widget draws its frame from (the pages' frame-src).
	TurnstileFrame = "https://challenges.cloudflare.com"
	RecaptchaFrame = "https://www.google.com"
)

// The test-only addresses of siteverify (an e2e run's "the provider is
// unreachable"). Empty in production.
const (
	EnvTurnstileSiteverify = "CAPTCHA_TURNSTILE_SITEVERIFY"
	EnvRecaptchaSiteverify = "CAPTCHA_RECAPTCHA_SITEVERIFY"
)

// The form fields.
const (
	// TokenField is the provider token's canonical name; the provider's own
	// names (`cf-turnstile-response`, `g-recaptcha-response`) and
	// `turnstile_token` are read as well.
	TokenField = "captcha_token"
	// LocalField carries a local solution (base64 JSON).
	LocalField = "captcha_local"
)

// The lifetimes the contract sets.
const (
	// FallbackFlagTTL is how long the outage flag lives after the last
	// failure to reach the provider.
	FallbackFlagTTL = 120 * time.Second
	// TokenTTL is how long an accepted provider token is remembered.
	TokenTTL = 5 * time.Minute
	// PassTTL is how long an upload pass may wait for its upload.
	PassTTL = 10 * time.Minute
	// AlarmWindow and AlarmThreshold: more than this many fallbacks in the
	// window tell the administrators.
	AlarmWindow    = 10 * time.Minute
	AlarmThreshold = 1
	// MisconfiguredAlarmEvery is how often one misconfiguration is told.
	MisconfiguredAlarmEvery = time.Hour
)

// probeToken is the token the reachability probe sends: the provider answers
// `invalid-input-response` when the secret is right, `invalid-input-secret`
// when it is not.
const probeToken = "filex-captcha-reachability-probe"

// Fields are the bot-check fields of a form, in a JSON body or a form.
type Fields struct {
	CaptchaToken        string `json:"captcha_token,omitempty"`
	CfTurnstileResponse string `json:"cf-turnstile-response,omitempty"`
	GRecaptchaResponse  string `json:"g-recaptcha-response,omitempty"`
	TurnstileToken      string `json:"turnstile_token,omitempty"`
	CaptchaLocal        string `json:"captcha_local,omitempty"`
}

// Token is the first provider token given.
func (f Fields) Token() string {
	for _, v := range []string{f.CaptchaToken, f.CfTurnstileResponse, f.GRecaptchaResponse, f.TurnstileToken} {
		if t := strings.TrimSpace(v); t != "" {
			return t
		}
	}
	return ""
}

// FieldsFromForm reads the fields of a parsed form.
func FieldsFromForm(get func(string) string) Fields {
	return Fields{
		CaptchaToken:        get("captcha_token"),
		CfTurnstileResponse: get("cf-turnstile-response"),
		GRecaptchaResponse:  get("g-recaptcha-response"),
		TurnstileToken:      get("turnstile_token"),
		CaptchaLocal:        get("captcha_local"),
	}
}

// Status is the kind of a Result.
type Status string

// The results.
const (
	// StatusPassed: the provider or the local fallback said yes.
	StatusPassed Status = "passed"
	// StatusSkipped: the check is off, or this form is not protected.
	StatusSkipped Status = "skipped"
	// StatusInvalid: no token, a refused one, an expired one, a bad
	// solution → 422.
	StatusInvalid Status = "invalid"
	// StatusFallback: the provider is down or the configuration cannot work
	// → 409 with a local challenge.
	StatusFallback Status = "fallback"
	// StatusUnavailable: the same, with the fallback off → 503.
	StatusUnavailable Status = "unavailable"
)

// The reasons of an invalid result, besides the local fallback's own.
const (
	ReasonMissingToken     = "missing_token"
	ReasonInvalidToken     = "invalid_token"
	ReasonTokenExpired     = "token_expired"
	ReasonBadToken         = "bad_token"
	ReasonProviderRejected = "provider_rejected"
	ReasonFallbackInactive = "fallback_inactive"
	ReasonFallbackOff      = "fallback_off"
)

// Result is the answer to one form.
type Result struct {
	Status Status
	// Reason says why (invalid), what passed it ("provider" / "local"), or
	// what took the provider away (fallback, unavailable).
	Reason string
	// Challenge is the local challenge of a fallback.
	Challenge *Challenge
}

// OK reports whether the form may go on.
func (r Result) OK() bool { return r.Status == StatusPassed || r.Status == StatusSkipped }

// HTTPStatus is the status the refusal is answered with.
func (r Result) HTTPStatus() int {
	switch r.Status {
	case StatusPassed, StatusSkipped:
		return http.StatusOK
	case StatusFallback:
		return http.StatusConflict
	case StatusUnavailable:
		return http.StatusServiceUnavailable
	}
	return http.StatusUnprocessableEntity
}

// Alarms is how the check tells the administrators.
type Alarms struct {
	// Counted is told every time the local fallback is handed out (the
	// metric); cause is "unreachable" or "misconfigured".
	Counted func(cause string)
	// Fallback is told once per window when the window's fallbacks pass the
	// threshold.
	Fallback func(count int, window time.Duration, cause, form string)
	// Misconfigured is told once an hour per reason.
	Misconfigured func(reason, form string)
}

// Verifier is the check.
type Verifier struct {
	Settings *Settings
	Marks    Marks
	// Client calls siteverify; nil = a plain client (each call has its own
	// timeout).
	Client *http.Client
	// Local issues and checks the fallback's challenges.
	Local *Local
	// PassKey signs the upload passes; nil = Local.Key.
	PassKey []byte
	Alarms  Alarms
	// Siteverify overrides a provider's address (tests).
	Siteverify map[string]string
	// Now is the clock; nil = time.Now.
	Now func() time.Time

	total     atomic.Int64
	mu        sync.Mutex
	window    int64
	windowN   int
	told      int64
	toldMis   map[string]time.Time
	lastPrune atomic.Int64
}

func (v *Verifier) now() time.Time {
	if v.Now != nil {
		return v.Now()
	}
	return time.Now()
}

// Config is the configuration in force.
func (v *Verifier) Config(ctx context.Context) Config { return v.Settings.Load(ctx) }

// FallbackTotal is how many local challenges this instance handed out.
func (v *Verifier) FallbackTotal() int64 { return v.total.Load() }

// Check judges one form.
func (v *Verifier) Check(ctx context.Context, form string, f Fields, ip string) Result {
	cfg := v.Settings.Load(ctx)
	if !cfg.FormEnabled(form) {
		return Result{Status: StatusSkipped, Reason: "off"}
	}
	v.pruneNowAndThen(ctx)
	if local := strings.TrimSpace(f.CaptchaLocal); local != "" {
		return v.checkLocal(ctx, cfg, local)
	}
	// A configuration that cannot work is an outage: the local fallback and
	// an alarm, with or without a token. The protection is never off.
	if mis := cfg.Misconfiguration(); mis != "" {
		return v.misconfigured(ctx, cfg, mis, form)
	}
	token := f.Token()
	if token == "" {
		return Result{Status: StatusInvalid, Reason: ReasonMissingToken}
	}
	return v.checkToken(ctx, cfg, token, ip, form)
}

func (v *Verifier) checkLocal(ctx context.Context, cfg Config, payload string) Result {
	if cfg.Fallback != FallbackLocal {
		return Result{Status: StatusInvalid, Reason: ReasonFallbackOff}
	}
	// ⚠ Only during an outage: while the provider answers, a bot must not
	// skip the widget with the cheap proof of work.
	if !v.FallbackActive(ctx) {
		return Result{Status: StatusInvalid, Reason: ReasonFallbackInactive}
	}
	r := v.Local.Verify(payload, func(sig string, ttl time.Duration) bool {
		now := v.now()
		ok, err := v.marks().AddCaptchaMark(ctx, prefixLocalUsed+sig, unix(now.Add(ttl)), unix(now))
		if err != nil {
			slog.Error("captcha: the once-only mark could not be written", slog.String("err", err.Error()))
			return false
		}
		return ok
	})
	if !r.OK {
		return Result{Status: StatusInvalid, Reason: r.Reason}
	}
	return Result{Status: StatusPassed, Reason: "local"}
}

func (v *Verifier) checkToken(ctx context.Context, cfg Config, token, ip, form string) Result {
	// Bounded BEFORE the provider is asked: a token of the wrong shape is
	// refused here, with no call out (and only its shape is logged).
	if !TokenShapeOK(cfg.Provider, token) {
		slog.Info("captcha: a token of the wrong shape was refused without asking the provider",
			slog.Int("length", len(token)), slog.Bool("narrow_charset", tokenCharsetRe.MatchString(token)), slog.String("form", form))
		return Result{Status: StatusInvalid, Reason: ReasonBadToken}
	}
	sum := sha256.Sum256([]byte(token))
	key := prefixToken + hex.EncodeToString(sum[:])
	now := v.now()
	if until, ok, err := v.marks().CaptchaMarkUntil(ctx, key); err == nil && ok && until > unix(now) {
		return Result{Status: StatusInvalid, Reason: ReasonReplayed}
	}
	out := v.siteverify(ctx, cfg, token, ip)
	switch {
	case !out.reachable:
		return v.outage(ctx, cfg, out.cause, form)
	case out.misconfigured != "":
		return v.misconfigured(ctx, cfg, out.misconfigured, form)
	case !out.success:
		return Result{Status: StatusInvalid, Reason: reasonFromCodes(out.codes)}
	}
	if ok, err := v.marks().AddCaptchaMark(ctx, key, unix(now.Add(TokenTTL)), unix(now)); err == nil && !ok {
		return Result{Status: StatusInvalid, Reason: ReasonReplayed}
	}
	return Result{Status: StatusPassed, Reason: "provider"}
}

// The tokens' shape, measured 2026-11-03 on real tokens: Turnstile's test
// key gave 21 characters ([A-Z.]), a live Turnstile widget 730
// ([A-Za-z0-9._-]), reCAPTCHA v2 1678 and 2297 ([A-Za-z0-9_-]). Turnstile
// documents at most 2048; reCAPTCHA documents none, so its bound is 4096.
// ⚠ Go's `$` is the end of the text (no multi-line flag): a trailing line
// break does not slip through.
const (
	TurnstileTokenMax = 2048
	RecaptchaTokenMax = 4096
)

var tokenCharsetRe = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

// TokenShapeOK reports whether token is the provider's shape: its length
// bound and the narrow character set.
func TokenShapeOK(provider, token string) bool {
	limit := TurnstileTokenMax
	if provider == ProviderRecaptcha {
		limit = RecaptchaTokenMax
	}
	return token != "" && len(token) <= limit && tokenCharsetRe.MatchString(token)
}

// misconfiguredCodes are the provider's answers that mean the KEYS are wrong,
// not the token: only these. ⚠ `bad-request` is not one of them - the token
// comes from the client, and a broken token that earned a bad-request must not
// open the instance-wide fallback for everybody.
var misconfiguredCodes = []string{MisInvalidSecret, MisMissingSecret, "sitekey-secret-mismatch", "invalid-keys"}

func reasonFromCodes(codes []string) string {
	switch {
	case slices.Contains(codes, "timeout-or-duplicate"):
		return ReasonTokenExpired
	case slices.Contains(codes, "missing-input-response"):
		return ReasonMissingToken
	}
	for _, c := range codes {
		if strings.HasPrefix(c, "http_4") {
			return ReasonProviderRejected
		}
	}
	return ReasonInvalidToken
}

// FallbackActive reports whether the outage flag lives.
func (v *Verifier) FallbackActive(ctx context.Context) bool {
	until, ok, err := v.marks().CaptchaMarkUntil(ctx, FlagFallback)
	return err == nil && ok && until > unix(v.now())
}

// outage answers a provider that could not be reached.
func (v *Verifier) outage(ctx context.Context, cfg Config, cause, form string) Result {
	slog.Warn("captcha: the provider could not be reached", slog.String("cause", cause), slog.String("form", form))
	if cfg.Fallback != FallbackLocal {
		return Result{Status: StatusUnavailable, Reason: cause}
	}
	c, err := v.activate(ctx, "unreachable", cause, form)
	if err != nil {
		return Result{Status: StatusUnavailable, Reason: cause}
	}
	return Result{Status: StatusFallback, Reason: "provider_unreachable", Challenge: c}
}

// misconfigured answers a configuration that cannot work: the fallback, and
// the administrators are told.
func (v *Verifier) misconfigured(ctx context.Context, cfg Config, reason, form string) Result {
	slog.Error("captcha: the configuration cannot work; the local fallback answers", slog.String("reason", reason), slog.String("form", form))
	v.tellMisconfigured(reason, form)
	if cfg.Fallback != FallbackLocal {
		return Result{Status: StatusUnavailable, Reason: "misconfigured:" + reason}
	}
	c, err := v.activate(ctx, "misconfigured", "misconfigured:"+reason, form)
	if err != nil {
		return Result{Status: StatusUnavailable, Reason: "misconfigured:" + reason}
	}
	return Result{Status: StatusFallback, Reason: "misconfigured:" + reason, Challenge: c}
}

// activate raises the outage flag, counts the fallback and issues a
// challenge.
func (v *Verifier) activate(ctx context.Context, kind, cause, form string) (*Challenge, error) {
	now := v.now()
	if err := v.marks().PutCaptchaMark(ctx, FlagFallback, unix(now.Add(FallbackFlagTTL))); err != nil {
		slog.Error("captcha: the outage flag could not be written", slog.String("err", err.Error()))
		return nil, err
	}
	v.count(kind, cause, form)
	c, err := v.Local.Issue()
	if err != nil {
		return nil, err
	}
	return &c, nil
}

func (v *Verifier) count(kind, cause, form string) {
	v.total.Add(1)
	if v.Alarms.Counted != nil {
		v.Alarms.Counted(kind)
	}
	now := v.now()
	bucket := now.Unix() / int64(AlarmWindow/time.Second)
	v.mu.Lock()
	if bucket != v.window {
		v.window, v.windowN = bucket, 0
	}
	v.windowN++
	n := v.windowN
	tell := n > AlarmThreshold && v.told != bucket
	if tell {
		v.told = bucket
	}
	v.mu.Unlock()
	if tell && v.Alarms.Fallback != nil {
		v.Alarms.Fallback(n, AlarmWindow, cause, form)
	}
}

func (v *Verifier) tellMisconfigured(reason, form string) {
	now := v.now()
	v.mu.Lock()
	if v.toldMis == nil {
		v.toldMis = map[string]time.Time{}
	}
	last, seen := v.toldMis[reason]
	tell := !seen || now.Sub(last) >= MisconfiguredAlarmEvery
	if tell {
		v.toldMis[reason] = now
	}
	v.mu.Unlock()
	if tell && v.Alarms.Misconfigured != nil {
		v.Alarms.Misconfigured(reason, form)
	}
}

func (v *Verifier) marks() Marks {
	if v.Marks == nil {
		v.mu.Lock()
		if v.Marks == nil {
			v.Marks = NewMemoryMarks()
		}
		v.mu.Unlock()
	}
	return v.Marks
}

// pruneNowAndThen deletes ended marks at most once every ten minutes.
func (v *Verifier) pruneNowAndThen(ctx context.Context) {
	now := v.now().Unix()
	last := v.lastPrune.Load()
	if now-last < 600 || !v.lastPrune.CompareAndSwap(last, now) {
		return
	}
	if _, err := v.marks().PruneCaptchaMarks(context.WithoutCancel(ctx), now); err != nil {
		slog.Warn("captcha: ended marks could not be pruned", slog.String("err", err.Error()))
	}
}

// ── siteverify ─────────────────────────────────────────────────────────────

type verdict struct {
	reachable     bool
	success       bool
	codes         []string
	cause         string
	misconfigured string
	status        int
}

func (v *Verifier) siteverifyURL(provider string) string {
	if u := strings.TrimSpace(v.Siteverify[provider]); u != "" {
		return u
	}
	if provider == ProviderRecaptcha {
		return RecaptchaSiteverify
	}
	return TurnstileSiteverify
}

// siteverify asks the provider. ⚠ Turnstile and reCAPTCHA v2 take the same
// form (secret, response, remoteip) and answer the same shape.
//
// An outage (reachable false) is only: no connection, a timeout, a 5xx, a
// body that is not the provider's JSON, `internal-error`. Those are tried a
// second time. A 4xx is the provider answering: no outage, no retry. The
// secret's refusal is read BEFORE the status: Cloudflare answers a wrong or
// empty secret with HTTP 400 and `invalid-input-secret` /
// `missing-input-secret` (measured 2026-11-03), and that is a configuration
// to fix, not a visitor to refuse.
func (v *Verifier) siteverify(ctx context.Context, cfg Config, token, ip string) verdict {
	form := url.Values{"secret": {cfg.Secret}, "response": {token}}
	if ip != "" {
		form.Set("remoteip", ip)
	}
	target := v.siteverifyURL(cfg.Provider)
	var out verdict
	for attempt := 0; attempt < 2; attempt++ {
		out = v.siteverifyOnce(ctx, cfg.VerifyTimeout, target, form)
		if out.reachable || ctx.Err() != nil {
			return out
		}
	}
	return out
}

func (v *Verifier) siteverifyOnce(ctx context.Context, timeout time.Duration, target string, form url.Values) verdict {
	if timeout <= 0 {
		timeout = DefaultVerifyTimeout
	}
	tctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(tctx, http.MethodPost, target, strings.NewReader(form.Encode()))
	if err != nil {
		return verdict{cause: "request: " + err.Error()}
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	client := v.Client
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		var ne net.Error
		if errors.As(err, &ne) && ne.Timeout() || errors.Is(err, context.DeadlineExceeded) {
			return verdict{cause: "timeout"}
		}
		return verdict{cause: "connection"}
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	var ans struct {
		Success *bool    `json:"success"`
		Codes   []string `json:"error-codes"`
	}
	parsed := json.Unmarshal(body, &ans) == nil
	for _, c := range misconfiguredCodes {
		if parsed && slices.Contains(ans.Codes, c) {
			return verdict{reachable: true, codes: ans.Codes, misconfigured: c, status: resp.StatusCode}
		}
	}
	switch {
	case resp.StatusCode >= 500:
		return verdict{cause: "http_" + strconv.Itoa(resp.StatusCode), status: resp.StatusCode}
	case resp.StatusCode >= 400:
		slog.Warn("captcha: siteverify answered 4xx (a refusal, not an outage)", slog.Int("status", resp.StatusCode), slog.Any("codes", ans.Codes))
		return verdict{reachable: true, codes: append(ans.Codes, "http_"+strconv.Itoa(resp.StatusCode)), status: resp.StatusCode}
	case !parsed || ans.Success == nil:
		return verdict{cause: "bad_response_" + strconv.Itoa(resp.StatusCode), status: resp.StatusCode}
	case slices.Contains(ans.Codes, "internal-error"):
		return verdict{cause: "internal-error", status: resp.StatusCode}
	}
	return verdict{reachable: true, success: *ans.Success, codes: ans.Codes, status: resp.StatusCode}
}

// Probe is what the reachability probe found.
type Probe struct {
	Reachable bool `json:"reachable"`
	// SecretOK: the provider took the secret (it refused only the probe's
	// token).
	SecretOK bool     `json:"secret_ok"`
	Codes    []string `json:"codes"`
	// Misconfigured names a configuration the probe could not use.
	Misconfigured string `json:"misconfigured,omitempty"`
	Cause         string `json:"cause,omitempty"`
}

// ProbeWith asks the provider with cfg's values and a token it will refuse.
func (v *Verifier) ProbeWith(ctx context.Context, cfg Config) Probe {
	if mis := cfg.Misconfiguration(); mis != "" && mis != MisSiteKeyMissing {
		return Probe{Misconfigured: mis, Codes: []string{}}
	}
	out := v.siteverify(ctx, cfg, probeToken, "")
	p := Probe{Reachable: out.reachable, Codes: out.codes, Misconfigured: out.misconfigured, Cause: out.cause}
	if p.Codes == nil {
		p.Codes = []string{}
	}
	p.SecretOK = out.reachable && out.misconfigured == "" && !slices.ContainsFunc(out.codes, func(c string) bool { return strings.HasPrefix(c, "http_4") })
	return p
}

// ── the endpoints' answers ─────────────────────────────────────────────────

// PublicConfig is what a form reads before it draws the widget
// (GET /api/captcha/config). No secret.
type PublicConfig struct {
	Provider       string   `json:"provider"`
	SiteKey        *string  `json:"site_key"`
	Script         *string  `json:"script"`
	Fallback       string   `json:"fallback"`
	Forms          []string `json:"forms"`
	Misconfigured  bool     `json:"misconfigured"`
	FallbackActive bool     `json:"fallback_active"`
	LocalField     string   `json:"local_field"`
	TokenField     string   `json:"token_field"`
}

// Public is the configuration a form reads.
func (v *Verifier) Public(ctx context.Context) PublicConfig {
	cfg := v.Settings.Load(ctx)
	out := PublicConfig{Provider: ProviderNone, Fallback: cfg.Fallback, Forms: []string{}, LocalField: LocalField, TokenField: TokenField}
	if !cfg.Enabled() {
		return out
	}
	out.Provider = cfg.Provider
	if cfg.SiteKey != "" {
		k := cfg.SiteKey
		out.SiteKey = &k
	}
	s := ScriptFor(cfg.Provider)
	out.Script = &s
	out.Forms = slices.Clone(cfg.Forms)
	out.Misconfigured = cfg.Misconfiguration() != ""
	out.FallbackActive = v.FallbackActive(ctx)
	return out
}

// ScriptFor is the provider's script.
func ScriptFor(provider string) string {
	if provider == ProviderRecaptcha {
		return RecaptchaScript
	}
	return TurnstileScript
}

// FrameOrigin is the origin the provider's widget frames, "" when the check
// is off: the pages' frame-src names it.
func FrameOrigin(cfg Config) string {
	switch cfg.Provider {
	case ProviderTurnstile:
		return TurnstileFrame
	case ProviderRecaptcha:
		return RecaptchaFrame
	}
	return ""
}

// ChallengeAnswer is POST /api/captcha/local/challenge: the status and the
// body. The browser asks when the provider's script did not load; the
// fallback is handed out only when the SERVER cannot reach the provider
// either (or the configuration cannot work) - a script an ad blocker stopped
// is not an outage.
func (v *Verifier) ChallengeAnswer(ctx context.Context) (int, map[string]any) {
	cfg := v.Settings.Load(ctx)
	switch {
	case !cfg.Enabled():
		return http.StatusNotFound, map[string]any{"captcha": "disabled"}
	case cfg.Fallback != FallbackLocal:
		return http.StatusServiceUnavailable, map[string]any{"captcha": "unavailable"}
	}
	issue := func(kind, cause string) (int, map[string]any) {
		c, err := v.activate(ctx, kind, cause, "probe")
		if err != nil {
			return http.StatusServiceUnavailable, map[string]any{"captcha": "unavailable"}
		}
		return http.StatusOK, map[string]any{"captcha": "fallback", "challenge": c}
	}
	if v.FallbackActive(ctx) {
		c, err := v.Local.Issue()
		if err != nil {
			return http.StatusServiceUnavailable, map[string]any{"captcha": "unavailable"}
		}
		return http.StatusOK, map[string]any{"captcha": "fallback", "challenge": c}
	}
	if mis := cfg.Misconfiguration(); mis != "" {
		v.tellMisconfigured(mis, "probe")
		return issue("misconfigured", "misconfigured:"+mis)
	}
	out := v.siteverify(ctx, cfg, probeToken, "")
	switch {
	case out.reachable && out.misconfigured != "":
		v.tellMisconfigured(out.misconfigured, "probe")
		return issue("misconfigured", "misconfigured:"+out.misconfigured)
	case out.reachable:
		return http.StatusConflict, map[string]any{"captcha": "provider"}
	}
	return issue("unreachable", "client_probe")
}

// ── upload passes ──────────────────────────────────────────────────────────

// A file request's upload is one multipart request; a refusal sent while
// the browser is still sending the body cannot be read by it (the
// connection closes under the upload). So the check runs first, on a small
// JSON request, and its yes is a pass the upload carries in a header: signed,
// bound to one link, spent once, short-lived.

// PassHeader carries the pass on the upload.
const PassHeader = "X-Captcha-Pass"

func (v *Verifier) passKey() []byte {
	if len(v.PassKey) > 0 {
		return v.PassKey
	}
	return v.Local.Key
}

// IssuePass signs a pass for scope (what it may be spent on).
func (v *Verifier) IssuePass(scope string) (string, time.Time) {
	nonce := make([]byte, 16)
	_, _ = rand.Read(nonce)
	exp := v.now().Add(PassTTL)
	body := hex.EncodeToString(nonce) + "." + strconv.FormatInt(exp.Unix(), 10)
	m := hmac.New(sha256.New, v.passKey())
	m.Write([]byte("pass|" + scope + "|" + body))
	return base64.RawURLEncoding.EncodeToString([]byte(body)) + "." + hex.EncodeToString(m.Sum(nil)), exp
}

// SpendPass checks a pass for scope and spends it: true once.
func (v *Verifier) SpendPass(ctx context.Context, pass, scope string) bool {
	enc, sig, ok := strings.Cut(strings.TrimSpace(pass), ".")
	if !ok || len(pass) > 512 {
		return false
	}
	raw, err := base64.RawURLEncoding.DecodeString(enc)
	if err != nil {
		return false
	}
	body := string(raw)
	m := hmac.New(sha256.New, v.passKey())
	m.Write([]byte("pass|" + scope + "|" + body))
	if subtle.ConstantTimeCompare([]byte(hex.EncodeToString(m.Sum(nil))), []byte(strings.ToLower(sig))) != 1 {
		return false
	}
	nonce, expRaw, ok := strings.Cut(body, ".")
	if !ok {
		return false
	}
	exp, err := strconv.ParseInt(expRaw, 10, 64)
	now := v.now()
	if err != nil || unix(now) >= exp {
		return false
	}
	added, err := v.marks().AddCaptchaMark(ctx, prefixPass+nonce, exp, unix(now))
	if err != nil {
		slog.Error("captcha: an upload pass could not be spent", slog.String("err", err.Error()))
		return false
	}
	return added
}
