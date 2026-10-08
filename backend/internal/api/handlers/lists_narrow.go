// Package handlers — lists_narrow.go
//
// The pieces every list and search answer shares since filex 0.54 (task
// #207): a search's narrowing bound to the request (D6), the `starred` flag on
// each row (D4), the one order rows come back in (Y3) and the name matcher the
// explorer's "Filter in this folder" box asks (D5).
package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/brf-tech/filex/backend/internal/auth"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/listorder"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/namefold"
	"github.com/brf-tech/filex/backend/internal/nodefilter"
	"github.com/brf-tech/filex/backend/internal/search"
)

// narrowing is one search's nodefilter.Criteria bound to the request: who
// is asking (for owner=me), the store (to read a hit's row once) and the
// storage names (for an adapter-qualified under / not_under).
type narrowing struct {
	crit     nodefilter.Criteria
	viewer   int64
	ctx      context.Context
	store    db.Store
	nodes    map[int64]*model.Node
	storages map[int64]string
}

// newNarrowing binds crit to the request. A nil result is never returned:
// an inactive narrowing accepts everything and costs nothing.
func newNarrowing(ctx context.Context, store db.Store, crit nodefilter.Criteria) *narrowing {
	var viewer int64
	if u := auth.UserFrom(ctx); u != nil {
		viewer = u.ID
	}
	return &narrowing{crit: crit, viewer: viewer, ctx: ctx, store: store,
		nodes: map[int64]*model.Node{}, storages: map[int64]string{}}
}

func (nw *narrowing) active() bool { return nw != nil && nw.crit.Active() }

// node is the catalogue row of id, read once per request (nil when gone).
func (nw *narrowing) node(id int64) *model.Node {
	if n, ok := nw.nodes[id]; ok {
		return n
	}
	n, err := nw.store.GetNode(nw.ctx, id)
	if err != nil {
		n = nil
	}
	nw.nodes[id] = n
	return n
}

func (nw *narrowing) storageName(id int64) string {
	if name, ok := nw.storages[id]; ok {
		return name
	}
	name := ""
	if st, err := nw.store.GetStorage(nw.ctx, id); err == nil && st != nil {
		name = st.Name
	}
	nw.storages[id] = name
	return name
}

// accepts applies the narrowing to a row in hand.
func (nw *narrowing) accepts(n *model.Node) bool {
	if !nw.active() {
		return true
	}
	if n == nil {
		return false
	}
	storage := ""
	if nw.crit.UnderStorage != "" || nw.crit.NotUnderStorage != "" {
		storage = nw.storageName(n.StorageID)
	}
	return nw.crit.AcceptIn(n, nw.viewer, storage)
}

// into returns f carrying the narrowing (a copy; f itself is not changed),
// or f as it is when there is nothing to narrow. The index asks Accept about
// every candidate before it counts it against the limit.
func (nw *narrowing) into(f *search.Filter) *search.Filter {
	if !nw.active() {
		return f
	}
	out := &search.Filter{}
	if f != nil {
		*out = *f
	}
	out.Accept = func(id int64) bool { return nw.accepts(nw.node(id)) }
	out.AcceptNode = nw.accepts
	return out
}

// narrowedFallbackWindow is how many candidate rows the index-less search
// reads while a narrowing is on: the narrowing turns rows away after the
// database chose them, so the window has to be wider for `limit` to count
// rows that satisfy the whole question.
const narrowedFallbackWindow = 4000

// narrowedWindow is the fallback window to read: window, or the wider
// narrowedFallbackWindow while nw narrows anything.
func narrowedWindow(nw *narrowing, window int) int {
	if nw.active() && window < narrowedFallbackWindow {
		return narrowedFallbackWindow
	}
	return window
}

// writeBadFilter answers a narrowing parameter the server cannot read: 400,
// naming the parameter, rather than a quietly wider answer.
func writeBadFilter(w http.ResponseWriter, err error) {
	writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad_filter", "field": nodefilter.Field(err)})
}

// ── starred (D4) ────────────────────────────────────────────────────────────

// starredAmong answers, in ONE query, which of ids the caller has starred.
// Empty without a signed-in caller (an app token starring for nobody).
func starredAmong(ctx context.Context, store db.Store, ids []int64) map[int64]bool {
	out := map[int64]bool{}
	u := auth.UserFrom(ctx)
	if u == nil || len(ids) == 0 {
		return out
	}
	at, err := store.UserNodeMetaAt(ctx, u.ID, userMetaKeyStarred, ids)
	if err != nil {
		return out
	}
	for id := range at {
		out[id] = true
	}
	return out
}

// annotateStarred puts `starred: true` on the listing rows the caller has
// starred. The key is omitted otherwise, like every other flag on a row.
func annotateStarred(ctx context.Context, store db.Store, files []map[string]any) {
	ids := make([]int64, 0, len(files))
	for _, e := range files {
		if id, ok := e["id"].(int64); ok && id > 0 {
			ids = append(ids, id)
		}
	}
	starred := starredAmong(ctx, store, ids)
	if len(starred) == 0 {
		return
	}
	for _, e := range files {
		if id, ok := e["id"].(int64); ok && starred[id] {
			e["starred"] = true
		}
	}
}

// ── order (Y3) ──────────────────────────────────────────────────────────────

// listOrderFrom reads `?sort=`; false (after writing a 400) when the key is
// one the server does not know.
func listOrderFrom(w http.ResponseWriter, r *http.Request, fallback listorder.Order) (listorder.Order, bool) {
	o, ok := listorder.Parse(r.URL.Query().Get("sort"), fallback)
	if !ok {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad_sort", "field": "sort"})
		return o, false
	}
	return o, true
}

// ── the admin lists' search box (D5) ────────────────────────────────────────

// labelMatches is the admin lists' `?q=` (Users, Groups): every word of q,
// folded by namefold.Loose (accents, case and the four i's - a colleague's
// name typed on whatever keyboard), inside one of the fields. The rule the
// panel search uses (panelFold); before 0.54 the two pages filtered in the
// browser with the browser's own locale rules.
func labelMatches(q string, fields ...string) bool {
	words := strings.Fields(namefold.Loose(q))
	if len(words) == 0 {
		return true
	}
	hay := namefold.Loose(strings.Join(fields, " | "))
	for _, w := range words {
		if !strings.Contains(hay, w) {
			return false
		}
	}
	return true
}

// ── the name matcher (D5) ───────────────────────────────────────────────────

// matchNamesMax bounds one request: a folder of this many entries is far past
// what a person scrolls, and the body stays small.
const matchNamesMax = 20000

type matchNamesReq struct {
	Query string   `json:"q"`
	Names []string `json:"names"`
}

// MatchNames answers the explorer's "Filter in this folder" box:
//
//	POST /api/files/search/match   {q, names: [...]}   -> {matches: [index, ...]}
//
// It tells which of the given names answer q by the search's own rule
// (search.NameMatcher) - the one matcher, on the server. It reads no storage
// and returns nothing the caller did not send: the names are the rows the
// explorer already holds.
func (h *Search) MatchNames(w http.ResponseWriter, r *http.Request) {
	var req matchNamesReq
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<20)).Decode(&req); err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			writeJSON(w, http.StatusRequestEntityTooLarge, map[string]string{"error": "too_many_names"})
			return
		}
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad json"})
		return
	}
	if len(req.Names) > matchNamesMax {
		writeJSON(w, http.StatusRequestEntityTooLarge, map[string]string{"error": "too_many_names"})
		return
	}
	m := search.NewNameMatcher(strings.TrimSpace(req.Query))
	matches := make([]int, 0, len(req.Names))
	for i, name := range req.Names {
		if m.Match(name) {
			matches = append(matches, i)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"matches": matches})
}
