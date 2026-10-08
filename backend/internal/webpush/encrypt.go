package webpush

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
)

// recordSize is the one record a push carries (RFC 8188 `rs`): every payload
// filex sends fits in it.
const recordSize = 4096

// headerSize is salt (16) + rs (4) + idlen (1) + the sender's public key (65).
const headerSize = 16 + 4 + 1 + 65

// MaxPayload is the most plaintext one push carries: a push service must take
// a 4096-byte body (RFC 8291 §4), which holds the header, the plaintext, its
// one-byte delimiter and the 16-byte tag.
const MaxPayload = recordSize - headerSize - 1 - 16

// ErrTooLarge is a payload over MaxPayload.
var ErrTooLarge = errors.New("webpush: payload too large")

// The key schedule's info strings (RFC 8291 §3.3-3.4, RFC 8188 §2.2).
const (
	infoKey   = "WebPush: info\x00"
	infoCEK   = "Content-Encoding: aes128gcm\x00"
	infoNonce = "Content-Encoding: nonce\x00"
)

// CheckKeys reports whether a subscription's keys are what RFC 8291 needs: a
// P-256 point (65 bytes, uncompressed) and a 16-byte auth secret.
func CheckKeys(p256dh, auth string) error {
	if _, err := decodeKey(p256dh); err != nil {
		return err
	}
	_, err := decodeAuth(auth)
	return err
}

func decodeKey(s string) (*ecdh.PublicKey, error) {
	b, err := decodeB64(s)
	if err != nil {
		return nil, err
	}
	if len(b) != 65 || b[0] != 0x04 {
		return nil, fmt.Errorf("%w: p256dh is not an uncompressed P-256 point", ErrKeys)
	}
	pub, err := ecdh.P256().NewPublicKey(b)
	if err != nil {
		return nil, fmt.Errorf("%w: p256dh is not on the curve", ErrKeys)
	}
	return pub, nil
}

func decodeAuth(s string) ([]byte, error) {
	b, err := decodeB64(s)
	if err != nil {
		return nil, err
	}
	if len(b) != 16 {
		return nil, fmt.Errorf("%w: auth is not 16 bytes", ErrKeys)
	}
	return b, nil
}

// Encrypt seals plaintext for one browser subscription (RFC 8291): a fresh
// key agreed with the browser's p256dh key, the auth secret mixed in, a random
// salt, one aes128gcm record. The result is the request's body.
func Encrypt(plaintext []byte, p256dh, auth string) ([]byte, error) {
	ua, err := decodeKey(p256dh)
	if err != nil {
		return nil, err
	}
	authSecret, err := decodeAuth(auth)
	if err != nil {
		return nil, err
	}
	as, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("webpush: key agreement: %w", err)
	}
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return nil, fmt.Errorf("webpush: salt: %w", err)
	}
	return encrypt(plaintext, ua, authSecret, as, salt)
}

// encrypt is Encrypt with the sender's key and the salt given - the test
// vector of RFC 8291 Appendix A fixes both.
func encrypt(plaintext []byte, ua *ecdh.PublicKey, authSecret []byte, as *ecdh.PrivateKey, salt []byte) ([]byte, error) {
	if len(plaintext) > MaxPayload {
		return nil, ErrTooLarge
	}
	asPub := as.PublicKey().Bytes()
	cek, nonce, err := contentKeys(as, ua, ua.Bytes(), asPub, authSecret, salt)
	if err != nil {
		return nil, err
	}
	gcm, err := newGCM(cek)
	if err != nil {
		return nil, err
	}
	record := make([]byte, 0, len(plaintext)+1)
	record = append(record, plaintext...)
	record = append(record, 0x02) // the last (and only) record's delimiter
	out := make([]byte, 0, headerSize+len(record)+gcm.Overhead())
	out = append(out, salt...)
	out = binary.BigEndian.AppendUint32(out, recordSize)
	out = append(out, byte(len(asPub)))
	out = append(out, asPub...)
	return gcm.Seal(out, nonce, record, nil), nil
}

// contentKeys is the key schedule both halves run (RFC 8291 §3.4): the shared
// secret of own and peer, mixed with the auth secret and the two public keys
// (the browser's first), then the content key and nonce from the salt.
func contentKeys(own *ecdh.PrivateKey, peer *ecdh.PublicKey, uaPub, asPub, authSecret, salt []byte) (cek, nonce []byte, err error) {
	shared, err := own.ECDH(peer)
	if err != nil {
		return nil, nil, fmt.Errorf("webpush: key agreement: %w", err)
	}
	ikm, err := hkdf.Key(sha256.New, shared, authSecret, infoKey+string(uaPub)+string(asPub), 32)
	if err != nil {
		return nil, nil, err
	}
	if cek, err = hkdf.Key(sha256.New, ikm, salt, infoCEK, 16); err != nil {
		return nil, nil, err
	}
	if nonce, err = hkdf.Key(sha256.New, ikm, salt, infoNonce, 12); err != nil {
		return nil, nil, err
	}
	return cek, nonce, nil
}

func newGCM(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("webpush: cipher: %w", err)
	}
	return cipher.NewGCM(block)
}

// Decrypt is the browser's half of Encrypt: what a subscription whose private
// key is ua and whose auth secret is authSecret reads out of body. filex never
// receives a push; this is how its tests read what it sent.
func Decrypt(body []byte, ua *ecdh.PrivateKey, authSecret []byte) ([]byte, error) {
	if len(body) < 21 {
		return nil, errors.New("webpush: body shorter than its header")
	}
	salt := body[:16]
	idlen := int(body[20])
	if len(body) < 21+idlen+16 {
		return nil, errors.New("webpush: body shorter than its key id and tag")
	}
	asPub := body[21 : 21+idlen]
	as, err := ecdh.P256().NewPublicKey(asPub)
	if err != nil {
		return nil, fmt.Errorf("webpush: sender key: %w", err)
	}
	cek, nonce, err := contentKeys(ua, as, ua.PublicKey().Bytes(), asPub, authSecret, salt)
	if err != nil {
		return nil, err
	}
	gcm, err := newGCM(cek)
	if err != nil {
		return nil, err
	}
	plain, err := gcm.Open(nil, nonce, body[21+idlen:], nil)
	if err != nil {
		return nil, fmt.Errorf("webpush: open: %w", err)
	}
	// Padding is zeros after the delimiter; the last record's is 0x02.
	i := len(plain) - 1
	for i >= 0 && plain[i] == 0 {
		i--
	}
	if i < 0 || plain[i] != 0x02 {
		return nil, errors.New("webpush: no last-record delimiter")
	}
	return plain[:i], nil
}
