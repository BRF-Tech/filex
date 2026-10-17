// Package handlers — search_admin.go
//
// Admin endpoints for the embedded Bleve full-text index.
//
//	GET   /api/admin/search/stats     — index stats
//	POST  /api/admin/search/rebuild   — drop and rebuild the index from nodes
//	GET   /api/admin/search/settings  — the search settings (task #261)
//	PATCH /api/admin/search/settings  — {content?, content_max_mb?,
//	                                     memory_percent?, auto_rebuild?}
package handlers

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/dbsetting"
	"github.com/brf-tech/filex/backend/internal/search"
	"github.com/brf-tech/filex/backend/internal/srvtext"
	"github.com/brf-tech/filex/backend/internal/sysmem"
)

// SearchAdmin holds the Bleve admin actions.
//
// There is deliberately no rebuild lock here. The index owns that guard,
// because filex also rebuilds on its own when it finds an index written by
// an older document schema, and a handler-local flag could only ever see
// the rebuilds this handler started — the two would happily run at once
// and race to swap their replacement index in.
type SearchAdmin struct {
	Index *search.Index
	Store db.Store
}

// NewSearchAdmin constructs the handler.
func NewSearchAdmin(idx *search.Index, store db.Store) *SearchAdmin {
	return &SearchAdmin{Index: idx, Store: store}
}

// Stats returns index document counts and size.
func (h *SearchAdmin) Stats(w http.ResponseWriter, r *http.Request) {
	if !requireSupertenant(w, r, searchIsInstanceWide) {
		return
	}
	if h.Index == nil {
		writeJSON(w, http.StatusOK, map[string]any{
			"enabled":          false,
			"document_count":   0,
			"index_size_bytes": 0,
		})
		return
	}
	stats := h.Index.Stats()
	writeJSON(w, http.StatusOK, map[string]any{
		"enabled":          true,
		"document_count":   stats.DocCount,
		"file_count":       stats.Files,
		"folder_count":     stats.Folders,
		"index_size_bytes": stats.SizeBytes,
		"last_updated_at":  stats.LastUpdated,
		// last_built_at is the name the admin page reads (it always asked
		// for this one; nothing sent it, so "Last built" said "—").
		"last_built_at": stats.LastUpdated,
		// needs_rebuild is how an operator finds out that an upgrade
		// added indexed fields their documents do not have yet. Search
		// keeps working without it (the pre-upgrade sub-queries are
		// still part of every query), so this is an invitation, not an
		// alarm — the repair usually runs on its own (see
		// FILEX_SEARCH_AUTO_REBUILD) and this is what says whether it
		// has finished.
		"needs_rebuild": stats.NeedsRebuild,
		// rebuilding lets the admin UI say "rebuilding" instead of
		// showing a needs_rebuild banner over an index that is already
		// being repaired, and looking broken while it happens.
		"rebuilding": stats.Rebuilding,
		// How the latest rebuild ended: `rebuilding` turns false on a
		// failure too, so the page reads these before it says the new index
		// is live. Absent/empty before any rebuild of this process ended.
		"last_rebuild_finished_at": stats.LastRebuildAt,
		"last_rebuild_error":       stats.LastRebuildError,
	})
}

// Rebuild reindexes every node row into a replacement index and swaps it
// in when it is complete. Search keeps answering from the existing index
// for the whole rebuild, and extracted text is carried across, so this is
// no longer the destructive operation it was before v0.30.
//
// ?content=1 additionally re-enqueues content extraction for every
// eligible node once the new index is live — for an operator who has just
// added an extractor or raised FILEX_SEARCH_CONTENT_MAX and wants the text
// derived again rather than copied.
func (h *SearchAdmin) Rebuild(w http.ResponseWriter, r *http.Request) {
	if !requireSupertenant(w, r, searchIsInstanceWide) {
		return
	}
	if h.Index == nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "search index disabled"})
		return
	}
	withContent := false
	if v := r.URL.Query().Get("content"); v == "1" || strings.EqualFold(v, "true") {
		withContent = true
	}
	// StartRebuild detaches from r.Context — chi cancels it the moment we
	// return from this handler, which would kill the background reindex
	// before it processed a single row — and refuses if a rebuild (this
	// endpoint's, or the automatic schema repair) is already running.
	err := h.Index.StartRebuild(h.Store, search.RebuildOptions{ReExtract: withContent, Reason: "admin"})
	switch {
	case errors.Is(err, search.ErrRebuildInProgress):
		writeJSON(w, http.StatusConflict, map[string]string{"error": "rebuild already in progress"})
		return
	case err != nil:
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	note := "rebuild started in background"
	if withContent {
		note = "rebuild started in background (content extraction re-enqueued)"
	}
	writeJSON(w, http.StatusAccepted, map[string]any{
		"ok":   true,
		"note": note,
	})
}

// searchIsInstanceWide is the refusal an admin of a tenant reads: there is one
// index and one set of search settings for the whole instance.
const searchIsInstanceWide = "the search index is one instance-wide index"

// searchSettingsResponse is what both verbs of /api/admin/search/settings
// answer. The four settings are settings-table rows (internal/search
// settings.go): db, then the variable on a first boot, then the default.
//
// The bounds travel with the values, so the page offers exactly the range
// the PATCH takes. The memory_* figures are what the percent came to in THIS
// process (Index.ApplyMemoryPercent): the share is of the memory filex was
// given, which only the running server can read.
type searchSettingsResponse struct {
	// Content: a file's text is extracted into the index when it is written.
	// Live: the next write is (or is not) extracted.
	Content bool `json:"content"`
	// ContentMaxMB: larger source files are not extracted. Live.
	ContentMaxMB    int `json:"content_max_mb"`
	ContentMaxMBMin int `json:"content_max_mb_min"`
	ContentMaxMBMax int `json:"content_max_mb_max"`
	// MemoryPercent: the share of the given memory indexing may take. Applied
	// the moment it is saved.
	MemoryPercent    int `json:"memory_percent"`
	MemoryPercentMin int `json:"memory_percent_min"`
	MemoryPercentMax int `json:"memory_percent_max"`
	// MemoryBudgetBytes is that share in bytes, MemoryGivenBytes the memory
	// it is a share of and MemoryFrom where filex read it (a container's
	// limit, or the machine's RAM). 0 / "" when the index is not open or the
	// figure could not be read.
	MemoryBudgetBytes uint64 `json:"memory_budget_bytes"`
	MemoryGivenBytes  uint64 `json:"memory_given_bytes"`
	MemoryFrom        string `json:"memory_from"`
	// MemoryFromKind is MemoryFrom as a word the page translates:
	// "container" (a cgroup limit), "host" (the machine's RAM) or "" (not
	// read). MemoryFrom stays the log's exact wording, for an operator.
	MemoryFromKind string `json:"memory_from_kind"`
	// AutoRebuild: repair an index written by an older document schema at
	// startup. Read at startup, so a change applies at the next start.
	AutoRebuild bool `json:"auto_rebuild"`
	// IndexEnabled is whether this process opened the index at all
	// (FILEX_SEARCH_ENABLED, environment only). Read-only.
	IndexEnabled bool `json:"index_enabled"`
}

// searchSettingsPatch is the PATCH body: pointers, so "absent" is not "false"
// or 0.
type searchSettingsPatch struct {
	Content       *bool `json:"content"`
	ContentMaxMB  *int  `json:"content_max_mb"`
	MemoryPercent *int  `json:"memory_percent"`
	AutoRebuild   *bool `json:"auto_rebuild"`
}

// Settings answers GET /api/admin/search/settings.
func (h *SearchAdmin) Settings(w http.ResponseWriter, r *http.Request) {
	if !requireSupertenant(w, r, searchIsInstanceWide) {
		return
	}
	writeJSON(w, http.StatusOK, h.settingsSnapshot(r))
}

// UpdateSettings answers PATCH /api/admin/search/settings: validates every
// number against its setting's bounds (refused, never clamped - the person is
// looking at the form), stores what was sent, applies a new memory percent at
// once, and answers the fresh GET shape.
func (h *SearchAdmin) UpdateSettings(w http.ResponseWriter, r *http.Request) {
	if !requireSupertenant(w, r, searchIsInstanceWide) {
		return
	}
	var req searchSettingsPatch
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		// A number box sent empty or with a fraction lands here.
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error":   "bad json",
			"message": srvtext.Text(requestLang(r), "server.protection.whole_number", nil),
		})
		return
	}
	if req.Content == nil && req.ContentMaxMB == nil && req.MemoryPercent == nil && req.AutoRebuild == nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "no fields to update"})
		return
	}
	for _, n := range []struct {
		field string
		v     *int
		spec  dbsetting.IntSpec
	}{
		{"content_max_mb", req.ContentMaxMB, search.ContentMaxSetting},
		{"memory_percent", req.MemoryPercent, search.MemoryPercentSetting},
	} {
		if n.v == nil {
			continue
		}
		if err := n.spec.Validate(*n.v); err != nil {
			writeProtectionOutOfRange(w, r, n.field, err.Error(), n.spec.Min, n.spec.Max)
			return
		}
	}
	ctx := r.Context()
	writes := map[string]string{}
	if v := req.Content; v != nil {
		writes[search.ContentSetting.Key] = dbsetting.FormatBool(*v)
	}
	if v := req.ContentMaxMB; v != nil {
		writes[search.ContentMaxSetting.Key] = strconv.Itoa(*v)
	}
	if v := req.MemoryPercent; v != nil {
		writes[search.MemoryPercentSetting.Key] = strconv.Itoa(*v)
	}
	if v := req.AutoRebuild; v != nil {
		writes[search.AutoRebuildSetting.Key] = dbsetting.FormatBool(*v)
	}
	for key, value := range writes {
		if err := h.Store.UpsertSetting(ctx, key, value); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
	}
	// The budget is the one setting whose new value has to be PUT somewhere:
	// the others are read where they are used.
	if req.MemoryPercent != nil && h.Index != nil {
		h.Index.ApplyMemoryPercent(*req.MemoryPercent)
	}
	writeJSON(w, http.StatusOK, h.settingsSnapshot(r))
}

// settingsSnapshot reads the four settings as they are in force, and the
// budget the running index was given.
func (h *SearchAdmin) settingsSnapshot(r *http.Request) searchSettingsResponse {
	ctx := r.Context()
	out := searchSettingsResponse{
		Content:          search.ContentSetting.Resolve(ctx, h.Store),
		ContentMaxMB:     search.ContentMaxSetting.Resolve(ctx, h.Store),
		ContentMaxMBMin:  search.ContentMaxSetting.Min,
		ContentMaxMBMax:  search.ContentMaxSetting.Max,
		MemoryPercent:    search.MemoryPercentSetting.Resolve(ctx, h.Store),
		MemoryPercentMin: search.MemoryPercentSetting.Min,
		MemoryPercentMax: search.MemoryPercentSetting.Max,
		AutoRebuild:      search.AutoRebuildSetting.Resolve(ctx, h.Store),
		IndexEnabled:     h.Index != nil,
	}
	if h.Index != nil {
		b := h.Index.AppliedMemoryBudget()
		out.MemoryBudgetBytes, out.MemoryGivenBytes, out.MemoryFrom = b.Bytes, b.GivenBytes, b.From
		switch {
		case b.GivenBytes == 0:
		case b.From == sysmem.SourceHost:
			out.MemoryFromKind = "host"
		case b.From == sysmem.SourceCgroupV1 || b.From == sysmem.SourceCgroupV2:
			out.MemoryFromKind = "container"
		}
	}
	return out
}
