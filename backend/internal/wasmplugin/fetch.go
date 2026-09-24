package wasmplugin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"github.com/brf-tech/filex/backend/pkg/pluginkit/wire"
)

// ── Installing from a URL or a GitHub repository ───────────────────────
//
// There is no marketplace. A plugin is a public repository whose root holds
// filex-app.json, and whose manifest says where the prebuilt module is
// (`wasm.url`, with `{tag}` standing for the ref) and what it hashes to
// (`wasm.sha256`). filex fetches the manifest, shows the permissions, then
// fetches the module and refuses it unless the hash matches. A plain URL
// install is the same thing with the two addresses spelled out.

var githubRepoRe = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)

// GitHubInput is the JSON body of a GitHub install.
type GitHubInput struct {
	Repo string `json:"github_repo"`
	Ref  string `json:"ref"`
}

// URLInput is the JSON body of a URL install.
type URLInput struct {
	URL         string `json:"url"`
	ManifestURL string `json:"manifest_url"`
	SHA256      string `json:"sha256"`
	Signature   string `json:"signature"`
}

// FetchGitHub resolves a repository + ref into an InstallInput (manifest and
// module fetched, sha256 taken from the manifest unless overridden).
func (r *Registry) FetchGitHub(ctx context.Context, in GitHubInput) (*InstallInput, error) {
	repo := strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(in.Repo, "https://github.com/"), "github.com/"))
	repo = strings.TrimSuffix(repo, ".git")
	repo = strings.TrimSuffix(repo, "/")
	if !githubRepoRe.MatchString(repo) {
		return nil, &InstallError{Code: ErrCodeFetch, Reason: FetchReasonBadRepo, Where: repo, Message: "github_repo must be owner/name"}
	}
	ref := strings.TrimSpace(in.Ref)
	refs := []string{ref}
	if ref == "" {
		refs = []string{"main", "master"}
	}
	var manifest []byte
	var usedRef string
	var lastErr error
	for _, rf := range refs {
		u := "https://raw.githubusercontent.com/" + repo + "/" + url.PathEscape(rf) + "/filex-app.json"
		b, err := r.fetch(ctx, u, wire.MaxManifestBytes)
		if err == nil {
			manifest, usedRef = b, rf
			break
		}
		lastErr = err
	}
	if manifest == nil {
		ie := fetchFailure(lastErr, FetchReasonManifestNotFound, repo)
		ie.Refs = refs
		ie.Message = "filex-app.json not found in " + repo + ": " + lastErr.Error()
		return nil, ie
	}
	m, err := ParseManifest(manifest)
	if err != nil {
		return nil, installErr(ErrCodeManifestInvalid, err.Error())
	}
	// A language pack IS its manifest: there is no module to fetch, so the
	// repository needs no release and no `wasm` block — a translator pushes a
	// JSON file and that is the whole distribution.
	if m.IsLanguagePack() {
		return &InstallInput{Manifest: manifest, Source: "github", SourceURL: "https://github.com/" + repo + "@" + usedRef}, nil
	}
	if m.Wasm == nil || strings.TrimSpace(m.Wasm.URL) == "" {
		return nil, installErr(ErrCodeManifestInvalid, "the manifest has no wasm.url; a repository install needs a prebuilt module address")
	}
	if strings.TrimSpace(m.Wasm.SHA256) == "" {
		return nil, installErr(ErrCodeSHA256Required, "the manifest has no wasm.sha256; a downloaded module must be pinned by hash")
	}
	wasmURL := strings.ReplaceAll(m.Wasm.URL, "{tag}", usedRef)
	if !strings.Contains(wasmURL, "://") {
		wasmURL = "https://raw.githubusercontent.com/" + repo + "/" + url.PathEscape(usedRef) + "/" + strings.TrimPrefix(wasmURL, "/")
	}
	wasm, err := r.fetch(ctx, wasmURL, r.opts.MaxWasmBytes)
	if err != nil {
		ie := fetchFailure(err, FetchReasonModuleNotFound, wasmURL)
		ie.Message = "module: " + err.Error()
		return nil, ie
	}
	return &InstallInput{
		Manifest: manifest, Wasm: bytes.NewReader(wasm), SHA256: m.Wasm.SHA256,
		Source: "github", SourceURL: "https://github.com/" + repo + "@" + usedRef,
	}, nil
}

// FetchURL resolves explicit module + manifest addresses.
func (r *Registry) FetchURL(ctx context.Context, in URLInput) (*InstallInput, error) {
	if strings.TrimSpace(in.ManifestURL) == "" {
		return nil, &InstallError{Code: ErrCodeFetch, Reason: FetchReasonMissingURL,
			Message: "manifest_url is required (and url, the module's address, unless the manifest is a language pack)"}
	}
	manifest, err := r.fetch(ctx, in.ManifestURL, wire.MaxManifestBytes)
	if err != nil {
		ie := fetchFailure(err, FetchReasonManifestNotFound, in.ManifestURL)
		ie.Message = "manifest: " + err.Error()
		return nil, ie
	}
	m, err := ParseManifest(manifest)
	if err != nil {
		return nil, installErr(ErrCodeManifestInvalid, err.Error())
	}
	if m.IsLanguagePack() {
		// `sha256`, when given, pins the MANIFEST (stagePack checks it); a
		// module address for an app that has no module is a mistake worth
		// saying out loud rather than a file quietly left unfetched.
		if strings.TrimSpace(in.URL) != "" {
			return nil, installErr(ErrCodeManifestInvalid, "this manifest is a language pack — it has no module, so leave the module address empty")
		}
		return &InstallInput{Manifest: manifest, SHA256: strings.TrimSpace(in.SHA256), Signature: in.Signature, Source: "url", SourceURL: in.ManifestURL}, nil
	}
	if strings.TrimSpace(in.URL) == "" {
		return nil, &InstallError{Code: ErrCodeFetch, Reason: FetchReasonMissingURL,
			Message: "url (the module's address) and manifest_url are required"}
	}
	sum := strings.TrimSpace(in.SHA256)
	if sum == "" && m.Wasm != nil {
		sum = m.Wasm.SHA256
	}
	if sum == "" {
		return nil, installErr(ErrCodeSHA256Required, "a downloaded module must be pinned by sha256 (in the request or in the manifest)")
	}
	wasm, err := r.fetch(ctx, in.URL, r.opts.MaxWasmBytes)
	if err != nil {
		ie := fetchFailure(err, FetchReasonModuleNotFound, in.URL)
		ie.Message = "module: " + err.Error()
		return nil, ie
	}
	return &InstallInput{
		Manifest: manifest, Wasm: bytes.NewReader(wasm), SHA256: sum, Signature: in.Signature,
		Source: "url", SourceURL: in.URL,
	}, nil
}

// fetchError says why one GET failed, so the caller can name the reason
// (InstallError.Reason) instead of passing an English sentence up.
type fetchError struct {
	reason string // FetchReasonBadURL | Unreachable | HTTPStatus | TooLarge
	status int
	msg    string
}

func (e *fetchError) Error() string { return e.msg }

// fetchFailure turns a fetch error into the install error for it. A 404 is
// `notFound` — the reason depends on WHAT was being fetched (the manifest or
// the module), which only the caller knows.
func fetchFailure(err error, notFound, where string) *InstallError {
	ie := &InstallError{Code: ErrCodeFetch, Where: where, Reason: FetchReasonUnreachable}
	var fe *fetchError
	if errors.As(err, &fe) {
		ie.Reason, ie.Status = fe.reason, fe.status
		if fe.reason == FetchReasonHTTPStatus && fe.status == http.StatusNotFound {
			ie.Reason = notFound
		}
	}
	return ie
}

// fetch GETs a URL with a size cap. Only https (and http for loopback, for
// tests) is accepted; redirects follow the client's policy.
func (r *Registry) fetch(ctx context.Context, raw string, limit int64) ([]byte, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return nil, &fetchError{reason: FetchReasonBadURL, msg: "bad url"}
	}
	switch u.Scheme {
	case "https":
	case "http":
		host := u.Hostname()
		if host != "127.0.0.1" && host != "localhost" && host != "::1" {
			return nil, &fetchError{reason: FetchReasonBadURL, msg: "plain http is only accepted for loopback addresses"}
		}
	default:
		return nil, &fetchError{reason: FetchReasonBadURL, msg: fmt.Sprintf("unsupported scheme %q", u.Scheme)}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, &fetchError{reason: FetchReasonBadURL, msg: err.Error()}
	}
	req.Header.Set("User-Agent", "filex-app-plugins/"+HostVersion)
	resp, err := r.opts.HTTP.Do(req)
	if err != nil {
		return nil, &fetchError{reason: FetchReasonUnreachable, msg: err.Error()}
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return nil, &fetchError{reason: FetchReasonHTTPStatus, status: resp.StatusCode, msg: fmt.Sprintf("http %d from %s", resp.StatusCode, u.Host)}
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, &fetchError{reason: FetchReasonUnreachable, msg: err.Error()}
	}
	if int64(len(b)) > limit {
		return nil, &fetchError{reason: FetchReasonTooLarge, msg: fmt.Sprintf("response exceeds %d bytes", limit)}
	}
	return b, nil
}

// decodeJSONBody is a small helper for the handlers' JSON install bodies.
func DecodeInstallBody(b []byte) (map[string]json.RawMessage, error) {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, err
	}
	return m, nil
}
