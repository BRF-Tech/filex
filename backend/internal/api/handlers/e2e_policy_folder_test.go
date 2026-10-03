package handlers_test

// A FOLDER named like an encryption is not the file (e2e_policy_gate.go,
// checkE2EWrite).
//
// Writing a key file (`.filex-e2e.json`) or a `.fxe` where there was none is a
// new encryption, and the rule is asked. A door that decided "does this write
// create?" by whether ITS target was there — a Stat that answered, a catalogue
// row, an archive it could read — read a folder with the name as something to
// replace, and never asked. On an object store a folder is a prefix and the
// file is written beside it; production runs on one (DT S3), so a folder named
// `x.fxe` followed by a write of `x.fxe` put a `.fxe` there with the policy
// off. Every HTTP door that decided that way now asks the rule's own question —
// no FILE at the path is a create — and keeps its own permission, lock and
// blocked-extension checks.
//
// On local storage the folder is a directory and a write onto it fails at the
// driver anyway, so each test wants the RULE's refusal, not a later error: 403
// e2e_not_allowed for policy_off, and nothing lands as a file. Each also
// rewrites a FILE that is there, which stays free with the policy off: a door
// that stopped showing the rule what is at its target would ask every time.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/api"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/storage"
	"github.com/brf-tech/filex/backend/internal/storage/drivers/local"
	"github.com/brf-tech/filex/backend/internal/testutil"
)

// The agent API's write and zip decided by writeNeed (a Stat that answered),
// and save-text by a catalogue row. With a folder at the target the question
// was skipped; the write and the zip then met their kind guard — a 409, not
// the rule — and save-text wrote the key file.
func TestE2EPolicy_AFolderWithTheNameIsNotAFileToReplace(t *testing.T) {
	f := newMTFix(t, false)
	ctx := context.Background()
	admin, err := f.Store.GetUserByEmail(ctx, "admin@alpha.test")
	require.NoError(t, err)
	tok := issueToken(t, f.Store, admin.ID, fullScopes, nil)
	// While encryption is still allowed: folders named like the two, where the
	// doors below try to write the files (a folder is not an encryption, and
	// making one is free); files named like the two, encrypted before the
	// policy was switched off; and a plain file to pack.
	//
	// The folder named like a key file sits in a folder of its own, Anahtar:
	// the catalogue reads a folder with that name as a key file, and the agent
	// API (0.50) refuses plain bytes into what reads as an encrypted folder
	// (E2E_PLAINTEXT_REFUSED). The rewrites in Acik must not stand in one.
	for _, dir := range []struct{ parent, name string }{
		{"alpha://", "Acik"}, {"alpha://Acik", "ajan.fxe"}, {"alpha://Acik", "arsiv.fxe"},
		{"alpha://", "Anahtar"}, {"alpha://Anahtar", e2eKeyFile},
		{"alpha://", "Bayat"}, {"alpha://Bayat", e2eKeyFile}, {"alpha://", "Kasa"},
	} {
		status, body := fxMutate(t, f.URL, tok, "newfolder", map[string]any{"path": dir.parent, "name": dir.name})
		require.Equal(t, http.StatusOK, status, body)
	}
	fxUpload(t, f.URL, tok, "alpha://Kasa", e2eKeyFile, kfOne)
	fxUpload(t, f.URL, tok, "alpha://Acik", "eski.fxe", fxeBody)
	fxUpload(t, f.URL, tok, "alpha://Acik", "eski-arsiv.fxe", fxeBody)
	fxUpload(t, f.URL, tok, "alpha://Acik", "duz.txt", "plain text")
	encryptionOff(t, f.Store)

	// aiOps.WriteStream, the funnel of /api/ai/upload, MCP file_write,
	// /api/sharex/upload and /u/{ticket}. Its kind guard comes after the
	// question, so a folder taken away in between let the file land.
	// A key file is not the rule's question there at all: the agent API never
	// writes one (assertKeylessRefused).
	t.Run("agent upload", func(t *testing.T) {
		status, body := fxPost(t, f.URL+"/api/ai/upload", tok, map[string]any{"path": "alpha://Anahtar/" + e2eKeyFile, "content": "x"})
		assertKeylessRefused(t, "an agent's upload onto the folder Anahtar/"+e2eKeyFile, status, body)
		assert.DirExists(t, filepath.Join(f.RootA, "Anahtar", e2eKeyFile), "Anahtar/%s is not the folder any more", e2eKeyFile)
		status, body = fxPost(t, f.URL+"/api/ai/upload", tok, map[string]any{"path": "alpha://Acik/ajan.fxe", "content": "x"})
		assertE2ERefused(t, "an agent's upload onto the folder Acik/ajan.fxe", status, body)
		assert.DirExists(t, filepath.Join(f.RootA, "Acik", "ajan.fxe"), "Acik/ajan.fxe is not the folder any more")
		status, body = fxPost(t, f.URL+"/api/ai/upload", tok, map[string]any{"path": "alpha://Acik/eski.fxe", "content": fxeBody + " v2"})
		require.Equal(t, http.StatusOK, status, "replacing a .fxe that is there: %s", body)
	})
	// aiOps.Zip: the same writeNeed, and a whole archive built before the kind
	// guard looked again — time enough to take the folder away.
	t.Run("agent zip", func(t *testing.T) {
		status, body := fxPost(t, f.URL+"/api/ai/zip", tok, map[string]any{"sources": []string{"alpha://Acik/duz.txt"}, "dest": "alpha://Anahtar/" + e2eKeyFile})
		assertKeylessRefused(t, "an agent's zip onto the folder Anahtar/"+e2eKeyFile, status, body)
		assert.DirExists(t, filepath.Join(f.RootA, "Anahtar", e2eKeyFile), "Anahtar/%s is not the folder any more", e2eKeyFile)
		status, body = fxPost(t, f.URL+"/api/ai/zip", tok, map[string]any{"sources": []string{"alpha://Acik/duz.txt"}, "dest": "alpha://Acik/arsiv.fxe"})
		assertE2ERefused(t, "an agent's zip onto the folder Acik/arsiv.fxe", status, body)
		assert.DirExists(t, filepath.Join(f.RootA, "Acik", "arsiv.fxe"), "Acik/arsiv.fxe is not the folder any more")
		status, body = fxPost(t, f.URL+"/api/ai/zip", tok, map[string]any{"sources": []string{"alpha://Acik/duz.txt"}, "dest": "alpha://Acik/eski-arsiv.fxe"})
		require.Equal(t, http.StatusOK, status, "a zip over a .fxe that is there: %s", body)
	})
	// save-text read the target's catalogue row as the file. A folder's row
	// can outlive the folder: on an object store a folder made by uploading a
	// file into it is only that file's prefix, and once the file goes to the
	// trash the prefix is gone while the row stays. Taking the directory away
	// under its row is that, on local storage.
	t.Run("save-text", func(t *testing.T) {
		require.NoError(t, os.Remove(filepath.Join(f.RootA, "Bayat", e2eKeyFile)))
		status, body := fxPost(t, f.URL+"/api/files/save-text", tok, map[string]any{"path": "alpha://Bayat/" + e2eKeyFile, "content": kfOne})
		assertE2ERefused(t, "the text editor saving a key file under a folder's row", status, body)
		assert.NoFileExists(t, filepath.Join(f.RootA, "Bayat", e2eKeyFile))

		status, body = fxPost(t, f.URL+"/api/files/save-text", tok, map[string]any{"path": "alpha://Kasa/" + e2eKeyFile, "content": kfNewPw})
		require.Equal(t, http.StatusOK, status, "rewriting a key file that is there: %s", body)
		got, err := os.ReadFile(filepath.Join(f.RootA, "Kasa", e2eKeyFile))
		require.NoError(t, err)
		assert.Equal(t, kfNewPw, string(got))
	})
}

// localParts is local storage that also hands out part URLs — the presigned
// upload (upload.go Init) is open only on a driver that does — and counts the
// multipart uploads it was asked to start.
type localParts struct {
	*local.Driver
	started atomic.Int32
}

func (d *localParts) InitMultipart(context.Context, string, int64, int) (string, []string, error) {
	return fmt.Sprintf("upload-%d", d.started.Add(1)), nil, nil
}

func (d *localParts) CompleteMultipart(context.Context, string, string, []storage.PartCompletion) error {
	return nil
}

func (d *localParts) AbortMultipart(context.Context, string, string) error { return nil }

// The presigned upload decided "create" by a Stat that failed, and a folder
// answered it: the Init read as an overwrite and the rule was never asked.
// Nothing looks again — no kind guard at Init, none at Finalize — so on an
// object store the parts went up and were assembled beside the prefix.
func TestE2EPolicy_PresignedInitOntoAFolderWithTheName(t *testing.T) {
	root := t.TempDir()
	lp := &localParts{Driver: &local.Driver{}}
	require.NoError(t, lp.Init(context.Background(), map[string]any{"root": root}))
	srv, _, store := testutil.NewTestServerWith(t, nil, func(d *api.Deps) {
		d.StorageResolver = func(int64) (storage.Driver, error) { return lp, nil }
	})
	ctx := context.Background()
	st, err := store.CreateStorage(ctx, &model.Storage{Name: "yerel", Driver: "local", MountPath: "/yerel", Enabled: true, ConfigJSON: json.RawMessage(`{}`)})
	require.NoError(t, err)
	adminID, _ := testutil.SeedAdminUser(t, store)
	tok := issueToken(t, store, adminID, fullScopes, nil)
	targets := []string{"Kasa/" + e2eKeyFile, "rapor.pdf.fxe"}
	for _, rel := range targets {
		require.NoError(t, os.MkdirAll(filepath.Join(root, filepath.FromSlash(rel)), 0o755))
	}
	require.NoError(t, os.WriteFile(filepath.Join(root, "eski.fxe"), []byte(fxeBody), 0o644))
	encryptionOff(t, store)
	initUpload := func(rel string) (int, string) {
		return fxPost(t, srv.URL+"/api/files/upload/init", tok, map[string]any{"storage_id": st.ID, "path": rel, "size": 6 << 20})
	}

	for _, rel := range targets {
		status, body := initUpload(rel)
		assertE2ERefused(t, "a presigned upload onto the folder "+rel, status, body)
		assert.DirExists(t, filepath.Join(root, filepath.FromSlash(rel)), "%s is not the folder any more", rel)
	}
	assert.Zero(t, lp.started.Load(), "the driver was asked to start a multipart upload")

	status, body := initUpload("eski.fxe")
	require.Equal(t, http.StatusOK, status, "replacing a .fxe that is there: %s", body)
	status, body = initUpload("yedek.bin")
	require.Equal(t, http.StatusOK, status, "an ordinary file: %s", body)
}

// stagedFixtureOver is newStagedFixture over the driver wrap makes of its
// local storage: one wrapped driver for the fixture's life, so the wrapper's
// own state is the fixture's.
func stagedFixtureOver(t *testing.T, wrap func(*local.Driver) storage.Driver) *stagedFixture {
	t.Helper()
	return newStagedFixtureWith(t, func(d *api.Deps) {
		inner := d.StorageResolver
		var (
			once    sync.Once
			wrapped storage.Driver
		)
		d.StorageResolver = func(id int64) (storage.Driver, error) {
			drv, err := inner(id)
			if err != nil {
				return nil, err
			}
			once.Do(func() { wrapped = wrap(drv.(*local.Driver)) })
			return wrapped, nil
		}
	})
}

// aFolderAppears is local storage on which a folder named like a pending
// target appears the moment a look at that target has found nothing — what a
// MKCOL sent alongside the write does to a door that looks twice: its kind
// guard (storage.EnsureFileTarget) sees the name free, its own "is it there"
// sees a folder. The folder is made through the driver, as the MKCOL makes it.
type aFolderAppears struct {
	*local.Driver
	mu      sync.Mutex
	pending map[string]bool
}

func folderAppearsAt(d *local.Driver, targets ...string) *aFolderAppears {
	pending := map[string]bool{}
	for _, t := range targets {
		pending[t] = true
	}
	return &aFolderAppears{Driver: d, pending: pending}
}

func (d *aFolderAppears) Stat(ctx context.Context, p string) (storage.Object, error) {
	obj, err := d.Driver.Stat(ctx, p)
	if errors.Is(err, storage.ErrNotFound) {
		rel := strings.Trim(p, "/")
		d.mu.Lock()
		appears := d.pending[rel]
		delete(d.pending, rel)
		d.mu.Unlock()
		if appears {
			_ = d.Driver.Mkdir(ctx, rel)
		}
	}
	return obj, err
}

// The explorer's upload and the resumable upload's begin look at their
// target twice: the kind guard, then their own "is it there" for files.create
// or files.modify. The question went by the second look, so a folder that
// appeared between the two made the upload an overwrite nobody asked about. On
// an object store the explorer's file then lands beside the folder; the
// resumable upload needs only the folder gone again before its commit.
func TestE2EPolicy_AFolderThatAppearsBetweenTwoLooksIsNotTheFile(t *testing.T) {
	names := []string{e2eKeyFile, "yeni.fxe"}
	t.Run("explorer upload", func(t *testing.T) {
		f := stagedFixtureOver(t, func(d *local.Driver) storage.Driver { return folderAppearsAt(d, names...) })
		require.NoError(t, os.WriteFile(filepath.Join(f.rootDir, "eski.fxe"), []byte(fxeBody), 0o644))
		encryptionOff(t, f.store)
		tok := issueToken(t, f.store, f.userID, fullScopes, nil)
		for _, name := range names {
			status, body := fxUploadStatus(t, f.srv.URL, tok, "main://", name, fxeBody)
			assertE2ERefused(t, "an upload of "+name+" as a folder of its name appeared", status, body)
			assert.DirExists(t, filepath.Join(f.rootDir, name), "no folder %s appeared: the race was not run", name)
		}
		status, body := fxUploadStatus(t, f.srv.URL, tok, "main://", "eski.fxe", fxeBody+" v2")
		require.Equal(t, http.StatusOK, status, "replacing a .fxe that is there: %s", body)
	})
	t.Run("staged begin", func(t *testing.T) {
		f := stagedFixtureOver(t, func(d *local.Driver) storage.Driver { return folderAppearsAt(d, names...) })
		require.NoError(t, os.WriteFile(filepath.Join(f.rootDir, "eski.fxe"), []byte(fxeBody), 0o644))
		encryptionOff(t, f.store)
		for _, name := range names {
			code, out := f.begin(t, map[string]any{"path": "main://", "name": name, "size": 7})
			body, _ := json.Marshal(out)
			assertE2ERefused(t, "a resumable upload of "+name+" as a folder of its name appeared", code, string(body))
			assert.DirExists(t, filepath.Join(f.rootDir, name), "no folder %s appeared: the race was not run", name)
		}
		staged, _ := os.ReadDir(filepath.Join(f.dataDir, "uploads"))
		assert.Empty(t, staged, "a refused begin reserved a staging directory")
		code, out := f.begin(t, map[string]any{"path": "main://", "name": "eski.fxe", "size": 7})
		require.Equal(t, http.StatusOK, code, "replacing a .fxe that is there: %v", out)
	})
}

// aFolderReads is local storage whose folders read like files — as they do on
// a WebDAV storage whose server answers a GET on a collection with a listing.
type aFolderReads struct{ *local.Driver }

func (d aFolderReads) Read(ctx context.Context, p string) (io.ReadCloser, error) {
	if obj, err := d.Driver.Stat(ctx, p); err == nil && obj.Kind == storage.KindDirectory {
		return io.NopCloser(strings.NewReader("<html><body><h1>Index of /" + p + "</h1></body></html>")), nil
	}
	return d.Driver.Read(ctx, p)
}

// archive/add decided "create" by whether an archive at the path could be
// read. Where a folder reads, the folder was an archive to add to and the
// question was skipped; the kind guard looked only after the whole archive
// was built.
func TestE2EPolicy_ArchiveAddOntoAFolderThatReadsLikeAFile(t *testing.T) {
	f := stagedFixtureOver(t, func(d *local.Driver) storage.Driver { return aFolderReads{d} })
	names := []string{e2eKeyFile, "ek.fxe"}
	for _, name := range names {
		require.NoError(t, os.Mkdir(filepath.Join(f.rootDir, name), 0o755))
	}
	require.NoError(t, os.WriteFile(filepath.Join(f.rootDir, "duz.txt"), []byte("plain text"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(f.rootDir, "eski.fxe"), buildZip(t, map[string]string{"ilk.txt": "first"}), 0o644))
	encryptionOff(t, f.store)
	tok := issueToken(t, f.store, f.userID, fullScopes, nil)
	add := func(rel string) (int, string) {
		return fxPost(t, f.srv.URL+"/api/files/archive/add", tok, map[string]any{
			"storage_id": f.storage.ID, "path": rel,
			"files": []map[string]string{{"name": "duz.txt", "source": "duz.txt"}},
		})
	}

	for _, name := range names {
		status, body := add(name)
		assertE2ERefused(t, "an archive added to the folder "+name, status, body)
		assert.DirExists(t, filepath.Join(f.rootDir, name), "%s is not the folder any more", name)
	}
	status, body := add("eski.fxe")
	require.Equal(t, http.StatusOK, status, "adding to an archive named .fxe that is there: %s", body)
}
