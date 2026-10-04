package handlers_test

// Who may encrypt (internal/e2epolicy) holds for an app's output too.
//
// An interface's "save as" and the result of a job both land through
// AppPlugins.CommitSibling, and the router builds ONE AppPlugins: it is the
// handler behind the app routes and, through NewAppPlugins, the registry's
// output sink. The door matrix builds an AppPlugins of its own to reach
// CommitSibling, so it cannot see whether the router hands ITS AppPlugins the
// rule (routes.go: apH.E2EPolicy). This runs the real router.
//
// The route walked is the interface's "save as", because the app's own module
// cannot be steered from here: the echo module names what it writes itself
// (…-upper.txt), and the manifest's output.name pattern is consulted only
// where a module leaves the name to the host — changing `upper`'s pattern to
// `{stem}.fxe` leaves its output at note-upper.txt. A job's result and a
// "save as" reach the rule through the same line of CommitSibling, so the
// wiring is the same.

import (
	"bytes"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/pathkey"
	"github.com/brf-tech/filex/backend/internal/wasmplugin"
)

func TestE2EPolicy_AnAppsSaveAsIsHeldToTheRuleThroughTheRouter(t *testing.T) {
	f := newAppFixture(t, nil)
	// An app whose interface opens .json — a key file's own extension — so that
	// saving a key file "as" is a save this view makes and reaches the rule
	// (viewSaves holds a save to the kinds its view opens).
	manifest := strings.Replace(uiOnlyManifest, `"ext":["sketch"]`, `"ext":["json"]`, 1)
	require.Contains(t, manifest, `"ext":["json"]`)
	_, _, err := f.reg.Install(t.Context(), &wasmplugin.InstallInput{
		Manifest: []byte(manifest), UI: bytes.NewReader(uiBundleZip(t)),
		Granted: []string{"files:read", "files:write", "ui", "ui-viewer:.json"},
	})
	require.NoError(t, err)
	f.writeFile(t, "Acik/notlar.txt", "x")
	saveAs := func(name string) (int, string) {
		return f.uiSave(t, "sketch", "editor", "dir="+url.QueryEscape("main://Acik")+"&name="+url.QueryEscape(name), "{}")
	}

	// Policy off: the interface's save is refused as the rule's own 403 — not
	// as a failed save (500 save_failed) — and nothing is written or catalogued.
	encryptionOff(t, f.store)
	code, body := saveAs(e2eKeyFile)
	assertE2ERefused(t, "an app's save as of a key file", code, body)
	assert.NoFileExists(t, filepath.Join(f.root, "Acik", e2eKeyFile))
	node, err := f.store.GetNodeByPath(t.Context(), f.st.ID, pathkey.Hash(f.st.ID, "/Acik/"+e2eKeyFile))
	assert.True(t, err != nil || node == nil, "a refused save has a catalogue row")

	// The route itself is not what is refused: an ordinary name still saves.
	code, body = saveAs("veri.json")
	require.Equal(t, http.StatusCreated, code, "an ordinary file, policy off: %s", body)
	assert.Equal(t, "{}", f.readFile(t, "Acik/veri.json"))

	// Policy permitted (the default): the same save lands. This is also what
	// says the person the save is judged for was found — with nobody to judge,
	// the refusal above would read `permission` and this would be refused too.
	require.NoError(t, f.store.UpsertSetting(t.Context(), model.SettingE2EPolicy, model.E2EPolicyPermitted))
	code, body = saveAs(e2eKeyFile)
	require.Equal(t, http.StatusCreated, code, "a key file, policy permitted: %s", body)
	assert.Equal(t, "{}", f.readFile(t, "Acik/"+e2eKeyFile))
}
