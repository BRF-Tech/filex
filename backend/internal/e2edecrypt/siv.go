package e2edecrypt

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/subtle"
	"errors"
)

// AES-SIV (RFC 5297), the cipher of encrypted file names.
//
// The browser builds the same construction out of WebCrypto primitives
// (packages/core/src/lib/aessiv.ts); this is its Go twin. Both are pinned to
// RFC 5297 appendix A.1 and to testdata/name-vectors.json, which an
// independent implementation (Python `cryptography`'s AESSIV) produced — so a
// mistake the two hand-written versions happened to share would still fail.

const sivBlock = 16

// errSivAuth is returned when a ciphertext fails the synthetic-IV check:
// tampered, damaged, or sealed under another key.
var errSivAuth = errors.New("aes-siv: authentication failed")

type sivKey struct {
	mac    cipher.Block // S2V half (AES-CMAC)
	ctr    cipher.Block // CTR half
	k1, k2 [sivBlock]byte
}

// newSivKey imports a 32-byte (AES-128-SIV) or 64-byte (AES-256-SIV) key.
// The left half keys S2V, the right half keys CTR (RFC 5297 §2.6).
func newSivKey(raw []byte) (*sivKey, error) {
	if len(raw) != 32 && len(raw) != 64 {
		return nil, errors.New("aes-siv: key must be 32 or 64 bytes")
	}
	half := len(raw) / 2
	mac, err := aes.NewCipher(raw[:half])
	if err != nil {
		return nil, err
	}
	ctr, err := aes.NewCipher(raw[half:])
	if err != nil {
		return nil, err
	}
	k := &sivKey{mac: mac, ctr: ctr}
	var l [sivBlock]byte
	mac.Encrypt(l[:], l[:])
	k.k1 = dbl(l)
	k.k2 = dbl(k.k1)
	return k, nil
}

// dbl is doubling in GF(2^128) with the polynomial x^128 + x^7 + x^2 + x + 1.
func dbl(b [sivBlock]byte) [sivBlock]byte {
	var out [sivBlock]byte
	var carry byte
	for i := sivBlock - 1; i >= 0; i-- {
		out[i] = b[i]<<1 | carry
		carry = b[i] >> 7
	}
	if b[0]&0x80 != 0 {
		out[sivBlock-1] ^= 0x87
	}
	return out
}

func xor16(a, b [sivBlock]byte) [sivBlock]byte {
	var out [sivBlock]byte
	for i := range out {
		out[i] = a[i] ^ b[i]
	}
	return out
}

// cmac is AES-CMAC (RFC 4493) under the S2V half.
func (k *sivKey) cmac(msg []byte) [sivBlock]byte {
	n := (len(msg) + sivBlock - 1) / sivBlock
	if n == 0 {
		n = 1
	}
	complete := len(msg) > 0 && len(msg)%sivBlock == 0
	var x [sivBlock]byte
	for i := 0; i < n-1; i++ {
		for j := 0; j < sivBlock; j++ {
			x[j] ^= msg[i*sivBlock+j]
		}
		k.mac.Encrypt(x[:], x[:])
	}
	var last [sivBlock]byte
	rest := msg[(n-1)*sivBlock:]
	copy(last[:], rest)
	if complete {
		last = xor16(last, k.k1)
	} else {
		last[len(rest)] = 0x80
		last = xor16(last, k.k2)
	}
	x = xor16(x, last)
	k.mac.Encrypt(x[:], x[:])
	return x
}

// s2v is RFC 5297 §2.4 over the associated-data strings and the plaintext.
func (k *sivKey) s2v(ad [][]byte, p []byte) [sivBlock]byte {
	var zero [sivBlock]byte
	d := k.cmac(zero[:])
	for _, a := range ad {
		d = xor16(dbl(d), k.cmac(a))
	}
	if len(p) >= sivBlock {
		t := make([]byte, len(p))
		copy(t, p)
		off := len(p) - sivBlock
		for i := 0; i < sivBlock; i++ {
			t[off+i] ^= d[i]
		}
		return k.cmac(t)
	}
	var padded [sivBlock]byte
	copy(padded[:], p)
	padded[len(p)] = 0x80
	t := xor16(dbl(d), padded)
	return k.cmac(t[:])
}

func (k *sivKey) ctrXOR(v [sivBlock]byte, in []byte) []byte {
	q := v
	q[8] &= 0x7f
	q[12] &= 0x7f
	out := make([]byte, len(in))
	cipher.NewCTR(k.ctr, q[:]).XORKeyStream(out, in)
	return out
}

// seal is SIV-ENCRYPT: V || C.
func (k *sivKey) seal(p []byte, ad ...[]byte) []byte {
	v := k.s2v(ad, p)
	c := k.ctrXOR(v, p)
	return append(v[:], c...)
}

// open is SIV-DECRYPT; errSivAuth when the tag does not match.
func (k *sivKey) open(sealed []byte, ad ...[]byte) ([]byte, error) {
	if len(sealed) < sivBlock {
		return nil, errSivAuth
	}
	var v [sivBlock]byte
	copy(v[:], sealed[:sivBlock])
	p := k.ctrXOR(v, sealed[sivBlock:])
	t := k.s2v(ad, p)
	if subtle.ConstantTimeCompare(t[:], v[:]) != 1 {
		for i := range p {
			p[i] = 0
		}
		return nil, errSivAuth
	}
	return p, nil
}
