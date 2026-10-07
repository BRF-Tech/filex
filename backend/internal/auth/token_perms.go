package auth

import (
	"context"
	"net/http"

	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/tokenperm"
)

// # A token's permissions with a level, on every door
//
// Beside its verbs (token_verbs.go) a token holds each permission of package
// tokenperm at a level - `comments` at `read` or `rw`. The verbs answer "may
// it change files"; a permission answers "may it do this one other thing",
// and `write` does not stand in for it: a `read,write` token holds comments
// at `read` unless its list says `comments:rw` (the maintainer, 2026-10-06,
// task #157). A token that does not name a permission holds its default
// (tokenperm.LevelIn) - every token minted before the permission existed.
//
// One question, asked here for every door: the explorer's route and the
// handler it runs (/api/files/comments), the same handler run in process by
// /api/ai and the MCP tools, and the MCP tool list, which leaves out what the
// token cannot use. A request with no token - a browser session - is judged
// by the account alone, as with the verbs.

// The comments permission's two needs. Reading a file's comments asks
// CommentsRead; adding and deleting one asks CommentsWrite.
var (
	CommentsRead  = tokenperm.Need{Key: tokenperm.Comments, Level: tokenperm.Read}
	CommentsWrite = tokenperm.Need{Key: tokenperm.Comments, Level: tokenperm.ReadWrite}
)

// TokenHolds reports whether tok holds need. A nil token holds nothing: ask
// TokenAllowsPerm about a request, where no token at all is a session.
func TokenHolds(tok *model.APIToken, need tokenperm.Need) bool {
	return tok != nil && tok.PermLevel(need.Key).Covers(need.Level)
}

// TokenAllowsPerm reports whether the request may do what need names: always
// for a request with no API token on its context, and for a token when it
// holds need.
func TokenAllowsPerm(ctx context.Context, need tokenperm.Need) bool {
	tok := TokenFrom(ctx)
	return tok == nil || TokenHolds(tok, need)
}

// RefusePerm writes the refusal a token without need gets: the verbs' answer,
// naming the permission - `403 {"error":"token missing scope: comments:write"}`.
func RefusePerm(w http.ResponseWriter, need tokenperm.Need) {
	writeAuthErr(w, http.StatusForbidden, "token missing scope: "+need.String())
}

// AllowTokenPerm asks need inside a handler: it answers the refusal itself
// and reports whether the handler may go on. It is asked in the handler and
// not on the route because the handler is what every door runs - /api/ai and
// the MCP tools run the explorer's handlers in process.
func AllowTokenPerm(w http.ResponseWriter, r *http.Request, need tokenperm.Need) bool {
	if TokenAllowsPerm(r.Context(), need) {
		return true
	}
	RefusePerm(w, need)
	return false
}
