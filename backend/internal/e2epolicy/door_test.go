package e2epolicy_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/brf-tech/filex/backend/internal/e2epolicy"
	"github.com/brf-tech/filex/backend/internal/storage"
)

// kinds is a driver that answers Stat from a map, and counts the Stats; a
// path it does not hold fails with the error it is given (not found when nil).
type kinds struct {
	storage.Driver
	at    map[string]storage.ObjectKind
	fail  error
	stats int
}

func (k *kinds) Stat(_ context.Context, p string) (storage.Object, error) {
	k.stats++
	kind, ok := k.at[p]
	if !ok {
		if k.fail != nil {
			return storage.Object{}, k.fail
		}
		return storage.Object{}, storage.ErrNotFound
	}
	return storage.Object{Path: p, Kind: kind}, nil
}

// A relocation onto a key file's or a `.fxe`'s name is a new encryption but
// in three cases (operator decision 2026-10-03): a folder under any name, a
// `.fxe` that stays a `.fxe`, a key file that stays its own folder's on its
// own storage. The names decide first; the source is looked at only when they
// do not, and only a clean "a folder" frees it.
func TestRelocationEncrypts(t *testing.T) {
	ctx := context.Background()
	drv := &kinds{at: map[string]storage.ObjectKind{
		"Acik/rapor.bin": storage.KindFile, "Acik/a.fxe": storage.KindFile, "Acik/Dosyalar": storage.KindDirectory,
		"Acik/klasor.fxe": storage.KindDirectory, "Kasa/.filex-e2e.json": storage.KindFile,
	}}
	for _, c := range []struct {
		why, src, dst string
		across        bool
		want          bool
	}{
		{why: "a plain file given a .fxe's name", src: "Acik/rapor.bin", dst: "Acik/rapor.bin.fxe", want: true},
		{why: "a plain file given a key file's name", src: "/Acik/rapor.bin", dst: "Acik/.FILEX-E2E.JSON", want: true},
		{why: "a .fxe given a key file's name", src: "Acik/a.fxe", dst: "Acik/.filex-e2e.json", want: true},
		{why: "a key file given a .fxe's name", src: "Kasa/.filex-e2e.json", dst: "Kasa/k.fxe", want: true},
		{why: "a key file moved into another folder", src: "Kasa/.filex-e2e.json", dst: "Kasa/Alt/.filex-e2e.json", want: true},
		{why: "a key file onto its folder's path on another storage", src: "Kasa/.filex-e2e.json", dst: "Kasa/.filex-e2e.json", across: true, want: true},
		{why: "a .fxe that stays a .fxe", src: "Acik/a.fxe", dst: "Klasor/b.FXE"},
		{why: "a .fxe that stays a .fxe on another storage", src: "Acik/a.fxe", dst: "Acik/a.fxe", across: true},
		{why: "a key file that stays its folder's", src: "/Kasa/.filex-e2e.json", dst: "Kasa/.FILEX-E2E.JSON"},
		{why: "a folder given a key file's name", src: "Acik/Dosyalar", dst: "Acik/.filex-e2e.json"},
		{why: "a folder named like a .fxe given a key file's name", src: "Acik/klasor.fxe", dst: "Acik/.filex-e2e.json"},
		{why: "a plain name", src: "Kasa/.filex-e2e.json", dst: "Kasa/eski.json"},
	} {
		assert.Equal(t, c.want, e2epolicy.RelocationEncrypts(ctx, drv, c.src, c.dst, !c.across), c.why)
	}

	// The names decide these without a look at the source.
	drv.stats = 0
	for _, c := range [][2]string{{"Acik/a.fxe", "Acik/b.fxe"}, {"Kasa/.filex-e2e.json", "Kasa/.FILEX-E2E.JSON"}, {"Acik/rapor.bin", "Acik/rapor.txt"}} {
		assert.False(t, e2epolicy.RelocationEncrypts(ctx, drv, c[0], c[1], true), "%s → %s", c[0], c[1])
	}
	assert.Zero(t, drv.stats, "names that decide looked at the source")

	// Nothing to look with, or a look that fails, is a file.
	assert.True(t, e2epolicy.RelocationEncrypts(ctx, nil, "Acik/Dosyalar", "Acik/d.fxe", true), "no driver: a folder")
	broken := &kinds{fail: errors.New("connection reset")}
	assert.True(t, e2epolicy.RelocationEncrypts(ctx, broken, "Acik/Dosyalar", "Acik/d.fxe", true), "a failed Stat: a folder")
}

// FileThere: only a file that answers is there. A folder with the name, a
// path that is not there, a Stat that fails and no driver at all are not.
func TestFileThere(t *testing.T) {
	ctx := context.Background()
	drv := &kinds{at: map[string]storage.ObjectKind{"Kasa/.filex-e2e.json": storage.KindFile, "Acik/x.fxe": storage.KindDirectory}}
	assert.True(t, e2epolicy.FileThere(ctx, drv, "/Kasa/.filex-e2e.json"))
	assert.False(t, e2epolicy.FileThere(ctx, drv, "Acik/x.fxe"), "a folder with the name")
	assert.False(t, e2epolicy.FileThere(ctx, drv, "Acik/y.fxe"), "nothing there")
	assert.False(t, e2epolicy.FileThere(ctx, &kinds{fail: errors.New("connection reset")}, "Kasa/.filex-e2e.json"), "a failed Stat")
	assert.False(t, e2epolicy.FileThere(ctx, nil, "Kasa/.filex-e2e.json"), "no driver")
}
