// Package handlers - panel_search.go
//
// The admin panel's search (task #168, docs/ADMIN-PANEL.md → Search): the
// people, groups, API keys, apps, storages and shares that match a query, in
// one answer, and the person's recent searches.
//
// ⚠⚠ Nothing is listed again here. Each kind is read by running, in process,
// the very handler the panel's own page calls (the Users page's
// GET /api/admin/users, the API / MCP page's GET /api/admin/ai-tokens, …)
// behind the very gate its route has (handlers.RequireAdminPermission for
// the delegated pages, auth.RequireAdmin for the rest) - routes.go registers
// each source next to the route it reads, with the same gate value. So who
// may find what is, by construction, who may open what: a delegated
// administrator holding admin.users finds people and groups and nothing
// else, a tenant's administrator finds their own tenant's (the handlers'
// tenant filters and the tenant-scoped store), and a kind whose list
// refuses the reader is left out of the answer - never an error. The pattern
// is the AI surface's doors (ai_doors.go).
//
// What this file adds is only the matching, and the shape of a row: an id, a
// label, a detail line, the other words a row answers to. A row never carries
// what the list it came from must not hand on - an API key's value is not in
// that list to begin with (only its hash is stored), a share's link token and
// address are dropped here, and nothing else is copied but the named fields.
//
// Pages, settings and files are not here: the panel finds its own pages and
// settings from its menu (web/src/lib/adminSearch.ts), and files through the
// explorer's own search (GET /api/files/search).
package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"path"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"
	"golang.org/x/text/unicode/norm"

	"github.com/brf-tech/filex/backend/internal/acl"
	"github.com/brf-tech/filex/backend/internal/auth"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/namefold"
	"github.com/brf-tech/filex/backend/internal/perm"
	"github.com/brf-tech/filex/backend/pkg/pluginkit/wire"
)

// Panel search kinds, as the client's prefixes name them (core
// lib/panelSearch PANEL_SEARCH_PREFIXES; `share` has no prefix).
const (
	panelKindUser    = "user"
	panelKindGroup   = "group"
	panelKindKey     = "key"
	panelKindApp     = "app"
	panelKindStorage = "storage"
	panelKindShare   = "share"
)

// panelDefaultLimit and panelMaxLimit bound the rows of one kind.
const (
	panelDefaultLimit = 8
	panelMaxLimit     = 50
)

// panelShareScan is how many of the newest links a share search reads (the
// Shares page's own largest page, sharePaging).
const panelShareScan = 500

// panelPerms are the delegated admin permissions (internal/perm): holding
// any one of them opens the admin panel, and so its search. admin.full is
// the role itself, which CallerMayAdminister already answers.
var panelPerms = []perm.Perm{perm.AdminUsers, perm.AdminGrants, perm.AdminShares, perm.AdminAudit, perm.AdminMonitor}

// panelHit is one row of the answer.
type panelHit struct {
	Kind   string `json:"kind"`
	ID     string `json:"id"`
	Label  string `json:"label"`
	Detail string `json:"detail,omitempty"`
	// Labels is an app's (or an app action's) label in every language it
	// gave, so the client shows the interface's and matches every one.
	Labels map[string]string `json:"labels,omitempty"`
	// App is the app a row belongs to: its page's address (`plugins.app`).
	App string `json:"app,omitempty"`
	// Terms are other words the row answers to and does not show.
	Terms []string `json:"terms,omitempty"`
}

// panelSource is one kind: the door it is read through and how its answer
// becomes rows. `then` is a second door whose rows join this kind's, asked
// only when the first one answered (an app's actions, behind the apps list).
type panelSource struct {
	kind  string
	door  http.Handler
	query url.Values
	rows  func(body []byte, lang string) ([]panelHit, error)
	then  *panelSource
}

// PanelSearch is the admin panel's search. Sources are added by routes.go,
// each next to the route it reads; the zero set answers no rows.
type PanelSearch struct {
	Store   db.Store
	ACL     *acl.Resolver
	sources []panelSource
}

// NewPanelSearch builds the handler. Sources are added with the Add* methods.
func NewPanelSearch(store db.Store, resolver *acl.Resolver) *PanelSearch {
	return &PanelSearch{Store: store, ACL: resolver}
}

// RequirePanel admits whoever the admin panel opens for: an administrator
// (a session, or an API key that may administer - auth.CallerMayAdminister),
// or a signed-in session holding at least one delegated admin.* permission.
// Everybody else is refused, a plain account and a delegated administrator's
// API key alike (the delegated area is a session's, RequireAdminPermission).
func (h *PanelSearch) RequirePanel(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		if auth.CallerMayAdminister(ctx) {
			next.ServeHTTP(w, r)
			return
		}
		if auth.UserFrom(ctx) == nil {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
			return
		}
		if auth.TokenFrom(ctx) != nil || h.ACL == nil {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "forbidden"})
			return
		}
		for _, p := range panelPerms {
			if callerCan(ctx, h.ACL, p) {
				next.ServeHTTP(w, r)
				return
			}
		}
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "forbidden"})
	})
}

// ── sources ─────────────────────────────────────────────────────────────────

// AddUsers reads the Users page's list (GET /api/admin/users).
func (h *PanelSearch) AddUsers(door http.Handler) {
	h.sources = append(h.sources, panelSource{kind: panelKindUser, door: door, rows: panelUsers})
}

// AddGroups reads the Groups page's list (GET /api/admin/groups).
func (h *PanelSearch) AddGroups(door http.Handler) {
	h.sources = append(h.sources, panelSource{kind: panelKindGroup, door: door, rows: panelGroups})
}

// AddShares reads the Shares page's list (GET /api/admin/shares), the newest
// panelShareScan links.
func (h *PanelSearch) AddShares(door http.Handler) {
	h.sources = append(h.sources, panelSource{
		kind: panelKindShare, door: door, rows: panelShares,
		query: url.Values{"limit": {strconv.Itoa(panelShareScan)}},
	})
}

// AddKeys reads the API / MCP page's list (GET /api/admin/ai-tokens).
func (h *PanelSearch) AddKeys(door http.Handler) {
	h.sources = append(h.sources, panelSource{kind: panelKindKey, door: door, rows: panelKeys})
}

// AddApps reads the installed apps (GET /api/admin/app-plugins) and, when
// that list answered, their actions (GET /api/files/plugins/actions).
func (h *PanelSearch) AddApps(list, actions http.Handler) {
	src := panelSource{kind: panelKindApp, door: list, rows: panelApps}
	if actions != nil {
		src.then = &panelSource{kind: panelKindApp, door: actions, rows: panelAppActions}
	}
	h.sources = append(h.sources, src)
}

// AddStorages reads the Storages page's list (GET /api/admin/storages)
// without its per-storage counts (`stats=none`).
func (h *PanelSearch) AddStorages(door http.Handler) {
	h.sources = append(h.sources, panelSource{
		kind: panelKindStorage, door: door, rows: panelStorages,
		query: url.Values{"stats": {"none"}},
	})
}

// ── GET /api/admin/panel-search?q=…&kinds=…&limit=… ─────────────────────────

// Search answers {"results": [...], "searched": [...]}: the rows that match
// every word of q, at most `limit` per kind, and the kinds that were read -
// a kind the reader may not open is not in `searched`.
func (h *PanelSearch) Search(w http.ResponseWriter, r *http.Request) {
	qs := r.URL.Query()
	q := strings.TrimSpace(qs.Get("q"))
	if utf8.RuneCountInString(q) > model.RecentSearchMaxLen {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "query_too_long"})
		return
	}
	limit := panelDefaultLimit
	if v, err := strconv.Atoi(qs.Get("limit")); err == nil && v > 0 {
		limit = min(v, panelMaxLimit)
	}
	want := map[string]bool{}
	for _, k := range strings.Split(qs.Get("kinds"), ",") {
		if k = strings.TrimSpace(k); k != "" {
			want[k] = true
		}
	}
	results := []panelHit{}
	searched := []string{}
	words := panelWords(q)
	if len(words) == 0 {
		writeJSON(w, http.StatusOK, map[string]any{"results": results, "searched": searched})
		return
	}
	lang := langOf(r)
	for _, src := range h.sources {
		if len(want) > 0 && !want[src.kind] {
			continue
		}
		rows, ok := h.read(r, src, lang)
		if !ok {
			continue
		}
		searched = append(searched, src.kind)
		results = append(results, panelMatch(rows, words, limit)...)
	}
	writeJSON(w, http.StatusOK, map[string]any{"results": results, "searched": searched})
}

// read runs a source's door and turns its answer into rows; ok is false when
// the door refused (the reader may not open that list) or answered nothing
// readable.
func (h *PanelSearch) read(r *http.Request, src panelSource, lang string) ([]panelHit, bool) {
	status, body := runPanelDoor(r.Context(), src.door, r, src.query)
	if status != http.StatusOK {
		return nil, false
	}
	rows, err := src.rows(body, lang)
	if err != nil {
		return nil, false
	}
	if src.then != nil {
		if more, ok := h.read(r, *src.then, lang); ok {
			rows = append(rows, more...)
		}
	}
	return rows, true
}

// runPanelDoor runs a list handler in process as a GET with this request's
// identity, tenant and headers - the context the admin group's middleware
// already resolved - and its own query.
func runPanelDoor(ctx context.Context, door http.Handler, r *http.Request, query url.Values) (int, []byte) {
	req := r.Clone(ctx)
	req.Method = http.MethodGet
	req.Body = http.NoBody
	req.ContentLength = 0
	u := *r.URL
	u.RawQuery = query.Encode()
	req.URL = &u
	rec := newBufRecorder()
	door.ServeHTTP(rec, req)
	return rec.status, rec.buf.Bytes()
}

// ── matching ────────────────────────────────────────────────────────────────

// panelFold is how a typed word and a row are compared: accents and the
// Turkish letters folded to their base letter (ş → s, ğ → g, ü → u, ö → o,
// ç → c), the four i's one letter and case ignored (namefold.Rune). The same
// rule as the client's (core lib/fileFilters foldText), so the server never
// leaves out a row the client would have found. Unlike a file search
// (internal/namefold) accents do not count here: a person types a page's or
// a colleague's name on whatever keyboard they have.
func panelFold(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range norm.NFD.String(s) {
		if unicode.Is(unicode.Mn, r) {
			continue
		}
		b.WriteRune(namefold.Rune(r))
	}
	return b.String()
}

// panelWords is q folded and split into words.
func panelWords(q string) []string {
	return strings.Fields(panelFold(q))
}

// panelMatch keeps the rows every word is in (the label, the detail or a
// term), best first: a word starting the label counts most, one inside it
// less, one found elsewhere least. At most limit rows.
func panelMatch(rows []panelHit, words []string, limit int) []panelHit {
	type scored struct {
		hit   panelHit
		score int
	}
	keep := []scored{}
	for _, h := range rows {
		label := panelFold(h.Label)
		parts := []string{label, panelFold(h.Detail)}
		for _, t := range h.Terms {
			parts = append(parts, panelFold(t))
		}
		for _, t := range h.Labels {
			parts = append(parts, panelFold(t))
		}
		// One string per field, joined by a line break, so a word never
		// matches across the end of one field and the start of the next.
		hay := strings.Join(parts, "\n")
		score := 0
		for _, w := range words {
			switch {
			case strings.HasPrefix(label, w):
				score += 3
			case strings.Contains(label, w):
				score += 2
			case strings.Contains(hay, w):
				score++
			default:
				score = -1
			}
			if score < 0 {
				break
			}
		}
		if score > 0 {
			keep = append(keep, scored{h, score})
		}
	}
	sort.SliceStable(keep, func(i, j int) bool { return keep[i].score > keep[j].score })
	if len(keep) > limit {
		keep = keep[:limit]
	}
	out := make([]panelHit, len(keep))
	for i := range keep {
		out[i] = keep[i].hit
	}
	return out
}

// ── what each list answers, as rows ─────────────────────────────────────────

func panelUsers(body []byte, _ string) ([]panelHit, error) {
	var users []struct {
		ID          int64  `json:"id"`
		Email       string `json:"email"`
		Username    string `json:"username"`
		DisplayName string `json:"display_name"`
	}
	if err := json.Unmarshal(body, &users); err != nil {
		return nil, err
	}
	out := make([]panelHit, 0, len(users))
	for _, u := range users {
		label := strings.TrimSpace(u.DisplayName)
		if label == "" {
			label = u.Email
		}
		out = append(out, panelHit{
			Kind: panelKindUser, ID: strconv.FormatInt(u.ID, 10),
			Label: label, Detail: u.Email, Terms: nonEmpty(u.Username),
		})
	}
	return out, nil
}

func panelGroups(body []byte, _ string) ([]panelHit, error) {
	var ans struct {
		Groups []struct {
			ID            int64  `json:"id"`
			Name          string `json:"name"`
			Description   string `json:"description"`
			DirectoryName string `json:"directory_name"`
		} `json:"groups"`
	}
	if err := json.Unmarshal(body, &ans); err != nil {
		return nil, err
	}
	out := make([]panelHit, 0, len(ans.Groups))
	for _, g := range ans.Groups {
		out = append(out, panelHit{
			Kind: panelKindGroup, ID: strconv.FormatInt(g.ID, 10),
			Label: g.Name, Detail: g.Description, Terms: nonEmpty(g.DirectoryName),
		})
	}
	return out, nil
}

// panelKeys: an API key by its name and the identities it acts under. Its
// value is never in the list (only a hash is stored, and the hash is not
// sent either - model.APIToken `json:"-"`).
func panelKeys(body []byte, _ string) ([]panelHit, error) {
	var ans struct {
		Tokens []struct {
			ID        int64  `json:"id"`
			Label     string `json:"label"`
			Usernames string `json:"usernames"`
		} `json:"tokens"`
	}
	if err := json.Unmarshal(body, &ans); err != nil {
		return nil, err
	}
	out := make([]panelHit, 0, len(ans.Tokens))
	for _, t := range ans.Tokens {
		out = append(out, panelHit{
			Kind: panelKindKey, ID: strconv.FormatInt(t.ID, 10),
			Label: t.Label, Detail: strings.ReplaceAll(t.Usernames, ",", ", "),
		})
	}
	return out, nil
}

// panelShares: a link by the file or folder it shares, where it is and who
// made it. ⚠ The link's token and address are NOT copied: a share's token is
// the link itself, and a search row is no place to hand it out.
func panelShares(body []byte, _ string) ([]panelHit, error) {
	var ans struct {
		Items []struct {
			Share *struct {
				ID int64 `json:"id"`
			} `json:"share"`
			NodePath    string `json:"node_path"`
			StorageName string `json:"storage_name"`
			CreatorName string `json:"creator_name"`
			PluginName  string `json:"plugin_name"`
		} `json:"items"`
	}
	if err := json.Unmarshal(body, &ans); err != nil {
		return nil, err
	}
	out := make([]panelHit, 0, len(ans.Items))
	for _, it := range ans.Items {
		if it.Share == nil {
			continue
		}
		where := strings.TrimPrefix(it.NodePath, "/")
		label := path.Base("/" + where)
		if label == "/" || label == "." {
			label = it.StorageName
		}
		detail := strings.Trim(it.StorageName+"/"+where, "/")
		out = append(out, panelHit{
			Kind: panelKindShare, ID: strconv.FormatInt(it.Share.ID, 10),
			Label: label, Detail: detail, Terms: nonEmpty(it.CreatorName, it.PluginName),
		})
	}
	return out, nil
}

// panelApps: an installed app by its label in every language it gave and its
// name; the detail is its description in the reader's language.
func panelApps(body []byte, lang string) ([]panelHit, error) {
	var ans struct {
		Plugins []struct {
			Name        string    `json:"name"`
			Label       wire.Text `json:"label"`
			Description wire.Text `json:"description"`
		} `json:"plugins"`
	}
	if err := json.Unmarshal(body, &ans); err != nil {
		return nil, err
	}
	out := make([]panelHit, 0, len(ans.Plugins))
	for _, p := range ans.Plugins {
		label := p.Label.Get(lang)
		if label == "" {
			label = p.Name
		}
		out = append(out, panelHit{
			Kind: panelKindApp, ID: p.Name, App: p.Name,
			Label: label, Labels: map[string]string(p.Label), Detail: p.Description.Get(lang),
			Terms: nonEmpty(p.Name),
		})
	}
	return out, nil
}

// panelAppActions: what an app does from the file menu ("Convert to PDF"),
// found under its app.
func panelAppActions(body []byte, lang string) ([]panelHit, error) {
	var ans struct {
		Actions []struct {
			Plugin string    `json:"plugin"`
			ID     string    `json:"id"`
			Label  wire.Text `json:"label"`
		} `json:"actions"`
	}
	if err := json.Unmarshal(body, &ans); err != nil {
		return nil, err
	}
	out := make([]panelHit, 0, len(ans.Actions))
	for _, a := range ans.Actions {
		label := a.Label.Get(lang)
		if label == "" {
			label = a.ID
		}
		out = append(out, panelHit{
			Kind: panelKindApp, ID: a.Plugin + "/" + a.ID, App: a.Plugin,
			Label: label, Labels: map[string]string(a.Label), Detail: a.Plugin,
		})
	}
	return out, nil
}

func panelStorages(body []byte, _ string) ([]panelHit, error) {
	var storages []struct {
		ID     int64  `json:"id"`
		Name   string `json:"name"`
		Driver string `json:"driver"`
	}
	if err := json.Unmarshal(body, &storages); err != nil {
		return nil, err
	}
	out := make([]panelHit, 0, len(storages))
	for _, s := range storages {
		out = append(out, panelHit{
			Kind: panelKindStorage, ID: strconv.FormatInt(s.ID, 10),
			Label: s.Name, Detail: s.Driver,
		})
	}
	return out, nil
}

// nonEmpty is the given words without the empty ones; nil when none is left.
func nonEmpty(words ...string) []string {
	var out []string
	for _, w := range words {
		if w = strings.TrimSpace(w); w != "" {
			out = append(out, w)
		}
	}
	return out
}

// ── recent searches (migration 00090) ───────────────────────────────────────
//
// The person's OWN list, whoever they are: every call is about the caller's
// rows and no id of anybody else's is ever reachable (the store answers
// "not found" for one, exactly as for an id that does not exist).

// panelCaller is the signed-in person a recent-searches call is about; it
// answers 401 itself when there is none.
func panelCaller(w http.ResponseWriter, r *http.Request) (*model.User, bool) {
	u := auth.UserFrom(r.Context())
	if u == nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return nil, false
	}
	return u, true
}

// panelFailed answers a store failure.
func panelFailed(w http.ResponseWriter, err error) {
	writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
}

// Recent answers {"searches": [...]}, newest first.
func (h *PanelSearch) Recent(w http.ResponseWriter, r *http.Request) {
	u, ok := panelCaller(w, r)
	if !ok {
		return
	}
	rows, err := h.Store.ListRecentSearches(r.Context(), u.ID, model.RecentSearchSurfaceAdmin, model.RecentSearchKeep)
	if err != nil {
		panelFailed(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"searches": rows})
}

// Remember records a search ({"query": "…"}): it moves to the top, and the
// list keeps the newest model.RecentSearchKeep. Answers 201 with the row.
func (h *PanelSearch) Remember(w http.ResponseWriter, r *http.Request) {
	u, ok := panelCaller(w, r)
	if !ok {
		return
	}
	var req struct {
		Query string `json:"query"`
	}
	if !decodeSmall(w, r, &req) {
		return
	}
	q := strings.Join(strings.Fields(req.Query), " ")
	if q == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "query_required"})
		return
	}
	if utf8.RuneCountInString(q) > model.RecentSearchMaxLen {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "query_too_long"})
		return
	}
	row, err := h.Store.AddRecentSearch(r.Context(), u.ID, model.RecentSearchSurfaceAdmin, q, model.RecentSearchKeep)
	if err != nil {
		panelFailed(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, row)
}

// Forget removes one of the caller's own recent searches; 404 for an id that
// is not theirs.
func (h *PanelSearch) Forget(w http.ResponseWriter, r *http.Request) {
	u, ok := panelCaller(w, r)
	if !ok {
		return
	}
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || id <= 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad id"})
		return
	}
	found, err := h.Store.DeleteRecentSearch(r.Context(), u.ID, id)
	if err != nil {
		panelFailed(w, err)
		return
	}
	if !found {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not_found"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// Clear empties the caller's recent searches.
func (h *PanelSearch) Clear(w http.ResponseWriter, r *http.Request) {
	u, ok := panelCaller(w, r)
	if !ok {
		return
	}
	if err := h.Store.ClearRecentSearches(r.Context(), u.ID, model.RecentSearchSurfaceAdmin); err != nil {
		panelFailed(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}
