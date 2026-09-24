package handlers

import (
	"context"

	"github.com/brf-tech/filex/backend/internal/db"
)

// personNames resolves user ids to the name every screen shows a person by
// (model.PersonLabel: display name, else username, else e-mail), in ONE query
// for the whole page — GetUserDisplayNames, the Owner column's lookup.
//
// ⚠ The admin audit log, the Shares page and the dashboard printed the
// person's e-mail where the explorer printed their name, so one account read
// "admin@local" in one table and "admin2" in the next (QA, 2026-09-21). The
// e-mail stays on the row as the second line; this is the first.
//
// Best-effort: a failed lookup leaves the names empty and the e-mail beside
// them still says who it was.
func personNames(ctx context.Context, store db.Store, ids []int64) map[int64]string {
	seen := map[int64]bool{}
	uniq := make([]int64, 0, len(ids))
	for _, id := range ids {
		if id > 0 && !seen[id] {
			seen[id] = true
			uniq = append(uniq, id)
		}
	}
	if len(uniq) == 0 {
		return map[int64]string{}
	}
	names, err := store.GetUserDisplayNames(ctx, uniq)
	if err != nil {
		return map[int64]string{}
	}
	return names
}
