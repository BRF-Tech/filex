package smb

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/brf-tech/filex/backend/internal/storage"
)

// Live tests against a real SMB server.
//
// ⚠⚠ This is the only kind of test that means anything for a protocol driver.
// The unit tests above check that the config is parsed and the paths are built;
// every actual bug in a driver like this lives on the wire — a name-separator
// the server rejects, a Mkdir that only does one level, a directory delete that
// refuses because the folder is not empty, a "." entry in a listing that makes
// a tree walker loop. None of that is visible without a server.
//
// Bring one up with:
//
//	docker run -d --name fx-smb-test -p 14450:445 \
//	  -e 'USER=filex;filexpass' -e 'SHARE=data;/share;;no;no;filex;;;' \
//	  dperson/samba
//	FILEX_SMB_TEST=127.0.0.1:14450 go test ./internal/storage/drivers/smb/
//
// Skipped when that variable is absent, so `go test ./...` on a machine with no
// Samba stays green rather than red for a reason that is not about the code.
func liveDriver(t *testing.T) *Driver {
	t.Helper()
	addr := os.Getenv("FILEX_SMB_TEST")
	if addr == "" {
		t.Skip("set FILEX_SMB_TEST=host:port (see the comment above) to run the live SMB tests")
	}
	host, port, _ := strings.Cut(addr, ":")
	cfg := map[string]any{
		"host":  host,
		"share": envOr("FILEX_SMB_SHARE", "data"),
		"user":  envOr("FILEX_SMB_USER", "filex"),
		// The password is read from the environment so it is never in the repo.
		"password": envOr("FILEX_SMB_PASS", "filexpass"),
		"root":     "filex-live",
	}
	if port != "" {
		cfg["port"] = port
	}
	d := &Driver{}
	if err := d.Init(context.Background(), cfg); err != nil {
		t.Fatalf("init: %v", err)
	}
	t.Cleanup(func() {
		_ = d.Delete(context.Background(), "")
		_ = d.Close()
	})
	return d
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func TestLive_RoundTrip(t *testing.T) {
	d := liveDriver(t)
	ctx := context.Background()

	// ⚠ Two levels deep into a base path that does not exist yet. go-smb2's
	// Mkdir is ONE level, unlike the SFTP client's MkdirAll, so this is the
	// case that fails with a path-not-found reading like a permission problem.
	body := []byte("gölge türkçe içerik — SMB")
	if err := d.Write(ctx, "belgeler/notlar.txt", bytes.NewReader(body), int64(len(body))); err != nil {
		t.Fatalf("write two levels deep: %v", err)
	}

	got, err := d.Read(ctx, "belgeler/notlar.txt")
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	defer got.Close()
	back, _ := io.ReadAll(got)
	if !bytes.Equal(back, body) {
		t.Fatalf("read back %q, want %q", back, body)
	}

	st, err := d.Stat(ctx, "belgeler/notlar.txt")
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if st.Size != int64(len(body)) {
		t.Errorf("size = %d, want %d", st.Size, len(body))
	}
	if st.Kind != storage.KindFile {
		t.Errorf("kind = %v, want file", st.Kind)
	}
	if st.Name != "notlar.txt" {
		t.Errorf("name = %q, want notlar.txt", st.Name)
	}
}

// ⚠ SMB reports "." and ".." as real directory entries. Passing them up
// produces a folder that contains itself, and every tree walker in filex would
// follow it until it ran out of something.
func TestLive_ListHasNoDotEntries(t *testing.T) {
	d := liveDriver(t)
	ctx := context.Background()
	must(t, d.Write(ctx, "listing/a.txt", strings.NewReader("a"), 1))
	must(t, d.Write(ctx, "listing/b.txt", strings.NewReader("b"), 1))
	must(t, d.Mkdir(ctx, "listing/sub"))

	entries, err := d.List(ctx, "listing")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	names := map[string]storage.ObjectKind{}
	for _, e := range entries {
		if e.Name == "." || e.Name == ".." {
			t.Fatalf("the listing contains %q — a folder that contains itself", e.Name)
		}
		names[e.Name] = e.Kind
	}
	if len(names) != 3 {
		t.Fatalf("listing = %v, want three entries", names)
	}
	if names["sub"] != storage.KindDirectory {
		t.Errorf("sub is %v, want a directory", names["sub"])
	}
	// The path a listing reports must be the one a caller can pass straight
	// back to Read — relative to filex, not to the share.
	for _, e := range entries {
		if !strings.HasPrefix(e.Path, "listing/") {
			t.Errorf("entry path %q is not relative to the request", e.Path)
		}
	}
}

// The ranged read is what makes a NAS usable behind `filex mount` and behind
// video seeking: the bytes before the offset must not cross the network, and —
// far more importantly — the bytes that come back must be the right ones.
func TestLive_RangedReadReturnsTheRightWindow(t *testing.T) {
	d := liveDriver(t)
	ctx := context.Background()

	src := make([]byte, 256*1024)
	for i := range src {
		src[i] = byte(i * 7)
	}
	must(t, d.Write(ctx, "big.bin", bytes.NewReader(src), int64(len(src))))

	const off, length = 100_000, 4096
	rc, err := d.ReadRange(ctx, "big.bin", off, length)
	if err != nil {
		t.Fatalf("read range: %v", err)
	}
	defer rc.Close()
	window, _ := io.ReadAll(rc)
	if len(window) != length {
		t.Fatalf("read %d bytes, want %d", len(window), length)
	}
	if !bytes.Equal(window, src[off:off+length]) {
		t.Fatal("a ranged read returned the wrong bytes — the offset was ignored somewhere")
	}

	// A zero-length range is a legitimate request and must not read the file.
	empty, err := d.ReadRange(ctx, "big.bin", off, 0)
	if err != nil {
		t.Fatalf("zero-length range: %v", err)
	}
	defer empty.Close()
	if n, _ := io.ReadAll(empty); len(n) != 0 {
		t.Fatalf("zero-length range returned %d bytes", len(n))
	}
}

func TestLive_MoveCopyAndDelete(t *testing.T) {
	d := liveDriver(t)
	ctx := context.Background()
	must(t, d.Write(ctx, "src/one.txt", strings.NewReader("one"), 3))

	// A move into a folder that does not exist yet has to create it, the same
	// as a write does.
	must(t, d.Move(ctx, "src/one.txt", "moved/deep/two.txt"))
	if _, err := d.Stat(ctx, "src/one.txt"); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("the source still exists after a move: %v", err)
	}
	if _, err := d.Stat(ctx, "moved/deep/two.txt"); err != nil {
		t.Fatalf("the destination is missing after a move: %v", err)
	}

	must(t, d.Copy(ctx, "moved/deep/two.txt", "copied/three.txt"))
	rc, err := d.Read(ctx, "copied/three.txt")
	if err != nil {
		t.Fatalf("read the copy: %v", err)
	}
	body, _ := io.ReadAll(rc)
	rc.Close()
	if string(body) != "one" {
		t.Fatalf("the copy holds %q, want %q", body, "one")
	}

	// ⚠⚠ Deleting a NON-EMPTY directory. SMB refuses it outright, and filex's
	// trash moves folders wholesale — a Delete that only worked on files would
	// leave the folder behind on every purge.
	must(t, d.Delete(ctx, "moved"))
	if _, err := d.Stat(ctx, "moved"); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("a non-empty directory survived Delete: %v", err)
	}
}

// A path that is not there must answer storage.ErrNotFound, not an opaque NT
// status — the callers above this branch on it.
func TestLive_MissingPathsAreNotFound(t *testing.T) {
	d := liveDriver(t)
	ctx := context.Background()

	if _, err := d.Stat(ctx, "nope/not-here.txt"); !errors.Is(err, storage.ErrNotFound) {
		t.Errorf("stat of a missing path = %v, want ErrNotFound", err)
	}
	if _, err := d.Read(ctx, "nope/not-here.txt"); !errors.Is(err, storage.ErrNotFound) {
		t.Errorf("read of a missing path = %v, want ErrNotFound", err)
	}
	if _, err := d.List(ctx, "nope"); !errors.Is(err, storage.ErrNotFound) {
		t.Errorf("list of a missing folder = %v, want ErrNotFound", err)
	}
}

func TestLive_SetMtime(t *testing.T) {
	d := liveDriver(t)
	ctx := context.Background()
	must(t, d.Write(ctx, "touch.txt", strings.NewReader("x"), 1))

	want := time.Date(2021, 3, 4, 5, 6, 7, 0, time.UTC)
	if err := d.SetMtime(ctx, "touch.txt", want); err != nil {
		t.Fatalf("set mtime: %v", err)
	}
	st, err := d.Stat(ctx, "touch.txt")
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if diff := st.Mtime.UTC().Sub(want); diff > time.Second || diff < -time.Second {
		t.Fatalf("mtime = %s, want %s — sync would re-upload this file forever", st.Mtime.UTC(), want)
	}
}

// A wrong share name is the commonest mistake, and the server's own error
// (STATUS_BAD_NETWORK_NAME) means nothing to anybody. The driver has to name it.
func TestLive_WrongShareSaysWhichShare(t *testing.T) {
	addr := os.Getenv("FILEX_SMB_TEST")
	if addr == "" {
		t.Skip("set FILEX_SMB_TEST to run the live SMB tests")
	}
	host, port, _ := strings.Cut(addr, ":")
	d := &Driver{}
	must(t, d.Init(context.Background(), map[string]any{
		"host": host, "port": port, "share": "no-such-share",
		"user": envOr("FILEX_SMB_USER", "filex"), "password": envOr("FILEX_SMB_PASS", "filexpass"),
	}))
	defer d.Close()

	_, err := d.List(context.Background(), "")
	if err == nil {
		t.Fatal("a share that does not exist mounted anyway")
	}
	if !strings.Contains(err.Error(), "no-such-share") {
		t.Fatalf("error %q does not name the share the operator typed", err)
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}
