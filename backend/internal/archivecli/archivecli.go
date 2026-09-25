// Package archivecli runs operator-provided archive tools behind a small,
// fixed interface. It never evaluates a shell command: provider arguments are
// assembled here and the executable path comes only from process config.
package archivecli

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path"
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
	// ErrPasswordCharset: a password 7-Zip cannot take. It reads one LINE from
	// stdin, so a line break cannot be part of one, and it refuses to encrypt
	// a ZIP with anything but printable ASCII (E_INVALIDARG) — said up front
	// instead of as "archive provider failed" after the files were staged.
	ErrPasswordCharset = errors.New("archive password contains characters this format cannot use")
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
	// Special: a FIFO, a device or a socket — neither a file nor a folder.
	Special bool `json:"special,omitempty"`
	// SizeUnknown: the archive does not say how large this member is (a RAR5
	// stream written by `rar -si` sets that flag; 7-Zip lists `Size = ` empty).
	// 7-Zip then decodes it to the end of its data, so nothing before the
	// extraction can hold it to the limit: Preflight refuses it.
	SizeUnknown bool `json:"-"`
}

// Refused reports a member filex never extracts: a link or a special file.
func (e Entry) Refused() bool { return e.IsLink || e.Special }

// ProviderStatus describes an archive provider without exposing host paths.
type ProviderStatus struct {
	Name           string   `json:"name"`
	Available      bool     `json:"available"`
	Version        string   `json:"version,omitempty"`
	CreateFormats  []string `json:"create_formats"`
	ExtractFormats []string `json:"extract_formats"`
	Encrypted      bool     `json:"encrypted"`
	Error          string   `json:"error,omitempty"`
	// RequiredVersion: the oldest version filex accepts, so the admin page can
	// say what to install in its own language.
	RequiredVersion string `json:"required_version,omitempty"`
}

// Service resolves and invokes supported archive providers.
type Service struct {
	store SettingsStore
	cfg   Config

	// probe caches the 7-Zip resolution (path + version check) for
	// probeTTL: the capabilities answer asks on every page load, and each
	// list/test/extract used to spawn `7z i` first.
	probeMu sync.Mutex
	probe   *sevenZipProbe
}

type sevenZipProbe struct {
	path   string
	banner string
	// rar: this 7-Zip has the RAR handlers. ⚠ Not a given — Alpine's `7zip`
	// package (the official image's) is built without them (the unRAR
	// licence), and answers "Unsupported archive type" to `-trar`.
	rar bool
	err error
	at  time.Time
}

const probeTTL = 30 * time.Second

func New(store SettingsStore, cfg Config) *Service { return &Service{store: store, cfg: cfg} }

func (s *Service) WorkDir() string { return s.cfg.WorkDir }

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

// ⚠ 25.01, not 24.07. 24.07 closed CVE-2024-11477 (the Zstandard decoder), but
// 24.09 — Alpine 3.22's package — still has CVE-2025-11001/11002 (ZIP symlink
// traversal), CVE-2025-53816 (RAR5 heap overflow), CVE-2025-53817 (compound
// document crash), all fixed in 25.00, and CVE-2025-55188 (symlinks written
// outside the output folder during extraction), fixed in 25.01. The last one
// is exactly the class of bug the link checks here guard against.
const (
	minimumSevenZipMajor = 25
	minimumSevenZipMinor = 1
)

func minimumSevenZipVersion() string {
	return fmt.Sprintf("%d.%02d", minimumSevenZipMajor, minimumSevenZipMinor)
}

// 7zz prints "7-Zip (z) 26.01", 7za "7-Zip (a) …", 7zr "7-Zip (r) …" and
// p7zip "7-Zip [64] 16.02".
var sevenZipVersionPattern = regexp.MustCompile(`(?i)7-zip(?: \([a-z]\)| \[[^]]+\])?\s+([0-9]{2,})\.([0-9]{2})`)

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

// sevenZipRARFormat finds the Rar5 handler in the `7z i` format table
// ("  ...F....  Rar5     rar r00  R a r ! 1A 07 01 00").
var sevenZipRARFormat = regexp.MustCompile(`(?m)^\s*\S{10,}\s+Rar5\s`)

func probeSevenZip(path string) (banner string, rar bool, err error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, path, "i").CombinedOutput()
	if ctx.Err() != nil {
		return "", false, fmt.Errorf("%w: 7-Zip version probe timed out", ErrUnavailable)
	}
	if err != nil {
		return "", false, fmt.Errorf("%w: 7-Zip version probe failed", ErrUnavailable)
	}
	if _, err := validateSevenZipVersion(string(out)); err != nil {
		return "", false, err
	}
	return firstLine(string(out)), sevenZipRARFormat.Match(out), nil
}

func probeSevenZipVersion(path string) (string, error) {
	banner, _, err := probeSevenZip(path)
	if err != nil {
		return "", err
	}
	return validateSevenZipVersion(banner)
}

func (s *Service) sevenZipPath() (string, error) {
	probe := s.resolveSevenZip()
	return probe.path, probe.err
}

// resolveSevenZip finds 7-Zip and checks its version, remembering the answer
// (a failure too) for probeTTL.
func (s *Service) resolveSevenZip() sevenZipProbe {
	if s == nil {
		return sevenZipProbe{err: ErrUnavailable}
	}
	s.probeMu.Lock()
	defer s.probeMu.Unlock()
	if s.probe != nil && time.Since(s.probe.at) < probeTTL {
		return *s.probe
	}
	probe := &sevenZipProbe{at: time.Now()}
	probe.path, probe.banner, probe.rar, probe.err = s.locateSevenZip()
	if probe.err != nil {
		probe.path, probe.banner, probe.rar = "", "", false
	}
	s.probe = probe
	return *probe
}

func (s *Service) forgetSevenZip() {
	s.probeMu.Lock()
	s.probe = nil
	s.probeMu.Unlock()
}

func (s *Service) locateSevenZip() (string, string, bool, error) {
	p := strings.TrimSpace(s.cfg.SevenZipBin)
	if p != "" {
		st, err := os.Stat(p)
		if err != nil || st.IsDir() {
			return "", "", false, fmt.Errorf("%w: configured 7-Zip binary %q is not executable", ErrUnavailable, p)
		}
	} else {
		for _, name := range []string{"7zz", "7z"} {
			if found, err := exec.LookPath(name); err == nil {
				p = found
				break
			}
		}
		if p == "" {
			return "", "", false, fmt.Errorf("%w: 7zz or 7z was not found on PATH", ErrUnavailable)
		}
	}
	banner, rar, err := probeSevenZip(p)
	if err != nil {
		return "", "", false, err
	}
	return p, banner, rar, nil
}

// SevenZipAvailable reports whether a supported 7-Zip is usable right now:
// installed, new enough, and not switched off in the archive policy.
func (s *Service) SevenZipAvailable(ctx context.Context) bool {
	if s == nil || !s.Policy(ctx).Enabled {
		return false
	}
	_, err := s.sevenZipPath()
	return err == nil
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
		Name: "builtin-zip", Available: true, Version: "Go archive/zip, archive/tar, compress/gzip, compress/bzip2",
		CreateFormats: []string{"zip"}, ExtractFormats: []string{"zip", "tar", "tar.gz", "tar.bz2", "gz", "bz2"}, Encrypted: false,
	}}
	seven := ProviderStatus{
		Name: "sevenzip", CreateFormats: []string{"zip", "7z", "tar", "tar.gz", "tar.bz2", "tar.xz"},
		ExtractFormats: []string{"zip", "7z", "tar.xz", "xz"}, Encrypted: true,
		RequiredVersion: minimumSevenZipVersion(),
	}
	// The admin page asks rarely and wants the truth now (7-Zip may have just
	// been installed), so it does not reuse the cached probe.
	s.forgetSevenZip()
	if probe := s.resolveSevenZip(); probe.err != nil {
		slog.Warn("archive provider unavailable", slog.String("provider", "sevenzip"), slog.String("err", probe.err.Error()))
		seven.Error = "7-Zip " + minimumSevenZipVersion() + " or newer is unavailable"
	} else {
		seven.Available = true
		seven.Version = probe.banner
		if probe.rar {
			seven.ExtractFormats = []string{"zip", "7z", "rar", "tar.xz", "xz"}
		}
	}
	return append(out, seven)
}

func (s *Service) timeoutContext(ctx context.Context) (context.Context, context.CancelFunc) {
	p := s.Policy(ctx)
	return context.WithTimeout(ctx, time.Duration(p.TimeoutSeconds)*time.Second)
}

// command builds a 7-Zip invocation. ⚠ The password never goes on the
// command line: `-p<password>` is readable by every local account through
// /proc/<pid>/cmdline and `ps` for as long as 7-Zip runs. 7-Zip reads it
// from stdin instead, one line, whenever it needs one — list, test and
// extract ask on their own when an archive is encrypted, create asks
// because of a bare `-p`. With no password, stdin is empty and the prompt
// ends at EOF, which classify reports as ErrPasswordRequired.
func (s *Service) command(ctx context.Context, bin string, args []string, password string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, bin, args...)
	if password != "" {
		cmd.Stdin = strings.NewReader(password + "\n")
	} else {
		cmd.Stdin = strings.NewReader("")
	}
	return cmd
}

// CheckPassword refuses what 7-Zip cannot be given: a line break ends the one
// line it reads, and ZIP encryption takes printable ASCII only.
func CheckPassword(password, format string) error {
	if strings.ContainsAny(password, "\r\n\x00") {
		return ErrPasswordCharset
	}
	if format == "zip" {
		for _, r := range password {
			if r < 0x20 || r > 0x7e {
				return ErrPasswordCharset
			}
		}
	}
	return nil
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
	case strings.Contains(lower, "is not supported archive"), strings.Contains(lower, "cannot open the file as"),
		strings.Contains(lower, "unsupported archive type"):
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

// sevenZipType is the `-t` switch for a file the user calls logicalName.
//
// ⚠ Always named, never guessed. Without `-t`, 7-Zip opens whatever format
// the BYTES say: a file named .zip that is really a compound document runs the
// compound-document parser (CVE-2025-53817 lived there), a .tar.gz that is
// really a 7z carrying links is unpacked as a 7z. 7-Zip has some fifty
// parsers; filex offers seven formats, so seven parsers are all it may reach.
func sevenZipType(archivePath, logicalName string) (string, error) {
	switch FormatFromPath(logicalName) {
	case "7z":
		return "7z", nil
	case "zip":
		return "zip", nil
	case "rar":
		return rarType(archivePath)
	case "tar":
		return "tar", nil
	case "gz", "tar.gz":
		return "gzip", nil
	case "bz2", "tar.bz2":
		return "bzip2", nil
	case "xz", "tar.xz":
		return "xz", nil
	}
	return "", fmt.Errorf("%w: %s", ErrUnsupported, path.Ext(logicalName))
}

// rarType tells RAR 5 from RAR 1.5–4, which 7-Zip reads with two different
// handlers ("Rar5" and "Rar"), by the signature both start with.
func rarType(archivePath string) (string, error) {
	f, err := os.Open(archivePath)
	if err != nil {
		return "", err
	}
	defer f.Close()
	head := make([]byte, 8)
	n, _ := io.ReadFull(f, head)
	head = head[:n]
	switch {
	case bytes.HasPrefix(head, []byte("Rar!\x1a\x07\x01\x00")):
		return "rar5", nil
	case bytes.HasPrefix(head, []byte("Rar!\x1a\x07\x00")):
		return "rar", nil
	}
	return "", fmt.Errorf("%w: not a RAR archive", ErrUnsupported)
}

// sevenZipReady is the gate in front of every 7-Zip run of type kind (a `-t`
// value, or the format being created): the policy's switch, a supported
// binary, and — for RAR — one built with the RAR handlers.
func (s *Service) sevenZipReady(ctx context.Context, kind string) (string, error) {
	if !s.Policy(ctx).Enabled {
		return "", fmt.Errorf("%w: external archive processing is disabled", ErrUnavailable)
	}
	probe := s.resolveSevenZip()
	if probe.err != nil {
		return "", probe.err
	}
	if (kind == "rar" || kind == "rar5") && !probe.rar {
		return "", fmt.Errorf("%w: this 7-Zip is built without RAR support", ErrUnsupported)
	}
	return probe.path, nil
}

// List lists archivePath, taking its format from its own name.
func (s *Service) List(ctx context.Context, archivePath, password string) ([]Entry, error) {
	return s.ListAs(ctx, archivePath, archivePath, password)
}

// ListAs lists archivePath as the format logicalName names. The TAR family
// and bare gzip/bzip2/xz are read here (stream.go); 7z, RAR and ZIP through
// 7-Zip, which prints stable Path/Size/Modified/Attributes blocks.
func (s *Service) ListAs(ctx context.Context, archivePath, logicalName, password string) ([]Entry, error) {
	if sf, ok := streamFormatFor(logicalName); ok {
		return s.listStream(ctx, archivePath, logicalName, sf)
	}
	kind, err := sevenZipType(archivePath, logicalName)
	if err != nil {
		return nil, err
	}
	bin, err := s.sevenZipReady(ctx, kind)
	if err != nil {
		return nil, err
	}
	args := []string{"l", "-t" + kind, "-slt", "-ba", "-sccUTF-8", "-bd", "--", archivePath}
	cctx, cancel := s.timeoutContext(ctx)
	defer cancel()
	out, runErr := s.command(cctx, bin, args, password).CombinedOutput()
	if err := cctx.Err(); err != nil {
		return nil, err
	}
	if err := classifyPassword(string(out), password, runErr); err != nil {
		return nil, err
	}
	return parseTechnicalList(string(out), archivePath), nil
}

// unixModePattern is the ls-style mode 7-Zip prints: in `Mode` for TAR, and
// as the last word of `Attributes` for ZIP and 7z entries made on unix.
var unixModePattern = regexp.MustCompile(`^[-dlcbps?][-rwxsStT]{9}$`)

// unixMode finds the member's unix mode, or "" when 7-Zip shows none.
func unixMode(fields map[string]string) string {
	if m := strings.TrimSpace(fields["Mode"]); unixModePattern.MatchString(m) {
		return m
	}
	parts := strings.Fields(fields["Attributes"])
	if n := len(parts); n > 0 && unixModePattern.MatchString(parts[n-1]) {
		return parts[n-1]
	}
	return ""
}

// parseTechnicalList reads `7z l -slt`. The first Path block describes the
// archive itself and is omitted.
//
// ⚠⚠ A link is not always a "Symbolic Link" or "Hard Link" field. Those two
// exist for TAR only: a ZIP made with `zip -y` or a 7z made with `7zz a -snl`
// carries its symlink as an ordinary-looking entry whose Attributes END in
// `lrwxrwxrwx`, and a TAR FIFO or device has neither field, only
// `Mode = prw-r--r--` / `crw-r--r--` (testdata/slt holds the real listings).
// The mode is what decides.
func parseTechnicalList(out, archivePath string) []Entry {
	var entries []Entry
	cur := map[string]string{}
	flush := func() {
		name := strings.TrimSpace(cur["Path"])
		if name == "" || filepath.Clean(name) == filepath.Clean(archivePath) {
			cur = map[string]string{}
			return
		}
		size, sizeErr := strconv.ParseInt(strings.TrimSpace(cur["Size"]), 10, 64)
		mt, _ := time.Parse("2006-01-02 15:04:05", strings.TrimSpace(cur["Modified"]))
		attrs := strings.TrimSpace(cur["Attributes"])
		mode := unixMode(cur)
		kind := byte(0)
		if mode != "" {
			kind = mode[0]
		}
		// RAR5 has a third kind, a "file copy" of another member: Copy Link.
		isLink := strings.TrimSpace(cur["Symbolic Link"]) != "" || strings.TrimSpace(cur["Hard Link"]) != "" ||
			strings.TrimSpace(cur["Copy Link"]) != "" || kind == 'l'
		isDir := !isLink && (strings.TrimSpace(cur["Folder"]) == "+" || strings.HasPrefix(attrs, "D") ||
			strings.HasSuffix(name, "/") || kind == 'd')
		entries = append(entries, Entry{
			Name: name, Size: size, Mtime: mt,
			IsDir:       isDir,
			Encrypted:   strings.TrimSpace(cur["Encrypted"]) == "+",
			IsLink:      isLink,
			Special:     kind == 'p' || kind == 'c' || kind == 'b' || kind == 's',
			SizeUnknown: !isDir && sizeErr != nil,
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

// Preflight judges a listing before anything is extracted, the same way for
// every reader: each member must have a clean relative path, be a file or a
// folder (never a link or a special file) and say how large it is; the
// declared sizes and the number of entries must fit the policy.
func Preflight(entries []Entry, policy Policy) error {
	var declared int64
	for _, entry := range entries {
		if _, err := CleanMemberPath(entry.Name); err != nil {
			return fmt.Errorf("%w: archive contains an unsafe member path", ErrUnsupported)
		}
		if entry.Refused() {
			return fmt.Errorf("%w: archive contains a link or special entry", ErrUnsupported)
		}
		if entry.IsDir {
			continue
		}
		if entry.SizeUnknown {
			return fmt.Errorf("%w: archive does not declare the size of a member", ErrUnsupported)
		}
		if entry.Size < 0 || entry.Size > policy.MaxExpandedBytes-declared {
			return extractionLimitError()
		}
		declared += entry.Size
	}
	if len(entries) > policy.MaxEntries {
		return extractionLimitError()
	}
	return nil
}

// Test validates that every member can be decrypted without materialising it.
// Listing alone is insufficient for ZIP and non-header-encrypted 7z archives:
// 7-Zip can reveal their names without ever checking the content password.
func (s *Service) Test(ctx context.Context, archivePath, password string) error {
	return s.TestAs(ctx, archivePath, archivePath, password)
}

// TestAs is Test for an archive the user calls logicalName.
func (s *Service) TestAs(ctx context.Context, archivePath, logicalName, password string) error {
	kind, err := sevenZipType(archivePath, logicalName)
	if err != nil {
		return err
	}
	bin, err := s.sevenZipReady(ctx, kind)
	if err != nil {
		return err
	}
	args := []string{"t", "-t" + kind, "-bd", "-sccUTF-8", "--", archivePath}
	cctx, cancel := s.timeoutContext(ctx)
	defer cancel()
	out, runErr := s.command(cctx, bin, args, password).CombinedOutput()
	if err := cctx.Err(); err != nil {
		return err
	}
	return classifyPassword(string(out), password, runErr)
}

const (
	minimumExtractionBudgetDelay = 100 * time.Millisecond
	maximumExtractionBudgetDelay = 2 * time.Second
	extractionBudgetBackoff      = 4
)

func nextExtractionBudgetDelay(scanDuration time.Duration) time.Duration {
	delay := scanDuration * extractionBudgetBackoff
	if delay < minimumExtractionBudgetDelay {
		return minimumExtractionBudgetDelay
	}
	if delay > maximumExtractionBudgetDelay {
		return maximumExtractionBudgetDelay
	}
	return delay
}

func extractionLimitError() error {
	return fmt.Errorf("%w: extraction limit exceeded", ErrLimits)
}

func checkExtractionBudget(root string, maxEntries int, maxBytes int64) error {
	entries := 0
	var expanded int64
	err := filepath.WalkDir(root, func(name string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			if errors.Is(walkErr, os.ErrNotExist) {
				return nil
			}
			return walkErr
		}
		if name == root {
			return nil
		}
		entries++
		if entries > maxEntries {
			return extractionLimitError()
		}
		info, err := entry.Info()
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return nil
			}
			return err
		}
		if info.Mode().IsRegular() {
			if info.Size() > maxBytes-expanded {
				return extractionLimitError()
			}
			expanded += info.Size()
		}
		return nil
	})
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

func monitorExtractionBudget(ctx context.Context, root string, maxEntries int, maxBytes int64, stop <-chan struct{}) error {
	timer := time.NewTimer(minimumExtractionBudgetDelay)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-stop:
			return nil
		case <-timer.C:
			scanStarted := time.Now()
			if err := checkExtractionBudget(root, maxEntries, maxBytes); err != nil {
				return err
			}
			timer.Reset(nextExtractionBudgetDelay(time.Since(scanStarted)))
		}
	}
}

// Extract expands archivePath, taking its format from its own name.
func (s *Service) Extract(ctx context.Context, archivePath, destDir, password string, members []string) error {
	return s.ExtractAs(ctx, archivePath, archivePath, destDir, password, members)
}

// ExtractAs expands an archive the user calls logicalName into destDir, an
// empty private workspace, while enforcing the live archive policy. The
// caller still validates paths and file types before copying anything into a
// storage driver.
//
// The TAR family and bare gzip/bzip2/xz are read by filex (stream.go): exact
// limits, links refused from the header. 7z, RAR and ZIP go through 7-Zip,
// whose decoders stop at each member's declared size — the preflight checks
// those — with the workspace monitor as the backstop for a format that does
// not declare one.
func (s *Service) ExtractAs(ctx context.Context, archivePath, logicalName, destDir, password string, members []string) error {
	if sf, ok := streamFormatFor(logicalName); ok {
		return s.extractStream(ctx, archivePath, logicalName, destDir, members, sf)
	}
	kind, err := sevenZipType(archivePath, logicalName)
	if err != nil {
		return err
	}
	return s.extractSevenZip(ctx, archivePath, kind, destDir, password, members)
}

// extractSevenZip runs `7z x -t<kind>` into destDir under the workspace
// monitor. The monitor samples the folder, so it can only stop a member that
// grows past the limit some time after it did (the 0.44 review measured the
// overshoot: zz_measure_test.go); it is the backstop for the formats 7-Zip
// alone reads, whose decoders stop at each member's declared size.
func (s *Service) extractSevenZip(ctx context.Context, archivePath, kind, destDir, password string, members []string) error {
	bin, err := s.sevenZipReady(ctx, kind)
	if err != nil {
		return err
	}
	args := []string{"x", "-t" + kind, "-y", "-aoa", "-bd", "-sccUTF-8", "-o" + destDir, "--", archivePath}
	args = append(args, members...)
	cctx, cancel := s.timeoutContext(ctx)
	defer cancel()
	runCtx, stopProcess := context.WithCancel(cctx)
	defer stopProcess()

	policy := s.Policy(ctx)
	stopMonitor := make(chan struct{})
	monitorDone := make(chan error, 1)
	go func() {
		err := monitorExtractionBudget(runCtx, destDir, policy.MaxEntries, policy.MaxExpandedBytes, stopMonitor)
		if err != nil {
			stopProcess()
		}
		monitorDone <- err
	}()

	out, runErr := s.command(runCtx, bin, args, password).CombinedOutput()
	close(stopMonitor)
	if err := <-monitorDone; err != nil {
		return err
	}
	if err := checkExtractionBudget(destDir, policy.MaxEntries, policy.MaxExpandedBytes); err != nil {
		return err
	}
	if err := cctx.Err(); err != nil {
		return err
	}
	return classifyPassword(string(out), password, runErr)
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
	if err := CheckPassword(opts.Password, format); err != nil {
		return err
	}
	bin, err := s.sevenZipReady(ctx, format)
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
	return s.runSevenZipCreate(cctx, bin, sevenZipCreateArgs(destPath, format, opts), sourceDir, opts.Password, opts.Progress)
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

func (s *Service) runSevenZipCreate(ctx context.Context, bin string, args []string, dir, password string, progress func(int)) error {
	if progress != nil {
		return s.runSevenZipCreateWithProgress(ctx, bin, args, dir, password, progress)
	}
	cmd := s.command(ctx, bin, args, password)
	cmd.Dir = dir
	out, runErr := cmd.CombinedOutput()
	if err := ctx.Err(); err != nil {
		return err
	}
	return classify(string(out), runErr)
}
func (s *Service) runSevenZipCreateWithProgress(ctx context.Context, bin string, args []string, dir, password string, progress func(int)) error {
	cmd := s.command(ctx, bin, sevenZipProgressArgs(args), password)
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
	// ⚠ Our own pipes, not StderrPipe(): Wait closes a StderrPipe as soon as
	// the process exits, and the Waits below run WHILE the scanners still
	// read — so a scanner sometimes read a closed pipe and the whole archive
	// failed with "read |0: file already closed" (about one run in ten of
	// TestSevenZipTarFormatsRoundTrip). With an io.Pipe, Wait finishes the
	// copy first and the pipe is closed only after it returns.
	producerStderr, producerStderrW := io.Pipe()
	compressorStderr, compressorStderrW := io.Pipe()
	producer.Stderr = producerStderrW
	compressor.Stderr = compressorStderrW
	if err := compressor.Start(); err != nil {
		return err
	}
	if err := producer.Start(); err != nil {
		_ = writer.CloseWithError(err)
		_ = reader.CloseWithError(err)
		cancel()
		_ = compressorStderr.Close()
		_ = compressor.Wait()
		_ = compressorStderrW.Close()
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
		// Keep reading to EOF even after a scan error: an unread pipe would
		// block the copy that Wait waits for.
		_, _ = io.Copy(io.Discard, producerStderr)
	}()
	go func() {
		defer scans.Done()
		compressorScanErr = scanSevenZipStream(compressorStderr, &compressorText, streamProgress)
		_, _ = io.Copy(io.Discard, compressorStderr)
	}()

	type processResult struct {
		stage string
		err   error
	}
	done := make(chan processResult, 2)
	go func() {
		err := producer.Wait()
		_ = producerStderrW.Close()
		_ = writer.CloseWithError(err)
		done <- processResult{stage: "tar", err: err}
	}()
	go func() {
		err := compressor.Wait()
		_ = compressorStderrW.Close()
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
	if opts.Password != "" {
		// A bare -p: 7-Zip asks, and command() answers on stdin.
		args = append(args, "-p")
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
