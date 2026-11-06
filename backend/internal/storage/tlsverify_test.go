package storage

import (
	"crypto/tls"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func skippingVerify(t *testing.T) {
	t.Helper()
	SetSkipTLSVerify(true)
	t.Cleanup(func() { SetSkipTLSVerify(false) })
}

// Off is the default, and off must change nothing: an http.Transport treats a
// nil TLSClientConfig and an empty one differently.
func TestApplyTLSVerify_OffLeavesTheConfigurationAlone(t *testing.T) {
	assert.Nil(t, ApplyTLSVerify(nil))
	base := &tls.Config{ServerName: "s3.example.com"}
	assert.Same(t, base, ApplyTLSVerify(base))
	assert.False(t, base.InsecureSkipVerify)
}

func TestApplyTLSVerify_OnAcceptsAnyCertificateOnACopy(t *testing.T) {
	skippingVerify(t)
	base := &tls.Config{ServerName: "s3.example.com", MinVersion: tls.VersionTLS13}

	got := ApplyTLSVerify(base)

	require.NotSame(t, base, got)
	assert.True(t, got.InsecureSkipVerify)
	assert.Equal(t, "s3.example.com", got.ServerName, "the server still has to be told its name")
	assert.Equal(t, uint16(tls.VersionTLS13), got.MinVersion)
	assert.False(t, base.InsecureSkipVerify, "base may be shared")
}

func TestApplyTLSVerify_OnWithoutABaseStaysOnModernTLS(t *testing.T) {
	skippingVerify(t)

	got := ApplyTLSVerify(nil)

	require.NotNil(t, got)
	assert.True(t, got.InsecureSkipVerify)
	assert.Equal(t, uint16(tls.VersionTLS12), got.MinVersion)
}
