package cliclient

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
)

// App actions (docs/APP-PLUGINS.md) and archives (docs/ARCHIVES.md): server
// side work the explorer starts from a file's menu. Both are jobs of the
// operations queue, which the CLI follows to the end.

// ───────────────────────── app actions ─────────────────────────

// AppAction is one action an installed app offers the caller
// (GET /api/files/plugins/actions).
type AppAction struct {
	Plugin  string            `json:"plugin"`
	ID      string            `json:"id"`
	Label   map[string]string `json:"label"`
	Applies struct {
		Kind  string   `json:"kind,omitempty"`
		Ext   []string `json:"ext,omitempty"`
		Mime  []string `json:"mime,omitempty"`
		Multi bool     `json:"multi,omitempty"`
	} `json:"applies"`
	// View names the form the action asks its input in, in the explorer.
	View string `json:"view,omitempty"`
}

// LabelIn is the action's label in lang (then English, then any language).
func (a AppAction) LabelIn(lang string) string {
	if s := a.Label[lang]; s != "" {
		return s
	}
	if i := strings.IndexAny(lang, "-_"); i > 0 {
		if s := a.Label[lang[:i]]; s != "" {
			return s
		}
	}
	if s := a.Label["en"]; s != "" {
		return s
	}
	keys := make([]string, 0, len(a.Label))
	for k := range a.Label {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if s := a.Label[k]; s != "" {
			return s
		}
	}
	return a.ID
}

// AppActions lists the actions the caller may run.
func (c *Client) AppActions(ctx context.Context) ([]AppAction, []byte, error) {
	req, err := c.newRequest(ctx, http.MethodGet, "/api/files/plugins/actions", nil, nil)
	if err != nil {
		return nil, nil, err
	}
	raw, err := c.doJSON(req)
	if err != nil {
		return nil, nil, withServerMessage(err)
	}
	var out struct {
		Actions []AppAction `json:"actions"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, nil, fmt.Errorf("parse app actions: %w", err)
	}
	return out.Actions, raw, nil
}

// ErrActionNeedsInput is an action that opens a form in the explorer and was
// run with no parameters: the server answered the form instead of queueing.
var ErrActionNeedsInput = errors.New("this action asks for its input in a form")

// RunAction runs an app's action on paths (`adapter://path`, one storage) with
// params and waits for its job (POST /api/files/plugins/actions/{plugin}/{action}/run).
// The final op carries the files the action wrote (Op.Outputs).
//
// params nil sends none: an action that asks for input in a form then answers
// ErrActionNeedsInput, and its fields are given as params.
func (c *Client) RunAction(ctx context.Context, plugin, action string, paths []string, params map[string]any) (*Op, []byte, error) {
	if plugin == "" || action == "" {
		return nil, nil, errors.New("an app action is named by its app and its action")
	}
	if len(paths) == 0 {
		return nil, nil, errors.New("an app action needs at least one file")
	}
	wire := make([]string, 0, len(paths))
	for _, p := range paths {
		rp, err := ParseRemotePath(p)
		if err != nil {
			return nil, nil, err
		}
		wire = append(wire, rp.String())
	}
	body := map[string]any{"paths": wire}
	if params != nil {
		body["params"] = params
	}
	raw, err := c.postJSON(ctx, "/api/files/plugins/actions/"+url.PathEscape(plugin)+"/"+url.PathEscape(action)+"/run", body)
	if err != nil {
		return nil, nil, withServerMessage(err)
	}
	var surface struct {
		Surface json.RawMessage `json:"surface"`
	}
	if json.Unmarshal(raw, &surface) == nil && len(surface.Surface) > 0 && string(surface.Surface) != "null" {
		return nil, raw, fmt.Errorf("%w: %s/%s - give its fields with --param key=value", ErrActionNeedsInput, plugin, action)
	}
	op, final, err := c.settle(ctx, raw)
	if err == nil && op == nil {
		return nil, raw, fmt.Errorf("the server accepted %s/%s but named no operation to follow", plugin, action)
	}
	return op, final, err
}

// ───────────────────────── archives ─────────────────────────

// ArchiveCreateOptions are the choices of POST /api/files/archive/create.
// Zero values are the server's defaults (the format from the archive's
// extension, then the administrator's default).
type ArchiveCreateOptions struct {
	Format       string // zip | 7z | tar | tar.gz | tar.bz2 | tar.xz | tar.zst (what the server allows)
	Password     string // zip and 7z only
	EncryptNames bool   // 7z: encrypt the file names too
	Compression  int    // 0-9; 0 = the server's default
}

// CreateArchive packs sources (files and folders, `adapter://path`) into a new
// archive at dest and waits for it. A dest that exists is refused (409
// TARGET_EXISTS); nothing is overwritten.
func (c *Client) CreateArchive(ctx context.Context, dest string, sources []string, opt ArchiveCreateOptions) (*Op, []byte, error) {
	dp, err := ParseRemotePath(dest)
	if err != nil {
		return nil, nil, err
	}
	if dp.IsRoot() {
		return nil, nil, errors.New("the archive needs a file name, e.g. docs://out/files.7z")
	}
	if len(sources) == 0 {
		return nil, nil, errors.New("nothing to pack: name at least one file or folder")
	}
	srcs := make([]string, 0, len(sources))
	for _, s := range sources {
		sp, err := ParseRemotePath(s)
		if err != nil {
			return nil, nil, err
		}
		srcs = append(srcs, sp.String())
	}
	body := map[string]any{"dest": dp.String(), "sources": srcs}
	if opt.Format != "" {
		body["format"] = opt.Format
	}
	if opt.Password != "" {
		body["password"] = opt.Password
	}
	if opt.EncryptNames {
		body["encrypt_filenames"] = true
	}
	if opt.Compression > 0 {
		body["compression"] = opt.Compression
	}
	raw, err := c.postJSON(ctx, "/api/files/archive/create", body)
	if err != nil {
		return nil, nil, err
	}
	return c.settle(ctx, raw)
}

// ErrArchivePassword is an encrypted archive extracted with no password or a
// wrong one (401 PASSWORD_REQUIRED / BAD_PASSWORD). Nothing was extracted.
var ErrArchivePassword = errors.New("the archive is encrypted")

// ExtractArchive unpacks the archive at path into dest (a folder on the same
// storage; "" = the archive's own folder) and waits for it. members, given,
// extracts only those entries.
func (c *Client) ExtractArchive(ctx context.Context, path, dest, password string, members []string) (*Op, []byte, error) {
	ap, err := ParseRemotePath(path)
	if err != nil {
		return nil, nil, err
	}
	if ap.IsRoot() {
		return nil, nil, errors.New("extract needs an archive file, not a storage root")
	}
	body := map[string]any{"path": ap.String()}
	if dest != "" {
		dp, err := ParseRemotePath(dest)
		if err != nil {
			return nil, nil, err
		}
		if dp.Adapter != ap.Adapter {
			return nil, nil, fmt.Errorf("an archive is extracted on its own storage (%s://), not into %s://", ap.Adapter, dp.Adapter)
		}
		body["dest"] = dp.String()
	}
	if password != "" {
		body["password"] = password
	}
	if len(members) > 0 {
		body["members"] = members
	}
	raw, err := c.postJSON(ctx, "/api/files/archive/extract", body)
	if err != nil {
		var ae *APIError
		if errors.As(err, &ae) && ae.Status == http.StatusUnauthorized {
			var code struct {
				Code string `json:"code"`
			}
			// ⚠ Not wrapped: this 401 is the archive's, not the session's, and
			// an *APIError 401 inside would earn the "run `filex client login`"
			// hint (IsUnauthorized).
			if json.Unmarshal(ae.Body, &code) == nil && (code.Code == "PASSWORD_REQUIRED" || code.Code == "BAD_PASSWORD") {
				what := "a password is required"
				if code.Code == "BAD_PASSWORD" {
					what = "the password is wrong"
				}
				return nil, nil, fmt.Errorf("%w and %s; nothing was extracted", ErrArchivePassword, what)
			}
		}
		return nil, nil, err
	}
	return c.settle(ctx, raw)
}
