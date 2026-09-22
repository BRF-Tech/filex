package httpx

import (
	"context"
	"sync"
	"testing"
)

// Every caller writes into the holder without checking whether there is one
// (a background job, a test, an SFTP session has none), so nil must be a
// silent no-op on every method.
func TestRequestLog_NilIsANoOp(t *testing.T) {
	if l := RequestLogFrom(context.Background()); l != nil {
		t.Fatalf("a context without a holder returned %v", l)
	}
	var l *RequestLog
	l.NoteUser(7)
	l.NoteToken(42)
	l.NoteTenant("acme")
	if l.UserID() != 0 || l.TokenID() != 0 || l.Tenant() != "" {
		t.Fatal("a nil holder reported a caller")
	}
}

// Zero is not an account or a token (a synthesized principal carries it), and
// an empty slug is not a tenant: none of them may overwrite a real value.
func TestRequestLog_ZeroAndEmptyAreIgnored(t *testing.T) {
	ctx, l := WithRequestLog(context.Background())
	if RequestLogFrom(ctx) != l {
		t.Fatal("RequestLogFrom does not return the holder WithRequestLog put there")
	}
	l.NoteUser(7)
	l.NoteToken(42)
	l.NoteTenant("acme")
	l.NoteUser(0)
	l.NoteToken(0)
	l.NoteTenant("")
	if l.UserID() != 7 || l.TokenID() != 42 || l.Tenant() != "acme" {
		t.Fatalf("got user=%d token=%d tenant=%q", l.UserID(), l.TokenID(), l.Tenant())
	}
}

// A WebSocket handler keeps the request context alive in goroutines that can
// still be writing while the logger reads. Run with -race.
func TestRequestLog_ConcurrentUse(t *testing.T) {
	_, l := WithRequestLog(context.Background())
	var wg sync.WaitGroup
	for i := 1; i <= 8; i++ {
		wg.Add(1)
		go func(id int64) {
			defer wg.Done()
			l.NoteUser(id)
			l.NoteToken(id)
			l.NoteTenant("t")
			_ = l.UserID() + l.TokenID()
			_ = l.Tenant()
		}(int64(i))
	}
	wg.Wait()
	if l.UserID() == 0 || l.TokenID() == 0 || l.Tenant() != "t" {
		t.Fatal("concurrent notes were lost")
	}
}
