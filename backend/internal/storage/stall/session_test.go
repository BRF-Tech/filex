package stall

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type fakeSession struct{ dead atomic.Bool }

func (s *fakeSession) Dead() bool { return s.dead.Load() }

// Many callers at once dial once; a dead session is dialed again; Forget and
// Take let go.
func TestOne_DialsOnceAndAgainOnlyForADeadSession(t *testing.T) {
	var o One[*fakeSession]
	var dials atomic.Int32
	release := make(chan struct{})
	dial := func(context.Context) (*fakeSession, error) {
		dials.Add(1)
		<-release
		return &fakeSession{}, nil
	}
	var wg sync.WaitGroup
	got := make([]*fakeSession, 8)
	for i := range got {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s, err := o.Current(context.Background(), dial)
			if err != nil {
				t.Error(err)
			}
			got[i] = s
		}()
	}
	time.Sleep(50 * time.Millisecond)
	close(release)
	wg.Wait()
	if n := dials.Load(); n != 1 {
		t.Fatalf("%d dials for 8 callers at once, want 1", n)
	}
	for _, s := range got {
		if s != got[0] {
			t.Fatal("the callers were handed different sessions")
		}
	}

	got[0].dead.Store(true)
	if _, ok := o.Live(); ok {
		t.Fatal("a dead session is not live")
	}
	next, err := o.Current(context.Background(), func(context.Context) (*fakeSession, error) {
		dials.Add(1)
		return &fakeSession{}, nil
	})
	if err != nil || next == got[0] || dials.Load() != 2 {
		t.Fatalf("a dead session is dialed again: err=%v same=%v dials=%d", err, next == got[0], dials.Load())
	}

	o.Forget(got[0])
	if s, ok := o.Live(); !ok || s != next {
		t.Fatal("forgetting a session that is not the one held keeps the held one")
	}
	o.Forget(next)
	if _, ok := o.Live(); ok {
		t.Fatal("the forgotten session is still held")
	}
	if _, ok := o.Take(); ok {
		t.Fatal("Take on an empty holder holds nothing")
	}
}

// A caller whose context ends while another dials leaves at once with its
// context's error; a failed dial is not kept.
func TestOne_AWaitingCallerLeavesOnItsContext(t *testing.T) {
	var o One[*fakeSession]
	hold := make(chan struct{})
	started := make(chan struct{})
	go func() {
		_, _ = o.Current(context.Background(), func(context.Context) (*fakeSession, error) {
			close(started)
			<-hold
			return nil, errors.New("server gone")
		})
	}()
	<-started
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	t0 := time.Now()
	_, err := o.Current(ctx, func(context.Context) (*fakeSession, error) {
		t.Error("a second dial while one is running")
		return nil, nil
	})
	if !errors.Is(err, context.DeadlineExceeded) || time.Since(t0) > time.Second {
		t.Fatalf("the waiting caller should leave on its context: err=%v after %v", err, time.Since(t0))
	}
	close(hold)
	time.Sleep(20 * time.Millisecond)
	if _, ok := o.Live(); ok {
		t.Fatal("a failed dial kept a session")
	}
}
