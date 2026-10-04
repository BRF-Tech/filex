package handlers_test

// Encryption requests — the approval policy (internal/e2epolicy requests.go)
// over real HTTP, on a multi-tenant instance: who may ask, who decides, what
// an approval opens, what expires, and whose bell hears of it.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/acl"
	"github.com/brf-tech/filex/backend/internal/api"
	"github.com/brf-tech/filex/backend/internal/config"
	"github.com/brf-tech/filex/backend/internal/confine"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/e2e"
	"github.com/brf-tech/filex/backend/internal/e2epolicy"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/notify"
	"github.com/brf-tech/filex/backend/internal/pathkey"
	"github.com/brf-tech/filex/backend/internal/testutil"
	"github.com/brf-tech/filex/backend/internal/testutil/dbtest"
)

// e2eTenant is one tenant of the fixture: its administrator and a member,
// both signed in, and its storage.
type e2eTenant struct {
	id       int64
	admin    *http.Client
	member   *http.Client
	memberID int64
	storage  *model.Storage
}

// e2eFix is a multi-tenant instance with two tenants under the approval
// policy, the platform operator signed in, the bell wired — and the policy
// and request services at hand, for the question no HTTP door here asks
// (CheckCreate is a create door's) and, when the fixture built them, for the
// clock.
type e2eFix struct {
	srv    *httptest.Server
	store  db.Store
	super  *http.Client
	policy *e2epolicy.Service
	reqs   *e2epolicy.Requests
	notif  notify.Service
	clock  *clock
	a, b   e2eTenant
}

func newE2EFix(t *testing.T) *e2eFix { return newE2EFixWith(t, true) }

// newE2EFixWith builds the fixture; own=false leaves the policy and the
// request services to api.BuildRouter, as internal/server has them — with
// the real clock.
func newE2EFixWith(t *testing.T, own bool) *e2eFix {
	t.Helper()
	f := &e2eFix{clock: &clock{t: time.Now().UTC()}}
	var deps *api.Deps
	srv, super, store := testutil.NewTestServerWith(t, func(c *config.Config) {
		c.MultiTenant = true
	}, func(d *api.Deps) {
		deps = d
		f.notif = notify.New(d.Store, notify.Config{RetryBackoffs: []time.Duration{}})
		d.Notify = f.notif
		if !own {
			return
		}
		d.ACL = acl.New(d.Store)
		f.policy = e2epolicy.New(e2epolicy.Options{
			Store: d.Store, ACL: d.ACL, MultiTenant: true, Now: f.clock.Now, OnUse: e2epolicy.UseRecorder(d.Store),
		})
		d.E2EPolicy = f.policy
		f.reqs = e2epolicy.NewRequests(e2epolicy.RequestsOptions{
			Store: d.Store, Policy: f.policy, Notify: f.notif, Now: f.clock.Now,
		})
		d.E2ERequests = f.reqs
	})
	t.Cleanup(f.notif.Stop)
	f.srv, f.super, f.store = srv, super, store
	if !own {
		f.policy, f.reqs = deps.E2EPolicy, deps.E2ERequests
	}

	email, pw := testutil.SeedAdmin(t, store) // provider 1 (`default`) = supertenant
	testutil.LoginAs(t, srv, super, email, pw)
	f.a = f.tenant(t, "acme")
	f.b = f.tenant(t, "globex")
	return f
}

// tenant seeds a tenant under the approval policy: an administrator, a
// member and a storage, the two people signed in.
func (f *e2eFix) tenant(t *testing.T, slug string) e2eTenant {
	t.Helper()
	id, email, pw := seedTenant(t, f.store, slug, "admin@"+slug+".test", false)
	require.NoError(t, f.store.SetProviderE2E(context.Background(), id,
		model.ProviderE2E{Allowed: true, Policy: model.E2EPolicyApproval}))
	tn := e2eTenant{id: id, admin: freshClient(t), member: freshClient(t)}
	testutil.LoginAs(t, f.srv, tn.admin, email, pw)
	tn.memberID = seedUserIn(t, f.store, id, "uye@"+slug+".test")
	testutil.LoginAs(t, f.srv, tn.member, "uye@"+slug+".test", "VictimPass!1")
	tn.storage = seedStorageFor(t, f.store, id, slug+"-depo")
	return tn
}

func (f *e2eFix) user(t *testing.T, id int64) *model.User {
	t.Helper()
	u, err := f.store.GetUser(context.Background(), id)
	require.NoError(t, err)
	return u
}

func (f *e2eFix) url(p string) string { return f.srv.URL + p }

// e2eReq is a request on the wire (handlers e2eRequestWire).
type e2eReq struct {
	ID           int64      `json:"id"`
	Path         string     `json:"path"`
	Storage      string     `json:"storage"`
	Kind         string     `json:"kind"`
	Reason       string     `json:"reason"`
	Status       string     `json:"status"`
	Requester    string     `json:"requester"`
	RequesterID  int64      `json:"requester_id"`
	Decider      string     `json:"decider"`
	DecisionNote string     `json:"decision_note"`
	UsedAt       *time.Time `json:"used_at"`
	TenantID     *int64     `json:"tenant_id"`
}

func decodeE2EReq(t *testing.T, raw []byte) (e2eReq, bool) {
	t.Helper()
	var out struct {
		Request e2eReq `json:"request"`
		Created bool   `json:"created"`
	}
	require.NoError(t, json.Unmarshal(raw, &out), string(raw))
	return out.Request, out.Created
}

func decodeE2EReqs(t *testing.T, raw []byte) []e2eReq {
	t.Helper()
	var out struct {
		Requests []e2eReq `json:"requests"`
	}
	require.NoError(t, json.Unmarshal(raw, &out), string(raw))
	return out.Requests
}

// ask leaves a request as c; the body's path is <the tenant's storage>://rel.
// What it names is catalogued first when nothing is there yet (a folder for
// the two folder kinds, a file for a file): a request names something that
// exists (operator decision 2026-10-03, TestE2ERequests_ARequestNamesWhatIsThere).
func (f *e2eFix) ask(t *testing.T, c *http.Client, tn e2eTenant, rel, kind string) (int, []byte) {
	t.Helper()
	f.ensure(t, tn, rel, kind)
	return doReq(t, c, http.MethodPost, f.url("/api/files/e2e/requests"), map[string]any{
		"path": tn.storage.Name + "://" + rel, "kind": kind, "reason": "Müşteri sözleşmeleri",
	})
}

// askAndApprove has the tenant's member ask and its administrator approve,
// and answers the request's id.
func (f *e2eFix) askAndApprove(t *testing.T, tn e2eTenant, rel, kind string) int64 {
	t.Helper()
	st, raw := f.ask(t, tn.member, tn, rel, kind)
	require.Equal(t, http.StatusCreated, st, string(raw))
	req, _ := decodeE2EReq(t, raw)
	st, raw = doReq(t, tn.admin, http.MethodPost, f.url(fmt.Sprintf("/api/admin/e2e/requests/%d/approve", req.ID)), nil)
	require.Equal(t, http.StatusOK, st, string(raw))
	return req.ID
}

// bellRow is one row of a bell, as GET /api/notifications answers it.
type bellRow struct {
	Title string         `json:"title"`
	Body  string         `json:"body"`
	Meta  map[string]any `json:"meta"`
}

// bell answers the rows of one event in c's bell.
func (f *e2eFix) bell(t *testing.T, c *http.Client, event notify.EventType) []bellRow {
	t.Helper()
	st, raw := doReq(t, c, http.MethodGet, f.url("/api/notifications?limit=50"), nil)
	require.Equal(t, http.StatusOK, st, string(raw))
	var out struct {
		Items []struct {
			Event string `json:"event"`
			bellRow
		} `json:"items"`
	}
	require.NoError(t, json.Unmarshal(raw, &out), string(raw))
	var rows []bellRow
	for _, it := range out.Items {
		if it.Event == string(event) {
			rows = append(rows, it.bellRow)
		}
	}
	return rows
}

// A member asks; their own tenant's administrator sees and decides it —
// another tenant's cannot even tell it exists.
func TestE2ERequests_AMemberAsksTheirAdministratorDecides(t *testing.T) {
	f := newE2EFix(t)

	st, raw := f.ask(t, f.a.member, f.a, "Proje", "folder")
	require.Equal(t, http.StatusCreated, st, string(raw))
	req, created := decodeE2EReq(t, raw)
	assert.True(t, created)
	assert.Equal(t, model.E2ERequestPending, req.Status)
	assert.Equal(t, f.a.storage.Name+"://Proje", req.Path)
	assert.Equal(t, model.E2ERequestFolder, req.Kind)
	assert.Equal(t, "Müşteri sözleşmeleri", req.Reason)
	assert.Equal(t, f.a.memberID, req.RequesterID)
	require.NotNil(t, req.TenantID)
	assert.Equal(t, f.a.id, *req.TenantID)

	// Asking again answers the request already waiting.
	st, raw = f.ask(t, f.a.member, f.a, "Proje", "folder")
	require.Equal(t, http.StatusOK, st, string(raw))
	again, created := decodeE2EReq(t, raw)
	assert.False(t, created)
	assert.Equal(t, req.ID, again.ID)

	st, raw = doReq(t, f.a.member, http.MethodGet, f.url("/api/files/e2e/requests"), nil)
	require.Equal(t, http.StatusOK, st, string(raw))
	require.Len(t, decodeE2EReqs(t, raw), 1)
	st, raw = doReq(t, f.b.member, http.MethodGet, f.url("/api/files/e2e/requests"), nil)
	require.Equal(t, http.StatusOK, st, string(raw))
	assert.Empty(t, decodeE2EReqs(t, raw), "somebody else's requests are not in my list")

	list := func(c *http.Client) []e2eReq {
		st, raw := doReq(t, c, http.MethodGet, f.url("/api/admin/e2e/requests"), nil)
		require.Equal(t, http.StatusOK, st, string(raw))
		var out struct {
			TTLDays int `json:"ttl_days"`
		}
		require.NoError(t, json.Unmarshal(raw, &out))
		assert.Equal(t, 7, out.TTLDays)
		return decodeE2EReqs(t, raw)
	}
	require.Len(t, list(f.a.admin), 1, "the tenant's administrator sees the request")
	assert.Len(t, list(f.super), 1, "the platform operator sees every tenant's")
	assert.Empty(t, list(f.b.admin), "another tenant's administrator sees none of it")
	st, raw = doReq(t, f.a.admin, http.MethodGet, f.url("/api/admin/e2e"), nil)
	require.Equal(t, http.StatusOK, st)
	assert.Equal(t, 1, decodeE2EPolicy(t, raw).Pending)

	// A foreign id answers exactly like an id nobody has.
	for _, path := range []string{
		fmt.Sprintf("/api/admin/e2e/requests/%d/approve", req.ID),
		fmt.Sprintf("/api/admin/e2e/requests/%d/reject", req.ID),
		"/api/admin/e2e/requests/999999/approve",
	} {
		st, body := doJSON(t, f.b.admin, http.MethodPost, f.url(path), nil)
		require.Equal(t, http.StatusNotFound, st, "%s: %v", path, body)
		assert.Equal(t, "not_found", body["error"])
	}

	st, raw = doReq(t, f.a.admin, http.MethodPost, f.url(fmt.Sprintf("/api/admin/e2e/requests/%d/approve", req.ID)),
		map[string]any{"note": "Tamam"})
	require.Equal(t, http.StatusOK, st, string(raw))
	decided, _ := decodeE2EReq(t, raw)
	assert.Equal(t, model.E2ERequestApproved, decided.Status)
	assert.Equal(t, "Tamam", decided.DecisionNote)
	assert.NotEmpty(t, decided.Decider)

	// Decided once.
	for _, verb := range []string{"approve", "reject"} {
		st, body := doJSON(t, f.a.admin, http.MethodPost, f.url(fmt.Sprintf("/api/admin/e2e/requests/%d/%s", req.ID, verb)), nil)
		require.Equal(t, http.StatusConflict, st, "%v", body)
		assert.Equal(t, "not_pending", body["error"])
	}
	st, raw = doReq(t, f.a.admin, http.MethodGet, f.url("/api/admin/e2e/requests?status=all"), nil)
	require.Equal(t, http.StatusOK, st, string(raw))
	require.Len(t, decodeE2EReqs(t, raw), 1, "a decided request is in the `all` list")
	st, body := doJSON(t, f.a.admin, http.MethodGet, f.url("/api/admin/e2e/requests?status=someday"), nil)
	require.Equal(t, http.StatusBadRequest, st, "%v", body)

	assert.Len(t, e2eAuditRows(t, f.store, e2epolicy.AuditActionRequestCreate), 1)
	approvals := e2eAuditRows(t, f.store, e2epolicy.AuditActionRequestApprove)
	require.Len(t, approvals, 1)
	assert.Equal(t, strconv.FormatInt(req.ID, 10), approvals[0].TargetID)
	assert.Equal(t, f.a.storage.Name+"://Proje", approvals[0].Metadata["target_name"])
}

// An approval opens exactly one encryption: for that person, at that place,
// once.
func TestE2ERequests_AnApprovalOpensOneEncryption(t *testing.T) {
	f := newE2EFix(t)
	ctx := context.Background()
	member := f.user(t, f.a.memberID)
	marker := "Proje/" + e2e.MarkerName
	var refused *e2epolicy.RefusedError

	require.ErrorAs(t, f.policy.CheckCreate(ctx, member, f.a.storage, marker), &refused)
	assert.Equal(t, e2epolicy.ReasonApprovalRequired, refused.Reason)

	id := f.askAndApprove(t, f.a, "Proje", model.E2ERequestFolder)
	colleague := f.user(t, seedUserIn(t, f.store, f.a.id, "uye2@acme.test"))
	require.ErrorAs(t, f.policy.CheckCreate(ctx, colleague, f.a.storage, marker), &refused,
		"an approval is its requester's, not their tenant's")
	require.ErrorAs(t, f.policy.CheckCreate(ctx, member, f.a.storage, "Baska/"+e2e.MarkerName), &refused,
		"an approval is for the folder it names")

	require.NoError(t, f.policy.CheckCreate(ctx, member, f.a.storage, marker))
	require.ErrorAs(t, f.policy.CheckCreate(ctx, member, f.a.storage, marker), &refused, "…once")
	assert.Equal(t, e2epolicy.ReasonApprovalRequired, refused.Reason)

	row, err := f.store.GetE2ERequest(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, model.E2ERequestUsed, row.Status)
	assert.NotNil(t, row.UsedAt)
	uses := e2eAuditRows(t, f.store, e2epolicy.AuditActionRequestUse)
	require.Len(t, uses, 1)
	assert.Equal(t, strconv.FormatInt(id, 10), uses[0].TargetID)

	// A single file: asked by its name, kept — and approved — as its folder's
	// (e2epolicy.ApprovalPath): the name its `.fxe` gets is not known yet.
	fid := f.askAndApprove(t, f.a, "Proje/rapor.pdf", model.E2ERequestFile)
	frow, err := f.store.GetE2ERequest(ctx, fid)
	require.NoError(t, err)
	assert.Equal(t, "Proje", frow.Path)
	assert.Equal(t, model.E2ERequestFile, frow.Kind)
	require.NoError(t, f.policy.CheckCreate(ctx, member, f.a.storage, "Proje/encrypted-3f9a1c2b"+e2e.FileExtension))
}

// The services api.BuildRouter builds — the ones internal/server runs — log
// an approval spent at a create door (Options.OnUse → UseRecorder).
func TestE2ERequests_TheRoutersPolicyLogsASpentApproval(t *testing.T) {
	f := newE2EFixWith(t, false)
	id := f.askAndApprove(t, f.a, "Proje", model.E2ERequestFolder)
	require.NoError(t, f.policy.CheckCreate(context.Background(), f.user(t, f.a.memberID), f.a.storage, "Proje/"+e2e.MarkerName))
	uses := e2eAuditRows(t, f.store, e2epolicy.AuditActionRequestUse)
	require.Len(t, uses, 1, "the spent approval is in the audit log")
	assert.Equal(t, strconv.FormatInt(id, 10), uses[0].TargetID)
}

// The same at a REAL door. A rename onto a key file through /api/files/manager
// asks the policy api.BuildRouter built — no test wired one, as in the test
// above, and nothing but the router's own line hands it the OnUse that writes
// the row — and the approval it spends is in the audit log once, naming the
// request, the person, the folder approved and the folder encrypted. A
// new-folder approval opens one new folder directly inside the folder it was
// given for, so the two differ, and for a create over a protocol this row is
// the one link between an encryption and its approval.
func TestE2ERequests_AnApprovalSpentAtARealDoorIsAudited(t *testing.T) {
	f := newMTFix(t, false)
	ctx := context.Background()
	tok := issueToken(t, f.Store, f.UserA, "read,write", nil)
	status, body := fxMutate(t, f.URL, tok, "newfolder", map[string]any{"path": "alpha://", "name": "Acik"})
	require.Equal(t, http.StatusOK, status, body)
	fxUpload(t, f.URL, tok, "alpha://Acik", "m.json", kfOne)
	require.NoError(t, f.Store.UpsertSetting(ctx, model.SettingE2EPolicy, model.E2EPolicyApproval))
	req := dbtest.ApproveE2E(t, f.Store, f.UserA, f.StA.ID, "Acik", model.E2ERequestFolder)
	assert.Empty(t, e2eAuditRows(t, f.Store, e2epolicy.AuditActionRequestUse), "nothing is spent yet")

	status, body = fxMutate(t, f.URL, tok, "rename", map[string]any{"path": "alpha://Acik", "item": "alpha://Acik/m.json", "name": e2eKeyFile})
	require.Equal(t, http.StatusOK, status, "the approved rename: %s", body)
	assert.Equal(t, model.E2ERequestUsed, dbtest.E2EStatus(t, f.Store, req.ID), "the approval was not spent")

	uses := e2eAuditRows(t, f.Store, e2epolicy.AuditActionRequestUse)
	require.Len(t, uses, 1, "a spent approval is one audit row, written by the router's own policy")
	assert.Equal(t, e2epolicy.AuditTargetRequest, uses[0].TargetType)
	assert.Equal(t, strconv.FormatInt(req.ID, 10), uses[0].TargetID)
	require.NotNil(t, uses[0].UserID)
	assert.Equal(t, f.UserA, *uses[0].UserID, "the person who spent it")
	assert.Equal(t, "alpha://Acik", uses[0].Metadata["target_name"])
	assert.Equal(t, "alpha://Acik", uses[0].Metadata["encrypted"], "spent on the folder approved")
	assert.Equal(t, model.E2ERequestUsed, uses[0].Metadata["status"])

	// Spent on a new folder inside the one approved: the explorer's "new
	// encrypted folder here" makes the folder, then uploads its key file.
	for _, dir := range []map[string]any{{"path": "alpha://", "name": "Ust"}, {"path": "alpha://Ust", "name": "Yeni"}} {
		status, body = fxMutate(t, f.URL, tok, "newfolder", dir)
		require.Equal(t, http.StatusOK, status, body)
	}
	parent := dbtest.ApproveE2E(t, f.Store, f.UserA, f.StA.ID, "Ust", model.E2ERequestNewFolder)
	fxUpload(t, f.URL, tok, "alpha://Ust/Yeni", e2eKeyFile, kfOne)
	assert.Equal(t, model.E2ERequestUsed, dbtest.E2EStatus(t, f.Store, parent.ID), "the parent's approval was not spent")
	var spent *model.AuditEntry
	for _, row := range e2eAuditRows(t, f.Store, e2epolicy.AuditActionRequestUse) {
		if row.TargetID == strconv.FormatInt(parent.ID, 10) {
			spent = row
		}
	}
	require.NotNil(t, spent, "the parent's spent approval has no audit row")
	assert.Equal(t, "alpha://Ust", spent.Metadata["target_name"], "the approval")
	assert.Equal(t, "alpha://Ust/Yeni", spent.Metadata["encrypted"], "the folder it opened")
}

// A rejection opens nothing, and closes the request: asking again is a new
// request.
func TestE2ERequests_ARejectionOpensNothing(t *testing.T) {
	f := newE2EFix(t)
	st, raw := f.ask(t, f.a.member, f.a, "Proje", "folder")
	require.Equal(t, http.StatusCreated, st, string(raw))
	req, _ := decodeE2EReq(t, raw)

	st, raw = doReq(t, f.a.admin, http.MethodPost, f.url(fmt.Sprintf("/api/admin/e2e/requests/%d/reject", req.ID)),
		map[string]any{"reason": "Gerek yok"})
	require.Equal(t, http.StatusOK, st, string(raw))
	rejected, _ := decodeE2EReq(t, raw)
	assert.Equal(t, model.E2ERequestRejected, rejected.Status)
	assert.Equal(t, "Gerek yok", rejected.DecisionNote)

	var refused *e2epolicy.RefusedError
	require.ErrorAs(t, f.policy.CheckCreate(context.Background(), f.user(t, f.a.memberID), f.a.storage, "Proje/"+e2e.MarkerName), &refused)
	assert.Equal(t, e2epolicy.ReasonApprovalRequired, refused.Reason)
	rows := f.bell(t, f.a.member, notify.EventE2ERequestDecided)
	require.Len(t, rows, 1, "the person who asked hears the no too")
	assert.Equal(t, "Encryption request rejected: Proje", rows[0].Title)
	assert.Equal(t, "Gerek yok", rows[0].Body)
	assert.Equal(t, model.E2ERequestRejected, rows[0].Meta["decision"])

	st, raw = f.ask(t, f.a.member, f.a, "Proje", "folder")
	require.Equal(t, http.StatusCreated, st, string(raw))
	next, created := decodeE2EReq(t, raw)
	assert.True(t, created)
	assert.NotEqual(t, req.ID, next.ID)
	assert.Len(t, e2eAuditRows(t, f.store, e2epolicy.AuditActionRequestReject), 1)
}

// What nobody acts on expires: a request nobody decided, an approval nobody
// spent — seven days on.
func TestE2ERequests_WhatNobodyActsOnExpires(t *testing.T) {
	f := newE2EFix(t)
	ctx := context.Background()
	st, raw := f.ask(t, f.a.member, f.a, "Bekleyen", "folder")
	require.Equal(t, http.StatusCreated, st, string(raw))
	waiting, _ := decodeE2EReq(t, raw)
	approved := f.askAndApprove(t, f.a, "Onayli", model.E2ERequestFolder)

	f.clock.add(e2epolicy.ApprovalTTL + time.Minute)
	var refused *e2epolicy.RefusedError
	require.ErrorAs(t, f.policy.CheckCreate(ctx, f.user(t, f.a.memberID), f.a.storage, "Onayli/"+e2e.MarkerName), &refused,
		"an approval past its seven days opens nothing, whatever its row still says")

	assert.Equal(t, 2, f.reqs.ExpireDue(ctx))
	for _, id := range []int64{waiting.ID, approved} {
		row, err := f.store.GetE2ERequest(ctx, id)
		require.NoError(t, err)
		assert.Equal(t, model.E2ERequestExpired, row.Status, "request %d", id)
	}
	assert.Len(t, e2eAuditRows(t, f.store, e2epolicy.AuditActionRequestExpire), 2)
	assert.Zero(t, f.reqs.ExpireDue(ctx), "a second sweep finds nothing")

	st, raw = doReq(t, f.a.admin, http.MethodGet, f.url("/api/admin/e2e/requests"), nil)
	require.Equal(t, http.StatusOK, st, string(raw))
	assert.Empty(t, decodeE2EReqs(t, raw), "nothing is waiting any more")
}

// backlog leaves n requests pending for the tenant's member, one folder each
// (prefix-000, prefix-001, …), oldest first: each expires a second after the
// one before it, ApprovalTTL from the fixture's clock.
func (f *e2eFix) backlog(t *testing.T, tn e2eTenant, prefix string, n int) []int64 {
	t.Helper()
	ctx := context.Background()
	start, ids := f.clock.Now(), make([]int64, n)
	for i := range ids {
		r, err := f.store.CreateE2ERequest(ctx, &model.E2ERequest{
			ProviderID: &tn.id, UserID: tn.memberID, Requester: "uye", StorageID: tn.storage.ID,
			Path: fmt.Sprintf("%s-%03d", prefix, i), Kind: model.E2ERequestFolder, Reason: "yedek",
			ExpiresAt: start.Add(e2epolicy.ApprovalTTL + time.Duration(i)*time.Second),
		})
		require.NoError(t, err)
		ids[i] = r.ID
	}
	return ids
}

// A pending request past its seven days is not decided, however many newer
// requests there are — one more than a list holds, here. Approved, it would
// get seven fresh days and open what nobody approved in time. It expires
// instead, and the answer is the one a request decided already gets.
func TestE2ERequests_ARequestPastItsTimeCannotBeApproved(t *testing.T) {
	f := newE2EFix(t)
	ctx := context.Background()
	ids := f.backlog(t, f.a, "Bekleyen", 501)
	f.clock.add(e2epolicy.ApprovalTTL + time.Hour)

	st, body := doJSON(t, f.a.admin, http.MethodPost, f.url(fmt.Sprintf("/api/admin/e2e/requests/%d/approve", ids[0])), nil)
	require.Equal(t, http.StatusConflict, st, "the oldest request, past its time, was decided: %v", body)
	assert.Equal(t, "not_pending", body["error"])
	row, err := f.store.GetE2ERequest(ctx, ids[0])
	require.NoError(t, err)
	assert.Equal(t, model.E2ERequestExpired, row.Status)
	var refused *e2epolicy.RefusedError
	require.ErrorAs(t, f.policy.CheckCreate(ctx, f.user(t, f.a.memberID), f.a.storage, "Bekleyen-000/"+e2e.MarkerName), &refused,
		"nothing was opened")
	assert.Equal(t, e2epolicy.ReasonApprovalRequired, refused.Reason)
}

// One sweep closes every request that is due, however many — and only those:
// 501 past their time, under 500 newer ones still waiting that a list of the
// newest reads first.
func TestE2ERequests_OneSweepClosesEverythingDue(t *testing.T) {
	f := newE2EFix(t)
	ctx := context.Background()
	due := f.backlog(t, f.a, "Eski", 501)
	f.clock.add(e2epolicy.ApprovalTTL + time.Hour)
	waiting := f.backlog(t, f.a, "Yeni", 500)

	assert.Equal(t, 501, f.reqs.ExpireDue(ctx))
	for _, id := range []int64{due[0], due[250], due[500]} {
		row, err := f.store.GetE2ERequest(ctx, id)
		require.NoError(t, err)
		assert.Equal(t, model.E2ERequestExpired, row.Status, "request %d", id)
	}
	for _, id := range []int64{waiting[0], waiting[499]} {
		row, err := f.store.GetE2ERequest(ctx, id)
		require.NoError(t, err)
		assert.Equal(t, model.E2ERequestPending, row.Status, "request %d is not due", id)
	}
	_, logged, err := f.store.ListAuditFiltered(ctx, nil, e2epolicy.AuditActionRequestExpire, nil, nil, 1, 0)
	require.NoError(t, err)
	assert.EqualValues(t, 501, logged, "one expire row each")
	assert.Zero(t, f.reqs.ExpireDue(ctx), "a second sweep finds nothing")
}

// e2eSweepFails is a store the sweep cannot read: every list of what is due
// (E2ERequestFilter.ExpiresBefore) fails. Everything else reads through.
type e2eSweepFails struct{ db.Store }

func (s e2eSweepFails) ListE2ERequests(ctx context.Context, f model.E2ERequestFilter) ([]*model.E2ERequest, error) {
	if !f.ExpiresBefore.IsZero() {
		return nil, errors.New("connection reset by peer")
	}
	return s.Store.ListE2ERequests(ctx, f)
}

// blindRequests is the fixture's request desk over a store its sweep cannot
// read, so nothing but the desk's own reading of a row's time can stop it.
func (f *e2eFix) blindRequests() *e2epolicy.Requests {
	return e2epolicy.NewRequests(e2epolicy.RequestsOptions{
		Store: e2eSweepFails{f.store}, Policy: f.policy, Now: f.clock.Now,
		Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
}

// Deciding does not lean on the sweep. A request past its seven days that no
// sweep has closed is expired where it is decided — with its audit row — and
// answered like a request decided already.
func TestE2ERequests_DecidingExpiresWhatTheSweepMissed(t *testing.T) {
	f := newE2EFix(t)
	ctx := context.Background()
	st, raw := f.ask(t, f.a.member, f.a, "Proje", "folder")
	require.Equal(t, http.StatusCreated, st, string(raw))
	req, _ := decodeE2EReq(t, raw)
	f.clock.add(e2epolicy.ApprovalTTL + time.Minute)

	admin, err := f.store.GetUserByEmail(ctx, "admin@acme.test")
	require.NoError(t, err)
	_, err = f.blindRequests().Decide(ctx, e2epolicy.Decision{
		ID: req.ID, Approve: true, Tenant: &f.a.id, Who: e2epolicy.Actor{UserID: &admin.ID, Name: admin.Email},
	})
	var refused *e2epolicy.RequestError
	require.ErrorAs(t, err, &refused, "a request past its time was decided")
	assert.Equal(t, http.StatusConflict, refused.Status)
	assert.Equal(t, "not_pending", refused.Code)

	row, err := f.store.GetE2ERequest(ctx, req.ID)
	require.NoError(t, err)
	assert.Equal(t, model.E2ERequestExpired, row.Status)
	assert.Nil(t, row.DecidedBy, "nobody decided it")
	expired := e2eAuditRows(t, f.store, e2epolicy.AuditActionRequestExpire)
	require.Len(t, expired, 1)
	assert.Equal(t, strconv.FormatInt(req.ID, 10), expired[0].TargetID)
	assert.Equal(t, model.E2ERequestPending, expired[0].Metadata["was"])
	assert.Empty(t, e2eAuditRows(t, f.store, e2epolicy.AuditActionRequestApprove))
}

// A request past its time is not "waiting": asking again files a new one,
// even while no sweep has closed the old row.
func TestE2ERequests_AnExpiredRequestCanBeAskedAgain(t *testing.T) {
	f := newE2EFix(t)
	ctx := context.Background()
	reqs := f.blindRequests()
	f.ensure(t, f.a, "Proje", model.E2ERequestFolder)
	in := e2epolicy.CreateInput{
		User: f.user(t, f.a.memberID), Storage: f.a.storage, Path: "Proje", Kind: model.E2ERequestFolder, Reason: "sözleşmeler",
	}
	first, created, err := reqs.Create(ctx, in)
	require.NoError(t, err)
	require.True(t, created)
	again, created, err := reqs.Create(ctx, in)
	require.NoError(t, err)
	assert.False(t, created, "asked again within its time: the request waiting")
	assert.Equal(t, first.ID, again.ID)

	f.clock.add(e2epolicy.ApprovalTTL + time.Minute)
	next, created, err := reqs.Create(ctx, in)
	require.NoError(t, err)
	assert.True(t, created, "the request past its time was answered as the one waiting")
	assert.NotEqual(t, first.ID, next.ID)
	assert.Equal(t, model.E2ERequestPending, next.Status)
}

// The request reaches its tenant's administrators - once, as one broadcast -
// and the decision reaches the person who asked. The platform operator sees
// it on Admin -> Encryption, and is not told of each one (operator decision
// 2026-10-03, TestE2ERequests_TheOperatorSeesButDoesNotDecide).
func TestE2ERequests_TheRightBellsHearOfIt(t *testing.T) {
	f := newE2EFix(t)
	st, raw := f.ask(t, f.a.member, f.a, "Proje", "folder")
	require.Equal(t, http.StatusCreated, st, string(raw))
	req, _ := decodeE2EReq(t, raw)

	created := notify.EventE2ERequestCreated
	rows := f.bell(t, f.a.admin, created)
	require.Len(t, rows, 1, "the tenant's administrator hears of the request")
	assert.Equal(t, "Encryption request: Proje", rows[0].Title)
	assert.Equal(t, f.user(t, f.a.memberID).Label()+": Müşteri sözleşmeleri", rows[0].Body)
	assert.Equal(t, float64(req.ID), rows[0].Meta["request_id"])
	assert.Equal(t, model.E2ERequestFolder, rows[0].Meta["target_kind"])
	assert.NotContains(t, rows[0].Meta, "kind", "meta.kind picks another event's file wording in a reader")
	assert.Equal(t, "Proje", rows[0].Meta["node"].(map[string]any)["path"], "placed on the folder")
	assert.Equal(t, "Müşteri sözleşmeleri", rows[0].Meta["reason"])
	assert.NotEmpty(t, rows[0].Meta["requester"])
	assert.Empty(t, f.bell(t, f.super, created), "the platform operator is not told of a tenant's request")
	assert.Empty(t, f.bell(t, f.b.admin, created), "another tenant's administrator does not")
	assert.Empty(t, f.bell(t, f.a.member, created), "a member is not told of requests")

	all, _, err := f.notif.List(context.Background(), nil, notify.AdminBell, false, 50, 0)
	require.NoError(t, err)
	n := 0
	for _, row := range all {
		if row.Event == string(created) {
			n++
			assert.Nil(t, row.UserID, "one broadcast, addressed to nobody")
		}
	}
	assert.Equal(t, 1, n, "one row, so one webhook delivery")

	st, raw = doReq(t, f.a.admin, http.MethodPost, f.url(fmt.Sprintf("/api/admin/e2e/requests/%d/approve", req.ID)),
		map[string]any{"note": "Tamam"})
	require.Equal(t, http.StatusOK, st, string(raw))
	decided := notify.EventE2ERequestDecided
	rows = f.bell(t, f.a.member, decided)
	require.Len(t, rows, 1, "the person who asked hears the answer")
	assert.Equal(t, "Encryption request approved: Proje", rows[0].Title)
	assert.Equal(t, "Tamam", rows[0].Body)
	assert.Equal(t, model.E2ERequestApproved, rows[0].Meta["decision"])
	assert.Equal(t, float64(req.ID), rows[0].Meta["request_id"])
	assert.Empty(t, f.bell(t, f.a.admin, decided), "the answer is the requester's")
	assert.Empty(t, f.bell(t, f.b.member, decided))
}

// A request is filed only where the rule itself answers `request`.
func TestE2ERequests_OnlyWhatTheRuleAsksForIsFiled(t *testing.T) {
	f := newE2EFix(t)
	ctx := context.Background()
	post := func(c *http.Client, body map[string]any) (int, map[string]any) {
		return doJSON(t, c, http.MethodPost, f.url("/api/files/e2e/requests"), body)
	}
	wire := f.a.storage.Name + "://Proje"

	st, body := post(f.a.admin, map[string]any{"path": wire, "kind": "folder", "reason": "x"})
	require.Equal(t, http.StatusBadRequest, st, "%v", body)
	assert.Equal(t, "not_requestable", body["error"])
	assert.Equal(t, "allowed", body["answer"], "an administrator is not asked for approval")

	st, body = post(f.a.member, map[string]any{"path": wire, "kind": "folder", "reason": "  "})
	require.Equal(t, http.StatusBadRequest, st, "%v", body)
	assert.Equal(t, "reason_required", body["error"])
	st, body = post(f.a.member, map[string]any{"path": wire, "kind": "disk", "reason": "x"})
	require.Equal(t, http.StatusBadRequest, st, "%v", body)
	assert.Equal(t, "bad_request", body["error"])
	st, body = post(f.a.member, map[string]any{"path": wire + "/" + e2e.MarkerName, "kind": "folder", "reason": "x"})
	require.Equal(t, http.StatusBadRequest, st, "a request names the folder, not its key file: %v", body)
	assert.Equal(t, "bad_request", body["error"])
	st, body = post(f.a.member, map[string]any{"path": wire + "/rapor.pdf" + e2e.FileExtension, "kind": "file", "reason": "x"})
	require.Equal(t, http.StatusBadRequest, st, "an encrypted file is not asked for again: %v", body)
	assert.Equal(t, "bad_request", body["error"])
	st, body = post(f.a.member, map[string]any{"path": "Proje", "kind": "folder", "reason": "x"})
	require.Equal(t, http.StatusBadRequest, st, "a path that names no storage: %v", body)
	assert.Equal(t, "bad_request", body["error"])
	st, body = post(f.a.member, map[string]any{"path": f.b.storage.Name + "://Proje", "kind": "folder", "reason": "x"})
	require.Equal(t, http.StatusNotFound, st, "another tenant's storage reads as one that does not exist: %v", body)

	for _, c := range []struct {
		setting model.ProviderE2E
		answer  string
		reason  string
	}{
		{model.ProviderE2E{Allowed: true, Policy: model.E2EPolicyPermitted}, "allowed", ""},
		{model.ProviderE2E{Allowed: true, Policy: model.E2EPolicyOff}, "denied", "policy_off"},
		{model.ProviderE2E{Allowed: false, Policy: model.E2EPolicyApproval}, "denied", "tenant_disabled"},
	} {
		require.NoError(t, f.store.SetProviderE2E(ctx, f.a.id, c.setting))
		st, body = post(f.a.member, map[string]any{"path": wire, "kind": "folder", "reason": "x"})
		require.Equal(t, http.StatusBadRequest, st, "%+v: %v", c.setting, body)
		assert.Equal(t, "not_requestable", body["error"])
		assert.Equal(t, c.answer, body["answer"])
		if c.reason != "" {
			assert.Equal(t, c.reason, body["reason"])
		}
	}
	assert.Empty(t, e2eAuditRows(t, f.store, e2epolicy.AuditActionRequestCreate), "nothing was filed")
}

// Deciding is a signed-in person's; an admin-scoped key may read the table.
func TestE2ERequests_DecidingIsAPersons(t *testing.T) {
	f := newE2EFix(t)
	useProductionAuthChain(t, f.store)
	st, raw := f.ask(t, f.a.member, f.a, "Proje", "folder")
	require.Equal(t, http.StatusCreated, st, string(raw))
	req, _ := decodeE2EReq(t, raw)
	admin, err := f.store.GetUserByEmail(context.Background(), "admin@acme.test")
	require.NoError(t, err)
	tok := testutil.NewAPIToken(t, f.store, admin.ID, "admin,read")

	for _, verb := range []string{"approve", "reject"} {
		st, raw := withToken(t, http.MethodPost, f.url(fmt.Sprintf("/api/admin/e2e/requests/%d/%s", req.ID, verb)), tok, map[string]any{})
		require.Equal(t, http.StatusForbidden, st, string(raw))
		var body map[string]any
		require.NoError(t, json.Unmarshal(raw, &body))
		assert.Equal(t, "session_required", body["error"])
	}
	st, raw = withToken(t, http.MethodGet, f.url("/api/admin/e2e/requests"), tok, nil)
	require.Equal(t, http.StatusOK, st, string(raw))
	require.Len(t, decodeE2EReqs(t, raw), 1)
	assert.Equal(t, model.E2ERequestPending, decodeE2EReqs(t, raw)[0].Status)
}

// ensure catalogues what a request of kind names at rel, unless something is
// catalogued there already (a test that puts the other kind there keeps it).
func (f *e2eFix) ensure(t *testing.T, tn e2eTenant, rel, kind string) {
	t.Helper()
	if n, err := f.store.GetNodeByPath(context.Background(), tn.storage.ID, pathkey.Hash(tn.storage.ID, "/"+rel)); err == nil && n != nil {
		return
	}
	typ := model.NodeTypeDirectory
	if kind == model.E2ERequestFile {
		typ = model.NodeTypeFile
	}
	f.catalogue(t, tn, rel, typ)
}

// catalogue records a node of typ at rel on the tenant's storage, as a
// listing would have.
func (f *e2eFix) catalogue(t *testing.T, tn e2eTenant, rel string, typ model.NodeType) {
	t.Helper()
	_, err := f.store.CreateNode(context.Background(), &model.Node{
		StorageID: tn.storage.ID, Name: path.Base(rel), Path: "/" + rel, PathHash: pathkey.Hash(tn.storage.ID, "/"+rel),
		Type: typ,
	})
	require.NoError(t, err)
}

// A request's kind is what is there: a folder is asked for as a folder and a
// file as a file. The other kind would be a row nothing could spend — a folder
// request on a file — or one kept under the wrong folder: a file request on a
// folder P is kept under P's parent. Where nothing is there, nothing is
// requested (path_missing, operator decision 2026-10-03).
func TestE2ERequests_TheKindIsWhatIsThere(t *testing.T) {
	f := newE2EFix(t)
	f.catalogue(t, f.a, "Proje", model.NodeTypeDirectory)
	f.catalogue(t, f.a, "Proje/rapor.pdf", model.NodeTypeFile)
	post := func(rel, kind string) (int, map[string]any) {
		t.Helper()
		return doJSON(t, f.a.member, http.MethodPost, f.url("/api/files/e2e/requests"), map[string]any{
			"path": f.a.storage.Name + "://" + rel, "kind": kind, "reason": "sözleşmeler",
		})
	}

	for _, c := range []struct{ rel, kind string }{{"Proje", "file"}, {"Proje/rapor.pdf", "folder"}} {
		st, body := post(c.rel, c.kind)
		require.Equal(t, http.StatusBadRequest, st, "%s asked for as a %s: %v", c.rel, c.kind, body)
		assert.Equal(t, "kind_mismatch", body["error"], "%s asked for as a %s", c.rel, c.kind)
	}
	assert.Empty(t, e2eAuditRows(t, f.store, e2epolicy.AuditActionRequestCreate), "a mismatch filed a request")
	// What is there is looked at only for someone the rule wants a request
	// from: anyone else hears the rule's own answer, whatever the kind.
	st, body := doJSON(t, f.a.admin, http.MethodPost, f.url("/api/files/e2e/requests"), map[string]any{
		"path": f.a.storage.Name + "://Proje", "kind": "file", "reason": "sözleşmeler",
	})
	require.Equal(t, http.StatusBadRequest, st, "%v", body)
	assert.Equal(t, "not_requestable", body["error"])

	// Nothing there at all is no request (operator decision 2026-10-03,
	// TestE2ERequests_ARequestNamesWhatIsThere).
	st, body = post("Yok/not.pdf", "file")
	require.Equal(t, http.StatusNotFound, st, "%v", body)
	assert.Equal(t, "path_missing", body["error"])
	for _, c := range []struct{ rel, kind, kept string }{
		{"Proje", "folder", "Proje"},
		{"Proje/rapor.pdf", "file", "Proje"},
	} {
		st, raw := f.ask(t, f.a.member, f.a, c.rel, c.kind)
		require.Equal(t, http.StatusCreated, st, "%s as a %s: %s", c.rel, c.kind, raw)
		req, _ := decodeE2EReq(t, raw)
		assert.Equal(t, f.a.storage.Name+"://"+c.kept, req.Path)
		assert.Equal(t, c.kind, req.Kind)
	}

	// A file approval waiting in Arsiv answers a file request there, although
	// nothing is catalogued at the file's path: no second request.
	dbtest.ApproveE2E(t, f.store, f.a.memberID, f.a.storage.ID, "Arsiv", model.E2ERequestFile)
	st, body = post("Arsiv/yeni.pdf", "file")
	require.Equal(t, http.StatusBadRequest, st, "%v", body)
	assert.Equal(t, "not_requestable", body["error"])
	assert.Equal(t, "allowed", body["answer"])
}

// A token confined to a folder (`root:`) asks only under it. The explorer's
// JSON body is confined on the way in (confine.Middleware); a body sent as
// anything else is not, so the handler reads the root itself — and answers as
// the middleware does.
func TestE2ERequests_ARootConfinedTokenAsksOnlyUnderItsRoot(t *testing.T) {
	f := newE2EFix(t)
	useProductionAuthChain(t, f.store)
	st := f.a.storage.Name
	tok := testutil.NewAPIToken(t, f.store, f.a.memberID, "read,write,root:"+st+"://Proje")
	post := func(wire, contentType string) (int, map[string]any) {
		t.Helper()
		raw, err := json.Marshal(map[string]any{"path": wire, "kind": "folder", "reason": "sözleşmeler"})
		require.NoError(t, err)
		req, err := http.NewRequest(http.MethodPost, f.url("/api/files/e2e/requests"), bytes.NewReader(raw))
		require.NoError(t, err)
		req.Header.Set("Authorization", "Bearer "+tok)
		req.Header.Set("Content-Type", contentType)
		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()
		var out map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&out)
		return resp.StatusCode, out
	}

	refused := func(wire, contentType string) {
		t.Helper()
		code, body := post(wire, contentType)
		assert.Equal(t, http.StatusForbidden, code, "%s sent as %s: %v", wire, contentType, body)
		assert.Equal(t, confine.ErrOutOfRoot.Error(), body["error"], "%s sent as %s", wire, contentType)
	}
	for _, outside := range []string{st + "://Baska", st + "://Proje2", f.b.storage.Name + "://Proje"} {
		refused(outside, "text/plain")
		refused(outside, "application/json")
	}
	// The storage itself: a JSON body's is read as the token's root folder
	// (confine.Middleware), a plain one's is not rewritten, and is outside.
	refused(st+"://", "text/plain")
	assert.Empty(t, e2eAuditRows(t, f.store, e2epolicy.AuditActionRequestCreate), "a request was filed outside the root")

	f.ensure(t, f.a, "Proje/Alt", model.E2ERequestFolder)
	code, body := post(st+"://Proje/Alt", "text/plain")
	require.Equal(t, http.StatusCreated, code, "inside the root: %v", body)
	assert.Len(t, e2eAuditRows(t, f.store, e2epolicy.AuditActionRequestCreate), 1)
}

// e2eHeldNotice holds the first notice it is asked to send until released —
// a notice going out slowly — and passes the rest through.
type e2eHeldNotice struct {
	notify.Service
	first   atomic.Bool
	sending chan struct{} // closed once the first notice is being sent
	release chan struct{}
}

func (n *e2eHeldNotice) Send(ctx context.Context, ev notify.Event) (int64, error) {
	if n.first.CompareAndSwap(false, true) {
		close(n.sending)
		<-n.release
	}
	return n.Service.Send(ctx, ev)
}

// One request's notice going out slowly does not hold up the next request:
// the desk's lock covers "is one waiting? no → file it", not the audit row
// and the notices after it.
func TestE2ERequests_ANoticeDoesNotHoldUpTheNextRequest(t *testing.T) {
	f := newE2EFix(t)
	ctx := context.Background()
	held := &e2eHeldNotice{Service: f.notif, sending: make(chan struct{}), release: make(chan struct{})}
	reqs := e2epolicy.NewRequests(e2epolicy.RequestsOptions{Store: f.store, Policy: f.policy, Notify: held, Now: f.clock.Now})
	ask := func(u *model.User, st *model.Storage, rel string) error {
		_, created, err := reqs.Create(ctx, e2epolicy.CreateInput{User: u, Storage: st, Path: rel, Kind: model.E2ERequestFolder, Reason: "x"})
		if err == nil && !created {
			err = errors.New("not filed")
		}
		return err
	}
	ada, bob := f.user(t, f.a.memberID), f.user(t, f.b.memberID)
	f.ensure(t, f.a, "Proje", model.E2ERequestFolder)
	f.ensure(t, f.b, "Arsiv", model.E2ERequestFolder)

	first := make(chan error, 1)
	go func() { first <- ask(ada, f.a.storage, "Proje") }()
	select {
	case <-held.sending:
	case err := <-first:
		t.Fatalf("the first request never reached its notice: %v", err)
	case <-time.After(10 * time.Second):
		t.Fatal("the first request never reached its notice")
	}
	second := make(chan error, 1)
	go func() { second <- ask(bob, f.b.storage, "Arsiv") }()
	select {
	case err := <-second:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		close(held.release)
		<-first
		t.Fatal("a request waited for another request's notice: the lock is held past filing")
	}
	close(held.release)
	require.NoError(t, <-first)
}

// A request's state and owner are the server's. A body that names a status,
// a tenant, a person, a storage or an expiry of its own still files a pending
// request of the caller, for the caller's tenant, on the storage its path
// names — and it reaches nobody else's list.
func TestE2ERequests_ABodyCannotChooseItsStateOrOwner(t *testing.T) {
	f := newE2EFix(t)
	ctx := context.Background()
	f.ensure(t, f.a, "Proje", model.E2ERequestFolder)
	st, raw := doReq(t, f.a.member, http.MethodPost, f.url("/api/files/e2e/requests"), map[string]any{
		"path": f.a.storage.Name + "://Proje", "kind": "folder", "reason": "sözleşmeler",
		"status": model.E2ERequestApproved, "provider_id": f.b.id, "tenant_id": f.b.id,
		"user_id": f.b.memberID, "requester_id": f.b.memberID, "storage_id": f.b.storage.ID,
		"decided_by": f.b.memberID, "expires_at": "2099-01-01T00:00:00Z",
	})
	require.Equal(t, http.StatusCreated, st, string(raw))
	req, _ := decodeE2EReq(t, raw)

	row, err := f.store.GetE2ERequest(ctx, req.ID)
	require.NoError(t, err)
	assert.Equal(t, model.E2ERequestPending, row.Status)
	require.NotNil(t, row.ProviderID)
	assert.Equal(t, f.a.id, *row.ProviderID, "the caller's tenant")
	assert.Equal(t, f.a.memberID, row.UserID, "the caller")
	assert.Equal(t, f.a.storage.ID, row.StorageID, "the storage the path names")
	assert.Nil(t, row.DecidedBy)
	assert.WithinDuration(t, f.clock.Now().Add(e2epolicy.ApprovalTTL), row.ExpiresAt, time.Second, "seven days from now")

	list := func(c *http.Client, url string) []e2eReq {
		t.Helper()
		st, raw := doReq(t, c, http.MethodGet, f.url(url), nil)
		require.Equal(t, http.StatusOK, st, string(raw))
		return decodeE2EReqs(t, raw)
	}
	assert.Len(t, list(f.a.admin, "/api/admin/e2e/requests"), 1, "the caller's tenant's administrator sees it")
	assert.Empty(t, list(f.b.admin, "/api/admin/e2e/requests?status=all"), "the tenant the body named does not")
	assert.Empty(t, list(f.b.member, "/api/files/e2e/requests"), "nor does the person it named")
}

// A token confined to a folder lists only its own requests inside it, as it
// sees nothing else outside it.
func TestE2ERequests_MyRequestsKeepToTheRoot(t *testing.T) {
	f := newE2EFix(t)
	useProductionAuthChain(t, f.store)
	for _, rel := range []string{"Proje/Alt", "Baska", "Proje2"} {
		st, raw := f.ask(t, f.a.member, f.a, rel, "folder")
		require.Equal(t, http.StatusCreated, st, string(raw))
	}
	tok := testutil.NewAPIToken(t, f.store, f.a.memberID, "read,root:"+f.a.storage.Name+"://Proje")

	st, raw := withToken(t, http.MethodGet, f.url("/api/files/e2e/requests"), tok, nil)
	require.Equal(t, http.StatusOK, st, string(raw))
	got := decodeE2EReqs(t, raw)
	require.Len(t, got, 1, "%s", raw)
	assert.Equal(t, f.a.storage.Name+"://Proje/Alt", got[0].Path)
	st, raw = doReq(t, f.a.member, http.MethodGet, f.url("/api/files/e2e/requests"), nil)
	require.Equal(t, http.StatusOK, st, string(raw))
	assert.Len(t, decodeE2EReqs(t, raw), 3, "the session is confined to nothing")
}

// The admin table lists one state at a time: pending unless ?status= names
// another, or `all`.
func TestE2ERequests_TheAdminListFiltersByState(t *testing.T) {
	f := newE2EFix(t)
	ctx := context.Background()
	st, raw := f.ask(t, f.a.member, f.a, "Suresi", "folder")
	require.Equal(t, http.StatusCreated, st, string(raw))
	expired, _ := decodeE2EReq(t, raw)
	f.clock.add(e2epolicy.ApprovalTTL + time.Minute)

	approved := f.askAndApprove(t, f.a, "Onayli", model.E2ERequestFolder)
	used := f.askAndApprove(t, f.a, "Kullanilan", model.E2ERequestFolder)
	require.NoError(t, f.policy.CheckCreate(ctx, f.user(t, f.a.memberID), f.a.storage, "Kullanilan/"+e2e.MarkerName))
	st, raw = f.ask(t, f.a.member, f.a, "Reddedilen", "folder")
	require.Equal(t, http.StatusCreated, st, string(raw))
	rejected, _ := decodeE2EReq(t, raw)
	st, raw = doReq(t, f.a.admin, http.MethodPost, f.url(fmt.Sprintf("/api/admin/e2e/requests/%d/reject", rejected.ID)), nil)
	require.Equal(t, http.StatusOK, st, string(raw))
	st, raw = f.ask(t, f.a.member, f.a, "Bekleyen", "folder")
	require.Equal(t, http.StatusCreated, st, string(raw))
	pending, _ := decodeE2EReq(t, raw)

	ids := func(query string) []int64 {
		t.Helper()
		st, raw := doReq(t, f.a.admin, http.MethodGet, f.url("/api/admin/e2e/requests"+query), nil)
		require.Equal(t, http.StatusOK, st, string(raw))
		out := []int64{}
		for _, r := range decodeE2EReqs(t, raw) {
			out = append(out, r.ID)
		}
		return out
	}
	assert.Equal(t, []int64{pending.ID}, ids(""), "pending, when no state is named")
	for query, want := range map[string]int64{
		"?status=pending": pending.ID, "?status=approved": approved, "?status=rejected": rejected.ID,
		"?status=expired": expired.ID, "?status=used": used,
	} {
		assert.Equal(t, []int64{want}, ids(query), query)
	}
	assert.ElementsMatch(t, []int64{pending.ID, approved, rejected.ID, expired.ID, used}, ids("?status=all"))
}
