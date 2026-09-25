package update

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func decidePkg(t *testing.T, current string, pol Policy, m PackageManager, releases ...Release) Decision {
	t.Helper()
	cur, err := ParseVersion(current)
	require.NoError(t, err)
	return Decide(Input{
		Current:  cur,
		Manifest: &Manifest{Releases: releases},
		Policy:   pol,
		Mode:     ModePackage,
		Manager:  m,
		Now:      time.Date(2026, 7, 29, 4, 0, 0, 0, time.UTC),
	})
}

// A package manager owns the binary, so no policy moves it: the same patch
// that a binary install takes by itself is an instruction here, and so is
// what a binary install would only have offered.
func TestDecide_PackageNeverSelfApplies(t *testing.T) {
	// Premise: under this policy a binary install applies the patch itself.
	require.Equal(t, ActionAuto, decide(t, "v0.7.5", PolicyPatch, ModeBinary, rel("v0.7.6")).Action)

	d := decidePkg(t, "v0.7.5", PolicyPatch, ManagerHomebrew, rel("v0.7.6"))
	assert.Equal(t, ActionInstruct, d.Action)
	assert.Equal(t, "server.update.reason.package", d.ReasonKey)
	assert.Equal(t, map[string]string{"manager": "Homebrew"}, d.ReasonVars)
	assert.Contains(t, d.Reason, "Homebrew")
	assert.NotContains(t, d.Reason, "container", "the container sentence is not this install's")

	unnamed := decidePkg(t, "v0.7.5", PolicyPatch, "", rel("v0.7.6"))
	assert.Equal(t, ActionInstruct, unnamed.Action)
	assert.Equal(t, "server.update.reason.package_unknown", unnamed.ReasonKey)
	assert.Empty(t, unnamed.ReasonVars)

	for _, m := range []PackageManager{ManagerHomebrew, ManagerWinget, ManagerSnap, ""} {
		for _, pol := range []Policy{PolicyOff, PolicyManual, PolicyPatch, PolicyMinor} {
			for _, c := range []struct {
				cur    string
				target Release
			}{
				{"v0.7.5", rel("v0.7.6")},                 // patch
				{"v1.2.0", rel("v1.3.0")},                 // minor, automatic on 1.x under PolicyMinor
				{"v0.7.6", rel("v0.8.0")},                 // 0.x minor
				{"v0.7.5", rel("v0.7.6", withMigrations)}, // would ask for a backup
				{"v0.9.0", rel("v1.0.0")},                 // major
			} {
				d := decidePkg(t, c.cur, pol, m, c.target)
				assert.Equal(t, ActionInstruct, d.Action, "%s %s %s→%s", m, pol, c.cur, c.target.Version)
			}
		}
	}
}

// The service end to end, against a manifest served locally: in package
// mode neither the periodic automatic path nor an explicit Apply fetches a
// single byte of the release, let alone replaces the binary.
func TestService_PackageInstallNeverReplacesItself(t *testing.T) {
	var downloads atomic.Int32
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/stable.json":
			_ = json.NewEncoder(w).Encode(Manifest{Releases: []Release{{
				Version: "v0.7.6", AutoOK: true,
				Assets: []Asset{{
					OS: runtime.GOOS, Arch: runtime.GOARCH,
					URL: srv.URL + "/filex.tar.gz",
					// Never the digest of what is served: even the control
					// below stops at the checksum, before anything on disk.
					SHA256: strings.Repeat("0", 64),
				}},
			}}})
		case "/filex.tar.gz":
			downloads.Add(1)
			_, _ = w.Write([]byte("not an archive"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	ctx := context.Background()
	newSvc := func(inst Install) *Service {
		return New(Config{
			Enabled:        true,
			Policy:         PolicyPatch,
			ManifestURL:    srv.URL + "/stable.json",
			CurrentVersion: "v0.7.5",
			Install:        inst,
		})
	}

	// Premise — the harness can see an attempt: a binary install under the
	// same policy takes the release by itself and reaches the download.
	bin := newSvc(Install{Mode: ModeBinary})
	d, err := bin.Check(ctx)
	require.NoError(t, err)
	require.Equal(t, ActionAuto, d.Action)
	bin.maybeAutoApply(ctx)
	require.Equal(t, int32(1), downloads.Load(), "the binary install fetched the release (and the checksum refused it)")

	for _, inst := range []Install{
		{Mode: ModePackage, Manager: ManagerHomebrew, Package: "filex", Cask: true},
		{Mode: ModePackage, Manager: ManagerWinget, Package: "BRFTech.filex"},
		{Mode: ModePackage, Manager: ManagerSnap, Package: "filex"},
		{Mode: ModePackage},
	} {
		downloads.Store(0)
		svc := newSvc(inst)
		assert.Equal(t, ModePackage, svc.Mode())
		assert.Equal(t, inst, svc.Install(), "Config.Install replaces detection")

		d, err := svc.Check(ctx)
		require.NoError(t, err)
		assert.Equal(t, "v0.7.6", d.Target.Version, "the check still learns about the release")
		assert.Equal(t, ActionInstruct, d.Action, "%+v", inst)

		svc.maybeAutoApply(ctx)
		err = svc.Apply(ctx, d.Target)
		assert.ErrorIs(t, err, ErrNotSelfApplicable, "%+v", inst)
		var pm *PackageManagedError
		assert.ErrorAs(t, err, &pm)
		assert.Zero(t, downloads.Load(), "%+v: nothing was downloaded", inst)
		assert.Empty(t, svc.State().LastApplied)
		assert.False(t, svc.RestartRequired())
	}
}
