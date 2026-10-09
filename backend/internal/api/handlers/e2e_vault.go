package handlers

import (
	"bytes"
	"context"
	crand "crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/brf-tech/filex/backend/internal/auth"
	"github.com/brf-tech/filex/backend/internal/e2e"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/pathkey"
	"github.com/brf-tech/filex/backend/internal/perm"
	"github.com/brf-tech/filex/backend/internal/quota"
	"github.com/brf-tech/filex/backend/internal/realtime"
	"github.com/brf-tech/filex/backend/internal/srvtext"
	"github.com/brf-tech/filex/backend/internal/storage"
	"github.com/brf-tech/filex/backend/internal/syspath"
	"github.com/brf-tech/filex/backend/internal/vaultlock"
	"github.com/brf-tech/filex/backend/internal/writegate"
)

// The vault API (encryption level 3): /api/files/e2e/vault/*,
// docs/E2E-VAULT-FORMAT.md → The write lock → API.
//
// A vault is one folder: a key file, encrypted index files (v/idx/G.fxi) and
// packs of one fixed size (v/p/XX/ID.fxp). The server never decrypts any of
// it. What it does is the part no client can do alone:
//
//   - it ORDERS the commits. One session writes at a time, under a write lock
//     kept in the database (internal/vaultlock); a generation is written only
//     as latest + 1, by the lock's holder, through a temporary file renamed
//     into place (one PUT on S3), and abandoned after 60 seconds;
//   - it CHECKS what it can see: a pack is exactly 2^pack bytes with the
//     header of its id and size, an index file has the header of its
//     generation and a Padmé size, names are the format's own, the three
//     newest generations are never deleted;
//   - it KEEPS EVERY OTHER DOOR OUT. Inside a vault folder only this API
//     writes (writegate's vault rule, which every write door asks); the folder
//     itself and its key file stay an ordinary encrypted folder's.
//
// Reads use the ordinary download (ranges included): packs and index files
// are ciphertext, and whoever may download from the folder may read them.
//
// ⚠ Off unless FILEX_E2E_VAULT is on (capabilities.e2e_vault): until the
// level works end to end nothing offers it, and every route here answers 404
// VAULT_DISABLED. writegate's vault rule follows the same switch.

// VaultLockHeader carries the write lock's token on every vault write.
const VaultLockHeader = "X-Filex-Vault-Lock"

const (
	// vaultJSONMax bounds the JSON requests: create carries a key file and a
	// 64 KiB index file in base64; delete at most 1 000 names.
	vaultJSONMax = 1 << 20
	// vaultDeleteMax is the most names one delete may carry.
	vaultDeleteMax = 1000
	// vaultListDefault / vaultListMax page a listing.
	vaultListDefault = 1000
	vaultListMax     = 10000
	// vaultTempMaxAge: a v/idx/.tmp-* file older than this is a commit that
	// stopped half way, and the next commit removes it.
	vaultTempMaxAge = time.Hour
	// vaultLabelMax caps a lock's free-text label, in characters.
	vaultLabelMax = 120
	// vaultObjectMime is what the catalogue records for a pack or index file.
	vaultObjectMime = "application/octet-stream"
)

// VaultEmitter publishes a vault's own frames (realtime.Hub.EmitVault).
type VaultEmitter interface {
	EmitVault(storageID int64, dir string, ev realtime.VaultEvent)
}

// vaultEmitter is the process-wide, optional emitter, like changeEmitter: nil
// until the server wires the hub (SetVaultEmitter), and every call site is
// nil-safe.
var vaultEmitter VaultEmitter

// SetVaultEmitter installs the realtime emitter for vault frames.
func SetVaultEmitter(e VaultEmitter) { vaultEmitter = e }

func emitVault(storageID int64, dir string, ev realtime.VaultEvent) {
	if vaultEmitter != nil {
		vaultEmitter.EmitVault(storageID, dir, ev)
	}
}

// E2EVault serves /api/files/e2e/vault/*. It works through the explorer's
// Manager - its store, storage drivers, ACL, encryption rule, quota and
// catalogue - so a vault write is judged and recorded the way every other
// write is.
type E2EVault struct {
	M       *Manager
	Locks   *vaultlock.Service
	Enabled bool
}

// NewE2EVault builds the handler over m. enabled is FILEX_E2E_VAULT.
func NewE2EVault(m *Manager, enabled bool) *E2EVault {
	h := &E2EVault{M: m, Enabled: enabled}
	if m != nil && m.Store != nil {
		h.Locks = vaultlock.New(m.Store)
		h.Locks.OnEnded = h.recordEnded
	}
	return h
}

// vaultRef is a resolved vault folder.
type vaultRef struct {
	st   *model.Storage
	rel  string // the vault folder, clean; never the storage's root
	drv  storage.Driver
	info e2e.VaultInfo
	key  vaultlock.Key
}

func (v *vaultRef) at(rel string) string { return v.rel + "/" + rel }

func (v *vaultRef) place() vaultlock.Place {
	return vaultlock.Place{StorageID: v.st.ID, Path: v.rel}
}

// vaultRead is what reading a vault asks at its folder; vaultWrite what a
// write session asks (docs/E2E-VAULT-FORMAT.md → Permissions and tenancy): it
// can add, change and remove anything inside, and the server cannot tell
// which. files.encrypt is not asked, as for adding a file to an encrypted
// folder.
var (
	vaultRead  = []perm.Perm{perm.FilesDownload}
	vaultWrite = []perm.Perm{perm.FilesCreate, perm.FilesModify, perm.FilesDelete}
)

// vaultRefuse writes a vault refusal: {"error": code, "message": …} in the
// reader's language, plus extra.
func vaultRefuse(w http.ResponseWriter, r *http.Request, status int, code, key string, vars srvtext.Vars, extra map[string]any) {
	body := map[string]any{"error": code, "message": srvtext.Text(langOf(r), key, vars)}
	for k, v := range extra {
		body[k] = v
	}
	writeJSON(w, status, body)
}

func vaultBadRequest(w http.ResponseWriter, r *http.Request, detail string) {
	vaultRefuse(w, r, http.StatusBadRequest, "VAULT_BAD_REQUEST", "server.e2e.vault.bad_request", nil, map[string]any{"detail": detail})
}

func vaultBadObject(w http.ResponseWriter, r *http.Request, detail string) {
	vaultRefuse(w, r, http.StatusBadRequest, "VAULT_BAD_OBJECT", "server.e2e.vault.bad_object", nil, map[string]any{"detail": detail})
}

// on answers 404 VAULT_DISABLED while vaults are off, and reports whether
// they are on.
func (h *E2EVault) on(w http.ResponseWriter, r *http.Request) bool {
	if h != nil && h.Enabled && h.M != nil && h.Locks != nil {
		return true
	}
	vaultRefuse(w, r, http.StatusNotFound, "VAULT_DISABLED", "server.e2e.vault.disabled", nil, nil)
	return false
}

// place resolves a wire path (`docs://Kasa`) to its storage and clean
// relative path, and holds it to the caller's tenant and token root: the
// root is asked before the storage is looked up, and a storage outside the
// tenant reads exactly like one that does not exist (E2E.resolveDir).
func (h *E2EVault) place(w http.ResponseWriter, r *http.Request, wire string) (*model.Storage, string, bool) {
	ctx := r.Context()
	if auth.UserFrom(ctx) == nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return nil, "", false
	}
	adapter, rel := splitAdapterPath(confinedPath(ctx, wire))
	if adapter == "" || pathHasDotDot(rel) {
		vaultBadRequest(w, r, "path must be <storage>://<folder>")
		return nil, "", false
	}
	rel = strings.Trim(path.Clean("/"+rel), "/")
	if rel == "" {
		vaultBadRequest(w, r, "a vault is a folder, not a storage's root")
		return nil, "", false
	}
	if !rootAllowsNamed(ctx, adapter, rel) {
		refuseOutsideRoot(w, r)
		return nil, "", false
	}
	st, err := h.M.Store.GetStorageByName(ctx, adapter)
	if err != nil || st == nil || !scopeOf(ctx).CanAccessStorage(st.ID) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "unknown adapter: " + adapter})
		return nil, "", false
	}
	if !rootAllowsIn(ctx, st, rel) {
		refuseOutsideRoot(w, r)
		return nil, "", false
	}
	return st, rel, true
}

// may asks the caller's permissions at rel: every one of need. An unwired ACL
// (a handler built by hand in a test) allows, as Manager.require does.
func (h *E2EVault) may(w http.ResponseWriter, r *http.Request, st *model.Storage, rel string, need []perm.Perm) bool {
	if h.M.ACL == nil || len(need) == 0 {
		return true
	}
	set, err := h.M.ACL.LoadSet(r.Context(), auth.UserFrom(r.Context()), st)
	if err != nil || set == nil {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "insufficient permission"})
		return false
	}
	for _, p := range need {
		if !requireCan(w, r, set, rel, p, "insufficient permission") {
			return false
		}
	}
	return true
}

// tenantOf is the tenant a storage belongs to: the provider it is linked to,
// 0 when none (a single-tenant install). A lookup that fails is a failure,
// never tenant 0 - that would be another lock for the same vault.
func (h *E2EVault) tenantOf(ctx context.Context, storageID int64) (int64, error) {
	id, ok, err := h.M.Store.GetProviderIDForStorage(ctx, storageID)
	if err != nil {
		return 0, err
	}
	if !ok {
		return 0, nil
	}
	return id, nil
}

// open resolves wire to a vault the caller may act on with need: the key file
// at its folder must name the vault feature and keep the format's rules
// (400 NOT_A_VAULT otherwise).
func (h *E2EVault) open(w http.ResponseWriter, r *http.Request, wire string, need []perm.Perm) (*vaultRef, bool) {
	ctx := r.Context()
	st, rel, ok := h.place(w, r, wire)
	if !ok || !h.may(w, r, st, rel, need) {
		return nil, false
	}
	drv, err := h.M.StorageResolver(st.ID)
	if err != nil || drv == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "storage_unavailable"})
		return nil, false
	}
	info, isVault, perr := e2e.ParseVaultKeyFile(readSmall(ctx, drv, rel+"/"+e2e.MarkerName))
	if !isVault || perr != nil {
		extra := map[string]any{}
		if isVault {
			extra["detail"] = perr.Error()
		}
		vaultRefuse(w, r, http.StatusBadRequest, "NOT_A_VAULT", "server.e2e.vault.not_a_vault", nil, extra)
		return nil, false
	}
	tenant, err := h.tenantOf(ctx, st.ID)
	if err != nil {
		slog.Warn("vault: tenant of storage", slog.Int64("storage", st.ID), slog.String("err", err.Error()))
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "storage_unavailable"})
		return nil, false
	}
	return &vaultRef{st: st, rel: rel, drv: drv, info: info, key: vaultlock.Key{Tenant: tenant, Vault: info.IDHex()}}, true
}

// lockAnswer writes what a vault lock error answers: 409 VAULT_LOCKED (who,
// since when, how long to wait), 409 VAULT_LOCK_LOST (why), 503 VAULT_BUSY
// for a lock row that kept changing, 500 otherwise.
func lockAnswer(w http.ResponseWriter, r *http.Request, err error) {
	var locked *vaultlock.LockedError
	var lost *vaultlock.LostError
	switch {
	case errors.As(err, &locked):
		vaultRefuse(w, r, http.StatusConflict, "VAULT_LOCKED", "server.e2e.vault.locked", srvtext.Vars{"name": locked.Holder.Name}, map[string]any{
			"holder":      holderJSON(locked.Holder),
			"since":       vaultTime(locked.Since),
			"retry_after": int64((locked.RetryAfter + time.Second - 1) / time.Second),
		})
	case errors.As(err, &lost):
		extra := map[string]any{"reason": lost.Reason}
		// taken and broken also say who: the session that holds it now, or
		// the person who broke it.
		if lost.Holder != nil {
			extra["holder"] = holderJSON(*lost.Holder)
		}
		vaultRefuse(w, r, http.StatusConflict, "VAULT_LOCK_LOST", "server.e2e.vault.lock_lost."+lost.Reason, nil, extra)
	case errors.Is(err, vaultlock.ErrContention):
		vaultRefuse(w, r, http.StatusServiceUnavailable, "VAULT_BUSY", "server.e2e.vault.busy", nil, nil)
	default:
		slog.Warn("vault: lock", slog.String("err", err.Error()))
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "vault lock unavailable"})
	}
}

func holderJSON(h vaultlock.Holder) map[string]any {
	return map[string]any{"name": h.Name, "client": h.Client, "label": h.Label}
}

// vaultTime is how the vault API writes a time (docs/E2E-VAULT-FORMAT.md →
// Values): RFC 3339, UTC, to the second.
func vaultTime(t time.Time) string { return t.UTC().Format(time.RFC3339) }

func realtimeHolder(h vaultlock.Holder) *realtime.VaultHolder {
	return &realtime.VaultHolder{Name: h.Name, Client: h.Client, Label: h.Label}
}

// vaultHolder is who the caller is, as a lock names them.
func vaultHolder(u *model.User, client, label string) vaultlock.Holder {
	name := u.DisplayName
	if name == "" {
		name = u.Username
	}
	if name == "" {
		name = u.Email
	}
	return vaultlock.Holder{UserID: u.ID, Name: name, Client: client, Label: label}
}

// cutLabel keeps a lock label short and on one line.
func cutLabel(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if utf8.RuneCountInString(s) <= vaultLabelMax {
		return s
	}
	r := []rune(s)
	return string(r[:vaultLabelMax])
}

// vaultIndexObj is one index file of a vault's v/idx/.
type vaultIndexObj struct {
	gen   uint64
	size  int64
	mtime time.Time
}

// vaultIndexes lists a vault's index files, by generation. A vault without
// v/idx/ has none (generation 0). sweep removes the temporary files of
// commits that stopped more than an hour ago.
func vaultIndexes(ctx context.Context, drv storage.Driver, rel string, sweep bool, now time.Time) ([]vaultIndexObj, error) {
	objs, err := drv.List(ctx, rel+"/v/idx")
	if errors.Is(err, storage.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []vaultIndexObj
	for _, o := range objs {
		if o.Kind == storage.KindDirectory {
			continue
		}
		kind, _, gen := e2e.ParseVaultPath("v/idx/" + o.Name)
		switch kind {
		case e2e.VaultPathIndex:
			out = append(out, vaultIndexObj{gen: gen, size: o.Size, mtime: o.Mtime})
		case e2e.VaultPathTemp:
			if sweep && !o.Mtime.IsZero() && now.Sub(o.Mtime) > vaultTempMaxAge {
				vaultRemove(ctx, drv, rel+"/v/idx/"+o.Name)
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].gen < out[j].gen })
	return out, nil
}

func latestOf(idx []vaultIndexObj) uint64 {
	if len(idx) == 0 {
		return 0
	}
	return idx[len(idx)-1].gen
}

// vaultRemove deletes one object for good (no trash, no version): a missing
// one counts as deleted.
func vaultRemove(ctx context.Context, drv storage.Driver, rel string) {
	if d, ok := drv.(storage.Deleter); ok {
		if err := d.Delete(ctx, rel); err != nil && !errors.Is(err, storage.ErrNotFound) {
			slog.Warn("vault: remove", slog.String("err", err.Error()))
		}
	}
}

// vaultMkdirs makes the folders of rel under the vault folder, for the
// storages that need a folder before a file goes into it. Object stores
// have no folders: nothing is made there, and a keep marker never lands in a
// vault.
func vaultMkdirs(ctx context.Context, drv storage.Driver, vault, rel string) {
	mk, ok := drv.(storage.Mkdirer)
	if !ok || drv.Name() == "s3" {
		return
	}
	built := vault
	for _, seg := range strings.Split(rel, "/") {
		built += "/" + seg
		_ = mk.Mkdir(ctx, built)
	}
}

// recordFile puts an object the vault API wrote into the catalogue, so the
// quota counts it (quotastore) and the vault rule sees the key file
// (vaultlock.Finder reads it by its row). No thumbnail, index entry or
// post-write hook: it is ciphertext, and the vault's own frames say it
// changed.
func (h *E2EVault) recordFile(ctx context.Context, st *model.Storage, drv storage.Driver, rel string, size int64, mime string) {
	dir := path.Dir(rel)
	if dir == "." {
		dir = ""
	}
	parentID, err := h.M.ensureDirChain(ctx, st.ID, dir)
	if err != nil {
		slog.Warn("vault: catalogue", slog.Int64("storage", st.ID), slog.String("err", err.Error()))
		return
	}
	clean := normalizeDBPath(rel)
	hash := pathkey.Hash(st.ID, clean)
	lsize, etag, mtime := storage.Landed(ctx, drv, rel, size)
	if existing, _ := h.M.Store.GetNodeByPath(ctx, st.ID, hash); existing != nil {
		_ = h.M.Store.UpdateNodeMeta(ctx, existing.ID, lsize, mime, etag, mtime)
		return
	}
	if _, err := h.M.Store.CreateNode(ctx, &model.Node{
		StorageID:    st.ID,
		ParentID:     parentID,
		Name:         path.Base(rel),
		Path:         clean,
		PathHash:     hash,
		StorageKey:   clean,
		Type:         model.NodeTypeFile,
		Size:         lsize,
		Mime:         mime,
		Etag:         etag,
		BackendMtime: &mtime,
		SyncState:    model.SyncStateSynced,
	}); err != nil {
		slog.Warn("vault: catalogue", slog.Int64("storage", st.ID), slog.String("err", err.Error()))
	}
}

// forgetFile drops an object the vault API deleted from the catalogue (the
// quota gives its bytes back).
func (h *E2EVault) forgetFile(ctx context.Context, st *model.Storage, rel string) {
	n, err := h.M.Store.GetNodeByPath(ctx, st.ID, pathkey.Hash(st.ID, normalizeDBPath(rel)))
	if err != nil || n == nil {
		return
	}
	_ = h.M.Store.HardDeleteNode(ctx, n.ID)
}

// quotaRefused answers a write the quota refuses, as the explorer's upload
// does, and reports whether it did.
func (h *E2EVault) quotaRefused(w http.ResponseWriter, r *http.Request, size int64) bool {
	err := h.M.checkQuota(r.Context(), size, size)
	switch {
	case err == nil:
		return false
	case errors.Is(err, quota.ErrQuotaExceeded):
		writeQuotaExceeded(w, r)
	case errors.Is(err, quota.ErrFileTooLarge):
		writeJSON(w, http.StatusRequestEntityTooLarge, map[string]string{"error": err.Error(), "code": "FILE_TOO_LARGE"})
	default:
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}
	return true
}

func (h *E2EVault) audit(ctx context.Context, r *http.Request, actor *int64, action, vaultID string, meta map[string]any) {
	e := &model.AuditEntry{UserID: actor, Action: action, TargetType: "vault", TargetID: vaultID, Metadata: meta}
	if r != nil {
		e.IP = clientIP(r)
	}
	if err := h.M.Store.InsertAuditEntry(ctx, e); err != nil {
		slog.Warn("vault: audit", slog.String("action", action), slog.String("err", err.Error()))
	}
}

func userIDOf(u *model.User) *int64 {
	if u == nil {
		return nil
	}
	id := u.ID
	return &id
}

// recordEnded is vaultlock's OnEnded: every lock that ends is one
// vault.unlock row - with the reason, and the first and last generation
// committed under it - and one vault.lock frame saying the vault is free.
func (h *E2EVault) recordEnded(ctx context.Context, e vaultlock.Ended) {
	holder := e.Holder.UserID
	meta := map[string]any{
		"reason":           e.Reason,
		"storage_id":       e.StorageID,
		"folder":           e.Path,
		"holder":           e.Holder.Name,
		"client":           e.Holder.Client,
		"label":            e.Holder.Label,
		"since":            vaultTime(e.Since),
		"ended":            vaultTime(e.At),
		"first_generation": e.FirstGen,
		"last_generation":  e.LastGen,
	}
	if e.By.Name != "" {
		meta["broken_by"] = e.By.Name
	}
	h.audit(context.WithoutCancel(ctx), nil, &holder, "vault.unlock", e.Vault, meta)
	emitVault(e.StorageID, e.Path, realtime.VaultEvent{Type: realtime.VaultLockEvent, Held: false})
}

// ── POST /create ─────────────────────────────────────────────────────────

type vaultCreateReq struct {
	Path   string          `json:"path"`
	Marker json.RawMessage `json:"marker"`
	Index  string          `json:"index"`
}

// vaultMarker checks a new vault's key file: a vault's by the format's rules,
// a pack size writers make (22 or 24), and the slots of levels 1 and 2 that
// unlocking needs (salt, iter, verify).
func vaultMarker(raw []byte) (e2e.VaultInfo, string) {
	info, isVault, err := e2e.ParseVaultKeyFile(raw)
	switch {
	case !isVault && err == nil:
		return info, "the key file does not name the vault feature"
	case err != nil:
		return info, err.Error()
	case info.PackLog2 != e2e.VaultDefaultPackLog2 && info.PackLog2 != e2e.VaultLargePackLog2:
		return info, "a new vault's packs are 2^22 or 2^24 bytes"
	}
	var m map[string]json.RawMessage
	if json.Unmarshal(raw, &m) != nil {
		return info, "the key file is not a JSON object"
	}
	for _, k := range []string{"salt", "verify"} {
		var s string
		if json.Unmarshal(m[k], &s) != nil || s == "" {
			return info, "the key file has no " + k
		}
	}
	var iter int64
	if json.Unmarshal(m["iter"], &iter) != nil || iter <= 0 {
		return info, "the key file has no iter"
	}
	return info, ""
}

// Create makes a new, empty vault: the folder (unless an empty one is there),
// its key file and generation 1's index file, in that order - and removes
// what it wrote when a step fails.
//
//	POST /api/files/e2e/vault/create {path, marker, index} → 201 {generation: 1, owner_id, owner_name, owner_self}
func (h *E2EVault) Create(w http.ResponseWriter, r *http.Request) {
	if !h.on(w, r) {
		return
	}
	ctx := r.Context()
	body, ok := confinedBody(w, r, vaultJSONMax)
	if !ok {
		return
	}
	var req vaultCreateReq
	if err := json.Unmarshal(body, &req); err != nil {
		vaultBadRequest(w, r, "bad json")
		return
	}
	st, rel, ok := h.place(w, r, req.Path)
	if !ok {
		return
	}
	parent := path.Dir(rel)
	if parent == "." {
		parent = ""
	}
	if st.ReadOnly {
		writeReadOnly(w, r, http.StatusForbidden)
		return
	}
	if !h.may(w, r, st, parent, []perm.Perm{perm.FilesCreate}) {
		return
	}
	markerRel := rel + "/" + e2e.MarkerName
	indexRel := rel + "/" + e2e.VaultIndexPath(1)
	if gate(w, r, h.M.ACL, st.ID, writegate.Writes(rel).ForVault(), writegate.Writes(markerRel).ForVault(), writegate.Writes(indexRel).ForVault()) {
		return
	}
	info, why := vaultMarker(req.Marker)
	if why != "" {
		vaultBadObject(w, r, why)
		return
	}
	index, err := base64.StdEncoding.DecodeString(req.Index)
	if err != nil {
		index, err = base64.RawStdEncoding.DecodeString(req.Index)
	}
	if err != nil || len(index) != e2e.VaultIndexMinSize || e2e.CheckVaultIndexHeader(index, 1) != nil {
		vaultBadObject(w, r, "index must be the base64 of generation 1's 65536-byte index file")
		return
	}
	// Not inside an encrypted folder: two keys for one place is what nesting
	// would make, and the transfer guard refuses it everywhere else.
	if _, enc := e2e.FindRoot(ctx, h.M.Store, st.ID, parent); enc {
		vaultRefuse(w, r, http.StatusConflict, "VAULT_NESTED", "server.e2e.vault.nested", nil, nil)
		return
	}
	drv, err := h.M.StorageResolver(st.ID)
	if err != nil || drv == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "storage_unavailable"})
		return
	}
	wr, ok := drv.(storage.Writer)
	if !ok {
		writeJSON(w, http.StatusNotImplemented, map[string]string{"error": "driver does not support write"})
		return
	}
	// A new folder, or an empty one: nothing but a keep marker in it.
	existed := false
	switch obj, serr := drv.Stat(ctx, rel); {
	case serr == nil && obj.Kind != storage.KindDirectory:
		vaultRefuse(w, r, http.StatusConflict, "VAULT_EXISTS", "server.e2e.vault.exists", nil, nil)
		return
	case serr == nil:
		entries, lerr := drv.List(ctx, rel)
		if lerr != nil && !errors.Is(lerr, storage.ErrNotFound) {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "storage_unavailable"})
			return
		}
		for _, e := range entries {
			if e.Name != syspath.KeepMarker {
				vaultRefuse(w, r, http.StatusConflict, "VAULT_EXISTS", "server.e2e.vault.exists", nil, nil)
				return
			}
		}
		existed = true
	case !errors.Is(serr, storage.ErrNotFound):
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "storage_unavailable"})
		return
	}
	// The encryption rule: a new encrypted folder here (and, under the
	// approval policy, the approval it spends).
	u := auth.UserFrom(ctx)
	if answerE2E(w, r, st, checkE2ECreate(ctx, h.M.E2EPolicy, u, st, markerRel)) {
		return
	}
	if h.quotaRefused(w, r, int64(len(req.Marker)+len(index))) {
		return
	}

	// The folder, the key file, the index - and back out of all three when a
	// step fails, so a stopped create leaves nothing that looks like a vault.
	// An object store has no folders: its keep marker would land in the vault.
	if !existed && drv.Name() != "s3" {
		if mk, ok := drv.(storage.Mkdirer); ok {
			if err := mk.Mkdir(ctx, rel); err != nil {
				writeJSON(w, mapDriverErr(err), map[string]string{"error": "mkdir: " + err.Error()})
				return
			}
		}
	}
	undo := func() {
		vaultRemove(ctx, drv, indexRel)
		vaultRemove(ctx, drv, markerRel)
		if !existed {
			vaultRemove(ctx, drv, rel+"/v")
			vaultRemove(ctx, drv, rel)
		}
	}
	if err := wr.Write(ctx, markerRel, bytes.NewReader(req.Marker), int64(len(req.Marker))); err != nil {
		undo()
		writeJSON(w, mapDriverErr(err), map[string]string{"error": "write: " + err.Error()})
		return
	}
	vaultMkdirs(ctx, drv, rel, "v/idx")
	if err := wr.Write(ctx, indexRel, bytes.NewReader(index), int64(len(index))); err != nil {
		undo()
		writeJSON(w, mapDriverErr(err), map[string]string{"error": "write: " + err.Error()})
		return
	}
	if _, err := h.M.ensureDirChain(ctx, st.ID, rel); err != nil {
		slog.Warn("vault: catalogue", slog.Int64("storage", st.ID), slog.String("err", err.Error()))
	}
	h.recordFile(ctx, st, drv, markerRel, int64(len(req.Marker)), "application/json")
	h.recordFile(ctx, st, drv, indexRel, int64(len(index)), vaultObjectMime)

	h.audit(ctx, r, userIDOf(u), "vault.create", info.IDHex(), map[string]any{
		"storage":   st.Name,
		"folder":    rel,
		"pack_log2": info.PackLog2,
	})
	emitFolderChange(st.ID, parent, realtime.ChangeEvent{Action: "create", Name: path.Base(rel)})
	// Who owns the new vault, as `state` says it: the tab that made it opens
	// it without asking `state`.
	answer := h.folderOwner(ctx, st, rel, vaultViewer(ctx))
	answer["generation"] = 1
	writeJSON(w, http.StatusCreated, answer)
}

// ── GET /state ──────────────────────────────────────────────────────────

// State answers a vault's id, pack size, latest generation and lock.
//
//	GET /api/files/e2e/vault/state?path= → 200 {vault_id, pack_log2, generation, lock, owner_id, owner_name, owner_self}
//
// lock is null or {holder: {name, client, label}, since, expires_at, mine};
// mine is true for the session whose token the request carries. owner_id,
// owner_name and owner_self say who owns the vault folder, as a listing row
// does (each omitted when there is nothing to say; folderOwner): the
// explorer draws every row inside the vault with them.
func (h *E2EVault) State(w http.ResponseWriter, r *http.Request) {
	if !h.on(w, r) {
		return
	}
	ctx := r.Context()
	v, ok := h.open(w, r, r.URL.Query().Get("path"), vaultRead)
	if !ok {
		return
	}
	idx, err := vaultIndexes(ctx, v.drv, v.rel, false, h.Locks.Now())
	if err != nil {
		writeJSON(w, mapDriverErr(err), map[string]string{"error": "list: " + err.Error()})
		return
	}
	lock, err := h.Locks.State(ctx, v.key)
	if err != nil {
		lockAnswer(w, r, err)
		return
	}
	var lockJSON any
	if lock != nil {
		lockJSON = map[string]any{
			"holder":     holderJSON(lock.Holder),
			"since":      vaultTime(lock.Since),
			"expires_at": vaultTime(lock.ExpiresAt),
			"mine":       lock.Mine(r.Header.Get(VaultLockHeader)),
		}
	}
	answer := h.folderOwner(ctx, v.st, v.rel, vaultViewer(ctx))
	answer["vault_id"] = base64.RawURLEncoding.EncodeToString(v.info.ID[:])
	answer["pack_log2"] = v.info.PackLog2
	answer["generation"] = latestOf(idx)
	answer["lock"] = lockJSON
	writeJSON(w, http.StatusOK, answer)
}

// ── GET /list ───────────────────────────────────────────────────────────

// List pages a vault's index files or packs, in name order.
//
//	GET /api/files/e2e/vault/list?path=&kind=index|pack&after=&limit= → 200 {items, next}
//
// kind=index: {generation, size, mtime}; kind=pack: {id, size, mtime}.
// after is the name - 16 hex digits of a generation, 32 of a pack id - of
// the last item of the page before; next is the cursor of the next page, or
// null. limit is 1 000 by default and at most 10 000.
func (h *E2EVault) List(w http.ResponseWriter, r *http.Request) {
	if !h.on(w, r) {
		return
	}
	ctx := r.Context()
	q := r.URL.Query()
	kind := q.Get("kind")
	if kind != "index" && kind != "pack" {
		vaultBadRequest(w, r, "kind must be index or pack")
		return
	}
	limit := vaultListDefault
	if s := q.Get("limit"); s != "" {
		n, err := strconv.Atoi(s)
		if err != nil || n < 1 {
			vaultBadRequest(w, r, "limit must be a positive integer")
			return
		}
		limit = n
	}
	if limit > vaultListMax {
		limit = vaultListMax
	}
	after := q.Get("after")
	switch {
	case after == "":
	case kind == "index" && len(after) != 16, kind == "pack" && len(after) != 32, strings.Trim(after, "0123456789abcdef") != "":
		vaultBadRequest(w, r, "after is the hex name of the last item of the previous page")
		return
	}
	v, ok := h.open(w, r, q.Get("path"), vaultRead)
	if !ok {
		return
	}
	type item struct {
		name string
		body map[string]any
	}
	var items []item
	switch kind {
	case "index":
		idx, err := vaultIndexes(ctx, v.drv, v.rel, false, h.Locks.Now())
		if err != nil {
			writeJSON(w, mapDriverErr(err), map[string]string{"error": "list: " + err.Error()})
			return
		}
		for _, o := range idx {
			name := strings.TrimSuffix(path.Base(e2e.VaultIndexPath(o.gen)), ".fxi")
			items = append(items, item{name, map[string]any{"generation": o.gen, "size": o.size, "mtime": vaultTime(o.mtime)}})
		}
	case "pack":
		packs, err := vaultPacks(ctx, v.drv, v.rel)
		if err != nil {
			writeJSON(w, mapDriverErr(err), map[string]string{"error": "list: " + err.Error()})
			return
		}
		for _, o := range packs {
			items = append(items, item{o.Name, map[string]any{"id": o.Name, "size": o.Size, "mtime": vaultTime(o.Mtime)}})
		}
	}
	sort.Slice(items, func(i, j int) bool { return items[i].name < items[j].name })
	out := make([]map[string]any, 0)
	var next any
	lastName := ""
	for _, it := range items {
		if after != "" && it.name <= after {
			continue
		}
		if len(out) == limit {
			next = lastName
			break
		}
		out = append(out, it.body)
		lastName = it.name
	}
	// now is the server's clock: retention (15 minutes after the next
	// generation) is measured against it, never against the client's.
	writeJSON(w, http.StatusOK, map[string]any{"items": out, "next": next, "now": vaultTime(h.Locks.Now())})
}

// vaultPacks lists a vault's packs: every v/p/XX/ID.fxp whose name is the
// format's, Name holding the id's 32 hex digits.
func vaultPacks(ctx context.Context, drv storage.Driver, rel string) ([]storage.Object, error) {
	dirs, err := drv.List(ctx, rel+"/v/p")
	if errors.Is(err, storage.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []storage.Object
	for _, d := range dirs {
		if d.Kind != storage.KindDirectory || len(d.Name) != 2 {
			continue
		}
		objs, err := drv.List(ctx, rel+"/v/p/"+d.Name)
		if errors.Is(err, storage.ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		for _, o := range objs {
			if o.Kind == storage.KindDirectory {
				continue
			}
			kind, id, _ := e2e.ParseVaultPath("v/p/" + d.Name + "/" + o.Name)
			if kind != e2e.VaultPathPack {
				continue
			}
			o.Name = hex.EncodeToString(id[:])
			out = append(out, o)
		}
	}
	return out, nil
}

// ── the lock ────────────────────────────────────────────────────────────

type vaultLockReq struct {
	Path   string `json:"path"`
	Client string `json:"client"`
	Label  string `json:"label"`
	Active bool   `json:"active"`
	Reason string `json:"reason"`
}

func (h *E2EVault) lockReq(w http.ResponseWriter, r *http.Request) (vaultLockReq, bool) {
	var req vaultLockReq
	body, ok := confinedBody(w, r, vaultJSONMax)
	if !ok {
		return req, false
	}
	if err := json.Unmarshal(body, &req); err != nil {
		vaultBadRequest(w, r, "bad json")
		return req, false
	}
	return req, true
}

// Lock takes the vault's write lock for a new session.
//
//	POST /api/files/e2e/vault/lock {path, client, label}
//	  → 200 {token, generation, lease_seconds, idle_seconds, expires_at}
//	  · 409 VAULT_LOCKED {holder, since, retry_after}
func (h *E2EVault) Lock(w http.ResponseWriter, r *http.Request) {
	if !h.on(w, r) {
		return
	}
	ctx := r.Context()
	req, ok := h.lockReq(w, r)
	if !ok {
		return
	}
	if !vaultlock.Clients[req.Client] {
		vaultBadRequest(w, r, "client is web, desktop, cli or mount")
		return
	}
	v, ok := h.open(w, r, req.Path, vaultWrite)
	if !ok {
		return
	}
	if v.st.ReadOnly {
		writeReadOnly(w, r, http.StatusForbidden)
		return
	}
	idx, err := vaultIndexes(ctx, v.drv, v.rel, false, h.Locks.Now())
	if err != nil {
		writeJSON(w, mapDriverErr(err), map[string]string{"error": "list: " + err.Error()})
		return
	}
	u := auth.UserFrom(ctx)
	holder := vaultHolder(u, req.Client, cutLabel(req.Label))
	taken, err := h.Locks.Take(ctx, v.key, v.place(), holder)
	if err != nil {
		lockAnswer(w, r, err)
		return
	}
	h.audit(ctx, r, userIDOf(u), "vault.lock", v.key.Vault, map[string]any{
		"storage": v.st.Name,
		"folder":  v.rel,
		"client":  holder.Client,
		"label":   holder.Label,
	})
	emitVault(v.st.ID, v.rel, realtime.VaultEvent{Type: realtime.VaultLockEvent, Held: true, Holder: realtimeHolder(holder)})
	writeJSON(w, http.StatusOK, map[string]any{
		"token":         taken.Token,
		"generation":    latestOf(idx),
		"lease_seconds": taken.LeaseSeconds,
		"idle_seconds":  taken.IdleSeconds,
		"expires_at":    vaultTime(taken.ExpiresAt),
	})
}

// Renew is the holder's heartbeat: every 15 seconds, active while it is
// about to write.
//
//	POST /api/files/e2e/vault/lock/renew {path, active} + token
//	  → 200 {expires_at, idle_until} · 409 VAULT_LOCK_LOST {reason}
func (h *E2EVault) Renew(w http.ResponseWriter, r *http.Request) {
	if !h.on(w, r) {
		return
	}
	req, ok := h.lockReq(w, r)
	if !ok {
		return
	}
	v, ok := h.open(w, r, req.Path, vaultWrite)
	if !ok {
		return
	}
	ren, err := h.Locks.Renew(r.Context(), v.key, r.Header.Get(VaultLockHeader), req.Active)
	if err != nil {
		lockAnswer(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"expires_at": vaultTime(ren.ExpiresAt), "idle_until": vaultTime(ren.IdleUntil)})
}

// Release ends the caller's lock, whether or not the token still held it.
// reason locked_idle says the client locked the vault itself after its idle
// time (docs/E2E-VAULT-FORMAT.md → The idle lock); it is what the vault.unlock
// row records.
//
//	POST /api/files/e2e/vault/lock/release {path, reason?} + token → 204
//
// No permission is asked beyond reaching the folder: giving a lock up only
// narrows what a session can do, and must stay possible after the
// permission that took it is gone.
func (h *E2EVault) Release(w http.ResponseWriter, r *http.Request) {
	if !h.on(w, r) {
		return
	}
	req, ok := h.lockReq(w, r)
	if !ok {
		return
	}
	v, ok := h.open(w, r, req.Path, nil)
	if !ok {
		return
	}
	if err := h.Locks.Release(r.Context(), v.key, r.Header.Get(VaultLockHeader), req.Reason); err != nil {
		lockAnswer(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// Break ends somebody else's lock: the vault folder's owner or an
// administrator of its tenant (resolving the path already held the caller to
// the tenant). The holder's next call is told VAULT_LOCK_LOST broken.
//
//	POST /api/files/e2e/vault/lock/break {path} → 204
func (h *E2EVault) Break(w http.ResponseWriter, r *http.Request) {
	if !h.on(w, r) {
		return
	}
	ctx := r.Context()
	req, ok := h.lockReq(w, r)
	if !ok {
		return
	}
	v, ok := h.open(w, r, req.Path, nil)
	if !ok {
		return
	}
	u := auth.UserFrom(ctx)
	if !u.IsAdmin() && !h.ownsFolder(ctx, v, u) {
		vaultRefuse(w, r, http.StatusForbidden, "permission_denied", "server.e2e.vault.break_denied", nil, nil)
		return
	}
	client := req.Client
	if !vaultlock.Clients[client] {
		client = ""
	}
	broken, err := h.Locks.Break(ctx, v.key, vaultHolder(u, client, cutLabel(req.Label)))
	if err != nil {
		lockAnswer(w, r, err)
		return
	}
	if broken != nil {
		h.audit(ctx, r, userIDOf(u), "vault.lock_break", v.key.Vault, map[string]any{
			"storage":        v.st.Name,
			"folder":         v.rel,
			"holder":         broken.Holder.Name,
			"holder_user_id": broken.Holder.UserID,
			"holder_client":  broken.Holder.Client,
		})
	}
	w.WriteHeader(http.StatusNoContent)
}

// folderOwner says who owns the vault folder at rel, in the keys a listing
// row carries (owner_id, owner_name, owner_self; handlers/manager.go), each
// omitted when it has nothing to say - a folder nobody owns answers none.
//
// ⚠ Every row INSIDE a vault is the vault folder's: the server knows no file
// in it and the index records no author (docs/E2E-VAULT-FORMAT.md), so the
// explorer drew them with no owner, and a row without one reads "System" -
// the word for a file nobody put here through filex, said of a file the
// person had just uploaded (0.54 screenshots). The answer is the server's,
// the client only shows it.
func (h *E2EVault) folderOwner(ctx context.Context, st *model.Storage, rel string, viewer int64) map[string]any {
	out := map[string]any{}
	if h.M == nil || h.M.Store == nil {
		return out
	}
	n, err := h.M.Store.GetNodeByPath(ctx, st.ID, pathkey.Hash(st.ID, normalizeDBPath(rel)))
	if err != nil || n == nil {
		return out
	}
	owner, err := h.M.Store.GetNodeOwner(ctx, n.ID)
	if err != nil || owner == nil || *owner <= 0 {
		return out
	}
	out["owner_id"] = *owner
	if names, err := h.M.Store.GetUserDisplayNames(ctx, []int64{*owner}); err == nil && names[*owner] != "" {
		out["owner_name"] = names[*owner]
	}
	if viewer > 0 && *owner == viewer {
		out["owner_self"] = true
	}
	return out
}

// vaultViewer is the id of the account asking, 0 for nobody.
func vaultViewer(ctx context.Context) int64 {
	if u := auth.UserFrom(ctx); u != nil {
		return u.ID
	}
	return 0
}

// ownsFolder reports whether u is the recorded owner of the vault folder.
func (h *E2EVault) ownsFolder(ctx context.Context, v *vaultRef, u *model.User) bool {
	if u == nil {
		return false
	}
	n, err := h.M.Store.GetNodeByPath(ctx, v.st.ID, pathkey.Hash(v.st.ID, normalizeDBPath(v.rel)))
	if err != nil || n == nil {
		return false
	}
	owner, err := h.M.Store.GetNodeOwner(ctx, n.ID)
	return err == nil && owner != nil && *owner == u.ID
}

// ── PUT /pack ───────────────────────────────────────────────────────────

// errVaultBodySize is a body that is not the size it has to be.
var errVaultBodySize = errors.New("the body is not the object's size")

// exactReader passes on exactly `remain` bytes of r and refuses a body that is
// shorter or longer (errVaultBodySize; bad says so even when a driver wraps
// the error without %w).
type exactReader struct {
	r      io.Reader
	remain int64
	bad    bool
}

func (e *exactReader) Read(p []byte) (int, error) {
	if e.remain <= 0 {
		var one [1]byte
		n, err := e.r.Read(one[:])
		if n > 0 || (err != nil && err != io.EOF) {
			e.bad = true
			return 0, errVaultBodySize
		}
		return 0, io.EOF
	}
	if int64(len(p)) > e.remain {
		p = p[:e.remain]
	}
	n, err := e.r.Read(p)
	e.remain -= int64(n)
	switch {
	case err == io.EOF && e.remain > 0:
		e.bad = true
		return n, errVaultBodySize
	case err == io.EOF:
		return n, nil
	case err != nil:
		e.bad = true
		return n, errVaultBodySize
	}
	return n, nil
}

// PutPack stores one pack, whole, once: exactly 2^pack bytes with the header
// of its id and size. A pack is never replaced.
//
//	PUT /api/files/e2e/vault/pack?path=&id= (body: the pack) + token → 201
//	  · 409 VAULT_PACK_EXISTS · 400 VAULT_BAD_OBJECT · 409 VAULT_LOCK_LOST
func (h *E2EVault) PutPack(w http.ResponseWriter, r *http.Request) {
	if !h.on(w, r) {
		return
	}
	ctx := r.Context()
	q := r.URL.Query()
	id, okID := e2e.ParseVaultID(q.Get("id"))
	if !okID {
		vaultBadRequest(w, r, "id is 32 lower-case hex digits")
		return
	}
	v, ok := h.open(w, r, q.Get("path"), vaultWrite)
	if !ok {
		return
	}
	size := int64(1) << uint(v.info.PackLog2)
	if err := h.Locks.Touch(ctx, v.key, r.Header.Get(VaultLockHeader)); err != nil {
		lockAnswer(w, r, err)
		return
	}
	if r.ContentLength >= 0 && r.ContentLength != size {
		vaultBadObject(w, r, "a pack is exactly 2^"+strconv.Itoa(v.info.PackLog2)+" bytes")
		return
	}
	packRel := v.at(e2e.VaultPackPath(id))
	if gate(w, r, h.M.ACL, v.st.ID, writegate.Writes(packRel).ForVault()) {
		return
	}
	if storage.Exists(ctx, v.drv, packRel) {
		vaultRefuse(w, r, http.StatusConflict, "VAULT_PACK_EXISTS", "server.e2e.vault.pack_exists", nil, nil)
		return
	}
	if h.quotaRefused(w, r, size) {
		return
	}
	wr, ok := v.drv.(storage.Writer)
	if !ok {
		writeJSON(w, http.StatusNotImplemented, map[string]string{"error": "driver does not support write"})
		return
	}
	body := http.MaxBytesReader(w, r.Body, size)
	hdr := make([]byte, e2e.VaultPackHeaderLen)
	if _, err := io.ReadFull(body, hdr); err != nil {
		vaultBadObject(w, r, "shorter than a pack header")
		return
	}
	if err := e2e.CheckVaultPackHeader(hdr, id, v.info.PackLog2); err != nil {
		vaultBadObject(w, r, err.Error())
		return
	}
	vaultMkdirs(ctx, v.drv, v.rel, path.Dir(e2e.VaultPackPath(id)))
	rest := &exactReader{r: body, remain: size - int64(len(hdr))}
	if err := wr.Write(ctx, packRel, io.MultiReader(bytes.NewReader(hdr), rest), size); err != nil {
		vaultRemove(context.WithoutCancel(ctx), v.drv, packRel)
		if rest.bad || errors.Is(err, errVaultBodySize) {
			vaultBadObject(w, r, "a pack is exactly 2^"+strconv.Itoa(v.info.PackLog2)+" bytes")
			return
		}
		writeJSON(w, mapDriverErr(err), map[string]string{"error": "write: " + err.Error()})
		return
	}
	h.recordFile(ctx, v.st, v.drv, packRel, size, vaultObjectMime)
	writeJSON(w, http.StatusCreated, map[string]any{"id": hex.EncodeToString(id[:])})
}

// ── PUT /index ──────────────────────────────────────────────────────────

// PutIndex commits generation latest + 1: an index file with the header of
// that generation and a Padmé size of 64 KiB to 64 MiB. It is written to
// v/idx/.tmp-<random> and renamed into place - one PUT where the storage
// cannot rename (S3: atomic) - and abandoned after 60 seconds, never moved
// into place afterwards. Until it ends the lock is not free, even if it ends.
//
//	PUT /api/files/e2e/vault/index?path=&generation= (body: the index file) + token
//	  → 201 {generation} · 409 VAULT_GENERATION {latest} · 400 VAULT_BAD_OBJECT
func (h *E2EVault) PutIndex(w http.ResponseWriter, r *http.Request) {
	if !h.on(w, r) {
		return
	}
	ctx := r.Context()
	q := r.URL.Query()
	gen, err := strconv.ParseUint(q.Get("generation"), 10, 64)
	if err != nil || gen == 0 || gen > 1<<53-1 {
		vaultBadRequest(w, r, "generation is a positive integer")
		return
	}
	v, ok := h.open(w, r, q.Get("path"), vaultWrite)
	if !ok {
		return
	}
	token := r.Header.Get(VaultLockHeader)
	if err := h.Locks.BeginIndex(ctx, v.key, token); err != nil {
		if errors.Is(err, vaultlock.ErrIndexRunning) {
			idx, _ := vaultIndexes(ctx, v.drv, v.rel, false, h.Locks.Now())
			vaultRefuse(w, r, http.StatusConflict, "VAULT_GENERATION", "server.e2e.vault.generation",
				srvtext.Vars{"latest": strconv.FormatUint(latestOf(idx), 10)}, map[string]any{"latest": latestOf(idx)})
			return
		}
		lockAnswer(w, r, err)
		return
	}
	begun := h.Locks.Now()
	deadline := begun.Add(vaultlock.IndexWriteMax)
	ended := false
	end := func(committed bool) {
		if ended {
			return
		}
		ended = true
		if err := h.Locks.EndIndex(context.WithoutCancel(ctx), v.key, token, int64(gen), committed); err != nil {
			slog.Warn("vault: end index write", slog.String("err", err.Error()))
		}
	}
	defer end(false)

	idx, err := vaultIndexes(ctx, v.drv, v.rel, true, begun)
	if err != nil {
		writeJSON(w, mapDriverErr(err), map[string]string{"error": "list: " + err.Error()})
		return
	}
	latest := latestOf(idx)
	if gen != latest+1 {
		vaultRefuse(w, r, http.StatusConflict, "VAULT_GENERATION", "server.e2e.vault.generation",
			srvtext.Vars{"latest": strconv.FormatUint(latest, 10)}, map[string]any{"latest": latest})
		return
	}
	finalRel := v.at(e2e.VaultIndexPath(gen))
	var (
		body io.Reader
		size int64
	)
	if r.ContentLength >= 0 {
		size = r.ContentLength
		if !e2e.ValidVaultIndexSize(size) {
			vaultBadObject(w, r, "an index file is a Padmé size from 65536 bytes to 64 MiB")
			return
		}
		body = http.MaxBytesReader(w, r.Body, size)
	} else {
		buf, rerr := io.ReadAll(http.MaxBytesReader(w, r.Body, e2e.VaultIndexReadMax))
		if rerr != nil {
			vaultBadObject(w, r, "an index file is at most 64 MiB")
			return
		}
		size = int64(len(buf))
		if !e2e.ValidVaultIndexSize(size) {
			vaultBadObject(w, r, "an index file is a Padmé size from 65536 bytes to 64 MiB")
			return
		}
		body = bytes.NewReader(buf)
	}
	hdr := make([]byte, e2e.VaultIndexHeaderLen)
	if _, err := io.ReadFull(body, hdr); err != nil {
		vaultBadObject(w, r, "shorter than an index header")
		return
	}
	if err := e2e.CheckVaultIndexHeader(hdr, gen); err != nil {
		vaultBadObject(w, r, err.Error())
		return
	}
	mover, canMove := v.drv.(storage.Mover)
	direct := !canMove || v.drv.Name() == "s3"
	target := finalRel
	if !direct {
		target = v.at("v/idx/" + e2e.VaultTempPrefix + vaultRandHex(8))
	}
	if gate(w, r, h.M.ACL, v.st.ID, writegate.Writes(finalRel).ForVault(), writegate.Writes(target).ForVault()) {
		return
	}
	if h.quotaRefused(w, r, size) {
		return
	}
	wr, ok := v.drv.(storage.Writer)
	if !ok {
		writeJSON(w, http.StatusNotImplemented, map[string]string{"error": "driver does not support write"})
		return
	}
	vaultMkdirs(ctx, v.drv, v.rel, "v/idx")
	wctx, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()
	rest := &exactReader{r: body, remain: size - int64(len(hdr))}
	werr := wr.Write(wctx, target, io.MultiReader(bytes.NewReader(hdr), rest), size)
	if werr == nil && !direct {
		if h.Locks.Now().After(deadline) {
			werr = context.DeadlineExceeded
		} else {
			werr = mover.Move(wctx, target, finalRel)
		}
	}
	if werr != nil {
		vaultRemove(context.WithoutCancel(ctx), v.drv, target)
		switch {
		case rest.bad || errors.Is(werr, errVaultBodySize):
			vaultBadObject(w, r, "the body is not the size it announced")
		case errors.Is(werr, context.DeadlineExceeded) || wctx.Err() != nil:
			vaultRefuse(w, r, http.StatusServiceUnavailable, "VAULT_TIMEOUT", "server.e2e.vault.timeout", nil, nil)
		default:
			writeJSON(w, mapDriverErr(werr), map[string]string{"error": "write: " + werr.Error()})
		}
		return
	}
	end(true)
	h.recordFile(ctx, v.st, v.drv, finalRel, size, vaultObjectMime)
	emitVault(v.st.ID, v.rel, realtime.VaultEvent{Type: realtime.VaultGenerationEvent, Generation: gen})
	writeJSON(w, http.StatusCreated, map[string]any{"generation": gen})
}

func vaultRandHex(n int) string {
	b := make([]byte, n)
	if _, err := crand.Read(b); err != nil {
		return strconv.FormatInt(time.Now().UnixNano(), 16)
	}
	return hex.EncodeToString(b)
}

// ── POST /delete ────────────────────────────────────────────────────────

type vaultDeleteReq struct {
	Path    string   `json:"path"`
	Packs   []string `json:"packs"`
	Indexes []uint64 `json:"indexes"`
}

// Delete removes packs and index files for good - no trash, no version - for
// the lock holder's garbage collection. At most 1 000 names; never one of the
// three newest generations; a missing file counts as deleted.
//
//	POST /api/files/e2e/vault/delete {path, packs, indexes} + token
//	  → 200 {deleted: {packs, indexes}} · 400 VAULT_KEEP
func (h *E2EVault) Delete(w http.ResponseWriter, r *http.Request) {
	if !h.on(w, r) {
		return
	}
	ctx := r.Context()
	body, ok := confinedBody(w, r, vaultJSONMax)
	if !ok {
		return
	}
	var req vaultDeleteReq
	if err := json.Unmarshal(body, &req); err != nil {
		vaultBadRequest(w, r, "bad json")
		return
	}
	if len(req.Packs)+len(req.Indexes) > vaultDeleteMax {
		vaultBadRequest(w, r, "at most 1000 names at a time")
		return
	}
	ids := make([][16]byte, 0, len(req.Packs))
	for _, p := range req.Packs {
		id, ok := e2e.ParseVaultID(p)
		if !ok {
			vaultBadRequest(w, r, "a pack id is 32 lower-case hex digits")
			return
		}
		ids = append(ids, id)
	}
	for _, g := range req.Indexes {
		if g == 0 {
			vaultBadRequest(w, r, "a generation is a positive integer")
			return
		}
	}
	v, ok := h.open(w, r, req.Path, vaultWrite)
	if !ok {
		return
	}
	if err := h.Locks.Touch(ctx, v.key, r.Header.Get(VaultLockHeader)); err != nil {
		lockAnswer(w, r, err)
		return
	}
	idx, err := vaultIndexes(ctx, v.drv, v.rel, false, h.Locks.Now())
	if err != nil {
		writeJSON(w, mapDriverErr(err), map[string]string{"error": "list: " + err.Error()})
		return
	}
	// The three newest generations: the three highest that are there, and
	// whatever is numbered above latest - 3.
	keep := map[uint64]bool{}
	for i := len(idx) - 1; i >= 0 && i >= len(idx)-e2e.VaultKeepGenerations; i-- {
		keep[idx[i].gen] = true
	}
	latest := latestOf(idx)
	for _, g := range req.Indexes {
		if keep[g] || g+e2e.VaultKeepGenerations > latest {
			vaultRefuse(w, r, http.StatusBadRequest, "VAULT_KEEP", "server.e2e.vault.keep", nil, map[string]any{"generation": g})
			return
		}
	}
	targets := make([]writegate.Target, 0, len(ids)+len(req.Indexes))
	for _, id := range ids {
		targets = append(targets, writegate.Writes(v.at(e2e.VaultPackPath(id))).ForVault())
	}
	for _, g := range req.Indexes {
		targets = append(targets, writegate.Writes(v.at(e2e.VaultIndexPath(g))).ForVault())
	}
	if gate(w, r, h.M.ACL, v.st.ID, targets...) {
		return
	}
	if _, ok := v.drv.(storage.Deleter); !ok {
		writeJSON(w, http.StatusNotImplemented, map[string]string{"error": "driver does not support delete"})
		return
	}
	packs := make([]string, 0, len(ids))
	for _, id := range ids {
		rel := v.at(e2e.VaultPackPath(id))
		vaultRemove(ctx, v.drv, rel)
		h.forgetFile(ctx, v.st, rel)
		packs = append(packs, hex.EncodeToString(id[:]))
	}
	gens := make([]uint64, 0, len(req.Indexes))
	for _, g := range req.Indexes {
		rel := v.at(e2e.VaultIndexPath(g))
		vaultRemove(ctx, v.drv, rel)
		h.forgetFile(ctx, v.st, rel)
		gens = append(gens, g)
	}
	writeJSON(w, http.StatusOK, map[string]any{"deleted": map[string]any{"packs": len(packs), "indexes": len(gens)}})
}

// ── /prefs ──────────────────────────────────────────────────────────────

// GetPrefs answers the caller's idle time: what they set, else 3.
//
//	GET /api/files/e2e/vault/prefs → 200 {idle_minutes}
func (h *E2EVault) GetPrefs(w http.ResponseWriter, r *http.Request) {
	if !h.on(w, r) {
		return
	}
	u := auth.UserFrom(r.Context())
	if u == nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	n, err := h.Locks.IdleMinutes(r.Context(), u.ID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"idle_minutes": n})
}

// PutPrefs sets the caller's idle time, 1 to 10 minutes. It is kept for the
// person, not the browser; a lock already held keeps the time it was taken
// with.
//
//	PUT /api/files/e2e/vault/prefs {idle_minutes} → 200 {idle_minutes}
func (h *E2EVault) PutPrefs(w http.ResponseWriter, r *http.Request) {
	if !h.on(w, r) {
		return
	}
	u := auth.UserFrom(r.Context())
	if u == nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	var body struct {
		IdleMinutes *int `json:"idle_minutes"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&body); err != nil || body.IdleMinutes == nil {
		vaultBadRequest(w, r, "idle_minutes is an integer")
		return
	}
	if err := h.Locks.SetIdleMinutes(r.Context(), u.ID, *body.IdleMinutes); err != nil {
		if errors.Is(err, vaultlock.ErrIdleRange) {
			vaultRefuse(w, r, http.StatusBadRequest, "VAULT_BAD_REQUEST", "server.e2e.vault.idle_range", nil, nil)
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"idle_minutes": *body.IdleMinutes})
}

// ── the key file through the explorer's upload ──────────────────────────

// vaultKeyFileMax is what is read of an uploaded key file to judge it.
const vaultKeyFileMax = 1 << 20

// refuseVaultKeyFile judges an upload of a key file (`.filex-e2e.json`) at
// rel: src is the new bytes (rewound after), the key file there now is read
// from drv. 409 VAULT_KEYFILE when the write would change a vault's v, req or
// vault, or make a folder a vault (e2e.KeepsVaultBlock). Reports whether it
// answered.
func refuseVaultKeyFile(w http.ResponseWriter, r *http.Request, drv storage.Driver, rel string, src io.ReadSeeker) bool {
	after, err := io.ReadAll(io.LimitReader(src, vaultKeyFileMax+1))
	if _, serr := src.Seek(0, io.SeekStart); err != nil || serr != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "rewind part"})
		return true
	}
	var before []byte
	if storage.Exists(r.Context(), drv, rel) {
		before = readSmall(r.Context(), drv, rel)
	}
	if e2e.KeepsVaultBlock(before, after) {
		return false
	}
	answerVaultGate(w, langOf(r), writegate.ErrVaultKeyFile)
	return true
}
