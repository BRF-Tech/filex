package plugin

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os/exec"
	"path/filepath"
	"time"
)

// One conformance run of a binary, outside any Manager.
//
// # What it is for
//
// An app store that lists storage plugins (filex Apps, #215) has to know
// that a build does what it says before it vouches for it - and "what it
// says" is judged by filex's own rules: the handshake, the describe filex
// accepts, and the conformance probes filex runs at every start of a plugin.
// Writing those rules a second time in the store would let the two drift; so
// the store runs THIS code, in a container of its own (`filex
// plugin-validator`, cmd/filex), and reads the result.
//
// CheckBinary starts the binary the way a Manager does (the same Process:
// its environment allow-list, its process group, its handshake rules), asks
// it to describe itself, opens its selftest area and probes every capability
// it declared. Nothing is registered, nothing is kept, and the process is
// stopped before it returns. A plugin with no /v1/selftest is answered
// CheckNoSelfTest: it proved nothing, which a Manager tolerates (the probes
// then run on the first storage) and a store that vouches for a build must
// not.

// Check result codes (CheckResult.Code).
const (
	CheckStartFailed       = "start_failed"
	CheckHandshake         = "handshake"
	CheckRefused           = "refused"
	CheckDescribeInvalid   = "describe_invalid"
	CheckNoSelfTest        = "no_selftest"
	CheckConformanceFailed = "conformance_failed"
	CheckTimeout           = "timeout"
)

// CheckOptions configure CheckBinary.
type CheckOptions struct {
	// Binary is the file to run, absolute; the caller verified it.
	Binary string
	// WorkDir is a directory the run owns: the plugin's working directory
	// is the binary's own, its socket directory <WorkDir>/run. A caller that
	// starts the plugin as another user (Tune) creates run/ for that user
	// first; the store's validator keeps everything else its own.
	WorkDir string
	// Name is what the plugin is told it was installed as
	// (FILEX_PLUGIN_NAME; default "plugin").
	Name string
	Log  *slog.Logger
	// Tune adjusts the plugin's command before it starts (an unprivileged
	// user, on the store's validator).
	Tune func(cmd *exec.Cmd)
	// UpTimeout bounds the start: the handshake (the Process's own 20 s) and
	// the describe. Default 30 s.
	UpTimeout time.Duration
}

// CheckResult is one run, as `filex plugin-validator` reports it.
type CheckResult struct {
	OK      bool   `json:"ok"`
	Code    string `json:"code,omitempty"`
	Message string `json:"message,omitempty"`
	// Describe is what the plugin said of itself (absent when it never got
	// that far).
	Describe *DescribeResponse `json:"describe,omitempty"`
	// Conformance is the report of the probes (absent without a selftest).
	Conformance *Report `json:"conformance,omitempty"`
}

func checkFail(r *CheckResult, code, format string, a ...any) *CheckResult {
	r.OK, r.Code, r.Message = false, code, fmt.Sprintf(format, a...)
	return r
}

// CheckBinary runs one conformance check of a plugin binary.
func CheckBinary(ctx context.Context, o CheckOptions) *CheckResult {
	res := &CheckResult{}
	if o.Log == nil {
		o.Log = slog.Default()
	}
	if o.Name == "" {
		o.Name = "plugin"
	}
	if o.UpTimeout <= 0 {
		o.UpTimeout = 30 * time.Second
	}
	token, err := mintToken()
	if err != nil {
		return checkFail(res, CheckStartFailed, "%v", err)
	}
	ups := make(chan *Client, 1)
	downs := make(chan error, 1)
	proc := &Process{
		Name:    o.Name,
		Binary:  o.Binary,
		Token:   token,
		SockDir: filepath.Join(o.WorkDir, "run"),
		Log:     o.Log,
		Tune:    o.Tune,
		// One start: a run that does not come up is answered, not retried.
		restartBackoff: time.Hour,
	}
	proc.OnUp = func(_ context.Context, c *Client) error {
		select {
		case ups <- c:
		default:
		}
		return nil
	}
	proc.OnDown = func(err error) {
		select {
		case downs <- err:
		default:
		}
	}
	runCtx, cancel := context.WithCancel(ctx)
	proc.Start(runCtx)
	// Deferred in this order so the context ends first (the supervisor stops
	// at once, even while it waits to restart a plugin that died), then the
	// process is stopped and waited for.
	defer proc.Stop()
	defer cancel()

	var c *Client
	select {
	case c = <-ups:
	case err := <-downs:
		switch {
		case err == nil:
			return checkFail(res, CheckStartFailed, "the plugin exited before it was up")
		case errors.Is(err, ErrHandshake):
			return checkFail(res, CheckHandshake, "%v", err)
		case errors.Is(err, ErrRefused):
			return checkFail(res, CheckRefused, "%v", err)
		default:
			return checkFail(res, CheckStartFailed, "%v", err)
		}
	case <-time.After(o.UpTimeout):
		return checkFail(res, CheckTimeout, "the plugin did not come up within %s", o.UpTimeout)
	case <-ctx.Done():
		return checkFail(res, CheckTimeout, "%v", ctx.Err())
	}

	dctx, dcancel := context.WithTimeout(ctx, o.UpTimeout)
	desc, err := c.Describe(dctx)
	dcancel()
	if err != nil {
		return checkFail(res, CheckDescribeInvalid, "%v", err)
	}
	res.Describe = desc

	sctx, scancel := context.WithTimeout(ctx, o.UpTimeout)
	inst, err := c.SelfTest(sctx)
	scancel()
	if err != nil {
		return checkFail(res, CheckNoSelfTest,
			"the plugin offers no /v1/selftest, so none of its claims could be probed (%v)", err)
	}
	defer func() {
		delCtx, delCancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer delCancel()
		_ = c.DeleteInstance(delCtx, inst)
	}()
	drv := newBoundDriver(fixedHandle{c: c, name: desc.Name}, desc.Capabilities, c, inst)
	rep := RunConformance(ctx, drv, desc.Capabilities, "", "selftest")
	res.Conformance = rep
	if err := rep.FailureError(); err != nil || !rep.Verified {
		if err == nil {
			err = errors.New("the conformance run did not verify")
		}
		return checkFail(res, CheckConformanceFailed, "%v", err)
	}
	res.OK = true
	return res
}
