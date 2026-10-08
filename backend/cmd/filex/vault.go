package main

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"time"

	"github.com/spf13/cobra"

	"github.com/brf-tech/filex/backend/internal/cliclient"
	"github.com/brf-tech/filex/backend/internal/e2e"
	"github.com/brf-tech/filex/backend/internal/e2edecrypt"
)

// ─────────────────── vault ───────────────────
//
// `filex vault` works with a vault (end-to-end encryption level 3,
// docs/E2E-VAULT-FORMAT.md) on a filex server:
//
//	filex vault mount docs://Kasa [<mountpoint>]   the vault as a drive
//	filex vault prune docs://Kasa                  collect and repack
//
// Everything is decrypted and encrypted on this machine: the server stores
// packs and index files it cannot read, and keeps the write lock. The password
// is never an argument or an environment variable - the terminal without
// echo, or one line on stdin with --password-stdin.

func vaultCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "vault",
		Short: "Work with a vault (end-to-end encryption level 3) on a filex server",
		Long: "A vault is a folder encrypted whole: its files and subfolders live in encrypted\n" +
			"packs and one encrypted index, so the server sees neither names, sizes nor how\n" +
			"many files there are. It is made in the web app (or the desktop app); these\n" +
			"commands open one from the terminal.\n\n" +
			"  filex vault mount docs://Kasa      the vault as a drive on this machine\n" +
			"  filex vault prune docs://Kasa      delete what it no longer needs\n" +
			"  filex decrypt docs://Kasa          a plain copy of the whole vault\n\n" +
			"The connection is the one `filex client` uses: --url/--token, FILEX_URL/\n" +
			"FILEX_TOKEN, or the session `filex client login` saved.",
	}
	c.AddCommand(vaultMountCmd(), vaultPruneCmd())
	return c
}

// vaultFlags are what every vault command takes.
type vaultFlags struct {
	url, token string
	recovery   bool
	fromStdin  bool
}

func (f *vaultFlags) register(c *cobra.Command) {
	fl := c.Flags()
	fl.StringVar(&f.url, "url", "", "filex server URL (default: $FILEX_URL or ~/.filex/cli.yaml)")
	fl.StringVar(&f.token, "token", "", "API or session token (default: $FILEX_TOKEN, else the saved session)")
	fl.BoolVar(&f.recovery, "recovery-key", false, "unlock with the recovery key instead of the password")
	fl.BoolVar(&f.fromStdin, "password-stdin", false, "read the password (or recovery key) as one line from stdin")
}

// errNotVault is a key file that is not a vault's.
func errNotVault(remote string) error {
	return fmt.Errorf("%s is not a vault: an encrypted folder of level 1 or 2 is opened by downloading it (the folder, or a .zip of it) and running `filex decrypt` on the copy", remote)
}

// vaultExit maps what stopped a vault command to the documented statuses.
func vaultExit(err error) error {
	var ue *e2edecrypt.UnsupportedError
	var ce *e2edecrypt.CorruptError
	switch {
	case err == nil:
		return nil
	case errors.Is(err, e2edecrypt.ErrWrongPassword):
		return &exitError{code: exitDecryptWrongSecret, err: errors.New("wrong password")}
	case errors.Is(err, e2edecrypt.ErrWrongRecoveryKey):
		return &exitError{code: exitDecryptWrongSecret, err: errors.New("wrong recovery key (or this vault has none)")}
	case errors.As(err, &ue):
		return &exitError{code: exitDecryptUnsupported, err: err}
	case errors.As(err, &ce), errors.Is(err, e2edecrypt.ErrNotMarker):
		return &exitError{code: exitDecryptCorrupt, err: err}
	}
	return authHint(err)
}

// readRemoteVaultMarker reads and parses the key file of a vault on a server.
func readRemoteVaultMarker(ctx context.Context, api *cliclient.Client, wire string) (*e2edecrypt.Marker, error) {
	rp, err := cliclient.ParseRemotePath(wire)
	if err != nil {
		return nil, err
	}
	data, err := api.ReadSmall(ctx, rp.Join(e2edecrypt.MarkerName).String(), 1<<20)
	if err != nil {
		var ae *cliclient.APIError
		if errors.As(err, &ae) && ae.Status == http.StatusNotFound {
			return nil, fmt.Errorf("%s has no key file (%s): it is not an encrypted folder", wire, e2edecrypt.MarkerName)
		}
		return nil, fmt.Errorf("reading the key file of %s: %w", wire, authHint(err))
	}
	m, err := e2edecrypt.ParseMarker(data)
	if err != nil {
		return nil, err
	}
	if !m.IsVault() {
		return nil, errNotVault(wire)
	}
	return m, nil
}

// unlockVaultMarker asks for the password (or the recovery key) and opens the
// key file.
func unlockVaultMarker(cmd *cobra.Command, m *e2edecrypt.Marker, recovery, fromStdin bool) (*e2edecrypt.Keys, error) {
	label := "Vault password"
	if recovery {
		label = "Recovery key"
	}
	secret, err := readSecret(cmd, label, fromStdin)
	if err != nil {
		return nil, fmt.Errorf("reading the %s: %w", strings.ToLower(label), err)
	}
	if secret == "" {
		return nil, fmt.Errorf("no %s given", strings.ToLower(label))
	}
	if recovery {
		return m.UnlockRecoveryKey(secret)
	}
	return m.UnlockPassword(secret)
}

// ─────────────────────────── the server as a source ───────────────────────

// remoteVaultSource reads a vault on a server: `state` for the latest
// generation, `list` for the listings, the ordinary download (whole, or a
// Range) for index files and packs.
type remoteVaultSource struct {
	api    *cliclient.Client
	remote string
}

func (s *remoteVaultSource) object(rel string) string {
	rp, err := cliclient.ParseRemotePath(s.remote)
	if err != nil {
		return s.remote + "/" + rel
	}
	return rp.Join(rel).String()
}

func isNotFound(err error) bool {
	var ae *cliclient.APIError
	if errors.As(err, &ae) && ae.Status == http.StatusNotFound {
		return true
	}
	var ve *cliclient.VaultError
	return errors.As(err, &ve) && ve.Status == http.StatusNotFound
}

// LatestGeneration asks `state`.
func (s *remoteVaultSource) LatestGeneration(ctx context.Context) (uint64, error) {
	st, err := s.api.VaultState(ctx, s.remote)
	if err != nil {
		var ve *cliclient.VaultError
		if isNotFound(err) && !errors.As(err, &ve) {
			return 0, fmt.Errorf("this server has no vault API (it needs a newer filex): %w", err)
		}
		return 0, err
	}
	return st.Generation, nil
}

// ListIndexes pages through `list?kind=index`.
func (s *remoteVaultSource) ListIndexes(ctx context.Context) ([]e2edecrypt.VaultObject, error) {
	items, err := s.api.VaultListAll(ctx, s.remote, "index")
	if err != nil {
		return nil, err
	}
	out := make([]e2edecrypt.VaultObject, 0, len(items))
	for _, it := range items {
		if it.Generation == 0 {
			continue
		}
		out = append(out, e2edecrypt.VaultObject{Generation: it.Generation, Size: it.Size, ModTime: it.MTime.Time})
	}
	return out, nil
}

// ListPacks pages through `list?kind=pack`.
func (s *remoteVaultSource) ListPacks(ctx context.Context) ([]e2edecrypt.VaultObject, error) {
	items, err := s.api.VaultListAll(ctx, s.remote, "pack")
	if err != nil {
		return nil, err
	}
	out := make([]e2edecrypt.VaultObject, 0, len(items))
	for _, it := range items {
		id, ok := parsePackID(it.ID)
		if !ok {
			continue
		}
		out = append(out, e2edecrypt.VaultObject{Pack: id, Size: it.Size, ModTime: it.MTime.Time})
	}
	return out, nil
}

func parsePackID(s string) ([16]byte, bool) {
	var id [16]byte
	if len(s) != 32 || strings.ToLower(s) != s {
		return id, false
	}
	b, err := hex.DecodeString(s)
	if err != nil {
		return id, false
	}
	copy(id[:], b)
	return id, true
}

// ReadIndex downloads one index file whole (at most 64 MiB).
func (s *remoteVaultSource) ReadIndex(ctx context.Context, gen uint64) ([]byte, error) {
	p := e2e.VaultIndexPath(gen)
	b, err := s.api.ReadWhole(ctx, s.object(p), e2e.VaultIndexReadMax)
	switch {
	case errors.Is(err, cliclient.ErrObjectTooLarge):
		return nil, &e2edecrypt.CorruptError{Path: p, Err: fmt.Errorf("more than an index file may hold: %w", e2edecrypt.ErrVaultDamaged)}
	case isNotFound(err):
		return nil, fmt.Errorf("%s: %w", p, e2edecrypt.ErrVaultObjectMissing)
	}
	return b, err
}

// ReadPack fetches a byte range of a pack.
func (s *remoteVaultSource) ReadPack(ctx context.Context, id [16]byte, off, n int64) ([]byte, error) {
	p := e2e.VaultPackPath(id)
	b, err := s.api.ReadRange(ctx, s.object(p), off, n)
	if isNotFound(err) {
		return nil, fmt.Errorf("%s: %w", p, e2edecrypt.ErrVaultObjectMissing)
	}
	return b, err
}

// ─────────────────────────── a session ────────────────────────────────────

// errVaultLockGone: a write was attempted without the write lock.
var errVaultLockGone = errors.New("this session does not hold the vault's write lock")

// vaultSession is one unlocked vault on a server, and its write lock when it
// holds one.
type vaultSession struct {
	api    *cliclient.Client
	remote string // the vault folder, adapter://path
	name   string // its folder name
	info   e2e.VaultInfo
	keys   *e2edecrypt.VaultKeys
	src    *remoteVaultSource
	client string // cli | mount
	label  string

	mu       sync.Mutex
	token    string
	uploaded map[[16]byte]bool
}

func openVaultSession(ctx context.Context, cmd *cobra.Command, f *vaultFlags, remote, client string) (*vaultSession, error) {
	api, err := (&clientOpts{url: f.url, token: f.token}).api(true)
	if err != nil {
		return nil, err
	}
	rp, err := cliclient.ParseRemotePath(remote)
	if err != nil {
		return nil, err
	}
	if rp.IsRoot() {
		return nil, errors.New("a vault is a folder: name it, like docs://Kasa")
	}
	wire := rp.String()
	m, err := readRemoteVaultMarker(ctx, api, wire)
	if err != nil {
		return nil, err
	}
	keys, err := unlockVaultMarker(cmd, m, f.recovery, f.fromStdin)
	if err != nil {
		return nil, err
	}
	vk, err := e2edecrypt.NewVaultKeys(keys.FMK, m.Vault.ID)
	clear(keys.FMK)
	if err != nil {
		return nil, err
	}
	label, _ := os.Hostname()
	if len(label) > 64 {
		label = label[:64]
	}
	return &vaultSession{
		api: api, remote: wire, name: rp.Base(), info: *m.Vault, keys: vk,
		src:    &remoteVaultSource{api: api, remote: wire},
		client: client, label: label,
		uploaded: map[[16]byte]bool{},
	}, nil
}

// close drops the keys from memory.
func (s *vaultSession) close() { s.keys.Wipe() }

func (s *vaultSession) heldToken() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.token
}

// lock takes the write lock.
func (s *vaultSession) lock(ctx context.Context) (*cliclient.VaultLock, error) {
	l, err := s.api.VaultLock(ctx, s.remote, s.client, s.label)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	s.token = l.Token
	s.uploaded = map[[16]byte]bool{}
	s.mu.Unlock()
	return l, nil
}

func (s *vaultSession) renew(ctx context.Context, active bool) error {
	tok := s.heldToken()
	if tok == "" {
		return errVaultLockGone
	}
	_, err := s.api.VaultRenew(ctx, s.remote, tok, active)
	return err
}

// dropLock forgets the token (the server ended the lock).
func (s *vaultSession) dropLock() {
	s.mu.Lock()
	s.token = ""
	s.mu.Unlock()
}

// release gives the lock back, best effort.
func (s *vaultSession) release(ctx context.Context) {
	tok := s.heldToken()
	if tok == "" {
		return
	}
	s.dropLock()
	_ = s.api.VaultRelease(ctx, s.remote, tok)
}

// lockHolder says who holds a lock someone else has.
func lockHolder(err error) (string, bool) {
	var ve *cliclient.VaultError
	if !errors.As(err, &ve) || ve.Code != cliclient.VaultCodeLocked {
		return "", false
	}
	who := "someone"
	if ve.Holder != nil {
		who = ve.Holder.String()
	}
	if !ve.Since.IsZero() {
		who += " (since " + ve.Since.Local().Format("15:04") + ")"
	}
	return who, true
}

// outcomeUnknown reports whether a failed upload may have landed anyway: no
// answer at all, or a server error.
func outcomeUnknown(ctx context.Context, err error) bool {
	if ctx.Err() != nil {
		return false
	}
	var ae *cliclient.APIError
	if errors.As(err, &ae) {
		return ae.Status >= 500
	}
	var ve *cliclient.VaultError
	if errors.As(err, &ve) {
		return ve.Status >= 500
	}
	return true
}

// sink stores a pack under the write lock (e2edecrypt.VaultPackSink).
func (s *vaultSession) sink(ctx context.Context, id [16]byte, pack []byte) error {
	tok := s.heldToken()
	if tok == "" {
		return errVaultLockGone
	}
	err := s.api.VaultPutPack(ctx, s.remote, tok, hex.EncodeToString(id[:]), pack)
	if err == nil {
		s.mu.Lock()
		s.uploaded[id] = true
		s.mu.Unlock()
		return nil
	}
	if outcomeUnknown(ctx, err) {
		// It may have landed: count it as this writer's, so a collection
		// never deletes it under a commit that names it.
		s.mu.Lock()
		s.uploaded[id] = true
		s.mu.Unlock()
		return fmt.Errorf("%v: %w", err, e2edecrypt.ErrVaultPackUnknown)
	}
	return err
}

// commit seals the writer's generation and stores its index file.
func (s *vaultSession) commit(ctx context.Context, w *e2edecrypt.VaultWriter) (*e2edecrypt.VaultCommit, error) {
	c, err := w.Commit(ctx)
	if err != nil {
		return nil, err
	}
	tok := s.heldToken()
	if tok == "" {
		return nil, errVaultLockGone
	}
	if _, err := s.api.VaultPutIndex(ctx, s.remote, tok, c.Index.Generation, c.File); err != nil {
		return nil, err
	}
	return c, nil
}

// collect runs one collection pass of at most limit deletions under the
// lock. The plan's Forget are for the next commit's graveyard.
func (s *vaultSession) collect(ctx context.Context, latest *e2edecrypt.VaultIndex, limit int) (e2edecrypt.VaultGCPlan, error) {
	tok := s.heldToken()
	if tok == "" {
		return e2edecrypt.VaultGCPlan{}, errVaultLockGone
	}
	indexes, err := s.src.ListIndexes(ctx)
	if err != nil {
		return e2edecrypt.VaultGCPlan{}, err
	}
	packs, err := s.src.ListPacks(ctx)
	if err != nil {
		return e2edecrypt.VaultGCPlan{}, err
	}
	s.mu.Lock()
	mine := maps.Clone(s.uploaded)
	s.mu.Unlock()
	plan := e2edecrypt.PlanVaultGC(latest, indexes, packs, mine, time.Now(), limit)
	if plan.Empty() {
		return plan, nil
	}
	ids := make([]string, 0, len(plan.Packs))
	for _, p := range plan.Packs {
		ids = append(ids, hex.EncodeToString(p[:]))
	}
	if _, _, err := s.api.VaultDelete(ctx, s.remote, tok, ids, plan.Indexes); err != nil {
		return e2edecrypt.VaultGCPlan{}, err
	}
	return plan, nil
}

// heartbeat renews the lock every 15 seconds until stopped; a lock the
// server ended, or 45 seconds without a renewal, is reported once by err.
type vaultHeartbeat struct {
	cancel context.CancelFunc
	done   chan struct{}
	mu     sync.Mutex
	lost   error
}

func (s *vaultSession) heartbeat(ctx context.Context, active func() bool) *vaultHeartbeat {
	ctx, cancel := context.WithCancel(ctx)
	hb := &vaultHeartbeat{cancel: cancel, done: make(chan struct{})}
	go func() {
		defer close(hb.done)
		t := time.NewTicker(vaultHeartbeatEvery)
		defer t.Stop()
		lastOK := time.Now()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
			}
			err := s.renew(ctx, active())
			if err == nil {
				lastOK = time.Now()
				continue
			}
			if ctx.Err() != nil {
				return
			}
			if cliclient.IsVaultCode(err, cliclient.VaultCodeLockLost) || errors.Is(err, errVaultLockGone) || time.Since(lastOK) >= vaultLockGiveUp {
				hb.mu.Lock()
				hb.lost = err
				hb.mu.Unlock()
				s.dropLock()
				return
			}
		}
	}()
	return hb
}

func (hb *vaultHeartbeat) err() error {
	hb.mu.Lock()
	defer hb.mu.Unlock()
	return hb.lost
}

func (hb *vaultHeartbeat) stop() {
	hb.cancel()
	<-hb.done
}

const (
	// vaultHeartbeatEvery is the holder's renewal period; vaultLockGiveUp
	// how long without one before the holder stops writing.
	vaultHeartbeatEvery = 15 * time.Second
	vaultLockGiveUp     = 45 * time.Second
)

// ─────────────────────────── prune ────────────────────────────────────────

func vaultPruneCmd() *cobra.Command {
	f := &vaultFlags{}
	c := &cobra.Command{
		Use:   "prune <adapter://vault>",
		Short: "Delete what a vault no longer needs, and repack its emptiest packs",
		Long: "Takes the vault's write lock and runs a full collection (docs/E2E-VAULT-FORMAT.md\n" +
			"→ \"Garbage collection\"): the index files of generations nobody needs any more,\n" +
			"packs only those generations used, and packs no generation ever used (an\n" +
			"interrupted upload, a lost lock). The three newest generations, and any replaced\n" +
			"less than 15 minutes ago, are kept for readers still working on them. Then, when\n" +
			"more than half of the vault's packs is dead space and the live bytes of the\n" +
			"emptiest packs fit in fewer of them, it copies those bytes into new packs -\n" +
			"nothing is encrypted again - and commits a generation that points there.\n\n" +
			"Writers do a smaller pass after every commit on their own; this is the full one,\n" +
			"on demand. Deletions are for good: no trash, no version.\n\n" +
			"Exit status: 0 done, 5 wrong password or recovery key, 6 the vault is damaged,\n" +
			"7 the vault needs a newer filex, 1 anything else (somebody else is writing, say).",
		Example: "  filex vault prune docs://Kasa\n" +
			"  pass show kasa | filex vault prune docs://Kasa --password-stdin",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt)
			defer stop()
			s, err := openVaultSession(ctx, cmd, f, args[0], cliclient.VaultClientCLI)
			if err != nil {
				return vaultExit(err)
			}
			defer s.close()
			return vaultExit(runVaultPrune(ctx, cmd.OutOrStdout(), s))
		},
	}
	f.register(c)
	return quiet(c)
}

func runVaultPrune(ctx context.Context, w io.Writer, s *vaultSession) error {
	l, err := s.lock(ctx)
	if who, locked := lockHolder(err); locked {
		return fmt.Errorf("%s is writing in this vault; run prune again when the lock is free", who)
	}
	if err != nil {
		return err
	}
	defer s.release(context.WithoutCancel(ctx))
	hb := s.heartbeat(ctx, func() bool { return true })
	defer hb.stop()

	st, err := e2edecrypt.LoadVault(ctx, s.src, s.keys, s.info.PackLog2, e2edecrypt.VaultLoadOptions{})
	if err != nil {
		return err
	}
	if err := st.Writable(); err != nil {
		return err
	}
	latest := st.Index
	if latest.Generation != l.Generation {
		return fmt.Errorf("the server says the latest generation is %d, its files say %d; try again", l.Generation, latest.Generation)
	}

	var deletedIdx, deletedPacks int
	var forgot [][16]byte
	pass := func() error {
		asked := map[string]bool{}
		for round := 0; round < 1000; round++ {
			if err := hb.err(); err != nil {
				return fmt.Errorf("the vault's write lock was lost: %w", err)
			}
			plan, err := s.collect(ctx, latest, e2edecrypt.VaultGCPassMax)
			if err != nil {
				return err
			}
			forgot = append(forgot, plan.Forget...)
			fresh := 0
			for _, g := range plan.Indexes {
				if k := fmt.Sprintf("i%d", g); !asked[k] {
					asked[k], fresh, deletedIdx = true, fresh+1, deletedIdx+1
				}
			}
			for _, p := range plan.Packs {
				if k := fmt.Sprintf("p%x", p[:]); !asked[k] {
					asked[k], fresh, deletedPacks = true, fresh+1, deletedPacks+1
				}
			}
			// A listing that still shows what was deleted (a storage that
			// lists late) must not keep this loop going.
			if plan.Empty() || fresh == 0 {
				return nil
			}
		}
		return nil
	}
	if err := pass(); err != nil {
		return err
	}

	if set := e2edecrypt.PlanVaultRepack(latest, s.info.PackLog2); len(set) > 0 {
		wr, err := e2edecrypt.NewVaultWriter(e2edecrypt.VaultWriterConfig{Keys: s.keys, PackLog2: s.info.PackLog2, Base: latest, Sink: s.sink})
		if err != nil {
			return err
		}
		wr.ForgetGrave(forgot)
		if err := wr.Repack(ctx, set, s.src.ReadPack); err != nil {
			return err
		}
		if err := hb.err(); err != nil {
			return fmt.Errorf("the vault's write lock was lost: %w", err)
		}
		c, err := s.commit(ctx, wr)
		if err != nil {
			return err
		}
		latest = c.Index
		fmt.Fprintf(w, "Repacked %d pack(s) into %d (generation %d). The old ones stay 15 minutes for readers; the next prune deletes them.\n", len(set), len(c.Written), c.Index.Generation)
		if err := pass(); err != nil {
			return err
		}
	}
	if deletedIdx == 0 && deletedPacks == 0 {
		fmt.Fprintln(w, "Nothing to delete.")
	} else {
		fmt.Fprintf(w, "Deleted %d index file(s) and %d pack(s).\n", deletedIdx, deletedPacks)
	}
	if msg := e2edecrypt.VaultWarning(latest.Tree.Len(), latest.Size); msg != "" {
		fmt.Fprintln(w, "warning: "+msg)
	}
	return nil
}
