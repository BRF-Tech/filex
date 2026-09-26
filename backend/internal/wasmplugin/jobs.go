package wasmplugin

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/ops"
	"github.com/brf-tech/filex/backend/internal/srvtext"
	"github.com/brf-tech/filex/backend/internal/storage"
	"github.com/brf-tech/filex/backend/internal/writegate"
	"github.com/brf-tech/filex/backend/pkg/pluginkit/wire"
)

// ── Actions for a caller, jobs, and the ops runner ─────────────────────

// ActionRow is one menu entry as GET /api/files/plugins/actions returns it.
type ActionRow struct {
	Plugin  string       `json:"plugin"`
	ID      string       `json:"id"`
	Key     string       `json:"key"`
	Label   wire.Text    `json:"label"`
	Icon    string       `json:"icon,omitempty"`
	Applies wire.Applies `json:"applies"`
	View    string       `json:"view,omitempty"`
	// ViewPlacement is the declared placement of View (modal | page), so
	// the menu knows whether to open a dialog or a new tab.
	ViewPlacement string    `json:"view_placement,omitempty"`
	Confirm       wire.Text `json:"confirm,omitempty"`
	MinRole       string    `json:"min_role,omitempty"`
	Danger        bool      `json:"danger,omitempty"`
	OutputMode    string    `json:"output_mode"`
	// OutputElsewhere: the result may go into a folder the person chooses
	// (manifest output.elsewhere), so the action is offered on a read-only
	// storage too — its screen asks where the result should go.
	OutputElsewhere bool `json:"output_elsewhere,omitempty"`
	// Gated is what the action WOULD also be offered on once a requirement
	// the server lacks is met — the extensions LibreOffice would add to the
	// signing app's "Sign…", say. Sent to administrators only: the owner's
	// rule for anything that depends on how the server is set up is "greyed
	// with the reason for an administrator, not shown to anybody else", so
	// the explorer draws each as a disabled row that says what is missing.
	Gated []GatedRule `json:"gated,omitempty"`
}

// GatedRule is one "offered once X is there" part of an action's rule.
type GatedRule struct {
	Ext   []string `json:"ext,omitempty"`
	Needs Need     `json:"needs"`
}

// Need is a requirement the server does not meet: `kind` says which sort
// ("engine" today), `id` the machine's word, `name` the one a person reads.
type Need struct {
	Kind string `json:"kind"`
	ID   string `json:"id"`
	Name string `json:"name"`
}

// ViewRow is one non-modal view (inspector/home) the explorer may offer.
type ViewRow struct {
	Plugin    string       `json:"plugin"`
	ID        string       `json:"id"`
	Placement string       `json:"placement"`
	Label     wire.Text    `json:"label"`
	Icon      string       `json:"icon,omitempty"`
	Applies   wire.Applies `json:"applies"`
}

// ActionsAnswer is the whole list for one caller.
type ActionsAnswer struct {
	Actions []ActionRow `json:"actions"`
	Views   []ViewRow   `json:"views"`
}

// ActionsFor lists what the caller may see: running plugins, enabled
// actions, admin-only ones only for admins.
func (r *Registry) ActionsFor(ctx context.Context, isAdmin bool) (*ActionsAnswer, error) {
	out := &ActionsAnswer{Actions: []ActionRow{}, Views: []ViewRow{}}
	for _, p := range r.All() {
		if state, _ := p.State(); state != StateRunning {
			continue
		}
		effs, err := r.effectiveActions(ctx, p)
		if err != nil {
			return nil, err
		}
		for _, e := range effs {
			if !e.Enabled || (e.AdminOnly && !isAdmin) || e.Action.Hidden {
				continue
			}
			row := ActionRow{
				Plugin: p.Row.Name, ID: e.Action.ID, Key: "plugin:" + p.Row.Name + "/" + e.Action.ID,
				Label: e.Action.Label, Icon: e.Action.Icon, Applies: e.Applies, View: e.Action.View,
				Confirm: e.Action.Confirm, MinRole: e.Action.MinRole, Danger: e.Action.Danger,
				OutputMode: e.Action.Output.Mode, OutputElsewhere: e.Action.Output.Elsewhere,
			}
			if isAdmin {
				row.Gated = e.Gated
			}
			if v, ok := p.Manifest.View(e.Action.View); ok {
				row.ViewPlacement = v.Placement
			}
			out.Actions = append(out.Actions, row)
		}
		for _, v := range p.Manifest.Views {
			if v.Placement == "modal" || v.Placement == "page" {
				continue
			}
			out.Views = append(out.Views, ViewRow{Plugin: p.Row.Name, ID: v.ID, Placement: v.Placement, Label: v.Label, Icon: p.Manifest.Icon, Applies: v.Applies})
		}
	}
	return out, nil
}

// ResolveAction finds a running plugin's enabled action for a run request.
// isAdmin gates admin-only actions. The error is a *CallError with
// CodeUnsupported (not running / disabled) or CodeRefused (admin only).
func (r *Registry) ResolveAction(ctx context.Context, plugin, action string, isAdmin bool) (*Installed, *wire.Action, wire.Applies, error) {
	p, ok := r.ByName(plugin)
	if !ok {
		return nil, nil, wire.Applies{}, &CallError{Code: CodeUnsupported, Message: "no such plugin"}
	}
	if state, serr := p.State(); state != StateRunning {
		return nil, nil, wire.Applies{}, &CallError{Code: CodeUnsupported, Message: "plugin is not running: " + serr}
	}
	effs, err := r.effectiveActions(ctx, p)
	if err != nil {
		return nil, nil, wire.Applies{}, err
	}
	for _, e := range effs {
		if e.Action.ID != action {
			continue
		}
		if !e.Enabled {
			return nil, nil, wire.Applies{}, &CallError{Code: CodeUnsupported, Message: "action is disabled by the administrator"}
		}
		if e.AdminOnly && !isAdmin {
			return nil, nil, wire.Applies{}, &CallError{Code: CodeRefused, Message: "action is restricted to administrators"}
		}
		return p, e.Action, e.Applies, nil
	}
	return nil, nil, wire.Applies{}, &CallError{Code: CodeUnsupported, Message: "no such action"}
}

// PersonalStateKeys is the view of a file's state keys (`<plugin>:<key>`)
// that ONE person gets: a personal key `<key>@<id>` (wire.PersonalState) is
// that person's own and reads `<key>@me`; anybody else's is left out, so a
// listing never says who else has something to do on the file. Other keys
// pass as they are. The listing (app badges) and the run check (authorise)
// both read the state through this, so the menu and the server agree.
func PersonalStateKeys(keys []string, userID int64) []string {
	out := keys[:0:0]
	for _, k := range keys {
		at := strings.LastIndexByte(k, '@')
		if at < 0 || at == len(k)-1 || !allDigits(k[at+1:]) {
			out = append(out, k)
			continue
		}
		if userID > 0 && k[at+1:] == strconv.FormatInt(userID, 10) {
			out = append(out, k[:at]+wire.PersonalStateSuffix)
		}
	}
	return out
}

func allDigits(s string) bool {
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return s != ""
}

// NewJobID mints the random token pending_ops.dest carries.
func NewJobID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// OutputSink lands a finished output on a storage with filex's own
// bookkeeping (kind guard, version snapshot on overwrite, node row, index,
// thumbnail, write hook, change frame). Implemented by the HTTP layer, which
// owns those services; the registry only decides WHAT to write WHERE.
type OutputSink interface {
	// CommitSibling writes a NEW file next to the first input, taking a
	// unique name when the preferred one is taken. Returns the final path.
	CommitSibling(ctx context.Context, storageID int64, dir, name string, r io.Reader, size int64, actor *int64) (string, error)
	// CommitVersion overwrites rel in place (the previous bytes become a
	// version when versioning is on).
	CommitVersion(ctx context.Context, storageID int64, rel string, r io.Reader, size int64, actor *int64) error
}

// Cataloguer records in the catalogue a file the STORAGE has and the
// catalogue has not seen yet, with the same bookkeeping a write through filex
// gets (node row, parent rows, index, thumbnail), and answers its node.
//
// The output sink implements it (handlers.AppPlugins, through protocolsync);
// it is a separate interface so a sink that does not — every test double of
// OutputSink — leaves share_create exactly as strict as it was.
type Cataloguer interface {
	Catalogue(ctx context.Context, storageID int64, rel string) (*model.Node, error)
}

// catalogueInput is share_create's answer to "the job's own input has no
// node" (see hfShareCreate). nil when there is nothing to catalogue with or
// the storage does not have the file either — the caller then answers "no
// such file", as it always did.
func (r *Registry) catalogueInput(ctx context.Context, p *Installed, storageID int64, rel string) *model.Node {
	c, ok := r.sink.(Cataloguer)
	if !ok {
		return nil
	}
	n, err := c.Catalogue(ctx, storageID, rel)
	if err != nil || n == nil {
		if err != nil {
			p.log("warn", "the document "+rel+" is not in the catalogue and could not be added: "+err.Error())
		}
		return nil
	}
	p.log("info", "catalogued "+rel+", which the storage had and the catalogue had not seen yet")
	return n
}

// JobOutput is one committed output, as the ops row reports it.
type JobOutput struct {
	Path string `json:"path"`
}

// ── ops integration ────────────────────────────────────────────────────

// RunPluginAction is what the ops worker calls for an OpPluginAction row.
// op.Dest carries the job id. Progress is fed to live so the tray can draw
// a bar; the job row records status, message and outputs.
func (r *Registry) RunPluginAction(ctx context.Context, op *ops.Op, live func(done, total int64)) error {
	job, err := r.opts.Store.GetAppPluginJob(ctx, op.Dest)
	if err != nil || job == nil {
		return errors.New("app-plugins: job row missing for op")
	}
	job.Status = model.AppPluginJobRunning
	_ = r.opts.Store.UpdateAppPluginJob(ctx, job)

	outputs, msg, err := r.runJob(ctx, job, live)
	now := time.Now()
	job.FinishedAt = &now
	job.Message = msg
	if err != nil {
		if ctx.Err() != nil {
			job.Status = model.AppPluginJobCancelled
			job.Error = msgJobCancelled
		} else {
			job.Status = model.AppPluginJobFailed
			job.Error = userMessage(err)
		}
		_ = r.opts.Store.UpdateAppPluginJob(context.WithoutCancel(ctx), job)
		return errors.New(job.Error)
	}
	job.Status = model.AppPluginJobOK
	job.OutputsJSON = jsonOf(outputs)
	_ = r.opts.Store.UpdateAppPluginJob(context.WithoutCancel(ctx), job)
	return nil
}

// The host's own words for a failed job. They are also what
// classifyJobError reads back, so the two cannot drift apart.
const (
	msgJobTimeout       = "the plugin ran out of time"
	msgJobOOM           = "the plugin ran out of memory"
	msgJobTrap          = "the plugin crashed"
	msgJobPluginRemoved = "plugin was removed"
	msgJobActionRemoved = "action no longer exists"
	msgJobCancelled     = "cancelled"
)

// engineMissingMessage is the host's answer to an engine call on a host that
// has no such binary. An app usually passes it on as its own error, so
// classifyJobError finds it anywhere in the job's text.
func engineMissingMessage(engine string) string {
	return "engine " + engine + " is not installed on this host"
}

var engineMissingRe = regexp.MustCompile(`engine ([A-Za-z0-9_.-]+) is not installed on this host`)

// classifyJobError names a failed job's error for the CLIENT, which says it
// in the person's language (ops row `error_code`, lib/errorWords `jobFailure`).
//
// ⚠⚠ QA, 2026-09-21: the operations centre printed "engine libreoffice is not
// installed on this host" to whoever ran a conversion — English, and a
// sentence about the server's plumbing. The job keeps its text (an
// administrator's second line); the code is what a person reads. "app" is the
// app's own words, which the client shows as they are unless they look like
// plumbing.
func classifyJobError(status, text string) (code, engine string) {
	if status == model.AppPluginJobCancelled || text == msgJobCancelled {
		return "cancelled", ""
	}
	if m := engineMissingRe.FindStringSubmatch(text); m != nil {
		return "engine_missing", m[1]
	}
	switch text {
	case msgJobTimeout:
		return "timeout", ""
	case msgJobOOM:
		return "out_of_memory", ""
	case msgJobTrap:
		return "crashed", ""
	case msgJobPluginRemoved:
		return "app_removed", ""
	case msgJobActionRemoved:
		return "action_removed", ""
	case "":
		return "", ""
	}
	return "app", ""
}

// userMessage is what a failed job tells the user: the plugin's own error
// text, or a short host classification — never a wazero stack.
func userMessage(err error) string {
	var ce *CallError
	if errors.As(err, &ce) {
		switch ce.Code {
		case CodeTimeout:
			return msgJobTimeout
		case CodePluginOOM:
			return msgJobOOM
		case CodePluginTrap:
			return msgJobTrap
		}
		return clip(ce.Message, 500)
	}
	return clip(err.Error(), 500)
}

func (r *Registry) runJob(ctx context.Context, job *model.AppPluginJob, live func(done, total int64)) ([]JobOutput, string, error) {
	p, ok := r.ByID(job.PluginID)
	if !ok {
		return nil, "", &CallError{Code: CodeUnsupported, Message: msgJobPluginRemoved}
	}
	c, err := p.running()
	if err != nil {
		return nil, "", err
	}
	action, ok := p.Manifest.Action(job.ActionID)
	if !ok {
		return nil, "", &CallError{Code: CodeUnsupported, Message: msgJobActionRemoved}
	}
	select {
	case p.sem <- struct{}{}:
	case <-ctx.Done():
		return nil, "", ctx.Err()
	}
	defer func() { <-p.sem }()

	if r.opts.StorageResolver == nil {
		return nil, "", errors.New("no storage resolver")
	}
	drv, err := r.opts.StorageResolver(job.StorageID)
	if err != nil {
		return nil, "", fmt.Errorf("storage: %w", err)
	}
	var paths []string
	_ = json.Unmarshal([]byte(job.PathsJSON), &paths)
	if len(paths) == 0 {
		return nil, "", errors.New("job has no inputs")
	}
	var params map[string]any
	_ = json.Unmarshal([]byte(job.ParamsJSON), &params)
	if ov := OutputOverride(params); ov != nil {
		// The surface's choice for this one job (JobRequest.Output); the
		// handler validated it at submit and put it under __output.
		eff := *action
		eff.Output = *ov
		action = &eff
		delete(params, outputOverrideKey)
	}

	actor := &model.User{}
	if job.ActorID != nil {
		if u, err := r.opts.Store.GetUser(ctx, *job.ActorID); err == nil && u != nil {
			actor = u
		}
	}
	scope, err := newScope(p, r, job.ID, job.StorageID, drv, actor, job.Locale, true)
	if err != nil {
		return nil, "", err
	}
	defer scope.Close()
	scope.storageName, scope.readOnly = r.storageFacts(ctx, job.StorageID)
	// What this job will do with what the plugin writes. share_create reads it
	// to refuse a link against an action that keeps no outputs, which would be
	// a token nothing ever answers.
	scope.outputMode = action.Output.Mode
	scope.progress = func(done, total int64, msg string) {
		if live != nil {
			live(done, total)
		}
		if msg != "" {
			job.Message = msg
			_ = r.opts.Store.UpdateAppPluginJob(context.WithoutCancel(ctx), job)
		}
	}
	for _, rel := range paths {
		rel = strings.TrimPrefix(rel, "/")
		obj, err := drv.Stat(ctx, rel)
		if err != nil {
			return nil, "", fmt.Errorf("%s: %w", path.Base(rel), err)
		}
		if obj.Kind == storage.KindDirectory {
			if action.Applies.Kind == "file" {
				return nil, "", fmt.Errorf("%s is a folder", path.Base(rel))
			}
		} else if r.opts.MaxInputBytes > 0 && obj.Size > r.opts.MaxInputBytes {
			return nil, "", &CallError{Code: CodeRefused, Message: fmt.Sprintf("%s exceeds the %d MiB input limit", path.Base(rel), r.opts.MaxInputBytes>>20)}
		}
		scope.AddInput(rel, obj.Size, obj.Mime)
	}

	in := wire.ActionRunInput{
		JobID: job.ID, ActionID: job.ActionID, Params: params, Inputs: scope.Inputs(),
		Output: action.Output,
		Actor:  wire.Actor{ID: actor.ID, Email: actor.Email, Name: actor.DisplayName, Role: actor.Role},
		Locale: job.Locale, Settings: r.publicSettings(ctx, p), Engines: r.enginesFor(p),
		ShareMaxTTLDays: r.linkCeiling(ctx, p),
	}
	inb, _ := json.Marshal(in)
	budget := time.Duration(JobTimeout(action.Limits)) * time.Second
	outb, err := c.Call(WithScope(ctx, scope), "action_run", inb, budget)
	if err != nil {
		p.log("warn", "action "+job.ActionID+" failed: "+err.Error())
		return nil, "", err
	}
	var out wire.ActionRunOutput
	if err := json.Unmarshal(outb, &out); err != nil {
		return nil, "", &CallError{Code: CodePluginError, Message: "action_run returned malformed JSON"}
	}
	// The job's own language for the error text an administrator reads (and
	// classifyJobError parses); every language the app wrote for the row
	// (EncodeJobText), which the ops list reads in its reader's.
	msg := out.Message.Get(job.Locale)
	words := EncodeJobText(out.Message)
	if !out.OK {
		if msg == "" {
			msg = "the plugin reported a failure"
		}
		return nil, words, &CallError{Code: CodePluginError, Message: msg}
	}
	if action.Output.Mode == "none" || len(out.Outputs) == 0 {
		return []JobOutput{}, words, nil
	}
	if r.sink == nil {
		return nil, "", errors.New("no output sink wired")
	}
	committed := make([]JobOutput, 0, len(out.Outputs))
	// The committer judges every output with writegate, which lets the app
	// that holds a lock write into the file it froze and nobody else — so
	// it has to be told which app this is.
	ctx = writegate.WithApp(ctx, job.PluginID)
	first := strings.TrimPrefix(paths[0], "/")
	var (
		outStorage int64  // a chosen folder's storage (mode folder)
		outName    string // …and its name
	)
	for i, o := range out.Outputs {
		f, ok := scope.Output(o.Ref)
		if !ok {
			return nil, "", &CallError{Code: CodePluginError, Message: "output " + o.Ref + " was not created by this job"}
		}
		if err := scope.closeAllFor(f); err != nil {
			return nil, "", err
		}
		src, err := os.Open(f.Path)
		if err != nil {
			return nil, "", fmt.Errorf("open output: %w", err)
		}
		st, _ := src.Stat()
		size := int64(-1)
		if st != nil {
			size = st.Size()
		}
		var rel string
		switch action.Output.Mode {
		case "version":
			target := first
			if i < len(paths) {
				target = strings.TrimPrefix(paths[i], "/")
			}
			err = r.sink.CommitVersion(ctx, job.StorageID, target, src, size, job.ActorID)
			rel = target
		default:
			name := safeName(o.Name)
			if name == "" || name == f.Name && strings.HasPrefix(f.Ref, "eng:") && action.Output.Name != "" {
				name = outputName(action.Output.Name, first, o.Name)
			}
			if name == "" {
				name = outputName(action.Output.Name, first, "")
			}
			if action.Output.Mode == "folder" {
				// The folder the person chose (checked at submit; the sink
				// judges the written file again with writegate).
				dest, dir, ferr := r.outputFolder(ctx, action.Output.Dir)
				if ferr != nil {
					err = ferr
					break
				}
				outStorage, outName = dest.ID, dest.Name
				rel, err = r.sink.CommitSibling(ctx, dest.ID, dir, name, src, size, job.ActorID)
				break
			}
			rel, err = r.sink.CommitSibling(ctx, job.StorageID, path.Dir(first), name, src, size, job.ActorID)
		}
		src.Close()
		if err != nil {
			return nil, "", fmt.Errorf("write %s: %w", o.Name, err)
		}
		if outName != "" {
			// Written on ANOTHER storage: say which, so the tray and the
			// explorer land on it (DecorateOps qualifies only bare paths).
			committed = append(committed, JobOutput{Path: outName + "://" + rel})
			r.keepPromisedShares(context.WithoutCancel(ctx), scope, p, outStorage, o.Ref, rel)
			r.keepPromisedLock(context.WithoutCancel(ctx), scope, p, outStorage, o.Ref, rel)
			continue
		}
		committed = append(committed, JobOutput{Path: rel})
		// ⚠ The output now HAS a node, which is the one fact a share of it was
		// waiting for. Any link the plugin promised against this ref becomes a
		// row here — and nowhere else, so a job that fails before this point
		// leaves the token it handed out answering nothing (public_promised.go).
		//
		// The context is detached: the person who queued the job closing their
		// browser must not cancel the opening of a link that has already been
		// mailed to somebody else.
		r.keepPromisedShares(context.WithoutCancel(ctx), scope, p, job.StorageID, o.Ref, rel)
		r.keepPromisedLock(context.WithoutCancel(ctx), scope, p, job.StorageID, o.Ref, rel)
	}
	return committed, words, nil
}

// closeAllFor shuts any handle still open on f so its bytes are complete.
func (s *Scope) closeAllFor(f *scopeFile) error {
	s.mu.Lock()
	var ids []uint64
	for id, h := range s.handles {
		if h.f == f {
			ids = append(ids, id)
		}
	}
	s.mu.Unlock()
	for _, id := range ids {
		if err := s.closeHandle(id); err != nil {
			return err
		}
	}
	return nil
}

// outputName renders an output pattern: {stem}, {ext} (with dot), {name}.
// outputOverrideKey is where enqueue stashes a JobRequest.Output inside the
// job params; runJob lifts it back out and hides it from the guest.
const outputOverrideKey = "__output"

// OutputOverride reads the per-job output stored in params, or nil.
func OutputOverride(params map[string]any) *wire.Output {
	raw, ok := params[outputOverrideKey]
	if !ok {
		return nil
	}
	b, err := json.Marshal(raw)
	if err != nil {
		return nil
	}
	var o wire.Output
	if json.Unmarshal(b, &o) != nil || !ValidOutputMode(o.Mode) {
		return nil
	}
	return &o
}

// outputFolder resolves a chosen folder (`docs://reports`) to its storage and
// storage-relative path. A storage that is gone or now read-only refuses the
// write: the submit checked it, but a job runs later.
func (r *Registry) outputFolder(ctx context.Context, qualified string) (*model.Storage, string, error) {
	adapter, rel, ok := strings.Cut(strings.TrimSpace(qualified), "://")
	if !ok || adapter == "" {
		return nil, "", fmt.Errorf("the chosen folder %q is not adapter-qualified", qualified)
	}
	st, err := r.opts.Store.GetStorageByName(ctx, adapter)
	if err != nil || st == nil {
		return nil, "", fmt.Errorf("the chosen folder's storage %q is gone", adapter)
	}
	if st.ReadOnly {
		return nil, "", fmt.Errorf("read_only: the chosen folder's storage is read-only")
	}
	rel = strings.Trim(path.Clean("/"+rel), "/")
	if rel == "." {
		rel = ""
	}
	return st, rel, nil
}

// SetOutputOverride stores o under the override key.
func SetOutputOverride(params map[string]any, o *wire.Output) {
	v := map[string]any{"mode": o.Mode, "name": o.Name}
	if o.Dir != "" {
		v["dir"] = o.Dir
	}
	params[outputOverrideKey] = v
}

// ValidOutputMode is the closed set an Output.Mode may take. `folder` is a
// per-job choice only (a manifest declares `sibling` + `elsewhere`).
func ValidOutputMode(m string) bool {
	return m == "sibling" || m == "version" || m == "none" || m == "folder"
}

func outputName(pattern, firstInput, suggested string) string {
	base := path.Base(firstInput)
	ext := path.Ext(base)
	stem := strings.TrimSuffix(base, ext)
	if pattern == "" {
		if suggested != "" {
			return safeName(suggested)
		}
		pattern = "{stem}-out{ext}"
	}
	out := strings.NewReplacer("{stem}", stem, "{ext}", ext, "{name}", base).Replace(pattern)
	return safeName(out)
}

func (r *Registry) enginesFor(p *Installed) map[string]bool {
	out := map[string]bool{}
	for name, present := range r.engines.Available() {
		out[name] = present && p.Grants.HasEngine(name)
	}
	return out
}

// DecorateOps fills the plugin fields of ops rows that are plugin jobs.
// DecorateOps fills the plugin columns of ops rows. A read failure is
// logged rather than swallowed: the row still renders, but as a nameless
// "plugin-action" with no message, which is how two different lost-update
// bugs stayed invisible.
func (r *Registry) DecorateOps(ctx context.Context, rows []*ops.Op) {
	var ids []int64
	for _, op := range rows {
		if op != nil && op.Kind == ops.OpPluginAction {
			ids = append(ids, op.ID)
		}
	}
	if len(ids) == 0 {
		return
	}
	jobs, err := r.opts.Store.ListAppPluginJobsByOp(ctx, ids)
	if err != nil {
		r.log.Warn("app-plugins: ops rows cannot be decorated", "ops", len(ids), "err", err.Error())
		return
	}
	names := map[int64]string{}
	// The reader's language: the words were kept in every language the app
	// wrote (jobtext.go), and are said here in the one on the reader's screen.
	lang := srvtext.Reader(ctx)
	for _, op := range rows {
		j := jobs[op.ID]
		if j == nil {
			continue
		}
		op.Plugin = j.PluginName
		op.Action = j.ActionID
		op.Label = JobText(j.Label, lang)
		op.Message = JobText(j.Message, lang)
		if j.Status == model.AppPluginJobFailed || j.Status == model.AppPluginJobCancelled {
			op.ErrorCode, op.ErrorEngine = classifyJobError(j.Status, j.Error)
			// An app that said WHY it failed said it in every language it
			// speaks; the queue row only kept the job's own.
			if j.Status == model.AppPluginJobFailed && op.ErrorCode == "app" && j.Message != "" {
				op.Error = JobText(j.Message, lang)
			}
		}
		var outs []ops.OpOutput
		_ = json.Unmarshal([]byte(j.OutputsJSON), &outs)
		// Outputs are reported adapter-qualified (`docs://x/y.pdf`), the form
		// the explorer navigates by; the job row keeps them storage-relative.
		name, ok := names[j.StorageID]
		if !ok {
			if st, err := r.opts.Store.GetStorage(ctx, j.StorageID); err == nil && st != nil {
				name = st.Name
			}
			names[j.StorageID] = name
		}
		for i := range outs {
			if name != "" && !strings.Contains(outs[i].Path, "://") {
				outs[i].Path = name + "://" + strings.TrimPrefix(outs[i].Path, "/")
			}
		}
		op.Outputs = outs
	}
}

// ── Views (M1: modal views opened from an action; sanitised surfaces) ──

// ViewEvent runs one view event and returns the sanitised surface.
func (r *Registry) ViewEvent(ctx context.Context, plugin, view string, storageID int64, rels []string, actor *model.User, locale string, in wire.ViewEventInput) (*wire.Surface, error) {
	p, ok := r.ByName(plugin)
	if !ok {
		return nil, &CallError{Code: CodeUnsupported, Message: "no such plugin"}
	}
	c, err := p.running()
	if err != nil {
		return nil, err
	}
	if _, ok := p.Manifest.View(view); !ok {
		return nil, &CallError{Code: CodeUnsupported, Message: "no such view"}
	}
	var drv storage.Driver
	if storageID > 0 && r.opts.StorageResolver != nil {
		drv, _ = r.opts.StorageResolver(storageID)
	}
	scope, err := newScope(p, r, "", storageID, drv, actor, locale, false)
	if err != nil {
		return nil, err
	}
	defer scope.Close()
	scope.storageName, scope.readOnly = r.storageFacts(ctx, storageID)
	for _, rel := range rels {
		rel = strings.TrimPrefix(rel, "/")
		var size int64
		var mime string
		if drv != nil {
			if obj, err := drv.Stat(ctx, rel); err == nil {
				size, mime = obj.Size, obj.Mime
			}
		}
		scope.AddInput(rel, size, mime)
	}
	in.ViewID = view
	in.Context = wire.CallContext{Inputs: scope.Inputs(), Locale: locale, Settings: r.publicSettings(ctx, p), Engines: r.enginesFor(p),
		ShareMaxTTLDays: r.linkCeiling(ctx, p)}
	// Where a result goes when it cannot go beside its source — asked only
	// for an app that can put one elsewhere (it costs a look at every
	// storage the person has).
	if r.home != nil && actor != nil && p.Manifest.WritesElsewhere() {
		in.Context.Home = r.home(ctx, actor)
	}
	if actor != nil {
		in.Context.Actor = &wire.Actor{ID: actor.ID, Email: actor.Email, Name: actor.DisplayName, Role: actor.Role,
			IP: actorIPFrom(ctx)}
	}
	inb, _ := json.Marshal(in)
	outb, err := c.Call(WithScope(ctx, scope), "view_event", inb, 0)
	if err != nil {
		return nil, err
	}
	var s wire.Surface
	if err := json.Unmarshal(outb, &s); err != nil {
		return nil, &CallError{Code: CodePluginError, Message: "view_event returned malformed JSON"}
	}
	SanitizeSurface(&s)
	if err := CheckOpen(p, &s); err != nil {
		return nil, err
	}
	return &s, nil
}

type actorIPKey struct{}

// WithActorIP carries the address a signed-in person's request came from
// into a view call, where it becomes `context.actor.ip` (wire.Actor.IP). The
// handler knows the request (and the trusted-proxy rule that reads it); the
// registry does not, and should not have to.
func WithActorIP(ctx context.Context, ip string) context.Context {
	if ip == "" {
		return ctx
	}
	return context.WithValue(ctx, actorIPKey{}, ip)
}

func actorIPFrom(ctx context.Context) string {
	ip, _ := ctx.Value(actorIPKey{}).(string)
	return ip
}

// storageName is the adapter name a scope qualifies its paths with.
func (r *Registry) storageFacts(ctx context.Context, storageID int64) (name string, readOnly bool) {
	if storageID <= 0 {
		return "", false
	}
	st, err := r.opts.Store.GetStorage(ctx, storageID)
	if err != nil || st == nil {
		return "", false
	}
	return st.Name, st.ReadOnly
}
