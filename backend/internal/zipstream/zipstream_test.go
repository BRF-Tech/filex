package zipstream

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"
)

// mem builds a Member whose bytes are a fixed string.
func mem(name, body string) Member {
	return Member{
		Name:  name,
		Size:  int64(len(body)),
		Mtime: time.Date(2026, 9, 13, 10, 0, 0, 0, time.UTC),
		Open: func(context.Context) (io.ReadCloser, error) {
			return io.NopCloser(strings.NewReader(body)), nil
		},
	}
}

// read unpacks an archive into name → body.
func read(t *testing.T, b []byte) map[string]string {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(b), int64(len(b)))
	if err != nil {
		t.Fatalf("the archive does not open: %v", err)
	}
	out := map[string]string{}
	for _, f := range zr.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatalf("member %s: %v", f.Name, err)
		}
		body, err := io.ReadAll(rc)
		_ = rc.Close()
		if err != nil {
			t.Fatalf("member %s: %v", f.Name, err)
		}
		out[f.Name] = string(body)
	}
	return out
}

func TestWriteMembers(t *testing.T) {
	var buf bytes.Buffer
	skips, err := Write(context.Background(), &buf, []Member{
		mem("a.txt", "alpha"),
		mem("dir/b.txt", "bravo"),
	}, Options{})
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if len(skips) != 0 {
		t.Fatalf("nothing should have been skipped, got %v", skips)
	}
	got := read(t, buf.Bytes())
	if got["a.txt"] != "alpha" || got["dir/b.txt"] != "bravo" {
		t.Fatalf("members are wrong: %#v", got)
	}
}

// The mtime has to survive, or every unpacked tree reads as "changed in 1979".
func TestWriteKeepsMtime(t *testing.T) {
	want := time.Date(2026, 9, 13, 10, 0, 0, 0, time.UTC)
	var buf bytes.Buffer
	if _, err := Write(context.Background(), &buf, []Member{mem("a.txt", "x")}, Options{}); err != nil {
		t.Fatal(err)
	}
	zr, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatal(err)
	}
	if got := zr.File[0].Modified.UTC(); !got.Equal(want) {
		t.Fatalf("mtime = %v, want %v", got, want)
	}
}

// A member that disappeared between the listing and the read must cost that
// member and nothing else: the archive still opens, the other members are
// whole, and the loss is reported rather than silent.
func TestVanishedMemberIsSkippedNotFatal(t *testing.T) {
	boom := errors.New("no such file")
	var buf bytes.Buffer
	var seen []Skip
	skips, err := Write(context.Background(), &buf, []Member{
		mem("first.txt", "one"),
		{Name: "gone.txt", Open: func(context.Context) (io.ReadCloser, error) { return nil, boom }},
		mem("last.txt", "three"),
	}, Options{
		OnSkip:   func(s Skip) { seen = append(seen, s) },
		Manifest: "_filex-INCOMPLETE.txt",
	})
	if err != nil {
		t.Fatalf("one missing member must not fail the archive: %v", err)
	}
	if len(skips) != 1 || len(seen) != 1 {
		t.Fatalf("want exactly one reported skip, got %v / %v", skips, seen)
	}
	if skips[0].Partial {
		t.Error("a member that never opened is not a PARTIAL member")
	}
	got := read(t, buf.Bytes())
	if got["first.txt"] != "one" || got["last.txt"] != "three" {
		t.Fatalf("the surviving members are wrong: %#v", got)
	}
	if _, ok := got["gone.txt"]; ok {
		t.Error("the member that could not be opened must not be in the archive at all")
	}
	manifest, ok := got["_filex-INCOMPLETE.txt"]
	if !ok {
		t.Fatal("an incomplete archive must carry the note saying so")
	}
	if !strings.Contains(manifest, "gone.txt") || !strings.Contains(manifest, "no such file") {
		t.Fatalf("the note must name the member and the reason, got:\n%s", manifest)
	}
}

// A clean archive gains no manifest — the note exists to warn, not to decorate.
func TestNoManifestWhenNothingSkipped(t *testing.T) {
	var buf bytes.Buffer
	if _, err := Write(context.Background(), &buf, []Member{mem("a.txt", "x")}, Options{Manifest: "_filex-INCOMPLETE.txt"}); err != nil {
		t.Fatal(err)
	}
	if _, ok := read(t, buf.Bytes())["_filex-INCOMPLETE.txt"]; ok {
		t.Error("a complete archive must not carry an incompleteness note")
	}
}

// halfReader hands back n bytes then fails — a storage that dies mid-object.
type halfReader struct {
	body string
	n    int
	err  error
}

func (h *halfReader) Read(p []byte) (int, error) {
	if h.n <= 0 {
		return 0, h.err
	}
	n := copy(p, h.body[:h.n])
	h.n -= n
	return n, nil
}
func (h *halfReader) Close() error { return nil }

// The dangerous case: the entry header is already out, so the short member will
// look perfectly well-formed to every unzip tool. It must be reported as
// PARTIAL and named in the manifest, and it must not take the rest down.
func TestTruncatedMemberIsReportedAsPartial(t *testing.T) {
	var buf bytes.Buffer
	skips, err := Write(context.Background(), &buf, []Member{
		{Name: "half.bin", Open: func(context.Context) (io.ReadCloser, error) {
			return &halfReader{body: "abcdefghij", n: 4, err: errors.New("connection reset")}, nil
		}},
		mem("after.txt", "still here"),
	}, Options{Manifest: "_filex-INCOMPLETE.txt"})
	if err != nil {
		t.Fatalf("a truncated member must not fail the archive: %v", err)
	}
	if len(skips) != 1 || !skips[0].Partial {
		t.Fatalf("want one PARTIAL skip, got %#v", skips)
	}
	if skips[0].Written != 4 {
		t.Errorf("written = %d, want the 4 bytes that did arrive", skips[0].Written)
	}
	got := read(t, buf.Bytes())
	if got["after.txt"] != "still here" {
		t.Error("the members after a truncated one must still be written")
	}
	if got["half.bin"] != "abcd" {
		t.Errorf("half.bin = %q, want the bytes that arrived", got["half.bin"])
	}
	if !strings.Contains(got["_filex-INCOMPLETE.txt"], "incomplete after 4 bytes") {
		t.Errorf("the note must say the member is short, got:\n%s", got["_filex-INCOMPLETE.txt"])
	}
}

// Two files with the same basename from two folders is an ordinary selection.
// Writing both under one name would hand the user an archive where one
// silently overwrites the other.
func TestDuplicateNamesAreDeduped(t *testing.T) {
	var buf bytes.Buffer
	if _, err := Write(context.Background(), &buf, []Member{
		mem("report.txt", "from A"),
		mem("report.txt", "from B"),
		mem("report.txt", "from C"),
	}, Options{}); err != nil {
		t.Fatal(err)
	}
	got := read(t, buf.Bytes())
	if len(got) != 3 {
		t.Fatalf("want 3 distinct members, got %d: %#v", len(got), got)
	}
	if got["report.txt"] != "from A" || got["report (2).txt"] != "from B" || got["report (3).txt"] != "from C" {
		t.Fatalf("dedupe put the wrong bodies under the wrong names: %#v", got)
	}
}

func TestEntryNameRejectsEscapes(t *testing.T) {
	for _, bad := range []string{"", "../etc/passwd", "a/../../b", `C:\secrets`, "/"} {
		if got, err := EntryName(bad); err == nil {
			t.Errorf("EntryName(%q) = %q, want an error", bad, got)
		}
	}
	for _, ok := range []struct{ in, want string }{
		{"a.txt", "a.txt"},
		{"/a/b.txt", "a/b.txt"},
		{`dir\sub\c.txt`, "dir/sub/c.txt"},
		{"./x.txt", "x.txt"},
	} {
		got, err := EntryName(ok.in)
		if err != nil || got != ok.want {
			t.Errorf("EntryName(%q) = %q, %v; want %q, nil", ok.in, got, err, ok.want)
		}
	}
}

// A member whose name escapes the archive is dropped, not written — the
// caller's paths are not automatically trustworthy just because they are ours.
func TestEscapingMemberIsSkipped(t *testing.T) {
	var buf bytes.Buffer
	skips, err := Write(context.Background(), &buf, []Member{
		mem("../escape.txt", "nope"),
		mem("fine.txt", "yes"),
	}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(skips) != 1 {
		t.Fatalf("want the escaping member skipped, got %#v", skips)
	}
	got := read(t, buf.Bytes())
	if len(got) != 1 || got["fine.txt"] != "yes" {
		t.Fatalf("archive = %#v", got)
	}
}

// The gate is the "stop, nobody is waiting for this any more" signal, and it
// has to be able to interrupt a SINGLE member's copy. Between-members checking
// is no bound at all when the one member is enormous.
func TestGateInterruptsALongCopy(t *testing.T) {
	stop := errors.New("abandoned")
	var closed bool
	var buf bytes.Buffer
	_, err := Write(context.Background(), &buf, []Member{
		{Name: "big.bin", Open: func(context.Context) (io.ReadCloser, error) {
			return io.NopCloser(iotestInfinite{}), nil
		}},
	}, Options{
		GateEvery: time.Nanosecond,
		Gate: func() error {
			if closed {
				return stop
			}
			closed = true
			return nil
		},
	})
	if !errors.Is(err, stop) {
		t.Fatalf("err = %v, want the gate's own error", err)
	}
}

// A client that hangs up must stop the build, not be written to for another
// 15 GB.
func TestContextCancelStopsTheBuild(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var buf bytes.Buffer
	_, err := Write(ctx, &buf, []Member{mem("a.txt", "x")}, Options{})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}

// A cancellation partway through must NOT be filed as a per-member skip: the
// build is over, and calling it "one file could not be read" would make a
// truncated download look like a mostly-successful one.
func TestCancelMidCopyIsNotASkip(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	var buf bytes.Buffer
	skips, err := Write(ctx, &buf, []Member{
		{Name: "big.bin", Open: func(context.Context) (io.ReadCloser, error) {
			cancel()
			return io.NopCloser(iotestInfinite{}), nil
		}},
		mem("never.txt", "unreached"),
	}, Options{})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	for _, s := range skips {
		if s.Partial {
			t.Errorf("a cancelled build reported a partial member (%v) — that reads as a survivable loss", s)
		}
	}
}

// iotestInfinite never ends, which is what makes the two tests above meaningful.
type iotestInfinite struct{}

func (iotestInfinite) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = 'x'
	}
	return len(p), nil
}

func TestFlushAndProgressFire(t *testing.T) {
	var flushes, done int
	var buf bytes.Buffer
	if _, err := Write(context.Background(), &buf, []Member{
		mem("a.txt", "1"), mem("b.txt", "2"), mem("c.txt", "3"),
	}, Options{
		Flush:    func() { flushes++ },
		OnMember: func(Member) { done++ },
	}); err != nil {
		t.Fatal(err)
	}
	if flushes != 3 || done != 3 {
		t.Fatalf("flush=%d done=%d, want 3/3", flushes, done)
	}
}
