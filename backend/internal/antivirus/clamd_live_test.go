package antivirus_test

// Live tests against a REAL clamd.
//
// ⚠⚠ A mock clamd agrees with whatever this code believes the protocol to be,
// which is the belief under test — so the mock in clamd_test.go covers edge
// cases and this file is the evidence that daemon mode actually works. Both
// directions are proved with a real EICAR string against a real signature
// database: a scanner is only interesting if it says "clean" to one file and
// "infected" to another, and a test that only ever checks the clean direction
// passes just as happily against a scanner that is switched off.
//
// Run them by pointing the env vars at a daemon:
//
//	docker run -d --name clamd -p 33100:3310 \
//	  -v /tmp/filex-clamd:/tmp clamav/clamav:latest
//	FILEX_TEST_CLAMD_ADDR=127.0.0.1:33100 \
//	FILEX_TEST_CLAMD_SOCKET=/tmp/filex-clamd/clamd.sock \
//	  go test ./internal/antivirus/ -run Live -v
//
// Unset variables skip, so the normal suite needs no container.

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/rand"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/antivirus"
)

// eicar is the standard antivirus test string, split so this source file is
// not itself flagged by the scanner reading the repository.
const eicar = `X5O!P%@AP[4\PZX54(P^)7CC)7}$` + `EICAR-STANDARD-ANTIVIRUS-TEST-FILE!$H+H*`

func liveScanner(t *testing.T, envVar string) *antivirus.Scanner {
	t.Helper()
	addr := os.Getenv(envVar)
	if addr == "" {
		t.Skipf("%s not set; start clamav/clamav and point it here", envVar)
	}
	sc, err := antivirus.NewWithDaemon(addr)
	require.NoError(t, err)
	sc.SetTimeout(60 * time.Second)
	// ⚠ A fresh clamav container refuses commands for the first minute or two
	// while it loads its signature database. Waiting for PONG here keeps that
	// from looking like a protocol bug.
	deadline := time.Now().Add(90 * time.Second)
	for {
		if err := sc.Health(context.Background()); err == nil {
			return sc
		} else if time.Now().After(deadline) {
			t.Fatalf("clamd at %s never answered PING: %v", addr, err)
		}
		time.Sleep(2 * time.Second)
	}
}

func runLivePair(t *testing.T, sc *antivirus.Scanner) {
	t.Helper()
	ctx := context.Background()

	infected, sig, err := sc.Scan(ctx, strings.NewReader("a perfectly ordinary sentence"))
	require.NoError(t, err)
	assert.False(t, infected, "clean file must scan clean")
	assert.Equal(t, "", sig)

	infected, sig, err = sc.Scan(ctx, strings.NewReader(eicar))
	require.NoError(t, err)
	assert.True(t, infected, "EICAR must be detected")
	assert.NotEmpty(t, sig)
	assert.NotEqual(t, "unknown", sig, "the signature name must survive the reply parser")
	t.Logf("clamd reported signature %q via %s", sig, sc.Address())

	// Multi-chunk, both directions. One INSTREAM chunk is 32 KiB, so a
	// half-megabyte stream is ~16 of them plus the zero-length terminator; a
	// wrong terminator shows up here as a timeout rather than a verdict.
	clean := strings.Repeat("ordinary sentence. ", 30000) // ~570 KB
	infected, sig, err = sc.Scan(ctx, strings.NewReader(clean))
	require.NoError(t, err)
	assert.False(t, infected, "multi-chunk clean stream must come back clean")

	// ⚠ EICAR only matches when it is the WHOLE file (the specification caps
	// it at 128 bytes), so padding it does not produce a big infected file —
	// an early version of this test asserted exactly that and failed for a
	// reason that had nothing to do with the chunk loop. A ZIP that contains
	// it does: clamd unpacks the archive, and the archive is comfortably
	// larger than one chunk because the filler is stored uncompressed.
	infected, sig, err = sc.Scan(ctx, bytes.NewReader(zippedEicar(t)))
	require.NoError(t, err)
	assert.True(t, infected, "multi-chunk infected stream must still be detected")
	assert.NotEmpty(t, sig)
	t.Logf("multi-chunk archive signature: %q", sig)
}

// zippedEicar builds a >32 KiB ZIP holding the EICAR file next to
// incompressible filler, so the stream spans several INSTREAM chunks.
func zippedEicar(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, err := zw.Create("eicar.com")
	require.NoError(t, err)
	_, err = w.Write([]byte(eicar))
	require.NoError(t, err)
	// Stored (not deflated) random bytes: compressible filler would collapse
	// and the archive would fit in one chunk, quietly voiding the point.
	fw, err := zw.CreateHeader(&zip.FileHeader{Name: "filler.bin", Method: zip.Store})
	require.NoError(t, err)
	filler := make([]byte, 256<<10)
	_, err = rand.Read(filler)
	require.NoError(t, err)
	_, err = fw.Write(filler)
	require.NoError(t, err)
	require.NoError(t, zw.Close())
	require.Greater(t, buf.Len(), 32<<10, "the archive has to span more than one chunk")
	return buf.Bytes()
}

func TestLiveClamd_TCP(t *testing.T) {
	sc := liveScanner(t, "FILEX_TEST_CLAMD_ADDR")
	assert.Equal(t, antivirus.ModeDaemon, sc.Mode())
	assert.Equal(t, "clamd", sc.BinName())
	v, err := sc.Version(context.Background())
	require.NoError(t, err)
	assert.Contains(t, v, "ClamAV")
	t.Logf("daemon version: %s", v)
	runLivePair(t, sc)
}

func TestLiveClamd_UnixSocket(t *testing.T) {
	sc := liveScanner(t, "FILEX_TEST_CLAMD_SOCKET")
	assert.Equal(t, antivirus.ModeDaemon, sc.Mode())
	runLivePair(t, sc)
}

// The whole configuration path, not just the transport: a settings table that
// selects daemon mode and names the address must produce a scanner that
// detects EICAR. This is what an admin actually does on the Protection page.
func TestLiveClamd_ResolvedFromSettings(t *testing.T) {
	addr := os.Getenv("FILEX_TEST_CLAMD_ADDR")
	if addr == "" {
		t.Skip("FILEX_TEST_CLAMD_ADDR not set")
	}
	// ⚠ FILEX_CLAMAV=0 in the environment must NOT veto the stored switch:
	// the variable is a seed, and the row is what is in force.
	t.Setenv("FILEX_CLAMAV", "0")
	store := settingsMap{
		antivirus.EnabledSetting.Key: "true",
		antivirus.ModeSetting.Key:    antivirus.ModeDaemon,
		antivirus.AddrSetting.Key:    addr,
	}
	sc := antivirus.New(context.Background(), store)
	require.True(t, sc.Supports())
	require.NoError(t, sc.Health(context.Background()))

	infected, sig, err := sc.Scan(context.Background(), strings.NewReader(eicar))
	require.NoError(t, err)
	assert.True(t, infected)
	assert.NotEmpty(t, sig)
}
