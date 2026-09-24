package wasmplugin

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/enginebin"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/plugin"
	"github.com/brf-tech/filex/backend/internal/secretbox"
	"github.com/brf-tech/filex/backend/internal/share"
	"github.com/brf-tech/filex/backend/internal/storage"
	"github.com/brf-tech/filex/backend/pkg/pluginkit/wire"
)

// ── Registry: the installed set ────────────────────────────────────────
//
// One Registry per server. It owns the Runtime, the plugin directory
// (<data-dir>/app-plugins/<name>/{plugin.wasm, filex-app.json}), and one
// Installed per row: the manifest as installed, the GRANT the admin approved,
// the compiled module and its live state. Rows are the intent; Installed is
// what that intent currently amounts to.

// Options configures the registry.
type Options struct {
	Store db.Store
	// Share mints the public links app plugins open. An app's public page IS
	// a share (migration 00046), so it goes through the same service every
	// other link does and inherits the instance's expiry ceiling, the PIN
	// gate and the administrator's revoke. nil refuses share_create.
	Share *share.Service
	// Dir is where modules live; the compilation cache and the call spool
	// sit next to it.
	Dir string
	// SecretKey seals secret settings (secretbox). Empty → secret fields are
	// refused at PutSettings rather than stored in the clear.
	SecretKey string
	// TrustedKeys (hex/base64 ed25519) make a detached signature mandatory.
	TrustedKeys []string
	// Demo refuses every install/upgrade (the admin account is public).
	Demo bool
	// HTTP fetches manifests and modules for URL/GitHub installs.
	HTTP *http.Client
	Log  *slog.Logger
	// StorageResolver opens the driver a job reads from and writes to.
	StorageResolver func(int64) (storage.Driver, error)
	// Limits (bytes). Zero → defaults below.
	MaxWasmBytes   int64
	MaxInputBytes  int64
	MaxOutputBytes int64
	// PerPluginJobs bounds concurrent action_run calls per plugin (default 2).
	PerPluginJobs int
}

const (
	defaultMaxWasm   = 64 << 20
	defaultMaxInput  = 256 << 20
	defaultMaxOutput = 512 << 20
)

// Plugin states.
const (
	StateRunning  = "running"
	StateDisabled = "disabled"
	StateRefused  = "refused"
	StateFailed   = "failed"
)

// Installed is one plugin's live entry.
type Installed struct {
	Row      *model.AppPlugin
	Manifest *Manifest
	Grants   Grants
	// Perms is the grant as a sorted list, for answers.
	Perms []Permission

	mu       sync.RWMutex
	compiled *Compiled
	state    string
	stateErr string
	logs     *logRing
	sem      chan struct{}
	mailRate rateWindow
	signRate minuteWindow
}

func (p *Installed) log(level, msg string) { p.logs.add(level, msg) }

// State returns the live state and its error.
func (p *Installed) State() (string, string) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.state, p.stateErr
}

func (p *Installed) setState(state, err string) {
	p.mu.Lock()
	p.state, p.stateErr = state, err
	p.mu.Unlock()
	if err != "" {
		p.logs.add("error", state+": "+err)
	}
}

// Registry is the installed set plus the runtime.
type Registry struct {
	opts    Options
	rt      *Runtime
	box     *secretbox.Box
	trusted []ed25519.PublicKey
	engines *engineSet
	log     *slog.Logger

	mu     sync.RWMutex
	byID   map[int64]*Installed
	byName map[string]*Installed

	sink OutputSink
	// queue is where scheduled work is handed to the ops worker (schedule.go).
	// nil means a due item fails loudly rather than vanishing.
	queue     JobQueue
	notify    notifySink
	mailer    mailSink
	userScope func(ctx context.Context, u *model.User) context.Context
	// home answers CallContext.Home (SetHomeResolver).
	home func(ctx context.Context, u *model.User) string
	// visible answers "may this person see this file?" for state_list.
	// Nil means every file passes, which is what a single-user instance
	// with no ACL wiring wants.
	visible  func(ctx context.Context, u *model.User, storageID int64, rel string) bool
	outbound http.RoundTripper

	// instanceID names this process on every schedule row it claims, so an
	// operator can tell which node ran a piece of unattended work.
	instanceID string

	publicBase string
	// originFor resolves an absolute link's origin with no request in scope
	// (internal/tenanturl.Resolver.ForStorage). nil falls back to publicBase.
	originFor func(ctx context.Context, storageID int64) string
	signMu    sync.Mutex

	// assetFlights are the asset downloads in progress (assets.go), keyed
	// by app name and sha256, so every call that wants one waits on the same
	// download instead of starting its own.
	assetMu      sync.Mutex
	assetFlights map[string]*assetFlight
	// assetFails remembers the last failure per asset until it is fetched:
	// no new attempt for a minute, and the outage logged once.
	assetFails map[string]*assetFail

	// cat is the interface's string catalogue, for language coverage
	// (langpack.go SetCatalogue). Nil until set: coverage unknown.
	cat catalogueRef
}

// New prepares the registry (no plugin is loaded until Load).
func New(o Options) (*Registry, error) {
	if o.Store == nil {
		return nil, errors.New("wasmplugin: store required")
	}
	if o.Dir == "" {
		return nil, errors.New("wasmplugin: dir required")
	}
	if !ArchSupported() {
		return nil, ErrUnsupportedArch
	}
	if o.Log == nil {
		o.Log = slog.Default()
	}
	if o.HTTP == nil {
		o.HTTP = &http.Client{Timeout: 2 * time.Minute}
	}
	if o.MaxWasmBytes <= 0 {
		o.MaxWasmBytes = defaultMaxWasm
	}
	if o.MaxInputBytes <= 0 {
		o.MaxInputBytes = defaultMaxInput
	}
	if o.MaxOutputBytes <= 0 {
		o.MaxOutputBytes = defaultMaxOutput
	}
	if o.PerPluginJobs <= 0 {
		o.PerPluginJobs = 2
	}
	for _, sub := range []string{"", "cache", "spool", "public"} {
		if err := os.MkdirAll(filepath.Join(o.Dir, sub), 0o700); err != nil {
			return nil, fmt.Errorf("wasmplugin: dir: %w", err)
		}
	}
	// Spool leftovers from a previous run are garbage by definition: every
	// call removes its own directory on the way out.
	if entries, err := os.ReadDir(filepath.Join(o.Dir, "spool")); err == nil {
		for _, e := range entries {
			_ = os.RemoveAll(filepath.Join(o.Dir, "spool", e.Name()))
		}
	}
	rt, err := NewRuntime(filepath.Join(o.Dir, "cache"))
	if err != nil {
		return nil, err
	}
	box, err := secretbox.New(o.SecretKey)
	if err != nil {
		rt.Close(context.Background())
		return nil, fmt.Errorf("wasmplugin: secretbox: %w", err)
	}
	trusted, err := plugin.ParsePublicKeys(o.TrustedKeys)
	if err != nil {
		rt.Close(context.Background())
		return nil, fmt.Errorf("wasmplugin: %w", err)
	}
	return &Registry{
		opts: o, rt: rt, box: box, trusted: trusted, engines: probeEngines(), log: o.Log,
		byID: map[int64]*Installed{}, byName: map[string]*Installed{},
		outbound: newOutboundTransport(), instanceID: newInstanceID(),
	}, nil
}

func (r *Registry) spoolRoot() string { return filepath.Join(r.opts.Dir, "spool") }

// Dir is where modules live.
func (r *Registry) Dir() string { return r.opts.Dir }

// RequiresSignature reports whether installs must carry a signature.
func (r *Registry) RequiresSignature() bool { return len(r.trusted) > 0 }

// Engines is the engine → installed map for this host.
func (r *Registry) Engines() map[string]bool { return r.engines.Available() }

// SetStorageResolver wires (or replaces) the driver resolver jobs read
// through; the server builds it after the registry.
func (r *Registry) SetStorageResolver(fn func(int64) (storage.Driver, error)) {
	r.opts.StorageResolver = fn
}

// SetOutputSink wires the committer that lands job outputs on a storage.
func (r *Registry) SetOutputSink(s OutputSink) { r.sink = s }

// SetHomeResolver wires the answer to "where do this person's results go
// when not beside their source" (CallContext.Home) — the HTTP layer, which
// knows storages, tenants and the ACL.
func (r *Registry) SetHomeResolver(f func(ctx context.Context, u *model.User) string) { r.home = f }

// Close frees every compiled module and the runtime.
func (r *Registry) Close(ctx context.Context) {
	r.mu.Lock()
	for _, p := range r.byID {
		p.mu.Lock()
		if p.compiled != nil {
			_ = p.compiled.Close(ctx)
			p.compiled = nil
		}
		p.mu.Unlock()
	}
	r.byID = map[int64]*Installed{}
	r.byName = map[string]*Installed{}
	r.mu.Unlock()
	_ = r.rt.Close(ctx)
}

// Load reads every row and compiles the enabled ones. A row that fails to
// load is kept (state failed/refused) so the admin sees why; it never stops
// the server.
func (r *Registry) Load(ctx context.Context) error {
	rows, err := r.opts.Store.ListAppPlugins(ctx)
	if err != nil {
		return err
	}
	for _, row := range rows {
		p, err := r.entryFor(row)
		if err != nil {
			r.log.Warn("app-plugins: row unreadable", slog.String("plugin", row.Name), slog.Any("err", err))
			continue
		}
		r.put(p)
		r.upgradeLegacyOverrides(ctx, p)
		if row.Enabled {
			r.compile(ctx, p)
		} else {
			p.setState(StateDisabled, "")
		}
	}
	return nil
}

// upgradeLegacyOverrides rewrites an app's override rows that still hold a
// whole rule (written before applies_override.go) as deltas — once, at
// start, against the manifest the admin saw when saving it: no upgrade of
// the app can have happened in between, because upgrades run through this
// process, after Load. A row that cannot be read is left as it is (and
// still read as a legacy rule by storedDelta).
func (r *Registry) upgradeLegacyOverrides(ctx context.Context, p *Installed) {
	stored, err := r.opts.Store.ListAppPluginOverrides(ctx, p.Row.ID)
	if err != nil || len(stored) == 0 {
		return
	}
	changed := 0
	for _, o := range stored {
		if strings.TrimSpace(o.AppliesJSON) == "" || isDeltaJSON(o.AppliesJSON) {
			continue
		}
		a, ok := p.Manifest.Action(o.ActionID)
		if !ok {
			continue
		}
		var legacy wire.Applies
		if json.Unmarshal([]byte(o.AppliesJSON), &legacy) != nil {
			continue
		}
		o.AppliesJSON = encodeDelta(legacyDelta(a.Applies, manifestEngineExt(a.Applies), legacy))
		changed++
	}
	if changed == 0 {
		return
	}
	if err := r.opts.Store.PutAppPluginOverrides(ctx, p.Row.ID, stored); err != nil {
		r.log.Warn("app-plugins: overrides not converted", slog.String("plugin", p.Row.Name), slog.Any("err", err))
		return
	}
	r.log.Info("app-plugins: overrides stored as changes against the manifest", slog.String("plugin", p.Row.Name), slog.Int("rows", changed))
}

func (r *Registry) put(p *Installed) {
	r.mu.Lock()
	r.byID[p.Row.ID] = p
	r.byName[p.Row.Name] = p
	r.mu.Unlock()
}

func (r *Registry) drop(p *Installed) {
	r.mu.Lock()
	delete(r.byID, p.Row.ID)
	delete(r.byName, p.Row.Name)
	r.mu.Unlock()
}

// entryFor turns a row into an Installed (manifest + grant parsed).
func (r *Registry) entryFor(row *model.AppPlugin) (*Installed, error) {
	m, err := ParseManifest([]byte(row.ManifestJSON))
	if err != nil {
		return nil, err
	}
	var granted []string
	_ = json.Unmarshal([]byte(row.PermissionsJSON), &granted)
	perms := make([]Permission, 0, len(granted))
	for _, g := range granted {
		p, err := ParsePermission(g)
		if err != nil {
			return nil, fmt.Errorf("grant: %w", err)
		}
		perms = append(perms, p)
	}
	sort.Slice(perms, func(i, j int) bool { return perms[i] < perms[j] })
	return &Installed{
		Row: row, Manifest: m, Grants: NewGrants(perms), Perms: perms,
		state: StateDisabled, sem: make(chan struct{}, r.opts.PerPluginJobs), logs: &logRing{},
	}, nil
}

// compile builds the module and runs describe; on any failure the entry is
// marked and left without a compiled module.
//
// ⚠ A language pack (wire.Manifest.IsLanguagePack) has no module: "loading"
// it is switching it on — its languages are served from the manifest while
// the state is running — and no runtime instance is ever started for it.
// Every path that makes an app runnable (Load, Install, Upgrade, SetEnabled)
// comes through here, so this is the one place that has to know.
func (r *Registry) compile(ctx context.Context, p *Installed) {
	if p.Manifest.IsLanguagePack() {
		p.mu.Lock()
		if p.compiled != nil {
			// An app that USED to have a module and was upgraded to a pack.
			_ = p.compiled.Close(ctx)
			p.compiled = nil
		}
		p.mu.Unlock()
		p.setState(StateRunning, "")
		if p.Row.LastError != "" {
			r.persistError(ctx, p, "")
		}
		p.log("info", "language pack "+p.Row.Name+" "+p.Row.Version+" serves "+strings.Join(sortedTags(p.Manifest.UILocales), ", ")+" — no module, nothing runs")
		return
	}
	wasmPath := filepath.Join(r.opts.Dir, p.Row.Name, p.Row.WasmPath)
	c, err := r.rt.Compile(ctx, wasmPath, p.Row.SHA256, p.Manifest, nil, p.log)
	if err != nil {
		p.setState(StateFailed, err.Error())
		r.persistError(ctx, p, err.Error())
		return
	}
	if _, err := c.Describe(ctx, ""); err != nil {
		_ = c.Close(ctx)
		state := StateFailed
		if IsCode(err, CodeRefused) {
			state = StateRefused
		}
		p.setState(state, err.Error())
		r.persistError(ctx, p, err.Error())
		return
	}
	// An app that asks to be woken must have something to wake. Checked
	// here rather than at the first wake-up, because "the module has no
	// tick export" is a packaging mistake the author should learn about at
	// install, not an hour later in a log nobody is reading.
	if err := checkTickExport(ctx, p.Grants, c); err != nil {
		_ = c.Close(ctx)
		p.setState(StateRefused, err.Error())
		r.persistError(ctx, p, err.Error())
		return
	}
	p.mu.Lock()
	if p.compiled != nil {
		_ = p.compiled.Close(ctx)
	}
	p.compiled = c
	p.mu.Unlock()
	p.setState(StateRunning, "")
	if p.Row.LastError != "" {
		r.persistError(ctx, p, "")
	}
	p.log("info", "loaded "+p.Row.Name+" "+p.Row.Version)
	// Arming here covers every way a plugin becomes runnable — load,
	// install, upgrade, enable — with one line instead of four.
	r.armWakeup(ctx, p)
}

// exportProbe is all checkTickExport needs of a compiled module — which is
// what lets the rule be tested without a second wasm fixture whose only
// distinction would be a missing export.
type exportProbe interface {
	HasExport(ctx context.Context, name string) (bool, error)
}

// checkTickExport refuses a module that was granted `schedule` but exports
// no `tick`. Split out so the rule can be read, and tested, on its own.
func checkTickExport(ctx context.Context, g Grants, c exportProbe) error {
	if !g.Has(PermSchedule) {
		return nil
	}
	has, err := c.HasExport(ctx, "tick")
	if err != nil {
		return err
	}
	if !has {
		return &CallError{Code: CodeRefused, Export: "tick",
			Message: "the manifest asks for the schedule permission, but the module has no tick export — an app that cannot be woken must not ask to be"}
	}
	return nil
}

func (r *Registry) persistError(ctx context.Context, p *Installed, msg string) {
	p.Row.LastError = clip(msg, 1000)
	_ = r.opts.Store.UpdateAppPlugin(ctx, p.Row)
}

// ── Lookup ─────────────────────────────────────────────────────────────

// ByName returns the entry for a plugin name.
func (r *Registry) ByName(name string) (*Installed, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	p, ok := r.byName[name]
	return p, ok
}

// ByID returns the entry for a row id.
func (r *Registry) ByID(id int64) (*Installed, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	p, ok := r.byID[id]
	return p, ok
}

// All returns every entry, by name. (The languages the running apps add to
// filex itself — UILocaleList / UILocale — live in langpack.go.)
func (r *Registry) All() []*Installed {
	r.mu.RLock()
	out := make([]*Installed, 0, len(r.byID))
	for _, p := range r.byID {
		out = append(out, p)
	}
	r.mu.RUnlock()
	sort.Slice(out, func(i, j int) bool { return out[i].Row.Name < out[j].Row.Name })
	return out
}

// running returns the compiled module or a typed error.
func (p *Installed) running() (*Compiled, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if p.compiled == nil {
		msg := "plugin is not running"
		if p.Manifest != nil && p.Manifest.IsLanguagePack() {
			// Nothing should ever route a call here — a pack declares no
			// action, screen or page — but if something does, say what it
			// is rather than blaming a runtime that never existed.
			msg = "a language pack has no module to call"
		}
		if p.stateErr != "" {
			msg += ": " + p.stateErr
		}
		return nil, &CallError{Code: CodeUnsupported, Message: msg}
	}
	return p.compiled, nil
}

// ── Status (what the admin API returns) ────────────────────────────────

// Status is one plugin as the admin list shows it.
type Status struct {
	ID          int64     `json:"id"`
	Name        string    `json:"name"`
	Version     string    `json:"version"`
	Label       wire.Text `json:"label"`
	Description wire.Text `json:"description,omitempty"`
	Icon        string    `json:"icon,omitempty"`
	Homepage    string    `json:"homepage,omitempty"`
	Enabled     bool      `json:"enabled"`
	State       string    `json:"state"`
	StateError  string    `json:"state_error,omitempty"`
	Source      string    `json:"source"`
	SourceURL   string    `json:"source_url,omitempty"`
	SHA256      string    `json:"sha256"`
	Signed      bool      `json:"signed"`
	Permissions []string  `json:"permissions"`
	// Scheduled says this app is woken once an hour and runs work of its
	// own choosing — the one thing on this row that happens with nobody
	// present, so the list can mark it.
	Scheduled   bool `json:"scheduled"`
	Actions     int  `json:"actions"`
	Views       int  `json:"views"`
	PublicPages int  `json:"public_pages"`
	// Kind is KindApp or KindLanguagePack (langpack.go). A language pack has
	// no module, so `sha256` is the hash of its MANIFEST — the thing that was
	// verified and, on an instance that requires signatures, signed.
	Kind string `json:"kind"`
	// Languages are the languages this app adds to filex itself, each with
	// how much of the CURRENT catalogue it covers. Absent when it adds none.
	Languages []LanguageRow `json:"languages,omitempty"`
	CreatedAt time.Time     `json:"created_at"`
	UpdatedAt time.Time     `json:"updated_at"`
}

// StatusOf builds the list row. ⚠ Nil-safe receiver: the wire-fixture test
// calls it on a nil registry, which then reports languages without coverage.
func (r *Registry) StatusOf(p *Installed) *Status {
	state, serr := p.State()
	perms := make([]string, 0, len(p.Perms))
	for _, x := range p.Perms {
		perms = append(perms, string(x))
	}
	return &Status{
		ID: p.Row.ID, Name: p.Row.Name, Version: p.Row.Version,
		Label: p.Manifest.Label, Description: p.Manifest.Description, Icon: p.Manifest.Icon, Homepage: p.Manifest.Homepage,
		Enabled: p.Row.Enabled, State: state, StateError: serr,
		Source: p.Row.Source, SourceURL: p.Row.SourceURL, SHA256: p.Row.SHA256, Signed: p.Row.Signed,
		Permissions: perms, Scheduled: p.Grants.Has(PermSchedule),
		Actions: len(p.Manifest.Actions), Views: len(p.Manifest.Views), PublicPages: len(p.Manifest.PublicPages),
		Kind: kindOf(p.Manifest), Languages: r.LanguageRows(p.Manifest),
		CreatedAt: p.Row.CreatedAt, UpdatedAt: p.Row.UpdatedAt,
	}
}

// PermissionRow is one line of the install-time review.
type PermissionRow struct {
	ID     string    `json:"id"`
	Label  string    `json:"label"`
	Reason wire.Text `json:"reason,omitempty"`
}

// PermissionRows renders the review for a manifest in lang.
func PermissionRows(m *Manifest, lang string) []PermissionRow {
	out := make([]PermissionRow, 0, len(m.Perms))
	for _, p := range m.Perms {
		out = append(out, PermissionRow{ID: string(p), Label: p.Label(lang), Reason: m.PermissionReasons[string(p)]})
	}
	return out
}

// ── Install / upgrade ──────────────────────────────────────────────────

// InstallError is a failure caused by what the caller supplied. Code is what
// the HTTP layer answers; Missing carries the permission diff for
// permissions_incomplete / permissions_changed.
//
// Reason, Where, Refs and Status narrow a fetch_failed, so the install wizard
// can write a sentence in the reader's language that says WHAT to check.
// ⚠ The message alone used to be the answer: "filex-app.json not found in
// BRF-Tech/yok-boyle-bir-depo: http 404 from raw.githubusercontent.com",
// English, in the Turkish wizard (release-candidate sweep, 2026-09-21).
type InstallError struct {
	Code    string
	Message string
	Missing []string
	Reason  string   // a FetchReason* for fetch_failed, else ""
	Where   string   // the repository or the URL that was being fetched
	Refs    []string // the refs a repository install tried
	Status  int      // the HTTP status that came back, when one did
}

func (e *InstallError) Error() string { return e.Code + ": " + e.Message }

func installErr(code, msg string) error { return &InstallError{Code: code, Message: msg} }

// Install error codes.
const (
	ErrCodeManifestInvalid       = "manifest_invalid"
	ErrCodeSHA256Mismatch        = "sha256_mismatch"
	ErrCodeSHA256Required        = "sha256_required"
	ErrCodeSignatureRequired     = "signature_required"
	ErrCodeSignatureInvalid      = "signature_invalid"
	ErrCodePermissionsIncomplete = "permissions_incomplete"
	ErrCodePermissionsChanged    = "permissions_changed"
	ErrCodeNameTaken             = "name_taken"
	ErrCodeDescribeMismatch      = "describe_mismatch"
	ErrCodeTooLarge              = "too_large"
	ErrCodeDemo                  = "demo_refused"
	ErrCodeFetch                 = "fetch_failed"
	ErrCodeNotFound              = "not_found"
)

// Why a fetch failed (InstallError.Reason on fetch_failed).
const (
	// FetchReasonBadRepo: the repository is not written as owner/name.
	FetchReasonBadRepo = "bad_repo"
	// FetchReasonManifestNotFound: no filex-app.json at any ref tried — a
	// repository that does not exist, is private, has no manifest at its
	// root, or a ref that does not exist.
	FetchReasonManifestNotFound = "manifest_not_found"
	// FetchReasonModuleNotFound: the manifest names a module address that
	// answers 404 — usually a release whose asset was never uploaded.
	FetchReasonModuleNotFound = "module_not_found"
	// FetchReasonUnreachable: no answer at all (DNS, network, TLS).
	FetchReasonUnreachable = "unreachable"
	// FetchReasonHTTPStatus: an answer, but not a success or a 404.
	FetchReasonHTTPStatus = "http_status"
	// FetchReasonBadURL: not an https address (plain http only for loopback).
	FetchReasonBadURL = "bad_url"
	// FetchReasonMissingURL: a URL install without its addresses — the
	// manifest always, the module unless the manifest is a language pack.
	FetchReasonMissingURL = "missing_url"
	// FetchReasonTooLarge: the answer was larger than the cap.
	FetchReasonTooLarge = "too_large"
)

// InstallInput is one install or upgrade request after the HTTP layer has
// gathered the pieces.
type InstallInput struct {
	Manifest []byte
	// Wasm is the module; Reader is consumed, capped at MaxWasmBytes.
	Wasm io.Reader
	// SHA256 (lower hex) the module must match; "" skips (upload only).
	SHA256    string
	Signature string
	Source    string
	SourceURL string
	// Granted is the permission list the admin approved. It must equal the
	// manifest's set exactly.
	Granted []string
	DryRun  bool
	Lang    string
}

// DryRunAnswer is what a dry run returns for the review step.
//
// ⚠ Installed and EnginesMissing are said HERE, at the review, because the
// dry run already knows them. Before, reinstalling an app that was already
// there passed the review and only "Install" answered "an app with this name
// is already installed", and an app needing an engine this server lacks was
// reviewed as if it would work (release-candidate sweep, 2026-09-21).
type DryRunAnswer struct {
	Manifest    *wire.Manifest  `json:"manifest"`
	Permissions []PermissionRow `json:"permissions"`
	// WasmSHA256 / WasmBytes describe the module; both empty for a language
	// pack, which has none (ManifestSHA256 is what was verified instead).
	WasmSHA256 string `json:"wasm_sha256"`
	WasmBytes  int64  `json:"wasm_bytes"`
	Signed     bool   `json:"signed"`
	// Kind: KindApp | KindLanguagePack. The review says which BEFORE the
	// install, so an administrator is not asked to trust a module that is
	// not there, or surprised by one that is.
	Kind           string `json:"kind"`
	ManifestSHA256 string `json:"manifest_sha256,omitempty"`
	// Languages: what the app would add to filex itself — coverage of the
	// current catalogue, and whether each is written right to left.
	Languages []LanguageRow `json:"languages,omitempty"`
	// Installed is the app of the same name already on this server (an
	// install cannot proceed; an upgrade of it can). Nil on an upgrade's own
	// dry run, and when nothing of that name is installed.
	Installed *DryRunInstalled `json:"installed,omitempty"`
	// EnginesMissing are the engines the manifest asks for that this server
	// does not have: the app installs, and whatever needs them will not work.
	EnginesMissing []DryRunEngine `json:"engines_missing,omitempty"`
}

// DryRunInstalled names the installed app a new install would collide with.
type DryRunInstalled struct {
	ID      int64  `json:"id"`
	Version string `json:"version"`
}

// DryRunEngine is one engine by its id and by the name a person reads.
type DryRunEngine struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// dryRun is the review of a staged install, for Install and Upgrade alike:
// what the app is (a module, or a language pack and its coverage), what it
// may do, and — because the dry run already knows — whether an app of that
// name is installed (installing only) and which engines it asks for that
// this server lacks.
func (r *Registry) dryRun(ctx context.Context, st *staged, lang string, installing bool) *DryRunAnswer {
	ans := &DryRunAnswer{
		Manifest: &st.m.Manifest, Permissions: PermissionRows(st.m, lang), Signed: st.signd,
		Kind: kindOf(st.m), Languages: r.LanguageRows(st.m),
	}
	if st.wasm == nil {
		ans.ManifestSHA256 = st.sum
	} else {
		ans.WasmSHA256, ans.WasmBytes = st.sum, int64(len(st.wasm))
	}
	if installing {
		if p, ok := r.ByName(st.m.Name); ok {
			ans.Installed = &DryRunInstalled{ID: p.Row.ID, Version: p.Row.Version}
		} else if row, _ := r.opts.Store.GetAppPluginByName(ctx, st.m.Name); row != nil {
			ans.Installed = &DryRunInstalled{ID: row.ID, Version: row.Version}
		}
	}
	for _, p := range st.m.Perms {
		name, ok := strings.CutPrefix(string(p), permPrefixEngines)
		if ok && (r.engines == nil || !r.engines.available(name)) {
			ans.EnginesMissing = append(ans.EnginesMissing, DryRunEngine{ID: name, Name: enginebin.DisplayName(name)})
		}
	}
	return ans
}

type staged struct {
	m     *Manifest
	wasm  []byte // nil for a language pack
	sum   string // the module's sha256 — the manifest's for a language pack
	signd bool
}

// stage validates the manifest and the module bytes, without touching disk
// or the database.
func (r *Registry) stage(in *InstallInput) (*staged, error) {
	if r.opts.Demo {
		return nil, installErr(ErrCodeDemo, "installing app plugins is disabled on the demo instance")
	}
	m, err := ParseManifest(in.Manifest)
	if err != nil {
		return nil, installErr(ErrCodeManifestInvalid, err.Error())
	}
	if m.IsLanguagePack() {
		return r.stagePack(m, in)
	}
	if in.Wasm == nil {
		return nil, installErr(ErrCodeManifestInvalid, "no module supplied — an app with actions, screens, public pages, settings or permissions needs its WebAssembly module (only a manifest that adds languages and nothing else installs without one)")
	}
	lr := io.LimitReader(in.Wasm, r.opts.MaxWasmBytes+1)
	wasm, err := io.ReadAll(lr)
	if err != nil {
		return nil, installErr(ErrCodeFetch, "read module: "+err.Error())
	}
	if int64(len(wasm)) > r.opts.MaxWasmBytes {
		return nil, installErr(ErrCodeTooLarge, fmt.Sprintf("module exceeds %d MiB", r.opts.MaxWasmBytes>>20))
	}
	if len(wasm) < 8 || !bytes.HasPrefix(wasm, []byte{0x00, 0x61, 0x73, 0x6d}) {
		return nil, installErr(ErrCodeManifestInvalid, "module is not a WebAssembly binary")
	}
	sum, signd, err := r.checkIntegrity("module", wasm, in.SHA256, in.Signature)
	if err != nil {
		return nil, err
	}
	return &staged{m: m, wasm: wasm, sum: sum, signd: signd}, nil
}

// checkIntegrity hashes the payload an install stands on — the module, or a
// language pack's manifest (`what` names it in the refusals) — and holds it
// to the caller's sha256 pin and, on an instance with trusted keys, to the
// detached signature over that hash. ONE implementation for both, so "this
// instance only runs signed apps" means the same thing for every kind.
func (r *Registry) checkIntegrity(what string, payload []byte, pin, signature string) (string, bool, error) {
	h := sha256.Sum256(payload)
	sum := hex.EncodeToString(h[:])
	if want := strings.ToLower(strings.TrimSpace(pin)); want != "" && want != sum {
		return "", false, installErr(ErrCodeSHA256Mismatch, what+" sha256 "+sum[:12]+"… does not match the expected "+want[:min(12, len(want))]+"…")
	}
	if err := plugin.VerifyDetached(r.trusted, sum, signature); err != nil {
		if errors.Is(err, plugin.ErrSignatureRequired) {
			return "", false, installErr(ErrCodeSignatureRequired, "this instance only accepts signed apps (FILEX_PLUGIN_TRUSTED_KEYS is set) — supply the detached signature over the "+what+"'s sha256")
		}
		return "", false, installErr(ErrCodeSignatureInvalid, err.Error())
	}
	return sum, len(r.trusted) > 0, nil
}

func (r *Registry) checkGrant(m *Manifest, granted []string, code string) error {
	g := Grants{}
	for _, s := range granted {
		p, err := ParsePermission(s)
		if err != nil {
			return &InstallError{Code: code, Message: "granted permission " + s + ": " + err.Error()}
		}
		g[p] = true
	}
	missing := g.Missing(m.Perms)
	if len(missing) > 0 {
		ms := make([]string, 0, len(missing))
		for _, p := range missing {
			ms = append(ms, string(p))
		}
		return &InstallError{Code: code, Message: "the manifest asks for permissions that were not granted", Missing: ms}
	}
	return nil
}

// Install stages, reviews and installs a plugin.
func (r *Registry) Install(ctx context.Context, in *InstallInput) (*Status, *DryRunAnswer, error) {
	st, err := r.stage(in)
	if err != nil {
		return nil, nil, err
	}
	if in.DryRun {
		return nil, r.dryRun(ctx, st, in.Lang, true), nil
	}
	if err := r.checkGrant(st.m, in.Granted, ErrCodePermissionsIncomplete); err != nil {
		return nil, nil, err
	}
	// ⚠⚠ From here on the install finishes — or undoes itself — whatever
	// the client does. Measured 2026-09-21: the 20 MB signing module took
	// 29 s to compile on a busy machine, the admin page's request gave up
	// at 30 s, and the cancelled request context (a) aborted the compile,
	// so the install answered describe_mismatch for a module that was fine,
	// and (b) made the cleanup's DeleteAppPlugin fail too, leaving a row no
	// list showed and every later install refused as "name_taken" until a
	// restart. Nothing below may be cut half-way by a closed tab.
	ctx = context.WithoutCancel(ctx)
	if _, taken := r.ByName(st.m.Name); taken {
		return nil, nil, installErr(ErrCodeNameTaken, "a plugin named "+st.m.Name+" is already installed")
	}
	if row, _ := r.opts.Store.GetAppPluginByName(ctx, st.m.Name); row != nil {
		return nil, nil, installErr(ErrCodeNameTaken, "a plugin named "+st.m.Name+" is already installed")
	}
	if err := r.writeFiles(st.m.Name, st, in.Manifest); err != nil {
		return nil, nil, err
	}
	permsJSON := permsJSONOf(st.m)
	row, err := r.opts.Store.CreateAppPlugin(ctx, &model.AppPlugin{
		Name: st.m.Name, Version: st.m.Version, LabelJSON: jsonOf(st.m.Label), ManifestJSON: string(in.Manifest),
		WasmPath: st.wasmPath(), SHA256: st.sum, Source: sourceOr(in.Source), SourceURL: in.SourceURL, Signed: st.signd,
		PermissionsJSON: permsJSON, Enabled: true,
	})
	if err != nil {
		_ = os.RemoveAll(filepath.Join(r.opts.Dir, st.m.Name))
		return nil, nil, fmt.Errorf("app-plugins: create row: %w", err)
	}
	p, err := r.entryFor(row)
	if err != nil {
		_ = r.opts.Store.DeleteAppPlugin(ctx, row.ID)
		_ = os.RemoveAll(filepath.Join(r.opts.Dir, st.m.Name))
		return nil, nil, err
	}
	r.put(p)
	r.compile(ctx, p)
	if state, serr := p.State(); state != StateRunning {
		// A module that does not describe itself as the manifest says is not
		// installed half-way: files and row go, the error is the answer.
		r.drop(p)
		_ = r.opts.Store.DeleteAppPlugin(ctx, row.ID)
		_ = os.RemoveAll(filepath.Join(r.opts.Dir, st.m.Name))
		return nil, nil, installErr(ErrCodeDescribeMismatch, serr)
	}
	return r.StatusOf(p), nil, nil
}

// Upgrade replaces the module + manifest of an installed plugin. A manifest
// that asks for a permission not yet granted answers permissions_changed
// until the request grants it; a narrower manifest simply narrows the grant.
func (r *Registry) Upgrade(ctx context.Context, id int64, in *InstallInput) (*Status, *DryRunAnswer, error) {
	p, ok := r.ByID(id)
	if !ok {
		return nil, nil, installErr(ErrCodeNotFound, "no such plugin")
	}
	st, err := r.stage(in)
	if err != nil {
		return nil, nil, err
	}
	if st.m.Name != p.Row.Name {
		return nil, nil, installErr(ErrCodeManifestInvalid, "the new manifest names "+st.m.Name+", the installed plugin is "+p.Row.Name)
	}
	if in.DryRun {
		return nil, r.dryRun(ctx, st, in.Lang, false), nil
	}
	granted := in.Granted
	if len(granted) == 0 {
		granted = permStrings(p.Perms)
	}
	if err := r.checkGrant(st.m, granted, ErrCodePermissionsChanged); err != nil {
		return nil, nil, err
	}
	// ⚠ As in Install: an upgrade swaps files and compiles; a client that
	// leaves in the middle must not leave the app half-replaced.
	ctx = context.WithoutCancel(ctx)
	// Keep the old files until the new module has proven itself.
	dir := filepath.Join(r.opts.Dir, p.Row.Name)
	backup := dir + ".prev"
	_ = os.RemoveAll(backup)
	if err := os.Rename(dir, backup); err != nil && !os.IsNotExist(err) {
		return nil, nil, fmt.Errorf("app-plugins: stash previous: %w", err)
	}
	if err := r.writeFiles(p.Row.Name, st, in.Manifest); err != nil {
		_ = os.RemoveAll(dir)
		_ = os.Rename(backup, dir)
		return nil, nil, err
	}
	oldRow := *p.Row
	p.Row.Version = st.m.Version
	p.Row.LabelJSON = jsonOf(st.m.Label)
	p.Row.ManifestJSON = string(in.Manifest)
	// ⚠ An upgrade may cross the line either way (a pack that grows a module,
	// an app whose module is dropped), so the module path follows the NEW
	// manifest rather than staying what the old row said.
	p.Row.WasmPath = st.wasmPath()
	p.Row.SHA256 = st.sum
	p.Row.Signed = st.signd
	p.Row.Source = sourceOr(in.Source)
	p.Row.SourceURL = in.SourceURL
	p.Row.PermissionsJSON = permsJSONOf(st.m)
	np, err := r.entryFor(p.Row)
	if err != nil {
		*p.Row = oldRow
		_ = os.RemoveAll(dir)
		_ = os.Rename(backup, dir)
		return nil, nil, err
	}
	np.logs = p.logs
	r.compile(ctx, np)
	if state, serr := np.State(); state != StateRunning && p.Row.Enabled {
		*p.Row = oldRow
		_ = os.RemoveAll(dir)
		_ = os.Rename(backup, dir)
		r.compile(ctx, p)
		return nil, nil, installErr(ErrCodeDescribeMismatch, serr)
	}
	if err := r.opts.Store.UpdateAppPlugin(ctx, p.Row); err != nil {
		return nil, nil, fmt.Errorf("app-plugins: update row: %w", err)
	}
	p.mu.Lock()
	if p.compiled != nil {
		_ = p.compiled.Close(ctx)
	}
	p.mu.Unlock()
	_ = os.RemoveAll(backup)
	r.put(np)
	np.log("info", "upgraded to "+st.m.Version)
	return r.StatusOf(np), nil, nil
}

func (r *Registry) writeFiles(name string, st *staged, manifest []byte) error {
	dir := filepath.Join(r.opts.Dir, name)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("app-plugins: mkdir: %w", err)
	}
	if st.wasm != nil { // a language pack has no module to write
		if err := os.WriteFile(filepath.Join(dir, "plugin.wasm"), st.wasm, 0o600); err != nil {
			return fmt.Errorf("app-plugins: write module: %w", err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "filex-app.json"), manifest, 0o600); err != nil {
		return fmt.Errorf("app-plugins: write manifest: %w", err)
	}
	return nil
}

// SetEnabled flips a plugin on or off, loading or unloading it.
func (r *Registry) SetEnabled(ctx context.Context, id int64, on bool) (*Status, error) {
	p, ok := r.ByID(id)
	if !ok {
		return nil, installErr(ErrCodeNotFound, "no such plugin")
	}
	if r.opts.Demo {
		return nil, installErr(ErrCodeDemo, "app plugins cannot be changed on the demo instance")
	}
	p.Row.Enabled = on
	if err := r.opts.Store.UpdateAppPlugin(ctx, p.Row); err != nil {
		return nil, err
	}
	if on {
		r.compile(ctx, p)
	} else {
		p.mu.Lock()
		if p.compiled != nil {
			_ = p.compiled.Close(ctx)
			p.compiled = nil
		}
		p.mu.Unlock()
		p.setState(StateDisabled, "")
	}
	return r.StatusOf(p), nil
}

// Remove uninstalls a plugin: module, row and everything keyed on it.
func (r *Registry) Remove(ctx context.Context, id int64) error {
	p, ok := r.ByID(id)
	if !ok {
		return installErr(ErrCodeNotFound, "no such plugin")
	}
	if r.opts.Demo {
		return installErr(ErrCodeDemo, "app plugins cannot be changed on the demo instance")
	}
	p.mu.Lock()
	if p.compiled != nil {
		_ = p.compiled.Close(ctx)
		p.compiled = nil
	}
	p.mu.Unlock()
	r.drop(p)
	if err := r.opts.Store.DeleteAppPlugin(ctx, id); err != nil {
		return err
	}
	_ = os.RemoveAll(filepath.Join(r.opts.Dir, p.Row.Name))
	// Its downloads go with it (asset_fetch).
	_ = os.RemoveAll(r.assetDir(p.Row.Name))
	return nil
}

// Logs returns the ring lines after seq.
func (r *Registry) Logs(id int64, after int64) ([]LogLine, int64, error) {
	p, ok := r.ByID(id)
	if !ok {
		return nil, 0, installErr(ErrCodeNotFound, "no such plugin")
	}
	lines, next := p.logs.after(after)
	return lines, next, nil
}

// ── Settings ───────────────────────────────────────────────────────────

// Settings returns the stored values with secret ones masked as "***".
func (r *Registry) Settings(ctx context.Context, id int64) (map[string]string, []wire.Field, error) {
	p, ok := r.ByID(id)
	if !ok {
		return nil, nil, installErr(ErrCodeNotFound, "no such plugin")
	}
	vals, err := r.opts.Store.GetAppPluginSettings(ctx, p.Row.ID)
	if err != nil {
		return nil, nil, err
	}
	out := map[string]string{}
	for _, f := range p.Manifest.Settings {
		v, has := vals[f.Key]
		switch {
		case f.Secret && has && v != "":
			out[f.Key] = secretMask
		case has:
			out[f.Key] = v
		}
	}
	return out, p.Manifest.Settings, nil
}

const secretMask = "***"

// PutSettings stores values. A secret field sent as "***" keeps its current
// value; other secret values are sealed. Unknown keys are dropped.
func (r *Registry) PutSettings(ctx context.Context, id int64, values map[string]string) error {
	p, ok := r.ByID(id)
	if !ok {
		return installErr(ErrCodeNotFound, "no such plugin")
	}
	if r.opts.Demo {
		return installErr(ErrCodeDemo, "app plugins cannot be changed on the demo instance")
	}
	current, err := r.opts.Store.GetAppPluginSettings(ctx, p.Row.ID)
	if err != nil {
		return err
	}
	next := map[string]string{}
	for _, f := range p.Manifest.Settings {
		v, has := values[f.Key]
		if !has {
			if cur, ok := current[f.Key]; ok {
				next[f.Key] = cur
			}
			continue
		}
		if f.Secret {
			if v == secretMask {
				if cur, ok := current[f.Key]; ok {
					next[f.Key] = cur
				}
				continue
			}
			if v == "" {
				continue
			}
			if !r.box.Enabled() {
				return installErr(ErrCodeManifestInvalid, "secret settings need FILEX_SECRET_KEY to be stored")
			}
			sealed, err := r.box.Seal(v)
			if err != nil {
				return err
			}
			next[f.Key] = sealed
			continue
		}
		next[f.Key] = v
	}
	return r.opts.Store.PutAppPluginSettings(ctx, p.Row.ID, next)
}

// openSettings returns the plain values (secrets opened) for a host call.
func (r *Registry) openSettings(ctx context.Context, p *Installed) (map[string]string, error) {
	vals, err := r.opts.Store.GetAppPluginSettings(ctx, p.Row.ID)
	if err != nil {
		return nil, err
	}
	for k, v := range vals {
		if secretbox.IsSealed(v) {
			plain, err := r.box.Open(v)
			if err != nil {
				delete(vals, k)
				continue
			}
			vals[k] = plain
		}
	}
	for _, f := range p.Manifest.Settings {
		if _, has := vals[f.Key]; !has && f.Default != nil {
			vals[f.Key] = fmt.Sprint(f.Default)
		}
	}
	return vals, nil
}

// publicSettings is openSettings without secrets — what a call input carries.
func (r *Registry) publicSettings(ctx context.Context, p *Installed) map[string]string {
	vals, err := r.openSettings(ctx, p)
	if err != nil {
		return nil
	}
	for _, f := range p.Manifest.Settings {
		if f.Secret {
			delete(vals, f.Key)
		}
	}
	return vals
}

// ── Overrides ──────────────────────────────────────────────────────────

// OverrideRow is the admin API shape of one action override. Applies is a
// WHOLE rule both ways — the editor shows a list and sends a list — while
// the row stores only what changed against the manifest (applies_override.go
// says why). Nil = as the manifest says.
type OverrideRow struct {
	ID        string        `json:"id"`
	Enabled   bool          `json:"enabled"`
	AdminOnly bool          `json:"admin_only"`
	Applies   *wire.Applies `json:"applies"`
}

// Overrides returns one row per manifest action, merged with stored changes.
func (r *Registry) Overrides(ctx context.Context, id int64) ([]OverrideRow, error) {
	p, ok := r.ByID(id)
	if !ok {
		return nil, installErr(ErrCodeNotFound, "no such plugin")
	}
	stored, err := r.opts.Store.ListAppPluginOverrides(ctx, p.Row.ID)
	if err != nil {
		return nil, err
	}
	byID := map[string]*model.AppPluginOverride{}
	for _, o := range stored {
		byID[o.ActionID] = o
	}
	out := make([]OverrideRow, 0, len(p.Manifest.Actions))
	for _, a := range p.Manifest.Actions {
		// A hidden action is the app's own machinery (`apply` behind a
		// signer's link, a scheduled `expire`), not a row in anybody's menu,
		// so it is not offered for overriding. See hiddenIgnoresOverride.
		if a.Hidden {
			continue
		}
		row := OverrideRow{ID: a.ID, Enabled: true}
		if o := byID[a.ID]; o != nil {
			row.Enabled, row.AdminOnly = o.Enabled, o.AdminOnly
			if d := storedDelta(o.AppliesJSON, a.Applies); d != nil {
				ap := editorRule(a.Applies, manifestEngineExt(a.Applies), d)
				row.Applies = &ap
			}
		}
		out = append(out, row)
	}
	return out, nil
}

// PutOverrides stores the rows (unknown and hidden action ids dropped,
// applies validated).
func (r *Registry) PutOverrides(ctx context.Context, id int64, rows []OverrideRow) error {
	p, ok := r.ByID(id)
	if !ok {
		return installErr(ErrCodeNotFound, "no such plugin")
	}
	if r.opts.Demo {
		return installErr(ErrCodeDemo, "app plugins cannot be changed on the demo instance")
	}
	var list []*model.AppPluginOverride
	for _, row := range rows {
		a, ok := p.Manifest.Action(row.ID)
		if !ok || a.Hidden {
			continue
		}
		o := &model.AppPluginOverride{PluginID: p.Row.ID, ActionID: row.ID, Enabled: row.Enabled, AdminOnly: row.AdminOnly}
		if row.Applies != nil {
			ap := *row.Applies
			ap.Ext = append([]string(nil), ap.Ext...)
			ap.Mime = append([]string(nil), ap.Mime...)
			if err := validateApplies(&ap); err != nil {
				return installErr(ErrCodeManifestInvalid, "action "+row.ID+": "+err.Error())
			}
			// ⚠ Stored as the change against the manifest, resolved at every
			// read — never as the rule itself (applies_override.go).
			o.AppliesJSON = encodeDelta(deltaFrom(a.Applies, manifestEngineExt(a.Applies), ap))
		}
		list = append(list, o)
	}
	return r.opts.Store.PutAppPluginOverrides(ctx, p.Row.ID, list)
}

// effective is an action with the admin's override applied.
type effective struct {
	Action    *wire.Action
	Applies   wire.Applies
	Enabled   bool
	AdminOnly bool
	// Gated: what the rule would add once an engine the app was granted is
	// installed (ActionRow.Gated).
	Gated []GatedRule
}

// effectiveActions merges the manifest with stored overrides.
func (r *Registry) effectiveActions(ctx context.Context, p *Installed) ([]effective, error) {
	stored, err := r.opts.Store.ListAppPluginOverrides(ctx, p.Row.ID)
	if err != nil {
		return nil, err
	}
	byID := map[string]*model.AppPluginOverride{}
	for _, o := range stored {
		byID[o.ActionID] = o
	}
	out := make([]effective, 0, len(p.Manifest.Actions))
	// Resolved NOW, against the engines present now: an override saved
	// before LibreOffice was installed offers .docx once it is, and stops
	// when it is removed.
	engines := r.enginesFor(p)
	for i := range p.Manifest.Actions {
		a := &p.Manifest.Actions[i]
		e := effective{Action: a, Applies: a.Applies, Enabled: true}
		var delta *appliesDelta
		if o := byID[a.ID]; o != nil && !hiddenIgnoresOverride(a) {
			e.Enabled, e.AdminOnly = o.Enabled, o.AdminOnly
			if d := storedDelta(o.AppliesJSON, a.Applies); d != nil {
				delta = d
				ap, offerable := d.resolve(a.Applies, manifestEngineExt(a.Applies), engines)
				e.Applies = ap
				if !offerable {
					// Every type the admin kept needs an engine that is not
					// here: offered on nothing, not on everything.
					e.Enabled = false
				}
			}
		}
		// Extensions that need an engine exist only while it does.
		e.Applies = withEngineExt(e.Applies, engines)
		e.Gated = gatedRules(a.Applies, delta, e.Applies, engines, p.Grants.HasEngine)
		out = append(out, e)
	}
	return out, nil
}

// gatedRules is what an action's rule would ALSO accept once each engine it
// was granted, and the server lacks, is installed: the rule resolved with
// that one engine present, minus the rule as it is now — the admin's
// override honoured (an extension they removed stays removed). One entry per
// missing engine, in name order.
func gatedRules(m wire.Applies, d *appliesDelta, now wire.Applies, engines map[string]bool, granted func(string) bool) []GatedRule {
	g := manifestEngineExt(m)
	if len(g) == 0 {
		return nil
	}
	names := make([]string, 0, len(g))
	for n := range g {
		names = append(names, n)
	}
	sort.Strings(names)
	var out []GatedRule
	for _, n := range names {
		if engines[n] || !granted(n) {
			continue
		}
		with := make(map[string]bool, len(engines)+1)
		for k, v := range engines {
			with[k] = v
		}
		with[n] = true
		var would wire.Applies
		if d != nil {
			would, _ = d.resolve(m, g, with)
		} else {
			would = withEngineExt(m, with)
		}
		extra := minus(normExt, would.Ext, now.Ext)
		if len(extra) == 0 {
			continue
		}
		out = append(out, GatedRule{Ext: extra, Needs: Need{Kind: "engine", ID: n, Name: enginebin.DisplayName(n)}})
	}
	return out
}

// hiddenIgnoresOverride: an administrator's switch never reaches a hidden
// action.
//
// ⚠⚠ Owner's rule (v0.43.0 release-candidate sweep, 2026-09-21): "a hidden
// action is not a person's to switch off". The signer's `apply` is what an
// outside signer's link runs, a scheduled `expire` is what closes a request
// at its deadline; they are the app's machinery, in no menu, and switching
// one off half-breaks the app in a way nobody can see from a menu — requests
// go out and can never be signed. The whole app has a switch for stopping
// all of it. So they are not listed (Overrides), not stored (PutOverrides)
// and — in case a row predates this, or was written by hand — not honoured
// here either.
func hiddenIgnoresOverride(a *wire.Action) bool { return a != nil && a.Hidden }

// ── helpers ────────────────────────────────────────────────────────────

func permsJSONOf(m *Manifest) string {
	return jsonOf(permStrings(m.Perms))
}

func permStrings(ps []Permission) []string {
	out := make([]string, 0, len(ps))
	for _, p := range ps {
		out = append(out, string(p))
	}
	return out
}

func jsonOf(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func sourceOr(s string) string {
	switch s {
	case model.AppPluginSourceURL, model.AppPluginSourceGitHub, model.AppPluginSourceBundle, model.AppPluginSourceUpload:
		return s
	}
	return model.AppPluginSourceUpload
}
