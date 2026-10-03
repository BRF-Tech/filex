package e2epolicy

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/brf-tech/filex/backend/internal/acl"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/pathkey"
	"github.com/brf-tech/filex/backend/internal/perm"
)

// Options wires a Service.
type Options struct {
	Store db.Store
	// ACL answers files.encrypt on a path. Nil: acl.New(Store).
	ACL *acl.Resolver
	// MultiTenant reads the ceiling and the policy per tenant (providers).
	// Off, the install's e2e.policy setting decides for everybody.
	MultiTenant bool
	// Now is the clock approvals expire by. Nil: time.Now.
	Now func() time.Time
	// OnUse, when set, hears of every approval CheckCreate used up, after the
	// row changed — the requests service writes its e2e_request.use audit row
	// from it (UseRecorder). Never called for a lost race. dir is the folder
	// the approval was spent on: for a folder approval of P, P itself or one
	// folder directly inside P; for a file approval, its folder P.
	OnUse func(ctx context.Context, r *model.E2ERequest, u *model.User, dir string)
}

// Service answers the rule against the store: the tenant's ceiling and
// policy, the person's permissions, and the approvals.
type Service struct {
	o Options
}

// New returns a Service.
func New(o Options) *Service {
	if o.ACL == nil && o.Store != nil {
		o.ACL = acl.New(o.Store)
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	return &Service{o: o}
}

// PolicyFor is the ceiling and the policy that bind one tenant. A
// single-tenant install — and a caller with no tenant — has no ceiling and
// the install's setting for a policy. A policy nobody knows (a hand-written
// setting) reads as the default, permitted, which is what Decide does with
// it too.
func (s *Service) PolicyFor(ctx context.Context, providerID *int64) (model.ProviderE2E, error) {
	if s.o.MultiTenant && providerID != nil {
		e, err := s.o.Store.GetProviderE2E(ctx, *providerID)
		if err != nil {
			return model.ProviderE2E{}, fmt.Errorf("e2epolicy: tenant %d: %w", *providerID, err)
		}
		e.Policy = knownPolicy(e.Policy)
		return e, nil
	}
	raw, err := s.o.Store.GetSetting(ctx, model.SettingE2EPolicy)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return model.ProviderE2E{}, fmt.Errorf("e2epolicy: %s: %w", model.SettingE2EPolicy, err)
	}
	return model.ProviderE2E{Allowed: true, Policy: knownPolicy(raw)}, nil
}

func knownPolicy(p string) string {
	if p = strings.TrimSpace(p); model.ValidE2EPolicy(p) {
		return p
	}
	return model.E2EPolicyPermitted
}

// TenantFor is the tenant whose ceiling and policy bind u encrypting on st;
// nil on a single-tenant install, where the install's setting decides.
//
// A tenant's own member is judged by their tenant: confinement keeps them on
// its storages anyway. A platform operator — a member of the supertenant —
// is judged by the tenant whose data it is: the storage's tenant (its
// lowest-id link, as tags' teamTenantFor picks it), else their own. A
// storage no tenant is linked to is the platform's.
func (s *Service) TenantFor(ctx context.Context, u *model.User, st *model.Storage) (*int64, error) {
	if !s.o.MultiTenant {
		return nil, nil
	}
	var own *int64
	if u != nil && u.ProviderID != nil {
		p, err := s.o.Store.GetProvider(ctx, *u.ProviderID)
		if err != nil {
			return nil, fmt.Errorf("e2epolicy: tenant of user %d: %w", u.ID, err)
		}
		if !p.IsSupertenant {
			return &p.ID, nil
		}
		own = &p.ID
	}
	if st != nil {
		id, ok, err := s.o.Store.GetProviderIDForStorage(ctx, st.ID)
		if err != nil {
			return nil, fmt.Errorf("e2epolicy: tenant of storage %d: %w", st.ID, err)
		}
		if ok {
			return &id, nil
		}
	}
	return own, nil
}

// facts is what the rule knows about one person on one storage.
type facts struct {
	policy model.ProviderE2E
	admin  bool
	set    *acl.Set
}

func (s *Service) factsFor(ctx context.Context, u *model.User, st *model.Storage) (*facts, error) {
	tenant, err := s.TenantFor(ctx, u, st)
	if err != nil {
		return nil, err
	}
	pol, err := s.PolicyFor(ctx, tenant)
	if err != nil {
		return nil, err
	}
	set, err := s.o.ACL.LoadSet(ctx, u, st)
	if err != nil {
		return nil, fmt.Errorf("e2epolicy: permissions: %w", err)
	}
	return &facts{policy: pol, admin: u.IsAdmin(), set: set}, nil
}

// decide is Decide at rel before any approval is looked for: AnswerRequest
// means "only an approval is missing".
func (f *facts) decide(rel string) (Answer, Reason) {
	return Decide(Input{
		TenantAllowed: f.policy.Allowed,
		Policy:        f.policy.Policy,
		IsAdmin:       f.admin,
		HasPermission: f.set.Can(rel, perm.FilesEncrypt),
	})
}

// AnswerFor is the explorer's question: may u encrypt at rel on st, rel
// being the folder to make an encrypted folder in, the folder to encrypt, or
// the file to encrypt. It changes nothing: an approval is looked for, never
// used. When the answer cannot be found out — the tenant, the permissions or
// a lookup failed — it is denied (and logged): the menu hides what the server
// might refuse, and the server decides anyway.
func (s *Service) AnswerFor(ctx context.Context, u *model.User, st *model.Storage, rel string) (Answer, Reason) {
	f, ok := s.factsOrDeny(ctx, u, st)
	if !ok {
		return AnswerDenied, ReasonPermission
	}
	ans, why, err := s.answer(ctx, f, u, st, acl.CleanRel(rel), "")
	if err != nil {
		warnDenied(u, st, err)
	}
	return ans, why
}

// errKindMismatch is a request whose kind is not what the catalogue has at
// its path: a folder asked for as a file, or a file as a folder.
var errKindMismatch = errors.New("e2epolicy: the kind asked for is not what is there")

// answerForRequest is AnswerFor for a request to encrypt rel as kind: the
// approval looked for is one of that kind, and what the catalogue has at rel,
// when it has anything, must be of that kind — errKindMismatch otherwise, the
// only error it returns. A lookup that fails is logged and denied, as
// AnswerFor's is.
func (s *Service) answerForRequest(ctx context.Context, u *model.User, st *model.Storage, rel, kind string) (Answer, Reason, error) {
	f, ok := s.factsOrDeny(ctx, u, st)
	if !ok {
		return AnswerDenied, ReasonPermission, nil
	}
	ans, why, err := s.answer(ctx, f, u, st, acl.CleanRel(rel), kind)
	if err != nil && !errors.Is(err, errKindMismatch) {
		warnDenied(u, st, err)
		err = nil
	}
	return ans, why, err
}

// AnswersFor is AnswerFor for many paths on one storage, in their order,
// finding the tenant, the policy and the person's permissions once — the
// explorer asks for a whole listing at a time. The first lookup that fails
// ends the asking: it is logged once, and that path and every one after it is
// denied, instead of a failing store being asked again for each row.
func (s *Service) AnswersFor(ctx context.Context, u *model.User, st *model.Storage, rels []string) []Answer {
	out := make([]Answer, len(rels))
	f, ok := s.factsOrDeny(ctx, u, st)
	for i, rel := range rels {
		out[i] = AnswerDenied
		if !ok {
			continue
		}
		ans, _, err := s.answer(ctx, f, u, st, acl.CleanRel(rel), "")
		if err != nil {
			warnDenied(u, st, err)
			ok = false
			continue
		}
		out[i] = ans
	}
	return out
}

func (s *Service) factsOrDeny(ctx context.Context, u *model.User, st *model.Storage) (*facts, bool) {
	if u == nil || st == nil {
		return nil, false
	}
	f, err := s.factsFor(ctx, u, st)
	if err != nil {
		warnDenied(u, st, err)
		return nil, false
	}
	return f, true
}

// warnDenied logs an answer that could not be found out and was denied.
func warnDenied(u *model.User, st *model.Storage, err error) {
	slog.Warn("e2epolicy: no answer, denying",
		slog.Int64("user_id", u.ID), slog.Int64("storage_id", st.ID), slog.String("err", err.Error()))
}

// answer turns AnswerRequest into AnswerAllowed when an approval of kind is
// waiting for rel, under ApprovalPath: the file's folder's for a file, rel's
// own for a folder. kind "" is the one the catalogue says — a file's for a
// catalogued file, a folder's otherwise. A kind given that is not what the
// catalogue has at rel is errKindMismatch; it is looked at only once the rule
// wants a request, so a person who may not encrypt at rel learns nothing of
// what is there.
//
// Finding nothing (sql.ErrNoRows) is an answer; a lookup that FAILED is not.
// It comes back as the error, with the denial the caller gives, so that
// "Request encryption" is never offered on a guess.
func (s *Service) answer(ctx context.Context, f *facts, u *model.User, st *model.Storage, rel, kind string) (Answer, Reason, error) {
	ans, why := f.decide(rel)
	if ans != AnswerRequest {
		return ans, why, nil
	}
	there, err := s.kindAt(ctx, st, rel)
	if err != nil {
		return AnswerDenied, ReasonPermission, err
	}
	switch {
	case kind == "" && there == "":
		kind = model.E2ERequestFolder
	case kind == "":
		kind = there
	case there != "" && there != kind:
		return AnswerDenied, ReasonPermission, errKindMismatch
	}
	_, err = s.o.Store.FindApprovedE2ERequest(ctx, u.ID, st.ID, ApprovalPath(rel, kind), kind, s.o.Now())
	switch {
	case err == nil:
		return AnswerAllowed, "", nil
	case errors.Is(err, sql.ErrNoRows):
		return ans, why, nil
	default:
		return AnswerDenied, ReasonPermission, fmt.Errorf("e2epolicy: approval lookup: %w", err)
	}
}

// kindAt is the kind of encryption what the catalogue has at rel takes: a
// file's for a file, a folder's for anything else, "" for nothing there.
func (s *Service) kindAt(ctx context.Context, st *model.Storage, rel string) (string, error) {
	n, err := s.o.Store.GetNodeByPath(ctx, st.ID, pathkey.Hash(st.ID, rel))
	switch {
	case errors.Is(err, sql.ErrNoRows) || (err == nil && n == nil):
		return "", nil
	case err != nil:
		return "", fmt.Errorf("e2epolicy: node lookup: %w", err)
	case n.Type == model.NodeTypeFile:
		return model.E2ERequestFile, nil
	default:
		return model.E2ERequestFolder, nil
	}
}

// CheckCreate is the rule at a door. It answers nil when u may create rel on
// st, a *RefusedError when the rule refuses, and another error when it could
// not be decided — the door fails the write either way.
//
// Call it where the door knows the write CREATES rel (nothing is catalogued
// there), and only there: rewriting a marker that exists is free (a new
// password, a recovery key), and so are copies and moves of what is already
// encrypted (RelocationEncrypts). A name that encrypts nothing returns nil
// before anything is looked up. A caller with no person behind the write
// passes the account it acts for; nil is refused.
//
// Under the approval policy the person's approval is used up here, before the
// bytes are written. ⚠ A write that fails afterwards does not give it back —
// the person asks again. Spending it after the write would let two parallel
// uploads use one approval twice.
func (s *Service) CheckCreate(ctx context.Context, u *model.User, st *model.Storage, rel string) error {
	return s.checkCreate(ctx, u, st, rel, true)
}

// CheckCreateWithoutApproval is CheckCreate that never uses an approval:
// where CheckCreate would spend one, it refuses with ReasonApprovalRequired,
// and nothing is recorded. Every other answer is CheckCreate's.
//
// For a write judged as somebody who did not make it: a file request's
// upload is the link creator's file, but the visitor must not use up the
// approval the creator asked for (operator decision 2026-10-03).
func (s *Service) CheckCreateWithoutApproval(ctx context.Context, u *model.User, st *model.Storage, rel string) error {
	return s.checkCreate(ctx, u, st, rel, false)
}

// checkCreate is CheckCreate; spend says whether an approval may be used.
func (s *Service) checkCreate(ctx context.Context, u *model.User, st *model.Storage, rel string, spend bool) error {
	dir, kind := TargetOf(rel)
	if kind == "" {
		return nil
	}
	if u == nil || st == nil {
		return &RefusedError{Reason: ReasonPermission}
	}
	f, err := s.factsFor(ctx, u, st)
	if err != nil {
		return err
	}
	// A marker is judged at the folder it encrypts, a .fxe at its own path.
	at := dir
	if kind == model.E2ERequestFile {
		at = acl.CleanRel(rel)
	}
	switch ans, why := f.decide(at); {
	case ans == AnswerAllowed:
		return nil
	case ans == AnswerRequest && spend:
		return s.useApproval(ctx, u, st, dir, kind)
	default:
		return &RefusedError{Reason: why}
	}
}

// useApproval spends one approval for an encryption of kind at dir. A folder
// may also spend its parent's: a folder approval for P opens P itself, or one
// folder directly inside P — a new one ("make an encrypted folder here" is
// asked from the folder the person is in, before the new folder has a name)
// or one already there. OnUse hears of dir, the folder it was spent on.
func (s *Service) useApproval(ctx context.Context, u *model.User, st *model.Storage, dir, kind string) error {
	places := []string{dir}
	if kind == model.E2ERequestFolder && dir != "" {
		places = append(places, parentOf(dir))
	}
	for _, at := range places {
		r, err := s.o.Store.FindApprovedE2ERequest(ctx, u.ID, st.ID, at, kind, s.o.Now())
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return fmt.Errorf("e2epolicy: approval: %w", err)
		}
		used := s.o.Now().UTC()
		r.Status, r.UsedAt = model.E2ERequestUsed, &used
		ok, err := s.o.Store.UpdateE2ERequest(ctx, r, model.E2ERequestApproved)
		if err != nil {
			return fmt.Errorf("e2epolicy: use approval %d: %w", r.ID, err)
		}
		if !ok {
			// Another write spent it a moment ago.
			continue
		}
		if s.o.OnUse != nil {
			s.o.OnUse(ctx, r, u, dir)
		}
		return nil
	}
	return &RefusedError{Reason: ReasonApprovalRequired}
}
