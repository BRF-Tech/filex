// Package e2epolicy — requests.go
//
// The approval half of the policy (`approval`): somebody who holds
// files.encrypt asks first, and an administrator of the tenant decides.
//
// # What a request is
//
// A PERSON, a FOLDER P and a KIND, for this account (operator decision
// 2026-10-03: the kinds are separate, and an approval opens only what was
// asked, in the folder it was asked for, never in a folder below it):
//
//   - folder: encrypt P itself, where it is (what P holds is encrypted in
//     place);
//   - new_folder: make one NEW encrypted folder directly inside P, spent only
//     on a folder that holds nothing (Service.approvalPlaces);
//   - file: one single encrypted file (`.fxe`) in P.
//
// It is kept under ApprovalPath, where CheckCreate and AnswerFor look for it:
// a file's request is its folder's, because the name a `.fxe` is stored under
// cannot be known when the person asks. Approved, it opens exactly one
// encryption of its kind, once (CheckCreate marks it `used`; UseRecorder logs
// it, with the folder it was spent on), within ApprovalTTL of the approval.
// A request nobody decides expires after ApprovalTTL too, and past it nobody
// can decide it any more. One request per person, folder and kind waits at a
// time: asking again answers the one already waiting. A person has at most
// MaxPendingPerPerson waiting at once (too_many_pending past it).
//
// A request is only ever what the rule itself answers `request` for
// (Service.AnswerForKind, asked with the request's kind): under the approval
// policy, with files.encrypt there, and no approval of that kind already
// waiting to be spent. What it names must be there and be of its kind: a
// folder for the two folder kinds, a file for a file (path_missing,
// kind_mismatch otherwise). Anything else would be a row an administrator
// could approve while the create door refused anyway, or never asked.
//
// # Who decides
//
// The administrators of the tenant whose policy asked for the approval
// (Service.TenantFor), which is what the row's provider_id records — nil on a
// single-tenant install. A tenant administrator lists and decides their own
// tenant's requests only, and another tenant's reads as missing (404, the
// reasoning of handlers/tenantown.go). A single-tenant install's
// administrators see and decide every request. The platform operator (an
// administrator of the supertenant on a multi-tenant install) SEES every
// tenant's requests but decides only the platform's own: the supertenant's,
// or one filed under no tenant. Another tenant's is that tenant's
// administrators' decision (not_decidable, MayDecide; operator decision
// 2026-10-03).
//
// # What is announced
//
// notify.EventE2ERequestCreated, to whoever decides it and to nobody else
// (announce): a tenant's request is ONE broadcast placed on the folder, so
// the bell hands it to the administrators of the tenant that storage belongs
// to, while the platform operator's bell does not take it (notify
// PlatformAdminBell); a webhook gets one delivery. The platform's own request
// on a multi-tenant install is addressed to each administrator of the
// supertenant instead, the webhook riding on the first of those rows.
// notify.EventE2ERequestDecided — to the requester alone.
//
// Every step writes its own audit row (audit.go): create, approve, reject,
// expire, use.
package e2epolicy

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/brf-tech/filex/backend/internal/acl"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/notify"
)

// TTLDays is ApprovalTTL in days, as the admin table says it.
func TTLDays() int { return int(ApprovalTTL / (24 * time.Hour)) }

// maxRequestChars caps a reason or a decision note, in characters; the rest is
// cut, not refused.
const maxRequestChars = 2000

// sweepEvery is how often StartSweeper closes what is past its time.
const sweepEvery = time.Hour

// expireBatch is how many due rows ExpireDue reads at a time: one full
// ListE2ERequests, the store's own cap.
const expireBatch = 500

// MaxPendingPerPerson is how many requests one person may have waiting at
// once (operator decision 2026-10-03). Every new request tells the
// administrators, so a script must not be able to bury them: the next one is
// refused (429 too_many_pending) until one of the waiting ones is decided or
// expires. Asking again for a request already waiting is not a new one and
// is always answered.
const MaxPendingPerPerson = 20

// RequestsOptions wire a Requests.
type RequestsOptions struct {
	// Store keeps the rows (e2e_requests) and the audit log.
	Store db.Store
	// Policy answers whether a request is what the rule asks for, and whose
	// tenant files it.
	Policy *Service
	// Notify tells the administrators and the requester (nil: nobody).
	Notify notify.Service
	Now    func() time.Time
	Log    *slog.Logger
}

// Requests keeps the encryption requests.
type Requests struct {
	o RequestsOptions
	// mu makes "is one waiting for this person and folder? no → create" one
	// step (fileOnce), and is held for nothing else.
	mu sync.Mutex
}

// NewRequests builds a Requests.
func NewRequests(o RequestsOptions) *Requests {
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Log == nil {
		o.Log = slog.Default()
	}
	return &Requests{o: o}
}

func (q *Requests) now() time.Time { return q.o.Now().UTC() }

// RequestError is a refusal the HTTP layer answers as it is.
type RequestError struct {
	Status  int
	Code    string
	Message string
	// Answer and Reason are the rule's own, when the refusal is that no
	// request is needed here or none could help (code not_requestable).
	Answer Answer
	Reason Reason
}

func (e *RequestError) Error() string { return e.Code + ": " + e.Message }

func refuseRequest(status int, code, message string) *RequestError {
	return &RequestError{Status: status, Code: code, Message: message}
}

// cut trims s and keeps at most n characters of it.
func cut(s string, n int) string {
	s = strings.TrimSpace(s)
	if r := []rune(s); len(r) > n {
		return string(r[:n])
	}
	return s
}

// CreateInput is one request as the requester asks for it.
type CreateInput struct {
	User    *model.User
	Storage *model.Storage
	// Path is what the person asks about, relative to the storage: for kind
	// folder the folder to encrypt where it is, for kind new_folder the folder
	// to make a new encrypted folder in, and for kind file the file to
	// encrypt.
	Path   string
	Kind   string
	Reason string
	Who    Actor
}

// notRequestable says why no request is filed.
func notRequestable(a Answer) string {
	if a == AnswerAllowed {
		return "no approval is needed here: go ahead and encrypt"
	}
	return "encrypting here is not allowed, and a request would not change that"
}

// Create files a request — or, when one already waits for this person, folder
// and kind, answers that one (created=false) and records nothing.
func (q *Requests) Create(ctx context.Context, in CreateInput) (*model.E2ERequest, bool, error) {
	q.ExpireDue(ctx)
	if in.User == nil || in.Storage == nil {
		return nil, false, refuseRequest(http.StatusUnauthorized, "unauthorized", "a request is a signed-in person's, about a storage")
	}
	kind := strings.ToLower(strings.TrimSpace(in.Kind))
	rel := acl.CleanRel(in.Path)
	switch {
	case !model.ValidE2ERequestKind(kind):
		return nil, false, refuseRequest(http.StatusBadRequest, "bad_request", "kind must be folder, new_folder or file")
	case kind == model.E2ERequestFile && rel == "":
		return nil, false, refuseRequest(http.StatusBadRequest, "bad_request", "a file request names the file")
	case IsEncryptionName(rel):
		return nil, false, refuseRequest(http.StatusBadRequest, "bad_request",
			"a request names what is to be encrypted, not a key file or an encrypted file")
	}
	reason := cut(in.Reason, maxRequestChars)
	if reason == "" {
		return nil, false, refuseRequest(http.StatusBadRequest, "reason_required",
			"say why it must be encrypted (`reason`): the administrator who decides reads it")
	}
	answer, why, err := q.o.Policy.answerForRequest(ctx, in.User, in.Storage, rel, kind)
	switch {
	case errors.Is(err, errKindMismatch):
		return nil, false, refuseRequest(http.StatusBadRequest, "kind_mismatch",
			"kind must be what is there: folder or new_folder for a folder, file for a file")
	case errors.Is(err, errMissing):
		return nil, false, refuseRequest(http.StatusNotFound, "path_missing",
			"nothing is there: a request names a folder or a file that exists")
	}
	if answer != AnswerRequest {
		return nil, false, &RequestError{Status: http.StatusBadRequest, Code: "not_requestable",
			Message: notRequestable(answer), Answer: answer, Reason: why}
	}
	tenant, err := q.o.Policy.TenantFor(ctx, in.User, in.Storage)
	if err != nil {
		return nil, false, err
	}
	row, created, err := q.fileOnce(ctx, &model.E2ERequest{
		ProviderID: tenant, UserID: in.User.ID, Requester: in.User.Label(),
		StorageID: in.Storage.ID, Path: ApprovalPath(rel, kind), Kind: kind, Reason: reason,
		Status: model.E2ERequestPending, ExpiresAt: q.now().Add(ApprovalTTL),
	})
	if err != nil || !created {
		return row, false, err
	}
	q.audit(ctx, AuditActionRequestCreate, row, in.Who, nil)
	q.announce(ctx, row)
	return row, true, nil
}

// fileOnce files r unless this person already has one waiting for its folder
// and kind, which it answers instead (created=false), or already has
// MaxPendingPerPerson waiting (too_many_pending). The look and the insert
// are one step under q.mu, and nothing else is: the audit row and the notices
// come after, outside it.
//
// ⚠ q.mu is one process's. Two filex instances on one database can each file
// one for the same person, folder and kind at the same moment; the cap still
// bounds what one person can file (docs/E2E-ENCRYPTION.md).
func (q *Requests) fileOnce(ctx context.Context, r *model.E2ERequest) (*model.E2ERequest, bool, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	prev, open, err := q.waiting(ctx, r.UserID, r.StorageID, r.Path, r.Kind)
	if prev != nil || err != nil {
		return prev, false, err
	}
	if open >= MaxPendingPerPerson {
		return nil, false, refuseRequest(http.StatusTooManyRequests, "too_many_pending",
			fmt.Sprintf("you have %d encryption requests waiting already; wait for an administrator to answer one", open))
	}
	row, err := q.o.Store.CreateE2ERequest(ctx, r)
	if err != nil {
		return nil, false, err
	}
	return row, true, nil
}

// waiting answers the request this person already has waiting for this
// folder and kind, and how many they have waiting in all. One past its time
// is not waiting, whether or not a sweep has closed it yet: the person may
// ask again.
func (q *Requests) waiting(ctx context.Context, userID, storageID int64, at, kind string) (*model.E2ERequest, int, error) {
	rows, err := q.o.Store.ListE2ERequests(ctx, model.E2ERequestFilter{UserID: &userID, Status: model.E2ERequestPending})
	if err != nil {
		return nil, 0, err
	}
	now, open := q.now(), 0
	for _, r := range rows {
		if !r.ExpiresAt.After(now) {
			continue
		}
		if r.StorageID == storageID && r.Path == at && r.Kind == kind {
			return r, 0, nil
		}
		open++
	}
	return nil, open, nil
}

// List answers the requests in one state, newest first: "" is pending, "all"
// every state. tenant keeps that tenant's only (nil: every tenant's).
func (q *Requests) List(ctx context.Context, tenant *int64, status string) ([]*model.E2ERequest, error) {
	q.ExpireDue(ctx)
	switch status {
	case "":
		status = model.E2ERequestPending
	case "all":
		status = ""
	case model.E2ERequestPending, model.E2ERequestApproved, model.E2ERequestRejected,
		model.E2ERequestExpired, model.E2ERequestUsed:
	default:
		return nil, refuseRequest(http.StatusBadRequest, "bad_request",
			"status must be pending, approved, rejected, expired, used or all")
	}
	return q.o.Store.ListE2ERequests(ctx, model.E2ERequestFilter{ProviderID: tenant, Status: status})
}

// Mine answers one person's own requests, every state, newest first.
func (q *Requests) Mine(ctx context.Context, userID int64) ([]*model.E2ERequest, error) {
	q.ExpireDue(ctx)
	return q.o.Store.ListE2ERequests(ctx, model.E2ERequestFilter{UserID: &userID, Limit: 200})
}

// Decision is an administrator's answer to one request.
type Decision struct {
	ID      int64
	Approve bool
	// Tenant is the deciding administrator's tenant; nil when nobody confines
	// them. A request of another tenant reads as missing.
	Tenant *int64
	// Platform is the supertenant's id when the decider is the platform
	// operator on a multi-tenant install (MayDecide): they see every tenant's
	// request, and answer only the platform's own. nil: no such limit.
	Platform *int64
	Note     string
	Who      Actor
}

// Decide approves or rejects a waiting request.
//
// A request past its time is not waiting, whether or not a sweep has closed
// it yet: it expires here and is answered like one decided already. An
// approval would give it seven fresh days.
func (q *Requests) Decide(ctx context.Context, d Decision) (*model.E2ERequest, error) {
	q.ExpireDue(ctx)
	r, err := q.o.Store.GetE2ERequest(ctx, d.ID)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && d.Tenant != nil && (r.ProviderID == nil || *r.ProviderID != *d.Tenant)) {
		return nil, refuseRequest(http.StatusNotFound, "not_found", "no such encryption request")
	}
	if err != nil {
		return nil, err
	}
	if !MayDecide(r, d.Platform) {
		return nil, refuseRequest(http.StatusForbidden, "not_decidable",
			"this request belongs to a tenant; that tenant's administrators decide it")
	}
	if r.Status != model.E2ERequestPending {
		return r, refuseRequest(http.StatusConflict, "not_pending", "this request is already "+r.Status)
	}
	now := q.now()
	if !r.ExpiresAt.After(now) {
		ok, err := q.expire(ctx, r, model.E2ERequestPending)
		if err != nil {
			return nil, err
		}
		if !ok {
			// Closed meanwhile, by a sweep or another administrator.
			if fresh, gerr := q.o.Store.GetE2ERequest(ctx, d.ID); gerr == nil {
				r = fresh
			}
		}
		return r, refuseRequest(http.StatusConflict, "not_pending", "this request is already "+r.Status)
	}
	r.DecidedBy, r.Decider, r.DecidedAt = d.Who.UserID, d.Who.Name, &now
	r.DecisionNote = cut(d.Note, maxRequestChars)
	action := AuditActionRequestReject
	r.Status = model.E2ERequestRejected
	if d.Approve {
		action = AuditActionRequestApprove
		r.Status = model.E2ERequestApproved
		// The approval's own time, counted from now — not what was left of
		// the wait.
		r.ExpiresAt = now.Add(ApprovalTTL)
	}
	ok, err := q.o.Store.UpdateE2ERequest(ctx, r, model.E2ERequestPending)
	if err != nil {
		return nil, err
	}
	if !ok {
		// Two administrators, one request: the other one decided first.
		if fresh, gerr := q.o.Store.GetE2ERequest(ctx, d.ID); gerr == nil {
			r = fresh
		}
		return r, refuseRequest(http.StatusConflict, "not_pending", "this request was decided meanwhile")
	}
	q.audit(ctx, action, r, d.Who, map[string]any{"note": r.DecisionNote})
	q.announce(ctx, r)
	return r, nil
}

// MayDecide reports whether an administrator may answer r. platform is the
// supertenant's id for the platform operator of a multi-tenant install, who
// answers only the platform's own requests (the supertenant's, or one filed
// under no tenant); nil for everybody else, whose reach is the list they are
// shown.
func MayDecide(r *model.E2ERequest, platform *int64) bool {
	if platform == nil || r == nil {
		return true
	}
	return r.ProviderID == nil || *r.ProviderID == *platform
}

// ExpireDue closes what is past its time — a request nobody decided, an
// approval nobody spent — and answers how many. It runs before every read and
// write here, and hourly (StartSweeper).
//
// The store hands it only what is due, the longest overdue first
// (E2ERequestFilter.ExpiresBefore), a batch at a time until a batch comes
// back short: one call closes every due row, however many newer rows there
// are. What it cannot read or write is logged and left to the next call. That
// delays the label, and opens nothing: Decide refuses a pending request past
// its time by itself, and FindApprovedE2ERequest an approval past its expiry,
// whatever their rows still say.
func (q *Requests) ExpireDue(ctx context.Context) int {
	now, n := q.now(), 0
	for _, status := range []string{model.E2ERequestPending, model.E2ERequestApproved} {
		for {
			rows, err := q.o.Store.ListE2ERequests(ctx, model.E2ERequestFilter{
				Status: status, ExpiresBefore: now, Limit: expireBatch,
			})
			if err != nil {
				q.o.Log.Warn("e2epolicy: could not read the requests to expire", slog.String("status", status), slog.Any("err", err))
				break
			}
			closed := 0
			for _, r := range rows {
				if r.ExpiresAt.After(now) {
					continue
				}
				ok, err := q.expire(ctx, r, status)
				if err != nil {
					q.o.Log.Warn("e2epolicy: could not expire a request", slog.Int64("request", r.ID), slog.Any("err", err))
					continue
				}
				if ok {
					closed++
				}
			}
			n += closed
			// A short batch was the last. A full one that closed nothing
			// holds rows whose writes fail, or rows another sweep closed
			// first: stop, rather than read the same rows again.
			if len(rows) < expireBatch || closed == 0 {
				break
			}
		}
	}
	return n
}

// expire closes one request past its time from the state it was read in
// (was): only while it is still in that state, so one decided or spent
// meanwhile keeps its own. It reports whether this call closed it; the
// e2e_request.expire row is written when it did.
func (q *Requests) expire(ctx context.Context, r *model.E2ERequest, was string) (bool, error) {
	r.Status = model.E2ERequestExpired
	if was == model.E2ERequestPending {
		at := r.ExpiresAt
		r.DecidedAt = &at
	}
	ok, err := q.o.Store.UpdateE2ERequest(ctx, r, was)
	if ok && err == nil {
		q.audit(ctx, AuditActionRequestExpire, r, Actor{}, map[string]any{"was": was})
	}
	return ok, err
}

// StartSweeper closes what is past its time now and every hour after, until
// ctx ends.
func (q *Requests) StartSweeper(ctx context.Context) {
	go func() {
		for {
			q.ExpireDue(ctx)
			select {
			case <-ctx.Done():
				return
			case <-time.After(sweepEvery):
			}
		}
	}()
}

// storageName is a request's storage as its rows and notices name it ("" when
// it no longer resolves).
func storageName(ctx context.Context, store db.Store, id int64) string {
	if st, err := store.GetStorage(ctx, id); err == nil && st != nil {
		return st.Name
	}
	return ""
}

// auditRequest writes one request row: what was asked, where, by whom, and
// the state it reached. target_name is the folder's wire path.
func auditRequest(ctx context.Context, store db.Store, action string, r *model.E2ERequest, who Actor, extra map[string]any) {
	meta := map[string]any{
		"storage_id": r.StorageID, "path": r.Path, "kind": r.Kind, "status": r.Status,
		"requester": r.Requester, "requester_id": r.UserID,
	}
	if r.ProviderID != nil {
		meta["tenant_id"] = *r.ProviderID
	}
	for k, v := range extra {
		meta[k] = v
	}
	Audit(ctx, store, AuditRow{
		Action: action, TargetType: AuditTargetRequest, TargetID: strconv.FormatInt(r.ID, 10),
		TargetName: storageName(ctx, store, r.StorageID) + "://" + r.Path, Who: who, Meta: meta,
	})
}

func (q *Requests) audit(ctx context.Context, action string, r *model.E2ERequest, who Actor, extra map[string]any) {
	auditRequest(ctx, q.o.Store, action, r, who, extra)
}

// UseRecorder is Options.OnUse for a Service: the e2e_request.use row of an
// approval CheckCreate has just spent — the one event of a request no route
// sees, since it happens at whichever door creates the name. target_name is
// the approval's folder, meta `encrypted` the folder it was spent on (dir):
// for a folder approval of P, P itself or one folder directly inside it. For
// a create over a protocol this row is the one link between an encryption and
// its approval.
func UseRecorder(store db.Store) func(ctx context.Context, r *model.E2ERequest, u *model.User, dir string) {
	return func(ctx context.Context, r *model.E2ERequest, u *model.User, dir string) {
		who := Actor{}
		if u != nil {
			id := u.ID
			who.UserID, who.Name = &id, u.Label()
		}
		auditRequest(ctx, store, AuditActionRequestUse, r, who, map[string]any{
			"encrypted": storageName(ctx, store, r.StorageID) + "://" + dir,
		})
	}
}

// announce tells whoever a request's state concerns. A waiting request: the
// administrators who decide it. A tenant's is ONE broadcast placed on the
// folder, so the bell hands it to the administrators of the tenant that
// storage belongs to (notify/bell.go fileBroadcastEvents) and a webhook gets
// one delivery; the platform operator's bell leaves it out (notify
// PlatformAdminBell: the operator sees it on Admin -> Encryption, and does not
// decide it). The platform's own request on a multi-tenant install is
// addressed to each administrator of the supertenant (platformDeciders), the
// webhook riding on the first row only. A decision: the requester alone.
func (q *Requests) announce(ctx context.Context, r *model.E2ERequest) {
	if q.o.Notify == nil {
		return
	}
	st := storageName(ctx, q.o.Store, r.StorageID)
	name := path.Base("/" + r.Path)
	if r.Path == "" {
		name = st
	}
	// The node is the folder in both kinds: a file's request is its folder's.
	// ⚠ `target_kind`, never `kind`: meta.kind == "file" is what picks the
	// escrow and password events' file wording in a reader
	// (notificationText), and these two must not be tied to it.
	ev := notify.Event{
		Severity: notify.SeverityInfo,
		Node:     &notify.NodeRef{StorageID: r.StorageID, Path: r.Path, Name: name},
		Target:   notify.DirTarget(r.Path),
		Meta:     map[string]any{"request_id": r.ID, "target_kind": r.Kind, "storage": st},
	}
	// The server's own words — what a webhook receiver and a reader with no
	// phrase for the event see — are the explorer's English phrases
	// (notificationText): the folder by name, the requester and their reason.
	if r.Status == model.E2ERequestPending {
		ev.Event = notify.EventE2ERequestCreated
		ev.Title = "Encryption request: " + name
		ev.Body = r.Requester + ": " + r.Reason
		ev.Meta["requester"], ev.Meta["reason"] = r.Requester, r.Reason
	} else {
		ev.Event = notify.EventE2ERequestDecided
		ev.Title = fmt.Sprintf("Encryption request %s: %s", r.Status, name)
		ev.Body = r.DecisionNote
		ev.Meta["decision"], ev.Meta["note"], ev.Meta["decider"] = r.Status, r.DecisionNote, r.Decider
		uid := r.UserID
		ev.UserID = &uid
	}
	if r.Status == model.E2ERequestPending {
		if deciders := q.platformDeciders(ctx, r); len(deciders) > 0 {
			for i, a := range deciders {
				one, uid := ev, a.ID
				one.UserID, one.NoWebhook = &uid, i > 0
				q.send(ctx, r, one)
			}
			return
		}
	}
	q.send(ctx, r, ev)
}

func (q *Requests) send(ctx context.Context, r *model.E2ERequest, ev notify.Event) {
	if _, err := q.o.Notify.Send(context.WithoutCancel(ctx), ev); err != nil {
		q.o.Log.Warn("e2epolicy: request notice not sent", slog.Int64("request", r.ID), slog.Any("err", err))
	}
}

// platformDeciders is who a NEW request of the platform's own is addressed to
// on a multi-tenant install: the supertenant's enabled administrators, when
// the request was filed under the supertenant or under no tenant. nil
// otherwise: a single-tenant install's request and a tenant's stay one
// broadcast. A lookup that fails is logged and falls back to the broadcast.
func (q *Requests) platformDeciders(ctx context.Context, r *model.E2ERequest) []*model.User {
	if q.o.Policy == nil || !q.o.Policy.MultiTenant() {
		return nil
	}
	sup, err := q.o.Store.GetSupertenant(ctx)
	if err != nil || sup == nil {
		if err != nil {
			q.o.Log.Warn("e2epolicy: the supertenant could not be read", slog.Any("err", err))
		}
		return nil
	}
	if r.ProviderID != nil && *r.ProviderID != sup.ID {
		return nil
	}
	// An account of no tenant reaches nothing on a multi-tenant install
	// (auth.TenantResolver: tenant.DenyAll), so the supertenant's own are all.
	users, err := q.o.Store.ListUsersByProvider(ctx, sup.ID)
	if err != nil {
		q.o.Log.Warn("e2epolicy: the platform's administrators could not be read", slog.Any("err", err))
		return nil
	}
	var out []*model.User
	for _, u := range users {
		if u != nil && u.IsAdmin() && u.Enabled {
			out = append(out, u)
		}
	}
	return out
}
