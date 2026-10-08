package ops

// What a trash job says: a restore, a permanent delete, "empty the trash".
//
// ⚠⚠ Why here (0.54, findings A4 and A15). These sentences were written in
// the browser, twice: the explorer built "Trash emptied, but 3 items could not
// be purged - see the server log" (and printed a raw "504" or the server's
// English when the run stopped), the admin Trash page built its own from its
// own keys, the explorer's restore and permanent delete tallied one request
// per item and composed the summary themselves, and an agent (MCP) got bare
// counts. The server knows what happened; it says it, once, in the reader's
// language (internal/srvtext), and every surface shows that sentence: the
// batch answers of the trash handlers, the "empty the trash" status, and the
// `summary` of a restore, purge or trash-empty row of this queue.

import (
	"context"
	"strings"

	"github.com/brf-tech/filex/backend/internal/srvtext"
)

// Why the first entry of a trash batch that did not go did not go: the
// `reason_code` of a batch answer ({done, failed, reason_code}).
const (
	// ReasonExists: something already holds the entry's original place
	// (a restore; nothing moved, the entry is still in the trash).
	ReasonExists = "exists"
	// ReasonNotFound: the entry is not in the trash (any more), or is not the
	// caller's to see.
	ReasonNotFound = "not_found"
	// ReasonForbidden: the caller may not do this to the entry.
	ReasonForbidden = "forbidden"
	// ReasonFailed: anything else.
	ReasonFailed = "failed"
)

// TakenPrefix starts the error a restore job records for an entry whose
// original place is taken (handlers.Trash.RestoreNode); the rest is the name.
// The row's summary reads the reason and the name back from it.
const TakenPrefix = "something already exists at this path: "

// batchWords is one kind's sentences: what is on its way, what went, and what
// did not by reason. Every key is written out (srvtext's tree scan checks the
// literal keys exist).
type batchWords struct {
	queued, done string
	failed       map[string]string
}

var trashBatchWords = map[string]batchWords{
	OpRestore: {
		queued: "server.trash.restore.queued",
		done:   "server.trash.restore.done",
		failed: map[string]string{
			ReasonExists:    "server.trash.restore.failed.exists",
			ReasonNotFound:  "server.trash.restore.failed.not_found",
			ReasonForbidden: "server.trash.restore.failed.forbidden",
			ReasonFailed:    "server.trash.restore.failed.other",
		},
	},
	OpPurge: {
		queued: "server.trash.purge.queued",
		done:   "server.trash.purge.done",
		failed: map[string]string{
			ReasonNotFound:  "server.trash.purge.failed.not_found",
			ReasonForbidden: "server.trash.purge.failed.forbidden",
			ReasonFailed:    "server.trash.purge.failed.other",
		},
	},
}

// counted is a sentence about n things, with n written in the language's
// own digit grouping.
func counted(lang, key string, n int, vars srvtext.Vars) string {
	v := srvtext.Vars{}
	for k, x := range vars {
		v[k] = x
	}
	v["count"] = srvtext.Number(lang, int64(n))
	return srvtext.Plural(lang, key, n, v)
}

// SayTrashBatch is what a batch of trash entries restored (kind OpRestore)
// or deleted for good (OpPurge) says, in lang: how many went - or, queued,
// are on their way - and how many did not and why (reason, one of the Reason
// codes; name is the entry a taken place names). One part, or both joined.
func SayTrashBatch(lang, kind string, queued bool, done, failed int, reason, name string) string {
	w, ok := trashBatchWords[kind]
	if !ok {
		return ""
	}
	var parts []string
	if done > 0 || failed == 0 {
		key := w.done
		if queued {
			key = w.queued
		}
		parts = append(parts, counted(lang, key, done, nil))
	}
	if failed > 0 {
		key, ok := w.failed[reason]
		if !ok {
			key = w.failed[ReasonFailed]
		}
		parts = append(parts, counted(lang, key, failed, srvtext.Vars{"name": name}))
	}
	if len(parts) == 1 {
		return parts[0]
	}
	return srvtext.Text(lang, "server.trash.clauses", srvtext.Vars{"first": parts[0], "second": parts[1]})
}

// SayTrashEmpty is where an "empty the trash" run stands, in lang: waiting
// its turn, running (so far of all), or how it ended. ⚠ It never sends a
// person to the server log: what could not be deleted is still in the trash,
// and that is what they can act on.
func SayTrashEmpty(lang string, op *Op) string {
	if op == nil {
		return ""
	}
	purged := max(op.Done-op.Failed, 0)
	switch op.Status {
	case StatusPending:
		return srvtext.Text(lang, "server.trash.empty.queued", nil)
	case StatusRunning, StatusCancelling:
		return srvtext.Text(lang, "server.trash.empty.running", srvtext.Vars{
			"done":  srvtext.Number(lang, int64(op.Done)),
			"total": srvtext.Number(lang, int64(op.Total)),
		})
	case StatusCancelled:
		return counted(lang, "server.trash.empty.cancelled", purged, nil)
	case StatusPartial:
		return counted(lang, "server.trash.empty.partly", op.Failed, nil)
	case StatusFailed:
		switch {
		case op.Failed > 0 && purged == 0:
			return counted(lang, "server.trash.empty.none", op.Failed, nil)
		case op.Failed > 0:
			return counted(lang, "server.trash.empty.partly", op.Failed, nil)
		}
		return counted(lang, "server.trash.empty.stopped", purged, nil)
	}
	switch {
	case purged == 0:
		return srvtext.Text(lang, "server.trash.empty.done_nothing", nil)
	case op.BytesDone > 0:
		return counted(lang, "server.trash.empty.done", purged, srvtext.Vars{"size": srvtext.Bytes(lang, op.BytesDone)})
	}
	return counted(lang, "server.trash.empty.done_nosize", purged, nil)
}

// SayTrashEmptyPreview is the question "Empty the trash?" asks: what the
// purge would delete, counted the way the purge counts (trash.Service.Tally).
func SayTrashEmptyPreview(lang string, count int, bytes int64) string {
	switch {
	case count == 0:
		return srvtext.Text(lang, "server.trash.empty.confirm_nothing", nil)
	case bytes > 0:
		return counted(lang, "server.trash.empty.confirm", count, srvtext.Vars{"size": srvtext.Bytes(lang, bytes)})
	}
	return counted(lang, "server.trash.empty.confirm_nosize", count, nil)
}

// sayRow is a trash job's row summary, "" for every other kind and for a
// restore or purge somebody cancelled (the operations centre says that).
func sayRow(lang string, op *Op) string {
	switch op.Kind {
	case OpTrashEmpty:
		return SayTrashEmpty(lang, op)
	case OpRestore, OpPurge:
	default:
		return ""
	}
	switch op.Status {
	case StatusPending, StatusRunning, StatusCancelling:
		return SayTrashBatch(lang, op.Kind, true, op.Total, 0, "", "")
	case StatusOK:
		return SayTrashBatch(lang, op.Kind, false, op.Done, 0, "", "")
	case StatusPartial, StatusFailed:
		failed := op.Failed
		if failed == 0 {
			// The job stopped before it judged its entries one by one (no
			// restorer wired, the row unreadable): nothing of what is left went.
			failed = max(op.Total-op.Done, 1)
		}
		reason, name := rowReasonOf(op)
		return SayTrashBatch(lang, op.Kind, false, op.Done, failed, reason, name)
	}
	return ""
}

// rowReasonOf is a restore or purge row's reason: the code the row keeps
// (errcode.go: name_taken with the name, not_in_trash), else, for a row an
// older server wrote, its English read back by rowReason.
func rowReasonOf(op *Op) (reason, name string) {
	switch op.ErrorCode {
	case "name_taken":
		return ReasonExists, op.ErrorParams["name"]
	case "not_in_trash":
		return ReasonNotFound, ""
	case "":
		return rowReason(op.Error)
	}
	return ReasonFailed, ""
}

// rowReason reads a restore or purge job's recorded error as a reason code
// (and, for a taken place, the name it names).
func rowReason(msg string) (reason, name string) {
	switch {
	case strings.HasPrefix(msg, TakenPrefix):
		name = strings.TrimPrefix(msg, TakenPrefix)
		if i := strings.Index(name, "; "); i >= 0 {
			name = name[:i]
		}
		return ReasonExists, name
	case strings.Contains(msg, "not in the trash"), strings.Contains(msg, "trash entry not found"):
		return ReasonNotFound, ""
	}
	return ReasonFailed, ""
}

// sayRows fills the Summary of the trash rows, in the language of the reader
// the request names (srvtext.WithReader; the instance default otherwise).
func sayRows(ctx context.Context, rows []*Op) {
	var lang string
	for _, op := range rows {
		if op == nil {
			continue
		}
		if op.Kind != OpTrashEmpty && op.Kind != OpRestore && op.Kind != OpPurge {
			continue
		}
		if lang == "" {
			lang = srvtext.Pick(srvtext.Reader(ctx))
		}
		op.Summary = sayRow(lang, op)
	}
}
