package plugin

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/secretbox"
	"github.com/brf-tech/filex/backend/internal/storage"
)

// Runtime states a plugin can be in, as shown to the admin.
const (
	StateDisabled = "disabled" // row says off; nothing runs
	StateStarting = "starting" // launched, no handshake/describe yet
	StateRunning  = "running"  // described, driver registered
	StateFailed   = "failed"   // last attempt failed; a binary is backing off, a remote is re-checked
	StateRefused  = "refused"  // describe rejected by the host; not retried until restart
)

// Status is one plugin as the admin sees it: the row plus what is happening.
type Status struct {
	*model.Plugin
	State        string        `json:"state"`
	StateError   string        `json:"state_error,omitempty"`
	Restarts     int           `json:"restarts"`
	Capabilities *Capabilities `json:"capabilities,omitempty"`
	Label        string        `json:"label,omitempty"`
	FieldCount   int           `json:"field_count"`
	// InUse counts storage rows on this plugin's driver — shown before a
	// remove so the admin knows what will stop working.
	InUse int `json:"in_use"`
}

// Options configure a Manager.
type Options struct {
	Store db.Store
	// Dir is where binary plugins live: <data-dir>/plugins.
	Dir string
	// SecretKey seals remote plugins' bearer tokens (secretbox). Empty means
	// a remote plugin cannot be registered — the token would have to be
	// stored in plaintext, and this codebase does not do that.
	SecretKey string
	Log       *slog.Logger
	// MaxBinaryBytes caps an uploaded/downloaded plugin. 0 → 512 MiB.
	MaxBinaryBytes int64
	// HTTP downloads plugins from URLs; nil → http.DefaultClient with a
	// timeout.
	HTTP *http.Client
}

// Manager owns every plugin's lifecycle and its place in the storage
// registry. One per server.
type Manager struct {
	store db.Store
	dir   string
	box   *secretbox.Box
	log   *slog.Logger
	maxB  int64
	http  *http.Client

	ctx    context.Context
	cancel context.CancelFunc

	mu      sync.Mutex
	entries map[int64]*entry
	// drivers maps DriverPrefix+name → plugin id, so two plugins cannot both
	// claim "plugin:foo".
	drivers map[string]int64
}

// entry is one plugin's runtime.
type entry struct {
	m   *Manager
	row *model.Plugin

	mu       sync.Mutex
	proc     *Process // binary
	client   *Client  // remote, or the binary's current client
	desc     *DescribeResponse
	state    string
	stateErr string
	stopFn   context.CancelFunc // remote checker / binary supervisor ctx
}

// Handle implementation ─────────────────────────────────────────────────────

func (e *entry) Client() (*Client, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.state == StateRunning && e.client != nil {
		return e.client, nil
	}
	if e.stateErr != "" {
		return nil, fmt.Errorf("plugin %s is %s: %s", e.row.Name, e.state, e.stateErr)
	}
	return nil, fmt.Errorf("plugin %s is %s", e.row.Name, e.state)
}

func (e *entry) DriverName() string {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.desc != nil {
		return DriverPrefix + e.desc.Name
	}
	return DriverPrefix + e.row.Driver
}

// New builds a Manager. Call Load to start what the database says.
func New(o Options) (*Manager, error) {
	if o.Store == nil || o.Dir == "" {
		return nil, errors.New("plugin: store and dir are required")
	}
	box, err := secretbox.New(o.SecretKey)
	if err != nil {
		return nil, err
	}
	if o.Log == nil {
		o.Log = slog.Default()
	}
	if o.MaxBinaryBytes <= 0 {
		o.MaxBinaryBytes = 512 << 20
	}
	if o.HTTP == nil {
		o.HTTP = &http.Client{Timeout: 10 * time.Minute}
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &Manager{
		store: o.Store, dir: o.Dir, box: box, log: o.Log, maxB: o.MaxBinaryBytes, http: o.HTTP,
		ctx: ctx, cancel: cancel,
		entries: map[int64]*entry{}, drivers: map[string]int64{},
	}, nil
}

// Dir is the plugins directory.
func (m *Manager) Dir() string { return m.dir }

// Load starts every enabled plugin from the database. Start-up is
// asynchronous; WaitReady bounds how long the caller waits for the first
// describes so pre-warmed storages find their drivers.
func (m *Manager) Load(ctx context.Context) error {
	if err := os.MkdirAll(m.dir, 0o755); err != nil {
		return fmt.Errorf("plugin: mkdir %s: %w", m.dir, err)
	}
	rows, err := m.store.ListPlugins(ctx)
	if err != nil {
		return err
	}
	for _, row := range rows {
		m.ensureEntry(row)
	}
	m.mu.Lock()
	es := make([]*entry, 0, len(m.entries))
	for _, e := range m.entries {
		es = append(es, e)
	}
	m.mu.Unlock()
	for _, e := range es {
		if e.row.Enabled {
			m.start(e)
		}
	}
	return nil
}

// WaitReady blocks until every enabled plugin has left StateStarting or d
// elapsed. It exists for the server's start-up: storages on a plugin driver
// are pre-warmed right after, and would otherwise all log "unknown driver".
func (m *Manager) WaitReady(d time.Duration) {
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		pending := false
		m.mu.Lock()
		for _, e := range m.entries {
			e.mu.Lock()
			if e.row.Enabled && e.state == StateStarting {
				pending = true
			}
			e.mu.Unlock()
		}
		m.mu.Unlock()
		if !pending {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// Shutdown stops every plugin. Called once, on server shutdown.
func (m *Manager) Shutdown() {
	m.cancel()
	m.mu.Lock()
	es := make([]*entry, 0, len(m.entries))
	for _, e := range m.entries {
		es = append(es, e)
	}
	m.mu.Unlock()
	for _, e := range es {
		m.stop(e)
	}
}

func (m *Manager) ensureEntry(row *model.Plugin) *entry {
	m.mu.Lock()
	defer m.mu.Unlock()
	if e, ok := m.entries[row.ID]; ok {
		e.mu.Lock()
		e.row = row
		e.mu.Unlock()
		return e
	}
	e := &entry{m: m, row: row, state: StateDisabled}
	m.entries[row.ID] = e
	return e
}

// ── lifecycle ───────────────────────────────────────────────────────────────

func (m *Manager) start(e *entry) {
	e.mu.Lock()
	if e.stopFn != nil {
		e.mu.Unlock()
		return // already running
	}
	ctx, cancel := context.WithCancel(m.ctx)
	e.stopFn = cancel
	e.state, e.stateErr = StateStarting, ""
	row := e.row
	e.mu.Unlock()

	switch row.Kind {
	case model.PluginKindRemote:
		go m.runRemote(ctx, e)
	default:
		m.runBinary(ctx, e)
	}
}

func (m *Manager) stop(e *entry) {
	e.mu.Lock()
	cancel := e.stopFn
	e.stopFn = nil
	proc := e.proc
	e.proc = nil
	client := e.client
	e.client = nil
	e.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if proc != nil {
		proc.Stop()
	}
	if client != nil {
		client.Close()
	}
	m.unregister(e)
	e.mu.Lock()
	e.state, e.stateErr = StateDisabled, ""
	e.mu.Unlock()
}

// runBinary verifies the file, then hands it to a Process.
func (m *Manager) runBinary(ctx context.Context, e *entry) {
	row := e.row
	bin := filepath.Join(m.dir, row.Name, row.Binary)
	if err := m.checkBinary(bin, row.SHA256); err != nil {
		m.setFailed(e, StateRefused, err)
		return
	}
	token, err := mintToken()
	if err != nil {
		m.setFailed(e, StateFailed, err)
		return
	}
	proc := &Process{
		Name:    row.Name,
		Binary:  bin,
		Token:   token,
		SockDir: filepath.Join(m.dir, row.Name, "run"),
		Log:     m.log,
	}
	proc.OnUp = func(ctx context.Context, c *Client) error {
		return m.adopt(ctx, e, c)
	}
	proc.OnDown = func(err error) {
		m.unregister(e)
		e.mu.Lock()
		e.client = nil
		if errors.Is(err, ErrRefused) {
			e.state = StateRefused
		} else if errors.Is(err, ErrStopped) {
			e.state = StateDisabled
		} else {
			e.state = StateFailed
		}
		if err != nil {
			e.stateErr = err.Error()
		}
		e.mu.Unlock()
		if err != nil && !errors.Is(err, ErrStopped) {
			m.persistError(e, err)
		}
	}
	e.mu.Lock()
	e.proc = proc
	e.mu.Unlock()
	proc.Start(ctx)
}

// runRemote connects, describes, and re-checks every 30s while the entry is
// enabled — a remote that goes away is reported failed and re-adopted when
// it is back.
func (m *Manager) runRemote(ctx context.Context, e *entry) {
	row := e.row
	token, err := m.box.Open(row.TokenSealed)
	if err != nil {
		m.setFailed(e, StateRefused, fmt.Errorf("token cannot be opened (FILEX_SECRET_KEY changed?): %w", err))
		return
	}
	addr, err := ParseAddress(row.Address)
	if err != nil {
		m.setFailed(e, StateRefused, err)
		return
	}
	client := NewClient(addr, token)
	backoff := 5 * time.Second
	for {
		if ctx.Err() != nil {
			client.Close()
			return
		}
		e.mu.Lock()
		running := e.state == StateRunning
		e.mu.Unlock()
		if !running {
			upCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
			err := m.adopt(upCtx, e, client)
			cancel()
			if err != nil {
				state := StateFailed
				var pe *pluginError
				if !errors.As(err, &pe) && !isNetErr(err) {
					state = StateRefused // validation, not connectivity
				}
				m.setFailed(e, state, err)
				if state == StateRefused {
					client.Close()
					return
				}
			} else {
				backoff = 5 * time.Second
			}
		} else {
			hcCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
			_, err := client.Describe(hcCtx)
			cancel()
			if err != nil {
				m.unregister(e)
				m.setFailed(e, StateFailed, err)
			}
		}
		select {
		case <-time.After(backoff):
		case <-ctx.Done():
			client.Close()
			return
		}
		if backoff < time.Minute {
			backoff += 5 * time.Second
		}
	}
}

func isNetErr(err error) bool {
	s := err.Error()
	return strings.Contains(s, "connection refused") || strings.Contains(s, "no such host") ||
		strings.Contains(s, "timeout") || strings.Contains(s, "EOF") || strings.Contains(s, "reset by peer")
}

// adopt describes a freshly-up plugin and, if acceptable, registers its
// driver. Called from the supervisor (binary) or the checker (remote).
func (m *Manager) adopt(ctx context.Context, e *entry, c *Client) error {
	desc, err := c.Describe(ctx)
	if err != nil {
		return err
	}
	driver := DriverPrefix + desc.Name
	m.mu.Lock()
	if owner, taken := m.drivers[driver]; taken && owner != e.row.ID {
		m.mu.Unlock()
		return fmt.Errorf("driver %q is already provided by another plugin (id %d)", driver, owner)
	}
	if _, builtin := builtinDriver(driver); builtin {
		m.mu.Unlock()
		return fmt.Errorf("driver %q collides with a built-in driver", driver)
	}
	m.drivers[driver] = e.row.ID
	m.mu.Unlock()

	e.mu.Lock()
	e.desc = desc
	e.client = c
	e.state, e.stateErr = StateRunning, ""
	row := e.row
	e.mu.Unlock()

	// Register (idempotently) — a restart re-registers the same name.
	caps := desc.Capabilities
	storage.Unregister(driver)
	storage.UnregisterDescriptor(driver)
	storage.Register(driver, func() storage.Driver { return NewDriver(e, caps) })
	fields := make([]storage.Field, len(desc.Fields))
	copy(fields, desc.Fields)
	for i := range fields {
		if fields[i].I18nKey == "" {
			fields[i].I18nKey = "plugin." + desc.Name + "." + fields[i].Key
		}
	}
	storage.RegisterDescriptor(storage.Descriptor{
		Driver:  driver,
		Label:   desc.Label,
		I18nKey: "plugin." + desc.Name + ".label",
		Fields:  fields,
	})

	// Remember what it described, so a stopped plugin still shows its
	// driver and version in the list.
	if row.Driver != desc.Name || row.Version != desc.Version || row.LastError != "" {
		row.Driver, row.Version, row.LastError = desc.Name, desc.Version, ""
		if err := m.store.UpdatePlugin(context.Background(), row); err != nil {
			m.log.Warn("plugin: persist describe", slog.String("plugin", row.Name), slog.Any("err", err))
		}
	}
	m.log.Info("plugin up", slog.String("plugin", row.Name), slog.String("driver", driver), slog.String("version", desc.Version))
	return nil
}

func builtinDriver(name string) (string, bool) {
	// Anything without the prefix would be a built-in; the prefix rule alone
	// prevents the collision, this is belt-and-braces for a future rename.
	if !strings.HasPrefix(name, DriverPrefix) {
		return name, true
	}
	return name, false
}

func (m *Manager) unregister(e *entry) {
	e.mu.Lock()
	desc := e.desc
	e.mu.Unlock()
	if desc == nil {
		return
	}
	driver := DriverPrefix + desc.Name
	m.mu.Lock()
	if owner, ok := m.drivers[driver]; ok && owner == e.row.ID {
		delete(m.drivers, driver)
		storage.Unregister(driver)
		storage.UnregisterDescriptor(driver)
	}
	m.mu.Unlock()
}

func (m *Manager) setFailed(e *entry, state string, err error) {
	e.mu.Lock()
	e.state = state
	e.stateErr = err.Error()
	e.mu.Unlock()
	m.log.Warn("plugin", slog.String("plugin", e.row.Name), slog.String("state", state), slog.Any("err", err))
	m.persistError(e, err)
}

func (m *Manager) persistError(e *entry, err error) {
	e.mu.Lock()
	row := e.row
	e.mu.Unlock()
	msg := err.Error()
	if len(msg) > 1000 {
		msg = msg[:1000]
	}
	if row.LastError == msg {
		return
	}
	row.LastError = msg
	if uerr := m.store.UpdatePlugin(context.Background(), row); uerr != nil {
		m.log.Warn("plugin: persist error", slog.String("plugin", row.Name), slog.Any("err", uerr))
	}
}

// checkBinary refuses a file that is not what was installed.
func (m *Manager) checkBinary(path, want string) error {
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("binary missing: %w", err)
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return err
	}
	if got := hex.EncodeToString(h.Sum(nil)); !strings.EqualFold(got, want) {
		return fmt.Errorf("binary changed on disk since it was installed (sha256 %s, expected %s) — reinstall it", got[:12], want[:12])
	}
	return nil
}

// execName is the file name a plugin binary is stored under.
//
// ⚠⚠ On Windows the EXTENSION is what makes a file executable. A plugin
// uploaded as `memfs` (which is exactly what a Linux-shaped build is called,
// and what a browser sends for a file with no extension) was stored as
// `memfs`, and CreateProcess then refused it — Go reports that as
// `executable file not found in %PATH%`, which reads like the file is
// missing when it is sitting right there. Measured on Windows 11,
// 2026-08-19. So: give it .exe unless it already carries an executable
// extension.
//
// Everywhere else the name is taken as-is (with its directory stripped): a
// Unix executable's name is its permission bits, not its suffix.
func execName(filename, goos string) string {
	filename = filepath.Base(strings.TrimSpace(filename))
	if filename == "" || filename == "." || filename == string(filepath.Separator) || filename == ".." {
		filename = "plugin"
	}
	if goos != "windows" {
		return filename
	}
	switch strings.ToLower(filepath.Ext(filename)) {
	case ".exe", ".bat", ".cmd", ".com":
		return filename
	}
	return filename + ".exe"
}

func mintToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// ── admin operations ────────────────────────────────────────────────────────

// ValidName is the operator-chosen plugin name rule (same as the driver's).
func ValidName(s string) bool { return validName(s) }

var ErrBadName = errors.New("plugin name must match [a-z0-9][a-z0-9_-]{0,31}")

// InstallBinary stores an uploaded plugin binary and starts it. filename is
// the name the file will have inside the plugin's directory (its basename
// is used; an empty one becomes "plugin" or "plugin.exe").
func (m *Manager) InstallBinary(ctx context.Context, name, filename string, r io.Reader) (*Status, error) {
	if !validName(name) {
		return nil, ErrBadName
	}
	if _, err := m.store.GetPluginByName(ctx, name); err == nil {
		return nil, fmt.Errorf("plugin %q already exists", name)
	}
	filename = execName(filename, runtime.GOOS)
	dir := filepath.Join(m.dir, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	dst := filepath.Join(dir, filename)
	sum, err := writeBinary(dst, r, m.maxB)
	if err != nil {
		_ = os.RemoveAll(dir)
		return nil, err
	}
	row := &model.Plugin{Name: name, Kind: model.PluginKindBinary, Binary: filename, SHA256: sum, Enabled: true}
	row, err = m.store.CreatePlugin(ctx, row)
	if err != nil {
		_ = os.RemoveAll(dir)
		return nil, err
	}
	e := m.ensureEntry(row)
	m.start(e)
	return m.statusOf(ctx, e), nil
}

// InstallFromURL downloads a plugin binary. sha256 is REQUIRED: a URL is
// not a promise about bytes, and the operator has to say what they expect
// to receive — the same rule the self-update follows.
func (m *Manager) InstallFromURL(ctx context.Context, name, rawURL, sha string) (*Status, error) {
	if !validName(name) {
		return nil, ErrBadName
	}
	sha = strings.ToLower(strings.TrimSpace(sha))
	if len(sha) != 64 {
		return nil, errors.New("sha256 is required (64 hex characters) when installing from a URL")
	}
	if !strings.HasPrefix(rawURL, "https://") && !strings.HasPrefix(rawURL, "http://") {
		return nil, errors.New("url must be http(s)://")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	resp, err := m.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("download: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("download: http %d", resp.StatusCode)
	}
	filename := filepath.Base(req.URL.Path)
	st, err := m.InstallBinary(ctx, name, filename, resp.Body)
	if err != nil {
		return nil, err
	}
	if !strings.EqualFold(st.SHA256, sha) {
		// Wrong bytes: undo everything, say what arrived.
		_ = m.Remove(ctx, st.ID)
		return nil, fmt.Errorf("sha256 mismatch: downloaded %s, expected %s", st.SHA256[:12], sha[:12])
	}
	return st, nil
}

// InstallRemote registers a plugin filex connects to rather than runs.
func (m *Manager) InstallRemote(ctx context.Context, name, address, token string) (*Status, error) {
	if !validName(name) {
		return nil, ErrBadName
	}
	if _, err := m.store.GetPluginByName(ctx, name); err == nil {
		return nil, fmt.Errorf("plugin %q already exists", name)
	}
	if _, err := ParseAddress(address); err != nil {
		return nil, err
	}
	if !strings.HasPrefix(address, "http://") && !strings.HasPrefix(address, "https://") {
		return nil, errors.New("a remote plugin address must be an http(s):// URL")
	}
	if strings.TrimSpace(token) == "" {
		return nil, errors.New("a remote plugin needs the bearer token it expects")
	}
	if !m.box.Enabled() {
		return nil, errors.New("FILEX_SECRET_KEY is not set: the remote plugin's token cannot be stored (filex does not store secrets in plaintext)")
	}
	sealed, err := m.box.Seal(token)
	if err != nil {
		return nil, err
	}
	row := &model.Plugin{Name: name, Kind: model.PluginKindRemote, Address: address, TokenSealed: sealed, Enabled: true}
	row, err = m.store.CreatePlugin(ctx, row)
	if err != nil {
		return nil, err
	}
	e := m.ensureEntry(row)
	m.start(e)
	return m.statusOf(ctx, e), nil
}

// SetEnabled turns a plugin on or off, persistently.
func (m *Manager) SetEnabled(ctx context.Context, id int64, on bool) (*Status, error) {
	e, err := m.entryFor(ctx, id)
	if err != nil {
		return nil, err
	}
	e.mu.Lock()
	row := e.row
	e.mu.Unlock()
	if row.Enabled != on {
		row.Enabled = on
		if err := m.store.UpdatePlugin(ctx, row); err != nil {
			return nil, err
		}
	}
	if on {
		m.start(e)
	} else {
		m.stop(e)
	}
	return m.statusOf(ctx, e), nil
}

// Restart stops and starts a plugin — the way out of StateRefused after the
// operator fixed the binary or the remote.
func (m *Manager) Restart(ctx context.Context, id int64) (*Status, error) {
	e, err := m.entryFor(ctx, id)
	if err != nil {
		return nil, err
	}
	m.stop(e)
	e.mu.Lock()
	on := e.row.Enabled
	e.mu.Unlock()
	if on {
		m.start(e)
	}
	return m.statusOf(ctx, e), nil
}

// Remove stops the plugin, unregisters its driver, deletes its files and its
// row. Storages on its driver are left in place and simply fail to open
// until the plugin is back — deleting an admin's storages is not this
// function's decision to make.
func (m *Manager) Remove(ctx context.Context, id int64) error {
	e, err := m.entryFor(ctx, id)
	if err != nil {
		return err
	}
	m.stop(e)
	m.mu.Lock()
	delete(m.entries, id)
	m.mu.Unlock()
	if e.row.Kind == model.PluginKindBinary {
		_ = os.RemoveAll(filepath.Join(m.dir, e.row.Name))
	}
	return m.store.DeletePlugin(ctx, id)
}

// List returns every plugin with its runtime state, name-sorted.
func (m *Manager) List(ctx context.Context) ([]*Status, error) {
	rows, err := m.store.ListPlugins(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]*Status, 0, len(rows))
	for _, row := range rows {
		e := m.ensureEntry(row)
		out = append(out, m.statusOf(ctx, e))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// Get returns one plugin's status.
func (m *Manager) Get(ctx context.Context, id int64) (*Status, error) {
	e, err := m.entryFor(ctx, id)
	if err != nil {
		return nil, err
	}
	return m.statusOf(ctx, e), nil
}

func (m *Manager) entryFor(ctx context.Context, id int64) (*entry, error) {
	m.mu.Lock()
	e, ok := m.entries[id]
	m.mu.Unlock()
	if ok {
		return e, nil
	}
	row, err := m.store.GetPlugin(ctx, id)
	if err != nil {
		return nil, err
	}
	return m.ensureEntry(row), nil
}

func (m *Manager) statusOf(ctx context.Context, e *entry) *Status {
	e.mu.Lock()
	rowCopy := *e.row
	st := &Status{Plugin: &rowCopy, State: e.state, StateError: e.stateErr}
	if e.proc != nil {
		st.Restarts = e.proc.Restarts()
	}
	if e.desc != nil {
		c := e.desc.Capabilities
		st.Capabilities = &c
		st.Label = e.desc.Label
		st.FieldCount = len(e.desc.Fields)
	}
	driver := rowCopy.Driver
	e.mu.Unlock()
	if driver != "" {
		if rows, err := m.store.ListStorages(ctx); err == nil {
			for _, s := range rows {
				if s.Driver == DriverPrefix+driver {
					st.InUse++
				}
			}
		}
	}
	return st
}

// writeBinary streams r to path (0755), capped, returning its sha256.
func writeBinary(path string, r io.Reader, maxBytes int64) (string, error) {
	tmp := path + ".partial"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		return "", err
	}
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(f, h), io.LimitReader(r, maxBytes+1))
	cerr := f.Close()
	if err != nil {
		_ = os.Remove(tmp)
		return "", err
	}
	if cerr != nil {
		_ = os.Remove(tmp)
		return "", cerr
	}
	if n == 0 {
		_ = os.Remove(tmp)
		return "", errors.New("empty file")
	}
	if n > maxBytes {
		_ = os.Remove(tmp)
		return "", fmt.Errorf("plugin binary larger than %d MiB", maxBytes>>20)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
