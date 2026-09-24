package handlers_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/pathkey"
)

// A PIN-protected link says nothing about what is behind it until the PIN
// is right. The bytes were always behind the gate; the NAME was not, and a
// file name over a PIN box tells a stranger what they are knocking on.
func TestPublicShare_NameStaysBehindThePin(t *testing.T) {
	f := newAppFixture(t, nil)
	f.writeFile(t, "gizli/maas-bordrosu.pdf", "%PDF-1.7 payroll")
	// A share hangs off a catalogue row, and the harness writes straight to
	// disk, so the row is made here.
	_, err := f.store.CreateNode(context.Background(), &model.Node{
		StorageID: f.st.ID, Name: "maas-bordrosu.pdf", Path: "gizli/maas-bordrosu.pdf",
		PathHash: pathkey.Hash(f.st.ID, "/gizli/maas-bordrosu.pdf"), Type: model.NodeTypeFile, Size: 16,
	})
	require.NoError(t, err)
	token, pin := f.shareWithPin(t, "main://gizli/maas-bordrosu.pdf")

	status, raw := doReq(t, freshClient(t), http.MethodGet, f.srv.URL+"/api/public/s/"+token, nil)
	require.Equal(t, http.StatusOK, status, string(raw))
	body := string(raw)
	assert.NotContains(t, body, "maas-bordrosu", "the name is behind the gate")
	assert.NotContains(t, strings.ToLower(body), "payroll")
	var locked struct {
		NeedsPin bool `json:"needs_pin"`
		Unlocked bool `json:"unlocked"`
		Node     any  `json:"node"`
	}
	require.NoError(t, json.Unmarshal(raw, &locked))
	assert.True(t, locked.NeedsPin)
	assert.False(t, locked.Unlocked)
	assert.Nil(t, locked.Node)

	client := freshClient(t)
	status, raw = doReq(t, client, http.MethodPost, f.srv.URL+"/api/public/s/"+token+"/pin", map[string]any{"pin": pin})
	require.Equal(t, http.StatusOK, status, string(raw))
	assert.Contains(t, string(raw), "maas-bordrosu", "once the PIN is right the name is the point")
}

// shareWithPin makes a PIN-protected share of one file and returns its
// token and the PIN the server generated.
func (f *appFixture) shareWithPin(t *testing.T, path string) (token, pin string) {
	t.Helper()
	status, raw := doReq(t, f.admin, http.MethodPost, f.srv.URL+"/api/files/share",
		map[string]any{"path": path, "pin": "4917"})
	require.Equal(t, http.StatusOK, status, string(raw))
	var out struct {
		Token string `json:"token"`
		URL   string `json:"url"`
		PIN   string `json:"password_pin"`
	}
	require.NoError(t, json.Unmarshal(raw, &out))
	if out.PIN == "" {
		out.PIN = "4917" // the one we asked for
	}
	tok := out.Token
	if tok == "" && out.URL != "" {
		parts := strings.Split(strings.TrimRight(out.URL, "/"), "/")
		tok = parts[len(parts)-1]
	}
	require.NotEmpty(t, tok)
	return tok, out.PIN
}
