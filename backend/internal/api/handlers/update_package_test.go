package handlers_test

// The admin Updates page on an install a package manager owns (Homebrew,
// winget, Snap): the status names the manager and its command, the
// instructions are that command — never `filex self-update` — and "Upgrade
// now" is refused the way a container's is.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/api/handlers"
	"github.com/brf-tech/filex/backend/internal/srvtext"
	"github.com/brf-tech/filex/backend/internal/update"
)

// manifestServer serves one release list; no asset is ever downloadable.
func manifestServer(t *testing.T, releases ...update.Release) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/stable.json" {
			t.Errorf("unexpected request %s: a package install must not fetch a release", r.URL.Path)
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(update.Manifest{Releases: releases})
	}))
	t.Cleanup(srv.Close)
	return srv.URL + "/stable.json"
}

type pkgStatus struct {
	Mode               string   `json:"mode"`
	CanSelfApply       bool     `json:"can_self_apply"`
	Action             string   `json:"action"`
	Reason             string   `json:"reason"`
	Instructions       []string `json:"instructions"`
	PackageManager     string   `json:"package_manager"`
	PackageManagerName string   `json:"package_manager_name"`
	UpgradeCommand     string   `json:"upgrade_command"`
}

func checkedStatus(t *testing.T, inst update.Install, lang string, releases ...update.Release) (*handlers.Update, pkgStatus) {
	t.Helper()
	svc := update.New(update.Config{
		Enabled:        true,
		Policy:         update.PolicyPatch,
		ManifestURL:    manifestServer(t, releases...),
		CurrentVersion: "v0.7.5",
		Install:        inst,
	})
	h := handlers.NewUpdate(svc)
	req := httptest.NewRequest(http.MethodPost, "/api/admin/update/check", nil)
	if lang != "" {
		req.Header.Set("Accept-Language", lang)
	}
	rec := httptest.NewRecorder()
	h.Check(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	var st pkgStatus
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &st))
	return h, st
}

var brewCask = update.Install{Mode: update.ModePackage, Manager: update.ManagerHomebrew, Package: "filex", Cask: true}

func TestUpdateStatus_PackageInstallNamesItsManager(t *testing.T) {
	_, st := checkedStatus(t, brewCask, "", update.Release{Version: "v0.7.6", AutoOK: true})

	assert.Equal(t, "package", st.Mode)
	assert.False(t, st.CanSelfApply)
	assert.Equal(t, "homebrew", st.PackageManager)
	assert.Equal(t, "Homebrew", st.PackageManagerName)
	assert.Equal(t, "brew upgrade --cask filex", st.UpgradeCommand)
	assert.Equal(t, "instruct", st.Action, "patch policy, but a package manager owns the binary")
	assert.Equal(t, srvtext.Text("en", "server.update.reason.package", srvtext.Vars{"manager": "Homebrew"}), st.Reason)

	assert.Contains(t, st.Instructions, "brew upgrade --cask filex")
	for _, line := range st.Instructions {
		assert.NotContains(t, line, "self-update", "the one command that must not be offered")
		assert.NotContains(t, line, "docker", "not a container")
	}
}

func TestUpdateStatus_PackageReasonIsInTheReadersLanguage(t *testing.T) {
	_, st := checkedStatus(t, brewCask, "tr-TR,tr;q=0.9", update.Release{Version: "v0.7.6", AutoOK: true})
	want := srvtext.Text("tr", "server.update.reason.package", srvtext.Vars{"manager": "Homebrew"})
	assert.Equal(t, want, st.Reason)
	assert.True(t, strings.HasPrefix(st.Reason, "Homebrew ile kuruldu"), st.Reason)
}

func TestUpdateStatus_PackageInstructionsPerManager(t *testing.T) {
	patch := update.Release{Version: "v0.7.6", AutoOK: true}

	// winget cannot replace an executable that is running.
	_, st := checkedStatus(t, update.Install{Mode: update.ModePackage, Manager: update.ManagerWinget, Package: "BRFTech.filex"}, "", patch)
	require.GreaterOrEqual(t, len(st.Instructions), 2)
	i := indexOf(st.Instructions, "winget upgrade BRFTech.filex")
	require.Greater(t, i, 0, "the command, after the line that says to stop filex: %q", st.Instructions)
	assert.Contains(t, st.Instructions[i-1], "stop filex first")

	_, st = checkedStatus(t, update.Install{Mode: update.ModePackage, Manager: update.ManagerSnap, Package: "filex_beta"}, "", patch)
	assert.Contains(t, st.Instructions, "snap refresh filex_beta")

	// A schema change: the snapshot a self-upgrade would take does not happen
	// under a package manager, so the backup is asked for — first.
	_, st = checkedStatus(t, brewCask, "", update.Release{Version: "v0.7.6", AutoOK: true, Migrations: true})
	require.NotEmpty(t, st.Instructions)
	assert.Contains(t, st.Instructions[0], "back up")
	assert.Contains(t, st.Instructions, "brew upgrade --cask filex")

	// FILEX_INSTALL_MODE=package with nothing detectable (a .deb, an .rpm):
	// no manager to name, no command to invent.
	_, st = checkedStatus(t, update.Install{Mode: update.ModePackage}, "", patch)
	assert.Equal(t, "package", st.Mode)
	assert.Empty(t, st.PackageManager)
	assert.Empty(t, st.UpgradeCommand)
	assert.Equal(t, srvtext.Text("en", "server.update.reason.package_unknown", nil), st.Reason)
	assert.Contains(t, strings.Join(st.Instructions, "\n"), "package manager that installed it")
	assert.NotContains(t, strings.Join(st.Instructions, "\n"), "self-update")
}

func TestUpdateApply_PackageInstallIs409WithTheCommand(t *testing.T) {
	h, _ := checkedStatus(t, brewCask, "", update.Release{Version: "v0.7.6", AutoOK: true})

	rec := httptest.NewRecorder()
	h.Apply(rec, httptest.NewRequest(http.MethodPost, "/api/admin/update/apply", nil))
	require.Equal(t, http.StatusConflict, rec.Code, "permanent, like the container case — not a 5xx to retry")

	var body struct {
		Error        string   `json:"error"`
		Instructions []string `json:"instructions"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Contains(t, body.Error, "brew upgrade --cask filex")
	assert.NotContains(t, body.Error, "container")
	assert.Contains(t, body.Instructions, "brew upgrade --cask filex")
}

func indexOf(lines []string, want string) int {
	for i, l := range lines {
		if l == want {
			return i
		}
	}
	return -1
}

// #72: the policy badge said the saved policy ("installs patches") on an
// install that cannot apply anything by itself. The status now carries what
// the install does (behavior) and why that is less than the policy
// (policy_limit), worked out on the server; the policy stays as it was saved.
func TestUpdateStatus_EffectivePolicyPerInstall(t *testing.T) {
	patch := update.Release{Version: "v0.7.6", AutoOK: true}
	type effective struct {
		Policy      string `json:"policy"`
		Behavior    string `json:"behavior"`
		PolicyLimit string `json:"policy_limit"`
	}
	read := func(t *testing.T, inst update.Install) effective {
		t.Helper()
		h, _ := checkedStatus(t, inst, "", patch)
		rec := httptest.NewRecorder()
		h.Status(rec, httptest.NewRequest(http.MethodGet, "/api/admin/update", nil))
		require.Equal(t, http.StatusOK, rec.Code)
		var e effective
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &e))
		return e
	}

	for _, tc := range []struct {
		name     string
		inst     update.Install
		behavior string
		limit    string
	}{
		{"homebrew", brewCask, "announce", "package"},
		{"package, manager unknown", update.Install{Mode: update.ModePackage}, "announce", "package"},
		{"container", update.Install{Mode: update.ModeDocker}, "announce", "container"},
		{"binary", update.Install{Mode: update.ModeBinary}, "patch", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := read(t, tc.inst)
			assert.Equal(t, "patch", e.Policy, "the saved policy is reported as saved")
			assert.Equal(t, tc.behavior, e.Behavior)
			assert.Equal(t, tc.limit, e.PolicyLimit)
		})
	}
}

// Checking switched off: whatever the policy says, nothing happens, and the
// status says so instead of repeating the policy.
func TestUpdateStatus_CheckingOffOverridesThePolicy(t *testing.T) {
	svc := update.New(update.Config{
		Enabled:        false,
		Policy:         update.PolicyPatch,
		CurrentVersion: "v0.7.6",
		Install:        update.Install{Mode: update.ModeBinary},
	})
	rec := httptest.NewRecorder()
	handlers.NewUpdate(svc).Status(rec, httptest.NewRequest(http.MethodGet, "/api/admin/update", nil))
	var body map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Equal(t, "patch", body["policy"])
	assert.Equal(t, "off", body["behavior"])
	assert.Equal(t, "disabled", body["policy_limit"])
}
