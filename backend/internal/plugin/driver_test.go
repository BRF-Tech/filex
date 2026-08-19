package plugin_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/brf-tech/filex/backend/internal/plugin"
	"github.com/brf-tech/filex/backend/internal/storage"
)

func fullCaps() plugin.Capabilities {
	return plugin.Capabilities{Range: true, Write: true, Delete: true, Move: true, Copy: true, Mkdir: true, SetMtime: true, Watch: true}
}

// newDriver spins a fake plugin up and returns a driver already Init'ed
// against it.
func newDriver(t *testing.T, caps plugin.Capabilities) (*fakePlugin, storage.Driver) {
	t.Helper()
	f := newFakePlugin("fake", caps)
	t.Cleanup(f.Close)
	cl := f.client()
	t.Cleanup(cl.Close)
	d := plugin.NewDriver(staticHandle{c: cl, name: "fake"}, caps)
	if err := d.Init(context.Background(), map[string]any{"root": "/data"}); err != nil {
		t.Fatalf("init: %v", err)
	}
	return f, d
}

func TestDriverReadPath(t *testing.T) {
	f, d := newDriver(t, fullCaps())
	f.seed("docs/readme.md", "hello plugin")
	ctx := context.Background()

	objs, err := d.List(ctx, "docs")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(objs) != 1 || objs[0].Name != "readme.md" || objs[0].Size != 12 {
		t.Fatalf("unexpected listing: %+v", objs)
	}
	st, err := d.Stat(ctx, "docs/readme.md")
	if err != nil || st.Kind != storage.KindFile || st.Size != 12 {
		t.Fatalf("stat: %+v err=%v", st, err)
	}
	rc, err := d.Read(ctx, "docs/readme.md")
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	b, _ := io.ReadAll(rc)
	rc.Close()
	if string(b) != "hello plugin" {
		t.Fatalf("read body = %q", b)
	}
	if _, err := d.Stat(ctx, "nope.txt"); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("missing path should map to storage.ErrNotFound, got %v", err)
	}
}

// A plugin that declares no range support must still answer ranged reads
// correctly — the host emulates them. This is the difference between "slow"
// and "wrong", and only the second one is a bug.
func TestRangeIsEmulatedWhenThePluginLacksIt(t *testing.T) {
	for _, withRange := range []bool{true, false} {
		caps := fullCaps()
		caps.Range = withRange
		f, d := newDriver(t, caps)
		f.seed("f.bin", "0123456789")
		rr, ok := d.(storage.RangeReader)
		if !ok {
			t.Fatal("plugin driver must always implement RangeReader")
		}
		rc, err := rr.ReadRange(context.Background(), "f.bin", 3, 4)
		if err != nil {
			t.Fatalf("range=%v: %v", withRange, err)
		}
		b, _ := io.ReadAll(rc)
		rc.Close()
		if string(b) != "3456" {
			t.Fatalf("range=%v: got %q, want \"3456\"", withRange, b)
		}
		if withRange && f.rangeCalls == 0 {
			t.Fatal("a range-capable plugin should have received a Range header")
		}
		if !withRange && f.rangeCalls != 0 {
			t.Fatal("a plugin without range must not be sent a Range header")
		}
		// Past EOF is not an error (storage.RangeReader's contract).
		rc, err = rr.ReadRange(context.Background(), "f.bin", 99, -1)
		if err != nil {
			t.Fatalf("past EOF should not error, got %v", err)
		}
		n, _ := io.Copy(io.Discard, rc)
		rc.Close()
		if n != 0 {
			t.Fatalf("past EOF should be empty, got %d bytes", n)
		}
	}
}

func TestWriteAndDelete(t *testing.T) {
	f, d := newDriver(t, fullCaps())
	w, ok := d.(storage.Writer)
	if !ok {
		t.Fatal("writable plugin driver must implement storage.Writer")
	}
	ctx := context.Background()
	if err := w.Write(ctx, "new.txt", strings.NewReader("body"), 4); err != nil {
		t.Fatalf("write: %v", err)
	}
	if got := f.get("new.txt"); got == nil || string(got.data) != "body" {
		t.Fatalf("plugin did not receive the bytes: %+v", got)
	}
	if err := d.(storage.Deleter).Delete(ctx, "new.txt"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if f.get("new.txt") != nil {
		t.Fatal("file still there after delete")
	}
}

// A read-only plugin must not merely REFUSE writes — it must not look
// writable at all. filex decides what a storage can do by type-asserting
// storage.Writer at forty-odd call sites, so a driver that has the method
// and returns an error would be offered an upload button, a trash move and a
// version snapshot that all fail at the last moment.
func TestReadOnlyPluginIsNotAWriter(t *testing.T) {
	_, d := newDriver(t, plugin.Capabilities{Range: true})
	if _, ok := d.(storage.Writer); ok {
		t.Fatal("read-only plugin must NOT implement storage.Writer")
	}
	if _, ok := d.(storage.Deleter); ok {
		t.Fatal("read-only plugin must NOT implement storage.Deleter")
	}
	if _, ok := d.(storage.Mover); ok {
		t.Fatal("read-only plugin must NOT implement storage.Mover")
	}
	if _, ok := d.(storage.Mkdirer); ok {
		t.Fatal("read-only plugin must NOT implement storage.Mkdirer")
	}
	caps := storage.ComputeCapabilities(d)
	if caps.Write || caps.Delete {
		t.Fatalf("capabilities disagree with the interfaces: %+v", caps)
	}
}

// Same rule for the optional halves: a plugin that cannot set an mtime must
// not satisfy storage.Toucher, because the caller cannot tell "applied" from
// "pretended" — only "supported" from "not supported".
func TestOptionalInterfacesFollowCapabilities(t *testing.T) {
	caps := fullCaps()
	caps.SetMtime, caps.Watch = false, false
	_, d := newDriver(t, caps)
	if _, ok := d.(storage.Toucher); ok {
		t.Fatal("plugin without set_mtime must not implement storage.Toucher")
	}
	if _, ok := d.(storage.Watcher); ok {
		t.Fatal("plugin without watch must not implement storage.Watcher")
	}

	caps.SetMtime, caps.Watch = true, true
	f, d2 := newDriver(t, caps)
	f.seed("a.txt", "x")
	tt, ok := d2.(storage.Toucher)
	if !ok {
		t.Fatal("plugin with set_mtime must implement storage.Toucher")
	}
	when := time.Unix(1600000000, 0).UTC()
	if err := tt.SetMtime(context.Background(), "a.txt", when); err != nil {
		t.Fatalf("set mtime: %v", err)
	}
	if got := f.get("a.txt"); !got.mtime.Equal(when) {
		t.Fatalf("mtime not applied: %v", got.mtime)
	}
}

// move/copy are emulated when the plugin does not have them, so a plugin
// author writes them only when the backend can beat a byte copy.
func TestMoveAndCopyAreEmulated(t *testing.T) {
	caps := fullCaps()
	caps.Move, caps.Copy = false, false
	f, d := newDriver(t, caps)
	f.seed("src.txt", "payload")
	ctx := context.Background()

	if err := d.(storage.Copier).Copy(ctx, "src.txt", "copy.txt"); err != nil {
		t.Fatalf("copy: %v", err)
	}
	if got := f.get("copy.txt"); got == nil || string(got.data) != "payload" {
		t.Fatalf("emulated copy did not produce the file: %+v", got)
	}
	if err := d.(storage.Mover).Move(ctx, "src.txt", "moved.txt"); err != nil {
		t.Fatalf("move: %v", err)
	}
	if f.get("src.txt") != nil {
		t.Fatal("emulated move left the source behind")
	}
	if got := f.get("moved.txt"); got == nil || string(got.data) != "payload" {
		t.Fatalf("emulated move lost the bytes: %+v", got)
	}
}

// A plugin restart is invisible: the host re-creates its instance from the
// config it saved and retries the call once.
func TestInstanceIsRecreatedAfterAPluginRestart(t *testing.T) {
	f, d := newDriver(t, fullCaps())
	f.seed("keep.txt", "still here")
	ctx := context.Background()

	f.mu.Lock()
	f.forgetAll = true
	creationsBefore := f.creates
	f.mu.Unlock()

	objs, err := d.List(ctx, "")
	if err != nil {
		t.Fatalf("list after restart: %v", err)
	}
	if len(objs) != 1 || objs[0].Name != "keep.txt" {
		t.Fatalf("listing after restart: %+v", objs)
	}
	f.mu.Lock()
	creationsAfter := f.creates
	f.mu.Unlock()
	if creationsAfter != creationsBefore+1 {
		t.Fatalf("expected exactly one re-init, got %d", creationsAfter-creationsBefore)
	}
}

// Init must fail loudly when the plugin refuses the config: the admin's
// "test connection" and the server's pre-warm both depend on it.
func TestInitSurfacesTheConfigError(t *testing.T) {
	f := newFakePlugin("fake", fullCaps())
	defer f.Close()
	cl := f.client()
	defer cl.Close()
	d := plugin.NewDriver(staticHandle{c: cl, name: "fake"}, fullCaps())
	err := d.Init(context.Background(), map[string]any{"root": "boom"})
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("want the plugin's own message, got %v", err)
	}
}

// When the plugin is not running at all, every operation says which plugin
// and why — not "unknown driver" or a nil dereference.
func TestUnavailablePluginNamesItself(t *testing.T) {
	d := plugin.NewDriver(staticHandle{name: "fake", err: errors.New("plugin fake is failed: exit status 1")}, fullCaps())
	err := d.Init(context.Background(), map[string]any{})
	if err == nil || !strings.Contains(err.Error(), "plugin:fake") || !strings.Contains(err.Error(), "exit status 1") {
		t.Fatalf("error should name the plugin and the reason, got %v", err)
	}
}

func TestWatchStreamsEvents(t *testing.T) {
	f, d := newDriver(t, fullCaps())
	w, ok := d.(storage.Watcher)
	if !ok {
		t.Fatal("watch-capable plugin must implement storage.Watcher")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ch, err := w.Subscribe(ctx)
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	f.events <- plugin.Event{Op: "create", Path: "new.txt"}
	select {
	case ev := <-ch:
		if ev.Op != "create" || ev.Path != "new.txt" {
			t.Fatalf("unexpected event %+v", ev)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no event arrived")
	}
}

// The write path buffers small bodies so a restart mid-call is retried with
// the same bytes rather than a consumed reader.
func TestSmallWriteSurvivesARestart(t *testing.T) {
	f, d := newDriver(t, fullCaps())
	f.mu.Lock()
	f.forgetAll = true
	f.mu.Unlock()
	err := d.(storage.Writer).Write(context.Background(), "x.txt", bytes.NewReader([]byte("abc")), 3)
	if err != nil {
		t.Fatalf("write across a restart: %v", err)
	}
	if got := f.get("x.txt"); got == nil || string(got.data) != "abc" {
		t.Fatalf("bytes did not land: %+v", got)
	}
}
