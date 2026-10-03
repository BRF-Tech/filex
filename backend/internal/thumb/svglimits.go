package thumb

import (
	"context"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/brf-tech/filex/backend/internal/dbsetting"
	"github.com/brf-tech/filex/backend/internal/model"
)

// The two limits an SVG thumbnail is drawn under (GitHub #79: "set a limit,
// e.g. 5 MB; I have very complex SVGs and the preview takes forever").
// Administrators change them in Settings and in Admin → Tools → Thumbnail
// repair; a file over either is `skipped` with the limit it hit, shows its
// SVG icon, is listed with the reason on the repair tab, and is drawn again
// once the limit is raised (Assess and Wanted read the limit back).
//
// Why 5 MB and 10 s: of 54 real-world SVGs measured (docs/thumbnails.md,
// Design notes: SVG) the largest was 1.1 MB and the slowest took 0.93 s in
// the built-in engine — ten times under the time limit, with room for a
// machine several times slower. A file past either is not a picture anyone
// waits for a thumbnail of: 14 MB of machine-made paths took over twenty
// seconds, and every second a render takes is a second one of the few
// background workers is not drawing the next file.
var (
	// FolderPreviewsSetting: folders show their pictures (the folder card
	// drawn with up to three prints rising out of it, and the list of what
	// it holds on a resting pointer). On by default; an administrator turns
	// it off in Settings or on the repair tab, and the grid keeps the classic
	// folder cards.
	FolderPreviewsSetting = dbsetting.BoolSpec{
		Key: "thumbs.folder_previews", EnvVar: "FILEX_THUMBS_FOLDER_PREVIEWS", Default: true,
	}
	SVGMaxMBSetting = dbsetting.IntSpec{
		Key: "thumbs.svg_max_mb", EnvVar: "FILEX_THUMBS_SVG_MAX_MB",
		Default: 5, Min: 1, Max: 64, Unit: "MB",
	}
	SVGTimeoutSetting = dbsetting.IntSpec{
		Key: "thumbs.svg_timeout_seconds", EnvVar: "FILEX_THUMBS_SVG_TIMEOUT",
		Default: 10, Min: 1, Max: 120, Unit: "seconds",
	}
)

// Skip reasons for an SVG over a limit, recorded as "<code>:<limit>" so the
// row says which limit it was over (bytes, or milliseconds).
const (
	SkipSVGTooLarge = "svg_too_large"
	SkipSVGTimeout  = "svg_timeout"
)

// SVGLimits is what an SVG is drawn under right now.
type SVGLimits struct {
	MaxBytes int64
	Timeout  time.Duration
}

// DefaultSVGLimits is what applies with no settings store (tests, the CLI
// before the store is wired).
func DefaultSVGLimits() SVGLimits {
	return SVGLimits{MaxBytes: int64(SVGMaxMBSetting.Default) << 20, Timeout: time.Duration(SVGTimeoutSetting.Default) * time.Second}
}

// ResolveSVGLimits reads the two settings.
func ResolveSVGLimits(ctx context.Context, g dbsetting.Getter) SVGLimits {
	return SVGLimits{
		MaxBytes: int64(SVGMaxMBSetting.Resolve(ctx, g)) << 20,
		Timeout:  time.Duration(SVGTimeoutSetting.Resolve(ctx, g)) * time.Second,
	}
}

// svgSettings caches the thumbnail settings for a few seconds: a render reads
// the SVG limits and every listing reads the folder switch, and neither may be
// a settings query per file or per listing. A change on the settings page
// applies within svgLimitsTTL, or at once (ForgetSettings).
type svgSettings struct {
	g              dbsetting.Getter
	mu             sync.Mutex
	at             time.Time
	lim            SVGLimits
	office         OfficeLimits
	folderPreviews bool
}

const svgLimitsTTL = 5 * time.Second

// AttachSettings wires the settings store the SVG limits are read from.
func (p *Pipeline) AttachSettings(g dbsetting.Getter) {
	p.settings = &svgSettings{g: g}
}

// SVGLimits is what an SVG is drawn under right now.
func (p *Pipeline) SVGLimits() SVGLimits {
	if p == nil || p.settings == nil || p.settings.g == nil {
		return DefaultSVGLimits()
	}
	return p.settings.current().lim
}

// FolderPreviews reports whether folders show their pictures (the folder
// card, and the list on a resting pointer): FolderPreviewsSetting, cached.
func (p *Pipeline) FolderPreviews() bool {
	if p == nil || p.settings == nil || p.settings.g == nil {
		return FolderPreviewsSetting.Default
	}
	return p.settings.current().folderPreviews
}

// current is the cached settings, read again once svgLimitsTTL has passed.
func (s *svgSettings) current() *svgSettings {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.at.IsZero() || time.Since(s.at) > svgLimitsTTL {
		ctx := context.Background()
		s.lim = ResolveSVGLimits(ctx, s.g)
		s.office = ResolveOfficeLimits(ctx, s.g)
		s.folderPreviews = FolderPreviewsSetting.Resolve(ctx, s.g)
		s.at = time.Now()
	}
	return s
}

// ForgetSettings drops the cached settings: the settings page calls it after
// a change, so the next file is drawn, and the next folder listed, under the
// new ones.
func (p *Pipeline) ForgetSettings() {
	if p == nil || p.settings == nil {
		return
	}
	p.settings.mu.Lock()
	p.settings.at = time.Time{}
	p.settings.mu.Unlock()
}

func skipReason(code string, limit int64) string { return code + ":" + strconv.FormatInt(limit, 10) }

// ParseSkip reads a skip reason written by skipReason back: its code and the
// limit it records (svg_too_large, svg_timeout, oo_too_large; an
// oo_too_large of 0 is the document server's own limit). A reason of another
// shape comes back as ("", 0).
func ParseSkip(reason string) (code string, limit int64) {
	c, v, ok := strings.Cut(reason, ":")
	if !ok || (c != SkipSVGTooLarge && c != SkipSVGTimeout && c != SkipOfficeTooLarge) {
		return "", 0
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		return "", 0
	}
	return c, n
}

// limitRaised: the row was skipped for being over an SVG limit, and the
// limit in force now would let the file through.
func (p *Pipeline) limitRaised(n *model.Node, t *model.Thumbnail) bool {
	code, was := ParseSkip(t.Error)
	switch code {
	case SkipSVGTooLarge:
		now := p.SVGLimits().MaxBytes
		return now > was && n.Size <= now
	case SkipSVGTimeout:
		return p.SVGLimits().Timeout.Milliseconds() > was
	case SkipOfficeTooLarge:
		// 0 is the document server's own limit, which filex cannot see move.
		now := p.OfficeLimits().MaxBytes
		return was > 0 && now > was && n.Size <= now
	}
	return false
}
