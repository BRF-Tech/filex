package handlers

import (
	"context"
	"encoding/base64"
	"errors"
	"net/http"
	"unicode/utf8"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/brf-tech/filex/backend/internal/acl"
	"github.com/brf-tech/filex/backend/internal/auth"
	apitoken "github.com/brf-tech/filex/backend/internal/auth/drivers/apitoken"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/share"
	"github.com/brf-tech/filex/backend/internal/storage"
	"github.com/brf-tech/filex/backend/internal/version"
)

// AIMCP exposes filex as a Model Context Protocol server over streamable
// HTTP (JSON-RPC). The same aiOps core that backs the REST handler powers
// each MCP tool, so AI agents can drive filex directly while work.example.com's
// FilexClient uses the REST surface.
//
// Transport: streamable HTTP in stateless + JSON-response mode (one
// JSON-RPC request → one JSON response), which is what laravel/mcp's HTTP
// client speaks. Mounted at POST/GET /api/ai/mcp behind
// auth.APITokenMiddleware + RequireScope("mcp").
//
// Auth model: the route's middleware has already validated the AI token and
// stashed the principal on the request context by the time getServer runs.
// getServer reads that principal and builds a per-request server whose tools
// close over an aiOps bound to the store + resolver. If the principal is
// absent (should never happen behind the middleware) getServer returns nil
// and the SDK serves 400.
type AIMCP struct {
	store      db.Store
	resolver   func(int64) (storage.Driver, error)
	admin      *AIAdmin
	share      *share.Service
	publicURL  string
	convertURL string
	acl        *acl.Resolver
	handler    http.Handler
}

// AttachACL wires the RBAC resolver so every per-request MCP tool op is gated
// by the bound user's grants + role ceiling (same enforcement as the REST AI).
func (h *AIMCP) AttachACL(r *acl.Resolver) { h.acl = r }

// NewAIMCP builds the MCP HTTP handler. `admin` powers the admin_* tools,
// which are only registered for tokens carrying the `admin` scope; pass nil
// to disable the admin tool surface entirely. shareSvc + publicURL power the
// file_share / file_unshare tools; convertURL is surfaced via file_root.
func NewAIMCP(store db.Store, resolver func(int64) (storage.Driver, error), admin *AIAdmin, shareSvc *share.Service, publicURL, convertURL string) *AIMCP {
	h := &AIMCP{store: store, resolver: resolver, admin: admin, share: shareSvc, publicURL: publicURL, convertURL: convertURL}
	h.handler = mcp.NewStreamableHTTPHandler(h.getServer, &mcp.StreamableHTTPOptions{
		Stateless:    true,
		JSONResponse: true,
	})
	return h
}

// ServeHTTP delegates to the SDK's streamable handler.
func (h *AIMCP) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.handler.ServeHTTP(w, r)
}

// getServer constructs a fresh MCP server per request, with tools bound to
// the AI token's principal. Returning nil yields a 400 from the SDK.
func (h *AIMCP) getServer(r *http.Request) *mcp.Server {
	if auth.UserFrom(r.Context()) == nil {
		return nil
	}
	ops := newAIOps(h.store, h.resolver, h.share, h.publicURL, h.convertURL)
	ops.acl = h.acl
	srv := mcp.NewServer(&mcp.Implementation{
		Name:    "filex",
		Title:   "filex file manager",
		Version: version.String(),
	}, nil)
	registerFilexTools(srv, ops)

	// Admin tools are gated by the `admin` token scope (on top of the route's
	// `mcp` scope). A token without `admin` never sees admin_* in tools/list.
	if tok := auth.TokenFrom(r.Context()); h.admin != nil && tok != nil && tok.HasScope(apitoken.ScopeAdmin) {
		principal := h.admin.elevatedPrincipal(auth.UserFrom(r.Context()))
		registerAdminTools(srv, h.admin, principal)
	}
	return srv
}

// ───── tool input/output types ─────

type mcpListIn struct {
	Path string `json:"path,omitempty" jsonschema:"adapter://dir path to list; empty = first storage root"`
}
type mcpEntriesOut struct {
	Entries []aiEntry `json:"entries"`
}

// mcpRootIn is the (empty) input for file_root.
type mcpRootIn struct{}

type mcpReadIn struct {
	Path string `json:"path" jsonschema:"adapter://file path to read"`
}
type mcpReadOut struct {
	Path     string `json:"path"`
	Mime     string `json:"mime"`
	Encoding string `json:"encoding"` // "utf-8" | "base64"
	Content  string `json:"content"`
}

type mcpWriteIn struct {
	Path          string `json:"path" jsonschema:"adapter://file path to create or overwrite"`
	Content       string `json:"content,omitempty" jsonschema:"UTF-8 text content (use content_base64 for binary)"`
	ContentBase64 string `json:"content_base64,omitempty" jsonschema:"base64-encoded binary content"`
}
type mcpEntryOut struct {
	Entry *aiEntry `json:"entry"`
}

type mcpPathIn struct {
	Path string `json:"path" jsonschema:"adapter://path"`
}
type mcpOKOut struct {
	OK bool `json:"ok"`
}

type mcpMoveIn struct {
	Src string `json:"src" jsonschema:"source adapter://path"`
	Dst string `json:"dst" jsonschema:"destination adapter://path (same storage)"`
}

type mcpSearchIn struct {
	Path  string `json:"path,omitempty" jsonschema:"adapter:// scope for the search; empty = first storage"`
	Query string `json:"query" jsonschema:"substring to match against file/dir names"`
}

type mcpShareIn struct {
	Path          string `json:"path" jsonschema:"adapter://file-or-folder to share (folders download as a zip)"`
	Pin           bool   `json:"pin,omitempty" jsonschema:"generate a random PIN to protect the link"`
	ExpiresInDays int    `json:"expires_in_days,omitempty" jsonschema:"link expiry in days (0 = never)"`
	MaxDownloads  int    `json:"max_downloads,omitempty" jsonschema:"max downloads (0 = unlimited)"`
}

type mcpUnshareIn struct {
	Token string `json:"token" jsonschema:"the share token to revoke"`
}

type mcpZipIn struct {
	Sources []string `json:"sources" jsonschema:"adapter:// paths to pack (files and/or folders; folders are zipped recursively)"`
	Dest    string   `json:"dest" jsonschema:"adapter:// path of the .zip to create (same storage as the sources)"`
}

type mcpUnzipIn struct {
	Src     string `json:"src" jsonschema:"adapter:// path of the .zip to extract"`
	DestDir string `json:"dest_dir" jsonschema:"adapter:// directory to extract into (same storage as src)"`
}
type mcpUnzipOut struct {
	Extracted int `json:"extracted"` // number of files written
}

// registerFilexTools wires every MCP tool onto srv, bound to ops.
func registerFilexTools(srv *mcp.Server, ops *aiOps) {
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "file_root",
		Description: "Report your access scope FIRST: the confinement root you're locked to (if any) and the storage adapter names you can address. If confined, address files with bare relative paths (they resolve UNDER your root) or full adapter://root/... paths — never guess adapter names.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, _ mcpRootIn) (*mcp.CallToolResult, aiRootInfo, error) {
		return nil, ops.RootInfo(ctx), nil
	})

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "file_list",
		Description: "List files and folders in a directory. Path is adapter://dir (adapter = storage name); empty path lists the first storage's root.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in mcpListIn) (*mcp.CallToolResult, mcpEntriesOut, error) {
		entries, err := ops.List(ctx, in.Path)
		if err != nil {
			return toolErr[mcpEntriesOut](err)
		}
		return nil, mcpEntriesOut{Entries: entries}, nil
	})

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "file_info",
		Description: "Get metadata (size, mime, type, modified time) for a single file or folder.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in mcpPathIn) (*mcp.CallToolResult, mcpEntryOut, error) {
		e, err := ops.Info(ctx, in.Path)
		if err != nil {
			return toolErr[mcpEntryOut](err)
		}
		return nil, mcpEntryOut{Entry: e}, nil
	})

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "file_read",
		Description: "Read a file's contents. Returns UTF-8 text when the bytes are valid UTF-8, otherwise base64. Files above 8 MiB are rejected — use the REST download endpoint for those.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in mcpReadIn) (*mcp.CallToolResult, mcpReadOut, error) {
		data, mime, err := ops.ReadBytes(ctx, in.Path)
		if err != nil {
			return toolErr[mcpReadOut](err)
		}
		out := mcpReadOut{Path: in.Path, Mime: mime}
		if utf8.Valid(data) {
			out.Encoding = "utf-8"
			out.Content = string(data)
		} else {
			out.Encoding = "base64"
			out.Content = base64.StdEncoding.EncodeToString(data)
		}
		return nil, out, nil
	})

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "file_write",
		Description: "Create or overwrite a file. Provide UTF-8 text in `content`, or binary as base64 in `content_base64`.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in mcpWriteIn) (*mcp.CallToolResult, mcpEntryOut, error) {
		var data []byte
		if in.ContentBase64 != "" {
			b, derr := base64.StdEncoding.DecodeString(in.ContentBase64)
			if derr != nil {
				return toolErr[mcpEntryOut](errors.New("bad base64: " + derr.Error()))
			}
			data = b
		} else {
			data = []byte(in.Content)
		}
		e, err := ops.Write(ctx, in.Path, data)
		if err != nil {
			return toolErr[mcpEntryOut](err)
		}
		return nil, mcpEntryOut{Entry: e}, nil
	})

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "file_delete",
		Description: "Soft-delete a file or folder (moved to filex trash, recoverable from the UI).",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in mcpPathIn) (*mcp.CallToolResult, mcpOKOut, error) {
		if err := ops.Delete(ctx, in.Path); err != nil {
			return toolErr[mcpOKOut](err)
		}
		return nil, mcpOKOut{OK: true}, nil
	})

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "file_move",
		Description: "Move or rename a file/folder within the same storage.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in mcpMoveIn) (*mcp.CallToolResult, mcpEntryOut, error) {
		e, err := ops.Move(ctx, in.Src, in.Dst)
		if err != nil {
			return toolErr[mcpEntryOut](err)
		}
		return nil, mcpEntryOut{Entry: e}, nil
	})

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "file_mkdir",
		Description: "Create a directory at the given adapter://path.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in mcpPathIn) (*mcp.CallToolResult, mcpEntryOut, error) {
		e, err := ops.Mkdir(ctx, in.Path)
		if err != nil {
			return toolErr[mcpEntryOut](err)
		}
		return nil, mcpEntryOut{Entry: e}, nil
	})

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "file_search",
		Description: "Search file and folder names by substring within a storage.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in mcpSearchIn) (*mcp.CallToolResult, mcpEntriesOut, error) {
		entries, err := ops.Search(ctx, in.Path, in.Query)
		if err != nil {
			return toolErr[mcpEntriesOut](err)
		}
		return nil, mcpEntriesOut{Entries: entries}, nil
	})

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "file_share",
		Description: "Create a public share link for a file or folder (folders download as a ZIP). Returns the URL + a one-time PIN if pin=true. Use this to hand a file to someone without filex access — do NOT stream large files back through file_read.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in mcpShareIn) (*mcp.CallToolResult, aiShareResult, error) {
		res, err := ops.CreateShare(ctx, in.Path, in.Pin, in.ExpiresInDays, in.MaxDownloads)
		if err != nil {
			return toolErr[aiShareResult](err)
		}
		return nil, *res, nil
	})

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "file_unshare",
		Description: "Revoke a share link by its token (returned from file_share).",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in mcpUnshareIn) (*mcp.CallToolResult, mcpOKOut, error) {
		if err := ops.RevokeShare(ctx, in.Token); err != nil {
			return toolErr[mcpOKOut](err)
		}
		return nil, mcpOKOut{OK: true}, nil
	})

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "file_zip",
		Description: "Pack one or more files/folders into a .zip ON THE SERVER (folders recurse). The archive is written to storage at `dest` — the bytes never travel over MCP. To let someone download a big zip, call file_share on `dest`; do NOT file_read it.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in mcpZipIn) (*mcp.CallToolResult, mcpEntryOut, error) {
		e, err := ops.Zip(ctx, in.Sources, in.Dest)
		if err != nil {
			return toolErr[mcpEntryOut](err)
		}
		return nil, mcpEntryOut{Entry: e}, nil
	})

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "file_unzip",
		Description: "Extract a .zip already in storage into dest_dir ON THE SERVER (zip-slip protected; every entry stays within your confinement root). Returns the number of files written.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in mcpUnzipIn) (*mcp.CallToolResult, mcpUnzipOut, error) {
		n, err := ops.Unzip(ctx, in.Src, in.DestDir)
		if err != nil {
			return toolErr[mcpUnzipOut](err)
		}
		return nil, mcpUnzipOut{Extracted: n}, nil
	})
}

// toolErr packs an error into an MCP tool error result (IsError=true) rather
// than a protocol error, so the model sees a readable message and can retry.
func toolErr[T any](err error) (*mcp.CallToolResult, T, error) {
	var zero T
	return &mcp.CallToolResult{
		IsError: true,
		Content: []mcp.Content{&mcp.TextContent{Text: err.Error()}},
	}, zero, nil
}

// compile-time guard: AIMCP is an http.Handler.
var _ http.Handler = (*AIMCP)(nil)
