package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/cliclient"
	"github.com/brf-tech/filex/backend/internal/e2e"
	"github.com/brf-tech/filex/backend/internal/e2edecrypt"
)

// `filex decrypt docs://Kasa`, `filex vault mount` and `filex vault prune`
// against fakeVault: the vault API of docs/E2E-VAULT-FORMAT.md → "API" over
// files in memory, seeded with the vectors' vault (three generations, 64 KiB
// packs). The server's own implementation is tested on its side; this one
// only has to keep the contract the client relies on.

var (
	vaultTestdata   = filepath.Join("..", "..", "internal", "e2edecrypt", "testdata")
	vaultFixtureDir = filepath.Join(vaultTestdata, "vault", "v3-vault")
)

type vaultSecrets struct {
	Password    string `json:"password"`
	RecoveryKey string `json:"recovery_key"`
}

func loadVaultSecrets(t *testing.T) vaultSecrets {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(vaultTestdata, "vault-vectors.json"))
	require.NoError(t, err)
	var v struct {
		Secrets vaultSecrets `json:"secrets"`
	}
	require.NoError(t, json.Unmarshal(b, &v))
	return v.Secrets
}

// fakeVault is a server holding one vault, docs://Kasa.
type fakeVault struct {
	mu         sync.Mutex
	wire       string
	files      map[string][]byte
	mtime      map[string]time.Time
	holder     string // "", "me" (a token this fake gave), "other"
	token      string
	locks      int
	lostReason string // what the next renewal answers
	writes     int
}

func newFakeVault(t *testing.T, dir string) *fakeVault {
	t.Helper()
	f := &fakeVault{wire: "docs://Kasa", files: map[string][]byte{}, mtime: map[string]time.Time{}}
	old := time.Now().Add(-time.Hour)
	require.NoError(t, filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(dir, p)
		b, err := os.ReadFile(p)
		f.files[filepath.ToSlash(rel)] = b
		f.mtime[filepath.ToSlash(rel)] = old
		return err
	}))
	return f
}

func (f *fakeVault) latestLocked() uint64 {
	var g uint64
	for p := range f.files {
		if kind, _, gen := e2e.ParseVaultPath(p); kind == e2e.VaultPathIndex {
			g = max(g, gen)
		}
	}
	return g
}

func (f *fakeVault) latest() uint64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.latestLocked()
}

func (f *fakeVault) has(rel string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	_, ok := f.files[rel]
	return ok
}

// age moves every file's time back by d.
func (f *fakeVault) age(d time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for p, t := range f.mtime {
		f.mtime[p] = t.Add(-d)
	}
}

func (f *fakeVault) put(rel string, b []byte) {
	f.files[rel] = b
	f.mtime[rel] = time.Now()
}

func fakeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func (f *fakeVault) lockLostLocked(w http.ResponseWriter) {
	reason := f.lostReason
	if reason == "" {
		reason = "taken"
	}
	fakeJSON(w, http.StatusConflict, map[string]any{"error": "VAULT_LOCK_LOST", "message": "the lock is gone", "reason": reason})
}

func (f *fakeVault) tokenOKLocked(r *http.Request) bool {
	return f.holder == "me" && f.lostReason == "" && r.Header.Get(cliclient.VaultLockHeader) == f.token
}

func (f *fakeVault) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	q := r.URL.Query()
	rel := func(wire string) (string, bool) { return strings.CutPrefix(wire, f.wire+"/") }
	switch r.URL.Path {
	case "/api/files/manager":
		p, ok := rel(q.Get("path"))
		b, exists := f.files[p]
		if q.Get("action") != "download" || !ok || !exists {
			fakeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
			return
		}
		http.ServeContent(w, r, path.Base(p), f.mtime[p], bytes.NewReader(b))
	case "/api/files/e2e/vault/state":
		var lock any
		if f.holder != "" {
			lock = map[string]any{"holder": map[string]string{"name": "Ayşe", "client": "web"}, "since": time.Now().Format(time.RFC3339), "expires_at": time.Now().Add(time.Minute).UnixMilli(), "mine": f.holder == "me"}
		}
		fakeJSON(w, http.StatusOK, map[string]any{"vault_id": "wpU155hSR3hD1u8bSnGv7w", "pack_log2": 16, "generation": f.latestLocked(), "lock": lock})
	case "/api/files/e2e/vault/list":
		items := []map[string]any{}
		names := make([]string, 0, len(f.files))
		for p := range f.files {
			names = append(names, p)
		}
		slices.Sort(names)
		for _, p := range names {
			kind, id, gen := e2e.ParseVaultPath(p)
			item := map[string]any{"size": len(f.files[p]), "mtime": f.mtime[p].UnixMilli()}
			switch {
			case kind == e2e.VaultPathIndex && q.Get("kind") == "index":
				item["generation"] = gen
			case kind == e2e.VaultPathPack && q.Get("kind") == "pack":
				item["id"] = hex.EncodeToString(id[:])
			default:
				continue
			}
			items = append(items, item)
		}
		fakeJSON(w, http.StatusOK, map[string]any{"items": items, "next": nil})
	case "/api/files/e2e/vault/lock":
		if f.holder == "other" {
			fakeJSON(w, http.StatusConflict, map[string]any{
				"error": "VAULT_LOCKED", "message": "Ayşe is writing in this vault.",
				"holder": map[string]string{"name": "Ayşe", "client": "web", "label": "Firefox"},
				"since":  time.Now().Add(-time.Minute).Format(time.RFC3339), "retry_after": 30,
			})
			return
		}
		f.locks++
		f.holder, f.token, f.lostReason = "me", "tok-"+strconv.Itoa(f.locks), ""
		fakeJSON(w, http.StatusOK, map[string]any{"token": f.token, "generation": f.latestLocked(), "lease_seconds": 60, "idle_seconds": 180, "expires_at": time.Now().Add(time.Minute).Format(time.RFC3339)})
	case "/api/files/e2e/vault/lock/renew":
		if !f.tokenOKLocked(r) {
			f.lockLostLocked(w)
			return
		}
		fakeJSON(w, http.StatusOK, map[string]any{"expires_at": time.Now().Add(time.Minute).Format(time.RFC3339), "idle_until": time.Now().Add(3 * time.Minute).Format(time.RFC3339)})
	case "/api/files/e2e/vault/lock/release":
		if r.Header.Get(cliclient.VaultLockHeader) == f.token && f.holder == "me" {
			f.holder = ""
		}
		w.WriteHeader(http.StatusNoContent)
	case "/api/files/e2e/vault/pack":
		if !f.tokenOKLocked(r) {
			f.lockLostLocked(w)
			return
		}
		id := q.Get("id")
		p := "v/p/" + id[:2] + "/" + id + ".fxp"
		if _, ok := f.files[p]; ok {
			fakeJSON(w, http.StatusConflict, map[string]any{"error": "VAULT_PACK_EXISTS", "message": "exists"})
			return
		}
		b, _ := io.ReadAll(r.Body)
		f.put(p, b)
		f.writes++
		w.WriteHeader(http.StatusCreated)
	case "/api/files/e2e/vault/index":
		if !f.tokenOKLocked(r) {
			f.lockLostLocked(w)
			return
		}
		gen, _ := strconv.ParseUint(q.Get("generation"), 10, 64)
		if latest := f.latestLocked(); gen != latest+1 {
			fakeJSON(w, http.StatusConflict, map[string]any{"error": "VAULT_GENERATION", "message": "not latest + 1", "latest": latest})
			return
		}
		b, _ := io.ReadAll(r.Body)
		f.put(e2e.VaultIndexPath(gen), b)
		f.writes++
		fakeJSON(w, http.StatusCreated, map[string]any{"generation": gen})
	case "/api/files/e2e/vault/delete":
		if !f.tokenOKLocked(r) {
			f.lockLostLocked(w)
			return
		}
		var body struct {
			Packs   []string `json:"packs"`
			Indexes []uint64 `json:"indexes"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		latest := f.latestLocked()
		for _, g := range body.Indexes {
			if g+3 > latest {
				fakeJSON(w, http.StatusBadRequest, map[string]any{"error": "VAULT_KEEP", "message": "one of the three newest"})
				return
			}
		}
		for _, id := range body.Packs {
			delete(f.files, "v/p/"+id[:2]+"/"+id+".fxp")
		}
		for _, g := range body.Indexes {
			delete(f.files, e2e.VaultIndexPath(g))
		}
		fakeJSON(w, http.StatusOK, map[string]any{"deleted": map[string]any{"packs": len(body.Packs), "indexes": len(body.Indexes)}})
	default:
		http.NotFound(w, r)
	}
}

func serveFakeVault(t *testing.T, f *fakeVault) string {
	t.Helper()
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	return srv.URL
}

// testVaultSession is an unlocked session on the fake, as openVaultSession
// makes one after the password.
func testVaultSession(t *testing.T, url string, f *fakeVault, client string) *vaultSession {
	t.Helper()
	api := cliclient.New(cliclient.Conn{URL: url, Token: "t"})
	f.mu.Lock()
	marker := f.files[e2edecrypt.MarkerName]
	f.mu.Unlock()
	m, err := e2edecrypt.ParseMarker(marker)
	require.NoError(t, err)
	keys, err := m.UnlockPassword(loadVaultSecrets(t).Password)
	require.NoError(t, err)
	vk, err := e2edecrypt.NewVaultKeys(keys.FMK, m.Vault.ID)
	require.NoError(t, err)
	t.Cleanup(vk.Wipe)
	return &vaultSession{
		api: api, remote: f.wire, name: "Kasa", info: *m.Vault, keys: vk,
		src:    &remoteVaultSource{api: api, remote: f.wire},
		client: client, label: "test", uploaded: map[[16]byte]bool{},
	}
}

type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) Add(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

func newTestMount(t *testing.T, f *fakeVault) (*vaultMount, *vaultSession, *fakeClock, *syncBuffer) {
	t.Helper()
	s := testVaultSession(t, serveFakeVault(t, f), f, cliclient.VaultClientMount)
	st, err := e2edecrypt.LoadVault(context.Background(), s.src, s.keys, s.info.PackLog2, e2edecrypt.VaultLoadOptions{})
	require.NoError(t, err)
	clock := &fakeClock{t: time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)}
	log := &syncBuffer{}
	m, err := newVaultMount(s, st, vaultMountConfig{Log: log, Now: clock.Now, SpoolDir: t.TempDir()})
	require.NoError(t, err)
	t.Cleanup(m.cleanup)
	return m, s, clock, log
}

func mountWrite(t *testing.T, m *vaultMount, p string, data []byte) error {
	t.Helper()
	f, err := (&vaultFS{m: m}).OpenFile(context.Background(), p, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		return err
	}
	return f.Close()
}

func mountRead(t *testing.T, m *vaultMount, p string) []byte {
	t.Helper()
	f, err := (&vaultFS{m: m}).OpenFile(context.Background(), p, os.O_RDONLY, 0)
	require.NoError(t, err, p)
	defer f.Close()
	b, err := io.ReadAll(f)
	require.NoError(t, err, p)
	return b
}

func mountList(t *testing.T, m *vaultMount, dir string) []string {
	t.Helper()
	f, err := (&vaultFS{m: m}).OpenFile(context.Background(), dir, os.O_RDONLY, 0)
	require.NoError(t, err)
	infos, err := f.Readdir(-1)
	require.NoError(t, err)
	var names []string
	for _, fi := range infos {
		names = append(names, fi.Name())
	}
	slices.Sort(names)
	return names
}

// readFromServer opens the latest generation the fake holds, as another
// client would.
func readFromServer(t *testing.T, s *vaultSession, p string) ([]byte, uint64) {
	t.Helper()
	ctx := context.Background()
	st, err := e2edecrypt.LoadVault(ctx, s.src, s.keys, s.info.PackLog2, e2edecrypt.VaultLoadOptions{})
	require.NoError(t, err)
	n := st.Index.Tree.Lookup(p)
	require.NotNil(t, n, p)
	var buf bytes.Buffer
	_, err = e2edecrypt.NewVaultReader(s.src, s.keys).WriteTo(ctx, n, &buf)
	require.NoError(t, err)
	return buf.Bytes(), st.Index.Generation
}

// ─────────────────────────── filex decrypt ────────────────────────────────

func TestDecryptCmd_VaultFromTheServer(t *testing.T) {
	sec := loadVaultSecrets(t)
	f := newFakeVault(t, vaultFixtureDir)
	url := serveFakeVault(t, f)
	out := filepath.Join(t.TempDir(), "plain")
	stdout, _, err := runDecrypt(t, sec.Password+"\n", "docs://Kasa", "-o", out, "--password-stdin", "--url", url, "--token", "t")
	require.NoError(t, err)
	require.Contains(t, stdout, "Decrypted 2 file(s) and 2 folder(s)")
	b, err := os.ReadFile(filepath.Join(out, "not.txt"))
	require.NoError(t, err)
	require.Equal(t, "Kasadaki ikinci not: şeker yok.\n", string(b))
	require.DirExists(t, filepath.Join(out, "Belgeler", "Arşiv"))
	require.Zero(t, f.locks, "reading a vault takes no lock")
	require.Zero(t, f.writes)

	// An older generation the vault still keeps, by the recovery key.
	out2 := filepath.Join(t.TempDir(), "gen2")
	_, _, err = runDecrypt(t, sec.RecoveryKey+"\n", "docs://Kasa", "-o", out2, "--generation", "2", "--recovery-key", "--password-stdin", "--url", url, "--token", "t")
	require.NoError(t, err)
	big, err := os.ReadFile(filepath.Join(out2, "Belgeler", "Arşiv", "büyük.bin"))
	require.NoError(t, err)
	sum := sha256.Sum256(big)
	require.Equal(t, "0d09f3eabb5c78e5d435c41e3b3755f1c4d038657f739d0be0083c24e71235df", hex.EncodeToString(sum[:]))

	// A wrong password writes nothing.
	out3 := filepath.Join(t.TempDir(), "nope")
	_, _, err = runDecrypt(t, "not it\n", "docs://Kasa", "-o", out3, "--password-stdin", "--url", url, "--token", "t")
	require.Equal(t, exitDecryptWrongSecret, exitCode(err))
	require.NoDirExists(t, out3)
}

func TestDecryptCmd_OnlyAVaultComesFromTheServer(t *testing.T) {
	f := newFakeVault(t, filepath.Join(vaultTestdata, "folders", "v2-stream"))
	url := serveFakeVault(t, f)
	_, _, err := runDecrypt(t, "x\n", "docs://Kasa", "-o", filepath.Join(t.TempDir(), "o"), "--password-stdin", "--url", url, "--token", "t")
	require.ErrorContains(t, err, "is not a vault")
}

func TestDecryptCmd_VaultCopyByGeneration(t *testing.T) {
	sec := loadVaultSecrets(t)
	out := filepath.Join(t.TempDir(), "gen2")
	stdout, _, err := runDecrypt(t, sec.Password+"\n", vaultFixtureDir, "-o", out, "--generation", "2", "--password-stdin")
	require.NoError(t, err)
	require.Contains(t, stdout, "Decrypted 3 file(s)")
	_, _, err = runDecrypt(t, sec.Password+"\n", vaultFixtureDir, "-o", filepath.Join(t.TempDir(), "x"), "--url", "http://127.0.0.1:1", "--password-stdin")
	require.ErrorContains(t, err, "--url and --token are for a vault on a server")
}

// ─────────────────────────── filex vault mount ────────────────────────────

func TestVaultMount_ReadingTakesNoLock(t *testing.T) {
	f := newFakeVault(t, vaultFixtureDir)
	m, _, _, _ := newTestMount(t, f)
	require.Equal(t, []string{"Belgeler", "boş.txt", "not.txt"}, mountList(t, m, "/"))
	require.Equal(t, "Kasadaki ikinci not: şeker yok.\n", string(mountRead(t, m, "/not.txt")))
	require.Empty(t, mountRead(t, m, "/boş.txt"))
	fi, err := (&vaultFS{m: m}).Stat(context.Background(), "/Belgeler/Arşiv")
	require.NoError(t, err)
	require.True(t, fi.IsDir())
	require.Zero(t, f.locks, "mounting to look must not stop anybody else writing")
}

func TestVaultMount_FirstWriteTakesTheLockAndCommitsWithinSeconds(t *testing.T) {
	f := newFakeVault(t, vaultFixtureDir)
	m, s, clock, log := newTestMount(t, f)
	ctx := context.Background()
	fsys := &vaultFS{m: m}

	require.NoError(t, fsys.Mkdir(ctx, "/Yeni", 0o755))
	require.Equal(t, 1, f.locks, "the first write takes the lock")
	data := bytes.Repeat([]byte("kasa "), 300000) // 1.5 MB: two chunks, packs of 64 KiB
	require.NoError(t, mountWrite(t, m, "/Yeni/büyük.txt", data))
	require.NoError(t, fsys.Rename(ctx, "/not.txt", "/Yeni/not.txt"))
	require.Equal(t, data, mountRead(t, m, "/Yeni/büyük.txt"), "read back from the spool before the commit")
	require.Equal(t, uint64(3), f.latest(), "nothing committed yet")
	require.Contains(t, log.String(), "Took the vault's write lock")

	clock.Add(vaultCommitQuiet)
	require.NoError(t, m.tick(ctx))
	require.Equal(t, uint64(4), f.latest(), "one generation for the changes made together")

	got, gen := readFromServer(t, s, "Yeni/büyük.txt")
	require.Equal(t, uint64(4), gen)
	require.Equal(t, data, got)
	moved, _ := readFromServer(t, s, "Yeni/not.txt")
	require.Equal(t, "Kasadaki ikinci not: şeker yok.\n", string(moved))
	require.Equal(t, data, mountRead(t, m, "/Yeni/büyük.txt"), "read back from the vault after the commit")

	entries, err := os.ReadDir(m.spool)
	require.NoError(t, err)
	require.Empty(t, entries, "the spool is emptied by the commit")
	require.Equal(t, 1, f.locks, "the session keeps its lock")
}

func TestVaultMount_SpoolIsNeverPlaintext(t *testing.T) {
	f := newFakeVault(t, vaultFixtureDir)
	m, _, _, _ := newTestMount(t, f)
	secret := bytes.Repeat([]byte("gizli metin "), 1000)
	fh, err := (&vaultFS{m: m}).OpenFile(context.Background(), "/gizli.txt", os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0o644)
	require.NoError(t, err)
	_, err = fh.Write(secret)
	require.NoError(t, err)
	entries, err := os.ReadDir(m.spool)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	onDisk, err := os.ReadFile(filepath.Join(m.spool, entries[0].Name()))
	require.NoError(t, err)
	require.Len(t, onDisk, len(secret))
	require.NotContains(t, string(onDisk), "gizli")
	require.NoError(t, fh.Close())
	require.Equal(t, secret, mountRead(t, m, "/gizli.txt"))
}

func TestVaultMount_SomeoneElseWritingIsAccessDenied(t *testing.T) {
	f := newFakeVault(t, vaultFixtureDir)
	f.holder = "other"
	m, _, _, log := newTestMount(t, f)
	err := (&vaultFS{m: m}).Mkdir(context.Background(), "/Yeni", 0o755)
	require.True(t, os.IsPermission(err), "%v", err)
	require.Contains(t, log.String(), "Ayşe")

	req := httptest.NewRequest(http.MethodPut, "/"+m.secret+"/x.txt", strings.NewReader("x"))
	rec := httptest.NewRecorder()
	m.ServeHTTP(rec, req)
	require.Equal(t, http.StatusForbidden, rec.Code)
	require.Contains(t, rec.Body.String(), "Ayşe")
	require.Equal(t, uint64(3), f.latest())
	require.Equal(t, "Kasadaki ikinci not: şeker yok.\n", string(mountRead(t, m, "/not.txt")), "the vault stays readable")
}

func TestVaultMount_SystemLitterStaysInMemory(t *testing.T) {
	f := newFakeVault(t, vaultFixtureDir)
	m, _, _, _ := newTestMount(t, f)
	for _, name := range []string{"/.DS_Store", "/Belgeler/._not.txt", "/Thumbs.db", "/Belgeler/Desktop.ini"} {
		require.NoError(t, mountWrite(t, m, name, []byte("system")), name)
		require.Equal(t, "system", string(mountRead(t, m, name)))
	}
	require.Contains(t, mountList(t, m, "/"), ".DS_Store")
	require.Contains(t, mountList(t, m, "/Belgeler"), "._not.txt")
	require.NoError(t, (&vaultFS{m: m}).RemoveAll(context.Background(), "/Thumbs.db"))
	require.Zero(t, f.locks, "litter never takes the lock")
	require.Zero(t, f.writes, "and never reaches the vault")
}

func TestVaultMount_LostLockDropsWhatWasNotCommitted(t *testing.T) {
	f := newFakeVault(t, vaultFixtureDir)
	m, _, clock, log := newTestMount(t, f)
	ctx := context.Background()
	require.NoError(t, mountWrite(t, m, "/yarım.txt", []byte("not saved")))
	f.mu.Lock()
	f.lostReason, f.holder = "idle", ""
	f.mu.Unlock()

	clock.Add(vaultHeartbeatEvery)
	require.NoError(t, m.tick(ctx))
	require.False(t, m.isHeld())
	require.Contains(t, log.String(), "You did nothing for 3 minute(s)")
	require.Contains(t, log.String(), "Not saved: yarım.txt")
	_, err := (&vaultFS{m: m}).Stat(ctx, "/yarım.txt")
	require.True(t, os.IsNotExist(err), "the view is the last committed generation again")
	require.Equal(t, uint64(3), f.latest())

	// The next change takes the lock again.
	f.mu.Lock()
	f.lostReason = ""
	f.mu.Unlock()
	require.NoError(t, mountWrite(t, m, "/tekrar.txt", []byte("saved")))
	require.Equal(t, 2, f.locks)
}

func TestVaultMount_FollowsWhatOthersCommit(t *testing.T) {
	f := newFakeVault(t, vaultFixtureDir)
	m, s, clock, _ := newTestMount(t, f)
	ctx := context.Background()

	// Another client writes generation 4.
	other := testVaultSession(t, serveFakeVault(t, f), f, cliclient.VaultClientCLI)
	_, err := other.lock(ctx)
	require.NoError(t, err)
	st, err := e2edecrypt.LoadVault(ctx, other.src, other.keys, 16, e2edecrypt.VaultLoadOptions{})
	require.NoError(t, err)
	w, err := e2edecrypt.NewVaultWriter(e2edecrypt.VaultWriterConfig{Keys: other.keys, PackLog2: 16, Base: st.Index, Sink: other.sink})
	require.NoError(t, err)
	_, err = w.Write(ctx, "başkası.txt", 1, strings.NewReader("from elsewhere"), 14)
	require.NoError(t, err)
	_, err = other.commit(ctx, w)
	require.NoError(t, err)
	other.release(ctx)
	require.NotContains(t, mountList(t, m, "/"), "başkası.txt")

	clock.Add(vaultFollowEvery)
	require.NoError(t, m.tick(ctx))
	require.Contains(t, mountList(t, m, "/"), "başkası.txt")
	require.Equal(t, "from elsewhere", string(mountRead(t, m, "/başkası.txt")))
	_ = s
}

func TestVaultMount_FifteenIdleMinutesCommitReleaseAndExit(t *testing.T) {
	f := newFakeVault(t, vaultFixtureDir)
	m, s, clock, _ := newTestMount(t, f)
	ctx := context.Background()

	clock.Add(vaultMountIdleExit - time.Second)
	require.NoError(t, m.tick(ctx), "not yet")

	// A write restarts the 15 minutes; when they run out with the write not
	// committed yet, the shutdown commits it.
	require.NoError(t, mountWrite(t, m, "/son.txt", []byte("last words")))
	clock.Add(vaultMountIdleExit)
	require.ErrorIs(t, m.tick(ctx), errVaultMountIdle)
	require.Equal(t, uint64(3), f.latest(), "the idle tick itself commits nothing")
	require.NoError(t, m.shutdown(ctx))
	require.Equal(t, uint64(4), f.latest())
	got, _ := readFromServer(t, s, "son.txt")
	require.Equal(t, "last words", string(got))
	f.mu.Lock()
	defer f.mu.Unlock()
	require.Empty(t, f.holder, "the lock was given back")
}

func TestVaultMount_HTTP(t *testing.T) {
	f := newFakeVault(t, vaultFixtureDir)
	m, _, _, _ := newTestMount(t, f)
	do := func(method, target string, hdr map[string]string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, target, nil)
		for k, v := range hdr {
			req.Header.Set(k, v)
		}
		rec := httptest.NewRecorder()
		m.ServeHTTP(rec, req)
		return rec
	}
	require.Equal(t, http.StatusNotFound, do("PROPFIND", "/"+strings.Repeat("0", 32)+"/", map[string]string{"Depth": "1"}).Code, "the address is the key")
	require.Equal(t, http.StatusNotFound, do(http.MethodGet, "/", nil).Code)
	opt := do(http.MethodOptions, "/", nil)
	require.Equal(t, http.StatusOK, opt.Code)
	require.Contains(t, opt.Header().Get("DAV"), "1")

	list := do("PROPFIND", "/"+m.secret+"/", map[string]string{"Depth": "1"})
	require.Equal(t, http.StatusMultiStatus, list.Code)
	require.Contains(t, list.Body.String(), "not.txt")

	get := do(http.MethodGet, "/"+m.secret+"/not.txt", map[string]string{"Range": "bytes=0-7"})
	require.Equal(t, http.StatusPartialContent, get.Code)
	require.Equal(t, "Kasadaki", get.Body.String())

	m.port = 4242
	require.Equal(t, http.StatusForbidden, do(http.MethodGet, "/"+m.secret+"/not.txt", nil).Code, "a Host that is not this server's address")
	req := httptest.NewRequest(http.MethodGet, "/"+m.secret+"/not.txt", nil)
	req.Host = "127.0.0.1:4242"
	rec := httptest.NewRecorder()
	m.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Zero(t, f.locks)
}

// ─────────────────────────── filex vault prune ────────────────────────────

func TestVaultPrune_CollectsAndRepacks(t *testing.T) {
	f := newFakeVault(t, vaultFixtureDir)
	url := serveFakeVault(t, f)
	s := testVaultSession(t, url, f, cliclient.VaultClientCLI)
	ctx := context.Background()

	// Generations 4 to 6, a small file each, then an orphan pack; all of it
	// committed an hour ago.
	_, err := s.lock(ctx)
	require.NoError(t, err)
	for i := range 3 {
		st, err := e2edecrypt.LoadVault(ctx, s.src, s.keys, 16, e2edecrypt.VaultLoadOptions{})
		require.NoError(t, err)
		w, err := e2edecrypt.NewVaultWriter(e2edecrypt.VaultWriterConfig{Keys: s.keys, PackLog2: 16, Base: st.Index, Sink: s.sink})
		require.NoError(t, err)
		_, err = w.Write(ctx, fmt.Sprintf("f%d.txt", i), 1, strings.NewReader("x"), 1)
		require.NoError(t, err)
		_, err = s.commit(ctx, w)
		require.NoError(t, err)
	}
	s.release(ctx)
	orphan := "v/p/ab/ab" + strings.Repeat("0", 30) + ".fxp"
	f.mu.Lock()
	f.put(orphan, make([]byte, 1<<16))
	f.mu.Unlock()
	f.age(time.Hour)
	require.Equal(t, uint64(6), f.latest())

	var out bytes.Buffer
	require.NoError(t, runVaultPrune(ctx, &out, s))
	require.Contains(t, out.String(), "Repacked 4 pack(s) into 1 (generation 7)")
	require.Contains(t, out.String(), "Deleted ")
	require.Equal(t, uint64(7), f.latest())
	for g := uint64(1); g <= 4; g++ {
		require.False(t, f.has(e2e.VaultIndexPath(g)), "generation %d expired", g)
	}
	require.True(t, f.has(e2e.VaultIndexPath(5)), "the three newest stay")
	require.False(t, f.has(orphan), "an orphan goes")
	require.False(t, f.has("v/p/b0/b067d7bcd62c9f817216a5ef1b1b653b.fxp"), "a pack only generation 2 used goes")
	for i := range 3 {
		got, gen := readFromServer(t, s, fmt.Sprintf("f%d.txt", i))
		require.Equal(t, "x", string(got))
		require.Equal(t, uint64(7), gen)
	}
	note, _ := readFromServer(t, s, "not.txt")
	require.Equal(t, "Kasadaki ikinci not: şeker yok.\n", string(note))
	f.mu.Lock()
	require.Empty(t, f.holder, "prune gives the lock back")
	f.mu.Unlock()
}

func TestVaultPrune_WaitsForSomeoneElsesLock(t *testing.T) {
	f := newFakeVault(t, vaultFixtureDir)
	f.holder = "other"
	s := testVaultSession(t, serveFakeVault(t, f), f, cliclient.VaultClientCLI)
	err := runVaultPrune(context.Background(), io.Discard, s)
	require.ErrorContains(t, err, "Ayşe")
}

func TestVaultCmd_TakesNoSecretFlag(t *testing.T) {
	for _, c := range vaultCmd().Commands() {
		for _, name := range []string{"password", "pass", "secret", "key", "recovery"} {
			require.Nil(t, c.Flags().Lookup(name), "%s: --%s must not exist", c.Name(), name)
		}
	}
}

func TestSpoolCounter(t *testing.T) {
	ctr := [16]byte{15: 0xff}
	addCounter(&ctr, 1)
	require.Equal(t, [16]byte{14: 1}, ctr)
	ctr = [16]byte{}
	addCounter(&ctr, 0x1_0000_0001)
	require.Equal(t, [16]byte{11: 1, 15: 1}, ctr)
}
