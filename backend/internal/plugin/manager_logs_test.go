package plugin_test

// Issue #104: a storage plugin has a log its admin page shows, like an app
// plugin's - its starts and failures, and the storage sync's answers it could
// not make sense of, which reach it through LogFor(driver).

import (
	"context"
	"strings"
	"testing"

	"github.com/brf-tech/filex/backend/internal/plugin"
)

func TestAStoragePluginKeepsALogItsPageReads(t *testing.T) {
	f := newFakePlugin("acme", fullCaps())
	defer f.Close()
	m, _, _ := newManager(t)
	ctx := context.Background()

	st, err := m.InstallRemote(ctx, "acme", f.URL(), "test-token")
	if err != nil {
		t.Fatalf("install: %v", err)
	}
	waitState(t, m, st.ID, plugin.StateRunning)

	lines, next, err := m.Logs(ctx, st.ID, 0)
	if err != nil {
		t.Fatalf("logs: %v", err)
	}
	if len(lines) == 0 || !strings.Contains(lines[len(lines)-1].Msg, "plugin up") {
		t.Fatalf("the plugin's start is not in its log: %+v", lines)
	}

	// What the sync writes for a storage on this plugin's driver lands here,
	// and a repeat is counted on the same line.
	log := m.LogFor("plugin:acme")
	if log == nil {
		t.Fatal("no log for the plugin's driver")
	}
	log.Add("warn", "storage arsiv: Proje: no answer")
	log.Add("warn", "storage arsiv: Proje: no answer")
	got, _, err := m.Logs(ctx, st.ID, next)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Count != 2 || got[0].Level != "warn" {
		t.Fatalf("after the cursor: %+v", got)
	}

	if m.LogFor("plugin:nobody") != nil || m.LogFor("local") != nil {
		t.Error("a driver no plugin serves has a plugin log")
	}
	if _, _, err := m.Logs(ctx, st.ID+999, 0); err == nil {
		t.Error("the log of a plugin that does not exist was answered")
	}
}
