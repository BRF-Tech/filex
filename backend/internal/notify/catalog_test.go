package notify

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// inlineEventType matches a `notify.EventType("…")` / `EventType("…")`
// conversion of a string LITERAL — the shape that lets an event exist
// without a constant in event.go.
//
// A conversion of a variable (EventType(name)) is deliberately not matched:
// that is a subsystem forwarding an event it was handed, not the birth of a
// new one.
var inlineEventType = regexp.MustCompile(`\bEventType\("[^"]*"\)`)

// TestNoInlineEventTypes keeps event.go the single source of truth for the
// event catalogue.
//
// Why it exists: "e2e.escrow_used" was written as an inline
// notify.EventType("e2e.escrow_used") inside an API handler. It is emitted on
// every escrow unlock — one of the most security-relevant things filex can
// report — and nothing that reads the catalogue could see it, so it never
// appeared in the admin UI's subscription list and no operator could ask to be
// told. The bug was not the missing UI entry; it was that an event could be
// born somewhere the catalogue does not look.
//
// Declare the constant in event.go and use it. Then every reader — the UI
// list, its translations, the divergence test in web/tests/webhooks — sees it.
func TestNoInlineEventTypes(t *testing.T) {
	root := backendRoot(t)

	var offenders []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case "node_modules", "testdata", ".git":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		// event.go is where the catalogue lives; its own declarations use
		// `EventType = "…"`, not a conversion, but keep it exempt anyway so a
		// doc comment quoting the bad shape cannot fail the test.
		if filepath.Base(path) == "event.go" && filepath.Base(filepath.Dir(path)) == "notify" {
			return nil
		}
		src, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		for _, m := range inlineEventType.FindAll(src, -1) {
			rel, _ := filepath.Rel(root, path)
			offenders = append(offenders, filepath.ToSlash(rel)+": "+string(m))
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking %s: %v", root, err)
	}

	if len(offenders) > 0 {
		t.Fatalf("event types built from a string literal outside event.go:\n  %s\n\n"+
			"Declare the event as a constant in internal/notify/event.go and use it. "+
			"An event that is not a constant there never reaches the admin UI's "+
			"subscription list, so nobody can subscribe to it.",
			strings.Join(offenders, "\n  "))
	}
}

// TestCatalogEventsAreDotted documents (and enforces) the naming rule the
// admin UI relies on to tell a subscribable webhook-v2 event apart from a
// pre-v2 operational alarm.
func TestCatalogEventsAreDotted(t *testing.T) {
	dotted := []EventType{
		EventFileUploaded, EventFileUpdated, EventFileUploadFailed,
		EventFileDeleted, EventFileMoved, EventFileTrashed,
		EventShareCreated, EventDropReceived, EventFileInfected,
		EventCommentAdded, EventE2EEscrowUsed,
	}
	for _, e := range dotted {
		if !strings.Contains(string(e), ".") {
			t.Errorf("catalogue event %q must carry a dot: the UI list, its "+
				"translations and the divergence test all select on that", e)
		}
	}

	// The pre-v2 alarms must NOT look like catalogue entries, or the parser in
	// web/tests/webhooks/eventCatalog.test.ts would demand UI checkboxes for
	// them.
	for _, e := range []EventType{
		EventReplicaFail, EventQuotaFull, EventDiskFull,
		EventQueueStuck, EventUpdateAvailable, EventUpdateApplied,
	} {
		if strings.Contains(string(e), ".") {
			t.Errorf("operational alarm %q carries a dot: it would be read as a "+
				"subscribable catalogue event", e)
		}
	}
}

// backendRoot walks up from this package to the `backend` module directory.
func backendRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for i := 0; i < 8; i++ {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	t.Fatalf("could not find the backend module root from %s", dir)
	return ""
}
