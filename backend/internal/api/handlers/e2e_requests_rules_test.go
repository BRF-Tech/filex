package handlers_test

// The approval policy's rules decided on 2026-10-03, over real HTTP:
//
//   - the kinds are separate, and the explorer's question names one
//     (POST /api/files/e2e/allowed with `kind`), answered exactly as the door
//     would answer it;
//   - the platform operator SEES every tenant's requests and DECIDES only the
//     platform's own; a tenant's request is not in the operator's bell;
//   - a request names something that is there, and one person has at most
//     twenty waiting.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/notify"
	"github.com/brf-tech/filex/backend/internal/testutil"
	"github.com/brf-tech/filex/backend/internal/testutil/dbtest"
)

// allowedAt asks POST /api/files/e2e/allowed as c, one item per (rel, kind)
// on the tenant's storage, and answers the answers and the reasons.
func (f *e2eFix) allowedAt(t *testing.T, c *http.Client, tn e2eTenant, asks ...[2]string) ([]string, []string) {
	t.Helper()
	items := make([]map[string]string, len(asks))
	for i, a := range asks {
		items[i] = map[string]string{"path": tn.storage.Name + "://" + a[0], "kind": a[1]}
	}
	st, raw := doReq(t, c, http.MethodPost, f.url("/api/files/e2e/allowed"), map[string]any{"items": items})
	require.Equal(t, http.StatusOK, st, string(raw))
	var out struct {
		Encrypt []string `json:"encrypt"`
		Reasons []string `json:"reasons"`
	}
	require.NoError(t, json.Unmarshal(raw, &out), string(raw))
	return out.Encrypt, out.Reasons
}

// The explorer names the kind it asks about, and hears the answer the door
// gives for that kind: a new-folder approval of Proje answers "allowed" for a
// new folder in Proje, and "request" for encrypting Proje where it is or a
// folder inside it that holds something.
func TestE2EAllowed_TheExplorerAsksForOneKind(t *testing.T) {
	f := newE2EFix(t)
	f.ensure(t, f.a, "Proje", "folder")
	f.catalogue(t, f.a, "Proje/Ekip", model.NodeTypeDirectory)
	f.catalogue(t, f.a, "Proje/Ekip/bordro.pdf", model.NodeTypeFile)
	dbtest.ApproveE2E(t, f.store, f.a.memberID, f.a.storage.ID, "Proje", "new_folder")

	answers, reasons := f.allowedAt(t, f.a.member, f.a,
		[2]string{"Proje", "new_folder"},
		[2]string{"Proje", "folder"},
		[2]string{"Proje/Ekip", "folder"},
		[2]string{"Proje", "disk"},
	)
	assert.Equal(t, []string{"allowed", "request", "request", "denied"}, answers)
	require.Len(t, reasons, 4, "each answer says which layer, as a 403 would")
	assert.Equal(t, []string{"", "approval_required", "approval_required"}, reasons[:3])
}

// The platform operator sees every tenant's requests and decides none but the
// platform's own; the tenant's administrator decides theirs. The operator's
// bell is not told of a tenant's request, nor does it count as waiting for
// them.
func TestE2ERequests_TheOperatorSeesButDoesNotDecide(t *testing.T) {
	f := newE2EFix(t)
	st, raw := f.ask(t, f.a.member, f.a, "Proje", "folder")
	require.Equal(t, http.StatusCreated, st, string(raw))
	req, _ := decodeE2EReq(t, raw)

	list := func(c *http.Client) []map[string]any {
		t.Helper()
		st, raw := doReq(t, c, http.MethodGet, f.url("/api/admin/e2e/requests"), nil)
		require.Equal(t, http.StatusOK, st, string(raw))
		var out struct {
			Requests []map[string]any `json:"requests"`
		}
		require.NoError(t, json.Unmarshal(raw, &out), string(raw))
		return out.Requests
	}
	ops := list(f.super)
	require.Len(t, ops, 1, "the operator sees the tenant's request")
	assert.Equal(t, false, ops[0]["decidable"], "and may not answer it")
	mine := list(f.a.admin)
	require.Len(t, mine, 1)
	assert.Equal(t, true, mine[0]["decidable"], "the tenant's administrator answers it")

	pending := func(c *http.Client) int {
		t.Helper()
		st, raw := doReq(t, c, http.MethodGet, f.url("/api/admin/e2e"), nil)
		require.Equal(t, http.StatusOK, st, string(raw))
		return decodeE2EPolicy(t, raw).Pending
	}
	assert.Equal(t, 0, pending(f.super), "nothing is waiting for the operator")
	assert.Equal(t, 1, pending(f.a.admin))
	assert.Empty(t, f.bell(t, f.super, notify.EventE2ERequestCreated), "the operator's bell is not told")

	for _, verb := range []string{"approve", "reject"} {
		st, body := doJSON(t, f.super, http.MethodPost, f.url(fmt.Sprintf("/api/admin/e2e/requests/%d/%s", req.ID, verb)),
			map[string]any{"reason": "operatör"})
		require.Equal(t, http.StatusForbidden, st, "%s: %v", verb, body)
		assert.Equal(t, "not_decidable", body["error"])
	}
	row, err := f.store.GetE2ERequest(context.Background(), req.ID)
	require.NoError(t, err)
	assert.Equal(t, model.E2ERequestPending, row.Status, "the operator changed it")

	st, raw = doReq(t, f.a.admin, http.MethodPost, f.url(fmt.Sprintf("/api/admin/e2e/requests/%d/approve", req.ID)), nil)
	require.Equal(t, http.StatusOK, st, string(raw))
}

// The platform's own request - a member of the supertenant asking about a
// storage of no tenant - is the operator's to decide, and reaches the
// operator's bell, addressed to each of the platform's administrators, with
// one webhook delivery.
func TestE2ERequests_ThePlatformsOwnRequestIsTheOperators(t *testing.T) {
	f := newE2EFix(t)
	ctx := context.Background()
	sup, err := f.store.GetSupertenant(ctx)
	require.NoError(t, err)
	require.NoError(t, f.store.SetProviderE2E(ctx, sup.ID, model.ProviderE2E{Allowed: true, Policy: model.E2EPolicyApproval}))
	memberID := seedUserIn(t, f.store, sup.ID, "uye@platform.test")
	member := freshClient(t)
	testutil.LoginAs(t, f.srv, member, "uye@platform.test", "VictimPass!1")
	second := seedUserIn(t, f.store, sup.ID, "admin2@platform.test")
	require.NoError(t, f.store.UpdateUserRole(ctx, second, model.RoleAdmin))
	shared, err := f.store.CreateStorage(ctx, &model.Storage{
		Name: "ortak", Driver: "local", MountPath: "/ortak", ConfigJSON: json.RawMessage(`{"root":"/tmp/ortak"}`),
		SyncMode: model.SyncModePoll, SyncIntervalS: 900, Enabled: true,
	})
	require.NoError(t, err)
	platform := e2eTenant{id: sup.ID, member: member, memberID: memberID, storage: shared}

	st, raw := f.ask(t, member, platform, "Belgeler", "folder")
	require.Equal(t, http.StatusCreated, st, string(raw))
	req, _ := decodeE2EReq(t, raw)
	require.NotNil(t, req.TenantID)
	assert.Equal(t, sup.ID, *req.TenantID)

	assert.Len(t, f.bell(t, f.super, notify.EventE2ERequestCreated), 1, "the operator is told of the platform's own")
	all, _, err := f.notif.History(ctx, 0, false, 50, 0)
	require.NoError(t, err)
	var rows []*model.Notification
	for _, n := range all {
		if n.Event == string(notify.EventE2ERequestCreated) {
			rows = append(rows, n)
		}
	}
	require.Len(t, rows, 2, "one row for each of the platform's administrators")
	skipped := 0
	for _, n := range rows {
		assert.NotNil(t, n.UserID, "addressed, not a broadcast")
		if n.WebhookStatus == string(notify.WebhookStatusSkipped) && n.WebhookError != "no webhook URL configured" {
			skipped++
		}
	}
	assert.Equal(t, 1, skipped, "the webhook is told once, with the first row")

	st, raw = doReq(t, f.super, http.MethodPost, f.url(fmt.Sprintf("/api/admin/e2e/requests/%d/approve", req.ID)), nil)
	require.Equal(t, http.StatusOK, st, "the operator decides the platform's own: %s", raw)
}

// A request names a folder or a file that is there: nothing there is
// path_missing, and the explorer's dialog says so.
func TestE2ERequests_ARequestNamesWhatIsThere(t *testing.T) {
	f := newE2EFix(t)
	for _, c := range []struct{ rel, kind string }{{"Yok", "folder"}, {"Yok", "new_folder"}, {"Yok/rapor.pdf", "file"}} {
		st, body := doJSON(t, f.a.member, http.MethodPost, f.url("/api/files/e2e/requests"), map[string]any{
			"path": f.a.storage.Name + "://" + c.rel, "kind": c.kind, "reason": "x",
		})
		require.Equal(t, http.StatusNotFound, st, "%s as %s: %v", c.rel, c.kind, body)
		assert.Equal(t, "path_missing", body["error"], "%s as %s", c.rel, c.kind)
	}
	assert.Empty(t, e2eAuditRows(t, f.store, "e2e_request.create"), "a request was filed for nothing")
	assert.Empty(t, f.bell(t, f.a.admin, notify.EventE2ERequestCreated), "an administrator was told of nothing")
}

// One person has at most twenty requests waiting: the twenty-first is refused
// (429 too_many_pending) until one is answered, and asking again for one that
// waits is still answered.
func TestE2ERequests_OnePersonHasAtMostTwentyWaiting(t *testing.T) {
	f := newE2EFix(t)
	for i := 0; i < 20; i++ {
		st, raw := f.ask(t, f.a.member, f.a, fmt.Sprintf("Klasor%02d", i), "folder")
		require.Equal(t, http.StatusCreated, st, "request %d: %s", i, raw)
	}
	f.ensure(t, f.a, "Klasor20", "folder")
	st, body := doJSON(t, f.a.member, http.MethodPost, f.url("/api/files/e2e/requests"), map[string]any{
		"path": f.a.storage.Name + "://Klasor20", "kind": "folder", "reason": "x",
	})
	require.Equal(t, http.StatusTooManyRequests, st, "%v", body)
	assert.Equal(t, "too_many_pending", body["error"])
	assert.Len(t, e2eAuditRows(t, f.store, "e2e_request.create"), 20)

	st, raw := f.ask(t, f.a.member, f.a, "Klasor03", "folder")
	require.Equal(t, http.StatusOK, st, "asking again for one waiting: %s", raw)
	first, _ := decodeE2EReq(t, raw)
	st, raw = doReq(t, f.a.admin, http.MethodPost, f.url(fmt.Sprintf("/api/admin/e2e/requests/%d/reject", first.ID)),
		map[string]any{"reason": "gerek yok"})
	require.Equal(t, http.StatusOK, st, string(raw))
	st, raw = f.ask(t, f.a.member, f.a, "Klasor20", "folder")
	require.Equal(t, http.StatusCreated, st, "one answered, one more may wait: %s", raw)

	// Somebody else is not held back by Ada's twenty.
	st, raw = f.ask(t, f.b.member, f.b, "Proje", "folder")
	require.Equal(t, http.StatusCreated, st, string(raw))
}
