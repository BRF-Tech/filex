package archivecli

import (
	"archive/tar"
	"archive/zip"
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"hash/crc32"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/testutil/archivetest"
)

const (
	testSevenZipVersionEnv      = "FILEX_TEST_7ZIP_VERSION"
	testSevenZipExtractBytesEnv = "FILEX_TEST_7ZIP_EXTRACT_BYTES"
	// testSevenZipRecordEnv names a file the helper appends one JSON line to
	// per invocation: the arguments it was given and what arrived on stdin.
	testSevenZipRecordEnv = "FILEX_TEST_7ZIP_RECORD"
	// testSevenZipRAREnv makes the helper's `i` list the Rar5 handler, as a
	// 7-Zip built with RAR support does (Alpine's package is not).
	testSevenZipRAREnv = "FILEX_TEST_7ZIP_RAR"
)

type recordedCall struct {
	Args  []string `json:"args"`
	Stdin string   `json:"stdin"`
}

func TestMain(m *testing.M) {
	version := os.Getenv(testSevenZipVersionEnv)
	if version == "" {
		os.Exit(m.Run())
	}
	if record := os.Getenv(testSevenZipRecordEnv); record != "" && !(len(os.Args) > 1 && os.Args[1] == "i") {
		stdin, _ := io.ReadAll(os.Stdin)
		if f, err := os.OpenFile(record, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600); err == nil {
			_ = json.NewEncoder(f).Encode(recordedCall{Args: os.Args[1:], Stdin: string(stdin)})
			_ = f.Close()
		}
	}
	if len(os.Args) > 1 && os.Args[1] == "i" {
		_, _ = os.Stdout.WriteString("7-Zip (z) " + version + " (test helper)\n")
		if os.Getenv(testSevenZipRAREnv) != "" {
			_, _ = os.Stdout.WriteString("\nFormats:\n    ...F..................  Rar5     rar r00       R a r ! 1A 07 01 00\n")
		}
		os.Exit(0)
	}
	if raw := os.Getenv(testSevenZipExtractBytesEnv); raw != "" {
		total, err := strconv.Atoi(raw)
		if err != nil {
			os.Exit(2)
		}
		dest := ""
		for _, arg := range os.Args[1:] {
			if strings.HasPrefix(arg, "-o") {
				dest = strings.TrimPrefix(arg, "-o")
			}
		}
		if dest == "" || os.MkdirAll(dest, 0o700) != nil {
			os.Exit(2)
		}
		file, err := os.Create(filepath.Join(dest, "payload.bin"))
		if err != nil {
			os.Exit(2)
		}
		chunk := bytes.Repeat([]byte("x"), 4096)
		for written := 0; written < total; written += len(chunk) {
			remaining := total - written
			if remaining < len(chunk) {
				chunk = chunk[:remaining]
			}
			if _, err := file.Write(chunk); err != nil {
				_ = file.Close()
				os.Exit(2)
			}
			time.Sleep(2 * time.Millisecond)
		}
		_ = file.Close()
		os.Exit(0)
	}
	_, _ = os.Stdout.WriteString("7-Zip (z) " + version + " (test helper)\n")
	os.Exit(0)
}

type memorySettings map[string]string

func (m memorySettings) GetSetting(_ context.Context, key string) (string, error) {
	return m[key], nil
}

func (m memorySettings) UpsertSetting(_ context.Context, key, value string) error {
	m[key] = value
	return nil
}

func TestProviderStatusDoesNotExposeConfiguredBinaryPath(t *testing.T) {
	const sensitivePath = "/srv/filex/private/tools/7zz"
	providers := New(memorySettings{}, Config{SevenZipBin: sensitivePath}).Providers(context.Background())
	require.Len(t, providers, 2)
	seven := providers[1]
	assert.False(t, seven.Available)
	assert.NotContains(t, seven.Error, sensitivePath)

	b, err := json.Marshal(seven)
	require.NoError(t, err)
	assert.NotContains(t, string(b), sensitivePath)
	assert.NotContains(t, string(b), `"binary"`)
}

func TestPolicyCanonicalizesAndBoundsStoredValues(t *testing.T) {
	svc := New(memorySettings{
		SettingEnabled:          "false",
		SettingDefaultFormat:    ".7Z",
		SettingAllowedFormats:   "ZIP, .7z,zip,rar, .tgz,tar.bz2,txz,unknown",
		SettingMaxEntries:       "123",
		SettingMaxExpandedBytes: "456789",
		SettingTimeoutSeconds:   "90",
	}, Config{})

	p := svc.Policy(context.Background())
	assert.False(t, p.Enabled)
	assert.Equal(t, "7z", p.DefaultFormat)
	assert.Equal(t, []string{"zip", "7z", "tar.gz", "tar.bz2", "tar.xz"}, p.AllowedFormats)
	assert.Equal(t, 123, p.MaxEntries)
	assert.EqualValues(t, 456789, p.MaxExpandedBytes)
	assert.Equal(t, 90, p.TimeoutSeconds)
}

func TestParseTechnicalList(t *testing.T) {
	out := `Path = /tmp/example.7z
Type = 7z

Path = folder
Size = 0
Modified = 2026-09-22 10:11:12
Attributes = D....

Path = folder/report.txt
Size = 42
Modified = 2026-09-22 10:12:13
Attributes = A....
Encrypted = +

Path = folder/latest.txt
Size = 0
Modified = 2026-09-22 10:13:14
Attributes = A....
Symbolic Link = report.txt

Path = folder/copy.txt
Size = 42
Modified = 2026-09-22 10:14:15
Attributes = A....
Hard Link = folder/report.txt
`
	entries := parseTechnicalList(out, "/tmp/example.7z")
	require.Len(t, entries, 4)
	assert.Equal(t, "folder", entries[0].Name)
	assert.True(t, entries[0].IsDir)
	assert.Equal(t, "folder/report.txt", entries[1].Name)
	assert.EqualValues(t, 42, entries[1].Size)
	assert.False(t, entries[1].Mtime.IsZero())
	assert.True(t, entries[1].Encrypted)
	assert.False(t, entries[1].IsLink)
	assert.True(t, entries[2].IsLink)
	assert.True(t, entries[3].IsLink)
}

func TestSevenZipMinimumVersion(t *testing.T) {
	// 25.01 is the floor: 25.00 closed the ZIP symlink traversal
	// (CVE-2025-11001/11002) and the RAR5 and compound-document crashes
	// (CVE-2025-53816/53817), 25.01 the symlink handling that let an archive
	// write outside the output folder (CVE-2025-55188). 24.09 is what Alpine
	// 3.22 ships, and it has all four.
	version, err := validateSevenZipVersion("7-Zip (z) 25.01 (x64)")
	require.NoError(t, err)
	assert.Equal(t, "25.01", version)

	version, err = validateSevenZipVersion("7-Zip (a) 26.01 (x64) : Copyright (c) 1999-2026 Igor Pavlov")
	require.NoError(t, err, "7za and 7zr banners name the same version")
	assert.Equal(t, "26.01", version)

	version, err = validateSevenZipVersion("7-Zip (z) 24.09 (x64)")
	assert.Equal(t, "24.09", version)
	assert.ErrorIs(t, err, ErrUnavailable)

	version, err = validateSevenZipVersion("7-Zip [64] 23.01")
	assert.Equal(t, "23.01", version)
	assert.ErrorIs(t, err, ErrUnavailable)
	assert.Contains(t, err.Error(), "required "+minimumSevenZipVersion())

	_, err = validateSevenZipVersion("not a 7-Zip banner")
	assert.ErrorIs(t, err, ErrUnavailable)

	_, err = validateSevenZipVersion("copyright 24.09")
	assert.ErrorIs(t, err, ErrUnavailable)
}

func TestSevenZipPathRejectsOutdatedBinary(t *testing.T) {
	bin, err := os.Executable()
	require.NoError(t, err)

	t.Setenv(testSevenZipVersionEnv, "23.01")
	_, err = New(memorySettings{}, Config{SevenZipBin: bin}).sevenZipPath()
	assert.ErrorIs(t, err, ErrUnavailable)

	t.Setenv(testSevenZipVersionEnv, "24.09")
	_, err = New(memorySettings{}, Config{SevenZipBin: bin}).sevenZipPath()
	assert.ErrorIs(t, err, ErrUnavailable, "24.09 predates the 25.00/25.01 link and parser fixes")

	t.Setenv(testSevenZipVersionEnv, "25.01")
	resolved, err := New(memorySettings{}, Config{SevenZipBin: bin}).sevenZipPath()
	require.NoError(t, err)
	assert.Equal(t, bin, resolved)
}

func TestExtractionBudgetDelayAdaptsToScanCost(t *testing.T) {
	assert.Equal(t, 100*time.Millisecond, nextExtractionBudgetDelay(time.Millisecond))
	assert.Equal(t, 400*time.Millisecond, nextExtractionBudgetDelay(100*time.Millisecond))
	assert.Equal(t, 2*time.Second, nextExtractionBudgetDelay(time.Second))
}

func TestExtractionBudgetCountsEntriesAndBytes(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(root, "folder"), 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(root, "folder", "file.txt"), []byte("ab"), 0o600))

	assert.ErrorIs(t, checkExtractionBudget(root, 1, 2), ErrLimits)
	assert.ErrorIs(t, checkExtractionBudget(root, 2, 1), ErrLimits)
	assert.NoError(t, checkExtractionBudget(root, 2, 2))
}

func TestExtractStopsWhenRunningBudgetIsExceeded(t *testing.T) {
	bin, err := os.Executable()
	require.NoError(t, err)
	t.Setenv(testSevenZipVersionEnv, "25.01")
	const generatedBytes = 4 << 20
	t.Setenv(testSevenZipExtractBytesEnv, strconv.Itoa(generatedBytes))
	root := t.TempDir()
	svc := New(memorySettings{
		SettingMaxEntries:       "10",
		SettingMaxExpandedBytes: "1024",
		SettingTimeoutSeconds:   "10",
	}, Config{SevenZipBin: bin})

	err = svc.Extract(context.Background(), "archive.7z", root, "", nil)
	assert.ErrorIs(t, err, ErrLimits)
	info, statErr := os.Stat(filepath.Join(root, "payload.bin"))
	require.NoError(t, statErr)
	assert.Less(t, info.Size(), int64(generatedBytes), "provider must be stopped before it writes the whole payload")
}

func BenchmarkCheckExtractionBudget(b *testing.B) {
	for _, entries := range []int{100, 1_000, 10_000, 20_000} {
		b.Run(strconv.Itoa(entries), func(b *testing.B) {
			root := b.TempDir()
			for i := 0; i < entries; i++ {
				if err := os.WriteFile(filepath.Join(root, strconv.Itoa(i)), []byte("x"), 0o600); err != nil {
					b.Fatal(err)
				}
			}
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if err := checkExtractionBudget(root, entries, int64(entries)); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func TestProviderErrorsAreClassified(t *testing.T) {
	err := errors.New("exit status 2")
	assert.ErrorIs(t, classify("ERROR: Wrong password", err), ErrBadPassword)
	assert.ErrorIs(t, classify("Enter password", err), ErrPasswordRequired)
	assert.ErrorIs(t, classify("Cannot open the file as archive", err), ErrUnsupported)
	assert.ErrorIs(t, classifyPassword("ERROR: Wrong password", "", err), ErrPasswordRequired)
	assert.ErrorIs(t, classifyPassword("ERROR: Wrong password", "bad", err), ErrBadPassword)
}

func TestFormatFromPath(t *testing.T) {
	assert.Equal(t, "7z", FormatFromPath("backup.7Z"))
	assert.Equal(t, "tar.gz", FormatFromPath("backup.tar.gz"))
	assert.Equal(t, "tar.gz", FormatFromPath("backup.tgz"))
	assert.Equal(t, "tar.bz2", FormatFromPath("backup.tbz2"))
	assert.Equal(t, "tar.xz", FormatFromPath("backup.txz"))
	assert.Equal(t, "gz", FormatFromPath("single.gz"))
	assert.Empty(t, FormatFromPath("README"))
}

func TestSevenZipCreateArgsIncludeAdvanced7zOptions(t *testing.T) {
	solid := false
	args := sevenZipCreateArgs("/tmp/output.7z", "7z", CreateOptions{
		Password: "secret", EncryptNames: true, Compression: 7,
		Solid: &solid, DictionarySizeMiB: 128,
	})
	assert.Equal(t, []string{
		"a", "-y", "-bd", "-t7z", "-mx=7", "-ms=off", "-md=128m",
		"-p", "-mhe=on", "/tmp/output.7z", ".",
	}, args)
}

func TestSevenZipCreateArgsForTarLayers(t *testing.T) {
	assert.Equal(t, []string{"a", "-y", "-bd", "-ttar", "/tmp/output.tar", "."},
		sevenZipCreateArgs("/tmp/output.tar", "tar", CreateOptions{Compression: 9}))
	assert.Equal(t, []string{"a", "-y", "-bd", "-tgzip", "-mx=7", "/tmp/output.tar.gz", "output.tar"},
		sevenZipCreateArgs("/tmp/output.tar.gz", "gz", CreateOptions{Compression: 7}, "output.tar"))
	assert.Equal(t, []string{"a", "-y", "-bd", "-tbzip2", "-mx=7", "/tmp/output.tar.bz2", "output.tar"},
		sevenZipCreateArgs("/tmp/output.tar.bz2", "bz2", CreateOptions{Compression: 7}, "output.tar"))
	assert.Equal(t,
		[]string{"a", "-y", "-ttar", "-so", "-an", "-bso2", "-bse2", "-bsp2", "--", "."},
		sevenZipTarStreamArgs(),
	)
	assert.Equal(t,
		[]string{"a", "-y", "-tgzip", "-mx=7", "-sioutput.tar", "-bso2", "-bse2", "-bsp2", "--", "/tmp/output.tar.gz"},
		sevenZipCompressorStreamArgs("/tmp/output.tar.gz", "gz", 7, "output.tar"),
	)
}

func TestSevenZipProgressArgsEnableMachineReadableProgress(t *testing.T) {
	args := sevenZipProgressArgs([]string{"a", "-y", "-bd", "-t7z", "out.7z", "."})
	assert.NotContains(t, args, "-bd")
	assert.Contains(t, args, "-bsp1")
	assert.Contains(t, args, "-bso0")
	assert.Contains(t, args, "-bse1")
}

func TestSevenZipProgressStreamParsesTerminalUpdates(t *testing.T) {
	var capture strings.Builder
	var progress []int
	err := scanSevenZipStream(
		strings.NewReader("  0%\b\b 37% 1 + a.txt\r 37% 1 + a.txt\r100% 1 + a.txt\n"),
		&capture,
		func(percent int) { progress = append(progress, percent) },
	)
	require.NoError(t, err)
	assert.Equal(t, []int{0, 37, 100}, progress)
	assert.Contains(t, capture.String(), "37%")
}

func TestCreateOptionsRejectUnsupportedDictionarySizes(t *testing.T) {
	for _, size := range []int{0, 1, 2, 4, 8, 16, 32, 64, 128, 256} {
		assert.True(t, ValidDictionarySizeMiB(size), "size %d", size)
	}
	for _, size := range []int{-1, 3, 512, 1024} {
		assert.False(t, ValidDictionarySizeMiB(size), "size %d", size)
	}
}

func TestRARCreationIsDeferred(t *testing.T) {
	err := New(nil, Config{}).Create(context.Background(), t.TempDir(), "out.rar", CreateOptions{Format: "rar"})
	assert.ErrorIs(t, err, ErrUnsupported)
}

func TestCompressedTarDetection(t *testing.T) {
	for name, want := range map[string]streamFormat{
		"backup.TAR.GZ": {codec: "gz", tar: true},
		"backup.tgz":    {codec: "gz", tar: true},
		"backup.tbz2":   {codec: "bz2", tar: true},
		"backup.txz":    {codec: "xz", tar: true},
		"backup.tar":    {tar: true},
		"backup.gz":     {codec: "gz"},
	} {
		got, ok := streamFormatFor(name)
		assert.True(t, ok, name)
		assert.Equal(t, want, got, name)
	}
	_, ok := streamFormatFor("backup.7z")
	assert.False(t, ok, "7z is 7-Zip's")
}

func TestSevenZipEncryptedRoundTrip(t *testing.T) {
	bin := supportedSevenZip(t)
	var err error

	root := t.TempDir()
	source := filepath.Join(root, "source")
	extracted := filepath.Join(root, "extracted")
	require.NoError(t, os.Mkdir(source, 0o700))
	require.NoError(t, os.Mkdir(extracted, 0o700))
	want := bytes.Repeat([]byte("filex encrypted archive round trip\n"), 32*1024)
	require.NoError(t, os.WriteFile(filepath.Join(source, "report.txt"), want, 0o600))

	svc := New(memorySettings{}, Config{SevenZipBin: bin, WorkDir: root})
	archive := filepath.Join(root, "protected.7z")
	var progress []int
	solid := true
	require.NoError(t, svc.Create(context.Background(), source, archive, CreateOptions{
		Format: "7z", Password: "correct horse", EncryptNames: true,
		Compression: 1, Solid: &solid, DictionarySizeMiB: 4,
		Progress: func(percent int) { progress = append(progress, percent) },
	}))
	require.NotEmpty(t, progress, "the real provider should emit progress updates")
	for i, percent := range progress {
		assert.GreaterOrEqual(t, percent, 0)
		assert.LessOrEqual(t, percent, 100)
		if i > 0 {
			assert.GreaterOrEqual(t, percent, progress[i-1], "progress must be monotonic")
		}
	}

	_, err = svc.List(context.Background(), archive, "")
	assert.ErrorIs(t, err, ErrPasswordRequired)
	_, err = svc.List(context.Background(), archive, "wrong")
	assert.ErrorIs(t, err, ErrBadPassword)
	entries, err := svc.List(context.Background(), archive, "correct horse")
	require.NoError(t, err)
	require.Len(t, entries, 1)
	assert.Equal(t, "report.txt", entries[0].Name)
	assert.True(t, entries[0].Encrypted)
	require.NoError(t, svc.Test(context.Background(), archive, "correct horse"))
	require.NoError(t, svc.Extract(context.Background(), archive, extracted, "correct horse", nil))
	got, err := os.ReadFile(filepath.Join(extracted, "report.txt"))
	require.NoError(t, err)
	assert.Equal(t, want, got)
}

func TestSevenZipTarFormatsRoundTrip(t *testing.T) {
	bin := supportedSevenZip(t)

	for _, format := range []string{"tar", "tar.gz", "tar.bz2", "tar.xz"} {
		t.Run(format, func(t *testing.T) {
			root := t.TempDir()
			source := filepath.Join(root, "source")
			extracted := filepath.Join(root, "extracted")
			require.NoError(t, os.MkdirAll(filepath.Join(source, "nested"), 0o700))
			require.NoError(t, os.Mkdir(extracted, 0o700))
			want := bytes.Repeat([]byte("filex tar archive round trip\n"), 32*1024)
			require.NoError(t, os.WriteFile(filepath.Join(source, "nested", "report.txt"), want, 0o600))

			svc := New(memorySettings{}, Config{SevenZipBin: bin, WorkDir: root})
			archive := filepath.Join(root, "bundle."+format)
			intermediate := strings.TrimSuffix(archive, "."+compressedTarCodec(format))
			var progress []int
			require.NoError(t, svc.Create(context.Background(), source, archive, CreateOptions{
				Format: format, Compression: 1,
				Progress: func(percent int) {
					progress = append(progress, percent)
					if format != "tar" {
						_, statErr := os.Stat(intermediate)
						assert.ErrorIs(t, statErr, os.ErrNotExist, "the TAR layer must never be materialised")
					}
				},
			}))
			for i, percent := range progress {
				assert.GreaterOrEqual(t, percent, 0)
				assert.LessOrEqual(t, percent, 100)
				if i > 0 {
					assert.GreaterOrEqual(t, percent, progress[i-1], "progress must be monotonic")
				}
			}
			if format != "tar" {
				require.NotEmpty(t, progress)
				assert.Equal(t, 100, progress[len(progress)-1])
				_, statErr := os.Stat(intermediate)
				assert.ErrorIs(t, statErr, os.ErrNotExist, "the TAR layer must never be materialised")
			}

			entries, err := svc.ListAs(context.Background(), archive, "bundle."+format, "")
			require.NoError(t, err)
			assert.Contains(t, entryNames(entries), "nested/report.txt")
			require.NoError(t, svc.ExtractAs(context.Background(), archive, "bundle."+format, extracted, "", nil))
			got, err := os.ReadFile(filepath.Join(extracted, "nested", "report.txt"))
			require.NoError(t, err)
			assert.Equal(t, want, got)
		})
	}
}

func TestSevenZipTarStreamCancellationRemovesPartialOutput(t *testing.T) {
	bin := supportedSevenZip(t)
	var err error

	root := t.TempDir()
	source := filepath.Join(root, "source")
	require.NoError(t, os.Mkdir(source, 0o700))
	require.NoError(t, os.WriteFile(
		filepath.Join(source, "large.bin"),
		bytes.Repeat([]byte("stream cancellation payload\n"), 512*1024),
		0o600,
	))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var cancelOnce sync.Once
	archive := filepath.Join(root, "cancelled.tar.xz")
	err = New(memorySettings{}, Config{SevenZipBin: bin, WorkDir: root}).Create(ctx, source, archive, CreateOptions{
		Format: "tar.xz", Compression: 9,
		Progress: func(int) { cancelOnce.Do(cancel) },
	})
	require.ErrorIs(t, err, context.Canceled)
	_, err = os.Stat(archive)
	assert.ErrorIs(t, err, os.ErrNotExist, "a cancelled stream must remove its partial destination")
	_, err = os.Stat(filepath.Join(root, "cancelled.tar"))
	assert.ErrorIs(t, err, os.ErrNotExist, "a streamed TAR must never be materialised")
}

func supportedSevenZip(t *testing.T) string {
	t.Helper()
	bin, err := exec.LookPath("7zz")
	if err != nil {
		bin, err = exec.LookPath("7z")
	}
	if err != nil {
		t.Skip("7-Zip is not installed")
	}
	if _, err := probeSevenZipVersion(bin); err != nil {
		t.Skipf("installed 7-Zip is unsupported: %v", err)
	}
	return bin
}

func entryNames(entries []Entry) []string {
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name)
	}
	return names
}

// ── break tests (0.44 review of #48) ──────────────────────────────────────
//
// Each one was written against the branch as submitted and watched fail
// before the fix it pins was made.

// The listings under testdata/slt are 7-Zip 26.01's real `l -slt` output for
// archives made with `ln -s`, `ln`, `mkfifo`, `mknod`, `zip -y` and
// `7zz a -snl`. A ZIP or 7z symlink has NO "Symbolic Link" field: the only
// place 7-Zip says so is the unix mode at the end of Attributes.
func TestParseTechnicalListFlagsLinksShownOnlyByTheirMode(t *testing.T) {
	for file, name := range map[string]string{
		"sym.zip": "sym", "sym.7z": "sym", "sym.tar": "sym", "hard.tar": "hard",
	} {
		raw, err := os.ReadFile(filepath.Join("testdata", "slt", file+".slt"))
		require.NoError(t, err)
		entries := parseTechnicalList(string(raw), "/w/"+file)
		byName := map[string]Entry{}
		for _, entry := range entries {
			byName[entry.Name] = entry
		}
		require.Contains(t, byName, name, file)
		require.Contains(t, byName, "target.txt", file)
		assert.True(t, byName[name].IsLink, "%s: %s is a link", file, name)
		assert.False(t, byName["target.txt"].IsLink, "%s: target.txt is a plain file", file)
	}
}

// ...and a FIFO or a device in a TAR has neither link field: only its Mode.
func TestParseTechnicalListFlagsFifosAndDevices(t *testing.T) {
	for file, name := range map[string]string{"fifo.tar": "fifo", "chardev.tar": "chardev"} {
		raw, err := os.ReadFile(filepath.Join("testdata", "slt", file+".slt"))
		require.NoError(t, err)
		byName := map[string]Entry{}
		for _, entry := range parseTechnicalList(string(raw), "/w/"+file) {
			byName[entry.Name] = entry
		}
		require.Contains(t, byName, name, file)
		assert.True(t, byName[name].Special, "%s: %s is not a file", file, name)
		assert.True(t, byName[name].Refused(), file)
		assert.False(t, byName["target.txt"].Refused(), "%s: target.txt is a plain file", file)
	}
}

// recordSevenZip points a service at the test binary acting as 7-Zip, and
// returns the calls it received (minus the version probe).
func recordSevenZip(t *testing.T, settings memorySettings) (*Service, func() []recordedCall) {
	t.Helper()
	bin, err := os.Executable()
	require.NoError(t, err)
	record := filepath.Join(t.TempDir(), "calls.jsonl")
	t.Setenv(testSevenZipVersionEnv, "26.01")
	t.Setenv(testSevenZipRecordEnv, record)
	if settings == nil {
		settings = memorySettings{}
	}
	svc := New(settings, Config{SevenZipBin: bin, WorkDir: t.TempDir()})
	return svc, func() []recordedCall {
		f, err := os.Open(record)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		require.NoError(t, err)
		defer f.Close()
		var calls []recordedCall
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			var call recordedCall
			require.NoError(t, json.Unmarshal(sc.Bytes(), &call))
			calls = append(calls, call)
		}
		return calls
	}
}

// A password on the command line is readable by every local account through
// /proc/<pid>/cmdline (and `ps`) for as long as 7-Zip runs. It goes on stdin.
func TestPasswordsNeverReachTheCommandLine(t *testing.T) {
	const secret = "correct horse battery"
	svc, calls := recordSevenZip(t, nil)
	archive := filepath.Join(t.TempDir(), "upload.tmp")
	require.NoError(t, os.WriteFile(archive, []byte("7z\xbc\xaf\x27\x1c"), 0o600))
	ctx := context.Background()

	_, _ = svc.ListAs(ctx, archive, "secret.7z", secret)
	_ = svc.TestAs(ctx, archive, "secret.7z", secret)
	_ = svc.ExtractAs(ctx, archive, "secret.7z", t.TempDir(), secret, nil)
	source := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(source, "a.txt"), []byte("a"), 0o600))
	_ = svc.Create(ctx, source, filepath.Join(t.TempDir(), "out.7z"), CreateOptions{Format: "7z", Password: secret})

	got := calls()
	require.GreaterOrEqual(t, len(got), 4, "list, test, extract and create each ran 7-Zip")
	for _, call := range got {
		for _, arg := range call.Args {
			assert.NotContains(t, arg, secret, "7-Zip was given the password as an argument: %q", call.Args)
		}
		assert.Contains(t, call.Stdin, secret, "7-Zip %q did not get the password on stdin", call.Args)
	}
}

// 7-Zip opens whatever format the BYTES say, whatever the name: a file named
// .zip that is really a compound document runs the compound parser (and 7-Zip
// has ~50 of them). The archive type comes from the name the user sees.
func TestTheArchiveTypeComesFromTheNameNotTheBytes(t *testing.T) {
	svc, calls := recordSevenZip(t, nil)
	upload := filepath.Join(t.TempDir(), "filex-arc-123.zip")
	require.NoError(t, os.WriteFile(upload, []byte("not really anything"), 0o600))
	ctx := context.Background()

	for logical, want := range map[string]string{"photos.7z": "-t7z", "backup.zip": "-tzip"} {
		_, _ = svc.ListAs(ctx, upload, logical, "")
		_ = svc.ExtractAs(ctx, upload, logical, t.TempDir(), "", nil)
		got := calls()
		require.NotEmpty(t, got)
		for _, call := range got[len(got)-2:] {
			assert.Contains(t, call.Args, want, "%s: 7-Zip ran without naming the type: %q", logical, call.Args)
		}
	}
}

// The outer layer of a .tar.gz used to be handed to 7-Zip as-is and fully
// extracted before anything was checked, so an archive of ANY kind named
// .tar.gz (a 7z carrying links, say) was unpacked with no preflight at all.
// A .tar.gz is gzip, or it is refused.
func TestACompressedTarIsGzipOrItIsRefused(t *testing.T) {
	svc, _ := recordSevenZip(t, nil)
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, err := zw.Create("inside.txt")
	require.NoError(t, err)
	_, _ = w.Write([]byte("a zip wearing a .tar.gz name"))
	require.NoError(t, zw.Close())
	disguised := filepath.Join(t.TempDir(), "upload.tmp")
	require.NoError(t, os.WriteFile(disguised, buf.Bytes(), 0o600))

	_, err = svc.ListAs(context.Background(), disguised, "backup.tar.gz", "")
	assert.ErrorIs(t, err, ErrUnsupported)
	dest := t.TempDir()
	err = svc.ExtractAs(context.Background(), disguised, "backup.tar.gz", dest, "", nil)
	assert.ErrorIs(t, err, ErrUnsupported)
	assert.Empty(t, treeFiles(t, dest))
}

// gzipBomb returns gzip data that inflates to `size` zero bytes while its
// trailer claims 100: the declared size a listing reports is a lie.
func gzipBomb(t *testing.T, size int) []byte {
	t.Helper()
	var buf bytes.Buffer
	gw, err := gzip.NewWriterLevel(&buf, gzip.BestSpeed)
	require.NoError(t, err)
	gw.Name = "payload.bin"
	zero := make([]byte, 1<<20)
	for written := 0; written < size; written += len(zero) {
		_, err := gw.Write(zero)
		require.NoError(t, err)
	}
	require.NoError(t, gw.Close())
	raw := buf.Bytes()
	binary.LittleEndian.PutUint32(raw[len(raw)-4:], 100)
	return raw
}

func treeFiles(t *testing.T, root string) map[string]os.FileMode {
	t.Helper()
	out := map[string]os.FileMode{}
	require.NoError(t, filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil || p == root {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		out[filepath.ToSlash(rel)] = info.Mode()
		return nil
	}))
	return out
}

func treeBytes(t *testing.T, root string) int64 {
	t.Helper()
	var total int64
	require.NoError(t, filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		info, err := d.Info()
		if err == nil && info.Mode().IsRegular() {
			total += info.Size()
		}
		return err
	}))
	return total
}

// Fix 3's question, answered by measurement: with 7-Zip writing the output
// and a monitor sampling it, a gzip bomb whose trailer lies overshoots the
// limit before the poll catches it (92 MiB on disk for a 64 MiB limit with
// 7-Zip 26.01, 68 MiB with 24.09). Read by filex itself the stop is exact:
// not one byte past the limit is ever written.
func TestAGzipBombStopsExactlyAtTheLimit(t *testing.T) {
	const limit = 1 << 20
	svc, _ := recordSevenZip(t, memorySettings{SettingMaxExpandedBytes: strconv.Itoa(limit)})
	bomb := filepath.Join(t.TempDir(), "upload.tmp")
	require.NoError(t, os.WriteFile(bomb, gzipBomb(t, 16<<20), 0o600))

	dest := t.TempDir()
	err := svc.ExtractAs(context.Background(), bomb, "payload.bin.gz", dest, "", nil)
	require.ErrorIs(t, err, ErrLimits)
	assert.LessOrEqual(t, treeBytes(t, dest), int64(limit), "bytes were written past the limit")
}

type tarMember struct {
	name     string
	typeflag byte
	body     string
	link     string
}

func buildTar(t *testing.T, gz bool, members ...tarMember) []byte {
	t.Helper()
	var buf bytes.Buffer
	var w io.Writer = &buf
	var gw *gzip.Writer
	if gz {
		gw = gzip.NewWriter(&buf)
		w = gw
	}
	tw := tar.NewWriter(w)
	for _, m := range members {
		hdr := &tar.Header{Name: m.name, Typeflag: m.typeflag, Mode: 0o644, Linkname: m.link, ModTime: time.Unix(1_700_000_000, 0)}
		if m.typeflag == tar.TypeReg {
			hdr.Size = int64(len(m.body))
		}
		if m.typeflag == tar.TypeDir {
			hdr.Mode = 0o755
		}
		require.NoError(t, tw.WriteHeader(hdr))
		if hdr.Size > 0 {
			_, err := tw.Write([]byte(m.body))
			require.NoError(t, err)
		}
	}
	require.NoError(t, tw.Close())
	if gw != nil {
		require.NoError(t, gw.Close())
	}
	return buf.Bytes()
}

// A FIFO, a device or a link inside a tar is refused while extracting, before
// anything of that kind exists on disk — whichever way it was listed.
func TestTarLinksAndDevicesAreRefusedWhileExtracting(t *testing.T) {
	svc, _ := recordSevenZip(t, nil)
	for _, bad := range []tarMember{
		{name: "fifo", typeflag: tar.TypeFifo},
		{name: "dev", typeflag: tar.TypeChar},
		{name: "blk", typeflag: tar.TypeBlock},
		{name: "sym", typeflag: tar.TypeSymlink, link: "../../outside"},
		{name: "hard", typeflag: tar.TypeLink, link: "ok.txt"},
	} {
		t.Run(bad.name, func(t *testing.T) {
			archive := filepath.Join(t.TempDir(), "upload.tmp")
			require.NoError(t, os.WriteFile(archive, buildTar(t, true,
				tarMember{name: "ok.txt", typeflag: tar.TypeReg, body: "fine"}, bad), 0o600))
			dest := t.TempDir()
			err := svc.ExtractAs(context.Background(), archive, "bundle.tar.gz", dest, "", nil)
			require.ErrorIs(t, err, ErrUnsupported)
			for name, mode := range treeFiles(t, dest) {
				assert.True(t, mode.IsRegular() || mode.IsDir(), "%s was created as %v", name, mode)
			}
		})
	}
}

// Entry limits hold while extracting, not only in the listing.
func TestTarEntryLimitHoldsWhileExtracting(t *testing.T) {
	svc, _ := recordSevenZip(t, memorySettings{SettingMaxEntries: "3"})
	var members []tarMember
	for i := 0; i < 6; i++ {
		members = append(members, tarMember{name: "f" + strconv.Itoa(i) + ".txt", typeflag: tar.TypeReg, body: "x"})
	}
	archive := filepath.Join(t.TempDir(), "upload.tmp")
	require.NoError(t, os.WriteFile(archive, buildTar(t, false, members...), 0o600))
	dest := t.TempDir()
	err := svc.ExtractAs(context.Background(), archive, "many.tar", dest, "", nil)
	require.ErrorIs(t, err, ErrLimits)
	assert.LessOrEqual(t, len(treeFiles(t, dest)), 3)
}

// The TAR family needs no external program: a server without 7-Zip (the slim
// image, a desktop install) lists and extracts .tar, .tar.gz and .tar.bz2.
func TestTarFamilyWorksWithoutSevenZip(t *testing.T) {
	svc := New(memorySettings{}, Config{SevenZipBin: filepath.Join(t.TempDir(), "no-7zip-here"), WorkDir: t.TempDir()})
	archive := filepath.Join(t.TempDir(), "upload.tmp")
	require.NoError(t, os.WriteFile(archive, buildTar(t, true,
		tarMember{name: "docs/", typeflag: tar.TypeDir},
		tarMember{name: "docs/readme.txt", typeflag: tar.TypeReg, body: "hello from a tarball"},
	), 0o600))

	entries, err := svc.ListAs(context.Background(), archive, "bundle.tgz", "")
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"docs", "docs/readme.txt"}, entryNames(entries))
	for _, entry := range entries {
		assert.Equal(t, entry.Name == "docs", entry.IsDir, entry.Name)
	}

	dest := t.TempDir()
	require.NoError(t, svc.ExtractAs(context.Background(), archive, "bundle.tgz", dest, "", nil))
	got, err := os.ReadFile(filepath.Join(dest, "docs", "readme.txt"))
	require.NoError(t, err)
	assert.Equal(t, "hello from a tarball", string(got))
}

// ── the formats only 7-Zip reads ──────────────────────────────────────────

// A RAR5 member may carry no size at all (the "unknown size" flag `rar -si`
// writes): 7-Zip lists `Size = ` empty and decodes it to the end of its data.
// The listing read that as 0, so the preflight passed a member it could not
// bound. And a RAR5 "file copy" is a link of a third kind (`Copy Link`), next
// to Symbolic Link and Hard Link. The listings are 7-Zip 26.01's real output
// for hand-made RAR5 archives.
func TestPreflightRefusesRAR5MembersWithoutASizeAndCopyLinks(t *testing.T) {
	policy := Policy{MaxEntries: 100, MaxExpandedBytes: 1 << 30}
	read := func(file string) map[string]Entry {
		raw, err := os.ReadFile(filepath.Join("testdata", "slt", file+".slt"))
		require.NoError(t, err)
		byName := map[string]Entry{}
		for _, entry := range parseTechnicalList(string(raw), "/w/"+file) {
			byName[entry.Name] = entry
		}
		return byName
	}
	entries := func(m map[string]Entry) []Entry {
		out := make([]Entry, 0, len(m))
		for _, e := range m {
			out = append(out, e)
		}
		return out
	}

	known := read("rar5-known-size.rar")
	require.Contains(t, known, "payload.bin")
	assert.False(t, known["payload.bin"].SizeUnknown)
	assert.Equal(t, int64(4<<20), known["payload.bin"].Size)
	assert.NoError(t, Preflight(entries(known), policy))

	unknown := read("rar5-unknown-size.rar")
	require.Contains(t, unknown, "payload.bin")
	assert.True(t, unknown["payload.bin"].SizeUnknown, "7-Zip lists no size for it")
	assert.ErrorIs(t, Preflight(entries(unknown), policy), ErrUnsupported)

	copies := read("rar5-copy.rar")
	require.Contains(t, copies, "copy.bin")
	assert.True(t, copies["copy.bin"].IsLink, "a file copy is a link")
	assert.False(t, copies["payload.bin"].IsLink)
	assert.ErrorIs(t, Preflight(entries(copies), policy), ErrUnsupported)
}

// Alpine's `7zip` package — the one the official image installs — is built
// without the RAR handlers (the unRAR licence): `7zz -trar5` answers
// "Unsupported archive type", yet #48 listed RAR among the formats 7-Zip
// extracts. RAR is offered, and 7-Zip is run for it, only when the 7-Zip in
// use lists the handler.
func TestRAROnlyWithASevenZipThatHasIt(t *testing.T) {
	ctx := context.Background()
	svc, calls := recordSevenZip(t, nil)
	rar := filepath.Join(t.TempDir(), "upload.tmp")
	require.NoError(t, os.WriteFile(rar, append([]byte("Rar!"), 0x1a, 0x07, 0x01, 0x00, 'x'), 0o600))

	_, err := svc.ListAs(ctx, rar, "photos.rar", "")
	assert.ErrorIs(t, err, ErrUnsupported)
	assert.Empty(t, calls(), "7-Zip was run for a format it cannot read")
	assert.NotContains(t, svc.Providers(ctx)[1].ExtractFormats, "rar")

	t.Setenv(testSevenZipRAREnv, "1")
	svc.forgetSevenZip()
	_, _ = svc.ListAs(ctx, rar, "photos.rar", "")
	got := calls()
	require.Len(t, got, 1)
	assert.Contains(t, got[0].Args, "-trar5")
	assert.Contains(t, svc.Providers(ctx)[1].ExtractFormats, "rar")
}

// sevenZipNumber is the 7z header's variable-length integer.
func sevenZipNumber(v uint64) []byte {
	first, mask := byte(0), byte(0x80)
	i := 0
	for ; i < 8; i++ {
		if v < uint64(1)<<(7*(i+1)) {
			first |= byte(v >> (8 * i))
			break
		}
		first |= mask
		mask >>= 1
	}
	out := []byte{first}
	for ; i > 0; i-- {
		out = append(out, byte(v))
		v >>= 8
	}
	return out
}

// lyingSevenZip makes a real .7z of size zero bytes with an uncompressed
// header, then rewrites the member's unpack size to 100 and fixes the CRCs.
func lyingSevenZip(t *testing.T, bin string, size int) []byte {
	t.Helper()
	src := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(src, "payload.bin"), make([]byte, size), 0o600))
	out := filepath.Join(t.TempDir(), "z.7z")
	cmd := exec.Command(bin, "a", "-t7z", "-mhc=off", "-m0=LZMA", "-mx=1", "-ms=off", "-bso0", out, "payload.bin")
	cmd.Dir = src
	msg, err := cmd.CombinedOutput()
	require.NoError(t, err, string(msg))
	raw, err := os.ReadFile(out)
	require.NoError(t, err)
	nextOffset := binary.LittleEndian.Uint64(raw[12:20])
	nextSize := binary.LittleEndian.Uint64(raw[20:28])
	start := 32 + nextOffset
	header := raw[start : start+nextSize]
	needle := append([]byte{0x0c}, sevenZipNumber(uint64(size))...)
	require.Equal(t, 1, bytes.Count(header, needle), "the unpack size is where the format puts it")
	header = bytes.Replace(header, needle, append([]byte{0x0c}, sevenZipNumber(100)...), 1)
	startHeader := make([]byte, 20)
	binary.LittleEndian.PutUint64(startHeader[0:8], nextOffset)
	binary.LittleEndian.PutUint64(startHeader[8:16], uint64(len(header)))
	binary.LittleEndian.PutUint32(startHeader[16:20], crc32.ChecksumIEEE(header))
	patched := append([]byte{}, raw[:8]...)
	patched = binary.LittleEndian.AppendUint32(patched, crc32.ChecksumIEEE(startHeader))
	patched = append(patched, startHeader...)
	patched = append(patched, raw[32:start]...)
	return append(patched, header...)
}

// Fix 3's premise, pinned with a real 7-Zip: its ZIP and 7z decoders stop at
// the size a member DECLARES, so the declared-size preflight bounds them — a
// member whose headers lie ends in a data error with no more than its
// declared 100 bytes on disk (measured: 2 GiB of zeros claiming 100 bytes, in
// both formats). Should a 7-Zip ever decode past it, this turns red.
func TestALyingMemberStopsAtItsDeclaredSize(t *testing.T) {
	bin := supportedSevenZip(t)
	ctx := context.Background()
	svc := New(memorySettings{SettingMaxExpandedBytes: strconv.Itoa(1 << 20)}, Config{SevenZipBin: bin})
	for name, raw := range map[string][]byte{
		"lying.zip": archivetest.LyingZip(t, 8<<20),
		"lying.7z":  lyingSevenZip(t, bin, 8<<20),
	} {
		archive := filepath.Join(t.TempDir(), "upload.tmp")
		require.NoError(t, os.WriteFile(archive, raw, 0o600))
		entries, err := svc.ListAs(ctx, archive, name, "")
		require.NoError(t, err, name)
		require.Len(t, entries, 1, name)
		assert.Equal(t, int64(100), entries[0].Size, "%s: the member claims 100 bytes", name)
		dest := t.TempDir()
		assert.Error(t, svc.ExtractAs(ctx, archive, name, dest, "", nil), name)
		assert.LessOrEqual(t, treeBytes(t, dest), int64(100), "%s: 7-Zip wrote past the declared size", name)
	}
}
