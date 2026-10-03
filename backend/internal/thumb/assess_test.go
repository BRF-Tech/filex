package thumb

import (
	"testing"
	"time"

	"github.com/brf-tech/filex/backend/internal/model"
)

// The freshness rule (docs/thumbnails.md, Design notes). Every row of the
// table in the document is a case here, and the cases say WHY in their name:
// a listing that re-renders too eagerly burns CPU on every page view, one that
// is too shy leaves a changed file with its old picture for good (0.49).

func at(s int) *time.Time {
	t := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC).Add(time.Duration(s) * time.Second)
	return &t
}

func fileWith(size int64, etag string, mtime *time.Time) *model.Node {
	return &model.Node{ID: 7, StorageID: 1, Name: "a.png", Path: "/a.png", Type: model.NodeTypeFile,
		Size: size, Etag: etag, BackendMtime: mtime}
}

// svgFile is an SVG as an upload or the local driver records it: the content
// sniff has no signature for SVG and says text/xml.
func svgFile(mtime *time.Time) *model.Node {
	return &model.Node{ID: 8, StorageID: 1, Name: "logo.svg", Path: "/logo.svg", Type: model.NodeTypeFile,
		Size: 300, Mime: "text/xml; charset=utf-8", BackendMtime: mtime}
}

func TestAssess_TheFreshnessTable(t *testing.T) {
	now := *at(3600)
	n := fileWith(100, "", at(0))
	sig := n.ContentFingerprint()
	longAgo := at(0)
	p := New(nil, "", Capabilities{Image: true, SVG: true, Video: true})

	cases := []struct {
		name string
		node *model.Node
		row  *model.Thumbnail
		want Verdict
	}{
		{"no row: never drawn (a synced file, issue #79)", n, nil, Render},
		{"ready and the same content: nothing to do", n,
			&model.Thumbnail{State: "ready", StorageKey: "k", SourceSig: sig, AttemptedAt: longAgo, GeneratedAt: longAgo}, Leave},
		{"ready but drawn from other content: render again", n,
			&model.Thumbnail{State: "ready", StorageKey: "k", SourceSig: "90:0", AttemptedAt: longAgo, GeneratedAt: longAgo}, Render},
		{"ready before 0.50 (no signature), file modified after the render: render again", fileWith(100, "", at(120)),
			&model.Thumbnail{State: "ready", StorageKey: "k", GeneratedAt: at(60)}, Render},
		{"ready before 0.50 (no signature), file older than the render: leave it", fileWith(100, "", at(30)),
			&model.Thumbnail{State: "ready", StorageKey: "k", GeneratedAt: at(60)}, Leave},
		{"ready before 0.50, same second as the render: leave it (mtime granularity)", fileWith(100, "", at(60)),
			&model.Thumbnail{State: "ready", StorageKey: "k", GeneratedAt: at(60)}, Leave},
		{"failed on this content: do not hammer it", n,
			&model.Thumbnail{State: "failed", SourceSig: sig, AttemptedAt: longAgo}, Leave},
		{"failed on other content: this content may draw", n,
			&model.Thumbnail{State: "failed", SourceSig: "90:0", AttemptedAt: longAgo}, Render},
		{"failed before 0.50: the repair tool's job, not every listing's", n,
			&model.Thumbnail{State: "failed"}, Leave},
		{"skipped (encrypted) on this content: leave it", n,
			&model.Thumbnail{State: "skipped", Error: "e2e-encrypted content", SourceSig: sig, AttemptedAt: longAgo}, Leave},
		{"skipped for want of an SVG engine that is there now", n,
			&model.Thumbnail{State: "skipped", Error: SkipNoSVGEngine, SourceSig: sig, AttemptedAt: longAgo}, Render},
		{"skipped by 0.49 because rsvg-convert was missing: there is an engine now", n,
			&model.Thumbnail{State: "skipped", Error: legacySkipNoRsvg}, Render},
		{"pending left by a crash", n,
			&model.Thumbnail{State: "pending", AttemptedAt: longAgo}, Render},
		{"pending from before 0.50 (no attempt time)", n,
			&model.Thumbnail{State: "pending"}, Render},
		{"pending and started moments ago: somebody is drawing it", n,
			&model.Thumbnail{State: "pending", AttemptedAt: at(3590)}, Leave},
		{"stale but attempted 20 s ago: the loop guard holds", n,
			&model.Thumbnail{State: "ready", StorageKey: "k", SourceSig: "90:0", AttemptedAt: at(3580)}, Leave},
		{"stale and attempted 2 min ago: the guard has passed", n,
			&model.Thumbnail{State: "ready", StorageKey: "k", SourceSig: "90:0", AttemptedAt: at(3480)}, Render},
		{"a folder has no thumbnail of its own", &model.Node{Type: model.NodeTypeDirectory}, nil, Leave},
		{"a trashed file is not drawn", &model.Node{Type: model.NodeTypeFile, DeletedAt: at(0)}, nil, Leave},
		{"an upload still in transfer is the upload's to draw", &model.Node{Type: model.NodeTypeFile, TransferState: "staged"}, nil, Leave},
		{"an archive is drawn as the list of what is in it", &model.Node{Type: model.NodeTypeFile, Name: "yedek.zip"}, nil, Render},
		{"a text file is drawn as its first lines", &model.Node{Type: model.NodeTypeFile, Name: "notlar.txt"}, nil, Render},
		{"a code file's card is only its extension: not drawn from a listing", &model.Node{Type: model.NodeTypeFile, Name: "main.go", Mime: "text/plain; charset=utf-8"}, nil, Leave},
		{"a 3D model's card is only its extension: not drawn from a listing", &model.Node{Type: model.NodeTypeFile, Name: "kup.glb"}, nil, Leave},
		{"a PDF is drawn", &model.Node{Type: model.NodeTypeFile, Name: "rapor.pdf"}, nil, Render},
		{"a song gets its waveform", &model.Node{Type: model.NodeTypeFile, Name: "şarkı.mp3"}, nil, Render},
		{"a picture known by its MIME alone", &model.Node{Type: model.NodeTypeFile, Name: "adsız", Mime: "image/png"}, nil, Render},
		{"an SVG the upload sniffed as XML is still an SVG", &model.Node{Type: model.NodeTypeFile, Name: "logo.svg", Mime: "text/xml; charset=utf-8"}, nil, Render},
		{"an SVG the local driver sniffed as text is still an SVG", &model.Node{Type: model.NodeTypeFile, Name: "logo.svg", Mime: "text/plain; charset=utf-8"}, nil, Render},
		{"an XML file is a text, not a picture", &model.Node{Type: model.NodeTypeFile, Name: "veri.xml", Mime: "text/xml; charset=utf-8"}, nil, Leave},
		{"a sniff that could not name the file defers to the extension, .png too", &model.Node{Type: model.NodeTypeFile, Name: "sahte.png", Mime: "text/plain; charset=utf-8"}, nil, Render},
		{"a Word 97 document the sniff called octet-stream is a document", &model.Node{Type: model.NodeTypeFile, Name: "rapor.doc", Mime: "application/octet-stream"}, nil, Render},
		{"a text the sniff recorded as text/plain is still drawn as its lines", &model.Node{Type: model.NodeTypeFile, Name: "notlar.txt", Mime: "text/plain; charset=utf-8"}, nil, Render},
		{"skipped for want of FFmpeg, and FFmpeg is here now", n,
			&model.Thumbnail{State: "skipped", Error: "no_tool:video", SourceSig: sig, AttemptedAt: longAgo}, Render},
		{"skipped for want of a program that is still missing", n,
			&model.Thumbnail{State: "skipped", Error: "no_tool:office", SourceSig: sig, AttemptedAt: longAgo}, Leave},
		{"an SVG drawn before 0.50 was the placeholder card: drawn once more", svgFile(at(30)),
			&model.Thumbnail{State: "ready", StorageKey: "k", GeneratedAt: at(60)}, Render},
		{"an SVG drawn since 0.50, same content: leave it", svgFile(at(30)),
			&model.Thumbnail{State: "ready", StorageKey: "k", SourceSig: svgFile(at(30)).ContentFingerprint(), AttemptedAt: longAgo, GeneratedAt: at(60)}, Leave},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := p.Assess(c.node, c.row, now); got != c.want {
				t.Fatalf("Assess = %v, want %v", got, c.want)
			}
		})
	}
}

// Without an SVG engine, a row skipped for want of one stays skipped: asking
// again would only skip again, on every listing.
func TestAssess_NoEngineStillNoEngine(t *testing.T) {
	p := New(nil, "", Capabilities{Image: true, SVG: false})
	n := fileWith(100, "", at(0))
	row := &model.Thumbnail{State: "skipped", Error: SkipNoSVGEngine, SourceSig: n.ContentFingerprint(), AttemptedAt: at(0)}
	if got := p.Assess(n, row, *at(3600)); got != Leave {
		t.Fatalf("Assess = %v, want Leave", got)
	}
}

// The etag wins over size and time: an S3 object rewritten with the same size
// in the same second is still a different object.
func TestAssess_EtagDecides(t *testing.T) {
	p := New(nil, "", Capabilities{Image: true})
	n := fileWith(100, `"abc"`, at(0))
	row := &model.Thumbnail{State: "ready", StorageKey: "k", SourceSig: `"abd"`, AttemptedAt: at(0)}
	if got := p.Assess(n, row, *at(3600)); got != Render {
		t.Fatalf("Assess = %v, want Render", got)
	}
}

// A nil pipeline (thumbnails switched off) never asks for anything.
func TestAssess_NilPipeline(t *testing.T) {
	var p *Pipeline
	if got := p.Assess(fileWith(1, "", nil), nil, time.Now()); got != Leave {
		t.Fatalf("Assess = %v, want Leave", got)
	}
}

// The repair tool and `filex thumb backfill` choose files with Wanted: no
// loop guard, and the selection decides what else is drawn besides the
// missing and the pending.
func TestWanted_TheRepairSelection(t *testing.T) {
	p := New(nil, "", Capabilities{Image: true, SVG: true})
	n := fileWith(100, "", at(0))
	sig := n.ContentFingerprint()
	recent := at(3595)
	fresh := &model.Thumbnail{State: "ready", StorageKey: "k", SourceSig: sig, AttemptedAt: recent}
	stale := &model.Thumbnail{State: "ready", StorageKey: "k", SourceSig: "90:0", AttemptedAt: recent}
	failed := &model.Thumbnail{State: "failed", SourceSig: sig, AttemptedAt: recent}
	skipped := &model.Thumbnail{State: "skipped", Error: "e2e-encrypted content", SourceSig: sig, AttemptedAt: recent}
	oldSVG := &model.Thumbnail{State: "skipped", Error: legacySkipNoRsvg}
	pending := &model.Thumbnail{State: "pending", AttemptedAt: recent}

	cases := []struct {
		name string
		row  *model.Thumbnail
		sel  Selection
		want bool
	}{
		{"missing, any selection", nil, Selection{}, true},
		{"pending, any selection (an administrator asked)", pending, Selection{}, true},
		{"fresh, fix", fresh, Fix, false},
		{"fresh, rebuild", fresh, Selection{All: true}, true},
		{"stale, fix: no loop guard for an explicit ask", stale, Fix, true},
		{"stale, the CLI with --stale=false", stale, Selection{}, false},
		{"failed, fix", failed, Fix, true},
		{"failed, default CLI", failed, Selection{Stale: true}, false},
		{"skipped, fix", skipped, Fix, true},
		{"skipped, default CLI", skipped, Selection{Stale: true}, false},
		{"skipped by 0.49 for want of rsvg-convert: drawn by default", oldSVG, Selection{Stale: true}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := p.Wanted(n, c.row, c.sel); got != c.want {
				t.Fatalf("Wanted = %v, want %v", got, c.want)
			}
		})
	}
	placeholderSVG := &model.Thumbnail{State: "ready", StorageKey: "k", GeneratedAt: at(60)}
	if !p.Wanted(svgFile(at(30)), placeholderSVG, Fix) {
		t.Fatal("Fix leaves an SVG drawn before 0.50 as the placeholder card")
	}
	if p.Wanted(fileWith(100, "", at(30)), placeholderSVG, Fix) {
		t.Fatal("Fix draws a PNG drawn before 0.50 that did not change")
	}
	if p.Wanted(&model.Node{Type: model.NodeTypeDirectory}, nil, Selection{All: true}) {
		t.Fatal("a folder is never drawn")
	}
}

// routeMime: what the pipeline draws a file as. The catalogue's type is a
// content sniff, which names a handful of formats; what it cannot name, and
// the containers that do not say what is in them, go by the extension.
func TestRouteMime(t *testing.T) {
	cases := []struct{ name, mime, want string }{
		{"logo.svg", "text/xml; charset=utf-8", "image/svg+xml"},
		{"logo.svg", "text/plain; charset=utf-8", "image/svg+xml"},
		{"rapor.doc", "application/octet-stream", "application/msword"},
		{"tablo.xls", "application/octet-stream", "application/vnd.ms-excel"},
		{"sunum.ppt", "application/octet-stream", "application/vnd.ms-powerpoint"},
		{"IMG_0001.HEIC", "application/octet-stream", "image/heic"},
		{"kapak.avif", "application/octet-stream", "image/avif"},
		{"tatil.mov", "application/octet-stream", "video/quicktime"},
		{"şarkı.m4a", "video/mp4", "audio/mp4"},
		{"şarkı.ogg", "application/ogg", "audio/ogg"},
		{"klip.ogv", "application/ogg", "video/ogg"},
		{"mektup.docx", "application/zip", "application/vnd.openxmlformats-officedocument.wordprocessingml.document"},
		{"s3.jpg", "binary/octet-stream", "image/jpeg"},
		{"adsız", "", ""},
		{"x.png", "", "image/png"},
		// A type the sniff did name is kept.
		{"foto.jpg", "image/jpeg", "image/jpeg"},
		{"belge.pdf", "application/pdf; charset=binary", "application/pdf"},
		{"klip.mp4", "video/mp4", "video/mp4"},
		// No generator for the extension: the sniff's type stays.
		{"veri.xml", "text/xml; charset=utf-8", "text/xml"},
		{"arsiv.zip", "application/zip", "application/zip"},
		{"notlar.txt", "text/plain; charset=utf-8", "text/plain"},
	}
	for _, c := range cases {
		n := &model.Node{Name: c.name, Mime: c.mime}
		if got := routeMime(n); got != c.want {
			t.Errorf("routeMime(%q, %q) = %q, want %q", c.name, c.mime, got, c.want)
		}
	}
}

// Fix draws a file skipped for want of a program once the program is here;
// the default CLI selection (stale only) does too, as it does for SVG.
func TestWanted_AProgramThatArrived(t *testing.T) {
	n := fileWith(100, "", at(0))
	row := &model.Thumbnail{State: "skipped", Error: "no_tool:pdf", SourceSig: n.ContentFingerprint(), AttemptedAt: at(0)}
	without := New(nil, "", Capabilities{Image: true, SVG: true})
	with := New(nil, "", Capabilities{Image: true, SVG: true, PDF: true})
	if without.Wanted(n, row, Selection{Stale: true}) {
		t.Fatal("the CLI's default run redraws a PDF while there is still nothing to draw it with")
	}
	if !with.Wanted(n, row, Selection{Stale: true}) {
		t.Fatal("the CLI's default run leaves a PDF skipped for want of Ghostscript once it is installed")
	}
	if !without.Wanted(n, row, Fix) {
		t.Fatal("Fix leaves a skipped file alone")
	}
}
