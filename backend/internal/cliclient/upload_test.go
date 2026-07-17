package cliclient

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ─────────────────── relRemote (pure path computation) ───────────────────

// TestRelRemote maps local walk paths onto remote targets — including the
// walk root itself and OS-native separators.
func TestRelRemote(t *testing.T) {
	dest, err := ParseRemotePath("docs://inbox/proje")
	require.NoError(t, err)
	root := filepath.Join("some", "local", "tree")

	cases := []struct {
		name    string
		local   string
		want    string
		wantErr bool
	}{
		{"walk root maps to destRoot", root, "docs://inbox/proje", false},
		{"top-level file", filepath.Join(root, "a.txt"), "docs://inbox/proje/a.txt", false},
		{"nested file", filepath.Join(root, "sub", "alt", "b.txt"), "docs://inbox/proje/sub/alt/b.txt", false},
		{"nested dir", filepath.Join(root, "boş klasör"), "docs://inbox/proje/boş klasör", false},
		{"escape above root rejected", filepath.Join("some", "local", "other.txt"), "", true},
	}
	for _, tc := range cases {
		got, err := relRemote(dest, root, tc.local)
		if tc.wantErr {
			assert.Error(t, err, tc.name)
			continue
		}
		require.NoError(t, err, tc.name)
		assert.Equal(t, tc.want, got.String(), tc.name)
	}
}

// ─────────────────── UploadTree (fake-server scenarios) ───────────────────

// makeTree lays out the standard local fixture:
//
//	<tmp>/proje/
//	  a.txt
//	  sub/b.txt
//	  empty/          (must exist remotely too)
func makeTree(t *testing.T) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), "proje")
	require.NoError(t, os.MkdirAll(filepath.Join(root, "sub"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(root, "empty"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "a.txt"), []byte("içerik A"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, "sub", "b.txt"), []byte("içerik B"), 0o644))
	return root
}

// uploadedInto returns the dest dirs the fake server received, keyed by
// uploaded filename.
func uploadedInto(fs *fakeServer) map[string]string {
	out := map[string]string{}
	for _, u := range fs.uploads {
		out[u.Name] = u.Dest
	}
	return out
}

// TestUploadTree_IntoExistingDir uploads the fixture into an existing
// remote folder: the local basename becomes a new remote subfolder, empty
// dirs included, files land in their mirrored parents.
func TestUploadTree_IntoExistingDir(t *testing.T) {
	fs, srv := newFakeServer(t)
	api := testClient(srv, "good-token")
	root := makeTree(t)

	var events []TreeEvent
	rep, err := api.UploadTree(context.Background(), root, "docs://inbox", func(ev TreeEvent) {
		events = append(events, ev)
	})
	require.NoError(t, err)

	assert.Equal(t, 2, rep.Files)
	assert.Equal(t, 3, rep.Dirs, "proje + sub + empty")
	assert.Empty(t, rep.Errors)
	assert.Empty(t, rep.Symlinks)

	assert.True(t, fs.dirs["docs://inbox/proje"], "tree root created")
	assert.True(t, fs.dirs["docs://inbox/proje/sub"], "nested dir created")
	assert.True(t, fs.dirs["docs://inbox/proje/empty"], "EMPTY dir created remotely")

	got := uploadedInto(fs)
	assert.Equal(t, "docs://inbox/proje", got["a.txt"])
	assert.Equal(t, "docs://inbox/proje/sub", got["b.txt"])

	assert.Len(t, events, 5, "3 dir + 2 file progress events")
}

// TestUploadTree_RenameForm targets a non-existing remote path without a
// trailing slash: the tree lands AT that path (rename form), mirroring
// the single-file Upload semantics.
func TestUploadTree_RenameForm(t *testing.T) {
	fs, srv := newFakeServer(t)
	api := testClient(srv, "good-token")
	root := makeTree(t)

	rep, err := api.UploadTree(context.Background(), root, "docs://inbox/yeni-ad", nil)
	require.NoError(t, err)

	assert.Equal(t, 2, rep.Files)
	assert.Equal(t, 3, rep.Dirs)
	assert.True(t, fs.dirs["docs://inbox/yeni-ad"], "renamed root created")
	assert.True(t, fs.dirs["docs://inbox/yeni-ad/sub"])

	got := uploadedInto(fs)
	assert.Equal(t, "docs://inbox/yeni-ad", got["a.txt"])
	assert.Equal(t, "docs://inbox/yeni-ad/sub", got["b.txt"])
}

// TestUploadTree_SkipsSymlinks records the link, uploads nothing for it,
// and exits clean — symlinks are a warning, not an error.
func TestUploadTree_SkipsSymlinks(t *testing.T) {
	fs, srv := newFakeServer(t)
	api := testClient(srv, "good-token")
	root := makeTree(t)
	if err := os.Symlink(filepath.Join(root, "a.txt"), filepath.Join(root, "halka.txt")); err != nil {
		t.Skipf("symlinks unavailable here (%v)", err) // Windows without dev mode
	}

	rep, err := api.UploadTree(context.Background(), root, "docs://inbox", nil)
	require.NoError(t, err)

	require.Len(t, rep.Symlinks, 1)
	assert.Equal(t, filepath.Join(root, "halka.txt"), rep.Symlinks[0])
	assert.Empty(t, rep.Errors)
	assert.Equal(t, 2, rep.Files, "link itself is not uploaded")
	_, linkUploaded := uploadedInto(fs)["halka.txt"]
	assert.False(t, linkUploaded)
}

// TestUploadTree_MkdirFailureSkipsSubtree keeps walking past a failed
// mkdir but never uploads into the missing folder; the run reports the
// error and everything else still lands.
func TestUploadTree_MkdirFailureSkipsSubtree(t *testing.T) {
	fs, srv := newFakeServer(t)
	api := testClient(srv, "good-token")
	root := makeTree(t)
	require.NoError(t, os.MkdirAll(filepath.Join(root, "yasak"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "yasak", "c.txt"), []byte("girilmez"), 0o644))

	rep, err := api.UploadTree(context.Background(), root, "docs://inbox", nil)
	require.NoError(t, err)

	require.Len(t, rep.Errors, 1)
	assert.Equal(t, filepath.Join(root, "yasak"), rep.Errors[0].Local)
	assert.Contains(t, rep.Errors[0].Err.Error(), "forbidden")

	got := uploadedInto(fs)
	_, cUploaded := got["c.txt"]
	assert.False(t, cUploaded, "file inside the failed dir must be skipped")
	assert.Equal(t, 2, rep.Files, "the rest of the tree still uploads")
	assert.Equal(t, 3, rep.Dirs)
}

// TestUploadTree_PlainFileDegrades mirrors `cp -r file dest`: -r on a
// regular file is just a single upload.
func TestUploadTree_PlainFileDegrades(t *testing.T) {
	fs, srv := newFakeServer(t)
	api := testClient(srv, "good-token")
	local := filepath.Join(t.TempDir(), "tek.txt")
	require.NoError(t, os.WriteFile(local, []byte("tek dosya"), 0o644))

	rep, err := api.UploadTree(context.Background(), local, "docs://inbox", nil)
	require.NoError(t, err)
	assert.Equal(t, 1, rep.Files)
	assert.Equal(t, 0, rep.Dirs)
	assert.Empty(t, rep.Errors)
	assert.Equal(t, "docs://inbox", uploadedInto(fs)["tek.txt"])
}

// TestUpload_DirWithoutRecursiveHint keeps the old refusal for a bare
// directory argument but now points at -r.
func TestUpload_DirWithoutRecursiveHint(t *testing.T) {
	_, srv := newFakeServer(t)
	api := testClient(srv, "good-token")

	_, _, err := api.Upload(context.Background(), t.TempDir(), "docs://inbox/")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "is a directory")
	assert.Contains(t, err.Error(), "--recursive")
}
