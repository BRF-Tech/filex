package thumb

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	_ "image/jpeg" // an app's answer may be a JPEG
	_ "image/png"  // or a PNG
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/brf-tech/filex/backend/internal/assoc"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/storage"
	"github.com/brf-tech/filex/backend/pkg/pluginkit/wire"
)

// ── Thumbnails drawn by apps (docs/thumbnails.md → Thumbnails drawn by apps)
//
// The pipeline does not know apps. It asks AppThumbs - the server's adapter
// over internal/assoc (who is in the chain, in which order) and the app
// registry (draw this file) - and keeps everything else: the e2e checks, the
// source bytes (openSource), the JPEG it writes and the row it records.

// AppThumbs is what the pipeline asks about thumbnails drawn by apps.
type AppThumbs interface {
	// ThumbChain is the handlers that draw a file of this name and type, in
	// the order they are asked (assoc.Service.Chain, capability thumbnail).
	ThumbChain(ctx context.Context, name, mime string) assoc.Chain
	// DrawThumbnail asks the app behind h to draw one file: a PNG or a JPEG,
	// or an *assoc.DrawError.
	DrawThumbnail(ctx context.Context, h assoc.Handler, req assoc.DrawRequest) ([]byte, error)
	// AppLimits are an app's limits now: the largest file it is sent, and the
	// time it has per file (to tell a row skipped for one of them that the
	// limit was raised). ok is false for an app that draws nothing now.
	AppLimits(app string) (maxBytes int64, timeout time.Duration, ok bool)
}

// AttachApps wires the apps that draw thumbnails. Nil (the runtime is off):
// filex's own drawer alone, as before 0.50.
func (p *Pipeline) AttachApps(a AppThumbs) { p.apps = a }

// SkipNoHandler is the reason of a file whose kind has handlers, every one of
// them switched off by the administrator.
const SkipNoHandler = "no_handler"

// The reasons an app's handler records (the row's error when it was the first
// in the chain, and its entry in `attempts` either way).
const (
	SkipAppFailed   = "app_failed"
	SkipAppTimeout  = "app_timeout"
	SkipAppTooLarge = "app_too_large"
)

// appReason spells what an app's draw ended in: `app_failed:<app>`,
// `app_timeout:<app>:<ms>`, `app_too_large:<app>:<bytes>`.
func appReason(de *assoc.DrawError) string {
	switch de.Code {
	case assoc.DrawTimeout:
		return SkipAppTimeout + ":" + de.App + ":" + strconv.FormatInt(de.Limit, 10)
	case assoc.DrawTooLarge:
		return SkipAppTooLarge + ":" + de.App + ":" + strconv.FormatInt(de.Limit, 10)
	}
	return SkipAppFailed + ":" + de.App
}

// ParseAppReason reads an app's reason back: its code (app_failed,
// app_timeout, app_too_large), the app, and the limit it records (0 for
// app_failed). ok is false for any other reason.
func ParseAppReason(reason string) (code, app string, limit int64, ok bool) {
	parts := strings.Split(reason, ":")
	switch {
	case len(parts) == 2 && parts[0] == SkipAppFailed && parts[1] != "":
		return parts[0], parts[1], 0, true
	case len(parts) == 3 && (parts[0] == SkipAppTimeout || parts[0] == SkipAppTooLarge) && parts[1] != "":
		n, err := strconv.ParseInt(parts[2], 10, 64)
		if err != nil {
			return "", "", 0, false
		}
		return parts[0], parts[1], n, true
	}
	return "", "", 0, false
}

// Attempt is one handler asked about a file, as the row's `attempts` keeps
// it: the handler (assoc.Handler.Key: `builtin`, `app:<name>@<version>`) and
// what it answered ("ok", or the reason).
type Attempt struct {
	H string `json:"h"`
	R string `json:"r"`
}

func encodeAttempts(a []Attempt) string {
	if a == nil {
		a = []Attempt{}
	}
	b, _ := json.Marshal(a)
	return string(b)
}

// ParseAttempts reads a row's `attempts` back; nil for a row from before 0.50
// (or one that cannot be read).
func ParseAttempts(s string) []Attempt {
	if s == "" {
		return nil
	}
	var out []Attempt
	if err := json.Unmarshal([]byte(s), &out); err != nil {
		return nil
	}
	if out == nil {
		out = []Attempt{}
	}
	return out
}

// BuiltinDraws reports whether filex draws a file of this name and type
// itself: an image (SVG and HEIC included), a video, an audio file, a PDF,
// a text, an archive it lists. It does not say the tool is installed: a kind
// whose program is missing is still the built-in drawer's, which skips it
// with the reason (no_tool). Office documents are not: the OnlyOffice
// document server draws them (OnlyOfficeDraws, office.go).
func BuiltinDraws(name, mime string) bool {
	return builtinDraws(&model.Node{Name: name, Mime: mime})
}

// chainFor is the chain of handlers that draw n. Without apps attached it is
// the default order of filex's own: the document server for an office kind
// while OnlyOffice is configured, filex's own drawer for the kinds it draws,
// and nothing for the rest (assoc.ThumbDefault).
func (p *Pipeline) chainFor(ctx context.Context, n *model.Node) assoc.Chain {
	mime := routeMime(n)
	if p.apps == nil {
		return assoc.Chain{On: assoc.ThumbDefault(officeKind(n) && p.officeReady(ctx), builtinDraws(n), nil)}
	}
	return p.apps.ThumbChain(ctx, n.Name, mime)
}

// appDraws reports whether an app is on in n's chain (so a listing draws a
// kind filex itself would only give the placeholder card).
func (p *Pipeline) appDraws(n *model.Node) bool {
	if p.apps == nil {
		return false
	}
	for _, h := range p.chainFor(context.Background(), n).On {
		if h.IsApp() {
			return true
		}
	}
	return false
}

// drawApp asks an app to draw node: it is handed the file's bytes (through
// openSource, so a staged upload is read and ciphertext is caught), and its
// answer is checked, scaled and written as filex's own JPEG.
//
// ⚠ It never takes the process down: a panic anywhere below (the runtime, a
// host function, an image decoder fed an app's answer) is the APP failing
// this file - recorded as app_failed, and the next handler is asked - not a
// crash of the goroutine that draws thumbnails, which nothing above recovers.
func (p *Pipeline) drawApp(ctx context.Context, node *model.Node, drv storage.Driver, h assoc.Handler) (out drawOutcome) {
	defer func() {
		if r := recover(); r != nil {
			slog.Error("thumb: an app's thumbnail panicked", slog.String("app", h.App), slog.Int64("node", node.ID), slog.Any("panic", r))
			out = drawOutcome{state: "skipped", reason: appReason(&assoc.DrawError{App: h.App, Code: assoc.DrawFailed, Err: fmt.Errorf("panic: %v", r)})}
		}
	}()
	rc, err := p.openSource(ctx, drv, node)
	if errors.Is(err, errEncryptedContent) {
		return drawOutcome{encrypted: true}
	}
	if err != nil {
		return drawOutcome{state: "failed", reason: err.Error(), err: err}
	}
	defer rc.Close()
	answer, err := p.apps.DrawThumbnail(ctx, h, assoc.DrawRequest{
		NodeID: node.ID, StorageID: node.StorageID, Path: node.Path,
		Name: node.Name, Mime: routeMime(node), Size: node.Size, Body: rc,
	})
	if err != nil {
		de := assoc.AsDrawError(h.App, err)
		slog.Debug("thumb: an app did not draw", slog.String("app", h.App), slog.Int64("node", node.ID), slog.String("err", err.Error()))
		return drawOutcome{state: "skipped", reason: appReason(de)}
	}
	img, err := decodeAnswer(answer, "the app")
	if err != nil {
		slog.Debug("thumb: an app's answer was refused", slog.String("app", h.App), slog.Int64("node", node.ID), slog.String("err", err.Error()))
		return drawOutcome{state: "skipped", reason: appReason(&assoc.DrawError{App: h.App, Code: assoc.DrawFailed, Err: err})}
	}
	if err := p.writeJPEG(node.ID, scaleDown(img, thumbMaxWidth, thumbMaxHeight), thumbQuality); err != nil {
		return drawOutcome{state: "failed", reason: err.Error(), err: err}
	}
	return drawOutcome{state: "ready"}
}

// decodeAnswer checks a picture another program drew (an app's answer, the
// document server's) before a pixel of it is decoded: at most
// wire.ThumbnailMaxOutputBytes, a PNG or a JPEG by its own header, at most
// wire.ThumbnailMaxPixels on each side. A decoder handed a header that
// promises 60 000 x 60 000 pixels would allocate them first and fail later.
// who names the drawer in the error.
func decodeAnswer(b []byte, who string) (image.Image, error) {
	if len(b) == 0 {
		return nil, fmt.Errorf("%s answered no image", who)
	}
	if len(b) > wire.ThumbnailMaxOutputBytes {
		return nil, fmt.Errorf("%s answered an image of %d bytes, over %d", who, len(b), wire.ThumbnailMaxOutputBytes)
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(b))
	if err != nil {
		return nil, fmt.Errorf("the image %s answered cannot be read: %w", who, err)
	}
	if format != "png" && format != "jpeg" {
		return nil, fmt.Errorf("%s answered %s; a thumbnail is a PNG or a JPEG", who, format)
	}
	if cfg.Width <= 0 || cfg.Height <= 0 || cfg.Width > wire.ThumbnailMaxPixels || cfg.Height > wire.ThumbnailMaxPixels {
		return nil, fmt.Errorf("%s answered %dx%d; at most %dx%d", who, cfg.Width, cfg.Height, wire.ThumbnailMaxPixels, wire.ThumbnailMaxPixels)
	}
	img, _, err := image.Decode(bytes.NewReader(b))
	if err != nil {
		return nil, fmt.Errorf("the image %s answered cannot be decoded: %w", who, err)
	}
	return img, nil
}

// askedAnApp reports whether the row records an app among the handlers asked
// (who drew it, or one that failed before).
func askedAnApp(t *model.Thumbnail) bool {
	if t == nil {
		return false
	}
	if strings.HasPrefix(t.Generator, "app:") {
		return true
	}
	for _, a := range ParseAttempts(t.Attempts) {
		if strings.HasPrefix(a.H, "app:") {
			return true
		}
	}
	return false
}

// isE2ESkip: the row was skipped because the file is end-to-end encrypted -
// nothing any handler could draw, so who is in the chain does not matter.
func isE2ESkip(reason string) bool { return strings.HasPrefix(reason, "e2e-encrypted") }

// handlersChanged reports whether the answer for n would now come from
// somebody else than the row records (docs/thumbnails.md → Thumbnails drawn
// by apps → Freshness follows the chain):
//
//   - a ready row: any handler up to and including the one that drew it is
//     different now (in front of it, switched off, removed, upgraded); one
//     added after it changes nothing;
//   - a failed or skipped row: the chain is different in any way;
//   - a row with no `attempts` (drawn before 0.50, or the placeholder card):
//     as if the built-in drawer had drawn it - stale when it is no longer
//     first for a kind it draws, or when somebody draws a kind it does not.
func (p *Pipeline) handlersChanged(n *model.Node, t *model.Thumbnail) bool {
	if t.State == "skipped" && isE2ESkip(t.Error) {
		return false
	}
	chain := p.chainFor(context.Background(), n)
	cur := chain.Keys()
	// ⚠ An office document that nobody here can draw now (OnlyOffice is not
	// configured, or no longer) is left as it is: a picture is KEPT - drawing
	// it again would only replace it with "no_tool:office" - and a row
	// without one would only record the same reason again. That covers the
	// pages LibreOffice drew before 0.50. Once OnlyOffice is configured the
	// chain names it and the row is stale then (below); the redraw goes one
	// document at a time (office.go, the slots). An administrator who
	// switched every handler off for the kind (AllOff) asked for no picture,
	// and gets none.
	if len(cur) == 0 && !chain.AllOff() && officeKind(n) {
		return false
	}
	if t.Attempts == "" {
		// Before 0.50 filex's own drawer drew every row, office documents
		// included (LibreOffice).
		if drawsContent(n) {
			return len(cur) == 0 || cur[0] != assoc.Builtin
		}
		return len(cur) > 0
	}
	trail := ParseAttempts(t.Attempts)
	if t.State == "ready" {
		if len(cur) < len(trail) {
			return true
		}
		for i := range trail {
			if cur[i] != trail[i].H {
				return true
			}
		}
		return false
	}
	if len(cur) != len(trail) {
		return true
	}
	for i := range trail {
		if cur[i] != trail[i].H {
			return true
		}
	}
	return false
}

// appLimitRaised: an app was not sent the file because it was too large, or
// ran out of time on it, and the administrator has raised that limit since.
// The row's own reason and every entry of its attempts are read: the app may
// have been second in the chain.
func (p *Pipeline) appLimitRaised(n *model.Node, t *model.Thumbnail) bool {
	if p.apps == nil {
		return false
	}
	reasons := []string{t.Error}
	for _, a := range ParseAttempts(t.Attempts) {
		reasons = append(reasons, a.R)
	}
	for _, r := range reasons {
		code, app, was, ok := ParseAppReason(r)
		if !ok || code == SkipAppFailed {
			continue
		}
		maxBytes, timeout, live := p.apps.AppLimits(app)
		if !live {
			continue
		}
		switch code {
		case SkipAppTooLarge:
			if maxBytes > was && n.Size <= maxBytes {
				return true
			}
		case SkipAppTimeout:
			if timeout.Milliseconds() > was {
				return true
			}
		}
	}
	return false
}
