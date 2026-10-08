package webpush

import (
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"
)

// Key is the application server's VAPID key pair (RFC 8292): ECDSA on P-256.
// Its public half is the `applicationServerKey` every browser subscribes with,
// and a push service accepts a push for a subscription only when it is signed
// by the private half - so a leaked endpoint alone pushes nothing.
type Key struct {
	priv *ecdsa.PrivateKey
	// public is the uncompressed point (65 bytes), base64url.
	public string
}

// GenerateKey makes a new key pair.
func GenerateKey() (*Key, error) {
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("webpush: generate key: %w", err)
	}
	return newKey(priv)
}

func newKey(priv *ecdsa.PrivateKey) (*Key, error) {
	pub, err := priv.PublicKey.ECDH()
	if err != nil {
		return nil, fmt.Errorf("webpush: public key: %w", err)
	}
	if pub.Curve() != ecdh.P256() {
		return nil, errors.New("webpush: not a P-256 key")
	}
	return &Key{priv: priv, public: b64.EncodeToString(pub.Bytes())}, nil
}

// Public is the public key as a browser takes it (`applicationServerKey`) and
// as every request names it (`k=`): the uncompressed point, base64url.
func (k *Key) Public() string { return k.public }

// MarshalPrivate is the key as text (PKCS #8, base64) - what is sealed and
// stored. ⚠ A secret: it never leaves the server unsealed.
func (k *Key) MarshalPrivate() (string, error) {
	der, err := x509.MarshalPKCS8PrivateKey(k.priv)
	if err != nil {
		return "", fmt.Errorf("webpush: marshal key: %w", err)
	}
	return base64.StdEncoding.EncodeToString(der), nil
}

// ParseKey reads MarshalPrivate's text back.
func ParseKey(text string) (*Key, error) {
	der, err := base64.StdEncoding.DecodeString(strings.TrimSpace(text))
	if err != nil {
		return nil, errors.New("webpush: the stored key is not base64")
	}
	parsed, err := x509.ParsePKCS8PrivateKey(der)
	if err != nil {
		return nil, fmt.Errorf("webpush: parse key: %w", err)
	}
	priv, ok := parsed.(*ecdsa.PrivateKey)
	if !ok {
		return nil, errors.New("webpush: the stored key is not an ECDSA key")
	}
	return newKey(priv)
}

// vapidHeader is the JWT header of every VAPID token.
var vapidHeader = b64.EncodeToString([]byte(`{"typ":"JWT","alg":"ES256"}`))

// Authorization is the VAPID header of a request to endpoint, valid until exp
// (RFC 8292 §2-3): `vapid t=<JWT>, k=<public key>`. The token's audience is
// the endpoint's origin; subject is the contact a push service may write to
// about this server (a mailto: or an https: address - Apple refuses others).
//
// ⚠ exp no later than 24 hours ahead: a push service refuses a longer one.
func (k *Key) Authorization(endpoint, subject string, exp time.Time) (string, error) {
	u, err := url.Parse(endpoint)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return "", fmt.Errorf("%w: not an absolute address", ErrEndpoint)
	}
	claims, err := json.Marshal(map[string]any{
		"aud": u.Scheme + "://" + u.Host,
		"exp": exp.Unix(),
		"sub": subject,
	})
	if err != nil {
		return "", fmt.Errorf("webpush: claims: %w", err)
	}
	signing := vapidHeader + "." + b64.EncodeToString(claims)
	sum := sha256.Sum256([]byte(signing))
	r, s, err := ecdsa.Sign(rand.Reader, k.priv, sum[:])
	if err != nil {
		return "", fmt.Errorf("webpush: sign: %w", err)
	}
	// ES256 is the two 32-byte numbers side by side (RFC 7518 §3.4), not DER.
	sig := make([]byte, 64)
	r.FillBytes(sig[:32])
	s.FillBytes(sig[32:])
	return "vapid t=" + signing + "." + b64.EncodeToString(sig) + ", k=" + k.public, nil
}
