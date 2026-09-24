package regfile

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

func TestOpen_ARegularFileOpens(t *testing.T) {
	p := filepath.Join(t.TempDir(), "a.txt")
	if err := os.WriteFile(p, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	f, err := Open(p)
	if err != nil {
		t.Fatalf("a regular file was refused: %v", err)
	}
	defer f.Close()
	buf := make([]byte, 5)
	if _, err := f.Read(buf); err != nil || string(buf) != "hello" {
		t.Fatalf("read %q, %v", buf, err)
	}
}

func TestOpenFile_CreatesAndTruncatesLikeOsCreate(t *testing.T) {
	p := filepath.Join(t.TempDir(), "new.txt")
	f, err := OpenFile(p, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0o666)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := f.Write([]byte("longer text")); err != nil {
		t.Fatal(err)
	}
	f.Close()
	f, err = OpenFile(p, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0o666)
	if err != nil {
		t.Fatalf("re-create: %v", err)
	}
	f.Close()
	if fi, _ := os.Stat(p); fi.Size() != 0 {
		t.Fatalf("O_TRUNC did not truncate: %d bytes left", fi.Size())
	}
}

func TestOpen_AFolderIsNotAFile(t *testing.T) {
	_, err := Open(t.TempDir())
	if !errors.Is(err, ErrNotRegular) {
		t.Fatalf("a folder opened as a file: %v", err)
	}
}

func TestSpecial(t *testing.T) {
	for _, c := range []struct {
		mode fs.FileMode
		want bool
	}{
		{0o644, false},
		{fs.ModeDir | 0o755, false},
		{fs.ModeSymlink | 0o777, false},
		{fs.ModeNamedPipe | 0o644, true},
		{fs.ModeSocket | 0o755, true},
		{fs.ModeDevice | 0o660, true},
		{fs.ModeDevice | fs.ModeCharDevice | 0o666, true},
		{fs.ModeIrregular, true},
	} {
		if got := Special(c.mode); got != c.want {
			t.Errorf("Special(%v) = %v, want %v", c.mode, got, c.want)
		}
	}
}
