// Package handlers — e2e_policy_files.go
//
// Who may encrypt (internal/e2epolicy), the explorer's half:
//
//	POST /api/files/e2e/allowed   - may I encrypt here? {"items":[{"path":"Depo://a","kind"?:"folder"|"new_folder"|"file"}]} → {"encrypt":["allowed"|"request"|"denied",…]}
//	POST /api/files/e2e/requests  - leave a request: {"path":"Depo://a/b","kind":"folder"|"new_folder"|"file","reason":"…"}
//	GET  /api/files/e2e/requests  — the caller's own requests
//
// A request is what the approval policy asks of somebody who holds
// files.encrypt: an administrator of their tenant approves it on
// Admin → Encryption (handlers/e2e_policy_admin.go), and the approval opens
// one encryption of its kind there, once, for seven days.
//
// The service writes the audit rows; /api/files/e2e/requests is not one of the
// file paths the audit middleware records (auth.shouldAudit), so one event is
// one row.
package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/brf-tech/filex/backend/internal/acl"
	"github.com/brf-tech/filex/backend/internal/auth"
	"github.com/brf-tech/filex/backend/internal/confine"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/e2epolicy"
	"github.com/brf-tech/filex/backend/internal/model"
)

// E2EPolicyFiles is the handler set.
type E2EPolicyFiles struct {
	Store    db.Store
	Policy   *e2epolicy.Service
	Requests *e2epolicy.Requests
}

// NewE2EPolicyFiles constructs the handler.
func NewE2EPolicyFiles(store db.Store, policy *e2epolicy.Service, requests *e2epolicy.Requests) *E2EPolicyFiles {
	return &E2EPolicyFiles{Store: store, Policy: policy, Requests: requests}
}

// e2eRequestWire is one encryption request on the wire: the admin table's row
// and the requester's own list. Every key is always present.
type e2eRequestWire struct {
	ID           int64      `json:"id"`
	Path         string     `json:"path"`
	Storage      string     `json:"storage"`
	Kind         string     `json:"kind"`
	Reason       string     `json:"reason"`
	Status       string     `json:"status"`
	Requester    string     `json:"requester"`
	RequesterID  int64      `json:"requester_id"`
	Decider      string     `json:"decider"`
	DecidedAt    *time.Time `json:"decided_at"`
	DecisionNote string     `json:"decision_note"`
	ExpiresAt    time.Time  `json:"expires_at"`
	UsedAt       *time.Time `json:"used_at"`
	CreatedAt    time.Time  `json:"created_at"`
	TenantID     *int64     `json:"tenant_id"`
	// Decidable: the administrator reading the list may answer this request.
	// False for another tenant's request on the platform operator's list
	// (e2epolicy.MayDecide), and on a requester's own list.
	Decidable bool `json:"decidable"`
}

func e2eRequestView(r *model.E2ERequest, storage string) e2eRequestWire {
	return e2eRequestWire{
		ID: r.ID, Path: joinAdapterPath(storage, r.Path), Storage: storage, Kind: r.Kind,
		Reason: r.Reason, Status: r.Status, Requester: r.Requester, RequesterID: r.UserID,
		Decider: r.Decider, DecidedAt: r.DecidedAt, DecisionNote: r.DecisionNote,
		ExpiresAt: r.ExpiresAt, UsedAt: r.UsedAt, CreatedAt: r.CreatedAt, TenantID: r.ProviderID,
	}
}

// e2eRequestViews renders requests, naming each storage once. keep, when
// set, drops the rows it refuses (a folder-confined caller's); decidable,
// when set, says which rows the reader may answer.
func e2eRequestViews(ctx context.Context, store db.Store, rows []*model.E2ERequest, keep func(storage, rel string) bool, decidable func(*model.E2ERequest) bool) []e2eRequestWire {
	names := map[int64]string{}
	out := make([]e2eRequestWire, 0, len(rows))
	for _, r := range rows {
		name, seen := names[r.StorageID]
		if !seen {
			if st, err := store.GetStorage(ctx, r.StorageID); err == nil && st != nil {
				name = st.Name
			}
			names[r.StorageID] = name
		}
		if keep != nil && !keep(name, r.Path) {
			continue
		}
		v := e2eRequestView(r, name)
		v.Decidable = decidable != nil && decidable(r)
		out = append(out, v)
	}
	return out
}

// writeE2ERequestError answers a refusal from the request service with the
// code a client switches on.
func writeE2ERequestError(w http.ResponseWriter, err error) {
	var re *e2epolicy.RequestError
	if !errors.As(err, &re) {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed", "message": err.Error()})
		return
	}
	body := map[string]any{"error": re.Code, "message": re.Message}
	if re.Answer != "" {
		body["answer"] = string(re.Answer)
	}
	if re.Reason != "" {
		body["reason"] = string(re.Reason)
	}
	writeJSON(w, re.Status, body)
}

// decodeOptionalJSON reads a body that may be absent (a bare POST decides
// with no note). False, with the 400 written, when one was sent and is not
// JSON.
func decodeOptionalJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(dst)
	if err == nil || errors.Is(err, io.EOF) {
		return true
	}
	writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad_request", "message": "bad json"})
	return false
}

// place reads a request's wire path the way a durable request needs it: the
// storage named — an approval is authorisation state, and guessing its
// storage is not recoverable (grants.go resolvePath says the same) — inside
// the caller's root when a token or X-Filex-Root confines them, inside the
// caller's tenant and switched on, where another tenant's storage reads as
// one that does not exist, and no "..".
//
// ⚠ The root is read here although confine.Middleware confines the path of
// a JSON body on the way in: CreateRequest decodes a body sent as anything
// else too, and the middleware leaves that alone. Read before the storage is
// looked up and answered in the middleware's words, as it refuses another
// storage.
func (h *E2EPolicyFiles) place(w http.ResponseWriter, r *http.Request, wire string) (*model.Storage, string, bool) {
	adapter, rel := splitAdapterPath(wire)
	if adapter == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "bad_request", "message": `path must name a storage, e.g. "Dosyalar://klasor"`,
		})
		return nil, "", false
	}
	if root, rooted := callerRoot(r.Context()); rooted && !root.Within(adapter, rel) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": confine.ErrOutOfRoot.Error()})
		return nil, "", false
	}
	st, err := h.Store.GetStorageByName(r.Context(), adapter)
	if err != nil || st == nil || !st.Enabled || !scopeOf(r.Context()).CanAccessStorage(st.ID) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not_found", "message": "unknown adapter: " + adapter})
		return nil, "", false
	}
	if pathHasDotDot(rel) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad_request", "message": "bad path"})
		return nil, "", false
	}
	return st, acl.CleanRel(rel), true
}

// CreateRequest leaves a request: POST /api/files/e2e/requests. 201 with the
// new request, 200 with the one already waiting for this person and place.
// 404 path_missing when nothing is at the path, 429 too_many_pending when
// the person has e2epolicy.MaxPendingPerPerson waiting already.
func (h *E2EPolicyFiles) CreateRequest(w http.ResponseWriter, r *http.Request) {
	u := auth.UserFrom(r.Context())
	if u == nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	var body struct {
		Path   string `json:"path"`
		Kind   string `json:"kind"`
		Reason string `json:"reason"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad_request", "message": "bad json"})
		return
	}
	st, rel, ok := h.place(w, r, body.Path)
	if !ok {
		return
	}
	req, created, err := h.Requests.Create(r.Context(), e2epolicy.CreateInput{
		User: u, Storage: st, Path: rel, Kind: body.Kind, Reason: body.Reason, Who: e2eActorOf(r),
	})
	if err != nil {
		writeE2ERequestError(w, err)
		return
	}
	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	writeJSON(w, status, map[string]any{"request": e2eRequestView(req, st.Name), "created": created})
}

// MyRequests answers the caller's own requests, newest first:
// GET /api/files/e2e/requests. A token confined to a folder (`root:`) sees
// the ones inside it, as it sees nothing else outside it.
func (h *E2EPolicyFiles) MyRequests(w http.ResponseWriter, r *http.Request) {
	u := auth.UserFrom(r.Context())
	if u == nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	rows, err := h.Requests.Mine(r.Context(), u.ID)
	if err != nil {
		writeE2ERequestError(w, err)
		return
	}
	var keep func(storage, rel string) bool
	if root, rooted := callerRoot(r.Context()); rooted {
		keep = root.Within
	}
	writeJSON(w, http.StatusOK, map[string]any{"requests": e2eRequestViews(r.Context(), h.Store, rows, keep, nil)})
}

// e2eAllowedMaxItems bounds one question, as vfAllowedMaxItems does the
// permission question; a bigger selection is asked in parts.
const e2eAllowedMaxItems = 1000

// Allowed answers, for each path, what the explorer may offer there:
// POST /api/files/e2e/allowed {"items":[{"path":"Depo://a","kind":"new_folder"}]}
// → {"encrypt":["allowed"|"request"|"denied",…], "reasons":["",…]}, in the
// order of the items. A reason names the layer that said no, as a 403
// e2e_not_allowed would (e2epolicy.Reason; approval_required with "request",
// "" with "allowed"): the CLI says it before asking for a password.
//
// An item's kind says which encryption is asked about: `folder` (the path is
// the folder to encrypt where it is), `new_folder` (the folder to make a new
// encrypted folder in), `file` (the file to encrypt), or none for the one the
// catalogue says. The answer is e2epolicy.Service.AnswersForKinds there - the
// rule every create door asks (CheckCreate), with the approvals that door
// would spend looked for and never spent, so the menu offers exactly what the
// server then accepts. A kind it does not know answers "denied". It changes
// nothing, so it needs `read`, like POST ?action=allowed beside it.
//
// ⚠ Asked for administrators too, unlike the permission question: policy
// `off` stops them, and so does their tenant's ceiling.
//
// A path it cannot place — a storage outside the caller's tenant (the list is
// the tenant's, tenantstore), "..", a storage nobody has — answers "denied".
// So does a path outside the caller's root, when a token's `root:` scope or
// X-Filex-Root confines them to a folder: nothing of the rule is heard there.
//
// ⚠ confine.Middleware refuses such a path in a JSON body, but it reads no
// other body, and this handler decodes JSON whatever the Content-Type says —
// so the root is read here too, before any rule is asked, as place() reads it
// for a request.
//
// The answer is a list and not keyed by path: a folder-confined token's paths
// are rewritten on the way in (confine.Middleware), so the caller would not
// recognise them.
func (h *E2EPolicyFiles) Allowed(w http.ResponseWriter, r *http.Request) {
	u := auth.UserFrom(r.Context())
	if u == nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	var body struct {
		Items []struct {
			Path string `json:"path"`
			Kind string `json:"kind"`
		} `json:"items"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad json"})
		return
	}
	if len(body.Items) > e2eAllowedMaxItems {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "too many items"})
		return
	}
	storages, err := h.Store.ListEnabledStorages(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	byName := make(map[string]*model.Storage, len(storages))
	for _, s := range storages {
		byName[s.Name] = s
	}
	// Asked per storage: AnswersFor finds the tenant, the policy and the
	// person's permissions once for all of a storage's paths.
	type asked struct {
		at  int
		ask e2epolicy.Ask
	}
	perStorage := map[*model.Storage][]asked{}
	out := make([]string, len(body.Items))
	why := make([]string, len(body.Items))
	root, rooted := callerRoot(r.Context())
	for i, it := range body.Items {
		out[i], why[i] = string(e2epolicy.AnswerDenied), string(e2epolicy.ReasonPermission)
		adapter, rel := splitAdapterPath(it.Path)
		if adapter == "" && len(storages) > 0 {
			adapter = storages[0].Name
		}
		// Outside the root the item stays "denied", before the rule is asked.
		if rooted && !root.Within(adapter, rel) {
			continue
		}
		kind := strings.TrimSpace(it.Kind)
		if kind != "" && !model.ValidE2ERequestKind(kind) {
			continue
		}
		if st := byName[adapter]; st != nil && !pathHasDotDot(rel) {
			perStorage[st] = append(perStorage[st], asked{at: i, ask: e2epolicy.Ask{Rel: rel, Kind: kind}})
		}
	}
	for st, items := range perStorage {
		asks := make([]e2epolicy.Ask, len(items))
		for k, a := range items {
			asks[k] = a.ask
		}
		for k, v := range h.Policy.VerdictsFor(r.Context(), u, st, asks) {
			out[items[k].at], why[items[k].at] = string(v.Answer), string(v.Reason)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"encrypt": out, "reasons": why})
}
