package protoperm_test

import (
	"bytes"
	"context"
	"errors"
	"log"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/brf-tech/filex/backend/internal/auth"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/e2epolicy"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/protoperm"
	"github.com/brf-tech/filex/backend/internal/storage"
	"github.com/brf-tech/filex/backend/internal/storage/drivers/local"
	"github.com/brf-tech/filex/backend/internal/testutil/dbtest"
)

// EncryptionAllowed asks the rule about the two names only, and refuses what
// it cannot judge. The rule's own answers are internal/e2epolicy's tests; each
// protocol's perm_test.go drives a real client against a wired one.
func TestEncryptionAllowed_AsksOnlyAboutTheTwoNames(t *testing.T) {
	ctx := context.Background()
	st := &model.Storage{ID: 1, Name: "main"}
	if got := protoperm.EncryptionAllowed(ctx, nil, nil, st, "Kasa/.filex-e2e.json"); got != protoperm.EncryptionOK {
		t.Fatalf("an unwired rule (nil service) answered %v", got)
	}
	// No store behind it: every answer below comes from the name and the
	// storage row alone, with no lookup — an ordinary name passes, a key file
	// or a .fxe on a storage nobody could name is refused.
	svc := e2epolicy.New(e2epolicy.Options{})
	for _, rel := range []string{"notes.txt", "Kasa/report.pdf", "a.fxe.txt", ".filex-e2e.json.bak"} {
		if got := protoperm.EncryptionAllowed(ctx, svc, nil, st, rel); got != protoperm.EncryptionOK {
			t.Fatalf("%s encrypts nothing and was answered %v", rel, got)
		}
	}
	for _, rel := range []string{"Kasa/.filex-e2e.json", "rapor.FXE"} {
		if got := protoperm.EncryptionAllowed(ctx, svc, nil, nil, rel); got != protoperm.EncryptionRefused {
			t.Fatalf("%s with no storage row was answered %v, want refused", rel, got)
		}
	}
}

// counting is a driver that counts the Stats it answers.
type counting struct {
	storage.Driver
	stats int
}

func (c *counting) Stat(ctx context.Context, p string) (storage.Object, error) {
	c.stats++
	return c.Driver.Stat(ctx, p)
}

// unreachable is a backend that cannot say what is at a path.
type unreachable struct{ storage.Driver }

func (unreachable) Stat(context.Context, string) (storage.Object, error) {
	return storage.Object{}, errors.New("backend unavailable")
}

// fix is one storage on a local driver and a plain member on the context,
// with the policy off: every question the rule is asked is refused, so an
// OK means it was not asked.
type fix struct {
	ctx   context.Context
	store db.Store
	u     *model.User
	st    *model.Storage
	drv   *counting
	root  string
}

func encryptionOff(t *testing.T) *fix {
	t.Helper()
	ctx := context.Background()
	_, store := dbtest.NewTestDB(t)
	if err := store.UpsertSetting(ctx, model.SettingE2EPolicy, model.E2EPolicyOff); err != nil {
		t.Fatal(err)
	}
	dbtest.SeedRegularUser(t, store, "p@example.com", "PermPass!1")
	u, err := store.GetUserByEmail(ctx, "p@example.com")
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	st, err := store.CreateStorage(ctx, &model.Storage{
		Name: "main", Driver: "local", Enabled: true,
		ConfigJSON: []byte(`{"path":` + strconv.Quote(filepath.ToSlash(root)) + `}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	drv := &local.Driver{}
	if err := drv.Init(ctx, map[string]any{"path": root}); err != nil {
		t.Fatal(err)
	}
	return &fix{ctx: auth.WithUser(ctx, u), store: store, u: u, st: st, drv: &counting{Driver: drv}, root: root}
}

// rule is the rule reading through store: f.store, or a view of it that fails.
func (f *fix) rule(store db.Store) *e2epolicy.Service {
	return e2epolicy.New(e2epolicy.Options{Store: store})
}

// A folder named like an encryption is not the file. A write that would put
// the FILE there creates it — a copy replaces the folder with it, an object
// store keeps it beside the folder — so the rule is asked, and here refuses.
// Only a file that is there is a rewrite (a new password), and free. What
// cannot be looked at counts as a create: "could not look" is not "it is
// there". A name that encrypts nothing costs no lookup at all.
func TestEncryptionAllowed_AFolderWithTheNameIsNotTheFile(t *testing.T) {
	f := encryptionOff(t)
	svc := f.rule(f.store)
	for _, dir := range []string{"Acik/.filex-e2e.json", "Acik/x.fxe", "Kasa"} {
		if err := os.MkdirAll(filepath.Join(f.root, filepath.FromSlash(dir)), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(f.root, "Kasa", ".filex-e2e.json"), []byte(`{"v":2}`), 0o644); err != nil {
		t.Fatal(err)
	}
	ask := func(drv storage.Driver, rel string) protoperm.EncryptionAnswer {
		return protoperm.EncryptionAllowed(f.ctx, svc, drv, f.st, rel)
	}

	for _, rel := range []string{"Acik/.filex-e2e.json", "Acik/x.fxe"} {
		if got := ask(f.drv, rel); got != protoperm.EncryptionRefused {
			t.Errorf("%s: a folder with the name counted as the file (%v), and the rule was not asked", rel, got)
		}
	}
	if got := ask(f.drv, "Acik/yeni.fxe"); got != protoperm.EncryptionRefused {
		t.Errorf("Acik/yeni.fxe: nothing is there, and the rule was not asked (%v)", got)
	}
	if got := ask(f.drv, "Kasa/.filex-e2e.json"); got != protoperm.EncryptionOK {
		t.Errorf("rewriting a key file that is there asked the rule (%v)", got)
	}
	if got := ask(nil, "Kasa/.filex-e2e.json"); got != protoperm.EncryptionRefused {
		t.Errorf("with no driver to look, a key file counted as there (%v)", got)
	}
	if got := ask(unreachable{f.drv}, "Kasa/.filex-e2e.json"); got != protoperm.EncryptionRefused {
		t.Errorf("a Stat that failed counted as a key file that is there (%v)", got)
	}

	f.drv.stats = 0
	for _, rel := range []string{"Acik/notes.txt", "Acik/x.fxe/inside.txt", "a.fxe.txt"} {
		if got := ask(f.drv, rel); got != protoperm.EncryptionOK {
			t.Errorf("%s encrypts nothing and was answered %v", rel, got)
		}
	}
	if f.drv.stats != 0 {
		t.Errorf("names that encrypt nothing cost %d Stats, want none", f.drv.stats)
	}
}

// logged captures what slog writes for the rest of the test. SetDefault also
// points the log package at the new handler, and putting the old logger back
// does not undo that: restore the package's writer and flags by hand.
func logged(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	prev, out, flags := slog.Default(), log.Writer(), log.Flags()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	t.Cleanup(func() {
		slog.SetDefault(prev)
		log.SetOutput(out)
		log.SetFlags(flags)
	})
	return &buf
}

// A rule that cannot be decided — here the policy cannot be read — is not a
// refusal. It comes back as its own answer, for the door to give as its
// server failure, and it is logged: the storage, the person and the error,
// never the path, which may be a file's name. A refusal is an answer, and
// logs nothing.
func TestEncryptionAllowed_ARuleThatCannotBeDecidedIsNotARefusal(t *testing.T) {
	f := encryptionOff(t)
	logs := logged(t)

	if got := protoperm.EncryptionAllowed(f.ctx, f.rule(f.store), f.drv, f.st, "Gizli/yeni.fxe"); got != protoperm.EncryptionRefused {
		t.Fatalf("the policy off: %v, want refused", got)
	}
	if logs.Len() != 0 {
		t.Fatalf("a refusal was logged: %s", logs)
	}

	broken := dbtest.SettingFails(f.store, model.SettingE2EPolicy, errors.New("database is locked"))
	if got := protoperm.EncryptionAllowed(f.ctx, f.rule(broken), f.drv, f.st, "Gizli/yeni.fxe"); got != protoperm.EncryptionUndecided {
		t.Fatalf("an unreadable policy: %v, want undecided", got)
	}
	line := logs.String()
	for _, want := range []string{
		"level=ERROR", "e2e policy: could not decide a create",
		"storage_id=" + strconv.FormatInt(f.st.ID, 10), "user_id=" + strconv.FormatInt(f.u.ID, 10), "database is locked",
	} {
		if !strings.Contains(line, want) {
			t.Errorf("the log line lacks %q: %s", want, line)
		}
	}
	for _, leak := range []string{"Gizli", "yeni.fxe"} {
		if strings.Contains(line, leak) {
			t.Errorf("the log line names the path (%q): %s", leak, line)
		}
	}
}

// RenameEncryptionAllowed asks every rename onto a key file's or a `.fxe`'s
// name but three — a folder's, a `.fxe` that stays a `.fxe`, a key file that
// stays its own folder's (e2epolicy.RelocationEncrypts) — and then exactly as
// EncryptionAllowed asks a write there: a file there is replaced (free),
// nothing or a folder there is a create. With the policy off, an OK means it
// was not asked, or asked about a rewrite; a refusal means it was asked about
// a create.
func TestRenameEncryptionAllowed_OnlyARelocationThatEncrypts(t *testing.T) {
	f := encryptionOff(t)
	svc := f.rule(f.store)
	for _, dir := range []string{"Acik/Dosyalar", "Acik/klasor.fxe"} {
		if err := os.MkdirAll(filepath.Join(f.root, filepath.FromSlash(dir)), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, file := range []string{"Acik/rapor.bin", "Acik/a.fxe", "Acik/eski.fxe", "Kasa/.filex-e2e.json"} {
		p := filepath.Join(f.root, filepath.FromSlash(file))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	ask := func(drv storage.Driver, src, dst string) protoperm.EncryptionAnswer {
		return protoperm.RenameEncryptionAllowed(f.ctx, svc, drv, f.st, src, dst)
	}

	for _, c := range []struct {
		why, src, dst string
		want          protoperm.EncryptionAnswer
	}{
		{"a plain file given a .fxe's name", "Acik/rapor.bin", "Acik/rapor.bin.fxe", protoperm.EncryptionRefused},
		{"a plain file given a key file's name elsewhere", "Acik/rapor.bin", "Klasor/.FILEX-E2E.JSON", protoperm.EncryptionRefused},
		{"a plain file given a folder's .fxe name: no file there, a create", "Acik/rapor.bin", "Acik/klasor.fxe", protoperm.EncryptionRefused},
		{"a plain file onto a .fxe that is there: a rewrite", "Acik/rapor.bin", "Acik/eski.fxe", protoperm.EncryptionOK},
		{"a .fxe given a key file's name: it encrypts the folder", "Acik/a.fxe", "Acik/.filex-e2e.json", protoperm.EncryptionRefused},
		{"a key file renamed .fxe: a new .fxe", "Kasa/.filex-e2e.json", "Kasa/k.fxe", protoperm.EncryptionRefused},
		{"a key file moved into another folder: it encrypts that one", "Kasa/.filex-e2e.json", "Klasor/.filex-e2e.json", protoperm.EncryptionRefused},
		{"a .fxe renamed .fxe: encrypted already", "Acik/a.fxe", "Acik/b.fxe", protoperm.EncryptionOK},
		{"a .fxe moved into another folder as a .fxe", "Acik/a.fxe", "Klasor/a.fxe", protoperm.EncryptionOK},
		{"a key file that stays its folder's", "Kasa/.filex-e2e.json", "Kasa/.FILEX-E2E.JSON", protoperm.EncryptionOK},
		{"a folder given a key file's name", "Acik/Dosyalar", "Acik/.filex-e2e.json", protoperm.EncryptionOK},
		{"a folder named like a .fxe given a key file's name", "Acik/klasor.fxe", "Acik/.filex-e2e.json", protoperm.EncryptionOK},
		{"a folder given a .fxe's name", "Acik/Dosyalar", "Acik/d.fxe", protoperm.EncryptionOK},
		{"a plain file given a plain name", "Acik/rapor.bin", "Kasa/rapor.bin", protoperm.EncryptionOK},
	} {
		if got := ask(f.drv, c.src, c.dst); got != c.want {
			t.Errorf("%s (%s → %s): %v, want %v", c.why, c.src, c.dst, got, c.want)
		}
	}
	// What cannot be looked at counts as a file.
	if got := ask(nil, "Acik/Dosyalar", "Acik/d.fxe"); got != protoperm.EncryptionRefused {
		t.Errorf("with no driver to look, a source counted as a folder (%v)", got)
	}
	if got := ask(unreachable{f.drv}, "Acik/Dosyalar", "Acik/d.fxe"); got != protoperm.EncryptionRefused {
		t.Errorf("a Stat that failed counted the source as a folder (%v)", got)
	}
	if got := protoperm.RenameEncryptionAllowed(f.ctx, nil, f.drv, f.st, "Acik/rapor.bin", "Acik/rapor.bin.fxe"); got != protoperm.EncryptionOK {
		t.Errorf("an unwired rule (nil service) answered %v", got)
	}

	// A rename whose names cannot encrypt costs no lookup.
	f.drv.stats = 0
	for _, c := range [][2]string{
		{"Acik/rapor.bin", "Acik/rapor.txt"}, {"Acik/a.fxe", "Acik/b.fxe"}, {"Kasa/.filex-e2e.json", "Kasa/eski.json"},
		{"Kasa/.filex-e2e.json", "Kasa/.FILEX-E2E.JSON"},
	} {
		if got := ask(f.drv, c[0], c[1]); got != protoperm.EncryptionOK {
			t.Errorf("%s → %s: %v", c[0], c[1], got)
		}
	}
	if f.drv.stats != 0 {
		t.Errorf("renames that cannot encrypt cost %d Stats, want none", f.drv.stats)
	}
}
