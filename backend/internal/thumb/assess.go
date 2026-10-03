package thumb

import (
	"strings"
	"time"

	"github.com/brf-tech/filex/backend/internal/model"
)

// Verdict is what Assess says about one file's thumbnail.
type Verdict uint8

const (
	// Leave means the row is as good as it will get: fresh, or failed/skipped
	// on this very content.
	Leave Verdict = iota
	// Render means the file should be drawn (again): it never was, its content
	// changed since, or what stopped it last time is gone.
	Render
)

func (v Verdict) String() string {
	if v == Render {
		return "render"
	}
	return "leave"
}

// SkipNoSVGEngine is the reason a row records when an SVG could not be drawn
// because no SVG engine was available. Assess reads it back: once an engine
// is there, those rows are drawn.
const SkipNoSVGEngine = "svg: no engine available"

// legacySkipNoRsvg is the reason 0.49 and earlier recorded for the same
// thing, when the only engine was rsvg-convert on PATH.
const legacySkipNoRsvg = "rsvg-convert not in PATH"

const (
	// retryGuard: a file whose render started this recently is never asked
	// for again. It is what stops a listing that keeps seeing a different
	// signature (a clock that disagrees, a backend that reports a new etag on
	// every listing) from turning every page view into a render.
	retryGuard = 60 * time.Second
	// pendingStale: a row left `pending` for this long was left by a render
	// that never finished — a crash or a restart.
	pendingStale = 10 * time.Minute
	// legacySlack is the modification-time granularity the storage sync uses
	// (sync/etag.go mtimeGranularity): a file modified within the same second
	// as its render is not called stale.
	legacySlack = time.Second
)

// SourceSig is the signature a row records for the content it was drawn
// from: the node's ContentFingerprint, the one definition the search index
// uses as well.
func SourceSig(n *model.Node) string { return n.ContentFingerprint() }

// Assess decides whether n's thumbnail row t (nil: no row) should be drawn.
//
// ⚠⚠ It reads only what the caller already holds: the catalogue row a listing
// or the storage sync just read, and the thumbnail row next to it. No byte of
// the file and no request to the backend, because it runs on every listing.
// The table it implements, with the reasons, is in docs/thumbnails.md
// (Design notes → Freshness).
func (p *Pipeline) Assess(n *model.Node, t *model.Thumbnail, now time.Time) Verdict {
	if p == nil || n == nil || n.Type != model.NodeTypeFile || n.DeletedAt != nil {
		return Leave
	}
	// An upload still being transferred out of staging is drawn by the upload
	// path when it commits; drawing it from here as well would race it.
	if n.TransferState != "" && n.TransferState != model.TransferStateStored {
		return Leave
	}
	// A file whose thumbnail would only be the placeholder card (an archive,
	// a text, a model) is not drawn from a listing: the card says the
	// extension and nothing about the content, so there is nothing to keep
	// fresh, and every view draws that file's type tile (or, for text, its
	// first lines) instead. Uploads and the repair tool still draw it.
	//
	// ⚠ Unless an app draws that kind (apps.go), or drew this row: then its
	// thumbnail IS a picture of the content, and it is kept fresh like any
	// other - an app switched off for the kind leaves pictures that are stale.
	if !drawsContent(n) && !p.appDraws(n) && !askedAnApp(t) {
		return Leave
	}
	// The document server failed this content for a reason that may pass
	// (office.go: oo_retry), and its back-off is over.
	retryDue := officeRetryDue(t, now)
	if t == nil {
		return Render
	}
	if t.AttemptedAt != nil && now.Sub(*t.AttemptedAt) < retryGuard {
		return Leave
	}
	changed := sigChanged(n, t)
	switch t.State {
	case "ready":
		if changed || stale(n, t) || p.handlersChanged(n, t) || retryDue {
			return Render
		}
		return Leave
	case "failed":
		if changed || p.handlersChanged(n, t) || retryDue {
			return Render
		}
		return Leave
	case "skipped":
		if changed || p.engineReturned(t) || p.limitRaised(n, t) || p.appLimitRaised(n, t) || p.handlersChanged(n, t) {
			return Render
		}
		return Leave
	case "pending":
		if t.AttemptedAt == nil || now.Sub(*t.AttemptedAt) > pendingStale {
			return Render
		}
		return Leave
	}
	return Render
}

// drawsContent reports whether n's thumbnail is a picture of its content (an
// image, an SVG, a video frame, a waveform, a page, a text's first lines, an
// archive's list, an office document's first page), as opposed to the
// placeholder card generic.go draws for every other kind. An office document
// is one whether or not OnlyOffice is configured: without it the row says
// so (no_tool:office), and it is drawn once it is.
func drawsContent(n *model.Node) bool { return builtinDraws(n) || officeKind(n) }

// builtinDraws reports whether filex's own drawer draws n: an image, a video,
// an audio file, a PDF, a text, an archive it lists. Office documents are the
// document server's (office.go).
func builtinDraws(n *model.Node) bool {
	if isTextThumb(n.Name) || archiveKind(n.Name) != "" {
		return true
	}
	m := routeMime(n)
	return strings.HasPrefix(m, "image/") || strings.HasPrefix(m, "video/") || strings.HasPrefix(m, "audio/") ||
		m == "application/pdf"
}

// sigChanged: the row records a source, and it is not this content.
func sigChanged(n *model.Node, t *model.Thumbnail) bool {
	return t.SourceSig != "" && t.SourceSig != SourceSig(n)
}

// stale: a ready picture of other content, by the signature, or for a row
// drawn before rows had one, by the dates. An SVG's row from before rows had
// a signature is stale as it is: 0.49 routed SVGs by their sniffed type and
// drew most of them as the placeholder card (routeMime), so each is drawn
// once more, and the new row records its signature.
func stale(n *model.Node, t *model.Thumbnail) bool {
	if t.SourceSig == "" {
		return legacyStale(n, t) || routeMime(n) == svgMime
	}
	return sigChanged(n, t)
}

// IsNoEngineSkip reports whether a skip reason is "no SVG engine" (this
// release's wording or 0.49's).
func IsNoEngineSkip(reason string) bool {
	return reason == SkipNoSVGEngine || reason == legacySkipNoRsvg
}

// engineReturned: the row was skipped for want of an SVG engine, or of the
// program its kind needs (notool.go), and this install has it now.
func (p *Pipeline) engineReturned(t *model.Thumbnail) bool {
	return (IsNoEngineSkip(t.Error) && p.caps.SVG) || p.toolReturned(t)
}

// Selection is what a repair run draws: Admin → Tools → Thumbnail repair and
// `filex thumb backfill` (server.BackfillThumbs). A file with no row, or one
// left pending, is always drawn; a skip whose engine has arrived too.
type Selection struct {
	// All draws every file in scope ("Rebuild").
	All bool
	// Stale draws a ready picture of other content, and a failure or a skip
	// recorded on other content.
	Stale bool
	// Failed and Skipped draw those rows whatever content they were on.
	Failed  bool
	Skipped bool
}

// Fix is the selection "repair what is wrong": missing, stale, failed and
// skipped.
var Fix = Selection{Stale: true, Failed: true, Skipped: true}

// Wanted reports whether a repair run with selection sel draws n, whose row
// is t. Unlike Assess it has no loop guard and no idea of "somebody is
// drawing it": an administrator asked for exactly this.
func (p *Pipeline) Wanted(n *model.Node, t *model.Thumbnail, sel Selection) bool {
	if p == nil || n == nil || n.Type != model.NodeTypeFile || n.DeletedAt != nil {
		return false
	}
	if sel.All || t == nil {
		return true
	}
	switch t.State {
	case "ready":
		return sel.Stale && (stale(n, t) || p.handlersChanged(n, t))
	case "failed":
		return sel.Failed || (sel.Stale && (sigChanged(n, t) || p.handlersChanged(n, t)))
	case "skipped":
		return sel.Skipped || (sel.Stale && (sigChanged(n, t) || p.handlersChanged(n, t))) ||
			p.engineReturned(t) || p.limitRaised(n, t) || p.appLimitRaised(n, t)
	}
	return true
}

// legacyStale answers "stale?" for a row drawn before rows recorded their
// source: the file was modified after the render. The render follows the
// write on every path that draws, so a modification time later than the
// render is a change the render never saw.
func legacyStale(n *model.Node, t *model.Thumbnail) bool {
	if n.BackendMtime == nil || t.GeneratedAt == nil {
		return false
	}
	return n.BackendMtime.UTC().Truncate(legacySlack).After(t.GeneratedAt.UTC().Truncate(legacySlack))
}
