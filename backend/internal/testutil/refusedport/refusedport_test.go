package refusedport

import (
	"net"
	"runtime"
	"strings"
	"testing"
	"time"
)

// The address refuses a connection, and no listener can take its port.
func TestAddr_RefusedAndNotTaken(t *testing.T) {
	addr := Addr(t)
	_, port, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatal(err)
	}
	at := []string{addr}
	if runtime.GOOS == "linux" {
		at = append(at, ":"+port)
	}
	for _, a := range at {
		if l, err := net.Listen("tcp", a); err == nil {
			_ = l.Close()
			t.Fatalf("a listener took %s", a)
		}
	}
	c, err := net.DialTimeout("tcp", addr, 10*time.Second)
	if err == nil {
		_ = c.Close()
		t.Fatal("the connection was accepted")
	}
	if !strings.Contains(err.Error(), "refused") {
		t.Fatalf("not refused: %v", err)
	}
}
