package oidc

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/brf-tech/filex/backend/internal/auth"
)

// probeTimeout bounds each network step of a provider test.
const probeTimeout = 10 * time.Second

// probeClient is the HTTP client the provider test uses; a test swaps it.
var probeClient = &http.Client{Timeout: probeTimeout}

// Probe tests an OpenID Connect configuration as far as it can be tested
// without a person signing in:
//
//	required    issuer, client ID and return address are filled in
//	discovery   <issuer>/.well-known/openid-configuration answers JSON
//	issuer      the document names the SAME issuer (the spec requires it, and
//	            go-oidc refuses a mismatch at sign-in)
//	endpoints   it names an authorization, a token and a key endpoint
//	client      the token endpoint accepts the client ID and secret — asked
//	            with a client-credentials request, which proves who the client
//	            is and grants nothing a person could use
//	redirect    NOT checkable here: an identity provider judges the return
//	            address only inside a real sign-in, so the result says so
//
// ⚠ "client" is only as good as the answer can be read. 401 or
// `invalid_client` means the ID or the secret was refused; 200 or a 400 with
// `unauthorized_client` means the client authenticated and is merely not
// allowed that grant; anything else is reported as not checked rather than
// guessed. A client with no secret (a public client) cannot be checked
// without a sign-in at all, and is said to be.
func (d *Driver) Probe(ctx context.Context, cfg map[string]any, _ *http.Request) []auth.ProbeCheck {
	var missing []string
	for _, k := range []string{"issuer", "client_id", "redirect_url"} {
		if auth.CfgString(cfg, k) == "" {
			missing = append(missing, k)
		}
	}
	if len(missing) > 0 {
		return []auth.ProbeCheck{auth.Check("required", auth.ProbeFail, "fields", strings.Join(missing, ","))}
	}
	out := []auth.ProbeCheck{auth.Check("required", auth.ProbeOK)}

	issuer := strings.TrimRight(auth.CfgString(cfg, "issuer"), "/")
	docURL := issuer + "/.well-known/openid-configuration"
	u, err := url.Parse(docURL)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		return append(out, auth.Check("discovery", auth.ProbeFail, "url", docURL, "reason", "bad_url"))
	}

	var doc struct {
		Issuer        string   `json:"issuer"`
		Authorization string   `json:"authorization_endpoint"`
		Token         string   `json:"token_endpoint"`
		JWKS          string   `json:"jwks_uri"`
		AuthMethods   []string `json:"token_endpoint_auth_methods_supported"`
	}
	status, body, err := probeGet(ctx, docURL)
	switch {
	case err != nil:
		return append(out, auth.Check("discovery", auth.ProbeFail, "url", docURL, "reason", auth.NetReason(err), "detail", err.Error()))
	case status != http.StatusOK:
		return append(out, auth.Check("discovery", auth.ProbeFail, "url", docURL, "reason", "status", "status", strconv.Itoa(status)))
	case json.Unmarshal(body, &doc) != nil:
		return append(out, auth.Check("discovery", auth.ProbeFail, "url", docURL, "reason", "not_json"))
	}
	out = append(out, auth.Check("discovery", auth.ProbeOK, "url", docURL))

	if strings.TrimRight(doc.Issuer, "/") != issuer {
		return append(out, auth.Check("issuer", auth.ProbeFail, "configured", issuer, "announced", doc.Issuer))
	}
	out = append(out, auth.Check("issuer", auth.ProbeOK, "issuer", doc.Issuer))

	var noEndpoint []string
	for name, v := range map[string]string{"authorization_endpoint": doc.Authorization, "token_endpoint": doc.Token, "jwks_uri": doc.JWKS} {
		if strings.TrimSpace(v) == "" {
			noEndpoint = append(noEndpoint, name)
		}
	}
	if len(noEndpoint) > 0 {
		return append(out, auth.Check("endpoints", auth.ProbeFail, "missing", strings.Join(sortedCopy(noEndpoint), ",")))
	}
	out = append(out, auth.Check("endpoints", auth.ProbeOK))

	out = append(out, probeClientCredentials(ctx, doc.Token, doc.AuthMethods,
		auth.CfgString(cfg, "client_id"), auth.CfgString(cfg, "client_secret")))
	out = append(out, auth.Check("redirect", auth.ProbeUnchecked, "url", auth.CfgString(cfg, "redirect_url")))
	return out
}

// probeClientCredentials asks the token endpoint whether it knows the client.
func probeClientCredentials(ctx context.Context, tokenURL string, methods []string, id, secret string) auth.ProbeCheck {
	if secret == "" {
		return auth.Check("client", auth.ProbeUnchecked, "client", id, "reason", "public")
	}
	form := url.Values{"grant_type": {"client_credentials"}}
	post := false
	if len(methods) > 0 && !contains(methods, "client_secret_basic") && contains(methods, "client_secret_post") {
		post = true
		form.Set("client_id", id)
		form.Set("client_secret", secret)
	}
	cctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(cctx, http.MethodPost, tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return auth.Check("client", auth.ProbeFail, "client", id, "reason", "bad_url", "detail", err.Error())
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	if !post {
		req.SetBasicAuth(url.QueryEscape(id), url.QueryEscape(secret))
	}
	resp, err := probeClient.Do(req)
	if err != nil {
		return auth.Check("client", auth.ProbeFail, "client", id, "reason", auth.NetReason(err), "detail", err.Error())
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	var answer struct {
		Error string `json:"error"`
	}
	_ = json.Unmarshal(raw, &answer)
	switch {
	case resp.StatusCode == http.StatusOK:
		return auth.Check("client", auth.ProbeOK, "client", id)
	case resp.StatusCode == http.StatusUnauthorized || answer.Error == "invalid_client":
		return auth.Check("client", auth.ProbeFail, "client", id, "reason", "credentials")
	case resp.StatusCode == http.StatusBadRequest && answer.Error == "unauthorized_client":
		return auth.Check("client", auth.ProbeOK, "client", id)
	}
	return auth.Check("client", auth.ProbeUnchecked, "client", id, "reason", "unclear", "status", strconv.Itoa(resp.StatusCode), "detail", answer.Error)
}

func probeGet(ctx context.Context, u string) (int, []byte, error) {
	cctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(cctx, http.MethodGet, u, nil)
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := probeClient.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return resp.StatusCode, nil, errors.New("reading the discovery document: " + err.Error())
	}
	return resp.StatusCode, body, nil
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

func sortedCopy(in []string) []string {
	out := append([]string(nil), in...)
	sort.Strings(out)
	return out
}
