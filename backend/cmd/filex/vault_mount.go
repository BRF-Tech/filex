package main

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"mime"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/net/webdav"

	"github.com/brf-tech/filex/backend/internal/cliclient"
	"github.com/brf-tech/filex/backend/internal/e2e"
	"github.com/brf-tech/filex/backend/internal/e2edecrypt"
)

// ─────────────────── vault mount ───────────────────
//
// `filex vault mount docs://Kasa` serves an unlocked vault from a WebDAV
// server on 127.0.0.1 - a random port, and a random 128-bit path prefix as its
// only credential - and asks the operating system to mount it (net use,
// mount_webdav, gio/davfs2: vault_mount_<os>.go). No cgo, no FUSE.
// docs/E2E-VAULT-FORMAT.md → "filex vault mount" is the contract:
//
//   - reading needs no lock: byte ranges through a cache of decrypted chunks;
//   - the write lock is taken at the FIRST write, never at mount time, so a
//     mount that only looks never stops anybody else writing; a lock someone
//     else holds makes the write fail ("access denied") and the mount says who;
//   - a write is answered once its bytes are in the spool and committed
//     within 5 seconds of the last write - the one exception to "saved means
//     committed", said in the help;
//   - the heartbeat, the person's idle time, a collection after each commit;
//   - 15 minutes without any operation through the mount: it commits,
//     releases the lock, unmounts, drops the keys and exits;
//   - .DS_Store, ._*, Thumbs.db and desktop.ini stay in memory, never in
//     the vault;
//   - without the lock it follows new generations (`state` every 30 s).
//
// The spool holds written files until they are committed. It is never
// plaintext on the disk: every spool file is AES-CTR under a key that lives
// only in this process, so a crash leaves bytes nobody can read.

const (
	vaultMountIdleExit = 15 * time.Minute
	vaultCommitQuiet   = 2 * time.Second
	vaultCommitWithin  = 5 * time.Second
	vaultFollowEvery   = 30 * time.Second
	vaultLitterMax     = 16 << 20
)

var (
	errVaultMountIdle = errors.New("nothing happened in the vault for 15 minutes")
	errSpoolGone      = errors.New("the spool file was released")
)

type vaultMountFlags struct {
	vaultFlags
	readOnly    bool
	cacheChunks int
	spoolDir    string
	noOSMount   bool
	debug       bool
}

func vaultMountCmd() *cobra.Command {
	f := &vaultMountFlags{}
	c := &cobra.Command{
		Use:   "mount <adapter://vault> [<mountpoint>]",
		Short: "Open a vault as a drive on this machine",
		Long: `Unlocks a vault and serves it from a WebDAV server on this machine
(127.0.0.1, a random port, and a random address that is its only key), then asks
the system to mount it: a drive letter on Windows (net use), a folder on macOS
(mount_webdav), GNOME's gio - or davfs2 at the folder you name - on Linux. When
the system cannot, it prints the address for a WebDAV client.

Reading takes no lock: anybody else may write meanwhile, and the mount follows
the new state. The FIRST change takes the vault's write lock; while someone else
holds it a change fails with "access denied" and the mount says who. The lock
goes back after your idle time (the setting in the web app, 3 minutes unless you
changed it); the next change takes it again if it is free.

⚠ Saved is not committed here. The system writes file by file and waits for
each, so the mount answers once a file's bytes are in its spool (encrypted, in
a temporary folder) and commits them to the vault within 5 seconds of the last
write. A crash in that window loses those writes, as a disk's write cache would.

After 15 minutes with nothing done through the mount, it saves what is pending,
gives the lock back, unmounts, forgets the keys and exits. .DS_Store, ._*,
Thumbs.db and desktop.ini are kept in memory and never written to the vault.

Windows: the WebDAV client (the WebClient service) must be allowed to run, and
by default it refuses files over 50 MB; the command checks both and prints the
fix. Files over 4 GB cannot be opened through a Windows WebDAV drive at all.`,
		Example: "  filex vault mount docs://Kasa                 # Windows: the first free drive letter\n" +
			"  filex vault mount docs://Kasa Z:\n" +
			"  filex vault mount docs://Kasa ~/Kasa          # macOS; Linux with davfs2\n" +
			"  filex vault mount docs://Kasa --no-os-mount   # just print the address\n" +
			"  pass show kasa | filex vault mount docs://Kasa --password-stdin",
		Args: cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			mountpoint := ""
			if len(args) == 2 {
				mountpoint = args[1]
			}
			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			s, err := openVaultSession(ctx, cmd, &f.vaultFlags, args[0], cliclient.VaultClientMount)
			if err != nil {
				return vaultExit(err)
			}
			defer s.close()
			return vaultExit(runVaultMount(ctx, cmd.ErrOrStderr(), s, f, mountpoint))
		},
	}
	f.register(c)
	fl := c.Flags()
	fl.BoolVar(&f.readOnly, "read-only", false, "never write: the vault's write lock is never taken")
	fl.IntVar(&f.cacheChunks, "cache-chunks", 32, "how many decrypted 1 MiB chunks to keep in memory")
	fl.StringVar(&f.spoolDir, "spool-dir", "", "where written files wait, encrypted with a key only this process holds, until they are committed (default: the system temp folder)")
	fl.BoolVar(&f.noOSMount, "no-os-mount", false, "do not ask the system to mount it: print the address for a WebDAV client")
	fl.BoolVar(&f.debug, "debug", false, "log every WebDAV request")
	return quiet(c)
}

// osMount is the system's mount of the local WebDAV server.
type osMount struct {
	where   string // what to tell the person: a drive letter, a folder
	unmount func() error
}

func runVaultMount(ctx context.Context, w io.Writer, s *vaultSession, f *vaultMountFlags, mountpoint string) error {
	st, err := e2edecrypt.LoadVault(ctx, s.src, s.keys, s.info.PackLog2, e2edecrypt.VaultLoadOptions{})
	if err != nil {
		return err
	}
	m, err := newVaultMount(s, st, vaultMountConfig{ReadOnly: f.readOnly, CacheChunks: f.cacheChunks, SpoolDir: f.spoolDir, Log: w, Debug: f.debug})
	if err != nil {
		return err
	}
	defer m.cleanup()
	if err := m.listen(); err != nil {
		return err
	}
	defer m.stopServer()

	addr := m.url()
	fmt.Fprintf(w, "Vault %s (generation %d) is served on this machine at\n  %s\n", s.remote, st.Index.Generation, addr)
	if r := m.readOnlyReason(); r != "" {
		fmt.Fprintf(w, "Read-only: %s.\n", r)
	}
	var osm *osMount
	if !f.noOSMount {
		osm, err = mountVaultOS(ctx, addr, mountpoint, s.name, w)
		if err != nil {
			fmt.Fprintf(w, "The system did not mount it: %v\nOpen the address above in a WebDAV client instead.\n", err)
			osm = nil
		} else {
			fmt.Fprintf(w, "Mounted at %s.\n", osm.where)
		}
	}
	fmt.Fprintln(w, "The first change takes the vault's write lock. Changes are committed within 5 seconds of the last write.")
	fmt.Fprintln(w, "Stop with Ctrl-C; after 15 minutes with nothing done it saves, locks the vault and unmounts by itself.")

	runErr := m.run(ctx)
	if osm != nil {
		if err := osm.unmount(); err != nil {
			fmt.Fprintf(w, "warning: unmounting %s: %v\n", osm.where, err)
		}
	}
	if err := m.shutdown(context.WithoutCancel(ctx)); err != nil {
		fmt.Fprintf(w, "warning: %v\n", err)
	}
	if errors.Is(runErr, errVaultMountIdle) {
		fmt.Fprintln(w, "Nothing happened in the vault for 15 minutes: saved, locked and unmounted.")
	} else {
		fmt.Fprintln(w, "Saved, locked and unmounted.")
	}
	return nil
}

// ─────────────────────────── the mount ────────────────────────────────────

type vaultMountConfig struct {
	ReadOnly    bool
	CacheChunks int
	SpoolDir    string
	Log         io.Writer
	Debug       bool
	// Now is the clock (tests move it).
	Now func() time.Time
}

type vaultOpKind int

const (
	vaultOpMkdir vaultOpKind = iota + 1
	vaultOpWrite
	vaultOpDelete
	vaultOpMove
)

// vaultOp is one change waiting for its commit, in the order it was made.
type vaultOp struct {
	kind  vaultOpKind
	path  string
	to    string // a move's destination
	mtime int64
	spool *vaultSpool // a write's bytes
}

func (op *vaultOp) String() string {
	switch op.kind {
	case vaultOpMkdir:
		return "new folder " + op.path
	case vaultOpWrite:
		return op.path
	case vaultOpDelete:
		return "delete " + op.path
	}
	return op.path + " → " + op.to
}

// vaultLitter is an operating system's own file, kept in memory.
type vaultLitter struct {
	data  []byte
	mtime time.Time
}

type vaultMount struct {
	s       *vaultSession
	cfg     vaultMountConfig
	reader  *e2edecrypt.VaultReader
	cache   *vaultChunkCache
	secret  string
	dav     *webdav.Handler
	spool   string
	ctx     context.Context
	cancel  context.CancelFunc
	srv     *http.Server
	port    int
	logMu   sync.Mutex
	lockMu  sync.Mutex // one attempt at the lock at a time
	commitM sync.Mutex // one commit at a time

	mu           sync.Mutex
	base         *e2edecrypt.VaultIndex
	view         *e2edecrypt.VaultTree
	ops          []*vaultOp
	forget       [][16]byte
	held         bool
	idleSeconds  int
	stateRO      string // why this state must not be written (damaged, rollback, newer filex)
	seen         uint64
	knownLatest  uint64 // the latest generation the server last named
	gcDue        bool
	lastActivity time.Time
	lastWrite    time.Time
	firstPending time.Time
	lastRenewTry time.Time
	lastRenewOK  time.Time
	lastFollow   time.Time
	lastDenied   time.Time
	litter       map[string]*vaultLitter
	warned       bool
}

func newVaultMount(s *vaultSession, st *e2edecrypt.VaultState, cfg vaultMountConfig) (*vaultMount, error) {
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.Log == nil {
		cfg.Log = io.Discard
	}
	cfg.CacheChunks = max(cfg.CacheChunks, 4)
	secret := make([]byte, 16)
	if _, err := rand.Read(secret); err != nil {
		return nil, err
	}
	spool, err := os.MkdirTemp(cfg.SpoolDir, "filex-vault-spool-")
	if err != nil {
		return nil, fmt.Errorf("the spool folder: %w", err)
	}
	m := &vaultMount{
		s:       s,
		cfg:     cfg,
		reader:  e2edecrypt.NewVaultReader(s.src, s.keys),
		cache:   newVaultChunkCache(cfg.CacheChunks),
		secret:  hex.EncodeToString(secret),
		spool:   spool,
		litter:  map[string]*vaultLitter{},
		stateRO: st.ReadOnly(),
		seen:    st.Latest,

		knownLatest: st.Latest,
	}
	m.ctx, m.cancel = context.WithCancel(context.Background())
	m.adoptLocked(st.Index)
	now := cfg.Now()
	m.lastActivity, m.lastFollow = now, now
	m.dav = &webdav.Handler{
		Prefix:     "/" + m.secret,
		FileSystem: &vaultFS{m: m},
		LockSystem: webdav.NewMemLS(),
		Logger: func(r *http.Request, err error) {
			if cfg.Debug {
				m.logf("%s %s: %v", r.Method, strings.TrimPrefix(r.URL.Path, "/"+m.secret), err)
			}
		},
	}
	return m, nil
}

func (m *vaultMount) logf(format string, a ...any) {
	m.logMu.Lock()
	defer m.logMu.Unlock()
	fmt.Fprintf(m.cfg.Log, format+"\n", a...)
}

// adoptLocked shows a new generation (no change is pending).
func (m *vaultMount) adoptLocked(idx *e2edecrypt.VaultIndex) {
	m.base = idx
	m.view = idx.Tree.Clone()
	m.seen = max(m.seen, idx.Generation)
}

func (m *vaultMount) readOnlyReason() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.cfg.ReadOnly {
		return "--read-only"
	}
	return m.stateRO
}

func (m *vaultMount) nowMS() int64 { return m.cfg.Now().UnixMilli() }

func (m *vaultMount) touch() {
	m.mu.Lock()
	m.lastActivity = m.cfg.Now()
	m.mu.Unlock()
}

func (m *vaultMount) isHeld() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.held
}

// ─────────────────────────── serving ──────────────────────────────────────

func (m *vaultMount) listen() error {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	m.port = ln.Addr().(*net.TCPAddr).Port
	m.srv = &http.Server{Handler: m, ReadHeaderTimeout: 30 * time.Second}
	go func() { _ = m.srv.Serve(ln) }()
	return nil
}

func (m *vaultMount) url() string {
	return fmt.Sprintf("http://127.0.0.1:%d/%s/", m.port, m.secret)
}

func (m *vaultMount) stopServer() {
	if m.srv == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = m.srv.Shutdown(ctx)
}

// hostAllowed refuses a request whose Host is not this server's own
// address: a web page that rebinds a name to 127.0.0.1 still sends its name.
func (m *vaultMount) hostAllowed(host string) bool {
	h, p, err := net.SplitHostPort(host)
	if err != nil {
		return false
	}
	return (h == "127.0.0.1" || strings.EqualFold(h, "localhost")) && p == strconv.Itoa(m.port)
}

// secretOf is the first segment of a request path.
func secretOf(p string) string {
	p = strings.TrimPrefix(p, "/")
	if i := strings.IndexByte(p, '/'); i >= 0 {
		return p[:i]
	}
	return p
}

var davWriteMethods = map[string]bool{
	http.MethodPut: true, "MKCOL": true, http.MethodDelete: true, "MOVE": true, "COPY": true,
}

func (m *vaultMount) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if m.port != 0 && !m.hostAllowed(r.Host) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	seg := secretOf(r.URL.Path)
	if subtle.ConstantTimeCompare([]byte(seg), []byte(m.secret)) != 1 {
		if r.Method == http.MethodOptions {
			// Windows asks the server root before it maps a folder below it.
			w.Header().Set("DAV", "1, 2")
			w.Header().Set("MS-Author-Via", "DAV")
			w.Header().Set("Allow", "OPTIONS")
			w.WriteHeader(http.StatusOK)
			return
		}
		http.NotFound(w, r)
		return
	}
	m.touch()
	if davWriteMethods[r.Method] {
		rel := strings.TrimPrefix(r.URL.Path, "/"+m.secret)
		if !isOSLitter(path.Base(rel)) {
			if err := m.ensureWritable(r.Context()); err != nil {
				http.Error(w, m.deniedText(err), http.StatusForbidden)
				return
			}
		}
	}
	m.dav.ServeHTTP(w, r)
}

// isOSLitter: files an operating system writes for itself, kept in memory.
func isOSLitter(base string) bool {
	return base == ".DS_Store" || strings.HasPrefix(base, "._") ||
		strings.EqualFold(base, "Thumbs.db") || strings.EqualFold(base, "desktop.ini")
}

// ─────────────────────────── the write lock ───────────────────────────────

// errVaultDenied is a write this mount cannot make now; its text says why.
type errVaultDenied struct{ why string }

func (e *errVaultDenied) Error() string { return e.why }
func (e *errVaultDenied) Unwrap() error { return os.ErrPermission }

func (m *vaultMount) deniedText(err error) string {
	var d *errVaultDenied
	if errors.As(err, &d) {
		return "this vault is read-only now: " + d.why
	}
	return "this vault is read-only now"
}

// ensureWritable takes the write lock if this mount does not hold it: the
// first write of a session, or the first after the lock went back.
func (m *vaultMount) ensureWritable(ctx context.Context) error {
	if r := m.readOnlyReason(); r != "" {
		return &errVaultDenied{why: r}
	}
	if m.isHeld() {
		return nil
	}
	m.lockMu.Lock()
	defer m.lockMu.Unlock()
	if m.isHeld() {
		return nil
	}
	l, err := m.s.lock(ctx)
	if err != nil {
		why := fmt.Sprintf("the vault's write lock could not be taken (%v)", err)
		if who, locked := lockHolder(err); locked {
			why = who + " is writing in this vault"
		}
		m.mu.Lock()
		quiet := m.cfg.Now().Sub(m.lastDenied) < 30*time.Second
		m.lastDenied = m.cfg.Now()
		m.mu.Unlock()
		if !quiet {
			m.logf("cannot write: %s.", why)
		}
		return &errVaultDenied{why: why}
	}
	// Latest first: a generation committed since this mount loaded its own is
	// loaded before anything changes.
	m.mu.Lock()
	baseGen, seen := m.base.Generation, m.seen
	m.mu.Unlock()
	if l.Generation != baseGen {
		st, lerr := e2edecrypt.LoadVault(ctx, m.s.src, m.s.keys, m.s.info.PackLog2, e2edecrypt.VaultLoadOptions{Seen: seen})
		if lerr == nil {
			lerr = st.Writable()
		}
		if lerr != nil {
			m.s.release(context.WithoutCancel(ctx))
			m.mu.Lock()
			m.stateRO = lerr.Error()
			m.mu.Unlock()
			m.logf("cannot write: %v", lerr)
			return &errVaultDenied{why: lerr.Error()}
		}
		m.mu.Lock()
		m.adoptLocked(st.Index)
		m.knownLatest = st.Latest
		m.mu.Unlock()
	}
	now := m.cfg.Now()
	m.mu.Lock()
	m.held = true
	m.idleSeconds = l.IdleSeconds
	m.lastRenewTry, m.lastRenewOK = now, now
	m.gcDue = true
	gen := m.base.Generation
	m.mu.Unlock()
	m.logf("Took the vault's write lock (generation %d).", gen)
	return nil
}

// lost ends this mount's write session: what was not committed is not
// saved, and the mount shows the last committed generation, read-only until
// the next write takes the lock again.
func (m *vaultMount) lost(reason string) {
	m.s.dropLock()
	m.mu.Lock()
	ops := m.ops
	m.ops, m.held, m.firstPending, m.forget = nil, false, time.Time{}, nil
	m.view = m.base.Tree.Clone()
	idle := m.idleSeconds
	var unsaved []string
	for _, op := range ops {
		if op.spool != nil {
			op.spool.lost = true
			if op.spool.closed {
				op.spool.release()
			}
		}
		unsaved = append(unsaved, op.String())
	}
	m.mu.Unlock()
	m.logf("%s", lostMessage(reason, idle))
	if len(unsaved) > 0 {
		m.logf("Not saved: %s", strings.Join(unsaved, "; "))
	}
}

func lostMessage(reason string, idleSeconds int) string {
	switch reason {
	case "idle":
		mins := max(idleSeconds/60, 1)
		return fmt.Sprintf("You did nothing for %d minute(s), so this vault went back to read-only. The next change takes the lock again.", mins)
	case "broken":
		return "Your write lock was broken (by the vault's owner or an administrator); this vault is read-only now."
	case "taken":
		return "Someone else took over writing; this vault is read-only now."
	case "released":
		return "The write lock was released; this vault is read-only now."
	}
	return "The connection was lost, so this vault went back to read-only."
}

func lostReason(err error) (string, bool) {
	var ve *cliclient.VaultError
	if errors.As(err, &ve) && ve.Code == cliclient.VaultCodeLockLost {
		return ve.Reason, true
	}
	if errors.Is(err, errVaultLockGone) {
		return "released", true
	}
	return "", false
}

// ─────────────────────────── the clock ────────────────────────────────────

// run ticks once a second until ctx ends or the mount has been idle for 15
// minutes.
func (m *vaultMount) run(ctx context.Context) error {
	t := time.NewTicker(time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
		}
		if err := m.tick(ctx); err != nil {
			return err
		}
	}
}

// tick does what is due: the heartbeat, a commit, a collection, following a
// newer generation - or reports the 15 idle minutes.
func (m *vaultMount) tick(ctx context.Context) error {
	now := m.cfg.Now()
	m.mu.Lock()
	idle := now.Sub(m.lastActivity) >= vaultMountIdleExit
	renewDue := m.held && now.Sub(m.lastRenewTry) >= vaultHeartbeatEvery
	active := len(m.ops) > 0
	commitDue := m.commitDueLocked(now)
	gcDue := m.held && m.gcDue
	followDue := !m.held && len(m.ops) == 0 && now.Sub(m.lastFollow) >= vaultFollowEvery
	m.mu.Unlock()

	if idle {
		return errVaultMountIdle
	}
	if renewDue {
		m.renew(ctx, active, now)
	}
	if commitDue {
		if err := m.commit(ctx); err != nil && ctx.Err() == nil {
			m.logf("warning: committing: %v (it is tried again)", err)
		}
	}
	if gcDue {
		m.collect(ctx)
	}
	if followDue {
		m.follow(ctx, now)
	}
	return nil
}

func (m *vaultMount) renew(ctx context.Context, active bool, now time.Time) {
	m.mu.Lock()
	m.lastRenewTry = now
	m.mu.Unlock()
	err := m.s.renew(ctx, active)
	if err == nil {
		m.mu.Lock()
		m.lastRenewOK = now
		m.mu.Unlock()
		return
	}
	if ctx.Err() != nil {
		return
	}
	if reason, ok := lostReason(err); ok {
		m.lost(reason)
		return
	}
	m.mu.Lock()
	gone := now.Sub(m.lastRenewOK) >= vaultLockGiveUp
	m.mu.Unlock()
	if gone {
		m.lost("connection")
	}
}

// committableLocked is how many pending changes can be committed: up to the
// first write whose file is still being written.
func (m *vaultMount) committableLocked() int {
	for i, op := range m.ops {
		if op.kind == vaultOpWrite && !op.spool.closed {
			return i
		}
	}
	return len(m.ops)
}

// commitDueLocked: changes that end within 2 seconds of each other share a
// generation, and nothing waits more than 5 seconds.
func (m *vaultMount) commitDueLocked(now time.Time) bool {
	if !m.held || m.committableLocked() == 0 {
		return false
	}
	return now.Sub(m.lastWrite) >= vaultCommitQuiet || now.Sub(m.firstPending) >= vaultCommitWithin
}

func (m *vaultMount) logOpLocked(op *vaultOp) {
	if len(m.ops) == 0 {
		m.firstPending = m.cfg.Now()
	}
	m.ops = append(m.ops, op)
	m.lastWrite = m.cfg.Now()
	if !m.warned {
		if msg := e2edecrypt.VaultWarning(m.view.Len(), m.base.Size); msg != "" {
			m.warned = true
			m.logf("warning: %s", msg)
		}
	}
}

// commit replays the committable changes on the latest generation, stores
// the packs and the index, and points the view's files at what was stored.
func (m *vaultMount) commit(ctx context.Context) error {
	m.commitM.Lock()
	defer m.commitM.Unlock()
	m.mu.Lock()
	k := m.committableLocked()
	if !m.held || k == 0 {
		m.mu.Unlock()
		return nil
	}
	batch := slices.Clone(m.ops[:k])
	base := m.base
	forget := slices.Clone(m.forget)
	m.mu.Unlock()

	w, err := e2edecrypt.NewVaultWriter(e2edecrypt.VaultWriterConfig{Keys: m.s.keys, PackLog2: m.s.info.PackLog2, Base: base, Sink: m.s.sink})
	if err != nil {
		return err
	}
	w.ForgetGrave(forget)
	stored := map[*vaultSpool]*e2edecrypt.VaultContent{}
	for _, op := range batch {
		var err error
		switch op.kind {
		case vaultOpMkdir:
			err = w.Mkdir(op.path, op.mtime)
		case vaultOpWrite:
			size := op.spool.Size()
			var n *e2edecrypt.VaultNode
			if n, err = w.Write(ctx, op.path, op.mtime, io.NewSectionReader(op.spool, 0, size), size); err == nil {
				stored[op.spool] = n.Content
			}
		case vaultOpDelete:
			err = w.Delete(op.path)
		case vaultOpMove:
			err = w.Move(op.path, op.to)
		}
		if err != nil {
			if reason, ok := lostReason(err); ok {
				m.lost(reason)
				return err
			}
			if ctx.Err() != nil || errors.Is(err, errSpoolGone) {
				return err
			}
			var ve *cliclient.VaultError
			if errors.As(err, &ve) || isTransient(err) {
				return err
			}
			// The view took this change and the latest generation does not:
			// nothing pending can be trusted any more.
			m.logf("cannot save %s: %v", op, err)
			m.lost("conflict")
			return err
		}
	}
	c, err := m.s.commit(ctx, w)
	if err != nil {
		if reason, ok := lostReason(err); ok {
			m.lost(reason)
		} else if cliclient.IsVaultCode(err, cliclient.VaultCodeGeneration) {
			m.lost("conflict")
		}
		return err
	}
	m.mu.Lock()
	m.base = c.Index
	m.seen = max(m.seen, c.Index.Generation)
	m.knownLatest = c.Index.Generation
	m.ops = m.ops[k:]
	m.forget = m.forget[min(len(forget), len(m.forget)):]
	for sp, content := range stored {
		if sp.node != nil && sp.node.Local == sp {
			sp.node.Content = content
			sp.node.Local = nil
		}
		sp.release()
	}
	if len(m.ops) > 0 {
		m.firstPending = m.cfg.Now()
	} else {
		m.firstPending = time.Time{}
	}
	m.gcDue = true
	m.mu.Unlock()
	if m.cfg.Debug {
		m.logf("committed generation %d (%d change(s))", c.Index.Generation, len(batch))
	}
	return nil
}

func isTransient(err error) bool {
	var ne net.Error
	var ae *cliclient.APIError
	return errors.As(err, &ne) || (errors.As(err, &ae) && ae.Status >= 500) || errors.Is(err, io.ErrUnexpectedEOF)
}

// collect runs one collection pass after a commit or after taking the lock.
func (m *vaultMount) collect(ctx context.Context) {
	m.mu.Lock()
	base := m.base
	m.gcDue = false
	m.mu.Unlock()
	plan, err := m.s.collect(ctx, base, e2edecrypt.VaultGCPassMax)
	if err != nil {
		if m.cfg.Debug {
			m.logf("collection: %v", err)
		}
		return
	}
	m.mu.Lock()
	m.forget = append(m.forget, plan.Forget...)
	m.mu.Unlock()
}

// follow loads a generation someone else committed (no lock, nothing
// pending).
func (m *vaultMount) follow(ctx context.Context, now time.Time) {
	m.mu.Lock()
	m.lastFollow = now
	known, seen := m.knownLatest, m.seen
	m.mu.Unlock()
	latest, err := m.s.src.LatestGeneration(ctx)
	if err != nil || latest == known {
		return
	}
	st, err := e2edecrypt.LoadVault(ctx, m.s.src, m.s.keys, m.s.info.PackLog2, e2edecrypt.VaultLoadOptions{Seen: seen})
	if err != nil {
		m.logf("warning: loading generation %d: %v", latest, err)
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.held || len(m.ops) > 0 {
		return
	}
	m.knownLatest = st.Latest
	if st.RolledBack {
		m.stateRO = st.ReadOnly()
		m.logf("warning: %s; staying read-only.", m.stateRO)
		return
	}
	m.adoptLocked(st.Index)
	m.stateRO = st.ReadOnly()
}

// shutdown commits what can be committed and gives the lock back.
func (m *vaultMount) shutdown(ctx context.Context) error {
	var err error
	if m.isHeld() {
		err = m.commit(ctx)
		m.mu.Lock()
		var unsaved []string
		for _, op := range m.ops {
			unsaved = append(unsaved, op.String())
		}
		m.mu.Unlock()
		if len(unsaved) > 0 {
			m.logf("Not saved (still being written when it stopped): %s", strings.Join(unsaved, "; "))
		}
		if err == nil {
			m.collect(ctx)
		}
	}
	m.s.release(ctx)
	return err
}

// cleanup removes the spool.
func (m *vaultMount) cleanup() {
	m.cancel()
	m.mu.Lock()
	for _, op := range m.ops {
		if op.spool != nil {
			op.spool.release()
		}
	}
	m.ops = nil
	m.mu.Unlock()
	_ = os.RemoveAll(m.spool)
}

// ─────────────────────────── the spool ────────────────────────────────────

// vaultSpool is one written file waiting for its commit, AES-CTR under a
// key of its own that never leaves this process.
type vaultSpool struct {
	mu    sync.RWMutex
	f     *os.File
	block cipher.Block
	iv    [16]byte
	size  int64
	gone  bool

	// guarded by vaultMount.mu
	closed bool
	lost   bool
	node   *e2edecrypt.VaultNode
}

func (m *vaultMount) newSpool() (*vaultSpool, error) {
	f, err := os.CreateTemp(m.spool, "w-")
	if err != nil {
		return nil, err
	}
	key := make([]byte, 32)
	sp := &vaultSpool{f: f}
	if _, err := rand.Read(key); err != nil {
		f.Close()
		return nil, err
	}
	if _, err := rand.Read(sp.iv[:]); err != nil {
		f.Close()
		return nil, err
	}
	sp.block, err = aes.NewCipher(key)
	clear(key)
	if err != nil {
		f.Close()
		return nil, err
	}
	return sp, nil
}

// addCounter adds n to a 128-bit big-endian counter.
func addCounter(ctr *[16]byte, n uint64) {
	for i := 15; i >= 0 && n > 0; i-- {
		s := uint64(ctr[i]) + (n & 0xff)
		ctr[i] = byte(s)
		n = (n >> 8) + (s >> 8)
	}
}

// xorAt applies the spool's keystream at byte off.
func (sp *vaultSpool) xorAt(dst, src []byte, off int64) {
	ctr := sp.iv
	addCounter(&ctr, uint64(off/aes.BlockSize))
	stream := cipher.NewCTR(sp.block, ctr[:])
	if skip := int(off % aes.BlockSize); skip > 0 {
		var junk [aes.BlockSize]byte
		stream.XORKeyStream(junk[:skip], junk[:skip])
	}
	stream.XORKeyStream(dst, src)
}

// ReadAt reads plaintext (io.ReaderAt, for the writer and for reads).
func (sp *vaultSpool) ReadAt(p []byte, off int64) (int, error) {
	sp.mu.RLock()
	defer sp.mu.RUnlock()
	if sp.gone {
		return 0, errSpoolGone
	}
	if off >= sp.size {
		return 0, io.EOF
	}
	want := min(int64(len(p)), sp.size-off)
	n, err := sp.f.ReadAt(p[:want], off)
	sp.xorAt(p[:n], p[:n], off)
	if err == nil && n < len(p) {
		err = io.EOF
	}
	return n, err
}

// WriteAt writes plaintext; a gap before off is zeros, as in a file.
func (sp *vaultSpool) WriteAt(p []byte, off int64) (int, error) {
	sp.mu.Lock()
	defer sp.mu.Unlock()
	if sp.gone {
		return 0, errSpoolGone
	}
	for sp.size < off {
		z := make([]byte, min(off-sp.size, 1<<20))
		sp.xorAt(z, z, sp.size)
		k, err := sp.f.WriteAt(z, sp.size)
		sp.size += int64(k)
		if err != nil {
			return 0, err
		}
	}
	buf := make([]byte, len(p))
	sp.xorAt(buf, p, off)
	n, err := sp.f.WriteAt(buf, off)
	if end := off + int64(n); end > sp.size {
		sp.size = end
	}
	return n, err
}

// Size is how many bytes were written.
func (sp *vaultSpool) Size() int64 {
	sp.mu.RLock()
	defer sp.mu.RUnlock()
	return sp.size
}

// release deletes the spool file once.
func (sp *vaultSpool) release() {
	sp.mu.Lock()
	defer sp.mu.Unlock()
	if sp.gone {
		return
	}
	sp.gone = true
	name := sp.f.Name()
	_ = sp.f.Close()
	_ = os.Remove(name)
}

// ─────────────────────────── the chunk cache ──────────────────────────────

// vaultChunkCache holds decrypted chunks by (content id, chunk): a content id
// is new for every version of a file, so nothing in it is ever stale.
type vaultChunkCache struct {
	mu     sync.Mutex
	max    int
	chunks map[vaultChunkKey][]byte
	order  []vaultChunkKey
}

type vaultChunkKey struct {
	id [16]byte
	i  int64
}

func newVaultChunkCache(n int) *vaultChunkCache {
	return &vaultChunkCache{max: n, chunks: map[vaultChunkKey][]byte{}}
}

func (c *vaultChunkCache) get(k vaultChunkKey) ([]byte, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	b, ok := c.chunks[k]
	return b, ok
}

func (c *vaultChunkCache) put(k vaultChunkKey, b []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.chunks[k]; ok {
		return
	}
	c.chunks[k] = b
	c.order = append(c.order, k)
	for len(c.order) > c.max {
		delete(c.chunks, c.order[0])
		c.order = c.order[1:]
	}
}

// ─────────────────────────── reading ──────────────────────────────────────

// readAt reads a file of the view: its spool while it waits for a commit,
// else its chunks through the cache.
func (m *vaultMount) readAt(n *e2edecrypt.VaultNode, p []byte, off int64) (int, error) {
	for try := 0; ; try++ {
		m.mu.Lock()
		size, content, name := n.Size, n.Content, n.Name
		sp, _ := n.Local.(*vaultSpool)
		m.mu.Unlock()
		if off >= size {
			return 0, io.EOF
		}
		want := min(int64(len(p)), size-off)
		if sp != nil {
			k, err := sp.ReadAt(p[:want], off)
			if errors.Is(err, errSpoolGone) && try == 0 {
				continue // committed meanwhile: read it from the vault
			}
			if err == io.EOF && k > 0 {
				err = nil
			}
			return k, err
		}
		if content == nil {
			return 0, fmt.Errorf("%s: its contents are not stored", name)
		}
		cs := int64(1) << content.ChunkLog2
		i := off / cs
		key := vaultChunkKey{id: content.ID, i: i}
		chunk, ok := m.cache.get(key)
		if !ok {
			snap := &e2edecrypt.VaultNode{Name: name, Size: size, Content: content}
			var err error
			if chunk, err = m.reader.ReadChunk(m.ctx, snap, i); err != nil {
				return 0, err
			}
			m.cache.put(key, chunk)
		}
		k := copy(p[:want], chunk[off-i*cs:])
		return k, nil
	}
}

// ─────────────────────────── the WebDAV file system ───────────────────────

type vaultFS struct{ m *vaultMount }

func vaultRel(name string) string { return strings.Trim(path.Clean("/"+name), "/") }

func davErr(op, name string, err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, e2edecrypt.ErrVaultNotFound):
		return &os.PathError{Op: op, Path: name, Err: os.ErrNotExist}
	case errors.Is(err, e2edecrypt.ErrVaultExists):
		return &os.PathError{Op: op, Path: name, Err: os.ErrExist}
	case errors.Is(err, os.ErrPermission), errors.Is(err, e2edecrypt.ErrVaultFull):
		return &os.PathError{Op: op, Path: name, Err: os.ErrPermission}
	}
	return &os.PathError{Op: op, Path: name, Err: err}
}

func (v *vaultFS) Mkdir(ctx context.Context, name string, _ os.FileMode) error {
	m := v.m
	m.touch()
	p := vaultRel(name)
	if p == "" {
		return davErr("mkdir", name, e2edecrypt.ErrVaultExists)
	}
	m.mu.Lock()
	err := m.view.CheckMkdir(p)
	m.mu.Unlock()
	if err != nil {
		return davErr("mkdir", name, err)
	}
	if err := m.ensureWritable(ctx); err != nil {
		return davErr("mkdir", name, err)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.held {
		return davErr("mkdir", name, os.ErrPermission)
	}
	if m.view.Len() >= e2e.VaultEntriesWriteMax {
		return davErr("mkdir", name, e2edecrypt.ErrVaultFull)
	}
	mtime := m.nowMS()
	if _, err := m.view.Mkdir(p, mtime); err != nil {
		return davErr("mkdir", name, err)
	}
	m.logOpLocked(&vaultOp{kind: vaultOpMkdir, path: p, mtime: mtime})
	return nil
}

func (v *vaultFS) OpenFile(ctx context.Context, name string, flag int, _ os.FileMode) (webdav.File, error) {
	m := v.m
	m.touch()
	p := vaultRel(name)
	writing := flag&(os.O_WRONLY|os.O_RDWR) != 0 && flag&(os.O_CREATE|os.O_TRUNC) != 0
	if p != "" && isOSLitter(path.Base(p)) {
		return m.openLitter(p, name, flag, writing)
	}
	m.mu.Lock()
	n := m.view.Lookup(p)
	m.mu.Unlock()
	if n != nil && (n.Dir || !writing || flag&os.O_TRUNC == 0) {
		if n.Dir && writing {
			return nil, davErr("open", name, e2edecrypt.ErrVaultIsDir)
		}
		if flag&os.O_EXCL != 0 && flag&os.O_CREATE != 0 {
			return nil, davErr("open", name, e2edecrypt.ErrVaultExists)
		}
		return m.openRead(n, p), nil
	}
	if n == nil && !writing {
		return nil, davErr("open", name, e2edecrypt.ErrVaultNotFound)
	}
	if n == nil && flag&os.O_CREATE == 0 {
		return nil, davErr("open", name, e2edecrypt.ErrVaultNotFound)
	}
	if err := m.ensureWritable(ctx); err != nil {
		return nil, davErr("open", name, err)
	}
	return m.openWrite(p, name, flag)
}

func (m *vaultMount) openWrite(p, name string, flag int) (webdav.File, error) {
	sp, err := m.newSpool()
	if err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	fail := func(err error) (webdav.File, error) {
		sp.release()
		return nil, davErr("open", name, err)
	}
	if !m.held {
		return fail(os.ErrPermission)
	}
	adds, err := m.view.CheckPutFile(p)
	if err != nil {
		return fail(err)
	}
	if !adds && flag&os.O_EXCL != 0 {
		return fail(e2edecrypt.ErrVaultExists)
	}
	if adds && m.view.Len() >= e2e.VaultEntriesWriteMax {
		return fail(e2edecrypt.ErrVaultFull)
	}
	mtime := m.nowMS()
	n, err := m.view.PutFile(p, mtime, 0, nil)
	if err != nil {
		return fail(err)
	}
	n.Local, sp.node = sp, n
	m.logOpLocked(&vaultOp{kind: vaultOpWrite, path: p, mtime: mtime, spool: sp})
	return &vaultFile{m: m, node: n, name: n.Name, spool: sp, write: true}, nil
}

func (m *vaultMount) openRead(n *e2edecrypt.VaultNode, p string) webdav.File {
	f := &vaultFile{m: m, node: n, name: n.Name}
	if p == "" {
		f.name = m.s.name
	}
	if n.Dir {
		f.dir = true
		m.mu.Lock()
		for _, c := range n.Children() {
			f.children = append(f.children, m.infoLocked(c, c.Name))
		}
		for k, lit := range m.litter {
			if path.Dir("/"+k) == path.Clean("/"+p) {
				f.children = append(f.children, &vaultInfo{name: path.Base(k), size: int64(len(lit.data)), mtime: lit.mtime})
			}
		}
		m.mu.Unlock()
	}
	return f
}

func (v *vaultFS) RemoveAll(ctx context.Context, name string) error {
	m := v.m
	m.touch()
	p := vaultRel(name)
	if p == "" {
		return davErr("remove", name, os.ErrPermission)
	}
	m.mu.Lock()
	if _, ok := m.litter[p]; ok {
		delete(m.litter, p)
		m.mu.Unlock()
		return nil
	}
	exists := m.view.Lookup(p) != nil
	m.mu.Unlock()
	if !exists {
		return nil
	}
	if err := m.ensureWritable(ctx); err != nil {
		return davErr("remove", name, err)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.held {
		return davErr("remove", name, os.ErrPermission)
	}
	if _, err := m.view.Remove(p); err != nil {
		return davErr("remove", name, err)
	}
	for k := range m.litter {
		if strings.HasPrefix(k, p+"/") {
			delete(m.litter, k)
		}
	}
	m.logOpLocked(&vaultOp{kind: vaultOpDelete, path: p})
	return nil
}

func (v *vaultFS) Rename(ctx context.Context, oldName, newName string) error {
	m := v.m
	m.touch()
	from, to := vaultRel(oldName), vaultRel(newName)
	if from == "" || to == "" {
		return davErr("rename", oldName, os.ErrPermission)
	}
	fromLitter, toLitter := isOSLitter(path.Base(from)), isOSLitter(path.Base(to))
	m.mu.Lock()
	lit := m.litter[from]
	m.mu.Unlock()
	switch {
	case fromLitter && toLitter:
		m.mu.Lock()
		defer m.mu.Unlock()
		if lit == nil {
			return davErr("rename", oldName, e2edecrypt.ErrVaultNotFound)
		}
		delete(m.litter, from)
		m.litter[to] = lit
		return nil
	case fromLitter:
		// Something saved under a system name and renamed into place: now it
		// is the person's file.
		if lit == nil {
			return davErr("rename", oldName, e2edecrypt.ErrVaultNotFound)
		}
		if err := m.ensureWritable(ctx); err != nil {
			return davErr("rename", newName, err)
		}
		f, err := m.openWrite(to, newName, os.O_CREATE|os.O_TRUNC|os.O_WRONLY)
		if err != nil {
			return err
		}
		if _, err := f.Write(lit.data); err != nil {
			_ = f.Close()
			return err
		}
		if err := f.Close(); err != nil {
			return err
		}
		m.mu.Lock()
		delete(m.litter, from)
		m.mu.Unlock()
		return nil
	case toLitter:
		return davErr("rename", newName, os.ErrPermission)
	}
	m.mu.Lock()
	err := m.view.CheckMove(from, to)
	m.mu.Unlock()
	if err != nil {
		return davErr("rename", oldName, err)
	}
	if err := m.ensureWritable(ctx); err != nil {
		return davErr("rename", oldName, err)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.held {
		return davErr("rename", oldName, os.ErrPermission)
	}
	if _, err := m.view.Move(from, to); err != nil {
		return davErr("rename", oldName, err)
	}
	for k, l := range m.litter {
		if strings.HasPrefix(k, from+"/") {
			delete(m.litter, k)
			m.litter[to+strings.TrimPrefix(k, from)] = l
		}
	}
	m.logOpLocked(&vaultOp{kind: vaultOpMove, path: from, to: to})
	return nil
}

func (v *vaultFS) Stat(ctx context.Context, name string) (os.FileInfo, error) {
	m := v.m
	m.touch()
	p := vaultRel(name)
	m.mu.Lock()
	defer m.mu.Unlock()
	if lit, ok := m.litter[p]; ok {
		return &vaultInfo{name: path.Base(p), size: int64(len(lit.data)), mtime: lit.mtime}, nil
	}
	n := m.view.Lookup(p)
	if n == nil {
		return nil, davErr("stat", name, e2edecrypt.ErrVaultNotFound)
	}
	if p == "" {
		return m.infoLocked(n, m.s.name), nil
	}
	return m.infoLocked(n, n.Name), nil
}

// ─────────────────────────── files ────────────────────────────────────────

// vaultInfo is os.FileInfo plus webdav's ContentTyper and ETager, so a
// listing never opens a file to sniff it.
type vaultInfo struct {
	name  string
	size  int64
	mtime time.Time
	dir   bool
	etag  string
}

func (fi *vaultInfo) Name() string { return fi.name }
func (fi *vaultInfo) Size() int64  { return fi.size }
func (fi *vaultInfo) Mode() os.FileMode {
	if fi.dir {
		return os.ModeDir | 0o755
	}
	return 0o644
}
func (fi *vaultInfo) ModTime() time.Time { return fi.mtime }
func (fi *vaultInfo) IsDir() bool        { return fi.dir }
func (fi *vaultInfo) Sys() any           { return nil }

// ContentType implements webdav.ContentTyper.
func (fi *vaultInfo) ContentType(context.Context) (string, error) {
	if fi.dir {
		return "httpd/unix-directory", nil
	}
	if ct := mime.TypeByExtension(path.Ext(fi.name)); ct != "" {
		return ct, nil
	}
	return "application/octet-stream", nil
}

// ETag implements webdav.ETager: a stored version's content id.
func (fi *vaultInfo) ETag(context.Context) (string, error) {
	if fi.etag == "" {
		return "", webdav.ErrNotImplemented
	}
	return fi.etag, nil
}

func (m *vaultMount) infoLocked(n *e2edecrypt.VaultNode, name string) *vaultInfo {
	fi := &vaultInfo{name: name, dir: n.Dir, mtime: time.UnixMilli(n.MTime)}
	if !n.Dir {
		fi.size = n.Size
		if n.Local == nil && n.Content != nil {
			fi.etag = `"` + hex.EncodeToString(n.Content.ID[:]) + `"`
		}
	}
	return fi
}

// vaultFile is a handle on a file or folder of the view.
type vaultFile struct {
	m        *vaultMount
	node     *e2edecrypt.VaultNode
	name     string
	off      int64
	spool    *vaultSpool
	write    bool
	dir      bool
	children []os.FileInfo
	dirOff   int
}

func (f *vaultFile) Close() error {
	if f.write {
		f.m.finishWrite(f.spool)
	}
	return nil
}

func (f *vaultFile) Read(p []byte) (int, error) {
	if f.dir {
		return 0, fs.ErrInvalid
	}
	f.m.touch()
	n, err := f.m.readAt(f.node, p, f.off)
	f.off += int64(n)
	return n, err
}

func (f *vaultFile) Seek(offset int64, whence int) (int64, error) {
	if f.dir {
		return 0, fs.ErrInvalid
	}
	f.m.mu.Lock()
	size := f.node.Size
	f.m.mu.Unlock()
	var abs int64
	switch whence {
	case io.SeekStart:
		abs = offset
	case io.SeekCurrent:
		abs = f.off + offset
	case io.SeekEnd:
		abs = size + offset
	default:
		return 0, fs.ErrInvalid
	}
	if abs < 0 {
		return 0, fs.ErrInvalid
	}
	f.off = abs
	return abs, nil
}

func (f *vaultFile) Write(p []byte) (int, error) {
	if !f.write {
		return 0, os.ErrPermission
	}
	m := f.m
	m.touch()
	m.mu.Lock()
	lost := f.spool.lost
	m.mu.Unlock()
	if lost {
		return 0, &errVaultDenied{why: "the write lock was lost"}
	}
	n, err := f.spool.WriteAt(p, f.off)
	f.off += int64(n)
	size := f.spool.Size()
	m.mu.Lock()
	if f.node.Local == f.spool {
		f.node.Size = size
	}
	m.lastWrite = m.cfg.Now()
	m.mu.Unlock()
	return n, err
}

func (f *vaultFile) Readdir(count int) ([]fs.FileInfo, error) {
	if !f.dir {
		return nil, fs.ErrInvalid
	}
	if count <= 0 {
		out := f.children[f.dirOff:]
		f.dirOff = len(f.children)
		return out, nil
	}
	if f.dirOff >= len(f.children) {
		return nil, io.EOF
	}
	end := min(f.dirOff+count, len(f.children))
	out := f.children[f.dirOff:end]
	f.dirOff = end
	return out, nil
}

func (f *vaultFile) Stat() (fs.FileInfo, error) {
	f.m.mu.Lock()
	defer f.m.mu.Unlock()
	return f.m.infoLocked(f.node, f.name), nil
}

// finishWrite: the system closed a written file; its change may commit.
func (m *vaultMount) finishWrite(sp *vaultSpool) {
	size := sp.Size()
	m.mu.Lock()
	defer m.mu.Unlock()
	if sp.lost {
		sp.release()
		return
	}
	if sp.closed {
		return
	}
	sp.closed = true
	now := m.nowMS()
	for _, op := range m.ops {
		if op.spool == sp {
			op.mtime = now
		}
	}
	if sp.node != nil && sp.node.Local == sp {
		sp.node.MTime, sp.node.Size = now, size
	}
	m.lastWrite = m.cfg.Now()
}

// ─────────────────────────── the system's litter ──────────────────────────

func (m *vaultMount) openLitter(p, name string, flag int, writing bool) (webdav.File, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	lit := m.litter[p]
	if !writing {
		if lit == nil {
			return nil, davErr("open", name, e2edecrypt.ErrVaultNotFound)
		}
		return &litterFile{m: m, lit: lit, name: path.Base(p)}, nil
	}
	if parent := m.view.Lookup(path.Dir("/" + p)); parent == nil || !parent.Dir {
		return nil, davErr("open", name, e2edecrypt.ErrVaultNotFound)
	}
	if lit == nil || flag&os.O_TRUNC != 0 {
		lit = &vaultLitter{mtime: m.cfg.Now()}
		m.litter[p] = lit
	}
	return &litterFile{m: m, lit: lit, name: path.Base(p), write: true}, nil
}

type litterFile struct {
	m     *vaultMount
	lit   *vaultLitter
	name  string
	off   int64
	write bool
}

func (f *litterFile) Close() error { return nil }

func (f *litterFile) Read(p []byte) (int, error) {
	f.m.mu.Lock()
	defer f.m.mu.Unlock()
	if f.off >= int64(len(f.lit.data)) {
		return 0, io.EOF
	}
	n := copy(p, f.lit.data[f.off:])
	f.off += int64(n)
	return n, nil
}

func (f *litterFile) Seek(offset int64, whence int) (int64, error) {
	f.m.mu.Lock()
	size := int64(len(f.lit.data))
	f.m.mu.Unlock()
	switch whence {
	case io.SeekCurrent:
		offset += f.off
	case io.SeekEnd:
		offset += size
	}
	if offset < 0 {
		return 0, fs.ErrInvalid
	}
	f.off = offset
	return offset, nil
}

func (f *litterFile) Write(p []byte) (int, error) {
	if !f.write {
		return 0, os.ErrPermission
	}
	f.m.mu.Lock()
	defer f.m.mu.Unlock()
	end := f.off + int64(len(p))
	if end > vaultLitterMax {
		return 0, os.ErrPermission
	}
	if end > int64(len(f.lit.data)) {
		f.lit.data = append(f.lit.data, make([]byte, end-int64(len(f.lit.data)))...)
	}
	copy(f.lit.data[f.off:], p)
	f.off = end
	f.lit.mtime = f.m.cfg.Now()
	return len(p), nil
}

func (f *litterFile) Readdir(int) ([]fs.FileInfo, error) { return nil, fs.ErrInvalid }

func (f *litterFile) Stat() (fs.FileInfo, error) {
	f.m.mu.Lock()
	defer f.m.mu.Unlock()
	return &vaultInfo{name: f.name, size: int64(len(f.lit.data)), mtime: f.lit.mtime}, nil
}
