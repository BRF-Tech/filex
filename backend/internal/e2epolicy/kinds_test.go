package e2epolicy_test

// The approval kinds are separate (operator decision 2026-10-03): an approval
// opens only the kind it was asked for, in the folder it was asked for, never
// in a folder below it - and the explorer's answer (AnswerFor) is the door's
// (CheckCreate), because both read one list of the approvals that count.
//
// ⚠ The kinds are written as strings here, not as the model's constants, so
// these tests also compile against a tree that predates the third kind: what
// they pin is behaviour.

import (
	"context"
	"path"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/e2epolicy"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/pathkey"
)

// dir catalogues a folder, as a listing would have.
func (f *fix) dir(t *testing.T, rel string) {
	t.Helper()
	_, err := f.store.CreateNode(context.Background(), &model.Node{
		StorageID: f.st.ID, Name: path.Base(rel), Path: "/" + rel, PathHash: pathkey.Hash(f.st.ID, "/"+rel),
		Type: model.NodeTypeDirectory,
	})
	require.NoError(t, err)
}

// An in-place approval of P opens P itself, where it is, and nothing else: not
// a new folder inside P, not a folder inside P that holds something.
func TestApproval_AnInPlaceApprovalOpensOnlyItsFolder(t *testing.T) {
	f := newFix(t, true)
	f.policy(t, true, model.E2EPolicyApproval)
	ctx := context.Background()
	f.dir(t, "Muhasebe")
	f.file(t, "Muhasebe/Ekip/bordro.pdf")
	r := f.approve(t, f.ada, "Muhasebe", "folder", f.now.Add(e2epolicy.ApprovalTTL))

	requireRefused(t, f.svc.CheckCreate(ctx, f.ada, f.st, "Muhasebe/Yeni/.filex-e2e.json"), e2epolicy.ReasonApprovalRequired)
	requireRefused(t, f.svc.CheckCreate(ctx, f.ada, f.st, "Muhasebe/Ekip/.filex-e2e.json"), e2epolicy.ReasonApprovalRequired)
	assert.Equal(t, model.E2ERequestApproved, f.status(t, r.ID), "a folder below spent the approval of the folder above")
	assert.Empty(t, f.used)

	require.NoError(t, f.svc.CheckCreate(ctx, f.ada, f.st, "Muhasebe/.filex-e2e.json"))
	assert.Equal(t, model.E2ERequestUsed, f.status(t, r.ID))
	assert.Equal(t, []string{"Muhasebe"}, f.opened)
}

// A new-folder approval of P opens one NEW encrypted folder directly inside P:
// never P itself, never a folder inside P that holds something (that would be
// encrypting a team's folder in place with an approval for a new one), never
// a folder further down.
func TestApproval_ANewFolderApprovalOpensOnlyANewFolder(t *testing.T) {
	f := newFix(t, true)
	f.policy(t, true, model.E2EPolicyApproval)
	ctx := context.Background()
	f.dir(t, "Muhasebe")
	f.file(t, "Muhasebe/Ekip/bordro.pdf")
	r := f.approve(t, f.ada, "Muhasebe", "new_folder", f.now.Add(e2epolicy.ApprovalTTL))

	for _, rel := range []string{
		"Muhasebe/.filex-e2e.json",
		"Muhasebe/Ekip/.filex-e2e.json",
		"Muhasebe/Yeni/Alt/.filex-e2e.json",
	} {
		requireRefused(t, f.svc.CheckCreate(ctx, f.ada, f.st, rel), e2epolicy.ReasonApprovalRequired)
	}
	assert.Equal(t, model.E2ERequestApproved, f.status(t, r.ID), "what it does not open spent it")
	assert.Empty(t, f.used)

	require.NoError(t, f.svc.CheckCreate(ctx, f.ada, f.st, "Muhasebe/Yeni/.filex-e2e.json"), "a new folder inside")
	assert.Equal(t, model.E2ERequestUsed, f.status(t, r.ID))
	assert.Equal(t, []string{"Muhasebe/Yeni"}, f.opened)
	requireRefused(t, f.svc.CheckCreate(ctx, f.ada, f.st, "Muhasebe/Yeni2/.filex-e2e.json"), e2epolicy.ReasonApprovalRequired)
}

// An empty folder that is there already is new: encrypting it makes a new
// encrypted folder, and a new-folder approval of its parent covers it.
func TestApproval_AnEmptyFolderIsANewOne(t *testing.T) {
	f := newFix(t, true)
	f.policy(t, true, model.E2EPolicyApproval)
	ctx := context.Background()
	f.dir(t, "Muhasebe")
	f.dir(t, "Muhasebe/Bos")
	f.approve(t, f.ada, "Muhasebe", "new_folder", f.now.Add(e2epolicy.ApprovalTTL))

	ans, _ := f.svc.AnswerFor(ctx, f.ada, f.st, "Muhasebe/Bos")
	assert.Equal(t, e2epolicy.AnswerAllowed, ans, "the explorer offers what the door accepts")
	require.NoError(t, f.svc.CheckCreate(ctx, f.ada, f.st, "Muhasebe/Bos/.filex-e2e.json"))
}

// The explorer's answer is the door's: for each approval and each place, the
// menu says "allowed" exactly when the create door lets the encryption
// through. Before the kinds were separate the explorer looked only at the
// folder's own approval while the door spent its parent's as well, so the
// menu said "Request encryption…" over an encryption the server would have
// let through on WebDAV.
func TestApproval_TheExplorerSaysWhatTheDoorDoes(t *testing.T) {
	cases := []struct {
		name            string
		approveAt, kind string
		ask, create     string
		wantAllowed     bool
	}{
		{"its own folder, in place", "Muhasebe", "folder", "Muhasebe", "Muhasebe/.filex-e2e.json", true},
		{"a folder inside that holds something, on the parent's in-place approval", "Muhasebe", "folder", "Muhasebe/Ekip", "Muhasebe/Ekip/.filex-e2e.json", false},
		{"a folder inside that holds something, on the parent's new-folder approval", "Muhasebe", "new_folder", "Muhasebe/Ekip", "Muhasebe/Ekip/.filex-e2e.json", false},
		{"an empty folder inside, on the parent's new-folder approval", "Muhasebe", "new_folder", "Muhasebe/Bos", "Muhasebe/Bos/.filex-e2e.json", true},
		{"a file, on its folder's file approval", "Muhasebe/rapor.pdf", "file", "Muhasebe/rapor.pdf", "Muhasebe/Zm9vYmFyYmF6cXV4.fxe", true},
		{"a file, on its folder's in-place approval", "Muhasebe", "folder", "Muhasebe/rapor.pdf", "Muhasebe/Zm9vYmFyYmF6cXV4.fxe", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newFix(t, true)
			f.policy(t, true, model.E2EPolicyApproval)
			ctx := context.Background()
			f.dir(t, "Muhasebe")
			f.dir(t, "Muhasebe/Bos")
			f.file(t, "Muhasebe/Ekip/bordro.pdf")
			f.file(t, "Muhasebe/rapor.pdf")
			f.approve(t, f.ada, c.approveAt, c.kind, f.now.Add(e2epolicy.ApprovalTTL))

			ans, _ := f.svc.AnswerFor(ctx, f.ada, f.st, c.ask)
			err := f.svc.CheckCreate(ctx, f.ada, f.st, c.create)
			assert.Equal(t, err == nil, ans == e2epolicy.AnswerAllowed,
				"the explorer says %s, the door says %v", ans, err)
			assert.Equal(t, c.wantAllowed, err == nil, "the door: %v", err)
		})
	}
}
