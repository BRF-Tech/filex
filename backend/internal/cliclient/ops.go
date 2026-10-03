package cliclient

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// The server's operations queue (GET /api/files/ops/{id}): a copy, a move, an
// app's action, an archive's creation or extraction is accepted with 202 and
// an op, and finishes in the background. The CLI follows the op to its end so
// a command's exit status says what happened to the files, not merely that the
// server took the request.

// Op is one job of the operations queue, the fields the CLI reads.
type Op struct {
	ID      int64      `json:"id"`
	Kind    string     `json:"kind"`
	Status  string     `json:"status"` // pending | running | ok | failed | partial | cancelling | cancelled
	Total   int        `json:"total"`
	Done    int        `json:"done"`
	Failed  int        `json:"failed"`
	Error   string     `json:"error,omitempty"`
	Message string     `json:"message,omitempty"`
	Outputs []OpOutput `json:"outputs,omitempty"`
}

// OpOutput is a file an app's action wrote (`adapter://path` or a
// storage-relative path, as the server reports it).
type OpOutput struct {
	Path string `json:"path"`
}

// Finished reports whether the op has reached a final state.
func (o *Op) Finished() bool {
	switch o.Status {
	case "ok", "failed", "partial", "cancelled":
		return true
	}
	return false
}

// OpError is an op that ended any other way than `ok`: failed, partly done or
// cancelled. Op carries the final row.
type OpError struct {
	Op *Op
}

func (e *OpError) Error() string {
	msg := e.Op.Error
	if msg == "" {
		msg = e.Op.Message
	}
	switch e.Op.Status {
	case "partial":
		if msg == "" {
			return fmt.Sprintf("operation #%d finished with %d of %d item(s) failed", e.Op.ID, e.Op.Failed, e.Op.Total)
		}
		return fmt.Sprintf("operation #%d finished with %d of %d item(s) failed: %s", e.Op.ID, e.Op.Failed, e.Op.Total, msg)
	case "cancelled":
		return fmt.Sprintf("operation #%d was cancelled", e.Op.ID)
	}
	if msg == "" {
		msg = "no reason given"
	}
	return fmt.Sprintf("operation #%d failed: %s", e.Op.ID, msg)
}

// DefaultOpPollInterval is how often WaitOp asks about a running op.
const DefaultOpPollInterval = 500 * time.Millisecond

func (c *Client) opPollInterval() time.Duration {
	if c.OpPollInterval > 0 {
		return c.OpPollInterval
	}
	return DefaultOpPollInterval
}

// GetOp reads one op (GET /api/files/ops/{id}); the answer is the op itself.
func (c *Client) GetOp(ctx context.Context, id int64) (*Op, []byte, error) {
	req, err := c.newRequest(ctx, http.MethodGet, "/api/files/ops/"+strconv.FormatInt(id, 10), nil, nil)
	if err != nil {
		return nil, nil, err
	}
	raw, err := c.doJSON(req)
	if err != nil {
		return nil, nil, err
	}
	var op Op
	if err := json.Unmarshal(raw, &op); err != nil {
		return nil, nil, fmt.Errorf("parse operation: %w", err)
	}
	return &op, raw, nil
}

// WaitOp follows an op until it is finished and returns its final row. An op
// that did not end `ok` is an *OpError (the row is returned as well). A
// cancelled ctx stops the waiting, not the op: the server goes on with it.
func (c *Client) WaitOp(ctx context.Context, id int64) (*Op, []byte, error) {
	for {
		op, raw, err := c.GetOp(ctx, id)
		if err != nil {
			return nil, nil, err
		}
		if op.Finished() {
			if op.Status != "ok" {
				return op, raw, &OpError{Op: op}
			}
			return op, raw, nil
		}
		select {
		case <-ctx.Done():
			return op, raw, fmt.Errorf("stopped waiting for operation #%d (%s); the server carries on with it: %w", id, op.Status, ctx.Err())
		case <-time.After(c.opPollInterval()):
		}
	}
}

// postJSON POSTs body as JSON to p and returns the raw answer.
func (c *Client) postJSON(ctx context.Context, p string, body any) ([]byte, error) {
	b, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	req, err := c.newRequest(ctx, http.MethodPost, p, nil, bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	return c.doJSON(req)
}

// opOf reads the `{"op": …}` envelope a queued request answers with; nil when
// the answer carries none (a server that did the work in the request itself).
func opOf(raw []byte) (*Op, error) {
	var env struct {
		Op *Op `json:"op"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil, fmt.Errorf("parse answer: %w", err)
	}
	if env.Op == nil || env.Op.ID <= 0 {
		return nil, nil
	}
	return env.Op, nil
}

// settle waits for the op in a queued request's answer, when there is one.
// The raw answer returned is the op's final row (or the original answer when
// nothing was queued), so `--json` prints what happened, not "accepted".
func (c *Client) settle(ctx context.Context, raw []byte) (*Op, []byte, error) {
	op, err := opOf(raw)
	if err != nil || op == nil {
		return nil, raw, err
	}
	if op.Finished() {
		if op.Status != "ok" {
			return op, raw, &OpError{Op: op}
		}
		return op, raw, nil
	}
	return c.WaitOp(ctx, op.ID)
}

// ───────────────────────── cp / mv ─────────────────────────

// ErrTargetExists is a copy or move to a full path whose name is already taken.
// Nothing was sent: the CLI never replaces, and the server would put the item
// beside it under another name rather than at the path that was typed.
var ErrTargetExists = errors.New("the target already exists")

// transferPlan is where one copy or move lands.
type transferPlan struct {
	destDir RemotePath // the folder it goes into
	name    string     // its name there; "" keeps its own
	landed  RemotePath // where it ends up (when the name is free)
	rename  bool       // same folder, same storage: the rename verb
}

// planTransfer applies Unix cp/mv semantics to dst: the storage root, a
// trailing slash or an existing folder receives the item under its own name;
// anything else is the item's full new path.
func (c *Client) planTransfer(ctx context.Context, verb string, sp RemotePath, dst string) (transferPlan, error) {
	dp, err := ParseRemotePath(dst)
	if err != nil {
		return transferPlan{}, err
	}
	if dp.IsRoot() || strings.HasSuffix(dst, "/") || c.remoteIsDir(ctx, dp) {
		return transferPlan{destDir: dp, landed: dp.Join(sp.Base())}, nil
	}
	plan := transferPlan{destDir: dp.Dir(), name: dp.Base(), landed: dp}
	if verb == "move" && dp.Adapter == sp.Adapter && dp.Dir().Rel == sp.Dir().Rel {
		plan.rename = true
		return plan, nil
	}
	if _, err := c.entry(ctx, dp); err == nil {
		return transferPlan{}, fmt.Errorf("%w: %s - nothing was %s; remove it first or pick another name", ErrTargetExists, dp.String(), pastTense(verb))
	} else if !errors.Is(err, errNoSuchEntry) {
		return transferPlan{}, err
	}
	return plan, nil
}

func pastTense(verb string) string {
	if verb == "copy" {
		return "copied"
	}
	return "moved"
}

// transfer queues one copy or move (POST /api/files/{copy|move}) and waits for
// it. The server takes the source's storage from its `adapter://` prefix and
// the destination's from the target's, so a transfer between two storages is
// the same request as one inside a storage; `name`, given, is the item's name
// in the target folder, moved or copied and named in ONE step of the queue.
func (c *Client) transfer(ctx context.Context, verb string, src RemotePath, plan transferPlan) (*Op, []byte, error) {
	body := map[string]any{
		"source": []string{src.String()},
		"target": plan.destDir.String(),
	}
	if plan.name != "" {
		body["name"] = plan.name
	}
	raw, err := c.postJSON(ctx, "/api/files/"+verb, body)
	if err != nil {
		return nil, nil, err
	}
	op, final, err := c.settle(ctx, raw)
	if err == nil && op == nil {
		return nil, raw, fmt.Errorf("the server accepted the %s but named no operation to follow", verb)
	}
	return op, final, err
}

// Transfer is what one copy or move did.
type Transfer struct {
	// Into is true when the target was a folder the item went into under its
	// own name. A name already taken there makes the server pick a free one
	// beside it (`a-copy.txt`), so To is then where the item WOULD be.
	Into bool
	// Folder is the folder the item went into.
	Folder RemotePath
	// To is the item's path: the full path the target named, or Folder joined
	// with the item's own name.
	To RemotePath
	// Op is the queued operation's final row; nil for a rename in place.
	Op *Op
	// Raw is the server's last answer (the op's final row).
	Raw []byte
}

// Move implements Unix-mv semantics. dst may be an existing folder (the item
// moves into it, keeping its name - a name already taken there gets a free one
// beside it, as in the web explorer) or the item's full new path:
//
//   - in the same folder of the same storage, a rename (the server's rename
//     verb: a taken name is refused with 409 NAME_TAKEN, a case-only rename
//     works);
//   - anywhere else - another folder, another storage, or both - one queued
//     move that also names the item, so nothing can stop half-way between a
//     move and a rename. A taken target is refused before anything is sent
//     (ErrTargetExists).
//
// A move between two storages is the server's: it streams the bytes across
// and removes the source once they have arrived.
func (c *Client) Move(ctx context.Context, src, dst string) (*Transfer, error) {
	sp, plan, t, err := c.startTransfer(ctx, "move", src, dst, "cannot move a storage root")
	if err != nil {
		return nil, err
	}
	if plan.rename {
		t.Raw, err = c.rename(ctx, sp, plan.name)
		return t, err
	}
	t.Op, t.Raw, err = c.transfer(ctx, "move", sp, plan)
	return t, err
}

// Copy implements Unix-cp semantics with Move's target rules (no rename
// short-cut: a copy into its own folder under another name is a copy). A
// copy into a folder that already holds the name lands beside it as
// `name-copy.ext`; a copy to a taken full path is refused (ErrTargetExists).
// Folders are copied whole.
func (c *Client) Copy(ctx context.Context, src, dst string) (*Transfer, error) {
	sp, plan, t, err := c.startTransfer(ctx, "copy", src, dst, "cannot copy a storage root - copy its folders")
	if err != nil {
		return nil, err
	}
	t.Op, t.Raw, err = c.transfer(ctx, "copy", sp, plan)
	return t, err
}

// startTransfer is the start Move and Copy share: the source parsed and not a
// storage root (rootRefusal says so), the target planned (planTransfer), and
// the answer's frame.
func (c *Client) startTransfer(ctx context.Context, verb, src, dst, rootRefusal string) (RemotePath, transferPlan, *Transfer, error) {
	sp, err := ParseRemotePath(src)
	if err != nil {
		return RemotePath{}, transferPlan{}, nil, err
	}
	if sp.IsRoot() {
		return RemotePath{}, transferPlan{}, nil, errors.New(rootRefusal)
	}
	plan, err := c.planTransfer(ctx, verb, sp, dst)
	if err != nil {
		return RemotePath{}, transferPlan{}, nil, err
	}
	return sp, plan, &Transfer{Into: plan.name == "", Folder: plan.destDir, To: plan.landed}, nil
}

// IsDir reports whether remote is a folder the caller can list (the storage
// root always is).
func (c *Client) IsDir(ctx context.Context, remote string) (bool, error) {
	rp, err := ParseRemotePath(remote)
	if err != nil {
		return false, err
	}
	if rp.IsRoot() {
		return true, nil
	}
	return c.remoteIsDir(ctx, rp), nil
}

// errNoSuchEntry is entry's "the folder holds nothing by that name".
var errNoSuchEntry = errors.New("no such file or folder")

// entry finds p in its folder's listing. The listing is the only way to ask
// "what is at this path" that every server version answers, and its row
// carries the catalogue id versions and tags are addressed by.
func (c *Client) entry(ctx context.Context, p RemotePath) (*ListEntry, error) {
	if p.IsRoot() {
		return nil, errors.New("a storage root is not a file")
	}
	res, err := c.List(ctx, p.Dir().String())
	if err != nil {
		var ae *APIError
		if errors.As(err, &ae) && ae.Status == http.StatusNotFound {
			return nil, fmt.Errorf("%s: %w", p.String(), errNoSuchEntry)
		}
		return nil, err
	}
	for i := range res.Files {
		if res.Files[i].Basename == p.Base() {
			return &res.Files[i], nil
		}
	}
	return nil, fmt.Errorf("%s: %w", p.String(), errNoSuchEntry)
}

// NodeID is the server's catalogue id of the file or folder at remote - what
// versions and tags are addressed by.
func (c *Client) NodeID(ctx context.Context, remote string) (int64, error) {
	rp, err := ParseRemotePath(remote)
	if err != nil {
		return 0, err
	}
	e, err := c.entry(ctx, rp)
	if err != nil {
		return 0, err
	}
	if e.ID <= 0 {
		return 0, fmt.Errorf("%s has no record on the server yet: it is on the storage but not catalogued (the next scan of the storage catalogues it); pass --id when you know it", rp.String())
	}
	return e.ID, nil
}
