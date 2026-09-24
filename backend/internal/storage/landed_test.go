package storage

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

type landedFake struct {
	obj Object
	err error
}

func (f landedFake) Init(context.Context, map[string]any) error          { return nil }
func (f landedFake) Name() string                                        { return "landedfake" }
func (f landedFake) List(context.Context, string) ([]Object, error)      { return nil, nil }
func (f landedFake) Read(context.Context, string) (io.ReadCloser, error) { return nil, ErrNotFound }
func (f landedFake) Capabilities() Capabilities                          { return Capabilities{} }
func (f landedFake) Stat(context.Context, string) (Object, error)        { return f.obj, f.err }

func TestLanded_TakesWhatTheBackendReports(t *testing.T) {
	mt := time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC)
	size, etag, mtime := Landed(context.Background(),
		landedFake{obj: Object{Kind: KindFile, Size: 42, Etag: "abc", Mtime: mt}}, "a.txt", 7)
	assert.EqualValues(t, 42, size, "the size the backend holds, not the size the caller meant to send")
	assert.Equal(t, "abc", etag)
	assert.True(t, mtime.Equal(mt))
}

// A failed Stat must not hand back the REPLACED file's etag — there is none
// to hand back here, and "" is the value that reads as drift on the next scan.
func TestLanded_StatFailureIsAnEmptyEtagAndNow(t *testing.T) {
	before := time.Now()
	size, etag, mtime := Landed(context.Background(), landedFake{err: errors.New("503")}, "a.txt", 7)
	assert.EqualValues(t, 7, size)
	assert.Equal(t, "", etag)
	assert.False(t, mtime.Before(before))
}

// A backend with no modification time (and a driver that reports no etag at
// all, like the local one) still yields a usable timestamp.
func TestLanded_NoMtimeIsNow(t *testing.T) {
	before := time.Now()
	_, etag, mtime := Landed(context.Background(), landedFake{obj: Object{Kind: KindFile, Size: 3}}, "a.txt", 3)
	assert.Equal(t, "", etag)
	assert.False(t, mtime.Before(before))
}

// Whatever answers at p after a FILE write, a directory is not it.
func TestLanded_ADirectoryIsNotWhatLanded(t *testing.T) {
	size, etag, _ := Landed(context.Background(),
		landedFake{obj: Object{Kind: KindDirectory, Etag: "dir"}}, "a", 5)
	assert.EqualValues(t, 5, size)
	assert.Equal(t, "", etag)
}
