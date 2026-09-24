package db

import (
	"strings"

	"github.com/brf-tech/filex/backend/internal/model"
)

// TagQueryWhere renders a model.TagQuery as a SQL predicate over the `tags`
// table aliased `alias`, with the placeholder syntax the caller's engine uses
// (`?` for SQLite/MySQL, `$n` for Postgres — `ph(n)` is given the 1-based
// position counting from `first`).
//
// ONE builder for both store implementations. The predicate is the
// visibility boundary between one person's tags and another's, and between
// one tenant's and another's; two hand-written copies of it are two chances
// for the engines to disagree about who sees a tag.
//
// An empty query (no owner, no team) renders "1=0": a caller that asked for
// nothing gets nothing, never "no filter".
func TagQueryWhere(q model.TagQuery, alias string, first int, ph func(n int) string) (string, []any) {
	var parts []string
	var args []any
	next := func(v any) string {
		args = append(args, v)
		return ph(first + len(args) - 1)
	}
	if q.OwnerID > 0 {
		parts = append(parts, alias+".owner_id = "+next(q.OwnerID))
	}
	if q.Team {
		switch {
		case q.TeamTenants == nil:
			parts = append(parts, alias+".kind = 'team'")
		case len(q.TeamTenants) > 0:
			in := make([]string, len(q.TeamTenants))
			for i, id := range q.TeamTenants {
				in[i] = next(id)
			}
			parts = append(parts, "("+alias+".kind = 'team' AND "+alias+".tenant_id IN ("+strings.Join(in, ",")+"))")
		}
	}
	if len(parts) == 0 {
		return "1=0", nil
	}
	return "(" + strings.Join(parts, " OR ") + ")", args
}

// IDList renders ids as a placeholder list with ph, for `IN (…)`.
func IDList(ids []int64, first int, ph func(n int) string) (string, []any) {
	ps := make([]string, len(ids))
	args := make([]any, len(ids))
	for i, id := range ids {
		ps[i] = ph(first + i)
		args[i] = id
	}
	return strings.Join(ps, ","), args
}
