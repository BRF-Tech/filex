package handlers

import "github.com/brf-tech/filex/backend/internal/storage"

// detachedMutation is the context a change to the storage runs under once
// every check has passed — storage.DetachMutation, the one rule every surface
// (WebDAV included) asks; see there for why.
var detachedMutation = storage.DetachMutation
