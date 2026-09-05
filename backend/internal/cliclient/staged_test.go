package cliclient

// Resumable uploads for the CLI — and therefore for `filex sync`, and therefore
// for the desktop app, which drives sync by running this binary.
//
// The case these tests exist for is the one that was asked for in as many
// words (translated from Turkish): "if it is interrupted halfway, it has to be
// able to resume." So they do not check which endpoints were called. They cut
// a real TCP connection in the middle of a chunk, throw the client away, build
// a NEW one — the equivalent of the process exiting and being started again —
// and then measure:
//
//	how many bytes crossed the wire, and whether the assembled file's sha256
//	matches the local one.
//
// A test that asserted "PUT was called" would pass while the product re-sent
// four gigabytes.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ── a fake filex speaking the staged protocol ───────────────────────────────

type stagedSession struct {
	id        string
	path      string
	name      string
	total     int64
	chunkSize int64
	hash      string
	parts     map[int][]byte
	state     string
}

// stagedFake implements enough of docs/UPLOADS.md to hold the client to the
// protocol: the numbered-part store, the CONTIGUOUS offset, and the refusal of
// a chunk shorter than its Content-Range claims.
type stagedFake struct {
	t   *testing.T
	mu  sync.Mutex
	srv *httptest.Server

	sessions map[string]*stagedSession
	seq      int

	beginCalls  int
	commitCalls int
	statusCalls int
	// wireBytes counts every byte the client pushed, accepted or not — the
	// number that answers "did it resume or start over?".
	wireBytes int64
	// multipartBytes counts the legacy one-shot path separately.
	multipartBytes int64

	// cutAt: when set, the PUT starting at this offset has its connection
	// killed after cutAfter bytes. Cleared by clearCut.
	cutAt    int64
	cutAfter int64
	cutOn    bool

	// beginStatus, when non-zero, is returned instead of a session (used to
	// pin the fallback to the multipart path on an older server).
	beginStatus int
}

func newStagedFake(t *testing.T) *stagedFake {
	f := &stagedFake{t: t, sessions: map[string]*stagedSession{}, cutAt: -1}
	f.srv = httptest.NewServer(http.HandlerFunc(f.handle))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *stagedFake) client(resumeDir string) *Client {
	// A 4 KiB grid keeps the tests fast while exercising the real multi-part
	// arithmetic; production defaults to 8 MiB on both ends.
	return &Client{
		BaseURL:         strings.TrimRight(f.srv.URL, "/"),
		Token:           "tok",
		HTTP:            &http.Client{},
		StagedThreshold: stagedTestChunk,
		ChunkSize:       stagedTestChunk,
		ResumeDir:       resumeDir,
	}
}

func (f *stagedFake) cut(at, after int64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cutAt, f.cutAfter, f.cutOn = at, after, true
}

func (f *stagedFake) clearCut() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cutOn = false
}

func (f *stagedFake) writeJSON(w http.ResponseWriter, code int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(body)
}

// offset is the contiguous run from part 1 — the resume point, exactly as the
// server computes it. A part past a hole does not move it.
func (s *stagedSession) offset() int64 {
	var off int64
	for n := 1; ; n++ {
		p, ok := s.parts[n]
		if !ok {
			return off
		}
		off += int64(len(p))
	}
}

func (s *stagedSession) partCount() int {
	if s.chunkSize <= 0 {
		return 0
	}
	return int((s.total + s.chunkSize - 1) / s.chunkSize)
}

func (s *stagedSession) complete() bool {
	return len(s.parts) == s.partCount() && s.offset() == s.total
}

func (s *stagedSession) assembled() []byte {
	keys := make([]int, 0, len(s.parts))
	for k := range s.parts {
		keys = append(keys, k)
	}
	sort.Ints(keys)
	out := make([]byte, 0, s.total)
	for _, k := range keys {
		out = append(out, s.parts[k]...)
	}
	return out
}

func (f *stagedFake) handle(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Authorization") != "Bearer tok" {
		f.writeJSON(w, 401, map[string]string{"error": "unauthorized"})
		return
	}
	switch {
	case r.URL.Path == "/api/files/upload/begin":
		f.handleBegin(w, r)
	case strings.HasSuffix(r.URL.Path, "/commit"):
		f.handleCommit(w, r)
	case strings.HasPrefix(r.URL.Path, "/api/files/upload/"):
		f.handleSession(w, r)
	case strings.HasPrefix(r.URL.Path, "/api/files/ops/"):
		f.writeJSON(w, 200, map[string]any{"id": 1, "status": "ok"})
	case r.URL.Path == "/api/files/manager":
		f.handleManager(w, r)
	default:
		f.writeJSON(w, 404, map[string]string{"error": "no route " + r.URL.Path})
	}
}

func (f *stagedFake) handleBegin(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	f.beginCalls++
	status := f.beginStatus
	f.mu.Unlock()
	if status != 0 {
		f.writeJSON(w, status, map[string]string{"error": "staged uploads unavailable"})
		return
	}
	var body struct {
		Path      string `json:"path"`
		Name      string `json:"name"`
		Size      int64  `json:"size"`
		Hash      string `json:"hash"`
		ChunkSize int64  `json:"chunk_size"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		f.writeJSON(w, 400, map[string]string{"error": "bad json"})
		return
	}
	chunk := body.ChunkSize
	if chunk <= 0 {
		chunk = stagedTestChunk
	}
	f.mu.Lock()
	f.seq++
	id := fmt.Sprintf("upload-%08d", f.seq)
	f.sessions[id] = &stagedSession{
		id: id, path: body.Path, name: body.Name, total: body.Size,
		chunkSize: chunk, hash: body.Hash, parts: map[int][]byte{}, state: "staging",
	}
	f.mu.Unlock()
	f.writeJSON(w, 200, map[string]any{
		"id": id, "chunk_size": chunk, "offset": 0,
		"total_size": body.Size, "state": "staging",
	})
}

func (f *stagedFake) session(r *http.Request) (*stagedSession, bool) {
	id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/api/files/upload/"), "/commit")
	f.mu.Lock()
	defer f.mu.Unlock()
	s, ok := f.sessions[id]
	return s, ok
}

func (f *stagedFake) handleSession(w http.ResponseWriter, r *http.Request) {
	s, ok := f.session(r)
	if !ok {
		f.writeJSON(w, 404, map[string]string{"error": "upload not found"})
		return
	}
	switch r.Method {
	case http.MethodGet:
		f.mu.Lock()
		f.statusCalls++
		f.mu.Unlock()
		f.writeJSON(w, 200, map[string]any{
			"id": s.id, "offset": s.offset(), "received": s.offset(),
			"total_size": s.total, "chunk_size": s.chunkSize,
			"state": s.state, "complete": s.complete(),
		})
	case http.MethodDelete:
		f.mu.Lock()
		delete(f.sessions, s.id)
		f.mu.Unlock()
		f.writeJSON(w, 200, map[string]any{"ok": true})
	case http.MethodPut:
		f.handlePut(w, r, s)
	default:
		f.writeJSON(w, 405, map[string]string{"error": "method"})
	}
}

func (f *stagedFake) handlePut(w http.ResponseWriter, r *http.Request, s *stagedSession) {
	var start, end, total int64
	if _, err := fmt.Sscanf(r.Header.Get("Content-Range"), "bytes %d-%d/%d", &start, &end, &total); err != nil {
		f.writeJSON(w, 400, map[string]string{"error": "bad Content-Range"})
		return
	}
	if total != s.total {
		f.writeJSON(w, 400, map[string]string{"error": "total mismatch"})
		return
	}
	if start%s.chunkSize != 0 {
		f.writeJSON(w, 400, map[string]string{"error": "chunk off the grid"})
		return
	}
	claimed := end - start + 1

	f.mu.Lock()
	cutting := f.cutOn && f.cutAt == start
	cutAfter := f.cutAfter
	f.mu.Unlock()

	if cutting {
		// Read part of the body, then kill the connection underneath the
		// client — the shape a dropped link actually takes.
		n, _ := io.CopyN(io.Discard, r.Body, cutAfter)
		f.mu.Lock()
		f.wireBytes += n
		f.mu.Unlock()
		if hj, ok := w.(http.Hijacker); ok {
			conn, _, err := hj.Hijack()
			if err == nil {
				_ = conn.Close()
				return
			}
		}
		f.writeJSON(w, 500, map[string]string{"error": "cut"})
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, claimed+1))
	f.mu.Lock()
	f.wireBytes += int64(len(body))
	f.mu.Unlock()
	if err != nil {
		f.writeJSON(w, 400, map[string]string{"error": "read body"})
		return
	}
	if int64(len(body)) != claimed {
		// The offset must NOT advance over bytes we do not have.
		f.writeJSON(w, 400, map[string]string{"error": "SHORT_CHUNK", "code": "SHORT_CHUNK"})
		return
	}
	f.mu.Lock()
	s.parts[int(start/s.chunkSize)+1] = body
	off := s.offset()
	f.mu.Unlock()
	f.writeJSON(w, 200, map[string]any{
		"id": s.id, "offset": off, "received": off,
		"total_size": s.total, "state": s.state,
	})
}

func (f *stagedFake) handleCommit(w http.ResponseWriter, r *http.Request) {
	s, ok := f.session(r)
	if !ok {
		f.writeJSON(w, 404, map[string]string{"error": "upload not found"})
		return
	}
	f.mu.Lock()
	f.commitCalls++
	f.mu.Unlock()
	if !s.complete() {
		f.writeJSON(w, 409, map[string]any{"error": "upload incomplete", "offset": s.offset()})
		return
	}
	// The declared digest is verified over the ASSEMBLED bytes — the check a
	// resume actually needs, because its parts were written by two different
	// processes.
	if s.hash != "" {
		want := strings.TrimPrefix(s.hash, "sha256:")
		sum := sha256.Sum256(s.assembled())
		if hex.EncodeToString(sum[:]) != want {
			f.writeJSON(w, 422, map[string]string{"error": "hash mismatch"})
			return
		}
	}
	s.state = "committing"
	f.writeJSON(w, 202, map[string]any{
		"id": s.id, "op_id": 1, "node_id": 9,
		"path": s.path + "/" + s.name, "transfer_state": "staged",
	})
}

// handleManager is the legacy one-shot multipart path — kept so the tests can
// measure what it costs when a connection drops.
func (f *stagedFake) handleManager(w http.ResponseWriter, r *http.Request) {
	if r.URL.Query().Get("action") != "upload" {
		f.writeJSON(w, 200, map[string]any{"adapter": "docs", "files": []any{}})
		return
	}
	f.mu.Lock()
	cutting := f.cutOn && f.cutAt == 0
	cutAfter := f.cutAfter
	f.mu.Unlock()
	if cutting {
		n, _ := io.CopyN(io.Discard, r.Body, cutAfter)
		f.mu.Lock()
		f.multipartBytes += n
		f.mu.Unlock()
		if hj, ok := w.(http.Hijacker); ok {
			if conn, _, err := hj.Hijack(); err == nil {
				_ = conn.Close()
				return
			}
		}
		f.writeJSON(w, 500, map[string]string{"error": "cut"})
		return
	}
	n, _ := io.Copy(io.Discard, r.Body)
	f.mu.Lock()
	f.multipartBytes += n
	f.mu.Unlock()
	f.writeJSON(w, 200, map[string]any{"adapter": "docs", "files": []any{}})
}

// ── fixtures ────────────────────────────────────────────────────────────────

const stagedTestChunk = 4096

func stagedTestFile(t *testing.T, n int) (string, []byte) {
	t.Helper()
	b := make([]byte, n)
	rnd := rand.New(rand.NewSource(int64(n)*7919 + 3))
	_, _ = rnd.Read(b)
	p := filepath.Join(t.TempDir(), "payload.bin")
	require.NoError(t, os.WriteFile(p, b, 0o600))
	return p, b
}

func sha256Of(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func onlySession(t *testing.T, f *stagedFake) *stagedSession {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	require.Len(t, f.sessions, 1, "expected exactly one staged session")
	for _, s := range f.sessions {
		return s
	}
	return nil
}

// ── the headline test ───────────────────────────────────────────────────────

// A large file, the connection cut in the middle of a chunk, the client thrown
// away, a NEW client built (the process restarted) — and the upload continues
// from the server's offset instead of from zero.
func TestUpload_ResumesAcrossProcessRestart(t *testing.T) {
	f := newStagedFake(t)
	resumeDir := t.TempDir()
	local, data := stagedTestFile(t, stagedTestChunk*5+321)

	// ── run 1: dies inside chunk 3 ──
	f.cut(stagedTestChunk*2, 700)
	c1 := f.client(resumeDir)
	_, _, err := c1.Upload(context.Background(), local, "docs://inbox/")
	require.Error(t, err, "the run must fail — the link was cut")

	s := onlySession(t, f)
	assert.EqualValues(t, stagedTestChunk*2, s.offset(),
		"an interrupted chunk must NOT advance the offset")

	// The bookmark is what survives the process, and it is the only thing that
	// has to: the bytes are the server's.
	entries, rerr := os.ReadDir(resumeDir)
	require.NoError(t, rerr)
	require.Len(t, entries, 1, "an interrupted upload must leave a resume bookmark")

	wireAfterRun1 := func() int64 { f.mu.Lock(); defer f.mu.Unlock(); return f.wireBytes }()

	// ── run 2: a brand-new client — nothing shared but the bookmark dir ──
	f.clearCut()
	c2 := f.client(resumeDir)
	dest, raw, err := c2.Upload(context.Background(), local, "docs://inbox/")
	require.NoError(t, err)
	assert.Equal(t, "docs://inbox/payload.bin", dest.String())

	f.mu.Lock()
	begins, commits, wire := f.beginCalls, f.commitCalls, f.wireBytes
	f.mu.Unlock()

	assert.Equal(t, 1, begins, "resuming must NOT open a second staged session")
	assert.Equal(t, 1, commits)

	// The measurement that matters. Everything before the cut was sent once;
	// only the interrupted chunk is paid for twice.
	resent := wire - wireAfterRun1
	assert.Less(t, resent, int64(len(data)),
		"the second run re-sent the whole file — that is the bug this change removes")
	assert.EqualValues(t, int64(len(data))-stagedTestChunk*2, resent,
		"the second run must send exactly the bytes after the server's offset")

	// And the file is the file.
	assert.Equal(t, sha256Of(data), sha256Of(s.assembled()))
	assert.Contains(t, string(raw), `"node_id"`)

	// A finished upload leaves no bookmark to trip over next time.
	entries, rerr = os.ReadDir(resumeDir)
	require.NoError(t, rerr)
	assert.Empty(t, entries)
}

// RED EVIDENCE — the behaviour being replaced, measured rather than described.
//
// With the staged path disabled the client falls back to exactly what it did
// before this change: one multipart POST per file, no resume. Cut it in the
// middle and the next attempt sends the whole file again.
func TestUpload_MultipartPath_RestartsFromZero(t *testing.T) {
	f := newStagedFake(t)
	local, data := stagedTestFile(t, stagedTestChunk*5+321)

	c := f.client(t.TempDir())
	c.StagedThreshold = -1 // pin the pre-change path

	f.cut(0, 700)
	_, _, err := c.Upload(context.Background(), local, "docs://inbox/")
	require.Error(t, err)

	f.clearCut()
	_, _, err = c.Upload(context.Background(), local, "docs://inbox/")
	require.NoError(t, err)

	f.mu.Lock()
	sent, begins := f.multipartBytes, f.beginCalls
	f.mu.Unlock()

	assert.Zero(t, begins, "the multipart path never touches the staged protocol")
	// Everything the second attempt sent was already sent once. The multipart
	// envelope adds a little, so compare against the payload rather than an
	// exact figure.
	assert.Greater(t, sent, int64(len(data)),
		"pre-change behaviour: the interrupted file is uploaded again from byte 0")
}

// A chunk that arrives short (the connection cut mid-body, but the response
// still gets through) is refused, the offset stays put, and the SAME run
// retries it — no process restart needed.
func TestUpload_ShortChunkRetriedWithinTheRun(t *testing.T) {
	f := newStagedFake(t)
	local, data := stagedTestFile(t, stagedTestChunk*3)

	// Cut once, then let it through: the retry inside putChunkWithRetry must
	// carry the run to completion.
	f.cut(stagedTestChunk, 100)
	go func() {
		// Release the cut as soon as the first attempt has been counted.
		for {
			f.mu.Lock()
			hit := f.wireBytes >= stagedTestChunk+100
			f.mu.Unlock()
			if hit {
				f.clearCut()
				return
			}
		}
	}()

	c := f.client(t.TempDir())
	_, _, err := c.Upload(context.Background(), local, "docs://inbox/")
	require.NoError(t, err)

	s := onlySession(t, f)
	assert.Equal(t, sha256Of(data), sha256Of(s.assembled()))
	f.mu.Lock()
	begins := f.beginCalls
	f.mu.Unlock()
	assert.Equal(t, 1, begins)
}

// Small files keep the one-shot path — it is fine for a 20 KB text file, and
// every existing integration speaks it.
func TestUpload_SmallFileKeepsTheFastPath(t *testing.T) {
	f := newStagedFake(t)
	local, data := stagedTestFile(t, 1024)

	c := f.client(t.TempDir())
	_, _, err := c.Upload(context.Background(), local, "docs://inbox/")
	require.NoError(t, err)

	f.mu.Lock()
	defer f.mu.Unlock()
	assert.Zero(t, f.beginCalls, "a small file must not open a staged session")
	assert.GreaterOrEqual(t, f.multipartBytes, int64(len(data)))
}

// An older filex (or one with no staging directory) answers 404/501 at begin.
// Nothing has been sent at that point, so the whole file is still available to
// the multipart path — the client must fall back rather than fail.
func TestUpload_FallsBackWhenServerHasNoStagedPath(t *testing.T) {
	for _, code := range []int{http.StatusNotFound, http.StatusNotImplemented} {
		t.Run(fmt.Sprint(code), func(t *testing.T) {
			f := newStagedFake(t)
			f.beginStatus = code
			local, data := stagedTestFile(t, stagedTestChunk*3)

			c := f.client(t.TempDir())
			_, _, err := c.Upload(context.Background(), local, "docs://inbox/")
			require.NoError(t, err)

			f.mu.Lock()
			defer f.mu.Unlock()
			assert.Equal(t, 1, f.beginCalls)
			assert.GreaterOrEqual(t, f.multipartBytes, int64(len(data)),
				"the fallback must carry the whole file")
		})
	}
}

// A refusal on the merits (quota, permission) is NOT a reason to push the same
// bytes down the one-shot path — that would spend the upload twice to be
// refused twice.
func TestUpload_RealRefusalIsNotRetriedOnTheFastPath(t *testing.T) {
	f := newStagedFake(t)
	f.beginStatus = http.StatusRequestEntityTooLarge
	local, _ := stagedTestFile(t, stagedTestChunk*3)

	c := f.client(t.TempDir())
	_, _, err := c.Upload(context.Background(), local, "docs://inbox/")
	require.Error(t, err)

	f.mu.Lock()
	defer f.mu.Unlock()
	assert.Zero(t, f.multipartBytes, "a 413 must not be retried as a whole-file POST")
}

// The bookmark is pinned to the exact bytes it was opened for. A local file
// that changed since must start a new session: appending its tail to the old
// head is the one way a resumable upload corrupts data.
func TestUpload_ChangedLocalFileStartsAFreshSession(t *testing.T) {
	f := newStagedFake(t)
	resumeDir := t.TempDir()
	local, _ := stagedTestFile(t, stagedTestChunk*3)

	f.cut(stagedTestChunk, 100)
	c := f.client(resumeDir)
	_, _, err := c.Upload(context.Background(), local, "docs://inbox/")
	require.Error(t, err)
	f.clearCut()

	// Same name, same size, different content and mtime.
	replacement := make([]byte, stagedTestChunk*3)
	rnd := rand.New(rand.NewSource(4242))
	_, _ = rnd.Read(replacement)
	require.NoError(t, os.WriteFile(local, replacement, 0o600))

	_, _, err = c.Upload(context.Background(), local, "docs://inbox/")
	require.NoError(t, err)

	f.mu.Lock()
	begins := f.beginCalls
	var newest *stagedSession
	for _, s := range f.sessions {
		if newest == nil || s.id > newest.id {
			newest = s
		}
	}
	f.mu.Unlock()

	assert.Equal(t, 2, begins, "a changed file must not inherit the old session")
	assert.Equal(t, sha256Of(replacement), sha256Of(newest.assembled()))
}

// UploadTree (`filex upload -r`, and the same code `filex sync` calls per file)
// goes through the resumable path too — the point of putting the decision in
// uploadFile rather than in each command.
func TestUploadTree_UsesTheResumablePathPerFile(t *testing.T) {
	f := newStagedFake(t)
	dir := t.TempDir()
	big := make([]byte, stagedTestChunk*2+11)
	rnd := rand.New(rand.NewSource(99))
	_, _ = rnd.Read(big)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "big.bin"), big, 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "small.txt"), []byte("hello"), 0o600))

	c := f.client(t.TempDir())
	rep, err := c.UploadTree(context.Background(), dir, "docs://inbox/", nil)
	require.NoError(t, err)
	require.Empty(t, rep.Errors)
	assert.Equal(t, 2, rep.Files)

	f.mu.Lock()
	defer f.mu.Unlock()
	assert.Equal(t, 1, f.beginCalls, "exactly the large file goes staged")
	assert.GreaterOrEqual(t, f.multipartBytes, int64(5), "the small one keeps the fast path")
}

// Without a state directory the client still uploads and still resumes inside a
// single run; it only loses the cross-restart part. A missing home directory
// must not disable uploads.
func TestUpload_NoResumeDirStillUploads(t *testing.T) {
	f := newStagedFake(t)
	local, data := stagedTestFile(t, stagedTestChunk*2)

	c := f.client("")
	_, _, err := c.Upload(context.Background(), local, "docs://inbox/")
	require.NoError(t, err)

	s := onlySession(t, f)
	assert.Equal(t, sha256Of(data), sha256Of(s.assembled()))
}
