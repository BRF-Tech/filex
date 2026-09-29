// Package pluginreq is the "leave a request" half of installing a plugin.
//
// # The rule (owner, 2026-09-28)
//
// An API key — an agent, a script, the CLI — may not install, upgrade, remove
// or re-permission a plugin. Those need an administrator signed in to the
// admin panel (handlers.requireSession). What a key CAN do is leave a
// REQUEST: which plugin, from where, and why. An administrator reads it on the
// Plugins page and approves or rejects it.
//
// The design principle it protects: "sha256 protects the administrator, the
// sandbox protects the user". The security boundary for an app is the
// administrator's permission review; before this, a key could copy the
// manifest's permission list into `permissions` and pass that review itself.
//
// # What a request freezes
//
// A request is resolved when it is made — the install review's dry run — and
// what the source answered is kept on the row: the manifest (an app's
// filex-app.json, a storage plugin's filex-storage.json feed), the hash the
// bytes must have, and the permissions it asks to grant. The administrator
// approves THAT. Approval fetches the source again and installs only when it
// still answers those bytes; otherwise the request becomes `superseded`,
// nothing is installed, and the requester can ask again.
//
// One pending request per source: asking twice answers the first request.
// A pending request expires after the TTL (14 days by default,
// FILEX_PLUGIN_REQUEST_TTL_DAYS).
//
// Every step writes an audit row: created, approved, rejected, expired,
// superseded.
package pluginreq

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/notify"
	"github.com/brf-tech/filex/backend/internal/plugin"
	"github.com/brf-tech/filex/backend/internal/wasmplugin"
)

// DefaultTTL is how long a request waits for a decision.
const DefaultTTL = 14 * 24 * time.Hour

// maxReason caps the requester's words; the rest is cut, not refused.
const maxReason = 2000

// Source kinds (plugin_requests.source_kind).
const (
	SourceGitHub     = "github"
	SourceURL        = "url"
	SourceFeed       = "source"
	SourceFromSource = "from_source"
)

// The audit actions a request writes. Constants, so the admin panel's audit
// labels are held to them (web/tests/lib/auditLabel.test.ts reads them).
const (
	AuditActionPluginRequestCreate    = "plugin_request.create"
	AuditActionPluginRequestApprove   = "plugin_request.approve"
	AuditActionPluginRequestReject    = "plugin_request.reject"
	AuditActionPluginRequestExpire    = "plugin_request.expire"
	AuditActionPluginRequestSupersede = "plugin_request.supersede"
	auditTargetType                   = "plugin_request"
)

// Source is where a request's plugin comes from — the install endpoints'
// own shapes, flattened into the request body.
type Source struct {
	// An app from a GitHub repository (+ ref; default main, then master).
	GitHubRepo string `json:"github_repo,omitempty"`
	Ref        string `json:"ref,omitempty"`
	// An app from explicit addresses (the module's, the manifest's), or a
	// storage plugin's binary by address. SHA256 pins the bytes.
	URL         string `json:"url,omitempty"`
	ManifestURL string `json:"manifest_url,omitempty"`
	SHA256      string `json:"sha256,omitempty"`
	Signature   string `json:"signature,omitempty"`
	// A storage plugin's source: `owner/name` or a filex-storage.json address.
	Source string `json:"source,omitempty"`
	// An upgrade from the installed plugin's own source (the default for an
	// upgrade that names no other).
	FromSource bool `json:"from_source,omitempty"`
}

// CreateInput is one request as the caller asks for it.
type CreateInput struct {
	Kind string `json:"kind"`
	Op   string `json:"op"`
	// Name: a storage plugin's name (install), or the installed plugin an
	// upgrade replaces (either kind; PluginID works too).
	Name     string `json:"name"`
	PluginID int64  `json:"plugin_id"`
	Reason   string `json:"reason"`
	Source
}

// Actor is who is asking or deciding.
type Actor struct {
	UserID     *int64
	Name       string
	TokenID    *int64
	TokenLabel string
	IP         string
}

// Error is a refusal the HTTP layer answers as it is.
type Error struct {
	Status  int
	Code    string
	Message string
}

func (e *Error) Error() string { return e.Code + ": " + e.Message }

func refuse(status int, code, format string, a ...any) *Error {
	return &Error{Status: status, Code: code, Message: fmt.Sprintf(format, a...)}
}

// Options wire a Service.
type Options struct {
	Store db.Store
	// Apps is nil when app plugins are off; Plugins nil when storage
	// plugins are off. A request of that kind is then refused (503).
	Apps    *wasmplugin.Registry
	Plugins *plugin.Manager
	// Notify tells the administrators a request is waiting (nil: nobody).
	Notify notify.Service
	TTL    time.Duration
	Now    func() time.Time
	Log    *slog.Logger
}

// Service keeps the requests.
type Service struct {
	o Options

	// mu makes "is one pending for this source? no → create" one step.
	mu sync.Mutex
	// busy holds the requests an approval is working on: a second approval,
	// a rejection or the expiry sweep leaves them alone until it is done.
	busyMu sync.Mutex
	busy   map[int64]bool
}

// New builds a Service.
func New(o Options) *Service {
	if o.TTL <= 0 {
		o.TTL = DefaultTTL
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Log == nil {
		o.Log = slog.Default()
	}
	return &Service{o: o, busy: map[int64]bool{}}
}

// TTL is how long a request waits.
func (s *Service) TTL() time.Duration { return s.o.TTL }

func (s *Service) now() time.Time { return s.o.Now().UTC() }

// ── Create ─────────────────────────────────────────────────────────────

// Create resolves a request's source (the dry run), freezes what it answered
// and records the request — or, when one is already pending for the same
// source, answers that one (created=false) and records nothing.
func (s *Service) Create(ctx context.Context, in CreateInput, who Actor) (*model.PluginRequest, bool, error) {
	s.ExpireDue(ctx)
	in.Kind = strings.ToLower(strings.TrimSpace(in.Kind))
	in.Op = strings.ToLower(strings.TrimSpace(in.Op))
	if in.Op == "" {
		in.Op = model.PluginRequestOpInstall
	}
	in.Name = strings.TrimSpace(in.Name)
	in.Reason = strings.TrimSpace(in.Reason)
	if len([]rune(in.Reason)) > maxReason {
		in.Reason = string([]rune(in.Reason)[:maxReason])
	}
	if in.Op != model.PluginRequestOpInstall && in.Op != model.PluginRequestOpUpgrade {
		return nil, false, refuse(http.StatusBadRequest, "bad_request", "op must be install or upgrade")
	}
	if in.Reason == "" {
		return nil, false, refuse(http.StatusBadRequest, "reason_required",
			"say why the plugin is needed (`reason`): the administrator who approves it reads it")
	}

	// ⚠ The source is written one way and the dedup key taken BEFORE anything
	// is fetched: asking again for a source that already waits answers the
	// waiting request without reaching the network, and without the answer
	// depending on what the source says today.
	var p *plan
	var err error
	switch in.Kind {
	case model.PluginRequestKindApp:
		if s.o.Apps == nil {
			return nil, false, refuse(http.StatusServiceUnavailable, "app_plugins_disabled", "app plugins are disabled on this instance")
		}
		p, err = s.planApp(in)
	case model.PluginRequestKindStorage:
		if s.o.Plugins == nil {
			return nil, false, refuse(http.StatusServiceUnavailable, "plugins_disabled", "storage plugins are disabled on this instance (FILEX_PLUGINS_DISABLED)")
		}
		p, err = s.planStorage(ctx, in)
	default:
		return nil, false, refuse(http.StatusBadRequest, "bad_request", "kind must be app or storage")
	}
	if err != nil {
		return nil, false, err
	}
	if prev, err := s.pending(ctx, p.key); prev != nil || err != nil {
		return prev, false, err
	}

	var row *model.PluginRequest
	if in.Kind == model.PluginRequestKindApp {
		row, err = s.resolveApp(ctx, p)
	} else {
		row, err = s.resolveStorage(ctx, p)
	}
	if err != nil {
		return nil, false, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	// Again under the lock: two requests for one source that arrived together
	// both got past the first look.
	if prev, err := s.pending(ctx, p.key); prev != nil || err != nil {
		return prev, false, err
	}
	row.Reason = in.Reason
	row.RequestedBy = who.UserID
	row.Requester = who.Name
	row.TokenID = who.TokenID
	row.TokenLabel = who.TokenLabel
	row.ExpiresAt = s.now().Add(s.o.TTL)
	created, err := s.o.Store.CreatePluginRequest(ctx, row)
	if err != nil {
		return nil, false, err
	}
	s.audit(ctx, AuditActionPluginRequestCreate, created, who, nil)
	s.announce(ctx, created)
	return created, true, nil
}

// pending answers the request waiting for a source key, nil when none is.
func (s *Service) pending(ctx context.Context, key string) (*model.PluginRequest, error) {
	prev, err := s.o.Store.PendingPluginRequestBySource(ctx, key)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return prev, err
}

// plan is a request before it reaches the network: its source written one
// way, the installed plugin an upgrade replaces, and the dedup key.
type plan struct {
	in   CreateInput
	kind string // source kind
	src  Source
	// app: the installed app an upgrade replaces.
	app *wasmplugin.Installed
	// storage: the installed plugin an upgrade replaces.
	plugin *model.Plugin
	// pluginID is either one's id (0 for an install).
	pluginID int64
	key      string
}

// sourceKey is the dedup key of a request: its kind, its operation, the
// installed plugin an upgrade replaces, and its source spelled one way.
func sourceKey(kind, op string, pluginID int64, sourceKind string, src Source) string {
	norm := struct {
		Kind, Op, SourceKind string
		PluginID             int64
		Repo, Ref            string
		URL, ManifestURL     string
		Feed                 string
	}{kind, op, sourceKind, pluginID, strings.ToLower(src.GitHubRepo), src.Ref, src.URL, src.ManifestURL, strings.ToLower(src.Source)}
	b, _ := json.Marshal(norm)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

func jsonString(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	return string(b)
}

func sha256Hex(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// normalizeRepo writes a repository the one way FetchGitHub reads it.
func normalizeRepo(r string) string {
	r = strings.TrimSpace(r)
	r = strings.TrimPrefix(strings.TrimPrefix(r, "https://github.com/"), "github.com/")
	r = strings.TrimSuffix(strings.TrimSuffix(r, "/"), ".git")
	return r
}

// appSource decides which of an app's source shapes a request uses.
func appSource(src *Source, upgrade bool) (string, error) {
	src.GitHubRepo = normalizeRepo(src.GitHubRepo)
	src.Ref = strings.TrimSpace(src.Ref)
	src.URL = strings.TrimSpace(src.URL)
	src.ManifestURL = strings.TrimSpace(src.ManifestURL)
	src.SHA256 = strings.ToLower(strings.TrimSpace(src.SHA256))
	src.Source = ""
	switch {
	case src.GitHubRepo != "":
		src.URL, src.ManifestURL, src.FromSource = "", "", false
		return SourceGitHub, nil
	case src.ManifestURL != "" || src.URL != "":
		src.FromSource = false
		return SourceURL, nil
	case upgrade:
		src.FromSource = true
		return SourceFromSource, nil
	}
	return "", refuse(http.StatusBadRequest, "bad_request",
		"name the app's source: github_repo (+ ref), or manifest_url (+ url and sha256 for a module)")
}

// fetchApp reads an app's source into an install input (nothing installed).
func (s *Service) fetchApp(ctx context.Context, kind string, src Source, upgradeID int64) (*wasmplugin.InstallInput, error) {
	switch kind {
	case SourceGitHub:
		return s.o.Apps.FetchGitHub(ctx, wasmplugin.GitHubInput{Repo: src.GitHubRepo, Ref: src.Ref})
	case SourceURL:
		return s.o.Apps.FetchURL(ctx, wasmplugin.URLInput{URL: src.URL, ManifestURL: src.ManifestURL, SHA256: src.SHA256, Signature: src.Signature})
	case SourceFromSource:
		return s.o.Apps.FetchUpdate(ctx, upgradeID)
	}
	return nil, refuse(http.StatusBadRequest, "bad_request", "unknown source")
}

// installedApp finds the app an upgrade request names.
func (s *Service) installedApp(in CreateInput) (*wasmplugin.Installed, error) {
	if in.PluginID > 0 {
		if p, ok := s.o.Apps.ByID(in.PluginID); ok {
			return p, nil
		}
	} else if in.Name != "" {
		if p, ok := s.o.Apps.ByName(in.Name); ok {
			return p, nil
		}
	} else {
		return nil, refuse(http.StatusBadRequest, "bad_request", "an upgrade names the installed app: plugin_id or name")
	}
	return nil, refuse(http.StatusNotFound, "not_found", "no such app is installed")
}

// planApp writes an app request's source one way and finds the app an
// upgrade replaces. Nothing is fetched.
func (s *Service) planApp(in CreateInput) (*plan, error) {
	upgrade := in.Op == model.PluginRequestOpUpgrade
	p := &plan{in: in, src: in.Source}
	var err error
	if p.kind, err = appSource(&p.src, upgrade); err != nil {
		return nil, err
	}
	if upgrade {
		if p.app, err = s.installedApp(in); err != nil {
			return nil, err
		}
		p.pluginID = p.app.Row.ID
	}
	p.key = sourceKey(model.PluginRequestKindApp, in.Op, p.pluginID, p.kind, p.src)
	return p, nil
}

// resolveApp runs the install review against the source and freezes what it
// answered.
func (s *Service) resolveApp(ctx context.Context, p *plan) (*model.PluginRequest, error) {
	upgrade := p.app != nil
	fetched, err := s.fetchApp(ctx, p.kind, p.src, p.pluginID)
	if err != nil {
		return nil, err
	}
	manifest := append([]byte(nil), fetched.Manifest...)
	fetched.DryRun = true
	fetched.Lang = "en"
	var dry *wasmplugin.DryRunAnswer
	if upgrade {
		_, dry, err = s.o.Apps.Upgrade(ctx, p.pluginID, fetched)
	} else {
		_, dry, err = s.o.Apps.Install(ctx, fetched)
	}
	if err != nil {
		return nil, err
	}
	if dry == nil || dry.Manifest == nil {
		return nil, errors.New("pluginreq: the dry run answered no review")
	}
	if !upgrade && dry.Installed != nil {
		return nil, refuse(http.StatusConflict, "already_installed",
			"%s is already installed (version %s): request an upgrade instead (op: upgrade, name: %s)",
			dry.Manifest.Name, dry.Installed.Version, dry.Manifest.Name)
	}
	if dry.Compat != nil && !dry.Compat.OK {
		return nil, refuse(http.StatusConflict, "incompatible",
			"%s %s works with filex %s; this is filex %s", dry.Manifest.Name, dry.Manifest.Version, dry.Compat.Requires, dry.Compat.Filex)
	}
	perms := make([]string, 0, len(dry.Permissions))
	for _, perm := range dry.Permissions {
		perms = append(perms, perm.ID)
	}
	sum := dry.WasmSHA256
	if sum == "" {
		sum = dry.ManifestSHA256
	}
	row := &model.PluginRequest{
		Kind: model.PluginRequestKindApp, Op: p.in.Op, Name: dry.Manifest.Name,
		SourceKind: p.kind, SourceJSON: jsonString(p.src), SourceKey: p.key,
		Version: dry.Manifest.Version, ManifestJSON: string(manifest), ReviewJSON: jsonString(reviewOf(dry)),
		SHA256: sum, ManifestSHA256: sha256Hex(manifest), PermissionsJSON: jsonString(perms),
	}
	if upgrade {
		id := p.app.Row.ID
		row.PluginID = &id
		row.FromVersion = p.app.Row.Version
	}
	return row, nil
}

// reviewOf is the dry run as a request keeps it: everything the install
// review shows, without a language pack's translations (`ui_locales`, up to
// megabytes, already in the frozen manifest; the review keeps each
// language's coverage row).
func reviewOf(dry *wasmplugin.DryRunAnswer) *wasmplugin.DryRunAnswer {
	out := *dry
	if dry.Manifest != nil {
		m := *dry.Manifest
		m.UILocales = nil
		out.Manifest = &m
	}
	return &out
}

// storageReview is what a storage request's review shows: the build a
// source publishes, or the binary an address serves.
type storageReview struct {
	Build   *plugin.SourceBuild `json:"build,omitempty"`
	URL     string              `json:"url,omitempty"`
	SHA256  string              `json:"sha256"`
	Bytes   int64               `json:"bytes,omitempty"`
	Version string              `json:"version,omitempty"`
}

// planStorage writes a storage request's source one way and finds the
// plugin an upgrade replaces. Nothing is fetched.
func (s *Service) planStorage(ctx context.Context, in CreateInput) (*plan, error) {
	p := &plan{in: in, src: in.Source}
	p.src.GitHubRepo, p.src.Ref, p.src.ManifestURL = "", "", ""
	p.src.Source = strings.TrimSpace(p.src.Source)
	p.src.URL = strings.TrimSpace(p.src.URL)
	p.src.SHA256 = strings.ToLower(strings.TrimSpace(p.src.SHA256))
	if in.Op == model.PluginRequestOpUpgrade {
		var (
			row *model.Plugin
			err error
		)
		switch {
		case in.PluginID > 0:
			row, err = s.o.Store.GetPlugin(ctx, in.PluginID)
		case in.Name != "":
			row, err = s.o.Store.GetPluginByName(ctx, in.Name)
		default:
			return nil, refuse(http.StatusBadRequest, "bad_request", "an upgrade names the installed storage plugin: plugin_id or name")
		}
		if err != nil || row == nil {
			return nil, refuse(http.StatusNotFound, "not_found", "no such storage plugin is installed")
		}
		if p.src.Source != "" || p.src.URL != "" {
			return nil, refuse(http.StatusBadRequest, "bad_request",
				"a storage plugin is upgraded from the source it follows; name it on the plugin, then request the upgrade")
		}
		p.plugin, p.pluginID, p.kind = row, row.ID, SourceFromSource
		p.src = Source{FromSource: true}
		p.key = sourceKey(model.PluginRequestKindStorage, in.Op, p.pluginID, p.kind, p.src)
		return p, nil
	}
	switch {
	case p.src.Source != "":
		p.src.URL, p.src.SHA256, p.src.Signature, p.src.FromSource = "", "", "", false
		p.kind = SourceFeed
	case p.src.URL != "":
		p.src.FromSource = false
		p.kind = SourceURL
	default:
		return nil, refuse(http.StatusBadRequest, "bad_request",
			"name the storage plugin's source: source (owner/name or a filex-storage.json address), or url (+ sha256)")
	}
	// The name is not part of the key: one source is one plugin, whatever
	// name somebody wants it under.
	p.key = sourceKey(model.PluginRequestKindStorage, in.Op, 0, p.kind, p.src)
	return p, nil
}

// resolveStorage reads a storage plugin's source (its feed, or the binary an
// address serves — downloaded and hashed, never run) and freezes the build.
func (s *Service) resolveStorage(ctx context.Context, p *plan) (*model.PluginRequest, error) {
	if p.plugin != nil {
		return s.resolveStorageUpgrade(ctx, p)
	}
	name := p.in.Name
	var review storageReview
	switch p.kind {
	case SourceFeed:
		build, err := s.o.Plugins.ResolveSource(ctx, p.src.Source)
		if err != nil {
			return nil, err
		}
		review = storageReview{Build: build, SHA256: build.Binary.SHA256, Version: build.Feed.Version}
		if name == "" {
			name = strings.TrimSpace(build.Feed.Name)
		}
	case SourceURL:
		sum, n, err := s.o.Plugins.HashURL(ctx, p.src.URL)
		if err != nil {
			return nil, err
		}
		if p.src.SHA256 != "" && p.src.SHA256 != sum {
			return nil, refuse(http.StatusBadRequest, "sha256_mismatch",
				"the address serves %s…, not the %s… this request names", sum[:12], p.src.SHA256[:min(12, len(p.src.SHA256))])
		}
		review = storageReview{URL: p.src.URL, SHA256: sum, Bytes: n}
	}
	if !plugin.ValidName(name) {
		return nil, refuse(http.StatusBadRequest, "bad_request", "name the storage plugin: %s", plugin.ErrBadName.Error())
	}
	if _, err := s.o.Store.GetPluginByName(ctx, name); err == nil {
		return nil, refuse(http.StatusConflict, "already_installed",
			"a storage plugin named %s is already installed: request an upgrade instead (op: upgrade, name: %s)", name, name)
	}
	manifest := ""
	if review.Build != nil {
		manifest = jsonString(review.Build.Feed)
	}
	return &model.PluginRequest{
		Kind: model.PluginRequestKindStorage, Op: model.PluginRequestOpInstall, Name: name,
		SourceKind: p.kind, SourceJSON: jsonString(p.src), SourceKey: p.key,
		Version: review.Version, ManifestJSON: manifest, ReviewJSON: jsonString(review),
		SHA256: review.SHA256, PermissionsJSON: "[]",
	}, nil
}

func (s *Service) resolveStorageUpgrade(ctx context.Context, p *plan) (*model.PluginRequest, error) {
	build, current, err := s.o.Plugins.ResolveUpdate(ctx, p.pluginID)
	if err != nil {
		return nil, err
	}
	review := storageReview{Build: build, SHA256: build.Binary.SHA256, Version: build.Feed.Version}
	id := current.ID
	return &model.PluginRequest{
		Kind: model.PluginRequestKindStorage, Op: model.PluginRequestOpUpgrade, Name: current.Name, PluginID: &id,
		SourceKind: SourceFromSource, SourceJSON: jsonString(p.src), SourceKey: p.key,
		Version: strings.TrimPrefix(strings.TrimSpace(build.Feed.Version), "v"), FromVersion: current.Version,
		ManifestJSON: jsonString(build.Feed), ReviewJSON: jsonString(review),
		SHA256: build.Binary.SHA256, PermissionsJSON: "[]",
	}, nil
}

// ── Read ───────────────────────────────────────────────────────────────

// List answers the requests in one state ("" = every state), newest first.
func (s *Service) List(ctx context.Context, status string) ([]*model.PluginRequest, error) {
	s.ExpireDue(ctx)
	switch status {
	case "", "all":
		status = ""
	case model.PluginRequestPending, model.PluginRequestApproved, model.PluginRequestRejected,
		model.PluginRequestExpired, model.PluginRequestSuperseded:
	default:
		return nil, refuse(http.StatusBadRequest, "bad_request", "status must be pending, approved, rejected, expired, superseded or all")
	}
	return s.o.Store.ListPluginRequests(ctx, status, 200)
}

// Get answers one request.
func (s *Service) Get(ctx context.Context, id int64) (*model.PluginRequest, error) {
	s.ExpireDue(ctx)
	r, err := s.o.Store.GetPluginRequest(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, refuse(http.StatusNotFound, "not_found", "no such plugin request")
	}
	return r, err
}

// ── Decide ─────────────────────────────────────────────────────────────

// claim marks a request as being worked on; false when it already is.
func (s *Service) claim(id int64) bool {
	s.busyMu.Lock()
	defer s.busyMu.Unlock()
	if s.busy[id] {
		return false
	}
	s.busy[id] = true
	return true
}

func (s *Service) release(id int64) {
	s.busyMu.Lock()
	delete(s.busy, id)
	s.busyMu.Unlock()
}

func (s *Service) isBusy(id int64) bool {
	s.busyMu.Lock()
	defer s.busyMu.Unlock()
	return s.busy[id]
}

// pendingOrRefuse reads a request that must still be pending.
func (s *Service) pendingOrRefuse(ctx context.Context, id int64) (*model.PluginRequest, error) {
	r, err := s.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if r.Status != model.PluginRequestPending {
		return r, refuse(http.StatusConflict, "not_pending", "this request is already %s", r.Status)
	}
	return r, nil
}

// Superseded is Approve's answer when the source no longer serves the bytes
// the request froze: the request is closed as superseded, nothing installed.
type Superseded struct{ Why string }

func (e *Superseded) Error() string { return "superseded: " + e.Why }

// Approve installs what the request froze, and only that. It answers the
// closed request and the installed plugin's status; *Superseded when the
// source changed (the request is closed as superseded); any other failure
// leaves the request pending, with the refusal kept on it.
func (s *Service) Approve(ctx context.Context, id int64, who Actor, lang string) (*model.PluginRequest, any, error) {
	if !s.claim(id) {
		return nil, nil, refuse(http.StatusConflict, "busy", "this request is being approved right now")
	}
	defer s.release(id)
	r, err := s.pendingOrRefuse(ctx, id)
	if err != nil {
		return r, nil, err
	}
	// ⚠ Detached from the request, like the install endpoints: an approval
	// downloads and installs, and an administrator closing the tab must not
	// cut it in half.
	ctx = context.WithoutCancel(ctx)
	var (
		result any
		why    string
	)
	switch r.Kind {
	case model.PluginRequestKindApp:
		result, why, err = s.approveApp(ctx, r, who, lang)
	case model.PluginRequestKindStorage:
		result, why, err = s.approveStorage(ctx, r)
	default:
		err = refuse(http.StatusInternalServerError, "bad_request", "unknown kind %q", r.Kind)
	}
	if why != "" {
		closed, cerr := s.supersede(ctx, r, who, why)
		if cerr != nil {
			return r, nil, cerr
		}
		return closed, nil, &Superseded{Why: why}
	}
	if err != nil {
		// Still pending: the administrator may try again (a network failure)
		// or reject it. The refusal is kept so the page can say it.
		r.ResultJSON = jsonString(map[string]any{"error": err.Error(), "at": s.now()})
		if _, uerr := s.o.Store.UpdatePluginRequest(ctx, r, true); uerr != nil {
			s.o.Log.Warn("pluginreq: could not record a failed approval", slog.Int64("request", r.ID), slog.Any("err", uerr))
		}
		return r, nil, err
	}
	now := s.now()
	r.Status = model.PluginRequestApproved
	r.DecidedBy = who.UserID
	r.Decider = who.Name
	r.DecidedAt = &now
	r.DecisionNote = ""
	r.ResultJSON = jsonString(result)
	if _, err := s.o.Store.UpdatePluginRequest(ctx, r, true); err != nil {
		return r, result, err
	}
	s.audit(ctx, AuditActionPluginRequestApprove, r, who, nil)
	return r, result, nil
}

// permissionsOf reads a request's frozen permission list.
func permissionsOf(r *model.PluginRequest) []string {
	var perms []string
	_ = json.Unmarshal([]byte(r.PermissionsJSON), &perms)
	if perms == nil {
		perms = []string{}
	}
	return perms
}

// SourceOf reads a request's source.
func SourceOf(r *model.PluginRequest) Source {
	var src Source
	_ = json.Unmarshal([]byte(r.SourceJSON), &src)
	return src
}

// approveApp answers (the installed app's status, "", nil), or ("", why, nil)
// when the source changed, or (nil, "", err) for any other failure.
func (s *Service) approveApp(ctx context.Context, r *model.PluginRequest, who Actor, lang string) (any, string, error) {
	if s.o.Apps == nil {
		return nil, "", refuse(http.StatusServiceUnavailable, "app_plugins_disabled", "app plugins are disabled on this instance")
	}
	upgrade := r.Op == model.PluginRequestOpUpgrade
	var upgradeID int64
	if upgrade {
		if r.PluginID == nil {
			return nil, "", refuse(http.StatusInternalServerError, "bad_request", "an upgrade request names no app")
		}
		upgradeID = *r.PluginID
		p, ok := s.o.Apps.ByID(upgradeID)
		if !ok {
			return nil, "the app it upgrades is no longer installed", nil
		}
		if p.Row.Version != r.FromVersion {
			return nil, fmt.Sprintf("the app is at %s now, not the %s this request would replace", p.Row.Version, r.FromVersion), nil
		}
	}
	in, err := s.fetchApp(ctx, r.SourceKind, SourceOf(r), upgradeID)
	if err != nil {
		var ie *wasmplugin.InstallError
		if errors.As(err, &ie) && (ie.Code == wasmplugin.ErrCodeUpToDate || ie.Code == wasmplugin.ErrCodeIncompatible || ie.Code == wasmplugin.ErrCodeNotFound) {
			return nil, "the source no longer offers the version this request froze: " + ie.Message, nil
		}
		return nil, "", err
	}
	if got := sha256Hex(in.Manifest); got != r.ManifestSHA256 {
		return nil, fmt.Sprintf("the source now serves a different manifest (sha256 %s…, the request froze %s…)", short(got), short(r.ManifestSHA256)), nil
	}
	in.SHA256 = r.SHA256
	in.Granted = permissionsOf(r)
	in.Lang = lang
	in.ActorID = who.UserID
	in.DryRun = false
	var st *wasmplugin.Status
	if upgrade {
		st, _, err = s.o.Apps.Upgrade(ctx, upgradeID, in)
	} else {
		st, _, err = s.o.Apps.Install(ctx, in)
	}
	if err != nil {
		var ie *wasmplugin.InstallError
		if errors.As(err, &ie) && ie.Code == wasmplugin.ErrCodeSHA256Mismatch {
			return nil, "the source now serves different bytes: " + ie.Message, nil
		}
		return nil, "", err
	}
	return st, "", nil
}

// approveStorage is approveApp for a storage plugin.
func (s *Service) approveStorage(ctx context.Context, r *model.PluginRequest) (any, string, error) {
	if s.o.Plugins == nil {
		return nil, "", refuse(http.StatusServiceUnavailable, "plugins_disabled", "storage plugins are disabled on this instance (FILEX_PLUGINS_DISABLED)")
	}
	src := SourceOf(r)
	var (
		st  *plugin.Status
		err error
	)
	switch {
	case r.Op == model.PluginRequestOpUpgrade:
		if r.PluginID == nil {
			return nil, "", refuse(http.StatusInternalServerError, "bad_request", "an upgrade request names no plugin")
		}
		row, gerr := s.o.Store.GetPlugin(ctx, *r.PluginID)
		if gerr != nil {
			return nil, "the storage plugin it upgrades is no longer installed", nil
		}
		if row.Version != r.FromVersion {
			return nil, fmt.Sprintf("the plugin is at %s now, not the %s this request would replace", row.Version, r.FromVersion), nil
		}
		build, _, rerr := s.o.Plugins.ResolveUpdate(ctx, row.ID)
		if rerr != nil {
			var rej plugin.RejectedError
			if errors.As(rerr, &rej) {
				return nil, "the source no longer offers the version this request froze: " + rerr.Error(), nil
			}
			return nil, "", rerr
		}
		if build.Binary.SHA256 != r.SHA256 {
			return nil, fmt.Sprintf("the source now publishes %s %s (sha256 %s…), not the build this request froze (%s…)",
				r.Name, build.Feed.Version, short(build.Binary.SHA256), short(r.SHA256)), nil
		}
		st, err = s.o.Plugins.UpgradeFromSourcePinned(ctx, row.ID, r.SHA256)
	case r.SourceKind == SourceFeed:
		build, rerr := s.o.Plugins.ResolveSource(ctx, src.Source)
		if rerr != nil {
			var rej plugin.RejectedError
			if errors.As(rerr, &rej) {
				return nil, "the source no longer offers the build this request froze: " + rerr.Error(), nil
			}
			return nil, "", rerr
		}
		if build.Binary.SHA256 != r.SHA256 {
			return nil, fmt.Sprintf("the source now publishes %s (sha256 %s…), not the build this request froze (%s…)",
				build.Feed.Version, short(build.Binary.SHA256), short(r.SHA256)), nil
		}
		st, err = s.o.Plugins.InstallFromSourcePinned(ctx, r.Name, src.Source, r.SHA256)
	case r.SourceKind == SourceURL:
		st, err = s.o.Plugins.InstallFromURL(ctx, r.Name, src.URL, r.SHA256, src.Signature)
	default:
		return nil, "", refuse(http.StatusInternalServerError, "bad_request", "unknown source %q", r.SourceKind)
	}
	if err != nil {
		if errors.Is(err, plugin.ErrSHA256Mismatch) || errors.Is(err, plugin.ErrSourceChanged) {
			return nil, "the source now serves different bytes: " + err.Error(), nil
		}
		return nil, "", err
	}
	return st, "", nil
}

func short(s string) string {
	if len(s) > 12 {
		return s[:12]
	}
	return s
}

// supersede closes a request whose source changed.
func (s *Service) supersede(ctx context.Context, r *model.PluginRequest, who Actor, why string) (*model.PluginRequest, error) {
	now := s.now()
	r.Status = model.PluginRequestSuperseded
	r.DecidedBy = who.UserID
	r.Decider = who.Name
	r.DecidedAt = &now
	r.DecisionNote = why
	ok, err := s.o.Store.UpdatePluginRequest(ctx, r, true)
	if err != nil {
		return r, err
	}
	if ok {
		s.audit(ctx, AuditActionPluginRequestSupersede, r, who, map[string]any{"why": why})
	}
	return r, nil
}

// Reject closes a request without installing anything; note is optional.
func (s *Service) Reject(ctx context.Context, id int64, who Actor, note string) (*model.PluginRequest, error) {
	if !s.claim(id) {
		return nil, refuse(http.StatusConflict, "busy", "this request is being approved right now")
	}
	defer s.release(id)
	r, err := s.pendingOrRefuse(ctx, id)
	if err != nil {
		return r, err
	}
	note = strings.TrimSpace(note)
	if len([]rune(note)) > maxReason {
		note = string([]rune(note)[:maxReason])
	}
	now := s.now()
	r.Status = model.PluginRequestRejected
	r.DecidedBy = who.UserID
	r.Decider = who.Name
	r.DecidedAt = &now
	r.DecisionNote = note
	ok, err := s.o.Store.UpdatePluginRequest(ctx, r, true)
	if err != nil {
		return r, err
	}
	if !ok {
		return r, refuse(http.StatusConflict, "not_pending", "this request was closed meanwhile")
	}
	s.audit(ctx, AuditActionPluginRequestReject, r, who, map[string]any{"note": note})
	return r, nil
}

// ── Expiry ─────────────────────────────────────────────────────────────

// ExpireDue closes every pending request past its expiry and answers how
// many. It runs before every read and write here, and hourly (StartSweeper),
// so a request is never shown as waiting after its time.
func (s *Service) ExpireDue(ctx context.Context) int {
	rows, err := s.o.Store.ListPluginRequests(ctx, model.PluginRequestPending, 500)
	if err != nil {
		s.o.Log.Warn("pluginreq: could not read the pending requests", slog.Any("err", err))
		return 0
	}
	now := s.now()
	n := 0
	for _, r := range rows {
		if r.ExpiresAt.After(now) || s.isBusy(r.ID) {
			continue
		}
		at := r.ExpiresAt
		r.Status = model.PluginRequestExpired
		r.DecidedBy = nil
		r.Decider = ""
		r.DecidedAt = &at
		r.DecisionNote = ""
		ok, err := s.o.Store.UpdatePluginRequest(ctx, r, true)
		if err != nil {
			s.o.Log.Warn("pluginreq: could not expire a request", slog.Int64("request", r.ID), slog.Any("err", err))
			continue
		}
		if ok {
			n++
			s.audit(ctx, AuditActionPluginRequestExpire, r, Actor{}, nil)
		}
	}
	return n
}

// StartSweeper expires overdue requests every hour until ctx ends.
func (s *Service) StartSweeper(ctx context.Context) {
	go func() {
		t := time.NewTicker(time.Hour)
		defer t.Stop()
		s.ExpireDue(ctx)
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				s.ExpireDue(ctx)
			}
		}
	}()
}

// ── Audit + announcement ───────────────────────────────────────────────

func (s *Service) audit(ctx context.Context, action string, r *model.PluginRequest, who Actor, extra map[string]any) {
	meta := map[string]any{
		"kind": r.Kind, "op": r.Op, "name": r.Name, "version": r.Version, "sha256": r.SHA256,
		"source_kind": r.SourceKind, "status": r.Status, "target_name": r.Name,
	}
	if r.PluginID != nil {
		meta["plugin_id"] = *r.PluginID
	}
	if who.TokenID != nil {
		meta["token_id"] = *who.TokenID
	}
	if who.TokenLabel != "" {
		meta["token_label"] = who.TokenLabel
	}
	for k, v := range extra {
		meta[k] = v
	}
	entry := &model.AuditEntry{
		UserID: who.UserID, Action: action, TargetType: auditTargetType,
		TargetID: fmt.Sprintf("%d", r.ID), Metadata: meta, IP: who.IP, CreatedAt: s.now(),
	}
	actx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if err := s.o.Store.InsertAuditEntry(actx, entry); err != nil {
		s.o.Log.Warn("pluginreq: audit row not written", slog.String("action", action), slog.Any("err", err))
	}
}

// announce tells the administrators a request is waiting: the bell and every
// webhook that carries operator alarms (notify.EventPluginRequested).
func (s *Service) announce(ctx context.Context, r *model.PluginRequest) {
	if s.o.Notify == nil {
		return
	}
	meta := map[string]any{
		"plugin": r.Name, "plugin_label_en": r.Name, "version": r.Version,
		"kind": r.Kind, "op": r.Op, "requester": r.Requester, "request_id": r.ID, "reason": r.Reason,
	}
	if r.Kind == model.PluginRequestKindApp {
		if m, err := wasmplugin.ParseManifest([]byte(r.ManifestJSON)); err == nil {
			for lang, text := range m.Label {
				if strings.TrimSpace(text) != "" {
					meta["plugin_label_"+lang] = text
				}
			}
		}
	}
	verb := "install"
	if r.Op == model.PluginRequestOpUpgrade {
		verb = "upgrade to"
	}
	if _, err := s.o.Notify.Send(context.WithoutCancel(ctx), notify.Event{
		Event: notify.EventPluginRequested, Severity: notify.SeverityInfo,
		Title: fmt.Sprintf("%s asks to %s %s %s", r.Requester, verb, r.Name, r.Version),
		Body:  r.Reason,
		Meta:  meta,
	}); err != nil {
		s.o.Log.Warn("pluginreq: request notice not sent", slog.Int64("request", r.ID), slog.Any("err", err))
	}
}
