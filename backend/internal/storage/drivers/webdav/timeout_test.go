package webdav

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/brf-tech/filex/backend/internal/storage"
	"github.com/brf-tech/filex/backend/internal/storage/stall"
)

// The form shows the descriptor's defaults and bounds; the driver applies the
// policy's. They are the same values — this keeps it that way.
func TestDescriptorShowsThePolicy(t *testing.T) {
	desc, ok := storage.DescriptorFor("webdav")
	if !ok {
		t.Fatal("no webdav descriptor")
	}
	d := &Driver{}
	if err := d.Init(context.Background(), map[string]any{"url": "https://dav.example.com/", "user": "u", "root": "fx"}); err != nil {
		t.Fatal(err)
	}
	want := map[string]struct{ def, max int }{
		"attempt_timeout_s": {int(d.policy.AttemptTimeout / time.Second), stall.AttemptTimeoutLimitS},
		"max_attempts":      {d.policy.MaxAttempts, stall.MaxAttemptsLimit},
		"total_timeout_s":   {int(d.policy.TotalTimeout / time.Second), stall.TotalTimeoutLimitS},
	}
	for key, w := range want {
		f, ok := desc.Field(key)
		if !ok {
			t.Errorf("%s is not in the descriptor, so no form offers it", key)
			continue
		}
		if f.Type != storage.FieldInt || !f.Advanced || f.Default != w.def || *f.Min != 1 || *f.Max != w.max {
			t.Errorf("%s: %+v, want int/advanced/%d/1/%d", key, f, w.def, w.max)
		}
	}
	if f, _ := desc.Field("attempt_timeout_s"); !strings.Contains(f.Help, "10 minutes") || f.HelpI18nKey != "storages.fieldHelp.webdavAttemptTimeout" {
		t.Errorf("the WebDAV form does not say copies and moves wait longer: %+v", f)
	}
	if d.policy.AttemptTimeout != 30*time.Second {
		t.Errorf("default attempt timeout %v: a Nextcloud listing of a big folder needs well over 10 s", d.policy.AttemptTimeout)
	}
}

// The error names the server, never a password written into its URL.
func TestUnavailable_NamesTheServerWithoutThePassword(t *testing.T) {
	d := &Driver{}
	url := strings.Replace(refusedEndpoint(t), "http://", "http://alice:s3cret@", 1) + "/remote.php/dav/"
	if err := d.Init(context.Background(), map[string]any{
		"url": url, "user": "alice", "password": "s3cret", "root": "fx",
		"attempt_timeout_s": 1, "total_timeout_s": 1,
	}); err != nil {
		t.Fatal(err)
	}
	_, err := d.List(context.Background(), "/")
	if err == nil || !strings.Contains(err.Error(), "webdav server http://127.0.0.1:") {
		t.Fatalf("got %v", err)
	}
	if strings.Contains(err.Error(), "s3cret") {
		t.Errorf("the error carries the password: %v", err)
	}
}
