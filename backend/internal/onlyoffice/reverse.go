package onlyoffice

// Reverse-path probe: does the DOCUMENT SERVER reach filex?
//
// # Why this exists
//
// Three machines must reach three addresses before a document opens and saves:
// the browser must load the editor's JavaScript, the filex process must reach
// the document server, and the document server must come back to filex for the
// bytes and for the save. The Test button measured the second. The admin page
// now measures the first from the browser. The third was written off as
// unmeasurable — "filex has no way to make another container issue a request
// on demand" — and an operator whose third leg was broken saw a green badge, a
// document that opened, and a save that never arrived (issue #17).
//
// That write-off was wrong. The document server has a conversion endpoint that
// takes a URL and downloads it, so filex CAN make it issue a request: hand it a
// one-shot URL of our own and see whether the request arrives. The verdict is
// the arrival, not the conversion — a conversion that fails for its own reasons
// still proves the route.
//
// # The same door a document uses (issue #80)
//
// The probe used to have a door of its own, /api/files/onlyoffice/probe, that
// asked for nothing: no signature, no check that filex has a configuration in
// force. A green third leg therefore proved less than it looked like: a reverse
// proxy that forwarded /probe and not /fetch, or a fetch refused for its
// signature, passed the Test and failed every document. The probe now goes
// through the document fetch endpoint itself, with a URL signed exactly the way
// a document's is (node ProbeNodeID, an expiry, the HMAC) and verified by the
// same check (VerifyFetchSignatureCtx). Only the storage read is left out, and
// that is because there is no document behind a probe; what a real document's
// fetch answered is kept per document (fetchlog.go).
//
// # The second question: does the document server enforce JWT?
//
// A document server with JWT off still checks the token of filex's editor
// configuration, against its own secret, and refuses it (measured with Docs
// 9.4: no document opens); it applies its private-address filter to every
// download, and sends its save callbacks unsigned - which filex refuses. None of that shows on the first request, so
// the Test asks a second time WITHOUT a token: a document server that enforces
// JWT answers -8 (its getRequestParams returns the "invalid token" error before
// it reads the request), and anything else means it took an unsigned request.

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

// probeTTL is how long an issued probe token is worth answering. Long enough
// for a document server to pick the job up, short enough that a token found in
// a log is already dead.
const probeTTL = 2 * time.Minute

// FetchPath is the endpoint the document server downloads a document from.
// The reverse-path probe is sent to it too.
const FetchPath = "/api/files/onlyoffice/fetch"

// ProbeNodeID is the node id a probe URL carries. No document has it: node ids
// start at 1, so the fetch handler can tell a probe from a document by it.
const ProbeNodeID int64 = 0

// probeBody is what a probe is answered with: a tiny, fixed text document. It
// carries no data of anyone's, because the caller is by definition
// unauthenticated.
const probeBody = "filex reachability probe\n"

type probeState struct {
	issued time.Time
	// arrived: the document server's request reached filex.
	arrived bool
	// status is what filex answered it (0 until it did). 200 means served.
	status int
	// reason is why filex refused it, empty when it served.
	reason string
	// draft: the probe was signed with values that are not the configuration
	// in force (the admin page's unsaved form), so it is verified against
	// them. secret is the one it was signed with.
	draft  bool
	secret string
}

type probeRegistry struct {
	mu     sync.Mutex
	tokens map[string]*probeState
}

func (r *probeRegistry) issue(draft bool, secret string) string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		// A failure here would mean a guessable token; refuse instead.
		return ""
	}
	tok := hex.EncodeToString(b[:])
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.tokens == nil {
		r.tokens = map[string]*probeState{}
	}
	// Opportunistic sweep: this map only ever holds the probes an operator
	// pressed a button for.
	for k, v := range r.tokens {
		if time.Since(v.issued) > probeTTL {
			delete(r.tokens, k)
		}
	}
	r.tokens[tok] = &probeState{issued: time.Now(), draft: draft, secret: secret}
	return tok
}

// arrive marks a live token as arrived and returns a copy of its state. An
// unknown or expired token is not a probe: nothing is recorded for it.
func (r *probeRegistry) arrive(tok string) (probeState, bool) {
	if tok == "" {
		return probeState{}, false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	st, ok := r.tokens[tok]
	if !ok || time.Since(st.issued) > probeTTL {
		return probeState{}, false
	}
	st.arrived = true
	return *st, true
}

// settle records what filex answered an arrived probe.
func (r *probeRegistry) settle(tok string, status int, reason string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if st, ok := r.tokens[tok]; ok {
		st.status, st.reason = status, reason
	}
}

func (r *probeRegistry) get(tok string) probeState {
	r.mu.Lock()
	defer r.mu.Unlock()
	if st, ok := r.tokens[tok]; ok {
		return *st
	}
	return probeState{}
}

// probes returns the registry, creating it on first use so a zero Service (the
// shape every unit test builds) needs no constructor change.
func (s *Service) probes() *probeRegistry {
	s.probeOnce.Do(func() { s.probeReg = &probeRegistry{} })
	return s.probeReg
}

// ServeFetchProbe answers a probe that arrived at the fetch endpoint (a request
// for node ProbeNodeID carrying a probe token `t`). It returns the HTTP status
// to answer with and, on 200, the body.
//
// It asks what a document's fetch asks, in the same order: is OnlyOffice
// configured in filex right now, and is the signature good (the same HMAC,
// checked by VerifyFetchSignatureCtx). A probe made from the admin page's
// unsaved values is verified against those values instead, because they are
// what is being tested. An unknown or expired token is a plain 404 and leaves
// no trace: a stranger who guesses at it learns nothing.
func (s *Service) ServeFetchProbe(ctx context.Context, token string, exp int64, sig string) (int, string) {
	if s == nil {
		return http.StatusNotFound, ""
	}
	st, ok := s.probes().arrive(token)
	if !ok {
		return http.StatusNotFound, ""
	}
	var err error
	if st.draft {
		err = verifyFetch(ProbeNodeID, exp, sig, st.secret)
	} else {
		if !s.EnabledCtx(ctx) {
			s.probes().settle(token, http.StatusServiceUnavailable, "onlyoffice is not configured in filex")
			return http.StatusServiceUnavailable, ""
		}
		err = s.VerifyFetchSignatureCtx(ctx, ProbeNodeID, exp, sig)
	}
	if err != nil {
		s.probes().settle(token, http.StatusUnauthorized, "signature refused: "+err.Error())
		return http.StatusUnauthorized, ""
	}
	s.probes().settle(token, http.StatusOK, "")
	return http.StatusOK, probeBody
}

// probeURL is a probe's address: the fetch endpoint, signed for ProbeNodeID
// the way a document's URL is signed for its node.
func probeURL(base, token, secret string) string {
	exp := time.Now().Add(probeTTL).Unix()
	v := url.Values{}
	v.Set("n", strconv.FormatInt(ProbeNodeID, 10))
	v.Set("exp", strconv.FormatInt(exp, 10))
	v.Set("sig", fetchSignature(ProbeNodeID, exp, secret))
	v.Set("t", token)
	return base + FetchPath + "?" + v.Encode()
}

// ReverseResult is one answer to "can the document server reach filex?".
type ReverseResult struct {
	// Checked is false when the question could not be put at all — nothing
	// configured, or the document server did not answer. A false here must
	// never be rendered as a failure of the route.
	Checked bool `json:"checked"`
	// OK is true only when the probe request actually ARRIVED at filex and
	// filex served it, the way it serves a document.
	OK bool `json:"ok"`
	// URL is the address the document server was asked to fetch, so an
	// operator reading a red result can try it themselves.
	URL string `json:"url,omitempty"`
	// Code is the document server's own error number when it returned one.
	// -4 is "error while downloading the document file" — the shape of a
	// broken reverse path. 0 means it did not report an error.
	Code int `json:"code,omitempty"`
	// Detail is the sentence to show the operator.
	Detail string `json:"detail,omitempty"`
	// Advice says what to look at first when the document server could not
	// download the probe (one of the Advice* codes). Empty when there is
	// nothing to advise. A client that wants its own wording keys off this,
	// never off Detail.
	Advice string `json:"advice,omitempty"`
	// JWTEnforced answers the second question: did the document server refuse
	// a request that carried no token? true: it enforces JWT. false: it took
	// an unsigned request, so it refuses filex's editor token (it checks it
	// against its own secret) and sends its save callbacks unsigned, which
	// filex refuses. Absent: it could not be told.
	JWTEnforced *bool `json:"jwt_enforced,omitempty"`
}

// Advice codes for a failed download (ReverseResult.Advice).
const (
	// AdviceCheckJWT: the document server answered -4 and filex cannot tell
	// whether it enforces JWT. Check that first: by default the document
	// server fetches the address in a signed request without its
	// private-address filter (see downloadAdvice for where that is set), so
	// with JWT on a container address is fine and the filter is not the
	// cause. The filter matters with JWT off, or when that default was turned
	// off.
	AdviceCheckJWT = "check_jwt"
	// AdviceJWTOff: -4, and the document server took an unsigned request. With
	// JWT off it applies its private-address filter to every download (a
	// container network is private), and its save callbacks arrive unsigned.
	// Turning JWT on with filex's secret fixes both.
	AdviceJWTOff = "jwt_off"
	// AdviceRoute: -4 from a document server that enforces JWT. By default
	// the filter does not apply to this signed request, so it could not reach
	// filex at that address (name, network, proxy).
	AdviceRoute = "route"
	// AdviceFilexRefused: the request reached filex and filex refused it.
	// Detail names the reason.
	AdviceFilexRefused = "filex_refused"
)

// Target is the configuration a reverse-path check runs against.
type Target struct {
	DocumentServerURL string
	Secret            string
	// CallbackURL is the address the document server is sent to. Empty means
	// filex's public URL, as for a document.
	CallbackURL string
	// Draft marks values that are not the ones in force (the admin page's
	// unsaved form). The probe is verified against them rather than against
	// the saved configuration, which would refuse a candidate secret.
	Draft bool
}

// LiveTarget is the configuration in force right now.
func (s *Service) LiveTarget(ctx context.Context) Target {
	u, sec := s.settings(ctx)
	return Target{DocumentServerURL: u, Secret: sec, CallbackURL: s.callbackBase(ctx)}
}

// askConvert posts one conversion request for a probe: the fixed text file
// at fileURL, made into a docx. secret signs it; an empty secret sends it
// unsigned. answered is false when the reply was not a ConvertService JSON
// answer. The request goes through the one conversion client (convert.go),
// the same one a thumbnail and an app's conversion use.
func askConvert(ctx context.Context, docURL, fileURL, key, secret string, async bool) (ans convertAnswer, status int, answered bool, err error) {
	c := &Converter{DocumentServerURL: docURL, Secret: secret}
	req := ConvertRequest{FetchURL: fileURL, FileType: "txt", OutputType: "docx", Title: "filex-probe.txt", Key: key}
	return c.post(ctx, req.payload(async))
}

// VerifyReversePath runs the reverse-path check against the configuration in
// force.
func (s *Service) VerifyReversePath(ctx context.Context) ReverseResult {
	return s.CheckReversePath(ctx, nil)
}

// CheckReversePath asks the document server to download a one-shot URL from
// filex and reports whether that request arrived and was served. t nil means
// the configuration in force.
//
// ⚠ The verdict is the ARRIVAL. The conversion result is a hint for the
// message, never the answer: a document server that downloaded our probe and
// then failed to convert it has still proved the only thing being asked.
func (s *Service) CheckReversePath(ctx context.Context, t *Target) ReverseResult {
	if s == nil {
		return ReverseResult{Detail: "onlyoffice is not wired"}
	}
	tgt := s.LiveTarget(ctx)
	if t != nil {
		tgt = *t
	}
	docURL := strings.TrimRight(tgt.DocumentServerURL, "/")
	if docURL == "" {
		return ReverseResult{Detail: "the document server URL is not configured"}
	}
	base := strings.TrimRight(tgt.CallbackURL, "/")
	if base == "" {
		base = strings.TrimRight(s.PublicURL, "/")
	}
	if base == "" {
		return ReverseResult{Detail: "filex has no public URL to be reached at"}
	}

	tok := s.probes().issue(tgt.Draft, tgt.Secret)
	if tok == "" {
		return ReverseResult{Detail: "could not generate a probe token"}
	}
	probe := probeURL(base, tok, tgt.Secret)

	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	answer, status, answered, err := askConvert(ctx, docURL, probe, tok, tgt.Secret, false)
	if err != nil {
		// We could not even ask. That is the SECOND leg failing, which the
		// server-side probe already reports; saying the reverse path is broken
		// here would blame the wrong address.
		return ReverseResult{URL: probe, Detail: "the document server did not answer the conversion request: " + err.Error()}
	}

	// ⚠ Asking is not the same as being answered. A 404 or a page of HTML
	// means whatever is at that URL did not take the request — an old document
	// server, a reverse proxy that swallowed the path, something else
	// entirely — and the honest report is "could not measure", not "the route
	// back is broken". Blaming an address nobody tested is how a diagnosis
	// sends an operator to the wrong machine.
	if !s.probes().get(tok).arrived && (status >= 400 || !answered) {
		return ReverseResult{
			URL: probe,
			Detail: fmt.Sprintf(
				"the document server did not answer the conversion endpoint (%s%s returned HTTP %d), so its route back to filex could not be measured",
				docURL, convertPath, status),
		}
	}

	// The document server may answer before it has finished fetching. Give the
	// arrival a moment to land rather than calling a slow route a broken one.
	// ⚠ Only when it did not already report an error: an error code is the
	// document server saying it is done, and waiting two more seconds for a
	// request that is never coming makes an operator watch a spinner for
	// nothing.
	settled := func() bool { return s.probes().get(tok).status != 0 }
	done := settled()
	for i := 0; !done && answer.Error == 0 && i < 20; i++ {
		select {
		case <-ctx.Done():
			i = 20
		case <-time.After(100 * time.Millisecond):
		}
		done = settled()
	}
	st := s.probes().get(tok)

	res := ReverseResult{Checked: true, OK: st.arrived && st.status == http.StatusOK, URL: probe, Code: answer.Error}
	if answer.Error == -8 || answer.Error == -9 {
		// It refused our token, so it does check tokens; the secret is wrong.
		res.JWTEnforced = boolPtr(true)
	} else {
		res.JWTEnforced = s.jwtEnforced(ctx, docURL, base, tgt)
	}

	switch {
	case res.OK:
		res.Detail = "the document server fetched " + probe + " through the signed document address - the route back to filex works"
	case st.arrived:
		res.Advice = AdviceFilexRefused
		res.Detail = fmt.Sprintf("the document server reached filex at %s, and filex refused the request (HTTP %d): %s",
			probe, st.status, st.reason)
	case answer.Error == -8 || answer.Error == -9:
		res.Checked = false
		res.Detail = "the document server rejected the request's signature, so it never tried to download anything - check that the JWT secret matches the one in the document server's configuration"
	case answer.Error == -4:
		res.Advice, res.Detail = downloadAdvice(probe, res.JWTEnforced)
	case answer.Error != 0:
		res.Detail = fmt.Sprintf("the document server answered error %d and no request reached filex at %s", answer.Error, probe)
	default:
		res.Detail = "no request reached filex at " + probe + " within the timeout"
	}
	return res
}

// downloadAdvice is the sentence for a -4, by what is known about JWT.
//
// ⚠ -4 does not say WHY, and the private-address filter is NOT the first
// suspect. This hint used to say "7.4+ refuses private IP addresses, set
// ALLOW_PRIVATE_IP_ADDRESS=true" for every -4, and then that only Docs 8.1
// and later skip the filter for a signed request. Both were wrong. What the
// ONLYOFFICE server source (github.com/ONLYOFFICE/server) says:
//
//   - 7.4.0 turned the filter on by default: request-filtering-agent
//     allowPrivateIPAddress went from true to false (commit e0aaeee6). The
//     Docker image's ALLOW_PRIVATE_IP_ADDRESS sets that key.
//   - A request whose token the document server verified is fetched without
//     the filter, and has been since before 7.4: 7.4 hard-codes it
//     (converter.js: filterPrivate = !withAuthorization), 7.5.0 to 8.0 make
//     it services.CoAuthoring.server.allowPrivateIPAddressForSignedRequests
//     (default true, commit 1deefe3e), and 8.1.0 replaced that with
//     externalRequest.directIfIn.jwtToken (default true, commit 446245c0;
//     a non-empty directIfIn.allowList takes its place).
//
// So on a document server with JWT on, a container address like
// http://filex:5212 passes by default, and that advice sent the operator to a
// setting that changes nothing (issue #80). The filter applies when JWT is OFF
// on the document server (no token is verified), or when the signed-request
// default was turned off.
func downloadAdvice(probe string, jwtEnforced *bool) (string, string) {
	head := "the document server could not download " + probe + " (its error -4). "
	switch {
	case jwtEnforced != nil && !*jwtEnforced:
		return AdviceJWTOff, head + "It also accepted a request without a token, so JWT is off on it: it then applies its private-address filter to every download (on by default since ONLYOFFICE Docs 7.4; a container network is private) and its save callbacks arrive unsigned, which filex refuses. Enable JWT on the document server (JWT_ENABLED=true) with the same secret as filex. Only if JWT has to stay off, allow private addresses there (ALLOW_PRIVATE_IP_ADDRESS=true)"
	case jwtEnforced != nil && *jwtEnforced:
		return AdviceRoute, head + "It enforces JWT, and by default it fetches the address in a signed request without its private-address filter, so it cannot reach filex at that address: check the name, the network and any proxy from inside its container. The filter applies to a signed request only if that default was turned off on the document server (externalRequest.directIfIn, or allowPrivateIPAddressForSignedRequests on Docs 7.5 to 8.0); then allow private addresses there (ALLOW_PRIVATE_IP_ADDRESS=true)"
	default:
		return AdviceCheckJWT, head + "Check first that JWT is enabled on the document server with the same secret as filex: by default it fetches the address in a signed request without its private-address filter, so with JWT on the document server cannot reach filex at that address - try it from inside its container. The private-address filter (ALLOW_PRIVATE_IP_ADDRESS) matters when JWT is off, or when that signed-request default was turned off (externalRequest.directIfIn, or allowPrivateIPAddressForSignedRequests on Docs 7.5 to 8.0)"
	}
}

// jwtEnforced asks the document server the second question: will it take a
// conversion request that carries no token? It is handed a probe URL of its
// own, so an unsigned request that is obeyed shows up as an arrival as well.
//
// ⚠ Asynchronous on purpose: a document server that enforces JWT answers -8
// before it reads anything, and one that does not has already said so by
// answering at all; waiting for its conversion would only make the operator
// wait. Nil means it could not be told (no answer, not JSON, an HTTP error).
func (s *Service) jwtEnforced(ctx context.Context, docURL, base string, tgt Target) *bool {
	tok := s.probes().issue(tgt.Draft, tgt.Secret)
	if tok == "" {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	answer, status, answered, err := askConvert(ctx, docURL, probeURL(base, tok, tgt.Secret), tok, "", true)
	switch {
	case s.probes().get(tok).arrived:
		return boolPtr(false)
	case err == nil && answered && answer.Error == -8:
		return boolPtr(true)
	case err != nil || status >= 400 || !answered:
		return nil
	default:
		return boolPtr(false)
	}
}

func boolPtr(b bool) *bool { return &b }
