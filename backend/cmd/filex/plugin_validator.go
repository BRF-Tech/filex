package main

// `filex plugin-validator` - the conformance run an app store asks of a
// storage plugin build before it lists it (filex Apps, #215;
// docs/PLUGINS.md "Storage plugins from a store").
//
// It runs in a container of its own that has NO network at all: the store
// cannot call it, so the two share a directory (the spool), and the protocol
// is files:
//
//	<spool>/status.json      written here every few seconds: this filex, its platform
//	<spool>/in/<id>.bin      the build, written by the store
//	<spool>/in/<id>.json     the job, written by the store last (its arrival is the job's)
//	<spool>/out/<id>.json    the result, written here
//
// A job is run with filex's own code (plugin.CheckBinary): the binary is
// copied into a private directory while its sha256 is computed - what runs is
// the bytes that were checked - then started the way a Manager starts a
// plugin, described, and its selftest area probed for every capability it
// declared. Nothing is installed or kept.
//
// ⚠⚠ The build is somebody else's native program. On Linux, run as root in
// the container, this command starts it as an UNPRIVILEGED user of its own
// (--plugin-uid/--plugin-gid, required there): never root, never this
// command's user, never the store's. The spool's `in/` is handed to the
// store's user (--store-uid) and `out/` stays this command's, so the plugin
// under test can neither leave a job nor forge a result; a job file another
// user wrote is not run. Outside Linux there is no other user to hand it to,
// so the command starts only with --insecure-dev (development only).
//
// ⚠⚠ One run must not reach the next. A plugin can fork a helper that leaves
// its process group (setsid), and every run uses the same user, so:
//   - the run's directory and the build stay this command's (0711 / 0555) in a
//     parent of this command's own that nobody else can list (--work-dir,
//     never a shared /tmp); the plugin is given only <run>/run, its socket
//     directory - it can start its build, never replace it;
//   - before and after every run, every process of the plugin's user is
//     killed (plugin_validator_linux.go pvKillUser), and a run does not start
//     while one is left; what that user left in the shared temporary
//     directories is removed.
// A PID namespace per run would do the killing by itself, but creating one
// needs CAP_SYS_ADMIN, which the store's container does not have (and Docker's
// default seccomp profile refuses new namespaces without it); the sweep needs
// only CAP_KILL, which it has.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/brf-tech/filex/backend/internal/plugin"
	"github.com/brf-tech/filex/backend/internal/version"
)

// pvProtocol is the spool's version (the store reads only this one).
const pvProtocol = 1

type pvOptions struct {
	spool     string
	workRoot  string
	pluginUID int
	pluginGID int
	storeUID  int
	maxBinary int64
	jobTime   time.Duration
	once      bool
	// insecureDev lets the command start outside Linux, where the plugin under
	// test runs as this command's own user (development only).
	insecureDev bool
	log         *slog.Logger
}

func pluginValidatorCmd() *cobra.Command {
	o := pvOptions{}
	var maxMB int64
	c := &cobra.Command{
		Use:   "plugin-validator",
		Short: "Run storage plugin conformance checks an app store leaves in a spool directory",
		Long: "Run storage plugin conformance checks an app store leaves in a spool directory.\n" +
			"For the store's isolated validator container: no network, the plugin under test\n" +
			"started as an unprivileged user (--plugin-uid), the spool's in/ owned by the store.",
		RunE: func(cmd *cobra.Command, args []string) error {
			o.maxBinary = maxMB << 20
			o.log = slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
			ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
			defer cancel()
			return runPluginValidator(ctx, o)
		},
	}
	c.Flags().StringVar(&o.spool, "spool", "", "the spool directory shared with the store (required)")
	c.Flags().StringVar(&o.workRoot, "work-dir", "", "a directory of this command's own where each run gets a private one (required on Linux: a tmpfs mounted exec, mode 0711 - never a shared directory such as /tmp)")
	c.Flags().IntVar(&o.pluginUID, "plugin-uid", -1, "the user a plugin under test runs as (required on Linux: not 0, not this command's user, not --store-uid, used by nothing else)")
	c.Flags().IntVar(&o.pluginGID, "plugin-gid", -1, "the group a plugin under test runs as (Linux; not 0; default: the uid)")
	c.Flags().IntVar(&o.storeUID, "store-uid", -1, "the store's user: in/ is handed to it, and only its job files are run")
	c.Flags().Int64Var(&maxMB, "max-binary-mb", 512, "the largest build a job may hand over")
	c.Flags().DurationVar(&o.jobTime, "job-timeout", 3*time.Minute, "how long one run may take, start and probes together")
	c.Flags().BoolVar(&o.once, "once", false, "run the jobs waiting now and exit")
	c.Flags().BoolVar(&o.insecureDev, "insecure-dev", false, "outside Linux only: start anyway, the plugin under test running as this command's own user (development only, no isolation)")
	_ = c.MarkFlagRequired("spool")
	return c
}

// pvStatus is <spool>/status.json.
type pvStatus struct {
	Protocol     int       `json:"protocol"`
	FilexVersion string    `json:"filex_version"`
	Platform     string    `json:"platform"`
	HeartbeatAt  time.Time `json:"heartbeat_at"`
	Busy         string    `json:"busy,omitempty"`
}

// pvJob is <spool>/in/<id>.json.
type pvJob struct {
	Protocol  int       `json:"protocol"`
	ID        string    `json:"id"`
	SHA256    string    `json:"sha256"`
	Name      string    `json:"name"`
	Version   string    `json:"version"`
	CreatedAt time.Time `json:"created_at"`
}

// pvResult is <spool>/out/<id>.json.
type pvResult struct {
	Protocol     int                      `json:"protocol"`
	ID           string                   `json:"id"`
	SHA256       string                   `json:"sha256"`
	FilexVersion string                   `json:"filex_version"`
	Platform     string                   `json:"platform"`
	OK           bool                     `json:"ok"`
	Code         string                   `json:"code,omitempty"`
	Message      string                   `json:"message,omitempty"`
	Describe     *plugin.DescribeResponse `json:"describe,omitempty"`
	Conformance  *plugin.Report           `json:"conformance,omitempty"`
	StartedAt    time.Time                `json:"started_at"`
	EndedAt      time.Time                `json:"ended_at"`
}

var pvIDRe = regexp.MustCompile(`^[0-9a-f]{32}$`)

func pvPlatform() string { return runtime.GOOS + "/" + runtime.GOARCH }

func runPluginValidator(ctx context.Context, o pvOptions) error {
	if err := pvCheckOptions(&o); err != nil {
		return err
	}
	return pvServe(ctx, o)
}

// pvCheckOptions refuses a configuration in which the plugin under test
// would not be held apart from this command, the store or the next run
// (it fills in the defaults it may).
func pvCheckOptions(o *pvOptions) error {
	if strings.TrimSpace(o.spool) == "" {
		return errors.New("plugin-validator: --spool is required")
	}
	if o.pluginGID < 0 {
		o.pluginGID = o.pluginUID
	}
	if !pvCredentialsSupported {
		if o.pluginUID >= 0 {
			return errors.New("plugin-validator: --plugin-uid works on Linux only")
		}
		if !o.insecureDev {
			return errors.New("plugin-validator: outside Linux the plugin under test would run as this command's own user, with nothing between it and its result; " +
				"refused without --insecure-dev (development only: a store's validator runs on Linux)")
		}
		if strings.TrimSpace(o.workRoot) == "" {
			o.workRoot = filepath.Join(os.TempDir(), "filex-plugin-validator")
		}
		return nil
	}
	if o.insecureDev {
		return errors.New("plugin-validator: --insecure-dev is for development outside Linux; on Linux the plugin under test runs as --plugin-uid")
	}
	// ⚠⚠ The plugin under test runs as a user of its own, always: as this
	// command's user it would own out/ and could write its own result, as the
	// store's it could leave jobs in in/, as root it would own the container.
	if o.pluginUID < 0 {
		return errors.New("plugin-validator: --plugin-uid is required: the plugin under test runs as an unprivileged user of its own (--plugin-uid, --plugin-gid)")
	}
	if o.pluginUID == 0 || o.pluginGID == 0 {
		return errors.New("plugin-validator: a plugin under test is never started as root: --plugin-uid and --plugin-gid must not be 0")
	}
	if o.pluginUID == os.Geteuid() {
		return fmt.Errorf("plugin-validator: --plugin-uid %d is this command's own user: the plugin under test could write its own result", o.pluginUID)
	}
	if o.storeUID >= 0 && o.pluginUID == o.storeUID {
		return fmt.Errorf("plugin-validator: --plugin-uid %d is the store's user (--store-uid): the plugin under test could leave jobs of its own", o.pluginUID)
	}
	if strings.TrimSpace(o.workRoot) == "" {
		return errors.New("plugin-validator: --work-dir is required: a directory of this command's own (a tmpfs mounted exec, mode 0711), never a shared temporary directory such as /tmp")
	}
	return nil
}

// pvServe runs the validator with options pvCheckOptions accepted.
func pvServe(ctx context.Context, o pvOptions) error {
	if err := pvPrepareWorkRoot(o.workRoot); err != nil {
		return fmt.Errorf("plugin-validator: --work-dir: %w", err)
	}
	in := filepath.Join(o.spool, "in")
	out := filepath.Join(o.spool, "out")
	for _, d := range []string{in, out} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return fmt.Errorf("plugin-validator: %w", err)
		}
		if err := os.Chmod(d, 0o755); err != nil {
			return fmt.Errorf("plugin-validator: %w", err)
		}
	}
	if o.storeUID >= 0 {
		if err := pvChown(in, o.storeUID, -1); err != nil {
			return fmt.Errorf("plugin-validator: handing in/ to the store's user: %w", err)
		}
	}
	if o.pluginUID > 0 {
		// The results, and the heartbeat beside them, are written where the
		// plugin's user can neither write nor plant a link: out/ is this
		// command's, and the spool itself must not be that user's to change.
		if err := pvChown(out, os.Geteuid(), os.Getegid()); err != nil {
			return fmt.Errorf("plugin-validator: taking out/: %w", err)
		}
		for _, d := range []string{o.spool, out} {
			fi, err := os.Stat(d)
			if err != nil {
				return fmt.Errorf("plugin-validator: %w", err)
			}
			if pvWritableBy(fi, o.pluginUID, o.pluginGID) {
				return fmt.Errorf("plugin-validator: %s is writable by the plugin's user (uid %d): it could replace the results; the spool must be this command's (0755)", d, o.pluginUID)
			}
		}
		// Whatever an earlier start left running as that user goes first.
		if err := pvSweep(o); err != nil {
			return fmt.Errorf("plugin-validator: %w", err)
		}
	}
	o.log.Info("plugin-validator: ready", slog.String("spool", o.spool), slog.String("filex", version.String()),
		slog.String("platform", pvPlatform()), slog.Int("plugin_uid", o.pluginUID), slog.Int("store_uid", o.storeUID),
		slog.String("work_dir", o.workRoot))
	// The heartbeat beats on its own: a run takes minutes, and the store
	// takes a validator whose heartbeat is older than that as gone.
	var busy atomic.Value
	busy.Store("")
	var beatMu sync.Mutex
	beat := func() {
		beatMu.Lock()
		defer beatMu.Unlock()
		pvBeat(o, busy.Load().(string))
	}
	beat()
	if !o.once {
		go func() {
			t := time.NewTicker(5 * time.Second)
			defer t.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-t.C:
					beat()
				}
			}
		}()
	}
	done := map[string]bool{}
	for {
		for _, id := range pvWaiting(in, done) {
			done[id] = true
			busy.Store(id)
			beat()
			res := pvRun(ctx, o, id)
			busy.Store("")
			beat()
			if err := pvWriteJSON(filepath.Join(out, id+".json"), res); err != nil {
				o.log.Warn("plugin-validator: the result could not be written", slog.String("job", id), slog.Any("err", err))
			}
			o.log.Info("plugin-validator: job done", slog.String("job", id), slog.Bool("ok", res.OK), slog.String("code", res.Code))
			if ctx.Err() != nil {
				return nil
			}
		}
		pvPrune(out, in, done)
		if o.once {
			return nil
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(time.Second):
		}
	}
}

// pvWaiting lists the job ids in in/ not yet run, oldest name first. A file
// that is not a regular file, or (with --store-uid) not the store's, is
// skipped for good.
func pvWaiting(in string, done map[string]bool) []string {
	entries, err := os.ReadDir(in)
	if err != nil {
		return nil
	}
	var ids []string
	for _, e := range entries {
		id, ok := strings.CutSuffix(e.Name(), ".json")
		if !ok || done[id] || !pvIDRe.MatchString(id) {
			continue
		}
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// pvBeat writes the heartbeat (busy: the job being run).
func pvBeat(o pvOptions, busy string) {
	st := pvStatus{Protocol: pvProtocol, FilexVersion: version.String(), Platform: pvPlatform(), HeartbeatAt: time.Now().UTC(), Busy: busy}
	if err := pvWriteJSON(filepath.Join(o.spool, "status.json"), st); err != nil {
		o.log.Warn("plugin-validator: the heartbeat could not be written", slog.Any("err", err))
	}
}

// pvRun runs one job and answers its result, whatever happened.
func pvRun(ctx context.Context, o pvOptions, id string) *pvResult {
	res := &pvResult{Protocol: pvProtocol, ID: id, FilexVersion: version.String(), Platform: pvPlatform(), StartedAt: time.Now().UTC()}
	fail := func(code, format string, a ...any) *pvResult {
		res.OK, res.Code, res.Message, res.EndedAt = false, code, fmt.Sprintf(format, a...), time.Now().UTC()
		return res
	}
	in := filepath.Join(o.spool, "in")
	jobPath := filepath.Join(in, id+".json")
	binPath := filepath.Join(in, id+".bin")
	for _, p := range []string{jobPath, binPath} {
		fi, err := os.Lstat(p)
		if err != nil {
			return fail("job_invalid", "the job's files are not both there")
		}
		if !fi.Mode().IsRegular() {
			return fail("job_invalid", "a job file is not a regular file")
		}
		if o.storeUID >= 0 {
			if uid, ok := pvOwner(fi); ok && uid != o.storeUID {
				return fail("job_invalid", "a job file was not written by the store's user")
			}
		}
	}
	jb, err := pvReadSmall(jobPath, 64<<10)
	if err != nil {
		return fail("job_invalid", "the job does not read: %v", err)
	}
	var job pvJob
	if err := json.Unmarshal(jb, &job); err != nil || job.Protocol != pvProtocol || job.ID != id {
		return fail("job_invalid", "the job is not one this validator reads")
	}
	want := strings.ToLower(strings.TrimSpace(job.SHA256))
	if len(want) != 64 || strings.Trim(want, "0123456789abcdef") != "" {
		return fail("job_invalid", "the job pins no sha256")
	}
	res.SHA256 = want

	// ⚠⚠ Nothing of an earlier run may still be running as the plugin's
	// user: it would reach this run's socket directory, or its build.
	if o.pluginUID > 0 {
		if err := pvSweep(o); err != nil {
			return fail("validator_error", "%v", err)
		}
	}
	work, err := os.MkdirTemp(o.workRoot, "fxpv-")
	if err != nil {
		return fail("validator_error", "%v", err)
	}
	defer func() { _ = os.RemoveAll(work) }()
	if o.pluginUID > 0 {
		// Deferred after the removal, so it runs before it: what the plugin
		// started dies before its directory goes. A run that leaves a process
		// nobody could kill did not pass.
		defer func() {
			if err := pvSweep(o); err != nil {
				res.OK, res.Code, res.Message, res.EndedAt = false, "validator_error", err.Error(), time.Now().UTC()
			}
		}()
	}
	name := "plugin"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	bin := filepath.Join(work, name)
	got, err := pvCopyHashed(binPath, bin, o.maxBinary)
	if err != nil {
		return fail("job_invalid", "the build could not be copied: %v", err)
	}
	if got != want {
		res.SHA256 = got
		return fail("sha256_mismatch", "the build handed over is %s, the job pins %s", got, want)
	}
	// ⚠⚠ The run's directory and the build stay this command's: the plugin
	// may enter the one and start the other, never replace either. Its only
	// directory of its own is run/, where it creates its socket.
	if err := os.Chmod(bin, 0o555); err != nil {
		return fail("validator_error", "%v", err)
	}
	if err := os.Chmod(work, 0o711); err != nil {
		return fail("validator_error", "%v", err)
	}
	runDir := filepath.Join(work, "run")
	if err := os.Mkdir(runDir, 0o700); err != nil {
		return fail("validator_error", "%v", err)
	}
	var tune func(*exec.Cmd)
	if o.pluginUID >= 0 {
		if err := pvChown(runDir, o.pluginUID, o.pluginGID); err != nil {
			return fail("validator_error", "the run's socket directory could not be handed to the plugin's user: %v", err)
		}
		tune = pvRunAs(o.pluginUID, o.pluginGID)
	}
	pluginName := "plugin"
	if plugin.ValidName(job.Name) {
		pluginName = job.Name
	}
	jctx, cancel := context.WithTimeout(ctx, o.jobTime)
	defer cancel()
	opts := plugin.CheckOptions{Binary: bin, WorkDir: work, Name: pluginName, Log: o.log}
	if tune != nil {
		opts.Tune = tune
	}
	r := plugin.CheckBinary(jctx, opts)
	res.OK, res.Code, res.Message, res.Describe, res.Conformance = r.OK, r.Code, r.Message, r.Describe, r.Conformance
	res.EndedAt = time.Now().UTC()
	return res
}

// pvPrepareWorkRoot makes dir the runs' parent: this command's own, a
// directory (not a link), not shared, and 0711 - the plugin's user can pass
// through it to its run, never list it (the other runs' names) or create in
// it.
func pvPrepareWorkRoot(dir string) error {
	if err := os.MkdirAll(dir, 0o711); err != nil {
		return err
	}
	fi, err := os.Lstat(dir)
	if err != nil {
		return err
	}
	if !fi.IsDir() {
		return fmt.Errorf("%s is not a directory", dir)
	}
	if pvCredentialsSupported {
		if uid, ok := pvOwner(fi); ok && uid != os.Geteuid() {
			return fmt.Errorf("%s belongs to uid %d, not to this command's user", dir, uid)
		}
		// ⚠ Never a shared directory: chmod-ing /tmp to 0711 would take it
		// from everyone else, and leaving it 1777 lets anyone list the runs.
		if fi.Mode()&(os.ModeSticky|0o002) != 0 {
			return fmt.Errorf("%s is a shared directory (anyone may create in it); give this command a directory of its own, mode 0711", dir)
		}
	}
	return os.Chmod(dir, 0o711)
}

// pvCopyHashed copies src to dst (0700, written by this process) while
// hashing it, at most max bytes; what is checked is what will run.
func pvCopyHashed(src, dst string, max int64) (string, error) {
	f, err := os.Open(src)
	if err != nil {
		return "", err
	}
	defer f.Close()
	w, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o700)
	if err != nil {
		return "", err
	}
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(w, h), io.LimitReader(f, max+1))
	if cerr := w.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return "", err
	}
	if n > max {
		return "", fmt.Errorf("larger than %d MiB", max>>20)
	}
	if n == 0 {
		return "", errors.New("empty")
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func pvReadSmall(path string, max int64) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, max+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > max {
		return nil, fmt.Errorf("larger than %d KiB", max>>10)
	}
	return b, nil
}

// pvWriteJSON writes v to a temporary name beside path and renames it into
// place, 0644: the store reads it as another user.
func pvWriteJSON(path string, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	tmp := path + ".partial"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

// pvPrune removes results older than an hour (the store reads a result
// within seconds), and forgets a run job once the store removed its files.
func pvPrune(out, in string, done map[string]bool) {
	if entries, err := os.ReadDir(out); err == nil {
		for _, e := range entries {
			if fi, err := e.Info(); err == nil && time.Since(fi.ModTime()) > time.Hour {
				_ = os.Remove(filepath.Join(out, e.Name()))
			}
		}
	}
	for id := range done {
		if _, err := os.Lstat(filepath.Join(in, id+".json")); errors.Is(err, os.ErrNotExist) {
			delete(done, id)
		}
	}
}
