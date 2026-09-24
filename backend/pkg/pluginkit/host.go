package pluginkit

// ── Host functions, guest side ─────────────────────────────────────────
//
// Thin, typed wrappers over the imports filex provides (see the host's
// internal/wasmplugin/hostfn.go for the wire convention). Every call needs
// the matching permission in the manifest AND the admin's grant; a refused
// call returns *HostError with code permission_denied rather than trapping,
// so a plugin can degrade (skip an optional step, tell the user) instead of
// dying.
//
// The wasm imports live in host_wasm.go (GOOS=wasip1); on any other target
// (host-side unit tests of a plugin's logic) they answer ErrNotWasm.

import (
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"time"

	"github.com/brf-tech/filex/backend/pkg/pluginkit/wire"
)

// HostError is a host function's in-band failure.
type HostError struct {
	Code    string
	Message string
}

func (e *HostError) Error() string { return e.Code + ": " + e.Message }

// IsPermissionDenied reports whether err is a refused host call.
func IsPermissionDenied(err error) bool {
	var he *HostError
	return errors.As(err, &he) && he.Code == wire.ErrPermissionDenied
}

// IsNotFound reports whether err is the host saying the thing asked about
// does not exist — for ShareInfo, a link that was deleted outright.
func IsNotFound(err error) bool {
	var he *HostError
	return errors.As(err, &he) && he.Code == wire.ErrNotFound
}

// ErrNotWasm is what every host call returns off-wasm.
var ErrNotWasm = errors.New("pluginkit: host functions are only available inside filex (GOOS=wasip1)")

type envelope struct {
	Error *wire.HostError `json:"error,omitempty"`
}

// callJSON runs a JSON host function and decodes into out (may be nil).
func callJSON(fn func([]byte) []byte, in any, out any) error {
	b, err := json.Marshal(in)
	if err != nil {
		return err
	}
	raw := fn(b)
	if raw == nil {
		return ErrNotWasm
	}
	var env envelope
	if err := json.Unmarshal(raw, &env); err == nil && env.Error != nil {
		return &HostError{Code: env.Error.Code, Message: env.Error.Message}
	}
	if out != nil {
		return json.Unmarshal(raw, out)
	}
	return nil
}

// ── files ──────────────────────────────────────────────────────────────

// Input is an open handle on one of the call's inputs. It implements
// io.Reader; Close releases the host side.
type Input struct {
	handle uint64
	size   int64
	buf    []byte
	eof    bool
}

// OpenInput opens a file ref from ActionRunInput.Inputs (or a ref an engine
// produced) for reading.
func OpenInput(ref string) (*Input, error) {
	var out struct {
		Handle uint64 `json:"handle"`
		Size   int64  `json:"size"`
	}
	if err := callJSON(hostFileOpen, map[string]string{"ref": ref}, &out); err != nil {
		return nil, err
	}
	return &Input{handle: out.Handle, size: out.Size}, nil
}

// Size is the file's size as the host reported it (-1 when unknown).
func (in *Input) Size() int64 { return in.size }

func (in *Input) Read(p []byte) (int, error) {
	if len(in.buf) == 0 && !in.eof {
		b, _ := json.Marshal(map[string]any{"handle": in.handle, "max": ChunkSize})
		raw := hostFileRead(b)
		if raw == nil {
			return 0, ErrNotWasm
		}
		if len(raw) == 0 {
			return 0, io.ErrUnexpectedEOF
		}
		switch raw[0] {
		case 0:
			in.buf = raw[1:]
		case 1:
			in.eof = true
		default:
			var env envelope
			if json.Unmarshal(raw[1:], &env) == nil && env.Error != nil {
				return 0, &HostError{Code: env.Error.Code, Message: env.Error.Message}
			}
			return 0, errors.New("pluginkit: file_read failed")
		}
	}
	if len(in.buf) == 0 && in.eof {
		return 0, io.EOF
	}
	n := copy(p, in.buf)
	in.buf = in.buf[n:]
	return n, nil
}

// Close releases the handle.
func (in *Input) Close() error {
	return callJSON(hostFileClose, map[string]any{"handle": in.handle}, nil)
}

// ReadInput reads a whole input into memory.
func ReadInput(ref string) ([]byte, error) {
	in, err := OpenInput(ref)
	if err != nil {
		return nil, err
	}
	defer in.Close()
	return io.ReadAll(in)
}

// Output is a file the plugin is producing. Name it in
// ActionRunOutput.Outputs (by Ref) to have filex store it.
type Output struct {
	handle uint64
	ref    string
	name   string
}

// CreateOutput opens a new output file (files:write).
func CreateOutput(name string) (*Output, error) {
	var out struct {
		Handle uint64 `json:"handle"`
		Ref    string `json:"ref"`
	}
	if err := callJSON(hostFileCreate, map[string]string{"name": name}, &out); err != nil {
		return nil, err
	}
	return &Output{handle: out.Handle, ref: out.Ref, name: name}, nil
}

// Ref is the handle to name in ActionRunOutput.Outputs.
func (o *Output) Ref() wire.OutputRef { return wire.OutputRef{Ref: o.ref, Name: o.name} }

func (o *Output) Write(p []byte) (int, error) {
	written := 0
	for len(p) > 0 {
		n := len(p)
		if n > ChunkSize {
			n = ChunkSize
		}
		frame := make([]byte, 8+n)
		binary.LittleEndian.PutUint64(frame[:8], o.handle)
		copy(frame[8:], p[:n])
		raw := hostFileWrite(frame)
		if raw == nil {
			return written, ErrNotWasm
		}
		var env envelope
		if json.Unmarshal(raw, &env) == nil && env.Error != nil {
			return written, &HostError{Code: env.Error.Code, Message: env.Error.Message}
		}
		p = p[n:]
		written += n
	}
	return written, nil
}

// Close finishes the file; call it before returning the action.
func (o *Output) Close() error {
	return callJSON(hostFileClose, map[string]any{"handle": o.handle}, nil)
}

// WriteOutput creates an output and writes b into it in one go.
func WriteOutput(name string, b []byte) (wire.OutputRef, error) {
	o, err := CreateOutput(name)
	if err != nil {
		return wire.OutputRef{}, err
	}
	if _, err := o.Write(b); err != nil {
		o.Close()
		return wire.OutputRef{}, err
	}
	if err := o.Close(); err != nil {
		return wire.OutputRef{}, err
	}
	return o.Ref(), nil
}

// ChunkSize is the largest block one file_read/file_write moves.
const ChunkSize = 1 << 20

// ── progress / settings / state ────────────────────────────────────────

// Progress reports done/total (any unit; total 0 = unknown) and an optional
// message the tray shows.
func Progress(done, total int64, message string) {
	_ = callJSON(hostJobProgress, map[string]any{"done": done, "total": total, "message": message}, nil)
}

// Setting reads one admin-configured setting (permission `settings`);
// secret fields are only readable here.
func Setting(key string) (string, bool, error) {
	var out struct {
		Value string `json:"value"`
		Found bool   `json:"found"`
	}
	if err := callJSON(hostSettingsGet, map[string]string{"key": key}, &out); err != nil {
		return "", false, err
	}
	return out.Value, out.Found, nil
}

// StateGet reads per-file state kept for an input ref (permission `state`).
func StateGet(ref, key string) (string, bool, error) {
	var out struct {
		Value string `json:"value"`
		Found bool   `json:"found"`
	}
	if err := callJSON(hostStateGet, map[string]string{"ref": ref, "key": key}, &out); err != nil {
		return "", false, err
	}
	return out.Value, out.Found, nil
}

// StateSet writes per-file state (≤ 64 KiB); action jobs only.
func StateSet(ref, key, value string) error {
	return callJSON(hostStateSet, map[string]any{"ref": ref, "key": key, "value": value}, nil)
}

// StateDelete removes one key.
func StateDelete(ref, key string) error {
	return callJSON(hostStateSet, map[string]any{"ref": ref, "key": key, "value": nil}, nil)
}

// OpenFile is the answer a row action gives when clicking it should take
// the person to a document:
//
//	&wire.Surface{Open: pluginkit.OpenFile(path, "fill", "")}
//
// Name an action OR a view, or neither to just open the file. The host
// checks the screen belongs to this plugin, and filex drops the link when
// the person asking may not see that file — so a list can offer to go
// somewhere without becoming a way around permissions.
func OpenFile(path, action, view string) *wire.OpenRequest {
	return &wire.OpenRequest{Path: path, Action: action, View: view}
}

// StateItem is one file a StateList answer names.
type StateItem struct {
	// Path is adapter-qualified (`docs://reports/nda.pdf`) — what a menu
	// action or a view takes.
	Path  string `json:"path"`
	Name  string `json:"name"`
	Key   string `json:"key"`
	Value string `json:"value"`
}

// StateList finds the files this plugin keeps `key` on (empty key = every
// key), newest first. This is how a home screen lists its own work: without
// it an app can only see the file it was opened on. Deleted files are not in
// the answer, and neither are files the person asking may not see.
func StateList(key string, limit int) ([]StateItem, error) {
	var out struct {
		Items []StateItem `json:"items"`
	}
	if err := callJSON(hostStateList, map[string]any{"key": key, "limit": limit}, &out); err != nil {
		return nil, err
	}
	return out.Items, nil
}

// ── file locks (permission `files:lock`) ───────────────────────────────

// LockUntilLifted is FileLock's ttlDays for "no end": the lock holds until
// the app lifts it or an administrator does (audited).
const LockUntilLifted = -1

// FileLock freezes a file read-only for everyone — administrators included
// — for ttlDays (0 = 30, at most 365; LockUntilLifted = no end) or until
// FileUnlock. Folder renames/moves/deletes that would carry the file along
// are refused too. The locking plugin's own jobs may still write into the
// file. Action jobs only. Returns the expiry (zero for LockUntilLifted).
//
// ref may be an input, or one of this job's OWN outputs: that lock is
// promised and taken when the job's output is committed (never, if the job
// fails) — how an app locks the file it has just produced.
func FileLock(ref string, ttlDays int, reason string) (time.Time, error) {
	var out struct {
		Until *time.Time `json:"until"`
	}
	if err := callJSON(hostFileLock, map[string]any{"ref": ref, "ttl_days": ttlDays, "reason": reason}, &out); err != nil {
		return time.Time{}, err
	}
	if out.Until == nil {
		return time.Time{}, nil
	}
	return *out.Until, nil
}

// FileLockMessage is FileLock with its reason named by one of the manifest's
// `messages` (key) and that message's {placeholders} (args): filex then says
// the reason in each reader's own language — the administrator's panel, the
// details panel, a refused rename — instead of in the language of the call
// that took the lock. Prefer it to FileLock's plain string.
func FileLockMessage(ref string, ttlDays int, key string, args map[string]string) (time.Time, error) {
	var out struct {
		Until *time.Time `json:"until"`
	}
	if err := callJSON(hostFileLock, map[string]any{"ref": ref, "ttl_days": ttlDays, "reason_key": key, "reason_args": args}, &out); err != nil {
		return time.Time{}, err
	}
	if out.Until == nil {
		return time.Time{}, nil
	}
	return *out.Until, nil
}

// FileLockPath is FileLock by adapter-qualified path (`docs://a/b.pdf`) on
// the job's own storage — for a file that is not one of the job's inputs.
func FileLockPath(qualified string, ttlDays int, reason string) (time.Time, error) {
	var out struct {
		Until *time.Time `json:"until"`
	}
	if err := callJSON(hostFileLock, map[string]any{"path": qualified, "ttl_days": ttlDays, "reason": reason}, &out); err != nil {
		return time.Time{}, err
	}
	if out.Until == nil {
		return time.Time{}, nil
	}
	return *out.Until, nil
}

// FileUnlock lifts this plugin's lock on an input (no-op when unlocked).
func FileUnlock(ref string) error {
	return callJSON(hostFileUnlock, map[string]any{"ref": ref}, nil)
}

// FileUnlockPath is FileUnlock by adapter-qualified path.
func FileUnlockPath(qualified string) error {
	return callJSON(hostFileUnlock, map[string]any{"path": qualified}, nil)
}

// ── engines ────────────────────────────────────────────────────────────

// EngineRequest runs one of the host's heavy engines (permission
// `engines:<name>`). Inputs maps a file name inside the run directory to a
// ref; Args are bare tokens (no paths); Outputs names the files to collect
// (empty = every new file).
type EngineRequest struct {
	Engine   string            `json:"engine"`
	Args     []string          `json:"args"`
	Inputs   map[string]string `json:"inputs,omitempty"`
	Outputs  []string          `json:"outputs,omitempty"`
	TimeoutS int               `json:"timeout_s,omitempty"`
}

// EngineResult is what the engine produced.
type EngineResult struct {
	Exit       int              `json:"exit"`
	StdoutTail string           `json:"stdout_tail"`
	StderrTail string           `json:"stderr_tail"`
	Outputs    []wire.OutputRef `json:"outputs"`
	DurationMS int64            `json:"duration_ms"`
}

// EngineAvailable reports whether the engine is installed AND granted.
func EngineAvailable(name string) bool {
	var out struct {
		Available bool `json:"available"`
	}
	if err := callJSON(hostEngineAvailable, map[string]string{"engine": name}, &out); err != nil {
		return false
	}
	return out.Available
}

// EngineRun runs the engine and returns its artefacts as refs the plugin
// may read (OpenInput) or hand back as outputs.
func EngineRun(req EngineRequest) (*EngineResult, error) {
	var out EngineResult
	if err := callJSON(hostEngineRun, req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ── people / notifications / mail / network ────────────────────────────

// User is a directory entry from UsersLookup.
type User struct {
	ID    int64  `json:"user_id"`
	Email string `json:"email"`
	Name  string `json:"name,omitempty"`
}

// UsersLookup searches the caller's directory (permission `users:lookup`,
// tenant-scoped; at most 20 rows).
func UsersLookup(q string) ([]User, error) {
	var out struct {
		Users []User `json:"users"`
	}
	if err := callJSON(hostUsersLookup, map[string]string{"q": q}, &out); err != nil {
		return nil, err
	}
	return out.Users, nil
}

// Notice is what NotifySend posts: title/body per language (en required),
// severity info|warning|error, and a few small facts.
type Notice struct {
	Title    wire.Text      `json:"title"`
	Body     wire.Text      `json:"body,omitempty"`
	Severity string         `json:"severity,omitempty"`
	Meta     map[string]any `json:"meta,omitempty"`
	// ToUserID addresses one person (their bell, push and — if they turned
	// it on — mail). 0 posts to the instance feed as before.
	ToUserID int64 `json:"to_user_id,omitempty"`
	// Target makes the notification clickable: a click opens the file and,
	// when Action or View is set, that screen of this plugin on it.
	Target *NoticeTarget `json:"target,omitempty"`
}

// NoticeTarget names the file (an input Ref, or an adapter-qualified Path
// on the job's storage) and the optional screen to open on it.
//
// A notice may instead open one of this app's HOME pages: no Ref, no Path,
// View naming a view placed `home`, and optionally the Section of that page
// to land on (Surface.Sections). A click then opens the page itself, in the
// app, at that section — for a notice about the list rather than about one
// document ("your request progressed" opens the requests you sent).
type NoticeTarget struct {
	Ref     string `json:"ref,omitempty"`
	Path    string `json:"path,omitempty"`
	Action  string `json:"action,omitempty"`
	View    string `json:"view,omitempty"`
	Section string `json:"section,omitempty"`
}

// NotifySend raises a `plugin.notice` notification (permission `notify:send`).
func NotifySend(n Notice) (int64, error) {
	var out struct {
		ID int64 `json:"id"`
	}
	if err := callJSON(hostNotifySend, n, &out); err != nil {
		return 0, err
	}
	return out.ID, nil
}

// MailSend sends a plain-text mail through filex's SMTP (permission
// `mail:send`, 60 per hour per plugin; filex appends its own footer).
func MailSend(to, subject, body string) error {
	return callJSON(hostMailSend, map[string]string{"to": to, "subject": subject, "body": body}, nil)
}

// MailSendIn is MailSend for a mail the app wrote in lang (a language tag:
// "en", "tr", "es"…). filex writes its footer ("Sent by the … app on filex")
// in that language — a language pack's included — and labels the message
// with it (Content-Language). MailSend leaves both to the language the call
// runs in, which is empty on a scheduled wake-up: a reminder the app writes
// in the requester's language would then carry the instance's footer.
func MailSendIn(lang, to, subject, body string) error {
	return callJSON(hostMailSend, map[string]string{"to": to, "subject": subject, "body": body, "lang": lang}, nil)
}

// HTTPRequest is an outbound call; the host must be in the manifest's
// `http:<host>` grants, private/local addresses are refused, bodies are
// capped at 8 MiB and the call at 30 s.
type HTTPRequest struct {
	Method   string            `json:"method,omitempty"`
	URL      string            `json:"url"`
	Headers  map[string]string `json:"headers,omitempty"`
	Body     []byte            `json:"-"`
	BodyB64  string            `json:"body_b64,omitempty"`
	TimeoutS int               `json:"timeout_s,omitempty"`
}

// HTTPResponse is the answer; Body is decoded for the caller.
type HTTPResponse struct {
	Status  int               `json:"status"`
	Headers map[string]string `json:"headers"`
	BodyB64 string            `json:"body_b64"`
	Body    []byte            `json:"-"`
}

// Asset is a file asset_fetch put in this app's cache on the host.
type Asset struct {
	// Ref reads it: OpenInput(Ref) / ReadInput(Ref). No `files:read` needed.
	Ref  string `json:"ref"`
	Size int64  `json:"size"`
	// Cached is true when no network was used (it was already downloaded).
	Cached bool `json:"cached"`
}

// AssetFetch downloads url ONCE into this app's cache on the host and hands
// back a ref to read it with. The file must match sha256 (64 hex, pinned by
// the app) — a mismatch is refused with the `integrity` code and nothing is
// kept — and may be at most maxBytes (the host's ceiling is 32 MiB). Later
// calls with the same sha256 are served from the cache without the network.
//
// The URL's host must be one of the app's `http:<host>` permissions, like
// HTTPDo; https only. Offline, or when the host cannot reach the server, the
// call fails (`unavailable` / `timeout`) and the app decides what to do —
// the signing app tells the person which characters will not print.
func AssetFetch(url, sha256 string, maxBytes int64) (*Asset, error) {
	var out Asset
	req := map[string]any{"url": url, "sha256": sha256, "max_bytes": maxBytes}
	if err := callJSON(hostAssetFetch, req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// HTTPDo performs the request.
func HTTPDo(req HTTPRequest) (*HTTPResponse, error) {
	if len(req.Body) > 0 && req.BodyB64 == "" {
		req.BodyB64 = base64.StdEncoding.EncodeToString(req.Body)
	}
	var out HTTPResponse
	if err := callJSON(hostHTTPRequest, req, &out); err != nil {
		return nil, err
	}
	out.Body, _ = base64.StdEncoding.DecodeString(out.BodyB64)
	return &out, nil
}

// ── public pages ───────────────────────────────────────────────────────

// PageCreate is what ShareCreate asks for. PIN: "auto" mints a 6-digit
// PIN (returned to the app once; the link's creator or an administrator can
// read it back later, as for any share), a literal sets it, "" leaves the
// page open — within the manifest's `pin` policy for that page. Files are
// refs of this call (inputs, outputs, engine artefacts) copied out for the
// visitor.
type PageCreate struct {
	// PageID names the manifest page this link renders. EMPTY opens an
	// ordinary download share of the document instead — no surface, no
	// copies, the kind an administrator already revokes in Shares.
	PageID string `json:"page_id"`
	// Ref / Path name the document the link is about; empty means the call's
	// first input. ⚠ The host has read these since v3 and both documents
	// promise them, but this struct did not carry them — so a plugin could
	// not name any file but the first input, which is exactly what a
	// page-less link's refusal message asks it to do.
	Ref       string           `json:"ref,omitempty"`
	Path      string           `json:"path,omitempty"`
	Subject   string           `json:"subject,omitempty"`
	PIN       string           `json:"pin,omitempty"`
	TTLDays   int              `json:"ttl_days,omitempty"`
	MaxVisits int              `json:"max_visits,omitempty"`
	State     any              `json:"state,omitempty"`
	Files     []wire.OutputRef `json:"files,omitempty"`
	// Purpose says what THIS link is, in a list of links (My shares, the
	// admin's Shares) — the same shape a manifest page declares, and it wins
	// over the page's own. ⚠ A page-less link (no PageID: a plain share of a
	// file, e.g. a finished document handed to everybody) has no page to
	// declare one, so this is the only way it can say "Signed copy" instead
	// of reading as a share somebody made by hand.
	Purpose *wire.PagePurpose `json:"purpose,omitempty"`
}

// PageCreated is the answer: the link, the PIN (once, when one was made).
type PageCreated struct {
	Token     string `json:"token"`
	URL       string `json:"url"`
	PIN       string `json:"pin,omitempty"`
	ExpiresAt string `json:"expires_at"`
}

// ShareCreate opens a share link, /s/<token>, for an outside participant
// (permission `public_pages`; action jobs only).
func ShareCreate(req PageCreate) (*PageCreated, error) {
	var out PageCreated
	if err := callJSON(hostShareCreate, req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ShareRevoke ends a share link early.
func ShareRevoke(token string) error {
	return callJSON(hostShareRevoke, map[string]string{"token": token}, nil)
}

// ShareState reads the link's durable record into out. Inside a
// page_event call token may be "" (the current link).
func ShareState(token string, out any) error {
	var env struct {
		State json.RawMessage `json:"state"`
	}
	if err := callJSON(hostShareState, map[string]string{"token": token}, &env); err != nil {
		return err
	}
	if out == nil || len(env.State) == 0 {
		return nil
	}
	return json.Unmarshal(env.State, out)
}

// ShareStateSet replaces the link's durable record (≤ 64 KiB).
func ShareStateSet(token string, state any) error {
	return callJSON(hostShareState, map[string]any{"token": token, "state": state}, nil)
}

// ShareFacts is what an app may learn about one of its OWN links: never the
// token's owner, the PIN or who else it went to.
type ShareFacts struct {
	Page    string `json:"page"`
	Subject string `json:"subject"`
	Visits  int    `json:"visits"`
	// ExpiresAt is when the link stops (nil: never). Revoking a link sets it
	// to the moment of the revoke, so a link that ends EARLIER than the day
	// the app was handed at share_create was ended by somebody.
	ExpiresAt *time.Time `json:"expires_at"`
	// Revoked says the link no longer opens: it ran out, reached its visit
	// ceiling, or somebody revoked it. ExpiresAt tells the three apart from
	// what the app recorded when it opened the link.
	Revoked bool `json:"revoked"`
	// HasPIN: the link is PIN-protected at all.
	HasPIN bool `json:"has_pin"`
	// PinRecoverable: the PIN can still be SHOWN to the person who made the
	// link (SharePIN). False on a link minted before the instance kept a
	// recoverable copy, on an instance with no secret key, and on one whose
	// key has been rotated away — the link still works, its PIN is simply
	// gone. ⚠ Offer "show the PIN" only when this is true; otherwise say so
	// and offer what does work, which is a new link.
	PinRecoverable bool `json:"pin_recoverable"`
}

// ShareInfo reads the facts of one of this app's links (permission
// `public_pages`). Allowed in every call — a screen, a page and the hourly
// wake-up may read, which is how an app learns that a link it handed out was
// revoked from the Shares screen: nobody tells it; it asks.
//
// A link that was DELETED outright (an administrator's Delete, not Revoke)
// answers an error for which IsNotFound is true. Treat it as ended.
func ShareInfo(token string) (*ShareFacts, error) {
	var env struct {
		Page *ShareFacts `json:"page"`
	}
	if err := callJSON(hostShareState, map[string]string{"token": token}, &env); err != nil {
		return nil, err
	}
	if env.Page == nil {
		return &ShareFacts{}, nil
	}
	return env.Page, nil
}

// SharePIN reads the PIN of one of this app's own links, for the PERSON
// making the call (permission `public_pages`).
//
// ⚠⚠ This is the platform's own PIN store, not a copy: an app must never
// keep a PIN of its own. The host allows the read only to the person who
// created the link or to an administrator, and writes an audit row for every
// read, successful or not — the same row the "My shares" screen writes. A
// call with no signed-in person behind it (a public page's visitor, the
// hourly wake-up) is refused.
//
// An empty PIN comes back with a REASON rather than as a blank:
//
//	no_pin           the link is not PIN-protected
//	no_secret_key    this installation cannot show any PIN it minted
//	not_recoverable  this particular PIN is gone for good
//
// Say the reason. A blank where a secret should be reads as a bug, and the
// person cannot tell "there is no PIN" from "we lost it".
func SharePIN(token string) (pin string, reason string, err error) {
	var out struct {
		PIN    string `json:"pin"`
		Reason string `json:"reason"`
	}
	if err := callJSON(hostSharePIN, map[string]string{"token": token}, &out); err != nil {
		return "", "", err
	}
	return out.PIN, out.Reason, nil
}

// The v1/v2 spelling. An app's public page is a share from v3 on; these
// names keep older sources compiling and call the same host functions.
//
// Deprecated: use ShareCreate.
func PublicPageCreate(req PageCreate) (*PageCreated, error) { return ShareCreate(req) }

// Deprecated: use ShareRevoke.
func PublicPageRevoke(token string) error { return ShareRevoke(token) }

// Deprecated: use ShareState.
func PublicPageState(token string, out any) error { return ShareState(token, out) }

// Deprecated: use ShareStateSet.
func PublicPageStateSet(token string, state any) error { return ShareStateSet(token, state) }
