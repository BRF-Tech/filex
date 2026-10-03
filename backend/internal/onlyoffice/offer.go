package onlyoffice

// Offered files (filex 0.50): a file on this machine handed to the document
// server for ONE conversion - the office engine's input (internal/wasmplugin
// office.go), which lives in a job's private run directory and is not a
// catalogue document. An app's conversion is often the second step of a
// route (json → csv in the sandbox, then csv → xlsx here), so there is no
// node to name.
//
// The document server downloads it through the same door as everything else
// (FetchPath), with `o=<token>&exp=<unix>&p=convert&sig=<hmac>`:
//
//   - The token is 32 random bytes, the only name the file has outside this
//     process. The path never leaves it.
//   - The signature covers token, expiry and purpose with the secret in force,
//     like a purpose fetch (purpose.go), and the address lives PurposeFetchTTL.
//   - The offer is withdrawn the moment the conversion ends (the caller's
//     withdraw), so a leaked address is good for the length of one conversion
//     at most, and never for another file.

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"sync"
	"time"
)

// ErrOfferGone: the address names no file on offer (never offered, withdrawn,
// or expired).
var ErrOfferGone = errors.New("onlyoffice: no such offered file")

type offered struct {
	path string
	name string
	exp  time.Time
}

type offerRegistry struct {
	mu   sync.Mutex
	byID map[string]offered
}

func (s *Service) offers() *offerRegistry {
	s.offerOnce.Do(func() { s.offerReg = &offerRegistry{byID: map[string]offered{}} })
	return s.offerReg
}

func offerSignature(token string, exp int64, purpose, secret string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = fmt.Fprintf(mac, "o=%s&exp=%d&p=%s", token, exp, purpose)
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

// OfferFile puts the file at path on offer to the document server and answers
// the address it downloads it from, and the function that withdraws it (call
// it when the conversion ends, whatever the outcome). name is what the
// download is called. ErrNotConfigured when OnlyOffice is not configured.
func (s *Service) OfferFile(ctx context.Context, path, name string) (string, func(), error) {
	u, secret := s.settings(ctx)
	if u == "" || secret == "" {
		return "", func() {}, ErrNotConfigured
	}
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", func() {}, err
	}
	token := base64.RawURLEncoding.EncodeToString(raw[:])
	expAt := time.Now().Add(PurposeFetchTTL)
	reg := s.offers()
	reg.mu.Lock()
	now := time.Now()
	for id, o := range reg.byID {
		if now.After(o.exp) {
			delete(reg.byID, id)
		}
	}
	reg.byID[token] = offered{path: path, name: name, exp: expAt}
	reg.mu.Unlock()
	exp := expAt.Unix()
	v := url.Values{}
	v.Set("o", token)
	v.Set("exp", strconv.FormatInt(exp, 10))
	v.Set("p", PurposeConvert)
	v.Set("sig", offerSignature(token, exp, PurposeConvert, secret))
	withdraw := func() {
		reg.mu.Lock()
		delete(reg.byID, token)
		reg.mu.Unlock()
	}
	return s.callbackBase(ctx) + FetchPath + "?" + v.Encode(), withdraw, nil
}

// OpenOffered checks an offered file's address and opens the file: the file,
// the name it is offered under, or ErrUnknownPurpose, ErrSignatureExpired,
// ErrBadSignature or ErrOfferGone.
func (s *Service) OpenOffered(ctx context.Context, token string, exp int64, purpose, sig string) (*os.File, string, error) {
	if purpose != PurposeConvert {
		return nil, "", ErrUnknownPurpose
	}
	if exp < time.Now().Unix() {
		return nil, "", ErrSignatureExpired
	}
	_, secret := s.settings(ctx)
	if secret == "" || !hmac.Equal([]byte(offerSignature(token, exp, purpose, secret)), []byte(sig)) {
		return nil, "", ErrBadSignature
	}
	reg := s.offers()
	reg.mu.Lock()
	o, ok := reg.byID[token]
	reg.mu.Unlock()
	if !ok || time.Now().After(o.exp) {
		return nil, "", ErrOfferGone
	}
	f, err := os.Open(o.path)
	if err != nil {
		return nil, "", ErrOfferGone
	}
	return f, o.name, nil
}
