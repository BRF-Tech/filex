package handlers_test

// Who may encrypt (internal/e2epolicy) holds at EVERY door that creates a
// file.
//
// Creating an encrypted folder's key file (`.filex-e2e.json`) or a single
// encrypted file (`*.fxe`) is a new encryption. With the policy `off` nobody —
// administrators included — may make one, and every door that could refuses
// it: 403 e2e_not_allowed where the door answers for one file, a skipped
// member where it answers for an archive. Nothing reaches the disk and no
// catalogue row is written.
//
// What is not a NEW encryption keeps working with the policy off: rewriting a
// key file that is there (a password change, a level change), replacing a
// `.fxe`, adding a file into an encrypted folder, a rename or a move of what
// is already encrypted and a restore from the trash (spec, clarification 2).
// A plain file renamed, moved or copied onto a key file's or a `.fxe`'s name
// is a new encryption, and asked (e2e_policy_rename_test.go); so is a COPY of
// an encrypted folder or of a `.fxe` (operator decision 2026-10-03,
// e2e_policy_copy_test.go).

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	v4 "github.com/aws/aws-sdk-go-v2/aws/signer/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/acl"
	"github.com/brf-tech/filex/backend/internal/api"
	"github.com/brf-tech/filex/backend/internal/api/handlers"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/e2epolicy"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/pathkey"
	"github.com/brf-tech/filex/backend/internal/protocolauth"
	"github.com/brf-tech/filex/backend/internal/share"
	"github.com/brf-tech/filex/backend/internal/srvtext"
	"github.com/brf-tech/filex/backend/internal/storage"
	"github.com/brf-tech/filex/backend/internal/storage/drivers/local"
)

// e2eKeyFile is an encrypted folder's key file (e2e.MarkerName).
const e2eKeyFile = ".filex-e2e.json"

// fxeBody opens like a single encrypted file. Only the NAME matters to the
// rule; it never reads content.
const fxeBody = "filexfxe\x01not a real header"

// encryptionOff switches the instance's policy (e2e.policy) to `off`: nobody,
// administrators included, may start an encryption.
func encryptionOff(t *testing.T, store db.Store) {
	t.Helper()
	require.NoError(t, store.UpsertSetting(context.Background(), model.SettingE2EPolicy, model.E2EPolicyOff))
}

// assertE2ERefused: what a door that creates one file answers when the policy
// says no — 403, the code a client branches on, the reason, and a sentence.
func assertE2ERefused(t *testing.T, what string, status int, body string) {
	t.Helper()
	assert.Equal(t, http.StatusForbidden, status, "%s was let through: %s", what, body)
	var got struct{ Error, Reason, Message string }
	_ = json.Unmarshal([]byte(body), &got)
	assert.Equal(t, "e2e_not_allowed", got.Error, "%s: %s", what, body)
	assert.Equal(t, string(e2epolicy.ReasonPolicyOff), got.Reason, "%s: %s", what, body)
	assert.NotEmpty(t, got.Message, "%s: the refusal says why in words", what)
}

// assertKeylessRefused: what a surface that holds no key (the agent API, and
// ShareX and the upload ticket through it) answers for an encrypted folder's
// key file. Since 0.50 it never writes, moves or renames anything onto that
// name (syspath.Keyless): 403 RESERVED_NAME, before the rule is asked.
func assertKeylessRefused(t *testing.T, what string, status int, body string) {
	t.Helper()
	assert.Equal(t, http.StatusForbidden, status, "%s was let through: %s", what, body)
	var got struct{ Code string }
	_ = json.Unmarshal([]byte(body), &got)
	assert.Equal(t, "RESERVED_NAME", got.Code, "%s: %s", what, body)
}

// assertNotWritten: a refused create left nothing — no bytes on the storage,
// no catalogue row.
func assertNotWritten(t *testing.T, f *mtFix, rel string) {
	t.Helper()
	assert.NoFileExists(t, filepath.Join(f.RootA, filepath.FromSlash(rel)), "%s reached the disk", rel)
	n, err := f.Store.GetNodeByPath(context.Background(), f.StA.ID, pathkey.Hash(f.StA.ID, "/"+rel))
	assert.True(t, err != nil || n == nil, "%s has a catalogue row", rel)
}

// appsWithTheRule is an AppPlugins built by hand over alpha's storage with the
// rule wired, so a test can reach CommitSibling — an app's output, judged for
// the person it is written for — directly. The router's own AppPlugins is
// walked in e2e_policy_app_test.go.
func appsWithTheRule(t *testing.T, f *mtFix) *handlers.AppPlugins {
	t.Helper()
	aclR := acl.New(f.Store)
	apps := handlers.NewAppPlugins(nil, f.Store, aclR, nil, func(int64) (storage.Driver, error) {
		d := &local.Driver{}
		if err := d.Init(context.Background(), map[string]any{"root": f.RootA}); err != nil {
			return nil, err
		}
		return d, nil
	}, nil, nil)
	apps.E2EPolicy = e2epolicy.New(e2epolicy.Options{Store: f.Store, ACL: aclR})
	return apps
}

func TestE2EPolicy_EveryHTTPCreateDoorRefusesWhenEncryptionIsOff(t *testing.T) {
	f := newMTFix(t, false)
	ctx := context.Background()
	admin, err := f.Store.GetUserByEmail(ctx, "admin@alpha.test")
	require.NoError(t, err)
	tok := issueToken(t, f.Store, admin.ID, fullScopes, nil)
	var (
		status int
		body   string
	)

	// While encryption is still allowed (the default, `permitted`): the
	// folders the doors write into, a plain file, and an archive whose members
	// are named like the two.
	for _, dir := range []string{"Acik", "Gelen"} {
		status, body = fxMutate(t, f.URL, tok, "newfolder", map[string]any{"path": "alpha://", "name": dir})
		require.Equal(t, http.StatusOK, status, body)
	}
	fxUpload(t, f.URL, tok, "alpha://Acik", "duz.txt", "plain text")
	zipBytes := buildZip(t, map[string]string{e2eKeyFile: kfOne, "alt/gizli.fxe": fxeBody, "icerik.txt": "plain"})
	fxUpload(t, f.URL, tok, "alpha://Acik", "paket.zip", string(zipBytes))
	encryptionOff(t, f.Store)

	// ── the explorer ─────────────────────────────────────────────────────
	for _, name := range []string{e2eKeyFile, "yeni.fxe"} {
		status, body = fxUploadStatus(t, f.URL, tok, "alpha://Acik", name, "x")
		assertE2ERefused(t, "an upload of "+name, status, body)
		assertNotWritten(t, f, "Acik/"+name)
	}
	// save-text takes a key file (JSON is text); a .fxe is not text and is
	// refused there before the rule is asked.
	status, body = fxPost(t, f.URL+"/api/files/save-text", tok, map[string]any{"path": "alpha://Acik/" + e2eKeyFile, "content": kfOne})
	assertE2ERefused(t, "the text editor saving a new key file", status, body)
	assertNotWritten(t, f, "Acik/"+e2eKeyFile)
	for _, doc := range []map[string]any{
		{"path": "alpha://Acik", "name": e2eKeyFile, "type": "json", "exact_name": true},
		{"path": "alpha://Acik", "name": "yeni-belge.fxe", "type": "txt", "exact_name": true},
	} {
		name := doc["name"].(string)
		status, body = fxMutate(t, f.URL, tok, "newfile", doc)
		assertE2ERefused(t, "New document "+name, status, body)
		assertNotWritten(t, f, "Acik/"+name)
	}
	// A draft (#71) is not a door — it lives in its owner's drafts area — but
	// its save is: that is where the file comes into being. Drafts are kept
	// for a person, not for an API key, so this one is the admin's session.
	code, out := doJSON(t, f.AdminA, http.MethodPost, f.URL+"/api/files/drafts", map[string]any{"path": "alpha://Acik", "name": e2eKeyFile, "type": "json", "exact_name": true})
	require.Equal(t, http.StatusCreated, code, "%v", out)
	draft, _ := out["draft"].(map[string]any)
	key, _ := draft["key"].(string)
	require.NotEmpty(t, key, "%v", out)
	code, out = doJSON(t, f.AdminA, http.MethodPost, f.URL+"/api/files/drafts/"+key+"/save", map[string]any{})
	saved, _ := json.Marshal(out)
	assertE2ERefused(t, "a draft's save", code, string(saved))
	assertNotWritten(t, f, "Acik/"+e2eKeyFile)

	// ── the agent API and the surfaces that write through it ──────────────
	// A key file is refused there before the rule is asked: the surface holds
	// no key and never writes one (assertKeylessRefused). A `.fxe` is the
	// rule's.
	status, body = fxPost(t, f.URL+"/api/ai/upload", tok, map[string]any{"path": "alpha://Acik/" + e2eKeyFile, "content": "x"})
	assertKeylessRefused(t, "an agent's upload of a key file", status, body)
	assertNotWritten(t, f, "Acik/"+e2eKeyFile)
	status, body = fxPost(t, f.URL+"/api/ai/upload", tok, map[string]any{"path": "alpha://Acik/ajan.fxe", "content": "x"})
	assertE2ERefused(t, "an agent's upload to alpha://Acik/ajan.fxe", status, body)
	assertNotWritten(t, f, "Acik/ajan.fxe")
	status, body = fxPost(t, f.URL+"/api/ai/zip", tok, map[string]any{"sources": []string{"alpha://Acik/duz.txt"}, "dest": "alpha://Acik/arsiv.fxe"})
	assertE2ERefused(t, "an agent's zip named .fxe", status, body)
	assertNotWritten(t, f, "Acik/arsiv.fxe")
	// A ticket is minted for the path; its redeem is the write.
	status, body = fxPost(t, f.URL+"/api/ai/upload/ticket", tok, map[string]any{"path": "alpha://Acik/bilet.fxe"})
	require.Equal(t, http.StatusOK, status, body)
	var ticket struct{ Ticket string }
	require.NoError(t, json.Unmarshal([]byte(body), &ticket))
	req, err := http.NewRequest(http.MethodPut, f.URL+"/u/"+ticket.Ticket, strings.NewReader("x"))
	require.NoError(t, err)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	raw, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	assertE2ERefused(t, "a ticket's upload", resp.StatusCode, string(raw))
	assertNotWritten(t, f, "Acik/bilet.fxe")
	var capture bytes.Buffer
	cw := multipart.NewWriter(&capture)
	fw, err := cw.CreateFormFile("file", "ekran.fxe")
	require.NoError(t, err)
	_, _ = fw.Write([]byte(fxeBody))
	require.NoError(t, cw.Close())
	status, body = fxReq(t, http.MethodPost, f.URL+"/api/sharex/upload", tok, &capture, cw.FormDataContentType())
	assertE2ERefused(t, "a ShareX capture named .fxe", status, body)
	for _, root := range []string{f.RootA, f.RootA2, f.RootB} {
		landed, _ := filepath.Glob(filepath.Join(root, "sharex", "*.fxe"))
		assert.Empty(t, landed, "a ShareX capture landed")
	}

	// ── archives: a member named like the two is skipped, the rest land ───
	// Skipped for the POLICY: a member skipped for want of a person to judge
	// looks the same from here, and TestE2EPolicy_TheActorPathsCreateWhatThePolicyPermits
	// is what tells the two apart. Not for the agent unzip's key file: that
	// surface skips the member before the rule is asked, whatever the policy
	// (syspath.Keyless), so here it pins the keyless skip, and the `.fxe`
	// member alone is the policy's.
	status, body = fxPost(t, f.URL+"/api/ai/unzip", tok, map[string]any{"src": "alpha://Acik/paket.zip", "dest": "alpha://AjanCikti"})
	require.Equal(t, http.StatusOK, status, body)
	assert.Contains(t, body, `"extracted":1`)
	assert.FileExists(t, filepath.Join(f.RootA, "AjanCikti", "icerik.txt"), "precondition: the ordinary member was extracted")
	assertNotWritten(t, f, "AjanCikti/"+e2eKeyFile)
	assertNotWritten(t, f, "AjanCikti/alt/gizli.fxe")
	status, body = fxPost(t, f.URL+"/api/files/archive/extract", tok, map[string]any{"storage_id": f.StA.ID, "path": "Acik/paket.zip", "dest": ""})
	require.Equal(t, http.StatusAccepted, status, body)
	f.drainOps(t)
	assert.FileExists(t, filepath.Join(f.RootA, "Acik", "icerik.txt"), "precondition: the ordinary member was extracted")
	assertNotWritten(t, f, "Acik/"+e2eKeyFile)
	assertNotWritten(t, f, "Acik/alt/gizli.fxe")
	status, body = fxPost(t, f.URL+"/api/files/archive/add", tok, map[string]any{
		"storage_id": f.StA.ID, "path": "Acik/ek.fxe",
		"files": []map[string]string{{"name": "duz.txt", "source": "Acik/duz.txt"}},
	})
	assertE2ERefused(t, "a new archive named .fxe (add)", status, body)
	assertNotWritten(t, f, "Acik/ek.fxe")
	status, body = fxPost(t, f.URL+"/api/files/archive/create", tok, map[string]any{
		"sources": []string{"alpha://Acik/duz.txt"}, "dest": "alpha://Acik/paket.fxe", "format": "zip",
	})
	assertE2ERefused(t, "a new archive named .fxe (create)", status, body)
	assertNotWritten(t, f, "Acik/paket.fxe")

	// ── a file request: the visitor has no account, so the LINK CREATOR's
	// rule applies — asked before the submission folder is made ──────────
	gelen, err := f.Store.GetNodeByPath(ctx, f.StA.ID, pathkey.Hash(f.StA.ID, "/Gelen"))
	require.NoError(t, err)
	link, err := share.NewService(f.Store).Create(ctx, share.CreateOpts{NodeID: gelen.ID, Kind: model.ShareKindDrop, CreatedBy: &admin.ID})
	require.NoError(t, err)
	for _, name := range []string{e2eKeyFile, "gonderi.fxe"} {
		var drop bytes.Buffer
		dw := multipart.NewWriter(&drop)
		part, err := dw.CreateFormFile("file[]", name)
		require.NoError(t, err)
		_, _ = part.Write([]byte("x"))
		require.NoError(t, dw.Close())
		status, body = fxReq(t, http.MethodPost, f.URL+"/d/"+link.Token, "", &drop, dw.FormDataContentType())
		assertE2ERefused(t, "a file request carrying "+name, status, body)
	}
	left, err := os.ReadDir(filepath.Join(f.RootA, "Gelen"))
	require.NoError(t, err)
	assert.Empty(t, left, "a refused drop still made its submission folder")

	// ── an app's output: an interface's "save as" and a job's result both
	// write through CommitSibling, judged for the person it writes for ────
	apps := appsWithTheRule(t, f)
	for _, name := range []string{e2eKeyFile, "cikti.fxe"} {
		_, err := apps.CommitSibling(ctx, f.StA.ID, "Acik", name, strings.NewReader("x"), 1, &admin.ID)
		// The reason, not just the refusal: an actor CommitSibling cannot find
		// (e2eActor answering nil) is refused too — as `permission` — and would
		// satisfy ErrRefused alone.
		var refused *e2epolicy.RefusedError
		if assert.ErrorAs(t, err, &refused, "an app wrote %s", name) {
			assert.Equal(t, e2epolicy.ReasonPolicyOff, refused.Reason, "%s is refused for the policy, not for want of a person", name)
		}
		assertNotWritten(t, f, "Acik/"+name)
	}
	rel, err := apps.CommitSibling(ctx, f.StA.ID, "Acik", "cikti.pdf", strings.NewReader("x"), 1, &admin.ID)
	require.NoError(t, err, "an ordinary output is written")
	assert.Equal(t, "Acik/cikti.pdf", rel)

	// ── nothing of either kind reached the storage ───────────────────────
	for _, name := range []string{"yeni.fxe", "ajan.fxe", "arsiv.fxe", "bilet.fxe", "ek.fxe", "paket.fxe", "cikti.fxe", "yeni-belge.fxe"} {
		assertNotWritten(t, f, "Acik/"+name)
	}
	assertNotWritten(t, f, "Acik/"+e2eKeyFile)
}

// The resumable upload (upload_staged.go) asks at begin — before a staging
// directory exists — and not again at commit: under the approval policy the
// question spends the approval.
func TestE2EPolicy_StagedUploadRefusesAtBegin(t *testing.T) {
	f := newStagedFixture(t)
	encryptionOff(t, f.store)
	for _, name := range []string{e2eKeyFile, "buyuk.fxe"} {
		code, out := f.begin(t, map[string]any{"path": "main://", "name": name, "size": 7})
		assert.Equal(t, http.StatusForbidden, code, "%v", out)
		assert.Equal(t, "e2e_not_allowed", out["error"], "%v", out)
		assert.NoFileExists(t, filepath.Join(f.rootDir, name))
	}
	staged, _ := os.ReadDir(filepath.Join(f.dataDir, "uploads"))
	assert.Empty(t, staged, "a refused begin reserved a staging directory")
	code, out := f.begin(t, map[string]any{"path": "main://", "name": "buyuk.bin", "size": 7})
	require.Equal(t, http.StatusOK, code, "an ordinary file: %v", out)
}

// The MCP surface builds a fresh ops core for every request (getServer) and
// the router hands it the rule (AIMCP.AttachE2EPolicy). The REST doors of the
// matrix reach neither line, so this sends a real tools/call through the real
// router: deleting either line lets a key file or a `.fxe` through MCP.
func TestE2EPolicy_TheMCPFileWriteToolAsksTheRule(t *testing.T) {
	f := newMTFix(t, false)
	ctx := context.Background()
	admin, err := f.Store.GetUserByEmail(ctx, "admin@alpha.test")
	require.NoError(t, err)
	tok := issueToken(t, f.Store, admin.ID, fullScopes, nil)
	status, body := fxMutate(t, f.URL, tok, "newfolder", map[string]any{"path": "alpha://", "name": "Acik"})
	require.Equal(t, http.StatusOK, status, body)
	encryptionOff(t, f.Store)

	fileWrite := func(target string) (int, string) {
		call, err := json.Marshal(map[string]any{
			"jsonrpc": "2.0", "id": 1, "method": "tools/call",
			"params": map[string]any{"name": "file_write", "arguments": map[string]any{"path": target, "content": "x"}},
		})
		require.NoError(t, err)
		return mcpPost(t, &http.Client{}, f.URL+"/api/ai/mcp", tok, string(call))
	}

	// A tool answers its refusal as an error RESULT (HTTP 200, isError), with
	// the rule's reason in the text: `policy_off`, which is also what says the
	// person was found — with nobody to judge, the reason would be `permission`.
	status, body = fileWrite("alpha://Acik/mcp.fxe")
	require.Equal(t, http.StatusOK, status, body)
	assert.Contains(t, body, `"isError":true`, "the tool let a .fxe through: %s", body)
	assert.Contains(t, body, string(e2epolicy.ReasonPolicyOff), "the refusal names its reason: %s", body)
	assertNotWritten(t, f, "Acik/mcp.fxe")

	// The same tool, the same policy, an ordinary name: written.
	status, body = fileWrite("alpha://Acik/mcp.txt")
	require.Equal(t, http.StatusOK, status, body)
	assert.NotContains(t, body, `"isError":true`, "an ordinary file: %s", body)
	assert.FileExists(t, filepath.Join(f.RootA, "Acik", "mcp.txt"))
}

// A tenant's policy is its own, and the service provider's ceiling is the
// tenant's: switching alpha off stops alpha's people, not bravo's.
func TestE2EPolicy_ATenantsPolicyIsItsOwn(t *testing.T) {
	f := newMTFix(t, true)
	ctx := context.Background()
	tokA := issueToken(t, f.Store, f.UserA, "read,write", nil)
	tokB := issueToken(t, f.Store, f.UserB, "read,write", nil)
	for _, c := range []struct{ tok, storage string }{{tokA, "alpha"}, {tokB, "bravo"}} {
		status, body := fxMutate(t, f.URL, c.tok, "newfolder", map[string]any{"path": c.storage + "://", "name": "Kasa"})
		require.Equal(t, http.StatusOK, status, body)
	}
	require.NoError(t, f.Store.SetProviderE2E(ctx, f.ProvA, model.ProviderE2E{Allowed: true, Policy: model.E2EPolicyOff}))

	status, body := fxUploadStatus(t, f.URL, tokA, "alpha://Kasa", e2eKeyFile, kfOne)
	assertE2ERefused(t, "alpha's member while alpha is off", status, body)
	assertNotWritten(t, f, "Kasa/"+e2eKeyFile)
	status, body = fxUploadStatus(t, f.URL, tokB, "bravo://Kasa", e2eKeyFile, kfOne)
	require.Equal(t, http.StatusOK, status, "bravo is not alpha: %s", body)

	// The ceiling: alpha chose `permitted`, but the provider has not made
	// encryption available to it.
	require.NoError(t, f.Store.SetProviderE2E(ctx, f.ProvA, model.ProviderE2E{Allowed: false, Policy: model.E2EPolicyPermitted}))
	status, body = fxUploadStatus(t, f.URL, tokA, "alpha://Kasa", "rapor.fxe", fxeBody)
	require.Equal(t, http.StatusForbidden, status, body)
	assert.Contains(t, body, `"reason":"tenant_disabled"`)
	assertNotWritten(t, f, "Kasa/rapor.fxe")
}

// What is not a NEW encryption keeps working with the policy off (spec,
// clarification 2). None of these doors asks the rule; this pins that none of
// them starts to.
func TestE2EPolicy_WhatIsNotANewEncryptionStillWorks(t *testing.T) {
	f := newMTFix(t, false)
	ctx := context.Background()
	admin, err := f.Store.GetUserByEmail(ctx, "admin@alpha.test")
	require.NoError(t, err)
	tok := issueToken(t, f.Store, admin.ID, fullScopes, nil)
	for _, dir := range []string{"Kasa", "Arsiv"} {
		status, body := fxMutate(t, f.URL, tok, "newfolder", map[string]any{"path": "alpha://", "name": dir})
		require.Equal(t, http.StatusOK, status, body)
	}
	// Encrypted before the policy was switched off: a folder and a file.
	fxUpload(t, f.URL, tok, "alpha://Kasa", e2eKeyFile, kfOne)
	fxUpload(t, f.URL, tok, "alpha://", "rapor.pdf.fxe", fxeBody)
	encryptionOff(t, f.Store)

	// Rewriting the key file that is there: a new password and level 2 — the
	// folder is already encrypted.
	status, body := fxUploadStatus(t, f.URL, tok, "alpha://Kasa", e2eKeyFile, kfNewPw)
	require.Equal(t, http.StatusOK, status, "rewriting a key file that is there: %s", body)
	got, err := os.ReadFile(filepath.Join(f.RootA, "Kasa", e2eKeyFile))
	require.NoError(t, err)
	assert.Equal(t, kfNewPw, string(got))
	// …the agent API replacing a .fxe that is there…
	status, body = fxPost(t, f.URL+"/api/ai/upload", tok, map[string]any{"path": "alpha://rapor.pdf.fxe", "content": fxeBody + " v2"})
	require.Equal(t, http.StatusOK, status, "replacing a .fxe that is there: %s", body)
	// …a file into the encrypted folder, and an ordinary file.
	status, body = fxUploadStatus(t, f.URL, tok, "alpha://Kasa", "ek.bin", "filexe2e\x01cipher")
	require.Equal(t, http.StatusOK, status, "a file into an encrypted folder: %s", body)
	status, body = fxUploadStatus(t, f.URL, tok, "alpha://", "notlar.txt", "plain")
	require.Equal(t, http.StatusOK, status, "an ordinary file: %s", body)

	// A rename keeps what is already encrypted — onto a .fxe name too.
	status, body = fxMutate(t, f.URL, tok, "rename", map[string]any{"path": "alpha://", "item": "alpha://rapor.pdf.fxe", "name": "rapor-2027.pdf.fxe"})
	require.Equal(t, http.StatusOK, status, "renaming a .fxe: %s", body)
	// The trash gives back what it took.
	n, err := f.Store.GetNodeByPath(ctx, f.StA.ID, pathkey.Hash(f.StA.ID, "/rapor-2027.pdf.fxe"))
	require.NoError(t, err)
	status, body = fxMutate(t, f.URL, tok, "delete", map[string]any{"path": "alpha://", "items": []map[string]string{{"path": "alpha://rapor-2027.pdf.fxe"}}})
	require.Equal(t, http.StatusOK, status, body)
	status, body = fxPost(t, f.URL+"/api/files/manager/restore", tok, map[string]any{"node_id": n.ID})
	require.Equal(t, http.StatusOK, status, "restoring a .fxe from the trash: %s", body)
	assert.FileExists(t, filepath.Join(f.RootA, "rapor-2027.pdf.fxe"))

	// The queue moves a .fxe and an encrypted folder: their key file and bytes
	// travel with them. (A COPY of either is a new encryption and is asked:
	// e2e_policy_copy_test.go.)
	status, body = fxPost(t, f.URL+"/api/files/move", tok, map[string]any{"source": []string{"alpha://rapor-2027.pdf.fxe"}, "target": "alpha://Arsiv"})
	require.Equal(t, http.StatusAccepted, status, "moving a .fxe: %s", body)
	status, body = fxPost(t, f.URL+"/api/files/move", tok, map[string]any{"source": []string{"alpha://Kasa"}, "target": "alpha://Arsiv"})
	require.Equal(t, http.StatusAccepted, status, "moving an encrypted folder: %s", body)
	f.drainOps(t)
	assert.FileExists(t, filepath.Join(f.RootA, "Arsiv", "rapor-2027.pdf.fxe"))
	moved, err := os.ReadFile(filepath.Join(f.RootA, "Arsiv", "Kasa", e2eKeyFile))
	require.NoError(t, err, "the queue's move of an encrypted folder lost its key file")
	assert.Equal(t, kfNewPw, string(moved))
}

// The refusals above say what the rule stops; they cannot say WHY. A path that
// lost the person the rule judges — the context's user of an archive member in
// a request and in the queued job, the agent token's user, the actor
// CommitSibling is handed — is refused as `permission` whatever the policy, and
// from the outside that looks exactly like `off`. So under the default policy
// (`permitted`) the same creations must land: that is what pins the person
// each of these paths carries.
func TestE2EPolicy_TheActorPathsCreateWhatThePolicyPermits(t *testing.T) {
	f := newMTFix(t, false)
	ctx := context.Background()
	admin, err := f.Store.GetUserByEmail(ctx, "admin@alpha.test")
	require.NoError(t, err)
	tok := issueToken(t, f.Store, admin.ID, fullScopes, nil)
	for _, dir := range []string{"AjanCikti", "KuyrukCikti", "Uygulama"} {
		status, body := fxMutate(t, f.URL, tok, "newfolder", map[string]any{"path": "alpha://", "name": dir})
		require.Equal(t, http.StatusOK, status, body)
	}
	fxUpload(t, f.URL, tok, "alpha://", "paket.zip", string(buildZip(t, map[string]string{e2eKeyFile: kfOne, "alt/gizli.fxe": fxeBody})))
	landed := func(dir string, rels ...string) {
		t.Helper()
		for _, rel := range rels {
			assert.FileExists(t, filepath.Join(f.RootA, dir, filepath.FromSlash(rel)), "%s/%s was not extracted", dir, rel)
		}
	}

	// The agent API's unzip judges each member for the token's person: the
	// `.fxe` lands. The key file does not, whatever the policy: that surface
	// never writes one (assertKeylessRefused).
	status, body := fxPost(t, f.URL+"/api/ai/unzip", tok, map[string]any{"src": "alpha://paket.zip", "dest": "alpha://AjanCikti"})
	require.Equal(t, http.StatusOK, status, body)
	landed("AjanCikti", "alt/gizli.fxe")
	assertNotWritten(t, f, "AjanCikti/"+e2eKeyFile)

	// …and so does an extraction through the queue, for the person its worker
	// restores on the job's context.
	status, body = fxPost(t, f.URL+"/api/files/archive/extract", tok, map[string]any{"storage_id": f.StA.ID, "path": "paket.zip", "dest": "KuyrukCikti"})
	require.Equal(t, http.StatusAccepted, status, body)
	f.drainOps(t)
	landed("KuyrukCikti", e2eKeyFile, "alt/gizli.fxe")

	// An app's output is judged for the actor it is written for.
	apps := appsWithTheRule(t, f)
	for _, name := range []string{e2eKeyFile, "cikti.fxe"} {
		rel, err := apps.CommitSibling(ctx, f.StA.ID, "Uygulama", name, strings.NewReader("x"), 1, &admin.ID)
		require.NoError(t, err, "an app's output named %s, under a policy that permits it", name)
		assert.Equal(t, "Uygulama/"+name, rel)
		assert.FileExists(t, filepath.Join(f.RootA, "Uygulama", name))
	}
}

// Every reason the rule gives is said in both shipped languages. The key is
// put together at run time ("server.e2e.not_allowed." + reason), so the
// catalogue's scan for literal keys cannot see it.
func TestE2EPolicy_EveryReasonIsSaidInBothLanguages(t *testing.T) {
	en, tr := srvtext.Builtin("en"), srvtext.Builtin("tr")
	for _, reason := range []e2epolicy.Reason{
		e2epolicy.ReasonTenantDisabled, e2epolicy.ReasonPolicyOff, e2epolicy.ReasonAdminsOnly,
		e2epolicy.ReasonPermission, e2epolicy.ReasonApprovalRequired,
	} {
		key := "server.e2e.not_allowed." + string(reason)
		assert.NotEmpty(t, en[key], key)
		assert.NotEmpty(t, tr[key], key)
	}
}

// The WebDAV the router mounts asks the rule — the real construction
// (BuildRouter → dav.NewHandler), which no protocol package's own harness
// goes through. With the policy off even an administrator's PUT of a `.fxe`
// is refused, and nothing lands; an ordinary name is the control.
func TestE2EPolicy_WebDAVThroughTheRouterAsksTheRule(t *testing.T) {
	f := newStagedFixture(t)
	encryptionOff(t, f.store)
	put := func(name string) (int, string) {
		t.Helper()
		req, err := http.NewRequest(http.MethodPut, f.srv.URL+"/dav/main/"+name, strings.NewReader("x"))
		require.NoError(t, err)
		req.SetBasicAuth(f.adminEml, f.adminPw)
		resp, err := f.client.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b)
	}

	status, body := put("yeni.fxe")
	assert.Equal(t, http.StatusForbidden, status, body)
	assert.Contains(t, body, "encrypted", "the refusal is the rule's")
	assert.NoFileExists(t, filepath.Join(f.rootDir, "yeni.fxe"))
	status, body = put("notlar.txt")
	assert.Contains(t, []int{http.StatusCreated, http.StatusNoContent}, status, "an ordinary file: %s", body)
}

// The S3 endpoint the router mounts asks the rule — the real construction
// (BuildRouter → s3api.NewHandler), with a key issued by the router's own
// resolver and a real SigV4 signature. With the policy off even an
// administrator's PUT of a `.fxe` is AccessDenied, and nothing lands.
func TestE2EPolicy_S3ThroughTheRouterAsksTheRule(t *testing.T) {
	// A secret box is what lets an S3 access key be issued at all.
	f := newStagedFixtureWith(t, func(d *api.Deps) { d.Cfg.SecretKey = "e2e-policy-router-test" })
	encryptionOff(t, f.store)
	ctx := context.Background()
	admin, err := f.store.GetUser(ctx, f.userID)
	require.NoError(t, err)
	key, err := f.deps.ProtocolAuth.Issue(ctx, protocolauth.IssueRequest{User: admin, Label: "e2e"})
	require.NoError(t, err)
	put := func(name string) (int, string) {
		t.Helper()
		body := []byte("x")
		req, err := http.NewRequest(http.MethodPut, f.srv.URL+"/s3/main/"+name, bytes.NewReader(body))
		require.NoError(t, err)
		sum := sha256.Sum256(body)
		hash := hex.EncodeToString(sum[:])
		req.Header.Set("X-Amz-Content-Sha256", hash)
		signer := v4.NewSigner(func(o *v4.SignerOptions) { o.DisableURIPathEscaping = true })
		creds := aws.Credentials{AccessKeyID: key.Key.AccessKeyID, SecretAccessKey: key.Secret}
		require.NoError(t, signer.SignHTTP(ctx, creds, req, hash, "s3", "us-east-1", time.Now()))
		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b)
	}

	status, body := put("yeni.fxe")
	assert.Equal(t, http.StatusForbidden, status, body)
	assert.Contains(t, body, "<Code>AccessDenied</Code>", body)
	assert.Contains(t, body, "encrypted", "the refusal is the rule's")
	assert.NoFileExists(t, filepath.Join(f.rootDir, "yeni.fxe"))
	status, body = put("notlar.txt")
	assert.Equal(t, http.StatusOK, status, "an ordinary object: %s", body)
}
