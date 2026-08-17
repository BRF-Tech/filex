package protocolauth

import (
	"context"
	"crypto/rand"
	"encoding/base32"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/brf-tech/filex/backend/internal/confine"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/secretbox"
)

// Errors the issuing surface distinguishes. Authentication still collapses to
// ErrUnauthorized — these are for the person creating a key, who is already
// authenticated and deserves to know what went wrong.
var (
	// ErrNoSecretBox means no FILEX_SECRET_KEY is configured. Issuing fails
	// rather than storing the secret in the clear, because a credential
	// created "temporarily unencrypted" is one nobody goes back for.
	ErrNoSecretBox = errors.New("protocolauth: no secret key configured; set FILEX_SECRET_KEY to issue S3 access keys")
	// ErrWidensParent means the requested confinement is not inside the
	// confinement of the credential it is being minted from.
	ErrWidensParent = errors.New("protocolauth: an access key may narrow its parent's scope, never widen it")
)

// accessKeyIDAlphabet is uppercase base32 without padding: what every S3 client
// and every example in the world shows, and safe in a URL, an .ini file and a
// shell variable alike.
var accessKeyIDAlphabet = base32.StdEncoding.WithPadding(base32.NoPadding)

// IssueRequest describes a key to mint.
type IssueRequest struct {
	// User is the account the key belongs to. Required.
	User *model.User
	// Token, when set, is the API token the key inherits from. The key then
	// carries that token's scopes, its confinement and its expiry ceiling — it
	// is the token projected into a protocol that cannot carry a bearer value,
	// not a second, independent credential.
	Token *model.APIToken
	Label string
	// Bucket/Prefix optionally narrow the key further, in the shape S3 clients
	// already understand.
	Bucket string
	Prefix string
	// ExpiresAt optionally expires the key. It is clamped to the parent
	// token's expiry when there is one — a projection outliving its original
	// would be a way to launder a short-lived credential into a long-lived one.
	ExpiresAt *time.Time
}

// IssuedKey is what the caller shows the user exactly once.
type IssuedKey struct {
	Key *model.S3AccessKey
	// Secret is the plaintext. It exists only in this struct, only in memory,
	// and only until the response is written: the stored copy is sealed, and no
	// listing endpoint can ever recover it.
	Secret string
}

// Issue mints an S3 access key.
//
// The permission rule is enforced here rather than at the HTTP boundary,
// because "every credential we issue can connect, at exactly its permission"
// is a property of the credential, not of the form that created it. A key
// minted from a token cannot outlive it, cannot escape its confinement, and
// dies with it (the FK cascades).
func (r *Resolver) Issue(ctx context.Context, req IssueRequest) (*IssuedKey, error) {
	if req.User == nil {
		return nil, ErrUnauthorized
	}
	if r.Secrets == nil || !r.Secrets.Enabled() {
		return nil, ErrNoSecretBox
	}

	parent, hasParent := confine.Root{}, false
	if req.Token != nil {
		parent, hasParent = tokenRoot(req.Token)
	}

	bucket := strings.TrimSpace(req.Bucket)
	prefix := strings.Trim(strings.TrimSpace(req.Prefix), "/")
	if bucket == "" && prefix != "" {
		// A prefix with no bucket cannot be evaluated: prefixes are relative to
		// one storage. Refusing beats guessing which storage was meant.
		return nil, fmt.Errorf("%w: a prefix needs a bucket", ErrWidensParent)
	}
	if hasParent {
		if bucket == "" {
			// Inherit the parent's confinement verbatim rather than leaving the
			// key unconfined — the whole point is that it cannot widen.
			bucket, prefix = parent.Adapter, parent.Rel
		} else if !parent.Within(bucket, prefix) {
			return nil, ErrWidensParent
		}
	}

	expires := req.ExpiresAt
	if req.Token != nil && req.Token.ExpiresAt != nil {
		if expires == nil || expires.After(*req.Token.ExpiresAt) {
			expires = req.Token.ExpiresAt
		}
	}

	id, err := newAccessKeyID()
	if err != nil {
		return nil, err
	}
	secret, err := newAccessKeySecret()
	if err != nil {
		return nil, err
	}
	sealed, err := r.Secrets.Seal(secret)
	if err != nil {
		return nil, err
	}

	k := &model.S3AccessKey{
		AccessKeyID: id,
		SecretEnc:   sealed,
		UserID:      req.User.ID,
		Label:       strings.TrimSpace(req.Label),
		Bucket:      bucket,
		Prefix:      prefix,
		ExpiresAt:   expires,
	}
	if req.Token != nil {
		tid := req.Token.ID
		k.APITokenID = &tid
	}
	saved, err := r.Store.CreateS3AccessKey(ctx, k)
	if err != nil {
		return nil, err
	}
	return &IssuedKey{Key: saved, Secret: secret}, nil
}

// AccessKey resolves an access key id into its caller and the secret a
// signature must be verified against.
//
// ⚠ It returns the plaintext secret, and that is not an oversight: SigV4 gives
// the server a MAC, never the secret, so verification is only possible by
// recomputing the chain locally. The caller must use it for exactly that and
// must not log it, echo it, or keep it past the request.
//
// ⚠ It does NOT verify anything. A caller that forgets to check the signature
// has authenticated nobody — which is why the S3 layer's signature check and
// this lookup live next to each other and are tested together.
func (r *Resolver) AccessKey(ctx context.Context, accessKeyID string) (*Principal, string, error) {
	if strings.TrimSpace(accessKeyID) == "" {
		return nil, "", ErrUnauthorized
	}
	k, err := r.Store.GetS3AccessKey(ctx, accessKeyID)
	if err != nil || k == nil {
		return nil, "", ErrUnauthorized
	}
	if !k.Usable(time.Now()) {
		return nil, "", ErrUnauthorized
	}
	if r.Secrets == nil {
		return nil, "", ErrUnauthorized
	}
	secret, err := r.Secrets.Open(k.SecretEnc)
	if err != nil || secret == "" {
		// A key whose secret cannot be opened (wrong or missing FILEX_SECRET_KEY)
		// authenticates nobody. Reporting the reason on the wire would tell an
		// unauthenticated caller about our configuration.
		return nil, "", ErrUnauthorized
	}

	u, err := r.Store.GetUser(ctx, k.UserID)
	if err != nil || u == nil {
		return nil, "", ErrUnauthorized
	}

	// The parent token still has to be alive and valid: the FK cascade covers
	// deletion, but not expiry, so check it here rather than trusting the row.
	var tok *model.APIToken
	if k.APITokenID != nil {
		tok, err = r.Store.GetAPITokenByID(ctx, *k.APITokenID)
		if err != nil || tok == nil {
			return nil, "", ErrUnauthorized
		}
		if tok.ExpiresAt != nil && tok.ExpiresAt.Before(time.Now()) {
			return nil, "", ErrUnauthorized
		}
	}

	p, err := r.principal(ctx, u, tok)
	if err != nil {
		return nil, "", err
	}
	// The key's own confinement is the narrowest of everything upstream — Issue
	// already refused anything that widened, so what is stored is authoritative.
	if k.Bucket != "" {
		p.Confine = &confine.Root{Adapter: k.Bucket, Rel: k.Prefix}
	} else if tok != nil {
		if root, ok := tokenRoot(tok); ok {
			p.Confine = &root
		}
	}
	p.AccessKey = k

	_ = r.Store.TouchS3AccessKey(ctx, k.ID)
	return p, secret, nil
}

// newAccessKeyID returns a 20-character uppercase id, the shape AWS uses and
// therefore the shape every client's input validation expects.
func newAccessKeyID() (string, error) {
	b := make([]byte, 13) // 13 bytes -> 21 base32 chars, trimmed to 20
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("protocolauth: access key id: %w", err)
	}
	return "FLX" + accessKeyIDAlphabet.EncodeToString(b)[:17], nil
}

// newAccessKeySecret returns a 40-character secret, again matching the shape
// clients expect.
func newAccessKeySecret() (string, error) {
	b := make([]byte, 30)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("protocolauth: access key secret: %w", err)
	}
	return base64.RawStdEncoding.EncodeToString(b)[:40], nil
}

// SecretsFrom builds the box the resolver seals with. Kept here so a caller
// wiring a Resolver does not have to know which package owns the encryption.
func SecretsFrom(key string) (*secretbox.Box, error) { return secretbox.New(key) }
