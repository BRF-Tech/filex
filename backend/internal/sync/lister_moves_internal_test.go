package sync

import (
	"context"
	"io"
	"path"
	"testing"

	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/rowgate"
	"github.com/brf-tech/filex/backend/internal/scanrule"
	"github.com/brf-tech/filex/backend/internal/storage"
)

// pictureDriver is an object store whose one-pass listing (WalkTree) is a
// picture taken before a rename and whose List is the storage after it.
type pictureDriver struct {
	tree []storage.Object
	live map[string][]storage.Object
	// during runs while the picture is being taken.
	during func()
	lists  int
}

func (d *pictureDriver) Init(context.Context, map[string]any) error { return nil }
func (d *pictureDriver) Name() string                               { return "picture-test" }
func (d *pictureDriver) Capabilities() storage.Capabilities {
	return storage.Capabilities{Read: true}
}
func (d *pictureDriver) List(_ context.Context, p string) ([]storage.Object, error) {
	d.lists++
	return d.live[path.Clean("/"+p)], nil
}
func (d *pictureDriver) Stat(context.Context, string) (storage.Object, error) {
	return storage.Object{}, storage.ErrNotFound
}
func (d *pictureDriver) Read(context.Context, string) (io.ReadCloser, error) {
	return nil, storage.ErrNotFound
}
func (d *pictureDriver) WalkTree(_ context.Context, _ string, fn func(storage.Object) error) error {
	if d.during != nil {
		d.during()
	}
	for _, o := range d.tree {
		if err := fn(o); err != nil {
			return err
		}
	}
	return nil
}

func newPicture() *pictureDriver {
	return &pictureDriver{
		tree: []storage.Object{{Path: "/klasor", Name: "klasor", Kind: storage.KindDirectory}},
		live: map[string][]storage.Object{"/": {{Path: "/yeni", Name: "yeni", Kind: storage.KindDirectory}}},
	}
}

func pictureNames(objs []storage.Object) []string {
	out := []string{}
	for _, o := range objs {
		out = append(out, o.Name)
	}
	return out
}

func pictureSyncer(id int64, d *pictureDriver) *storageSyncer {
	return &storageSyncer{
		storage: &model.Storage{ID: id, Name: "resim"},
		driver:  d,
		rule:    &scanrule.Rule{},
	}
}

// Issue #192: a walk reads an object store from a picture taken up front.
// Once a rename (a two-step change, rowgate.Move) has finished on the storage
// since that picture, the picture shows the folder at its OLD name while its
// rows are at the new one - and the walk would catalogue it again, as a copy.
// From then on, every directory is asked of the storage itself.
//
// Break: in lister, answer from the picture whatever rowgate.Moves says.
func TestLister_AsksTheStorageOnceAMoveHasFinished(t *testing.T) {
	const id = 9_200_001
	ctx := context.Background()
	d := newPicture()
	list := pictureSyncer(id, d).lister(ctx, "/")

	got, err := list(ctx, "/")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Name != "klasor" || d.lists != 0 {
		t.Fatalf("before any change the walk reads the picture: got %v, %d listings", pictureNames(got), d.lists)
	}

	rowgate.Move(id)() // a rename finishes on the storage
	got, err = list(ctx, "/")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Name != "yeni" || d.lists != 1 {
		t.Fatalf("after a rename finished the walk still read the picture: got %v, %d listings", pictureNames(got), d.lists)
	}
}

// A rename that finishes WHILE the picture is being taken counts too: the
// count the walk compares with is read before the picture.
//
// Break: read rowgate.Moves after prefetchTree in lister.
func TestLister_AMoveDuringThePictureCounts(t *testing.T) {
	const id = 9_200_002
	ctx := context.Background()
	d := newPicture()
	d.during = func() { rowgate.Move(id)() }
	list := pictureSyncer(id, d).lister(ctx, "/")

	got, err := list(ctx, "/")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Name != "yeni" || d.lists != 1 {
		t.Fatalf("a picture taken across a rename was trusted: got %v, %d listings", pictureNames(got), d.lists)
	}
}
