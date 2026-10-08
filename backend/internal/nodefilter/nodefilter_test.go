package nodefilter

import (
	"errors"
	"net/url"
	"testing"
	"time"

	"github.com/brf-tech/filex/backend/internal/model"
)

func file(name, mime string, size int64, mod time.Time) *model.Node {
	m := mod
	return &model.Node{Name: name, Path: "/docs/" + name, Type: model.NodeTypeFile, Mime: mime, Size: size, BackendMtime: &m}
}

func parse(t *testing.T, q string) Criteria {
	t.Helper()
	v, err := url.ParseQuery(q)
	if err != nil {
		t.Fatal(err)
	}
	c, err := Parse(v)
	if err != nil {
		t.Fatalf("Parse(%q): %v", q, err)
	}
	return c
}

func TestKindOf(t *testing.T) {
	for _, c := range []struct {
		name, mime string
		dir        bool
		want       string
	}{
		{"Reports", "", true, KindFolder},
		{"a.XLSX", "", false, KindSpreadsheet},
		{"notes.md", "", false, KindText},
		{"IMG_0042", "image/jpeg", false, KindImage},
		{"LICENSE", "text/plain", false, KindText},
		{"app.apk", "", false, KindArchive},
		{"blob.bin", "application/octet-stream", false, KindOther},
	} {
		if got := KindOf(c.name, c.mime, c.dir); got != c.want {
			t.Errorf("KindOf(%q, %q) = %q, want %q", c.name, c.mime, got, c.want)
		}
	}
}

func TestAccept_EachPart(t *testing.T) {
	now := time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)
	big := file("film.mp4", "video/mp4", 200<<20, now)
	small := file("notes.txt", "text/plain", 10, now.AddDate(-1, 0, 0))
	dir := &model.Node{Name: "docs", Path: "/docs", Type: model.NodeTypeDirectory}

	if c := parse(t, "min_size=1048576"); !c.Accept(big, 0) || c.Accept(small, 0) || c.Accept(dir, 0) {
		t.Error("min_size keeps the big file only; a folder never satisfies a size bound")
	}
	if c := parse(t, "type=video"); !c.Accept(big, 0) || c.Accept(small, 0) {
		t.Error("type=video")
	}
	if c := parse(t, "type=document"); !c.Accept(small, 0) {
		t.Error("type=document takes plain text too, as the Documents chip always did")
	}
	if c := parse(t, "type=file"); c.Accept(dir, 0) || !c.Accept(small, 0) {
		t.Error("type=file drops folders")
	}
	if c := parse(t, "modified_after=2026-10-01"); !c.Accept(big, 0) || c.Accept(small, 0) {
		t.Error("modified_after")
	}
	if c := parse(t, "mime=video/"); !c.Accept(big, 0) || c.Accept(small, 0) {
		t.Error("mime prefix")
	}
	if c := parse(t, "under=main://docs"); !c.AcceptIn(small, 0, "main") || c.AcceptIn(small, 0, "other") || !c.Accept(dir, 0) {
		t.Error("under: inside the folder (the folder itself counts), in the named storage only")
	}
	if c := parse(t, "not_under=docs"); c.Accept(small, 0) {
		t.Error("not_under drops what is inside")
	}
	owner := int64(7)
	mine := file("mine.txt", "text/plain", 1, now)
	mine.OwnerID = &owner
	if c := parse(t, "owner=me"); !c.Accept(mine, 7) || c.Accept(mine, 8) || c.Accept(small, 7) {
		t.Error("owner=me")
	}
	if c := parse(t, "owner=system"); c.Accept(mine, 7) || !c.Accept(small, 7) {
		t.Error("owner=system is the ownerless row")
	}
	dot := file(".env", "text/plain", 1, now)
	if c := parse(t, "hidden=false"); c.Accept(dot, 0) || !c.Accept(small, 0) {
		t.Error("hidden=false drops dot names")
	}
	if c := parse(t, ""); c.Active() || !c.Accept(dot, 0) {
		t.Error("no parameter narrows nothing")
	}
}

// TestParse_RefusesWhatItCannotRead: a filter quietly dropped answers a wider
// question than the one asked.
func TestParse_RefusesWhatItCannotRead(t *testing.T) {
	for _, q := range []string{"min_size=lots", "modified_after=yesterday", "type=spaceship", "owner=-3", "hidden=maybe"} {
		v, _ := url.ParseQuery(q)
		_, err := Parse(v)
		if !errors.Is(err, ErrBad) {
			t.Errorf("Parse(%q) = %v, want ErrBad", q, err)
		}
	}
}
