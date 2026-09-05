package handlers_test

// End-to-end tests for the pre-write overwrite guard, driven through real HTTP
// requests against a real router.
//
// The behaviour under test is the one the guard exists for: uploading a file
// over an existing one must leave the replaced bytes recoverable, and a
// snapshot that cannot be taken must refuse the write rather than proceed.
//
// ⚠ The case that matters most in production is
// TestOverwriteGuard_Staged_PartUploaderDriver_StillSnapshots. Every real
// deployment runs S3, and on any driver implementing storage.PartUploader the
// staged commit never calls storage.Writer.Write for the uploaded object at
// all -- it goes InitMultipart / UploadPart / CompleteMultipart. A guard hung
// off the driver write would pass every other test in this file and protect
// nobody.

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/api"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/pathkey"
	"github.com/brf-tech/filex/backend/internal/storage"
	"github.com/brf-tech/filex/backend/internal/versioning"
)

// ── helpers ─────────────────────────────────────────────────────────────────

// withVersions wires a real versioning service over the fixture's own resolver,
// which is what makes BuildRouter install the guard.
func withVersions(d *api.Deps) {
	d.Versions = versioning.New(d.Store, versioning.StorageResolver(d.StorageResolver))
}

// withBrokenVersions wires a versioning service that can never snapshot: the
// file is writable but its history is not. This is the "read-only versions
// area / storage error" case.
func withBrokenVersions(d *api.Deps) {
	d.Versions = versioning.New(d.Store, func(int64) (storage.Driver, error) {
		return nil, fmt.Errorf("versions area is unavailable")
	})
}

// uploadStaged drives the full staged protocol the web client speaks: begin,
// one chunk, commit, and wait for the ops worker to finish the transfer.
func (f *stagedFixture) uploadStaged(t *testing.T, name string, body []byte) {
	t.Helper()
	code, out := f.commitStagedRaw(t, name, body)
	require.Equal(t, http.StatusAccepted, code, "%v", out)
	require.Equal(t, "ok", f.waitForOp(t, num(out["op_id"])))
}

// commitStagedRaw is uploadStaged without the success assertions, for the tests
// that expect the commit itself to be refused.
func (f *stagedFixture) commitStagedRaw(t *testing.T, name string, body []byte) (int, map[string]any) {
	t.Helper()
	total := int64(len(body))
	code, begun := f.begin(t, map[string]any{"path": "main://", "name": name, "size": total})
	require.Equal(t, http.StatusOK, code, "%v", begun)
	id := begun["id"].(string)

	code, put := f.putChunk(t, id, 0, total, total, body)
	require.Equal(t, http.StatusOK, code, "%v", put)

	return f.commit(t, id)
}

// versionsOf reads the version timeline for the node catalogued at rel.
func versionsOf(t *testing.T, f *stagedFixture, rel string) []*model.NodeVersion {
	t.Helper()
	node, err := f.store.GetNodeByPath(context.Background(), f.storage.ID, pathkey.Hash(f.storage.ID, rel))
	require.NoError(t, err, "no catalogued node at %q", rel)
	require.NotNil(t, node)
	svc := versioning.New(f.store, versioning.StorageResolver(f.deps.StorageResolver))
	out, err := svc.List(context.Background(), node.ID)
	require.NoError(t, err)
	return out
}

// readSnapshot returns the bytes a recorded version is holding.
func readSnapshot(t *testing.T, f *stagedFixture, v *model.NodeVersion) string {
	t.Helper()
	drv, err := f.deps.StorageResolver(f.storage.ID)
	require.NoError(t, err)
	rc, err := drv.Read(context.Background(), v.StorageKey)
	require.NoError(t, err)
	defer rc.Close()
	b, err := io.ReadAll(rc)
	require.NoError(t, err)
	return string(b)
}

// ── the headline contrast ───────────────────────────────────────────────────

// Upload a file, upload a different file over the same name, and the FIRST
// version is still recoverable. Against the pre-guard code this fails with zero
// recorded versions -- that contrast is the whole point of the change.
func TestOverwriteGuard_StagedCommit_OverExistingFile_KeepsAVersion(t *testing.T) {
	f := newStagedFixtureWith(t, withVersions)

	f.uploadStaged(t, "report.txt", []byte("VERSION-ONE"))
	f.uploadStaged(t, "report.txt", []byte("VERSION-TWO-REPLACES-IT"))

	live, err := os.ReadFile(filepath.Join(f.rootDir, "report.txt"))
	require.NoError(t, err)
	assert.Equal(t, "VERSION-TWO-REPLACES-IT", string(live), "the live file is the new one")

	versions := versionsOf(t, f, "report.txt")
	require.Len(t, versions, 1, "the replaced bytes must be recoverable")
	assert.Equal(t, "VERSION-ONE", readSnapshot(t, f, versions[0]))
}

// The classic single-POST browser upload, the other surface a user reaches.
func TestOverwriteGuard_ManagerUpload_OverExistingFile_KeepsAVersion(t *testing.T) {
	f := newStagedFixtureWith(t, withVersions)

	f.uploadMultipart(t, "notes.txt", []byte("FIRST"))
	f.uploadMultipart(t, "notes.txt", []byte("SECOND"))

	versions := versionsOf(t, f, "notes.txt")
	require.Len(t, versions, 1)
	assert.Equal(t, "FIRST", readSnapshot(t, f, versions[0]))
}

// Editing in the browser keeps history too. It always did -- save-text was the
// one caller that ever snapshotted -- so this pins the behaviour rather than
// introducing it, and sits next to the upload cases as the contrast that made
// the original bug so easy to miss.
func TestOverwriteGuard_SaveText_OverExistingFile_KeepsAVersion(t *testing.T) {
	f := newStagedFixtureWith(t, withVersions)

	f.uploadStaged(t, "edit.txt", []byte("BEFORE-EDIT"))
	require.Equal(t, http.StatusOK, f.saveText(t, "main://edit.txt", "AFTER-EDIT"))

	versions := versionsOf(t, f, "edit.txt")
	require.Len(t, versions, 1)
	assert.Equal(t, "BEFORE-EDIT", readSnapshot(t, f, versions[0]))
}

// ⚠⚠ The production case: an S3-shaped backend, where the object is assembled
// by CompleteMultipart and storage.Writer.Write is never called for it.
func TestOverwriteGuard_Staged_PartUploaderDriver_StillSnapshots(t *testing.T) {
	drv := newPartStore()
	f := newStagedFixtureWith(t, func(d *api.Deps) {
		d.StorageResolver = func(int64) (storage.Driver, error) { return drv, nil }
		withVersions(d)
	})

	f.uploadStaged(t, "big.bin", []byte("MULTIPART-ORIGINAL"))
	f.uploadStaged(t, "big.bin", []byte("MULTIPART-REPLACEMENT"))

	// The object really did arrive over multipart. Without this assertion the
	// test could silently start measuring the plain-writer path and still pass.
	assert.Equal(t, viaMultipart, drv.mechanismFor("big.bin"),
		"precondition: a PartUploader driver must assemble the object via CompleteMultipart, not Writer.Write")
	assert.Equal(t, "MULTIPART-REPLACEMENT", drv.contentOf("big.bin"))

	versions := versionsOf(t, f, "big.bin")
	require.Len(t, versions, 1,
		"a PartUploader overwrite must be snapshotted too -- this is exactly the case a Writer.Write-level guard would miss")
	assert.Equal(t, "MULTIPART-ORIGINAL", readSnapshot(t, f, versions[0]))
}

// ── refusal ─────────────────────────────────────────────────────────────────

// A snapshot that cannot be taken must refuse the write, and the LIVE bytes
// must survive untouched. Anything less turns a broken snapshot backend into
// silent data loss.
func TestOverwriteGuard_ManagerUpload_SnapshotFailure_Refuses503(t *testing.T) {
	f := newStagedFixtureWith(t, withBrokenVersions)

	// The first upload has nothing to snapshot, so it lands normally.
	f.uploadMultipart(t, "precious.txt", []byte("MUST-SURVIVE"))

	code, body := f.uploadMultipartCode(t, "precious.txt", []byte("MUST-NOT-LAND"))
	assert.Equal(t, http.StatusServiceUnavailable, code, body)
	assert.Contains(t, body, "SNAPSHOT_FAILED")

	live, err := os.ReadFile(filepath.Join(f.rootDir, "precious.txt"))
	require.NoError(t, err)
	assert.Equal(t, "MUST-SURVIVE", string(live), "a refused overwrite must not have touched the file")
}

// The same refusal on the staged path, which answers at commit -- before the
// node is published and before the ops worker moves a byte.
func TestOverwriteGuard_StagedCommit_SnapshotFailure_Refuses503(t *testing.T) {
	f := newStagedFixtureWith(t, withBrokenVersions)

	f.uploadStaged(t, "doc.txt", []byte("ORIGINAL-STAYS"))

	code, body := f.commitStagedRaw(t, "doc.txt", []byte("REFUSED"))
	assert.Equal(t, http.StatusServiceUnavailable, code, "%v", body)
	assert.Equal(t, "SNAPSHOT_FAILED", body["code"])

	live, err := os.ReadFile(filepath.Join(f.rootDir, "doc.txt"))
	require.NoError(t, err)
	assert.Equal(t, "ORIGINAL-STAYS", string(live))
}

// Editing in the browser is refused on the same terms. This used to log
// "snapshot failed (continuing with write)" and overwrite anyway, contradicting
// Snapshot's own contract -- it was the last fail-open write surface.
func TestOverwriteGuard_SaveText_SnapshotFailure_Refuses503(t *testing.T) {
	f := newStagedFixtureWith(t, withBrokenVersions)

	f.uploadStaged(t, "edit.txt", []byte("KEEP-ME"))
	assert.Equal(t, http.StatusServiceUnavailable, f.saveText(t, "main://edit.txt", "SHOULD-NOT-LAND"))

	live, err := os.ReadFile(filepath.Join(f.rootDir, "edit.txt"))
	require.NoError(t, err)
	assert.Equal(t, "KEEP-ME", string(live))
}

// ── the off switch ──────────────────────────────────────────────────────────

// An unversioned installation writes normally. This is the escape hatch an
// operator reaches for when the object store cannot afford the extra write, so
// it has to be a true no-op rather than a slower path.
func TestOverwriteGuard_Disabled_WritesNormallyAndKeepsNoVersions(t *testing.T) {
	f := newStagedFixtureWith(t, func(d *api.Deps) {
		withVersions(d)
		d.Cfg.VersionsOnOverwrite = false
	})

	f.uploadStaged(t, "plain.txt", []byte("ONE"))
	f.uploadStaged(t, "plain.txt", []byte("TWO"))

	live, err := os.ReadFile(filepath.Join(f.rootDir, "plain.txt"))
	require.NoError(t, err)
	assert.Equal(t, "TWO", string(live), "the write must still go through")
	assert.Empty(t, versionsOf(t, f, "plain.txt"), "no guard means no snapshots")
}

// Disabled AND a broken snapshot backend: still writes. This is the state an
// operator flips to during an incident, so it must not depend on versioning
// working at all.
func TestOverwriteGuard_Disabled_WritesEvenWhenSnapshotsWouldFail(t *testing.T) {
	f := newStagedFixtureWith(t, func(d *api.Deps) {
		withBrokenVersions(d)
		d.Cfg.VersionsOnOverwrite = false
	})

	f.uploadMultipart(t, "incident.txt", []byte("OLD"))
	f.uploadMultipart(t, "incident.txt", []byte("NEW"))

	live, err := os.ReadFile(filepath.Join(f.rootDir, "incident.txt"))
	require.NoError(t, err)
	assert.Equal(t, "NEW", string(live))
}

// With no versioning service at all -- the shape every other fixture in this
// package runs with -- writes behave exactly as they did before.
func TestOverwriteGuard_NoVersioningService_WritesNormally(t *testing.T) {
	f := newStagedFixtureWith(t, nil)

	f.uploadStaged(t, "vanilla.txt", []byte("A"))
	f.uploadStaged(t, "vanilla.txt", []byte("B"))

	live, err := os.ReadFile(filepath.Join(f.rootDir, "vanilla.txt"))
	require.NoError(t, err)
	assert.Equal(t, "B", string(live))
}

// Fail-open is the deliberate opt-in for an operator riding out a full object
// store: the snapshot still fails, but the write is allowed through.
func TestOverwriteGuard_FailOpen_AllowsTheWriteThrough(t *testing.T) {
	f := newStagedFixtureWith(t, func(d *api.Deps) {
		withBrokenVersions(d)
		d.Cfg.VersionsFailOpen = true
	})

	f.uploadMultipart(t, "ride-it-out.txt", []byte("OLD"))
	f.uploadMultipart(t, "ride-it-out.txt", []byte("NEW"))

	live, err := os.ReadFile(filepath.Join(f.rootDir, "ride-it-out.txt"))
	require.NoError(t, err)
	assert.Equal(t, "NEW", string(live), "fail-open means the overwrite happens anyway")
}

// A first write must not cost a version row -- otherwise every new file on the
// instance carries a pointless copy of itself.
func TestOverwriteGuard_FirstUpload_TakesNoSnapshot(t *testing.T) {
	f := newStagedFixtureWith(t, withVersions)

	f.uploadStaged(t, "fresh.txt", []byte("ONLY"))
	assert.Empty(t, versionsOf(t, f, "fresh.txt"))
}

// ── an S3-shaped driver ─────────────────────────────────────────────────────

type writeMechanism string

const (
	viaWrite     writeMechanism = "Writer.Write"
	viaMultipart writeMechanism = "CompleteMultipart"
)

// partStore is a keyed, in-memory backend that implements storage.PartUploader
// as well as storage.Writer, and REMEMBERS which mechanism delivered each key.
//
// It is keyed rather than single-object (unlike writerDriver in
// upload_staged_guard_test.go) because this test needs the live object and its
// .versions/ snapshot to coexist: a snapshot is a second key on the same
// driver, and the versioning service reaches it through this same resolver.
//
// It deliberately has no Copy: that forces versioning.copyOrStream down its
// Read+Write fallback, which is the harder path and the one that would show up
// as a stray Writer.Write if the mechanism bookkeeping were sloppy. Snapshot
// writes are therefore recorded under their own .versions/ key, leaving the
// live key's mechanism untouched.
//
// ⚠⚠ Every key goes through storeKey, because every real driver normalises the
// paths it is handed -- the S3 driver does exactly this
// (storage/drivers/s3/s3.go, `strings.TrimLeft(path.Clean("/"+p), "/")`) and so
// does local. A first draft of this fake looked the key up raw, and the staged
// path stores a node whose StorageKey carries a leading slash while
// CompleteMultipart receives the key without one -- so Stat missed, Snapshot
// took its "nothing here to snapshot" branch and the guard passed silently with
// zero versions. That was the fake being unrealistic, not the guard being
// wrong, but it is worth knowing that Snapshot's ErrNotFound branch is a SILENT
// skip: a driver that failed to normalise would lose files with no error
// anywhere.
type partStore struct {
	mu        sync.Mutex
	objects   map[string][]byte
	mechanism map[string]writeMechanism
	parts     map[string]map[int][]byte
	nextID    int
}

func newPartStore() *partStore {
	return &partStore{
		objects:   map[string][]byte{},
		mechanism: map[string]writeMechanism{},
		parts:     map[string]map[int][]byte{},
	}
}

func (d *partStore) mechanismFor(key string) writeMechanism {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.mechanism[storeKey(key)]
}

func (d *partStore) contentOf(key string) string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return string(d.objects[storeKey(key)])
}

func (d *partStore) Init(context.Context, map[string]any) error { return nil }
func (d *partStore) Name() string                               { return "partstore" }

// storeKey normalises the way a real driver does: leading slash optional,
// cleaned, no trailing slash.
func storeKey(p string) string { return strings.TrimLeft(path.Clean("/"+p), "/") }

func (d *partStore) List(context.Context, string) ([]storage.Object, error) { return nil, nil }

func (d *partStore) Capabilities() storage.Capabilities {
	return storage.Capabilities{Write: true}
}

func (d *partStore) Stat(_ context.Context, p string) (storage.Object, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	b, ok := d.objects[storeKey(p)]
	if !ok {
		return storage.Object{}, storage.ErrNotFound
	}
	return storage.Object{Path: p, Name: p, Size: int64(len(b)), Kind: storage.KindFile, Mtime: time.Now()}, nil
}

func (d *partStore) Read(_ context.Context, p string) (io.ReadCloser, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	b, ok := d.objects[storeKey(p)]
	if !ok {
		return nil, storage.ErrNotFound
	}
	return io.NopCloser(bytes.NewReader(append([]byte(nil), b...))), nil
}

func (d *partStore) Write(_ context.Context, p string, r io.Reader, _ int64) error {
	b, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	d.objects[storeKey(p)] = b
	d.mechanism[storeKey(p)] = viaWrite
	return nil
}

func (d *partStore) InitMultipart(_ context.Context, key string, _ int64, _ int) (string, []string, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.nextID++
	id := fmt.Sprintf("upload-%d-%s", d.nextID, key)
	d.parts[id] = map[int][]byte{}
	return id, nil, nil
}

func (d *partStore) UploadPart(_ context.Context, _, uploadID string, n int, r io.Reader, size int64) (string, error) {
	b, err := io.ReadAll(r)
	if err != nil {
		return "", err
	}
	if int64(len(b)) != size {
		return "", fmt.Errorf("part %d: declared %d bytes, got %d", n, size, len(b))
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	set, ok := d.parts[uploadID]
	if !ok {
		return "", fmt.Errorf("unknown upload id %q", uploadID)
	}
	set[n] = b
	return fmt.Sprintf("etag-%d", n), nil
}

func (d *partStore) CompleteMultipart(_ context.Context, key, uploadID string, parts []storage.PartCompletion) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	set, ok := d.parts[uploadID]
	if !ok {
		return fmt.Errorf("unknown upload id %q", uploadID)
	}
	nums := make([]int, 0, len(parts))
	for _, p := range parts {
		nums = append(nums, p.PartNumber)
	}
	sort.Ints(nums)
	var out []byte
	for _, n := range nums {
		b, ok := set[n]
		if !ok {
			return fmt.Errorf("part %d never uploaded", n)
		}
		out = append(out, b...)
	}
	d.objects[storeKey(key)] = out
	d.mechanism[storeKey(key)] = viaMultipart
	delete(d.parts, uploadID)
	return nil
}

func (d *partStore) AbortMultipart(_ context.Context, _, uploadID string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	delete(d.parts, uploadID)
	return nil
}
