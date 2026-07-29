package update

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Service owns the periodic release check and caches its outcome so the admin
// UI can render instantly without touching the network on every page load.
//
// It is intentionally passive: Check() only LEARNS. Applying is a separate,
// explicit call (Apply), so a bug in the checker can never move an install.
type Service struct {
	cfg    Config
	client *http.Client
	mode   InstallMode

	mu       sync.RWMutex
	state    State
	preApply PreApply

	// OnNewRelease, when set, is called once per newly discovered version —
	// wired to notifications. "Once" is tracked in the persisted state, so a
	// restart loop cannot spam the operator.
	OnNewRelease func(Decision)
}

// Config is the runtime configuration of the updater.
type Config struct {
	// Enabled turns the whole feature off (no outbound request at all).
	Enabled bool
	// Policy is how far this install may move on its own.
	Policy Policy
	// Channel selects the manifest flavor; informational unless ManifestURL is
	// templated by the operator.
	Channel string
	// ManifestURL overrides DefaultManifestURL (air-gapped mirrors, forks).
	ManifestURL string
	// Window restricts automatic application to a daily maintenance window.
	Window Window
	// Interval between checks. Zero → 24h. Values below an hour are raised to
	// an hour: a self-hosted install has no reason to poll harder, and the
	// endpoint is shared by everyone.
	Interval time.Duration
	// StateDir is where the last-seen state is persisted (usually DataDir).
	StateDir string
	// CurrentVersion is the running build (version.Version).
	CurrentVersion string
}

// State is the cached result of the last check, persisted across restarts.
type State struct {
	LastCheck   time.Time `json:"last_check"`
	LastError   string    `json:"last_error,omitempty"`
	LatestSeen  string    `json:"latest_seen,omitempty"`
	NotifiedFor string    `json:"notified_for,omitempty"`
	// LastApplied records a self-update this install performed, for the audit
	// trail the UI shows ("upgraded 0.7.5 → 0.7.6 on …").
	LastApplied     string    `json:"last_applied,omitempty"`
	LastAppliedAt   time.Time `json:"last_applied_at,omitempty"`
	LastApplyError  string    `json:"last_apply_error,omitempty"`
	decisionCache   Decision
	decisionIsFresh bool
}

// New builds a Service. It does not perform any I/O.
func New(cfg Config) *Service {
	if cfg.Interval <= 0 {
		cfg.Interval = 24 * time.Hour
	}
	if cfg.Interval < time.Hour {
		cfg.Interval = time.Hour
	}
	if cfg.Policy == "" {
		cfg.Policy = PolicyManual
	}
	s := &Service{
		cfg:    cfg,
		client: &http.Client{Timeout: 15 * time.Second},
		mode:   DetectInstallMode(),
	}
	s.state = s.loadState()
	return s
}

// Mode exposes the detected install mode (binary vs container).
func (s *Service) Mode() InstallMode { return s.mode }

// Policy exposes the configured policy.
func (s *Service) Policy() Policy { return s.cfg.Policy }

// Enabled reports whether checking is on.
func (s *Service) Enabled() bool { return s.cfg.Enabled && s.cfg.Policy != PolicyOff }

// Check fetches the manifest and recomputes the decision. A network failure is
// recorded, not returned as a fault to the caller's health: an install without
// outbound access is a valid deployment, not a broken one.
func (s *Service) Check(ctx context.Context) (Decision, error) {
	if !s.Enabled() {
		return Decision{Action: ActionNone, Reason: "update checking is disabled"}, nil
	}
	cur, err := ParseVersion(s.cfg.CurrentVersion)
	if err != nil {
		// A dev build ("0.1.0-dev" parses; something unparsable does not) has
		// no meaningful position in the release order.
		s.recordError("current version is not semantic: " + s.cfg.CurrentVersion)
		return Decision{Action: ActionNone, Reason: "current version is not semantic"}, nil
	}
	man, err := Fetch(ctx, s.client, s.cfg.ManifestURL, s.cfg.CurrentVersion)
	if err != nil {
		s.recordError(err.Error())
		slog.Debug("update check failed", "err", err)
		return Decision{Action: ActionNone, Reason: "update check failed"}, err
	}
	d := Decide(Input{
		Current:  cur,
		Manifest: man,
		Policy:   s.cfg.Policy,
		Mode:     s.mode,
		Now:      time.Now(),
		Window:   s.cfg.Window,
	})

	s.mu.Lock()
	s.state.LastCheck = time.Now()
	s.state.LastError = ""
	s.state.LatestSeen = d.Target.Version
	s.state.decisionCache = d
	s.state.decisionIsFresh = true
	notify := d.Action != ActionNone && s.state.NotifiedFor != d.Target.Version
	if notify {
		s.state.NotifiedFor = d.Target.Version
	}
	st := s.state
	s.mu.Unlock()
	s.saveState(st)

	if notify && s.OnNewRelease != nil {
		s.OnNewRelease(d)
	}
	return d, nil
}

// Decision returns the cached decision without touching the network.
func (s *Service) Decision() (Decision, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.state.decisionCache, s.state.decisionIsFresh
}

// State returns a copy of the persisted state (for the admin API).
func (s *Service) State() State {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.state
}

// Run starts the periodic loop until ctx is cancelled. The first check is
// delayed by a short jitter-free grace period so a fleet restarting together
// does not stampede the endpoint at boot.
func (s *Service) Run(ctx context.Context) {
	if !s.Enabled() {
		return
	}
	select {
	case <-time.After(2 * time.Minute):
	case <-ctx.Done():
		return
	}
	for {
		if _, err := s.Check(ctx); err == nil {
			s.maybeAutoApply(ctx)
		}
		select {
		case <-time.After(s.cfg.Interval):
		case <-ctx.Done():
			return
		}
	}
}

// maybeAutoApply applies a decision that is ActionAuto, if the maintenance
// window allows it right now. Anything else waits for a human.
func (s *Service) maybeAutoApply(ctx context.Context) {
	d, ok := s.Decision()
	if !ok || d.Action != ActionAuto {
		return
	}
	if !s.cfg.Window.Allows(time.Now()) {
		slog.Info("update deferred to maintenance window", "target", d.Target.Version, "window", s.cfg.Window)
		return
	}
	slog.Info("applying update automatically", "target", d.Target.Version, "reason", d.Reason)
	if err := s.Apply(ctx, d.Target); err != nil {
		slog.Error("automatic update failed", "target", d.Target.Version, "err", err)
	}
}

// ── state persistence ───────────────────────────────────────────────────────

func (s *Service) statePath() string {
	dir := s.cfg.StateDir
	if dir == "" {
		return ""
	}
	return filepath.Join(dir, "update-state.json")
}

func (s *Service) loadState() State {
	p := s.statePath()
	if p == "" {
		return State{}
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return State{}
	}
	var st State
	if err := json.Unmarshal(b, &st); err != nil {
		return State{}
	}
	return st
}

func (s *Service) saveState(st State) {
	p := s.statePath()
	if p == "" {
		return
	}
	b, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return
	}
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return
	}
	_ = os.Rename(tmp, p)
}

func (s *Service) recordError(msg string) {
	s.mu.Lock()
	s.state.LastCheck = time.Now()
	s.state.LastError = msg
	st := s.state
	s.mu.Unlock()
	s.saveState(st)
}
