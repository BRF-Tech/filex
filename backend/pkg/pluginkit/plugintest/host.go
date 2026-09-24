package plugintest

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"math/big"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/brf-tech/filex/backend/pkg/pluginkit"
	"github.com/brf-tech/filex/backend/pkg/pluginkit/wire"
)

// ── The fake host ──────────────────────────────────────────────────────
//
// Host is filex's host side in memory: files, settings, state, locks,
// engines, signing, notifications, mail, HTTP and shares. It answers with
// the SAME error codes the real host returns (wire.Err*), refuses a call
// the manifest did not ask for, and refuses from a screen what only an
// action job may do — a kit that says yes to everything is a kit that
// lies, and the plugin finds out in production instead of in `go test`.

// File is one file handed to a call.
type File struct {
	Name string
	Data []byte
	Mime string
	// Path is the adapter-qualified spelling (`docs://a/b.pdf`); the
	// harness invents one when it is empty.
	Path string
	// Rel is the storage-relative path; defaults to Name.
	Rel string
	// ReadOnly puts the file on a storage that takes no writes
	// (wire.FileRef.ReadOnly).
	ReadOnly bool
}

type hostFile struct {
	ref      string
	name     string
	data     []byte
	mime     string
	path     string
	rel      string
	size     int64
	output   bool
	readOnly bool
	asset    bool
}

// ProgressLine is one job_progress call.
type ProgressLine struct {
	Done    int64
	Total   int64
	Message string
}

// Mail is one mail_send call.
type Mail struct {
	To      string
	Subject string
	Body    string
	// Lang is what MailSendIn stated; "" for MailSend.
	Lang string
}

// EngineCall is one engine_run call, recorded in order.
type EngineCall struct {
	Request pluginkit.EngineRequest
	Result  *pluginkit.EngineResult
	Err     error
}

// Share is a public link the plugin opened (share_create).
type Share struct {
	Token     string
	PageID    string
	Subject   string
	PIN       string
	ExpiresAt time.Time
	MaxVisits int
	Visits    int
	Revoked   bool
	State     json.RawMessage
	// Purpose is what the plugin said this link is (PageCreate.Purpose).
	Purpose *wire.PagePurpose
	// Files are the copies the visitor may read, as `pub:N` refs.
	Files []wire.OutputRef
}

// IssuedKey is a certificate the fake CA minted.
type IssuedKey struct {
	Ref        string
	CommonName string
	Email      string
	Cert       *x509.Certificate
	CertPEM    string
	Destroyed  bool
	priv       *ecdsa.PrivateKey
}

// Host is the in-memory filex. Build one with NewHost, or take the one a
// Harness made (h.Host).
type Host struct {
	mu sync.Mutex

	manifest wire.Manifest
	grants   map[string]bool

	// writable is true inside an action job. Views and pages may read but
	// not create outputs, set state, take locks, run engines or open
	// shares — exactly as internal/wasmplugin.Scope.writable gates them.
	writable bool
	// share is the token of the public link a page_event answers; "" off a
	// page. state_set is allowed on a page call even though it is not
	// writable, as the real host allows it.
	share string

	files   map[string]*hostFile
	order   []string
	nextOut int
	nextEng int
	nextPub int

	settings map[string]string
	state    map[string]map[string]string
	locks    map[string]time.Time
	// lockReasons: the message a lock was taken with (FileLockMessage).
	lockReasons map[string]wire.Text

	// engines are the engines INSTALLED on this fake server; the grant is a
	// separate question (manifest permissions).
	engines map[string]bool
	users   []pluginkit.User

	shares map[string]*Share
	keys   map[string]*IssuedKey
	caCert *x509.Certificate
	caPEM  string
	caKey  *ecdsa.PrivateKey

	// Recorded traffic, for assertions.
	ProgressLog []ProgressLine
	Notices     []pluginkit.Notice
	Mails       []Mail
	Engines     []EngineCall
	Requests    []pluginkit.HTTPRequest
	Logs        []string
	SettingsGot []string

	// EngineFn answers engine_run. The default produces one artefact per
	// name in req.Outputs (`engine:<name>` as bytes), or a single
	// `out.bin`. Set it to script failures, timeouts and real payloads.
	EngineFn func(n int, req pluginkit.EngineRequest) (*pluginkit.EngineResult, error)
	// HTTPFn answers http_request for a granted host. The default is a
	// 200 with an empty body.
	HTTPFn func(req pluginkit.HTTPRequest) (*pluginkit.HTTPResponse, error)

	// MailPerHour is the quota mail_send enforces (the real host: 60).
	MailPerHour int
	mailsSent   int
	// SignUnavailable makes host_sign_info answer "not available" without
	// failing — an instance with no signing key.
	SignUnavailable string
	// SignBudget, when > 0, is how many cert_issue/host_sign calls succeed
	// before the host answers `busy` (the real host rate-limits signing).
	SignBudget int
	signsDone  int
	// MailConfigured false makes mail_send answer `unavailable`.
	MailConfigured bool
	// NotifyConfigured false makes notify_send answer `unavailable`.
	NotifyConfigured bool
	// MaxInputBytes / MaxOutputBytes are the per-file ceilings (0 = none).
	MaxInputBytes  int64
	MaxOutputBytes int64
	// BaseURL is the instance a share link points at.
	BaseURL string

	// Network is what asset_fetch can download: URL → bytes. A URL that is
	// not here answers `unavailable` (the server said 404).
	Network map[string][]byte
	// Offline makes every asset_fetch that needs the network fail with
	// `unavailable` — an installation with no internet. The cache still
	// answers.
	Offline bool
	// Downloads lists every URL asset_fetch actually downloaded, in order;
	// a call served from the cache adds nothing.
	Downloads []string
	assets    map[string][]byte // the app's cache on the host: sha256 → bytes
	nextAsset int

	tokenSeq int
}

// NewHost builds the fake host for a manifest. The manifest's permissions
// are the grants: a call the manifest never asked for is refused, so a
// plugin cannot pass its tests by using a capability it forgot to declare.
func NewHost(m wire.Manifest) *Host {
	h := &Host{
		manifest:         m,
		grants:           map[string]bool{},
		files:            map[string]*hostFile{},
		settings:         map[string]string{},
		state:            map[string]map[string]string{},
		locks:            map[string]time.Time{},
		engines:          map[string]bool{},
		shares:           map[string]*Share{},
		keys:             map[string]*IssuedKey{},
		Network:          map[string][]byte{},
		assets:           map[string][]byte{},
		MailPerHour:      60,
		MailConfigured:   true,
		NotifyConfigured: true,
		BaseURL:          "https://filex.test",
	}
	for _, p := range m.Permissions {
		h.grants[p] = true
	}
	// Defaults the administrator would have filled in: every setting with a
	// default, as its string form.
	for _, f := range m.Settings {
		if f.Default != nil {
			h.settings[f.Key] = fmt.Sprint(f.Default)
		}
	}
	return h
}

// ── wiring the fake instance ───────────────────────────────────────────

// AddInput registers a file the call may read and answers its ref.
func (h *Host) AddInput(f File) wire.FileRef {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.addInputLocked(f)
}

func (h *Host) addInputLocked(f File) wire.FileRef {
	ref := "in:" + strconv.Itoa(len(h.order))
	rel := f.Rel
	if rel == "" {
		rel = f.Name
	}
	path := f.Path
	if path == "" {
		path = "test://" + strings.TrimPrefix(rel, "/")
	}
	hf := &hostFile{ref: ref, name: f.Name, data: f.Data, mime: f.Mime, path: path, rel: rel, size: int64(len(f.Data)), readOnly: f.ReadOnly}
	h.files[ref] = hf
	h.order = append(h.order, ref)
	return wire.FileRef{Ref: ref, Name: hf.name, Size: hf.size, Mime: hf.mime, PathRel: rel, Path: path, ReadOnly: f.ReadOnly}
}

// SetSetting writes one administrator setting (secret fields included).
func (h *Host) SetSetting(key, value string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.settings[key] = value
}

// InstallEngine marks an engine as present on this server. Availability and
// the grant are separate: an engine the manifest never asked for stays
// refused even when installed.
func (h *Host) InstallEngine(names ...string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, n := range names {
		h.engines[n] = true
	}
}

// InstalledEngines is the engine map a call carries (ActionRunInput.Engines
// / CallContext.Engines): installed AND granted, as the host reports it.
func (h *Host) InstalledEngines() map[string]bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := map[string]bool{}
	for name := range h.engines {
		if h.grants["engines:"+name] {
			out[name] = true
		}
	}
	return out
}

// AddUser puts someone in the directory users_lookup searches.
func (h *Host) AddUser(u pluginkit.User) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.users = append(h.users, u)
}

// Bytes answers what a ref holds — an input, an output the plugin wrote or
// an engine artefact. It is how a test reads the result of a job.
func (h *Host) Bytes(ref string) ([]byte, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	f, ok := h.files[ref]
	if !ok {
		return nil, false
	}
	return f.data, true
}

// Outputs lists what the plugin wrote through file_create, in order.
func (h *Host) Outputs() []wire.OutputRef {
	h.mu.Lock()
	defer h.mu.Unlock()
	var out []wire.OutputRef
	for _, ref := range h.order {
		if f := h.files[ref]; f != nil && f.output && strings.HasPrefix(ref, "out:") {
			out = append(out, wire.OutputRef{Ref: f.ref, Name: f.name})
		}
	}
	return out
}

// ShareByToken answers a link the plugin opened.
func (h *Host) ShareByToken(token string) (*Share, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	s, ok := h.shares[token]
	return s, ok
}

// Shares lists every link the plugin opened, in order.
func (h *Host) Shares() []*Share {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]*Share, 0, len(h.shares))
	for _, s := range h.shares {
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Token < out[j].Token })
	return out
}

// Key answers a certificate the fake CA issued.
func (h *Host) Key(ref string) (*IssuedKey, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	k, ok := h.keys[ref]
	return k, ok
}

// Locked reports whether the plugin holds a lock on a ref, and until when.
func (h *Host) Locked(ref string) (time.Time, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	t, ok := h.locks[ref]
	return t, ok
}

// State answers a per-file state key the plugin kept.
func (h *Host) State(ref, key string) (string, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	v, ok := h.state[ref][key]
	return v, ok
}

// EnterJob makes the following calls behave like an action job (writable).
// The Harness does this around Run; call it directly only when driving the
// plugin's functions yourself.
func (h *Host) EnterJob() { h.mu.Lock(); h.writable, h.share = true, ""; h.mu.Unlock() }

// EnterScreen makes the following calls behave like a view event: readable,
// but no outputs, state, locks, engines or shares.
func (h *Host) EnterScreen() { h.mu.Lock(); h.writable, h.share = false, ""; h.mu.Unlock() }

// EnterShare makes the following calls behave like a page event on that
// link: not writable, but state_set and share_state("") answer for it.
func (h *Host) EnterShare(token string) {
	h.mu.Lock()
	h.writable, h.share = false, token
	h.mu.Unlock()
}

// ── errors ─────────────────────────────────────────────────────────────

func hostErr(code, msg string) error { return &pluginkit.HostError{Code: code, Message: msg} }

// IsCode reports whether err is a host failure with that wire.Err* code.
func IsCode(err error, code string) bool {
	var he *pluginkit.HostError
	return errors.As(err, &he) && he.Code == code
}

// Code answers the wire.Err* code of a host failure, or "".
func Code(err error) string {
	var he *pluginkit.HostError
	if errors.As(err, &he) {
		return he.Code
	}
	return ""
}

func (h *Host) need(perm string) error {
	if h.grants[perm] {
		return nil
	}
	return hostErr(wire.ErrPermissionDenied, "plugin was not granted "+perm)
}

func (h *Host) needJob(what string) error {
	if h.writable {
		return nil
	}
	return hostErr(wire.ErrPermissionDenied, what+" from action jobs only")
}

// ── files ──────────────────────────────────────────────────────────────

// ReadInput reads a call-scoped ref (files:read).
func (h *Host) ReadInput(ref string) ([]byte, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	f, ok := h.files[ref]
	// An asset is the app's own download: reading it needs no files:read,
	// as on the real host.
	if !ok || !f.asset {
		if err := h.need("files:read"); err != nil {
			return nil, err
		}
	}
	if !ok {
		return nil, hostErr(wire.ErrNotFound, "no such file ref")
	}
	if h.MaxInputBytes > 0 && f.size > h.MaxInputBytes && !f.output {
		return nil, hostErr(wire.ErrTooLarge, "input exceeds the per-file limit")
	}
	out := make([]byte, len(f.data))
	copy(out, f.data)
	return out, nil
}

// InputSize answers the size the host reported for a ref (-1 unknown).
func (h *Host) InputSize(ref string) int64 {
	h.mu.Lock()
	defer h.mu.Unlock()
	if !h.grants["files:read"] {
		return -1
	}
	f, ok := h.files[ref]
	if !ok {
		return -1
	}
	return f.size
}

// WriteOutput creates an output file with those bytes (files:write).
func (h *Host) WriteOutput(name string, data []byte) (wire.OutputRef, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if err := h.need("files:write"); err != nil {
		return wire.OutputRef{}, err
	}
	if !h.writable {
		return wire.OutputRef{}, hostErr(wire.ErrPermissionDenied, "this call may not create files")
	}
	name = safeName(name)
	if name == "" {
		return wire.OutputRef{}, hostErr(wire.ErrInvalid, "empty output name")
	}
	if h.MaxOutputBytes > 0 && int64(len(data)) > h.MaxOutputBytes {
		return wire.OutputRef{}, hostErr(wire.ErrTooLarge, "output exceeds the per-file limit")
	}
	ref := "out:" + strconv.Itoa(h.nextOut)
	h.nextOut++
	f := &hostFile{ref: ref, name: name, data: append([]byte(nil), data...), size: int64(len(data)), output: true}
	h.files[ref] = f
	h.order = append(h.order, ref)
	return wire.OutputRef{Ref: ref, Name: name}, nil
}

// safeName reduces a plugin-supplied name to one path element, as the host
// does: a plugin cannot write `../../etc/passwd`.
func safeName(name string) string {
	name = strings.TrimSpace(name)
	name = strings.ReplaceAll(name, "\\", "/")
	if i := strings.LastIndex(name, "/"); i >= 0 {
		name = name[i+1:]
	}
	if name == "." || name == ".." {
		return ""
	}
	return strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, name)
}

// ── progress / log / settings / state ──────────────────────────────────

// Progress records a job_progress call (no permission needed).
func (h *Host) Progress(done, total int64, message string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.ProgressLog = append(h.ProgressLog, ProgressLine{Done: done, Total: total, Message: message})
}

// Log records a plugin log line.
func (h *Host) Log(level, msg string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.Logs = append(h.Logs, level+": "+msg)
}

// Setting reads one administrator setting (permission `settings`).
func (h *Host) Setting(key string) (string, bool, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if err := h.need("settings"); err != nil {
		return "", false, err
	}
	h.SettingsGot = append(h.SettingsGot, key)
	v, ok := h.settings[key]
	return v, ok, nil
}

// StateGet reads per-file state (permission `state`).
func (h *Host) StateGet(ref, key string) (string, bool, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if err := h.need("state"); err != nil {
		return "", false, err
	}
	if _, ok := h.files[ref]; !ok {
		return "", false, hostErr(wire.ErrNotFound, "state is kept per storage file; ref is not one")
	}
	v, ok := h.state[ref][key]
	return v, ok, nil
}

// StateSet writes per-file state (≤ 64 KiB; jobs and page events only).
func (h *Host) StateSet(ref, key, value string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if err := h.need("state"); err != nil {
		return err
	}
	if !h.writable && h.share == "" {
		return hostErr(wire.ErrPermissionDenied, "this call may not write state")
	}
	if _, ok := h.files[ref]; !ok {
		return hostErr(wire.ErrNotFound, "state is kept per storage file; ref is not one")
	}
	if len(value) > 64<<10 {
		return hostErr(wire.ErrTooLarge, "state value over 64 KiB")
	}
	if strings.HasSuffix(key, wire.PersonalStateSuffix) {
		return hostErr(wire.ErrInvalid, "a personal key is written as <key>@<user id> (wire.PersonalState), never @me")
	}
	if h.state[ref] == nil {
		h.state[ref] = map[string]string{}
	}
	h.state[ref][key] = value
	return nil
}

// StateDelete removes one key.
func (h *Host) StateDelete(ref, key string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if err := h.need("state"); err != nil {
		return err
	}
	if !h.writable && h.share == "" {
		return hostErr(wire.ErrPermissionDenied, "this call may not write state")
	}
	delete(h.state[ref], key)
	return nil
}

// ── locks ──────────────────────────────────────────────────────────────

// FileLock freezes a ref read-only (permission `files:lock`, jobs only).
func (h *Host) FileLock(ref string, ttlDays int, reason string) (time.Time, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if err := h.need("files:lock"); err != nil {
		return time.Time{}, err
	}
	if err := h.needJob("locks are taken"); err != nil {
		return time.Time{}, err
	}
	if _, ok := h.files[ref]; !ok {
		return time.Time{}, hostErr(wire.ErrNotFound, "lock: "+ref)
	}
	if ttlDays < pluginkit.LockUntilLifted || ttlDays > 365 {
		return time.Time{}, hostErr(wire.ErrInvalid, "ttl_days must be -1 (until lifted) or 0..365")
	}
	// The real host would refuse ANOTHER app's lock; one app is the only one
	// here, and it may renew its own (as the real host lets it).
	if ttlDays == pluginkit.LockUntilLifted {
		// Until lifted: the zero time is "no end" (Locked reports it).
		h.locks[ref] = time.Time{}
		return time.Time{}, nil
	}
	if ttlDays == 0 {
		ttlDays = 30
	}
	until := time.Now().Add(time.Duration(ttlDays) * 24 * time.Hour)
	h.locks[ref] = until
	return until, nil
}

// FileLockMessage is FileLock with a reason named by one of the manifest's
// `messages` (pluginkit.FileLockMessage). A key the manifest does not
// declare is refused, as the host refuses it. LockReason reads it back.
func (h *Host) FileLockMessage(ref string, ttlDays int, key string, args map[string]string) (time.Time, error) {
	h.mu.Lock()
	msg, ok := h.manifest.Messages[key]
	h.mu.Unlock()
	if !ok {
		return time.Time{}, hostErr(wire.ErrInvalid, "reason_key: the manifest declares no message "+key)
	}
	until, err := h.FileLock(ref, ttlDays, "")
	if err != nil {
		return until, err
	}
	h.mu.Lock()
	if h.lockReasons == nil {
		h.lockReasons = map[string]wire.Text{}
	}
	h.lockReasons[ref] = wire.FillMessage(msg, args)
	h.mu.Unlock()
	return until, nil
}

// LockReason is the reason a lock on ref was taken with, in every language
// (FileLockMessage), or nil.
func (h *Host) LockReason(ref string) wire.Text {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.lockReasons[ref]
}

// FileUnlock lifts this plugin's lock (no-op when unlocked).
func (h *Host) FileUnlock(ref string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if err := h.need("files:lock"); err != nil {
		return err
	}
	if err := h.needJob("locks are lifted"); err != nil {
		return err
	}
	delete(h.locks, ref)
	return nil
}

// ── engines ────────────────────────────────────────────────────────────

// EngineAvailable reports whether the engine is installed AND granted.
func (h *Host) EngineAvailable(name string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.grants["engines:"+name] && h.engines[name]
}

// EngineRun runs one of the host's engines (permission `engines:<name>`,
// jobs only). Arguments are judged before anything else — including whether
// the engine exists — so a refused argument is a bug the author sees on
// every host, not only on the one that happens to have ffmpeg.
func (h *Host) EngineRun(req pluginkit.EngineRequest) (*pluginkit.EngineResult, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	res, err := h.engineRunLocked(req)
	h.Engines = append(h.Engines, EngineCall{Request: req, Result: res, Err: err})
	return res, err
}

func (h *Host) engineRunLocked(req pluginkit.EngineRequest) (*pluginkit.EngineResult, error) {
	if err := h.need("engines:" + req.Engine); err != nil {
		return nil, err
	}
	if err := h.needJob("engines run"); err != nil {
		return nil, err
	}
	if len(req.Args) > 256 {
		return nil, hostErr(wire.ErrInvalid, "too many arguments")
	}
	for _, a := range req.Args {
		if err := checkEngineArg(a); err != nil {
			return nil, err
		}
	}
	if !h.engines[req.Engine] {
		return nil, hostErr(wire.ErrUnavailable, "engine "+req.Engine+" is not installed on this host")
	}
	for name, ref := range req.Inputs {
		if _, ok := h.files[ref]; !ok {
			return nil, hostErr(wire.ErrNotFound, "input "+name+" ref "+ref)
		}
	}
	if h.EngineFn != nil {
		n := len(h.Engines)
		return h.EngineFn(n, req)
	}
	res := &pluginkit.EngineResult{DurationMS: 1}
	names := req.Outputs
	if len(names) == 0 {
		names = []string{"out.bin"}
	}
	for _, name := range names {
		res.Outputs = append(res.Outputs, h.artefactLocked(name, []byte("engine:"+name)))
	}
	return res, nil
}

// Artefact registers a file "an engine produced" and answers its ref — what
// an EngineFn hands back in EngineResult.Outputs.
func (h *Host) Artefact(name string, data []byte) wire.OutputRef {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.artefactLocked(name, data)
}

func (h *Host) artefactLocked(name string, data []byte) wire.OutputRef {
	ref := "eng:" + strconv.Itoa(h.nextEng)
	h.nextEng++
	f := &hostFile{ref: ref, name: name, data: append([]byte(nil), data...), size: int64(len(data)), output: true}
	h.files[ref] = f
	h.order = append(h.order, ref)
	return wire.OutputRef{Ref: ref, Name: name}
}

// checkEngineArg mirrors the host's rule: every argument is a bare token —
// no path separators, no `..`, no `@list`, no protocol prefixes.
func checkEngineArg(a string) error {
	if len(a) > 4096 {
		return hostErr(wire.ErrInvalid, "argument too long")
	}
	if strings.ContainsAny(a, "/\\\x00") || strings.Contains(a, "..") {
		return hostErr(wire.ErrInvalid, "argument may not contain path separators or '..': "+a)
	}
	if strings.HasPrefix(a, "@") {
		return hostErr(wire.ErrInvalid, "argument may not read a file list: "+a)
	}
	lower := strings.ToLower(a)
	for _, bad := range []string{"file:", "http:", "https:", "ftp:", "pipe:", "tcp:", "udp:", "rtsp:", "rtmp:", "concat:", "subfile:", "data:", "-safe", "-protocol_whitelist", "--infilter", "-shell"} {
		if strings.HasPrefix(lower, bad) || strings.Contains(lower, "="+bad) {
			return hostErr(wire.ErrInvalid, "argument not allowed: "+a)
		}
	}
	return nil
}

// ── people, notifications, mail, network ───────────────────────────────

// UsersLookup searches the directory (permission `users:lookup`).
func (h *Host) UsersLookup(q string) ([]pluginkit.User, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if err := h.need("users:lookup"); err != nil {
		return nil, err
	}
	q = strings.ToLower(strings.TrimSpace(q))
	var out []pluginkit.User
	for _, u := range h.users {
		if q == "" || strings.Contains(strings.ToLower(u.Email), q) || strings.Contains(strings.ToLower(u.Name), q) {
			out = append(out, u)
		}
		if len(out) == 20 {
			break
		}
	}
	return out, nil
}

// NotifySend raises a notification (permission `notify:send`).
func (h *Host) NotifySend(n pluginkit.Notice) (int64, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if err := h.need("notify:send"); err != nil {
		return 0, err
	}
	if !h.NotifyConfigured {
		return 0, hostErr(wire.ErrUnavailable, "notifications are not available on this instance")
	}
	if n.Title == nil || n.Title["en"] == "" {
		return 0, hostErr(wire.ErrInvalid, "title (en) is required")
	}
	if n.Target != nil {
		if n.Target.Action != "" && !hasAction(h.manifest, n.Target.Action) {
			return 0, hostErr(wire.ErrInvalid, "target.action: no such action")
		}
		if n.Target.View != "" && !hasView(h.manifest, n.Target.View) {
			return 0, hostErr(wire.ErrInvalid, "target.view: no such view")
		}
	}
	h.Notices = append(h.Notices, n)
	return int64(len(h.Notices)), nil
}

// MailSend sends a plain-text mail (permission `mail:send`, quota per hour).
func (h *Host) MailSend(to, subject, body string) error { return h.MailSendIn("", to, subject, body) }

// MailSendIn is MailSend for a mail written in lang (pluginkit.MailSendIn).
func (h *Host) MailSendIn(lang, to, subject, body string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if err := h.need("mail:send"); err != nil {
		return err
	}
	if !h.MailConfigured {
		return hostErr(wire.ErrUnavailable, "mail is not configured on this instance")
	}
	if !strings.Contains(to, "@") || strings.ContainsAny(to, " \t\r\n") {
		return hostErr(wire.ErrInvalid, "to: not an email address")
	}
	if strings.TrimSpace(subject) == "" {
		return hostErr(wire.ErrInvalid, "subject is required")
	}
	if len(body) > 64<<10 {
		return hostErr(wire.ErrTooLarge, "body over 64 KiB")
	}
	if h.MailPerHour > 0 && h.mailsSent >= h.MailPerHour {
		return hostErr(wire.ErrBusy, "this plugin may send "+strconv.Itoa(h.MailPerHour)+" mails per hour")
	}
	h.mailsSent++
	h.Mails = append(h.Mails, Mail{To: to, Subject: subject, Body: body, Lang: lang})
	return nil
}

// HTTPDo performs an outbound request (permission `http:<host>`).
func (h *Host) HTTPDo(req pluginkit.HTTPRequest) (*pluginkit.HTTPResponse, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	host, err := hostOf(req.URL)
	if err != nil {
		return nil, err
	}
	if !h.hostGranted(host) {
		return nil, hostErr(wire.ErrPermissionDenied, "plugin was not granted http:"+host)
	}
	if isPrivateHost(host) {
		return nil, hostErr(wire.ErrPermissionDenied, "private and local addresses are refused")
	}
	method := strings.ToUpper(strings.TrimSpace(req.Method))
	if method == "" {
		method = "GET"
	}
	switch method {
	case "GET", "POST", "PUT", "PATCH", "DELETE", "HEAD":
	default:
		return nil, hostErr(wire.ErrInvalid, "method not allowed")
	}
	if len(req.Body) > 8<<20 {
		return nil, hostErr(wire.ErrTooLarge, "request body over 8 MiB")
	}
	h.Requests = append(h.Requests, req)
	if h.HTTPFn != nil {
		return h.HTTPFn(req)
	}
	return &pluginkit.HTTPResponse{Status: 200, Headers: map[string]string{}, Body: nil}, nil
}

// AssetFetch is asset_fetch: download url once into the app's cache, check
// it against the pinned sha256, hand back a ref to read it with. It enforces
// what the real host does — https, an `http:<host>` grant, no private
// address, max_bytes within 32 MiB, the hash — and serves a repeat from the
// cache without touching Network (Downloads does not grow).
func (h *Host) AssetFetch(url, sum string, maxBytes int64) (*pluginkit.Asset, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	sum = strings.ToLower(strings.TrimSpace(sum))
	if len(sum) != 64 || strings.Trim(sum, "0123456789abcdef") != "" {
		return nil, hostErr(wire.ErrInvalid, "sha256 must be 64 hex characters: an asset is always pinned")
	}
	if maxBytes <= 0 || maxBytes > 32<<20 {
		return nil, hostErr(wire.ErrInvalid, "max_bytes must be 1..33554432")
	}
	if !strings.HasPrefix(url, "https://") {
		return nil, hostErr(wire.ErrInvalid, "url must be https://host/…")
	}
	host, err := hostOf(url)
	if err != nil {
		return nil, err
	}
	if !h.hostGranted(host) {
		return nil, hostErr(wire.ErrPermissionDenied, "plugin was not granted http:"+host)
	}
	if isPrivateHost(host) {
		return nil, hostErr(wire.ErrPermissionDenied, "private and local addresses are refused")
	}
	if data, ok := h.assets[sum]; ok {
		return &pluginkit.Asset{Ref: h.addAssetLocked(sum, data), Size: int64(len(data)), Cached: true}, nil
	}
	if h.Offline {
		return nil, hostErr(wire.ErrUnavailable, "the network is not reachable")
	}
	data, ok := h.Network[url]
	if !ok {
		return nil, hostErr(wire.ErrUnavailable, "the server answered 404")
	}
	h.Downloads = append(h.Downloads, url)
	if int64(len(data)) > maxBytes {
		return nil, hostErr(wire.ErrTooLarge, "the asset is larger than max_bytes")
	}
	got := sha256.Sum256(data)
	if fmt.Sprintf("%x", got) != sum {
		return nil, hostErr(wire.ErrIntegrity, "the downloaded file does not match its pinned sha256")
	}
	h.assets[sum] = append([]byte(nil), data...)
	return &pluginkit.Asset{Ref: h.addAssetLocked(sum, h.assets[sum]), Size: int64(len(data))}, nil
}

func (h *Host) addAssetLocked(sum string, data []byte) string {
	ref := "asset:" + strconv.Itoa(h.nextAsset)
	h.nextAsset++
	h.files[ref] = &hostFile{ref: ref, name: sum, data: data, size: int64(len(data)), asset: true}
	return ref
}

func (h *Host) hostGranted(host string) bool {
	if h.grants["http:"+host] {
		return true
	}
	for p := range h.grants {
		if !strings.HasPrefix(p, "http:*.") {
			continue
		}
		suffix := strings.TrimPrefix(p, "http:*")
		if strings.HasSuffix(host, suffix) && len(host) > len(suffix) {
			return true
		}
	}
	return false
}

func hostOf(raw string) (string, error) {
	s := strings.TrimSpace(raw)
	low := strings.ToLower(s)
	if !strings.HasPrefix(low, "http://") && !strings.HasPrefix(low, "https://") {
		return "", hostErr(wire.ErrInvalid, "url must be http(s)://host/…")
	}
	s = s[strings.Index(s, "://")+3:]
	if i := strings.IndexAny(s, "/?#"); i >= 0 {
		s = s[:i]
	}
	if i := strings.LastIndex(s, "@"); i >= 0 {
		s = s[i+1:]
	}
	if strings.HasPrefix(s, "[") {
		if i := strings.Index(s, "]"); i > 0 {
			return strings.ToLower(s[1:i]), nil
		}
	}
	if i := strings.LastIndex(s, ":"); i >= 0 {
		s = s[:i]
	}
	if s == "" {
		return "", hostErr(wire.ErrInvalid, "url must be http(s)://host/…")
	}
	return strings.ToLower(s), nil
}

func isPrivateHost(host string) bool {
	switch host {
	case "localhost", "127.0.0.1", "::1", "0.0.0.0":
		return true
	}
	return strings.HasPrefix(host, "10.") || strings.HasPrefix(host, "192.168.") ||
		strings.HasPrefix(host, "169.254.") || strings.HasPrefix(host, "172.16.") ||
		strings.HasSuffix(host, ".local") || strings.HasSuffix(host, ".internal")
}

// ── shares (an app's public page IS a share) ───────────────────────────

// ShareCreate opens a public link for an outside participant (permission
// `public_pages`, jobs only).
func (h *Host) ShareCreate(req pluginkit.PageCreate) (*pluginkit.PageCreated, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if err := h.need("public_pages"); err != nil {
		return nil, err
	}
	if err := h.needJob("public links are opened"); err != nil {
		return nil, err
	}
	// An empty PageID is a page-less link — a plain share of a file, as the
	// host opens one; it has no page policy to follow.
	page, ok := pageByID(h.manifest, req.PageID)
	if !ok && req.PageID != "" {
		return nil, hostErr(wire.ErrInvalid, "the manifest declares no public page "+req.PageID)
	}
	// A link's purpose speaks every language the app declares, as the host
	// checks it (wasmplugin.checkPurpose).
	if p := req.Purpose; p != nil {
		langs := h.manifest.Languages
		if len(langs) == 0 {
			langs = []string{"en"}
		}
		for _, lang := range langs {
			if strings.TrimSpace(p.Label[lang]) == "" || (len(p.Revoke) > 0 && strings.TrimSpace(p.Revoke[lang]) == "") {
				return nil, hostErr(wire.ErrInvalid, "purpose: every text needs a "+lang+" spelling")
			}
		}
	}
	state, err := json.Marshal(req.State)
	if err != nil {
		return nil, hostErr(wire.ErrInvalid, "state is not JSON")
	}
	if len(state) > 64<<10 {
		return nil, hostErr(wire.ErrTooLarge, "state over 64 KiB")
	}
	pin := req.PIN
	switch {
	case pin == "auto":
		pin = "123456"
	case pin == "":
		if page.PIN == "required" {
			return nil, hostErr(wire.ErrInvalid, "this page requires a pin")
		}
	default:
		if len(pin) < 4 || len(pin) > 12 {
			return nil, hostErr(wire.ErrInvalid, "pin must be 4–12 characters")
		}
		if page.PIN == "none" {
			return nil, hostErr(wire.ErrInvalid, "this page takes no pin")
		}
	}
	ttl := req.TTLDays
	if ttl == 0 {
		ttl = page.DefaultTTLDays
	}
	if ttl == 0 {
		ttl = 14
	}
	if page.MaxTTLDays > 0 && ttl > page.MaxTTLDays {
		ttl = page.MaxTTLDays
	}
	h.tokenSeq++
	token := "tok" + strconv.Itoa(h.tokenSeq)
	sh := &Share{
		Token: token, PageID: req.PageID, Subject: req.Subject, PIN: pin,
		ExpiresAt: time.Now().Add(time.Duration(ttl) * 24 * time.Hour),
		MaxVisits: req.MaxVisits, State: state, Purpose: req.Purpose,
	}
	for _, f := range req.Files {
		src, ok := h.files[f.Ref]
		if !ok {
			return nil, hostErr(wire.ErrNotFound, "exposed file ref "+f.Ref)
		}
		ref := "pub:" + strconv.Itoa(h.nextPub)
		h.nextPub++
		name := f.Name
		if name == "" {
			name = src.name
		}
		copyF := &hostFile{ref: ref, name: name, data: append([]byte(nil), src.data...), size: src.size, mime: src.mime}
		h.files[ref] = copyF
		h.order = append(h.order, ref)
		sh.Files = append(sh.Files, wire.OutputRef{Ref: ref, Name: name})
	}
	h.shares[token] = sh
	minted := ""
	if req.PIN == "auto" {
		minted = pin
	}
	return &pluginkit.PageCreated{
		Token:     token,
		URL:       h.BaseURL + "/s/" + token,
		PIN:       minted,
		ExpiresAt: sh.ExpiresAt.UTC().Format(time.RFC3339),
	}, nil
}

// ShareRevoke ends a link early.
func (h *Host) ShareRevoke(token string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if err := h.need("public_pages"); err != nil {
		return err
	}
	sh, ok := h.shares[h.tokenOr(token)]
	if !ok {
		return hostErr(wire.ErrNotFound, "no such share")
	}
	sh.Revoked = true
	return nil
}

// ShareState reads a link's durable record into out. Inside a page event
// token may be "" (the current link).
func (h *Host) ShareState(token string, out any) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if err := h.need("public_pages"); err != nil {
		return err
	}
	sh, ok := h.shares[h.tokenOr(token)]
	if !ok {
		return hostErr(wire.ErrNotFound, "no such share")
	}
	if out == nil || len(sh.State) == 0 {
		return nil
	}
	return json.Unmarshal(sh.State, out)
}

// ShareStateSet replaces a link's durable record (≤ 64 KiB).
func (h *Host) ShareStateSet(token string, state any) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if err := h.need("public_pages"); err != nil {
		return err
	}
	sh, ok := h.shares[h.tokenOr(token)]
	if !ok {
		return hostErr(wire.ErrNotFound, "no such share")
	}
	b, err := json.Marshal(state)
	if err != nil {
		return hostErr(wire.ErrInvalid, "state is not JSON")
	}
	if len(b) > 64<<10 {
		return hostErr(wire.ErrTooLarge, "state over 64 KiB")
	}
	sh.State = b
	return nil
}

func (h *Host) tokenOr(token string) string {
	if token == "" {
		return h.share
	}
	return token
}

// ── signing ────────────────────────────────────────────────────────────

// HostSignInfo answers whether this instance can sign (permission `sign`).
func (h *Host) HostSignInfo() (*pluginkit.SignInfo, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if err := h.need("sign"); err != nil {
		return nil, err
	}
	if h.SignUnavailable != "" {
		return &pluginkit.SignInfo{Available: false, Reason: h.SignUnavailable}, nil
	}
	if err := h.ensureCALocked(); err != nil {
		return nil, err
	}
	return &pluginkit.SignInfo{Available: true, CACertPEM: h.caPEM, Algorithm: "ecdsa-p256-sha256"}, nil
}

func (h *Host) ensureCALocked() error {
	if h.caCert != nil {
		return nil
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return hostErr(wire.ErrInternal, "keygen")
	}
	tpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "plugintest CA"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(10 * 365 * 24 * time.Hour),
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &key.PublicKey, key)
	if err != nil {
		return hostErr(wire.ErrInternal, "issue: "+err.Error())
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return hostErr(wire.ErrInternal, "parse: "+err.Error())
	}
	h.caKey, h.caCert = key, cert
	h.caPEM = string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
	return nil
}

// CertIssue mints a real P-256 certificate from the fake tenant CA
// (permission `sign`, jobs only), so a signature a test produces actually
// verifies against the chain.
func (h *Host) CertIssue(commonName, email string, days int) (*pluginkit.IssuedCert, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if err := h.need("sign"); err != nil {
		return nil, err
	}
	if err := h.needJob("certificates are issued"); err != nil {
		return nil, err
	}
	if strings.TrimSpace(commonName) == "" {
		return nil, hostErr(wire.ErrInvalid, "common_name is required")
	}
	if h.SignUnavailable != "" {
		return nil, hostErr(wire.ErrUnavailable, h.SignUnavailable)
	}
	if err := h.budgetLocked(); err != nil {
		return nil, err
	}
	if err := h.ensureCALocked(); err != nil {
		return nil, err
	}
	if days <= 0 || days > 825 {
		days = 825
	}
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, hostErr(wire.ErrInternal, "keygen")
	}
	serial := big.NewInt(int64(len(h.keys) + 2))
	tpl := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: commonName},
		NotBefore:    time.Now().Add(-time.Minute),
		NotAfter:     time.Now().Add(time.Duration(days) * 24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageEmailProtection},
	}
	if email != "" {
		tpl.EmailAddresses = []string{email}
	}
	der, err := x509.CreateCertificate(rand.Reader, tpl, h.caCert, &priv.PublicKey, h.caKey)
	if err != nil {
		return nil, hostErr(wire.ErrInternal, "issue: "+err.Error())
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, hostErr(wire.ErrInternal, "parse: "+err.Error())
	}
	certPEM := string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
	ref := "key:" + strconv.Itoa(len(h.keys)+1)
	h.keys[ref] = &IssuedKey{Ref: ref, CommonName: commonName, Email: email, Cert: cert, CertPEM: certPEM, priv: priv}
	return &pluginkit.IssuedCert{
		KeyRef:   ref,
		CertPEM:  certPEM,
		ChainPEM: h.caPEM,
		NotAfter: cert.NotAfter.UTC().Format(time.RFC3339),
	}, nil
}

// PlatformSeal is the installation's seal for this app: the same key on
// every call, which KeyDestroy refuses — as on the real host (seal.go).
func (h *Host) PlatformSeal() (*pluginkit.IssuedCert, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if err := h.need("sign"); err != nil {
		return nil, err
	}
	if err := h.needJob("the platform seal is handed out"); err != nil {
		return nil, err
	}
	if h.SignUnavailable != "" {
		return nil, hostErr(wire.ErrUnavailable, h.SignUnavailable)
	}
	if err := h.ensureCALocked(); err != nil {
		return nil, err
	}
	if k, ok := h.keys[sealRef]; ok {
		return &pluginkit.IssuedCert{KeyRef: sealRef, CertPEM: k.CertPEM, ChainPEM: h.caPEM,
			NotAfter: k.Cert.NotAfter.UTC().Format(time.RFC3339)}, nil
	}
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, hostErr(wire.ErrInternal, "keygen")
	}
	tpl := &x509.Certificate{
		SerialNumber: big.NewInt(9_000_000),
		Subject:      pkix.Name{CommonName: "filex document seal", Organization: []string{"filex"}, OrganizationalUnit: []string{h.manifest.Name}},
		NotBefore:    time.Now().Add(-time.Minute),
		NotAfter:     time.Now().Add(10 * 365 * 24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageContentCommitment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageEmailProtection},
	}
	der, err := x509.CreateCertificate(rand.Reader, tpl, h.caCert, &priv.PublicKey, h.caKey)
	if err != nil {
		return nil, hostErr(wire.ErrInternal, "issue: "+err.Error())
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, hostErr(wire.ErrInternal, "parse: "+err.Error())
	}
	certPEM := string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
	h.keys[sealRef] = &IssuedKey{Ref: sealRef, CommonName: tpl.Subject.CommonName, Cert: cert, CertPEM: certPEM, priv: priv}
	return &pluginkit.IssuedCert{KeyRef: sealRef, CertPEM: certPEM, ChainPEM: h.caPEM,
		NotAfter: cert.NotAfter.UTC().Format(time.RFC3339)}, nil
}

// sealRef is the fake host's one platform seal.
const sealRef = "key:platform-seal"

// HostSign signs a sha256 digest with a host-held key. The answer is a
// DER-encoded ECDSA signature — a real one, verifiable with the leaf.
func (h *Host) HostSign(keyRef string, digest []byte) ([]byte, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if err := h.need("sign"); err != nil {
		return nil, err
	}
	if err := h.budgetLocked(); err != nil {
		return nil, err
	}
	k, ok := h.keys[keyRef]
	if !ok || k.Destroyed {
		return nil, hostErr(wire.ErrNotFound, "no such key")
	}
	if len(digest) != sha256.Size {
		return nil, hostErr(wire.ErrInvalid, "digest must be 32 bytes (sha256)")
	}
	sig, err := ecdsa.SignASN1(rand.Reader, k.priv, digest)
	if err != nil {
		return nil, hostErr(wire.ErrInternal, "sign")
	}
	return sig, nil
}

// KeyDestroy discards the private key; the certificate stays.
func (h *Host) KeyDestroy(keyRef string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if err := h.need("sign"); err != nil {
		return err
	}
	if keyRef == sealRef {
		return hostErr(wire.ErrInvalid, "the platform seal key is kept by the host; only a signer's key is destroyed")
	}
	k, ok := h.keys[keyRef]
	if !ok {
		return hostErr(wire.ErrNotFound, "no such key")
	}
	k.Destroyed, k.priv = true, nil
	return nil
}

func (h *Host) budgetLocked() error {
	if h.SignBudget <= 0 {
		return nil
	}
	if h.signsDone >= h.SignBudget {
		return hostErr(wire.ErrBusy, "signing rate limit")
	}
	h.signsDone++
	return nil
}

// Signer is a crypto.Signer over a key the fake host holds, so a PDF
// library that takes one (pdfsign, pkcs7) is exercised for real.
type Signer struct {
	Host   *Host
	KeyRef string
	Cert   *x509.Certificate
}

// NewSigner binds an issued certificate to this host.
func (h *Host) NewSigner(issued *pluginkit.IssuedCert) (*Signer, error) {
	blk, _ := pem.Decode([]byte(issued.CertPEM))
	if blk == nil {
		return nil, errors.New("plugintest: certificate unreadable")
	}
	cert, err := x509.ParseCertificate(blk.Bytes)
	if err != nil {
		return nil, err
	}
	return &Signer{Host: h, KeyRef: issued.KeyRef, Cert: cert}, nil
}

// Public returns the certificate's public key.
func (s *Signer) Public() crypto.PublicKey { return s.Cert.PublicKey }

// Sign asks the host to sign a sha256 digest.
func (s *Signer) Sign(_ io.Reader, digest []byte, opts crypto.SignerOpts) ([]byte, error) {
	if opts != nil && opts.HashFunc() != crypto.SHA256 {
		return nil, errors.New("plugintest: host signs sha256 digests only")
	}
	return s.Host.HostSign(s.KeyRef, digest)
}

// ── manifest lookups ───────────────────────────────────────────────────

func hasAction(m wire.Manifest, id string) bool {
	for _, a := range m.Actions {
		if a.ID == id {
			return true
		}
	}
	return false
}

func hasView(m wire.Manifest, id string) bool {
	for _, v := range m.Views {
		if v.ID == id {
			return true
		}
	}
	return false
}

func pageByID(m wire.Manifest, id string) (wire.PublicPage, bool) {
	for _, p := range m.PublicPages {
		if p.ID == id {
			return p, true
		}
	}
	return wire.PublicPage{}, false
}
