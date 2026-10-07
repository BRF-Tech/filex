package model

// OfficeSession is the version of a document an ONLYOFFICE editing session
// opened (table office_sessions, migration 00092; internal/onlyoffice
// session_base.go owns what it means, #184). A save of the session is written
// over the file only while the file is still this version.
type OfficeSession struct {
	// DocKey is the session's document.key.
	DocKey string `json:"doc_key"`
	NodeID int64  `json:"node_id"`
	// Size, MtimeNs (Unix nanoseconds, 0 = none) and Etag ("" = none) are the
	// version the storage driver reported.
	Size    int64  `json:"size"`
	MtimeNs int64  `json:"mtime_ns"`
	Etag    string `json:"etag,omitempty"`
	// Unknown: the session is on an older version, which one is not known.
	Unknown bool `json:"unknown,omitempty"`
	// Dropped: the person chose the outside version; the save is not written.
	Dropped bool `json:"dropped,omitempty"`
	// ExpiresUnix is when the row may be removed (Unix seconds).
	ExpiresUnix int64 `json:"expires_unix"`
}
