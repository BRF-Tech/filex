package handlers

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/acl"
	"github.com/brf-tech/filex/backend/internal/apierr"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/wasmplugin"
)

// ── The encrypted folder's door (filex 0.56.0, task #189; app_ui_e2e.go) ─
//
// All red before 0.56: e2eDoor, encryptedBody and the sentences did not
// exist, and every app door refused every file of an encrypted folder.

// e2eSettingsStore is a db.Store that answers settings only - all the door
// reads of the store (the administrator's switch).
type e2eSettingsStore struct {
	db.Store
	m map[string]string
}

func (s e2eSettingsStore) GetSetting(_ context.Context, k string) (string, error) { return s.m[k], nil }

// e2eVaults says "a vault" for the folders it names.
type e2eVaults map[string]bool

func (v e2eVaults) VaultRoot(_ context.Context, _ int64, rel string) (string, bool) {
	for root := range v {
		if rel == root || strings.HasPrefix(rel, root+"/") {
			return root, true
		}
	}
	return "", false
}

func e2eDoorFixture(settings map[string]string, vaults e2eVaults) *AppPlugins {
	r := acl.New(nil)
	if vaults != nil {
		r.AttachVaults(vaults)
	}
	return &AppPlugins{Store: e2eSettingsStore{m: settings}, ACL: r}
}

func e2eApp(perms ...wasmplugin.Permission) *wasmplugin.Installed {
	return &wasmplugin.Installed{Grants: wasmplugin.NewGrants(perms)}
}

// askDoor runs the door and returns whether it let the request through and,
// when it did not, the refusal's status, code and message.
func askDoor(t *testing.T, h *AppPlugins, p *wasmplugin.Installed, rel, kind string, writing bool) (bool, int, string, string) {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/x?e2e=1", nil)
	req.Header.Set("Accept-Language", "en")
	ok := h.e2eDoor(rec, req, p, 7, rel, kind, writing)
	if ok {
		assert.Equal(t, 0, rec.Body.Len(), "a door that lets the request through writes nothing")
		return true, 0, "", ""
	}
	var body map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body), rec.Body.String())
	code, _ := body["error"].(string)
	msg, _ := body["message"].(string)
	return false, rec.Code, code, msg
}

func TestE2EDoor_AGrantedAppIsLetThroughToAFileOfAnEncryptedFolder(t *testing.T) {
	h := e2eDoorFixture(nil, nil)
	app := e2eApp(wasmplugin.PermFilesRead, wasmplugin.PermFilesWrite, wasmplugin.PermFilesE2EPlaintext)
	for _, writing := range []bool{false, true} {
		ok, _, _, _ := askDoor(t, h, app, "kasa/q3.docx", encryptedInFolder, writing)
		assert.True(t, ok, "writing=%v", writing)
	}
}

func TestE2EDoor_RefusesWhatTheServerCannotVouchFor(t *testing.T) {
	granted := e2eApp(wasmplugin.PermFilesRead, wasmplugin.PermFilesWrite, wasmplugin.PermFilesE2EPlaintext)

	// No grant: the administrator never said yes to this app.
	ok, status, code, msg := askDoor(t, e2eDoorFixture(nil, nil), e2eApp(wasmplugin.PermFilesRead), "kasa/q3.docx", encryptedInFolder, false)
	assert.False(t, ok)
	assert.Equal(t, http.StatusForbidden, status)
	assert.Equal(t, "not_granted", code)
	assert.Contains(t, msg, "files:e2e-plaintext")

	// The administrator's switch, off for every app.
	ok, status, code, msg = askDoor(t, e2eDoorFixture(map[string]string{"apps.e2e_plaintext": "false"}, nil), granted, "kasa/q3.docx", encryptedInFolder, false)
	assert.False(t, ok)
	assert.Equal(t, http.StatusForbidden, status)
	assert.Equal(t, "encrypted", code)
	assert.Equal(t, apierr.Text("en", "app_e2e_off", nil), msg)

	// A vault: 0.56 opens its files in no app.
	ok, status, code, msg = askDoor(t, e2eDoorFixture(nil, e2eVaults{"Kasa": true}), granted, "Kasa/q3.docx", encryptedInFolder, true)
	assert.False(t, ok)
	assert.Equal(t, http.StatusForbidden, status)
	assert.Equal(t, "encrypted", code)
	assert.Contains(t, msg, "Kasa/q3.docx")

	// A single encrypted file carries its own password: the 0.55 refusal.
	ok, status, code, _ = askDoor(t, e2eDoorFixture(nil, nil), granted, "q3.docx.fxe", encryptedSingle, false)
	assert.False(t, ok)
	assert.Equal(t, http.StatusForbidden, status)
	assert.Equal(t, "encrypted", code)

	// Not (or no longer) in an encrypted folder: not what the explorer opened.
	ok, status, code, msg = askDoor(t, e2eDoorFixture(nil, nil), granted, "acik/q3.docx", "", true)
	assert.False(t, ok)
	assert.Equal(t, http.StatusConflict, status)
	assert.Equal(t, "changed", code)
	assert.Contains(t, msg, "acik/q3.docx")
}

func TestEncryptedBody_OnlyWhatStartsLikeAnEncryptedFile(t *testing.T) {
	for _, v := range []byte{1, 2} {
		in := "filexe2e" + string([]byte{v}) + "the rest of the ciphertext"
		r, ok := encryptedBody(strings.NewReader(in))
		require.True(t, ok, "version %d", v)
		got, err := io.ReadAll(r)
		require.NoError(t, err)
		assert.Equal(t, in, string(got), "the bytes it read to decide are put back")
	}
	for name, in := range map[string]string{
		"a docx in the clear":        "PK\x03\x04word/document.xml",
		"plain text":                 "Merhaba dünya",
		"empty":                      "",
		"the magic alone":            "filexe2e",
		"a version this filex lacks": "filexe2e\x03xxxx",
		"a single encrypted file":    "filexfxe\x01header",
	} {
		_, ok := encryptedBody(strings.NewReader(in))
		assert.False(t, ok, name)
	}
}

// The door's sentences, in both shipped languages, with the file named.
func TestE2EDoor_TheSentencesAreSaidInBothLanguages(t *testing.T) {
	p := apierr.Params{"name": "kasa/q3.docx"}
	for _, said := range []string{"app_e2e_off", "app_e2e_vault", "app_e2e_not_encrypted", "app_save_changed"} {
		require.True(t, apierr.Known(said), said)
		en, tr := apierr.Text("en", said, p), apierr.Text("tr", said, p)
		assert.NotEmpty(t, en, said)
		assert.NotEmpty(t, tr, said)
		assert.NotEqual(t, en, tr, "%s is not translated", said)
		assert.NotContains(t, en, "{", "%s left a placeholder open: %s", said, en)
		assert.NotContains(t, tr, "{", "%s left a placeholder open: %s", said, tr)
		if said != "app_e2e_off" {
			assert.Contains(t, en, "kasa/q3.docx", said)
			assert.Contains(t, tr, "kasa/q3.docx", said)
		}
	}
}
