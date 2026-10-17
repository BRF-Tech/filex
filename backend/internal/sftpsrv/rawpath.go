package sftpsrv

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"strings"
)

// A request's path, as the client sent it.
//
// ⚠⚠ pkg/sftp's request server cleans every path before a handler sees it,
// with the HOST's filepath.Clean, and keeps nothing of the original
// (sftp.Request carries only the cleaned path; no server option changes
// that). On a Windows host a backslash is a separator to that Clean: a client
// that uploads `back\slash.txt` got a folder `back` holding `slash.txt`,
// silently made, and could never reach a file whose name holds one. filex's
// rule is that a backslash is a character of a NAME, refused when a name is
// given (internal/leafname) and read like any other in a name that already
// exists - on every host (task #238).
//
// So every path in a request packet is escaped on its way in, before the
// library parses the packet: a backslash becomes the two bytes 0x00 0x01, a
// NUL becomes 0x00 0x00. Neither byte is a separator to any host's Clean, so
// the path reaches the handlers whole, and split (rawPath) undoes the escape
// before filex reads a path. The escape is the same on every host, so the
// suite measures it on Linux too.

// maxPacket is pkg/sftp's own bound on a packet (maxMsgLength). A longer one
// ends the session there too; refusing it here keeps one packet's buffer
// bounded.
const maxPacket = 256 * 1024

// errLongPacket is a length prefix over maxPacket.
var errLongPacket = errors.New("sftp: packet too long")

// The request packets that carry paths, and how many path strings follow the
// request id (SFTP v3, draft-ietf-secsh-filexfer-02).
var pathStrings = map[byte]int{
	3:  1, // OPEN
	7:  1, // LSTAT
	9:  1, // SETSTAT
	11: 1, // OPENDIR
	13: 1, // REMOVE
	14: 1, // MKDIR
	15: 1, // RMDIR
	16: 1, // REALPATH
	17: 1, // STAT
	18: 2, // RENAME
	19: 1, // READLINK
	20: 2, // SYMLINK
}

// extendedPaths is the same for the EXTENDED (200) requests that carry
// paths, after their name.
var extendedPaths = map[string]int{
	"posix-rename@openssh.com": 2,
	"hardlink@openssh.com":     2,
	"statvfs@openssh.com":      1,
}

// escapingConn is the channel the request server reads: whole packets, each
// path in a request escaped (escapePacket); writes and close pass through.
type escapingConn struct {
	io.ReadWriteCloser

	pending []byte // rewritten bytes not handed out yet
	scratch []byte // the packet buffer, reused
	err     error
}

func escapePaths(ch io.ReadWriteCloser) io.ReadWriteCloser {
	return &escapingConn{ReadWriteCloser: ch}
}

func (c *escapingConn) Read(p []byte) (int, error) {
	for len(c.pending) == 0 {
		if c.err != nil {
			return 0, c.err
		}
		c.pending, c.err = c.next()
	}
	n := copy(p, c.pending)
	c.pending = c.pending[n:]
	return n, nil
}

// next reads one packet - its 4-byte length and its body - and returns it,
// rewritten when it is a request with a path.
func (c *escapingConn) next() ([]byte, error) {
	var head [4]byte
	if _, err := io.ReadFull(c.ReadWriteCloser, head[:]); err != nil {
		return nil, err
	}
	length := binary.BigEndian.Uint32(head[:])
	if length > maxPacket {
		return nil, errLongPacket
	}
	if cap(c.scratch) < int(length)+4 {
		c.scratch = make([]byte, int(length)+4)
	}
	pkt := c.scratch[:int(length)+4]
	copy(pkt, head[:])
	if _, err := io.ReadFull(c.ReadWriteCloser, pkt[4:]); err != nil {
		if errors.Is(err, io.EOF) {
			err = io.ErrUnexpectedEOF
		}
		return nil, err
	}
	return escapePacket(pkt), nil
}

// escapePacket returns pkt (length prefix included) with every path string of
// a request escaped; a packet with no path, or with nothing to escape, comes
// back as it is. A packet too short for what its type says is left alone:
// the library refuses it in its own words.
func escapePacket(pkt []byte) []byte {
	if len(pkt) < 9 {
		return pkt
	}
	body := pkt[4:]
	off := 5 // type + request id
	count, ok := pathStrings[body[0]]
	if !ok {
		if body[0] != 200 {
			return pkt
		}
		name, end, ok := sshString(body, off)
		if !ok {
			return pkt
		}
		if count, ok = extendedPaths[string(name)]; !ok {
			return pkt
		}
		off = end
	}
	type span struct{ start, end int }
	spans := make([]span, 0, count)
	needs := false
	for i := 0; i < count; i++ {
		s, end, ok := sshString(body, off)
		if !ok {
			return pkt
		}
		spans = append(spans, span{off, end})
		needs = needs || bytes.ContainsAny(s, "\\\x00")
		off = end
	}
	if !needs {
		return pkt
	}
	out := make([]byte, 4, len(pkt)+16)
	last := 0
	for _, sp := range spans {
		out = append(out, body[last:sp.start]...)
		esc := escapePath(body[sp.start+4 : sp.end])
		out = binary.BigEndian.AppendUint32(out, uint32(len(esc)))
		out = append(out, esc...)
		last = sp.end
	}
	out = append(out, body[last:]...)
	binary.BigEndian.PutUint32(out[:4], uint32(len(out)-4))
	return out
}

// sshString reads the SSH string (uint32 length, bytes) at off: its bytes and
// the offset after it.
func sshString(b []byte, off int) ([]byte, int, bool) {
	if off+4 > len(b) {
		return nil, 0, false
	}
	n := int(binary.BigEndian.Uint32(b[off:]))
	end := off + 4 + n
	if n < 0 || end > len(b) || end < off {
		return nil, 0, false
	}
	return b[off+4 : end], end, true
}

// escapePath is the escape: a backslash is 0x00 0x01, a NUL 0x00 0x00.
func escapePath(p []byte) []byte {
	out := make([]byte, 0, len(p)+8)
	for _, b := range p {
		switch b {
		case '\\':
			out = append(out, 0, 1)
		case 0:
			out = append(out, 0, 0)
		default:
			out = append(out, b)
		}
	}
	return out
}

// rawPath undoes escapePath on a path the library has cleaned: the path as
// the client sent it, backslashes and NULs where they were.
func rawPath(p string) string {
	if strings.IndexByte(p, 0) < 0 {
		return p
	}
	var b strings.Builder
	b.Grow(len(p))
	for i := 0; i < len(p); i++ {
		if p[i] == 0 && i+1 < len(p) {
			switch p[i+1] {
			case 1:
				b.WriteByte('\\')
				i++
				continue
			case 0:
				b.WriteByte(0)
				i++
				continue
			}
		}
		b.WriteByte(p[i])
	}
	return b.String()
}
