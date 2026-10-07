package storage

// The replication wrapper as the running server hands it out (#186).
//
// Until 0.53 the server never built one: these are the properties a wrapper
// must have once every write of a linked storage goes through it - the
// primary's own abilities kept (a disk keeps its modification times, a bucket
// its multipart uploads), a deleted file never served back from the backup,
// a change that reaches a retired wrapper recorded rather than lost, rules
// that match whichever way a path is spelled, and the pieces the initial copy
// and the repairs are built from.

import (
	"context"
	"errors"
	"io"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// touchFake is a fakeDriver that keeps modification times.
type touchFake struct {
	*fakeDriver
	touched atomic.Int32
}

func (f *touchFake) SetMtime(_ context.Context, p string, m time.Time) error {
	f.touched.Add(1)
	f.mu.Lock()
	defer f.mu.Unlock()
	o, ok := f.stats[p]
	if !ok {
		return ErrNotFound
	}
	o.Mtime = m
	f.stats[p] = o
	return nil
}

// presignFake is a fakeDriver with presigned links.
type presignFake struct{ *fakeDriver }

func (presignFake) PresignUpload(context.Context, string, int64) (PresignedUpload, error) {
	return PresignedUpload{URL: "https://bucket.example/up"}, nil
}
func (presignFake) PresignDownload(context.Context, string, time.Duration) (string, error) {
	return "https://bucket.example/down", nil
}

// partFake is a fakeDriver with server-side multipart uploads: the parts are
// kept until CompleteMultipart assembles them.
type partFake struct {
	*fakeDriver
	parts map[int][]byte
}

func (f *partFake) InitMultipart(context.Context, string, int64, int) (string, []string, error) {
	f.parts = map[int][]byte{}
	return "up-1", nil, nil
}
func (f *partFake) UploadPart(_ context.Context, _, _ string, n int, r io.Reader, _ int64) (string, error) {
	b, err := io.ReadAll(r)
	if err != nil {
		return "", err
	}
	f.parts[n] = b
	return "etag", nil
}
func (f *partFake) CompleteMultipart(_ context.Context, p string, _ string, parts []PartCompletion) error {
	var all []byte
	for _, pc := range parts {
		all = append(all, f.parts[pc.PartNumber]...)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.files[p] = all
	f.stats[p] = Object{Path: p, Size: int64(len(all))}
	return nil
}
func (f *partFake) AbortMultipart(context.Context, string, string) error { return nil }

// closeFake counts Close.
type closeFake struct {
	*fakeDriver
	closed atomic.Int32
}

func (f *closeFake) Close() error {
	f.closed.Add(1)
	return nil
}

type touchPartFake struct {
	*partFake
}

func (f touchPartFake) SetMtime(context.Context, string, time.Time) error { return nil }

func TestReplicatedShape_CarriesExactlyWhatThePrimaryCan(t *testing.T) {
	cases := []struct {
		name             string
		primary          Driver
		touch, pre, part bool
	}{
		{"bare", newFakeDriver("p"), false, false, false},
		{"disk keeps times", &touchFake{fakeDriver: newFakeDriver("p")}, true, false, false},
		{"bucket presigns", presignFake{newFakeDriver("p")}, false, true, false},
		{"bucket parts", &partFake{fakeDriver: newFakeDriver("p")}, false, false, true},
		{"times and parts", touchPartFake{&partFake{fakeDriver: newFakeDriver("p")}}, true, false, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			d := NewReplicated(c.primary, newFakeDriver("r"), DefaultRules(), &fakeRecorder{}, &fakeNotifier{}).Shaped()
			_, touch := d.(Toucher)
			_, pre := d.(Presigner)
			_, part := d.(PartUploader)
			assert.Equal(t, c.touch, touch, "Toucher")
			assert.Equal(t, c.pre, pre, "Presigner")
			assert.Equal(t, c.part, part, "PartUploader")
			for _, must := range []bool{
				func() bool { _, ok := d.(Writer); return ok }(),
				func() bool { _, ok := d.(Deleter); return ok }(),
				func() bool { _, ok := d.(Mover); return ok }(),
				func() bool { _, ok := d.(Copier); return ok }(),
				func() bool { _, ok := d.(RangeReader); return ok }(),
			} {
				assert.True(t, must, "every shape writes, deletes, moves, copies and reads ranges")
			}
			rd, ok := AsReplicated(d)
			require.True(t, ok, "the wrapper must be found inside its shape")
			assert.NotNil(t, rd)
		})
	}
	_, ok := AsReplicated(newFakeDriver("bare"))
	assert.False(t, ok, "a bare driver is not a wrapper")
}

func TestReplicatedShape_APresignedUploadIsRefused(t *testing.T) {
	d := NewReplicated(presignFake{newFakeDriver("p")}, newFakeDriver("r"), DefaultRules(), &fakeRecorder{}, &fakeNotifier{}).Shaped()
	ps := d.(Presigner)
	_, err := ps.PresignUpload(context.Background(), "a.bin", 10)
	assert.ErrorIs(t, err, ErrUnsupported, "a browser upload straight to the bucket would skip the fan-out")
	u, err := ps.PresignDownload(context.Background(), "a.bin", time.Minute)
	require.NoError(t, err)
	assert.Equal(t, "https://bucket.example/down", u)
}

func TestReplicatedShape_CompleteMultipartFansOut(t *testing.T) {
	primary := &partFake{fakeDriver: newFakeDriver("p")}
	replica := newFakeDriver("r")
	rd := NewReplicated(primary, replica, DefaultRules(), &fakeRecorder{}, &fakeNotifier{})
	pu := rd.Shaped().(PartUploader)
	ctx := context.Background()

	id, _, err := pu.InitMultipart(ctx, "big.bin", 6, 2)
	require.NoError(t, err)
	_, err = pu.UploadPart(ctx, "big.bin", id, 1, strings.NewReader("abc"), 3)
	require.NoError(t, err)
	_, err = pu.UploadPart(ctx, "big.bin", id, 2, strings.NewReader("def"), 3)
	require.NoError(t, err)
	require.NoError(t, pu.CompleteMultipart(ctx, "big.bin", id, []PartCompletion{{PartNumber: 1}, {PartNumber: 2}}))
	rd.Stop()

	assert.Equal(t, "abcdef", string(replica.files["big.bin"]), "an S3 upload in parts never reached the backup")
}

func TestReplicatedShape_SetMtimeReachesTheReplica(t *testing.T) {
	primary := &touchFake{fakeDriver: newFakeDriver("p")}
	replica := &touchFake{fakeDriver: newFakeDriver("r")}
	rd := NewReplicated(primary, replica, DefaultRules(), &fakeRecorder{}, &fakeNotifier{})
	d := rd.Shaped()
	ctx := context.Background()
	require.NoError(t, d.(Writer).Write(ctx, "a.txt", strings.NewReader("x"), 1))
	rd.Stop()
	before := replica.touched.Load()

	rd2 := NewReplicated(primary, replica, DefaultRules(), &fakeRecorder{}, &fakeNotifier{})
	when := time.Date(2020, 1, 2, 3, 4, 5, 0, time.UTC)
	require.NoError(t, rd2.Shaped().(Toucher).SetMtime(ctx, "a.txt", when))
	rd2.Stop()
	assert.Greater(t, replica.touched.Load(), before, "the time set on the primary never reached the backup")
	assert.Equal(t, when, replica.stats["a.txt"].Mtime)
}

// ⚠ A deleted file must not come back from the backup. In append_only the
// backup keeps every deleted file; a fallback on "not found" served it again
// and made its name read as taken.
func TestReplicated_NotFoundOnThePrimaryIsNotAnOutage(t *testing.T) {
	primary := newFakeDriver("p")
	replica := newFakeDriver("r")
	replica.files["gone.txt"] = []byte("old")
	replica.stats["gone.txt"] = Object{Path: "gone.txt", Size: 3}
	notif := &fakeNotifier{}
	rd := NewReplicated(primary, replica, DefaultRules(), &fakeRecorder{}, notif)
	defer rd.Stop()
	ctx := context.Background()

	_, err := rd.Stat(ctx, "gone.txt")
	assert.ErrorIs(t, err, ErrNotFound, "the backup's copy of a deleted file answered Stat")
	_, err = rd.Read(ctx, "gone.txt")
	assert.ErrorIs(t, err, ErrNotFound, "the backup's copy of a deleted file was served")
	_, err = rd.ReadRange(ctx, "gone.txt", 0, -1)
	assert.ErrorIs(t, err, ErrNotFound)
	assert.False(t, Exists(ctx, rd, "gone.txt"), "a deleted name reads as taken")
	assert.Equal(t, 0, notif.read, "a miss is not a primary outage")

	// An outage still falls back.
	primary.readErr = errors.New("connection refused")
	rc, err := rd.Read(ctx, "gone.txt")
	require.NoError(t, err)
	rc.Close()
	assert.Equal(t, 1, notif.read)
}

func TestReplicated_ReadRangeWithoutAPrimaryRangeReader(t *testing.T) {
	primary := newFakeDriver("p")
	primary.files["a.txt"] = []byte("0123456789")
	rd := NewReplicated(primary, newFakeDriver("r"), DefaultRules(), &fakeRecorder{}, &fakeNotifier{})
	defer rd.Stop()
	ctx := context.Background()

	rc, err := rd.ReadRange(ctx, "a.txt", 2, 3)
	require.NoError(t, err)
	b, _ := io.ReadAll(rc)
	rc.Close()
	assert.Equal(t, "234", string(b))

	rc, err = rd.ReadRange(ctx, "a.txt", 20, 3)
	require.NoError(t, err, "past the end is an empty answer, not an error")
	b, _ = io.ReadAll(rc)
	rc.Close()
	assert.Empty(t, b)
}

// A rule `temp/*` means "temp/x.tmp" and "/temp/x.tmp": filex writes paths
// both ways, and the anchored match missed every absolute one.
func TestReplicated_RulesMatchWhicheverWayAPathIsSpelled(t *testing.T) {
	primary := newFakeDriver("p")
	replica := newFakeDriver("r")
	rules := NewRulesEngine(func() ([]RuleSpec, ReplicaMode) {
		return []RuleSpec{{ID: 1, Pattern: "temp/*", Mode: ModeSkip, Priority: 1, Enabled: true}}, ModeMirror
	})
	rd := NewReplicated(primary, replica, rules, &fakeRecorder{}, &fakeNotifier{})
	require.NoError(t, rd.Write(context.Background(), "/temp/x.tmp", strings.NewReader("x"), 1))
	rd.Stop()
	assert.Equal(t, int32(0), replica.writeCount.Load(), "a skip rule did not hold for an absolute path")
	assert.Equal(t, ModeSkip, rd.Mode("/temp/x.tmp"))
}

func TestReplicated_AChangeReachingARetiredWrapperIsRecorded(t *testing.T) {
	primary := newFakeDriver("p")
	replica := newFakeDriver("r")
	rec := &fakeRecorder{}
	rd := NewReplicated(primary, replica, DefaultRules(), rec, &fakeNotifier{})
	rd.Stop()
	require.True(t, rd.Halted())

	require.NoError(t, rd.Write(context.Background(), "late.txt", strings.NewReader("x"), 1))
	assert.Equal(t, int32(1), primary.writeCount.Load(), "the user's write must still land")
	assert.Equal(t, int32(0), replica.writeCount.Load(), "a retired wrapper wrote to a replica being closed")
	require.Len(t, rec.recorded, 1, "the change was lost without a word")
	assert.Equal(t, "REPLICA_DRIVER_REPLACED", rec.recorded[0].Code)
	assert.Equal(t, "write", rec.recorded[0].Op)
}

func TestReplicated_CloseReleasesBothDriversAfterTheFanOuts(t *testing.T) {
	primary := &closeFake{fakeDriver: newFakeDriver("p")}
	replica := &closeFake{fakeDriver: newFakeDriver("r")}
	rd := NewReplicated(primary, replica, DefaultRules(), &fakeRecorder{}, &fakeNotifier{})
	require.NoError(t, rd.Write(context.Background(), "a.txt", strings.NewReader("x"), 1))
	require.NoError(t, rd.Close())
	assert.True(t, rd.Halted())
	select {
	case <-rd.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("the wrapper never released its drivers")
	}
	assert.Equal(t, int32(1), primary.closed.Load())
	assert.Equal(t, int32(1), replica.closed.Load())
	assert.Equal(t, int32(1), replica.writeCount.Load(), "the fan-out in flight was cut off by Close")
}

func TestReplicated_ReplicaHolds(t *testing.T) {
	ctx := context.Background()
	at := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	obj := Object{Path: "a.txt", Size: 3, Mtime: at}

	keeps := &touchFake{fakeDriver: newFakeDriver("r")}
	rd := NewReplicated(newFakeDriver("p"), keeps, DefaultRules(), &fakeRecorder{}, &fakeNotifier{})
	assert.False(t, rd.ReplicaHolds(ctx, obj), "nothing there yet")
	keeps.stats["a.txt"] = Object{Path: "a.txt", Size: 3, Mtime: at.Add(time.Second)}
	assert.True(t, rd.ReplicaHolds(ctx, obj), "same size, same time (a second apart is SMB rounding)")
	keeps.stats["a.txt"] = Object{Path: "a.txt", Size: 4, Mtime: at}
	assert.False(t, rd.ReplicaHolds(ctx, obj), "another size is another file")
	keeps.stats["a.txt"] = Object{Path: "a.txt", Size: 3, Mtime: at.Add(time.Hour)}
	assert.False(t, rd.ReplicaHolds(ctx, obj), "a target that keeps times must have the same one")

	bucket := newFakeDriver("r") // keeps no times: the upload's moment
	rd2 := NewReplicated(newFakeDriver("p"), bucket, DefaultRules(), &fakeRecorder{}, &fakeNotifier{})
	bucket.stats["a.txt"] = Object{Path: "a.txt", Size: 3, Mtime: at.Add(time.Hour)}
	assert.True(t, rd2.ReplicaHolds(ctx, obj), "an object store's copy made after the file's time is there")
	bucket.stats["a.txt"] = Object{Path: "a.txt", Size: 3, Mtime: at.Add(-time.Hour)}
	assert.False(t, rd2.ReplicaHolds(ctx, obj), "a copy older than the file is stale")
}

func TestReplicated_RepairCopiesAndResolves(t *testing.T) {
	primary := newFakeDriver("p")
	replica := newFakeDriver("r")
	primary.files["a.txt"] = []byte("abc")
	primary.stats["a.txt"] = Object{Path: "a.txt", Size: 3}
	rec := &fakeRecorder{}
	rd := NewReplicated(primary, replica, DefaultRules(), rec, &fakeNotifier{})
	defer rd.Stop()
	ctx := context.Background()

	require.NoError(t, rd.Repair(ctx, "a.txt", "move"))
	assert.Equal(t, "abc", string(replica.files["a.txt"]))
	assert.Contains(t, rec.resolved, "a.txt:move", "a repaired move left its failure open")

	require.NoError(t, rd.Repair(ctx, "never-there.txt", "write"), "a file the primary no longer has leaves nothing to repair")
	assert.Contains(t, rec.resolved, "never-there.txt:write")

	require.NoError(t, rd.Repair(ctx, "a.txt", "delete"))
	_, still := replica.files["a.txt"]
	assert.False(t, still)
	assert.Error(t, rd.Repair(ctx, "a.txt", "rename"), "an unknown op is refused")
}

func TestUnavailableReplica_SaysWhyOnEveryChange(t *testing.T) {
	primary := newFakeDriver("p")
	rec := &fakeRecorder{}
	rd := NewReplicated(primary, UnavailableDriver("smb", errors.New("smb: host and share required")), DefaultRules(), rec, &fakeNotifier{})
	ctx := context.Background()
	require.NoError(t, rd.Write(ctx, "a.txt", strings.NewReader("x"), 1))
	require.NoError(t, rd.Delete(ctx, "a.txt"))
	rd.Stop()
	require.Len(t, rec.recorded, 2)
	for _, f := range rec.recorded {
		assert.Equal(t, "REPLICA_UNAVAILABLE", f.Code)
		assert.Contains(t, f.Msg, "host and share required")
	}
	assert.Error(t, rd.ProbeReplica(ctx))
}
