package thumb

// Office documents are drawn by OnlyOffice (filex 0.50, docs/thumbnails.md →
// Office through OnlyOffice).
//
// The thumbnail of a Word, Excel, PowerPoint or OpenDocument file is a picture
// of its first page made by the OnlyOffice document server filex is
// configured with: its conversion service is asked for a PNG of the first
// page (internal/onlyoffice: Service.DrawThumbnail), which downloads the
// document from filex through a fetch address signed for that purpose. filex
// ships no office suite of its own: LibreOffice left the image in 0.50, and
// with it the `soffice` path that used to live here. An install without
// OnlyOffice draws no office thumbnails, and says so: the file is skipped as
// "no_tool:office" (the Thumbnail repair tab: "OnlyOffice is not
// configured"), never given the placeholder card, and drawn once OnlyOffice
// is configured (Assess, toolReturned and the chain).
//
// The pipeline does not import the onlyoffice package (it imports this one
// through protocolsync); the server hands it an OfficeDrawer, the same way it
// hands it the apps (AppThumbs).
//
// ⚠⚠ Gently. A Community Edition document server runs ONE converter, shared
// with the editors (an editor opening a document goes first, a conversion
// waits). So the pipeline asks it for one file at a time per filex process
// (OfficeSlotsSetting, 1 by default, at most 4), gives each file
// officeTimeout, and sends nothing over OfficeMaxMBSetting.
//
// What an answer means for the row (one table, officeOutcome):
//
//	corrupt (-3, -7, -9)        failed  oo_corrupt:<what>   never asked again for this content
//	password (-5)               skipped oo_password         never asked again for this content
//	too large (-10)             skipped oo_too_large:0      the document server's own limit
//	over the size setting       skipped oo_too_large:<max>  asked again when the setting is raised
//	encrypted (filex refused)   skipped e2e-encrypted ...   the file is end-to-end encrypted
//	not configured              skipped no_tool:office      asked again once it is configured
//	anything else               failed  oo_retry:<n>:<what> asked again after a back-off, 5 times
//
// The back-off: 2 minutes, 8, 32, about 2 hours, about 8 hours after the
// failures one to five (officeBackoff); after the sixth try the row is left
// until the file changes, a repair asks for it, or the chain changes.

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/brf-tech/filex/backend/internal/dbsetting"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/storage"
	"github.com/brf-tech/filex/backend/pkg/pluginkit/wire"
)

// OfficeDrawer is what the pipeline asks to draw an office document's first
// page (the server's adapter over internal/onlyoffice).
type OfficeDrawer interface {
	// Ready reports whether OnlyOffice is configured right now. From memory
	// (the configuration is cached): Assess asks it for every listed office
	// file.
	Ready(ctx context.Context) bool
	// DrawPage asks the document server for a PNG of the document's first
	// page. A failure is an *OfficeError; any other error is transient.
	DrawPage(ctx context.Context, page OfficePage) ([]byte, error)
}

// OfficePage is one document to draw.
type OfficePage struct {
	NodeID    int64
	StorageID int64
	// Name is the file's name; its extension is the type the document server
	// is told.
	Name string
	// ContentSig is the content's fingerprint (SourceSig), and Attempt counts
	// the tries at it from 1: both are part of the document server's key, so
	// a new version or a retry is never answered from its cache.
	ContentSig string
	Attempt    int
	// MaxBytes is the largest answer accepted.
	MaxBytes int64
}

// The classes of an OfficeError.
const (
	OfficeTransient    = "transient"
	OfficeCorrupt      = "corrupt"
	OfficePassword     = "password"
	OfficeTooLarge     = "too_large"
	OfficeEncrypted    = "encrypted"
	OfficeUnconfigured = "unconfigured"
)

// OfficeError is why the document server did not draw a document. What is a
// short name of the failure for the row ("ds-3", "http-502", "net",
// "timeout", "origin", ...): never an address, never a secret.
type OfficeError struct {
	Class string
	What  string
	Err   error
}

func (e *OfficeError) Error() string {
	msg := "office thumbnail: " + e.Class
	if e.What != "" {
		msg += " (" + e.What + ")"
	}
	if e.Err != nil {
		msg += ": " + e.Err.Error()
	}
	return msg
}

func (e *OfficeError) Unwrap() error { return e.Err }

// AttachOffice wires the document server. Nil: no office thumbnails (each
// office file is skipped as no_tool:office).
func (p *Pipeline) AttachOffice(d OfficeDrawer) { p.office = d }

// officeReady: OnlyOffice is configured now.
func (p *Pipeline) officeReady(ctx context.Context) bool {
	return p != nil && p.office != nil && p.office.Ready(ctx)
}

// The reasons an office document's row records (see the table above).
const (
	SkipOfficeCorrupt  = "oo_corrupt"
	SkipOfficePassword = "oo_password"
	SkipOfficeTooLarge = "oo_too_large"
	SkipOfficeRetry    = "oo_retry"
)

// officeKinds are the kinds the document server draws a thumbnail of, by
// extension: the text documents, spreadsheets and presentations of Microsoft
// Office (with their macro-enabled and template variants) and OpenDocument,
// RTF, EPUB, Apple's Pages, Numbers and Keynote, OpenDocument drawings and
// Visio. ⚠ PDF, pictures and plain text stay filex's own (it draws them
// without a round trip), and so does CSV (its first lines).
var officeKinds = map[string]bool{
	"doc": true, "docx": true, "docm": true, "dot": true, "dotx": true, "dotm": true,
	"odt": true, "ott": true, "rtf": true, "epub": true, "pages": true,
	"xls": true, "xlsx": true, "xlsm": true, "xlsb": true, "xlt": true, "xltx": true, "xltm": true,
	"ods": true, "ots": true, "numbers": true,
	"ppt": true, "pptx": true, "pptm": true, "pps": true, "ppsx": true, "ppsm": true,
	"pot": true, "potx": true, "potm": true, "odp": true, "otp": true, "key": true,
	"odg": true, "vsdx": true,
}

// OnlyOfficeDraws reports whether the document server draws a thumbnail of a
// file of this name and type (whether OnlyOffice is configured is not this
// function's question).
//
// ⚠ `.key` is a Keynote presentation only when its content is a package (a
// zip, as the sniff calls it): the same extension names TLS private keys and
// licence keys, and those are never handed to anybody to "draw".
func OnlyOfficeDraws(name, mime string) bool {
	ext := strings.TrimPrefix(strings.ToLower(extOf(name)), ".")
	if !officeKinds[ext] {
		return false
	}
	if ext == "key" {
		m := strings.ToLower(strings.TrimSpace(mime))
		if i := strings.IndexByte(m, ';'); i >= 0 {
			m = strings.TrimSpace(m[:i])
		}
		return m == "application/zip"
	}
	return true
}

// officeKind: n is a kind the document server draws.
func officeKind(n *model.Node) bool { return OnlyOfficeDraws(n.Name, routeMime(n)) }

// The two settings an office thumbnail is drawn under (Settings, and Admin →
// Tools → Thumbnail repair), and the time each file has.
var (
	// OfficeMaxMBSetting: the largest document sent to the document server.
	// 25 MB: the measured documents drew in under a second up to a few MB, a
	// 13 MB sheet took 15 s; past this a thumbnail is not worth holding the
	// one converter the editors share. The document server's own download
	// limit is 100 MB by default (FileConverter.converter.maxDownloadBytes).
	OfficeMaxMBSetting = dbsetting.IntSpec{
		Key: "thumbs.office_max_mb", EnvVar: "FILEX_THUMBS_OFFICE_MAX_MB",
		Default: 25, Min: 1, Max: 100, Unit: "MB",
	}
	// OfficeSlotsSetting: how many documents one filex process has the
	// document server convert at once. 1: a Community Edition server runs one
	// converter, shared with the editors.
	OfficeSlotsSetting = dbsetting.IntSpec{
		Key: "thumbs.office_slots", EnvVar: "FILEX_THUMBS_OFFICE_SLOTS",
		Default: 1, Min: 1, Max: 4, Unit: "conversions",
	}
)

const (
	// officeTimeout is the time one document has: the request, the
	// polling, the download of the picture.
	officeTimeout = 60 * time.Second
	// officeSlotWait is the longest a document waits for a free slot before
	// the try counts as a transient failure (`busy`) and is backed off.
	officeSlotWait = 2 * time.Minute
	// officeMaxRetries: tries after the first failure. Six tries in all.
	officeMaxRetries = 5
)

// OfficeLimits is what an office document is drawn under right now.
type OfficeLimits struct {
	MaxBytes int64
	Slots    int
}

// DefaultOfficeLimits apply with no settings store.
func DefaultOfficeLimits() OfficeLimits {
	return OfficeLimits{MaxBytes: int64(OfficeMaxMBSetting.Default) << 20, Slots: OfficeSlotsSetting.Default}
}

// ResolveOfficeLimits reads the two settings.
func ResolveOfficeLimits(ctx context.Context, g dbsetting.Getter) OfficeLimits {
	return OfficeLimits{
		MaxBytes: int64(OfficeMaxMBSetting.Resolve(ctx, g)) << 20,
		Slots:    OfficeSlotsSetting.Resolve(ctx, g),
	}
}

// OfficeLimits is what an office document is drawn under right now (cached
// with the other thumbnail settings, svglimits.go).
func (p *Pipeline) OfficeLimits() OfficeLimits {
	if p == nil || p.settings == nil || p.settings.g == nil {
		return DefaultOfficeLimits()
	}
	return p.settings.current().office
}

// officeBackoff is how long after its n-th failure (from 1) a document is
// asked for again: 2 minutes, then four times longer each time.
func officeBackoff(n int) time.Duration {
	d := 2 * time.Minute
	for i := 1; i < n; i++ {
		d *= 4
	}
	return d
}

// officeRetryReason spells a transient failure: the tries so far and what
// failed.
func officeRetryReason(n int, what string) string {
	return SkipOfficeRetry + ":" + strconv.Itoa(n) + ":" + cleanWhat(what)
}

// cleanWhat keeps a failure's short name to what a reason may carry.
func cleanWhat(what string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(what) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
			b.WriteRune(r)
		}
		if b.Len() >= 24 {
			break
		}
	}
	if b.Len() == 0 {
		return "failed"
	}
	return b.String()
}

// ParseOfficeReason reads an office reason back: its code (oo_corrupt,
// oo_password, oo_retry; oo_too_large is ParseSkip's), what failed, and for
// oo_retry the tries so far. ok is false for any other reason.
func ParseOfficeReason(reason string) (code, what string, tries int, ok bool) {
	parts := strings.Split(reason, ":")
	switch {
	case parts[0] == SkipOfficePassword && len(parts) == 1:
		return SkipOfficePassword, "", 0, true
	case parts[0] == SkipOfficeCorrupt && len(parts) == 2:
		return SkipOfficeCorrupt, parts[1], 0, true
	case parts[0] == SkipOfficeRetry && len(parts) == 3:
		n, err := strconv.Atoi(parts[1])
		if err != nil || n < 1 {
			return "", "", 0, false
		}
		return SkipOfficeRetry, parts[2], n, true
	}
	return "", "", 0, false
}

// officeTries is how many transient failures the row records for the
// document server on this content: the row's own reason, or its entry in the
// attempts (another handler may have drawn after it). 0 for other content.
func officeTries(t *model.Thumbnail, sig string) int {
	if t == nil || (sig != "" && t.SourceSig != "" && t.SourceSig != sig) {
		return 0
	}
	reasons := []string{t.Error}
	for _, a := range ParseAttempts(t.Attempts) {
		reasons = append(reasons, a.R)
	}
	for _, r := range reasons {
		if code, _, n, ok := ParseOfficeReason(r); ok && code == SkipOfficeRetry {
			return n
		}
	}
	return 0
}

// officeRetryDue: the document server failed this content for a reason that
// may pass (oo_retry), at most officeMaxRetries times so far, and its
// back-off is over.
func officeRetryDue(t *model.Thumbnail, now time.Time) bool {
	n := officeTries(t, "")
	if n == 0 || n > officeMaxRetries || t.AttemptedAt == nil {
		return false
	}
	return now.Sub(*t.AttemptedAt) >= officeBackoff(n)
}

// officeSlots bounds how many documents this process has the document server
// convert at once. The bound is read at every acquire, so a changed setting
// applies to the next document.
type officeSlots struct {
	mu   sync.Mutex
	busy int
	free chan struct{}
}

func (s *officeSlots) acquire(ctx context.Context, limit int) error {
	if limit < 1 {
		limit = 1
	}
	for {
		s.mu.Lock()
		if s.busy < limit {
			s.busy++
			s.mu.Unlock()
			return nil
		}
		if s.free == nil {
			s.free = make(chan struct{})
		}
		wait := s.free
		s.mu.Unlock()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-wait:
		}
	}
}

func (s *officeSlots) release() {
	s.mu.Lock()
	s.busy--
	if s.free != nil {
		close(s.free)
		s.free = nil
	}
	s.mu.Unlock()
}

// drawOffice asks the document server to draw node (the `onlyoffice` handler
// of the chain). prev is the row before this render: its transient failures
// on this content count the try.
func (p *Pipeline) drawOffice(ctx context.Context, node *model.Node, drv storage.Driver, sig string, prev *model.Thumbnail) drawOutcome {
	if !p.officeReady(ctx) {
		return drawOutcome{state: "skipped", reason: noToolReason(ToolOffice)}
	}
	lim := p.OfficeLimits()
	if node.Size > lim.MaxBytes {
		return drawOutcome{state: "skipped", reason: skipReason(SkipOfficeTooLarge, lim.MaxBytes)}
	}
	attempt := officeTries(prev, sig) + 1
	retry := func(what string, err error) drawOutcome {
		return drawOutcome{state: "failed", reason: officeRetryReason(attempt, what), err: err}
	}
	// ⚠ Ciphertext never leaves filex: the head is read through openSource
	// (both encrypted magics) before the document server is asked to download
	// a byte. The fetch door refuses it as well - the second defence.
	rc, err := p.openSource(ctx, drv, node)
	if errors.Is(err, errEncryptedContent) {
		return drawOutcome{encrypted: true}
	}
	if err != nil {
		return retry("source", err)
	}
	_ = rc.Close()

	wctx, cancel := context.WithTimeout(ctx, officeSlotWait)
	err = p.officeSlot.acquire(wctx, lim.Slots)
	cancel()
	if err != nil {
		return retry("busy", err)
	}
	defer p.officeSlot.release()

	dctx, cancel := context.WithTimeout(ctx, officeTimeout)
	defer cancel()
	answer, err := p.office.DrawPage(dctx, OfficePage{
		NodeID: node.ID, StorageID: node.StorageID, Name: node.Name,
		ContentSig: sig, Attempt: attempt, MaxBytes: wire.ThumbnailMaxOutputBytes,
	})
	if err != nil {
		out := officeOutcome(err, attempt)
		slog.Debug("thumb: the document server did not draw", slog.Int64("node", node.ID),
			slog.String("path", node.Path), slog.String("reason", out.reason), slog.String("err", err.Error()))
		return out
	}
	img, err := decodeAnswer(answer, "the document server")
	if err != nil {
		return retry("image", err)
	}
	if err := p.writeJPEG(node.ID, scaleDown(img, thumbMaxWidth, thumbMaxHeight), thumbQuality); err != nil {
		return drawOutcome{state: "failed", reason: err.Error(), err: err}
	}
	return drawOutcome{state: "ready"}
}

// officeOutcome is what a failure of the document server's means for the
// row: the table at the top of this file.
func officeOutcome(err error, attempt int) drawOutcome {
	var oe *OfficeError
	if !errors.As(err, &oe) {
		return drawOutcome{state: "failed", reason: officeRetryReason(attempt, "failed"), err: err}
	}
	switch oe.Class {
	case OfficeCorrupt:
		return drawOutcome{state: "failed", reason: SkipOfficeCorrupt + ":" + cleanWhat(oe.What), err: err}
	case OfficePassword:
		return drawOutcome{state: "skipped", reason: SkipOfficePassword}
	case OfficeTooLarge:
		return drawOutcome{state: "skipped", reason: skipReason(SkipOfficeTooLarge, 0)}
	case OfficeEncrypted:
		return drawOutcome{encrypted: true}
	case OfficeUnconfigured:
		return drawOutcome{state: "skipped", reason: noToolReason(ToolOffice)}
	}
	return drawOutcome{state: "failed", reason: officeRetryReason(attempt, oe.What), err: err}
}

// String is for logs.
func (l OfficeLimits) String() string {
	return fmt.Sprintf("%d MB, %d at once", l.MaxBytes>>20, l.Slots)
}
