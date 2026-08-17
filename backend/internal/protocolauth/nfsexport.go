package protocolauth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/brf-tech/filex/backend/internal/confine"
	"github.com/brf-tech/filex/backend/internal/model"
)

// NFS exports: the credential whose secret is a PATH.
//
// # Why this shape
//
// NFSv3 cannot authenticate a caller in a way filex can use. Real per-request
// identity means RPCSEC_GSS, which means Kerberos, which means a KDC and a
// machine keytab on every client; AUTH_SYS — the thing every NAS actually ships
// — is the client asserting "I am uid 1000" with nothing to check it against.
//
// So the identity binds to the EXPORT rather than to the request. The mount
// handshake names a path, that path carries 32 bytes of entropy, and the mount
// is pinned to one principal for its whole lifetime. Every operation inside it
// runs under that principal's scope, and the uid/gid on each request is
// discarded rather than trusted.
//
// ⚠⚠ It is a bearer secret in a place people do not treat as one — an /etc/fstab
// line, a mount script, a NAS admin page. That is the trade, and the reason the
// listener is off by default and meant for a LAN or a VPN.

// exportPathPrefix is where every export lives. Fixed so a client's mount line
// is predictable and so the secret is unmistakably the part after it.
const exportPathPrefix = "/x/"

// IssueExportRequest is what the caller asked for.
type IssueExportRequest struct {
	User  *model.User
	Token *model.APIToken
	Label string
	// Storage/Prefix confine the mount. Empty Storage means every storage the
	// account can see.
	Storage string
	Prefix  string
	// ReadOnly refuses every write through this export regardless of the
	// account's own permission.
	ReadOnly bool
	// AllowCIDRs is a comma-separated allow-list of client addresses.
	AllowCIDRs string
	ExpiresAt  *time.Time
}

// IssuedExport is a freshly minted export. The path is shown exactly once.
type IssuedExport struct {
	Export *model.NFSExport
	// Path is the full export path, e.g. `/x/9f2c…`. It is the credential:
	// it is not stored, only its hash is, and it cannot be recovered.
	Path string
}

// IssueExport mints an export.
//
// The permission rules are the same as an access key's, and enforced here for
// the same reason: "a credential can narrow but never widen" is a property of
// the credential rather than of the form that made it.
func (r *Resolver) IssueExport(ctx context.Context, req IssueExportRequest) (*IssuedExport, error) {
	if req.User == nil {
		return nil, ErrUnauthorized
	}
	parent, hasParent := confine.Root{}, false
	if req.Token != nil {
		parent, hasParent = tokenRoot(req.Token)
	}

	storage := strings.TrimSpace(req.Storage)
	prefix := strings.Trim(strings.TrimSpace(req.Prefix), "/")
	if storage == "" && prefix != "" {
		return nil, fmt.Errorf("%w: a prefix needs a storage", ErrWidensParent)
	}
	if hasParent {
		if storage == "" {
			storage, prefix = parent.Adapter, parent.Rel
		} else if !parent.Within(storage, prefix) {
			return nil, ErrWidensParent
		}
	}
	if _, err := parseCIDRs(req.AllowCIDRs); err != nil {
		// Refused rather than stored: an allow-list nobody can parse is an
		// allow-list that silently allows everything.
		return nil, err
	}

	expires := req.ExpiresAt
	if req.Token != nil && req.Token.ExpiresAt != nil {
		if expires == nil || expires.After(*req.Token.ExpiresAt) {
			expires = req.Token.ExpiresAt
		}
	}

	secret, err := newExportSecret()
	if err != nil {
		return nil, err
	}
	e := &model.NFSExport{
		UserID:      req.User.ID,
		Label:       strings.TrimSpace(req.Label),
		TokenHash:   HashExportSecret(secret),
		StorageName: storage,
		Prefix:      prefix,
		ReadOnly:    req.ReadOnly,
		AllowCIDRs:  strings.TrimSpace(req.AllowCIDRs),
		ExpiresAt:   expires,
	}
	if req.Token != nil {
		tid := req.Token.ID
		e.APITokenID = &tid
	}
	saved, err := r.Store.CreateNFSExport(ctx, e)
	if err != nil {
		return nil, err
	}
	return &IssuedExport{Export: saved, Path: exportPathPrefix + secret}, nil
}

// Export resolves a mount path into its caller.
//
// remote is the client's address, checked against the export's allow-list. It
// may be nil when there is no address to check (a test, or a transport that
// does not expose one), in which case an export WITH an allow-list is refused —
// a restriction that cannot be evaluated must not be treated as satisfied.
func (r *Resolver) Export(ctx context.Context, path string, remote net.IP) (*Principal, *model.NFSExport, error) {
	secret := strings.TrimPrefix(strings.TrimSpace(path), exportPathPrefix)
	secret = strings.Trim(secret, "/")
	if secret == "" || strings.Contains(secret, "/") {
		return nil, nil, ErrUnauthorized
	}
	e, err := r.Store.GetNFSExport(ctx, HashExportSecret(secret))
	if err != nil || e == nil || !e.Usable(time.Now()) {
		return nil, nil, ErrUnauthorized
	}
	if ok, err := cidrAllows(e.AllowCIDRs, remote); err != nil || !ok {
		return nil, nil, ErrUnauthorized
	}

	u, err := r.Store.GetUser(ctx, e.UserID)
	if err != nil || u == nil {
		return nil, nil, ErrUnauthorized
	}
	// The parent token must still be alive: the FK cascade covers deletion but
	// not expiry.
	var tok *model.APIToken
	if e.APITokenID != nil {
		tok, err = r.Store.GetAPITokenByID(ctx, *e.APITokenID)
		if err != nil || tok == nil {
			return nil, nil, ErrUnauthorized
		}
		if tok.ExpiresAt != nil && tok.ExpiresAt.Before(time.Now()) {
			return nil, nil, ErrUnauthorized
		}
	}

	p, err := r.principal(ctx, u, tok)
	if err != nil {
		return nil, nil, err
	}
	p.Export = e
	if e.StorageName != "" {
		p.Confine = &confine.Root{Adapter: e.StorageName, Rel: e.Prefix}
	} else if tok != nil {
		if root, ok := tokenRoot(tok); ok {
			p.Confine = &root
		}
	}
	_ = r.Store.TouchNFSExport(ctx, e.ID)
	return p, e, nil
}

// HashExportSecret is how the path secret is stored and looked up.
//
// sha256 and not bcrypt, deliberately: this is a bearer credential with 256
// bits of entropy, so there is nothing to brute-force, and the lookup happens
// on every mount — a work factor would buy nothing and cost a hundred
// milliseconds. (The same reasoning api_tokens uses.)
func HashExportSecret(secret string) string {
	sum := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(sum[:])
}

// newExportSecret returns 32 bytes of entropy, hex-encoded.
//
// Hex rather than base64 because it goes into a PATH: base64 contains `/` and
// `+`, and a mount path with a slash in the middle of its secret is a path that
// several clients and half the shell scripts in the world will mangle.
func newExportSecret() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("protocolauth: export secret: %w", err)
	}
	return hex.EncodeToString(b), nil
}

// parseCIDRs turns the stored allow-list into networks.
func parseCIDRs(list string) ([]*net.IPNet, error) {
	list = strings.TrimSpace(list)
	if list == "" {
		return nil, nil
	}
	var out []*net.IPNet
	for _, raw := range strings.Split(list, ",") {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		// A bare address is a valid thing to write and means "just this one".
		if ip := net.ParseIP(raw); ip != nil {
			bits := 32
			if ip.To4() == nil {
				bits = 128
			}
			out = append(out, &net.IPNet{IP: ip, Mask: net.CIDRMask(bits, bits)})
			continue
		}
		_, n, err := net.ParseCIDR(raw)
		if err != nil {
			return nil, fmt.Errorf("protocolauth: %q is not an address or a CIDR", raw)
		}
		out = append(out, n)
	}
	return out, nil
}

// cidrAllows reports whether remote is inside the allow-list.
//
// ⚠ An unparseable list, or a list with no address to check it against, denies.
// A restriction that cannot be evaluated must never be treated as satisfied —
// that is the direction in which a mistake becomes an open mount.
func cidrAllows(list string, remote net.IP) (bool, error) {
	nets, err := parseCIDRs(list)
	if err != nil {
		return false, err
	}
	if len(nets) == 0 {
		return true, nil
	}
	if remote == nil {
		return false, nil
	}
	for _, n := range nets {
		if n.Contains(remote) {
			return true, nil
		}
	}
	return false, nil
}
