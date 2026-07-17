package queue_test

// Tests for the antivirus_scan job ("Koru" v0.4): eligibility gating,
// zero side effects on clean files, and the infected flow — quarantine
// move into `.filex-trash/`, DB soft-delete retag, and the
// `file.infected` event landing on a notify test-double.

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/notify"
	"github.com/brf-tech/filex/backend/internal/queue"
	"github.com/brf-tech/filex/backend/internal/storage"
)

// fakeAVStore implements queue.AVNodeStore, recording retag calls.
type fakeAVStore struct {
	nodes  map[int64]*model.Node
	retags []retagCall
}

type retagCall struct {
	id                             int64
	trashPath, trashHash, origPath string
}

func (f *fakeAVStore) GetNode(_ context.Context, id int64) (*model.Node, error) {
	n, ok := f.nodes[id]
	if !ok {
		return nil, errors.New("not found")
	}
	return n, nil
}

func (f *fakeAVStore) SoftDeleteAndRetag(_ context.Context, id int64, trashPath, trashHash, origPath string) error {
	f.retags = append(f.retags, retagCall{id: id, trashPath: trashPath, trashHash: trashHash, origPath: origPath})
	return nil
}

// fakeAVDriver serves fixed bytes and records Move calls.
type fakeAVDriver struct {
	files map[string][]byte
	moves [][2]string
}

func (f *fakeAVDriver) Init(context.Context, map[string]any) error { return nil }
func (f *fakeAVDriver) Name() string                               { return "fake-av" }
func (f *fakeAVDriver) List(context.Context, string) ([]storage.Object, error) {
	return nil, storage.ErrUnsupported
}
func (f *fakeAVDriver) Stat(context.Context, string) (storage.Object, error) {
	return storage.Object{}, storage.ErrUnsupported
}
func (f *fakeAVDriver) Read(_ context.Context, path string) (io.ReadCloser, error) {
	b, ok := f.files[path]
	if !ok {
		return nil, storage.ErrNotFound
	}
	return io.NopCloser(bytes.NewReader(b)), nil
}
func (f *fakeAVDriver) Move(_ context.Context, src, dst string) error {
	f.moves = append(f.moves, [2]string{src, dst})
	return nil
}
func (f *fakeAVDriver) Capabilities() storage.Capabilities { return storage.Capabilities{} }

// fakeAVScanner flags paths whose content contains "VIRUS".
type fakeAVScanner struct {
	err error
}

func (f *fakeAVScanner) Supports() bool { return true }
func (f *fakeAVScanner) Scan(_ context.Context, r io.Reader) (bool, string, error) {
	if f.err != nil {
		return false, "", f.err
	}
	b, _ := io.ReadAll(r)
	if bytes.Contains(b, []byte("VIRUS")) {
		return true, "Test-Signature", nil
	}
	return false, "", nil
}

// captureNotify is a notify.Service test-double recording Send calls.
type captureNotify struct {
	mu     sync.Mutex
	events []notify.Event
}

func (c *captureNotify) Send(_ context.Context, e notify.Event) (int64, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.events = append(c.events, e)
	return int64(len(c.events)), nil
}
func (c *captureNotify) List(context.Context, *int64, bool, int, int) ([]*model.Notification, int64, error) {
	return nil, 0, nil
}
func (c *captureNotify) UnreadCount(context.Context, *int64) (int64, error) { return 0, nil }
func (c *captureNotify) MarkRead(context.Context, int64, *int64) error      { return nil }
func (c *captureNotify) MarkAllRead(context.Context, *int64) error          { return nil }
func (c *captureNotify) GetSettings(context.Context, int64) (*model.NotificationSettings, error) {
	return nil, nil
}
func (c *captureNotify) UpsertSettings(context.Context, *model.NotificationSettings) error {
	return nil
}
func (c *captureNotify) SetWebhook(string, string)     {}
func (c *captureNotify) WebhookConfig() (string, bool) { return "", false }
func (c *captureNotify) TestTarget(context.Context, *model.WebhookTarget) notify.TargetDeliveryStatus {
	return notify.TargetDeliveryStatus{}
}
func (c *captureNotify) TargetStatuses() map[int64]notify.TargetDeliveryStatus { return nil }
func (c *captureNotify) Wait()                                                 {}
func (c *captureNotify) Stop()                                                 {}

func avFileNode(id int64, path string, size int64) *model.Node {
	return &model.Node{
		ID:         id,
		StorageID:  1,
		Name:       path[strings.LastIndex(path, "/")+1:],
		Path:       path,
		StorageKey: strings.TrimPrefix(path, "/"),
		Type:       model.NodeTypeFile,
		Size:       size,
		Mime:       "application/octet-stream",
	}
}

func TestAntivirusScan_EligibleGating(t *testing.T) {
	job := queue.NewAntivirusScanner(&fakeAVStore{}, nil, &fakeAVScanner{}, nil, nil, 1024)

	assert.True(t, job.Eligible(avFileNode(1, "/dir/a.bin", 100)))

	// Over the size cap.
	assert.False(t, job.Eligible(avFileNode(1, "/big.bin", 4096)))
	// Empty file.
	assert.False(t, job.Eligible(avFileNode(1, "/empty.bin", 0)))
	// Trash + version artifacts are never re-scanned.
	assert.False(t, job.Eligible(avFileNode(1, "/.filex-trash/1-abc__a.bin", 100)))
	assert.False(t, job.Eligible(avFileNode(1, "/.versions/7/1", 100)))
	// Directories never.
	dir := &model.Node{ID: 2, Path: "/d", Type: model.NodeTypeDirectory, Size: 1}
	assert.False(t, job.Eligible(dir))
	// Soft-deleted rows never.
	del := avFileNode(3, "/gone.bin", 10)
	now := time.Now()
	del.DeletedAt = &now
	assert.False(t, job.Eligible(del))
}

func TestAntivirusScan_CleanNoSideEffects(t *testing.T) {
	n := avFileNode(5, "/docs/report.pdf", 64)
	st := &fakeAVStore{nodes: map[int64]*model.Node{5: n}}
	drv := &fakeAVDriver{files: map[string][]byte{n.StorageKey: []byte("clean content")}}
	sink := &captureNotify{}
	job := queue.NewAntivirusScanner(st, func(int64) (storage.Driver, error) { return drv, nil },
		&fakeAVScanner{}, sink, nil, 0)

	err := job.Handle(context.Background(), queue.Op{
		Type: queue.TypeAntivirusScan, Payload: map[string]any{"node_id": float64(5)}})
	require.NoError(t, err)

	assert.Empty(t, st.retags, "clean file must not be retagged")
	assert.Empty(t, drv.moves, "clean file must not be moved")
	assert.Empty(t, sink.events, "clean file must not emit events")
}

func TestAntivirusScan_InfectedQuarantinesAndEmits(t *testing.T) {
	n := avFileNode(7, "/inbox/malware.exe", 128)
	st := &fakeAVStore{nodes: map[int64]*model.Node{7: n}}
	drv := &fakeAVDriver{files: map[string][]byte{n.StorageKey: []byte("VIRUS payload")}}
	sink := &captureNotify{}
	job := queue.NewAntivirusScanner(st, func(int64) (storage.Driver, error) { return drv, nil },
		&fakeAVScanner{}, sink, nil, 0)

	err := job.Handle(context.Background(), queue.Op{
		Type: queue.TypeAntivirusScan, Payload: map[string]any{"node_id": int64(7)}})
	require.NoError(t, err)

	// Bytes moved into the trash prefix.
	require.Len(t, drv.moves, 1)
	assert.Equal(t, n.StorageKey, drv.moves[0][0])
	assert.True(t, strings.HasPrefix(drv.moves[0][1], ".filex-trash/"),
		"quarantine destination must live under .filex-trash/, got %q", drv.moves[0][1])
	assert.True(t, strings.HasSuffix(drv.moves[0][1], "__malware.exe"))

	// DB row retagged to the trash location, original path preserved.
	require.Len(t, st.retags, 1)
	assert.EqualValues(t, 7, st.retags[0].id)
	assert.Equal(t, "/inbox/malware.exe", st.retags[0].origPath)
	assert.True(t, strings.HasPrefix(st.retags[0].trashPath, "/.filex-trash/"))
	assert.NotEmpty(t, st.retags[0].trashHash)

	// file.infected event with the signature in meta.
	require.Len(t, sink.events, 1)
	ev := sink.events[0]
	assert.Equal(t, notify.EventFileInfected, ev.Event)
	assert.Equal(t, notify.SeverityWarning, ev.Severity)
	assert.Equal(t, "Test-Signature", ev.Meta["signature"])
	assert.Equal(t, true, ev.Meta["quarantined"])
	require.NotNil(t, ev.Node)
	assert.Equal(t, "/inbox/malware.exe", ev.Node.Path)
}

func TestAntivirusScan_HandleSkipsGracefully(t *testing.T) {
	job := queue.NewAntivirusScanner(&fakeAVStore{nodes: map[int64]*model.Node{}}, nil,
		&fakeAVScanner{}, nil, nil, 0)

	// Missing payload + vanished nodes resolve as done, not failures.
	assert.NoError(t, job.Handle(context.Background(), queue.Op{Type: queue.TypeAntivirusScan}))
	assert.NoError(t, job.Handle(context.Background(), queue.Op{
		Type: queue.TypeAntivirusScan, Payload: map[string]any{"node_id": float64(404)}}))

	// Scanner failure IS an error → the queue's retry budget applies.
	n := avFileNode(9, "/x.bin", 10)
	st := &fakeAVStore{nodes: map[int64]*model.Node{9: n}}
	drv := &fakeAVDriver{files: map[string][]byte{n.StorageKey: []byte("data")}}
	job = queue.NewAntivirusScanner(st, func(int64) (storage.Driver, error) { return drv, nil },
		&fakeAVScanner{err: errors.New("clamd down")}, nil, nil, 0)
	err := job.Handle(context.Background(), queue.Op{
		Type: queue.TypeAntivirusScan, Payload: map[string]any{"node_id": int64(9)}})
	assert.Error(t, err)
	assert.Empty(t, st.retags)
}

func TestAntivirusScan_EnqueueOnlyEligible(t *testing.T) {
	ctx := context.Background()
	drv := setupSQLite(t)
	job := queue.NewAntivirusScanner(&fakeAVStore{}, nil, &fakeAVScanner{}, nil, nil, 1024)

	job.Enqueue(ctx, drv, avFileNode(1, "/a.bin", 100)) // eligible
	job.Enqueue(ctx, drv, avFileNode(2, "/big.bin", 4096))
	job.Enqueue(ctx, drv, avFileNode(3, "/.filex-trash/1-x__a.bin", 100))

	ops, total, err := drv.List(ctx, queue.StatusPending, 10, 0)
	require.NoError(t, err)
	require.EqualValues(t, 1, total)
	require.Len(t, ops, 1)
	assert.Equal(t, queue.TypeAntivirusScan, ops[0].Type)
	assert.EqualValues(t, 1, ops[0].Payload["node_id"])
}
