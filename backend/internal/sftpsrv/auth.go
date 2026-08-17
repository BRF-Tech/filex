package sftpsrv

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"golang.org/x/crypto/ssh"

	"github.com/brf-tech/filex/backend/internal/protocolauth"
)

// Authentication.
//
// ⚠ Every route ends at internal/protocolauth. This package must not be able
// to construct a caller by itself: identity, the tenant scope, the ACL
// resolver and the confinement arrive together or not at all — the failure
// that rule exists to prevent (a protocol authenticating outside the HTTP
// middleware chain and starting unscoped) already cost /dav a cross-tenant
// listing once.

// session is one authenticated SSH connection.
type session struct {
	principal *protocolauth.Principal
	// login is what the client typed as the username. Kept for the log line;
	// the identity that matters is on the principal.
	login string
	// live is this connection's entry in the revocation registry, set once the
	// connection is accepted. Nil until then, and nil in tests that drive the
	// handlers directly — every use must tolerate that.
	live *protocolauth.LiveSession
}

// permissionsKey is where the session is stashed between the auth callback and
// the connection handler. x/crypto/ssh gives us `Permissions.Extensions`
// (strings only), so the map carries a lookup id and the sessions live here.
const permissionsKey = "filex-session"

// sshConfig builds the server config, with every authentication route wired to
// the shared resolver.
func (s *Server) sshConfig() (*ssh.ServerConfig, error) {
	cfg := &ssh.ServerConfig{
		// ⚠ No anonymous access, ever. NoClientAuth would mean a filesystem
		// reachable by anyone who can open a TCP connection.
		NoClientAuth:  false,
		MaxAuthTries:  6,
		ServerVersion: "SSH-2.0-filex",
		AuthLogCallback: func(conn ssh.ConnMetadata, method string, err error) {
			if err == nil {
				return
			}
			slog.Debug("sftp: auth failed",
				slog.String("user", conn.User()),
				slog.String("method", method),
				slog.String("remote", conn.RemoteAddr().String()))
		},
	}
	if s.cfg.Banner != "" {
		cfg.BannerCallback = func(ssh.ConnMetadata) string { return s.cfg.Banner + "\n" }
	}

	cfg.PasswordCallback = s.passwordCallback
	cfg.PublicKeyCallback = s.publicKeyCallback

	signers, err := s.hostKeys()
	if err != nil {
		return nil, err
	}
	for _, sig := range signers {
		cfg.AddHostKey(sig)
	}
	return cfg, nil
}

// passwordCallback accepts an account password OR an API token, in that order.
//
// The order is protocolauth.Any's, shared with every other password-carrying
// protocol — it is observable (it decides which credential wins if a token ever
// equals a password), so it is decided once rather than per protocol.
//
// ⚠ An account with TOTP enabled is refused by the resolver, on purpose: SSH
// has no second-factor channel here, so accepting the password would make this
// endpoint a documented 2FA bypass. Such an account connects with an API token
// or a registered public key, both individually revocable.
func (s *Server) passwordCallback(conn ssh.ConnMetadata, password []byte) (*ssh.Permissions, error) {
	login := conn.User()
	p, err := s.cfg.Auth.Any(context.Background(), login, string(password))
	if err != nil {
		return nil, errAuth
	}
	return s.grant(conn, p, login)
}

// publicKeyCallback authenticates against the keys the account registered.
//
// ⚠ The library has ALREADY verified the signature by the time this runs — it
// is asking whether this key is allowed, not whether the client holds it. What
// this must get right is the lookup: a fingerprint is matched against the
// account the client named, so a key registered by one user cannot log in as
// another.
func (s *Server) publicKeyCallback(conn ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
	login := conn.User()
	if s.cfg.Store == nil {
		return nil, errAuth
	}
	fp := Fingerprint(key)
	p, err := s.cfg.Auth.PublicKey(context.Background(), login, fp)
	if err != nil {
		return nil, errAuth
	}
	return s.grant(conn, p, login)
}

// errAuth is what every failure returns.
//
// One error for all of them: "no such account", "wrong password", "disabled
// account" and "that key is not registered" must be indistinguishable, or the
// endpoint becomes an account-enumeration oracle for anyone who can connect.
var errAuth = errors.New("permission denied")

// grant records the resolved caller for the connection handler.
func (s *Server) grant(conn ssh.ConnMetadata, p *protocolauth.Principal, login string) (*ssh.Permissions, error) {
	if p == nil || p.User == nil {
		return nil, errAuth
	}
	id := s.sessions.put(&session{principal: p, login: login})
	return &ssh.Permissions{
		Extensions: map[string]string{permissionsKey: id},
	}, nil
}

// Fingerprint is the SHA256 fingerprint OpenSSH prints, without the
// `SHA256:` prefix — the form stored in the ssh_keys table.
func Fingerprint(key ssh.PublicKey) string {
	return strings.TrimPrefix(ssh.FingerprintSHA256(key), "SHA256:")
}

// ParseAuthorizedKey accepts one line in authorized_keys form and returns its
// fingerprint and normalised wire form.
//
// ⚠ It refuses a line with anything before the key type (an authorized_keys
// OPTIONS field: `command=`, `permitopen=`, `no-pty`). Those options mean
// something to OpenSSH and nothing here; storing a key with options filex
// silently ignores would hand somebody a restriction they believe is enforced.
func ParseAuthorizedKey(line string) (fingerprint, normalized, comment string, err error) {
	line = strings.TrimSpace(line)
	if line == "" {
		return "", "", "", errors.New("empty key")
	}
	fields := strings.Fields(line)
	if len(fields) < 2 || !strings.HasPrefix(fields[0], "ssh-") && !strings.HasPrefix(fields[0], "ecdsa-") && !strings.HasPrefix(fields[0], "sk-") {
		return "", "", "", errors.New("the line must start with the key type, with no authorized_keys options in front of it")
	}
	key, cmt, _, _, perr := ssh.ParseAuthorizedKey([]byte(line))
	if perr != nil {
		return "", "", "", fmt.Errorf("not a valid public key: %w", perr)
	}
	wire := key.Type() + " " + base64.StdEncoding.EncodeToString(key.Marshal())
	return Fingerprint(key), wire, cmt, nil
}
