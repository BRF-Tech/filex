// Package antivirus wraps ClamAV behind a small Scan API ("Koru" v0.4).
//
// # Two ways to reach ClamAV
//
//	binary  — exec a scanner on filex's own $PATH (clamdscan / clamscan).
//	daemon  — speak clamd's protocol over TCP or a unix socket (clamd.go).
//
// Which one is in force is a SETTING, not a guess: see settings.go. Daemon
// mode exists because in Docker and podman deployments ClamAV is its own
// container (`clamav/clamav`, port 3310) and `clamav:3310` is the natural
// configuration — the alternative being a ~1 GB scanner and its signature
// database baked into the filex image.
//
// # One place, on purpose
//
// Resolution lives ONLY in settings.go (Resolve) and is shared with
// internal/capability and the admin protection handler, so the advertised
// `antivirus` flag, the state the admin page shows and the actual scan
// pipeline can never disagree. Anything that wants to know whether scanning is
// on asks Resolve; nothing re-derives it.
//
// # Unavailable is not clean
//
// ⚠⚠ Every failure path here returns an ERROR. None of them returns
// (false, "", nil). A scanner that cannot reach ClamAV and reports every file
// clean is worse than no scanner at all: it produces the same green tick as a
// real pass, so the failure is invisible exactly where it matters. The queue
// job turns a scan error into a failed op that retries; it turns "clean" into
// a file nobody will look at again.
package antivirus

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/brf-tech/filex/backend/internal/dbsetting"
)

// DefaultTimeout bounds a single scan invocation. 60s is generous for the
// worker context — the scan is async and never sits on an upload response.
const DefaultTimeout = 60 * time.Second

// healthTimeout bounds the admin-facing reachability probe. It is short on
// purpose: an admin is watching a page render, and "clamd did not answer in
// three seconds" is the answer they need, not something to wait a minute for.
const healthTimeout = 3 * time.Second

// DefaultMaxScanBytes is the source-size ceiling for scanning when nothing
// has been configured (100 MiB — matches clamd's own MaxFileSize default;
// bigger files are skipped, not failed).
//
// ⚠ The value itself now lives in the settings table, not the environment:
// see MaxScanSetting / MaxScanBytesFrom in maxscan.go. This constant remains
// the floor everything falls back to when there is no store to ask.
const DefaultMaxScanBytes int64 = DefaultMaxScanMB * bytesPerMB

// ErrUnavailable is returned by Scan when scanning is switched off or no
// ClamAV — binary or daemon — is configured.
var ErrUnavailable = errors.New("antivirus: no clamav binary or daemon available")

// ResolveBin resolves the ClamAV EXECUTABLE for binary mode. FILEX_CLAMAV_BIN,
// when set, is authoritative (an invalid value disables scanning rather than
// silently falling back to something else on $PATH); otherwise $PATH is
// searched for clamdscan, then clamscan. Empty string means no binary.
//
// ⚠ It no longer consults FILEX_CLAMAV. That variable is now the SEED for the
// antivirus.enabled row (settings.go), and reading it here as well would let a
// stale compose entry silently veto a switch an admin had turned on in the UI
// — the exact disagreement between advertised state and real pipeline this
// package exists to prevent. The kill-switch is applied once, in Resolve.
func ResolveBin() string {
	if bin := os.Getenv("FILEX_CLAMAV_BIN"); bin != "" {
		if p, err := exec.LookPath(bin); err == nil {
			return p
		}
		return ""
	}
	for _, name := range []string{"clamdscan", "clamscan"} {
		if p, err := exec.LookPath(name); err == nil {
			return p
		}
	}
	return ""
}

// Scanner runs ClamAV scans through one resolved transport.
type Scanner struct {
	res     Resolution
	timeout time.Duration
}

// New resolves the scanner from the settings table, falling back to the
// environment when g is nil (see Resolve). The returned Scanner is usable even
// when nothing resolved — Supports() reports false and Scan returns
// ErrUnavailable.
func New(ctx context.Context, g dbsetting.Getter) *Scanner {
	return NewWithResolution(Resolve(ctx, g))
}

// NewWithResolution wraps an already-resolved configuration. Used by callers
// that resolved once and want both the Scanner and the Resolution (the admin
// handler needs the second to explain the first).
func NewWithResolution(res Resolution) *Scanner {
	return &Scanner{res: res, timeout: DefaultTimeout}
}

// NewWithBin constructs a Scanner around an explicit binary path (tests /
// custom wiring). Empty bin means unavailable.
func NewWithBin(bin string) *Scanner {
	return NewWithResolution(Resolution{Enabled: bin != "", Mode: ModeBinary, Bin: bin})
}

// NewWithDaemon constructs a Scanner around an explicit clamd address (tests /
// custom wiring). An unparseable address is an error here rather than a
// Scanner that fails later.
func NewWithDaemon(addr string) (*Scanner, error) {
	network, address, err := ParseAddr(addr)
	if err != nil {
		return nil, err
	}
	return NewWithResolution(Resolution{
		Enabled: true, Mode: ModeDaemon, Addr: addr,
		Network: network, Address: address,
	}), nil
}

// SetTimeout overrides the per-scan timeout. <=0 keeps the default.
func (s *Scanner) SetTimeout(d time.Duration) {
	if s != nil && d > 0 {
		s.timeout = d
	}
}

// Supports reports whether a scan can be attempted (see Resolution.Available:
// configured, not necessarily reachable).
func (s *Scanner) Supports() bool { return s != nil && s.res.Available() }

// Resolution returns the configuration this Scanner was built from.
func (s *Scanner) Resolution() Resolution {
	if s == nil {
		return Resolution{}
	}
	return s.res
}

// Mode reports the transport in force ("binary", "daemon", or "" when
// unavailable).
func (s *Scanner) Mode() string {
	if !s.Supports() {
		return ""
	}
	return s.res.Mode
}

// Address returns the clamd address as configured, "" in binary mode.
func (s *Scanner) Address() string {
	if s == nil {
		return ""
	}
	return s.res.Addr
}

// Bin returns the resolved binary path ("" in daemon mode / when unavailable).
func (s *Scanner) Bin() string {
	if s == nil {
		return ""
	}
	return s.res.Bin
}

// BinName names what actually answers a scan: "clamscan" or "clamdscan" in
// binary mode, "clamd" in daemon mode, "" when unavailable.
//
// ⚠ "clamd" extends the enum the protection API used to advertise
// ("clamscan|clamdscan|"). It is deliberately non-empty: an admin looking at
// an enabled scanner with a blank binary field cannot tell a working daemon
// from a broken probe, and the mode/address fields beside it say the rest.
func (s *Scanner) BinName() string {
	if !s.Supports() {
		return ""
	}
	if s.res.Mode == ModeDaemon {
		return "clamd"
	}
	base := filepath.Base(s.res.Bin)
	return strings.TrimSuffix(base, filepath.Ext(base))
}

// Health answers "would a scan get through right now?", which Supports()
// deliberately does not.
//
// In daemon mode it dials and runs PING/PONG: a dial alone only proves
// something is listening, while PONG proves it is clamd and that it is past
// loading its signature database (a fresh clamav container refuses commands
// for the first minute or two, and "starting up" must not read as "healthy").
// In binary mode it re-stats the executable. Unavailable is an error naming
// why, so the admin page can show it verbatim.
func (s *Scanner) Health(ctx context.Context) error {
	if s == nil {
		return ErrUnavailable
	}
	if s.res.Err != nil {
		return s.res.Err
	}
	if !s.res.Enabled {
		return ErrUnavailable
	}
	if s.res.Mode == ModeDaemon {
		if s.res.Address == "" {
			return ErrNoDaemonAddress
		}
		return clamdPing(ctx, s.res.Network, s.res.Address, healthTimeout)
	}
	if s.res.Bin == "" {
		return ErrUnavailable
	}
	if _, err := os.Stat(s.res.Bin); err != nil {
		return fmt.Errorf("antivirus: %s is no longer executable: %w", s.res.Bin, err)
	}
	return nil
}

// Version reports what answered, for the admin surface: clamd's VERSION reply
// in daemon mode, the binary's name in binary mode. Best-effort — an error
// here is decoration missing, not a fault.
func (s *Scanner) Version(ctx context.Context) (string, error) {
	if !s.Supports() {
		return "", ErrUnavailable
	}
	if s.res.Mode == ModeDaemon {
		return clamdVersion(ctx, s.res.Network, s.res.Address, healthTimeout)
	}
	return s.BinName(), nil
}

// Scan runs r's bytes past ClamAV and reports the verdict.
//
// In daemon mode the bytes are streamed to clamd with INSTREAM: nothing is
// written to disk, and filex and clamd do not need a shared filesystem — which
// is the whole point of the mode, since a split into two containers is exactly
// where a temp-file handoff stops working.
//
// In binary mode r is spooled to a temp file because both ClamAV CLIs want a
// path argument; the file is always removed.
func (s *Scanner) Scan(ctx context.Context, r io.Reader) (infected bool, signature string, err error) {
	if !s.Supports() {
		if s != nil && s.res.Err != nil {
			return false, "", s.res.Err
		}
		return false, "", ErrUnavailable
	}
	if s.res.Mode == ModeDaemon {
		return clamdScan(ctx, s.res.Network, s.res.Address, s.timeout, r)
	}
	return s.scanExec(ctx, r)
}

// scanExec is the original exec path.
//
// ClamAV exit codes: 0 = clean, 1 = infected, anything else = scan error.
// On infection the signature name is parsed from the "<path>: <SIG> FOUND"
// stdout line. The temp file is always removed.
func (s *Scanner) scanExec(ctx context.Context, r io.Reader) (bool, string, error) {
	tmp, err := os.CreateTemp("", "filex-av-*")
	if err != nil {
		return false, "", fmt.Errorf("antivirus: temp file: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := io.Copy(tmp, r); err != nil {
		tmp.Close()
		return false, "", fmt.Errorf("antivirus: spool: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return false, "", fmt.Errorf("antivirus: spool close: %w", err)
	}

	cctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()
	args := []string{"--no-summary"}
	if strings.HasPrefix(filepath.Base(s.res.Bin), "clamdscan") {
		// Hand the fd to clamd so the daemon can read the temp file even
		// when it runs as a different user.
		args = append(args, "--fdpass")
	}
	args = append(args, tmpName)
	cmd := exec.CommandContext(cctx, s.res.Bin, args...)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out

	runErr := cmd.Run()
	if runErr == nil {
		return false, "", nil
	}
	var ee *exec.ExitError
	if errors.As(runErr, &ee) && ee.ExitCode() == 1 {
		return true, parseSignature(out.String()), nil
	}
	if cctx.Err() != nil {
		return false, "", fmt.Errorf("antivirus: scan timed out after %s", s.timeout)
	}
	return false, "", fmt.Errorf("antivirus: %s failed: %v: %s",
		filepath.Base(s.res.Bin), runErr, firstLine(out.String()))
}

// parseSignature extracts the signature name from ClamAV's infected
// output line: "<path>: <SignatureName> FOUND". The daemon's reply has the
// same shape with "stream" in place of the path, so both paths share it.
func parseSignature(out string) string {
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasSuffix(line, " FOUND") {
			continue
		}
		line = strings.TrimSuffix(line, " FOUND")
		if i := strings.LastIndex(line, ": "); i >= 0 {
			line = line[i+2:]
		}
		if line = strings.TrimSpace(line); line != "" {
			return line
		}
	}
	return "unknown"
}

// firstLine truncates multi-line tool output for error messages.
func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	return s
}

// ---------------------------------------------------------------------------
// What this process actually booted with.
//
// The enabled/mode/address settings take effect at the next restart, so the
// admin page has to be able to say "restart required" — and, more importantly,
// to STOP saying it once the restart has happened. That needs the value the
// running process wired itself with, which no row can supply: the row is the
// desired state and the entire point is that the two can differ.
//
// It is package state rather than a field threaded through the router because
// there is exactly one scan pipeline per process, and threading it would mean
// touching the wiring of every handler that never asks.
// ---------------------------------------------------------------------------

var (
	bootMu   sync.RWMutex
	bootRes  Resolution
	bootSeen bool
)

// SetBoot records the resolution the scan pipeline was actually wired with.
// Called once, from internal/server, at the point the wiring is decided.
func SetBoot(res Resolution) {
	bootMu.Lock()
	defer bootMu.Unlock()
	bootRes, bootSeen = res, true
}

// Boot returns what SetBoot recorded. ok is false before boot wiring has run
// (unit tests, CLI), in which case callers must not claim a restart is
// pending — an unknown boot state is not evidence of a difference.
func Boot() (res Resolution, ok bool) {
	bootMu.RLock()
	defer bootMu.RUnlock()
	return bootRes, bootSeen
}

// RestartPending reports whether the stored configuration differs from what
// this process is running, i.e. whether restarting would change behaviour.
//
// It compares only the three deferred settings. The live ones (scan ceiling,
// save window) are read per use and can never be pending.
func RestartPending(want Resolution) bool {
	got, ok := Boot()
	if !ok {
		return false
	}
	if want.Enabled != got.Enabled {
		return true
	}
	if !want.Enabled {
		// Off in both: how it would have connected is moot.
		return false
	}
	return want.Mode != got.Mode || want.Addr != got.Addr || want.Bin != got.Bin
}
