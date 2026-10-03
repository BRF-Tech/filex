package handlers

import (
	"context"
	"net/http"

	"github.com/brf-tech/filex/backend/internal/acl"
	"github.com/brf-tech/filex/backend/internal/auth"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/perm"
	"github.com/brf-tech/filex/backend/internal/srvtext"
)

// Per-user permissions (package perm) at the HTTP boundary.
//
// Two different refusals come out of here, on purpose:
//
//   - the PATH said no (no grant, a viewer account, an app-plugin lock): the
//     handler's own long-standing 403 body, unchanged, because clients match
//     on it;
//   - the PERMISSION said no: 403 {"error":"permission_denied",
//     "permission":"files.delete","source":{…},"message":"…"} — the source is
//     what lets the file manager say WHY ("the rule “Contractors” does not
//     allow you to delete files") instead of a bare "forbidden".

// permDeniedBody is the JSON of a permission refusal.
type permDeniedBody struct {
	Error      string      `json:"error"`
	Permission perm.Perm   `json:"permission"`
	Source     perm.Source `json:"source"`
	Message    string      `json:"message"`
}

// permDeniedMessage is the refusal sentence in the reader's language — the
// deciding role named in it too, when the role has a name in that language
// (Source.RuleNameFor). The body's source.rule_name stays the role's own.
func permDeniedMessage(lang string, p perm.Perm, src perm.Source) string {
	vars := srvtext.Vars{
		"action": srvtext.Text(lang, "server.perm.action."+string(p), nil),
		"rule":   src.RuleNameFor(lang),
		"group":  src.GroupName,
	}
	kind := string(src.Kind)
	if kind == "" {
		kind = string(perm.SourceBase)
	}
	// A role the account holds through a group names the group too — the
	// person has no role of their own to look for.
	if src.GroupName != "" {
		kind += "_group"
	}
	return srvtext.Text(lang, "server.perm.denied."+kind, vars)
}

// writePermDenied writes the 403 for a caller lacking permission p. res may
// be nil (the source is then left empty).
func writePermDenied(w http.ResponseWriter, r *http.Request, res *perm.Result, p perm.Perm) {
	writePermDeniedSource(w, r, p, res.Why(p))
}

// writePermDeniedSource is writePermDenied with the source already known —
// the path-aware one (acl.Set.WhyAt) when a rule limited to paths decided.
func writePermDeniedSource(w http.ResponseWriter, r *http.Request, p perm.Perm, src perm.Source) {
	writeJSON(w, http.StatusForbidden, permDeniedBody{
		Error:      "permission_denied",
		Permission: p,
		Source:     src,
		Message:    permDeniedMessage(langOf(r), p, src),
	})
}

// requireCan answers whether the caller may take action p on rel with the
// ACL set already loaded, writing the refusal when not: legacyMsg (the
// handler's historical body) when the path is the reason, the permission
// refusal when the permission is.
func requireCan(w http.ResponseWriter, r *http.Request, set *acl.Set, rel string, p perm.Perm, legacyMsg string) bool {
	v := verdictOf(set, rel, p)
	if v.ok {
		return true
	}
	if !v.WritePerm(w, r) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": legacyMsg})
	}
	return false
}

// requirePerm gates an action that is not about a path (commenting, minting
// a token, the admin area). An unwired resolver (tests) allows; a resolution
// error refuses.
func requirePerm(w http.ResponseWriter, r *http.Request, resolver *acl.Resolver, p perm.Perm) bool {
	res, err := resolver.Perms(r.Context(), auth.UserFrom(r.Context()))
	if err != nil {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "forbidden"})
		return false
	}
	if res == nil || res.Can(p) {
		return true
	}
	writePermDenied(w, r, res, p)
	return false
}

// callerCan is requirePerm without writing a response — for a handler that
// narrows what it does rather than refusing.
func callerCan(ctx context.Context, resolver *acl.Resolver, p perm.Perm) bool {
	res, err := resolver.Perms(ctx, auth.UserFrom(ctx))
	if err != nil {
		return false
	}
	return res == nil || res.Can(p)
}

// permVerdict is the outcome of a permission-aware ACL check that has not
// written anything yet, so the call site can keep its own refusal for the
// path case. deniedBy is set only when the path allowed and the per-user
// permission is what refused.
type permVerdict struct {
	ok       bool
	p        perm.Perm
	deniedBy *perm.Result
	// src is where the refusal came from, for this path (acl.Set.WhyAt).
	src perm.Source
	// blockedExt is set when a rule's blocked_extensions refused the name.
	blockedExt string
}

// ByPerm reports whether the permission (not the path) refused.
func (v permVerdict) ByPerm() bool { return !v.ok && (v.deniedBy != nil || v.blockedExt != "") }

// WritePerm writes the permission refusal and reports true when that was the
// reason; otherwise it writes nothing and reports false so the caller writes
// its path refusal.
func (v permVerdict) WritePerm(w http.ResponseWriter, r *http.Request) bool {
	if !v.ByPerm() {
		return false
	}
	if v.blockedExt != "" {
		writeJSON(w, http.StatusForbidden, map[string]string{
			"error":     "blocked_file_type",
			"extension": v.blockedExt,
			"message":   srvtext.Text(langOf(r), "server.perm.blocked_extension", srvtext.Vars{"ext": v.blockedExt}),
		})
		return true
	}
	writePermDeniedSource(w, r, v.p, v.src)
	return true
}

// verdictOf evaluates p on rel against a loaded set.
func verdictOf(set *acl.Set, rel string, p perm.Perm) permVerdict {
	v := permVerdict{p: p}
	if set.Can(rel, p) {
		v.ok = true
		return v
	}
	if set != nil && set.Effective(rel) >= acl.NeedLevel(p) {
		if ext := set.BlockedExtension(rel); ext != "" && (p == perm.FilesCreate || p == perm.FilesModify || p == perm.FilesRename || p == perm.FilesMove) {
			v.blockedExt = ext
			return v
		}
	}
	if set != nil && set.Effective(rel) >= acl.NeedLevel(p) && !set.AllowsAt(rel, p) {
		v.src = set.WhyAt(rel, p)
		v.deniedBy = set.Perms()
		if v.deniedBy == nil {
			v.deniedBy = &perm.Result{}
		}
	}
	return v
}

// aclCanID is aclAllowID for an action with a per-user permission: the level
// acl.NeedLevel(p) on rel AND p. A nil resolver (ACL unwired, tests) allows.
func aclCanID(ctx context.Context, resolver *acl.Resolver, store db.Store, storageID int64, rel string, p perm.Perm) permVerdict {
	if resolver == nil {
		return permVerdict{ok: true, p: p}
	}
	st, err := store.GetStorage(ctx, storageID)
	if err != nil || st == nil {
		return permVerdict{p: p}
	}
	return aclCanStorage(ctx, resolver, st, rel, p)
}

// aclCanName is aclCanID keyed by storage (adapter) name.
func aclCanName(ctx context.Context, resolver *acl.Resolver, store db.Store, storageName, rel string, p perm.Perm) permVerdict {
	if resolver == nil {
		return permVerdict{ok: true, p: p}
	}
	st, err := store.GetStorageByName(ctx, storageName)
	if err != nil || st == nil {
		return permVerdict{p: p}
	}
	return aclCanStorage(ctx, resolver, st, rel, p)
}

func aclCanStorage(ctx context.Context, resolver *acl.Resolver, st *model.Storage, rel string, p perm.Perm) permVerdict {
	set, err := resolver.LoadSet(ctx, auth.UserFrom(ctx), st)
	if err != nil || set == nil {
		return permVerdict{p: p}
	}
	return verdictOf(set, rel, p)
}

// RequirePermission is route middleware for a permission that is not about a
// path: the whole door is the action (minting an S3 key, the agent API,
// posting a comment). It must run after the auth middleware has put the user
// on the context. A nil resolver (tests) allows.
func RequirePermission(resolver *acl.Resolver, p perm.Perm) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if resolver != nil && !requirePerm(w, r, resolver, p) {
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// RequireAdminPermission gates one route of the delegated admin area. A full
// administrator (auth.CallerMayAdminister — the account role, and a token
// that may administer) passes as before. Anybody else passes only with the
// admin permission p, and only from a signed-in session: a self-service token
// can never carry the admin scope (tokens_self.go), so a delegated
// administrator using one would be the only token that administers without
// it.
//
// ⚠ This is the ONLY door into the routes it guards: they are registered
// outside the RequireAdmin group precisely so this can admit a non-admin.
// Every handler behind it must therefore hold its own line against
// escalation (see refuseAdminTarget) — the route cannot know which account a
// request is about.
func RequireAdminPermission(resolver *acl.Resolver, p perm.Perm) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := r.Context()
			if auth.CallerMayAdminister(ctx) {
				next.ServeHTTP(w, r)
				return
			}
			if auth.UserFrom(ctx) == nil {
				writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
				return
			}
			if auth.TokenFrom(ctx) != nil {
				writeJSON(w, http.StatusForbidden, map[string]string{"error": "delegated administration is available to a signed-in session only, not to an API token"})
				return
			}
			if resolver == nil || !requirePerm(w, r, resolver, p) {
				if resolver == nil {
					writeJSON(w, http.StatusForbidden, map[string]string{"error": "forbidden"})
				}
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// callerIsFullAdmin reports whether the request comes from a full
// administrator rather than a delegated one.
func callerIsFullAdmin(ctx context.Context) bool { return auth.CallerMayAdminister(ctx) }

// roleRank orders account roles for the no-escalation rule.
func roleRank(role string) int {
	switch role {
	case model.RoleViewer:
		return 1
	case model.RoleUser:
		return 2
	case model.RoleAdmin:
		return 3
	default:
		return 0
	}
}

// refuseAdminTarget is the delegated administrator's line: they manage other
// accounts, never an administrator's (which would let them reset its password
// and become it), and never hand out a role above their own. target may be
// nil (a create); newRole may be "" (no role change). It writes the 403 and
// reports true when refused. A full administrator is never refused here.
func refuseAdminTarget(w http.ResponseWriter, r *http.Request, target *model.User, newRole string) bool {
	ctx := r.Context()
	if callerIsFullAdmin(ctx) {
		return false
	}
	if target != nil && target.IsAdmin() {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "only an administrator can change an administrator's account"})
		return true
	}
	if newRole != "" {
		caller := auth.UserFrom(ctx)
		if caller == nil || newRole == model.RoleAdmin || roleRank(newRole) > roleRank(caller.Role) {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "you cannot give an account a role above your own"})
			return true
		}
	}
	return false
}

// reachOf is everything res lets its account do somewhere: what it holds
// everywhere, and what its role allows only in some folders.
func reachOf(res *perm.Result) perm.Set {
	if res == nil {
		return 0
	}
	return res.Allowed | res.AllowedInFolders()
}

// delegatedCaller resolves the calling delegated administrator's own
// permissions, writing a 403 and answering nil when they cannot be known —
// a delegated line that cannot be judged is not crossed.
func delegatedCaller(w http.ResponseWriter, r *http.Request, resolver *acl.Resolver) (*model.User, *perm.Result) {
	ctx := r.Context()
	caller := auth.UserFrom(ctx)
	var mine *perm.Result
	var err error
	if caller != nil && resolver != nil {
		mine, err = resolver.Perms(ctx, caller)
	}
	if caller == nil || err != nil || mine == nil {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "forbidden"})
		return nil, nil
	}
	return caller, mine
}

// refuseGain is the delegated administrator's line on the RESULT of a change
// to an account (PR #75 review). refuseAdminTarget and the handlers' own
// checks read the request: an Allow the caller does not hold, a custom role
// whose list they do not hold, a built-in role above their own. None of them
// sees a permission handed out without being named — lifting a Deny, ending
// a restrictive custom role, picking the built-in User role for an account
// whose custom role took something away, a new account on a built-in role
// that holds more than the caller. So the change is resolved (perm.Loader.
// Preview) and compared with what the account holds now: anything it would
// newly hold must be something the caller holds everywhere. Taking away is
// never refused, and the caller editing themselves can gain nothing.
//
// target is nil for an account being created. It writes the 403 and reports
// true when refused. A full administrator is never refused here.
func refuseGain(w http.ResponseWriter, r *http.Request, resolver *acl.Resolver, target *model.User, change func(*perm.Input)) bool {
	ctx := r.Context()
	if callerIsFullAdmin(ctx) {
		return false
	}
	_, mine := delegatedCaller(w, r, resolver)
	if mine == nil {
		return true
	}
	cur := &perm.Result{}
	subject := &model.User{}
	if target != nil {
		subject = target
		var err error
		if cur, err = resolver.Perms(ctx, target); err != nil || cur == nil {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "forbidden"})
			return true
		}
	}
	after, err := resolver.PermsPreview(ctx, subject, change)
	if err != nil || after == nil {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "forbidden"})
		return true
	}
	gained := (after.Allowed &^ cur.Allowed) | (after.AllowedInFolders() &^ reachOf(cur))
	if extra := gained &^ mine.Allowed; extra != 0 {
		writeJSON(w, http.StatusForbidden, map[string]any{
			"error":       "you cannot leave an account holding a permission you do not hold",
			"permissions": extra.Strings(),
		})
		return true
	}
	return false
}

// refuseTakeover is the line on handing a delegated administrator an
// account's credentials (resetting or setting its password): only for an
// account that holds nothing they do not — otherwise signing in as it IS the
// escalation, whatever refuseAdminTarget says about administrators. Their
// own account is always theirs. It writes the 403 and reports true when
// refused; a full administrator is never refused here.
func refuseTakeover(w http.ResponseWriter, r *http.Request, resolver *acl.Resolver, target *model.User) bool {
	ctx := r.Context()
	if callerIsFullAdmin(ctx) {
		return false
	}
	caller, mine := delegatedCaller(w, r, resolver)
	if mine == nil {
		return true
	}
	if target == nil || target.ID == caller.ID {
		return false
	}
	cur, err := resolver.Perms(ctx, target)
	if err != nil || cur == nil {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "forbidden"})
		return true
	}
	if extra := reachOf(cur) &^ mine.Allowed; extra != 0 {
		writeJSON(w, http.StatusForbidden, map[string]any{
			"error":       "this account holds permissions you do not; only an administrator can set its password",
			"permissions": extra.Strings(),
		})
		return true
	}
	return false
}
