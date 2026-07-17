package handlers_test

// wiring:e2 — manager listing awareness of E2E-encrypted folders:
//   * `.filex-e2e.json` marker rows are hidden from listings,
//   * the encrypted dir row is badged `e2e:true` in its parent listing,
//   * listings inside the subtree carry `e2e` + `e2e_root` so the client
//     can raise the lock screen and fetch the marker.

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"net/url"
	"path"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/model"
)

func e2eListPathHash(storageID int64, p string) string {
	h := md5.New()
	_, _ = h.Write([]byte(strings.TrimRight(path.Clean("/"+p), "/")))
	_, _ = h.Write([]byte{'\x00'})
	_, _ = h.Write([]byte{byte(storageID), byte(storageID >> 8), byte(storageID >> 16), byte(storageID >> 24)})
	return hex.EncodeToString(h.Sum(nil))
}

type e2eListResp struct {
	Adapter string           `json:"adapter"`
	Dirname string           `json:"dirname"`
	E2e     *bool            `json:"e2e"`
	E2eRoot string           `json:"e2e_root"`
	Files   []map[string]any `json:"files"`
}

func seedE2eTree(t *testing.T) (fx *twoStorageFixture, storageID int64) {
	t.Helper()
	fx = newTwoStorageFixture(t)
	ctx := context.Background()
	st := fx.stA

	mkNode := func(name, p string, typ model.NodeType, parent *int64) *model.Node {
		n, err := fx.store.CreateNode(ctx, &model.Node{
			StorageID: st.ID,
			ParentID:  parent,
			Name:      name,
			Path:      p,
			PathHash:  e2eListPathHash(st.ID, p),
			Type:      typ,
			Size:      4,
			Mime:      "text/plain",
			Etag:      "e-" + name,
		})
		require.NoError(t, err)
		return n
	}

	kasa := mkNode("kasa", "/kasa", model.NodeTypeDirectory, nil)
	mkNode(".filex-e2e.json", "/kasa/.filex-e2e.json", model.NodeTypeFile, &kasa.ID)
	mkNode("gizli.txt", "/kasa/gizli.txt", model.NodeTypeFile, &kasa.ID)
	alt := mkNode("alt", "/kasa/alt", model.NodeTypeDirectory, &kasa.ID)
	mkNode("derin.txt", "/kasa/alt/derin.txt", model.NodeTypeFile, &alt.ID)
	mkNode("acik", "/acik", model.NodeTypeDirectory, nil)

	return fx, st.ID
}

func e2eIndex(t *testing.T, fx *twoStorageFixture, p string) e2eListResp {
	t.Helper()
	rec := callList(t, fx.mh, url.Values{"action": {"index"}, "path": {p}})
	require.Equal(t, 200, rec.Code, rec.Body.String())
	var resp e2eListResp
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	return resp
}

func TestE2eListing_ParentBadgesEncryptedDir(t *testing.T) {
	fx, _ := seedE2eTree(t)
	resp := e2eIndex(t, fx, "alpha://")

	var kasaRow, acikRow map[string]any
	for _, f := range resp.Files {
		switch f["basename"] {
		case "kasa":
			kasaRow = f
		case "acik":
			acikRow = f
		}
	}
	require.NotNil(t, kasaRow, "kasa dir row missing")
	assert.Equal(t, true, kasaRow["e2e"], "encrypted dir must carry e2e:true")
	require.NotNil(t, acikRow)
	_, has := acikRow["e2e"]
	assert.False(t, has, "plain dir must not carry e2e")
	// The parent listing itself is not inside the subtree.
	assert.Empty(t, resp.E2eRoot)
}

func TestE2eListing_MarkerHiddenAndRootFlagged(t *testing.T) {
	fx, _ := seedE2eTree(t)
	resp := e2eIndex(t, fx, "alpha://kasa")

	names := make([]string, 0, len(resp.Files))
	for _, f := range resp.Files {
		names = append(names, f["basename"].(string))
	}
	assert.NotContains(t, names, ".filex-e2e.json", "marker must be hidden from the listing")
	assert.Contains(t, names, "gizli.txt")

	require.NotNil(t, resp.E2e)
	assert.True(t, *resp.E2e, "listing at the encrypted root must set e2e:true")
	assert.Equal(t, "alpha://kasa", resp.E2eRoot)
}

func TestE2eListing_SubdirInheritsRoot(t *testing.T) {
	fx, _ := seedE2eTree(t)
	resp := e2eIndex(t, fx, "alpha://kasa/alt")

	require.NotNil(t, resp.E2e)
	assert.False(t, *resp.E2e, "subdir is inside the subtree but not the root")
	assert.Equal(t, "alpha://kasa", resp.E2eRoot)
}

func TestE2eListing_PlainDirUnflagged(t *testing.T) {
	fx, _ := seedE2eTree(t)
	resp := e2eIndex(t, fx, "alpha://acik")
	assert.Nil(t, resp.E2e)
	assert.Empty(t, resp.E2eRoot)
}
