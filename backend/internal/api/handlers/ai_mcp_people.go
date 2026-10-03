package handlers

import (
	"context"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// The MCP tools of the bell, stars, comments and item permissions
// (ai_doors_people.go), registered with the other door tools
// (registerDoorTools) and gated by fileToolVerb like every file tool.

type mcpNotificationsListIn struct {
	Unread bool `json:"unread,omitempty" jsonschema:"only the notices not read yet"`
	Limit  int  `json:"limit,omitempty" jsonschema:"page size"`
	Offset int  `json:"offset,omitempty" jsonschema:"notices to skip"`
}

type mcpNotificationReadIn struct {
	ID  int64 `json:"id,omitempty" jsonschema:"the notice to mark read (an id notifications_list answers)"`
	All bool  `json:"all,omitempty" jsonschema:"mark every notice you can see read instead"`
}

type mcpStarIn struct {
	Path string `json:"path" jsonschema:"adapter:// path of the file or folder"`
	Star *bool  `json:"star,omitempty" jsonschema:"true (default) stars it, false takes the star off"`
}

type mcpCommentAddIn struct {
	Path string `json:"path" jsonschema:"adapter:// path of the file or folder"`
	Text string `json:"text" jsonschema:"the comment, plain text"`
}

type mcpCommentIDIn struct {
	ID int64 `json:"id" jsonschema:"the comment's id (from file_comments)"`
}

type mcpPermissionUsersIn struct {
	Q string `json:"q" jsonschema:"part of a person's name or e-mail address"`
}

type mcpPermissionSetIn struct {
	Path    string `json:"path" jsonschema:"adapter:// path of the file or folder"`
	UserID  int64  `json:"user_id,omitempty" jsonschema:"the person to grant to (an id file_permission_users answers); or group_id"`
	GroupID int64  `json:"group_id,omitempty" jsonschema:"the group to grant to (every member); or user_id"`
	Level   string `json:"level" jsonschema:"viewer, editor or owner"`
}

type mcpPermissionRevokeIn struct {
	ID    int64 `json:"id" jsonschema:"the grant's id (a row of file_permissions)"`
	Group bool  `json:"group,omitempty" jsonschema:"true for a group's grant (a row with kind: group) - the two are numbered apart"`
}

func registerPeopleTools(srv *mcp.Server, ops *aiOps) {
	regDoorTool(srv, ops, "notifications_list",
		"Your notification bell: {items: [{id, event, title, body, read_at, …}], total, limit, offset} - your notices and the broadcasts you may see, newest first. A token confined to a folder sees only the notices about files inside it (and the ones that name no file).",
		func(ctx context.Context, in mcpNotificationsListIn) (doorAnswer, error) {
			return ops.NotificationsList(ctx, in.Unread, in.Limit, in.Offset)
		}, nil)

	regDoorTool(srv, ops, "notification_read",
		"Mark a notice read ({id}), or every notice you can see ({all: true}). Your own bookkeeping: it needs read and is not audited, as in the bell.",
		func(ctx context.Context, in mcpNotificationReadIn) (doorAnswer, error) {
			return ops.NotificationRead(ctx, in.ID, in.All)
		}, nil)

	regDoorTool(srv, ops, "file_star",
		"Star a file or folder for yourself (star: false takes it off) - the explorer's star; only you see it, and it is listed under Starred.",
		func(ctx context.Context, in mcpStarIn) (doorAnswer, error) {
			star := in.Star == nil || *in.Star
			return ops.Star(ctx, in.Path, star)
		}, nil)

	regDoorTool(srv, ops, "file_comments",
		"The comments on a file or folder: {comments: [{id, user_id, body, created_at, …}], node_id}. Everyone who can see the item reads them.",
		func(ctx context.Context, in mcpPathIn) (doorAnswer, error) { return ops.Comments(ctx, in.Path) }, nil)

	regDoorTool(srv, ops, "file_comment_add",
		"Comment on a file or folder. Everyone who can see the item reads it and its owner is told; needs the account's comments.write.",
		func(ctx context.Context, in mcpCommentAddIn) (doorAnswer, error) {
			return ops.CommentAdd(ctx, in.Path, in.Text)
		}, nil)

	regDoorTool(srv, ops, "file_comment_delete",
		"Delete a comment by its id: your own, or - for an administrator - any on the tenant's files.",
		func(ctx context.Context, in mcpCommentIDIn) (doorAnswer, error) { return ops.CommentDelete(ctx, in.ID) },
		func(in mcpCommentIDIn) string { return fmt.Sprint(in.ID) })

	regDoorTool(srv, ops, "file_permissions",
		"Who may open a file or folder, as its Share dialog shows the owner: {path, storage_rbac, direct: [grants on it], inherited: [grants from a folder above], effective (your own level)}. Each grant has an id, kind (user or group), the person or group and the level. Needs owner level on the item.",
		func(ctx context.Context, in mcpPathIn) (doorAnswer, error) { return ops.Permissions(ctx, in.Path) }, nil)

	regDoorTool(srv, ops, "file_permission_users",
		"Find people to grant to, by part of a name or e-mail address, in your tenant: {users: [{id, email, display_name, username, role}]}.",
		func(ctx context.Context, in mcpPermissionUsersIn) (doorAnswer, error) {
			return ops.PermissionUsers(ctx, in.Q)
		}, nil)

	regDoorTool(srv, ops, "file_permission_set",
		"Grant a person (user_id) or a group (group_id) a level on a file or folder - viewer, editor or owner; a folder's grant reaches what is inside it. Needs owner level on the item and the account's share.users, and works on a storage with access control (storage_rbac); a viewer account can only be given viewer (RBAC.md).",
		func(ctx context.Context, in mcpPermissionSetIn) (doorAnswer, error) {
			return ops.PermissionSet(ctx, in.Path, in.UserID, in.GroupID, in.Level)
		}, nil)

	regDoorTool(srv, ops, "file_permission_revoke",
		"Take a grant away by its id (a row of file_permissions; group: true for a group's row - the two are numbered apart). Needs owner level on the item.",
		func(ctx context.Context, in mcpPermissionRevokeIn) (doorAnswer, error) {
			return ops.PermissionRevoke(ctx, in.ID, in.Group)
		},
		func(in mcpPermissionRevokeIn) string { return fmt.Sprint(in.ID) })
}
