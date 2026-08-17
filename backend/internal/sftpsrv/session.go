package sftpsrv

import (
	"crypto/rand"
	"encoding/hex"
	"io"
	"log/slog"
	"sync"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
)

// The bridge between an authentication callback and the connection that
// follows it.
//
// x/crypto/ssh hands the callback's result to the connection as
// `Permissions.Extensions`, which is a map of STRINGS — it cannot carry a
// resolved principal. So the callback parks the session here under a random id
// and the handler collects it. The alternative, re-authenticating in the
// handler, would mean the identity that authorised the connection and the
// identity that serves it are resolved twice, and could differ.

// sessionStore holds sessions between the auth callback and the handler.
type sessionStore struct {
	mu sync.Mutex
	m  map[string]*session
}

func newSessionStore() *sessionStore { return &sessionStore{m: map[string]*session{}} }

func (s *sessionStore) put(sess *session) string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		// Unreachable in practice; a predictable id would let one connection
		// claim another's session, so refusing to issue one is the safe answer.
		return ""
	}
	id := hex.EncodeToString(b[:])
	s.mu.Lock()
	defer s.mu.Unlock()
	s.m[id] = sess
	return id
}

// take removes and returns a session. Removal is the point: an id is good for
// exactly one connection, so a leaked one cannot be replayed.
func (s *sessionStore) take(id string) (*session, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, ok := s.m[id]
	delete(s.m, id)
	return sess, ok
}

// drop discards a parked session whose connection never arrived.
func (s *sessionStore) drop(id string) {
	if id == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.m, id)
}

// sessionFrom recovers the session an authentication callback parked.
func (s *Server) sessionFrom(conn *ssh.ServerConn) (*session, bool) {
	if conn == nil || conn.Permissions == nil {
		return nil, false
	}
	id := conn.Permissions.Extensions[permissionsKey]
	if id == "" {
		return nil, false
	}
	return s.sessions.take(id)
}

// serveChannel runs the SFTP subsystem on one channel.
//
// ⚠ Only the `sftp` subsystem is served. `exec` and `shell` are refused: filex
// has no shell, and answering an exec request with anything other than a
// refusal is how a file server grows a command execution surface.
func (s *Server) serveChannel(sess *session, ch ssh.Channel, reqs <-chan *ssh.Request) {
	defer ch.Close()

	ok := false
	for req := range reqs {
		switch req.Type {
		case "subsystem":
			// The payload is a length-prefixed string; anything shorter is
			// malformed and gets the same refusal as an unknown subsystem.
			if len(req.Payload) > 4 && string(req.Payload[4:]) == "sftp" {
				ok = true
			}
		default:
			// exec, shell, pty-req, env, …
		}
		if req.WantReply {
			_ = req.Reply(ok, nil)
		}
		if ok {
			break
		}
	}
	if !ok {
		return
	}
	// Requests keep arriving on this channel (window changes, signals); nothing
	// here answers them, but they must be drained or the client blocks.
	go func() {
		for req := range reqs {
			if req.WantReply {
				_ = req.Reply(false, nil)
			}
		}
	}()

	handlers := s.handlers(sess)
	srv := sftp.NewRequestServer(ch, handlers, sftp.WithRSAllocator())
	defer srv.Close()

	status := uint32(0)
	if err := srv.Serve(); err != nil && err != io.EOF {
		slog.Debug("sftp: session ended",
			slog.String("user", sess.login), slog.Any("err", err))
		status = 1
	}

	// ⚠⚠ The subsystem's exit status. A real sshd sends one when sftp-server
	// exits, and OpenSSH's `scp` — which since 9.0 speaks SFTP rather than the
	// old protocol — takes the channel's exit status as ITS exit status. Without
	// this every scp against filex ended in a silent `exit 1` with the bytes
	// already transferred and no error printed anywhere: the copy worked and
	// every script around it decided it had failed. (Measured 2026-08-16 with
	// OpenSSH 9.6; the file was on the server and scp still said no.)
	_, _ = ch.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{Status: status}))
}
