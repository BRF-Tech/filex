// Package handlers - openwith.go
//
// Endpoints (0.50, docs/APP-PLUGINS.md → Default apps):
//
//	GET    /api/me/open-with         (auth) → {"choices": {ext: handler}}
//	PUT    /api/me/open-with/{ext}   (auth) ← {"handler": "builtin" | "onlyoffice" | "app:<app>/<view>"} → {"choices": …}
//	DELETE /api/me/open-with/{ext}   (auth) → {"choices": …}
//	DELETE /api/me/open-with         (auth) → {"choices": {}}
//
// "Always open this kind with this app" - ONE record per ACCOUNT (the maintainer,
// 2026-10-01): the browser, the desktop app and every embed read and write the
// same choices. A person who picked an app for .board files in the browser
// gets it in the desktop app too; where that app cannot open the file (it is
// not offered there, or the administrator switched it off for the kind) the
// next handler that is on opens it, and the choice is kept for where it can.
//
// ⚠⚠ NOT a key of the per-surface document. That document is PUT whole, so a
// shared key in it would be overwritten by whichever surface wrote last with
// whatever it read at boot - a choice made on the desktop undone by a palette
// change in a browser tab opened before it. So the choices are their own row
// (user_prefs, surface "account", `{"openWith": {…}}`), changed one kind at a
// time, and the surface document only CARRIES them on the way out:
// GET /api/me/prefs puts them in as `openWith` (the JSON string the client has
// always read), PUT /api/me/prefs drops the key (userprefs.go).
//
// ⚠ Adoption, once: an account whose row has never held a choice takes the
// `openWith` of its most recently written surface document (web or desktop),
// the one that was in use last. Choices are not merged across documents:
// which device's choice for a kind is "right" is not knowable, and the last
// one written is the one the person saw last.
package handlers

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/brf-tech/filex/backend/internal/assoc"
	"github.com/brf-tech/filex/backend/internal/auth"
	"github.com/brf-tech/filex/backend/internal/db"
)

// SurfaceAccount is the user_prefs row that holds what belongs to the account
// rather than to one surface. It is not a surface a client may name in
// /api/me/prefs (prefsSurface refuses it): it is written one kind at a time,
// here.
const SurfaceAccount = "account"

// openWithKey is the choices' key, in the account row and in the surface
// document a client reads.
const openWithKey = "openWith"

// maxOpenWithChoices bounds one account's choices: a kind per extension a
// person meets is dozens, not thousands.
const maxOpenWithChoices = 512

// OpenWith serves the account's "open with" choices.
type OpenWith struct {
	Store db.Store
}

// NewOpenWith constructs the handler.
func NewOpenWith(store db.Store) *OpenWith { return &OpenWith{Store: store} }

// validOpenChoice reports whether id is a handler the open capability knows
// for the kind ext: filex's own viewer, an app's interface
// (`app:<app>/<view>`), or - for a kind the document server opens as a choice
// (assoc.OnlyOfficeOpens: `.csv`, filex 0.51) - ONLYOFFICE (`onlyoffice`).
//
// ⚠ The shape only, never "is it there now": a choice of ONLYOFFICE is kept
// when the administrator switches OnlyOffice off, and the kind opens in the
// next handler that is on until it is back (lib/appViewer).
func validOpenChoice(ext, id string) bool {
	if id == assoc.Builtin {
		return true
	}
	if id == assoc.OnlyOffice {
		return assoc.OnlyOfficeOpens(ext)
	}
	return strings.Contains(id, "/") && assoc.ValidID(assoc.CapOpen, id)
}

// cleanChoices keeps the well-formed entries of a choices map.
func cleanChoices(in map[string]string) map[string]string {
	out := map[string]string{}
	for k, v := range in {
		if assoc.ValidExt(k) && validOpenChoice(k, v) {
			out[k] = v
		}
	}
	return out
}

// choicesFromDoc reads the choices out of a surface document as a client
// wrote it before 0.50's account row: `openWith` is a JSON STRING holding an
// object. ok is false when the document has none.
func choicesFromDoc(doc string) (map[string]string, bool) {
	var top map[string]json.RawMessage
	if json.Unmarshal([]byte(doc), &top) != nil {
		return nil, false
	}
	raw, ok := top[openWithKey]
	if !ok {
		return nil, false
	}
	var s string
	if json.Unmarshal(raw, &s) != nil || strings.TrimSpace(s) == "" {
		return nil, false
	}
	var m map[string]string
	if json.Unmarshal([]byte(s), &m) != nil {
		return nil, false
	}
	m = cleanChoices(m)
	return m, len(m) > 0
}

// accountDoc reads the account row as a map of its keys.
func accountDoc(ctx context.Context, store db.Store, userID int64) (map[string]json.RawMessage, error) {
	doc, err := store.GetUserPrefs(ctx, userID, SurfaceAccount)
	if err != nil {
		return nil, err
	}
	top := map[string]json.RawMessage{}
	if strings.TrimSpace(doc) != "" {
		_ = json.Unmarshal([]byte(doc), &top)
		if top == nil {
			top = map[string]json.RawMessage{}
		}
	}
	return top, nil
}

// loadOpenWith answers the account's choices, adopting them once from the
// most recently written surface document when the account row never held
// any (see the header).
func loadOpenWith(ctx context.Context, store db.Store, userID int64) (map[string]string, error) {
	top, err := accountDoc(ctx, store, userID)
	if err != nil {
		return nil, err
	}
	if raw, ok := top[openWithKey]; ok {
		var m map[string]string
		_ = json.Unmarshal(raw, &m)
		return cleanChoices(m), nil
	}
	docs, err := store.ListUserPrefs(ctx, userID)
	if err != nil {
		return nil, err
	}
	// Newest first (ListUserPrefs): the first surface document with a choice
	// is the one that was written last.
	for _, d := range docs {
		if d.Surface != SurfaceWeb && d.Surface != SurfaceDesktop {
			continue
		}
		if m, ok := choicesFromDoc(d.Doc); ok {
			if err := saveOpenWith(ctx, store, userID, m); err != nil {
				return nil, err
			}
			return m, nil
		}
	}
	return map[string]string{}, nil
}

// saveOpenWith writes the choices into the account row, keeping its other
// keys.
func saveOpenWith(ctx context.Context, store db.Store, userID int64, m map[string]string) error {
	top, err := accountDoc(ctx, store, userID)
	if err != nil {
		return err
	}
	b, _ := json.Marshal(cleanChoices(m))
	top[openWithKey] = b
	out, _ := json.Marshal(top)
	return store.SetUserPrefs(ctx, userID, SurfaceAccount, string(out))
}

// withOpenWith is a surface document as a client reads it: its own keys, and
// `openWith` set to the account's choices (the JSON string the client has
// always read) - or removed when there are none, so a stale copy a surface
// document still carries from before 0.50 is never what the client sees.
func withOpenWith(doc string, m map[string]string) string {
	var top map[string]json.RawMessage
	if json.Unmarshal([]byte(doc), &top) != nil || top == nil {
		top = map[string]json.RawMessage{}
	}
	_, had := top[openWithKey]
	if len(m) == 0 {
		if !had {
			return doc
		}
		delete(top, openWithKey)
	} else {
		inner, _ := json.Marshal(m)
		s, _ := json.Marshal(string(inner))
		top[openWithKey] = s
	}
	out, _ := json.Marshal(top)
	return string(out)
}

// withoutOpenWith is a surface document as it is stored: never with the
// account's choices in it (see the header).
func withoutOpenWith(doc string) string {
	var top map[string]json.RawMessage
	if json.Unmarshal([]byte(doc), &top) != nil || top == nil {
		return doc
	}
	if _, ok := top[openWithKey]; !ok {
		return doc
	}
	delete(top, openWithKey)
	out, _ := json.Marshal(top)
	return string(out)
}

func (h *OpenWith) answer(w http.ResponseWriter, m map[string]string) {
	if m == nil {
		m = map[string]string{}
	}
	w.Header().Set("Cache-Control", "private, no-store")
	writeJSON(w, http.StatusOK, map[string]any{"choices": m})
}

// Get answers the caller's choices.
func (h *OpenWith) Get(w http.ResponseWriter, r *http.Request) {
	u := auth.UserFrom(r.Context())
	if u == nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthenticated"})
		return
	}
	m, err := loadOpenWith(r.Context(), h.Store, u.ID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	h.answer(w, m)
}

// kindOf reads and validates the {ext} of the path.
func kindOf(w http.ResponseWriter, r *http.Request) (string, bool) {
	ext := strings.ToLower(strings.TrimPrefix(strings.TrimSpace(chi.URLParam(r, "ext")), "."))
	if !assoc.ValidExt(ext) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad_kind", "message": "a kind is a file extension: lower-case letters and digits, no dot"})
		return "", false
	}
	return ext, true
}

// Put keeps one choice: the handler that opens the kind for this person.
//
// ⚠ Not checked against what is installed or on now: a choice is kept when
// its app is switched off and used again when it is back (lib/appViewer
// decides what opens a file, from what is on for the surface it runs on).
func (h *OpenWith) Put(w http.ResponseWriter, r *http.Request) {
	u := auth.UserFrom(r.Context())
	if u == nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthenticated"})
		return
	}
	ext, ok := kindOf(w, r)
	if !ok {
		return
	}
	var body struct {
		Handler string `json:"handler"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 4<<10)).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad json"})
		return
	}
	if !validOpenChoice(ext, body.Handler) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad_handler", "message": "a choice is builtin, app:<app>/<view>, or onlyoffice for a kind ONLYOFFICE opens (" + strings.Join(assoc.OnlyOfficeOpenKinds(), ", ") + ")"})
		return
	}
	m, err := loadOpenWith(r.Context(), h.Store, u.ID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if _, had := m[ext]; !had && len(m) >= maxOpenWithChoices {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "too_many_choices", "message": "forget a choice first"})
		return
	}
	m[ext] = body.Handler
	if err := saveOpenWith(r.Context(), h.Store, u.ID, m); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	h.answer(w, m)
}

// Forget drops one choice: the kind opens with what the administrator put
// first again.
func (h *OpenWith) Forget(w http.ResponseWriter, r *http.Request) {
	u := auth.UserFrom(r.Context())
	if u == nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthenticated"})
		return
	}
	ext, ok := kindOf(w, r)
	if !ok {
		return
	}
	m, err := loadOpenWith(r.Context(), h.Store, u.ID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	delete(m, ext)
	if err := saveOpenWith(r.Context(), h.Store, u.ID, m); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	h.answer(w, m)
}

// Clear drops every choice. The account row keeps an empty set, so nothing
// is adopted again from a surface document that still carries an old copy.
func (h *OpenWith) Clear(w http.ResponseWriter, r *http.Request) {
	u := auth.UserFrom(r.Context())
	if u == nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthenticated"})
		return
	}
	if err := saveOpenWith(r.Context(), h.Store, u.ID, map[string]string{}); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	h.answer(w, map[string]string{})
}
