package handlers

// The AI assistant's settings (task #269): one JSON row for what an
// administrator chose (internal/assistant Settings) and one sealed row for
// the provider's API key, with the admin page's routes
// (GET/PATCH /api/admin/assistant, POST /api/admin/assistant/test) and their
// admin_ai_assistant_* MCP twins (ai_admin.go).
//
// ⚠⚠ The API key goes one way. It is sealed with the installation's secret
// key the moment it arrives (secretbox, bound to its row), opened only to
// call the provider, and never written back to anybody: not in an answer
// (`has_key` says whether there is one), not in the audit row (the field's
// name only), not in a log line (the drivers redact it from the provider's
// own words). An installation without FILEX_SECRET_KEY cannot store one and is
// told so (`assistant_no_secret_key`) - a provider that needs no key (Ollama,
// an OpenAI-compatible server of one's own) still works there.

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/brf-tech/filex/backend/internal/apierr"
	"github.com/brf-tech/filex/backend/internal/assistant"
	"github.com/brf-tech/filex/backend/internal/auth"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/secretbox"
)

// The settings rows.
const (
	// AssistantSettingKey holds the assistant.Settings document.
	AssistantSettingKey = "assistant.config"
	// AssistantSecretKey holds the provider's API key, sealed. Its last
	// segment is a secret's name (isSecretSettingKey), so the generic
	// settings list shows "***" for it.
	AssistantSecretKey = "assistant.secret"
)

// assistantSecretAAD binds the sealed key to its row: a value copied into
// another row does not open there.
const assistantSecretAAD = "settings:" + AssistantSecretKey

const assistantIsInstanceWide = "the AI assistant is set up for the whole instance by the platform operator"

// AssistantConfig reads and writes the assistant's settings.
type AssistantConfig struct {
	Store db.Store
	// Box seals the API key (FILEX_SECRET_KEY). Nil or without a key: no key
	// can be stored.
	Box *secretbox.Box
	// Client is the HTTP client the drivers use (nil: one with the
	// configured timeout). Tests point it at their fake provider.
	Client *http.Client
}

// assistantLoaded is the settings as they stand, with the key opened.
type assistantLoaded struct {
	Settings assistant.Settings
	Key      string
	HasKey   bool
}

// State is the assistant's state (assistant.StateOn, StateOff,
// StateNotConfigured).
func (l assistantLoaded) State() string { return l.Settings.State(l.HasKey) }

// Load reads the settings: a fresh install's defaults when nothing was
// saved, the defaults again (and a log line) when the row does not parse.
// The key is opened only when it is sealed: a value written into the row by
// hand, in the clear, is not used.
func (c *AssistantConfig) Load(ctx context.Context) assistantLoaded {
	out := assistantLoaded{Settings: assistant.Defaults()}
	if c == nil || c.Store == nil {
		return out
	}
	if raw, err := c.Store.GetSetting(ctx, AssistantSettingKey); err == nil && strings.TrimSpace(raw) != "" {
		var s assistant.Settings
		if jerr := json.Unmarshal([]byte(raw), &s); jerr != nil {
			slog.Warn("assistant: the settings row does not parse; using the defaults", slog.String("err", jerr.Error()))
		} else {
			out.Settings = s
		}
	}
	out.Settings = out.Settings.Normalized()
	sealed, err := c.Store.GetSetting(ctx, AssistantSecretKey)
	if err != nil || strings.TrimSpace(sealed) == "" {
		return out
	}
	if !secretbox.IsSealed(sealed) {
		slog.Warn("assistant: the API key row is not sealed; it is not used")
		return out
	}
	key, oerr := c.Box.OpenFor(sealed, assistantSecretAAD)
	if oerr != nil {
		slog.Warn("assistant: the API key cannot be opened", slog.String("err", oerr.Error()))
		return out
	}
	out.Key, out.HasKey = key, key != ""
	return out
}

// saveSettings writes the settings row.
func (c *AssistantConfig) saveSettings(ctx context.Context, s assistant.Settings) error {
	b, err := json.Marshal(s)
	if err != nil {
		return err
	}
	return c.Store.UpsertSetting(ctx, AssistantSettingKey, string(b))
}

// errAssistantNoSecretKey: a key was given and there is no box to seal it.
var errAssistantNoSecretKey = errors.New("no FILEX_SECRET_KEY to seal the assistant's API key with")

// saveKey seals and writes key; "" removes it.
func (c *AssistantConfig) saveKey(ctx context.Context, key string) error {
	if key == "" {
		return c.Store.UpsertSetting(ctx, AssistantSecretKey, "")
	}
	if c.Box == nil || !c.Box.Enabled() {
		return errAssistantNoSecretKey
	}
	sealed, err := c.Box.SealFor(key, assistantSecretAAD)
	if err != nil {
		return err
	}
	return c.Store.UpsertSetting(ctx, AssistantSecretKey, sealed)
}

// provider builds the driver for l.
func (c *AssistantConfig) provider(l assistantLoaded) (assistant.Provider, error) {
	var client *http.Client
	if c != nil {
		client = c.Client
	}
	return assistant.New(l.Settings, l.Key, client)
}

// ── the admin page ───────────────────────────────────────────────────────────

// AssistantAdmin serves Admin → Settings → AI assistant.
type AssistantAdmin struct {
	Config *AssistantConfig
}

// assistantProviderInfo is what the page says about one provider.
type assistantProviderInfo struct {
	Kind            assistant.Kind `json:"kind"`
	DefaultBaseURL  string         `json:"default_base_url"`
	SuggestedModels []string       `json:"suggested_models"`
}

// assistantAdminView is the page's answer. ⚠ No key in it - has_key only.
type assistantAdminView struct {
	assistant.Settings
	HasKey bool   `json:"has_key"`
	State  string `json:"state"`
	// NeedsKey: the provider at this address cannot work without a key.
	NeedsKey bool `json:"needs_key"`
	// SecretKeyConfigured: FILEX_SECRET_KEY is set, so a key can be stored.
	SecretKeyConfigured bool                    `json:"secret_key_configured"`
	Providers           []assistantProviderInfo `json:"providers"`
	Bounds              map[string][2]int       `json:"bounds"`
}

func (h *AssistantAdmin) view(ctx context.Context) assistantAdminView {
	l := h.Config.Load(ctx)
	v := assistantAdminView{
		Settings:            l.Settings,
		HasKey:              l.HasKey,
		State:               l.State(),
		NeedsKey:            l.Settings.NeedsKey(),
		SecretKeyConfigured: h.Config.Box != nil && h.Config.Box.Enabled(),
		Bounds: map[string][2]int{
			"max_tool_rounds":     assistant.BoundsToolRounds,
			"max_output_tokens":   assistant.BoundsOutputTokens,
			"max_request_tokens":  assistant.BoundsRequestTokens,
			"timeout_seconds":     assistant.BoundsTimeout,
			"requests_per_minute": assistant.BoundsPerMinute,
			"retention_days":      assistant.BoundsRetention,
		},
	}
	for _, k := range assistant.Kinds() {
		v.Providers = append(v.Providers, assistantProviderInfo{
			Kind: k, DefaultBaseURL: assistant.DefaultBaseURL(k), SuggestedModels: assistant.SuggestedModels(k),
		})
	}
	return v
}

// Get answers the page.
//
//	GET /api/admin/assistant
func (h *AssistantAdmin) Get(w http.ResponseWriter, r *http.Request) {
	if !requireSupertenant(w, r, assistantIsInstanceWide) {
		return
	}
	writeJSON(w, http.StatusOK, h.view(r.Context()))
}

// assistantPatch is a change to the settings: any of the fields. APIKey is
// the new key (an empty string changes nothing); ClearAPIKey removes the one
// stored.
type assistantPatch struct {
	Enabled           *bool   `json:"enabled,omitempty"`
	HideButton        *bool   `json:"hide_button,omitempty"`
	Provider          *string `json:"provider,omitempty"`
	BaseURL           *string `json:"base_url,omitempty"`
	Model             *string `json:"model,omitempty"`
	MaxToolRounds     *int    `json:"max_tool_rounds,omitempty"`
	MaxOutputTokens   *int    `json:"max_output_tokens,omitempty"`
	MaxRequestTokens  *int    `json:"max_request_tokens,omitempty"`
	TimeoutSeconds    *int    `json:"timeout_seconds,omitempty"`
	RequestsPerMinute *int    `json:"requests_per_minute,omitempty"`
	RetentionDays     *int    `json:"retention_days,omitempty"`
	APIKey            *string `json:"api_key,omitempty"`
	ClearAPIKey       bool    `json:"clear_api_key,omitempty"`
}

// apply writes p over s and answers the names of the fields that changed.
func (p assistantPatch) apply(s assistant.Settings) (assistant.Settings, []string) {
	var changed []string
	setB := func(name string, dst *bool, v *bool) {
		if v != nil && *dst != *v {
			*dst = *v
			changed = append(changed, name)
		}
	}
	setS := func(name string, dst *string, v *string) {
		if v != nil && *dst != strings.TrimSpace(*v) {
			*dst = strings.TrimSpace(*v)
			changed = append(changed, name)
		}
	}
	setI := func(name string, dst *int, v *int) {
		if v != nil && *dst != *v {
			*dst = *v
			changed = append(changed, name)
		}
	}
	setB("enabled", &s.Enabled, p.Enabled)
	setB("hide_button", &s.HideButton, p.HideButton)
	if p.Provider != nil {
		k := assistant.Kind(strings.ToLower(strings.TrimSpace(*p.Provider)))
		if s.Provider != k {
			s.Provider = k
			changed = append(changed, "provider")
		}
	}
	setS("base_url", &s.BaseURL, p.BaseURL)
	setS("model", &s.Model, p.Model)
	setI("max_tool_rounds", &s.MaxToolRounds, p.MaxToolRounds)
	setI("max_output_tokens", &s.MaxOutputTokens, p.MaxOutputTokens)
	setI("max_request_tokens", &s.MaxRequestTokens, p.MaxRequestTokens)
	setI("timeout_seconds", &s.TimeoutSeconds, p.TimeoutSeconds)
	setI("requests_per_minute", &s.RequestsPerMinute, p.RequestsPerMinute)
	setI("retention_days", &s.RetentionDays, p.RetentionDays)
	s.BaseURL = strings.TrimRight(s.BaseURL, "/")
	return s, changed
}

// writeAssistantInvalid says which setting was refused and why.
func writeAssistantInvalid(w http.ResponseWriter, r *http.Request, err error) {
	var inv *assistant.Invalid
	if errors.As(err, &inv) && inv.Max > 0 {
		writeError(w, r, http.StatusBadRequest, "assistant_out_of_bounds", apierr.Params{
			"field": inv.Field, "min": strconv.Itoa(inv.Min), "max": strconv.Itoa(inv.Max),
		})
		return
	}
	field := "settings"
	if inv != nil {
		field = inv.Field
	}
	writeError(w, r, http.StatusBadRequest, "assistant_bad_value", apierr.Params{"field": field})
}

// Patch changes the settings, validated as the page validates them.
//
//	PATCH /api/admin/assistant
func (h *AssistantAdmin) Patch(w http.ResponseWriter, r *http.Request) {
	if !requireSupertenant(w, r, assistantIsInstanceWide) {
		return
	}
	var req assistantPatch
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, r, http.StatusBadRequest, "bad_json", nil)
		return
	}
	ctx := r.Context()
	// ⚠ Load has filled what the stored row left out; the patch is validated
	// as sent. Normalizing it again would turn a 0 or a negative number into
	// the default instead of refusing it.
	cur := h.Config.Load(ctx)
	next, changed := req.apply(cur.Settings)
	if err := next.Validate(); err != nil {
		writeAssistantInvalid(w, r, err)
		return
	}
	newKey := ""
	if req.APIKey != nil {
		newKey = strings.TrimSpace(*req.APIKey)
	}
	if newKey != "" || (req.ClearAPIKey && cur.HasKey) {
		if err := h.Config.saveKey(ctx, newKey); err != nil {
			if errors.Is(err, errAssistantNoSecretKey) {
				writeError(w, r, http.StatusConflict, "assistant_no_secret_key", nil)
				return
			}
			writeError(w, r, http.StatusInternalServerError, "internal_error", nil, "detail", err.Error())
			return
		}
		// The field's name, never its value.
		changed = append(changed, "api_key")
	}
	if err := h.Config.saveSettings(ctx, next); err != nil {
		writeError(w, r, http.StatusInternalServerError, "internal_error", nil, "detail", err.Error())
		return
	}
	auth.AddAuditDetail(ctx, "changed", changed)
	writeJSON(w, http.StatusOK, h.view(ctx))
}

// assistantTestReq is the connection test's form: the values on the page,
// saved or not. A field left out is the saved one.
type assistantTestReq struct {
	Provider *string `json:"provider,omitempty"`
	BaseURL  *string `json:"base_url,omitempty"`
	Model    *string `json:"model,omitempty"`
	APIKey   string  `json:"api_key,omitempty"`
}

// assistantTestAnswer is what the test found.
type assistantTestAnswer struct {
	OK bool `json:"ok"`
	// Models are the models the address offers (empty when it does not
	// list them).
	Models []string `json:"models"`
	// ModelListed: the model asked for is among them (nil: no list).
	ModelListed *bool `json:"model_listed,omitempty"`
	// Message is the outcome in the reader's language; Detail the
	// provider's own words, when it failed.
	Message string `json:"message"`
	Detail  string `json:"detail,omitempty"`
}

// assistantProbeTool is the one tool the test offers: a model that cannot
// call tools is refused right there, by the provider, instead of failing the
// first time somebody asks it something.
var assistantProbeTool = assistant.ToolDef{
	Name:        "noop",
	Description: "Does nothing. Do not call it.",
	Schema:      json.RawMessage(`{"type":"object","properties":{}}`),
}

// Test asks the provider for its models and for a one-word reply with a tool
// on offer, with the page's values.
//
//	POST /api/admin/assistant/test
func (h *AssistantAdmin) Test(w http.ResponseWriter, r *http.Request) {
	if !requireSupertenant(w, r, assistantIsInstanceWide) {
		return
	}
	var req assistantTestReq
	// An empty body is a test of the saved values.
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
		writeError(w, r, http.StatusBadRequest, "bad_json", nil)
		return
	}
	ctx := r.Context()
	auth.SetAuditAction(ctx, AuditActionAssistantTest, "assistant")
	saved := h.Config.Load(ctx)
	l := saved
	if req.Provider != nil {
		l.Settings.Provider = assistant.Kind(strings.ToLower(strings.TrimSpace(*req.Provider)))
	}
	if req.BaseURL != nil {
		l.Settings.BaseURL = strings.TrimRight(strings.TrimSpace(*req.BaseURL), "/")
	}
	if req.Model != nil {
		l.Settings.Model = strings.TrimSpace(*req.Model)
	}
	// ⚠ The stored key goes only to the address it was saved for: a test
	// against an address typed but not saved needs the key typed with it.
	sameTarget := l.Settings.Provider == saved.Settings.Provider && l.Settings.EffectiveBaseURL() == saved.Settings.EffectiveBaseURL()
	switch {
	case strings.TrimSpace(req.APIKey) != "":
		l.Key = strings.TrimSpace(req.APIKey)
	case !sameTarget:
		l.Key = ""
	}
	l.Settings = l.Settings.Normalized()
	if err := l.Settings.Validate(); err != nil || !assistant.ValidKind(l.Settings.Provider) {
		if err == nil {
			err = &assistant.Invalid{Field: "provider"}
		}
		writeAssistantInvalid(w, r, err)
		return
	}
	lang := readerLang(r)
	p, err := h.Config.provider(l)
	if err != nil {
		writeAssistantInvalid(w, r, err)
		return
	}
	tctx, cancel := context.WithTimeout(ctx, l.Settings.Timeout())
	defer cancel()
	out := assistantTestAnswer{Models: []string{}}
	if models, merr := p.Models(tctx); merr == nil {
		out.Models = models
		if l.Settings.Model != "" && len(models) > 0 {
			listed := containsString(models, l.Settings.Model)
			out.ModelListed = &listed
		}
	}
	if l.Settings.Model == "" {
		out.Message = assistantText(lang, "test.no_model", nil)
		writeJSON(w, http.StatusOK, out)
		return
	}
	_, cerr := p.Chat(tctx, assistant.Request{
		Model:     l.Settings.Model,
		System:    "You are a connection test. Reply with the single word OK.",
		Messages:  []assistant.Message{{Role: assistant.RoleUser, Parts: []assistant.Part{{Kind: assistant.PartText, Text: "Reply with OK."}}}},
		Tools:     []assistant.ToolDef{assistantProbeTool},
		MaxTokens: 512,
	}, func(string) {})
	if cerr != nil {
		out.Message, out.Detail = assistantProviderWords(lang, cerr)
		writeJSON(w, http.StatusOK, out)
		return
	}
	out.OK = true
	out.Message = assistantText(lang, "test.ok", nil)
	writeJSON(w, http.StatusOK, out)
}

// assistantProviderWords says a provider failure in the reader's language,
// with the provider's own words as the detail.
func assistantProviderWords(lang string, err error) (message, detail string) {
	var perr *assistant.ProviderError
	switch {
	case errors.As(err, &perr) && perr.Unauthorized():
		return apierr.Text(lang, "assistant_provider_key", nil), perr.Message
	case errors.As(err, &perr) && strings.Contains(strings.ToLower(perr.Message), "does not support tools"):
		return apierr.Text(lang, "assistant_no_tools", nil), perr.Message
	case errors.As(err, &perr):
		return apierr.Text(lang, "assistant_provider", apierr.Params{"detail": perr.Message}), perr.Message
	case errors.Is(err, context.DeadlineExceeded):
		return apierr.Text(lang, "assistant_timeout", nil), ""
	}
	return apierr.Text(lang, "assistant_provider", apierr.Params{"detail": err.Error()}), err.Error()
}

// AuditActionAssistantTest is the row a connection test leaves: it sent the
// key to the provider, so who did it and when is worth a row.
const AuditActionAssistantTest = "assistant.test"

// assistantSweepEvery is how often the retention sweep runs.
const assistantSweepEvery = time.Hour
