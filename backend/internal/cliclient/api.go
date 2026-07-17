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
// The multipart body is piped, so large files never load into memory.
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

	raw, err := c.uploadFile(ctx, destDir, name, localPath)
	if err != nil {
		return RemotePath{}, nil, err
	}
	return destDir.Join(name), raw, nil
}

// uploadFile streams one local file into destDir under name. The shared
// core behind Upload and UploadTree — no destination probing here, the
// caller already resolved destDir.
func (c *Client) uploadFile(ctx context.Context, destDir RemotePath, name, localPath string) ([]byte, error) {
	f, err := os.Open(localPath)
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
			part, err := mw.CreateFormFile("file[]", name)
			if err != nil {
				return err
			}
			if _, err := io.Copy(part, f); err != nil {
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

// Download streams the remote file into w and returns the byte count.
func (c *Client) Download(ctx context.Context, remote string, w io.Writer) (int64, error) {
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
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return 0, apiErrorFrom(resp.StatusCode, b)
	}
	return io.Copy(w, resp.Body)
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
