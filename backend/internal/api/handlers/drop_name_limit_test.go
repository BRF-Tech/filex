package handlers_test

// #210 (B11): the uploader's name limit is the server's, said up front and
// held to - not cut short without a word. The JavaScript page's box allowed
// 60 characters while the server silently kept 40 of them, so the owner read
// a different name from the one the visitor typed.

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/share"
)

func TestDrop_ANameOverTheLimitIsRefusedNotCut(t *testing.T) {
	r, svc, store, st, root := newDropFixture(t)
	folder := mkdirNode(t, store, st, root, "inbox")
	sh, err := svc.Create(context.Background(), share.CreateOpts{NodeID: folder.ID, Kind: model.ShareKindDrop})
	require.NoError(t, err)

	long := strings.Repeat("Ş", 41)
	rec := doDropUpload(t, r, "/d/"+sh.Token, "", map[string]string{"uploader_name": long}, []fpart{{"a.txt", "a"}})
	require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
	var out map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out))
	assert.Equal(t, "name_too_long", out["error"])
	assert.EqualValues(t, 40, out["name_max"])
	assert.Contains(t, out["message"], "40", "the sentence says the limit")
	assert.Empty(t, findUnder(root, "inbox", "a.txt"), "nothing is saved under a name the visitor did not give")

	// Exactly the limit (in characters, not bytes) is kept whole.
	exact := strings.Repeat("Ş", 40)
	rec = doDropUpload(t, r, "/d/"+sh.Token, "", map[string]string{"uploader_name": exact}, []fpart{{"b.txt", "b"}})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	got := findUnder(root, "inbox", "b.txt")
	require.NotEmpty(t, got)
	assert.Contains(t, got, exact, "the submission folder carries the whole name")
}

func TestDrop_TheLimitsSayTheNameMaxAndThePinMax(t *testing.T) {
	r, svc, store, st, root := newPublicFixture(t)
	folder := mkdirNode(t, store, st, root, "gelen")
	ds := `{"ask_name":true}`
	sh, err := svc.Create(context.Background(), share.CreateOpts{NodeID: folder.ID, Kind: model.ShareKindDrop, DropSettings: &ds, PIN: "4321"})
	require.NoError(t, err)

	rec := doGet(t, r, "/api/public/d/"+sh.Token)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var out struct {
		PinMax int `json:"pin_max"`
		Limits struct {
			NameMax int `json:"name_max"`
		} `json:"limits"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out))
	assert.Equal(t, 40, out.Limits.NameMax, "the JavaScript page's box stops where the server does")
	assert.Equal(t, share.PINMaxLen, out.PinMax)

	// The no-JavaScript form says the same number (a link without a PIN, so
	// the page is the uploader itself).
	open, err := svc.Create(context.Background(), share.CreateOpts{NodeID: folder.ID, Kind: model.ShareKindDrop, DropSettings: &ds})
	require.NoError(t, err)
	page := getPage(t, r, "/d/"+open.Token, "en")
	require.Equal(t, http.StatusOK, page.Code, page.Body.String())
	assert.Contains(t, page.Body.String(), `id="uploaderName" maxlength="40"`)
}
