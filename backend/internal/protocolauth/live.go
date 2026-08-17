package protocolauth

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/brf-tech/filex/backend/internal/auth"
)

// Revocation that reaches a connection which is already open.
//
// # The gap this closes
//
// Every credential check in this package happens ONCE, at the moment a caller
// authenticates. For HTTP that is the same thing as "on every request", because
// each request authenticates again. For the protocols this package was written
// for it is not: an SFTP session is a single authentication followed by hours of
// file operations, an FTPS control connection the same, and an NFS mount is a
// single authentication followed by DAYS.
//
// So "delete the token" did what the operator asked — the token could no longer
// be used to log in — and did not do what the operator MEANT. The session that
// token opened kept reading and writing, and nothing in the product would ever
// have closed it. Same for disabling an account, suspending a tenant, revoking
// an SSH key, or taking away a grant: all of them were true for the next login
// and false for the connection already in flight.
//
// # The shape
//
// Every stream protocol registers its session here and the registry re-checks
// them on a timer. A session whose credential no longer resolves is CUT: the
// connection is closed for the protocols that have one, and marked revoked for
// NFS, which has no connection to close because a mount is not a connection.
//
// ⚠ The sweep is the guarantee, not the notification. Wiring "when a token is
// deleted, kick its sessions" into the four handlers that delete credentials
// makes revocation instant, and Kick does exactly that — but it only covers the
// paths somebody remembered to wire. The sweep covers every path including the
// ones that do not go through the API at all: an admin disabling an account, an
// expiry passing, a tenant being suspended, a row edited directly in the
// database. Instant where we can, bounded everywhere.

// DefaultRevalidate is how often live sessions are re-checked. It is the upper
// bound on "how long does a revoked credential keep working on a connection
// that is already open", and it is deliberately the same order as the password
// cache TTL rather than something larger — one number, one answer.
const DefaultRevalidate = 30 * time.Second

// LiveSession is one registered connection.
type LiveSession struct {
	// Protocol is "sftp", "ftps", "nfs" — what the operator sees in a log line.
	Protocol string
	// Remote is the client address, Login what it typed. Both for the log.
	Remote string
	Login  string
	Since  time.Time

	id        uint64
	principal *Principal
	res       *Resolver
	closer    func()

	revoked atomic.Bool
	once    sync.Once
}

// Revoked reports whether this session has been cut.
//
// ⚠ It exists for NFS, which cannot be hung up on: a mount is a state the
// client holds, not a connection the server can close, so the only way to stop
// serving it is for every operation to start refusing. Connection protocols do
// not need to consult this — their closer already ended the conversation — but
// checking it costs an atomic load and makes the refusal deterministic instead
// of a race with the TCP close.
func (ls *LiveSession) Revoked() bool { return ls != nil && ls.revoked.Load() }

// Principal is the caller this session belongs to.
func (ls *LiveSession) Principal() *Principal {
	if ls == nil {
		return nil
	}
	return ls.principal
}

// Leave deregisters the session. Protocols defer it; calling it twice is fine.
func (ls *LiveSession) Leave() {
	if ls == nil || ls.res == nil {
		return
	}
	ls.res.mu.Lock()
	delete(ls.res.live, ls.id)
	ls.res.mu.Unlock()
}

// cut marks the session revoked and closes it, at most once.
func (ls *LiveSession) cut(reason string) {
	ls.revoked.Store(true)
	ls.once.Do(func() {
		slog.Info("protocolauth: live session revoked",
			slog.String("protocol", ls.Protocol),
			slog.String("user", ls.Login),
			slog.String("remote", ls.Remote),
			slog.String("reason", reason))
		if ls.closer != nil {
			// ⚠ In its own goroutine. A closer ends up inside the protocol
			// library's connection teardown, which may block on a write to a
			// client that has stopped reading — and the sweep must not stall
			// there holding up every other session's re-check.
			go ls.closer()
		}
	})
}

// Enter registers a live session.
//
// closer is how this protocol hangs up; nil is allowed and means "there is no
// connection to close" (NFS), in which case Revoked() is the whole mechanism.
func (r *Resolver) Enter(p *Principal, protocol, remote, login string, closer func()) *LiveSession {
	if r == nil || p == nil {
		return nil
	}
	ls := &LiveSession{
		Protocol:  protocol,
		Remote:    remote,
		Login:     login,
		Since:     time.Now(),
		id:        r.nextLive.Add(1),
		principal: p,
		res:       r,
		closer:    closer,
	}
	r.mu.Lock()
	if r.live == nil {
		r.live = map[uint64]*LiveSession{}
	}
	r.live[ls.id] = ls
	r.mu.Unlock()
	return ls
}

// LiveInfo is a snapshot of one session, for an operator view.
type LiveInfo struct {
	Protocol string
	Remote   string
	Login    string
	UserID   int64
	Since    time.Time
	Revoked  bool
}

// Live lists the registered sessions.
func (r *Resolver) Live() []LiveInfo {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]LiveInfo, 0, len(r.live))
	for _, ls := range r.live {
		info := LiveInfo{
			Protocol: ls.Protocol,
			Remote:   ls.Remote,
			Login:    ls.Login,
			Since:    ls.Since,
			Revoked:  ls.revoked.Load(),
		}
		if ls.principal != nil && ls.principal.User != nil {
			info.UserID = ls.principal.User.ID
		}
		out = append(out, info)
	}
	return out
}

// CredKind names the credential a Kick targets.
type CredKind int

const (
	// KickUser cuts every session belonging to an account, whatever it
	// authenticated with. This is what "disable this user" means.
	KickUser CredKind = iota
	KickToken
	KickAccessKey
	KickSSHKey
	KickExport
)

// Kick cuts every live session that authenticated with one credential, right
// now, without waiting for the sweep.
//
// It returns how many were cut, which is the number worth logging: "deleted the
// token, closed 2 sessions" is a different sentence from "deleted the token",
// and only the first one tells the operator the job is finished.
func (r *Resolver) Kick(kind CredKind, id int64) int {
	if r == nil {
		return 0
	}
	r.mu.Lock()
	targets := make([]*LiveSession, 0, 4)
	for _, ls := range r.live {
		if matches(ls.principal, kind, id) {
			targets = append(targets, ls)
		}
	}
	r.mu.Unlock()
	for _, ls := range targets {
		ls.cut("credential revoked")
	}
	// The password cache is keyed by the secret, not by the account, so there
	// is nothing to target — dropping all of it is cheap (a few bcrypt checks)
	// and keeps a disabled account from walking straight back in on a cached
	// password within the TTL.
	if kind == KickUser {
		r.Forget()
	}
	return len(targets)
}

func matches(p *Principal, kind CredKind, id int64) bool {
	if p == nil {
		return false
	}
	switch kind {
	case KickUser:
		return p.User != nil && p.User.ID == id
	case KickToken:
		return p.Token != nil && p.Token.ID == id
	case KickAccessKey:
		return p.AccessKey != nil && p.AccessKey.ID == id
	case KickSSHKey:
		return p.SSHKey != nil && p.SSHKey.ID == id
	case KickExport:
		return p.Export != nil && p.Export.ID == id
	}
	return false
}

// Sweep re-checks every live session and cuts the ones whose credential no
// longer resolves. Returns how many were cut.
func (r *Resolver) Sweep(ctx context.Context) int {
	if r == nil {
		return 0
	}
	r.mu.Lock()
	sessions := make([]*LiveSession, 0, len(r.live))
	for _, ls := range r.live {
		sessions = append(sessions, ls)
	}
	r.mu.Unlock()

	cut := 0
	for _, ls := range sessions {
		if ls.revoked.Load() {
			continue
		}
		if err := r.Recheck(ctx, ls.principal); err != nil {
			ls.cut(err.Error())
			cut++
			continue
		}
		// Still a valid caller — but the grants may have moved, and this
		// session has them cached. Dropping them here rather than only on the
		// ACL TTL means a revoked grant and a revoked credential land on the
		// same schedule instead of two different ones.
		ls.principal.ForgetACL()
	}
	return cut
}

// RunRevalidator sweeps on a timer until ctx is done. Started once, by the
// server, next to the other background services.
func (r *Resolver) RunRevalidator(ctx context.Context, every time.Duration) {
	if r == nil {
		return
	}
	if every <= 0 {
		every = DefaultRevalidate
	}
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			r.Sweep(ctx)
		}
	}
}

// Recheck asks the database whether a principal resolved earlier would still
// resolve now.
//
// ⚠ It re-reads every row rather than trusting the copies on the Principal —
// those are a photograph taken at login, and the whole point is to find out
// what has changed since. What it does NOT do is re-verify the secret: the
// password was never kept, the token's plaintext was never kept, and the SSH
// signature cannot be replayed. Possession was proven once; this checks that
// the credential still exists and still may be used.
func (r *Resolver) Recheck(ctx context.Context, p *Principal) error {
	if r == nil || p == nil || p.User == nil {
		return ErrUnauthorized
	}
	u, err := r.Store.GetUser(ctx, p.User.ID)
	if err != nil || u == nil {
		return ErrUnauthorized
	}
	if !auth.LoginAllowed(ctx, r.Store, r.MultiTenant, u) {
		return ErrUnauthorized
	}
	// ⚠ TOTP switched on since this session started. Password authentication
	// refuses such an account on every protocol (see Resolver.Password), so a
	// session that got in with a password before the switch is now a live 2FA
	// bypass — exactly the thing that gate exists to prevent. A session holding
	// a token, a key or an export is NOT affected: those are the individually
	// revocable credentials a TOTP account is supposed to use.
	if p.Token == nil && p.AccessKey == nil && p.SSHKey == nil && p.Export == nil && u.TOTPEnabled {
		return ErrUnauthorized
	}

	if p.Token != nil {
		tok, err := r.Store.GetAPITokenByID(ctx, p.Token.ID)
		if err != nil || tok == nil {
			return ErrUnauthorized
		}
		if tok.ExpiresAt != nil && tok.ExpiresAt.Before(time.Now()) {
			return ErrUnauthorized
		}
		// ⚠ Scopes narrowed under a live session. The session answers
		// HasScope from its cached copy, so a token edited from read,write down
		// to read would keep writing. Refusing the session is the honest
		// response: the client reconnects and gets the token it actually has.
		if tok.Scopes != p.Token.Scopes {
			return ErrUnauthorized
		}
	}
	if p.AccessKey != nil {
		k, err := r.Store.GetS3AccessKeyByID(ctx, p.AccessKey.ID)
		if err != nil || k == nil || !k.Usable(time.Now()) {
			return ErrUnauthorized
		}
	}
	if p.SSHKey != nil {
		k, err := r.Store.GetSSHPublicKeyByID(ctx, p.SSHKey.ID)
		if err != nil || k == nil || !k.Usable() {
			return ErrUnauthorized
		}
	}
	if p.Export != nil {
		e, err := r.Store.GetNFSExportByID(ctx, p.Export.ID)
		if err != nil || e == nil || !e.Usable(time.Now()) {
			return ErrUnauthorized
		}
	}

	// The account row on the Principal is refreshed so downstream code — quota
	// owner, audit actor, the tenant the scope was derived from — is not
	// working from a copy that is hours old.
	p.User = u
	return nil
}

// KickCredential is the small helper the API handlers call after deleting or
// disabling a credential, so revocation is instant rather than merely bounded.
// It is a free function taking a possibly-nil resolver because the handlers
// hold one that is nil when no protocol listener is configured.
func KickCredential(r *Resolver, kind CredKind, id int64) {
	if r == nil {
		return
	}
	_ = r.Kick(kind, id)
}
