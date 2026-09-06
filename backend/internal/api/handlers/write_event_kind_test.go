package handlers_test

// A write announces WHICH KIND of write it was.
//
// Before this, every surface fired file.uploaded whether the bytes created a
// file or replaced one that was already there — so a subscriber could not tell
// "a new document arrived in this folder" from "somebody edited that document",
// and no filter could separate them because the two were literally the same
// event id. The editor was worse than that: it announced nothing at all.
//
// These tests drive the two browser surfaces end to end through the real
// endpoints. The protocol surfaces (WebDAV PUT, S3 PutObject, SFTP/FTPS/NFS)
// share one code path and are pinned in internal/protocolsync.

import (
	"context"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/notify"
	"github.com/brf-tech/filex/backend/internal/writehook"
)

// eventSink records every writehook emission. Emission is off the request
// goroutine, so readers wait rather than assume.
type eventSink struct {
	notify.Service
	mu     sync.Mutex
	events []notify.Event
}

func (s *eventSink) Send(_ context.Context, e notify.Event) (int64, error) {
	s.mu.Lock()
	s.events = append(s.events, e)
	s.mu.Unlock()
	return 1, nil
}

// waitForPath returns the event types emitted for one node path, in order,
// once at least `want` of them have arrived.
func (s *eventSink) waitForPath(t *testing.T, p string, want int) []notify.EventType {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		s.mu.Lock()
		var got []notify.EventType
		for _, e := range s.events {
			if e.Node != nil && e.Node.Path == p {
				got = append(got, e.Event)
			}
		}
		s.mu.Unlock()
		if len(got) >= want {
			return got
		}
		if time.Now().After(deadline) {
			t.Fatalf("wanted %d events for %s within 2s, saw %v", want, p, got)
			return nil
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// installEventSink wires the writehook's notify sink for one test. The AV half
// is deliberately left nil: what is under test here is the event.
func installEventSink(t *testing.T) *eventSink {
	t.Helper()
	s := &eventSink{}
	writehook.Configure(nil, s)
	t.Cleanup(func() { writehook.Configure(nil, nil) })
	return s
}

// The editor: a file it creates is file.uploaded, a save over that same file is
// file.updated. Both come out of the ONE `existing` lookup the handler already
// does to choose between an immediate and a debounced antivirus scan.
//
// ⚠ On main this test fails at the first assertion, not the second: the editor
// emitted no event whatsoever, so the count never reaches 1.
func TestSaveText_CreateThenSave_UploadedThenUpdated(t *testing.T) {
	// ⚠ Fixture FIRST: BuildRouter calls writehook.Configure with the Deps'
	// own (nil) notify service, so a sink installed before it is thrown away.
	f := newStagedFixtureWith(t, nil)
	sink := installEventSink(t)

	require.Equal(t, http.StatusOK, f.saveText(t, "main://notes.md", "first"))
	require.Equal(t, []notify.EventType{notify.EventFileUploaded},
		sink.waitForPath(t, "/notes.md", 1), "a file the editor created is an upload")

	require.Equal(t, http.StatusOK, f.saveText(t, "main://notes.md", "first, edited"))
	assert.Equal(t,
		[]notify.EventType{notify.EventFileUploaded, notify.EventFileUpdated},
		sink.waitForPath(t, "/notes.md", 2),
		"editing a file that already existed is an update, not a second upload")
}

// The browser upload form: same file name twice. The second POST replaces the
// bytes of a file that is already catalogued, so it is file.updated.
func TestManagerUpload_ReplacingAnExistingFileIsUpdated(t *testing.T) {
	// ⚠ Fixture FIRST: BuildRouter calls writehook.Configure with the Deps'
	// own (nil) notify service, so a sink installed before it is thrown away.
	f := newStagedFixtureWith(t, nil)
	sink := installEventSink(t)

	f.uploadMultipart(t, "report.txt", []byte("v1"))
	require.Equal(t, []notify.EventType{notify.EventFileUploaded},
		sink.waitForPath(t, "/report.txt", 1))

	f.uploadMultipart(t, "report.txt", []byte("v2 is longer"))
	assert.Equal(t,
		[]notify.EventType{notify.EventFileUploaded, notify.EventFileUpdated},
		sink.waitForPath(t, "/report.txt", 2),
		"re-uploading over an existing name replaces it; that is an update")
}

// The staged (large-file) path cannot ask the DB — its node row is published
// before the bytes move — so it asks the driver instead, one statement before
// the overwrite. The answer has to come out the same.
func TestStagedUpload_ReplacingAnExistingFileIsUpdated(t *testing.T) {
	// ⚠ Fixture FIRST: BuildRouter calls writehook.Configure with the Deps'
	// own (nil) notify service, so a sink installed before it is thrown away.
	f := newStagedFixtureWith(t, nil)
	sink := installEventSink(t)

	f.uploadStaged(t, "big.bin", []byte("first payload"))
	require.Equal(t, []notify.EventType{notify.EventFileUploaded},
		sink.waitForPath(t, "/big.bin", 1))

	f.uploadStaged(t, "big.bin", []byte("second payload, longer"))
	assert.Equal(t,
		[]notify.EventType{notify.EventFileUploaded, notify.EventFileUpdated},
		sink.waitForPath(t, "/big.bin", 2))
}
