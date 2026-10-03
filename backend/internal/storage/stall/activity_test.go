package stall

import (
	"io"
	"net"
	"testing"
	"time"
)

// Issue #75: the silence limit for a connection a library reads from in the
// background. It must cut a call the peer stops answering, renew on every
// byte, count an upload's progress only for a call that sends, and never cut
// a connection nobody is calling on.

// pair is a loopback connection: the client end tracked, the server end raw.
func pair(t *testing.T) (*Activity, net.Conn, net.Conn) {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	accepted := make(chan net.Conn, 1)
	go func() {
		c, err := l.Accept()
		if err == nil {
			accepted <- c
		}
	}()
	raw, err := net.Dial("tcp", l.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	server := <-accepted
	a, client := Track(raw)
	t.Cleanup(func() { _ = client.Close(); _ = server.Close() })
	return a, client, server
}

func TestActivity_ACallThePeerDoesNotAnswerIsCut(t *testing.T) {
	a, client, _ := pair(t)
	w := a.Watch(300*time.Millisecond, false)
	start := time.Now()
	_, err := client.Read(make([]byte, 1))
	took := time.Since(start)
	if !w.Stop() || !a.WasCut() {
		t.Fatal("the watch did not cut a silent peer")
	}
	if err == nil {
		t.Fatal("the read waiting on the cut connection did not fail")
	}
	if took < 250*time.Millisecond || took > 2*time.Second {
		t.Errorf("cut after %s, want about 300ms", took)
	}
}

func TestActivity_EveryByteRenewsTheLimit(t *testing.T) {
	a, client, server := pair(t)
	go func() {
		for i := 0; i < 8; i++ {
			time.Sleep(150 * time.Millisecond)
			if _, err := server.Write([]byte("x")); err != nil {
				return
			}
		}
		_ = server.Close()
	}()
	w := a.Watch(300*time.Millisecond, false)
	n, _ := io.Copy(io.Discard, client) // 1.2 s of a peer that keeps talking
	if w.Stop() {
		t.Fatalf("a peer that sent a byte every 150ms was cut by a 300ms limit (got %d bytes)", n)
	}
	if n != 8 {
		t.Errorf("got %d bytes, want 8", n)
	}
}

// A connection nobody calls on is never cut: the clock runs only while a
// watch is armed.
func TestActivity_AnIdleConnectionIsNotCut(t *testing.T) {
	a, client, server := pair(t)
	w := a.Watch(200*time.Millisecond, false)
	if w.Stop() {
		t.Fatal("a stopped watch cut")
	}
	time.Sleep(600 * time.Millisecond)
	if a.WasCut() {
		t.Fatal("an idle connection was cut")
	}
	if _, err := server.Write([]byte("y")); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Read(make([]byte, 1)); err != nil {
		t.Fatalf("the connection does not work after an idle pause: %v", err)
	}
}

// An upload's progress (the peer taking bytes) counts only for a call that
// sends: a peer that reads but never answers is silent to any other call.
func TestActivity_SendsCountOnlyWhenTheCallSends(t *testing.T) {
	a, client, server := pair(t)
	go func() { _, _ = io.Copy(io.Discard, server) }()
	stop := make(chan struct{})
	go func() {
		for {
			select {
			case <-stop:
				return
			default:
			}
			if _, err := client.Write(make([]byte, 1024)); err != nil {
				return
			}
			time.Sleep(50 * time.Millisecond)
		}
	}()
	defer close(stop)

	sending := a.Watch(300*time.Millisecond, true)
	time.Sleep(700 * time.Millisecond)
	if sending.Stop() {
		t.Fatal("an upload the peer keeps taking was cut")
	}
	answering := a.Watch(300*time.Millisecond, false)
	time.Sleep(700 * time.Millisecond)
	if !answering.Stop() {
		t.Fatal("a call waiting for an answer that never comes was not cut, because bytes were leaving")
	}
}
