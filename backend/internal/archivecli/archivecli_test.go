package archivecli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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
		"-psecret", "-mhe=on", "/tmp/output.7z", ".",
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

func TestCompressedTarDetectionAndDiscovery(t *testing.T) {
	assert.True(t, isCompressedTar("backup.TAR.GZ"))
	assert.True(t, isCompressedTar("backup.tgz"))
	assert.False(t, isCompressedTar("backup.gz"))

	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "backup.tar"), []byte("tar"), 0o600))
	got, err := findTar(root)
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(root, "backup.tar"), got)
}

func TestSevenZipEncryptedRoundTrip(t *testing.T) {
	bin, err := exec.LookPath("7zz")
	if err != nil {
		bin, err = exec.LookPath("7z")
	}
	if err != nil {
		t.Skip("7-Zip is not installed")
	}

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
	bin, err := exec.LookPath("7zz")
	if err != nil {
		bin, err = exec.LookPath("7z")
	}
	if err != nil {
		t.Skip("7-Zip is not installed")
	}

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
	bin, err := exec.LookPath("7zz")
	if err != nil {
		bin, err = exec.LookPath("7z")
	}
	if err != nil {
		t.Skip("7-Zip is not installed")
	}

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

func entryNames(entries []Entry) []string {
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name)
	}
	return names
}
