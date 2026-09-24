// Package handlers — viewprefs.go
//
// Endpoints:
//
//	GET /api/files/manager/view-prefs   (auth)  → {"prefs": <object>, "instance_default": <object|null>}
//	PUT /api/files/manager/view-prefs   (auth)  ← {"prefs": <object>}
//
// How each person left each folder — view mode, sort key and direction, plus
// their column widths, order and visibility. One JSON document per user,
// stored against the user row by migration 00039.
//
// ⚠⚠ WHY THIS IS SERVER-SIDE at all, when theme, density and the start page
// are not. `localStorage` is per BROWSER, not per person: on a shared machine
// the next account to sign in inherits the previous one's folder arrangements,
// silently and with no error. The owner ruled that out explicitly
// ("her user kendi görünümünü görür"), and a server-side document also follows
// a person to the desktop app and to their other machines, which is what that
// sentence really asks for.
package handlers

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/brf-tech/filex/backend/internal/auth"
	"github.com/brf-tech/filex/backend/internal/db"
)

// ViewPrefs wires the per-user view-preference routes.
type ViewPrefs struct {
	Store db.Store
}

// NewViewPrefs constructs the handler.
func NewViewPrefs(store db.Store) *ViewPrefs { return &ViewPrefs{Store: store} }

// maxViewPrefsBytes caps what one account may store.
//
// The client keeps at most 300 folders and evicts the least-recently-used
// (packages/core/src/lib/viewPrefs.ts explains the 300), which lands around
// 33 KB. 128 KB is that with room for longer paths than the estimate assumed
// and for fields added later — and it is still small enough that a client
// with a bug, or somebody with curl, cannot turn a preference into a way to
// park megabytes in the database. ⚠ The cap is enforced HERE and not only in
// the client: a limit the client applies is a limit the client can skip.
const maxViewPrefsBytes = 128 << 10

type viewPrefsBody struct {
	// json.RawMessage, not a typed struct: the shape is the CLIENT's — which
	// columns exist, what a folder entry carries — and it changes when the UI
	// changes. Parsing it here would mean this file had to be edited in
	// lockstep with a Vue component, and a field it did not know about would
	// be dropped on the way through. It is validated as JSON and bounded in
	// size; it is not interpreted.
	Prefs json.RawMessage `json:"prefs"`
}

// Get returns the caller's document. A caller with no stored document gets an
// empty object rather than a 404: "nothing arranged yet" is the ordinary state
// of every new account, and a 404 would make the client treat its own first
// day as a failure.
func (h *ViewPrefs) Get(w http.ResponseWriter, r *http.Request) {
	u := auth.UserFrom(r.Context())
	if u == nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthenticated"})
		return
	}
	doc, err := h.Store.GetUserViewPrefs(r.Context(), u.ID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if doc == "" {
		doc = "{}"
	}
	// The OPERATOR's default folder view rides beside the person's document —
	// what a folder nobody has changed opens as when the person has not set a
	// default of their own (packages/core/src/lib/viewPrefs.ts).
	//
	// ⚠⚠ Beside it, never inside it. The client saves the document it read
	// back to this same route, so an instance default folded INTO `prefs`
	// would be written back as the person's own choice on their next column
	// drag, and from then on the operator could never change it for them —
	// the exact defect the instance palette shipped with this release.
	inst := "null"
	if v, err := h.Store.GetSetting(r.Context(), DefaultFolderViewSettingKey); err == nil && strings.TrimSpace(v) != "" {
		if norm, err := normaliseFolderViewDefault(v); err == nil && norm != "" {
			inst = norm
		}
	}
	w.Header().Set("Content-Type", "application/json")
	// ⚠ Written straight through rather than unmarshalled and re-marshalled:
	// the document is the client's own shape and round-tripping it through a
	// map would reorder its keys on every read for no reason.
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"prefs":` + doc + `,"instance_default":` + inst + `}`))
}

// DefaultFolderViewSettingKey is the operator's default folder view: what a
// folder opens as for a person who has neither changed that folder nor set a
// default of their own. Resolution, per field: the folder's own memory → the
// person's default → THIS → filex's built-in (list, name ↑).
//
// ⚠ Instance-wide, so `allowSettingWrite` refuses it to a tenant admin for the
// same reason it refuses `ui.default_theme`: it is not `branding.*`, so it has
// no per-tenant home, and a tenant admin writing it would set the view every
// other tenant's people open their folders in.
const DefaultFolderViewSettingKey = "ui.default_folder_view"

// folderViewDefault is the value's shape. Short keys, the same the client's
// per-folder memory uses: view mode, sort key, sort direction, hidden columns.
type folderViewDefault struct {
	V      string   `json:"v,omitempty"`
	K      string   `json:"k,omitempty"`
	D      string   `json:"d,omitempty"`
	Hidden []string `json:"hidden,omitempty"`
}

// normaliseFolderViewDefault validates the setting and returns it re-encoded
// with only the fields that mean something, or "" for an empty value.
//
// ⚠ Validated on WRITE (settings.go) and again on READ: a value stored before
// this check existed, or edited in the database by hand, must not reach every
// person's explorer as a view mode the client cannot draw. The client
// re-checks it too (`readInstanceFolderDefault`); three places, because this
// value is applied to people who never chose it.
func normaliseFolderViewDefault(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "null" || raw == "{}" {
		return "", nil
	}
	var in folderViewDefault
	if err := json.Unmarshal([]byte(raw), &in); err != nil {
		return "", fmt.Errorf("default folder view must be a JSON object: %w", err)
	}
	var out folderViewDefault
	switch in.V {
	case "", "list", "grid", "gallery":
		out.V = in.V
	default:
		return "", fmt.Errorf("view %q is not list, grid or gallery", in.V)
	}
	switch in.K {
	case "", "name", "type", "modified", "size":
		out.K = in.K
	default:
		return "", fmt.Errorf("sort %q is not name, type, modified or size", in.K)
	}
	switch in.D {
	case "":
	case "asc", "desc":
		if out.K != "" {
			out.D = in.D
		}
	default:
		return "", fmt.Errorf("direction %q is not asc or desc", in.D)
	}
	seen := map[string]bool{}
	for _, c := range in.Hidden {
		switch c {
		case "type", "location", "owner", "modified", "size":
			if !seen[c] {
				out.Hidden = append(out.Hidden, c)
				seen[c] = true
			}
		default:
			return "", fmt.Errorf("column %q cannot be hidden by default", c)
		}
	}
	if out.V == "" && out.K == "" && len(out.Hidden) == 0 {
		return "", nil
	}
	b, _ := json.Marshal(out)
	return string(b), nil
}

// Put replaces the caller's document.
func (h *ViewPrefs) Put(w http.ResponseWriter, r *http.Request) {
	u := auth.UserFrom(r.Context())
	if u == nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthenticated"})
		return
	}
	var body viewPrefsBody
	// ⚠ The body is bounded BEFORE it is decoded. Without the limit, a
	// multi-gigabyte PUT is read into memory in full before anything gets to
	// judge its size, and the cap below would be checked on a server that had
	// already paid the whole cost.
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxViewPrefsBytes+1<<10)).Decode(&body); err != nil {
		// ⚠ The size case is named separately. MaxBytesReader trips the decoder
		// first, so an over-large document would otherwise be reported as
		// "bad json" — sending whoever is debugging it to look for a syntax
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
	if len(doc) > maxViewPrefsBytes {
		writeJSON(w, http.StatusRequestEntityTooLarge, map[string]string{"error": "prefs too large"})
		return
	}
	// It must be a JSON OBJECT. `json.RawMessage` accepts any valid JSON value,
	// so without this a client could store `"hello"` or `[1,2,3]` and every
	// later reader would have to defend against it.
	if !json.Valid(body.Prefs) || doc[0] != '{' {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "prefs must be an object"})
		return
	}
	if err := h.Store.SetUserViewPrefs(r.Context(), u.ID, doc); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}
