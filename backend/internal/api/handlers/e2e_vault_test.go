package handlers_test

// The vault API (encryption level 3), over the real router:
// docs/E2E-VAULT-FORMAT.md → The write lock → API, and → Writes from anywhere
// else for the explorer's own doors. The server does no cryptography here,
// and neither do these tests: a pack is a header and random bytes, an index
// file a header and zeros - all the server can see of either.

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/api"
	"github.com/brf-tech/filex/backend/internal/api/handlers"
	"github.com/brf-tech/filex/backend/internal/auth"
	"github.com/brf-tech/filex/backend/internal/config"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/e2e"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/pathkey"
	"github.com/brf-tech/filex/backend/internal/realtime"
	"github.com/brf-tech/filex/backend/internal/storage"
	"github.com/brf-tech/filex/backend/internal/storage/drivers/local"
	"github.com/brf-tech/filex/backend/internal/tenant"
	"github.com/brf-tech/filex/backend/internal/testutil"
	"github.com/brf-tech/filex/backend/internal/testutil/dbtest"
)

const vaultFixtureDir = "../../e2edecrypt/testdata/vault/v3-vault"

// vaultEvents records the vault frames the handlers send.
type vaultEvents struct {
	mu  sync.Mutex
	got []realtime.VaultEvent
}

func (e *vaultEvents) EmitVault(_ int64, _ string, ev realtime.VaultEvent) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.got = append(e.got, ev)
}

func (e *vaultEvents) all() []realtime.VaultEvent {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]realtime.VaultEvent(nil), e.got...)
}

type vaultFix struct {
	srv      *httptest.Server
	store    db.Store
	root     string
	st       *model.Storage
	adminID  int64
	admin    string
	memberID int64
	member   string
	viewer   string
	events   *vaultEvents
}

func newVaultFix(t *testing.T, on bool) *vaultFix {
	t.Helper()
	ctx := context.Background()
	root := t.TempDir()
	srv, _, store := testutil.NewTestServerWith(t, func(c *config.Config) { c.E2EVault = on }, func(d *api.Deps) {
		d.StorageResolver = verbLocalResolver(d.Store)
	})
	useProductionAuthChain(t, store)
	cfg, err := json.Marshal(map[string]any{"root": root})
	require.NoError(t, err)
	st, err := store.CreateStorage(ctx, &model.Storage{
		Name: "depo", Driver: "local", MountPath: "/depo", ConfigJSON: cfg,
		SyncMode: model.SyncModePoll, SyncIntervalS: 900, Enabled: true,
	})
	require.NoError(t, err)
	adminID, _ := testutil.SeedAdminUser(t, store)
	member, err := store.CreateUser(ctx, "vault-member@test.local", "x", model.RoleUser, "tr", "UTC")
	require.NoError(t, err)
	viewer, err := store.CreateUser(ctx, "vault-viewer@test.local", "x", model.RoleViewer, "en", "UTC")
	require.NoError(t, err)
	ev := &vaultEvents{}
	handlers.SetVaultEmitter(ev)
	t.Cleanup(func() { handlers.SetVaultEmitter(nil) })
	return &vaultFix{
		srv: srv, store: store, root: root, st: st,
		adminID: adminID, admin: testutil.NewAPIToken(t, store, adminID, "read,write,delete"),
		memberID: member.ID, member: testutil.NewAPIToken(t, store, member.ID, "read,write,delete"),
		viewer: testutil.NewAPIToken(t, store, viewer.ID, "read,write,delete"),
		events: ev,
	}
}

// do sends one request; body nil sends none. A JSON body is labelled JSON
// unless hdr says otherwise.
func (f *vaultFix) do(t *testing.T, token, method, path string, body []byte, hdr map[string]string) (int, map[string]any) {
	t.Helper()
	var rdr io.Reader
	if body != nil {
		rdr = bytes.NewReader(body)
	}
	req, err := http.NewRequest(method, f.srv.URL+path, rdr)
	require.NoError(t, err)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("X-Filex-Token", token)
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var m map[string]any
	if json.Unmarshal(raw, &m) != nil || m == nil {
		m = map[string]any{"_raw": string(raw)}
	}
	return resp.StatusCode, m
}

func (f *vaultFix) post(t *testing.T, token, path string, body any, hdr map[string]string) (int, map[string]any) {
	t.Helper()
	b, err := json.Marshal(body)
	require.NoError(t, err)
	return f.do(t, token, http.MethodPost, path, b, hdr)
}

func vaultKeyFileWith(t *testing.T, mut func(m map[string]any)) []byte {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(vaultFixtureDir, e2e.MarkerName))
	require.NoError(t, err)
	var m map[string]any
	require.NoError(t, json.Unmarshal(raw, &m))
	m["vault"].(map[string]any)["pack"] = e2e.VaultDefaultPackLog2
	if mut != nil {
		mut(m)
	}
	out, err := json.Marshal(m)
	require.NoError(t, err)
	return out
}

func vaultGeneration1(t *testing.T) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(vaultFixtureDir, "v", "idx", "0000000000000001.fxi"))
	require.NoError(t, err)
	require.Len(t, b, e2e.VaultIndexMinSize)
	return b
}

// vaultIndexOf is an index file of generation gen: the header, a seal id,
// and bytes that stand for the ciphertext.
func vaultIndexOf(gen uint64, size int) []byte {
	b := make([]byte, size)
	copy(b, e2e.VaultMagicPrefix)
	b[8], b[9] = e2e.VaultFormat, e2e.VaultKindIndex
	binary.BigEndian.PutUint64(b[16:24], gen)
	_, _ = rand.Read(b[24:])
	return b
}

// vaultPackOf is a pack of 2^log2 bytes with the header of id.
func vaultPackOf(id [16]byte, log2 int) []byte {
	b := make([]byte, 1<<log2)
	copy(b, e2e.VaultMagicPrefix)
	b[8], b[9], b[10] = e2e.VaultFormat, e2e.VaultKindPack, byte(log2)
	copy(b[16:32], id[:])
	_, _ = rand.Read(b[32:])
	return b
}

func newVaultID() ([16]byte, string) {
	var id [16]byte
	_, _ = rand.Read(id[:])
	return id, hex.EncodeToString(id[:])
}

func (f *vaultFix) create(t *testing.T, token, rel string) {
	t.Helper()
	code, m := f.post(t, token, "/api/files/e2e/vault/create", map[string]any{
		"path":   "depo://" + rel,
		"marker": json.RawMessage(vaultKeyFileWith(t, nil)),
		"index":  base64.StdEncoding.EncodeToString(vaultGeneration1(t)),
	}, nil)
	require.Equal(t, http.StatusCreated, code, "%v", m)
	require.EqualValues(t, 1, m["generation"])
}

func (f *vaultFix) lock(t *testing.T, token, rel, client string) string {
	t.Helper()
	code, m := f.post(t, token, "/api/files/e2e/vault/lock", map[string]any{"path": "depo://" + rel, "client": client, "label": "Firefox, ofis"}, nil)
	require.Equal(t, http.StatusOK, code, "%v", m)
	tok, _ := m["token"].(string)
	require.NotEmpty(t, tok)
	return tok
}

func (f *vaultFix) putIndex(t *testing.T, token, lockTok, rel string, gen uint64, body []byte) (int, map[string]any) {
	t.Helper()
	return f.do(t, token, http.MethodPut, "/api/files/e2e/vault/index?path=depo://"+rel+"&generation="+jsonNum(gen), body,
		map[string]string{"Content-Type": "application/octet-stream", handlers.VaultLockHeader: lockTok})
}

func (f *vaultFix) putPack(t *testing.T, token, lockTok, rel, id string, body []byte) (int, map[string]any) {
	t.Helper()
	return f.do(t, token, http.MethodPut, "/api/files/e2e/vault/pack?path=depo://"+rel+"&id="+id, body,
		map[string]string{"Content-Type": "application/octet-stream", handlers.VaultLockHeader: lockTok})
}

func jsonNum(n uint64) string {
	b, _ := json.Marshal(n)
	return string(b)
}

func (f *vaultFix) exists(rel string) bool {
	_, err := os.Stat(filepath.Join(f.root, filepath.FromSlash(rel)))
	return err == nil
}

func (f *vaultFix) audit(t *testing.T, action string) []*model.AuditEntry {
	t.Helper()
	rows, err := f.store.ListAuditRecent(context.Background(), 200)
	require.NoError(t, err)
	var out []*model.AuditEntry
	for _, r := range rows {
		if r.Action == action {
			out = append(out, r)
		}
	}
	return out
}

func TestVault_OffUntilSwitchedOn(t *testing.T) {
	off := newVaultFix(t, false)
	code, m := off.do(t, off.admin, http.MethodGet, "/api/files/capabilities", nil, nil)
	require.Equal(t, http.StatusOK, code)
	require.Equal(t, false, m["e2e_vault"], "the capability is always present, false while off")
	code, m = off.post(t, off.admin, "/api/files/e2e/vault/create", map[string]any{
		"path": "depo://Kasa", "marker": json.RawMessage(vaultKeyFileWith(t, nil)),
		"index": base64.StdEncoding.EncodeToString(vaultGeneration1(t)),
	}, nil)
	require.Equal(t, http.StatusNotFound, code)
	require.Equal(t, "VAULT_DISABLED", m["error"])
	require.NotEmpty(t, m["message"])
	require.False(t, off.exists("Kasa"), "nothing is written while vaults are off")
	code, _ = off.do(t, off.admin, http.MethodGet, "/api/files/e2e/vault/state?path=depo://Kasa", nil, nil)
	require.Equal(t, http.StatusNotFound, code)
	code, _ = off.do(t, off.admin, http.MethodGet, "/api/files/e2e/vault/prefs", nil, nil)
	require.Equal(t, http.StatusNotFound, code)

	on := newVaultFix(t, true)
	code, m = on.do(t, on.admin, http.MethodGet, "/api/files/capabilities", nil, nil)
	require.Equal(t, http.StatusOK, code)
	require.Equal(t, true, m["e2e_vault"])
}

func TestVault_CreateAndState(t *testing.T) {
	f := newVaultFix(t, true)
	f.create(t, f.member, "Kasa")
	require.True(t, f.exists("Kasa/.filex-e2e.json"))
	require.True(t, f.exists("Kasa/v/idx/0000000000000001.fxi"))
	onDisk, err := os.ReadFile(filepath.Join(f.root, "Kasa", ".filex-e2e.json"))
	require.NoError(t, err)
	require.JSONEq(t, string(vaultKeyFileWith(t, nil)), string(onDisk), "the key file is written as sent")

	code, m := f.do(t, f.member, http.MethodGet, "/api/files/e2e/vault/state?path=depo://Kasa", nil, nil)
	require.Equal(t, http.StatusOK, code, "%v", m)
	require.Equal(t, "wpU155hSR3hD1u8bSnGv7w", m["vault_id"], "the key file's id, base64url")
	require.EqualValues(t, 22, m["pack_log2"])
	require.EqualValues(t, 1, m["generation"])
	require.Nil(t, m["lock"])

	// The key file's row is in the catalogue: the vault rule finds the vault
	// by it.
	n, err := f.store.GetNodeByPath(context.Background(), f.st.ID, pathkey.Hash(f.st.ID, "Kasa/.filex-e2e.json"))
	require.NoError(t, err)
	require.NotNil(t, n)
	require.Len(t, f.audit(t, "vault.create"), 1)

	body := func(rel string, marker []byte, index []byte) map[string]any {
		return map[string]any{"path": "depo://" + rel, "marker": json.RawMessage(marker), "index": base64.StdEncoding.EncodeToString(index)}
	}
	refused := []struct {
		name   string
		body   map[string]any
		status int
		code   string
	}{
		{"a vault already there", body("Kasa", vaultKeyFileWith(t, nil), vaultGeneration1(t)), http.StatusConflict, "VAULT_EXISTS"},
		{"packs of 2^16 (readers only)", body("Kucuk", vaultKeyFileWith(t, func(m map[string]any) { m["vault"].(map[string]any)["pack"] = 16 }), vaultGeneration1(t)), http.StatusBadRequest, "VAULT_BAD_OBJECT"},
		{"a level-2 key file", body("Duz", vaultKeyFileWith(t, func(m map[string]any) { m["req"] = []any{"names"} }), vaultGeneration1(t)), http.StatusBadRequest, "VAULT_BAD_OBJECT"},
		{"no salt", body("Tuzsuz", vaultKeyFileWith(t, func(m map[string]any) { delete(m, "salt") }), vaultGeneration1(t)), http.StatusBadRequest, "VAULT_BAD_OBJECT"},
		{"an index of generation 2", body("Iki", vaultKeyFileWith(t, nil), vaultIndexOf(2, e2e.VaultIndexMinSize)), http.StatusBadRequest, "VAULT_BAD_OBJECT"},
		{"an index of the wrong size", body("Uzun", vaultKeyFileWith(t, nil), vaultIndexOf(1, e2e.VaultIndexMinSize+2048)), http.StatusBadRequest, "VAULT_BAD_OBJECT"},
	}
	for _, c := range refused {
		code, m := f.post(t, f.member, "/api/files/e2e/vault/create", c.body, nil)
		require.Equal(t, c.status, code, "%s: %v", c.name, m)
		require.Equal(t, c.code, m["error"], c.name)
	}
	for _, rel := range []string{"Kucuk", "Duz", "Tuzsuz", "Iki", "Uzun"} {
		require.False(t, f.exists(rel), "%s: a refused create wrote nothing", rel)
	}

	// Only a new or an empty folder.
	require.NoError(t, os.WriteFile(filepath.Join(f.root, "dosya.txt"), []byte("x"), 0o644))
	require.NoError(t, os.MkdirAll(filepath.Join(f.root, "Dolu"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(f.root, "Dolu", "a.txt"), []byte("x"), 0o644))
	require.NoError(t, os.MkdirAll(filepath.Join(f.root, "Bos"), 0o755))
	for _, rel := range []string{"dosya.txt", "Dolu"} {
		code, m := f.post(t, f.member, "/api/files/e2e/vault/create", body(rel, vaultKeyFileWith(t, nil), vaultGeneration1(t)), nil)
		require.Equal(t, http.StatusConflict, code, "%s: %v", rel, m)
		require.Equal(t, "VAULT_EXISTS", m["error"])
	}
	f.create(t, f.member, "Bos")

	// Not inside an encrypted folder - an ordinary encrypted folder, or
	// another vault.
	require.NoError(t, os.MkdirAll(filepath.Join(f.root, "Gizli"), 0o755))
	dir, err := f.store.CreateNode(context.Background(), &model.Node{StorageID: f.st.ID, Name: "Gizli", Path: "/Gizli",
		PathHash: pathkey.Hash(f.st.ID, "/Gizli"), Type: model.NodeTypeDirectory})
	require.NoError(t, err)
	_, err = f.store.CreateNode(context.Background(), &model.Node{StorageID: f.st.ID, ParentID: &dir.ID, Name: ".filex-e2e.json",
		Path: "/Gizli/.filex-e2e.json", PathHash: pathkey.Hash(f.st.ID, "/Gizli/.filex-e2e.json"), Type: model.NodeTypeFile, Size: 10})
	require.NoError(t, err)
	for _, rel := range []string{"Gizli/Kasa", "Kasa/Ic"} {
		code, m := f.post(t, f.member, "/api/files/e2e/vault/create", body(rel, vaultKeyFileWith(t, nil), vaultGeneration1(t)), nil)
		require.Equal(t, http.StatusConflict, code, "%s: %v", rel, m)
		require.Equal(t, "VAULT_NESTED", m["error"], rel)
	}

	// A folder that is not a vault, and a path that is no folder at all.
	code, m = f.do(t, f.member, http.MethodGet, "/api/files/e2e/vault/state?path=depo://Gizli", nil, nil)
	require.Equal(t, http.StatusBadRequest, code)
	require.Equal(t, "NOT_A_VAULT", m["error"])
	code, m = f.do(t, f.member, http.MethodGet, "/api/files/e2e/vault/state?path=depo://", nil, nil)
	require.Equal(t, http.StatusBadRequest, code)
	require.Equal(t, "VAULT_BAD_REQUEST", m["error"], "a storage's root is never a vault")
}

func TestVault_OneWriterAtATime(t *testing.T) {
	f := newVaultFix(t, true)
	f.create(t, f.admin, "Kasa")
	tok := f.lock(t, f.member, "Kasa", "web")

	// Somebody else - and the same person in a second tab - waits.
	for _, who := range []string{f.admin, f.member} {
		code, m := f.post(t, who, "/api/files/e2e/vault/lock", map[string]any{"path": "depo://Kasa", "client": "desktop"}, nil)
		require.Equal(t, http.StatusConflict, code, "%v", m)
		require.Equal(t, "VAULT_LOCKED", m["error"])
		holder, _ := m["holder"].(map[string]any)
		require.Equal(t, "web", holder["client"])
		require.Equal(t, "Firefox, ofis", holder["label"])
		require.NotEmpty(t, holder["name"])
		require.Positive(t, m["retry_after"])
		since, _ := m["since"].(string)
		_, err := time.Parse(time.RFC3339, since)
		require.NoError(t, err, "times are RFC 3339 strings")
		require.True(t, strings.HasSuffix(since, "Z"), "in UTC")
	}

	// The holder's state says mine; anybody else's does not.
	lockHdr := map[string]string{handlers.VaultLockHeader: tok}
	code, m := f.do(t, f.member, http.MethodGet, "/api/files/e2e/vault/state?path=depo://Kasa", nil, lockHdr)
	require.Equal(t, http.StatusOK, code)
	lock, _ := m["lock"].(map[string]any)
	require.Equal(t, true, lock["mine"])
	code, m = f.do(t, f.admin, http.MethodGet, "/api/files/e2e/vault/state?path=depo://Kasa", nil, nil)
	require.Equal(t, http.StatusOK, code)
	lock, _ = m["lock"].(map[string]any)
	require.Equal(t, false, lock["mine"])

	code, m = f.post(t, f.member, "/api/files/e2e/vault/lock/renew", map[string]any{"path": "depo://Kasa", "active": true}, lockHdr)
	require.Equal(t, http.StatusOK, code, "%v", m)
	require.NotEmpty(t, m["expires_at"])
	require.NotEmpty(t, m["idle_until"])
	code, m = f.post(t, f.member, "/api/files/e2e/vault/lock/renew", map[string]any{"path": "depo://Kasa"},
		map[string]string{handlers.VaultLockHeader: "not-a-token"})
	require.Equal(t, http.StatusConflict, code)
	require.Equal(t, "VAULT_LOCK_LOST", m["error"])

	// A viewer cannot write, and so cannot take the lock either.
	code, m = f.post(t, f.viewer, "/api/files/e2e/vault/lock", map[string]any{"path": "depo://Kasa", "client": "cli"}, nil)
	require.Equal(t, http.StatusForbidden, code, "%v", m)

	// Breaking somebody's lock: the folder's owner or an administrator, and
	// not its holder.
	code, m = f.post(t, f.member, "/api/files/e2e/vault/lock/break", map[string]any{"path": "depo://Kasa"}, nil)
	require.Equal(t, http.StatusForbidden, code, "%v", m)
	code, m = f.post(t, f.admin, "/api/files/e2e/vault/lock/break", map[string]any{"path": "depo://Kasa", "client": "web"}, nil)
	require.Equal(t, http.StatusNoContent, code, "%v", m)
	code, m = f.post(t, f.member, "/api/files/e2e/vault/lock/renew", map[string]any{"path": "depo://Kasa"}, lockHdr)
	require.Equal(t, http.StatusConflict, code)
	require.Equal(t, "VAULT_LOCK_LOST", m["error"])
	require.Equal(t, "broken", m["reason"])
	breaker, _ := m["holder"].(map[string]any)
	require.NotEmpty(t, breaker["name"], "a broken lock says who broke it")
	require.NotEmpty(t, m["message"])

	// Then the vault is free: the administrator takes it, and the member's
	// old token hears it was taken.
	adminTok := f.lock(t, f.admin, "Kasa", "desktop")
	code, m = f.post(t, f.member, "/api/files/e2e/vault/lock/renew", map[string]any{"path": "depo://Kasa"}, lockHdr)
	require.Equal(t, http.StatusConflict, code)
	require.Equal(t, "taken", m["reason"])
	holder, _ := m["holder"].(map[string]any)
	require.Equal(t, "desktop", holder["client"], "a taken lock says who holds it now")

	code, _ = f.post(t, f.admin, "/api/files/e2e/vault/lock/release", map[string]any{"path": "depo://Kasa"},
		map[string]string{handlers.VaultLockHeader: adminTok})
	require.Equal(t, http.StatusNoContent, code)
	code, m = f.do(t, f.admin, http.MethodGet, "/api/files/e2e/vault/state?path=depo://Kasa", nil, nil)
	require.Equal(t, http.StatusOK, code)
	require.Nil(t, m["lock"], "released: free at once")
	// A release that holds nothing is still 204.
	code, _ = f.post(t, f.admin, "/api/files/e2e/vault/lock/release", map[string]any{"path": "depo://Kasa"},
		map[string]string{handlers.VaultLockHeader: adminTok})
	require.Equal(t, http.StatusNoContent, code)

	// The record: two locks taken, two ended (broken, released), one break.
	require.Len(t, f.audit(t, "vault.lock"), 2)
	unlocks := f.audit(t, "vault.unlock")
	require.Len(t, unlocks, 2)
	reasons := map[any]bool{}
	for _, u := range unlocks {
		reasons[u.Metadata["reason"]] = true
	}
	require.True(t, reasons["broken"] && reasons["released"], "%v", reasons)
	breaks := f.audit(t, "vault.lock_break")
	require.Len(t, breaks, 1)
	require.NotNil(t, breaks[0].UserID)
	require.Equal(t, f.adminID, *breaks[0].UserID, "the break names who broke it")

	// The realtime frames: held, free, held, free.
	var held []bool
	for _, ev := range f.events.all() {
		if ev.Type == realtime.VaultLockEvent {
			held = append(held, ev.Held)
		}
	}
	require.Equal(t, []bool{true, false, true, false}, held)
}

func TestVault_PacksIndexesAndCollection(t *testing.T) {
	f := newVaultFix(t, true)
	f.create(t, f.member, "Kasa")
	tok := f.lock(t, f.member, "Kasa", "web")

	id, idHex := newVaultID()
	pack := vaultPackOf(id, e2e.VaultDefaultPackLog2)
	code, m := f.putPack(t, f.member, tok, "Kasa", idHex, pack)
	require.Equal(t, http.StatusCreated, code, "%v", m)
	packRel := "Kasa/" + e2e.VaultPackPath(id)
	got, err := os.ReadFile(filepath.Join(f.root, filepath.FromSlash(packRel)))
	require.NoError(t, err)
	require.True(t, bytes.Equal(pack, got), "the pack is stored byte for byte")

	// Written once, never replaced.
	code, m = f.putPack(t, f.member, tok, "Kasa", idHex, vaultPackOf(id, e2e.VaultDefaultPackLog2))
	require.Equal(t, http.StatusConflict, code)
	require.Equal(t, "VAULT_PACK_EXISTS", m["error"])
	got, _ = os.ReadFile(filepath.Join(f.root, filepath.FromSlash(packRel)))
	require.True(t, bytes.Equal(pack, got))

	// Exactly 2^pack bytes, with its own header.
	id2, id2Hex := newVaultID()
	code, m = f.putPack(t, f.member, tok, "Kasa", id2Hex, vaultPackOf(id2, e2e.VaultDefaultPackLog2)[:1<<20])
	require.Equal(t, http.StatusBadRequest, code)
	require.Equal(t, "VAULT_BAD_OBJECT", m["error"])
	_, id3Hex := newVaultID()
	code, m = f.putPack(t, f.member, tok, "Kasa", id3Hex, vaultPackOf(id2, e2e.VaultDefaultPackLog2))
	require.Equal(t, http.StatusBadRequest, code, "a header naming another id")
	require.Equal(t, "VAULT_BAD_OBJECT", m["error"])
	code, m = f.putPack(t, f.member, tok, "Kasa", "B067", vaultPackOf(id2, e2e.VaultDefaultPackLog2))
	require.Equal(t, http.StatusBadRequest, code)
	require.Equal(t, "VAULT_BAD_REQUEST", m["error"])
	require.False(t, f.exists("Kasa/"+e2e.VaultPackPath(id2)))

	// Without the lock nothing is written.
	code, m = f.putPack(t, f.member, "", "Kasa", id2Hex, vaultPackOf(id2, e2e.VaultDefaultPackLog2))
	require.Equal(t, http.StatusConflict, code)
	require.Equal(t, "VAULT_LOCK_LOST", m["error"])
	require.False(t, f.exists("Kasa/"+e2e.VaultPackPath(id2)))

	// Generations in order: only latest + 1.
	code, m = f.putIndex(t, f.member, tok, "Kasa", 3, vaultIndexOf(3, e2e.VaultIndexMinSize))
	require.Equal(t, http.StatusConflict, code)
	require.Equal(t, "VAULT_GENERATION", m["error"])
	require.EqualValues(t, 1, m["latest"])
	code, m = f.putIndex(t, f.member, tok, "Kasa", 2, vaultIndexOf(5, e2e.VaultIndexMinSize))
	require.Equal(t, http.StatusBadRequest, code, "a header of another generation")
	require.Equal(t, "VAULT_BAD_OBJECT", m["error"])
	code, m = f.putIndex(t, f.member, tok, "Kasa", 2, vaultIndexOf(2, e2e.VaultIndexMinSize+1))
	require.Equal(t, http.StatusBadRequest, code, "not a Padmé size")
	require.Equal(t, "VAULT_BAD_OBJECT", m["error"])

	// A temporary file of a commit that stopped hours ago is swept on the way.
	stale := filepath.Join(f.root, "Kasa", "v", "idx", ".tmp-0123456789abcdef")
	require.NoError(t, os.WriteFile(stale, []byte("torn"), 0o644))
	old := time.Now().Add(-2 * time.Hour)
	require.NoError(t, os.Chtimes(stale, old, old))

	for gen := uint64(2); gen <= 5; gen++ {
		code, m = f.putIndex(t, f.member, tok, "Kasa", gen, vaultIndexOf(gen, e2e.VaultIndexMinSize))
		require.Equal(t, http.StatusCreated, code, "generation %d: %v", gen, m)
		require.EqualValues(t, gen, m["generation"])
	}
	entries, err := os.ReadDir(filepath.Join(f.root, "Kasa", "v", "idx"))
	require.NoError(t, err)
	for _, e := range entries {
		require.False(t, strings.HasPrefix(e.Name(), ".tmp-"), "a temporary index file was left: %s", e.Name())
	}

	code, m = f.do(t, f.member, http.MethodGet, "/api/files/e2e/vault/state?path=depo://Kasa", nil, nil)
	require.Equal(t, http.StatusOK, code)
	require.EqualValues(t, 5, m["generation"])

	// Listing: by name, paged, with the server's clock.
	code, m = f.do(t, f.member, http.MethodGet, "/api/files/e2e/vault/list?path=depo://Kasa&kind=index&limit=3", nil, nil)
	require.Equal(t, http.StatusOK, code, "%v", m)
	items, _ := m["items"].([]any)
	require.Len(t, items, 3)
	require.EqualValues(t, 1, items[0].(map[string]any)["generation"])
	require.Equal(t, "0000000000000003", m["next"])
	now, _ := m["now"].(string)
	_, err = time.Parse(time.RFC3339, now)
	require.NoError(t, err, "now is the server's clock, RFC 3339")
	code, m = f.do(t, f.member, http.MethodGet, "/api/files/e2e/vault/list?path=depo://Kasa&kind=index&limit=3&after=0000000000000003", nil, nil)
	require.Equal(t, http.StatusOK, code)
	items, _ = m["items"].([]any)
	require.Len(t, items, 2)
	require.Nil(t, m["next"])
	code, m = f.do(t, f.member, http.MethodGet, "/api/files/e2e/vault/list?path=depo://Kasa&kind=pack", nil, nil)
	require.Equal(t, http.StatusOK, code)
	items, _ = m["items"].([]any)
	require.Len(t, items, 1)
	require.Equal(t, idHex, items[0].(map[string]any)["id"])
	require.EqualValues(t, 1<<e2e.VaultDefaultPackLog2, items[0].(map[string]any)["size"])
	code, _ = f.do(t, f.member, http.MethodGet, "/api/files/e2e/vault/list?path=depo://Kasa&kind=both", nil, nil)
	require.Equal(t, http.StatusBadRequest, code)

	// Collection: never one of the three newest generations.
	del := func(packs []string, indexes []uint64) (int, map[string]any) {
		return f.post(t, f.member, "/api/files/e2e/vault/delete", map[string]any{"path": "depo://Kasa", "packs": packs, "indexes": indexes},
			map[string]string{handlers.VaultLockHeader: tok})
	}
	for _, g := range []uint64{5, 4, 3} {
		code, m = del(nil, []uint64{g})
		require.Equal(t, http.StatusBadRequest, code, "generation %d", g)
		require.Equal(t, "VAULT_KEEP", m["error"])
	}
	require.True(t, f.exists("Kasa/v/idx/0000000000000003.fxi"))
	code, m = del([]string{idHex}, []uint64{1, 2})
	require.Equal(t, http.StatusOK, code, "%v", m)
	deleted, _ := m["deleted"].(map[string]any)
	require.EqualValues(t, 1, deleted["packs"], "counts, not names")
	require.EqualValues(t, 2, deleted["indexes"])
	require.False(t, f.exists(packRel))
	require.False(t, f.exists("Kasa/v/idx/0000000000000001.fxi"))
	require.False(t, f.exists("Kasa/v/idx/0000000000000002.fxi"))
	// A missing one counts as deleted.
	code, _ = del([]string{idHex}, nil)
	require.Equal(t, http.StatusOK, code)
	code, m = del(make([]string, 1001), nil)
	require.Equal(t, http.StatusBadRequest, code, "at most 1000 names")
	require.Equal(t, "VAULT_BAD_REQUEST", m["error"])

	// The commits reach the record of the lock they were made under.
	code, _ = f.post(t, f.member, "/api/files/e2e/vault/lock/release", map[string]any{"path": "depo://Kasa", "reason": "locked_idle"},
		map[string]string{handlers.VaultLockHeader: tok})
	require.Equal(t, http.StatusNoContent, code)
	unlocks := f.audit(t, "vault.unlock")
	require.Len(t, unlocks, 1)
	require.Equal(t, "locked_idle", unlocks[0].Metadata["reason"], "the client's idle vault lock is recorded as such")
	require.EqualValues(t, 2, unlocks[0].Metadata["first_generation"])
	require.EqualValues(t, 5, unlocks[0].Metadata["last_generation"])

	var gens []uint64
	for _, ev := range f.events.all() {
		if ev.Type == realtime.VaultGenerationEvent {
			gens = append(gens, ev.Generation)
		}
	}
	require.Equal(t, []uint64{2, 3, 4, 5}, gens)
}

func TestVault_IdleTimeIsThePersons(t *testing.T) {
	f := newVaultFix(t, true)
	code, m := f.do(t, f.member, http.MethodGet, "/api/files/e2e/vault/prefs", nil, nil)
	require.Equal(t, http.StatusOK, code)
	require.EqualValues(t, 3, m["idle_minutes"], "3 minutes until the person sets their own")
	for _, bad := range []string{`{"idle_minutes":0}`, `{"idle_minutes":11}`, `{"idle_minutes":"5"}`, `{}`} {
		code, m = f.do(t, f.member, http.MethodPut, "/api/files/e2e/vault/prefs", []byte(bad), nil)
		require.Equal(t, http.StatusBadRequest, code, "%s: %v", bad, m)
	}
	code, m = f.do(t, f.member, http.MethodPut, "/api/files/e2e/vault/prefs", []byte(`{"idle_minutes":5}`), nil)
	require.Equal(t, http.StatusOK, code, "%v", m)
	code, m = f.do(t, f.member, http.MethodGet, "/api/files/e2e/vault/prefs", nil, nil)
	require.Equal(t, http.StatusOK, code)
	require.EqualValues(t, 5, m["idle_minutes"])
	code, m = f.do(t, f.admin, http.MethodGet, "/api/files/e2e/vault/prefs", nil, nil)
	require.Equal(t, http.StatusOK, code)
	require.EqualValues(t, 3, m["idle_minutes"], "somebody else's setting is not mine")

	f.create(t, f.member, "Kasa")
	code, m = f.post(t, f.member, "/api/files/e2e/vault/lock", map[string]any{"path": "depo://Kasa", "client": "mount"}, nil)
	require.Equal(t, http.StatusOK, code, "%v", m)
	require.EqualValues(t, 300, m["idle_seconds"], "the server reads the setting when the lock is taken")
	require.EqualValues(t, 60, m["lease_seconds"])
	code, m = f.post(t, f.admin, "/api/files/e2e/vault/lock", map[string]any{"path": "depo://Kasa", "client": "phone"}, nil)
	require.Equal(t, http.StatusBadRequest, code, "client is web, desktop, cli or mount: %v", m)
}

// uploadTo sends one file through the explorer's own upload.
func (f *vaultFix) uploadTo(t *testing.T, token, dir, name string, content []byte) (int, map[string]any) {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	require.NoError(t, mw.WriteField("path", dir))
	fw, err := mw.CreateFormFile("file[]", name)
	require.NoError(t, err)
	_, _ = fw.Write(content)
	require.NoError(t, mw.Close())
	return f.do(t, token, http.MethodPost, "/api/files/manager?action=upload", buf.Bytes(), map[string]string{"Content-Type": mw.FormDataContentType()})
}

// TestVault_TheExplorersDoorsStayOut: inside a vault folder only the vault
// API writes. The explorer's own verbs answer 403 VAULT_PATH, and 409
// VAULT_KEYFILE for the key file - which its upload still rewrites, as long
// as the vault block stays (a new password); the vault folder itself is an
// ordinary folder.
func TestVault_TheExplorersDoorsStayOut(t *testing.T) {
	f := newVaultFix(t, true)
	f.create(t, f.member, "Kasa")
	require.NoError(t, os.MkdirAll(filepath.Join(f.root, "Acik"), 0o755))

	code, m := f.uploadTo(t, f.member, "depo://Kasa/v/idx", "0000000000000002.fxi", vaultIndexOf(2, e2e.VaultIndexMinSize))
	require.Equal(t, http.StatusForbidden, code, "%v", m)
	require.Equal(t, "VAULT_PATH", m["error"])
	require.NotEmpty(t, m["message"])
	require.False(t, f.exists("Kasa/v/idx/0000000000000002.fxi"))

	code, m = f.uploadTo(t, f.member, "depo://Kasa", "notes.txt", []byte("plain"))
	require.Equal(t, http.StatusForbidden, code, "%v", m)
	require.Equal(t, "VAULT_PATH", m["error"])

	code, m = f.post(t, f.member, "/api/files/manager?action=newfolder", map[string]any{"path": "depo://Kasa/v", "name": "x"}, nil)
	require.Equal(t, http.StatusForbidden, code, "%v", m)
	require.Equal(t, "VAULT_PATH", m["error"])

	code, m = f.post(t, f.member, "/api/files/manager?action=delete", map[string]any{
		"path": "depo://Kasa/v/idx", "items": []map[string]string{{"path": "depo://Kasa/v/idx/0000000000000001.fxi"}},
	}, nil)
	require.Equal(t, http.StatusForbidden, code, "%v", m)
	require.Equal(t, "VAULT_PATH", m["error"])
	require.True(t, f.exists("Kasa/v/idx/0000000000000001.fxi"))

	// The key file: not renamed, not deleted on its own.
	code, m = f.post(t, f.member, "/api/files/manager?action=rename", map[string]any{
		"path": "depo://Kasa", "item": "depo://Kasa/.filex-e2e.json", "name": "kf.json",
	}, nil)
	require.Equal(t, http.StatusConflict, code, "%v", m)
	require.Equal(t, "VAULT_KEYFILE", m["error"])
	code, m = f.post(t, f.member, "/api/files/manager?action=delete", map[string]any{
		"path": "depo://Kasa", "items": []map[string]string{{"path": "depo://Kasa/.filex-e2e.json"}},
	}, nil)
	require.Equal(t, http.StatusConflict, code, "%v", m)
	require.Equal(t, "VAULT_KEYFILE", m["error"])

	// Rewritten through the upload: a new password is fine, another vault
	// block is not.
	newPassword := vaultKeyFileWith(t, func(m map[string]any) { m["salt"] = "bmV3c2FsdA=="; m["fmk_pw"] = "bmV3cHc=" })
	code, m = f.uploadTo(t, f.member, "depo://Kasa", e2e.MarkerName, newPassword)
	require.Equal(t, http.StatusOK, code, "a password change: %v", m)
	otherPack := vaultKeyFileWith(t, func(m map[string]any) { m["vault"].(map[string]any)["pack"] = 24 })
	code, m = f.uploadTo(t, f.member, "depo://Kasa", e2e.MarkerName, otherPack)
	require.Equal(t, http.StatusConflict, code, "%v", m)
	require.Equal(t, "VAULT_KEYFILE", m["error"])
	onDisk, err := os.ReadFile(filepath.Join(f.root, "Kasa", e2e.MarkerName))
	require.NoError(t, err)
	require.JSONEq(t, string(newPassword), string(onDisk), "the refused rewrite changed nothing")

	// No upload makes a folder a vault.
	code, m = f.uploadTo(t, f.member, "depo://Acik", e2e.MarkerName, vaultKeyFileWith(t, nil))
	require.Equal(t, http.StatusConflict, code, "%v", m)
	require.Equal(t, "VAULT_KEYFILE", m["error"])
	require.False(t, f.exists("Acik/"+e2e.MarkerName))

	// The folder itself: renamed like any encrypted folder.
	code, m = f.post(t, f.member, "/api/files/manager?action=rename", map[string]any{
		"path": "depo://", "item": "depo://Kasa", "name": "Kasa-2",
	}, nil)
	require.Equal(t, http.StatusOK, code, "%v", m)
	require.True(t, f.exists("Kasa-2/.filex-e2e.json"))
}

// TestVault_AgentAndQueueDoorsStayOut: the agent API (no key, no vault API)
// and the operations queue are refused inside a vault too.
func TestVault_AgentAndQueueDoorsStayOut(t *testing.T) {
	srv, client, store, tok, _ := aiFixtureConfigured(t, true, func(c *config.Config) { c.E2EVault = true })
	ctx := context.Background()
	st, err := store.GetStorageByName(ctx, "main")
	require.NoError(t, err)
	var cfg map[string]any
	require.NoError(t, json.Unmarshal(st.ConfigJSON, &cfg))
	root := cfg["root"].(string)
	seed := dbtest.SeedVault(t, store, st.ID, root, "Kasa")

	resp := aiReq(t, client, http.MethodPost, srv.URL+"/api/ai/mkdir", tok, map[string]any{"path": "main://Kasa/v/p/zz"})
	var body map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&body)
	resp.Body.Close()
	require.Equal(t, http.StatusForbidden, resp.StatusCode, "%v", body)
	require.Equal(t, "VAULT_PATH", body["code"])

	resp = aiReq(t, client, http.MethodPost, srv.URL+"/api/ai/upload", tok, map[string]any{"path": "main://Kasa/v/idx/0000000000000002.fxi", "content": "filexvlt"})
	body = nil
	_ = json.NewDecoder(resp.Body).Decode(&body)
	resp.Body.Close()
	require.Equal(t, http.StatusForbidden, resp.StatusCode, "%v", body)
	require.Equal(t, "VAULT_PATH", body["code"])
	_, err = os.Stat(filepath.Join(root, "Kasa", "v", "idx", "0000000000000002.fxi"))
	require.True(t, os.IsNotExist(err))

	// The queue: a delete of an index file, and of the key file on its own.
	for _, c := range []struct {
		path, code string
		status     int
	}{
		{"main://" + seed.Index, "VAULT_PATH", http.StatusForbidden},
		{"main://" + seed.KeyFile, "VAULT_KEYFILE", http.StatusConflict},
	} {
		resp = aiReq(t, client, http.MethodPost, srv.URL+"/api/files/delete", tok, map[string]any{"source": []string{c.path}})
		body = nil
		_ = json.NewDecoder(resp.Body).Decode(&body)
		resp.Body.Close()
		require.Equal(t, c.status, resp.StatusCode, "%s: %v", c.path, body)
		require.Equal(t, c.code, body["error"], c.path)
	}
	for _, p := range []string{seed.Index, seed.KeyFile} {
		_, err := os.Stat(filepath.Join(root, filepath.FromSlash(p)))
		require.NoError(t, err, "%s was deleted", p)
	}
}

// TestVault_TenantBoundary: a vault on a storage outside the caller's tenant
// reads exactly like one on a storage that does not exist.
func TestVault_TenantBoundary(t *testing.T) {
	_, store := testutil.NewTestDB(t)
	ctx := context.Background()
	root := t.TempDir()
	cfg, err := json.Marshal(map[string]any{"root": root})
	require.NoError(t, err)
	st, err := store.CreateStorage(ctx, &model.Storage{Name: "depo", Driver: "local", MountPath: "/depo", ConfigJSON: cfg, Enabled: true})
	require.NoError(t, err)
	dbtest.SeedVault(t, store, st.ID, root, "Kasa")
	drv := &local.Driver{}
	require.NoError(t, drv.Init(ctx, map[string]any{"root": root}))
	mh := handlers.NewManager(store, func(int64) (storage.Driver, error) { return drv, nil })
	h := handlers.NewE2EVault(mh, true)
	u, err := store.CreateUser(ctx, "tenant-b@test.local", "x", model.RoleAdmin, "en", "UTC")
	require.NoError(t, err)

	state := func(scope *tenant.Scope, path string) (int, string) {
		req := httptest.NewRequest(http.MethodGet, "/api/files/e2e/vault/state?path="+path, nil)
		c := auth.WithUser(req.Context(), u)
		if scope != nil {
			c = tenant.WithScope(c, scope)
		}
		rec := httptest.NewRecorder()
		h.State(rec, req.WithContext(c))
		return rec.Code, rec.Body.String()
	}
	code, _ := state(nil, "depo://Kasa")
	require.Equal(t, http.StatusOK, code, "unscoped: the vault answers")
	otherTenant := &tenant.Scope{ProviderID: 9, StorageIDs: []int64{st.ID + 1000}}
	code, body := state(otherTenant, "depo://Kasa")
	require.Equal(t, http.StatusBadRequest, code)
	codeMissing, bodyMissing := state(otherTenant, "yok://Kasa")
	require.Equal(t, codeMissing, code)
	require.Equal(t, strings.Replace(bodyMissing, "yok", "depo", 1), body, "the same answer as a storage that does not exist")
}

// TestVault_ListingSaysWhichFolderIsAVault: the explorer's listing marks a
// vault folder's row (`e2e_vault`) and says when the folder it lists is in a
// vault (`e2e_vault_root`), so that no client offers the vault's own layout
// (`v/`, packs, index files) as folders to open or to choose - also a client
// that never opened that vault.
func TestVault_ListingSaysWhichFolderIsAVault(t *testing.T) {
	f := newVaultFix(t, true)
	f.create(t, f.member, "Kasa")
	require.NoError(t, os.MkdirAll(filepath.Join(f.root, "Acik"), 0o755))

	code, m := f.do(t, f.member, http.MethodGet, "/api/files/manager?action=index&path=depo://", nil, nil)
	require.Equal(t, http.StatusOK, code, "%v", m)
	require.Nil(t, m["e2e_vault_root"], "the storage root is in no vault")
	rows := map[string]map[string]any{}
	for _, it := range m["files"].([]any) {
		row := it.(map[string]any)
		rows[row["basename"].(string)] = row
	}
	require.Contains(t, rows, "Kasa")
	require.Contains(t, rows, "Acik")
	require.Equal(t, true, rows["Kasa"]["e2e_vault"], "%v", rows["Kasa"])
	require.Nil(t, rows["Acik"]["e2e_vault"])

	for _, p := range []string{"depo://Kasa", "depo://Kasa/v", "depo://Kasa/v/idx"} {
		code, m = f.do(t, f.member, http.MethodGet, "/api/files/manager?action=index&path="+p, nil, nil)
		require.Equal(t, http.StatusOK, code, "%s: %v", p, m)
		require.Equal(t, "depo://Kasa", m["e2e_vault_root"], p)
	}
	code, m = f.do(t, f.member, http.MethodGet, "/api/files/manager?action=index&path=depo://Acik", nil, nil)
	require.Equal(t, http.StatusOK, code, "%v", m)
	require.Nil(t, m["e2e_vault_root"])
}
