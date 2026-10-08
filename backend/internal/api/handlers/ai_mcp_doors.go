package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/brf-tech/filex/backend/internal/auth"
)

// The MCP tools of the explorer's operations (ai_doors.go): copy, apps, the
// operations queue, trash, version history, archives, links. Each runs the
// same aiOps method its REST twin under /api/ai runs (routes.go), and answers
// what that route answers.

// mcpDoorOut is what a door tool answers: the HTTP status of its REST twin and
// that route's JSON answer.
//
// ⚠ Result is `any` holding a json.RawMessage, as adminOut's is: the SDK
// derives the output schema from this type and a json.RawMessage field reads
// as "null or an array", so every object answer would be refused as invalid
// output although the operation had run (ai_admin.go adminOut, 2026-10-01).
type mcpDoorOut struct {
	Status int `json:"status"`
	Result any `json:"result" jsonschema:"the operation's JSON answer - the body its REST twin under /api/ai answers (an object)"`
}

// doorWordCode matches a handler's one-word error ("permission_denied",
// "not_found", "app_plugins_disabled"): that word IS its code.
var doorWordCode = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

// doorErrorText is a refused door's error text, in the AI surface's form: the
// code first (`READ_ONLY: …`, `PERMISSION_DENIED: …`), as toolErr leads with
// it, then the message and the HTTP status of the REST twin. The code is the
// answer's `code`, or its `error` when that is one word.
func doorErrorText(ans doorAnswer) string {
	var m map[string]any
	if json.Unmarshal(ans.body, &m) != nil {
		return fmt.Sprintf("%s (HTTP %d)", strings.TrimSpace(string(ans.body)), ans.status)
	}
	str := func(k string) string { s, _ := m[k].(string); return s }
	code, errText, message := str("code"), str("error"), str("message")
	msg := errText
	if code == "" && doorWordCode.MatchString(errText) {
		code, msg = errText, ""
	}
	if message != "" {
		if msg != "" {
			msg += ": "
		}
		msg += message
	}
	text := strings.ToUpper(code)
	switch {
	case text == "" && msg == "":
		text = strings.TrimSpace(string(ans.body))
	case text == "":
		text = msg
	case msg != "":
		text += ": " + msg
	}
	return fmt.Sprintf("%s (HTTP %d)", text, ans.status)
}

// regDoorTool registers one door tool. A tool that changes something
// (mcpWriteTwin names its REST twin) opens the audit recorder before the
// handler runs, so what the handler says about the write lands in the ONE row
// written after it succeeds - the row its REST twin's AuditMiddleware writes.
// id names the twin route's {id} (op_cancel), nil elsewhere.
func regDoorTool[In any](srv *mcp.Server, ops *aiOps, name, desc string, call func(context.Context, In) (doorAnswer, error), id func(In) string) {
	_, writes := mcpWriteTwin[name]
	mcp.AddTool(srv, &mcp.Tool{Name: name, Description: desc},
		func(ctx context.Context, _ *mcp.CallToolRequest, in In) (*mcp.CallToolResult, mcpDoorOut, error) {
			var detail *auth.AuditDetail
			if writes {
				ctx, detail = auth.WithAuditDetail(ctx)
			}
			ans, err := call(ctx, in)
			if err != nil {
				return toolErr[mcpDoorOut](err)
			}
			body := json.RawMessage(ans.body)
			if len(ans.body) == 0 {
				body = json.RawMessage("null")
			}
			out := mcpDoorOut{Status: ans.status, Result: body}
			if ans.status >= 400 {
				return &mcp.CallToolResult{
					IsError: true,
					Content: []mcp.Content{&mcp.TextContent{Text: doorErrorText(ans)}},
				}, out, nil
			}
			if writes {
				rowID := ""
				if id != nil {
					rowID = id(in)
				}
				mcpAuditRow(ctx, ops.store, name, rowID, detail)
			}
			return nil, out, nil
		})
}

// ── inputs ───────────────────────────────────────────────────────────────────

type mcpCopyIn struct {
	Src string `json:"src" jsonschema:"adapter:// path of the file or folder to copy"`
	Dst string `json:"dst" jsonschema:"adapter:// path the copy lands at (folder + new name), on this storage or another one"`
}

type mcpAppRunIn struct {
	Plugin string         `json:"plugin" jsonschema:"the app's name (the plugin field app_actions answers)"`
	Action string         `json:"action" jsonschema:"the action's id (the id field app_actions answers)"`
	Paths  []string       `json:"paths" jsonschema:"adapter:// paths of the files the action runs on (one storage)"`
	Params map[string]any `json:"params,omitempty" jsonschema:"the action's parameters. Omit it to get the action's form (surface) when it has one - its fields are what params takes; pass {} for an action without a form"`
}

type mcpConvertIn struct {
	Path   string `json:"path" jsonschema:"adapter:// path of the file to convert"`
	Target string `json:"target" jsonschema:"the format to convert to, as an extension: pdf, docx, xlsx, png, …"`
}

type mcpOpsListIn struct {
	Status string `json:"status,omitempty" jsonschema:"only operations in this state: pending, running, ok, partial, failed, cancelled (empty = the most recent of every state)"`
}

type mcpOpIDIn struct {
	ID int64 `json:"id" jsonschema:"the operation's id (op.id from the tool that queued it, or ops_list)"`
}

type mcpTrashListIn struct {
	Storage string `json:"storage,omitempty" jsonschema:"only this storage's trash (its adapter name); empty = every storage you can reach"`
	Limit   int    `json:"limit,omitempty" jsonschema:"page size (default 50, at most 500)"`
	Offset  int    `json:"offset,omitempty" jsonschema:"entries to skip (total says how many there are)"`
}

type mcpTrashRestoreIn struct {
	NodeIDs []int64 `json:"node_ids" jsonschema:"the trash entries to bring back: the id field of trash_list entries (at most 1000)"`
}

type mcpVersionRestoreIn struct {
	Path            string `json:"path" jsonschema:"adapter:// path of the file"`
	VersionID       int64  `json:"version_id" jsonschema:"the version to bring back (an id file_versions answers)"`
	SnapshotCurrent bool   `json:"snapshot_current,omitempty" jsonschema:"keep the current content as a version first (the server already does this when its overwrite guard is on, the default)"`
}

type mcpArchiveCreateIn struct {
	Sources        []string `json:"sources" jsonschema:"adapter:// paths of the files and folders to pack (folders recurse)"`
	Dest           string   `json:"dest" jsonschema:"adapter:// path of the archive to create; it must not exist yet"`
	Format         string   `json:"format,omitempty" jsonschema:"zip, 7z, tar, tar.gz, tar.bz2 or tar.xz (default: from dest's extension, else the server's default); the administrator may allow fewer"`
	Password       string   `json:"password,omitempty" jsonschema:"protect a zip or 7z with this password; it goes to the job in memory only and is never logged"`
	EncryptNames   bool     `json:"encrypt_filenames,omitempty" jsonschema:"7z with a password: hide the member names too"`
	Compression    *int     `json:"compression,omitempty" jsonschema:"compression level 0-9"`
	AllowPlaintext bool     `json:"allow_plaintext,omitempty" jsonschema:"only for a dest inside an end-to-end encrypted folder: store the archive there UNENCRYPTED on purpose"`
}

type mcpArchiveExtractIn struct {
	Path           string   `json:"path" jsonschema:"adapter:// path of the archive (zip, 7z, rar, tar, tar.gz, …)"`
	Dest           string   `json:"dest,omitempty" jsonschema:"adapter:// folder to extract into, on the archive's storage (default: the archive's own folder)"`
	Members        []string `json:"members,omitempty" jsonschema:"only these entries, by their name inside the archive (default: all)"`
	Password       string   `json:"password,omitempty" jsonschema:"the archive's password, if it has one; never logged"`
	AllowPlaintext bool     `json:"allow_plaintext,omitempty" jsonschema:"only when the destination is inside an end-to-end encrypted folder: extract there UNENCRYPTED on purpose"`
}

type mcpShareListIn struct {
	Active bool `json:"active,omitempty" jsonschema:"only links that still work (not revoked, not expired)"`
	Limit  int  `json:"limit,omitempty" jsonschema:"page size"`
	Offset int  `json:"offset,omitempty" jsonschema:"rows to skip"`
}

type mcpFileRequestIn struct {
	Path          string         `json:"path" jsonschema:"adapter:// path of the FOLDER the uploads land in"`
	MaxUploads    int            `json:"max_uploads,omitempty" jsonschema:"close the link after this many uploads (0 = no limit)"`
	DropSettings  map[string]any `json:"drop_settings,omitempty" jsonschema:"the link's upload limits, as the explorer's File request dialog sets them (SHARING.md)"`
	ExpiresInDays int            `json:"expires_in_days,omitempty" jsonschema:"the link expires after this many days (0 = the server's default)"`
	Pin           bool           `json:"pin,omitempty" jsonschema:"protect the link with a generated PIN (answered once)"`
}

// opsFollow is said in every tool that queues a job, so an agent learns how to
// follow one from the catalogue.
const opsFollow = " It answers 202 with the queued operation ({op: {id, …}}): follow it with op_get {id} until its status is ok (or partial, failed, cancelled), stop it with op_cancel."

// registerDoorTools wires the door tools onto srv. Registered for every
// server, like the file tools; withdrawUngrantedFileTools takes away the ones
// whose verb the token lacks (fileToolVerb).
func registerDoorTools(srv *mcp.Server, ops *aiOps) {
	regDoorTool(srv, ops, "file_copy",
		"Copy a file or folder to dst - within a storage or to another one - on the server, through the operations queue (the explorer's paste). dst is where the copy lands (its folder and its name), as for file_move. Never overwrites: a taken name lands beside it (rapor-copy.txt), so read the finished operation rather than assuming the name. Needs edit rights on the source and files.create in the destination folder. An encrypted file does not leave its encrypted folder (E2E_BOUNDARY)."+opsFollow,
		func(ctx context.Context, in mcpCopyIn) (doorAnswer, error) { return ops.Copy(ctx, in.Src, in.Dst) }, nil)

	regDoorTool(srv, ops, "app_actions",
		"List the app actions (installed apps: Convert, e-signature, …) that apply to the file or folder at path, as the explorer's right-click menu offers them to you: {path, actions: [{plugin, id, label, view, output_mode, min_role, …}]}. Start one with app_run. An action with `view` has a form: app_run without params answers the form's fields first.",
		func(ctx context.Context, in mcpPathIn) (doorAnswer, error) { return ops.AppActions(ctx, in.Path) }, nil)

	regDoorTool(srv, ops, "app_run",
		"Run an app action on files (plugin and action from app_actions) - the explorer's menu action, with its rules: your role and grants on each file, the app's own permissions, the actions an administrator restricted. Without params an action that has a form answers it ({surface: …}) instead of running; call again with params holding the fields' values. With params (an empty object for an action without a form) the job is queued."+opsFollow+" An action an app starts itself (the second half of its own flow) cannot be run from here.",
		func(ctx context.Context, in mcpAppRunIn) (doorAnswer, error) {
			return ops.AppRun(ctx, in.Plugin, in.Action, in.Paths, in.Params)
		}, nil)

	regDoorTool(srv, ops, "file_convert",
		"Convert a file to another format (target: pdf, docx, xlsx, png, …) on the server with the Convert app - the explorer's Convert… action. Needs the Convert app installed by an administrator (else NOT_FOUND). The converted file lands beside the original under a free name."+opsFollow,
		func(ctx context.Context, in mcpConvertIn) (doorAnswer, error) {
			return ops.Convert(ctx, in.Path, in.Target)
		}, nil)

	regDoorTool(srv, ops, "ops_list",
		"List your operations on the queue - copies, app jobs, archive jobs, trash restores - newest first: {ops: [{id, kind, status, total, done, failed, error, …}]}. Only the ones you queued (an administrator's unconfined key sees everybody's).",
		func(ctx context.Context, in mcpOpsListIn) (doorAnswer, error) { return ops.OpsList(ctx, in.Status) }, nil)

	regDoorTool(srv, ops, "op_get",
		"One operation's state: {id, kind, status (pending, running, ok, partial, failed, cancelled), total, done, failed, error, …}. Someone else's operation answers NOT FOUND, as one that never existed.",
		func(ctx context.Context, in mcpOpIDIn) (doorAnswer, error) { return ops.OpGet(ctx, in.ID) }, nil)

	regDoorTool(srv, ops, "op_cancel",
		"Stop one of your pending or running operations. FINISHED: it has already ended; NOT_CANCELLABLE: it is one that finishes what it starts (a rename, a restore, a permanent delete).",
		func(ctx context.Context, in mcpOpIDIn) (doorAnswer, error) { return ops.OpCancel(ctx, in.ID) },
		func(in mcpOpIDIn) string { return fmt.Sprint(in.ID) })

	regDoorTool(srv, ops, "trash_list",
		"List what you can bring back from the trash: {entries: [{id, path (where it was), name, size, deleted_at, deleted_by_self, …}], total, total_bytes, storages: [{storage_name, count, bytes, newest_deleted_at}], summary, limit, offset}. total and total_bytes count every entry, not the page; summary says it in your language. Only entries you could see where they were, inside your root.",
		func(ctx context.Context, in mcpTrashListIn) (doorAnswer, error) {
			return ops.TrashList(ctx, in.Storage, in.Limit, in.Offset)
		}, nil)

	regDoorTool(srv, ops, "trash_restore",
		"Bring trash entries back to where they were (node_ids = the id of trash_list entries, at most 1000). Every entry is checked first - files.create where it came from, app locks - and nothing is queued unless all pass. A place that is taken meanwhile is reported on the operation. Answers 202 {ops: […], done, summary} (one op per storage); follow them with op_get, whose summary says how each ended.",
		func(ctx context.Context, in mcpTrashRestoreIn) (doorAnswer, error) {
			return ops.TrashRestore(ctx, in.NodeIDs)
		}, nil)

	regDoorTool(srv, ops, "file_versions",
		"A file's version history: {versions: [{id, size, created_at, …}], node}. Restore one with file_version_restore.",
		func(ctx context.Context, in mcpPathIn) (doorAnswer, error) { return ops.Versions(ctx, in.Path) }, nil)

	regDoorTool(srv, ops, "file_version_restore",
		"Replace a file's content with one of its versions (version_id from file_versions). Needs edit rights (files.modify); the content it replaces is kept as a version first. A file an app has locked answers LOCKED.",
		func(ctx context.Context, in mcpVersionRestoreIn) (doorAnswer, error) {
			return ops.VersionRestore(ctx, in.Path, in.VersionID, in.SnapshotCurrent)
		}, nil)

	regDoorTool(srv, ops, "file_snapshot",
		"Keep a file's current content as a version now (edit rights). Writes normally keep one by themselves; this is the explicit one before a risky change.",
		func(ctx context.Context, in mcpPathIn) (doorAnswer, error) { return ops.Snapshot(ctx, in.Path) }, nil)

	regDoorTool(srv, ops, "archive_create",
		"Pack files and folders into a NEW archive on the server: zip, 7z or the TAR family (format, or from dest's extension), optionally with a password (zip, 7z). The bytes never travel over MCP - share dest to hand it out. Refuses an existing dest (TARGET_EXISTS). Needs files.download on every source. An encrypted file does not leave its encrypted folder inside an archive (E2E_BOUNDARY); into an encrypted folder only with allow_plaintext."+opsFollow,
		func(ctx context.Context, in mcpArchiveCreateIn) (doorAnswer, error) {
			return ops.ArchiveCreate(withPlaintextConsent(ctx, in.AllowPlaintext), aiArchiveCreate{
				Sources: in.Sources, Dest: in.Dest, Format: in.Format, Password: in.Password,
				EncryptNames: in.EncryptNames, Compression: in.Compression,
			})
		}, nil)

	regDoorTool(srv, ops, "archive_extract",
		"Extract an archive already in storage (zip, 7z, rar, tar, tar.gz, tar.bz2, tar.xz, …) into a folder on the server - its own folder unless dest says another - with password for a protected one (PASSWORD_REQUIRED when it needs one). members picks entries. Members under one of filex's own names are skipped; an encrypted archive cannot be opened (E2E_ENCRYPTED); into an encrypted folder only with allow_plaintext."+opsFollow,
		func(ctx context.Context, in mcpArchiveExtractIn) (doorAnswer, error) {
			return ops.ArchiveExtract(withPlaintextConsent(ctx, in.AllowPlaintext), aiArchiveExtract{
				Path: in.Path, Dest: in.Dest, Members: in.Members, Password: in.Password,
			})
		}, nil)

	regDoorTool(srv, ops, "share_list",
		"List the public links and file requests YOU made, newest first: {shares: [{token, url, kind (download | drop), node_path, expires_at, download_count, …}], total}. Revoke one with file_unshare {token}. Only links inside your root.",
		func(ctx context.Context, in mcpShareListIn) (doorAnswer, error) {
			return ops.SharesList(ctx, in.Active, in.Limit, in.Offset)
		}, nil)

	regDoorTool(srv, ops, "file_request_create",
		"Create a file request: a public UPLOAD link into a folder, for people without a filex account to send files in (the explorer's File request). Needs edit rights on the folder and the account's share.upload_links. Never into an encrypted folder (E2E_ENCRYPTED). Answers the link ({share: {url, token, …}}); the PIN, when asked for, is answered once.",
		func(ctx context.Context, in mcpFileRequestIn) (doorAnswer, error) {
			req := aiFileRequest{Path: in.Path, MaxUploads: in.MaxUploads, ExpiresInDays: in.ExpiresInDays, Pin: in.Pin}
			if len(in.DropSettings) > 0 {
				b, err := json.Marshal(in.DropSettings)
				if err != nil {
					return doorAnswer{}, err
				}
				req.DropSettings = b
			}
			return ops.FileRequest(ctx, req)
		}, nil)

	registerPeopleTools(srv, ops)
}
