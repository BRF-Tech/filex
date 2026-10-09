package handlers_test

// #189 P1b, completed in 0.55: every route that answers with FILE rows says
// how each one is end-to-end encrypted - `encrypted: "vault"` inside a vault,
// `"folder"` inside any other encrypted folder, nothing elsewhere and nothing
// on a folder row - by the folder listing's own rule (encryptedKind). The
// explorer hands the value to an app's interface as is (app SDK
// FileInfo.encrypted), so an app opened from Recent, Starred, a tag view, a
// search result or Shared with me is told the same as one opened from the
// folder. Before this only the folder listing stamped it, and each of the
// subtests below was red: the rows carried no `encrypted` at all.

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/pathkey"
	"github.com/brf-tech/filex/backend/internal/testutil"
)

// encRowsFix: a vault `Kasa` (made through the vault API, so its key file is
// in the catalogue and on the disk, as vaultlock.Finder reads it), an
// ordinary encrypted folder `Sifreli` (a non-vault key file), and one file in
// each plus one outside both - all tagged `damga`, starred and opened by the
// member.
type encRowsFix struct {
	*vaultFix
	ids map[string]int64
}

func newEncRowsFix(t *testing.T) *encRowsFix {
	t.Helper()
	ctx := context.Background()
	f := newVaultFix(t, true)
	f.create(t, f.member, "Kasa")
	require.NoError(t, os.MkdirAll(filepath.Join(f.root, "Sifreli"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(f.root, "Sifreli", ".filex-e2e.json"), []byte("{}"), 0o644))

	// Each row under its folder's row, so the id-based listing finds it too.
	mk := func(rel string, typ model.NodeType) *model.Node {
		var parent *int64
		if dir := filepath.ToSlash(filepath.Dir(rel)); dir != "." {
			p, err := f.store.GetNodeByPath(ctx, f.st.ID, pathkey.Hash(f.st.ID, "/"+dir))
			require.NoError(t, err, dir)
			parent = &p.ID
		}
		n, err := f.store.CreateNode(ctx, &model.Node{
			StorageID: f.st.ID,
			ParentID:  parent,
			Name:      filepath.Base(rel),
			Path:      "/" + rel,
			PathHash:  pathkey.Hash(f.st.ID, "/"+rel),
			Type:      typ,
			Size:      5,
			Mime:      "application/octet-stream",
			Etag:      "e-" + rel,
			SeenAt:    time.Now(),
		})
		require.NoError(t, err)
		return n
	}
	sifreli := mk("Sifreli", model.NodeTypeDirectory)
	mk("Sifreli/.filex-e2e.json", model.NodeTypeFile)
	ids := map[string]int64{
		"Sifreli":    sifreli.ID,
		"ek.bin":     mk("Kasa/ek.bin", model.NodeTypeFile).ID,
		"rapor.docx": mk("Sifreli/rapor.docx", model.NodeTypeFile).ID,
		"acik.txt":   mk("acik.txt", model.NodeTypeFile).ID,
	}

	opened := time.Now().Unix()
	for _, id := range ids {
		require.NoError(t, f.store.SetUserNodeMeta(ctx, f.memberID, id, "starred", "1"))
		require.NoError(t, f.store.SetUserNodeMeta(ctx, f.memberID, id, "last_opened", strconv.FormatInt(opened, 10)))
		testutil.TagNodePersonal(t, f.store, id, f.memberID, "damga")
	}
	return &encRowsFix{vaultFix: f, ids: ids}
}

// wantEncrypted asserts the three file rows and the folder row of one answer,
// keyed by name.
func wantEncrypted(t *testing.T, where string, rows map[string]map[string]any, withFolder bool) {
	t.Helper()
	for _, name := range []string{"ek.bin", "rapor.docx", "acik.txt"} {
		require.Contains(t, rows, name, "%s: row %s missing (%v)", where, name, rows)
	}
	require.Equal(t, "vault", rows["ek.bin"]["encrypted"], "%s: a file in a vault: %v", where, rows["ek.bin"])
	require.Equal(t, "folder", rows["rapor.docx"]["encrypted"], "%s: a file in an encrypted folder: %v", where, rows["rapor.docx"])
	_, has := rows["acik.txt"]["encrypted"]
	require.False(t, has, "%s: a file outside every encrypted folder carries no such field: %v", where, rows["acik.txt"])
	if withFolder {
		require.Contains(t, rows, "Sifreli", "%s: the folder row missing", where)
		_, has = rows["Sifreli"]["encrypted"]
		require.False(t, has, "%s: a folder is told by e2e / e2e_vault, never by this field: %v", where, rows["Sifreli"])
	}
}

// encRowsByName indexes the rows of one answer by the given name key.
func encRowsByName(t *testing.T, list any, key string) map[string]map[string]any {
	t.Helper()
	items, ok := list.([]any)
	require.True(t, ok, "not a list: %v", list)
	out := map[string]map[string]any{}
	for _, it := range items {
		row := it.(map[string]any)
		name, _ := row[key].(string)
		out[name] = row
	}
	return out
}

func TestEncryptedRows_EveryFileRowSaysHowItIsEncrypted(t *testing.T) {
	f := newEncRowsFix(t)

	for _, tc := range []struct {
		name, path, list string
	}{
		{"recent", "/api/files/manager/recent?limit=50", "nodes"},
		{"starred", "/api/files/manager/star/list?limit=50", "nodes"},
		{"tag view", "/api/files/manager/tagged?tag=damga", "nodes"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			code, m := f.do(t, f.member, http.MethodGet, tc.path, nil, nil)
			require.Equal(t, http.StatusOK, code, "%v", m)
			wantEncrypted(t, tc.name, encRowsByName(t, m[tc.list], "name"), true)
		})
	}

	t.Run("search", func(t *testing.T) {
		code, m := f.do(t, f.member, http.MethodGet, "/api/files/search?q=tag:damga&limit=50", nil, nil)
		require.Equal(t, http.StatusOK, code, "%v", m)
		wantEncrypted(t, "search", encRowsByName(t, m["results"], "name"), true)
	})

	t.Run("the explorer's search action", func(t *testing.T) {
		code, m := f.do(t, f.member, http.MethodGet, "/api/files/manager?action=search&path=depo://&filter=tag:damga", nil, nil)
		require.Equal(t, http.StatusOK, code, "%v", m)
		wantEncrypted(t, "manager search", encRowsByName(t, m["files"], "basename"), true)
	})

	t.Run("a file's own stat", func(t *testing.T) {
		for name, want := range map[string]string{"ek.bin": "vault", "rapor.docx": "folder", "acik.txt": "", "Sifreli": ""} {
			code, m := f.do(t, f.member, http.MethodGet, "/api/files/stat?id="+strconv.FormatInt(f.ids[name], 10), nil, nil)
			require.Equal(t, http.StatusOK, code, "%s: %v", name, m)
			require.Equal(t, name, m["name"], "the node row is what it was: %v", m)
			if want == "" {
				_, has := m["encrypted"]
				require.False(t, has, "%s: %v", name, m)
				continue
			}
			require.Equal(t, want, m["encrypted"], "%s: %v", name, m)
		}
	})

	t.Run("the id-based listing", func(t *testing.T) {
		kasa, err := f.store.GetNodeByPath(context.Background(), f.st.ID, pathkey.Hash(f.st.ID, "/Kasa"))
		require.NoError(t, err)
		code, m := f.do(t, f.member, http.MethodGet,
			"/api/files/manager?storage="+strconv.FormatInt(f.st.ID, 10)+"&parent="+strconv.FormatInt(kasa.ID, 10), nil, nil)
		require.Equal(t, http.StatusOK, code, "%v", m)
		rows := encRowsByName(t, m["nodes"], "name")
		require.Contains(t, rows, "ek.bin", "%v", m)
		require.Equal(t, "vault", rows["ek.bin"]["encrypted"], "%v", rows["ek.bin"])
	})

	// Last: it turns the storage's RBAC on, which the routes above do not
	// expect.
	t.Run("shared with me", func(t *testing.T) {
		ctx := context.Background()
		f.st.RBACEnabled = true
		require.NoError(t, f.store.UpdateStorage(ctx, f.st))
		viewer, err := f.store.GetUserByEmail(ctx, "vault-viewer@test.local")
		require.NoError(t, err)
		for rel, dir := range map[string]bool{"Kasa/ek.bin": false, "Sifreli/rapor.docx": false, "acik.txt": false, "Sifreli": true} {
			_, err := f.store.CreateFileGrant(ctx, &model.FileGrant{
				StorageID: f.st.ID, PathPrefix: rel, IsDir: dir, UserID: viewer.ID, Level: model.GrantViewer,
			})
			require.NoError(t, err)
		}
		code, m := f.do(t, f.viewer, http.MethodGet, "/api/files/manager/shared-with-me?limit=50", nil, nil)
		require.Equal(t, http.StatusOK, code, "%v", m)
		wantEncrypted(t, "shared with me", encRowsByName(t, m["files"], "basename"), true)
	})
}
