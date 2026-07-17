// Package handlers — duplicates.go
//
// GET /api/admin/duplicates?limit=100&min_size=1 — admin-only report of
// files sharing (size, etag), i.e. byte-identical copies as far as the
// storage backend can tell (frozen v0.2 "Bul" contract #2).
//
// Grouping runs in SQL (db.Store.ListDuplicateNodes: GROUP BY size, etag
// HAVING COUNT(*)>1 on live file nodes with a non-empty etag); the
// handler folds the flat rows into groups, computes total_waste =
// (count-1)*size and sorts groups by total_waste descending.
package handlers

import (
	"fmt"
	"net/http"
	"sort"
	"strconv"

	"github.com/brf-tech/filex/backend/internal/db"
)

// Duplicates serves the admin duplicate-file report.
type Duplicates struct {
	Store db.Store
}

// NewDuplicates constructs the handler.
func NewDuplicates(store db.Store) *Duplicates { return &Duplicates{Store: store} }

// duplicateNode / duplicateGroup mirror the frozen contract JSON shape.
type duplicateNode struct {
	ID        int64  `json:"id"`
	StorageID int64  `json:"storage_id"`
	Path      string `json:"path"`
	Name      string `json:"name"`
	Size      int64  `json:"size"`
	Etag      string `json:"etag"`
}

type duplicateGroup struct {
	Key        string          `json:"key"`
	Size       int64           `json:"size"`
	Count      int             `json:"count"`
	TotalWaste int64           `json:"total_waste"`
	Nodes      []duplicateNode `json:"nodes"`
}

// Report handles GET /api/admin/duplicates.
//
//	limit    — max groups returned (default 100, capped at 1000)
//	min_size — minimum file size in bytes to consider (default 1, so
//	           zero-byte files never flood the report)
func (h *Duplicates) Report(w http.ResponseWriter, r *http.Request) {
	limit := queryPosInt(r, "limit", 100, 1000)
	if limit < 1 {
		limit = 1
	}
	minSize := int64(queryPosInt(r, "min_size", 1, 0))

	rows, err := h.Store.ListDuplicateNodes(r.Context(), minSize)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	// Fold flat rows into groups. Key = "<size>-<etag>" per contract;
	// insertion order preserved via the slice, lookup via the map.
	groups := make([]*duplicateGroup, 0)
	byKey := map[string]*duplicateGroup{}
	for _, row := range rows {
		key := fmt.Sprintf("%d-%s", row.Size, row.Etag)
		g, ok := byKey[key]
		if !ok {
			g = &duplicateGroup{Key: key, Size: row.Size}
			byKey[key] = g
			groups = append(groups, g)
		}
		g.Nodes = append(g.Nodes, duplicateNode{
			ID:        row.ID,
			StorageID: row.StorageID,
			Path:      row.Path,
			Name:      row.Name,
			Size:      row.Size,
			Etag:      row.Etag,
		})
	}
	for _, g := range groups {
		g.Count = len(g.Nodes)
		g.TotalWaste = int64(g.Count-1) * g.Size
	}

	// Contract: groups ordered by total_waste desc. Ties break on size
	// desc, then key asc, so the output is deterministic.
	sort.SliceStable(groups, func(i, j int) bool {
		if groups[i].TotalWaste != groups[j].TotalWaste {
			return groups[i].TotalWaste > groups[j].TotalWaste
		}
		if groups[i].Size != groups[j].Size {
			return groups[i].Size > groups[j].Size
		}
		return groups[i].Key < groups[j].Key
	})
	if len(groups) > limit {
		groups = groups[:limit]
	}

	writeJSON(w, http.StatusOK, map[string]any{"groups": groups})
}

// queryPosInt parses a non-negative int query param with a default;
// max <= 0 means "no upper cap". Malformed / negative values fall back
// to the default.
func queryPosInt(r *http.Request, name string, def, max int) int {
	raw := r.URL.Query().Get(name)
	if raw == "" {
		return def
	}
	v, err := strconv.Atoi(raw)
	if err != nil || v < 0 {
		return def
	}
	if max > 0 && v > max {
		return max
	}
	return v
}
