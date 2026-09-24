package wasmplugin

import (
	"errors"
	"fmt"
)

// Outcome codes a guest call can end with. They are what the job row, the
// admin log and the user-facing message key on — never a wazero stack.
const (
	CodePluginError = "plugin_error" // the guest set an error message
	CodePluginTrap  = "plugin_trap"  // the guest crashed (unreachable, OOB…)
	CodePluginOOM   = "plugin_oom"   // the guest hit its memory ceiling
	CodeTimeout     = "timeout"      // the call outlived its budget
	CodeRefused     = "refused"      // describe disagrees with the manifest
	CodeUnsupported = "unsupported"  // this host cannot run plugins (arch)
)

// CallError is a failed guest call, classified.
type CallError struct {
	Code    string
	Export  string
	Message string // safe for a user; the guest's own words when it set any
	Cause   error  // the runtime's error, for the plugin log only
}

func (e *CallError) Error() string {
	if e.Message != "" {
		return fmt.Sprintf("%s (%s): %s", e.Export, e.Code, e.Message)
	}
	return fmt.Sprintf("%s (%s)", e.Export, e.Code)
}

func (e *CallError) Unwrap() error { return e.Cause }

// ErrUnsupportedArch is returned by New on a CPU wazero has no compiler for.
var ErrUnsupportedArch = errors.New("wasmplugin: app plugins need an amd64 or arm64 host (wazero's interpreter is refused — it would take every core)")

// IsCode reports whether err is a CallError with the given code.
func IsCode(err error, code string) bool {
	var ce *CallError
	return errors.As(err, &ce) && ce.Code == code
}
