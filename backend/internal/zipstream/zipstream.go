// Package zipstream is the one place filex turns a set of files into a ZIP
// that is written out as it is built.
//
// # Why a package rather than a fourth loop
//
// Three places grew their own `zip.NewWriter` + read + `io.Copy` loop:
// internal/sharezip (the folder-share archive cache), the public folder-share
// download in api/handlers/share.go, and — with this change — the authenticated
// "download my selection" endpoint. They agreed on the important things by
// accident and disagreed on the rest: one skipped an unreadable member and one
// aborted; one could be told to stop mid-build and one could not; none of them
// recorded what they had dropped, so a member that vanished between the listing
// and the read left a SHORTER archive and said nothing. Three loops is how the
// next fix lands in one of them.
//
// Everything here streams. `Write` takes an io.Writer and never seeks, so the
// same call serves an http.ResponseWriter (bytes leave as they are produced,
// nothing is buffered, a 4 GB selection costs a few hundred KB of memory) and a
// temp file on disk.
//
// # What it will not do
//
// It will not tell you how big the archive is going to be. There is no such
// number before the last member is deflated, and inventing one is the exact
// shape of a bug this repo has already paid for: a Content-Length taken from
// the catalogue instead of the bytes, after which Go truncates the body and the
// failure surfaces in somebody else's program under a generic name. A caller
// serving this over HTTP must let the response be chunked.
package zipstream

import (
	"archive/zip"
	"context"
	"fmt"
	"io"
	"path"
	"strconv"
	"strings"
	"time"
)

// Member is one entry to be written into the archive.
//
// Open is called at most once, only when the member's turn comes, so a member
// list costs nothing to build and a caller may name more bytes than it could
// ever hold. It is a func rather than an io.Reader precisely so the read starts
// late: by the time a 200-file archive reaches member 200, member 1's
// connection would long since have timed out.
type Member struct {
	// Name is the path inside the archive, forward-slashed and relative.
	// Cleaned and de-duplicated by Write; a name that escapes the archive
	// root is rejected there, not here.
	Name string
	// Size is what the caller believed the member's size to be. It is used
	// for reporting and progress only — never for a length header, and never
	// to decide the archive is finished.
	Size int64
	// Mtime is stamped into the entry so an unpacked tree keeps its dates.
	// The zero value writes the zip epoch, which is what a bare zw.Create
	// does and is only ever ugly, never wrong.
	Mtime time.Time
	// Open yields the member's bytes.
	Open func(ctx context.Context) (io.ReadCloser, error)
}

// Skip records one member that did not make it into the archive whole, and
// why. Reasons are the two things that can go wrong once building has started.
type Skip struct {
	Name string
	// Err is why. Never nil.
	Err error
	// Partial is true when the failure happened AFTER bytes had already been
	// written into the entry — the difference between "this file is missing
	// from your archive" and "this file is in your archive and it is short".
	// A caller that reports skips must keep them apart: the second one is the
	// dangerous kind, because the entry looks perfectly well-formed to every
	// unzip tool on earth.
	Partial bool
	// Written is how many bytes reached the entry before the failure.
	Written int64
}

func (s Skip) String() string {
	if s.Partial {
		return fmt.Sprintf("%s — incomplete after %d bytes: %v", s.Name, s.Written, s.Err)
	}
	return fmt.Sprintf("%s — not included: %v", s.Name, s.Err)
}

// Options tune one Write. The zero value is valid and means "write every
// member, stop for nothing, report nothing".
type Options struct {
	// Gate is asked, at most once per GateEvery, whether the build should
	// still be running. A non-nil error from it ends Write with that error.
	// It is consulted BETWEEN members and DURING a member's copy, because
	// "between members" is no bound at all on a set whose one member is
	// 15 GB.
	Gate func() error
	// GateEvery throttles Gate. Zero or negative means NO throttle: the gate
	// is asked at every opportunity.
	//
	// ⚠ Unthrottled is the default on purpose, and it is the cheap direction.
	// A gate is a caller's own predicate — sharezip's is a mutex and a map
	// lookup — so asking it once per 32 KiB read costs microseconds per
	// gigabyte, while a throttle that is accidentally in force is a build
	// that keeps running for seconds after everyone stopped caring. Callers
	// whose gate is genuinely expensive set an interval; nobody has to
	// remember to switch responsiveness ON.
	GateEvery time.Duration

	// OnMember runs after each member is fully written. Progress counters
	// hang off this.
	OnMember func(m Member)

	// OnSkip runs for each member that was dropped or truncated. A caller
	// that ignores this is choosing to lose files silently.
	OnSkip func(s Skip)

	// Flush is called after each member. Over HTTP this is what makes the
	// browser show a growing download instead of a stalled one while a slow
	// storage is being read.
	Flush func()

	// Manifest, when non-empty, is the name of an extra text member appended
	// to the archive listing every Skip. It is written ONLY if something was
	// skipped, so a clean archive gains nothing. This is the honest half of
	// "keep going when one member disappears": the archive stays valid and
	// the person opening it can see what is not in it.
	Manifest string
}

// Write builds a ZIP of members into out and returns the members it could not
// include whole.
//
// # What ends the archive and what does not
//
// A member that cannot be opened is skipped: nothing is written for it, and it
// is reported. This is deliberate and is the common case — a selection is a
// list of paths that were true when the user clicked, and by the time the
// archive reaches member 40 one of them may have been renamed by somebody else.
// Failing the whole download because one file moved would be worse for the
// person waiting than handing them the other thirty-nine and a note.
//
// A member whose read fails PART WAY through cannot be un-written; archive/zip
// has already emitted its header. Write finishes that entry with the bytes it
// got, records a partial Skip, and carries on. The manifest is what stops that
// from being silent.
//
// The two things that DO end the archive early: ctx being done (the client hung
// up, or the server is shutting down) and Gate saying so. Both return an error
// and leave whatever was already written on out — over HTTP that is a truncated
// body, which is exactly the signal a client needs, and on a temp file it is a
// partial file the caller must delete.
func Write(ctx context.Context, out io.Writer, members []Member, opt Options) ([]Skip, error) {
	g := &gate{check: opt.Gate, every: opt.GateEvery, last: time.Now()}

	zw := zip.NewWriter(out)
	var skips []Skip
	note := func(s Skip) {
		skips = append(skips, s)
		if opt.OnSkip != nil {
			opt.OnSkip(s)
		}
	}

	used := make(map[string]bool, len(members))
	for _, m := range members {
		if err := ctx.Err(); err != nil {
			_ = zw.Close()
			return skips, err
		}
		if err := g.due(); err != nil {
			_ = zw.Close()
			return skips, err
		}

		name, err := EntryName(m.Name)
		if err != nil {
			note(Skip{Name: m.Name, Err: err})
			continue
		}
		name = dedupe(used, name)

		if m.Open == nil {
			note(Skip{Name: name, Err: errNoOpener})
			continue
		}
		rc, err := m.Open(ctx)
		if err != nil {
			// The whole point of the skip path: the file went away, or the
			// storage refused it, and thirty-nine other files are waiting.
			note(Skip{Name: name, Err: err})
			continue
		}

		hdr := &zip.FileHeader{Name: name, Method: zip.Deflate}
		if !m.Mtime.IsZero() {
			hdr.Modified = m.Mtime
		}
		fw, err := zw.CreateHeader(hdr)
		if err != nil {
			_ = rc.Close()
			_ = zw.Close()
			return skips, err
		}
		n, cpErr := io.Copy(fw, &gatedReader{ctx: ctx, r: rc, gate: g})
		_ = rc.Close()
		if cpErr != nil {
			// Past the point of no return for THIS entry. If the copy died
			// because the whole build is over (client gone, gate closed),
			// that is not a skip — it is the end.
			if ctx.Err() != nil || g.closed != nil {
				_ = zw.Close()
				return skips, cpErr
			}
			note(Skip{Name: name, Err: cpErr, Partial: true, Written: n})
			continue
		}
		if opt.OnMember != nil {
			opt.OnMember(m)
		}
		if opt.Flush != nil {
			opt.Flush()
		}
	}

	if opt.Manifest != "" && len(skips) > 0 {
		if err := writeManifest(zw, dedupe(used, opt.Manifest), skips); err != nil {
			_ = zw.Close()
			return skips, err
		}
	}
	return skips, zw.Close()
}

// writeManifest appends the "what is not in here" note.
func writeManifest(zw *zip.Writer, name string, skips []Skip) error {
	fw, err := zw.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Deflate, Modified: time.Now()})
	if err != nil {
		return err
	}
	var b strings.Builder
	b.WriteString("This archive is incomplete.\r\n")
	b.WriteString("The following entries could not be included in full:\r\n\r\n")
	for _, s := range skips {
		b.WriteString(s.String())
		b.WriteString("\r\n")
	}
	_, err = io.WriteString(fw, b.String())
	return err
}

// EntryName normalizes a member name to something that is safe to unpack:
// forward slashes, no leading slash, no drive letter, no ".." component.
//
// It is the write-side twin of the read-side sanitizer in api/handlers
// (sanitizeZipPath). Both exist because the danger runs both ways: an archive
// we UNPACK can try to escape its destination, and an archive we BUILD from
// paths the caller supplied can carry an escape to whoever unpacks it next.
func EntryName(raw string) (string, error) {
	clean := strings.ReplaceAll(raw, `\`, "/")
	clean = strings.TrimLeft(clean, "/")
	if clean == "" {
		return "", errEmptyName
	}
	if len(clean) >= 2 && clean[1] == ':' {
		return "", fmt.Errorf("zipstream: absolute path %q", raw)
	}
	for _, part := range strings.Split(clean, "/") {
		if part == ".." {
			return "", fmt.Errorf("zipstream: parent traversal in %q", raw)
		}
	}
	clean = strings.TrimLeft(path.Clean("/"+clean), "/")
	if clean == "" || clean == "." || strings.HasPrefix(clean, "..") {
		return "", fmt.Errorf("zipstream: rejected %q", raw)
	}
	return clean, nil
}

// dedupe makes name unique within one archive by inserting " (2)", " (3)", …
// before the extension.
//
// Not cosmetic: a selection can legitimately contain two files with the same
// basename from two different folders (or two different storages), and a ZIP
// with two identical member names is a file most tools unpack by overwriting
// the first with the second. The user asked for both.
func dedupe(used map[string]bool, name string) string {
	if !used[name] {
		used[name] = true
		return name
	}
	ext := path.Ext(name)
	stem := strings.TrimSuffix(name, ext)
	for i := 2; ; i++ {
		cand := stem + " (" + strconv.Itoa(i) + ")" + ext
		if !used[cand] {
			used[cand] = true
			return cand
		}
	}
}

// gate throttles the caller's "should I still be running?" check.
type gate struct {
	check func() error
	every time.Duration
	last  time.Time
	// closed is set once the gate has refused, so the copy loop can tell an
	// abandoned build apart from a single unreadable member.
	closed error
}

func (g *gate) due() error {
	if g == nil || g.check == nil {
		return nil
	}
	if g.closed != nil {
		return g.closed
	}
	if g.every > 0 {
		now := time.Now()
		if now.Sub(g.last) < g.every {
			return nil
		}
		g.last = now
	}
	if err := g.check(); err != nil {
		g.closed = err
		return err
	}
	return nil
}

// gatedReader lets ctx cancellation and the gate interrupt a single long copy.
// Without it, one 15 GB member is 15 GB of writing after the client hung up.
type gatedReader struct {
	ctx  context.Context
	r    io.Reader
	gate *gate
}

func (r *gatedReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	if err := r.gate.due(); err != nil {
		return 0, err
	}
	return r.r.Read(p)
}

type constErr string

func (e constErr) Error() string { return string(e) }

const (
	errEmptyName = constErr("zipstream: empty entry name")
	errNoOpener  = constErr("zipstream: member has no opener")
)
