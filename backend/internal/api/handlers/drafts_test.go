package handlers_test

// Drafts (issue #71): New document makes a DRAFT, not a file. These run the
// real router (newMTFix, single-tenant: two ordinary accounts, A and B, on the
// same storages, and the platform operator) and assert on the bytes on disk,
// the rows in the catalogue and what every surface answers — create, save,
// save beside a taken name, the limit, discard to the trash, and that one
// person's drafts are nobody else's.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	apitoken "github.com/brf-tech/filex/backend/internal/auth/drivers/apitoken"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/pathkey"
	"github.com/brf-tech/filex/backend/internal/testutil"
)

// draftWire is the draft the API answers with (handlers.draftView).
type draftWire struct {
	Key       string `json:"key"`
	Name      string `json:"name"`
	Path      string `json:"path"`
	Storage   string `json:"storage"`
	TargetDir string `json:"target_dir"`
	Target    string `json:"target"`
	Type      string `json:"type"`
	Size      int64  `json:"size"`
}

// draftReq sends one JSON request and decodes the answer into out (when
// non-nil), returning the status and the raw body.
func draftReq(t *testing.T, c *http.Client, method, u string, body any, out any) (int, string) {
	t.Helper()
	var rdr *strings.Reader
	if body != nil {
		b, err := json.Marshal(body)
		require.NoError(t, err)
		rdr = strings.NewReader(string(b))
	} else {
		rdr = strings.NewReader("")
	}
	req, err := http.NewRequest(method, u, rdr)
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	var sb strings.Builder
	buf := make([]byte, 4096)
	for {
		n, rerr := resp.Body.Read(buf)
		sb.Write(buf[:n])
		if rerr != nil {
			break
		}
	}
	if out != nil && resp.StatusCode/100 == 2 {
		require.NoError(t, json.Unmarshal([]byte(sb.String()), out), sb.String())
	}
	return resp.StatusCode, sb.String()
}

// newDocFolder makes alpha://<name> through the API, the way the explorer does.
func newDocFolder(t *testing.T, f *mtFix, c *http.Client, name string) {
	t.Helper()
	status, body := draftReq(t, c, "POST", f.URL+"/api/files/manager?action=newfolder",
		map[string]any{"path": "alpha://", "name": name}, nil)
	require.Equal(t, http.StatusOK, status, body)
}

// createDraft is the New document dialog's request, as a draft.
func createDraft(t *testing.T, f *mtFix, c *http.Client, dir, name, typ string) draftWire {
	t.Helper()
	var resp struct {
		Path  string    `json:"path"`
		Name  string    `json:"name"`
		Ext   string    `json:"ext"`
		Draft draftWire `json:"draft"`
	}
	status, body := draftReq(t, c, "POST", f.URL+"/api/files/drafts",
		map[string]any{"path": dir, "name": name, "type": typ, "exact_name": true}, &resp)
	require.Equal(t, http.StatusCreated, status, body)
	require.Equal(t, resp.Draft.Path, resp.Path, "the answer's path is the draft's")
	require.Equal(t, name, resp.Name)
	return resp.Draft
}

func listDrafts(t *testing.T, f *mtFix, c *http.Client) []draftWire {
	t.Helper()
	var resp struct {
		Drafts []draftWire `json:"drafts"`
		Count  int         `json:"count"`
		Limit  int         `json:"limit"`
	}
	status, body := draftReq(t, c, "GET", f.URL+"/api/files/drafts", nil, &resp)
	require.Equal(t, http.StatusOK, status, body)
	require.Equal(t, len(resp.Drafts), resp.Count)
	return resp.Drafts
}

func userIDOf(t *testing.T, f *mtFix, email string) int64 {
	t.Helper()
	u, err := f.Store.GetUserByEmail(context.Background(), email)
	require.NoError(t, err)
	return u.ID
}

// onDisk reports whether rel exists under a storage root, and its content.
func onDisk(root, rel string) (string, bool) {
	b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil {
		return "", false
	}
	return string(b), true
}

func saveText(t *testing.T, f *mtFix, c *http.Client, wire, content string) (int, string) {
	t.Helper()
	return draftReq(t, c, "POST", f.URL+"/api/files/save-text", map[string]any{"path": wire, "content": content}, nil)
}

// ---------------------------------------------------------------- create

func TestDrafts_NewDocumentIsADraftNotAFile(t *testing.T) {
	f := newMTFix(t, false)
	newDocFolder(t, f, f.A, "Docs")
	uid := userIDOf(t, f, "member@alpha.test")

	d := createDraft(t, f, f.A, "alpha://Docs", "notes.txt", "txt")

	// Nothing where it was asked for…
	_, there := onDisk(f.RootA, "Docs/notes.txt")
	assert.False(t, there, "New document wrote the file at its destination")
	status, body := mtGet(t, f.A, f.URL+"/api/files/manager?action=index&path="+url.QueryEscape("alpha://Docs"))
	require.Equal(t, http.StatusOK, status)
	assert.NotContains(t, body, "notes.txt", "the destination listing shows a file that is only a draft")

	// …and a real file in the person's drafts area, remembered with its target.
	want := fmt.Sprintf(".filex-drafts/%d/%s/notes.txt", uid, d.Key)
	assert.Equal(t, "alpha://"+want, d.Path)
	_, there = onDisk(f.RootA, want)
	assert.True(t, there, "no draft file at %s", want)
	assert.Equal(t, "alpha://Docs", d.TargetDir)
	assert.Equal(t, "alpha://Docs/notes.txt", d.Target)
	assert.Equal(t, "txt", d.Type)

	list := listDrafts(t, f, f.A)
	require.Len(t, list, 1)
	assert.Equal(t, d.Key, list[0].Key)

	var cnt struct {
		Count int `json:"count"`
		Limit int `json:"limit"`
	}
	status, body = draftReq(t, f.A, "GET", f.URL+"/api/files/drafts/count", nil, &cnt)
	require.Equal(t, http.StatusOK, status, body)
	assert.Equal(t, 1, cnt.Count)
	assert.Equal(t, 50, cnt.Limit, "the owner's default limit")

	// The editor's door: its owner previews it and saves into it.
	resp, err := f.A.Get(f.URL + "/api/files/manager?action=preview&path=" + url.QueryEscape(d.Path))
	require.NoError(t, err)
	resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode, "the owner's editor could not read the draft")
	// A draft is written in place every few seconds: a cached body would reopen
	// it as it was the first time it was read — empty.
	assert.Equal(t, "no-store", resp.Header.Get("Cache-Control"), "a draft's body must never be cached")
	status, body = saveText(t, f, f.A, d.Path, "first words")
	require.Equal(t, http.StatusOK, status, "the owner's editor could not save the draft: %s", body)
	got, _ := onDisk(f.RootA, want)
	assert.Equal(t, "first words", got)
	// …and no version history of an unfinished document.
	_, versioned := onDisk(f.RootA, ".versions")
	assert.False(t, versioned)
}

func TestDrafts_OfficeDocumentGetsItsTemplate(t *testing.T) {
	f := newMTFix(t, false)
	newDocFolder(t, f, f.A, "Docs")
	d := createDraft(t, f, f.A, "alpha://Docs", "Plan.docx", "docx")
	raw, ok := onDisk(f.RootA, strings.TrimPrefix(d.Path, "alpha://"))
	require.True(t, ok)
	assert.True(t, strings.HasPrefix(raw, "PK"), "a draft .docx must be the same ZIP newfile writes")
}

// ---------------------------------------------------------------- save

func TestDrafts_SaveMovesItToItsPlace(t *testing.T) {
	f := newMTFix(t, false)
	newDocFolder(t, f, f.A, "Docs")
	d := createDraft(t, f, f.A, "alpha://Docs", "notes.txt", "txt")
	status, body := saveText(t, f, f.A, d.Path, "hello")
	require.Equal(t, http.StatusOK, status, body)

	var saved struct {
		Path string `json:"path"`
		Name string `json:"name"`
	}
	status, body = draftReq(t, f.A, "POST", f.URL+"/api/files/drafts/"+d.Key+"/save", nil, &saved)
	require.Equal(t, http.StatusOK, status, body)
	assert.Equal(t, "alpha://Docs/notes.txt", saved.Path)
	assert.Equal(t, "notes.txt", saved.Name)

	got, there := onDisk(f.RootA, "Docs/notes.txt")
	require.True(t, there, "Save did not put the file at its destination")
	assert.Equal(t, "hello", got)
	_, stillThere := onDisk(f.RootA, strings.TrimPrefix(d.Path, "alpha://"))
	assert.False(t, stillThere, "the draft file stayed behind")
	draftFolder := filepath.Join(f.RootA, filepath.FromSlash(path.Dir(strings.TrimPrefix(d.Path, "alpha://"))))
	_, statErr := os.Stat(draftFolder)
	assert.True(t, os.IsNotExist(statErr), "the draft's folder stayed behind: %v", statErr)

	assert.Empty(t, listDrafts(t, f, f.A), "a saved draft is still a draft")
	status, body = mtGet(t, f.A, f.URL+"/api/files/manager?action=index&path="+url.QueryEscape("alpha://Docs"))
	require.Equal(t, http.StatusOK, status)
	assert.Contains(t, body, "notes.txt", "the saved document is not listed where it went")

	// Its catalogue row moved with it (the same id — an editor still open on
	// it keeps saving into the document).
	n, err := f.Store.GetNodeByPath(context.Background(), f.StA.ID, pathkey.Hash(f.StA.ID, "/Docs/notes.txt"))
	require.NoError(t, err)
	require.NotNil(t, n)
	assert.Nil(t, n.DeletedAt)
	status, _ = draftReq(t, f.A, "GET", f.URL+"/api/files/drafts/"+d.Key, nil, nil)
	assert.Equal(t, http.StatusNotFound, status)
}

func TestDrafts_SaveBesideATakenNameAsksFirst(t *testing.T) {
	f := newMTFix(t, false)
	newDocFolder(t, f, f.A, "Docs")
	d := createDraft(t, f, f.A, "alpha://Docs", "notes.txt", "txt")
	status, body := saveText(t, f, f.A, d.Path, "the draft")
	require.Equal(t, http.StatusOK, status, body)
	// Somebody else put a notes.txt there in the meantime.
	status, body = saveText(t, f, f.B, "alpha://Docs/notes.txt", "already here")
	require.Equal(t, http.StatusOK, status, body)

	status, body = draftReq(t, f.A, "POST", f.URL+"/api/files/drafts/"+d.Key+"/save", nil, nil)
	require.Equal(t, http.StatusConflict, status, body)
	var refusal map[string]string
	require.NoError(t, json.Unmarshal([]byte(body), &refusal))
	assert.Equal(t, "TARGET_TAKEN", refusal["code"])
	assert.Equal(t, "notes (2).txt", refusal["suggested"], "the same numbering New document suggests")
	got, _ := onDisk(f.RootA, "Docs/notes.txt")
	assert.Equal(t, "already here", got, "the refusal touched what was there")
	require.Len(t, listDrafts(t, f, f.A), 1, "a refused save must leave the draft a draft")

	// A name that is not free either is refused the same way — what the
	// person agreed to is exactly what they get.
	status, body = draftReq(t, f.A, "POST", f.URL+"/api/files/drafts/"+d.Key+"/save",
		map[string]any{"as": "notes.txt"}, nil)
	require.Equal(t, http.StatusConflict, status, body)

	var saved struct {
		Path string `json:"path"`
	}
	status, body = draftReq(t, f.A, "POST", f.URL+"/api/files/drafts/"+d.Key+"/save",
		map[string]any{"as": "notes (2).txt"}, &saved)
	require.Equal(t, http.StatusOK, status, body)
	assert.Equal(t, "alpha://Docs/notes (2).txt", saved.Path)
	got, _ = onDisk(f.RootA, "Docs/notes (2).txt")
	assert.Equal(t, "the draft", got)
	got, _ = onDisk(f.RootA, "Docs/notes.txt")
	assert.Equal(t, "already here", got, "saving the draft replaced the file that had its name")

	// `as` is a name, never a path.
	d2 := createDraft(t, f, f.A, "alpha://Docs", "other.txt", "txt")
	status, _ = draftReq(t, f.A, "POST", f.URL+"/api/files/drafts/"+d2.Key+"/save",
		map[string]any{"as": "../escape.txt"}, nil)
	assert.Equal(t, http.StatusBadRequest, status)
}

func TestDrafts_SaveWhenTheFolderIsGone(t *testing.T) {
	f := newMTFix(t, false)
	newDocFolder(t, f, f.A, "Gone")
	d := createDraft(t, f, f.A, "alpha://Gone", "notes.txt", "txt")
	require.NoError(t, os.RemoveAll(filepath.Join(f.RootA, "Gone")))
	status, body := draftReq(t, f.A, "POST", f.URL+"/api/files/drafts/"+d.Key+"/save", nil, nil)
	require.Equal(t, http.StatusConflict, status, body)
	assert.Contains(t, body, "FOLDER_GONE")
	require.Len(t, listDrafts(t, f, f.A), 1)
}

// ---------------------------------------------------------------- limit

func TestDrafts_LimitRefusesAndCreatesNothing(t *testing.T) {
	f := newMTFix(t, false)
	newDocFolder(t, f, f.A, "Docs")
	require.NoError(t, f.Store.UpsertSetting(context.Background(), "drafts.limit", "2"))

	createDraft(t, f, f.A, "alpha://Docs", "a.txt", "txt")
	second := createDraft(t, f, f.A, "alpha://Docs", "b.txt", "txt")
	status, body := draftReq(t, f.A, "POST", f.URL+"/api/files/drafts",
		map[string]any{"path": "alpha://Docs", "name": "c.txt", "type": "txt", "exact_name": true}, nil)
	require.Equal(t, http.StatusConflict, status, body)
	var refusal map[string]any
	require.NoError(t, json.Unmarshal([]byte(body), &refusal))
	assert.Equal(t, "DRAFT_LIMIT", refusal["code"])
	assert.EqualValues(t, 2, refusal["limit"])
	// ⚠ Not a file instead: the owner's rule.
	_, there := onDisk(f.RootA, "Docs/c.txt")
	assert.False(t, there, "at the limit New document created the file instead")
	assert.Len(t, listDrafts(t, f, f.A), 2)

	// Another person's limit is their own.
	createDraft(t, f, f.B, "alpha://Docs", "c.txt", "txt")

	// Saving (or discarding) one makes room.
	status, body = draftReq(t, f.A, "POST", f.URL+"/api/files/drafts/"+second.Key+"/save", nil, nil)
	require.Equal(t, http.StatusOK, status, body)
	createDraft(t, f, f.A, "alpha://Docs", "c.txt", "txt")
}

// ---------------------------------------------------------------- discard

func TestDrafts_DiscardGoesToTheTrash_AndComesBack(t *testing.T) {
	f := newMTFix(t, false)
	newDocFolder(t, f, f.A, "Docs")
	d := createDraft(t, f, f.A, "alpha://Docs", "notes.txt", "txt")
	_, _ = saveText(t, f, f.A, d.Path, "keep me for a while")

	status, body := draftReq(t, f.A, "DELETE", f.URL+"/api/files/drafts/"+d.Key, nil, nil)
	require.Equal(t, http.StatusOK, status, body)
	assert.Contains(t, body, `"trashed":true`)
	assert.Empty(t, listDrafts(t, f, f.A))

	// The bytes are in the bin, where the retention purge will find them.
	entries, err := os.ReadDir(filepath.Join(f.RootA, ".filex-trash"))
	require.NoError(t, err, "nothing went to the trash")
	require.Len(t, entries, 1)
	assert.True(t, strings.HasSuffix(entries[0].Name(), "__notes.txt"))

	// Its owner sees it in the trash — as a draft, never by its internal path.
	status, body = mtGet(t, f.A, f.URL+"/api/files/manager/trash")
	require.Equal(t, http.StatusOK, status)
	var trashResp struct {
		Entries []struct {
			ID    int64  `json:"id"`
			Name  string `json:"name"`
			Path  string `json:"path"`
			Draft bool   `json:"draft"`
		} `json:"entries"`
	}
	require.NoError(t, json.Unmarshal([]byte(body), &trashResp), body)
	require.Len(t, trashResp.Entries, 1, body)
	e := trashResp.Entries[0]
	assert.Equal(t, "notes.txt", e.Name)
	assert.True(t, e.Draft)
	assert.Equal(t, "/notes.txt", e.Path)
	assert.NotContains(t, body, ".filex-drafts", "the trash named the drafts area")

	// Nobody else does, and nobody else brings it back.
	status, body = mtGet(t, f.B, f.URL+"/api/files/manager/trash")
	require.Equal(t, http.StatusOK, status)
	assert.NotContains(t, body, "notes.txt", "another person's trash listed somebody's discarded draft")
	status, _ = draftReq(t, f.B, "POST", f.URL+"/api/files/manager/restore", map[string]any{"node_id": e.ID}, nil)
	assert.Equal(t, http.StatusNotFound, status)

	// Its owner restores it — and it is a draft again.
	status, body = draftReq(t, f.A, "POST", f.URL+"/api/files/manager/restore", map[string]any{"node_id": e.ID}, nil)
	require.Equal(t, http.StatusOK, status, body)
	list := listDrafts(t, f, f.A)
	require.Len(t, list, 1)
	assert.Equal(t, d.Key, list[0].Key)
	got, _ := onDisk(f.RootA, strings.TrimPrefix(d.Path, "alpha://"))
	assert.Equal(t, "keep me for a while", got)
}

// ---------------------------------------------------------------- isolation

func TestDrafts_AreTheirOwnersAlone(t *testing.T) {
	f := newMTFix(t, false)
	newDocFolder(t, f, f.A, "Docs")
	d := createDraft(t, f, f.A, "alpha://Docs", "secret plan.txt", "txt")
	_, _ = saveText(t, f, f.A, d.Path, "not for anybody else")

	for who, c := range map[string]*http.Client{"another member": f.B, "the platform operator": f.Super} {
		assert.Empty(t, listDrafts(t, f, c), "%s listed somebody's drafts", who)
		for _, req := range []struct{ method, path string }{
			{"GET", "/api/files/drafts/" + d.Key},
			{"POST", "/api/files/drafts/" + d.Key + "/save"},
			{"DELETE", "/api/files/drafts/" + d.Key},
		} {
			status, _ := draftReq(t, c, req.method, f.URL+req.path, nil, nil)
			assert.Equal(t, http.StatusNotFound, status, "%s: %s %s", who, req.method, req.path)
		}
		for _, action := range []string{"preview", "download", "index"} {
			status, body := mtGet(t, c, f.URL+"/api/files/manager?action="+action+"&path="+url.QueryEscape(d.Path))
			assert.Equal(t, http.StatusNotFound, status, "%s read a draft by path (%s)", who, action)
			assert.NotContains(t, body, "not for anybody else")
		}
		status, body := saveText(t, f, c, d.Path, "overwritten")
		assert.Equal(t, http.StatusForbidden, status, "%s saved into somebody's draft: %s", who, body)
	}
	got, _ := onDisk(f.RootA, strings.TrimPrefix(d.Path, "alpha://"))
	assert.Equal(t, "not for anybody else", got)

	// Not even its owner lists the drafts area: only the editor's exact file.
	for _, wire := range []string{"alpha://.filex-drafts", filepath.ToSlash(filepath.Dir(d.Path))} {
		status, _ := mtGet(t, f.A, f.URL+"/api/files/manager?action=index&path="+url.QueryEscape(wire))
		assert.Equal(t, http.StatusNotFound, status, "the drafts area %s was listed", wire)
	}
	// Nor renames, moves, copies or shares it through the ordinary doors.
	status, _ := draftReq(t, f.A, "POST", f.URL+"/api/files/manager?action=rename",
		map[string]any{"path": "alpha://", "item": d.Path, "name": "x.txt"}, nil)
	assert.Equal(t, http.StatusForbidden, status)
}

func TestDrafts_HiddenFromListingsSearchAndCounts(t *testing.T) {
	f := newMTFix(t, false)
	newDocFolder(t, f, f.A, "Docs")
	f.seedFile(t, f.StA, f.RootA, "Docs/Plan notes.txt", "a person's own file")
	countBefore, sizeBefore, err := f.Store.StorageStats(context.Background(), f.StA.ID)
	require.NoError(t, err)
	d := createDraft(t, f, f.A, "alpha://Docs", "Plan draft.txt", "txt")
	_, _ = saveText(t, f, f.A, d.Path, "Plan of the draft")

	for _, c := range []*http.Client{f.A, f.B} {
		status, body := mtGet(t, c, f.URL+"/api/files/manager?action=index&path="+url.QueryEscape("alpha://"))
		require.Equal(t, http.StatusOK, status)
		assert.NotContains(t, body, ".filex-drafts", "the root listing offered the drafts area")

		status, body = mtGet(t, c, f.URL+"/api/files/search?q=Plan")
		require.Equal(t, http.StatusOK, status, body)
		assert.Contains(t, body, "Plan notes.txt", "precondition: the search answers")
		assert.NotContains(t, body, "Plan draft.txt", "search found a draft")

		status, body = mtGet(t, c, f.URL+"/api/files/manager?action=search&path="+url.QueryEscape("alpha://")+"&filter=Plan")
		require.Equal(t, http.StatusOK, status, body)
		assert.NotContains(t, body, "Plan draft.txt", "the toolbar search found a draft")

		status, body = mtGet(t, c, f.URL+"/api/files/manager/recent")
		if status == http.StatusOK {
			assert.NotContains(t, body, "Plan draft.txt")
		}
	}

	// The storage's counts leave it out, the way they leave the trash out.
	count, size, err := f.Store.StorageStats(context.Background(), f.StA.ID)
	require.NoError(t, err)
	assert.Equal(t, countBefore, count, "a draft was counted as a file of the storage")
	assert.Equal(t, sizeBefore, size, "a draft's bytes were counted as the storage's")
}

// ---------------------------------------------------------------- who may

func TestDrafts_NotForAppsOrConfinedCallers(t *testing.T) {
	f := newMTFix(t, false)
	newDocFolder(t, f, f.A, "Docs")
	uid := userIDOf(t, f, "member@alpha.test")

	// A person: capabilities offers drafts, with the limit.
	status, body := mtGet(t, f.A, f.URL+"/api/files/capabilities")
	require.Equal(t, http.StatusOK, status)
	assert.Contains(t, body, `"drafts":{"limit":50}`)

	// A person's own token (the desktop app's, the CLI's) is that person.
	own := draftTestToken(t, f, uid, model.TokenKindUser, fullScopes)
	status, body = fxReq(t, "GET", f.URL+"/api/files/capabilities", own, nil, "")
	require.Equal(t, http.StatusOK, status)
	assert.Contains(t, body, `"drafts":{"limit":50}`, "a person's own token was not offered drafts")

	// An app token, and a token confined to one folder, are not a person
	// acting for themselves: no drafts offered, none made.
	app := draftTestToken(t, f, uid, model.TokenKindApp, fullScopes)
	confined := draftTestToken(t, f, uid, model.TokenKindUser, fullScopes+",root:alpha://Docs")
	for who, tok := range map[string]string{"app token": app, "confined token": confined} {
		status, body := fxReq(t, "GET", f.URL+"/api/files/capabilities", tok, nil, "")
		require.Equal(t, http.StatusOK, status)
		assert.NotContains(t, body, `"drafts"`, "%s was offered drafts", who)
		status, body = fxReq(t, "POST", f.URL+"/api/files/drafts", tok,
			strings.NewReader(`{"path":"alpha://Docs","name":"x.txt","type":"txt","exact_name":true}`), "application/json")
		assert.Equal(t, http.StatusForbidden, status, "%s: %s", who, body)
		assert.Contains(t, body, "DRAFTS_UNAVAILABLE")
	}
	_, there := onDisk(f.RootA, ".filex-drafts")
	assert.False(t, there)
}

// draftTestToken mints an API token of a kind for uid.
func draftTestToken(t *testing.T, f *mtFix, uid int64, kind, scopes string) string {
	t.Helper()
	plain := "tok_" + randToken(t)
	_, err := f.Store.CreateAPIToken(context.Background(), &model.APIToken{
		UserID: uid, Label: kind + "-draft-token", TokenHash: apitoken.HashToken(plain),
		Scopes: scopes, Kind: kind,
	})
	require.NoError(t, err)
	return plain
}

// ---------------------------------------------------------------- the document editor

// personToken mints a person's own (user-kind) token: the kind the desktop
// app and the CLI hold, and the only kind that keeps drafts.
func personToken(t *testing.T, store interface {
	CreateAPIToken(ctx context.Context, tok *model.APIToken) (*model.APIToken, error)
}, uid int64) string {
	t.Helper()
	plain := "tok_" + randToken(t)
	_, err := store.CreateAPIToken(context.Background(), &model.APIToken{
		UserID: uid, Label: "person", TokenHash: apitoken.HashToken(plain),
		Scopes: fullScopes, Kind: model.TokenKindUser,
	})
	require.NoError(t, err)
	return plain
}

// An office document made as a draft is edited in ONLYOFFICE like any other:
// its owner gets an editing session, the document server's signed save lands
// in the draft — and one that arrives after the draft was saved lands in the
// saved document, because the save moved the file with its catalogue row.
// Nobody else, the administrator included, gets a session on it at all.
func TestDrafts_TheDocumentEditorEditsTheOwnersDraft(t *testing.T) {
	root := t.TempDir()
	srv, store := editorFixture(t, root)
	ctx := context.Background()
	adminID, _ := testutil.SeedAdminUser(t, store)
	admin := issueToken(t, store, adminID, fullScopes, nil)
	seedServerFile(t, store, "oo", "Documents/Rapor.docx", "PK already there")

	grant := func(email string) (int64, string) {
		testutil.SeedRegularUser(t, store, email, "SomePass1!")
		u, err := store.GetUserByEmail(ctx, email)
		require.NoError(t, err)
		status, body := fxPost(t, srv.URL+"/api/files/permissions", admin,
			map[string]any{"path": "oo://Documents", "user_id": u.ID, "level": "editor"})
		require.Equal(t, http.StatusOK, status, "grant: %s", body)
		return u.ID, personToken(t, store, u.ID)
	}
	_, owner := grant("owner@oo.test")
	_, other := grant("other@oo.test")

	status, body := fxPost(t, srv.URL+"/api/files/drafts", owner,
		map[string]any{"path": "oo://Documents", "name": "Plan.docx", "type": "docx", "exact_name": true})
	require.Equal(t, http.StatusCreated, status, body)
	var created struct {
		Draft draftWire `json:"draft"`
	}
	require.NoError(t, json.Unmarshal([]byte(body), &created))
	d := created.Draft
	rel := strings.TrimPrefix(d.Path, "oo://")
	st, err := store.GetStorageByName(ctx, "oo")
	require.NoError(t, err)
	n, err := store.GetNodeByPath(ctx, st.ID, pathkey.Hash(st.ID, "/"+rel))
	require.NoError(t, err)

	config := func(tok string, payload map[string]any) (int, string) {
		return fxPost(t, srv.URL+"/api/files/onlyoffice/config", tok, payload)
	}
	status, body = config(owner, map[string]any{"path": d.Path, "mode": "edit"})
	require.Equal(t, http.StatusOK, status, body)
	assert.Equal(t, "edit", editorModeOf(t, body), "the owner's draft opened read-only")

	for who, tok := range map[string]string{"another editor of the folder": other, "the administrator": admin} {
		for _, payload := range []map[string]any{{"path": d.Path, "mode": "edit"}, {"node_id": n.ID, "mode": "edit"}} {
			status, body := config(tok, payload)
			assert.Equal(t, http.StatusForbidden, status, "%s got a session on somebody's draft (%v): %s", who, payload, body)
		}
	}

	status, body = editorSave(t, srv.URL, n.ID, "PK typed into the draft")
	require.Equal(t, http.StatusOK, status)
	assert.Contains(t, body, `"error":0`, "the document server's save of a draft was refused: %s", body)
	got, _ := onDisk(root, rel)
	assert.Equal(t, "PK typed into the draft", got)

	// Saved beside the Rapor.docx already there — and the editor's last save,
	// arriving after, goes into the saved document.
	status, body = fxPost(t, srv.URL+"/api/files/drafts/"+d.Key+"/save", owner, map[string]any{})
	require.Equal(t, http.StatusOK, status, body)
	status, body = editorSave(t, srv.URL, n.ID, "PK the last save")
	require.Equal(t, http.StatusOK, status)
	assert.Contains(t, body, `"error":0`, body)
	got, _ = onDisk(root, "Documents/Plan.docx")
	assert.Equal(t, "PK the last save", got, "a save that arrived after the draft was saved went astray")
	got, _ = onDisk(root, "Documents/Rapor.docx")
	assert.Equal(t, "PK already there", got)
}
