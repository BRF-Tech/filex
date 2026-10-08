package ops

import (
	"context"
	"errors"

	"github.com/brf-tech/filex/backend/internal/apierr"
	"github.com/brf-tech/filex/backend/internal/auth"
	"github.com/brf-tech/filex/backend/internal/srvtext"
	"github.com/brf-tech/filex/backend/internal/trash"
	"github.com/brf-tech/filex/backend/internal/writegate"
)

// A failed row keeps a CODE and its params, not Go's error text (0.54 audit
// A2): the sentence is made when the row is READ, in the reader's language
// (sayErrors), and the English stays the row's `error` for an
// administrator's second line and for the log.
//
// ⚠⚠ Why: the explorer used to recognise the queue's failures by matching
// regular expressions against the English ("something with that name
// already exists here", "trash: the item is not in the trash") - a Go error
// text that changed silently stopped being translated, and the CLI and an
// MCP agent read the English as it was. The column is the same TEXT column;
// apierr.Encode writes the code into it as JSON, and a row an older server
// wrote reads back as its plain text (no code).

// errCode is the code a failure is kept under: its own (apierr.Error), or the
// one a known failure of the queue stands for; "" for anything else.
func errCode(err error) (string, apierr.Params) {
	if err == nil {
		return "", nil
	}
	if code, params := apierr.CodeOf(err); code != "" {
		return code, params
	}
	var le *writegate.LockedError
	switch {
	case errors.Is(err, ErrNameTaken):
		return "name_taken", nil
	case errors.Is(err, trash.ErrNotInTrash):
		return "not_in_trash", nil
	case errors.As(err, &le) && le.Lock != nil:
		return "locked", apierr.Params{"app": le.Lock.PluginName}
	case errors.Is(err, writegate.ErrLocked):
		return "locked", nil
	}
	return "", nil
}

// encodeFailure is what the row's error column keeps for err; tail is added
// to the English detail (the items a partly done op skipped).
func encodeFailure(err error, tail string) string {
	detail := errMessage(err)
	if tail != "" {
		if detail != "" {
			detail += "; "
		}
		detail += tail
	}
	code, params := errCode(err)
	return apierr.Encode(code, params, detail)
}

// decodeFailure reads back what encodeFailure wrote onto op.
func decodeFailure(op *Op) {
	code, params, detail := apierr.Decode(op.Error)
	if code == "" {
		return
	}
	op.ErrorCode, op.ErrorParams, op.Error = code, params, detail
}

// sayErrors says each failed row's code in the reader's language
// (srvtext.Reader on ctx): op.ErrorText. An app job's codes (the plugin
// Decorator's: timeout, engine_missing…) are said the same way; an
// administrator reads the form that tells them what to fix (`<code>_admin`)
// where there is one. A code the catalogue has no sentence for ("app", the
// app's own words; "cancelled") is left to the row's other fields.
func sayErrors(ctx context.Context, rows []*Op) {
	lang := srvtext.Reader(ctx)
	admin := auth.CallerMayAdminister(ctx)
	for _, op := range rows {
		if op == nil || op.ErrorCode == "" || op.ErrorText != "" {
			continue
		}
		code := op.ErrorCode
		if admin && apierr.Known(code+"_admin") {
			code += "_admin"
		}
		params := op.ErrorParams
		if op.ErrorEngine != "" {
			merged := apierr.Params{"engine": op.ErrorEngine}
			for k, v := range params {
				merged[k] = v
			}
			params = merged
		}
		op.ErrorText = apierr.Text(lang, code, params)
	}
}
