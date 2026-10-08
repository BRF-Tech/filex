package writegate

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/syspath"
)

// table is a Locks over a plain map, the way acl.Set answers.
type table map[string]*model.AppPluginLock

func (m table) Lock(rel string) *model.AppPluginLock { return m[rel] }
func (m table) LockWithin(rel string) *model.AppPluginLock {
	if l := m[rel]; l != nil {
		return l
	}
	for k, l := range m {
		if rel == "" || strings.HasPrefix(k, rel+"/") {
			return l
		}
	}
	return nil
}

func TestCheck(t *testing.T) {
	sign := &model.AppPluginLock{Rel: "imza/NDA.docx", PluginID: 7, PluginName: "sign"}
	locks := table{"imza/NDA.docx": sign}

	cases := []struct {
		name    string
		app     int64
		targets []Target
		want    error
	}{
		{"an ordinary write", 0, []Target{Writes("docs/a.txt")}, nil},
		{"the frozen file", 0, []Target{Writes("imza/NDA.docx")}, ErrLocked},
		{"the folder around it", 0, []Target{Writes("imza")}, ErrLocked},
		{"the whole storage", 0, []Target{Writes("")}, ErrLocked},
		{"named, not changed: the folder a file goes INTO", 0, []Target{Names("imza")}, nil},
		{"named, not changed: a copy's source", 0, []Target{Names("imza/NDA.docx")}, nil},
		{"a sibling next to it", 0, []Target{Writes("imza/NDA-imzali.pdf")}, nil},
		{"the holder app", 7, []Target{Writes("imza/NDA.docx")}, nil},
		{"another app", 8, []Target{Writes("imza/NDA.docx")}, ErrLocked},
		{"a reserved name, even named", 0, []Target{Names(".filex-trash")}, syspath.ErrReserved},
		{"reserved is judged before locks", 0, []Target{Writes("imza"), Writes(".versions/1")}, syspath.ErrReserved},
		{"the desktop's claim", 0, []Target{Writes(".filex-open/a1b2c3d4e5f6-x.docx").As(syspath.PutWorkCopy)}, nil},
		{"a protocol's keep marker", 0, []Target{Writes("docs/.keepdir").As(syspath.Mounted)}, nil},
		// A draft (issue #71): its owner's editor, by the owner, and nobody else.
		{"the owner's draft, by the owner", 0, []Target{Writes(".filex-drafts/7/0123456789abcdef/a.txt").As(syspath.OwnDraft).By(7)}, nil},
		{"somebody's draft, by another", 0, []Target{Writes(".filex-drafts/7/0123456789abcdef/a.txt").As(syspath.OwnDraft).By(8)}, syspath.ErrReserved},
		{"a draft, by nobody", 0, []Target{Writes(".filex-drafts/7/0123456789abcdef/a.txt").As(syspath.OwnDraft)}, syspath.ErrReserved},
		{"a draft, without the claim", 0, []Target{Writes(".filex-drafts/7/0123456789abcdef/a.txt").By(7)}, syspath.ErrReserved},
		{"the claim buys nothing elsewhere", 0, []Target{Writes(".versions/1").As(syspath.OwnDraft).By(7)}, syspath.ErrReserved},
	}
	for _, c := range cases {
		err := Check(locks, c.app, c.targets...)
		if c.want == nil && err != nil || c.want != nil && !errors.Is(err, c.want) {
			t.Errorf("%s: Check = %v, want %v", c.name, err, c.want)
		}
	}

	var le *LockedError
	if err := Check(locks, 0, Writes("imza")); !errors.As(err, &le) || le.Lock != sign || le.Rel != "imza" {
		t.Fatalf("the refusal does not carry the lock and the path: %#v", err)
	}
	if err := Check(nil, 0, Writes("imza/NDA.docx")); err != nil {
		t.Fatalf("no lock table must judge names only: %v", err)
	}
}

func TestAppContext(t *testing.T) {
	ctx := context.Background()
	if AppFrom(ctx) != 0 {
		t.Fatal("a plain context names an app")
	}
	if AppFrom(WithApp(ctx, 7)) != 7 {
		t.Fatal("WithApp did not carry the app")
	}
}

// vaultTable is a lock table that also knows where the vaults are, the way
// acl.Set answers once the resolver has a vault finder.
type vaultTable struct {
	table
	roots []string
}

func (v vaultTable) VaultRoot(rel string) (string, bool) {
	for _, r := range v.roots {
		if rel == r || r == "" || strings.HasPrefix(rel, r+"/") {
			return r, true
		}
	}
	return "", false
}

// TestCheckVault: inside a vault folder only the vault API writes
// (docs/E2E-VAULT-FORMAT.md → Writes from anywhere else). The vault folder
// itself is an ordinary encrypted folder; its key file is rewritten only by a
// door that claims it, and never removed or moved on its own.
func TestCheckVault(t *testing.T) {
	locks := vaultTable{roots: []string{"Kasa", "ev/Arşiv"}}
	cases := []struct {
		name    string
		targets []Target
		want    error
	}{
		{"a pack, from any door", []Target{Writes("Kasa/v/p/b0/b067d7bcd62c9f817216a5ef1b1b653b.fxp")}, ErrVaultPath},
		{"an index file", []Target{Writes("Kasa/v/idx/0000000000000003.fxi")}, ErrVaultPath},
		{"a new file beside the key file", []Target{Writes("Kasa/notes.txt")}, ErrVaultPath},
		{"a folder made inside", []Target{Writes("Kasa/v/p/zz")}, ErrVaultPath},
		{"v/ itself", []Target{Writes("Kasa/v")}, ErrVaultPath},
		{"copied INTO the vault (the landing name)", []Target{Names("Kasa"), Names("Kasa/rapor.pdf")}, ErrVaultPath},
		{"a pack copied OUT (its source)", []Target{Names("Kasa/v/p/b0/x.fxp")}, ErrVaultPath},
		{"a protocol client's write", []Target{Writes("ev/Arşiv/v/idx/.tmp-1").As(syspath.Mounted)}, ErrVaultPath},
		{"the key file replaced without the claim", []Target{Writes("Kasa/.filex-e2e.json")}, ErrVaultKeyFile},
		{"the key file renamed or deleted", []Target{Writes("ev/Arşiv/.filex-e2e.json")}, ErrVaultKeyFile},
		{"the key file rewritten by its door", []Target{Writes("Kasa/.filex-e2e.json").RewritesKeyFile()}, nil},
		{"the key file only named", []Target{Names("Kasa/.filex-e2e.json")}, nil},
		{"the vault folder renamed, moved or deleted", []Target{Writes("Kasa")}, nil},
		{"the vault folder copied (its source)", []Target{Names("ev/Arşiv")}, nil},
		{"the folder around a vault", []Target{Writes("ev")}, nil},
		{"a file beside the vault", []Target{Writes("Kasa-2/a.txt"), Writes("ev/Arşivler/a.txt")}, nil},
		{"the vault API's own write", []Target{Writes("Kasa/v/p/b0/b067d7bcd62c9f817216a5ef1b1b653b.fxp").ForVault()}, nil},
		{"the vault API's key file", []Target{Writes("Kasa/.filex-e2e.json").ForVault()}, nil},
		{"spelled with slashes", []Target{Writes("/Kasa/v/")}, ErrVaultPath},
		{"filex's own names first", []Target{Writes("Kasa/.versions/1").ForVault()}, syspath.ErrReserved},
	}
	for _, c := range cases {
		err := Check(locks, 0, c.targets...)
		if c.want == nil && err != nil || c.want != nil && !errors.Is(err, c.want) {
			t.Errorf("%s: Check = %v, want %v", c.name, err, c.want)
		}
	}

	var ve *VaultPathError
	if err := Check(locks, 0, Writes("ev/Arşiv/v/idx/0000000000000001.fxi")); !errors.As(err, &ve) || ve.Root != "ev/Arşiv" || ve.KeyFile {
		t.Fatalf("the refusal does not name the vault: %#v", err)
	}
	if errors.Is(ErrVaultPath, ErrVaultKeyFile) || errors.Is(&VaultPathError{Rel: "x"}, ErrVaultKeyFile) {
		t.Fatal("a path inside and the key file are two refusals")
	}
	// A vault at the storage's root: everything but its folder (the root) and
	// its key file's claimed rewrite is inside it.
	root := vaultTable{roots: []string{""}}
	if err := Check(root, 0, Writes("v/p/b0/x.fxp")); !errors.Is(err, ErrVaultPath) {
		t.Errorf("a vault at the root: %v", err)
	}
	if err := Check(root, 0, Writes(".filex-e2e.json").RewritesKeyFile()); err != nil {
		t.Errorf("a vault at the root, its key file: %v", err)
	}
	// A lock table that does not know vaults judges names and locks only, as
	// before the vault existed.
	if err := Check(table{}, 0, Writes("Kasa/v/p/b0/x.fxp")); err != nil {
		t.Errorf("no vault answer must mean no vault rule: %v", err)
	}
	if !RefusesMounted(locks, "Kasa/v/idx/0000000000000002.fxi") || RefusesMounted(locks, "Kasa") {
		t.Error("a protocol door is judged by the same rule")
	}
}
