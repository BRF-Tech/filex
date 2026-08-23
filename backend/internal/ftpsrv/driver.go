package ftpsrv

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"strings"
	"sync"
	"time"

	ftpserver "github.com/fclairamb/ftpserverlib"

	"github.com/brf-tech/filex/backend/internal/protocolauth"
)

// The library's MainDriver: greeting, authentication, settings, TLS.

type driver struct {
	srv *Server

	// live maps a client id to its entry in the revocation registry. The
	// library hands ClientDisconnected a ClientContext and nothing else, so
	// there has to be somewhere to look the session up on the way out.
	liveMu sync.Mutex
	live   map[uint32]*protocolauth.LiveSession

	// host is the resolved PASV address, kept once resolution succeeded.
	hostMu       sync.Mutex
	host         string
	hostResolved bool
}

// GetSettings describes the endpoint to the library.
func (d *driver) GetSettings() (*ftpserver.Settings, error) {
	c := d.srv.cfg
	host, err := d.passiveHostWithRetry()
	if err != nil {
		return nil, err
	}
	return &ftpserver.Settings{
		ListenAddr: c.Addr,
		PublicHost: host,
		Banner:     bannerOr(c.Banner),
		PassiveTransferPortRange: &ftpserver.PortRange{
			Start: c.PassivePortMin,
			End:   c.PassivePortMax,
		},
		IdleTimeout: c.IdleTimeout,
		// ⚠⚠ Mandatory, not preferred. Plain FTP sends the password in the
		// clear and the file after it; a flag that let an operator turn this
		// off would be a flag that publishes their users' credentials.
		TLSRequired: ftpserver.MandatoryEncryption,
		// ⚠ Active mode has the SERVER dial the client, which does not survive
		// NAT, is blocked by every sane firewall, and turns the endpoint into
		// something that makes outbound connections to an address the client
		// chose. Passive only.
		DisableActiveMode: true,
		// filex has no shell and no site-specific commands to expose.
		DisableSite: true,
		// ⚠⚠ ASCII mode REWRITES line endings. On a text file transferred by a
		// client that guessed wrong that is silent corruption of somebody's
		// data, and FTP's ASCII default is a 1985 decision about teletypes.
		// Bytes are bytes here.
		DisableASCIIConversion: true,
		DefaultTransferType:    ftpserver.TransferTypeBinary,
	}, nil
}

func bannerOr(s string) string {
	if s == "" {
		return "filex"
	}
	return s
}

// ClientConnected sends the first line.
func (d *driver) ClientConnected(cc ftpserver.ClientContext) (string, error) {
	slog.Debug("ftps: client connected",
		slog.String("remote", cc.RemoteAddr().String()),
		slog.String("client", cc.GetClientVersion()))
	return bannerOr(d.srv.cfg.Banner), nil
}

func (d *driver) ClientDisconnected(cc ftpserver.ClientContext) {
	slog.Debug("ftps: client disconnected", slog.String("remote", cc.RemoteAddr().String()))
	d.liveMu.Lock()
	ls := d.live[cc.ID()]
	delete(d.live, cc.ID())
	d.liveMu.Unlock()
	ls.Leave()
}

// AuthUser authenticates and returns this session's view of the tree.
//
// ⚠ Every route ends at protocolauth, exactly as it does for SFTP and S3:
// identity, the tenant scope, the ACL resolver and the confinement arrive
// together or not at all. An account password or an API token both work
// (protocolauth.Any's shared order), and an account with TOTP enabled is
// refused a password — FTP has no second-factor channel either.
func (d *driver) AuthUser(cc ftpserver.ClientContext, user, pass string) (ftpserver.ClientDriver, error) {
	// Belt and braces over the library's own TLSRequired: if a future change to
	// the settings ever let a cleartext session through, it would still not get
	// a password past this point.
	if !cc.HasTLSForControl() {
		slog.Warn("ftps: refused a cleartext login attempt",
			slog.String("remote", cc.RemoteAddr().String()))
		return nil, errAuth
	}
	p, err := d.srv.cfg.Auth.Any(context.Background(), user, pass)
	if err != nil {
		// One error for every failure: "no such account", "wrong password" and
		// "that account may not use this protocol" must be indistinguishable,
		// or the endpoint is an account-enumeration oracle.
		return nil, errAuth
	}
	slog.Info("ftps: session",
		slog.String("user", user),
		slog.String("remote", cc.RemoteAddr().String()))
	// Data connections must be encrypted too. Without this a client can log in
	// over TLS and then transfer the file itself in the clear, which is the
	// FTPS misconfiguration people actually ship.
	if err := cc.SetTLSRequirement(ftpserver.MandatoryEncryption); err != nil {
		return nil, err
	}

	// ⚠ Registered so revoking the credential reaches this control connection.
	// An FTP session logs in once and stays open; without this, deleting the
	// token it used left the session transferring files. cc.Close() drops the
	// control connection, which takes any data transfer with it.
	ls := d.srv.cfg.Auth.Enter(p, "ftps", cc.RemoteAddr().String(), user, func() {
		_ = cc.Close()
	})
	d.liveMu.Lock()
	if d.live == nil {
		d.live = map[uint32]*protocolauth.LiveSession{}
	}
	d.live[cc.ID()] = ls
	d.liveMu.Unlock()

	return newFS(d.srv, p), nil
}

var errAuth = errors.New("authentication failed")

// GetTLSConfig hands the library the certificate.
func (d *driver) GetTLSConfig() (*tls.Config, error) { return d.srv.tls, nil }

// PreAuthUser runs before the password is asked for.
//
// It is where the TLS requirement is pinned per client, which the library
// checks at USER time — so a client that never issued AUTH TLS is refused
// before it can send a password rather than after.
func (d *driver) PreAuthUser(cc ftpserver.ClientContext, _ string) error {
	return cc.SetTLSRequirement(ftpserver.MandatoryEncryption)
}

// principalOf is a small helper for the tests.
func principalOf(cd ftpserver.ClientDriver) *protocolauth.Principal {
	if f, ok := cd.(*fs); ok {
		return f.principal
	}
	return nil
}

// lookupIP is net.LookupIP, swappable by tests that need DNS to fail on cue.
var lookupIP = net.LookupIP

// errPublicHostUnresolved marks a lookup failure — the one settings error that
// is worth retrying, because at container start it is usually the resolver
// that is not ready rather than the name that is wrong.
var errPublicHostUnresolved = errors.New("ftpsrv: cannot resolve public host")

// listenRetry / listenAttempts bound the start-up retry: two minutes in
// five-second steps. Vars so tests can shrink them.
var (
	listenRetry    = 5 * time.Second
	listenAttempts = 24
)

// passiveHostWithRetry resolves the public host for the PASV reply, retrying
// a failed LOOKUP for up to listenAttempts × listenRetry before giving up.
// Resolved once, then memoised: the library asks for settings at Listen time
// and the answer must not change underneath a running listener.
func (d *driver) passiveHostWithRetry() (string, error) {
	d.hostMu.Lock()
	defer d.hostMu.Unlock()
	if d.hostResolved {
		return d.host, nil
	}
	var err error
	for attempt := 1; ; attempt++ {
		var host string
		host, err = passiveAddress(d.srv.cfg.PublicHost)
		if err == nil {
			d.host, d.hostResolved = host, true
			return host, nil
		}
		if !errors.Is(err, errPublicHostUnresolved) || attempt >= listenAttempts {
			return "", err
		}
		// ⚠ Measured on fm.example.com (v0.25.0 rollout): Docker's embedded DNS
		// timed out once, four seconds into the container's life, and FTPS
		// stayed down for the life of that container with /healthz green and
		// the host port still published. The resolver not being ready yet is
		// the normal case at boot, not a misconfiguration.
		slog.Warn("ftps: public host not resolvable yet, retrying",
			slog.String("host", d.srv.cfg.PublicHost), slog.Int("attempt", attempt),
			slog.Int("of", listenAttempts), slog.String("err", err.Error()))
		time.Sleep(listenRetry)
	}
}

// passiveAddress turns whatever the operator put in FILEX_FTPS_PUBLIC_HOST
// into the literal IPv4 address passive replies have to carry.
//
// ⚠⚠ The PASV reply is a dotted quad on the wire — the protocol has no room
// for a name — and the library rejects anything else with "invalid passive
// IP", which stops the listener with a message that names neither the setting
// nor the fix. The setting is called PUBLIC_HOST and is documented as "the
// address the client would use", so a HOST NAME is the obvious thing to put
// there, and putting it there used to mean FTPS silently never started while
// the rest of the server came up healthy.
//
// Resolving it here means the natural value works. An address that is already
// a literal is passed through untouched, and an empty value keeps the
// library's own default (answer with the control connection's local address).
func passiveAddress(publicHost string) (string, error) {
	h := strings.TrimSpace(publicHost)
	if h == "" {
		return "", nil
	}
	if ip := net.ParseIP(h); ip != nil {
		// ⚠ An IPv6 literal cannot be advertised in a PASV reply at all; the
		// modern reply (EPSV) carries only a port and needs no address, so the
		// setting is meaningless there rather than merely wrong.
		if ip.To4() == nil {
			return "", fmt.Errorf("ftpsrv: public host %q is IPv6; PASV can only advertise an IPv4 address", h)
		}
		return h, nil
	}
	ips, err := lookupIP(h)
	if err != nil {
		return "", fmt.Errorf("%w %q: %w (an IPv4 address is also accepted)", errPublicHostUnresolved, h, err)
	}
	for _, ip := range ips {
		if v4 := ip.To4(); v4 != nil {
			return v4.String(), nil
		}
	}
	return "", fmt.Errorf("ftpsrv: public host %q has no IPv4 address; PASV cannot advertise an IPv6 one", h)
}
