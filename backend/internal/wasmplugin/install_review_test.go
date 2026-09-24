package wasmplugin

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/enginebin"
)

// The review says what the dry run already knows. Before v0.43.0's sweep,
// reinstalling an app that was already there passed the review and only
// "Install" answered "an app with this name is already installed", and an app
// that needs an engine this server lacks was reviewed as if it would work.

func TestDryRun_SaysTheAppIsAlreadyInstalled(t *testing.T) {
	h := newHarness(t, nil)
	p := h.install(t)

	in := echoInput(t, nil)
	in.DryRun = true
	_, dry, err := h.reg.Install(context.Background(), in)
	require.NoError(t, err, "a dry run of an installed app is a review, not a refusal")
	require.NotNil(t, dry.Installed, "the review must say the name is taken")
	assert.Equal(t, p.Row.ID, dry.Installed.ID, "…and which install it is, so the wizard can offer to upgrade it")
	assert.Equal(t, p.Row.Version, dry.Installed.Version)

	// The upgrade's own dry run is not a collision with itself.
	in = echoInput(t, nil)
	in.DryRun = true
	_, dry, err = h.reg.Upgrade(context.Background(), p.Row.ID, in)
	require.NoError(t, err)
	assert.Nil(t, dry.Installed)
}

func TestDryRun_NamesTheEnginesThisServerLacks(t *testing.T) {
	restore := enginebin.SetForTest(map[string]string{})
	defer restore()
	h := newHarness(t, nil)
	in := echoInput(t, nil)
	in.DryRun = true
	_, dry, err := h.reg.Install(context.Background(), in)
	require.NoError(t, err)
	require.Len(t, dry.EnginesMissing, 1, "echo asks for engines:ffmpeg and this server has none")
	assert.Equal(t, DryRunEngine{ID: "ffmpeg", Name: "FFmpeg"}, dry.EnginesMissing[0],
		"named the way a person reads it, not as a permission id")
	assert.Nil(t, dry.Installed, "nothing of that name is installed yet")

	restore2 := enginebin.SetForTest(map[string]string{"ffmpeg": "/usr/bin/ffmpeg"})
	defer restore2()
	h2 := newHarness(t, nil)
	in = echoInput(t, nil)
	in.DryRun = true
	_, dry, err = h2.reg.Install(context.Background(), in)
	require.NoError(t, err)
	assert.Empty(t, dry.EnginesMissing, "an engine that is there is not missing")
}

// fakeWeb answers every GET from a table of URL → status (404 for anything
// not in it); `down` makes every request fail before an answer.
type fakeWeb struct {
	ok   map[string]string
	down bool
}

func (f *fakeWeb) RoundTrip(req *http.Request) (*http.Response, error) {
	if f.down {
		return nil, errors.New("dial tcp: lookup raw.githubusercontent.com: no such host")
	}
	body, found := f.ok[req.URL.String()]
	status := http.StatusOK
	if !found {
		status = http.StatusNotFound
	}
	return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(body)), Header: http.Header{}, Request: req}, nil
}

func withWeb(web *fakeWeb) func(*Options) {
	return func(o *Options) { o.HTTP = &http.Client{Transport: web} }
}

func installError(t *testing.T, err error) *InstallError {
	t.Helper()
	var ie *InstallError
	require.True(t, errors.As(err, &ie), "want an InstallError, got %v", err)
	return ie
}

// A repository install that finds nothing says so with a reason the wizard
// can put in words — the repository and the refs it tried — instead of the
// English line it used to pass up ("filex-app.json not found in … http 404
// from raw.githubusercontent.com", in the Turkish wizard).
func TestFetchGitHub_SaysWhyAndWhat(t *testing.T) {
	h := newHarness(t, withWeb(&fakeWeb{}))
	_, err := h.reg.FetchGitHub(context.Background(), GitHubInput{Repo: "BRF-Tech/yok-boyle-bir-depo"})
	ie := installError(t, err)
	assert.Equal(t, ErrCodeFetch, ie.Code)
	assert.Equal(t, FetchReasonManifestNotFound, ie.Reason)
	assert.Equal(t, "BRF-Tech/yok-boyle-bir-depo", ie.Where)
	assert.Equal(t, []string{"main", "master"}, ie.Refs, "with no ref given, both default branches were tried")
	assert.Equal(t, http.StatusNotFound, ie.Status)

	_, err = h.reg.FetchGitHub(context.Background(), GitHubInput{Repo: "BRF-Tech/x", Ref: "v9.9.9"})
	assert.Equal(t, []string{"v9.9.9"}, installError(t, err).Refs)

	_, err = h.reg.FetchGitHub(context.Background(), GitHubInput{Repo: "not a repo"})
	assert.Equal(t, FetchReasonBadRepo, installError(t, err).Reason)

	// The manifest is there, the module it names is not (a release whose
	// asset was never uploaded).
	manifest := `{"manifest_version":1,"name":"a","version":"1.0.0","label":{"en":"A"},` +
		`"wasm":{"url":"https://github.com/o/r/releases/download/{tag}/plugin.wasm","sha256":"` + strings.Repeat("0", 64) + `"}}`
	h = newHarness(t, withWeb(&fakeWeb{ok: map[string]string{"https://raw.githubusercontent.com/o/r/v1/filex-app.json": manifest}}))
	_, err = h.reg.FetchGitHub(context.Background(), GitHubInput{Repo: "o/r", Ref: "v1"})
	ie = installError(t, err)
	assert.Equal(t, FetchReasonModuleNotFound, ie.Reason)
	assert.Equal(t, "https://github.com/o/r/releases/download/v1/plugin.wasm", ie.Where)

	h = newHarness(t, withWeb(&fakeWeb{down: true}))
	_, err = h.reg.FetchGitHub(context.Background(), GitHubInput{Repo: "o/r"})
	assert.Equal(t, FetchReasonUnreachable, installError(t, err).Reason)
}

func TestFetchURL_SaysWhyAndWhat(t *testing.T) {
	h := newHarness(t, withWeb(&fakeWeb{}))
	_, err := h.reg.FetchURL(context.Background(), URLInput{URL: "https://x.test/a.wasm"})
	assert.Equal(t, FetchReasonMissingURL, installError(t, err).Reason)

	_, err = h.reg.FetchURL(context.Background(), URLInput{URL: "http://x.test/a.wasm", ManifestURL: "http://x.test/filex-app.json"})
	ie := installError(t, err)
	assert.Equal(t, FetchReasonBadURL, ie.Reason, "plain http off loopback")
	assert.Equal(t, "http://x.test/filex-app.json", ie.Where)

	_, err = h.reg.FetchURL(context.Background(), URLInput{URL: "https://x.test/a.wasm", ManifestURL: "https://x.test/filex-app.json"})
	ie = installError(t, err)
	assert.Equal(t, FetchReasonManifestNotFound, ie.Reason)
	assert.Equal(t, "https://x.test/filex-app.json", ie.Where)
}
