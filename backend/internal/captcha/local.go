package captcha

// The local fallback: a proof of work the browser does when the provider
// cannot be reached (docs/BOT-PROTECTION.md, "When the provider is down").
//
// ⚠⚠ This file implements a contract shared with the other products that run
// the same bot check, field by field and step by step - change nothing here
// that the vectors do not change first. The vectors (testdata/
// captcha-local-vectors.json, pinned by hash in local_test.go) are the gate:
// every product's verifier gives every one of them the same answer.
//
//	salt      = <32 lowercase hex>?expires=<unix seconds>
//	hash      = sha256(salt + number)        number in decimal ASCII
//	signature = HMAC-SHA256(key, hash)       hash as lowercase hex ASCII
//	solution  = base64(JSON{algorithm, salt, number, hash, signature})
//
// The server keeps no state for a challenge (the signature says it issued the
// hash); a solution is spent once through Consume, keyed by its signature.

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"math/big"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Algorithm is the only one there is.
const Algorithm = "SHA-256"

// Limits of the local fallback.
const (
	// MaxPayloadBytes is the longest `captcha_local` value read.
	MaxPayloadBytes = 2048
	// MaxSaltLength is the longest salt a challenge carries.
	MaxSaltLength = 256
	// DefaultLocalTTL is how long a challenge may be solved in.
	DefaultLocalTTL = 600 * time.Second
	// DefaultMaxNumber is the top of the range the browser scans: about one to
	// three seconds on a desktop, under eight on a phone.
	DefaultMaxNumber = 200000
)

// The reasons a solution is refused, in the order the steps run.
const (
	ReasonBadPayload   = "bad_payload"
	ReasonBadAlgorithm = "bad_algorithm"
	ReasonBadSalt      = "bad_salt"
	ReasonExpired      = "expired"
	ReasonBadNumber    = "bad_number"
	ReasonBadSignature = "bad_signature"
	ReasonBadHash      = "bad_hash"
	ReasonReplayed     = "replayed"
)

// Challenge is what the browser is given to solve.
type Challenge struct {
	Algorithm string `json:"algorithm"`
	Salt      string `json:"salt"`
	Hash      string `json:"hash"`
	Signature string `json:"signature"`
	MaxNumber int    `json:"maxnumber"`
}

// LocalResult is the answer to one solution.
type LocalResult struct {
	OK bool
	// Reason is "ok" or the first step that refused.
	Reason string
	// Signature is the solution's signature, lowercase, when it was read.
	Signature string
	// Expires is the challenge's end, when it was read.
	Expires time.Time
}

// Local issues and checks challenges.
type Local struct {
	// Key is the HMAC key. Every instance that checks a challenge must hold
	// the key that issued it.
	Key []byte
	// TTL is how long a challenge may be solved in; 0 = DefaultLocalTTL.
	TTL time.Duration
	// MaxNumber is the top of the range; 0 = DefaultMaxNumber.
	MaxNumber int
	// Now is the clock; nil = time.Now.
	Now func() time.Time
}

func (l *Local) now() time.Time {
	if l.Now != nil {
		return l.Now()
	}
	return time.Now()
}

func (l *Local) ttl() time.Duration {
	if l.TTL > 0 {
		return l.TTL
	}
	return DefaultLocalTTL
}

// Max is the top of the range this instance issues and accepts.
func (l *Local) Max() int {
	if l.MaxNumber > 0 {
		return l.MaxNumber
	}
	return DefaultMaxNumber
}

// HashFor is sha256(salt + number), lowercase hex.
func HashFor(salt string, number int64) string {
	sum := sha256.Sum256([]byte(salt + strconv.FormatInt(number, 10)))
	return hex.EncodeToString(sum[:])
}

// SignatureFor is HMAC-SHA256(key, lowercase hash), lowercase hex.
func SignatureFor(key []byte, hash string) string {
	m := hmac.New(sha256.New, key)
	m.Write([]byte(strings.ToLower(hash)))
	return hex.EncodeToString(m.Sum(nil))
}

// Issue makes a new challenge.
func (l *Local) Issue() (Challenge, error) {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return Challenge{}, err
	}
	n, err := rand.Int(rand.Reader, big.NewInt(int64(l.Max())+1))
	if err != nil {
		return Challenge{}, err
	}
	return l.IssueWith(hex.EncodeToString(salt), n.Int64(), l.now()), nil
}

// IssueWith makes the challenge for a given salt prefix, number and time
// (tests; Issue picks them at random).
func (l *Local) IssueWith(randomHex string, number int64, now time.Time) Challenge {
	salt := randomHex + "?expires=" + strconv.FormatInt(now.Add(l.ttl()).Unix(), 10)
	hash := HashFor(salt, number)
	return Challenge{Algorithm: Algorithm, Salt: salt, Hash: hash, Signature: SignatureFor(l.Key, hash), MaxNumber: l.Max()}
}

// Consume spends a solution: true the first time its signature is seen, false
// after (and when the store cannot tell). ttl is how long the mark must last.
type Consume func(signature string, ttl time.Duration) bool

var integerRe = regexp.MustCompile(`^-?(0|[1-9][0-9]*)$`)

// Verify checks a solution (the `captcha_local` value), steps 1 to 8 in
// their order; the first that fails names the reason. consume runs last, only
// for a solution that passed every other step.
func (l *Local) Verify(payload string, consume Consume) LocalResult {
	fail := func(reason string) LocalResult { return LocalResult{Reason: reason} }
	now := l.now()

	// 1) the payload: standard base64 of a JSON object with the five fields.
	if len(payload) > MaxPayloadBytes {
		return fail(ReasonBadPayload)
	}
	raw, err := base64.StdEncoding.Strict().DecodeString(payload)
	if err != nil {
		// An unpadded value is read too; the client always pads.
		raw, err = base64.RawStdEncoding.Strict().DecodeString(payload)
		if err != nil {
			return fail(ReasonBadPayload)
		}
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil || fields == nil {
		return fail(ReasonBadPayload)
	}
	str := func(name string) (string, bool) {
		v, ok := fields[name]
		if !ok {
			return "", false
		}
		var s string
		if json.Unmarshal(v, &s) != nil {
			return "", false
		}
		return s, true
	}
	algorithm, ok1 := str("algorithm")
	salt, ok2 := str("salt")
	hash, ok3 := str("hash")
	signature, ok4 := str("signature")
	if !ok1 || !ok2 || !ok3 || !ok4 {
		return fail(ReasonBadPayload)
	}
	// ⚠ A JSON integer: not a string ("4242"), not a bool, not 4242.0 or
	// 1e3 (another language reads those as a fraction), not one too big for
	// 64 bits.
	numRaw, ok := fields["number"]
	if !ok || !integerRe.Match(numRaw) {
		return fail(ReasonBadPayload)
	}
	number, err := strconv.ParseInt(string(numRaw), 10, 64)
	if err != nil {
		return fail(ReasonBadPayload)
	}

	// 2) the algorithm.
	if algorithm != Algorithm {
		return fail(ReasonBadAlgorithm)
	}

	// 3) the salt and the end it carries.
	expires, ok := ExpiresOf(salt)
	if !ok {
		return fail(ReasonBadSalt)
	}

	// 4) the time: the end itself is already too late.
	if now.Unix() >= expires {
		return fail(ReasonExpired)
	}

	// 5) the range, before the signature: a signed number over the top is
	// refused as a number.
	if number < 0 || number > int64(l.Max()) {
		return fail(ReasonBadNumber)
	}

	// 6) the signature, over the hash: "this server issued this hash".
	hash = strings.ToLower(hash)
	signature = strings.ToLower(signature)
	if subtle.ConstantTimeCompare([]byte(SignatureFor(l.Key, hash)), []byte(signature)) != 1 {
		return fail(ReasonBadSignature)
	}

	// 7) the work: this salt and this number make that hash.
	if subtle.ConstantTimeCompare([]byte(HashFor(salt, number)), []byte(hash)) != 1 {
		return fail(ReasonBadHash)
	}

	// 8) once.
	ttl := time.Duration(expires-now.Unix()) * time.Second
	if ttl < time.Second {
		ttl = time.Second
	}
	if consume != nil && !consume(signature, ttl) {
		return fail(ReasonReplayed)
	}
	return LocalResult{OK: true, Reason: "ok", Signature: signature, Expires: time.Unix(expires, 0)}
}

var expiresRe = regexp.MustCompile(`^[0-9]{1,12}$`)

// ExpiresOf reads `expires` (unix seconds) from the salt's query; any other
// parameter is ignored, the last `expires` wins.
func ExpiresOf(salt string) (int64, bool) {
	if len(salt) > MaxSaltLength {
		return 0, false
	}
	_, query, ok := strings.Cut(salt, "?")
	if !ok {
		return 0, false
	}
	found := ""
	have := false
	for _, pair := range strings.Split(query, "&") {
		k, v, _ := strings.Cut(pair, "=")
		if key, err := url.QueryUnescape(k); err != nil || key != "expires" {
			continue
		}
		val, err := url.QueryUnescape(v)
		if err != nil {
			continue
		}
		found, have = val, true
	}
	if !have || !expiresRe.MatchString(found) {
		return 0, false
	}
	n, err := strconv.ParseInt(found, 10, 64)
	if err != nil {
		return 0, false
	}
	return n, true
}

// EncodeSolution packs a solution the way the browser does.
func EncodeSolution(c Challenge, number int64) string {
	var b strings.Builder
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(struct {
		Algorithm string `json:"algorithm"`
		Salt      string `json:"salt"`
		Number    int64  `json:"number"`
		Hash      string `json:"hash"`
		Signature string `json:"signature"`
	}{Algorithm, c.Salt, number, c.Hash, c.Signature})
	return base64.StdEncoding.EncodeToString([]byte(strings.TrimRight(b.String(), "\r\n")))
}

// Solve finds the number by scanning, as the browser's worker does (tests,
// the CLI). -1 when there is none in range.
func Solve(c Challenge) int64 {
	want := strings.ToLower(c.Hash)
	for n := int64(0); n <= int64(c.MaxNumber); n++ {
		if HashFor(c.Salt, n) == want {
			return n
		}
	}
	return -1
}
