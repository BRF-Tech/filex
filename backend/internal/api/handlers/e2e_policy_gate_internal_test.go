package handlers

import (
	"bytes"
	"context"
	"errors"
	"log"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/auth"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/e2epolicy"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/storage"
	"github.com/brf-tech/filex/backend/internal/storage/drivers/local"
	"github.com/brf-tech/filex/backend/internal/testutil/dbtest"
)

// checkE2EWrite: a folder named like an encryption is not the file. Writing
// the file there creates it — an object store keeps it beside the folder — so
// the rule is asked, and with the policy off refuses; only a file that is
// there is a rewrite. The rule on its own, with nothing in front of it: at a
// door a kind guard or a name check can answer first. The doors that ask it
// are walked in e2e_policy_folder_test.go.
func TestCheckE2EWrite_AFolderWithTheNameIsNotTheFile(t *testing.T) {
	ctx := context.Background()
	_, store := dbtest.NewTestDB(t)
	require.NoError(t, store.UpsertSetting(ctx, model.SettingE2EPolicy, model.E2EPolicyOff))
	dbtest.SeedRegularUser(t, store, "p@example.com", "PermPass!1")
	u, err := store.GetUserByEmail(ctx, "p@example.com")
	require.NoError(t, err)
	root := t.TempDir()
	st, err := store.CreateStorage(ctx, &model.Storage{Name: "main", Driver: "local", Enabled: true, ConfigJSON: []byte(`{}`)})
	require.NoError(t, err)
	drv := &local.Driver{}
	require.NoError(t, drv.Init(ctx, map[string]any{"path": root}))
	for _, dir := range []string{"Acik/.filex-e2e.json", "Acik/x.fxe", "Kasa"} {
		require.NoError(t, os.MkdirAll(filepath.Join(root, filepath.FromSlash(dir)), 0o755))
	}
	require.NoError(t, os.WriteFile(filepath.Join(root, "Kasa", ".filex-e2e.json"), []byte(`{"v":2}`), 0o644))
	svc := e2epolicy.New(e2epolicy.Options{Store: store})

	for _, rel := range []string{"Acik/.filex-e2e.json", "Acik/x.fxe"} {
		var refused *e2epolicy.RefusedError
		require.ErrorAs(t, checkE2EWrite(ctx, svc, drv, u, st, rel), &refused, "%s: a folder with the name counted as the file", rel)
		require.Equal(t, e2epolicy.ReasonPolicyOff, refused.Reason, rel)
	}
	require.NoError(t, checkE2EWrite(ctx, svc, drv, u, st, "Kasa/.filex-e2e.json"), "rewriting a key file that is there asked the rule")
}

// checkE2ERename asks every rename, move or copy onto a key file's or a
// `.fxe`'s name but three — a folder's, a `.fxe` that stays a `.fxe`, a key
// file that stays its own folder's on its own storage
// (e2epolicy.RelocationEncrypts) — and then as a create at the destination:
// no HTTP door that renames replaces what holds its destination (a rename
// refuses a taken name, a move or a copy lands beside it as `…-copy`, a new
// `.fxe` beside a `.fxe`). With the policy off, nil means it was not asked.
func TestCheckE2ERename_OnlyARelocationThatEncrypts(t *testing.T) {
	ctx := context.Background()
	_, store := dbtest.NewTestDB(t)
	require.NoError(t, store.UpsertSetting(ctx, model.SettingE2EPolicy, model.E2EPolicyOff))
	dbtest.SeedRegularUser(t, store, "p@example.com", "PermPass!1")
	u, err := store.GetUserByEmail(ctx, "p@example.com")
	require.NoError(t, err)
	root := t.TempDir()
	st, err := store.CreateStorage(ctx, &model.Storage{Name: "main", Driver: "local", Enabled: true, ConfigJSON: []byte(`{}`)})
	require.NoError(t, err)
	drv := &local.Driver{}
	require.NoError(t, drv.Init(ctx, map[string]any{"path": root}))
	require.NoError(t, os.MkdirAll(filepath.Join(root, "Acik", "Dosyalar"), 0o755))
	for _, file := range []string{"Acik/rapor.bin", "Acik/a.fxe", "Acik/eski.fxe", "Kasa/.filex-e2e.json"} {
		p := filepath.Join(root, filepath.FromSlash(file))
		require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
		require.NoError(t, os.WriteFile(p, []byte("x"), 0o644))
	}
	svc := e2epolicy.New(e2epolicy.Options{Store: store})

	// across: the destination is on another storage than the source.
	for _, c := range []struct {
		why, src, dst string
		across        bool
	}{
		{why: "a plain file given a .fxe's name", src: "Acik/rapor.bin", dst: "Acik/rapor.bin.fxe"},
		{why: "a plain file given a key file's name elsewhere", src: "/Acik/rapor.bin", dst: "Klasor/.FILEX-E2E.JSON"},
		{why: "a plain file onto a .fxe that is there: it lands beside it", src: "Acik/rapor.bin", dst: "Acik/eski.fxe"},
		{why: "a .fxe given a key file's name", src: "Acik/a.fxe", dst: "Acik/.filex-e2e.json"},
		{why: "a key file renamed .fxe", src: "Kasa/.filex-e2e.json", dst: "Kasa/k.fxe"},
		{why: "a key file moved into another folder", src: "Kasa/.filex-e2e.json", dst: "Klasor/.filex-e2e.json"},
		{why: "a key file onto its folder's path on another storage", src: "Kasa/.filex-e2e.json", dst: "Kasa/.filex-e2e.json", across: true},
	} {
		var refused *e2epolicy.RefusedError
		require.ErrorAs(t, checkE2ERename(ctx, svc, drv, u, st, c.src, c.dst, !c.across), &refused, "%s was not asked", c.why)
		require.Equal(t, e2epolicy.ReasonPolicyOff, refused.Reason, c.why)
	}
	for _, c := range []struct{ why, src, dst string }{
		{"a .fxe renamed .fxe", "Acik/a.fxe", "Acik/b.fxe"},
		{"a .fxe moved into another folder as a .fxe", "Acik/a.fxe", "Klasor/a.fxe"},
		{"a key file that stays its folder's", "Kasa/.filex-e2e.json", "/Kasa/.FILEX-E2E.JSON"},
		{"a folder given a key file's name", "Acik/Dosyalar", "Acik/.filex-e2e.json"},
		{"a folder given a .fxe's name", "/Acik/Dosyalar/", "Acik/d.fxe"},
		{"a plain file given a plain name", "Acik/rapor.bin", "Acik/rapor.txt"},
	} {
		require.NoError(t, checkE2ERename(ctx, svc, drv, u, st, c.src, c.dst, true), "%s was asked", c.why)
	}
	// What cannot be looked at counts as a file; an unwired rule allows.
	require.Error(t, checkE2ERename(ctx, svc, nil, u, st, "Acik/Dosyalar", "Acik/d.fxe", true), "no driver: the source counted as a folder")
	require.NoError(t, checkE2ERename(ctx, nil, drv, u, st, "Acik/rapor.bin", "Acik/rapor.bin.fxe", true), "an unwired rule refused")

	// A rename whose names decide costs no lookup.
	counted := &statCounter{Driver: drv}
	for _, c := range [][2]string{{"Acik/rapor.bin", "Acik/rapor.txt"}, {"Acik/a.fxe", "Acik/b.fxe"}, {"Kasa/.filex-e2e.json", "Kasa/.FILEX-E2E.JSON"}} {
		require.NoError(t, checkE2ERename(ctx, svc, counted, u, st, c[0], c[1], true), "%s → %s", c[0], c[1])
	}
	require.Zero(t, counted.stats, "renames whose names decide looked at their source")
}

// statCounter counts the Stats a driver answers.
type statCounter struct {
	storage.Driver
	stats int
}

func (c *statCounter) Stat(ctx context.Context, p string) (storage.Object, error) {
	c.stats++
	return c.Driver.Stat(ctx, p)
}

// An HTTP door that cannot decide the rule answers 500 and logs it (answerE2E)
// with who and where — the storage and the person, the fields the protocols
// log (protoperm.EncryptionAllowed) — never the path: a file's name can say as
// much as its contents. A refusal is an answer, and logs nothing.
func TestAnswerE2E_AnUndecidedRuleIsLoggedWithoutThePath(t *testing.T) {
	ctx := context.Background()
	_, store := dbtest.NewTestDB(t)
	require.NoError(t, store.UpsertSetting(ctx, model.SettingE2EPolicy, model.E2EPolicyOff))
	// Somebody else first, so the person's id is not the storage's.
	dbtest.SeedRegularUser(t, store, "first@example.com", "PermPass!1")
	dbtest.SeedRegularUser(t, store, "p@example.com", "PermPass!1")
	u, err := store.GetUserByEmail(ctx, "p@example.com")
	require.NoError(t, err)
	st, err := store.CreateStorage(ctx, &model.Storage{Name: "main", Driver: "local", Enabled: true, ConfigJSON: []byte(`{}`)})
	require.NoError(t, err)
	require.NotEqual(t, st.ID, u.ID)
	broken := dbtest.SettingFails(store, model.SettingE2EPolicy, errors.New("database is locked"))
	logs := e2eLogs(t)
	const rel = "Gizli/yeni.fxe"
	// Both ways a door asks — it has decided the write creates, or the rule's
	// own look decides — answer through the one writer.
	doors := []struct {
		name string
		ask  func(http.ResponseWriter, *http.Request, *e2epolicy.Service) bool
	}{
		{"refuseE2ECreate", func(w http.ResponseWriter, r *http.Request, svc *e2epolicy.Service) bool {
			return refuseE2ECreate(w, r, svc, st, rel)
		}},
		{"refuseE2EWrite", func(w http.ResponseWriter, r *http.Request, svc *e2epolicy.Service) bool {
			return refuseE2EWrite(w, r, svc, nil, st, rel)
		}},
		{"refuseE2ERenameAt", func(w http.ResponseWriter, r *http.Request, svc *e2epolicy.Service) bool {
			done, _ := refuseE2ERenameAt(w, r, svc, store, nil, st.ID, "Gizli/yeni.bin", rel, true)
			return done
		}},
	}

	for _, door := range doors {
		for _, c := range []struct {
			store db.Store
			code  int
		}{{store, http.StatusForbidden}, {broken, http.StatusInternalServerError}} {
			w := httptest.NewRecorder()
			r := httptest.NewRequest(http.MethodPost, "/", nil).WithContext(auth.WithUser(ctx, u))
			require.True(t, door.ask(w, r, e2epolicy.New(e2epolicy.Options{Store: c.store})), "%s let the write on", door.name)
			require.Equal(t, c.code, w.Code, "%s: %s", door.name, w.Body.String())
			if c.code == http.StatusInternalServerError {
				assert.Contains(t, w.Body.String(), "could not check the encryption policy", door.name)
			}
		}
	}
	lines := e2eLogLines(logs.String())
	require.Len(t, lines, len(doors), "one line for each undecided rule and none for a refusal: %s", logs.String())
	for _, line := range lines {
		for _, want := range []string{
			"level=ERROR", "storage_id=" + strconv.FormatInt(st.ID, 10), "user_id=" + strconv.FormatInt(u.ID, 10), "database is locked",
		} {
			assert.Contains(t, line, want)
		}
		for _, leak := range []string{"Gizli", "yeni.fxe"} {
			assert.NotContains(t, line, leak, "the log line names the path")
		}
	}
}

// storageFails is a store whose storage rows cannot be read.
type storageFails struct {
	db.Store
	err error
}

func (s *storageFails) GetStorage(context.Context, int64) (*model.Storage, error) { return nil, s.err }

// A door that holds only the storage's id reads the row to ask the rule. A
// row that cannot be read leaves the rule undecided — a 500 and one line with
// who and where, never a refusal for want of a storage — and is read only
// when the rule is to be asked: not for a name that encrypts nothing, nor for
// a key file that is there to rewrite.
func TestRefuseE2EWriteAt_AStorageRowThatCannotBeReadIsUndecided(t *testing.T) {
	ctx := context.Background()
	_, store := dbtest.NewTestDB(t)
	dbtest.SeedRegularUser(t, store, "p@example.com", "PermPass!1")
	u, err := store.GetUserByEmail(ctx, "p@example.com")
	require.NoError(t, err)
	root := t.TempDir()
	st, err := store.CreateStorage(ctx, &model.Storage{Name: "main", Driver: "local", Enabled: true, ConfigJSON: []byte(`{}`)})
	require.NoError(t, err)
	drv := &local.Driver{}
	require.NoError(t, drv.Init(ctx, map[string]any{"path": root}))
	require.NoError(t, os.MkdirAll(filepath.Join(root, "Kasa"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "Kasa", ".filex-e2e.json"), []byte(`{"v":2}`), 0o644))
	broken := &storageFails{Store: store, err: errors.New("database is locked")}
	svc := e2epolicy.New(e2epolicy.Options{Store: store})
	logs := e2eLogs(t)
	ask := func(drv storage.Driver, rel string) (bool, *httptest.ResponseRecorder) {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodPost, "/", nil).WithContext(auth.WithUser(ctx, u))
		return refuseE2EWriteAt(w, r, svc, drv, broken, st.ID, rel), w
	}

	done, w := ask(drv, "Gizli/yeni.fxe")
	require.True(t, done, "the write went on")
	require.Equal(t, http.StatusInternalServerError, w.Code, w.Body.String())
	assert.Contains(t, w.Body.String(), "could not check the encryption policy")
	for _, rel := range []string{"Gizli/notlar.txt", "Kasa/.filex-e2e.json"} {
		done, w = ask(drv, rel)
		require.False(t, done, "%s: %d %s", rel, w.Code, w.Body.String())
	}
	lines := e2eLogLines(logs.String())
	require.Len(t, lines, 1, "%s", logs.String())
	for _, want := range []string{"level=ERROR", "storage_id=" + strconv.FormatInt(st.ID, 10), "user_id=" + strconv.FormatInt(u.ID, 10), "database is locked"} {
		assert.Contains(t, lines[0], want)
	}
	assert.NotContains(t, lines[0], "Gizli", "the log line names the path")
}

// e2eActor: a person who is not there any more is nobody (the rule refuses
// nobody); a person who cannot be looked up leaves the rule undecided,
// logged with their id.
func TestE2EActor_ALookupThatFailsIsUndecided(t *testing.T) {
	ctx := context.Background()
	_, store := dbtest.NewTestDB(t)
	st, err := store.CreateStorage(ctx, &model.Storage{Name: "main", Driver: "local", Enabled: true, ConfigJSON: []byte(`{}`)})
	require.NoError(t, err)
	logs := e2eLogs(t)
	gone := int64(4242)
	u, err := e2eActor(ctx, store, st, &gone)
	require.NoError(t, err)
	require.Nil(t, u)
	u, err = e2eActor(ctx, store, st, nil)
	require.NoError(t, err)
	require.Nil(t, u)
	require.Empty(t, e2eLogLines(logs.String()))

	u, err = e2eActor(ctx, &userLookupFails{Store: store}, st, &gone)
	require.ErrorIs(t, err, e2epolicy.ErrUndecided)
	require.Nil(t, u)
	lines := e2eLogLines(logs.String())
	require.Len(t, lines, 1)
	assert.Contains(t, lines[0], "user_id=4242")
	assert.Contains(t, lines[0], "connection reset")
}

type userLookupFails struct{ db.Store }

func (userLookupFails) GetUser(context.Context, int64) (*model.User, error) {
	return nil, errors.New("connection reset")
}

// e2eLogs captures what slog writes for the rest of the test. SetDefault also
// points the log package at the new handler, and putting the old logger back
// does not undo that: its writer and flags are restored by hand, as
// protoperm's tests do. The buffer is locked: a goroutine another test left
// behind may still be logging.
func e2eLogs(t *testing.T) *syncBuffer {
	t.Helper()
	buf := &syncBuffer{}
	prev, out, flags := slog.Default(), log.Writer(), log.Flags()
	slog.SetDefault(slog.New(slog.NewTextHandler(buf, nil)))
	t.Cleanup(func() {
		slog.SetDefault(prev)
		log.SetOutput(out)
		log.SetFlags(flags)
	})
	return buf
}

type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// e2eLogLines is what the HTTP doors logged about the rule.
func e2eLogLines(logs string) []string {
	var out []string
	for _, line := range strings.Split(logs, "\n") {
		if strings.Contains(line, "e2e policy:") {
			out = append(out, line)
		}
	}
	return out
}
