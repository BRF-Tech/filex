package handlers

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/storage"
	"github.com/brf-tech/filex/backend/internal/storage/drivers/local"
	"github.com/brf-tech/filex/backend/internal/testutil/dbtest"
)

// progressLog keeps every unit an archive job reported, in order.
type progressLog struct {
	mu   sync.Mutex
	seen []int
}

func (l *progressLog) report(done int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.seen = append(l.seen, done)
}

// inside counts the reports strictly between lo and hi.
func (l *progressLog) inside(lo, hi int) int {
	n := 0
	for _, v := range l.seen {
		if v > lo && v < hi {
			n++
		}
	}
	return n
}

// archiveSources writes the files under src/ of a fresh local storage and
// returns a handler over it with those files as resolved members.
func archiveSources(t *testing.T, files map[string][]byte) (*Archive, *model.Storage, []archiveMember) {
	t.Helper()
	_, store := dbtest.NewTestDB(t)
	root := t.TempDir()
	drv := &local.Driver{}
	require.NoError(t, drv.Init(context.Background(), map[string]any{"root": root}))
	st, err := store.CreateStorage(context.Background(), &model.Storage{
		Name: "main", Driver: "local", MountPath: "/data", Enabled: true,
		ConfigJSON: json.RawMessage(`{"root":"` + filepath.ToSlash(root) + `"}`),
	})
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(filepath.Join(root, "src"), 0o700))
	var members []archiveMember
	for name, body := range files {
		require.NoError(t, os.WriteFile(filepath.Join(root, "src", name), body, 0o600))
		members = append(members, archiveMember{StorageID: st.ID, Path: "src/" + name, Name: name, Size: int64(len(body))})
	}
	return NewArchive(store, func(int64) (storage.Driver, error) { return drv, nil }), st, members
}

// Issue #27, for archives. On a remote storage most of an archive job is
// reading its members: 175 files off S3 were ~100 of the 118 seconds of a
// production run (2026-09-25). That phase was worth 0-10 of the job's 100
// units, counted per member, and the built-in ZIP and the destination write
// reported nothing at all — so the operations centre read "0%" beside an
// empty ring for as long as the job ran, and the person who started it saw
// no sign of work. Every phase has to move while it runs.
func TestArchiveCreateProgressMovesWhileEachPhaseRuns(t *testing.T) {
	big := make([]byte, 4<<20)
	_, err := rand.Read(big) // incompressible: the written archive is as big as its member
	require.NoError(t, err)
	h, st, members := archiveSources(t, map[string][]byte{"big.bin": big})

	var log progressLog
	_, err = h.createArchive(context.Background(), archiveCreateRequest{}, "zip", st.ID, "out/big.zip", members, log.report)
	require.NoError(t, err)

	require.NotEmpty(t, log.seen)
	for i := 1; i < len(log.seen); i++ {
		require.Greater(t, log.seen[i], log.seen[i-1], "progress never stands still or goes back: %v", log.seen)
	}
	assert.Equal(t, 100, log.seen[len(log.seen)-1])
	assert.GreaterOrEqual(t, log.inside(0, archiveStagedAt), 10, "reading the member moves the bar: %v", log.seen)
	assert.GreaterOrEqual(t, log.inside(archiveStagedAt, archiveCompressedAt), 10, "compressing moves the bar: %v", log.seen)
	assert.GreaterOrEqual(t, log.inside(archiveCompressedAt, 100), 3, "writing the archive moves the bar: %v", log.seen)
}

// A folder of empty files has no bytes to count; its members still do.
func TestArchiveCreateProgressCountsEmptyMembers(t *testing.T) {
	h, st, members := archiveSources(t, map[string][]byte{"a.txt": nil, "b.txt": nil, "c.txt": nil})

	var log progressLog
	_, err := h.createArchive(context.Background(), archiveCreateRequest{}, "zip", st.ID, "out/empty.zip", members, log.report)
	require.NoError(t, err)

	assert.GreaterOrEqual(t, log.inside(0, archiveStagedAt), 2, "each staged member moves the bar: %v", log.seen)
	assert.Contains(t, log.seen, archiveStagedAt, "staging ends where compression starts: %v", log.seen)
	assert.Equal(t, 100, log.seen[len(log.seen)-1])
}

// The phases share one bar: 7-Zip's percentage lands inside compression's
// band, a byte count inside its own, and nothing is reported twice or backwards
// — 7-Zip's two streams report from two goroutines at once.
func TestArchivePhaseProgress(t *testing.T) {
	var log progressLog
	p := newArchiveProgress(log.report)

	p.span(0, archiveStagedAt, 0, 1000)
	p.span(0, archiveStagedAt, 500, 1000)
	p.span(0, archiveStagedAt, 400, 1000) // a retried read rewinds; the bar does not
	p.span(0, archiveStagedAt, 1000, 1000)
	p.span(archiveStagedAt, archiveCompressedAt, 50, 100)
	p.span(archiveCompressedAt, archiveWrittenAt, 5000, 1000) // more bytes than announced stay in the band
	assert.Equal(t, []int{archiveStagedAt / 2, archiveStagedAt, (archiveStagedAt + archiveCompressedAt) / 2, archiveWrittenAt}, log.seen)

	var wg sync.WaitGroup
	var race progressLog
	q := newArchiveProgress(race.report)
	for g := 0; g < 4; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := int64(0); i <= 100; i++ {
				q.span(archiveStagedAt, archiveCompressedAt, i, 100)
			}
		}()
	}
	wg.Wait()
	require.NotEmpty(t, race.seen)
	for i := 1; i < len(race.seen); i++ {
		require.Greater(t, race.seen[i], race.seen[i-1])
	}
	assert.Equal(t, archiveCompressedAt, race.seen[len(race.seen)-1])
}
