// Package gatetest catches a two-step change half way and asks whether the
// storage's row gate (internal/rowgate) is held there.
//
// A rename, a move, a delete into the trash and a delete for good change the
// storage first and the catalogue after. Every surface that makes one - the
// explorer, the operations queue, WebDAV, SFTP, FTPS, NFS, the S3 gateway, the
// AI/MCP surface, the drafts area - must hold the gate from the first byte to
// the last row, so a storage scan never judges the catalogue between the two
// (issues #192 and #201). Each surface's test drives its own verb over a
// Driver, which stops right after the bytes have changed, and HeldHalfWay
// checks the gate from there: one helper, so the surfaces are measured alike.
package gatetest

import (
	"context"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/brf-tech/filex/backend/internal/rowgate"
	"github.com/brf-tech/filex/backend/internal/storage"
	"github.com/brf-tech/filex/backend/internal/storage/drivers/local"
)

// Driver is a local driver whose Move and Delete stop half way: the bytes have
// changed, and the call does not return until Finish. Only the first such call
// stops; every one after Finish runs through. Its other methods (Write, Copy,
// Mkdir, ...) are the local driver's own, so seeding a fixture works as usual.
type Driver struct {
	*local.Driver

	reached, letGo         chan struct{}
	reachedOnce, letGoOnce sync.Once
	armed                  sync.Mutex
	on                     bool
}

// New is a Driver over a fresh directory, stopping from the start (Arm). The
// test's cleanup finishes whatever is still stopped, so a red test never
// leaves a change waiting for ever.
func New(t *testing.T) *Driver {
	t.Helper()
	base := &local.Driver{}
	if err := base.Init(context.Background(), map[string]any{"root": t.TempDir()}); err != nil {
		t.Fatalf("local driver: %v", err)
	}
	d := Wrap(base)
	t.Cleanup(d.Finish)
	return d
}

// Wrap is a Driver over an initialised local driver, stopping from the start.
// The caller finishes it (Finish) when the test ends.
func Wrap(base *local.Driver) *Driver {
	return &Driver{Driver: base, reached: make(chan struct{}), letGo: make(chan struct{}), on: true}
}

// Arm decides whether the next Move or Delete stops (on) or runs through (off,
// while a fixture is seeded through the same driver).
func (d *Driver) Arm(on bool) {
	d.armed.Lock()
	d.on = on
	d.armed.Unlock()
}

// Finish lets the stopped call return. Safe to call more than once.
func (d *Driver) Finish() { d.letGoOnce.Do(func() { close(d.letGo) }) }

// Reached is closed once a call has stopped half way.
func (d *Driver) Reached() <-chan struct{} { return d.reached }

func (d *Driver) halfWay() {
	d.armed.Lock()
	on := d.on
	d.armed.Unlock()
	if !on {
		return
	}
	d.reachedOnce.Do(func() { close(d.reached) })
	<-d.letGo
}

// Move moves the bytes, then stops (halfWay).
func (d *Driver) Move(ctx context.Context, src, dst string) error {
	err := d.Driver.Move(ctx, src, dst)
	if err == nil {
		d.halfWay()
	}
	return err
}

// Delete deletes the bytes, then stops (halfWay).
func (d *Driver) Delete(ctx context.Context, p string) error {
	err := d.Driver.Delete(ctx, p)
	if err == nil {
		d.halfWay()
	}
	return err
}

// View is how a test's resolver hands a Driver to the code under test: a
// view of it (NoTrash), in place of the Driver itself.
type View func(*Driver) storage.Driver

// Serve is d as a test's resolver hands it out: through the view when one is
// given (the last one, when several are), else d itself.
func (d *Driver) Serve(views ...View) storage.Driver {
	var out storage.Driver = d
	for _, v := range views {
		out = v(d)
	}
	return out
}

// NoTrash is d seen as a storage that cannot keep what it deletes: no Move
// and no Copy, so trash.Put answers trash.ErrUnsupported and a surface's
// delete becomes a delete for good (files.purge) - the branch a purge test
// measures. Its Delete is d's, so it stops half way like d's (and Arm,
// Finish and HeldHalfWay are asked of d); Write and Mkdir are d's too, so a
// fixture still seeds through the view.
func (d *Driver) NoTrash() storage.Driver { return noTrash{Driver: d, d: d} }

// noTrash shows storage.Driver's own methods, Write, Mkdir and Delete, and
// nothing else of the local driver.
type noTrash struct {
	storage.Driver
	d *Driver
}

// Capabilities says what the view can do: no Move, no Copy.
func (n noTrash) Capabilities() storage.Capabilities {
	c := n.d.Capabilities()
	c.Move, c.Copy = false, false
	return c
}

func (n noTrash) Write(ctx context.Context, p string, r io.Reader, size int64) error {
	return n.d.Write(ctx, p, r, size)
}

func (n noTrash) Mkdir(ctx context.Context, p string) error { return n.d.Mkdir(ctx, p) }

func (n noTrash) Delete(ctx context.Context, p string) error { return n.d.Delete(ctx, p) }

// HeldHalfWay waits until d has stopped half way through a change on
// storageID, then asks for the gate the way a storage scan does
// (rowgate.Judge). The scan must NOT get it while the change stands there; once
// the change is let go (Finish), it must, and rowsFollowed - asked while the
// scan holds the gate - must answer true: by the time the gate opened, the rows
// had followed the bytes.
//
// On code that makes the change without the gate, the scan gets the gate at
// once, half way, and the test fails here.
func HeldHalfWay(t *testing.T, storageID int64, d *Driver, rowsFollowed func() bool) {
	t.Helper()
	select {
	case <-d.Reached():
	case <-time.After(10 * time.Second):
		t.Fatal("the change never reached the storage")
	}
	judged := make(chan bool, 1)
	go func() {
		release := rowgate.Judge(storageID)
		ok := rowsFollowed()
		release()
		judged <- ok
	}()
	select {
	case <-judged:
		d.Finish()
		t.Fatal("a storage scan got the row gate while the change stood half way: the bytes had changed and the rows had not (rowgate)")
	case <-time.After(200 * time.Millisecond):
	}
	d.Finish()
	select {
	case ok := <-judged:
		if !ok {
			t.Error("the row gate opened before the rows followed the bytes")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the storage scan never got the row gate after the change finished")
	}
}

// ObjectStore is d seen as an object store: it walks a tree in one pass
// (storage.TreeWalker), so a folder moved or trashed on it is a long change
// that fences its prefixes instead of holding the storage's row gate
// (storage.ObjectByObject, rowgate.FenceCtx) - the branch FencedHalfWay
// measures. Everything else is d's: Move and Delete stop half way like d's,
// and Write, Copy and Mkdir seed a fixture as usual.
func (d *Driver) ObjectStore() storage.Driver { return objectStore{Driver: d} }

type objectStore struct{ *Driver }

// WalkTree lists below p directory by directory: what an object store's
// one-pass listing answers.
func (o objectStore) WalkTree(ctx context.Context, p string, fn func(storage.Object) error) error {
	return walkTree(ctx, o.Driver, p, fn)
}

// AsObjectStore is any driver seen as an object store (storage.TreeWalker),
// for a test whose change moves the bytes itself: only the shape counts.
func AsObjectStore(d storage.Driver) storage.Driver { return anyObjectStore{Driver: d} }

type anyObjectStore struct{ storage.Driver }

func (o anyObjectStore) WalkTree(ctx context.Context, p string, fn func(storage.Object) error) error {
	return walkTree(ctx, o.Driver, p, fn)
}

func walkTree(ctx context.Context, d storage.Driver, p string, fn func(storage.Object) error) error {
	objs, err := d.List(ctx, p)
	if err != nil {
		return nil
	}
	for _, o := range objs {
		if err := fn(o); err != nil {
			return err
		}
		if o.Kind == storage.KindDirectory {
			if err := walkTree(ctx, d, o.Path, fn); err != nil {
				return err
			}
		}
	}
	return nil
}

// FencedHalfWay is HeldHalfWay for a long change - a folder moved or trashed
// object by object (storage.ObjectByObject): it waits until d has stopped half
// way through a change on storageID, and then the storage's scan must get the
// gate AT ONCE (the change holds no gate), and see fenced - the folder's path
// and its destination's - fenced, and free not (the rest of the storage is
// scanned as usual). Once the change is let go (Finish) every fence must open,
// and rowsFollowed - asked under the gate after that - must answer true: by
// the time the fence opened, the rows had followed the bytes.
//
// On code that holds the storage's gate for the whole change, the scan does
// not get it half way, and the test fails here.
func FencedHalfWay(t *testing.T, storageID int64, d *Driver, fenced []string, free string, rowsFollowed func() bool) {
	t.Helper()
	select {
	case <-d.Reached():
	case <-time.After(10 * time.Second):
		t.Fatal("the change never reached the storage")
	}
	release, err := rowgate.JudgeWithin(context.Background(), storageID, 200*time.Millisecond)
	if err != nil {
		d.Finish()
		t.Fatalf("a long change held the storage's whole row gate half way (%v): the rest of the storage could not be scanned", err)
	}
	fences := rowgate.Fences(storageID)
	for _, p := range fenced {
		if !fences.Covers(p) {
			t.Errorf("%s is not fenced while the change stands half way: a scan would judge it", p)
		}
	}
	if free != "" && fences.Covers(free) {
		t.Errorf("%s is fenced, but the change does not touch it", free)
	}
	release()
	d.Finish()
	deadline := time.Now().Add(10 * time.Second)
	for !rowgate.Fences(storageID).Empty() {
		if time.Now().After(deadline) {
			t.Fatal("the fence never opened after the change finished")
		}
		time.Sleep(5 * time.Millisecond)
	}
	release = rowgate.Judge(storageID)
	ok := rowsFollowed()
	release()
	if !ok {
		t.Error("the fence opened before the rows followed the bytes")
	}
}
