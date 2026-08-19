package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/plugin"
	"github.com/brf-tech/filex/backend/internal/storage"
)

// The second conformance gate: a storage on a plugin is probed against the
// configuration somebody just typed, BEFORE the row is written.
//
// # Why here and not only at install
//
// The install-time selftest proves the plugin's code works. It cannot prove
// that these credentials reach that bucket, that this path exists, or that
// the account may write there — and a storage created on a configuration
// that half-works is the same failure as a plugin that half-works: the user
// meets operations the UI offers and the backend refuses, and reads it as
// filex being broken.
//
// ⚠ Built-in drivers are NOT put through this. They are compiled in, covered
// by the repository's own tests, and already have their own "test connection"
// path; running probes on every storage save would be a behaviour change for
// paths that have never had this problem.

// storageVerifyTimeout bounds the whole check so a save cannot hang on a
// backend that accepts connections and then says nothing.
const storageVerifyTimeout = 2 * time.Minute

// verifyPluginStorage returns (message, ok). ok is false when the storage
// must be refused; message is then the reason, already phrased for the
// operator who is looking at the form.
//
// Anything that is not a plugin driver passes untouched.
func verifyPluginStorage(ctx context.Context, mgr *plugin.Manager, resolver func(int64) (storage.Driver, error), st *model.Storage) (string, bool) {
	if mgr == nil || st == nil || !strings.HasPrefix(st.Driver, plugin.DriverPrefix) {
		return "", true
	}
	mode := mgr.ConformanceMode()
	if mode == plugin.ConformanceOff {
		return "", true
	}

	caps, ok := mgr.CapabilitiesFor(st.Driver)
	if !ok {
		// The driver is not registered: its plugin is stopped, failed, or was
		// removed. Saying that is far more useful than the generic "unknown
		// driver" the registry would produce a moment later.
		return fmt.Sprintf("the plugin providing %q is not running — start it on the Plugins page and try again", st.Driver), false
	}

	drv, err := storage.Get(st.Driver)
	if err != nil {
		return fmt.Sprintf("%s: %v", st.Driver, err), false
	}
	cctx, cancel := context.WithTimeout(ctx, storageVerifyTimeout)
	defer cancel()

	cfg := map[string]any{}
	if len(st.ConfigJSON) > 0 {
		if err := json.Unmarshal(st.ConfigJSON, &cfg); err != nil {
			return "storage config is not valid JSON", false
		}
	}
	if err := drv.Init(cctx, cfg); err != nil {
		return fmt.Sprintf("the plugin could not open this storage: %v", err), false
	}

	rep := plugin.VerifyStorage(cctx, drv, caps)
	if rep.Verified {
		return "", true
	}
	if mode == plugin.ConformanceWarn {
		return "", true
	}
	return rep.FailureError().Error(), false
}
