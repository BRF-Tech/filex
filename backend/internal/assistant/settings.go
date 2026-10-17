package assistant

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Kind names a provider.
type Kind string

const (
	// KindOpenAI is OpenAI's Chat Completions API - and every server that
	// speaks it: OpenRouter, vLLM, LM Studio, a company gateway.
	KindOpenAI Kind = "openai"
	// KindAnthropic is Anthropic's Messages API (Claude).
	KindAnthropic Kind = "anthropic"
	// KindOllama is a local Ollama server's own chat API.
	KindOllama Kind = "ollama"
)

// Kinds is every provider, in the order the settings page offers them.
func Kinds() []Kind { return []Kind{KindAnthropic, KindOpenAI, KindOllama} }

// ValidKind reports whether k is a provider this build has a driver for.
func ValidKind(k Kind) bool {
	for _, x := range Kinds() {
		if x == k {
			return true
		}
	}
	return false
}

// DefaultBaseURL is the address a provider answers on when the administrator
// names none.
func DefaultBaseURL(k Kind) string {
	switch k {
	case KindOpenAI:
		return "https://api.openai.com/v1"
	case KindAnthropic:
		return "https://api.anthropic.com"
	case KindOllama:
		return "http://localhost:11434"
	}
	return ""
}

// SuggestedModels are the models the settings page offers before the
// provider has been asked for its list (Models). Only Anthropic's are known
// ahead: an OpenAI-compatible server or an Ollama install has whatever its
// operator put on it, so those come from the provider itself.
func SuggestedModels(k Kind) []string {
	if k == KindAnthropic {
		return []string{"claude-opus-5-5", "claude-sonnet-5-5", "claude-haiku-5-5", "claude-fable-5-1"}
	}
	return nil
}

// Settings is what an administrator sets on Admin → Settings → AI assistant.
// The API key is not here: it is kept sealed beside it (handlers
// assistant_settings.go) and handed to New on its own.
type Settings struct {
	// Enabled turns the assistant on. Off by default: nothing leaves the
	// server for a model until an administrator says so.
	Enabled bool `json:"enabled"`
	// HideButton keeps the assistant's button out of the explorer's toolbar
	// for everybody, whatever else is set - the switch for an installation
	// that wants no trace of it.
	HideButton bool   `json:"hide_button"`
	Provider   Kind   `json:"provider"`
	BaseURL    string `json:"base_url"`
	Model      string `json:"model"`
	// MaxToolRounds is how many times one request may go back to the model
	// with tool answers before it is stopped.
	MaxToolRounds int `json:"max_tool_rounds"`
	// MaxOutputTokens is the longest single reply the model may write.
	MaxOutputTokens int `json:"max_output_tokens"`
	// MaxRequestTokens is one request's budget: input and output of every
	// round together. Reaching it stops the request.
	MaxRequestTokens int `json:"max_request_tokens"`
	// TimeoutSeconds is how long one call to the provider may take, retries
	// included.
	TimeoutSeconds int `json:"timeout_seconds"`
	// RequestsPerMinute is how many requests one person may start a minute.
	RequestsPerMinute int `json:"requests_per_minute"`
	// RetentionDays is how long a conversation is kept after it was last
	// used.
	RetentionDays int `json:"retention_days"`
}

// Bounds of the numbers, each [min, max].
var (
	BoundsToolRounds    = [2]int{1, 50}
	BoundsOutputTokens  = [2]int{256, 128000}
	BoundsRequestTokens = [2]int{2000, 10000000}
	BoundsTimeout       = [2]int{10, 900}
	BoundsPerMinute     = [2]int{1, 600}
	BoundsRetention     = [2]int{1, 3650}
)

// Defaults is a fresh install's settings: off, nothing chosen.
func Defaults() Settings {
	return Settings{
		MaxToolRounds:     10,
		MaxOutputTokens:   8192,
		MaxRequestTokens:  400000,
		TimeoutSeconds:    120,
		RequestsPerMinute: 20,
		RetentionDays:     30,
	}
}

// Normalized fills the numbers a stored document left out (a row written by
// an older version) with the defaults, and trims the strings.
func (s Settings) Normalized() Settings {
	d := Defaults()
	fill := func(v *int, def int) {
		if *v <= 0 {
			*v = def
		}
	}
	fill(&s.MaxToolRounds, d.MaxToolRounds)
	fill(&s.MaxOutputTokens, d.MaxOutputTokens)
	fill(&s.MaxRequestTokens, d.MaxRequestTokens)
	fill(&s.TimeoutSeconds, d.TimeoutSeconds)
	fill(&s.RequestsPerMinute, d.RequestsPerMinute)
	fill(&s.RetentionDays, d.RetentionDays)
	s.Provider = Kind(strings.ToLower(strings.TrimSpace(string(s.Provider))))
	s.BaseURL = strings.TrimRight(strings.TrimSpace(s.BaseURL), "/")
	s.Model = strings.TrimSpace(s.Model)
	return s
}

// ErrInvalid wraps every refusal of Validate.
var ErrInvalid = errors.New("invalid assistant setting")

// Invalid is one refused field: what the settings page puts beside it.
type Invalid struct {
	Field string
	Min   int
	Max   int
}

func (e *Invalid) Error() string {
	if e.Max > 0 {
		return fmt.Sprintf("%s must be between %d and %d", e.Field, e.Min, e.Max)
	}
	return e.Field + " is not valid"
}

func (e *Invalid) Unwrap() error { return ErrInvalid }

// Validate refuses a setting out of its bounds, a provider this build does
// not know and an address that is not http(s). An incomplete one (no model
// yet) is valid: it is State that says it is not ready.
func (s Settings) Validate() error {
	check := func(field string, v int, b [2]int) error {
		if v < b[0] || v > b[1] {
			return &Invalid{Field: field, Min: b[0], Max: b[1]}
		}
		return nil
	}
	for _, c := range []struct {
		field string
		v     int
		b     [2]int
	}{
		{"max_tool_rounds", s.MaxToolRounds, BoundsToolRounds},
		{"max_output_tokens", s.MaxOutputTokens, BoundsOutputTokens},
		{"max_request_tokens", s.MaxRequestTokens, BoundsRequestTokens},
		{"timeout_seconds", s.TimeoutSeconds, BoundsTimeout},
		{"requests_per_minute", s.RequestsPerMinute, BoundsPerMinute},
		{"retention_days", s.RetentionDays, BoundsRetention},
	} {
		if err := check(c.field, c.v, c.b); err != nil {
			return err
		}
	}
	if s.Provider != "" && !ValidKind(s.Provider) {
		return &Invalid{Field: "provider"}
	}
	if s.BaseURL != "" {
		u, err := url.Parse(s.BaseURL)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil {
			return &Invalid{Field: "base_url"}
		}
	}
	if s.MaxRequestTokens < s.MaxOutputTokens {
		return &Invalid{Field: "max_request_tokens", Min: s.MaxOutputTokens, Max: BoundsRequestTokens[1]}
	}
	return nil
}

// EffectiveBaseURL is the address the driver calls: the administrator's, or
// the provider's own.
func (s Settings) EffectiveBaseURL() string {
	if s.BaseURL != "" {
		return s.BaseURL
	}
	return DefaultBaseURL(s.Provider)
}

// NeedsKey reports whether this provider at this address cannot work
// without an API key: Anthropic always, OpenAI on OpenAI's own address. An
// OpenAI-compatible server of one's own (vLLM, LM Studio) and Ollama may run
// without one.
func (s Settings) NeedsKey() bool {
	switch s.Provider {
	case KindAnthropic:
		return true
	case KindOpenAI:
		return hostOf(s.EffectiveBaseURL()) == "api.openai.com"
	}
	return false
}

// The assistant's states, as the explorer and the user settings say them.
const (
	// StateOn: switched on and set up.
	StateOn = "on"
	// StateOff: switched off.
	StateOff = "off"
	// StateNotConfigured: switched on, but a provider, a model or a key is
	// missing - "the administrator has not set it up".
	StateNotConfigured = "not_configured"
)

// State is the assistant's state given whether a key is stored.
func (s Settings) State(hasKey bool) string {
	if !s.Enabled {
		return StateOff
	}
	if !ValidKind(s.Provider) || s.Model == "" || (s.NeedsKey() && !hasKey) {
		return StateNotConfigured
	}
	return StateOn
}

// Timeout is TimeoutSeconds as a duration.
func (s Settings) Timeout() time.Duration { return time.Duration(s.TimeoutSeconds) * time.Second }

// New builds the driver for s. key is the API key (empty for none); client
// is the HTTP client to use (nil: one with s's timeout).
func New(s Settings, key string, client *http.Client) (Provider, error) {
	s = s.Normalized()
	if client == nil {
		client = &http.Client{Timeout: s.Timeout()}
	}
	t := &transport{client: client, key: key, retries: 2}
	base := s.EffectiveBaseURL()
	switch s.Provider {
	case KindOpenAI:
		return &openAI{t: t, base: base}, nil
	case KindAnthropic:
		return &anthropic{t: t, base: base}, nil
	case KindOllama:
		return &ollama{t: t, base: base}, nil
	}
	return nil, &Invalid{Field: "provider"}
}

// hostOf is a URL's host name, lower-cased; "" when it does not parse.
func hostOf(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return strings.ToLower(u.Hostname())
}
