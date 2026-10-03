package onlyoffice

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/model"
)

// filexUnder starts a stand-in for filex that answers the document fetch
// endpoint the way the real handler does for a probe (handlers.OnlyOffice.Fetch
// hands node ProbeNodeID with a token to ServeFetchProbe). Anything else is a
// 404, the old /probe door included.
func filexUnder(t *testing.T, svc *Service) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if r.URL.Path != FetchPath || q.Get("n") != "0" || q.Get("t") == "" {
			http.NotFound(w, r)
			return
		}
		exp, _ := strconv.ParseInt(q.Get("exp"), 10, 64)
		status, body := svc.ServeFetchProbe(r.Context(), q.Get("t"), exp, q.Get("sig"))
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// dsOpts shapes a stand-in document server's conversion endpoint.
type dsOpts struct {
	// fetch: it downloads the URL it is handed (a signed request).
	fetch bool
	// answer is its JSON reply to a signed request.
	answer map[string]any
	// unsigned is its JSON reply to a request with no token. Nil: it treats
	// an unsigned request like a signed one (JWT off), fetch included.
	unsigned map[string]any
	// unsignedStatus, when set, is the HTTP status of the unsigned reply with
	// a body that is not JSON: the "could not be told" case.
	unsignedStatus int
	// tamper rewrites the URL before it is fetched.
	tamper func(string) string
}

// dsLog is what the stand-in saw.
type dsLog struct {
	mu       sync.Mutex
	handed   []string
	unsigned int
	signed   int
}

// seen returns the URLs handed over, in order, and how many requests carried
// no token. Read under the lock: the stand-in writes from its own goroutine.
func (l *dsLog) seen() ([]string, int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string(nil), l.handed...), l.unsigned
}

func signed(r *http.Request, req map[string]any) bool {
	_, inBody := req["token"]
	return inBody || r.Header.Get("Authorization") != ""
}

func docServer(t *testing.T, o dsOpts) (*httptest.Server, *dsLog) {
	t.Helper()
	log := &dsLog{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req map[string]any
		_ = json.NewDecoder(r.Body).Decode(&req)
		u, _ := req["url"].(string)
		isSigned := signed(r, req)
		log.mu.Lock()
		log.handed = append(log.handed, u)
		if isSigned {
			log.signed++
		} else {
			log.unsigned++
		}
		log.mu.Unlock()

		answer := o.answer
		if !isSigned {
			if o.unsignedStatus != 0 {
				w.WriteHeader(o.unsignedStatus)
				_, _ = io.WriteString(w, "<html>not json</html>")
				return
			}
			if o.unsigned != nil {
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(o.unsigned)
				return
			}
		}
		if o.fetch {
			if o.tamper != nil {
				u = o.tamper(u)
			}
			resp, err := http.Get(u) //nolint:noctx // test stand-in
			if err == nil {
				_, _ = io.Copy(io.Discard, resp.Body)
				resp.Body.Close()
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(answer)
	}))
	t.Cleanup(srv.Close)
	return srv, log
}

// enforcing is the answer a document server with JWT on gives an unsigned
// request: -8, before it reads anything.
var enforcing = map[string]any{"error": -8}

// TestReversePath_ArrivalIsTheVerdict is the whole point of the probe: what
// counts is whether the document server's request REACHED filex.
func TestReversePath_ArrivalIsTheVerdict(t *testing.T) {
	svc := &Service{FetchTTL: 0}
	filex := filexUnder(t, svc)
	svc.PublicURL = filex.URL

	t.Run("it fetched", func(t *testing.T) {
		ds, _ := docServer(t, dsOpts{fetch: true, answer: map[string]any{"endConvert": true, "fileUrl": "http://example/out.docx"}, unsigned: enforcing})
		svc.DocumentServerURL, svc.JWTSecret = ds.URL, "s3cret"

		res := svc.VerifyReversePath(context.Background())
		require.True(t, res.Checked)
		require.True(t, res.OK, res.Detail)
		require.Contains(t, res.URL, FetchPath)
	})

	t.Run("a rejected signature is not a broken route", func(t *testing.T) {
		// -8 means the document server refused the request before downloading
		// anything. Reporting that as "the route back is broken" would send an
		// operator to fix the wrong address.
		ds, _ := docServer(t, dsOpts{answer: map[string]any{"error": -8}, unsigned: enforcing})
		svc.DocumentServerURL, svc.JWTSecret = ds.URL, "s3cret"

		res := svc.VerifyReversePath(context.Background())
		require.False(t, res.Checked, "an unanswerable question must not read as a failure")
		require.Contains(t, res.Detail, "signature")
		require.NotNil(t, res.JWTEnforced)
		require.True(t, *res.JWTEnforced, "a document server that refuses filex's token does check tokens")
	})
}

// TestReversePath_DownloadErrorAdviceFollowsJWT is issue #80. Error -4 used to
// be answered with "allow private IP addresses" whatever the document server
// was doing. By default a document server fetches a signed request's address
// without its private-address filter (see downloadAdvice), so the advice
// depends on whether the document server enforces JWT, and filex now asks it.
func TestReversePath_DownloadErrorAdviceFollowsJWT(t *testing.T) {
	svc := &Service{}
	filex := filexUnder(t, svc)
	svc.PublicURL = filex.URL
	minus4 := map[string]any{"error": -4}

	t.Run("JWT enforced: the route, not the filter", func(t *testing.T) {
		ds, _ := docServer(t, dsOpts{answer: minus4, unsigned: enforcing})
		svc.DocumentServerURL, svc.JWTSecret = ds.URL, "s3cret"

		res := svc.VerifyReversePath(context.Background())
		require.True(t, res.Checked)
		require.False(t, res.OK)
		require.Equal(t, -4, res.Code)
		require.Contains(t, res.Detail, "could not download")
		require.Equal(t, AdviceRoute, res.Advice)
		require.NotNil(t, res.JWTEnforced)
		require.True(t, *res.JWTEnforced)
	})

	t.Run("JWT off: turn JWT on", func(t *testing.T) {
		// Answers -4 to the unsigned request as well: it took it.
		ds, _ := docServer(t, dsOpts{answer: minus4})
		svc.DocumentServerURL, svc.JWTSecret = ds.URL, "s3cret"

		res := svc.VerifyReversePath(context.Background())
		require.Equal(t, AdviceJWTOff, res.Advice)
		require.NotNil(t, res.JWTEnforced)
		require.False(t, *res.JWTEnforced)
	})

	t.Run("JWT unknown: check JWT first", func(t *testing.T) {
		ds, _ := docServer(t, dsOpts{answer: minus4, unsignedStatus: http.StatusBadGateway})
		svc.DocumentServerURL, svc.JWTSecret = ds.URL, "s3cret"

		res := svc.VerifyReversePath(context.Background())
		require.Equal(t, AdviceCheckJWT, res.Advice,
			"a -4 whose JWT state is unknown must send the operator to JWT first")
		require.Nil(t, res.JWTEnforced)
	})
}

// TestDownloadAdvice_SignedExemptionIsNotTiedToDocs81: the -4 advice said
// that only ONLYOFFICE Docs 8.1 and later fetch a signed request's address
// without the private-address filter, and that 7.4 to 8.0 filter signed
// requests too. The ONLYOFFICE server source says otherwise: the exemption is
// the default on every release that filters private addresses (7.4
// hard-codes it, 7.5 to 8.0 have allowPrivateIPAddressForSignedRequests, 8.1
// and later externalRequest.directIfIn.jwtToken, all on by default). An
// operator on 7.4 to 8.0 with JWT on was sent to ALLOW_PRIVATE_IP_ADDRESS,
// which changes nothing for them.
func TestDownloadAdvice_SignedExemptionIsNotTiedToDocs81(t *testing.T) {
	on, off := true, false
	for _, tc := range []struct {
		name   string
		jwt    *bool
		advice string
	}{
		{"JWT enforced", &on, AdviceRoute},
		{"JWT unknown", nil, AdviceCheckJWT},
	} {
		t.Run(tc.name, func(t *testing.T) {
			advice, detail := downloadAdvice("http://filex:5212/probe", tc.jwt)
			require.Equal(t, tc.advice, advice)
			require.Contains(t, detail, "by default", "the exemption is a default the operator can turn off")
			require.NotContains(t, detail, "8.1", "the signed-request exemption did not start in 8.1")
			require.NotContains(t, detail, "7.4 to 8.0", "7.4 to 8.0 do not filter signed requests by default")
			require.Contains(t, detail, "externalRequest.directIfIn", "names the setting that turns the exemption off")
		})
	}

	t.Run("JWT off: the filter, on by default since 7.4", func(t *testing.T) {
		advice, detail := downloadAdvice("http://filex:5212/probe", &off)
		require.Equal(t, AdviceJWTOff, advice)
		require.Contains(t, detail, "since ONLYOFFICE Docs 7.4")
		require.Contains(t, detail, "ALLOW_PRIVATE_IP_ADDRESS=true")
	})
}

// TestReversePath_GoesThroughTheSignedFetch: the probe walks the door a
// document walks. It used to have one of its own (/probe) that checked no
// signature, so a Test could pass while every document's fetch was refused.
func TestReversePath_GoesThroughTheSignedFetch(t *testing.T) {
	svc := &Service{}
	filex := filexUnder(t, svc)
	svc.PublicURL = filex.URL

	t.Run("the URL is the document fetch, signed", func(t *testing.T) {
		ds, log := docServer(t, dsOpts{fetch: true, answer: map[string]any{"endConvert": true}, unsigned: enforcing})
		svc.DocumentServerURL, svc.JWTSecret = ds.URL, "s3cret"

		res := svc.VerifyReversePath(context.Background())
		require.True(t, res.OK, res.Detail)
		handed, _ := log.seen()
		require.NotEmpty(t, handed)
		u, err := url.Parse(handed[0])
		require.NoError(t, err)
		require.Equal(t, FetchPath, u.Path)
		q := u.Query()
		require.Equal(t, "0", q.Get("n"))
		require.NotEmpty(t, q.Get("exp"))
		require.NotEmpty(t, q.Get("sig"), "the probe is signed like a document's URL")
	})

	t.Run("a bad signature is refused, as for a document", func(t *testing.T) {
		tamper := func(raw string) string {
			u, _ := url.Parse(raw)
			q := u.Query()
			q.Set("sig", "forged")
			u.RawQuery = q.Encode()
			return u.String()
		}
		ds, _ := docServer(t, dsOpts{fetch: true, tamper: tamper, answer: map[string]any{"endConvert": true}, unsigned: enforcing})
		svc.DocumentServerURL, svc.JWTSecret = ds.URL, "s3cret"

		res := svc.VerifyReversePath(context.Background())
		require.True(t, res.Checked, "the request arrived; the question was put")
		require.False(t, res.OK, "filex refused it, so the route a document needs does not work")
		require.Equal(t, AdviceFilexRefused, res.Advice)
		require.Contains(t, res.Detail, "signature")
	})

	t.Run("an unsaved secret is tested against itself", func(t *testing.T) {
		ds, _ := docServer(t, dsOpts{fetch: true, answer: map[string]any{"endConvert": true}, unsigned: enforcing})
		svc.DocumentServerURL, svc.JWTSecret = ds.URL, "saved"

		res := svc.CheckReversePath(context.Background(), &Target{
			DocumentServerURL: ds.URL, Secret: "candidate", CallbackURL: filex.URL, Draft: true,
		})
		require.True(t, res.OK, "a draft probe is verified with the draft's secret: %s", res.Detail)

		// The same candidate secret, NOT marked as a draft, is checked against
		// the configuration in force and refused: that is what a document
		// signed with the wrong secret would meet.
		res = svc.CheckReversePath(context.Background(), &Target{
			DocumentServerURL: ds.URL, Secret: "candidate", CallbackURL: filex.URL,
		})
		require.False(t, res.OK)
		require.Equal(t, AdviceFilexRefused, res.Advice)
	})
}

// TestReversePath_AsksWhetherJWTIsEnforced: the second request carries no
// token at all, and the answer is read as "does it enforce JWT".
func TestReversePath_AsksWhetherJWTIsEnforced(t *testing.T) {
	svc := &Service{}
	filex := filexUnder(t, svc)
	svc.PublicURL = filex.URL
	ok := map[string]any{"endConvert": true}

	ds, log := docServer(t, dsOpts{fetch: true, answer: ok, unsigned: enforcing})
	svc.DocumentServerURL, svc.JWTSecret = ds.URL, "s3cret"
	res := svc.VerifyReversePath(context.Background())
	require.True(t, res.OK, res.Detail)
	_, unsigned := log.seen()
	require.Equal(t, 1, unsigned, "exactly one request went without a token")
	require.NotNil(t, res.JWTEnforced)
	require.True(t, *res.JWTEnforced)

	// A document server with JWT off answers the unsigned request and fetches
	// what it was handed: the route works and JWT is still reported off.
	ds, _ = docServer(t, dsOpts{fetch: true, answer: ok})
	svc.DocumentServerURL = ds.URL
	res = svc.VerifyReversePath(context.Background())
	require.True(t, res.OK, res.Detail)
	require.NotNil(t, res.JWTEnforced)
	require.False(t, *res.JWTEnforced, "an unsigned request was obeyed")
}

// TestReversePath_UsesTheCallbackAddress proves the probe is sent to the
// address the document server is told to use, not to the browser-facing one.
func TestReversePath_UsesTheCallbackAddress(t *testing.T) {
	svc := &Service{PublicURL: "https://files.example.com", JWTSecret: "s3cret"}
	filex := filexUnder(t, svc)
	svc.LiveCallbackURL = func(context.Context) string { return filex.URL }

	ds, log := docServer(t, dsOpts{fetch: true, answer: map[string]any{"endConvert": true}, unsigned: enforcing})
	svc.DocumentServerURL = ds.URL

	res := svc.VerifyReversePath(context.Background())
	require.True(t, res.OK, res.Detail)
	seen, _ := log.seen()
	require.NotEmpty(t, seen)
	handed := seen[0]
	require.True(t, strings.HasPrefix(handed, filex.URL),
		"the document server must be sent to the callback address, got %q", handed)
	require.NotContains(t, handed, "files.example.com")
}

// TestProbeToken_IsOneShotAndUnguessable keeps the endpoint from becoming a
// thing a stranger can poke.
func TestProbeToken_IsOneShotAndUnguessable(t *testing.T) {
	svc := &Service{DocumentServerURL: "https://docs.example", JWTSecret: "s3cret"}
	exp := time.Now().Add(time.Minute).Unix()
	sig := fetchSignature(ProbeNodeID, exp, "s3cret")

	status, _ := svc.ServeFetchProbe(context.Background(), "", exp, sig)
	require.Equal(t, http.StatusNotFound, status, "an empty token must not be served")
	status, _ = svc.ServeFetchProbe(context.Background(), "0123456789abcdef0123456789abcdef", exp, sig)
	require.Equal(t, http.StatusNotFound, status, "a token filex never issued must not be served, even signed")

	tok := svc.probes().issue(false, "s3cret")
	require.Len(t, tok, 32)
	status, body := svc.ServeFetchProbe(context.Background(), tok, exp, sig)
	require.Equal(t, http.StatusOK, status)
	require.NotEmpty(t, body)
	require.False(t, strings.Contains(body, tok), "the answer must not echo the token")
}

// TestEditorConfig_PointsAtTheCallbackAddress: the two URLs the document
// server is handed must be built from the callback address. They used to be
// built from the public URL with no way to separate them, which is the gap
// issue #17 ended on.
func TestEditorConfig_PointsAtTheCallbackAddress(t *testing.T) {
	mtime := time.Unix(1700000000, 0)
	node := &model.Node{ID: 42, Name: "rapor.docx", PathHash: "abc", Size: 10, BackendMtime: &mtime}

	svc := &Service{
		DocumentServerURL: "https://office.example.com",
		JWTSecret:         "s3cret",
		PublicURL:         "https://files.example.com",
		LiveCallbackURL:   func(context.Context) string { return "http://filex:5212" },
	}
	cfg, err := svc.BuildConfigForNode(context.Background(), node, nil, "en", "edit")
	require.NoError(t, err)

	doc := cfg.Config["document"].(map[string]any)
	editor := cfg.Config["editorConfig"].(map[string]any)
	require.True(t, strings.HasPrefix(doc["url"].(string), "http://filex:5212/"),
		"the document fetch URL must use the callback address, got %v", doc["url"])
	require.True(t, strings.HasPrefix(editor["callbackUrl"].(string), "http://filex:5212/"),
		"the save callback must use the callback address, got %v", editor["callbackUrl"])

	// With no callback address configured, both fall back to the public URL —
	// which is every install where one address serves both.
	svc.LiveCallbackURL = nil
	cfg, err = svc.BuildConfigForNode(context.Background(), node, nil, "en", "edit")
	require.NoError(t, err)
	doc = cfg.Config["document"].(map[string]any)
	require.True(t, strings.HasPrefix(doc["url"].(string), "https://files.example.com/"))
}
