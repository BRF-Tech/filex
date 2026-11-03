package handlers

// The bot check's doors (internal/captcha, docs/BOT-PROTECTION.md):
//
//	GET  /api/captcha/config            what a form draws (no secret)
//	POST /api/captcha/local/challenge   the local fallback, when the widget's
//	                                    script did not load and the server
//	                                    cannot reach the provider either
//	POST /api/public/d/{token}/captcha  a file request's upload pass
//	GET/PATCH /api/admin/captcha        Admin → Bot protection
//	POST /api/admin/captcha/test        the provider asked with the page's keys
//
// and Gate, which every protected form asks before it does anything else:
// the sign-in (Auth.Login), a link's PIN (PublicAPI.PIN, the no-JavaScript
// forms) and the upload into a file request (Drop.handleDrop).

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/brf-tech/filex/backend/internal/apierr"
	"github.com/brf-tech/filex/backend/internal/auth"
	"github.com/brf-tech/filex/backend/internal/captcha"
)

// Captcha answers the bot check's public doors and gates the forms.
type Captcha struct {
	V *captcha.Verifier

	configLimit    *ipLimiter
	challengeLimit *ipLimiter
	passLimit      *ipLimiter
}

// NewCaptcha wraps v. Nil v: every form passes (a harness without the
// check).
func NewCaptcha(v *captcha.Verifier) *Captcha {
	return &Captcha{
		V: v,
		// The config is read on every form's load; the challenge asks the
		// provider (a server-side call per request), so it is held tighter.
		configLimit:    newIPLimiter(120, time.Minute),
		challengeLimit: newIPLimiter(10, time.Minute),
		passLimit:      newIPLimiter(30, time.Minute),
	}
}

// Protected reports whether form is protected now.
func (h *Captcha) Protected(ctx context.Context, form string) bool {
	return h != nil && h.V != nil && h.V.Config(ctx).FormEnabled(form)
}

// captchaCode is the refusal's code: the one a program branches on and the
// one its sentence is said by (server.error.<code>).
func captchaCode(res captcha.Result) string {
	switch res.Status {
	case captcha.StatusFallback:
		return "captcha_fallback"
	case captcha.StatusUnavailable:
		return "captcha_unavailable"
	}
	switch res.Reason {
	case captcha.ReasonMissingToken, captchaMissingPass:
		return "captcha_required"
	case captcha.ReasonTokenExpired, captcha.ReasonReplayed, captcha.ReasonExpired:
		return "captcha_expired"
	case captcha.ReasonFallbackInactive:
		return "captcha_fallback_inactive"
	}
	return "captcha_failed"
}

// The upload pass's own reasons.
const (
	captchaMissingPass = "missing_pass"
	captchaBadPass     = "bad_pass"
)

// writeCaptchaRefusal answers a form the check stopped: 422 (`captcha` = the
// reason), 409 with the local challenge, or 503.
func writeCaptchaRefusal(w http.ResponseWriter, r *http.Request, res captcha.Result) {
	field := res.Reason
	switch res.Status {
	case captcha.StatusFallback:
		field = "fallback"
	case captcha.StatusUnavailable:
		field = "unavailable"
	}
	extra := []any{"captcha", field}
	if res.Challenge != nil {
		extra = append(extra, "challenge", res.Challenge)
	}
	w.Header().Set("Cache-Control", "no-store")
	writeError(w, r, res.HTTPStatus(), captchaCode(res), nil, extra...)
}

// Gate judges form and answers the refusal itself; true = go on.
func (h *Captcha) Gate(w http.ResponseWriter, r *http.Request, form string, f captcha.Fields) bool {
	if h == nil || h.V == nil {
		return true
	}
	res := h.V.Check(r.Context(), form, f, clientIP(r))
	if res.OK() {
		return true
	}
	writeCaptchaRefusal(w, r, res)
	return false
}

// Config answers the form's question: which widget, with which site key.
//
//	GET /api/captcha/config
func (h *Captcha) Config(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if !h.configLimit.allow(clientIP(r)) {
		writeError(w, r, http.StatusTooManyRequests, "rate_limited", nil)
		return
	}
	if h == nil || h.V == nil {
		writeJSON(w, http.StatusOK, captcha.PublicConfig{Provider: captcha.ProviderNone, Fallback: captcha.FallbackLocal, Forms: []string{},
			LocalField: captcha.LocalField, TokenField: captcha.TokenField})
		return
	}
	writeJSON(w, http.StatusOK, h.V.Public(r.Context()))
}

// LocalChallenge hands out the local fallback when the server cannot reach
// the provider either (internal/captcha ChallengeAnswer).
//
//	POST /api/captcha/local/challenge
func (h *Captcha) LocalChallenge(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if !h.challengeLimit.allow(clientIP(r)) {
		writeError(w, r, http.StatusTooManyRequests, "rate_limited", nil)
		return
	}
	if h == nil || h.V == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"captcha": "disabled"})
		return
	}
	status, body := h.V.ChallengeAnswer(r.Context())
	writeJSON(w, status, body)
}

// passScope binds a pass to one file request: the token's hash, never the
// token.
func passScope(token string) string {
	sum := sha256.Sum256([]byte(strings.ToLower(strings.TrimSpace(token))))
	return captcha.FormFileRequest + ":" + hex.EncodeToString(sum[:])
}

// DropPass checks the visitor of a file request BEFORE the upload and hands
// back the pass the upload carries (captcha.PassHeader): a multipart request
// refused while its body is still on the way cannot be read by the browser.
//
//	POST /api/public/d/{token}/captcha   {captcha_token | captcha_local}
func (h *Captcha) DropPass(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if !h.passLimit.allow(clientIP(r)) {
		writeError(w, r, http.StatusTooManyRequests, "rate_limited", nil)
		return
	}
	var f captcha.Fields
	if err := json.NewDecoder(io.LimitReader(r.Body, 16<<10)).Decode(&f); err != nil && !errors.Is(err, io.EOF) {
		writeError(w, r, http.StatusBadRequest, "bad_json", nil)
		return
	}
	if !h.Protected(r.Context(), captcha.FormFileRequest) {
		writeJSON(w, http.StatusOK, map[string]any{"pass": ""})
		return
	}
	if !h.Gate(w, r, captcha.FormFileRequest, f) {
		return
	}
	pass, exp := h.V.IssuePass(passScope(pathParam(r, "token")))
	writeJSON(w, http.StatusOK, map[string]any{"pass": pass, "expires_at": exp.UTC().Format(time.RFC3339)})
}

// GateDropUpload is the upload's half: the pass from DropPass, spent once,
// for this link only. True = go on.
func (h *Captcha) GateDropUpload(w http.ResponseWriter, r *http.Request, token string) bool {
	if !h.Protected(r.Context(), captcha.FormFileRequest) {
		return true
	}
	pass := strings.TrimSpace(r.Header.Get(captcha.PassHeader))
	switch {
	case pass == "":
		writeCaptchaRefusal(w, r, captcha.Result{Status: captcha.StatusInvalid, Reason: captchaMissingPass})
		return false
	case !h.V.SpendPass(r.Context(), pass, passScope(token)):
		writeCaptchaRefusal(w, r, captcha.Result{Status: captcha.StatusInvalid, Reason: captchaBadPass})
		return false
	}
	return true
}

// ── Admin → Bot protection ─────────────────────────────────────

const captchaIsInstanceWide = "the bot check is set up for the whole instance by the platform operator"

// CaptchaAdmin serves the settings page.
type CaptchaAdmin struct {
	V *captcha.Verifier
}

// captchaEffective is a configuration as the page shows it: no secret.
type captchaEffective struct {
	Provider        string   `json:"provider"`
	SiteKey         string   `json:"site_key"`
	HasSecret       bool     `json:"has_secret"`
	Forms           []string `json:"forms"`
	Fallback        string   `json:"fallback"`
	VerifyTimeoutMS int      `json:"verify_timeout_ms"`
}

type captchaAdminView struct {
	Stored              captcha.Stored    `json:"stored"`
	Effective           captchaEffective  `json:"effective"`
	Sources             map[string]string `json:"sources"`
	Misconfigured       string            `json:"misconfigured"`
	SecretKeyConfigured bool              `json:"secret_key_configured"`
	FallbackActive      bool              `json:"fallback_active"`
	FallbackTotal       int64             `json:"fallback_total"`
	AllForms            []string          `json:"all_forms"`
	Providers           []string          `json:"providers"`
	Bounds              map[string][2]int `json:"bounds"`
}

func (h *CaptchaAdmin) view(ctx context.Context) captchaAdminView {
	cfg := h.V.Settings.Load(ctx)
	stored := h.V.Settings.Stored(ctx)
	forms := cfg.Forms
	if forms == nil {
		forms = []string{}
	}
	return captchaAdminView{
		Stored: stored,
		Effective: captchaEffective{
			Provider: cfg.Provider, SiteKey: cfg.SiteKey, HasSecret: cfg.Secret != "" || cfg.SecretUnreadable,
			Forms: forms, Fallback: cfg.Fallback, VerifyTimeoutMS: int(cfg.VerifyTimeout / time.Millisecond),
		},
		Sources:             cfg.Sources,
		Misconfigured:       cfg.Misconfiguration(),
		SecretKeyConfigured: h.V.Settings.Box != nil && h.V.Settings.Box.Enabled(),
		FallbackActive:      h.V.FallbackActive(ctx),
		FallbackTotal:       h.V.FallbackTotal(),
		AllForms:            captcha.Forms,
		Providers:           captcha.Providers,
		Bounds:              map[string][2]int{"verify_timeout_ms": {captcha.MinVerifyTimeoutMS, captcha.MaxVerifyTimeoutMS}},
	}
}

// Get answers the page.
//
//	GET /api/admin/captcha
func (h *CaptchaAdmin) Get(w http.ResponseWriter, r *http.Request) {
	if !requireSupertenant(w, r, captchaIsInstanceWide) {
		return
	}
	writeJSON(w, http.StatusOK, h.view(r.Context()))
}

// Patch changes the settings. The secret is written and never read back;
// the audit row names the fields, never a value.
//
//	PATCH /api/admin/captcha
func (h *CaptchaAdmin) Patch(w http.ResponseWriter, r *http.Request) {
	if !requireSupertenant(w, r, captchaIsInstanceWide) {
		return
	}
	var p captcha.Patch
	if err := json.NewDecoder(io.LimitReader(r.Body, 64<<10)).Decode(&p); err != nil {
		writeError(w, r, http.StatusBadRequest, "bad_json", nil)
		return
	}
	changed, err := h.V.Settings.Apply(r.Context(), p)
	if len(changed) > 0 {
		auth.AddAuditDetail(r.Context(), "changed", changed)
	}
	var inv *captcha.Invalid
	switch {
	case errors.As(err, &inv):
		params := apierr.Params{"field": inv.Field}
		if inv.Max > 0 {
			params["min"], params["max"] = strconv.Itoa(inv.Min), strconv.Itoa(inv.Max)
			writeError(w, r, http.StatusBadRequest, "captcha_out_of_bounds", params)
			return
		}
		writeError(w, r, http.StatusBadRequest, "captcha_bad_value", params)
		return
	case errors.Is(err, captcha.ErrNoSecretKey):
		writeError(w, r, http.StatusConflict, "captcha_no_secret_key", nil)
		return
	case err != nil:
		writeError(w, r, http.StatusInternalServerError, "internal_error", nil, "detail", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, h.view(r.Context()))
}

// captchaTestReq is the page's values, saved or not; a field left out is the
// one in force.
type captchaTestReq struct {
	Provider *string `json:"provider,omitempty"`
	SiteKey  *string `json:"site_key,omitempty"`
	Secret   string  `json:"secret,omitempty"`
}

type captchaTestAnswer struct {
	captcha.Probe
	OK      bool   `json:"ok"`
	Message string `json:"message"`
}

// AuditActionCaptchaTest is the row a test leaves: it sent the secret to the
// provider.
const AuditActionCaptchaTest = "captcha.test"

// Test asks the provider with a token it will refuse: is it reachable, and
// does it take the secret.
//
//	POST /api/admin/captcha/test
func (h *CaptchaAdmin) Test(w http.ResponseWriter, r *http.Request) {
	if !requireSupertenant(w, r, captchaIsInstanceWide) {
		return
	}
	var req captchaTestReq
	if err := json.NewDecoder(io.LimitReader(r.Body, 16<<10)).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
		writeError(w, r, http.StatusBadRequest, "bad_json", nil)
		return
	}
	ctx := r.Context()
	auth.SetAuditAction(ctx, AuditActionCaptchaTest, "captcha")
	cfg := h.V.Settings.Load(ctx)
	saved := cfg.Provider
	if req.Provider != nil {
		cfg.Provider = strings.ToLower(strings.TrimSpace(*req.Provider))
	}
	if req.SiteKey != nil {
		cfg.SiteKey = strings.TrimSpace(*req.SiteKey)
	}
	// ⚠ The stored secret goes only to the provider it was saved for.
	switch {
	case strings.TrimSpace(req.Secret) != "":
		cfg.Secret, cfg.SecretUnreadable = strings.TrimSpace(req.Secret), false
	case cfg.Provider != saved:
		cfg.Secret, cfg.SecretUnreadable = "", false
	}
	lang := readerLang(r)
	if !cfg.Enabled() {
		writeJSON(w, http.StatusOK, captchaTestAnswer{Probe: captcha.Probe{Codes: []string{}}, Message: apierr.Text(lang, "captcha_test_off", nil)})
		return
	}
	p := h.V.ProbeWith(ctx, cfg)
	out := captchaTestAnswer{Probe: p, OK: p.SecretOK}
	switch {
	case p.Misconfigured != "":
		out.Message = apierr.Text(lang, "captcha_test_misconfigured", apierr.Params{"detail": p.Misconfigured})
	case !p.Reachable:
		out.Message = apierr.Text(lang, "captcha_test_unreachable", nil)
	case !p.SecretOK:
		out.Message = apierr.Text(lang, "captcha_test_refused", apierr.Params{"detail": strings.Join(p.Codes, ", ")})
	case cfg.SiteKey == "":
		out.OK = false
		out.Message = apierr.Text(lang, "captcha_test_no_site_key", nil)
	default:
		out.Message = apierr.Text(lang, "captcha_test_ok", nil)
	}
	writeJSON(w, http.StatusOK, out)
}
