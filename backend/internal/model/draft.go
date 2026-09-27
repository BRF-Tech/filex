package model

import "time"

// Draft is a new document that has not been saved yet (issue #71, migration
// 00064). Its bytes are a real file in the storage's drafts area —
// `.filex-drafts/<UserID>/<Key>/<name>` (internal/syspath.Drafts) — with a
// catalogue row of its own (NodeID); this is what the file cannot say about
// itself: whose it is and where it is meant to go.
//
// ⚠ Not serialised as it is. The API answers with its own shape
// (internal/drafts.View), which never carries the internal path or the owner.
type Draft struct {
	ID int64
	// Key is the `<draft key>` folder segment, and the draft's address in the
	// API (/api/files/drafts/{key}).
	Key       string
	UserID    int64
	StorageID int64
	NodeID    int64
	// TargetDir is the storage-relative folder the document is meant for,
	// "" for the storage root, no leading slash.
	TargetDir string
	// TargetName is the file name it is meant to have there.
	TargetName string
	// DocType is the New-document type it was made as (internal/newdoc key),
	// so a name with no extension still opens in the right editor.
	DocType   string
	CreatedAt time.Time
	UpdatedAt time.Time

	// Read from the draft file's catalogue row by the store's queries: where
	// the bytes are, how many there are, when they last changed, and whether
	// the row is live (a discarded draft's row is in the trash).
	Path     string
	Size     int64
	Mime     string
	Mtime    *time.Time
	NodeLive bool
}
