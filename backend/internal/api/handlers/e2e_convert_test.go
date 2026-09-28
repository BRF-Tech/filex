package handlers_test

// Encrypting a folder that already exists, in place (e2e_convert.go): a
// conversion write keeps no version of the plaintext it replaces — and
// nothing else gets to skip the version — and POST /api/files/e2e/cleanup
// removes what filex still holds from before.

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

const (
	kfConvPending = `{"v":3,"req":["conv"],"conv":{"pending":true},"salt":"c2FsdA==","iter":600000,"verify":"dmVy","fmk":"wrapped","fmk_pw":"cHc="}`
	kfConvDone    = `{"v":2,"salt":"c2FsdA==","iter":600000,"verify":"dmVy","fmk":"wrapped","fmk_pw":"cHc="}`
)

func cipherBody(s string) []byte { return append([]byte("filexe2e\x01"), []byte(s)...) }

// stagedAt is uploadStaged into a folder, optionally as a conversion write.
func (f *stagedFixture) stagedAt(t *testing.T, dir, name string, body []byte, convert bool) {
	t.Helper()
	total := int64(len(body))
	code, begun := f.begin(t, map[string]any{"path": "main://" + dir, "name": name, "size": total})
	require.Equal(t, http.StatusOK, code, "%v", begun)
	id := begun["id"].(string)
	code, put := f.putChunk(t, id, 0, total, total, body)
	require.Equal(t, http.StatusOK, code, "%v", put)
	url := f.srv.URL + "/api/files/upload/" + id + "/commit"
	if convert {
		url += "?e2e_convert=1"
	}
	resp, err := f.client.Post(url, "application/json", nil)
	require.NoError(t, err)
	defer resp.Body.Close()
	out := map[string]any{}
	_ = json.NewDecoder(resp.Body).Decode(&out)
	require.Equal(t, http.StatusAccepted, resp.StatusCode, "%v", out)
	require.Equal(t, "ok", f.waitForOp(t, num(out["op_id"])))
}

func TestE2EConvert_AConversionWriteKeepsNoPlaintextVersion(t *testing.T) {
	f := newStagedFixtureWith(t, withVersions)
	for _, n := range []string{"a.txt", "b.txt", "c.txt", "d.txt"} {
		f.stagedAt(t, "Kasa", n, []byte("plaintext of "+n), false)
	}
	f.stagedAt(t, "Kasa", ".filex-e2e.json", []byte(kfConvPending), false)

	// The conversion write: plaintext replaced by ciphertext, flag set.
	f.stagedAt(t, "Kasa", "a.txt", cipherBody("a"), true)
	require.Empty(t, versionsOf(t, f, "Kasa/a.txt"), "the plaintext is not kept as a version")

	// Without the flag: an ordinary overwrite, which keeps its version.
	f.stagedAt(t, "Kasa", "b.txt", cipherBody("b"), false)
	require.Len(t, versionsOf(t, f, "Kasa/b.txt"), 1)

	// With the flag but plaintext going in: not a conversion write.
	f.stagedAt(t, "Kasa", "c.txt", []byte("still plaintext"), true)
	require.Len(t, versionsOf(t, f, "Kasa/c.txt"), 1)

	// With the flag, once the key file no longer says a conversion is under way.
	f.stagedAt(t, "Kasa", ".filex-e2e.json", []byte(kfConvDone), false)
	f.stagedAt(t, "Kasa", "d.txt", cipherBody("d"), true)
	require.Len(t, versionsOf(t, f, "Kasa/d.txt"), 1, "the flag alone never skips a version")
}

func TestE2EConvert_TheFlagDoesNothingOutsideAnEncryptedFolder(t *testing.T) {
	f := newStagedFixtureWith(t, withVersions)
	f.stagedAt(t, "Acik", "x.txt", []byte("plaintext"), false)
	f.stagedAt(t, "Acik", "x.txt", cipherBody("x"), true)
	require.Len(t, versionsOf(t, f, "Acik/x.txt"), 1)
}

func TestE2ECleanup_DropsVersionsWhenAsked(t *testing.T) {
	f := newStagedFixtureWith(t, withVersions)
	f.stagedAt(t, "Kasa", "a.txt", []byte("one"), false)
	f.stagedAt(t, "Kasa", "a.txt", []byte("two"), false)
	f.stagedAt(t, "Kasa", ".filex-e2e.json", []byte(kfConvDone), false)
	require.Len(t, versionsOf(t, f, "Kasa/a.txt"), 1)

	post := func(body map[string]any) (int, map[string]any) {
		buf, _ := json.Marshal(body)
		resp, err := f.client.Post(f.srv.URL+"/api/files/e2e/cleanup", "application/json", bytes.NewReader(buf))
		require.NoError(t, err)
		defer resp.Body.Close()
		out := map[string]any{}
		_ = json.NewDecoder(resp.Body).Decode(&out)
		return resp.StatusCode, out
	}

	code, out := post(map[string]any{"path": "main://Kasa", "versions": false})
	require.Equal(t, http.StatusOK, code, "%v", out)
	require.Len(t, versionsOf(t, f, "Kasa/a.txt"), 1, "versions stay unless asked")

	code, out = post(map[string]any{"path": "main://Kasa", "versions": true})
	require.Equal(t, http.StatusOK, code, "%v", out)
	require.EqualValues(t, 1, out["versions_deleted"])
	require.Empty(t, versionsOf(t, f, "Kasa/a.txt"))

	rows, _, err := f.store.ListAuditFiltered(context.Background(), nil, "e2e.folder_cleanup", nil, nil, 10, 0)
	require.NoError(t, err)
	require.Len(t, rows, 2)

	code, _ = post(map[string]any{"path": "main://", "versions": true})
	require.Equal(t, http.StatusBadRequest, code, "not an encrypted folder")
}
