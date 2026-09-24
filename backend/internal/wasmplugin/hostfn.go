package wasmplugin

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	extism "github.com/extism/go-sdk"

	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/pathkey"
	"github.com/brf-tech/filex/backend/internal/storage"
	"github.com/brf-tech/filex/backend/pkg/pluginkit/wire"
)

// ── Host functions ─────────────────────────────────────────────────────
//
// Every function the guest can import lives in the `extism:host/user`
// namespace and follows one convention: one Extism memory pointer in (a JSON
// document, or a framed byte block for the two chunk functions), one pointer
// out (JSON, or a framed block). A failure is returned IN BAND as
// {"error":{"code","message"}} — never as a trap — so a plugin can tell
// "permission denied" from "file not found" and say something useful.
//
// Each function is wrapped by withScope(perm): it refuses without a call
// scope on the context (a describe call has none) and without the named
// permission in the plugin's GRANT — the list the admin approved at install,
// not whatever the manifest says today.
//
// Framed byte blocks (file_read result, file_write input):
//
//	file_read  in : {"handle":N,"max":M}           out: [status u8][bytes…]  status 0=data 1=eof 2=error(json follows)
//	file_write in : [handle u64 LE][bytes…]        out: {"written":N} | {"error":…}

const hostNamespace = "extism:host/user"

// hostFunctions builds the per-plugin function table. It is the same table
// for every plugin; the scope on the context is what differs per call.
func hostFunctions() []HostFunc {
	fns := []HostFunc{
		// ⚠ file_open / file_read / file_close check `files:read` THEMSELVES
		// (needRead): an asset_fetch download is the app's own file and is
		// read through the same calls without that permission.
		jsonFn("file_open", "", hfFileOpen),
		rawFn("file_read", "", hfFileRead),
		jsonFn("file_create", PermFilesWrite, hfFileCreate),
		rawFn("file_write", PermFilesWrite, hfFileWrite),
		jsonFn("file_close", "", hfFileClose),
		jsonFn("job_progress", "", hfJobProgress),
		jsonFn("settings_get", PermSettings, hfSettingsGet),
		jsonFn("state_get", PermState, hfStateGet),
		jsonFn("state_set", PermState, hfStateSet),
		jsonFn("state_list", PermState, hfStateList),
		jsonFn("file_lock", PermFilesLock, hfFileLock),
		jsonFn("file_unlock", PermFilesLock, hfFileUnlock),
		jsonFn("engine_available", "", hfEngineAvailable),
		jsonFn("engine_run", "", hfEngineRun),
		jsonFn("users_lookup", PermUsersLookup, hfUsersLookup),
		jsonFn("notify_send", PermNotifySend, hfNotifySend),
		jsonFn("mail_send", PermMailSend, hfMailSend),
		jsonFn("http_request", "", hfHTTPRequest),
		jsonFn("asset_fetch", "", hfAssetFetch),
		// share_* is the name from v3 on: an app's public page IS a share,
		// so it carries the share machinery (one revoke list, one expiry
		// policy, one PIN implementation). public_page_* stays registered
		// because modules built against v1/v2 import it by that name.
		jsonFn("share_create", PermPublicPages, hfShareCreate),
		jsonFn("share_revoke", PermPublicPages, hfShareRevoke),
		jsonFn("share_state", PermPublicPages, hfShareState),
		jsonFn("share_pin", PermPublicPages, hfSharePIN),
		jsonFn("public_page_create", PermPublicPages, hfShareCreate),
		jsonFn("public_page_revoke", PermPublicPages, hfShareRevoke),
		jsonFn("public_page_state", PermPublicPages, hfShareState),
		jsonFn("host_sign_info", PermSign, hfSignInfo),
		jsonFn("cert_issue", PermSign, hfCertIssue),
		jsonFn("host_sign", PermSign, hfHostSign),
		jsonFn("key_destroy", PermSign, hfKeyDestroy),
	}
	return fns
}

type jsonHandler func(ctx context.Context, s *Scope, in json.RawMessage) (any, error)
type rawHandler func(ctx context.Context, s *Scope, in []byte) ([]byte, error)

// jsonFn wraps a JSON-in/JSON-out handler.
func jsonFn(name string, perm Permission, h jsonHandler) HostFunc {
	fn := extism.NewHostFunctionWithStack(name,
		func(ctx context.Context, p *extism.CurrentPlugin, stack []uint64) {
			in, err := p.ReadBytes(stack[0])
			if err != nil {
				stack[0] = writeJSON(p, errEnvelope(hostErr(wire.ErrInvalid, "unreadable input")))
				return
			}
			s := scopeFrom(ctx)
			if s == nil {
				stack[0] = writeJSON(p, errEnvelope(hostErr(wire.ErrPermissionDenied, "no call scope")))
				return
			}
			if perm != "" && !s.plugin.Grants.Has(perm) {
				stack[0] = writeJSON(p, errEnvelope(hostErr(wire.ErrPermissionDenied, "plugin was not granted "+string(perm))))
				return
			}
			out, err := h(ctx, s, in)
			if err != nil {
				stack[0] = writeJSON(p, errEnvelope(err))
				return
			}
			stack[0] = writeJSON(p, out)
		},
		[]extism.ValueType{extism.ValueTypePTR}, []extism.ValueType{extism.ValueTypePTR})
	fn.SetNamespace(hostNamespace)
	return fn
}

// rawFn wraps a framed-bytes handler.
func rawFn(name string, perm Permission, h rawHandler) HostFunc {
	fn := extism.NewHostFunctionWithStack(name,
		func(ctx context.Context, p *extism.CurrentPlugin, stack []uint64) {
			in, err := p.ReadBytes(stack[0])
			if err != nil {
				stack[0] = writeFrame(p, 2, errJSON(hostErr(wire.ErrInvalid, "unreadable input")))
				return
			}
			s := scopeFrom(ctx)
			if s == nil {
				stack[0] = writeFrame(p, 2, errJSON(hostErr(wire.ErrPermissionDenied, "no call scope")))
				return
			}
			if perm != "" && !s.plugin.Grants.Has(perm) {
				stack[0] = writeFrame(p, 2, errJSON(hostErr(wire.ErrPermissionDenied, "plugin was not granted "+string(perm))))
				return
			}
			out, err := h(ctx, s, in)
			if err != nil {
				stack[0] = writeFrame(p, 2, errJSON(err))
				return
			}
			off, werr := p.WriteBytes(out)
			if werr != nil {
				stack[0] = 0
				return
			}
			stack[0] = off
		},
		[]extism.ValueType{extism.ValueTypePTR}, []extism.ValueType{extism.ValueTypePTR})
	fn.SetNamespace(hostNamespace)
	return fn
}

func writeJSON(p *extism.CurrentPlugin, v any) uint64 {
	b, err := json.Marshal(v)
	if err != nil {
		b = errJSON(hostErr(wire.ErrInternal, "marshal"))
	}
	off, err := p.WriteBytes(b)
	if err != nil {
		return 0
	}
	return off
}

func writeFrame(p *extism.CurrentPlugin, status byte, body []byte) uint64 {
	out := make([]byte, 1+len(body))
	out[0] = status
	copy(out[1:], body)
	off, err := p.WriteBytes(out)
	if err != nil {
		return 0
	}
	return off
}

func errEnvelope(err error) map[string]any {
	he := asHostError(err)
	return map[string]any{"error": wire.HostError{Code: he.Code, Message: he.Message}}
}

func errJSON(err error) []byte {
	b, _ := json.Marshal(errEnvelope(err))
	return b
}

// ── files ──────────────────────────────────────────────────────────────

func hfFileOpen(ctx context.Context, s *Scope, in json.RawMessage) (any, error) {
	var req struct {
		Ref string `json:"ref"`
	}
	if err := json.Unmarshal(in, &req); err != nil {
		return nil, hostErr(wire.ErrInvalid, "bad json")
	}
	if f, ok := s.file(req.Ref); !ok || !f.Asset {
		if err := needRead(s); err != nil {
			return nil, err
		}
	}
	h, size, err := s.openRead(ctx, req.Ref)
	if err != nil {
		return nil, err
	}
	return map[string]any{"handle": h, "size": size}, nil
}

// needRead is the `files:read` check file_open used to get from its table
// entry. A handle can only be one file_open returned, so read and close are
// checked against the file the handle was opened on.
func needRead(s *Scope) error {
	if !s.plugin.Grants.Has(PermFilesRead) {
		return hostErr(wire.ErrPermissionDenied, "plugin was not granted "+string(PermFilesRead))
	}
	return nil
}

// handleIsAsset reports whether a handle reads an asset_fetch download.
func (s *Scope) handleIsAsset(id uint64) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	h := s.handles[id]
	return h != nil && h.f != nil && h.f.Asset
}

func hfFileRead(_ context.Context, s *Scope, in []byte) ([]byte, error) {
	var req struct {
		Handle uint64 `json:"handle"`
		Max    int    `json:"max"`
	}
	if err := json.Unmarshal(in, &req); err != nil {
		return nil, hostErr(wire.ErrInvalid, "bad json")
	}
	if !s.handleIsAsset(req.Handle) {
		if err := needRead(s); err != nil {
			return nil, err
		}
	}
	b, eof, err := s.read(req.Handle, req.Max)
	if err != nil {
		return nil, err
	}
	if eof {
		return []byte{1}, nil
	}
	out := make([]byte, 1+len(b))
	out[0] = 0
	copy(out[1:], b)
	return out, nil
}

func hfFileCreate(_ context.Context, s *Scope, in json.RawMessage) (any, error) {
	var req struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(in, &req); err != nil {
		return nil, hostErr(wire.ErrInvalid, "bad json")
	}
	h, ref, err := s.create(req.Name)
	if err != nil {
		return nil, err
	}
	return map[string]any{"handle": h, "ref": ref}, nil
}

func hfFileWrite(_ context.Context, s *Scope, in []byte) ([]byte, error) {
	if len(in) < 8 {
		return nil, hostErr(wire.ErrInvalid, "short frame")
	}
	h := binary.LittleEndian.Uint64(in[:8])
	body := in[8:]
	if len(body) > maxChunk {
		return nil, hostErr(wire.ErrTooLarge, "chunk over 1 MiB")
	}
	if err := s.write(h, body); err != nil {
		return nil, err
	}
	return []byte(fmt.Sprintf(`{"written":%d}`, len(body))), nil
}

func hfFileClose(_ context.Context, s *Scope, in json.RawMessage) (any, error) {
	var req struct {
		Handle uint64 `json:"handle"`
	}
	if err := json.Unmarshal(in, &req); err != nil {
		return nil, hostErr(wire.ErrInvalid, "bad json")
	}
	if err := s.closeHandle(req.Handle); err != nil {
		return nil, err
	}
	return map[string]any{"ok": true}, nil
}

// ── progress ───────────────────────────────────────────────────────────

func hfJobProgress(_ context.Context, s *Scope, in json.RawMessage) (any, error) {
	var req struct {
		Done    int64  `json:"done"`
		Total   int64  `json:"total"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(in, &req); err != nil {
		return nil, hostErr(wire.ErrInvalid, "bad json")
	}
	s.mu.Lock()
	fn := s.progress
	s.mu.Unlock()
	if fn != nil {
		fn(req.Done, req.Total, clip(strings.TrimSpace(req.Message), 200))
	}
	return map[string]any{"ok": true}, nil
}

// ── settings ───────────────────────────────────────────────────────────

func hfSettingsGet(ctx context.Context, s *Scope, in json.RawMessage) (any, error) {
	var req struct {
		Key string `json:"key"`
	}
	if err := json.Unmarshal(in, &req); err != nil {
		return nil, hostErr(wire.ErrInvalid, "bad json")
	}
	vals, err := s.reg.openSettings(ctx, s.plugin)
	if err != nil {
		return nil, hostErr(wire.ErrUnavailable, "settings: "+err.Error())
	}
	v, ok := vals[req.Key]
	return map[string]any{"value": v, "found": ok}, nil
}

// ── per-file state ─────────────────────────────────────────────────────

func stateKey(s *Scope, ref string) (string, error) {
	h, _, err := stateKeyRel(s, ref)
	return h, err
}

// stateKeyRel is stateKey plus the path it hashed, which a write stores so
// a listing can name the file later.
func stateKeyRel(s *Scope, ref string) (string, string, error) {
	f, ok := s.file(ref)
	if !ok || f.Rel == "" {
		return "", "", hostErr(wire.ErrNotFound, "state is kept per storage file; ref is not one")
	}
	rel := strings.TrimPrefix(f.Rel, "/")
	return pathkey.Hash(s.storageID, "/"+rel), rel, nil
}

func hfStateGet(ctx context.Context, s *Scope, in json.RawMessage) (any, error) {
	var req struct {
		Ref string `json:"ref"`
		Key string `json:"key"`
	}
	if err := json.Unmarshal(in, &req); err != nil || req.Key == "" {
		return nil, hostErr(wire.ErrInvalid, "bad json")
	}
	ph, err := stateKey(s, req.Ref)
	if err != nil {
		return nil, err
	}
	v, found, err := s.reg.opts.Store.GetAppPluginState(ctx, s.plugin.Row.ID, s.storageID, ph, req.Key)
	if err != nil {
		return nil, hostErr(wire.ErrUnavailable, "state: "+err.Error())
	}
	return map[string]any{"value": v, "found": found}, nil
}

func hfStateSet(ctx context.Context, s *Scope, in json.RawMessage) (any, error) {
	var req struct {
		Ref   string  `json:"ref"`
		Key   string  `json:"key"`
		Value *string `json:"value"`
	}
	if err := json.Unmarshal(in, &req); err != nil || req.Key == "" {
		return nil, hostErr(wire.ErrInvalid, "bad json")
	}
	if !s.writable && s.page == nil {
		return nil, hostErr(wire.ErrPermissionDenied, "this call may not write state")
	}
	// `@me` is how a listing SHOWS a personal key to its person
	// (wire.PersonalState); a key stored under that name would be shown to
	// everybody as theirs.
	if strings.HasSuffix(req.Key, wire.PersonalStateSuffix) {
		return nil, hostErr(wire.ErrInvalid, "a personal key is written as <key>@<user id> (wire.PersonalState), never @me")
	}
	ph, rel, err := stateKeyRel(s, req.Ref)
	if err != nil {
		return nil, err
	}
	if req.Value == nil {
		if err := s.reg.opts.Store.DeleteAppPluginState(ctx, s.plugin.Row.ID, s.storageID, ph, req.Key); err != nil {
			return nil, hostErr(wire.ErrUnavailable, "state: "+err.Error())
		}
		return map[string]any{"ok": true}, nil
	}
	if len(*req.Value) > maxStateBytes {
		return nil, hostErr(wire.ErrTooLarge, "state value over 64 KiB")
	}
	if err := s.reg.opts.Store.SetAppPluginState(ctx, s.plugin.Row.ID, s.storageID, ph, rel, req.Key, *req.Value); err != nil {
		return nil, hostErr(wire.ErrUnavailable, "state: "+err.Error())
	}
	return map[string]any{"ok": true}, nil
}

const maxStateBytes = 64 << 10

// state_list answers "which files do I keep this key on?" — the plugin's own
// state joined back to the files it belongs to.
//
// It exists because a home screen cannot be written without it: an app that
// records "this document is waiting for a signature" on each file had no way
// to find those files again, so its own list was empty until somebody
// navigated to one. A deleted file drops out by itself (the join is to live
// node rows), the answer is this plugin's state and nothing else, and every
// row is filtered through the ASKING PERSON's permission — a listing is not
// a way around the ACL.
const maxStateListLimit = 500

func hfStateList(ctx context.Context, s *Scope, in json.RawMessage) (any, error) {
	var req struct {
		Key   string `json:"key"`
		Limit int    `json:"limit"`
	}
	if err := json.Unmarshal(in, &req); err != nil {
		return nil, hostErr(wire.ErrInvalid, "bad json")
	}
	if req.Limit <= 0 || req.Limit > maxStateListLimit {
		req.Limit = 100
	}
	rows, err := s.reg.opts.Store.ListAppPluginStateFiles(ctx, s.plugin.Row.ID, strings.TrimSpace(req.Key), req.Limit)
	if err != nil {
		return nil, hostErr(wire.ErrUnavailable, "state: "+err.Error())
	}
	items := make([]map[string]any, 0, len(rows))
	for _, f := range rows {
		rel := strings.TrimPrefix(f.Path, "/")
		if !s.mayList(ctx, f.StorageID, rel) {
			continue
		}
		items = append(items, map[string]any{
			"path":  f.StorageName + "://" + rel,
			"name":  f.Name,
			"key":   f.Key,
			"value": f.Value,
		})
	}
	return map[string]any{"items": items}, nil
}

// mayList answers whether this call may be told about a state row's file.
//
// A PERSON's listing is narrowed by that person's ACL: an app may keep state
// on a document the asker was never given, and a listing is not a way around
// the ACL. That is the rule, and it is unchanged.
//
// ⚠⚠ The hourly WAKE-UP has no person — the host started it on its own clock
// — so there is no ACL to narrow it BY, and running it through one anyway
// answers "nobody may see anything": acl.CanSee refuses a nil user, which is
// the right answer to "may this stranger?" and the wrong answer to "may the
// app read its own bookkeeping?". So a system call lists the app's own rows,
// which are the app's, and nothing else: the rows are already filtered to
// THIS plugin by ListAppPluginStateFiles.
//
// ⚠ Deliberately keyed on the system MARKER and never on "the actor is nil".
// A public page call is actor-less too, and that one is a visitor who found a
// link — for them the nil-user refusal is exactly right, and they keep it.
func (s *Scope) mayList(ctx context.Context, storageID int64, rel string) bool {
	if s.system {
		return true
	}
	return s.reg.visible == nil || s.reg.visible(ctx, s.actor, storageID, rel)
}

// ── file_lock / file_unlock ─────────────────────────────────────────────
//
// A lock freezes one storage file for everyone — administrators included —
// until the plugin lifts it or ttl_days pass: the ACL caps every caller at
// viewer on that path and folder moves/renames/deletes that would carry it
// along are refused with 423. The plugin's own outputs still land: the ops
// worker writes through the sink, and a job of the LOCKING plugin passes the
// submit-time ACL as if the lock were not there. Locks are taken from action
// jobs only (like state writes); a view that wants one asks for a job.

const (
	lockMaxDays     = 365
	lockDefaultDays = 30
)

func (s *Scope) targetRel(ref, qualified string) (string, error) {
	if ref != "" {
		f, ok := s.file(ref)
		if !ok || f.Rel == "" {
			return "", hostErr(wire.ErrNotFound, "ref is not a storage file")
		}
		return strings.TrimPrefix(f.Rel, "/"), nil
	}
	qualified = strings.TrimSpace(qualified)
	if qualified == "" {
		return "", hostErr(wire.ErrInvalid, "ref or path is required")
	}
	if i := strings.Index(qualified, "://"); i >= 0 {
		if s.storageName == "" || qualified[:i] != s.storageName {
			return "", hostErr(wire.ErrPermissionDenied, "path is on another storage than this call")
		}
		qualified = qualified[i+3:]
	}
	rel := strings.Trim(cleanRel(qualified), "/")
	if rel == "" || rel == "." {
		return "", hostErr(wire.ErrInvalid, "path names the storage root")
	}
	return rel, nil
}

func cleanRel(p string) string {
	p = strings.ReplaceAll(p, "\\", "/")
	out := make([]string, 0, 8)
	for _, seg := range strings.Split(p, "/") {
		switch seg {
		case "", ".":
		case "..":
			if len(out) > 0 {
				out = out[:len(out)-1]
			}
		default:
			out = append(out, seg)
		}
	}
	return strings.Join(out, "/")
}

func hfFileLock(ctx context.Context, s *Scope, in json.RawMessage) (any, error) {
	var req struct {
		Ref     string `json:"ref"`
		Path    string `json:"path"`
		TTLDays int    `json:"ttl_days"`
		Reason  string `json:"reason"`
		// ReasonKey names one of the manifest's `messages`, so filex can say
		// the reason in each reader's language (LockReason); ReasonArgs fill
		// its {placeholders}.
		ReasonKey  string            `json:"reason_key"`
		ReasonArgs map[string]string `json:"reason_args"`
	}
	if err := json.Unmarshal(in, &req); err != nil {
		return nil, hostErr(wire.ErrInvalid, "bad json")
	}
	if !s.writable {
		return nil, hostErr(wire.ErrPermissionDenied, "locks are taken from action jobs only")
	}
	if req.ReasonKey != "" {
		stored, err := lockMessage(s.plugin, req.ReasonKey, req.ReasonArgs)
		if err != nil {
			return nil, err
		}
		req.Reason = stored
	}
	if req.TTLDays < lockUntilLifted || req.TTLDays > lockMaxDays {
		return nil, hostErr(wire.ErrInvalid, fmt.Sprintf("ttl_days must be %d (until lifted) or 0..%d", lockUntilLifted, lockMaxDays))
	}
	// A lock on one of this job's OWN outputs is promised, like a share of
	// one (public_promised.go): the file has no path yet, and the lock is
	// taken the moment runJob commits it — or never, if the job fails.
	//
	// ⚠⚠ Why (2026-09-22, the owner): "lock the signed file when every
	// signature is in". The signed document is the job's output — a new file
	// beside the original — and a lock taken after the job ended would be a
	// second job, with a window in between where anybody could change it.
	if req.Ref != "" {
		if f, ok := s.file(req.Ref); ok && f.Output && f.Rel == "" {
			l := &model.AppPluginLock{StorageID: s.storageID, PluginID: s.plugin.Row.ID, PluginName: s.plugin.Row.Name,
				Reason: clip(strings.TrimSpace(req.Reason), 190), Until: lockUntil(req.TTLDays)}
			if s.actor != nil && s.actor.ID > 0 {
				id := s.actor.ID
				l.CreatedBy = &id
			}
			s.promiseLock(req.Ref, l)
			s.plugin.log("info", "lock promised for "+req.Ref+", taken when the job writes it")
			return map[string]any{"ok": true, "until": l.Until, "promised": true}, nil
		}
	}
	rel, err := s.targetRel(req.Ref, req.Path)
	if err != nil {
		return nil, err
	}
	// Files only. A folder lock would read differently on each write door
	// (the browser upload asks the ACL about the FOLDER, the staged one about
	// the file it is creating), and half a promise is worse than none: say so
	// here instead of freezing a folder on one path and not the other.
	if s.drv != nil {
		obj, serr := s.drv.Stat(ctx, rel)
		if serr != nil {
			return nil, hostErr(wire.ErrNotFound, "lock: "+rel)
		}
		if obj.Kind == storage.KindDirectory {
			return nil, hostErr(wire.ErrInvalid, "locks are for files, not folders")
		}
	}
	ph := pathkey.Hash(s.storageID, "/"+rel)
	if cur, err := s.reg.opts.Store.GetAppPluginLock(ctx, s.storageID, ph); err == nil && cur != nil && cur.Live(time.Now()) && cur.PluginID != s.plugin.Row.ID {
		return nil, hostErr(wire.ErrBusy, "already locked by app "+cur.PluginName)
	}
	until := lockUntil(req.TTLDays)
	l := &model.AppPluginLock{
		StorageID: s.storageID, PathHash: ph, Rel: rel, PluginID: s.plugin.Row.ID, PluginName: s.plugin.Row.Name,
		Reason: clip(strings.TrimSpace(req.Reason), 190), Until: until,
	}
	if s.actor != nil && s.actor.ID > 0 {
		id := s.actor.ID
		l.CreatedBy = &id
	}
	if err := s.reg.opts.Store.PutAppPluginLock(ctx, l); err != nil {
		return nil, hostErr(wire.ErrUnavailable, "lock: "+err.Error())
	}
	s.plugin.log("info", "locked "+rel+" "+lockWords(until))
	return map[string]any{"ok": true, "until": until}, nil
}

// lockMsgPrefix marks a lock reason stored as a manifest message: the row
// keeps `msg:<key>` (and its arguments as JSON after a space), and the words
// are made from the app's manifest each time a person reads them, in that
// person's language (Registry.LockReason). The column is 190 bytes: a reason
// in every language would not fit, a key and a few arguments do.
const lockMsgPrefix = "msg:"

// lockMessage checks a message reason and gives the value to store.
func lockMessage(p *Installed, key string, args map[string]string) (string, error) {
	if _, ok := p.Manifest.Messages[key]; !ok {
		return "", hostErr(wire.ErrInvalid, "reason_key: the manifest declares no message "+key)
	}
	if len(args) > 8 {
		return "", hostErr(wire.ErrInvalid, "reason_args: at most 8")
	}
	stored := lockMsgPrefix + key
	if len(args) > 0 {
		b, _ := json.Marshal(args)
		stored += " " + string(b)
	}
	if len(stored) > 190 {
		return "", hostErr(wire.ErrTooLarge, "reason_key and reason_args take more than 190 bytes")
	}
	return stored, nil
}

// lockUntilLifted is ttl_days' "no end": the lock holds until the app lifts
// it or an administrator does (Admin → Apps → Locks, audited). An app asks
// for it when a file must stay as it is for good — the signing app's
// "lock the signed file when every signature is in".
const lockUntilLifted = -1

// lockUntil is the end of a lock asked for ttlDays: nil for until-lifted,
// the default for 0.
func lockUntil(ttlDays int) *time.Time {
	if ttlDays == lockUntilLifted {
		return nil
	}
	if ttlDays == 0 {
		ttlDays = lockDefaultDays
	}
	t := time.Now().Add(time.Duration(ttlDays) * 24 * time.Hour)
	return &t
}

func lockWords(until *time.Time) string {
	if until == nil {
		return "until it is lifted"
	}
	return "until " + until.Format(time.RFC3339)
}

func hfFileUnlock(ctx context.Context, s *Scope, in json.RawMessage) (any, error) {
	var req struct {
		Ref  string `json:"ref"`
		Path string `json:"path"`
	}
	if err := json.Unmarshal(in, &req); err != nil {
		return nil, hostErr(wire.ErrInvalid, "bad json")
	}
	if !s.writable {
		return nil, hostErr(wire.ErrPermissionDenied, "locks are lifted from action jobs only")
	}
	rel, err := s.targetRel(req.Ref, req.Path)
	if err != nil {
		return nil, err
	}
	ph := pathkey.Hash(s.storageID, "/"+rel)
	cur, err := s.reg.opts.Store.GetAppPluginLock(ctx, s.storageID, ph)
	if err != nil {
		return nil, hostErr(wire.ErrUnavailable, "lock: "+err.Error())
	}
	if cur == nil {
		return map[string]any{"ok": true, "was_locked": false}, nil
	}
	if cur.PluginID != s.plugin.Row.ID {
		return nil, hostErr(wire.ErrPermissionDenied, "locked by app "+cur.PluginName+", not by this one")
	}
	if err := s.reg.opts.Store.DeleteAppPluginLock(ctx, s.storageID, ph); err != nil {
		return nil, hostErr(wire.ErrUnavailable, "lock: "+err.Error())
	}
	s.plugin.log("info", "unlocked "+rel)
	return map[string]any{"ok": true, "was_locked": true}, nil
}

// ── engines ────────────────────────────────────────────────────────────

func hfEngineAvailable(_ context.Context, s *Scope, in json.RawMessage) (any, error) {
	var req struct {
		Engine string `json:"engine"`
	}
	if err := json.Unmarshal(in, &req); err != nil {
		return nil, hostErr(wire.ErrInvalid, "bad json")
	}
	ok := s.plugin.Grants.HasEngine(req.Engine) && s.reg.engines.available(req.Engine)
	return map[string]any{"available": ok}, nil
}

func hfEngineRun(ctx context.Context, s *Scope, in json.RawMessage) (any, error) {
	var req engineRequest
	if err := json.Unmarshal(in, &req); err != nil {
		return nil, hostErr(wire.ErrInvalid, "bad json")
	}
	if !s.plugin.Grants.HasEngine(req.Engine) {
		return nil, hostErr(wire.ErrPermissionDenied, "plugin was not granted engines:"+req.Engine)
	}
	if !s.writable {
		return nil, hostErr(wire.ErrPermissionDenied, "engines run in action jobs only")
	}
	return s.reg.engines.run(ctx, s, &req)
}
