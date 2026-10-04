package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/e2e"
	"github.com/brf-tech/filex/backend/internal/e2edecrypt"
)

// `filex encrypt` end to end (#95). The claim under test is the task's:
// what the CLI encrypts, `filex decrypt` (and so the browser, which reads the
// same bytes - internal/e2edecrypt is pinned to the browser's fixtures and to
// independent vectors) opens; and a run stopped half-way continues.

const encPW = "a CLI password, long enough"

func runEncrypt(t *testing.T, stdin string, args ...string) (string, string, error) {
	t.Helper()
	cmd := encryptCmd()
	var out, errb bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errb)
	cmd.SetIn(strings.NewReader(stdin))
	cmd.SetArgs(args)
	err := cmd.Execute()
	return out.String(), errb.String(), err
}

func sumHex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func writeTree(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for rel, content := range files {
		p := filepath.Join(root, filepath.FromSlash(rel))
		require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
		require.NoError(t, os.WriteFile(p, []byte(content), 0o644))
	}
}

// plainTreeOf is a folder's files as rel path -> sha256.
func plainTreeOf(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	require.NoError(t, filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		out[filepath.ToSlash(rel)] = sumHex(b)
		return nil
	}))
	return out
}

var sampleFiles = map[string]string{
	"notlar.txt":                 "merhaba",
	"boş.txt":                    "",
	"Faturalar/2024.pdf":         "%PDF 2024",
	"Faturalar/Eski/2019.md":     strings.Repeat("satır\n", 500),
	"Faturalar/Eski/İzmir ödeme": "ödendi",
}

func wantTree() map[string]string {
	out := map[string]string{}
	for rel, c := range sampleFiles {
		out[rel] = sumHex([]byte(c))
	}
	return out
}

// ── a folder on this machine ─────────────────────────────────────────────

func TestEncryptCmd_LocalFolderOpensWithFilexDecrypt(t *testing.T) {
	for _, level := range []string{"1", "2"} {
		t.Run("level "+level, func(t *testing.T) {
			in := filepath.Join(t.TempDir(), "Kasa")
			writeTree(t, in, sampleFiles)
			keyFile := filepath.Join(t.TempDir(), "kasa.key")
			stdout, stderr, err := runEncrypt(t, encPW+"\n", in, "--level", level, "--password-stdin", "--recovery-key-file", keyFile, "-q")
			require.NoError(t, err, stderr)
			require.Contains(t, stdout, "Encrypted 5 file(s)")
			require.NotContains(t, stdout+stderr, encPW, "the password is printed nowhere")
			out := in + "-encrypted"

			key, err := os.ReadFile(keyFile)
			require.NoError(t, err)
			require.NotContains(t, stdout+stderr, strings.TrimSpace(string(key)), "the recovery key went to its file only")
			if fi, err := os.Stat(keyFile); err == nil && os.PathSeparator == '/' {
				require.Equal(t, os.FileMode(0o600), fi.Mode().Perm())
			}

			for _, byRecovery := range []bool{false, true} {
				secret := encPW
				if byRecovery {
					secret = strings.TrimSpace(string(key))
				}
				plain := filepath.Join(t.TempDir(), "plain")
				_, err := e2edecrypt.DecryptTree(out, e2edecrypt.OpenOptions{Out: plain}, secret, byRecovery)
				require.NoError(t, err)
				require.Equal(t, wantTree(), plainTreeOf(t, plain))
			}
		})
	}
}

func TestEncryptCmd_LocalShortPasswordWritesNothing(t *testing.T) {
	in := filepath.Join(t.TempDir(), "Kasa")
	writeTree(t, in, sampleFiles)
	_, _, err := runEncrypt(t, "short\n", in, "--password-stdin")
	require.Error(t, err)
	_, statErr := os.Stat(in + "-encrypted")
	require.True(t, os.IsNotExist(statErr))
	_, statErr = os.Stat(in + "-encrypted.partial")
	require.True(t, os.IsNotExist(statErr))
}

func TestEncryptCmd_TakesNoSecretFlag(t *testing.T) {
	c := encryptCmd()
	for _, name := range []string{"password", "pass", "secret", "key"} {
		require.Nil(t, c.Flags().Lookup(name), "--%s must not exist", name)
	}
}

// ── a folder on a server ─────────────────────────────────────────────────

// fakeFilex is enough of a filex server for `filex encrypt`: raw listings
// that hide the key file and say where an encrypted folder is, ranged
// downloads, conditional multipart uploads, renames, capabilities, cleanup.
// It has no staged uploads (begin answers 404), so every write is one POST.
type fakeFilex struct {
	t     *testing.T
	mu    sync.Mutex
	srv   *httptest.Server
	files map[string][]byte
	dirs  map[string]bool
	mtime map[string]int64
	clock int64
	// converted is every write that said it was a conversion write.
	converted map[string]bool
	expects   map[string]string
	// refuse answers 412 to the next n writes of a path.
	refuse   map[string]int
	cleanups []map[string]any
	escrow   string
	uploads  int
	// e2eAnswer, when set, is the server's encryption policy answer to
	// POST /api/files/e2e/allowed ("allowed", "request", "denied"), with
	// e2eReason; unset, the endpoint is missing (a server older than the
	// policy). e2eAsked is every question it was asked.
	e2eAnswer, e2eReason string
	e2eAsked             []map[string]any
}

func newFakeFilex(t *testing.T) *fakeFilex {
	f := &fakeFilex{t: t, files: map[string][]byte{}, dirs: map[string]bool{"docs://": true}, mtime: map[string]int64{},
		converted: map[string]bool{}, expects: map[string]string{}, refuse: map[string]int{}}
	f.srv = httptest.NewServer(http.HandlerFunc(f.handle))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeFilex) put(p string, b []byte) {
	f.clock++
	f.files[p] = b
	f.mtime[p] = 1_790_000_000_000 + f.clock
	for d := parentWire(p); ; d = parentWire(d) {
		f.dirs[d] = true
		if strings.HasSuffix(d, "://") {
			break
		}
	}
}

func (f *fakeFilex) seed(root string, files map[string]string) {
	f.dirs[root] = true
	for rel, c := range files {
		f.put(root+"/"+rel, []byte(c))
	}
}

func parentWire(p string) string {
	i := strings.LastIndexByte(p, '/')
	if i < 0 || strings.HasSuffix(p[:i+1], "://") {
		return p[:strings.Index(p, "://")+3]
	}
	return p[:i]
}

func baseWire(p string) string { return p[strings.LastIndexByte(p, '/')+1:] }

func (f *fakeFilex) hasMarker(dir string) bool {
	_, ok := f.files[dir+"/"+e2edecrypt.MarkerName]
	return ok
}

func (f *fakeFilex) e2eRoot(dir string) string {
	for d := dir; ; d = parentWire(d) {
		if f.hasMarker(d) {
			return d
		}
		if strings.HasSuffix(d, "://") {
			return ""
		}
	}
}

func (f *fakeFilex) json(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func (f *fakeFilex) handle(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	switch {
	case r.URL.Path == "/api/capabilities":
		esc := map[string]any{"enabled": false}
		if f.escrow != "" {
			esc = map[string]any{"enabled": true, "public_key": f.escrow, "kid": e2edecrypt.EscrowKID(f.escrow)}
		}
		f.json(w, 200, map[string]any{"e2e_escrow": esc})
	case r.URL.Path == "/api/files/upload/begin":
		f.json(w, 404, map[string]string{"error": "no staged uploads here"})
	case r.URL.Path == "/api/files/e2e/allowed" && f.e2eAnswer != "":
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		f.e2eAsked = append(f.e2eAsked, body)
		f.json(w, 200, map[string]any{"encrypt": []string{f.e2eAnswer}, "reasons": []string{f.e2eReason}})
	case r.URL.Path == "/api/files/e2e/cleanup":
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		f.cleanups = append(f.cleanups, body)
		f.json(w, 200, map[string]int{"versions_deleted": 2, "trash_purged": 1})
	case r.URL.Path == "/api/files/manager":
		f.manager(w, r)
	default:
		f.json(w, 404, map[string]string{"error": "not here"})
	}
}

func (f *fakeFilex) manager(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	switch q.Get("action") {
	case "index":
		dir := strings.TrimRight(q.Get("path"), "/")
		if strings.HasSuffix(q.Get("path"), "://") {
			dir = q.Get("path")
		}
		if !f.dirs[dir] {
			f.json(w, 404, map[string]string{"error": "no such folder"})
			return
		}
		var rows []map[string]any
		for d := range f.dirs {
			if d != dir && parentWire(d) == dir {
				row := map[string]any{"path": d, "basename": baseWire(d), "type": "dir"}
				if f.hasMarker(d) {
					row["e2e"] = true
				}
				rows = append(rows, row)
			}
		}
		for p, b := range f.files {
			if parentWire(p) == dir && baseWire(p) != e2edecrypt.MarkerName {
				rows = append(rows, map[string]any{"path": p, "basename": baseWire(p), "type": "file", "size": len(b), "last_modified": f.mtime[p]})
			}
		}
		sort.Slice(rows, func(a, b int) bool { return rows[a]["path"].(string) < rows[b]["path"].(string) })
		resp := map[string]any{"adapter": "docs", "dirname": dir, "files": rows}
		if root := f.e2eRoot(dir); root != "" {
			resp["e2e_root"] = root
		}
		f.json(w, 200, resp)
	case "download":
		b, ok := f.files[q.Get("path")]
		if !ok {
			f.json(w, 404, map[string]string{"error": "no such file"})
			return
		}
		http.ServeContent(w, r, baseWire(q.Get("path")), time.UnixMilli(f.mtime[q.Get("path")]), bytes.NewReader(b))
	case "upload":
		require.NoError(f.t, r.ParseMultipartForm(64<<20))
		fh := r.MultipartForm.File["file[]"][0]
		src, err := fh.Open()
		require.NoError(f.t, err)
		data, err := io.ReadAll(src)
		require.NoError(f.t, err)
		target := strings.TrimRight(r.FormValue("path"), "/") + "/" + fh.Filename
		expect := r.FormValue("expect")
		_, exists := f.files[target]
		if f.refuse[target] > 0 {
			f.refuse[target]--
			f.json(w, 412, map[string]string{"error": "changed", "code": "PRECONDITION_FAILED"})
			return
		}
		switch {
		case expect == "none" && exists,
			expect != "" && expect != "none" && expect != fmt.Sprintf("%d:%d", len(f.files[target]), f.mtime[target]):
			f.json(w, 412, map[string]string{"error": "changed", "code": "PRECONDITION_FAILED"})
			return
		}
		if r.FormValue("e2e_convert") == "1" {
			f.converted[target] = true
			f.expects[target] = expect
		}
		f.uploads++
		f.put(target, data)
		f.json(w, 200, map[string]any{"files": []any{}})
	case "rename":
		var body struct{ Path, Item, Name string }
		require.NoError(f.t, json.NewDecoder(r.Body).Decode(&body))
		to := strings.TrimRight(body.Path, "/") + "/" + body.Name
		if _, taken := f.files[to]; taken || f.dirs[to] {
			f.json(w, 409, map[string]string{"error": "taken"})
			return
		}
		for p, b := range f.files {
			if p == body.Item || strings.HasPrefix(p, body.Item+"/") {
				delete(f.files, p)
				f.files[to+strings.TrimPrefix(p, body.Item)] = b
				f.mtime[to+strings.TrimPrefix(p, body.Item)] = f.mtime[p]
			}
		}
		for d := range f.dirs {
			if d == body.Item || strings.HasPrefix(d, body.Item+"/") {
				delete(f.dirs, d)
				f.dirs[to+strings.TrimPrefix(d, body.Item)] = true
			}
		}
		f.json(w, 200, map[string]any{"files": []any{}})
	default:
		f.json(w, 400, map[string]string{"error": "unknown action"})
	}
}

// dump writes the server's copy of a folder to disk, key file included - a
// download of the encrypted folder.
func (f *fakeFilex) dump(t *testing.T, root string) string {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	out := filepath.Join(t.TempDir(), "download")
	require.NoError(t, os.MkdirAll(out, 0o755))
	for d := range f.dirs {
		if strings.HasPrefix(d, root+"/") {
			require.NoError(t, os.MkdirAll(filepath.Join(out, filepath.FromSlash(strings.TrimPrefix(d, root+"/"))), 0o755))
		}
	}
	for p, b := range f.files {
		if strings.HasPrefix(p, root+"/") {
			dst := filepath.Join(out, filepath.FromSlash(strings.TrimPrefix(p, root+"/")))
			require.NoError(t, os.MkdirAll(filepath.Dir(dst), 0o755))
			require.NoError(t, os.WriteFile(dst, b, 0o644))
		}
	}
	return out
}

func (f *fakeFilex) marker(t *testing.T, root string) *e2edecrypt.Marker {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	b, ok := f.files[root+"/"+e2edecrypt.MarkerName]
	require.True(t, ok, "no key file at %s", root)
	m, err := e2edecrypt.ParseMarker(b)
	require.NoError(t, err)
	return m
}

func (f *fakeFilex) remote(t *testing.T, stdin string, args ...string) (string, string, error) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", t.TempDir())
	return runEncrypt(t, stdin, append(args, "--url", f.srv.URL, "--token", "tok", "-q")...)
}

func decryptDump(t *testing.T, f *fakeFilex, root, secret string, recovery bool) map[string]string {
	t.Helper()
	plain := filepath.Join(t.TempDir(), "plain")
	_, err := e2edecrypt.DecryptTree(f.dump(t, root), e2edecrypt.OpenOptions{Out: plain}, secret, recovery)
	require.NoError(t, err)
	return plainTreeOf(t, plain)
}

func TestEncryptCmd_ServerFolderInPlace(t *testing.T) {
	f := newFakeFilex(t)
	f.seed("docs://Kasa", sampleFiles)
	listed := map[string]string{}
	for p, b := range f.files {
		listed[p] = fmt.Sprintf("%d:%d", len(b), f.mtime[p])
	}
	keyFile := filepath.Join(t.TempDir(), "kasa.key")
	stdout, stderr, err := f.remote(t, encPW+"\n", "docs://Kasa", "--password-stdin", "--recovery-key-file", keyFile)
	require.NoError(t, err, stderr)
	require.Contains(t, stdout, "Encrypted docs://Kasa: 5 file(s) now")

	m := f.marker(t, "docs://Kasa")
	require.Equal(t, 2, m.V, "the conversion finished: back to a v2 key file")
	require.False(t, m.ConvPending)
	for rel := range sampleFiles {
		p := "docs://Kasa/" + rel
		require.True(t, e2edecrypt.HasMagic(f.files[p]), "%s is still plaintext", p)
		require.True(t, f.converted[p], "%s was not written as a conversion write", p)
		require.Equal(t, listed[p], f.expects[p], "%s was not written on the listing's condition", p)
	}
	require.Equal(t, []map[string]any{{"path": "docs://Kasa", "versions": true, "trash": true}}, f.cleanups)

	key, err := os.ReadFile(keyFile)
	require.NoError(t, err)
	require.Equal(t, wantTree(), decryptDump(t, f, "docs://Kasa", encPW, false))
	require.Equal(t, wantTree(), decryptDump(t, f, "docs://Kasa", strings.TrimSpace(string(key)), true))
}

func TestEncryptCmd_ServerFolderContinuesAfterARefusedWrite(t *testing.T) {
	f := newFakeFilex(t)
	f.seed("docs://Kasa", sampleFiles)
	f.refuse["docs://Kasa/Faturalar/2024.pdf"] = 1
	_, stderr, err := f.remote(t, encPW+"\n", "docs://Kasa", "--password-stdin", "--recovery-key-file", filepath.Join(t.TempDir(), "k"))
	require.Error(t, err)
	require.Equal(t, exitEncryptIncomplete, exitCode(err))
	require.Contains(t, stderr, "Faturalar/2024.pdf")
	m := f.marker(t, "docs://Kasa")
	require.True(t, m.ConvPending, "the conversion stays open")
	require.Equal(t, "%PDF 2024", string(f.files["docs://Kasa/Faturalar/2024.pdf"]))
	require.Empty(t, f.cleanups, "nothing is cleaned up before the folder is whole")
	before := f.uploads

	stdout, stderr, err := f.remote(t, encPW+"\n", "docs://Kasa", "--password-stdin")
	require.NoError(t, err, stderr)
	require.Contains(t, stdout, "1 file(s) now, 4 already encrypted")
	require.Equal(t, before+2, f.uploads, "one file, and the key file without `conv`")
	require.False(t, f.marker(t, "docs://Kasa").ConvPending)
	require.Len(t, f.cleanups, 1)
	require.Equal(t, wantTree(), decryptDump(t, f, "docs://Kasa", encPW, false))
}

func TestEncryptCmd_ServerFolderAtLevel2HidesTheNames(t *testing.T) {
	f := newFakeFilex(t)
	f.seed("docs://Kasa", sampleFiles)
	_, stderr, err := f.remote(t, encPW+"\n", "docs://Kasa", "--level", "2", "--password-stdin", "--recovery-key-file", filepath.Join(t.TempDir(), "k"))
	require.NoError(t, err, stderr)
	m := f.marker(t, "docs://Kasa")
	require.Equal(t, 3, m.V)
	require.Equal(t, []string{"names"}, m.Req)
	require.False(t, m.Names.Pending)
	for p := range f.files {
		for _, word := range []string{"notlar", "Fatura", "Eski", "İzmir", "2019", "boş"} {
			require.NotContains(t, p, word, "a plaintext name is left on the server")
		}
	}
	require.Equal(t, wantTree(), decryptDump(t, f, "docs://Kasa", encPW, false))
}

func TestEncryptCmd_ServerFolderHoldingAnEncryptedFolderIsLeftAlone(t *testing.T) {
	f := newFakeFilex(t)
	f.seed("docs://Kasa", sampleFiles)
	f.put("docs://Kasa/Faturalar/Eski/"+e2edecrypt.MarkerName, []byte(`{"v":2}`))
	_, _, err := f.remote(t, encPW+"\n", "docs://Kasa", "--password-stdin")
	require.Error(t, err)
	require.Contains(t, err.Error(), "cannot be nested")
	require.False(t, f.hasMarker("docs://Kasa"))
	require.Zero(t, f.uploads)
	require.Equal(t, "merhaba", string(f.files["docs://Kasa/notlar.txt"]))
}

// The server's encryption policy is asked BEFORE the password, and its
// refusal is said in words with nothing written: a refusal at the key file
// would come after somebody typed a password twice for nothing.
func TestEncryptCmd_TheServersPolicyIsAskedBeforeAnythingIsWritten(t *testing.T) {
	for _, c := range []struct {
		answer, reason, says string
	}{
		{"denied", "policy_off", "an administrator has switched encryption off"},
		{"denied", "tenant_disabled", "the platform operator has switched encryption off"},
		{"denied", "permission", "your role does not allow encrypting here"},
		{"request", "approval_required", "needs an administrator's approval first"},
	} {
		t.Run(c.answer+"/"+c.reason, func(t *testing.T) {
			f := newFakeFilex(t)
			f.seed("docs://Kasa", sampleFiles)
			f.e2eAnswer, f.e2eReason = c.answer, c.reason
			_, _, err := f.remote(t, encPW+"\n", "docs://Kasa", "--password-stdin")
			require.Error(t, err)
			require.Contains(t, err.Error(), c.says)
			require.Contains(t, err.Error(), "nothing was changed")
			require.False(t, f.hasMarker("docs://Kasa"), "a key file was written")
			require.Zero(t, f.uploads)
			require.Len(t, f.e2eAsked, 1)
			items, _ := f.e2eAsked[0]["items"].([]any)
			require.Len(t, items, 1)
			require.Equal(t, map[string]any{"path": "docs://Kasa", "kind": "folder"}, items[0], "the folder, encrypted where it is")
		})
	}

	// Allowed, it goes on as before.
	f := newFakeFilex(t)
	f.seed("docs://Kasa", sampleFiles)
	f.e2eAnswer = "allowed"
	_, stderr, err := f.remote(t, encPW+"\n", "docs://Kasa", "--password-stdin")
	require.NoError(t, err, stderr)
	require.True(t, f.hasMarker("docs://Kasa"))
}

func TestEncryptCmd_ServerFolderAlreadyEncryptedIsNothingToDo(t *testing.T) {
	f := newFakeFilex(t)
	f.seed("docs://Kasa", sampleFiles)
	_, _, err := f.remote(t, encPW+"\n", "docs://Kasa", "--password-stdin", "--recovery-key-file", filepath.Join(t.TempDir(), "k"))
	require.NoError(t, err)
	n := f.uploads
	stdout, _, err := f.remote(t, "", "docs://Kasa", "--password-stdin")
	require.NoError(t, err)
	require.Contains(t, stdout, "already an encrypted folder")
	require.Equal(t, n, f.uploads)
}

func TestEncryptCmd_ServerFolderResumeWithTheWrongPasswordExitsFive(t *testing.T) {
	f := newFakeFilex(t)
	f.seed("docs://Kasa", sampleFiles)
	f.refuse["docs://Kasa/notlar.txt"] = 1
	_, _, err := f.remote(t, encPW+"\n", "docs://Kasa", "--password-stdin", "--recovery-key-file", filepath.Join(t.TempDir(), "k"))
	require.Equal(t, exitEncryptIncomplete, exitCode(err))
	n := f.uploads
	_, _, err = f.remote(t, "not the password at all\n", "docs://Kasa", "--password-stdin")
	require.Equal(t, exitDecryptWrongSecret, exitCode(err))
	require.Equal(t, n, f.uploads, "nothing written with a wrong password")
}

// The recovery key is shown once: a --recovery-key-file that is already there
// is refused BEFORE the key file goes up, or the new key would have nowhere to
// go and an older one would be overwritten.
func TestEncryptCmd_AnExistingRecoveryKeyFileIsRefusedBeforeAnythingIsWritten(t *testing.T) {
	f := newFakeFilex(t)
	f.seed("docs://Kasa", sampleFiles)
	keyFile := filepath.Join(t.TempDir(), "kasa.key")
	require.NoError(t, os.WriteFile(keyFile, []byte("an older key\n"), 0o600))
	_, _, err := f.remote(t, encPW+"\n", "docs://Kasa", "--password-stdin", "--recovery-key-file", keyFile)
	require.Error(t, err)
	require.Zero(t, f.uploads)
	require.False(t, f.hasMarker("docs://Kasa"))
	b, err := os.ReadFile(keyFile)
	require.NoError(t, err)
	require.Equal(t, "an older key\n", string(b))
}

func TestEncryptCmd_ServerFolderKeepFlagsReachTheCleanup(t *testing.T) {
	f := newFakeFilex(t)
	f.seed("docs://Kasa", sampleFiles)
	_, stderr, err := f.remote(t, encPW+"\n", "docs://Kasa", "--password-stdin", "--keep-versions", "--keep-trash", "--recovery-key-file", filepath.Join(t.TempDir(), "k"))
	require.NoError(t, err, stderr)
	require.Equal(t, []map[string]any{{"path": "docs://Kasa", "versions": false, "trash": false}}, f.cleanups)
}

func TestEncryptCmd_ServerEscrowKeySealsASlotAndIsSaidFirst(t *testing.T) {
	pub, _, err := e2e.GenerateEscrowKeyPair(2048)
	require.NoError(t, err)
	f := newFakeFilex(t)
	f.escrow = pub
	f.seed("docs://Kasa", sampleFiles)
	_, stderr, err := f.remote(t, encPW+"\n", "docs://Kasa", "--password-stdin", "--recovery-key-file", filepath.Join(t.TempDir(), "k"))
	require.NoError(t, err, stderr)
	require.Contains(t, stderr, "key escrow on")
	var raw struct {
		Esc *struct {
			KID string `json:"kid"`
		} `json:"esc"`
	}
	require.NoError(t, json.Unmarshal(f.files["docs://Kasa/"+e2edecrypt.MarkerName], &raw))
	require.NotNil(t, raw.Esc)
	require.Equal(t, e2edecrypt.EscrowKID(pub), raw.Esc.KID)
}
