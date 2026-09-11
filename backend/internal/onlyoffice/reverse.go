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

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

// probeTTL is how long an issued probe token is worth answering. Long enough
// for a document server to pick the job up, short enough that a token found in
// a log is already dead.
const probeTTL = 2 * time.Minute

// ProbePath is the endpoint the document server is asked to download.
const ProbePath = "/api/files/onlyoffice/probe"

// probeBody is what that endpoint serves: a tiny, fixed text document. It
// carries no data of anyone's, because the caller is by definition
// unauthenticated.
const probeBody = "filex reachability probe\n"

type probeState struct {
	issued  time.Time
	fetched bool
}

type probeRegistry struct {
	mu     sync.Mutex
	tokens map[string]*probeState
}

func (r *probeRegistry) issue() string {
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
	// Opportunistic sweep — this map only ever holds the probes an operator
	// pressed a button for.
	for k, v := range r.tokens {
		if time.Since(v.issued) > probeTTL {
			delete(r.tokens, k)
		}
	}
	r.tokens[tok] = &probeState{issued: time.Now()}
	return tok
}

func (r *probeRegistry) mark(tok string) bool {
	if tok == "" {
		return false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	st, ok := r.tokens[tok]
	if !ok || time.Since(st.issued) > probeTTL {
		return false
	}
	st.fetched = true
	return true
}

func (r *probeRegistry) fetched(tok string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	st, ok := r.tokens[tok]
	return ok && st.fetched
}

// probes returns the registry, creating it on first use so a zero Service (the
// shape every unit test builds) needs no constructor change.
func (s *Service) probes() *probeRegistry {
	s.probeOnce.Do(func() { s.probeReg = &probeRegistry{} })
	return s.probeReg
}

// ServeProbe answers the document server's download of a probe URL. It reports
// whether the token was one filex issued and is still live; an unknown or
// expired token is served as not-found, so the endpoint says nothing to a
// stranger who guesses at it.
func (s *Service) ServeProbe(token string) (body string, ok bool) {
	if s == nil || !s.probes().mark(token) {
		return "", false
	}
	return probeBody, true
}

// ReverseResult is one answer to "can the document server reach filex?".
type ReverseResult struct {
	// Checked is false when the question could not be put at all — nothing
	// configured, or the document server did not answer. A false here must
	// never be rendered as a failure of the route.
	Checked bool `json:"checked"`
	// OK is true only when the probe request actually ARRIVED at filex.
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
}

// convertPath is the document server's conversion endpoint. It is the only
// documented way to hand OnlyOffice a URL and have it fetch it.
const convertPath = "/ConvertService.ashx"

// VerifyReversePath asks the document server to download a one-shot URL from
// filex and reports whether that request arrived.
//
// ⚠ The verdict is the ARRIVAL. The conversion result is a hint for the
// message, never the answer: a document server that downloaded our probe and
// then failed to convert it has still proved the only thing being asked.
func (s *Service) VerifyReversePath(ctx context.Context) ReverseResult {
	if s == nil {
		return ReverseResult{Detail: "onlyoffice is not wired"}
	}
	docURL, secret := s.settings(ctx)
	if docURL == "" {
		return ReverseResult{Detail: "the document server URL is not configured"}
	}
	base := s.callbackBase(ctx)
	if base == "" {
		return ReverseResult{Detail: "filex has no public URL to be reached at"}
	}

	tok := s.probes().issue()
	if tok == "" {
		return ReverseResult{Detail: "could not generate a probe token"}
	}
	probeURL := base + ProbePath + "?t=" + tok

	payload := map[string]any{
		"async":      false,
		"filetype":   "txt",
		"key":        tok,
		"outputtype": "docx",
		"title":      "filex-probe.txt",
		"url":        probeURL,
	}
	if secret != "" {
		// The document server rejects an unsigned request when a secret is
		// configured, and it rejects it BEFORE downloading anything — so an
		// unsigned probe would look exactly like an unreachable filex.
		if tok, err := signHS256(payload, secret); err == nil {
			payload["token"] = tok
		}
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return ReverseResult{URL: probeURL, Detail: "could not build the probe request"}
	}

	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		strings.TrimRight(docURL, "/")+convertPath, bytes.NewReader(body))
	if err != nil {
		return ReverseResult{URL: probeURL, Detail: err.Error()}
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	if secret != "" {
		if hdr, err := signHS256(map[string]any{"payload": payload}, secret); err == nil {
			req.Header.Set("Authorization", "Bearer "+hdr)
		}
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		// We could not even ask. That is the SECOND leg failing, which the
		// server-side probe already reports; saying the reverse path is broken
		// here would blame the wrong address.
		return ReverseResult{URL: probeURL, Detail: "the document server did not answer the conversion request: " + err.Error()}
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))

	var answer struct {
		Error      int    `json:"error"`
		FileURL    string `json:"fileUrl"`
		EndConvert bool   `json:"endConvert"`
	}
	answered := json.Unmarshal(raw, &answer) == nil

	// ⚠ Asking is not the same as being answered. A 404 or a page of HTML
	// means whatever is at that URL did not take the request — an old document
	// server, a reverse proxy that swallowed the path, something else
	// entirely — and the honest report is "could not measure", not "the route
	// back is broken". Blaming an address nobody tested is how a diagnosis
	// sends an operator to the wrong machine.
	if !s.probes().fetched(tok) && (resp.StatusCode >= 400 || !answered) {
		return ReverseResult{
			URL: probeURL,
			Detail: fmt.Sprintf(
				"the document server did not answer the conversion endpoint (%s%s returned HTTP %d), so its route back to filex could not be measured",
				strings.TrimRight(docURL, "/"), convertPath, resp.StatusCode),
		}
	}

	// The document server may answer before it has finished fetching. Give the
	// arrival a moment to land rather than calling a slow route a broken one.
	// ⚠ Only when it did not already report an error: an error code is the
	// document server saying it is done, and waiting two more seconds for a
	// request that is never coming makes an operator watch a spinner for
	// nothing.
	arrived := s.probes().fetched(tok)
	for i := 0; !arrived && answer.Error == 0 && i < 20; i++ {
		select {
		case <-ctx.Done():
			i = 20
		case <-time.After(100 * time.Millisecond):
		}
		arrived = s.probes().fetched(tok)
	}

	res := ReverseResult{Checked: true, OK: arrived, URL: probeURL, Code: answer.Error}
	switch {
	case arrived:
		res.Detail = "the document server fetched " + probeURL + " — the route back to filex works"
	case answer.Error == -8 || answer.Error == -9:
		res.Checked = false
		res.Detail = "the document server rejected the request's signature, so it never tried to download anything — check that the JWT secret matches the one in the document server's configuration"
	case answer.Error == -4:
		res.Detail = "the document server could not download " + probeURL + " (its error -4). It is running, and it cannot reach filex at that address"
	case answer.Error != 0:
		res.Detail = fmt.Sprintf("the document server answered error %d and no request reached filex at %s", answer.Error, probeURL)
	default:
		res.Detail = "no request reached filex at " + probeURL + " within the timeout"
	}
	return res
}
