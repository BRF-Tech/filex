// Package archivecli runs operator-provided archive tools behind a small,
// fixed interface. It never evaluates a shell command: provider arguments are
// assembled here and the executable path comes only from process config.
package archivecli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	SettingEnabled          = "archive.enabled"
	SettingDefaultFormat    = "archive.default_format"
	SettingAllowedFormats   = "archive.allowed_formats"
	SettingMaxEntries       = "archive.max_entries"
	SettingMaxExpandedBytes = "archive.max_expanded_bytes"
	SettingTimeoutSeconds   = "archive.timeout_seconds"

	DefaultMaxEntries       = 20_000
	DefaultMaxExpandedBytes = int64(20 << 30)
	DefaultTimeout          = 30 * time.Minute
)

var (
	ErrUnavailable      = errors.New("archive provider unavailable")
	ErrPasswordRequired = errors.New("archive password required")
	ErrBadPassword      = errors.New("incorrect archive password")
	ErrUnsupported      = errors.New("unsupported archive format")
	ErrLimits           = errors.New("archive exceeds configured limits")
)

// SettingsStore is the deliberately tiny settings-table surface used here.
type SettingsStore interface {
	GetSetting(context.Context, string) (string, error)
	UpsertSetting(context.Context, string, string) error
}

// Config contains the operator-owned settings. Binary is never writable over
// HTTP because changing it is equivalent to allowing arbitrary code execution.
type Config struct {
	SevenZipBin string
	WorkDir     string
}

// CreateOptions is the fixed, validated set of switches filex may pass to an
// archive provider. Keeping this typed prevents a browser from smuggling raw
// command-line arguments into the process invocation.
type CreateOptions struct {
	Format            string
	Password          string
	EncryptNames      bool
	Compression       int
	Solid             *bool
	DictionarySizeMiB int
	Progress          func(percent int)
}

// Policy is the live, admin-writable archive policy.
type Policy struct {
	Enabled          bool     `json:"enabled"`
	DefaultFormat    string   `json:"default_format"`
	AllowedFormats   []string `json:"allowed_formats"`
	MaxEntries       int      `json:"max_entries"`
	MaxExpandedBytes int64    `json:"max_expanded_bytes"`
	TimeoutSeconds   int      `json:"timeout_seconds"`
}

// Entry is one archive member as reported by the provider.
type Entry struct {
	Name      string    `json:"name"`
	Size      int64     `json:"size"`
	Mtime     time.Time `json:"mtime"`
	IsDir     bool      `json:"is_dir"`
	Encrypted bool      `json:"encrypted,omitempty"`
	IsLink    bool      `json:"is_link,omitempty"`
}

// ProviderStatus describes an archive provider without exposing host paths.
type ProviderStatus struct {
	Name           string   `json:"name"`
	Available      bool     `json:"available"`
	Version        string   `json:"version,omitempty"`
	CreateFormats  []string `json:"create_formats"`
	ExtractFormats []string `json:"extract_formats"`
	Encrypted      bool     `json:"encrypted"`
	Error          string   `json:"error,omitempty"`
}

// Service resolves and invokes supported archive providers.
type Service struct {
	store SettingsStore
	cfg   Config
}

func New(store SettingsStore, cfg Config) *Service { return &Service{store: store, cfg: cfg} }

func (s *Service) WorkDir() string { return s.cfg.WorkDir }

func (s *Service) workspaceTemp(pattern string) (string, error) {
	base := s.cfg.WorkDir
	if base != "" {
		if err := os.MkdirAll(base, 0o700); err != nil {
			return "", err
		}
	}
	return os.MkdirTemp(base, pattern)
}

// Policy returns validated defaults when a setting is absent or malformed.
func (s *Service) Policy(ctx context.Context) Policy {
	p := Policy{
		Enabled:          true,
		DefaultFormat:    "7z",
		AllowedFormats:   []string{"zip", "7z", "tar", "tar.gz", "tar.bz2", "tar.xz"},
		MaxEntries:       DefaultMaxEntries,
		MaxExpandedBytes: DefaultMaxExpandedBytes,
		TimeoutSeconds:   int(DefaultTimeout / time.Second),
	}
	if s == nil || s.store == nil {
		return p
	}
	if v, err := s.store.GetSetting(ctx, SettingEnabled); err == nil && v != "" {
		p.Enabled = parseBool(v, true)
	}
	if v, err := s.store.GetSetting(ctx, SettingDefaultFormat); err == nil {
		if f := CanonicalFormat(v); IsCreateFormat(f) {
			p.DefaultFormat = f
		}
	}
	if v, err := s.store.GetSetting(ctx, SettingAllowedFormats); err == nil && v != "" {
		if fs := CanonicalFormats(strings.Split(v, ",")); len(fs) > 0 {
			p.AllowedFormats = fs
		}
	}
	if v, err := s.store.GetSetting(ctx, SettingMaxEntries); err == nil {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			p.MaxEntries = n
		}
	}
	if v, err := s.store.GetSetting(ctx, SettingMaxExpandedBytes); err == nil {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil && n > 0 {
			p.MaxExpandedBytes = n
		}
	}
	if v, err := s.store.GetSetting(ctx, SettingTimeoutSeconds); err == nil {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			p.TimeoutSeconds = n
		}
	}
	return p
}

func parseBool(v string, fallback bool) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	default:
		return fallback
	}
}

func CanonicalFormat(v string) string {
	v = strings.TrimPrefix(strings.ToLower(strings.TrimSpace(v)), ".")
	switch v {
	case "zip", "7z", "rar", "tar", "gz", "gzip", "bz2", "bzip2", "xz", "tar.gz", "tgz", "tar.bz2", "tbz2", "tar.xz", "txz":
		if v == "gzip" {
			return "gz"
		}
		if v == "bzip2" {
			return "bz2"
		}
		switch v {
		case "tgz":
			return "tar.gz"
		case "tbz2":
			return "tar.bz2"
		case "txz":
			return "tar.xz"
		}
		return v
	default:
		return ""
	}
}

func IsCreateFormat(format string) bool {
	switch format {
	case "zip", "7z", "tar", "tar.gz", "tar.bz2", "tar.xz":
		return true
	default:
		return false
	}
}

func CanonicalFormats(in []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(in))
	for _, raw := range in {
		f := CanonicalFormat(raw)
		if !IsCreateFormat(f) || seen[f] {
			continue
		}
		seen[f] = true
		out = append(out, f)
	}
	return out
}

func FormatFromPath(name string) string {
	lower := strings.ToLower(strings.TrimSpace(name))
	for _, suffix := range []string{".tar.gz", ".tar.bz2", ".tar.xz", ".tgz", ".tbz2", ".txz"} {
		if strings.HasSuffix(lower, suffix) {
			return CanonicalFormat(strings.TrimPrefix(suffix, "."))
		}
	}
	ext := strings.ToLower(filepath.Ext(name))
	return CanonicalFormat(ext)
}

const (
	minimumSevenZipMajor = 24
	minimumSevenZipMinor = 7
)

func minimumSevenZipVersion() string {
	return fmt.Sprintf("%d.%02d", minimumSevenZipMajor, minimumSevenZipMinor)
}

var sevenZipVersionPattern = regexp.MustCompile(`(?i)7-zip(?: \(z\)| \[[^]]+\])?\s+([0-9]{2,})\.([0-9]{2})`)

func validateSevenZipVersion(output string) (string, error) {
	match := sevenZipVersionPattern.FindStringSubmatch(output)
	if match == nil {
		return "", fmt.Errorf("%w: 7-Zip version could not be determined", ErrUnavailable)
	}
	major, _ := strconv.Atoi(match[1])
	minor, _ := strconv.Atoi(match[2])
	version := match[1] + "." + match[2]
	if major < minimumSevenZipMajor || (major == minimumSevenZipMajor && minor < minimumSevenZipMinor) {
		return version, fmt.Errorf("%w: 7-Zip %s is older than required %s", ErrUnavailable, version, minimumSevenZipVersion())
	}
	return version, nil
}

func probeSevenZipVersion(path string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, path, "i").CombinedOutput()
	if ctx.Err() != nil {
		return "", fmt.Errorf("%w: 7-Zip version probe timed out", ErrUnavailable)
	}
	if err != nil {
		return "", fmt.Errorf("%w: 7-Zip version probe failed", ErrUnavailable)
	}
	return validateSevenZipVersion(string(out))
}

func (s *Service) sevenZipPath() (string, error) {
	if s == nil {
		return "", ErrUnavailable
	}
	p := strings.TrimSpace(s.cfg.SevenZipBin)
	if p != "" {
		st, err := os.Stat(p)
		if err != nil || st.IsDir() {
			return "", fmt.Errorf("%w: configured 7-Zip binary %q is not executable", ErrUnavailable, p)
		}
	} else {
		for _, name := range []string{"7zz", "7z"} {
			if found, err := exec.LookPath(name); err == nil {
				p = found
				break
			}
		}
		if p == "" {
			return "", fmt.Errorf("%w: 7zz or 7z was not found on PATH", ErrUnavailable)
		}
	}
	if _, err := probeSevenZipVersion(p); err != nil {
		return "", err
	}
	return p, nil
}

func firstLine(s string) string {
	sc := bufio.NewScanner(strings.NewReader(strings.TrimSpace(s)))
	if sc.Scan() {
		return strings.TrimSpace(sc.Text())
	}
	return ""
}

func (s *Service) Providers(ctx context.Context) []ProviderStatus {
	out := []ProviderStatus{{
		Name: "builtin-zip", Available: true, Version: "Go archive/zip",
		CreateFormats: []string{"zip"}, ExtractFormats: []string{"zip"}, Encrypted: false,
	}}
	seven := ProviderStatus{
		Name: "sevenzip", CreateFormats: []string{"zip", "7z", "tar", "tar.gz", "tar.bz2", "tar.xz"},
		ExtractFormats: []string{"zip", "7z", "rar", "tar", "tar.gz", "tar.bz2", "tar.xz", "gz", "bz2", "xz"}, Encrypted: true,
	}
	if p, err := s.sevenZipPath(); err != nil {
		slog.Warn("archive provider unavailable", slog.String("provider", "sevenzip"), slog.String("err", err.Error()))
		seven.Error = "7-Zip " + minimumSevenZipVersion() + " or newer is unavailable"
	} else {
		seven.Available = true
		cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		b, err := exec.CommandContext(cctx, p, "i").CombinedOutput()
		cancel()
		if err == nil {
			seven.Version = firstLine(string(b))
		} else {
			slog.Warn("archive provider version probe failed", slog.String("provider", "sevenzip"), slog.String("err", err.Error()))
			seven.Error = "7-Zip version could not be queried"
		}
	}
	return append(out, seven)
}

func (s *Service) timeoutContext(ctx context.Context) (context.Context, context.CancelFunc) {
	p := s.Policy(ctx)
	return context.WithTimeout(ctx, time.Duration(p.TimeoutSeconds)*time.Second)
}

func passwordArg(password string) string {
	if password == "" {
		return ""
	}
	return "-p" + password
}

func classify(output string, err error) error {
	if err == nil {
		return nil
	}
	lower := strings.ToLower(output)
	switch {
	case strings.Contains(lower, "wrong password"), strings.Contains(lower, "incorrect password"):
		return fmt.Errorf("%w: %s", ErrBadPassword, firstLine(output))
	case strings.Contains(lower, "password") && (strings.Contains(lower, "required") || strings.Contains(lower, "enter password")):
		return fmt.Errorf("%w: %s", ErrPasswordRequired, firstLine(output))
	case strings.Contains(lower, "cannot open encrypted archive"), strings.Contains(lower, "headers error"):
		return fmt.Errorf("%w: %s", ErrPasswordRequired, firstLine(output))
	case strings.Contains(lower, "is not supported archive"), strings.Contains(lower, "cannot open the file as archive"):
		return fmt.Errorf("%w: %s", ErrUnsupported, firstLine(output))
	default:
		msg := strings.TrimSpace(output)
		if len(msg) > 2048 {
			msg = msg[len(msg)-2048:]
		}
		return fmt.Errorf("archive provider: %w: %s", err, msg)
	}
}

func classifyPassword(output, password string, err error) error {
	classified := classify(output, err)
	// 7-Zip commonly reports "Wrong password" both when a supplied password
	// is wrong and when no password was supplied at all. The distinction is
	// important to clients: the latter is discovery and should open the shared
	// password prompt, not accuse the user of entering a bad password.
	if password == "" && errors.Is(classified, ErrBadPassword) {
		return fmt.Errorf("%w: %s", ErrPasswordRequired, firstLine(output))
	}
	return classified
}

// List asks 7-Zip for technical output and parses its stable Path/Size/
// Modified/Attributes fields. The first Path block describes the archive
// itself and is omitted.
func (s *Service) List(ctx context.Context, archivePath, password string) ([]Entry, error) {
	bin, err := s.sevenZipPath()
	if err != nil {
		return nil, err
	}
	args := []string{"l", "-slt", "-ba", "-sccUTF-8", "-bd"}
	if p := passwordArg(password); p != "" {
		args = append(args, p)
	}
	args = append(args, "--", archivePath)
	cctx, cancel := s.timeoutContext(ctx)
	defer cancel()
	out, runErr := exec.CommandContext(cctx, bin, args...).CombinedOutput()
	if err := cctx.Err(); err != nil {
		return nil, err
	}
	if err := classifyPassword(string(out), password, runErr); err != nil {
		return nil, err
	}
	return parseTechnicalList(string(out), archivePath), nil
}

// ListAs handles formats made of two archive layers. 7-Zip deliberately
// expands gzip/bzip2/xz and the contained tar in separate commands; treating
// a .tar.gz like a single layer would show only "archive.tar" in preview.
func (s *Service) ListAs(ctx context.Context, archivePath, logicalName, password string) ([]Entry, error) {
	if !isCompressedTar(logicalName) {
		return s.List(ctx, archivePath, password)
	}
	root, err := s.workspaceTemp("filex-archive-list-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(root)
	if err := s.Extract(ctx, archivePath, root, password, nil); err != nil {
		return nil, err
	}
	tarPath, err := findTar(root)
	if err != nil {
		return nil, err
	}
	return s.List(ctx, tarPath, "")
}

func parseTechnicalList(out, archivePath string) []Entry {
	var entries []Entry
	cur := map[string]string{}
	flush := func() {
		name := strings.TrimSpace(cur["Path"])
		if name == "" || filepath.Clean(name) == filepath.Clean(archivePath) {
			cur = map[string]string{}
			return
		}
		size, _ := strconv.ParseInt(strings.TrimSpace(cur["Size"]), 10, 64)
		mt, _ := time.Parse("2006-01-02 15:04:05", strings.TrimSpace(cur["Modified"]))
		attrs := cur["Attributes"]
		entries = append(entries, Entry{
			Name: name, Size: size, Mtime: mt,
			IsDir:     strings.HasPrefix(attrs, "D") || strings.HasSuffix(name, "/"),
			Encrypted: strings.TrimSpace(cur["Encrypted"]) == "+",
			IsLink:    cur["Symbolic Link"] != "" || cur["Hard Link"] != "",
		})
		cur = map[string]string{}
	}
	sc := bufio.NewScanner(strings.NewReader(out))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			if len(cur) > 0 {
				flush()
			}
			continue
		}
		if i := strings.Index(line, " = "); i > 0 {
			cur[line[:i]] = line[i+3:]
		}
	}
	if len(cur) > 0 {
		flush()
	}
	return entries
}

// Test validates that every member can be decrypted without materialising it.
// Listing alone is insufficient for ZIP and non-header-encrypted 7z archives:
// 7-Zip can reveal their names without ever checking the content password.
func (s *Service) Test(ctx context.Context, archivePath, password string) error {
	bin, err := s.sevenZipPath()
	if err != nil {
		return err
	}
	args := []string{"t", "-bd", "-sccUTF-8"}
	if p := passwordArg(password); p != "" {
		args = append(args, p)
	}
	args = append(args, "--", archivePath)
	cctx, cancel := s.timeoutContext(ctx)
	defer cancel()
	out, runErr := exec.CommandContext(cctx, bin, args...).CombinedOutput()
	if err := cctx.Err(); err != nil {
		return err
	}
	return classifyPassword(string(out), password, runErr)
}

// Extract expands an archive into an empty private workspace. Validation of
// the produced filesystem tree belongs to the caller before any file is
// copied into a storage driver.
func (s *Service) Extract(ctx context.Context, archivePath, destDir, password string, members []string) error {
	bin, err := s.sevenZipPath()
	if err != nil {
		return err
	}
	args := []string{"x", "-y", "-aoa", "-bd", "-sccUTF-8", "-o" + destDir}
	if p := passwordArg(password); p != "" {
		args = append(args, p)
	}
	args = append(args, "--", archivePath)
	args = append(args, members...)
	cctx, cancel := s.timeoutContext(ctx)
	defer cancel()
	out, runErr := exec.CommandContext(cctx, bin, args...).CombinedOutput()
	if err := cctx.Err(); err != nil {
		return err
	}
	return classifyPassword(string(out), password, runErr)
}

// ExtractAs fully expands layered tar compression (.tar.gz/.tgz, .tar.bz2
// and .tar.xz) rather than leaving the intermediate tar in the destination.
func (s *Service) ExtractAs(ctx context.Context, archivePath, logicalName, destDir, password string, members []string) error {
	if !isCompressedTar(logicalName) {
		return s.Extract(ctx, archivePath, destDir, password, members)
	}
	root, err := s.workspaceTemp("filex-archive-layer-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(root)
	if err := s.Extract(ctx, archivePath, root, password, nil); err != nil {
		return err
	}
	tarPath, err := findTar(root)
	if err != nil {
		return err
	}
	return s.Extract(ctx, tarPath, destDir, "", members)
}

func isCompressedTar(name string) bool {
	lower := strings.ToLower(name)
	for _, suffix := range []string{".tar.gz", ".tgz", ".tar.bz2", ".tbz2", ".tar.xz", ".txz"} {
		if strings.HasSuffix(lower, suffix) {
			return true
		}
	}
	return false
}

func findTar(root string) (string, error) {
	var found string
	err := filepath.WalkDir(root, func(name string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if !entry.IsDir() && strings.EqualFold(filepath.Ext(entry.Name()), ".tar") {
			if found != "" {
				return errors.New("compressed tar layer produced more than one tar file")
			}
			found = name
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	if found == "" {
		return "", errors.New("compressed tar layer did not produce a tar file")
	}
	return found, nil
}

// Create builds an archive from the entire contents of sourceDir. Compressed
// TAR formats stream the TAR producer directly into gzip, bzip2 or xz, avoiding
// a potentially large intermediate file. sourceDir is an isolated workspace
// prepared by filex, so no user-controlled path becomes an option and no host
// path can be archived accidentally.
func (s *Service) Create(ctx context.Context, sourceDir, destPath string, opts CreateOptions) error {
	format := CanonicalFormat(opts.Format)
	if !IsCreateFormat(format) {
		return ErrUnsupported
	}
	if format != "zip" && format != "7z" && (opts.Password != "" || opts.EncryptNames || opts.Solid != nil || opts.DictionarySizeMiB != 0) {
		return ErrUnsupported
	}
	bin, err := s.sevenZipPath()
	if err != nil {
		return err
	}
	if opts.Compression < 0 || opts.Compression > 9 {
		opts.Compression = 5
	}
	cctx, cancel := s.timeoutContext(ctx)
	defer cancel()
	if codec := compressedTarCodec(format); codec != "" {
		if err := runSevenZipTarStream(cctx, bin, sourceDir, destPath, codec, opts); err != nil {
			_ = os.Remove(destPath)
			return err
		}
		return nil
	}
	return runSevenZipCreate(cctx, bin, sevenZipCreateArgs(destPath, format, opts), sourceDir, opts.Progress)
}

func compressedTarCodec(format string) string {
	switch format {
	case "tar.gz":
		return "gz"
	case "tar.bz2":
		return "bz2"
	case "tar.xz":
		return "xz"
	default:
		return ""
	}
}

func runSevenZipCreate(ctx context.Context, bin string, args []string, dir string, progress func(int)) error {
	if progress != nil {
		return runSevenZipCreateWithProgress(ctx, bin, args, dir, progress)
	}
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Dir = dir
	out, runErr := cmd.CombinedOutput()
	if err := ctx.Err(); err != nil {
		return err
	}
	return classify(string(out), runErr)
}
func runSevenZipCreateWithProgress(ctx context.Context, bin string, args []string, dir string, progress func(int)) error {
	cmd := exec.CommandContext(ctx, bin, sevenZipProgressArgs(args)...)
	cmd.Dir = dir
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}

	var stdoutText, stderrText strings.Builder
	var stdoutErr, stderrErr error
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		stdoutErr = scanSevenZipStream(stdout, &stdoutText, progress)
	}()
	go func() {
		defer wg.Done()
		stderrErr = scanSevenZipStream(stderr, &stderrText, nil)
	}()
	wg.Wait()
	runErr := cmd.Wait()
	if err := ctx.Err(); err != nil {
		return err
	}
	if runErr == nil {
		if stdoutErr != nil {
			return stdoutErr
		}
		if stderrErr != nil {
			return stderrErr
		}
	}
	return classify(stdoutText.String()+"\n"+stderrText.String(), runErr)
}

func runSevenZipTarStream(ctx context.Context, bin, sourceDir, destPath, codec string, opts CreateOptions) error {
	pipelineCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	tarName := strings.TrimSuffix(filepath.Base(destPath), "."+codec)
	producer := exec.CommandContext(pipelineCtx, bin, sevenZipTarStreamArgs()...)
	producer.Dir = sourceDir
	compressor := exec.CommandContext(pipelineCtx, bin, sevenZipCompressorStreamArgs(destPath, codec, opts.Compression, tarName)...)

	reader, writer := io.Pipe()
	producer.Stdout = writer
	compressor.Stdin = reader
	producerStderr, err := producer.StderrPipe()
	if err != nil {
		return err
	}
	compressorStderr, err := compressor.StderrPipe()
	if err != nil {
		return err
	}
	if err := compressor.Start(); err != nil {
		return err
	}
	if err := producer.Start(); err != nil {
		_ = writer.CloseWithError(err)
		_ = reader.CloseWithError(err)
		cancel()
		_ = compressor.Wait()
		return err
	}

	reportProgress := monotonicProgress(opts.Progress)
	streamProgress := func(percent int) { reportProgress(percent * 95 / 100) }
	var producerText, compressorText strings.Builder
	var producerScanErr, compressorScanErr error
	var scans sync.WaitGroup
	scans.Add(2)
	go func() {
		defer scans.Done()
		producerScanErr = scanSevenZipStream(producerStderr, &producerText, streamProgress)
	}()
	go func() {
		defer scans.Done()
		compressorScanErr = scanSevenZipStream(compressorStderr, &compressorText, streamProgress)
	}()

	type processResult struct {
		stage string
		err   error
	}
	done := make(chan processResult, 2)
	go func() {
		err := producer.Wait()
		_ = writer.CloseWithError(err)
		done <- processResult{stage: "tar", err: err}
	}()
	go func() {
		err := compressor.Wait()
		_ = reader.CloseWithError(err)
		done <- processResult{stage: "compression", err: err}
	}()

	first := <-done
	if first.err != nil {
		cancel()
	}
	second := <-done
	scans.Wait()
	if err := ctx.Err(); err != nil {
		return err
	}
	if producerScanErr != nil {
		return producerScanErr
	}
	if compressorScanErr != nil {
		return compressorScanErr
	}
	for _, result := range []processResult{first, second} {
		output := producerText.String()
		if result.stage == "compression" {
			output = compressorText.String()
		}
		if err := classify(output, result.err); err != nil {
			return err
		}
	}
	reportProgress(100)
	return nil
}

func monotonicProgress(progress func(int)) func(int) {
	if progress == nil {
		return func(int) {}
	}
	var mu sync.Mutex
	last := -1
	return func(percent int) {
		mu.Lock()
		defer mu.Unlock()
		if percent > last {
			last = percent
			progress(percent)
		}
	}
}

func sevenZipTarStreamArgs() []string {
	return []string{"a", "-y", "-ttar", "-so", "-an", "-bso2", "-bse2", "-bsp2", "--", "."}
}

func sevenZipCompressorStreamArgs(destPath, codec string, compression int, tarName string) []string {
	return []string{
		"a", "-y", "-t" + sevenZipArchiveType(codec), "-mx=" + strconv.Itoa(compression),
		"-si" + tarName, "-bso2", "-bse2", "-bsp2", "--", destPath,
	}
}

func sevenZipProgressArgs(args []string) []string {
	result := make([]string, 0, len(args)+2)
	for _, arg := range args {
		if arg == "-bd" {
			result = append(result, "-bsp1", "-bso0", "-bse1")
			continue
		}
		result = append(result, arg)
	}
	return result
}

func scanSevenZipStream(reader io.Reader, capture *strings.Builder, progress func(int)) error {
	scanner := bufio.NewScanner(reader)
	scanner.Split(splitSevenZipLines)
	lastPercent := -1
	for scanner.Scan() {
		line := scanner.Text()
		capture.WriteString(line)
		capture.WriteByte('\n')
		if percent, ok := parseSevenZipProgress(line); ok && progress != nil && percent > lastPercent {
			lastPercent = percent
			progress(percent)
		}
	}
	return scanner.Err()
}

func splitSevenZipLines(data []byte, atEOF bool) (advance int, token []byte, err error) {
	for i, b := range data {
		if b == '\r' || b == '\n' || b == '\b' {
			return i + 1, data[:i], nil
		}
	}
	if atEOF && len(data) > 0 {
		return len(data), data, nil
	}
	return 0, nil, nil
}

func parseSevenZipProgress(line string) (int, bool) {
	percentAt := strings.IndexByte(line, '%')
	if percentAt < 1 {
		return 0, false
	}
	start := percentAt
	for start > 0 && line[start-1] >= '0' && line[start-1] <= '9' {
		start--
	}
	if start == percentAt {
		return 0, false
	}
	percent, err := strconv.Atoi(line[start:percentAt])
	if err != nil || percent < 0 || percent > 100 {
		return 0, false
	}
	return percent, true
}

func sevenZipCreateArgs(destPath, format string, opts CreateOptions, inputs ...string) []string {
	args := []string{"a", "-y", "-bd", "-t" + sevenZipArchiveType(format)}
	if format != "tar" {
		args = append(args, "-mx="+strconv.Itoa(opts.Compression))
	}
	if format == "7z" {
		if opts.Solid != nil {
			solid := "off"
			if *opts.Solid {
				solid = "on"
			}
			args = append(args, "-ms="+solid)
		}
		if opts.DictionarySizeMiB > 0 {
			args = append(args, "-md="+strconv.Itoa(opts.DictionarySizeMiB)+"m")
		}
	}
	if p := passwordArg(opts.Password); p != "" {
		args = append(args, p)
		if format == "7z" && opts.EncryptNames {
			args = append(args, "-mhe=on")
		}
		if format == "zip" {
			args = append(args, "-mem=AES256")
		}
	}
	if len(inputs) == 0 {
		inputs = []string{"."}
	}
	args = append(args, destPath)
	args = append(args, inputs...)
	return args
}

func sevenZipArchiveType(format string) string {
	switch format {
	case "gz":
		return "gzip"
	case "bz2":
		return "bzip2"
	default:
		return format
	}
}

// ValidDictionarySizeMiB bounds 7z's memory use to explicit, predictable
// powers of two. Zero means let 7-Zip choose its format/level default.
func ValidDictionarySizeMiB(size int) bool {
	switch size {
	case 0, 1, 2, 4, 8, 16, 32, 64, 128, 256:
		return true
	default:
		return false
	}
}
