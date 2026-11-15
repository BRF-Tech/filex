package handlers

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"

	"github.com/brf-tech/filex/backend/internal/apierr"
	"github.com/brf-tech/filex/backend/internal/auth"
	apitoken "github.com/brf-tech/filex/backend/internal/auth/drivers/apitoken"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/storage"
	syncpkg "github.com/brf-tech/filex/backend/internal/sync"
	"github.com/brf-tech/filex/backend/internal/tenanturl"
	"github.com/brf-tech/filex/backend/internal/watchin"
	"github.com/brf-tech/filex/backend/internal/watchproto"
)

// # filex-watch on the server (docs/FILEX-WATCH.md)
//
// A storage whose disk is on another machine - a NAS share mounted over the
// network, the folder an SFTP storage points at - changes without filex's own
// watch hearing of it. filex-watch runs on THAT machine and reports which
// folders changed; filex looks at those folders alone.
//
//	POST   /api/storages/{id}/watch/events            the agent's door (a filex-watch key, no session)
//	GET    /api/admin/storages/{id}/watch             → {storage_id, storage, server, release, keys, setup}
//	POST   /api/admin/storages/{id}/watch/keys        {name} → 201 {key, row, setup}
//	DELETE /api/admin/storages/{id}/watch/keys/{key}  → {ok: true}
//
// The key is an API token row (model.TokenSourceWatch) and not a credential
// system of its own: listed, revoked and last-used like every API key, and
// shown once. What makes it a key for ONE door: its list names only
// `watch:<storage id>`, its hash is taken over a domain of its own
// (apitoken.HashWatchKey) so no other door's lookup finds it, and every door
// that resolves a token refuses it all the same (model.APIToken.DoorOnly). A
// leaked key makes filex look at one storage's folders sooner; it reads,
// writes and lists nothing.
//
// The door asks of the key's OWNER, as they are now, what a key asks of its
// owner everywhere: the account is on and its tenant not suspended
// (auth.RefuseBlockedAccount), it is still an administrator - only an
// administrator makes a key here - and its tenant still has the storage
// (db.AccountReaches).
type StorageWatch struct {
	store   db.Store
	inbox   *watchin.Inbox
	agents  *watchin.Agents
	tenants tenanturl.Resolver
	// perKey holds one key to a report rate; perAddr holds one address to a
	// request rate, keys or no keys (a flood of made-up keys included).
	perKey  *ipLimiter
	perAddr *ipLimiter
	// instance names this process to the agents (watchproto.Answer.Instance):
	// one that hears a new one asks for a resync, because the folders this
	// process had taken and not yet handed on died with the one before.
	instance string
}

// Limits of the door and of the keys.
const (
	// watchReportsPerMinute is one key's report rate. An agent sends a batch
	// a second at most while a disk is busy, and a heartbeat a minute.
	watchReportsPerMinute = 120
	// watchRequestsPerMinute is one address's (an IPv6 /64's) request rate.
	watchRequestsPerMinute = 600
	// watchKeysPerStorage caps the keys one storage may have.
	watchKeysPerStorage = 20
	// watchKeyNameMax is the longest key name, in characters.
	watchKeyNameMax = 100
)

// The published program: the release assets and the image (filex-watch's own
// release line, watchproto.AgentVersion).
const (
	watchReleaseDownload = "https://github.com/BRF-Tech/filex/releases/download/"
	watchImage           = "ghcr.io/brf-tech/filex-watch"
)

// NewStorageWatch builds the door and the key surface. r is the catalogue
// (the server's sync.Worker); a storage it no longer has (ErrNoSyncer) has
// its reported folders dropped.
func NewStorageWatch(store db.Store, r watchin.Rescanner) *StorageWatch {
	in := watchin.New(r)
	in.Gone = func(err error) bool { return errors.Is(err, syncpkg.ErrNoSyncer) }
	return &StorageWatch{
		store:    store,
		inbox:    in,
		agents:   &watchin.Agents{},
		perKey:   newIPLimiter(watchReportsPerMinute, time.Minute),
		perAddr:  newIPLimiter(watchRequestsPerMinute, time.Minute),
		instance: newWatchInstance(),
	}
}

// newWatchInstance is a process's name for the agents: random, made once.
func newWatchInstance() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// watchSupported reports whether a filex-watch agent can serve a storage on
// driver: the driver's own answer (storage.WatchAgentApplies), which the
// storage page reads from the driver descriptor (`watch_agent`) too.
func watchSupported(driver string) bool {
	drv, err := storage.Get(driver)
	return err == nil && storage.WatchAgentApplies(drv)
}

// AttachTenants wires the shared origin resolver: the address the install
// commands name is the one the administrator reached filex at.
func (h *StorageWatch) AttachTenants(rv tenanturl.Resolver) { h.tenants = rv }

// ── the agent's door ───────────────────────────────────────────────────────

// Events serves POST /api/storages/{id}/watch/events. Every refusal is the
// apierr envelope - a code the agent branches on and a sentence it logs.
func (h *StorageWatch) Events(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if !h.perAddr.allow(clientIP(r)) {
		w.Header().Set("Retry-After", "60")
		writeError(w, r, http.StatusTooManyRequests, "watch_rate_limited", nil)
		return
	}
	storageID, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || storageID <= 0 {
		writeError(w, r, http.StatusNotFound, "watch_storage_gone", nil)
		return
	}
	raw := apitoken.ExtractToken(r)
	if raw == "" {
		writeError(w, r, http.StatusUnauthorized, "watch_key_missing", nil)
		return
	}
	tok := h.keyOf(r, raw)
	if tok == nil {
		writeError(w, r, http.StatusUnauthorized, "watch_key_unknown", nil)
		return
	}
	if sid, _ := tok.WatchStorage(); sid != storageID {
		writeError(w, r, http.StatusForbidden, "watch_key_other_storage", apierr.Params{"storage": strconv.FormatInt(sid, 10)})
		return
	}
	if !h.perKey.allowN("watch-key:"+strconv.FormatInt(tok.ID, 10), 1) {
		w.Header().Set("Retry-After", "60")
		writeError(w, r, http.StatusTooManyRequests, "watch_rate_limited", nil)
		return
	}
	st, ok := h.ownerMayReport(w, r, tok, storageID)
	if !ok {
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, watchproto.MaxBodyBytes)
	var rep watchproto.Report
	if err := json.NewDecoder(r.Body).Decode(&rep); err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			writeError(w, r, http.StatusRequestEntityTooLarge, "watch_report_too_large", apierr.Params{"max_kb": strconv.Itoa(watchproto.MaxBodyBytes >> 10)})
			return
		}
		writeError(w, r, http.StatusBadRequest, "watch_report_invalid", apierr.Params{"reason": "the body is not a JSON report"})
		return
	}
	if err := rep.Validate(); err != nil {
		writeError(w, r, http.StatusBadRequest, "watch_report_invalid", apierr.Params{"reason": err.Error()})
		return
	}
	folders, ignoredF := watchScope(rep.Folders)
	subtrees, ignoredS := watchScope(rep.Subtrees)
	_ = h.store.TouchAPIToken(ctx, tok.ID)

	resync := subtrees
	if rep.Resync {
		resync = append([]string{"/"}, subtrees...)
	}
	queued := len(folders) + len(resync)
	if h.agents.Note(tok.ID, rep) {
		// The same batch again: the agent did not hear the first answer.
		// Its folders are already waiting.
		queued = 0
	} else {
		h.inbox.Add(storageID, folders, resync)
	}
	writeJSON(w, http.StatusAccepted, watchproto.Answer{
		Accepted: true,
		Seq:      rep.Seq,
		Queued:   queued,
		Ignored:  ignoredF + ignoredS,
		Resync:   rep.Resync,
		Storage:  st.Name,
		Instance: h.instance,
	})
}

// keyOf is the filex-watch key raw names, nil for anything else: not one of
// ours by its shape, no such key (revoked), expired, or a row that is not a
// watch key. Only this door hashes a key this way (apitoken.HashWatchKey).
func (h *StorageWatch) keyOf(r *http.Request, raw string) *model.APIToken {
	if !strings.HasPrefix(raw, watchproto.KeyPrefix) {
		return nil
	}
	tok, err := h.store.GetAPITokenByHash(r.Context(), apitoken.HashWatchKey(raw))
	if err != nil || tok == nil || !tok.DoorOnly() {
		return nil
	}
	if tok.ExpiresAt != nil && !tok.ExpiresAt.After(time.Now()) {
		return nil
	}
	return tok
}

// ownerMayReport asks of the key's owner, as they are now, what makes a
// report theirs to send, and reads the storage. On a refusal it has answered.
func (h *StorageWatch) ownerMayReport(w http.ResponseWriter, r *http.Request, tok *model.APIToken, storageID int64) (*model.Storage, bool) {
	ctx := r.Context()
	owner, err := h.store.GetUser(ctx, tok.UserID)
	if err != nil || owner == nil {
		writeError(w, r, http.StatusUnauthorized, "watch_key_unknown", nil)
		return nil, false
	}
	if auth.RefuseBlockedAccount(w, r, h.store, owner) {
		return nil, false
	}
	if !owner.IsAdmin() {
		writeError(w, r, http.StatusForbidden, "watch_owner_not_admin", nil)
		return nil, false
	}
	if !db.AccountReaches(ctx, h.store, owner, storageID) {
		writeError(w, r, http.StatusGone, "watch_storage_gone", nil)
		return nil, false
	}
	st, err := h.store.GetStorage(ctx, storageID)
	if err != nil || st == nil {
		writeError(w, r, http.StatusGone, "watch_storage_gone", nil)
		return nil, false
	}
	// A key is for the storage it was made for, not for its number: a storage
	// deleted and made again can be handed the same number (SQLite reuses a
	// freed one), and the new one is younger than the key.
	if !st.CreatedAt.IsZero() && tok.CreatedAt.Truncate(time.Second).Before(st.CreatedAt.Truncate(time.Second)) {
		writeError(w, r, http.StatusGone, "watch_storage_gone", nil)
		return nil, false
	}
	if !st.Enabled {
		writeError(w, r, http.StatusConflict, "watch_storage_off", apierr.Params{"name": st.Name})
		return nil, false
	}
	return st, true
}

// watchScope keeps the folders the catalogue may be asked about: never one
// inside filex's own trees (sync.ScopePath, the rule a folder rescan
// applies). Paths arrive canonical (watchproto.Report.Validate).
func watchScope(paths []string) (kept []string, ignored int) {
	for _, p := range paths {
		if _, err := syncpkg.ScopePath(p); err != nil {
			ignored++
			continue
		}
		kept = append(kept, p)
	}
	return kept, ignored
}

// ── the storage page's keys ────────────────────────────────────────────────

// watchKeyView is one key as the storage page lists it. Never the key.
type watchKeyView struct {
	ID         int64      `json:"id"`
	Name       string     `json:"name"`
	CreatedAt  time.Time  `json:"created_at"`
	LastUsedAt *time.Time `json:"last_used_at,omitempty"`
	// CreatedBy is the administrator whose key it is (it reports as them).
	CreatedBy string `json:"created_by,omitempty"`
	// Agent is what the program using the key last said about itself, since
	// filex started; absent until it reports.
	Agent *watchin.AgentStatus `json:"agent,omitempty"`
}

// watchSetup is everything a machine needs to run the agent for this
// storage, built ONCE here so no client builds an install command of its own
// (the uploaders' rule). The commands read the key from a file; Key is the
// line that writes that file, with the key in it only in the answer that made
// the key, and a placeholder everywhere else.
type watchSetup struct {
	Server    string `json:"server"`
	StorageID int64  `json:"storage_id"`
	Release   string `json:"release"`
	Image     string `json:"image"`
	Linux     string `json:"linux"`
	MacOS     string `json:"macos"`
	Windows   string `json:"windows"`
	Docker    string `json:"docker"`
}

// storageFor reads the storage {id} the request names, answering 404 for one
// that is not there or not the caller's tenant's.
func (h *StorageWatch) storageFor(w http.ResponseWriter, r *http.Request) (*model.Storage, bool) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || id <= 0 {
		notFound(w, "")
		return nil, false
	}
	if !ownsStorage(w, r, id, "") {
		return nil, false
	}
	st, err := h.store.GetStorage(r.Context(), id)
	if err != nil || st == nil {
		notFound(w, "")
		return nil, false
	}
	return st, true
}

// keysOf lists the filex-watch keys of storage id, newest first.
func (h *StorageWatch) keysOf(r *http.Request, id int64) ([]*model.APIToken, error) {
	all, err := h.store.ListAPITokens(r.Context())
	if err != nil {
		return nil, err
	}
	var out []*model.APIToken
	for _, t := range all {
		if sid, ok := t.WatchStorage(); ok && sid == id {
			out = append(out, t)
		}
	}
	return out, nil
}

func (h *StorageWatch) view(r *http.Request, t *model.APIToken) watchKeyView {
	v := watchKeyView{ID: t.ID, Name: t.Label, CreatedAt: t.CreatedAt, LastUsedAt: t.LastUsedAt}
	if u, err := h.store.GetUser(r.Context(), t.UserID); err == nil && u != nil {
		v.CreatedBy = model.PersonLabel(u.DisplayName, u.Username, u.Email)
	}
	if st, ok := h.agents.Get(t.ID); ok {
		v.Agent = &st
	}
	return v
}

// Status serves GET /api/admin/storages/{id}/watch.
func (h *StorageWatch) Status(w http.ResponseWriter, r *http.Request) {
	st, ok := h.storageFor(w, r)
	if !ok {
		return
	}
	keys, err := h.keysOf(r, st.ID)
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "internal_error", nil)
		return
	}
	views := make([]watchKeyView, 0, len(keys))
	for _, t := range keys {
		views = append(views, h.view(r, t))
	}
	server := h.tenants.FromRequest(r)
	writeJSON(w, http.StatusOK, map[string]any{
		"storage_id": st.ID,
		"storage":    st.Name,
		"server":     server,
		"release":    watchproto.AgentVersion,
		"keys":       views,
		"max_keys":   watchKeysPerStorage,
		"setup":      watchSetupFor(server, st.ID, ""),
		// Whether an agent can serve this storage at all (an object store
		// has no folders on a disk to watch): a key is refused when not.
		"supported": watchSupported(st.Driver),
	})
}

// watchKeyRequest is the body of POST …/watch/keys.
type watchKeyRequest struct {
	// Name says which machine the key is for ("nas01"); default filex-watch.
	Name string `json:"name"`
}

// CreateKey serves POST /api/admin/storages/{id}/watch/keys: a new key, shown
// in this answer and never again (only its hash is kept).
func (h *StorageWatch) CreateKey(w http.ResponseWriter, r *http.Request) {
	u := auth.UserFrom(r.Context())
	if u == nil {
		writeError(w, r, http.StatusUnauthorized, "unauthorized", nil)
		return
	}
	st, ok := h.storageFor(w, r)
	if !ok {
		return
	}
	// An object store (S3, an object-store plugin) has no folder tree on a
	// disk for an agent to watch, and a plugin that streams its own changes
	// tells filex itself: no key for either.
	if !watchSupported(st.Driver) {
		writeError(w, r, http.StatusConflict, "watch_unsupported_driver", apierr.Params{"driver": st.Driver})
		return
	}
	var req watchKeyRequest
	if r.ContentLength != 0 {
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&req); err != nil {
			writeError(w, r, http.StatusBadRequest, "bad_json", nil)
			return
		}
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		name = "filex-watch"
	}
	if utf8.RuneCountInString(name) > watchKeyNameMax {
		writeError(w, r, http.StatusBadRequest, "watch_key_name_too_long", apierr.Params{"max": strconv.Itoa(watchKeyNameMax)})
		return
	}
	existing, err := h.keysOf(r, st.ID)
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "internal_error", nil)
		return
	}
	if len(existing) >= watchKeysPerStorage {
		writeError(w, r, http.StatusConflict, "watch_keys_full", apierr.Params{"max": strconv.Itoa(watchKeysPerStorage)})
		return
	}
	key, err := newPrefixedKey(watchproto.KeyPrefix)
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "internal_error", nil)
		return
	}
	created, err := h.store.CreateAPIToken(r.Context(), &model.APIToken{
		UserID:    u.ID,
		Label:     name,
		TokenHash: apitoken.HashWatchKey(key),
		Scopes:    model.WatchScopePrefix + strconv.FormatInt(st.ID, 10),
		Kind:      model.TokenKindApp,
		Source:    model.TokenSourceWatch,
	})
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "internal_error", nil)
		return
	}
	auth.SetAuditTarget(r.Context(), strconv.FormatInt(st.ID, 10), st.Name)
	server := h.tenants.FromRequest(r)
	writeJSON(w, http.StatusCreated, map[string]any{
		"key":   key,
		"row":   h.view(r, created),
		"setup": watchSetupFor(server, st.ID, key),
	})
}

// DeleteKey serves DELETE /api/admin/storages/{id}/watch/keys/{key}: the key
// stops working at once; the agent using it is refused from its next report.
func (h *StorageWatch) DeleteKey(w http.ResponseWriter, r *http.Request) {
	st, ok := h.storageFor(w, r)
	if !ok {
		return
	}
	keyID, err := strconv.ParseInt(chi.URLParam(r, "key"), 10, 64)
	if err != nil || keyID <= 0 {
		writeError(w, r, http.StatusNotFound, "watch_key_not_found", nil)
		return
	}
	tok, err := h.store.GetAPITokenByID(r.Context(), keyID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		writeError(w, r, http.StatusInternalServerError, "internal_error", nil)
		return
	}
	// Another storage's key, or an API key that is not a watch key, is
	// answered exactly like one that does not exist.
	if sid, ok := tok.WatchStorage(); err != nil || !ok || sid != st.ID {
		writeError(w, r, http.StatusNotFound, "watch_key_not_found", nil)
		return
	}
	if err := h.store.DeleteAPIToken(r.Context(), keyID); err != nil {
		writeError(w, r, http.StatusInternalServerError, "internal_error", nil)
		return
	}
	h.agents.Forget(keyID)
	auth.SetAuditTarget(r.Context(), strconv.FormatInt(st.ID, 10), st.Name)
	// An object, not 204: the admin MCP tool answers what the route does.
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// watchKeyPlaceholder stands for the key in a setup built without one.
const watchKeyPlaceholder = "PASTE-THE-KEY-HERE"

// watchFolderPlaceholder stands for the folder on the agent's machine: the
// storage's own path is the path on the filex machine, which is usually not
// the same (a NAS share is /volume1/media there and /mnt/nas here).
const watchFolderPlaceholder = "/path/to/the/folder"

// watchSetupFor is the setup for storage id on server, with key in the line
// that writes the key file ("" for the placeholder).
func watchSetupFor(server string, id int64, key string) watchSetup {
	if key == "" {
		key = watchKeyPlaceholder
	}
	rel := watchproto.AgentVersion
	base := watchReleaseDownload + watchproto.TagPrefix + rel + "/"
	sid := strconv.FormatInt(id, 10)
	unixConfig := func(dir string) string {
		return fmt.Sprintf(`{"server": %q, "storage": %s, "key_file": %q, "root": %q}`,
			server, sid, dir+"/key", watchFolderPlaceholder)
	}
	linux := strings.Join([]string{
		`ARCH=$(uname -m | sed 's/x86_64/amd64/;s/aarch64/arm64/')`,
		`sudo curl -fL -o /usr/local/bin/filex-watch ` + base + `filex-watch-linux-$ARCH`,
		`sudo chmod 755 /usr/local/bin/filex-watch`,
		`sudo install -d -m 700 /etc/filex-watch`,
		`printf '%s' '` + key + `' | sudo tee /etc/filex-watch/key >/dev/null`,
		`sudo chmod 600 /etc/filex-watch/key`,
		`echo '` + unixConfig("/etc/filex-watch") + `' | sudo tee /etc/filex-watch/config.json >/dev/null`,
		`sudo curl -fL -o /etc/systemd/system/filex-watch.service ` + base + `filex-watch.service`,
		`sudo systemctl daemon-reload && sudo systemctl enable --now filex-watch`,
		`journalctl -u filex-watch -f`,
	}, "\n")
	macos := strings.Join([]string{
		`ARCH=$(uname -m | sed 's/x86_64/amd64/')`,
		`sudo mkdir -p /usr/local/bin /usr/local/etc/filex-watch`,
		`sudo curl -fL -o /usr/local/bin/filex-watch ` + base + `filex-watch-darwin-$ARCH`,
		`sudo chmod 755 /usr/local/bin/filex-watch`,
		`printf '%s' '` + key + `' | sudo tee /usr/local/etc/filex-watch/key >/dev/null`,
		`sudo chmod 600 /usr/local/etc/filex-watch/key`,
		`echo '` + unixConfig("/usr/local/etc/filex-watch") + `' | sudo tee /usr/local/etc/filex-watch/config.json >/dev/null`,
		`sudo curl -fL -o /Library/LaunchDaemons/sh.filex.watch.plist ` + base + `sh.filex.watch.plist`,
		`sudo launchctl bootstrap system /Library/LaunchDaemons/sh.filex.watch.plist`,
		`tail -f /var/log/filex-watch.log`,
	}, "\n")
	// One folder with no space in its path (C:\ProgramData\filex-watch), so
	// the service's command line needs no quotes inside quotes.
	windows := strings.Join([]string{
		`$data = "$env:ProgramData\filex-watch"`,
		`New-Item -ItemType Directory -Force $data | Out-Null`,
		`Invoke-WebRequest ` + base + `filex-watch-windows-amd64.exe -OutFile "$data\filex-watch.exe"`,
		`Set-Content -NoNewline -Path "$data\key" -Value '` + key + `'`,
		`icacls "$data\key" /inheritance:r /grant:r "SYSTEM:R" "Administrators:F" | Out-Null`,
		`Set-Content -Path "$data\config.json" -Value '` + fmt.Sprintf(`{"server": %q, "storage": %s, "key_file": "C:/ProgramData/filex-watch/key", "root": "D:/path/to/the/folder", "log_file": "C:/ProgramData/filex-watch/filex-watch.log"}`, server, sid) + `'`,
		`New-Service -Name filex-watch -DisplayName filex-watch -StartupType Automatic -BinaryPathName "$data\filex-watch.exe --config $data\config.json"`,
		`Start-Service filex-watch`,
		`Get-Content "$data\filex-watch.log" -Wait`,
	}, "\n")
	docker := strings.Join([]string{
		`sudo install -d -m 700 /etc/filex-watch`,
		`printf '%s' '` + key + `' | sudo tee /etc/filex-watch/key >/dev/null`,
		`docker run -d --name filex-watch --restart unless-stopped \`,
		`  -e FILEX_WATCH_SERVER=` + server + ` -e FILEX_WATCH_STORAGE=` + sid + ` \`,
		`  -v /etc/filex-watch/key:/run/secrets/filex-watch-key:ro \`,
		`  -v ` + watchFolderPlaceholder + `:/watch:ro \`,
		`  ` + watchImage + `:` + rel,
	}, "\n")
	return watchSetup{
		Server:    server,
		StorageID: id,
		Release:   rel,
		Image:     watchImage + ":" + rel,
		Linux:     linux,
		MacOS:     macos,
		Windows:   windows,
		Docker:    docker,
	}
}
