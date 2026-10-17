package handlers

// The AI assistant's tools (task #269) are filex's MCP tools - the very ones
// an agent reaches at /api/ai/mcp - called in process, over an in-memory
// MCP session whose context is the person's own request: their account,
// their session or their own key with its verbs and `root:`, their tenant,
// their address. Nothing here mints a token or widens a right: a tool refuses
// whatever the person could not do (acl.Resolver, files.download, the tenant
// boundary, the root), exactly as it refuses an agent.
//
// What this file adds on top, for a model that is not the person:
//
//   - a fixed list of the tools it is offered (assistantToolClass), with what
//     each is: a read, a change that can be undone, or an action that cannot
//     (a public link, a permission, a job an app runs) and so waits for the
//     person's approval;
//   - encrypted folders, encrypted files and the vault are closed (guard):
//     a call that names one is refused before the tool runs, and a row inside
//     one is taken out of every answer - their content never reaches a model;
//   - an undo for every change, worked out before it runs (assess) and run
//     as the person when they ask (Assistant.Undo);
//   - an audit row for every file content that left for a model: who, which
//     file, which provider and model (assistant.content_sent);
//   - answers cut to a size a model can take, and a file's bytes never sent
//     as base64.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/brf-tech/filex/backend/internal/assistant"
	"github.com/brf-tech/filex/backend/internal/auth"
	"github.com/brf-tech/filex/backend/internal/clientip"
	"github.com/brf-tech/filex/backend/internal/confine"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/e2e"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/pathkey"
	"github.com/brf-tech/filex/backend/internal/trash"
	"github.com/brf-tech/filex/backend/internal/version"
)

// The tools' classes.
const (
	toolRead    = "read"
	toolChange  = "change"
	toolConfirm = "confirm"
)

// assistantToolClass is every tool the assistant is offered, and what it is.
// A tool not listed is not offered: the upload ticket (a credential-free
// address for bytes on somebody's own disk), the bell, every admin_* tool.
//
// ⚠ A new tool joins by a line here, and a change tool by a case in assess
// that says how it is undone; one without is done at once with no undo link,
// so a tool that cannot be undone must be toolConfirm.
var assistantToolClass = map[string]string{
	"file_root":             toolRead,
	"file_list":             toolRead,
	"file_info":             toolRead,
	"file_read":             toolRead,
	"file_search":           toolRead,
	"file_versions":         toolRead,
	"trash_list":            toolRead,
	"share_list":            toolRead,
	"ops_list":              toolRead,
	"op_get":                toolRead,
	"app_actions":           toolRead,
	"file_comments":         toolRead,
	"file_permissions":      toolRead,
	"file_permission_users": toolRead,

	"file_tags":            toolChange, // a read without `set`
	"file_move":            toolChange,
	"file_mkdir":           toolChange,
	"file_write":           toolChange,
	"file_delete":          toolChange, // permanent on a storage without trash: waits
	"file_copy":            toolChange,
	"file_zip":             toolChange,
	"file_unzip":           toolChange,
	"archive_create":       toolChange,
	"archive_extract":      toolChange,
	"file_star":            toolChange,
	"file_comment_add":     toolChange,
	"file_snapshot":        toolChange,
	"trash_restore":        toolChange,
	"file_version_restore": toolChange,

	"file_share":             toolConfirm,
	"file_unshare":           toolConfirm,
	"file_request_create":    toolConfirm,
	"file_permission_set":    toolConfirm,
	"file_permission_revoke": toolConfirm,
	"file_comment_delete":    toolConfirm,
	"op_cancel":              toolConfirm,
	"app_run":                toolConfirm,
	"file_convert":           toolConfirm,
}

// assistantHiddenArgs are arguments the assistant's tools are offered
// without, and that are dropped when a model sends them anyway: a password
// (it would sit in the stored conversation and travel to the model), and
// the consent to write plaintext into an encrypted folder (the guard refuses
// those folders whatever is said).
var assistantHiddenArgs = []string{"password", "encrypt_filenames", "allow_plaintext"}

// assistantPathArgs and assistantPathListArgs are the arguments that name a
// place: what the guard holds to the person's root and keeps out of
// encrypted folders.
var (
	assistantPathArgs     = []string{"path", "src", "dst", "dest", "dest_dir", "under", "not_under"}
	assistantPathListArgs = []string{"sources", "paths"}
)

// assistantMaxResult is the most of one tool's answer a model is sent.
const assistantMaxResult = 48 << 10

// assistantMaxFileText is the most of one file's text file_read hands a
// model; the rest is said to be there.
const assistantMaxFileText = 32 << 10

// ── the call's context ───────────────────────────────────────────────────────

type assistantCallKey struct{}

// assistantCallInfo marks a context as the assistant's: a tool's audit row
// says `via: assistant` and names the provider and the model (ai_mcp.go
// mcpAuditRow).
type assistantCallInfo struct {
	provider, model string
	conversation    int64
}

func withAssistantCall(ctx context.Context, c *assistantCallInfo) context.Context {
	return context.WithValue(ctx, assistantCallKey{}, c)
}

func assistantCallFrom(ctx context.Context) *assistantCallInfo {
	c, _ := ctx.Value(assistantCallKey{}).(*assistantCallInfo)
	return c
}

// ── the toolbox ──────────────────────────────────────────────────────────────

// assistantToolbox runs one request's tool calls (assistant.Toolbox).
type assistantToolbox struct {
	store   db.Store
	session *mcp.ClientSession
	ops     *aiOps
	defs    []assistant.ToolDef
	offered map[string]string
	conv    *assistantConv
	lang    string
	// confirmAll: the person asked to approve every change too.
	confirmAll bool
	// root is the caller's confinement (a key's `root:`, narrowed by
	// X-Filex-Root): every path a call names stays inside it.
	root     confine.Root
	confined bool
	call     *assistantCallInfo
	user     *model.User
	emit     func(event string, v any)
	now      func() time.Time
}

// openAssistantTools connects an in-memory MCP session to the file tools, as
// the person on ctx, and returns the toolbox and its closer.
func openAssistantTools(ctx context.Context, m *AIMCP, store db.Store) (*assistantToolbox, func(), error) {
	if m == nil {
		return nil, func() {}, errors.New("the tool surface is not wired")
	}
	srv, ops := m.fileServer()
	if tok := auth.TokenFrom(ctx); tok != nil {
		// The person's own key: the verbs it holds are what it may do here
		// too, as on /api/ai/mcp.
		withdrawUngrantedFileTools(srv, tok)
	}
	st, ct := mcp.NewInMemoryTransports()
	ss, err := srv.Connect(ctx, st, nil)
	if err != nil {
		return nil, func() {}, err
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "filex-assistant", Version: version.String()}, nil)
	cs, err := client.Connect(ctx, ct, nil)
	if err != nil {
		_ = ss.Close()
		return nil, func() {}, err
	}
	closer := func() {
		_ = cs.Close()
		_ = ss.Wait()
	}
	tb := &assistantToolbox{store: store, session: cs, ops: ops, offered: map[string]string{}, now: time.Now}
	tb.root, tb.confined = confine.CallerRoot(ctx)
	list, err := cs.ListTools(ctx, &mcp.ListToolsParams{})
	if err != nil {
		closer()
		return nil, func() {}, err
	}
	for _, t := range list.Tools {
		class, ok := assistantToolClass[t.Name]
		if !ok {
			continue
		}
		tb.offered[t.Name] = class
		tb.defs = append(tb.defs, assistant.ToolDef{Name: t.Name, Description: t.Description, Schema: assistantSchema(t.InputSchema)})
	}
	// One order, every time: a provider's cache (and Anthropic's thinking
	// blocks) are tied to the tools exactly as they were sent.
	sort.Slice(tb.defs, func(i, j int) bool { return tb.defs[i].Name < tb.defs[j].Name })
	return tb, closer, nil
}

// assistantSchema is a tool's input schema as a model is offered it: the
// hidden arguments taken out, and nothing a provider may refuse ($schema).
func assistantSchema(raw any) json.RawMessage {
	b, err := json.Marshal(raw)
	if err != nil {
		return json.RawMessage(`{"type":"object","properties":{}}`)
	}
	var m map[string]any
	if json.Unmarshal(b, &m) != nil || m == nil {
		return json.RawMessage(`{"type":"object","properties":{}}`)
	}
	delete(m, "$schema")
	delete(m, "$id")
	if props, ok := m["properties"].(map[string]any); ok {
		for _, k := range assistantHiddenArgs {
			delete(props, k)
		}
	}
	if req, ok := m["required"].([]any); ok {
		kept := req[:0]
		for _, r := range req {
			if s, _ := r.(string); !containsString(assistantHiddenArgs, s) {
				kept = append(kept, r)
			}
		}
		m["required"] = kept
	}
	if _, ok := m["type"]; !ok {
		m["type"] = "object"
	}
	out, _ := json.Marshal(m)
	return out
}

func (tb *assistantToolbox) Defs() []assistant.ToolDef { return tb.defs }

// stepFor is the step of call, made when it is new.
func (tb *assistantToolbox) stepFor(call assistant.ToolCall) *assistantStep {
	if s := tb.conv.step(call.ID); s != nil {
		return s
	}
	msg := len(tb.conv.Messages) - 1
	for i := len(tb.conv.Messages) - 1; i >= 0; i-- {
		if tb.conv.Messages[i].Role != assistant.RoleAssistant {
			continue
		}
		for _, c := range tb.conv.Messages[i].ToolCalls() {
			if c.ID == call.ID {
				msg = i
			}
		}
		break
	}
	s := &assistantStep{ID: len(tb.conv.Steps) + 1, CallID: call.ID, Tool: call.Name, Msg: msg, At: tb.now().UnixMilli()}
	tb.conv.Steps = append(tb.conv.Steps, s)
	return s
}

func (tb *assistantToolbox) send(event string, s *assistantStep) {
	if tb.emit != nil {
		tb.emit(event, s.view(tb.lang))
	}
}

// fail ends a step that did not run, telling the model text.
func (tb *assistantToolbox) fail(s *assistantStep, text string) assistant.ToolOutcome {
	s.Status, s.Error = stepFailed, firstLine(text)
	tb.send("step", s)
	return assistant.ToolOutcome{Text: text, IsError: true}
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if utf8.RuneCountInString(s) > 300 {
		s = string([]rune(s)[:300]) + "…"
	}
	return s
}

// Run runs one call the model made (assistant.Toolbox).
func (tb *assistantToolbox) Run(ctx context.Context, call assistant.ToolCall) assistant.ToolOutcome {
	s := tb.stepFor(call)
	class, ok := tb.offered[call.Name]
	if !ok {
		return tb.fail(s, "UNKNOWN_TOOL: there is no tool named "+call.Name+" here.")
	}
	args := assistant.ArgsObject(call.Args)
	if args == nil {
		return tb.fail(s, "INVALID_JSON: the arguments are not a JSON object - nothing was done. Send them again as one.")
	}
	for _, k := range assistantHiddenArgs {
		delete(args, k)
	}
	if call.Name == "file_tags" {
		if _, set := args["set"]; !set {
			class = toolRead
		}
	}
	s.Class = class
	s.Target, s.To = assistantTargets(call.Name, args)
	if refusal := tb.guard(ctx, args); refusal != "" {
		return tb.fail(s, refusal)
	}
	var plan assistantPlan
	if class == toolChange {
		plan = tb.assess(ctx, call.Name, args)
	}
	why := plan.wait
	if why == "" && class == toolChange && tb.confirmAll {
		why = "confirm_all"
	}
	if class == toolConfirm && why == "" {
		why = "irreversible"
	}
	if why != "" {
		switch call.Decision {
		case assistant.DecisionApprove:
		case assistant.DecisionDeny:
			s.Status = stepDenied
			tb.send("step", s)
			return assistant.ToolOutcome{Text: "DENIED: the person declined this action, so nothing was done. Do not try it again unless they ask.", IsError: true}
		default:
			s.Status, s.Why, s.Args = stepWaiting, why, assistantArgsShown(args)
			tb.send("approval", s)
			return assistant.ToolOutcome{Pending: true}
		}
	}
	if plan.before != nil {
		if err := plan.before(ctx); err != nil && call.Decision != assistant.DecisionApprove {
			// The change cannot be undone after all (a version of what it
			// replaces could not be kept): it waits for the person.
			s.Status, s.Why, s.Args = stepWaiting, plan.beforeWhy, assistantArgsShown(args)
			tb.send("approval", s)
			return assistant.ToolOutcome{Pending: true}
		}
	}
	s.Status = stepRunning
	tb.send("step", s)
	out, text, isErr, err := tb.callTool(ctx, call.Name, args)
	if err != nil {
		return tb.fail(s, "TOOL_FAILED: "+err.Error())
	}
	if isErr {
		return tb.fail(s, text)
	}
	text = tb.shape(ctx, call.Name, out, text)
	if plan.undo != nil {
		s.Undo = plan.undo(out)
	}
	s.Status = stepDone
	tb.send("step", s)
	return assistant.ToolOutcome{Text: text}
}

// callTool calls a tool over the session: its structured answer (nil when it
// has none), its text, and whether the tool refused.
func (tb *assistantToolbox) callTool(ctx context.Context, name string, args map[string]any) (map[string]any, string, bool, error) {
	res, err := tb.session.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		return nil, "", true, err
	}
	var text []string
	for _, c := range res.Content {
		if t, ok := c.(*mcp.TextContent); ok {
			text = append(text, t.Text)
		}
	}
	var out map[string]any
	if res.StructuredContent != nil {
		if b, merr := json.Marshal(res.StructuredContent); merr == nil {
			_ = json.Unmarshal(b, &out)
		}
	}
	return out, strings.Join(text, "\n"), res.IsError, nil
}

// ── what stays out ───────────────────────────────────────────────────────────

// guard holds every place a call names to the person's root (a bare path
// becomes the qualified one under it) and refuses one inside an encrypted
// folder, an encrypted file or the vault. "" lets the call through.
func (tb *assistantToolbox) guard(ctx context.Context, args map[string]any) string {
	check := func(p string) (string, string) {
		p = strings.TrimSpace(p)
		if tb.confined {
			np, err := tb.root.EnforcePath(p)
			if err != nil {
				return "", "OUTSIDE_ROOT: " + p + " is outside the folder this explorer shows (" + tb.root.String() + "). Use a path inside it."
			}
			p = np
		}
		if p == "" {
			return p, ""
		}
		if tb.encrypted(ctx, p) {
			return "", "ENCRYPTED: " + p + " is an encrypted folder, inside one, an encrypted file or in the vault. The assistant never works there and its content never reaches you. Tell the person to open it in the explorer themselves."
		}
		return p, ""
	}
	for _, k := range assistantPathArgs {
		v, ok := args[k].(string)
		if !ok {
			continue
		}
		np, refusal := check(v)
		if refusal != "" {
			return refusal
		}
		args[k] = np
	}
	for _, k := range assistantPathListArgs {
		list, ok := args[k].([]any)
		if !ok {
			continue
		}
		for i, item := range list {
			v, ok := item.(string)
			if !ok {
				continue
			}
			np, refusal := check(v)
			if refusal != "" {
				return refusal
			}
			list[i] = np
		}
	}
	return ""
}

// encrypted reports whether p is, or is inside, something whose content the
// server holds as ciphertext: an encrypted folder (a vault is one), a file in
// one, a single encrypted file (.fxe). The rows' own rule (encryptedAtDoor,
// e2e.IsRoot) - no content is read to answer it.
func (tb *assistantToolbox) encrypted(ctx context.Context, p string) bool {
	s, rel, err := tb.ops.resolveStorage(ctx, p)
	if err != nil || rel == "" {
		return false
	}
	if encryptedAtDoor(ctx, tb.store, s.ID, rel) != "" {
		return true
	}
	if lk, ok := tb.store.(e2e.NodeByPathLookup); ok && e2e.IsRoot(ctx, lk, s.ID, strings.Trim(rel, "/")) {
		return true
	}
	return false
}

// visible reports whether a row of an answer may reach a model: not a row
// inside an encrypted folder or the vault, not one outside the root.
func (tb *assistantToolbox) visible(row map[string]any) bool {
	p, _ := row["path"].(string)
	if root, _ := row["e2e_root"].(string); root != "" && p != root {
		return false
	}
	if enc, _ := row["encrypted"].(string); enc == encryptedInFolder || enc == encryptedInVault {
		return false
	}
	if tb.confined && strings.Contains(p, "://") {
		if _, err := tb.root.EnforcePath(p); err != nil {
			return false
		}
	}
	return true
}

// scrub takes every row visible refuses out of v, at any depth.
func (tb *assistantToolbox) scrub(v any) any {
	switch t := v.(type) {
	case map[string]any:
		for k, x := range t {
			t[k] = tb.scrub(x)
		}
		return t
	case []any:
		kept := t[:0]
		for _, x := range t {
			if row, ok := x.(map[string]any); ok && !tb.visible(row) {
				continue
			}
			kept = append(kept, tb.scrub(x))
		}
		return kept
	}
	return v
}

// shape is what the model is told of an answer: scrubbed, a file's text cut
// to size and its bytes never as base64, the whole cut to size - and the
// audit rows of what left for the model.
func (tb *assistantToolbox) shape(ctx context.Context, tool string, out map[string]any, text string) string {
	if out == nil {
		return clipText(text, assistantMaxResult)
	}
	out = tb.scrub(out).(map[string]any)
	switch tool {
	case "file_read":
		p, _ := out["path"].(string)
		if enc, _ := out["encoding"].(string); enc == "base64" {
			out["content"] = ""
			out["note"] = "This file is not text; its bytes are not sent to you. Describe it from its name, type and size, or offer a conversion."
			break
		}
		content, _ := out["content"].(string)
		if len(content) > assistantMaxFileText {
			out["content"] = clipText(content, assistantMaxFileText)
			out["note"] = fmt.Sprintf("Only the first %d KiB of %d bytes are here.", assistantMaxFileText>>10, len(content))
		}
		sent, _ := out["content"].(string)
		tb.auditContent(ctx, tool, []string{p}, len(sent))
	case "file_search":
		var withText []string
		if entries, ok := out["entries"].([]any); ok {
			for _, e := range entries {
				if row, ok := e.(map[string]any); ok {
					if snip, _ := row["snippet"].(string); snip != "" {
						p, _ := row["path"].(string)
						withText = append(withText, p)
					}
				}
			}
		}
		if len(withText) > 0 {
			tb.auditContent(ctx, tool, withText, 0)
		}
	}
	b, err := json.Marshal(out)
	if err != nil {
		return clipText(text, assistantMaxResult)
	}
	return clipText(string(b), assistantMaxResult)
}

// clipText cuts s to at most n bytes on a character boundary, saying so.
func clipText(s string, n int) string {
	if len(s) <= n {
		return s
	}
	cut := n
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + "\n[cut: the rest is not shown]"
}

// AuditActionAssistantContentSent is the row of file content that left for
// a model: who, which files, which provider and model.
const AuditActionAssistantContentSent = "assistant.content_sent"

// auditContent writes the row of file content sent to the model.
func (tb *assistantToolbox) auditContent(ctx context.Context, tool string, paths []string, bytes int) {
	if tb.store == nil || len(paths) == 0 {
		return
	}
	if len(paths) > 50 {
		paths = paths[:50]
	}
	meta := map[string]interface{}{"via": auth.ViaAssistant, "tool": tool, "paths": paths}
	if tb.call != nil {
		meta["ai_provider"], meta["ai_model"] = tb.call.provider, tb.call.model
		meta["conversation_id"] = tb.call.conversation
	}
	if bytes > 0 {
		meta["bytes"] = bytes
	}
	entry := &model.AuditEntry{
		Action:     AuditActionAssistantContentSent,
		TargetType: "node",
		TargetID:   paths[0],
		IP:         clientip.FromContext(ctx),
		CreatedAt:  tb.now(),
		Metadata:   auth.StampTokenDoor(ctx, meta, auth.ViaAssistant),
	}
	if tb.user != nil {
		uid := tb.user.ID
		entry.UserID = &uid
	}
	wctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	_ = tb.store.InsertAuditEntry(wctx, entry)
}

// ── changes and their undo ───────────────────────────────────────────────────

// assistantPlan is what a change needs: whether it must wait for the person
// (wait: a key under server.assistant.why.), what to do right before it
// (before - keeping a version), and how to undo it once it ran.
type assistantPlan struct {
	wait      string
	before    func(ctx context.Context) error
	beforeWhy string
	undo      func(out map[string]any) []assistantUndoCall
}

// str is a string argument.
func str(args map[string]any, k string) string {
	v, _ := args[k].(string)
	return v
}

// dig is the value at keys inside v ("entry", "path").
func dig(v any, keys ...string) any {
	for _, k := range keys {
		m, ok := v.(map[string]any)
		if !ok {
			return nil
		}
		v = m[k]
	}
	return v
}

// digStr is dig for a string.
func digStr(v any, keys ...string) string {
	s, _ := dig(v, keys...).(string)
	return s
}

// digID is dig for a number id (JSON numbers are float64).
func digID(v any, keys ...string) int64 {
	switch n := dig(v, keys...).(type) {
	case float64:
		return int64(n)
	case int64:
		return n
	}
	return 0
}

func undoCall(tool string, args map[string]any) []assistantUndoCall {
	return []assistantUndoCall{{Tool: tool, Args: args}}
}

// trashed is the undo of a change that made p: it goes to the trash.
func trashed(p string) []assistantUndoCall {
	if p == "" {
		return nil
	}
	return undoCall("file_delete", map[string]any{"path": p})
}

// exists reports whether p is there for the person.
func (tb *assistantToolbox) exists(ctx context.Context, p string) bool {
	_, err := tb.ops.Info(ctx, p)
	return err == nil
}

// keepVersion is a change's `before` that keeps what it replaces as a
// version, remembering the version's id for the undo.
func (tb *assistantToolbox) keepVersion(p string, id *int64) func(ctx context.Context) error {
	return func(ctx context.Context) error {
		out, text, isErr, err := tb.callTool(ctx, "file_snapshot", map[string]any{"path": p})
		if err != nil {
			return err
		}
		if isErr {
			return errors.New(text)
		}
		if *id = digID(out, "result", "version", "id"); *id == 0 {
			return errors.New("no version id in the snapshot's answer")
		}
		return nil
	}
}

// assess is a change's plan, from what is there now.
func (tb *assistantToolbox) assess(ctx context.Context, tool string, args map[string]any) assistantPlan {
	switch tool {
	case "file_move":
		src := str(args, "src")
		return assistantPlan{undo: func(out map[string]any) []assistantUndoCall {
			now := digStr(out, "entry", "path")
			if now == "" {
				return nil
			}
			if kept, _ := dig(out, "entry", "source_kept").(bool); kept {
				// A folder moved across storages that left things behind:
				// copied, its source kept - the undo takes the copy away.
				return trashed(now)
			}
			return undoCall("file_move", map[string]any{"src": now, "dst": src})
		}}
	case "file_mkdir":
		if tb.exists(ctx, str(args, "path")) {
			return assistantPlan{}
		}
		return assistantPlan{undo: func(out map[string]any) []assistantUndoCall { return trashed(digStr(out, "entry", "path")) }}
	case "file_write":
		p := str(args, "path")
		if !tb.exists(ctx, p) {
			return assistantPlan{undo: func(out map[string]any) []assistantUndoCall { return trashed(digStr(out, "entry", "path")) }}
		}
		if !tb.ops.versionsOn() {
			return assistantPlan{wait: "overwrite"}
		}
		var id int64
		return assistantPlan{before: tb.keepVersion(p, &id), beforeWhy: "overwrite", undo: func(map[string]any) []assistantUndoCall {
			return undoCall("file_version_restore", map[string]any{"path": p, "version_id": id})
		}}
	case "file_version_restore":
		p := str(args, "path")
		if !tb.ops.versionsOn() {
			return assistantPlan{}
		}
		var id int64
		return assistantPlan{before: tb.keepVersion(p, &id), beforeWhy: "overwrite", undo: func(map[string]any) []assistantUndoCall {
			return undoCall("file_version_restore", map[string]any{"path": p, "version_id": id})
		}}
	case "file_delete":
		return tb.assessDelete(ctx, str(args, "path"))
	case "file_copy":
		dst := str(args, "dst")
		if tb.exists(ctx, dst) {
			// The copy lands beside what is there, under a name the queue
			// picks: never on top of it, so nothing to undo but nothing lost.
			return assistantPlan{}
		}
		return assistantPlan{undo: func(map[string]any) []assistantUndoCall { return trashed(dst) }}
	case "file_zip":
		if tb.exists(ctx, str(args, "dest")) {
			return assistantPlan{wait: "dest_exists"}
		}
		return assistantPlan{undo: func(out map[string]any) []assistantUndoCall { return trashed(digStr(out, "entry", "path")) }}
	case "archive_create":
		dest := str(args, "dest")
		return assistantPlan{undo: func(map[string]any) []assistantUndoCall { return trashed(dest) }}
	case "file_unzip", "archive_extract":
		dest := str(args, "dest_dir")
		if tool == "archive_extract" {
			dest = str(args, "dest")
			if dest == "" {
				dest = aiParentPath(str(args, "path"))
			}
		}
		if dest == "" || tb.exists(ctx, dest) {
			// Extracting into a folder that is there can put files on top of
			// others: that waits.
			return assistantPlan{wait: "dest_exists"}
		}
		return assistantPlan{undo: func(map[string]any) []assistantUndoCall { return trashed(dest) }}
	case "file_tags":
		p := str(args, "path")
		prev, _, isErr, err := tb.callTool(ctx, "file_tags", map[string]any{"path": p})
		if err != nil || isErr {
			return assistantPlan{}
		}
		var set []map[string]any
		if tags, ok := prev["tags"].([]any); ok {
			for _, t := range tags {
				set = append(set, map[string]any{"name": digStr(t, "name"), "kind": digStr(t, "kind")})
			}
		}
		if set == nil {
			set = []map[string]any{}
		}
		return assistantPlan{undo: func(map[string]any) []assistantUndoCall {
			return undoCall("file_tags", map[string]any{"path": p, "set": set})
		}}
	case "file_star":
		star := true
		if v, ok := args["star"].(bool); ok {
			star = v
		}
		p := str(args, "path")
		return assistantPlan{undo: func(map[string]any) []assistantUndoCall {
			return undoCall("file_star", map[string]any{"path": p, "star": !star})
		}}
	case "file_comment_add":
		return assistantPlan{undo: func(out map[string]any) []assistantUndoCall {
			id := digID(out, "result", "comment", "id")
			if id == 0 {
				id = digID(out, "result", "id")
			}
			if id == 0 {
				return nil
			}
			return undoCall("file_comment_delete", map[string]any{"id": id})
		}}
	}
	// file_snapshot (a version more is nothing to undo), trash_restore (the
	// way back is deleting it again, from the explorer).
	return assistantPlan{}
}

// assessDelete is a delete's plan: to the trash, undone from it - or, on a
// storage without one, permanent, and so it waits.
func (tb *assistantToolbox) assessDelete(ctx context.Context, p string) assistantPlan {
	s, rel, err := tb.ops.resolveStorage(ctx, p)
	if err != nil || rel == "" {
		return assistantPlan{}
	}
	drv, err := tb.ops.resolver(s.ID)
	if err != nil {
		return assistantPlan{}
	}
	if !trash.Supported(drv) {
		return assistantPlan{wait: "permanent"}
	}
	node, _ := tb.store.GetNodeByPath(ctx, s.ID, pathkey.Hash(s.ID, rel))
	if node == nil {
		return assistantPlan{}
	}
	id := node.ID
	return assistantPlan{undo: func(map[string]any) []assistantUndoCall {
		return undoCall("trash_restore", map[string]any{"node_ids": []any{id}})
	}}
}

// aiParentPath is the folder holding an adapter path ("store://a/b" →
// "store://a").
func aiParentPath(p string) string {
	adapter, rel := splitAdapterPath(p)
	dir := path.Dir("/" + strings.Trim(rel, "/"))
	if dir == "/" || dir == "." {
		dir = ""
	}
	return joinAdapterPath(adapter, strings.TrimPrefix(dir, "/"))
}

// assistantTargets is what a step is about, as the panel shows it: the place
// a call names first, and where it goes.
func assistantTargets(tool string, args map[string]any) (target, to string) {
	for _, k := range []string{"path", "src"} {
		if v := str(args, k); v != "" {
			target = v
			break
		}
	}
	if target == "" {
		if list, ok := args["sources"].([]any); ok && len(list) > 0 {
			target, _ = list[0].(string)
		} else if list, ok := args["paths"].([]any); ok && len(list) > 0 {
			target, _ = list[0].(string)
		}
	}
	for _, k := range []string{"dst", "dest", "dest_dir"} {
		if v := str(args, k); v != "" {
			to = v
			break
		}
	}
	if target == "" {
		switch {
		case args["id"] != nil:
			target = fmt.Sprint(args["id"])
		case args["token"] != nil:
			target = fmt.Sprint(args["token"])
		case args["q"] != nil:
			target = fmt.Sprint(args["q"])
		case args["query"] != nil:
			target = fmt.Sprint(args["query"])
		}
	}
	return target, to
}

// assistantArgsShown are a call's arguments as an approval card shows them.
func assistantArgsShown(args map[string]any) map[string]any {
	out := make(map[string]any, len(args))
	for k, v := range args {
		if containsString(assistantHiddenArgs, k) {
			continue
		}
		out[k] = v
	}
	return out
}

// runUndo runs a step's undo as the person: every call, in order; the first
// refusal ends it.
func (tb *assistantToolbox) runUndo(ctx context.Context, s *assistantStep) error {
	for _, c := range s.Undo {
		args := map[string]any{}
		for k, v := range c.Args {
			args[k] = v
		}
		_, text, isErr, err := tb.callTool(ctx, c.Tool, args)
		if err != nil {
			return err
		}
		if isErr {
			return errors.New(text)
		}
	}
	return nil
}
