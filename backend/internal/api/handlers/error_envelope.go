package handlers

import (
	"fmt"
	"net/http"

	"github.com/brf-tech/filex/backend/internal/apierr"
)

// The one shape of a refusal (internal/apierr, docs/API-ERRORS.md):
//
//	{"error": "<code>", "message": "<sentence in the reader's language>", "params": {…}}
//
// ⚠⚠ The sentence is the server's (`server.error.<code>`, srvtext): every
// client prints `message` as it came - the explorer, the admin panel, the
// desktop app, the CLI, an MCP agent - and none of them keeps a table of its
// own. The code stays for a program to branch on; a refusal that always
// answered a code keeps answering it.

// writeError writes code's refusal in the reader's language. extra is
// key/value pairs a refusal carries beside the three (`"code", "READ_ONLY"`,
// `"max", 200`): a field older clients read keeps its place.
func writeError(w http.ResponseWriter, r *http.Request, status int, code string, params apierr.Params, extra ...any) {
	writeJSON(w, status, errorBody(r, code, params, extra...))
}

// errorBody is writeError's body, for a caller that hands it on (a helper
// that answers through its own writer).
func errorBody(r *http.Request, code string, params apierr.Params, extra ...any) map[string]any {
	var fields map[string]any
	if len(extra) > 0 {
		fields = make(map[string]any, len(extra)/2)
		for i := 0; i+1 < len(extra); i += 2 {
			fields[fmt.Sprint(extra[i])] = extra[i+1]
		}
	}
	return apierr.Map(langOf(r), code, params, fields)
}

// writeCodedError writes err's refusal when it carries a code (apierr.Error)
// and reports whether it did.
func writeCodedError(w http.ResponseWriter, r *http.Request, status int, err error) bool {
	code, params := apierr.CodeOf(err)
	if code == "" {
		return false
	}
	writeError(w, r, status, code, params)
	return true
}

// writeReadOnly is the refusal of a write to a read-only storage. ⚠ It used to
// be `403 {"error":"storage is read-only"}` on the manager's verbs and `409
// {"error":"read_only"}` elsewhere; the status each door answers is kept, the
// code and the sentence are one.
func writeReadOnly(w http.ResponseWriter, r *http.Request, status int) {
	writeError(w, r, status, "read_only", nil)
}

// writeQuotaExceeded is the refusal of a write over the owner's quota. `code`
// QUOTA_EXCEEDED stays for the clients that read it.
func writeQuotaExceeded(w http.ResponseWriter, r *http.Request, extra ...any) {
	writeError(w, r, http.StatusRequestEntityTooLarge, "quota_exceeded", nil, append([]any{"code", "QUOTA_EXCEEDED"}, extra...)...)
}
