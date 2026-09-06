// Package antivirus — clamd.go
//
// Talking to a clamd DAEMON over TCP or a unix socket, instead of exec'ing a
// scanner binary.
//
// # Why this exists
//
// In Docker and podman deployments ClamAV is its own container
// (`clamav/clamav`, port 3310) and the natural configuration is
// `clamav:3310`. Before this file the only way to reach ClamAV was an
// executable on filex's own $PATH, which meant either baking a ~1 GB scanner
// and its signature database into the filex image or giving up on scanning.
//
// # Why INSTREAM and not SCAN
//
// clamd's SCAN/CONTSCAN commands take a PATH and the daemon opens it itself,
// so filex and clamd must see the same bytes at the same path. That is exactly
// what a container split does not give you: two images, two filesystems, and a
// temp file in filex's container that clamd cannot open. INSTREAM instead
// pushes the bytes down the same connection the command went out on, so the
// two only need a network route. It is also why the daemon path never spools
// to a temp file the way the exec path has to.
//
// # Why the `z` prefix
//
// clamd accepts three framings: the legacy unprefixed command (deprecated
// upstream), `n<CMD>\n` (newline-terminated) and `z<CMD>\x00`
// (NUL-terminated). With `n`, the REPLY is newline-terminated too — and a
// reply carries a virus signature name or a clamd error string, neither of
// which this code chose. NUL is the one byte that cannot appear inside either,
// so `z` is the only framing where the end of the reply cannot be forged by
// its own payload. filex therefore always sends `z` and always reads to NUL.
package antivirus

import (
	"bufio"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"time"
)

// DefaultClamdPort is clamd's registered TCP port. An address given without
// one ("clamav") is completed with it, because "clamav" and "clamav:3310"
// are the same intent and only one of them is a dialable address.
const DefaultClamdPort = "3310"

// clamdChunkSize is the INSTREAM chunk length. 32 KiB keeps the 4-byte length
// headers to a rounding error on real files while staying far under clamd's
// StreamMaxLength (25 MB by default), which is a per-STREAM ceiling, not a
// per-chunk one.
const clamdChunkSize = 32 << 10

// clamdDialTimeout bounds reaching the daemon, separately from the scan
// timeout that bounds the daemon's work. They are different failures: a
// refused connection is an answer in milliseconds, and waiting a full scan
// timeout for one turns "your address is wrong" into "scanning is slow".
const clamdDialTimeout = 5 * time.Second

// ErrNoDaemonAddress is returned when daemon mode is selected and no address
// has been configured — a state that must read as "unavailable", never as
// "clean".
var ErrNoDaemonAddress = errors.New("antivirus: daemon mode selected but no clamd address is configured")

// ParseAddr turns an operator-typed clamd address into a dial target.
//
// Accepted, with the scheme optional:
//
//	clamav:3310            → tcp,  clamav:3310
//	clamav                 → tcp,  clamav:3310   (DefaultClamdPort)
//	tcp://127.0.0.1:33100  → tcp,  127.0.0.1:33100
//	[::1]:3310             → tcp,  [::1]:3310
//	/var/run/clamav/clamd.ctl        → unix, that path
//	unix:///var/run/clamav/clamd.ctl → unix, that path
//
// ⚠ The unix form is not an afterthought: a single-host compose that shares a
// volume between the two containers talks over the socket, and so does a
// distro package install where clamd listens on /var/run/clamav/clamd.ctl and
// nothing at all listens on TCP.
//
// An empty string is an error rather than a silent "no daemon" — the caller
// that chose daemon mode is the one that has to hear about it.
func ParseAddr(raw string) (network, address string, err error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return "", "", ErrNoDaemonAddress
	}
	if strings.ContainsAny(s, " \t\r\n\x00") {
		return "", "", fmt.Errorf("antivirus: clamd address %q contains whitespace", raw)
	}

	switch {
	case strings.HasPrefix(s, "unix://"):
		p := strings.TrimPrefix(s, "unix://")
		if p == "" {
			return "", "", fmt.Errorf("antivirus: clamd address %q has no socket path", raw)
		}
		return "unix", p, nil
	case strings.HasPrefix(s, "unix:"):
		p := strings.TrimPrefix(s, "unix:")
		if p == "" {
			return "", "", fmt.Errorf("antivirus: clamd address %q has no socket path", raw)
		}
		return "unix", p, nil
	case strings.HasPrefix(s, "tcp://"):
		s = strings.TrimPrefix(s, "tcp://")
	case strings.HasPrefix(s, "/"), strings.HasPrefix(s, "./"), strings.HasPrefix(s, "../"):
		// A bare absolute or relative path is a socket. No host:port can
		// start with a slash, so this is unambiguous.
		return "unix", s, nil
	}

	if s == "" {
		return "", "", fmt.Errorf("antivirus: clamd address %q is empty after its scheme", raw)
	}
	// Bare host with no port: complete it rather than failing, since a port
	// other than 3310 is the unusual case.
	if !strings.Contains(s, ":") {
		s += ":" + DefaultClamdPort
	}
	host, port, splitErr := net.SplitHostPort(s)
	if splitErr != nil {
		return "", "", fmt.Errorf("antivirus: clamd address %q is not host:port: %v", raw, splitErr)
	}
	if host == "" {
		return "", "", fmt.Errorf("antivirus: clamd address %q has no host", raw)
	}
	if port == "" {
		port = DefaultClamdPort
	}
	n, perr := strconv.Atoi(port)
	if perr != nil || n < 1 || n > 65535 {
		return "", "", fmt.Errorf("antivirus: clamd address %q has an invalid port %q", raw, port)
	}
	return "tcp", net.JoinHostPort(host, strconv.Itoa(n)), nil
}

// clamdDial opens the connection, honouring ctx's deadline as well as the
// dial timeout.
//
// ⚠ The caller bounds ctx by the operation's own timeout FIRST (withTimeout)
// so that a host which black-holes packets cannot hold an admin page open for
// the dial timeout on top of the probe timeout. The 5s dialer timeout is only
// the ceiling for a caller that passed none.
func clamdDial(ctx context.Context, network, address string) (net.Conn, error) {
	d := net.Dialer{Timeout: clamdDialTimeout}
	conn, err := d.DialContext(ctx, network, address)
	if err != nil {
		return nil, fmt.Errorf("antivirus: clamd %s://%s unreachable: %w", network, address, err)
	}
	return conn, nil
}

// withTimeout bounds ctx by timeout, so the dial and the reply share one
// budget instead of each getting a fresh one.
func withTimeout(ctx context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	if timeout <= 0 {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, timeout)
}

// clamdPing runs clamd's PING/PONG liveness command. It is the health probe:
// a successful dial only proves something is listening, while PONG proves it
// is clamd and that it is past loading its signature database.
func clamdPing(ctx context.Context, network, address string, timeout time.Duration) error {
	ctx, cancel := withTimeout(ctx, timeout)
	defer cancel()
	conn, err := clamdDial(ctx, network, address)
	if err != nil {
		return err
	}
	defer conn.Close()
	if dl, ok := deadline(ctx, timeout); ok {
		_ = conn.SetDeadline(dl)
	}
	if _, err := conn.Write([]byte("zPING\x00")); err != nil {
		return fmt.Errorf("antivirus: clamd %s://%s ping write: %w", network, address, err)
	}
	reply, err := readZReply(conn)
	if err != nil {
		return fmt.Errorf("antivirus: clamd %s://%s ping read: %w", network, address, err)
	}
	if !strings.EqualFold(reply, "PONG") {
		return fmt.Errorf("antivirus: clamd %s://%s answered %q, not PONG", network, address, reply)
	}
	return nil
}

// clamdVersion asks the daemon what it is, for the admin surface. A failure
// is not fatal anywhere — it is decoration on top of a successful PING.
func clamdVersion(ctx context.Context, network, address string, timeout time.Duration) (string, error) {
	ctx, cancel := withTimeout(ctx, timeout)
	defer cancel()
	conn, err := clamdDial(ctx, network, address)
	if err != nil {
		return "", err
	}
	defer conn.Close()
	if dl, ok := deadline(ctx, timeout); ok {
		_ = conn.SetDeadline(dl)
	}
	if _, err := conn.Write([]byte("zVERSION\x00")); err != nil {
		return "", err
	}
	return readZReply(conn)
}

// clamdScan streams r to the daemon with INSTREAM and returns the same triple
// the exec path does.
//
// The wire sequence is: `zINSTREAM\0`, then repeating {4-byte big-endian
// length, that many bytes}, then a zero length to close the stream, then one
// NUL-terminated reply.
//
// ⚠ The zero-length terminator is not optional and is not the same as closing
// the socket: without it clamd waits for more chunks until its ReadTimeout
// expires and the scan reads as a timeout rather than as a verdict.
func clamdScan(ctx context.Context, network, address string, timeout time.Duration, r io.Reader) (infected bool, signature string, err error) {
	ctx, cancel := withTimeout(ctx, timeout)
	defer cancel()
	conn, err := clamdDial(ctx, network, address)
	if err != nil {
		return false, "", err
	}
	defer conn.Close()
	if dl, ok := deadline(ctx, timeout); ok {
		_ = conn.SetDeadline(dl)
	}

	w := bufio.NewWriter(conn)
	writeErr := streamTo(w, r)
	if writeErr == nil {
		writeErr = w.Flush()
	}

	// ⚠ Read the reply even when the write failed. clamd enforces
	// StreamMaxLength by answering "INSTREAM size limit exceeded. ERROR" and
	// then HANGING UP mid-stream, so on a file over the daemon's limit the
	// first thing this code sees is a broken pipe. Returning that as the
	// error would report a network fault for what is really a configured
	// refusal, and would hide the one message that says how to fix it.
	reply, readErr := readZReply(conn)
	if reply != "" {
		return parseClamdReply(reply)
	}
	if writeErr != nil {
		return false, "", fmt.Errorf("antivirus: clamd %s://%s stream: %w", network, address, writeErr)
	}
	if readErr != nil {
		return false, "", fmt.Errorf("antivirus: clamd %s://%s reply: %w", network, address, readErr)
	}
	return false, "", fmt.Errorf("antivirus: clamd %s://%s closed the connection without a verdict", network, address)
}

// streamTo writes the INSTREAM command and r's bytes as length-prefixed
// chunks, terminated by a zero-length chunk.
func streamTo(w *bufio.Writer, r io.Reader) error {
	if _, err := w.WriteString("zINSTREAM\x00"); err != nil {
		return err
	}
	buf := make([]byte, clamdChunkSize)
	var hdr [4]byte
	for {
		n, rerr := r.Read(buf)
		if n > 0 {
			binary.BigEndian.PutUint32(hdr[:], uint32(n))
			if _, err := w.Write(hdr[:]); err != nil {
				return err
			}
			if _, err := w.Write(buf[:n]); err != nil {
				return err
			}
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			return rerr
		}
	}
	binary.BigEndian.PutUint32(hdr[:], 0)
	if _, err := w.Write(hdr[:]); err != nil {
		return err
	}
	return w.Flush()
}

// readZReply reads one NUL-terminated reply. A reply that ends at EOF without
// its NUL is still returned — clamd hangs up after some errors — so the caller
// can prefer a real message over a transport error.
func readZReply(conn net.Conn) (string, error) {
	var sb strings.Builder
	buf := make([]byte, 1)
	for sb.Len() < 4096 {
		n, err := conn.Read(buf)
		if n > 0 {
			if buf[0] == 0 {
				return strings.TrimSpace(sb.String()), nil
			}
			sb.WriteByte(buf[0])
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				return strings.TrimSpace(sb.String()), nil
			}
			return strings.TrimSpace(sb.String()), err
		}
	}
	return strings.TrimSpace(sb.String()), nil
}

// parseClamdReply turns one INSTREAM reply into the scan triple.
//
// The three shapes clamd produces:
//
//	stream: OK
//	stream: Win.Test.EICAR_HDB-1 FOUND
//	stream: <reason> ERROR          (also: INSTREAM size limit exceeded. ERROR)
//
// ⚠ Anything that is not recognisably one of these is an ERROR, never a pass.
// A scanner that answers "clean" to a reply it did not understand is worse
// than no scanner, because it produces a green tick for an unscanned file.
func parseClamdReply(reply string) (bool, string, error) {
	line := strings.TrimSpace(reply)
	switch {
	case line == "":
		return false, "", errors.New("antivirus: clamd sent an empty reply")
	case strings.HasSuffix(line, " FOUND"):
		return true, parseSignature(line), nil
	case strings.HasSuffix(line, " ERROR"):
		msg := strings.TrimSpace(strings.TrimSuffix(line, " ERROR"))
		if i := strings.Index(msg, "size limit exceeded"); i >= 0 {
			return false, "", fmt.Errorf("antivirus: clamd refused the stream: %s "+
				"(raise StreamMaxLength in clamd.conf, or lower filex's max_scan_mb below it)", msg)
		}
		return false, "", fmt.Errorf("antivirus: clamd: %s", msg)
	case strings.HasSuffix(line, " OK"), line == "OK":
		return false, "", nil
	}
	return false, "", fmt.Errorf("antivirus: clamd sent an unrecognised reply %q", line)
}

// deadline turns a timeout plus ctx's own deadline into the earlier of the
// two, for net.Conn.SetDeadline.
func deadline(ctx context.Context, timeout time.Duration) (time.Time, bool) {
	var dl time.Time
	if timeout > 0 {
		dl = time.Now().Add(timeout)
	}
	if cdl, ok := ctx.Deadline(); ok && (dl.IsZero() || cdl.Before(dl)) {
		dl = cdl
	}
	return dl, !dl.IsZero()
}
