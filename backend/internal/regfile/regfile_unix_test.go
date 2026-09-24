//go:build unix

package regfile

// Real named pipes and sockets. A Windows host has neither inside a folder, so
// these run where they can exist (Linux, macOS, WSL).

import (
	"errors"
	"net"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// within fails the test when fn has not returned after d. The goroutine is
// abandoned on purpose: a blocked open cannot be interrupted, and the point of
// the test is that it is never blocked in the first place.
func within(t *testing.T, d time.Duration, what string, fn func() error) error {
	t.Helper()
	done := make(chan error, 1)
	go func() { done <- fn() }()
	select {
	case err := <-done:
		return err
	case <-time.After(d):
		t.Fatalf("%s did not return within %s — it is waiting on the pipe", what, d)
		return nil
	}
}

func mkfifo(t *testing.T, dir, name string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := syscall.Mkfifo(p, 0o644); err != nil {
		t.Skipf("mkfifo: %v", err)
	}
	return p
}

func TestOpen_ANamedPipeIsRefusedAtOnce(t *testing.T) {
	p := mkfifo(t, t.TempDir(), "cinit_cmd")
	err := within(t, 5*time.Second, "Open(fifo)", func() error {
		f, err := Open(p)
		if f != nil {
			f.Close()
		}
		return err
	})
	if !errors.Is(err, ErrNotRegular) {
		t.Fatalf("a named pipe was not refused: %v", err)
	}
}

// ⚠ The half that matters when a pipe appears AFTER OpenFile looked: the open
// itself must not wait for a writer. RED without nonBlock — the read-only open
// of a pipe nobody writes to never returns.
func TestOpenChecked_APipeThatAppearedAfterTheLookDoesNotBlock(t *testing.T) {
	p := mkfifo(t, t.TempDir(), "late.fifo")
	err := within(t, 5*time.Second, "openChecked(fifo, O_RDONLY)", func() error {
		f, err := openChecked(p, os.O_RDONLY, 0)
		if f != nil {
			f.Close()
		}
		return err
	})
	if !errors.Is(err, ErrNotRegular) {
		t.Fatalf("a pipe opened as a file: %v", err)
	}
	// Writing: with nobody reading, a non-blocking write open fails at once.
	err = within(t, 5*time.Second, "openChecked(fifo, O_WRONLY)", func() error {
		f, err := openChecked(p, os.O_WRONLY|os.O_TRUNC, 0)
		if f != nil {
			f.Close()
		}
		return err
	})
	if err == nil {
		t.Fatal("a pipe was opened for writing")
	}
}

func TestOpenFile_WritingOntoAPipeIsRefusedAtOnce(t *testing.T) {
	p := mkfifo(t, t.TempDir(), "sink.fifo")
	err := within(t, 5*time.Second, "OpenFile(fifo, create)", func() error {
		f, err := OpenFile(p, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0o666)
		if f != nil {
			f.Close()
		}
		return err
	})
	if !errors.Is(err, ErrNotRegular) {
		t.Fatalf("os.Create semantics on a pipe: %v", err)
	}
}

func TestOpen_ASocketIsRefused(t *testing.T) {
	// Unix socket paths are short (108 bytes); t.TempDir can exceed that.
	dir, err := os.MkdirTemp("", "rf")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	p := filepath.Join(dir, "vnc.sock")
	l, err := net.Listen("unix", p)
	if err != nil {
		t.Skipf("unix socket: %v", err)
	}
	defer l.Close()
	fi, err := os.Lstat(p)
	if err != nil || !Special(fi.Mode()) {
		t.Fatalf("a socket is not Special: %v %v", fi, err)
	}
	if _, err := Open(p); !errors.Is(err, ErrNotRegular) {
		t.Fatalf("a socket was not refused: %v", err)
	}
}

func TestOpen_ADeviceIsRefused(t *testing.T) {
	if _, err := os.Stat("/dev/null"); err != nil {
		t.Skip("no /dev/null")
	}
	if _, err := Open("/dev/null"); !errors.Is(err, ErrNotRegular) {
		t.Fatalf("/dev/null opened as a file: %v", err)
	}
}
