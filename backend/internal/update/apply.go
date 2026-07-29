package update

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// ErrNotSelfApplicable is returned when the install cannot replace itself —
// the container case. It is a permanent condition, not a transient failure.
var ErrNotSelfApplicable = errors.New("this install cannot upgrade itself (container image is immutable); upgrade the image instead")

// maxBinaryBytes caps the download. filex binaries are ~30 MB; 256 MB is a
// generous ceiling that still refuses an endless stream.
const maxBinaryBytes = 256 << 20

// PreApply, when set, runs immediately before the binary is swapped — this is
// where a database snapshot is taken. A non-nil error aborts the upgrade with
// nothing changed, which is the point: no backup, no upgrade.
type PreApply func(ctx context.Context, target Release) error

// SetPreApply installs the pre-upgrade hook.
func (s *Service) SetPreApply(fn PreApply) { s.preApply = fn }

// Apply downloads, verifies and installs the target release, then asks the
// supervisor to restart. The sequence is deliberately ordered so that every
// failure mode leaves a WORKING install behind:
//
//  1. refuse outright on container installs (ghost-upgrade trap)
//  2. download + SHA-256 verify (against the manifest, over TLS)
//  3. unpack to a temp file NEXT TO the current binary (same filesystem, so
//     the final move is atomic rather than a cross-device copy)
//  4. smoke-test the new binary by running `filex --version` — catches a
//     truncated download, wrong architecture, or a corrupt archive
//  5. PreApply hook (database snapshot)
//  6. keep the old binary as filex.bak-<version>, then rename into place
//  7. restart if a supervisor is detected; otherwise report "restart required"
//
// Everything before step 6 is reversible by doing nothing.
func (s *Service) Apply(ctx context.Context, target Release) error {
	if !s.mode.CanSelfApply() {
		return ErrNotSelfApplicable
	}
	asset, ok := target.AssetFor(runtime.GOOS, runtime.GOARCH)
	if !ok {
		return fmt.Errorf("release %s has no build for %s/%s", target.Version, runtime.GOOS, runtime.GOARCH)
	}
	if asset.SHA256 == "" {
		return fmt.Errorf("release %s asset has no sha256 — refusing to install unverified bytes", target.Version)
	}

	exePath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("cannot locate running binary: %w", err)
	}
	exePath, _ = filepath.EvalSymlinks(exePath)
	dir := filepath.Dir(exePath)

	archive, err := s.download(ctx, asset)
	if err != nil {
		s.recordApplyError(err)
		return err
	}
	bin, err := extractBinary(archive, asset)
	if err != nil {
		s.recordApplyError(err)
		return err
	}

	tmp := filepath.Join(dir, ".filex-update-"+target.Version)
	if err := os.WriteFile(tmp, bin, 0o755); err != nil {
		s.recordApplyError(err)
		return fmt.Errorf("cannot stage new binary in %s: %w", dir, err)
	}
	defer os.Remove(tmp) // no-op once renamed

	if err := smokeTest(ctx, tmp, target.Version); err != nil {
		s.recordApplyError(err)
		return err
	}

	if s.preApply != nil {
		if err := s.preApply(ctx, target); err != nil {
			s.recordApplyError(err)
			return fmt.Errorf("pre-upgrade step failed, nothing was changed: %w", err)
		}
	}

	backup := filepath.Join(dir, filepath.Base(exePath)+".bak-"+s.cfg.CurrentVersion)
	if err := copyFile(exePath, backup); err != nil {
		slog.Warn("could not keep a backup of the current binary", "err", err)
	}
	if err := os.Rename(tmp, exePath); err != nil {
		s.recordApplyError(err)
		return fmt.Errorf("cannot replace binary (restore from %s if needed): %w", backup, err)
	}

	s.mu.Lock()
	s.state.LastApplied = target.Version
	s.state.LastAppliedAt = time.Now()
	s.state.LastApplyError = ""
	st := s.state
	s.mu.Unlock()
	s.saveState(st)

	slog.Info("filex upgraded on disk", "from", s.cfg.CurrentVersion, "to", target.Version, "backup", backup)
	return s.restart(ctx)
}

// RestartRequired reports whether a new binary is installed but the running
// process is still the old one — surfaced in the admin UI so the state is
// never invisible.
func (s *Service) RestartRequired() bool {
	st := s.State()
	return st.LastApplied != "" && st.LastApplied != s.cfg.CurrentVersion
}

// restart hands over to the supervisor. Only systemd is driven directly; for
// anything else filex reports the requirement instead of guessing, because a
// process that exits under an unknown supervisor may simply stay down.
func (s *Service) restart(ctx context.Context) error {
	unit := systemdUnit()
	if unit == "" {
		slog.Info("new version installed — restart filex to activate it")
		return nil
	}
	slog.Info("restarting via systemd", "unit", unit)
	cmd := exec.CommandContext(ctx, "systemctl", "restart", unit)
	if out, err := cmd.CombinedOutput(); err != nil {
		slog.Warn("systemctl restart failed — restart manually", "unit", unit, "err", err, "out", string(out))
	}
	return nil
}

// systemdUnit returns this process's systemd unit, or "" when not running
// under systemd. INVOCATION_ID is set by systemd for every unit it starts.
func systemdUnit() string {
	if os.Getenv("INVOCATION_ID") == "" {
		return ""
	}
	if u := os.Getenv("FILEX_SYSTEMD_UNIT"); u != "" {
		return u
	}
	b, err := os.ReadFile("/proc/self/cgroup")
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(b), "\n") {
		if i := strings.Index(line, ".service"); i > 0 {
			seg := line[:i]
			if j := strings.LastIndexAny(seg, "/:"); j >= 0 {
				seg = seg[j+1:]
			}
			return seg + ".service"
		}
	}
	return ""
}

func (s *Service) download(ctx context.Context, a Asset) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, a.URL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", userAgent(s.cfg.CurrentVersion))
	client := s.client
	if client == nil {
		client = &http.Client{}
	}
	// Downloads are large; give them their own generous deadline instead of
	// the short one used for the manifest.
	dl := *client
	dl.Timeout = 15 * time.Minute
	resp, err := dl.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("download %s: HTTP %d", a.URL, resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBinaryBytes))
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(body)
	got := hex.EncodeToString(sum[:])
	if !strings.EqualFold(got, a.SHA256) {
		return nil, fmt.Errorf("checksum mismatch for %s: manifest says %s, download is %s", a.URL, a.SHA256, got)
	}
	return body, nil
}

// extractBinary pulls the filex executable out of a .tar.gz or .zip archive.
func extractBinary(archive []byte, a Asset) ([]byte, error) {
	want := a.Binary
	if want == "" {
		want = "filex"
		if strings.EqualFold(a.OS, "windows") {
			want = "filex.exe"
		}
	}
	if strings.HasSuffix(strings.ToLower(a.URL), ".zip") {
		return extractZip(archive, want)
	}
	return extractTarGz(archive, want)
}

func extractTarGz(archive []byte, want string) ([]byte, error) {
	gz, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		return nil, fmt.Errorf("archive is not gzip: %w", err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		if h.Typeflag != tar.TypeReg {
			continue
		}
		if filepath.Base(h.Name) != want {
			continue
		}
		return io.ReadAll(io.LimitReader(tr, maxBinaryBytes))
	}
	return nil, fmt.Errorf("archive does not contain %q", want)
}

func extractZip(archive []byte, want string) ([]byte, error) {
	zr, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
	if err != nil {
		return nil, fmt.Errorf("archive is not a zip: %w", err)
	}
	for _, f := range zr.File {
		if filepath.Base(f.Name) != want {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return nil, err
		}
		defer rc.Close()
		return io.ReadAll(io.LimitReader(rc, maxBinaryBytes))
	}
	return nil, fmt.Errorf("archive does not contain %q", want)
}

// smokeTest runs the freshly unpacked binary with --version. A binary that
// cannot even print its version will not serve traffic either, and finding
// that out BEFORE the swap is the difference between a no-op and an outage.
func smokeTest(ctx context.Context, path, wantVersion string) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, path, "--version").CombinedOutput()
	if err != nil {
		return fmt.Errorf("new binary failed its smoke test (%w): %s", err, strings.TrimSpace(string(out)))
	}
	got := strings.TrimSpace(string(out))
	trimmed := strings.TrimPrefix(wantVersion, "v")
	if !strings.Contains(got, trimmed) {
		return fmt.Errorf("new binary reports %q, expected %s — refusing to install", got, wantVersion)
	}
	return nil
}

func copyFile(src, dst string) error {
	b, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, b, 0o755)
}

func (s *Service) recordApplyError(err error) {
	s.mu.Lock()
	s.state.LastApplyError = err.Error()
	st := s.state
	s.mu.Unlock()
	s.saveState(st)
}
