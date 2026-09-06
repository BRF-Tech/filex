package antivirus_test

// clamd protocol tests against a MOCK daemon.
//
// ⚠ These are the edge cases, not the evidence that daemon mode works: a mock
// clamd agrees with whatever this code believes the protocol to be, which is
// exactly the belief under test. The proof that filex talks to the real thing
// is clamd_live_test.go, which runs against a `clamav/clamav` container and a
// real EICAR file. This file covers what a container cannot be made to do on
// demand — a refused connection, a truncated reply, a size-limit refusal
// mid-stream, a daemon that answers something nobody expected.

import (
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/antivirus"
)

// mockClamd answers one command per connection with a scripted reply.
type mockClamd struct {
	ln net.Listener
	// reply is written (NUL-terminated) after the stream terminator.
	reply string
	// hangUpAfter, when > 0, closes the connection after that many stream
	// bytes have been read — how clamd enforces StreamMaxLength.
	hangUpAfter int
	// hangUpReply is sent just before hanging up, when set.
	hangUpReply string
	// noReply closes without saying anything.
	noReply bool
	// gotBytes accumulates the payload the client streamed.
	gotBytes chan []byte
	// gotCmd records the command line as received, prefix included.
	gotCmd chan string
}

func startMockClamd(t *testing.T, m *mockClamd) *mockClamd {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	m.ln = ln
	m.gotBytes = make(chan []byte, 4)
	m.gotCmd = make(chan string, 4)
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			conn, aerr := ln.Accept()
			if aerr != nil {
				return
			}
			go m.serve(conn)
		}
	}()
	return m
}

func (m *mockClamd) addr() string { return m.ln.Addr().String() }

func (m *mockClamd) serve(conn net.Conn) {
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))

	// Read the NUL-terminated command.
	var cmd strings.Builder
	one := make([]byte, 1)
	for {
		n, err := conn.Read(one)
		if n > 0 {
			if one[0] == 0 {
				break
			}
			cmd.WriteByte(one[0])
		}
		if err != nil {
			return
		}
	}
	m.gotCmd <- cmd.String()

	if strings.EqualFold(cmd.String(), "zPING") || strings.EqualFold(cmd.String(), "PING") {
		if m.noReply {
			return
		}
		_, _ = conn.Write([]byte(m.reply + "\x00"))
		return
	}

	// INSTREAM: length-prefixed chunks until a zero length.
	var payload []byte
	var hdr [4]byte
	for {
		if _, err := io.ReadFull(conn, hdr[:]); err != nil {
			break
		}
		n := binary.BigEndian.Uint32(hdr[:])
		if n == 0 {
			break
		}
		buf := make([]byte, n)
		if _, err := io.ReadFull(conn, buf); err != nil {
			break
		}
		payload = append(payload, buf...)
		if m.hangUpAfter > 0 && len(payload) >= m.hangUpAfter {
			if m.hangUpReply != "" {
				_, _ = conn.Write([]byte(m.hangUpReply + "\x00"))
			}
			m.gotBytes <- payload
			return
		}
	}
	m.gotBytes <- payload
	if m.noReply {
		return
	}
	_, _ = conn.Write([]byte(m.reply + "\x00"))
}

func TestParseAddr(t *testing.T) {
	cases := []struct {
		in, net, addr string
	}{
		{"clamav:3310", "tcp", "clamav:3310"},
		{"clamav", "tcp", "clamav:3310"},
		{" clamav:3310 ", "tcp", "clamav:3310"},
		{"tcp://127.0.0.1:33100", "tcp", "127.0.0.1:33100"},
		{"[::1]:3310", "tcp", "[::1]:3310"},
		{"/var/run/clamav/clamd.ctl", "unix", "/var/run/clamav/clamd.ctl"},
		{"unix:///var/run/clamav/clamd.ctl", "unix", "/var/run/clamav/clamd.ctl"},
		{"unix:/tmp/clamd.sock", "unix", "/tmp/clamd.sock"},
	}
	for _, c := range cases {
		n, a, err := antivirus.ParseAddr(c.in)
		require.NoError(t, err, c.in)
		assert.Equal(t, c.net, n, c.in)
		assert.Equal(t, c.addr, a, c.in)
	}

	// ⚠ The refusals matter more than the acceptances: each of these is a
	// value an operator can type into the admin form, and every one of them
	// must be refused AT SAVE TIME rather than discovered by the first file
	// that failed to get scanned.
	for _, bad := range []string{
		"",                 // nothing configured
		"clamav 3310",      // space instead of a colon
		"clamav:0",         // port 0
		"clamav:70000",     // port out of range
		"clamav:http",      // non-numeric port
		":3310",            // no host
		"unix://",          // no socket path
		"tcp://",           // no host after the scheme
		"clamav:3310\nfoo", // embedded newline
	} {
		_, _, err := antivirus.ParseAddr(bad)
		assert.Error(t, err, "should refuse %q", bad)
	}

	// The AddrSetting's Check is the same function, so the admin API refuses
	// exactly what ParseAddr refuses — and accepts empty, which means
	// "not configured yet".
	assert.NoError(t, antivirus.AddrSetting.Validate(""))
	assert.NoError(t, antivirus.AddrSetting.Validate("clamav:3310"))
	assert.Error(t, antivirus.AddrSetting.Validate("clamav 3310"))
}

func TestClamdScan_Clean(t *testing.T) {
	m := startMockClamd(t, &mockClamd{reply: "stream: OK"})
	sc, err := antivirus.NewWithDaemon(m.addr())
	require.NoError(t, err)
	require.True(t, sc.Supports())
	assert.Equal(t, "daemon", sc.Mode())
	assert.Equal(t, "clamd", sc.BinName())

	infected, sig, err := sc.Scan(context.Background(), strings.NewReader("harmless bytes"))
	require.NoError(t, err)
	assert.False(t, infected)
	assert.Equal(t, "", sig)

	assert.Equal(t, "zINSTREAM", <-m.gotCmd, "the z prefix is what frames the reply")
	assert.Equal(t, "harmless bytes", string(<-m.gotBytes))
}

func TestClamdScan_Infected(t *testing.T) {
	m := startMockClamd(t, &mockClamd{reply: "stream: Win.Test.EICAR_HDB-1 FOUND"})
	sc, err := antivirus.NewWithDaemon(m.addr())
	require.NoError(t, err)

	infected, sig, err := sc.Scan(context.Background(), strings.NewReader("X5O!P%@AP"))
	require.NoError(t, err)
	assert.True(t, infected)
	assert.Equal(t, "Win.Test.EICAR_HDB-1", sig,
		"the daemon reply and the CLI output share one signature parser")
}

// ⚠⚠ The case this whole feature has to get right. A scanner that cannot
// reach clamd must be an ERROR, never (clean, no signature, no error): the
// queue turns an error into a failed op that retries and is visible, and turns
// "clean" into a file nobody will ever look at again.
func TestClamdScan_UnreachableIsAnErrorNotClean(t *testing.T) {
	// A port nothing is listening on: bind one, learn its number, release it.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	dead := ln.Addr().String()
	require.NoError(t, ln.Close())

	sc, err := antivirus.NewWithDaemon(dead)
	require.NoError(t, err)
	assert.True(t, sc.Supports(), "configured; reachability is a separate question")

	infected, sig, err := sc.Scan(context.Background(), strings.NewReader("anything"))
	require.Error(t, err, "an unreachable daemon must not report a clean file")
	assert.False(t, infected)
	assert.Equal(t, "", sig)
	assert.Contains(t, err.Error(), "unreachable")

	// And the admin surface says so rather than showing a green light.
	herr := sc.Health(context.Background())
	require.Error(t, herr)
	assert.Contains(t, herr.Error(), "unreachable")
}

func TestClamdHealth_PingPong(t *testing.T) {
	m := startMockClamd(t, &mockClamd{reply: "PONG"})
	sc, err := antivirus.NewWithDaemon(m.addr())
	require.NoError(t, err)
	require.NoError(t, sc.Health(context.Background()))
	assert.Equal(t, "zPING", <-m.gotCmd)
}

// A daemon that accepts the connection but is still loading its signature
// database answers something other than PONG. "Something is listening" is not
// "scanning works", so Health must refuse it.
func TestClamdHealth_WrongAnswerIsUnhealthy(t *testing.T) {
	m := startMockClamd(t, &mockClamd{reply: "UNKNOWN COMMAND"})
	sc, err := antivirus.NewWithDaemon(m.addr())
	require.NoError(t, err)
	err = sc.Health(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not PONG")
}

// clamd enforces StreamMaxLength by replying and then HANGING UP mid-stream,
// so the client's first symptom is a broken pipe. Reporting that as a network
// fault would hide the one message that says how to fix it.
func TestClamdScan_SizeLimitRefusalIsReported(t *testing.T) {
	m := startMockClamd(t, &mockClamd{
		hangUpAfter: 32 << 10,
		hangUpReply: "INSTREAM size limit exceeded. ERROR",
	})
	sc, err := antivirus.NewWithDaemon(m.addr())
	require.NoError(t, err)

	big := strings.NewReader(strings.Repeat("A", 512<<10))
	infected, _, err := sc.Scan(context.Background(), big)
	require.Error(t, err)
	assert.False(t, infected)
	assert.Contains(t, err.Error(), "size limit exceeded")
	assert.Contains(t, err.Error(), "StreamMaxLength",
		"the error has to name the knob that fixes it")
}

func TestClamdScan_DaemonErrorReply(t *testing.T) {
	m := startMockClamd(t, &mockClamd{reply: "stream: Can't allocate memory ERROR"})
	sc, err := antivirus.NewWithDaemon(m.addr())
	require.NoError(t, err)

	infected, _, err := sc.Scan(context.Background(), strings.NewReader("data"))
	require.Error(t, err)
	assert.False(t, infected)
	assert.Contains(t, err.Error(), "Can't allocate memory")
}

// ⚠ An unrecognised reply is an error, never a pass. Answering "clean" to a
// sentence this code did not understand produces the same green tick as a real
// scan.
func TestClamdScan_UnknownReplyIsAnError(t *testing.T) {
	m := startMockClamd(t, &mockClamd{reply: "something entirely else"})
	sc, err := antivirus.NewWithDaemon(m.addr())
	require.NoError(t, err)

	infected, _, err := sc.Scan(context.Background(), strings.NewReader("data"))
	require.Error(t, err)
	assert.False(t, infected)
	assert.Contains(t, err.Error(), "unrecognised reply")
}

func TestClamdScan_SilentHangUpIsAnError(t *testing.T) {
	m := startMockClamd(t, &mockClamd{noReply: true})
	sc, err := antivirus.NewWithDaemon(m.addr())
	require.NoError(t, err)

	infected, _, err := sc.Scan(context.Background(), strings.NewReader("data"))
	require.Error(t, err)
	assert.False(t, infected)
	assert.Contains(t, err.Error(), "without a verdict")
}

func TestClamdScan_Timeout(t *testing.T) {
	// A listener that accepts and then says nothing at all.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			conn, aerr := ln.Accept()
			if aerr != nil {
				return
			}
			// Hold it open without answering.
			go func() { time.Sleep(10 * time.Second); _ = conn.Close() }()
		}
	}()

	sc, err := antivirus.NewWithDaemon(ln.Addr().String())
	require.NoError(t, err)
	sc.SetTimeout(300 * time.Millisecond)

	start := time.Now()
	infected, _, err := sc.Scan(context.Background(), strings.NewReader("data"))
	require.Error(t, err)
	assert.False(t, infected)
	assert.Less(t, time.Since(start), 5*time.Second, "the scan timeout has to bound the wait")
}

// A unix socket is the transport a single-host compose with a shared volume
// uses, and a distro clamd package's only transport. It has to work, not just
// parse.
func TestClamdScan_UnixSocket(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix sockets: the suite runs under WSL/Linux")
	}
	sock := t.TempDir() + "/clamd.sock"
	ln, err := net.Listen("unix", sock)
	require.NoError(t, err)
	t.Cleanup(func() { _ = ln.Close() })
	m := &mockClamd{reply: "stream: Unix.Test FOUND", gotBytes: make(chan []byte, 4), gotCmd: make(chan string, 4)}
	go func() {
		for {
			conn, aerr := ln.Accept()
			if aerr != nil {
				return
			}
			go m.serve(conn)
		}
	}()

	sc, err := antivirus.NewWithDaemon(sock)
	require.NoError(t, err)
	assert.Equal(t, sock, sc.Address())

	infected, sig, err := sc.Scan(context.Background(), strings.NewReader("payload"))
	require.NoError(t, err)
	assert.True(t, infected)
	assert.Equal(t, "Unix.Test", sig)
	assert.Equal(t, "payload", string(<-m.gotBytes))
}

// Daemon mode with no address configured is unavailable and says why. It must
// never be mistaken for "scanning is off" (a choice) or for "clean".
func TestDaemonModeWithoutAddressIsUnavailable(t *testing.T) {
	res := antivirus.Resolve(context.Background(), settingsMap{
		antivirus.EnabledSetting.Key: "true",
		antivirus.ModeSetting.Key:    antivirus.ModeDaemon,
	})
	assert.True(t, res.Enabled)
	assert.False(t, res.Available())
	assert.ErrorIs(t, res.Err, antivirus.ErrNoDaemonAddress)

	sc := antivirus.NewWithResolution(res)
	assert.False(t, sc.Supports())
	_, _, err := sc.Scan(context.Background(), strings.NewReader("data"))
	assert.ErrorIs(t, err, antivirus.ErrNoDaemonAddress)
	assert.True(t, errors.Is(sc.Health(context.Background()), antivirus.ErrNoDaemonAddress))
}
