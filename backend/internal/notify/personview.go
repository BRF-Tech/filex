package notify

import (
	"encoding/json"
	"log/slog"
	"strings"

	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/syspath"
)

// What an event about filex's OWN directories means to a person.
//
// filex writes machinery into a storage — the trash, version history, the
// desktop app's "open with filex" working copies (syspath) — and every write,
// move and delete there goes through the same post-write gate as a person's
// own files. So the gate announced them: measured 2026-09-21, one "open with
// filex" round trip on the desktop produced three bell rows — "New file:
// a1b2c3d4e5f6-Bütçe Özeti.xlsx", "File changed: a1b2c3d4e5f6-Bütçe
// Özeti.xlsx", "Moved to trash: a1b2c3d4e5f6-Bütçe Özeti.xlsx", each reading
// `/.filex-open/…` under it and each opening `.filex-open` (or `.filex-trash`)
// when clicked; and every ordinary delete targeted `.filex-trash/<key>`, whose
// raw folder the explorer then showed. The owner's words: they must appear
// neither in the notification nor in the app after the click.
//
// This file is the ONE place that decides, for every event from every
// subsystem, because notify.Service.Send is the one door every event goes
// through (writehook, antivirus, comments, shares, app plugins). The rules:
//
//   - A pure bookkeeping write — anything whose subject lives in filex's own
//     directories and is not a working copy's save — is not an event: no bell
//     row, no webhook. That covers a working copy being placed or swept, a
//     version snapshot, a keep marker, anything inside the trash.
//
//   - A save of an open-with working copy IS an event: it is a change to the
//     ORIGINAL document, which lives on the person's own computer (a document
//     that already had a copy here — a synced one — is edited in place and
//     never gets a working copy). It is announced under the original's NAME,
//     with no path (no path on any storage names it) and nothing to open
//     (`target: none`); `meta.open_with` says why. The same for an antivirus
//     hit on a working copy: the person's own document is infected.
//
//   - A person's file whose ADDRESS is inside filex's directories — a soft
//     delete, a quarantine — keeps its subject and gets a real address: the
//     Trash view with the item selected (TargetTrash), never `.filex-trash/…`.
//
// ⚠ Webhooks get the same view, deliberately. The in-app row and the webhook
// body are one Event, and a receiver acting on `.filex-open/<session>-x.docx`
// acts on a transient copy nobody can see. What a webhook still carries that
// the bell does not show is `meta.trash_path`, documented for `file.infected`
// and kept for operators; the bell's read path strips it (sanitizeRow).
// The audit log is unaffected: it records verbs and ids, never these paths.

// openWithEvents are the events that mean something about the ORIGINAL
// document when their subject is its open-with working copy.
func openWithEvent(ev EventType) bool {
	switch ev {
	case EventFileUpdated, EventFileInfected:
		return true
	}
	return false
}

// personView returns the event as a person (and a webhook) must receive it,
// or ok=false when it must not be announced at all. Pure: it touches nothing
// but its argument, so the send path and the read path (sanitizeRow) share it.
func personView(e Event) (Event, bool) {
	if e.Node != nil && syspath.Hidden(e.Node.Path) {
		name, isCopy := syspath.OpenWithOriginal(e.Node.Path)
		if !isCopy || !openWithEvent(e.Event) {
			return e, false
		}
		internal := e.Node.Path
		e.Node = &NodeRef{StorageID: e.Node.StorageID, Name: name, Size: e.Node.Size}
		e.Target = &Target{Kind: TargetNone}
		e.Meta = cloneMeta(e.Meta)
		e.Meta["open_with"] = true
		delete(e.Meta, "trash_path")
		// The emitters write the path as the body (writehook) or as
		// `<path>: <signature>` (antivirus). Neither survives: a body that is
		// a path would be rendered under the title as that path, and the
		// readers compose the sentence from the facts anyway.
		if strings.Contains(e.Body, strings.TrimPrefix(internal, "/")) {
			e.Body = ""
		}
	}
	if t := e.Target; t != nil && (t.Kind == TargetFile || t.Kind == TargetDir) && syspath.Hidden(t.Path) {
		switch {
		case (e.Event == EventFileTrashed || e.Event == EventFileInfected) && e.Node != nil && e.Node.Path != "":
			e.Target = &Target{Kind: TargetTrash, Storage: t.Storage, Path: cleanTargetPath(e.Node.Path)}
		default:
			// Nothing better is known than "there is nowhere to go", and that
			// is better than a folder nobody may see.
			e.Target = &Target{Kind: TargetNone}
		}
	}
	// A move reads `{from} → {to}` — an end of it inside filex's directories is
	// not a place to show.
	for _, k := range []string{"from", "to"} {
		if s, ok := e.Meta[k].(string); ok && syspath.Hidden(s) {
			e.Meta = cloneMeta(e.Meta)
			delete(e.Meta, k)
		}
	}
	return e, true
}

func cloneMeta(m map[string]any) map[string]any {
	out := make(map[string]any, len(m)+1)
	for k, v := range m {
		out[k] = v
	}
	return out
}

// hiddenBodies are the LIKE patterns the read path hides rows by
// (db.Store.ListNotifications): a body that IS a path inside one of filex's
// own directories. That is what every file event wrote before this gate
// existed (writehook puts the node path in the body), and personView never
// writes one now — so the patterns match exactly the old bookkeeping rows.
//
// ⚠ The rows are not rewritten or deleted: history stays as it was recorded,
// and the admin can still read it in the database. Only the read hides them,
// in SQL, so the unread badge (a COUNT) and the list agree.
//
// ⚠ Every internal directory name is plain — no `%`, `_` or `\` — so a
// pattern built from it matches only itself. syspath's tests hold that.
func hiddenBodies() []string {
	dirs := syspath.Dirs()
	out := make([]string, 0, len(dirs)*4)
	for _, d := range dirs {
		out = append(out, d, d+"/%", "/"+d, "/"+d+"/%")
	}
	return out
}

// storedMeta is the part of a row's meta_json the person view judges. The
// rest of the blob is carried through untouched.
type storedMeta struct {
	Node   *NodeRef  `json:"node,omitempty"`
	Share  *ShareRef `json:"share,omitempty"`
	Actor  *ActorRef `json:"actor,omitempty"`
	Target *Target   `json:"target,omitempty"`
}

// sanitizeRow applies personView to a row as it is READ, for rows recorded
// before the send path applied it (and as a seatbelt for any that slip past
// the SQL filter). It rewrites the returned copy only — never the table.
// Returns false when the row must not be shown at all.
//
// It also strips `meta.trash_path` from what a reader receives: the bell never
// renders it, and it is the one field left that names the bin's inside.
func sanitizeRow(n *model.Notification) bool {
	if n == nil || len(n.MetaJSON) == 0 {
		return true
	}
	var rest map[string]any
	if err := json.Unmarshal(n.MetaJSON, &rest); err != nil {
		return true // not ours to judge; HydrateTarget will read nothing either
	}
	var known storedMeta
	_ = json.Unmarshal(n.MetaJSON, &known)
	for _, k := range []string{"node", "share", "actor", "target"} {
		delete(rest, k)
	}
	before := Event{Event: EventType(n.Event), Body: n.Body, Meta: rest, Node: known.Node, Share: known.Share, Actor: known.Actor, Target: known.Target}
	after, ok := personView(before)
	if !ok {
		slog.Debug("notify: hiding a row about filex's own directories", slog.Int64("id", n.ID), slog.String("event", n.Event))
		return false
	}
	meta := cloneMeta(after.Meta)
	delete(meta, "trash_path")
	after.Meta = meta
	blob, err := marshalMeta(after)
	if err != nil {
		return true
	}
	n.MetaJSON = blob
	n.Body = after.Body
	return true
}
