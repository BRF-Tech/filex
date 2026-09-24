//go:build unix

package local

// Issue #38: a named pipe (or a socket, or a device) anywhere under a local
// storage hung the storage scan forever.
//
// The driver treated every entry that was not a folder or a symlink as a file
// and opened it to sniff its type; opening a pipe nobody writes to never
// returns. The reporter's storage held Docker overlay directories —
// `.cinit_cmd` pipes and X11/VNC sockets — so the sync sat in `running` with
// nothing processed, and every sync after it queued behind the hung one.
//
// These run where a pipe can exist (Linux, macOS, WSL). Every call is made
// under a deadline: the unfixed code does not fail, it never returns.

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/brf-tech/filex/backend/internal/storage"
)

func promptly[T any](t *testing.T, what string, fn func() (T, error)) (T, error) {
	t.Helper()
	type res struct {
		v   T
		err error
	}
	done := make(chan res, 1)
	go func() {
		v, err := fn()
		done <- res{v, err}
	}()
	select {
	case r := <-done:
		return r.v, r.err
	case <-time.After(5 * time.Second):
		t.Fatalf("%s did not return within 5s — it is waiting on a named pipe", what)
		var zero T
		return zero, nil
	}
}

// specialRoot builds the reporter's shape: an ordinary file beside a Docker
// overlay directory holding a pipe, and a socket at the top.
func specialRoot(t *testing.T) (*Driver, string) {
	t.Helper()
	// Unix socket paths are limited to ~108 bytes; t.TempDir can exceed that.
	root, err := os.MkdirTemp("", "fx38")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	if err := os.WriteFile(filepath.Join(root, "notes.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	overlay := filepath.Join(root, "docker", "merged", "tmp")
	if err := os.MkdirAll(overlay, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(filepath.Join(overlay, ".cinit_cmd"), 0o644); err != nil {
		t.Skipf("mkfifo: %v", err)
	}
	if err := os.WriteFile(filepath.Join(overlay, "keep.txt"), []byte("kept"), 0o644); err != nil {
		t.Fatal(err)
	}
	l, err := net.Listen("unix", filepath.Join(root, "vnc.sock"))
	if err != nil {
		t.Skipf("unix socket: %v", err)
	}
	t.Cleanup(func() { _ = l.Close() })
	d := &Driver{}
	if err := d.Init(context.Background(), map[string]any{"path": root}); err != nil {
		t.Fatal(err)
	}
	return d, root
}

func names(objs []storage.Object) []string {
	out := make([]string, 0, len(objs))
	for _, o := range objs {
		out = append(out, o.Name)
	}
	return out
}

func TestList_SkipsPipesAndSocketsInsteadOfOpeningThem(t *testing.T) {
	d, _ := specialRoot(t)
	ctx := context.Background()

	top, err := promptly(t, "List(/)", func() ([]storage.Object, error) { return d.List(ctx, "/") })
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(names(top), ","); got != "docker,notes.txt" && got != "notes.txt,docker" {
		t.Fatalf("top level lists %q — a socket is not a file", got)
	}
	deep, err := promptly(t, "List(overlay)", func() ([]storage.Object, error) { return d.List(ctx, "/docker/merged/tmp") })
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(names(deep), ","); got != "keep.txt" {
		t.Fatalf("the overlay lists %q — the pipe must be skipped, its neighbour kept", got)
	}
}

func TestStatReadWrite_AnswerForAPipeAtOnce(t *testing.T) {
	d, root := specialRoot(t)
	ctx := context.Background()
	const pipe = "/docker/merged/tmp/.cinit_cmd"

	if _, err := promptly(t, "Stat(pipe)", func() (storage.Object, error) { return d.Stat(ctx, pipe) }); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("Stat of a pipe: %v, want ErrNotFound (List does not show it)", err)
	}
	if _, err := promptly(t, "Read(pipe)", func() (any, error) { return d.Read(ctx, pipe) }); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("Read of a pipe: %v", err)
	}
	if _, err := promptly(t, "ReadRange(pipe)", func() (any, error) { return d.ReadRange(ctx, pipe, 0, 10) }); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("ReadRange of a pipe: %v", err)
	}
	// Writing onto the pipe's name: refused, and the pipe is still a pipe.
	if _, err := promptly(t, "Write(pipe)", func() (any, error) {
		return nil, d.Write(ctx, pipe, strings.NewReader("x"), 1)
	}); !errors.Is(err, storage.ErrKindConflict) {
		t.Fatalf("Write onto a pipe: %v, want ErrKindConflict", err)
	}
	fi, err := os.Lstat(filepath.Join(root, "docker", "merged", "tmp", ".cinit_cmd"))
	if err != nil || fi.Mode()&os.ModeNamedPipe == 0 {
		t.Fatalf("the operator's pipe was replaced: %v %v", fi, err)
	}
}

func TestCopy_AFolderHoldingAPipeIsCopiedWithoutIt(t *testing.T) {
	d, root := specialRoot(t)
	ctx := context.Background()
	if _, err := promptly(t, "Copy(folder)", func() (any, error) { return nil, d.Copy(ctx, "/docker", "/docker-copy") }); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(root, "docker-copy", "merged", "tmp", "keep.txt"))
	if err != nil || string(b) != "kept" {
		t.Fatalf("the ordinary file did not come along: %q %v", b, err)
	}
	if _, err := os.Lstat(filepath.Join(root, "docker-copy", "merged", "tmp", ".cinit_cmd")); !os.IsNotExist(err) {
		t.Fatalf("the pipe was copied: %v", err)
	}
}

// A link to a pipe is the pipe: not listed, not opened. Following is on so the
// link is judged by its target (an in-root link is always followed anyway).
func TestList_ALinkToAPipeIsSkippedToo(t *testing.T) {
	d, root := specialRoot(t)
	ctx := context.Background()
	if err := os.Symlink(filepath.Join(root, "docker", "merged", "tmp", ".cinit_cmd"), filepath.Join(root, "pipe-link")); err != nil {
		t.Skipf("symlink: %v", err)
	}
	if err := os.Symlink("/dev/null", filepath.Join(root, "null-link")); err != nil {
		t.Skipf("symlink: %v", err)
	}
	d.followSymlinks = true
	top, err := promptly(t, "List(/)", func() ([]storage.Object, error) { return d.List(ctx, "/") })
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range names(top) {
		if n == "pipe-link" || n == "null-link" {
			t.Fatalf("%s is listed; it points at something that is not a file", n)
		}
	}
	if _, err := promptly(t, "Stat(pipe-link)", func() (storage.Object, error) { return d.Stat(ctx, "/pipe-link") }); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("Stat of a link to a pipe: %v", err)
	}
}

// The operator is told — once per entry, not once per scan.
func TestList_SaysSoOncePerEntry(t *testing.T) {
	d, _ := specialRoot(t)
	ctx := context.Background()
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })

	for i := 0; i < 3; i++ {
		if _, err := promptly(t, "List(overlay)", func() ([]storage.Object, error) { return d.List(ctx, "/docker/merged/tmp") }); err != nil {
			t.Fatal(err)
		}
	}
	log := buf.String()
	if n := strings.Count(log, ".cinit_cmd"); n != 1 {
		t.Fatalf("the pipe was reported %d times in three scans, want once:\n%s", n, log)
	}
	if !strings.Contains(log, "named pipe") {
		t.Fatalf("the report does not say what it was:\n%s", log)
	}
}
