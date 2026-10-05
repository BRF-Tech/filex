package notify

import (
	"github.com/brf-tech/filex/backend/internal/srvtext"
)

// Saved roles that allow adding files but not encrypting (EventPermissionGaps,
// perm/gaps.go). The words are the server catalogue's, in every built-in
// language in meta, like TenantDomainChanged: each reader's bell shows their
// own.

// PermissionGaps is the notice that count roles may have lost files.encrypt,
// for one administrator (userID) or, nil, as a broadcast the administrators
// nobody confines to a tenant read.
func PermissionGaps(userID *int64, count int) Event {
	// Whole keys, never assembled: the catalogue's test finds every key by
	// its literal.
	const titleKey, bodyKey = "server.permission_gaps.title", "server.permission_gaps.body"
	meta := map[string]any{"count": count}
	for _, lang := range srvtext.BuiltinLanguages() {
		meta["title_"+lang] = srvtext.Plural(lang, titleKey, count, nil)
		meta["body_"+lang] = srvtext.Plural(lang, bodyKey, count, nil)
	}
	lang := srvtext.Pick()
	return Event{
		Event:    EventPermissionGaps,
		Severity: SeverityWarning,
		Title:    srvtext.Plural(lang, titleKey, count, nil),
		Body:     srvtext.Plural(lang, bodyKey, count, nil),
		Meta:     meta,
		UserID:   userID,
	}
}
