package sftpsrv

import (
	"bytes"
	"encoding/binary"
	"io"
	"path"
	"strings"
	"testing"
)

// The path escape (rawpath.go), measured on any host: what pkg/sftp's
// host-dependent filepath.Clean sees of a request path holds no backslash, so
// a Windows host's Clean - which splits at one - cannot split a name.

// windowsClean is what pkg/sftp's cleanPath does to a path on a Windows host:
// filepath.Clean, which takes `\` for a separator, then ToSlash.
func windowsClean(p string) string {
	return path.Clean(strings.ReplaceAll(p, `\`, "/"))
}

// sftpPacket builds one request packet: length, type, request id, then each
// field - a string field as an SSH string, raw bytes as they are.
func sftpPacket(typ byte, fields ...any) []byte {
	body := []byte{typ, 0, 0, 0, 7}
	for _, f := range fields {
		switch v := f.(type) {
		case string:
			body = binary.BigEndian.AppendUint32(body, uint32(len(v)))
			body = append(body, v...)
		case []byte:
			body = append(body, v...)
		}
	}
	return append(binary.BigEndian.AppendUint32(nil, uint32(len(body))), body...)
}

// pathFields reads n SSH strings starting at off of a packet's body.
func pathFields(t *testing.T, pkt []byte, off, n int) []string {
	t.Helper()
	if got := int(binary.BigEndian.Uint32(pkt)); got != len(pkt)-4 {
		t.Fatalf("length prefix %d, packet body %d bytes", got, len(pkt)-4)
	}
	body := pkt[4:]
	var out []string
	for i := 0; i < n; i++ {
		s, end, ok := sshString(body, off)
		if !ok {
			t.Fatalf("field %d does not parse", i)
		}
		out = append(out, string(s))
		off = end
	}
	return out
}

const backslashPath = `/main/back\slash.txt`

func TestEscapedPathSurvivesAWindowsClean(t *testing.T) {
	// The hazard itself: Windows splits the name.
	if got := windowsClean(backslashPath); got != "/main/back/slash.txt" {
		t.Fatalf("the simulation is off: %q", got)
	}
	esc := string(escapePath([]byte(backslashPath)))
	if strings.Contains(esc, `\`) {
		t.Fatalf("the escaped path still holds a backslash: %q", esc)
	}
	if got := rawPath(windowsClean(esc)); got != backslashPath {
		t.Fatalf("after a Windows Clean the path is %q, want %q", got, backslashPath)
	}
	// A NUL travels too, and the escape is undone exactly.
	withNUL := "/main/a" + string(rune(0)) + `\b` + string(rune(0)) + string(rune(1))
	if got := rawPath(string(escapePath([]byte(withNUL)))); got != withNUL {
		t.Fatalf("round trip %q, want %q", got, withNUL)
	}
	if rawPath("/main/plain.txt") != "/main/plain.txt" {
		t.Fatal("a path with nothing to undo changed")
	}
}

func TestEscapePacket_EveryPathOfARequest(t *testing.T) {
	attrs := []byte{0, 0, 0, 0}
	cases := []struct {
		name   string
		pkt    []byte
		off, n int
		rest   []byte
	}{
		{"open", sftpPacket(3, backslashPath, []byte{0, 0, 0, 0x1a}, attrs), 5, 1, append([]byte{0, 0, 0, 0x1a}, attrs...)},
		{"mkdir", sftpPacket(14, backslashPath, attrs), 5, 1, attrs},
		{"stat", sftpPacket(17, backslashPath), 5, 1, nil},
		{"rename", sftpPacket(18, `/main/a\b`, backslashPath), 5, 2, nil},
		{"posix-rename", sftpPacket(200, "posix-rename@openssh.com", `/main/a\b`, backslashPath), 5 + 4 + len("posix-rename@openssh.com"), 2, nil},
		{"statvfs", sftpPacket(200, "statvfs@openssh.com", backslashPath), 5 + 4 + len("statvfs@openssh.com"), 1, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			in := append([]byte(nil), c.pkt...)
			out := escapePacket(in)
			for i, f := range pathFields(t, out, c.off, c.n) {
				if strings.Contains(f, `\`) {
					t.Errorf("path %d still holds a backslash: %q", i, f)
				}
				if raw := rawPath(f); !strings.Contains(raw, `\`) {
					t.Errorf("path %d does not undo to the client's: %q", i, raw)
				}
			}
			if c.rest != nil && !bytes.HasSuffix(out, c.rest) {
				t.Errorf("what follows the path changed: % x", out)
			}
			if out[4] != c.pkt[4] || !bytes.Equal(out[5:9], c.pkt[5:9]) {
				t.Error("the type or the request id changed")
			}
		})
	}
}

func TestEscapePacket_LeavesEverythingElseAlone(t *testing.T) {
	for name, pkt := range map[string][]byte{
		"write data":       sftpPacket(6, "handle", []byte{0, 0, 0, 0, 0, 0, 0, 0}, `bytes \ with \ backslashes`),
		"plain path":       sftpPacket(17, "/main/plain.txt"),
		"unknown extended": sftpPacket(200, "fsync@openssh.com", `handle\`),
		"init":             {0, 0, 0, 5, 1, 0, 0, 0, 3},
		"too short":        sftpPacket(17)[:7],
	} {
		if got := escapePacket(append([]byte(nil), pkt...)); !bytes.Equal(got, pkt) {
			t.Errorf("%s changed: % x", name, got)
		}
	}
}

// readWriteCloser is a channel that answers reads from a buffer.
type readWriteCloser struct {
	io.Reader
	io.Writer
}

func (readWriteCloser) Close() error { return nil }

// The conn hands the library whole rewritten packets, one after the other,
// however small the library's reads.
func TestEscapingConn_PacketByPacket(t *testing.T) {
	first := sftpPacket(17, backslashPath)
	second := sftpPacket(6, "h", []byte{0, 0, 0, 0, 0, 0, 0, 0}, `data\`)
	conn := escapePaths(readWriteCloser{Reader: bytes.NewReader(append(append([]byte(nil), first...), second...)), Writer: io.Discard})
	var got []byte
	buf := make([]byte, 3)
	for {
		n, err := conn.Read(buf)
		got = append(got, buf[:n]...)
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("read: %v", err)
		}
	}
	want := append(escapePacket(append([]byte(nil), first...)), second...)
	if !bytes.Equal(got, want) {
		t.Fatalf("read % x\nwant % x", got, want)
	}

	long := binary.BigEndian.AppendUint32(nil, maxPacket+1)
	conn = escapePaths(readWriteCloser{Reader: bytes.NewReader(long), Writer: io.Discard})
	if _, err := conn.Read(buf); err != errLongPacket {
		t.Fatalf("a packet over the bound: %v, want errLongPacket", err)
	}
}
