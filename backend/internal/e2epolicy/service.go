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
	"github.com/brf-tech/filex/backend/internal/storage"
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
	// Drivers reaches a storage's driver, for what the catalogue may not
	// know yet: whether a folder is new (nothing in it) and whether a path a
	// request names is there. Nil: the catalogue alone answers.
	Drivers func(storageID int64) (storage.Driver, error)
	// OnUse, when set, hears of every approval CheckCreate used up, after the
	// row changed — the requests service writes its e2e_request.use audit row
	// from it (UseRecorder). Never called for a lost race. dir is the folder
	// the approval was spent on: for an in-place approval of P, P itself; for
	// a new-folder approval of P, the new folder directly inside P; for a
	// file approval, its folder P.
	OnUse func(ctx context.Context, r *model.E2ERequest, u *model.User, dir string)
}

// Service answers the rule against the store: the tenant's ceiling and
// policy, the person's permissions, and the approvals.
type Service struct {
	o Options
}

// MultiTenant reports whether the Service reads the ceiling and the policy
// per tenant (Options.MultiTenant).
func (s *Service) MultiTenant() bool { return s.o.MultiTenant }

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
// being the folder to encrypt in place, or the file to encrypt (AnswerForKind
// with the kind the catalogue says). It changes nothing: an approval is looked
// for, never used. When the answer cannot be found out - the tenant, the
// permissions or a lookup failed - it is denied (and logged): the menu hides
// what the server might refuse, and the server decides anyway.
func (s *Service) AnswerFor(ctx context.Context, u *model.User, st *model.Storage, rel string) (Answer, Reason) {
	return s.AnswerForKind(ctx, u, st, rel, "")
}

// AnswerForKind is AnswerFor for one kind of encryption: E2ERequestFolder
// (rel is the folder to encrypt where it is), E2ERequestNewFolder (rel is the
// folder to make a new encrypted folder in), E2ERequestFile (rel is the file),
// or "" for the one the catalogue says. The approvals it looks for are the
// ones CheckCreate would spend for that encryption (approvalPlaces), so the
// explorer and the doors cannot disagree. A kind that is not what is at rel
// (a file asked about as a folder) is denied.
func (s *Service) AnswerForKind(ctx context.Context, u *model.User, st *model.Storage, rel, kind string) (Answer, Reason) {
	f, ok := s.factsOrDeny(ctx, u, st)
	if !ok {
		return AnswerDenied, ReasonPermission
	}
	ans, why, err := s.answer(ctx, f, u, st, acl.CleanRel(rel), kind, false)
	if err != nil && !errors.Is(err, errKindMismatch) {
		warnDenied(u, st, err)
	}
	return ans, why
}

// errKindMismatch is a request whose kind is not what the catalogue has at
// its path: a folder asked for as a file, or a file as a folder.
var errKindMismatch = errors.New("e2epolicy: the kind asked for is not what is there")

// errMissing is a request about a path where nothing is: a folder request
// needs the folder, a file request the file (operator decision 2026-10-03).
var errMissing = errors.New("e2epolicy: nothing is there to encrypt")

// answerForRequest is AnswerFor for a request to encrypt rel as kind: the
// approval looked for is one of that kind, and what is at rel must be of that
// kind - errKindMismatch otherwise - and must be there at all: errMissing.
// Those two are the only errors it returns, and only once the rule wants a
// request, so a person who may not encrypt at rel learns nothing of what is
// there. A lookup that fails is logged and denied, as AnswerFor's is.
func (s *Service) answerForRequest(ctx context.Context, u *model.User, st *model.Storage, rel, kind string) (Answer, Reason, error) {
	f, ok := s.factsOrDeny(ctx, u, st)
	if !ok {
		return AnswerDenied, ReasonPermission, nil
	}
	ans, why, err := s.answer(ctx, f, u, st, acl.CleanRel(rel), kind, true)
	if err != nil && !errors.Is(err, errKindMismatch) && !errors.Is(err, errMissing) {
		warnDenied(u, st, err)
		err = nil
	}
	return ans, why, err
}

// Ask is one question of AnswersForKinds: may the person encrypt Rel as Kind
// ("" for the kind the catalogue says).
type Ask struct {
	Rel  string
	Kind string
}

// AnswersFor is AnswerFor for many paths on one storage, in their order.
func (s *Service) AnswersFor(ctx context.Context, u *model.User, st *model.Storage, rels []string) []Answer {
	asks := make([]Ask, len(rels))
	for i, rel := range rels {
		asks[i] = Ask{Rel: rel}
	}
	return s.AnswersForKinds(ctx, u, st, asks)
}

// AnswersForKinds is VerdictsFor's answers alone.
func (s *Service) AnswersForKinds(ctx context.Context, u *model.User, st *model.Storage, asks []Ask) []Answer {
	vs := s.VerdictsFor(ctx, u, st, asks)
	out := make([]Answer, len(vs))
	for i, v := range vs {
		out[i] = v.Answer
	}
	return out
}

// Verdict is the rule's answer to one Ask, with the layer that refused (""
// when it is allowed; ReasonApprovalRequired with AnswerRequest).
type Verdict struct {
	Answer Answer
	Reason Reason
}

// VerdictsFor is AnswerForKind for many questions on one storage, in their
// order, finding the tenant, the policy and the person's permissions once -
// the explorer asks for a whole listing at a time. The first lookup that
// fails ends the asking: it is logged once, and that question and every one
// after it is denied, instead of a failing store being asked again for each
// row.
func (s *Service) VerdictsFor(ctx context.Context, u *model.User, st *model.Storage, asks []Ask) []Verdict {
	out := make([]Verdict, len(asks))
	f, ok := s.factsOrDeny(ctx, u, st)
	for i, a := range asks {
		out[i] = Verdict{Answer: AnswerDenied, Reason: ReasonPermission}
		if !ok {
			continue
		}
		ans, why, err := s.answer(ctx, f, u, st, acl.CleanRel(a.Rel), a.Kind, false)
		if errors.Is(err, errKindMismatch) {
			continue
		}
		if err != nil {
			warnDenied(u, st, err)
			ok = false
			continue
		}
		out[i] = Verdict{Answer: ans, Reason: why}
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

// answer turns AnswerRequest into AnswerAllowed when an approval waits for
// this encryption of rel as kind: one of the places CheckCreate would spend
// it at (approvalPlaces). kind "" is the one the catalogue says - a file's
// for a catalogued file, an in-place folder's otherwise. A kind that is not
// what the catalogue has at rel is errKindMismatch; for a request
// (forRequest) nothing at rel is errMissing. Both are looked at only once the
// rule wants a request, so a person who may not encrypt at rel learns nothing
// of what is there.
//
// Finding nothing (sql.ErrNoRows) is an answer; a lookup that FAILED is not.
// It comes back as the error, with the denial the caller gives, so that
// "Request encryption" is never offered on a guess.
func (s *Service) answer(ctx context.Context, f *facts, u *model.User, st *model.Storage, rel, kind string, forRequest bool) (Answer, Reason, error) {
	ans, why := f.decide(rel)
	if ans != AnswerRequest {
		return ans, why, nil
	}
	there, err := s.kindAt(ctx, st, rel)
	if err != nil {
		return AnswerDenied, ReasonPermission, err
	}
	switch kind {
	case "":
		kind = model.E2ERequestFolder
		if there == model.E2ERequestFile {
			kind = model.E2ERequestFile
		}
	case model.E2ERequestFolder, model.E2ERequestNewFolder:
		if there == model.E2ERequestFile {
			return AnswerDenied, ReasonPermission, errKindMismatch
		}
	case model.E2ERequestFile:
		if there == model.E2ERequestFolder {
			return AnswerDenied, ReasonPermission, errKindMismatch
		}
	default:
		return AnswerDenied, ReasonPermission, errKindMismatch
	}
	places, err := s.approvalPlaces(ctx, st, rel, kind)
	if err != nil {
		return AnswerDenied, ReasonPermission, err
	}
	for _, p := range places {
		_, err = s.o.Store.FindApprovedE2ERequest(ctx, u.ID, st.ID, p.path, p.kind, s.o.Now())
		switch {
		case err == nil:
			return AnswerAllowed, "", nil
		case errors.Is(err, sql.ErrNoRows):
		default:
			return AnswerDenied, ReasonPermission, fmt.Errorf("e2epolicy: approval lookup: %w", err)
		}
	}
	// An approval waiting answers "allowed" (no second request) whatever is
	// there; only a request for something new needs it to be there.
	if forRequest && there == "" {
		return AnswerDenied, ReasonPermission, errMissing
	}
	return ans, why, nil
}

// kindAt is the kind of encryption what is at rel takes: a file's for a file,
// a folder's for a folder (the storage root is one), "" for nothing there.
// The catalogue is asked first; where it has no row and a driver is wired
// (Options.Drivers), the storage itself is: a folder nobody has listed yet is
// still there.
func (s *Service) kindAt(ctx context.Context, st *model.Storage, rel string) (string, error) {
	if rel == "" {
		return model.E2ERequestFolder, nil
	}
	n, err := s.o.Store.GetNodeByPath(ctx, st.ID, pathkey.Hash(st.ID, rel))
	switch {
	case errors.Is(err, sql.ErrNoRows) || (err == nil && n == nil):
	case err != nil:
		return "", fmt.Errorf("e2epolicy: node lookup: %w", err)
	case n.Type == model.NodeTypeFile:
		return model.E2ERequestFile, nil
	default:
		return model.E2ERequestFolder, nil
	}
	drv := s.reachableDriver(st)
	if drv == nil {
		return "", nil
	}
	obj, err := drv.Stat(ctx, rel)
	switch {
	case errors.Is(err, storage.ErrNotFound):
		return "", nil
	case err != nil:
		return "", fmt.Errorf("e2epolicy: stat: %w", err)
	case obj.Kind == storage.KindDirectory:
		return model.E2ERequestFolder, nil
	default:
		return model.E2ERequestFile, nil
	}
}

// reachableDriver is st's driver when the Service was given a way to reach it
// (Options.Drivers) and it answers; nil otherwise. A storage that cannot be
// reached leaves the catalogue's answer alone: it is no folder that is new and
// no path that is there, so it opens nothing (folderIsNew, kindAt).
func (s *Service) reachableDriver(st *model.Storage) storage.Driver {
	if s.o.Drivers == nil {
		return nil
	}
	drv, err := s.o.Drivers(st.ID)
	if err != nil {
		slog.Debug("e2epolicy: the storage's own answer is not available; the catalogue's stands",
			slog.Int64("storage_id", st.ID), slog.String("err", err.Error()))
		return nil
	}
	return drv
}

// place is one approval that may open an encryption: its kind, and the folder
// it is kept under (ApprovalPath).
type place struct {
	kind string
	path string
}

// approvalPlaces is every approval that may open the encryption of `at` as
// kind, in the order one is spent. It is the ONE answer to "which approval
// counts here": CheckCreate spends at these places and AnswerForKind looks
// for them, so the explorer offers exactly what the doors accept (operator
// decision 2026-10-03).
//
//   - E2ERequestFile, `at` a file: the file approval of its folder.
//   - E2ERequestNewFolder, `at` a folder P: the new-folder approval of P.
//   - E2ERequestFolder, `at` a folder D: D's own in-place approval, and, when
//     D is NEW (folderIsNew: nothing in it, or not there yet), the new-folder
//     approval of D's parent. A new-folder approval is never spent on a
//     folder that holds something, and no approval reaches past the folder it
//     was given for: not the folders below it, not a second folder.
func (s *Service) approvalPlaces(ctx context.Context, st *model.Storage, at, kind string) ([]place, error) {
	switch kind {
	case model.E2ERequestFile:
		return []place{{kind: kind, path: parentOf(at)}}, nil
	case model.E2ERequestNewFolder:
		return []place{{kind: kind, path: at}}, nil
	}
	out := []place{{kind: model.E2ERequestFolder, path: at}}
	if at == "" {
		return out, nil
	}
	fresh, err := s.folderIsNew(ctx, st, at)
	if err != nil {
		return nil, err
	}
	if fresh {
		out = append(out, place{kind: model.E2ERequestNewFolder, path: parentOf(at)})
	}
	return out, nil
}

// folderIsNew reports whether the folder at dir holds nothing - or is not
// there yet, as a copy's destination is not - so that encrypting it makes a
// new encrypted folder rather than encrypting one in place. The catalogue
// must have nothing below it, and where a driver is wired the storage must
// list nothing in it either: a folder whose files nobody has listed yet is
// not new. A catalogue that cannot be read is an error; a storage that cannot
// be reached or listed answers "not new", never "new".
func (s *Service) folderIsNew(ctx context.Context, st *model.Storage, dir string) (bool, error) {
	holds, err := s.o.Store.HasLiveNodesUnder(ctx, st.ID, dir)
	if err != nil {
		return false, fmt.Errorf("e2epolicy: what the folder holds: %w", err)
	}
	if holds {
		return false, nil
	}
	if s.o.Drivers == nil {
		return true, nil
	}
	drv := s.reachableDriver(st)
	if drv == nil {
		return false, nil
	}
	objs, err := drv.List(ctx, dir)
	switch {
	case errors.Is(err, storage.ErrNotFound):
		return true, nil
	case err != nil:
		slog.Debug("e2epolicy: a folder the storage could not list is not new",
			slog.Int64("storage_id", st.ID), slog.String("err", err.Error()))
		return false, nil
	}
	return len(objs) == 0, nil
}

// CheckCreate is the rule at a door. It answers nil when u may create rel on
// st, a *RefusedError when the rule refuses, and another error when it could
// not be decided — the door fails the write either way.
//
// Call it where the door knows the write CREATES rel (nothing is catalogued
// there), and only there: rewriting a marker that exists is free (a new
// password, a recovery key), and so are moves of what is already encrypted
// (RelocationEncrypts). A copy is asked with CheckCopy. A name that encrypts
// nothing returns nil before anything is looked up. A caller with no person
// behind the write passes the account it acts for; nil is refused.
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
		places, err := s.approvalPlaces(ctx, st, at, kind)
		if err != nil {
			return err
		}
		return s.useApproval(ctx, u, st, places, dir)
	default:
		return &RefusedError{Reason: why}
	}
}

// useApproval spends one approval of places (approvalPlaces), the first that
// is waiting. OnUse hears of dir, the folder it was spent on: the folder
// encrypted, or the one a new `.fxe` went into.
func (s *Service) useApproval(ctx context.Context, u *model.User, st *model.Storage, places []place, dir string) error {
	for _, at := range places {
		r, err := s.o.Store.FindApprovedE2ERequest(ctx, u.ID, st.ID, at.path, at.kind, s.o.Now())
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

// CheckCopy is the rule at a door that COPIES the item at src (on
// srcStorageID; srcIsDir: it is a folder) to dst on st (operator decision
// 2026-10-03). A copy of an encrypted folder or of a `.fxe` makes a new
// encrypted item where it lands, exactly as creating one would, so it is
// asked as that create; a move and a rename are not (RelocationEncrypts).
//
//   - A file is asked by the name it lands under (CheckCreate at dst): a
//     `.fxe` copied as a `.fxe`, a key file copied into another folder, a plain
//     file copied onto either name. A `.fxe` copied under a plain name is no
//     encryption by its name - the rule reads names (docs: the honest limits).
//   - A folder that holds a key file or a `.fxe` anywhere below it (the
//     catalogue says, as the transfer guard reads it) is a NEW encrypted folder
//     at dst: asked once, at dst, and under the approval policy it spends a
//     new-folder approval of dst's folder. What it holds is looked up only when
//     the rule would not let it through anyway, so an install whose policy
//     lets this person encrypt pays nothing for it.
//
// Under the approval policy it spends, as CheckCreate does: ask it once per
// copied item.
func (s *Service) CheckCopy(ctx context.Context, u *model.User, st *model.Storage, dst string, srcStorageID int64, src string, srcIsDir bool) error {
	if !srcIsDir {
		return s.CheckCreate(ctx, u, st, dst)
	}
	at := acl.CleanRel(dst)
	var (
		f   *facts
		err error
	)
	if u != nil && st != nil {
		if f, err = s.factsFor(ctx, u, st); err != nil {
			return err
		}
		if ans, _ := f.decide(at); ans == AnswerAllowed {
			return nil
		}
	}
	carries, err := s.carriesEncryption(ctx, srcStorageID, src)
	if err != nil || !carries {
		return err
	}
	if f == nil {
		return &RefusedError{Reason: ReasonPermission}
	}
	switch ans, why := f.decide(at); {
	case ans == AnswerRequest && at != "":
		places := []place{{kind: model.E2ERequestNewFolder, path: parentOf(at)}}
		return s.useApproval(ctx, u, st, places, at)
	case ans == AnswerRequest:
		return &RefusedError{Reason: ReasonApprovalRequired}
	default:
		return &RefusedError{Reason: why}
	}
}

// SourceIsFolder reports whether the item a copy reads at src (on
// srcStorageID) is a folder: srcDrv answers when the door has it, else the
// storage's driver (Options.Drivers), else the catalogue. Nothing there is not
// a folder (the copy fails on its own); a look that fails is an error, so a
// folder is never taken for a file on a guess.
func (s *Service) SourceIsFolder(ctx context.Context, srcStorageID int64, srcDrv storage.Driver, src string) (bool, error) {
	rel := acl.CleanRel(src)
	if rel == "" {
		return true, nil
	}
	if srcDrv == nil && s.o.Drivers != nil {
		d, err := s.o.Drivers(srcStorageID)
		if err != nil {
			return false, fmt.Errorf("e2epolicy: storage %d: %w", srcStorageID, err)
		}
		srcDrv = d
	}
	if srcDrv != nil {
		obj, err := srcDrv.Stat(ctx, rel)
		switch {
		case errors.Is(err, storage.ErrNotFound):
			return false, nil
		case err != nil:
			return false, fmt.Errorf("e2epolicy: stat the copied item: %w", err)
		}
		return obj.Kind == storage.KindDirectory, nil
	}
	n, err := s.o.Store.GetNodeByPath(ctx, srcStorageID, pathkey.Hash(srcStorageID, rel))
	switch {
	case errors.Is(err, sql.ErrNoRows) || (err == nil && n == nil):
		return false, nil
	case err != nil:
		return false, fmt.Errorf("e2epolicy: node lookup: %w", err)
	}
	return n.Type != model.NodeTypeFile, nil
}

// carriesEncryption reports whether the folder at src holds a key file or a
// `.fxe` anywhere below it, as the catalogue has it.
func (s *Service) carriesEncryption(ctx context.Context, storageID int64, src string) (bool, error) {
	rows, err := s.o.Store.ListNodesUnder(ctx, storageID, acl.CleanRel(src), false)
	if err != nil {
		return false, fmt.Errorf("e2epolicy: what the copied folder holds: %w", err)
	}
	for _, n := range rows {
		if n.Type == model.NodeTypeFile && IsEncryptionName(n.Path) {
			return true, nil
		}
	}
	return false, nil
}
