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

	"github.com/brf-tech/filex/backend/internal/assoc"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/enginebin"
	"github.com/brf-tech/filex/backend/internal/keylock"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/netguard"
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
	// HTTP fetches manifests, modules and interface bundles - an
	// administrator's install, an install request, the update check. Nil (what
	// a server passes) is the shared guarded download client
	// (netguard.DownloadClient): public addresses only, after DNS and on every
	// redirect hop, no redirect from https to plain http, 5 hops at most.
	// Supplying a client lifts that guard; it exists for tests and for an
	// embedder with an egress policy of its own.
	HTTP *http.Client
	// LoopbackSources lets the guarded client reach this machine
	// (FILEX_PLUGIN_LOOPBACK_SOURCES): development and the end-to-end tests
	// serve app sources from a loopback server. Never the private network.
	LoopbackSources bool
	// GitHubRawBase is where a repository's files are read from for a GitHub
	// install (FILEX_APP_GITHUB_RAW_BASE): "" is https://raw.githubusercontent.com.
	// A mirror of it (an air-gapped install's), or the end-to-end tests' fake
	// GitHub. The download guard applies to it like to every other address.
	GitHubRawBase string
	Log           *slog.Logger
	// StorageResolver opens the driver a job reads from and writes to.
	StorageResolver func(int64) (storage.Driver, error)
	// Limits (bytes). Zero → defaults below.
	MaxWasmBytes   int64
	MaxInputBytes  int64
	MaxOutputBytes int64
	// MaxUIBytes caps an interface bundle, zipped (FILEX_APP_PLUGIN_MAX_UI_MB).
	MaxUIBytes int64
	// PerPluginJobs bounds concurrent action_run calls per plugin (default 2).
	PerPluginJobs int
	// PerPluginCalls bounds concurrent SCREEN calls per plugin — view and
	// public page events (default 8). Each is a fresh instance with up to
	// the manifest's memory ceiling (256 MiB at most), and a public page
	// event is answered for an anonymous visitor: without a ceiling, a burst
	// of events on one link held that much memory per request.
	PerPluginCalls int
	// Office is the office engine (`engines:office`, alias
	// `engines:libreoffice`): the connected OnlyOffice Document Server's
	// conversion API (office.go). Nil = no office engine on this server.
	Office OfficeConverter
	// Clock is the apps' clock (appclock.go). The zero value reads
	// FILEX_APP_CLOCK, and with that unset it is the real clock. ⚠
	// Screenshots and tests only: an app on a moved clock dates its
	// signatures and reminders with a time that is not now.
	Clock AppClock
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
	// StateUnlicensed is a paid app whose license does not hold (revoked,
	// expired, never confirmed, its grace ended): loaded, not removed, and
	// running nothing until the license holds again (license_hold.go,
	// internal/appstore). It overlays "running" only.
	StateUnlicensed = "unlicensed"
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
	// ui is the loaded interface (uibundle.go); nil when the app has none.
	ui *uiBundle
	// prev is the version an upgrade replaced, kept to go back to
	// (versions.go).
	prev     *prevState
	state    string
	stateErr string
	// hold is why the app's license keeps it from running ("" = it does
	// not); see StateUnlicensed.
	hold string
	logs *logRing
	sem  chan struct{}
	// calls bounds the plugin's concurrent screen calls (Options.PerPluginCalls).
	calls    chan struct{}
	mailRate rateWindow
	signRate minuteWindow
	// thumb is the app's thumbnail machinery (thumbnails.go): its limits, its
	// slots, its thumbnail copy of the module.
	thumb thumbState
}

func (p *Installed) log(level, msg string) { p.logs.add(level, msg) }

// UI is the app's loaded interface, nil when it has none (or it failed to
// load — the state says why).
func (p *Installed) UI() *uiBundle {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.ui
}

// UIBundle is the interface version an address names (its bundle sha256's
// first 16 hex digits); nil when this app serves no such version.
func (p *Installed) UIBundle(short string) *uiBundle {
	if b := p.UI(); b != nil && b.short == short {
		return b
	}
	return nil
}

// HasModule reports whether the app runs a wasm module: every app but a
// language pack and an app that is only an interface (its row has no module
// file).
func (p *Installed) HasModule() bool {
	return p.Row.WasmPath != ""
}

// State returns the live state and its error. A running app whose license
// does not hold is StateUnlicensed, with the license's reason: every caller
// that runs an app asks for StateRunning, so a held app runs nothing.
func (p *Installed) State() (string, string) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if p.state == StateRunning && p.hold != "" {
		return StateUnlicensed, p.hold
	}
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
	// holds are the license holds by app name (license_hold.go): kept here
	// and not only on the entry, because an upgrade replaces the entry.
	holds map[string]string
	// installLocks serialise Install per app name.
	installLocks keylock.Map

	sink OutputSink
	// queue is where scheduled work is handed to the ops worker (schedule.go).
	// nil means a due item fails loudly rather than vanishing.
	queue     JobQueue
	notify    notifySink
	mailer    mailSink
	userScope func(ctx context.Context, u *model.User) context.Context
	// home answers CallContext.Home (SetHomeResolver).
	home func(ctx context.Context, u *model.User) string
	// heldPerms answers wire.Actor.Permissions (SetHeldPermissions).
	heldPerms func(ctx context.Context, u *model.User, p *Installed) []string
	// visible answers "may this person see this file?" for state_list.
	// Nil means every file passes, which is what a single-user instance
	// with no ACL wiring wants.
	visible  func(ctx context.Context, u *model.User, storageID int64, rel string) bool
	outbound http.RoundTripper
	// fetchGuard is the address policy of the registry's own download client
	// (Options.HTTP nil); nil when the embedder supplied the client.
	fetchGuard *netguard.Policy

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

	// upgradeMu serialises every upgrade (an administrator's and the update
	// check's) and lets Close wait for the one in flight; closing refuses
	// the next.
	upgradeMu sync.Mutex
	closing   bool
	// updates is the update check's own state (updates.go).
	updates updateState

	// uiOriginURL is where interfaces are served when they have an origin of
	// their own (FILEX_APP_UI_ORIGIN): their addresses are then absolute.
	// "" = filex's own origin, relative addresses.
	uiOriginURL string
	// upgraded hears every approved version change (versions.go).
	upgraded UpgradeListener
}

// SetUIOrigin sets the separate origin interfaces are served from
// (FILEX_APP_UI_ORIGIN, with its base path, no trailing slash); "" for filex's
// own.
func (r *Registry) SetUIOrigin(origin string) { r.uiOriginURL = strings.TrimRight(origin, "/") }

func (r *Registry) uiOrigin() string { return r.uiOriginURL }

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
	// ⚠ The guard follows the client: a registry that builds its own client
	// checks addresses before the dial too (fetch), one handed a client by
	// its embedder leaves that to the embedder - the same switch the storage
	// plugin downloader has (plugin.Options.HTTP).
	var fetchGuard *netguard.Policy
	if o.HTTP == nil {
		p := netguard.Policy{Loopback: o.LoopbackSources}
		fetchGuard = &p
		o.HTTP = p.DownloadClient(2 * time.Minute)
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
	if o.MaxUIBytes <= 0 {
		o.MaxUIBytes = DefaultMaxUIBytes
	}
	if o.PerPluginJobs <= 0 {
		o.PerPluginJobs = 2
	}
	if o.PerPluginCalls <= 0 {
		o.PerPluginCalls = 8
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
	if !o.Clock.On() {
		c, cerr := AppClockFromEnv()
		if cerr != nil {
			o.Log.Warn("app-plugins: " + cerr.Error() + "; the apps keep the real clock")
		}
		o.Clock = c
	}
	if o.Clock.On() {
		// Said at start, every start: a moved clock on a real server would
		// date signatures and reminders wrongly, and this line is where an
		// operator who set it by mistake finds out.
		o.Log.Warn("app-plugins: the apps' clock is moved ("+EnvAppClock+") - for screenshots and tests only",
			slog.Time("app_now", o.Clock.Now().UTC()), slog.Duration("offset", o.Clock.Offset()))
	}
	rt.clock = o.Clock
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
		opts: o, rt: rt, box: box, trusted: trusted, engines: probeEngines(o.Office), log: o.Log,
		byID: map[int64]*Installed{}, byName: map[string]*Installed{},
		outbound: newOutboundTransport(), instanceID: newInstanceID(),
		fetchGuard: fetchGuard,
	}, nil
}

func (r *Registry) spoolRoot() string { return filepath.Join(r.opts.Dir, "spool") }

// appClock is the apps' clock (appclock.go); the real clock on a nil registry.
func (r *Registry) appClock() AppClock {
	if r == nil {
		return AppClock{}
	}
	return r.opts.Clock
}

func (r *Registry) maxUIBytes() int64 { return r.opts.MaxUIBytes }

// MaxOutputBytes is the ceiling on one file an app writes (a job's output, an
// interface's save).
func (r *Registry) MaxOutputBytes() int64 { return r.opts.MaxOutputBytes }

// MaxUIBytes is the interface bundle's ceiling, zipped.
func (r *Registry) MaxUIBytes() int64 { return r.opts.MaxUIBytes }

// Dir is where modules live.
func (r *Registry) Dir() string { return r.opts.Dir }

// RequiresSignature reports whether installs must carry a signature.
func (r *Registry) RequiresSignature() bool { return len(r.trusted) > 0 }

// SetOffice wires the office engine (Options.Office) after the registry is
// built - the document server's service exists later in the server's boot.
// Call it before the server starts serving.
func (r *Registry) SetOffice(o OfficeConverter) {
	if r == nil || r.engines == nil {
		return
	}
	r.engines.office = o
}

// Engines is the engine → can-run-now map for this host, every engine by its
// own id (the office engine as `office`, never its alias).
func (r *Registry) Engines(ctx context.Context) map[string]bool { return r.engines.Available(ctx) }

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

// SetHeldPermissions wires the answer to "which of this app's own user
// permissions does this person hold" (wire.Actor.Permissions) — the HTTP
// layer, which decides them at every door with the same question
// (handlers.appPermHeld), so what an app is told is what the door does.
func (r *Registry) SetHeldPermissions(f func(ctx context.Context, u *model.User, p *Installed) []string) {
	r.heldPerms = f
}

// wireActor is the person a call runs as, as the app is told: who, their
// address on a view (ip; empty on a job), and which of THIS app's
// user_permissions they hold. Nobody (id 0: a wake-up, work nobody started)
// holds none.
func (r *Registry) wireActor(ctx context.Context, p *Installed, u *model.User, ip string) wire.Actor {
	a := wire.Actor{ID: u.ID, Email: u.Email, Name: u.DisplayName, Role: u.Role, IP: ip}
	if r.heldPerms != nil && u.ID > 0 && p != nil && p.Manifest != nil && len(p.Manifest.UserPermissions) > 0 {
		a.Permissions = r.heldPerms(ctx, u, p)
	}
	return a
}

// Close frees every compiled module and the runtime. It waits for an upgrade
// in flight — an automatic one included — to finish its swap, and refuses any
// after it, so a shutdown never leaves an app half-replaced.
func (r *Registry) Close(ctx context.Context) {
	r.upgradeMu.Lock()
	r.closing = true
	r.upgradeMu.Unlock()
	r.mu.Lock()
	for _, p := range r.byID {
		p.mu.Lock()
		if p.compiled != nil {
			_ = p.compiled.Close(ctx)
			p.compiled = nil
		}
		p.mu.Unlock()
		p.dropThumbModule(ctx)
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
		r.recoverInterruptedUpgrade(row)
		p, err := r.entryFor(row)
		if err != nil {
			r.log.Warn("app-plugins: row unreadable", slog.String("plugin", row.Name), slog.Any("err", err))
			continue
		}
		r.put(p)
		r.loadPrevious(ctx, p)
		r.upgradeLegacyOverrides(ctx, p)
		if row.Enabled {
			r.compile(ctx, p)
		} else {
			p.setState(StateDisabled, "")
		}
		// ⚠ An app whose range leaves this filex out (filex was upgraded
		// past it) KEEPS RUNNING: the range is the author's promise, not a
		// proof, every host call is still held to the grant, and switching it
		// off here would take a language — or a signing flow — away from
		// everybody at the moment filex was upgraded, with nobody having
		// decided it. The administrator is told (the Apps list marks it; the
		// log says it here) and the update check looks for a version that
		// does fit.
		if c := compatOf(p.Manifest); c != nil && !c.OK {
			p.log("warn", p.Row.Name+" "+p.Row.Version+" says it works with filex "+c.Requires+"; this is filex "+FilexVersion()+". It keeps running; the update check looks for a version that fits.")
		}
	}
	return nil
}

// recoverInterruptedUpgrade puts an app's files back the way its row says
// they are when the process stopped in the middle of an upgrade — a crash, a
// kill, a power cut. (A clean shutdown waits for the swap: Close.)
//
// Upgrade stashes the old files as <name>.prev, writes the new ones, and only
// once the new module has proven itself writes the row and drops the stash.
// So at start, with a stash on disk:
//
//   - the directory holds what the row describes (its manifest, and the
//     module — or for a language pack the manifest — hashing to the row's
//     sha256): the row was written, the stash is left over and goes;
//   - anything else (no directory, a half-written one, the new files beside
//     the old row): the row still describes the stash, and it goes back.
//
// ⚠ No `.prev` can be an app of its own: a name has no dot (nameRe).
func (r *Registry) recoverInterruptedUpgrade(row *model.AppPlugin) {
	dir := filepath.Join(r.opts.Dir, row.Name)
	prev := dir + ".prev"
	if fi, err := os.Stat(prev); err != nil || !fi.IsDir() {
		return
	}
	if dirMatchesRow(dir, row) {
		_ = os.RemoveAll(prev)
		return
	}
	_ = os.RemoveAll(dir)
	if err := os.Rename(prev, dir); err != nil {
		r.log.Warn("app-plugins: an interrupted upgrade could not be undone", slog.String("plugin", row.Name), slog.Any("err", err))
		return
	}
	r.log.Warn("app-plugins: an upgrade was interrupted; the previous version is back", slog.String("plugin", row.Name), slog.String("version", row.Version))
}

// dirMatchesRow reports whether dir holds the files row describes.
func dirMatchesRow(dir string, row *model.AppPlugin) bool {
	manifest, err := os.ReadFile(filepath.Join(dir, "filex-app.json"))
	if err != nil || string(manifest) != row.ManifestJSON {
		return false
	}
	payload := manifest
	if row.WasmPath != "" {
		if payload, err = os.ReadFile(filepath.Join(dir, row.WasmPath)); err != nil {
			return false
		}
	}
	h := sha256.Sum256(payload)
	return strings.EqualFold(hex.EncodeToString(h[:]), row.SHA256)
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
	defer r.mu.Unlock()
	// ⚠ The hold goes on BEFORE the entry is published, under the lock
	// SetLicenseHold takes: set after it, a hold set in between was
	// overwritten with the one read before (license_hold.go, Y5).
	p.setHold(r.holds[p.Row.Name])
	r.byID[p.Row.ID] = p
	r.byName[p.Row.Name] = p
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
		state: StateDisabled, sem: make(chan struct{}, r.opts.PerPluginJobs), calls: make(chan struct{}, r.opts.PerPluginCalls), logs: &logRing{},
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
		p.log("info", "language pack "+p.Row.Name+" "+p.Row.Version+" serves "+strings.Join(sortedTags(p.Manifest.UILocales), ", ")+" - no module, nothing runs")
		return
	}
	// The interface first (uibundle.go): an app whose interface does not load
	// — its bundle gone, or no longer the bytes that were approved — is not
	// running, exactly like a module that no longer hashes to its row.
	if err := r.loadUI(p); err != nil {
		p.setState(StateFailed, err.Error())
		r.persistError(ctx, p, err.Error())
		return
	}
	if !p.HasModule() {
		// ⚠ An app that is ONLY an interface (draw.io's editor): no module,
		// no runtime instance, nothing to describe. "Running" means its
		// interface is served.
		if p.Manifest.UI == nil {
			msg := "the app has neither a module nor an interface"
			p.setState(StateFailed, msg)
			r.persistError(ctx, p, msg)
			return
		}
		p.mu.Lock()
		if p.compiled != nil {
			_ = p.compiled.Close(ctx)
			p.compiled = nil
		}
		p.mu.Unlock()
		p.setState(StateRunning, "")
		if p.Row.LastError != "" {
			r.persistError(ctx, p, "")
		}
		p.log("info", "loaded "+p.Row.Name+" "+p.Row.Version+" - an interface, no module")
		return
	}
	wasmPath := filepath.Join(r.opts.Dir, p.Row.Name, p.Row.WasmPath)
	c, err := r.rt.Compile(ctx, wasmPath, p.Row.SHA256, p.Manifest, nil, p.log)
	if err != nil {
		p.setState(StateFailed, err.Error())
		r.persistError(ctx, p, err.Error())
		return
	}
	described, err := c.Describe(ctx, "")
	if err != nil {
		_ = c.Close(ctx)
		state := StateFailed
		if IsCode(err, CodeRefused) {
			state = StateRefused
		}
		p.setState(state, err.Error())
		r.persistError(ctx, p, err.Error())
		return
	}
	if err := r.checkDescribedUI(p.Manifest, described); err != nil {
		_ = c.Close(ctx)
		p.setState(StateRefused, err.Error())
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
	// The same for an app that says it draws thumbnails (thumbnails.go).
	if err := checkThumbnailExport(ctx, p.Manifest, c); err != nil {
		_ = c.Close(ctx)
		p.setState(StateRefused, err.Error())
		r.persistError(ctx, p, err.Error())
		return
	}
	p.dropThumbModule(ctx)
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

// loadUI loads (or drops) the app's interface from its directory.
func (r *Registry) loadUI(p *Installed) error {
	var b *uiBundle
	if p.Manifest.UI != nil {
		var err error
		if b, err = loadUIBundle(filepath.Join(r.opts.Dir, p.Row.Name), p.Row.Name, p.Row.UISHA256, p.Manifest); err != nil {
			return err
		}
	}
	p.mu.Lock()
	p.ui = b
	p.mu.Unlock()
	return nil
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
			Message: "the manifest asks for the schedule permission, but the module has no tick export - an app that cannot be woken must not ask to be"}
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
//
// ⚠⚠ Every way the module runs passes here - a screen and its events
// (ViewEvent), a queued job (runJob, possibly queued before a hold), a
// wake-up, an interface call, a public page, a thumbnail - so a license hold
// is refused HERE, once, and not only in State(): a caller that skips State()
// still cannot run a held app.
func (p *Installed) running() (*Compiled, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if p.hold != "" {
		return nil, &CallError{Code: CodeUnsupported, Message: "plugin is not running: " + p.hold}
	}
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
	// ManifestURL: where a URL install reads its manifest (the update check's
	// address; SourceURL is the module's for an app with one).
	ManifestURL string   `json:"manifest_url,omitempty"`
	SHA256      string   `json:"sha256"`
	Signed      bool     `json:"signed"`
	Permissions []string `json:"permissions"`
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
	// Compat is the manifest's `filex` range against the running filex
	// (compat.go); absent when it names none. `ok: false` on an installed app
	// is a warning, not a stop: it keeps running (Load says why).
	Compat *Compat `json:"compat,omitempty"`
	// UpdateSource is where newer versions are looked for: github | url, or
	// absent when there is nowhere to ask (an uploaded app).
	UpdateSource string `json:"update_source,omitempty"`
	// Update is what the last check found; absent before the first one.
	Update *UpdateInfo `json:"update,omitempty"`
	// UpdateSaid is the line the Apps list shows about the app's updates,
	// in the reader's language (handlers sayStatus, 0.55): a newer version
	// that needs another filex, what an approval adds, an automatic update
	// undone, a source that could not be read, or no source to check. Empty
	// where a status word says it all (up to date, available).
	UpdateSaid string `json:"update_said,omitempty"`
	// Engine: the app runs a WebAssembly module. False for a language pack
	// and for an app that is only an interface.
	Engine bool `json:"engine"`
	// UI describes the app's own interface; absent when it has none.
	UI *UIInfo `json:"ui,omitempty"`
	// Previous is the version an upgrade replaced, kept to go back to
	// (versions.go); absent when none is kept.
	Previous  *PreviousVersion `json:"previous,omitempty"`
	CreatedAt time.Time        `json:"created_at"`
	UpdatedAt time.Time        `json:"updated_at"`
}

// StatusOf builds the list row. ⚠ Nil-safe receiver: the wire-fixture test
// calls it on a nil registry, which then reports languages without coverage.
func (r *Registry) StatusOf(p *Installed) *Status {
	state, serr := p.State()
	perms := make([]string, 0, len(p.Perms))
	for _, x := range p.Perms {
		perms = append(perms, string(x))
	}
	var upd *UpdateInfo
	if info := updateInfoOf(p.Row); info.Status != "" || info.Auto != nil {
		upd = &info
	}
	src, _ := sourceOf(p)
	return &Status{
		ID: p.Row.ID, Name: p.Row.Name, Version: p.Row.Version,
		Label: p.Manifest.Label, Description: p.Manifest.Description, Icon: p.Manifest.Icon, Homepage: p.Manifest.Homepage,
		Enabled: p.Row.Enabled, State: state, StateError: serr,
		Source: p.Row.Source, SourceURL: p.Row.SourceURL, ManifestURL: p.Row.ManifestURL, SHA256: p.Row.SHA256, Signed: p.Row.Signed,
		Permissions: perms, Scheduled: p.Grants.Has(PermSchedule),
		Actions: len(p.Manifest.Actions), Views: len(p.Manifest.Views), PublicPages: len(p.Manifest.PublicPages),
		Kind: kindOf(p.Manifest), Languages: r.LanguageRows(p.Manifest),
		Compat: compatOf(p.Manifest), UpdateSource: src.Kind, Update: upd,
		Engine: p.HasModule(), UI: p.uiInfo(), Previous: p.previousInfo(),
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
		reason := m.PermissionReasons[string(p)]
		if len(reason) == 0 {
			reason = m.uiReasons[p]
		}
		out = append(out, PermissionRow{ID: string(p), Label: p.Label(lang), Reason: reason})
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
	// Requires and Filex narrow an `incompatible`: the range the manifest
	// declares and the filex it leaves out (compat.go).
	Requires string
	Filex    string
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
	// ErrCodeIncompatible: the manifest's `filex` range leaves the running
	// filex out (compat.go).
	ErrCodeIncompatible = "incompatible"
	// ErrCodeUpToDate: "upgrade from its source" found nothing newer there
	// (updates.go FetchUpdate).
	ErrCodeUpToDate = "up_to_date"
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
	// FetchReasonBadURL: not an address filex downloads from - not https
	// (plain http only for loopback, with FILEX_PLUGIN_LOOPBACK_SOURCES), or a
	// private or local one (this machine, the private network, the cloud
	// metadata service), by its literal or by what its name resolves to, on
	// any redirect hop.
	FetchReasonBadURL = "bad_url"
	// FetchReasonMissingURL: a URL install without its addresses — the
	// manifest always, the module unless the manifest is a language pack.
	FetchReasonMissingURL = "missing_url"
	// FetchReasonTooLarge: the answer was larger than the cap.
	FetchReasonTooLarge = "too_large"
	// FetchReasonChanged: the source answered a different version between
	// the update check's read and the install's (updates.go).
	FetchReasonChanged = "changed"
)

// InstallInput is one install or upgrade request after the HTTP layer has
// gathered the pieces.
type InstallInput struct {
	Manifest []byte
	// Wasm is the module; Reader is consumed, capped at MaxWasmBytes.
	Wasm io.Reader
	// UI is the interface bundle (a zip) when the manifest has a `ui` block;
	// consumed, capped at MaxUIBytes.
	UI io.Reader
	// SHA256 (lower hex) the module must match; "" skips (upload only).
	SHA256    string
	Signature string
	Source    string
	SourceURL string
	// ManifestURL is where a URL install read the manifest — what the update
	// check re-reads (SourceURL is the module's address for an app).
	ManifestURL string
	// Pinned: the administrator gave the SHA-256 themselves (a URL install).
	// "Exactly these bytes" is not something an automatic update may
	// overrule, so such an install starts with automatic updates off.
	Pinned bool
	// Granted is the permission list the admin approved. It must equal the
	// manifest's set exactly.
	Granted []string
	DryRun  bool
	Lang    string
	// ActorID is the administrator who approved this (the audit row names
	// them); nil for the system.
	ActorID *int64
	// Rollback: this is "Back to <version>" (versions.go) — the kept files,
	// under the grant they ran with.
	Rollback bool
	// UIPin pins the interface bundle when the manifest does not (a kept
	// version is held to the hash recorded when it was replaced).
	UIPin string
	// Mirrors are mirrored external files already at hand, by sha256 (a kept
	// version's): used instead of downloading them again.
	Mirrors map[string][]byte
	// Notes are the source's release notes for this version, as plain text,
	// for the review (a GitHub release's body).
	Notes string
	// Placements are the administrator's choices at the review's File types
	// group: where the new app goes for each kind it opens or draws
	// (internal/assoc). The registry does not read them; the HTTP layer
	// writes them once the app is installed and running.
	Placements []assoc.Placement
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
	// Compat is the manifest's `filex` range judged against this filex
	// (compat.go); absent when it names none. `ok: false` is said HERE, at
	// the review, and the install or upgrade itself answers `incompatible`.
	Compat *Compat `json:"compat,omitempty"`
	// Upgrade is what an upgrade's review shows on top of an install's: the
	// version it replaces and how the grant changes. Nil on an install.
	Upgrade *DryRunUpgrade `json:"upgrade,omitempty"`
	// Engine: the app brings a WebAssembly module (false: a language pack, or
	// an app that is only an interface).
	Engine bool `json:"engine"`
	// UI is the app's own interface: the bundle, its script-policy
	// exceptions, and every external address — mirrored or live. The review
	// draws it as its own group, with what a live address means.
	UI *UIInfo `json:"ui,omitempty"`
	// FileTypes are the kinds the app would open or draw thumbnails of, each
	// with who handles it now and where the app lands by default - the
	// review's File types group (internal/assoc, filled by the HTTP layer).
	FileTypes []assoc.InstallKind `json:"file_types,omitempty"`
}

// DryRunUpgrade is the jump an upgrade makes. Added are the permissions the
// new version asks for that the installed one was not granted — what the
// administrator is asked to approve; Removed are the ones it no longer asks
// for (the grant narrows by itself). AddsModule: a language pack that now
// brings a module — code that runs, where there was none.
type DryRunUpgrade struct {
	From       string   `json:"from"`
	Added      []string `json:"added,omitempty"`
	Removed    []string `json:"removed,omitempty"`
	AddsModule bool     `json:"adds_module,omitempty"`
	// What else the version changes, for the review (upgradeReview): the
	// module (its hash), the interface (added, removed, its files), the filex
	// range, the signature, and the source's own notes. The approval rule
	// itself is Added/AddsModule and nothing else.
	ModuleFrom string      `json:"module_from,omitempty"`
	ModuleTo   string      `json:"module_to,omitempty"`
	UIFrom     string      `json:"ui_from,omitempty"`
	UITo       string      `json:"ui_to,omitempty"`
	UIFiles    *UIFileDiff `json:"ui_files,omitempty"`
	FilexFrom  string      `json:"filex_from,omitempty"`
	FilexTo    string      `json:"filex_to,omitempty"`
	SignedFrom bool        `json:"signed_from"`
	SignedTo   bool        `json:"signed_to"`
	Notes      string      `json:"notes,omitempty"`
}

// UIFileDiff is how an upgrade's interface files differ, by name. Each list
// is cut at maxUIFileDiff names; the counts are whole.
type UIFileDiff struct {
	Added        []string `json:"added,omitempty"`
	Removed      []string `json:"removed,omitempty"`
	Changed      []string `json:"changed,omitempty"`
	AddedCount   int      `json:"added_count"`
	RemovedCount int      `json:"removed_count"`
	ChangedCount int      `json:"changed_count"`
}

const maxUIFileDiff = 200

// upgradeOf compares a staged manifest with the installed app: the version it
// leaves, the permissions it adds to and drops from the grant, and whether a
// language pack becomes an app with a module. ONE comparison, for the upgrade
// review and for the update check's decision (updates.go), so "needs
// approval" means the same thing on both.
func upgradeOf(p *Installed, m *Manifest) *DryRunUpgrade {
	u := &DryRunUpgrade{From: p.Row.Version}
	for _, perm := range m.Perms {
		if !p.Grants.Has(perm) {
			u.Added = append(u.Added, string(perm))
		}
	}
	next := NewGrants(m.Perms)
	for _, perm := range p.Perms {
		if !next.Has(perm) {
			u.Removed = append(u.Removed, string(perm))
		}
	}
	u.AddsModule = p.Manifest.IsLanguagePack() && !m.IsLanguagePack()
	return u
}

// upgradeReview fills what the review shows beyond the grant: the module and
// interface hashes, the interface's files, the filex range, the signature and
// the source's notes.
func upgradeReview(u *DryRunUpgrade, p *Installed, st *staged, notes string) {
	if p.HasModule() {
		u.ModuleFrom = p.Row.SHA256
	}
	if st.wasm != nil {
		u.ModuleTo = st.sum
	}
	u.UIFrom, u.UITo = p.Row.UISHA256, st.uiSum()
	u.SignedFrom, u.SignedTo = p.Row.Signed, st.signd
	u.FilexFrom, u.FilexTo = rangeText(p.Manifest), rangeText(st.m)
	u.Notes = notes
	var before, after map[string]uiEntry
	if b := p.UI(); b != nil {
		before = b.idx.files
	}
	if st.ui != nil {
		after = st.ui.idx.files
	}
	if before == nil && after == nil {
		return
	}
	d := &UIFileDiff{}
	for name, e := range after {
		old, ok := before[name]
		switch {
		case !ok:
			d.AddedCount++
			if len(d.Added) < maxUIFileDiff {
				d.Added = append(d.Added, name)
			}
		case old.crc != e.crc || old.usize != e.usize:
			d.ChangedCount++
			if len(d.Changed) < maxUIFileDiff {
				d.Changed = append(d.Changed, name)
			}
		}
	}
	for name := range before {
		if _, ok := after[name]; !ok {
			d.RemovedCount++
			if len(d.Removed) < maxUIFileDiff {
				d.Removed = append(d.Removed, name)
			}
		}
	}
	sort.Strings(d.Added)
	sort.Strings(d.Removed)
	sort.Strings(d.Changed)
	u.UIFiles = d
}

// rangeText is a manifest's filex range as compatRange reads it ("" for
// none).
func rangeText(m *Manifest) string {
	if m == nil {
		return ""
	}
	rng, err := compatRange(m)
	if err != nil {
		return ""
	}
	return rng.text
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
	// Kind is DryRunEngineOffice for the office engine, which is a document
	// server to connect rather than a program to install (and needs no
	// restart once connected); empty for a binary engine.
	Kind string `json:"kind,omitempty"`
}

// DryRunEngineOffice is DryRunEngine.Kind for the office engine.
const DryRunEngineOffice = "office"

// dryRun is the review of a staged install, for Install and Upgrade alike:
// what the app is (a module, or a language pack and its coverage), what it
// may do, and — because the dry run already knows — whether an app of that
// name is installed (installing only) and which engines it asks for that
// this server lacks.
func (r *Registry) dryRun(ctx context.Context, st *staged, lang string, upgrading *Installed, notes string) *DryRunAnswer {
	installing := upgrading == nil
	ans := &DryRunAnswer{
		Manifest: &st.m.Manifest, Permissions: PermissionRows(st.m, lang), Signed: st.signd,
		Kind: kindOf(st.m), Languages: r.LanguageRows(st.m), Compat: compatOf(st.m).Said(lang, st.m.Name, st.m.Version),
	}
	if upgrading != nil {
		ans.Upgrade = upgradeOf(upgrading, st.m)
		upgradeReview(ans.Upgrade, upgrading, st, notes)
	}
	if st.wasm == nil {
		ans.ManifestSHA256 = st.sum
	} else {
		ans.WasmSHA256, ans.WasmBytes = st.sum, int64(len(st.wasm))
	}
	ans.Engine = st.wasm != nil
	if st.m.UI != nil {
		ans.UI = st.ui.info(st.m)
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
		if ok && (r.engines == nil || !r.engines.available(ctx, name)) {
			de := DryRunEngine{ID: name, Name: enginebin.DisplayName(name)}
			if isOffice(name) {
				// Not a program to install: a document server to connect,
				// and no restart after it (the review says so).
				de.Kind = DryRunEngineOffice
			}
			ans.EnginesMissing = append(ans.EnginesMissing, de)
		}
	}
	return ans
}

type staged struct {
	m     *Manifest
	wasm  []byte // nil for a language pack and for an app that is only an interface
	sum   string // the module's sha256 — the manifest's when there is no module
	signd bool
	// ui is the checked interface bundle and its mirrored files; nil when the
	// manifest has no `ui` block.
	ui *stagedUI
}

// stage validates the manifest, the module bytes and the interface bundle,
// without touching disk or the database.
func (r *Registry) stage(ctx context.Context, in *InstallInput) (*staged, error) {
	if r.opts.Demo {
		return nil, installErr(ErrCodeDemo, "installing app plugins is disabled on the demo instance")
	}
	m, err := ParseManifest(in.Manifest)
	if err != nil {
		return nil, installErr(ErrCodeManifestInvalid, err.Error())
	}
	// A range that does not parse is a broken manifest, refused here like
	// any other. Whether the range lets THIS filex in is decided after the
	// review (refuseIncompatible), so the review can say it first.
	if _, err := compatRange(m); err != nil {
		return nil, installErr(ErrCodeManifestInvalid, err.Error())
	}
	if m.IsLanguagePack() {
		if in.UI != nil {
			return nil, installErr(ErrCodeManifestInvalid, "an interface bundle was supplied, but this manifest is a language pack and has no ui block")
		}
		return r.stagePack(m, in)
	}
	var ui *stagedUI
	switch {
	case m.UI != nil:
		if ui, err = r.stageUI(ctx, m, in); err != nil {
			return nil, err
		}
	case in.UI != nil:
		return nil, installErr(ErrCodeManifestInvalid, "an interface bundle was supplied, but the manifest has no ui block")
	}
	if in.Wasm == nil {
		if m.UI != nil && !m.NeedsModule() {
			// ⚠ An app that is ONLY an interface: as for a language pack,
			// the pin and the signature move to the manifest — and the
			// manifest pins the bundle (ui.bundle.sha256), so they reach it.
			sum, signd, err := r.checkIntegrity("manifest", in.Manifest, in.SHA256, in.Signature)
			if err != nil {
				return nil, err
			}
			return &staged{m: m, sum: sum, signd: signd, ui: ui}, nil
		}
		why := "an app with actions, screens, public pages, settings or permissions needs its WebAssembly module (only a manifest that adds languages, or one that is only an interface, installs without one)"
		if m.UI != nil {
			why = "this app has an interface, but it also needs a module: " + m.moduleOnlyReason()
		}
		return nil, installErr(ErrCodeManifestInvalid, "no module supplied - "+why)
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
	return &staged{m: m, wasm: wasm, sum: sum, signd: signd, ui: ui}, nil
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
			return "", false, installErr(ErrCodeSignatureRequired, "this instance only accepts signed apps (FILEX_PLUGIN_TRUSTED_KEYS is set) - supply the detached signature over the "+what+"'s sha256")
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
	st, err := r.stage(ctx, in)
	if err != nil {
		return nil, nil, err
	}
	if in.DryRun {
		return nil, r.dryRun(ctx, st, in.Lang, nil, ""), nil
	}
	if err := refuseIncompatible(st.m); err != nil {
		return nil, nil, err
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
	// ⚠⚠ One install of a name at a time. Two at once (two administrators, a
	// store link and a repository install) both found the name free, both
	// wrote <Dir>/<name>, the second row was refused by the unique name - and
	// that loser's clean-up below removed the directory the WINNER's row
	// points at. Under the lock the second one finds the name taken.
	unlock := r.installLocks.Lock(st.m.Name)
	defer unlock()
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
		WasmPath: st.wasmPath(), SHA256: st.sum, UISHA256: st.uiSum(), Source: sourceOr(in.Source), SourceURL: in.SourceURL, Signed: st.signd,
		ManifestURL: in.ManifestURL, PermissionsJSON: permsJSON, Enabled: true, AutoUpdate: false, Signature: in.Signature,
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
	// ⚠ The module's own state, not State(): a paid app is installed HELD
	// until its license is confirmed (license_hold.go), and a hold is not a
	// module that failed to describe itself.
	if state, serr := p.loadState(); state != StateRunning {
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
	st, err := r.stage(ctx, in)
	if err != nil {
		return nil, nil, err
	}
	if st.m.Name != p.Row.Name {
		return nil, nil, installErr(ErrCodeManifestInvalid, "the new manifest names "+st.m.Name+", the installed plugin is "+p.Row.Name)
	}
	if in.DryRun {
		return nil, r.dryRun(ctx, st, in.Lang, p, in.Notes), nil
	}
	if err := refuseIncompatible(st.m); err != nil {
		return nil, nil, err
	}
	// ⚠ One upgrade at a time, and none once the registry is closing. The
	// update check (updates.go) runs upgrades with nobody watching, so an
	// administrator's own upgrade of the same app can now meet one: both
	// would stash the same directory, and the second would swap out the
	// module the first just compiled. And Close frees the runtime a compile
	// is using — a shutdown waits here for the swap in flight to finish.
	r.upgradeMu.Lock()
	defer r.upgradeMu.Unlock()
	if r.closing {
		return nil, nil, errors.New("app-plugins: the server is shutting down")
	}
	// Re-read under the lock: an upgrade that finished while this one waited
	// replaced the entry.
	if p, ok = r.ByID(id); !ok {
		return nil, nil, installErr(ErrCodeNotFound, "no such plugin")
	}
	granted := in.Granted
	if len(granted) == 0 {
		granted = permStrings(p.Perms)
	}
	if err := r.checkGrant(st.m, granted, ErrCodePermissionsChanged); err != nil {
		return nil, nil, err
	}
	// What the version change is, against the grant it leaves — for the
	// audit row (afterUpgrade).
	up := upgradeOf(p, st.m)
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
	p.Row.UISHA256 = st.uiSum()
	p.Row.Signed = st.signd
	p.Row.Signature = in.Signature
	p.Row.Source = sourceOr(in.Source)
	p.Row.SourceURL = in.SourceURL
	p.Row.ManifestURL = in.ManifestURL
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
	// ⚠ The new module proves itself whether or not the app is switched on:
	// an upgrade that was let through because the app happened to be off
	// left a module that did not describe itself as its manifest says, to be
	// found the day somebody switched it back on (and the update check now
	// upgrades apps that are off, too).
	if state, serr := np.loadState(); state != StateRunning {
		*p.Row = oldRow
		_ = os.RemoveAll(dir)
		_ = os.Rename(backup, dir)
		// ⚠⚠ The failed compile wrote its error through the row it shares
		// with the old entry — the NEW version, manifest and hash included
		// (persistError). Unless the old row is written back, the database
		// says v2 while the disk holds v1 again, and the next start refuses
		// the app for a hash mismatch: a rolled-back upgrade broke the app
		// one restart later.
		if err := r.opts.Store.UpdateAppPlugin(ctx, p.Row); err != nil {
			r.log.Warn("app-plugins: rolled-back row not written", slog.String("plugin", p.Row.Name), slog.Any("err", err))
		}
		if p.Row.Enabled {
			r.compile(ctx, p)
		}
		np.log("error", "upgrade to "+st.m.Version+" rolled back: "+serr)
		return nil, nil, installErr(ErrCodeDescribeMismatch, serr)
	}
	// The upgrade settles what the update check had found, when this version
	// is at least that one (updates.go).
	settleUpdate(p.Row, st.m.Version)
	if err := r.opts.Store.UpdateAppPlugin(ctx, p.Row); err != nil {
		return nil, nil, fmt.Errorf("app-plugins: update row: %w", err)
	}
	p.mu.Lock()
	if p.compiled != nil {
		_ = p.compiled.Close(ctx)
		p.compiled = nil
	}
	p.mu.Unlock()
	p.dropThumbModule(ctx)
	// The version this one replaced is KEPT (versions.go): "Back to …" puts
	// it back without a new approval.
	r.keepPrevious(ctx, &oldRow, backup, in.ActorID)
	r.put(np)
	r.loadPrevious(ctx, np)
	if !np.Row.Enabled {
		// Proven, and still off: what the administrator switched off stays off.
		r.unload(ctx, np)
	}
	if in.Rollback {
		np.log("info", "back to "+st.m.Version+" (from "+oldRow.Version+")")
	} else {
		np.log("info", "upgraded to "+st.m.Version)
	}
	r.afterUpgrade(ctx, np, &oldRow, in, up)
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
	return writeUI(dir, st.ui)
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
		r.unload(ctx, p)
	}
	return r.StatusOf(p), nil
}

// unload frees an app's module and marks it off.
func (r *Registry) unload(ctx context.Context, p *Installed) {
	p.mu.Lock()
	if p.compiled != nil {
		_ = p.compiled.Close(ctx)
		p.compiled = nil
	}
	p.mu.Unlock()
	p.dropThumbModule(ctx)
	p.setState(StateDisabled, "")
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
	p.dropThumbModule(ctx)
	r.drop(p)
	if err := r.opts.Store.DeleteAppPlugin(ctx, id); err != nil {
		return err
	}
	// Its thumbnail limits go with it (00077).
	_ = r.opts.Store.DeleteAppThumbLimits(ctx, id)
	_ = os.RemoveAll(filepath.Join(r.opts.Dir, p.Row.Name))
	// Its downloads go with it (asset_fetch).
	_ = os.RemoveAll(r.assetDir(p.Row.Name))
	// ...and so do the versions it kept to go back to.
	_ = os.RemoveAll(r.versionsRoot(p.Row.Name))
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
//
// ⚠ Every value sent is judged against its manifest Field first
// (CheckSetting: type, options, min/max, a required field left empty) and
// the whole save is refused with a *SettingError naming the first field that
// does not fit — nothing is stored. The admin panel draws these fields with
// the same rules, but an API key or a crafted request never ran that screen,
// and an app reads its settings trusting the types its manifest declared.
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
		if !(f.Secret && v == secretMask) {
			if prob := CheckSetting(f, v); prob != nil {
				return &SettingError{FieldProblem: *prob}
			}
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
	engines := r.enginesFor(ctx, p)
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
		need := Need{Kind: "engine", ID: n, Name: enginebin.DisplayName(n)}
		if isOffice(n) {
			// Connected, not installed: the explorer says what to connect.
			need.Kind = "office"
		}
		out = append(out, GatedRule{Ext: extra, Needs: need})
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
