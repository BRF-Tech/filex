package model

import "time"

// App plugin sources — where the module came from (migration 00042).
const (
	AppPluginSourceUpload = "upload"
	AppPluginSourceURL    = "url"
	AppPluginSourceGitHub = "github"
	AppPluginSourceBundle = "bundle"
)

// AppPlugin is one installed in-process WebAssembly app plugin — the admin's
// intent (module, manifest as installed, the permission grant, on/off).
// Whether it is compiled and answering right now is runtime state the
// wasmplugin registry derives by loading it; only the last load failure is
// kept so a stopped plugin still shows something useful.
//
// Not a storage plugin: those are model.Plugin (00029, out of process).
type AppPlugin struct {
	ID int64 `json:"id"`
	// Name is the manifest's name, unique, [a-z0-9][a-z0-9_-]{0,31}. It names
	// the plugin's directory under <data-dir>/app-plugins/ and every menu key
	// (`plugin:<name>/<action>`).
	Name    string `json:"name"`
	Version string `json:"version"`
	// LabelJSON is the manifest label ({"en": …, "tr": …}) kept as-is so a
	// list can show it without parsing the whole manifest.
	LabelJSON string `json:"-"`
	// ManifestJSON is filex-app.json exactly as installed (validated).
	ManifestJSON string `json:"-"`
	// WasmPath is the module file inside the plugin's directory.
	WasmPath string `json:"-"`
	// SHA256 of the module as installed; checked again at every load.
	SHA256    string `json:"sha256"`
	Source    string `json:"source"`
	SourceURL string `json:"source_url,omitempty"`
	Signed    bool   `json:"signed"`
	// PermissionsJSON is the grant the admin approved at install, a JSON
	// array of permission strings. It is what host functions check — never
	// the manifest's list, so an upgrade cannot widen it without a new
	// approval.
	PermissionsJSON string    `json:"-"`
	Enabled         bool      `json:"enabled"`
	LastError       string    `json:"last_error,omitempty"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

// AppPluginOverride is the admin's change to one action of a plugin. A
// missing row means "as the manifest says". AppliesJSON "" keeps the
// manifest's rule; otherwise it is what the admin CHANGED about it
// (wasmplugin.appliesDelta, `"v":2`), resolved against the current manifest
// and engines at every read. A row without `"v"` is a whole rule written
// before v0.43.0; the registry rewrites those as changes at start.
type AppPluginOverride struct {
	PluginID    int64  `json:"-"`
	ActionID    string `json:"id"`
	Enabled     bool   `json:"enabled"`
	AdminOnly   bool   `json:"admin_only"`
	AppliesJSON string `json:"-"`
}

// App plugin job statuses. They mirror the ops row the job rides on; the
// job row is where the plugin-specific facts live (which plugin/action, the
// parameters, the outputs).
const (
	AppPluginJobPending   = "pending"
	AppPluginJobRunning   = "running"
	AppPluginJobOK        = "ok"
	AppPluginJobFailed    = "failed"
	AppPluginJobCancelled = "cancelled"
)

// AppPluginJob is one action run.
type AppPluginJob struct {
	// ID is a random 32-hex token; pending_ops.dest carries it so the worker
	// can find this row from the queue row.
	ID         string `json:"id"`
	OpID       *int64 `json:"op_id,omitempty"`
	PluginID   int64  `json:"plugin_id"`
	PluginName string `json:"plugin"`
	ActionID   string `json:"action"`
	StorageID  int64  `json:"storage_id"`
	// PathsJSON is the JSON array of storage-relative input paths.
	PathsJSON string `json:"-"`
	// ParamsJSON is the JSON object of action parameters (from the form).
	ParamsJSON string `json:"-"`
	ActorID    *int64 `json:"actor_id,omitempty"`
	Locale     string `json:"locale,omitempty"`
	// Label is the action label in the actor's locale, resolved at submit so
	// the tray never has to know the manifest.
	Label   string `json:"label"`
	Status  string `json:"status"`
	Message string `json:"message,omitempty"`
	// OutputsJSON is the JSON array of {path} objects written once the
	// outputs are committed to the storage.
	OutputsJSON string     `json:"-"`
	Error       string     `json:"error,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
	FinishedAt  *time.Time `json:"finished_at,omitempty"`
}

// App-plugin schedule statuses (migration 00050). `status` doubles as the
// lease two filex processes claim a due row through: only the UPDATE that
// moved a row from due to running may run it.
const (
	AppPluginScheduleDue     = "due"
	AppPluginScheduleRunning = "running"
	// AppPluginScheduleQueued is the terminal state of a piece of WORK: the
	// ops job exists and owns the outcome from here. The schedule's promise
	// ends at "handed to the queue at the right minute".
	AppPluginScheduleQueued = "queued"
	AppPluginScheduleFailed = "failed"
	// AppPluginScheduleSkipped is a row that came due while the app could
	// not run it — stopped, uninstalled, or the schedule permission
	// withdrawn. Not an error and not retried: the next wake-up asks again.
	AppPluginScheduleSkipped = "skipped"
)

// AppPluginScheduleWakeupKey is the reserved key of the hourly wake-up row.
// An app's own keys are 1..64 characters (see wasmplugin.ValidScheduleKey),
// so nothing an app returns can ever land on this row.
const AppPluginScheduleWakeupKey = ""

// AppPluginScheduleItem is one row that comes due: either the hourly wake-up
// of an app (Key == AppPluginScheduleWakeupKey) or one piece of work that
// wake-up asked for. See db/migrations/sqlite/00050_app_plugin_schedule.sql.
type AppPluginScheduleItem struct {
	PluginID int64  `json:"plugin_id"`
	Key      string `json:"key"`
	// DueAt is when this runs, to the second. Nothing runs before it.
	DueAt time.Time `json:"due_at"`
	// ActionID, StorageID, PathsJSON and ParamsJSON are the job to queue;
	// all four are empty on a wake-up row.
	ActionID   string `json:"action_id,omitempty"`
	StorageID  int64  `json:"storage_id,omitempty"`
	PathsJSON  string `json:"-"`
	ParamsJSON string `json:"-"`
	Status     string `json:"status"`
	// Attempts counts claims, so a row that keeps coming back says so.
	Attempts int `json:"attempts"`
	// ClaimedBy names the filex process that took the row (Registry.InstanceID).
	ClaimedBy string     `json:"claimed_by,omitempty"`
	ClaimedAt *time.Time `json:"claimed_at,omitempty"`
	// JobID is the app_plugin_jobs row this item became, once queued.
	JobID string `json:"job_id,omitempty"`
	// Error is why the last attempt did not queue work — or, on a wake-up
	// row, what the last wake-up decided.
	Error     string    `json:"error,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// AppPluginSigningKey is a key the host holds for a signing plugin
// (migration 00044): the tenant CA (purpose "ca") or a per-signer leaf
// (purpose "leaf"). KeySealed is the PKCS#8 key sealed with the instance
// secret; a destroyed leaf keeps its certificate and loses the key.
type AppPluginSigningKey struct {
	ID          string     `json:"id"`
	TenantID    int64      `json:"tenant_id"`
	PluginID    int64      `json:"plugin_id"`
	Purpose     string     `json:"purpose"`
	Subject     string     `json:"subject"`
	CertPEM     string     `json:"cert_pem"`
	KeySealed   string     `json:"-"`
	CreatedAt   time.Time  `json:"created_at"`
	ExpiresAt   *time.Time `json:"expires_at,omitempty"`
	DestroyedAt *time.Time `json:"destroyed_at,omitempty"`
	RetiredAt   *time.Time `json:"retired_at,omitempty"`
}

// AppPluginStateFile is one file an app plugin keeps state on, with the
// file still there to show for it. The state table is keyed by a path hash,
// so the path itself comes from the node row it joins — which also means a
// deleted file drops out of the listing by itself.
type AppPluginStateFile struct {
	StorageID   int64  `json:"storage_id"`
	StorageName string `json:"storage"`
	Path        string `json:"path"`
	Name        string `json:"name"`
	Key         string `json:"key"`
	Value       string `json:"value"`
}

// AppPluginLock makes one file read-only for EVERYONE — owner and
// administrators included — until the holding plugin lifts it or Until
// passes (migration 00045). A signature request is the canonical holder: the
// document must not change under the signers. The ACL caps every caller's
// effective level on Rel at viewer while a lock is live; the plugin's own
// jobs write through the ops worker, which does not consult the ACL, so the
// signing itself still lands. One lock per file; only the plugin that holds
// it (or an administrator through the admin API) may lift it.
type AppPluginLock struct {
	StorageID  int64      `json:"storage_id"`
	PathHash   string     `json:"-"`
	Rel        string     `json:"path"`
	PluginID   int64      `json:"plugin_id"`
	PluginName string     `json:"plugin"`
	Reason     string     `json:"reason,omitempty"`
	Until      *time.Time `json:"until,omitempty"`
	CreatedBy  *int64     `json:"created_by,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
}

// Live reports whether the lock still holds at now (nil Until = until lifted).
func (l *AppPluginLock) Live(now time.Time) bool {
	if l == nil {
		return false
	}
	return l.Until == nil || now.Before(*l.Until)
}
