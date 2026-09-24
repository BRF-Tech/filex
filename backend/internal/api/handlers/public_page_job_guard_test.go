package handlers_test

// The SUBMIT-TIME GATE on the public door, measured from the outside.
//
// filex has two doors that queue an app-plugin job. The authenticated one
// (app_plugins.go: authorise -> enqueue) has always asked the full set of
// questions: is the plugin running, is the action enabled, is it reserved for
// administrators, is the storage writable, does the caller hold the ACL every
// input needs, is the folder encrypted, are the parameters small enough. The
// public one (public_api.go: enqueueAsCreator) resolved the action out of the
// RAW MANIFEST and asked only about the read-only flag -- so a stranger
// holding a link could start an action an administrator had switched off or
// reserved, with unbounded parameters, on a document the link's creator could
// no longer open. And the job then ran AS THE CREATOR.
//
// These tests exist because that gap is invisible from the authenticated side:
// every check has a passing test there, and the public door had none of them.
// Each case below therefore measures the VISITOR's answer plus the only fact
// that settles the argument -- whether a row was written.
//
// ⭐ Two rows, counted both: a refusal that skips pending_ops but still writes
// app_plugin_jobs is not a refusal, it is a leak with no worker attached. The
// census reads the tables themselves for that reason.
//
// ⚠ And what the visitor is TOLD is asserted as tightly as what is queued.
// This file's neighbour public_api_leak_test.go pins the same promise for the
// link's other answers: a stranger with a token learns nothing about how this
// instance is configured or about who may reach what.

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
	"github.com/brf-tech/filex/backend/internal/testutil"
)

// queueCensus is how many rows the two tables a submit writes are holding.
type queueCensus struct{ jobs, ops int }

// census counts them. Read before and after a refusal, an unchanged census is
// the whole assertion: nothing was queued, under any name.
func (f *appFixture) census(t *testing.T) queueCensus {
	t.Helper()
	var q queueCensus
	require.NoError(t, f.sql.QueryRow(`SELECT COUNT(*) FROM app_plugin_jobs`).Scan(&q.jobs))
	require.NoError(t, f.sql.QueryRow(`SELECT COUNT(*) FROM pending_ops`).Scan(&q.ops))
	return q
}

// seedDoc puts a document on the storage AND in the node table, which is what
// the link's anchor is resolved through (Registry.PageAnchor -> GetNode).
func (f *appFixture) seedDoc(t *testing.T, rel string) {
	t.Helper()
	f.writeFile(t, rel, "the terms")
	name := rel
	if i := strings.LastIndex(rel, "/"); i >= 0 {
		name = rel[i+1:]
	}
	_, err := f.store.CreateNode(context.Background(), &model.Node{
		StorageID: f.st.ID, Name: name, Path: "/" + rel,
		PathHash: pathkey.Hash(f.st.ID, "/"+rel), Type: model.NodeTypeFile, Size: 9, Mime: "text/plain",
	})
	require.NoError(t, err)
}

// visitorSubmit is the stranger's press of the page's one button. `data`
// steers the fixture app's page (see testdata/echo: `run` names the action the
// surface asks for, `bulk` pads its parameters); nil is the page's ordinary
// submit, the same one every other test in this package makes.
func (f *appFixture) visitorSubmit(t *testing.T, token string, data map[string]any) (int, []byte) {
	t.Helper()
	body := map[string]any{"event": "submit"}
	if data != nil {
		body["data"] = data
	}
	return doReq(t, freshClient(t), http.MethodPost, f.srv.URL+"/api/public/s/"+token+"/event", body)
}

// visitorGet is a stranger's GET on one of the link's other doors: `tail` is
// "" for the no-JS page under /s/{token} (no Accept header, so the SPA shell
// is not served -- see TestNoJSFallback_StaysForNonBrowsers) and
// "/file/pub:N" for a copy the app exposed.
func (f *appFixture) visitorGet(t *testing.T, token, tail string) (int, []byte) {
	t.Helper()
	base := f.srv.URL + "/api/public/s/" + token
	if tail == "" {
		base = f.srv.URL + "/s/" + token
	}
	return doReq(t, freshClient(t), http.MethodGet, base+tail, nil)
}

// putOverride is the administrator's switch in the app's own panel: the
// action stays in the manifest and keeps working for every other install,
// and THIS instance refuses it (or reserves it).
//
// ⚠ PutOverrides replaces the plugin's whole override set, so naming one
// action here leaves every other one at its manifest default -- which is why
// `invite` still opens links in the tests below.
func (f *appFixture) putOverride(t *testing.T, pluginID int64, row map[string]any) {
	t.Helper()
	status, raw := doReq(t, f.admin, http.MethodPut,
		f.srv.URL+"/api/admin/app-plugins/"+itoa(pluginID)+"/overrides",
		map[string]any{"actions": []map[string]any{row}})
	require.Equal(t, http.StatusOK, status, string(raw))
}

// refusalLeaksNothing asserts what a visitor-facing refusal must NOT carry.
// The words below are the ones the internals would have leaked -- the
// registry's own refusal text names the administrator, an ACL refusal on the
// authenticated door names the path -- plus whatever `secrets` the caller
// knows this instance would be giving away (its storage name, the document).
//
// ⚠ Kept apart from refusalIsMute because the two doors answer in different
// shapes and BOTH owe this half: the job door sends a sentence, the page door
// sends the bare `gone` every dead link sends (so the shell can draw its own
// screen in the visitor's language). One list of forbidden words for both --
// a second copy is how one door quietly starts leaking.
func refusalLeaksNothing(t *testing.T, raw []byte, secrets ...string) {
	t.Helper()
	low := strings.ToLower(string(raw))
	for _, w := range []string{"administrator", "disabled", "insufficient", "permission: ", "acl", "grant", "storage_id", "read_only"} {
		assert.NotContains(t, low, w, "the visitor was told how the instance is configured")
	}
	for _, sec := range secrets {
		assert.NotContains(t, low, strings.ToLower(sec), "the visitor was told a storage name or a path")
	}
}

// refusalIsMute is refusalLeaksNothing plus the other half the JOB door owes:
// it says what to DO (go back to whoever sent the link), because the person
// reading it has no account here and cannot act on anything else.
func refusalIsMute(t *testing.T, raw []byte, secrets ...string) {
	t.Helper()
	refusalLeaksNothing(t, raw, secrets...)
	var body struct{ Message string }
	require.NoError(t, json.Unmarshal(raw, &body))
	assert.Contains(t, strings.ToLower(body.Message), "ask them for a new link",
		"a person who is not a filex user must be left with something they can act on")
}

// ⭐ The administrator switched the action off; the link kept working.
//
// This is the whole bug in one case. The page asks for `upper`, the raw
// manifest still declares `upper`, and the old path queued it -- the override
// that turns an action off lives in the REGISTRY, which this door never asked.
func TestPublicPageJob_AnActionTheAdministratorDisabledQueuesNothing(t *testing.T) {
	f := newAppFixture(t, nil)
	id := f.installEcho(t)
	f.seedDoc(t, "docs/terms.txt")
	token := f.openSigningLink(t, id, "docs/terms.txt")

	f.putOverride(t, id, map[string]any{"id": "upper", "enabled": false})

	before := f.census(t)
	status, raw := f.visitorSubmit(t, token, nil)
	require.Equal(t, http.StatusConflict, status, string(raw))
	assert.Equal(t, before, f.census(t), "a refused submit queued a row")
	refusalIsMute(t, raw, "main", "docs/terms.txt")
}

// ⭐ An action that is not there at all gets the SAME answer as one that is
// switched off -- the visitor cannot tell the two apart, and so cannot map the
// instance by trying action ids.
//
// ⚠ This changed shape deliberately: the old path answered 404 "no such
// action", which is a different status AND a different sentence from the
// refusals beside it. Three distinguishable answers for three reasons a link
// can fail is three bits a stranger did not have to be given.
func TestPublicPageJob_AnUnknownActionIsRefusedLikeEveryOtherDeadLink(t *testing.T) {
	f := newAppFixture(t, nil)
	id := f.installEcho(t)
	f.seedDoc(t, "docs/terms.txt")
	token := f.openSigningLink(t, id, "docs/terms.txt")

	before := f.census(t)
	status, raw := f.visitorSubmit(t, token, map[string]any{"run": "no-such-action"})
	require.Equal(t, http.StatusConflict, status, string(raw))
	assert.Equal(t, before, f.census(t))
	refusalIsMute(t, raw, "main", "docs/terms.txt")
	assert.NotContains(t, strings.ToLower(string(raw)), "no such action")
}

// ⭐ The app behind the link was stopped.
//
// ⚠ Read this one for what it pins, not for where it is caught. A stopped
// plugin is refused THREE times over: LoadPage answers 410 before the app is
// consulted at all (public_api.go: the PIN gate and the liveness gate come
// first so a locked link cannot be used to probe which apps are running), the
// runtime has no instance to call, and ResolveAction refuses at submit. Only
// the last of those is new here, and it is unreachable through this door while
// the first stands -- so this case cannot be made red by reverting the fix
// alone. It is kept because it is the contract a visitor meets, and because
// the day the shell's gate moves, the queue must still stay empty.
func TestPublicPageJob_AStoppedAppQueuesNothing(t *testing.T) {
	f := newAppFixture(t, nil)
	id := f.installEcho(t)
	f.seedDoc(t, "docs/terms.txt")
	token := f.openSigningLink(t, id, "docs/terms.txt")

	status, raw := doReq(t, f.admin, http.MethodPatch, f.srv.URL+"/api/admin/app-plugins/"+itoa(id),
		map[string]any{"enabled": false})
	require.Equal(t, http.StatusOK, status, string(raw))

	before := f.census(t)
	status, raw = f.visitorSubmit(t, token, nil)
	assert.GreaterOrEqual(t, status, 400, string(raw))
	assert.Less(t, status, 500, string(raw))
	assert.Equal(t, before, f.census(t), "a submit on a stopped app queued a row")
}

// ⭐ An action reserved for administrators follows the LINK'S CREATOR, not
// the visitor -- and it follows them both ways.
//
// The visitor is a courier: they press a button on a screen the app drew for
// them, and the job spends the creator's rights, on the creator's storage,
// under the creator's name in the audit log. So an administrator who minted a
// link into an admin-restricted action meant it (half one), and a link a
// non-admin minted cannot become one by being handed to a stranger (half two).
// Reading the VISITOR's own admin status -- anonymous, therefore never an
// admin -- would have been the easy answer and would have broken half one.
func TestPublicPageJob_AnAdminOnlyActionFollowsTheLinksCreator(t *testing.T) {
	t.Run("minted by an administrator, it runs", func(t *testing.T) {
		f := newAppFixture(t, nil)
		id := f.installEcho(t)
		f.seedDoc(t, "docs/terms.txt")
		token := f.openSigningLink(t, id, "docs/terms.txt")
		f.putOverride(t, id, map[string]any{"id": "upper", "enabled": true, "admin_only": true})

		status, raw := f.visitorSubmit(t, token, nil)
		require.Equal(t, http.StatusAccepted, status, string(raw))
		var ans struct {
			JobID string `json:"job_id"`
		}
		require.NoError(t, json.Unmarshal(raw, &ans))
		assert.NotEmpty(t, ans.JobID)
	})

	t.Run("minted by somebody who is not, it does not", func(t *testing.T) {
		f := newAppFixture(t, nil)
		id := f.installEcho(t)
		f.seedDoc(t, "docs/terms.txt")
		client, _, _ := f.loginRegular(t, "editor", "editor")
		token := f.openSigningLinkAs(t, client, id, "docs/terms.txt")
		f.putOverride(t, id, map[string]any{"id": "upper", "enabled": true, "admin_only": true})

		before := f.census(t)
		status, raw := f.visitorSubmit(t, token, nil)
		require.Equal(t, http.StatusForbidden, status, string(raw))
		assert.Equal(t, before, f.census(t))
		refusalIsMute(t, raw, "main", "docs/terms.txt")
	})
}

// ⭐⭐ The creator's access is re-read AT SUBMIT, and a link outlives the
// grant that justified it.
//
// The consequence the product owner accepted, measured: the requester's grant
// on the folder is taken away -- they changed teams, the sharing was tidied
// up, the account was demoted -- and the links already in strangers' mailboxes
// STOP. They stop here, at the door, with a sentence the visitor can act on,
// instead of queueing a job that dies unseen in the worker with the sender's
// name on the audit row.
func TestPublicPageJob_ACreatorWhoLostAccessCanNoLongerQueue(t *testing.T) {
	f := newAppFixture(t, nil)
	id := f.installEcho(t)
	f.seedDoc(t, "docs/terms.txt")
	client, _, grant := f.loginRegular(t, "signer", "editor")
	token := f.openSigningLinkAs(t, client, id, "docs/terms.txt")

	// The link still works while the grant stands -- otherwise the case below
	// would prove nothing but that non-admins cannot use links at all.
	status, raw := f.visitorSubmit(t, token, nil)
	require.Equal(t, http.StatusAccepted, status, string(raw))

	require.NoError(t, f.store.DeleteFileGrant(context.Background(), grant))

	before := f.census(t)
	status, raw = f.visitorSubmit(t, token, nil)
	require.Equal(t, http.StatusForbidden, status, string(raw))
	assert.Equal(t, before, f.census(t), "a job was queued on a document its owner can no longer open")
	// ⚠ The words a person who is not a filex user must not meet: the name of
	// a storage they have never heard of, the path of a document they were
	// only ever shown one screen of, or the vocabulary of an ACL.
	refusalIsMute(t, raw, "main", "docs/terms.txt", "docs")
}

// ⭐ An action's own `min_role` floor is raised on this door too.
//
// It is the one demand an app AUTHOR can make of the caller — "whoever runs
// this must own the file", which a signing app puts under the step that
// finalises an envelope — and until the public door read the ACL at all there
// was nowhere for it to land. Measured against a creator who holds editor: the
// page's ordinary job (editor is enough) goes through, and the owner-only one
// from the same link, by the same person, does not.
func TestPublicPageJob_AnActionsMinRoleFloorIsRaisedForTheCreatorToo(t *testing.T) {
	f := newAppFixture(t, nil)
	id := f.installEcho(t)
	f.seedDoc(t, "docs/terms.txt")
	client, _, _ := f.loginRegular(t, "holder", "editor")
	token := f.openSigningLinkAs(t, client, id, "docs/terms.txt")

	// Editor is enough for the page's ordinary job, which writes a sibling.
	status, raw := f.visitorSubmit(t, token, nil)
	require.Equal(t, http.StatusAccepted, status, string(raw))

	before := f.census(t)
	status, raw = f.visitorSubmit(t, token, map[string]any{"run": "owned"})
	require.Equal(t, http.StatusForbidden, status, string(raw))
	assert.Equal(t, before, f.census(t), "an owner-only action was queued for an editor")
	refusalIsMute(t, raw, "main", "docs/terms.txt")
}

// ⭐⭐ A link does not outlive its creator's ACCOUNT either -- and the stop is
// a PAUSE, not a demolition.
//
// The owner's call, and it is a real trade rather than an obvious win.
// `Enabled` is explicitly NOT a soft delete elsewhere in filex ("files, quota
// and grants are untouched"), so switching an account off could equally have
// been read as leaving its links alone. It was decided the other way: somebody
// who has left must not leave an outside party's door into the company's
// documents standing open. The accepted cost is the other half -- a
// temporarily disabled account's signature flows stop and start again -- and
// THAT is what the last third of this test measures, because a decision taken
// on the promise of reversibility is worth nothing if the reversal is untested.
//
// ⚠ FIVE doors, one reading (linkCreator, public_api.go): the state the shell
// renders, the surface the visitor presses, the job those presses queue, the
// bytes of the exposed copy, and the no-JS page that lists it. Every one of
// them is measured here, because a link that is dead in four of them and alive
// in the fifth is worse than a link that is simply dead — and the fifth is
// the one that would have survived, since it reads the copies straight off the
// share row and needs neither the app nor the ACL.
func TestPublicPageLink_ADisabledCreatorPausesTheLinkAndResumesIt(t *testing.T) {
	f := newAppFixture(t, nil)
	id := f.installEcho(t)
	f.seedDoc(t, "docs/terms.txt")
	client, creator, _ := f.loginRegular(t, "leaver", "editor")
	token := f.openSigningLinkAs(t, client, id, "docs/terms.txt")
	ctx := context.Background()

	// The link works while the account does -- every door of it -- otherwise
	// everything below would pass on a link that never worked at all.
	status, raw := f.visitorSubmit(t, token, nil)
	require.Equal(t, http.StatusAccepted, status, string(raw))
	status, raw = f.visitorGet(t, token, "/file/pub:0")
	require.Equal(t, http.StatusOK, status, string(raw))
	require.Contains(t, string(raw), "the terms", "the exposed copy is the document the signer was sent")

	require.NoError(t, f.store.SetUserEnabled(ctx, creator.ID, false))

	// 1. The SHELL calls it dead, so the visitor meets the dead-link screen
	//    instead of a form. `revoked`, not `expired`: nothing ran out.
	status, raw = doReq(t, freshClient(t), http.MethodGet, f.srv.URL+"/api/public/s/"+token, nil)
	require.Equal(t, http.StatusOK, status, string(raw))
	var state struct {
		Kind     string `json:"kind"`
		Revoked  bool   `json:"revoked"`
		Expired  bool   `json:"expired"`
		NeedsPIN bool   `json:"needs_pin"`
	}
	require.NoError(t, json.Unmarshal(raw, &state))
	assert.Equal(t, "app", state.Kind)
	assert.True(t, state.Revoked, "the shell still calls a dead link live")
	assert.False(t, state.Expired, "this is not the clock and must not be reported as it")

	// 2. The SURFACE refuses to open. This is the half that matters beyond
	//    tidiness: `page_event` runs the app, and a signing app writes its
	//    record (page state, `signed_by`) BEFORE it asks for the job -- so a
	//    gate that waited for the submit would let a dead link record that
	//    the visitor signed.
	before := f.census(t)
	status, raw = doReq(t, freshClient(t), http.MethodPost, f.srv.URL+"/api/public/s/"+token+"/event",
		map[string]any{"event": "open"})
	require.Equal(t, http.StatusGone, status, string(raw))
	refusalLeaksNothing(t, raw, "main", "docs/terms.txt", "leaver@test.local")

	// 2b. ⭐ AND THE DOCUMENT WITH IT. A surface that refuses while this route
	//     still hands over the exposed copy is a closed door beside an open
	//     window: that copy IS what the outside participant was sent.
	status, raw = f.visitorGet(t, token, "/file/pub:0")
	assert.Equal(t, http.StatusGone, status, string(raw))
	assert.NotContains(t, string(raw), "the terms", "the document was handed over anyway")

	// 2c. And the no-JS page, which lists those copies straight off the share
	//     row -- it needs neither the running app nor the ACL, so it is the one
	//     surface that would otherwise have survived every other gate.
	status, raw = f.visitorGet(t, token, "")
	assert.Equal(t, http.StatusNotFound, status)
	assert.NotContains(t, string(raw), "pub:0", "the dead page still offered the document")

	// 3. And a submit queues nothing, in either table.
	status, raw = f.visitorSubmit(t, token, nil)
	assert.Equal(t, http.StatusGone, status, string(raw))
	refusalLeaksNothing(t, raw, "main", "docs/terms.txt", "leaver@test.local")
	assert.Equal(t, before, f.census(t), "a link whose creator is switched off still queued a job")

	// 4. ⭐ Switch the account back on and the SAME link works again -- no new
	//    link, no re-invite, nothing about the share rewritten while it was
	//    down. This is the assertion the owner's decision rests on.
	require.NoError(t, f.store.SetUserEnabled(ctx, creator.ID, true))
	status, raw = f.visitorSubmit(t, token, nil)
	require.Equal(t, http.StatusAccepted, status, string(raw))
	status, raw = f.visitorGet(t, token, "/file/pub:0")
	require.Equal(t, http.StatusOK, status, string(raw))
	assert.Contains(t, string(raw), "the terms", "the document did not come back with the account")
	after := f.census(t)
	assert.Equal(t, before.jobs+1, after.jobs, "the resumed link did not queue its job")
	assert.Equal(t, before.ops+1, after.ops, "the resumed link did not queue its ops row")
}

// ⭐ The parameters a page may queue are capped at the SAME ceiling the
// authenticated door uses -- the anonymous door was the generous one.
//
// ⚠ Measured on the final bytes: the surface's override, `share_id` and
// `page_token_hash` are stamped in before the column has to hold them, so a
// page that sits just under the line on its own params still crosses it.
func TestPublicPageJob_ParamsOverTheCeilingAre413(t *testing.T) {
	f := newAppFixture(t, nil)
	id := f.installEcho(t)
	f.seedDoc(t, "docs/terms.txt")
	token := f.openSigningLink(t, id, "docs/terms.txt")

	// Comfortably under: the ceiling is a ceiling, not a blanket refusal of
	// anything a page cares to send.
	status, raw := f.visitorSubmit(t, token, map[string]any{"bulk": 32 * 1024})
	require.Equal(t, http.StatusAccepted, status, string(raw))

	before := f.census(t)
	status, raw = f.visitorSubmit(t, token, map[string]any{"bulk": 80 * 1024})
	require.Equal(t, http.StatusRequestEntityTooLarge, status, string(raw))
	assert.Equal(t, before, f.census(t), "an oversized submit still wrote its row")
}

// loginRegular seeds a non-admin account, grants it `level` on `docs/`, logs it
// in on its own cookie jar and returns (client, the account, grant id) -- the
// account because the tests below take it away in both of the ways a creator
// can stop being able to stand behind a link: the grant is deleted, or the
// account itself is switched off.
//
// ⚠ A separate jar, not the admin's: two principals in one jar is one
// principal, and the whole point of these tests is WHICH of them opened the
// link.
func (f *appFixture) loginRegular(t *testing.T, name, level string) (*http.Client, *model.User, int64) {
	t.Helper()
	email, pw := name+"@test.local", "TestUserPass!1"
	testutil.SeedRegularUser(t, f.store, email, pw)
	u, err := f.store.GetUserByEmail(context.Background(), email)
	require.NoError(t, err)
	require.NotNil(t, u)
	g, err := f.store.CreateFileGrant(context.Background(), &model.FileGrant{
		StorageID: f.st.ID, PathPrefix: "docs", IsDir: true, UserID: u.ID, Level: level,
	})
	require.NoError(t, err)
	client := freshClient(t)
	testutil.LoginAs(t, f.srv, client, email, pw)
	return client, u, g.ID
}
