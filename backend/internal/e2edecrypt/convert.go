package e2edecrypt

import (
	"errors"
	"fmt"
	"strings"
)

// Encrypting a folder that already exists, in place: the Go twin of
// packages/core/src/lib/e2econvert.ts (docs/E2E-ENCRYPTION.md → "Encrypting a
// folder you already have"), for `filex encrypt <adapter://folder>`.
//
// The key file is written first, with the conversion under way
// (StartConversion), which makes the folder an encrypted folder to the server
// from that moment. Then this walks it: every file whose bytes do not start
// with the `filexe2e` magic is encrypted under the folder key and written back
// over itself as a CONVERSION WRITE - one the server keeps no version of,
// because what it replaces is the plaintext being removed. A file over the
// one-shot limit is a STREAM (0x02) file, as in the browser; here nothing
// holds a file in memory whatever its size.
//
// Idempotent and resumable by the magic: a file already converted is left
// alone, so running it again after a stop, a dropped connection or a failure
// continues it. A write is conditional on the file being the one that was
// listed (expect "<size>:<ms>"), so a file changed meanwhile is not
// overwritten with an older version of itself - it counts as failed, and the
// next run picks it up.
//
// Names are not touched here: at level 2 the name pass (namepass.go) runs
// after it. Pure over an interface, like the name pass.

// ConvertRow is one entry of a folder's raw listing (stored names).
type ConvertRow struct {
	// Path is the entry's full path on the server (adapter://rel).
	Path string
	// Name is its stored name.
	Name string
	Dir  bool
	// Size in bytes and Modified in Unix milliseconds, as listed; -1 when the
	// listing did not say. Together they are the write's precondition.
	Size     int64
	Modified int64
}

// Expect is the listing's own signature of a file, "<size>:<ms>", or "" when
// the listing did not give both.
func (r ConvertRow) Expect() string {
	if r.Size < 0 || r.Modified < 0 {
		return ""
	}
	return fmt.Sprintf("%d:%d", r.Size, r.Modified)
}

// ConvertIO is what the conversion needs from wherever the folder is.
type ConvertIO interface {
	// List is the raw listing of a folder.
	List(dir string) ([]ConvertRow, error)
	// Head is the first bytes of a file (at least 9 when it has them).
	Head(path string) ([]byte, error)
	// Convert encrypts the file `row` (in `dir`) under the folder key and
	// writes it over itself as a conversion write, on condition that it is
	// still the file listed (expect). Errors when the server refuses (a
	// changed file: 412).
	Convert(dir string, row ConvertRow, expect string) error
	// Stopped is asked between files.
	Stopped() bool
}

// ConvertFailure is one file the run could not convert, and why.
type ConvertFailure struct {
	Path string
	Err  error
}

// ConvertProgress counts a run.
type ConvertProgress struct {
	// Total is the files found (everything but the key file and sidecars).
	Total int
	// Done is the files converted by this run; Large of them as STREAM.
	Done, Large int
	// Skipped is the files already encrypted (converted earlier, or written
	// encrypted).
	Skipped int
	// Failed is the files that could not be read or written; Failures says
	// which and why.
	Failed   int
	Failures []ConvertFailure
	// OnFile, when set, is told every file as its turn comes.
	OnFile func(path string, size int64)
}

func (p *ConvertProgress) fail(path string, err error) {
	p.Failed++
	p.Failures = append(p.Failures, ConvertFailure{Path: path, Err: err})
}

// ErrStopped is a run that was asked to stop. What it did so far stands.
var ErrStopped = errors.New("stopped")

// RunConversion converts every plaintext file under root. It returns
// ErrStopped when Stopped said so; a file that failed is counted, not
// returned.
func RunConversion(root string, io ConvertIO, prog *ConvertProgress) error {
	type item struct {
		dir string
		row ConvertRow
	}
	// 1. The files, all of them first: the progress has a total.
	var files []item
	queue := []string{root}
	isRoot := func(dir string) bool { return strings.TrimRight(dir, "/") == strings.TrimRight(root, "/") }
	for len(queue) > 0 {
		if io.Stopped() {
			return ErrStopped
		}
		dir := queue[0]
		queue = queue[1:]
		rows, err := io.List(dir)
		if err != nil {
			prog.fail(dir, err)
			continue
		}
		for _, r := range rows {
			if r.Dir {
				queue = append(queue, r.Path)
				continue
			}
			if isRoot(dir) && r.Name == MarkerName {
				continue
			}
			if ClassifyStoredName(r.Name).Kind == KindSidecar {
				continue
			}
			files = append(files, item{dir: dir, row: r})
		}
	}
	prog.Total = len(files)

	// 2. One file at a time.
	for _, it := range files {
		if io.Stopped() {
			return ErrStopped
		}
		head, err := io.Head(it.row.Path)
		if err != nil {
			prog.fail(it.row.Path, err)
			continue
		}
		if HasMagic(head) {
			prog.Skipped++
			continue
		}
		if it.row.Size < 0 {
			// The write has to say how long the plaintext is (a STREAM's last
			// chunk is known by it), and the precondition needs it too.
			prog.fail(it.row.Path, errors.New("the listing did not give its size"))
			continue
		}
		if prog.OnFile != nil {
			prog.OnFile(it.row.Path, it.row.Size)
		}
		if err := io.Convert(it.dir, it.row, it.row.Expect()); err != nil {
			prog.fail(it.row.Path, err)
			continue
		}
		prog.Done++
		if it.row.Size > MaxOneShot {
			prog.Large++
		}
	}
	return nil
}
