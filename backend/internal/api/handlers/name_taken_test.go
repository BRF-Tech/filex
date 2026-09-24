package handlers

import (
	"context"
	"errors"
	"io"
	"os"
	"path"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/pathkey"
	"github.com/brf-tech/filex/backend/internal/storage"
)

// nameFakeDriver is a directory listing and nothing else, answering Stat the
// way a case-sensitive store (S3, ext4) or a case-insensitive one (a default
// macOS or Windows disk) does. That difference is the whole reason the guard
// lists the folder before it believes a Stat.
type nameFakeDriver struct {
	entries     map[string]storage.ObjectKind // storage-relative, no leading slash
	insensitive bool
	statErr     error
}

func (d *nameFakeDriver) Init(context.Context, map[string]any) error { return nil }
func (d *nameFakeDriver) Name() string                               { return "namefake" }
func (d *nameFakeDriver) Capabilities() storage.Capabilities         { return storage.Capabilities{} }
func (d *nameFakeDriver) Read(context.Context, string) (io.ReadCloser, error) {
	return nil, storage.ErrNotFound
}

func (d *nameFakeDriver) Stat(_ context.Context, p string) (storage.Object, error) {
	if d.statErr != nil {
		return storage.Object{}, d.statErr
	}
	p = strings.Trim(p, "/")
	for k, kind := range d.entries {
		if k == p || (d.insensitive && strings.EqualFold(k, p)) {
			return storage.Object{Path: p, Name: path.Base(p), Kind: kind}, nil
		}
	}
	return storage.Object{}, storage.ErrNotFound
}

func (d *nameFakeDriver) List(_ context.Context, dir string) ([]storage.Object, error) {
	dir = strings.Trim(dir, "/")
	var out []storage.Object
	for k, kind := range d.entries {
		parent := path.Dir(k)
		if parent == "." {
			parent = ""
		}
		if parent == dir {
			out = append(out, storage.Object{Path: k, Name: path.Base(k), Kind: kind})
		}
	}
	return out, nil
}

// nameFakeRows is the catalogue: live rows by path hash.
type nameFakeRows map[string]*model.Node

func (r nameFakeRows) GetNodeByPath(_ context.Context, _ int64, hash string) (*model.Node, error) {
	if n, ok := r[hash]; ok {
		return n, nil
	}
	return nil, errors.New("sql: no rows in result set")
}

func nameFakeFiles(names ...string) map[string]storage.ObjectKind {
	out := map[string]storage.ObjectKind{}
	for _, n := range names {
		out[n] = storage.KindFile
	}
	return out
}

func TestDestinationTaken(t *testing.T) {
	const sid = int64(7)
	ctx := context.Background()

	cases := []struct {
		name     string
		drv      *nameFakeDriver
		rows     nameFakeRows
		src, dst string
		want     bool
	}{
		{name: "free", drv: &nameFakeDriver{entries: nameFakeFiles("docs/a.txt")},
			src: "docs/a.txt", dst: "docs/b.txt", want: false},
		{name: "a file already has the name", drv: &nameFakeDriver{entries: nameFakeFiles("docs/a.txt", "docs/b.txt")},
			src: "docs/a.txt", dst: "docs/b.txt", want: true},
		{name: "a folder already has the name",
			drv: &nameFakeDriver{entries: map[string]storage.ObjectKind{"Belgeler": storage.KindDirectory, "Arsiv": storage.KindDirectory}},
			src: "Belgeler", dst: "Arsiv", want: true},
		// The source itself answers Stat("A.txt") on a case-insensitive disk.
		// That is the item being renamed, not a second file.
		{name: "case-only rename, case-insensitive store", drv: &nameFakeDriver{entries: nameFakeFiles("docs/a.txt"), insensitive: true},
			src: "docs/a.txt", dst: "docs/A.txt", want: false},
		{name: "case-only rename at the root", drv: &nameFakeDriver{entries: nameFakeFiles("a.txt"), insensitive: true},
			src: "a.txt", dst: "A.txt", want: false},
		// A case-sensitive store can hold both spellings: the second one is a
		// different file, and renaming onto it would replace it.
		{name: "case-only rename onto a real twin", drv: &nameFakeDriver{entries: nameFakeFiles("docs/a.txt", "docs/A.txt")},
			src: "docs/a.txt", dst: "docs/A.txt", want: true},
		// On a case-insensitive disk "B.txt" IS "b.txt", which is not the source.
		{name: "another file answers case-insensitively", drv: &nameFakeDriver{entries: nameFakeFiles("docs/x.txt", "docs/b.txt"), insensitive: true},
			src: "docs/x.txt", dst: "docs/B.txt", want: true},
		// Across two storages there is no "the source itself" to excuse.
		{name: "no source in this storage", drv: &nameFakeDriver{entries: nameFakeFiles("docs/b.txt"), insensitive: true},
			src: "", dst: "docs/B.txt", want: true},
		// The listing shows a file there even though the bytes are gone: the
		// name is taken as far as the person looking is concerned, and the row
		// still owns versions, shares and comments.
		{name: "a live row with no bytes behind it", drv: &nameFakeDriver{entries: nameFakeFiles("docs/a.txt")},
			rows: nameFakeRows{pathkey.Hash(sid, "/docs/b.txt"): {ID: 41, Path: "/docs/b.txt"}},
			src:  "docs/a.txt", dst: "docs/b.txt", want: true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rows := c.rows
			if rows == nil {
				rows = nameFakeRows{}
			}
			got, err := destinationTaken(ctx, rows, c.drv, sid, c.src, c.dst)
			require.NoError(t, err)
			assert.Equal(t, c.want, got)
		})
	}
}

// "I could not check" must never read as "free": the caller refuses instead.
func TestDestinationTaken_InconclusiveStatIsAnError(t *testing.T) {
	drv := &nameFakeDriver{entries: nameFakeFiles("a.txt"), statErr: errors.New("503 slow down")}
	_, err := destinationTaken(context.Background(), nameFakeRows{}, drv, 1, "a.txt", "b.txt")
	require.Error(t, err)
}

// The refusal reads as a 409 on every surface that maps driver errors, and
// names the file — never a server path.
func TestNameTakenError(t *testing.T) {
	err := error(&nameTakenError{name: "b.txt"})
	assert.True(t, errors.Is(err, os.ErrExist))
	assert.Equal(t, 409, mapDriverErr(err))
	assert.Equal(t, 409, aiStatus(err))
	assert.Contains(t, err.Error(), "b.txt")
}
