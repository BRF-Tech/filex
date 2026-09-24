// Package handlers — userprefs.go
//
// Endpoints:
//
//	GET /api/me/prefs?surface=web   (auth)  → {"surface": "...", "prefs": <object>}
//	PUT /api/me/prefs?surface=web   (auth)  ← {"prefs": <object>}
//
// What a person chose about the interface itself — theme, palette, density,
// language. One JSON document per person PER SURFACE (`web` | `desktop`),
// stored by migration 00047.
//
// ⚠⚠ WHY THE SERVER. The owner picked a theme in one browser and the other
// browser did not have it. `localStorage` is per BROWSER, never per person: a
// second machine, a private window, a cleared site and the desktop app are
// each a fresh empty set of choices, with nothing to notice and no error.
//
// ⚠ NOT a replacement for the view prefs (00039, viewprefs.go). Those are how
// each FOLDER was left and are written on every navigation; these are the
// surface's own appearance and are read once at sign-in. Two tables because
// they change at completely different rates and one of them is per folder.
//
// ⚠ A caller only ever reaches THEIR OWN row. There is no user id in the path
// or the body, and the only identity this file consults is the session's, so
// there is nothing for a client to substitute.
package handlers

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/brf-tech/filex/backend/internal/auth"
	"github.com/brf-tech/filex/backend/internal/db"
)

// UserPrefs wires the per-user, per-surface preference routes.
type UserPrefs struct {
	Store db.Store
}

// NewUserPrefs constructs the handler.
func NewUserPrefs(store db.Store) *UserPrefs { return &UserPrefs{Store: store} }

// Surfaces a preference document may belong to.
const (
	// SurfaceWeb is the browser.
	SurfaceWeb = "web"
	// SurfaceDesktop is the desktop application.
	SurfaceDesktop = "desktop"
)

// maxUserPrefsBytes caps what one account may store per surface.
//
// A theme, a palette, a density, a language and room for what a later release
// adds is a few hundred bytes. 64 KiB is that with three orders of magnitude
// of headroom and still small enough that a client with a bug — or somebody
// with curl — cannot turn an appearance setting into a way to park megabytes
// in the database. ⚠ Enforced HERE and not only in the client: a limit the
// client applies is a limit the client can skip.
const maxUserPrefsBytes = 64 << 10

// prefsSurface reads and validates the surface from the query.
//
// ⚠ An unknown surface is REFUSED rather than quietly folded into "web". A
// typo that silently wrote somebody's desktop theme into their browser row
// would look exactly like the bug this table exists to fix.
func prefsSurface(r *http.Request) (string, bool) {
	s := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("surface")))
	if s == "" {
		s = SurfaceWeb
	}
	return s, s == SurfaceWeb || s == SurfaceDesktop
}

type userPrefsBody struct {
	// json.RawMessage, not a typed struct: the shape is the CLIENT's — which
	// appearance knobs exist — and it changes when the interface changes.
	// Parsing it here would mean this file had to be edited in lockstep with a
	// settings screen, and a field it did not know about would be dropped on
	// the way through. It is validated as JSON and bounded in size; it is not
	// interpreted.
	Prefs json.RawMessage `json:"prefs"`
}

// Get returns the caller's document for one surface. A caller with nothing
// stored gets an empty object rather than a 404: "nothing chosen yet" is the
// ordinary state of every new account, and a 404 would make the client treat
// its own first day as a failure.
func (h *UserPrefs) Get(w http.ResponseWriter, r *http.Request) {
	u := auth.UserFrom(r.Context())
	if u == nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthenticated"})
		return
	}
	surface, ok := prefsSurface(r)
	if !ok {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad surface", "hint": "surface must be web or desktop"})
		return
	}
	doc, err := h.Store.GetUserPrefs(r.Context(), u.ID, surface)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if strings.TrimSpace(doc) == "" {
		doc = "{}"
	}
	w.Header().Set("Content-Type", "application/json")
	// ⚠ Private: this is one person's appearance, on a route every signed-in
	// browser hits at boot. A shared cache holding it would hand the next
	// person somebody else's choices.
	w.Header().Set("Cache-Control", "private, no-store")
	w.WriteHeader(http.StatusOK)
	// Written straight through rather than unmarshalled and re-marshalled:
	// the document is the client's own shape and round-tripping it through a
	// map would reorder its keys on every read for no reason.
	_, _ = w.Write([]byte(`{"surface":"` + surface + `","prefs":` + doc + `}`))
}

// Put replaces the caller's document for one surface.
func (h *UserPrefs) Put(w http.ResponseWriter, r *http.Request) {
	u := auth.UserFrom(r.Context())
	if u == nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthenticated"})
		return
	}
	surface, ok := prefsSurface(r)
	if !ok {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad surface", "hint": "surface must be web or desktop"})
		return
	}
	var body userPrefsBody
	// ⚠ The body is bounded BEFORE it is decoded. Without the limit a
	// multi-gigabyte PUT is read into memory in full before anything gets to
	// judge its size, and the cap below would be checked on a server that had
	// already paid the whole cost.
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxUserPrefsBytes+1<<10)).Decode(&body); err != nil {
		// ⚠ The size case is named separately. MaxBytesReader trips the
		// decoder first, so an over-large document would otherwise be reported
		// as "bad json" — sending whoever is debugging it to look for a syntax
		// error in a document that is perfectly well-formed and simply too big.
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			writeJSON(w, http.StatusRequestEntityTooLarge, map[string]string{"error": "prefs too large"})
			return
		}
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad json"})
		return
	}
	doc := string(body.Prefs)
	if doc == "" || doc == "null" {
		doc = "{}"
	}
	if len(doc) > maxUserPrefsBytes {
		writeJSON(w, http.StatusRequestEntityTooLarge, map[string]string{"error": "prefs too large"})
		return
	}
	// It must be a JSON OBJECT. json.RawMessage accepts any valid JSON value,
	// so without this a client could store `"hello"` or `[1,2,3]` and every
	// later reader would have to defend against it.
	if !json.Valid([]byte(doc)) || doc[0] != '{' {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "prefs must be an object"})
		return
	}
	if err := h.Store.SetUserPrefs(r.Context(), u.ID, surface, doc); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	h.mirrorLocale(r, u.ID, u.Locale, u.Timezone, doc)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "surface": surface})
}

// mirrorLocale keeps the ACCOUNT's language in step with the language the
// person just chose in the interface.
//
// ⚠⚠ A person has ONE language, and until this call there were two places
// recording it: `users.locale`, which every server-rendered string reads
// (`langOf` — an app plugin's screens, the label on a queued job, mail,
// notifications), and this document, which the language switcher writes. They
// drifted the moment somebody used the switcher: the window turned Turkish
// and the signing wizard kept answering in English, inside the same page.
//
// The document stays per surface, because a dense list on a laptop and a
// roomy one on a wall screen is a real want. A LANGUAGE is not that: the last
// one chosen, on whichever surface, is the one the person reads in.
//
// A failure here is not worth refusing the write over — the preference is
// saved, and the account column catches up on the next change.
func (h *UserPrefs) mirrorLocale(r *http.Request, id int64, was, tz, doc string) {
	var got struct {
		Locale string `json:"locale"`
	}
	if err := json.Unmarshal([]byte(doc), &got); err != nil {
		return
	}
	lang := strings.ToLower(strings.TrimSpace(got.Locale))
	if lang == "" || lang == strings.ToLower(was) {
		return
	}
	_ = h.Store.UpdateUserLocale(r.Context(), id, lang, tz)
}
