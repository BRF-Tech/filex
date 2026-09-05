package handlers_test

// Read-during-transfer: a file must be openable while its bytes are still on
// their way to the storage backend.
//
// A staged upload publishes its node the moment the client commits and hands
// the actual transfer to a background op. For as long as that op runs the node
// is listed, has a size, can be shared — and the driver does not have the
// object. Every read surface used to ask the driver, so every one of them
// answered 404 for a file the user could see. These tests drive the REAL
// surfaces (the app's download/preview, a Range request, a public share link,
// the folder-share browse page, the thumbnail pipeline and WebDAV GET) during
// that window and assert the BYTES, not the wiring.
//
// This file deliberately uses only symbols that predate the change (api.Deps,
// api.BuildRouter, the local driver, ops, the share service, thumb.Pipeline),
// so it compiles against the pre-change tree and can be run there as red
// evidence. Assertions that need the new package live in
// read_during_transfer_wiring_test.go.

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/api"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/pathkey"
	"github.com/brf-tech/filex/backend/internal/share"
	"github.com/brf-tech/filex/backend/internal/storage"
	"github.com/brf-tech/filex/backend/internal/thumb"
)

// ── the gate ────────────────────────────────────────────────────────────────

// gateDriver is a storage driver whose Write can be held open, which is the
// only honest way to test "while the transfer is still running": the node is
// staged, the op is in flight, and the backend genuinely does not have the
// object yet. Everything else is forwarded to the real local driver, including
// ReadRange — a fake that could not range would hide the case this chunk cares
// about most.
type gateDriver struct {
	storage.Driver

	mu     sync.Mutex
	phase  *gatePhase
	failed error // when set, the transfer fails instead of writing
	writes int
}

// gatePhase is one parked transfer. A fresh one is armed per transfer so a test
// can hold a SECOND transfer open (an overwrite) after the first has landed.
type gatePhase struct {
	once    sync.Once
	relOnce sync.Once
	started chan struct{}
	release chan struct{}
}

func newGatePhase() *gatePhase {
	return &gatePhase{started: make(chan struct{}), release: make(chan struct{})}
}

func newGateDriver(inner storage.Driver) *gateDriver {
	return &gateDriver{Driver: inner, phase: newGatePhase()}
}

func (g *gateDriver) current() *gatePhase {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.phase
}

// rearm parks the NEXT transfer. Call it once the previous one has finished.
func (g *gateDriver) rearm() {
	g.mu.Lock()
	g.phase = newGatePhase()
	g.writes = 0
	g.mu.Unlock()
}

func (g *gateDriver) Write(ctx context.Context, p string, r io.Reader, size int64) error {
	ph := g.current()
	ph.once.Do(func() { close(ph.started) })
	<-ph.release
	g.mu.Lock()
	failed := g.failed
	g.writes++
	g.mu.Unlock()
	if failed != nil {
		return failed
	}
	w, ok := g.Driver.(storage.Writer)
	if !ok {
		return storage.ErrUnsupported
	}
	return w.Write(ctx, p, r, size)
}

// ReadRange is forwarded explicitly: embedding storage.Driver (the interface)
// would otherwise hide the local driver's ranged reads, and the "after the
// transfer, ranges come from the driver" assertion would silently test the
// unranged fallback instead.
func (g *gateDriver) ReadRange(ctx context.Context, p string, off, length int64) (io.ReadCloser, error) {
	rr, ok := g.Driver.(storage.RangeReader)
	if !ok {
		return nil, storage.ErrUnsupported
	}
	return rr.ReadRange(ctx, p, off, length)
}

// failWith makes the next transfer fail. The staging directory is kept on a
// failed transfer and the node stays "staged" — which is exactly why reads
// must keep working afterwards.
func (g *gateDriver) failWith(err error) {
	g.mu.Lock()
	g.failed = err
	g.mu.Unlock()
}

func (g *gateDriver) writeCount() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.writes
}

// waitStarted blocks until the transfer has actually entered the driver, so a
// test never asserts on the staged window before it has opened.
func (g *gateDriver) waitStarted(t *testing.T) {
	t.Helper()
	ph := g.current()
	select {
	case <-ph.started:
	case <-time.After(15 * time.Second):
		t.Fatal("the transfer never reached the driver")
	}
}

// let opens the gate. Idempotent, and registered as a cleanup by the fixture:
// a test that fails BEFORE releasing would otherwise leave the ops worker
// parked inside Write, and the shutdown that waits for it would hang the whole
// package instead of reporting the failure.
func (g *gateDriver) let() {
	ph := g.current()
	ph.relOnce.Do(func() { close(ph.release) })
}

// ── fixture ─────────────────────────────────────────────────────────────────

type transferFixture struct {
	*stagedFixture
	gate  *gateDriver
	thumb *thumb.Pipeline
}

// newTransferFixture is the staged-upload fixture with the driver behind a
// gate, a thumbnail pipeline and WebDAV enabled.
func newTransferFixture(t *testing.T) *transferFixture {
	t.Helper()
	var gate *gateDriver
	var once sync.Once
	thumbDir := t.TempDir()
	var pipeline *thumb.Pipeline

	f := newStagedFixtureWith(t, func(d *api.Deps) {
		inner := d.StorageResolver
		d.StorageResolver = func(id int64) (storage.Driver, error) {
			drv, err := inner(id)
			if err != nil {
				return nil, err
			}
			once.Do(func() { gate = newGateDriver(drv) })
			return gate, nil
		}
		pipeline = thumb.New(d.Store, thumbDir, thumb.Capabilities{Image: true})
		d.Thumbs = pipeline
		d.Cfg.DAV.Enabled = true
	})
	// Force the gate into existence before any test uses it.
	_, err := f.deps.StorageResolver(f.storage.ID)
	require.NoError(t, err)
	require.NotNil(t, gate)
	pipeline.AttachStorage(f.storage.ID, gate)
	// Always open the gate at the end of the test, whatever happened: an
	// assertion that fires while the transfer is parked must be reported, not
	// swallowed by a shutdown waiting on a blocked worker.
	t.Cleanup(gate.let)

	return &transferFixture{stagedFixture: f, gate: gate, thumb: pipeline}
}

// stageAndCommit pushes body through the staged protocol into main://<dir>/<name>
// and commits it. On return the node exists with transfer_state="staged" and the
// transfer is parked inside the gate.
func (f *transferFixture) stageAndCommit(t *testing.T, dir, name string, body []byte, chunk int64) (nodeID, opID int64) {
	t.Helper()
	total := int64(len(body))
	code, begun := f.begin(t, map[string]any{
		"path":       "main://" + dir,
		"name":       name,
		"size":       total,
		"chunk_size": chunk,
		"hash":       "sha256:" + sha256Hex(body),
	})
	require.Equal(t, http.StatusOK, code, "begin: %v", begun)
	id, _ := begun["id"].(string)
	require.NotEmpty(t, id)

	for off := int64(0); off < total; off += chunk {
		end := off + chunk
		if end > total {
			end = total
		}
		code, put := f.putChunk(t, id, off, end-off, total, body[off:end])
		require.Equal(t, http.StatusOK, code, "put at %d: %v", off, put)
	}

	code, committed := f.commit(t, id)
	require.Equal(t, http.StatusAccepted, code, "commit: %v", committed)
	require.Equal(t, model.TransferStateStaged, committed["transfer_state"])
	return num(committed["node_id"]), num(committed["op_id"])
}

// ── surface drivers (each one is the real HTTP route) ───────────────────────

func (f *transferFixture) get(t *testing.T, url string, hdr map[string]string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, url, nil)
	require.NoError(t, err)
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	resp, err := f.client.Do(req)
	require.NoError(t, err)
	return resp
}

func readAll(t *testing.T, resp *http.Response) []byte {
	t.Helper()
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	return b
}

// managerURL builds the app's own download/preview URL.
func (f *transferFixture) managerURL(action, rel string) string {
	return fmt.Sprintf("%s/api/files/manager?action=%s&path=%s", f.srv.URL, action, "main://"+rel)
}

// davGet is a WebDAV GET with the admin's Basic credentials.
func (f *transferFixture) davGet(t *testing.T, rel string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, f.srv.URL+"/dav/main/"+rel, nil)
	require.NoError(t, err)
	req.SetBasicAuth(f.adminEml, f.adminPw)
	resp, err := (&http.Client{}).Do(req)
	require.NoError(t, err)
	return resp
}

// shareFile mints a public link for a node.
func (f *transferFixture) shareToken(t *testing.T, nodeID int64) string {
	t.Helper()
	sh, err := f.deps.Share.Create(context.Background(), share.CreateOpts{NodeID: nodeID})
	require.NoError(t, err)
	return sh.Token
}

// seedFolderNode creates the directory on the driver AND its catalogue row, so
// a folder share resolves and its browse page can list.
func (f *transferFixture) seedFolderNode(t *testing.T, rel string) *model.Node {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Join(f.rootDir, rel), 0o755))
	n, err := f.store.CreateNode(context.Background(), &model.Node{
		StorageID: f.storage.ID,
		Name:      filepath.Base(rel),
		Path:      rel,
		PathHash:  pathkey.Hash(f.storage.ID, rel),
		Type:      model.NodeTypeDirectory,
	})
	require.NoError(t, err)
	return n
}

// ── the headline test ───────────────────────────────────────────────────────

// A 20 000-byte payload over 4 KiB chunks is five staged parts, so every read
// below crosses part boundaries — the assembled staging reader has to be right
// about where one part ends and the next begins, and a Range that lands inside
// part 3 has to open part 3.
const transferChunk = 4096

func transferPayload() []byte {
	// A repeating decimal pattern: an off-by-N window is visibly the wrong
	// slice rather than plausible-looking random bytes.
	return []byte(strings.Repeat("0123456789", 2000))
}

func TestReadDuringTransfer_EverySurfaceServesStagedBytes(t *testing.T) {
	f := newTransferFixture(t)
	folder := f.seedFolderNode(t, "docs")
	body := transferPayload()

	nodeID, _ := f.stageAndCommit(t, "docs", "moving.bin", body, transferChunk)
	f.gate.waitStarted(t)

	// The catalogue says the file is here, sized, and still transferring.
	node, err := f.store.GetNode(context.Background(), nodeID)
	require.NoError(t, err)
	require.Equal(t, model.TransferStateStaged, node.TransferState)
	require.EqualValues(t, len(body), node.Size)

	// …and the backend genuinely does not have it yet.
	_, statErr := f.gate.Driver.Stat(context.Background(), "docs/moving.bin")
	require.Error(t, statErr, "the driver must not have the object while the transfer is parked")

	t.Run("download", func(t *testing.T) {
		resp := f.get(t, f.managerURL("download", "docs/moving.bin"), nil)
		require.Equal(t, http.StatusOK, resp.StatusCode)
		got := readAll(t, resp)
		require.True(t, bytes.Equal(body, got), "download served %d bytes, want %d", len(got), len(body))
	})

	t.Run("preview", func(t *testing.T) {
		resp := f.get(t, f.managerURL("preview", "docs/moving.bin"), nil)
		require.Equal(t, http.StatusOK, resp.StatusCode)
		require.True(t, bytes.Equal(body, readAll(t, resp)))
	})

	t.Run("ranged download across a part boundary", func(t *testing.T) {
		// 4000-4200 straddles the end of part 1 (bytes 0-4095) and the start
		// of part 2 — the seam a naive concatenation gets wrong.
		resp := f.get(t, f.managerURL("download", "docs/moving.bin"),
			map[string]string{"Range": "bytes=4000-4199"})
		require.Equal(t, http.StatusPartialContent, resp.StatusCode)
		require.Equal(t, "bytes 4000-4199/20000", resp.Header.Get("Content-Range"))
		require.Equal(t, body[4000:4200], readAll(t, resp))
	})

	t.Run("ranged download deep inside a later part", func(t *testing.T) {
		resp := f.get(t, f.managerURL("download", "docs/moving.bin"),
			map[string]string{"Range": "bytes=15000-"})
		require.Equal(t, http.StatusPartialContent, resp.StatusCode)
		require.Equal(t, body[15000:], readAll(t, resp))
	})

	t.Run("share link", func(t *testing.T) {
		tok := f.shareToken(t, nodeID)
		resp := f.get(t, f.srv.URL+"/s/"+tok, nil)
		require.Equal(t, http.StatusOK, resp.StatusCode)
		require.True(t, bytes.Equal(body, readAll(t, resp)))
	})

	t.Run("folder-share browse", func(t *testing.T) {
		tok := f.shareToken(t, folder.ID)
		resp := f.get(t, f.srv.URL+"/s/"+tok+"/f/moving.bin", nil)
		require.Equal(t, http.StatusOK, resp.StatusCode)
		require.True(t, bytes.Equal(body, readAll(t, resp)))
	})

	t.Run("webdav GET", func(t *testing.T) {
		resp := f.davGet(t, "docs/moving.bin")
		require.Equal(t, http.StatusOK, resp.StatusCode)
		require.True(t, bytes.Equal(body, readAll(t, resp)))
	})

	t.Run("webdav ranged GET", func(t *testing.T) {
		req, err := http.NewRequest(http.MethodGet, f.srv.URL+"/dav/main/docs/moving.bin", nil)
		require.NoError(t, err)
		req.SetBasicAuth(f.adminEml, f.adminPw)
		req.Header.Set("Range", "bytes=9000-9099")
		resp, err := (&http.Client{}).Do(req)
		require.NoError(t, err)
		require.Equal(t, http.StatusPartialContent, resp.StatusCode)
		require.Equal(t, body[9000:9100], readAll(t, resp))
	})

	// Nothing above may have let the transfer finish behind our back.
	require.Zero(t, f.gate.writeCount(), "the transfer must still have been parked throughout")

	// Metadata, last so a failing run reports the SURFACES first: while staged,
	// Stat must answer with the committed values, and the ETag must describe
	// the staged bytes. Carrying the pre-overwrite ETag here would let a client
	// holding the old file revalidate, get a 304 and keep the version it was
	// just told had been replaced.
	require.NotEmpty(t, node.Etag, "a staged node must carry the ETag of the bytes that were committed")
	require.NotEqual(t, "", node.Etag)

	f.gate.let()
}

// ── the thumbnail pipeline ──────────────────────────────────────────────────

// tinyPNG is a 1x1 PNG. Small enough to inline, real enough that the image
// decoder either got the bytes or it did not.
var tinyPNG = []byte{
	0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a,
	0x00, 0x00, 0x00, 0x0d, 'I', 'H', 'D', 'R',
	0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01,
	0x08, 0x06, 0x00, 0x00, 0x00, 0x1f, 0x15, 0xc4,
	0x89, 0x00, 0x00, 0x00, 0x0a, 'I', 'D', 'A', 'T',
	0x78, 0x9c, 0x63, 0x00, 0x01, 0x00, 0x00, 0x05,
	0x00, 0x01, 0x0d, 0x0a, 0x2d, 0xb4, 0x00, 0x00,
	0x00, 0x00, 'I', 'E', 'N', 'D', 0xae, 0x42, 0x60, 0x82,
}

func TestReadDuringTransfer_ThumbnailUsesStagedBytes(t *testing.T) {
	f := newTransferFixture(t)
	nodeID, _ := f.stageAndCommit(t, "", "shot.png", tinyPNG, transferChunk)
	f.gate.waitStarted(t)

	node, err := f.store.GetNode(context.Background(), nodeID)
	require.NoError(t, err)
	require.Equal(t, model.TransferStateStaged, node.TransferState)

	// The dispatch path, not a private helper: GenerateThumb picks the image
	// generator off the mime and reads the source itself.
	require.NoError(t, f.thumb.GenerateThumb(context.Background(), node))

	th, err := f.store.GetThumbnail(context.Background(), nodeID)
	require.NoError(t, err)
	require.NotNil(t, th)
	assert.Equal(t, "ready", th.State,
		"a thumbnail for a file that is still transferring must come from the staged bytes (err=%q)", th.Error)

	f.gate.let()
}

// ── after the transfer ──────────────────────────────────────────────────────

func TestReadDuringTransfer_AfterTransferReadsComeFromTheDriver(t *testing.T) {
	f := newTransferFixture(t)
	body := transferPayload()
	nodeID, opID := f.stageAndCommit(t, "", "landed.bin", body, transferChunk)
	f.gate.waitStarted(t)
	f.gate.let()
	require.Equal(t, "ok", f.waitForOp(t, opID))

	// The catalogue flipped, the staging directory is gone, and the object is
	// on the backend — so any byte served now can only have come from there.
	node, err := f.store.GetNode(context.Background(), nodeID)
	require.NoError(t, err)
	require.Equal(t, model.TransferStateStored, node.TransferState)
	onDisk, err := os.ReadFile(filepath.Join(f.rootDir, "landed.bin"))
	require.NoError(t, err)
	require.True(t, bytes.Equal(body, onDisk), "the transferred file must be byte-identical to the source")
	require.Empty(t, stagingDirsOnDisk(t, f.dataDir), "a successful transfer must leave no staging behind")

	resp := f.get(t, f.managerURL("download", "landed.bin"), nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.True(t, bytes.Equal(body, readAll(t, resp)))

	ranged := f.get(t, f.managerURL("download", "landed.bin"),
		map[string]string{"Range": "bytes=4000-4199"})
	require.Equal(t, http.StatusPartialContent, ranged.StatusCode)
	require.Equal(t, body[4000:4200], readAll(t, ranged))

	tok := f.shareToken(t, nodeID)
	shared := f.get(t, f.srv.URL+"/s/"+tok, nil)
	require.Equal(t, http.StatusOK, shared.StatusCode)
	require.True(t, bytes.Equal(body, readAll(t, shared)))

	dav := f.davGet(t, "landed.bin")
	require.Equal(t, http.StatusOK, dav.StatusCode)
	require.True(t, bytes.Equal(body, readAll(t, dav)))
}

// stagingDirsOnDisk lists the staging directories currently present under
// <data>/uploads.
func stagingDirsOnDisk(t *testing.T, dataDir string) []string {
	t.Helper()
	ents, err := os.ReadDir(filepath.Join(dataDir, "uploads"))
	if err != nil {
		return nil
	}
	out := []string{}
	for _, e := range ents {
		if e.IsDir() {
			out = append(out, e.Name())
		}
	}
	return out
}

// ── a failed transfer ───────────────────────────────────────────────────────

// The staging directory is KEPT when a transfer fails, so the upload can be
// retried without re-sending a byte and the node stays "staged". A reader must
// not be able to tell the difference: the file is still openable.
func TestReadDuringTransfer_FailedTransferKeepsServing(t *testing.T) {
	f := newTransferFixture(t)
	body := transferPayload()
	f.gate.failWith(fmt.Errorf("backend is down"))

	nodeID, opID := f.stageAndCommit(t, "", "stuck.bin", body, transferChunk)
	f.gate.waitStarted(t)
	f.gate.let()
	require.Equal(t, "failed", f.waitForOp(t, opID))

	node, err := f.store.GetNode(context.Background(), nodeID)
	require.NoError(t, err)
	require.Equal(t, model.TransferStateFailed, node.TransferState,
		"a failed transfer is marked failed, not left at staged where it is "+
			"indistinguishable from one still in flight (issue #16)")
	require.NotEmpty(t, stagingDirsOnDisk(t, f.dataDir), "a failed transfer must keep its staging")

	resp := f.get(t, f.managerURL("download", "stuck.bin"), nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.True(t, bytes.Equal(body, readAll(t, resp)))

	tok := f.shareToken(t, nodeID)
	shared := f.get(t, f.srv.URL+"/s/"+tok, nil)
	require.Equal(t, http.StatusOK, shared.StatusCode)
	require.True(t, bytes.Equal(body, readAll(t, shared)))

	dav := f.davGet(t, "stuck.bin")
	require.Equal(t, http.StatusOK, dav.StatusCode)
	require.True(t, bytes.Equal(body, readAll(t, dav)))
}

// ── staging gone ────────────────────────────────────────────────────────────

// Reachable in production: the sweeper removes a `failed` session once it has
// been idle for a full TTL, and the node it belongs to keeps saying "staged"
// forever. The read must then FAIL, loudly, with a message — and must not fall
// back to whatever the driver happens to hold at that path, which on an
// overwrite is the version the staged bytes were replacing.
func TestReadDuringTransfer_MissingStagingFailsCleanly(t *testing.T) {
	f := newTransferFixture(t)
	body := transferPayload()
	f.gate.failWith(fmt.Errorf("backend is down"))

	nodeID, opID := f.stageAndCommit(t, "", "orphan.bin", body, transferChunk)
	f.gate.waitStarted(t)
	f.gate.let()
	require.Equal(t, "failed", f.waitForOp(t, opID))

	// Remove the staged bytes the way a sweep (or a human with rm) would.
	dirs := stagingDirsOnDisk(t, f.dataDir)
	require.NotEmpty(t, dirs)
	for _, d := range dirs {
		require.NoError(t, os.RemoveAll(filepath.Join(f.dataDir, "uploads", d)))
	}

	node, err := f.store.GetNode(context.Background(), nodeID)
	require.NoError(t, err)
	require.Equal(t, model.TransferStateFailed, node.TransferState)

	resp := f.get(t, f.managerURL("download", "orphan.bin"), nil)
	got := readAll(t, resp)
	require.Equal(t, http.StatusServiceUnavailable, resp.StatusCode,
		"a file whose staged copy is gone must fail with a real error, not a body: %s", got)
	require.NotContains(t, string(got), string(body[:64]),
		"not one byte of the file may be served once its staged copy is gone")
	require.Contains(t, string(got), "staged", "the error must say what happened")

	// And the same on the public surfaces, which must not 200 either.
	tok := f.shareToken(t, nodeID)
	shared := f.get(t, f.srv.URL+"/s/"+tok, nil)
	sharedBody := readAll(t, shared)
	require.Equal(t, http.StatusServiceUnavailable, shared.StatusCode)
	require.NotContains(t, string(sharedBody), string(body[:64]))

	dav := f.davGet(t, "orphan.bin")
	davBody := readAll(t, dav)
	require.NotEqual(t, http.StatusOK, dav.StatusCode, "WebDAV must not serve a body it does not have")
	require.NotContains(t, string(davBody), string(body[:64]))
}

// ── overwrite: the case where "the driver answered" is not good enough ───────

// A staged upload that REPLACES an existing file is the reason the catalogue is
// consulted before the driver rather than as a fallback. The backend still
// holds the previous version at that exact path, so a read that asks it gets a
// perfectly healthy 200 — of the wrong file. There is no error to notice.
func TestReadDuringTransfer_OverwriteServesTheNewBytesNotTheOldOnes(t *testing.T) {
	f := newTransferFixture(t)
	oldBody := []byte(strings.Repeat("OLDOLDOLD.", 2000))
	newBody := []byte(strings.Repeat("NEWNEWNEW.", 2000))
	require.Equal(t, len(oldBody), len(newBody), "same length, so only the CONTENT can tell them apart")

	// The previous version, landed the ordinary way.
	nodeID, opID := f.stageAndCommit(t, "", "same-name.bin", oldBody, transferChunk)
	f.gate.waitStarted(t)
	f.gate.let()
	require.Equal(t, "ok", f.waitForOp(t, opID))
	before, err := f.store.GetNode(context.Background(), nodeID)
	require.NoError(t, err)
	oldEtag := before.Etag

	// Park the NEXT transfer, so the replacement stays in flight.
	f.gate.rearm()

	newNodeID, _ := f.stageAndCommit(t, "", "same-name.bin", newBody, transferChunk)
	require.Equal(t, nodeID, newNodeID, "an overwrite updates the same node")
	f.gate.waitStarted(t)

	// The backend still has the OLD file — that is the trap.
	onDisk, err := os.ReadFile(filepath.Join(f.rootDir, "same-name.bin"))
	require.NoError(t, err)
	require.True(t, bytes.Equal(oldBody, onDisk), "the driver must still hold the previous version")

	resp := f.get(t, f.managerURL("download", "same-name.bin"), nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	got := readAll(t, resp)
	require.True(t, bytes.Equal(newBody, got),
		"an in-flight overwrite must serve the committed bytes, not the version they replace")

	tok := f.shareToken(t, nodeID)
	shared := f.get(t, f.srv.URL+"/s/"+tok, nil)
	require.Equal(t, http.StatusOK, shared.StatusCode)
	require.True(t, bytes.Equal(newBody, readAll(t, shared)))

	after, err := f.store.GetNode(context.Background(), nodeID)
	require.NoError(t, err)
	require.NotEqual(t, oldEtag, after.Etag,
		"keeping the pre-overwrite ETag would let a cached client revalidate into the old file")
}
