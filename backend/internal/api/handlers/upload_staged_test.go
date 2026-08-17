package handlers_test

// Staged upload protocol — the driver-agnostic, resumable write path.
//
// Every test here runs against a LOCAL storage driver on purpose. That is the
// whole point of the change: before it, the only chunked upload path required
// storage.MultipartUploader, which the S3 driver alone implements, so every
// other driver answered 501 "storage does not support multipart upload" — see
// TestLegacyChunkedUpload_NonS3Driver_Is501 below, which pins the gap this
// suite exists to close.
//
// This file used to state that it depends only on long-standing symbols so it
// could be replayed against the pre-chunk-4 tree as red evidence. That
// evidence has been taken, and the fixture now also wraps the store in
// internal/quotastore — because the fixture's job is to mirror what the
// running server wires (internal/server.New wraps at the same point), and a
// harness that quietly differs from production is how "the tests pass but the
// product is broken" happens. Chunk 7's red evidence is recorded in its own
// commit message and in docs/QUOTAS.md instead.

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"mime/multipart"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/api"
	"github.com/brf-tech/filex/backend/internal/auth"
	authlocal "github.com/brf-tech/filex/backend/internal/auth/drivers/local"
	"github.com/brf-tech/filex/backend/internal/capability"
	"github.com/brf-tech/filex/backend/internal/config"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/ops"
	"github.com/brf-tech/filex/backend/internal/quotastore"
	"github.com/brf-tech/filex/backend/internal/share"
	"github.com/brf-tech/filex/backend/internal/storage"
	"github.com/brf-tech/filex/backend/internal/storage/drivers/local"
	syncpkg "github.com/brf-tech/filex/backend/internal/sync"
	"github.com/brf-tech/filex/backend/internal/testutil"
)

// stagedFixture is a full router over an in-memory DB, a real local-FS storage
// and a running ops worker, with an admin session already on the client's jar.
type stagedFixture struct {
	srv      *httptest.Server
	client   *http.Client
	store    db.Store
	storage  *model.Storage
	rootDir  string // the local driver's root on disk
	dataDir  string // <data>/uploads is the staging area
	deps     *api.Deps
	userID   int64
	adminPw  string
	adminEml string
}

func newStagedFixture(t *testing.T) *stagedFixture {
	t.Helper()
	return newStagedFixtureWith(t, nil)
}

// newStagedFixtureWith builds the fixture, letting a caller adjust Deps (and
// through it the config) before the router is constructed.
func newStagedFixtureWith(t *testing.T, tweak func(*api.Deps)) *stagedFixture {
	t.Helper()

	sqlDB, raw := testutil.NewTestDB(t)
	// Mirror production: every consumer gets the quota-accounting store
	// (internal/server.New wraps it at the same point). Without this the
	// fixture would exercise a store the running product does not have, and
	// "usage is counted on every write path" would be untestable here.
	accounting := quotastore.New(raw)
	var store db.Store = accounting
	rootDir := t.TempDir()
	dataDir := t.TempDir()

	drv := &local.Driver{}
	require.NoError(t, drv.Init(context.Background(), map[string]any{"root": rootDir}))

	st, err := store.CreateStorage(context.Background(), &model.Storage{
		Name:       "main",
		Driver:     "local",
		MountPath:  "/data",
		Enabled:    true,
		ConfigJSON: json.RawMessage(`{"root":"` + jsonEscape(rootDir) + `"}`),
	})
	require.NoError(t, err)

	resolver := func(id int64) (storage.Driver, error) {
		if id != st.ID {
			return nil, fmt.Errorf("unknown storage %d", id)
		}
		return drv, nil
	}

	localDrv := authlocal.New(store)
	require.NoError(t, localDrv.Init(context.Background(), nil))
	auth.SetEnabled([]auth.Driver{localDrv})

	opsSvc := ops.New(sqlDB, resolver)
	require.NoError(t, opsSvc.Migrate(context.Background()))

	cfg := config.Default()
	cfg.PublicURL = "http://test.local"
	cfg.DataDir = dataDir
	cfg.CORS.AllowedOrigins = []string{"*"}

	deps := &api.Deps{
		Cfg:             cfg,
		Store:           store,
		Quota:           accounting.Quota(),
		Worker:          syncpkg.New(store),
		Caps:            capability.New(store),
		Share:           share.NewService(store),
		Ops:             opsSvc,
		StorageResolver: resolver,
		LocalAuth:       localDrv,
	}
	if tweak != nil {
		tweak(deps)
	}
	router := api.BuildRouter(deps)

	srv := httptest.NewServer(router)
	t.Cleanup(srv.Close)

	// Run the ops worker for the lifetime of the test — the staged commit is
	// finished by it, so without this nothing ever lands on the driver.
	wctx, cancel := context.WithCancel(context.Background())
	go opsSvc.Run(wctx)
	t.Cleanup(func() {
		cancel()
		opsSvc.Stop()
	})

	jar, err := cookiejar.New(nil)
	require.NoError(t, err)
	client := &http.Client{Jar: jar}

	email, pw := testutil.SeedAdmin(t, store)
	testutil.LoginAs(t, srv, client, email, pw)
	u, err := store.GetUserByEmail(context.Background(), email)
	require.NoError(t, err)

	return &stagedFixture{
		srv: srv, client: client, store: store, storage: st,
		rootDir: rootDir, dataDir: dataDir, deps: deps,
		userID: u.ID, adminEml: email, adminPw: pw,
	}
}

func jsonEscape(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(s, `\`, `\\`), `"`, `\"`)
}

// ── protocol helpers ────────────────────────────────────────────────────────

func (f *stagedFixture) begin(t *testing.T, body map[string]any) (int, map[string]any) {
	t.Helper()
	buf, _ := json.Marshal(body)
	resp, err := f.client.Post(f.srv.URL+"/api/files/upload/begin", "application/json", bytes.NewReader(buf))
	require.NoError(t, err)
	defer resp.Body.Close()
	out := map[string]any{}
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

// putChunk sends `body` announcing the range [start, start+claimLen). Passing a
// claimLen larger than len(body) is how an interrupted chunk is simulated: the
// header promises bytes the connection never delivers.
func (f *stagedFixture) putChunk(t *testing.T, id string, start int64, claimLen int64, total int64, body []byte) (int, map[string]any) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPut, f.srv.URL+"/api/files/upload/"+id, bytes.NewReader(body))
	require.NoError(t, err)
	req.Header.Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, start+claimLen-1, total))
	req.Header.Set("Content-Type", "application/octet-stream")
	resp, err := f.client.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	out := map[string]any{}
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

func (f *stagedFixture) status(t *testing.T, id string) (int, map[string]any) {
	t.Helper()
	resp, err := f.client.Get(f.srv.URL + "/api/files/upload/" + id)
	require.NoError(t, err)
	defer resp.Body.Close()
	out := map[string]any{}
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

func (f *stagedFixture) commit(t *testing.T, id string) (int, map[string]any) {
	t.Helper()
	resp, err := f.client.Post(f.srv.URL+"/api/files/upload/"+id+"/commit", "application/json", nil)
	require.NoError(t, err)
	defer resp.Body.Close()
	out := map[string]any{}
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

func (f *stagedFixture) abort(t *testing.T, id string) int {
	t.Helper()
	req, err := http.NewRequest(http.MethodDelete, f.srv.URL+"/api/files/upload/"+id, nil)
	require.NoError(t, err)
	resp, err := f.client.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	return resp.StatusCode
}

// waitForOp polls the ops tray until the op leaves the queue.
func (f *stagedFixture) waitForOp(t *testing.T, opID int64) string {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := f.client.Get(fmt.Sprintf("%s/api/files/ops/%d", f.srv.URL, opID))
		require.NoError(t, err)
		var op map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&op)
		resp.Body.Close()
		switch op["status"] {
		case "ok", "failed", "partial":
			return fmt.Sprint(op["status"])
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("op %d never finished", opID)
	return ""
}

func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func randomBytes(n int) []byte {
	b := make([]byte, n)
	rnd := rand.New(rand.NewSource(int64(n) * 7919))
	_, _ = rnd.Read(b)
	return b
}

func num(v any) int64 {
	switch t := v.(type) {
	case float64:
		return int64(t)
	case int64:
		return t
	}
	return -1
}

// ── the gap this suite closes ───────────────────────────────────────────────

// The legacy chunked path needs storage.MultipartUploader; a local driver does
// not have it, so it answers 501. This is not a bug in that handler — it is the
// reason the staged path exists, and this test pins it so nobody "fixes" the
// legacy path by making it lie.
func TestLegacyChunkedUpload_NonS3Driver_Is501(t *testing.T) {
	f := newStagedFixture(t)
	body, _ := json.Marshal(map[string]any{
		"storage_id": f.storage.ID,
		"path":       "main://",
		"filename":   "big.bin",
		"size":       50 << 20,
	})
	resp, err := f.client.Post(f.srv.URL+"/api/files/upload/init", "application/json", bytes.NewReader(body))
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusNotImplemented, resp.StatusCode,
		"the presigned chunked path is S3-only by construction")
}

// ── begin → chunks → interrupted chunk → resume → commit → bytes ────────────

// The headline test: a chunked, resumable upload onto a NON-S3 driver, with a
// simulated mid-chunk disconnection in the middle, and a checksum on what
// actually landed on disk.
func TestStagedUpload_ResumeAfterInterruption_BytesMatch(t *testing.T) {
	f := newStagedFixture(t)

	const chunk = 4096
	src := randomBytes(10000) // 3 parts: 4096 + 4096 + 1808
	total := int64(len(src))

	code, begun := f.begin(t, map[string]any{
		"path":       "main://",
		"name":       "resume.bin",
		"size":       total,
		"chunk_size": chunk,
		"hash":       "sha256:" + sha256Hex(src),
	})
	require.Equal(t, http.StatusOK, code, "begin must work on a local driver: %v", begun)
	id, _ := begun["id"].(string)
	require.NotEmpty(t, id)
	require.EqualValues(t, chunk, num(begun["chunk_size"]))
	require.EqualValues(t, 0, num(begun["offset"]))

	// Chunk 1 and 2 arrive whole.
	code, put := f.putChunk(t, id, 0, chunk, total, src[:chunk])
	require.Equal(t, http.StatusOK, code, "%v", put)
	assert.EqualValues(t, chunk, num(put["offset"]))

	code, put = f.putChunk(t, id, chunk, chunk, total, src[chunk:2*chunk])
	require.Equal(t, http.StatusOK, code, "%v", put)
	assert.EqualValues(t, 2*chunk, num(put["offset"]))

	// Chunk 3 is cut off mid-flight: the header promises 1808 bytes, only 900
	// arrive. It must be refused AND must not move the offset — accepting a
	// partial chunk is how a resumable upload silently corrupts a file.
	code, put = f.putChunk(t, id, 2*chunk, total-2*chunk, total, src[2*chunk:2*chunk+900])
	require.Equal(t, http.StatusBadRequest, code, "%v", put)

	// The client lost its state; it asks where to continue.
	code, stat := f.status(t, id)
	require.Equal(t, http.StatusOK, code)
	require.EqualValues(t, 2*chunk, num(stat["offset"]), "offset must not advance over an interrupted chunk")
	require.Equal(t, false, stat["complete"])

	// Resume from the reported offset.
	code, put = f.putChunk(t, id, num(stat["offset"]), total-2*chunk, total, src[2*chunk:])
	require.Equal(t, http.StatusOK, code, "%v", put)
	assert.EqualValues(t, total, num(put["offset"]))

	code, committed := f.commit(t, id)
	require.Equal(t, http.StatusAccepted, code, "%v", committed)
	nodeID := num(committed["node_id"])
	require.Positive(t, nodeID)
	opID := num(committed["op_id"])
	require.Positive(t, opID)

	// The node is listed the moment the commit is accepted, while its bytes are
	// still in staging — the seam chunk 5 builds on.
	node, err := f.store.GetNode(context.Background(), nodeID)
	require.NoError(t, err)
	assert.Equal(t, "/resume.bin", node.Path)

	require.Equal(t, "ok", f.waitForOp(t, opID))

	landed, err := os.ReadFile(filepath.Join(f.rootDir, "resume.bin"))
	require.NoError(t, err)
	assert.Equal(t, sha256Hex(src), sha256Hex(landed), "bytes on the driver must match the source")
	assert.Len(t, landed, len(src))

	// Staging is gone and the session row with it.
	entries, _ := os.ReadDir(filepath.Join(f.dataDir, "uploads"))
	assert.Empty(t, entries, "staging directory must be removed after a successful transfer")
	code, _ = f.status(t, id)
	assert.Equal(t, http.StatusNotFound, code, "the session is gone once the bytes are stored")
	// (transfer_state is asserted at the DB level in upload_staged_guard_test.go)
}

// Parts may arrive in any order — the S3 UploadPart API this layer is built for
// does exactly that. The offset only counts the contiguous run from part 1, so
// a part written past a hole is kept but does not pretend the hole is filled.
func TestStagedUpload_OutOfOrderParts_AssembleCorrectly(t *testing.T) {
	f := newStagedFixture(t)

	const chunk = 4096
	src := randomBytes(4096 * 3)
	total := int64(len(src))

	code, begun := f.begin(t, map[string]any{
		"path": "main://", "name": "ooo.bin", "size": total, "chunk_size": chunk,
	})
	require.Equal(t, http.StatusOK, code, "%v", begun)
	id := begun["id"].(string)

	// Part 3 first.
	code, put := f.putChunk(t, id, 2*chunk, chunk, total, src[2*chunk:])
	require.Equal(t, http.StatusOK, code, "%v", put)
	assert.EqualValues(t, 0, num(put["offset"]), "a part past a hole must not move the resume point")

	// Part 2 — still a hole at part 1.
	code, put = f.putChunk(t, id, chunk, chunk, total, src[chunk:2*chunk])
	require.Equal(t, http.StatusOK, code, "%v", put)
	assert.EqualValues(t, 0, num(put["offset"]))

	// Part 1 closes the hole and the offset jumps to the end in one step.
	code, put = f.putChunk(t, id, 0, chunk, total, src[:chunk])
	require.Equal(t, http.StatusOK, code, "%v", put)
	assert.EqualValues(t, total, num(put["offset"]))

	code, committed := f.commit(t, id)
	require.Equal(t, http.StatusAccepted, code, "%v", committed)
	require.Equal(t, "ok", f.waitForOp(t, num(committed["op_id"])))

	landed, err := os.ReadFile(filepath.Join(f.rootDir, "ooo.bin"))
	require.NoError(t, err)
	assert.Equal(t, sha256Hex(src), sha256Hex(landed), "assembly must be in part-number order, not arrival order")
}

// A declared hash that does not match the staged bytes is refused before a
// single byte is handed to the driver.
func TestStagedUpload_HashMismatch_Refused(t *testing.T) {
	f := newStagedFixture(t)
	src := randomBytes(5000)
	total := int64(len(src))

	code, begun := f.begin(t, map[string]any{
		"path": "main://", "name": "bad-hash.bin", "size": total, "chunk_size": 8192,
		"hash": "sha256:" + sha256Hex(randomBytes(16)),
	})
	require.Equal(t, http.StatusOK, code, "%v", begun)
	id := begun["id"].(string)

	code, _ = f.putChunk(t, id, 0, total, total, src)
	require.Equal(t, http.StatusOK, code)

	code, out := f.commit(t, id)
	assert.Equal(t, http.StatusUnprocessableEntity, code, "%v", out)
	_, err := os.Stat(filepath.Join(f.rootDir, "bad-hash.bin"))
	assert.Error(t, err, "nothing may reach the driver on a hash mismatch")
}

// Committing before every part is present is a conflict, and the response says
// exactly how far the server got.
func TestStagedUpload_CommitIncomplete_Refused(t *testing.T) {
	f := newStagedFixture(t)
	src := randomBytes(9000)
	total := int64(len(src))

	code, begun := f.begin(t, map[string]any{
		"path": "main://", "name": "half.bin", "size": total, "chunk_size": 4096,
	})
	require.Equal(t, http.StatusOK, code, "%v", begun)
	id := begun["id"].(string)
	code, _ = f.putChunk(t, id, 0, 4096, total, src[:4096])
	require.Equal(t, http.StatusOK, code)

	code, out := f.commit(t, id)
	assert.Equal(t, http.StatusConflict, code)
	assert.EqualValues(t, 4096, num(out["offset"]))
}

// ── abort ───────────────────────────────────────────────────────────────────

func TestStagedUpload_Abort_DeletesStaging(t *testing.T) {
	f := newStagedFixture(t)
	src := randomBytes(8000)
	total := int64(len(src))

	code, begun := f.begin(t, map[string]any{
		"path": "main://", "name": "abort.bin", "size": total, "chunk_size": 4096,
	})
	require.Equal(t, http.StatusOK, code, "%v", begun)
	id := begun["id"].(string)
	code, _ = f.putChunk(t, id, 0, 4096, total, src[:4096])
	require.Equal(t, http.StatusOK, code)

	stagingDir := filepath.Join(f.dataDir, "uploads", id)
	_, err := os.Stat(stagingDir)
	require.NoError(t, err, "staging directory should exist before the abort")

	assert.Equal(t, http.StatusOK, f.abort(t, id))

	_, err = os.Stat(stagingDir)
	assert.True(t, os.IsNotExist(err), "abort must delete the staging directory")

	code, _ = f.status(t, id)
	assert.Equal(t, http.StatusNotFound, code, "abort must delete the session row too")
}

// ── ownership ───────────────────────────────────────────────────────────────

// An upload id is a handle, not a capability: the owner is verified on every
// call, so another account cannot push bytes into someone else's upload.
func TestStagedUpload_NonOwnerCannotPut(t *testing.T) {
	f := newStagedFixture(t)
	src := randomBytes(8000)
	total := int64(len(src))

	code, begun := f.begin(t, map[string]any{
		"path": "main://", "name": "owned.bin", "size": total, "chunk_size": 4096,
	})
	require.Equal(t, http.StatusOK, code, "%v", begun)
	id := begun["id"].(string)

	// A second, unrelated account with its own session.
	testutil.SeedRegularUser(t, f.store, "intruder@test.local", "IntruderPass!1")
	jar, err := cookiejar.New(nil)
	require.NoError(t, err)
	intruder := &http.Client{Jar: jar}
	testutil.LoginAs(t, f.srv, intruder, "intruder@test.local", "IntruderPass!1")

	req, err := http.NewRequest(http.MethodPut, f.srv.URL+"/api/files/upload/"+id, bytes.NewReader(src[:4096]))
	require.NoError(t, err)
	req.Header.Set("Content-Range", fmt.Sprintf("bytes 0-4095/%d", total))
	resp, err := intruder.Do(req)
	require.NoError(t, err)
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	assert.Equal(t, http.StatusNotFound, resp.StatusCode,
		"a non-owner must not be able to write into someone else's upload: %s", body)

	// …and cannot read its state, commit it or abort it either.
	sresp, err := intruder.Get(f.srv.URL + "/api/files/upload/" + id)
	require.NoError(t, err)
	sresp.Body.Close()
	assert.Equal(t, http.StatusNotFound, sresp.StatusCode)

	cresp, err := intruder.Post(f.srv.URL+"/api/files/upload/"+id+"/commit", "application/json", nil)
	require.NoError(t, err)
	cresp.Body.Close()
	assert.Equal(t, http.StatusNotFound, cresp.StatusCode)

	dreq, _ := http.NewRequest(http.MethodDelete, f.srv.URL+"/api/files/upload/"+id, nil)
	dresp, err := intruder.Do(dreq)
	require.NoError(t, err)
	dresp.Body.Close()
	assert.Equal(t, http.StatusNotFound, dresp.StatusCode)

	// The owner's upload is untouched.
	code, stat := f.status(t, id)
	require.Equal(t, http.StatusOK, code)
	assert.EqualValues(t, 0, num(stat["offset"]))
}

// ── quota ───────────────────────────────────────────────────────────────────

// Quota is reserved at begin. Without that a staged upload is invisible to the
// ceiling until it commits, and a user can stage far past their limit.
func TestStagedUpload_QuotaRefusesOverLimitBegin(t *testing.T) {
	f := newStagedFixture(t)
	require.NoError(t, f.store.SetUserQuota(context.Background(), f.userID, 10000))

	code, out := f.begin(t, map[string]any{
		"path": "main://", "name": "too-big.bin", "size": 20000, "chunk_size": 4096,
	})
	assert.Equal(t, http.StatusRequestEntityTooLarge, code, "%v", out)
	assert.Equal(t, "QUOTA_EXCEEDED", out["code"])

	// Under the limit is fine…
	code, first := f.begin(t, map[string]any{
		"path": "main://", "name": "ok-1.bin", "size": 6000, "chunk_size": 4096,
	})
	require.Equal(t, http.StatusOK, code, "%v", first)

	// …but the reservation it holds counts against the next one, even though
	// not a single byte has been uploaded yet.
	code, second := f.begin(t, map[string]any{
		"path": "main://", "name": "ok-2.bin", "size": 6000, "chunk_size": 4096,
	})
	assert.Equal(t, http.StatusRequestEntityTooLarge, code,
		"an open staged upload must hold its reservation: %v", second)

	// Aborting the first releases the reservation.
	require.Equal(t, http.StatusOK, f.abort(t, first["id"].(string)))
	code, third := f.begin(t, map[string]any{
		"path": "main://", "name": "ok-3.bin", "size": 6000, "chunk_size": 4096,
	})
	assert.Equal(t, http.StatusOK, code, "%v", third)
}

// ── input guards ────────────────────────────────────────────────────────────

func TestStagedUpload_RejectsTraversalAndBadRanges(t *testing.T) {
	f := newStagedFixture(t)

	// `..` in the directory is rejected by the shared path guard.
	code, _ := f.begin(t, map[string]any{"path": "main://../etc", "name": "x.bin", "size": 10})
	assert.Equal(t, http.StatusBadRequest, code)

	// …and as a filename, where path.Base would otherwise hand back ".." and
	// path.Join would walk out of the destination directory.
	code, _ = f.begin(t, map[string]any{"path": "main://", "name": "..", "size": 10})
	assert.Equal(t, http.StatusBadRequest, code)

	code, begun := f.begin(t, map[string]any{
		"path": "main://", "name": "ranges.bin", "size": 12288, "chunk_size": 4096,
	})
	require.Equal(t, http.StatusOK, code, "%v", begun)
	id := begun["id"].(string)
	body := randomBytes(4096)

	// Misaligned start.
	code, _ = f.putChunk(t, id, 100, 4096, 12288, body)
	assert.Equal(t, http.StatusBadRequest, code, "a chunk must start on the grid begin handed out")

	// Wrong declared total.
	code, _ = f.putChunk(t, id, 0, 4096, 999, body)
	assert.Equal(t, http.StatusBadRequest, code)

	// Range past the end of the object.
	code, _ = f.putChunk(t, id, 8192, 8192, 12288, randomBytes(8192))
	assert.Equal(t, http.StatusBadRequest, code)

	// A wrong-length (but in-range) chunk: part 1 must be exactly 4096.
	code, _ = f.putChunk(t, id, 0, 2048, 12288, body[:2048])
	assert.Equal(t, http.StatusBadRequest, code)

	// An unknown id is a 404 and never a path.
	code, _ = f.status(t, "not-a-real-upload-id-000000")
	assert.Equal(t, http.StatusNotFound, code)
}

// The old `?action=upload` multipart path must keep working untouched — it is
// the small-file fast path and every integration still uses it.
func TestLegacyMultipartUpload_StillWorks(t *testing.T) {
	f := newStagedFixture(t)

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	require.NoError(t, mw.WriteField("path", "main://"))
	part, err := mw.CreateFormFile("file[]", "small.txt")
	require.NoError(t, err)
	_, err = io.WriteString(part, "hello fast path")
	require.NoError(t, err)
	require.NoError(t, mw.Close())

	req, err := http.NewRequest(http.MethodPost, f.srv.URL+"/api/files/manager?action=upload", &buf)
	require.NoError(t, err)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	resp, err := f.client.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	got, err := os.ReadFile(filepath.Join(f.rootDir, "small.txt"))
	require.NoError(t, err)
	assert.Equal(t, "hello fast path", string(got))
}
