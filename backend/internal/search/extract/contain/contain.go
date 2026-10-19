// Package contain runs a document parser in a process of its own, with a
// memory ceiling and a time limit (GitHub #123).
//
// # What went wrong
//
// The PDF reader (ledongthuc/pdf) ran inside filex. Some small PDFs, about a
// megabyte, send its text extraction into memory without end: one install's
// filex reached 27 GB and the kernel killed the whole server, three times in
// a minute, as the job came back after every restart. The source-size cap
// does not bound what a parser builds from a file, the 200 KiB text cap
// applies after the parse, a soft memory limit cannot free memory that is in
// use, and recover cannot catch the kernel.
//
// # What it does now
//
// An extractor that walks a document's structure (extract.Isolated: PDF and
// the office formats) runs in a child: filex starts its own binary again with
// FILEX_EXTRACT_CHILD set, and the init below does nothing in that process
// but the extraction - the file on stdin, the text on stdout. The child holds
// itself to the ceiling it is given (debug.SetMemoryLimit, then a watch that
// ends it once the memory it holds passes the ceiling, read the way the
// indexing budget reads it: sysmem.InUse), and on Linux offers itself first to
// the kernel's OOM killer. The parent kills it when the time limit passes or
// the job is cancelled. Whatever ends the child, the server goes on: the file
// is indexed by its name and the job does not come back.
//
// ⚠ The dispatch is an init, not a call from main: every binary that links
// this package - the server, a package's test binary - answers as a child
// when it is started as one. A call from main would leave a test binary that
// extracts a PDF starting its own tests again, in a loop.
package contain

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime/debug"
	"strconv"
	"sync"
	"time"

	"github.com/brf-tech/filex/backend/internal/search/extract"
	"github.com/brf-tech/filex/backend/internal/sysmem"
)

// What the parent tells the child, in its environment.
const (
	envChild  = "FILEX_EXTRACT_CHILD"
	envMime   = "FILEX_EXTRACT_MIME"
	envExt    = "FILEX_EXTRACT_EXT"
	envLimit  = "FILEX_EXTRACT_LIMIT"
	envMemory = "FILEX_EXTRACT_MEMORY"
)

// The child's exit codes. Go's runtime uses 2 for a panic and for running out
// of memory, so the child's own start at 3.
const (
	exitNoExtractor = 3
	exitOverMemory  = 4
	exitFailed      = 5
)

// Timeout is how long one extraction may take before its process is killed.
// A variable so a test can shorten it.
var Timeout = 2 * time.Minute

// watchEvery is how often the child reads the memory it holds. It reads the
// runtime's own figures (no stop-the-world), so a short period costs little.
const watchEvery = 10 * time.Millisecond

// waitDelay bounds how long Wait waits for the child's output to close
// once the time limit or the job's end has killed it. A variable so a test
// can shorten it.
var waitDelay = 5 * time.Second

// copyGrace is how long a child that exited well waits for its source copy to
// say how it ended: the child read to the end, so the copy is ending too.
const copyGrace = time.Second

// ErrStopped is the error of an extraction whose process was stopped: over
// its memory ceiling, past the time limit, or ended by the system. The file
// has no text to give; the caller indexes it by its name.
var ErrStopped = errors.New("extraction stopped")

func init() {
	if os.Getenv(envChild) != "1" {
		return
	}
	os.Exit(child())
}

// child is the whole life of an extraction process.
func child() int {
	if ceiling, _ := strconv.ParseUint(os.Getenv(envMemory), 10, 64); ceiling > 0 {
		debug.SetMemoryLimit(int64(ceiling))
		go watch(ceiling)
	}
	offerToOOMKiller()
	ex := extract.For(os.Getenv(envMime), os.Getenv(envExt))
	if ex == nil {
		return exitNoExtractor
	}
	limit, _ := strconv.ParseInt(os.Getenv(envLimit), 10, 64)
	text, err := ex.Extract(context.Background(), os.Stdin, limit)
	if err != nil {
		return exitFailed
	}
	if _, err := io.WriteString(os.Stdout, text); err != nil {
		return exitFailed
	}
	return 0
}

// watch ends the child once the memory it holds passes ceiling.
func watch(ceiling uint64) {
	t := time.NewTicker(watchEvery)
	defer t.Stop()
	for range t.C {
		if sysmem.InUse() > ceiling {
			os.Exit(exitOverMemory)
		}
	}
}

// Extract reads the text of a file of the given mime and extension, read from
// r, in a process of its own: at most limit bytes of text (<= 0: the
// extraction contract's default), with ceiling bytes of memory (0: no
// ceiling) and Timeout to do it in.
//
// An error is one of three: ctx's own when the job was cancelled (nothing was
// learnt about the file, so the caller should not record it as empty),
// ErrStopped when the process was stopped, or what kept the process from
// starting or the source from being read.
func Extract(ctx context.Context, mime, ext string, r io.Reader, limit int64, ceiling uint64) (string, error) {
	if limit <= 0 {
		limit = extract.DefaultLimit
	}
	runCtx, cancel := context.WithTimeout(ctx, Timeout)
	defer cancel()
	cmd := exec.CommandContext(runCtx, self())
	cmd.Env = append(os.Environ(),
		envChild+"=1",
		envMime+"="+mime,
		envExt+"="+ext,
		envLimit+"="+strconv.FormatInt(limit, 10),
		envMemory+"="+strconv.FormatUint(ceiling, 10),
	)
	out := &capped{max: int(limit)}
	cmd.Stdout = out
	cmd.WaitDelay = waitDelay
	// ⚠ The source is copied here, not handed to cmd.Stdin: Wait waits for
	// its own stdin copy to end, and a copy blocked in a source read (a stalled
	// storage, a child that died before reading it all) would hold the job
	// after the child is gone. This copy ends when the caller closes the
	// source, which it does once Extract returns.
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return "", err
	}
	if err := cmd.Start(); err != nil {
		return "", err
	}
	src := &sourceReader{r: r}
	copied := make(chan struct{})
	go func() {
		defer close(copied)
		_, _ = io.Copy(stdin, src)
		_ = stdin.Close()
	}()
	err = cmd.Wait()
	switch {
	case err == nil:
		select {
		case <-copied:
		case <-time.After(copyGrace):
		}
		if err := src.failed(); err != nil {
			return "", err
		}
		return out.buf.String(), nil
	case ctx.Err() != nil:
		return "", ctx.Err()
	case runCtx.Err() != nil:
		return "", fmt.Errorf("%w: no text after %s", ErrStopped, Timeout)
	}
	var exit *exec.ExitError
	if !errors.As(err, &exit) {
		return "", err
	}
	switch code := exit.ExitCode(); code {
	case exitOverMemory:
		return "", fmt.Errorf("%w: over its memory ceiling of %d MB", ErrStopped, ceiling>>20)
	case -1:
		return "", fmt.Errorf("%w: ended by the system (%s)", ErrStopped, exit.String())
	default:
		return "", fmt.Errorf("%w: exit %d", ErrStopped, code)
	}
}

// sourceReader remembers how reading the source failed, apart from the
// child's side of the pipe: a source that failed is the caller's error (the
// queue may retry), a child that stopped reading is not.
type sourceReader struct {
	r   io.Reader
	mu  sync.Mutex
	err error
}

func (s *sourceReader) Read(p []byte) (int, error) {
	n, err := s.r.Read(p)
	if err != nil && err != io.EOF {
		s.mu.Lock()
		s.err = err
		s.mu.Unlock()
	}
	return n, err
}

// failed is how reading the source failed, nil if it did not.
func (s *sourceReader) failed() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.err
}

// capped keeps the first max bytes written to it and drops the rest, so a
// child cannot hand the parent more than the text it was asked for.
type capped struct {
	buf bytes.Buffer
	max int
}

func (c *capped) Write(p []byte) (int, error) {
	if room := c.max - c.buf.Len(); room > 0 {
		c.buf.Write(p[:min(len(p), room)])
	}
	return len(p), nil
}
