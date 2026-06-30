package handlers

import (
	"context"
	"encoding/base64"
	"errors"
	"net/http"
	"unicode/utf8"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"gitlab.com/brftech/filemanager/backend/internal/auth"
	apitoken "gitlab.com/brftech/filemanager/backend/internal/auth/drivers/apitoken"
	"gitlab.com/brftech/filemanager/backend/internal/db"
	"gitlab.com/brftech/filemanager/backend/internal/storage"
	"gitlab.com/brftech/filemanager/backend/internal/version"
)

// AIMCP exposes filex as a Model Context Protocol server over streamable
// HTTP (JSON-RPC). The same aiOps core that backs the REST handler powers
// each MCP tool, so AI agents can drive filex directly while work.brf.sh's
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
	store    db.Store
	resolver func(int64) (storage.Driver, error)
	admin    *AIAdmin
	handler  http.Handler
}

// NewAIMCP builds the MCP HTTP handler. `admin` powers the admin_* tools,
// which are only registered for tokens carrying the `admin` scope; pass nil
// to disable the admin tool surface entirely.
func NewAIMCP(store db.Store, resolver func(int64) (storage.Driver, error), admin *AIAdmin) *AIMCP {
	h := &AIMCP{store: store, resolver: resolver, admin: admin}
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
	ops := newAIOps(h.store, h.resolver)
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

// registerFilexTools wires every MCP tool onto srv, bound to ops.
func registerFilexTools(srv *mcp.Server, ops *aiOps) {
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
