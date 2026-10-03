package thumb

import (
	"context"
	"strings"

	"github.com/brf-tech/filex/backend/internal/enginebin"
	"github.com/brf-tech/filex/backend/internal/model"
)

// A file whose kind needs an external program that this install does not
// have is recorded as `skipped` with the reason "no_tool:<kind>", not as a
// failure and not as the placeholder card: nothing is wrong with the file,
// the Thumbnail repair tab says which program is missing, and once it is
// installed (the probe runs at boot) the next listing, or a Fix repair, draws
// the file (toolReturned).
//
// ⚠ Before 0.50 such a file got the extension's placeholder card as `ready`,
// which said nothing about why and was never drawn again once the tool
// arrived.

// SkipNoTool is the reason prefix for a kind whose program is missing.
const SkipNoTool = "no_tool"

// The kinds a missing program can leave undrawn, and what each needs.
const (
	ToolVideo = "video" // FFmpeg
	ToolAudio = "audio" // FFmpeg
	ToolPDF   = "pdf"   // Ghostscript or poppler
	// ToolOffice: OnlyOffice is not configured (office.go). Not a program on
	// this machine: the document server filex is configured with.
	ToolOffice = "office"
	ToolHEIF   = "heif" // ImageMagick with HEIC/AVIF support (libheif)
	// ToolHEICCodec: ImageMagick is there and cannot decode a HEIC photo -
	// libheif without its HEVC decoder plugin (libheif-plugin-libde265 on
	// Debian and Ubuntu, libheif-libde265 on Alpine). Measured, not assumed:
	// enginebin.HEIC draws a sample. AVIF is not affected (another decoder).
	ToolHEICCodec = "heic_codec"
)

func noToolReason(kind string) string { return SkipNoTool + ":" + kind }

// ParseNoTool reads a "no_tool:<kind>" reason back.
func ParseNoTool(reason string) (kind string, ok bool) {
	c, k, found := strings.Cut(reason, ":")
	if !found || c != SkipNoTool || k == "" {
		return "", false
	}
	return k, true
}

// toolKind is the program a type needs, "" when it needs none (images Go
// decodes, SVG, the placeholder card).
func toolKind(mime string) string {
	switch {
	case isHEIF(mime):
		return ToolHEIF
	case strings.HasPrefix(mime, "video/"):
		return ToolVideo
	case strings.HasPrefix(mime, "audio/"):
		return ToolAudio
	case mime == "application/pdf":
		return ToolPDF
	}
	return ""
}

// missingTool is the kind whose program this install lacks for a file of
// this type ("no_tool:<kind>"), "" when it has all the type needs. A HEIC
// needs ImageMagick first (heif), then a HEVC decoder under it (heic_codec).
func (p *Pipeline) missingTool(mime string) string {
	kind := toolKind(mime)
	switch {
	case kind == "":
		return ""
	case !p.hasTool(kind):
		return kind
	case kind == ToolHEIF && isHEVC(mime) && !p.hasTool(ToolHEICCodec):
		return ToolHEICCodec
	}
	return ""
}

// hasTool reports whether this install has what kind needs.
func (p *Pipeline) hasTool(kind string) bool {
	switch kind {
	case ToolHEICCodec:
		// enginebin.HEIC: the server's one cached answer, read for every HEIC
		// file, so an ImageMagick that changed is seen without a new pipeline.
		return p.caps.HEIF && enginebin.HEIC().Decodes
	case ToolVideo:
		return p.caps.Video
	case ToolAudio:
		return p.caps.Audio
	case ToolPDF:
		return p.caps.PDF
	case ToolOffice:
		// The document server draws the page; nothing here is needed.
		return p.officeReady(context.Background())
	case ToolHEIF:
		return p.caps.HEIF
	}
	return true
}

// toolReturned: the row was skipped for want of a program this install has
// now.
func (p *Pipeline) toolReturned(t *model.Thumbnail) bool {
	kind, ok := ParseNoTool(t.Error)
	return ok && p.hasTool(kind)
}

// isHEIF: the HEIF family (HEIC photos, AVIF), which Go cannot decode.
func isHEIF(mime string) bool {
	switch mime {
	case "image/heic", "image/heif", "image/heic-sequence", "image/heif-sequence", "image/avif":
		return true
	}
	return false
}

// isHEVC: the HEIF types whose picture is HEVC, which libheif decodes through
// its libde265 plugin. AVIF (AV1) has a decoder of its own.
func isHEVC(mime string) bool {
	return isHEIF(mime) && mime != "image/avif"
}
