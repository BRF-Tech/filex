package handlers

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/brf-tech/filex/backend/internal/acl"
	"github.com/brf-tech/filex/backend/internal/apierr"
	"github.com/brf-tech/filex/backend/internal/auth"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/e2epolicy"
	"github.com/brf-tech/filex/backend/internal/filebody"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/pathkey"
	"github.com/brf-tech/filex/backend/internal/perm"
	"github.com/brf-tech/filex/backend/internal/quotastore"
	"github.com/brf-tech/filex/backend/internal/search"
	"github.com/brf-tech/filex/backend/internal/share"
	"github.com/brf-tech/filex/backend/internal/srvtext"
	"github.com/brf-tech/filex/backend/internal/storage"
	"github.com/brf-tech/filex/backend/internal/tenant"
	"github.com/brf-tech/filex/backend/internal/tenanturl"
	"github.com/brf-tech/filex/backend/internal/thumb"
	"github.com/brf-tech/filex/backend/internal/writehook"
	"github.com/brf-tech/filex/backend/pkg/pluginkit/wire"
)

// # The upload door: POST /api/uploads
//
// One request, one new file, one link back - for any program that can send
// a multipart form: a screenshot tool (ShareX, Flameshot), a phone shortcut,
// curl, a script.
//
//	POST /api/uploads                         (multipart/form-data)
//	  Authorization: Bearer <uploader key>    (or X-Filex-Uploader-Key)
//	  file → the bytes
//	→ 200 {"url":…, "deletion_url":…, "thumbnail_url":…}
//	→ 4xx/5xx {"error":"<a sentence>", "code":"<code>"}
//
// The credential is an UPLOADER's key (model.Uploader), never an API token:
// this is the only door that looks one up, so the key reaches nothing else.
// What the request may say is the file; WHERE it lands and WHAT the link
// opens are the uploader's settings, made in filex - a tool's config file
// carries no folder, no visibility and no lifetime, so copying it around
// cannot widen any of them.
//
// The write goes through the same aiOps core the agent API uses (stored AND
// indexed, staged above the chunk threshold, quota, the encryption rules, a
// thumbnail, the write hook with origin "uploader"), as the uploader's owner
// as the account is NOW, under a random-prefixed name so an upload never
// replaces another.
type Uploads struct {
	core uploadCore
	// multiTenant is the mode in force (AttachMultiTenant): an uploader's
	// owner is then asked again under their tenant's current scope.
	multiTenant bool
	// Shorten answers an upload's short address for an uploader that asks
	// for one (ShortLinks.ForUpload, 0.60), "" for none. Nil: never.
	Shorten func(ctx context.Context, owner *model.User, target wire.ShortenTarget) string
}

// NewUploads builds the upload door. publicURL is the base links are made on
// when no tenant names another; shareSvc mints the public links.
func NewUploads(store db.Store, resolver func(int64) (storage.Driver, error), shareSvc *share.Service, publicURL string) *Uploads {
	ops := newAIOps(store, resolver, shareSvc, publicURL)
	ops.origin = writehook.OriginUploader
	return &Uploads{core: uploadCore{ops: ops}}
}

// AttachTenants wires the shared origin resolver (internal/tenanturl).
func (h *Uploads) AttachTenants(rv tenanturl.Resolver) { h.core.ops.tenants = rv }

// AttachACL wires the permission resolver: the write and the link are the
// owner's, under the owner's grants and permissions.
func (h *Uploads) AttachACL(r *acl.Resolver) { h.core.ops.acl = r }

// AttachE2EPolicy wires who may encrypt (e2e_policy_gate.go).
func (h *Uploads) AttachE2EPolicy(s *e2epolicy.Service) { h.core.ops.e2e = s }

// AttachThumbs wires the thumbnail pipeline (nil = none drawn).
func (h *Uploads) AttachThumbs(p *thumb.Pipeline) { h.core.ops.thumbs = p }

// AttachSearchIndex wires the search index - an upload is findable at once.
func (h *Uploads) AttachSearchIndex(idx *search.Index) { h.core.ops.attachSearchIndex(idx) }

// AttachStaged routes uploads above the chunk threshold through staging.
func (h *Uploads) AttachStaged(s *StagedUpload) { h.core.ops.staged = s }

// AttachBody wires the byte-source resolver (staging-aware reads).
func (h *Uploads) AttachBody(b *filebody.Resolver) { h.core.ops.attachBody(b) }

// AttachMultiTenant tells the door the mode in force.
func (h *Uploads) AttachMultiTenant(on bool) { h.multiTenant = on }

// uploadMaxBytes caps one upload. The bytes are streamed, so this is not a
// memory ceiling - it bounds what one request may push into the multipart
// spool and the staging filesystem. Matches the agent API's ceiling.
const uploadMaxBytes = 512 << 20 // 512 MiB

// uploadDefaultFolder is where an uploader's files go unless its owner says
// otherwise. Neutral on purpose: the folder is the product's, not a client's.
const uploadDefaultFolder = "uploads"

// uploaderKeyPrefix marks an uploader's key, so a person (or a secret
// scanner) reading one in a config file knows what it is and what it opens.
const uploaderKeyPrefix = "fxu_"

// uploaderKeyHeader is the dedicated header; `Authorization: Bearer` works
// as well, and is what the generated configs send.
const uploaderKeyHeader = "X-Filex-Uploader-Key"

// newUploaderKey mints a key: the prefix and 32 random bytes.
func newUploaderKey() (string, error) {
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return uploaderKeyPrefix + base64.RawURLEncoding.EncodeToString(raw[:]), nil
}

// hashSecret is the stored form of a bearer secret (a key, a deletion
// token): its sha256 in hex. The server only ever compares.
func hashSecret(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

// uploaderKeyFrom reads the key a request carries, "" for none.
func uploaderKeyFrom(r *http.Request) string {
	if v := strings.TrimSpace(r.Header.Get(uploaderKeyHeader)); v != "" {
		return v
	}
	const bearer = "Bearer "
	if h := r.Header.Get("Authorization"); strings.HasPrefix(h, bearer) {
		return strings.TrimSpace(h[len(bearer):])
	}
	return ""
}

// writeUploadError answers the door: `error` is a SENTENCE, in the reader's
// language, because the program on the other end (ShareX's ErrorMessage,
// a shortcut's alert) shows it to a person as it is; `code` is what a
// program branches on.
func writeUploadError(w http.ResponseWriter, r *http.Request, status int, code string, params apierr.Params) {
	body := errorBody(r, code, params)
	body["code"] = code
	body["error"] = body["message"]
	delete(body, "message")
	writeJSON(w, status, body)
}

// writeUploadFailure answers a failed write: the aiOps error's own text (it
// is already a sentence) under its code, or the encryption rule's sentence.
func writeUploadFailure(w http.ResponseWriter, r *http.Request, err error) {
	var re *e2epolicy.RefusedError
	if errors.As(err, &re) {
		writeJSON(w, http.StatusForbidden, map[string]string{
			"error": srvtext.Text(langOf(r), "server.e2e.not_allowed."+string(re.Reason), nil),
			"code":  "e2e_not_allowed",
		})
		return
	}
	var ue *entryUnavailableError
	if errors.As(err, &ue) {
		writeJSON(w, http.StatusConflict, ue.body(""))
		return
	}
	writeJSON(w, aiStatus(err), aiErrorBody(err))
}

// Upload serves POST /api/uploads.
func (h *Uploads) Upload(w http.ResponseWriter, r *http.Request) {
	// Spilled multipart temp files outlive the response unless dropped here.
	defer func() {
		if r.MultipartForm != nil {
			_ = r.MultipartForm.RemoveAll()
		}
	}()
	key := uploaderKeyFrom(r)
	if key == "" {
		writeUploadError(w, r, http.StatusUnauthorized, "uploader_key_missing", nil)
		return
	}
	store := h.core.ops.store
	up, err := store.GetUploaderByKey(r.Context(), hashSecret(key))
	if err != nil || up == nil {
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			slog.Error("uploads: key lookup failed", slog.String("err", err.Error()))
		}
		writeUploadError(w, r, http.StatusUnauthorized, "uploader_key_unknown", nil)
		return
	}

	// Before a byte of the body is read: the owner as they are now, the
	// tenant the uploader was made in, and the storage behind the id.
	ctx, st, ok := h.asOwner(w, r, up)
	if !ok {
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, uploadMaxBytes+(1<<20))
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			writeUploadError(w, r, http.StatusRequestEntityTooLarge, "upload_too_large", apierr.Params{"max_mb": strconv.Itoa(uploadMaxBytes >> 20)})
			return
		}
		writeUploadError(w, r, http.StatusBadRequest, "upload_bad_form", nil)
		return
	}
	f, fh, err := r.FormFile("file")
	if err != nil {
		writeUploadError(w, r, http.StatusBadRequest, "upload_file_missing", nil)
		return
	}
	defer f.Close()
	if fh.Size > uploadMaxBytes {
		writeUploadError(w, r, http.StatusRequestEntityTooLarge, "upload_too_large", apierr.Params{"max_mb": strconv.Itoa(uploadMaxBytes >> 20)})
		return
	}

	public := up.Visibility == model.UploaderPublic
	stored, err := h.core.store(ctx, st, up.Folder, fh.Filename, f, fh.Size, public)
	if err != nil {
		if errors.Is(err, errUploadPublicRefused) {
			writeUploadError(w, r, http.StatusForbidden, "uploader_public_denied", nil)
			return
		}
		writeUploadFailure(w, r, err)
		return
	}
	_ = store.TouchUploader(ctx, up.ID)
	// The audit row the key's write gets: the owner, the file, the uploader
	// that sent it - what a person reads after a key leaked. Written here
	// because the door has no session for the middleware to read.
	owner := up.UserID
	_ = store.InsertAuditEntry(ctx, &model.AuditEntry{
		UserID:     &owner,
		Action:     "uploader.upload",
		TargetType: "node",
		TargetID:   strconv.FormatInt(stored.nodeID, 10),
		Metadata:   map[string]interface{}{"target_name": stored.qualified, "uploader": up.Name},
		IP:         clientIP(r),
		CreatedAt:  time.Now(),
	})

	reply := uploadReply{}
	if public {
		res, err := h.core.ops.CreateShare(ctx, stored.qualified, false, up.LinkDays, 0)
		if err != nil {
			// The file is in the folder; only its link could not be made.
			// Say so rather than answer a link that does not exist - the
			// person finds the file in filex, and nothing is left half-told.
			writeUploadFailure(w, r, err)
			return
		}
		reply.URL = inlineURL(res.URL)
		reply.shareToken = res.Token
		if strings.HasPrefix(stored.mime, "image/") {
			reply.ThumbnailURL = reply.URL
		}
	} else {
		reply.URL = privateFileURL(h.core.ops.tenants.ForStorage(ctx, st.ID), st.Name, stored.rel)
	}

	// The short address, when the uploader asks for one and an app holding
	// the link-shortener role gives it; otherwise the link as it is.
	if up.ShortLink && h.Shorten != nil {
		if owner, err := store.GetUser(ctx, up.UserID); err == nil && owner != nil {
			if short := h.Shorten(ctx, owner, wire.ShortenTarget{Kind: wire.ShortenUpload, URL: reply.URL, Link: reply.shareToken, Name: path.Base(stored.rel)}); short != "" {
				reply.URL = short
			}
		}
	}
	del, err := h.core.mintDeletion(ctx, up, st, stored, reply.shareToken)
	if err != nil {
		// The upload stands; a missing deletion link is not worth failing
		// the request the person is waiting on. Logged, and said in place.
		slog.Error("uploads: deletion link not made", slog.Int64("uploader", up.ID), slog.String("err", err.Error()))
	} else {
		reply.DeletionURL = del
	}
	writeJSON(w, http.StatusOK, presentUpload(reply))
}

// asOwner is the context an uploader's write runs in, and its storage as it
// reads now - asked of the owner as they are NOW: the account may still sign
// in and use keys (auth.RefuseBlockedAccount, the answer every key door
// gives; access.api), it is still in a tenant that has the storage (the stamp
// the uploader was made with, and the owner's current scope), and the storage
// behind the id is still there. On a refusal it has answered and ok is false.
func (h *Uploads) asOwner(w http.ResponseWriter, r *http.Request, up *model.Uploader) (context.Context, *model.Storage, bool) {
	store := h.core.ops.store
	ctx := r.Context()
	owner, err := store.GetUser(ctx, up.UserID)
	if err != nil || owner == nil {
		writeUploadError(w, r, http.StatusUnauthorized, "uploader_key_unknown", nil)
		return nil, nil, false
	}
	if auth.RefuseBlockedAccount(w, r, store, owner) {
		return nil, nil, false
	}
	uctx := asTicketMinter(ctx, store, h.multiTenant, owner, nil)
	if h.core.ops.acl != nil && !callerCan(uctx, h.core.ops.acl, perm.AccessAPI) {
		writeUploadError(w, r, http.StatusForbidden, "uploader_owner_no_keys", nil)
		return nil, nil, false
	}
	if !db.StampStands(ctx, store, up.TenantID, up.StorageID) {
		writeUploadError(w, r, http.StatusGone, "uploader_storage_gone", nil)
		return nil, nil, false
	}
	if scope, ok := tenant.FromContext(uctx); ok && scope != nil && !scope.CanAccessStorage(up.StorageID) {
		writeUploadError(w, r, http.StatusGone, "uploader_storage_gone", nil)
		return nil, nil, false
	}
	st, err := store.GetStorage(ctx, up.StorageID)
	if err != nil || st == nil {
		writeUploadError(w, r, http.StatusGone, "uploader_storage_gone", nil)
		return nil, nil, false
	}
	return quotastore.WithOwner(uctx, owner.ID), st, true
}

// uploadReply is what the door answers. ⚠ presentUpload is the ONE place a
// reply becomes the wire answer: a link shortener, when filex has one, goes
// there and nowhere else, so every upload - whichever client sent it - gets
// the same links.
type uploadReply struct {
	URL          string `json:"url"`
	DeletionURL  string `json:"deletion_url,omitempty"`
	ThumbnailURL string `json:"thumbnail_url,omitempty"`
	// shareToken is the public link's token ("" for a private link), for
	// the deletion link to revoke.
	shareToken string
}

// presentUpload turns a reply into the wire answer (see uploadReply).
func presentUpload(r uploadReply) uploadReply { return r }

// inlineURL appends ?inline=1 (respecting an existing query string) so the
// link's download endpoint serves the bytes with Content-Disposition: inline:
// images and text render in the browser instead of downloading.
func inlineURL(u string) string {
	if u == "" {
		return u
	}
	sep := "?"
	if strings.Contains(u, "?") {
		sep = "&"
	}
	return u + sep + "inline=1"
}

// privateFileURL is the address of a file IN filex: the explorer, opened on
// its folder with the file selected (Explore.vue `?select=`, the shape a
// notification's link has - packages/core lib/notificationTarget
// notificationHref). It opens for whoever may see the file there, after
// signing in, and for nobody else.
//
// ⚠ The end-user front door (/drive, routes.go UserUIPrefix), not /admin:
// the person who follows the link is a reader, not an operator. The hash is
// the explorer's own path form, `<storage>/<folder>`, each segment escaped on
// its own (encodeHashPath); the select is the qualified `<storage>://<path>`.
func privateFileURL(origin, storageName, rel string) string {
	folder := aiParent(rel)
	hash := storageName
	if folder != "" {
		hash += "/" + folder
	}
	segs := strings.Split(hash, "/")
	for i, s := range segs {
		segs[i] = url.PathEscape(s)
	}
	return strings.TrimRight(origin, "/") + UserUIPrefix + "/explore?select=" +
		url.QueryEscape(joinAdapterPath(storageName, rel)) + "#" + strings.Join(segs, "/")
}

// UserUIPrefix mirrors api.UserUIPrefix (routes.go), the end-user front door;
// declared here because package api imports this one, not the reverse.
const UserUIPrefix = "/drive"

// errUploadPublicRefused: the owner may no longer make the public link the
// uploader is set to answer with - asked BEFORE the bytes land, so a refused
// upload leaves no file behind.
var errUploadPublicRefused = errors.New("uploads: the owner may not make public links")

// uploadCore is what the upload door and the deprecated ShareX endpoint
// share: the folder chain, a fresh name, the write.
type uploadCore struct {
	ops *aiOps
}

// storedUpload is where a write landed.
type storedUpload struct {
	rel       string // path inside the storage
	qualified string // adapter://rel, as it read at the write
	name      string // the stored name
	mime      string
	nodeID    int64
}

// store writes one upload into folder on st under a fresh name, as the
// context's account. public asks, before a byte is read, whether that
// account may link the file publicly (publicLinkRefusal, the rule every
// link door asks) - a refused upload leaves nothing behind.
func (c uploadCore) store(ctx context.Context, st *model.Storage, folder, filename string, src io.Reader, size int64, public bool) (*storedUpload, error) {
	folder = cleanUploadFolder(folder)
	if folder != "" {
		if err := c.ensureFolder(ctx, st, folder); err != nil {
			return nil, err
		}
	}
	rel, err := c.freshName(ctx, st, folder, filename)
	if err != nil {
		return nil, err
	}
	if public {
		if err := publicLinkRefusal(ctx, c.ops.acl, c.ops.store, st.ID, rel, perm.ShareLinks); err != nil {
			var lr *linkRefusal
			if errors.As(err, &lr) || errors.Is(err, errLinkNeedsEdit) {
				return nil, errUploadPublicRefused
			}
			return nil, err
		}
	}
	qualified := joinAdapterPath(st.Name, rel)
	entry, err := c.ops.WriteStreamOn(ctx, st.ID, qualified, src, size)
	if err != nil {
		return nil, err
	}
	out := &storedUpload{rel: rel, qualified: qualified, name: path.Base(rel), mime: entry.Mime}
	if out.mime == "" {
		out.mime = mime.TypeByExtension(path.Ext(rel))
	}
	if n, err := c.ops.store.GetNodeByPath(ctx, st.ID, pathkey.Hash(st.ID, rel)); err == nil && n != nil {
		out.nodeID = n.ID
		if out.mime == "" {
			out.mime = n.Mime
		}
	}
	return out, nil
}

// ensureFolder makes every segment of folder that is not there yet, and
// mirrors the ones that are into the catalogue, so the whole parent chain has
// a row before the file is written - a write into an un-catalogued folder
// leaves no row for the link to name.
//
// ⚠ A folder that is there is not made again: Mkdir asks files.create in the
// folder ABOVE, and a person whose access starts at their uploads folder
// would be refused for a folder that exists.
func (c uploadCore) ensureFolder(ctx context.Context, st *model.Storage, folder string) error {
	drv, err := c.ops.resolver(st.ID)
	if err != nil {
		return err
	}
	cur := ""
	for _, seg := range strings.Split(folder, "/") {
		if seg == "" {
			continue
		}
		if cur == "" {
			cur = seg
		} else {
			cur += "/" + seg
		}
		if info, serr := drv.Stat(ctx, cur); serr == nil && info.Kind == storage.KindDirectory {
			c.ops.cacheUpsertDir(ctx, st, cur)
			continue
		}
		if _, err := c.ops.Mkdir(ctx, joinAdapterPath(st.Name, cur)); err != nil {
			return err
		}
	}
	return nil
}

// freshName is the path a new upload is stored at: the sent name, stripped
// of any directory a client put in it, behind a random prefix - and never a
// path that is taken, so an upload cannot replace a file (or repoint the link
// an earlier upload answered with).
func (c uploadCore) freshName(ctx context.Context, st *model.Storage, folder, filename string) (string, error) {
	drv, err := c.ops.resolver(st.ID)
	if err != nil {
		return "", err
	}
	for i := 0; i < 8; i++ {
		rel := uploadFilename(filename)
		if folder != "" {
			rel = folder + "/" + rel
		}
		if _, serr := drv.Stat(ctx, rel); serr != nil {
			return rel, nil
		}
	}
	return "", errors.New("uploads: no free name for the upload")
}

// mintDeletion makes the upload's deletion link: a random token, of which
// only the hash is kept, naming the file's catalogue row on the storage by
// id. It returns the link.
func (c uploadCore) mintDeletion(ctx context.Context, up *model.Uploader, st *model.Storage, s *storedUpload, shareToken string) (string, error) {
	if s.nodeID <= 0 {
		return "", errors.New("the upload has no catalogue row")
	}
	var raw [24]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	tok := base64.RawURLEncoding.EncodeToString(raw[:])
	var shareID int64
	if shareToken != "" {
		if sh, err := c.ops.store.GetShareByToken(ctx, shareToken); err == nil && sh != nil {
			shareID = sh.ID
		}
	}
	stamp, _ := db.StorageStamp(ctx, c.ops.store, st.ID)
	if err := c.ops.store.CreateUploadDeletion(ctx, &model.UploadDeletion{
		TokenHash:  hashSecret(tok),
		UserID:     up.UserID,
		UploaderID: up.ID,
		StorageID:  st.ID,
		NodeID:     s.nodeID,
		ShareID:    shareID,
		TenantID:   stamp,
		Name:       s.name,
	}); err != nil {
		return "", err
	}
	return strings.TrimRight(c.ops.tenants.ForStorage(ctx, st.ID), "/") + "/x/" + tok, nil
}

// uploadFilename derives a safe, unique storage name from the multipart
// part's filename: any directory component dropped (Windows clients may send
// a full path), the basename sanitized, a short random prefix in front.
func uploadFilename(raw string) string {
	raw = strings.ReplaceAll(raw, "\\", "/")
	base := strings.TrimSpace(path.Base(raw))
	if base == "" || base == "." || base == "/" {
		base = "upload"
	}
	// sanitizeFilename neutralizes quote/backslash/newline; drop any residual
	// separators so the name stays a single path segment.
	base = strings.ReplaceAll(sanitizeFilename(base), "/", "_")
	return randHex6() + "-" + base
}

// cleanUploadFolder normalizes a folder into a safe bare relative path: any
// adapter:// prefix dropped, "." and ".." segments stripped (traversal defence
// on top of resolveStorage's own checks), each remaining segment sanitized.
// An empty or invalid input yields "" (the storage's root).
func cleanUploadFolder(raw string) string {
	raw = strings.ReplaceAll(raw, "\\", "/")
	if i := strings.Index(raw, "://"); i >= 0 {
		raw = raw[i+3:]
	}
	out := make([]string, 0)
	for _, seg := range strings.Split(raw, "/") {
		seg = strings.TrimSpace(seg)
		if seg == "" || seg == "." || seg == ".." {
			continue
		}
		out = append(out, sanitizeFilename(seg))
	}
	return strings.Join(out, "/")
}
