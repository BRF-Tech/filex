package handlers

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"

	"github.com/brf-tech/filex/backend/internal/acl"
	"github.com/brf-tech/filex/backend/internal/apierr"
	"github.com/brf-tech/filex/backend/internal/auth"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/perm"
	"github.com/brf-tech/filex/backend/internal/share"
	"github.com/brf-tech/filex/backend/internal/storage"
	"github.com/brf-tech/filex/backend/internal/tenanturl"
)

// Uploaders is the self-service surface at /api/auth/uploaders: a person's
// upload destinations - make one, change it, give it a new key, delete it.
//
//	GET    /api/auth/uploaders          → {uploaders, endpoint, link_max_days, may_link_publicly, may_create, default_folder, storages}
//	POST   /api/auth/uploaders          → 201 {uploader, key, setup}             (access.api)
//	PATCH  /api/auth/uploaders/{id}     → {uploader}                             (access.api)
//	POST   /api/auth/uploaders/{id}/key → {uploader, key, setup}                 (access.api)
//	DELETE /api/auth/uploaders/{id}     → 204
//
// It sits in routes.go's self-service CREDENTIAL group (RequirePersonalCaller:
// an app token has no person to make an uploader for). Making or changing one
// asks access.api - an uploader is a key a program outside the web app uses,
// as an API token is - and, from a personal API key (the desktop app's), the
// key's `write` and a folder inside its `root:`: a key makes no credential
// wider than itself. Deleting one asks nothing: a person may always take a
// credential of theirs away.
//
// ⚠ The key is said exactly once, in the answer that made it (`key`, and in
// `setup`, the ready-made client settings built from it). Only its hash is
// stored; a lost key is replaced, never shown again.
type Uploaders struct {
	ops *aiOps
	// ShortLinks answers whether this person is offered a short link for
	// their uploads (ShortLinks.Available, 0.60). Nil: never.
	ShortLinks func(r *http.Request) bool
}

// NewUploaders builds the surface. resolver and publicURL are the agent
// API's: the folder an uploader names is made, and asked about, through the
// same aiOps core its uploads go through.
func NewUploaders(store db.Store, resolver func(int64) (storage.Driver, error), publicURL string) *Uploaders {
	return &Uploaders{ops: newAIOps(store, resolver, nil, publicURL)}
}

// AttachACL wires the permission resolver (folder rights, share.links, the
// link ceiling of the person's rules).
func (h *Uploaders) AttachACL(r *acl.Resolver) { h.ops.acl = r }

// AttachTenants wires the shared origin resolver: the endpoint the setup
// names is the address the person reached filex at.
func (h *Uploaders) AttachTenants(rv tenanturl.Resolver) { h.ops.tenants = rv }

// uploaderNameMax is the longest name, in characters.
const uploaderNameMax = 100

// uploaderView is one uploader as the surface answers it: the row, plus the
// storage's CURRENT name (the row holds its id) - "" when it is gone.
type uploaderView struct {
	*model.Uploader
	Storage string `json:"storage"`
	// Available is false when the storage behind the id is gone, or no
	// longer in the tenant the uploader was made in: its uploads are
	// refused until the person points it at another folder.
	Available bool `json:"available"`
}

// uploaderSetup is everything a client needs, built ONCE on the server from
// the key it was just given (the key is not kept, so this exists only in the
// answer that made it): the endpoint, a ShareX custom uploader file and a
// curl line. The web, desktop and embedded panels download or copy these as
// they come - none of them builds a config of its own.
type uploaderSetup struct {
	Endpoint string `json:"endpoint"`
	// ShareX is the `.sxcu` file's content; ShareXFile its suggested name.
	ShareX     sharexConfig `json:"sharex"`
	ShareXFile string       `json:"sharex_file"`
	// Curl is one line that uploads `<file>`; replace it with a path.
	Curl string `json:"curl"`
}

// sharexConfig is a ShareX custom uploader (.sxcu). ONE file for images,
// text and files: ShareX lists it under all three destinations. Fields in the
// order ShareX writes them. The response fields it reads are the door's own
// (`{json:url}`, …): the server already said everything in its answer.
type sharexConfig struct {
	Version         string            `json:"Version"`
	Name            string            `json:"Name"`
	DestinationType string            `json:"DestinationType"`
	RequestMethod   string            `json:"RequestMethod"`
	RequestURL      string            `json:"RequestURL"`
	Headers         map[string]string `json:"Headers"`
	Body            string            `json:"Body"`
	FileFormName    string            `json:"FileFormName"`
	URL             string            `json:"URL"`
	ThumbnailURL    string            `json:"ThumbnailURL"`
	DeletionURL     string            `json:"DeletionURL"`
	ErrorMessage    string            `json:"ErrorMessage"`
}

// uploadsEndpoint is the door's address on origin.
func uploadsEndpoint(origin string) string {
	return strings.TrimRight(origin, "/") + "/api/uploads"
}

// buildUploaderSetup is the setup for key on endpoint.
func buildUploaderSetup(endpoint, name, key string) uploaderSetup {
	host := endpoint
	if u, err := url.Parse(endpoint); err == nil && u.Host != "" {
		host = u.Host
	}
	label := name + " (" + host + ")"
	return uploaderSetup{
		Endpoint: endpoint,
		ShareX: sharexConfig{
			Version:         "17.0.0",
			Name:            label,
			DestinationType: "ImageUploader, TextUploader, FileUploader",
			RequestMethod:   "POST",
			RequestURL:      endpoint,
			Headers:         map[string]string{"Authorization": "Bearer " + key},
			Body:            "MultipartFormData",
			FileFormName:    "file",
			URL:             "{json:url}",
			ThumbnailURL:    "{json:thumbnail_url}",
			DeletionURL:     "{json:deletion_url}",
			ErrorMessage:    "{json:error}",
		},
		ShareXFile: setupFileName(name) + ".sxcu",
		Curl:       `curl -H "Authorization: Bearer ` + key + `" -F "file=@<file>" ` + endpoint,
	}
}

// setupFileName is a name safe for a downloaded file: letters, digits, dash
// and underscore of the uploader's name, "filex-uploader" when none is left.
func setupFileName(name string) string {
	var b strings.Builder
	for _, r := range strings.TrimSpace(name) {
		switch {
		case r == ' ' || r == '-' || r == '_':
			b.WriteByte('-')
		case r < utf8.RuneSelf && (r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9'):
			b.WriteRune(r)
		case r >= utf8.RuneSelf:
			b.WriteRune(r)
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		return "filex-uploader"
	}
	return out
}

// view reads an uploader the way the surface answers it.
func (h *Uploaders) view(r *http.Request, u *model.Uploader) uploaderView {
	v := uploaderView{Uploader: u}
	ctx := r.Context()
	st, err := h.ops.store.GetStorage(ctx, u.StorageID)
	if err != nil || st == nil {
		return v
	}
	v.Storage = st.Name
	v.Available = db.StampStands(ctx, h.ops.store, u.TenantID, u.StorageID)
	return v
}

// List serves GET /api/auth/uploaders.
func (h *Uploaders) List(w http.ResponseWriter, r *http.Request) {
	u := auth.UserFrom(r.Context())
	if u == nil {
		writeError(w, r, http.StatusUnauthorized, "unauthorized", nil)
		return
	}
	rows, err := h.ops.store.ListUploaders(r.Context(), u.ID)
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "internal_error", nil)
		return
	}
	out := make([]uploaderView, 0, len(rows))
	for _, row := range rows {
		out = append(out, h.view(r, row))
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"uploaders": out,
		"endpoint":  uploadsEndpoint(h.ops.tenants.FromRequest(r)),
		// The longest a public link may live for this person (the install's
		// share.max_ttl_days or their rules', whichever is shorter), 0 for no
		// ceiling: what the form offers, so it never offers what the server
		// would cut.
		"link_max_days":     linkCeilingDays(r.Context(), h.ops.store, h.ops.acl),
		"may_link_publicly": h.ops.acl == nil || callerCan(r.Context(), h.ops.acl, perm.ShareLinks),
		"may_create":        h.ops.acl == nil || callerCan(r.Context(), h.ops.acl, perm.AccessAPI),
		"default_folder":    uploadDefaultFolder,
		// The storages the person may point an uploader at: the ones they can
		// see (a confined key's own one alone) - the form's picker.
		"storages": h.ops.RootInfo(r.Context()).Storages,
		// An app holding the link-shortener role, whose permission the person
		// holds (0.60): the form then offers "answer with a short link".
		"short_links_available": h.ShortLinks != nil && h.ShortLinks(r),
	})
}

// uploaderRequest is the body of POST and PATCH. On PATCH every field is
// optional and a missing one keeps what the uploader has.
type uploaderRequest struct {
	Name       *string `json:"name"`
	Storage    *string `json:"storage"`
	Folder     *string `json:"folder"`
	Visibility *string `json:"visibility"`
	LinkDays   *int    `json:"link_days"`
	// ShortLink: answer with the link's short address (0.60).
	ShortLink *bool `json:"short_link"`
}

// settle validates req over base (nil for a new uploader) and answers the
// uploader it describes, with the folder made and the tenant stamp taken -
// or writes the refusal and answers nil. clamped reports a link life cut to
// the ceiling.
func (h *Uploaders) settle(w http.ResponseWriter, r *http.Request, req uploaderRequest, base *model.Uploader) (*model.Uploader, bool) {
	ctx := r.Context()
	out := &model.Uploader{Visibility: model.UploaderPublic, Folder: uploadDefaultFolder}
	storageName := ""
	if base != nil {
		cp := *base
		out = &cp
		if st, err := h.ops.store.GetStorage(ctx, base.StorageID); err == nil && st != nil {
			storageName = st.Name
		}
	}
	if req.Name != nil {
		out.Name = strings.TrimSpace(*req.Name)
	}
	switch {
	case out.Name == "":
		writeError(w, r, http.StatusBadRequest, "uploader_name_required", nil)
		return nil, false
	case utf8.RuneCountInString(out.Name) > uploaderNameMax:
		writeError(w, r, http.StatusBadRequest, "uploader_name_too_long", apierr.Params{"max": strconv.Itoa(uploaderNameMax)})
		return nil, false
	}
	if req.Visibility != nil {
		switch v := strings.TrimSpace(*req.Visibility); v {
		case model.UploaderPublic, model.UploaderPrivate:
			out.Visibility = v
		default:
			writeError(w, r, http.StatusBadRequest, "uploader_visibility_invalid", nil)
			return nil, false
		}
	}
	if req.LinkDays != nil {
		out.LinkDays = *req.LinkDays
	}
	if req.ShortLink != nil {
		out.ShortLink = *req.ShortLink
	}
	if out.LinkDays < 0 || out.LinkDays > share.MaxTTLDaysLimit {
		writeError(w, r, http.StatusBadRequest, "uploader_link_days_invalid", apierr.Params{"max": strconv.Itoa(share.MaxTTLDaysLimit)})
		return nil, false
	}
	if req.Storage != nil {
		storageName = strings.TrimSpace(*req.Storage)
	}
	if req.Folder != nil {
		out.Folder = cleanUploadFolder(*req.Folder)
	}
	if storageName == "" {
		writeError(w, r, http.StatusBadRequest, "uploader_storage_required", nil)
		return nil, false
	}

	// The folder, as the person is now: a storage they can see, a folder
	// they may add files to - made now, so a refusal is said here and the
	// folder is in their drive before the first upload.
	st, _, err := h.ops.resolveStorage(ctx, joinAdapterPath(storageName, out.Folder))
	if err != nil {
		writeUploaderRefusal(w, r, err)
		return nil, false
	}
	if st.ReadOnly {
		writeReadOnly(w, r, http.StatusForbidden)
		return nil, false
	}
	out.StorageID = st.ID
	core := uploadCore{ops: h.ops}
	if out.Folder != "" {
		if err := core.ensureFolder(ctx, st, out.Folder); err != nil {
			writeUploaderRefusal(w, r, err)
			return nil, false
		}
	}
	probe := path.Join(out.Folder, "upload")
	if err := h.ops.can(ctx, st, probe, perm.FilesCreate); err != nil {
		writeUploaderRefusal(w, r, err)
		return nil, false
	}
	if out.Visibility == model.UploaderPublic {
		if err := publicLinkRefusal(ctx, h.ops.acl, h.ops.store, st.ID, probe, perm.ShareLinks); err != nil {
			if errors.Is(err, errE2EEncrypted) {
				writeError(w, r, http.StatusConflict, "uploader_public_encrypted", nil)
				return nil, false
			}
			writeError(w, r, http.StatusForbidden, "uploader_public_denied", nil)
			return nil, false
		}
	}
	stamp, err := db.StorageStamp(ctx, h.ops.store, st.ID)
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "internal_error", nil)
		return nil, false
	}
	out.TenantID = stamp
	clamped := false
	if ceiling := linkCeilingDays(ctx, h.ops.store, h.ops.acl); ceiling > 0 && out.LinkDays > ceiling {
		out.LinkDays, clamped = ceiling, true
	}
	return out, clamped
}

// writeUploaderRefusal answers a folder the person may not use: the aiOps
// refusal's own sentence under its status.
func writeUploaderRefusal(w http.ResponseWriter, r *http.Request, err error) {
	var ue *entryUnavailableError
	if errors.As(err, &ue) {
		writeJSON(w, http.StatusConflict, ue.body(""))
		return
	}
	writeJSON(w, aiStatus(err), aiErrorBody(err))
}

// Create serves POST /api/auth/uploaders.
func (h *Uploaders) Create(w http.ResponseWriter, r *http.Request) {
	u := auth.UserFrom(r.Context())
	if u == nil {
		writeError(w, r, http.StatusUnauthorized, "unauthorized", nil)
		return
	}
	// A key may make a credential no wider than itself: `write`, and the
	// folder inside its `root:` (resolveStorage asks that in settle).
	if !auth.AllowVerb(w, r, auth.VerbWrite) {
		return
	}
	var req uploaderRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, r, http.StatusBadRequest, "bad_json", nil)
		return
	}
	up, clamped := h.settle(w, r, req, nil)
	if up == nil {
		return
	}
	key, err := newUploaderKey()
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "internal_error", nil)
		return
	}
	up.UserID = u.ID
	up.KeyHash = hashSecret(key)
	created, err := h.ops.store.CreateUploader(r.Context(), up)
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "internal_error", nil)
		return
	}
	auth.SetAuditTarget(r.Context(), strconv.FormatInt(created.ID, 10), created.Name)
	writeJSON(w, http.StatusCreated, map[string]any{
		"uploader":          h.view(r, created),
		"key":               key,
		"setup":             buildUploaderSetup(uploadsEndpoint(h.ops.tenants.FromRequest(r)), created.Name, key),
		"link_days_clamped": clamped,
	})
}

// own reads the caller's uploader {id}, or answers 404.
func (h *Uploaders) own(w http.ResponseWriter, r *http.Request) (*model.User, *model.Uploader) {
	u := auth.UserFrom(r.Context())
	if u == nil {
		writeError(w, r, http.StatusUnauthorized, "unauthorized", nil)
		return nil, nil
	}
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, r, http.StatusNotFound, "uploader_not_found", nil)
		return nil, nil
	}
	up, err := h.ops.store.GetUploader(r.Context(), id)
	if err != nil || up == nil || up.UserID != u.ID {
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			writeError(w, r, http.StatusInternalServerError, "internal_error", nil)
			return nil, nil
		}
		// Somebody else's uploader is answered exactly like one that does
		// not exist.
		writeError(w, r, http.StatusNotFound, "uploader_not_found", nil)
		return nil, nil
	}
	return u, up
}

// Update serves PATCH /api/auth/uploaders/{id}.
func (h *Uploaders) Update(w http.ResponseWriter, r *http.Request) {
	if !auth.AllowVerb(w, r, auth.VerbWrite) {
		return
	}
	_, up := h.own(w, r)
	if up == nil {
		return
	}
	var req uploaderRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, r, http.StatusBadRequest, "bad_json", nil)
		return
	}
	next, clamped := h.settle(w, r, req, up)
	if next == nil {
		return
	}
	ok, err := h.ops.store.UpdateUploader(r.Context(), next)
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "internal_error", nil)
		return
	}
	if !ok {
		writeError(w, r, http.StatusNotFound, "uploader_not_found", nil)
		return
	}
	saved, err := h.ops.store.GetUploader(r.Context(), up.ID)
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "internal_error", nil)
		return
	}
	auth.SetAuditTarget(r.Context(), strconv.FormatInt(saved.ID, 10), saved.Name)
	writeJSON(w, http.StatusOK, map[string]any{"uploader": h.view(r, saved), "link_days_clamped": clamped})
}

// RotateKey serves POST /api/auth/uploaders/{id}/key: a new key, the old one
// dead the moment this answers.
func (h *Uploaders) RotateKey(w http.ResponseWriter, r *http.Request) {
	if !auth.AllowVerb(w, r, auth.VerbWrite) {
		return
	}
	u, up := h.own(w, r)
	if up == nil {
		return
	}
	key, err := newUploaderKey()
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "internal_error", nil)
		return
	}
	ok, err := h.ops.store.SetUploaderKey(r.Context(), up.ID, u.ID, hashSecret(key))
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "internal_error", nil)
		return
	}
	if !ok {
		writeError(w, r, http.StatusNotFound, "uploader_not_found", nil)
		return
	}
	saved, err := h.ops.store.GetUploader(r.Context(), up.ID)
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "internal_error", nil)
		return
	}
	auth.SetAuditTarget(r.Context(), strconv.FormatInt(saved.ID, 10), saved.Name)
	writeJSON(w, http.StatusOK, map[string]any{
		"uploader": h.view(r, saved),
		"key":      key,
		"setup":    buildUploaderSetup(uploadsEndpoint(h.ops.tenants.FromRequest(r)), saved.Name, key),
	})
}

// Delete serves DELETE /api/auth/uploaders/{id}: the uploader and its key.
// What it uploaded stays, and so do the links and deletion links it answered
// with.
func (h *Uploaders) Delete(w http.ResponseWriter, r *http.Request) {
	u, up := h.own(w, r)
	if up == nil {
		return
	}
	ok, err := h.ops.store.DeleteUploader(r.Context(), up.ID, u.ID)
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "internal_error", nil)
		return
	}
	if !ok {
		writeError(w, r, http.StatusNotFound, "uploader_not_found", nil)
		return
	}
	auth.SetAuditTarget(r.Context(), strconv.FormatInt(up.ID, 10), up.Name)
	w.WriteHeader(http.StatusNoContent)
}
