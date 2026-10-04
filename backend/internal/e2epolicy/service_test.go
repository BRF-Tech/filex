package e2epolicy_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log"
	"log/slog"
	"path"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/e2epolicy"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/pathkey"
	"github.com/brf-tech/filex/backend/internal/perm"
	"github.com/brf-tech/filex/backend/internal/testutil"
)

// fix is one install: a storage, an administrator and a plain member (Ada),
// either in tenant "alpha" of a multi-tenant install or on a single-tenant
// one, and a Service on a stopped clock.
type fix struct {
	store  db.Store
	svc    *e2epolicy.Service
	tenant *model.Provider // nil: single-tenant
	st     *model.Storage
	admin  *model.User
	ada    *model.User
	now    time.Time
	used   []*model.E2ERequest // what OnUse heard
	opened []string            // and the folders it heard each was spent on
}

func newFix(t *testing.T, multiTenant bool) *fix {
	t.Helper()
	ctx := context.Background()
	_, store := testutil.NewTestDB(t)
	f := &fix{store: store, now: time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)}
	st, err := store.CreateStorage(ctx, &model.Storage{
		Name: "Depo", Driver: "local", MountPath: "/data", ConfigJSON: []byte(`{}`),
		SyncMode: model.SyncModePoll, SyncIntervalS: 900, Enabled: true,
	})
	require.NoError(t, err)
	f.st = st
	if multiTenant {
		f.tenant, err = store.CreateProvider(ctx, &model.Provider{Slug: "alpha", Name: "Alpha", AuthType: model.AuthTypeLocal, Enabled: true})
		require.NoError(t, err)
		require.NoError(t, store.LinkProviderStorage(ctx, f.tenant.ID, st.ID))
	}
	f.admin = f.user(t, "admin@alpha.test", model.RoleAdmin)
	f.ada = f.user(t, "ada@alpha.test", model.RoleUser)
	f.svc = f.serviceOn(store)
	// Permissions are cached process-wide; an override a test wrote must not
	// reach the next one.
	t.Cleanup(perm.Invalidate)
	return f
}

// serviceOn is a Service for this install reading through store: f.store
// itself, or a view of it that fails or races.
func (f *fix) serviceOn(store db.Store) *e2epolicy.Service {
	return e2epolicy.New(e2epolicy.Options{
		Store: store, MultiTenant: f.tenant != nil,
		Now: func() time.Time { return f.now },
		OnUse: func(_ context.Context, r *model.E2ERequest, _ *model.User, dir string) {
			f.used, f.opened = append(f.used, r), append(f.opened, dir)
		},
	})
}

// user makes an account homed in the fixture's tenant (the default provider
// on a single-tenant install) and reads it back as the handlers see it.
func (f *fix) user(t *testing.T, email, role string) *model.User {
	t.Helper()
	ctx := context.Background()
	u, err := f.store.CreateUser(ctx, email, "hash", role, "tr", "UTC")
	require.NoError(t, err)
	if f.tenant != nil {
		require.NoError(t, f.store.SetUserProvider(ctx, u.ID, f.tenant.ID, ""))
	}
	u, err = f.store.GetUser(ctx, u.ID)
	require.NoError(t, err)
	return u
}

// policy sets the policy where this install keeps it; allowed is the
// tenant's ceiling (a single-tenant install has none).
func (f *fix) policy(t *testing.T, allowed bool, policy string) {
	t.Helper()
	ctx := context.Background()
	if f.tenant != nil {
		require.NoError(t, f.store.SetProviderE2E(ctx, f.tenant.ID, model.ProviderE2E{Allowed: allowed, Policy: policy}))
		return
	}
	require.NoError(t, f.store.UpsertSetting(ctx, model.SettingE2EPolicy, policy))
}

// approve leaves what the requests service leaves when an administrator
// approves u's request to encrypt rel as kind: an approved row, expiring at
// expires.
func (f *fix) approve(t *testing.T, u *model.User, rel, kind string, expires time.Time) *model.E2ERequest {
	t.Helper()
	ctx := context.Background()
	var tenantID *int64
	if f.tenant != nil {
		tenantID = &f.tenant.ID
	}
	r, err := f.store.CreateE2ERequest(ctx, &model.E2ERequest{
		ProviderID: tenantID, UserID: u.ID, Requester: u.Email, StorageID: f.st.ID,
		Path: e2epolicy.ApprovalPath(rel, kind), Kind: kind, Reason: "bordrolar",
		ExpiresAt: f.now.Add(e2epolicy.ApprovalTTL),
	})
	require.NoError(t, err)
	decided := f.now
	r.Status, r.DecidedBy, r.Decider, r.DecidedAt, r.ExpiresAt = model.E2ERequestApproved, &f.admin.ID, f.admin.Email, &decided, expires
	ok, err := f.store.UpdateE2ERequest(ctx, r, model.E2ERequestPending)
	require.NoError(t, err)
	require.True(t, ok)
	return r
}

// file catalogues a file, as a listing would have.
func (f *fix) file(t *testing.T, rel string) {
	t.Helper()
	_, err := f.store.CreateNode(context.Background(), &model.Node{
		StorageID: f.st.ID, Name: path.Base(rel), Path: "/" + rel, PathHash: pathkey.Hash(f.st.ID, "/"+rel),
		Type: model.NodeTypeFile, Size: 3, Mime: "application/pdf",
	})
	require.NoError(t, err)
}

func (f *fix) status(t *testing.T, id int64) string {
	t.Helper()
	r, err := f.store.GetE2ERequest(context.Background(), id)
	require.NoError(t, err)
	return r.Status
}

// requireRefused asserts the rule's own refusal, for the reason given.
func requireRefused(t *testing.T, err error, why e2epolicy.Reason) {
	t.Helper()
	var refused *e2epolicy.RefusedError
	require.True(t, errors.As(err, &refused), "want a refusal (%s), got %v", why, err)
	require.Equal(t, why, refused.Reason)
}

// errBoom is how a failing store answers: anything but sql.ErrNoRows.
var errBoom = errors.New("boom")

// failing is the real store with either lookup behind the explorer's answer
// made to fail, and a count of how often the rule asks.
type failing struct {
	db.Store
	nodeErr, approvalErr     error
	nodeCalls, approvalCalls int
}

func (s *failing) GetNodeByPath(ctx context.Context, storageID int64, pathHash string) (*model.Node, error) {
	s.nodeCalls++
	if s.nodeErr != nil {
		return nil, s.nodeErr
	}
	return s.Store.GetNodeByPath(ctx, storageID, pathHash)
}

func (s *failing) FindApprovedE2ERequest(ctx context.Context, userID, storageID int64, at, kind string, now time.Time) (*model.E2ERequest, error) {
	s.approvalCalls++
	if s.approvalErr != nil {
		return nil, s.approvalErr
	}
	return s.Store.FindApprovedE2ERequest(ctx, userID, storageID, at, kind, now)
}

// racing is the real store with a rival between the rule's two steps: once
// the rule has found an approval, another write spends it, the way the
// service itself does, before the rule can. spends counts the approvals
// turned to used, by anyone.
type racing struct {
	db.Store
	t      *testing.T
	now    time.Time
	spends int
}

func (s *racing) FindApprovedE2ERequest(ctx context.Context, userID, storageID int64, at, kind string, now time.Time) (*model.E2ERequest, error) {
	r, err := s.Store.FindApprovedE2ERequest(ctx, userID, storageID, at, kind, now)
	if err != nil {
		return nil, err
	}
	rival, used := *r, s.now.UTC()
	rival.Status, rival.UsedAt = model.E2ERequestUsed, &used
	won, err := s.UpdateE2ERequest(ctx, &rival, model.E2ERequestApproved)
	require.NoError(s.t, err)
	require.True(s.t, won, "the rival has to win, or this is no race")
	return r, nil // as the rule found it: still approved
}

func (s *racing) UpdateE2ERequest(ctx context.Context, r *model.E2ERequest, onlyIfStatus string) (bool, error) {
	ok, err := s.Store.UpdateE2ERequest(ctx, r, onlyIfStatus)
	if ok && r.Status == model.E2ERequestUsed {
		s.spends++
	}
	return ok, err
}

// logged captures what slog writes for the rest of the test. SetDefault also
// points the log package at the new handler, and putting the old logger back
// does not undo that: restore the package's writer and flags by hand.
func logged(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	prev, out, flags := slog.Default(), log.Writer(), log.Flags()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	t.Cleanup(func() {
		slog.SetDefault(prev)
		log.SetOutput(out)
		log.SetFlags(flags)
	})
	return &buf
}

const (
	marker = "Muhasebe/Bordrolar/.filex-e2e.json"
	fxe    = "Muhasebe/rapor.pdf.fxe"
)

func TestPolicyFor_SingleTenantReadsTheSetting(t *testing.T) {
	f := newFix(t, false)
	ctx := context.Background()

	got, err := f.svc.PolicyFor(ctx, nil)
	require.NoError(t, err)
	assert.Equal(t, model.ProviderE2E{Allowed: true, Policy: model.E2EPolicyPermitted}, got, "no setting: what filex always did")

	f.policy(t, true, model.E2EPolicyAdmins)
	got, err = f.svc.PolicyFor(ctx, nil)
	require.NoError(t, err)
	assert.Equal(t, model.ProviderE2E{Allowed: true, Policy: model.E2EPolicyAdmins}, got)

	// The default provider's columns are not read on a single-tenant install.
	def, err := f.store.GetProviderBySlug(ctx, model.DefaultProviderSlug)
	require.NoError(t, err)
	require.NoError(t, f.store.SetProviderE2E(ctx, def.ID, model.ProviderE2E{Allowed: false, Policy: model.E2EPolicyOff}))
	got, err = f.svc.PolicyFor(ctx, &def.ID)
	require.NoError(t, err)
	assert.Equal(t, model.ProviderE2E{Allowed: true, Policy: model.E2EPolicyAdmins}, got)

	require.NoError(t, f.store.UpsertSetting(ctx, model.SettingE2EPolicy, "everyone"))
	got, err = f.svc.PolicyFor(ctx, nil)
	require.NoError(t, err)
	assert.Equal(t, model.E2EPolicyPermitted, got.Policy, "a value nobody knows reads as the default")
}

func TestPolicyFor_MultiTenantReadsTheTenant(t *testing.T) {
	f := newFix(t, true)
	ctx := context.Background()

	got, err := f.svc.PolicyFor(ctx, &f.tenant.ID)
	require.NoError(t, err)
	assert.Equal(t, model.ProviderE2E{Allowed: true, Policy: model.E2EPolicyPermitted}, got)

	f.policy(t, false, model.E2EPolicyApproval)
	got, err = f.svc.PolicyFor(ctx, &f.tenant.ID)
	require.NoError(t, err)
	assert.Equal(t, model.ProviderE2E{Allowed: false, Policy: model.E2EPolicyApproval}, got)

	got, err = f.svc.PolicyFor(ctx, nil)
	require.NoError(t, err)
	assert.Equal(t, model.ProviderE2E{Allowed: true, Policy: model.E2EPolicyPermitted}, got, "no tenant: the install's setting")

	missing := f.tenant.ID + 1000
	_, err = f.svc.PolicyFor(ctx, &missing)
	require.Error(t, err, "a tenant that is not there is an error, not a default")
}

// A tenant's member is judged by their tenant; a platform operator by the
// tenant whose storage it is, and by their own on a storage no tenant has.
func TestTenantFor(t *testing.T) {
	f := newFix(t, true)
	ctx := context.Background()

	got, err := f.svc.TenantFor(ctx, f.ada, f.st)
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, f.tenant.ID, *got)

	operator, err := f.store.CreateUser(ctx, "ops@platform.test", "hash", model.RoleAdmin, "tr", "UTC")
	require.NoError(t, err)
	operator, err = f.store.GetUser(ctx, operator.ID)
	require.NoError(t, err)
	got, err = f.svc.TenantFor(ctx, operator, f.st)
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, f.tenant.ID, *got, "the tenant's data, the tenant's rules")

	platform, err := f.store.CreateStorage(ctx, &model.Storage{
		Name: "Platform", Driver: "local", MountPath: "/platform", ConfigJSON: []byte(`{}`),
		SyncMode: model.SyncModePoll, SyncIntervalS: 900, Enabled: true,
	})
	require.NoError(t, err)
	got, err = f.svc.TenantFor(ctx, operator, platform)
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, *operator.ProviderID, *got, "a storage no tenant has is the platform's")

	single := newFix(t, false)
	got, err = single.svc.TenantFor(ctx, single.ada, single.st)
	require.NoError(t, err)
	assert.Nil(t, got, "a single-tenant install has no tenant")
}

// Names that encrypt nothing pass at once, whatever the policy and whoever
// writes — the rule costs a plain upload nothing.
func TestCheckCreate_OtherNamesPass(t *testing.T) {
	f := newFix(t, true)
	f.policy(t, false, model.E2EPolicyOff)
	ctx := context.Background()
	for _, rel := range []string{"Muhasebe/rapor.pdf", "Muhasebe/.filex-e2e.json.bak", "Muhasebe/kasa.fxe/ek.txt"} {
		assert.NoError(t, f.svc.CheckCreate(ctx, f.ada, f.st, rel), rel)
		assert.NoError(t, f.svc.CheckCreate(ctx, nil, nil, rel), rel)
	}
}

// Nothing configured: everybody who could encrypt before still can.
func TestCheckCreate_DefaultsChangeNothing(t *testing.T) {
	for _, multi := range []bool{false, true} {
		f := newFix(t, multi)
		ctx := context.Background()
		for _, u := range []*model.User{f.admin, f.ada} {
			require.NoError(t, f.svc.CheckCreate(ctx, u, f.st, marker), u.Email)
			require.NoError(t, f.svc.CheckCreate(ctx, u, f.st, fxe), u.Email)
			ans, why := f.svc.AnswerFor(ctx, u, f.st, "Muhasebe")
			assert.Equal(t, e2epolicy.AnswerAllowed, ans)
			assert.Equal(t, e2epolicy.Reason(""), why)
		}
	}
}

func TestCheckCreate_TenantCeilingBindsAdministrators(t *testing.T) {
	f := newFix(t, true)
	f.policy(t, false, model.E2EPolicyPermitted)
	ctx := context.Background()
	for _, u := range []*model.User{f.admin, f.ada} {
		requireRefused(t, f.svc.CheckCreate(ctx, u, f.st, marker), e2epolicy.ReasonTenantDisabled)
		requireRefused(t, f.svc.CheckCreate(ctx, u, f.st, fxe), e2epolicy.ReasonTenantDisabled)
		ans, why := f.svc.AnswerFor(ctx, u, f.st, "Muhasebe")
		assert.Equal(t, e2epolicy.AnswerDenied, ans)
		assert.Equal(t, e2epolicy.ReasonTenantDisabled, why)
	}
}

func TestCheckCreate_PolicyOffBindsAdministrators(t *testing.T) {
	for _, multi := range []bool{false, true} {
		f := newFix(t, multi)
		f.policy(t, true, model.E2EPolicyOff)
		ctx := context.Background()
		requireRefused(t, f.svc.CheckCreate(ctx, f.admin, f.st, marker), e2epolicy.ReasonPolicyOff)
		requireRefused(t, f.svc.CheckCreate(ctx, f.ada, f.st, fxe), e2epolicy.ReasonPolicyOff)
	}
}

func TestCheckCreate_AdminsOnly(t *testing.T) {
	f := newFix(t, true)
	f.policy(t, true, model.E2EPolicyAdmins)
	ctx := context.Background()
	require.NoError(t, f.svc.CheckCreate(ctx, f.admin, f.st, marker))
	requireRefused(t, f.svc.CheckCreate(ctx, f.ada, f.st, marker), e2epolicy.ReasonAdminsOnly)
	ans, why := f.svc.AnswerFor(ctx, f.ada, f.st, "Muhasebe")
	assert.Equal(t, e2epolicy.AnswerDenied, ans)
	assert.Equal(t, e2epolicy.ReasonAdminsOnly, why)
}

// files.encrypt taken away from one person refuses them and nobody else; a
// viewer never holds it.
func TestCheckCreate_PermissionDenied(t *testing.T) {
	f := newFix(t, true)
	ctx := context.Background()
	require.NoError(t, f.store.SetUserPermissionOverrides(ctx, f.ada.ID, map[string]string{string(perm.FilesEncrypt): model.PermDeny}, nil))
	perm.Invalidate()

	requireRefused(t, f.svc.CheckCreate(ctx, f.ada, f.st, marker), e2epolicy.ReasonPermission)
	requireRefused(t, f.svc.CheckCreate(ctx, f.ada, f.st, fxe), e2epolicy.ReasonPermission)
	ans, why := f.svc.AnswerFor(ctx, f.ada, f.st, "Muhasebe")
	assert.Equal(t, e2epolicy.AnswerDenied, ans)
	assert.Equal(t, e2epolicy.ReasonPermission, why)

	require.NoError(t, f.svc.CheckCreate(ctx, f.admin, f.st, marker), "the administrator is not narrowed")

	viewer := f.user(t, "viewer@alpha.test", model.RoleViewer)
	requireRefused(t, f.svc.CheckCreate(ctx, viewer, f.st, marker), e2epolicy.ReasonPermission)
	requireRefused(t, f.svc.CheckCreate(ctx, nil, f.st, marker), e2epolicy.ReasonPermission)
}

// Under the approval policy: no approval, a request; an approval is used by
// the first encryption and only by it.
func TestCheckCreate_ApprovalIsUsedOnce(t *testing.T) {
	f := newFix(t, true)
	f.policy(t, true, model.E2EPolicyApproval)
	ctx := context.Background()

	ans, why := f.svc.AnswerFor(ctx, f.ada, f.st, "Muhasebe/Bordrolar")
	assert.Equal(t, e2epolicy.AnswerRequest, ans)
	assert.Equal(t, e2epolicy.ReasonApprovalRequired, why)
	requireRefused(t, f.svc.CheckCreate(ctx, f.ada, f.st, marker), e2epolicy.ReasonApprovalRequired)
	require.NoError(t, f.svc.CheckCreate(ctx, f.admin, f.st, marker), "an administrator needs no approval")

	r := f.approve(t, f.ada, "Muhasebe/Bordrolar", model.E2ERequestFolder, f.now.Add(e2epolicy.ApprovalTTL))
	for i := 0; i < 2; i++ {
		ans, _ = f.svc.AnswerFor(ctx, f.ada, f.st, "Muhasebe/Bordrolar")
		assert.Equal(t, e2epolicy.AnswerAllowed, ans, "asking does not use it")
	}
	assert.Equal(t, model.E2ERequestApproved, f.status(t, r.ID))

	require.NoError(t, f.svc.CheckCreate(ctx, f.ada, f.st, marker))
	got, err := f.store.GetE2ERequest(ctx, r.ID)
	require.NoError(t, err)
	assert.Equal(t, model.E2ERequestUsed, got.Status)
	require.NotNil(t, got.UsedAt)
	assert.WithinDuration(t, f.now, *got.UsedAt, time.Second)
	require.Len(t, f.used, 1, "OnUse hears of it once")
	assert.Equal(t, r.ID, f.used[0].ID)
	assert.Equal(t, []string{"Muhasebe/Bordrolar"}, f.opened, "…and of the folder it was spent on")

	requireRefused(t, f.svc.CheckCreate(ctx, f.ada, f.st, marker), e2epolicy.ReasonApprovalRequired)
	ans, _ = f.svc.AnswerFor(ctx, f.ada, f.st, "Muhasebe/Bordrolar")
	assert.Equal(t, e2epolicy.AnswerRequest, ans)
	assert.Len(t, f.used, 1)
}

// CheckCreateWithoutApproval answers what CheckCreate answers, except that
// it never uses an approval: where CheckCreate would spend one it refuses
// with approval_required, the approval stays approved, and OnUse hears
// nothing. The file request's door (a visitor's upload judged as the link
// creator's) asks it.
func TestCheckCreateWithoutApproval_NeverSpendsOne(t *testing.T) {
	f := newFix(t, true)
	ctx := context.Background()
	f.policy(t, true, model.E2EPolicyApproval)
	r := f.approve(t, f.ada, "Muhasebe/Bordrolar", model.E2ERequestFolder, f.now.Add(e2epolicy.ApprovalTTL))
	for i := 0; i < 2; i++ {
		requireRefused(t, f.svc.CheckCreateWithoutApproval(ctx, f.ada, f.st, marker), e2epolicy.ReasonApprovalRequired)
	}
	assert.Equal(t, model.E2ERequestApproved, f.status(t, r.ID), "the approval was spent")
	assert.Empty(t, f.used, "OnUse heard of a use")
	require.NoError(t, f.svc.CheckCreateWithoutApproval(ctx, f.admin, f.st, marker), "an administrator needs no approval")
	require.NoError(t, f.svc.CheckCreateWithoutApproval(ctx, f.ada, f.st, "Muhasebe/Bordrolar/notlar.txt"), "a name that encrypts nothing")

	// The other answers are CheckCreate's.
	f.policy(t, true, model.E2EPolicyPermitted)
	require.NoError(t, f.svc.CheckCreateWithoutApproval(ctx, f.ada, f.st, marker))
	f.policy(t, true, model.E2EPolicyOff)
	requireRefused(t, f.svc.CheckCreateWithoutApproval(ctx, f.ada, f.st, marker), e2epolicy.ReasonPolicyOff)
	requireRefused(t, f.svc.CheckCreateWithoutApproval(ctx, nil, f.st, marker), e2epolicy.ReasonPermission)
	assert.Equal(t, model.E2ERequestApproved, f.status(t, r.ID))
}

// "Make an encrypted folder here" is asked from the folder the person is in:
// that folder's NEW-FOLDER approval covers a new folder inside it (operator
// decision 2026-10-03: a kind of its own, kinds_test.go).
func TestCheckCreate_ApprovalCoversANewFolderInside(t *testing.T) {
	f := newFix(t, true)
	f.policy(t, true, model.E2EPolicyApproval)
	ctx := context.Background()
	r := f.approve(t, f.ada, "Muhasebe", model.E2ERequestNewFolder, f.now.Add(e2epolicy.ApprovalTTL))

	ans, _ := f.svc.AnswerForKind(ctx, f.ada, f.st, "Muhasebe", model.E2ERequestNewFolder)
	assert.Equal(t, e2epolicy.AnswerAllowed, ans)
	require.NoError(t, f.svc.CheckCreate(ctx, f.ada, f.st, "Muhasebe/Yeni/.filex-e2e.json"))
	assert.Equal(t, model.E2ERequestUsed, f.status(t, r.ID))
	assert.Equal(t, []string{"Muhasebe/Yeni"}, f.opened, "OnUse hears of the folder encrypted, not the one approved")
	requireRefused(t, f.svc.CheckCreate(ctx, f.ada, f.st, "Muhasebe/Yeni2/.filex-e2e.json"), e2epolicy.ReasonApprovalRequired)
}

// A file's approval is its folder's: the .fxe may take any name there,
// hidden or not. A folder's approval is no file's, and the reverse.
func TestCheckCreate_FileApprovalIsTheFolders(t *testing.T) {
	f := newFix(t, true)
	f.policy(t, true, model.E2EPolicyApproval)
	ctx := context.Background()
	f.file(t, "Muhasebe/rapor.pdf")

	ans, _ := f.svc.AnswerFor(ctx, f.ada, f.st, "Muhasebe/rapor.pdf")
	assert.Equal(t, e2epolicy.AnswerRequest, ans)

	folderOnly := f.approve(t, f.ada, "Muhasebe", model.E2ERequestFolder, f.now.Add(e2epolicy.ApprovalTTL))
	ans, _ = f.svc.AnswerFor(ctx, f.ada, f.st, "Muhasebe/rapor.pdf")
	assert.Equal(t, e2epolicy.AnswerRequest, ans, "a folder's approval is no file's")
	requireRefused(t, f.svc.CheckCreate(ctx, f.ada, f.st, "Muhasebe/Zm9vYmFyYmF6cXV4.fxe"), e2epolicy.ReasonApprovalRequired)

	r := f.approve(t, f.ada, "Muhasebe/rapor.pdf", model.E2ERequestFile, f.now.Add(e2epolicy.ApprovalTTL))
	assert.Equal(t, "Muhasebe", r.Path)
	ans, _ = f.svc.AnswerFor(ctx, f.ada, f.st, "Muhasebe/rapor.pdf")
	assert.Equal(t, e2epolicy.AnswerAllowed, ans)
	require.NoError(t, f.svc.CheckCreate(ctx, f.ada, f.st, "Muhasebe/Zm9vYmFyYmF6cXV4.fxe"), "a hidden name")
	assert.Equal(t, model.E2ERequestUsed, f.status(t, r.ID))
	assert.Equal(t, model.E2ERequestApproved, f.status(t, folderOnly.ID), "the folder's approval is still there")
}

func TestCheckCreate_ApprovalIsThePersonsAndExpires(t *testing.T) {
	f := newFix(t, true)
	f.policy(t, true, model.E2EPolicyApproval)
	ctx := context.Background()

	bob := f.user(t, "bob@alpha.test", model.RoleUser)
	bobs := f.approve(t, bob, "Muhasebe/Bordrolar", model.E2ERequestFolder, f.now.Add(e2epolicy.ApprovalTTL))
	requireRefused(t, f.svc.CheckCreate(ctx, f.ada, f.st, marker), e2epolicy.ReasonApprovalRequired)
	assert.Equal(t, model.E2ERequestApproved, f.status(t, bobs.ID), "somebody else's approval is not touched")

	stale := f.approve(t, f.ada, "Muhasebe/Bordrolar", model.E2ERequestFolder, f.now.Add(-time.Hour))
	requireRefused(t, f.svc.CheckCreate(ctx, f.ada, f.st, marker), e2epolicy.ReasonApprovalRequired)
	assert.Equal(t, model.E2ERequestApproved, f.status(t, stale.ID), "an expired approval is not used")
	assert.Empty(t, f.used)
}

// A new-folder approval reaches a new encrypted folder just inside the folder
// it was given for (ApprovalCoversANewFolderInside), and stops there: not two
// folders down, not the folder itself (kinds_test.go).
func TestCheckCreate_ApprovalStopsOneFolderDown(t *testing.T) {
	f := newFix(t, true)
	f.policy(t, true, model.E2EPolicyApproval)
	ctx := context.Background()
	r := f.approve(t, f.ada, "Muhasebe", model.E2ERequestNewFolder, f.now.Add(e2epolicy.ApprovalTTL))

	requireRefused(t, f.svc.CheckCreate(ctx, f.ada, f.st, "Muhasebe/A/B/.filex-e2e.json"), e2epolicy.ReasonApprovalRequired)
	assert.Equal(t, model.E2ERequestApproved, f.status(t, r.ID), "asking out of its reach spends nothing")
	assert.Empty(t, f.used)

	// It was live all along: what it does cover still goes through.
	require.NoError(t, f.svc.CheckCreate(ctx, f.ada, f.st, "Muhasebe/A/.filex-e2e.json"))
	assert.Equal(t, model.E2ERequestUsed, f.status(t, r.ID))
}

// A file's approval is its folder's, and goes no further than that folder: a
// .fxe made in a folder below it needs its own.
func TestCheckCreate_FileApprovalStopsAtItsFolder(t *testing.T) {
	f := newFix(t, true)
	f.policy(t, true, model.E2EPolicyApproval)
	ctx := context.Background()
	r := f.approve(t, f.ada, "Muhasebe/rapor.pdf", model.E2ERequestFile, f.now.Add(e2epolicy.ApprovalTTL))
	require.Equal(t, "Muhasebe", r.Path)

	requireRefused(t, f.svc.CheckCreate(ctx, f.ada, f.st, "Muhasebe/Alt/Zm9vYmFyYmF6cXV4.fxe"), e2epolicy.ReasonApprovalRequired)
	assert.Equal(t, model.E2ERequestApproved, f.status(t, r.ID), "asking out of its reach spends nothing")
	assert.Empty(t, f.used)

	// It was live all along: its own folder still takes the file.
	require.NoError(t, f.svc.CheckCreate(ctx, f.ada, f.st, "Muhasebe/Zm9vYmFyYmF6cXV4.fxe"))
	assert.Equal(t, model.E2ERequestUsed, f.status(t, r.ID))
}

// Two writes of one person race for one approval. The one that loses is
// refused, and the approval is used once, by the winner alone: OnUse is not
// told of a write that did not spend it.
func TestCheckCreate_ALostRaceForAnApprovalIsRefused(t *testing.T) {
	f := newFix(t, true)
	f.policy(t, true, model.E2EPolicyApproval)
	ctx := context.Background()
	r := f.approve(t, f.ada, "Muhasebe/Bordrolar", model.E2ERequestFolder, f.now.Add(e2epolicy.ApprovalTTL))
	rival := &racing{Store: f.store, t: t, now: f.now}

	requireRefused(t, f.serviceOn(rival).CheckCreate(ctx, f.ada, f.st, marker), e2epolicy.ReasonApprovalRequired)
	assert.Empty(t, f.used, "OnUse hears of the write that spent the approval, not of the one that lost")
	assert.Equal(t, 1, rival.spends, "the approval was used once")
	assert.Equal(t, model.E2ERequestUsed, f.status(t, r.ID))
}

func TestAnswersFor(t *testing.T) {
	f := newFix(t, true)
	f.policy(t, true, model.E2EPolicyApproval)
	ctx := context.Background()
	f.approve(t, f.ada, "Muhasebe", model.E2ERequestFolder, f.now.Add(e2epolicy.ApprovalTTL))

	got := f.svc.AnswersFor(ctx, f.ada, f.st, []string{"Muhasebe", "Arşiv", ""})
	assert.Equal(t, []e2epolicy.Answer{e2epolicy.AnswerAllowed, e2epolicy.AnswerRequest, e2epolicy.AnswerRequest}, got)
	assert.Equal(t, []e2epolicy.Answer{e2epolicy.AnswerDenied, e2epolicy.AnswerDenied},
		f.svc.AnswersFor(ctx, nil, f.st, []string{"Muhasebe", "Arşiv"}), "nobody may not")
}

// A lookup that fails is not "nothing there". Read that way, the explorer
// would offer "Request encryption" on a guess, and a listing would ask a
// failing store once per row. The answer is no, and the log says why, once.
func TestAnswers_ALookupThatFailsDenies(t *testing.T) {
	f := newFix(t, true)
	f.policy(t, true, model.E2EPolicyApproval)
	ctx := context.Background()
	rels := []string{"Muhasebe", "Arşiv", "Bordro", ""}
	logs := logged(t)

	// The control: the same view of the store, nothing failing, no approval.
	// That is a plain request, and nothing to log.
	ans, why := f.serviceOn(&failing{Store: f.store}).AnswerFor(ctx, f.ada, f.st, "Muhasebe")
	require.Equal(t, e2epolicy.AnswerRequest, ans)
	require.Equal(t, e2epolicy.ReasonApprovalRequired, why)
	require.NotContains(t, logs.String(), "level=WARN", "no approval is not an error")

	for _, c := range []struct {
		name string
		fail func(*failing)
		hits func(*failing) int // how often the lookup that fails was asked
	}{
		{"the node lookup", func(s *failing) { s.nodeErr = errBoom }, func(s *failing) int { return s.nodeCalls }},
		{"the approval lookup", func(s *failing) { s.approvalErr = errBoom }, func(s *failing) int { return s.approvalCalls }},
	} {
		t.Run(c.name, func(t *testing.T) {
			one := &failing{Store: f.store}
			c.fail(one)
			logs.Reset()
			ans, why := f.serviceOn(one).AnswerFor(ctx, f.ada, f.st, "Muhasebe")
			assert.Equal(t, e2epolicy.AnswerDenied, ans, "a lookup that failed is not an approval that is missing")
			assert.Equal(t, e2epolicy.ReasonPermission, why)
			assert.Equal(t, 1, strings.Count(logs.String(), "level=WARN"))
			for _, want := range []string{fmt.Sprintf("user_id=%d", f.ada.ID), fmt.Sprintf("storage_id=%d", f.st.ID), "boom"} {
				assert.Contains(t, logs.String(), want, "the log names who, where and what failed")
			}

			many := &failing{Store: f.store}
			c.fail(many)
			logs.Reset()
			got := f.serviceOn(many).AnswersFor(ctx, f.ada, f.st, rels)
			denied := e2epolicy.AnswerDenied
			assert.Equal(t, []e2epolicy.Answer{denied, denied, denied, denied}, got, "the path that failed and every one after it")
			assert.Equal(t, 1, c.hits(many), "the failing store is asked once, not once per path")
			assert.LessOrEqual(t, many.nodeCalls, 1)
			assert.LessOrEqual(t, many.approvalCalls, 1)
			assert.Equal(t, 1, strings.Count(logs.String(), "level=WARN"), "one line for the whole listing")
		})
	}
}
