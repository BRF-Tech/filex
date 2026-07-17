// Package antivirus wraps the optional ClamAV binaries (clamdscan /
// clamscan) behind a small Scan API ("Koru" v0.4).
//
// Binary resolution mirrors the OCR pattern (internal/search/extract
// TesseractBin): FILEX_CLAMAV=0 is the kill-switch, FILEX_CLAMAV_BIN is
// authoritative when set (an invalid value disables scanning rather than
// silently falling back), otherwise $PATH is searched for clamdscan
// first (daemon-backed, fast) then clamscan. Resolution lives ONLY here
// and is shared with internal/capability so the advertised `antivirus`
// flag and the actual scan pipeline can never disagree.
//
// Scanning is exec-based — no cgo, no new Go dependency. The reader is
// spooled to a temp file because both ClamAV CLIs want a path argument.
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
	"strconv"
	"strings"
	"time"
)

// DefaultTimeout bounds a single scan invocation. 60s is generous for the
// worker context — the scan is async and never sits on an upload response.
const DefaultTimeout = 60 * time.Second

// DefaultMaxScanBytes is the source-size ceiling for scanning when
// FILEX_CLAMAV_MAX is unset (100 MiB — matches clamd's own MaxFileSize
// default; bigger files are skipped, not failed).
const DefaultMaxScanBytes int64 = 100 << 20

// ErrUnavailable is returned by Scan when no ClamAV binary is configured.
var ErrUnavailable = errors.New("antivirus: no clamav binary available")

// ResolveBin resolves the ClamAV binary path. FILEX_CLAMAV=0/false is the
// kill-switch (always ""). FILEX_CLAMAV_BIN, when set, is authoritative
// (invalid value → ""); otherwise $PATH is searched for clamdscan, then
// clamscan. Empty string means scanning is unavailable.
func ResolveBin() string {
	if v := os.Getenv("FILEX_CLAMAV"); v == "0" || strings.EqualFold(v, "false") {
		return ""
	}
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

// MaxScanBytes returns the configured scan-size ceiling
// (FILEX_CLAMAV_MAX, bytes). <=0 / unset falls back to DefaultMaxScanBytes.
func MaxScanBytes() int64 {
	if v := os.Getenv("FILEX_CLAMAV_MAX"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil && n > 0 {
			return n
		}
	}
	return DefaultMaxScanBytes
}

// Scanner runs ClamAV scans through one resolved binary.
type Scanner struct {
	bin     string
	timeout time.Duration
}

// New resolves the binary from the environment (ResolveBin). The returned
// Scanner is usable even when no binary was found — Supports() reports
// false and Scan returns ErrUnavailable.
func New() *Scanner { return NewWithBin(ResolveBin()) }

// NewWithBin constructs a Scanner around an explicit binary path (tests /
// custom wiring). Empty bin means unavailable.
func NewWithBin(bin string) *Scanner {
	return &Scanner{bin: bin, timeout: DefaultTimeout}
}

// SetTimeout overrides the per-scan timeout. <=0 keeps the default.
func (s *Scanner) SetTimeout(d time.Duration) {
	if s != nil && d > 0 {
		s.timeout = d
	}
}

// Supports reports whether a ClamAV binary is available.
func (s *Scanner) Supports() bool { return s != nil && s.bin != "" }

// Bin returns the resolved binary path ("" when unavailable).
func (s *Scanner) Bin() string {
	if s == nil {
		return ""
	}
	return s.bin
}

// BinName returns the resolved binary's base name ("clamscan",
// "clamdscan", "" …) — the shape the protection API contract advertises.
func (s *Scanner) BinName() string {
	if s == nil || s.bin == "" {
		return ""
	}
	return strings.TrimSuffix(filepath.Base(s.bin), filepath.Ext(filepath.Base(s.bin)))
}

// Scan spools r to a temp file and runs the ClamAV binary over it.
//
// ClamAV exit codes: 0 = clean, 1 = infected, anything else = scan error.
// On infection the signature name is parsed from the "<path>: <SIG> FOUND"
// stdout line. The temp file is always removed.
func (s *Scanner) Scan(ctx context.Context, r io.Reader) (infected bool, signature string, err error) {
	if !s.Supports() {
		return false, "", ErrUnavailable
	}
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
	if strings.HasPrefix(filepath.Base(s.bin), "clamdscan") {
		// Hand the fd to clamd so the daemon can read the temp file even
		// when it runs as a different user.
		args = append(args, "--fdpass")
	}
	args = append(args, tmpName)
	cmd := exec.CommandContext(cctx, s.bin, args...)
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
		filepath.Base(s.bin), runErr, firstLine(out.String()))
}

// parseSignature extracts the signature name from ClamAV's infected
// output line: "<path>: <SignatureName> FOUND".
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
