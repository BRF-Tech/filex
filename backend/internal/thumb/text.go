package thumb

import (
	"context"
	"io"
	"strings"

	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/storage"
)

// A text file's thumbnail is its own first lines, not the extension's
// placeholder card: a folder shows it among its newest files, and a card
// that reads "Minutes, April / 1. Budget ..." says which file it is where
// "TXT" says nothing. The explorer's own text cards draw the same thing in
// the browser (docs/thumbnails.md, Generators).
//
// ⚠ Only the kinds named here, by extension: a source tree has thousands of
// code files, and drawing each of them in the background would be work for
// nobody (the views draw their first lines themselves).
var textThumbExts = map[string]bool{"txt": true, "md": true, "markdown": true, "text": true}

// textReadLimit is how much of the file is read: a page of lines is a few
// hundred bytes, and a file of one enormous line is cut here.
const textReadLimit = 8 << 10

func isTextThumb(name string) bool {
	return textThumbExts[strings.TrimPrefix(strings.ToLower(extOf(name)), ".")]
}

func (p *Pipeline) generateText(ctx context.Context, node *model.Node, drv storage.Driver) error {
	rc, err := p.openSource(ctx, drv, node)
	if err != nil {
		return err
	}
	defer rc.Close()
	buf, err := io.ReadAll(io.LimitReader(rc, textReadLimit))
	if err != nil {
		return err
	}
	img, err := drawLines("", textLines(string(buf), isMarkdown(node.Name)))
	if err != nil {
		return err
	}
	return p.writeJPEG(node.ID, img, thumbQuality)
}

func isMarkdown(name string) bool {
	e := strings.ToLower(extOf(name))
	return e == ".md" || e == ".markdown"
}

// textLines turns the head of a text file into the lines drawn: valid UTF-8,
// tabs as four spaces, control characters dropped, blank lines at either end gone.
// In Markdown a heading is drawn bold, without its #.
func textLines(s string, markdown bool) []textLine {
	s = strings.ToValidUTF8(s, "�")
	s = strings.ReplaceAll(strings.ReplaceAll(s, "\r\n", "\n"), "\r", "\n")
	var out []textLine
	for _, raw := range strings.Split(s, "\n") {
		ln := strings.Map(func(r rune) rune {
			if r == '\t' {
				return ' '
			}
			if r < 0x20 || r == 0x7F {
				return -1
			}
			return r
		}, strings.ReplaceAll(raw, "\t", "    "))
		ln = strings.TrimRight(ln, " ")
		if len(out) == 0 && strings.TrimSpace(ln) == "" {
			continue
		}
		bold := false
		if markdown {
			if t := strings.TrimLeft(ln, "#"); t != ln && (t == "" || t[0] == ' ') {
				ln, bold = strings.TrimSpace(t), true
			}
		}
		out = append(out, textLine{Text: ln, Bold: bold})
		if len(out) >= 16 {
			break
		}
	}
	for len(out) > 0 && strings.TrimSpace(out[len(out)-1].Text) == "" {
		out = out[:len(out)-1]
	}
	return out
}
