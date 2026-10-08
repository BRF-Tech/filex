package acl

import (
	"context"
	"errors"
	"testing"

	"github.com/brf-tech/filex/backend/internal/writegate"
)

// A Set is what every write door hands writegate.Check: with a vault finder
// attached to the resolver it answers writegate.Vaults, so the vault rule
// (inside a vault, only the vault API writes) binds every door that asks the
// gate - and with none attached, nothing changes.

type fakeVaults struct {
	storage int64
	root    string
	asked   []string
}

func (f *fakeVaults) VaultRoot(_ context.Context, storageID int64, rel string) (string, bool) {
	f.asked = append(f.asked, rel)
	if storageID != f.storage {
		return "", false
	}
	if rel == f.root || len(rel) > len(f.root) && rel[:len(f.root)+1] == f.root+"/" {
		return f.root, true
	}
	return "", false
}

var _ writegate.Vaults = (*Set)(nil)

func TestSetVaultRoot(t *testing.T) {
	ctx := context.Background()
	fv := &fakeVaults{storage: 7, root: "Kasa"}
	r := &Resolver{}
	r.AttachVaults(fv)

	s := &Set{vaults: r.vaultLookup(ctx, 7)}
	if root, ok := s.VaultRoot("/Kasa/v/p/b0/x.fxp/"); !ok || root != "Kasa" {
		t.Fatalf("VaultRoot = %q, %v", root, ok)
	}
	if fv.asked[len(fv.asked)-1] != "Kasa/v/p/b0/x.fxp" {
		t.Errorf("the finder was asked %q, not the clean path", fv.asked[len(fv.asked)-1])
	}
	if _, ok := s.VaultRoot("Kasa2/a.txt"); ok {
		t.Error("a sibling with a longer name is not in the vault")
	}
	other := &Set{vaults: r.vaultLookup(ctx, 8)}
	if _, ok := other.VaultRoot("Kasa/v"); ok {
		t.Error("the same path on another storage is another folder")
	}
	if err := writegate.Check(s, 0, writegate.Writes("Kasa/v/idx/0000000000000002.fxi")); !errors.Is(err, writegate.ErrVaultPath) {
		t.Errorf("writegate through a Set: %v", err)
	}

	var nilSet *Set
	if _, ok := nilSet.VaultRoot("Kasa/v"); ok {
		t.Error("a nil Set knows no vault")
	}
	plain := &Resolver{}
	if plain.vaultLookup(ctx, 7) != nil {
		t.Error("no finder attached must mean no lookup")
	}
	if _, ok := (&Set{vaults: plain.vaultLookup(ctx, 7)}).VaultRoot("Kasa/v"); ok {
		t.Error("without a finder no path is in a vault")
	}
	var nilResolver *Resolver
	nilResolver.AttachVaults(fv)
}
