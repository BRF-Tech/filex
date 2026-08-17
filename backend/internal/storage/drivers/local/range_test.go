package local

// ReadRange, measured in bytes rather than in "it returned something".
// A driver that quietly restarts at offset 0 hands http.ServeContent the
// wrong window and the client saves a corrupt file, so every case here
// compares the exact slice the contract promises.

import (
	"context"
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/storage"
)

// payload is 1000 bytes of "0123456789" repeated, so any offset error
// shows up as a shifted pattern instead of a plausible-looking blob.
const payloadLen = 1000

func seedPayload(t *testing.T, d *Driver) string {
	t.Helper()
	body := strings.Repeat("0123456789", payloadLen/10)
	require.NoError(t, d.Write(context.Background(), "big.bin", strings.NewReader(body), int64(len(body))))
	return body
}

func readAllRange(t *testing.T, d *Driver, off, length int64) []byte {
	t.Helper()
	rc, err := d.ReadRange(context.Background(), "big.bin", off, length)
	require.NoError(t, err)
	defer rc.Close()
	b, err := io.ReadAll(rc)
	require.NoError(t, err)
	return b
}

func TestReadRange_OffsetAndLength(t *testing.T) {
	d := newDriver(t)
	body := seedPayload(t, d)

	got := readAllRange(t, d, 100, 100)
	require.Len(t, got, 100)
	require.Equal(t, body[100:200], string(got))
}

func TestReadRange_LengthNegativeReadsToEnd(t *testing.T) {
	d := newDriver(t)
	body := seedPayload(t, d)

	got := readAllRange(t, d, 940, -1)
	require.Equal(t, body[940:], string(got))
	require.Len(t, got, 60)
}

func TestReadRange_LengthBeyondEOFIsClamped(t *testing.T) {
	d := newDriver(t)
	body := seedPayload(t, d)

	got := readAllRange(t, d, 900, 5000)
	require.Equal(t, body[900:], string(got), "asking past the end must short-read, not error")
}

func TestReadRange_OffsetAtOrPastEOFIsEmptyNotError(t *testing.T) {
	d := newDriver(t)
	seedPayload(t, d)

	for _, off := range []int64{payloadLen, payloadLen + 1, payloadLen * 10} {
		rc, err := d.ReadRange(context.Background(), "big.bin", off, -1)
		require.NoError(t, err, "offset %d past EOF must not be an error", off)
		b, err := io.ReadAll(rc)
		require.NoError(t, err)
		require.NoError(t, rc.Close())
		require.Empty(t, b, "offset %d past EOF must read empty", off)
	}
}

func TestReadRange_ZeroLengthTouchesNothing(t *testing.T) {
	d := newDriver(t)
	seedPayload(t, d)

	got := readAllRange(t, d, 10, 0)
	require.Empty(t, got)
}

func TestReadRange_NegativeOffsetIsAnError(t *testing.T) {
	d := newDriver(t)
	seedPayload(t, d)

	_, err := d.ReadRange(context.Background(), "big.bin", -5, 10)
	require.Error(t, err, "suffix ranges are the caller's job — the driver must refuse")
}

func TestReadRange_MissingPathIsErrNotFound(t *testing.T) {
	d := newDriver(t)

	_, err := d.ReadRange(context.Background(), "nope.bin", 0, 10)
	require.ErrorIs(t, err, storage.ErrNotFound)
}

func TestReadRange_AdvertisedAsACapability(t *testing.T) {
	d := newDriver(t)
	require.True(t, storage.ComputeCapabilities(d).Range)
	var _ storage.RangeReader = d
}
