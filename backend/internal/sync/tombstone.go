package sync

// tombstone.go — house for any future, more elaborate tombstone-pass
// heuristics. The current behaviour lives in poll.go's RunOnce + guardOK
// (block deletes when seen_count drops > 30%).
//
// Roadmap:
//   - per-storage configurable threshold (DB column or settings table)
//   - persistent "miss counter" so a node has to disappear N runs in a row
//     before being soft-deleted (catches transient list errors)
//   - emit sync_conflicts row instead of soft-delete when storage_etag
//     mismatches but local edits exist (sync_state="dirty")
//
// All currently no-op — the file exists so future agents have an obvious
// place to extend without scrolling poll.go.
