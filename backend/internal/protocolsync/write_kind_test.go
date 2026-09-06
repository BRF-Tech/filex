package protocolsync

// The protocol surfaces — WebDAV PUT, S3 PutObject and CopyObject, SFTP,
// FTPS, NFS, plus the AI/MCP write and the archive extractor — all reach the
// bytes through Syncer.Write. That makes this ONE test the answer for all of
// them: whether a write announced itself as a new file or as an edit of one
// that was already there.

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/notify"
	"github.com/brf-tech/filex/backend/internal/writehook"
)

type kindSink struct {
	notify.Service
	mu     sync.Mutex
	events []notify.EventType
}

func (k *kindSink) Send(_ context.Context, e notify.Event) (int64, error) {
	k.mu.Lock()
	k.events = append(k.events, e.Event)
	k.mu.Unlock()
	return 1, nil
}

// wait blocks until at least n events have arrived (emission is a goroutine).
func (k *kindSink) wait(t *testing.T, n int) []notify.EventType {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		k.mu.Lock()
		got := append([]notify.EventType(nil), k.events...)
		k.mu.Unlock()
		if len(got) >= n {
			return got
		}
		if time.Now().After(deadline) {
			t.Fatalf("wanted %d events within 2s, saw %v", n, got)
			return nil
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func installWriteSink(t *testing.T) *kindSink {
	t.Helper()
	k := &kindSink{}
	writehook.Configure(nil, k)
	t.Cleanup(func() { writehook.Configure(nil, nil) })
	return k
}

// The first write to a path creates it; the second replaces its bytes. On main
// both announced themselves as file.uploaded, so no subscriber could tell an
// arriving document from an edited one — over WebDAV, S3, SFTP, FTPS and NFS
// alike, since they share this call.
func TestWriteAnnouncesCreatedThenReplaced(t *testing.T) {
	installEmitter(t)
	sink := installWriteSink(t)
	s, st := newSyncer(t)

	require.True(t, s.Write(context.Background(), st, "docs/a.txt", 4, "text/plain"))
	assert.Equal(t, []notify.EventType{notify.EventFileUploaded}, sink.wait(t, 1))

	require.True(t, s.Write(context.Background(), st, "docs/a.txt", 9, "text/plain"))
	assert.Equal(t,
		[]notify.EventType{notify.EventFileUploaded, notify.EventFileUpdated},
		sink.wait(t, 2))
}

// WriteRows reports the same fact without emitting anything, so the one caller
// that fires its own event (the AI move) can still say which kind it was.
func TestWriteRowsReportsTheKind(t *testing.T) {
	installEmitter(t)
	s, st := newSyncer(t)

	_, kind, ok := s.WriteRows(context.Background(), st, "docs/b.txt", 4, "text/plain")
	require.True(t, ok)
	assert.Equal(t, writehook.Created, kind)

	_, kind, ok = s.WriteRows(context.Background(), st, "docs/b.txt", 8, "text/plain")
	require.True(t, ok)
	assert.Equal(t, writehook.Replaced, kind)
}
