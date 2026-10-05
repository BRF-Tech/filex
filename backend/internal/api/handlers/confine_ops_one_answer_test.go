package handlers_test

// The operations queue tells a `root:` token nothing about what lies outside
// its folder, whatever shape its request has (found in the GHSA-8gvc-6w52-6c7j
// review, 2026-10-04).
//
// confine.Middleware refuses a path outside the root in a body labelled JSON
// with one answer. The same body sent as text/plain, or with no Content-Type,
// reached the queue's handlers as written, and they answered according to what
// they found first: an unknown storage 400, a read-only one 403 READ_ONLY with
// its name, a `..` 400 BAD_PATH, and the root's own refusal with the path
// appended. So the answer told a confined token which storages exist outside
// its folder and which of them are read-only. Every request that names a path
// outside the root now gets the middleware's own answer, byte for byte, in
// every shape; inside the root the queue works as it did.

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The three shapes a body arrives in.
var opsBodyShapes = []string{"application/json", "text/plain", ""}

// opsOutside is one request naming a path outside main://kutu.
type opsOutside struct{ label, route, body string }

func TestConfineOps_OutsideTheRootIsOneAnswerInEveryShape(t *testing.T) {
	f, tok := confinedFix(t)
	ctx := context.Background()
	// yan becomes a read-only storage outside the token's root.
	yan, err := f.Store.GetStorage(ctx, f.Yan.ID)
	require.NoError(t, err)
	yan.ReadOnly = true
	require.NoError(t, f.Store.UpdateStorage(ctx, yan))
	code, raw := confRaw(t, f.URL, tok, http.MethodPost, "/api/files/manager?action=newfolder", "application/json",
		[]byte(`{"path":"main://kutu","name":"hedef"}`))
	require.Equal(t, http.StatusOK, code, raw)

	cases := []opsOutside{
		// The per-verb doors (the explorer's paste and delete).
		{"delete, a file outside", "/api/files/delete", `{"source":["main://disari/gizli.txt"]}`},
		{"delete, a file outside that is not there", "/api/files/delete", `{"source":["main://disari/yok.txt"]}`},
		{"delete, a bare path outside", "/api/files/delete", `{"source":["disari/gizli.txt"]}`},
		{"delete, a storage that does not exist", "/api/files/delete", `{"source":["yokdepo://x.txt"]}`},
		{"delete, a read-only storage", "/api/files/delete", `{"source":["yan://x.txt"]}`},
		{"copy into a folder outside", "/api/files/copy", `{"source":["main://kutu/ic.txt"],"target":"main://disari"}`},
		{"copy into a folder outside that is not there", "/api/files/copy", `{"source":["main://kutu/ic.txt"],"target":"main://yok"}`},
		{"copy into a read-only storage", "/api/files/copy", `{"source":["main://kutu/ic.txt"],"target":"yan://"}`},
		{"copy into a storage that does not exist", "/api/files/copy", `{"source":["main://kutu/ic.txt"],"target":"yokdepo://x"}`},
		{"move in from outside", "/api/files/move", `{"source":["main://disari/gizli.txt"],"target":"main://kutu/hedef"}`},
		{"move with a source folder outside", "/api/files/move", `{"source":["main://kutu/ic.txt"],"target":"main://kutu/hedef","sourceDir":"main://disari"}`},
		// POST /api/files/ops: storage-relative paths beside a storage_id.
		{"ops delete outside", "/api/files/ops", fmt.Sprintf(`{"kind":"delete","storage_id":%d,"sources":["disari/gizli.txt"]}`, f.Main.ID)},
		{"ops delete climbing out", "/api/files/ops", fmt.Sprintf(`{"kind":"delete","storage_id":%d,"sources":["../disari/gizli.txt"]}`, f.Main.ID)},
		{"ops copy into a folder outside", "/api/files/ops", fmt.Sprintf(`{"kind":"copy","storage_id":%d,"sources":["kutu/ic.txt"],"dest":"disari/"}`, f.Main.ID)},
		{"ops copy, a dest naming another storage", "/api/files/ops", fmt.Sprintf(`{"kind":"copy","storage_id":%d,"sources":["kutu/ic.txt"],"dest":"baska/alt://kutu/"}`, f.Main.ID)},
		{"ops on a read-only storage", "/api/files/ops", fmt.Sprintf(`{"kind":"delete","storage_id":%d,"sources":["kutu/ic.txt"]}`, f.Yan.ID)},
		{"ops on a storage id that does not exist", "/api/files/ops", fmt.Sprintf(`{"kind":"delete","storage_id":%d,"sources":["kutu/ic.txt"]}`, f.Yan.ID+1000)},
	}

	// The answer: what the confinement middleware says to a JSON body.
	code, want := confRaw(t, f.URL, tok, http.MethodPost, cases[0].route, "application/json", []byte(cases[0].body))
	require.Equal(t, http.StatusForbidden, code, want)
	for _, c := range cases {
		for _, ct := range opsBodyShapes {
			code, body := confRaw(t, f.URL, tok, http.MethodPost, c.route, ct, []byte(c.body))
			assert.Equal(t, http.StatusForbidden, code, "%s (%q): %s", c.label, ct, body)
			assert.Equal(t, want, body, "%s (%q): outside the root, one answer", c.label, ct)
		}
	}
	settle()
	assert.True(t, confExists(f.RootMain, "disari/gizli.txt"), "the file outside stays")
	assert.False(t, confExists(f.RootMain, "kutu/hedef/gizli.txt"), "nothing is brought in")
	assert.False(t, confExists(f.RootMain, "disari/ic.txt"), "nothing is put outside")

	// Inside the root, the same text/plain bodies are queued and carried out.
	code, raw = confRaw(t, f.URL, tok, http.MethodPost, "/api/files/copy", "text/plain",
		[]byte(`{"source":["main://kutu/ic.txt"],"target":"main://kutu/hedef"}`))
	require.Equal(t, http.StatusAccepted, code, raw)
	cdEventually(t, func() bool { return confExists(f.RootMain, "kutu/hedef/ic.txt") }, "a per-verb copy inside the root must still be carried out")
	code, raw = confRaw(t, f.URL, tok, http.MethodPost, "/api/files/ops", "",
		[]byte(fmt.Sprintf(`{"kind":"copy","storage_id":%d,"sources":["kutu/ic.txt"],"dest":"kutu/hedef/kopya.txt"}`, f.Main.ID)))
	require.Equal(t, http.StatusAccepted, code, raw)
	cdEventually(t, func() bool { return confExists(f.RootMain, "kutu/hedef/kopya.txt") }, "a queued copy inside the root must still be carried out")
}
