package handlers_test

// Who may encrypt (internal/e2epolicy) at the HTTP doors that give an item a
// new name: the explorer's rename (and the queue's rename it hands a folder
// to), the queue's copy and move under a name of the caller's choosing
// (POST /api/files/copy and /move with `name`, POST /api/files/ops with a
// literal destination), and the agent's move (POST /api/ai/move, the MCP
// file_move tool).
//
// Giving a plain FILE a key file's (`.filex-e2e.json`) or a `.fxe`'s name
// encrypts as surely as creating one — upload rapor.bin, rename it
// rapor.bin.fxe — so such a rename, move or copy is asked exactly as a create
// at its destination would be (operator decision 2026-09-30). So is every
// other landing on one of the names but three (operator decision 2026-10-03,
// e2epolicy.RelocationEncrypts): a folder under any name, a `.fxe` that stays
// a `.fxe`, and a key file that stays its own folder's. A `.fxe` given the key
// file's name, or a key file moved into another folder, encrypts a folder
// nobody was asked about.
//
// The agent's move is the exception for the key file's name: a surface that
// holds no key never puts anything there, a folder included (0.50,
// syspath.Keyless), and says RESERVED_NAME before the rule is asked. Its
// `.fxe` cases are the rule's like every other door's.
//
// None of these doors replaces what holds its destination: a rename refuses a
// taken name, a move or a copy lands on a free name beside it. And beside a
// `.fxe` that free name is `…-copy.fxe`, a new `.fxe` all the same — so a
// taken `.fxe` name is still asked.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/e2epolicy"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/ops"
	"github.com/brf-tech/filex/backend/internal/testutil/dbtest"
)

// renameDoor is one HTTP door that gives an item a new name. do asks for src
// to become dst (both paths in alpha) and returns the answer; a door that
// queues the work has run it by the time do returns.
type renameDoor struct {
	name string
	// inPlace: the door renames within the item's own folder only.
	inPlace bool
	// copies: the source stays where it was.
	copies bool
	// mcp: the door is a tool, and answers with a tool result.
	mcp bool
	// keyless: the door holds no key (the agent's surface) and refuses a key
	// file's name itself (reserved).
	keyless bool
	do      func(t *testing.T, f *mtFix, tok, src, dst string) (int, string)
}

// afterQueue runs the queue's worker once a door has queued its work.
func afterQueue(t *testing.T, f *mtFix, status int, body string) (int, string) {
	t.Helper()
	if status == http.StatusAccepted {
		f.drainOps(t)
	}
	return status, body
}

func renameDoors() []renameDoor {
	rename := func(action string) func(t *testing.T, f *mtFix, tok, src, dst string) (int, string) {
		return func(t *testing.T, f *mtFix, tok, src, dst string) (int, string) {
			status, body := fxMutate(t, f.URL, tok, action, map[string]any{
				"path": "alpha://" + path.Dir(src), "item": "alpha://" + src, "name": path.Base(dst),
			})
			return afterQueue(t, f, status, body)
		}
	}
	perVerb := func(verb string) func(t *testing.T, f *mtFix, tok, src, dst string) (int, string) {
		return func(t *testing.T, f *mtFix, tok, src, dst string) (int, string) {
			status, body := fxPost(t, f.URL+"/api/files/"+verb, tok, map[string]any{
				"source": []string{"alpha://" + src}, "target": "alpha://" + path.Dir(dst), "name": path.Base(dst),
			})
			return afterQueue(t, f, status, body)
		}
	}
	literal := func(kind string) func(t *testing.T, f *mtFix, tok, src, dst string) (int, string) {
		return func(t *testing.T, f *mtFix, tok, src, dst string) (int, string) {
			status, body := fxPost(t, f.URL+"/api/files/ops", tok, map[string]any{
				"kind": kind, "storage_id": f.StA.ID, "sources": []string{src}, "dest": dst,
			})
			return afterQueue(t, f, status, body)
		}
	}
	return []renameDoor{
		{name: "the explorer's rename", inPlace: true, do: rename("rename")},
		// A folder's rename is asked with queued=1 and runs as a job of the
		// queue (ops.OpRename); nothing else queues one.
		{name: "the explorer's queued rename", inPlace: true, do: rename("rename&queued=1")},
		{name: "the queue's move with a name", do: perVerb("move")},
		{name: "the queue's copy with a name", copies: true, do: perVerb("copy")},
		{name: "a move to a literal path", do: literal("move")},
		{name: "a copy to a literal path", copies: true, do: literal("copy")},
		{name: "the agent's move", keyless: true, do: func(t *testing.T, f *mtFix, tok, src, dst string) (int, string) {
			return fxPost(t, f.URL+"/api/ai/move", tok, map[string]any{"src": "alpha://" + src, "dst": "alpha://" + dst})
		}},
		{name: "the MCP file_move tool", mcp: true, keyless: true, do: func(t *testing.T, f *mtFix, tok, src, dst string) (int, string) {
			call, err := json.Marshal(map[string]any{
				"jsonrpc": "2.0", "id": 1, "method": "tools/call",
				"params": map[string]any{"name": "file_move", "arguments": map[string]any{"src": "alpha://" + src, "dst": "alpha://" + dst}},
			})
			require.NoError(t, err)
			return mcpPost(t, &http.Client{}, f.URL+"/api/ai/mcp", tok, string(call))
		}},
	}
}

// refused: the rule's no, in the door's own form — 403 e2e_not_allowed for
// the policy, or a tool's error result naming the reason.
func (d renameDoor) refused(t *testing.T, what string, status int, body string) {
	t.Helper()
	if d.mcp {
		assert.Equal(t, http.StatusOK, status, "%s: %s", what, body)
		assert.Contains(t, body, `"isError":true`, "%s was let through: %s", what, body)
		assert.Contains(t, body, string(e2epolicy.ReasonPolicyOff), "%s: the refusal names its reason: %s", what, body)
		return
	}
	assertE2ERefused(t, what, status, body)
}

// reserved: a keyless door's refusal of a key file's name, in its own form -
// 403 RESERVED_NAME, or a tool's error result naming it.
func (d renameDoor) reserved(t *testing.T, what string, status int, body string) {
	t.Helper()
	if d.mcp {
		assert.Equal(t, http.StatusOK, status, "%s: %s", what, body)
		assert.Contains(t, body, `"isError":true`, "%s was let through: %s", what, body)
		assert.Contains(t, body, "RESERVED_NAME", "%s: %s", what, body)
		return
	}
	assertKeylessRefused(t, what, status, body)
}

// allowed: the door went ahead.
func (d renameDoor) allowed(t *testing.T, what string, status int, body string) {
	t.Helper()
	assert.Less(t, status, 300, "%s: %s", what, body)
	if d.mcp {
		assert.NotContains(t, body, `"isError":true`, "%s: %s", what, body)
	}
}

// layRenames puts in dir what the cases of one door need, while encryption is
// still allowed: plain files, `.fxe`s and an encrypted folder (with a folder
// inside it) made before the policy was switched off, and a folder to give a
// key file's name.
func layRenames(t *testing.T, f *mtFix, tok, dir string, inPlace bool) {
	t.Helper()
	for _, sub := range []string{dir, dir + "/Klasör", dir + "/Dosyalar", dir + "/Kasa", dir + "/Kasa/Alt"} {
		status, body := fxMutate(t, f.URL, tok, "newfolder", map[string]any{"path": "alpha://" + path.Dir(sub), "name": path.Base(sub)})
		require.Equal(t, http.StatusOK, status, "%s: %s", sub, body)
	}
	// The sources of the key-file cases sit in the key file's folder for a
	// door that renames in place, beside it for a door that moves; so does
	// the file for the encrypted folder.
	mjson, ek := dir, dir
	if inPlace {
		mjson, ek = dir+"/Klasör", dir+"/Kasa"
	}
	for _, file := range []struct{ dir, name, body string }{
		{dir, "notlar.txt", "plain"},
		{dir, "rapor.bin", "plain"},
		{mjson, "m.json", "{}"},
		{mjson, "c.fxe", fxeBody},
		{dir, "a.fxe", fxeBody},
		{dir + "/Dosyalar", "not.txt", "plain"},
		{dir + "/Kasa", e2eKeyFile, kfOne},
		{ek, "ek.bin", "plain"},
		{dir, "eski.fxe", fxeBody},
		{dir, "yedek.bin", "plain"},
	} {
		fxUpload(t, f.URL, tok, "alpha://"+file.dir, file.name, file.body)
	}
}

// inAlpha is rel's path under alpha's root.
func inAlpha(f *mtFix, rel string) string { return filepath.Join(f.RootA, filepath.FromSlash(rel)) }

// With the policy off nobody may start an encryption, administrators included:
// every door that renames refuses to give a plain file a key file's or a
// `.fxe`'s name, a `.fxe` a key file's, or a key file another folder (or, in
// place, a `.fxe`'s name), and nothing moves. What is not a new encryption
// goes on: a `.fxe` that stays one, a folder under a key file's name, a plain
// file under its own name.
func TestE2EPolicy_GivingAPlainFileAnEncryptionNameAsksTheRule(t *testing.T) {
	f := newMTFix(t, false)
	admin, err := f.Store.GetUserByEmail(context.Background(), "admin@alpha.test")
	require.NoError(t, err)
	tok := issueToken(t, f.Store, admin.ID, fullScopes, nil)
	doors := renameDoors()
	for i, d := range doors {
		layRenames(t, f, tok, fmt.Sprintf("k%d", i), d.inPlace)
	}
	encryptionOff(t, f.Store)

	for i, d := range doors {
		t.Run(d.name, func(t *testing.T) {
			dir := fmt.Sprintf("k%d", i)
			at := func(rel string) string { return dir + "/" + rel }

			// An ordinary new name: the control.
			status, body := d.do(t, f, tok, at("notlar.txt"), at("notlar-2.txt"))
			d.allowed(t, "a plain name", status, body)
			assert.FileExists(t, inAlpha(f, at("notlar-2.txt")))

			// A plain file given a `.fxe`'s name, and one given a key file's
			// name in another folder (in its own folder, for a rename in place).
			// A `.fxe` given a key file's name is no different, and nor is a
			// key file that leaves its folder: Kasa's moved into Kasa/Alt
			// would encrypt Alt. A rename in place cannot take it out of its
			// folder; renamed a `.fxe`, it is a new `.fxe` all the same.
			mjson, cfxe := at("m.json"), at("c.fxe")
			keyFile := struct{ what, src, dst string }{"Kasa's key file moved into Kasa/Alt", at("Kasa/" + e2eKeyFile), at("Kasa/Alt/" + e2eKeyFile)}
			if d.inPlace {
				mjson, cfxe = at("Klasör/m.json"), at("Klasör/c.fxe")
				keyFile = struct{ what, src, dst string }{"Kasa's key file renamed k.fxe", at("Kasa/" + e2eKeyFile), at("Kasa/k.fxe")}
			}
			for _, c := range []struct{ what, src, dst string }{
				{"rapor.bin renamed rapor.bin.fxe", at("rapor.bin"), at("rapor.bin.fxe")},
				{"m.json renamed Klasör/" + e2eKeyFile, mjson, at("Klasör/" + e2eKeyFile)},
				{"c.fxe renamed Klasör/" + e2eKeyFile, cfxe, at("Klasör/" + e2eKeyFile)},
				keyFile,
			} {
				status, body := d.do(t, f, tok, c.src, c.dst)
				if d.keyless && path.Base(c.dst) == e2eKeyFile {
					d.reserved(t, c.what, status, body)
				} else {
					d.refused(t, c.what, status, body)
				}
				assert.FileExists(t, inAlpha(f, c.src), "%s: the source moved", c.what)
				assertNotWritten(t, f, c.dst)
			}

			// A `.fxe` is encrypted already: a new name for it encrypts nothing.
			// Its COPY is a second `.fxe`, a new encryption (operator decision
			// 2026-10-03, e2e_policy_copy_test.go).
			status, body = d.do(t, f, tok, at("a.fxe"), at("b.fxe"))
			if d.copies {
				d.refused(t, "a.fxe copied as b.fxe", status, body)
				assertNotWritten(t, f, at("b.fxe"))
			} else {
				d.allowed(t, "a.fxe renamed b.fxe", status, body)
				assert.FileExists(t, inAlpha(f, at("b.fxe")))
				assert.NoFileExists(t, inAlpha(f, at("a.fxe")), "a.fxe was not moved")
			}

			// A plain file keeps its plain name in an encrypted folder: adding
			// to one is not the rule's business. A rename in place renames it
			// there; a move or a copy INTO one is the transfer guard's, which
			// refuses plaintext into an encrypted folder as it always has.
			if d.inPlace {
				status, body = d.do(t, f, tok, at("Kasa/ek.bin"), at("Kasa/ek-2.bin"))
				d.allowed(t, "a plain file renamed in an encrypted folder", status, body)
				assert.FileExists(t, inAlpha(f, at("Kasa/ek-2.bin")))
			} else {
				status, body = d.do(t, f, tok, at("ek.bin"), at("Kasa/ek.bin"))
				assert.NotContains(t, body, "e2e_not_allowed", "the rule answered a plain name: %s", body)
				assert.NotContains(t, body, string(e2epolicy.ReasonPolicyOff), "the rule answered a plain name: %s", body)
				assert.Contains(t, body, "into an encrypted folder", "not the transfer guard's answer: %d %s", status, body)
				assert.FileExists(t, inAlpha(f, at("ek.bin")))
			}

			// Onto a `.fxe` that is there. A rename refuses a taken name; a
			// move or a copy would land beside it as eski-copy.fxe — a new
			// `.fxe` — so it is asked, and refused.
			status, body = d.do(t, f, tok, at("yedek.bin"), at("eski.fxe"))
			if d.inPlace {
				assert.Equal(t, http.StatusConflict, status, body)
				assert.Contains(t, body, "NAME_TAKEN")
			} else {
				d.refused(t, "yedek.bin onto the taken eski.fxe", status, body)
				assertNotWritten(t, f, at("eski-copy.fxe"))
			}
			assert.FileExists(t, inAlpha(f, at("yedek.bin")), "yedek.bin moved")
			got, err := os.ReadFile(inAlpha(f, at("eski.fxe")))
			require.NoError(t, err)
			assert.Equal(t, fxeBody, string(got), "eski.fxe was replaced")

			// A folder named like a key file is not one; what it holds is not
			// asked either. Last: the catalogue reads a folder with the name as
			// a key file, and the cases above must not stand in one. A keyless
			// door refuses the name itself, folder or not.
			status, body = d.do(t, f, tok, at("Dosyalar"), at(e2eKeyFile))
			if d.keyless {
				d.reserved(t, "a folder renamed "+e2eKeyFile, status, body)
				assert.DirExists(t, inAlpha(f, at("Dosyalar")), "the folder moved")
				assert.NoDirExists(t, inAlpha(f, at(e2eKeyFile)))
				return
			}
			d.allowed(t, "a folder renamed "+e2eKeyFile, status, body)
			fi, err := os.Stat(inAlpha(f, at(e2eKeyFile)))
			require.NoError(t, err, "the folder did not arrive")
			assert.True(t, fi.IsDir(), "%s is not the folder", e2eKeyFile)
			assert.FileExists(t, inAlpha(f, at(e2eKeyFile+"/not.txt")))
		})
	}
}

// Under the default policy (permitted) a plain member holding files.encrypt
// renames a file into a `.fxe` — by the explorer's rename, the queue's copy
// under a name and the agent's move alike. An upgrade changes nobody's access.
func TestE2EPolicy_ARenameOntoAnEncryptionNameGoesThroughWherePermitted(t *testing.T) {
	f := newMTFix(t, false)
	tok := issueToken(t, f.Store, f.UserA, "read,write", nil)
	status, body := fxMutate(t, f.URL, tok, "newfolder", map[string]any{"path": "alpha://", "name": "Izinli"})
	require.Equal(t, http.StatusOK, status, body)
	for _, name := range []string{"rapor.bin", "kopya.bin", "ajan.bin"} {
		fxUpload(t, f.URL, tok, "alpha://Izinli", name, "plain")
	}

	status, body = fxMutate(t, f.URL, tok, "rename", map[string]any{"path": "alpha://Izinli", "item": "alpha://Izinli/rapor.bin", "name": "rapor.bin.fxe"})
	require.Equal(t, http.StatusOK, status, "the explorer's rename: %s", body)
	assert.FileExists(t, inAlpha(f, "Izinli/rapor.bin.fxe"))
	assert.NoFileExists(t, inAlpha(f, "Izinli/rapor.bin"))

	status, body = fxPost(t, f.URL+"/api/files/copy", tok, map[string]any{
		"source": []string{"alpha://Izinli/kopya.bin"}, "target": "alpha://Izinli", "name": "kopya.fxe",
	})
	require.Equal(t, http.StatusAccepted, status, "the queue's copy: %s", body)
	f.drainOps(t)
	assert.FileExists(t, inAlpha(f, "Izinli/kopya.fxe"))

	status, body = fxPost(t, f.URL+"/api/ai/move", tok, map[string]any{"src": "alpha://Izinli/ajan.bin", "dst": "alpha://Izinli/ajan.fxe"})
	require.Equal(t, http.StatusOK, status, "the agent's move: %s", body)
	assert.FileExists(t, inAlpha(f, "Izinli/ajan.fxe"))
}

// Under the approval policy one approval for a folder lets exactly one rename
// make its key file: the request turns used, and the next key file — one
// folder down, which the same approval would have covered — is refused.
func TestE2EPolicy_ARenameSpendsAnApprovalOnce(t *testing.T) {
	f := newMTFix(t, false)
	ctx := context.Background()
	tok := issueToken(t, f.Store, f.UserA, "read,write", nil)
	for _, dir := range []string{"Acik", "Acik/Alt"} {
		status, body := fxMutate(t, f.URL, tok, "newfolder", map[string]any{"path": "alpha://" + path.Dir(dir), "name": path.Base(dir)})
		require.Equal(t, http.StatusOK, status, body)
	}
	fxUpload(t, f.URL, tok, "alpha://Acik", "m.json", kfOne)
	fxUpload(t, f.URL, tok, "alpha://Acik/Alt", "n.json", kfOne)

	require.NoError(t, f.Store.UpsertSetting(ctx, model.SettingE2EPolicy, model.E2EPolicyApproval))
	r := dbtest.ApproveE2E(t, f.Store, f.UserA, f.StA.ID, "Acik", model.E2ERequestFolder)
	status, body := fxMutate(t, f.URL, tok, "rename", map[string]any{"path": "alpha://Acik", "item": "alpha://Acik/m.json", "name": e2eKeyFile})
	require.Equal(t, http.StatusOK, status, "the approved rename: %s", body)
	assert.FileExists(t, inAlpha(f, "Acik/"+e2eKeyFile))
	assert.Equal(t, model.E2ERequestUsed, dbtest.E2EStatus(t, f.Store, r.ID), "the approval was not spent")

	status, body = fxMutate(t, f.URL, tok, "rename", map[string]any{"path": "alpha://Acik/Alt", "item": "alpha://Acik/Alt/n.json", "name": e2eKeyFile})
	assert.Equal(t, http.StatusForbidden, status, "a second key file on one approval: %s", body)
	var got struct{ Error, Reason string }
	_ = json.Unmarshal([]byte(body), &got)
	assert.Equal(t, "e2e_not_allowed", got.Error, body)
	assert.Equal(t, string(e2epolicy.ReasonApprovalRequired), got.Reason, body)
	assert.FileExists(t, inAlpha(f, "Acik/Alt/n.json"))
	assertNotWritten(t, f, "Acik/Alt/"+e2eKeyFile)
}

// A queued rename, move or copy of a FOLDER onto an encryption name asks
// nothing: a folder is no key file. A file that takes the folder's place
// before the queue runs the job would land as that name unasked, so the
// worker refuses it: the door told the queue which sources it settled
// (ops.WithEncryptionSettled), and the folder was not one. What a door asked
// the rule about still runs (TestE2EPolicy_ARenameOntoAnEncryptionNameGoesThroughWherePermitted).
func TestE2EPolicy_AQueuedFolderTurnedFileIsRefusedWhenTheJobRuns(t *testing.T) {
	f := newMTFix(t, false)
	ctx := context.Background()
	admin, err := f.Store.GetUserByEmail(ctx, "admin@alpha.test")
	require.NoError(t, err)
	tok := issueToken(t, f.Store, admin.ID, fullScopes, nil)
	for _, dir := range []string{"Kuyruk", "Kuyruk/A", "Kuyruk/B", "Kuyruk/C"} {
		status, body := fxMutate(t, f.URL, tok, "newfolder", map[string]any{"path": "alpha://" + path.Dir(dir), "name": path.Base(dir)})
		require.Equal(t, http.StatusOK, status, "%s: %s", dir, body)
	}
	encryptionOff(t, f.Store)

	// Three doors queue a folder onto an encryption name, and none asks.
	status, body := fxMutate(t, f.URL, tok, "rename&queued=1", map[string]any{"path": "alpha://Kuyruk", "item": "alpha://Kuyruk/A", "name": "a.fxe"})
	require.Equal(t, http.StatusAccepted, status, "the explorer's queued rename: %s", body)
	status, body = fxPost(t, f.URL+"/api/files/move", tok, map[string]any{"source": []string{"alpha://Kuyruk/B"}, "target": "alpha://Kuyruk", "name": "b.fxe"})
	require.Equal(t, http.StatusAccepted, status, "the queue's move with a name: %s", body)
	status, body = fxPost(t, f.URL+"/api/files/ops", tok, map[string]any{
		"kind": "copy", "storage_id": f.StA.ID, "sources": []string{"Kuyruk/C"}, "dest": "Kuyruk/" + e2eKeyFile,
	})
	require.Equal(t, http.StatusAccepted, status, "a copy to a literal path: %s", body)

	// Before the queue gets to them, each folder becomes a file of its name.
	for _, src := range []string{"Kuyruk/A", "Kuyruk/B", "Kuyruk/C"} {
		require.NoError(t, os.RemoveAll(inAlpha(f, src)))
		require.NoError(t, os.WriteFile(inAlpha(f, src), []byte(kfOne), 0o644))
	}
	f.drainOps(t)

	for _, c := range []struct{ src, dst string }{{"Kuyruk/A", "Kuyruk/a.fxe"}, {"Kuyruk/B", "Kuyruk/b.fxe"}, {"Kuyruk/C", "Kuyruk/" + e2eKeyFile}} {
		assert.NoFileExists(t, inAlpha(f, c.dst), "%s landed unasked", c.dst)
		assert.FileExists(t, inAlpha(f, c.src), "%s left its place", c.src)
	}
	list, err := f.Ops.List(ctx, "")
	require.NoError(t, err)
	refused := 0
	for _, op := range list {
		if op.Status == ops.StatusFailed && strings.Contains(op.Error, "queue it again") {
			refused++
		}
	}
	assert.Equal(t, 3, refused, "not every job was refused in words: %+v", list)
}
