package plugin

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/metrics"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/pluginlog"
	"github.com/brf-tech/filex/backend/internal/secretbox"
	"github.com/brf-tech/filex/backend/internal/storage"

	"github.com/brf-tech/filex/backend/internal/netguard"
)

// Runtime states a plugin can be in, as shown to the admin.
const (
	StateDisabled = "disabled" // row says off; nothing runs
	StateStarting = "starting" // launched, no handshake/describe yet
	StateRunning  = "running"  // described, driver registered
	StateFailed   = "failed"   // last attempt failed; a binary is backing off, a remote is re-checked
	StateRefused  = "refused"  // describe or conformance rejected it; not retried until restart
)

// Conformance modes (FILEX_PLUGIN_CONFORMANCE).
const (
	// ConformanceEnforce is the default: a plugin that fails its own claims
	// is refused, so nobody can build a storage on half a driver.
	ConformanceEnforce = "enforce"
	// ConformanceWarn registers the plugin anyway and keeps the report. For
	// an operator debugging a plugin they are writing — never the default,
	// because the cost of a broken claim is paid by the user, who reads it as
	// filex being broken.
	ConformanceWarn = "warn"
	// ConformanceOff skips the probes entirely.
	ConformanceOff = "off"
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
	// Conformance is the last verification of the plugin's own claims. Nil
	// when it has not been probed (an older row, or probing is off).
	Conformance *Report `json:"conformance,omitempty"`
	// Load is what this plugin is doing right now: in-flight operations, and
	// how often callers have had to wait or been refused a slot. A plugin
	// that is merely SLOW shows up here long before it shows up as an error.
	Load Stats `json:"load"`
	// Update is what the last check of the plugin's source found
	// (updates.go); absent for a plugin without a source, or before the
	// first check.
	Update *UpdateInfo `json:"update,omitempty"`
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
	// HTTP downloads plugins from URLs. Nil → the shared guarded download
	// client (netguard.DownloadClient) that refuses private, loopback and
	// link-local targets (after DNS, and on every redirect hop) and a
	// redirect from https to plain http - a plugin URL must not become a probe
	// of the server's own network. Supplying a client lifts that guard; it
	// exists for tests and for an embedder that has its own egress policy.
	HTTP *http.Client
	// LoopbackSources lets the guarded client reach this machine
	// (FILEX_PLUGIN_LOOPBACK_SOURCES) - development and end-to-end tests
	// only. Never the private network.
	LoopbackSources bool
	// Conformance is enforce (default), warn or off.
	Conformance string
	// MaxInFlight bounds concurrent operations per plugin (0 → the default).
	MaxInFlight int
	// TrustedKeys are ed25519 public keys (hex or base64) that may sign a
	// plugin binary. When ANY key is configured, an unsigned or badly signed
	// binary is refused at install: the sha256 only proves the file has not
	// changed since it arrived, never that it came from someone you trust.
	TrustedKeys []string
	// Platform is the GOOS/GOARCH key a source's build is chosen by
	// (updates.go). Empty = the running one; tests set it.
	Platform string
}

// Manager owns every plugin's lifecycle and its place in the storage
// registry. One per server.
type Manager struct {
	store       db.Store
	dir         string
	box         *secretbox.Box
	log         *slog.Logger
	maxB        int64
	http        *http.Client
	conf        string
	maxInFlight int
	trusted     []ed25519.PublicKey
	// guardDownloads is the address policy of the guarded default client,
	// nil when the embedder supplied its own; the pre-dial literal-IP check
	// in InstallFromURL follows the same switch.
	guardDownloads *netguard.Policy

	ctx    context.Context
	cancel context.CancelFunc

	// updates: the source check's wiring and schedule (updates.go).
	updates updateState

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
	lim *limiter

	mu       sync.Mutex
	proc     *Process // binary
	client   *Client  // remote, or the binary's current client
	desc     *DescribeResponse
	report   *Report
	state    string
	stateErr string
	stopFn   context.CancelFunc // remote checker / binary supervisor ctx

	// logs is what the plugin's admin page shows (GET
	// /api/admin/plugins/{id}/logs): its starts and failures, and the
	// storage sync's answers it could not make sense of (issue #104). The same
	// helper as the app plugins' log (internal/pluginlog): a repeated line is
	// counted, not written again, and reaches the server log at most once
	// every five minutes.
	logs *pluginlog.Log
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

// limits and metricName make an entry a limitedHandle: every operation a
// storage performs on this plugin passes through the same ceiling and lands
// on the same Prometheus labels.
func (e *entry) limits() *limiter { return e.lim }

func (e *entry) metricName() string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.row.Name
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
	// Absolute, because a launched plugin is handed its socket directory and
	// runs in its own folder: a relative FILEX_DATA_DIR would put its socket
	// somewhere else than where the handshake is held to.
	if abs, err := filepath.Abs(o.Dir); err == nil {
		o.Dir = abs
	}
	var guardDownloads *netguard.Policy
	if o.HTTP == nil {
		p := netguard.Policy{Loopback: o.LoopbackSources}
		guardDownloads = &p
		o.HTTP = p.DownloadClient(10 * time.Minute)
	}
	var trusted []ed25519.PublicKey
	for _, k := range o.TrustedKeys {
		pub, err := parsePublicKey(k)
		if err != nil {
			return nil, fmt.Errorf("plugin: trusted key %q: %w", short(k), err)
		}
		trusted = append(trusted, pub)
	}
	ctx, cancel := context.WithCancel(context.Background())
	switch o.Conformance {
	case ConformanceWarn, ConformanceOff:
	default:
		o.Conformance = ConformanceEnforce
	}
	return &Manager{
		store: o.Store, dir: o.Dir, box: box, log: o.Log, maxB: o.MaxBinaryBytes, http: o.HTTP,
		conf: o.Conformance, maxInFlight: o.MaxInFlight, trusted: trusted, guardDownloads: guardDownloads,
		ctx: ctx, cancel: cancel,
		entries: map[int64]*entry{}, drivers: map[string]int64{},
		updates: updateState{platform: o.Platform},
	}, nil
}

// Dir is the plugins directory.
func (m *Manager) Dir() string { return m.dir }

// MaxBinaryBytes is the largest binary an install or upgrade accepts; the
// admin handlers bound a request body by it.
func (m *Manager) MaxBinaryBytes() int64 { return m.maxB }

// ConformanceMode is enforce | warn | off.
func (m *Manager) ConformanceMode() string { return m.conf }

// CapabilitiesFor returns what the plugin behind a driver name declared, and
// whether that driver is currently provided at all. The second return is the
// honest answer to "why can I not save a storage on this": a plugin that is
// stopped or refused provides nothing.
func (m *Manager) CapabilitiesFor(driver string) (Capabilities, bool) {
	m.mu.Lock()
	id, ok := m.drivers[driver]
	e := m.entries[id]
	m.mu.Unlock()
	if !ok || e == nil {
		return Capabilities{}, false
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.desc == nil || e.state != StateRunning {
		return Capabilities{}, false
	}
	return e.desc.Capabilities, true
}

// Load starts every enabled plugin from the database. Start-up is
// asynchronous; WaitReady bounds how long the caller waits for the first
// describes so pre-warmed storages find their drivers.
func (m *Manager) Load(ctx context.Context) error {
	if err := os.MkdirAll(m.dir, 0o755); err != nil {
		return fmt.Errorf("plugin: mkdir %s: %w", m.dir, err)
	}
	// Copies left by a previous run that did not get to remove them: every
	// start makes its own.
	_ = os.RemoveAll(filepath.Join(m.dir, verifiedDir))
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
	e := &entry{m: m, row: row, state: StateDisabled, lim: newLimiter(row.Name, m.maxInFlight),
		logs: pluginlog.New().Mirror(m.log, slog.String("plugin", row.Name))}
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
	row := e.row
	e.mu.Unlock()
	// ⚠ The row is trusted by everything below: its name becomes a directory
	// under the plugins dir and its binary a file inside it. A row that does
	// not pass the same rules an install applies (someone edited the table,
	// or a migration went wrong) is refused here rather than joined to a path.
	if err := rowError(row); err != nil {
		m.setFailed(e, StateRefused, err)
		return
	}
	e.mu.Lock()
	ctx, cancel := context.WithCancel(m.ctx)
	e.stopFn = cancel
	e.state, e.stateErr = StateStarting, ""
	e.mu.Unlock()

	switch row.Kind {
	case model.PluginKindRemote:
		go m.runRemote(ctx, e)
	default:
		m.runBinary(ctx, e)
	}
}

func (m *Manager) stop(e *entry) {
	metrics.PluginUp.WithLabelValues(e.row.Name).Set(0)
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

// rowError applies the install-time rules to a row read back from the
// database. Name and binary are joined into filesystem paths, so a row that
// fails them is not "odd", it is a path that leaves the plugins directory.
func rowError(row *model.Plugin) error {
	if !validName(row.Name) {
		return fmt.Errorf("plugin row %d has an invalid name %q - it will not be started; remove it", row.ID, row.Name)
	}
	if row.Kind != model.PluginKindRemote {
		if row.Binary == "" || row.Binary != filepath.Base(row.Binary) || row.Binary == "." || row.Binary == ".." {
			return fmt.Errorf("plugin %s has an invalid binary name %q - it will not be started; reinstall it", row.Name, row.Binary)
		}
		// ⚠ A short or non-hex sha256 (a hand-edited table) used to panic the
		// start, slicing it for the "changed on disk" message - and with it
		// the server's Load.
		if !validSHA256(row.SHA256) {
			return fmt.Errorf("plugin %s has an invalid sha256 %q - it will not be started; reinstall it", row.Name, short(row.SHA256))
		}
	}
	return nil
}

// validSHA256 is 64 hex digits.
func validSHA256(s string) bool {
	if len(s) != 64 {
		return false
	}
	_, err := hex.DecodeString(s)
	return err == nil
}

// runBinary verifies the file, then hands it to a Process.
func (m *Manager) runBinary(ctx context.Context, e *entry) {
	row := e.row
	bin := filepath.Join(m.dir, row.Name, row.Binary)
	if err := m.checkBinary(bin, row.SHA256); err != nil {
		m.setFailed(e, StateRefused, err)
		return
	}
	if err := m.checkStoredSignature(bin, row.SHA256); err != nil {
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
		// Every start - each restart after a crash included - runs a copy
		// verified at that start (verifiedCopy).
		Prepare: func() (string, func(), error) { return m.verifiedCopy(row) },
	}
	proc.OnUp = func(ctx context.Context, c *Client) error {
		return m.adopt(ctx, e, c)
	}
	proc.OnDown = func(err error) {
		metrics.PluginUp.WithLabelValues(row.Name).Set(0)
		if err != nil && !errors.Is(err, ErrStopped) {
			metrics.PluginRestarts.WithLabelValues(row.Name).Inc()
		}
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
	// Re-checked at every start, not only at registration: a name that was
	// private when the row was written may resolve elsewhere now, and a row
	// registered before this rule existed still has to meet it.
	if err := checkRemoteAddress(ctx, row.Address); err != nil {
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

// isNetErr says whether an error came from the NETWORK — the plugin could not
// be reached — as opposed to the plugin answering something unacceptable.
// The first is retried; the second is refused, because retrying a validation
// failure only churns.
//
// Typed, not searched: an error whose message happens to contain "timeout"
// (a plugin's own message, say) is not a connectivity problem, and a DNS
// failure spelled differently on another platform still is one. An io.EOF
// counts only when the transport reported it (inside a *url.Error): an EOF
// from decoding an empty describe body is the plugin's fault, not the wire's.
func isNetErr(err error) bool {
	if err == nil {
		return false
	}
	var ue *url.Error
	fromTransport := errors.As(err, &ue)
	if fromTransport {
		err = ue.Err
	}
	var ne net.Error
	if errors.As(err, &ne) {
		return true
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, syscall.ECONNREFUSED) || errors.Is(err, syscall.ECONNRESET) {
		return true
	}
	if fromTransport && (errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF)) {
		return true
	}
	return false
}

// adopt describes a freshly-up plugin and, if acceptable, registers its
// driver. Called from the supervisor (binary) or the checker (remote).
func (m *Manager) adopt(ctx context.Context, e *entry, c *Client) error {
	desc, err := c.Describe(ctx)
	if err != nil {
		return err
	}
	driver := DriverPrefix + desc.Name

	// ⚠⚠ The driver name is the plugin's own claim, and every storage on
	// plugin:<name> hands its configuration - its credentials - to whichever
	// plugin provides that name. So the name is the ROW's from the first time
	// it was accepted: a plugin may not trade it for another one later (an
	// upgrade, a remote that changed), and a plugin may not take a name
	// another installed plugin holds, running or not. Before this, a second
	// plugin claiming the name while the first was stopped was handed every
	// storage built on the first.
	e.mu.Lock()
	pinned, rowID, rowName := e.row.Driver, e.row.ID, e.row.Name
	e.mu.Unlock()
	if pinned != "" && pinned != desc.Name {
		return fmt.Errorf("plugin %q was accepted as driver %q and now describes itself as %q; a plugin keeps the driver name it was first accepted with (remove and reinstall it to change the name)", rowName, pinned, desc.Name)
	}
	if owner, err := m.driverHolder(ctx, desc.Name, rowID); err != nil {
		return err
	} else if owner != "" {
		return fmt.Errorf("driver %q is already provided by plugin %q (remove that plugin to give the name to this one)", driver, owner)
	}
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

	// ⚠⚠ PROBE BEFORE REGISTERING. Once the driver is in the registry the
	// UI offers every operation the plugin claimed, and a claim that does not
	// hold up is read by the user as filex being broken — so the claims are
	// tested first, against a throwaway instance the plugin opens for exactly
	// this (POST /v1/selftest). A plugin that has no selftest endpoint is
	// registered but marked UNVERIFIED, and the same probes then run when a
	// storage is saved on it.
	report, confErr := m.verify(ctx, c, desc, e)
	if confErr != nil {
		m.mu.Lock()
		delete(m.drivers, driver)
		m.mu.Unlock()
		e.mu.Lock()
		e.report = report
		e.mu.Unlock()
		return confErr
	}

	e.mu.Lock()
	e.desc = desc
	e.client = c
	e.report = report
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
	// driver and version in the list. Written under e.mu — statusOf copies
	// the row under the same lock — and persisted from a snapshot.
	e.mu.Lock()
	changed := row.Driver != desc.Name || row.Version != desc.Version || row.LastError != ""
	if changed {
		row.Driver, row.Version, row.LastError = desc.Name, desc.Version, ""
	}
	snapshot := *row
	e.mu.Unlock()
	if changed {
		if err := m.store.UpdatePlugin(context.Background(), &snapshot); err != nil {
			m.log.Warn("plugin: persist describe", slog.String("plugin", row.Name), slog.Any("err", err))
		}
	}
	metrics.PluginUp.WithLabelValues(row.Name).Set(1)
	m.log.Info("plugin up", slog.String("plugin", row.Name), slog.String("driver", driver), slog.String("version", desc.Version))
	e.logs.Add("info", "plugin up: driver "+driver+", version "+desc.Version)
	return nil
}

// driverHolder is the name of ANOTHER installed plugin whose row holds the
// driver name, "" when none does.
func (m *Manager) driverHolder(ctx context.Context, name string, self int64) (string, error) {
	rows, err := m.store.ListPlugins(ctx)
	if err != nil {
		return "", fmt.Errorf("checking who holds driver %q: %w", name, err)
	}
	for _, r := range rows {
		if r.ID != self && r.Driver == name {
			return r.Name, nil
		}
	}
	return "", nil
}

// verify runs the conformance probes against a throwaway instance the plugin
// opens for POST /v1/selftest.
//
// Returns (report, error). A non-nil error means the plugin must not be
// registered; the report is kept either way so the admin page can show which
// probe failed rather than a sentence.
func (m *Manager) verify(ctx context.Context, c *Client, desc *DescribeResponse, e *entry) (*Report, error) {
	if m.conf == ConformanceOff {
		return nil, nil
	}
	inst, err := c.SelfTest(ctx)
	if err != nil {
		// No selftest endpoint is not a failure: an older plugin, or one
		// whose backend has no throwaway space. It stays UNVERIFIED until a
		// storage is saved on it, and the admin list says so.
		m.log.Info("plugin has no selftest endpoint; conformance will run when a storage is saved",
			slog.String("plugin", e.row.Name), slog.Any("err", err))
		return nil, nil
	}
	defer func() {
		delCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		_ = c.DeleteInstance(delCtx, inst)
	}()

	// A driver bound to the scratch instance, without going through the
	// registry: nothing may be registered until the probes pass.
	drv := newBoundDriver(fixedHandle{c: c, name: desc.Name}, desc.Capabilities, c, inst)
	rep := RunConformance(ctx, drv, desc.Capabilities, "", "selftest")
	if rep.Verified {
		m.log.Info("plugin conformance", slog.String("plugin", e.row.Name), slog.String("result", rep.Summary()))
		return rep, nil
	}
	if m.conf == ConformanceWarn {
		m.log.Warn("plugin fails its own claims but FILEX_PLUGIN_CONFORMANCE=warn - registering anyway",
			slog.String("plugin", e.row.Name), slog.String("result", rep.Summary()))
		return rep, nil
	}
	return rep, rep.FailureError()
}

// fixedHandle hands the conformance run one specific client, with no
// re-instantiation: the scratch instance must not be silently recreated
// underneath a probe.
type fixedHandle struct {
	c    *Client
	name string
}

func (h fixedHandle) Client() (*Client, error) { return h.c, nil }
func (h fixedHandle) DriverName() string       { return DriverPrefix + h.name }

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
	e.logs.Add("error", state+": "+err.Error())
	m.persistError(e, err)
}

// Logs returns plugin id's log lines with a cursor above after, and the
// cursor to ask with next.
func (m *Manager) Logs(ctx context.Context, id, after int64) ([]pluginlog.Line, int64, error) {
	e, err := m.entryFor(ctx, id)
	if err != nil {
		return nil, 0, err
	}
	lines, next := e.logs.After(after)
	return lines, next, nil
}

// LogFor returns the log of the plugin that serves driver ("plugin:<name>"),
// or nil when no plugin does. A nil log takes lines and drops them.
func (m *Manager) LogFor(driver string) *pluginlog.Log {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	id, ok := m.drivers[driver]
	if !ok {
		return nil
	}
	if e, ok := m.entries[id]; ok {
		return e.logs
	}
	return nil
}

func (m *Manager) persistError(e *entry, err error) {
	msg := err.Error()
	if len(msg) > 1000 {
		msg = msg[:1000]
	}
	e.mu.Lock()
	row := e.row
	if row.LastError == msg {
		e.mu.Unlock()
		return
	}
	row.LastError = msg
	snapshot := *row
	e.mu.Unlock()
	if uerr := m.store.UpdatePlugin(context.Background(), &snapshot); uerr != nil {
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
		return changedOnDisk(got, want)
	}
	return nil
}

func changedOnDisk(got, want string) error {
	return fmt.Errorf("binary changed on disk since it was installed (sha256 %s, expected %s) - reinstall it", short(got), short(want))
}

// verifiedDir holds the copies a launched plugin actually runs from, one
// directory per start (<plugins dir>/.verified/<name>-<random>/<binary>).
// A plugin name cannot begin with a dot, so it never collides with one.
const verifiedDir = ".verified"

// verifiedCopy is what a start runs: the installed binary checked against its
// row - the stored signature when keys are trusted, the sha256 always - and
// copied, while it is hashed, to a file of its own. The copy IS the bytes
// that were hashed, so what runs is what was checked whatever happens to the
// installed file afterwards; checking the file and then executing it by name
// left the time between the two to whoever could write there. done removes
// the copy once the process is gone.
func (m *Manager) verifiedCopy(row *model.Plugin) (string, func(), error) {
	bin := filepath.Join(m.dir, row.Name, row.Binary)
	if err := m.checkStoredSignature(bin, row.SHA256); err != nil {
		return "", nil, err
	}
	src, err := os.Open(bin)
	if err != nil {
		return "", nil, fmt.Errorf("binary missing: %w", err)
	}
	defer src.Close()
	root := filepath.Join(m.dir, verifiedDir)
	if err := os.MkdirAll(root, 0o700); err != nil {
		return "", nil, err
	}
	sub, err := os.MkdirTemp(root, row.Name+"-")
	if err != nil {
		return "", nil, err
	}
	done := func() { _ = os.RemoveAll(sub) }
	dst := filepath.Join(sub, row.Binary)
	f, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o700)
	if err != nil {
		done()
		return "", nil, err
	}
	h := sha256.New()
	_, err = io.Copy(io.MultiWriter(f, h), src)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		done()
		return "", nil, err
	}
	if got := hex.EncodeToString(h.Sum(nil)); !strings.EqualFold(got, row.SHA256) {
		done()
		return "", nil, changedOnDisk(got, row.SHA256)
	}
	return dst, done, nil
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
func (m *Manager) InstallBinary(ctx context.Context, name, filename string, r io.Reader, signature string) (*Status, error) {
	return m.install(ctx, name, filename, r, signature, "")
}

// install is the one path every binary install takes, in this order and no
// other: the bytes land in the plugin's directory, the sha256 is compared
// with what the operator expected (when they said), the signature is
// verified, the signature is kept beside the file, the row is written, and
// ONLY THEN is the binary started.
//
// ⚠⚠ The order is the point. InstallFromURL used to hand the download to
// InstallBinary — which started it — and compare the sha256 afterwards: a
// mismatched download was already running as filex's user by the time it was
// found out and removed. Nothing is executed here until every check that can
// refuse it has passed.
func (m *Manager) install(ctx context.Context, name, filename string, r io.Reader, signature, wantSHA string) (*Status, error) {
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
	// ⚠ Staged private and non-executable (0600, writeBinary): the binary
	// reaches its final path, executable, only once every check has passed.
	staged := dst + ".staged"
	sum, err := writeBinary(staged, r, m.maxB)
	if err != nil {
		_ = os.RemoveAll(dir)
		return nil, err
	}
	if wantSHA != "" && !strings.EqualFold(sum, wantSHA) {
		// Wrong bytes: say what arrived, and leave nothing behind.
		_ = os.RemoveAll(dir)
		return nil, RejectedError{fmt.Errorf("%w: downloaded %s, expected %s", ErrSHA256Mismatch, sum[:12], wantSHA[:min(12, len(wantSHA))])}
	}
	if err := m.checkSignature(sum, signature); err != nil {
		_ = os.RemoveAll(dir)
		return nil, err
	}
	if err := promoteBinary(staged, dst); err != nil {
		_ = os.RemoveAll(dir)
		return nil, err
	}
	if err := writeSignature(dst, signature); err != nil {
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

// signaturePath is where a binary's detached signature is kept: beside it,
// as <binary>.sig. The row carries the sha256; the signature that vouches for
// that sha256 lives here, so it can be checked again at every start.
func signaturePath(bin string) string { return bin + ".sig" }

// writeSignature stores the signature next to the binary (0600), or removes
// a stale one when the install carried no signature.
func writeSignature(bin, signature string) error {
	signature = strings.TrimSpace(signature)
	if signature == "" {
		if err := os.Remove(signaturePath(bin)); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	return os.WriteFile(signaturePath(bin), []byte(signature+"\n"), 0o600)
}

// checkStoredSignature re-verifies the signature kept beside the binary
// against the trusted keys, at START rather than only at install.
//
// ⚠ Without this, FILEX_PLUGIN_TRUSTED_KEYS was a gate on the install
// endpoint and nothing more: a plugin installed before the keys were set, or
// while they were briefly unset, ran forever with no signature at all, and
// the setting looked enforced. No trusted keys → nothing is required, as at
// install. Trusted keys and no signature file → refused with the way out.
func (m *Manager) checkStoredSignature(bin, sha string) error {
	if len(m.trusted) == 0 {
		return nil
	}
	b, err := os.ReadFile(signaturePath(bin))
	if err != nil {
		if os.IsNotExist(err) {
			return errors.New("signature required (installed before trusted keys were set - reinstall)")
		}
		return fmt.Errorf("signature file: %w", err)
	}
	if err := VerifyDetached(m.trusted, sha, string(b)); err != nil {
		return fmt.Errorf("stored signature does not verify - reinstall the plugin: %w", err)
	}
	return nil
}

// parsePublicKey accepts an ed25519 key as hex or standard base64.
func parsePublicKey(k string) (ed25519.PublicKey, error) {
	k = strings.TrimSpace(k)
	if b, err := hex.DecodeString(k); err == nil && len(b) == ed25519.PublicKeySize {
		return ed25519.PublicKey(b), nil
	}
	if b, err := base64.StdEncoding.DecodeString(k); err == nil && len(b) == ed25519.PublicKeySize {
		return ed25519.PublicKey(b), nil
	}
	return nil, fmt.Errorf("not a %d-byte ed25519 public key in hex or base64", ed25519.PublicKeySize)
}

func short(s string) string {
	if len(s) > 12 {
		return s[:12] + "…"
	}
	return s
}

// RejectedError marks a failure caused by what the CALLER supplied — a
// missing signature, one that does not verify — rather than by filex failing.
//
// ⚠ This exists because the handler used to classify these by SEARCHING THE
// MESSAGE for words like "required" or "sha256". Under that scheme "this
// instance only accepts signed plugins …" happened to contain "sha256" and
// answered 400, while "signature does not verify against any trusted key"
// matched nothing and answered 500 — the same class of mistake, reported as
// two different kinds of event, one of them blamed on the server. A rejected
// upload is not an outage; it should not spend the server's error budget or
// page anybody.
type RejectedError struct{ err error }

func (e RejectedError) Error() string { return e.err.Error() }
func (e RejectedError) Unwrap() error { return e.err }

func reject(format string, a ...any) error { return RejectedError{fmt.Errorf(format, a...)} }

// ErrSHA256Mismatch: the bytes that arrived are not the ones the install or
// upgrade was told to expect. Always inside a RejectedError; errors.Is finds
// it, so a caller that pinned the bytes (internal/pluginreq) can tell "the
// source changed" from any other refusal without reading the message.
var ErrSHA256Mismatch = errors.New("sha256 mismatch")

// HashURL downloads rawURL through the guarded download client — the one an
// install by address uses — and answers the sha256 of what came back and its
// size. The dry run of an install by address: nothing is written, nothing is
// run. Capped at the binary size limit.
func (m *Manager) HashURL(ctx context.Context, rawURL string) (string, int64, error) {
	resp, err := m.download(ctx, strings.TrimSpace(rawURL))
	if err != nil {
		return "", 0, err
	}
	defer resp.Body.Close()
	h := sha256.New()
	n, err := io.Copy(h, io.LimitReader(resp.Body, m.maxB+1))
	if err != nil {
		return "", 0, fmt.Errorf("download: %w", err)
	}
	if n > m.maxB {
		return "", 0, reject("the plugin is larger than %d MiB", m.maxB>>20)
	}
	return hex.EncodeToString(h.Sum(nil)), n, nil
}

// RequiresSignature reports whether this instance will refuse an unsigned
// plugin. Surfaces show it so nobody discovers the rule from a rejection.
func (m *Manager) RequiresSignature() bool { return len(m.trusted) > 0 }

// checkSignature verifies a detached ed25519 signature over the binary's
// sha256, against any configured trusted key.
//
// ⚠ Signing the HASH rather than the file keeps verification cheap and lets
// an operator sign with the same digest they already publish. The hash is
// hex-encoded exactly as it appears in the plugin row, so
//
//	sha256sum myfs | cut -d' ' -f1 | tr -d '\n' | signify-like-tool
//
// and filex agree on what was signed.
func (m *Manager) checkSignature(sha, signature string) error {
	err := VerifyDetached(m.trusted, sha, signature)
	switch {
	case err == nil:
		return nil
	case errors.Is(err, ErrSignatureRequired):
		return reject("this instance only accepts signed plugins (FILEX_PLUGIN_TRUSTED_KEYS is set) - supply the detached signature over the binary's sha256")
	default:
		return reject("signature %s", strings.TrimPrefix(err.Error(), ErrSignatureInvalid.Error()+": "))
	}
}

// Upgrade replaces a binary plugin's file in place, keeping the row, the
// name, the driver and every storage built on it.
//
// # Why this is not remove + install
//
// Removing takes the registration with it, and a storage whose driver has
// gone is a storage that cannot open. An upgrade is the ordinary thing an
// operator does — a new build of the same plugin — and it must not put the
// storages through a window where they are broken, nor make the operator
// re-enter the configuration. So: stop, swap the file, verify it, start. If
// the new binary fails to start or fails conformance, the plugin is refused
// and the old file is restored, because a failed upgrade must not also be a
// lost plugin. filename is accepted for the caller's convenience and not
// used: the file keeps the name it was installed under (see the note inside).
func (m *Manager) Upgrade(ctx context.Context, id int64, filename string, r io.Reader, signature string) (*Status, error) {
	return m.upgrade(ctx, id, r, signature, "")
}

// upgrade is Upgrade with the bytes held to wantSHA when it is set (an
// upgrade from the plugin's source: the feed names the hash, updates.go).
func (m *Manager) upgrade(ctx context.Context, id int64, r io.Reader, signature, wantSHA string) (*Status, error) {
	e, err := m.entryFor(ctx, id)
	if err != nil {
		return nil, err
	}
	e.mu.Lock()
	row := e.row
	e.mu.Unlock()
	if row.Kind != model.PluginKindBinary {
		// Return the status with every failure, not only after a rollback:
		// the page's "running now" line should never be missing because the
		// upgrade was refused early rather than late.
		return m.statusOf(ctx, e), errors.New("only a binary plugin can be upgraded; a remote one is upgraded where it runs")
	}

	if err := rowError(row); err != nil {
		return m.statusOf(ctx, e), err
	}

	// ⚠ The file keeps the name it was INSTALLED under, whatever the upload
	// is called. One name means one target, one backup and one rollback: when
	// the target was derived from the new upload's name, an upgrade under a
	// different name (myfs → myfs-v2) found no "old file" at the new path,
	// took the no-rollback branch, and then reported "the previous one was
	// restored" while the plugin sat refused with the old file orphaned
	// beside it.
	dir := filepath.Join(m.dir, row.Name)
	target := filepath.Join(dir, row.Binary)
	staged := target + ".upgrade"
	backup := target + ".previous"
	sigBackup := signaturePath(target) + ".previous"

	sum, err := writeBinary(staged, r, m.maxB)
	if err != nil {
		return m.statusOf(ctx, e), err
	}
	if wantSHA != "" && !strings.EqualFold(sum, wantSHA) {
		// Wrong bytes: nothing was stopped or swapped yet.
		_ = os.Remove(staged)
		return m.statusOf(ctx, e), RejectedError{fmt.Errorf("%w: downloaded %s, the source names %s", ErrSHA256Mismatch, sum[:12], wantSHA[:min(12, len(wantSHA))])}
	}
	if err := m.checkSignature(sum, signature); err != nil {
		_ = os.Remove(staged)
		return m.statusOf(ctx, e), err
	}

	// Stop first: a running executable cannot be replaced on Linux (ETXTBSY),
	// and a plugin serving requests should not have the ground moved under it.
	wasEnabled := row.Enabled
	m.stop(e)

	_ = os.Remove(backup)
	_ = os.Remove(sigBackup)
	// An old file that is simply gone (an operator cleaned up by hand) is not
	// a reason to refuse the upgrade; there is then nothing to roll back to.
	oldExists := true
	if err := os.Rename(target, backup); err != nil {
		if !os.IsNotExist(err) {
			_ = os.Remove(staged)
			return m.statusOf(ctx, e), fmt.Errorf("could not set the running binary aside: %w", err)
		}
		oldExists = false
	}
	if err := os.Rename(signaturePath(target), sigBackup); err != nil && !os.IsNotExist(err) {
		_ = os.Remove(staged)
		_ = os.Rename(backup, target)
		return m.statusOf(ctx, e), fmt.Errorf("could not set the signature aside: %w", err)
	}
	// rollback puts the previous binary (and its signature) back exactly as
	// they were; the row is only touched by the caller when it changed it.
	rollback := func() {
		_ = os.Remove(target)
		_ = os.Remove(signaturePath(target))
		if oldExists {
			_ = os.Rename(backup, target)
		}
		_ = os.Rename(sigBackup, signaturePath(target))
	}
	if err := promoteBinary(staged, target); err != nil {
		rollback()
		return m.statusOf(ctx, e), fmt.Errorf("could not put the new binary in place: %w", err)
	}
	if err := writeSignature(target, signature); err != nil {
		rollback()
		return m.statusOf(ctx, e), err
	}

	prevSum := row.SHA256
	row.SHA256 = sum
	if err := m.store.UpdatePlugin(ctx, row); err != nil {
		row.SHA256 = prevSum
		rollback()
		return m.statusOf(ctx, e), err
	}

	if !wasEnabled {
		_ = os.Remove(backup)
		_ = os.Remove(sigBackup)
		return m.statusOf(ctx, e), nil
	}

	m.start(e)
	st := m.waitOutOfStarting(ctx, e, 45*time.Second)
	if st.State == StateRunning {
		_ = os.Remove(backup)
		_ = os.Remove(sigBackup)
		return st, nil
	}

	// The new binary does not work. Put the old one back and start it, so an
	// upgrade that fails costs an error message rather than a storage.
	m.log.Warn("plugin upgrade failed; rolling back",
		slog.String("plugin", row.Name), slog.String("state", st.State), slog.String("err", st.StateError))
	m.stop(e)
	rollback()
	if !oldExists {
		return m.statusOf(ctx, e), fmt.Errorf("the new binary did not come up (%s: %s) - there was no previous binary to restore", st.State, st.StateError)
	}
	row.SHA256 = prevSum
	_ = m.store.UpdatePlugin(ctx, row)
	m.start(e)
	m.waitOutOfStarting(ctx, e, 45*time.Second)
	return m.statusOf(ctx, e), fmt.Errorf("the new binary did not come up (%s: %s) - the previous one was restored", st.State, st.StateError)
}

// waitOutOfStarting blocks until the plugin settles or d elapses.
func (m *Manager) waitOutOfStarting(ctx context.Context, e *entry, d time.Duration) *Status {
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		st := m.statusOf(ctx, e)
		if st.State != StateStarting {
			return st
		}
		time.Sleep(100 * time.Millisecond)
	}
	return m.statusOf(ctx, e)
}

// InstallFromURL downloads a plugin binary. sha256 is REQUIRED: a URL is
// not a promise about bytes, and the operator has to say what they expect
// to receive — the same rule the self-update follows.
func (m *Manager) InstallFromURL(ctx context.Context, name, rawURL, sha, signature string) (*Status, error) {
	if !validName(name) {
		return nil, ErrBadName
	}
	sha = strings.ToLower(strings.TrimSpace(sha))
	if len(sha) != 64 {
		return nil, errors.New("sha256 is required (64 hex characters) when installing from a URL")
	}
	if _, err := hex.DecodeString(sha); err != nil {
		return nil, errors.New("sha256 is required (64 hex characters) when installing from a URL")
	}
	resp, err := m.download(ctx, strings.TrimSpace(rawURL))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	filename := filepath.Base(resp.Request.URL.Path)
	// The sha256 is compared BEFORE anything is started — see install.
	return m.install(ctx, name, filename, resp.Body, signature, sha)
}

// download GETs rawURL through the guarded download client — the one
// address check and the one client every plugin download uses (an install
// by address, a source's feed, a source's build). The caller closes the
// body of a 200; anything else is an error.
func (m *Manager) download(ctx context.Context, rawURL string) (*http.Response, error) {
	u, err := checkDownloadURL(rawURL, m.guardDownloads)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	resp, err := m.http.Do(req)
	if err != nil {
		// netguard.ErrPrivateTarget, not the wrapped errPrivateTarget: the
		// dialer returns the shared sentinel, and errors.Is walks from the
		// error we HAVE towards its causes, never sideways to another
		// wrapper of the same cause.
		if errors.Is(err, netguard.ErrPrivateTarget) || errors.Is(err, netguard.ErrDowngrade) {
			return nil, reject("download: %v", err)
		}
		return nil, fmt.Errorf("download: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf("download: http %d", resp.StatusCode)
	}
	return resp, nil
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
	// The token and every storage credential go to this address for as long
	// as the row exists: TLS, unless it never leaves the private network.
	if err := checkRemoteAddress(ctx, address); err != nil {
		return nil, err
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
	// ⚠ Only a name that passes the install rule is joined to the plugins
	// directory: a row whose name is ".." would otherwise remove the parent.
	if e.row.Kind == model.PluginKindBinary && validName(e.row.Name) {
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
	st.Conformance = e.report
	st.Load = e.lim.stats()
	st.Update = updateInfoOf(&rowCopy)
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

// promoteBinary makes a verified staged binary executable and puts it at its
// final path - the only way a binary gets there.
func promoteBinary(staged, final string) error {
	if err := os.Chmod(staged, 0o755); err != nil {
		return err
	}
	return os.Rename(staged, final)
}

// writeBinary streams r to path, capped, returning its sha256. The file is
// private and NOT executable (0600): it has not been verified yet, and
// promoteBinary is what makes it a program.
func writeBinary(path string, r io.Reader, maxBytes int64) (string, error) {
	tmp := path + ".partial"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
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
