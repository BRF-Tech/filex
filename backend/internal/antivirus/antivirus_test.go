package antivirus_test

// Scanner tests use fake shell scripts standing in for clamscan so the
// suite runs without ClamAV installed: exit 0 = clean, exit 1 + a
// "<path>: <SIG> FOUND" line = infected, exit 2 = scan error.

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/antivirus"
)

// writeFakeClam drops an executable shell script into a temp dir and
// returns its path. Skips on Windows — the suite runs under WSL/Linux.
func writeFakeClam(t *testing.T, name, body string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("fake clamav scripts need a POSIX shell")
	}
	p := filepath.Join(t.TempDir(), name)
	require.NoError(t, os.WriteFile(p, []byte("#!/bin/sh\n"+body), 0o755))
	return p
}

func TestAntivirusResolveBin_KillSwitch(t *testing.T) {
	t.Setenv("FILEX_CLAMAV", "0")
	t.Setenv("FILEX_CLAMAV_BIN", "/bin/sh") // even a valid bin is ignored
	assert.Equal(t, "", antivirus.ResolveBin())
	assert.False(t, antivirus.New().Supports())
}

func TestAntivirusResolveBin_ExplicitBinAuthoritative(t *testing.T) {
	fake := writeFakeClam(t, "clamscan", "exit 0\n")
	t.Setenv("FILEX_CLAMAV", "")
	t.Setenv("FILEX_CLAMAV_BIN", fake)
	got := antivirus.ResolveBin()
	assert.Equal(t, fake, got)

	// Invalid explicit value disables instead of falling back to $PATH.
	t.Setenv("FILEX_CLAMAV_BIN", filepath.Join(t.TempDir(), "missing-bin"))
	assert.Equal(t, "", antivirus.ResolveBin())
}

func TestAntivirusResolveBin_PathAbsent(t *testing.T) {
	t.Setenv("FILEX_CLAMAV", "")
	t.Setenv("FILEX_CLAMAV_BIN", "")
	t.Setenv("PATH", t.TempDir()) // empty dir → no clamdscan/clamscan
	assert.Equal(t, "", antivirus.ResolveBin())
	sc := antivirus.New()
	assert.False(t, sc.Supports())
	assert.Equal(t, "", sc.BinName())

	// Supports=false → Scan refuses with ErrUnavailable.
	_, _, err := sc.Scan(context.Background(), strings.NewReader("data"))
	assert.ErrorIs(t, err, antivirus.ErrUnavailable)
}

func TestAntivirusResolveBin_PathPrefersClamdscan(t *testing.T) {
	dir := t.TempDir()
	if runtime.GOOS == "windows" {
		t.Skip("fake clamav scripts need a POSIX shell")
	}
	for _, name := range []string{"clamdscan", "clamscan"} {
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\nexit 0\n"), 0o755))
	}
	t.Setenv("FILEX_CLAMAV", "")
	t.Setenv("FILEX_CLAMAV_BIN", "")
	t.Setenv("PATH", dir)
	assert.Equal(t, filepath.Join(dir, "clamdscan"), antivirus.ResolveBin())
	assert.Equal(t, "clamdscan", antivirus.New().BinName())
}

func TestAntivirusScan_Clean(t *testing.T) {
	fake := writeFakeClam(t, "clamscan", "exit 0\n")
	sc := antivirus.NewWithBin(fake)
	require.True(t, sc.Supports())

	infected, sig, err := sc.Scan(context.Background(), strings.NewReader("harmless bytes"))
	require.NoError(t, err)
	assert.False(t, infected)
	assert.Equal(t, "", sig)
}

func TestAntivirusScan_Infected(t *testing.T) {
	fake := writeFakeClam(t, "clamscan",
		`echo "$0: Eicar-Signature FOUND"`+"\nexit 1\n")
	sc := antivirus.NewWithBin(fake)

	infected, sig, err := sc.Scan(context.Background(), strings.NewReader("X5O!virus"))
	require.NoError(t, err)
	assert.True(t, infected)
	assert.Equal(t, "Eicar-Signature", sig)
}

func TestAntivirusScan_Error(t *testing.T) {
	fake := writeFakeClam(t, "clamscan",
		`echo "ERROR: Can't access database" >&2`+"\nexit 2\n")
	sc := antivirus.NewWithBin(fake)

	infected, _, err := sc.Scan(context.Background(), strings.NewReader("data"))
	require.Error(t, err)
	assert.False(t, infected)
	assert.NotErrorIs(t, err, antivirus.ErrUnavailable)
	assert.Contains(t, err.Error(), "failed")
}

func TestAntivirusScan_Timeout(t *testing.T) {
	fake := writeFakeClam(t, "clamscan", "sleep 5\nexit 0\n")
	sc := antivirus.NewWithBin(fake)
	sc.SetTimeout(200 * time.Millisecond)

	_, _, err := sc.Scan(context.Background(), strings.NewReader("data"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "timed out")
}

func TestAntivirusMaxScanBytes(t *testing.T) {
	t.Setenv("FILEX_CLAMAV_MAX", "")
	assert.Equal(t, antivirus.DefaultMaxScanBytes, antivirus.MaxScanBytes())
	t.Setenv("FILEX_CLAMAV_MAX", "1048576")
	assert.EqualValues(t, 1048576, antivirus.MaxScanBytes())
	t.Setenv("FILEX_CLAMAV_MAX", "not-a-number")
	assert.Equal(t, antivirus.DefaultMaxScanBytes, antivirus.MaxScanBytes())
}
