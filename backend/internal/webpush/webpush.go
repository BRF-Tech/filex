// Package webpush sends Web Push messages: RFC 8030 delivery, the payload
// encrypted for the receiving browser (RFC 8291, "aes128gcm") and every
// request signed with the application server's VAPID key (RFC 8292).
//
// It is the transport and nothing else. Who is told what, and when, is
// internal/notify's (push.go): a push is one more reader of a person's bell,
// never a channel with rules of its own.
//
// Standard library only: the key is ECDSA P-256 (crypto/ecdsa for the VAPID
// signature, crypto/ecdh for the payload's key agreement), the key schedule is
// crypto/hkdf, the record is AES-128-GCM.
//
// ⚠ An endpoint is written by the browser but reaches the server in a
// person's request, so it is an address a person chose. Policy.Check (an
// allow-list of the browsers' push services) and SafeClient (no address on
// this machine or a private network, no proxy, no redirect) are what keep the
// sender from being a way to make the server POST into its own network.
package webpush

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"strings"
)

// b64 is base64url without padding - the form browsers, push services and
// the VAPID header all use.
var b64 = base64.RawURLEncoding

// MaxEndpoint bounds an endpoint's length. The longest real ones (Windows
// Push Notification Services) are a few hundred characters.
const MaxEndpoint = 2048

// ErrEndpoint is what Policy.Check answers an endpoint it refuses with.
var ErrEndpoint = errors.New("webpush: endpoint refused")

// ErrKeys is what CheckKeys answers a subscription's unusable keys with.
var ErrKeys = errors.New("webpush: subscription keys unusable")

// EndpointHash is the lower-hex SHA-256 of an endpoint, as given. It is how a
// device is named on the wire (GET /api/notifications/push): the client
// hashes its own subscription's endpoint the same way to find itself in the
// list, and the endpoint itself - an address anybody holding it may push to,
// given the key - never leaves the server.
func EndpointHash(endpoint string) string {
	sum := sha256.Sum256([]byte(endpoint))
	return hex.EncodeToString(sum[:])
}

// ServiceOf is the push service an endpoint belongs to (its host), for a
// person reading their device list.
func ServiceOf(endpoint string) string {
	u, err := url.Parse(endpoint)
	if err != nil {
		return ""
	}
	return strings.ToLower(u.Hostname())
}

// decodeB64 reads base64url (the browsers' form), padded or not, and falls
// back to standard base64 for a client that wrote that.
func decodeB64(s string) ([]byte, error) {
	s = strings.TrimRight(strings.TrimSpace(s), "=")
	if b, err := b64.DecodeString(s); err == nil {
		return b, nil
	}
	b, err := base64.RawStdEncoding.DecodeString(s)
	if err != nil {
		return nil, fmt.Errorf("%w: not base64", ErrKeys)
	}
	return b, nil
}
