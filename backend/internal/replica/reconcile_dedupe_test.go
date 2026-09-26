package replica

import (
	"context"
	"testing"

	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/queue"
)

// "Repair all" queues one retry per failure, once.
//
// ⚠ Every press queued a full set again: the retries had no coalescing key,
// so pressing twice (or once more because the list looked unchanged, which
// it does until a retry runs) doubled the queue and sent a second "retries
// queued" to the bell. A retry still waiting in the queue now absorbs the
// next request for the same failure, the answer says how many were already
// queued, and the bell hears only of new ones.

// pendingQueue is a queue.Driver that honours DedupKey the way the real
// drivers do: while a pending op holds a key, Enqueue refuses the key.
type pendingQueue struct {
	queue.Driver
	pending map[string]bool
	ops     []queue.Op
}

func (q *pendingQueue) Enqueue(_ context.Context, op queue.Op) (string, error) {
	if op.DedupKey != "" {
		if q.pending[op.DedupKey] {
			return "", queue.ErrDuplicate
		}
		q.pending[op.DedupKey] = true
	}
	q.ops = append(q.ops, op)
	return op.Type, nil
}

type failingStore struct {
	stubStore
	failures []*model.ReplicaFailure
}

func (s *failingStore) ListReplicaFailures(context.Context, bool, int, int) ([]*model.ReplicaFailure, int64, error) {
	return s.failures, int64(len(s.failures)), nil
}

func TestReconcileAll_ASecondPressQueuesNothingNew(t *testing.T) {
	store := &failingStore{failures: []*model.ReplicaFailure{
		{Path: "/a.txt", Op: "write"},
		{Path: "/b.txt", Op: "delete"},
	}}
	q := &pendingQueue{pending: map[string]bool{}}
	bell := &stubNotifier{}
	svc := New(store, nil, q, bell)

	got, err := svc.ReconcileAll(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got.Queued != 2 || got.AlreadyQueued != 0 {
		t.Fatalf("first press = %+v, want 2 queued", got)
	}

	got, err = svc.ReconcileAll(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got.Queued != 0 || got.AlreadyQueued != 2 {
		t.Fatalf("second press = %+v, want 0 queued and 2 already queued", got)
	}
	if len(q.ops) != 2 {
		t.Fatalf("%d retries in the queue after two presses, want 2", len(q.ops))
	}
	if bell.sends != 1 {
		t.Fatalf("the bell was told %d times, want once", bell.sends)
	}
}

func TestFixOne_ARetryAlreadyWaitingIsNotQueuedAgain(t *testing.T) {
	q := &pendingQueue{pending: map[string]bool{}}
	svc := New(&stubStore{}, nil, q, nil)

	queued, err := svc.FixOne(context.Background(), "/a.txt", "write")
	if err != nil || !queued {
		t.Fatalf("first = %v, %v; want queued", queued, err)
	}
	queued, err = svc.FixOne(context.Background(), "/a.txt", "write")
	if err != nil || queued {
		t.Fatalf("second = %v, %v; want not queued and no error", queued, err)
	}
	if len(q.ops) != 1 {
		t.Fatalf("%d retries queued, want 1", len(q.ops))
	}
}
