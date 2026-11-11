package model

import "time"

// Uploader is one of a person's upload destinations (migration 00136): a
// screenshot tool, a phone shortcut, a script - anything that sends a file to
// POST /api/uploads with the uploader's key and wants a link back.
//
// ⚠⚠ The key is NOT an API token. It lives in its own table, the way an S3
// key, an SSH key and an NFS export do, and exactly one door knows it: the
// upload endpoint. No other route looks a request's key up here, so a key
// that leaks (a tool's settings file copied around, a config pasted into a
// chat) can put files into ONE folder and nothing else - it cannot list,
// read, download, overwrite or delete anything, there or anywhere. An API
// token with `write` and a `root:` folder would still have reached every
// write verb of the agent API inside that folder; that is what this
// replaces (it was the ShareX endpoint's credential until 0.60).
//
// The folder is held by the storage's ID, which never moves, plus the tenant
// the storage was in when the uploader was made (lesson #1787): renaming the
// storage keeps the uploader, while a storage the operator moved to another
// tenant, or deleted and created again under the same name, takes nothing.
type Uploader struct {
	ID     int64  `json:"id"`
	UserID int64  `json:"user_id"`
	Name   string `json:"name"`
	// StorageID and Folder are where every upload lands: Folder is the path
	// inside the storage, "" for its root.
	StorageID int64  `json:"storage_id"`
	Folder    string `json:"folder"`
	// Visibility is what the link an upload answers with opens:
	// UploaderPublic, a public share link anybody with it may open, or
	// UploaderPrivate, the file in filex, behind sign-in and the reader's own
	// permission.
	Visibility string `json:"visibility"`
	// LinkDays is how long a public link lives, in days; 0 asks for the
	// longest the server allows - no expiry where there is no ceiling,
	// otherwise the ceiling. A link is cut to the ceiling as it is made,
	// whatever this says (share.ClampExpiry), so a ceiling lowered later
	// holds for every upload after it.
	LinkDays int `json:"link_days"`
	// ShortLink answers an upload with its link's SHORT address when an app
	// holds the link-shortener role (0.60, migration 00138): off by default.
	// No shortener, or one that does not answer, and the upload answers the
	// link itself.
	ShortLink bool `json:"short_link"`
	// TenantID is the tenant the storage was in when the uploader was made
	// (db.StorageStamp, 0 for none); an upload is refused once that tenant no
	// longer has the storage.
	TenantID int64 `json:"-"`
	// KeyHash is the sha256 of the key. The key is shown once - at creation
	// and whenever a new one is made - and never stored.
	KeyHash    string     `json:"-"`
	CreatedAt  time.Time  `json:"created_at"`
	KeySetAt   time.Time  `json:"key_set_at"`
	LastUsedAt *time.Time `json:"last_used_at,omitempty"`
}

// The two things an uploader's link can be.
const (
	UploaderPublic  = "public"
	UploaderPrivate = "private"
)

// NormalizeUploaderVisibility maps a stored or requested value to one of the
// two; anything that is not exactly "private" is public, the default.
func NormalizeUploaderVisibility(s string) string {
	if s == UploaderPrivate {
		return UploaderPrivate
	}
	return UploaderPublic
}

// UploadDeletion is the deletion link of one upload: whoever holds the link
// may move that one file to the trash and revoke its public link, and nothing
// else. Like the key, only the sha256 of the link's token is kept.
//
// It names the FILE by its catalogue row (NodeID) on a storage by ID: a file
// moved or renamed since is still the one the link deletes, and a different
// file that later takes the old name is never touched. A link whose file is
// gone answers so, and does nothing.
type UploadDeletion struct {
	ID         int64  `json:"id"`
	TokenHash  string `json:"-"`
	UserID     int64  `json:"user_id"`
	UploaderID int64  `json:"uploader_id"`
	StorageID  int64  `json:"storage_id"`
	NodeID     int64  `json:"node_id"`
	// ShareID is the public link the upload answered with, 0 for a private
	// one; deleting the file revokes it.
	ShareID int64 `json:"share_id"`
	// TenantID: the tenant the storage was in at the upload (StorageStamp).
	TenantID int64 `json:"-"`
	// Name is the file's name as it was stored, for the confirmation page.
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"created_at"`
}
