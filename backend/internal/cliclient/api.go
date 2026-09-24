package cliclient

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/brf-tech/filex/backend/internal/regfile"
)

// managerPath is the server's combined browse/mutate endpoint. Every verb
// used here already exists on the server — the CLI adds no new API.
const managerPath = "/api/files/manager"

// ───────────────────────── login ─────────────────────────

// LoginResponse is the subset of POST /api/auth/login the CLI needs.
type LoginResponse struct {
	Token string `json:"token"`
	Raw   []byte `json:"-"`
}

// Login exchanges email+password (and an optional TOTP code) for a
// session token. Call on a token-less Client.
func (c *Client) Login(ctx context.Context, email, password, totp string) (*LoginResponse, error) {
	body, err := json.Marshal(map[string]string{
		"email":    email,
		"password": password,
		"totp":     totp,
	})
	if err != nil {
		return nil, err
	}
	req, err := c.newRequest(ctx, http.MethodPost, "/api/auth/login", nil, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	raw, err := c.doJSON(req)
	if err != nil {
		var ae *APIError
		if errors.As(err, &ae) {
			var extra struct {
				TotpRequired bool `json:"totp_required"`
			}
			_ = json.Unmarshal(ae.Body, &extra)
			if extra.TotpRequired {
				return nil, fmt.Errorf("%w (this account has two-factor auth — pass --totp <code>)", err)
			}
		}
		return nil, err
	}
	var lr LoginResponse
	if err := json.Unmarshal(raw, &lr); err != nil {
		return nil, fmt.Errorf("parse login response: %w", err)
	}
	if lr.Token == "" {
		return nil, errors.New("login response carried no token")
	}
	lr.Raw = raw
	return &lr, nil
}

// ───────────────────────── ls ─────────────────────────

// ListEntry is one row of a directory listing (server FileNode shape).
type ListEntry struct {
	Path         string `json:"path"`
	Basename     string `json:"basename"`
	Type         string `json:"type"` // "file" | "dir"
	Extension    string `json:"extension"`
	Size         int64  `json:"size"`
	MimeType     string `json:"mime_type"`
	LastModified int64  `json:"last_modified"` // Unix millis; 0 = unknown
}

// ListResult is GET /api/files/manager?action=index.
type ListResult struct {
	Adapter  string      `json:"adapter"`
	Storages []string    `json:"storages"`
	Dirname  string      `json:"dirname"`
	ReadOnly bool        `json:"read_only"`
	Files    []ListEntry `json:"files"`
	Raw      []byte      `json:"-"`
}

// List returns the directory listing at remote (`adapter://rel`). An
// empty remote asks the server for its default view — useful only for
// discovering the Storages slice (adapter names).
func (c *Client) List(ctx context.Context, remote string) (*ListResult, error) {
	q := url.Values{}
	q.Set("action", "index")
	if remote != "" {
		rp, err := ParseRemotePath(remote)
		if err != nil {
			return nil, err
		}
		q.Set("path", rp.String())
	}
	req, err := c.newRequest(ctx, http.MethodGet, managerPath, q, nil)
	if err != nil {
		return nil, err
	}
	raw, err := c.doJSON(req)
	if err != nil {
		return nil, err
	}
	var res ListResult
	if err := json.Unmarshal(raw, &res); err != nil {
		return nil, fmt.Errorf("parse listing: %w", err)
	}
	res.Raw = raw
	return &res, nil
}

// remoteIsDir probes whether p is a browsable directory. The server's
// index action 404s on files and phantom prefixes (Stat-confirmed), so a
// clean 200 is a reliable "directory" signal. Any error → not a dir.
func (c *Client) remoteIsDir(ctx context.Context, p RemotePath) bool {
	_, err := c.List(ctx, p.String())
	return err == nil
}

// ───────────────────────── upload ─────────────────────────

// Upload streams localPath to the server. remote may be a destination
// folder (`docs://reports/` — trailing slash or an existing dir) or a
// full target path (`docs://reports/renamed.pdf`); an existing remote
// folder wins, otherwise the last segment becomes the uploaded filename.
// Nothing is ever buffered in memory, and a large file is resumable — see
// uploadFile.
func (c *Client) Upload(ctx context.Context, localPath, remote string) (RemotePath, []byte, error) {
	rp, err := ParseRemotePath(remote)
	if err != nil {
		return RemotePath{}, nil, err
	}
	if fi, err := os.Stat(localPath); err == nil && fi.IsDir() {
		return RemotePath{}, nil, fmt.Errorf("%s is a directory — upload takes a single file (pass -r/--recursive to upload the folder)", localPath)
	}

	destDir := rp
	name := filepath.Base(localPath)
	if !rp.IsRoot() && !strings.HasSuffix(remote, "/") && !c.remoteIsDir(ctx, rp) {
		destDir = rp.Dir()
		name = rp.Base()
	}

	raw, err := c.uploadFile(ctx, destDir, name, localPath, "")
	if err != nil {
		return RemotePath{}, nil, err
	}
	return destDir.Join(name), raw, nil
}

// ErrPreconditionFailed is a conditional upload the server refused because
// the target is no longer what the caller last saw (HTTP 412, code
// PRECONDITION_FAILED). Nothing was written.
var ErrPreconditionFailed = errors.New("the file changed on the server since it was listed")

// UploadTo sends localPath to destDir/name with no destination probing and an
// optional overwrite precondition — the call `filex sync` makes.
//
// expect is "" (no precondition), "none" (the target must not exist yet) or
// "<size>:<last_modified-ms>" (the target must still be exactly what a listing
// reported). A refusal comes back as ErrPreconditionFailed. An older server
// ignores the field and writes unconditionally — no worse than before.
//
// ⚠ No remoteIsDir probe, unlike Upload: the caller already knows the target
// is a file path, and the probe is a full listing round-trip in front of every
// file — on a server behind a CDN proxy (~0.35 s per request, measured) that
// was a third of the time a one-line edit took to leave the machine.
func (c *Client) UploadTo(ctx context.Context, localPath string, destDir RemotePath, name, expect string) ([]byte, error) {
	return c.uploadFile(ctx, destDir, name, localPath, expect)
}

// preconditionErr maps a 412 onto ErrPreconditionFailed.
func preconditionErr(err error) error {
	var ae *APIError
	if errors.As(err, &ae) && ae.Status == http.StatusPreconditionFailed {
		return fmt.Errorf("%w (%v)", ErrPreconditionFailed, err)
	}
	return err
}

// uploadFile sends one local file into destDir under name. The shared core
// behind Upload, UploadTree and `filex sync` — no destination probing here, the
// caller already resolved destDir.
//
// Anything at or above StagedThreshold goes over the resumable staged protocol
// (staged.go): the bytes land in filex's own staging area, the offset survives
// a dropped connection, and a bookmark on disk lets the NEXT process continue
// the same session. Small files keep the one-shot multipart POST, which is fine
// for a 20 KB text file and is what every existing integration speaks.
//
// The decision lives here, in the one function all three commands call, rather
// than in each command: `filex sync` is the case that matters most and it never
// touches Upload at all.
func (c *Client) uploadFile(ctx context.Context, destDir RemotePath, name, localPath, expect string) ([]byte, error) {
	fi, err := os.Stat(localPath)
	if err != nil {
		return nil, err
	}
	if th := c.stagedThreshold(); th > 0 && fi.Size() >= th {
		raw, serr := c.uploadStaged(ctx, destDir, name, localPath, fi.Size(), fi.ModTime(), expect)
		if serr == nil {
			return raw, nil
		}
		if !errors.Is(serr, errStagedUnsupported) {
			return nil, preconditionErr(serr)
		}
		// An older server, or one with no staging configured. Fall through —
		// nothing has been sent yet, so the multipart POST still has the whole
		// file to work with.
	}
	raw, err := c.uploadMultipart(ctx, destDir, name, localPath, expect)
	return raw, preconditionErr(err)
}

// uploadMultipart streams one file as a single multipart POST. The body is
// piped, so large files never load into memory — but there is no resume: a
// dropped connection costs the whole file. That is why anything large goes
// through uploadStaged instead.
func (c *Client) uploadMultipart(ctx context.Context, destDir RemotePath, name, localPath, expect string) ([]byte, error) {
	// regfile, not os.Open: a named pipe would hold this open forever (#38).
	f, err := regfile.Open(localPath)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	pr, pw := io.Pipe()
	mw := multipart.NewWriter(pw)
	go func() {
		err := func() error {
			if err := mw.WriteField("path", destDir.String()); err != nil {
				return err
			}
			if expect != "" {
				if err := mw.WriteField("expect", expect); err != nil {
					return err
				}
			}
			part, err := mw.CreateFormFile("file[]", name)
			if err != nil {
				return err
			}
			if _, err := io.Copy(part, c.UpLimit.Reader(ctx, f)); err != nil {
				return err
			}
			return mw.Close()
		}()
		pw.CloseWithError(err)
	}()

	q := url.Values{}
	q.Set("action", "upload")
	req, err := c.newRequest(ctx, http.MethodPost, managerPath, q, pr)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	return c.doJSON(req)
}

// ───────────────────────── download ─────────────────────────

// SizeMismatchError is a download whose body is not the length the caller's
// listing reported. When the server DECLARES the other length up front
// (Content-Length, or the total of a Content-Range) not one byte reaches the
// writer; a body that only turns out short or long at its end has reached it,
// and the caller — which writes to a temporary file — throws it away.
type SizeMismatchError struct {
	Remote string
	Want   int64 // what the listing said
	Got    int64 // what the server declared or sent
}

func (e *SizeMismatchError) Error() string {
	return fmt.Sprintf("%s: the server listed %d bytes but sent %d; nothing was written", e.Remote, e.Want, e.Got)
}

// Download streams the remote file into w and returns the byte count.
//
// ⚠ Only a body that IS the file is ever copied: a 200, or a 206 answering this
// request's own `bytes=0-` with the whole object. Servers from v0.20 to v0.42
// answered an unranged download of a big file on a slow storage with
// `202 {"state":"preparing",…}`, and the old check here — anything 2xx is the
// file — wrote that JSON to disk under the file's name. The sync engine then
// saw a locally edited file and uploaded the JSON over the real one: 45 files
// of 70–290 MB on one deployment, unrecoverable from version history.
//
// `bytes=0-` is the request no server version answers with 202 (a Range comes
// from a client already committed to a body), so the CLI never waits on a
// server-side copy of a file it reads exactly once anyway. Any other 2xx is
// still refused, and nothing of it is written.
func (c *Client) Download(ctx context.Context, remote string, w io.Writer) (int64, error) {
	return c.DownloadSized(ctx, remote, w, -1)
}

// DownloadSized is Download for a caller that knows how big the file is — the
// sync engine does, from the listing it planned against. want < 0 means
// unknown. A body of any other length is a *SizeMismatchError, which on its
// own would have caught the 202 above: the listing said 151,983,227 bytes and
// the body was 88.
func (c *Client) DownloadSized(ctx context.Context, remote string, w io.Writer, want int64) (int64, error) {
	rp, err := ParseRemotePath(remote)
	if err != nil {
		return 0, err
	}
	if rp.IsRoot() {
		return 0, errors.New("download needs a file path, not a storage root")
	}
	q := url.Values{}
	q.Set("action", "download")
	q.Set("path", rp.String())
	req, err := c.newRequest(ctx, http.MethodGet, managerPath, q, nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Range", "bytes=0-")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	declared := int64(-1) // what the server says it is sending; -1 = not said
	switch resp.StatusCode {
	case http.StatusOK:
		// The server ignored the Range (a driver that cannot seek, or an
		// older build) and sends the whole object — which is what we asked for.
		declared = resp.ContentLength
	case http.StatusPartialContent:
		cr := resp.Header.Get("Content-Range")
		total, ok := wholeObjectRange(cr)
		if !ok {
			return 0, fmt.Errorf("%s: the server answered bytes=0- with %q instead of the whole file; nothing was written", remote, cr)
		}
		declared = total
	case http.StatusRequestedRangeNotSatisfiable:
		// bytes=0- of an EMPTY object: there is no byte 0 to start from. That
		// is an empty file — but only when the server says so and the caller
		// was not expecting bytes.
		if total, ok := unsatisfiedRangeTotal(resp.Header.Get("Content-Range")); ok && total == 0 && want <= 0 {
			return 0, nil
		}
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return 0, apiErrorFrom(resp.StatusCode, b)
	default:
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		ae := apiErrorFrom(resp.StatusCode, b)
		if resp.StatusCode == http.StatusAccepted {
			ae.Message = "the server is still preparing this file and sent a status report instead of it (" + ae.Message + "); nothing was written"
		}
		return 0, ae
	}
	if want >= 0 && declared >= 0 && declared != want {
		return 0, &SizeMismatchError{Remote: remote, Want: want, Got: declared}
	}
	n, err := io.Copy(w, c.DownLimit.Reader(ctx, resp.Body))
	if err != nil {
		return n, err
	}
	if want >= 0 && n != want {
		return n, &SizeMismatchError{Remote: remote, Want: want, Got: n}
	}
	if declared >= 0 && n != declared {
		return n, fmt.Errorf("%s: the transfer ended after %d of %d bytes", remote, n, declared)
	}
	return n, nil
}

// wholeObjectRange reads a 206's `Content-Range: bytes 0-(N-1)/N` and returns
// N. Anything else — a window that does not start at 0, stops short of the
// end, or has an unknown total — is not the file.
func wholeObjectRange(cr string) (int64, bool) {
	spec, ok := strings.CutPrefix(strings.TrimSpace(cr), "bytes ")
	if !ok {
		return 0, false
	}
	span, totalStr, ok := strings.Cut(spec, "/")
	if !ok {
		return 0, false
	}
	startStr, endStr, ok := strings.Cut(span, "-")
	if !ok {
		return 0, false
	}
	start, err1 := strconv.ParseInt(strings.TrimSpace(startStr), 10, 64)
	end, err2 := strconv.ParseInt(strings.TrimSpace(endStr), 10, 64)
	total, err3 := strconv.ParseInt(strings.TrimSpace(totalStr), 10, 64)
	if err1 != nil || err2 != nil || err3 != nil {
		return 0, false
	}
	if start != 0 || total <= 0 || end != total-1 {
		return 0, false
	}
	return total, true
}

// unsatisfiedRangeTotal reads a 416's `Content-Range: bytes */N`.
func unsatisfiedRangeTotal(cr string) (int64, bool) {
	spec, ok := strings.CutPrefix(strings.TrimSpace(cr), "bytes */")
	if !ok {
		return 0, false
	}
	n, err := strconv.ParseInt(strings.TrimSpace(spec), 10, 64)
	if err != nil || n < 0 {
		return 0, false
	}
	return n, true
}

// ───────────────────────── mkdir / rm ─────────────────────────

// Mkdir creates the folder named by the last segment of remote inside
// its parent (server verb: newfolder).
func (c *Client) Mkdir(ctx context.Context, remote string) ([]byte, error) {
	rp, err := ParseRemotePath(remote)
	if err != nil {
		return nil, err
	}
	if rp.IsRoot() {
		return nil, errors.New("mkdir needs a folder path below the storage root")
	}
	return c.postManager(ctx, "newfolder", map[string]any{
		"path": rp.Dir().String(),
		"name": rp.Base(),
	})
}

// Remove sends the item to the server-side trash (server verb: delete —
// filex soft-deletes into `.filex-trash`, restorable from the panel).
func (c *Client) Remove(ctx context.Context, remote string) ([]byte, error) {
	rp, err := ParseRemotePath(remote)
	if err != nil {
		return nil, err
	}
	if rp.IsRoot() {
		return nil, errors.New("refusing to delete a storage root")
	}
	return c.postManager(ctx, "delete", map[string]any{
		"path":  rp.Dir().String(),
		"items": []map[string]string{{"path": rp.String()}},
	})
}

// ───────────────────────── mv ─────────────────────────

// Move implements Unix-mv semantics on top of the server's move/rename
// verbs. dst may be an existing directory (item moves into it, keeping
// its name) or a target path (rename, or move+rename across dirs — two
// wire calls, the manager API has no combined verb). Cross-adapter moves
// are rejected client-side; the server refuses them anyway.
func (c *Client) Move(ctx context.Context, src, dst string) (RemotePath, []byte, error) {
	sp, err := ParseRemotePath(src)
	if err != nil {
		return RemotePath{}, nil, err
	}
	dp, err := ParseRemotePath(dst)
	if err != nil {
		return RemotePath{}, nil, err
	}
	if sp.Adapter != dp.Adapter {
		return RemotePath{}, nil, errors.New("cross-adapter move is not supported by the server")
	}
	if sp.IsRoot() {
		return RemotePath{}, nil, errors.New("cannot move a storage root")
	}

	// Destination directory form: root, trailing slash, or an existing dir.
	if dp.IsRoot() || strings.HasSuffix(dst, "/") || c.remoteIsDir(ctx, dp) {
		raw, err := c.moveInto(ctx, sp, dp)
		return dp.Join(sp.Base()), raw, err
	}

	// Same parent → pure rename.
	if dp.Dir().Rel == sp.Dir().Rel {
		raw, err := c.rename(ctx, sp, dp.Base())
		return dp, raw, err
	}

	// Different parent + different target name → move, then rename.
	raw, err := c.moveInto(ctx, sp, dp.Dir())
	if err != nil {
		return RemotePath{}, raw, err
	}
	moved := dp.Dir().Join(sp.Base())
	if moved.Base() != dp.Base() {
		raw, err = c.rename(ctx, moved, dp.Base())
		if err != nil {
			return moved, raw, fmt.Errorf("moved to %s but rename failed: %w", moved.String(), err)
		}
	}
	return dp, raw, nil
}

// moveInto issues the manager move verb (dest keeps the item basename).
func (c *Client) moveInto(ctx context.Context, item, destDir RemotePath) ([]byte, error) {
	return c.postManager(ctx, "move", map[string]any{
		"path":  destDir.String(),
		"items": []map[string]string{{"path": item.String()}},
	})
}

// rename issues the manager rename verb (same-dir name change).
func (c *Client) rename(ctx context.Context, item RemotePath, newName string) ([]byte, error) {
	return c.postManager(ctx, "rename", map[string]any{
		"path": item.Dir().String(),
		"item": item.String(),
		"name": newName,
	})
}

// postManager POSTs a JSON body to /api/files/manager?action=<verb>.
func (c *Client) postManager(ctx context.Context, action string, body any) ([]byte, error) {
	b, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	q := url.Values{}
	q.Set("action", action)
	req, err := c.newRequest(ctx, http.MethodPost, managerPath, q, bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	return c.doJSON(req)
}

// ───────────────────────── search ─────────────────────────

// SearchHit is one result row from /api/files/search (node fields plus
// the v0.2 content-search additions).
type SearchHit struct {
	ID        int64  `json:"id"`
	StorageID int64  `json:"storage_id"`
	Name      string `json:"name"`
	Path      string `json:"path"`
	Type      string `json:"type"`
	Size      int64  `json:"size"`
	Snippet   string `json:"snippet"`
	Matched   string `json:"matched"` // "name" | "content" | "both"
}

// SearchResult is the /api/files/search envelope.
type SearchResult struct {
	Results []SearchHit `json:"results"`
	Raw     []byte      `json:"-"`
}

// Search queries the server-side index. scope is "name", "content" or
// "all" ("" = server default, all). storageID 0 searches every storage.
func (c *Client) Search(ctx context.Context, query, scope string, storageID int64, limit int) (*SearchResult, error) {
	switch scope {
	case "", "name", "content", "all":
	default:
		return nil, fmt.Errorf("bad --scope %q: want name, content or all", scope)
	}
	q := url.Values{}
	q.Set("q", query)
	if scope != "" {
		q.Set("scope", scope)
	}
	if storageID > 0 {
		q.Set("storage_id", strconv.FormatInt(storageID, 10))
	}
	if limit > 0 {
		q.Set("limit", strconv.Itoa(limit))
	}
	req, err := c.newRequest(ctx, http.MethodGet, "/api/files/search", q, nil)
	if err != nil {
		return nil, err
	}
	raw, err := c.doJSON(req)
	if err != nil {
		return nil, err
	}
	var res SearchResult
	if err := json.Unmarshal(raw, &res); err != nil {
		return nil, fmt.Errorf("parse search response: %w", err)
	}
	res.Raw = raw
	return &res, nil
}

// ───────────────────────── share ─────────────────────────

// ShareResult is the nested `share` object of POST /api/files/share.
type ShareResult struct {
	URL       string     `json:"url"`
	Token     string     `json:"token"`
	HasPin    bool       `json:"has_pin"`
	PIN       string     `json:"password_pin"` // only set when the server generated one
	ExpiresAt *time.Time `json:"expires_at"`
	Raw       []byte     `json:"-"`
}

// Share mints a public download link for remote. pin=true asks the
// server to generate an unlock PIN (returned once, in PIN); expiresDays
// > 0 sets the expiry that many days from now.
func (c *Client) Share(ctx context.Context, remote string, pin bool, expiresDays int) (*ShareResult, error) {
	rp, err := ParseRemotePath(remote)
	if err != nil {
		return nil, err
	}
	if rp.IsRoot() {
		return nil, errors.New("share needs a file or folder path, not a storage root")
	}
	body := map[string]any{"path": rp.String()}
	if pin {
		body["password"] = true
	}
	if expiresDays > 0 {
		body["expires_at"] = time.Now().Add(time.Duration(expiresDays) * 24 * time.Hour).UTC().Format(time.RFC3339)
	}
	b, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	req, err := c.newRequest(ctx, http.MethodPost, "/api/files/share", nil, bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	raw, err := c.doJSON(req)
	if err != nil {
		return nil, err
	}
	var envelope struct {
		Share ShareResult `json:"share"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return nil, fmt.Errorf("parse share response: %w", err)
	}
	if envelope.Share.URL == "" {
		return nil, errors.New("share response carried no URL")
	}
	res := envelope.Share
	res.Raw = raw
	return &res, nil
}
