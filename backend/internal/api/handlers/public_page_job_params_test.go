package handlers_test

// The documented contract for a job a public page asks for
// (docs/APP-PLUGINS-API.md):
//
//	"A surface carrying `job` is queued on that document as the link's
//	 creator (their ACL, their storage), with `params.page_token_hash`
//	 added; the visitor gets 202 {"accepted": true, "job_id"} and never
//	 sees the ops row."
//
// The host was adding only `share_id`. That parameter says WHICH LINK, not
// WHICH VISITOR: the job runs as the link's creator, so without the hash an
// outside signer's submission arrives looking like the requester's, and the
// signing app answers "you are not one of this document's signers".

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/pathkey"
)

// openSigningLink runs the fixture app's link-opening action on rel and
// returns the token of the share it minted. `pin: "-"` asks the fixture for a
// link with no PIN, so the visitor below walks straight in.
func (f *appFixture) openSigningLink(t *testing.T, pluginID int64, rel string) string {
	t.Helper()
	return f.openSigningLinkAs(t, f.admin, pluginID, rel)
}

// openSigningLinkAs is openSigningLink minted by a NAMED principal.
//
// ⚠ The creator is not a detail of the fixture: the job a visitor's submit
// asks for runs as whoever opened the link, with their ACL and their admin
// status (public_api.go enqueueAsCreator), so a test about those two facts has
// to be able to hand the link to somebody other than the seeded administrator.
// Draining stays on the admin client because polling an ops row is not part of
// what is being measured.
func (f *appFixture) openSigningLinkAs(t *testing.T, client *http.Client, pluginID int64, rel string) string {
	t.Helper()
	ctx := context.Background()
	before, _, err := f.store.ListAppPluginShares(ctx, pluginID, false, 50, 0)
	require.NoError(t, err)

	status, raw := doReq(t, client, http.MethodPost, f.srv.URL+"/api/files/plugins/actions/echo/invite/run",
		map[string]any{"paths": []string{"main://" + rel}, "params": map[string]any{"pin": "-"}})
	require.Equal(t, http.StatusAccepted, status, string(raw))
	var ans struct {
		Op struct {
			ID int64 `json:"id"`
		} `json:"op"`
	}
	require.NoError(t, json.Unmarshal(raw, &ans))
	op := f.drain(t, ans.Op.ID)
	require.Equal(t, "ok", op["status"], op)

	after, _, err := f.store.ListAppPluginShares(ctx, pluginID, false, 50, 0)
	require.NoError(t, err)
	require.Len(t, after, len(before)+1, "the action did not open a link")
	// Newest first is not promised, so the one that is new is found by id.
	seen := map[int64]bool{}
	for _, s := range before {
		seen[s.Share.ID] = true
	}
	for _, s := range after {
		if !seen[s.Share.ID] {
			return s.Share.Token
		}
	}
	t.Fatal("no new share")
	return ""
}

// submitAsVisitor is the stranger's POST: no account, no cookie but the
// link's own, and the surface it gets back asks for a job.
func (f *appFixture) submitAsVisitor(t *testing.T, token string) string {
	t.Helper()
	status, raw := doReq(t, freshClient(t), http.MethodPost,
		f.srv.URL+"/api/public/s/"+token+"/event", map[string]any{"event": "submit"})
	require.Equal(t, http.StatusAccepted, status, string(raw))
	var out struct {
		Accepted bool   `json:"accepted"`
		JobID    string `json:"job_id"`
	}
	require.NoError(t, json.Unmarshal(raw, &out))
	assert.True(t, out.Accepted)
	require.NotEmpty(t, out.JobID)
	return out.JobID
}

func TestPublicPageJob_CarriesTheTokenHashSoTheAppKnowsWhoIsActing(t *testing.T) {
	f := newAppFixture(t, nil)
	id := f.installEcho(t)
	ctx := context.Background()

	for _, rel := range []string{"docs/one.txt", "docs/two.txt"} {
		f.writeFile(t, rel, "the terms")
		_, err := f.store.CreateNode(ctx, &model.Node{
			StorageID: f.st.ID, Name: strings.TrimPrefix(rel, "docs/"), Path: "/" + rel,
			PathHash: pathkey.Hash(f.st.ID, "/"+rel), Type: model.NodeTypeFile, Size: 9, Mime: "text/plain",
		})
		require.NoError(t, err)
	}

	// Two links, as a request with two outside signers has.
	tokenA := f.openSigningLink(t, id, "docs/one.txt")
	tokenB := f.openSigningLink(t, id, "docs/two.txt")
	require.NotEqual(t, tokenA, tokenB)

	params := func(token string) (map[string]any, string) {
		jobID := f.submitAsVisitor(t, token)
		job, err := f.store.GetAppPluginJob(ctx, jobID)
		require.NoError(t, err)
		var p map[string]any
		require.NoError(t, json.Unmarshal([]byte(job.ParamsJSON), &p))
		return p, job.ParamsJSON
	}

	pa, rawA := params(tokenA)
	pb, _ := params(tokenB)

	// ⭐ The documented parameter is there, and it is the hash a plugin that
	// kept sha256(token) when it minted the link will recognise.
	assert.Equal(t, hash(tokenA), pa["page_token_hash"], "the job does not say which visitor acted")
	assert.Equal(t, hash(tokenB), pb["page_token_hash"])

	// ⭐ It IDENTIFIES the visitor: two links on the same app carry two
	// different hashes, so a submission cannot be mistaken for the other
	// signer's — which is the whole reason the app asks for it.
	assert.NotEqual(t, pa["page_token_hash"], pb["page_token_hash"])

	// The older parameter is untouched: which LINK, as well as which visitor.
	assert.NotNil(t, pa["share_id"])

	// ⚠ The HASH, never the token. These parameters are a queue row an
	// administrator reads, and the token IS the link.
	assert.NotContains(t, rawA, tokenA, "the live token travelled into the queue row")
	assert.NotContains(t, rawA, tokenB)
}

func hash(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// TestPageLessPluginShare_IsServedAsAnOrdinaryLink is the other half of the
// delivery story, at the surface a visitor actually meets: a share an app
// opened with NO page behind it is a download link and nothing else. The
// bytes come from the node, the shell calls it a file, and the app-surface
// routes refuse it — there is no surface.
func TestPageLessPluginShare_IsServedAsAnOrdinaryLink(t *testing.T) {
	f := newAppFixture(t, nil)
	id := f.installEcho(t)
	ctx := context.Background()

	f.writeFile(t, "docs/contract-signed.txt", "SIGNED BYTES")
	node, err := f.store.CreateNode(ctx, &model.Node{
		StorageID: f.st.ID, Name: "contract-signed.txt", Path: "/docs/contract-signed.txt",
		PathHash: pathkey.Hash(f.st.ID, "/docs/contract-signed.txt"),
		Type:     model.NodeTypeFile, Size: 12, Mime: "text/plain",
	})
	require.NoError(t, err)

	sh, err := f.store.CreateShare(ctx, &model.Share{
		NodeID: node.ID, Token: "0a1b2c3d4e5f60718293a4b5c6d7e8f9",
		Kind: model.ShareKindDownload, PluginID: id, PageID: "", Subject: "contract-signed.txt",
	})
	require.NoError(t, err)
	require.False(t, sh.IsApp(), "plugin_id alone does not make an app link")

	// The shell sees a file, not an app.
	status, raw := doReq(t, freshClient(t), http.MethodGet, f.srv.URL+"/api/public/s/"+sh.Token, nil)
	require.Equal(t, http.StatusOK, status, string(raw))
	var state struct {
		Kind string `json:"kind"`
		App  any    `json:"app"`
		Node *struct {
			Name string `json:"name"`
		} `json:"node"`
	}
	require.NoError(t, json.Unmarshal(raw, &state))
	assert.Equal(t, "file", state.Kind)
	assert.Nil(t, state.App)
	require.NotNil(t, state.Node)
	assert.Equal(t, "contract-signed.txt", state.Node.Name)

	// The bytes are the node's, through the ordinary download path.
	status, raw = doReq(t, freshClient(t), http.MethodGet, f.srv.URL+"/s/"+sh.Token, nil)
	require.Equal(t, http.StatusOK, status)
	assert.Equal(t, "SIGNED BYTES", string(raw))

	// And the app-surface routes will not answer for it.
	status, _ = doReq(t, freshClient(t), http.MethodPost, f.srv.URL+"/api/public/s/"+sh.Token+"/event",
		map[string]any{"event": "open"})
	assert.Equal(t, http.StatusNotFound, status, "a link with no page is not an app surface")
}
