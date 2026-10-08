package cliclient

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// The vault API (encryption level 3) - docs/E2E-VAULT-FORMAT.md → "API".
// What `filex decrypt docs://Kasa`, `filex vault mount` and `filex vault
// prune` need from the server. Nothing here decrypts: packs and index files
// travel as the ciphertext they are, and reads go through the ordinary
// download with a Range.

const vaultAPI = "/api/files/e2e/vault"

// VaultLockHeader carries the write lock's token on every vault write.
const VaultLockHeader = "X-Filex-Vault-Lock"

// The error codes of the vault API this client acts on.
const (
	VaultCodeLocked       = "VAULT_LOCKED"
	VaultCodeLockLost     = "VAULT_LOCK_LOST"
	VaultCodeNotAVault    = "NOT_A_VAULT"
	VaultCodePackExists   = "VAULT_PACK_EXISTS"
	VaultCodeGeneration   = "VAULT_GENERATION"
	VaultCodeBadObject    = "VAULT_BAD_OBJECT"
	VaultCodeKeep         = "VAULT_KEEP"
	VaultCodeVaultPath    = "VAULT_PATH"
	VaultClientCLI        = "cli"
	VaultClientMount      = "mount"
	vaultListPageMax      = 10000
	vaultObjectReadMaxCap = 64 << 20
)

// VaultTime is a time the vault API sends: RFC 3339, or milliseconds since
// 1970 as a number; null is the zero time.
type VaultTime struct{ time.Time }

// UnmarshalJSON reads either form.
func (t *VaultTime) UnmarshalJSON(b []byte) error {
	s := strings.TrimSpace(string(b))
	if s == "null" || s == `""` {
		t.Time = time.Time{}
		return nil
	}
	if strings.HasPrefix(s, `"`) {
		var str string
		if err := json.Unmarshal(b, &str); err != nil {
			return err
		}
		v, err := time.Parse(time.RFC3339Nano, str)
		if err != nil {
			return fmt.Errorf("vault time %q: %w", str, err)
		}
		t.Time = v
		return nil
	}
	n, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return fmt.Errorf("vault time %s: %w", s, err)
	}
	t.Time = time.UnixMilli(int64(n))
	return nil
}

// VaultHolder is who holds a vault's write lock.
type VaultHolder struct {
	Name   string `json:"name"`
	Client string `json:"client"`
	Label  string `json:"label"`
}

// String is "Ayşe (web, Firefox, ofis)".
func (h VaultHolder) String() string {
	var extra []string
	for _, s := range []string{h.Client, h.Label} {
		if s != "" {
			extra = append(extra, s)
		}
	}
	name := h.Name
	if name == "" {
		name = "someone"
	}
	if len(extra) == 0 {
		return name
	}
	return name + " (" + strings.Join(extra, ", ") + ")"
}

// VaultLockInfo is `state`'s lock.
type VaultLockInfo struct {
	Holder    VaultHolder `json:"holder"`
	Since     VaultTime   `json:"since"`
	ExpiresAt VaultTime   `json:"expires_at"`
	Mine      bool        `json:"mine"`
}

// VaultState is GET /state.
type VaultState struct {
	VaultID    string         `json:"vault_id"`
	PackLog2   int            `json:"pack_log2"`
	Generation uint64         `json:"generation"`
	Lock       *VaultLockInfo `json:"lock"`
}

// VaultLock is a taken write lock.
type VaultLock struct {
	Token        string    `json:"token"`
	Generation   uint64    `json:"generation"`
	LeaseSeconds int       `json:"lease_seconds"`
	IdleSeconds  int       `json:"idle_seconds"`
	ExpiresAt    VaultTime `json:"expires_at"`
}

// VaultRenewal is POST /lock/renew.
type VaultRenewal struct {
	ExpiresAt VaultTime `json:"expires_at"`
	IdleUntil VaultTime `json:"idle_until"`
}

// VaultListItem is one row of GET /list: an index file (Generation) or a
// pack (ID, 32 lower-case hex digits).
type VaultListItem struct {
	Generation uint64    `json:"generation"`
	ID         string    `json:"id"`
	Size       int64     `json:"size"`
	MTime      VaultTime `json:"mtime"`
}

// VaultError is an error answer of the vault API.
type VaultError struct {
	Status  int
	Code    string
	Message string
	// Holder, Since and RetryAfter come with VAULT_LOCKED.
	Holder     *VaultHolder
	Since      time.Time
	RetryAfter int
	// Reason comes with VAULT_LOCK_LOST: expired, idle, broken, released, taken.
	Reason string
	// Latest comes with VAULT_GENERATION.
	Latest uint64
}

func (e *VaultError) Error() string {
	msg := e.Message
	if msg == "" {
		msg = e.Code
	}
	if e.Code != "" && msg != e.Code {
		return fmt.Sprintf("HTTP %d %s: %s", e.Status, e.Code, msg)
	}
	return fmt.Sprintf("HTTP %d: %s", e.Status, msg)
}

// IsVaultCode reports whether err is a vault API answer with that code.
func IsVaultCode(err error, code string) bool {
	var ve *VaultError
	return errors.As(err, &ve) && ve.Code == code
}

// vaultError turns an *APIError into a *VaultError when its body is a vault
// API error ({"error": CODE, "message": ...}); anything else is kept.
func vaultError(err error) error {
	var ae *APIError
	if !errors.As(err, &ae) {
		return err
	}
	var body struct {
		Error      string       `json:"error"`
		Message    string       `json:"message"`
		Holder     *VaultHolder `json:"holder"`
		Since      VaultTime    `json:"since"`
		RetryAfter int          `json:"retry_after"`
		Reason     string       `json:"reason"`
		Latest     uint64       `json:"latest"`
	}
	if json.Unmarshal(ae.Body, &body) != nil || body.Error == "" {
		return err
	}
	return &VaultError{
		Status: ae.Status, Code: body.Error, Message: body.Message,
		Holder: body.Holder, Since: body.Since.Time, RetryAfter: body.RetryAfter,
		Reason: body.Reason, Latest: body.Latest,
	}
}

func vaultPathOf(remote string) (string, error) {
	rp, err := ParseRemotePath(remote)
	if err != nil {
		return "", err
	}
	if rp.IsRoot() {
		return "", errors.New("a vault is a folder, not a storage root")
	}
	return rp.String(), nil
}

func (c *Client) vaultDo(ctx context.Context, method, p string, q url.Values, body io.Reader, contentType, token string) ([]byte, error) {
	req, err := c.newRequest(ctx, method, vaultAPI+p, q, body)
	if err != nil {
		return nil, err
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	if token != "" {
		req.Header.Set(VaultLockHeader, token)
	}
	raw, err := c.doJSON(req)
	if err != nil {
		return nil, vaultError(err)
	}
	return raw, nil
}

func (c *Client) vaultJSON(ctx context.Context, p string, payload any, token string, out any) error {
	b, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	raw, err := c.vaultDo(ctx, http.MethodPost, p, nil, bytes.NewReader(b), "application/json", token)
	if err != nil {
		return err
	}
	if out == nil || len(bytes.TrimSpace(raw)) == 0 {
		return nil
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("parse the vault answer to %s: %w", p, err)
	}
	return nil
}

// VaultState asks for a vault's id, pack size, latest generation and lock.
func (c *Client) VaultState(ctx context.Context, remote string) (*VaultState, error) {
	p, err := vaultPathOf(remote)
	if err != nil {
		return nil, err
	}
	raw, err := c.vaultDo(ctx, http.MethodGet, "/state", url.Values{"path": {p}}, nil, "", "")
	if err != nil {
		return nil, err
	}
	var st VaultState
	if err := json.Unmarshal(raw, &st); err != nil {
		return nil, fmt.Errorf("parse the vault state: %w", err)
	}
	return &st, nil
}

// VaultList is one page of GET /list; kind is "index" or "pack". next is the
// cursor of the following page, "" at the end.
func (c *Client) VaultList(ctx context.Context, remote, kind, after string, limit int) ([]VaultListItem, string, error) {
	p, err := vaultPathOf(remote)
	if err != nil {
		return nil, "", err
	}
	q := url.Values{"path": {p}, "kind": {kind}}
	if after != "" {
		q.Set("after", after)
	}
	if limit > 0 {
		q.Set("limit", strconv.Itoa(limit))
	}
	raw, err := c.vaultDo(ctx, http.MethodGet, "/list", q, nil, "", "")
	if err != nil {
		return nil, "", err
	}
	var out struct {
		Items []VaultListItem `json:"items"`
		Next  json.RawMessage `json:"next"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, "", fmt.Errorf("parse the vault listing: %w", err)
	}
	return out.Items, cursorOf(out.Next), nil
}

// cursorOf reads a `next` cursor: null, a string, or a number.
func cursorOf(raw json.RawMessage) string {
	s := strings.TrimSpace(string(raw))
	if s == "" || s == "null" {
		return ""
	}
	var str string
	if json.Unmarshal(raw, &str) == nil {
		return str
	}
	return s
}

// VaultListAll pages through GET /list.
func (c *Client) VaultListAll(ctx context.Context, remote, kind string) ([]VaultListItem, error) {
	var all []VaultListItem
	after := ""
	for page := 0; ; page++ {
		items, next, err := c.VaultList(ctx, remote, kind, after, vaultListPageMax)
		if err != nil {
			return nil, err
		}
		all = append(all, items...)
		if next == "" || next == after || len(items) == 0 {
			return all, nil
		}
		if page > 1000 {
			return nil, errors.New("the vault listing does not end")
		}
		after = next
	}
}

// VaultLock takes the write lock. client is "cli" or "mount"; label is a
// short free text naming this machine. A lock someone else holds is a
// *VaultError VAULT_LOCKED with the holder.
func (c *Client) VaultLock(ctx context.Context, remote, client, label string) (*VaultLock, error) {
	p, err := vaultPathOf(remote)
	if err != nil {
		return nil, err
	}
	var out VaultLock
	if err := c.vaultJSON(ctx, "/lock", map[string]string{"path": p, "client": client, "label": label}, "", &out); err != nil {
		return nil, err
	}
	if out.Token == "" {
		return nil, errors.New("the server took the vault lock but sent no token")
	}
	return &out, nil
}

// VaultRenew is the heartbeat; active says this session is about to write.
// A lock that ended is a *VaultError VAULT_LOCK_LOST with its reason.
func (c *Client) VaultRenew(ctx context.Context, remote, token string, active bool) (*VaultRenewal, error) {
	p, err := vaultPathOf(remote)
	if err != nil {
		return nil, err
	}
	var out VaultRenewal
	if err := c.vaultJSON(ctx, "/lock/renew", map[string]any{"path": p, "active": active}, token, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// VaultRelease gives the lock back (whether or not the token still held it).
func (c *Client) VaultRelease(ctx context.Context, remote, token string) error {
	p, err := vaultPathOf(remote)
	if err != nil {
		return err
	}
	return c.vaultJSON(ctx, "/lock/release", map[string]string{"path": p}, token, nil)
}

// VaultBreak ends someone else's lock (the folder's owner, an administrator).
func (c *Client) VaultBreak(ctx context.Context, remote string) error {
	p, err := vaultPathOf(remote)
	if err != nil {
		return err
	}
	return c.vaultJSON(ctx, "/lock/break", map[string]string{"path": p}, "", nil)
}

// VaultPutPack stores a pack (created, never replaced: VAULT_PACK_EXISTS).
// id is 32 lower-case hex digits.
func (c *Client) VaultPutPack(ctx context.Context, remote, token, id string, pack []byte) error {
	p, err := vaultPathOf(remote)
	if err != nil {
		return err
	}
	_, err = c.vaultDo(ctx, http.MethodPut, "/pack", url.Values{"path": {p}, "id": {id}}, bytes.NewReader(pack), "application/octet-stream", token)
	return err
}

// VaultPutIndex commits the index file of generation gen (latest + 1, or
// VAULT_GENERATION with the latest).
func (c *Client) VaultPutIndex(ctx context.Context, remote, token string, gen uint64, file []byte) (uint64, error) {
	p, err := vaultPathOf(remote)
	if err != nil {
		return 0, err
	}
	raw, err := c.vaultDo(ctx, http.MethodPut, "/index", url.Values{"path": {p}, "generation": {strconv.FormatUint(gen, 10)}}, bytes.NewReader(file), "application/octet-stream", token)
	if err != nil {
		return 0, err
	}
	var out struct {
		Generation uint64 `json:"generation"`
	}
	if len(bytes.TrimSpace(raw)) > 0 {
		if err := json.Unmarshal(raw, &out); err != nil {
			return 0, fmt.Errorf("parse the commit answer: %w", err)
		}
	}
	if out.Generation == 0 {
		out.Generation = gen
	}
	return out.Generation, nil
}

// VaultDelete deletes packs (hex ids) and index files (generations) for
// good. At most 1 000 names per call; a missing one counts as deleted.
func (c *Client) VaultDelete(ctx context.Context, remote, token string, packs []string, indexes []uint64) (int, int, error) {
	p, err := vaultPathOf(remote)
	if err != nil {
		return 0, 0, err
	}
	if packs == nil {
		packs = []string{}
	}
	if indexes == nil {
		indexes = []uint64{}
	}
	var out struct {
		Deleted struct {
			Packs   json.RawMessage `json:"packs"`
			Indexes json.RawMessage `json:"indexes"`
		} `json:"deleted"`
	}
	if err := c.vaultJSON(ctx, "/delete", map[string]any{"path": p, "packs": packs, "indexes": indexes}, token, &out); err != nil {
		return 0, 0, err
	}
	return countOf(out.Deleted.Packs), countOf(out.Deleted.Indexes), nil
}

// countOf reads a count that is either a number or a list.
func countOf(raw json.RawMessage) int {
	var n int
	if json.Unmarshal(raw, &n) == nil {
		return n
	}
	var list []json.RawMessage
	if json.Unmarshal(raw, &list) == nil {
		return len(list)
	}
	return 0
}

// VaultIdleMinutes is the person's idle time before a write lock ends.
func (c *Client) VaultIdleMinutes(ctx context.Context) (int, error) {
	raw, err := c.vaultDo(ctx, http.MethodGet, "/prefs", nil, nil, "", "")
	if err != nil {
		return 0, err
	}
	var out struct {
		IdleMinutes int `json:"idle_minutes"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return 0, fmt.Errorf("parse the vault preferences: %w", err)
	}
	return out.IdleMinutes, nil
}

// ErrObjectTooLarge: a whole read stopped at its limit.
var ErrObjectTooLarge = errors.New("the file is larger than it may be")

// ReadWhole downloads a file whole, refusing more than limit bytes (an index
// file: at most 64 MiB).
func (c *Client) ReadWhole(ctx context.Context, remote string, limit int64) ([]byte, error) {
	if limit <= 0 || limit > vaultObjectReadMaxCap {
		limit = vaultObjectReadMaxCap
	}
	w := &cappedBuffer{max: limit}
	if _, err := c.Download(ctx, remote, w); err != nil {
		return nil, err
	}
	return w.buf.Bytes(), nil
}

// cappedBuffer is limitedBuffer with a sentinel error (the buffer is a field,
// not embedded: see limitedBuffer).
type cappedBuffer struct {
	buf bytes.Buffer
	max int64
}

func (b *cappedBuffer) Write(p []byte) (int, error) {
	if int64(b.buf.Len()+len(p)) > b.max {
		return 0, ErrObjectTooLarge
	}
	return b.buf.Write(p)
}
