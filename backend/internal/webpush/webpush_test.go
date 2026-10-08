package webpush

import (
	"context"
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// The transport of Web Push (task #191). ⚠ Red on the code before it: the
// package does not exist.

func mustB64(t *testing.T, s string) []byte {
	t.Helper()
	b, err := b64.DecodeString(s)
	require.NoError(t, err)
	return b
}

// TestEncrypt_TheRFC8291Vector is RFC 8291 Appendix A to the byte: the
// application server's key and the salt fixed, the message a push service
// would carry. A key schedule that is wrong in any of its five steps (the
// order of the two public keys, which side salts which extract, a missing NUL
// in an info string, the record delimiter) changes every byte after the header.
func TestEncrypt_TheRFC8291Vector(t *testing.T) {
	plaintext := mustB64(t, "V2hlbiBJIGdyb3cgdXAsIEkgd2FudCB0byBiZSBhIHdhdGVybWVsb24")
	require.Equal(t, "When I grow up, I want to be a watermelon", string(plaintext))

	as, err := ecdh.P256().NewPrivateKey(mustB64(t, "yfWPiYE-n46HLnH0KqZOF1fJJU3MYrct3AELtAQ-oRw"))
	require.NoError(t, err)
	require.Equal(t, "BP4z9KsN6nGRTbVYI_c7VJSPQTBtkgcy27mlmlMoZIIgDll6e3vCYLocInmYWAmS6TlzAC8wEqKK6PBru3jl7A8",
		b64.EncodeToString(as.PublicKey().Bytes()))
	ua, err := ecdh.P256().NewPrivateKey(mustB64(t, "q1dXpw3UpT5VOmu_cf_v6ih07Aems3njxI-JWgLcM94"))
	require.NoError(t, err)
	uaPublic := "BCVxsr7N_eNgVRqvHtD0zTZsEc6-VV-JvLexhqUzORcxaOzi6-AYWXvTBHm4bjyPjs7Vd8pZGH6SRpkNtoIAiw4"
	require.Equal(t, uaPublic, b64.EncodeToString(ua.PublicKey().Bytes()))
	auth := mustB64(t, "BTBZMqHH6r4Tts7J_aSIgg")
	salt := mustB64(t, "DGv6ra1nlYgDCS1FRnbzlw")

	uaPub, err := decodeKey(uaPublic)
	require.NoError(t, err)
	body, err := encrypt(plaintext, uaPub, auth, as, salt)
	require.NoError(t, err)
	require.Equal(t,
		"DGv6ra1nlYgDCS1FRnbzlwAAEABBBP4z9KsN6nGRTbVYI_c7VJSPQTBtkgcy27mlmlMoZIIgDll6e3vCYLocInmYWAmS6TlzAC8wEqKK6PBru3jl7A_yl95bQpu6cVPTpK4Mqgkf1CXztLVBSt2Ks3oZwbuwXPXLWyouBWLVWGNWQexSgSxsj_Qulcy4a-fN",
		b64.EncodeToString(body))

	// And the browser reads it back.
	got, err := Decrypt(body, ua, auth)
	require.NoError(t, err)
	require.Equal(t, string(plaintext), string(got))
}

// subscriber is one browser: its key pair and auth secret, and the strings its
// PushSubscription.toJSON() gives.
type subscriber struct {
	priv   *ecdh.PrivateKey
	auth   []byte
	p256dh string
	secret string
}

func newSubscriber(t *testing.T) subscriber {
	t.Helper()
	priv, err := ecdh.P256().GenerateKey(rand.Reader)
	require.NoError(t, err)
	auth := make([]byte, 16)
	_, err = rand.Read(auth)
	require.NoError(t, err)
	return subscriber{priv: priv, auth: auth, p256dh: b64.EncodeToString(priv.PublicKey().Bytes()), secret: b64.EncodeToString(auth)}
}

func TestEncrypt_ARandomPushReadsBackAndFitsTheBody(t *testing.T) {
	s := newSubscriber(t)
	msg := []byte(`{"title":"filex","body":"Rapor: 1 dosya eklendi"}`)
	body, err := Encrypt(msg, s.p256dh, s.secret)
	require.NoError(t, err)
	got, err := Decrypt(body, s.priv, s.auth)
	require.NoError(t, err)
	require.Equal(t, string(msg), string(got))

	// Two pushes of one message share nothing a push service could match.
	again, err := Encrypt(msg, s.p256dh, s.secret)
	require.NoError(t, err)
	require.NotEqual(t, body[:16], again[:16], "the salt is fresh")
	require.NotEqual(t, body[21:86], again[21:86], "the sender key is fresh")

	// The largest payload makes a 4096-byte body; one byte more is refused.
	full, err := Encrypt(make([]byte, MaxPayload), s.p256dh, s.secret)
	require.NoError(t, err)
	require.Len(t, full, 4096)
	_, err = Encrypt(make([]byte, MaxPayload+1), s.p256dh, s.secret)
	require.ErrorIs(t, err, ErrTooLarge)
}

func TestCheckKeys_RefusesWhatIsNotASubscriptionsKey(t *testing.T) {
	s := newSubscriber(t)
	require.NoError(t, CheckKeys(s.p256dh, s.secret))
	require.NoError(t, CheckKeys(s.p256dh+"=", s.secret+"=="), "padding is tolerated")
	for _, c := range [][2]string{
		{"", s.secret},
		{s.p256dh, ""},
		{"not base64 at all!", s.secret},
		{b64.EncodeToString(make([]byte, 65)), s.secret},                          // not a point
		{b64.EncodeToString(append([]byte{0x04}, make([]byte, 64)...)), s.secret}, // not on the curve
		{b64.EncodeToString(s.priv.PublicKey().Bytes()[:33]), s.secret},           // compressed length
		{s.p256dh, b64.EncodeToString(make([]byte, 15))},                          // short auth
	} {
		require.ErrorIs(t, CheckKeys(c[0], c[1]), ErrKeys, "%q / %q", c[0], c[1])
	}
}

// TestVAPID_ATokenAPushServiceAccepts reads the Authorization header the way
// a push service does: `vapid t=<JWT>, k=<key>`, the JWT ES256 over its
// header and claims, the audience the endpoint's origin, an expiry within a
// day, the contact as given - and the key the same one the browser
// subscribed with, which survives the round trip through storage.
func TestVAPID_ATokenAPushServiceAccepts(t *testing.T) {
	k, err := GenerateKey()
	require.NoError(t, err)
	text, err := k.MarshalPrivate()
	require.NoError(t, err)
	back, err := ParseKey(text)
	require.NoError(t, err)
	require.Equal(t, k.Public(), back.Public(), "the stored key is the same key")
	require.Len(t, mustB64(t, k.Public()), 65)

	exp := time.Now().Add(12 * time.Hour)
	h, err := back.Authorization("https://fcm.googleapis.com/fcm/send/abc:def", "mailto:ops@example.com", exp)
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(h, "vapid t="), h)
	parts := strings.SplitN(strings.TrimPrefix(h, "vapid t="), ", k=", 2)
	require.Len(t, parts, 2)
	require.Equal(t, k.Public(), parts[1])

	jwt := strings.Split(parts[0], ".")
	require.Len(t, jwt, 3)
	var header, claims map[string]any
	require.NoError(t, json.Unmarshal(mustB64(t, jwt[0]), &header))
	require.Equal(t, map[string]any{"typ": "JWT", "alg": "ES256"}, header)
	require.NoError(t, json.Unmarshal(mustB64(t, jwt[1]), &claims))
	require.Equal(t, "https://fcm.googleapis.com", claims["aud"])
	require.Equal(t, "mailto:ops@example.com", claims["sub"])
	require.EqualValues(t, exp.Unix(), claims["exp"])

	sig := mustB64(t, jwt[2])
	require.Len(t, sig, 64, "ES256 is r||s, not DER")
	sum := sha256.Sum256([]byte(jwt[0] + "." + jwt[1]))
	require.True(t, ecdsa.Verify(&k.priv.PublicKey, sum[:],
		new(big.Int).SetBytes(sig[:32]), new(big.Int).SetBytes(sig[32:])), "the signature verifies with the key k= names")

	_, err = ParseKey("bm90IGEga2V5")
	require.Error(t, err)
}

func TestPolicy_OnlyThePushServicesOfTheBrowsers(t *testing.T) {
	var p Policy
	for _, ok := range []string{
		"https://fcm.googleapis.com/fcm/send/dXyz:APA91b",
		"https://updates.push.services.mozilla.com/wpush/v2/gAAAA",
		"https://web.push.apple.com/QGl6_abc",
		"https://wns2-par02p.notify.windows.com/w/?token=BQYAAA",
		"https://fcm.googleapis.com:443/x",
	} {
		require.NoError(t, p.Check(ok), ok)
	}
	for _, bad := range []string{
		"",
		"http://fcm.googleapis.com/fcm/send/x", // not https
		"https://169.254.169.254/latest/meta-data", // the cloud's metadata service
		"https://127.0.0.1/x",                      // this machine
		"https://[::1]/x",
		"https://10.0.0.5/x",
		"https://localhost/x", // a name, but not a push service
		"https://intranet.example/x",
		"https://fcm.googleapis.com.attacker.example/x", // a suffix trick
		"https://evilfcm.googleapis.com.example/x",
		"https://user:pw@fcm.googleapis.com/x", // credentials
		"https://fcm.googleapis.com:8443/x",    // another port
		"ftp://fcm.googleapis.com/x",
		"fcm.googleapis.com/x",
		"https://fcm.googleapis.com/x y",
		"https://fcm.googleapis.com/" + strings.Repeat("a", MaxEndpoint),
	} {
		require.ErrorIs(t, p.Check(bad), ErrEndpoint, bad)
	}

	// FILEX_PUSH_HOSTS adds a service; `*` takes any name - never an address.
	extra := Policy{Hosts: []string{"push.example.org"}}
	require.NoError(t, extra.Check("https://eu.push.example.org/s/1"))
	require.ErrorIs(t, extra.Check("https://fcm.googleapis.com/x"), ErrEndpoint, "a list given replaces the default one")
	anyHost := Policy{AnyHost: true}
	require.NoError(t, anyHost.Check("https://push.self-hosted.example/x"))
	require.ErrorIs(t, anyHost.Check("https://192.168.64.10/x"), ErrEndpoint)
	require.ErrorIs(t, anyHost.Check("http://push.self-hosted.example/x"), ErrEndpoint)
}

func TestPublicAddr(t *testing.T) {
	for _, s := range []string{"8.8.8.8", "142.250.185.106", "2a00:1450:4001:82a::200a"} {
		require.True(t, PublicAddr(netip.MustParseAddr(s)), s)
	}
	for _, s := range []string{"127.0.0.1", "::1", "10.1.2.3", "172.16.0.1", "192.168.64.1", "169.254.169.254",
		"100.64.12.7", "0.0.0.0", "fe80::1", "fd00::1", "::ffff:127.0.0.1", "224.0.0.1"} {
		require.False(t, PublicAddr(netip.MustParseAddr(s)), s)
	}
}

// TestSafeClient_NeverReachesThisMachine: even a URL the policy let through
// cannot be made to land on a private address - the dialer checks the address
// the name resolved to.
func TestSafeClient_NeverReachesThisMachine(t *testing.T) {
	hit := false
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { hit = true }))
	defer srv.Close()
	_, err := SafeClient(2*time.Second).Post(srv.URL, "application/octet-stream", strings.NewReader("x"))
	require.Error(t, err)
	require.Contains(t, err.Error(), "is not a public address")
	require.False(t, hit, "the request reached a loopback server")
}

// pushService is an httptest push service: it records what arrives and
// answers with status.
type pushService struct {
	mu     sync.Mutex
	status int
	got    []*http.Request
	bodies [][]byte
}

func (p *pushService) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	b, _ := io.ReadAll(r.Body)
	p.mu.Lock()
	p.got = append(p.got, r)
	p.bodies = append(p.bodies, b)
	st := p.status
	p.mu.Unlock()
	w.WriteHeader(st)
}

func TestSend_WhatAPushServiceReceivesAndHowItsAnswerIsRead(t *testing.T) {
	ps := &pushService{status: http.StatusCreated}
	srv := httptest.NewServer(ps)
	defer srv.Close()
	k, err := GenerateKey()
	require.NoError(t, err)
	s := newSubscriber(t)
	sender := &Sender{Client: srv.Client(), Subject: "mailto:ops@example.com"}
	sub := Subscription{Endpoint: srv.URL + "/push/abc", P256dh: s.p256dh, Auth: s.secret}

	res, err := sender.Send(context.Background(), k, sub, Message{Payload: []byte(`{"id":7}`), TTL: 12 * time.Hour, Urgency: "high"})
	require.NoError(t, err)
	require.Equal(t, http.StatusCreated, res.Status)
	require.False(t, res.Gone)
	require.Len(t, ps.got, 1)
	r := ps.got[0]
	require.Equal(t, http.MethodPost, r.Method)
	require.Equal(t, "/push/abc", r.URL.Path)
	require.Equal(t, "aes128gcm", r.Header.Get("Content-Encoding"))
	require.Equal(t, "application/octet-stream", r.Header.Get("Content-Type"))
	require.Equal(t, "43200", r.Header.Get("TTL"))
	require.Equal(t, "high", r.Header.Get("Urgency"))
	require.True(t, strings.HasSuffix(r.Header.Get("Authorization"), ", k="+k.Public()))
	plain, err := Decrypt(ps.bodies[0], s.priv, s.auth)
	require.NoError(t, err)
	require.Equal(t, `{"id":7}`, string(plain), "the push service carries ciphertext; the browser reads the message")
	require.NotContains(t, string(ps.bodies[0]), `"id"`)

	for _, c := range []struct {
		status int
		gone   bool
	}{{http.StatusGone, true}, {http.StatusNotFound, true}, {http.StatusForbidden, false}, {http.StatusTooManyRequests, false}, {http.StatusRequestEntityTooLarge, false}} {
		ps.mu.Lock()
		ps.status = c.status
		ps.mu.Unlock()
		res, err := sender.Send(context.Background(), k, sub, Message{Payload: []byte("x")})
		require.Error(t, err, "%d", c.status)
		require.Equal(t, c.status, res.Status)
		require.Equal(t, c.gone, res.Gone, "%d", c.status)
	}

	// A subscription whose keys are not a browser's is never sent.
	before := len(ps.got)
	_, err = sender.Send(context.Background(), k, Subscription{Endpoint: sub.Endpoint, P256dh: "x", Auth: s.secret}, Message{Payload: []byte("x")})
	require.True(t, errors.Is(err, ErrKeys))
	require.Len(t, ps.got, before)
}

func TestEndpointHash_IsTheHexSHA256TheClientComputes(t *testing.T) {
	// The value the settings pane computes with crypto.subtle for the same text.
	require.Equal(t, "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad", EndpointHash("abc"))
	require.Equal(t, "fcm.googleapis.com", ServiceOf("https://FCM.googleapis.com/fcm/send/x"))
}
